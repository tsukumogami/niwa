---
topic: agent-capability-contract
chain_started: 2026-08-17T21:25:44Z
last_updated: 2026-08-17T21:48:00Z
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
parent_orchestration:
  parent: scope
  topic: agent-capability-contract
  child: design
  invoked_at: 2026-08-17T21:48:00Z
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
