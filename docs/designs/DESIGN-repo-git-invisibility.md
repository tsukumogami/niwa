---
upstream: docs/prds/PRD-repo-git-invisibility.md
status: Accepted
problem: |
  niwa writes files into the working trees of the git repositories it manages
  and the worktrees it creates, but its invisibility to those repos' git status
  depends on the user's committed .gitignore carrying *.local* and on every
  niwa-written path remembering the .local convention. Worktree creation
  scaffolds an untracked .niwa/ directory nothing ignores. niwa needs to
  guarantee invisibility itself, without modifying any committed file, and
  prove it with a test that catches future leaks.
decision: |
  After materializing into a managed repository or worktree, niwa writes a
  delimited managed block of ignore patterns (*.local* and .niwa/) into that
  repository's .git/info/exclude -- a per-repository, never-committed git file
  shared across all of the repo's worktrees. The write is idempotent (a marked
  begin/end block, rewritten in place), preserves any user content in the file,
  and fails the apply if the file cannot be written. A behavioral functional
  test runs niwa against committed-clean fixtures and asserts an empty
  git status --porcelain, with a negative test proving the assertion catches a
  planted leak.
rationale: |
  .git/info/exclude is the only ignore surface that is repository-scoped,
  never committed (satisfying "touch no tracked file"), and automatically
  shared with linked worktrees through the common git directory -- so one write
  covers the repo and every worktree of it. A fixed pattern block is chosen over
  deriving entries from the materialized file set because the file set is the
  same registry the stale-hook bug escaped; a broad *.local* + .niwa/ block
  needs no per-run recomputation, and the behavioral test (not the registry)
  is what guarantees completeness.
---

# DESIGN: Repo Git Invisibility

## Status

Accepted

## Context and Problem Statement

niwa's apply pipeline (`internal/workspace/apply.go`) materializes files into
each managed repository's working tree through four materializers (hooks,
settings, env, files) plus the content installers, and every managed-repo path
they emit today carries a `.local` infix. Worktree creation
(`internal/mcp/handlers_session.go`, `CreateSession` -> `scaffoldWorktreeNiwa`)
runs `git worktree add` and then writes a `.niwa/` directory into the new
worktree's working tree -- a path with no `.local` infix at all.

A git working tree's `git status` is computed against that repository's tracked
set plus the ignore rules git consults for that tree: the tree's own
`.gitignore` files, the repository-local `.git/info/exclude`, and the user's
global `core.excludesFile`. Crucially, git does not consult `.gitignore` files
above the working tree's top level, so the instance-root `.gitignore` that
`EnsureInstanceGitignore` maintains does not reach into a worktree checked out
under `.niwa/worktrees/`. The result is two concrete leaks: a managed repo whose
committed `.gitignore` lacks `*.local*` shows niwa's `.local` files as
untracked, and every fresh worktree shows `.niwa/` as untracked.

The technical problem is to give niwa a repository-scoped ignore surface it
controls, that is never committed (so recording coverage changes no tracked
file), that reaches both the primary working tree and all linked worktrees, and
that niwa keeps idempotent and non-destructive across repeated applies -- then
to guard the whole guarantee with a test that fails on any future leak without
enumerating niwa's filenames.

The source PRD is `docs/prds/PRD-repo-git-invisibility.md`.

## Decision Drivers

- **Touch no tracked file (PRD R4).** Recording coverage must not modify the
  repository's committed `.gitignore` or any other tracked file.
- **Independent of the user's gitignore (PRD R1, R2).** Invisibility must hold
  when the committed `.gitignore` has no `*.local*` pattern.
- **Cover worktrees, including `.niwa/` (PRD R3).** The mechanism must reach
  worktrees checked out under `.niwa/worktrees/`.
- **Idempotent and non-destructive (PRD R5, R8).** Repeated applies must not
  duplicate coverage, and any pre-existing user content in the coverage
  location must be preserved.
- **Fail closed (PRD R9).** If coverage cannot be recorded, the apply errors
  rather than leaving files visible.
- **Drift-proof verification (PRD R7).** The test must catch a future leaking
  file without a per-filename allowlist, and run locally and in CI.
- **Minimal blast radius / maintainability.** Prefer a single small helper with
  one obvious call site per operation over scattering writes across the
  materializers.

## Considered Options

### Decision 1: Where niwa records its ignore coverage

- **`.git/info/exclude` (chosen).** A repository-local ignore file git always
  consults, never committed, and resolved from the common git directory so it
  is shared by the primary checkout and every linked worktree. One write per
  repository covers the repo and all its worktrees. Satisfies R4 (untracked by
  the repo) and R3 (reaches worktrees) directly.
- **Committed `.gitignore` (rejected).** niwa could append `*.local*` and
  `.niwa/` to the repository's tracked `.gitignore`. Rejected by R4: it
  modifies a tracked file, so the act of recording coverage is itself a visible
  change -- exactly what the feature forbids.
- **`git update-index --skip-worktree` / `--assume-unchanged` (rejected).**
  These bits hide modifications to *tracked* files; they do nothing for
  untracked files like niwa's `.local` output or `.niwa/`, which are the actual
  leak. Wrong tool for untracked content.
- **Global `core.excludesFile` (rejected).** A per-user global ignore file is
  not repository-scoped: it would leak niwa's patterns into every repo on the
  machine and is not niwa's to own. It also does not travel with the workspace.

### Decision 2: What patterns niwa writes

- **Fixed managed block: `*.local*` and `.niwa/` (chosen).** These two families
  cover everything niwa materializes today -- all managed-repo output carries
  `.local`, and worktree scaffolding lives under `.niwa/`. A fixed block needs
  no per-run computation and no coupling to runtime state.
- **Derive entries from the materialized file set (rejected).** niwa could emit
  one exclude entry per file it wrote, read from the apply's produced-file set.
  Rejected: that produced-file set is the same `ManagedFiles` registry a prior
  bug escaped (a file written but never registered), so deriving coverage from
  it reintroduces the exact blind spot. The behavioral test -- not the registry
  -- is the completeness guarantee, so the patterns can be broad and static.

### Decision 3: How the block stays idempotent and non-destructive

- **Delimited managed block with begin/end markers (chosen).** niwa writes its
  patterns between `# >>> niwa managed >>>` and `# <<< niwa managed <<<`
  sentinel lines. On re-apply it replaces the content between the markers in
  place, leaving everything outside untouched. This mirrors the existing
  `EnsureInstanceGitignore` idiom, preserves user-authored exclude lines
  (R5), and is naturally idempotent (R8).
- **Append-if-missing per line (rejected).** Scanning for each pattern and
  appending when absent (the current `EnsureInstanceGitignore` approach) works
  for a single stable pattern but does not let niwa later evolve its block
  without leaving orphaned lines. A delimited block is the supersettable form.

## Decision Outcome

niwa gains a single helper -- `EnsureRepoExclude(repoGitDir)` in
`internal/workspace` -- that ensures the repository's `.git/info/exclude`
contains a niwa-managed block holding `*.local*` and `.niwa/`. The helper:

1. Resolves the repository's exclude file. For a primary checkout this is
   `<repo>/.git/info/exclude`; for a linked worktree, `<worktree>/.git` is a
   file pointing at `.git/worktrees/<id>/`, and the exclude file lives in the
   shared common directory. niwa resolves it with
   `git rev-parse --git-common-dir` (run with `-C <tree>`), then appends
   `info/exclude`, so both the apply path and the worktree path target the same
   shared file.
2. Reads the existing file (empty if absent), splices in or rewrites the
   delimited niwa block, and writes the result with `os.WriteFile`, preserving
   all content outside the markers.
3. Returns an error if the directory or file cannot be written; callers treat
   that error as fatal to the operation (fail closed, R9).

The apply pipeline calls `EnsureRepoExclude` once per managed repository after
materialization. `CreateSession` calls it for the new worktree after
`scaffoldWorktreeNiwa`. Because both resolve to the common-dir exclude file, the
worktree call and the apply call converge on one file per repository.

The now-obsolete managed-repo `.gitignore` warning (`CheckGitignore`,
`internal/workspace/content.go`) is removed (R6): niwa no longer needs the user
to add `*.local*`, so instructing them to would contradict the guarantee.
`CheckGitignore` is invoked at two in-pipeline sites -- `InstallRepoContent`
(content.go) and `SettingsMaterializer.Materialize` (materialize.go) -- and the
removal must cover both so no stale warning survives. The instance-root
`EnsureInstanceGitignore` is untouched -- it serves the non-git workspace
parent, which is out of scope.

## Solution Architecture

### New component: `EnsureRepoExclude`

`internal/workspace/exclude.go` (new file):

- `const niwaExcludeBegin = "# >>> niwa managed >>>"`
- `const niwaExcludeEnd = "# <<< niwa managed <<<"`
- `niwaExcludePatterns = []string{"*.local*", ".niwa/"}`
- `EnsureRepoExclude(tree string) error` -- `tree` is a working-tree path
  (primary checkout or worktree).
  1. `commonDir`: run `git -C <tree> rev-parse --git-common-dir`; resolve the
     result relative to `tree` when it is not absolute.
  2. `excludePath = <commonDir>/info/exclude`; `MkdirAll(<commonDir>/info)`.
  3. Read existing content; compute the new content by replacing the text
     between the markers (or appending a fresh marked block when absent).
  4. Write the file with `os.WriteFile`; return any error to the caller.
- `renderNiwaBlock(existing []byte) []byte` -- pure function, unit-testable
  without a git repo: given existing file bytes, returns the bytes with the
  niwa block inserted/replaced. Idempotent: `render(render(x)) == render(x)`.

### Call sites

- **Apply.** In `internal/workspace/apply.go`, in the `runPipeline` per-repo
  loop (Step 6.5, where `repoDir` is in scope after the materializers run for
  that repository), call `EnsureRepoExclude(repoDir)`. A returned error aborts
  the apply for that instance with a clear message.
- **Worktree create.** In `internal/mcp/handlers_session.go`, inside
  `CreateSession` after `scaffoldWorktreeNiwa(wtPath, repo)` succeeds, call
  `EnsureRepoExclude(wtPath)`. A returned error joins the existing
  cleanup-on-failure path (remove the worktree, return the error).

### Data flow

```
niwa apply
  -> runPipeline materializes *.local* files into <repo>
  -> EnsureRepoExclude(<repo>)
       git -C <repo> rev-parse --git-common-dir  => <repo>/.git
       write <repo>/.git/info/exclude  [niwa block: *.local*, .niwa/]
  -> git status in <repo> is clean

niwa session create <repo> <purpose>
  -> git worktree add <wt> ; scaffoldWorktreeNiwa writes <wt>/.niwa/
  -> EnsureRepoExclude(<wt>)
       git -C <wt> rev-parse --git-common-dir  => <repo>/.git   (shared)
       write <repo>/.git/info/exclude  [niwa block]
  -> git status in <wt> is clean (.niwa/ ignored)
```

### Test architecture

The functional suite (`test/functional/`, godog) gains scenarios in a new
`test/functional/features/git-invisibility.feature`, backed by steps that reuse
the existing `newLocalGitServer` fixture factory and `runGitInDir` helper:

- **Apply invisibility.** Given a committed-clean managed repo whose
  `.gitignore` has no `*.local*`; when `niwa apply` runs; then
  `git -C <repo> status --porcelain` is empty, and at least one niwa-authored
  file exists on disk (non-vacuous, PRD AC2).
- **Worktree invisibility.** Given a managed instance; when
  `niwa session create <repo> <purpose>` runs; then
  `git -C <worktree> status --porcelain` is empty.
- **Idempotency.** Running `niwa apply` twice leaves status empty and the
  exclude block un-duplicated.
- **User-content preservation.** A pre-existing line in `.git/info/exclude`
  survives the niwa write.
- **Negative (assertion has teeth).** With an uncovered niwa-style file planted
  (a path matching neither `*.local*` nor `.niwa/`), the porcelain assertion
  fails -- proving the test would catch a real leak.

Unit tests cover `renderNiwaBlock` idempotency and user-content preservation
directly, without a git repo.

## Implementation Approach

1. **`exclude.go` + unit tests.** Implement `renderNiwaBlock` and
   `EnsureRepoExclude`; unit-test the pure renderer (insert, replace,
   idempotent, preserve surrounding content).
2. **Wire apply.** Call `EnsureRepoExclude` per managed repo in the apply path;
   propagate errors (fail closed). Remove the obsolete managed-repo
   `CheckGitignore` warning.
3. **Wire worktree create.** Call `EnsureRepoExclude` after
   `scaffoldWorktreeNiwa`; route errors through the existing worktree cleanup.
4. **Functional feature + steps.** Add `git-invisibility.feature` and step
   definitions (apply, worktree, idempotency, user-content, negative).
5. **Verify.** `go test ./...`, `make test-functional`, `go vet`,
   `golangci-lint`.

## Security Considerations

- **Path resolution.** `EnsureRepoExclude` resolves the exclude file via
  `git rev-parse --git-common-dir` rather than string-building, so a worktree's
  `.git` file indirection is handled by git itself; niwa does not parse the
  pointer. The common-dir output is joined with the fixed literal
  `info/exclude`; no untrusted input enters the path.
- **No untrusted interpolation.** The block content is the two fixed literal
  patterns; nothing from the repository, the user, or the network is
  interpolated into the written bytes.
- **Non-destructive writes.** The helper preserves all content outside its
  markers, so it cannot clobber a user's existing exclude rules. The file is
  written with standard non-secret permissions (0o644); it contains no secrets.
- **Symlink consideration.** `.git/info/exclude` is inside the repository's git
  directory, which niwa already owns and writes to (state, scaffolding). The
  write target is derived from git's own common-dir resolution, not a
  user-supplied path, so this introduces no new traversal surface beyond what
  niwa already has over `.git`.
- **Fail closed.** An unwritable exclude file aborts the operation, so the
  failure mode is "niwa refuses to leave files visible," never "niwa silently
  leaks." No denial-of-service or privilege concern: the operation is a local
  file write niwa already has the rights to perform.

Net: no new external input, no secret handling, no new traversal surface beyond
niwa's existing ownership of `.git`. Security impact is minimal.

## Consequences

### Positive

- Invisibility becomes niwa's guarantee, independent of the user's committed
  `.gitignore` (PRD R1, R2).
- Worktrees are covered, including the `.niwa/` scaffold, via the shared
  common-dir exclude (PRD R3).
- One small helper with two obvious call sites; the pure `renderNiwaBlock` is
  unit-testable without a git repo, and the behavioral functional test guards
  the end-to-end guarantee against future drift (PRD R7).
- Removing the obsolete `.gitignore` warning ends a now-contradictory
  instruction to the user (PRD R6).

### Negative

- niwa writes into `.git/info/exclude`, a file some developers maintain by
  hand. Mitigation: the delimited managed block preserves all user content and
  is rewritten only between its markers (PRD R5).
- A user's global `core.excludesFile` that deliberately un-ignores niwa's paths
  can still override repository-local excludes. Mitigation: documented as a
  Known Limitation in the PRD; the functional test runs in a sandboxed HOME so
  the guarantee is verified independent of any personal global config.
- The guarantee is "going forward": files a user already committed are not
  scrubbed. Mitigation: explicitly scoped out in the PRD; on the next apply the
  exclude block makes currently-untracked niwa files invisible.

### Mitigations

- Delimited markers + content-preserving rewrite protect user exclude entries.
- Fail-closed error handling prevents silent leaks.
- The negative functional test proves the porcelain assertion catches a real
  leak, so the guard cannot silently rot into a no-op.
