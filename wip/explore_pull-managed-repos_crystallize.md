# Crystallize Decision: pull-managed-repos

## Chosen Type

Design Doc

## Rationale

The exploration established clear requirements (from issue #30: pull latest, skip
dirty repos, non-destructive) but surfaced multiple competing approaches for
command UX (separate command vs flag vs default behavior) and meaningful
architectural decisions (state tracking enhancements, pipeline integration
point, git operation strategy). The core open question is "how should we build
this?" -- exactly what a design doc resolves.

Five architectural/design decisions were made during exploration that need
permanent documentation: git strategy (fetch + ff-only), default behavior rules
(pull only clean repos on default branch), no stash/rebase in defaults, TOML
drift as separate concern, and shell-out pattern. These decisions would be lost
when wip/ is cleaned if not captured in a design doc.

## Signal Evidence

### Signals Present

- **What to build is clear, how is not**: Issue #30 defines the feature.
  Exploration focused entirely on implementation approach.
- **Technical decisions needed between approaches**: Four command UX options
  (A: separate sync, B: --pull flag, C: always pull, D: smart pull) with
  different trade-offs.
- **Architecture/integration questions remain**: Pipeline insertion point,
  RepoState schema enhancement, drift detection logic.
- **Multiple viable implementation paths**: All four UX options are feasible;
  the research shows trade-offs but doesn't definitively eliminate any.
- **Decisions made during exploration**: Git strategy, default behavior matrix,
  TOML drift separation -- all need permanent record.
- **Core question is "how should we build this?"**: Yes.

### Anti-Signals Checked

- **What to build is still unclear**: Not present. Requirements are clear.
- **No meaningful technical risk or trade-offs**: Not present. Significant
  trade-offs exist (backward compat, dirty repo handling, UX friction).
- **Problem is operational, not architectural**: Not present. This is
  architectural (pipeline changes, state schema, command surface).

## Alternatives Considered

- **PRD**: Score -2. Requirements were provided as input (issue #30), not
  discovered during exploration. No stakeholder alignment needed.
- **Plan**: Score 0 (demoted). Could break into issues, but the technical
  approach (command UX, state tracking) has open decisions that a plan
  can't sequence without a design doc first.
- **No Artifact**: Score -2 (demoted). Too many architectural decisions
  to implement without documenting the approach first.
- **Decision Record (deferred)**: Partially fits for the command UX choice,
  but there are multiple design decisions, not just one. Design Doc subsumes
  a decision record.
