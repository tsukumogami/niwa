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

**Phase:** 3 - Cross-Validation complete
**Last Updated:** 2026-04-15

## Phase Progress

- **Phase 0** (Setup + Context): complete — design skeleton with
  Context and Problem Statement + Decision Drivers.
- **Phase 1** (Decision Decomposition): complete — 6 decisions
  identified (D1 critical, D2 critical, D3-D6 standard).
- **Phase 2** (Decision Execution): complete — all 6 decision
  agents returned high-confidence recommendations. Reports at
  `wip/design_vault-integration_decision_<N>_report.md`.
- **Phase 3** (Cross-Validation): complete — no hard conflicts.
  Two minor reconciliations logged (shadow timing, VersionToken
  field names). Considered Options + Decision Outcome written.
- **Phase 4** (Architecture): complete — Solution Architecture,
  Implementation Approach (11 phases), and Consequences written.
  Frontmatter decision + rationale populated.
- **Phase 5** (Security): next (mandatory).
