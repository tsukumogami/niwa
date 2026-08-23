# Crystallize: codex-instance-root-skills

## Candidacy

| Arm | On the board? | Why |
|---|---|---|
| `/execute` | **No** | No qualifying PLAN. `docs/plans/` does not exist in this repo, and no `.md` carries `schema: plan/v1` for this topic. |
| Competitive analysis | **No** | `## Visibility` is Public. Precondition fails. |

Both failed arms take no further part and are not offered as alternatives.

## Stage 1 — what the exploration is

| Category | Signals | Anti-signals | Score |
|---|---|---|---|
| Rejection Record | 0 | 2 (no rejection conclusion; nothing to reject) | -2 |
| Spike Report | 2 (risks identified and tested; concrete findings) | 2 (the question is "what do we build and how", not "can we"; the exploration was broad rather than one technical risk) | 0 |
| Decision Record | 1 (alternatives compared with trade-offs) | 2 (several interrelated decisions, all with work attached) | -1 |
| **A Chain** | **5** | **0** | **5** |

A Chain's signals, each grounded: the exploration converged on something someone
will build (a root-scope skills delivery plus a bound row 19); architecture
questions remain open (which binding shape row 18 takes, whether the embedded
tree gains a plugin manifest, how the dispatch warning is re-gated); decisions
made during exploration need a durable home (six recorded in the decisions file,
one deliberately escalated); a scope boundary emerged rather than an answer (the
trust-entry line that separates skills from the config half); and the core
question is what to build and how.

The Spike Report arm scores zero rather than negative, and that is worth saying
plainly instead of rounding away: this exploration produced genuinely new
measured findings about `codex-cli 0.147.0` — the plugin-manifest namespacing
rule and a root-layer positive/negative control. Those belong in the **existing**
`docs/spikes/SPIKE-codex-discovery-mechanics.md` as amendments to findings 1 and
5, not in a second spike document competing with it. That is an artifact update
the chain carries, not a separate outcome.

## Stage 2 — where the chain starts

| Entry point | Signals | Anti-signals | Score |
|---|---|---|---|
| File an issue | 0 | 4 (architectural decisions were made; scope was debated across two rounds; others need documentation to build from; the mandated structural properties need a written contract) | -4 |
| `/charter` | 0 | 3 (the project exists; this is one bounded feature; no cross-feature ordering question) | -3 |
| **`/scope`** | **6** | **0** | **6** |

`/scope` takes it. One coherent feature emerged; what to build is clear and how
to build it is not; technical decisions remain between named approaches;
integration questions remain across four packages; the exploration surfaced
multiple viable implementation paths; and architectural decisions made along the
way need to be on record. Its terminal artifact is a PLAN, which is what the
next hop needs.

The margin is 10 points over the nearest alternative, so no near-tie applies and
only `/scope` is presented.

## What the chain carries forward

**The decided.** Six decisions in
`wip/explore_codex-instance-root-skills_decisions.md`: the delivery is a sibling
producer method rather than a payload-layout scope (D1); no scope field is added
to `SkillsInputs` (D2); root links point at the same targets the repository
links already do, for a stated reason rather than by inheritance (D3); the root
delivery does not reach for the repository exclude path (D4); both rows join
`boundCapabilities`, which pulls the Claude side in with them (D5); and the
plugin-manifest question is escalated to the design hop with its evidence
gathered (D6).

**The measured.** Against the real binary, isolated `CODEX_HOME`, no model
turns: a real plugin tree symlinked at the session's own working directory
yields 20 correctly namespaced skills untrusted, while the same shape one
directory up yields nothing; and `.claude-plugin/plugin.json` is what produces
the namespace, which niwa's own embedded tree lacks.

**The open, for the design hop.**

1. Row 18's binding shape. Its route is `RoutePlan`, so a binding must name a
   `Delivery` registered as a `Materializer`. The alternative is to bind it the
   way every other `RoutePlan` capability is bound today — by tagging plan
   entries with the capability and testing the tag — and leave it out of
   `boundCapabilities`. The brief's mandate names the binding rule, which is the
   `boundCapabilities` one; the codebase's own precedent is the other. This
   needs deciding on the record.
2. Whether to add `.claude-plugin/plugin.json` to the embedded tree, given the
   unmeasured risk to the documented `/niwa:migrate-config` command on the
   Claude side.
3. How the dispatch warning is re-gated once row 18 stops gating it, given that
   no declaration row means "in a repository, not at the root" and inventing one
   is forbidden.
4. Where the embedded tree is materialized inside the instance, and whether that
   name can collide with a configured marketplace called `niwa`.

**The surface that must move with the change.** Eight sites, enumerated in the
findings: three tests in `capability_test.go`, the binding tests, the gap-list
drift test (regenerated, not edited), the dispatch warning and its two pinning
tests, the guide's authored prose, the feature file's own preamble prose, and a
PRD amendment in the repo's existing amendment style.

## Recommendation

Route to `/scope codex-instance-root-skills`, tactical, public.
