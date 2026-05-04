# Lead: Parallel worktree conflicts

## Findings

### Git mechanics under worktrees

Git worktrees share a single `.git` object store (the bare repository in the parent directory) but maintain separate working directories and `HEAD` pointers. Key git operations that lock or affect all worktrees:

1. **Per-worktree safe operations** (no cross-worktree conflicts):
   - Checkout, branch creation, reset, commit, stash — each worktree's `HEAD`, index, and working tree are isolated
   - Two worktrees can be on different branches simultaneously without git-level conflict
   - Each worktree has its own `refs/heads/`, `HEAD` file, and index — no shared state

2. **Repository-wide operations that DO impact all worktrees**:
   - `git gc`, `git repack`, `git prune`: These operate on the shared `.git/objects/` and `.git/refs/` directories. A gc in one worktree can temporarily block or slow other worktrees during the repack phase. Git uses a `gc.lock` file in `.git/` to coordinate, but it is short-lived (seconds) unless interrupted.
   - `git reflog expire`: Operates on packed refs; could theoretically race with simultaneous ref updates across worktrees if both are writing to the same branch (rare in practice — each worktree has its own branch by design).
   - `git fetch`, `git pull`: Acquire `refs.lock` or `HEAD.lock` when updating refs; concurrent `git fetch` from two worktrees pulling the same remote branch will serialize at the ref-update stage (30s flock timeout on `.git/HEAD.lock`). This is safe but slow.

3. **Worktree-specific git files** (no conflict):
   - Each worktree has `.git/worktrees/<name>/` directory with its own `HEAD`, `index`, and `locked` file
   - `.git/worktrees/<name>/locked` is a marker; removing it unlocks the worktree

**Critical finding:** Git worktrees themselves are safe for concurrent operation. Lock contention on the shared object store is expected and handled by git's advisory locking. No data corruption occurs; operations serialize cleanly.

### Niwa hooks and settings

From `.claude/settings.local.json` and hook inspection:

1. **Settings scope**: `.claude/settings.json` is per-instance (lives at the workspace root under `.claude/`). Multiple worktrees for the same repo share:
   - The same instance root
   - The same `.claude/settings.json` (and local overrides)
   - The same hooks configuration

2. **Hook execution model**: Claude Code hooks (PreToolUse, Stop) are defined in `settings.json` as arrays of command objects. Multiple sessions sharing the same `.claude/settings.json` will execute the same hooks independently — no coordination between them.

3. **Stop hook behavior** (from DESIGN-coordinator-loop.md):
   - Each worker session executes its own Stop hook at every turn boundary
   - The hook calls `niwa mesh report-progress --task-id $NIWA_TASK_ID`
   - Environment variable `NIWA_TASK_ID` is set per-worker at spawn time and inherited by child processes
   - Two simultaneous workers each have a different `NIWA_TASK_ID`, so their `report-progress` calls write to different task directories (`.niwa/tasks/<task-id-1>/state.json` and `.niwa/tasks/<task-id-2>/state.json`)

4. **Hook file conflict**: The hook script path is absolute (e.g., `/home/.../niwa/.claude/hooks/stop/workflow-continue.local.sh`). Both workers execute the same script file, but it is read-only and state-less — no conflict.

**Critical finding:** Shared `.claude/settings.json` is safe. Hooks are stateless and tagged with `NIWA_TASK_ID`, so parallel execution is safe and expected.

### Mesh state and atomicity

From `internal/mcp/taskstore.go` and DESIGN-cross-session-communication.md:

1. **`.niwa/` structure**:
   - `.niwa/workspace.toml` — workspace config, read-only per-instance
   - `.niwa/instance.json` — instance state, shared by all sessions in the workspace
   - `.niwa/tasks/<task-id>/` — per-task state and log; each task is independent

2. **State synchronization**:
   - Each task directory has a dedicated `.lock` file (zero-byte coordination target)
   - `UpdateState()` function enforces strict write order: `flock(LOCK_EX)` → read → mutate → write-tmp → fsync → rename → fsync → unlock
   - Lock timeout is 30 seconds; lock poll interval is 20 ms
   - All writers use the same `UpdateState()` path; no direct state.json rewrites outside this function

3. **Concurrent access**:
   - Two workers for different tasks never contend on locks (different task directories)
   - Two workers for the same task would contend on the same `.niwa/tasks/<task-id>/.lock` — but this should never happen (the daemon's atomic envelope consumption prevents two workers claiming the same task)
   - Reads use shared flock (LOCK_SH); writers use exclusive flock (LOCK_EX). No reader blocks a writer or vice versa for more than 30 seconds.

4. **Instance.json access**:
   - `.niwa/instance.json` is written by `niwa apply` at instance setup, then read during `niwa go` / apply to resolve roles and repo paths
   - No locking on instance.json reads (safe: file is immutable during normal operation)
   - Multiple instances can coexist in the same workspace without conflict (each is a separate directory)

**Critical finding:** The task state machine is designed for concurrent access from multiple workers. Flock-based atomicity is the synchronization primitive, and the design assumes two workers could simultaneously query or update different tasks (which is the normal mesh operation). No explicit per-worktree state isolation is needed.

### Command compatibility under worktrees

From `explore_mesh-session-lifecycle_r1_lead-command-compat-audit.md`:

1. **Commands that are fully worktree-compatible** (no code changes needed):
   - `niwa go`, `niwa apply`, `niwa create`, `niwa destroy`, `niwa reset`
   - Reason: All use config/state file discovery (walking for `.niwa/workspace.toml`, `.niwa/instance.json`), not git introspection
   - Path resolution uses `filepath.Dir()`, not git working-tree queries

2. **Commands that have minimal issues**:
   - `niwa apply`: One-line fix needed in `snapshotwriter.dotGitExists()` — the function checks `info.IsDir()` on `.git`, which fails for worktree symlinks. Change to `return err == nil` (test existence, not directory-ness).

**Critical finding:** Worktree compatibility is already nearly complete. Minimal code changes required.

### Parallel push and branch conflicts

Two sessions simultaneously on different branches in the same repo:

1. **Different branches (most common case)**:
   - Session A on `feature-x`, Session B on `feature-y`
   - Each pushes to its own branch: `git push origin feature-x` and `git push origin feature-y`
   - No conflict; git ref-update serializes cleanly via `.git/HEAD.lock`

2. **Same branch conflict** (rare, but possible if coordinator starts two sessions for the same branch):
   - Session A and B both on `feature-z`, both make commits and try to push
   - First push succeeds; second push gets a "rejected (non-fast-forward)" and must rebase/force-push
   - This is not a git worktree issue — it's a workflow design issue (coordinator should not start two workers on the same branch)

3. **Shared ref contention**:
   - `git fetch` from both worktrees updating the same remote-tracking branch (`refs/remotes/origin/main`) will serialize at `.git/refs/remotes/origin/main.lock` (30s timeout)
   - Expected and safe; git handles it

**Critical finding:** No special conflict beyond standard git concurrency model. If a coordinator starts two sessions on the same branch, standard git merge conflicts apply — that's a coordinator-level issue, not a worktree issue.

### `.niwa-snapshot.toml` and config distribution

From DESIGN-config-distribution.md and DESIGN-workspace-config-sources.md:

1. **Snapshot model**:
   - `.niwa-snapshot.toml` is a provenance marker created at apply time in the workspace root
   - Distributed to each instance/repo via config copy
   - If the main clone converts from legacy working-tree to snapshot model, the snapshot is materialized into each instance's `.niwa/`

2. **Per-worktree snapshot**:
   - Each worktree can have its own independent `.niwa/` copy during apply
   - `.niwa-snapshot.toml` is not shared between worktrees (each apply materialization creates a fresh copy)
   - No cross-worktree dependency on snapshot state

**Critical finding:** Snapshots are per-apply and per-instance; no contention between parallel worktrees.

## Implications

### For coordinator-managed session lifecycle

1. **Safe to start**: The mesh is designed for concurrent task execution. Two worktrees for the same repo can run simultaneously without any code changes to the task state machine.

2. **Required safeguards** (coordinator-level, not worktree-level):
   - The coordinator must assign each session a unique `task_id` (already done via `NewTaskID()` UUID generation)
   - The coordinator must not start two sessions on the same feature branch unless it plans to handle merge conflicts
   - The coordinator should track which session maps to which worktree (out of scope for niwa; coordinator's responsibility)

3. **Settings sharing is safe**: Multiple sessions in the same workspace share `.claude/settings.json` and hooks. This is by design. Hooks are tagged with `NIWA_TASK_ID` and write to task-specific state directories, so parallel execution is safe.

4. **Mesh state is safe**: The flock-based task state machine is designed for this. No per-worktree state isolation is needed in `.niwa/tasks/`.

### For worktree creation and cleanup

1. **Worktree lifecycle hook**: A coordinator that manages worktrees should:
   - Call `git worktree add <path> <branch>` to create a new worktree
   - Set `NIWA_TASK_ID` environment variable in the session spawned in that worktree
   - Call `git worktree remove <path>` when the session ends (after pushing or abandoning work)

2. **Instance cleanup**: Each worktree can have its own `.niwa/instance.json` (recommended for session isolation) or share the main instance (simpler, works because task state is keyed by task_id, not instance).

## Surprises

1. **The task state machine already assumes concurrent workers**: The flock-based atomicity design in `taskstore.go` is built for exactly this scenario — multiple independent workers (on different task IDs) writing to `.niwa/tasks/` simultaneously. The 30s flock timeout and 20ms poll interval are tuned for production concurrency.

2. **Hooks are designed for parallel execution**: The Stop hook uses `NIWA_TASK_ID` to disambiguate which worker it belongs to. Multiple workers' stop hooks can fire in parallel without interference.

3. **Settings aren't per-worktree; they're per-instance**: Both the main clone and a worktree of the same repo read from `.claude/settings.json` at the instance root. This is safe and works as expected for shared hooks.

4. **Git's own locking is sufficient**: No additional locking needed at the niwa level. Git's advisory file locks (`.git/HEAD.lock`, `.git/refs/refs.lock`, `gc.lock`) handle serialization of concurrent operations.

5. **Instance discovery works for worktrees without changes**: The audit found that `workspace.EnumerateInstances()` scans direct children of `workspaceRoot` for `.niwa/instance.json`. Worktrees are separate top-level directories (not children of workspace), so if worktrees are siblings of the main clone, instance enumeration won't find them. This is expected — worktrees are not managed as separate instances; they are physical anchors for sessions within the same instance.

## Open Questions

1. **Session-to-worktree mapping**: Should the coordinator maintain a mapping of session ID → worktree path, or should niwa track this? The niwa codebase does not currently persist this mapping, so the coordinator must manage it.

2. **Instance.json sharing vs per-worktree isolation**: Should all worktrees of the same repo read from the same `.niwa/instance.json`, or should each worktree have its own copy? Current design allows either (all use config discovery), but the coordinator should decide the model.

3. **Worktree cleanup on session end**: Who is responsible for calling `git worktree remove` — the coordinator, a cleanup hook in the worker, or a separate niwa subcommand? Currently not addressed.

4. **Conflict resolution for concurrent branch operations**: If two sessions on different branches both push and one rebases, what is the expected behavior? This is a workflow design question, not a niwa conflict, but the coordinator needs guidance.

5. **Git worktree lock semantics**: The `.git/worktrees/<name>/locked` file is a human-readable marker. Should niwa or the coordinator respect this, or treat it as opaque?

## Summary

Git worktrees are safe for concurrent operation with niwa mesh: the task state machine is already designed for multiple parallel workers (different task IDs), and task-scoped flock atomicity prevents corruption. The coordinator can safely start two sessions for the same repo simultaneously if they are anchored to separate worktrees; git's own serialization (ref locks, gc.lock) handles cross-worktree contention cleanly. The only worktree-related code fix needed is a one-line change to `snapshotwriter.dotGitExists()` to recognize worktree `.git` symlinks — all other niwa commands already work unchanged. No per-worktree state isolation is required in `.niwa/tasks/`; task IDs are the synchronization boundary. The main open question is coordinator-level: who creates and removes worktrees, and how does the coordinator track the session-to-worktree mapping?
