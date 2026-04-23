# Crystallize Decision: workspace-config-sources

## Chosen Type

**PRD**

## Rationale

The exploration *identified* requirements rather than received them as input.
The user opened with "let's look at how these different git config repos
work and try to determine how they should be configured. This may lead into
a PRD before we get into a design." Round 1 produced the substantive
requirements list (snapshot model, subpath sourcing, convention-based
discovery, GitHub-tarball fetch, three-marker root probe, registry/state
schema bumps, drop `--allow-dirty`, replace two `.git/`-dependent code
paths). The remaining questions are PRD-shaped: what's in v1's scope and
why, how the user-facing contract reads, what the migration story commits
to. The technical "how" decisions (slug delimiter, lock file location,
snapshot placement) belong in a downstream design doc that the PRD's
acceptance criteria will constrain.

The framework's disambiguation rule applies directly: "Exploration
surfaced both requirement gaps AND technical questions. If the user
doesn't know what to build AND doesn't know how to build it, favor PRD.
Requirements come first." Both kinds of gap are present here, with the
"what's in v1" gap dominating.

## Signal Evidence

### Signals Present

- **Single coherent feature emerged from exploration**: a unified
  subpath-aware config-sourcing model with snapshot materialization,
  spanning the team config, personal overlay, and workspace overlay
  uniformly. Not a bundle of features.
- **Requirements are unclear or contested**: v1 scope is unsettled —
  multi-host adapter coverage (L2 open question), migration command
  (L7 open question), `vault_scope = "@source"` shorthand (L6 open
  question), `niwa.toml` `content_dir` requirement (L4 open question)
  all need a stake-in-ground answer before design.
- **Multiple stakeholders need alignment on what to build**: weaker
  signal than the others, but real — adopting this in
  `tsukumogami/vision` and `codespar/codespar-web` (the two named
  brain repos) commits the user to a slug syntax and migration
  ritual that those orgs' contributors will see.
- **The core question is "what should we build and why?"**: the
  exploration produced a coherent answer to "what" (subpath +
  snapshot + convention discovery) but the boundary of v1 is still
  open. The PRD frames that boundary.
- **User stories or acceptance criteria are missing**: neither
  exists today. The findings list decisions but not testable
  acceptance criteria.

### Anti-Signals Checked

- **Requirements were provided as input**: NOT PRESENT. The user
  came in with a problem statement (issue #72) and a directional
  hunch (brain-repo pattern); /explore produced the requirements.
- **Multiple independent features that don't share scope**: NOT
  PRESENT. The redesign is a single coherent feature with internal
  parts that must land together (registry schema + snapshot model +
  slug parser + discovery + state v3 + the two `.git/`-replacement
  fixes). Splitting them creates intermediate states that don't fix
  #72 and don't enable subpath sourcing.
- **Independently-shippable steps that don't need coordination**:
  NOT PRESENT. The state schema bump, registry schema change, and
  snapshot materialization are interlocked.

## Alternatives Considered

- **Design Doc**: Strong candidate. Anti-signal partially present —
  "what to build is still unclear" applies to v1 scope questions,
  not to the core feature. Loses the tiebreaker against PRD because
  the framework rule "requirements identified by /explore → PRD"
  applies cleanly. The right sequence is PRD first (lock v1 scope
  and acceptance criteria), then Design Doc (resolve the slug
  delimiter, lock file location, snapshot placement, instance.json
  placement, ref-resolution-timing decisions).

- **Plan**: Demoted. Anti-signal present — no upstream PRD or
  design doc exists for this topic. Plan is the wrong artifact when
  decisions are still pending.

- **No Artifact**: Demoted. Multiple anti-signals present — others
  need documentation to build from; architectural decisions were
  made during exploration; multiple people will work on this.

- **Decision Record**: Demoted. Anti-signal present — multiple
  interrelated decisions need a design doc, not a single
  architectural choice.

- **Spike Report**: Demoted. Not a feasibility question; we already
  know GitHub tarball + tar extraction is feasible (L2 ran a working
  experiment).

- **Roadmap**: Demoted. Single coherent feature, not a sequence of
  features needing ordering.

- **VISION**: Demoted. Project exists. Anti-signal: "Project already
  exists and question is about its next feature."

- **Rejection Record**: Demoted. Exploration converged on a positive
  direction, not a rejection conclusion.

- **Competitive Analysis**: Demoted. Anti-signal — public repo
  (Competitive Analysis is private-only per framework).

## Deferred Types

None applicable.

## Auto-Mode Note

In `--auto` mode the user-confirmation step is skipped and the recommendation
is followed per the research-first protocol. The user explicitly flagged the
PRD-then-design trajectory in the original command argument, which corroborates
the framework's verdict.
