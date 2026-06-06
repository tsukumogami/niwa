---
status: Accepted
problem: |
  niwa's worktree-level commands (`niwa worktree create|destroy|list|attach|detach`)
  are asymmetric with the workspace-instance lifecycle (`niwa create|apply|destroy`).
  `niwa worktree create` installs no CLAUDE context — an agent launched in a worktree
  is under-served versus a repo checkout, which gets CLAUDE.local.md and `.claude/`
  accessories from `niwa apply`. There is no worktree analog of `apply`, no
  worktree-specific customization surface, and a real risk that the worktree level
  grows its own parallel content-install/teardown code.
goals: |
  Define a coherent, symmetric command structure where worktree-level operations
  mirror the workspace-instance lifecycle, a created worktree is a first-class CLAUDE
  working context (project parity plus a worktree-specific layer), and the operations
  that do similar things at both levels share one implementation. Identify the full
  surface (missing verbs, customizations, hooks, templates); a first slice may be
  implemented, but the deliverable is the requirements that the DESIGN and PLAN realize.
upstream: docs/briefs/BRIEF-worktree-command-parity.md
---

# PRD: worktree command parity

## Status

Accepted

## Problem Statement

niwa has two command levels that should feel like one coherent tool. At the
workspace-instance level, `niwa create` materializes an instance, `niwa apply`
idempotently installs and re-syncs its CLAUDE content/settings/accessories, and
`niwa destroy` tears it down; a repo checkout inside the instance emerges from
`apply` as a complete CLAUDE working context. At the worktree level — made
first-class by the mesh removal — `niwa worktree create` only runs `git worktree
add` and scaffolds bare state, installing no CLAUDE content at all.

The people affected are developers and agents working in worktrees, and the
maintainers extending niwa. A worktree is the isolated working context niwa
exists to produce, yet an agent launched there lacks the repo's `CLAUDE.local.md`
(a `.local`/untracked file that does not travel into a separate `git worktree`)
and any context describing what the worktree is for. There is no worktree-level
re-sync analog of `apply`, and the customization inputs that shape instance
content (content templates, overlay, hooks) have no worktree counterpart.

This matters now because the worktree commands just became a permanent,
user-facing part of niwa. Left unaddressed, the worktree level will accrete its
own parallel implementations of content installation and teardown — duplicating
instance-level logic and guaranteeing the two surfaces drift into inconsistent
behavior. The window to define the symmetric structure and shared code paths is
now, before that divergence sets in.

## Goals

- A worktree created by niwa is a first-class CLAUDE working context: it carries
  the same class of accessories a repo checkout gets from `niwa apply`, plus a
  worktree-specific layer.
- The worktree command surface is symmetric with the workspace-instance lifecycle
  where operations correspond, with the missing verbs identified and the
  no-analog gaps documented as deliberate.
- Worktree-level and workspace-level operations that do the same kind of work
  share one implementation rather than forking.
- A worktree-specific customization surface exists (keyed on purpose/branch),
  mirroring instance content customization.

## User Stories

- As a developer, I want `niwa worktree create` to set up the worktree with the
  project's CLAUDE context (as a repo checkout has), so an agent I launch there is
  oriented without manual setup.
- As an agent author, I want a worktree to carry a worktree-specific context layer
  naming its purpose and branch, so the agent knows the specific job, not just the
  project.
- As an operator, I want to re-sync an existing worktree's content after a config
  change through a verb that mirrors `niwa apply`, with the same idempotent
  guarantees, rather than a bespoke procedure.
- As a contributor, I want the worktree command surface to mirror the instance
  lifecycle so I can predict it by analogy.
- As a maintainer, I want worktree and instance operations that do similar things
  to share code, so the two levels stay consistent as either evolves.

## Requirements

### Functional

- **R1** — `niwa worktree create` installs into the new worktree the same class of
  CLAUDE accessories a repo checkout receives from `niwa apply` (e.g.
  `CLAUDE.local.md` and the relevant `.claude/` accessories/settings/hooks), via
  reuse of the existing installers.
- **R2** — A created worktree carries a worktree-specific context layer keyed on
  its purpose and branch, distinct from the repo-level content.
- **R3** — A worktree-level re-sync verb exists, mirroring `niwa apply`: it
  idempotently re-installs/updates an existing worktree's content and accessories
  after workspace configuration changes.
- **R4** — The worktree command set is symmetric with the workspace-instance
  lifecycle where operations correspond: the design maps each instance verb
  (`create`/`apply`/`destroy`) to its worktree analog, names any missing verbs,
  and documents verbs that deliberately have no analog.
- **R5** — A worktree customization surface (a template and/or hook keyed on
  purpose/branch) exists as the counterpart of instance content customization,
  letting a maintainer shape worktree context without hand-editing each worktree.
- **R6** — Worktree-level and workspace-level operations that perform the same kind
  of work (content installation, teardown) share one implementation; the worktree
  level does not duplicate installer/teardown logic that exists for the instance
  level.
- **R7** — `niwa worktree destroy` is symmetric with the worktree-scope teardown:
  it removes what `worktree create`/the worktree-apply installed, consistent with
  how instance destroy tears down instance content.

### Non-functional

- **R8** — No behavior change to the workspace-instance commands (`create`/
  `apply`/`destroy`); the worktree work reuses existing installers without altering
  instance behavior.
- **R9** — The implementation respects the package-dependency constraints from the
  mesh removal: `internal/worktree/` remains a leaf (no import cycle), and content-
  install orchestration lives where it does not force one.

## Acceptance Criteria

- [ ] After `niwa worktree create <repo> <purpose>`, the worktree contains the same
      class of CLAUDE accessories a repo checkout has after `niwa apply` (verified
      file-for-file against the repo-level install).
- [ ] The worktree contains a worktree-specific context layer that names its
      purpose and branch.
- [ ] A worktree re-sync verb (the `apply` analog) exists and, run twice, is
      idempotent (second run produces no spurious changes).
- [ ] The design documents the full instance-verb -> worktree-verb mapping,
      including missing verbs and deliberate no-analog gaps.
- [ ] A worktree customization surface (template/hook keyed on purpose/branch) is
      defined and demonstrably shapes the worktree's context.
- [ ] Worktree content-install and teardown call the same underlying
      implementation as the instance level (no duplicated installer/teardown code
      path).
- [ ] `niwa worktree destroy` removes the worktree and the content the worktree
      install created.
- [ ] `go build ./...` and `go test ./...` pass; workspace-instance command
      behavior is unchanged.

## Out of Scope

- Implementing the entire designed surface in the current branch. This PRD feeds a
  DESIGN and a PLAN; the PLAN phases the work (a create-parity slice may land
  first), but full implementation is not required here.
- Changing the behavior of the workspace-instance commands. They are the reference
  the worktree level mirrors.
- New capabilities above the worktree/repo level (no new instance-level verbs), and
  no reintroduction of the agent-coordination/mesh surface removed by prior work.

## Decisions and Trade-offs

- **This work needs a DESIGN before implementation.** The verb mapping (what the
  worktree analogs of create/apply/destroy are and which gaps stay empty), the
  shared-code-path architecture (where content-install orchestration lives so
  `internal/worktree/` stays a leaf), and the worktree customization model are
  architectural decisions. See the downstream DESIGN-worktree-command-parity. The
  PRD states the required outcomes (R1-R9) without prescribing the package shape.
- **Reuse over re-implementation (R6).** The worktree level reuses the existing
  workspace content installers rather than growing parallel ones. The trade-off —
  more upfront design to share code across two levels versus a quick standalone
  worktree installer — is accepted to prevent long-term drift.
- **Symmetry as a design goal, not literal 1:1 (R4).** Not every instance verb has
  a sensible worktree analog. The design maps the correspondences and documents
  the deliberate gaps rather than forcing a verb that does not fit.
