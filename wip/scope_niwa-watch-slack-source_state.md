---
topic: niwa-watch-slack-source
chain_started: 2026-08-01T21:18:41Z
last_updated: 2026-08-01T21:19:17Z
phase_pointer: phase-2
visibility: Public
exit: UNSET
exit_artifacts: []
planned_chain:
  - brief
  - prd
  - design
  - plan
chain_skipped: []
chain_ran: []
worktree_rebases:
  - onto: origin/main
    commits: 2
    impact: informational
    note: >-
      niwa#226 derives the dispatch prompt cap from MAX_ARG_STRLEN
      (maxArgStringBytes = 32*4096-1, minus a keep-alive reserve). Sharpens
      driver D9 with a concrete number and strengthens Decision 4's rejection
      of prompt-inlining; no decision changes. Fold the citation in when
      /design revises the DESIGN.
parent_orchestration:
  parent: scope
  topic: niwa-watch-slack-source
  child: brief
  invoked_at: 2026-08-01T21:19:41Z
child_snapshots:
  design:
    status: Proposed
    content_hash: 70c9f55e919d2c41aeed40862b86c9c654be920e
    captured_at: 2026-08-01T21:19:17Z
---

# /scope state: niwa-watch-slack-source

Gate verdicts (Phase 1):
- brief: fires (R4 EITHER-signal -- no BRIEF at canonical path; framing has not
  shifted, it was never written at this altitude)
- prd: fires (R5 Mandatory-with-auto-skip -- no PRD at canonical path)
- design: fires (R7 shape-dependent -- P1 fires: multiple implementation choices
  left open; P2 fires: new internal/slack package and guard-egress subcommand;
  P3 fires: architectural complexity warrants a DESIGN. Existing DESIGN is
  Proposed, not Accepted, so it is revised in place rather than protected.)
- plan: fires (ALWAYS)
