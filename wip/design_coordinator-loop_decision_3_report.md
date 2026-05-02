# Decision 3: niwa_ask No-Live-Session Error Format

## Question

What error response should `niwa_ask` return when the target role has no live session — a new
status value, an existing vocabulary term, or an error object — and what information should it
include so callers can take corrective action?

## Chosen: Option A — New status value `no_live_session`

Return a typed `textResult` (not an `errResult`) with `status: "no_live_session"`, the `role`
name, and a human-readable `message`. The handler returns this immediately, before creating any
task store entry, because no ask task exists to reference. Callers detect the condition with a
single string comparison on `status` — the same pattern coordinators already use to distinguish
`question_pending` from `completed` in the `niwa_await_task` re-wait loop. The response shape
fits naturally alongside the existing ask-related statuses: `question_pending` means the question
was routed, `timeout` means the wait period elapsed, `no_live_session` means routing was
impossible before any wait began. These three conditions are semantically distinct and map cleanly
to three different caller actions: answer, retry/abandon, and investigate.

This is the correct choice over the alternatives because the condition is not an API error (the
call was well-formed) and not a timeout (no wait occurred). The `textResult` path keeps the
response in the `Result` field of the JSON-RPC response, consistent with every other non-error
outcome in `handleAsk`. `IsError: true` responses in this codebase signal protocol or
authorization failures — wrong role, bad arguments, unknown task IDs. A missing coordinator
session is a runtime topology condition that the caller is expected to observe and act on, not a
programming error. Using `errResultCode` would conflate the two categories and would also require
an existing R50 code, none of which describe this condition. An existing coordinator that
pattern-matches on `status` fields and falls through to a default case on unknown values will
fail silently rather than crashing — the backward-compatibility bar from the decision drivers.

## Exact Response Shape

```json
{
  "status": "no_live_session",
  "role": "coordinator",
  "message": "No live session found for role 'coordinator'. The role may have completed its task or not yet started."
}
```

The `role` field carries the value of `args.To` as provided by the caller. No `task_id` is
included because no ask task is created in this path — the handler returns before
`createAskTaskStore` is called.

## Alternatives Considered

**Option B — Reuse `timeout` with `reason: "no_live_session"`**: Would conflate two distinct
conditions — a timed-out wait (an ask task exists and ran out of time) versus an immediate
rejection (no session, no task created, no wait started). Rejected because a caller treating
any `timeout` as retriable would re-issue the ask unnecessarily, and because `timeout` responses
carry a `task_id` that does not exist in this path, creating a structural inconsistency callers
would have to defend against.

**Option C — MCP protocol-level error (`IsError: true` / JSON-RPC error)**: The server's
`tools/call` dispatch always places the `toolResult` in the JSON-RPC `Result` field, never in
the `Error` field, for all handler returns (line 163 in server.go). Returning a true protocol
error would require changing the dispatch path in `callTool` or `handleRequest`, not just
`handleAsk` — a wider change than warranted. Rejected also on semantic grounds: `IsError: true`
in this codebase means bad arguments or authorization failures, not topology conditions. The R50
error codes (`NOT_TASK_OWNER`, `NOT_TASK_PARTY`, `TASK_ALREADY_TERMINAL`, `BAD_PAYLOAD`,
`BAD_TYPE`, `UNKNOWN_ROLE`) do not include a session-liveness code, and the handler comment
explicitly states "No new codes are introduced."

## Assumptions

- `handleAsk` currently falls back to the ephemeral spawn path when no live coordinator is found.
  The fix removes that fallback and replaces it with the `no_live_session` return for the
  coordinator→worker direction. The worker→coordinator direction (already fixed by PR #93) is
  unaffected.
- The spawn fallback is only triggered when `args.To == "coordinator"` and
  `lookupLiveCoordinator` returns `(_, false)`. If `niwa_ask` is generalized to non-coordinator
  targets in the future, a `lookupLiveRole` variant would be needed; the response shape chosen
  here accommodates that generalization via the `role` field.
- No ask task entry is written to disk before returning `no_live_session`. This avoids orphaned
  task directories with no worker and no routing path.
- Callers that do not inspect `status` on `niwa_ask` responses will silently drop the signal.
  This is acceptable: the same risk exists for `timeout` today, and coordinator skill content
  already documents the obligation to check `status` after every `niwa_ask` call.

## Confidence

High — the response shape fits the existing vocabulary without ambiguity, the implementation
requires no changes outside `handleAsk`, and the R50 constraint (no new error codes) is
respected by using `textResult` rather than `errResultCode`.
