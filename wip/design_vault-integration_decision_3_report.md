# Decision 3: Provider Interface Surface

## Decision Question

What is the exact Go interface surface for niwa's pluggable vault Provider
type, and how does it admit both stateful backends (Infisical with session
caching, HashiCorp Vault with leases, 1Password with session tokens) and
stateless backends (sops+age) without forcing one pattern on the other?

## Context Recap

- PRD R1 fixes the public contract: `Resolve`, `Close`, `Name`, `Kind`.
- PRD R20 forbids Go SDK imports — every backend shells out.
- PRD R22 requires `secret.Value` return and stderr scrubbing on errors.
- PRD R15 requires `version-token` provenance feeding `SourceFingerprint`.
- PRD R16 forbids niwa-internal cross-invocation caching; within a single
  command a provider MAY hold session state.
- PRD R29 forbids any disk-persisted secret/session cache on niwa's side.
- Decision 1 (pipeline ordering) places vault resolution before merge,
  file-local and provider-scoped, so the Provider is invoked once per
  vault-ref per source file per `niwa apply`.
- Decision 2 (`secret.Value`) defines the return-value wrapper shape.
- Decision 4 (VersionToken reduction) defines how tokens feed the
  fingerprint.

## Options Evaluated

### Option 1: Minimal interface per R1 skeleton

Just the four R1 methods. Ref is the raw URI string. VersionToken is a
plain string. Backends handle batching internally or not at all.

```go
type Provider interface {
    Resolve(ctx context.Context, ref string) (secret.Value, string, error)
    Close() error
    Name() string
    Kind() string
}
```

**Trade-offs.**
- Plus: smallest interface, easiest to mock, easiest to add new backends.
- Plus: no ceremony for sops (Close is a trivial no-op).
- Minus: provider instantiation has no home — callers construct each
  Provider concretely, wiring the config package to every backend package.
  That couples `internal/config/` (or `internal/workspace/`) to a growing
  set of backend-specific constructors.
- Minus: every `Resolve` call re-parses the URI into `name, key,
  optional`. Cheap, but every backend duplicates the parse and the
  provider-name match against its own `Name()`.
- Minus: provenance for `version-token` is squashed into a plain
  `string`, forcing backends to invent their own encoding and
  `niwa status` to parse it back. This cross-couples Decision 3 to
  Decision 4's storage shape.
- Minus: no structural place for batched resolution when one Infisical
  `secrets list` call could return all keys for a project at once.

### Option 2: Split Reader vs Factory

Two interfaces. `Factory.Open(ctx, cfg)` returns a `Reader`; stateless
backends return a fresh `Reader` per call, stateful ones return a cached
one.

```go
type Factory interface {
    Kind() string
    Open(ctx context.Context, cfg map[string]any) (Reader, error)
}

type Reader interface {
    Name() string
    Resolve(ctx context.Context, ref Ref) (secret.Value, VersionToken, error)
    Close() error
}
```

**Trade-offs.**
- Plus: separates "how do I instantiate a backend from TOML" from "how
  do I read a secret" — clean.
- Plus: natural home for the Registry (map of `Kind()` to `Factory`).
- Minus: two interfaces where one would do. Stateless backends like sops
  would implement a trivial `Open` that just returns `self`. That's
  ceremony with no payoff.
- Minus: PRD R1 is written against one interface (Resolve + Close +
  Name + Kind). Splitting it introduces vocabulary the PRD doesn't use,
  which forces documentation to explain why.
- Minus: Factory being a separate type adds indirection without adding
  expressive power compared to a package-level `Register(kind, ctor)`.

### Option 3: Factory + Provider with optional BatchResolver

One required `Provider` interface (Resolve + Close + Name + Kind), one
optional `BatchResolver` interface detected via type assertion, one
`Factory` interface for construction, one `Registry` type that maps
`kind` to Factory.

```go
type Provider interface {
    Resolve(ctx context.Context, ref Ref) (secret.Value, VersionToken, error)
    Close() error
    Name() string
    Kind() string
}

type BatchResolver interface {
    ResolveAll(ctx context.Context, refs []Ref) ([]Result, error)
}

type Factory func(name string, cfg map[string]any) (Provider, error)

type Registry struct { ... }  // kind -> Factory
```

**Trade-offs.**
- Plus: required surface matches PRD R1 exactly (four methods).
- Plus: batching is genuinely optional — no backend pays the cost of
  implementing it if they don't batch. sops never needs `ResolveAll`.
- Plus: Registry is the natural place for kind-to-factory dispatch and
  for enforcing R2 (anon/named declaration shapes) at construction.
- Plus: Ref-as-struct lets the caller pre-parse and validate URI once,
  and lets providers ignore URI-level concerns they don't care about.
- Minus: type-assertion for the optional interface (`if br, ok :=
  p.(BatchResolver); ok { ... }`) is idiomatic Go but requires
  discipline in the caller. Easy to get wrong if you forget to add the
  fallback.
- Minus: four types instead of one — higher explanation cost in docs.

### Option 4: Unified Provider with explicit lifecycle

Single interface adding `Init`. All backends implement all methods.
sops's `Init` and `Close` are no-ops.

```go
type Provider interface {
    Init(ctx context.Context, cfg map[string]any) error
    Resolve(ctx context.Context, ref Ref) (secret.Value, VersionToken, error)
    Close() error
    Name() string
    Kind() string
}
```

**Trade-offs.**
- Plus: simpler than Option 3 — one interface.
- Plus: no optional-interface type assertion dance.
- Minus: `Init` at the same level as `Resolve` is a smell —
  instantiation and steady-state use aren't the same lifecycle layer.
  Callers forgetting to call `Init` is a runtime bug instead of a
  compile-time error.
- Minus: no home for batching. Either it's unsupported or it leaks into
  the main interface, forcing sops to implement a no-op batch method.
- Minus: no Registry/Factory abstraction — construction still happens
  somewhere, but now entangled with the `Init` step.

## Chosen

**Option 3: Factory + Provider with optional BatchResolver, plus a
typed Ref struct and a typed VersionToken.**

## Rationale

Option 3 matches the PRD R1 four-method shape exactly, so documentation
doesn't have to reconcile vocabulary. Construction is factored cleanly
into a Registry-indexed-by-kind, which gives `internal/config/` a stable
import surface (just the Registry) even as backends are added in v1.1
(sops) and later (1Password, Vault). Batching is a real performance
win for Infisical (one `infisical export` per project rather than N
`infisical secrets get`), but sops genuinely doesn't benefit — the
optional `BatchResolver` lets backends opt in without forcing a no-op
method on everyone else. Ref as a struct pre-parses the URI exactly
once at the merge-boundary in Decision 1's pipeline, and VersionToken
as a struct with `Native` plus `Provenance` gives Decision 4 a typed
handle for the provenance payload R15 demands without pushing string
encoding into every backend. Stderr scrubbing belongs inside each
backend's subprocess invocation (the scrubber is a shared helper) and
is enforced via an acceptance test — the interface doesn't police it
structurally, because every backend shells out differently and the
common path is a helper, not a type.

## Rejected

- **Option 1 (minimal).** No home for construction and no typed
  provenance — pushes dispatch and version-token encoding into every
  caller and backend.
- **Option 2 (Reader/Factory split).** Splitting the Provider into
  Reader+Factory introduces a vocabulary the PRD doesn't use and
  forces stateless backends to implement a trivial `Open` with no
  payoff.
- **Option 4 (unified with Init).** `Init` alongside `Resolve` is a
  lifecycle smell; the Factory pattern expresses construction more
  honestly and catches wiring errors at compile time.

## Interface API Sketch

Package layout: `internal/vault/` (new). Subpackages for each backend:
`internal/vault/infisical/`, `internal/vault/sops/` (v1.1).

```go
// Package vault — public interface for pluggable vault backends.
package vault

import (
    "context"

    "github.com/tsukumogami/niwa/internal/secret"
)

// Ref is a parsed vault:// URI. The resolver parses raw URIs once at
// the pre-merge boundary (Decision 1) and passes structured Refs to
// providers. Providers never see the raw URI.
//
// Anonymous declaration: Provider is empty. Named: Provider is set.
// Resolvers enforce provider-name match against the Provider.Name()
// before dispatching to Resolve; a Provider never receives a Ref
// destined for a different provider.
type Ref struct {
    Provider string // empty for anonymous [vault.provider] declaration
    Key      string // non-empty; provider-native key path
    Optional bool   // true when URI has ?required=false
    Origin   string // workspace.toml | global-overlay | etc. (diagnostic only)
}

// VersionToken is the opaque per-backend version identifier returned by
// Resolve. It feeds SourceFingerprint (R15) and niwa status diagnostics.
//
// Native is the backend-native version (Infisical version ID, sops blob
// SHA-256, 1Password version int, Vault lease+version). Provenance is
// human-pointable context (git commit SHA for git-hosted backends, audit-
// log entry ID for API-hosted backends). Decision 4 owns the storage
// strategy for these tokens in state.json; this file owns only the
// shape returned across the interface boundary.
type VersionToken struct {
    Native     string // backend-specific version identifier
    Provenance string // human-readable trace pointer (git SHA, audit URL, etc.)
}

// Provider is the minimum vault backend contract (PRD R1). All
// registered backends MUST implement Provider. Backends MAY implement
// BatchResolver additionally.
//
// A Provider MAY hold session state across Resolve calls within a single
// niwa command invocation. A Provider MUST NOT persist state across
// invocations (R29 no-disk-cache; enforced by convention and review).
// Close is called at end-of-invocation — stateless backends return nil.
type Provider interface {
    // Name returns the declared provider name. Empty string for an
    // anonymous [vault.provider] declaration. Used in diagnostics and
    // for Ref dispatch.
    Name() string

    // Kind returns the backend kind (e.g., "infisical", "sops"). Used
    // by the Registry for factory lookup and in diagnostics.
    Kind() string

    // Resolve returns the secret bytes wrapped in secret.Value plus a
    // VersionToken. On error the returned error MUST be stderr-scrubbed
    // (the backend uses the shared vault.ScrubStderr helper before
    // wrapping subprocess stderr into the error). Ref.Optional is
    // advisory — Resolve SHOULD NOT special-case it; the resolver
    // layer (Decision 1) handles optional downgrades.
    Resolve(ctx context.Context, ref Ref) (secret.Value, VersionToken, error)

    // Close releases any session-scoped resources. MUST be idempotent.
    // Stateless backends (sops) return nil. Called once per Provider
    // instance at end-of-invocation.
    Close() error
}

// BatchResolver is an optional capability. Providers that can resolve
// multiple refs more cheaply than N independent Resolve calls (e.g.,
// Infisical's `infisical export` listing all keys in one subprocess)
// MAY implement it. Callers detect support via type assertion and fall
// back to iterated Resolve when absent.
type BatchResolver interface {
    ResolveAll(ctx context.Context, refs []Ref) ([]BatchResult, error)
}

// BatchResult pairs each ref in the request with its outcome. Order
// MUST match the request order 1:1. Err is non-nil only for per-ref
// failures (e.g., one key missing, others found); whole-batch auth
// failures return a non-nil second error from ResolveAll instead.
type BatchResult struct {
    Ref     Ref
    Value   secret.Value
    Version VersionToken
    Err     error
}

// Factory constructs a Provider from a declared name and the TOML
// subtable for that provider. cfg holds backend-specific locator fields
// (e.g., project/environment for Infisical, file path for sops). The
// returned Provider is fully initialized but has not yet touched the
// network — auth happens lazily on first Resolve.
type Factory func(name string, cfg map[string]any) (Provider, error)

// Registry maps backend kinds to factories. v1 registers "infisical".
// v1.1 registers "sops". New kinds are added without interface changes.
type Registry struct{ /* kind -> Factory */ }

// Register adds a factory to the registry. Called from backend
// packages' init() functions.
func (r *Registry) Register(kind string, f Factory)

// Build instantiates a Provider from a declared entry, dispatching on
// kind. Called once per declared [vault.provider] or
// [vault.providers.<name>] entry at config-load time.
func (r *Registry) Build(name, kind string, cfg map[string]any) (Provider, error)

// DefaultRegistry is the process-wide registry populated by backend
// init() functions. Tests use a fresh Registry to avoid global state.
var DefaultRegistry = &Registry{}

// ScrubStderr is the shared stderr-scrubbing helper. Backends call it
// on captured subprocess stderr before wrapping into errors (R22
// error-coverage). It removes known-secret fragments from any in-flight
// secret.Value that the provider has previously produced in this
// session, and enforces a conservative allow-list of fields that may
// appear in error context.
func ScrubStderr(raw []byte, known []secret.Value) string
```

## Backend Fit Check

### Infisical (v1, stateful)

```go
// internal/vault/infisical/provider.go
type Provider struct {
    name string
    cfg  struct {
        Project     string
        Environment string
        Path        string // default "/"
    }
    // session state (valid only within one invocation):
    cache map[string]cacheEntry // key -> (value, version)
    mu    sync.Mutex
    authChecked bool
}

func (p *Provider) Name() string { return p.name }
func (p *Provider) Kind() string { return "infisical" }

func (p *Provider) Resolve(ctx context.Context, ref vault.Ref) (secret.Value, vault.VersionToken, error) {
    // On first call, runs `infisical secrets get` for the key, or
    // `infisical export` if BatchResolver path is taken first.
    // Caches within the Provider lifetime only.
    // Stderr scrubbed via vault.ScrubStderr before error-wrap.
}

func (p *Provider) ResolveAll(ctx context.Context, refs []vault.Ref) ([]vault.BatchResult, error) {
    // Implements BatchResolver — one `infisical export` subprocess,
    // parse stdout into per-ref results, cache all.
}

func (p *Provider) Close() error {
    // Clears in-memory cache. No network call; infisical CLI owns its
    // own session refresh and keychain state out-of-process.
    return nil
}

func init() {
    vault.DefaultRegistry.Register("infisical", New)
}
```

Infisical holds state (the `cache` map) across Resolve calls in one
invocation, implements BatchResolver for the perf win, and hands the
session-token story to the `infisical` CLI (which caches auth in the
system keychain per R16 — out of niwa's scope).

### sops + age (v1.1, stateless)

```go
// internal/vault/sops/provider.go
type Provider struct {
    name     string
    filePath string // path to encrypted file (relative to ConfigDir)
}

func (p *Provider) Name() string { return p.name }
func (p *Provider) Kind() string { return "sops" }

func (p *Provider) Resolve(ctx context.Context, ref vault.Ref) (secret.Value, vault.VersionToken, error) {
    // Spawn `sops -d <file>` once per Resolve (or cache the decrypted
    // JSON within the Provider lifetime — see note below).
    // VersionToken.Native = SHA-256 of encrypted file bytes.
    // VersionToken.Provenance = last-modifying git commit SHA from
    //   `git log -1 --format=%H -- <file>` in ConfigDir.
}

// sops does NOT implement BatchResolver — a single `sops -d` already
// yields the whole file. Within-invocation caching of the decrypted
// map is a Provider-internal optimization, not a separate interface.

func (p *Provider) Close() error {
    // Clear the in-memory decrypted map. No session to release.
    return nil
}

func init() {
    vault.DefaultRegistry.Register("sops", New)
}
```

sops does not implement BatchResolver because `sops -d` on a file
already yields all keys — the Provider caches the decrypted map
internally and serves all subsequent `Resolve` calls from that cache,
within the single-invocation lifetime. `Close` clears the map. No
session to tear down.

### Future: 1Password, HashiCorp Vault OSS (deferred)

Both backends are stateful session-token / lease holders. They fit the
Provider interface identically to Infisical. 1Password's `op signin`
token and Vault's lease-renewal are owned by the CLIs, not by niwa; the
Provider just tracks its in-process `cache` and returns tokens. When
added, they register in init() and the rest of niwa needs zero
changes.

### Future: batching-hostile backends

If any future backend can't batch (one round-trip per key, no export),
it simply omits BatchResolver. Callers using type-assertion fall back
to iterated Resolve. No interface change.

## Open Items for Phase 3 Cross-Validation

- **Decision 2 coupling.** `secret.Value` shape is owned by Decision 2.
  This decision assumes it's a struct type passable by value with
  redacting formatters. If Decision 2 chooses an interface, the Resolve
  signature needs to change to return `secret.Value` as an interface
  (still fine, but the signature differs).
- **Decision 4 coupling.** `VersionToken` is a struct with
  `Native string, Provenance string` here. Decision 4 owns
  state.json storage strategy. If Decision 4 needs richer shape (e.g.,
  typed provenance with URL + audit-log ID fields), this struct grows
  new fields without breaking existing backends — additive, safe.
- **Decision 1 coupling.** The pre-merge resolver (Decision 1) is the
  only caller of `Registry.Build` and the iterator over `Resolve`
  calls. It owns: URI parsing into `Ref`, provider-name dispatch,
  optional-downgrade handling, `--allow-missing-secrets` semantics, and
  end-of-invocation `Close`. The interface sketch here does not
  specify that ordering; Decision 1 does.
- **`ScrubStderr` API.** The helper's `known []secret.Value` parameter
  assumes the resolver threads the accumulated resolved values into
  subsequent error-context scrubs. An alternative is a scrubber with
  an `Add(secret.Value)` method bound to the Provider's lifetime. This
  is a helper-ergonomics call and can be settled at implementation
  time; it does not change the Provider interface.
- **Registry as package global.** `DefaultRegistry` is a package global
  populated by backend init(). Tests should be able to construct a
  fresh Registry with just the backends they need. The constructor
  and accessor shape will be confirmed during implementation.
