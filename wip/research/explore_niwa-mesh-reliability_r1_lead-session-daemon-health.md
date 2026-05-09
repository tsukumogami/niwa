# Lead: What signals does the daemon spawner have about spawn success and runtime liveness, and how could those surface synchronously through `niwa_create_session` and `niwa_list_sessions`?

## Findings

### 1. The daemon's startup contract

The daemon is the long-running `niwa mesh watch` process. Its startup
sequence is in `internal/cli/mesh_watch.go`, function `runMeshWatch`:

- `internal/cli/mesh_watch.go:156-176` — opens `<niwa>/daemon.log` and
  writes `daemon starting pid=<n> instance-root=<...>`.
- `internal/cli/mesh_watch.go:188-196` — resolves the worker spawn binary
  (`claude` or `NIWA_WORKER_SPAWN_COMMAND` override). On failure, logs
  `fatal: cannot resolve worker spawn binary: ...` and returns a non-nil
  error. The daemon exits BEFORE writing daemon.pid.
- `internal/cli/mesh_watch.go:201-212` — `fsnotify.NewWatcher()` and
  `registerInboxWatches()`. **This is the failure mode in #110**:
  fsnotify can fail with `too many open files` on inotify exhaustion.
  The daemon returns the error and exits — again, BEFORE daemon.pid is
  written.
- `internal/cli/mesh_watch.go:246-261` — acquires the exclusive flock on
  `<niwa>/daemon.pid.lock`. A losing race exits silently with
  `another daemon is running; exiting` and returns nil.
- `internal/cli/mesh_watch.go:283-287` — `writePIDFile(niwaDir)` is the
  CANONICAL "spawn succeeded" filesystem signal, followed by
  `logger.Printf("daemon ready, PID file written")` (line 287). The
  comment at `mesh_watch.go:280-282` makes the contract explicit:

  > Write PID file atomically AFTER watches are registered and the lock
  > is held so EnsureDaemonRunning's "pid-file-appears" signal means the
  > daemon really can accept events.

- `internal/cli/mesh_watch.go:2378-2394` — `writePIDFile` writes
  `<pid>\n<starttime>\n` atomically (tmp + rename).

There is NO periodic heartbeat. After the "daemon ready" line, the
daemon's logger only emits on events (fsnotify events, exit notices,
watchdog firings, abandons). Lines 2378–2394 confirm daemon.pid is
written exactly once at startup; no periodic touch. There is no
heartbeat file written by the daemon and no `last_seen` field in any
state file.

### 2. The spawner-side signal: `EnsureDaemonRunning`

`internal/workspace/daemon.go:35-102` — `EnsureDaemonRunning(instanceRoot, extraEnv)`:

- **Line 44**: if a live daemon is already recorded in daemon.pid (PID +
  start time, cross-checked via `kill(0)` and `/proc/<pid>/stat`),
  returns nil immediately.
- **Lines 51-79**: spawn `niwa mesh watch --instance-root=<...>` with
  `Setsid: true`, redirect stdout/stderr to `daemon.log`, call
  `cmd.Start()` (not Run, so it returns immediately).
- **Lines 82-83**: `cmd.Process.Release()` — the parent drops the child.
  At this point `cmd.Start()` has succeeded (the child PID is alive),
  but the child has not necessarily reached "daemon ready" yet.
- **Lines 86-101**: poll for the existence of `<niwa>/daemon.pid` for up
  to 500ms (20ms interval). If it appears within the window, return
  nil. **If it does NOT appear within 500ms, the function STILL returns
  nil with the comment**:

  > // Timed out — daemon may have failed to start (e.g. missing fsnotify
  > // support). Return nil so Create/Apply still succeed; the missing
  > // PID file is the observable failure signal.

This is the core of #110: the spawner has access to a hard signal
(daemon.pid appearing within 500ms) but explicitly throws it away. The
caller can't distinguish "daemon up" from "daemon failed to spawn".

The function only returns a non-nil error for three local pre-spawn
failures:
- `os.Executable()` failure (line 53)
- `os.MkdirAll(niwaDir)` failure (line 58)
- `os.OpenFile(daemon.log)` failure (line 63)
- `cmd.Start()` failure (line 78) — this only fires if exec itself can't
  be launched, e.g. ENOMEM, not for daemon-internal failures.

### 3. The MCP `niwa_create_session` handler

`internal/mcp/handlers_session.go:146-229` — `handleCreateSession`:

- Validates role/repo (lines 147-161).
- Generates session ID via O_EXCL placeholder
  (`session_lifecycle.go:132-151`).
- `git worktree add` to create the branch + worktree (line 188).
- `scaffoldWorktreeNiwa` (line 199) — creates `.niwa/{tasks,sessions,roles/<repo>/inbox/...}`
  plus EMPTY placeholder files `daemon.pid` and `daemon.log` (lines
  97-105 in same file, function definition at 80-108). **Note**: the
  scaffold pre-creates an EMPTY daemon.pid placeholder. This is a
  potential confusion source for any "does daemon.pid exist?" probe —
  the empty placeholder exists before the daemon writes the real
  contents.
- Writes the session lifecycle state with `Status: SessionStatusActive`
  (line 205-209, default from `NewSessionLifecycleState` in
  `session_lifecycle.go:162-177`).
- **Lines 220-225**: invokes `s.daemonStarter` (the injected
  `workspace.EnsureDaemonRunning`). On error, populates
  `resp["daemon_warning"]` with the error message, but **the response
  is still `textResult` (success), not `errResult`**, and the session
  state is left at `Status: active`.

Critical observation: because `EnsureDaemonRunning` returns nil on the
500ms-timeout path, the `daemon_warning` field is essentially
unreachable by the inotify-exhaustion failure mode. It only fires if a
local pre-spawn step fails (mkdir, open log, exec). The #110 failure
class (daemon spawn started but immediately exited) doesn't propagate.

The comment at line 222 reinforces the design intent ("Non-fatal:
session state is written; coordinator can retry daemon start") — but
because the `EnsureDaemonRunning` retry would also see daemon.pid
missing, retrying isn't useful without a stronger probe.

### 4. The MCP `niwa_list_sessions` handler

`internal/mcp/handlers_session.go:26-50` — `handleListSessions`:

- Reads sessionsDir via `ListSessionLifecycleStates`
  (`session_lifecycle.go:94-121`).
- Filters by repo / status (string-equality on the persisted Status
  field).
- Marshals the array of `SessionLifecycleState` objects.

The persisted schema is `SessionLifecycleState`
(`session_lifecycle.go:30-47`):

```go
type SessionLifecycleState struct {
    V                    int    `json:"v"`
    SessionID            string `json:"session_id"`
    ParentSessionID      string `json:"parent_session_id,omitempty"`
    Repo                 string `json:"repo"`
    Purpose              string `json:"purpose"`
    Status               string `json:"status"`              // active|ended|abandoned
    CreationTime         string `json:"creation_time"`
    WorktreePath         string `json:"worktree_path"`
    ClaudeConversationID string `json:"claude_conversation_id,omitempty"`
    CreatorPID           int    `json:"creator_pid"`
    CreatorStartTime     int64  `json:"creator_start_time"`
    BranchWarning        string `json:"-"`                   // never persisted
}
```

`Status` is the ONLY liveness-shaped field, and it's a static label
controlled exclusively by `WriteSessionLifecycleState` calls in the
lifecycle code paths (create writes "active", destroy writes "ended").
**Nothing in `handleListSessions` consults daemon liveness.** No
`kill -0` against the daemon PID, no daemon.pid mtime check, no probe
of any kind. Issue #111's claim is verified: a session whose daemon
crashed minutes ago still reports `status=active` indefinitely until
explicitly destroyed. `CreatorPID`/`CreatorStartTime` describe the
*MCP server* that created the session (typically the coordinator's
mcp-serve), not the daemon.

The user-facing contract in `docs/guides/sessions.md:258-281` already
acknowledges this gap explicitly:

> ## When the session daemon crashes
> If the per-worktree daemon dies after a session is created:
> - The worktree stays on disk.
> - The state file stays with `status: active`.
> - No new tasks can be delivered until the daemon restarts.

Documentation is in sync with the bug; the code makes the user
responsible for correlating an "active" listing with daemon liveness
out-of-band.

### 5. Available signals (without code changes) the API could expose

Each session worktree has a known fixed location for daemon health
state, derived from `WorktreePath`:

| Signal | Source | Cost | Reliability |
|---|---|---|---|
| `daemon.pid` exists with non-empty content | `<worktreePath>/.niwa/daemon.pid` | one stat + read | High — only the daemon writes the real content; the empty placeholder from scaffold is distinguishable by file size > 0 / parseable PID |
| PID is alive | `mcp.IsPIDAlive(pid, startTime)` (`liveness.go:14-35`) — exists, with `kill(0)` + /proc start-time cross-check | one syscall + one read of `/proc/<pid>/stat` | High — already used by `lookupLiveCoordinator` and `EnsureDaemonRunning`; resistant to PID recycling |
| `daemon.log` mtime | `<worktreePath>/.niwa/daemon.log` | one stat | Low — the log is append-only but only appends on events; an idle healthy daemon won't refresh mtime |
| flock probe of `daemon.pid.lock` | `<worktreePath>/.niwa/daemon.pid.lock` | one open + non-blocking flock | Medium — sidecar lock the daemon holds while alive (`mesh_watch.go:246-261`); a successful EX acquire by a probe means the daemon is dead. Probe must release the lock so it doesn't deadlock the next legitimate spawn. |

The daemon does NOT currently write any periodic heartbeat file. The
strongest existing primitive is the `daemon.pid` + `IsPIDAlive`
combination — it already cross-checks the recorded start time against
`/proc/<pid>/stat`, which is exactly what `EnsureDaemonRunning` uses to
decide whether the existing daemon is still real.

### 6. Where a synchronous wait could be added without blocking other tools

Two natural insertion points:

1. **Inside `EnsureDaemonRunning`** (`internal/workspace/daemon.go:91-101`):
   change the return-nil-on-timeout to return a typed error
   (`ErrDaemonSpawnTimeout`) and tighten the polling loop. The MCP
   handler (`handlers_session.go:220-225`) already inspects the error
   to populate `daemon_warning`; the change is purely whether timeout
   counts as an error.

   - 500ms is plenty for inotify-healthy systems; the comment at
     `daemon.go:88-89` confirms <100ms in normal conditions.
   - Could also add: after daemon.pid exists, parse it and call
     `mcp.IsPIDAlive(pid, startTime)` for a ~zero-cost confirmation
     that the recorded PID is actually our spawned child (the daemon
     spawn could have happened, then exited within the same 500ms;
     daemon.pid would still be present from a prior daemon).
   - **No effect on other operations**: `EnsureDaemonRunning` runs only
     synchronously in the calling goroutine; the daemon child is
     already detached via `Setsid` + `Process.Release`. The 500ms
     blocks only the MCP `tools/call` for `niwa_create_session`, and
     the JSON-RPC server already runs `dispatch` synchronously per
     `server.go:178-181`. The watcher goroutine continues independently.

2. **Inside `handleCreateSession`** (`handlers_session.go:220-225`):
   could keep `EnsureDaemonRunning` lenient and add a post-spawn probe
   here (`IsPIDAlive` against daemon.pid, with a short retry budget).
   This decouples the CLI/legacy callers from the new strict semantics
   if backward compat with `EnsureDaemonRunning`'s return contract
   matters.

Either insertion can also flip the response to an `errResult` (so
`niwa_create_session` returns `IsError: true`) and roll back the
worktree + branch + session state file, matching the existing
`cleanupWorktree` defer pattern at `handlers_session.go:194-208`. That
gives synchronous spawn-failure reporting end-to-end.

For `niwa_list_sessions` (`handlers_session.go:26-50`): the cleanest
shape is to enrich each entry in the returned array with a `daemon`
sub-object computed at list time:

```json
{
  "session_id": "ab12cd34",
  "status": "active",
  "daemon": {
    "alive": true,
    "pid": 12345,
    "started_at": "2026-05-09T10:00:00Z"
  },
  ...
}
```

The probe (read daemon.pid from `<worktreePath>/.niwa/daemon.pid`,
call `IsPIDAlive`) is the same primitive used elsewhere in the
codebase. Cost: one stat + one /proc read per session, bounded by the
small number of active sessions per workspace (typically <10).

Whether `Status` should change from "active" to a new value (e.g.
"daemon_dead") is a contract question — keeping the persisted Status
as it is and adding a separate `daemon` field has the advantage that
"active" continues to mean "the lifecycle layer hasn't called destroy"
while the new field reports the orthogonal "daemon process is alive"
question. This composes cleanly with #112's dangling-task work
(dangling tasks correlate with `daemon.alive=false`).

## Implications

1. **#110 is one-line-conceptually**: change
   `daemon.go:101`'s "return nil on timeout" to "return
   ErrDaemonSpawnTimeout" and adjust `handlers_session.go:220-225` to
   roll back the session state on that error class. The infrastructure
   to detect spawn failure (the 500ms poll loop, daemon.pid as the
   ready signal) already exists.

2. **#111 needs a new field, not a Status mutation**: persisted
   `Status` is owned by the lifecycle code path; daemon liveness is
   computed. Surfacing the latter as `daemon: {alive, pid, started_at}`
   keeps the two concerns separable and avoids a write race between
   `niwa_list_sessions` (would-be writer of Status=daemon_dead) and
   `niwa_destroy_session` (writer of Status=ended).

3. **No new heartbeat infrastructure required**. `daemon.pid` plus
   `mcp.IsPIDAlive` (which already does PID-recycle protection via
   /proc start-time) is sufficient. Adding a heartbeat-file writer
   inside the daemon's event loop would be more work and add a new
   failure mode (stale heartbeat ≠ dead daemon).

4. **The empty daemon.pid placeholder created by `scaffoldWorktreeNiwa`
   (`handlers_session.go:97-105`) is a footgun for any liveness probe
   that uses bare existence**. The probe must read + parse the file
   contents (which is already what `ReadPIDFile` does in
   `daemon.go:241-269` — returns `(0, 0, nil)` when missing, but
   returns an error on a present-but-empty file at line 253). Existing
   `IsPIDAlive(0, 0)` correctly returns false at `liveness.go:15-17`.

5. **Two-tier error reporting is natural**: hard pre-spawn errors
   (mkdir / log / exec) → `errResult` rolling back the session;
   spawn-but-not-ready (daemon exited within 500ms) → also `errResult`
   rolling back; spawn-and-ready-but-degraded (e.g. role inbox missing
   but daemon up) → `daemon_warning` and proceed. The current code
   collapses all of these to "warning, proceed".

6. **CLI is already wired for the change**. `runSessionCreate`
   (`session_lifecycle_cmd.go:62-75`) calls `CreateSessionDirect`,
   inspects `result.IsError`, and surfaces stderr — flipping
   `niwa_create_session` to error on spawn failure tightens the CLI
   automatically.

## Surprises

1. **The 500ms poll loop already exists and just doesn't propagate the
   result.** Comments at `daemon.go:86-101` and `mesh_watch.go:280-282`
   describe a contract that's enforced on the daemon's writer side but
   discarded on the reader side. This looks like a deliberate
   permissive-error choice that hardened over time into a bug.

2. **The session scaffold pre-creates an empty `daemon.pid`
   placeholder** (`handlers_session.go:97-105`). This means a naive
   `os.Stat(daemon.pid)` probe will succeed even before the daemon has
   started. Any liveness check must read + parse the contents.

3. **`SessionLifecycleState.CreatorPID` records the MCP server (the
   coordinator's `mcp-serve`), not the daemon**. So even though the
   schema has a PID field, it cannot serve double duty as a daemon
   liveness signal — it's the wrong process.

4. **There is NO periodic heartbeat anywhere in the daemon.** The
   "daemon ready, PID file written" line is the last unconditional log
   entry; subsequent entries are event-driven. An idle, healthy daemon
   could go indefinitely with no log writes.

5. **A second `daemon.pid.lock` flock probe is available as a
   secondary signal.** The daemon holds an exclusive flock on
   `daemon.pid.lock` for its full lifetime (`mesh_watch.go:246-261`).
   A non-blocking EX acquire by a probe that succeeds means the daemon
   is dead. This is more expensive than `IsPIDAlive` but doesn't
   require parsing daemon.pid contents.

## Open Questions

1. **Should `niwa_list_sessions` also auto-prune (transition to
   `abandoned`?) when it observes a dead daemon?** Or is observation
   strictly read-only and abandonment requires explicit `destroy`?
   Auto-prune buys self-healing; read-only preserves the
   single-writer-per-Status invariant. The `lookupLiveCoordinator`
   path (`session_registry.go:78-89`) already does best-effort prune
   of stale coordinator entries from `sessions.json`, so there's
   precedent for auto-prune in the codebase.

2. **What is the right timeout budget for `niwa_create_session` to
   wait for daemon ready?** 500ms covers normal startup. Inotify
   exhaustion fails fast (within milliseconds), so the same window
   catches it. Slow systems (CI, low-end hardware) might need 1-2s.
   Could be exposed as `NIWA_DAEMON_SPAWN_TIMEOUT_MS` env, mirroring
   the `NIWA_DESTROY_GRACE_SECONDS` knob.

3. **Should an `errResult` from `niwa_create_session` also clean up
   the session state file?** Today's flow writes the state file
   BEFORE invoking the daemon starter (line 205 vs line 220). On
   spawn failure, both worktree cleanup AND session-state-file cleanup
   would be needed for full rollback. Leaving the state file behind
   with `status=abandoned` is an alternative — preserves a record for
   list calls, matches the existing destroy-without-daemon path.

4. **Does the daemon need a periodic heartbeat at all?** For the
   present scope, `daemon.pid` + `IsPIDAlive` answers the question
   without one. A heartbeat would only help if we wanted to detect
   "daemon process alive but stuck" (deadlocked event loop). That's
   out of scope for #111 but might surface in #112 (dangling-task
   recovery) — a stuck daemon could leave tasks dangling.

5. **Could the spawner pass a side-channel (anonymous pipe or
   unix-domain socket) instead of polling for daemon.pid?** That
   would give a deterministic "daemon ready" signal with no race and
   no timeout knob. It's heavier infrastructure but eliminates the
   "spawn-and-ready" ambiguity entirely. Probably overkill for the
   reliability pass; flagged here for completeness.

## Summary

The infrastructure for synchronous spawn-success reporting is already
in place — the daemon writes `daemon.pid` only after fsnotify
registration succeeds, and `EnsureDaemonRunning` already polls for
that signal for 500 ms — but the spawner deliberately throws the
result away on timeout, so the inotify-exhaustion failure mode in #110
never reaches the MCP response. For #111, `handleListSessions` returns
the persisted `Status` field verbatim with no daemon-liveness probe,
even though `mcp.IsPIDAlive` (already used by the coordinator-registry
path and by `EnsureDaemonRunning` itself) gives a cheap, PID-recycle-
safe answer. The cleanest fix shape is to make `EnsureDaemonRunning`
return a typed error on the 500 ms timeout (so `niwa_create_session`
can roll back worktree + branch + state and return `IsError`) and
enrich each `niwa_list_sessions` entry with a computed
`daemon: {alive, pid, started_at}` sub-object derived from
`<worktree>/.niwa/daemon.pid` + `IsPIDAlive` — no new heartbeat file
or daemon-side change required.
