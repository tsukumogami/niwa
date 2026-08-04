# Exploration Decisions: orchestration-learnings

## Round 1 — scoping (before research)

- **Home repo is `niwa`**: it launches the background sessions, ships the `/dispatch` root
  skill, and already documents session lifecycle. Ruled out shirabe (no concept of niwa
  dispatch) and private/tools (not a path any dispatch loads).
- **`_common.md` ships from niwa but stays overrideable**, in the spirit of the shirabe
  extension mechanism.
- **Review standard gets the full treatment**, not just scattered rules.
- **Skill shape left open** for a researched recommendation.
- **Leads 4 and 5 run directly, not delegated**: the session under study lost three of four
  status-gathering agents to silent stalls while direct queries answered the same questions
  in under a minute. Applying its own finding.

## Round 1 — convergence (after research, author-answered)

- **Audience is any niwa user**, not this workspace. Consequence: no tsuku issue numbers or
  in-house war stories as load-bearing content; criteria must be stateable to a stranger
  with the evidence base characterised rather than cited.
- **The centred failure is wrong chain shape / launch mode.** Everything else is
  supporting. Ranking was not recoverable from evidence; it is the author's call.
- **Success is "the next fleet ran without you watching."** Reconciled with the centred
  failure rather than treated as a separate goal: a badly-shaped or badly-launched worker
  is precisely what forces the human to watch. Under-shaped work produces a PR implementing
  the first plausible mechanism; over-shaped work burns a cycle; the wrong launch mode lets
  an agent confidently resolve a framing question the author would have answered
  differently, discovered only at PR time.
- **The deliverable is a launch-decision aid.** The after-launch loop is its safety net for
  when the decision was wrong, not a co-equal product. This resolves the "tbd" the author
  left on the artifact-type question.
- **Chain shape is expressed as tool-neutral framing levels.** `explore → scope → execute`,
  `work-on` and `decision` are shirabe's skill names; niwa cannot assume shirabe is
  installed, and shipping them as doctrine would be niwa teaching another tool's API. niwa
  owns the decision — how much framing the work needs before implementation, at three
  levels, selected by properties of the work — with a short mapping note for workspaces
  that use a workflow plugin.
- **Scope confirmed at all four pieces**: launch decision, shipped standing agreement,
  harness-mechanics reference, after-launch loop including the stranded-work sweep and the
  review standard.
- **Criteria ship with their evidence base and confidence stated.** Sixteen dispatches, one
  repo, three days, shape confounded with date. Three criteria survive scrutiny; two
  intuitive ones are contradicted by the data. Stating that is more useful than false
  certainty and matches the discipline the mechanics reference needs.

## Round 1 — structural findings that constrained the options

- **A `docs/guides/` file in niwa is unreachable from a workspace that uses niwa.** niwa is a
  binary; workspaces clone whatever their config names. Only `go:embed`-ed content reaches
  every workspace. The split is therefore by audience (re-verifiable evidence vs
  operational recipe), not by kind.
- **The `@import` layering the author named does not transfer.** `_common.md` arrives via a
  Read tool call, which returns `@` lines as literal text. Verified experimentally.
  Sentinel-section merge chosen instead, modelled on the existing worktree-context layer.
- **The preservation guarantee is not an obstacle**: it defends the snapshot swap, which
  runs before root materialization in the same command.
