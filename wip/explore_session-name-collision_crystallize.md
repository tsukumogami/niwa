# Crystallize Decision: session-name-collision

## Chosen Type

`/scope` — the tactical chain, entered at the top.

Selected in `--auto` mode via the research-first protocol. No `AskUserQuestion`
was raised: this exploration runs as a dispatched background worker with no
interactive author, so the recommendation was formed from the evidence, followed,
and recorded here.

## Candidacy

- **`/execute`: not a candidate.** No `docs/plans/` directory exists in the repo,
  and no file anywhere under `docs/` carries `schema: plan/v1` frontmatter. There
  is no qualifying PLAN, so the arm is absent from the ranking and from the
  alternatives below.
- **Competitive analysis: not a candidate.** `## Visibility` in the scope file
  reads `Public` (from `## Repo Visibility: Public` in niwa's CLAUDE.md). The
  category is removed from stage 1 entirely.

## Rationale

The exploration did not end at an answer; it ended at a feature with three
unsettled design choices and a pile of decisions that need somewhere durable to
live.

What it settled is substantial. The bug is real and confirmed from both ends of
the exec — statically through `internal/cli/dispatch.go:458` → `:564` → `:985`,
and first-hand by this session reading back its own display name as
`session_name_collision` while running in `tsuku+session_name_collision-21dbe5c8`.
It is also already happening: seven names shared by fifteen sessions in a live
115-peer listing, plus a hand-applied `legacy-` rename in the machine's job store.
The approach space collapsed to one survivor, unique-by-construction, after
detect-and-warn was gutted by the discovery that roughly 100 of 115 peers are
off-machine and detect-and-auto-suffix was rejected as a racing promise the
mechanism cannot keep. Placement is settled too: the suffix goes in `runDispatch`
between `:459` and `:564`, a constraint four leads reached independently.

What it did not settle is what makes this a chain rather than a terminal outcome.
The suffix width is a live trade-off with evidence on both sides — reuse the
instance's 8 hex for pair-legibility, or match the harness's own 6-hex row id.
Whether `niwa dispatch` should now print the session name is an open requirement,
and a real one: today it prints only the UUID and the instance path, so adding a
suffix without adding that line would make the address genuinely harder to find.
And whether `niwa watch`'s two call sites and the unnamed-dispatch case ride along
or are filed separately is a scope-boundary question with a cost either way. Those
are requirements-and-approach questions, which is exactly what the tactical chain
exists to walk.

The decisions file holds eleven settled choices, including two eliminated
approaches with their reasoning. `wip/` is cleaned before any PR merges. Filing an
issue and walking away would destroy that reasoning, and the next person to look
at dispatch naming would re-derive the same false confidence the design doc
already invites at `DESIGN-instance-dispatch.md:172-174`.

## Stage 1 Evidence

### A Chain — score 6, no anti-signals

**Signals present:**

- *Exploration converged on something someone will build*: unique-by-construction
  session naming, with the change site identified to the line.
- *Requirements, architecture, or sequencing questions remain open*: suffix width,
  whether dispatch prints the name, whether watch's call sites are included.
- *Decisions made during exploration need a durable home and downstream work*:
  eleven decisions in `wip/explore_session-name-collision_decisions.md`, including
  two rejected approaches; `wip/` does not survive the merge.
- *Multiple stakeholders need alignment on what to build*: a sibling session's
  peer-message pre-approval work treats this as arguably a prerequisite, so the
  outcome has a consumer outside this exploration.
- *A scope boundary emerged, not just an answer*: three adjacent collisions
  (`niwa watch`'s by-construction slug, the `/dispatch` brief-file overwrite, the
  unnamed-dispatch case) were explicitly recorded and placed outside.
- *The core question is "what do we build, and how?"*: after round 1, yes — the
  "is it real" and "is it worth it" halves are answered.

**Anti-signals checked:**

- *Nothing was left to build*: not present.
- *The whole output is one choice between named options*: not present — three
  design choices plus implementation work.
- *The output is a feasibility verdict nobody has committed to acting on*: not
  present.
- *Findings center on external products rather than something to build*: not present.
- *The conclusion is that the work should not happen*: not present.

### Decision Record — score 2, demoted

**Signals present:** a single decision with clear options was evaluated
((a) always suffix / (b1) detect-and-warn / (b2) detect-and-auto-suffix /
(c) nothing); future contributors need to understand why; the exploration compared
specific alternatives with trade-offs.

**Anti-signal present:** *multiple interrelated decisions came with work attached*.
Three open design choices remain and each carries implementation consequences, so
the output is not one choice standing alone.

### Spike Report — score 0, demoted

**Signals present:** a time-boxed investigation produced concrete findings;
specific technical risks were identified and tested (the TOCTOU race, off-machine
blindness, the AST guardrails).

**Anti-signals present:** *the question is "should we do this?" or "what should we
build?"* — the brief explicitly asked for the worth-fixing case to be argued either
way. And *exploration was broad, not focused on a specific technical risk* — seven
leads spanning the code path, harness documentation, downstream consumers,
detectability, architectural guardrails, and readability.

### Rejection Record — score 0

No signals present: the exploration reached a "proceed" conclusion with no
rejection evidence, no adversarial lead was run, and no blocking failure mode was
identified. No anti-signals present either, but nothing supports it.

### Ranking

| Category | Score | Status |
|---|---|---|
| A Chain | 6 | top, no anti-signals |
| Rejection Record | 0 | no anti-signals |
| Decision Record | 2 | demoted (1 anti-signal) |
| Spike Report | 0 | demoted (2 anti-signals) |
| Competitive Analysis | — | not a candidate (public repo) |

### Tiebreakers Applied

None required at stage 1: "A Chain" leads by 6 points over the nearest undemoted
category. For the record, the *A chain vs Decision Record* tiebreaker would have
resolved the same way — the suffix-width choice is one input among several a build
still needs, not the exploration's entire output.

Stage 2 runs because "A Chain" is the top-ranked stage-1 category.

## Stage 2 Evidence

### `/scope` — score 9, no anti-signals

**Signals present:**

- *A single coherent feature emerged*: make dispatched session names unique by
  construction.
- *Requirements are unclear or contested*: suffix width; whether to print the name.
- *Multiple stakeholders need alignment on what to build*: the sibling pre-approval
  exploration depends on the outcome.
- *User stories or acceptance criteria are missing*: none written anywhere.
- *What to build is clear, but how to build it is not*: the approach is settled,
  the parameters are not.
- *Technical decisions need to be made between approaches*: 8-hex reuse versus
  6-hex fresh, and whether `dispatchNameSuffix`'s signature changes to surface the
  token.
- *Architecture, integration, or system design questions remain*: whether `niwa
  watch`'s two call sites are included, given its slug doubles as a staged-record
  filename and a human-typeable handle.
- *Exploration surfaced multiple viable implementation paths*: four approaches
  evaluated, two eliminated with reasons.
- *Architectural or technical decisions were made during exploration that should be
  on record*: eleven, in the decisions file, facing `wip/` cleanup.

**Anti-signals checked:**

- *Multiple independent features whose order affects delivery*: not present — the
  three adjacent collisions were deliberately excluded, not sequenced.
- *One person can act on this without a written contract*: not present. The code
  change is small, but the eliminated approaches and the design-doc correction need
  a durable home, and a sibling session consumes the outcome.
- *A qualifying PLAN already covers this work*: not present — none exists.
- *The exploration produced no work*: not present.

### File an Issue — score 1, demoted

**Signals present:** simple enough to act on directly (the change is one
expression); one person can implement it; the exploration was a single round.

**Anti-signals present:** *any architectural, dependency, or structural decisions
were made during exploration* — eleven of them, two eliminating whole approaches.
And *others need documentation to build from* — the sibling session depends on the
outcome, and `DESIGN-instance-dispatch.md:172-174` needs correcting so the next
reader does not re-derive the same false confidence.

### `/charter` — score -3, demoted

**Signals present:** none material. The nearest is that a sibling session's
delivery depends on this landing, which is a dependency between two work items but
not a multi-feature sequencing question.

**Anti-signals present:** *the project already exists and the question is about its
next feature*; *the work is one bounded feature, however large*; *specific users and
needs are already identified and uncontested*; *no sequencing question — the items
have no order that affects delivery*.

### Ranking

| Entry point | Score | Status |
|---|---|---|
| `/scope` | 9 | top, no anti-signals |
| File an issue | 1 | demoted (2 anti-signals) |
| `/charter` | -3 | demoted (4 anti-signals) |
| `/execute` | — | not a candidate (no qualifying PLAN) |

### Tiebreakers Applied

None required: `/scope` leads by 8 points and is the only undemoted entry point.
The *`/scope` vs file an issue* tiebreaker would have resolved the same way — one
person cannot act on this without a written contract, because the reasoning that
eliminated detect-and-warn and detect-and-auto-suffix lives only in `wip/` and dies
at merge.

## Alternatives Considered

- **File an issue** — the closest real alternative, and tempting: the code change
  is genuinely one expression at a known line. Ranked lower because the exploration
  eliminated two whole approaches with reasoning that exists nowhere durable, and
  because three design choices remain open. An issue would carry the fix and lose
  the argument.
- **Decision Record** — fits the shape of the suffix-width question well, and if
  that were the only open item this would be the answer. Ranked lower because it is
  one of three open choices, all with work attached, rather than the exploration's
  whole output.
- **Spike Report** — the investigation was time-boxed and produced concrete
  technical findings, but feasibility was never in doubt and the brief asked a
  should-we question, not a can-we one. Demoted on both anti-signals.
- **Rejection Record** — scored zero. The exploration concluded "proceed", on two
  independent lines of observed evidence.
- **`/charter`** — the project exists and this is one bounded feature. Demoted on
  four anti-signals.

## Deferred Type

**Prototype** was checked and does not apply. The question was never "does this
work?" — the mechanism is understood and the change site is identified to the line.
Nothing here would be answered faster by building a proof of concept than by
settling the three open parameters.
