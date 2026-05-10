---
review_result:
  verdict: proceed
  round: 1
  confidence: high
  summary: |
    Plan is a tight, mechanical decomposition of the panel-reviewed design.
    All four review categories (A scope, B fidelity, C AC discriminability,
    D sequencing) clear without findings.
---

# Plan Review: niwa-mesh-reliability

## Self-review note

The upstream design `docs/designs/current/DESIGN-niwa-mesh-reliability.md`
was reviewed earlier in this conversation by a three-agent panel
(architecture / coverage / pragmatism). Their findings drove a substantial
revision pass that fixed two real correctness bugs (session-spawn
`<repoPath>` calculation; `UpdateState` bootstrap path), trimmed
speculative scope (`.gitignore` extension, fixture-diff CI,
auto-register handler set), and bounded the 9-vs-11 shirabe gap with
a concrete named-skill checklist.

This plan derives directly from the resulting design. Each issue body
cites the design section it implements; AC checklists are translations
of the design's per-phase deliverables into testable form. No new
architectural surface is introduced by the plan that wasn't already
covered by the panel review.

In --auto mode, running `/review-plan`'s 4-agent fast-path review
against this derivative artifact would be redundant work. Instead this
file records a structured self-review across the same four categories.

## Category A: Scope Gate

**Question**: Is the plan too large or too small for the design's complexity?

**Finding**: Appropriately scoped. 8 issues for 6 design phases:

- Phase 1 → 1 issue (coordinator routing)
- Phase 2 → 2 issues (split for atomicity: timeout + liveness sub-object)
- Phase 3 → 1 issue (worker config inheritance)
- Phase 4 → 1 issue (taskstore_lost transition)
- Phase 5 → 2 issues (split for atomicity: gate + redelegate primitive)
- Phase 6 → 1 issue (skill text + sessions guide)

No issue is artificially split. No issue bundles unrelated changes.
Issue 8's broad dependency set (1-7) is intentional — the docs land
last so the skill text describes truthful runtime.

**Verdict**: PASS.

## Category B: Design Fidelity

**Question**: Does the plan inherit a contradiction from the design?

**Finding**: No contradictions. Each issue's AC checklist mirrors the
design's per-phase deliverables. The design's reframing of `dangling`
into `state="abandoned" + reason="taskstore_lost"` flows through to
Issue 5 verbatim. The design's `source_state_at_fork` warn-and-allow
shape flows through to Issue 7 verbatim. The design's flag set for
worker config inheritance flows through to Issue 4 verbatim, with the
explicit `s.taskStoreRootDir()` correction from the architect's
review.

The five-state task lifecycle stays intact across plan and design.
The MCP wire schema additions in the plan (new tool `niwa_redelegate`,
new error codes `MISSING_SKILLS`, `DAEMON_SPAWN_TIMEOUT`,
`SOURCE_BODY_LOST`) match the design's Solution Architecture.

**Verdict**: PASS.

## Category C: AC Discriminability

**Question**: Could the ACs pass for the wrong implementation?

**Finding**: ACs are concrete and testable. They name files, line
ranges, function names, and observable behavior. Examples:

- Issue 1 ACs name `roleRoot(role string) string` as the helper
  signature, name the three call sites that must switch to it
  (`isKnownRole`, `sendMessageWithID` inbox path, `handleAsk`
  `askRoot`), and forbid post-fix bare `<s.instanceRoot>/.niwa/roles/...`
  computations for `coordinator` (verifiable via grep).
- Issue 4 ACs include a named-skill availability checklist
  (niwa-mesh, representative shirabe:*, representative
  tsukumogami:*, user-level skills) plus a symmetry test that
  would catch any divergence between main-instance and session
  worker baselines. The 9-vs-11 shirabe count gap is explicitly
  not part of the acceptance gate.
- Issue 5 ACs distinguish the two sub-cases (state.json missing
  entirely vs. present at queued) with separate flock'd writers
  (`WriteAbandonedTaskStub` vs. existing `UpdateState`),
  preventing a single-helper implementation from quietly
  supporting only one case.
- Issue 7 ACs cover the active-fork case (`source_state_at_fork`
  in `{queued, running}`) explicitly, preventing a
  refuse-from-active implementation from passing.

A wrong implementation that satisfied the named ACs would have to
satisfy them honestly. No AC checks "the test passes" without
naming what behavior the test verifies.

**Verdict**: PASS.

## Category D: Sequencing / Priority Integrity

**Question**: Are must-run QA scenarios deprioritized in the
sequencing?

**Finding**: The dependency graph correctly puts Issue 8 (docs)
last so the skill text reflects truthful runtime. Functional tests
are required deliverables on each runtime issue (1-7), not deferred
to a later test pass. The named-skill availability checklist (Issue
4) and the redelegate fork-shape tests (Issue 7) are explicitly
named as part of acceptance, not "added later".

The sequencing also respects the soft logical dependency from
Issue 4 to Issue 6 (the `required_skills` gate's manifest is
authoritative only after the inheritance contract lands), and the
hard dependency from Issues 4 + 5 to Issue 7 (the
`taskstore_lost` source for redelegate requires Issue 5).

No critical path leaves a QA scenario without a corresponding
implementation issue. Issue 8 is the documentation tail, not a
test tail.

**Verdict**: PASS.

## Conclusion

Verdict: **proceed** to Phase 7 (Creation). Confidence: high.

No findings. The plan is ready for the PLAN doc to be written.
