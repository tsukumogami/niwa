---
status: Proposed
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

Proposed

## Context and Problem Statement

Today's apply pipeline (`internal/workspace/apply.go`) authenticates
vault providers in two coupled steps:

1. **`LoadProviderAuth(niwaConfigDir)`** at apply.go:492 reads
   `~/.config/niwa/provider-auth.toml` once, returning a
   `[]ProviderAuthEntry` slice.
2. **`injectProviderTokens(ctx, entries, registry)`** at apply.go:592,
   742, and 746 walks each `VaultRegistry` (workspace overlay, team
   config, personal-overlay-global). For each provider spec, it
   calls `MatchProviderAuth(spec, entries)` (providerauth.go:102) to
   find a matching entry by `(kind, project)`, authenticates against
   the backend if matched, and injects the resulting token into
   `ProviderConfig["token"]`.

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
- **`vault.Registry.Build`** (the existing pipeline that opens vault
  providers from a `VaultRegistry`) is the canonical way to
  instantiate a `vault.Provider`. Reusing it keeps backend
  authentication uniform.
- **`vault.Provider.Resolve(ctx, ref)`** is single-key only. There
  is no list/scan API. This forces the design to either know which
  keys to ask for (we do — from the resolved registries) or to
  publish a manifest convention (rejected; see Considered Options).
- **`state.json`** is the existing per-workspace persistence file
  (`internal/state/state.go`). Adding a top-level field is the
  established way to add per-apply records.

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
go through the pool's `Lookup(kind, project) (*PoolEntry, error)`
method, which returns:

- The local-file entry if present (always wins per R4)
- Otherwise, lazily fetches from the vault, parses the TOML body,
  validates the `version` field (R8), and returns the result
- Otherwise, returns `nil` (no entry — fall through to CLI session)

A second method `LookupAll(kind, project)` returns BOTH layers'
entries (used by audit so we can record fallbacks):

```go
type PoolEntry struct {
    Source      Source           // local-file | vault | none
    ProviderName string          // for vault source: provider name (empty for anonymous)
    Entry       ProviderAuthEntry
}
```

`injectProviderTokens` is updated to take the pool, call
`LookupAll`, persist both layers' presence into the audit record
(see Decision 3), and authenticate using the winning entry.

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
// R9 check: spec's (kind, project) MUST NOT appear in pool's keys
if poolHasKey(file, spec.Kind, spec.Config["project"]) {
    return chickenAndEggError(spec)
}
// Open via existing factory; auth via CLI session (no pool entry by R9)
prov, err := vault.OpenSingle(ctx, spec, /*authEntries=*/nil)
if err != nil { return err }
defer prov.Close()
```

The `authEntries` argument is intentionally `nil` — passing the pool
would re-introduce the cycle R9 forbids.

**Rationale**: Option A is appealing for code reuse but couples the
two `Bundle` lifecycles. Option C scatters credential-sync logic
across backends and breaks the abstraction the PRD's R7 schema
contract relies on (one path convention applies to every backend
that ever ships).

### Decision 3: State persistence schema for offline audit

R11 requires `niwa status --audit-auth` to work fully offline. The
audit reads from persisted state. Three options for *where and how*
to persist:

- **Option A — New top-level `auth_sources` map in
  `state.json`** keyed by `"<kind>/<project>"`, value is `{source,
  fallback}`.
- **Option B — Annotate existing per-provider state** records with
  a `source` field. State is already tracked per provider in
  `state.json`; this would extend that.
- **Option C — Separate `auth_state.json` file** alongside
  `state.json`.

#### Chosen: Option A — `auth_sources` map in `state.json`

Add a top-level field:

```json
{
  "version": 4,
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
invocation: `apply.go` collects `(kind, project) -> PoolEntry`
records as `injectProviderTokens` runs and writes the map at
state-save time. Failed applies leave the previous map intact (the
on-disk state isn't written when apply errors before state save).

**Rationale**: Option A has the smallest blast radius — one new map
in a file that already contains per-apply state. Option B requires
threading a `source` field through every per-provider state record
and risks breaking on `state.json` migrations. Option C creates a
new on-disk surface, which violates R17.

State-version bump: this design adds `auth_sources` at the top
level. We bump `state.json` schema version (`v3` -> `v4`) and write
a one-line migration that initializes `auth_sources` to an empty map
when reading a v3 file. The empty map renders an empty
`--audit-auth` table on the first read after upgrade; the next apply
populates it.

### Decision 4: Validation timing and placement

R2 (unknown-provider name) and R9 (chicken-and-egg) must surface
before any vault call. Two placements considered:

- **Option A — Extend `validate_vault_refs.go`** (the existing
  config-parse-time validator that checks `vault://` URI references)
  with new rules for `[global.machine_identities]`.
- **Option B — Add a new pre-fetch validation pass** in `apply.go`
  that runs after the global overlay is loaded but before the
  credential-sync provider is opened.

#### Chosen: Option A for R2, Option B for R9

R2 (unknown provider name) is a **purely config-local** check: does
the named provider exist in the same file's `[global.vault.*]`
declarations? This is exactly the shape `validate_vault_refs.go`
handles for `vault://` URIs. Add the check there, mirroring the
existing diagnostic at validate_vault_refs.go:285-293.

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

The four decisions compose into a small additive feature gated on
`[global.machine_identities]`:

1. **Config parser (`internal/config/validate_vault_refs.go`)**
   gains R2: when `[global.machine_identities]` is present in the
   personal overlay, validate `from` against declared
   `[global.vault.*]` provider names.
2. **Apply (`internal/workspace/apply.go`)** gains:
   - A pre-fetch R9 check
     (`validateCredentialSyncBootstrap`).
   - A credential-sync provider open
     (`openCredentialSyncProvider`) gated on opt-in.
   - A `CredentialPool` constructed from the file (existing) plus
     the opened provider (new).
   - `injectProviderTokens` updated to take the pool and record
     audit info.
   - Per-pair stderr emission via the existing `Reporter`.
3. **State (`internal/state/state.go`)** gains an `auth_sources`
   map and a `v3 -> v4` migration.
4. **Status (`internal/cli/status.go`)** gains a `--audit-auth`
   flag that reads `auth_sources` from `state.json` and renders the
   text-table format from PRD R11.
5. **Infisical backend (`internal/vault/infisical/`)**: no changes.
   The credential-sync provider uses the same `Resolve(ctx, ref)`
   API workspace env-resolution already uses.

Single-org and non-opting-in users see byte-identical behavior. The
CredentialPool constructor returns a file-only pool (with a nil
vault loader) when `[global.machine_identities]` is absent.

## Solution Architecture

### Overview

```
Apply pipeline (chronological):

1. Load workspace + overlay + global override configs
   └─> validate_vault_refs.go runs R2 if [global.machine_identities] present

2. LoadProviderAuth(~/.config/niwa/provider-auth.toml)  (unchanged)

3. Compute resolved vault specs (workspace + overlay + personal-global)

4. If [global.machine_identities] opted in:
   a. validateCredentialSyncBootstrap(file, allVaultSpecs, syncSpec)  (R9)
   b. openCredentialSyncProvider(ctx, overlay, machineIds)
      └─> uses vault.Registry.Build with single spec, nil authEntries
                                                              (CLI session only)
   c. CredentialPool = NewPool(file, syncProvider, "/niwa/provider-auth/")

   Else:
      CredentialPool = NewPool(file, nil)  (vault loader disabled)

5. For each layer (overlay, team, global):
   injectProviderTokens(ctx, pool, layer.Vault)
     └─> per spec: pool.LookupAll(kind, project)
         └─> file lookup (eager, cached)
         └─> vault lookup (lazy: Resolve once per (kind, project))
         └─> records audit entry into pool.AuditLog
         └─> authenticates with winning entry
         └─> emits stderr per R12

6. State save: state.AuthSources = pool.AuditLog.AsMap()
   state.json gets {auth_sources: {...}}

7. niwa status --audit-auth (separate command, separate process):
   reads state.json, renders text table, sets exit code per R11.
```

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

type PoolEntry struct {
    Source       Source
    ProviderName string  // for SourceVault: empty for anonymous, name for named
    Entry        ProviderAuthEntry
}

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
    fileEntry = MatchProviderAuth_file(kind, project)
    vaultEntry, vaultErr = nil, nil
    if vaultLoader != nil:
        if cached, ok := cache[key]; ok:
            vaultEntry, vaultErr = cached.Entry, cached.Err
        else:
            ref = Ref{Path: PathPrefix + kind, Key: project}
            secret, _, err = vaultLoader.Provider.Resolve(ctx, ref)
            if isKeyNotFound(err):
                cache[key] = {nil, nil}
                vaultEntry = nil
            elif err != nil:
                cache[key] = {nil, err}
                return nil, _, wrapVaultUnreachable(err)  // R13.1
            else:
                vaultEntry, parseErr = parseProviderAuthBody(secret.PlainText)
                cache[key] = {vaultEntry, parseErr}
                if parseErr != nil:
                    return nil, _, parseErr  // R13.4 / R13.5 / R13.7

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

func parseProviderAuthBody(raw []byte) (*ProviderAuthEntry, error) {
    var body providerAuthBody
    if err := toml.Unmarshal(raw, &body); err != nil {
        return nil, malformedBodyError(err)  // R13.4
    }
    version := body.Version
    if version == "" {
        version = "1"  // R8 backward-compat default
    }
    if version != "1" {
        return nil, unsupportedVersionError(version)  // R13.7
    }
    if body.ClientID == "" {
        return nil, missingFieldError("client_id")  // R13.5
    }
    if body.ClientSecret == "" {
        return nil, missingFieldError("client_secret")  // R13.5
    }
    return &ProviderAuthEntry{
        Kind: "infisical",
        Config: map[string]any{
            "project":       /* extracted from path or passed in */,
            "client_id":     body.ClientID,
            "client_secret": body.ClientSecret,
            "api_url":       body.APIURL,
        },
    }, nil
}
```

### Apply.go integration

The five-line patch to `apply.go` around the existing
`LoadProviderAuth` + `injectProviderTokens` calls:

```go
// existing
authEntries, err := LoadProviderAuth(niwaConfigDir)
if err != nil { return err }

// NEW: build pool, with optional vault loader
var loader *vaultCredLoader
if mi := globalOverride.Global.MachineIdentities; mi != nil {
    if err := validateCredentialSyncBootstrap(authEntries, allVaultSpecs, mi.SyncSpec); err != nil {
        return err  // R9
    }
    syncProvider, syncSpec, err := openCredentialSyncProvider(ctx, globalOverride.Global, mi)
    if err != nil { return err }
    defer syncProvider.Close()
    loader = &vaultCredLoader{
        Provider:     syncProvider,
        ProviderName: syncSpec.Name,
        PathPrefix:   "/niwa/provider-auth/",
    }
}
pool := NewCredentialPool(authEntries, loader)

// existing injectProviderTokens calls, updated signature
if err := injectProviderTokens(ctx, pool, overlay.Vault); err != nil { return err }
// ...
if err := injectProviderTokens(ctx, pool, cfg.Vault); err != nil { return err }
if err := injectProviderTokens(ctx, pool, globalOverride.Global.Vault); err != nil { return err }

// NEW: persist audit log
state.AuthSources = pool.AuditLog().AsMap()
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

`internal/config/validate_vault_refs.go` (R2 check):

```go
func validateMachineIdentities(file string, ov GlobalOverride) error {
    if ov.MachineIdentities == nil {
        return nil
    }
    if ov.Vault == nil {
        return errors.New(`[global.machine_identities] is enabled but no vault provider is declared. ...`)
    }
    if ov.MachineIdentities.From == "" {
        if ov.Vault.Provider == nil {
            return errors.New(`[global.machine_identities] is enabled but no anonymous [global.vault.provider] declared. Set "from" or declare an anonymous provider.`)
        }
        return nil  // anonymous default
    }
    if _, ok := ov.Vault.Providers[ov.MachineIdentities.From]; !ok {
        return fmt.Errorf(`[global.machine_identities] from = %q references unknown vault provider. Declared providers in this file: [%s]`,
            ov.MachineIdentities.From, knownNames(ov.Vault.Providers))
    }
    return nil
}
```

### State.json schema (v3 -> v4)

`internal/state/state.go`:

```go
type State struct {
    Version      int                            `json:"version"`
    // ... existing fields
    AuthSources  map[string]AuthSourceRecord    `json:"auth_sources,omitempty"`  // NEW v4
}

type AuthSourceRecord struct {
    Source   string `json:"source"`              // local-file | vault:<name> | cli-session | none
    Fallback string `json:"fallback,omitempty"`  // vault:<name> when local overrides vault
}

func migrateV3ToV4(s *State) {
    if s.AuthSources == nil {
        s.AuthSources = map[string]AuthSourceRecord{}
    }
    s.Version = 4
}
```

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

**Phase A — Pool + file-only path (no behavior change).**
1. Add `CredentialPool` type with file-only constructor.
2. Refactor `injectProviderTokens` to take a pool instead of a slice.
3. Audit log infrastructure (collect records, no persistence yet).
4. Tests: existing behavior unchanged for all current users.

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
- **`state.json` schema bump** (v3 -> v4). Old niwa versions
  reading a v4 state file will fail (unknown version). Mitigation:
  this is normal niwa upgrade behavior, and the migration is
  forward-only (upgraded niwa reads v3 fine).
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
| state.json migration breaks downgrade | Forward-only migration; consistent with niwa policy |
| Latency cost on multi-org workspaces | PRD R16 budget is informative; revisit only if user feedback demands |
| TOML parser DoS via malicious vault body | Body-size cap at fetch; existing parser limits |
| Process-memory credential lingering | Per-apply lifetime; documented in code comment |
| Bug in R9 check creates auth cycle | Small table-tested function; opening test asserts CLI-session auth only |
