# Decision 1: Stop Hook Automation Level

## Question

How should the stop hook call `niwa_report_progress` — by directly updating task
state via a CLI subcommand (fully automated, zero agent awareness), or by
injecting a reminder message that the agent reads and acts on (simpler, but
agent-dependent)?

## Chosen: Option A — Fully automated via new CLI subcommand

A new `niwa mesh report-progress --task-id $NIWA_TASK_ID` CLI subcommand writes
directly to `state.json` via `mcp.UpdateState`, resetting `last_progress.at` at
every turn boundary without any agent involvement. The stop hook script reads
`NIWA_TASK_ID` from the environment (set by the daemon at spawn time, line 909 of
`internal/cli/mesh_watch.go`) and calls the subcommand. Claude Code injects the
hook's stdout back as a user message only when the output is non-empty; the script
produces no output on success, so the agent sees nothing and the watchdog timer
resets silently.

The implementation is safe to run concurrently with the MCP server and the daemon
watchdog because `mcp.UpdateState` already enforces the full flock + atomic-rename
+ fsync discipline documented in `internal/mcp/taskstore.go` (lines 8–16). The
watchdog in `runWatchdog` (`mesh_watch.go`, lines 1112–1128) reads `last_progress.at`
under a shared flock every two seconds and resets its stall timer when the value
advances — so any writer that goes through `UpdateState` is visible to the watchdog
without any additional coordination. A CLI call from the stop hook is structurally
identical to the MCP handler path in `handleReportProgress`
(`internal/mcp/handlers_task.go`, lines 457–499): both call `UpdateState` with a
mutator that sets `next.LastProgress`.

The CLI subcommand bypasses the MCP `kindExecutor` authorization check
(`internal/mcp/auth.go`, lines 172–221). That check cross-validates PPID chain
and `start_time` against `state.json.worker.*`, which are meaningless for a hook
script that is a child of the Claude Code harness rather than the worker process
itself. The subcommand instead validates ownership by reading `state.json` directly
and verifying that `NIWA_SESSION_ROLE` matches `state.json.worker.role` and that
`NIWA_TASK_ID` matches `state.json.task_id` — a lighter check that is sufficient
for a same-process-tree invocation inside a known worker workspace. If `NIWA_TASK_ID`
is unset or empty (for example, in a coordinator session where no task is active),
the script exits silently without writing anything and produces no output.

The one trade-off accepted is the new subcommand: `internal/cli/mesh.go` gains a
`report-progress` child command. This is a small, well-bounded addition. The
subcommand reuses `mcp.UpdateState` and `mcp.taskDirPath`, so no new storage logic
is introduced; the existing flock contract covers all concurrent-write safety.

## Alternatives Considered

**Option B — Reminder injection**: The stop hook outputs a reminder string; Claude
Code injects it as a user message; the agent is expected to call
`niwa_report_progress` in the next turn. Rejected because it violates the
"abstraction integrity" constraint: skills and application code must not carry niwa
awareness, and agent compliance cannot be guaranteed. An agent focused on a
long-running shell command may defer or ignore the reminder, leaving the watchdog
timer unreset. The fix must work without agent cooperation, which reminder injection
cannot guarantee by construction.

**Option C — Hybrid: automated with fallback reminder**: The hook first attempts the
CLI call and falls back to a reminder if `NIWA_TASK_ID` is unset or the call fails.
Rejected because the fallback reintroduces the agent-dependency that Option A
eliminates. If the CLI call fails in a way that indicates a real problem (corrupted
state, missing instance root), emitting a reminder does not help the watchdog and
misleads the agent. The correct behavior on failure is silent exit so the agent is
not interrupted; any real failure should be visible in the task's `transitions.log`
or the daemon log rather than as a user-facing message. The added complexity is not
justified by the marginal resilience gain.

## Assumptions

- `NIWA_TASK_ID`, `NIWA_INSTANCE_ROOT`, and `NIWA_SESSION_ROLE` are reliably
  present in every worker's environment. This is confirmed at `mesh_watch.go`
  lines 907–909: the daemon sets all three unconditionally at spawn time, and
  they persist in every child process of the worker session, including hook scripts.
- The stop hook fires at every turn boundary for the lifetime of the worker
  session. This is the documented behavior of Claude Code's `Stop` hook mechanism
  and is not verified in the niwa source; it is a property of the Claude Code
  harness.
- The CLI subcommand will not introduce a meaningful latency penalty. `UpdateState`
  holds an exclusive flock for the duration of a state.json rewrite, which is a
  sub-millisecond operation under normal conditions. The watchdog polls every two
  seconds, so the flock contention window is negligible.
- Terminal tasks (completed, abandoned, cancelled) should be handled gracefully:
  `UpdateState` returns `ErrAlreadyTerminal` when the task is in a terminal state.
  The CLI subcommand should treat this as a no-op and exit silently, since a
  completed task no longer needs watchdog resets.

## Confidence

High — the locking model, environment variable injection, and watchdog polling
logic are all confirmed by direct code inspection; the design has no speculative
elements.
