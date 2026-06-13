---
status: Done
problem: |
  niwa writes files into the git repositories it manages, and is meant to
  leave no trace in their history. That invisibility currently depends on the
  user adding *.local* to their committed .gitignore and on every niwa-written
  file remembering the .local convention. Worktree creation scaffolds an
  untracked .niwa/ directory nothing guarantees is ignored, and each new
  capability can write a file that escapes the convention. No automated check
  catches the drift, so the first signal of a regression is a developer seeing
  niwa's files in git status after it ships.
goals: |
  niwa guarantees its own invisibility in the working trees of the repos it
  manages and the worktrees it creates, independent of the user's committed
  .gitignore. A contributor whose change would make niwa leave a trace is
  caught by a functional test, locally and in CI, before merge.
upstream: docs/briefs/BRIEF-repo-git-invisibility.md
---

## Status

Done

## Problem Statement

niwa clones and manages git repositories on a developer's behalf and writes
files into them as part of its job: workspace context, settings, environment
files, hook scripts, and per-worktree scaffolding. A core promise is that none
of this shows up in the managed repository's git history -- niwa is invisible
to the repos it touches.

That promise rests on two fragile mechanisms today. The first is a naming
convention: niwa gives its managed-repo output a `.local` infix and asks the
user to add a `*.local*` pattern to the repository's committed `.gitignore`.
niwa only warns when the pattern is missing; if the user never adds it, every
niwa file appears as untracked. The second is the convention's completeness:
invisibility holds only while every file niwa writes carries the `.local`
infix and lands where the pattern reaches.

Both erode as niwa grows. Creating a worktree scaffolds a `.niwa/` directory
straight into the worktree's working tree, and nothing guarantees the managed
repository ignores it, so a fresh worktree can show untracked niwa files
immediately. Every new capability that writes a file is another chance to pick
a path the convention does not cover. Nothing notices when niwa output starts
leaking into a working tree, so a regression is discovered by a developer
reading `git status`, after the change has shipped.

Affected: developers who use niwa to manage repositories, and contributors who
extend niwa and can introduce a leak without any signal.

## Goals

- A developer sees nothing from niwa in `git status` for any managed
  repository or niwa-created worktree, whether or not their committed
  `.gitignore` mentions niwa.
- niwa holds the invisibility guarantee itself; it is no longer a setting the
  user must configure and maintain.
- A contributor who would introduce a leak is told by a failing test, locally
  and in CI, before the change can merge.
- The guarantee is verified end to end by a check that does not enumerate file
  names, so a newly leaking file trips it automatically.

## User Stories

Technical feature; described as use cases.

- As a developer adopting niwa on an existing repository, I want niwa's files
  to stay out of `git status` without my editing `.gitignore`, so that niwa is
  invisible by default.
- As a developer creating a worktree, I want a freshly created worktree to
  have a clean `git status`, so that the only changes that ever appear are my
  own.
- As a niwa contributor, I want a test that fails when my change makes niwa
  leave a trace in a managed tree, so that I catch the regression before
  opening a pull request.

### Terminology

- **niwa-authored file**: any file niwa writes into a managed repository's
  working tree or a niwa-created worktree's working tree -- workspace context,
  settings, environment files, hook scripts, and per-worktree scaffolding such
  as `.niwa/`. Files a user authors are not niwa-authored.
- **Managed-tree baseline**: the requirements below, and the test that verifies
  them (R7), assume the managed repository starts from a committed-clean state
  (`git status --porcelain` empty) before niwa runs. Any dirtiness observed
  after niwa runs is therefore attributable to niwa, with no per-file allowlist
  needed.

This feature concerns the managed-repository `.gitignore` and the managed
repository's git metadata. It does not concern the instance-root `.gitignore`
that `EnsureInstanceGitignore` writes into the non-git workspace parent (out of
scope -- see Out of Scope).

### Functional

- **R1.** After `niwa apply`, every managed repository working tree niwa wrote
  into shows no niwa-authored file as untracked or modified in `git status`,
  regardless of the repository's committed `.gitignore` content (including when
  it contains no `*.local*` pattern).
- **R2.** niwa achieves R1 on its own: invisibility does not depend on the user
  having added any pattern to the repository's committed `.gitignore`. niwa
  records the ignore coverage it needs in a location that is not committed to
  the managed repository, and it records that coverage on every apply (a niwa
  that wrote files but recorded no coverage does not satisfy R1).
- **R3.** After `niwa session create` (worktree creation) the worktree working
  tree shows no niwa-authored file -- including per-worktree scaffolding such
  as `.niwa/` -- as untracked or modified in `git status`. Re-syncing an
  existing worktree's content reuses the same materialization path as apply, so
  it inherits R1's guarantee; no separate worktree re-sync command is assumed
  by this PRD.
- **R4.** niwa modifies no file tracked by the managed repository in order to
  achieve invisibility. In particular, niwa does not edit the repository's
  committed `.gitignore`; the act of recording coverage is itself invisible.
- **R5.** The coverage location may be one a user can also edit (an uncommitted,
  repository-local git file). When pre-existing content is present there, niwa
  adds its entries without discarding or reordering content it did not write.
- **R6.** niwa does not emit guidance telling the user that invisibility depends
  on adding `*.local*` to the managed repository's committed `.gitignore`. Any
  such pre-existing warning is removed or recast as advisory, so the user is
  not left a stale instruction that contradicts the self-guaranteed behavior.

### Non-functional

- **R7.** An automated functional test verifies R1 and R3 end to end by running
  the real niwa operations against committed git fixtures and asserting an
  empty `git status --porcelain`. It is written generically -- it does not
  enumerate niwa's file names -- so a future niwa-written file that escapes
  coverage causes the test to fail. A companion negative test confirms the
  assertion has teeth: with niwa's coverage suppressed (or an uncovered file
  planted), the same `git status --porcelain` assertion fails. The test runs
  under `make test-functional` locally and in the CI workflow.
- **R8.** The guarantee is idempotent: repeated `niwa apply` runs do not
  duplicate or accumulate the recorded coverage, and `git status` stays clean
  across runs. Recording coverage when the managed repository's committed
  `.gitignore` already contains `*.local*` does not double-cover or conflict.
- **R9.** If niwa cannot record coverage (the coverage location is unwritable),
  niwa fails the apply with a clear error rather than silently proceeding to
  leave niwa-authored files visible in the managed tree.

## Acceptance Criteria

- [ ] Running `niwa apply` in a workspace whose managed repository `.gitignore`
  contains no `*.local*` pattern, starting from a committed-clean repository,
  leaves `git -C <repo> status --porcelain` empty. (R1, R2)
- [ ] In that same scenario, niwa actually materialized at least one
  niwa-authored file into the managed tree, so the empty status proves coverage
  rather than absence of output. (R2)
- [ ] Running `niwa session create <repo> <purpose>` leaves
  `git -C <worktree> status --porcelain` empty, with no `.niwa/` shown as
  untracked. (R3)
- [ ] No file tracked by the managed repository (including its committed
  `.gitignore`) is changed by niwa to achieve invisibility. (R4)
- [ ] When the coverage location already contains user content before niwa
  runs, that content is still present after niwa records its entries. (R5)
- [ ] niwa emits no message instructing the user to add `*.local*` to the
  managed repository's committed `.gitignore` as a precondition for
  invisibility. (R6)
- [ ] A functional test exercises the apply and worktree-create scenarios and
  asserts an empty `git status --porcelain`; it runs under
  `make test-functional` and in the CI test workflow. (R7)
- [ ] A negative functional test demonstrates the assertion catches leaks: with
  coverage suppressed or an uncovered niwa-style file planted, the same
  `git status --porcelain` assertion fails. (R7)
- [ ] Re-running `niwa apply` is idempotent: recorded coverage is not
  duplicated and `git status --porcelain` remains empty across runs, including
  when the committed `.gitignore` already contains `*.local*`. (R8)
- [ ] When the coverage location cannot be written, `niwa apply` exits with a
  non-zero status and a clear error, rather than completing with niwa-authored
  files left visible. (R9)

## Out of Scope

- The instance root (the non-git workspace parent directory niwa writes
  workspace-level context and settings into). It is non-git by design and is
  not a repository niwa manages.
- Remediating niwa files a user has already committed to a managed
  repository's history. niwa does not rewrite history or delete tracked files;
  the guarantee covers working-tree invisibility going forward, and on the next
  apply the recorded coverage makes currently-untracked niwa files invisible.
- The specific recording mechanism (how and where niwa registers its ignore
  coverage). The PRD constrains it to "not a committed file" (R4); the choice
  of mechanism is downstream design work.
- Changing the `.local` naming convention. This feature makes invisibility
  self-guaranteed; it does not redefine how niwa names its output.
- Taking over management of the user's committed `.gitignore`. niwa stops
  depending on it but does not edit or own it.

## Known Limitations

- A user's global git configuration (for example a `core.excludesFile` that
  contradicts niwa's coverage, or a global include that forces a path) can
  override repository-local ignore behavior. niwa guarantees invisibility
  through the coverage it controls; it cannot defend against a user's global
  git config deliberately un-ignoring niwa's paths. The functional test runs in
  a sandboxed HOME, so it verifies the guarantee independent of any developer's
  personal global config.

## Decisions and Trade-offs

This section closes the upstream BRIEF's Open Questions.

- **Going-forward guarantee, not retroactive scrubbing** (BRIEF Open Question
  1). niwa guarantees invisibility going forward; on the next apply, the
  recorded coverage makes currently-untracked niwa files invisible. niwa does
  not delete or rewrite files a user has already committed. Alternative
  considered: actively scrub pre-existing pollution -- rejected as risky (it
  touches user-tracked content) and above the altitude of an
  invisibility guarantee.
- **Coverage set the check verifies** (BRIEF Open Question 2). The automated
  check covers both `niwa apply` and `niwa session create`; re-sync is covered
  transitively because it uses the same materialization path. Alternative
  considered: apply-only -- rejected because the worktree `.niwa/` scaffold is
  the most concrete current leak and must be guarded directly.
- **Coverage recorded outside committed files** (R4). The mechanism must record
  ignore coverage somewhere the managed repository does not track, so the act
  of recording does not itself become a visible change. The exact location is a
  design decision; the requirement only fixes the constraint.
