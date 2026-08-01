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
child_snapshots:
  brief:
    status: Draft
    content_hash: 52a222f391ad0377b4d062f970875915a6c48c8f
    captured_at: 2026-08-01T16:31:10Z
worktree_rebases:
  - phase: brief
    upstream_commits: []
    impact: none
    rebased_at: 2026-08-01T16:23:05Z
chain_paused:
  after: brief
  reason: author-requested-review-gate
  detail: >-
    Author asked to stop once the BRIEF was in a PR for review. The BRIEF is at
    status Draft; downstream children require an Accepted or Done upstream, so
    the pause is also the structurally correct stopping point. Resume by
    accepting the BRIEF and re-invoking /scope dispatch-paste-prompt.
  pull_request: https://github.com/tsukumogami/niwa/pull/224
```
