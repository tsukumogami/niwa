# Lead: niwa_wait semantics under question delivery

## Findings

### 1. How `niwa_wait` works today

**Current implementation:** `niwa_wait` is an alias for `niwa_await_task` (files `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-6/public/niwa/internal/mcp/handlers_task.go:258-314`).

**Current behavior:**
- Accepts `task_id` and optional `timeout_seconds` (default 600)
- Registers a buffered-1 channel waiter on the task ID before issuing a race-guard state re-read
- Blocks on `select` waiting for either a task-terminal event or timeout
- Returns one of three outcomes:
  1. **Task completed/abandoned/cancelled:** Returns `{status: <state>, task_id, restart_count, result|reason, last_progress, max_restarts}`
  2. **Timeout:** Returns `{status: "timeout", task_id, timeout_seconds, current_state, last_progress}`
  3. **Already terminal on entry:** Race-guard catches it and returns immediate terminal result

**Event delivery path:** Task-terminal messages (`task.completed`, `task.abandoned`, `task.cancelled`) route through `notifyNewFile()` in watcher.go, which dispatches to `awaitWaiters[task_id]` *before* checking reply waiters (line 121-146). This ensures sync callers wake immediately on terminal messages.

### 2. Current Message Routing

**Message types:** Default vocabulary includes `task.delegate`, `task.progress`, `task.completed`, `task.abandoned`, `task.cancelled`, `question.ask`, `question.answer`, `status.update` (DESIGN line 472).

**Current `niwa_ask` flow (lines 677-724 of handlers_task.go):**
- Wraps the body as `{"kind":"ask","body":<original>}`
- Creates a task envelope via `createTaskEnvelope()` targeting the `to` role
- Registers an awaitWaiter and blocks on task completion
- Returns the worker's `niwa_finish_task(outcome="completed", result=<answer>)` as the reply

**Current limitation:** There is no special routing for `to='coordinator'`. The task is queued to the coordinator's inbox like any other. If no coordinator session is running, the daemon would spawn an ephemeral `claude -p`, which is the current broken behavior. The handler does not distinguish "live session" from "no session."

### 3. Proposed Semantic: "done or question, whichever comes first"

**The design challenge:** When a worker calls `niwa_ask(to='coordinator', ...)` during execution, it creates a task that queues in the coordinator's inbox. If the coordinator is currently blocked in `niwa_wait(task_id)` waiting for that worker's task to finish, we have a deadlock:
- Worker: waiting for coordinator to answer the question
- Coordinator: waiting for worker's task to finish
- Daemon: waiting for either to make progress

**Proposed solution:** `niwa_wait` must return early when *any* question arrives in the waiting role's inbox, not just when the awaited task terminates.

**Response payload to distinguish outcomes:**
The caller must be able to distinguish "task done" from "question arrived." Proposed shapes:

```json
{
  "status": "completed|abandoned|cancelled|timeout",
  "task_id": "<original task id>",
  "restart_count": 0,
  ...terminal fields (result|reason)...
}
```

vs. a new shape for question arrival:

```json
{
  "status": "question_pending",
  "question_task_id": "<ask task id>",
  "from_role": "<asking role>",
  "body": <question body>,
  "message_id": "<message id for reply>"
}
```

**Key insight:** The response must carry enough context for the coordinator to call `niwa_send_message(to='<asking_role>', type='question.answer', reply_to='<message_id>', body=<answer>)` without additional state lookups.

### 4. State Preservation for Re-wait

**Current problem:** Once `niwa_wait` returns, the caller must be able to re-call it with the same `task_id` to resume waiting without losing progress or causing the daemon to re-deliver the question.

**Required daemon state:**
1. The original awaited `task_id` must remain tracked even though the wait returned
2. Any questions that arrived while waiting must be *removed from the inbox* (moved to `inbox/read/` or equivalent) so they are not re-delivered on the next `niwa_check_messages`
3. The task state file and transitions log must preserve all terminal information so a re-wait arriving after the task already finished still returns the correct outcome

**Current implementation:** Already has this capability via `notifyNewFile()` moving files to `inbox/read/` (line 159 of watcher.go). When a message is delivered to an awaitWaiter, it is atomically renamed out of the top-level inbox, preventing duplicate delivery.

**For questions:** The same move-to-read pattern must apply. After the awaitWaiter channel receives a question notification, the question message file must be moved from `inbox/` to `inbox/read/` so it does not appear in the next `niwa_check_messages` call.

### 5. Handling Multiple Questions During a Single Wait

**Current behavior (for reference):** awaitWaiters are buffered-1 channels. If multiple terminal events arrive, only the first is consumed; later events drop into the void (default case in the `select` on line 140 of watcher.go).

**Proposed behavior for questions:** The same pattern should apply. The first question unblocks the wait; subsequent questions queue in the inbox normally and will be picked up on the next `niwa_check_messages` or next `niwa_wait` call.

**Race handling:** `notifyNewFile()` currently runs serially in the fsnotify/polling loop, so race conditions between terminal event and question arrival are naturally serialized at the lock granularity of `waitersMu`. The first message type (task-terminal or question) to call `markSeen()` wins; the caller receives exactly one outcome per wait invocation.

### 6. Coordinator's Re-wait Loop Pattern

**Pseudo-code:**

```go
loop {
  result := niwa_wait(task_id=<worker_task_id>, timeout_seconds=600)
  
  if result.status == "completed" || result.status == "abandoned" || result.status == "cancelled" {
    // Task is done. Process result.
    return result
  }
  
  if result.status == "question_pending" {
    // Worker is asking a question mid-task.
    answer := ask_user_or_llm(result.body)  // Coordinator's own logic
    niwa_send_message(
      to=result.from_role,
      type="question.answer",
      reply_to=result.message_id,
      body={"answer": answer}
    )
    // Loop back: continue waiting for the original task to finish
    continue
  }
  
  if result.status == "timeout" {
    // No progress; decide whether to retry or abandon
    return result
  }
}
```

**Key properties:**
1. No new tools required on the coordinator side — uses existing `niwa_send_message` to reply
2. The loop is transparent to the worker — from the worker's perspective, `niwa_ask` still blocks until it gets an answer
3. State is preserved across loop iterations: the daemon keeps the awaited task tracked and does not re-deliver questions
4. Multiple questions during a single wait are handled naturally: first question unblocks, others queue in inbox for next loop iteration

---

## Implications

1. **Backward compatibility:** Existing code that calls `niwa_wait(task_id)` and assumes the result always means "done" will break. Must check `result.status` and loop on `question_pending`.

2. **Daemon statefulness:** The daemon must track which questions have been delivered to awaiting coordinators (via the move-to-read mechanism) to avoid duplicate delivery. No new in-memory state needed; the filesystem already provides this via the atomic rename pattern.

3. **Question envelope design:** The response payload must include `message_id` so the coordinator can reply with the correct `reply_to`. This is different from the current task-terminal result shape and requires a new struct or union type.

4. **Coordinator registration dependency:** The daemon must know which role is "coordinator" and which session is active/live to make the "queue question" vs "spawn ephemeral" routing decision. This is a separate piece of liveness tracking, not addressed by this lead.

5. **niwa-mesh skill changes:** The skill's coordinator loop documentation must explain the new `question_pending` case and show the re-wait pattern. The skill is the user-facing behavior contract; niwa owns only the tool API and task state machine.

---

## Surprises

1. **No new daemon infrastructure required:** The existing `notifyNewFile()` and `markSeen()` pattern fully supports delivering questions to awaiting coordinators. The move-to-read behavior already prevents duplicate delivery across tool calls.

2. **Task terminal events take precedence:** `notifyNewFile()` checks task-terminal message types *before* reply-waiter types (line 121 vs 151 of watcher.go). This means if a task finishes while a question is pending, the terminal event will reach the awaitWaiter first, unblocking the coordinator with `status=completed`. The question remains in the inbox for the next `niwa_check_messages` — a reasonable fallback.

3. **awaitWaiters buffers questions directly:** The buffered-1 channel pattern used for task terminals can be reused for questions without modification. A question is just another event type dispatched to the same channel, with a different `Kind` value.

4. **No serialization of `question_pending` outcome today:** The current `formatEventResult()` and `formatTerminalResult()` functions only handle task-terminal events. A new response-format helper is needed to serialize the question case.

---

## Open Questions

1. **What is the exact response JSON shape for `question_pending`?** Candidate fields:
   - `status: "question_pending"` (vs `task_pending`?)
   - `question_task_id: "<ask task id>"` (or `task_id`? for consistency)
   - `from_role: "<asking role>"`
   - `message_id: "<msg id>"` (required for reply routing)
   - `body: <question body>` (full body or summary?)
   - `expires_at: "<RFC3339>"` (carry through from message?)

2. **Should `niwa_wait` return on ANY message type or only specific types?** Current proposal implies "any question" but the API signature says `types: []string`. Should `niwa_wait` be redefined to accept question message types in its filter?

3. **Does the coordinator need a new tool to answer questions, or is `niwa_send_message` sufficient?** Currently, answering is done via `niwa_send_message(type='question.answer', reply_to=<msg_id>, body=...)`. This works but is not symmetrical to `niwa_ask`.

4. **How does session liveness tracking work?** The design mentions the daemon must distinguish "queue question for live coordinator" from "spawn ephemeral worker." What mechanism registers the coordinator as live, and how does stale detection work?

5. **What happens if a question arrives after the task has already finished?** Does the question remain in the inbox for `niwa_check_messages` to pick up, or is it discarded?

---

## Summary

`niwa_wait` today returns only when an awaited task reaches a terminal state (completed/abandoned/cancelled) or times out. To prevent deadlock when workers ask questions mid-execution, `niwa_wait` must return early (with status `"question_pending"`) when a question message arrives in the coordinator's inbox. The response must include enough context (`from_role`, `message_id`) for the coordinator to reply via `niwa_send_message`. The daemon's existing message move-to-read pattern handles state preservation and prevents duplicate delivery; no new infrastructure is needed. The coordinator re-waits by looping on `result.status`, answering each question and continuing until the task terminates.

