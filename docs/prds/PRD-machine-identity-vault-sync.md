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
  `~/.config/niwa/global/`. Carries `niwa.toml`. Already may declare
  `[global.vault.provider]` or `[global.vault.providers.<name>]`.
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
  `infisical login`, then assemble the credential file by hand.
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

## Requirements

### Functional

**R1 — Opt-in via global config.**
The personal overlay's `niwa.toml` may declare a top-level
`[global.machine_identities]` table with a `from` field whose value
is either:

- empty / unset, in which case niwa uses the anonymous
  `[global.vault.provider]` if declared and errors otherwise; or
- the name of a declared `[global.vault.providers.<name>]` block in
  the same overlay file.

When the table is absent, the feature is disabled and niwa's
behavior is byte-identical to today's.

**R2 — Provider name validation at parse time.**
When `[global.machine_identities] from = "X"` is set, niwa validates
at config-parse time that `X` matches a declared provider name in
the same file. If no match exists (or the field is set but no vault
providers are declared at all), niwa fails apply with a diagnostic
naming the offending value and listing declared provider names.
Mirrors `internal/config/validate_vault_refs.go:285-293`.

**R3 — Backend-agnostic credential pool.**
The local credential file (`provider-auth.toml`) and the personal
vault (when opted in) jointly populate a credential pool. Each
entry in the pool carries `(kind, project)` plus the backend-specific
auth fields. niwa matches a vault provider's `(kind, project)`
against this pool exactly as it matches against the local file
today (`internal/workspace/providerauth.go:102-122`).

**R4 — Local file wins on conflict.**
When the same `(kind, project)` pair has entries in both the local
file and the personal vault, the local-file entry is used. The
vault-sourced entry is recorded in audit output but not used for
authentication. This matches the "per-machine override" mental
model and keeps the local file authoritative when the vault is
unreachable.

**R5 — Vault as augmentation, not replacement.**
Vault-sourced credentials are described in user-facing surfaces as
**augmenting** or **falling back from** the local file, not as
**shadowing** or **overriding** it. This avoids vocabulary collision
with the existing personal-overlay-vs-team-config "shadow" diagnostics
which carry a different precedence direction.

**R6 — In-memory only, fetched per apply.**
Vault-sourced credential entries are never written to disk. Each
apply re-fetches the entries it needs. This matches the existing
no-token-cache invariant (vault-integration.md §"Security notes":
"No token cache. niwa re-authenticates on every apply").

**R7 — Schema convention published as a contract.**
For Infisical, the credential body for a given project is stored at
the path `/niwa/provider-auth/infisical/<project-uuid>` as a single
secret value whose body is a TOML document with the shape:

```toml
version = "1"
client_id = "..."
client_secret = "..."
api_url = "..."          # optional
```

Other backends added in the future use parallel paths
(`/niwa/provider-auth/<kind>/<key>`) with their own body shape. The
`<project-uuid>` segment is the same UUID that appears in the
workspace's `[vault.provider]` `project` field.

**R8 — Body version validation.**
niwa reads the `version` field of the fetched body and rejects any
value other than `"1"` with a clear diagnostic ("unsupported
provider-auth schema version X; upgrade niwa or use a v1-compatible
body"). Forward compatibility is achieved by introducing a new
version string when the body shape changes; old niwa versions fail
loudly rather than silently misinterpreting.

**R9 — Personal vault must auth via CLI session.**
The vault provider that supplies machine-identity entries is itself
authenticated via the CLI session (`infisical login` for Infisical),
never via an entry in the local file or via an entry sourced from
itself. This is enforced by validation: if the personal vault's
`(kind, project)` matches an entry in the local credential pool
(local-file or vault-sourced), niwa fails apply with a diagnostic
describing the chicken-and-egg cycle and the resolution
("authenticate the personal vault via CLI session, not via
provider-auth.toml").

**R10 — Personal vault is the only credential-sync source.**
Workspace team configs cannot supply machine-identity entries. Only
the personal overlay's vault provider can be the source. Team
configs are public-facing and must not be wired into a credential
distribution path.

**R11 — Audit surface.**
`niwa status --audit-auth` lists every `(kind, project)` pair niwa
needed credentials for during the last apply, with a column
identifying the source as one of:

| Source | Meaning |
|--------|---------|
| `local-file` | Entry came from `~/.config/niwa/provider-auth.toml` |
| `vault:<name>` | Entry came from the personal vault (provider name shown; empty for anonymous) |
| `cli-session` | No entry matched; niwa fell back to the CLI session token |
| `none` | No credential available; provider authentication will fail |

When the same `(kind, project)` has entries in both the local file
and the vault, both are shown with the local-file source marked
**ACTIVE** and the vault entry marked **FALLBACK**.

**R12 — Apply-time stderr signal for vault-sourced credentials.**
On every apply that uses at least one vault-sourced credential,
niwa emits a stderr line per provider listing the source it used.
Shape: `auth: <kind>/<project-uuid> source=vault:<name>`. No line
is emitted for `local-file` or `cli-session` sources to avoid noise
on the no-vault-sync code path. This mirrors the existing
`rotated <path>` stderr pattern.

**R13 — Failure-mode contract.**

| Failure | Behavior | Exit code |
|---------|----------|-----------|
| Personal vault unreachable (network down, CLI not installed) | Warn to stderr; fall back to local-file / cli-session for every entry. Apply continues. | 0 (apply succeeds if no required credentials are missing) |
| Conventional key absent in vault | Silent. Treated as "no vault entry for this `(kind, project)`." Falls through to local-file / cli-session. Visible in audit. | 0 |
| Body malformed (TOML parse error, missing `client_id`/`client_secret`, wrong `version`) | Hard error. Apply fails. Diagnostic names the project UUID and the parse error. | non-zero |
| Body well-formed but credentials invalid (e.g., rotated `client_secret`) | Hard error from the backend's auth call. Same wording style as today's manual-credentials path. | non-zero |
| Opted in (`from` is set) but the named provider doesn't exist | Hard error at parse time (R2). | non-zero |
| Opted in via anonymous default but no `[global.vault.provider]` declared | Hard error at parse time. Diagnostic names the missing declaration. | non-zero |

**R14 — No public-repo guardrail change.**
The plaintext-secrets guardrail walks `*.secrets` tables in workspace
configs. Machine-identity credentials live in the vault, not in any
config file, so the guardrail surface is unchanged. The PRD records
this explicitly to dispel future confusion: opting in to
machine-identity sync from a public personal-overlay repo does not
expose secret values, only the topology (the chosen provider name
and project UUID, both already discoverable from the overlay file).

**R15 — Single-org users see no behavior change.**
A user who has no `~/.config/niwa/provider-auth.toml`, has no
`[global.machine_identities]` table, and uses only their
`infisical login` session continues to work identically. No new
config required, no new error paths exercised, no new latency.

### Non-functional

**R16 — Latency budget.**
For a workspace touching N Infisical projects whose credentials are
sourced from the personal vault, total apply-time latency increase
is bounded by N × (one Infisical export call + one universal-auth
login) ≈ N × 200ms. The existing per-org auth latency was already
~100ms; the additional ~100ms per provider is the export call. Any
implementation must measure and document the actual budget.

**R17 — No new disk surface.**
This feature introduces zero new on-disk files. No cache, no
materialized credential snapshot, no token store. The only on-disk
surfaces remain: `~/.config/niwa/provider-auth.toml` (today's
optional file, unchanged) and `~/.config/niwa/global/` (today's
overlay clone, unchanged).

**R18 — No new MUST-redact surfaces.**
Vault-sourced credentials flow through the same `secret.Value` /
`Redactor` pipeline as today's local-file credentials. No new
log/stderr/argv surfaces are introduced that need new redaction
logic.

## Acceptance Criteria

- [ ] A user with `[global.machine_identities] from = "X"` in their
      personal overlay, where `X` matches a declared
      `[global.vault.providers.X]`, has machine-identity entries
      fetched from that provider on apply.
- [ ] A user with `[global.machine_identities]` (empty `from`) and
      a declared anonymous `[global.vault.provider]` has entries
      fetched from the anonymous provider.
- [ ] A user with `[global.machine_identities] from = "missing"`
      sees a parse-time error naming `missing` and listing declared
      provider names. Apply fails before any vault call.
- [ ] A user with `[global.machine_identities]` but no declared
      vault providers sees a parse-time error naming the missing
      `[global.vault.provider]` declaration.
- [ ] A user with both a local-file entry and a vault entry for the
      same `(kind, project)` has the local-file entry used for
      authentication, with the vault entry visible in
      `niwa status --audit-auth` as `FALLBACK`.
- [ ] When the personal vault is unreachable, apply continues using
      whatever the local file and CLI session can supply, with a
      stderr warning naming the unreachable provider.
- [ ] When a vault-sourced body has `version = "2"`, apply fails
      with a "unsupported provider-auth schema version" error.
- [ ] When a vault-sourced body is missing `client_secret`, apply
      fails with a "malformed provider-auth body" error naming the
      missing field.
- [ ] When the personal vault's own `(kind, project)` matches a
      credential entry (local-file or vault-sourced), apply fails
      with a chicken-and-egg diagnostic before any vault call.
- [ ] `niwa status --audit-auth` lists every `(kind, project)` niwa
      needed credentials for in the last apply, with the source
      column populated correctly.
- [ ] On every apply that uses at least one vault-sourced credential,
      stderr carries one `auth: <kind>/<project> source=vault:<name>`
      line per such credential.
- [ ] A user with no `provider-auth.toml`, no
      `[global.machine_identities]` table, and a single Infisical
      session experiences zero behavior change relative to the
      current release.
- [ ] No new files are created under `~/.config/niwa/` by this
      feature. `provider-auth.toml` and `global/` remain the only
      on-disk surfaces.

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

## Open Questions

- **Should `niwa status --audit-auth` trigger a vault fetch or work
  fully offline?** The existing `--audit-secrets` is fully offline
  (reads `state.json`). For `--audit-auth`, an offline view shows
  what the last apply did; a fetching view shows what the next apply
  will see. Probably want offline by default with an opt-in
  `--check-vault` analog. Decision deferred to design.
- **Should the apply-time stderr signal (R12) be aggregated or
  per-provider?** Per-provider is more verbose but clearer; aggregated
  ("auth: 3 providers via vault:personal") is cleaner. Probably
  per-provider for diagnostic value, but worth confirming with users.
- **Should the body schema include audit metadata (rotation timestamp,
  rotated-by user)?** Useful for forensics but adds shape that niwa
  doesn't act on. Defer to v1.1 unless a use case appears.

## Known Limitations

- **Bootstrap requires `infisical login`.** A user setting up a
  fresh laptop must still run `infisical login` once before
  credential sync can fetch anything. This isn't a regression
  (today's setup also requires it for the default org), but it
  caps the achievable "zero-step bootstrap" experience.
- **Personal vault is single-backend in v1.** The opt-in points at
  one provider declaration. A user whose personal credentials are
  split across two Infisical orgs (or across Infisical + a future
  backend) would need to consolidate or wait for v1.1 to declare
  multiple credential sources.
- **No rotation observability built in.** A user can rotate a
  client_secret in the vault, but niwa doesn't surface "this credential
  was different on the last apply" — the user finds out by the next
  apply succeeding. A `niwa status --audit-auth --check-vault` could
  add a drift signal; deferred to design.
- **Vault round-trip per provider per apply.** The no-cache stance
  costs ~200ms per provider per apply. For workspaces with many orgs,
  this adds up. Acceptable for v1; revisit if user feedback shows
  pain.

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
the vault for every entry, which makes adoption harder.

**D-2: In-memory only, no on-disk cache.**
Decision: re-fetch credentials on every apply. Never write to disk.
Alternatives considered: write-through cache to provider-auth.toml,
opt-in cache flag.
Reasoning: matches the existing no-token-cache invariant
(vault-integration.md). Eliminates "who edited the file last"
ambiguity. The ~200ms per provider per apply cost is acceptable
given today's apply latency.

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

**D-7: Disabled by default; opt-in is explicit.**
Decision: the feature activates only when `[global.machine_identities]`
is present in the personal overlay's `niwa.toml`. No implicit
activation based on the presence of a vault provider.
Alternatives considered: auto-enable when a personal-overlay vault
provider is declared.
Reasoning: the personal overlay may declare vault providers for
many reasons unrelated to credential distribution. Implicit
activation would surprise users by changing what data niwa fetches
from a vault they thought of as "for env vars only." Explicit
opt-in is one extra config line; the predictability is worth it.
