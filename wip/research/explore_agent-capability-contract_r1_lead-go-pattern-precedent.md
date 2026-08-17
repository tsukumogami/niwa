# Lead: What in-repo Go patterns exist for interfaces with multiple implementations, registries, and capability negotiation — and what does niwa's package layout convention look like?

## A. Interfaces With Multiple Implementations

**`internal/vault` — Provider/Factory/Registry, the closest existing analog to an "agent contract."**
`internal/vault/provider.go:36` declares `Provider` (4 methods: `Name`, `Kind`, `Resolve`, `Close`), and `internal/vault/provider.go:70` declares an *optional* extension interface, `BatchResolver`, detected by runtime type assertion rather than declared on `Provider` itself:

```go
// BatchResolver is an optional Provider extension for backends that
// can resolve many refs in a single RPC. The resolver stage tests for
// this interface via runtime type assertion and prefers it when
// available.
type BatchResolver interface {
    ResolveBatch(ctx context.Context, refs []Ref) ([]BatchResult, error)
}
```
This is the repo's one real precedent for "capability negotiation": a base interface every implementation must satisfy, plus a marker-style optional interface the caller probes for (`if br, ok := p.(BatchResolver); ok { ... }`), rather than a boolean "supports X" flag.

Implementations live in **separate sub-packages**, not files in the `vault` package itself: `internal/vault/infisical/` (real backend) and `internal/vault/fake/` (test double), each importing `internal/vault` and registering a `Factory` via `init()`. `internal/vault/registry.go:1-33` documents the wiring:

```go
// Registry indexes Factory instances by Kind. Backends register
// their Factory with a Registry (typically DefaultRegistry) via
// init(); the resolver stage then calls Build with the set of
// ProviderSpecs parsed from config to obtain a ready-to-use Bundle.
...
var DefaultRegistry = NewRegistry()
```
Selection is by string `Kind` looked up in a map (`r.factories[spec.Kind]`, `registry.go:97`), not a switch on a closed Go type — the set of backends is open-ended (new vault kinds can register without editing `vault` itself). The fake backend **deliberately does not self-register** with `DefaultRegistry` (`provider.go:19-21`, `registry.go:24-26`): tests construct `NewRegistry()` and `Register` it explicitly, so production code paths never see the fake by accident. `Register` and `Build` are documented as a completeness/consistency contract, not merely storage: duplicate `Kind` is a hard error, `Build` fails closed if no factory matches a configured kind, and it closes any providers already opened on partial failure (`registry.go:88-114`).

**`internal/github` — interface + one production implementation + no committed fake.**
`internal/github/client.go:22-25`:
```go
// Client is the interface for querying GitHub repos.
type Client interface {
    ListRepos(ctx context.Context, org string) ([]Repo, error)
}
```
followed immediately by the single concrete `APIClient`. There is no second implementation and no fake type in the package (checked `client_test.go`, `fetch_test.go`, `pulls_test.go` — tests use `httptest.Server`, not an interface fake). This is the "interface with one implementation, sized for a future second one" case, not a capability-negotiation case.

**`internal/worktree` — narrow single-method seam interface.**
`internal/worktree/worktree.go:27`: `GitInvoker` — a narrow interface (looks like a single `Invoke`-style method) used to substitute git execution in tests. This is the repo's "interface exists purely to let a test fake stand in for a subprocess-calling implementation" pattern, same shape as the CLI-local interfaces below.

**`internal/cli` — narrow, unexported, call-site-local interfaces for test seams.**
`internal/cli/source_inspect.go:67: type sourceInspectFetcher interface { ... }` and `internal/cli/watch.go:617: type prFreshnessClient interface { ... }`. Both are lowercase (package-private), declared next to the one function that uses them, sized to exactly the methods that call site needs (not the full `github.Client`). This is niwa's idiom for "I need to fake one dependency in one file's tests" — a tiny local interface, not a shared package-level abstraction.

**`internal/plugin` and `internal/cli/plugin_adapter.go` — function-field seam instead of an interface at all.**
`internal/workspace/apply.go:102-108` (`Applier.InstallNiwaPlugin`) and `apply.go:110-120` (`Applier.PrewarmDeclaredPlugins`) are **plain function-typed struct fields**, not interface methods:
```go
// InstallNiwaPlugin is the test seam for the niwa plugin
// auto-installer. Production wires this to plugin.Install via
// NewApplier; tests override to capture install-or-skip behavior
// without writing to the user's home directory. When nil, the
// installer is a no-op ...
InstallNiwaPlugin func(state *InstanceState, reporter *Reporter, skipInstall bool)
```
`internal/cli/plugin_adapter.go:9-16` supplies the production closure and states the reason directly: this is how niwa breaks an import cycle without inventing an interface:
```go
// installNiwaPluginAdapter is the cli-side adapter that bridges
// workspace.Applier.InstallNiwaPlugin to plugin.Install. It lives in
// the cli package to break the workspace→plugin→workspace import
// cycle: workspace declares the seam as a function field, cli
// supplies the implementation at construction time.
```
This is a second, distinct "multiple implementation" pattern in the repo: production closure vs. nil/test closure, selected by whoever constructs the `Applier` (`cli` for real runs, tests directly). No interface type appears anywhere in this seam.

**`internal/tui` and `internal/promptcapture`** — no interfaces; each is a single concrete implementation, explicitly noted as a byte-for-byte copy from a sibling repo (tui) or a niwa-only leaf (promptcapture). Not relevant to multi-implementation patterns, but relevant to package-boundary rationale (see C).

**Summary of the two live "multiple implementation" idioms:**
1. **Registry + Factory + sub-packages-per-implementation**, for an open/pluggable set (`internal/vault`). Optional capability = separate interface, tested via type assertion.
2. **Function-field seam on a struct**, for a closed production-vs-test-double pair where an interface would create an import cycle (`workspace.Applier.InstallNiwaPlugin`).
Neither is a `map[Agent]Implementation` keyed switch — that shape (`internal/cli/dispatch_model.go`, see B) is used for *data* variation per agent, not for swapping an implementation.

## B. Registries and Enumerable Sets

**`internal/agent/agent.go:31`** — the lead's own reference case:
```go
// known lists the accepted agent values for error messages. It is kept in sync
// with the constants above.
var known = []Agent{AgentClaude, AgentCodex}
```
No compiler-enforced completeness check — the comment is the only guarantee `known` tracks the constants. `ParseAgent` (`agent.go:36-45`) is a `switch` over the same two constants, independently duplicating the closed set; nothing ties `known`, the `switch`, and the constant block together except code review discipline.

**`internal/cli/dispatch_model.go:23-53`** — two maps keyed by `agent.Agent`, each covering exactly `{AgentClaude, AgentCodex}`:
```go
var modelCategoriesByAgent = map[agent.Agent]map[string]string{
    agent.AgentClaude: {...},
    agent.AgentCodex:  {...},
}
var knownModelNamesByAgent = map[agent.Agent]map[string]bool{
    agent.AgentClaude: {...},
    agent.AgentCodex:  {...},
}
```
Both have graceful-fallback accessors (`modelCategoriesFor`, `knownModelNamesFor`, lines 55-71) that default to the Claude entry on cache miss — i.e., an agent value missing from the map degrades silently to Claude behavior rather than failing loudly. This is the exact shape the lead's core question is worried about: **this file is a live example of per-agent data living in a map at a call site, not behind a method on `agent.Agent`**, in contrast to `RootContextFileName`/`WritesRepoLevelContext`, which *are* methods on `Agent` consulted by `internal/workspace` call sites (`content.go:44`, `content.go:130`, `root_materializer.go:375`, `worktree_content.go:740`) with no raw `agent.AgentCodex ==` comparison in `internal/workspace` itself. So the repo already has both the good pattern (method-on-Agent, call site agent-blind) and the pattern-to-avoid (map/switch scattered at a call site) — `dispatch_model.go` and `internal/cli/dispatch.go:236` (`if resolvedAgent != agent.AgentClaude`) are existing instances of the latter, not hypothetical risks.

**`internal/vault` registry** (see A) is the one enumerable set with an actual completeness/consistency check: duplicate-`Kind` registration is a hard error, and `Build` fails closed on an unregistered kind rather than silently defaulting. This is the strongest in-repo precedent for "every capability is implemented or explicitly declared unavailable with a reason" — the failure mode is a returned `error`, not a fallback.

**`internal/keyreport`** rank constants (`internal/keyreport/render.go:13-18`, `rankNoSource = iota …`) are a closed, ordered enumeration used for sorting, with a comment stating what determines rendering order — same "list plus comment, no compiler check" pattern as `agent.known`.

No `go vet`/`staticcheck`-style exhaustiveness lint config was found gating these switches (not investigated exhaustively — worth a targeted check if the design wants to lean on it).

## C. Package Layout Convention

**The mandated property — "generic/specific boundary legible from layout, not just function bodies" — has one strong precedent and one strong counter-precedent, both worth citing directly:**

- **Strong precedent (extraction into a leaf sub-package):** `internal/gitexclude`, `internal/envformat`, `internal/keyreport`, `internal/promptcapture` are each single-purpose packages carved out specifically so `internal/workspace` (and in gitexclude's case, also a worktree-create path) can depend on them **without creating an import cycle**. Every one of their package docs states this explicitly as the reason for the package's existence, not merely as an implementation note:
  - `internal/gitexclude/exclude.go:1-6`: "It is a leaf package (stdlib only) so both internal/workspace (the apply path) and internal/mcp (the worktree-create path) can use it without an import cycle."
  - `internal/envformat/envformat.go:1-4`: "It is a leaf package (stdlib only) so the workspace materializer can use it without an import cycle, mirroring internal/gitexclude."
  - `internal/keyreport/keyreport.go:1-19`: explicitly *not* stdlib-only (imports `internal/config` for shared enum types) but still a leaf relative to `internal/workspace` and `internal/vault`, and the doc explains *why* it copies nothing from envformat/gitexclude's stdlib-only leaf shape — it shares config's cause/level vocabulary instead of duplicating it, because "copying it here would leave two enums free to drift."
  - `internal/promptcapture/promptcapture.go:1-13`: separated from `internal/tui` specifically because tui is a byte-equivalent copy of a sibling repo's file and promptcapture is niwa-only — i.e., package boundaries here track *provenance/ownership*, not just dependency direction.

  The pattern: **a package earns its own directory when (a) more than one higher-level package needs it and sharing it as one of their files would create a cycle, or (b) it has a different maintenance/provenance contract than its neighbor** (niwa-only vs. byte-copied). It is not used merely to group "a concern" for tidiness — each doc comment states a specific caller and a specific cycle it avoids.

- **Counter-precedent (no split, by design):** `internal/workspace` is a single large package (~90 files, both `.go` and `_test.go`, covering apply/materialize/bootstrap/clone/credentials/worktree/env-example/etc.) with **no sub-packages** under it except `internal/workspace/rootskills` (a data/asset directory, not investigated in depth here — worth a follow-up read of its doc comment). Everything from `apply.go` to `worktree_content.go` to `credentialpool.go` lives in one package. This is a deliberate choice, not an oversight: the repeated "X is a leaf package... so internal/workspace can use it without an import cycle" framing in every sibling leaf package's doc comment treats `internal/workspace` as the fixed, monolithic consumer that the ecosystem is built around — the split happens on the *dependency* side, not by carving `workspace` itself into pieces. No git-log evidence of `internal/workspace` ever having been split apart (see D) — files have been added to it steadily, not extracted from it.

**Package doc comment style — two representative examples, both worth quoting as house style:**

`internal/agent/agent.go:1-14` (already the lead's own touchstone):
```go
// Package agent defines the AI coding agent niwa prepares a workspace for.
//
// The Agent discriminator is a session-global choice (one agent for a whole
// workspace preparation), resolved once per session from a workspace-config
// default plus a per-session flag/environment override. It is deliberately a
// leaf package -- it imports nothing else in the module -- so both
// internal/config (which carries the raw default as a string) and the
// higher-level internal/workspace and internal/cli packages can depend on it
// without an import cycle.
```

`internal/vault/provider.go:1-21` (already quoted in full in A) — states the interface skeleton, the optional-capability mechanism, the registry/factory split, and where implementations physically live, all in the package doc rather than scattered across files.

Common shape across every good example found (`agent`, `gitexclude`, `envformat`, `keyreport`, `promptcapture`, `vault`): **first sentence states what the package does in one line; the rest of the doc states *why the package boundary is where it is* — naming the specific caller(s) and the specific cycle avoided — not a general description of contents.** A new package's doc comment should be held to that same bar: name the workspace-preparation call site it serves and the cycle it avoids, not just "this package holds agent capability logic."

**Per-implementation files vs. per-implementation packages — direct precedent for both, cleanly split by whether the set is closed or open:**
- **Closed, small set → files/methods in one package.** `internal/agent/agent.go` handles both Claude and Codex as branches inside single methods (`RootContextFileName`, `WritesRepoLevelContext`) in one file, not `agent_claude.go`/`agent_codex.go`. There is no precedent anywhere in the repo for a `foo_claude.go` / `foo_codex.go` file split — searched `internal/cli` and `internal/workspace` for such a pattern and found none; per-agent variation is always either a method-with-branches (agent package) or a map keyed by `agent.Agent` (`dispatch_model.go`).
- **Open, pluggable set → per-implementation sub-packages + registry.** `internal/vault/{infisical,fake}` is the only precedent for one-package-per-implementation, and it exists because vault backends are meant to be added without touching the `vault` package itself (Factory self-registers via `init()`). Since niwa's agent set is closed and enumerated in one place already (`agent.known`), the `internal/vault` sub-package-per-backend shape is probably **overkill** for a 2-agent (soon maybe 3-agent) contract — the `internal/agent` single-package, method-per-capability shape is the closer precedent for what's actually being asked for.

## D. Dependency Direction and Cycle Constraints

Direct grep of every non-test `tsukumogami/niwa/internal/*` import inside `internal/workspace/*.go` gives this complete import set:
```
internal/agent, internal/config, internal/envformat, internal/gitexclude,
internal/github, internal/guardrail, internal/keyreport, internal/pluginrecord,
internal/secret, internal/source, internal/testfault, internal/vault, internal/worktree
```
Notably **absent**: `internal/plugin`, `internal/cli`, `internal/tui`. `internal/vault` itself only imports `internal/secret` (confirmed by grepping `internal/vault/*.go` and its two sub-packages) — it does not import `internal/workspace`, `internal/config`, or `internal/agent`, keeping it a genuinely low-level leaf despite being a "big" package with a registry.

What imports `internal/workspace`: `internal/cli/*.go` (extensively) and exactly one non-cli file, `internal/plugin/installer.go`. That one edge is the whole story behind the function-field seam in section A: `internal/plugin` → `internal/workspace` exists, so `internal/workspace` → `internal/plugin` would be a cycle, which is why `Applier.InstallNiwaPlugin` is a function field wired from `internal/cli` (which can safely import both) instead of `internal/workspace` importing `internal/plugin` directly.

**Concrete constraint for a new agent-capability package:**
- If it needs to be *read* from `internal/workspace` materializer call sites (the stated goal), it must not import `internal/workspace`, `internal/cli`, or `internal/plugin` — i.e., it must sit at or below the same level as `internal/agent`, `internal/vault`, `internal/keyreport` etc.
- `internal/config` is safe to depend on (workspace already does, and so does `keyreport`), so a new package modeled on `keyreport`'s "not stdlib-only, but still a leaf relative to workspace" shape can freely share types with `internal/config` (e.g., if agent-capability values need to be described in workspace config).
- **`internal/workspace/agentplan` (a sub-package of workspace) is riskier than a standalone `internal/agentcap`:** nothing here shows workspace importing its own sub-packages today (`rootskills` wasn't confirmed as Go code — likely data), so there's no clean precedent either way; a fresh top-level `internal/` package mirroring `internal/agent`/`internal/vault` is the shape with actual precedent behind it, and it avoids any ambiguity about whether `internal/workspace` importing `internal/workspace/agentplan` is idiomatic here.
- A sibling package that itself needs to be read by `internal/agent` (making `agent` non-leaf) would break the explicit "deliberately a leaf" contract stated in `agent.go:6-9` and used by `config`, `workspace`, and `cli` to justify their own dependency on `agent` — that direction (agent depending on the new package) should be avoided; the new package should depend on `agent`, not the reverse.

## Implications

- The repo already has a working precedent for "optional capability, declared and probed" — `vault.BatchResolver` via type assertion — that is more idiomatic here than adding a boolean method to every implementation. A capability contract for agents could follow the same shape: a small mandatory interface/method set on `agent.Agent` (or a new leaf type), plus optional per-capability interfaces implementations opt into, tested via assertion where "not implemented" is a legitimate, explicit state rather than a zero value.
- The repo also already demonstrates the failure mode the lead is trying to prevent: `internal/cli/dispatch_model.go`'s two `map[agent.Agent]...` globals and the `if resolvedAgent != agent.AgentClaude` check in `dispatch.go:236` are exactly "agent constants at call sites" living outside the `agent` package. Any new contract should either absorb this file's logic into methods on `agent.Agent` (matching the `RootContextFileName` precedent) or explicitly justify why model-name mapping is different in kind from context-file naming.
- `internal/vault`'s registry gives a completeness-by-construction technique worth reusing: duplicate registration is a hard error, and unresolvable capability lookups fail closed with a named error rather than silently defaulting. `internal/agent`'s `known` slice, by contrast, has no such enforcement — it is comment-only. A design that wants "every capability is implemented or explicitly declared unavailable with a reason" as a *checkable* property should look more like `vault.Registry.Build`'s fail-closed behavior than `agent.known`'s honor-system list.
- For package layout, the strongest-fit precedent is a new top-level `internal/` leaf package (sibling to `agent`, structured like `agent` for a closed 2-3 value set, or like `vault` if the capability set is meant to be open-ended and pluggable) — not a `internal/workspace` sub-package (no precedent either way, and workspace has never been split) and not per-agent files inside an existing package (no precedent for that shape anywhere in the repo).

## Surprises

- `internal/workspace` has apparently never been split, despite being by far the largest package in the module (~90 files). Every other package in the repo that touches workspace is small and single-purpose specifically *because* workspace itself stays monolithic — the "split" pressure in this codebase is absorbed entirely by peripheral leaf packages, not by decomposing the hub.
- `dispatch_model.go`'s per-agent maps are a pre-existing, already-shipped instance of the exact anti-pattern ("agent constants at call sites") the new contract is meant to prevent. This is worth flagging directly rather than treating PR #248 as the only failure mode to guard against — the anti-pattern already lives in production code outside the failed PR.
- The `vault` fake backend's deliberate non-self-registration with `DefaultRegistry` (so production code paths structurally cannot see it) is a subtle but reusable idea for an agent contract too, if a "fake/no-op agent" is ever needed for tests: register it into a test-local registry, never into whatever `DefaultRegistry`-equivalent production code consults.

## Open Questions

- Is `internal/workspace/rootskills` Go code or a data/asset directory? If it's Go, it would be the one precedent for a workspace sub-package and should be re-examined before ruling that shape out.
- Does the repo have any `exhaustive`-style lint (staticcheck/exhaustive linter) wired into CI that would catch a missing `case` in switches like `ParseAgent` or `agentBinaryName`? Not confirmed either way; if absent, that's a gap worth naming explicitly since the design leans on "every capability is implemented or explicitly declared" as a checkable property.
- Are there other `map[agent.Agent]...` globals beyond `dispatch_model.go` and `instance_from_hook.go:499` / `internal/cli/agent.go:16`? The grep in this pass covered non-test files repo-wide for `agent.Agent`/`AgentClaude`/`AgentCodex`/the three `agent.Agent` methods, and `dispatch_model.go` was the only per-agent-map instance found, but a second pass focused purely on `internal/cli/*.go` beyond dispatch would firm this up.

## Summary
niwa's one real capability-negotiation precedent is `internal/vault`: a mandatory `Provider` interface, an optional `BatchResolver` interface probed via type assertion, and a `Registry`/`Factory` pair with fail-closed, no-silent-default lookup — implementations live in separate sub-packages (`internal/vault/infisical`, `internal/vault/fake`) that self-register via `init()`. `internal/agent` is the house style for a small closed set (methods with internal branches, one `known` slice with no compiler-enforced completeness), and it is already violated in production by `internal/cli/dispatch_model.go`'s per-agent maps and `dispatch.go:236`'s raw `agent.AgentClaude` comparison — the exact "constants at call sites" anti-pattern the new contract must avoid. `internal/workspace` has never been split and imports every leaf package (agent, vault, keyreport, envformat, gitexclude, etc.) but never `internal/plugin` or `internal/cli`, which do import `internal/workspace` — so any new capability package must sit at or below `internal/agent`'s level, and a fresh top-level `internal/` package matching that leaf shape is the layout with actual precedent, not a `internal/workspace` sub-package.
