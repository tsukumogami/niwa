# /brief Discovery: agent-capability-contract

Chain-driven run. The scoping conversation is replaced by the consumed
/explore handoff (wip/scope_agent-capability-contract_handoff.md), the
exploration findings and decisions files, and the originating dispatch brief.
This file records the problem/outcome pair and the journey sketch derived
from them.

## Problem candidate

niwa's workspace-preparation path is structurally Claude-shaped. The
`agent.Agent` type meant to unify agents reaches two of roughly twenty
capabilities the path delivers; hooks, settings, permissions, plugins,
marketplaces, environment injection, worktree-hook delegation,
ephemeral-session provisioning, and root skills take no agent parameter at
all. A prior attempt to add Codex (tsukumogami/niwa#248, closed as a
prototype, branch retained) shipped a dead abstraction: the agent value was
threaded through the applier and read by nothing while every call site
hardcoded an agent constant. The defect predates that attempt:
`agent.LocalContextFileName()` has zero callers on main, because two
functions accept an agent, use it only as a run/skip gate, then hardcode the
Claude filename inside the gated body. There is no contract for a second
agent to implement and no test that fails when one is faked.

## Outcome candidate

A developer prepares a workspace once and the instance serves both Claude
Code and Codex sessions, with no agent choice forced at creation time. Where
Codex lacks a capability, the developer learns it from a single gap list in
the user guide, generated from the declarations in code rather than written
by hand. A reviewer or CI can check the structural claims mechanically:
agent-specific behavior is reached through the contract, every capability is
in exactly one of two states per agent, and the first delivery provably
changes no behavior.

## Journey sketch

1. Developer on a mixed-agent team prepares an instance and opens a Codex
   session — everything the contract implements arrives; gaps are declared.
2. Reviewer of the first (no-behavior-change) PR — characterization test
   plus structural tests replace line-by-line trust.
3. Developer hits a Codex gap (hooks, named subagents) — the guide's
   generated gap list answers with a reason, and cannot drift from code.
4. Contributor adding agent-specific behavior later — the contract forces an
   explicit answer per agent; a capability in neither state fails a test.

## Constraints carried from the mandate (binding)

- Two PRs, sequenced: contract first against existing Claude behavior only,
  no behavior change; Codex second, delivered through the contract.
- Four structural properties, each enforced by a test: no agent constants at
  materializer call sites; every capability in exactly one of two states per
  agent with a reason on the unavailable side; no agent's name gates another
  agent's delivery (rename with compatibility alias per repo precedent); the
  generic/specific boundary legible from the package layout.
- Guide gap list generated from declared-unavailable capabilities; if code
  and doc disagree, the code is right and the doc is a bug.
- Codex discovery mechanics consumed from the standing spike
  (docs/spikes/SPIKE-codex-discovery-mechanics.md, landing via
  tsukumogami/niwa#254), never re-derived.
- The 15 functional scenarios on the closed docs/dual-agent-workspace branch
  are the acceptance bar; restructuring allowed, silently lowering it not.

## Settled by exploration (kept out of the brief's open questions)

- Two capability states plus Requires edges; no third "conditional" state.
- Plan-producing leaf package plus one agent-blind executor (design-level;
  the brief names the boundary requirement, not the mechanism).
- PR 1 ships zero config renames; renames land with PR 2.
- Characterization test committed before the refactor.
- Plugin-skill dependency on a Claude-owned directory is fixed, not declared.
- Payload-config permission and git-exclude fixes land in the same change
  that first writes secrets to that file.

## Open at handoff (deferred to the PRD)

- How far the first PR's contract reaches across the capability set — the
  chain's central scope question, a requirements-level answer.
- MCP delivery shape: parse the distributed file vs. a structured
  agent-neutral declaration generating both formats.
- Five unresolved capability-matrix rows, three under active measurement;
  one (worktree .git-file project-root marker) could flip an acceptance
  scenario from satisfiable to not.
- How new measurements reach the standing spike, which lives on an open PR
  this work does not own.
