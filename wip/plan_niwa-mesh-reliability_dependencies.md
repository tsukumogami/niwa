# Plan Dependencies: niwa-mesh-reliability

## Summary

- Total issues: 8
- Issues with no dependencies: 5 (issues 1, 2, 3, 4, 5)
- Maximum dependency depth: 3 (issue 4 → 7 → 8, or issue 5 → 7 → 8)

## Dependency Graph

```
Issue 1 (mesh routing repair, no deps)
Issue 2 (typed spawn-timeout error, no deps)
Issue 3 (daemon liveness sub-object, no deps)
Issue 4 (worker config inheritance, no deps)
├── Issue 6 (required_skills gate, blocked by 4)
└── Issue 7 (niwa_redelegate, blocked by 4 and 5)
Issue 5 (taskstore_lost transition, no deps)
└── Issue 7 (niwa_redelegate, blocked by 4 and 5)

Issue 8 (skill text + sessions guide, blocked by 1, 2, 3, 4, 5, 6, 7)
```

## Issue Dependencies

| Issue | Title | Blocked By | Blocks |
|-------|-------|------------|--------|
| 1 | fix(mesh): route coordinator-targeted role lookups to main instance | None | 8 |
| 2 | feat(daemon): return typed spawn-timeout error from EnsureDaemonRunning | None | 8 |
| 3 | feat(mesh): expose daemon liveness on niwa_list_sessions | None | 8 |
| 4 | feat(mesh): inherit workspace Claude config in spawned workers | None | 6, 7, 8 |
| 5 | feat(mesh): transition taskstore-lost tasks to abandoned | None | 7, 8 |
| 6 | feat(mesh): add required_skills queue-time gate | 4 | 8 |
| 7 | feat(mesh): add niwa_redelegate primitive | 4, 5 | 8 |
| 8 | docs(mesh): rewrite niwa-mesh skill and sessions guide | 1, 2, 3, 4, 5, 6, 7 | None |

## Parallelization Opportunities

- **Immediate start (5 issues in parallel)**: Issues 1, 2, 3, 4, 5 — all independent of each other.
- **After Issue 4**: Issue 6 can start.
- **After Issues 4 and 5**: Issue 7 can start.
- **After Issues 1-7 all complete**: Issue 8 (docs) lands.

In single-pr mode this still matters: even though all eight issues land in one branch and one PR, the implementation can pick up any of issues 1-5 first, and issue 8 is held until the runtime issues are done so the skill text describes truthful behavior.

## Critical Path

There are two critical paths of equal length:

1. Issue 4 → Issue 6 → Issue 8 (3 issues)
2. Issue 4 → Issue 7 → Issue 8 (3 issues)
3. Issue 5 → Issue 7 → Issue 8 (3 issues)

Length: 3 issues. Minimum implementation time-to-merge for the full PR is dominated by these chains. Issues 1, 2, 3 can land alongside but do not extend the critical path.

## Validation

- [x] No circular dependencies (verified by topological sort: {1,2,3,4,5} → {6} (after 4) → {7} (after 4+5) → {8} (after all)).
- [x] All blockers exist in issue list (every "Blocked by" entry references an issue 1-7).
- [x] At least one issue has no dependencies (5 of them: 1, 2, 3, 4, 5).
- [x] Critical path length is reasonable (3 issues on the longest chain).
- [x] No "first issue" bottleneck — five issues can start immediately.

## Sequencing notes for single-PR implementation

Recommended commit-by-commit order within the branch:

1. Issue 1 — coordinator routing repair (small, well-bounded; sets up the role-redirect helper that Issue 7's docs reference).
2. Issue 2 — typed daemon-spawn timeout (independent, small).
3. Issue 3 — daemon liveness sub-object (independent, small).
4. Issue 4 — worker config inheritance (medium, the core architectural shift).
5. Issue 5 — taskstore_lost transition (independent of 1-4).
6. Issue 6 — required_skills gate (after 4 lands so the manifest-source-of-truth is real).
7. Issue 7 — niwa_redelegate (after 4 + 5 land).
8. Issue 8 — skill text and sessions guide (after all runtime issues land so the docs describe truthful behavior).

Issues 1, 2, 3 in particular are so small and independent that they can be batched into a single commit each without losing reviewability. Issues 4-7 are larger and warrant individual commits.
