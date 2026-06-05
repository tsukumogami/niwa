---
topic: niwa-mesh-removal
chain_started: 2026-06-05T22:13:47Z
last_updated: 2026-06-05T22:14:30Z
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
    content_hash: 4e182658cd72be5e4f7644546e26fa0592f3c39f
    captured_at: 2026-06-05T22:18:29Z
  prd:
    status: Accepted
    content_hash: dca42e6e44983c8f73a2be789da34300362de6ec
    captured_at: 2026-06-05T22:20:37Z
  design:
    status: Accepted
    content_hash: 63d9f1e8f1f8b1c188aa549cf0abfccfc3b24eda
    captured_at: 2026-06-05T22:28:28Z
visibility: Public
worktree_rebases:
  - phase: brief
    upstream_commits: []
    impact: none
    rebased_at: 2026-06-05T22:15:00Z
  - phase: prd
    upstream_commits: []
    impact: none
    rebased_at: 2026-06-05T22:19:00Z
  - phase: design
    upstream_commits: []
    impact: none
    rebased_at: 2026-06-05T22:22:00Z
---

# /scope state — niwa-mesh-removal

Tactical chain (BRIEF -> PRD -> DESIGN -> PLAN) for removing niwa's pre-pivot
agent-facing mesh while preserving worktree creation. Remove-first reframe;
substantive sequencing already merged in the toolkit roadmap (do not re-decide).

Brief context source: `wip/scope_niwa-mesh-removal_brief-context.md` (this worktree).
Vision-side tracking worktree: `.niwa/worktrees/vision-0c91248d` (separate PR).
