---
complexity: testable
complexity_rationale: New API field set computed from existing primitives; introduces a wrapper response struct in the MCP handler to preserve single-writer on the persisted lifecycle file. Cleanly bounded to `handleListSessions` plus the response shape; no daemon-side changes.
---

## Goal

Add a computed `daemon: {alive, pid, started_at}` sub-object to each `niwa_list_sessions` row by introducing a wrapper response struct that embeds the persisted `SessionLifecycleState` plus the daemon health fields probed via `<worktreePath>/.niwa/daemon.pid` and `mcp.IsPIDAlive`.

## Context

Design: `docs/designs/current/DESIGN-niwa-mesh-reliability.md`

`handleListSessions` (`internal/mcp/handlers_session.go:26-50`) currently `json.Marshal`s the persisted `[]SessionLifecycleState` directly. The persisted `Status` field is a static label — it stays "active" even when the daemon has crashed (#111).

`mcp.IsPIDAlive` (`internal/mcp/liveness.go:14-35`) already exists with PID-recycle protection (cross-checks the recorded start time against `/proc/<pid>/stat`); it's used by `lookupLiveCoordinator` and `EnsureDaemonRunning`. The daemon writes `daemon.pid` (atomic, two-line: `<pid>\n<starttime>\n`) only after fsnotify registration succeeds, so a successful read implies the daemon really did become ready.

The design's "Decision Drivers" section commits to keeping `Status` single-writer (owned by the lifecycle code path), so `daemon` must be a computed sub-object on the response — NOT a transient field on the persisted struct. The wrapper-struct approach mirrors the existing `BranchWarning string \`json:"-"\`` pattern but in the opposite direction (compose at marshal time rather than mark the persisted field as non-serializing).

Decision 1 of #116 (deferred PRD) extends this sub-object with `last_claim_at`, `last_progress_at`, `watcher_count` — out of scope for this issue.

Closes #111 (within the scoped field set).

## Acceptance Criteria

- [ ] `handleListSessions` returns a wrapper response struct (e.g., `sessionListEntry`) that embeds the existing `SessionLifecycleState` and adds a `Daemon *daemonInfo \`json:"daemon"\`` field.
- [ ] `daemonInfo` struct shape: `{alive bool \`json:"alive"\`, pid int \`json:"pid"\`, started_at string \`json:"started_at"\`}`.
- [ ] `daemon.alive` is computed by reading `<WorktreePath>/.niwa/daemon.pid` and calling `mcp.IsPIDAlive(pid, startTime)` for each session row.
- [ ] `daemon.pid` reflects the parsed PID from `daemon.pid`; `0` if the file is missing or contains the empty placeholder pre-created by `scaffoldWorktreeNiwa`.
- [ ] `daemon.started_at` is the recorded start time from `daemon.pid`, formatted as RFC3339 (or empty string if unknown).
- [ ] When `daemon.pid` is missing or empty, the response carries `daemon.alive=false, daemon.pid=0, daemon.started_at=""`.
- [ ] `SessionLifecycleState` itself is NOT modified — no transient field added to the persisted struct.
- [ ] Existing top-level fields on the response (`session_id`, `status`, `repo`, `purpose`, `creation_time`, `worktree_path`, `claude_conversation_id`, `parent_session_id`, etc.) keep their position and shape; the `daemon` sub-object is additive.
- [ ] `status` keeps its lifecycle-marker meaning (`active`/`ended`/`abandoned`); it does NOT mutate based on daemon liveness.
- [ ] Probing 10 dead and 10 alive sessions in one `niwa_list_sessions` call completes within reasonable bounds (no daemon-side calls; only filesystem stats and `/proc` reads).
- [ ] Functional test: a session whose daemon was killed reports `daemon.alive=false` while `status=active`.
- [ ] Must deliver: stable `daemon` sub-object shape (required by <<ISSUE:8>> for the sessions guide).

## Dependencies

None. Phase 2 (liveness half) is independent of all other issues and can ship in parallel with <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:4>>, <<ISSUE:5>>.

## Downstream Dependencies

- <<ISSUE:8>> documents the new `daemon` sub-object in `docs/guides/sessions.md` and the niwa-mesh skill.
