# /prd Scope: mesh-session-lifecycle

## Problem Statement

Niwa mesh creates a fresh Claude session for every `niwa_delegate` call, which
discards conversation context between sequential task delegations to the same
repo agent. Coordinators running multi-step workflows (e.g., `/shirabe:design`
followed by `/shirabe:plan` in the same repo) fail because the second agent starts
blind. Separately, the main clone of each repo gets stranded on a feature branch
when work switches focus, because `niwa apply` only pulls repos that are clean and
on the default branch — leaving no automated path back to `main`. These two problems
have a common fix: coordinator-managed sessions, where each session anchors a
persistent Claude context to a git worktree, keeping the main clone permanently
on `main`.

## Initial Scope

### In Scope

- Session lifecycle: coordinator creates a named session for a repo, delegates
  multiple tasks within it (sharing Claude context), and ends the session when done
- Worktree-based anchoring: each session gets its own git worktree, keeping the
  main clone always on `main`
- Session persistence across task boundaries: workers within a session resume the
  same Claude conversation, not a fresh one
- Parallel sessions: a single coordinator can run multiple independent sessions for
  the same repo simultaneously (e.g., two features in parallel)
- Session cleanup guards: niwa blocks cleanup of sessions with unpushed commits;
  `pending_merge` state for sessions with an open PR
- Session registry: coordinator can list active sessions and query session state;
  survives coordinator compaction (context window reset)
- Non-mesh session management: `niwa session start` / `niwa session end` CLI
  commands for non-mesh users who want the always-clean-main benefit without a
  coordinator
- Backward compatibility: existing workspaces using the current single-checkout
  model continue to work unchanged; sessions are opt-in

### Out of Scope

- Session handoff between coordinators (follow-on; needs coordinator identity
  design first)
- Session→PR lifecycle tracking (follow-on; `pr_url` field reserved in schema but
  not surfaced in V1 UX)
- Session summary for compacted coordinators (`niwa_session_summary` tool, follow-on)
- Session audit history and `niwa_session_history` tool (follow-on)
- Changes to the task delegation protocol (envelope format, inbox atomics)
- Install/recipe logic

## Research Leads

1. **What does the coordinator experience when managing a session?** What does it
   call to start a session, how does it pass the session to subsequent task delegations,
   and what does it see when it lists sessions? Define the full coordinator workflow
   from session creation to cleanup, with concrete MCP tool signatures.

2. **What does "session continuity" guarantee to the worker?** When a coordinator
   delegates task B within a session that already ran task A, what exactly does the
   worker see? Full task A conversation context? Or just task A's result? Define the
   contract between session and worker, including how `--resume` and fresh spawns
   interact with session continuity.

3. **What are the session lifecycle state transitions and their triggers?** Define
   `active → pending_merge`, `active → ended`, `active → abandoned`, and any
   transition guards (e.g., unpushed commits blocking `ended`). Include who triggers
   each transition: coordinator, niwa daemon, or user?

4. **What does the non-mesh session UX look like?** A non-mesh user running
   `niwa session start <repo>` — what happens? Does niwa create the worktree? Start
   a daemon? Just create the git worktree and let the user manage Claude manually?
   Define the minimum viable non-mesh session experience.

5. **What should niwa enforce vs. leave to coordinator skills?** Niwa can enforce
   worktree creation, session state persistence, cleanup guards, and routing. Coordinator
   skills document the workflow (start session before delegating, end session after
   pushing). Where is the line? What happens if a coordinator delegates without a
   session — does niwa create an implicit anonymous session or fail?

## Coverage Notes

The exploration did not fully resolve:

- **niwa_ask routing across worktree daemons**: Workers in session worktrees need to
  reach the coordinator. Currently `lookupLiveCoordinator` reads from the main instance's
  `sessions.json`. The PRD should specify whether the shared coordinator registration
  lives in the main clone's `.niwa/sessions/sessions.json` (accessible to all worktrees)
  or needs a new cross-instance lookup mechanism.

- **Session orphan recovery**: If a coordinator crashes, worktrees are left in an
  unknown state. The PRD should specify what a newly started coordinator should do when
  it discovers orphaned sessions: reclaim, ignore, or surface to the user?

- **Scope of `niwa apply` changes**: With worktrees as the session model, does `niwa apply`
  need a new mode (e.g., `niwa apply --session`) or does it continue to apply only to the
  main clone and leave worktrees to session lifecycle management?

## Decisions from Exploration

- **Skip standalone session ID threading**: Threading a session ID through `niwa_delegate`
  without worktrees would fix context loss cheaply but not dirty workspace or parallel
  sessions, and creates migration debt. The full session model subsumes session continuity.

- **Design for universal scope, implement mesh-first**: Non-mesh commands are already
  layout-agnostic. Non-mesh users have the same stranded-branch problem. The PRD should
  not artificially scope to mesh-only; the first implementation target is mesh.

- **Per-worktree daemon model**: Each session worktree is its own niwa instance with its
  own daemon. Maps onto the existing "one daemon per instance" model. A shared sessions
  registry in the main clone enables `niwa_ask` routing across worktree daemons.

- **Four session lifecycle states**: `active`, `pending_merge`, `ended`, `abandoned`.
  `pending_merge` prevents premature cleanup when a PR is open but not merged.
  `abandoned` is the force-end path for explicitly discarded unpushed work.

- **Reserve follow-on extension points in SessionState**: `pr_url` field and
  `coordinator_session_id` in `SessionEntry` reserved from the start; zero-cost and
  unlocks high-value follow-ons without schema migration later.

- **Session summary on-demand**: `niwa_session_summary` queries existing task state at
  call time; avoids stale data from pre-materialization.
