# Lead: Command compatibility audit for worktree layout

## Findings

### `niwa go` (internal/cli/go.go)

**Path assumptions:**
- `resolveCurrentWorkspaceRoot()` (line 90): calls `config.Discover(cwd)` to walk up from cwd looking for `.niwa/workspace.toml`
- `resolveCurrentInstanceRepo()` (line 121): calls `workspace.DiscoverInstance(cwd)` to find the nearest `.niwa/instance.json` walking upward
- All resolution paths use `filepath.Dir()` and relative path construction to build workspace/instance/repo directories
- No direct `.git` interrogation; relies on config/state file discovery

**Worktree compatibility:** GOOD
- Config discovery (`config.Discover()`) uses `os.Stat()` on `.niwa/workspace.toml` path — works in worktree (target is inside workspace, not .git)
- Instance discovery (`workspace.DiscoverInstance()`) walks for `.niwa/instance.json` — works in worktree
- No hardcoded assumptions about `.git/` structure; treats it as opaque
- **Impact:** `niwa go` requires NO changes for worktree mode

---

### `niwa apply` (internal/cli/apply.go)

**Path assumptions:**
1. **Registry scope resolution** (line 179-214):
   - Looks up workspace in `~/.config/niwa/config.toml` by name
   - Reads `entry.Source` (path to `.niwa/workspace.toml`) and `entry.Root` (workspace root directory)
   - Calls `workspace.EnumerateInstances(workspaceRoot)` to list instance directories
   - **Assumption:** All instances are direct children of `workspaceRoot` (line 204-207 handles single-instance case)

2. **Config/instance discovery** (line 84-88):
   - From cwd: calls `workspace.ResolveApplyScope(cwd, applyInstance)` which tries to find the current instance or workspace
   - If `--instance` flag set, finds workspace root via `config.Discover()`, then matches by `InstanceName` in state files

3. **Config snapshot management** (line 109-114):
   - Calls `checkConfigSourceURLChange()` which reads `.niwa-snapshot.toml` provenance marker
   - Reads `git remote get-url origin` from configDir (line 394, `runGitOrigin()`) for legacy working-tree detection
   - **Critical:** line 393 checks `os.Stat(filepath.Join(configDir, ".git"))` to detect legacy working trees

4. **Global registry updates** (line 165):
   - `updateRegistry()` preserves absolute paths for `config.Source` and workspace `root`

**Worktree compatibility:** BREAKING IN ONE PLACE
- Lines 393-395: `onDiskSourceURL()` checks `os.Stat(filepath.Join(configDir, ".git"))` and then calls `runGitOrigin(configDir)` to read git remote
- In a worktree, `.git` is a file (symlink to parent's `.git/`), not a directory
- `info.IsDir()` on line 95 of `snapshotwriter.go` returns **false** for a worktree's `.git` file
- This breaks the legacy-working-tree detection path: the function will fail to recognize a worktree config dir as a legacy working tree and skip lazy conversion
- **Impact:** Moderate — only affects workspaces with legacy `.git`-backed config dirs; new snapshot-based workspaces unaffected. **But:** if the main clone has a legacy config dir, apply will not convert it when run from a worktree

---

### Instance discovery & enumeration (internal/workspace/state.go)

**Path assumptions:**
- `DiscoverInstance()` (line 277-295): walks upward from cwd looking for `.niwa/instance.json`
- `EnumerateInstances()` (line 301-322): scans **direct children** of workspaceRoot for directories containing `.niwa/instance.json`
  - **Key line 316:** `os.ReadDir(workspaceRoot)` then checks `statePath(dir)` in each child
  - Assumes flat directory structure: `workspaceRoot/instance1/.niwa/instance.json`, `workspaceRoot/instance2/.niwa/instance.json`

**Worktree compatibility:** GOOD
- Both use `os.Stat()` on state file paths, not git introspection
- Flat structure assumption holds for worktrees (worktrees are separate directories on disk, each with its own `.niwa/instance.json`)

---

### Config discovery (internal/config/discover.go)

**Path assumptions:**
- `Discover()` (line 18-38): walks upward from startDir looking for `.niwa/workspace.toml`
- Uses `os.Stat()` to check file existence
- Returns absolute paths constructed via `filepath.Join()`

**Worktree compatibility:** GOOD
- No git assumptions; pure filesystem walk

---

### Workspace state & snapshot management (internal/workspace/snapshotwriter.go)

**Path assumptions:**
1. **Legacy working-tree detection** (line 90-96): `dotGitExists()`
   - Calls `os.Stat(filepath.Join(dir, ".git"))`
   - Checks `info.IsDir()` to confirm it's a directory
   - **In a worktree:** `.git` is a file, not a directory → returns false

2. **Lazy conversion flow** (line 165-204): `lazyConvertWorkingTree()`
   - Calls `dotGitExists()` — fails silently for worktree `.git`
   - Calls `readGitOrigin()` (line 166) which runs `git -C dir remote get-url origin` — works in worktree
   - But the conversion only fires if `dotGitExists()` returned true, so worktree config dirs skip conversion

3. **Provenance marker** (line 85-88): `provenanceMarkerExists()`
   - Checks `.niwa-snapshot.toml` existence — works in worktree

**Worktree compatibility:** BREAKING FOR LEGACY CONFIG DIRS
- The `dotGitExists()` check on line 91-95 uses `info.IsDir()` which returns false for worktree `.git` files
- Worktrees with legacy `.git`-backed config dirs will not be converted to snapshots
- **Impact:** If the main clone's config dir is still a legacy working tree (pre-snapshot model), and you run apply from a worktree, the conversion won't happen. The worktree's apply will silently skip the config refresh since `dotGitExists()` returns false.

---

### Repo cloning (internal/workspace/clone.go, apply.go)

**Path assumptions:**
- Repos cloned to `filepath.Join(instanceRoot, cr.Group, cr.Repo.Name)` (apply.go line 909)
- Each repo checked for existing clone via `repoAlreadyCloned()` (line 1366-1369)
  - Checks `os.Stat(filepath.Join(dir, ".git"))` for any git marker (directory or file)
  - **Note:** Does NOT check `info.IsDir()` — just checks for existence
- **Impact:** This check works fine for worktree repos

**Worktree compatibility:** GOOD
- Repo detection doesn't assume `.git/` structure
- Uses existence check, not directory test

---

## Implications

**Summary of breakage surface:**

1. **Small syntax issue (fixable):** The `dotGitExists()` function on line 90-96 of `snapshotwriter.go` uses `info.IsDir()` to validate that `.git` is a directory. This breaks for worktrees where `.git` is a file. Fix: change to `return err == nil` (test for existence, not directory-ness).

2. **Moderate business logic impact:** Workspace config dirs in legacy `.git`-backed form will not be auto-converted to snapshots when apply is run from a worktree. This is only a problem if:
   - The main clone's config dir is still using the pre-snapshot model (uncommon but possible in existing workspaces)
   - Someone creates a worktree and runs `niwa apply` before the main clone has been converted

3. **Commands analysis:**
   - `niwa go`: **Fully compatible** (no changes needed)
   - `niwa apply`: **Requires one-line fix** in `snapshotwriter.dotGitExists()` 
   - Scope resolution: **Fully compatible** (uses state files, not git)
   - Instance/repo discovery: **Fully compatible**
   - Registry operations: **Fully compatible** (uses absolute paths)

**Which commands are mesh-only vs workspace-universal:**
- `niwa go`, `niwa apply`, `niwa create`, `niwa destroy`, `niwa reset`: workspace-universal (work in both main and worktree)
- `niwa status`, `niwa channels`, `niwa mesh` commands: workspace-universal
- All commands rely on config/state file discovery, not git branch/commit state

---

## Surprises

1. **The `.git` check is minimal and isolated:** Only appears in one function (`dotGitExists`) and one test (`repoAlreadyCloned` check in apply.go line 1369). Easy to fix globally.

2. **No path assumptions about the git repository location:** Niwa never assumes `.git` is the anchor or uses git introspection for directory structure. The model is fully decoupled from git working-tree shape.

3. **Repo clone check is more permissive:** Line 1369 checks `os.Stat(filepath.Join(dir, ".git"))` without `IsDir()`, which accidentally works for worktrees.

4. **The registry is path-absolute and durable:** Workspace registry stores absolute paths to config and root, so the same workspace can be referenced from main clone or worktree without re-registration.

5. **Single-instance layout already worked partially:** The special case in `apply.go` line 204-207 handles single-instance workspaces (where instance root = workspace root). This pattern would work unchanged in a worktree, but only if the instance.json path resolution fires correctly.

---

## Open Questions

1. **Lazy conversion intent:** Is the goal to support running `niwa apply` from a worktree and having it lazily convert a main-clone legacy config dir? Or should the main clone always be responsible for its own migration?

2. **Worktree lifecycle:** Should worktrees have their own `.niwa/instance.json` or share state with the main clone? Current design assumes each clone/worktree has independent state.

3. **Session anchor clarity:** If a session spans multiple repos and uses worktrees, which `.niwa/` directory is the session-level source of truth? (This may be answered by CLAUDE.md or session design docs.)

4. **Config snapshot distribution:** If the main clone converts its config dir to a snapshot, does that snapshot need to be synchronized to worktrees, or does each run independently materialize?

---

## Summary

Niwa's folder-structure assumptions are minimal and mesh well with git worktrees: only the legacy-working-tree detection in `snapshotwriter.dotGitExists()` breaks because it checks `info.IsDir()` on a worktree's `.git` file. A one-line fix (return `err == nil` instead of `info.IsDir()`) removes this blocker. All other path discovery mechanisms—config files, state files, instance enumeration—work unchanged in worktrees because they use filesystem walks and explicit `.niwa/` markers, not git introspection. The registry already anchors by absolute paths, so the same workspace can be managed from main or worktree without re-registration. Worktree support is feasible as an anchoring model with minimal code changes.

