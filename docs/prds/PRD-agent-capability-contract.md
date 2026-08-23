---
schema: prd/v1
status: Done
problem: |
  niwa's workspace-preparation path is structurally Claude-shaped: the type
  meant to unify agents governs two of roughly twenty capabilities the path
  delivers, and everything else runs Claude-shaped unconditionally. The prior
  Codex attempt (tsukumogami/niwa#248) shipped its unifying abstraction as
  dead code because no test could fail on the structure. There is no contract
  a second agent can implement, and no single honest account of what a
  non-Claude session gets.
goals: |
  A prepared instance serves both Claude Code and Codex with no agent choice
  forced at creation time. Every capability is either implemented for an
  agent or explicitly declared unavailable with a reason; the user guide's
  gap list is generated from those declarations; and every structural claim
  is held by a test that fails on regression. The contract lands first,
  against existing Claude behavior only and provably without behavior
  change, and Codex arrives as its second implementation.
upstream: docs/briefs/BRIEF-agent-capability-contract.md
motivating_context: |
  This is the second attempt at dual-agent workspace preparation. The first
  shipped and was closed as a prototype: its agent value was threaded
  through the applier and read by nothing while every call site hardcoded an
  agent constant. That defect predates the attempt and is live on main
  today -- agent.LocalContextFileName() has zero callers anywhere in the
  module -- which makes the contract repair work in its own right, not
  scaffolding for Codex.
---

# PRD: agent capability contract

## Status

Done

This PRD owns the requirements for the capability contract and for Codex as
its second implementation, delivered as two sequenced pull requests. It
closes the four questions its upstream brief deferred: how far the first
PR's contract reaches, the MCP delivery shape, the disposition of the
capability matrix's unresolved rows, and how new measurements reach the
standing spike. The downstream design owns the package layout, the plan or
interface shape of the contract, and the mechanics of each enforcement
test.

## Problem Statement

When niwa prepares a workspace instance -- context files, settings, hooks,
permissions, plugins, skills, environment, trust -- nearly every step is
Claude-shaped by construction. The `agent.Agent` type that was meant to
make the path agent-aware governs two of the roughly twenty capabilities
the path delivers: which filename root and group context land in, and
whether repository-level context is written at all. Everything else takes
no agent parameter and runs Claude-shaped unconditionally. The preparation
path carries 567 Claude references across nineteen non-test files.

The first attempt to add OpenAI Codex on top of this (tsukumogami/niwa#248)
shipped and was closed as a prototype. Its failure was structural: the
abstraction meant to unify the two agents was dead code. The agent value
was threaded through the applier and read by nothing, while every
materializer call site hardcoded an agent constant -- two hardcoded passes
where a shared contract should have been. Its design had diagnosed exactly
this risk and prescribed the cure, and the code shipped the disease anyway,
because the replacement structure was never something a test could fail on.

That defect isn't something the prior attempt introduced. On main today,
`agent.LocalContextFileName()` has zero callers anywhere in the module: two
functions (`InstallRepoContentTo` in `internal/workspace/content.go` and
`installWorktreeContextLayer` in `internal/workspace/worktree_content.go`)
accept an agent parameter, use it only as a run/skip gate, and then
hardcode the Claude filename inside the gated body. The accessor that was
supposed to make the path agent-aware is dead, and nothing fails because of
it.

So the problem is not that Codex support is missing. It is that there is no
contract for a second agent to be an implementation of, and no test that
fails when one is faked. Until that exists, any Codex delivery -- however
careful its file composition -- collapses back into a parallel hardcoded
pass, and a user has no honest account of what a non-Claude session
actually gets: the prior attempt's guide described Codex as a delta from a
Claude baseline and scattered its gaps across a design's negative-space
section and a scope note, where no reader would assemble them into an
answer.

## Goals

- A developer prepares a workspace instance once and it serves whichever
  agent they or a teammate opens it with. No agent choice is forced at
  creation time, and preparing for one agent never silently disables
  delivery for the other.
- Every capability the preparation path delivers is, per agent, either
  implemented or explicitly declared unavailable with a reason. A
  capability in neither state fails a test.
- Where Codex genuinely can't have something, the developer learns it from
  one plain list in the user guide, generated from the declarations in
  code. If code and doc ever disagree, the code is right and the doc is a
  bug.
- The structural claims stop being a matter of trust: whether logic is
  generic or agent-specific is visible in the package layout, and each
  structural property is held by a test that fails on regression.
- The first delivery lands the contract against existing Claude behavior
  only and carries a mechanical proof that it changed nothing a user can
  observe.

## User Stories

- As a developer on a mixed-agent team, I want `niwa create` with no agent
  flag to prepare an instance that serves both a Codex session and a Claude
  Code session -- context composed within each agent's discovery rules,
  skills namespaced and reachable, MCP servers and environment delivered,
  the instance trusted -- so that neither I nor my teammate hand-edits
  agent configuration and neither of us loses anything the other has.
- As a maintainer reviewing the first pull request, I want its
  no-behavior-change claim proven by a characterization test committed
  against current behavior before the refactor began, plus structural tests
  that fail if an agent constant appears at a materializer call site, so
  that a large diff is reviewable mechanically rather than on the title's
  word.
- As a developer whose workspace hooks don't fire in a Codex session, I
  want the user guide's generated gap list to tell me that hooks don't
  reach Codex sessions and why, so that I don't discover gaps by experiment
  and the answer can't have drifted from what the code does.
- As a contributor adding agent-specific behavior or a third agent, I want
  the contract to confront me with the whole capability set -- each
  capability implemented or declared unavailable with a reason, anything in
  neither state failing CI -- so that the prior attempt's failure mode, an
  interface nothing reads satisfied by hardcoded passes, is no longer
  something I can reproduce by accident.

## Requirements

### The contract

- **R1. Closed capability set.** The capabilities the workspace-preparation
  path delivers are enumerated as a closed, exported, testable set. The
  initial set is the 24 capabilities in the matrix below. Deliberate
  exclusions are recorded alongside the set so the closure doesn't look
  arbitrary: vault-backed secret resolution (an upstream source feeding
  environment delivery, not something a session receives) and the
  `claude.enabled` gate (a gate over deliveries, not a delivery).
- **R2. Two states, with reasons.** Every (capability, agent) pair over the
  contract's enumerated agents is in exactly one of two states: implemented,
  or unavailable. An unavailable declaration carries a machine-readable
  reason kind -- the agent cannot receive it, the concept does not exist for
  the agent, or niwa has not built it -- and a human-readable reason. There
  is no third state. A pair in neither state, a pair in both, an unavailable
  declaration missing its reason, or an implemented declaration carrying one
  each fail a test.
- **R3. Requirement edges.** An implemented declaration may name other
  capabilities it requires (for example, Codex MCP delivery requires
  directory trust). A test fails unless every named requirement is itself
  implemented for the same agent and the requirement graph is acyclic.
  Preconditions niwa cannot own are not expressible as requirements; the
  honest declaration for those is unavailable, with a reason.
- **R4. Declarations are load-bearing.** The apply path delivers a
  capability for an agent if and only if the declaration table says
  implemented. Both drift directions fail a test: an implemented
  declaration with no delivery behind it, and a delivery reachable for a
  pair the table does not declare implemented.
- **R5. No agent constants at call sites.** In the preparation path,
  agent-specific behavior is reached through the contract, never by naming
  an agent or an agent's context filename at a materializer call site. This
  is enforced by a structural test over the source. The test must be
  meaningful on the day it lands: its context-filename half fails on
  today's tree at eight known sites (in `internal/workspace/content.go`,
  `worktree_content.go`, `workspace_context.go`, and
  `root_materializer.go`) and passes after the first PR's conversion.
- **R6. Package-legible boundary.** Whether a piece of preparation logic is
  generic or agent-specific is readable from where it lives in the package
  layout, not only from function bodies, and the boundary is enforced
  structurally -- the layer that declares capabilities and describes
  deliveries performs no filesystem writes and launches no processes,
  which a test asserts mechanically. The concrete layout is the design's
  decision.
- **R7. No cross-agent gating.** No configuration key or internal gate
  named for one agent controls another agent's delivery. Disabling one
  agent's delivery for a repository leaves every other agent's delivery
  intact. This retires the prior attempt's defect where `claude = false`
  on a repository silently disabled all three Codex delivery steps.
- **R8. Rename discipline.** Where satisfying R7 requires renaming a
  shipped configuration key to an agent-neutral name, the rename follows
  the repository's recorded precedent
  (docs/designs/current/DESIGN-claude-key-consolidation.md): both keys
  accepted, a deprecation warning on the old one, a hard error when both
  are set, removal at the v1.0 line. A rename lands in the PR that first
  gives the key a second agent to mis-gate -- which is the second PR, never
  the first. The first PR ships zero configuration renames. The known
  instances at writing time: the Claude-namespaced content configuration
  (the `claude.content` table and its `content_dir`) gains an agent-neutral
  alias in the second PR, and the `claude.enabled` gate is not renamed but
  restructured so it governs only Claude-owned deliveries (R7) -- relabeling
  it would reproduce the same mis-gating under a new spelling.

### Sequencing and proof

- **R9. Two pull requests, in order.** The first PR lands the contract
  against existing Claude behavior only: no Codex delivery, no behavior
  change. The second delivers Codex through the contract the first
  established. The split is not optional; the prior attempt buried its
  structure inside eleven thousand lines where it could not be reviewed on
  its own merits.
- **R10. Mechanical no-behavior-change proof.** Before the refactor begins,
  a characterization test is committed against current behavior: it pins
  every file the preparation path writes, by path and content hash, using
  the apply path's own managed-file record rather than a hand-picked
  subset. Nondeterministic inputs (the workspace's absolute path, the
  executable path in generated hook commands) are normalized so the
  characterization is stable across machines. The first PR must leave this
  test passing unchanged.
- **R11. How far the first PR's contract reaches.** The contract's
  declaration layer covers the full capability set from the first PR:
  Claude's column is complete, and if the enumerated agent set already
  includes Codex, its column must state main's truth -- nothing is
  delivered for Codex on main. The binding rule (R4) and the structural
  tests (R5, R6) hold path-wide from the first PR. The delivery-side
  restructuring in the first PR covers exactly the surfaces that are
  agent-shaped today: the eight context-writer sites named in R5 and the
  settings-document builder (the code that composes the `.claude/`
  settings documents an instance receives). Materializers that are
  agent-agnostic today
  (dotenv files, arbitrary file distribution, hook installation) are
  declared and bound under the contract but not restructured in the first
  PR -- they contain no agent-specific logic to put behind a contract, and
  R4 and R5 prevent any future change from adding agent-specific behavior
  to them outside it. From the second PR on, the standing scope rule
  applies: wherever dual-agent capability lands, the refactor lands with
  it -- never a hardcoded second pass.

### Codex delivery (second PR)

- **R12. Context composition within Codex's measured discovery rules.**
  Workspace and group orientation content is composed into each
  repository's own context file (the only placement Codex reads), and
  repository-level and worktree-level context files are written at the
  names Codex's discovery selects. Composition never writes an empty file,
  never overwrites a file the repository commits at one of niwa's names
  (the conflict is reported and the committed file left alone), and the
  declared byte budget covers the composed chain so context isn't silently
  truncated. Discovery mechanics are consumed from the standing spike
  (docs/spikes/SPIKE-codex-discovery-mechanics.md, landing via
  tsukumogami/niwa#254), never re-derived.
- **R13. Worktree context is delivered; the marker question is measured.**
  A linked worktree's `.git` file -- a regular pointer file, not a
  directory -- satisfies Codex's project-root marker. This is measured
  against codex-cli 0.147.0 with a root-only context file read from two
  levels deep and a passing negative control, so worktree-level context
  is implemented for Codex (requiring directory trust for the budget
  override) and the worktree acceptance scenario stands as written. The
  measurement flows to the standing spike per R24.
- **R14. Skills without a Claude Code dependency.** Workspace-declared
  plugin skills reach a Codex session whole and namespaced. Delivery must
  not depend on Claude Code being installed on the machine: content for
  github-sourced marketplaces is fetched into a niwa-owned location rather
  than resolved out of Claude Code's user-global plugin directory. A
  machine that has never run Claude Code gets the same skills.
- **R15. MCP servers via an agent-neutral declaration.** The workspace
  configuration gains a structured, agent-neutral MCP server declaration
  from which niwa generates each agent's native format -- Claude's
  `.mcp.json` and Codex's `[mcp_servers.*]` project-layer entries. Four
  properties are required, each grounded in measured codex-cli 0.147.0
  behavior:
  - A construct that does not map to an agent's format is reported, never
    silently dropped or silently altered. The known unmappables in the
    Codex direction: SSE transport (Codex has none and silently serves a
    declared SSE server over a different wire protocol) and `${VAR}`
    interpolation (Codex performs none, anywhere).
  - Every value is fully resolved before writing; nothing niwa writes
    relies on expansion at load time.
  - Everything niwa writes into a Codex configuration layer -- not only
    MCP entries -- is validated before writing. One malformed entry makes
    Codex's entire configuration for the directory unloadable, including
    valid sibling servers, and even keys Codex ignores at the project
    layer are still type-checked, so a malformed ignored key fails the
    whole load too. "Codex will ignore what it doesn't understand" is
    false as a safety assumption; a generated file must never be the
    thing that bricks a session.
  - niwa never silently produces a hybrid server definition. Codex merges
    configuration layers recursively field by field, so a name collision
    with a server the developer already has yields a definition neither
    party wrote. Collisions are detected or made impossible by naming;
    they are never left to the merge.
  The existing `.mcp.json` distribution route keeps working unchanged as a
  compatibility path. It reaches Claude sessions only, and when a
  workspace distributes an `.mcp.json` without a structured declaration,
  apply reports that MCP servers reach Claude sessions only.
- **R16. Environment variables to Codex sessions.** Environment variables
  a workspace declares for sessions are delivered to Codex through the
  measured project-layer route, with values fully resolved before writing.
  The workspace-side declaration these variables come from is
  agent-neutral: a Claude-named configuration key never gates Codex
  environment delivery (R7). The delivery declares its requirement on
  directory trust (R3): the route is measured to be inert in an untrusted
  directory. Dotenv-file distribution remains agent-agnostic and separate.
- **R17. Directory trust as a delivered capability.** niwa writes the
  per-repository trust entry into the developer's own Codex configuration:
  additive, canonical paths, one entry per repository, retracting only
  keys niwa itself previously wrote, leaving the developer's own content
  untouched, and stable across repeated applies. An unreadable or
  malformed developer configuration fails neither create nor apply. For
  Claude, this capability is declared unavailable (no such concept):
  Claude Code keeps no per-directory trust record.
- **R18. Secret hygiene lands with the first secret.** The Codex payload
  configuration is written with the repository's secret file mode and
  covered by git-exclude in the same change that first writes secret
  material into it -- not after. All niwa-written Codex-side files at the
  instance root and in repositories carry git-exclude coverage, exactly as
  the Claude-side files do.
- **R19. No agent choice at creation; preparation is unconditional.**
  `niwa create` and `niwa apply` take no agent selection, and every apply
  delivers every enumerated agent's implemented capabilities. A
  codex-default workspace still materializes the whole Claude tree, and a
  claude-default workspace still materializes Codex's. (The prior attempt
  got this right; it must not regress.)
- **R20. Codex-side failures are loud.** Where the contract declares a
  Codex capability implemented, a delivery failure fails the apply with a
  clear error rather than degrading silently. The prior attempt's
  error-handling posture -- hard-failing where several Claude-side steps
  only warn -- is preserved, without changing the Claude-side posture in
  the first PR.
- **R21. Approval and sandbox posture reaches Codex sessions.** Codex's
  `approval_policy` and `sandbox_mode` are not on the project-layer
  denylist: both are measured to take effect from a trusted project
  layer and to revert to defaults the moment the trust entry is removed,
  putting them on the same side of the trust line as MCP servers and
  environment delivery. The second PR therefore delivers
  workspace-declared approval and sandbox posture to Codex sessions,
  declared implemented with a requirement on directory trust (R3), from
  an agent-neutral declaration source (R7). Three safety properties are
  required. Delivery is opt-in and absent by default: when the workspace
  config declares no posture, niwa writes neither key and Codex's own
  defaults apply unchanged -- niwa is never the reason a session runs
  with weaker guardrails than the developer chose for themselves. The
  posture niwa does write is reported at apply time. And approval
  posture and sandbox posture stay separate decisions -- the measured
  Codex setting that suppresses approval prompts most fully also
  disables filesystem and network sandboxing together, and a workspace
  declaration must never disable sandboxing as an unstated side effect
  of relaxing approvals.

### Documentation

- **R22. The gap list is generated, not written.** The user guide's
  account of what a Codex session does not get is generated from the
  declared-unavailable capabilities: a filter over the declaration table,
  grouped by reason kind, rendering each reason -- never a judgment call
  and never hand-maintained prose. A test regenerates the list and fails
  when the committed guide section drifts from it. If code and doc
  disagree, the code is right and the doc is a bug. The guide's safety
  list -- what niwa refuses to write into the developer's own Codex
  configuration -- remains a separate section and is not the gap list.
- **R23. The acceptance bar is inherited, restructured only in the open.**
  The 15 functional scenarios from the prior attempt
  (`test/functional/features/codex-agent.feature` on the retained
  `docs/dual-agent-workspace` branch) define what a working Codex session
  means and are the floor for the second PR. They may be restructured --
  scenario 2 must be: under the contract, dispatch's refusal becomes a
  declared-unavailable capability, and the scenario asserts the
  declaration rather than the bare refusal, so the test and the gap list
  cannot drift apart -- but any restructuring is recorded in the PR that
  performs it, and the bar is never silently lowered. Scenario 10
  (worktree context) stands as written: the measurement behind R13
  confirmed a worktree root is a Codex project root.
- **R24. New measurements reach the standing spike, never a fork.** Every
  measurement this work produces that extends or corrects the spike's
  findings is contributed to docs/spikes/SPIKE-codex-discovery-mechanics.md
  rather than to a second spike document. That covers the MCP schema and
  environment-policy semantics measured against codex-cli 0.147.0, the
  trust-gating results, the worktree-marker result (R13), and the
  approval/sandbox result (R21) -- and, importantly, two corrections to
  the spike itself: `project_root_markers` is accepted at the project
  layer (acceptance measured, effect untested), contradicting the
  spike's claim that project-root marker configuration cannot be carried
  there; and the measured project-layer denylist holds eight keys across
  roughly fifty probed, against the spike's figure of eleven -- not a
  claimed error, but an unresolved count with a stated completion
  method. Corrections strengthen the case for contributing to the
  standing document: a fork would leave two spikes disagreeing. While
  the spike lives on the unmerged tsukumogami/niwa#254, contributions
  are posted as structured comments on that pull request; once it
  merges, pending contributions land as an in-repo update to the spike
  file. This work creates no competing spike document under any
  circumstances.

### Non-functional

- **N1. Standard toolchain only.** The structural and characterization
  tests use the Go standard library (`go/ast`, `go/parser`, `go/token`)
  and introduce no new module dependencies. CI remains `gofmt -l .`,
  `go vet ./...`, and `go test -race ./...`.
- **N2. The first PR is reviewable mechanically.** Its review contract is
  the characterization test (R10) plus the structural tests (R5, R6), so
  a reviewer verifies the tests exist and pass rather than auditing the
  diff line by line for behavior change.
- **N3. Determinism.** The characterization and structural tests produce
  identical results across machines and repeated runs; anything
  machine-specific in written output is normalized (R10).

### The capability set and its target declarations

The initial capability set, with the state each agent's column must declare
once the second PR lands. Reason kinds: cannot-receive (the agent's own
mechanics put it out of reach), no-such-concept (the thing doesn't exist
for this agent), not-built (a route exists that niwa hasn't built).
Evidence for the Codex column is the standing spike's measured findings,
the measured codex-cli 0.147.0 behavior R24 feeds back to it, and the
retained prior-attempt branch. Every row is now grounded in measurement or
working prior-attempt code; none is inferred.

| # | Capability | Claude | Codex | Codex reason / notes |
|---|---|---|---|---|
| 1 | Workspace/group orientation reaches a repo session | Implemented | Implemented | Composed into each repo's own context file rather than placed at the root |
| 2 | A session at the workspace or instance root is oriented | Implemented | Implemented (amended) | Settled here as Unavailable (cannot-receive) on a reason measured false; see the amendment below the table |
| 3 | Repo-level orientation doc (requires trust for the budget override) | Implemented | Implemented | Requires: directory trust |
| 4 | Worktree-level orientation doc | Implemented | Implemented | Requires: directory trust; a linked worktree's `.git` pointer file satisfies the project-root marker (measured, R13) |
| 5 | Workspace-declared plugin skills usable in the session | Implemented | Implemented | Loads even untrusted; delivery must not depend on Claude Code's presence (R14) |
| 6 | Marketplace/plugin registration with the agent's plugin system | Implemented | Unavailable (cannot-receive) | Registration lives in the developer's own configuration; skills are delivered directly instead |
| 7 | Named subagent types | Implemented | Unavailable (no-such-concept) | Codex caches a plugin's agents directory and never surfaces it |
| 8 | MCP servers available to the session | Implemented | Implemented | Via R15; requires directory trust |
| 9 | Environment variables present in the session | Implemented | Implemented | Via R16; requires directory trust (measured) |
| 10 | Dotenv files written to declared paths | Implemented | Implemented | Agent-agnostic |
| 11 | Arbitrary source-to-destination file distribution | Implemented | Implemented | Agent-agnostic |
| 12 | Approval / sandbox posture | Implemented | Implemented | Via R21; requires directory trust (measured: both keys honored from the project layer, reverting when trust is removed) |
| 13 | Hooks (lifecycle commands) | Implemented | Unavailable (cannot-receive) | No demonstrated route installs a niwa-owned hook without a blocking review prompt |
| 14 | Work-summary hooks | Implemented | Unavailable (cannot-receive) | Delivered as hooks; follows row 13 |
| 15 | PR-body hook | Implemented | Unavailable (cannot-receive) | Delivered as a hook; follows row 13 |
| 16 | Worktree-hook delegation (or the deny fallback) | Implemented | Unavailable (no-such-concept) | The mechanism is Claude Code harness surface; Codex has neither the events nor the tools |
| 17 | Ephemeral-session provisioning | Implemented | Unavailable (cannot-receive) | Scoped to the trigger: only a session-start hook announces a session niwa did not launch; follows row 13. See the note below the table |
| 18 | Root-installed project skills (e.g. dispatch) | Implemented | Unavailable (not-built) (amended) | Settled here as cannot-receive on row 2's reason; see the amendment below the table |
| 19 | niwa's own plugin (migrate-config skill) | Implemented | Unavailable (not-built) | Codex accepts the identical plugin manifest; the wiring is unbuilt and out of this PRD's scope |
| 20 | Remote-control-at-startup | Implemented | Unavailable (no-such-concept) | Names claude.ai's remote-control bridge; no Codex equivalent |
| 21 | Dispatch keep-alive | Implemented | Unavailable (no-such-concept) | No background-session bridge to keep warm |
| 22 | Launching a background worker (niwa dispatch) | Implemented | Unavailable (not-built) | The launch path refuses non-Claude; the per-agent model table already carries Codex entries; out of this PRD's scope |
| 23 | Per-directory trust bootstrap | Unavailable (no-such-concept) | Implemented | Claude Code keeps no per-directory trust record; posture is settings-driven |
| 24 | Git-exclude bookkeeping for niwa-written files | Implemented | Implemented | Agent-agnostic; covers Codex-side names exactly as Claude-side ones |

Target totals for Codex: 11 implemented, 13 unavailable. For Claude: 23
implemented, 1 unavailable.

**Amendment to rows 2 and 18.** This matrix is cited by name in
`internal/agentplan/capability_test.go` as the authority its own map of final
reason kinds follows, so an unamended row here is a test disagreeing with the
document it says it is obeying. Two rows changed after this PRD was settled.

Both rested on one reason: Codex reads context only from the nearest
project-root marker downward, and an instance root has none. The second clause
is true and the conclusion does not follow. A session's own working directory
always contributes its context file — it is the last directory of the walk
whether the walk began at a marker-bearing ancestor or, with no marker anywhere
above, at the working directory itself. Measured against codex-cli 0.147.0 both
ways, with a negative control that puts the document one directory up and sees
it not arrive.

Row 2 is implemented: an instance root and the workspace root each carry a
context document, composed for an agent that cannot follow an `@import`. Row 18
stays unavailable and becomes not-built: Codex does load a skills tree from a
project layer at a root-started session's own working directory, untrusted, so
what is missing is niwa writing one there. Rows 5, 8, 9 and 12 stay
repository-scoped, which this matrix has no axis to express — see
`docs/designs/current/DESIGN-agent-capability-contract.md` for that limit and
`docs/guides/codex-agent.md` for what it means for a session at the root.

Row 22 is also no longer this matrix's answer: background dispatch for Codex was
built. The settled column is 13 implemented and 11 unavailable, asserted by
`TestCodexColumnTotals`, which is the authority the target totals above are not.

**Row 17 is scoped to the trigger, not to provisioning.** niwa learns that a
session it did not launch has started only from the agent's own session-start
event, so the row stands or falls with row 13, and Codex hooks are
plugin-delivered behind a blocking review prompt. The kind is cannot-receive to
match rows 14 and 15, the other two rows delivered through hooks;
`TestHookDeliveredRowsRestOnTheHooksRow` binds all three to row 13.

Provisioning for a session niwa *does* launch needs no hook and is not this row.
One agent-neutral provisioner, shared by the hook path and the dispatch path,
creates the instance before either agent's binary starts, and that delivery is
row 22 — implemented for both agents.

What this row has no axis to say is that the capability is out of reach in one
launch context and delivered in another. That is the same limit rows 5, 8, 9 and
12 meet, recorded above and in the design document, and it is a product decision
rather than something a reason correction should invent.

## Acceptance Criteria

Contract and structure:

- [ ] A test enumerates the full (capability, agent) cross product and
  fails when any pair lacks exactly one declaration; deleting any single
  declaration makes it fail.
- [ ] A test fails on each malformed-declaration shape: unavailable
  without a reason kind or reason text, implemented carrying either, and
  requirement edges on an unavailable declaration.
- [ ] A test fails when a capability's declared requirement is not itself
  implemented for the same agent, or when requirement edges form a cycle.
  Declaring Codex MCP implemented while Codex directory trust is
  unavailable is the canonical failing case.
- [ ] A test fails in both binding directions: an implemented declaration
  with no registered delivery behind it, and a delivery registered for a
  pair not declared implemented.
- [ ] A structural test over the preparation path's source rejects agent
  constants and agent context filenames at materializer call sites. Run
  against the pre-refactor tree it fails at the eight known sites; against
  the first PR's tree it passes.
- [ ] A structural test asserts the declaring layer performs no filesystem
  writes and launches no processes.
- [ ] The characterization test exists in a commit that predates the first
  refactor commit, pins every preparation-written file by path and content
  hash with a completeness guarantee (sourced from the managed-file
  record, not a hand-picked list), and passes unchanged at the first PR's
  head.
- [ ] The first PR changes no configuration surface: the same keys parse
  before and after, the generated example configuration is byte-identical,
  and no new warnings are emitted.
- [ ] The full test suite passes unchanged at the first PR's head; no
  existing test is modified or deleted, and new tests are only added.

Codex delivery:

- [ ] All 15 inherited scenarios pass in the second PR's tree, with
  scenario 2 asserting dispatch's declared unavailability, scenario 10
  standing as written, and every restructuring named in that PR's
  description.
- [ ] A workspace with a github-sourced marketplace delivers its skills to
  a Codex session on a machine with no Claude Code installation.
- [ ] Generating Codex MCP entries from a declaration containing an
  unmappable construct (an SSE server, or a value using `${VAR}`
  interpolation) produces a reported error and writes no partial file.
- [ ] When the developer's own Codex configuration already defines a
  server with a name niwa would write, generation reports the collision
  rather than silently writing the entry; and no configuration niwa
  writes leaves Codex's config for the directory unloadable.
- [ ] A workspace distributing `.mcp.json` with no structured MCP
  declaration produces an apply-time report that MCP servers reach Claude
  sessions only.
- [ ] The Codex payload configuration carries the secret file mode and a
  git-exclude entry in the same commit that first writes secret material
  into it; every niwa-written Codex-side file is git-excluded.
- [ ] A repository with Claude delivery disabled still receives full Codex
  delivery, and the reverse.
- [ ] Re-applying three times adds nothing, changes nothing, and leaves
  trust entries canonical and singular per repository.
- [ ] With a Codex delivery target made unwritable, apply fails with a
  named error identifying the capability, rather than warning and
  continuing.
- [ ] With no posture declared in workspace config, the generated Codex
  project-layer configuration contains neither `approval_policy` nor
  `sandbox_mode`, and apply reports no posture write -- the
  absent-declaration case is asserted directly, not inferred.
- [ ] A workspace-declared approval and sandbox posture appears in the
  generated Codex project-layer configuration, its delivery declares the
  directory-trust requirement, the written posture is reported at apply
  time, and no declaration that relaxes approvals changes the sandbox
  setting unless the workspace declared that too.
- [ ] Where the second PR renames a configuration key, the old key still
  parses with a deprecation warning, and setting both old and new keys
  fails with an error.

Documentation and process:

- [ ] The user guide's Codex gap list is produced by a generator from the
  declaration table; a test fails when the committed guide section differs
  from the generated output; editing the guide section by hand without a
  matching declaration change fails CI.
- [ ] Every unavailable declaration's reason appears in the generated gap
  list; the guide's safety list remains a distinct section.
- [ ] The measured findings this work produced -- the MCP schema,
  environment-policy semantics, trust-gating, the worktree-marker result
  (R13), the approval/sandbox result (R21), and the two spike
  corrections named in R24 -- are posted to tsukumogami/niwa#254 or
  committed into docs/spikes/SPIKE-codex-discovery-mechanics.md, and no
  new spike document exists in this work's deliverables.

## Out of Scope

- Refactoring parts of the CLI that dual-agent capability doesn't touch.
  The rule is that the refactor lands wherever the capability lands, not
  everywhere.
- Running two agents side by side in one session. Preparation defers the
  choice; it doesn't multiplex.
- Re-measuring Codex discovery mechanics already recorded in the standing
  spike. Two attempts to reason about them from outside got them wrong in
  opposite directions; the spike's measured findings are consumed, not
  re-derived. (The new measurements this work produced flow back to the
  spike per R24.)
- Building Codex dispatch (row 22) and Codex delivery of niwa's own plugin
  (row 19). Both are declared not-built and appear on the generated gap
  list; closing them is future work the declarations make visible.
- Support for agents beyond Claude Code and Codex. The contract must be
  the kind of thing a third agent could implement; delivering one is not
  this feature.
- Weakening the acceptance bar. The 15 scenarios may be restructured in
  the open (R23), never silently narrowed.

## Known Limitations

- A Codex session started at the workspace or instance root sees nothing
  niwa wrote -- context reaches Codex only inside repositories. This is
  Codex's own discovery mechanics, declared and listed, not fixable by
  niwa.
- What a plugin carries beyond skills -- commands, agents, hooks -- does
  not reach Codex sessions, because registration lives in the developer's
  own configuration.
- Hooks stay out of Codex sessions unless a route without a blocking
  review prompt is demonstrated. If one is, the reason kind flips from
  cannot-receive to not-built and dependent rows flip with it; the state
  stays unavailable either way, so the gap list is correct today
  regardless.
- A developer whose own Codex configuration narrows the environment (an
  `include_only` allowlist) silently drops variables niwa delivers
  through the project layer -- measured behavior of the layer merge, with
  no error surfaced by Codex. The user guide must say so next to the gap
  list.
- The environment-policy measurements were taken through Codex's
  user-invoked sandbox entry point; the in-session shell tool is believed
  but not proven to resolve the same policy. Any security-sensitive claim
  built on the environment defaults needs the in-session confirmation
  first (R24 carries it to the spike when taken).

## Decisions and Trade-offs

Closing the brief's four open questions, plus decisions carried in from
the exploration the chain consumed.

1. **How far the first PR's contract reaches (R11).** Decided:
   declarations, binding, and the structural tests are path-wide from the
   first PR; delivery restructuring in the first PR covers the
   agent-shaped surfaces only (the eight context-writer sites and the
   settings-document builder). Alternatives: restructure everything in the
   first PR (rejected -- it rewrites config-driven materializers that
   contain no agent-specific logic, exactly the speculative refactor the
   scope boundary excludes, inside a PR whose job is to be invisible); or bring
   only context files under the contract (rejected -- that reproduces the
   prior failure at smaller scale, a contract governing two capabilities
   of twenty-four). The chosen cut makes the contract govern the whole set
   from day one at the declaration level while confining behavior-risk to
   the surfaces the characterization test pins hardest.
2. **The MCP delivery shape (R15).** Decided: a structured agent-neutral
   declaration generating both agents' native formats, with the existing
   `.mcp.json` distribution kept as a reported, Claude-only compatibility
   path. Alternative: parse the `.mcp.json` niwa already distributes
   (rejected on measured evidence -- translation is lossy in both
   directions: Codex silently serves a declared SSE server over a
   different protocol, performs no `${VAR}` interpolation, and carries
   fields `.mcp.json` cannot express; and a Claude-format file as the
   source of Codex delivery would put one agent's format in front of
   another agent's delivery, against R7's spirit).
3. **The capability matrix's unresolved rows.** All settled by
   measurement against codex-cli 0.147.0, none guessed: environment
   delivery is trust-gated (the requirement edge in row 9 is measured,
   toggled reversibly by the trust entry alone, not inferred by
   grouping); the Codex MCP schema is pinned; a linked worktree's `.git`
   pointer file satisfies the project-root marker, so row 4 stays
   implemented and scenario 10 stands (R13); and approval and sandbox
   posture is settable from a trusted project layer and reverts with
   trust, so row 12 -- the matrix's only hard unresolved row -- lands
   implemented (R21). That last result carries a scope decision: the
   upstream brief's delivery list was written while the row was
   unresolved and doesn't name approval posture; this PRD brings it into
   the second PR because the route is now measured, the write is the
   same trust-gated project layer the PR already produces for MCP and
   environment, and it's the gap a developer notices first. The
   alternative -- declare it not-built and defer -- was rejected as
   declaring a gap the measurement just closed. The hooks reason kind
   could still flip from cannot-receive to not-built if a non-blocking
   route is demonstrated; the state is unavailable either way, so
   nothing downstream blocks on it. The skills github-marketplace
   question was settled as an implementation obligation: build the
   niwa-owned fetch (R14) rather than ship a capability whose truth
   depends on another vendor's CLI having run.
4. **How measurements reach the standing spike (R24).** Decided:
   contribute, never fork -- comments on tsukumogami/niwa#254 while it is
   open, an in-repo spike update once it merges. Alternatives: a new
   spike document (rejected -- it would compete with the standing spike
   and split the measured record), or holding the findings in this chain's
   working notes (rejected -- working notes are non-durable and the
   findings are load-bearing for rows 4, 8, 9, and 12). The question
   turned out to be more than additive: two of the findings correct the
   spike (the `project_root_markers` acceptance and the denylist count),
   which settles the fork question decisively -- a fork would leave two
   spike documents disagreeing about measured behavior.
5. **Two states, not three.** A "conditional" state was rejected upstream
   and the rejection holds here: trust is a capability niwa delivers, so
   trust-dependent capabilities are implemented with a requirement edge a
   closure test enforces, and the gap-list generator stays a filter
   rather than a judgment. The cost, accepted: a few rows read oddly for
   one agent (directory trust is unavailable-no-such-concept for Claude);
   the guide renders no-such-concept rows as "does not apply" notes
   rather than gaps.
6. **Zero renames in the first PR (R8).** A compatibility alias is
   behavior-preserving but not diff-free -- it adds warnings and
   regenerates example configuration -- so all renames ride the second
   PR, where a second agent first gives a Claude-named key something to
   mis-gate.
7. **Characterization before refactor (R10).** Committing the
   characterization first means it pins current behavior rather than
   being written to match new code. The alternative -- trusting the
   existing suite -- was rejected because the suite asserts on a
   hand-picked subset of paths and has no completeness check.

## References

- docs/briefs/BRIEF-agent-capability-contract.md -- the upstream framing
  this PRD's requirements are written from.
- docs/spikes/SPIKE-codex-discovery-mechanics.md (landing via
  tsukumogami/niwa#254) -- measured Codex discovery behavior, consumed not
  re-derived; the destination for this work's new measurements (R24).
- docs/designs/current/DESIGN-claude-key-consolidation.md -- the config
  rename precedent (R8), and a documented decision this work partially
  reverses: it consolidated content configuration under the Claude
  namespace on the grounds that content is entirely Claude-coupled, which
  dual-agent capability is precisely what falsifies.
- tsukumogami/niwa#248 -- the closed prior attempt (branch retained at
  `docs/dual-agent-workspace`), whose Codex-side composition mechanics
  remain sound, whose 15 functional scenarios set the acceptance bar
  (R23), and whose structural failure this PRD's tests exist to make
  unrepeatable.
