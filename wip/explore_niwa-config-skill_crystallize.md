# Crystallize Decision: niwa-config-skill

## Chosen Type

Design Doc

## Rationale

What to build is clear and largely fixed by the dispatch brief: a bundled
skill that teaches an agent how to update `.niwa/workspace.toml` in place,
covering `claude.*`, `env.*`, `vault.*`, `files`, and `instance` blocks, plus
common edits (add a hook, wire a secret, add a plugin, add instance files).
How to build it is not settled -- research surfaced two mechanically-proven
but architecturally distinct delivery mechanisms (generalize the embedded
plugin's rank-2-only install gate, or use `[instance.files]` to materialize
the skill from `workspace.toml` itself) with real trade-offs on reach,
precedent, and opt-in behavior, plus a content-shape question (static
schema copy vs. doc-linked vs. scaffold-anchored) where the evidence
(`DESIGN-workspace-config.md`'s documented staleness) actively rules out the
naive option. This is squarely a "how should we build this?" problem with
architectural decisions that need to be on record before implementation.

## Signal Evidence

### Signals Present

- What to build is clear, but how to build it is not: the brief states the
  goal precisely; research (all six leads) focused entirely on mechanism and
  content-shape options, not on what the skill should teach.
- Technical decisions need to be made between approaches: embedded-plugin
  install-gate extension vs. `[instance.files]` materialization vs. a
  combination, each with different reach/precedent trade-offs
  (`lead-plugin-install-gate`, `lead-instance-files-mechanism`,
  `lead-bootstrap-scaffold`).
- Architecture, integration, or system design questions remain: where the
  install trigger lives (`internal/workspace/apply.go`'s duplicated rank-2
  gate vs. a new condition), how content sourcing avoids the drift failure
  mode documented in `DESIGN-workspace-config.md`.
- Exploration surfaced multiple viable implementation paths: both delivery
  mechanisms are mechanically proven (installer tests, `[instance.files]`
  directory-copy tests) with no clear single winner from evidence alone.
- Architectural/technical decisions were made during exploration that
  should be on record: ruled out bootstrap-scaffold-only delivery and a bare
  static schema copy (see `wip/explore_niwa-config-skill_decisions.md`) --
  these eliminations need a permanent home before `wip/` is cleaned.
- Core question is "how should we build this?": yes, per the brief's own
  "Open questions to resolve during explore/scope" section, which is
  entirely about mechanism and content shape, not about what the skill
  should accomplish.

### Anti-Signals Checked

- "What to build is still unclear": not present -- the brief and research
  agree on the skill's purpose and required coverage.
- "No meaningful technical risk or trade-offs": not present -- the install
  gate and drift-risk findings show real trade-offs.
- "Problem is operational, not architectural": not present -- both
  candidate mechanisms require code/config-shape decisions, not just an
  operational runbook.

## Alternatives Considered

- **PRD**: ranked lower. Requirements were provided as input by the dispatch
  brief (goal, in-scope use cases, guardrails all specified), not identified
  during exploration -- the PRD vs. Design Doc tiebreaker ("Identified ->
  PRD, Given -> Design Doc") favors Design Doc directly. No stakeholder
  alignment gap was surfaced either.
- **Plan**: ranked lower. No PRD or design doc exists yet to decompose, and
  the technical approach (which delivery mechanism) is still open --
  exploration explicitly deferred that choice rather than confirming it.
  Per the framework, open architectural decisions must be resolved before a
  Plan can sequence work.
- **No Artifact**: ranked lower. Exploration produced multiple decisions
  during convergence (ruling out two candidate approaches) that need a
  permanent record before `wip/` is cleaned -- the framework's anti-signal
  for "any architectural, dependency, or structural decisions were made
  during exploration" is directly present.

## Deferred Types

None scored competitively.
