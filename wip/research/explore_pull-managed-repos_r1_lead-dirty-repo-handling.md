# Lead: How should dirty repos and non-default branches be handled?

## Findings

### Current State Detection in niwa

**Implemented:**
- `CheckUncommittedChanges(instanceDir)` in `destroy.go`: runs `git status --porcelain` per repo to detect dirty working trees (used before destructive operations)
- `SyncConfigDir(allowDirty bool)` in `configsync.go`: detects dirty config directory and optionally allows apply with local mods via `--allow-dirty` flag
- Clone logic handles branches via `CloneWithBranch(branch string)` and respects repo-level overrides in config: `[repos.X] branch = "..."`
- Instance state tracks which repos are cloned (`Cloned: bool`) but does NOT track: current branch, ahead/behind status, or working tree cleanliness

**Not Implemented:**
- Detection of current branch (only cloning branches)
- Remote tracking: checking if local is ahead/behind/diverged from origin
- Per-repo pull/sync operations
- Handling of merge/rebase conflicts on pull

### Full State Space Matrix

For each managed repo, the relevant dimensions are:

| Working Tree | Branch | Remote Status | Safe to Pull? | Default Behavior | Notes |
|---|---|---|---|---|---|
| **Clean** | default | up-to-date | YES | Pull (no-op) | No risk; pull can proceed |
| Clean | default | behind | YES | Pull (ff-only) | Fetch latest; fastest path to current |
| Clean | default | ahead | NO | Warn, skip | Local unpushed commits; risky to pull |
| Clean | default | diverged | NO | Warn, skip | Merge/rebase needed; risky default |
| Clean | other | behind | MAYBE | Skip (warn) | Non-default branch, risky context switch |
| Clean | other | ahead | NO | Skip (warn) | Non-default branch, unpushed work |
| Clean | other | diverged | NO | Skip (warn) | Non-default branch, conflicted |
| **Dirty** | default | behind | NO | Skip (warn) | Stash or fail? Data-loss risk |
| Dirty | default | ahead | NO | Skip (warn) | Unpushed + uncommitted; high risk |
| Dirty | default | diverged | NO | Skip (warn) | Merge/rebase with uncommitted; high risk |
| Dirty | other | any | NO | Skip (warn) | Multiple risk factors |
| **Staged** | default | behind | NO | Warn, skip | Staged != committed; risky to pull |
| Staged | default | any | NO | Skip (warn) | Staged changes present |
| **Untracked** | any | any | MAYBE | Pull anyway | Untracked files don't block pull |

### Proposed State Detection

To support the above matrix, niwa needs to detect:

1. **Working tree state:**
   - Fully clean: `git status --porcelain` returns empty
   - Staged changes: `git status --porcelain | grep '^[A-Z] '`
   - Unstaged changes: `git status --porcelain | grep '^ [A-Z]'`
   - Untracked files: `git status --porcelain | grep '^\?'`

2. **Current branch:**
   - `git rev-parse --abbrev-ref HEAD` (returns branch name or "HEAD" for detached)
   - Compare against config's `default_branch` (default: "main")

3. **Remote tracking status:**
   - `git fetch origin` (required to check remote; no-op if up-to-date)
   - `git rev-list --count --left-right @{u}...HEAD` (returns left-count and right-count)
     - Left = commits ahead (local unique)
     - Right = commits behind (remote unique)
   - Both zero: up-to-date
   - Only right: behind (safe to ff-merge)
   - Only left: ahead (local unpushed)
   - Both non-zero: diverged

### Proposed Default Behavior

**For each repo during `niwa apply`:**

1. **If working tree is staged or unstaged dirty:**
   - SKIP the repo with a warning
   - Reason: safety; uncommitted work is explicit and intentional, force-pulling would lose it

2. **If on non-default branch (regardless of status):**
   - SKIP the repo with a warning
   - Reason: non-default branches are intentional working contexts; switching them unexpectedly can confuse workflow
   - Exception: if repo is clean AND at default branch, could consider pulling, but skip is safer

3. **If working tree is clean AND on default branch:**
   - **Behind remote:** Pull with `git pull --ff-only origin <default_branch>`
     - Safe: ff-only prevents merge commits; if it fails, repo is unchanged
   - **At/ahead of remote:** Skip with info message
     - Reason: might be intentional (local-only work or awaiting review); don't force-push
   - **Diverged:** Warn and skip
     - Reason: would require merge or rebase; too risky without explicit intent

4. **If working tree has untracked files only:**
   - Proceed with pull (untracked files don't block `git pull`)

### Proposed Override Flags & Config

For `niwa apply`:

```bash
# Default: skip dirty repos, skip non-default branches, pull where safe
niwa apply

# --skip-dirty: explicitly skip any repo with uncommitted changes (safer)
niwa apply --skip-dirty

# --pull: aggressive pull; attempt to clean/switch branches before pulling
# (dangerous; needs sub-flags)
niwa apply --pull
niwa apply --pull=stash-and-pull  # stash dirty, switch branch, pull
niwa apply --pull=fail-on-dirty   # fail if any repo is dirty (safer)
niwa apply --pull=force-switch    # switch to default branch, pull (risky)

# Per-repo config override in workspace.toml
[repos.vision]
pull_on_apply = true   # Always try to pull this repo
branch = "main"        # Explicitly pin this repo to "main" during pull
```

### Implications for apply Pipeline

**Current flow:**
1. Discover repos from config
2. Classify into groups
3. For each repo: clone if missing, skip if exists
4. Install content

**With pull support:**
1. Discover repos from config
2. Classify into groups
3. For each repo:
   - If not cloned: clone
   - If cloned:
     - Check state (dirty, branch, remote status)
     - Based on config + flags, pull or skip
     - Log actions/warnings
4. Install content

**New responsibilities:**
- State detection function: `RepoState CheckRepoState(repoDir string, defaultBranch string) error`
  - Returns: { IsClean, CurrentBranch, IsAheadOfRemote, IsBehindRemote, HasStagedChanges, ... }
- Pull decision function: `PullDecision DecidePull(cfg, repoState, flags) (shouldPull, shouldStash, shouldSwitch bool, err error)`
- Pull execution with fallback: handle ff-only failure gracefully

## Implications

### UX Impact

1. **Safer by default:** Skipping dirty repos aligns with niwa's non-destructive philosophy. Users expect `niwa apply` to be idempotent and safe.

2. **Explicit intent for risky operations:** Pulling with stashing or branch-switching requires opt-in via flags or config. Default is conservative.

3. **Workflow clarity:** Status output (from status command or apply output) makes it clear why repos were skipped, reducing surprise.

4. **Per-repo config enables gradual adoption:** Teams can set `pull_on_apply = true` for repos where auto-pull is safe (e.g., read-only clones), and keep manual control for active development repos.

### Implementation Complexity

- **Moderate:** State detection is straightforward git commands; decision logic is a state machine with fallbacks.
- **Test coverage:** Each state combination needs a test case (matrix: 3 dirty states × 2 branches × 3 remote statuses ≈ 18 cases).
- **Config schema:** Extend `RepoOverride` in workspace config with pull-related fields.

### Interaction with Other Features

- **Workspace TOML syncing:** Apply already discovers added/removed repos. Pull would operate within the current repo set. Removed repos are cleaned up separately.
- **Instance state:** Could track last-pull timestamp, pull success/failure, to improve status messages. Not required for v1 but useful later.
- **Reset command:** Reset already destroys and recreates, so pull is not needed during reset.

## Surprises

1. **Branch awareness was incomplete:** Config schema already has per-repo `branch` override (for cloning), but current clone logic doesn't verify branch stays correct on subsequent applies. Pull logic must check/enforce default branch.

2. **Divergence requires merge/rebase:** Unlike simple "behind" cases, diverged branches can't be fixed by pull alone without choosing a merge strategy. This is an edge case but more common than expected (parallel local + remote work).

3. **Untracked files are harmless:** Many developers assume untracked files block pulls; they don't. This might be worth explaining in docs/output to reduce confusion.

4. **Pull --ff-only is not universal:** Not all repos can/should use ff-only. Some workflows require explicit merges (documented in git config). The safe default for niwa is ff-only; teams wanting merge commits need a per-repo override.

## Open Questions

1. **Stashing dirty changes:** Should `--pull=stash-and-pull` actually stash, or just fail with a message? Stashing is risky if the user forgets the stash; fails are explicit but require manual recovery. Recommend: fail by default, stash only with explicit `--force-stash` flag.

2. **Detached HEAD state:** Should niwa handle repos in detached HEAD state? Currently clone can produce this (e.g., cloning a commit SHA). Decide: treat detached as "not on default branch" and skip, or detect it and offer to checkout.

3. **Shallow clones and pull:** Current clone supports `--depth` but pull from shallow clones has gotchas (may need `--unshallow`). Out of scope for v1; note for future.

4. **Per-branch pull strategy:** Some teams use rebase (linear history), others merge (explicit merge commits). Config should allow per-repo override of merge strategy. Defer to v2?

5. **Pull failure recovery:** If `git pull --ff-only` fails (non-ff history), should niwa suggest `git rebase origin/main` or offer a `--pull-rebase` flag? Recommend: fail with clear message, user decides strategy.

6. **Notification of skipped repos:** Should skipped repos appear in status, or only in apply output? Both? Recommend: apply output shows detailed reason; status command shows summary (e.g., "3 repos behind, 1 dirty").

## Summary

niwa should detect repo state (dirty/clean, branch, remote status) and skip pulling by default when repos have uncommitted changes or are on non-default branches, with explicit per-repo config and command-line flags for overrides. The state space has 18 meaningful combinations; safe default behavior pulls only clean repos on the default branch that are behind their remote (using `--ff-only`), skipping all others with clear warnings. Per-repo config in workspace.toml (e.g., `pull_on_apply = true`) enables gradual adoption without forcing all repos into the same policy.

