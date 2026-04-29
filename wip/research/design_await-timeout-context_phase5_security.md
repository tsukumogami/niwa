# Security review: await-timeout-context

## Dimension analysis

### External artifact handling
**Applies:** No
This design only reads fields from an in-process TaskState struct (LastProgress.Summary,
MaxRestarts) and adds them to JSON response payloads. No external content is
downloaded, parsed, or executed.

### Permission scope
**Applies:** No
No new filesystem paths, network sockets, or process permissions are required. The
response is returned over the existing Unix socket transport. handleAwaitTask and
formatTerminalResult already have access to TaskState as part of their normal operation.

### Supply chain or dependency trust
**Applies:** No
No new dependencies are introduced. The change is purely additive within existing
functions using existing types.

### Data exposure
**Applies:** Yes (low severity, no action needed)
LastProgress.Summary is a short string (max 200 chars) that was supplied by the
worker via niwa_report_progress. It's already returned by niwa_query_task, so this
design does not expand who can read it — only when they see it (at await timeout /
task completion rather than on explicit query).

The documented guarantee that progress *bodies* are never persisted is not affected.
This design only reads Summary and At from the already-persisted TaskProgress struct.

**Severity:** Low. The coordinator who calls niwa_await_task is already the
authorized recipient of task information. Including LastProgress.Summary in the
response is equivalent to the coordinator calling niwa_query_task immediately after
timeout — same data, same caller, fewer round trips.

## Recommended outcome

**OPTION 3 - N/A with justification:**

This design reads fields already stored in TaskState (LastProgress.Summary, At;
MaxRestarts) and includes them in existing MCP tool responses. No new data is
persisted, no external input is processed, no new trust boundaries are crossed, and
the documented guarantee that progress bodies are never stored is not affected.
LastProgress.Summary is already accessible via niwa_query_task; this change surfaces
it at await timeout and task completion without expanding the set of callers who can
read it.

## Summary

No security dimensions require design changes or additional documentation. The change
is a response payload addition reading already-persisted, already-accessible fields.
Option 3 (N/A with justification) is the correct outcome.
