# Explore Scope: mesh-session-lifecycle

## Visibility

Public

## Core Question

Should niwa mesh support coordinator-managed session lifecycles, where multiple
sequential tasks share a Claude context, and is git worktree the right physical
anchoring model? This touches two overlapping problems: task-scoped sessions
discard context between delegations (design → plan fails), and the main clone of
each repo gets stranded on a feature branch when work switches repos, because
`niwa apply` only merges clean.

## Context

The coordinator already passes session IDs for stall recovery (`claude --resume
<id>`), but there is no mechanism for the coordinator to keep a session alive
across task boundaries. Each `niwa_delegate` call currently spawns a fresh Claude
session. The user confirmed a concrete failure: a coordinator delegated `/shirabe:design`
and then `/shirabe:plan` to the same repo agent; the second task started from a
blank session and could not access the design work.

The proposed model: keep the main clone always on `main`, give each
coordinator-initiated session its own worktree, and thread the Claude session
across tasks within that session window. The coordinator determines when a session
starts and ends; niwa enforces the lifecycle and guards against cleanup before
work is pushed.

## In Scope

- Session lifecycle API design (coordinator-side start/end)
- Worktree-based physical anchoring for sessions
- Compatibility audit of workspace commands under a worktree layout
- How `.niwa/` mesh state interacts with worktrees
- Coordinator UX for session management
- Backward compatibility for non-mesh workspaces
- Follow-on features enabled by persistent sessions

## Out of Scope

- Task delegation protocol internals (envelope format, inbox atomics)
- Install/recipe logic
- Coordinator session memory (CLAUDE.md, memory files) — that's a separate concern

## Research Leads

1. **What does `niwa go`, `niwa apply`, and the wider command set assume about
   folder structure, and which assumptions break under a worktree layout?**
   Need a full inventory before committing to worktrees as the anchoring model.
   If the breakage surface is large, a different anchoring approach may be
   preferable.

2. **How does git worktree interact with `.niwa/` mesh state?**
   `.niwa/` lives in the repo root; with multiple worktrees for the same repo,
   which worktree owns the inbox, tasks, hooks, and settings? Does each worktree
   get its own `.niwa/` or share one? Shared state risks race conditions; per-worktree
   state risks split-brain.

3. **Can session continuity be achieved more simply — persisting and threading a
   session ID between tasks — without worktrees, and what would that miss?**
   The stall-recovery path already does `claude --resume <id>`. Explicitly threading
   the session ID through sequential delegations may be simpler, but would not
   solve the dirty-workspace problem and would not support parallel sessions.

4. **What is the full current behavior of `niwa apply` around branch cleanup, and
   what exactly causes repos to be stranded on feature branches?**
   Understanding the exact failure mode informs whether worktrees fix it or move
   the problem. Need to understand the "only merges clean" constraint and what
   "clean" means here.

5. **What would coordinator-managed session lifecycle look like as a UX?**
   What MCP tools would the coordinator call to start, resume, and end a session?
   How does the coordinator track which session maps to which repo? What happens
   when a session ends without a pushed PR — is the worktree preserved, archived,
   or cleaned up?

6. **What conflicts arise from multiple parallel worktrees for the same repo under
   a single coordinator?**
   Parallel sessions share the bare `.git` object store. What lock contention,
   ref conflicts, or hook interaction issues appear when two worktrees for the
   same repo are active simultaneously?

7. **Should non-mesh workspaces (plain `niwa go` / `niwa apply` workflows) continue
   to use the current checkout format, or should worktrees become the default for
   all workspaces?**
   If worktrees become mesh-only, there are two divergent code paths to maintain.
   If they become universal, migration and backward compatibility need design.

8. **What additional functionality becomes possible on top of persistent sessions?**
   Examples: session-to-PR tracking (a session maps to exactly one PR lifecycle),
   session summary generation for a compacted coordinator that needs to re-orient,
   session handoff between coordinators. Which of these are high-value follow-ons
   worth keeping in mind during the design?
