# Design Summary: workspace-config

## Input Context (Phase 0)
**Source:** Freeform topic, informed by prior exploration in vision repo
**Problem:** niwa needs a TOML schema for workspace.toml that generalizes the tools repo's imperative installer into a declarative config format covering repos, groups, CLAUDE.md hierarchy, hooks, settings, env, and channel config.
**Constraints:** Go TOML parseable, content by reference, multi-instance support, phased delivery

## Prior Research
- Vision repo exploration: 6 research leads covering config format design, multi-repo orchestration tools, AI workspace patterns, tools repo inventory, bootstrapping patterns, tsuku boundary
- Accepted PRD-niwa.md with 13 user stories and 19 requirements
- Tools repo install.sh as imperative reference (27 operations, 3 entry points)

## Current Status
**Phase:** 0 - Setup (Freeform)
**Last Updated:** 2026-03-25
