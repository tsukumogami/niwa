# Plan Dependencies: DESIGN-inert-defaultmode-key

## Summary
- Total issues: 5
- Issues with no dependencies: 1
- Maximum dependency depth: 4

## Dependency Graph

```
Issue 1 (no deps)
└── Issue 2 (blocked by 1)
    └── Issue 3 (blocked by 2)
        ├── Issue 4 (blocked by 3)
        └── Issue 5 (blocked by 3)
```

## Issue Dependencies

| Issue | Title | Blocked By | Blocks |
|-------|-------|------------|--------|
| 1 | feat(workspace): record the resolved permission posture in instance state | None | 2 |
| 2 | refactor(dispatch): derive --permission-mode from instance state and pass it explicitly to the argv builder | 1 | 3 |
| 3 | fix(workspace): stop writing permission modes Claude Code ignores or rejects | 2 | 4, 5 |
| 4 | test(functional): cover dispatch argv and the S1-S9 document matrix end to end | 3 | None |
| 5 | docs: correct the documents that describe the retired permission mechanism | 3 | None |

## Parallelization Opportunities

- **Immediate start**: Issue 1 (no dependencies)
- **After Issue 3**: Issues 4 and 5 can be worked in parallel

The chain from 1 to 3 is serial by design, not by accident: the dispatch
reader has to move onto instance state (Issue 2) before the materializer stops
writing the value it used to read (Issue 3). That order is what keeps the
existing `@critical` dispatch scenarios green at every commit. Issue 2 needs
the recorded field from Issue 1 to exist before it can read it.

## Critical Path

Issue 1 -> Issue 2 -> Issue 3 -> Issue 4

Length: 4 issues

## Validation

- [x] No circular dependencies
- [x] All blockers exist in issue list
- [x] At least one issue has no dependencies
- [x] Critical path length is reasonable (a single-pr plan; the chain is the
      design's mandated reader-before-producer order)
- [x] Dependencies confirmed against the five generated outlines: each
      outline's Dependencies section names exactly the blocker above
