<!-- decision:start id="mcp-topology-and-caller-auth" status="assumed" -->
### Decision: MCP Server Topology and Caller Authentication

**Context**

Under the new PRD, the niwa MCP server must serve eleven tools spanning
messaging and task lifecycle. Workers are ephemeral `claude -p` processes
spawned on demand by the daemon; they do not register in `sessions.json`
(R40) but must be authorized when they call `niwa_finish_task`,
`niwa_report_progress`, and similar tools that mutate specific tasks.
Coordinators do register in `sessions.json` (R39) and must be authorized
as delegators for `niwa_update_task`, `niwa_cancel_task`, `niwa_await_task`,
and `niwa_query_task`. The design must pick a topology for the MCP
server, a coordination mechanism between MCP servers and the stateless
daemon, and an identity channel tight enough to defeat
prompt-injection-driven control-plane attacks while accepting the PRD's
stated trust-boundary ceiling of "role integrity" (same-UID malicious
processes are not defended against in v1).

Three topologies were considered: per-session stdio MCP server (as
today), a single per-instance daemon-hosted MCP server with per-session
relays, and a hybrid. The status-quo pattern (per-session stdio server
plus fsnotify + per-process waiter map) is proven in
`internal/mcp/server.go` for `niwa_ask` reply correlation; the question
is whether that pattern extends cleanly to task lifecycle tools, or
whether cross-session coordination of waiters and authorization warrants
a different architecture.

**Assumptions**

- Claude Code inherits env vars from the parent `claude -p` process into
  the MCP server subprocess it spawns (standard POSIX behavior; already
  relied on by existing niwa code for `NIWA_INSTANCE_ROOT`,
  `NIWA_SESSION_ROLE`, `NIWA_SESSION_ID`). If this ever stops being true,
  the design falls back to a token-file identity channel.
- `claude -p` inherits env from the daemon exactly as the daemon specifies
  it via `exec.Command`. Niwa controls the daemon's spawn code, so this
  is a design choice, not an external dependency.
- fsnotify latency on supported platforms is ~10 ms, well under PRD R22's
  5-second delivery bound.
- `sessions.json` continues to carry only coordinator entries (R40); the
  design adds no worker entries.
- A worker's MCP server serves exactly one task's identity for its
  lifetime (one worker per task; at most one worker per role).

**Chosen: Per-session stdio MCP server + filesystem coordination + env-var task identity + state.json authorization**

The niwa MCP server remains a stdio subprocess spawned per Claude session
by Claude Code from `.claude/.mcp.json`. This is true for every caller
class: the long-lived coordinator, peer-to-peer sessions, and ephemeral
`claude -p` workers. No single per-instance MCP server, no daemon-hosted
MCP server, no Unix-socket relay.

Coordination between MCP server instances and the daemon is
filesystem-only. The daemon writes per-task state to
`.niwa/tasks/<T>/state.json` (extended to include `worker_pid`,
`worker_start_time`, `worker_role` at spawn time). MCP tool calls read
these files under shared `flock` for authorization, and read/write
`inbox/` directories for message delivery. The existing fsnotify-based
`notifyNewFile` pathway is extended to recognize task lifecycle message
kinds (`task.completed`, `task.abandoned`, `task.cancelled`) and to
signal a new `awaitWaiters map[string]chan taskEvent` (keyed on task_id)
for `niwa_await_task` and synchronous `niwa_delegate`.

Worker identity is established at MCP server startup via env vars
injected by the daemon when it spawns `claude -p`:

```
NIWA_INSTANCE_ROOT=<instance-root>
NIWA_SESSION_ROLE=<target-role>   # the role whose inbox this worker claims
NIWA_TASK_ID=<task-id>            # identity of this spawn's task
```

The MCP server reads these once on startup and stores them in the
`Server` struct as `instanceRoot`, `role`, and `taskID`. Coordinators
have no `NIWA_TASK_ID`; their `taskID` is empty. Peer/non-worker sessions
without a registered coordinator role have `role` set from their
registration via the SessionStart hook, matching the existing pattern.

Authorization is uniform across tool handlers and uses a single helper:

```go
// authorizeTaskCall reads envelope.json + state.json under shared flock
// and returns a structured error if the caller (identified by s.role and
// s.taskID) is not authorized for the requested access kind on taskID.
func (s *Server) authorizeTaskCall(taskID string, kind accessKind) (*envelope, *taskState, *toolResult)
```

- `kindDelegator` (for `niwa_await_task`, `niwa_update_task`,
  `niwa_cancel_task`): envelope.from.role must equal s.role; else
  `NOT_TASK_OWNER`.
- `kindExecutor` (for `niwa_finish_task`, `niwa_report_progress`): s.taskID
  must equal the requested task_id, and state.json.worker_role must equal
  s.role. If s.taskID is empty or mismatched: `NOT_TASK_PARTY`. If state
  is already terminal: `TASK_ALREADY_TERMINAL`.
- `kindParty` (for `niwa_query_task`): accept if either the delegator
  check or the executor check passes; else `NOT_TASK_PARTY`.
- Other tools (`niwa_delegate`, `niwa_ask`, `niwa_send_message`,
  `niwa_check_messages`, `niwa_list_outbound_tasks`): no per-task
  authorization; the caller's role from env is the envelope `from.role`
  on any messages they send. If `s.taskID` is non-empty, delegated
  children automatically populate `parent_task_id = s.taskID` per R15.

Cross-session waiter signaling extends the existing `notifyNewFile`
pattern. Today it checks `reply_to` against `waiters`. The extended
function additionally inspects the message type: if the type is
`task.completed`, `task.abandoned`, or `task.cancelled`, and the message
body carries a `task_id` matching a registered `awaitWaiters` entry, the
waiter's channel receives the resolved tool result. `niwa_await_task`
registers its waiter with a `defer cancel()` before checking state.json
for an already-terminal state (race guard mirroring the existing
`handleAsk` pattern).

Optional hardening layer: on Linux, `niwa_finish_task` and
`niwa_report_progress` may additionally verify that the MCP server's
process ancestry (via `/proc/<pid>/stat` one hop up) reaches
`state.json.worker_pid`, using the existing `PIDStartTime` cross-check
from `internal/mcp/liveness.go` to defeat PID recycling. This check is
best-effort and skipped on platforms where it is not reliable; the
primary identity assertion is env + state.json freshness
(`worker_start_time`).

**Rationale**

The chosen design is the minimum delta from the existing codebase that
satisfies every PRD authorization requirement. The per-session stdio
topology is already how niwa's MCP server works, and matches Claude
Code's launch model — there is no supported way to redirect Claude's
MCP subprocess to a shared socket without building a relay, and a relay
would not eliminate the need for filesystem state anyway. The
filesystem-based coordination pattern aligns with the PRD's threaded
principle ("stateless daemon, durable state on disk") and carries
through from Decisions 1 and 2 of this design. The env-var identity
channel reuses the mechanism niwa already uses for session identity
(`NIWA_SESSION_ID`, `NIWA_SESSION_ROLE`) and does not require new
cryptographic primitives. Authorization decisions are made against
information the daemon writes to disk at spawn time, so a worker cannot
claim an identity the daemon did not assign without also being able to
modify state.json — which is the same trust ceiling the PRD explicitly
bounds.

Against the prompt-injection attack class the PRD explicitly cares
about, the design is tight: a task's body cannot modify the worker's env
or its `state.json`, and every authorization check is rooted in
startup-fixed env + daemon-written state.json. Against same-UID
malicious processes, the design is exactly as permeable as the PRD
acknowledges — this is the stated ceiling.

**Alternatives Considered**

- **Single per-instance daemon-hosted MCP server with Unix-socket
  relays**: every `niwa mcp-serve` invocation becomes a JSON-RPC relay
  that proxies tool calls to a server embedded in `niwa mesh watch`.
  In-process waiter signaling would give microsecond cross-session
  coordination. Rejected because (a) PRD R22's 5-second bound is
  comfortably met by fsnotify's ~10 ms latency, so the perf advantage is
  irrelevant; (b) introducing a new Unix-socket IPC layer, framing, and
  reconnect logic adds several hundred lines of code and a new failure
  mode (daemon crash = all tool calls fail); (c) violates the PRD's
  "stateless daemon" principle threaded through decisions 1 and 2 of the
  design; (d) makes crash recovery require reconstructing waiter state
  from disk anyway, eliminating the coordination advantage.

- **Per-session stdio server + token-file identity channel**: daemon
  writes `.niwa/tasks/<T>/worker.token` with a high-entropy value and
  exports `NIWA_TASK_TOKEN`; MCP server compares on every privileged
  call. Rejected as primary because `state.json.worker_start_time` +
  `NIWA_TASK_ID` provides equivalent defense (restart rotates
  `worker_start_time`; a dying worker's stored identity no longer matches).
  Retained as a contingency migration path if PID-based hardening proves
  fragile in practice; the tool API and authorization semantics are
  identical.

**Consequences**

*Positive*
- No new dependencies, no new IPC surface, no new failure modes. The
  design extends existing machinery (waiter map, fsnotify watcher,
  env-var identity, flock on state files).
- Daemon remains stateless from a correctness perspective; crashing and
  restarting the daemon does not invalidate any in-flight tool calls.
- Authorization is uniformly file-backed: `envelope.json` for delegator
  role, `state.json` for executor identity. Both are atomically written
  by the sole writers (envelope by the delegator once at creation,
  state.json by the daemon and the worker's `finish_task` path under
  flock).
- Prompt-injection resistance is real: the worker's LLM cannot modify
  the env or state.json of the MCP server process it is running
  under, so authorization claims it forges are rejected at the server
  boundary.
- `parent_task_id` auto-population (R15) is a one-line read of
  `s.taskID` during envelope construction.
- Cross-session waiter signaling reuses the proven `notifyNewFile` path;
  the code diff is adding three event kinds and one waiter map.

*Negative / Trade-offs*
- Every privileged tool call performs a state.json read (shared flock);
  at PRD-stated cadence this is negligible, but under pathological
  load it could become a bottleneck. Deferred as out-of-scope for v1.
- Waiter signaling depends on fsnotify being operational; `watchInboxPolling`
  fallback exists but adds latency up to the poll interval.
- Worker identity relies on env-var inheritance through two exec layers
  (daemon → claude → MCP subprocess). Verified by functional test
  using `NIWA_WORKER_SPAWN_COMMAND` with a scripted fake that asserts
  it receives `NIWA_TASK_ID`. If Claude Code ever drops env inheritance,
  the design requires migration to token-file identity (C) — API-compatible.
- PID ancestry verification is platform-specific; treated as optional
  hardening rather than required auth. On macOS, identity rests
  entirely on env + state.json freshness (no weaker than the PRD
  ceiling).
- The `awaitWaiters` map keyed on `task_id` introduces a new waiter
  class alongside `reply_to` and `typeWaiters`; the state must be
  correctly cleaned up on timeout (existing `defer cancel()` pattern
  applies).

*Implementation checklist*
- Extend `mcp.Server` struct with `taskID string`.
- Extend `state.json` schema to include `worker_pid`, `worker_start_time`,
  `worker_role`, `spawned_at` (written atomically by daemon at spawn).
- Add `authorizeTaskCall(taskID, kind)` helper in `internal/mcp/server.go`.
- Add `awaitWaiters map[string]chan taskEvent` alongside existing `waiters`.
- Extend `notifyNewFile` to route `task.completed`, `task.abandoned`,
  `task.cancelled` to `awaitWaiters[body.task_id]`.
- Daemon spawn code (R33) sets `NIWA_TASK_ID`, `NIWA_SESSION_ROLE`,
  `NIWA_INSTANCE_ROOT` (plus PATH, HOME, USER) on the `claude -p`
  subprocess env.
- `niwa mcp-serve` reads `NIWA_TASK_ID` on startup; passes to `mcp.New`.
- Functional test via `NIWA_WORKER_SPAWN_COMMAND`: scripted worker asserts
  it received `NIWA_TASK_ID`; calls `niwa_finish_task` and observes
  state transition; a second scripted worker with a forged `NIWA_TASK_ID`
  for a different task is rejected with `NOT_TASK_PARTY`.
<!-- decision:end -->
