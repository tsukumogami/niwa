---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-repo-git-invisibility.md
milestone: "Repo Git Invisibility"
issue_count: 4
---

# PLAN: Repo Git Invisibility

## Status

Active

## Scope Summary

Make niwa self-guarantee its invisibility in managed repositories and the
worktrees it creates by writing a delimited `*.local*` + `.niwa/` block into each
repository's `.git/info/exclude`, and prove the guarantee with behavioral
functional tests. Lands as a single PR.

## Decomposition Strategy

**Horizontal.** The design has a clean seam: a self-contained helper
(`EnsureRepoExclude` + the pure `renderNiwaBlock`) with a well-defined
interface, two thin call sites that depend on it, and a functional test layer
that depends on the wiring being live. The components have stable interfaces and
minimal runtime coupling, so building the helper fully (with unit tests) before
wiring it in, then adding the end-to-end functional tests last, is the natural
order. A walking skeleton offers nothing here -- there is no integration risk to
surface early beyond the helper's own correctness, which its unit tests cover.

All four outlines land in one PR (single-pr): the feature delivers observable
value only once the helper exists, is wired into both apply and worktree create,
and is guarded by tests -- no intermediate slice is independently useful to a
user.

## Issue Outlines

### Issue 1: feat(workspace): add EnsureRepoExclude helper with unit tests

**Goal**: Add the `EnsureRepoExclude` helper and the pure `renderNiwaBlock`
function that write/refresh niwa's delimited managed block in a repository's
`.git/info/exclude`, with unit tests for the renderer.

**Acceptance Criteria**:
- [x] New `internal/workspace/exclude.go` defines `EnsureRepoExclude(tree string) error`
  and a pure `renderNiwaBlock(existing []byte) []byte`.
- [x] `renderNiwaBlock` writes a block delimited by `# >>> niwa managed >>>` and
  `# <<< niwa managed <<<` containing `*.local*` and `.niwa/`; replacing an
  existing block in place and appending a fresh one when absent.
- [x] `renderNiwaBlock` is idempotent (`render(render(x)) == render(x)`) and
  preserves all content outside the markers.
- [x] `EnsureRepoExclude` resolves the exclude file via
  `git -C <tree> rev-parse --git-common-dir` (resolving a relative result
  against `tree`), creates `<common-dir>/info/` if needed, and writes the file;
  it returns an error when the file cannot be written.
- [x] Unit tests in `internal/workspace/exclude_test.go` cover insert, replace,
  idempotency, and user-content preservation for `renderNiwaBlock`.
- [x] `go test ./internal/workspace/...` and `go vet` pass; no new lint issues introduced.

**Dependencies**: None

**Type**: code
**Files**: `internal/workspace/exclude.go`, `internal/workspace/exclude_test.go`

### Issue 2: feat(apply): record exclude coverage and drop the obsolete gitignore warning

**Goal**: Call `EnsureRepoExclude` per managed repository in the apply pipeline
(fail closed on error) and remove the now-contradictory managed-repo
`CheckGitignore` warning.

**Acceptance Criteria**:
- [x] In `internal/workspace/apply.go`, the `runPipeline` per-repo path (Step
  6.5, where `repoDir` is in scope) calls `EnsureRepoExclude(repoDir)` after the
  materializers run; a returned error aborts the apply with a clear message
  (PRD R9, fail closed).
- [x] The managed-repo `CheckGitignore` warning is removed at both call sites --
  `InstallRepoContent` (`content.go`) and `SettingsMaterializer.Materialize`
  (`materialize.go`) -- so no stale "add *.local* to .gitignore" guidance
  survives (PRD R6). `EnsureInstanceGitignore` (instance root) is untouched.
- [x] Existing unit tests in `internal/workspace` still pass; any test asserting
  the removed warning is updated to the new behavior.
- [x] `go test ./internal/workspace/...` and `go vet` pass; no new lint issues introduced.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/workspace/apply.go`, `internal/workspace/content.go`, `internal/workspace/materialize.go`

### Issue 3: feat(session): record exclude coverage on worktree create

**Goal**: Call `EnsureRepoExclude` for a new worktree after `scaffoldWorktreeNiwa`,
routing any error through the existing worktree cleanup path.

**Acceptance Criteria**:
- [x] In `internal/mcp/handlers_session.go`, `CreateSession` calls
  `EnsureRepoExclude(wtPath)` after `scaffoldWorktreeNiwa(wtPath, repo)` succeeds.
- [x] A returned error triggers the existing `cleanupWorktree()` path and is
  returned to the caller (fail closed; no half-created worktree left with a
  visible `.niwa/`).
- [x] Existing session/worktree unit tests still pass.
- [x] `go test ./internal/mcp/...` and `go vet` pass; no new lint issues introduced.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/mcp/handlers_session.go`

### Issue 4: test(functional): assert git invisibility for apply and worktree create

**Goal**: Add a behavioral functional feature and steps that run real niwa
operations against committed-clean fixtures and assert an empty
`git status --porcelain`, including a negative test proving the assertion has
teeth.

**Acceptance Criteria**:
- [x] New `test/functional/features/git-invisibility.feature` covers the apply
  path: apply invisibility (managed repo whose `.gitignore` lacks `*.local*` ->
  clean porcelain, with niwa-style output planted so the pass is non-vacuous,
  PRD R1/R7); idempotency (apply twice -> still clean, exclude block not
  duplicated); user-content preservation (a pre-existing line in
  `.git/info/exclude` survives); and a negative scenario where an uncovered file
  planted in the tree makes the porcelain assertion fail.
- [x] Worktree invisibility (PRD R3) is covered by
  `TestEnsureRepoExclude_CoversWorktree` in `internal/gitexclude`, which adds a
  real linked worktree, records coverage, and asserts a planted `.niwa/` is
  invisible while an uncovered file still shows -- a more reliable unit-level
  proof than a session/daemon-dependent functional scenario, with the
  `CreateSession` call site exercised by the `internal/mcp` package tests.
- [x] Step definitions reuse the existing `newLocalGitServer` fixture factory
  and `runGitInDir` helper; the assertion checks `git status --porcelain` is
  empty without enumerating niwa's filenames (PRD R7).
- [x] The scenarios run under `make test-functional` locally and in the CI
  `test.yml` workflow (they live in `test/functional/`, no new CI wiring).
- [x] `make test-functional` passes locally (the new scenarios pass; the one
  unrelated failure is a pre-existing infisical-login environment issue that
  also fails on the base commit and passes in CI); `go vet` passes.

**Dependencies**: Blocked by <<ISSUE:2>>, Blocked by <<ISSUE:3>>

**Type**: code
**Files**: `test/functional/features/git-invisibility.feature`, `test/functional/git_invisibility_steps_test.go`

## Dependency Graph

## Implementation Sequence

**Critical path:** Issue 1 -> (Issue 2 and Issue 3) -> Issue 4.

1. **Issue 1** (helper + unit tests) is the foundation; nothing else can wire in
   until `EnsureRepoExclude` exists.
2. **Issue 2** (apply wiring) and **Issue 3** (worktree wiring) both depend only
   on Issue 1 and are independent of each other -- they can be built in parallel.
3. **Issue 4** (functional tests) depends on both wirings being live, since its
   scenarios exercise apply and worktree create end to end. It lands last.

Since this is single-pr, all four outlines are implemented on the shared branch
and land together in one pull request.
