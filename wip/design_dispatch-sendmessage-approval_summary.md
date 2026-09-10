# Design Summary: dispatch-sendmessage-approval

## Input Context (Phase 0)
**Source PRD:** docs/prds/PRD-dispatch-sendmessage-approval.md
**Problem (implementation framing):** Deliver Claude Code's `crossSessionInbound: "accept"` to a dispatched worker through the one `--settings` slot remote control already uses, gated agent-neutrally, recorded on the session mapping, with a once-per-configuration-directory explanation.

## Decisions (Phases 1-3)
Four independent decisions, resolved inline under the `/scope` parent
(decision-bypass fallback): settings builder, capability row, marker beside
`config.toml`, mapping and instance fields. Cross-validation found no conflicts.

## Security Review (Phase 5)
**Outcome:** Option 2 - document considerations
**Summary:** Mechanics sound (constant-only settings document, symlink-safe
marker that gates nothing). Added a review-session messaging deny, qualified the
config-source guarantee for `XDG_CONFIG_HOME`/`HOME` relocation, and adopted
`os.Lstat` for the marker check.

## Final Review (Phase 6)
Architecture review: applied attach-aware explanation timing, capability row
moved into the delivering phase as row 25 with count/gap-list/contract updates,
map-plus-helper rendering, hostGlobal hoist, inboundApplied boolean, resolver
result struct, config-path error handling, 0o755 directory mode, named test and
doc deliverables, explicit phase order. Watch exclusion moved to unit coverage;
PRD R18 amended (assumed, high priority).
Structural-format review: added Decision 5, trimmed PRD retelling in Context,
refocused Decision 3/4 alternatives on open questions, paired every negative
consequence with a mitigation, dropped an unchoosable alternative.
Security review (Phase 6): found the prompt-as-flag route into `--settings`
(confirmed by measurement; fixed with Claude `PromptSeparator`, Decision 6),
widened the review deny to four session-reaching tools verified by matcher and
command, corrected the posture claim, and documented the four holds `accept`
lifts, the inbox socket, and overlay/HOME relocation. Out-of-scope items and
three unmeasured behaviors recorded as decisions.

## Current Status
**Phase:** 6 - Final review
**Last Updated:** 2026-09-10
