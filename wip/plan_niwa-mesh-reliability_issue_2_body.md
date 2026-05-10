---
complexity: testable
complexity_rationale: Behavior change in the spawn-success contract — flips a silent nil-return into a typed error, with rollback paths for worktree, branch, and session-state file. Touches `internal/workspace/daemon.go` and `internal/mcp/handlers_session.go`; adds three named test cases.
---

## Goal

Make synchronous spawn failures observable by returning a typed `ErrDaemonSpawnTimeout` from `EnsureDaemonRunning`'s 500 ms wait and rolling back the worktree, branch, and session-state file in `handleCreateSession` when that error class fires.

## Context

Design: `docs/designs/current/DESIGN-niwa-mesh-reliability.md`

The daemon writes `daemon.pid` only after fsnotify registration succeeds (`internal/cli/mesh_watch.go:283-287`); that's the canonical "ready" signal. `EnsureDaemonRunning` (`internal/workspace/daemon.go:35-102`) already polls for `daemon.pid` for 500 ms but returns nil on timeout with the comment "Return nil so Create/Apply still succeed; the missing PID file is the observable failure signal." That contract masks the inotify-exhaustion failure mode reported in #110: `niwa_create_session` returns success with `status=active` while no daemon is alive to claim work.

Existing pre-spawn errors (binary missing, mkdir failures, log open failures, `cmd.Start()` failure) already return non-nil errors from this function — those keep their current return paths.

Closes #110.

## Acceptance Criteria

- [ ] New error sentinel `ErrDaemonSpawnTimeout` exported from `internal/workspace/daemon.go`.
- [ ] `EnsureDaemonRunning` returns `ErrDaemonSpawnTimeout` (instead of nil) when the 500 ms PID-file poll times out.
- [ ] `handleCreateSession` (`internal/mcp/handlers_session.go:146-228`) handles `ErrDaemonSpawnTimeout` by rolling back the worktree (existing `cleanupWorktree` defer pattern at lines 194-208), the branch, and the session-state file; returns `errResult` with structured error code `DAEMON_SPAWN_TIMEOUT`.
- [ ] Successful spawn paths are unchanged (verified by existing tests).
- [ ] Pre-spawn errors (mkdir/log/cmd.Start failures) keep their current return paths and result in the same rollback behavior in `handleCreateSession`.
- [ ] Functional test (a) — inotify exhaustion: simulate by exhausting inotify instances; `niwa_create_session` returns `DAEMON_SPAWN_TIMEOUT` and no session state file remains.
- [ ] Functional test (b) — missing/non-executable target binary: simulate by setting `NIWA_WORKER_SPAWN_COMMAND` to a non-existent path; `niwa_create_session` returns the existing pre-spawn error and rolls back.
- [ ] Functional test (c) — daemon-internal PID file write failure: simulate by making `<niwa>/daemon.pid` unwritable (e.g., via permissions); `niwa_create_session` returns `DAEMON_SPAWN_TIMEOUT` (the daemon writes the PID file at startup; failure to write means daemon.pid never appears, so the timeout fires).
- [ ] Must deliver: `DAEMON_SPAWN_TIMEOUT` error code in the MCP response surface (required by <<ISSUE:8>> for the sessions guide and skill rewrite).

## Dependencies

None. Phase 2 (timeout half) is independent of all other issues and can ship in parallel with <<ISSUE:1>>, <<ISSUE:3>>, <<ISSUE:4>>, <<ISSUE:5>>.

## Downstream Dependencies

- <<ISSUE:8>> documents the new error code and rollback contract in `docs/guides/sessions.md` and the niwa-mesh skill.
