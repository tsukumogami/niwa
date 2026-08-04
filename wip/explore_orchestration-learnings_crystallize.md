# Crystallize: orchestration-learnings

## Route

**Design Doc, then Plan** — handed to `shirabe:scope`, which the dispatch brief mandates
as the next step and which owns the BRIEF → PRD → DESIGN → PLAN chain.

## Why not the alternatives

**Not "No artifact."** Exploration produced decisions a future contributor needs and
`wip/` is deleted before merge: the audience choice and its consequence for chain-shape
vocabulary, the sentinel-merge mechanism over write-if-absent, the finding that
`docs/guides/` is unreachable from a workspace and what that forces, and the corrected
resume recipe. All of that dies with `wip/` unless it lands somewhere permanent.

**Not PRD-first.** What to build and why are settled by the author's four answers: a
launch-decision aid for any niwa user, centred on chain shape and launch mode, succeeding
when a fleet runs unattended. The open questions are mechanical — how content reaches a
workspace, how a shipped file stays overrideable, how root skills carry more than one
file. Those are design questions.

**Not straight to Plan.** Three decisions have live alternatives with real trade-offs and
would otherwise be made implicitly during implementation:

1. **Seeding mechanism for the standing agreement** — sentinel merge vs write-if-absent vs
   overwrite-plus-`.local`. Recommended: sentinel merge, modelled on the existing
   `installWorktreeContextLayer` / `stripWorktreeContextSection` pair. Write-if-absent
   freezes every workspace at the version it first saw and niwa could never ship a
   correction.
2. **Root-skill file layout** — the materializer currently picks up only
   `rootskills/<name>/SKILL.md`, with no `references/` support. The content does not fit
   comfortably in single files, and extending the walk is a small change with a real
   alternative (write tighter, or point at guides that most workspaces cannot reach).
3. **Skill boundaries** — one shipped skill or two. The before-launch decision and the
   after-launch loop have different trigger moments; a skill named `dispatch` will not load
   when someone asks what is in flight. But two shipped root skills doubles niwa's shipped
   agent surface, and the coordinator that produced this material specifically predicted
   over-production here.

Plus one non-blocking scope question the design should settle: `MaterializeWorkspaceRoot`
runs only at root-scope apply and on named/clone init, so `niwa create` and `niwa dispatch`
never refresh shipped content. Fix, document, or file.

## What the design doc must carry

- The audience decision and its consequence: chain shape expressed as tool-neutral framing
  levels, with a mapping note rather than shirabe skill names as doctrine.
- The load-point table — which path each piece of content lands in, who reads it, and when
  — because the acceptance criterion is "you can say concretely when it gets read."
- The corrected resume recipe, and the fact that it supersedes a recipe that was published
  and wrong.
- The evidence base and confidence for the framing-level criteria, including the two
  intuitive criteria the data contradicts.

## Handoff

`wip/explore_orchestration-learnings_findings.md` and
`wip/explore_orchestration-learnings_decisions.md` are the inputs. The five research files
under `wip/research/` carry the citations.
