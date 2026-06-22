---
schema: brief/v1
status: Draft
problem: |
  A worktree's environment is re-derived from scratch instead of inherited
  from the workspace instance it belongs to. The re-derivation can fail even
  when the instance's own environment is complete and working, and niwa has
  never decided whether a worktree's environment should match its instance or
  be allowed to differ.
outcome: |
  A developer creates a worktree and its environment matches the workspace
  instance it came from -- same variables, same values -- without the worktree
  needing live vault access or re-authentication at create time. Worktree
  creation stops failing for environment reasons the instance already solved.
motivating_context: |
  A real `niwa worktree create` failed mid-run -- once on an Infisical 403
  from a fallback credential session, once on a vault provider reference the
  worktree path did not assemble. Both traced back to the same gap: the
  worktree path re-resolves environment and secrets through a code path that
  is not the one the instance apply uses, and nobody had decided whether a
  worktree's environment should be a fresh derivation at all.
---

# BRIEF: worktree environment provisioning

## Status

Draft

The brief stops at framing. The downstream PRD owns the requirements
(which provisioning model wins, the freshness contract, whether intentional
per-worktree divergence is supported); the downstream DESIGN owns the
architecture.

## Problem Statement

A niwa workspace instance, once applied, holds a complete and working
environment: every variable and secret is resolved and written to each
repo's materialized env file. A worktree is a second working copy of one of
those repos, created under the same instance.

When a developer creates a worktree, niwa does not hand it the environment
the instance already established. Instead it re-derives the worktree's
environment from scratch -- re-reading config, re-resolving secrets from
their source. That re-derivation is the problem on two counts.

First, it can fail even when the instance's environment is complete and
working. Re-resolving secrets at worktree-create time depends on the secret
source being reachable and correctly authenticated *at that moment* -- a
condition the instance already satisfied when it was applied, but which may
not hold later (a credential session in the wrong organization, an
unreachable vault, a network gap, a provider reference the worktree path
assembles differently than the instance path does). When the re-derivation
trips on any of these, worktree creation fails -- even though a complete,
correct environment for that exact repo is sitting in the instance the
worktree was created from.

Second, and underneath the failure: niwa has never decided what a worktree's
environment *should* be. Should it be an exact inheritance of the instance's
environment? A fresh derivation that may legitimately differ? Something in
between? Because the question was never settled, the behavior is whatever the
code path happens to do, and it can drift from the instance silently. A
developer cannot reason about whether their worktree's environment matches
their workspace, because no contract says it should.

The two halves compound. The fragile re-derivation is the symptom a developer
hits; the undecided provisioning model is why the symptom keeps recurring in
new forms as the code evolves.

## User Outcome

A developer who creates a worktree gets an environment that matches the
workspace instance it was created from -- the same variables with the same
values -- and gets it without the worktree reaching out to the secret source
or re-authenticating. Creating a worktree "just works" the way the workspace
it came from already works: if the instance can build, test, and authenticate,
so can its worktrees, immediately and without a fresh round-trip to a vault.

When live secret resolution genuinely is not available at worktree-create
time, the developer still gets a usable worktree from what the instance
already materialized, rather than a hard failure.

And a maintainer who asks "should a worktree's environment ever differ from
the main clone?" finds a written answer in the project's durable record,
rather than having to read it out of the current behavior of a code path.
The provisioning model becomes a decision the project owns, not an accident
of implementation.

## User Journeys

### A developer opens a worktree to start a task

A developer (or an AI coding agent acting as one) is working in a fully
provisioned workspace and needs an isolated branch for a new task. They run
`niwa worktree create`. The worktree opens carrying the same environment the
workspace has -- the GitHub token, the API keys, every value -- and they start
work immediately. No authentication prompt, no secret-source round-trip, no
failure partway through creation.

### An operator creates a worktree where live secrets are not reachable

An operator creates a worktree on a machine where the secret source is not
reachable at that moment -- the credential session is scoped to the wrong
organization, the network is down, or the vault is temporarily unavailable.
They run worktree create. Because the instance the worktree belongs to already
materialized a complete environment, creation succeeds from that materialized
state instead of hard-failing on a live resolution it does not actually need.

### A maintainer reasons about an intentional per-worktree difference

A maintainer wonders whether a worktree can deliberately carry a different
value for one variable than the main clone -- pointing a worktree at a scratch
database, say. They consult the project's documentation and durable design
record. They get a definite answer: either the provisioning model supports
intentional per-worktree differences (and the downstream design defines how),
or it deliberately does not and a worktree always mirrors its instance. Either
way they can reason about it instead of guessing from observed behavior.

### A contributor finds a worktree carrying a stale secret

A contributor notices a worktree still holds the old value of a secret that
was rotated after the worktree was created. They want to know how to refresh
it. The provisioning model gives them a defined action -- re-apply the
instance, re-apply the worktree, or whichever the model specifies -- that
brings the worktree's environment back in line with the source of truth. The
freshness behavior is a documented contract, not an open question they have
to reverse-engineer.

## Scope Boundary

### In scope

- Deciding the worktree environment provisioning model: whether a worktree
  inherits the instance's already-materialized environment, re-derives it from
  the secret source, or combines the two.
- The environment- and secret-materialization behavior of
  `niwa worktree create` and `niwa worktree apply`.
- Graceful behavior when live secret resolution is unavailable at
  worktree-create time, so a complete instance environment is enough to
  provision a worktree.
- A durable, written answer to "when, if ever, should a worktree's environment
  legitimately differ from the main clone?"

### Out of scope

- The instance-apply secret-resolution pipeline itself (credential/token
  handling, overlay vault merging, provider bundle assembly). That path
  already works for instance apply; this feature decides what a worktree does
  with the result, not how the instance produces it.
- Repairing a misconfigured external secret-source session (for example, a
  vault CLI logged into the wrong organization). That is an environment and
  credential-configuration concern, not part of niwa's worktree contract.
- The concrete configuration syntax for any intentional per-worktree
  environment override. If the chosen model supports deliberate divergence,
  designing that surface is downstream PRD/DESIGN work; this brief only decides
  whether deliberate divergence is a goal.
- Non-environment worktree content -- the CLAUDE.md content, the
  workspace-context rules import, the worktree-specific layer, and worktree
  hooks. That content payload is already settled by the worktree command
  parity design.
- Multi-repo or cross-repo coordination behavior.

## Open Questions

These defer framing details to the downstream PRD; none is a blocker that
should stop the brief.

- Which provisioning model the PRD adopts -- inherit the instance's
  materialized environment, re-derive from source, or a hybrid that prefers
  inheritance and falls back to a fresh derivation.
- Whether intentional per-worktree environment divergence is a real user need
  worth supporting, or whether a worktree should always mirror its instance.
- The freshness contract: how, and by which command, a worktree picks up a
  secret that rotated after the worktree was created.
- Whether `niwa worktree create` and `niwa worktree apply` should share
  identical environment behavior or differ deliberately.

## References

- `docs/designs/current/DESIGN-worktree-command-parity.md` -- established what
  content a worktree receives and that worktree apply reuses the instance
  installers; its Decision C chose to run the repo materializers (including
  env) against the worktree but did not settle the secret-resolution sourcing
  model this brief frames.
- `docs/designs/current/DESIGN-secret-output-targets.md` -- defines the
  materialized environment output the instance already produces, which the
  inheritance option would draw on.
- `docs/designs/current/DESIGN-vault-multi-org-auth.md` -- the instance
  secret-resolution machinery this brief deliberately leaves out of scope.
