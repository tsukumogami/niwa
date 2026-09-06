# Explore Scope: worktree-setup-scripts

## Visibility

Public

## Core Question

`niwa apply` runs a repo's `scripts/setup/` against the clone it materializes,
on every apply. Nothing runs them against a worktree. A fresh
`.niwa/worktrees/<repo>-<sid>/` therefore arrives with the repo's source but
none of its dependency tree -- no `node_modules`, no `.venv`. Is that a gap
niwa should close, and if so, where does the fix hook in and what does it do
about cost, blocking, idempotency, and partial failure?

## Context

Raised by the `host_env_tsuku` coordinator session, which hit this from the
codespar side. Four evidence points arrived with the brief and were confirmed
against `origin/main` = `d25ad4d` (the commit `niwa 0.24.0` was built from)
before any research started:

1. One production caller of `RunSetupScripts` -- `internal/workspace/apply.go:1959`,
   over `classified` repos, against the clone path.
2. Nothing in `internal/worktree/*.go` references setup provisioning.
3. `apply.go:1921` (Step 6.6) fans env out to live worktrees and states the
   guarantee: "after an apply no live worktree holds a value different from its
   clone (R6)". `apply.go:1951` (Step 6.75) then does clone-only setup work.
4. `docs/designs/current/DESIGN-post-clone-scripts.md` contains zero occurrences
   of "worktree".

One thing the brief did not have, found during confirmation and worth carrying
into the research: `internal/workspace/worktree_content.go` already defines
`ApplyToWorktree` and `worktreeApplyEvent = "apply"`, described as "the
worktree-lifecycle event run by ApplyToWorktree on both `niwa worktree create`
and `niwa worktree apply`", sharing `runRepoMaterializers` with the instance
pipeline so there is "no forked installer". The worktree side is not a missing
subsystem -- it is an existing pipeline with one step absent, and it lives in
the same Go package as `RunSetupScripts`.

Cost data from the coordinator's own measurement on the codespar monorepo:
32 s for the first worktree on a machine, ~13 s for each subsequent one (build
cache shared through `$HOME`), 341 MB of `node_modules` per worktree.

Existing workarounds, both documented rather than inferred: `dangazineu/commuter`'s
README instructs readers to run `./scripts/setup/01-python-env.sh` and
`02-project-tools.sh` by hand inside "a git worktree (which niwa does not
provision)"; the codespar workspace has a `cs-worktree-bootstrap` script doing
the same job from the other direction.

## In Scope

- Whether the clone/worktree asymmetry is a deliberate boundary or an accident.
- Where a fix would hook in, if one is warranted, named to file:line.
- The design questions that make it non-obvious: blocking vs. background on
  `worktree create`, the idempotency contract, whether `setup_dir = ""` is
  enough of an opt-out, and the failure posture for a half-provisioned worktree.
- The blast radius of each candidate hook point.
- The "no, this is not niwa's job" answer, taken seriously.

## Out of Scope

- Implementing the change. This run ends at an entry point.
- Any change to `RunSetupScripts`' existing clone behavior.
- The codespar workspace and its repos.

## Research Leads

1. **Where could setup scripts hook into the worktree lifecycle, and what does each
   candidate point already have in hand?**
   The fix's shape is decided by which call sites can reach a resolved config, a
   redactor, a reporter, and the worktree path at once. Map `niwa worktree create`
   and `niwa worktree apply` end to end and name every candidate with file:line,
   plus what it has and what it lacks.

2. **What did the design record actually decide about setup scripts and about what
   a worktree is, and do those two records contradict each other?**
   Evidence point 4 says the post-clone-scripts design never mentions worktrees.
   The reverse question is unanswered: do the worktree designs claim an
   equivalence with clones, and is the env-only scope of that claim deliberate?

3. **Would running a repo's setup scripts inside a worktree actually work, and what
   would it cost?**
   A setup script written for a clone may be wrong in a worktree -- absolute
   paths, `.git` being a file rather than a directory, tool state under `$HOME`,
   caches. Survey real setup scripts and check whether the idempotency the apply
   path relies on holds per-worktree.

4. **What does the rest of niwa do when a worktree needs something the clone got,
   and what is the precedent outside niwa?**
   Step 6.6 is one answer already in the tree. Dispatch and ephemeral sessions
   may be another. Establish whether the fan-out pattern is niwa's house style
   for this class of problem, and whether other tooling has a post-worktree-create
   provisioning hook worth borrowing.
