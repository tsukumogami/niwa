# Plan Decomposition: niwa-destroy

## Strategy

**Horizontal.** Layered helpers first, then command rewrite, then functional tests. Doc surface lands in parallel.

## Execution mode

**single-pr** — implementation lands on `docs/niwa-destroy-rework` (PR #106) as one squash-merged commit. No GitHub issues or milestone created (single-pr convention).

## Issues

| ID | Title | Complexity | Type | Phase (design) |
|---|---|---|---|---|
| <<ISSUE:1>> | Amend PRDs and design docs for new destroy semantics | simple | docs | A |
| <<ISSUE:2>> | Extend shell wrapper to support `niwa destroy` cd | simple | code | B |
| <<ISSUE:3>> | Copy tsuku TUI picker into `internal/tui/` | testable | code | C |
| <<ISSUE:4>> | Add `ClassifyCwd` helper | testable | code | D.1 |
| <<ISSUE:5>> | Add non-pushed-work scan (`scan.go`) | testable | code | D.2 |
| <<ISSUE:6>> | Add `DestroyWorkspace` helper | testable | code | D.3 |
| <<ISSUE:7>> | Add `prompt.go` helper (TTY check + typed confirmation) | simple | code | D.4 |
| <<ISSUE:8>> | Rewrite `runDestroy` with contextual dispatch | critical | code | E |
| <<ISSUE:9>> | Add `@critical` and standard Gherkin scenarios | testable | test | F |

9 issues total. None are walking-skeleton issues.

## Atomic-issue check

- Each issue is independently completable in one focused session.
- Each issue has a clean test seam (unit tests local to the helper, except `<<ISSUE:1>>` which is doc-only and `<<ISSUE:9>>` which is functional-test-only).
- `<<ISSUE:8>>` is the integration point — it depends on `<<ISSUE:2,3,4,5,6,7>>`.
- `<<ISSUE:9>>` depends on `<<ISSUE:8>>` (the new behavior must exist to test).
- `<<ISSUE:1>>` is independent — doc amendments don't gate or block any code issue.

No further splitting warranted. No issues need merging.
