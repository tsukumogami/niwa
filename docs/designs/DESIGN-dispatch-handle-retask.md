---
upstream: docs/prds/PRD-dispatch-handle-retask.md
---

# DESIGN: Dispatch handle retask

## Status

Proposed

## Context and Problem Statement

The PRD requires a `niwa retask <target> <prompt>` command that delivers
a follow-up instruction to a dispatched session with single-owner
semantics, and a shared primitive niwa watch can adopt for chainable
review continuation.

The technical problem: Claude Code offers no in-place delivery into a
live background session, so the only supported route is relaunch-based —
stop the worker's process if needed, resume the transcript into a new
background session, and live with the platform minting a new session id
while the superseded session's job entry lingers. That route crosses
four hazards the design must neutralize:

1. **Capture ambiguity.** niwa correlates sessions to instances by jobs
   cwd. After a resume, two job entries share the instance cwd and
   `captureSessionID` reports ambiguity — today's watch continuation
   returns empty ids and degrades to once-per-session (#211).
2. **Mapping integrity.** The durable mapping under
   `.niwa/sessions/<session-id>.json` is keyed by session id; a rebind
   replaces the mapping's identity, not just a field, and a crash
   between resume and rebind must not strand the instance.
3. **Races.** `reapOpportunistically` runs at the start of every
   create/dispatch and deletes mappings for dead sessions; a retask's
   stop window makes the session look dead. Concurrent retasks against
   one target contend for the same rebind.
4. **Containment.** Watch's review sessions run under a no-egress
   sandbox that must be re-asserted through the same settings-applying
   launch path on every relaunch.

The affected code: `internal/cli/dispatch.go` (launch + capture),
`internal/cli/dispatch_capture.go` (cwd correlation),
`internal/workspace/session_map.go` (mapping store),
`internal/cli/reap.go` (liveness + reclamation), `internal/cli/list.go`
(observability join), and `internal/cli/watch.go` (`continueReview`,
the existing one-shot form of this flow).

## Decision Drivers

- **Single-owner invariant (R3/R6):** one live session per instance;
  the niwa handle survives while session ids rotate.
- **Chainability (R5/R7):** retask N+1 must work; capture must resolve
  deterministically with two entries on one cwd.
- **Fail-closed (N1/R4):** every failure path leaves prior state
  usable; busy/attached/gone workers are refused with distinct errors.
- **Race safety (N2):** concurrent retask-retask and retask-reap cannot
  corrupt state; interleavings must be testable through seams.
- **Supported surfaces only (R8):** claude resume/stop/respawn and
  jobs-dir reads; no state.json edits, no root, no fenced channels.
- **Replaceable delivery (R9):** the delivery step sits behind one seam
  so a future in-place channel swaps in without interface change.
- **Reuse over rebuild:** watch's continueReview already implements a
  one-shot version; the primitive should generalize it, not duplicate
  it, and watch must keep its sandbox re-assertion.
- **House style:** cobra command with SilenceErrors/SilenceUsage and
  `niwa: error: ...` formatting, `--json` output, seam-injected
  dependencies for offline tests.
