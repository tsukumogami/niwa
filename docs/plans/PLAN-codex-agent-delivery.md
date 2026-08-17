---
schema: plan/v1
status: Draft
execution_mode: single-pr
upstream: docs/designs/current/DESIGN-agent-capability-contract.md
milestone: "Codex Agent Delivery"
issue_count: 10
---

# PLAN: codex agent delivery

## Status

Draft

This plan covers the second of two mandated pull requests (R9 of
docs/prds/PRD-agent-capability-contract.md) and does not start until the
first -- docs/plans/PLAN-agent-capability-contract.md -- has merged. The
gating reason is specific, not a rule of thumb: the Codex column of the
declaration table cannot flip to its target states until the contract, the
agent-blind executor, and the exhaustiveness and closure tests exist to
enforce the shape as it changes. Flipping a row before those tests exist
would reproduce the prior attempt's failure -- structure no test can fail
on.

## Scope Summary

Deliver Codex through the contract the first plan established, as its
second implementation and never a hardcoded second pass: directory trust as
a delivered capability, context composition within Codex's measured
discovery rules, skills without a Claude Code dependency, MCP servers and
environment generated from agent-neutral declarations validated before
writing, approval and sandbox posture opt-in and absent by default, the
`[context]`/`context_dir` alias and the per-agent enabled gates, secret
hygiene landing with the first secret write, and a generated gap list in a
user guide at docs/guides/. The Codex column ends at its PRD target: 11
implemented, 13 unavailable with reasons.

The acceptance bar is inherited, not invented: the 15 functional scenarios
from the retained branch (`test/functional/features/codex-agent.feature` on
`docs/dual-agent-workspace`) define what a working Codex session means.
They may be restructured in the open, never silently narrowed (R23).

## Decomposition Strategy

**Vertical by capability, trust first.** One issue per delivery surface,
sequenced so the closure test's `Requires` edges are satisfiable as each
Codex declaration flips: issue 1 delivers directory trust before anything
else, because MCP, environment, context, and posture all declare
`Requires: DirectoryTrust`, and the closure test rejects such an edge while
row 23 is still unavailable.

Two grouping rules. Each delivery issue flips its own declaration rows in
the same change that lands the delivery, so the binding test (R4) holds at
every issue boundary rather than only at PR head. And secret hygiene is not
its own issue: R18 requires the 0600 payload mode and the git-exclude
coverage in the same increment that first writes secret material, so both
live inside issue 5.

The ordering overview:

| Issue | Depends on |
|-------|------------|
| 1 trust + unavailable rows | None |
| 2 context producers | 1 |
| 3 skills + OpDeliverTree | 1 |
| 4 MCP declaration + generators | 1 |
| 5 environment + secret safety | 4 |
| 6 posture delivery | 4 |
| 7 alias + enabled gates | 2 |
| 8 gap list + guide | 2, 3, 5, 6 |
| 9 scenarios + verification | 2, 3, 4, 5, 6, 7, 8 |
| 10 spike contributions | None |

Issue 10 has no dependency inside this plan and can land at any point in
the PR; posting after issue 6 lets one pass carry every finding, but
nothing waits on it.

## Issue Outlines

### Issue 1: feat(workspace): Codex directory trust delivery and the unavailable-row declarations

**Complexity**: testable

**Type**: code

**Requirements**: R2, R17

**Goal**: Open the PR by delivering directory trust -- the capability every
trust-gated Codex row will name in `Requires` -- and flipping the 13
target-unavailable Codex rows to their final declarations with reason kinds
and reasons. Port the trust-write mechanics from the retained branch
(`internal/workspace/codex_trust.go` on `docs/dual-agent-workspace`) behind
a registered delivery: TOML-surgical, additive, lock-serialized, canonical
paths, retracting only keys niwa itself wrote.

**Acceptance Criteria**:
- Row 23 (per-directory trust bootstrap) is declared implemented for Codex
  and unavailable (no-such-concept) for Claude; the delivery is registered
  and the binding test passes
- The 13 target-unavailable Codex rows carry their PRD reason kinds and
  reasons; the exhaustiveness and well-formedness tests pass
- Repeated applies leave trust entries canonical and singular per
  repository
- An unreadable or malformed developer Codex configuration fails neither
  create nor apply
- The trust write retracts only keys niwa previously wrote and leaves the
  developer's own content untouched

**Dependencies**: None

**Files**: `internal/workspace/` (trust delivery), `internal/agentplan/`
(declaration table)

---

### Issue 2: feat(agentplan): Codex context-composition producers

**Complexity**: complex

**Type**: code

**Requirements**: R12, R13, R19, R20

**Goal**: Port the retained branch's composition mechanics as plan
producers: workspace and group orientation composed into each repository's
own context file (the only placement Codex reads), repository-level and
worktree-level context at the names Codex's discovery selects, the conflict
verdict feeding `Exempt` and `Warnings`, and `cleanRemovedFiles` gaining its
`Exempt` consultation. Discovery mechanics come from the standing spike
(docs/spikes/SPIKE-codex-discovery-mechanics.md, landing via
tsukumogami/niwa#254), never re-derived.

**Acceptance Criteria**:
- Rows 1, 3, and 4 flip to implemented for Codex with `Requires:
  DirectoryTrust` where the PRD says so; closure and binding tests pass
- Composition never writes an empty file and never overwrites a file the
  repository commits at one of niwa's names -- the conflict is reported and
  the committed file left alone, with the path carried in `Exempt`
- The declared byte budget covers the composed chain; nothing is silently
  truncated
- Worktree context lands on the measured `.git`-pointer-file result (R13);
  the plan-shape test's assertion that no Codex entry path ends in
  `CLAUDE.md`/`CLAUDE.local.md` is now exercised by real entries
- Both agents' plans are produced on every apply with no agent selection
  anywhere in create or apply (R19)
- A delivery failure for an implemented Codex capability fails the apply
  with a named error rather than degrading silently (R20)
- `cleanRemovedFiles` consults `Exempt` and never deletes a path the
  verdict refused to write

**Dependencies**: <<ISSUE:1>>

**Files**: `internal/agentplan/` (producers), `internal/workspace/`
(cleanup, verdict port)

---

### Issue 3: feat(workspace): skills delivery without a Claude Code dependency, and OpDeliverTree

**Complexity**: complex

**Type**: code

**Requirements**: R14

**Goal**: Deliver workspace-declared plugin skills to Codex sessions, whole
and namespaced, without depending on Claude Code being installed: content
for github-sourced marketplaces is fetched into a niwa-owned location via
the existing `github.FetchTarball` and `ExtractSubpath`, never resolved out
of Claude Code's user-global plugin directory. Move the retained branch's
symlink-or-bounded-copy delivery (~150 lines) into the executor as the
implementation of `OpDeliverTree`, retiring the named error the first
plan's executor left for that op.

**Acceptance Criteria**:
- Row 5 flips to implemented for Codex; binding test passes
- A workspace with a github-sourced marketplace delivers its skills to a
  Codex session on a machine with no Claude Code installation
- `OpDeliverTree` is implemented in the executor (symlink, bounded copy on
  failure); the first plan's placeholder error for the op is retired; op
  semantics are unit tested
- Delivered tree names carry `ExcludeAs` git-exclude coverage
- Skills arrive namespaced per the spike's measured discovery rules

**Dependencies**: <<ISSUE:1>>

**Files**: `internal/workspace/` (executor, skills delivery),
`internal/agentplan/` (producer)

---

### Issue 4: feat(config): agent-neutral MCP declaration and validated generators

**Complexity**: complex

**Type**: code

**Requirements**: R15

**Goal**: Add the structured, agent-neutral MCP server declaration to
workspace configuration and generate each agent's native format from it --
Claude's `.mcp.json` and Codex's `[mcp_servers.*]` project-layer entries.
The four measured codex-cli 0.147.0 constraints from the design's Decision
5 govern the generator.

**Acceptance Criteria**:
- Row 8 flips to implemented for Codex with `Requires: DirectoryTrust`
- Everything niwa writes into a Codex layer is validated before writing; a
  validation failure reports the error and writes no partial file -- a
  generated file never bricks a session
- Unmappable constructs are reported, never silently dropped or altered:
  a declared SSE server and any `${VAR}` interpolation in a Codex-bound
  value each produce a reported error and no partial file
- Every written value is fully resolved; nothing relies on load-time
  expansion
- Name collisions with the developer's own `[mcp_servers.*]` entries are
  detected by a read-only read of their configuration and reported rather
  than written; an unreadable or malformed developer config degrades
  gracefully
- The existing verbatim `.mcp.json` distribution keeps working unchanged;
  a workspace distributing `.mcp.json` with no structured declaration gets
  an apply-time report that its MCP servers reach Claude sessions only
- Claude generation from the neutral declaration produces a `.mcp.json`
  equivalent to what the verbatim path would have distributed for the same
  servers

**Dependencies**: <<ISSUE:1>>

**Files**: `internal/config/` (declaration surface), `internal/workspace/`
(generators, validation, collision read), `internal/agentplan/` (producer)

---

### Issue 5: feat(workspace): environment delivery to Codex with secret safety in the same increment

**Complexity**: complex

**Type**: code

**Requirements**: R16, R18

**Goal**: Deliver workspace-declared session environment to Codex through
the measured project-layer route (`shell_environment_policy.set`), values
fully resolved before writing, from an agent-neutral declaration source.
Because resolved secrets now sit in the payload configuration, this same
issue flips the payload to `secretFileMode` (0o600,
`internal/workspace/materialize.go`) and lands git-exclude coverage for
every niwa-written Codex-side name -- at the instance root as well as in
repositories. R18 forbids splitting these from the first secret write.

**Acceptance Criteria**:
- Rows 9 and 24 flip to implemented for Codex (environment with `Requires:
  DirectoryTrust`; git-exclude bookkeeping covering Codex-side names
  exactly as Claude-side ones)
- The payload configuration carrying resolved environment values is
  written at 0o600 in this same issue; a test asserts the mode on any
  entry whose capability delivers resolved environment values
- Git-exclude patterns cover the payload directory and
  `AGENTS.override.md` at repositories, and the instance root gains
  coverage for Codex-side names outside the existing conventions -- the
  same test that checks the mode checks the patterns
- No Claude-named configuration key gates Codex environment delivery
- Dotenv-file distribution remains agent-agnostic and untouched
- Validation-before-write from issue 4 covers the environment keys

**Dependencies**: <<ISSUE:4>>

**Files**: `internal/workspace/` (generator, exclude coverage),
`internal/agentplan/` (producer, mode/pattern test)

---

### Issue 6: feat(workspace): approval and sandbox posture delivery

**Complexity**: simple

**Type**: code

**Requirements**: R21

**Goal**: Deliver workspace-declared `approval_policy` and `sandbox_mode`
to Codex sessions through the trusted project layer, opt-in and absent by
default, with approvals and sandbox as separate declarations.

**Acceptance Criteria**:
- Row 12 flips to implemented for Codex with `Requires: DirectoryTrust`
- With no posture declared, the generated project-layer configuration
  contains neither key and apply reports no posture write -- asserted
  directly, not inferred
- A declared posture appears in the generated configuration and is
  reported at apply time
- No declaration that relaxes approvals changes the sandbox setting unless
  the workspace declared that too
- Validation-before-write covers the posture keys

**Dependencies**: <<ISSUE:4>>

**Files**: `internal/workspace/`, `internal/config/`,
`internal/agentplan/`

---

### Issue 7: feat(config): context alias and per-agent enabled gates

**Complexity**: testable

**Type**: code

**Requirements**: R7, R8

**Goal**: Give `[claude.content]` and `content_dir` their agent-neutral
aliases `[context]` and `context_dir`, following the recorded rename
precedent (docs/designs/current/DESIGN-claude-key-consolidation.md, the
mechanism at `internal/config/config.go`): both keys accepted, deprecation
warning on the old name, hard error when both are set, removal at the v1.0
line. Restructure the `claude.enabled` gate so it filters Claude plan
production only, and introduce `codex.enabled` with identical semantics
over the Codex plan -- no gate reaches across agents, and the executor
never sees a gate at all.

**Acceptance Criteria**:
- `[context]`/`context_dir` parse; the old keys parse with a deprecation
  warning; setting both fails with an error
- `claude.enabled = false` on a repository leaves full Codex delivery
  intact, and `codex.enabled = false` leaves full Claude delivery intact --
  both directions asserted
- No other Claude-named key is renamed (`plugins`, `marketplaces`,
  `hooks`, `settings`, `env`, `work_summary_hooks`, `pr_body_hook` stay as
  they are)
- Generated example configuration and docs reflect the new names

**Dependencies**: <<ISSUE:2>>

**Files**: `internal/config/config.go`, `internal/workspace/` (gate
placement), example configuration

---

### Issue 8: docs(guides): generated Codex gap list with drift test

**Complexity**: simple

**Type**: code

**Requirements**: R22

**Goal**: Generate the user guide's account of what a Codex session does
not get from the declaration table -- a filter over `StateUnavailable`
grouped by `ReasonKind`, rendering each `Reason` -- and commit it into a
guide at `docs/guides/`. A test regenerates the list and fails when the
committed section drifts.

**Acceptance Criteria**:
- The gap list is produced by the generator, never hand-written; every
  unavailable declaration's reason appears in it
- `ReasonNoSuchConcept` rows render as short "does not apply" notes; the
  other two kinds form the gap list proper
- A test fails when the committed guide section differs from generated
  output; editing the section by hand without a matching declaration
  change fails CI
- The guide's safety section is distinct from the gap list and records:
  what niwa refuses to write into the developer's own Codex configuration;
  that codex-cli 0.147.0's `ignore_default_excludes` defaults to `true`,
  so the `*KEY*`/`*TOKEN*` environment excludes are inactive unless opted
  into; and that a developer's own `include_only` allowlist silently drops
  variables niwa delivers

**Dependencies**: <<ISSUE:2>>, <<ISSUE:3>>, <<ISSUE:5>>, <<ISSUE:6>>

**Files**: `docs/guides/` (new guide), `internal/agentplan/` (generator
plus drift test)

---

### Issue 9: test(functional): the 15 inherited scenarios and the PR verification gate

**Complexity**: complex

**Type**: code

**Requirements**: R19, R20, R23

**Goal**: Bring over the 15 functional scenarios from the retained branch
(`test/functional/features/codex-agent.feature` on
`docs/dual-agent-workspace`) as this PR's acceptance bar. Scenario 2 is
restructured: dispatch's refusal becomes a declared-unavailable capability
and the scenario asserts the declaration, so test and gap list can't drift
apart. Scenario 10 (worktree context) stands as written. Any other
restructuring is named in the PR description; the bar is never silently
lowered.

**Acceptance Criteria**:
- All 15 scenarios pass in the PR's tree
- Scenario 2 asserts dispatch's declared unavailability; scenario 10 is
  unchanged; every restructuring is named in the PR description
- Re-applying three times adds nothing, changes nothing, and leaves trust
  entries canonical and singular per repository
- With a Codex delivery target made unwritable, apply fails with a named
  error identifying the capability (R20)
- A codex-default workspace still materializes the whole Claude tree and
  the reverse; `niwa create` and `niwa apply` take no agent selection
  (R19)
- The Claude-side suite, the characterization test, and the structural
  tests all still pass at the PR's head
- The Codex column's totals match the PRD target: 11 implemented, 13
  unavailable

**Dependencies**: <<ISSUE:2>>, <<ISSUE:3>>, <<ISSUE:4>>, <<ISSUE:5>>,
<<ISSUE:6>>, <<ISSUE:7>>, <<ISSUE:8>>

**Files**: `test/functional/features/` (scenarios plus step definitions)

---

### Issue 10: docs(spikes): feed measurements and corrections to the standing spike

**Complexity**: simple

**Type**: docs

**Requirements**: R24

**Goal**: Contribute this work's measured findings to the standing spike --
the MCP schema and environment-policy semantics against codex-cli 0.147.0,
the trust-gating results, the worktree-marker result (R13), the
approval/sandbox result (R21), and the two corrections the PRD names:
`project_root_markers` is accepted at the project layer (acceptance
measured, effect untested), and the measured project-layer denylist holds
eight keys across roughly fifty probed against the spike's figure of
eleven. While the spike lives on tsukumogami/niwa#254, contributions are
posted as structured comments on that pull request; once it merges, pending
contributions land as an in-repo update to
docs/spikes/SPIKE-codex-discovery-mechanics.md. This issue depends on
nothing in this plan and can land at any point in the PR.

**Acceptance Criteria**:
- Every finding listed above is posted to tsukumogami/niwa#254 or
  committed into the spike file, whichever its merge state calls for
- The two corrections are framed as corrections, with the measurement
  method stated
- No new spike document exists anywhere in this work's deliverables

**Dependencies**: None

**Files**: `docs/spikes/SPIKE-codex-discovery-mechanics.md` (post-merge
case only)

---

## Dependency Graph

_Empty in single-pr mode per the PLAN format spec. The ordering overview is the table at the end of the Decomposition Strategy section, and each outline's Dependencies line declares its own edges._

## Implementation Sequence

**Trust first, then three parallel tracks.** Issue 1 opens the PR -- every
trust-requiring declaration flip downstream needs row 23 implemented or the
closure test rejects it. After issue 1, three tracks run in parallel:
context (issue 2, then 7), skills (issue 3), and the generator track (issue
4, then 5 and 6 in parallel). Issue 8 closes once all delivery rows have
flipped; issue 9 is the acceptance gate. Issue 10 floats free -- any point
in the PR works, and posting after issue 6 lets one pass carry every
finding.

**Second-order risk**: issue 5's same-increment rule. If environment
delivery is split across commits for review size, the 0600 mode and the
git-exclude patterns must travel with whichever commit first writes
resolved values -- R18 is about the increment, not the issue label.

**Cross-plan gate, restated**: nothing here starts until
docs/plans/PLAN-agent-capability-contract.md has merged. The declaration
flips in issues 1-6 are only safe because the exhaustiveness, closure, and
binding tests from that plan enforce the shape as it changes; without them,
a flipped row is exactly the untested structure the prior attempt shipped.

## References

- docs/designs/current/DESIGN-agent-capability-contract.md -- the upstream
  design; its Implementation Approach section is this plan's skeleton.
- docs/prds/PRD-agent-capability-contract.md -- requirements R1-R24,
  N1-N3, the 24-row capability matrix with target declarations, and the
  acceptance criteria each issue's ACs trace to.
- docs/plans/PLAN-agent-capability-contract.md -- the first plan; its
  merged PR is this plan's precondition.
- docs/spikes/SPIKE-codex-discovery-mechanics.md (landing via
  tsukumogami/niwa#254) -- measured Codex discovery behavior, consumed not
  re-derived; destination for issue 10's contributions.
- docs/designs/current/DESIGN-claude-key-consolidation.md -- the rename
  precedent issue 7 follows.
- tsukumogami/niwa#248 (branch retained at `docs/dual-agent-workspace`) --
  the prior attempt: sound composition mechanics issues 1-3 port, and the
  15 scenarios issue 9 inherits.
