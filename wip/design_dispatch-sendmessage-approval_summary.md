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

## Current Status
**Phase:** 5 - Security complete; Phase 6 review in progress
**Last Updated:** 2026-09-10
