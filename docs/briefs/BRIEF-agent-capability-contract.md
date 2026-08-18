---
schema: brief/v1
status: Done
problem: |
  niwa's workspace-preparation path is structurally Claude-shaped: the type
  meant to unify agents reaches two of roughly twenty capabilities. There
  is no contract a second agent could implement and no test that fails
  when one is faked, so the prior Codex attempt shipped as dead plumbing.
outcome: |
  A prepared instance serves both Claude Code and Codex with no agent
  choice forced at creation time. Codex gaps appear in the user guide's
  gap list with reasons, generated from declarations in code, and the
  structural claims are enforced by tests that fail on regression.
motivating_context: |
  The first attempt at dual-agent support (tsukumogami/niwa#248) shipped
  and was closed, branch retained: its agent value was threaded through
  the applier and read by nothing while every call site hardcoded an
  agent constant. That defect is live on main today, which makes the
  contract repair work in its own right, not scaffolding for Codex.
---

# BRIEF: agent capability contract

## Status

Done

This brief frames the second attempt at dual-agent workspace preparation. It
settles the framing: the contract lands first, against existing Claude
behavior only and provably without behavior change, and Codex arrives as its
second implementation. The downstream PRD owns the requirements — including
the chain's central open question, how far the first delivery's contract
reaches across the capability set.

## Problem Statement

When niwa prepares a workspace instance — context files, settings, hooks,
permissions, plugins, skills, environment, trust — nearly every step is
Claude-shaped by construction. The `agent.Agent` type that was meant to make
the path agent-aware governs two of the roughly twenty capabilities the path
delivers: which filename root and group context land in, and whether
repository-level context is written at all. Everything else takes no agent
parameter and runs Claude-shaped unconditionally.

The first attempt to add OpenAI Codex on top of this
(tsukumogami/niwa#248) shipped and was closed as a prototype. Its failure
was structural: the abstraction meant to unify the two agents was dead code.
The agent value was threaded through the applier and read by nothing, while
every materializer call site hardcoded an agent constant — two hardcoded
passes where a shared contract should have been. Its design had diagnosed
exactly this risk and prescribed the cure, and the code shipped the disease
anyway, because the replacement structure was never something a test could
fail on.

That defect isn't something the prior attempt introduced. On main today,
`agent.LocalContextFileName()` has zero callers anywhere in the module: two
functions accept an agent parameter, use it only as a run/skip gate, and
then hardcode the Claude filename inside the gated body. The accessor that
was supposed to make the path agent-aware is dead, and nothing fails because
of it.

So the problem is not that Codex support is missing. It is that there is no
contract for a second agent to be an implementation of, and no test that
fails when one is faked. Until that exists, any Codex delivery — however
careful its file composition — collapses back into a parallel hardcoded
pass, and a user has no honest account of what a non-Claude session actually
gets: the prior attempt's guide described Codex as a delta from a Claude
baseline and scattered its gaps across a design's negative-space section and
a scope note, where no reader would assemble them into an answer.

## User Outcome

A developer prepares a workspace instance once and it serves whichever agent
they or a teammate opens it with. No agent choice is forced at creation
time; a Claude Code session and a Codex session both find their context,
skills, and configuration in place. Preparing for one agent never silently
disables delivery for the other.

Where Codex genuinely can't have something — capabilities Codex itself
doesn't surface, or routes niwa deliberately refuses — the developer learns
it from one plain list in the user guide. That list is generated from the
capability declarations in code, so a gap that gains a reason in code can't
fail to appear in the doc, and the two can't drift apart. If they ever
disagree, the code is right and the doc is a bug.

For the maintainer and reviewer, the structural claims stop being a matter
of trust. Whether a piece of logic is generic or agent-specific is visible
in the package layout rather than only in function bodies, and each
structural claim is held by a test that fails on regression, so reviewing
one reduces to checking that its test exists and passes. The first
delivery, which lands the contract against existing Claude behavior only,
carries a mechanical proof that it changed nothing a user can observe.

## User Journeys

### Developer on a mixed-agent team opens a Codex session

A developer's team uses both Claude Code and Codex. They run `niwa create`
on the shared workspace — same command, no agent flag — and open a Codex
session in the prepared instance. The session finds its context files
composed within Codex's discovery rules, the workspace's skills namespaced
and reachable, MCP servers and environment variables delivered, and the
instance trusted, without the developer hand-editing Codex configuration.
The teammate who opens the same instance with Claude Code loses nothing.

### Reviewer verifies the no-behavior-change delivery

A maintainer reviews the first pull request, whose whole job is to
restructure without changing behavior. Instead of reading a large refactor
line by line and taking the title's word for it, they look at two things: a
characterization test, committed against the current behavior before the
refactor began, that pins every file the preparation path writes; and the
structural tests that fail if an agent constant appears at a materializer
call site. The diff can be large and still reviewable, because the proof is
mechanical rather than asserted.

### Developer hits a Codex gap and gets a straight answer

A developer expects their workspace's hooks to fire in a Codex session, and
they don't. They open the user guide and find the gap list: one section,
generated from the declared-unavailable capabilities, stating that hooks
don't reach Codex sessions and why — here, that no known route installs a
niwa-owned hook without Codex blocking the session on a review prompt.
They didn't have to discover the gap by experiment, and the answer can't
have drifted from what the code does, because it was derived from the
code's own declarations.

### Contributor adds a capability and the contract forces the question

A contributor picks up a request for new agent-specific behavior in the
preparation path: a delivery step one agent needs, or support for a third
agent. The contract confronts them with the whole capability set: for each
capability, either an implementation or an explicit unavailability with a
reason. Leaving a capability in neither state fails a test, and the gap
list picks up the new declarations without anyone editing the guide. The prior attempt's failure mode — an interface
nothing reads, satisfied by two hardcoded passes — is no longer something a
well-intentioned contributor can reproduce, because it no longer passes CI.

## Scope Boundary

**In:**

- The workspace-preparation path: apply through the materializers, and the
  config surfaces that gate them.
- A capability contract with per-agent implementations and explicit
  declared-unavailable states carrying reasons, plus the tests that enforce
  both — including that a capability in neither state fails, and that the
  generic/specific boundary is visible in the package layout rather than
  only in function bodies.
- A mechanical no-behavior-change proof for the first delivery: a
  characterization of what the preparation path writes, committed before
  the refactor it gates.
- Config-surface renaming where one agent's name gates shared behavior,
  with a compatibility alias following the repo's existing rename
  precedent (docs/designs/current/DESIGN-claude-key-consolidation.md),
  landing with the delivery that gives the rename a second agent to
  mis-gate, not before.
- Codex delivery through the contract: context composition, skills, MCP
  servers, environment variables, directory trust, and git-exclude
  coverage, including the file-permission and exclude fixes that must land
  in the same change that first writes secret material to the instance's
  payload config.
- A user guide whose gap list is generated from the declared-unavailable
  capabilities.
- Two sequenced pull requests: the contract against existing Claude
  behavior only, then Codex as its second implementation.

**Out:**

- Refactoring parts of the CLI that dual-agent capability doesn't touch.
  The rule is that the refactor lands wherever the capability lands, not
  everywhere.
- Running two agents side by side in one session. Preparation defers the
  choice; it doesn't multiplex.
- Re-measuring Codex discovery mechanics already recorded in the standing
  spike (docs/spikes/SPIKE-codex-discovery-mechanics.md). Two attempts to
  reason about them from outside got them wrong in opposite directions;
  the spike's measured findings are consumed, not re-derived.
- Weakening the acceptance bar. The 15 functional scenarios from the prior
  attempt define what a working Codex session means; they may be
  restructured, but not silently narrowed.
- Support for agents beyond Claude Code and Codex. The contract must be
  the kind of thing a third agent could implement, but delivering one is
  not this feature.

## Open Questions

- **How far the first delivery's contract reaches.** The exploration costed
  bringing the context writers and the settings-document builder under the
  contract and set the hooks, environment, and files materializers aside,
  but that was an implementation estimate, not a requirements decision. It
  is the difference between a contract governing two capabilities and one
  governing enough to matter, and the PRD owes the requirements-level
  answer.
- **The MCP delivery shape.** Whether niwa starts parsing the MCP file it
  already distributes, or adds a structured agent-neutral declaration that
  generates both agents' formats. Constructs that don't map between formats
  must be reported rather than dropped, whichever way this lands.
- **Five unresolved rows of the capability matrix,** three under active
  measurement against the spike's exact Codex build. One could invert: if a
  linked worktree's `.git` file doesn't satisfy Codex's project-root
  marker, worktree context flips from implemented to unavailable and one
  acceptance scenario becomes unsatisfiable as written. The framing here
  survives either result: if the row inverts, the scenario is restructured
  in the open, per the scope boundary's rule against silent narrowing.
- **How the new measurements reach the standing spike,** which lives on an
  open pull request this work doesn't own. The chain must pick a mechanism
  rather than fork a competing spike.

None of these block the brief; each defers a requirements- or design-level
determination to the downstream PRD and design.

## References

- docs/spikes/SPIKE-codex-discovery-mechanics.md — measured Codex discovery
  behavior this work consumes rather than re-derives (landing via
  tsukumogami/niwa#254).
- docs/designs/current/DESIGN-claude-key-consolidation.md — the config
  rename precedent, and a documented decision this work partially reverses:
  it consolidated content configuration under the Claude namespace on the
  grounds that content is entirely Claude-coupled, which dual-agent
  capability is precisely what falsifies.
- tsukumogami/niwa#248 — the closed prior attempt (branch retained), whose
  Codex-side composition mechanics remain sound and whose 15 functional
  scenarios set the acceptance bar.
