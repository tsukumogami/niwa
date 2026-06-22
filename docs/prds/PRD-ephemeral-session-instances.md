---
schema: prd/v1
status: Accepted
problem: |
  Background Claude Code sessions dispatched from a niwa workspace root all share
  one working tree, so parallel agents collide on branches and files. Provisioning
  an instance per session by hand (create before, destroy after) is manual on every
  session and the forgotten teardown leaves orphaned instances to reconcile.
goals: |
  Make each dispatched Claude Code background session run in its own ephemeral niwa
  instance, created on session start and torn down on session end, installed by
  default at the workspace root with no per-session manual setup, and backstopped by
  a sweep so instances whose session ended badly are still reclaimed.
upstream: docs/briefs/BRIEF-ephemeral-session-instances.md
motivating_context: |
  A feasibility spike (docs/spikes/SPIKE-ephemeral-session-instances.md) confirmed
  the SessionStart-hook provision, context injection, and SessionEnd/reaper teardown
  work with stock `claude agents` and no Agent SDK. It also established the load-
  bearing constraints this PRD encodes: SessionEnd's cwd is the launch dir not the
  instance (so teardown must key on a session->instance mapping), SessionEnd is
  best-effort (so a reaper is mandatory), and no native hook field distinguishes the
  coordinator from a worker (so the guard must be engineered).
---

# PRD: one ephemeral niwa instance per Claude Code session

## Status

Accepted

The downstream DESIGN owns the mechanism (hook scripts, the session->instance
mapping store, the reaper's liveness signal, the coordinator-vs-worker guard, and
the supporting niwa primitives). This PRD owns the requirements and the
developer-facing contract.

## Problem Statement

niwa creates multiple ephemeral instances of one workspace, each a full independent
clone. Claude Code's `claude agents` dispatches multiple independent background
sessions. The natural combined workflow is "fan out agents, each on its own
isolated copy of the workspace" -- but nothing connects the two. Dispatched
sessions inherit the directory `claude agents` was launched from, so they all share
one working tree.

A shared tree defeats the isolation the developer wanted from separate sessions:
parallel agents reach for the same branches, edit the same files, and overwrite
each other's uncommitted work. The manual workaround is to run `niwa create`, point
each session at the result, and `niwa destroy` afterward -- per session, every
time. Under fan-out that is both tedious and error-prone, and the step most often
skipped is teardown, so instances accumulate. The developer pays twice: collisions
during the work, then orphan cleanup after. The affected party is every developer
who fans out agents in a niwa workspace, and the cost lands on every fan-out.

## Goals

- A dispatched Claude Code background session in a niwa workspace runs in its own
  ephemeral instance, with no shared working tree across sessions.
- Provisioning and teardown are automatic and on by default at the workspace root,
  with no per-session manual setup.
- Instances are reclaimed even when a session ends without clean teardown, so
  fan-out never leaves a growing pile of orphans.
- Ordinary, non-worker sessions at the root are not turned into throwaway instances
  against the developer's intent.
- niwa stays the system of record for the instances it provisions this way: every
  one is enumerable, and teardown leaves nothing orphaned that the sweep cannot
  find.

## User Stories

- As a developer fanning out background agents from a workspace root, I want each
  dispatched session to come up in its own instance, so that parallel agents don't
  collide on branches and files without my provisioning anything.
- As a developer, I want a session's instance torn down when the session ends, so
  that fanning out many agents doesn't leave many instances to clean up.
- As a developer whose session crashed or was killed, I want niwa to reclaim the
  orphaned instance on its own, so that bad endings don't accumulate dead instances.
- As a developer opening an ordinary session at the root, I want it left alone, so
  that inspecting or editing the workspace itself doesn't spawn a throwaway
  instance.
- As a developer setting up a workspace, I want the session-hook configuration
  installed at the root by default, so that the behavior is there without my wiring
  hooks by hand.
- As a developer with an existing workspace, I want to run `niwa apply` from the
  workspace root to refresh the root's managed configuration and everything beneath
  it, so that I adopt or update the behavior without re-creating the workspace from
  scratch.

## Requirements

Functional:

- **R1.** When a Claude Code background session is dispatched from a niwa workspace
  root, a `SessionStart` hook provisions a fresh niwa instance for that session via
  `niwa create`, before the session does any work.
- **R2.** The provisioning step records a durable mapping from the session's
  identifier to the created instance's path, so teardown can later identify the
  instance independently of the session's reported working directory.
- **R3.** The new instance's context is delivered into the session by hook-injected
  context (not by relocating the session), and the session is directed into the
  instance's directory so its file and command work lands there.
- **R4.** When a session ends, a `SessionEnd` hook tears down that session's
  instance by looking it up through the R2 mapping (keyed on the session
  identifier, never on the hook's reported working directory) and destroying it
  non-interactively.
- **R5.** niwa provides a sweep ("reaper") that reclaims instances whose session
  ended without firing clean teardown, identifying orphans by a liveness signal
  rather than by trusting that `SessionEnd` ran.
- **R6.** Provisioning applies to dispatched worker sessions, not to every session
  that starts at the root: a guard prevents an ordinary or coordinator session from
  being turned into an ephemeral instance. The guard does not rely on a native hook
  field to distinguish session kinds (none exists).
- **R7.** niwa installs the workspace-root managed configuration -- the session
  hooks, the session permission posture (`permissions.defaultMode`), and a
  workspace-root `CLAUDE.md` -- by default when a workspace is initialized, with no
  per-developer manual editing. Because a root-launched session resolves its settings
  and `CLAUDE.md` at launch from the root (the agent's later `cd` into the instance
  does not reload them), this configuration must live at the root for root-launched
  sessions to inherit it.
- **R8.** `niwa apply` is context-aware: it converges the subtree rooted at the
  current scope and never reaches above it or into sibling scopes. At the workspace
  root that subtree is the root-managed configuration and vault plus every instance
  and each instance's worktrees; at an instance, that instance and its worktrees; at
  a worktree, that worktree. `apply` only converges (idempotent, drift-aware) and
  never destroys, and no separate refresh verb is introduced -- the root is just
  another `apply` scope.
- **R9.** `niwa create` exposes a machine-readable form of the created instance's
  path, so the provisioning hook can consume it programmatically rather than parsing
  human output or re-deriving the instance name.
- **R10.** niwa exposes a machine-readable enumeration of a workspace's instances,
  so the reaper can list candidates to evaluate for reclamation.
- **R11.** Instances provisioned this way carry an instance-level liveness/identity
  marker the reaper can use to decide whether the backing session is still alive.
- **R12.** A developer can opt an instance or workspace out of the
  ephemeral-per-session behavior, keeping plain background sessions at the root.
- **R13.** `niwa apply --no-cascade` limits the operation to the current scope's own
  configuration without descending into child scopes -- at the workspace root it
  refreshes only the root-managed configuration without re-converging the instances
  beneath it, for the heavy-op case.
- **R14.** A worktree is a distinct `apply` scope: `niwa apply` run inside a worktree
  converges that worktree, not the whole enclosing instance. This refines today's
  behavior, where running `apply` from within an instance always converges the entire
  instance.

## Acceptance Criteria

- [ ] Dispatching two background sessions from a workspace root yields two distinct
  niwa instances, each on its own clone; neither session's file or branch work
  appears in the other's instance.
- [ ] The session-to-instance mapping is written at session start and names the
  exact instance the session was provisioned with (verifiable from the mapping
  store).
- [ ] Ending a session destroys exactly that session's instance and no other; after
  teardown `niwa`'s instance enumeration no longer lists it and its directory is
  gone.
- [ ] Teardown targets the correct instance even though the `SessionEnd` hook's
  reported working directory is the launch root, not the instance (verifiable by
  ending a session whose agent had `cd`'d into the instance).
- [ ] With a session forced to end without firing `SessionEnd` (e.g. killed), the
  reaper later reclaims the orphaned instance; a reviewer can observe the instance
  present before the sweep and gone after.
- [ ] Launching `claude agents` at the root (the coordinator) and/or opting out does
  not create an ephemeral instance for that non-worker session.
- [ ] `niwa create`'s machine-readable output yields the created instance path with
  no human-text parsing, and the provisioning hook consumes it.
- [ ] niwa's instance enumeration lists every instance under the workspace in a
  machine-readable form the reaper consumes.
- [ ] The hooks install at the workspace root through a non-interactive workspace
  setup with no TTY attached.
- [ ] Running `niwa apply` from the workspace root updates the root's managed
  configuration (hooks, permission posture, and the root `CLAUDE.md`) to the current
  form and cascades into the instances and worktrees beneath it, without destroying
  any instance.
- [ ] `niwa apply --no-cascade` at the workspace root refreshes only the root-managed
  configuration and does not re-converge the instances beneath it.
- [ ] `niwa apply` run inside a worktree converges that worktree and not its sibling
  worktrees or the rest of the enclosing instance.
- [ ] The whole flow requires no agent harness other than Claude Code: with only
  Claude Code present, all of the above hold.

## Decisions and Trade-offs

- **Teardown keys on the session->instance mapping, not on cwd (R2, R4).** The spike
  established that `SessionEnd`'s reported cwd is the launch root, not the instance
  (the agent's `cd` moves only the Bash tool's directory, not the session cwd). A
  mapping written at `SessionStart` is the only reliable handle. Alternative
  considered: destroy whatever `cwd` reports -- rejected, it would destroy the
  workspace root.
- **A reaper is mandatory, not optional (R5).** `SessionEnd` is best-effort; the
  spike saw a session fire none. Relying on it alone guarantees orphans on crash,
  kill, or host drop. Alternative considered: SessionEnd-only teardown -- rejected
  as silently lossy. Trade-off accepted: a background sweep is extra machinery, but
  it is the only thing that makes "no orphans" a real guarantee.
- **The coordinator-vs-worker guard is engineered, not read from a hook field
  (R6).** The spike confirmed the coordinator and workers are indistinguishable in
  hook stdin (both `source:startup`, `agent_type:claude`), and that the coordinator
  spuriously created an instance. The guard must come from something niwa controls
  (e.g. a marker the worker carries, an already-inside-an-instance no-op, or an
  explicit opt-in); the exact mechanism is a design choice. Alternative considered:
  match on `source`/`agent_type` -- rejected, no field discriminates.
- **Context arrives by injection, not relocation (R3).** Mid-session `cd` does not
  reload `CLAUDE.md`/context; the instance's context must be injected at
  `SessionStart`. Alternative considered: rely on the agent re-rooting into the
  instance to pick up its context -- rejected, the spike showed `cd` does not
  re-root the session.
- **The workspace root becomes a managed surface, refreshed by context-aware `niwa
  apply` (R7, R8, R13, R14).** The root now hosts the session hooks, the permission
  posture, and a root `CLAUDE.md`, so it needs a non-destructive refresh path. Rather
  than a new verb, `apply` is made context-aware: it converges the subtree at the
  current scope (root, instance, or worktree) and never climbs above it, with
  `--no-cascade` to cap the operation at the current scope for heavy ops. This adds
  the worktree as a distinct scope, which refines today's behavior (where `apply` from
  anywhere inside an instance converges the whole instance) -- an intentional,
  pre-1.0 semantics change for a uniform "converge my subtree" model. Alternatives
  considered: a dedicated `niwa refresh` command (rejected -- the root is just another
  `apply` scope, and a second verb would drift from it); a `--root-only` flag scoped
  to the root (rejected -- `--no-cascade` is its general form, meaningful at every
  scope); and hand-edited root settings (rejected -- manual editing is the setup this
  feature removes).
- **The permission posture is ordinary root config, not a special gate (R7).** The
  `permissions.defaultMode` block is materialized at the root by the same
  `buildSettingsDoc` path that emits the hooks, governed by the same opt-in mode --
  no separate mechanism. Consequence (see Known Limitations): a root-level bypass
  posture applies to every session launched at the root, not only dispatched workers.
- **`niwa create` needs machine-readable output (R9).** Today it does not emit the
  instance path in a stable machine-readable form; the hook needs it
  programmatically. The exact form (a `--json` mode or a documented stable line) is
  a design choice.

## Known Limitations

- The feature depends on Claude Code firing `SessionStart`/`SessionEnd` for
  dispatched background sessions. On a harness or version that does not, the
  provisioning and clean-teardown paths do not engage; the reaper still bounds
  orphan accumulation but the per-session-instance behavior degrades.
- Teardown on clean exit is best-effort by nature; the reaper is the backstop, so
  an instance may outlive its session until the next sweep.
- Instance build cost is real (a full clone per session). This PRD takes that cost
  as accepted for the isolation it buys and does not require a cheaper instance
  primitive.
- The coordinator-vs-worker guard is heuristic by necessity (no native
  discriminator); an unusual launch pattern could misclassify, which the opt-out
  (R12) exists to escape.
- When the workspace opts into bypass permissions, the posture lives at the root and
  so applies to every session launched there -- the coordinator and any ordinary
  root session, not only dispatched workers -- because settings resolve at launch and
  neither the hook nor dispatch can scope permission mode per session. This is a
  wider grant than per-instance bypass; the opt-in mode (R12) bounds it to workspaces
  that chose it.

## Out of Scope

- The mechanism's technical design -- the exact hook contract, the mapping store's
  shape, the reaper's liveness signal and schedule, and the guard's implementation.
  Downstream DESIGN.
- What a niwa instance materializes and how its repos and secrets resolve. That
  pipeline exists and is unchanged here.
- The internals of `niwa create` / `niwa destroy` beyond R9's machine-readable
  output, R10's enumeration, and R11's liveness marker.
- Agent harnesses other than Claude Code.
- Sharing or resuming one instance across multiple sessions, and cross-machine
  session/instance resume.
- A cheaper-than-full-clone instance primitive.

## References

- docs/briefs/BRIEF-ephemeral-session-instances.md -- the framing this PRD
  operationalizes.
- docs/spikes/SPIKE-ephemeral-session-instances.md -- feasibility findings and the
  load-bearing constraints (mapping-not-cwd, best-effort SessionEnd, no native
  worker discriminator).
- docs/guides/worktree.md -- niwa's existing per-repo Claude Code hook integration,
  the worktree-level analog of this instance-level feature.
