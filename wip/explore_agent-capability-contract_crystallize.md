# Crystallize: agent-capability-contract

## Step 1: Candidacy

- **`/execute`** -- NOT a candidate. No qualifying PLAN exists; `docs/plans/`
  is absent from the repo entirely.
- **Competitive analysis** -- NOT a candidate. `## Visibility` is `Public`.

Both arms take no further part in this run.

## Step 2: Stage 1 -- What the exploration is

| Category | Signals | Anti-signals | Score |
|---|---|---|---|
| Rejection Record | 0 | 2 (no rejection conclusion; nothing argues the work should not happen) | -2 |
| Spike Report | 2 (technical uncertainty blocked decisions; specific risks identified and tested) | 2 (the core question is "what do we build and how", not "can we"; exploration was broad, not focused on one technical risk) | 0 |
| Decision Record | 1 (alternatives compared with trade-offs) | 2 (multiple interrelated decisions, all with work attached) | -1 |
| Competitive Analysis | precondition failed | -- | absent |
| **A Chain** | **5** (converged on something someone will build; requirements, architecture and sequencing all open; decisions made during exploration need a durable home plus downstream work; a scope boundary emerged rather than just an answer; the core question is "what do we build, and how") | **0** | **5** |

**Stage 1 result: A Chain**, by a margin well outside the one-point near-tie band.

The Spike Report arm deserves a note rather than a dismissal, because part of this
exploration genuinely is a spike: the round-3 measurement against `codex-cli
0.147.0` produces feasibility findings. But that is a *component* of the work, not
what the exploration is. It is also already owned elsewhere --
`docs/spikes/SPIKE-codex-discovery-mechanics.md` exists on the unmerged
`tsukumogami/niwa#254`, and the right home for new measurements is that document,
not a competing one. See Obligations below.

## Step 3: Stage 2 -- Where the chain starts

| Entry point | Signals | Anti-signals | Score |
|---|---|---|---|
| File an issue | 0 | 4 (others need documentation to build from; architectural and structural decisions were made during exploration; scope was debated across two rounds; more than one contributor will work on this) | -4 |
| `/charter` | 1 (dependencies between the two PRs affect delivery order) | 3 (the project already exists; this is one bounded feature, however large; users and needs are identified and uncontested) | -2 |
| **`/scope`** | **8** (a single coherent feature emerged; requirements are contested -- the brief's own two-state model needed correcting; what to build is clear but how is not; technical decisions between named approaches were required; architecture and integration questions remain; multiple viable implementation paths surfaced and were narrowed; architectural decisions made during exploration must go on record; the core question is exactly "what should we build, and how") | **0** | **8** |
| `/execute` | precondition failed | -- | absent |

**Stage 2 result: `/scope`.** Decisive, and it agrees with the dispatch brief's
own instruction to run `shirabe:scope` through to a PLAN.

## What the chain receives

The tactical chain enters with an unusually complete input set. `/scope` should
treat these as settled and spend its budget on what remains open, rather than
re-deriving them:

**Settled by round 2 and not worth relitigating:**
- The capability state model: two states, a required reason kind on the
  unavailable side, and `Requires []Capability` edges, with a closure test.
- The structural shape: a leaf `internal/agentplan` package producing plans, one
  agent-blind executor in `internal/workspace` over a closed four-op set, and the
  "reads inputs, declares outputs" boundary enforced by an AST assertion.
- The PR split: PR 1 ships zero config renames and is proven by a `ManagedFiles`
  characterization test committed on `main` before the refactor. PR 2 carries the
  `Content`/`content_dir` alias, the `claude.enabled` gate restructure, and Codex.
- The 24-row support matrix and the generated Codex gap list, in draft.
- The 15-scenario acceptance bar, enumerated.

**Genuinely open, and what `/scope` must decide:**
1. Which capabilities PR 1 brings under the contract. Round 2 costed the eight
   context writers plus `buildSettingsDoc`; the hooks, env and files
   materializers were explicitly left out as config-driven rather than
   agent-driven. That boundary needs a requirements-level answer, not just an
   implementation estimate, because it is the difference between a contract that
   governs two capabilities and one that governs enough to matter.
2. Whether the skills Claude-Code dependency is fixed in PR 2 or declared. Round 1
   found niwa already owns `github.FetchTarball` + `ExtractSubpath`, so the fix is
   cheap -- but it is net-new capability inside a PR that is already large.
3. The MCP surface: parse the distributed `.mcp.json`, or add an agent-neutral
   structured declaration that generates both formats. Round 2 recommends the
   latter and flags that unmappable constructs must be reported, never dropped.
4. How the payload-config permission and git-exclude defects are sequenced against
   environment delivery. They must land together; the plan has to say so.

## Obligations this exploration created

- **Feed the round-3 measurements back into the existing spike.** The brief
  directs that the spike be updated if its measurements are extended. The three
  new rows (approval/sandbox settability, the linked-worktree `.git`-file marker,
  `shell_environment_policy.set` trust-gating) belong in
  `docs/spikes/SPIKE-codex-discovery-mechanics.md`, which lives on an unmerged PR
  owned by someone else. `/scope` must decide the mechanism -- most likely a
  contribution to that branch or a note on #254 -- rather than forking a second
  spike document that would compete with it.
- **One matrix row may flip.** If a linked worktree's `.git` file does not satisfy
  the project-root marker, `CapabilityWorktreeContext` moves from Implemented to
  Unavailable for Codex and acceptance scenario 10 becomes unsatisfiable as
  written. The PRD cannot be finalized until that measurement lands.

## Decision

Route to **`/scope`**, carrying the findings, decisions, and the round-3 spike
output. Confirmed in `--auto` mode per the decision protocol; the evidence is
one-sided and no reasonable alternative reading survives the scoring.
