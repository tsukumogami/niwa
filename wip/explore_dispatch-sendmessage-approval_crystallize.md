# Crystallize Decision: dispatch-sendmessage-approval

## Chosen Type

/scope

## Candidacy

- /execute: not a candidate. No `docs/plans/PLAN-*.md` exists and no `.md` in the
  repo carries `schema: plan/v1` frontmatter.
- Competitive analysis: not a candidate (public).

## Rationale

The exploration converged on one bounded feature: dispatched sessions that
receive peer messages without a human approving each one, gated by a
per-machine setting that is off by default and overridable per dispatch. It ran
two discover-converge rounds and made a series of architectural decisions that
currently live only in `wip/` and are lost when the branch closes: that the
mechanism is `crossSessionInbound: "accept"` delivered through `--settings`;
that the materializer, `PreToolUse` hook, tool-permission, and managed-settings
channels are all ruled out, each for a recorded reason; that a merged
settings-document builder is mandatory because a repeated `--settings` is
measured to be last-wins and silent; that the re-entry path must carry the
grant; and that the control surface copies the `--keep-alive` precedent.

Design questions remain open (whether an instance-settings rung exists, the
merged builder's shape, the key's name) and no acceptance criteria exist yet.
The user chose to ship the re-entry bug fix first and the feature after it; a
PLAN is where that sequencing belongs, with the re-entry fix as its first issue.

## Stage 1 Evidence

### Signals Present

- Exploration converged on something someone will build: the dispatch-side
  grant, the merged settings-document builder, and the re-entry fix.
- Requirements, architecture, or sequencing questions remain open: the
  instance-settings rung, the builder's contributor interface, the key's name,
  and the ordering of the fix and the feature.
- Decisions made during exploration need a durable home and downstream work:
  eight round-1 decisions and seven round-2 decisions in the decisions file.
- A scope boundary emerged, not just an answer: the half niwa implements
  (every session it launches) versus the half it may only state (the user's own
  `~/.claude/settings.json`), with niwa barred from both detecting and writing
  the latter.
- The core question is "what do we build, and how?"

### Anti-Signals Checked

- Nothing was left to build: not present.
- The whole output is one choice between named options: not present.
- The output is a feasibility verdict nobody has committed to acting on: not
  present; the user committed to both the fix and the feature.
- Findings center on external products: not present.
- The conclusion is that the work should not happen: not present.

### Ranking

- A Chain: 5
- Rejection Record: 1 (multi-round only; no rejection conclusion)
- Spike Report: 3 (demoted: "the approach is known" and "the question is what
  should we build" both present)
- Decision Record: 0 (demoted: multiple interrelated decisions came with work
  attached)

## Stage 2 Evidence

Stage 2 ran because stage 1 returned a chain.

### Signals Present

- A single coherent feature emerged: unattended peer delivery for dispatched
  sessions, with the re-entry fix as its precursor.
- Requirements are unclear or contested: whether the instance-settings rung
  exists is contested between two leads.
- User stories or acceptance criteria are missing.
- Technical decisions need to be made between approaches: the merged builder's
  shape; how the default-fill-only remote-control injection coexists with an
  always-emitted contributor.
- Architecture or integration questions remain: the builder must feed both the
  launch path and `dispatch_reentry.go`.
- Architectural decisions were made during exploration that should be on record.
- The core question is "what should we build, and how?"

### Anti-Signals Checked

- Multiple independent features whose order affects delivery: not present. The
  re-entry fix is a defect in the same path the feature extends, not a separate
  feature; its ordering belongs inside one PLAN.
- One person can act on this without a written contract: not present, given
  the number of recorded decisions and the open design questions.
- A qualifying PLAN already covers this work: not present.
- The exploration produced no work: not present.

### Ranking

- /scope: 7
- File an issue: 2 (demoted: architectural decisions were made; scope was
  debated across rounds)
- /charter: 1 (demoted: the project already exists and this is its next
  feature; the work is one bounded feature)

## Tiebreakers Applied

- None needed; the margin after demotion is well over one point.
- The `/charter` vs `/scope` multi-feature boundary was checked anyway, because
  the deliverable has two sequenced parts. It resolves to `/scope`: the parts
  are a defect fix and the feature that depends on the same code path, not two
  separately sequenced features.

## Alternatives Considered

- **File an issue for the re-entry fix only**: fastest relief, and the fix is
  small and located. Ranked lower because it covers only the first half of what
  the user chose and would leave every architectural decision behind the
  feature in `wip/`.
- **/charter**: ranked lower because niwa exists and this is one feature, not a
  portfolio of features needing an order.
- **Spike Report**: feasibility was answered, but the answer committed the user
  to building, so it is an input to the chain rather than its terminal artifact.
