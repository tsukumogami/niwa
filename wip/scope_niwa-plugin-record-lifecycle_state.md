---
topic: niwa-plugin-record-lifecycle
chain_started: 2026-06-20T12:28:25Z
last_updated: 2026-06-20T12:28:25Z
phase_pointer: phase-3
chain_ran:
  - name: brief
    artifact: docs/briefs/BRIEF-niwa-plugin-record-lifecycle.md
    status: Accepted
    jury: all-PASS
  - name: prd
    artifact: docs/prds/PRD-niwa-plugin-record-lifecycle.md
    status: In Progress
    jury: all-PASS
  - name: design
    artifact: docs/designs/DESIGN-niwa-plugin-record-lifecycle.md
    status: Planned
    jury: all-PASS
  - name: plan
    artifact: docs/plans/PLAN-niwa-plugin-record-lifecycle.md
    status: Active
    review: PASS
    execution_mode: single-pr
chain_revised: false
exit: full-run
exit_artifacts:
  - docs/plans/PLAN-niwa-plugin-record-lifecycle.md
planned_chain:
  - brief
  - prd
  - design
  - plan
chain_skipped: []
phase-1: empty-cold-start
visibility: Public
---

# Scope state: niwa-plugin-record-lifecycle

## Context (from invocation)

Fix the niwa behaviors that amplify a Claude Code weakness into intermittent
plugin/skill registration failures (shirabe skills failing to register /
de-registering; cleared by `/reload-plugins`).

Claude Code owns `~/.claude/plugins/installed_plugins.json` and never GCs
records whose projectPath or cached version dir disappears. niwa amplifies:
1. Proliferation — writes `enabledPlugins` per repo-subdir per workspace
   instance (`internal/workspace/materialize.go:374-398`).
2. No teardown — destroy paths touch no `~/.claude` plugin state
   (`internal/workspace/destroy.go`, `destroy_workspace.go`).
3. Forced churn — hardcodes `autoUpdate:true`
   (`internal/workspace/workspace_context.go:328,341`).

Observed: 111 shirabe records, 109 dangling.

Candidate directions: A) clean up records on destroy; B) self-healing GC
(doctor/apply step) dropping missing-installPath/projectPath records;
C) reduce proliferation via instance-root enablement (spike: CC scoping
semantics); D) configurable `auto_update` (default off for directory/local);
E) fix `mapMarketplaceSourceWithIndex` keying marketplace by repo-ref name
instead of declared marketplace.json name. Recommended core: B+D, A
supporting, C spike-gated, E folded in.

Goal: full doc chain BRIEF → PRD → DESIGN → PLAN; stop when plan is ready
for review.
