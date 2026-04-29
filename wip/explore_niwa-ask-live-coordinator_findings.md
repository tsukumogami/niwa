# Exploration Findings: niwa-ask-live-coordinator

## Core Question

How should `niwa_ask(to='coordinator')` route to an already-running coordinator session rather
than spawning an ephemeral process? The design centers on piggybacking questions onto the
coordinator's existing polling/blocking calls (`niwa_check_messages`, `niwa_await_task`),
making delivery transparent to both sides without any new notification mechanism.

## Round 1

### Key Insights

- **`handleAsk` always spawns — the session registry is never consulted.** `.niwa/sessions/sessions.json`
  exists with PID tracking and `IsPIDAlive()` but `handleAsk` (server.go:677) never reads it. It
  unconditionally creates a task and spawns an ephemeral worker for every `niwa_ask(to='coordinator')`.
  (lead-daemon-protocol, lead-session-liveness)

- **`niwa_wait` doesn't exist — it's `niwa_await_task`.** The scope and issue used `niwa_wait` as
  a shorthand. The real tool is `niwa_await_task` (handlers_task.go:258). Only unblocks on
  terminal task events (completed/abandoned/cancelled) or timeout. No question-awareness mechanism.
  (lead-daemon-protocol, lead-niwa-wait-semantics)

- **The deadlock is real and unremediated.** Coordinator calling `niwa_await_task` on a worker
  task + that worker calling `niwa_ask(to='coordinator')` = both parties blocking indefinitely
  until the 600s timeout fires. (lead-daemon-protocol, lead-niwa-wait-semantics)

- **No new response tool needed.** Asks are first-class tasks with `{"kind":"ask","body":...}`.
  The asker blocks on `awaitWaiters[taskID]`, which fires when the task reaches terminal state.
  The coordinator answers by calling `niwa_finish_task(outcome=completed, result=answer)` on the
  ask task. The existing task-completion path already unblocks the waiting worker.
  (lead-coordinator-response-api)

- **`niwa_await_task` needs a new early-return case.** When a question arrives in the coordinator's
  inbox while it is blocking on `niwa_await_task`, the daemon needs to deliver it via the existing
  `awaitWaiters` mechanism with a new `question_pending` event kind. The coordinator then loops:
  answer the question via `niwa_finish_task`, re-call `niwa_await_task` with the same task ID.
  Existing move-to-read atomic rename prevents duplicate delivery. (lead-niwa-wait-semantics)

- **Session auto-registration is missing from the MCP path.** Registration is a manual CLI step
  (`niwa session register`). The MCP server never auto-registers. For routing to work, coordinators
  must be registered — either via explicit coordinator startup behavior or auto-registration on
  first MCP call. (lead-session-liveness)

- **Skill content is generated Go code, not markdown.** `buildSkillContent()` in `channels.go`
  produces the SKILL.md installed into every session. Updating the coordinator's loop guidance
  requires editing Go code, not markdown files. (lead-mesh-skill-docs)

### Tensions

- **Response mechanism ambiguity.** Lead 3 proposes `niwa_send_message(reply_to=<msg_id>)` as the
  coordinator's answer path; Lead 4 establishes `niwa_finish_task` as correct. These are different:
  `niwa_send_message` delivers a message to the inbox but does NOT fire the asker's `awaitWaiter`
  channel, so the worker remains blocked. `niwa_finish_task` triggers the task-terminal path and
  unblocks the worker. **Resolution: `niwa_finish_task` is the correct mechanism.** The `question_pending`
  return from `niwa_await_task` must include the ask task's `task_id` so the coordinator can call
  `niwa_finish_task` on it.

- **Session registration timing.** Manual registration via CLI is fragile — coordinators who skip
  the step appear dead to `handleAsk`. Auto-registration on first MCP call is simpler but may
  register sessions that aren't "coordinators" in any meaningful sense. The design needs to specify
  when registration happens and what "coordinator" means for routing.

### Gaps

- **Session liveness file was not saved** by the research agent; only the summary is available.
  The exact `sessions.json` schema and `IsPIDAlive` implementation details weren't fully captured.
  Not blocking — the summary confirms what's needed: PID-alive check exists, auto-registration
  does not.

- **Exact `question_pending` response shape is not finalized.** Candidate fields are known but
  the authoritative JSON schema needs to be decided in the design doc.

### Decisions (auto mode)

- Crystallize as design doc: requirements are clear (AC in issue #92); multiple technical
  decisions exist (session registration mechanism, `niwa_await_task` early-return shape, routing
  logic placement, skill update approach). Medium complexity, right fit for a design doc.

- `niwa_finish_task` is the coordinator response mechanism, not `niwa_send_message`. The worker
  blocks on task completion via `awaitWaiters`; only `niwa_finish_task` triggers that path.

- No second discovery round needed. All structural questions are answered. Gaps are design
  decisions, not unknown territory.

## Accumulated Understanding

The fix has three independent components:

**1. Session routing in `handleAsk`** — before creating a task and spawning an ephemeral worker,
`handleAsk` should check `.niwa/sessions/sessions.json` for a live coordinator registration
(PID-alive check). If found, skip spawn and allow the question to queue naturally as a task in
the coordinator's inbox. If not found, fall back to existing spawn behavior unchanged.

**2. `niwa_await_task` early return for questions** — when a coordinator is blocking on
`niwa_await_task` and a new ask task arrives in its inbox (detectable via the watcher's
`notifyNewFile`), the daemon should deliver a `question_pending` event via the existing
`awaitWaiters` mechanism. The return payload includes the ask task ID and question body so the
coordinator can call `niwa_finish_task` to answer. The coordinator re-calls `niwa_await_task`
with the original task ID to resume waiting. This breaks the deadlock.

**3. Skill and docs update** — `buildSkillContent()` in `channels.go` needs to describe the
coordinator's question-handling loop pattern. `docs/guides/cross-session-communication.md`
needs worked examples. Both must reference `niwa_finish_task` as the response mechanism.

The infrastructure for all three components largely exists: the sessions registry, the
`awaitWaiters` channel pattern, the task-completion unblock path. The changes are mostly
wiring: teach `handleAsk` to check the registry, teach the watcher to notify on new ask
tasks (not just terminal events), add a formatter for the new response shape, update skill content.

## Decision: Crystallize
