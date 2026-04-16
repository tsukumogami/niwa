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

## Phase 1: Decomposition

### D1.1 Decision count: 6 (1-5 bucket per scaling heuristic)
**Evidence:** Six independent technical choices identified after merging
coupled ones. Count is in the 6-7 bucket: --auto proceeds, records
assumption.
**Decision:** Proceed as single design doc. Recorded assumption: the 6
decisions are orthogonal enough that cross-validation in Phase 3 will
catch any implicit couplings without forcing a split.

### D1.2 Merged away: Infisical invocation strategy
**Evidence:** Choosing between `infisical secrets get` vs `infisical
export` is a backend-specific implementation detail, not a design-level
choice with cross-cutting trade-offs.
**Decision:** Move Infisical invocation to Phase 4 synthesis rather
than a standalone decision. Phase 4 synthesizes the Infisical-specific
implementation given the Provider interface from decision D3.

## Phase 3: Cross-Validation

### D3.1 No hard conflicts between decisions
**Evidence:** All 6 decisions align on pipeline order
(parse → resolve → [guardrail + shadow] → merge → materialize). Shadow
records carry only strings (no secret.Value). state.json schema bump
1→2 is jointly additive (ManagedFile gains Sources[]; InstanceState
gains Shadows[]). Context threading through Resolve is consistent.
**Decision:** No decision agents re-spawned. `cross_validation: passed`.

### D3.2 Reconciled: shadow detection timing wording
**Evidence:** D1 open item #4 said R31 diagnostics run "immediately
after merge"; D6 specifies pre-merge (so the detector can diff pre-flat
team config against overlay). D6 has the concrete design.
**Decision:** Adopt D6's pre-merge DetectShadows placement. D1's
wording updated via Phase 4 synthesis (not a conflict since D1
explicitly defers to the shadow-detection decision).

### D3.3 Reconciled: VersionToken field naming
**Evidence:** D3 uses `VersionToken{Native string; Provenance string}`;
D4 mentioned `{Kind; Token; Provenance}`. Both agree on an opaque
provider-side field + a provenance field.
**Decision:** Normalize to `VersionToken{Token string; Provenance string}`
in Phase 4. Drop Kind because `Provider.Kind()` already carries it.

### D3.4 Confirmed: R12 add-only semantics resolve D1's open-item #1
**Evidence:** PRD R12 is add-only for DIFFERENT provider names; same-name
collisions are hard errors. There is no shared/extended registry.
**Decision:** File-local scoping holds. Resolver walks team and
personal overlay independently.

### D1.3 Merged: pipeline ordering + shadow detection coupling
**Evidence:** Shadow detection location depends on pipeline ordering
(where the merge stage runs). But shadow detection is ALSO a separate
UX question (stderr vs state vs status), so they're not fully coupled.
**Decision:** Keep D1 (pipeline ordering) and D6 (shadow detection
integration) separate. Phase 3 cross-validation will verify consistency
between D1's merge-stage design and D6's detection-surface design.
