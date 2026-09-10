# /plan Decisions: inert-defaultmode-key

Auto mode, invoked by `/scope` under a `parent_orchestration:` sentinel.
Parent bindings: Phase 6 review is Inline-substitute-review; Phase 7 emit is
Deterministic-mode-bypass plus Parent-delegated-approval.

| id | artifact | tier | status | question |
|----|----------|------|--------|----------|
| L2.1 | wip/plan_inert-defaultmode-key_milestones.md | 1 | confirmed | Milestone title from a kebab-case heading |
| L3.1 | wip/plan_inert-defaultmode-key_decomposition.md | 2 | confirmed | Decomposition strategy |
| L3.2 | wip/plan_inert-defaultmode-key_decomposition.md | 2 | confirmed | Value confirmation (step 3.5a) |
| L3.3 | wip/plan_inert-defaultmode-key_decomposition.md | 2 | confirmed | Execution mode (step 3.6) |

## L2.1 -- Milestone title rendered as a phrase

The design's heading is the topic slug. The milestone rule asks for a
human-readable phrase, so the title is "Inert defaultMode key" rather than the
slug itself.

## L3.1 -- Horizontal decomposition, one issue per design phase

The design refactors existing code along named, stable interfaces and mandates
a layer order (record the posture, move the reader, change the producer) so the
`@critical` dispatch scenarios stay green throughout. That matches the
horizontal criteria (refactoring existing code; stable interfaces) and not the
walking-skeleton ones (a new end-to-end feature). Five issues follow the five
Implementation Approach phases. The docs-coverage emit fires on
`user_visible_surface: true`; Issue 5 carries it.

## L3.2 -- Value guard passes by construction

A single-pr plan has one unit, the whole plan, which delivers the PRD's full
outcome on its own.

## L3.3 -- single-pr under the consolidated default

No `## Delivery Preference:` header in the repo's CLAUDE.md, so the preference
resolves to `consolidated`. No branch fires: one repository, no merge gate, no
workflow that must land first, and the issues are ordered layers of one change.
No `split_branch` or `split_rationale` is recorded.

| L4.1 | wip/plan_inert-defaultmode-key_decisions.md | 1 | confirmed | Downstream mapping and AC-to-issue assignment for Phase 4 |

## L4.1 -- Downstream mapping derived by hand; PRD acceptance criteria assigned to issues

The dependency chain is linear, so the downstream mapping the graph script
would compute was derived directly: 1 -> [2]; 2 -> [3]; 3 -> [4, 5]; 4 and 5
are leaves.

Each outline agent was told which PRD acceptance criteria its issue owns, so
the PLAN traces every criterion to an issue:
- Issue 1: the recorded-posture foundation (Go tests on real `Create`).
- Issue 2: AC7, AC9, AC15, AC17, AC20, plus the single-reader static check and
  the missing-state warning.
- Issue 3: AC12, AC13, AC14, AC16, AC19's parse test, the S1-S9 agreement test,
  secret-safe errors, and the `workspace-config-sources` observable.
- Issue 4: AC1-AC6, AC8, AC10, AC11, and AC19's fixture-body check.
- Issue 5: AC18.
- AC21 (`go test ./...` and `make test-functional-critical`) is a criterion on
  every code issue.
