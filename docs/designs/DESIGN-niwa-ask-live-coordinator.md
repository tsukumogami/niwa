---
status: Planned
problem: |
  When a worker calls niwa_ask(to='coordinator'), the daemon always spawns an ephemeral
  claude -p process to fabricate an answer, even when the coordinator is actively running
  and polling niwa_check_messages or blocking on niwa_await_task. The routing path for
  live coordinator sessions was intentionally removed from handleAsk and never replaced.
  In practice this silently breaks approval gates: a worker that asks the coordinator for
  sign-off before proceeding gets an auto-generated "approval" from a ghost process, while
  the coordinator's progress log shows the worker as approved — having never seen the question.
  A secondary problem: if a coordinator is blocking on niwa_await_task waiting for a worker
  to finish, and that worker calls niwa_ask(to='coordinator') mid-task, both parties deadlock
  until the 600-second timeout fires.
decision: |
  Route worker questions to live coordinator sessions via two coordinated changes: handleAsk
  reads sessions.json, runs a PID/start-time liveness check, and writes a task.ask notification
  to the coordinator's inbox when a live session is found; niwa_await_task registers a separate
  questionWaiters[role] channel alongside the existing awaitWaiters[taskID] channel and uses a
  three-way select to return early with status: "question_pending" when a question arrives.
  Coordinators register lazily as a transparent side effect of their first niwa_await_task or
  niwa_check_messages call. No new MCP tools are added.
rationale: |
  The separate questionWaiters channel keeps terminal and question events on physically distinct
  channels, so a bug in question handling cannot affect existing coordinator code that reads
  terminal events. Lazy registration ties coordinator visibility to intentional mesh participation
  rather than protocol negotiation or hook execution, making it immune to hook failures and
  capability probes. No new MCP tools are required because niwa_finish_task already drives the
  awaitWaiters path that unblocks the asking worker — reusing it avoids a parallel mechanism
  with identical semantics.
---

# DESIGN: Route niwa_ask to live coordinator session

## Status

Planned

## Context and Problem Statement

`niwa_ask(to='coordinator')` is designed to let workers escalate questions to their coordinator.
In practice the inbound routing path doesn't exist: `handleAsk` (server.go) unconditionally
creates a task and spawns an ephemeral `claude -p` to answer it. A coordinator actively polling
`niwa_check_messages` never sees the question. Any approval gate a coordinator tries to enforce
is bypassed silently.

A sessions registry at `.niwa/sessions/sessions.json` tracks coordinator sessions with PID and
start-time metadata and exposes a `IsPIDAlive()` check, but `handleAsk` never reads it.

The outbound direction already works: coordinators delegate tasks to workers, workers launch as
needed. Only the inbound direction — worker → coordinator — is broken.

A secondary structural problem emerged during exploration: if a coordinator is blocking in
`niwa_await_task` waiting for a worker's task to complete, and that worker calls
`niwa_ask(to='coordinator')` before finishing, the two processes deadlock. The coordinator waits
for task completion; the worker waits for the coordinator to answer. `niwa_await_task` has no
mechanism to return early when a question arrives, so neither unblocks until timeout.

The fix requires three coordinated changes:
1. Teach `handleAsk` to check for a live coordinator session and queue the question there instead of spawning.
2. Teach `niwa_await_task` to return early with a `question_pending` outcome when a question arrives in the coordinator's inbox, and define the re-wait loop pattern.
3. Update the generated skill content in `buildSkillContent()` (channels.go) and the cross-session guide so coordinators understand the new polling loop.

## Decision Drivers

- **No new MCP tools if avoidable.** The existing `niwa_finish_task(outcome=completed, result=answer)` path already unblocks the asker via `awaitWaiters` — the response mechanism should reuse it. Adding a separate `niwa_respond` tool creates a parallel path with identical semantics.
- **Backward compatibility for coordinator loops.** Existing coordinator code that calls `niwa_await_task` and interprets the result as always meaning "done" will encounter `question_pending` results after this change. The design must minimize breakage and provide clear migration guidance in skill content.
- **Session auto-registration.** The current registration path is a manual CLI step (`niwa session register`). If the coordinator doesn't run this, it's invisible to `handleAsk`. Auto-registration on first MCP call would make the feature reliable, but raises questions about which sessions qualify as "coordinator" and how the daemon maps roles to sessions.
- **Liveness detection precision.** A registered session may have exited without de-registering. PID-alive checks are cheap and available; the design should specify when they run and what happens on stale entries.
- **Minimal watcher changes.** The `notifyNewFile` watcher already drives both `niwa_check_messages` delivery and `awaitWaiters` dispatch. Adding question delivery to `niwa_await_task` should be a wiring change, not a structural change.
- **Skill content is generated Go code.** `buildSkillContent()` in `internal/workspace/channels.go` produces the SKILL.md installed into every session. Updating the coordinator's loop documentation requires editing Go source, not markdown files.
- **Channel notifications do not resolve the deadlock.** The MCP server already sends `notifications/claude/channel` pushes when inbox files arrive. This was considered as an alternative delivery mechanism for questions. It doesn't work: MCP tool calls are synchronous — Claude Code cannot act on a channel notification while blocked waiting for `niwa_await_task` to return. The interrupt must come from inside the tool call itself, via the `questionWaiters` channel.

## Decisions Already Made

The following decisions were made during the exploration phase and should be treated as constraints by this design:

- **Response mechanism is `niwa_finish_task`, not `niwa_send_message`.** The worker's `awaitWaiter` channel fires only when the ask task reaches terminal state. `niwa_send_message` alone does not trigger this path and would leave the worker permanently blocked. The coordinator must call `niwa_finish_task(task_id=<ask_task_id>, outcome=completed, result=<answer>)` to unblock the asker.
- **No timeout/fallback-to-spawn.** Questions queue until the coordinator next contacts the daemon via `niwa_check_messages` or `niwa_await_task`. If the coordinator never resumes, the question waits indefinitely. This is the accepted behavior.
- **Both `niwa_check_messages` and `niwa_await_task` are delivery points.** The coordinator picks up questions at whichever of these calls comes next. This prevents the deadlock case where the coordinator is blocking on `niwa_await_task` while the worker is blocking on `niwa_ask`.
- **`niwa_await_task` is the correct tool name.** "niwa_wait" is informal shorthand used in the issue and exploration; the implemented tool is `niwa_await_task`.

## Considered Options

### Decision 1: Coordinator session registration

`handleAsk` needs to know whether to queue a question for a live coordinator or fall back to
spawning an ephemeral worker. That requires a reliable signal that a coordinator session is
active. Three approaches were evaluated.

Key assumptions: coordinators always call `niwa_await_task` or `niwa_check_messages` before any
worker question would need routing to them. `NIWA_SESSION_ROLE=coordinator` is set in `.mcp.json`
for the coordinator session. The existing `IsPIDAlive(pid, startTime)` check is available and cheap.

#### Chosen: Lazy registration on first niwa_await_task or niwa_check_messages call

When the MCP server processes a `niwa_await_task` or `niwa_check_messages` call and
`s.role == "coordinator"`, it checks whether a live session is already registered for the
coordinator role. If not, it writes a `SessionEntry` to `sessions.json` using the current PID
and start time. This registration is a transparent side effect of the tool call.

`handleAsk` then reads `sessions.json` and calls `IsPIDAlive` before deciding whether to queue
the question into the coordinator's inbox (live session found) or create a spawn task (no live
session). Stale entries are pruned by the `IsPIDAlive` check.

The implementation touches `handleAwaitTask` and `handleCheckMessages` in `server.go` (or a
shared `maybeRegisterCoordinator` helper) and `handleAsk` which gains the registry lookup.

**Why this works:** `niwa_await_task` and `niwa_check_messages` are the only two ways a coordinator
participates in the mesh. A coordinator that never calls either has no mechanism to receive
questions — routing to spawn in that case is the correct fallback. Lazy registration ties
registration to an intentional coordinator action rather than to protocol negotiation, and it's
immune to hook failures because it runs inside the MCP server using the already-correct `s.role`.

#### Alternatives Considered

**Auto-register on first MCP call**: Register when any MCP method is received (`initialize`,
`tools/list`, `tools/call`). Rejected because these fire for any MCP client connecting to the
niwa server, including capability probes that don't indicate an active coordinator session.
Registration would be coupled to protocol negotiation rather than coordinator behavior.

**Require explicit pre-registration via CLI**: Keep `niwa session register` as the sole
registration path, relying on the installed hooks. Rejected because hooks can fail, be missing
in older workspaces, or misbehave when the coordinator launches from an unexpected directory —
making routing reliability dependent on hook correctness violates the stated constraint.

---

### Decision 2: niwa_await_task question interrupt mechanism

`niwa_await_task` registers a buffered-1 channel in `awaitWaiters[taskID]` and blocks until a
task-terminal event arrives via `notifyNewFile` in `watcher.go`. It has no mechanism to return
early when a question arrives. This creates a deadlock: coordinator waits for task completion,
worker waits for the coordinator to answer its question.

All three options require `handleAsk` to write a `task.ask` notification message to the
coordinator's inbox. This is necessary because the daemon claims `task.delegate` messages before
the coordinator's MCP watcher can read them reliably; `handleAsk` is the correct write point.

Key assumptions: single coordinator per role. The deferred move-to-read fix (move notification
to `read/` only after successful channel send) is required for both channel-based options.

#### Chosen: Separate questionWaiters channel per awaiting coordinator (Option B)

Add `questionWaiters map[string]chan taskEvent` to `Server`, keyed by coordinator role.
`handleAwaitTask` registers both `awaitWaiters[delegatedTaskID]` (terminal events, unchanged)
and `questionWaiters[s.role]` (question interrupts, new). The select covers three channels:

```go
select {
case evt := <-terminalCh:     // task done
    return formatTerminalResult(st)
case qEvt := <-questionCh:    // question arrived — coordinator must answer and re-wait
    return formatQuestionResult(qEvt, taskDir)
case <-time.After(timeout):
    return timeoutResult(...)
}
```

`handleAsk` writes a `task.ask` notification to the coordinator's inbox before the spawn path.
`notifyNewFile` detects `task.ask` type and dispatches to `questionWaiters[to.role]`. The
terminal dispatch path (`awaitWaiters`) is untouched.

A catch-up scan runs at the top of `handleAwaitTask` (after registering both channels, before
blocking) to surface any `task.ask` notifications that arrived while no channel was registered.

The `question_pending` return payload carries `ask_task_id`, `from_role`, and the question body.
The coordinator calls `niwa_finish_task(task_id=<ask_task_id>, outcome=completed, result=<answer>)`
to unblock the worker, then re-calls `niwa_await_task` with the original task ID.

**Why Option B over Option A (extend awaitWaiters with EvtQuestion):** Hard separation between
terminal and question channels means a bug in question handling cannot reach the existing terminal
path. Option A placed both event types on the same channel, creating a shared failure domain.
The role-keyed routing is also simpler than Option A's task-id routing — no delegated task ID
needs to be embedded in the notification, and the coordinator can receive questions regardless
of which task it is awaiting.

#### Alternatives Considered

**Option A — Extend awaitWaiters with EvtQuestion**: Add `EvtQuestion` to the `taskEvent.Kind`
enum and dispatch to the existing channel. Smaller diff (~90-120 lines vs. ~130-150). Rejected
because mixing question events into the terminal-event channel creates a shared failure domain
where a bug in question dispatch could affect the existing terminal path. Requires embedding the
delegated task ID in `task.ask` notifications for routing, adding complexity vs. role-keyed routing.

**Option C — Convert niwa_await_task to hybrid polling**: Add a 500ms ticker alongside the
existing channel. Rejected because 500ms question detection latency is unacceptable for AI Q&A
workflows where workers are already blocked. The deferred move-to-read fix closes the multi-question
delivery advantage polling held over channel-based options; polling's only remaining characteristic
is its latency penalty.

## Decision Outcome

**Chosen: D1 (lazy registration) + D2 (separate questionWaiters channel)**

### Summary

When a coordinator's Claude Code session starts, it doesn't register with the daemon immediately.
Registration happens transparently on the first `niwa_await_task` or `niwa_check_messages` call:
the MCP server writes a `SessionEntry` to `sessions.json` containing the coordinator's PID and
start time. This makes the coordinator discoverable to `handleAsk` without requiring any manual
CLI step or hook.

When a worker calls `niwa_ask(to='coordinator')`, `handleAsk` reads `sessions.json`, runs an
`IsPIDAlive` check, and branches: if a live coordinator is registered, it writes a `task.ask`
notification to the coordinator's inbox and continues with normal ask-task creation (the ask task
is what the worker blocks on). If no coordinator is registered or the PID is dead, it falls back
to the existing ephemeral spawn path unchanged.

On the coordinator side, questions surface through two paths. If the coordinator is actively
polling (`niwa_check_messages`), the `task.ask` notification appears in the inbox like any other
message — the coordinator reads its `type` field, sees it's a question, and calls
`niwa_finish_task(task_id=<ask_task_id>, outcome=completed, result=<answer>)` to unblock the
worker. If the coordinator is blocking on `niwa_await_task`, the daemon delivers the question via
the `questionWaiters[role]` channel — a separate channel from `awaitWaiters[taskID]` that carries
only question events. `niwa_await_task` returns early with `status: "question_pending"` and the
coordinator follows the same answer path. It then re-calls `niwa_await_task` with the same task
ID to resume waiting; the daemon's catch-up scan ensures any questions that arrived during
re-registration are surfaced.

No new MCP tools are added. The coordinator's response mechanism is `niwa_finish_task`, reusing
the existing task-completion path that already unblocks the asking worker. Files changed:
`internal/mcp/server.go`, `internal/mcp/handlers_task.go`, `internal/mcp/watcher.go`,
`internal/mcp/types.go`, and `internal/workspace/channels.go` (skill content update).
The daemon (`internal/cli/mesh_watch.go`) is unchanged.

### Rationale

Lazy registration (D1) and separate question channels (D2) reinforce each other. Both use the
coordinator's existing activity as the signal: D1 registers when the coordinator first touches
the mesh; D2 delivers questions only when the coordinator is actively awaiting a task. A
coordinator that never participates in the mesh is invisible to both mechanisms and correctly
falls back to the spawn path.

The separation between `awaitWaiters` (terminal events) and `questionWaiters` (question events)
is worth the ~40 additional lines because it keeps the behavioral contract of existing
`niwa_await_task` callers intact with a physical boundary, not just a kind-check convention.
The `notifyNewFile` function gains one new detection branch but its overall structure is
unchanged. The daemon event loop is untouched entirely.

## Solution Architecture

### Overview

The fix wires three existing mechanisms together without changing the daemon's event loop or
adding new MCP tools. `handleAsk` gains a liveness check before the spawn path. `niwa_await_task`
gains a second channel for question events. The MCP server lazily registers coordinator sessions
on their first meaningful mesh call.

### Components

**`internal/mcp/server.go`**
- `maybeRegisterCoordinator(ctx)`: called at the top of `handleAwaitTask` and `handleCheckMessages`
  when `s.role == "coordinator"`. Reads `sessions.json`, calls `IsPIDAlive`, writes a `SessionEntry`
  if none exists. No-op if already registered and alive.
- `handleAsk`: gains a registry check before the existing task-creation path. If a live coordinator
  session is found, writes a `task.ask` notification to the coordinator's inbox, then continues
  with normal ask-task creation (the ask task is still the mechanism the worker blocks on).
- `questionWaiters map[string]chan taskEvent`: new field, keyed by coordinator role. Initialized
  in `NewServer`. Protected by `waitersMu` alongside `awaitWaiters`.

**`internal/mcp/handlers_task.go`**
- `handleAwaitTask`: after calling `maybeRegisterCoordinator`, registers both
  `awaitWaiters[delegatedTaskID]` and `questionWaiters[s.role]`, then runs a catch-up scan
  before blocking. The scan lists `roleInboxDir` for `.json` files, reads the `type` field of
  each, and tries a non-blocking send to `questionWaiters[s.role]` for any `task.ask` files found.
  It does not filter by `seenFiles` — a question file may be in `seenFiles` (from a prior channel
  notification that failed delivery) and still be present in `inbox/`. Selects on three channels:
  terminal event, question event, timeout. Deregisters both channels on return.
- `formatQuestionResult(qEvt questionEvent) string`: serializes `{status: "question_pending",
  ask_task_id, from_role, body}` as the tool response text. The `body` field already carries the
  `_niwa_note` wrapper written by `handleAsk` at notification-write time, so both the
  `niwa_check_messages` path and the `question_pending` path deliver wrapped question content.

**`internal/mcp/watcher.go`**
- `notifyNewFile`: gains a detection branch for `type == "task.ask"`. Dispatches to
  `questionWaiters[to.role]` using the same non-blocking send pattern as terminal dispatch.
  Applies the deferred move-to-read fix: notification files move to `inbox/read/` only after
  a successful channel send. If the channel is full (no waiter or already has an event), the
  file stays in inbox for the catch-up scan to find on re-registration. This fix is a
  correctness requirement, not an optimization — the current code moves terminal event files to
  `inbox/read/` before the channel send. Copying that pattern for `task.ask` would permanently
  lose any question whose send is dropped (channel full), defeating the catch-up scan.

**`internal/mcp/types.go`**
- Adds a `questionEvent` struct separate from `taskEvent`: `{ AskTaskID, FromRole string; Body json.RawMessage }`.
  `questionWaiters` is typed `map[string]chan questionEvent`. No `EvtQuestion` is added to `TaskEventKind`
  — questions are not task-state transitions and must not appear in `transitions.log`.

**`internal/cli/session_register.go`**
- `writeSessionEntry` is moved to `internal/mcp` (both `SessionEntry` and `SessionRegistry` already live
  in `mcp/types.go`). `maybeRegisterCoordinator` calls it directly. The CLI's `session_register.go` is
  updated to call the relocated function. This avoids a dependency inversion — `internal/cli` already
  imports `internal/mcp`; reversing that direction is not allowed.

**`internal/workspace/channels.go`**
- `buildSkillContent()`: updates the "Peer Interaction" section to explain questions arrive via
  `niwa_check_messages` and `niwa_await_task`; adds the coordinator re-wait loop pattern to
  "Common Patterns"; documents `niwa_finish_task` as the response mechanism.

### Key Interfaces

**`task.ask` notification** (written to coordinator inbox by `handleAsk`):
```json
{
  "type": "task.ask",
  "from": "<worker_role>",
  "to": "coordinator",
  "task_id": "<ask_task_id>",
  "body": {
    "ask_task_id": "<ask_task_id>",
    "from_role": "<worker_role>",
    "question": <original question body>
  }
}
```
This is a `Message` in the coordinator's inbox at `.niwa/roles/coordinator/inbox/<id>.json`.

**`niwa_await_task` question_pending response**:
```json
{
  "status": "question_pending",
  "ask_task_id": "<ask_task_id>",
  "from_role": "<worker_role>",
  "body": <question body>
}
```
The coordinator calls `niwa_finish_task(task_id=<ask_task_id>, outcome="completed", result=<answer>)`
and then re-calls `niwa_await_task(task_id=<original_task_id>)` to resume.

**Coordinator re-wait loop** (pseudocode for skill documentation):
```
result = niwa_await_task(task_id)
while result.status == "question_pending":
    answer = <formulate answer to result.body>
    niwa_finish_task(task_id=result.ask_task_id, outcome=completed, result=answer)
    result = niwa_await_task(task_id)
// proceed: result is completed/abandoned/cancelled/timeout
```

**`niwa_check_messages` path** (no code changes needed):
The `task.ask` notification is a standard inbox message. `handleCheckMessages` returns it
alongside other messages. The coordinator identifies it by `type == "task.ask"`, then calls
`niwa_finish_task` to answer. The notification moves to `inbox/read/` via the existing
atomic rename, preventing duplicate delivery.

### Data Flow

```
Worker                    Daemon (MCP server)           Coordinator
------                    ------------------           -----------
niwa_ask(to='coord')
  |
  +--> handleAsk
         |
         +--> read sessions.json
         |    IsPIDAlive check
         |
         +--> [live]  write task.ask to coord inbox
         |            create ask task in coord inbox
         |            register awaitWaiters[askTaskID]
         |            return (worker blocks)
         |
         +--> [dead]  create ask task (spawn path, unchanged)
                      register awaitWaiters[askTaskID]
                      return (worker blocks)

                                             niwa_await_task(workerTaskID)
                                               |
                                               +--> maybeRegisterCoordinator
                                               |    catch-up scan (task.ask in inbox?)
                                               |    register awaitWaiters[workerTaskID]
                                               |    register questionWaiters["coordinator"]
                                               |    select {
                                               |      case terminalEvt: ...
                                               |      case questionEvt: ...  <-- fires
                                               |      case timeout: ...
                                               |    }
                                               |
                                               +--> return {status: "question_pending", ask_task_id, ...}
                                               |
                                             coordinator answers:
                                             niwa_finish_task(ask_task_id, completed, answer)
                                               |
                                               +--> write task.completed to worker inbox
                                               |
Worker unblocks:                             coordinator re-waits:
niwa_ask returns answer                      niwa_await_task(workerTaskID)
                                               +--> (same select, waits for task completion)
```

## Implementation Approach

### Phase 1: Session registration and routing

Add lazy coordinator registration and the routing branch to `handleAsk`. This is the core
correctness fix — questions stop going to spawn and start queuing in the coordinator's inbox.
No `niwa_await_task` changes yet; questions delivered via inbox only (visible on next
`niwa_check_messages` call).

Deliverables:
- `maybeRegisterCoordinator` helper in `server.go`
- Registry lookup in `handleAsk` with `task.ask` notification write
- Unit tests: `handleAsk` routes to inbox when coordinator alive, falls back to spawn when dead

### Phase 2: niwa_await_task question interrupt

Add `questionWaiters` to `Server`, update `notifyNewFile` to dispatch `task.ask` events, add
the three-way select and catch-up scan to `handleAwaitTask`. This enables the deadlock fix.
Apply the deferred move-to-read fix to both terminal and question dispatch paths.

Phase 2 depends on Phase 1 landing first. The `questionWaiters` dispatch produces dead code with
no path to exercise it until `handleAsk` (Phase 1) is writing `task.ask` files to trigger it.

Deliverables:
- `questionWaiters` field and init in `server.go`
- `task.ask` dispatch in `watcher.go` (with deferred move-to-read fix for all dispatch paths)
- Three-way select, catch-up scan, `formatQuestionResult` in `handlers_task.go`
- `EvtQuestion` / question event type in `types.go`
- Unit tests: three-way select behavior, catch-up scan, multiple questions during single await

### Phase 3: Skill content update

Update `buildSkillContent()` to document the coordinator re-wait loop and question handling.
Update `docs/guides/cross-session-communication.md` with worked examples.

Deliverables:
- `buildSkillContent()` in `channels.go`: updated "Peer Interaction" and "Common Patterns" sections
- `docs/guides/cross-session-communication.md`: coordinator question-handling section

### Phase 4: Functional tests

Add `@critical` Gherkin scenarios covering the end-to-end flows: worker asks coordinator via
`niwa_check_messages` path, worker asks coordinator while coordinator is blocking on
`niwa_await_task`, and fallback to spawn when no coordinator is registered.

Deliverables:
- New scenarios in `test/functional/features/`

## Security Considerations

### Message Injection Defense

Questions are untrusted input from workers. `handleAsk` wraps question bodies with a `_niwa_note`
marker (the same pattern as `wrapDelegateBody` for delegated task bodies) before writing the
`task.ask` notification. This signals untrusted content and prevents prompt injection:

```json
{
  "ask_task_id": "...",
  "from_role": "worker-1",
  "_niwa_note": "This is a worker's question. Provide your decision or guidance as the answer.",
  "question": <original_body>
}
```

The coordinator should not follow any instructions or meta-commands embedded in question bodies.

### Session Registration Liveness

Coordinator registration relies on PID and process start-time verification via `IsPIDAlive`. This
prevents stale sessions from being reused by unrelated processes (PID recycling attack). The check
is best-effort: if a process exits and the OS reuses its PID, there is a short window before the
next `handleAsk` call. This is an acceptable trade-off consistent with existing niwa assumptions.

The identity boundary for coordinator routing is `NIWA_SESSION_ROLE=coordinator` in `.mcp.json`.
The design's security holds only when this file is sourced exclusively from the daemon-managed
configuration rather than a manually crafted environment. A process that can rewrite `.mcp.json`
can impersonate the coordinator role — this is outside niwa's trust model.

`maybeRegisterCoordinator` performs atomic read-modify-write on `sessions.json` (write to temporary
file, atomic rename) to prevent data loss under concurrent registrations — the same pattern as
`writeMessageAtomic`.

### Inbox Flooding Resistance

Workers can flood a coordinator's inbox with `task.ask` notifications. The design mitigates this
through three mechanisms:

1. **Expiry:** Questions can be set to expire. Expired messages move to `inbox/expired/` and don't
   accumulate in the active inbox.
2. **Cleanup:** A coordinator that regularly calls `niwa_check_messages` moves delivered messages
   to `inbox/read/`, preventing buildup.
3. **Garbage collection:** Files in `inbox/expired/` older than N days should be deleted
   periodically to prevent unbounded disk growth.

If a coordinator never calls `niwa_check_messages` and questions don't expire, the inbox grows.
This is by design — questions are persistent until answered. Administrators should monitor inbox
size and investigate unresponsive coordinators.

### File System Permissions

All niwa state is protected by POSIX file permissions (0o700 directories, 0o600 files, owned by
the user running Claude Code). The design adds no new permission escalation risks. Questions and
answers are as private as existing messages and tasks.

## Consequences

### Positive

- Worker → coordinator questions are delivered to the live session. Approval gates work as intended.
- The coordinator deadlock (both parties blocking until timeout) is eliminated.
- No new MCP tools. The `niwa_finish_task` response path is reused — no parallel mechanism to maintain.
- Lazy registration is self-contained in the MCP server. No hook failures can break routing.
- Questions that arrive before `niwa_await_task` is called queue in the inbox and are picked up
  by either the next `niwa_check_messages` call or the catch-up scan on next `niwa_await_task`.

### Negative

- Existing coordinator code that doesn't handle `status: "question_pending"` from `niwa_await_task`
  will silently drop questions. The worker remains blocked until the coordinator's await times out
  (600 seconds by default).
- The coordinator's re-wait loop adds code to coordinator implementations. The "wait until done"
  pattern becomes "loop until done, answering questions along the way."
- `notifyNewFile` grows a second dispatch branch. Readers must understand both the terminal
  dispatch path (`awaitWaiters`) and the question dispatch path (`questionWaiters`).
- The `task.ask` notification is written to the coordinator's inbox before it's known whether the
  coordinator will ever call `niwa_await_task` again. If the coordinator exits after registration
  but before answering, the notification sits in the inbox indefinitely.

### Mitigations

- Skill content update explicitly describes the re-wait loop pattern with pseudocode and worked
  examples. Coordinators that follow the skill will handle `question_pending` correctly.
- `status: "question_pending"` is distinct from all existing `niwa_await_task` return statuses
  (`completed`, `abandoned`, `cancelled`, `timeout`). Detecting it requires only a string comparison.
- The `waitersMu` mutex already serializes access to `awaitWaiters`; `questionWaiters` is added
  under the same lock, keeping the concurrency model simple.
- Stale `task.ask` notifications in the inbox are harmless — they appear on the next
  `niwa_check_messages` call and can be ignored if the coordinator sees them after the worker's
  ask has already timed out (the ask task will be in a terminal state that `niwa_finish_task`
  would reject cleanly).
