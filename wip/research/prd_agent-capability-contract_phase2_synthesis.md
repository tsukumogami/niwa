# Phase 2 Research: Synthesis (no new agents dispatched)

The /explore corpus already covers every lead in the scope file: eight round-1
leads (prep-path map, capability inventory, prior-attempt audit, config-rename
precedent, structural-test precedent, Go pattern precedent, skill resolution,
spike constraints), five round-2 leads (support matrix, plan-shaped contract,
rename blast radius, no-behavior-change proof, MCP/env shape), and the round-3
measurement against codex-cli 0.147.0. Dispatching fresh Phase 2 agents would
re-run that work; skipped per the --auto decision protocol, recorded in
wip/prd_agent-capability-contract_decisions.md.

## Key findings carried into the draft

- 24-row capability matrix, two-state model with reason kinds and Requires
  edges, plus the four enforcement tests (exhaustive, well-formed, closed,
  bound) -- r2 support-matrix lead.
- Eight context-writer sites hardcode Claude filenames on main today (the
  red-today evidence for the structural test); settings builder is the other
  agent-shaped surface -- r1/r2.
- ManagedFiles path+hash characterization, committed before the refactor, is
  the no-behavior-change proof; two nondeterminism sources need normalizing.
- Zero config renames in PR 1; Content/content_dir alias and claude.enabled
  restructure belong to PR 2 with the rename precedent
  (docs/designs/current/DESIGN-claude-key-consolidation.md).
- r3 measurements: [mcp_servers.*] schema pinned (16 fields, two transports,
  no SSE, no interpolation, whole-config blast radius on one malformed entry,
  recursive field-level layer merge => collision hazard);
  shell_environment_policy semantics pinned (inherit default all, set additive
  string-only, include_only silently drops set values, ignore_default_excludes
  defaults true); both keys measured trust-gated.
- Still unmeasured: worktree `.git`-file project-root marker; approval/sandbox
  settability from the project layer.

## Summary

Evidence is sufficient to draft requirements; the only open items are two
measurements whose both-ways outcomes the requirements can specify. Proceed to
Phase 3.
