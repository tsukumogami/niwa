# Architecture Review: Coordinator Loop Stall Recovery

## Overview

This review covers the proposed solution for coordinator loop stall recovery across four
implementation phases. The analysis is grounded in the current codebase state as of
the review date.

---

## Phase 1 — Stop Hook (`niwa mesh report-progress`)

### What already exists

`niwa_report_progress` is a fully-implemented MCP tool handler in
`internal/mcp/handlers_task.go`. It already writes `last_progress.at` to `state.json`
under the per-task flock, appends a `progress` entry to `transitions.log`, and delivers
a best-effort `task.progress` inbox message to the delegator. The watchdog in
`runWatchdog` (`internal/cli/mesh_watch.go`) already reads `last_progress.at` on every
2s tick and calls `resetTimer` when the value advances.

### What the design proposes

A new `niwa mesh report-progress` CLI subcommand that reads `state.json`, verifies
role+task_id ownership, and calls `mcp.UpdateState` to advance `last_progress.at`
without any summary or progress body — acting purely as a heartbeat.

### Issues and gaps

**The hook calls a CLI binary instead of the MCP tool.** The stop hook fires outside
the worker's MCP session, so it cannot call `niwa_report_progress` directly. The
proposed CLI subcommand must reach the same `state.json` file the daemon watches.
That path is sound: `mcp.UpdateState` is a library call with no session dependency.

**Ownership check requires reading `state.json`.** The design says the subcommand
"verifies role+task_id ownership." The current authorization model
(`authorizeTaskCall` in `internal/mcp/auth.go`) checks the caller's PPID + start_time
against `TaskWorker.PID` and `TaskWorker.StartTime`. The CLI subcommand runs in a new
process spawned by the hook script, not as the worker itself, so PPID-based
authorization is the only option. The design should explicitly state how the subcommand
identifies itself as belonging to the correct worker — likely by reading `NIWA_TASK_ID`
from env and cross-checking the worker PID recorded in `state.json` against the hook
script's parent PID.

**Silent-on-terminal-task no-op is important.** Workers that have already called
`niwa_finish_task` can still trigger a Stop hook (Claude Code stops after tool
completion). The subcommand must not write `last_progress.at` on a terminal task
because `mcp.UpdateState` will return `ErrAlreadyTerminal` and the hook would exit
non-zero, which Claude Code surfaces to the user. The design mentions this as a no-op,
but the implementation must explicitly handle `ErrAlreadyTerminal` with a zero exit
code.

**Hook script environment.** The stop hook runs with Claude Code's environment. The
subcommand needs `NIWA_INSTANCE_ROOT`, `NIWA_SESSION_ROLE`, and `NIWA_TASK_ID` from
env. These are injected by `spawnWorker` into the worker's `cmd.Env`; Claude Code's
stop hook inherits the spawned process's environment, so this should work. The design
should verify that Claude Code propagates these env vars to hook processes.

**Watchdog polling interval vs hook latency.** The watchdog polls every 2s; the stall
timer defaults to 900s. A single stop-hook heartbeat per turn is more than sufficient
to reset a 900s timer — the gap between turns is far smaller than 900s. No concern here.

---

## Phase 2 — Session ID Capture (`niwa_register_worker_session`)

### What already exists

`session_discovery.go` already implements `DiscoverClaudeSessionID` with three tiers
(env var, PPID walk, project dir scan). The `SessionEntry` type in `types.go` already
has a `ClaudeSessionID` field. These were designed to support coordinator-session
registration, not worker registration.

### What the design proposes

A new `niwa_register_worker_session` MCP tool that the worker calls early in its turn,
writing `ClaudeSessionID` and incrementally tracking `ResumeCount` in `TaskWorker`.
New fields `ClaudeSessionID` and `ResumeCount` on `TaskWorker`, and `MaxResumes` on
`TaskState`.

### Issues and gaps

**`$CLAUDE_SESSION_ID` availability is not guaranteed.** The design relies on the
worker discovering its session ID from `$CLAUDE_SESSION_ID` or `DiscoverClaudeSessionID`.
The PPID walk in tier 2 of `DiscoverClaudeSessionID` was designed for the coordinator
case (hook script invokes CLI). In the worker MCP tool call path, the process hierarchy
is different: Claude Code → MCP server (stdio). The tier-3 project-dir scan by mtime
may pick up a stale session. This is the most fragile part of the design; the
`CLAUDE_SESSION_ID` env var tier is reliable only if Claude Code sets it, which depends
on the Claude Code version.

**Bootstrap prompt change races the first tool call.** The design proposes that the
bootstrap prompt instructs the worker to call `niwa_register_worker_session` before
`niwa_check_messages`. This sequencing is advisory, not enforced. A model that
reorders or omits the registration call leaves `ClaudeSessionID` empty, falling back to
fresh-spawn behavior. That fallback is correct, but the design should acknowledge the
model-compliance assumption.

**`MaxResumes=0` default semantics.** The design says `MaxResumes=0` defaults to 2.
This is sound — zero-value produces correct fallback — but the default logic must live
in `createTaskEnvelope` (where `MaxRestarts: 3` is already seeded) or in the resume
branch of `retrySpawn`. Currently `createTaskEnvelope` does not seed `MaxResumes`, so
adding it there keeps the pattern consistent.

**Idempotent re-registration.** The design lists this as a test case. `ClaudeSessionID`
in `TaskWorker` is a flat string field; overwriting it with the same value is naturally
idempotent. The only concern is a resume that produces a different session ID
(legitimately new after `--resume`). The handler should accept updates — not reject
them as duplicates — and record the new session ID on each resume.

---

## Phase 3 — Resume Path

### What already exists

`handleSupervisorExit` and `retrySpawn` (implied by the codebase structure) already
manage the restart cap via `TaskState.RestartCount` and `TaskState.MaxRestarts`.
`spawnWorker` in `mesh_watch.go` builds and starts the worker command. The watchdog
already kills stalled workers via `escalateSignals`.

### What the design proposes

After a watchdog-triggered kill, `retrySpawn` checks `TaskState.Worker.ClaudeSessionID`,
validates the session file, and calls `spawnWorker` with `claude --resume <session_id>`
instead of a fresh `-p <bootstrap>`.

### Issues and gaps

**Session file integrity check is inherently racy.** The design proposes checking that
`~/.claude/projects/<cwd>/<session_id>.jsonl` exists, is non-empty, and that the last
complete line parses as JSON. Between this check and the `--resume` call, Claude Code
may still truncate or rotate the file. The check can reduce the false-resume rate but
cannot eliminate it. The design should document this as a best-effort gate, not a
correctness guarantee.

**`--resume` argv injection risk.** The design feeds `session_id` into
`claude --resume <session_id>`. The session ID is validated by `sessionIDRegex`
(`^[a-zA-Z0-9_-]{8,128}$`) before storage, which prevents shell injection characters.
The `exec.Command` call (not shell expansion) in `spawnWorker` means no shell
interpretation occurs anyway. This is safe.

**`resume: true` in `TransitionLogEntry`.** The current `TransitionLogEntry` schema
does not have a `resume` boolean. Adding it requires either a new field (minimal, no
breaking change with `omitempty`) or reusing the existing `Signal` or `Reason` fields
(wrong semantic). A new `Resume bool` field with `omitempty` is the right call.

**`ResumeCount` is separate from `RestartCount`.** The design keeps these orthogonal:
resume does not increment `RestartCount`. This is correct — a resume is not a new
attempt from scratch. However, the combined cap check (`ResumeCount < MaxResumes` AND
`RestartCount <= MaxRestarts`) means a task could potentially exhaust both budgets
independently. The interaction should be spelled out: if `RestartCount` is already at
cap, a resume should not be attempted regardless of `ResumeCount`.

---

## Phase 4 — `niwa_ask` Fix

### What already exists

`handleAsk` in `server.go` has two paths: live-coordinator path (writes `task.ask` to
the coordinator inbox) and an ephemeral-spawn fallback (writes `task.delegate`, daemon
claims it, spawns an ephemeral worker). The fallback was introduced to handle the case
where no live coordinator is registered.

### What the design proposes

Remove the ephemeral-spawn fallback entirely and return
`{status: "no_live_session", role: args.To, message: "..."}` when no live coordinator
is found.

### Issues and gaps

**This is a breaking behavior change.** The ephemeral-spawn path exists because early
iterations of the coordinator session registration were unreliable. If the coordinator's
`maybeRegisterCoordinator` call (in `handleCheckMessages` / `handleAwaitTask`) has not
fired yet when the first worker calls `niwa_ask`, removing the fallback means the ask
silently fails instead of queuing. The design assumes Phase 2 (session registration via
`niwa_register_worker_session`) makes coordinator registration reliable before any
worker calls `niwa_ask`. This sequencing assumption should be explicit.

**`no_live_session` is a new status value.** Workers that parse `niwa_ask` results need
to handle this new status. If existing workers don't check for it, they may misinterpret
the response as a successful empty answer. The design must specify what behavior is
expected of callers when `no_live_session` is returned.

**The fix is independent but has ordering implications.** Phase 4 is listed as
independent, but deploying it before Phase 2 (which makes coordinator registration
reliable) creates a regression window. Deploying them together or ensuring Phase 2 is
deployed first avoids this.

---

## Cross-Cutting Observations

### Stop hook as a fallback for blocked MCP calls

The stop-hook heartbeat (Phase 1) is a good defense against the 900s watchdog firing
on long single-tool calls. However, if the blocking call is itself an MCP call (e.g.
`niwa_await_task` with a 600s timeout), the worker turn does not end until that call
returns, so the stop hook does not fire during the blocking period. Phase 1 addresses
inter-turn stalls, not intra-turn blocking MCP calls. Phase 3 (resume after watchdog
kill) is the correct remedy for intra-turn stalls; Phase 1 is additive, not redundant.

### `niwa_register_worker_session` vs the existing `maybeRegisterCoordinator` pattern

`maybeRegisterCoordinator` is an implicit registration triggered as a side effect of the
first `niwa_check_messages` or `niwa_await_task` call. The proposed
`niwa_register_worker_session` is explicit and worker-only. The explicit model is
correct for workers because they need to register before `niwa_check_messages` (so the
session ID is available before any work starts). The implicit coordinator pattern
remains appropriate for coordinators since it requires no prompt changes.

### `ClaudeSessionID` field collision with `SessionEntry`

`SessionEntry` (coordinator registry) already has a `ClaudeSessionID` field. The
proposed design adds the same field to `TaskWorker`. These serve different purposes
(coordinator vs worker) and live in different on-disk locations, so there is no
collision. However, documentation should clarify that `SessionEntry.ClaudeSessionID`
is for coordinator lookups and `TaskWorker.ClaudeSessionID` is for worker resumption.

### Phase sequencing is correct

Phase 1 (stop hook) is independent and delivers immediate stall-detection improvement.
Phase 2 (session ID capture) is a prerequisite for Phase 3 (resume path). Phase 4
(`niwa_ask` fix) depends on Phase 2 being in production first. The proposed sequence
is correct.

---

## Summary of Missing Details

1. The ownership-check mechanism for the `niwa mesh report-progress` CLI subcommand
   (how it maps hook-script PPID to the worker PID in `state.json`) is not fully
   specified.
2. The `$CLAUDE_SESSION_ID` availability guarantee (whether Claude Code sets it for
   worker MCP server processes) is unverified in the current codebase.
3. The interaction between `ResumeCount` and `RestartCount` caps — specifically,
   whether hitting `MaxRestarts` should suppress further resumes — is not addressed.
4. The `no_live_session` response from Phase 4 requires callers to handle a new status;
   backward compatibility for existing worker prompts should be stated explicitly.
5. `TransitionLogEntry` needs a new `Resume bool \`json:"resume,omitempty"\`` field to
   support Phase 3 audit requirements; the current schema has no such field.
