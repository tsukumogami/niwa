# Crystallize Decision: niwa-mesh-reliability

## Chosen Type

Design Doc

## Rationale

The exploration started from nine open issues whose acceptance criteria
already state what each fix must achieve. The HOW is the open question.
Lead 2 surfaced three distinct injection mechanisms for plugin
propagation (#108: argv flag, env var, filesystem mirror), lead 4
surfaced two design options for the dangling-task lifecycle (#112: real
state vs. opt-in resurrect primitive), lead 2 also surfaced multiple
delivery options for the niwa-mesh skill itself (#97). These choices
are interdependent — the skill text (#97) must align with the runtime
fixes (#108, #109, #110, #111, #112), and the redelegate primitive
(#114) needs to know whether dangling is a state or a quarantine. A
single coordinated design records the decisions in lockstep and
sequences the implementation issues. Splitting into nine PRs would
force the same decisions to be revisited in each.

Status mode in --auto: `confirmed`. Evidence is unambiguous: signals
score is 6 with zero anti-signals, three points above the runner-up.

## Signal Evidence

### Signals Present

- **What to build is clear, but how to build it is not.** All nine issues
  state acceptance criteria. The architecture choices behind each fix
  are open: see findings sections "Lead 2: Worker spawn environment"
  (three injection points), "Lead 4: Task lifecycle and dangling" (two
  design options), and "Open Questions Surviving Round 1" in
  `wip/explore_niwa-mesh-reliability_findings.md`.
- **Technical decisions need to be made between approaches.** Plugin
  propagation, dangling-state shape, and skill delivery each have
  multiple viable mechanisms with different trade-offs.
- **Architecture, integration, or system design questions remain.** The
  unifying pattern identified in the findings ("niwa relies on
  filesystem-side-channel state the API doesn't read or relies on
  implicit discovery to find") is a design-shape claim that must be
  validated and committed to in writing.
- **Exploration surfaced multiple viable implementation paths.**
  Explicit in lead 2 and lead 4, less explicit but present in leads 5
  and 6.
- **Decisions were made during exploration that should be on record.**
  The decision to treat the cluster as one design (not nine bugfixes),
  the recognition that #92 and #109 are the same code-level bug, the
  selection of `daemon` sub-object over `Status` mutation for #111, the
  choice to register coordinator from `niwa_delegate` not just
  `await_task`/`check_messages` — all of these would be permanently
  lost when `wip/` is cleaned at PR-merge time unless captured in a
  permanent doc.
- **The core question is "how should we build this?"** Issue bodies
  state the what; the design must state the how.

### Anti-Signals Checked

- **What to build is still unclear (route to PRD first):** not present.
  All nine issues have stated acceptance criteria.
- **No meaningful technical risk or trade-offs:** not present. Multiple
  trade-offs surface in the findings.
- **Problem is operational, not architectural:** not present. The
  problems are architectural (state model, role registration, plugin
  propagation, contract surface).

## Alternatives Considered

- **Plan**: ranked lower because no PRD or design doc covers this topic
  yet; the dangling-state shape (#112) and plugin-propagation mechanism
  (#108) are still open architectural decisions. Plan is the natural
  follow-up after the design lands.
- **PRD**: ranked lower because the requirements were inputs to the
  exploration (the issue bodies), not its outputs. The "what" is
  settled; the "how" is the gap.
- **Roadmap**: ranked lower because the work is one coupled feature
  (mesh reliability), not multiple sequenced features. Cross-feature
  ordering is not the question.
- **No Artifact**: ranked lower because architectural decisions were
  made during exploration that future contributors need to understand,
  and `wip/` cleanup at merge time would lose them. Multiple
  contributors will work on the implementation issues across the
  design's milestone.
- **Decision Record**: ranked lower because at least three interrelated
  decisions need to be made (plugin propagation, dangling lifecycle,
  skill delivery). A Decision Record covers a single choice.
- **Spike Report**: ranked lower because feasibility is not the
  question; the codebase already shows that each fix is feasible.
- **VISION / Rejection Record / Competitive Analysis**: not applicable
  (project exists; conclusion is "proceed"; competitive analysis is
  private-repo-only).

## Deferred Types

None apply.
