# Design Summary: inert-defaultmode-key

## Input Context (Phase 0)
**Source PRD:** docs/prds/PRD-inert-defaultmode-key.md
**Problem (implementation framing):** `buildSettingsDoc`'s mapping writes
permission modes Claude Code ignores (`bypassPermissions`) or rejects whole
(`askPermissions`), and the dispatch derivation reads its bypass decision back
out of that dead value instead of the resolved declaration.

## Current Status
**Phase:** 5 - Security (in progress); Phases 2-4 complete
**Last Updated:** 2026-09-10
