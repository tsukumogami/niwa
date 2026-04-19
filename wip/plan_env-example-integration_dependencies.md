# Plan Dependencies: DESIGN-env-example-integration

## Summary
- Total issues: 6
- Issues with no dependencies: 3 (Issues 1, 2, 3)
- Maximum dependency depth: 3

## Dependency Graph

```
Issue 1: feat(config): add read_env_example opt-out flags     (no deps)
Issue 2: feat(workspace): implement parseDotEnvExample        (no deps)
Issue 3: feat(workspace): implement classifyEnvValue          (no deps)
└── Issue 4: feat(workspace): integrate .env.example pre-pass (blocked by 1, 2, 3)
    ├── Issue 5: feat(workspace): public-remote guardrail      (blocked by 4)
    └── Issue 6: feat(workspace): SourceKindEnvExample         (blocked by 4)
```

## Issue Dependencies

| Issue | Title | Blocked By | Blocks |
|-------|-------|------------|--------|
| 1 | feat(config): add read_env_example opt-out flags to workspace config | None | 4 |
| 2 | feat(workspace): implement parseDotEnvExample for Node-style .env.example syntax | None | 4 |
| 3 | feat(workspace): implement classifyEnvValue for probable-secret detection | None | 4 |
| 4 | feat(workspace): integrate .env.example pre-pass into EnvMaterializer | 1, 2, 3 | 5, 6 |
| 5 | feat(workspace): add per-repo public-remote guardrail for .env.example secrets | 4 | None |
| 6 | feat(workspace): add SourceKindEnvExample and verbose source attribution | 4 | None |

## Parallelization Opportunities

- **Immediate start**: Issues 1, 2, 3 — all independent
- **After Issues 1 + 2 + 3**: Issue 4
- **After Issue 4**: Issues 5 and 6 — can be worked in parallel

## Critical Path

Issue 1 (or 2 or 3) → Issue 4 → Issue 5 (or 6)

Length: 3 issues

## Validation

- [x] No circular dependencies
- [x] All blockers exist in the issue list
- [x] At least one issue has no dependencies (Issues 1, 2, 3)
- [x] Critical path length is reasonable (3 issues)
