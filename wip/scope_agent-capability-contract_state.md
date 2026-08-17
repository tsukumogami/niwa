---
topic: agent-capability-contract
chain_started: 2026-08-17T21:25:44Z
last_updated: 2026-08-17T22:20:00Z
phase_pointer: phase-2-chain-orchestration
visibility: Public
execution_mode: auto
max_rounds: 5
coordination_intent: absent
consumed_handoff: wip/scope_agent-capability-contract_handoff.md
planned_chain:
  - brief
  - prd
  - design
  - plan
chain_ran:
  - name: brief
    started_at: 2026-08-17T21:27:00Z
  - name: prd
    started_at: 2026-08-17T21:36:00Z
  - name: design
    started_at: 2026-08-17T21:48:00Z
parent_orchestration:
  parent: scope
  topic: agent-capability-contract
  child: plan
  invoked_at: 2026-08-17T22:20:00Z
chain_skipped: []
worktree_rebases: []
worktree_divergences: []
consolidation_judgments: []
---

# /scope state: agent-capability-contract

Phase 0 complete.

- Slug `agent-capability-contract` validated against `^[a-z0-9-]+$` as
  provided, byte for byte, with no normalization.
- Visibility read from `CLAUDE.md`'s `## Repo Visibility:` header in the niwa
  repository root: `Public`. Not defaulted.
- No `--upstream` supplied, so `consumed_upstream:` is absent. This is
  deliberate rather than an omission: the initiative's sequencing document
  lives in a private repository, and a public artifact must not name it.
- No coordination intent. The effort is confined to one repository, so the
  single-repo path applies and no coordination pull request is created.
- No stale `parent_orchestration:` block found at session start; nothing to
  self-heal.
- `/explore` handoff present at `wip/scope_agent-capability-contract_handoff.md`
  and consumed via the Slot 7 feeder-doc clause, so Phase 1 discovery enters
  with the exploration pre-loaded.

## Phase 1 — Discovery and chain proposal

No topic-related child docs exist. `docs/briefs/`, `docs/prds/`,
`docs/designs/current/` carry nothing matching this topic, and `docs/plans/`
does not exist in the repository at all. So no child is held back by re-entry
protection: `chain_skipped` stays empty and all four children run fresh.

### R6 shape-predicate walk

Evaluated against the projected PRD shape from the consumed `/explore` handoff.
Re-evaluated against the real PRD by the post-`/prd` gate.

- **P1 — architectural-alternatives count: FIRES.** Four alternatives are
  explicitly left open for the DESIGN to settle: a plan-producing leaf package
  versus an interface implemented inside the workspace package; MCP delivery by
  parsing the file niwa already distributes versus a new structured
  agent-neutral declaration generating both formats; fixing the plugin-skill
  dependency on a Claude-owned directory versus declaring it a limitation; and
  how far the first PR's contract reaches across the materializer set.
- **P2 — new-component references: FIRES.** The recommended shape introduces
  `internal/agentplan`, a new leaf package. Verified absent: `internal/` holds
  twenty packages and none is `agentplan`. A generic plan executor inside
  `internal/workspace` is also net-new.
- **P3 — Complex classification: FIRES.** The work carries a twenty-four-row
  capability matrix across two agents, two sequenced pull requests with the
  first required to be provably behavior-preserving, and four structural
  properties that must each become a test that fails on regression. The
  projected PRD names architectural complexity directly.

All three predicates fire, so R7 sizes `/design`'s decision roster at its
maximum: the full decision-researcher roster against the four open alternatives.

### Chain proposal

Proposed chain: `brief` -> `prd` -> `design` -> `plan`, the whole tactical
chain, none held back.

Resolution: **Proceed**. Recorded under `--auto` per the decision protocol;
the chain is the one the dispatch brief mandates and no adjustment signal
surfaced during discovery.

## Phase 2 — brief

Invoked `/brief agent-capability-contract --auto`. Returned
`docs/briefs/BRIEF-agent-capability-contract.md` at status Draft.

Worktree-staleness check: no rebase needed, branch is current with its base.
Impact classification: None.

Validator pass-through: `shirabe validate --format json --visibility=Public`
parsed a `shirabe-validate/v1` envelope, outcome `clean`, exit 0, zero errors
and zero notices. Re-run independently by the parent with the same result.
Boundary and hygiene greps clean: no private-repo references, no `wip/` paths
in the artifact.

Consolidation judgment: not applicable. The judgment fires only when both
endpoints of a chain edge appear in `chain_ran`, and the BRIEF is the first
artifact this run produced -- there is no upstream above it in this run to
judge it against.

Carried forward to `/prd` as open questions the PRD must resolve: how far the
first PR's contract reaches, the MCP delivery shape, the unresolved capability
matrix rows, and the mechanism for feeding new measurements into the standing
spike.

## Phase 2 — prd

Invoked `/prd agent-capability-contract --auto`. Returned
`docs/prds/PRD-agent-capability-contract.md` at Draft, upstream the BRIEF,
which it transitioned Draft -> Accepted.

Worktree-staleness check: no rebase needed. Impact classification: None.

Validator pass-through: envelope parsed, outcome `clean`, exit 0, zero errors
and zero notices. Re-run independently by the parent with the same result.
Boundary and hygiene greps clean.

Consolidation judgment, BRIEF -> PRD: **keep**. Both endpoints are in
`chain_ran`, so the judgment fires. The citation preflight passes -- the PRD is
the only citer -- but the BRIEF holds work the PRD does not: the framing that
the contract repair is warranted on its own, because the dead-accessor defect
is live on main independent of any Codex work. The PRD states requirements and
does not carry that argument, and folding would lose the reason the first PR
is justified without the second. Keep.

Post-`/prd` re-evaluation of the R6 predicates against the real PRD body: all
three still fire. P1 -- the PRD leaves architectural alternatives open by
construction, naming settlement paths rather than settling them. P2 -- the PRD
names a new leaf package and a new executor. P3 -- the PRD carries
architectural complexity explicitly. No resize; `/design` keeps the maximum
roster.

The four open questions the BRIEF deferred are closed. Two capability rows
remain measurement-dependent by design, each with a settlement path and
requirements covering both outcomes -- and both were subsequently settled
affirmatively by the extended measurement, which `/design` must fold in.

## Phase 2 — design

The first `design` invocation, dispatched through the `/design` skill, produced
no output in twenty-five minutes and answered neither of two progress pings. It
was abandoned rather than waited on further, and the document was authored
directly against the settled research corpus instead. The trade is defensible:
the skill's value is its decision orchestration, and all seven decisions were
already argued in the exploration -- the structural one adversarially, with its
strongest objection stated and answered, and every Codex behavior measured.
What remained was recording and justifying settlements against the repo's
`design/v1` schema.

Returned `docs/designs/current/DESIGN-agent-capability-contract.md`, 868 lines,
status Current, upstream the PRD.

Validator pass-through: envelope parsed, outcome `clean`, exit 0, zero errors
and zero notices. Re-run independently by the parent. Hygiene and boundary
greps clean.

Parent verification beyond the validator: the eight source sites the design
claims are red under the filename-literal half of the AST scan were re-grepped
independently and all eight hold at the stated line numbers with the stated
content -- `root_materializer.go:51`, `worktree_content.go:743`,
`workspace_context.go:196/229/411`, `content.go:156/186/208`. The claim that
the test is a deliverable rather than decoration is therefore verified, not
accepted on report.

Two design calls the PRD left open were settled by the design and reviewed here:

- **The alias spelling is `[context]`/`context_dir`,** not a resurrected
  `[content]`. Approved. Users who migrated to `[claude.content]` under the
  earlier deprecation would otherwise be whipsawed back, and a key deprecated
  in one release and canonical two releases later is a documentation hazard.
  A fresh name keeps all three generations distinguishable and the both-set
  error unambiguous.
- **`claude.enabled` is restructured with a parallel `codex.enabled`,** with the
  gate moved to plan production. Approved, and stronger than the rename it
  replaces: the executor never sees a gate at all, only entries that survived
  their own agent's filter, so no gate reaches across agents by construction
  rather than by discipline. The acceptance criterion asserts both directions.

Consolidation judgment, PRD -> DESIGN: **keep**. Citation preflight passes, but
the PRD holds work the DESIGN does not -- the twenty-four-row capability matrix
as a requirements table and the acceptance criteria the PLAN's issues will be
written against. The DESIGN records decisions and architecture and cites those
requirements by number rather than restating them. Folding would lose the layer
the implementation is verified against.
