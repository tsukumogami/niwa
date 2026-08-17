# Lead: How does this repo enforce structural properties in tests today, and what would it cost to build a test that fails when the structure regresses?

## A. Existing Structural-Test Precedent

**No `go/ast`/`go/parser`/`go/token`/`go/types`/`golang.org/x/tools/go/packages` test exists anywhere in the repo.** A grep across all `*_test.go` for those imports returns nothing. There is no source-scanning test precedent at all today — properties like "no agent constants at this call site" are currently enforced by nothing but code review and a design doc.

**No golden-file `-update` flag pattern exists.** A search for `-update` flags or golden-file conventions across `*_test.go` turned up nothing. Tests that compare written output against expectations (e.g. `internal/workspace/materialize_test.go`) inline the expected bytes/strings rather than reading from `testdata/*.golden` files with a regeneration flag.

**File-reading tests are pervasive but behavioral, not structural.** Hundreds of tests read files with `os.ReadFile` (`internal/workspace/materialize_test.go`, `internal/workspace/apply_test.go`, `internal/cli/shell_init_test.go`, etc.) but every one reads *materialized output* (a written `CLAUDE.md`, `settings.json`, `workspace.toml`) to assert on runtime behavior of the applier, not the shape of the `.go` source itself. `internal/cli/allow_missing_secrets_test.go:297` and `:388` use `filepath.WalkDir` — again over a materialized directory tree (a fake workspace), not over the repo's own source files. `internal/workspace/apply_test.go:2712` does the same over a directory being copied.

**Table-driven exhaustiveness precedent exists, but only over an unexported, un-enumerable local slice.** `internal/agent/agent.go:31` defines `var known = []Agent{AgentClaude, AgentCodex}`, lowercase and package-private — no other package can range over it. Every test that wants "for each agent" coverage (`internal/agent/agent_test.go:56,72,88` — `RootContextFileName`, `LocalContextFileName`, `WritesRepoLevelContext`; `internal/cli/dispatch_model_test.go:39` — `for _, ag := range []agent.Agent{agent.AgentClaude, agent.Agent("")}`) hand-writes the agent list as a test-local literal. There is no existing "iterate all agents from a single source of truth" test in the repo — each caller re-lists `{AgentClaude, AgentCodex}` (or a subset) by hand. This is exactly the gap Property 2 needs closed: today, adding a third agent would silently leave every one of these hand-written lists stale, and no test would catch it.

**No registry-completeness test** ("every X has a Y") of the kind the lead asked about exists yet — the closest analog is the per-agent maps in `internal/cli/dispatch_model.go:23-53` (`modelCategoriesByAgent`, `knownModelNamesByAgent`), but nothing asserts those two maps have matching key sets, and nothing asserts they cover all agents from a canonical source.

**No `depguard`-style or import-restriction test.** Nothing in the repo greps imports or restricts which packages may import which.

## B. Tooling Constraints

**CI runs `gofmt -l .`, `go vet ./...`, and `go test -race ./...` only** (`.github/workflows/test.yml:64-101`). No external linter (no golangci-lint, no staticcheck) appears anywhere in `.github/workflows/*.yml` or the `Makefile`. This confirms the CLAUDE.md line verbatim: the workspace's `go-development` skill convention of "go vet only, no external linters" holds — there is no linter binary installed or invoked in CI at all, just the standard toolchain (`gofmt`, `go vet`, `go test`).

**`go.mod` requires only**: `github.com/BurntSushi/toml`, `github.com/cucumber/godog`, `github.com/spf13/cobra`, `golang.org/x/sys`, `golang.org/x/term` (plus godog's transitive deps). **`golang.org/x/tools` is not a dependency today** — using `golang.org/x/tools/go/packages` would be a new dependency and would need to clear `go mod tidy` review in the tidiness CI check (`test.yml:57-63`).

Critically, **`go/ast`, `go/parser`, and `go/token` are Go standard library**, not part of `golang.org/x/tools`. A source-scanning test built on `go/parser.ParseFile` + `go/ast.Inspect` needs zero new dependencies and adds nothing for `go mod tidy` to flag. `go/types` is also stdlib, though full type-checking (as opposed to syntactic AST walking) requires either `go/types.Config.Check` (stdlib, but requires resolving the full import graph, which is heavier) or `golang.org/x/tools/go/packages` (the ergonomic wrapper, and the one that *would* be a new dependency). For a simple "does this identifier appear in this call position" check, plain `go/ast` walking over `go/parser`-parsed files is sufficient and dependency-free.

**The functional (Gherkin) suite** is driven by `godog` via `test/functional/*_test.go`, gated behind `NIWA_TEST_BINARY` (a prebuilt `niwa-test` binary) and tag filters via `NIWA_TEST_TAGS`. `make test-functional-critical` builds the binary then runs `go test -v ./test/functional/...` with `NIWA_TEST_TAGS=@critical`, restricting execution to scenarios tagged `@critical` in the `.feature` files (29 feature files exist, e.g. `test/functional/features/codex-agent.feature`, which already covers agent-specific behavioral scenarios). This suite exercises the built binary end-to-end (black-box), so it cannot assert on source structure — it's the wrong layer for either Property 1 or Property 2, which are about the Go source and its type structure, not runtime behavior of the compiled binary.

## C. Mechanism Options for the Two Properties

### Property 1 — "no agent constants at materializer call sites"

**Mechanism: a source-scanning test using stdlib `go/parser` + `go/ast`, scoped to `internal/workspace`.**

Concretely:

```go
// internal/workspace/agentconst_scan_test.go
package workspace

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// allowedAgentConstFiles are the only files in this package permitted to name
// agent.AgentClaude or agent.AgentCodex directly. Every entry must carry a
// comment here explaining why the call site is exempt from the interface
// boundary this test enforces.
var allowedAgentConstFiles = map[string]string{
	// Comments only (informational file docs), verified by hand at review
	// time; the scan below only flags occurrences inside actual code, but
	// this file is still listed for auditability.
}

func TestNoAgentConstantsAtCallSites(t *testing.T) {
	fset := token.NewFileSet()
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range matches {
		if filepath.Base(path) == "agentconst_scan_test.go" {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok || pkgIdent.Name != "agent" {
				return true
			}
			if sel.Sel.Name == "AgentClaude" || sel.Sel.Name == "AgentCodex" {
				if reason, ok := allowedAgentConstFiles[path]; ok {
					t.Logf("allowed agent constant in %s: %s", path, reason)
					return true
				}
				pos := fset.Position(sel.Pos())
				t.Errorf("%s:%d: materializer call site names agent.%s directly; route through the agent.Agent interface instead", path, pos.Line, sel.Sel.Name)
			}
			return true
		})
	}
}
```

**False-positive risk, concretely, in this repo today:**
- `internal/workspace/root_materializer.go:95` and `internal/workspace/worktree_content.go:440` reference `agent.AgentClaude` **inside `//` comments**, not code. A naive text grep would flag these; the `go/ast` approach above walks `*ast.SelectorExpr` nodes, which only exist in actual code, so comments are structurally excluded for free — this is the concrete reason to prefer AST-walking over a plain grep-based test.
- The `agent` package's own files (`internal/agent/agent.go`, `internal/agent/agent_test.go`) legitimately define and test the constants — but this test is scoped to `internal/workspace` (via `filepath.Glob("*.go")` run from that package's own test, i.e. `go test` sets the working directory to the package under test), so `internal/agent` is out of scope by construction, no allowlist needed.
- `internal/cli` (`dispatch.go`, `dispatch_model.go`, `instance_from_hook.go`) legitimately names both constants — this is the top-level wiring/config-resolution layer, not the materializer. As long as the test is scoped to `internal/workspace` only (not the whole module), `internal/cli` is out of scope by construction too.
- If a future legitimate call site inside `internal/workspace` needs to name an agent directly (e.g. a one-time migration shim), the `allowedAgentConstFiles` map is the escape hatch — but note the risk the lead named: an allowlist keyed by *file* is coarse and can become a rubber stamp if someone starts adding files to it instead of fixing the violation. A tighter design keys the allowlist by `file:line` (so it silently breaks — test fails again — the moment the excepted line moves or a *second* violation appears in the same file), forcing a human to re-affirm each exemption on any edit to that file.

**Weakness worth being honest about:** this test only catches identifier references to `agent.AgentClaude`/`agent.AgentCodex` by name. It does not catch `agent.ParseAgent("claude")` (a string literal reaching the same behavior through a different door), nor does it catch a local re-declaration (`const myClaude = agent.AgentClaude` elsewhere then used unqualified) — though the latter is contrived and would still surface at the point of the `const` declaration itself, which is in scope. It also does not by itself prove the replacement (interface dispatch) is *used* — only that the named constants are *absent*. Property 1 as stated ("must be reached through an interface, not by naming an agent at the call site") is fully falsifiable by this scan for the literal violation named in the lead's brief.

### Property 2 — "every capability is in exactly one of two states per agent"

**Mechanism: an exhaustiveness table test over an exported, enumerable capability set and agent set — but this requires type-design changes first, since no exported enumerator exists today.**

Today, `agent.known` (`internal/agent/agent.go:31`) is unexported, so no other package — including a materializer-package test — can range over "all agents" without hand-listing them (exactly the pattern in `internal/agent/agent_test.go` and `internal/cli/dispatch_model_test.go:39` today, which is the gap: hand-written lists silently rot). For Property 2 to be a real exhaustiveness test rather than a hand-maintained list, two things need to exist:

1. **`agent` package exports its enumeration**: `func All() []Agent { return append([]Agent(nil), known...) }` (or export `known` itself, capitalized). This makes "all agents" a single source of truth every test and every production exhaustiveness check can consume.
2. **Capabilities need to be a closed, enumerable set** — e.g. a `capability.Capability` string-enum type in its own leaf package (mirroring how `agent.Agent` is a leaf package today, per the doc comment at `internal/agent/agent.go:1-11`), with its own exported `capability.All()`.

Given both, the test is a straightforward double loop:

```go
// internal/workspace/capability_exhaustiveness_test.go
package workspace

func TestEveryCapabilityResolvesForEveryAgent(t *testing.T) {
	for _, ag := range agent.All() {
		for _, cap := range capability.All() {
			state, err := resolveCapability(ag, cap) // the seam under test
			if err != nil {
				t.Fatalf("agent=%s capability=%s: lookup itself failed: %v", ag, cap, err)
			}
			switch s := state.(type) {
			case Implemented:
				// fine, nothing further to assert here
			case Unavailable:
				if s.Reason == "" {
					t.Errorf("agent=%s capability=%s: declared unavailable with no reason", ag, cap)
				}
			default:
				t.Errorf("agent=%s capability=%s: resolved to neither Implemented nor Unavailable (got %T)", ag, cap, state)
			}
		}
	}
}
```

**What has to be true of the type design for this to work:**
- The capability set must be closed and enumerable from outside its defining package (an exported `All()`/`Known()`, same shape as what `agent.Agent` needs for #1 above).
- The per-agent lookup must be *total* — for every `(agent, capability)` pair, calling the resolution function must return one of exactly two variants, never "not found" as a silent third state (e.g. a map lookup with `ok == false` treated as neither). The cleanest way to guarantee totality is a `map[capability.Capability]CapabilityState` per agent that is asserted complete at either construction time (a package-level `init()` panic if a key is missing — fails fast, but only when the package is loaded, e.g. under `go test`) or via this very test (fails when `go test ./internal/workspace/...` runs, which is the CI-gated path already covered by `test.yml`).
- `CapabilityState` itself needs to be a genuine two-variant closed type (e.g. an interface with an unexported marker method implemented only by `Implemented{}` and `Unavailable{Reason string}` in the same package — the classic Go "sealed sum type" idiom) so that `default:` in the switch above is reachable only by a bug, not by a legitimate third case sneaking in unnoticed.

This is the piece of Property 2 that is **not free today**: the repo has the *pattern* precedent (per-agent maps in `dispatch_model.go`) but not the *exhaustiveness assertion* precedent, and not yet an exported agent enumerator. Building this test is real design work on the capability type, not just a test file.

### Other mechanisms the repo's conventions would favor

- **Table-driven subtests with `t.Run`** are the dominant idiom everywhere (`internal/agent/agent_test.go`, `internal/cli/dispatch_model_test.go`, `internal/workspace/marketplace_reconcile_test.go:52`) — any new test should follow that shape rather than a monolithic loop, so a single failing `(agent, capability)` pair reports as one named subtest failure (`--- FAIL: TestEveryCapabilityResolvesForEveryAgent/codex/some-capability`) rather than a bare `t.Errorf` buried in output.
- **Seams via struct fields, not package-level function vars**: `internal/workspace/marketplace_reconcile_test.go:73-79` shows the repo's existing pattern for testing side-effecting logic — `Applier.reconcileMarketplaceAutoUpdate` is a struct field holding a function, swapped in the test. Any capability-resolution seam should likely follow this shape rather than a global.
- **`-race` is on for every unit test run** (`test.yml:92-96`), which matters if capability resolution is memoized/cached — a scan or exhaustiveness test that touches shared state should be free of package-level mutable state or must be proven race-safe.

## D. What the Type System Can Carry

**Can enforce:** that every agent implementation satisfies a required method set — a genuine compile error, not a test failure, the moment a new agent type is added without implementing a method. Concretely, if capability resolution is expressed as an interface:

```go
// internal/agent/capability.go (or similar)
type CapabilityProvider interface {
	Resolve(cap capability.Capability) CapabilityState
}
```

and every agent (today: a `claudeAgent`, `codexAgent` pair of concrete types, not just `Agent` string constants) is required to implement `CapabilityProvider`, then a new agent type that forgets to implement `Resolve` fails `go build`, not `go test`. This is strictly stronger than a test for the "does every agent implement the interface" half of Property 2 — a missing method is caught before CI even runs `go vet`, let alone `go test`.

**Can also enforce, at compile time, that "unavailable" is a real value, not an absence:** the sealed-interface idiom (`Implemented{}` / `Unavailable{Reason string}`, both implementing an unexported marker method) means a function returning `CapabilityState` cannot return `nil` and have callers silently treat that as "some third undeclared state" without an explicit nil-check the compiler doesn't erase — though Go interfaces are nilable, so this specific guarantee ("never nil") still needs either a linter (unavailable per B) or a runtime check like the exhaustiveness test in C. This is the one place compile-time enforcement in Go falls short of what the property fully demands.

**Cannot enforce:** exhaustiveness of a `switch` over the sealed interface's variants, and cannot enforce that *every capability in the closed set* has an entry in *every agent's* map. Go has no `exhaustive`-linter-style compile-time check built into `go vet` or the standard toolchain (that functionality lives in the third-party `golangci-lint`-adjacent `exhaustive` linter, explicitly excluded by this repo's "go vet only" convention per section B). A `switch cap { case A: ...; case B: ... }` with no `default` compiles fine even if a third capability constant is added later and the switch is never updated — the missing case is silently dropped, not a build error. This is exactly why Property 2 needs the *test* from section C: the type system can guarantee "an agent implementation has *a* `Resolve` method" (structural, via the interface), but it cannot guarantee "that method's internal logic covers every capability in the current enum" — that's necessarily a runtime check, because Go's type system has no dependent-type or exhaustiveness-checking machinery for enums represented as constants (as opposed to e.g. Rust's `match` exhaustiveness over a real sum type, which Go's `iota`-based enums do not replicate).

**Net split:** the type system carries "does this agent implement the required shape" (compile error on omission); a test has to carry "does that implementation's logic actually cover every capability with a real state" (exhaustiveness is a run-time-checkable property here, not a compile-time one, given Go's enum idiom). Both halves are needed; neither alone delivers Property 2.

## Implications

- Property 1 is buildable today, cheaply, with zero new dependencies (`go/ast`/`go/parser`/`go/token` are stdlib) and a single new `_test.go` file scoped to `internal/workspace`. The comment-vs-code distinction that already exists in the codebase (two comment mentions of `agent.AgentClaude` in `root_materializer.go` and `worktree_content.go`) is the concrete reason to insist on AST-walking rather than a grep-based test — a grep test would need to special-case comments or false-positive on day one.
- Property 2 is not a "write a test" task alone — it requires first designing `capability.Capability` as a closed, exported, enumerable type (mirroring `agent.Agent`'s existing shape, per its own doc comment about being a deliberate leaf package) and exporting `agent.All()` (today `known` is unexported, and every existing "for each agent" test hand-lists the two agents instead of consuming a shared source of truth — itself a small latent regression risk worth flagging independent of this lead).
- Both properties benefit from the same architectural move: routing agent-specific capability behavior through a small sealed interface (`CapabilityProvider`/`CapabilityState`) rather than switch statements naming `AgentClaude`/`AgentCodex` — which directly answers the "what contract can niwa's workspace-preparation path route agent-specific behavior through" framing question. The interface gets Property 1 (no named constants at call sites — callers hold a `CapabilityProvider`, not an `Agent` they switch on) largely for free, structurally, while Property 2 still needs the explicit exhaustiveness test in C regardless of the interface, per D.

## Surprises

- The repo has **zero** structural/AST-based tests today — this is a greenfield mechanism for this codebase, not an established pattern to extend. Anything built here sets precedent rather than follows one.
- The materializer package (`internal/workspace`) is already closer to Property 1's ideal than expected: production code there never names `agent.AgentClaude`/`agent.AgentCodex` except in two doc comments — it already routes through `agent.Agent` methods (`RootContextFileName`, `LocalContextFileName`, `WritesRepoLevelContext`). The constants only appear in `internal/cli` (top-level flag/config resolution) and the `agent` package itself. This means Property 1's test, if written today, would likely pass immediately — its value is as a regression guard, not as a fix for an existing violation.
- `golang.org/x/tools` is *not* a dependency, but the tempting phrase "use `go/ast` for a structural test" doesn't actually require it — `go/parser` + `go/ast` are stdlib. This is worth surfacing explicitly since it changes the dependency-cost calculus the lead asked about from "new dependency" to "zero new dependency."

## Open Questions

- Does capability resolution belong in the `agent` package itself (extending `agent.Agent` with a `Resolve` method) or in a new leaf package (`internal/capability`) that both `agent` and `internal/workspace` depend on? The doc comment at `internal/agent/agent.go:1-11` explicitly frames `agent` as a deliberate leaf package with zero internal imports — adding a `capability` dependency there would break that invariant, arguing for a separate leaf package instead.
- Should the Property 1 scan run as a normal `go test` (in-band with `go test ./...`, gated by the existing `-race` CI step) or does it need to be its own opt-in target? Given no precedent for a separate structural-test Make target exists, folding it into the normal package test suite (as sketched in C) seems most consistent with repo convention and requires no Makefile/CI changes.
- What is the actual current shape of agent-specific capability logic in `internal/workspace` today — is there an existing switch/if-chain on `Agent` values (beyond the three methods in `agent.go`) that Property 2's design needs to migrate, or is this greenfield? This lead didn't map that surface; a companion lead on the prep-path map likely has it.

## Summary

The repo has no structural-test precedent at all today (no AST/parser tests, no golden-file `-update` pattern, no depguard-style checks) but Property 1 is cheap to build: `go/ast`/`go/parser`/`go/token` are stdlib (zero new dependency, unlike `golang.org/x/tools/go/packages`), and the materializer package already avoids naming `agent.AgentClaude`/`AgentCodex` in code (only in two comments), so a scan test scoped to `internal/workspace` would pass today and guard the interface-boundary contract going forward. Property 2 costs more: it needs `capability.Capability` designed as a closed, exported, enumerable sum type and `agent.All()` exported (today `agent.known` is unexported and every "for each agent" test hand-lists `{AgentClaude, AgentCodex}`), after which an exhaustiveness table test over `agent.All() x capability.All()` becomes straightforward and CI-native (no new Make target, fits inside `go test -race ./...`). The type system can compile-error a missing method on an agent's capability-provider implementation but cannot compile-error a switch that forgets a capability case — Go has no `exhaustive`-linter equivalent in stdlib `go vet`, so Property 2's exhaustiveness must live in a runtime test regardless of interface design.
