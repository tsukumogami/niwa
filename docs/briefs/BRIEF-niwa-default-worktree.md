---
schema: brief/v1
status: Done
problem: |
  In a niwa workspace, asking an AI agent to "work in a worktree" can
  silently create two competing worktree systems for one repo: the agent's
  bare in-repo checkout and niwa's managed worktree. The bare one lacks
  secrets and context, and niwa never sees it, so the developer cleans up
  the divergence by hand.
outcome: |
  A developer lets their agent make worktrees the normal way and trusts
  every worktree is a full niwa worktree -- same secrets, context, and
  session tracking as a real checkout -- with one worktree per task, all
  known to niwa, and no competing checkout to reconcile.
motivating_context: |
  A feasibility spike (docs/spikes/SPIKE-niwa-default-worktree.md) confirmed
  that the agent's native worktree creation can be routed through niwa. This
  brief frames the feature now that the blocking question is settled.
---

# BRIEF: niwa as the default worktree mechanism

## Status

Done

The downstream PRD owns the requirements; the downstream DESIGN owns the
delegation mechanism. This brief stops at the developer-facing framing.

## Problem Statement

niwa manages a multi-repo workspace and gives each managed worktree the same
environment a real checkout has: vault-resolved secrets, repo and workspace
context, and a session record niwa can list, attach to, and tear down. AI
coding agents also create worktrees on their own — when a developer asks one to
"work in a worktree," or when the agent spawns an isolated sub-task. Those
agent-made worktrees are bare git checkouts placed inside the repo. They carry
none of what niwa materializes, and niwa has no record that they exist.

The result is two worktree systems for the same repo, neither aware of the
other. The agent's bare checkout is a degraded environment: the secrets and
context the developer's normal checkout has are simply missing, so the agent
works without them and the developer often can't tell until something fails.
And because niwa never recorded the bare worktree, the developer is left to
notice and untangle the divergence — orphaned worktrees that niwa's commands
won't clean up, an unexpected directory showing in `git status`, and branch
collisions when both systems reach for the same repo. Nobody decided to run two
systems. The developer just asked for a worktree, and got a second mechanism
they now have to manage.

## User Outcome

A developer working in a niwa workspace can let their agent create worktrees the
ordinary way and stop thinking about which tool made them. Every worktree the
agent produces is a full niwa worktree, with the same secrets, repo and
workspace context, and session tracking a real checkout gets — so the agent
works in a faithful environment instead of a stripped-down one. There is one
worktree per task, niwa knows about all of them, and teardown leaves nothing
orphaned. The developer never has to spot a competing checkout, reconcile two
branch states, or clean up after a mechanism they didn't know was running. When
an environment can't support the integration, the developer still doesn't get a
silent degraded checkout: the agent is pointed at niwa's worktree command
instead, so the worst case is an explicit redirect rather than a quiet trap.

## User Journeys

### A developer asks their agent to work in a worktree

A developer is working with a coding agent in a workspace repo and asks it to
"work in a worktree" so a risky change stays isolated. The request triggers the
agent's normal worktree creation, but what comes back is a full niwa worktree —
secrets and context already in place, recorded as a niwa session. The developer
sees no second checkout and does nothing special; the isolation they asked for
is also a faithful environment.

### An agent spawns an isolated sub-task on its own

Without anyone asking for a worktree, an agent decides to break work into
parallel sub-tasks and runs one in its own isolated worktree so edits don't
collide. The isolated sub-task lands in a niwa worktree rather than a bare
checkout, so it has the same secrets and context as the parent — and when the
sub-task finishes, teardown flows through niwa and leaves no orphan behind.

### A developer creates a worktree through niwa directly

A developer runs niwa's worktree command themselves to set up an isolated task.
They get the managed worktree as before — and the agent's native tooling does
not later create a second, competing worktree for the same task. The one
worktree the developer made is the one the agent uses.

### An agent runs where the integration can't be honored

A developer runs an agent on a host or agent version where the worktree
integration can't take effect. Instead of silently producing a degraded bare
checkout, the native worktree path is unavailable and the agent is steered to
niwa's worktree command. The developer gets an explicit "use niwa for this"
rather than a quiet checkout missing its secrets and context.

## Scope Boundary

### In

- Making niwa's managed worktree the mechanism behind agent-initiated worktree
  creation in a niwa workspace, so the native "work in a worktree" path yields a
  full niwa worktree.
- Guaranteeing one worktree per task, recorded by niwa, whether the worktree was
  initiated by an agent or by a developer running niwa directly.
- A defined fallback for environments where the integration can't be honored:
  steer the agent to niwa's worktree command rather than let it produce a
  degraded bare checkout.
- Applying this by default to every workspace instance niwa manages, without
  per-developer manual setup.

### Out

- The technical delegation mechanism — how the agent's worktree creation is
  routed into niwa, the input adaptation, and removal reconciliation. That is
  downstream DESIGN work.
- What a niwa worktree materializes and how secrets resolve. That pipeline
  already exists and is unchanged by this feature.
- niwa's worktree lifecycle commands themselves (create, destroy, attach, list).
  They are already built; this feature changes what reaches for them, not what
  they do.
- Agent harnesses other than Claude Code.
- Requirements-level specifics — exact configuration keys, flag names, and
  acceptance criteria. Those belong to the downstream PRD.

## References

- docs/spikes/SPIKE-niwa-default-worktree.md — feasibility spike establishing
  that agent-initiated worktree creation can be routed through niwa.
- tsukumogami/niwa#166 — the enhancement issue that motivated the investigation.
