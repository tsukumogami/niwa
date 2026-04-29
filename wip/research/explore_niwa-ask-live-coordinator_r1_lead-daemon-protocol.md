# Lead: daemon protocol internals

## Findings

### How handleAsk works today

`handleAsk` (server.go:677) creates a first-class task with the ask body wrapped as `{"kind":"ask","body":<original>}`, then blocks on the task's terminal event via an `awaitWaiter` channel. The worker spawned to handle the ask must call `niwa_finish_task` with the reply; that completion message is routed through the watcher's `notifyNewFile` (watcher.go:116-147) which extracts the `result` field and delivers it to the ask caller's channel. Default timeout is 600 seconds (PRD R29).

The task is created via `createTaskEnvelope` (handlers_task.go:153) with the ask body wrapped at the niwa layer (not in the worker's view). The wrapped body structure is `{"kind":"ask","body":<question>}`, which signals to the worker bootstrap that this is a Q&A rather than work delegation. This unifies ask-and-reply with the task-lifecycle model rather than maintaining a parallel reply-waiter path.

**Current flow:**
1. Caller invokes `niwa_ask(to='coordinator', body={...})`
2. Handler creates task envelope with wrapped body, inserts into target inbox
3. Handler registers `awaitWaiter[taskID]` channel
4. Handler blocks on channel with timeout
5. When target calls `niwa_finish_task(outcome="completed", result=<answer>)`, daemon routes `task.completed` message to caller's inbox
6. Watcher's `notifyNewFile` extracts result from message and sends to channel
7. Caller unblocks with answer

**Key discovery:** The task envelope is NOT created with `parentTaskID` override in `handleAsk`; line 701 passes empty string, meaning ask tasks always use the caller's own `taskID` as parent (or empty if coordinator).

### What niwa_check_messages returns

`handleCheckMessages` (server.go:386) returns all non-expired message files from the caller's inbox (`.niwa/roles/<role>/inbox/`) formatted as markdown. Each message includes ID, sender role, type, timestamp, task ID (if present), and body. For task-delegation messages (type "task.delegate"), the body is wrapped in `wrapDelegateBody` (server.go:533) with `_niwa_task_body` and a note explaining untrusted content, providing defense against prompt injection (Decision 3).

After returning messages, files are atomic-renamed to `inbox/read/` so subsequent calls don't re-surface them. The in-progress task envelope (worker's own task, if NIWA_TASK_ID is set) is NOT moved to read/ and stays in `inbox/in-progress/` for lifetime of task (lines 435-446). This bootstrap contract is enforced: the daemon spawns workers with a fixed prompt "You are a worker for niwa task %s. Call niwa_check_messages to retrieve your task envelope." (mesh_watch.go:94), and the handler has a special case reading `inbox/in-progress/<NIWA_TASK_ID>.json` so the worker always retrieves its own task first.

**Data model returned:**
- Markdown with `## N new message(s)` header
- For each message: ID, sender, type, timestamp, task ID (if present), expiry (if set)
- Body as prettified JSON (or wrapped delegate body)
- File moved to inbox/read/ on return (except in-progress envelope)

### How niwa_wait works

**Critical finding: niwa_wait does NOT exist as an MCP tool.** The PRD R21 and the scope document mention "niwa_wait" as if it were a tool that should return early when a question arrives, but the tool is not implemented. The only relevant blocking calls are:
- `niwa_await_task(task_id, timeout_seconds?)` (handlers_task.go:258)
- `niwa_ask(to, body, timeout_seconds?)` (server.go:677)

Both use the same underlying mechanism: register an `awaitWaiter` channel keyed by task ID, re-read state.json under a race guard, then select on either the channel receiving an event OR the timeout firing. The channel is fed by `notifyNewFile` when a task-terminal message arrives in the inbox.

`niwa_await_task` unblocks on `task.completed`, `task.abandoned`, or `task.cancelled` messages (watcher.go:121). The timeout result returns `{"status":"timeout","task_id":%q,"current_state":%q}` and does NOT cancel the task.

**Key insight:** There is no mechanism for `niwa_await_task` to return early due to a pending **question** (ask to the awaiter). If a coordinator calls `niwa_await_task` on a worker task and a worker later calls `niwa_ask(to="coordinator")`, the coordinator remains blocked on await until the worker's task completes OR the await timeout fires. This is the deadlock scenario described in the scope: "coordinator waiting on task completion, worker waiting for answer."

### Daemon state model for asks, tasks, and messages

**Tasks:**
- Directory at `.niwa/tasks/<task-id>/` containing:
  - `envelope.json`: TaskEnvelope (v=1) with id, from, to, body, sent_at, parent_task_id, expires_at
  - `state.json`: TaskState (v=1) with state (queued/running/completed/abandoned/cancelled), state_transitions array, restart_count, worker metadata, delegator_role, target_role, result/reason/cancellation_reason (terminal only)
  - `transitions.log`: append-only NDJSON with TaskEventKind ("completed", "abandoned", "cancelled", "progress", etc.), timestamps, actor metadata
- Consumption barrier: daemon atomically renames from `inbox/<id>.json` to `inbox/in-progress/<id>.json` when claiming (R16, R13)
- Task states are mutated via `taskstore.UpdateState` (taskstore.go) which acquires exclusive flock on `.lock`, updates state.json, appends to transitions.log, and fsync's

**Messages:**
- Files at `.niwa/roles/<role>/inbox/<id>.json` (Message v=1 schema)
- Message structure: id, type, from, to, reply_to (optional), task_id (optional), sent_at, expires_at (optional), body
- Lifecycle: created in inbox/ → read by `niwa_check_messages` → moved to inbox/read/ → can optionally be swept to inbox/expired/ if past expires_at
- Task-terminal messages (task.completed, task.abandoned, task.cancelled) route through watcher's `awaitWaiters` BEFORE reply-waiter dispatch (watcher.go:116-147)
- `task.progress` messages land in delegator's inbox but do NOT unblock await callers

**Ask-specific model:**
- An ask is a task with body structure `{"kind":"ask","body":<question>}`
- Caller registers `awaitWaiter[taskID]` before state read (race guard)
- Worker receives ask task, calls `niwa_finish_task(outcome="completed", result=<answer>)`
- Daemon writes `task.completed` message to caller's inbox with result field
- Watcher's `notifyNewFile` extracts result and sends `taskEvent{Kind: EvtCompleted, Result: <answer>}` to awaiter's channel
- Caller unblocks with answer or timeout fires

**Coordinator registration (not yet integrated with ask routing):**
- PRD R39: Coordinator sessions register via `niwa session register` writing to `.niwa/sessions/sessions.json`
- SessionEntry captures: session UUID, role, PID, start_time, Claude session ID, inbox_path
- **However:** This registration mechanism exists but is NOT consulted by `handleAsk` when routing to "coordinator". The ask always creates a task and spawns an ephemeral worker, silently bypassing the live coordinator session (Issue #92).

## Implications

1. **niwa_wait does not exist** — the scope document assumes a blocking call that returns early on question arrival, but only `niwa_await_task` exists and it has no question-awareness mechanism. The poll-based delivery model proposed in the scope (both `niwa_check_messages` and niwa_wait become delivery points) must be implemented from scratch.

2. **Deadlock is possible today** — if a coordinator calls `niwa_await_task` on a worker task, and that worker calls `niwa_ask(to="coordinator")`, the system deadlocks: coordinator blocks on await, worker blocks on ask awaiting coordinator's niwa_check_messages poll. No detection, no timeout at the ask level (only at the tool call level, 600s default).

3. **Ask body wrapping is at the task layer** — the `{"kind":"ask","body":<question>}` wrapping happens in `handleAsk` before envelope creation, not in the worker's view. This is separate from the `task.delegate` body-wrapping defense. Workers see the kind field and must interpret asks differently from delegations.

4. **The watcher is the hub for all blocking unblocks** — both `awaitWaiters` and `waiters` (reply-waiter for messages) are filled from `notifyNewFile` in the inbox watcher. Task-terminal messages route through `awaitWaiters` first (lines 121-147), then reply-waiter dispatch (lines 151-164), preventing interference. This path is not yet extended to carry "question pending" signals.

5. **Coordinator registration exists but is unused for routing** — `.niwa/sessions/sessions.json` captures live coordinator metadata (PID, start_time, Claude session ID), but `handleAsk(to="coordinator")` does not consult it. Any ask to "coordinator" always spawns an ephemeral process. To fix this, the ask handler must check coordinator liveness before spawning.

6. **The watcher's fsnotify/polling dual path** — the inbox watcher uses fsnotify with fallback to 1-second polling (watcher.go:24-98). Both paths call `notifyNewFile` for every JSON file. This is the mechanism through which `niwa_check_messages` messages and task-terminal events are discovered. For questions to reach a polling coordinator, they must land in the inbox as first-class files (Message objects) before the coordinator's next poll.

## Surprises

1. **niwa_wait is mentioned in the scope but does not exist in the code** — this is a term used to describe a hypothetical blocking behavior, not an actual implemented tool. The scope document is proposing to add this capability, not documenting existing behavior.

2. **Ask tasks do not use `parentTaskID` override** — I expected `handleAsk` to preserve the ask as a "top-level" task by setting `parentTaskID=""`, but line 701 passes empty string which means the ask task inherits the caller's own taskID as parent. For a coordinator (taskID=""), asks will have `parent_task_id=""`. This might be intentional (asks are siblings, not children of the caller's work), but it's worth confirming.

3. **Task.progress messages do NOT unblock waiters** — the watcher only routes task-terminal messages to `awaitWaiters` (lines 121-147). Progress events go to inbox but do not trigger any early return from `niwa_await_task` or `niwa_ask`. A blocking caller sees progress only on the next `niwa_check_messages` call, not immediately.

## Open Questions

1. **What is the intended behavior when a question is pending and the coordinator is blocking on `niwa_await_task`?** The scope proposes that `niwa_wait` (or the new mechanism) returns early with "question pending" status, allowing the coordinator to call `niwa_check_messages` and answer. But what is the exact return shape? `{"status":"question","from":<role>,"question_id":<id>}`? And does answering the question require a new tool (e.g., `niwa_respond`), or does the answer route back as a regular `niwa_send_message` with `reply_to`?

2. **How does the daemon know whether to queue a question for a live coordinator or spawn an ephemeral worker?** The scope says "questions queue until the coordinator next contacts the daemon," but the implementation must detect liveness. Is it a PID-alive check on the registered coordinator session? What if the session is registered but paused/stuck? What is the fallback if the coordinator never resumes?

3. **Should asks to "coordinator" use a different code path than asks to worker roles?** Today all asks create tasks and spawn workers. For coordinator asks, should there be a "direct" path that bypasses task creation and injects the question directly into the coordinator's next poll? Or should the task model be uniform?

4. **Does the `parentTaskID` field in ask tasks need to be set differently?** Currently asks inherit the caller's taskID as parent (default behavior). Should ask tasks be explicitly top-level (`parent_task_id=""`), or is the current behavior correct?

5. **What happens to a question if the coordinator exits without answering?** Is the question persisted until the coordinator resumes (like task.progress messages)? Is there a timeout and fallback to spawn? The scope explicitly calls out "no timeout/fallback spawn," but how long can a question wait?

## Summary

`handleAsk` creates a task with wrapped body `{"kind":"ask"}` and blocks on the task's terminal event; `niwa_check_messages` returns all inbox messages formatted as markdown, moving them to `read/` except the worker's own in-progress envelope; and `niwa_wait` does not exist — only `niwa_await_task` blocks on task completion, with no awareness of pending questions. The daemon's state model unifies asks as tasks with explicit lifecycle (queued → running → completed via `niwa_finish_task`), stores them in `.niwa/tasks/<id>/` with envelope.json, state.json, and transitions.log, and delivers completion via `task.completed` messages that route through the watcher's `awaitWaiters` channel — but today asks to "coordinator" always spawn ephemeral workers because the ask handler does not consult the live coordinator registration in `.niwa/sessions/sessions.json`. The core challenge is that no mechanism currently exists to 1) detect coordinator liveness and queue questions for it, 2) signal pending questions through `niwa_await_task`'s blocking point, or 3) allow the coordinator to answer a question without spinning a full task-completion cycle.
