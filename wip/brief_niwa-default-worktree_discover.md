# /brief Discovery: niwa-default-worktree

## Problem Candidate

In a niwa-managed workspace, a developer who asks their AI coding agent to "work in
a worktree" can end up with two parallel, mutually-invisible worktree systems for
the same repo: the agent's built-in bare checkout (placed under the repo's
`.claude/worktrees/`) and niwa's managed worktree (placed under the workspace root,
carrying materialized secrets, repo and workspace context, and session tracking).
The bare checkout silently lacks the secrets and context a real checkout has, so the
agent works in a degraded environment; and because niwa has no record of it, the
developer is left to notice and clean up the divergence — orphaned worktrees,
polluted `git status`, branch collisions — by hand. Nobody chose to run two
systems; the developer just asked for a worktree.

## Outcome Candidate

A developer working in a niwa workspace can let their agent create worktrees the
normal way and trust that every worktree is a full niwa worktree — the same
materialized secrets, repo and workspace context, and session tracking a real
checkout gets — without having to think about which tool produced it. There is one
worktree per task, niwa knows about all of them, and the developer never has to
notice or reconcile a competing checkout. When an environment can't support the
integration, the agent is pointed at niwa's worktree command instead of silently
producing a degraded bare worktree.

## Grounding Anchor

conversation only (informed by docs/spikes/SPIKE-niwa-default-worktree.md, which
established that delegation is feasible via per-repo Claude Code worktree hooks)

## Journey Sketch

- Agent-initiated worktree: a coding agent operating in a workspace repo is asked to
  "work in a worktree" (or spawns an isolated subagent) and a fully-materialized niwa
  worktree is created transparently — the agent gets secrets and context without the
  developer doing anything special.
- Developer-initiated via niwa: the developer runs niwa's worktree command directly
  and gets the same result; the agent's native tooling does not create a second,
  competing worktree for the same task.
- Fallback environment: on a host or agent version that can't honor the integration,
  the native worktree tool does not silently produce a degraded checkout — the agent
  is steered to niwa's worktree command instead.
- Lifecycle and cleanup: when a task ends, teardown flows through niwa, so there are
  no orphaned worktrees and niwa's view of active worktrees reflects reality.

## Open Questions for Drafting

- The detailed delegation mechanism (per-repo hooks, the stdin->niwa adapter,
  WorktreeRemove reconciliation) is design-level and belongs in the downstream
  DESIGN, not the BRIEF. Keep the brief at the "what should the developer
  experience" altitude.
- Whether the fallback (steer-to-niwa) is surfaced to the developer as a visible
  difference or stays invisible is a PRD/design question.
