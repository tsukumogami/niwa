# Design Decisions: init-bootstrap-empty-source

## Mode

`--auto`: decision points routed to the /shirabe:decision skill rather
than to the user.

## Phase 1 (Decomposition)

- Approved decomposition of the design into 4 decision questions: 1
  critical (bootstrap UX model, merging G1+G2+G4), 3 standard
  (empty-repo detection, adjacent failure-mode scope, scaffold
  derivation details). G6 (overlay+plugin interaction) folded into
  Phase 4 synthesis as mechanical.
- Why: cross-validation pressure on the merged Decision 1 is intentional
  — worktree location, registry timing, and commit state are coupled by
  end-to-end UX flow and should be evaluated together.
- How to apply: Phase 2 dispatches one decision agent per question. The
  user explicitly enabled --auto + decision-skill routing for the rest
  of the design.
