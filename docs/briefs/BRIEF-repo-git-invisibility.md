---
schema: brief/v1
status: Accepted
problem: |
  niwa is meant to leave no trace in the git history of the repos it
  manages, but that invisibility depends on the user adding *.local* to
  their committed .gitignore, and on every niwa-written file remembering
  the .local convention. Worktrees scaffold an untracked .niwa/ directory
  nothing guarantees is ignored, and each new capability is a fresh chance
  to write a file that escapes the convention. No check catches the drift.
outcome: |
  A developer using niwa sees a clean git status in every managed repo and
  niwa-created worktree, regardless of what's in their own .gitignore,
  because niwa guarantees its own invisibility. A contributor who adds a
  capability that would leave a trace finds out from a failing test locally
  and in CI, before it merges.
motivating_context: |
  A stale Stop hook left on disk by a removed feature (the mesh cleanup)
  surfaced how easily niwa-written files outlive the code that produced
  them. The same file-class -- niwa output landing in a git working tree --
  is the invisibility risk, and it grows with every new capability.
---

## Status

Accepted

Authored as the BRIEF in a BRIEF -> PRD -> DESIGN -> PLAN chain for the
repo-git-invisibility feature. The downstream PRD owns the requirements;
the DESIGN owns the recording mechanism (how niwa registers its ignore
coverage). This brief stops at framing.

## Problem Statement

niwa clones and manages git repositories on a developer's behalf, and it
writes files into those repositories as part of doing its job -- context
files, settings, environment files, hook scripts, and per-worktree
scaffolding. A core promise is that none of this appears in the managed
repository's git history: niwa is supposed to be invisible to the repos
it touches.

Today that promise rests on two fragile mechanisms. The first is a naming
convention: niwa gives its managed-repo output a `.local` infix and asks
the user to add a `*.local*` pattern to the repository's committed
`.gitignore`. niwa only warns when the pattern is missing -- if the user
never adds it, every niwa file shows up as untracked. The second is the
convention's own completeness: invisibility holds only as long as every
file niwa writes remembers the `.local` infix, and only for files that
land where the pattern reaches.

Both mechanisms erode as niwa grows. Creating a worktree scaffolds a
`.niwa/` directory directly into the worktree's working tree, and nothing
guarantees the managed repository ignores it -- so a fresh worktree can
show untracked niwa files immediately. Every new capability that writes a
file is another opportunity to pick a path the convention doesn't cover.
There is no automated check that notices when niwa's output starts
leaking into a working tree, so the first signal of a regression is a
developer seeing niwa's files in their `git status` -- after it ships.

## User Outcome

A developer who runs niwa against their repositories sees nothing from
niwa in `git status` -- not in the managed repositories niwa clones, and
not in the worktrees niwa creates -- whether or not their own `.gitignore`
mentions niwa at all. Invisibility stops being something the user has to
configure and maintain; it becomes something niwa guarantees on its own.

A contributor extending niwa gets the same guarantee enforced for them. If
a change they make would cause niwa to leave a trace in a managed tree, a
test fails -- on their own machine and in CI -- naming the leak before the
change can merge. The invisibility promise is held by the test suite, not
by every contributor remembering a convention.

## User Journeys

### A developer adopts niwa on an existing repository

A developer runs niwa to manage a repository whose committed `.gitignore`
has never heard of niwa and contains no `*.local*` pattern. niwa applies
its configuration, writing its context and settings files into the
working tree. The developer runs `git status` and sees nothing out of
place -- the repository is exactly as clean as before niwa touched it,
without ever having edited `.gitignore`.

### A developer spins up a worktree to start a task

A developer uses niwa to create a worktree for a piece of work. niwa
checks out the branch and scaffolds its per-worktree state into the
working tree. Running `git status` in the new worktree before writing a
line of code shows a clean tree -- niwa's scaffolding is invisible, so
the only changes that ever appear are the developer's own.

### A contributor adds a capability that writes a new file

A contributor extends niwa with a feature that materializes a new file
into managed repositories. They write the code and run the test suite. A
test fails, reporting that the new file appears as untracked in a managed
repository's `git status`. The failure surfaces the leak before a pull
request opens; the contributor adjusts so niwa keeps the file invisible,
and the test passes. The same test guards the boundary in CI for every
future change.

## Scope Boundary

### In

- The git working trees of repositories niwa clones and manages: after
  niwa applies, their `git status` shows nothing niwa authored.
- The worktrees niwa creates for managed repositories: after creation and
  re-sync, their `git status` shows nothing niwa authored, including the
  per-worktree scaffolding.
- niwa holding the invisibility guarantee itself, rather than depending on
  the user having added a pattern to their committed `.gitignore`.
- An automated check that verifies the guarantee end to end and fails when
  niwa output leaks into a managed tree, runnable both locally and in CI,
  written so a newly leaking file trips it without the check having to
  enumerate file names.

### Out

- The instance root (the non-git workspace parent directory niwa writes
  workspace-level context and settings into). It is non-git by design and
  is not a repository niwa manages; its invisibility is a separate concern.
- The recording mechanism -- exactly how niwa registers its ignore
  coverage so a managed tree stays clean -- is downstream DESIGN territory,
  not framing.
- Changing the `.local` naming convention itself. This feature makes
  invisibility self-guaranteed; it does not redefine how niwa names its
  output.
- The content of the user's own committed `.gitignore`. niwa stops
  depending on it; niwa does not take over managing it.
