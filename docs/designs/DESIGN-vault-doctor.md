---
status: Accepted
upstream: docs/prds/PRD-vault-doctor.md
problem: |
  The PRD fixes what the vault doctor must do: enumerate every
  configured (kind, project) credential pair, fetch each body live,
  validate it with the same authority as apply, check the local
  provider-auth file layer, and report per-pair statuses without ever
  printing a secret. It deliberately left three things to this design:
  where the command lives on the CLI surface, how the live fetch is
  performed, and how the exit code encodes the three outcome classes.
decision: |
  Ship the doctor as `niwa vault check`, a new cobra subcommand under a
  new `vault` parent namespace. The orchestrator lives in
  internal/workspace next to the apply-path validator it reuses
  (parseProviderAuthBody stays unexported; the doctor calls it
  in-package through an exported CheckProviderAuth entry point). Live
  fetches ride the exact read path apply uses: one credential-sync
  provider, opened once, whose Resolve is called for every pair --
  never a bespoke probe, never a pair's own provider config. Exit codes:
  0 all valid, 1 at least one invalid pair or file-layer failure, 2
  vault unreachable or tool error, wired via a typed error and a branch
  in root.go's Execute(), matching the existing exit-code pattern.
rationale: |
  The doctor diagnoses a different object than any existing status
  flag: --audit-auth is offline, --check-vault watches secret-material
  rotation, --audit-secrets classifies values. A live check of the
  credential-sync auth contract doesn't belong in that flag family, and
  the already-named follow-on (`niwa vault init`) gives the namespace a
  second occupant. Reusing apply's read topology whole -- the single
  credential-sync provider, the three-registry pair enumeration, the
  self-pair exclusion, and parseProviderAuthBody -- is what makes PRD
  R5 (doctor and apply never disagree) true by construction rather
  than by test coverage. Distinct exit codes for
  invalid-pair vs unreachable let automated consumers remediate
  differently, which R10 requires.
---

# DESIGN: Vault doctor

## Status

Accepted

## Context and Problem Statement

niwa validates its team-vault credential-sync contract in one place:
`parseProviderAuthBody` in `internal/workspace/credentialpool.go`, deep
in the apply read path. Every failure mode -- missing entry, non-TOML
body, missing `client_id`/`client_secret`, unsupported version,
oversized body -- surfaces as a mid-apply error far from the
misconfigured entry. The PRD (docs/prds/PRD-vault-doctor.md) specifies
a read-only doctor that checks the contract live, per (kind, project)
pair, before an apply depends on it (R1-R12).

The PRD settles the capability. This design settles three deferred
questions: the command surface, the live-fetch mechanism, and the
exit-code encoding. Everything else is reuse -- the validator, the
file-layer loaders, the pair source, and the vault read path all exist.

## Decision Drivers

- PRD R5: the doctor's verdict must match apply's accept/reject
  decision on every body. The strongest guarantee is calling the same
  code, not a faithful copy.
- PRD R10: automated consumers must distinguish all-OK, invalid-pair,
  and vault-unreachable, because they remediate them differently.
- PRD R6/R7: strictly read-only, and no credential value on any output
  path.
- CLI taxonomy: the existing `niwa status` flags (`--audit-auth`,
  `--check-vault`, `--audit-secrets` in `internal/cli/status.go`) form
  a family, but none of them does what the doctor does; the surface
  choice sets precedent for the named follow-on (`niwa vault init`).
- Proportionality: this feature is mostly wiring around existing code.
  The design shouldn't invent new abstractions where the apply path
  already has them.

## Considered Options

### Decision 1: Command surface

**A. New `niwa vault check` subcommand.** Introduces a `vault` cobra
parent (none exists today; only `status_check_vault.go` carries the
word). Pro: the doctor is a live diagnosis of the credential-sync auth
contract -- a genuinely different object from every existing status
flag. `--audit-auth` reads state.json offline (last-apply snapshot),
`--check-vault` re-resolves `vault://` ManagedFiles to detect
secret-material rotation, `--audit-secrets` classifies secret table
values. None talks to the vault to ask whether future applies will
authenticate. A namespace also gives the follow-on `niwa vault init` a
coherent home, so this is the first member of a small family, not a
one-off. Con: a new top-level namespace to document and maintain.

**B. New `niwa status --check-credentials` flag.** Consistent with the
existing audit-flag family and adds no namespace. Con: it stretches
`status` -- whose flags are offline or rotation-focused -- to cover a
live network check of a contract `status` doesn't otherwise know about,
and it leaves `niwa vault init` homeless (a `status` flag can't scaffold
config). The flag family would then contain four near-synonyms
(`--audit-auth`, `--check-vault`, `--audit-secrets`,
`--check-credentials`) whose differences users already confuse; adding
a fourth makes the taxonomy worse, not more consistent.

**C. Extend `--check-vault`.** Cheapest surface change. Con: it merges
two different questions -- "has secret material rotated?" and "will
authentication resolve?" -- behind one flag, breaking the existing
flag's contract for scripts that consume it today, and it can't carry
the doctor's distinct exit-code semantics without overloading
`--check-vault`'s.

### Decision 2: Live-fetch mechanism

**A. Reuse the apply read path: `vault.Provider.Resolve(ctx, vault.Ref)`.**
This is exactly what `lookupVault` does (`internal/workspace/credentialpool.go`):
it builds `vault.Ref{Path: "/niwa/provider-auth/" + kind, Key: "p-" + project}`
and calls Resolve on the one credential-sync provider apply opens once
via `pickCredentialSyncSpec` + `openCredentialSyncProvider`
(`internal/workspace/credentialsync.go:31-71`) -- for Infisical,
Resolve shells `infisical export` via the unexported
`runInfisicalExport` (`internal/vault/infisical/subprocess.go`,
`cmd.Env = nil`, so it uses the caller's CLI session). A pair's own
`VaultProviderConfig` is never opened to fetch its own auth body; the
body is what would authenticate it, so that read would be circular.
The doctor gets identical semantics for free, including the
`ErrKeyNotFound` / `ErrProviderUnreachable` sentinel distinction the
Provider interface already guarantees.

**B. A lighter existence probe.** Cheaper per pair, but the doctor and
apply could then see different things (different path rendering, auth
mode, or error mapping), which is exactly the disagreement R5 forbids.
Rejected.

### Decision 3: Exit-code encoding

**A. 0 / 1 / 2.** 0 when every pair and the file layer are valid
(`absent` file is informational per R4, not a failure); 1 when the
vault was reached but at least one pair or file-layer check failed;
2 when the vault couldn't be reached at all (or the provider tool is
missing/broken). Matches common doctor-style CLI convention and gives
scripts the three-way branch R10 requires without parsing output.

**B. Single non-zero code, distinction only in `--json`.** Simpler,
but forces every automated consumer to parse JSON just to decide
between "fix the entry" and "log in first". Rejected.

## Decision Outcome

- **Surface: option A, `niwa vault check`.** The object-and-purpose
  distinction is decisive: the doctor reads the vault live to validate
  the auth contract, which no status flag does, and the namespace has a
  named second occupant coming. The consistency argument for B is real
  but points the wrong way -- consistency with a flag family is only a
  virtue when the new thing is the same kind of thing.
- **Fetch: option A, reuse `Provider.Resolve` on the single
  credential-sync provider, with the same Ref construction as
  `lookupVault`.** R5 becomes true by construction: same provider,
  same enumeration, same self-exclusion, same validator.
- **Exit codes: option A, 0/1/2**, carried by a typed error and mapped
  in `Execute()` in `internal/cli/root.go`, which already type-asserts
  `sessionattach.ExitCodeError` and `workspace.InitConflictError` for
  the same purpose.

## Solution Architecture

Two new pieces, thin by intent:

**Orchestrator: `internal/workspace` (new file, e.g.
`providerauthcheck.go`).** It lives in the `workspace` package so it
can call `parseProviderAuthBody` directly -- the validator stays
unexported, no wrapper needed. Exported entry point along the lines of:

```go
func CheckProviderAuth(ctx context.Context, global config.GlobalOverride,
    registries []*config.VaultRegistry, configDir string) (*ProviderAuthReport, error)
```

Behavior:

1. Open the credential-sync provider once, exactly as apply's Step 0.4
   does: `pickCredentialSyncSpec(global)` then
   `openCredentialSyncProvider`
   (`internal/workspace/credentialsync.go:31-71`). There is one
   credential-sync provider in production, not one per pair; every
   pair's body is fetched by calling `Resolve` on it. When
   `pickCredentialSyncSpec` returns nil -- the personal overlay has no
   anonymous `[global.vault.provider]` (`credentialsync.go:33`), a
   legitimate state for single-org users -- there is nothing to
   Resolve against: the report carries a single informational
   `no-credential-sync-configured` finding and the command exits 0
   while saying explicitly that no pairs were verified. It never
   renders an empty all-clear, and never reports this as
   vault-unreachable.
2. Enumerate (kind, project) pairs across the same three vault
   registries apply feeds `injectProviderTokens`: the
   workspace-overlay Vault (`internal/workspace/apply.go:915`), the
   team workspace-config Vault (`apply.go:1035`), and the personal
   global-overlay Vault (`apply.go:1039`), merged and deduplicated
   (R1). The CLI layer assembles the three the same way apply's
   pipeline does. Enumerating a single registry would silently drop
   pairs and violate R1.
3. Skip the credential-sync provider's own (kind, project), exactly as
   `lookupVault`'s self-guard does (`credentialpool.go:428`, via the
   `SelfKind`/`SelfProject` fields documented at
   `credentialpool.go:253-268`). apply never validates that pair
   against the vault -- it authenticates through the caller's CLI
   session (PRD R9's chicken-and-egg rule). The doctor reports it as
   OK with the detail "authenticates via CLI session", never as
   `missing-entry`.
4. For each remaining pair, build the same `vault.Ref` `lookupVault`
   builds (`CredentialSyncPathPrefix + kind`, key `"p-" + project`),
   call `Resolve` on the sync provider, and map the result:
   `ErrKeyNotFound` → `missing-entry`; `ErrProviderUnreachable` →
   abort classification and mark the run vault-unreachable; success →
   run `parseProviderAuthBody` and map its error classes to
   `malformed-body` / `missing-field` / `unsupported-version` / OK
   (R2, R3). One pair's failure never stops the loop (R12).
5. File layer: `LoadProviderAuth(configDir)`
   (`internal/workspace/providerauth.go`) already enforces the 0600
   guardrail and entry shape -- its errors map to `bad-mode` /
   `malformed-file`; a missing file maps to `absent` (informational).
   The `malformed-file` Detail is a fixed categorical string, not
   `LoadProviderAuth`'s raw parse error (see Security Considerations).
   For a present, valid file, `MatchProviderAuth` reports which pairs
   have a local entry (R4).
6. Return a `ProviderAuthReport`: a slice of per-pair records
   `{Kind, Project, ProviderName, Status, Detail}` plus file-layer
   findings. `Detail` carries only the scrubbed message the validator
   already renders (keyedPath + field name, never body bytes).

**CLI: `internal/cli/vault.go` + `internal/cli/vault_check.go`.** A
`vault` parent command and a `check` subcommand, self-registering via
`init()` + `rootCmd.AddCommand` like every other command. The
subcommand loads the workspace config and the personal global overlay,
assembles the three vault registries the same way apply's Step 0.4
pipeline does, calls `CheckProviderAuth`,
renders either the human table (one row per pair, plus file-layer
lines; R9) or `--json` (one record per pair and per file-layer
finding; R8), and returns a typed `vaultCheckError{ExitCode int}` that
`Execute()` maps to exit 1 or 2.

**Reuse vs net-new.** Reused: `parseProviderAuthBody` and its scrubbed
errors, `pickCredentialSyncSpec` and `openCredentialSyncProvider`,
`CredentialSyncPathPrefix`, `LoadProviderAuth`,
`MatchProviderAuth`, `VaultRegistry`, the `vault.Provider` interface
and its sentinel errors (and, behind it, `runInfisicalExport` --
untouched and still unexported). Net-new: the `vault` cobra parent, the
`check` subcommand, `CheckProviderAuth` and its result types, the table
and JSON renderers, the typed exit-code error plus its `Execute()`
branch. Nothing gets exported that isn't today.

## Implementation Approach

1. Add `CheckProviderAuth` and result types in `internal/workspace`,
   with unit tests per failure class (the fixtures mirror the PRD's
   acceptance list: absent key, non-TOML body, missing field,
   `version = "2"`, empty version, near-8-KiB body) plus a self-pair
   fixture (the credential-sync provider's own pair reports
   OK-via-session, never `missing-entry`) and a multi-registry fixture
   (pairs split across the workspace-overlay, team-config, and
   global-overlay registries all appear). After iterating pairs,
   `CheckProviderAuth` closes the provider via `bundle.CloseAll()`
   (matching `resolve.go`'s `defer bundle.CloseAll()`), so the
   provider's in-memory `paths` cache -- which holds sibling keys
   fetched at each path -- is cleared rather than left for GC.
2. Add the `vault` parent and `check` subcommand in `internal/cli`,
   plus the `Execute()` exit-code branch in `internal/cli/root.go`.
3. Renderers: table default, `--json` flag. A sentinel-secret test
   asserts no fixture secret value appears in stdout/stderr across all
   statuses and modes (R7); its fixture set includes a malformed local
   `provider-auth.toml`, pinning the fixed-string `malformed-file`
   detail.
4. A `@critical` Gherkin scenario in `test/functional/features/`
   covering the all-OK, one-broken-pair, and unreachable-vault exits
   (per the repo's functional-testing convention for new user-facing
   commands).

R5 conformance needs no dedicated mechanism because the doctor reuses
apply's read topology at every layer: the same single credential-sync
provider apply opens, the same three-registry enumeration, the same
self-pair exclusion, and the same `parseProviderAuthBody` validator.
A table-driven test feeding identical bodies to both the doctor and
the apply path documents the guarantee.

## Security Considerations

- **Strictly read-only (R6).** The doctor performs no writes anywhere:
  not to the vault, the workspace config, the provider-auth file, or
  state.json. It calls only `Resolve`, `LoadProviderAuth`, and config
  loading -- all reads. It must not create vault folders or entries
  even as a side effect of probing.
- **Secret hygiene (R7).** The only secret-adjacent strings the report
  can carry are the ones `parseProviderAuthBody` already renders:
  keyedPath and field names, deliberately scrubbed (including
  `sanitizeTOMLError`, which discards parser text entirely). The
  renderers print pair identity, status, and that detail string --
  never a fetched body or any field value. The sentinel-canary test
  from the apply path is replicated for the doctor's output.
- **File-layer error sanitization.** The vault-body path scrubs TOML
  parse errors (`sanitizeTOMLError`, `credentialpool.go:130-140`,
  which discards the parser's text for a fixed string), but
  `LoadProviderAuth`'s TOML-parse branch wraps the raw parser error
  unsanitized (`providerauth.go:65-67`). The doctor creates a new
  stdout/`--json` surface for that failure, so the `malformed-file`
  status's Detail MUST be a fixed categorical string -- never
  `LoadProviderAuth`'s raw `err.Error()`. Only `malformed-file` needs
  this; `bad-mode` and `absent` involve no parsing.
- **No authentication exchange, ever.** The doctor never calls
  `infisical.Authenticate` (`internal/vault/infisical/auth.go`) -- the
  function that sends `client_secret` over HTTP and has to scrub
  response bodies (`scrubResponseBody`) -- and performs no
  machine-identity HTTP exchange of any kind. It validates body shape
  only; it never trades credentials for a JWT. That scope boundary is
  why the vault-unreachable-vs-missing-entry distinction carries no
  leak risk: the unreachable classification fires before any body is
  parsed.
- **Transient secret in memory.** Fetching a credential body pulls a
  secret into memory transiently for shape validation; it is
  validated, then discarded -- never stored, cached across runs, or
  rendered. The only things that ever reach output are the pair
  identity, the keyedPath (`.../<kind>/p-<project>`, no secret), and
  the fixed status vocabulary.
- **No new credential handling.** The fetch rides the caller's
  existing vault session exactly as apply does (`cmd.Env = nil` in the
  Infisical subprocess). The doctor introduces no new auth flow, token
  storage, or prompt.

## Consequences

**Positive.** Mid-apply credential failures become an up-front, named
diagnosis. The net-new surface is small -- roughly two CLI files, one
orchestrator file, and renderers -- because validation, file loading,
pair enumeration, and fetching are all reused. The `vault` namespace
opens with a clear charter for `niwa vault init`.

**Negative / trade-offs.** A new top-level command namespace to
document and keep coherent. The doctor depends on the caller having a
working vault CLI session: "vault unreachable" and "not logged in"
both land in exit 2 and must be worded so neither is mistaken for a
genuinely missing entry (`missing-entry` is exit 1 and only ever
reported after a successful reach). The report is point-in-time -- a
clean run doesn't lock the vault against rotation before the next
apply (a PRD-acknowledged limitation, restated here because the exit-0
contract might otherwise read as stronger than it is). Non-goal: a
future `niwa vault check` that adds a real auth probe (a two-hop
credential exchange) must re-run security review and wire the same
`secret.WithRedactor`/`RegisterValue` protections apply uses.

**Mitigations.** The unreachable message names the provider and
suggests the provider's login command; the table prints the
distinction explicitly rather than relying on exit codes alone. The
functional scenario pins the 0/1/2 contract so it can't drift
silently.
