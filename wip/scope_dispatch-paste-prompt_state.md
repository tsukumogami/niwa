```yaml
topic: dispatch-paste-prompt
chain_started: 2026-08-01T16:19:17Z
last_updated: 2026-08-01T16:31:10Z
phase_pointer: phase-2
exit: UNSET
exit_artifacts: []
visibility: Public
phase-1: empty-cold-start
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
    content_hash: 81298c04f82dc2f3cac009a7de2a2429b00a7d30
    captured_at: 2026-08-01T16:36:00Z
  prd:
    status: Accepted
    content_hash: 5cb516015715618cb5eabad17950b8eabde60d55
    captured_at: 2026-08-01T17:30:00Z
worktree_rebases:
  - phase: brief
    upstream_commits: []
    impact: none
    rebased_at: 2026-08-01T16:23:05Z
  - phase: prd
    upstream_commits: []
    impact: none
    rebased_at: 2026-08-01T16:36:00Z
  - phase: design
    upstream_commits: []
    impact: none
    rebased_at: 2026-08-01T17:31:00Z
parent_orchestration:
  invoking_child: design
  suppress_status_aware_prompt: true
  rationale: fresh-chain
pull_request: https://github.com/tsukumogami/niwa/pull/224
brief_open_questions_closed_by: docs/prds/PRD-dispatch-paste-prompt.md
```
