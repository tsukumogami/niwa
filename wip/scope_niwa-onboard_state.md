```yaml
topic: niwa-onboard
chain_started: 2026-07-12T02:58:23Z
last_updated: 2026-07-12T04:52:36Z
execution_mode: auto
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
  - design
child_snapshots:
  brief:
    status: Accepted
    content_hash: 9f853464330f7d11922d4080b5f1dff3f42c476a
    captured_at: 2026-07-12T03:33:48Z
  prd:
    status: In Progress
    content_hash: 7659e36116846e33a05dff0157eb535dbf5f6047
    captured_at: 2026-07-12T04:02:10Z
  design:
    status: Accepted
    content_hash: e5f2ac4505b20ffcf5a085793ab8b5a3304c1f5c
    captured_at: 2026-07-12T04:52:36Z
visibility: Public
plan_execution_mode: single-pr
parent_orchestration:
  invoking_child: plan
  suppress_status_aware_prompt: true
  rationale: fresh-chain
```

<!-- worktree sync note (2026-07-12T04:05Z): upstream main gained 022d7cb
(feat(watch): real HOME + single watch_sandbox switch, #198). Impact: none —
watch subsystem, no path/symbol/contract this chain depends on. Synced via
merge commit d9eb042 rather than rebase (force-push unavailable in this
session). -->
