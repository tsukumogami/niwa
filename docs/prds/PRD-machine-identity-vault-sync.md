---
status: Done
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

Done.

**Last revised:** 2026-05 — amended to reflect two design changes
adopted after the initial Done milestone:

1. **Implicit credential sync (Alt 2).** The explicit
   `[global.machine_identities]` opt-in block is removed. Any
   anonymous `[global.vault.provider]` declared in the personal
   overlay automatically serves as both the `vault://`-URI-resolution
   provider and the credential-bootstrap source. Resolution chain
   unchanged: `provider-auth.toml` → personal-overlay vault →
   `cli-session`. Rationale and rejected alternatives are recorded
   in the machine-identity-vault-sync opt-in shape decision report
   (`wip/decision_machine_identity_optin_report.md`).
2. **`p-` prefix on vault credential keys.** Vault credential keys
   are now `Key: p-<project-uuid>` (the path
   `/niwa/provider-auth/<kind>` is unchanged). Infisical and
   likely other backends reject secret keys whose first character
   is a digit; ~37.5% of UUIDv4 values begin with a digit.

Acceptance criteria affected by these amendments are marked in line
below. Numbered ACs that were deleted (AC-3, AC-4, AC-5, AC-34) are
retained as gap-noted entries to preserve cross-references from
existing tests, design notes, and the implementation diff.

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
- **Personal vault**: the anonymous `[global.vault.provider]`
  declared in the personal overlay. This declaration is the single
  source of truth for "niwa may consult the personal vault" — it
  serves both as the `vault://`-URI-resolution provider and as the
  machine-identity credential-bootstrap source. Authenticated via
  the CLI session (`infisical login`) — never via an entry in the
  local credential file or in itself.
- **Credential entry**: one logical row pairing a `(kind, project)`
  identifier with the auth fields a backend needs (for Infisical:
  `client_id`, `client_secret`, optional `api_url`).
- **Credential pool**: the merged set of credential entries from all
  active sources (local file plus the personal vault, when the
  personal overlay declares an anonymous `[global.vault.provider]`)
  that niwa consults during apply.
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
  machine. Tomorrow: declares an anonymous personal-overlay vault,
  populates it once, every machine fetches.
- **Frequent-rotator.** Their org rotates machine-identity secrets
  monthly. Today: manually patches every machine. Tomorrow: rotates
  in the vault, next apply on each machine picks up the new value.
- **New-machine bootstrapper.** Sets up a fresh laptop. Today:
  `infisical login`, then assembles the credential file by hand.
  Tomorrow: `infisical login`, `niwa config set global <overlay>`,
  `niwa apply` — credential-sync handles the rest.

## Goals

- Eliminate the "edit `provider-auth.toml` on every machine" step
  for developers who declare an anonymous personal-overlay vault.
- Preserve the local credential file as the per-machine override
  layer — never break workflows that rely on it.
- Surface every credential source in `niwa status` so a user can
  always answer "which credential authenticated this provider?"
- Keep the existing vault threat model intact. Specifically, do not
  revisit or weaken R12 (no bulk team-provider replacement) or R31
  (override visibility).
- Add zero behavior change for users who do not declare an
  anonymous personal-overlay vault.

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

**US-7: Multi-org developer who hasn't declared a personal-overlay
vault is unaffected.**
As a developer who today maintains a hand-edited `provider-auth.toml`
across multiple orgs and has not declared an anonymous
`[global.vault.provider]` in my personal overlay, I want my existing
setup to keep working byte-identically, so that this feature imposes
nothing on me. (Population covered: developers using only named
`[global.vault.providers.<name>]` for `vault://` URI resolution, and
developers without any personal overlay vault declaration.)

## Requirements

The PRD uses RFC 2119 normative language: **MUST** is binding,
**SHOULD** is strong recommendation, **MAY** is permission.

### Functional

**R1 — Activation via personal-overlay vault declaration.**

When the personal overlay declares an anonymous
`[global.vault.provider]`, niwa MUST treat that provider as both the
`vault://`-URI-resolution provider AND the machine-identity
credential-bootstrap source. The declaration is the activation
signal — there is no separate opt-in construct.

When no anonymous personal-overlay vault is declared, niwa MUST NOT
consult any vault for machine-identity credentials. Behavior is
byte-identical to the previous niwa release in the no-credential-
sync code path.

Named providers under `[global.vault.providers.<name>]` participate
in `vault://` URI resolution but MUST NOT serve as the
credential-sync source. A user who wants both named providers for
URI resolution and credential sync MUST also declare an anonymous
`[global.vault.provider]` block.

The contract for `[global.vault.provider]` is therefore widened
relative to the pre-amendment design: one declaration, two roles.
This is documented in the user guide
(`docs/guides/machine-identity-vault-sync.md` §"Enabling credential
sync") and in the type-level documentation for the spec.

**R2 — Personal-overlay vault validation at parse time.**

R2 retains its parse-time validation role for the personal-overlay
vault declaration: when an anonymous `[global.vault.provider]` is
present, niwa MUST validate its `kind` and `project` fields at parse
time (when the personal overlay's `niwa.toml` is loaded at the start
of every `niwa apply`), surfacing any structural problems before the
apply graph is built. This validation mirrors
`internal/config/validate_vault_refs.go:285-293`.

The pre-amendment R2 surface — diagnostics for "machine_identities
is enabled but no vault provider declared" and "from = X references
unknown provider" — is removed. Both diagnostics referenced
constructs (`[global.machine_identities]`, `from = "X"`) that no
longer exist. Any user who had written either construct into a
personal overlay during the pre-amendment release sees a
standard-toml unknown-key parse error from the config loader (the
keys are no longer recognized); the user-facing migration message
is documented in the user guide.

Parse-time chicken-and-egg checks (R9) still fire and are
unchanged.

**R3 — Backend-agnostic credential pool.**

The local credential file (`provider-auth.toml`) and the personal
vault (when an anonymous `[global.vault.provider]` is declared)
jointly populate a credential pool. Each entry in the pool carries
`(kind, project)` plus the backend-specific auth fields. niwa MUST
match a vault provider's `(kind, project)` against this pool exactly
as it matches against the local file today
(`internal/workspace/providerauth.go:102-122`).

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
stored at:

```
Path: /niwa/provider-auth/infisical
Key:  p-<project-uuid>
```

The body is a single secret value whose contents are a TOML
document with the shape:

```toml
version = "1"
client_id = "<your-infisical-machine-identity-client-id>"
client_secret = "<your-infisical-machine-identity-client-secret>"
api_url = "https://app.infisical.com"   # optional; omit for default
```

The `<project-uuid>` segment MUST be the same UUID that appears in
the workspace's `[vault.provider]` `project` field. niwa MUST
prepend the literal `p-` prefix to that UUID before composing the
vault key. Lookups MUST NOT be attempted against the bare-UUID
key.

**Why the `p-` prefix.** Infisical (and likely other vault
backends) reject secret keys whose first character is a digit.
Roughly 37.5% of UUIDv4 values begin with a digit, so a bare-UUID
key would silently break for that fraction of project UUIDs without
any visible signal at the recipe-author level. Prefixing the **key**
with a stable letter — not the path — sidesteps the validation
while keeping path segments readable in the vault UI. niwa always
prepends `p-` and never falls back to the bare-UUID key on miss.

Other backends added in the future use parallel paths
(`/niwa/provider-auth/<kind>`) with their own body shape defined
in their own PRD/design. New backends SHOULD also use a stable
letter prefix on the key segment to avoid the same class of
leading-digit constraint, even if the specific backend does not
require it; this preserves a uniform user-visible vault layout
across kinds.

niwa MUST NOT auto-discover entries; it asks for exactly the keys
it needs based on which `(kind, project)` pairs the resolved vault
registries reference. There is no manifest, no list call.

**R8 — Body version validation (vault entries only).**

When niwa fetches a vault-sourced credential body, it MUST:

1. Parse the body as TOML.
2. Read the top-level `version` field.
3. If `version` is missing entirely, treat the body as version `"1"`
   (forward-compatible default for entries that predate versioning).
4. If `version` is present and not equal to `"1"`, fail apply with
   exit code non-zero and the diagnostic:
   > `provider-auth body at /niwa/provider-auth/<kind>/p-<project>
   > has unsupported schema version "X"; this niwa version supports
   > v1. Upgrade niwa or update the vault entry.`
5. Only after the version check passes, attempt to extract
   `client_id`, `client_secret`, and optional `api_url`.

niwa MUST NOT version-check entries from the local credential file.
Local entries continue to use today's parsing rules; a future schema
change can introduce its own opt-in.

**R9 — Personal vault MUST authenticate via CLI session, not via
the credential pool.**

When the personal overlay declares an anonymous
`[global.vault.provider]`, niwa MUST validate at parse time that
the provider's `(kind, project)` does NOT match any entry in the
credential pool (local file or vault-sourced). If a match exists,
niwa MUST fail with the diagnostic:

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
Only an anonymous `[global.vault.provider]` declared in the
personal overlay can be the source. Team configs are public-facing
and must not be wired into a credential distribution path.

A team config may declare named `[vault.providers.<name>]` blocks
for `vault://` URI resolution; those participate in URI resolution
and remain governed by R12 (the existing collision rule from
PRD-vault-integration), but they MUST NOT be used as a credential-
sync source. The implementation enforces this by deriving the
credential-sync provider exclusively from the personal overlay's
anonymous vault declaration: there is no syntax that lets a user
point credential sync at a team-declared provider.

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
| `SOURCE` | Where the credential came from in the last apply: `local-file`, `vault:<name>` (provider name), `cli-session`, or `none`. When the credential-sync provider is the anonymous `[global.vault.provider]`, the column renders `vault:(anonymous)`. niwa MUST NOT emit a bare trailing colon (`vault:`) — `(anonymous)` is the literal placeholder. |
| `FALLBACK` | The non-active source that ALSO had an entry, if any. `vault:<name>` (or `vault:(anonymous)`) when the local file won; otherwise `—` |

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

**Persistence contract.** The audit data is persisted as a new
field on `InstanceState` in `state.json`. The on-disk schema bumps
from version 3 to version 4. The new field is keyed by
`"<kind>/<project>"` and carries `{source, fallback}` strings
only — both fields are categorical identifiers, never credential
bytes. Concretely, the JSON schema for one row is:

```json
{
  "source":   "local-file" | "vault:<name>" | "vault:(anonymous)" | "cli-session" | "none",
  "fallback": "vault:<name>" | "vault:(anonymous)" | ""
}
```

The map is repopulated atomically on every successful apply; failed
applies leave the previous map intact (existing state-save semantics).
The schema bump is forward-only — see Known Limitations.

**R12 — Apply-time stderr signal for vault-sourced credentials.**

On every apply that uses at least one vault-sourced credential, niwa
MUST emit one stderr line per **unique `(kind, project)` pair**
sourced from the vault. Shape:

```
auth: <kind>/<project-uuid> source=vault:<name>
```

When the credential-sync provider is the anonymous
`[global.vault.provider]`, `<name>` renders as `(anonymous)` —
the same convention as R11's audit column. niwa MUST NOT emit a
bare trailing colon.

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
| 4 | Vault-sourced body fails to parse as TOML | Hard error. Diagnostic: "vault-sourced provider-auth body at `/niwa/provider-auth/<kind>/p-<project>` is malformed: `<TOML parse error>`." | non-zero |
| 5 | Vault-sourced body parses but is missing a required field (`client_id` or `client_secret`) | Hard error. Diagnostic: "vault-sourced provider-auth body at `/niwa/provider-auth/<kind>/p-<project>` is missing required field `<field>`." | non-zero |
| 6 | Vault-sourced body is well-formed but the credentials are invalid (e.g., `client_secret` was rotated and is now stale) | Hard error from the backend's auth call (HTTP 401 from Infisical). Diagnostic includes the `(kind, project)` pair and the backend's error. | non-zero |
| 7 | Vault-sourced body has `version = "X"` where X != "1" | Hard error per R8. | non-zero |
| 10 | Personal-overlay vault declared AND personal vault's own `(kind, project)` matches a credential pool entry | Parse-time error per R9 (chicken-and-egg). | non-zero |

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

This is recorded explicitly to dispel future confusion: declaring
an anonymous `[global.vault.provider]` from a public personal-
overlay repo does not expose secret values, only the topology (the
chosen provider kind and project UUID, both already discoverable
from the overlay file).

**R15 — Backward compatibility guarantees.**

niwa MUST guarantee zero behavior change for users who have not
declared an anonymous `[global.vault.provider]` in their personal
overlay, including users who already maintain a `provider-auth.toml`.
The guarantee includes:

- A user with no `provider-auth.toml`, no anonymous personal-overlay
  vault, and only their `infisical login` session sees identical
  behavior to today's release.
- A user with an existing `provider-auth.toml` and no anonymous
  personal-overlay vault sees identical behavior to today's release:
  the local file remains the only credential source, with the same
  precedence and matching rules
  (`internal/workspace/providerauth.go:102-122`).
- No new warnings, no new errors, no new latency are introduced for
  these users on any code path.

Users who declare an anonymous personal-overlay vault for `vault://`
URI resolution but have no machine-identity entries populated under
`/niwa/provider-auth/<kind>/p-<project>` see one extra `Resolve` call
per `(kind, project)` pair per apply, hitting R13 row 3 (silent
fallthrough on miss). This is the documented cost of widening the
anonymous-vault contract to cover credential bootstrap; it is
acknowledged as a known limitation of the Alt 2 design.

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

> **AC numbering note.** AC-3, AC-4, AC-5, and AC-34 were removed
> by the Alt 2 + p-prefix amendment because they exercised parser
> validation for the now-deleted `[global.machine_identities]`
> construct. To preserve cross-references from existing tests and
> the implementation diff, the AC numbers are NOT compacted: gaps
> remain at 3, 4, 5, 34, with a one-line "removed" entry in place
> of each. New ACs introduced by the amendment are appended at
> AC-NEW-1 / AC-NEW-2 to avoid collisions.

### Activation and validation

- [ ] **AC-1**: A user with an anonymous `[global.vault.provider]`
      declared in their personal overlay sees credential sync
      activate: machine-identity entries are fetched from that
      provider during apply, and `niwa status --audit-auth` renders
      the SOURCE column as `vault:(anonymous)` for any
      `(kind, project)` pair the vault covered. Verified by
      `internal/config/...` parser tests and
      `internal/workspace/credentialpool_*_test.go`.
- [ ] **AC-2**: A user with no anonymous personal-overlay vault sees
      no credential-sync activation. Behavior is byte-identical to
      the previous niwa release on this code path. Verified by the
      backward-compatibility snapshot tests (AC-28, AC-29).
- [ ] ~~**AC-3**~~ *(removed by Alt 2 amendment.* The `from = "missing"`
      diagnostic exercised
      `[global.machine_identities] from = "X"`, which no longer
      exists. Unknown-key parse errors from the standard config
      loader cover the migration message instead.*)*
- [ ] ~~**AC-4**~~ *(removed by Alt 2 amendment.* The "no anonymous
      vault declared" diagnostic exercised the now-deleted
      `[global.machine_identities]` construct.*)*
- [ ] ~~**AC-5**~~ *(removed by Alt 2 amendment.* The `from = ""`
      diagnostic exercised the now-deleted `from` field.*)*

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
      `/niwa/provider-auth/<kind>/p-<project>`, and the underlying
      TOML parse error.
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
      anonymous personal-overlay vault declared, and a single
      Infisical session experiences zero behavior change relative
      to the previous niwa release. Verified by snapshotting
      stderr/stdout and exit code on a known apply scenario before
      and after this feature lands; output is byte-identical.
- [ ] **AC-29** (R15): A user with an existing `provider-auth.toml`
      and no anonymous personal-overlay vault declared sees the
      local file used as the sole credential source, with no
      warnings, no new errors, and no new latency.

### Filesystem and process invariants

- [ ] **AC-30** (R17): No new files are created under
      `~/.config/niwa/` by this feature on any code path. Verified
      by snapshotting the directory before and after each apply
      scenario in tests.
- [ ] **AC-31** (R6): On a second apply with the same input, vault
      entries are re-fetched (no in-memory or on-disk cache survives
      between apply invocations). Verified by counting vault export
      calls in a controlled test.

### Pool semantics, vocabulary, and remaining requirement coverage

- [ ] **AC-32** (R3): The credential pool's matching for
      vault-sourced entries follows the same `(kind, project)` rule
      as for local-file entries. Verified by populating the personal
      vault with an entry whose `(kind, project)` matches the
      workspace's `[vault.provider]`, leaving the local file empty,
      and asserting that apply authenticates against the vault-
      sourced credential. Verified independently of AC-1/AC-2 by
      matching on the project UUID rather than on the activation
      signal (anonymous-vault declaration).
- [ ] **AC-33** (R5): No diagnostic, audit column header, stderr
      line, or doc string introduced by this feature contains the
      words "shadow" or "override" in the machine-identity-vault-sync
      surfaces. Verified by a regex grep test in CI over the new
      source files plus the user guide.
- [ ] ~~**AC-34**~~ *(removed by Alt 2 amendment.* The original
      AC-34 covered the "named team-config provider redirected via
      `from = "X"`" case, which no longer exists. Team-config
      providers cannot be the credential-sync source under any
      syntax — that property is now structurally enforced (R10),
      not parser-validated.*)*
- [ ] **AC-35** (R14): The plaintext-secrets guardrail's behavior
      is byte-identical with and without an anonymous personal-
      overlay vault declared in a public personal overlay. Verified
      by snapshotting guardrail output for a representative
      public-overlay scenario before and after this feature lands.
- [ ] **AC-36** (R18): Error chains from R13.4 / R13.5 / R13.7 do
      not contain credential body bytes — only the path
      `/niwa/provider-auth/<kind>/p-<project>` and the missing
      field name (or schema version string). Verified by a unit
      test that asserts each error message rendered against a body
      containing sentinel byte sequences (e.g.,
      `client_secret = "TESTCANARY"`) does not contain the sentinel.
- [ ] **AC-37** (R6 lazy verification): A workspace that does not
      reference a given `(kind, project)` pair in any vault registry
      MUST NOT trigger a `vault.Provider.Resolve` call for that
      pair. Verified by configuring a fake vault that records every
      `Resolve` invocation and asserting absence for un-referenced
      pairs.
- [ ] **AC-38** (R7 canonical path): The credential-body fetch path
      uses the unmodified `<project-uuid>` segment exactly as it
      appears in the workspace's `[vault.provider]` `project` field
      (no case-folding, no normalization). The `p-` prefix is
      prepended verbatim ahead of the segment. Verified by
      configuring a project UUID in mixed case and asserting the
      vault `Resolve` ref carries `p-<MixedCaseUUID>` preserving the
      original case.

### Anonymous-provider rendering

- [ ] **AC-39**: When the credential-sync provider is the anonymous
      `[global.vault.provider]`, `niwa status --audit-auth` renders
      the SOURCE column as `vault:(anonymous)`, the FALLBACK column
      uses the same form when applicable, and the R12 stderr line
      uses `source=vault:(anonymous)`. niwa MUST NOT emit a bare
      `vault:` token (trailing colon with empty name).

### `p-` key prefix (R7 amendment)

- [ ] **AC-NEW-1** (R7): Vault credential keys carry the `p-`
      prefix. A test that populates
      `/niwa/provider-auth/infisical/<bare-uuid>` (no prefix) finds
      no entry — the credential-pool fetch records a miss for the
      `(kind, project)` pair, and the apply falls through to the
      next layer per R13 row 3. A test that populates
      `/niwa/provider-auth/infisical/p-<bare-uuid>` is hit and
      authenticates successfully. Verified by
      `internal/workspace/credentialpool_lazyvault_test.go::TestPool_PPrefixOnVaultKey`.
- [ ] **AC-NEW-2** (R7): Diagnostics rendered by
      `parseProviderAuthBody` (R13 rows 4, 5, 7) reference the
      prefixed path `/niwa/provider-auth/<kind>/p-<project>` so
      the user can locate the entry in the vault UI. Verified by
      the credential-pool parse-error tests
      (`internal/workspace/credentialpool_test.go`).

### Test plan notes (non-AC)

- The "local wins on conflict" verification (AC-6a / AC-6b) requires
  an Infisical-backend test double that returns distinct,
  identifiable responses per credential — for example, an HTTP-level
  fake that echoes a header derived from the supplied
  `client_id`/`client_secret`. Without a distinguishing oracle,
  AC-6b reduces to "the audit table records what the implementation
  wrote," which is self-confirming. The test plan that ships with
  Phase D MUST include such a fixture.

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
  `/niwa/provider-auth/<kind>` and SHOULD use a stable letter prefix
  on the key segment per R7.
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
- **Per-pair allowlist scoping.** Alt 4 from the opt-in shape
  decision report (an explicit list of `(kind, project)` pairs the
  user authorizes credential sync for) is deferred to a future
  release. It would block the adversarial-team-config substitution
  attack at the lookup layer rather than the consent layer; v1
  accepts that attack as out of scope per the threat-model section
  below. If usage shows the threat model warrants tighter scoping,
  revisit.

## Considered Alternatives

The opt-in shape decision report
(`wip/decision_machine_identity_optin_report.md` —
"machine-identity-vault-sync opt-in shape") evaluated four
alternatives in a parallel-validator bakeoff. Two were rejected
explicitly enough to record here:

- **Alt 1: Explicit opt-in via `[global.machine_identities]` block
  (status quo before this amendment).** The personal overlay
  declared an empty `[global.machine_identities]` table to opt in;
  presence alone activated credential sync, with an optional
  `from = "X"` field to route to a named provider. *Rejected*
  because the empty anonymous form was acknowledged as a syntactic
  marker carrying no data beyond its presence ("looks like
  boilerplate" is literally accurate per the validator analysis),
  the R12 apply-time stderr line already provides a per-apply
  consent signal independent of the block, and explicit opt-in
  does not defeat the adversarial-team-config substitution attack
  once a user has opted in (the keying is per-team-declared-
  project, not per-user-allowlist, under any of Alts 1-3). The
  marginal protection over Alt 2 was narrow — only users who
  declared a personal vault for `vault://` URI resolution but
  never opted in benefit, a population the decision report calls
  "shrinking."
- **Alt 4: Per-pair allowlist scoping.** The personal overlay
  would declare an explicit list of `(kind, project)` pairs niwa
  is willing to ask the personal vault about. *Rejected* as
  primary because every new Infisical org onboarding would
  require two writes (vault + overlay) and a new silent-
  fallthrough failure mode (populated-but-not-allowlisted) needs
  audit-time discoverability. Alt 4 remains the only design that
  blocks the adversarial-team-config substitution attack at the
  lookup layer; if the threat model is escalated in a future
  release, Alt 4 is the migration target. Implementation cost is
  +50–100 lines additive vs the current Alt 2 design.

Alt 3 (implicit + `credential_sync = false` opt-out) was rejected
as the consensus weakest of the four — it inherits Alt 2's
security weakness without inheriting Alt 2's simplicity, and is
dominated on every axis by some other alternative. See the
decision report for the full bakeoff.

## Threat Model and Security Considerations

The Alt 2 design accepts one specific attack as **out of scope
for v1**: the **adversarial-team-config substitution attack**. The
attack shape is:

1. An attacker with merge access to a team's dot-niwa repo flips
   `[vault.provider] project = "<X>"` to a UUID that the user
   happens to have populated in their personal vault for an
   unrelated reason.
2. niwa, on the next apply, asks the personal vault for the
   credential at `/niwa/provider-auth/<kind>/p-<X>`, finds the
   user-populated body, and authenticates against the attacker's
   project.

Decision: **out of scope for v1**, with the following rationale.

- **Probability is low.** UUIDs are random, so the attacker must
  either (a) know the user's personal-vault contents (a much
  larger compromise that subsumes this attack) or (b) control an
  Infisical project whose UUID coincidentally matches one the
  user has populated. Both conditions are narrow.
- **The attack requires merge rights to a privileged repo.** An
  attacker with merge access to a team's dot-niwa repo already has
  simpler exploits available: hook scripts, koto recipes, and
  shell-init wiring all run with the user's privileges at apply
  time. The team-config review gate that already protects against
  those exploits also gates this redirect.
- **Alt 4 remains the migration path.** If future evidence shows
  the threat model warrants per-pair scoping, Alt 4 (above) is
  additive and can be adopted without breaking existing personal-
  overlay declarations.
- **The reviewer-attention objection is real but mitigable.** A
  reviewer scanning a team-config PR for "scary stuff" might not
  flag a UUID change. The user guide
  (`docs/guides/machine-identity-vault-sync.md`) documents the
  expanded contract for `[global.vault.provider]` so users
  reviewing team configs understand the redirect surface.

The non-substitution security properties are unchanged from the
pre-amendment design:

- Vault-content escape is impossible: the credential body at key
  `p-<X>` is scoped to project `<X>` and can only authenticate
  against `<X>`. A vault read of an entry the user populated
  themselves cannot leak credentials for an unintended project.
- niwa never writes to the personal vault (R17, AC-30). An
  attacker cannot inject bodies; they can only redirect which
  existing body niwa reads.
- The credential-bytes redaction surface (R18, AC-36) is
  unchanged.

## Decision References

- **Opt-in shape decision (Alt 2 vs Alt 1/3/4).** The
  machine-identity-vault-sync opt-in shape decision report
  (`wip/decision_machine_identity_optin_report.md`) records the
  full validator bakeoff, the diverging viewpoints on "silent
  vault extraction," and the explicit acceptance of the
  adversarial-team-config substitution attack as out of scope.
  This PRD's R1, R2, R10, R15, the AC removals (AC-3/4/5/34),
  and the threat-model section above all derive from that report.
- **`p-` key prefix (R7 amendment).** Adopted on the same branch
  as the Alt 2 implementation; the rationale (Infisical and
  likely other backends reject leading-digit secret keys; ~37.5%
  of UUIDv4 values affected) is recorded in R7 and the
  user guide (`docs/guides/machine-identity-vault-sync.md`
  §"Vault key schema").

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
- **Personal vault is single-backend in v1.** Credential sync uses
  the single anonymous `[global.vault.provider]` declared in the
  personal overlay. A user whose personal credentials are split
  across two Infisical orgs (or across Infisical + a future
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
- **`state.json` schema bump is forward-only.** This feature bumps
  the on-disk state schema from v3 to v4. A niwa binary that
  predates this feature cannot read a v4 state file (the existing
  forward-version check at `internal/workspace/state.go:246`
  rejects it). Pinning a niwa version below the feature's release
  after a single post-feature apply requires regenerating state
  by re-running `niwa apply` with the older binary, which in turn
  rewrites the file at v3. This is the same one-way property the
  v2→v3 bump shipped with; it is called out here so users
  understand the upgrade is not transparent.

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

**D-4: Schema is one TOML body per project, with a `version` field
and a `p-` key prefix.**
Decision: store the credential body as a TOML document at path
`/niwa/provider-auth/<kind>` under key `p-<project-uuid>` with a
top-level `version = "1"` field.
Alternatives considered: one Infisical key per credential field
(`client_id`, `client_secret`, `api_url` as separate keys); a JSON
body; no version field; embedding the UUID in the path segment
without a prefix.
Reasoning: TOML matches niwa's config language uniformly. A single
key per project matches the user's mental model ("one identity per
project"). Per-field keys would multiply the user's vault
maintenance burden by 3x with no benefit. The `version` field gives
us a clear forward-compat story without forcing path changes for
schema evolution. The `p-` key prefix sidesteps the leading-digit
secret-key constraint that Infisical (and likely other backends)
enforces; ~37.5% of UUIDv4 values begin with a digit, so a
bare-UUID key would silently break for that fraction of project
UUIDs. Prefixing the **key** rather than the path keeps path
segments readable in vault UIs while remaining structurally safe.

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
explicitly that declaring an anonymous personal-overlay vault from
a public personal-overlay repo does not expose secret values.
Alternatives considered: extend the guardrail to flag personal
overlays that declare a credential-sync-eligible vault from public
repos.
Reasoning: the guardrail's purpose is to prevent plaintext secrets
in committed config files. Credential-sync stores values in the
vault, not in the file. Extending the guardrail would conflate
"file contains a secret" with "file references a secret-source,"
which is a category error. The PRD records the explicit no-change
decision so future contributors don't re-litigate it.

**D-7: Activation is implicit in the anonymous personal-overlay
vault declaration (Alt 2).**
Decision: niwa treats the anonymous `[global.vault.provider]`
declared in the personal overlay as both the `vault://` URI-
resolution provider and the credential-bootstrap source. There is
no separate `[global.machine_identities]` opt-in construct.
Alternatives considered: explicit-opt-in block (Alt 1, the
pre-amendment design); implicit + opt-out flag (Alt 3); per-pair
allowlist (Alt 4). See "Considered Alternatives" section above
and the decision report
(`wip/decision_machine_identity_optin_report.md`).
Reasoning: the empty anonymous opt-in block under Alt 1 was
acknowledged as a syntactic marker carrying no data beyond its
presence. The R12 apply-time stderr line already provides a
per-apply consent signal independent of any block. Removing the
block reduces config surface, simplifies onboarding (one TOML
construct instead of two), and removes a class of parser
diagnostics that no longer correspond to user-meaningful errors.
The trade-off is acknowledged: users who declared a personal-
overlay vault for `vault://` URI resolution but never intended
credential bootstrap pay one extra `Resolve` call per
`(kind, project)` per apply (R13 row 3 silent fallthrough). The
contract for `[global.vault.provider]` widens to "URI resolution
plus credential bootstrap"; documentation reflects the expanded
contract.

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
