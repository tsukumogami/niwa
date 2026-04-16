---
status: Planned
upstream: docs/prds/PRD-vault-integration.md
problem: |
  Wiring a vault layer into niwa forces structural changes across three
  existing subsystems — the TOML schema, the override-merge pipeline, and
  the materialization pipeline — without breaking v0.6 configs or adding
  Go dependencies. The hardest technical problem is ordering: D-6 requires
  `vault://` URIs to resolve inside each source file's provider context
  BEFORE the merge runs, which inverts the current parse → merge →
  materialize flow into parse → resolve-per-file → merge → materialize.
decision: |
  Introduce three new packages (`internal/secret`, `internal/vault`,
  `internal/guardrail`) and a `config.MaybeSecret` leaf type. Insert a
  resolver stage between parse and merge in `apply.go:runPipeline` so
  that each config file's `vault://` URIs resolve within the same
  file's provider table (enforcing D-9). `secret.Value` is a struct
  wrapper with redacted formatters; `secret.Error` + a context-scoped
  redactor scrub error-chain interpolation. Provider backends are
  subprocess-only (R20); v1 ships Infisical, v1.1 adds sops+age.
  Merge and materializer signatures stay the same; only their leaf
  types change.
rationale: |
  Keeping the config package pure TOML and the merge pipeline a pure
  reduction preserves two valuable test-surface invariants while
  still satisfying D-6's resolve-before-merge ordering. The typed-
  value discipline starting immediately after parse means no
  downstream layer ever handles vault URIs as bare strings — R22's
  redaction story is structural. Subprocess-per-backend with an
  optional `BatchResolver` interface accommodates both
  session-caching backends (Infisical) and stateless ones (sops)
  without forcing one pattern on the other. The alternative
  approaches (resolve-at-materialize with provenance sidecar, lazy
  resolution handles, merge-embedded resolution) all either spread
  security-critical routing across more surface area or break
  existing test invariants. State-file schema-version 1→2 is
  additive and backwards-compatible; old states load fine.
---

# DESIGN: Vault Integration

## Status

Planned

## Context and Problem Statement

The [vault-integration PRD](../prds/PRD-vault-integration.md) commits niwa
to adding a pluggable vault layer, with per-file-scoped providers, an
override-aware personal-overlay model, and 11 "never leaks" security
invariants. Those requirements cut across three existing subsystems that
were not designed for this shape:

- **`internal/config/` (TOML schema + parser).** Today's schema has
  `[env.vars]` and `[claude.env.vars]` as flat string maps. The PRD splits
  them into sensitivity-coded siblings (`[env.vars]` + `[env.secrets]`)
  each with three requirement sub-tables, adds a top-level `[vault]`
  table in two mutually-exclusive shapes (`[vault.provider]` anonymous
  vs `[vault.providers.<name>]` named), adds `[workspace].vault_scope`,
  and adds a companion `GlobalOverride.Vault` field with tight merge
  semantics (R12: add-only, replace-forbidden). Several new parse-time
  rejections are also required: mixed anon/named, `vault://` URIs in
  forbidden contexts (`[claude.content]`, `[env.files]`, identifier
  fields), and cross-config provider name references.

- **`internal/workspace/override.go` (merge pipeline).** D-6 keeps the
  existing `MergeOverrides` / `MergeGlobalOverride` infrastructure but
  demands a new ordering: `vault://` URIs must resolve to `secret.Value`
  inside each source file's provider context BEFORE the merge runs.
  This is a structural inversion of today's parse → merge → materialize
  flow. Resolving post-merge would flatten file-of-origin and make
  `vault://<name>/key` ambiguous when layers declare identically-named
  providers (D-9 file-local scoping requires the opposite).

- **`internal/workspace/materialize.go` and `state.go` (materialization
  pipeline).** The PRD adds `secret.Value` as an opaque type that must
  survive every formatter, JSON encoder, error wrapper, and log path.
  It fixes a pre-existing `0o644` bug that currently materializes env
  and settings files world-readable. It adds `.local` infix + instance-
  root `.gitignore` invariants, and introduces `ManagedFile.SourceFingerprint`
  as a SHA-256 reduction of `(source-id, version-token)` tuples so
  `niwa status` can distinguish user-edited drift from upstream
  rotation. For mixed-source materialized files (the common `.local.env`
  case combining workspace env files, discovered repo env files,
  `env.vars` entries, and overlay-merged values), the fingerprint must
  reduce consistently across sources.

Three subsystems, coupled by a single cross-cutting concern. The design
must commit to concrete interfaces, an explicit ordering of the new
resolve-stage relative to the existing merge pipeline, a version-token
scheme that works for both Infisical (API-native versioning) and the
v1.1 sops backend (synthesized from blob hash + commit SHA), and
`secret.Value` machinery strong enough to survive the full error-wrap
chain (including provider-CLI stderr capture — R22's acceptance test
induces an auth error with a known-secret fragment in stderr).

The `vault://` URI resolution is happening in a system that already has
sophisticated override semantics. This design must honor v0.6's
`MergeGlobalOverride` behavior (personal-wins for `Env.Vars`), preserve
the `ClaudeConfig` / `ClaudeOverride` type split shipped in v0.6, and
add new constraints (R12 provider add-only, R31 shadow visibility)
without breaking v0.6 configs that don't use vaults at all.

## Decision Drivers

### From the PRD

- **Resolve-before-merge ordering (D-6, M-1 architect-review fix).** The
  design must specify exactly where in the pipeline `vault://` URIs become
  `secret.Value`. Post-merge resolution cannot honor D-9 file-local
  scoping; pre-merge resolution inverts the existing flow.
- **Pluggable provider interface from v1 (R1, D-1).** The Go interface
  must support Infisical (v1) and sops+age (v1.1) without rework. It
  must admit backends that cache authenticated sessions (Infisical)
  and backends that are stateless per call (sops).
- **Zero new Go library dependencies (R20).** All providers invoke
  subprocesses. This forces design decisions about subprocess lifecycle,
  stderr scrubbing, and version-token parsing from CLI output.
- **Opaque `secret.Value` type (R22).** Redaction must survive formatters,
  JSON encoding, `gob`, `fmt.Errorf("...: %w", err)` chains, and captured
  provider-CLI stderr. The type is load-bearing for the security
  invariants.
- **Eleven security invariants (R21–R31).** Each is a concrete constraint
  on implementation shape: no argv, no config writeback, `0o600`, `.local`
  infix, no CLAUDE.md interpolation, no status content, no `os.Setenv`,
  no disk cache, public-repo guardrail one-shot override, override-
  visibility diagnostics.
- **File-local provider scoping (R3, D-9).** The parser must track which
  file declared which providers; per-file resolution must use the
  right provider table; merging cannot mix provider names across layers.
- **Personal-overlay add-only semantics for providers (R12).** Personal
  overlay may add new provider names but cannot replace team-declared
  names. A collision is a hard apply error with remediation pointing
  to per-key overrides.
- **`SourceFingerprint` provenance (R15).** Version-tokens must carry
  enough metadata that `niwa status` can point a user to "what change
  caused this rotation" — commit SHA for sops/git-native, native
  version ID + audit-log pointer for API-hosted.
- **Public-repo guardrail with remote enumeration (R14, R30).** The
  guardrail must enumerate all git remotes (not just `origin`), detect
  GitHub public patterns, and block apply unless `--allow-plaintext-
  secrets` is supplied (strictly one-shot, no state persistence).
- **Infisical as the v1 backend (R1, D-1).** The design commits to
  Infisical's subprocess interface, auth model, and version-token
  shape. sops is v1.1 — the interface must not over-fit Infisical.

### Implementation-specific

- **Backwards compatibility with v0.6 configs.** Workspaces that don't
  use vaults must keep working unchanged. The `[env.vars]`/`[env.secrets]`
  split must accept v0.6's flat `[env.vars]` shape without migration.
- **Hook into the existing materializer interface.** `EnvMaterializer`,
  `SettingsMaterializer`, and `FilesMaterializer` already implement a
  common materializer contract. The design should preserve that
  boundary and not introduce a parallel "vault materializer."
- **Minimal churn in `MergeGlobalOverride`.** Today's merge is a pure
  function over `*WorkspaceConfig` values. Inserting a resolve-stage
  before it must not bloat `MergeGlobalOverride`'s signature or turn
  every test that merges configs into a test that needs a provider
  mock.
- **Testability of the security invariants.** Each of R21–R31 needs a
  verification path — unit test, functional test, or CI lint. The
  design must pick where each invariant lives (compile-time check vs
  runtime assertion vs acceptance test) to make them enforceable, not
  aspirational.
- **Performance sensitivity for the common case.** Typical workspaces
  hit ≤ ~20 vault references per apply. The subprocess-per-resolve
  model can be slow if each call cold-starts the provider CLI.
  Batching, provider-held session caches, and parallel resolution are
  all design choices that affect end-user latency.
- **Diagnostic ergonomics across the eleven invariants.** R31's
  override-visibility is not just a stderr line — it must integrate
  with `niwa status` and `niwa status --audit-secrets`. R15's
  fingerprint provenance must render via git SHA (for sops) and
  audit-log pointer (for Infisical), which are different UIs.
- **Config-package independence from git (S-1 architect concern).** The
  public-repo guardrail needs remote-URL classification. This must
  not pull git-awareness into `internal/config/`. The guardrail
  belongs in `internal/workspace/apply.go` or a dedicated package.

## Considered Options

Six design-level decisions. Each was investigated independently and then
cross-validated for conflicts. Full decision reports live in `wip/` and
are consolidated into Phase 4's Solution Architecture.

### Decision 1: Pipeline ordering — where vault:// URI resolution happens

PRD decision D-6 already commits niwa to `parse → per-file-resolve →
merge → materialize`. The question is how to realize that ordering in
Go code: which function owns the subprocess work, what type-shape the
merge stage sees, and which of today's functions it couples to.

Four options were evaluated:

- **Option 1 — Resolve-inside-load.** `config.Load` returns an
  already-resolved `*WorkspaceConfig`. Shortest diff at the merge layer
  but forces every parse-only test (today pure) to thread a provider
  registry. Config package acquires a subprocess dependency.
- **Option 2 — Separate Resolver stage between load and merge.** New
  `internal/vault` package exposes `ResolveWorkspace(ctx, cfg)` and
  `ResolveGlobalOverride(ctx, gco)`. Loader stays pure TOML; merge
  stays a pure reduction. `apply.go` composes the stages.
- **Option 3 — Resolve-at-materialize with provenance sidecar.** Keep
  URIs as plain strings through merge; carry an out-of-band
  `map[fieldpath]providerTable` that materializers consult. Doubles
  the merge code and puts security-critical routing inside stateless
  materializers.
- **Option 4 — Lazy `secret.Value` that defers resolution.** Handles
  captured file-local provider tables in closures; `.Get(ctx)`
  triggers resolution at materialize time. Spreads subprocess errors
  across every materializer and complicates `SourceFingerprint`
  computation by forcing fingerprint code to re-trigger resolution.

#### Chosen: Option 2 — Separate Resolver stage between load and merge

New `internal/vault` package holds `Provider` (R1 interface),
`ProviderRegistry` built from `cfg.Vault`, and `Resolver`. The apply
orchestrator composes:

```
config.Load(path)                      → *WorkspaceConfig    (pure)
config.ParseGlobalConfigOverride(data) → *GlobalConfigOverride (pure)
vault.ResolveWorkspace(ctx, cfg)       → *WorkspaceConfig    (subprocess IO)
vault.ResolveGlobalOverride(ctx, gco)  → *GlobalConfigOverride
workspace.ResolveGlobalOverride(…)     → GlobalOverride      (existing merge)
workspace.MergeGlobalOverride(…)       → *WorkspaceConfig    (existing merge)
workspace.MergeOverrides(…)            → EffectiveConfig     (existing merge)
materializers                          → files
```

Every TOML string slot that accepts a vault URI becomes a
`config.MaybeSecret` sum type carrying either `{Plain: string}` or
`{Secret: secret.Value, Token: VersionToken}`. The parser always
produces `{Plain}`; the resolver rewrites vault-URI plains into
`{Secret}`.

**Rationale:** Option 2 is the only option that keeps both valuable
invariants intact — the config package's pure-parse test harness, and
the merge pipeline's pure-reduction test harness. File-local scoping
is enforced structurally (the resolver reads providers from the same
struct it's resolving). R22's typed-value discipline starts immediately
after parse, so no downstream layer handles vault URIs as bare strings.
R16's re-resolve-every-apply is satisfied by construction (the
orchestrator calls Resolve unconditionally). The only new integration
point is two calls inserted in `apply.go:runPipeline` between
global-config parse and `MergeGlobalOverride`.

#### Alternatives Considered

- **Option 1** — rejected: subprocess IO in `config.Load` pollutes the
  parse-only test surface and requires a provider registry at every
  loader call site.
- **Option 3** — rejected: doubles merge code (every branch must also
  merge provenance) and concentrates security-critical vault routing
  in materializers, widening the attack surface for a property the PRD
  never asks for.
- **Option 4** — rejected: `.Get(ctx)` at materialize time spreads
  subprocess errors across every materializer and forces fingerprint
  code to re-trigger resolution.

### Decision 2: `secret.Value` type shape and error-wrap strategy

R22 load-bearing invariant. Redaction must survive every Go formatter,
JSON/gob, error-wrap chains (`fmt.Errorf("...: %w", err)`), and captured
provider-CLI stderr. The codebase has 186 existing `fmt.Errorf("%w")`
sites across 30 files; Options that can't defend the error-chain
interpolation path are fatally insufficient.

- **Option 1 — Struct wrapper + `secret.Error` wrapper + context-
  scoped `Redactor`.** `secret.Value` is a struct with private bytes
  plus all six PRD-required formatters returning `***`. `secret.Error`
  wraps errors whose chain touches secret values; `secret.Wrap(err,
  values...)` and `secret.Errorf(format, args...)` register known
  secret fragments on a per-resolve-call redactor that scrubs strings
  before interpolation. Plaintext access via `UnsafeReveal(v) []byte`
  in `internal/secret/reveal` — single grep-able read site.
- **Option 2 — String alias with marshal traps.** `type Value string`
  + method set. Cannot intercept `fmt.Errorf` interpolation of
  substrings; once a secret is baked into an error message, no method
  runs. Rejected as insufficient — fails R22's acceptance test by
  construction.
- **Option 3 — Interface with per-backend implementation.** Each
  backend returns its own concrete type implementing a `Value`
  interface with `Bytes() []byte` + `Redacted() string`. Plaintext
  accessor is trivially callable, losing the grep-ability of
  `UnsafeReveal`. Inconsistency risk between backends.
- **Option 4 — Option 1 + go/analysis linter enforcing
  `secret.Wrap`/`secret.Errorf` at every Value-touching call site.**
  Strongest compile-time enforcement. Adds a CI tool and learning
  curve. Deferred to post-v1 hardening.

#### Chosen: Option 1 (with Option 4 deferred to post-v1)

```go
package secret

type Value struct {
    b      []byte        // private plaintext bytes
    origin originTag     // provider name, key, version token
}

func (v Value) String() string        { return "***" }
func (v Value) GoString() string      { return "secret.Value(***)" }
func (v Value) Format(s fmt.State, verb rune) { /* all verbs -> *** */ }
func (v Value) MarshalJSON() ([]byte, error)  { return []byte(`"***"`), nil }
func (v Value) MarshalText() ([]byte, error)  { return []byte("***"), nil }
func (v Value) GobEncode() ([]byte, error)    { return nil, errRefuseGob }

type Error struct {
    inner    error
    redactor *Redactor
}
func (e *Error) Error() string { return e.redactor.Scrub(e.inner.Error()) }
func (e *Error) Unwrap() error { return e.inner }

func Wrap(err error, values ...Value) error
func Errorf(format string, args ...any) error
```

**Rationale:** Option 1 is the only option that handles the hardest
case — error-wrap interpolation of provider-CLI stderr carrying
fragments niwa didn't explicitly tag. The redactor accumulates
fragments per-resolve-call, scrubs before interpolation (idempotent on
re-wrap), and survives arbitrary depth of `%w` chains. `UnsafeReveal`
in a sub-package is a single grep-able plaintext-read surface for
code review. The go/analysis linter (Option 4) is deferred because
Option 1's runtime enforcement plus the acceptance test for
provider-CLI stderr scrubbing (PRD R22) meets the v1 bar.

#### Alternatives Considered

- **Option 2** — rejected: cannot intercept `fmt.Errorf` substring
  interpolation. Fails R22 acceptance test by construction.
- **Option 3** — rejected: `Bytes()` is trivially callable and loses
  grep-ability; inconsistency risk between backend implementations of
  `Redacted()`.
- **Option 4** — deferred: Option 1's runtime defenses + acceptance
  tests meet v1. A linter is the right v1.1 hardening when the v1
  patterns are stable.

### Decision 3: Provider interface surface

R1 pins a four-method skeleton. The design commits to concrete Go
signatures and decides how the interface accommodates Infisical
(session-caching, batch-capable via `infisical export`) vs sops
(stateless per-file decrypt) vs future backends (1Password sessions,
Vault leases).

- **Option 1 — Minimal interface per R1 skeleton.** Just Resolve +
  Close + Name + Kind. No batching, no lifecycle hooks. Simplest.
- **Option 2 — Split read-interface vs session-interface.** `Reader`
  interface + `Factory.Open(ctx, config) (Reader, error)`. Stateful
  backends cache one Reader; stateless return a new one per call.
  Introduces vocabulary (Reader/Factory) not used by the PRD.
- **Option 3 — Factory + Provider + optional BatchResolver, typed
  Ref + VersionToken.** Factory constructs Provider given config.
  Provider implements Resolve + Close + Name + Kind. Backends that
  can batch implement an optional `BatchResolver` interface detected
  at runtime. Ref is a parsed struct (provider name, key, optional
  flag). VersionToken is `{Token string; Provenance string}`.
- **Option 4 — Unified Provider with explicit lifecycle methods.**
  Resolve + Close + Init + Name + Kind. Simpler than Option 3 but
  forces all backends to implement Init even when no-op.

#### Chosen: Option 3 — Factory + Provider + optional BatchResolver

```go
package vault

type Provider interface {
    Name() string
    Kind() string
    Resolve(ctx context.Context, ref Ref) (secret.Value, VersionToken, error)
    Close() error
}

type BatchResolver interface {
    ResolveBatch(ctx context.Context, refs []Ref) ([]BatchResult, error)
}

type Factory interface {
    Kind() string
    Open(ctx context.Context, config ProviderConfig) (Provider, error)
}

type Ref struct {
    ProviderName string    // empty for anonymous singular
    Key          string
    Optional     bool      // from ?required=false
}

type VersionToken struct {
    Token      string      // provider-side opaque version identifier
    Provenance string      // user-facing pointer (git SHA, audit-log URL)
}

type Registry struct { /* indexed by Kind() */ }

// ProviderConfig is the per-provider TOML subtree (backend-specific
// fields alongside a shared Kind). Parsed into backend-specific structs
// via each Factory.
type ProviderConfig map[string]any

// ProviderSpec is one entry from cfg.Vault after normalization.
type ProviderSpec struct {
    Name   string         // "" for anonymous singular
    Kind   string         // "infisical", "sops", ...
    Config ProviderConfig // backend-specific fields
    Source string         // file path for diagnostics
}

// BatchResult is one entry returned by BatchResolver.
type BatchResult struct {
    Ref   Ref
    Value secret.Value
    Token VersionToken
    Err   error           // per-ref; nil on success
}

// ScrubStderr runs raw provider-CLI stderr through the caller's
// Redactor, also applying the known-fragments deny-list before the
// return. Callers wrap the result in secret.Errorf without further
// interpolation.
func ScrubStderr(ctx context.Context, raw []byte, known ...secret.Value) string

// ParseRef parses a vault:// URI. Lives in internal/vault (not
// internal/config) so the URI grammar stays colocated with the
// Provider machinery. Called from the resolver, not the parser.
func ParseRef(uri string) (Ref, error)

// Sentinel errors for apply.go's remediation formatting (R9, R12, R14).
var (
    ErrKeyNotFound           = errors.New("vault: key not found")
    ErrProviderUnreachable   = errors.New("vault: provider unreachable")
    ErrProviderNameCollision = errors.New("vault: personal overlay cannot replace team-declared provider")
    ErrTeamOnlyLocked        = errors.New("vault: key is locked by team_only")
)
```

**Rationale:** Option 3 matches PRD R1 vocabulary exactly; construction
is factored cleanly into a Registry (stable import surface for other
packages even as v1.1 sops and later 1Password/Vault/Bitwarden are
added); batching is a real perf win for Infisical (one `infisical
export` vs N per-key calls) that sops genuinely doesn't benefit from.
Typed `Ref` pre-parses the URI exactly once at the resolver boundary.
Typed `VersionToken` gives Decision 4 a stable handle for provenance
without forcing backends to encode/decode strings.

Stderr scrubbing lives in each backend's subprocess invocation via a
shared helper `vault.ScrubStderr(stderr, known...)`; every backend's
Resolve must route captured stderr through it before wrapping into
returned errors.

#### Alternatives Considered

- **Option 1** — rejected: no batching affordance; Infisical forced
  into N subprocess calls per apply for a typical workspace with ~20
  secret refs.
- **Option 2** — rejected: Reader/Factory vocabulary isn't in the PRD
  and would force documentation to reconcile names.
- **Option 4** — rejected: mandatory `Init()` forces no-op
  implementations on stateless backends.

### Decision 4: `SourceFingerprint` reduction shape and storage

R15 requires fingerprinting to distinguish user-edited drift from
upstream rotation, with provenance sufficient for a user to answer
"what change caused this?" without re-running apply.

- **Option A — Rollup hash only.** `state.json` stores just the
  SHA-256. Minimal state. Cannot attribute which sub-source changed
  when a mixed-source `.local.env` goes stale. Fails R15's provenance
  requirement.
- **Option B — Rollup hash + tuple list inline.** Store both in
  `state.json`. `niwa status` can name exactly which sub-source
  changed. Linear size in source count; tens of KB for a realistic
  instance.
- **Option C — Rollup + tuple list with cap.** Store tuples up to N
  entries, fall back to rollup-only above. Two-path complexity for a
  problem the numbers don't indicate.
- **Option D — Rollup + separate sidecar for tuples.** Load tuples
  lazily only when status needs attribution. Extra file per state.

#### Chosen: Option B — Rollup hash + tuple list inline

```go
// internal/workspace/state.go
type ManagedFile struct {
    Path              string
    ContentHash       string
    SourceFingerprint string        // SHA-256 rollup
    Sources           []SourceEntry `json:"sources,omitempty"`
}

type SourceEntry struct {
    Kind         string         // "plaintext" | "vault"
    SourceID     string         // file path OR provider-name/key
    VersionToken string         // opaque per-backend; for plaintext = content hash
    Provenance   string         // user-facing (git SHA, audit URL); non-secret
}
```

`InstanceState.SchemaVersion` bumps from 1 to 2 (additive; old states
load with `Sources == nil`, equivalent to zero attribution).

Per-backend provenance:
- **sops** — Token is SHA-256 of the encrypted blob; Provenance is
  the last-modifying commit SHA (`git log -1 --pretty=%H -- <path>`),
  or content hash of encrypted blob when file is not in git.
- **Infisical** — Token is the Infisical secret-version UUID;
  Provenance is the audit-log URL for that version.
- **Plaintext sources** — Token is SHA-256 of source bytes; Provenance
  is empty.

`niwa status` example output for a stale file:
```
.niwa/tsukumogami/.local.env  stale
  changed source: vault://team/github-pat (provider: infisical)
    version: v3 → v4
    audit:   https://app.infisical.com/projects/.../audit/<id>
```

**Rationale:** R15's provenance paragraph is explicit — rollup-only
fails it. The state-size cost of storing tuples is small (linear in
source count, bounded by config structure, not user data). Capping
(Option C) and sidecar splits (Option D) both add two-path complexity
to solve a problem the numbers don't show exists. Inline tuples keep
state atomicity as a single file-write and keep the status code path
simple.

#### Alternatives Considered

- **Option A** — rejected: fails R15 provenance requirement.
- **Option C** — rejected: two-path complexity; no evidence state
  size is a real problem.
- **Option D** — rejected: sidecar file adds a cleanup obligation
  and another atomicity boundary.

### Decision 5: Public-repo guardrail placement and detection

R14/R30 requires blocking apply on plaintext `*.secrets` values when
any configured git remote is a public GitHub repo. Four options for
detection and three options for placement.

**Detection options:**

- **Option 1 — URL pattern match only.** Cheap, offline, flags all
  github.com remotes (can't distinguish public from private at same
  host).
- **Option 2 — URL pattern + GitHub API probe.** Accurate but needs
  network and may hit rate limits.
- **Option 3 — Explicit `public = true` in workspace.toml.** Requires
  team cooperation; violates R14's enumeration requirement.
- **Option 4 — Hybrid.** Pattern as hard block, API probe opt-in.
  Overlaps with existing `--allow-plaintext-secrets` one-shot escape.
- **Option 5 — `gh` CLI probe.** Authenticated, no rate limits, but
  adds a hard runtime dependency.

**Placement options:** inside `internal/config/` (couples config to
git — architect review S-1), inside `internal/workspace/apply.go`
(bundles guardrails with apply logic), new `internal/guardrail/`
(cleanest separation).

#### Chosen: Option 1 — URL pattern match only, in new `internal/guardrail/` package

```go
// internal/guardrail/githubpublic.go
package guardrail

func CheckGitHubPublicRemoteSecrets(
    configDir string,
    cfg *config.WorkspaceConfig,
    allowPlaintextSecrets bool,
) error
```

Entry point runs from `apply.go:runPipeline` after vault resolution
and before merge. Shells out to `git -C <configDir> remote -v`,
regex-matches HTTPS/SSH GitHub URL patterns, walks `[env.secrets]` and
`[claude.env.secrets]` tables for non-`vault://` values.

**Rationale:** Option 1 is the only option cheap enough to run on
every apply (milliseconds, no network) and faithful to R14's enumerate-
all-remotes requirement. The "false positive on private github.com"
concern is addressed by R30's one-shot `--allow-plaintext-secrets`
escape, not by adding network probes. The narrow name
`CheckGitHubPublicRemoteSecrets` (not generic `publicRepoGuard`)
prevents silent pass for non-GitHub hosts — architect review S-1's
concern. New `internal/guardrail/` package keeps `internal/config/`
ignorant of git.

#### Alternatives Considered

- **Option 2 (API probe)** — rejected: breaks offline apply, rate-
  limited, adds authentication complexity for marginal accuracy gain.
- **Option 3 (explicit flag)** — rejected: violates R14's "MUST
  enumerate all remotes" and requires team opt-in.
- **Option 4 (hybrid)** — rejected: overlaps with existing one-shot
  escape hatch.
- **Option 5 (gh CLI)** — rejected: adds a hard runtime dependency
  for low incremental value.

### Decision 6: Shadow detection pipeline integration

R31 requires three diagnostic surfaces (apply stderr, status summary,
`--audit-secrets` column) from one underlying detection.

- **Option 1 — Compute in `MergeGlobalOverride`, return as second
  value.** Modifies merge signature; breaks every merge test.
- **Option 2 — Post-resolve pre-merge visitor walk, persist
  `[]Shadow` to state.json.** Pure function; merge signature
  preserved; status reads from state (stays offline).
- **Option 3 — Track during merge via a side-channel
  `*ShadowTracker`.** Optional tracker parameter; merges behave
  identically if nil. Adds an optional parameter to every merge call.
- **Option 4 — Compute twice (apply and status).** Apply holds
  pre-merge snapshots, status re-loads configs. Violates "status is
  offline" design.

#### Chosen: Option 2 — Post-resolve pre-merge visitor walk, persist to state

```go
// internal/workspace/shadows.go
type Shadow struct {
    Kind         string // "env-var" | "env-secret" | "provider" | ...
    Name         string // key name or provider name
    TeamSource   string // file path of team declaration
    PersonalSource string // file path of personal-overlay declaration
    Layer        string // "personal-overlay"
}

func DetectShadows(team, overlay *config.WorkspaceConfig) []Shadow

// internal/vault/shadows.go
func DetectProviderShadows(teamRegistry, overlayRegistry *ProviderRegistry) []Shadow
```

Pipeline integration:

```
parse → resolve
  → DetectProviderShadows              ← R12 provider case; emit then R12 hard error
  → workspace.ResolveGlobalOverride    ← flatten overlay
  → DetectShadows                      ← env/files case
  → workspace.MergeGlobalOverride      ← unchanged signature
  → persist shadows in state.json
  → stderr diagnostic per shadow
```

`InstanceState.Shadows []Shadow` stored alongside `ManagedFiles`
(same schema-1→2 bump as Decision 4). `niwa status` reads from state
(no re-load). `--audit-secrets` renders a `SHADOWED` column.

**Rationale:** Option 2 preserves `MergeGlobalOverride`'s signature
(no existing tests regress), keeps shadow detection a pure function
over two configs, and persists to state so `niwa status` stays
offline. `Shadow` carries only strings (names, paths, layer labels)
— never a `secret.Value`, so R22 compliance is structural.

#### Alternatives Considered

- **Option 1** — rejected: changes merge signature, breaks existing
  tests.
- **Option 3** — rejected: optional parameter clutters every merge
  call site.
- **Option 4** — rejected: double-compute requires status to load
  configs, violating the offline-status design.

## Decision Outcome

The six decisions compose into a single coherent architecture:

**1. Pipeline shape.** Two new stages (vault resolve + shadow/guardrail
checks) slot between the existing parse and merge stages, in
`apply.go:runPipeline`. The existing merge and materialize stages keep
their signatures; only their leaf field types change to carry typed
`MaybeSecret` values.

**2. Type-system spine.** `config.MaybeSecret` (sum type) carries
either a plain string or a `secret.Value`+`VersionToken` pair from the
resolver through merge and into the materializer. `secret.Value` is
opaque; all leaks are blocked at the type boundary via formatters,
marshallers, and a context-scoped redactor that scrubs error chains
before interpolation.

**3. Provider extensibility.** New `internal/vault` package holds
`Provider` + `Factory` + `Registry` (indexed by Kind). v1 ships an
Infisical backend that uses the optional `BatchResolver` for one-shot
`infisical export`; v1.1 ships a sops backend that does per-file
`sops -d`. Deferred backends (1Password, Vault, Bitwarden, Doppler)
drop in without changes to the interface.

**4. Rotation-vs-drift story.** Each provider returns a `VersionToken
{Token, Provenance}` alongside the `secret.Value`. The materializer
records `(source-id, version-token)` tuples in
`ManagedFile.Sources[]`. `niwa status` compares tuples, reports
`stale`/`drifted`/`ok`, and surfaces `Provenance` for user
investigation — git commit SHA for sops, audit-log URL for Infisical.

**5. Security defenses.** Three guards fire between resolve and
merge, in a fixed order: (a) `vault.DetectProviderShadows` emits
diagnostics then raises R12 hard error on name collision;
(b) `guardrail.CheckGitHubPublicRemoteSecrets` blocks apply on
plaintext secrets in team config when any remote is public GitHub;
(c) `workspace.DetectShadows` emits R31 diagnostics for env/files
shadows. All three complete before cloning or materialization.

**6. State-file schema bump.** `InstanceState.SchemaVersion` moves
from 1 to 2 (additive; old states load fine). New fields:
`ManagedFile.Sources []SourceEntry` (from Decision 4),
`InstanceState.Shadows []Shadow` (from Decision 6). Both fields are
JSON-omitempty.

## Solution Architecture

### Overview

Three new packages + leaf-type changes to the existing config and state
schemas. The existing `apply` orchestrator in
`internal/workspace/apply.go` gains five new stages that slot between
parse and merge. No existing merge or materialize logic is rewritten;
only their input types change.

```
┌─────────────────────────────────────────────────────────────────────┐
│ internal/cli/apply.go                                                │
│   flags: --allow-missing-secrets, --allow-plaintext-secrets           │
└─────────────────────────────────────────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────────┐
│ internal/workspace/apply.go :: runPipeline                           │
│                                                                       │
│   1. config.Load(path)                       → *WorkspaceConfig       │
│   2. config.ParseGlobalConfigOverride(data)  → *GlobalConfigOverride  │
│   3. vault.ResolveWorkspace(ctx, cfg)        → *WorkspaceConfig      ◄─── NEW
│   4. vault.ResolveGlobalOverride(ctx, gco)   → *GlobalConfigOverride ◄─── NEW
│   5. vault.DetectProviderShadows(team,over)  → []Shadow              ◄─── NEW
│   6. (if collision) R12 hard error                                  ◄─── NEW
│   7. guardrail.CheckGitHubPublicRemoteSecrets(configDir, team)      ◄─── NEW
│   8. workspace.ResolveGlobalOverride(…)      → GlobalOverride         │
│   9. workspace.DetectShadows(team, over)     → []Shadow              ◄─── NEW
│  10. workspace.MergeGlobalOverride(…)        → *WorkspaceConfig       │
│  11. workspace.MergeOverrides(…)             → EffectiveConfig        │
│  12. Close all providers (Resolver.CloseAll)                         ◄─── NEW
│  13. emit stderr shadows + persist to state.json                     ◄─── NEW
│  14. materializers → files                                           │
└─────────────────────────────────────────────────────────────────────┘
                               │
                               ▼
                        files on disk (0o600, .local.*)
```

### Components

**New packages:**

| Package | Purpose | Key types |
|---------|---------|-----------|
| `internal/secret` | Opaque secret runtime type | `Value`, `Error`, `Redactor`, `Wrap`, `Errorf` |
| `internal/secret/reveal` | Allow-listed plaintext accessor | `UnsafeReveal(v Value) []byte` |
| `internal/vault` | Provider interface + resolver | `Provider`, `Factory`, `Registry`, `Resolver`, `Ref`, `VersionToken` |
| `internal/vault/infisical` | v1 Infisical backend | `Factory` implementing `BatchResolver` |
| `internal/vault/sops` | v1.1 sops+age backend (stub in v1) | `Factory` |
| `internal/guardrail` | Apply-time hard blocks | `CheckGitHubPublicRemoteSecrets` |

**Modified packages:**

| Package | Changes |
|---------|---------|
| `internal/config` | Add `[vault]`, `[vault.provider]`, `[vault.providers.*]` schema. Split `[env.vars]` from new `[env.secrets]`. Add `.required`/`.recommended`/`.optional` sub-tables. Add `MaybeSecret` leaf type. Add `GlobalOverride.Vault`. Add `workspace.vault_scope`, `vault.team_only`. Parser rejects mixed anon/named provider tables, `vault://` in forbidden contexts, and cross-file provider name references. |
| `internal/workspace` | New files: `shadows.go` (DetectShadows), `resolve.go` (orchestrator helper for steps 3-12). `materialize.go` consumes `MaybeSecret` and reveals via `secret.UnsafeReveal`. `EnvMaterializer`, `SettingsMaterializer`, `FilesMaterializer` switch to `0o600` unconditionally (fixes pre-existing `0o644` bug). `state.go` adds `ManagedFile.Sources []SourceEntry` and `InstanceState.Shadows []Shadow`; `SchemaVersion` bumps 1→2. |
| `internal/cli` | New flags: `--allow-missing-secrets`, `--allow-plaintext-secrets`. New subcommand: `niwa status --audit-secrets`, `niwa status --check-vault`. `niwa create` ensures instance-root `.gitignore` covers `*.local*` (idempotent merge). |

### Key Interfaces

**Secret runtime (Decision 2):**

```go
package secret

type Value struct { /* private bytes */ }

func (v Value) String() string                     // "***"
func (v Value) GoString() string                   // "secret.Value(***)"
func (v Value) Format(s fmt.State, verb rune)      // all verbs -> ***
func (v Value) MarshalJSON() ([]byte, error)       // "\"***\""
func (v Value) MarshalText() ([]byte, error)       // "***"
func (v Value) GobEncode() ([]byte, error)         // refuse
func (v Value) IsEmpty() bool
func (v Value) Origin() Origin                     // provider name, key, token (non-secret)

type Error struct { /* wraps an error; scrubs fragments */ }
type Redactor struct { /* per-apply; holds known secret fragments */ }

func Wrap(err error, values ...Value) error
func Errorf(format string, args ...any) error
func WithRedactor(ctx context.Context, r *Redactor) context.Context
func RedactorFrom(ctx context.Context) *Redactor
```

```go
package reveal  // import: internal/secret/reveal

// UnsafeReveal is the sole plaintext accessor. Linter flags any caller
// outside the allow-list (materializers + vault providers).
func UnsafeReveal(v secret.Value) []byte
```

**Provider interface (Decision 3):**

```go
package vault

type Provider interface {
    Name() string
    Kind() string
    Resolve(ctx context.Context, ref Ref) (secret.Value, VersionToken, error)
    Close() error
}

type BatchResolver interface {
    ResolveBatch(ctx context.Context, refs []Ref) ([]BatchResult, error)
}

type Factory interface {
    Kind() string
    Open(ctx context.Context, cfg ProviderConfig) (Provider, error)
}

type Ref struct {
    ProviderName string    // "" for anonymous singular
    Key          string    // the path after vault://[name]/
    Optional     bool      // ?required=false
}

type VersionToken struct {
    Token      string      // opaque provider-side version ID
    Provenance string      // user-facing: git SHA, audit URL, "" if none
}

type Registry struct { /* indexed by Kind */ }

func (r *Registry) Register(f Factory)
func (r *Registry) Build(ctx context.Context, specs []ProviderSpec) (*Bundle, error)

// Bundle holds opened providers keyed by ProviderName, with Close-all.
type Bundle struct { /* ... */ }

func (b *Bundle) Get(name string) (Provider, error)
func (b *Bundle) CloseAll() error

type Resolver struct { /* walks MaybeSecret fields, calls provider.Resolve */ }

func ResolveWorkspace(ctx context.Context, cfg *config.WorkspaceConfig, opts ResolveOptions) (*config.WorkspaceConfig, error)
func ResolveGlobalOverride(ctx context.Context, gco *config.GlobalConfigOverride, opts ResolveOptions) (*config.GlobalConfigOverride, error)

type ResolveOptions struct {
    AllowMissing bool   // --allow-missing-secrets
}
```

Infisical backend in `internal/vault/infisical/` implements `Factory`
and uses the optional `BatchResolver` via `infisical export --format
json`. Stderr is scrubbed through `vault.ScrubStderr(raw, known...)`
before being wrapped into returned errors.

**Config leaf type (Decision 1):**

```go
package config

// MaybeSecret is a sum type; exactly one of Plain or Secret is set.
type MaybeSecret struct {
    Plain  string          // populated by parser if value doesn't start with vault://
    Secret secret.Value    // populated by resolver after provider.Resolve
    Token  vault.VersionToken // populated by resolver alongside Secret
}

func (m MaybeSecret) IsSecret() bool
func (m MaybeSecret) String() string  // plain text OR *** for secrets (no leak)
```

Zero-value semantics: `MaybeSecret{}` has `Plain == ""` and
`!IsSecret()` — equivalent to "empty non-secret." The parser never
produces a zero value; it either parses a TOML string into
`{Plain: "..."}` or omits the field entirely (map entry absent).

**Resolver auto-wraps `*.secrets` plaintext.** When the resolver
walks an `[env.secrets]` or `[claude.env.secrets]` value that is NOT
a `vault://` URI (a plaintext literal), it MUST still wrap the
plaintext in `secret.Value` before returning. The R14 guardrail
catches this case and blocks apply on public repos, but if the user
opts out via `--allow-plaintext-secrets`, the plaintext still flows
through the pipeline as a `secret.Value` so downstream redaction,
`secret.Error` wrapping, and materializer `0o600` mode still apply.
Authoring a plaintext in `*.secrets` is a commit risk, not a
redaction risk.

`EnvConfig`, `ClaudeEnvConfig`, `SettingsConfig`, `FilesConfig` leaf
string values become `MaybeSecret`. The `[env.vars]` / `[env.secrets]`
split (R33) is a separate classification dimension: both map types
use `MaybeSecret`, but `*.secrets` values are wrapped in `secret.Value`
unconditionally by the resolver even when the plaintext doesn't start
with `vault://` (so a user who writes a plaintext value in `*.secrets`
still gets redaction — the guardrail fires, but if suppressed by
`--allow-plaintext-secrets`, the value is still treated as secret).

**Shadow detection (Decision 6):**

```go
package workspace

type Shadow struct {
    Kind           string // "env-var" | "env-secret" | "files" | "settings"
    Name           string // key name
    TeamSource     string // file path of team declaration
    PersonalSource string // file path of personal-overlay declaration
    Layer          string // "personal-overlay"
}

func DetectShadows(team *config.WorkspaceConfig, overlay *config.GlobalConfigOverride) []Shadow
```

```go
package vault

type ProviderShadow struct {
    Name           string
    TeamSource     string
    PersonalSource string
}

func DetectProviderShadows(team, overlay *Bundle) []ProviderShadow
```

**State schema (Decision 4 + Decision 6):**

```go
package workspace

type SchemaVersion int
const CurrentSchemaVersion SchemaVersion = 2  // was 1

type ManagedFile struct {
    Path              string
    ContentHash       string
    SourceFingerprint string          // SHA-256 rollup (new in v2)
    Sources           []SourceEntry   `json:"sources,omitempty"`
}

type SourceEntry struct {
    Kind         string   // "plaintext" | "vault"
    SourceID     string   // file path OR provider-name/key
    VersionToken string   // opaque per-backend; content-hash for plaintext
    Provenance   string   // user-facing; never a secret
}

type InstanceState struct {
    SchemaVersion SchemaVersion
    ManagedFiles  []ManagedFile
    Shadows       []Shadow `json:"shadows,omitempty"`  // new in v2
    // ... existing fields ...
}
```

**Guardrail (Decision 5):**

```go
package guardrail

func CheckGitHubPublicRemoteSecrets(
    configDir string,
    cfg *config.WorkspaceConfig,
    allowPlaintextSecrets bool,
) error
```

Shells out to `git -C <configDir> remote -v`, matches GitHub URL
patterns, walks team's `[env.secrets]` and `[claude.env.secrets]` for
`MaybeSecret.Plain != ""` entries. If any match and
`allowPlaintextSecrets` is false, returns a structured error listing
offending keys.

### Data Flow

**Happy path for `niwa apply` on a workspace with vault refs:**

1. Parse team `workspace.toml` → `*WorkspaceConfig` with
   `MaybeSecret{Plain: "vault://github-pat"}` in `env.secrets`.
2. Parse personal overlay → `*GlobalConfigOverride` with its own
   `[workspaces.tsukumogami.env.secrets]` mapping.
3. Build provider bundles: team bundle from `cfg.Vault`, personal
   bundle from `gco.Vault`.
4. `DetectProviderShadows(team, personal)` — if both declare a
   provider named `team`, emit stderr shadow then raise R12 error
   and exit.
5. `vault.ResolveWorkspace(ctx, cfg)` — for each `MaybeSecret` with
   `Plain` starting with `vault://`, dispatch to team bundle.
   Replace `Plain` with `Secret` + `Token`. `Redactor` registers each
   resolved byte string as a scrub fragment.
6. `vault.ResolveGlobalOverride(ctx, gco)` — same, against personal
   bundle.
7. `guardrail.CheckGitHubPublicRemoteSecrets` on team config — if
   team's `env.secrets` still has `Plain != ""` entries and any
   remote is public GitHub, error out (unless
   `--allow-plaintext-secrets`).
8. `workspace.ResolveGlobalOverride(gco, scope)` — selects the
   `[workspaces.<scope>]` block.
9. `DetectShadows(team, flattenedOverlay)` — diffs env/files/settings;
   emit stderr diagnostic per shadow.
10. `MergeGlobalOverride(team, flattenedOverlay)` — pure last-
    writer-wins over `MaybeSecret` values. Personal wins per R7;
    `team_only` keys raise R8 error.
11. `MergeOverrides(merged, repoName)` — existing logic, unchanged.
12. `Resolver.CloseAll()` — teardown provider sessions.
13. Persist `Shadows` + `ManagedFiles` with new `Sources[]` to
    `state.json`.
14. Materializers write files at `0o600` with `.local` infix. Each
    materializer reads `MaybeSecret.Plain` or calls
    `secret.UnsafeReveal(m.Secret)` for bytes. `SourceFingerprint`
    is computed from the `Sources[]` tuple list per materialized file.

**Error paths:**

- Provider unreachable (network, auth expired) → resolver returns
  `secret.Errorf("provider %s unreachable: %w", p.Name(), err)`.
  Scrubbed stderr of the CLI is wrapped in the `%w` chain via
  `vault.ScrubStderr` before the wrap — no secret bytes escape.
- Key not found → resolver returns a distinct error type
  `vault.ErrKeyNotFound` so `apply.go` can format the R9 remediation
  pointers.
- R12 collision → emitted after `DetectProviderShadows` as a hard
  error listing the colliding provider name and pointing to per-key
  override syntax.
- R14 guardrail fires → error names offending keys and recommends
  `vault://` migration; `--allow-plaintext-secrets` overrides for
  one apply only (no state write).

### wip/ artifacts

Shadow records and source-fingerprint tuples are persisted to
`state.json` at the end of each apply. No new wip/ artifacts
introduced by this design beyond the standard `implement-doc`
flow.

## Implementation Approach

Walking-skeleton-first: get a vault URI resolved end-to-end with a
trivial provider before touching the materialization or guardrail
machinery. Each phase is one commit / one atomic PR.

### Phase 1: Secret runtime foundation

Build `internal/secret/` with `Value`, `Error`, `Redactor`, `Wrap`,
`Errorf`. Add `internal/secret/reveal/UnsafeReveal`. Ship with the
R22 acceptance tests (formatter coverage, gob refusal, error-wrap
scrub, context redactor).

Deliverables:
- `internal/secret/value.go` with full formatter coverage
- `internal/secret/error.go` with `Error` + `Wrap` + `Errorf` + `Redactor`
- `internal/secret/reveal/reveal.go`
- `internal/secret/value_test.go` + `internal/secret/error_test.go`

### Phase 2: Provider interface + registry + fake backend

Build `internal/vault/` with `Provider`, `Factory`, `Registry`,
`Bundle`, `Ref`, `VersionToken`. Ship a `fake` backend for unit tests
(implements `Factory`, returns pre-configured values). No real
backend yet.

Deliverables:
- `internal/vault/provider.go`, `registry.go`, `ref.go`, `token.go`
- `internal/vault/fake/fake.go` (test-only)
- `internal/vault/scrub.go` (`ScrubStderr` helper)
- Unit tests exercising Registry, Bundle, Resolve, Close

### Phase 3: Config schema additions (no resolver yet)

Extend `internal/config/` to accept `[vault.provider]`,
`[vault.providers.*]`, the `[env.vars]`/`[env.secrets]` split with
`.required`/`.recommended`/`.optional` sub-tables,
`workspace.vault_scope`, and `vault.team_only`. Add `MaybeSecret`
leaf type. Add `GlobalOverride.Vault`. Parser rejects mixed anon/named,
cross-file provider refs, and `vault://` in forbidden contexts. v0.6
configs without vault parse unchanged (backwards compat).

Deliverables:
- `internal/config/config.go` schema extensions
- `internal/config/maybesecret.go`
- `internal/config/config_test.go` + fixtures for accept/reject cases

### Phase 4: Resolver stage

Build `internal/vault/resolver.go` with `ResolveWorkspace` and
`ResolveGlobalOverride`. Wire into `apply.go:runPipeline` between
parse and merge. Use the `fake` backend for integration tests.
Existing merge tests get their one-time fixture update to wrap
plain strings in `MaybeSecret{Plain: ...}`.

Deliverables:
- `internal/vault/resolver.go`
- `internal/workspace/apply.go` edit (insert resolve calls)
- Update `internal/workspace/override_test.go` fixtures
- Integration test: parse → resolve → merge → materialize with fake
  backend carrying a secret

### Phase 5: Infisical backend

Build `internal/vault/infisical/` as a real backend. Shells out to
`infisical export --format json`. Implements `BatchResolver`. Scrubs
stderr via `vault.ScrubStderr` before wrapping errors. Depends on
users having `infisical` CLI installed and authed.

Deliverables:
- `internal/vault/infisical/infisical.go`
- Acceptance test inducing auth-failure stderr with known-secret
  fragment; assert fragment absent from returned error
- README walkthrough for Infisical bootstrap

### Phase 6: Materialization hardening

Modify `EnvMaterializer`, `SettingsMaterializer`, `FilesMaterializer`
to consume `MaybeSecret` and write `0o600` unconditionally (fixes
pre-existing `0o644` bug). Add `.local` infix enforcement. Add
`niwa create` instance-root `.gitignore` maintenance.

**Release-coupling note.** Phases 4 (resolver + `MaybeSecret` leaf
type) and 6 (materializer consumption of `MaybeSecret`) form one
logical release unit — intermediate builds that ship Phase 4 without
Phase 6 would have materializers that can't handle the new field
shape. If the work lands across multiple PRs, the phases between
Phase 4 and Phase 6 MUST either (a) land behind a feature flag that
keeps materializers on the old flat-string path, or (b) ship as a
single merged-and-released unit. The `0o600` bug-fix half of Phase 6
is independent — it can land as a precursor PR alongside Phase 1,
fully decoupled from the vault chain, because `0o644` → `0o600` for
existing non-vault configs is strictly safer.

Deliverables:
- `internal/workspace/materialize.go` edits
- `internal/cli/create.go` `.gitignore` maintenance
- Functional tests covering 0o600, .local, .gitignore scenarios

### Phase 7: Source fingerprint + status

Add `ManagedFile.Sources[]` and `SourceFingerprint`. Update materializers
to record per-source tuples. `niwa status` reports drifted/stale/ok
with per-source attribution. Bump schema 1→2 with backwards-compatible
load.

Deliverables:
- `internal/workspace/state.go` schema bump + migration
- `internal/workspace/materialize.go` fingerprint emission
- `internal/cli/status.go` output formatting
- Functional tests: drift-only, vault-rotated, mixed-source

**State-file write semantics.** Once Phase 7 lands, niwa
UNCONDITIONALLY writes `SchemaVersion: 2`. v1 states load via a
migration shim that zeros `Sources[]` and `Shadows[]`, then niwa
rewrites them as v2 on the next apply. There is no mixed v1/v2 state
— a niwa binary that can read v2 always writes v2. Downgrading to a
pre-Phase-7 binary after a Phase-7 apply would fail to parse
`state.json`; document this in the release notes.

### Phase 8: Shadow detection + diagnostics

Add `workspace.DetectShadows` and `vault.DetectProviderShadows`. Wire
into `apply.go` at steps 5 and 9. Persist `Shadows[]` to state.
`niwa status` reads shadows from state and renders summary line.
`niwa status --audit-secrets` adds SHADOWED column.

Deliverables:
- `internal/workspace/shadows.go`
- `internal/vault/shadows.go`
- `internal/cli/status.go` summary line + audit column
- R22-compliance acceptance test: print Shadow slice, assert no secret
  bytes

### Phase 9: Public-repo guardrail

Build `internal/guardrail/` with `CheckGitHubPublicRemoteSecrets`. Wire
into `apply.go` at step 7. Add `--allow-plaintext-secrets` flag (one-
shot, no state persistence).

Deliverables:
- `internal/guardrail/githubpublic.go`
- `internal/cli/apply.go` flag
- Functional tests: origin-private/upstream-public, both-private, both-public

### Phase 10: CLI surface and migration UX

Add `--allow-missing-secrets`, `?required=false`. Add `niwa status
--audit-secrets` and `niwa status --check-vault` subcommands. Update
`niwa init` to emit vault bootstrap pointer when template declares a
vault.

Deliverables:
- `internal/cli/status.go` new subcommands
- `internal/cli/apply.go` flag plumbing
- `internal/cli/init.go` post-clone message
- README migration walkthrough

### Phase 11: Docs + acceptance checklist verification

Walk every PRD acceptance criterion and confirm a test exists.
Bootstrap walkthrough for Infisical in the niwa docs. Call out the
v1-sops-stub status (interface present, backend arrives in v1.1).

Deliverables:
- `docs/guides/vault-integration.md`
- Test-coverage matrix in PR description

## Security Considerations

This design implements the eleven "never leaks" invariants (R21–R31)
enumerated in the PRD. The threat model and scope boundaries defined
in PRD §"Threat Model" apply unchanged; this section documents how
each invariant is realized, the residual risks niwa does not defend
against (by design), and the small number of forward-looking concerns
implementers must keep in mind.

### Invariant Coverage

| Invariant | Realized by |
|-----------|-------------|
| R21 (no-argv) | Infisical and sops subprocess invocations read auth from provider-CLI env/keychain, never from argv. Confirmed by Phase 5 and Phase 11 acceptance tests. |
| R22 (redact-logs) | `secret.Value` opaque type (Decision 2) with formatters returning `***` for every standard Go emission path (`String`, `GoString`, `Format`, `MarshalJSON`, `MarshalText`, `GobEncode`). `secret.Error` + context-scoped `Redactor` scrub error-chain interpolation including captured provider-CLI stderr via `vault.ScrubStderr`. |
| R23 (no-config-writeback) | Resolver is a pure function `(*WorkspaceConfig) → (*WorkspaceConfig)` returning a new struct; no filesystem write into `configDir`. |
| R24 (file-mode 0o600) | `EnvMaterializer`, `SettingsMaterializer`, `FilesMaterializer` write `0o600` unconditionally (Phase 6). Fixes the pre-existing `0o644` bug. |
| R25 (.local + .gitignore) | Materializer filename convention + `niwa create` idempotent `.gitignore` maintenance. |
| R26 (no CLAUDE.md interpolation) | Parser rejects `vault://` URIs in `[claude.content.*]` at load time. |
| R27 (no status content) | `niwa status` reads `state.json` only; renders `path + status` plus non-secret `Provenance` strings. |
| R28 (no process env publication) | No `os.Setenv` call in any code path. Secrets flow into the materializer's file-write path and nowhere else. |
| R29 (no disk cache) | `Resolver.CloseAll` at pipeline step 12; resolved secrets exist in process memory only for the duration of a single `niwa apply`. |
| R30 (public-repo guardrail) | `guardrail.CheckGitHubPublicRemoteSecrets` at pipeline step 7; one-shot `--allow-plaintext-secrets` flag with no state persistence. |
| R31 (override-visibility) | `DetectShadows` + `DetectProviderShadows` persist shadow records in `state.json`; stderr diagnostic at apply time; `niwa status` summary line; `--audit-secrets` SHADOWED column. |

Additionally, `SourceEntry.VersionToken` and `SourceEntry.Provenance`
fields (Decision 4) are non-secret by contract — the design forbids
backends from deriving these fields from decrypted secret bytes.
`Shadow` struct fields are all strings (names, paths, layer labels);
the type must never gain a `secret.Value`-typed field.

### Explicit Non-Scope

niwa is a developer-tool workspace manager, not a zero-trust vault
client. The following adversaries are out of scope per PRD §"Threat
Model":

- **Malicious same-user processes.** Can read `0o600` files the user
  owns; niwa does not encrypt state or materialized files at rest.
- **Root attackers or compromised kernel.** Out of scope.
- **Physical laptop theft without FDE.** Out of scope.
- **Compromised provider CLI binary** (trojan `infisical` on PATH,
  unsigned `sops` binary, etc.). niwa invokes provider CLIs via
  standard PATH lookup and trusts their stdout output. We do not
  verify binary signatures, pin versions, or lock the subprocess
  PATH.
- **Compromised vault service or credentials.** niwa's security story
  assumes the provider backend is honest and the user's vault
  credentials are uncompromised.

### Explicit In-Scope Defenses

niwa actively prevents the following accidents:

- **Accidental `git commit` of plaintext secrets in a public config
  repo** — R14/R30 guardrail enumerates ALL remotes (not just
  `origin`), regex-matches GitHub HTTPS/SSH URL patterns, and blocks
  apply when `[env.secrets]` or `[claude.env.secrets]` contains a
  non-`vault://` value. Bypass requires explicit one-shot
  `--allow-plaintext-secrets`.
- **Accidental materialization under world-readable permissions** —
  `0o600` is unconditional.
- **Accidental inclusion in CLAUDE.md** — parser-level rejection.
- **Accidental disclosure via logs, stderr, error chains, or
  provider-CLI stderr** — structural via `secret.Value`,
  `secret.Error`, and the `Redactor`.
- **Silent personal-overlay supply-chain attack** — R12 forbids
  personal overlays from replacing team-declared provider NAMES
  (hard error at apply time); R31 surfaces per-key shadowing at
  three diagnostic surfaces so a compromised overlay cannot silently
  redirect individual secrets.

### Guardrail Detection Boundary

The public-repo guardrail uses URL pattern matching, not authenticated
probes. Explicit boundaries:

- **Detects:** `github.com` HTTPS and SSH URLs across all remotes
  reported by `git remote -v`.
- **Does NOT detect:** GitHub Enterprise Server hosts, GitLab,
  Bitbucket, Gitea, self-hosted git at arbitrary hosts. A repo on
  `github.mycorp.com` or any non-`github.com` host will NOT trigger
  the guardrail even if public. Non-GitHub host coverage is tracked
  as deferred in the PRD Out-of-Scope list.
- **No git working tree:** If `git -C <configDir> remote -v` errors
  (no `.git`, missing binary, corrupted refs, symlinked `.git`
  pointing at a no-longer-existent directory), the guardrail emits a
  warning and proceeds. Users extracting a config tarball outside a
  git clone bypass the guardrail by construction; the guardrail's
  purpose is to prevent future commits, which a non-git tree cannot
  perform.

### Redactor Implementation Notes

The `Redactor` scrubs strings by replacing registered fragments with
`***`. Two implementation notes affect correctness (not security per
se), but matter for error-message usability:

- **Minimum fragment length.** Short secrets (< 6 bytes) have high
  collision rates with ordinary English/log text. The `Redactor`
  MUST skip registering fragments shorter than a safe threshold
  (6 bytes) and MUST NOT apply substring matching to such
  fragments. Secrets that short must be rejected at resolution time
  with a hard error.
- **Fragment ordering.** Scrub longest fragments first to avoid a
  substring of fragment A shadowing fragment B.
- **Whole-token matching.** Consider word-boundary or
  base64/hex-alphabet-boundary matching to prevent false positives
  in user-facing error text. This is a quality bar for the
  Redactor's acceptance tests.

### Forward-Looking: Explicit Subprocess Env

The PRD deferred `INV-EXPLICIT-SUBPROCESS-ENV` because niwa today
carries no secret-bearing env. This design changes that: the vault
resolver holds `secret.Value`s in process memory during apply. The
subprocesses niwa spawns during that window (provider CLIs, `git
remote -v`) inherit `os.Environ()` by default. Three invariants
implementers MUST honor:

1. **No `os.Setenv(secret)`.** Ever. Secret bytes never enter the
   niwa process's own environment. (R28.)
2. **No injection of secrets into subprocess env.** Provider CLIs
   obtain their auth from the user's shell env or keychain, not
   from niwa-built env. niwa does not forward `secret.Value` bytes
   into `exec.Cmd.Env`.
3. **Inherited env is passed through unchanged.** `exec.Cmd.Env =
   nil` (inherit) is the default; do not filter, do not extend with
   secrets.

A future feature that spawns Claude Code or hook scripts with
materialized secrets will need to revisit this section and
potentially promote these points to a formal invariant with
acceptance tests.

### Forward-Looking: Backend `ProviderConfig` Safety

The v1.1 sops backend and any future backend that reads identity or
key material from a filesystem path MUST NOT accept that path from
team-declared provider config. Identity file paths belong in
personal-overlay config or environment variables only. This prevents
a malicious team config from redirecting sops at an attacker-chosen
path on the user's machine. When the sops backend lands in v1.1, its
`ProviderConfig` schema MUST reject `identity_file` / `key_file` /
equivalent fields from team-layer sources.

### Residual Risks Accepted

- Provider CLI binary integrity (user responsibility).
- Same-user process memory inspection (out of scope per threat
  model; covered by OS user isolation).
- GitHub Enterprise public repos (deferred; same bucket as GitLab,
  Bitbucket).
- Users who bypass the guardrail with `--allow-plaintext-secrets`
  and then `git push` (the flag is explicit, one-shot, loud; this is
  user agency, not a niwa bug).

## Consequences

### Positive

- **Team configs become publishable.** Moving `tsukumogami/dot-niwa`
  from private to public is a schema-level exercise once secrets are
  in a vault. The guardrail prevents regressions.
- **Per-org personal scoping works without ceremony.** US-3 is
  satisfied end-to-end with no custom user code: a developer with
  separate PATs for `tsukumogami` and `codespar` writes one personal
  overlay, niwa picks the right one automatically.
- **Zero new Go dependencies.** The subprocess-based provider model
  means niwa's binary size and attack surface don't grow.
- **Pre-existing `0o644` bug fixed.** Materialized env and settings
  files become `0o600` across the board, including paths that don't
  use vault (tighter-by-default without user action).
- **Rotation investigation is cheap.** `niwa status` tells a user
  "what rotated and when" via git commit SHA or audit-log URL,
  without re-running apply.
- **Override-visibility makes supply-chain attacks visible.** A
  compromised personal overlay silently replacing a team provider
  would otherwise be invisible; R31's stderr + status integration
  surfaces it.
- **`secret.Value` discipline is enforceable.** One type, one grep-
  able plaintext-access point, one error-wrapping idiom. Future
  niwa features that touch secrets inherit the redaction story
  without custom work.

### Negative

- **New subprocess dependency on `git`.** The public-repo guardrail
  shells out to `git remote -v`. Users who have cloned a workspace
  outside a git working tree (e.g., extracted from a tarball) can't
  use the guardrail. Addressed by: emitting a warning "no git
  remotes detected; guardrail skipped" and trusting `--allow-
  plaintext-secrets` path.
- **VersionToken shape leaks provider vocabulary.** `Provenance`
  format varies per backend (commit SHA, URL, empty). Tooling that
  wants to cross-reference provenance has to know the backend. Lives
  with it — centralizing on one shape would force least-common-
  denominator output.
- **Subprocess-per-key latency.** Calling `infisical secrets get`
  N times per apply would be slow. Mitigated by the optional
  `BatchResolver` — Infisical uses `infisical export` once per
  project. sops has no batching benefit but also has no network.
- **Redactor-in-context is a mild anti-pattern.** Go stdlib
  discourages context values for non-request-scoped data. The
  per-apply redactor IS request-scoped in the HTTP-server sense, but
  teams used to strict context-value hygiene may push back.
  Addressed by: documentation explaining the request-scoped
  semantics; linter (Option 4, post-v1) would catch misuse.
- **Linter-as-hardening is deferred.** The `go/analysis` linter that
  would trap every `fmt.Errorf("...: %w", err)` touching a `Value`
  outside `secret.Wrap`/`Errorf` is v1.1 scope. v1 relies on runtime
  redaction + acceptance tests, which is weaker than compile-time
  rejection.
- **`MaybeSecret` leaf type ripples through merge tests.** Every
  existing merge test gains a one-time fixture migration (wrap
  plain strings). Non-trivial churn in `override_test.go`.
- **State file schema bump.** `state.json` schema-version 1 → 2.
  Additive, backwards-compatible load (old states parse with empty
  `Sources`/`Shadows`), but any external tooling that reads
  `state.json` has to tolerate the new fields.

### Mitigations

- **`--allow-plaintext-secrets` is strictly one-shot.** The guardrail
  regression path is documented and re-triggers on every apply;
  CI catches stale plaintext within one cycle after a vault rotation
  makes it obsolete.
- **`vault.ScrubStderr` is shared across all backends.** Stderr
  scrubbing is not reinvented per backend, so a new backend author
  can't forget it.
- **`UnsafeReveal` has a grep-able name + lives in a sub-package.**
  Code review for "did this introduce a new plaintext read site?"
  is a five-line grep. A linter can enforce the allow-list without
  changing runtime.
- **Test layering is preserved.** Config parse tests stay pure TOML;
  vault resolver tests use a fake backend; merge tests use
  `MaybeSecret` fixtures. No test category requires the others to
  set up.
- **Backwards-compat path for v0.6 configs.** The parser accepts
  v0.6 `[env.vars]` flat maps; the resolver no-ops on files without
  `[vault]`. A user who doesn't opt into vault sees zero change
  except the `0o600` file-mode (strictly-safer).
