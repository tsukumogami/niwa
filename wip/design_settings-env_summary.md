# Design Summary: settings-env

## Input Context (Phase 0)
**Source:** Freeform topic from issue #9 implementation feedback
**Problem:** [claude.env] needs promote mechanism to pull vars from resolved [env] pipeline into settings.local.json without value duplication. Resolution order across inline, file, promote, and per-repo overrides must be well-defined.
**Constraints:** No backwards-compatibility requirement (no users yet). Must handle all combinations of declaration types across hierarchy levels.

## Current Status
**Phase:** 0 - Setup (Freeform)
**Last Updated:** 2026-03-29
