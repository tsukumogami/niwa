# Crystallize Decision: inert-defaultmode-key

## Chosen Type

`/scope` -- the tactical chain, entering at the top.

## Candidacy

- `/execute`: **not a candidate.** No `docs/plans/` directory exists and no file
  in `docs/` carries `schema: plan/v1`. No qualifying PLAN, so the arm is off
  the board and is not offered as an alternative.
- Competitive analysis: **not a candidate.** The scope file's `## Visibility`
  section reads `Public`, which removes the category from stage 1.

## Rationale

The exploration converged on one bounded feature -- niwa's permission posture
travelling through channels that work -- and left both the requirements and the
approach open.

It is not a decision record, though it looks like one at first glance. The
brief posed a four-way choice of shapes, and the exploration did settle that
choice. But settling it turned out to require a second, larger finding: the
value niwa writes is not inert, it is a live regression that leaves dispatched
sessions *more restrictive* than writing nothing, because a project-scope
`bypassPermissions` wins the scope merge and is then downgraded to `default`,
destroying whatever the developer's own user settings contributed. The shape
choice is one input among several that a build still needs, which is the
framework's own tiebreaker for a chain over a decision record.

It is not a spike report either. Feasibility was never the open question; every
candidate shape was buildable. The exploration answered "what should we build,
and how", and the answer commits someone to building it.

The work spans a materializer change, a dispatch-input change, a dead-code
deletion that a shipped design doc already authorised and nobody performed, and
seven committed documents asserting a mechanism that no longer exists. Its
ordering is constrained -- the producer and the reader have to move in one
commit or the two `@critical` scenarios that are the only real regression net
stop protecting anything. Twelve decision blocks accumulated in `wip/`, which
is cleaned before any PR merges, so they need a durable home regardless.

## Stage 1 Evidence

### Signals Present -- A Chain (score 6, no anti-signals)

- **Converged on something someone will build**: the write-side fix (stop
  emitting a value that cannot take effect) and the read-side fix (feed the
  derivation from the effective config the provisioning call already computes
  and discards).
- **Requirements, architecture, or sequencing questions remain open**: nothing
  is written down about what the materializer should emit for a `bypass`
  declaration once the permissive value is withdrawn, nor about how the
  effective config reaches the derivation, nor about whether the write-side and
  read-side fixes land together or in sequence.
- **Decisions made during exploration need a durable home and downstream
  work**: D1-D12 in `wip/explore_inert-defaultmode-key_decisions.md`, including
  three eliminations that a future contributor would otherwise re-propose.
- **Multiple stakeholders need alignment**: a sibling session is working the
  `--settings` slot from another direction, and this exploration's measurement
  that the slot has a phantom occupant changes that session's premise.
- **A scope boundary emerged, not just an answer**: permissive values only, not
  the key; the `workspace.toml` input surface is a separate compatibility
  decision; `niwa watch`'s ask posture is sound and stays untouched; the
  remote-control flag switch is separate work.
- **The core question is "what do we build, and how?"**

### Anti-Signals Checked -- A Chain

- Nothing was left to build: **not present.**
- The whole output is one choice between named options: **not present** -- the
  shape choice sits alongside a regression fix, a dead-code deletion, and a
  documentation correction.
- The output is a feasibility verdict nobody has committed to acting on:
  **not present.**
- The conclusion is that the work should not happen: **not present.**
- Findings center on external products rather than something to build:
  **not present.**

### Signals and anti-signals -- other categories

**Decision Record (score 3, 1 anti-signal, demoted).** Signals present: a
single decision with clear options was evaluated; the core question was partly
"which option and why"; future contributors will need the reasoning; specific
alternatives were compared with trade-offs. Anti-signal present: *multiple
interrelated decisions came with work attached* -- twelve of them, with a
materializer change, a dispatch change, a deletion and a documentation sweep
behind them.

**Spike Report (score 0, 2 anti-signals, demoted).** Signals present: a
time-boxed investigation produced concrete findings; specific technical risks
were identified and tested. Anti-signals present: *the question is "what should
we build?"* rather than "can we?"; and *exploration was broad, not focused on a
specific technical risk* -- nine leads across the scope rule, the code graph,
the test surface, the argv channel, instance metadata, and external prior art.

**Rejection Record (score 1, no anti-signals).** Only signal present: the
investigation was multi-round. There is no rejection conclusion; the
exploration's verdict is proceed.

### Ranking

| Category | Score | Status |
|---|---|---|
| A Chain | 6 | top |
| Rejection Record | 1 | |
| Decision Record | 3 | demoted (1 anti-signal) |
| Spike Report | 0 | demoted (2 anti-signals) |
| Competitive Analysis | -- | not a candidate (public repo) |

## Stage 2 Evidence

Stage 2 ran because "a chain" was the top-ranked stage-1 category.

### Signals Present -- `/scope` (score 10, no anti-signals)

- **A single coherent feature emerged**: making niwa's declared permission
  posture reach a worker through channels that are honored, and stopping the
  settings document from carrying a value that harms the session.
- **Requirements are unclear or contested**: the brief's own framing was wrong
  in two directions -- it named the wrong release and it treated the key as
  merely inert.
- **User stories or acceptance criteria are missing**: nothing states what a
  `bypass` declaration should produce in each of the three materialized
  documents once the permissive value is withdrawn.
- **What to build is clear, but how to build it is not**: the write-side fix is
  one line; the read-side fix requires surfacing the effective config through
  `provisionResult`, and whether both land together is open.
- **Technical decisions need to be made between approaches**: rename into the
  `keepAliveOnDispatch` family versus stopping the round trip; the exploration
  recommends the latter but the trade-off belongs on the record.
- **Architecture and integration questions remain**: what `provisionResult`
  surfaces, and whether the derivation moves earlier so the SessionStart hook
  and `niwa watch` can act on the same resolved posture.
- **Exploration surfaced multiple viable implementation paths**: four shapes,
  two survived round 1, one is recommended.
- **Architectural decisions were made during exploration that should be on
  record**: three eliminations, each with reasoning a future contributor would
  otherwise redo.
- **Multiple stakeholders need alignment**: the sibling session's premise
  changes.
- **The core question is "what should we build, and how?"**

### Anti-Signals Checked -- `/scope`

- Multiple independent features whose order affects delivery: **not present.**
  The remote-control flag switch and the argv single-owner guard are riders or
  separate work, explicitly out of this feature's scope.
- One person can act on this without a written contract: **not present.**
- A qualifying PLAN already covers this work: **not present.**
- The exploration produced no work: **not present.**

### Signals and anti-signals -- other entry points

**File an issue (score 0, 3 anti-signals, demoted).** Anti-signals present:
*others need documentation to build from* -- the eliminations and the
clobbering measurement are the reasoning, not the change; *architectural,
dependency, or structural decisions were made during exploration*; *scope was
debated across rounds* -- the framing narrowed twice, once from "stop writing
the key" to "stop writing the values that cannot work", and once from
"misleading document" to "live regression".

**`/charter` (score 0, 3 anti-signals, demoted).** Anti-signals present: *the
project already exists and the question is about its next feature*; *the work
is one bounded feature, however large*; *specific users and needs are already
identified and uncontested*. No strategic justification was sought or produced,
and there is no set of features to sequence.

### Ranking

| Entry point | Score | Status |
|---|---|---|
| `/scope` | 10 | top |
| File an issue | -3 | demoted (3 anti-signals) |
| `/charter` | -3 | demoted (3 anti-signals) |
| `/execute` | -- | not a candidate (no qualifying PLAN) |

## Tiebreakers Applied

None were needed -- both stages resolved with a margin wider than one point and
the top-ranked arm carried no anti-signals. Two tiebreakers were checked
anyway, because the topic invites them:

- **A chain vs Decision Record**: the framework asks whether the exploration's
  entire output is one choice between named options. It is not. The shape
  choice is one input among several a build still needs, so the chain arm
  takes it and records the choice as it goes.
- **A chain vs Spike Report**: the framework asks whether the exploration
  answered "can we?" and stopped, or whether the answer committed someone to
  building. It committed. The measurement that a project-scope
  `bypassPermissions` actively suppresses a developer's own posture is a defect
  report, not a feasibility verdict.

## Alternatives Considered

- **Decision Record** -- Ranked lower because the exploration's output is not
  one choice. It settled the shape question, but it also found a regression, a
  dead file, an input-vocabulary defect that predates the deprecation, and
  seven stale documents. A decision record would capture the shape and drop the
  rest, and `wip/` is cleaned before merge, so the rest would be lost.
- **Spike Report** -- Ranked lower because feasibility was never the open
  question. Every candidate shape was buildable; the exploration was choosing
  among them and then found something larger.
- **File an issue** -- Ranked lower because three structural decisions were
  taken during the exploration and the scope was renegotiated twice. An issue
  would carry the change and lose the reasoning, and the next contributor would
  re-propose annotating the key in place, which is schema-illegal.
- **`/charter`** -- Ranked lower because this is one bounded feature inside a
  project that already exists, with no set of features whose order affects
  delivery.

## Deferred Type

Not applicable. The prototype type scored no signals: nothing here needs a
proof of concept, because the two probes already measured the behavior in
question against a real Claude Code build.
