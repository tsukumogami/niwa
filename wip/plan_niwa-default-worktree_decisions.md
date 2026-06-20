# PLAN Decisions (auto mode): niwa-default-worktree
- Decomposition: horizontal. Spike de-risked end-to-end integration; components have
  clear interfaces. 7 atomic issues following the design's Implementation Approach.
- Execution mode: single-pr (per chain directive). Value guard: the PR as a whole is
  the unit of value (a workspace where agent worktrees become niwa worktrees by
  default); no issue ships standalone user value -> one PR. Pass.
- Critical-shaped issues: #3 (from-hook: teardown data-safety + cwd security) and
  #5 (per-repo apply wiring). #7 is @critical functional coverage.
- Carried design risk: WorktreeRemove stdin schema unconfirmed -> AC in #3 to confirm
  the path-bearing field (fallback to cwd).
- Authored outlines directly + self-reviewed for completeness/sequencing rather than
  spawning Phase 4 decomposers / Phase 6 /review-plan, given the design was already
  juried twice and the user is the review gate on the PLAN itself.
