```yaml
topic: niwa-default-worktree
chain_started: 2026-06-20T20:50:35Z
last_updated: 2026-06-20T21:39:39Z
phase_pointer: phase-2
plan_execution_mode: single-pr
execution_mode: auto
exit: UNSET
exit_artifacts: []
planned_chain:
  - brief
  - prd
  - design
  - plan
chain_ran:
  - brief
  - prd
chain_skipped: []
visibility: Public
child_snapshots:
  brief:
    status: Accepted
    content_hash: c131e9e699515423422fdacce6bb0cd59e702294
    captured_at: 2026-06-20T21:30:27Z
  prd:
    status: Accepted
    content_hash: 54b7ac620eab1867cfd8f59df62285d872185721
    captured_at: 2026-06-20T21:39:39Z
parent_orchestration:
  active_child: design
  invoked_at: 2026-06-20T21:39:39Z
# Auto chain: brief (accepted) -> prd (accepted) -> design (now) -> plan.
# Stopping when the PLAN is ready for review.
```
