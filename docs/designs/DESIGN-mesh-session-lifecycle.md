# DESIGN: Mesh Session Lifecycle

## Status

Proposed

## Context and Problem Statement

Niwa's task infrastructure is built around a single main clone per repo and a
flat role-based routing model. Every `niwa_delegate` call spawns a fresh Claude
process against the main clone; every `niwa_ask` resolves a target by looking
up a role directory under `.niwa/roles/<role>/`. This works for stateless task
dispatch but breaks down in three ways that this design must resolve.

**No persistent Claude context across tasks.** Claude conversation history lives
in a JSONL file keyed to (CWD, session-id). When the daemon spawns a worker for
task B, it starts a fresh Claude process — the session JSONL from task A is
unreachable because the new process generates a new session ID. Coordinators
running multi-step workflows (`/shirabe:design → /shirabe:plan`) must re-state
context with every delegation.

**Main clone branch contamination.** All work happens on the main clone's checked-
out branch. After a coordinator finishes a feature branch and moves on, the repo
stays on that branch. `niwa apply` skips non-default-branch repos, so workspaces
accumulate stale checkouts with no automated recovery path.

**Role-directory routing cannot address tree-structured sessions.** The existing
`niwa_ask` handler validates the `to` field against `.niwa/roles/<to>/` on disk and
looks up the coordinator via a flat `sessions.json` registry. Virtual routing
targets — `"parent"` (resolved from a calling session's recorded parent ID) and
direct child session IDs — have no role directory, so the gate returns `UNKNOWN_ROLE`
before any routing logic runs. A session tree where child sessions can address their
parent cannot be built on top of the role-directory model without extending it.

**System boundaries affected:**
- `internal/mcp/server.go` — `handleAsk`, `isKnownRole`, `handleDelegate`
- `internal/cli/mesh_watch.go` — worker spawn path, `resumeSessionID`
- `internal/cli/shell_init.go` — shell wrapper CWD-change interception
- `internal/cli/go.go` — `niwa go` second-argument extension
- `internal/cli/session.go` — `niwa session list` name collision
- `internal/workspace/state.go` — `EnumerateInstances` (layout-solved)
- `internal/mcp/session_registry.go` — coordinator registry vs. session registry
- New: session state schema, per-worktree daemon lifecycle

## Decision Drivers

- **Layout isolation without code changes:** session worktrees must be invisible to
  `EnumerateInstances` (workspace-root scan) and `EnumerateRepos` (two-level scan).
  Placement under `<instance>/.niwa/worktrees/` satisfies this without touching
  enumeration logic.
- **Backward compatibility is non-negotiable:** `niwa_delegate` without `session_id`,
  `niwa apply`, and existing `niwa_ask(to="coordinator")` must behave identically.
- **Coordinator never handles Claude conversation IDs:** session continuity (JSONL
  path, `--resume` flag) is managed entirely by the daemon and MCP layer.
- **Reuse existing daemon lifecycle:** `EnsureDaemonRunning` in `internal/workspace/`
  is already reusable; per-worktree daemons should start via the same path.
- **Session state survives reboots:** all lifecycle state is file-based; in-memory
  state is not authoritative.
- **Public repo content governance:** no internal references in design or commit
  messages.
- **The `isKnownRole` gate is the primary architectural blocker:** any solution for
  virtual routing targets must either bypass or extend this gate cleanly.
- **`niwa_delegate` routing mechanism is the core open question:** how a coordinator's
  delegate call reaches a per-worktree daemon inbox (not the main instance daemon) is
  unspecified by the PRD and is the highest-priority design question.
