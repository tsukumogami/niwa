# Lead: How does niwa's current apply pipeline handle repos, and where would sync logic fit?

## Findings

### Core Apply Pipeline Flow

The apply pipeline is orchestrated by `Applier.Apply()` and `Applier.Create()` (in `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-3/public/niwa/internal/workspace/apply.go`), which share a common `runPipeline()` method with different entry/exit behavior.

**Pipeline sequence (lines 163-399):**
1. **Discover repos** from all sources (GitHub orgs or explicit lists) via `discoverAllRepos()`
2. **Classify repos** into groups based on config (visibility, explicit assignments)
3. **Clone repos** via `Cloner.CloneWithBranch()` -- one per classified repo
4. **Install workspace content** (CLAUDE.md at root level)
5. **Install workspace context and settings** at instance root
6. **Install group-level content** (CLAUDE.md per group)
7. **Install repo-level content** (CLAUDE.local.md per repo)
8. **Materialize hooks, settings, env** via materializer loop (Steps 6.5)
9. **Run setup scripts** from repos (Step 6.75)
10. **Hash managed files** for drift detection

### Clone Behavior: The "Skip Existing" Pattern

Clone handling is in `Cloner.CloneWithBranch()` and `Cloner.CloneWith()` (lines 31-78 of `clone.go`). The key behavior:

```go
// CloneWith clones a repo into targetDir with options.
// Returns true if clone was performed, false if directory already existed.
func (c *Cloner) CloneWith(ctx context.Context, url, targetDir string, opts CloneOptions) (bool, error) {
    if _, err := os.Stat(filepath.Join(targetDir, ".git")); err == nil {
        return false, nil  // Already exists, skip
    }
    // ... perform clone ...
    return true, nil
}
```

**In the apply pipeline (lines 206-215 of apply.go):**
```go
cloned, err := a.Cloner.CloneWithBranch(ctx, cloneURL, targetDir, branch)
if err != nil {
    return nil, fmt.Errorf("cloning repo %s: %w", cr.Repo.Name, err)
}
if cloned {
    fmt.Printf("cloned %s into %s\n", cr.Repo.Name, targetDir)
} else {
    fmt.Printf("skipped %s (already exists)\n", cr.Repo.Name)  // <-- Line 214
}
```

**This is the current limitation:** If a repo directory with `.git/` exists, the cloner returns false and prints "skipped". No pull, fetch, or branch checkout occurs. The repository on disk is not updated.

### State Management: What Gets Tracked

`RepoState` is defined in `state.go` (lines 48-51):
```go
type RepoState struct {
    URL    string `json:"url"`
    Cloned bool   `json:"cloned"`
}
```

This is persisted in `.niwa/instance.json` under the `Repos` map (line 37 of state.go). The state tracks:
- Which repos exist in the configuration
- Their clone URLs
- Whether they were cloned during apply

**Important:** The state does NOT track:
- Current branch or commit
- Whether the working tree is up-to-date
- Last fetch timestamp
- Divergence from remote

This limits the applier's ability to intelligently sync repos.

### Config Syncing: Already Exists for Workspace Config

There is ALREADY a sync mechanism for the workspace config itself in `configsync.go` (lines 11-49):

```go
// SyncConfigDir pulls the latest config from origin if the config directory
// is a git repo with a remote.
func SyncConfigDir(configDir string, allowDirty bool) error {
    // Check if it's a git repo
    // Check for origin remote
    // Check for dirty working tree (unless allowDirty=true)
    // Pull latest from origin with git pull --ff-only
}
```

This is called in the CLI layer before loading config (line 74 of `cli/apply.go`):
```go
if syncErr := workspace.SyncConfigDir(configDir, applyAllowDirty); syncErr != nil {
    return syncErr
}
```

**Key insight:** Config syncing uses `git pull --ff-only`, which is safe (no auto-merges) and idempotent. This pattern could be adapted for repos.

### Workspace TOML and Repo Configuration

Repos come from two sources in the config (defined in `config/config.go`):

1. **Auto-discovered** via `[[sources]]` blocks (line 78-82):
   ```go
   type SourceConfig struct {
       Org      string   `toml:"org"`
       Repos    []string `toml:"repos,omitempty"`  // explicit list
       MaxRepos int      `toml:"max_repos,omitempty"`
   }
   ```

2. **Explicit** via `[repos.<name>]` overrides (line 114-124):
   ```go
   type RepoOverride struct {
       URL      string `toml:"url,omitempty"`    // full clone URL
       Group    string `toml:"group,omitempty"`  // required for explicit
       Branch   string `toml:"branch,omitempty"` // checkout target
       // ... other fields ...
   }
   ```

**Branch handling** is already in the config schema. The applier computes effective branch via `RepoCloneBranch()` (called at line 204 of apply.go), and the Cloner supports it via `CloneWithBranch()`. But it only applies during initial clone, not on re-apply.

### Workspace State Reconciliation

When Apply runs on an existing instance, it:

1. **Loads existing state** (line 106 of apply.go)
2. **Checks drift** on managed files (lines 112-121) -- warns if user modified CLAUDE.md or installed files
3. **Runs the full pipeline** with the new config
4. **Cleans up removed repos** (lines 135-138):
   ```go
   // cleanRemovedGroupDirs removes empty group directories for repos
   // that existed in previous state but are no longer present.
   ```

**Config reconciliation happens implicitly:** If a repo is removed from the TOML, the next Apply skips it (not cloned), and the cleanup functions remove managed files and empty group dirs. But the repo directory itself is NOT deleted from disk -- it's left orphaned.

This is documented in the instance lifecycle design (`DESIGN-instance-lifecycle.md`, line 71-73):
```
| Behavior | Create | Apply |
|----------|--------|-------|
| Clone repos | Always clones all | Skips existing (idempotent) |
```

### CLI Entry Point: Where Scope is Resolved

The CLI in `cli/apply.go` (lines 46-112) handles scope detection:

1. **Scope detection** via `workspace.ResolveApplyScope()` (line 61) or registry lookup (line 54)
2. **Config sync** via `workspace.SyncConfigDir()` (line 74) -- this is the only sync currently in place
3. **Config load** via `config.Load()` (line 78)
4. **Apply loop** over all instances in scope (line 93-100)

**For each instance**, the Applier.Apply() is called once, and any failures are collected but don't stop other instances from applying.

### Cloner Implementation: No Pull/Fetch Logic

The `Cloner` struct (`clone.go`, lines 19-78) is intentionally minimal:
- `Clone(url, targetDir)` -- calls CloneWith with empty options
- `CloneWithBranch(url, targetDir, branch)` -- calls CloneWith with Ref=branch
- `CloneWith(url, targetDir, options)` -- the core method

It does NOT have methods for:
- Pulling updates from remote
- Checking current branch or commit
- Validating repo state
- Handling detached HEAD or merge conflicts

This is a clean separation: the Cloner is a stateless wrapper around `git clone`, not a full repo manager.

### Applied vs. Idempotent Behavior

The design document `DESIGN-instance-lifecycle.md` (lines 66-75) defines the intended behavior:

| Behavior | Create | Apply |
|----------|--------|-------|
| Instance dir | Creates new | Requires existing |
| Instance number | Assigned fresh | Preserved from state |
| **Clone repos** | Always clones all | **Skips existing (idempotent)** |
| Content files | Generates all | Regenerates all (overwrite) |
| Removed repos | N/A (fresh start) | Deletes managed files |
| Removed groups | N/A | Deletes managed files, removes empty dir |

The design explicitly states this is intentional for idempotency, but it acknowledges (line 300) a negative consequence: "Apply refactoring touches tested code. Mitigation: the pipeline logic doesn't change, only the entry points."

There is NO documented mechanism to pull latest code into existing repos.

## Implications

### 1. Sync Logic is Missing but Has a Clear Hook

The current pipeline **cannot pull latest code** into existing repos. However:

- **Config sync already works** for workspace.toml via `SyncConfigDir()` with `git pull --ff-only`
- **Repo state is tracked** (URL, cloned flag) -- could be extended to include commit/branch
- **Branch checkout is already in the config schema** (`RepoOverride.Branch`)
- **The pattern exists:** The Cloner is called per-repo in sequence, so per-repo post-clone steps can be inserted

**Pull logic could fit between lines 207-215 of apply.go**, after the clone decision:
```go
cloned, err := a.Cloner.CloneWithBranch(ctx, cloneURL, targetDir, branch)
if err != nil {
    return nil, fmt.Errorf("cloning repo %s: %w", cr.Repo.Name, err)
}
if cloned {
    fmt.Printf("cloned %s into %s\n", cr.Repo.Name, targetDir)
} else {
    // NEW: Pull latest if repo exists
    if pullErr := a.PullRepoIfExists(ctx, targetDir, branch); pullErr != nil {
        // decide: warn, error, or skip
    }
    fmt.Printf("skipped %s (already exists)\n", cr.Repo.Name)
}
```

### 2. Config Changes Are Partially Reconciled

When TOML changes add/remove repos:
- **New repos** are cloned on next Apply ✓
- **Removed repos** have their managed files deleted, but the repo dir stays on disk (orphaned) ✗
- **Branch changes** are NOT applied to existing clones (stays on original branch) ✗
- **URL changes** are NOT validated or updated (clone URL in state becomes stale) ✗

### 3. Two Dimensions of Freshness

The exploration context mentions two dimensions:

1. **Workspace TOML changes** (new/removed repos, group changes, branch overrides)
   - Currently: Partially handled (new repos cloned, removed repos orphaned but cleaned up via managed files)
   - Gap: URL changes and branch changes don't re-clone or update

2. **Latest code from remotes** (git pull for existing repos)
   - Currently: NOT handled (cloner skips existing repos)
   - Gap: No mechanism to refresh repo working trees

### 4. Design Precedent for Non-Destructive Updates

The config sync uses `git pull --ff-only`, which:
- Fails if local changes exist (fast-forward only)
- Is idempotent (pull again = no-op if already up-to-date)
- Preserves local modifications (warns via --allow-dirty)
- Works well for the use case

This could be the foundation for repo sync logic.

## Surprises

1. **Config sync already exists** -- `SyncConfigDir()` pulls workspace.toml from origin with `git pull --ff-only` before applying. This wasn't mentioned in the scope but is directly relevant to keeping managed workspaces fresh.

2. **Branch is in the config schema but unused on re-apply** -- `RepoOverride.Branch` exists and is passed to the cloner, but only during initial clone. If a user changes the branch in TOML for an existing repo, it's silently ignored.

3. **Repo state doesn't track enough** -- The `RepoState` struct only tracks URL and cloned flag, but not commit/branch. This limits the applier's ability to detect drift or decide whether to sync.

4. **Removed repos are only partially cleaned** -- When a repo is removed from config, managed files (CLAUDE.md, hooks, etc.) are deleted, and empty group dirs are removed, but the actual repo directory stays on disk as an orphaned clone. This was likely an intentional design choice (non-destructive), but it's not explicitly documented.

5. **The test setup pre-creates .git markers to skip cloning** -- In the apply tests (apply_test.go, lines 59-67 and 297-315), repos are pre-created with a `.git/` directory so the cloner skips them. This shows the skip-existing behavior is intentional, but it also means the tests don't exercise what happens when you run Apply twice on the same instance with fresh code.

## Open Questions

1. **Should pulling repos be opt-in or automatic?** Should `niwa apply` always pull latest, or only when explicitly requested or when config changes (branch override)?

2. **How should branch changes be handled?** If a user changes `[repos.myrepo].branch` in the TOML, should the next Apply check out the new branch, or require a manual `git checkout`?

3. **Should pulling be conditional on repo being "clean"?** Like config sync's `--allow-dirty` flag, should pulling skip repos with uncommitted changes?

4. **What should happen to orphaned repos?** When a repo is removed from TOML, should the next Apply delete its directory (destructive), leave it orphaned (current), or warn and let the user decide?

5. **How to communicate the state of syncing?** If a repo pull fails (network error, diverged branch), should it block the entire Apply, warn per-repo, or log and continue?

6. **Should pull use --ff-only (like config sync) or --rebase or full merge?** FF-only is safest but can fail if local commits exist.

## Summary

The apply pipeline discovers repos from config, classifies them by group, and clones them if they don't exist (detected by `.git/` directory). On re-apply, existing repos are intentionally skipped, making Apply idempotent but preventing code refresh. Config syncing is already implemented for workspace.toml using `git pull --ff-only` before the pipeline runs. Repo state tracks URLs and clone status but not commits/branches, limiting the ability to detect drift. Pull logic could fit cleanly between the clone decision and managed-file installation, and branch overrides are already in the config schema but only used on initial clone. Workspace TOML changes are partially reconciled (new repos cloned, removed repos orphaned but cleaned up); the gap is that URL and branch changes don't trigger re-clones or updates.

