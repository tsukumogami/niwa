---
design_doc: docs/designs/DESIGN-session-store-teardown.md
input_type: design
decomposition_strategy: horizontal
strategy_rationale: "The design refactors existing code along stable seams (a lock primitive, then the writers and readers it orders, then the CLI resolver), and each layer is a prerequisite for the next rather than a slice of one end-to-end flow."
confirmed_by_user: false
issue_count: 7
execution_mode: single-pr
docs_coverage: issue 7
---

# Plan Decomposition: DESIGN-session-store-teardown

## Strategy: Horizontal

The design already names seven phases along package seams, and they are the
decomposition: a test seam that must land red first, the lock package, the
writers and readers routed through it, the root-state guards, the root
refusal, the destroy resolver, and functional coverage with the guides. Each
issue is one reviewable commit in the single pull request the work lands in.

Walking skeleton was not appropriate: this is a refactor of existing code with
no new end-to-end flow to stub, and the riskiest part (ordering) has no useful
thin slice, because a half-ordered directory is exactly the bug being fixed.

## Value Confirmation (step 3.5a)

One unit: the whole plan lands in one pull request. It passes by construction.
A reader of that PR sees teardown resolving the ids they hold and parallel
provisioning keeping its mappings, with tests that fail on the parent commit.
No sub-unit is claimed to deliver standalone value, so no unit is waved
through on mechanism.

## Execution Mode (step 3.6)

single-pr, under the repository's default `consolidated` Delivery Preference
(niwa's CLAUDE.md declares no `## Delivery Preference:` header). No split
branch fires, so the PLAN records no `split_rationale`. The task brief also
asked for one pull request covering both issues (#292 and #297).

Tracking level resolves to `none` (no `## Tracking Level:` header, and
single-pr derives `none`), so no GitHub issues or milestone are created and the
PLAN carries Issue Outlines.

## Issue Outlines

### Issue 1: test(workspace): add testhook seam and failing race tests
- **Type**: code
- **Complexity**: testable
- **Goal**: Add the `internal/testhook` registry with its parser-based guard test, place the three behavior-neutral hook points, and add the forced race tests that fail against this commit.
- **Section**: Implementation Approach, Phase 1
- **Milestone**: session-store-teardown
- **Dependencies**: None

### Issue 2: feat(configdir): per-config-dir lock, layout and recovery
- **Type**: code
- **Complexity**: testable
- **Goal**: Add `internal/configdir` owning the lock, the swap layout names, `HoldsSnapshot`, staging creation, the recovery rules and the `Mutate`/`Read` entry points.
- **Section**: Implementation Approach, Phase 2
- **Milestone**: session-store-teardown
- **Dependencies**: Issue 1

### Issue 3: fix(workspace): order refresh, mapping store and watch writes
- **Type**: code
- **Complexity**: testable
- **Goal**: Route the snapshot writer, the swap, the mapping store, the watch state writers and config discovery through the lock, with recovery-first and deletes after release.
- **Section**: Implementation Approach, Phase 3
- **Milestone**: session-store-teardown
- **Dependencies**: Issue 2

### Issue 4: fix(workspace): atomic locked SaveState with skip-after-failed-read
- **Type**: code
- **Complexity**: testable
- **Goal**: Write `instance.json` through a temp file and rename, take the lock for refreshed config directories, and never write root state after a failed read.
- **Section**: Implementation Approach, Phase 4
- **Milestone**: session-store-teardown
- **Dependencies**: Issue 2

### Issue 5: fix(cli): refuse worktree subcommands at a multi-instance root
- **Type**: code
- **Complexity**: testable
- **Goal**: Rebuild `discoverInstanceRoot` on `ClassifyCwd` with `IsSingleInstanceLayout` (which requires a named instance), add the `errAtWorkspaceRoot` sentinel and the `worktree list` branch.
- **Section**: Implementation Approach, Phase 5
- **Milestone**: session-store-teardown
- **Dependencies**: None

### Issue 6: feat(cli): resolve worktree destroy by session id or handle
- **Type**: code
- **Complexity**: testable
- **Goal**: Add the destroy resolver and its outcome contract, so a session id or handle reaches that session's active worktrees from anywhere in the workspace.
- **Section**: Implementation Approach, Phase 6
- **Milestone**: session-store-teardown
- **Dependencies**: Issue 3, Issue 5

### Issue 7: docs(worktree): functional coverage and guide updates
- **Type**: docs
- **Complexity**: simple
- **Goal**: Add the functional scenarios for destroy by session id and handle and for four parallel dispatches against a local config source, and document the new id forms, outcomes and lock files in the guides.
- **Section**: Implementation Approach, Phase 7
- **Milestone**: session-store-teardown
- **Dependencies**: Issue 3, Issue 6
