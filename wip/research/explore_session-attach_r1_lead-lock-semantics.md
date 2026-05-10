# Lead: How should the attach lock be acquired, released, and recovered, and what happens to uncommitted worktree state on detach?

## Findings

### Existing precedent in niwa (read this first)

niwa already has three lock-adjacent patterns. Any attach-lock proposal should reason from these rather than inventing a new mechanism.

#### 1. `daemon.pid.lock` — exclusive flock, lifetime-of-process semantics

The per-instance daemon already solves "only one process owns this resource at a time." The mesh watch daemon in `internal/cli/mesh_watch.go:2363` (`acquireDaemonPIDLock`) takes an exclusive non-blocking flock on `<niwaDir>/daemon.pid.lock`:

```go
// /home/dgazineu/dev/niwaw/tsuku/tsuku-4/public/niwa/internal/cli/mesh_watch.go:2363
func acquireDaemonPIDLock(path string) (*os.File, error) {
    f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
    ...
    if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
        if errors.Is(err, syscall.EWOULDBLOCK) {
            return nil, errDaemonAlreadyRunning
        }
    }
    return f, nil
}
```

The lock is held for the daemon's lifetime (via `defer syscall.Flock(... LOCK_UN)` at line 258-261 of `mesh_watch.go`). Critically:

- **Acquisition is non-blocking** — `LOCK_NB` means losers get `EWOULDBLOCK` and exit cleanly with code 0 ("another daemon wins"). No FIFO queue, no wait.
- **Release is implicit** — when the daemon process dies (cleanly or by SIGKILL), the kernel drops the flock. No stale-lock detection is needed because flock state is tied to the open file descriptor.
- **Recovery is automatic** — `EnsureDaemonRunning` (`internal/workspace/daemon.go:35`) reads `daemon.pid` best-effort, calls `IsPIDAlive(pid, startTime)`, and if not alive, re-spawns. The new daemon takes the flock; if a zombie still holds it, the spawned daemon loses cleanly.

This is the cleanest precedent for an attach lock: filesystem flock with implicit release.

#### 2. `IsPIDAlive(pid, startTime)` — PID + start_time defends against PID recycling

`internal/mcp/liveness.go:14` is the canonical liveness check:

```go
// /home/dgazineu/dev/niwaw/tsuku/tsuku-4/public/niwa/internal/mcp/liveness.go:14
func IsPIDAlive(pid int, startTime int64) bool {
    if pid == 0 { return false }
    proc, err := os.FindProcess(pid)
    if err != nil { return false }
    if err := proc.Signal(syscall.Signal(0)); err != nil { return false }
    if startTime == 0 { return true }
    recorded, err := pidStartTime(pid)  // /proc/<pid>/stat field 22
    if err != nil { return true }       // conservative: alive
    return recorded == startTime
}
```

`pidStartTime` reads `/proc/<pid>/stat` field 22 (jiffies-since-boot at process creation). `(pid, startTime)` together form a unique-enough process identity to defend against the OS reusing a PID — important for any "is the attached process still alive?" check that may run minutes or hours after attach.

**Linux-only:** `pidStartTime` returns an error on non-Linux. Functionally niwa is already Linux-first; this is not a new constraint for attach.

#### 3. `worker.PID` + atomic state.json — task store treats "currently running" as a state.json field

The taskstore (`internal/mcp/taskstore.go`, `types.go:238`) tracks a running worker via:

```go
type TaskWorker struct {
    PID            int    `json:"pid"`
    StartTime      int64  `json:"start_time"`
    Role           string `json:"role"`
    SpawnStartedAt string `json:"spawn_started_at,omitempty"`
    AdoptedAt      string `json:"adopted_at,omitempty"`
    ClaudeSessionID string `json:"claude_session_id,omitempty"`
    ResumeCount     int    `json:"resume_count,omitempty"`
}
```

All writers go through `UpdateState` under a per-task exclusive flock on `<task>/.lock` (see `taskstore.go:8` "flock(.lock, LOCK_EX) → read state.json → mutate → atomic rename"). Readers take a shared flock for consistent snapshots. This is what makes "is a worker currently running for this task?" a precise, race-free question:

```go
_, st, _ := mcp.ReadState(taskDir)
running := st.State == mcp.TaskStateRunning && IsPIDAlive(st.Worker.PID, st.Worker.StartTime)
```

The destroy path (`internal/mcp/handlers_session.go:320` `killSessionWorkers`) uses exactly this pattern, including signalling the worker's process group with `syscall.Kill(-st.Worker.PID, ...)` because workers spawn with `Setsid=true` (so PID == PGID).

#### 4. `branch_warning` — destroy already has the precedent for "uncommitted/unmerged work" UX

`internal/mcp/handlers_session.go:282-292` is the existing pattern: by default `niwa_destroy_session` runs `git branch -d` (refuses on unmerged) and surfaces a `branch_warning` field if it fails:

```go
branchArg := "-d"
if args.Force { branchArg = "-D" }
if err := exec.Command("git", "-C", repoPath, "branch", branchArg, branchName).Run(); err != nil && !args.Force {
    state.BranchWarning = fmt.Sprintf(
        "branch %s was not deleted (unmerged commits remain); review and delete manually: ...",
        ...)
}
```

Note: `branch_warning` has `json:"-"` on the disk type and is only emitted in the wire response (a deliberate "never persist this" pattern — `internal/mcp/session_lifecycle.go:42-46`). The principle is "warn loudly, do not auto-clean, default to safe."

#### 5. Workspace-level scan of unpushed work — the heaviest existing "uncommitted" detector

`internal/workspace/scan.go` has a comprehensive `LossKind` taxonomy (dirty, untracked, unpushed, local-only, stash, detached-orphan, external-wt) used by `niwa destroy --force`. It runs `git status --porcelain`, `for-each-ref` with upstream-track, `stash list`, and detached-HEAD orphan detection. This is the model for "what would I lose if I detached without saving?"

The destroy CLI surfaces these via `FormatScans()` and forces typed-confirmation against the workspace name when losses are present (`internal/cli/destroy.go:319-339`). This is the strongest UX precedent for the detach-with-uncommitted-work case.

---

### Proposed lock mechanics for `niwa session attach`

Reasoning from the precedent above, here is a concrete proposal answering each sub-question.

#### Sub-question 1: Where does the lock live? FIFO/Reject/Wait?

**Proposal:** Filesystem flock at `<worktreePath>/.niwa/attach.lock` (a new zero-byte file alongside the existing `daemon.pid` and `daemon.log`). Use `LOCK_EX | LOCK_NB`. **Reject** on contention; do not queue or wait.

Why this lives in the worktree, not the main instance:

- Attach is fundamentally a per-worktree resource (the human cd's into the worktree, runs `claude --resume` there). The worktree already has `<worktree>/.niwa/` for the per-session daemon (`docs/guides/sessions.md:65-72`).
- The session lifecycle state file at `<instance>/.niwa/sessions/<sessionID>.json` is a poor fit for the lock itself — adding an `Attached` boolean field would need its own flock (the lifecycle file has no lock today, only atomic tmp+rename writes). Re-using the proven flock pattern is simpler.
- A separate `attach.lock` file decouples attach state from session state — important because session state already has its own consumers (`niwa session list`).

Why reject (not queue/wait):

- FIFO would require a blocking flock or a polling lock with stable ordering. Both add complexity for a human-only flow.
- The issue's open question "tasks queue outside; pending state becomes visible when the human detaches" is already satisfied by the daemon model: queued envelopes sit in the inbox until the daemon claims them. The human's lock just needs to prevent the daemon from claiming work while attached (see Sub-question 6 below). It does not need to serialize attaches.
- Reject UX: `niwa session attach <id>` returns `Error: session <id> is already attached by PID 12345 (since 14:32 UTC). Run "niwa session detach <id> --force" to break the lock if the holder is gone.` This is the same "tell the user clearly, point at the recovery command" pattern as `errDaemonAlreadyRunning` (`mesh_watch.go:2348`).

**Open question to the PRD:** Should `attach` also write a sentinel JSON file (e.g. `<worktree>/.niwa/attach.state` with `{attached_pid, start_time, attached_at, attached_by}`) for observability — so `niwa session list` can show "attached" without flock-probing? Yes, and this is the right place to surface "attached" state because the file is small, atomically written tmp+rename, and `IsPIDAlive` provides automatic stale detection. The lifecycle JSON should surface a derived `attached: true` field but not be the source of truth for the lock itself.

#### Sub-question 2: How is the lock released?

**Proposal:** Implicit release via flock-on-fd lifetime. The `niwa session attach` CLI process itself holds the flock; on process exit (clean or signal), the kernel drops it.

This means **`niwa session attach` must NOT exec-replace itself with `claude` and die immediately.** Two viable patterns:

- **(A) niwa parent + claude child:** niwa forks `claude --resume <id>` as a child process, holds the flock, blocks on `cmd.Wait()`. When claude exits, niwa removes the sentinel file and the flock drops as niwa exits. Exit code is propagated. This mirrors how `EnsureDaemonRunning` handles the daemon child (`internal/workspace/daemon.go:77-84`), except niwa-attach does NOT detach (no `Setsid`) — it stays in the user's TTY.
- **(B) exec-style (rejected):** if niwa exec'd claude directly, niwa would die before claude started, dropping the flock immediately. The lock would be useless.

Pattern (A) is correct. The cost is one extra process in the tree; the benefit is that the lock's lifetime is tied exactly to "the human is still in the session."

Trade-off: (A) means niwa must forward stdin/stdout/stderr to the child (or use `cmd.Stdin = os.Stdin; cmd.Stdout = os.Stdout; cmd.Stderr = os.Stderr` and let the child inherit). The TTY is the child's directly. Signal forwarding (Ctrl-C, Ctrl-Z) is also a concern but standard Go `exec.Cmd` patterns handle it. The pattern is well-trodden.

#### Sub-question 3: Stale-lock detection and recovery

The core stale scenarios:

| Scenario | What survives | How attach-lock detects it |
|---|---|---|
| Clean exit (Ctrl-D out of claude, niwa-attach process exits) | Nothing | Flock auto-released; no recovery needed. |
| niwa-attach SIGKILL'd locally | Sentinel file (lock auto-released) | Flock is free; sentinel file's PID is dead per `IsPIDAlive`. New attach succeeds; clean up sentinel during acquire. |
| Terminal/SSH window closes; SIGHUP cascades and kills niwa-attach + claude | Same as above | Same as above. |
| **SSH disconnect with surviving process** (SIGHUP swallowed, e.g. niwa-attach inherits no controlling terminal because of nohup or `setsid`) | flock STILL HELD by live niwa-attach | This is the only genuinely tricky case. |
| Host crash / kernel panic | Sentinel file (filesystem) | Flock state is per-kernel-boot — after reboot, no process holds the flock; sentinel's PID may not exist. New attach succeeds. |
| niwa-attach process killed but `claude` survived (parent died, child reparented to init) | Flock dropped (held by parent), claude still running | Flock-free; sentinel file's PID dead. New attach should detect "claude is still in the worktree, may conflict." This is a realistic failure mode worth surfacing. |

**The SSH-disconnect-with-survivor case** is where flock-only fails: the niwa-attach process is alive and holding the flock, but the human is gone. Detection options:

- **Heartbeat from niwa-attach:** niwa-attach writes a timestamp every N seconds to `<worktree>/.niwa/attach.heartbeat`. A new attach attempt that finds the flock held checks heartbeat staleness; older than e.g. 30s → offer `--force`. Adds complexity (goroutine, fsync cost) but is the only way to detect "process is alive but the TTY/SSH session is gone."
- **TTY check from niwa-attach:** niwa-attach periodically calls `unix.IoctlGetTermios(0, ...)` or polls `os.Stdin.Stat()` for `O_RDWR` errors when the TTY closes. When the TTY is gone, niwa-attach exits voluntarily, dropping the flock. This is simpler than a heartbeat and aligns with niwa's "implicit release" preference. A SIGHUP handler that calls `os.Exit` is the cheapest path.
- **No detection (rely on `--force`):** accept that an SSH-disconnected attach holds the lock until manually broken. Document `niwa session detach <id> --force` as the operator escape hatch.

**Recommendation:** combine the SIGHUP handler (so well-behaved SSH disconnects auto-release) with `--force` (operator escape hatch) and skip the heartbeat for v1. The heartbeat adds complexity disproportionate to the rare nohup-style hostile-detach case. This matches niwa's existing "PID + flock + IsPIDAlive" stance — no heartbeat anywhere in the codebase today.

**Cross-platform note:** `IsPIDAlive` start-time check is Linux-only because of `/proc/<pid>/stat`. The flock is portable. If attach lands on macOS later, the start-time check degrades to the conservative "alive if signal 0 succeeds" branch (`liveness.go:31-33`).

#### Sub-question 4: `niwa session detach <id> --force` — operator escape hatch

**Proposal:** Yes, ship `niwa session detach <id> --force` from day one. Behavior:

1. Read the sentinel file. If missing → no lock to break, return success.
2. If the sentinel's PID is alive per `IsPIDAlive`, **prompt** for typed confirmation against the session ID (mirrors `ReadConfirmation` in `internal/cli/destroy.go:120`). With `--force` flag, skip the prompt — but log loudly. Optionally send SIGTERM to the niwa-attach process; this drops the flock.
3. If the sentinel's PID is dead, just remove the sentinel file. The flock is already released (kernel cleaned it up when the process died).
4. Document that breaking a live attach is destructive: any unsaved claude state in the human's terminal is lost.

Race with a still-running attach: yes, this can race. The mitigation is that `--force` requires either a typed confirmation (interactive) or an explicit second `--force` flag (scripted) — this matches the destroy command's typed-confirmation discipline. Even if niwa-attach is still alive when SIGTERM arrives, niwa-attach should handle SIGTERM gracefully: stop the claude child, drop the lock, exit. There is no data corruption risk because the worktree's git state is whatever claude's last write produced; the lock guards the *running session*, not git state.

**Note on naming:** the issue uses "attach"/"detach" terminology. `niwa session detach` (without args) inside the attached terminal means "exit cleanly" (== Ctrl-D out of claude). `niwa session detach <id> [--force]` from outside means "break the lock." The PRD should commit to whether the detach command is two distinct operations or one with context-aware behavior.

#### Sub-question 5: Worktree state on detach (uncommitted changes)

This is where reasoning from precedent gives the clearest guidance.

**Existing destroy behavior** (`internal/cli/destroy.go:108-127`): refuses by default if `ScanInstance` reports any loss; with `--force`, scans and prints losses then requires typed confirmation. The default is *safe* — abort, tell the user, let them fix it.

**Existing branch_warning** (`internal/mcp/handlers_session.go:287-292`): `niwa session destroy` does **not** auto-clean. It tries `git branch -d` (refuses on unmerged), surfaces a warning with the exact manual command to run, and otherwise proceeds. The principle is "do the safe thing, surface the unsafe state, never silently discard work."

**Proposed attach detach behavior:**

| Worktree state on detach | Default action |
|---|---|
| Clean (no diff, no untracked, no stash) | Detach silently. |
| Dirty (unstaged/staged) and/or untracked | **Warn and allow.** Print `git status` summary to stderr. Do not stash, do not abort. The worktree persists; the human's changes survive on disk for the next attach or the next worker spawn. |
| Unpushed commits on `session/<id>` | Warn and allow. Same surface as `branch_warning`. |
| Detached HEAD orphan (e.g. human did experimental rebase) | Warn and allow with stronger language ("commits not on any branch will be unreachable if the session is destroyed"). |

Rejected alternatives:

- **Auto-stash:** breaks the principle "never silently mutate user state." The next worker spawn would get a clean tree it didn't expect. If the human wanted to stash, they can; the warn-and-allow path tells them how.
- **Abort:** too noisy for the common case where the human deliberately left a WIP for the next session run. Wraps the human's flow in a confirmation dialog they will learn to ignore.
- **Prompt:** TTY-only. Breaks scripting; adds asymmetry with the existing destroy commands which only prompt when there's loss.

A bonus the warn-and-allow default gives us: **the worker that takes the session back over after detach inherits a possibly-dirty tree.** That's actually fine — the worker can `git status` itself and decide. But the PRD should commit on whether the daemon waits for the human to push, or whether the next worker spawn happens automatically. (Open question; this lead's scope is the lock, not the post-detach handoff.)

#### Sub-question 6: Concurrent worker behavior at attach time

The issue locks in: "wait for running worker to finish naturally; `--force` SIGTERM-s it."

**How is "currently running" detected today?** Exactly one way, used consistently across destroy and the daemon: walk `<mainInstanceRoot>/.niwa/tasks/*/state.json`, find tasks with `state == "running"` whose envelope is in this session's worktree inbox (`taskInWorktreeInbox` in `handlers_session.go:370`), and check `IsPIDAlive(st.Worker.PID, st.Worker.StartTime)`.

**Proposal:** `niwa session attach <id>` does the following at acquire time:

1. Validate session exists and is `active` (per the issue: refuse for `ended`/`abandoned` — though the PRD should commit; forensics-only mode might be useful).
2. Take a `non-blocking` flock on `<worktree>/.niwa/attach.lock`. If contention → reject (sub-question 1).
3. Scan `<mainInstanceRoot>/.niwa/tasks/` for any task whose envelope is in this worktree's inbox AND state is `running`. (Uses the same `killSessionWorkers` walk, but read-only.)
4. If such a task exists:
   - **No `--force`:** release the attach lock, poll the task state every ~500ms (re-acquiring per check) until it transitions to a terminal state. Show `Waiting for worker on task <id>...` to the user. Re-take the attach lock.
   - **`--force`:** SIGTERM the worker's process group (`syscall.Kill(-pid, SIGTERM)`), wait `NIWA_DESTROY_GRACE_SECONDS` (default 5s, env-overridable per `daemon.go:175`), SIGKILL if still alive. Then proceed.
5. **Pause daemon claims for this worktree.** This is the subtle part. The per-worktree daemon's fsnotify watcher will keep claiming new envelopes from the inbox unless we tell it to stop. The cleanest mechanism is:
   - A sentinel file `<worktree>/.niwa/attach.lock` (the same file as the flock) presence-checked by `handleInboxEvent` before claiming. When attached, the daemon sees the sentinel and skips. This requires a small daemon change (one file-stat per inbox event).
   - **Alternatively:** stop the per-worktree daemon entirely while attached and restart it on detach. Re-using `TerminateDaemon` (`internal/workspace/daemon.go:118`) and `EnsureDaemonRunning` is a clean stop/start. Trade-off: tasks delegated *during* attach sit in the inbox un-watched but un-deleted, then the restarted daemon's `scanExistingInboxes` (`mesh_watch.go:275`) catches up. This actually matches the issue's locked-in behavior 3 ("tasks queue outside; pending state becomes visible when the human detaches") perfectly.

The "stop daemon, restart on detach" pattern is preferable because (a) it requires no changes to `handleInboxEvent`, (b) it leverages the existing catch-up replay path, and (c) it aligns conceptually — the daemon's whole purpose is automated task execution; suspending the daemon for the duration of human attach is the right primitive.

## Implications

For the PRD's **lock-ownership** open question:

- The lock should be a flock on `<worktree>/.niwa/attach.lock`, held by the niwa CLI process for the duration of the attach. Implicit release via flock-on-fd lifetime.
- Recovery is hybrid: implicit on process death + a `niwa session detach <id> --force` operator escape for the SSH-disconnect-with-survivor case.
- A sentinel JSON file `<worktree>/.niwa/attach.state` (atomically written) carries the human-readable metadata (`attached_pid`, `start_time`, `attached_at`) that `niwa session list` can render. Use `IsPIDAlive` for stale checks; this is a write-once-on-acquire artifact, not a heartbeat.
- No FIFO queue; concurrent attaches reject with a clear error message pointing at `--force`.

For the PRD's **worktree-state-on-detach** open question:

- Default: warn and allow. Print `git status` summary, list any unmerged commits on `session/<id>` (re-using the `branch_warning` style message), exit 0.
- Do not auto-stash; do not abort. This matches the existing `branch_warning` precedent (warn loudly, never auto-clean).
- Operators who want hard cleanup can chain `niwa session destroy <id> [--force]` after detach — that path already has the right semantics for forced cleanup.

For the **concurrent-worker** open question:

- "Wait" is implementable today by polling `<mainInstanceRoot>/.niwa/tasks/*/state.json` for tasks whose envelope lives in this worktree's inbox until they reach a terminal state. The per-worktree daemon should be stopped via `TerminateDaemon(worktreePath)` (existing helper) for the duration of attach, and `EnsureDaemonRunning` re-spawned on detach. Catch-up replay handles in-flight queue.
- `--force` reuses the destroy command's worker-SIGKILL pattern (`killSessionWorkers` / `killRunningWorkerPGIDs`), which already kills the worker's process group. Workers are spawned `Setsid=true` so PID == PGID — no surprise here.

## Surprises

1. **No flock anywhere on the session lifecycle JSON.** The per-session state files at `<instance>/.niwa/sessions/<id>.json` rely entirely on atomic `tmp+rename` for write coordination (`session_lifecycle.go:52-66`). The design comment at `DESIGN-mesh-session-lifecycle.md:251-254` explicitly notes shared-file alternatives were rejected because "concurrent writes from independent worktree daemons require a shared file lock." Translation: writes to lifecycle JSON are single-writer per session by construction. An attach-lock that mutates lifecycle JSON would be a new pattern; better to keep attach-lock state in its own file.

2. **The session destroy path doesn't actually need the daemon to be alive.** `docs/guides/sessions.md:278-281`: "Destroy reads the state file for the worktree path, kills any workers still listed as running, stops whatever daemon process it can find, removes the worktree, and marks the session ended. It doesn't require the daemon to be alive." This is a strong existing pattern: every cleanup operation in niwa is best-effort and idempotent. The attach-detach-with-force path should follow the same discipline.

3. **The per-worktree daemon already has its own `daemon.pid.lock` flock.** The `acquireDaemonPIDLock` mechanism is per-instance-root. For session worktrees, that means `<worktree>/.niwa/daemon.pid.lock` is held by the per-worktree daemon. The attach-lock at `<worktree>/.niwa/attach.lock` is a **separate** flock; the two coexist cleanly. The pattern of multiple flocks under one `.niwa/` dir is precedented (each task has its own `.niwa/tasks/<id>/.lock`).

4. **`Setsid=true` everywhere.** Both the daemon and workers spawn detached from the controlling terminal. The attach process should NOT do this — it must inherit the user's TTY so claude can render. This is a deliberate inversion of the daemon pattern.

5. **`--force` on `niwa session destroy` already toggles `git branch -d` vs `git branch -D`.** If the PRD adds `--force` to `niwa session attach`, it should be careful that the flag means *only* "SIGTERM the running worker" (per the issue's locked-in behavior #1) and nothing else. Overloading is a footgun.

## Open Questions

- **TTY/SSH disconnect detection:** ship the SIGHUP-handler-only approach (no heartbeat) for v1, and accept that nohup-style detached processes need `--force`? Or invest in a heartbeat from day one because hosted SSH proxies frequently swallow SIGHUP? Worth a quick survey of typical niwa user environments.
- **Forensics-only attach to ended/abandoned sessions:** the issue notes "reading back history for forensics may be valuable." Does v1 ship an attach mode for `--readonly` against terminal sessions? If yes, the lock is still valid (no concurrent claude allowed), but the worker-running check is a no-op and the daemon-stop step is a no-op (daemon is already gone). The behavior would degrade gracefully.
- **Daemon stop/start vs sentinel-file-skip:** "stop the daemon for the duration of attach" is conceptually clean but adds latency to attach (5s grace via `TerminateDaemon`) and to detach (must wait for `EnsureDaemonRunning` poll). "Sentinel-file-skip in `handleInboxEvent`" is faster but pollutes the daemon with attach awareness. Both are workable; recommend the daemon-stop approach for v1 because it has zero footprint on the hot path.
- **Cross-instance attach:** the issue scopes v1 to current-workspace-instance. Does the PRD need a `niwa session attach --instance <name> <id>` form for the workspace-root case? If yes, the lock path becomes `<workspaceRoot>/<instance>/.niwa/worktrees/<repo>-<id>/.niwa/attach.lock` and the CLI must accept an instance name. Probably yes for parity with `niwa destroy`.
- **Coordinator-process-registry interaction:** `sessions.json` (the `SessionEntry` registry, distinct from the lifecycle registry per `DESIGN-mesh-session-lifecycle.md:316-319`) tracks coordinator presence. Should the attached human be listed there too? Probably not — the coordinator registry is for MCP-server-attachable identities; attach is a CLI-only flow.
- **Telegram/coordinator notification on attach:** `cross-ref #109` in the issue body. The lead's scope doesn't cover whether the coordinator gets a "session attached" message; flagging here for the parent exploration.

## Summary

The attach lock should be a non-blocking exclusive flock at `<worktree>/.niwa/attach.lock`, held by the foreground `niwa session attach` process which forks `claude --resume` as a child — exactly mirroring how `daemon.pid.lock` already enforces single-daemon-per-instance with implicit release via flock-on-fd lifetime, plus a sibling `attach.state` JSON sentinel (atomic write, `IsPIDAlive`-checked) for `niwa session list` rendering and a `niwa session detach <id> --force` escape hatch for SSH-disconnect-with-survivor cases. The biggest implication is that the per-worktree daemon should be `TerminateDaemon`'d for the duration of attach and re-spawned via `EnsureDaemonRunning` on detach (re-using existing helpers and the catch-up-inbox-replay path) so concurrent worker behavior, queue visibility, and detach handoff fall out for free; uncommitted worktree state on detach should follow the existing `branch_warning` precedent (warn loudly, never auto-clean) rather than auto-stash. The biggest open question is whether v1 invests in a heartbeat to detect SSH-disconnected-but-still-alive niwa-attach processes, or accepts that operators must use `--force` for that rare hostile-detach case — a SIGHUP-handler-only approach is consistent with the codebase but assumes well-behaved terminal sessions.
