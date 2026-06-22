---
status: In Progress
problem: |
  `niwa worktree create` materializes a worktree's environment through a
  worktree-specific secret-resolution path that re-resolves secrets from their
  source at create time. That path is separate from the instance apply
  pipeline and fails where the instance does not (an unreachable or
  wrong-organization secret source, a provider reference it assembles
  differently), even though the instance already holds a complete, correct
  environment for the same repo.
goals: |
  A worktree inherits the instance's already-materialized environment instead
  of resolving its own, so creation is as reliable as the instance and needs
  no secret-source round-trip. `niwa apply` becomes the single action that
  refreshes every clone and worktree in an instance through the same logic, so
  a worktree can never hold a different value than its clone.
upstream: docs/briefs/BRIEF-worktree-env-provisioning.md
motivating_context: |
  A real `niwa worktree create` failed mid-run on an Infisical 403 from a
  fallback credential session, and separately on an unassembled vault provider
  reference. Both traced to worktree creation resolving secrets on a path that
  is not the instance apply path -- when it should not be resolving at all.
---

# PRD: worktree environment provisioning

## Status

In Progress

## Problem Statement

A niwa workspace instance, once applied, holds a complete environment for each
repo it manages: every variable and secret is resolved and written to that
repo's materialized env output file(s). A worktree is a second working
checkout of one of those repos, created under the same instance.

When a developer runs `niwa worktree create`, niwa does not hand the worktree
the environment the instance already produced. It runs a worktree-specific
materialization path that re-reads config and re-resolves `vault://` secrets
from their source at create time. That path is the problem.

It fails where the instance does not. Re-resolving secrets at create time
depends on the secret source being reachable and correctly authenticated at
that moment -- a condition the instance satisfied when it was applied, but
which need not hold later: a credential session scoped to the wrong
organization, an unreachable vault, or a provider reference the worktree path
assembles differently than the instance path. When that trips, worktree
creation fails -- even though a complete, correct environment for that exact
repo already exists in the instance the worktree was created from. This bites
hardest in automated and agent-driven contexts, where the shell that runs
`niwa worktree create` may not carry the same credential session the original
`niwa apply` ran under.

The affected users are anyone creating worktrees: developers branching off for
a task, operators on machines without a live secret-source session, and
automated workflows (niwa-mesh agents) that create worktrees programmatically.
The cost is a worktree that cannot be created at all, or one created with a
broken environment, despite a working instance sitting alongside it.

## Goals

- Worktree creation produces a working environment by inheriting what the
  instance already materialized, with no secret-source resolution at create
  time -- so it succeeds whenever the instance's environment is present.
- A clone and a worktree of the same repo always carry the same environment;
  the two cannot hold different values for the same key.
- `niwa apply` is the single refresh action: one run re-materializes every
  clone and worktree in the instance, so propagating a rotated secret or a
  config change is one familiar command.
- A worktree's environment is reliably consistent with its instance; a
  developer never has to reason about per-worktree drift.

## User Stories

- As a developer using an AI coding agent, I want `niwa worktree create` to
  succeed using my workspace's existing environment, so that agent-driven
  worktrees do not fail on secret-source authentication the workspace already
  handled.

- As an operator on a machine without a live secret-source session, I want to
  create a worktree from an already-applied instance, so that I can work
  without re-authenticating to the vault.

- As an operator rotating a secret, I want `niwa apply` to refresh worktrees
  alongside clones, so that one command propagates the new value everywhere in
  the instance.

- As a maintainer, I want a worktree's environment to always match its
  instance, so that I never debug a discrepancy between a worktree and its
  clone.

## Requirements

### Functional

- **R1.** `niwa worktree create` MUST materialize the worktree's environment by
  inheriting the instance's already-resolved environment for the same repo. It
  MUST NOT perform live secret resolution (no vault or other secret-source
  round-trip) at create time.

- **R2.** The environment a worktree receives MUST be equivalent to the
  instance clone's environment for the same repo and effective config: the same
  keys with the same values, written to the same set of output target files in
  the same formats.

- **R3.** The worktree's secret-output target set (file paths and formats) MUST
  be resolved from the same effective configuration the instance uses
  (per-repo, then workspace, then personal/global precedence). Combined with
  R2, this means a worktree honors custom target names, multiple targets, and
  non-dotenv formats exactly as the clone does.

- **R4.** `niwa worktree create` MUST NOT fail because the secret source is
  unreachable or the ambient credential/authentication state is wrong, when the
  instance's environment for the repo is already materialized.

- **R5.** `niwa worktree apply` MUST follow the same inherit behavior as
  `niwa worktree create` (R1-R4): re-syncing a worktree refreshes its
  environment from the instance's materialized environment, not from a live
  resolution.

- **R6.** `niwa apply` MUST refresh the environment of the instance's existing
  worktrees in the same run that refreshes the clones, using the shared
  materialization, so that after an apply no worktree holds a different value
  than its clone for any key.

- **R7.** While refreshing worktrees, `niwa apply` MUST tolerate worktrees that
  cannot be refreshed (locked, detached, or with a missing working directory):
  it skips them with a warning identifying the worktree and continues, rather
  than failing the apply.

- **R8.** When `niwa worktree create` is invoked for a repo whose environment
  the instance has not yet materialized (for example, env was enabled after the
  last apply), niwa MUST exit with a non-zero status and an error that directs
  the user to run `niwa apply`, rather than resolving secrets itself or writing
  an empty environment.

### Non-functional

- **N1.** The worktree environment step MUST complete with no network access;
  creating a worktree offline (with the instance already applied) succeeds.

- **N2.** A worktree's environment MUST stay equivalent to its instance clone's
  across every operation that touches it -- `niwa worktree create`, `niwa
  worktree apply`, and `niwa apply` -- so no sequence of supported operations
  leaves the worktree and its clone holding different values for the same key.

- **N3.** Worktree environment output files MUST retain the instance's secrecy
  posture: file permissions and git-ignore coverage match the clone's
  (no secret content becomes world-readable or git-visible as a result of
  inheritance).

## Acceptance Criteria

- [ ] With the secret source unreachable or logged into the wrong organization,
  `niwa worktree create` succeeds and the worktree's env output file(s) are
  byte-identical to the instance clone's for that repo.
- [ ] `niwa worktree create` completes successfully with no network access when
  the instance has already been applied.
- [ ] For a repo configured with a non-default or multiple secret-output targets
  (for example `secrets.json` plus a custom-named dotenv), the worktree receives
  the same target files in the same formats as the clone.
- [ ] After changing a secret's value at the source and running `niwa apply`,
  both the clone and a pre-existing worktree of the repo show the new value.
- [ ] `niwa apply` completes successfully and emits a warning naming a worktree
  that is locked or whose directory is missing, without failing the run.
- [ ] `niwa worktree create` for a repo with env configured but not yet
  materialized by the instance exits non-zero with a message naming `niwa
  apply`.
- [ ] Creating a worktree of a repo that has a `vault://` secret performs no
  call to the secret provider (verified by provider-call trace or offline run).
- [ ] `niwa worktree apply` on an existing worktree re-syncs its environment
  from the instance's materialized env with no secret-source call, and the
  result matches the clone (R5).
- [ ] A worktree's env output files have the same file permissions and the same
  git-ignore coverage as the clone's: no env file is world-readable and none
  appears in `git status` for the worktree (N3).
- [ ] Across the ordered sequence create-the-worktree, then rotate a secret,
  then `niwa apply`, the worktree ends with the rotated value and equals the
  clone -- exercising the cross-operation equivalence invariant as a chain, not
  just per-operation (N2).

## Decisions and Trade-offs

These close the three questions the upstream BRIEF deferred to this PRD.

- **D1 -- `niwa apply` refreshes worktrees automatically.** `niwa apply`
  enumerates the instance's live worktrees and refreshes each through the shared
  materialization (R6), rather than leaving worktrees to a manual `niwa worktree
  apply`. *Alternative considered:* leave worktree refresh entirely manual.
  *Why chosen:* the goal is one-command, instance-wide consistency; the manual
  alternative reintroduces exactly the clone/worktree drift this work removes.
  *Trade-off:* `niwa apply` does more work and must handle worktree edge states
  gracefully (R7).

- **D2 -- create errors rather than resolves when there is nothing to inherit.**
  When the instance has not materialized a repo's environment, `niwa worktree
  create` errors and points at `niwa apply` (R8). *Alternative considered:* a
  one-time bootstrap resolution at create time for this case. *Why chosen:* it
  preserves the core invariant that worktree create never resolves secrets; a
  single resolution entry point (apply) is easier to reason about than a
  conditional one. *Trade-off:* one extra step in the uncommon case where env
  was enabled after the last apply.

- **D3 -- worktrees mirror their instance; no per-worktree divergence (v1).**
  A worktree always carries the same environment as its instance clone; niwa
  offers no surface to give a worktree intentionally different values.
  *Alternative considered:* a per-worktree env override surface. *Why chosen:*
  the BRIEF scoped intentional divergence out; the mirror is what makes
  inheritance and the apply-refresh contract coherent. *Trade-off:* a future
  genuine need for per-worktree values would require a new feature.

## Out of Scope

- **Intentional per-worktree environment divergence.** Per D3, a worktree
  mirrors its instance; no override surface is provided.
- **The instance secret-resolution internals** -- credential/token handling,
  multi-org auth, overlay vault merging, provider bundle assembly. This work
  routes worktree materialization through that existing logic; it does not
  change how the instance resolves secrets.
- **Repairing a misconfigured external secret-source session** (for example a
  vault CLI logged into the wrong organization). That is an environment and
  credential-configuration concern, outside niwa's worktree contract.
- **Non-environment worktree content** -- CLAUDE.md content, the
  workspace-context rules import, the worktree-specific layer, and worktree
  hooks. Settled by the worktree command parity design.
- **Multi-repo or cross-repo coordination behavior.**
