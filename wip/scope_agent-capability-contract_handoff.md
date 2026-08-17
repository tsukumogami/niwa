# /scope Handoff: agent-capability-contract

## Provenance

Written by `/explore` on 2026-08-17 from
`wip/explore_agent-capability-contract_crystallize.md`. Research files:
`wip/explore_agent-capability-contract_findings.md`,
`wip/explore_agent-capability-contract_decisions.md`, and
`wip/research/explore_agent-capability-contract_r*_lead-*.md`.

The exploration ran two discover-converge rounds plus a third-round measurement
spike. Round 1 fanned out eight leads to map the preparation path, inventory its
capabilities, audit a closed prior attempt, and find precedent for the config
rename, the structural tests, the Go patterns, and the plugin-skill mechanism.
Round 2 sent five leads at the four tensions round 1 produced, and resolved all
four. Along the way the exploration narrowed hard: it ruled out re-deriving Codex
discovery behavior, ruled out a new `golang.org/x/tools` dependency for the
property tests, ruled out a whole-table config rename as disproportionate, and
ruled out the three-state capability model the first round had assumed it needed.

## Problem Statement

niwa's workspace-preparation path is structurally Claude-shaped, and the type
that was supposed to unify agents governs almost none of it: `agent.Agent`
reaches two of roughly twenty capabilities, while hooks, settings, permissions,
plugins, marketplaces, environment injection, worktree-hook delegation,
ephemeral-session provisioning, and root-installed skills take no agent parameter
at all. A prior attempt to add Codex on top of this shipped and was closed as a
prototype, because its abstraction was dead code -- the agent value was threaded
through the applier and read by nothing, while every call site hardcoded an agent
constant instead. The problem is not that Codex is missing; it is that there is
no contract for a second agent to be an implementation of, and no test that fails
when one is faked. What this chain must produce is a contract whose structural
properties a reviewer or CI can check, and then Codex delivered through it.

## Scope Boundary

### In scope

- The workspace-preparation path: apply through the materializers, and the config
  surfaces that gate them.
- A capability contract with per-agent implementations, explicit
  declared-unavailable states carrying reasons, and the tests that enforce both.
- Config-surface renaming where an agent's name gates shared behavior, with a
  compatibility alias following existing repo precedent.
- Codex delivery: context composition, skills, MCP servers, environment, trust,
  git-exclude coverage.
- A user guide whose gap list is generated from the capability declarations.

### Out of scope

- Refactoring parts of the CLI that dual-agent capability does not touch. The
  rule is that the refactor lands wherever the capability lands, not everywhere.
- Running two agents side by side in one session. Preparation defers the choice;
  it does not multiplex.
- Re-measuring Codex discovery mechanics already recorded in a standing spike.
  Two attempts to reason about them from outside got them wrong in opposite
  directions.
- The hooks, environment, and files materializers, which round 2 costed and set
  aside as config-driven rather than agent-driven. This exclusion is a proposal,
  not a settled boundary -- see Coverage Notes.

## Decisions Already Settled

- **Two capability states, not three.** Trust is a capability niwa delivers, not
  an external precondition, so the cases that looked conditional become
  implemented with a `Requires` edge, enforced by a closure test. A "conditional"
  state was rejected because it is a soft word a real gap hides inside, and
  because it would force the guide's gap-list generator to make a judgment rather
  than apply a filter.
- **A leaf package produces plans; one agent-blind executor performs them.** The
  boundary is "reads inputs, declares outputs", enforced by asserting the leaf
  never writes to disk. Per-agent files inside the existing workspace package
  were rejected: the repo has no precedent for that convention, and it makes
  every assertion cost a temporary directory and a full apply.
- **The first PR ships zero config renames.** A compatibility alias is
  behavior-preserving but not diff-free, and a PR whose job is to be invisible
  should not add warning text and regenerate example config.
- **The characterization test is committed before the refactor, not after,** so
  it characterizes current behavior rather than being written to match new code.
- **Codex discovery mechanics are consumed, not re-derived.**
- **The plugin-skill dependency on a Claude-Code-owned directory is fixed rather
  than declared,** because niwa already owns the primitives that close it.

## Coverage Notes

- **Which capabilities the first PR brings under the contract is unsettled.**
  Round 2 costed the context writers and the settings-document builder, and set
  the hooks, environment, and files materializers aside. That was an
  implementation estimate, not a requirements decision, and it is the difference
  between a contract governing two capabilities and one governing enough to
  matter. The chain owes a requirements-level answer.
- **The MCP surface is undecided** between parsing the file niwa already
  distributes and adding a structured agent-neutral declaration that generates
  both formats. The exploration recommends the latter and flags that constructs
  which do not map must be reported rather than dropped.
- **Five rows of the capability matrix are unresolved,** three of them under
  active measurement. One could invert: if a linked worktree's `.git` file does
  not satisfy the project-root marker, worktree context moves from implemented to
  unavailable and one acceptance scenario becomes unsatisfiable as written.
- **A permission and git-exclude defect must be sequenced against environment
  delivery.** The payload config is written world-readable and is not excluded at
  the instance root. Harmless while it carries only a byte budget; a leak the
  moment secrets land in it. The plan must put the fix in the same increment.
- **How the new measurements reach the standing spike is undecided.** That
  document lives on an open pull request this work does not own, so the chain
  must pick a mechanism rather than fork a competing spike.

## Upstream Observations

The exploration read a standing spike recording measured Codex discovery
mechanics, which is itself on an unmerged pull request rather than on the main
branch; its findings are treated as authoritative and its gaps as the reason for
the third-round measurement. It also read the closed prior attempt in full --
its pull request, its closing comment, its design document, its guide, its
Codex-specific source files, and its fifteen acceptance scenarios -- and
`docs/designs/current/DESIGN-claude-key-consolidation.md`, which supplies the
rename precedent this work will follow and, notably, argues against its own
premise: it consolidated content configuration *under* the Claude namespace on
the grounds that content is entirely Claude-coupled, which dual-agent capability
is precisely what falsifies. No ROADMAP is passed on the upstream flag.

## Framing-Shift Answer

**Pre-supplied answer:** yes, the framing shifted.

**Evidence:** the exploration entered framing this as adding Codex behind an
abstraction, and round 1 moved the boundary. The dead-accessor defect the prior
attempt was closed for exists on the main branch already --
`LocalContextFileName()` has zero callers module-wide, and two functions accept
an agent parameter, use it only as a run/skip gate, then hardcode the Claude
filename inside the gated body. So the first PR is not preparatory scaffolding
for Codex; it repairs a live structural defect that predates the prior attempt
and would outlive it. Round 2 moved the success criterion too: "the existing
suite passes unchanged" was replaced as the no-behavior-change proof by a
manifest-based characterization test, because the existing suite asserts on a
hand-picked subset of paths and has no completeness check.

## Shape Signals

### Architectural alternatives left open

- **Plan-producing leaf package versus an interface implemented inside the
  workspace package.** The exploration recommends the former and costed it at
  roughly 1100 lines touched, most of it deletions of repeated directory-create
  and file-write pairs; the latter is cheaper to start but can only be asserted
  against a filesystem, and invents a per-agent-file convention with no precedent
  in this repo.
- **MCP by parsing the distributed file versus a new structured declaration.**
  The first needs niwa to start parsing a file it currently only copies; the
  second adds a config surface and must keep the existing distribution route
  working as a compatibility path.
- **Fixing the plugin-skill dependency versus declaring it a limitation.** The
  brief permits either. The fix is cheap because the fetch primitives already
  exist, but it is net-new capability inside a PR that is already large.
- **How far the first PR's contract reaches.** Context writers only, or context
  writers plus the settings document, or wider. Named here rather than settled
  because it is the chain's central scope question.

### Complexity signals

- The preparation path carries 567 Claude references across nineteen non-test
  files, concentrated in one 44KB merge-and-override module and one 83KB
  materializer.
- One function concentrates seven capabilities across three call sites with no
  agent parameter anywhere, and converting it deletes two of three duplicated
  write blocks -- a net simplification hiding inside a refactor.
- The delivery is explicitly two pull requests, sequenced, with the first
  required to be behavior-preserving and provably so.
- Four structural properties must each become a test that fails on regression.
  One of them is red today at eight sites, which is the evidence that the
  property is real rather than decorative.
- The capability matrix runs to twenty-four rows across two agents, with five
  rows unresolved at handoff time.
