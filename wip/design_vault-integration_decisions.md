# Design Decisions Log: vault-integration

Tracks decisions made during the design workflow in --auto mode per
`references/decision-protocol.md`. Each decision records evidence,
recommendation, and rationale.

## Phase 0: Setup

### D0.1 Execution mode confirmed as --auto
**Evidence:** User instruction "make sure you proceed in --auto mode"
**Decision:** Proceed in auto mode; follow research-first protocol at
every decision point; do not block on user input.

### D0.2 Stay on current branch `docs/vault-integration`
**Evidence:** User instruction "let's /design it in this same branch"
**Decision:** Skip branch creation in Phase 0. Current branch matches
the `docs/<topic>` convention (topic: vault-integration).
