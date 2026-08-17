# /prd Scope: agent-capability-contract

Derived from the consumed /scope handoff (wip/scope_agent-capability-contract_handoff.md)
and the accepted brief; not re-derived conversationally (--auto, parent orchestration).

## Problem Statement

niwa's workspace-preparation path is structurally Claude-shaped: `agent.Agent`
governs two of roughly twenty capabilities, and the prior Codex attempt
(tsukumogami/niwa#248, closed) shipped its unifying abstraction as dead code
because no test could fail on the structure. There is no contract a second
agent can implement and no honest account of what a non-Claude session gets.

## Initial Scope

### In Scope
- Capability contract: closed set, two states per agent with reasons, Requires
  edges, enforcing tests (exhaustive, well-formed, closed, bound).
- Structural properties as tests: no agent constants at call sites, package-
  legible generic/specific boundary, no cross-agent gating, alias-backed renames.
- Two sequenced PRs; PR 1 provably behavior-preserving via characterization.
- Codex delivery through the contract: context, skills, MCP, env, trust,
  git-exclude; secret-hygiene fixes sequenced with env delivery.
- Generated user-guide gap list.

### Out of Scope
- Refactors dual-agent capability does not touch; side-by-side multi-agent
  sessions; re-measuring spike-recorded mechanics; weakening the 15-scenario
  bar; third-agent delivery.

## Research Leads

1. Capability matrix and state model: resolved by r2 support-matrix lead.
2. Codex MCP/env delivery shape: resolved by r3 measurement (codex-cli 0.147.0).
3. No-behavior-change proof: resolved by r2 (ManagedFiles characterization).
4. Rename blast radius: resolved by r2 (zero renames in PR 1).
5. PR 1 contract reach: requirements decision, taken in this PRD.

## Coverage Notes

Two measurements remain open (worktree `.git`-file marker; approval/sandbox
settability). Requirements are written outcome-neutral with settlement paths.
