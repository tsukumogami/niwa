# Decision Report: Slug Parser Package and API Shape

**Decision ID:** design_workspace-config-sources_decision_3
**Question:** Where does the canonical slug parser live and what does its API surface look like?
**Tier:** standard (fast path — no validator bakeoff)
**Status:** complete

## Decision

**New leaf package `internal/source/` exposing a typed `Source` struct with constructor `Parse(string) (Source, error)`, a `String() string` round-tripper, and a small set of methods that derive concrete artifacts (`CloneURL(protocol)`, `OverlayDerivedSource()`, `DisplayRef()`).**

The package is a leaf (only standard library imports), so every consumer in the workspace-config-sources design can import it without inducing a cycle:

- `internal/cli/init.go`, `internal/cli/config_set.go`, `internal/cli/status.go` (CLI surface)
- `internal/workspace/clone.go` and the new fetcher (`Cloner`, snapshot extractor)
- `internal/config/registry.go` (mirror-field population, R2/R22)
- `internal/workspace/state.go` (`config_source` block, R24)
- `internal/guardrail/githubpublic.go` (reads host/owner/repo from the snapshot marker, R31)
- New discovery probe and overlay slug deriver (R5–R7, R35)

## Confidence

**High.** The cycle analysis is mechanical (current import graph rules out two of the four candidate packages outright), and the Go convention argument is one-sided in this codebase (every leaf concern with a typed value already lives in its own package: `internal/secret`, `internal/buildinfo`, `internal/github`, `internal/vault`).

## Considered Alternatives

The decision splits into two orthogonal axes; each axis is evaluated separately.

### Axis A — Package boundary

#### A1. New package `internal/source/` (CHOSEN)

The parser, the `Source` value type, the renderer, and the table-driven test suite live in a single self-contained package.

**Pros:**

- **No import-cycle risk.** A leaf package can be imported by every current consumer (`cli`, `workspace`, `config`, `guardrail`). The existing graph already has `workspace -> config` and `guardrail -> config`; adding `internal/source/` as a dependency of all four packages is additive, not cyclical.
- **Matches existing leaf-package convention.** `internal/secret`, `internal/buildinfo`, `internal/github`, and `internal/vault` are each focused leaves whose names describe a domain concept rather than a layer. A `source` slug is a domain concept of comparable scope.
- **Test ergonomics.** R3's strict-parser rules call for a large table-driven test suite (per-character whitespace cases, separator-ordering cases, multi-`:` and multi-`@` cases, plus the AC-S* matrix). A package whose only job is the parser keeps that table self-contained and discoverable; a future contributor adding case AC-S3f doesn't have to figure out which file in `internal/workspace/` the table lives in.
- **Round-trip invariant is local.** R22 requires that re-parsing the canonical `source_url` and re-rendering produces a byte-identical string. Keeping `Parse` and `String()` next to each other in one file makes the property easy to assert in one place.

**Cons:**

- One more `internal/` directory to navigate. (Mitigated by the fact that the codebase already uses many narrow leaf packages, so this is the established style.)

#### A2. Extend `internal/workspace/` (joined with the new fetcher)

The parser lives next to the snapshot fetcher in `internal/workspace/`.

**Pros:**

- Co-locates the parser with its largest consumer (the fetcher and `Cloner`).
- Reduces directory count.

**Cons (decisive):**

- **Creates a hard cycle with `internal/config/`.** The registry writer in `internal/config/registry.go` needs the parsed five-tuple to populate mirror fields (R2, R22, R23). `config` cannot import `workspace` because `workspace` already imports `config` extensively (12+ files). The cycle is unbreakable at the package level.
- The `internal/workspace/` package is already large (60+ Go files) and mixes apply, classify, content, state, sync, and clone concerns. Adding a sixth orthogonal concern compounds the existing readability problem.
- Tests for the strict parser (R3) would compete for attention with the fetcher's integration tests in the same package.

#### A3. Extend `internal/config/`

The parser lives next to the registry schema in `internal/config/`.

**Pros:**

- Co-locates the parser with the registry mirror fields it populates (R2, R22).
- Avoids the `workspace -> config` cycle from A2 because the dependency direction is preserved.

**Cons:**

- **`internal/config/` is already the workspace's most-imported package** (15+ importers). Adding parser logic there means every test of `internal/config/`, every callsite that depends on `WorkspaceConfig`, and every package that touches the registry now also pulls in `regexp`, `unicode`, and the parser's surface area. The pattern in this codebase is to keep `config` focused on file-format types and validation, with adjacent concerns living in leaf packages.
- The existing `parseOrgRepo` helper in `internal/config/overlay.go` is unexported and intentionally narrow (one consumer: `DeriveOverlayURL` and `OverlayDir`). Promoting it into a richer parser would mix the overlay-URL concept with the team-config slug concept that R35 explicitly distinguishes (overlay slugs are derived from the parsed source, not parsed from user input).

#### A4. Stay in `internal/workspace/clone.go` and rename `ResolveCloneURL`

The simplest possible move: keep the current location and grow the existing function.

**Cons (decisive):**

- Same cycle problem as A2 — the registry can't reach it.
- `ResolveCloneURL` returns a string; the new contract returns a five-tuple. This is a rewrite, not a rename.

### Axis B — API shape

#### B1. Typed `Source` struct + `Parse`/`String` free functions + methods on `Source` (CHOSEN)

```go
package source

// Source is the parsed five-tuple from a slug.
type Source struct {
    Host    string // "github.com" by default
    Owner   string
    Repo    string
    Subpath string // "" means "run discovery"
    Ref     string // "" means "default branch"
}

// Parse strict-parses a slug per PRD R1/R3. Errors name the offending input.
func Parse(s string) (Source, error)

// String renders Source back to its canonical slug form. Round-trips exactly.
func (s Source) String() string

// CloneURL returns a git-clone URL for the given protocol ("https" | "ssh").
// Used by the git-clone fallback path (R15).
func (s Source) CloneURL(protocol string) (string, error)

// TarballURL returns the GitHub REST tarball URL for the source's ref.
// Returns ok=false when host is not "github.com" (R14).
func (s Source) TarballURL() (string, bool)

// CommitsAPIURL returns the GitHub REST commits/{ref} URL for the SHA-only
// drift check (R16). Returns ok=false when host is not "github.com".
func (s Source) CommitsAPIURL() (string, bool)

// OverlayDerivedSource returns the auto-discovered overlay slug per R35:
// whole-repo case -> <host>/<owner>/<repo>-overlay
// subpath case    -> <host>/<owner>/<basename(subpath)>-overlay
func (s Source) OverlayDerivedSource() Source

// DisplayRef returns the ref string for `niwa status`, with "(default branch)"
// appended when no ref was specified (R20).
func (s Source) DisplayRef() string
```

**Pros:**

- **Single canonical type for the five-tuple.** Decision Driver "one canonical source-tuple representation" maps directly: registry mirror, state's `config_source` block, status display, guardrail input, fetcher input, and overlay deriver all consume the same `Source` value. No "five places represent the same concept differently" pattern.
- **Idiomatic Go.** Matches `internal/github`'s `APIClient`, `internal/secret`'s `Error`/`Redactor`, `internal/vault`'s provider types — typed value + methods is the dominant style in this codebase.
- **Round-trip is a method pair.** `Parse` + `String()` next to each other make the R22 property test trivial: `for _, s := range cases { got, _ := Parse(s); if got.String() != s { ... } }`.
- **Methods compose without forcing all callers to depend on every consumer.** `CloneURL` doesn't drag in HTTP types; `TarballURL` doesn't drag in `archive/tar`. Each method returns a string + ok-flag, letting `internal/workspace/` and `internal/github/` remain the ones that actually do the I/O.
- **Marshalling is straightforward.** TOML/JSON tags on `Source` give the registry mirror fields (R2) and the state's `config_source` block (R24) for free, without a separate DTO type.

**Cons:**

- Slightly heavier than free functions for the simplest call sites (e.g., `niwa status` only needs `String()` + `DisplayRef()`). Not a real cost; the type is tiny.

#### B2. Free functions only (`Parse`, `Render`, `CloneURL`, `OverlayDerived`) with `Source` as a plain struct

```go
func Parse(s string) (Source, error)
func Render(s Source) string
func CloneURL(s Source, protocol string) (string, error)
// ...
```

**Pros:**

- Maximally explicit; no "is this a method of `Source` or a free function" lookup cost.
- Easy to mock at the test boundary.

**Cons:**

- Departs from the codebase's typed-value-with-methods convention (see `internal/github/APIClient.ListRepos`, `internal/secret/Redactor.Scrub`).
- Forces consumers to write `source.CloneURL(s, "ssh")` instead of `s.CloneURL("ssh")`, which reads worse at every call site.
- The `Source` value loses its grammar: a struct with no methods can't enforce that `Parse` is the only way to construct a valid `Source` (callers can hand-build invalid five-tuples). Methods on `Source` make the type self-describing and let `Parse` be the only documented entry point.

#### B3. Fluent builder pattern

```go
src := source.NewBuilder().Host("github.com").Owner("foo").Repo("bar").Build()
```

**Cons (decisive):**

- The PRD's slug grammar is read-once-from-user-input, not progressively assembled. There's no use case where a caller has half a slug and adds the other half later. A builder is overkill for "parse one string into one struct."
- No precedent in the codebase. Adding a builder pattern for one type would be inconsistent.

#### B4. Multi-return parser with separate renderer type

```go
host, owner, repo, subpath, ref, err := source.Parse(s)
```

**Cons (decisive):**

- Five-return-value functions are a Go anti-pattern. The whole reason the design driver "one canonical source-tuple representation" exists is to avoid passing the tuple around as positional arguments.
- The renderer would need the same five arguments, breaking the round-trip locality.

## Rationale Summary

The cycle analysis on Axis A is decisive: among the four package candidates, only A1 (new leaf package) lets every consumer import the parser without violating the existing `workspace -> config` direction. A2 and A4 create unbreakable cycles; A3 works but bloats the most-imported package in the codebase with logic that's stylistically misplaced.

On Axis B, B1 (typed struct with methods plus a `Parse` constructor) matches the dominant style of every existing leaf package in `internal/` (`secret`, `github`, `vault`, `buildinfo`), gives the round-trip invariant a natural home, and lets the parser be the single canonical representation of the five-tuple — directly satisfying the design driver "one canonical source-tuple representation."

The combination — `internal/source/` package with a typed `Source` value, `Parse(string)` / `String()` round-trippers, and methods that derive concrete artifacts — is the smallest change that satisfies all the consumer requirements without forcing any of them to know about the others.

## Implementation Notes for the Design Doc

- **File layout:** start with `internal/source/source.go` (types + `Parse` + `String`), `internal/source/source_test.go` (table-driven R3 test suite), `internal/source/url.go` (`CloneURL`, `TarballURL`, `CommitsAPIURL`), `internal/source/overlay.go` (`OverlayDerivedSource`).
- **Backward-compat:** `Parse("org/repo")` must produce `Source{Host: "github.com", Owner: "org", Repo: "repo", Subpath: "", Ref: ""}` and `.String()` must return exactly `"org/repo"` (host omitted when default, no separator when fields are empty). This satisfies R23/R33 — old registries with `source_url = "org/dot-niwa"` parse and re-render byte-identically.
- **Migration of existing parsers:** `ResolveCloneURL` in `internal/workspace/clone.go` becomes a one-line wrapper that calls `source.Parse(...)` then `Source.CloneURL(...)`. `parseOrgRepo` in `internal/config/overlay.go` is replaced by `source.Parse(...)`. `DeriveOverlayURL` and `OverlayRepoName` are replaced by `Source.OverlayDerivedSource().String()` and `Source.OverlayDerivedSource().Repo`.
- **Errors:** parser errors are plain `error` values from `fmt.Errorf` that name the offending input verbatim per R3 (the AC-S3* tests assert specific stderr substrings). No custom error type needed; the AC matrix is satisfied by string-matching.
- **TOML/JSON tags on `Source`:** add `toml:"source_host,omitempty"`-style tags so the registry's mirror fields (R2) and state's `config_source` block (R24) marshal directly from `Source` without a parallel DTO.

## Assumptions

1. **Default host is `github.com`.** When the slug omits a host, `Parse` fills in `"github.com"` and `String()` omits it on render. (PRD R1: `[host/]owner/repo[:subpath][@ref]`; the bracketed `host/` is optional.)
2. **`internal/source/` doesn't need to do I/O.** All network/disk operations stay in `internal/workspace/` (fetcher, snapshot extractor) and `internal/github/` (HTTP client). The parser produces strings (URLs); it does not call them.
3. **The five-tuple is exhaustive.** The decision assumes no sixth identity dimension (e.g., a query-string parameter or fragment) is added during the design's later phases. If one is, it's an additive field on `Source`, not a structural change.
4. **No third-party dependencies.** The parser uses only `strings`, `unicode`, and `fmt` — no regex packages beyond what's already in the codebase, and certainly no URL-parser library.

## Rejected Options Summary

| Option | Reason rejected |
|--------|-----------------|
| A2: extend `internal/workspace/` | Creates an unbreakable `config -> workspace` import cycle |
| A3: extend `internal/config/` | Bloats the most-imported package with stylistically misplaced logic |
| A4: keep in `clone.go`, rename | Same cycle problem as A2; also a rewrite, not a rename |
| B2: free functions only | Departs from the codebase's typed-value-with-methods convention |
| B3: fluent builder | No use case for progressive assembly; no codebase precedent |
| B4: multi-return parser | Five-return-value functions defeat the "one canonical type" driver |
