# /plan Decisions: dispatch-sendmessage-approval

| id | artifact | tier | status | question |
|---|---|---|---|---|
| design-auto-transition | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 1 | confirmed | Transition the design Accepted -> Planned under the parent sentinel? |
| strategy | wip/plan_dispatch-sendmessage-approval_decomposition.md | 2 | confirmed | Walking skeleton or horizontal? |
| phase6-split | wip/plan_dispatch-sendmessage-approval_decomposition.md | 1 | confirmed | Split the design's Phase 6 into a test unit and a docs unit? |
| value-guard | wip/plan_dispatch-sendmessage-approval_decomposition.md | 2 | confirmed | Does each unit pass the value test? |
| execution-mode | wip/plan_dispatch-sendmessage-approval_decomposition.md | 2 | confirmed | single-pr or multi-pr? |
| loopback-round-1 | wip/plan_dispatch-sendmessage-approval_review_loopback.md | 2 | confirmed | How to act on the round 1 loop-back verdict? |
| selective-regeneration | wip/plan_dispatch-sendmessage-approval_review_loopback.md | 2 | assumed | Regenerate every outline, or only the affected ones? |
| round2-scope | wip/research/review-plan_dispatch-sendmessage-approval_round2_catC.md | 2 | assumed | Re-run all four review categories in round 2, or only Category C? |
| plan-status | docs/plans/PLAN-dispatch-sendmessage-approval.md | 2 | confirmed | Author the single-pr PLAN at Draft or Active? |

<!-- decision:start id="plan-status" status="confirmed" -->
**Decision:** Author the PLAN at `status: Active` with `tracking_level: none`.
The scope exit-finalization reference says a single-pr PLAN is Draft, but the
`/plan` skill, which owns the PLAN lifecycle, says an activation that files no
GitHub issues auto-fires as authoring completes, and that a committed PLAN at
`Draft` is a violation the chain-aware `--lifecycle` check fails (L01). The
tracking level resolves to `none`: niwa's CLAUDE.md has no `## Tracking Level:`
header, and the single-pr default is `none`.
<!-- decision:end -->

<!-- decision:start id="round2-scope" status="assumed" -->
**Decision:** Round 2 re-runs only Category C (AC discriminability), carrying
forward round 1's empty findings for A (scope), B (design fidelity) and D
(sequencing). **Why assumed:** every round 1 finding was Category C; the rework
changed acceptance criteria in issues 1, 4, 7 and 8 and nothing else; the issue
count, titles, types, and dependency edges are unchanged (verified against each
outline's Dependencies section), so the inputs A, B and D judged did not move.
This follows the inline-substitute-review fallback's intent for `/plan` Phase 6
re-runs under a parent chain.
<!-- decision:end -->

<!-- decision:start id="loopback-round-1" status="confirmed" -->
**Decision:** Round 1 review returned loop-back at Phase 4 with four Category C
findings (issues 1, 4, 7, 8); A, B and D found nothing critical. Added the
design-fidelity reviewer's note about the missing four-parallel-dispatch
scenario as a fourth C finding, since it is an uncovered PRD acceptance
criterion. Incremented `review_rounds` to 1 and regenerated with framed
correction hints.
<!-- decision:end -->

<!-- decision:start id="selective-regeneration" status="assumed" -->
**Decision:** Deleted and regenerated only issues 1, 4, 7 and 8, keeping 2, 3, 5
and 6. **Why assumed:** the loop-back deletion list for target 4 names every
issue body, while the schema says `affected_issue_ids` is what `/plan` uses to
know which bodies to regenerate. No finding touched 2, 3, 5 or 6, and
regenerating them would only risk drift. Regeneration agents revise the prior
committed bodies rather than starting over.
<!-- decision:end -->

<!-- decision:start id="design-auto-transition" status="confirmed" -->
**Decision:** Ran `shirabe transition <design> Planned`. The `/scope` state file
carries `parent_orchestration.invoking_child: plan` and the design was Accepted,
which is the sentinel-gated auto-transition in Phase 1.1.
<!-- decision:end -->

<!-- decision:start id="strategy" status="confirmed" -->
**Decision:** Horizontal. The design matches walking-skeleton signals (new
feature, several layers, 3+ units) and horizontal signals (clear, stable
interfaces; a fixed commit order) about equally. Tiebreaker: the design already
names every interface and orders the commits, and the thinnest end-to-end path
(flag to launch argv) is unit 4 itself, so a stub skeleton would duplicate it.
<!-- decision:end -->

<!-- decision:start id="phase6-split" status="confirmed" -->
**Decision:** Split the design's Phase 6 into unit 7 (functional scenarios and
the watch-site unit test, type code) and unit 8 (guide, index, help and
completion check, manual delivery check, type docs). They touch disjoint files,
route to different review paths, and the guide's version line depends on the
manual check rather than on the scenarios.
<!-- decision:end -->
