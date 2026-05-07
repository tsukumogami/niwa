# Plan Review: niwa-destroy

## Self-review checklist

- [x] **Atomic issues**: each issue has a clean test seam and can be completed in one focused session.
- [x] **No issue is too large**: no single issue spans more than one new file (or one logical change for `<<ISSUE:1>>` and `<<ISSUE:8>>`).
- [x] **Critical path is short**: 4 issues deep (`<<ISSUE:5>>` → `<<ISSUE:6>>` → `<<ISSUE:8>>` → `<<ISSUE:9>>`).
- [x] **Dependencies match the design**: the graph mirrors the design doc's Phase A–G dependency map (A doc-only ⊥, B+C+D parallel, E gathers, F follows).
- [x] **Complexity assignments match the project label vocabulary**: `simple` (docs/wrapper/prompt), `testable` (helpers/picker/feature tests), `critical` (the user-facing rewrite).
- [x] **No strawman issues**: `<<ISSUE:1>>` (docs) could have been folded into `<<ISSUE:8>>` but is kept separate so doc churn is reviewable independently and doesn't gate code progress.
- [x] **Single-pr mode is appropriate**: one developer, one branch, one PR; the issue list is a sequencing tool, not a delivery mechanism.

## Issues NOT created

`<<ISSUE:0>>`-style "Companion PRD-niwa-destroy.md" was considered but kept out of this plan (the design doc's Out-of-Scope section explicitly defers it to a follow-up PR if review wants it scoped out).

## Risks flagged for implementation

1. **`<<ISSUE:8>>` is critical** because it's the user-facing surface and integrates 6 dependencies. The implementer should land it incrementally — first the dispatcher with stubs, then wire each runner — to keep code review tractable.
2. **`<<ISSUE:5>>` → `<<ISSUE:6>>` → `<<ISSUE:8>>` ordering is load-bearing**: the scan must produce stable types before `DestroyWorkspace` consumes them, and both must be done before the command rewrite.
3. **`<<ISSUE:9>>` depends on the wrapper change in `<<ISSUE:2>>`** (the `@critical` "destroy from inside lands at workspace root" scenario only works with the wrapper extended). Order them correctly in the implementation sequence.

## Approved

This plan is ready for the PLAN doc.
