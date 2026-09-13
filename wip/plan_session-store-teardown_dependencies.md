# Plan Dependencies: DESIGN-session-store-teardown

## Summary
- Total issues: 7
- Issues with no dependencies: 2 (Issue 1, Issue 5)
- Maximum dependency depth: 5 (1 -> 2 -> 3 -> 6 -> 7)

## Dependency Graph

```
Issue 1 (test seam + failing tests, no deps)
└── Issue 2 (configdir lock, layout, recovery)
    ├── Issue 3 (refresh, mapping store, watch writes ordered)
    │   ├── Issue 6 (destroy resolver)   [also blocked by Issue 5]
    │   └── Issue 7 (functional + docs)  [also blocked by Issue 6]
    └── Issue 4 (atomic locked SaveState)

Issue 5 (root refusal, no deps)
└── Issue 6
```

## Issue Dependencies

| Issue | Title | Blocked By | Blocks |
|-------|-------|------------|--------|
| 1 | test(workspace): add testhook seam and failing race tests | None | 2 |
| 2 | feat(configdir): per-config-dir lock, layout and recovery | 1 | 3, 4 |
| 3 | fix(workspace): order refresh, mapping store and watch writes | 2 | 6, 7 |
| 4 | fix(workspace): atomic locked SaveState with skip-after-failed-read | 2 | None |
| 5 | fix(cli): refuse worktree subcommands at a multi-instance root | None | 6 |
| 6 | feat(cli): resolve worktree destroy by session id or handle | 3, 5 | 7 |
| 7 | docs(worktree): functional coverage and guide updates | 3, 6 | None |

## Parallelization Opportunities

- **Immediate start**: Issue 1 and Issue 5. Issue 5 touches only the CLI's
  instance resolution and needs nothing from the lock.
- **After Issue 2**: Issues 3 and 4 are independent of each other; 4 touches
  `state.go` and `apply.go` while 3 touches the snapshot writer, the mapping
  store, watch and discovery.
- **After Issues 3 and 5**: Issue 6.
- **After Issue 6**: Issue 7.

## Critical Path

Issue 1 -> Issue 2 -> Issue 3 -> Issue 6 -> Issue 7

Length: 5 issues.

## Notes on ordering

Issue 1 lands red on purpose: its forced tests fail against the parent commit,
which is the evidence the PRD's verification requirements ask for. The tests it
adds turn green in the issue that fixes what each one holds: the mapping,
watch, refresh, reaper and kill tests in Issue 3, the root-state tests in
Issue 4, and the teardown-read test in Issue 6, which is also where that test
is added because the resolver does not exist before it.

## Validation

- [x] No circular dependencies
- [x] All blockers exist in the issue list
- [x] Two issues have no dependencies
- [x] Critical path length is reasonable (5 of 7)
