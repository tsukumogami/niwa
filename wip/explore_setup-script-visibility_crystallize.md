# Crystallize Decision: setup-script-visibility

## Chosen Type

Design Doc — produced as an in-place amendment to the existing
`docs/designs/current/DESIGN-post-clone-scripts.md`, not as a new `DESIGN-*.md` file.

## Rationale

Requirements were handed to this exploration, not discovered by it: issue #239 is the
specification and its acceptance criteria are explicit. What the exploration actually produced
is a set of technical decisions with real trade-offs — stream versus buffer-and-replay for
script output, which of three exit-code options to adopt, whether to express opt-in fatality as
a CLI flag or a config action, and where in the `Create`/`Apply` sequence a fatal setup failure
may safely be raised without deleting the instance. Every one of those needs to survive the
branch, and `wip/` does not.

The doc that should carry them already exists and is the doc that is currently *wrong*.
`DESIGN-post-clone-scripts.md` Decision 2 asserts two things the code does not do (stdout and
stderr are printed with a repo prefix; script names are printed before execution) and chose a
failure policy whose consequence is now being narrowed. Writing a separate new design would
leave the false claims in place and split the record across two files — which is precisely the
drift that produced this issue. The repo also has no `docs/decisions/` directory and no ADR
format in its doc validator, but has amended a `Current` design in place seven times, so an
amendment is the established move and a first-ever ADR would be a convention introduced as a
side effect of a bugfix.

A one-line correction to `DESIGN-clone-output-ux.md`'s contradictory `r.Status` interface
comment travels with it, so the two designs stop disagreeing about the same function.

## Signal Evidence

### Signals Present

- **Technical decisions need to be made between approaches**: stream-through-`Log` versus
  `runGitWithReporter`-style buffer-and-attach-to-error; the three exit-code options from the
  issue; `--strict-setup` flag versus `config.Action` cascade.
- **Architectural or technical decisions were made during exploration that should be on
  record**: the strongest signal here. Rejecting default non-zero exit rests on a non-obvious
  mechanical finding (`Create` runs `os.RemoveAll(instanceRoot)` on any pipeline error; the
  SessionStart-hook path discards the instance on error). A future contributor reading only the
  code would re-propose option 2 exactly as the issue author did.
- **Architecture and integration questions remain**: where a `fail` action's error is raised
  relative to `SaveState`, and whether `Create` returns the instance path alongside it.
- **Exploration surfaced multiple viable implementation paths**: prior art genuinely splits
  (git/direnv stream; npm/pre-commit buffer and replay), and niwa's own two design docs point
  one way while the code went the other.
- **The core question is "how should we build this?"**: what to build was never in question.

### Anti-Signals Checked

- *What to build is still unclear* — not present. Issue #239 states the problem, the citations,
  the measured evidence, and six acceptance criteria.
- *No meaningful technical risk or trade-offs* — not present. The rejected option would have
  deleted provisioned instances and orphaned hook-created ones.
- *Problem is operational, not architectural* — not present in the decisive half. The output bug
  alone would be operational; the failure-policy and config-cascade decisions are not.

## Alternatives Considered

- **Plan** (3 signals, 0 anti-signals): the work is well enough understood to decompose, and an
  upstream design doc nominally exists. Ranked lower because that existing doc does not yet
  cover the decisions this exploration made — it contradicts them. A plan can sequence work but
  cannot record why default-fatal was rejected. The right order is amend, then decompose; the
  Plan follows immediately rather than instead.

- **Decision Record** (4 signals, 1 anti-signal, demoted): the exit-code question on its own is
  a textbook single decision with three named options and real trade-offs. Demoted by the
  "multiple interrelated decisions need a design doc" anti-signal — output routing, the counted
  summary line, the config action, and redaction are one coherent change, not four. Independently
  disqualified by the repo having no ADR convention to write into.

- **PRD** (demoted): the "requirements were provided as input to the exploration" anti-signal is
  squarely present. Issue #239 is the requirements contract.

- **No Artifact** (demoted): two anti-signals present — architectural decisions were made during
  exploration, and the acceptance criteria explicitly require the reasoning to live somewhere
  other than a PR body.

- **VISION, Roadmap, Spike Report, Rejection Record** (0 or negative): niwa exists, this is a
  single feature needing no cross-feature sequencing, feasibility was never in doubt, and the
  conclusion is "proceed."

- **Competitive Analysis**: disqualified outright — private repos only, and this is a public repo.

## Deferred Types

- **Prototype**: not applicable. The defect was already reproduced by measurement during
  exploration; there is nothing left to prove by building a throwaway.
