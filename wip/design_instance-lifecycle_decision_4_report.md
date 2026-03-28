# Decision 4: How should reset and destroy work?

## Decision

**Chosen approach:** Destroy removes the instance subdirectory entirely via `os.RemoveAll`; reset calls destroy then re-runs the create+apply pipeline. Both use a shared `CheckUncommittedChanges` safety check that scans cloned repos for dirty git state before proceeding.

## Analysis

### What exists today

The codebase already has the building blocks:

- **`workspace.InstanceState`** tracks repos (name -> URL + cloned status) and managed files with hashes.
- **`workspace.EnumerateInstances`** and **`workspace.DiscoverInstance`** locate instances by scanning for `.niwa/instance.json`.
- **`workspace.LoadState`** reads instance state, giving access to the repo map.
- **`config.GlobalConfig`** registry maps workspace names to root directories, so name-based resolution already works (see `apply.go` lines 31-44).
- **`workspace.Applier.Apply`** handles the full create+apply pipeline (clone, classify, install content, write state).

### Design

#### Instance resolution (shared by both commands)

Both commands accept an instance name as a required argument. Resolution:

1. Load the global registry via `config.LoadGlobalConfig()`.
2. Look up the name with `globalCfg.LookupWorkspace(name)`.
3. Use `entry.Root` as the instance directory.
4. Verify `.niwa/instance.json` exists in that directory (guards against stale registry entries).

If running from within the instance directory (no name arg -- though the constraints say name is required), `DiscoverInstance` could be used as a fallback. Given the constraint that the name is required, registry lookup is the primary path.

#### Uncommitted changes check

A new function `CheckUncommittedChanges(instanceDir string) ([]string, error)`:

1. Load instance state from `instanceDir`.
2. For each repo in `state.Repos` where `Cloned == true`, run `git -C <repo-path> status --porcelain`.
3. Collect repo names with non-empty output (dirty working tree or staged changes).
4. Return the list of dirty repos.

This runs before any destructive action. If dirty repos exist and `--force` is not set, print the list and prompt for confirmation. The prompt uses a simple yes/no on stdin. If stdin is not a terminal (piped/scripted), treat non-force as an error rather than hanging.

#### `niwa destroy <instance>`

1. Resolve instance directory from name (registry lookup).
2. Load state, run uncommitted changes check.
3. If dirty repos and no `--force`: print warning, prompt, abort on "no".
4. `os.RemoveAll(instanceDir)` -- deletes everything including `.niwa/`, cloned repos, managed files.
5. Remove the registry entry from global config and save.
6. Print confirmation message.

No partial cleanup needed. The instance directory is self-contained by design (all repos clone inside it), so a single `RemoveAll` is correct and atomic from the filesystem perspective.

#### `niwa reset <instance>`

1. Resolve instance directory and load config path from registry (need the source before destroying).
2. Run uncommitted changes check (same as destroy).
3. If dirty repos and no `--force`: prompt.
4. Save the registry entry and config path before destruction.
5. Destroy: `os.RemoveAll(instanceDir)`.
6. Recreate: `os.MkdirAll(instanceDir)`, then run `Applier.Apply()` with the saved config.
7. Update registry entry (root stays the same, last-applied updates).

The key detail: reset must capture the config source *before* deleting the instance, since the config may live inside `.niwa/` (cloned config repo). For cloned configs, the config is re-fetched during apply. For local configs, the config lives inside `.niwa/workspace.toml` which gets destroyed -- so reset of a local-only workspace would fail. This is acceptable: local-only workspaces without a remote source can't be reset (they'd lose their config). The command should detect this and error with a clear message: "cannot reset a local-only workspace; the config would be lost. Use destroy + init instead."

#### CLI structure

```go
// internal/cli/destroy.go
var destroyForce bool

var destroyCmd = &cobra.Command{
    Use:   "destroy <instance>",
    Short: "Permanently remove a workspace instance",
    Args:  cobra.ExactArgs(1),
    RunE:  runDestroy,
}

// internal/cli/reset.go
var resetForce bool

var resetCmd = &cobra.Command{
    Use:   "reset <instance>",
    Short: "Tear down and recreate a workspace instance",
    Args:  cobra.ExactArgs(1),
    RunE:  runReset,
}
```

Both register `--force` via `cmd.Flags().BoolVar`.

#### Workspace-level functions

```go
// internal/workspace/destroy.go

// CheckUncommittedChanges returns repo names with uncommitted changes.
func CheckUncommittedChanges(instanceDir string) ([]string, error)

// DestroyInstance removes the instance directory entirely.
func DestroyInstance(instanceDir string) error
```

`DestroyInstance` is a thin wrapper around `os.RemoveAll` that validates the directory contains `.niwa/instance.json` first (safety check against deleting arbitrary directories).

### Alternatives considered

1. **Selective cleanup (delete only managed files and cloned repos):** More surgical, but adds complexity for no benefit. The instance directory is niwa-owned; partial preservation doesn't serve a use case.

2. **Soft delete (rename to .niwa-backup):** Adds recovery capability but complicates the directory structure. Users have git for recovery of repo contents. Not worth the complexity.

3. **Reset without full destroy (git clean/reset each repo):** Would preserve clone time but introduces partial states and git-level complexity. A clean re-clone is simpler and more predictable.

### Edge cases

- **Stale registry entry (directory already deleted):** Destroy should handle gracefully -- remove the registry entry and report "already removed" rather than erroring.
- **Instance directory is CWD:** Both commands work fine; the OS allows removing the current directory. Print a note that the user's shell CWD now points to a deleted path.
- **Concurrent access:** Not handled. Single-user CLI tool; file locking is out of scope.
- **Permissions:** `os.RemoveAll` may fail on permission issues within cloned repos (e.g., `.git/objects` with restricted permissions). Go's `RemoveAll` handles this on most platforms, but if it fails, the error surfaces to the user.
