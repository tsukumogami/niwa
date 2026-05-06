---
status: Draft
problem: |
  Developers using niwa across multiple Infisical organizations must
  hand-create and maintain `~/.config/niwa/provider-auth.toml` on every
  machine. There's no distribution mechanism: joining a new project
  means re-creating the entry on every laptop, and rotating a
  client_secret means touching every machine. This is friction the
  rest of niwa's "configure once, distribute via the personal overlay"
  story doesn't have.
goals: |
  Let a developer produce a machine identity once per Infisical
  project and distribute it across their machines via their own
  personal vault. The personal-overlay vault provider — already
  reachable in the apply pipeline today — becomes a credential
  source that augments the local `provider-auth.toml` file, with
  the local file remaining authoritative when both are present.
  Every credential niwa uses must be inspectable so the source is
  never silent. The feature must not weaken the existing vault
  threat model or revisit the rejected R12/D-9 bulk-provider-swap
  pattern.
---

# PRD: Machine Identity Vault Sync

## Status

Draft

## Glossary

- **Local credential file**: `~/.config/niwa/provider-auth.toml`,
  permission `0o600`, holds `[[providers]]` machine-identity entries.
  Today's only source of credentials for non-default-org Infisical
  authentication. Optional; single-org users never create it.
- **Personal overlay**: the user-owned configuration repo registered
  via `niwa config set global <slug>`, cloned to
  `~/.config/niwa/global/`. Carries `niwa.toml`. Already declares
  (or may declare) `[global.vault.provider]` or
  `[global.vault.providers.<name>]`.
- **Personal vault**: the vault provider declared in the personal
  overlay that this feature reads credentials from. Authenticated via
  the CLI session (`infisical login`) — never via an entry in the
  local credential file or in itself.
- **Credential entry**: one logical row pairing a `(kind, project)`
  identifier with the auth fields a backend needs (for Infisical:
  `client_id`, `client_secret`, optional `api_url`).
- **Credential pool**: the merged set of credential entries from all
  enabled sources (local file plus personal vault) that niwa consults
  during apply.
- **Parse time**: when niwa reads and validates the personal-overlay
  `niwa.toml` (during the global-config-load step that already runs
  at the start of every `niwa apply`). Errors at parse time surface
  before any vault call.
- **Apply time**: any later step of `niwa apply`, including vault
  fetches and backend authentication.

## Precedence

This feature introduces three layers of credential resolution. The
PRD uses a single fixed precedence rule everywhere:

```
LOCAL-FILE  >  VAULT  >  CLI-SESSION
```

A credential entry from the local file always wins over a
vault-sourced entry for the same `(kind, project)`. When neither
layer has an entry, niwa falls through to whatever session token the
backend's CLI holds (e.g., `infisical login`).

This direction inverts the personal-overlay-vs-team-config "shadow"
precedent (where the personal overlay wins) on purpose. Reasoning:

- The local file is the **per-machine override layer**. Letting the
  local file win gives a developer a fast escape hatch to pin or
  test a credential on one machine without coordinating a vault
  change.
- The vault is the **per-user distribution layer**. Letting the
  vault win would inhibit local-only experimentation and force the
  user to commit every change to their vault.
- Bootstrap: the local file MUST be authoritative when the vault is
  unreachable. A vault-wins policy would degrade gracelessly when
  the network is down.

To prevent vocabulary collision with the existing "shadow"
diagnostics (which carry the opposite precedence direction), this
feature uses **augmentation** and **fallback** in user-facing text:

- **Augmentation**: vault entries augment the local file by filling
  gaps for `(kind, project)` pairs the file doesn't cover.
- **Fallback**: when both layers have an entry, the vault entry
  becomes a **fallback** that's recorded in audit but not used.

Vault-wins is intentionally not supported in v1; see Out of Scope.

## Problem Statement

Today, when niwa needs to authenticate a vault provider in a non-default
Infisical organization, it consults `~/.config/niwa/provider-auth.toml`
for an entry matching the `(kind, project)` pair (see
`internal/workspace/providerauth.go:102`). That file is per-machine
state: it lives outside any git repo, must be hand-edited, and has
no sync mechanism.

Three pain points compound on developers who work across multiple orgs:

- **Onboarding tax per laptop.** A developer with a new machine must
  recreate every entry, copying `client_id`/`client_secret` pairs from
  another machine or re-pulling them from the Infisical dashboard.
- **Rotation tax per machine.** When a `client_secret` rotates, the
  developer must update every laptop. There's no diff mechanism, no
  drift signal.
- **Joining-a-project tax.** When the developer joins a new project
  whose team config references a new Infisical org, the entry must be
  added on every machine, which often means delaying first apply.

Meanwhile, the personal overlay is already a place niwa reaches into
during apply, and it can already declare a vault provider that's
authenticated against the local credential file
(`internal/workspace/apply.go:746`). The personal vault is the
natural distribution mechanism — but the wiring to use it as a
credential source doesn't exist.

The user archetypes affected:

- **Multi-project contributor.** Belongs to two or more orgs, each
  with its own Infisical project. Has 2-3 laptops (work, personal,
  ephemeral cloud dev container). Today: re-creates entries on every
  machine. Tomorrow: opts into credential sync, populates the
  personal vault once, every machine fetches.
- **Frequent-rotator.** Their org rotates machine-identity secrets
  monthly. Today: manually patches every machine. Tomorrow: rotates
  in the vault, next apply on each machine picks up the new value.
- **New-machine bootstrapper.** Sets up a fresh laptop. Today:
  `infisical login`, then assembles the credential file by hand.
  Tomorrow: `infisical login`, `niwa config set global <overlay>`,
  `niwa apply` — credential-sync handles the rest.

## Goals

- Eliminate the "edit `provider-auth.toml` on every machine" step for
  developers who opt in to credential sync.
- Preserve the local credential file as the per-machine override
  layer — never break workflows that rely on it.
- Surface every credential source in `niwa status` so a user can
  always answer "which credential authenticated this provider?"
- Keep the existing vault threat model intact. Specifically, do not
  revisit or weaken R12 (no bulk team-provider replacement) or R31
  (override visibility).
- Add zero behavior change for users who don't opt in.

## User Stories

**US-1: Multi-project contributor sets up a new laptop.**
As a developer who works across two Infisical orgs and has just
unboxed a new laptop, I want `niwa apply` to authenticate against
both orgs without me hand-editing a credential file, so that the
fresh-machine setup time is minutes not hours.

*Assumes the developer previously populated their personal vault with
machine-identity entries (see Known Limitations: vault population is
manual).*

**US-2: Joining a new project.**
As a developer joining a third project whose team config references
a third Infisical org, I want to populate the credential once
(in my personal vault) and have it work on every laptop I own, so
that adding orgs is a one-time action.

**US-3: Rotating a client_secret.**
As a developer (or a security policy) rotating a client_secret, I
want to update one place (the personal vault) and have every
machine pick up the new value on next apply, so that rotation
doesn't require touching every laptop.

**US-4: Pinning a credential locally.**
As a developer debugging a vault-related issue, I want to override
one machine-identity entry with a hand-written value in
`provider-auth.toml` without removing it from the vault, so that
I can isolate the test without affecting other machines.

**US-5: Auditing what authenticated.**
As a developer (or someone reading my workspace over my shoulder),
I want to see which source supplied every credential niwa used in
the last apply — the local file, the personal vault, or the CLI
session — so that I can confirm the credential surface is what I
expect.

**US-6: Single-org developer is unaffected.**
As a developer who only ever uses the org I'm logged into via
`infisical login`, I want this feature to introduce zero new
behavior, configuration, or runtime cost, so that I never have to
think about it.

**US-7: Multi-org developer with no opt-in is unaffected.**
As a developer who today maintains a hand-edited `provider-auth.toml`
across multiple orgs and chooses not to opt into credential sync,
I want my existing setup to keep working byte-identically, so that
this feature imposes nothing on me.

## Requirements

The PRD uses RFC 2119 normative language: **MUST** is binding,
**SHOULD** is strong recommendation, **MAY** is permission.

### Functional

**R1 — Opt-in via global config.**

The personal overlay's `niwa.toml` MAY declare a top-level
`[global.machine_identities]` table with an optional `from` field
whose value is either:

- absent or empty: niwa uses the anonymous `[global.vault.provider]`
  declared in the same file. If no anonymous provider is declared,
  this is a parse-time error (R2).
- the name of a declared `[global.vault.providers.<name>]` block in
  the same overlay file. If the name doesn't match a declared
  provider, this is a parse-time error (R2).

When the `[global.machine_identities]` table is **absent**, the
feature is disabled. niwa MUST NOT consult the personal vault for
credentials. Behavior is byte-identical to the previous niwa release.

When the table is **present**, the feature is enabled regardless of
whether `from` is set. "Present but empty" still counts as opted in
and triggers parse-time validation (R2).

**R2 — Provider name validation at parse time.**

When `[global.machine_identities]` is present in the personal
overlay, niwa MUST validate at **parse time** (when the personal
overlay's `niwa.toml` is loaded at the start of every `niwa apply`)
that:

- if `from = "X"` is set, `X` matches a declared provider name in
  `[global.vault.providers.*]` in the same file; AND
- if `from` is absent or empty, an anonymous `[global.vault.provider]`
  is declared in the same file.

If either check fails, niwa MUST fail before any vault call with one
of these diagnostics:

- Missing named provider:
  > `[global.machine_identities] from = "X"` references unknown
  > vault provider. Declared providers in this file:
  > `[<comma-separated names>]`.
- Missing anonymous provider when `from` is empty:
  > `[global.machine_identities]` is enabled but no vault provider
  > is declared. Either add `[global.vault.provider]` (anonymous)
  > or `[global.vault.providers.<name>]` and set
  > `from = "<name>"`.

This validation mirrors `internal/config/validate_vault_refs.go:285-293`
and surfaces errors before the apply graph is built.

**R3 — Backend-agnostic credential pool.**

The local credential file (`provider-auth.toml`) and the personal
vault (when opted in) jointly populate a credential pool. Each
entry in the pool carries `(kind, project)` plus the backend-specific
auth fields. niwa MUST match a vault provider's `(kind, project)`
against this pool exactly as it matches against the local file
today (`internal/workspace/providerauth.go:102-122`).

**R4 — Local file wins on conflict.**

When the same `(kind, project)` pair has entries in both the local
file and the personal vault, niwa MUST use the local-file entry for
authentication. The vault-sourced entry MUST be recorded in audit
output (R11) but MUST NOT be used to authenticate.

This precedence is documented in the Precedence section above and
is the only supported policy in v1. A flag to invert (vault-wins) is
explicitly out of scope (see Out of Scope).

**R5 — Vault as augmentation, not replacement.**

In all user-facing surfaces (audit columns, stderr diagnostics,
documentation), niwa MUST describe vault-sourced credentials as
**augmenting** the local file or as **falling back** when a local
entry exists. niwa MUST NOT use the word **shadow** or **override**
in machine-identity-vault-sync diagnostics — those words have a
fixed meaning in personal-overlay-vs-team-config diagnostics and
the precedence direction is opposite.

**R6 — In-memory only, fetched lazily per apply.**

Vault-sourced credential entries MUST never be written to disk.
niwa MUST fetch a vault-sourced entry only when a workspace
references the corresponding `(kind, project)` pair during the
current apply. Unused entries MUST NOT be fetched. On every apply,
needed entries are re-fetched fresh — there is no token cache, no
disk cache, no in-memory cache that survives between applies.

This matches the existing no-token-cache invariant (vault-integration.md
§"Security notes": "No token cache. niwa re-authenticates on every
apply") and bounds latency to actual usage (R16).

**R7 — Schema convention published as a contract.**

For Infisical, the credential body for a given project MUST be
stored at the path `/niwa/provider-auth/infisical/<project-uuid>` as
a single secret value whose body is a TOML document with the shape:

```toml
version = "1"
client_id = "<your-infisical-machine-identity-client-id>"
client_secret = "<your-infisical-machine-identity-client-secret>"
api_url = "https://app.infisical.com"   # optional; omit for default
```

The `<project-uuid>` segment MUST be the same UUID that appears in
the workspace's `[vault.provider]` `project` field.

niwa MUST NOT auto-discover entries; it asks for exactly the keys
it needs based on which `(kind, project)` pairs the resolved vault
registries reference. There is no manifest, no list call.

Other backends added in the future use parallel paths
(`/niwa/provider-auth/<kind>/<key>`) with their own body shape
defined in their own PRD/design.

**R8 — Body version validation (vault entries only).**

When niwa fetches a vault-sourced credential body, it MUST:

1. Parse the body as TOML.
2. Read the top-level `version` field.
3. If `version` is missing entirely, treat the body as version `"1"`
   (forward-compatible default for entries that predate versioning).
4. If `version` is present and not equal to `"1"`, fail apply with
   exit code non-zero and the diagnostic:
   > `provider-auth body at /niwa/provider-auth/<kind>/<project> has
   > unsupported schema version "X"; this niwa version supports v1.
   > Upgrade niwa or update the vault entry.`
5. Only after the version check passes, attempt to extract
   `client_id`, `client_secret`, and optional `api_url`.

niwa MUST NOT version-check entries from the local credential file.
Local entries continue to use today's parsing rules; a future schema
change can introduce its own opt-in.

**R9 — Personal vault MUST authenticate via CLI session, not via
the credential pool.**

niwa MUST validate at parse time that the personal vault provider's
`(kind, project)` does NOT match any entry in the credential pool
(local file or vault-sourced). If a match exists, niwa MUST fail
with the diagnostic:

> Personal vault provider (kind=`<k>`, project=`<p>`) cannot be
> bootstrapped by an entry in the local credential pool — this would
> create a chicken-and-egg cycle. Authenticate the personal vault
> via CLI session (`infisical login` for Infisical) instead.

Concrete examples (Infisical):

- Vault provider is `(infisical, abc-uuid)` and local file has an
  entry for `(infisical, abc-uuid)` → **error**.
- Vault provider is `(infisical, abc-uuid)` and local file has only
  `(infisical, xyz-uuid)` → **OK**.
- Vault provider is `(infisical, abc-uuid)` and a vault-sourced
  entry for `(infisical, abc-uuid)` is also discoverable in the
  same vault → **error**.

The match check applies to both layers of the pool. The error
surfaces at parse time so apply fails before any vault call.

**R10 — Personal vault is the only credential-sync source.**

Workspace team configs MUST NOT supply machine-identity entries.
Only the personal overlay's vault provider (referenced via
`[global.machine_identities]`) can be the source. Team configs are
public-facing and must not be wired into a credential distribution
path.

If a future user attempts to point `from` at a provider name that
exists in the team config but not in the personal overlay, R12 (the
existing collision rule from PRD-vault-integration) already prevents
the personal overlay from declaring a provider with the same name.
The R2 parse-time check therefore catches this case naturally
(the name is not in the personal overlay's declared providers). The
diagnostic SHOULD reference the personal-vs-team distinction:

> The named provider "X" is not declared in your personal overlay.
> machine-identity-vault-sync only uses personal-overlay vault
> providers. Either add `[global.vault.providers.X]` to your
> personal overlay or change `from` to a declared name.

**R11 — Audit surface (offline).**

niwa MUST add a `--audit-auth` flag to `niwa status` (mirroring the
existing `--audit-secrets` flag's command shape). The view MUST be
fully offline — it reads the credential-source record persisted by
the most recent apply (in `state.json`) and MUST NOT make any vault
calls, network calls, or backend authentication calls.

The output MUST be a fixed-column text table with this header and
column meaning:

```
KIND       PROJECT-UUID                          SOURCE              FALLBACK
infisical  550e8400-e29b-41d4-a716-446655440000  local-file          vault:personal
infisical  660f9511-f40c-52e5-b827-557766551111  vault:personal      —
infisical  770a0622-a51d-63f6-c938-668877662222  cli-session         —
infisical  880b1733-b62e-74a7-d949-779988773333  none                —
```

| Column | Meaning |
|--------|---------|
| `KIND` | Backend kind (e.g., `infisical`) |
| `PROJECT-UUID` | The `(kind, project)` identifier niwa needed credentials for |
| `SOURCE` | Where the credential came from in the last apply: `local-file`, `vault:<name>` (provider name; empty string for anonymous), `cli-session`, or `none` |
| `FALLBACK` | The non-active source that ALSO had an entry, if any. `vault:<name>` when the local file won; otherwise `—` |

When the same `(kind, project)` has entries in both layers, the
SOURCE column shows the layer that won (local-file) and FALLBACK
shows the layer that lost (vault:<name>). When only one layer has
an entry, FALLBACK is `—`.

Exit codes:

- **0** if every needed `(kind, project)` resolved to at least one
  entry in the last apply (regardless of fallback presence).
- **non-zero** if any row has SOURCE = `none`.

A future `--check-vault` flag (online verification of vault entries)
is explicitly deferred to v1.1; see Out of Scope.

**R12 — Apply-time stderr signal for vault-sourced credentials.**

On every apply that uses at least one vault-sourced credential, niwa
MUST emit one stderr line per **unique `(kind, project)` pair**
sourced from the vault. Shape:

```
auth: <kind>/<project-uuid> source=vault:<name>
```

Two `(kind, project)` pairs that happen to share a vault provider
name produce two lines, not one. The grouping is per pair.

When a local-file entry exists AND covers the same `(kind, project)`
as a vault entry (the conflict case from R4), niwa MUST also emit
one fallback line per such pair:

```
auth: <kind>/<project-uuid> source=local-file fallback=vault:<name>
```

niwa MUST NOT emit any line for `(kind, project)` pairs sourced from
the local file with no vault fallback, or from the CLI session, to
avoid noise on the no-vault-sync code path.

**R13 — Failure-mode contract.**

| # | Failure | Behavior | Exit code |
|---|---------|----------|-----------|
| 1 | Personal vault unreachable (network down, CLI not installed, not logged in) AND every needed `(kind, project)` has an entry in the local file or a working CLI session | Single stderr warning naming the unreachable provider. Apply continues. | 0 |
| 2 | Personal vault unreachable AND at least one needed `(kind, project)` has no local-file or CLI-session fallback | Single stderr warning naming the unreachable provider, then hard error from the backend's auth call for the missing credential. | non-zero |
| 3 | Personal vault reachable, but the conventional key for a `(kind, project)` is absent from the vault | Silent. Falls through to local file or CLI session. Audit (R11) shows the resolved source. | 0 (unless a downstream auth fails) |
| 4 | Vault-sourced body fails to parse as TOML | Hard error. Diagnostic: "vault-sourced provider-auth body at `/niwa/provider-auth/<kind>/<project>` is malformed: `<TOML parse error>`." | non-zero |
| 5 | Vault-sourced body parses but is missing a required field (`client_id` or `client_secret`) | Hard error. Diagnostic: "vault-sourced provider-auth body at `/niwa/provider-auth/<kind>/<project>` is missing required field `<field>`." | non-zero |
| 6 | Vault-sourced body is well-formed but the credentials are invalid (e.g., `client_secret` was rotated and is now stale) | Hard error from the backend's auth call (HTTP 401 from Infisical). Diagnostic includes the `(kind, project)` pair and the backend's error. | non-zero |
| 7 | Vault-sourced body has `version = "X"` where X != "1" | Hard error per R8. | non-zero |
| 8 | Opted in (`[global.machine_identities]` present), `from = "missing"` | Parse-time error per R2. | non-zero |
| 9 | Opted in but no vault provider declared at all | Parse-time error per R2. | non-zero |
| 10 | Opted in AND personal vault's own `(kind, project)` matches a credential pool entry | Parse-time error per R9 (chicken-and-egg). | non-zero |

For row 1, the stderr warning shape is:

```
warning: personal vault provider <name> unreachable; falling back
to local-file and cli-session credentials.
```

A single aggregated warning is emitted (not one per missed entry).

**R14 — No public-repo guardrail change.**

The plaintext-secrets guardrail walks `*.secrets` tables in workspace
configs (`internal/workspace/githubpublic.go:218-230`). Machine-identity
credentials live in the vault, not in any config file, so the
guardrail surface is unchanged. niwa MUST NOT extend the guardrail
for this feature.

This is recorded explicitly to dispel future confusion: opting in to
machine-identity sync from a public personal-overlay repo does not
expose secret values, only the topology (the chosen provider name
and project UUID, both already discoverable from the overlay file).

**R15 — Backward compatibility guarantees.**

niwa MUST guarantee zero behavior change for users who do not opt in,
including users who already maintain a `provider-auth.toml`. The
guarantee includes:

- A user with no `provider-auth.toml`, no `[global.machine_identities]`
  table, and only their `infisical login` session sees identical
  behavior to today's release.
- A user with an existing `provider-auth.toml` and no
  `[global.machine_identities]` table sees identical behavior to
  today's release: the local file remains the only credential source,
  with the same precedence and matching rules
  (`internal/workspace/providerauth.go:102-122`).
- No new warnings, no new errors, no new latency are introduced for
  non-opting-in users on any code path.

### Non-functional

**R16 — Latency budget (informative).**

The expected per-provider latency added by credential sync is the
sum of one Infisical export call (~100ms) plus one universal-auth
login (~100ms), giving ~200ms per `(kind, project)` pair sourced
from the vault on a given apply. For a workspace touching N
vault-sourced providers, total added latency is approximately
N × 200ms.

This budget is **informative**, not binding: the implementation
SHOULD aim for this range and MUST measure and document the
actual budget in the design doc and release notes. If actual
measured latency exceeds the budget by more than 50%, the
implementation team MUST surface the discrepancy and propose
either an optimization or a budget revision.

**R17 — No new on-disk surface.**

This feature MUST NOT create any new files under `~/.config/niwa/`
or anywhere else. Specifically:

- No credential cache file.
- No materialized merged provider-auth.toml snapshot.
- No vault token cache.

The only on-disk surfaces remain the existing optional
`~/.config/niwa/provider-auth.toml` and the existing
`~/.config/niwa/global/` overlay clone.

**R18 — No new MUST-redact surfaces.**

Vault-sourced credentials MUST flow through the same `secret.Value` /
`Redactor` pipeline as today's local-file credentials. The
implementation MUST NOT introduce any new log, stderr, or argv
surface that could leak a credential value and require new
redaction logic.

## Acceptance Criteria

### Opt-in and validation

- [ ] **AC-1**: A user with `[global.machine_identities] from = "X"`
      in their personal overlay, where `X` matches a declared
      `[global.vault.providers.X]`, has machine-identity entries
      fetched from that provider during apply.
- [ ] **AC-2**: A user with `[global.machine_identities]` (no `from`
      field) and a declared anonymous `[global.vault.provider]` has
      entries fetched from the anonymous provider during apply.
- [ ] **AC-3**: A user with `[global.machine_identities] from = "missing"`
      (where `missing` is not declared in any `[global.vault.providers.*]`)
      sees parse-time error exit non-zero with a diagnostic naming
      `missing` and listing declared provider names. No vault call is
      attempted.
- [ ] **AC-4**: A user with `[global.machine_identities]` (no `from`)
      and no declared anonymous `[global.vault.provider]` sees
      parse-time error exit non-zero with the diagnostic from R2.
- [ ] **AC-5**: A user with `[global.machine_identities] from = ""`
      (explicitly empty) and only named providers declared sees the
      same parse-time error as AC-4.

### Conflict resolution and audit

- [ ] **AC-6a**: When local file and vault both have an entry for the
      same `(kind, project)`, apply succeeds (assuming both credentials
      are valid).
- [ ] **AC-6b**: After such an apply, `niwa status --audit-auth` shows
      the row with SOURCE = `local-file` and FALLBACK = `vault:<name>`.
- [ ] **AC-7**: When only the local file has an entry, the row shows
      SOURCE = `local-file` and FALLBACK = `—`.
- [ ] **AC-8**: When only the vault has an entry, the row shows
      SOURCE = `vault:<name>` and FALLBACK = `—`.
- [ ] **AC-9**: When neither has an entry but the CLI session covers
      it, the row shows SOURCE = `cli-session` and FALLBACK = `—`.
- [ ] **AC-10**: When no source covers the `(kind, project)`, the row
      shows SOURCE = `none` and FALLBACK = `—`.
- [ ] **AC-11**: `niwa status --audit-auth` exits non-zero when at
      least one row has SOURCE = `none`; exits 0 otherwise.
- [ ] **AC-12**: `niwa status --audit-auth` makes zero network calls.
      Verified by running with the network disabled.

### Apply-time stderr

- [ ] **AC-13**: On every apply that uses at least one vault-sourced
      credential, exactly one `auth: <kind>/<project> source=vault:<name>`
      line per unique `(kind, project)` pair sourced from the vault is
      emitted to stderr.
- [ ] **AC-14**: When local file overrides vault for a `(kind, project)`,
      exactly one `auth: <kind>/<project> source=local-file fallback=vault:<name>`
      line is emitted to stderr per such pair.
- [ ] **AC-15**: No `auth:` stderr line is emitted for `(kind, project)`
      pairs sourced from the local file with no vault fallback or from
      the CLI session.

### Failure modes (R13 contract)

- [ ] **AC-16** (R13.1): When the personal vault is unreachable but
      every `(kind, project)` has a local-file entry, apply exits 0
      with exactly one stderr line: `warning: personal vault provider
      <name> unreachable; falling back to local-file and cli-session
      credentials.`
- [ ] **AC-17** (R13.2): When the personal vault is unreachable and
      at least one `(kind, project)` has no local-file entry and no
      CLI session, apply exits non-zero. The unreachable warning
      from AC-16 still appears.
- [ ] **AC-18** (R13.3): When the conventional key for a
      `(kind, project)` is absent from the reachable vault, apply
      exits 0 (assuming local-file or CLI-session covers it). No
      stderr line is emitted for the absent vault entry.
- [ ] **AC-19** (R13.4): When the vault-sourced body fails to parse
      as TOML (e.g., `not = valid toml [`), apply exits non-zero with
      a diagnostic containing `malformed`, the path
      `/niwa/provider-auth/<kind>/<project>`, and the underlying TOML
      parse error.
- [ ] **AC-20** (R13.5): When the vault-sourced body parses but is
      missing `client_id`, apply exits non-zero with a diagnostic
      naming `client_id` and the path.
- [ ] **AC-21** (R13.5): When the vault-sourced body parses but is
      missing `client_secret`, apply exits non-zero with a diagnostic
      naming `client_secret` and the path.
- [ ] **AC-22** (R13.6): When the vault-sourced body is well-formed
      but the credentials are invalid (Infisical returns HTTP 401 on
      the universal-auth call), apply exits non-zero with a diagnostic
      naming the `(kind, project)` and the backend's error.
- [ ] **AC-23** (R13.7): When a vault-sourced body has `version = "2"`,
      apply exits non-zero with a diagnostic containing `unsupported
      provider-auth schema version` and the version string.
- [ ] **AC-24** (R13.7 / R8): When a vault-sourced body omits the
      `version` field entirely, apply treats it as `version = "1"`
      and proceeds.

### Bootstrap protection

- [ ] **AC-25** (R9): When the personal vault provider's
      `(kind, project)` matches an entry in `provider-auth.toml`,
      apply exits non-zero at parse time with a chicken-and-egg
      diagnostic naming the conflict.
- [ ] **AC-26** (R9): When the personal vault provider's
      `(kind, project)` matches an entry that would be vault-sourced
      from the same vault, apply exits non-zero at parse time with
      the same chicken-and-egg diagnostic.
- [ ] **AC-27** (R9 positive case): When the personal vault provider
      has a `(kind, project)` that does not appear in the credential
      pool, the personal vault is authenticated via the CLI session
      and credential sync proceeds normally.

### Backward compatibility

- [ ] **AC-28** (R15): A user with no `provider-auth.toml`, no
      `[global.machine_identities]`, and a single Infisical session
      experiences zero behavior change relative to the current release.
      Verified by snapshotting stderr/stdout and exit code on a known
      apply scenario before and after this feature lands; output is
      byte-identical.
- [ ] **AC-29** (R15): A user with an existing `provider-auth.toml`
      and no `[global.machine_identities]` table sees the local file
      used as the sole credential source, with no warnings, no new
      errors, and no new latency.

### Filesystem and process invariants

- [ ] **AC-30** (R17): No new files are created under
      `~/.config/niwa/` by this feature on any code path. Verified
      by snapshotting the directory before and after each apply
      scenario in tests.
- [ ] **AC-31** (R6): On a second apply with the same input, vault
      entries are re-fetched (no in-memory or on-disk cache survives
      between apply invocations). Verified by counting vault export
      calls in a controlled test.

## Out of Scope

- **Replacing or shadowing team-declared vault providers.** This is
  the R12/D-9 rejected pattern in PRD-vault-integration. Not revisited.
- **Distributing user-payload secrets.** API keys, PATs, and other
  sensitive values used by tools at apply time already work via
  `vault://` references in `[env.secrets]` and need no new mechanism.
  This PRD is exclusively about authentication-bootstrap credentials.
- **Non-Infisical backends.** The schema convention (R7) is defined
  for Infisical only. sops + age and other backends remain
  v1.1-deferred per PRD-vault-integration. When they land, each
  backend defines its own body shape under
  `/niwa/provider-auth/<kind>/`.
- **Writing credentials to the vault from niwa.** No
  `niwa vault auth push` or analog. Credentials are populated by the
  user via the Infisical CLI or dashboard.
- **Auto-minting Infisical machine identities.** No Infisical
  admin-API call. Identity creation remains a manual dashboard step.
- **Cross-user / team-wide credential distribution.** Per-user
  distribution only. A team that wants every member to share a
  machine identity is explicitly outside the scope.
- **Removing or deprecating `provider-auth.toml`.** The local file
  remains the per-machine override layer indefinitely.
- **A flag to invert precedence (vault-wins).** Local-wins is the
  only supported policy in v1. If a future use case demonstrates
  the need (e.g., centrally-enforced rotation), revisit then.
- **Live vault verification in `niwa status`.** A `--check-vault`
  flag that re-fetches every vault entry to detect drift is
  deferred to v1.1. The v1 audit surface (R11) is offline only.
- **Audit metadata in the credential body.** Fields like rotation
  timestamp, rotated-by, or change reason are not part of the v1
  body schema. They could be added in a future schema version
  without breaking R7.

## Open Questions

This section MUST be empty before the PRD transitions to Accepted.

- *(none — all open questions from the draft were resolved during
  Phase 4 jury review.)*

## Known Limitations

- **Bootstrap requires `infisical login`.** A user setting up a
  fresh laptop must still run `infisical login` once before
  credential sync can fetch anything. This isn't a regression
  (today's setup also requires it for the default org), but it
  caps the achievable "zero-step bootstrap" experience.
- **Personal vault is single-backend in v1.** The opt-in points at
  one provider declaration. A user whose personal credentials are
  split across two Infisical orgs (or across Infisical + a future
  backend) would need to consolidate or wait for a future feature
  to declare multiple credential sources.
- **No rotation observability built in.** A user can rotate a
  client_secret in the vault, but niwa doesn't surface "this credential
  was different on the last apply" without an explicit re-fetch
  (deferred to the v1.1 `--check-vault` flag).
- **Vault round-trip per used provider per apply.** The no-cache,
  lazy-fetch stance costs ~200ms per used provider per apply.
  Acceptable for v1; revisit if user feedback shows pain.
- **Vault population is manual.** This PRD does not define a
  mechanism for auto-minting or bulk-importing machine identities
  into the personal vault. Users populate entries via the Infisical
  CLI (`infisical secrets set`) or the dashboard. The user guide
  bundled with the implementation will document the recommended
  workflow.

## Decisions and Trade-offs

**D-1: Local file wins on conflict (vault augments local).**
Decision: when the same `(kind, project)` has entries in both the
local file and the vault, the local-file entry is used.
Alternatives considered: vault-wins (centrally-managed-rotation
story), vault-only-on-opt-in (no overlap permitted).
Reasoning: local-wins gives the developer a fast per-machine
override path (e.g., for debugging a stale credential). Vault-wins
would inhibit local override entirely, which is a larger commitment
than v1 should make. Vault-only would force the user to commit to
the vault for every entry, which makes adoption harder. The local
file MUST also be authoritative when the vault is unreachable, which
mechanically pushes toward local-wins.

**D-2: In-memory only, no on-disk cache, lazy fetch.**
Decision: re-fetch credentials on every apply, only for the
`(kind, project)` pairs the apply needs. Never write to disk.
Alternatives considered: write-through cache to provider-auth.toml,
opt-in cache flag, eager fetch of every declared entry.
Reasoning: matches the existing no-token-cache invariant
(vault-integration.md). Eliminates "who edited the file last"
ambiguity. Lazy fetch bounds the latency cost to actual usage —
a workspace that doesn't reference a given org pays nothing for
that org's vault entry.

**D-3: Personal vault auths via CLI session, never via itself.**
Decision: validation rejects configurations where the personal
vault's `(kind, project)` matches a credential pool entry.
Alternatives considered: support self-auth via the local file (i.e.,
the personal vault's bootstrap credential lives in
provider-auth.toml).
Reasoning: self-auth via the local file is technically supportable
but adds a mental model burden ("the credential file is mostly
irrelevant, except for one entry that bootstraps everything else").
Requiring CLI-session auth keeps the bootstrap story uniform: log
into your home org once, everything else flows from the vault. Users
whose home org isn't their personal Infisical org can fall back to
manually maintaining the one entry — no worse than today.

**D-4: Schema is one TOML body per project, with a `version` field.**
Decision: store the credential body as a TOML document at
`/niwa/provider-auth/<kind>/<project>` with a top-level `version =
"1"` field.
Alternatives considered: one Infisical key per credential field
(`client_id`, `client_secret`, `api_url` as separate keys); a JSON
body; no version field.
Reasoning: TOML matches niwa's config language uniformly. A single
key per project matches the user's mental model ("one identity per
project"). Per-field keys would multiply the user's vault
maintenance burden by 3x with no benefit. The `version` field gives
us a clear forward-compat story without forcing path changes for
schema evolution.

**D-5: Use "augmentation" / "fallback" vocabulary, not "shadow."**
Decision: in user-facing diagnostics, audit columns, and docs, use
the words "augmentation" (for the vault layer relative to the local
file) and "fallback" (for what the vault entry becomes when a local
entry exists).
Alternatives considered: reuse the existing "shadow" vocabulary
from personal-overlay-vs-team-config diagnostics.
Reasoning: "shadow" already has a fixed meaning in niwa where the
SHADOWING layer wins (personal overlay shadows team config; the
overlay value is used). Reusing the same word with the opposite
precedence direction would confuse users. New words for new
semantics.

**D-6: Public-repo guardrail unchanged.**
Decision: do not extend the plaintext-secrets guardrail. Document
explicitly that opting in to credential sync from a public personal
overlay does not expose secret values.
Alternatives considered: extend the guardrail to flag personal
overlays that opt in to credential sync from public repos.
Reasoning: the guardrail's purpose is to prevent plaintext secrets
in committed config files. Credential-sync stores values in the
vault, not in the file. Extending the guardrail would conflate
"file contains a secret" with "file references a secret-source,"
which is a category error. The PRD records the explicit no-change
decision so future contributors don't re-litigate it.

**D-7: Disabled by default; opt-in is explicit (declaration =
opt-in).**
Decision: the feature activates only when `[global.machine_identities]`
is present in the personal overlay's `niwa.toml`. Presence alone
opts in; the `from` field is for selecting which provider to use,
not for enabling/disabling.
Alternatives considered: auto-enable when a personal-overlay vault
provider is declared; require `enabled = true` in the table.
Reasoning: the personal overlay may declare vault providers for
many reasons unrelated to credential distribution. Implicit
activation would surprise users by changing what data niwa fetches
from a vault they thought of as "for env vars only." Explicit
opt-in via section presence is one extra config block and matches
the discoverable shape of other niwa config sections.

**D-8: Audit surface is offline (state.json) in v1.**
Decision: `niwa status --audit-auth` reads what the last apply
recorded, never makes vault calls. A `--check-vault` flag for live
verification is deferred to v1.1.
Alternatives considered: live by default (fetch from vault on every
status call); offline by default with `--check-vault` opt-in.
Reasoning: matches the existing `--audit-secrets` shape (which is
also fully offline). Keeps `niwa status` fast and runnable on
flights / behind firewalls. Live verification has a clear use case
(rotation drift detection) but is a separate feature with its own
UX considerations; punting to v1.1 keeps v1 small.

**D-9: Apply-time stderr is per `(kind, project)` pair, not
per-provider.**
Decision: emit one `auth:` line per unique `(kind, project)` pair
sourced from the vault, even when multiple pairs share a vault
provider name.
Alternatives considered: aggregate per provider name (`auth: 3
pairs via vault:personal`); aggregate everything (`auth: vault
sources used`).
Reasoning: per-pair is more verbose but gives the user a complete
picture of which credentials came from where. The expected typical
apply has 1–3 vault-sourced pairs, so verbosity is bounded. Users
who find it noisy can filter by `auth:` prefix.
