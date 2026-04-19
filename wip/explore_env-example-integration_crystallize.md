# Crystallize Decision: env-example-integration

## Target artifact: PRD

## Rationale

Two rounds of research converged the feature's WHAT without leaving
open product questions. Remaining unknowns are design-level (exact
entropy threshold, exact CLI surface for the audit command), not
requirements questions. A PRD locks in the capability, scope,
acceptance criteria, and non-goals; a follow-up design doc will then
resolve the implementation-level decisions.

## Signals pointing at PRD over alternatives

- **Not a design doc** — the requirements (merge precedence,
  drift policy, secret rule, opt-outs) are product-level choices
  that deserve a user-facing requirements artifact before
  committing to an implementation.
- **Not a decision record** — too many interlocking requirements;
  an ADR captures one decision, this has five or six.
- **Not just-do-it** — the feature has security implications
  (secret detection, guardrail extension) and a migration story
  (four-state matrix); it needs acknowledged acceptance criteria
  before code lands.

## Next step

Hand off to `/shirabe:prd` with a scope synthesizing the exploration
findings. The PRD skill should skip its Phase 1 (scoping) and resume
at Phase 2 (research/discover) using the handoff artifact.
