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

## Decision: Explore further (round 1 -> round 2)

Round 1 established the terrain and produced four decision-relevant tensions,
none of which is answerable by design judgment alone -- each needs measurement.
Round 2 targets exactly those.

---

## Round 2

Five leads dispatched, five returned. All four round-1 tensions are resolved,
and one produced a finding nobody was looking for.

### The four tensions, resolved

**1. The third state dissolves; there is no "conditional".** Round 1 read the
spike as saying MCP, environment, and the byte budget are inert until the
developer trusts the directory, which a workspace cannot self-grant. That is
true of a *project config layer* and false of niwa. The spike scoped itself to
what a project layer can carry without touching the developer's own Codex
configuration -- and niwa is not a project layer, it is a tool the developer
runs. The prior attempt already built the thing the spike says a project layer
cannot do: `codex_trust.go` performs a lock-serialized, atomic, additive edit of
`~/.codex/config.toml`, retracting only keys niwa itself wrote, with four
acceptance scenarios pinning it.

So trust is a capability niwa *delivers*, not a precondition it waits on. Name it
`CapabilityDirectoryTrust` and the conditional cases become plainly implemented
with a dependency edge: `Requires: [CapabilityDirectoryTrust]`. A closure test --
every capability named in `Requires` is itself implemented for the same agent --
makes it honest by construction: declare MCP implemented while trust is
unavailable and the test fails. A soft "conditional" state could never catch
that; it would absorb it. Two states, a required reason kind on the unavailable
side, and a `Requires` edge. (`r2-support-matrix`)

**2. The package boundary is real, via a plan.** A new leaf package
(`internal/agentplan`, sibling to `internal/agent`) holds the capability
enumeration, the support matrix, the plan types, and the per-agent plan
producers. `internal/workspace` gains one generic executor implementing a closed
set of four operations, containing no agent name and no context filename. The
boundary is stated as **reads inputs, declares outputs** rather than "pure",
because the producers do read niwa-owned inputs and, for the inline case, a
guarded read of a repository's committed file. That line is mechanically
checkable: an AST assertion that the leaf never calls `os.WriteFile`,
`os.MkdirAll`, `os.Symlink`, `os.Remove`, or `exec.Command`.
(`r2-plan-shaped-contract`)

**3. PR 1 ships zero config renames.** Only two of nine `ClaudeConfig` fields
warrant an agent-neutral name. `Content`/`content_dir` is a single-field,
single-embedding-site change structurally identical to the proven `[content]`
precedent. `Enabled` is not a rename at all -- relabelling it reproduces the same
mis-gating under a new spelling, and the real fix is restructuring what the gate
governs, which is a PR 2 decision that cannot be made until PR 2 defines Codex's
delivery plan. Both belong to PR 2. The other six fields are correctly
Claude-named and stay. A compatibility alias is behavior-preserving at parse time
but not diff-free -- it adds warning text, changes the README, the config design
doc, and `scaffold.go`'s generated example -- which is real user-facing surface
for a PR whose whole job is to be invisible. (`r2-rename-blast-radius`)

**4. PR 1's no-behavior-change claim gets a mechanism.** `InstanceState.ManagedFiles`
(`apply.go:1695-1718`) already records `Path` + `ContentHash` for every written
file; the code hashes every path in `writtenFiles`, and content files and the
workspace `CLAUDE.md` are included (they simply carry no source tuples). Commit
a characterization test on `main` **first** -- fixture workspaces, run `Create`,
assert sorted path-and-hash pairs against a checked-in list -- so it characterizes
current behavior rather than being written to match the refactor. Then do the
refactor as a visibly mechanical diff gated by it. Two nondeterminism sources
need normalizing: the `{workspace}` absolute-path template variable and
`os.Executable()` in worktree-delegation hook commands. (`r2-no-behavior-change-proof`,
verified independently against `apply.go`)

### The finding nobody was looking for

**A latent secret leak in the code the brief invites us to lift.** The prior
attempt writes its payload config at `0o644` rather than the `secretFileMode`
(0600) every other secret-bearing writer in the repo uses, and the instance root
has no git-exclude coverage for files not named `.local`. This is harmless today
because that file carries only a byte budget. It becomes a real leak the moment
environment delivery puts resolved secrets there -- which is precisely what the
brief names as the highest-value gap to close. Both fixes are mechanical and must
land in the same change that first writes secret material to that file, not
after. (`r2-mcp-env-shape`)

### The test that is meaningful on the day it lands

This is the direct answer to the brief's central complaint -- that the prior
attempt's replacement structure was never something a test could fail on.

The property-1 AST scan has two halves. The agent-constants half **passes today**:
the only three occurrences in `internal/workspace` are comments
(`root_materializer.go:95`, `apply.go:46`, `worktree_content.go:440`). The
context-filename-literals half **fails today**, at `content.go:156`, `:186`,
`:208`, `worktree_content.go:743`, `workspace_context.go:196`, `:229`, `:411`,
and the dead `rootClaudeFile` const at `root_materializer.go:51`. A test that is
red before the work and green after is a deliverable; a test that is vacuously
green either way is the thing that let two hardcoded passes through review.

The plan model additionally makes property 2 testable *without a filesystem*:
`Plan(AgentCodex, ...)` returning any `CLAUDE.*` entry is a one-line assertion
failure, where today the equivalent check needs a tmpdir and a full apply. And
the wiring test closes the dead-plumbing hole directly -- if bytes can only reach
disk through a plan, and plans only come from a function taking the agent, then
the agent parameter is load-bearing by construction rather than by hope.

### Tensions

**One inconsistency between the two opus leads, resolved here in favor of the
matrix.** `r2-plan-shaped-contract`'s sketch of the property-2 test says
"Conditional/Unavailable with a non-empty reason", carrying forward round 1's
three-state framing. `r2-support-matrix` argues at length for two states plus
`Requires` edges and shows the third state cannot be caught by a test. The
matrix's argument is the stronger one and the design should follow it; the plan
lead's test sketch needs the state vocabulary swapped, which changes nothing
structural about it.

**The brief's MCP claim is only half-backed.** It states `[mcp_servers.*]` in the
project layer "works, verified live". The spike confirms such a file parses and
loads once trusted, but never pins down Codex's `mcp_servers` field schema or
`shell_environment_policy.set` semantics. That is the difference between "the
file is read" and "we know what to write in it". Measurable rather than
guessable, since `codex-cli 0.147.0` -- the exact build the spike measured -- is
installed here.

**Where "no behavior change" is hardest to defend.** The eight context writers
carry accumulated boundary rules (overlay append, subdir content, the `@import`
migration removals) that a mechanical conversion can silently drop. Two of them,
`InstallRepoContentTo` and `installWorktreeContextLayer`, are already half-broken
on `main`. That is exactly where the characterization test has to be pointed.

### Gaps

Five rows of the 24-row matrix are flagged unresolved. Three are measurable with
the installed binary and have been added to the running spike: whether
`approval_policy`/`sandbox_mode` are settable from the project layer (the
matrix's only hard unresolved row), whether a linked worktree's `.git` *file*
satisfies the project-root marker (if not, the worktree-context row flips from
implemented to unavailable and an acceptance scenario becomes unsatisfiable), and
whether `shell_environment_policy.set` is trust-gated (asserted by analogy today,
never measured).

## Decision: pending the round-3 spike

Round 1 established the terrain and produced four decision-relevant tensions,
none of which is answerable by design judgment alone -- each needs measurement.
Round 2 targets exactly those.
