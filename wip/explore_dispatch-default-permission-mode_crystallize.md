# Crystallize Decision: dispatch-default-permission-mode

## Chosen Type
/scope (the author asked to run it through the full `/deliver` workflow in this session)

## Candidacy
- /execute: not a candidate, no PLAN covers this topic
- Competitive analysis: not a candidate (public)

## Rationale
The exploration converged on one bounded feature, a host-level default dispatch
permission mode, and made decisions a future contributor needs: host-wins
precedence, which deliberately reverses the default_dispatch_harness polarity;
a niwa-owned vocabulary instead of raw passthrough; and Codex not mapping to
danger-full-access. Design calls remain open: the vocabulary, whether the
explicit flag adopts it, and what replaces the `ask` posture's defaultMode write
when the workspace key is deprecated.

## Stage 1 Evidence
### Signals Present
- Converged on something to build: the [global] key plus the derivation rung
- Requirements/architecture questions open: vocabulary, Codex mapping, deprecation replacement
- Decisions need a durable home: see the decisions file
- A scope boundary emerged: watch and ephemeral sessions out, Claude and Codex in
- Core question is what to build and how

### Anti-Signals Checked
- Nothing left to build: not present
- One choice between named options: not present
- Feasibility verdict only: not present
- Work should not happen: not present

### Ranking
- A chain: 5
- Decision Record: 1 (demoted, multiple interrelated decisions with work)
- Spike Report: 0 (demoted)
- Rejection Record: 0

## Stage 2 Evidence
### Signals Present
- /scope: single coherent feature; how to build not fully settled; decisions between approaches; multiple viable paths; decisions made that belong on record
- File an issue: one person can implement; one round, confident author

### Anti-Signals Checked
- File an issue: architectural decisions were made during exploration (present, demoted)
- /scope: multiple independent features (not present); qualifying PLAN exists (not present)
- /charter: one bounded feature in an existing project (present)

### Ranking
- /scope: 5
- File an issue: 1 (demoted)
- /charter: -2 (demoted)

## Tiebreakers Applied
- None needed; /scope led by more than one point after demotion.

## Alternatives Considered
- **File an issue**: decisions made during exploration need a durable home, and the deprecation is a second staged change.
- **Decision record**: several connected choices with work attached.
