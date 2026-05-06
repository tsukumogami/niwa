---
status: Planned
upstream: docs/prds/PRD-machine-identity-vault-sync.md
problem: |
  niwa's apply pipeline reads machine-identity credentials from a
  per-machine local file (`~/.config/niwa/provider-auth.toml`) and
  injects them into vault provider configs by `(kind, project)`
  match. There's no way to source credentials from the user's
  personal vault, so developers maintain identical local files on
  every laptop, copy-paste credentials between machines, and update
  every laptop on rotation. The PRD specifies adding the personal
  vault as a second source layer with local-file precedence, lazy
  per-(kind, project) fetch, no on-disk cache, and an offline audit
  surface.
decision: |
  Extend the existing `LoadProviderAuth` -> `injectProviderTokens`
  flow with a thin credential-pool abstraction that joins the local
  file with a lazy vault-backed loader. Open the personal vault
  provider once via the existing `vault.Registry.Build` pipeline
  applied to the `[global.vault.*]` registry from the personal
  overlay, gated by a parse-time check that enforces R9 (the personal
  vault's `(kind, project)` cannot appear in the credential pool).
  Persist a per-`(kind, project)` source record in a new
  `state.json` `auth_sources` map so `niwa status --audit-auth`
  works fully offline. Apply-time stderr emission and parse-time
  validations reuse the existing `Reporter` and config-validation
  paths.
rationale: |
  The PRD pins precedence (local > vault > cli-session), schema
  (TOML body at `/niwa/provider-auth/<kind>/<project>`), and most
  user-facing surfaces. The remaining design questions are pipeline
  shape, provider-lifecycle, and persistence — all narrow given the
  existing apply.go layout. Reusing `vault.Registry.Build` for the
  personal vault keeps the auth path identical to today's overlay
  vault open (CLI-session by default, multi-org via the existing
  provider-auth.toml entry that ISN'T self-referential per R9). A
  `CredentialPool` interface with two implementations (file +
  lazy-vault) keeps the existing matching code (`MatchProviderAuth`)
  unchanged. Persistence into `state.json` keeps the offline-audit
  contract (R11) without introducing new files (R17).
---

# DESIGN: Machine Identity Vault Sync

## Status

Planned

## Context and Problem Statement

Today's apply pipeline (`internal/workspace/apply.go`) authenticates
vault providers in two coupled steps:

1. **`LoadProviderAuth(niwaConfigDir)`** at apply.go:493 reads
   `~/.config/niwa/provider-auth.toml` once, returning a
   `[]ProviderAuthEntry` slice. The call is gated by an
   `if niwaConfigDir, err := NiwaConfigDir(); err == nil {` block at
   apply.go:492.
2. **`injectProviderTokens(ctx, entries, registry)`** at apply.go:592,
   742, and 746 walks each `VaultRegistry` (workspace overlay, team
   config, personal-overlay-global). For each provider spec, it
   calls `MatchProviderAuth(spec, entries)` (providerauth.go:102) to
   find a matching entry by `(kind, project)`, authenticates against
   the backend if matched, and mutates the spec's config in place,
   setting `ProviderConfig["token"]`. The injection lives in
   `internal/workspace/providerauth.go:132`.

The injection mechanism stops at the local file. There's no
secondary lookup against the personal-overlay's vault provider, even
though that provider is *itself* already authenticated and reachable
at apply.go:746.

The PRD requires:

- A vault-sourced credential layer that fills in `(kind, project)`
  pairs the local file doesn't cover (R3).
- Local file wins on conflict, with vault entries recorded as
  `FALLBACK` in audit but never used (R4, R11).
- Lazy fetch — vault entries for unused `(kind, project)` pairs MUST
  NOT be requested (R6).
- No on-disk cache, no token persistence (R6, R17).
- A parse-time check that the personal vault doesn't try to
  bootstrap itself from the credential pool (R9).
- Offline `niwa status --audit-auth` (R11), reading from `state.json`.
- Apply-time stderr lines per `(kind, project)` sourced from the
  vault, with a fallback line when local overrides vault (R12).

System boundaries the design must respect:

- **Existing matching code** (`MatchProviderAuth`,
  `injectProviderTokens`) is correct and should not be rewritten.
- **`vault.Registry.Build`**
  (`internal/vault/registry.go:90`) is the canonical factory. Its
  signature is `Build(ctx, []ProviderSpec) (*Bundle, error)` — it
  takes a slice of specs, returns a `*Bundle`, and opens each
  provider via the registered `factory.Open(ctx, spec.Config)`.
  `factory.Open` does NOT accept auth entries; token injection
  happens **before** `Build` is called by mutating
  `spec.Config["token"]` via `injectProviderTokens` (see flow
  above). To open a single provider, the design calls `Build` with
  a one-element slice and retrieves the result via `bundle.Get(name)`.
- **`vault.Provider.Resolve(ctx, ref)`**
  (`internal/vault/provider.go:54`) is single-key only and returns
  `(secret.Value, VersionToken, error)`. There is no list/scan API.
  The interface defines two sentinel errors —
  `vault.ErrKeyNotFound` and `vault.ErrProviderUnreachable` — that
  the design relies on to discriminate "key absent" (R13.3) from
  "vault unreachable" (R13.1).
- **`secret.Value`** (`internal/secret/value.go:67`) is a struct
  that intentionally does not expose its plaintext bytes. Plaintext
  access is gated through `internal/secret/reveal.UnsafeReveal`,
  which carries an explicit warning ("DO NOT import this package
  from new code without explicit review"). This design adds a new
  legitimate caller for credential-pool body parsing; the
  justification and reviewer guidance are spelled out in
  `parseProviderAuthBody` below.
- **`state.json`** is the existing per-workspace persistence file
  (`internal/workspace/state.go`). The schema-version constant is
  `SchemaVersion` (currently `3`); the persisted struct is
  `InstanceState`. Adding a top-level field is the established way
  to add per-apply records (see how `ConfigSource` arrived in v3).

## Decision Drivers

- **Reuse existing pipeline shape (PRD R3, R6).** The
  credential-injection path is small and well-tested. The new layer
  must extend it without rewriting.
- **No new on-disk files (PRD R17).** Audit persistence must use
  `state.json`. No cache file, no merged credential snapshot, no
  token store.
- **Lazy fetch (PRD R6).** Vault calls happen only when a
  `(kind, project)` is actually used. The design must thread "needed
  pairs" through to avoid eager enumeration.
- **R9 chicken-and-egg validation must be parse-time.** The personal
  vault must not be opened at all if it would self-bootstrap. This
  pushes validation upstream of `vault.Registry.Build` for the
  personal vault.
- **Backward compatibility byte-identical for non-opting-in users
  (PRD R15).** Every change must be gated behind the
  `[global.machine_identities]` opt-in. Code paths exercised by
  non-opting-in users must be unchanged.
- **Diagnostic vocabulary discipline (PRD R5).** "Augmentation" /
  "fallback" everywhere, never "shadow" or "override."
- **Match the existing offline-audit pattern (PRD R11, by analogy
  with `--audit-secrets`).** Audit reads `state.json`, not the live
  vault.

## Considered Options

### Decision 1: Pipeline integration point

The new credential layer must hook into `apply.go` somewhere between
`LoadProviderAuth` (line 492) and the three `injectProviderTokens`
calls (lines 592, 742, 746). Three placements considered:

- **Option A — Replace `LoadProviderAuth` with a richer
  `LoadCredentialPool`** that returns a unified pool combining file
  entries and a lazy vault loader. `injectProviderTokens` accepts
  the pool instead of `[]ProviderAuthEntry`.
- **Option B — Keep `LoadProviderAuth` unchanged.** Add a separate
  `OpenCredentialSyncSource(ctx, overlay) (*VaultCredSource, error)`
  call that returns a lazy vault loader. Pass *both* the file
  entries and the vault loader through to a new
  `injectProviderTokens(ctx, file, vault, registry)` signature.
- **Option C — Lazy at the match site.** Modify `MatchProviderAuth`
  to consult the vault when the file lookup misses. This pushes the
  vault dependency deep into the matcher and changes its signature.

#### Chosen: Option A — `LoadCredentialPool`

Introduce a `CredentialPool` type that owns both layers. The file
layer is loaded eagerly (small file, parse it once). The vault layer
is a lazy loader: it holds a reference to an opened
`vault.Provider`, the conventional path prefix
(`/niwa/provider-auth/<kind>/`), and a `version` validator. Lookups
go through the pool's
`Lookup(ctx, kind, project) (*ProviderAuthEntry, AuditRecord, error)`
method, which returns:

- The local-file entry if present (always wins per R4), with the
  audit record's `Fallback` set when the vault also had an entry.
- Otherwise, lazily fetches from the vault, parses the TOML body,
  validates the `version` field (R8), and returns the result.
- Otherwise, returns `nil` for the entry plus an audit record
  whose `Source` is tentatively `cli-session` (the caller may
  upgrade to `none` if backend authentication ultimately fails).

`Lookup` returns the winning entry plus an `AuditRecord` that
captures both layers' presence (the audit record's `Fallback`
field is set when the file wins but the vault also had an entry).
This collapses what could have been two separate methods
("which entry wins" and "what was in the other layer") into a
single call that walks the layers once.

```go
type AuditRecord struct {
    Kind     string
    Project  string
    Source   Source  // local-file | vault | cli-session | none
    Provider string  // for vault source: provider name (empty for anonymous)
    Fallback string  // "vault:<name>" when local overrides vault; "" otherwise
}
```

`injectProviderTokens` is updated to take the pool, call `Lookup`
once per spec, persist the returned `AuditRecord` into the pool's
audit log (see Decision 3), and authenticate using the winning
entry.

**Rationale**: Option A keeps the matching/auth flow inside one
place (`injectProviderTokens` plus the pool's lazy logic) and avoids
threading two parallel data structures (file + vault loader) through
three call sites. Option C is rejected because pushing vault
dependencies into `MatchProviderAuth` couples a previously pure
function to I/O. Option B works but creates a wider call signature
and forces every call site to combine both layers — exactly the work
the pool encapsulates.

### Decision 2: Personal vault provider lifecycle

The credential-sync vault provider needs to be opened, used, and
closed. R9 forbids it from authenticating via the credential pool.
Three approaches considered:

- **Option A — Reuse the existing personal-overlay
  `vault.Bundle`.** The personal overlay's vault providers are
  already opened in apply.go for env resolution. The credential-sync
  feature could grab a reference to the same `Bundle` and ask it to
  resolve `vault://...` URIs at the conventional path.
- **Option B — Open a fresh `vault.Provider` instance dedicated to
  credential-sync** via a new `OpenCredentialSyncProvider(ctx,
  overlay) (vault.Provider, error)`. Reuses
  `vault.Registry.Build` internals but with a single-spec input.
- **Option C — Defer to backend-specific code.** Skip the
  abstraction; let the Infisical backend expose a side-channel
  loader.

#### Chosen: Option B — Dedicated provider opened via the existing pipeline

A new helper, `openCredentialSyncProvider(ctx, overlay,
machineIdsConfig) (vault.Provider, vault.ProviderSpec, error)`,
locates the chosen provider from the overlay's `[global.vault.*]`
registry (the spec already declared in the personal overlay) and
opens it via the existing `vault.Registry.Build` factory. The
provider is opened separately from the personal-overlay env
resolution `Bundle` for two reasons:

1. **Lifecycle clarity.** Credential-sync needs the provider open
   *before* `injectProviderTokens` runs (to populate the pool that
   injection consults). The personal-overlay env-resolution
   `Bundle` is built later, after token injection populates the
   pool. Coupling the two would create a build-order tangle.
2. **Auth path symmetry.** The credential-sync provider is opened
   via the same `vault.Registry.Build` path the env-resolution
   `Bundle` uses. The provider's auth happens via the existing
   token-injection mechanism applied to the *credential-sync
   provider's spec only* — which, by R9, means it falls through to
   CLI session (the credential pool can't have an entry for this
   provider, since R9 enforces no overlap).

In code, the open looks like:

```go
spec, err := pickCredentialSyncSpec(globalOverride, machineIds)
if err != nil { return err }
// R9 check: spec's (kind, project) MUST NOT appear in the credential pool's keys.
if poolHasKey(file, spec.Kind, spec.Config["project"]) {
    return chickenAndEggError(spec)
}
// Open via existing factory. We deliberately do NOT call
// injectProviderTokens for this spec — the token-injection
// mechanism is what would re-introduce the R9 cycle. Skipping it
// means spec.Config["token"] stays unset, and the backend's
// factory.Open falls through to its CLI-session auth path
// (Infisical: the universal-auth login uses the active
// `infisical login` session). This is the structural enforcement
// of R9: there is no path by which the credential-sync provider
// can authenticate from the credential pool.
bundle, err := vault.DefaultRegistry.Build(ctx, []vault.ProviderSpec{spec})
if err != nil { return err }
defer bundle.CloseAll()
prov, err := bundle.Get(spec.Name)
if err != nil { return err }
```

`Registry.Build` returns `*vault.Bundle` rather than a bare
`vault.Provider`; the credential-sync provider's lifetime is owned
by this single-element bundle, and `defer bundle.CloseAll()` is
sufficient. There is no `vault.OpenSingle` helper to author —
`Build` with a one-element slice is the existing API.

**Rationale**: Option A is appealing for code reuse but couples the
two `Bundle` lifecycles. Option C scatters credential-sync logic
across backends and breaks the abstraction the PRD's R7 schema
contract relies on (one path convention applies to every backend
that ever ships).

### Decision 3: State persistence schema for offline audit

R11 requires `niwa status --audit-auth` to work fully offline. The
audit reads from persisted state. Three options for *where and how*
to persist:

The persisted struct is `InstanceState` in
`internal/workspace/state.go`, with the schema version held by the
`SchemaVersion` field (`schema_version` JSON tag). The current
constant is `SchemaVersion = 3` (`internal/workspace/state.go:43`).

- **Option A — New top-level `auth_sources` field on
  `InstanceState`** keyed by `"<kind>/<project>"`, value is
  `{source, fallback}`.
- **Option B — Annotate existing per-provider state** records with
  a `source` field. State is already tracked per provider in
  `InstanceState`; this would extend that.
- **Option C — Separate `auth_state.json` file** alongside
  `state.json`.

#### Chosen: Option A — `auth_sources` map in `state.json`

Add a top-level `auth_sources` field on `InstanceState`. The
JSON key is `auth_sources`; the schema-version field (already
present) bumps to `4`:

```json
{
  "schema_version": 4,
  ...
  "auth_sources": {
    "infisical/550e8400-...": {
      "source": "local-file",
      "fallback": "vault:personal"
    },
    "infisical/660f9511-...": {
      "source": "vault:personal",
      "fallback": ""
    },
    "infisical/770a0622-...": {
      "source": "cli-session",
      "fallback": ""
    },
    "infisical/880b1733-...": {
      "source": "none",
      "fallback": ""
    }
  }
}
```

The map is repopulated atomically on every successful apply
invocation: `apply.go` collects `(kind, project) -> AuditRecord`
entries as `injectProviderTokens` runs and writes the map at
state-save time. Failed applies leave the previous map intact (the
on-disk state isn't written when apply errors before state save).

**Rationale**: Option A has the smallest blast radius — one new map
in a file that already contains per-apply state. Option B requires
threading a `source` field through every per-provider state record
and risks breaking on `state.json` migrations. Option C creates a
new on-disk surface, which violates R17.

State-version bump: this design adds `auth_sources` at the top
level of `InstanceState`. The `SchemaVersion` constant in
`internal/workspace/state.go` bumps from `3` to `4`. The migration
shim in `LoadState` initializes `AuthSources` to an empty map when
reading a v3 file; the empty map renders an empty `--audit-auth`
table on the first read after upgrade, and the next apply
populates it. The migration is forward-only — a niwa binary that
predates this feature reading a v4 state file fails the existing
forward-version check (`internal/workspace/state.go:246`,
`schema_version > SchemaVersion`). This is the same downgrade
behavior as the v2→v3 bump; it is called out as a Known
Limitation in the PRD so users understand pinning a niwa version
post-feature is one-way.

### Decision 4: Validation timing and placement

R2 (unknown-provider name) and R9 (chicken-and-egg) must surface
before any vault call. Two placements considered:

- **Option A — Extend `ParseGlobalConfigOverride`** in
  `internal/config/config.go:450` so the R2 check runs alongside
  the existing single-file validations (`validateGlobalOverridePaths`,
  `cfg.Global.Vault.Validate`) that already run there. The
  diagnostic wording mirrors the precedent at
  `validate_vault_refs.go:285-293`.
- **Option B — Add a new pre-fetch validation pass** in `apply.go`
  that runs after the global overlay is loaded but before the
  credential-sync provider is opened.

#### Chosen: Option A for R2, Option B for R9

R2 (unknown provider name) is a **purely config-local** check: does
the named provider exist in the same file's `[global.vault.*]`
declarations? `validate_vault_refs.go` is the obvious-looking home,
but that file's existing entry points operate on
`*WorkspaceConfig` and reach the validator only as part of
`ValidateWorkspace`. R2's input is a `GlobalConfigOverride`, parsed
by `ParseGlobalConfigOverride` in `internal/config/config.go:450`.
Add the check there, immediately after
`cfg.Global.Vault.Validate("global overlay")` (line 458), so any
syntactically-parseable global override either has a valid
`[global.machine_identities]` block or fails before
`ParseGlobalConfigOverride` returns. The diagnostic wording mirrors
the precedent at `validate_vault_refs.go:285-293`. (We could also
re-export the existing diagnostic helper for shared wording; both
files emit very similar error text.)

R9 (chicken-and-egg) is a **cross-source** check that needs the
local credential file plus the resolved set of vault provider specs
from BOTH the workspace and the personal overlay (because either
could declare a provider with the (kind, project) the credential-sync
vault would self-reference). This information isn't available at
config-parse time for the personal overlay alone; it's known after
all configs are loaded.

So R9 lives in a new function `validateCredentialSyncBootstrap(ctx,
file, vaultSpecs, syncProviderSpec) error` called early in `apply.go`
after `LoadProviderAuth` and after the workspace + overlay configs
are merged but before the credential-sync provider is opened. The
function:

1. Computes the set of `(kind, project)` pairs in `file` (local
   entries).
2. Computes the set of `(kind, project)` pairs in `vaultSpecs`
   (every vault provider every layer wants to authenticate).
3. Errors if `syncProviderSpec.(kind, project)` is in either set
   (per the R9 examples in the PRD).

Both checks return the structured diagnostics from PRD R2 and R9
with the precise wording.

**Rationale**: putting R9 in the existing config-validator would
require the validator to see workspace+overlay merged state, which
violates its current single-config-file contract. Putting R2 outside
the validator would scatter validation logic. Each rule lives in its
narrowest natural home.

## Decision Outcome

The four decisions compose into an additive feature gated on
`[global.machine_identities]`. There is one cross-cutting refactor
the original framing missed and that this revision now calls out
explicitly: the workspace-overlay layer's `injectProviderTokens`
call at `apply.go:592` runs **before** the global override is
parsed today (the parse lives at `apply.go:713-726`). For a
vault-sourced credential to authenticate the overlay's
`[vault.provider]`, the credential-sync provider must be open
before that injection. Therefore the global-override parse moves
earlier in the pipeline (Step 0.4 below), ahead of the workspace
overlay sync at 0.5. This is a behavior-preserving reorder — the
parse only depends on `a.GlobalConfigDir` and `opts.skipGlobal`,
both available at the top of the pipeline — but it is a real
refactor with its own test surface.

The components changed:

1. **Config parser (`internal/config/config.go:450`,
   `ParseGlobalConfigOverride`)** gains R2: when
   `[global.machine_identities]` is present, validate `from`
   against declared `[global.vault.*]` provider names. The check
   sits next to the existing `cfg.Global.Vault.Validate(...)`
   call so any parsed override either has a valid
   `[global.machine_identities]` block or returns an error
   before the override leaves `ParseGlobalConfigOverride`.
2. **Apply (`internal/workspace/apply.go`)** gains:
   - The global-override parse moves earlier in the pipeline
     (new Step 0.4) so opt-in detection can happen before the
     overlay's `injectProviderTokens` at the existing line 592.
   - A pre-fetch R9 check
     (`validateCredentialSyncBootstrap`).
   - A credential-sync provider open
     (`openCredentialSyncProvider`) gated on opt-in. Internally
     uses `vault.DefaultRegistry.Build(ctx, []ProviderSpec{spec})`
     and `bundle.Get(spec.Name)`; deliberately skips
     `injectProviderTokens` for this single spec so authentication
     falls through to CLI session — the structural R9 enforcement.
   - A `CredentialPool` constructed from the file (existing) plus
     the opened provider (new).
   - `injectProviderTokens` updated to take the pool and record
     audit info. The existing call sites at `apply.go:592, 742,
     746` keep their ordering relative to the overlay/team/global
     vault registries.
   - Per-pair stderr emission via the existing
     `internal/workspace/reporter.go` `Reporter`.
3. **State (`internal/workspace/state.go`)** gains an
   `AuthSources` field on `InstanceState` and a `v3 -> v4` schema
   bump. The migration shim in `LoadState` initializes the field
   to an empty map for v3 files.
4. **Status (`internal/cli/status.go`)** gains a `--audit-auth`
   flag that reads `AuthSources` from `state.json` and renders the
   text-table format from PRD R11.
5. **Infisical backend (`internal/vault/infisical/`)**: no changes.
   The credential-sync provider uses the same `Resolve(ctx, ref)`
   API workspace env-resolution already uses, and relies on the
   existing `vault.ErrKeyNotFound` and `vault.ErrProviderUnreachable`
   sentinel errors to discriminate "key absent" (R13.3) from "vault
   unreachable" (R13.1).

Single-org and non-opting-in users see byte-identical behavior. The
`CredentialPool` constructor returns a file-only pool (with a nil
vault loader) when `[global.machine_identities]` is absent. The
global-override parse reorder is invisible to non-opting-in users
because the parsed value is consumed downstream identically; only
the position changes.

## Solution Architecture

### Overview

```
Apply pipeline (chronological; `<NEW>` marks new or reordered steps):

0.1 Parse base WorkspaceConfig                    (existing)
0.2 LoadProviderAuth(~/.config/niwa/provider-auth.toml)  (existing,
    moved from current line 491-498 to keep ordering with steps below)
0.3 Refresh personal-overlay snapshot, then parse GlobalConfigOverride
                                                  <NEW position>
    a. EnsureConfigSnapshotWithStatus           (existing Step 2a, hoisted)
       └─> swaps the global-config dir on upstream drift; emits
       └─> "syncing config..." Status (TTY-only no-op on non-TTY)
       └─> and the optional R28 conversion notice via Reporter.Log.
    b. read + ParseGlobalConfigOverride          (existing parse, hoisted)
       └─> runs R2 internally.

    The sync (a) and parse (b) are hoisted together as a single
    phase, in their original sync-then-parse order. Hoisting only
    the parse without the sync would change the parsed value for
    users whose global config tracks a remote (drift would be
    picked up one apply later than today). Both steps' inputs
    (a.GlobalConfigDir, opts.skipGlobal, a.GitHubClient, a.Reporter,
    opts.disclosedNotices) are Applier or pipelineOpts fields
    available at pipeline entry, so the move is mechanically safe.

    Why hoisted: workspace-overlay token injection at Step 0.6
    needs to know whether [global.machine_identities] is opted in,
    which means the parsed globalOverride must be available before
    Step 0.5 runs.
0.4 If [global.machine_identities] opted in:      <NEW>
    a. validateCredentialSyncBootstrap(file, allVaultSpecs, syncSpec)  (R9)
       — needs the resolved vault specs from workspace + overlay +
       global override. Workspace specs are available; overlay specs
       require the overlay parse (step 0.6) — see "Lazy R9
       evaluation" below for the resolution.
    b. openCredentialSyncProvider(ctx, globalOverride, machineIds)
       └─> Build(ctx, []ProviderSpec{spec}); bundle.Get(spec.Name)
       └─> Skips injectProviderTokens for this spec; CLI-session auth
                                                  (structural R9 enforcement)
    c. CredentialPool = NewPool(file, vaultLoader)
   Else:
    c. CredentialPool = NewPool(file, nil)  (vault loader disabled)

0.5 Workspace overlay sync + parse                (existing)
    └─> if overlayDir != "":
         injectProviderTokens(ctx, pool, overlay.Vault)  (existing
         site at apply.go:592, signature changed to take pool)
         └─> pool.Lookup may now reach into the vault loader

0.6 Build overlay vault bundle                    (existing)

1.  Parse globalOverride continues to feed downstream uses
    (CheckVaultScopeAmbiguity, etc.)               (existing)

2.  injectProviderTokens(ctx, pool, cfg.Vault)     (existing site
    at apply.go:742, signature changed to take pool)
    injectProviderTokens(ctx, pool, globalOverride.Global.Vault)
                                                   (existing site at 746)

3.  BuildBundle(team, personal-overlay)            (existing)

4.  Resolve workspace + overlay env, materialize files  (existing)

5.  State save: state.AuthSources = pool.AuditLog.AsMap()  <NEW>
    InstanceState bumps to schema_version 4.

6.  niwa status --audit-auth (separate command, separate process):
    reads state.json, renders text table, sets exit code per R11.
```

**Lazy R9 evaluation.** R9 requires that the credential-sync
provider's `(kind, project)` does not appear in the credential pool
or in any other vault registry's specs. The full set of
"any other vault registry" specs is only available after the
workspace overlay parse (step 0.5). Two-stage resolution:

- **Pre-overlay (step 0.4a)**: validate against the local file
  entries plus the global override's own vault specs. Catches the
  most common chicken-and-egg cases (overlap between the
  credential-sync provider and the local file or other personal
  overlay providers).
- **Post-overlay (step 0.5)**: re-validate against the overlay's
  vault specs once they are parsed. If the credential-sync
  provider's `(kind, project)` overlaps an overlay-declared spec,
  fail with the same R9 diagnostic. The credential-sync provider
  has been opened by this point but no `Resolve` call has run, so
  the provider can be `Close`'d cleanly.

Pre-overlay catches the typical mistake; post-overlay closes the
hole. Both produce the PRD R9 wording.

### Type definitions

`internal/workspace/credentialpool.go` (new file):

```go
package workspace

type Source string

const (
    SourceLocalFile Source = "local-file"
    SourceVault     Source = "vault"
    SourceCLISession Source = "cli-session"
    SourceNone      Source = "none"
)

type AuditRecord struct {
    Kind      string
    Project   string
    Source    Source
    Provider  string  // vault provider name (empty for non-vault)
    Fallback  string  // "vault:<name>" if vault entry was overridden by file; "" otherwise
}

type CredentialPool struct {
    fileEntries []ProviderAuthEntry            // eager, parsed at construct
    vaultLoader *vaultCredLoader               // nil when opt-in is off
    audit       []AuditRecord                  // appended during Lookup calls
    cache       map[string]vaultLookupResult   // memoized per (kind, project)
}

type vaultLookupResult struct {
    Entry *ProviderAuthEntry  // nil = vault has no entry
    Err   error                // non-nil = parse/version error
}

type vaultCredLoader struct {
    Provider     vault.Provider
    ProviderName string  // "" for anonymous
    PathPrefix   string  // e.g., "/niwa/provider-auth/"
}

func NewPool(file []ProviderAuthEntry, loader *vaultCredLoader) *CredentialPool

// Lookup returns the winning entry (file > vault > nil) plus an audit record.
func (p *CredentialPool) Lookup(ctx context.Context, kind, project string) (*ProviderAuthEntry, AuditRecord, error)

// AuditLog returns all records collected during Lookup calls in this apply.
func (p *CredentialPool) AuditLog() []AuditRecord
```

### Lookup algorithm

```
Lookup(ctx, kind, project):
    fileEntry = matchFileEntry(fileEntries, kind, project)
        // matchFileEntry synthesizes a vault.ProviderSpec for
        // (kind, project) and reuses MatchProviderAuth from
        // internal/workspace/providerauth.go:102 to keep the
        // matching rule single-sourced.
    vaultEntry, vaultErr = nil, nil
    if vaultLoader != nil:
        if cached, ok := cache[key]; ok:
            vaultEntry, vaultErr = cached.Entry, cached.Err
        else:
            ref = vault.Ref{Path: PathPrefix + kind, Key: project}
            sv, _, err = vaultLoader.Provider.Resolve(ctx, ref)
            switch {
            case errors.Is(err, vault.ErrKeyNotFound):
                cache[key] = {nil, nil}  // silent fallthrough per R13.3
                vaultEntry = nil
            case errors.Is(err, vault.ErrProviderUnreachable):
                cache[key] = {nil, err}
                return nil, _, wrapVaultUnreachable(err)  // R13.1 / R13.2
            case err != nil:
                cache[key] = {nil, err}
                return nil, _, err  // unexpected backend error
            default:
                // Plaintext access for parsing the credential body
                // is gated through internal/secret/reveal.UnsafeReveal.
                // This is a deliberate new caller (see Security
                // Considerations §"New surfaces") justified because
                // the body is a structured envelope niwa itself
                // wrote into the vault, not a user-payload secret.
                bodyBytes := reveal.UnsafeReveal(sv)
                vaultEntry, parseErr = parseProviderAuthBody(kind, project, bodyBytes)
                cache[key] = {vaultEntry, parseErr}
                if parseErr != nil:
                    return nil, _, parseErr  // R13.4 / R13.5 / R13.7
            }

    rec = AuditRecord{Kind: kind, Project: project}
    if fileEntry != nil:
        rec.Source = SourceLocalFile
        if vaultEntry != nil:
            rec.Fallback = "vault:" + vaultLoader.ProviderName
        appendAudit(rec)
        return fileEntry, rec, nil
    if vaultEntry != nil:
        rec.Source = SourceVault
        rec.Provider = vaultLoader.ProviderName
        appendAudit(rec)
        return vaultEntry, rec, nil
    rec.Source = SourceCLISession  // tentative; injectProviderTokens upgrades to None if backend auth ultimately fails
    appendAudit(rec)
    return nil, rec, nil
```

### `parseProviderAuthBody`

```go
type providerAuthBody struct {
    Version      string `toml:"version"`
    ClientID     string `toml:"client_id"`
    ClientSecret string `toml:"client_secret"`
    APIURL       string `toml:"api_url"`
}

// parseProviderAuthBody parses a vault-fetched credential body.
// kind and project are passed in by the caller — they are the
// (kind, project) pair the lookup was for — rather than re-derived
// from the vault key path, because the path was constructed from
// these inputs in the first place.
func parseProviderAuthBody(kind, project string, raw []byte) (*ProviderAuthEntry, error) {
    if len(raw) > maxProviderAuthBodyBytes {  // 8 KiB cap; see Security §"New surfaces"
        return nil, oversizedBodyError(len(raw))
    }
    var body providerAuthBody
    if err := toml.Unmarshal(raw, &body); err != nil {
        return nil, malformedBodyError(kind, project, err)  // R13.4
    }
    version := body.Version
    if version == "" {
        version = "1"  // R8 backward-compat default
    }
    if version != "1" {
        return nil, unsupportedVersionError(kind, project, version)  // R13.7
    }
    if body.ClientID == "" {
        return nil, missingFieldError(kind, project, "client_id")  // R13.5
    }
    if body.ClientSecret == "" {
        return nil, missingFieldError(kind, project, "client_secret")  // R13.5
    }
    return &ProviderAuthEntry{
        Kind: kind,
        Config: map[string]any{
            "project":       project,
            "client_id":     body.ClientID,
            "client_secret": body.ClientSecret,
            "api_url":       body.APIURL,  // empty string when omitted; backend uses default
        },
    }, nil
}
```

### Apply.go integration

The change is **not a five-line patch.** Realistic surface area in
`internal/workspace/apply.go`:

- ~10 lines: move the existing `globalOverride` parse from its
  current site (`apply.go:713-726`) up to a new Step 0.3 ahead of
  the workspace-overlay sync block (current `apply.go:509`). The
  parse only depends on `a.GlobalConfigDir` and `opts.skipGlobal`,
  both available immediately, so the move is mechanical — but
  every downstream reader of `globalOverride` (currently 8 sites,
  including `CheckVaultScopeAmbiguity` and the existing
  `injectProviderTokens` calls at 742/746) must keep working. The
  reorder is its own commit in Phase A.
- ~30 lines: opt-in detection, R9 validation, credential-sync
  provider open, `CredentialPool` construction, defer-close
  bookkeeping. New code, lives at the new Step 0.4 position.
- ~10 lines: signature change for `injectProviderTokens` at the
  three existing call sites (`apply.go:592, 742, 746`). The
  function signature changes from `(ctx, []ProviderAuthEntry,
  *VaultRegistry)` to `(ctx, *CredentialPool, *VaultRegistry)`.
  The change is contained within `internal/workspace/`; the
  function is unexported.
- ~3 lines: state persistence at the end of the pipeline
  (`state.AuthSources = pool.AuditLog().AsMap()`).
- Plus a new file `internal/workspace/credentialpool.go`
  (~150-200 lines including the lookup algorithm,
  `parseProviderAuthBody`, `vaultCredLoader`, audit-record
  collection, and helpers).
- Plus migration shim and field on `InstanceState` in
  `internal/workspace/state.go` (~15 lines).
- Plus the R2 check in `internal/config/config.go`
  `ParseGlobalConfigOverride` (~20 lines).
- Plus the `niwa status --audit-auth` flag handling in
  `internal/cli/status.go` (~40 lines).

Total: roughly 250-350 net new lines of production code spread
across 4 files, plus tests. The "5-line patch" framing in the
prior revision was misleading; this patch is medium-sized.

The integration sketch (showing the new Step 0.4 plus the changed
call sites):

```go
// Step 0.2 (existing, unchanged location)
var authEntries []ProviderAuthEntry
if niwaConfigDir, err := NiwaConfigDir(); err == nil {
    entries, err := LoadProviderAuth(niwaConfigDir)
    if err != nil { return err }
    authEntries = entries
}

// Step 0.3: refresh personal-overlay snapshot, then parse niwa.toml.
// Both halves (formerly Step 2a sync at apply.go:706, parse at
// apply.go:713-726) are hoisted together to preserve the original
// sync-then-parse ordering — without the sync hoist, drift on a
// GitHub-tracked global config would be picked up one apply later.
var globalOverride *config.GlobalConfigOverride
if a.GlobalConfigDir != "" && !opts.skipGlobal {
    // Step 0.3a: sync the personal overlay snapshot.
    a.Reporter.Status("syncing config...")
    fetcher, _ := a.GitHubClient.(FetchClient)
    converted, syncErr := EnsureConfigSnapshotWithStatus(ctx, a.GlobalConfigDir, fetcher, a.Reporter)
    if syncErr != nil {
        a.Reporter.Warn("could not sync config: %v", syncErr)
        return nil, fmt.Errorf("syncing global config: %w", syncErr)
    }
    if converted && !sliceContains(opts.disclosedNotices, noticeConfigConverted) {
        a.Reporter.Log("note: %s converted from working tree to snapshot. ...", a.GlobalConfigDir)
        newDisclosures = append(newDisclosures, noticeConfigConverted)
    }
    // Step 0.3b: parse the (possibly just-refreshed) niwa.toml.
    overridePath := filepath.Join(a.GlobalConfigDir, GlobalConfigOverrideFile)
    data, readErr := os.ReadFile(overridePath)
    if readErr == nil {
        parsed, parseErr := config.ParseGlobalConfigOverride(data)
        if parseErr != nil {
            return nil, fmt.Errorf("parsing global config override: %w", parseErr)
        }
        globalOverride = parsed
    } else if !os.IsNotExist(readErr) {
        return nil, fmt.Errorf("reading global config override: %w", readErr)
    }
}

// Step 0.4: build credential pool, with optional vault loader (NEW)
var loader *vaultCredLoader
var syncBundle *vault.Bundle
if globalOverride != nil && globalOverride.Global.MachineIdentities != nil {
    mi := globalOverride.Global.MachineIdentities
    syncSpec, err := pickCredentialSyncSpec(globalOverride.Global, mi)
    if err != nil { return err }
    if err := validateCredentialSyncBootstrapPreOverlay(authEntries, globalOverride.Global.Vault, syncSpec); err != nil {
        return err  // R9 — first stage; second stage runs after overlay parse
    }
    syncBundle, err = vault.DefaultRegistry.Build(ctx, []vault.ProviderSpec{syncSpec})
    if err != nil { return err }
    defer syncBundle.CloseAll()
    prov, err := syncBundle.Get(syncSpec.Name)
    if err != nil { return err }
    loader = &vaultCredLoader{
        Provider:     prov,
        ProviderName: syncSpec.Name,
        PathPrefix:   "/niwa/provider-auth/",
    }
}
pool := NewCredentialPool(authEntries, loader)

// Step 0.5 (existing): workspace overlay sync + parse + token injection.
//   Existing call site at apply.go:592, signature changed:
//     injectProviderTokens(ctx, pool, overlay.Vault)
//   After overlay parse, run the second R9 stage:
//     validateCredentialSyncBootstrapPostOverlay(pool, overlay.Vault, syncSpec)

// Existing call sites at apply.go:742 and 746, signature changed:
//   injectProviderTokens(ctx, pool, cfg.Vault)
//   injectProviderTokens(ctx, pool, globalOverride.Global.Vault)

// State save (existing site, NEW field):
//   state.AuthSources = pool.AuditLog().AsMap()
```

### Config parsing additions

`internal/config/config.go`:

```go
type GlobalOverride struct {
    Vault              *VaultRegistry              // existing
    MachineIdentities  *MachineIdentitiesConfig    // new, optional
    // ... other existing fields unchanged
}

type MachineIdentitiesConfig struct {
    From string `toml:"from"`  // empty/unset = use anonymous [global.vault.provider]
}
```

`internal/config/config.go` (R2 check, called from
`ParseGlobalConfigOverride` immediately after
`cfg.Global.Vault.Validate("global overlay")`):

```go
func validateMachineIdentities(prefix string, ov GlobalOverride) error {
    if ov.MachineIdentities == nil {
        return nil
    }
    if ov.Vault == nil {
        return fmt.Errorf("%s: [machine_identities] is enabled but no vault provider is declared. Add [vault.provider] (anonymous) or [vault.providers.<name>] and set from = \"<name>\".", prefix)
    }
    if ov.MachineIdentities.From == "" {
        if ov.Vault.Provider == nil {
            return fmt.Errorf("%s: [machine_identities] is enabled and from is unset but no anonymous [vault.provider] is declared. Either set from or declare an anonymous provider.", prefix)
        }
        return nil  // anonymous default
    }
    if _, ok := ov.Vault.Providers[ov.MachineIdentities.From]; !ok {
        return fmt.Errorf("%s: [machine_identities] from = %q references unknown vault provider. Declared providers in this file: [%s].",
            prefix, ov.MachineIdentities.From, knownNames(ov.Vault.Providers))
    }
    return nil
}
```

Wired into `ParseGlobalConfigOverride` like:

```go
if err := cfg.Global.Vault.Validate("global overlay"); err != nil {
    return nil, err
}
if err := validateMachineIdentities("global overlay", cfg.Global); err != nil {
    return nil, err  // R2
}
```

### State schema (v3 -> v4)

`internal/workspace/state.go` — bump `SchemaVersion` from `3` to
`4` and extend `InstanceState`:

```go
const SchemaVersion = 4  // was 3; v4 adds InstanceState.AuthSources

type InstanceState struct {
    SchemaVersion    int                            `json:"schema_version"`
    // ... all existing fields unchanged
    AuthSources      map[string]AuthSourceRecord    `json:"auth_sources,omitempty"`  // NEW v4
}

// AuthSourceRecord carries one row of the audit table that
// `niwa status --audit-auth` renders. Both fields are categorical
// strings (R24 / PRD R18): they identify *where* a credential came
// from, never the credential bytes themselves.
type AuthSourceRecord struct {
    Source   string `json:"source"`              // local-file | vault:<name> | cli-session | none
    Fallback string `json:"fallback,omitempty"`  // vault:<name> when local overrides vault
}
```

The migration shim in `LoadState` initializes `AuthSources` to an
empty map for v3 files and rewrites them as v4 on the next
`SaveState`. v1 and v2 files continue to flow through the existing
v1→v2→v3 migration shims and arrive at v4 the same way.

The forward-version check at
`internal/workspace/state.go:246` (`schema_version > SchemaVersion`)
prevents a pre-feature niwa from reading a v4 file. This is the
existing downgrade-safety mechanism and is the same behavior the
v2→v3 bump shipped with.

### `niwa status --audit-auth` rendering

`internal/cli/status.go`:

```go
if auditAuth {
    rows := make([]AuditAuthRow, 0, len(state.AuthSources))
    for key, rec := range state.AuthSources {
        kind, project, _ := strings.Cut(key, "/")
        rows = append(rows, AuditAuthRow{
            Kind: kind, Project: project,
            Source: displaySource(rec.Source),
            Fallback: displayFallback(rec.Fallback),
        })
    }
    sort.Slice(rows, ...)  // stable order: KIND, then PROJECT
    printAuditAuthTable(rows)  // text-table per PRD R11
    if hasNoneSource(rows) {
        return exitCodeNonZero
    }
    return 0
}
```

No vault calls. No network. Reads `state.json` only.

### Apply-time stderr per R12

The existing `Reporter` infrastructure (`internal/workspace/reporter.go`)
already supports per-line stderr emission. After `injectProviderTokens`
finishes for all three layers, walk `pool.AuditLog()` and emit one
line per record where `Source == SourceVault`:

```
auth: <kind>/<project> source=vault:<name>
```

Plus one line per record where `Source == SourceLocalFile && Fallback != ""`:

```
auth: <kind>/<project> source=local-file fallback=<fallback>
```

R12's "no line for cli-session or pure-local-file" maps to: emit nothing
when `Source == SourceCLISession` or `(Source == SourceLocalFile && Fallback == "")`.

## Implementation Approach

Phased to keep PRs reviewable:

**Phase A — Pool + file-only path + globalOverride parse reorder.**
The phase intentionally bundles the parse reorder with the
no-behavior-change refactor so the reorder lands behind the
existing tests; later phases assume `globalOverride` is available
at the start of the pipeline.
1. Move `globalOverride` parse from `apply.go:713-726` to before
   the workspace-overlay sync block. Verify every downstream
   reader (8 sites today) still works against the same parsed
   value. This is the riskiest part of Phase A and warrants
   its own focused review.
2. Add `CredentialPool` type with file-only constructor.
3. Refactor `injectProviderTokens` to take a pool instead of a
   slice. Three call sites updated.
4. Audit log infrastructure (collect records, no persistence yet).
5. Tests: existing apply behavior is byte-identical for all
   current users. Snapshot test of stderr/stdout/exit code on a
   representative apply scenario before and after the phase.

**Phase B — State persistence + `--audit-auth`.**
1. Add `AuthSources` field, v3->v4 migration, save during apply.
2. Add `niwa status --audit-auth` flag and rendering.
3. Tests: state migration, audit table output, exit codes.

**Phase C — Opt-in opening + lazy vault loader + R2 validation.**
1. `MachineIdentitiesConfig` parser additions.
2. `validateMachineIdentities` in validate_vault_refs.go.
3. `openCredentialSyncProvider` and `vaultCredLoader`.
4. `parseProviderAuthBody` with R8 version handling.
5. End-to-end test: opt in, fetch from a fake vault, succeed.

**Phase D — R9 + failure modes + R12 stderr.**
1. `validateCredentialSyncBootstrap` with all R9 cases.
2. Wire `vaultLookupResult` errors into apply errors per R13 table.
3. Wire `Reporter` per R12 lines.
4. Functional test scenarios (Gherkin) for every R13 row and the
   R12 stderr shapes.

Each phase is independently shippable; Phase A is a refactor with no
user-visible change, B adds a flag without behavior change in apply,
C+D ship the feature.

## Security Considerations

The vault PRD names invariants R21-R31 ("never leaks") and the
PRD-machine-identity-vault-sync explicitly inherits them. This
section walks each invariant and notes how the design preserves it.

### R21 — Argv-secret-rejection

**Threat**: A `client_secret` ends up on a process argv (visible to
`ps` and ProcFS to same-user processes).
**Design response**: The existing Infisical backend already passes
`client_id`/`client_secret` to the universal-auth HTTP endpoint, never
to a subprocess argv. The credential-sync feature uses the same
`vault.Provider.Resolve` API and the same Infisical backend path —
no new subprocess invocation, no new argv surface. The
`parseProviderAuthBody` produces a `ProviderAuthEntry` whose
`Config` map flows through the existing `injectProviderTokens` path
that already preserves R21.

### R22 — Log/stderr redaction

**Threat**: A secret appears in a log line or stderr message.
**Design response**: The new errors (R13.4, R13.5, R13.7) name the
**path** (`/niwa/provider-auth/<kind>/<project>`) and the missing
**field name** — never the value. The audit log records source
**identifiers** (`vault:<name>`, `local-file`), never credential
fields. The `secret.Value` type's existing redacting formatters
apply to any value that flows through `injectProviderTokens` after
`parseProviderAuthBody` returns. Test assertion required: after a
malformed-body apply error, no portion of the body bytes appears in
the final error chain.

### R23 — Process-env publication

**Threat**: A secret is written into the process environment of a
spawned tool.
**Design response**: Vault-sourced credentials follow the same flow
as local-file credentials: they end up in `ProviderConfig` and reach
the Infisical backend's universal-auth HTTP call. They are NOT
written to environment variables for spawned tools. Same surface as
today.

### R24 — Disk-cache prohibition

**Threat**: A secret is written to disk in a way the user doesn't
expect.
**Design response**: R6 + R17 are encoded structurally:
- The credential pool is a Go struct that lives only in process
  memory; no `MarshalJSON` method, no serialization code.
- The `CredentialPool.cache` is a Go map, not a file.
- `state.json`'s new `auth_sources` field stores ONLY
  source-identifiers (`vault:<name>`, `local-file`), not the
  credential bytes. Concrete invariant: the JSON serialization of
  `AuthSourceRecord` contains exactly two string fields, both of
  which are categorical (not credential-derived).
- A test (AC-30) snapshots `~/.config/niwa/` before and after each
  apply scenario and asserts byte-identity except for `state.json`,
  which itself is asserted not to contain credential bytes.

### R25 — Public-repo guardrail

**Threat**: A secret leaks via a publicly-readable config file.
**Design response**: Per PRD R14, the existing plaintext-secrets
guardrail is unchanged. Credential-sync stores nothing in any
config file; the personal overlay's `[global.machine_identities]`
contains only a provider name (not a secret), and the credential
body lives in the vault. No guardrail extension required, and we
add a unit test that asserts the guardrail's existing rules still
fire for plaintext-in-secrets-table scenarios after this feature
lands.

### R26 — CLAUDE.md interpolation refusal

**Threat**: A secret is interpolated into a CLAUDE.md file.
**Design response**: Vault-sourced credentials never enter the
`secret.Value` pool the CLAUDE.md interpolator consults. They are
attached to `ProviderConfig` only. Same surface as today's
local-file credentials.

### R27 — Status-content redaction

**Threat**: `niwa status` output contains a secret value.
**Design response**: The new `--audit-auth` view renders four
columns (KIND, PROJECT-UUID, SOURCE, FALLBACK). None contain
credential bytes. The PROJECT-UUID is the same UUID present in the
team config's `[vault.provider]` block (already public). The
SOURCE / FALLBACK columns hold categorical strings.

### R28 — No process-env publication for vault tokens

Same as R23. Tokens obtained from universal-auth flow through the
existing Infisical backend's `--token` mechanism, not via env vars.

### R29 — Override-visibility (R31 in vault-integration PRD)

**Threat**: A precedence override is silent and the user can't
audit it.
**Design response**: Every (kind, project) where local file
overrides vault is recorded in `state.json` AND emitted to stderr
during apply (R12) AND surfaced in `--audit-auth` as the FALLBACK
column (R11). Three independent surfaces guarantee visibility.

### R30 — Provider stderr scrubbing

**Threat**: An upstream provider error message contains a secret.
**Design response**: The Infisical backend's `vault.ScrubStderr`
already runs on every backend error. The credential-sync provider
uses the same backend, so its errors flow through the same scrub.

### R31 (PRD vault-integration) — Override visibility

Same as R29 above.

### New surfaces this design introduces

The design introduces three new code surfaces that need security
attention:

1. **`parseProviderAuthBody`**: parses TOML from a vault-fetched
   secret value. Risk: a malicious or corrupted body could trigger
   a TOML parser DoS (deeply-nested input, large allocation).
   **Mitigation**: bound the body size at fetch time (e.g., reject
   bodies > 8KB, well above the realistic ~200 byte size), and the
   `BurntSushi/toml` parser is already used everywhere with its
   default recursion limits. Add a unit test with a 100KB pathological
   body and assert the fetch is rejected before parse.

2. **`vaultCredLoader.cache`**: an in-memory map of fetched results.
   Risk: process-memory-resident credentials linger longer than
   strictly necessary. **Mitigation**: the cache is per-`apply`
   invocation. Apply lifetime is short (seconds). On apply
   completion, the `CredentialPool` is dropped and the GC reclaims
   the map. We do NOT zero the memory explicitly because Go's GC
   doesn't expose that primitive and the existing `secret.Value`
   pipeline doesn't either; documenting this in a code comment is
   sufficient under the same threat model the rest of the vault
   feature operates under.

3. **`openCredentialSyncProvider` + R9 validation**: the bootstrap
   check is the *only* defense against credential cycles. Risk: a
   bug in `validateCredentialSyncBootstrap` could allow a configuration
   that leads to the credential-sync provider trying to authenticate
   itself. **Mitigation**: the validation function is small (~30
   LOC) and table-tested with every PRD R9 example. We assert in a
   test that opening the credential-sync provider attempts auth via
   CLI session only (not via the pool) — a regression here would
   trip immediately.

### Threat model alignment

Per PRD-vault-integration §"Threat Model", the existing model
trusts the user's local machine, filesystem, OS keychain, and
provider CLI binaries; it does NOT defend against malicious
processes running as the same user, compromised CLI binaries, or
compromised vault services. This design adds no trust to that set
and removes none. The credential-sync vault provider is treated as
trusted (user owns the personal vault); a compromised personal
vault is out of scope (same as a compromised provider service in
the existing model).

## Consequences

### Positive

- **Zero behavior change for non-opting-in users.** Phase A
  refactors the pool API but the file-only construction path
  produces identical results to today's slice-based path.
- **Lazy fetch by construction.** The `CredentialPool.Lookup`
  method only reaches into the vault when the file misses and the
  caller actually asks. There's no enumeration step.
- **Audit visibility surfaces are testable in isolation.** The
  audit log is a Go data structure; the stderr emission is a
  function over that structure; the state-json serialization is
  a separate function. Each is unit-testable.
- **No new on-disk files.** R17 is structurally guaranteed: there
  is no place in the design to introduce one.
- **R9 chicken-and-egg is checked structurally**, before the
  credential-sync provider opens. Apply fails fast and clearly.

### Negative

- **`injectProviderTokens` signature change.** Three call sites
  must be updated; this is a breaking change in the internal API.
  Mitigation: the change lives entirely within
  `internal/workspace/`, no public Go API affected.
- **Apply pipeline reorder.** Phase A moves the `globalOverride`
  parse from its current position (`apply.go:713-726`) to ahead of
  the workspace-overlay sync. Eight downstream readers depend on
  the parsed value; the move is mechanical (no new dependencies)
  but it is a real refactor with its own test surface. Mitigation:
  Phase A bundles the reorder with the no-behavior-change pool
  refactor, and the phase's acceptance bar is "byte-identical
  apply output for all current users" (snapshot tests on a
  representative scenario).
- **`state.json` schema bump** (v3 -> v4). Old niwa versions
  reading a v4 state file will fail at the existing forward-version
  check at `internal/workspace/state.go:246`. Mitigation: this is
  normal niwa upgrade behavior — the same one-way property held
  for the v2→v3 bump — but the PRD now lists it explicitly under
  Known Limitations so users know that pinning a niwa version
  post-feature is one-way.
- **A new caller of `secret.reveal.UnsafeReveal`.** The package
  warns "DO NOT import this package from new code without explicit
  review." The credential-pool body parsing is a deliberate new
  caller; the justification (the body is a structured envelope
  niwa itself wrote into the vault, never user-payload secret
  data) is documented in code at the call site and in this design.
  A future allow-list linter will need to permit
  `internal/workspace/credentialpool.go` alongside the existing
  materializer/provider callers.
- **An additional ~200ms per used-and-vault-sourced
  `(kind, project)` pair per apply**, dominated by the Infisical
  export call. Acceptable per PRD R16; documented as a Known
  Limitation in the PRD.
- **Two TOML parsers in the credential path** — one for the local
  file (today) and one for vault-fetched bodies (new). The body
  schema is intentionally simpler than the file schema (no
  `[[providers]]` wrapper) so the body parser is small. Risk:
  semantic drift if the body shape ever needs to grow. Mitigation:
  the `version` field is the lever for evolution.

### Mitigations summary

| Risk | Mitigation |
|------|-----------|
| Internal API churn (pool refactor) | Phase A is a no-behavior-change refactor that lands first |
| Apply pipeline reorder (globalOverride parse) | Bundled into Phase A; acceptance bar is byte-identical snapshot of stderr/stdout/exit code; reorder gets its own commit for review |
| state.json migration breaks downgrade | Forward-only migration; consistent with niwa policy; called out in PRD Known Limitations |
| Latency cost on multi-org workspaces | PRD R16 budget is informative; revisit only if user feedback demands |
| TOML parser DoS via malicious vault body | 8 KiB body-size cap at fetch; existing parser limits |
| Process-memory credential lingering | Per-apply lifetime; documented in code comment |
| Bug in R9 check creates auth cycle | Two-stage validation (pre-overlay + post-overlay); small table-tested function; opening test asserts CLI-session auth only and that `injectProviderTokens` is NOT called for the credential-sync provider's spec |
| New `UnsafeReveal` caller | Justification documented at call site and in design; future allow-list linter must permit `internal/workspace/credentialpool.go` |
