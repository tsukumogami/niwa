```yaml
topic: instance-dispatch
chain_started: 2026-06-25T03:44:15Z
last_updated: 2026-06-25T03:44:15Z
phase_pointer: phase-2
exit: UNSET
exit_artifacts: []
planned_chain:
  - brief
  - prd
  - design
  - plan
chain_skipped: []
chain_ran:
  - brief
  - prd
child_snapshots:
  brief:
    status: Accepted
    content_hash: 826ab7e139d67654a1da9fdbe7fed9d2851528a2
    captured_at: 2026-06-25T03:49:42Z
  prd:
    status: Accepted
    content_hash: b9a19de9b58f5c12d96d1029bfef8a19dab7d3e4
    captured_at: 2026-06-25T04:00:57Z
visibility: Public
plan_execution_mode: single-pr
execution_mode: auto
r6_predicates:
  p1: fires (open choices: id-capture mechanism, command name/interface, teardown model, slug strategy)
  p2: fires (new niwa CLI command surface)
  p3: fires (Complex: concurrency, failure modes, id-capture race)
worktree_rebases:
  - phase: brief
    upstream_commits: []
    impact: none
    rebased_at: 2026-06-25T03:44:15Z
  - phase: prd
    upstream_commits: []
    impact: none
    rebased_at: 2026-06-25T03:49:42Z
  - phase: design
    upstream_commits: []
    impact: none
    rebased_at: 2026-06-25T04:00:57Z
parent_orchestration:
  invoking_child: design
  suppress_status_aware_prompt: true
  rationale: fresh-chain
```
