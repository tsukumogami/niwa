# Lead: How should workspace TOML drift be reconciled?

## Findings

### TOML Format: Repo Specification

The workspace TOML (at `.niwa/workspace.toml`) declares repos in two ways:

1. **Source-based discovery** (`[[sources]]`): GitHub orgs are queried via API, repos auto-discovered (threshold: 10 by default, configurable via `max_repos`). No explicit repo list needed for small orgs.

2. **Explicit repos** (`[repos.X]` with `url` and `group` fields): External repos (not from sources) can be declared with a full git URL and target group assignment. These inject into the classified list after discovery (Step 2.1 in the pipeline).

Additional configuration per repo:
- `branch`: default branch to clone (per-repo override, workspace default is `default_branch`)
- `claude`: boolean (skip Claude Code configuration if false)
- `scope`, `env`, `hooks`, `settings`: various overrides

**Key observation**: The TOML specifies *what repos should exist*, not *what state they should be in*. It's a declarative goal state, not a checkpoint of current clones.

### Current Apply Behavior: No Drift Detection for Repos

The `Applier.Apply()` pipeline (internal/workspace/apply.go) handles repos as follows:

**Discovery phase (Steps 1-2.5)**:
1. Query GitHub API for all sources to discover repos
2. Classify discovered repos into groups by metadata filters or explicit listing
3. Inject explicit repos (with `url+group`) into the classified list
4. Warn about unknown repo names in `[repos]` overrides

**Clone phase (Step 3)**:
```go
cloned, err := a.Cloner.CloneWithBranch(ctx, cloneURL, targetDir, branch)
if cloned {
    fmt.Printf("cloned %s into %s\n", ...)
} else {
    fmt.Printf("skipped %s (already exists)\n", ...)
}
```

The Cloner checks for `.git/` marker file. If it exists, the clone is skipped. **No verification of URL match, branch, or remote history.**

**Cleanup phase (after pipeline)**:
- `cleanRemovedFiles()`: Deletes managed files (CLAUDE.md, settings.json, etc.) that were in previous state but not in current pipeline result.
- `cleanRemovedGroupDirs()`: Removes empty group directories when groups are no longer classified.
- **But**: Cloned repos themselves are *never* deleted. `RepoState` tracks `{URL, Cloned}` but no per-repo cleanup logic exists.

**State tracking** (instance.json):
```json
{
  "repos": {
    "public/tsuku": {"url": "git@github.com:tsukumogami/tsuku.git", "cloned": true}
  }
}
```

Only `URL` and `Cloned` are recorded. No branch, no remote HEAD, no working tree state.

### Drift Scenarios and Current Behavior

#### Scenario A: New Repo Added to TOML

**TOML change**: Add `[[sources]]` org or declare `[repos.newrepo]` with `url+group`.

**Current behavior**: Apply discovers/injects the new repo, clones it, installs content. ✓ **Already handled.**

**State update**: `RepoState` added to `instance.json`.

#### Scenario B: Repo Removed from TOML

**TOML change**: Remove the source org or delete `[repos.X]` entry.

**Current behavior**: 
- If it was source-discovered, it won't be in the new classified list. Apply won't touch it.
- If it was explicit (url+group), same result.
- The old `RepoState` entry remains in `instance.json` forever.
- **The cloned directory remains on disk untouched.**

**Gap**: No mechanism to detect and clean up removed repos. Apply has drift detection for *managed files* (CLAUDE.md, settings.json) via hashes in state, but not for repos themselves.

**Implications for UX**: A developer removes a repo from TOML to "clean up the workspace," but the old clone persists on disk. They must manually `rm -rf group/repo` or re-initialize the instance. Apply gives no hint this happened.

#### Scenario C: Repo URL Changed in TOML

**TOML change**: Override URL in `[repos.tsuku] url = "git@github.com:neworg/tsuku.git"`.

**Current behavior**:
- The Cloner checks for `.git/` marker. It exists (from the old clone), so it skips the clone.
- Apply makes no attempt to verify the URL matches the recorded URL in state.
- **The old remote remains configured in the local clone.**

**If developer runs `git pull` in the stale clone**, they pull from the old org, not the new one. Silent mismatch.

**Gap**: No URL drift detection. The repo is still cloned but pointing to the wrong remote.

#### Scenario D: Repo Default Branch Changed in TOML

**TOML change**: Set `[repos.tsuku] branch = "develop"` (or workspace-level `default_branch` changed).

**Current behavior**:
- The Cloner passes the branch to `git clone --branch develop`. But the clone already exists, so the Cloner skips it.
- Apply makes no attempt to verify the current checkout matches the intended branch.

**If developer runs `git pull`, they pull their current branch, not the configured one.** Silent mismatch if they've manually checked out something else.

**Gap**: No branch drift detection.

#### Scenario E: Repo Config Changed (Path, Scope, etc.)

**TOML change**: Set `[repos.tsuku] scope = "strategic"` or move repo to a different group (changes directory).

**Current behavior**:
- Content files (CLAUDE.local.md) are regenerated in the new location.
- But the old content files are cleaned up only if they were in `managedFiles` from the previous apply.
- **If the repo directory moved between groups, the old clone directory is orphaned.**

For example, if a repo moves from `public/` to `private/` in the TOML:
1. Apply discovers it in the new group.
2. Clones to `private/tsuku/` (or skips if it already exists there).
3. Installs content to `private/tsuku/CLAUDE.local.md`.
4. The old `public/tsuku/` directory remains.

**Gap**: No directory migration logic. The repo can be in two places at once, or orphaned after a move.

### Apply Pipeline Flow (for reference)

```
Step 1: Discover repos from sources (GitHub API)
Step 2: Classify repos into groups (match filters or explicit lists)
Step 2.1: Inject explicit repos (url+group entries)
Step 2.5: Warn about unknown repo names
Step 3: Clone repos (skip if .git exists)
Step 4-6: Install content, materializers, setup scripts
Step 7: Build managed files with hashes
```

**After pipeline (Apply only)**:
- `cleanRemovedFiles()`: Delete managed files from previous state that aren't in current result.
- `cleanRemovedGroupDirs()`: Remove empty group directories.

**No repo-level cleanup or verification.**

### Proposed Default Behavior for Each Drift Scenario

#### Scenario A: New Repo Added (already works)

**Default**: Clone it.
**Override options**: None needed; cloning is fast and idempotent.

#### Scenario B: Repo Removed

**Default (safe)**: Warn but do not delete. Reason: the clone might have local work or history the user cares about. Deleting without warning is data loss.

**Override options**:
- `--clean-removed`: Delete repo directories for repos no longer in TOML.
- `--dry-run`: Show what would be deleted without doing it.

**Implementation**: After pipeline, diff previous `instance.json` repos list against current classified list. For missing repos, either warn or (if `--clean-removed`) delete.

#### Scenario C: Repo URL Changed

**Default (safe)**: Warn about the mismatch but don't change the remote. Reason: the user might have local changes or intentionally want to work against the old upstream for some reason.

**Override options**:
- `--update-remotes`: Run `git remote set-url origin <new-url>` for repos with URL drift.
- `--verify-remotes`: Only warn, don't fix (default behavior).

**Implementation**: Load state, compare URL from previous state vs. current TOML/discovery. If mismatch, warn with old and new URLs.

#### Scenario D: Repo Default Branch Changed

**Default (safe)**: Warn about the drift but don't change the checked-out branch. Reason: the user might have local work on a different branch.

**Override options**:
- `--update-branches`: Run `git checkout <new-branch>` for repos with branch drift (if clean).
- `--verify-branches`: Only warn.

**Implementation**: For each repo, check its current HEAD against the configured branch. If mismatch, warn. If `--update-branches` and no uncommitted changes, checkout the new branch.

#### Scenario E: Repo Directory/Group Changed

**Default (safe)**: This is a complex case. If a repo moves from one group to another:
1. Apply will try to clone to the new location (will skip if already exists there).
2. Content will be installed to the new location.
3. The old location is orphaned.

**Options**:
- **Option 1 (simple)**: Warn about the old location, suggest `niwa reset` to clean up.
- **Option 2 (automated)**: Detect the move (same repo name, different group path), delete the old location, clone to the new one if missing.

**Preference**: Option 1 for now (safer, explicit). Option 2 requires heuristics and can be added later if the scenario is common.

### State Tracking Enhancements Needed

The current `RepoState` has:
```json
{"url": "...", "cloned": true}
```

To detect drift, we'd need:
```json
{
  "url": "git@github.com:tsukumogami/tsuku.git",
  "branch": "main",
  "group": "public",
  "cloned": true,
  "last_verified": "2026-03-27T09:15:00Z"
}
```

This would enable:
- URL mismatch detection
- Branch drift detection
- Group/path changes
- Age-based warnings ("repo hasn't been verified in 30 days")

## Implications

1. **Apply is currently idempotent for cloning but not for configuration drift**: Running apply twice with the same TOML produces the same cloned repos (good), but if the TOML changes, configuration drift isn't detected or reconciled (bad).

2. **Removed repos are a silent problem**: Users can remove a repo from TOML expecting cleanup, but the clone remains on disk. This is confusing and wastes space.

3. **URL/branch changes are invisible unless the user manually checks remotes**: A misconfigured TOML won't be caught until the user tries to pull and realizes they're on the wrong remote.

4. **State tracking is minimal**: `instance.json` records whether a repo was cloned but not *how* it was cloned (URL, branch, group). This limits drift detection.

5. **The cleanup logic is partial**: Managed files are cleaned up when removed from config, but repos aren't. This asymmetry is confusing.

6. **Directory moves require manual intervention**: If a repo changes groups, the old clone isn't cleaned up automatically.

### For the Sync Feature

The "pull latest" feature (mentioned in the lead scope) is separate from TOML drift but related:
- TOML drift = configuration changes (repos added, removed, reconfigured)
- Pull latest = fetching new commits from remotes

Both need to be idempotent, non-destructive by default, and have override options for users who want aggressive cleanup or force updates.

## Surprises

1. **No per-repo cleanup logic exists**: Apply has `cleanRemovedFiles()` and `cleanRemovedGroupDirs()`, but no counterpart for repos. This was an intentional choice (documented in DESIGN-instance-lifecycle.md) to avoid data loss, but it means removed repos accumulate.

2. **URL is stored in state but never verified**: The state tracks the clone URL from the previous apply, but Apply never compares it against the current TOML to detect mismatches.

3. **RepoState is minimal by design**: It only tracks `{URL, Cloned}` to keep the schema simple. But this simplicity makes drift detection harder.

4. **The pipeline doesn't distinguish between "repo was just cloned" and "repo already existed"**: The RepoState is updated with `cloned || repoAlreadyCloned(targetDir)`, conflating fresh clones with existing clones. This loses information about what apply actually did.

## Open Questions

1. **Should removed repos ever be deleted automatically?** The safe default is to warn and require explicit opt-in (`--clean-removed`), but this might be overly conservative. Would a "backup before delete" approach be better? (Move to a `.trash/` subdirectory?)

2. **How aggressive should `--update-remotes` and `--update-branches` be?** Should they require a clean working tree, or should they bail out if there's uncommitted work? The safer option is stricter, but less convenient.

3. **Should we track the clone operation itself in state?** E.g., `"cloned_on": "2026-03-25T10:00:00Z"` and warn if a repo is stale (hasn't been cloned/pulled in 30 days)?

4. **How should the "sync" feature (pull latest) interact with TOML drift detection?** Should `niwa sync` handle both pulling remotes AND reconciling TOML changes, or should these be separate?

5. **What about repos cloned by the user manually (not via niwa)?** If a developer manually clones a repo into a group directory before running apply, should apply recognize it and track it in state, or should it error?

6. **Should there be a separate command for drift detection (`niwa status --drift`), or should it be baked into `niwa apply`?** Status could show drift without making changes, then apply could fix it.

## Summary

niwa's apply pipeline currently discovers and clones repos but provides no mechanism to detect configuration drift when the TOML changes. Removed repos are silently orphaned (not deleted), and URL/branch changes are undetected. The `RepoState` in instance.json tracks only `{URL, Cloned}`, lacking group membership and branch info needed for drift detection. The Apply command has cleanup logic for managed files but not repos, creating an asymmetry. To reconcile TOML drift safely, Apply should offer three levels of behavior: (1) warn-only (default, safe), (2) automated cleanup with confirmation, and (3) force mode for aggressive cleanup. This requires enhancing RepoState to include group and branch, adding drift detection logic after the pipeline, and providing clear CLI flags (`--clean-removed`, `--update-remotes`, `--update-branches`, `--verify-only`) to let users choose their risk tolerance.
