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
    status: Accepted
    content_hash: 81298c04f82dc2f3cac009a7de2a2429b00a7d30
    captured_at: 2026-08-01T16:36:00Z
worktree_rebases:
  - phase: brief
    upstream_commits: []
    impact: none
    rebased_at: 2026-08-01T16:23:05Z
  - phase: prd
    upstream_commits: []
    impact: none
    rebased_at: 2026-08-01T16:36:00Z
parent_orchestration:
  invoking_child: prd
  suppress_status_aware_prompt: true
  rationale: fresh-chain
pull_request: https://github.com/tsukumogami/niwa/pull/224
brief_open_questions_carried:
  - id: size-ceiling
    question: >-
      What the size ceiling should be, and how the developer is told. The PRD
      must state the ceiling as a requirement rather than inherit today's
      behavior: the current limit is known to be wrong in both its value and
      its coverage, so there is nothing sound to inherit.
  - id: unsupported-terminal
    question: >-
      What happens when the terminal cannot carry an interactive capture --
      refuse with guidance, or fall back to a cruder termination gesture.
  - id: detach-composition
    question: >-
      Whether the capture should be reachable when the developer has asked not
      to attach to the resulting session, or whether those compose at all. If
      they compose, the feature has two exit shapes to specify; if they do not,
      the command has a flag combination it must reject clearly.
  - id: non-interactive-invocation
    question: >-
      What a non-interactive invocation should do beyond not hanging, given
      that scripted piping is out of scope as a design driver but not as a
      caller.
brief_open_questions_closure_surface: >-
  The downstream PRD's Decisions and Trade-offs section. Removed from the BRIEF
  at the Draft -> Accepted transition per the brief/v1 lifecycle (Open Questions
  is Draft-only); carried here so the cleanup does not lose them.
```
