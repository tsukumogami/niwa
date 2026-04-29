# Exploration Decisions: niwa-ask-live-coordinator

## Round 1

- Crystallize as design doc: what to build is clear from issue #92 AC; technical approach
  has real decisions (session liveness, early-return shape, routing logic) that warrant design
  doc treatment before implementation.

- Response mechanism is `niwa_finish_task`, not `niwa_send_message`: the worker's `awaitWaiter`
  channel only fires on task-terminal events. `niwa_send_message` alone would leave the worker
  permanently blocked.

- No second discovery round: session registry mechanism is confirmed (PID-alive, CLI-registered,
  MCP never consults it). Remaining gaps are design decisions, not unknowns.

- `niwa_wait` terminology dropped in favor of `niwa_await_task` (actual tool name). Scope
  document used `niwa_wait` as informal shorthand; implementation uses `niwa_await_task`.

- No timeout/fallback-to-spawn: questions queue until the coordinator next polls. If coordinator
  never resumes, the question waits indefinitely — this is acceptable per the user's explicit
  direction. No additional recovery mechanism needed in this design.
