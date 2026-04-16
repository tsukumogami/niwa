---
status: Proposed
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
  Placeholder — populated by Phase 4.
rationale: |
  Placeholder — populated by Phase 4.
---

# DESIGN: Vault Integration

## Status

Proposed

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

Populated by Phase 4.

## Implementation Approach

Populated by Phase 4.

## Security Considerations

Populated by Phase 5.

## Consequences

Populated by Phase 6.
