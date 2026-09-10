# Plan Dependencies: DESIGN-dispatch-sendmessage-approval

## Summary
- Total issues: 8
- Issues with no dependencies: 3 (1, 2, 3)
- Maximum dependency depth: 4 (3 -> 4 -> 6 -> 7)

## Dependency Graph

```
Issue 1 (review deny, no deps)
Issue 2 (config key, no deps)
Issue 3 (settings rendering + prompt separator, no deps)
└── Issue 4 (flag, capability row, delivery; blocked by 2, 3)
    ├── Issue 5 (record + niwa list; blocked by 4)
    ├── Issue 6 (explanation + marker; blocked by 4)
    ├── Issue 7 (functional tests; blocked by 1, 4, 5, 6)
    └── Issue 8 (guide + manual check; blocked by 1, 4, 5, 6)
```

## Issue Dependencies

| Issue | Title | Blocked By | Blocks |
|-------|-------|------------|--------|
| 1 | feat(watch): deny session-reaching tools in review sessions | None | 7, 8 |
| 2 | feat(config): add the accept_session_messages_on_dispatch machine key | None | 4 |
| 3 | refactor(dispatch): render launch settings from one map and separate the Claude prompt | None | 4 |
| 4 | feat(dispatch): add --accept-session-messages with its capability row and audit lines | 2, 3 | 5, 6, 7, 8 |
| 5 | feat(list): record and report accepts_session_messages | 4 | 7, 8 |
| 6 | feat(dispatch): show the one-time explanation and remember it beside config.toml | 4 | 7, 8 |
| 7 | test(functional): cover dispatch session-message acceptance end to end | 1, 4, 5, 6 | None |
| 8 | docs(guide): document session-message acceptance and record the manual check | 1, 4, 5, 6 | None |

## Parallelization Opportunities

- **Immediate start**: Issues 1, 2, 3
- **After Issues 2 and 3**: Issue 4
- **After Issue 4**: Issues 5 and 6 in parallel (they touch different files apart from the step 11 literal and step 14 call site in `dispatch.go`, which are separate regions)
- **After Issues 1, 4, 5, 6**: Issues 7 and 8 in parallel

## Critical Path

Issue 3 -> Issue 4 -> Issue 6 -> Issue 7

Length: 4 issues

## Validation

- [x] No circular dependencies
- [x] All blockers exist in issue list
- [x] At least one issue has no dependencies
- [x] Critical path length is reasonable
- [x] Every outline's declared dependencies match this table (checked against the returned outline files)
