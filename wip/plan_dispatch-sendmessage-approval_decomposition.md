---
design_doc: docs/designs/DESIGN-dispatch-sendmessage-approval.md
input_type: design
decomposition_strategy: horizontal
strategy_rationale: "The design already fixes each component's interface and a commit order, and the thinnest end-to-end path (flag to launch argv) is itself one unit, so a separate skeleton would duplicate it."
confirmed_by_user: false
issue_count: 8
execution_mode: single-pr
docs_coverage_issue: 8
---

# Plan Decomposition: DESIGN-dispatch-sendmessage-approval

## Strategy: Horizontal

One unit per design phase, following the design's commit order, with Phase 6
split in two: the functional scenarios (code) and the guide plus manual check
(docs). Units 1-3 have no dependencies on each other. Unit 4 is where the
behavior turns on and depends on the config key and the settings rendering.
Units 5 and 6 build on unit 4's `inboundApplied` boolean. Units 7 and 8 close
out coverage and documentation.

Docs coverage (step 3.1a): the design sets `user_visible_surface: true`, so unit 8
is a dedicated `docs` unit covering the guide, the contributor-guide index, and
the flag's help and completion check.

## Issue Outlines

### Issue 1: feat(watch): deny session-reaching tools in review sessions
- **Type**: standard
- **Complexity**: critical
- **Goal**: Add a PreToolUse hook denying `SendMessage`, `SendFile`, `RemoteTrigger`, and `ListAgents` to every `niwa watch` review session, appended and verified by matcher and command in every containment mode.
- **Section**: Decision 5; Solution Architecture > Components (`internal/watch/containment.go`); Implementation Approach > Phase 7
- **Milestone**: Unattended peer messages for dispatched sessions
- **Dependencies**: None

### Issue 2: feat(config): add the accept_session_messages_on_dispatch machine key
- **Type**: standard
- **Complexity**: testable
- **Goal**: Add `GlobalSettings.AcceptSessionMessagesOnDispatch` and `config.CrossSessionInboundKey`, decoding and round-tripping, with nothing reading the key yet.
- **Section**: Decision Outcome > Summary; Components (`internal/config`); Implementation Approach > Phase 1
- **Milestone**: Unattended peer messages for dispatched sessions
- **Dependencies**: None

### Issue 3: refactor(dispatch): render launch settings from one map and separate the Claude prompt
- **Type**: standard
- **Complexity**: critical
- **Goal**: Route remote control's `--settings` injection through `renderLaunchSettings` with byte-identical output, and set `PromptSeparator` on Claude's launch spec so a prompt can't replace niwa's settings document.
- **Section**: Decision 1; Decision 6; D14; Implementation Approach > Phase 2
- **Milestone**: Unattended peer messages for dispatched sessions
- **Dependencies**: None

### Issue 4: feat(dispatch): add --accept-session-messages with its capability row and audit lines
- **Type**: standard
- **Complexity**: critical
- **Goal**: Register the tri-state flag, add the `DispatchInboundAcceptance` capability row, resolve flag over machine key into `inboundApplied`, add `crossSessionInbound: "accept"` to the launch settings map, and print the audit, override, and warning lines.
- **Section**: Decision 2; Decision Outcome > Summary; Data Flow steps 1-6; Implementation Approach > Phase 3
- **Milestone**: Unattended peer messages for dispatched sessions
- **Dependencies**: Issue 2, Issue 3

### Issue 5: feat(list): record and report accepts_session_messages
- **Type**: standard
- **Complexity**: testable
- **Goal**: Persist `inboundApplied` on the session mapping and report an always-present `accepts_session_messages` field and a `(accepts session messages)` marker in `niwa list`.
- **Section**: Decision 4; Implementation Approach > Phase 4
- **Milestone**: Unattended peer messages for dispatched sessions
- **Dependencies**: Issue 4

### Issue 6: feat(dispatch): show the one-time explanation and remember it beside config.toml
- **Type**: standard
- **Complexity**: testable
- **Goal**: Print the explanation on the first dispatch where the behavior takes effect, after any `claude attach` returns, and create the marker only when a terminal showed it.
- **Section**: Decision 3; Data Flow steps 6 and 8; Implementation Approach > Phase 5
- **Milestone**: Unattended peer messages for dispatched sessions
- **Dependencies**: Issue 4

### Issue 7: test(functional): cover dispatch session-message acceptance end to end
- **Type**: standard
- **Complexity**: testable
- **Goal**: Add the `@critical` Gherkin scenarios and step definitions for R18, the R3 source fixtures, the Codex warning, the prompt-separator case, and the watch-site unit test.
- **Section**: Implementation Approach > Phase 6 (scenarios)
- **Milestone**: Unattended peer messages for dispatched sessions
- **Dependencies**: Issue 1, Issue 4, Issue 5, Issue 6

### Issue 8: docs(guide): document session-message acceptance and record the manual check
- **Type**: docs
- **Complexity**: simple
- **Goal**: Write the session-message-acceptance guide and its index entry, check the flag's help and completion, and run the manual delivery check whose result sets the guide's Claude Code version line.
- **Section**: Implementation Approach > Phase 6 (guide, help, manual check); Security Considerations
- **Milestone**: Unattended peer messages for dispatched sessions
- **Dependencies**: Issue 1, Issue 4, Issue 5, Issue 6

## Value Confirmation (step 3.5a)

<!-- decision:start id="value-guard" status="confirmed" -->
**Decision:** Pass. The plan lands in a single PR, so it has one unit (the whole
plan), which passes by construction: merged, it delivers a developer-visible
opt-in that removes approval prompts on messages into dispatched workers, with
its audit trail and documentation.
<!-- decision:end -->

## Execution Mode (step 3.6)

<!-- decision:start id="execution-mode" status="confirmed" -->
**Decision:** `single-pr`. niwa's CLAUDE.md declares no `## Delivery Preference:`
header, so the preference resolves to `consolidated`. No split branch fires: all
work is in one repository, nothing must reach the default branch before another
piece can run, and no unit is a standalone increment a reader would want alone
(units 1-6 are building blocks of one feature; units 7-8 cover it). No
`split_branch` or `split_rationale` is recorded.
<!-- decision:end -->
