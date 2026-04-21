# Decision 1: Daemon Lifecycle Management

## Context

`niwa mesh watch` is a persistent background process that watches all session inbox
directories under `<instance-root>/.niwa/sessions/` via fsnotify and resumes idle Claude
sessions via `claude --resume <claude-session-id>` when messages arrive for sessions
whose PID is dead. The question is: how should this daemon be started, supervised, and
stopped as part of the workspace lifecycle?

The daemon must be running before the first Claude session opens (so it is ready to
resume sessions) and must stop cleanly when the workspace is destroyed. It must survive
its own crash restartably without message loss, because the inbox is file-based and
stateless.

## Key assumptions

- `niwa apply` is the canonical workspace provisioning step, and its `runPipeline` is
  where all workspace infrastructure is written. The daemon must start at the tail of
  this step.
- `niwa destroy` calls `workspace.DestroyInstance`, which currently removes the instance
  directory after stopping nothing. It must be extended to stop the daemon first.
- The daemon is fully stateless: all durable state is the inbox filesystem. A crash and
  restart loses no messages.
- PID tracking must use the same start-time verification pattern as `internal/mcp/liveness.go`
  (`IsPIDAlive` + `/proc/<pid>/stat` on Linux) to prevent false positives from PID recycling.
- The user must not need to manually run `niwa mesh watch`. Zero-friction provisioning
  requires automatic startup.
- Pure Go, no external process supervisors (systemd, launchd) as a hard requirement for
  the primary use case.
- The daemon is workspace-instance-scoped: one daemon per instance, not one per machine
  or per workspace root.

## Chosen: Option B — `niwa apply` spawns the daemon directly via exec

At the end of the `ChannelMaterializer.Materialize` call (or as a dedicated final step
in `runPipeline` after the materializer writes the sessions directory), `niwa apply`
checks whether a live daemon is already running for this instance (by reading
`<instance-root>/.niwa/daemon.pid` and calling `IsPIDAlive`). If not, it spawns
`niwa mesh watch --instance-root=<instance-root>` using `os.StartProcess` or
`exec.Command` configured so the child:

- Inherits no open file descriptors from the parent (`cmd.Stdin/Stdout/Stderr = nil`,
  redirected to `/dev/null` or a log file).
- Runs in its own process group (`cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}`)
  so it is not killed when the user's shell session ends.
- Writes its PID and start time to `<instance-root>/.niwa/daemon.pid` atomically before
  entering the watch loop (write to `.niwa/daemon.pid.tmp`, then rename).

The PID file format stores two fields: the integer PID and the jiffies-since-boot start
time (from `/proc/<pid>/stat`), so the `IsPIDAlive` function from `internal/mcp/liveness.go`
can be called directly by `niwa apply` (idempotency check) and by `niwa destroy` (stop
check).

`niwa destroy` is extended before calling `DestroyInstance`:

1. Read `<instance-root>/.niwa/daemon.pid`.
2. Call `IsPIDAlive(pid, startTime)`.
3. If alive, send `SIGTERM` via `syscall.Kill` and wait up to 5 seconds for the process
   to exit (polling the PID file for disappearance or confirming via `IsPIDAlive`).
4. If the daemon does not stop within 5 seconds, send `SIGKILL`.
5. Remove `daemon.pid` if it still exists.
6. Proceed with `DestroyInstance` (which removes the instance directory, taking the
   sessions directory with it).

The daemon itself handles `SIGTERM` via a `signal.NotifyContext` or `os/signal.Notify`
channel, closes the fsnotify watcher, waits for any in-flight `claude --resume`
subprocesses to exit (or kills them after a short grace period), and exits cleanly.

### PID file schema

```
<pid>\n
<start-time-jiffies>\n
```

Written atomically: `<instance-root>/.niwa/daemon.pid.tmp` then renamed to
`<instance-root>/.niwa/daemon.pid` after the daemon has established its watch loop.
This ensures `niwa apply` does not see a partial PID file as a valid daemon.

### Idempotency on re-apply

`niwa apply` checks the PID file before spawning. If a live daemon is already running,
it skips the spawn. This makes repeated `niwa apply` calls safe.

### Crash recovery

If the daemon crashes, `daemon.pid` remains on disk but `IsPIDAlive` returns false (the
PID no longer exists or was recycled, and the start time will not match). The next
`niwa apply` detects this and spawns a fresh daemon. Messages that arrived during the
daemon's downtime are not lost — they sit in the inbox directories — and the fresh
daemon's initial fsnotify scan will fire `CREATE` events for existing files it has not
yet processed, or the `SessionStart` hook will surface them when sessions next open.

### Integration point in runPipeline

The spawn is not a materializer; it is a lifecycle action. It runs as a final step in
`Applier.Apply` and `Applier.Create`, after all materializers complete and after
`SaveState` writes the instance state. This ordering ensures that `sessions/` exists and
`sessions.json` is initialized before the daemon starts watching. The spawn step is
gated on `cfg.Channels` being non-empty (same condition as `ChannelMaterializer`).

## Rationale

### Why Option B over Option C (SessionStart hook)

Option C is appealing because it is self-healing: every session open checks and starts
the daemon if needed. The problem is the constraint that the daemon must be running
**before** the first session opens. If the first session opens before the daemon is
running, messages sent during that window will sit in inboxes until the session opens
again or the user runs something that triggers the hook a second time. More critically,
a `SessionStart` hook runs inside a Claude Code session's tool invocation context and has
a time budget. Spawning a long-lived daemon from inside a hook introduces latency and
creates a situation where the daemon's startup is dependent on session lifecycle — the
opposite of what is wanted. Option B starts the daemon at `niwa apply` time, before any
session ever opens, which is the right causal ordering.

Option C also creates a subtle race: if two sessions open concurrently (common in a
multi-repo workspace where the user launches all terminals at once), each hook could
detect no live daemon and both could try to spawn one. A PID file with atomic rename
handles this, but that mechanism is exactly what Option B already implements. There is
no advantage to putting it in a hook.

### Why Option B over Option A (startup script)

Option A requires the user to run something extra — either manually or via a shell init
hook. The PRD states "the user should not need to manually run `niwa mesh watch`." Shell
init hooks (`~/.bashrc`) are not a reliable substitute: they do not fire at workspace
apply time, and users may not have shell integration installed. Option A is a fallback
mechanism, not a primary lifecycle approach.

### Why Option B over Option D (embedded in `niwa mcp-serve`)

Option D collapses the daemon into the first MCP server instance that starts. This is
fragile because `niwa mcp-serve` is a session-scoped process: it exits when the Claude
session closes. If the first session closes, the embedded daemon dies, and the remaining
sessions lose their watcher until one of them starts another MCP server. The PRD
requires a workspace-scoped daemon, not a session-scoped one.

Option D also creates a split responsibility: `niwa mcp-serve` must both serve its
session's MCP protocol and watch all other sessions' inboxes. The "only the first
instance does global watching" logic requires coordination (another PID file, same
mechanism as Option B) and is harder to reason about. Separating concerns — each
`mcp-serve` watches its own inbox, `mesh watch` watches all inboxes for the resume path
— is cleaner and testable in isolation.

### Why Option B over Option E (systemd/launchd)

Option E provides the best supervision story on Linux (automatic restart, journald
logging, `systemctl stop` for clean shutdown) but introduces a mandatory external
dependency on the OS init system. macOS requires a separate launchd plist path. The
constraint is "pure Go, no external deps beyond stdlib." More practically, writing
systemd units requires root or at minimum a user systemd instance, which is not
universally available in containerized or CI environments. Option B's self-managed PID
file costs one page of Go code and removes this dependency entirely.

## Rejected alternatives

### Option A: startup script written by `niwa apply`

`niwa apply` writes `<instance-root>/.niwa/start-daemon.sh`. The user or a shell init
hook runs it. Rejected because it violates the zero-friction requirement: users must not
need to manually start the daemon. Shell init hook integration is unreliable (only fires
in interactive shells, not in CI or non-interactive spawns). The lifecycle is not tied to
`niwa apply`/`niwa destroy` without additional instrumentation.

### Option C: SessionStart hook starts the daemon if not already running

Every Claude session's SessionStart hook checks whether a live daemon exists and spawns
it if not. Rejected because: (a) the daemon must exist before the first session opens,
not when it opens; (b) concurrent session starts race on spawn; (c) running a daemon
spawn inside a hook adds latency and coupling to session lifecycle; (d) the mechanism
required (PID file + atomic spawn) is identical to Option B, so there is no simplification
— only worse causal ordering.

### Option D: daemon embedded in `niwa mcp-serve` as a side effect

The first MCP server instance also watches all session inboxes. Subsequent instances
skip this if a global watcher is already running. Rejected because: `niwa mcp-serve` is
session-scoped and exits when its session closes, so the embedded daemon has the same
lifetime as the session — not the workspace. When the first session closes, the daemon
dies. The coordination logic (who holds the global watcher?) is the same PID-file
mechanism as Option B but embedded in a component with a different ownership boundary.

### Option E: systemd/launchd unit written by `niwa apply`

`niwa apply` writes a systemd user unit or launchd plist and enables it. Rejected
because: (a) violates the "no external deps beyond stdlib" constraint; (b) requires
user systemd or launchd to be available and configured, which is not guaranteed in
containers, CI, or macOS without Homebrew; (c) different code paths for Linux vs macOS
add maintenance burden; (d) `niwa destroy` must integrate with the OS service manager
to stop the unit, adding complexity with no benefit that a simple SIGTERM cannot provide.

## Consequences

- Positive:
  - The daemon starts at `niwa apply` time, before any session opens, satisfying the
    causal ordering requirement.
  - `niwa destroy` can stop the daemon cleanly with SIGTERM + wait before removing the
    directory, preventing the daemon from watching a directory that is being deleted.
  - Crash recovery is automatic: the next `niwa apply` detects the stale PID and spawns
    a fresh daemon without any user action.
  - The same `IsPIDAlive` function used by `internal/mcp/liveness.go` is reused, keeping
    PID tracking consistent across the codebase.
  - Idempotent: repeated `niwa apply` calls do not spawn duplicate daemons.
  - Pure Go, no OS-level service manager dependency.

- Negative:
  - The daemon is not automatically restarted if it crashes between `niwa apply` runs.
    It stays down until the user runs `niwa apply` again or until a new feature (Option C
    as a secondary check) is added. Sessions that arrive during downtime queue their
    messages durably but do not get resumed until the daemon restarts.
  - The daemon runs unsupervised with no automatic log rotation. Log output (if any) must
    be directed to a file under `.niwa/` explicitly; `niwa apply` does not set up log
    rotation.
  - `SysProcAttr` with `Setsid: true` is Unix-specific. A Windows port would require
    a different detachment mechanism (though Windows is out of scope for v1).

- Mitigations:
  - For daemon downtime between applies: `niwa apply` is designed to be re-run at any
    time; users can run it to restart a crashed daemon. A future `niwa mesh restart`
    command could expose this more explicitly. Alternatively, Option C (SessionStart hook
    as a secondary check) can be added as a belt-and-suspenders measure without replacing
    Option B as the primary mechanism.
  - For logging: direct stdout and stderr of the daemon to
    `<instance-root>/.niwa/daemon.log` (rotated by size on open, max 1 MB). This is a
    single `os.OpenFile` call in the spawn path.
  - For Windows portability: the `SysProcAttr` field is set conditionally behind a
    build tag (`//go:build !windows`) if Windows support is ever added.
