# Design Summary: claude-key-consolidation

## Input Context (Phase 0)
**Source:** /explore handoff
**Problem:** Rename `[content]` to `[claude.content]` in workspace.toml so
the Claude-specific semantics are explicit in the schema (today the
generic-sounding name hides 100% Claude coupling).
**Constraints:**
- Migration policy settled: accept both `[content]` and `[claude.content]`
  for N releases with a deprecation warning (no hard break).
- Rename preserves the shape of the sub-tables; only the root path
  changes.
- Content never participates in merge/override resolution — the rename
  is a pure syntactic refactor (~150 LOC across ~8 files, mostly tests).
- Per-repo override interaction needs a design call: research
  recommended splitting `ClaudeConfig` into a full form + narrower
  `ClaudeOverride` (no `Content`, no `Marketplaces`) used by
  `RepoOverride.Claude`, `InstanceConfig.Claude`, `GlobalOverride.Claude`.
- `workspace.content_dir` rename is flagged as a design-time decision.

## Current Status
**Phase:** 0 - Setup (Explore Handoff)
**Last Updated:** 2026-04-14
