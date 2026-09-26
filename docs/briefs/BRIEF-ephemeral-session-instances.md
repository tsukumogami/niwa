---
schema: brief/v1
status: Done
problem: |
  A developer who runs `claude agents` from a niwa workspace root to fan out
  parallel background sessions has them all share one working tree: the sessions
  collide on branches and files, and there is no clean per-session isolation or
  teardown. Giving each session its own niwa instance by hand (create before,
  destroy after) is manual, easy to forget, and leaves orphaned instances behind.
outcome: |
  A developer fans out background Claude Code sessions from the workspace root and
  each one runs in its own ephemeral niwa instance -- isolated repos, branches, and
  context -- created when the session starts and cleaned up when it ends, with no
  per-session manual setup and no orphaned instances left to reconcile.
motivating_context: |
  A feasibility spike (docs/spikes/SPIKE-ephemeral-session-instances.md) confirmed
  that a SessionStart hook can run `niwa create` and inject the new instance's
  context into a dispatched background session, and that a SessionEnd hook plus a
  reaper can garbage-collect it -- using stock `claude agents` with no Agent SDK.
  This brief frames the feature now that the blocking questions are settled.
---

# BRIEF: one ephemeral niwa instance per Claude Code session

## Status

Done

The downstream PRD owns the requirements; the downstream DESIGN owns the hook and
garbage-collection mechanism. This brief stops at the developer-facing framing.

> **Update note (2026-06-27).** Read "cleaned up when it ends" / "A session finishes
> and its instance disappears" at this altitude: an instance is reclaimed when the
> developer **deletes** the session from the Agent View, not the moment a session
> finishes a task. A completed, idle, or suspended session is still listed and
> resumable and keeps its instance. The refined teardown contract (delete-only,
> reaper-driven) lives in the DESIGN (Decision 6 revision).

## Problem Statement

niwa already lets a developer create multiple instances of one workspace -- full,
independent clones that are meant to be ephemeral. Separately, Claude Code now lets
a developer run `claude agents` and dispatch several background sessions that each
run as a full, independent conversation. Put together, the natural workflow is "fan
out several agents, each on its own isolated copy of the workspace." But nothing
connects the two: dispatched sessions all start in whatever directory `claude
agents` was launched from, so they share one working tree.

Sharing one tree is exactly wrong for parallel agents. They reach for the same
branches, edit the same files, and step on each other's uncommitted work -- the
isolation the developer wanted from separate sessions evaporates at the filesystem.
The workaround is to provision an instance per session by hand: run `niwa create`,
note the path, point the session at it, and remember to `niwa destroy` afterward.
That is manual on every session, easy to forget under fan-out, and the forgotten
half is teardown -- so instances pile up. The developer ends up reconciling
orphaned instances and disk by hand, which is the same "nobody chose this, it just
accumulated" trap niwa exists to remove.

## User Outcome

A developer working from a niwa workspace root can run `claude agents`, dispatch as
many background sessions as the task needs, and trust that each one is already
running in its own ephemeral instance -- its own repos, its own branches, its own
context -- without doing anything per session. Sessions don't collide because they
were never sharing a tree. When a session ends, its instance is torn down
automatically, so fanning out ten agents doesn't leave ten instances to clean up.
The developer thinks in terms of sessions; niwa quietly makes each one a faithful,
disposable workspace and cleans up after it. When a session ends in a way that
skips the clean teardown path, the developer still isn't left with a silent pile:
niwa sweeps the orphan on its own.

## User Journeys

### A developer fans out parallel agents from the workspace root

A developer has a workspace root and wants three agents working different angles of
a problem at once. They run `claude agents` and dispatch three background sessions.
Each session comes up already inside its own instance -- repos cloned, context in
place -- and the three never collide because they were never sharing a tree. The
developer reviews three independent results and never ran `niwa create` once.

### A session finishes and its instance disappears

One of the dispatched sessions completes its task and exits. Its ephemeral instance
is torn down automatically as part of the session ending -- the clone is removed,
niwa's records are updated, and nothing is left for the developer to clean up. The
developer never types `niwa destroy`.

### A session dies without a clean exit

A background session is killed, crashes, or its host drops before it can exit
cleanly, so the normal teardown never runs. Instead of leaving the instance
orphaned forever, niwa later notices the instance has no live session behind it and
reclaims it. The developer's workspace doesn't accumulate dead instances even when
sessions end badly.

### A developer runs an ordinary session at the root

A developer opens a normal Claude Code session at the workspace root to do
something that is not a fan-out -- inspecting config, editing the workspace itself.
That session is not turned into a throwaway instance against their intent; the
ephemeral-instance behavior applies to dispatched worker sessions, not to every
session that happens to start at the root.

## Scope Boundary

### In

- Making each dispatched Claude Code background session in a niwa workspace run in
  its own ephemeral niwa instance, created when the session starts.
- Delivering the new instance's context into the session so the agent operates as
  if it had launched there, without the developer wiring anything up.
- Automatic teardown of a session's instance when the session ends, plus a sweep
  that reclaims instances whose session ended without clean teardown.
- A boundary that keeps ordinary, non-worker sessions at the root from being turned
  into throwaway instances.
- Maintaining the workspace-root configuration niwa owns -- the session hooks, the
  permission posture, and a workspace-root CLAUDE.md -- installed by default when a
  workspace is set up and refreshed on an existing workspace the same way the rest
  of the workspace is refreshed, by running niwa's apply from the root.

### Out

- The technical mechanism -- the exact hook contract, the session-to-instance
  mapping store, the reaper's liveness signal, and the coordinator-vs-worker guard.
  That is downstream DESIGN work.
- What a niwa instance materializes and how its repos and secrets resolve. That
  pipeline already exists and is unchanged by this feature.
- niwa's instance lifecycle commands themselves (`create`, `destroy`) beyond the
  machine-readable output and enumeration the mechanism needs.
- Agent harnesses other than Claude Code.
- Sharing or resuming an instance across more than one session, and cross-machine
  session/instance resume.
- Requirements-level specifics -- exact flag names, config keys, command names, and
  acceptance criteria. Those belong to the downstream PRD.

## References

- docs/spikes/SPIKE-ephemeral-session-instances.md -- the feasibility spike
  establishing that SessionStart/SessionEnd hooks can drive `niwa create` /
  `niwa destroy` for a dispatched background session.
- docs/guides/worktree.md -- niwa's existing per-repo Claude Code hook integration,
  the worktree-level analog this feature mirrors at the instance level.
