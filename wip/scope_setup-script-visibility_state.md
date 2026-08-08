```yaml
topic: setup-script-visibility
chain_started: 2026-08-08T16:39:09-04:00
last_updated: 2026-08-08T16:39:09-04:00
phase_pointer: phase-2
exit: UNSET
exit_artifacts: []
visibility: Public
execution_mode: auto
max_rounds: 5
coordination_intent: absent
planned_chain:
  - design
  - plan
chain_ran: []
chain_skipped:
  - name: brief
    reason: >-
      R4 EITHER-signal evaluated and both signals resolved negative-in-substance.
      No BRIEF exists at the canonical path, but the framing this skill would
      persist is already durable and upstream: issue tsukumogami/niwa#239 states
      the problem, the intended outcome, the scope boundary, and six acceptance
      criteria, and wip/explore_setup-script-visibility_scope.md persists the
      In Scope / Out of Scope boundary from the exploration. The framing has not
      shifted. Per /brief's own contract ("or a downstream PRD/design when a
      standalone brief is too heavy"), the framing is carried into the DESIGN
      amendment instead of a standalone BRIEF.
  - name: prd
    reason: >-
      R5 Mandatory-with-auto-skip gate would fire on a literal reading (no PRD at
      docs/prds/PRD-setup-script-visibility.md). Skipped deliberately, recorded
      rather than silent: /explore's crystallize evaluation scored PRD as demoted
      by the "requirements were provided as input to the exploration" anti-signal.
      Issue #239's six acceptance criteria ARE the requirements contract, authored
      by the repo owner before exploration started. A PRD here would restate them
      verbatim and add no decision. The requirements are carried into the DESIGN
      amendment's Context section by reference to the issue.
child_snapshots: {}
```

## Phase 1 — Discovery and Chain Proposal

**Framing-shift answer (R4):** No. The problem shape, audience, scope boundary, and
success criteria are all fixed by issue #239 and unchanged by exploration. Exploration
changed the *answer*, not the framing.

**On-disk child-doc discovery:** no artifact exists at any of the five canonical paths
(`docs/briefs/BRIEF-`, `docs/prds/PRD-`, `docs/designs/DESIGN-`,
`docs/designs/current/DESIGN-`, `docs/plans/PLAN-` + `setup-script-visibility.md`).
`docs/plans/` and `docs/decisions/` do not exist yet in this repo.
`shirabe slug-prefix-detect` returned `no-prevailing-prefix`, so the slug stands as
provided.

**The doc this chain actually targets is not at a `<topic>`-named path.** The DESIGN
this chain produces is an in-place amendment to
`docs/designs/current/DESIGN-post-clone-scripts.md`, per the /explore crystallize
decision. Canonical-path globbing cannot discover it, so it is named here explicitly
and snapshotted below.

**R6 shape-predicate walk for `/design`:**

- **P1 — architectural-alternatives count: FIRES.** Three alternatives are left open by
  the requirements. (a) Output routing: stream every line through `Reporter.Log` versus
  `runGitWithReporter`-style buffer-and-attach-to-error — prior art genuinely splits, and
  niwa's own two design docs disagree with its code. (b) Discoverability: the issue names
  three options (summary line, non-zero exit, opt-in flag) and explicitly leaves the choice
  open. (c) Where an opt-in fatal error is raised relative to `SaveState`, and whether
  `Create` returns the instance path alongside it.
- **P2 — new-component references: DOES NOT FIRE.** Every change lands in existing
  packages: `internal/workspace/{gitutil,setup,apply,reporter}.go` and
  `internal/config/`. No new binary, service, library, or runtime substrate.
- **P3 — Complex classification: FIRES.** The requirements explicitly demand that the
  chosen failure-policy reasoning be recorded in a design doc or ADR rather than a PR body
  ("a decision that lives only in code is how this drifted in the first place"), and
  crystallize scored "architectural decisions were made during exploration that should be
  on record" as the dominant signal.

**R7 verdict:** `/design` fires (P1 fires, P2 does-not-fire, P3 fires). Roster shape is
driven by P1's three open alternatives.

**Planned chain:**

```
/brief  — skipped (framing durable upstream in issue #239 + explore scope file)
/prd    — skipped (requirements supplied as input; PRD anti-signal confirmed at crystallize)
/design — fires  (R7 shape-dependent: P1 fires, P2 does-not-fire, P3 fires)
/plan   — fires  (ALWAYS)
```

**Confirmation (auto mode):** Proceed. Running non-interactively per `--auto`; the chain
shape follows the recommended default given the /explore crystallize verdict, and both
skips are recorded above with reasons rather than taken silently.

## Phase 2 — Child Invocation Log

| Child | Pre-invocation SHA | Status | Artifact |
|-------|--------------------|--------|----------|
| design | (pending) | pending | docs/designs/current/DESIGN-post-clone-scripts.md (amendment) |
| plan | (pending) | pending | docs/plans/PLAN-setup-script-visibility.md |
