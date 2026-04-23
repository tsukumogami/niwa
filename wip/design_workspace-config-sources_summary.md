# Design Summary: workspace-config-sources

## Input Context (Phase 0)

**Source PRD:** `docs/prds/PRD-workspace-config-sources.md`
**PRD status:** transitioned to "In Progress" by /shirabe:design Phase 0
**Branch:** `docs/workspace-config-sources` (continuing from PR #73)
**Mode:** `--auto` (PRD argument carried `--auto` flag)
**Visibility:** Public · **Scope:** Tactical
**No Market Context section, no Required Tactical Designs section** (per
context-aware-sections table for tactical scope).
**Upstream Design Reference:** the upstream is the PRD itself, linked
via the design's frontmatter `upstream:` field.

**Problem (implementation framing):** five tightly-coupled surfaces in
the niwa codebase encode the working-tree-with-pull-ff-only model: three
clone primitives, two `.git/`-dependent guards, ad-hoc slug parsing,
schema fields that don't carry the new source identity, and the
*absence* of test infrastructure for the GitHub-path verification the
PRD requires. The design's job is to commit to a coherent replacement
that touches all five symmetrically.

## Current Status

**Phase:** 0 - Setup (PRD) complete
**Last Updated:** 2026-04-23
