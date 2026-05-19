---
design_doc: docs/designs/DESIGN-init-bootstrap-empty-source.md
input_type: design
decomposition_strategy: horizontal
strategy_rationale: "The design's Implementation Approach is itself a layered five-phase build (typed-error foundation → flag surface → scaffold + GitInvoker → orchestrator + session BranchPrefix → end-to-end coverage). Each phase composes on the previous one with stable interfaces between them. Walking-skeleton would force inventing a placeholder bootstrap path before the typed-error infrastructure exists, which the design explicitly avoids. Horizontal matches the design and produces five testable issues."
confirmed_by_user: true
issue_count: 5
execution_mode: single-pr
---

# Plan Decomposition: init-bootstrap-empty-source

## Strategy: Horizontal

Five issues, one per design phase. Each issue corresponds to a
commit-boundary in the single PR and ends with a CI-green,
meaningful user-visible state.

Sequential dependency chain follows the design's Implementation
Approach: Issue 1 → Issue 2 → Issue 3 → Issue 4 → Issue 5. There is
no parallelization opportunity between issues 2 and 3 (unlike the
prior W1 plan): Phase 3's `BootstrapParams` struct in the new
`bootstrap.go` is referenced by Phase 4 directly, and Phase 4 also
needs the flag surface from Phase 2. The strict linear ordering
makes the single-pr commit layout clean.

In single-pr execution mode, these five issues collapse into one PR
on the existing `docs/init-bootstrap-empty-source` branch.

## Issue Outlines

### Issue 1: feat(github): typed StatusError + fifth-wrap fix + classifier helper

- **Type**: standard
- **Complexity**: testable
- **Goal**: Build the typed-error infrastructure (`*github.StatusError`,
  fifth-wrap fix at `snapshotwriter.go:503`, classifier helper) without
  changing user-visible behavior.
- **Section**: Design Implementation Approach Phase 1
- **Milestone**: Init Bootstrap from Empty Source
- **Dependencies**: None

### Issue 2: feat(init): --bootstrap flag surface + TTY prompt + classifier dispatch

- **Type**: standard
- **Complexity**: testable
- **Goal**: Wire `--bootstrap` / `--no-bootstrap` flags with R25 mutual
  exclusion, R2 name derivation, R13 TTY-prompt dispatch, and activate
  the classifier from `runInit`. Bootstrap dispatch is a stub.
- **Section**: Design Implementation Approach Phase 2
- **Milestone**: Init Bootstrap from Empty Source
- **Dependencies**: Issue 1

### Issue 3: feat(workspace): ScaffoldFromSource + GetRepo + GitInvoker seam

- **Type**: standard
- **Complexity**: testable
- **Goal**: Add `ScaffoldFromSource` (PRD Appendix A byte-for-byte),
  `(*github.APIClient).GetRepo`, `GitInvoker` interface + `stdGitInvoker`,
  and `BootstrapParams` struct. All unit-tested, none called yet.
- **Section**: Design Implementation Approach Phase 3
- **Milestone**: Init Bootstrap from Empty Source
- **Dependencies**: Issue 1 (for `*github.StatusError` consumed by GetRepo)

### Issue 4: feat(workspace): RunBootstrap orchestrator + session BranchPrefix

- **Type**: standard
- **Complexity**: testable
- **Goal**: Add `BranchName` to `SessionLifecycleState`, factor
  `handleCreateSession` into `CreateSession(ctx, CreateSessionParams)`
  with `BranchPrefix` + `GitInvoker`, implement full `RunBootstrap`
  body with all four cleanup-defer layers, replace Issue 2's stub
  with the real call, emit R19 success block + R20 landing-path.
- **Section**: Design Implementation Approach Phase 4
- **Milestone**: Init Bootstrap from Empty Source
- **Dependencies**: Issue 1, Issue 2, Issue 3

### Issue 5: test+docs: end-to-end Gherkin + unit tests + docs/guides

- **Type**: standard
- **Complexity**: testable
- **Goal**: Land the full PRD Acceptance Criteria matrix as `@critical`
  Gherkin scenarios + unit tests for test-seam ACs (argv injection,
  classifier ordering table, cleanup defers, no-secret-on-disk).
  Update `docs/guides/` with the bootstrap flow; optional README
  mention.
- **Section**: Design Implementation Approach Phase 5
- **Milestone**: Init Bootstrap from Empty Source
- **Dependencies**: Issue 4
