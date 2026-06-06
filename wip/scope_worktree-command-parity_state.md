---
topic: worktree-command-parity
chain_started: 2026-06-06T16:13:38Z
last_updated: 2026-06-06T16:14:30Z
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
    content_hash: 7adfe2e005a30db08f887b2c423cff64c6beaf51
    captured_at: 2026-06-06T16:29:18Z
  prd:
    status: Accepted
    content_hash: 1a6784a24de289d3b3157e4d915360e6b5fe95c0
    captured_at: 2026-06-06T16:30:44Z
  design:
    status: Accepted
    content_hash: c0b2aa3cbf8ca6302c8edf12ba1f58fcffd7e121
    captured_at: 2026-06-06T16:33:54Z
visibility: Public
worktree_rebases:
  - phase: brief
    upstream_commits: []
    impact: none
    rebased_at: 2026-06-06T16:15:00Z
  - phase: prd
    upstream_commits: []
    impact: none
    rebased_at: 2026-06-06T16:29:30Z
  - phase: design
    upstream_commits: []
    impact: none
    rebased_at: 2026-06-06T16:31:00Z
  - phase: plan
    upstream_commits: []
    impact: none
    rebased_at: 2026-06-06T16:34:00Z
parent_orchestration:
  invoking_child: plan
  suppress_status_aware_prompt: true
  rationale: fresh-chain
---

# /scope state — worktree-command-parity

Design a symmetric worktree-level command surface that mirrors the workspace-level
`niwa create|apply|destroy`. Bring `niwa worktree create` to parity with the
claude-context setup a repo gets from `niwa apply`, identify the missing worktree
verbs / customizations / hooks / templates, and avoid divergent code paths.

Built on branch impl/niwa-mesh-removal (the mesh removal that made `niwa worktree`
first-class). Terminal artifact target: DESIGN + PLAN (identify + design; not full
implementation in this branch).

Brief context: wip/scope_worktree-command-parity_brief-context.md
