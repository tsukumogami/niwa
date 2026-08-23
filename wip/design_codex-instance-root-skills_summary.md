# Design Summary: codex-instance-root-skills

## Input Context (Phase 0)
**Source PRD:** docs/prds/PRD-codex-instance-root-skills.md (In Progress)
**Problem (implementation framing):** Deliver workspace plugin skills and
niwa's own plugin at the instance root for Codex through the existing plan
and procedure machinery, bind rows 18 and 19 for both agents through the
route-appropriate registries without inert bookkeeping, re-gate the
dispatch warning on the payload-scope fact, and keep the layout scans and
trust line intact.

**Chain context:** invoked under /scope's parent_orchestration sentinel
(active_child: design), --auto. Fallback shapes in effect:
decision-bypass-with-inline-resolution (Phase 2), serial-self-jury +
parent-delegated-approval (Phase 6). Terminal status: Proposed.

## Security Review (Phase 5)
**Outcome:** Option 2 (document considerations)
**Summary:** Widens where declared content is readable, not what a
session can do; the one novel hazard (upward-searching exclude writes)
is named and forbidden in the design.

## Final Review (Phase 6)
**Jury:** serial-self (dispatch fallback); all three rubrics PASS, one
architecture fix applied (increment 4 wording). Strawman check PASS.
**Validator:** shirabe validate --visibility=Public clean on the DESIGN,
the amended spike, and the PRD.
**Terminal state:** Proposed at docs/designs/DESIGN-codex-instance-root-skills.md,
handed back to the parent per parent-delegated-approval. No PR opened.

## Current Status
**Phase:** 6 - complete
**Last Updated:** 2026-08-23
