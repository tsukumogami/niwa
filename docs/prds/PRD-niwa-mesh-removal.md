---
status: Accepted
problem: |
  niwa carries a pre-pivot agent-facing mesh (MCP server, task-delegation
  substrate, per-worktree daemon, apply-pipeline hook synthesis) that is
  non-functional in practice. The one capability users rely on, git worktree
  creation, is implemented inside that mesh package and cannot survive its
  deletion without a deliberate decoupling. The dead code obscures what niwa
  is and what actually works.
goals: |
  Remove the non-functional mesh subsystem in full while preserving worktree
  creation as a first-class CLI command independent of the deleted package.
  After the change, niwa's codebase contains only working capabilities, and
  `niwa apply` synthesizes no mesh hooks and spawns no background daemons.
upstream: docs/briefs/BRIEF-niwa-mesh-removal.md
---

# PRD: niwa mesh removal

## Status

Accepted

## Problem Statement

niwa accumulated an agent-facing coordination layer before a strategic pivot:
an MCP server exposing mesh tools, a task-delegation substrate, a per-worktree
background daemon, and an apply pipeline that synthesizes default mesh hooks
into managed workspaces. That layer is non-functional in practice today —
nothing depends on it working and nothing exercises it end-to-end.

The people affected are niwa's users and contributors. Users rely on exactly
one capability from this area: creating and destroying git worktrees for a
repo in the workspace. That capability is structurally entangled with the dead
mesh code — the real `git worktree add` lives inside the MCP server's session
handler, the surviving CLI commands reach it through the MCP server type, and
the workspace bootstrap path calls the same session-creation logic through a
callback. Contributors reading the tree cannot tell which half of the tool is
load-bearing, and the largest internal package is dead weight.

This matters now because the mesh's continued presence blocks a clean codebase
and contradicts niwa's identity as a workspace and worktree manager rather than
an agent-facing tool. The removal must happen before any replacement
coordination mechanism is built, so the workspace never carries two competing
mechanisms at once. The capability cannot simply be deleted alongside the mesh,
because deleting the mesh package today would take worktree creation with it.

## Goals

- Worktree creation survives the removal as a first-class CLI command that
  depends on no MCP or mesh package.
- The pre-pivot mesh subsystem is gone in full: the MCP server, the
  task-delegation CLI cluster, and the apply-pipeline mesh-hook synthesis.
- `niwa apply` installs only the hooks the workspace configuration declares and
  starts no background process.
- The codebase a contributor reads contains only capabilities that work.

## User Stories

- As a developer in a niwa-managed workspace, I want to create and destroy git
  worktrees through a stable CLI command, so that my isolated-checkout workflow
  keeps working after the mesh is removed.
- As a contributor onboarding to niwa, I want the package tree to contain only
  live capabilities, so that I do not waste time reverse-engineering a
  non-functional agent-facing layer.
- As an operator running `niwa apply`, I want the pipeline to install only the
  hooks I declared and spawn no background daemon, so that applying a workspace
  has no hidden side effects.
- As a maintainer, I want the mesh removed before any replacement coordination
  layer exists, so that the workspace never carries two competing coordination
  mechanisms at the same time.

## Requirements

### Functional

- **R1** — Worktree creation, destruction, and listing remain available as CLI
  commands after the change, with behavior equivalent to today (same worktree
  and branch creation, same teardown).
- **R2** — The worktree CLI commands depend on no MCP or mesh package; the
  capability is reachable without instantiating an MCP server.
- **R3** — The MCP server package (server, mesh tools, audit subsystem,
  error-translation layer, daemon-starter wiring) is removed from the codebase.
- **R4** — The pre-pivot CLI command cluster that wraps the mesh and the
  task-delegation substrate is removed, and its orphaned subcommands are
  unregistered from the command tree.
- **R5** — `niwa apply` no longer synthesizes default mesh hooks; a workspace
  receives only the hooks its configuration declares explicitly.
- **R6** — `niwa apply` no longer spawns a background daemon; applying a
  workspace launches no background process.
- **R7** — The preserved worktree verbs are exposed under their final command
  name (`niwa worktree ...`), with deprecation aliases that keep the prior
  command name working and emit a deprecation notice.
- **R8** — The workspace bootstrap path continues to create its worktree
  through the preserved capability, with no dependency on the removed package.

### Non-functional

- **R9** — After the change, the project builds and the full test suite passes
  with no unresolved references to the removed package.
- **R10** — The removal lands atomically: the codebase does not pass through a
  committed state on the main branch in which it fails to build or the apply
  pipeline still references removed daemon-spawn or hook-synthesis code.

## Acceptance Criteria

- [ ] `niwa worktree create <repo> <purpose>` creates a worktree and branch
      end-to-end, equivalent to the prior command's behavior.
- [ ] `niwa worktree destroy` and `niwa worktree list` (or their equivalents)
      work end-to-end.
- [ ] The prior worktree command name still works and emits a deprecation
      notice pointing at the new name.
- [ ] The MCP server package no longer exists in the source tree.
- [ ] The pre-pivot mesh and task-delegation CLI files no longer exist, and no
      orphaned subcommands are registered.
- [ ] `niwa apply` against a workspace installs only declared hooks and starts
      no background process.
- [ ] The workspace bootstrap path creates its worktree successfully after the
      removal.
- [ ] The project builds and the full test suite passes with no references to
      the removed package.
- [ ] The change is structured so the removal lands in a single release with no
      intermediate non-building committed state on main.

## Out of Scope

- Backfilling task delegation or building any replacement coordination
  mechanism. The removal ships ahead of any replacement by design; building the
  replacement is separate downstream work.
- Changes to the workspace-materialization capability (clone, scaffold,
  snapshot, secret provisioning). That subsystem is independent of the mesh and
  is unchanged by this work.
- Expanding the worktree capability with new verbs or policies beyond the
  rename and its deprecation aliases.

## Decisions and Trade-offs

- **This work needs a DESIGN doc before implementation.** The capability is
  entangled with the package being deleted, so the order of operations (extract
  the worktree subsystem into a standalone package, then delete the mesh) and
  the package boundary are architectural decisions. See the downstream
  DESIGN-niwa-mesh-removal for the extraction boundary, how the daemon spawn is
  severed from worktree creation, and whether the worktree-attach primitive is
  retained. The PRD deliberately states the required boundary (R2) without
  prescribing the package shape.
- **Atomic landing over incremental deletion (R10).** Because the surviving CLI
  consumes the removed package's symbols and the apply pipeline references the
  daemon-spawn path, a partial deletion would leave a non-building tree. The
  trade-off — a larger single change versus several smaller ones — is accepted
  in favor of never landing a broken intermediate on main.
- **Rename folded in (R7).** The preserved verbs are renamed in the same change
  rather than in a follow-up, so the surviving capability lands once under its
  final name. Deprecation aliases bound the migration cost for existing callers.
