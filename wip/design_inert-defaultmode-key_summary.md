# Design Summary: inert-defaultmode-key

## Input Context (Phase 0)
**Source PRD:** docs/prds/PRD-inert-defaultmode-key.md
**Problem (implementation framing):** `buildSettingsDoc`'s mapping writes
permission modes Claude Code ignores (`bypassPermissions`) or rejects whole
(`askPermissions`), and the dispatch derivation reads its bypass decision back
out of that dead value instead of the resolved declaration.

## Current Status
**Phase:** 6 - Final Review (in progress); Phases 2-5 complete
**Last Updated:** 2026-09-10

## Security Review (Phase 5)
**Outcome:** Option 2 -- document considerations
**Summary:** No finding above Low; the design doesn't widen who can obtain
bypass. Four items folded in: a vault-safe invalid-value error with the
resolver returning literals, the same-process property on the derivation,
watch argv tests on a real `bypass` materialization, and the two watch-review
side effects recorded in Consequences.
