---
status: Planned
problem: |
  When niwa_await_task times out, the response contains only status, task_id,
  current_state, and timeout_seconds. The worker's last reported progress summary is
  dropped, so a coordinator re-awaiting after a timeout has no way to know whether
  the task is 10% or 90% done, or whether it's stalled. Terminal results have the
  same gap: restart_count appears with no indication of the retry cap, making it
  impossible to gauge whether two restarts represent normal resilience or near-failure.
decision: |
  Add last_progress (TaskProgress struct with summary and timestamp) to timeout
  responses when it exists in TaskState. Add last_progress and max_restarts to all
  terminal results — max_restarts only when restart_count > 0. Both changes read
  fields already stored in TaskState; no new storage, no log parsing, no schema
  changes.
rationale: |
  TaskState already stores LastProgress from niwa_report_progress calls, and
  MaxRestarts from the task configuration. The information gap is entirely in how
  formatTerminalResult and handleAwaitTask build their response payloads — they
  don't include these fields. Adding them closes the gap without any new persistence
  or protocol surface. Parsing transitions.log for per-restart exit codes would give
  richer context but adds complexity disproportionate to the gain; max_restarts
  already answers the coordinator's practical question of whether the restart count
  is concerning.
---

# DESIGN: await_task timeout and terminal result context

## Status

Planned

## Context and problem statement

When a coordinator delegates a long-running task and calls niwa_await_task, timeouts
return a minimal response:

```json
{"status":"timeout","task_id":"...","current_state":"running","timeout_seconds":600}
```

The worker may have called niwa_report_progress multiple times, but none of that
appears in the timeout response. The coordinator has to either re-await blindly or
make a separate niwa_query_task call just to find out what the worker last reported.
The mesh skill doc already documents the re-await loop as normal behavior; the gap
is that re-awaiting is uninformative.

A related problem exists in terminal results. When a task completes or is abandoned,
the result includes restart_count but nothing else:

```json
{"status":"completed","task_id":"...","restart_count":2,"result":{...}}
```

Seeing restart_count: 2 provides no way to judge whether that's concerning. The
coordinator doesn't know if the cap was 3 (near-failure) or 10 (normal resilience).
A separate niwa_query_task call does return max_restarts and last_progress, but
having to call it to recover information available in TaskState at result time is
a gap.

Both issues trace to the same root cause: handleAwaitTask and formatTerminalResult
build their response payloads from a small subset of TaskState fields, ignoring
LastProgress and MaxRestarts.

## Decision drivers

- TaskState.LastProgress already stores the most recent niwa_report_progress call
  (summary + RFC3339 timestamp). No new storage is needed.
- TaskState.MaxRestarts is already set at task creation. Including it alongside
  restart_count is a one-field addition.
- Progress bodies are explicitly not persisted (documented security guarantee in
  TaskProgress). This design must not touch that guarantee.
- The fix should not require a new MCP tool or state.json schema change.
- Parsing transitions.log for per-restart exit codes is out of scope: it adds a file
  read to every terminal result and makes response size unpredictable.

## Considered options

### Decision 1: What to add to the timeout response

The coordinator's goal after a timeout is to answer two questions: is the task
progressing? And should I re-await or investigate? The current timeout response
answers neither. TaskState has two candidate fields: LastProgress (summary + at) and
StateTransitions (the full audit trail). A third option is surfacing elapsed time
from Worker.StartTime.

#### Chosen: add last_progress (TaskProgress{summary, at}) when non-nil

LastProgress is the TaskProgress struct stored by niwa_report_progress. It has two
fields: Summary (truncated to 200 chars) and At (RFC3339 timestamp). Including both
is the minimal addition that lets a coordinator assess task health: Summary says what
the worker is doing, At says when it last reported, and together they answer whether
the task looks stalled. The nil guard means the field is absent from responses for
tasks that never called niwa_report_progress, keeping the response clean.

#### Alternatives considered

**Full state_transitions array:** includes the complete transition history. Rejected
because it's verbose, unpredictably sized, and the coordinator doesn't need the full
history to assess health — they need the most recent signal.

**Elapsed seconds from Worker.StartTime:** computable and would show total runtime.
Rejected because last_progress.at already provides relative timing context (how long
ago was the last checkpoint), which is more actionable than wall-clock elapsed time.
Elapsed time without progress context doesn't help assess whether the task is
healthy.

**Inline summary string at top level:** avoids the nested struct. Rejected because
losing the At timestamp degrades the staleness assessment. A progress summary
reported 2 minutes ago versus 2 hours ago is a different signal, and the coordinator
needs both.

---

### Decision 2: What restart context to include in terminal results

restart_count in a terminal result is currently uninterpretable without knowing the
cap. A coordinator seeing restart_count: 2 can't tell if the task nearly failed or
handled a transient blip normally. MaxRestarts (default 3 if zero) is already in
TaskState. LastProgress is also available and independently useful for completed
tasks regardless of restart count.

#### Chosen: add max_restarts when restart_count > 0; add last_progress when non-nil

max_restarts answers the coordinator's practical question: is restart_count: 2 out
of 3 near-failure, or restart_count: 2 out of 10 routine? The conditional guard
(only when restart_count > 0) keeps the payload clean for tasks that completed
cleanly. last_progress is included unconditionally when present because it's useful
for any terminal state — it shows the last checkpoint the worker reported, which
complements the outcome fields (result, reason, cancellation_reason).

The combination means a coordinator inspecting a completed task with restart_count: 2
sees max_restarts: 3 (concerning) or max_restarts: 5 (less so), plus the last
progress the worker reported before finishing.

#### Alternatives considered

**Parse transitions.log for per-restart exit events:** would surface exit codes and
timestamps for each restart, explaining specifically what caused each one. Rejected
because it requires a file read per terminal result, makes response size
unpredictable, and adds parsing logic disproportionate to the gain. The coordinator's
practical question is severity, not forensics; max_restarts answers it without the
complexity.

**Add max_restarts unconditionally:** simpler guard logic. Rejected because
max_restarts alongside restart_count: 0 is noise — it implies the coordinator should
be thinking about retries when there were none. The conditional keeps the payload
semantically clean.

**Add last_progress only when restart_count > 0:** ties last_progress to restart
context, which is artificial. last_progress is independently useful for any
completed task (it shows the last checkpoint), and restricting it to restart cases
would confuse future readers.

## Decision outcome

**Chosen: 1A + 2A**

### Summary

Two response payload changes, both reading fields already in TaskState.

The timeout response from handleAwaitTask gets a `last_progress` field when
TaskState.LastProgress is non-nil. The field is the TaskProgress struct as JSON:
`{"summary":"...","at":"2026-04-29T01:00:00Z"}`. A coordinator who times out and
re-awaits immediately sees what the worker last reported and when, without a separate
niwa_query_task call.

Terminal results from formatTerminalResult get two conditional additions. When
TaskState.LastProgress is non-nil, `last_progress` appears in the payload (same
struct as above). When TaskState.RestartCount > 0, `max_restarts` appears alongside
restart_count so the coordinator can interpret severity. These additions apply to all
three terminal states (completed, abandoned, cancelled).

Both changes are nil-safe: last_progress is omitted when no progress was ever
reported, and max_restarts is omitted for tasks with zero restarts.

### Rationale

The decisions reinforce each other: both surface already-available TaskState fields
that were being dropped from response payloads. The common constraint — don't add
new persistence, don't parse log files, don't change the state.json schema — is
satisfied by both. Accepting that this doesn't explain *why* each restart happened
(no exit codes, no per-restart timestamps) is the main trade-off, and it's the right
one: the coordinator's practical need is severity assessment, not forensic
reconstruction.

## Solution architecture

### Overview

Two payload construction sites in `internal/mcp/handlers_task.go` are updated to
include additional TaskState fields. No new types, no new storage, no protocol
changes.

### Components

- `handleAwaitTask` (handlers_task.go ~L291-302): builds the timeout response map.
  Reads `st.LastProgress` and adds it to the map when non-nil.

- `formatTerminalResult` (handlers_task.go ~L697-715): builds the terminal result
  payload. Reads `st.LastProgress` and `st.RestartCount`/`st.MaxRestarts`.

- `TaskProgress` (types.go): the existing struct `{Summary string, At string}`.
  No changes needed.

### Key interfaces

Timeout response (after fix):
```json
{
  "status": "timeout",
  "task_id": "...",
  "current_state": "running",
  "timeout_seconds": 600,
  "last_progress": {
    "summary": "Implemented phase 2, running tests",
    "at": "2026-04-29T01:03:00Z"
  }
}
```

`last_progress` is absent (not null) when no progress was ever reported.

Terminal result (after fix, with restart_count > 0):
```json
{
  "status": "completed",
  "task_id": "...",
  "restart_count": 2,
  "max_restarts": 3,
  "last_progress": {
    "summary": "PR created, all checks passed",
    "at": "2026-04-29T01:15:00Z"
  },
  "result": {...}
}
```

`max_restarts` is absent when `restart_count == 0`. `last_progress` is absent when
no progress was ever reported.

### Data flow

Both changes are read-only from the TaskState loaded at the start of each operation.
`handleAwaitTask` already has `st` in scope at the timeout branch. `formatTerminalResult`
already receives `*TaskState` as its argument.

## Implementation approach

### Phase 1: timeout response

Update the timeout response map in `handleAwaitTask` to include `last_progress`
when `st.LastProgress != nil`.

Deliverables:
- `internal/mcp/handlers_task.go`: one conditional map assignment in the timeout
  branch of handleAwaitTask

### Phase 2: terminal result

Update `formatTerminalResult` to include `last_progress` when non-nil and
`max_restarts` when `restart_count > 0`.

Deliverables:
- `internal/mcp/handlers_task.go`: two conditional map assignments in
  formatTerminalResult

### Phase 3: tests

Add test cases covering both additions: timeout response with and without
last_progress; terminal result with and without last_progress; terminal result
with max_restarts present (restart_count > 0) and absent (restart_count == 0).

Deliverables:
- `internal/mcp/handlers_task_test.go` or relevant test file: new test cases

## Security considerations

This design reads fields already stored in TaskState (LastProgress.Summary,
MaxRestarts) and includes them in MCP tool responses. No new data is persisted, no
external input is processed, and the documented guarantee that progress bodies are
never stored is not affected.

**External artifact handling:** not applicable. The design only reads from an
in-process struct and adds fields to JSON responses. No external downloads or
executions.

**Permission scope:** not applicable. No new filesystem access, network calls, or
process permissions are required. The response payload is returned over the existing
Unix socket transport.

**Supply chain or dependency trust:** not applicable. No new dependencies.

**Data exposure:** LastProgress.Summary is already returned by niwa_query_task and
is visible to any process that can call MCP tools. Including it in timeout and
terminal responses does not expose it to any new caller. The progress body is
explicitly not persisted (existing guarantee) and is not included here.

## Consequences

### Positive

- Coordinators can assess in-flight task health from a timeout response alone,
  without a separate niwa_query_task call.
- Coordinators can interpret restart_count in terminal results by comparing against
  max_restarts.
- The fix is two conditional map assignments in one file; minimal review surface.
- No breaking changes: existing callers that don't read the new fields are unaffected.

### Negative

- Terminal results for tasks with restart_count > 0 are slightly larger (two
  additional fields). This is negligible in practice.
- The design doesn't explain *why* restarts happened (no exit codes or per-restart
  timestamps). A coordinator who needs forensic detail still needs to consult
  transitions.log or niwa_query_task.

### Mitigations

- The added fields are absent (not null) when not applicable, so response parsers
  don't need to handle unexpected null values.
- The scope of "why restarts happened" (transitions.log parsing) is explicitly out
  of scope. If coordinators consistently need that level of detail, it can be
  addressed as a separate issue.
