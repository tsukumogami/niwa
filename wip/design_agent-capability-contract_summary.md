# Design Summary: agent-capability-contract

## Input Context (Phase 0)
**Source PRD:** docs/prds/PRD-agent-capability-contract.md
**Problem (implementation framing):** The workspace-preparation path has no
structure a test can fail on: agent-specific behavior is hardcoded at write
sites (eight known context-filename sites), the one accessor meant to route it
is dead, and there is no declaration layer a second agent could implement.
The design must pick the package shape, the capability state model, the
enforcement tests, the no-behavior-change proof, the MCP/env delivery surface,
the config rename mechanics, and the secret-hygiene increments.

## Orchestration
Invoked under /scope parent orchestration (sentinel in
wip/scope_agent-capability-contract_state.md, child: design). Fallback shapes
applied: decision-bypass-with-inline-resolution (Phase 2),
serial-self-jury (Phase 6), parent-delegated-approval (final status stays
Proposed; parent owns transitions and commits). PRD left at Draft; the parent
owns its Accepted/In Progress transitions.

## Current Status
**Phase:** 6 - Final review
**Last Updated:** 2026-08-17
