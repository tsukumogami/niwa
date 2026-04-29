---
status: Proposed
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
---

# DESIGN: Route niwa_ask to live coordinator session

## Status

Proposed

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

## Decisions Already Made

The following decisions were made during the exploration phase and should be treated as constraints by this design:

- **Response mechanism is `niwa_finish_task`, not `niwa_send_message`.** The worker's `awaitWaiter` channel fires only when the ask task reaches terminal state. `niwa_send_message` alone does not trigger this path and would leave the worker permanently blocked. The coordinator must call `niwa_finish_task(task_id=<ask_task_id>, outcome=completed, result=<answer>)` to unblock the asker.
- **No timeout/fallback-to-spawn.** Questions queue until the coordinator next contacts the daemon via `niwa_check_messages` or `niwa_await_task`. If the coordinator never resumes, the question waits indefinitely. This is the accepted behavior.
- **Both `niwa_check_messages` and `niwa_await_task` are delivery points.** The coordinator picks up questions at whichever of these calls comes next. This prevents the deadlock case where the coordinator is blocking on `niwa_await_task` while the worker is blocking on `niwa_ask`.
- **`niwa_await_task` is the correct tool name.** "niwa_wait" is informal shorthand used in the issue and exploration; the implemented tool is `niwa_await_task`.

## Considered Options

_To be filled during design phases._

## Decision Outcome

_To be filled during design phases._

## Solution Architecture

_To be filled during design phases._

## Implementation Approach

_To be filled during design phases._

## Security Considerations

_To be filled during design phases._

## Consequences

_To be filled during design phases._
