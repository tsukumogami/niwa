# Explore Scope: agent-capability-contract

## Visibility

Public

## Execution Mode

auto (dispatched background session; rounds run until leads are exhausted)

## Entry Assessment

Result: needs investigation
Confidence: high
Dissent: none
Signals cited: arrived as a dispatch brief, not an issue. The prior attempt
(tsukumogami/niwa#248) shipped and was closed as a prototype because its
abstraction was dead code -- `Applier.Agent` read once into a field with no
readers, `Agent.LocalContextFileName()` ignoring its receiver with no callers,
and every materializer call site hardcoding an agent constant. The open question
is structural.

## Core Question

What contract can niwa's workspace-preparation path route agent-specific
behavior through, such that "no agent constants at materializer call sites" and
"every capability is either implemented or explicitly declared unavailable with
a reason" become properties a test fails on rather than claims a design doc
makes? And once that contract exists, what does Codex need from it to be a
co-equal second implementation?

## Context

`internal/agent/agent.go` already exists on main: an `Agent` string
discriminator with `AgentClaude`/`AgentCodex` constants, `ParseAgent`,
`ResolveAgent`, and three accessors (`RootContextFileName`,
`LocalContextFileName`, `WritesRepoLevelContext`). Codex is inert on main --
the type exists but nothing materializes for it.

The preparation path is large: `internal/workspace/apply.go` (103KB),
`materialize.go` (83KB), `override.go` (44KB), `worktree_content.go` (42KB),
`root_materializer.go` (19KB), `content.go` (13KB). The refactor is scoped to
the paths dual-agent capability actually touches, not the whole CLI.

The prior attempt is closed but its branch (`docs/dual-agent-workspace`) is
retained, carrying `internal/workspace/codex_*.go`, 81 test functions, and 15
functional scenarios in `test/functional/features/codex-agent.feature`.

Measured `codex-cli 0.147.0` discovery mechanics are on the unmerged
`tsukumogami/niwa#254` (`docs/spikes/SPIKE-codex-discovery-mechanics.md`) and
must not be re-derived.

## In Scope

- The workspace-preparation path: apply -> materialize -> override -> content ->
  worktree content -> root materializer, and the config surfaces that gate them.
- A capability contract with per-agent implementations and declared-unavailable
  states, plus the test mechanism that enforces both structural properties.
- Config-surface renaming where an agent name gates shared behavior, with a
  compatibility alias.
- Codex delivery: context composition, skills, MCP servers, environment,
  trust, git-exclude. Hooks are presumed out but must be declared, not omitted.

## Out of Scope

- Refactoring parts of the CLI that dual-agent capability does not touch.
- Running two agents side by side in one session; preparation defers the choice,
  it does not multiplex.
- Re-measuring codex-cli discovery behavior (already spiked).
- Any edit to, or reference to, private repositories.

## Research Leads

1. **What does niwa's workspace-preparation path actually do today, and where
   are its agent-specific decision points?** (`lead-prep-path-map`)
   Map the call graph from `Applier.Apply` through the materializers, and mark
   every place where a Claude-specific filename, directory, config key, or
   behavior is decided. This is the raw material the interface has to cover, and
   without it the boundary in property 4 is guesswork.

2. **What discrete capabilities does preparation deliver, and what governs
   each?** (`lead-capability-inventory`)
   Enumerate context files, plugin/skill installation, MCP config, environment
   delivery, hooks, permissions/settings, git-exclude, and anything else the
   path writes. For each: where it is implemented, what config surface gates it,
   and whether the gate carries an agent name. Property 2 needs a closed set to
   quantify over.

3. **What is on the closed `docs/dual-agent-workspace` branch, and which parts
   are lift-able?** (`lead-prior-attempt-audit`)
   Read the branch: `internal/workspace/codex_*.go`, the feature file, the docs.
   Report the exact call sites that hardcode an agent constant, what
   `ClaudeEnabled` gates, and what the payload config declares -- so the new
   design can be checked against the failure it exists to prevent.

4. **Which config surfaces and internal gates carry an agent's name, and what is
   niwa's precedent for renaming a shipped config key?** (`lead-config-rename`)
   Property 3 requires agent-neutral names plus a compatibility alias. Find the
   existing alias/deprecation mechanism in `internal/config` and every key or
   field that would need one.

5. **How does this repo enforce structural properties in tests today?**
   (`lead-structural-test-precedent`)
   Properties 1 and 2 have to fail a test. Look for existing AST-walking tests,
   `go/ast`/`go/packages` usage, golden files, or lint-shaped unit tests. If no
   precedent exists, report what building one would cost and what the repo's
   test conventions would demand of it.

6. **What does SPIKE-codex-discovery-mechanics record, and what does it
   constrain?** (`lead-spike-constraints`)
   Read the spike on the `worktree-codex-findings` branch in full. Extract the
   hard constraints on any Codex implementation: the bounded downward walk,
   strict first-match, the shared byte budget draining outermost-first, how
   trust gates the project config layer, what a project layer can carry, hooks
   being plugin-delivered and trust-gated, and plugin-manifest portability.

7. **What in-repo Go patterns exist for interfaces with multiple
   implementations, registries, and capability negotiation?**
   (`lead-go-pattern-precedent`)
   The contract should look like niwa, not like a foreign framework. Survey
   `internal/plugin`, `internal/config/registry.go`, `internal/source`,
   `internal/cli/plugin_adapter.go` and anything similar for the house style on
   interface definition, registration, and package layout.

8. **How are marketplace/plugin skills resolved today, and where does that
   resolution depend on Claude Code being installed?** (`lead-skill-resolution`)
   The prior attempt resolved GitHub-marketplace skills out of Claude Code's own
   user-global plugin directory, so a machine without Claude Code got no skills
   and could not self-heal. Establish where that dependency lives on main and
   what a Claude-Code-independent route would cost.
