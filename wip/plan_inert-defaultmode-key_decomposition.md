---
design_doc: docs/designs/DESIGN-inert-defaultmode-key.md
input_type: design
decomposition_strategy: horizontal
strategy_rationale: "The design refactors existing code along stable interfaces and mandates a layer order -- the reader moves off the settings file before the producer stops writing it -- so layer-by-layer issues follow its phases."
confirmed_by_user: false
issue_count: 5
execution_mode: single-pr
---

# Plan Decomposition: DESIGN-inert-defaultmode-key

## Strategy: Horizontal

The design is a refactor of existing code: the posture mapping, the instance
pipeline, the dispatch derivation, and the tests around them all exist today.
Its interfaces are named and stable (`claudeDefaultMode`,
`instancePermissionsPosture`, `InstanceState.ClaudePermissions`,
`derivePermissionMode`, `buildDispatchPassthrough`), and it prescribes a strict
order so the existing `@critical` dispatch scenarios stay green at every step:
record the posture first, move the reader onto it second, change the producer
third. A walking skeleton would cut across that order. Each issue below
implements one of the design's five Implementation Approach phases.

## Issue Outlines

### Issue 1: feat(workspace): record the resolved permission posture in instance state
- **Type**: standard
- **Complexity**: testable
- **Goal**: Persist the instance-root permission posture (`bypass`, `ask`, or empty) that materialization resolved into a new `InstanceState.ClaudePermissions` field, with no change to any generated settings document.
- **Section**: Implementation Approach, Phase 1; Solution Architecture (Instance posture resolver, Persisted posture)
- **Milestone**: Inert defaultMode key
- **Dependencies**: None

### Issue 2: refactor(dispatch): derive --permission-mode from instance state and pass it explicitly to the argv builder
- **Type**: standard
- **Complexity**: critical
- **Goal**: Make `niwa dispatch` decide the bypass flag from the recorded posture instead of the materialized settings file, give `buildDispatchPassthrough` an explicit permission-mode parameter that both `niwa watch` launch sites pass empty, and remove the dead readers.
- **Section**: Implementation Approach, Phase 2; Solution Architecture (Dispatch derivation, Argv builder, Removed)
- **Milestone**: Inert defaultMode key
- **Dependencies**: Issue 1

### Issue 3: fix(workspace): stop writing permission modes Claude Code ignores or rejects
- **Type**: standard
- **Complexity**: critical
- **Goal**: Replace the posture mapping so `bypass` writes no `permissions.defaultMode` and `ask` writes `default` in all four generated documents, with secret-safe errors for invalid values, and move every affected test and golden to the new output.
- **Section**: Implementation Approach, Phase 3; Solution Architecture (Posture mapping)
- **Milestone**: Inert defaultMode key
- **Dependencies**: Issue 2

### Issue 4: test(functional): cover dispatch argv and the S1-S9 document matrix end to end
- **Type**: standard
- **Complexity**: testable
- **Goal**: Add the functional dispatch scenarios for every posture source and dispatch form, and a Scenario Outline asserting all four settings documents across the PRD's nine scenarios.
- **Section**: Implementation Approach, Phase 4; Considered Options, Decision 3
- **Milestone**: Inert defaultMode key
- **Dependencies**: Issue 3

### Issue 5: docs: correct the documents that describe the retired permission mechanism
- **Type**: docs
- **Complexity**: simple
- **Goal**: Update the seven committed documents the PRD names so none claims the generated settings file sets a session's posture, and each states that the posture reaches dispatched workers on the `--permission-mode` flag.
- **Section**: Implementation Approach, Phase 5
- **Milestone**: Inert defaultMode key
- **Dependencies**: Issue 3

## Docs Coverage (step 3.1a)

The design's frontmatter carries `user_visible_surface: true` (authoritative),
so the decomposition must carry documentation work. Issue 5 is a dedicated
`**Type**: docs` issue covering the user-facing guides
(`docs/guides/ephemeral-session-instances.md`, `docs/guides/file-distribution.md`)
and the four current design documents.

## Value Confirmation (step 3.5a)

<!-- decision:start id="plan-value-guard" status="confirmed" -->
The plan lands in a single PR, so it has one unit: the whole plan. It passes by
construction. Landed alone, it delivers the PRD's full outcome: dispatched
workers keep bypass through a route that takes effect, developers' own postures
survive in niwa instances, `ask` scopes prompt and keep their other settings,
and no generated file makes a claim it can't keep.
<!-- decision:end -->

## Execution Mode (step 3.6)

<!-- decision:start id="plan-execution-mode" status="confirmed" -->
**single-pr.** The repository declares no `## Delivery Preference:` header, so
the resolved preference is the `consolidated` default. No split branch fires:
there is no hard constraint (one repository, no workflow that must reach the
default branch first, no merge gate between steps), and the five issues are
ordered layers of one change rather than independently useful increments.
Under `consolidated` a single-pr plan records no `split_branch` or
`split_rationale`.
<!-- decision:end -->
