---
schema: brief/v1
status: Done
problem: |
  niwa now has two command levels — the workspace instance
  (`create`/`apply`/`destroy`) and the worktree (`worktree
  create`/`destroy`/`list`/`attach`/`detach`) — but they are asymmetric.
  `niwa worktree create` only runs `git worktree add` and scaffolds bare state;
  it installs none of the CLAUDE context a repo checkout gets from `niwa apply`,
  there is no worktree-level re-sync analog of `apply`, and the customization
  surfaces (content templates, hooks) have no worktree counterpart. The two
  levels risk diverging into different code paths and inconsistent UX.
outcome: |
  niwa's commands have a coherent, symmetric structure: worktree-level operations
  mirror workspace-level ones, an agent launched in a worktree gets the same
  first-class CLAUDE context a repo checkout gets (plus worktree-specific
  customization), and the operations that do similar things at both levels share
  one implementation rather than forking.
---

# BRIEF: worktree command parity

## Status

Done

The framing here is settled at the problem altitude; the downstream PRD captures
requirements and the DESIGN settles the symmetric verb set, the content-parity
model, the per-worktree customization hook, and the shared-code-path architecture.

## Problem Statement

The mesh removal turned worktree management into a first-class part of niwa's CLI:
`niwa worktree create | destroy | list | attach | detach`. But that surface grew
out of a session-management lineage, not out of the workspace-instance lineage,
so the two levels are shaped differently.

At the workspace-instance level, niwa has a clear lifecycle: `niwa create`
materializes an instance, `niwa apply` (idempotently) installs and re-syncs its
CLAUDE content, settings, and accessories, and `niwa destroy` tears it down. A
repo checkout inside an instance comes out of `apply` as a fully-formed CLAUDE
working context — `CLAUDE.local.md`, `.claude/` accessories, hooks, settings.

At the worktree level, `niwa worktree create` does far less: it adds a git
worktree and scaffolds bare state, and installs no CLAUDE content at all. So an
agent launched in a worktree is under-served compared to the same agent launched
in a repo checkout — it lacks the repo's `CLAUDE.local.md` (which, being a
`.local`/untracked file, does not travel into a separate worktree) and any
worktree-specific context describing what the worktree is for. There is also no
worktree-level analog of `apply` to re-sync that content when configuration
changes, and the customization inputs that shape instance content (templates,
overlay, hooks) have no defined worktree counterpart.

Two costs follow. First, the worktree experience is inconsistent with the
instance experience for operations that are conceptually the same. Second, absent
a deliberate design, the worktree level will accrete its own parallel
implementations of content installation and teardown — duplicating logic that
already exists for the instance level and guaranteeing the two drift apart.

## User Outcome

A developer who creates a worktree gets a working context as complete as a repo
checkout: launching an agent there loads the project's CLAUDE context plus a
worktree-specific layer that says what this worktree is for. An operator who
changes workspace configuration can re-sync a worktree the same way they re-sync
an instance, through a predictable verb that mirrors `apply`. A contributor
reading niwa's command surface finds a structure they can predict — the same
lifecycle shape at the worktree level as at the instance level — rather than two
differently-shaped command families. And a maintainer extending either level
touches shared content-installation and teardown code, so the two levels stay
consistent by construction.

## User Journeys

### Developer launches an agent in a fresh worktree

A developer runs `niwa worktree create <repo> <purpose>` to spin up an isolated
checkout for a task, then launches an agent in it. The agent loads the same
project CLAUDE context a repo checkout would, plus a worktree-specific layer
naming the purpose and branch — so it is oriented to both the project and the
specific job, with no manual setup.

### Operator re-syncs a worktree after a config change

A workspace's CLAUDE content or settings change. The operator re-syncs an
existing worktree through the worktree-level analog of `niwa apply`, and the
worktree's installed context updates idempotently — the same mental model and
the same guarantees as re-applying an instance, not a bespoke worktree procedure.

### Contributor predicts the command surface

A contributor who knows `niwa create | apply | destroy` at the instance level
reasons by analogy about the worktree level and finds the structure matches:
the worktree verbs map onto the instance verbs where the operations correspond,
and the gaps that don't have an analog are deliberate and documented rather than
accidental.

### Maintainer customizes per-worktree context

A maintainer wants every worktree for a given purpose to carry a particular
instruction. They use the worktree customization surface (a template or hook
keyed on purpose/branch) — the worktree-level counterpart of the instance content
templates — rather than hand-editing each worktree.

## Scope Boundary

### In

- A symmetric worktree-level command structure that mirrors the workspace-instance
  lifecycle (`create`/`apply`/`destroy`) where the operations correspond, with the
  missing verbs identified.
- CLAUDE-context parity for `niwa worktree create`: a worktree gets the same class
  of accessories a repo checkout gets from `niwa apply`, via reuse of the existing
  installers rather than a parallel implementation.
- A worktree-specific customization layer (keyed on purpose/branch) and its
  template/hook surface, as the worktree counterpart of instance content
  customization.
- A shared-code-path architecture so worktree-level and workspace-level operations
  that do similar things share implementation, respecting the package-dependency
  constraints introduced by the mesh removal.

### Out

- Implementing the entire designed surface in the current branch. This brief feeds
  a DESIGN and a PLAN; the PLAN phases the work and a first slice (create-parity)
  may follow, but full implementation is not in scope here.
- Changing the behavior of the workspace-instance commands (`create`/`apply`/
  `destroy`). They are the reference the worktree level mirrors, not a thing this
  work modifies.
- New capabilities above the worktree/repo level (no new instance-level verbs) or
  reintroduction of any agent-coordination/mesh surface removed by the prior work.
