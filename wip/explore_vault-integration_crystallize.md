# Crystallize Decision: vault-integration

## Chosen Type

PRD (Product Requirements Document)

## Rationale

User explicitly requested a PRD as the exploration's output. The research
output independently supports that choice:

- **Requirements emerged during exploration** rather than being fully
  specified upfront. The scoping convention (source-org with
  `vault_scope` escape hatch), the conflict-resolution policy (personal-
  wins with `team_only` opt-in), the bootstrap UX (age key pair flow vs
  `infisical login`), and the graceful-degradation stance (fail hard +
  flags) all were produced by this exploration. Those belong in a
  requirements artifact, not a design doc.

- **Stakeholder alignment is needed.** This feature has three distinct
  user archetypes (indie solo developer with personal vault, small team
  with shared vault, team-lead rotating keys after a member leaves). The
  PRD's persona + story + acceptance-criteria format is the right vehicle
  for aligning on all three simultaneously.

- **The technical approach is clear but has open requirements
  questions.** The architecture decisions the research settled (Option A
  scoping, Option 3 schema, pluggable backend, 12 security invariants)
  are enough to *design against* but a PRD still needs to nail: which
  secrets are in scope for v1 (team API tokens only? PATs?), what the
  plaintext-migration UX looks like, how the public-repo guardrail
  detects public remotes, and whether `niwa vault import` ships in v1.

- **"Should this project exist?" is NOT the question.** Vault integration
  is clearly within niwa's scope — the private `dot-niwa` repo problem is
  concrete and user-reported. This disqualifies VISION. No entire new
  project is being proposed; this is a feature of niwa.

## Signal Evidence

### Signals Present (PRD)

- **Single coherent feature emerged from exploration**: a layered vault
  integration with pluggable providers, shipping sops first.
- **Requirements are unclear or contested**: scoping semantics,
  conflict-resolution, bootstrap UX all had viable options; the research
  resolved them but the results need a requirements artifact to capture
  and validate with stakeholders.
- **Core question is "what should we build and why?"**: vault provider
  choice, schema shape, security invariants, and migration story all
  answer variants of this question.
- **User stories or acceptance criteria are missing**: the PRD will
  enumerate them (indie dev, small team lead, departing team member,
  public-repo migration, etc.).

### Anti-Signals Checked

- **Requirements were provided as input**: not quite — user gave the
  *problem* but not the requirements. The research produced the
  requirements shape.
- **Multiple independent features**: not present — this is one coherent
  feature.
- **Independently-shippable steps that don't need coordination**: not
  present — the scoping, schema, and security pieces are interdependent.

## Alternatives Considered

- **Design Doc**: Scored second. The architecture is well-specified by
  the research. But several requirements questions remain (v1 secret
  scope, migration UX, public-repo guardrail exact rules) that a design
  doc would treat as "settled" and bypass. PRD is the right stage first.
  Design doc follows.

- **Plan**: Not viable. No upstream artifact exists; requirements and
  architecture must be captured before a plan can sequence work.

- **No Artifact**: Not viable. The research made several architectural
  decisions (pluggable backend, Option A scoping, Option 3 schema,
  security invariants) that must live past `wip/` cleanup. A PRD is the
  minimum artifact that records them permanently.

- **VISION**: Not viable. Niwa already exists; this is a feature, not a
  strategic project direction.

## Deferred Types

None applicable.
