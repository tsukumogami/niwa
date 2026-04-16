# Design Summary: vault-integration

## Input Context (Phase 0)

**Source PRD:** docs/prds/PRD-vault-integration.md

**Problem (implementation framing):** Adding a pluggable vault layer to
niwa forces structural changes across three existing subsystems — the
TOML schema (`internal/config/`), the override-merge pipeline
(`internal/workspace/override.go`), and the materialization pipeline
(`internal/workspace/materialize.go` + `state.go`) — without breaking
v0.6 configs or adding Go dependencies. The hardest technical
challenge is ordering: D-6 requires `vault://` URIs to resolve to
`secret.Value` inside each source file's provider context BEFORE the
merge runs, inverting the current parse → merge → materialize flow.

**Execution mode:** auto (from user instruction). Decisions tracked in
`wip/design_vault-integration_decisions.md`.

**Visibility:** Public (niwa repo). Public-content governance applies.

**Scope:** Tactical (niwa default).

## Current Status

**Phase:** 0 - Setup (PRD) complete
**Last Updated:** 2026-04-15
