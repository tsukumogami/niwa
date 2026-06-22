---
schema: brief/v1
status: Accepted
problem: |
  Worktree creation runs its own secret-resolution path, separate from and
  divergent from the instance apply pipeline. That fork is why it breaks, and
  it is structurally wrong: resolving and materializing an environment is what
  `niwa apply` already does for a clone, and a worktree is just another
  checkout of the same repo. A worktree should inherit the environment the
  instance already produced, not resolve its own.
outcome: |
  Creating a worktree never resolves secrets on its own -- it inherits the
  environment the instance already materialized, so it is as reliable as the
  instance. Refreshing an environment is `niwa apply`, which re-materializes
  every clone and worktree the same way.
motivating_context: |
  A real `niwa worktree create` failed mid-run -- once on an Infisical 403
  from a fallback credential session, once on a vault provider reference the
  worktree path did not assemble. Both traced back to the same root: worktree
  creation re-resolves secrets through a code path that is not the one the
  instance apply uses, when it should not be resolving secrets at all.
---

# BRIEF: worktree environment provisioning

## Status

Accepted

The brief stops at framing. The downstream PRD owns the requirements (how
`niwa apply` reaches worktrees, what create does before a first apply); the
downstream DESIGN owns the architecture (where the shared resolution logic
lives, the import-cycle handling).

## Problem Statement

A niwa instance, once applied, holds a complete environment: every variable
and secret is resolved and written to each repo's materialized env output. A
worktree is a second checkout of one of those repos, created under the same
instance.

Worktree creation does not hand the worktree that environment. It runs a
secret-resolution path of its own -- re-reading config and re-resolving
`vault://` secrets from their source at create time. That separate path is
the problem, on two levels.

On the surface, it fails where the instance does not. Resolving secrets at
create time depends on the secret source being reachable and correctly
authenticated at that moment -- a condition the instance satisfied when it
was applied, but which need not hold later: a credential session scoped to
the wrong organization, an unreachable vault, or a provider reference the
worktree path assembles differently than the instance path does. When that
trips, worktree creation fails -- even though a complete, correct environment
for that exact repo already exists in the instance the worktree was made
from.

Underneath: there should not be a second resolution path at all. Resolving
and materializing an environment is what `niwa apply` does for a clone, and a
worktree is just another checkout of the same repo -- so the same logic
should produce its environment too. Instead the logic is forked into a
worktree-specific path, and the fork drifts from the original: each change to
the resolution pipeline risks the two paths diverging again, which is exactly
the shape of the failures above. Nothing in a worktree's environment is even
worktree-specific to recompute -- the materializer expands no path or context
variables into env values, so a worktree's correct environment is identical
to its clone's. There is nothing a separate resolution could produce that the
instance has not already produced.

## User Outcome

A developer who creates a worktree gets an environment that matches the
workspace instance it came from -- the same variables, the same values -- and
creating the worktree is as reliable as the instance already is. If the
instance builds, tests, and authenticates, so do its worktrees, the moment
they are created, with no separate provisioning step that can fail on its
own and no round-trip to a secret source.

Refreshing an environment -- after a secret rotates or config changes -- is a
single, familiar action: `niwa apply`. One command re-materializes every
clone and every worktree in the instance through the same logic, so nothing
is left stale and the two can never hold different values for the same key.
A developer carries one mental model: `apply` resolves and refreshes
everything; creating a worktree inherits what `apply` already produced.

## User Journeys

### A developer opens a worktree to start a task

A developer (or an AI coding agent acting as one) is working in a fully
provisioned workspace and needs an isolated branch for a task. They run
`niwa worktree create`. The worktree opens carrying the same environment the
instance has -- the GitHub token, the API keys, every value -- and they start
immediately. No authentication step, no secret-source round-trip, no failure
partway through creation, because nothing is being resolved: the worktree
inherits what the instance already produced.

### A developer refreshes secrets across the whole instance

A secret rotates, or someone changes the workspace config. The developer runs
`niwa apply`. The same resolution that refreshes the main clones also
refreshes every worktree under the instance, by the same logic -- so a
worktree never drifts from its clone and there is no separate worktree-only
refresh command to remember. The action that fixes a stale environment is the
action they already know.

### An agent creates worktrees as part of an automated workflow

An automated workflow (for example, a niwa-mesh agent picking up delegated
tasks) creates worktrees programmatically, possibly several in quick
succession. Because creation inherits the instance's environment rather than
authenticating to a secret source, the fan-out is reliable: it does not
multiply vault round-trips, cannot fail on a credential session the agent's
environment does not carry, and produces worktrees whose environments are
consistent with the instance and with each other.

## Scope Boundary

### In scope

- Worktree creation inherits the instance's already-materialized environment;
  it does not resolve secrets from the source at create time.
- Unifying environment resolution and materialization under the apply pipeline
  so a clone and a worktree of the same repo are produced by the same logic.
- `niwa apply` refreshing worktrees alongside clones, so one command keeps the
  whole instance consistent.

### Out of scope

- Intentional per-worktree environment divergence. A worktree mirrors its
  instance; deliberately giving a worktree different values from its clone is
  not a goal of this work.
- The instance secret-resolution internals -- credential/token handling,
  overlay vault merging, provider bundle assembly. This work routes worktrees
  through that existing logic; it does not redesign it.
- Repairing a misconfigured external secret-source session (for example, a
  vault CLI logged into the wrong organization). That is an environment and
  credential-configuration concern, not part of niwa's worktree contract.
- Non-environment worktree content -- the CLAUDE.md content, the
  workspace-context rules import, the worktree-specific layer, and worktree
  hooks. That payload is already settled by the worktree command parity
  design.
- Multi-repo or cross-repo coordination behavior.

## References

- `docs/designs/current/DESIGN-worktree-command-parity.md` -- established what
  content a worktree receives and that worktree apply reuses the instance
  installers; its Decision C ran the env materializers against the worktree
  but did not settle that a worktree should inherit rather than re-resolve --
  the gap this brief frames.
- `docs/designs/current/DESIGN-secret-output-targets.md` -- defines the
  materialized environment output the instance produces and that a worktree
  would inherit; the output targets are config-resolved, so inheriting is
  more than copying a single fixed filename.
- `docs/designs/current/DESIGN-vault-multi-org-auth.md` -- the instance
  secret-resolution machinery this brief routes worktrees through rather than
  duplicating.
