# /prd Decisions: agent-capability-contract

Execution mode: --auto, under /scope parent orchestration (fresh-chain).
Fallback shapes in force: serial-self-jury (Phase 4), parent-delegated-approval
(finalize at Draft; parent owns the acceptance prompt). Parent handles commits;
this child edits files only.

## Phase 0

- Branch `docs/agent-contract` matches the topic; no branch created.
- Upstream brief `docs/briefs/BRIEF-agent-capability-contract.md` transitioned
  Draft -> Accepted via `shirabe transition` (chain-handoff symmetry). Not
  committed; parent owns commits.
- Input was topic-mode (`agent-capability-contract --auto`), but the parent's
  dispatch names the brief as upstream, so the brief is treated as Input Mode 2
  for upstream/frontmatter and transition purposes. Judgment call, flagged in
  the report to the parent.

## Phase 1

- Scoping is pre-done: the /scope handoff (consumed /explore output, two
  discover-converge rounds plus a round-3 measurement) covers all six coverage
  dimensions. Scope file written from that corpus rather than re-derived.

## Phase 2

- No new research agents dispatched. The exploration corpus (r1 x8, r2 x5, r3
  measurement) already investigates every lead the scope names; a fresh fan-out
  would duplicate it. Decision recorded per the --auto protocol; synthesis file
  written so resume logic lands at Phase 3.

## Phase 3 requirement-level resolutions (the brief's four open questions)

1. **PR 1 contract reach.** Split the contract into declaration and delivery.
   Declarations: complete for every capability from PR 1 (Claude column
   complete; Codex column, if enumerated in PR 1, must state main's truth --
   nothing delivered). Binding (delivered iff declared) holds path-wide from
   PR 1. Delivery restructure in PR 1 covers exactly the surfaces that are
   agent-shaped today: the eight context-writer sites and the settings-document
   builder. Agent-agnostic materializers (hooks, dotenv, file distribution) are
   declared and bound but not restructured -- they contain no agent-specific
   logic to put behind a contract, and the path-wide structural tests prevent
   any future hardcoded second pass. Rationale: this is what makes the contract
   govern all 24 capabilities (not 2) without forcing a speculative rewrite of
   config-driven code the mandate says not to touch.
2. **MCP shape.** Structured agent-neutral declaration generating both agents'
   formats. Parsing the distributed `.mcp.json` rejected: measured evidence
   (codex-cli 0.147.0) shows translation is lossy both ways (SSE silently
   becomes a different protocol; `${VAR}` interpolation absent; Codex-only
   fields unexpressible), and a Claude-format file as the source of Codex
   delivery violates the no-cross-agent-gating property. `.mcp.json`
   distribution keeps working as a Claude-only compatibility path whose reach
   is reported.
3. **Matrix rows.** Settled by r3: env delivery is trust-gated (Requires edge
   confirmed); MCP schema pinned. Still open, stated with settlement paths, not
   guessed: worktree `.git`-file marker (one live check; both outcomes
   specified in requirements), approval/sandbox settability (denylist read or
   live probe; ships as not-built if unmeasured at PR 2). Hooks reason-kind
   flip noted as non-blocking (state is Unavailable either way). Skills
   github-marketplace fetch: build it (settled upstream decision).
4. **Spike mechanism.** Contribute to the standing spike, never fork: findings
   posted as a comment on tsukumogami/niwa#254 while it is open; appended to
   docs/spikes/SPIKE-codex-discovery-mechanics.md in-repo once it merges.

## Phase 4

- Serial self-jury: three reviewer passes run in-process, verdicts written to
  wip/research/prd_agent-capability-contract_phase4_*.md.
- PRD left at Draft; no acceptance prompt (parent-delegated-approval).

## Phase 4 resolution

Serial self-jury verdicts: completeness FAIL (minor), clarity FAIL (minor),
testability PASS. All findings fixed in place: R8 now names the known rename
instances, R16 requires an agent-neutral env source, two ACs added (loud
Codex failure; alias semantics), "the mandate" phrasing replaced with
self-contained wording, and three ACs reworded for precision. Re-validated
clean (exit 0) and re-grepped clean after fixes. PRD left at Draft per
parent-delegated-approval.
