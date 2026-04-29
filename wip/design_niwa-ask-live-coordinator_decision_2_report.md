<!-- decision:start id="niwa-ask-live-coordinator-interrupt-mechanism" status="assumed" -->
### Decision: niwa_await_task Question Interrupt Mechanism

**Context**

`niwa_await_task` blocks a coordinator by registering a buffered-1 channel in `awaitWaiters[taskID]`
and selecting on it until a task-terminal event (`task.completed`, `task.abandoned`,
`task.cancelled`) arrives via `notifyNewFile` in `watcher.go`. This design works well for
task delegation but creates a deadlock when a worker calls `niwa_ask(to='coordinator')` while the
coordinator is blocked: the worker waits for the coordinator to answer its ask task via
`niwa_finish_task`; the coordinator waits for the worker to finish its delegated task. Neither
can proceed.

The fix requires `niwa_await_task` to return early when a question arrives in the coordinator's
inbox, so the coordinator can answer and then re-call `niwa_await_task` to resume waiting on the
original task. Three interrupt mechanisms were evaluated: extend `awaitWaiters` with a new
`EvtQuestion` kind (Option A), add a separate `questionWaiters` channel keyed by coordinator role
(Option B), or convert `niwa_await_task` to hybrid polling with a ticker for question checks
(Option C).

All three options require `handleAsk` (the worker's MCP call) to write a `task.ask` notification
message to the coordinator's inbox. This is necessary because the daemon (`mesh_watch.go`) claims
`task.delegate` messages before the coordinator's MCP watcher can read them — the coordinator
watcher only sees messages the daemon leaves behind. `handleAsk` knows the worker's `NIWA_TASK_ID`
(which is the delegated task T from the coordinator's view) and the coordinator role, making it
the correct write point for this notification.

**Assumptions**

- The daemon claims `task.delegate` messages before the coordinator's MCP watcher reads them in
  most cases. The coordinator's watcher (10ms sleep before reading) cannot reliably compete with
  the daemon's immediate atomic rename.
- `niwa_await_task` is called by a single coordinator goroutine per task — concurrent multi-task
  await by the same role is not a current use pattern. The single-coordinator-per-role invariant
  holds.
- 500ms–2s question detection latency would be perceptible in AI Q&A workflows. Workers blocked
  on `niwa_ask` wait the entire detection interval before the coordinator can respond.
- The deferred move-to-read/ fix (moving notification to `read/` only after a successful channel
  send) is required to close the multi-question drop window for both Options A and B.

**Chosen: Option B — Separate questionWaiters channel per awaiting coordinator**

Add a `questionWaiters map[string]chan taskEvent` field to `Server`, keyed by coordinator role.
`handleAwaitTask` registers both `awaitWaiters[delegatedTaskID]` (terminal events, unchanged) and
`questionWaiters[s.role]` (question interrupts, new). The select covers three channels:

```go
select {
case evt := <-ch1:         // terminal event — task done
    return formatTerminalResult(st)
case qEvt := <-ch2:        // question — coordinator must answer and re-wait
    return formatQuestionResult(qEvt, taskDir)
case <-time.After(timeout):
    return timeoutResult(...)
}
```

`handleAsk` writes a `task.ask` notification to the coordinator's inbox before spawning. The
notification carries `ask_task_id`, `from_role`, and the question body. `notifyNewFile` detects
`task.ask` type and dispatches to `questionWaiters[to.role]`. The terminal dispatch path
(`awaitWaiters`) is untouched.

A catch-up scan runs at the top of `handleAwaitTask` (after registering both channels, before
blocking) to find any `task.ask` notifications already in the coordinator's inbox that arrived
while no channel was registered. This closes the re-registration race window.

The non-blocking send in `notifyNewFile` must be changed: move notification files to `read/`
only after a successful channel send. If the default: branch fires (channel full), leave the
notification in inbox so the catch-up scan can find it on re-registration.

**Rationale**

Option B's hard separation between terminal and question channels was the decisive factor. It
satisfies constraint 1 (no breakage to existing terminal-event code) with a physical boundary,
not a behavioral one: a bug in question-event handling cannot reach the `awaitWaiters` channel
that existing coordinator code reads. The `taskEvent.Kind` check in the terminal path never
encounters `EvtQuestion`.

The role-keyed routing for `questionWaiters` is simpler than Option A's task-id routing. A
question for the coordinator routes by `to.role` in the notification — no need to embed the
delegated task_id T in the notification body, no `parent_task_id` chain traversal. The coordinator
can be waiting on any task and still receive questions because the question channel is not
task-specific.

Option A was close. After revision, both A and B converged on writing `task.ask` from `handleAsk`,
closing the main routing complexity gap. The remaining difference: Option A puts EvtQuestion on
a channel that also carries terminal events, while Option B keeps them on separate channels. The
cross-examination confirmed that Option B's three-way select with clearly labeled cases is equally
readable to Option A's two-way select with kind-branching inside the case body. The modest extra
code (~130-150 lines vs. ~90-120) is justified by the invariant clarity.

Option C (polling) was eliminated after revision. The deferred move-to-read/ fix closes the
multi-question drop window for both A and B, removing Option C's main advantage. Option C still
requires `handleAsk` to write `task.ask` notifications, giving it no meaningful implementation
cost advantage. Its 500ms latency for question delivery is a real disadvantage for AI Q&A
workflows where sub-second responsiveness is the baseline.

**Alternatives Considered**

- **Option A: Extend awaitWaiters with EvtQuestion.** Extends the existing channel with a new
  event kind. Slightly smaller diff (~90-120 lines). Requires carrying the delegated task_id T
  in the `task.ask` notification for routing (since `awaitWaiters` is keyed by T). Rejected
  because mixing question events into the terminal-event channel creates a shared failure domain:
  a bug in question-event dispatch could affect the existing terminal path. Option B's separation
  is the more defensible invariant for a production system.

- **Option C: Convert niwa_await_task to hybrid polling.** Adds a 500ms ticker alongside the
  existing `awaitWaiters[T]` channel. Smallest notifyNewFile impact (no changes). Rejected because
  question detection latency (up to 500ms) is unacceptable for AI Q&A workflows where workers are
  already blocked waiting. The multi-question safety advantage does not hold after the deferred
  move-to-read/ fix in Options A and B.

**Consequences**

What changes:
- `Server` gains `questionWaiters map[string]chan taskEvent` and init code.
- `handleAsk` writes a `task.ask` notification to coordinator inbox before returning (new
  side-effect on the worker side).
- `notifyNewFile` gains a detection branch for `task.ask` type dispatching to `questionWaiters`.
- `notifyNewFile`'s terminal dispatch defers the move-to-read/ until after a successful channel
  send (correctness fix, applies to terminal path too).
- `handleAwaitTask` gains a three-way select, catch-up scan, and `formatQuestionResult`.
- The coordinator's programming model gains one new return status: `question_pending`. Existing
  coordinators that don't handle it are not broken — they'll loop or timeout on the next call.

What becomes easier:
- Coordinators can handle in-flight questions from workers without polling or timeouts.
- The Q&A coordination pattern is a first-class flow: delegate task, answer questions as they
  arrive, proceed when task completes.

What becomes harder:
- Coordinator implementations must handle the `question_pending` status if they expect to receive
  questions. Documentation and skill updates required.
- The `notifyNewFile` function handles two dispatch paths (terminal and question); readers must
  understand both.

Files changed: `internal/mcp/types.go`, `internal/mcp/server.go`, `internal/mcp/watcher.go`,
`internal/mcp/handlers_task.go`. Daemon (`internal/cli/mesh_watch.go`) unchanged.
<!-- decision:end -->
