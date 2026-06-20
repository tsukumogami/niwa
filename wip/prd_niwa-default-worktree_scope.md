# PRD Scope: niwa-default-worktree

## Execution Mode
auto (invoked within /scope chain)

## Upstream
docs/briefs/BRIEF-niwa-default-worktree.md (Accepted)

## Problem (from brief)
In a niwa workspace, agent-initiated worktree creation can silently produce a
second, competing bare worktree alongside niwa's managed one — degraded (no
secrets/context) and invisible to niwa, leaving the developer to reconcile
orphans, polluted git status, and branch collisions. The PRD captures the
requirements for making niwa's managed worktree the single default.

## Carried-forward open questions (from brief, PRD must resolve)
1. Is the steer-to-niwa fallback surfaced to the developer (visible) or invisible?
2. Can a developer opt a workspace instance out of the default, and how is it
   expressed?

## Feasibility basis (settled — not re-opened by PRD)
docs/spikes/SPIKE-niwa-default-worktree.md (Complete, GO): delegation works via
per-repo Claude Code WorktreeCreate/WorktreeRemove hooks (settings.local.json
scope); workspace-root install does not reach an in-repo agent; WorktreeRemove
is non-blocking; the hook stdin gives session_id/cwd/name and must echo the
worktree path.

## Research Leads
1. niwa's conventions for default-on behaviors and per-instance opt-out/escape
   hatches; how apply-time actions are surfaced to developers. (-> prd-conventions)
2. Existing-behavior constraints: when apply runs and what it touches per repo;
   what `niwa worktree create` outputs today; the AllowMissingSecrets policy for
   worktrees; idempotency of content sync. (-> prd-constraints)

## Altitude guard
PRD states WHAT/policy and testable requirements. The delegation mechanism
(hook scripts, stdin->niwa adapter, WorktreeRemove reconciliation) is DESIGN.
