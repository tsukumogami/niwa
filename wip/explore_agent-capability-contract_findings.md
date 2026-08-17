# Exploration Findings: agent-capability-contract

## Round 1

Eight leads dispatched, eight returned. Sources are the per-lead files under
`wip/research/explore_agent-capability-contract_r1_lead-*.md`.

### Key insights

**The discriminator reaches almost nothing.** The capability inventory found
roughly twenty distinct capabilities the preparation path delivers, and
`agent.Agent` governs two of them: which filename root/group context lands in,
and whether repository-level context is written at all. Everything else --
hooks, settings, permissions, plugins, marketplaces, env injection, worktree-hook
delegation, ephemeral-session provisioning, root skills -- takes no `agent.Agent`
parameter and runs Claude-shaped unconditionally. (`lead-capability-inventory`,
`lead-prep-path-map`)

**Two of the three existing accessors are already broken in the way the mandate
describes.** `InstallRepoContentTo` (`content.go:130`) and
`installWorktreeContextLayer` (`worktree_content.go:740`) accept `agent.Agent`
and use it only as a run/skip gate, then hardcode `"CLAUDE.local.md"` inside the
gated body instead of calling `LocalContextFileName()`. That accessor
consequently has zero callers anywhere in the module -- on `main`, before the
prior attempt. The dead-accessor problem is not something PR #248 introduced; it
inherited it. (`lead-prep-path-map`)

**The prior attempt's failure is reproducible from its source.** Every
niwa-owned level writes both agents' files by iterating a hardcoded
`materializedAgents` slice with `agent.AgentClaude`/`agent.AgentCodex` literals
at each call site, while `Applier.Agent` -> `WorktreeApplyOptions.Agent` is
threaded and read by nothing. The design predicted this exact dead plumbing and
prescribed retiring it; the code kept it. (`lead-prior-attempt-audit`)

**Both mandated property tests are buildable with the standard library and no
new dependency.** `go/ast`, `go/parser`, `go/token` are stdlib;
`golang.org/x/tools` is not currently a dependency and would have to clear
`go mod tidy` review. CI runs exactly `gofmt -l .`, `go vet ./...`, and
`go test -race ./...` -- no external linter anywhere. A source-scanning test for
property 1 costs nothing and would pass today, because `internal/workspace`
names the agent constants only in two comments. Property 2 needs type design
first: a closed exported enumerable capability set plus an exported `agent.All()`
(today `known` is unexported and every "for each agent" test hand-lists the two
constants). (`lead-structural-test-precedent`)

**niwa has a precise, citable rename precedent.** `[content]` -> `[claude.content]`,
commit `81aae0b`, recorded in `DESIGN-claude-key-consolidation.md`: accept both
keys, warn on the deprecated one, hard-error if both are set, remove at the v1.0
line, and close override-position leakage with a type split rather than runtime
checks. No JSON schema exists to keep in sync; only `DESIGN-workspace-config.md`,
the README, and `scaffold.go`'s generated example move with a rename.
(`lead-config-rename`)

**`internal/vault` is the house precedent for capability negotiation,** and it
is a close structural match for what this work needs: a mandatory `Provider`
interface, an *optional* `BatchResolver` interface probed by type assertion, and
a `Registry`/`Factory` pair whose lookup is fail-closed with no silent default.
Implementations live in sub-packages (`internal/vault/infisical`,
`internal/vault/fake`) that self-register via `init()`. (`lead-go-pattern-precedent`)

**The skills defect is narrower than feared and has a ready fix.** On `main`,
niwa never resolves plugin content out of a Claude-owned directory -- it writes
`enabledPlugins`/`extraKnownMarketplaces` into `.claude/settings.json` and lets
Claude Code's own startup consume them. The dependency was introduced entirely by
the prior attempt's `codex_payload.go`, where `codexMarketplaceRoots` points the
Codex skills symlink at `~/.claude/plugins/marketplaces/<name>/` for
github-sourced marketplaces only. niwa already owns `github.FetchTarball` +
`ExtractSubpath` (used for ordinary cloning in `snapshotwriter.go`), so fetching
into a niwa-owned directory closes it without depending on Claude Code.
(`lead-skill-resolution`)

**The spike gives a concrete unavailable-list for Codex.** Named/typed subagents
never surface (plugin `agents/` directories are copied and ignored). Hooks have
no demonstrated route that avoids both solving the trust hash and presenting a
blocking modal. Marketplace/plugin *registration* cannot come from the project
layer. Project-root-marker configuration cannot be set from the project layer.
And several capabilities -- budget override, MCP servers, environment variables --
are *conditionally* available: they work only once the developer's own config
carries a trust entry, which a workspace cannot self-grant.
(`lead-spike-constraints`)

### Tensions

**Conditional availability is a third state the mandate's binary does not have.**
Property 2 says every capability is either implemented or explicitly declared
unavailable with a reason. The spike produces a real third case: MCP servers and
environment delivery are implemented *and* inert until the developer trusts the
directory. Declaring them "implemented" overstates; declaring them "unavailable"
understates and would put them wrongly on the user guide's gap list. The contract
has to model this honestly or the guide will lie in one direction or the other.

**Property 4 pulls against the dependency graph.** `internal/workspace` imports
every leaf package and is imported by `internal/plugin` and `internal/cli`; it
has never been split. The precedent layout for a new concern is a fresh
`internal/` package at or below `internal/agent`'s leaf level. But capability
*delivery* needs the workspace's writing helpers, which sit above that line. A
leaf package can hold declarations and a delivery *plan*; it cannot hold code
that performs workspace writes. Whether the contract is "return a plan of writes
the workspace executes" or "an interface implemented inside `internal/workspace`
with per-agent files" is the load-bearing design decision, and only the first
gives property 4 a package boundary rather than a filename convention.

**`claude.enabled` is correctly named today and misnamed the moment PR 2 lands.**
It currently gates only genuinely Claude-owned artifacts, so renaming it in PR 1
would be a rename with no behavioral motive. But PR 2 makes it the mandate's
exact cautionary example. The rename therefore belongs to whichever PR first
gives it a second agent to mis-gate -- which argues for PR 2 -- while PR 1 is the
one that is supposed to carry structure without behavior change.

**PR 1's scope is genuinely ambiguous.** The brief says to scope the refactor to
paths dual-agent capability actually touches. The inventory says that is most of
the pipeline, because Codex has a defensible answer for hooks (declared
unavailable), settings (approval/sandbox), env (`shell_environment_policy.set`),
MCP, and skills. Bringing all of that under the contract in a no-behavior-change
PR is a large diff; bringing only context files under it reproduces the prior
attempt's mistake at smaller scale, because the contract would again govern two
capabilities out of twenty.

### Gaps

- No mechanism yet for proving "PR 1 changes no behavior" beyond the existing
  suite passing. There is no golden-file or snapshot precedent in the repo, and
  the functional suite is black-box over the built binary.
- The blast radius of renaming the `[claude]` table is unmeasured. `override.go`
  alone carries `MergeOverrides`, `MergeInstanceOverrides`, `MergeGlobalOverride`,
  `MergeWorkspaceOverlay`, `deepCopyRepoOverride`, and `copyClaudeConfigFull`,
  all of which touch `ClaudeConfig`.
- The exact per-capability Codex answer (implemented / conditional / unavailable,
  with a reason) has not been assembled. The spike constrains it and the
  inventory enumerates the capabilities, but nobody has joined the two.
- The MCP and `shell_environment_policy.set` additions are named as high value
  but their concrete config shape, and how a distributed `.mcp.json` maps into
  `[mcp_servers.*]`, is unspecified.

### Open questions

1. Can the agent contract be expressed as a pure declarative plan that
   `internal/workspace` executes, so per-agent implementations live in leaf
   packages and property 4 gets a real package boundary?
2. What is the honest state model for capabilities -- two states, or three with
   conditional/trust-gated?
3. What proves PR 1 changed no behavior, mechanically?
4. How large is the `[claude]` rename, and does it belong to PR 1 or PR 2?
5. What exactly do the MCP and environment additions look like in the payload
   config, and where does the `.mcp.json` niwa already distributes fit?

### Orchestrator note: the rename precedent argues against its own premise

`docs/designs/current/DESIGN-claude-key-consolidation.md` is worth reading in
full before the design phase, because it is the precedent for the alias
mechanism *and* a direct challenge to the rename this work proposes. That design
moved `[content]` **into** `[claude]` on the stated grounds that content is
"100% Claude-coupled -- every consumer writes to literal `CLAUDE.md` /
`CLAUDE.local.md` destinations", citing the same three `content.go` functions
this exploration is now looking at. It was correct when written.

Dual-agent capability is exactly the thing that falsifies its premise. Content
is the *first* capability to become agent-neutral, which means part of this work
partially reverses a deliberate, documented decision from months earlier. The
design must say so plainly and cite that doc, rather than renaming quietly and
leaving a reader to discover the contradiction. It also inherits two useful
details from it: the deprecation rides the existing `Parse` warnings machinery
(two fields with different TOML tags, a post-parse check, one warning, one
conflict error), and the `ClaudeConfig`/`ClaudeOverride` type split exists so the
decoder rejects override-position `content` at parse time -- a pattern any new
table has to preserve. It also left `workspace.content_dir` deliberately
unrenamed, which is a cheap second test case for whatever alias mechanism this
work builds.

### Orchestrator note: what the prior guide's gap section actually was

The brief faults the prior attempt's user guide for spreading its gaps across a
design's negative-space section and a scope note. Reading it makes the fault
sharper than that. The guide does have a `## What is deliberately not written`
section, but it answers a different question: it lists what niwa refuses to put
into *the developer's own Codex configuration* -- no hooks, no API key, no
global keys, no credentials. That is a safety-and-scope list, and a good one.

It is not a capability-gap list. Nothing in it tells a developer that MCP
servers do not reach their Codex session, that named subagents never surface,
that skills for a GitHub-sourced marketplace need Claude Code installed, or that
work-summary hooks, PR-body hooks, worktree-hook delegation, and
ephemeral-session provisioning do not apply. The section closest to admitting a
gap opens with "Two adjacent limits, so they don't read as gaps" -- which is the
honesty problem stated in the document's own words.

The fix is structural, not editorial: the guide's gap list has to be generated
from the declared-unavailable capabilities, so a capability that gains a reason
in code cannot fail to appear in the doc. Keeping the safety list as a separate
section is right; it should just stop being mistaken for the gap list.

### The acceptance bar, verbatim

The 15 scenarios in `test/functional/features/codex-agent.feature` on the closed
branch, so the PRD can treat them as requirements rather than re-derive them.
Thirteen are `@critical` and offline; two are `@codex-live` and skip when the
`codex` binary is absent.

1. A codex-default workspace still materializes the whole Claude tree.
2. `niwa dispatch` refuses in a codex-default workspace.
3. A claude-default workspace materializes both agents' context too.
4. A prepared instance serves a Codex session from the instance root down.
5. A workspace with nothing to say leaves the repository's own context in place.
6. The declared budget covers a context chain past Codex's default.
7. The workspace's skills reach Codex whole and namespaced.
8. Trust entries are canonical, one per repository, and additive.
9. An unreadable credential file fails neither create nor apply.
10. A worktree carries the workspace context and its own framing.
11. Re-applying three times adds nothing.
12. Changed content replaces the previous content everywhere.
13. Committed content at niwa's names degrades loudly and is never overwritten.
14. (`@codex-live`) A live Codex session writes a file on its first attempt.
15. (`@codex-live`) A live interactive Codex session starts clean from the root
    and from a nested directory.

Two observations for the design. Scenario 2 asserts a *limitation* as a passing
test -- dispatch refusing under Codex. Under a capability contract that becomes a
declared-unavailable capability with a reason, and the scenario should assert the
declaration rather than the bare refusal, so the test and the guide's gap list
cannot drift apart. And scenarios 1 and 3 together are the no-behavior-change
proof restated behaviorally: they assert the Claude tree is complete regardless
of which agent the workspace names, which is exactly the invariant the first PR
must hold without any Codex code present.

## Decision: Explore further

Round 1 established the terrain and produced four decision-relevant tensions,
none of which is answerable by design judgment alone -- each needs measurement.
Round 2 targets exactly those.
