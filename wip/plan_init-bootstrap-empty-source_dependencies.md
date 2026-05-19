# Plan Dependencies: init-bootstrap-empty-source

## Summary

- Total issues: 5
- Issues with no dependencies: 1 (Issue 1)
- Maximum dependency depth: 5 (linear chain: 1 → 2 → 4 → 5, with 3 in
  parallel-after-1)

## Dependency Graph

```
Issue 1 (no deps)
├── Issue 2 (blocked by 1)
└── Issue 3 (blocked by 1)
        Issue 4 (blocked by 1, 2, 3)
            Issue 5 (blocked by 4)
```

## Issue Dependencies

| Issue | Title | Blocked By | Blocks |
|-------|-------|------------|--------|
| 1 | feat(github): typed StatusError + fifth-wrap fix + classifier helper | None | 2, 3, 4 |
| 2 | feat(init): --bootstrap flag surface + TTY prompt + classifier dispatch | 1 | 4 |
| 3 | feat(workspace): ScaffoldFromSource + GetRepo + GitInvoker seam | 1 | 4 |
| 4 | feat(workspace): RunBootstrap orchestrator + session BranchPrefix | 1, 2, 3 | 5 |
| 5 | test+docs: end-to-end Gherkin matrix + docs/guides | 4 | None |

## Parallelization Opportunities

- **Immediate start**: Issue 1 (no dependencies).
- **After Issue 1**: Issues 2 and 3 can be worked in parallel. They
  touch different packages:
  - Issue 2 modifies `internal/cli/init.go` and adds
    `internal/cli/init_classifier.go`'s call sites.
  - Issue 3 adds `internal/github/client.go::GetRepo`,
    `internal/workspace/scaffold.go::ScaffoldFromSource`, and the
    `GitInvoker` / `BootstrapParams` partial in
    `internal/workspace/bootstrap.go`.
  Their only shared dependency is the typed `*github.StatusError`
  from Issue 1.
- **After Issues 2 and 3**: Issue 4 wires everything together
  (factors `handleCreateSession`, lands the full `RunBootstrap` body,
  replaces Issue 2's stub).
- **After Issue 4**: Issue 5 ships the end-to-end Gherkin matrix and
  docs.

In single-pr execution mode, these "parallelization opportunities"
map to natural commit boundaries within the single PR rather than
separate PRs. The branch builds linearly through Issue 1 → (2 || 3)
→ 4 → 5 with each issue as one or more commits.

## Critical Path

Issue 1 → Issue 2 → Issue 4 → Issue 5 (length 4) is one of two
equivalent longest chains. Issue 1 → Issue 3 → Issue 4 → Issue 5 is
the other (same length). Issues 2 and 3 are interchangeable in the
middle and can be committed in either order.

## Validation

- [x] No circular dependencies (DAG verified: 1→2, 1→3, 1→4, 2→4, 3→4, 4→5;
  topological order 1, 2, 3, 4, 5 or 1, 3, 2, 4, 5)
- [x] All blockers exist in the issue list
- [x] At least one issue has no dependencies (Issue 1)
- [x] Critical path length is reasonable (4 hops, 5 issues — fits a
  single PR comfortably)
