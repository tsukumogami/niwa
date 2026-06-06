---
schema: brief/v1
status: Done
problem: |
  niwa carries a pre-pivot agent-facing mesh (an MCP server, a
  task-delegation substrate, a per-worktree daemon, and apply-pipeline
  hook synthesis) that is non-functional in practice. The only capability
  that actually works is git worktree creation, yet that capability is
  buried inside the mesh package and cannot survive the mesh's removal
  without a deliberate extraction.
outcome: |
  niwa is the workspace and worktree manager it claims to be: worktree
  creation is a first-class CLI command independent of any MCP package,
  the dead mesh code is gone, and `niwa apply` no longer synthesizes mesh
  hooks or spawns background daemons. A reader of the codebase sees only
  capabilities that work.
---

# BRIEF: niwa mesh removal

## Status

Done

The framing here is settled: this is debt paydown that removes a
non-functional subsystem while preserving the one capability built on top
of it. The downstream PRD owns the requirements articulation; the DESIGN
owns the extraction boundary and the atomic-landing shape.

## Problem Statement

niwa accumulated an agent-facing coordination layer before a strategic
pivot: an MCP server exposing mesh tools, a task-delegation substrate, a
per-worktree background daemon, and an apply pipeline that synthesizes
default mesh hooks into managed workspaces. That layer is non-functional
in practice today. Nothing depends on it working, and nothing exercises it
end-to-end.

What does work, and what users actually rely on, is a single capability:
creating and tearing down git worktrees for a repo in the workspace. That
capability is implemented inside the mesh package — the real
`git worktree add` lives in the MCP server's session handler, and the
surviving CLI commands reach it through the MCP server type. Surviving CLI
files consume dozens of distinct symbols from the mesh package, and the
workspace bootstrap path calls into the same session-creation logic
through a callback.

The result is a codebase whose largest internal package is dead weight
that the working capability is structurally entangled with. A contributor
reading the tree cannot tell which parts are load-bearing. The mesh's
presence contradicts what niwa is — a workspace manager, not an
agent-facing tool — and it cannot simply be deleted, because deleting it
would take worktree creation down with it.

## User Outcome

A developer using niwa to manage worktrees keeps the capability they rely
on, now exposed as a first-class command that owes nothing to a mesh
package. A contributor reading the codebase sees only subsystems that
work: the worktree and workspace-materialization capabilities, with no
dead agent-facing layer to mistake for live functionality. An operator
running `niwa apply` against a workspace gets exactly the hooks the
workspace config declares, with no background process spawned behind their
back. The tool's surface matches its identity.

## User Journeys

### Developer creates a worktree after the removal

A developer working in a niwa-managed workspace runs the worktree-create
command to spin up an isolated checkout of a repo for a piece of work.
The command creates the worktree and its branch end-to-end exactly as
before, with no MCP server involved and no background daemon launched. The
developer cannot tell, from the capability's behavior, that an entire
package was deleted underneath it — which is the point.

### Contributor reads the codebase to onboard

A new contributor clones niwa and reads the package tree to understand
what the tool does. Every internal package they open corresponds to a
capability that works: worktree lifecycle, workspace materialization. They
do not encounter an MCP server, a task-delegation substrate, or hook
generators for a mesh that no longer exists, so they never spend time
reverse-engineering dead code or asking which half of the tool is real.

### Operator applies a workspace without surprise side effects

An operator runs `niwa apply` to materialize or update a workspace. The
apply pipeline installs only the hooks the workspace configuration
declares explicitly and starts no background process. The operator's
mental model — apply materializes a workspace and returns — holds, because
the pipeline no longer synthesizes default mesh hooks or spawns a daemon.

## Scope Boundary

### In

- Removal of the MCP server package and its tools, audit subsystem, and
  error-translation layer.
- Removal of the pre-pivot CLI command cluster that wraps the mesh and the
  task-delegation substrate, and unregistration of the orphaned
  subcommands.
- Narrowing the apply pipeline so it no longer synthesizes default mesh
  hooks and no longer spawns background daemons.
- A pre-cursor extraction of the session-lifecycle and worktree-state
  logic into a standalone package so worktree creation survives the
  removal decoupled from the deleted package.
- Renaming the preserved worktree verbs to their final command name, with
  deprecation aliases for the prior name.

### Out

- Backfilling task delegation or any replacement coordination mechanism.
  The removal deliberately ships ahead of any replacement; building the
  replacement is separate downstream work.
- Changes to the workspace-materialization capability (clone, scaffold,
  snapshot, secret provisioning). That subsystem is independent of the
  mesh and stays as-is.
- A general worktree-management feature expansion. This work preserves the
  existing worktree capability under a clean boundary; new verbs or
  policies beyond the rename are not in scope.
