# Crystallize Decision: niwa-session-keep-alive

## Chosen Type

Full tactical scope chain (BRIEF -> PRD -> DESIGN -> PLAN), via `shirabe:scope`.
Terminal artifact: PLAN.

The user explicitly elected to run the full `shirabe:scope` workflow rather than
crystallize to a standalone Design Doc. The scored recommendation was Design Doc;
the user chose to wrap that design step inside the complete scope chain so the
feature is framed (BRIEF), specified (PRD), designed (DESIGN), and decomposed
(PLAN) in one pass. This exploration's findings/decisions are the input.

## Rationale

Design Doc was the top scoring supported type (6 signals, 0 anti-signals): what to
build is clear (opt-in keep-host-awake + supervised RC re-arm, stop on
close/archive), but HOW is the open question, with genuine architectural decisions
made during exploration that need a permanent home. The user broadened the target
from a lone design doc to the full scope chain, which is a superset: it still
produces the design, plus the upstream framing/requirements and the downstream
issue breakdown.

## Signal Evidence

### Signals Present (Design Doc)
- What to build is clear, how is not: keep-awake + supervised re-arm agreed; the
  mechanism (per-platform inhibitor, watcher architecture, re-arm path) is open.
- Technical decisions between approaches: sleep-inhibitor per OS; watcher vs
  niwa's daemon-free design; hook-based vs external supervisor; how to re-arm RC
  (relaunch `--resume` + `remoteControlAtStartup`).
- Architecture/integration questions remain: fitting a long-lived per-session
  watcher into a deliberately pull-based, daemon-free lifecycle.
- Multiple viable implementation paths surfaced during exploration.
- Architectural decisions made during exploration that must be recorded: nudge
  approach eliminated; keep-awake necessary; auto-reconnect as recovery;
  jobs-entry-gone as the close/archive proxy.
- Core question is "how should we build this?"

### Anti-Signals Checked (Design Doc)
- "What to build still unclear": not present — requirements were given by the user.
- "No meaningful technical risk/trade-offs": not present — real trade-offs exist.
- "Problem is operational, not architectural": not present — it's a lifecycle/
  architecture question.

## Alternatives Considered
- **Spike Report**: fits the two empirical unknowns (does the jobs entry survive a
  timeout exit; does an inhibitor preserve RC reachability overnight), but the
  exploration was broad rather than a focused feasibility test, and carries the
  "exploration was broad, not focused on a specific technical risk" anti-signal.
  Folded into the design/plan as a validation step, not the top artifact.
- **PRD**: a single coherent feature emerged, but requirements were provided as
  input (anti-signal) — tiebreaker (given vs identified) routes to design, not PRD.
  Now subsumed by the scope chain's PRD step.
- **No Artifact / Plan / Decision Record**: each carries a demoting anti-signal
  (decisions need documenting; approach still has open decisions; multiple
  interrelated decisions rather than one).

## Deferred Types
- None selected. (Prototype not applicable.)

## Handoff Note for /scope
Feed the scope chain with:
- `wip/explore_niwa-session-keep-alive_findings.md` (Accumulated Understanding)
- `wip/explore_niwa-session-keep-alive_decisions.md`
- The three round-1 research files under `wip/research/`
Key framing to carry forward: diagnosis is host-unreachable >~10 min (sleeping
laptop), not idle; feature = opt-in keep-host-awake (necessary) + supervised RC
re-arm (recovery), stopping on TUI-close/archive; the close/archive signal and the
"jobs entry survives?" question are the load-bearing unknowns to validate.
