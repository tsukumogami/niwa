# Verdict: PASS

## Findings

### 1. Brief journeys and scope-boundary coverage — PASS
Every brief journey maps to a requirement or user story:
- "Coordinator re-tasks an idle worker" → User Story 1, R1, R2, R4(a).
- "Operator iterates without attaching" → User Story 2, R1, N3.
- "Watch chains a review continuation" → User Story 3, R7, and acceptance criteria on watch chaining.
- "Re-task after a long idle" → R4(b) (stopped worker, job entry intact), D2 (revive-on-retask via respawn).

Every in-scope boundary item maps: niwa command → R1; safe ownership (one live session, atomic mapping, no orphan) → R3; idle + stopped incl. revive → R4/D2; fork-on-resume with stable handle + superseded cleanup → R5/R6/D5; watch adoption → R7; documenting platform constraints → motivating_context + Known Limitations.

Every out-of-scope item maps to the PRD's Out of Scope section (mid-turn interrupt, human steering + keep-alive, channel plugin, Claude Code changes, non-dispatched/foreign-workspace sessions), with the PRD correctly extending it (non-ephemeral sessions without a mapping) rather than dropping anything.

### 2. Five deferred questions closed — PASS
All five carry explicit closure markers in Decisions and Trade-offs:
- Command surface → D1 "(Closes the brief's command-surface question.)"
- Keep-alive coupling → D2 "(Closes the keep-alive question.)"
- Channel-path adoption → D3 "(Closes the channel-adoption question.)"
- Sandboxed-watch interaction → D4 "(Closes the sandbox-interaction question.)"
- Superseded-session cleanup → D5 "(Closes the cleanup question.)"
Each states the decision, a rejected alternative, and rationale.

### 3. Error/edge cases from research — PASS
- Reaper race window (research §3, fact 4) → N1 (fail-closed before rebind), N2 (retask concurrent with reap never yields a reaped instance with a live session).
- Capture ambiguity / multi-job-same-cwd (research §1, §5, #211) → R5, D7, acceptance criterion on ambiguous capture, and the unit-test acceptance criterion exercising two job entries sharing the instance cwd (newest wins; tie/invalid fails closed).
- Busy/attached workers → R4 (fails closed with distinct error), D6, Known Limitations "Busy means refused," and a dedicated acceptance criterion.
- Gone job entry → R4, acceptance criterion (distinct error per cause).
- Concurrent retasks → N2, acceptance criterion.

### 4. Requirement-area coverage — PASS
- Delivery: R1, R2, R9.
- Ownership: R3, R6.
- Observability: N3 (list human + --json, keep-alive reporting, error content).
- Security/privilege posture: R8 (supported surfaces only, no root/managed settings), N4 (no new privileges).
- Watch adoption: R7 + D4.
- Forward-compat: R9 (replaceable delivery seam) + D3.

### 5. Content boundaries — PASS (with one watch item, non-blocking)
The PRD stays at behavior level. It does not name niwa-internal functions from the research (no `matchSessionByCwd`, `captureSessionID`, `continueReview`, `sessionLive`, seam var names) as requirements. R8's references to "resume, stop, respawn, jobs-dir reads" describe the constraint (documented/stable CLI surfaces only), not an implementation. R5's "newest-registration wins, validated before use" specifies an observable disambiguation contract rather than an algorithm implementation; it reads as an acceptance-level behavior spec, which is defensible for a PRD. Design should own the exact selection mechanism. This is a note for the downstream design, not a completeness gap.
