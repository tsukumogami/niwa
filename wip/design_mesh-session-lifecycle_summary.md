# Design Summary: mesh-session-lifecycle

## Input Context (Phase 0)
**Source PRD:** docs/prds/PRD-mesh-session-lifecycle.md
**Problem (implementation framing):** Niwa's single-main-clone, role-directory-routed
task model cannot support persistent Claude sessions across task boundaries, isolated
per-feature worktrees, or tree-structured session communication without targeted
extensions to delegate routing, ask routing, the session registry schema, and the
shell wrapper.

## Current Status
**Phase:** 0 - Setup (PRD)
**Last Updated:** 2026-05-04

## Decisions Log

| ID | Artifact | Tier | Status | Question |
|----|----------|------|--------|---------|
| D0-1 | this file | 1 | assumed | PRD status is Draft not Accepted; proceeding per explicit user instruction |
