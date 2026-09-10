# /plan Decisions: dispatch-sendmessage-approval

| id | artifact | tier | status | question |
|---|---|---|---|---|
| design-auto-transition | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 1 | confirmed | Transition the design Accepted -> Planned under the parent sentinel? |
| strategy | wip/plan_dispatch-sendmessage-approval_decomposition.md | 2 | confirmed | Walking skeleton or horizontal? |
| phase6-split | wip/plan_dispatch-sendmessage-approval_decomposition.md | 1 | confirmed | Split the design's Phase 6 into a test unit and a docs unit? |
| value-guard | wip/plan_dispatch-sendmessage-approval_decomposition.md | 2 | confirmed | Does each unit pass the value test? |
| execution-mode | wip/plan_dispatch-sendmessage-approval_decomposition.md | 2 | confirmed | single-pr or multi-pr? |

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
