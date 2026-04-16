---
status: Draft
problem: |
  niwa's team-shared workspace config repos (e.g., tsukumogami/dot-niwa) are
  private today primarily because they store plaintext API tokens and
  personal access tokens. Developers hit this pain on two fronts: team-shared
  API tokens force the config repo to be private (losing OSS reach and
  making the team config harder to onboard), and per-user PATs scoped to
  different source orgs have no clean layering model today. niwa has no
  vault integration and no mechanism for separating team-shared secrets
  from per-user secrets, so every secret becomes a leak risk or an
  onboarding tax.
goals: |
  Make niwa's workspace configs safely publishable by moving all secret
  values into a pluggable vault, with a layering model that cleanly
  separates team-shared secrets (resolved the same for every developer) from
  personal secrets scoped per-project (my tsukumogami PAT for tsukumogami
  workspaces, my codespar PAT for codespar workspaces). Success: a team can
  move tsukumogami/dot-niwa from private to public without any secret
  leakage, new developers joining the team bootstrap vault access in under
  10 minutes using only a GitHub org membership, and the 12 "never leaks"
  security invariants are enforced at the type system, pipeline, and
  filesystem-permission levels.
---

# PRD: Vault Integration

## Status

Draft

## Glossary

- **Team config**: the workspace configuration repo (e.g.,
  `tsukumogami/dot-niwa`) that declares shared structure and
  team-owned secrets. Read by `niwa init <name> --from <repo>`.
- **Personal overlay**: a user-owned configuration layer that
  merges on top of the team config. Provides personal vault
  providers and per-scope bindings for team-declared requirements.
  Same thing as `GlobalOverride` / `GlobalConfigOverride` in Go
  types; "personal overlay" is the canonical name in this PRD.
  Stored in a repo named by `niwa config set global <org/repo>`.
- **Scope** (for vault resolution): the string key used to pick the
  right `[workspaces.<scope>]` block in the personal overlay. By
  default this is the workspace's `[[sources]][0].org`; the team
  config can override it via `[workspace].vault_scope`. R4 and R5.
- **Provider**: a named vault backend declared under `[vault.provider]`
  (anonymous singular) or `[vault.providers.<name>]` (named). Each
  config file's provider names are local to that file (R3, D-9).

## Problem Statement

niwa's `tsukumogami/dot-niwa` team config repo is private today primarily
because its `workspace.toml` carries plaintext API tokens. The same pattern
repeats for every team using niwa: any shared secret (Anthropic API key, an
OpenAI key, a Sentry DSN, a team-shared GitHub PAT for CI mirroring)
forces the whole config repo to be private, which defeats the config-as-
documentation value proposition and makes it harder for contributors
outside the org to learn from or improve public workspace patterns.

Separately, each developer has **personal** secrets — GitHub PATs scoped to
specific orgs, personal API keys, SSH keys for org-specific clones. Today
these live in ad-hoc dotfiles, shell exports, or (worst case) are pasted
into the team config by accident. niwa has the `GlobalOverride` mechanism
(introduced in v0.5.0) that layers a personal overlay repo over the team
config repo, but the overlay carries plaintext values too.

The three user archetypes affected:

- **Indie solo developer using niwa for personal projects.** Has one
  developer account, multiple PATs scoped to different orgs, wants
  `niwa apply` to "just work" without manually exporting env vars before
  every session.
- **Small-team lead (1–5 devs).** Maintains `org/dot-niwa` with a
  handful of team-shared API tokens. Wants to move the repo public to
  share with contributors. Can't, because of the secrets.
- **Small-team member.** Onboards to a new team. Today: clones the
  private dot-niwa repo, copies a `.env.example` and fills in team
  secrets someone DMs them, then adds personal PATs on top. Tomorrow:
  clones a public dot-niwa, runs a bootstrap command to unlock the team
  vault via their GitHub org membership, then adds personal PATs.

The current materialization pipeline compounds the problem. Env files are
written to disk at mode `0o644` (world-readable). `ManagedFile` hashes
every written file into `state.json`, including secret-bearing files.
`niwa status` reports drift by re-hashing content, so an upstream secret
rotation looks the same as local tampering. There's no `secret.Value`
opaque type, no argv-rejection policy, no materialization-time redaction.

Moving secrets into a vault is necessary to make team configs
publishable; fixing the materialization pipeline is necessary so the vault
migration doesn't introduce new leaks.

## Threat Model

The invariants (R21–R31) defend a specific perimeter. This section
names what's inside, what's outside, and where the boundaries are.

**Trusted** (niwa assumes these are not compromised):

- The user's local machine — filesystem, process memory, OS keychain.
- The vault provider CLI (`infisical`, `sops`, `age`, `op`, etc.) —
  niwa invokes these as subprocesses and trusts their stdout output.
- Git credentials on the machine used to clone config repos.

**Not defended against** (out of scope for niwa's threat model):

- Malicious processes running as the same user (can read `0o600` files).
- Physical laptop theft without full-disk encryption.
- Compromised vault provider credentials or a breached provider service.
- Root-level attackers on the machine.
- A compromised provider CLI binary on `$PATH` (trojan `infisical`).

**Explicitly defended against** (the invariants' raison d'etre):

- Accidental `git commit` of plaintext secrets into the config repo
  (R14/R30 guardrail, R25 `.local` + `.gitignore` defense).
- Accidental inclusion of secrets in CLAUDE.md shared with the team
  (R26 forbids secret interpolation in Markdown).
- Log / CI / stderr disclosure (R22 `secret.Value` redaction + error-
  wrapping coverage).
- Shell-history exposure (R21 no-argv secrets).
- Silent personal-overlay override injection (R31 override-visibility
  diagnostics).

**Trust boundaries** (where data crosses a security-relevant edge):

| Boundary | What crosses | Defense |
|----------|-------------|---------|
| Vault service ↔ vault CLI | Auth tokens, secret bytes | Provider's own auth (out of niwa's scope) |
| Vault CLI ↔ niwa process | Secret bytes on subprocess stdout | R22 wraps into `secret.Value` immediately; R22 error-wrapping scrubs provider stderr |
| niwa process ↔ disk | Materialized `.local.env`, `settings.local.json` | R24 `0o600` mode; R25 `.local` infix + `.gitignore` |
| Disk ↔ git push | Config repo contents | R14/R30 guardrail; R23 no-config-writeback |
| niwa process ↔ logs/stderr | Diagnostic messages, error output | R22 redaction; R27 no-status-content |
| Team config ↔ personal overlay | Key names, provider declarations | R3/D-9 file-local scoping; R31 override-visibility |

This model explicitly excludes sophisticated adversaries (state-level,
root-access, supply-chain attacks on provider binaries). niwa's job is
to prevent the accidents that happen during normal development — not
to be a zero-trust vault client.

## Goals

- **Team configs can be publishable.** A team using niwa can move their
  `dot-niwa` repo from private to public without any secret value ever
  appearing in git history, current content, or future content.
- **Per-org personal secret scoping works end-to-end.** A developer with
  separate PATs for `tsukumogami` and `codespar` can declare both in
  their personal overlay repo and have niwa pick the right one
  automatically when working on a workspace in either org.
- **New-member bootstrap is fast.** A new developer joining
  `tsukumogami` who already has GitHub org membership is productive
  on `niwa apply` in minutes, not hours. Target for v1 (Infisical):
  `infisical login` → browser OAuth → first successful `niwa apply`
  in under 2 minutes. The design doc defines the measurement
  protocol.
- **Apply-time resolution is imperceptible for typical workspaces.**
  Realistic workspaces (≤ ~20 vault references) resolve without
  user-visible latency. Larger workspaces emit a progress indicator
  so users aren't left wondering. The design doc defines the
  benchmark harness and concrete thresholds.
- **Zero new leak classes.** The feature ships with the "never leaks"
  invariants (R21–R30) enforced by the type system, pipeline, and
  filesystem permissions. Fixes a pre-existing `0o644` bug where
  materialized env and settings files were world-readable.
- **Pluggable backend from v1, with two peer backends optimized for
  different team preferences.** Infisical Cloud ships as the OOTB
  low-effort option (free tier covers 5 identities / 3 projects /
  3 envs; bootstrap is `infisical login` → browser OAuth → done). sops +
  age ships as the vendor-neutral / git-native option (zero cost, zero
  hosted service, but requires age-key management). Neither is "the
  default"; users pick based on whether they want hosted
  convenience or fully-offline gitops. The interface allows additional
  backends (Doppler, 1Password, HashiCorp Vault OSS, Bitwarden Secrets
  Manager) to be added without rearchitecting.

**Goal traceability to requirements.** The five goals above map to
requirements this PRD enumerates:

| Goal | Satisfied by |
|------|--------------|
| Team configs can be publishable | R14, R30, R22–R29 (the invariant floor that lets secrets stay out of the repo) |
| Per-org personal secret scoping | R4, R5, R6, US-3, D-2 |
| New-member bootstrap is fast | US-2 (Infisical bootstrap in v1), R1 (CLI-exec interface means no niwa-specific auth bootstrap). Measurement protocol deferred to the design doc. |
| Apply-time resolution imperceptible for typical workspaces | R16 (re-resolve on every apply), R1 (interface allows session caching inside provider CLIs). Benchmark harness deferred to the design doc. |
| Zero new leak classes | R21–R31 (the "never leaks" invariants, including override visibility) |
| Pluggable backend from v1 | R1, D-1 |

## User Stories

### US-1: Team lead publishes the team config repo

As a small-team lead maintaining `org/dot-niwa`, I want to move all
plaintext API tokens into a vault so that I can flip the repo to public
without leaking secrets. I expect niwa to detect plaintext values in my
committed config and refuse to apply on a public remote once a vault is
configured, so I can't accidentally re-introduce plaintext.

### US-2: Team member bootstraps vault access with minimal setup

As a new developer joining `tsukumogami`, I want to unlock the team
vault with minimal setup, so that my first `niwa apply` succeeds
without asking a teammate for a shared password or service token.

In v1 (Infisical backend): `infisical login` → browser OAuth → done.
Under 2 minutes end-to-end. My GitHub identity (or any IdP Infisical
supports) unlocks vault access. Must avoid requiring a shared service
token distributed via Slack or 1Password-to-1Password hand-off.

(v1.1 adds sops + age as an alternative backend; that bootstrap is
longer — install `age` + `sops`, generate key pair, PR to
`.sops.yaml`, team lead re-encrypts — but is out of scope for v1.)

In CI environments, authenticate via the provider CLI's service-token
mechanism (e.g., `INFISICAL_TOKEN` env var for Infisical). niwa
doesn't need CI-specific auth — it invokes the provider CLI, which
reads the token from its own env.

### US-3: Developer has a team vault AND a personal vault, composed per workspace

As a developer who belongs to the `tsukumogami` team and also has my own
personal secrets, I want:

1. The team's shared secrets (API tokens the team pays for) to come
   from a **team vault declared in `tsukumogami/dot-niwa`**. When a
   team secret rotates, my next `niwa apply` picks it up without
   me touching my personal overlay.
2. My personal secrets (PATs, personal API keys) to come from a
   **personal vault declared in my own `dangazineu/dot-niwa`
   repo**, scoped per-project: my `tsukumogami` PAT is used for
   `tsukumogami` workspaces, my `codespar` PAT for `codespar`
   workspaces. I never have to manually switch credentials between
   sessions.
3. Both vaults resolved at the same `niwa apply` — team refs hit
   the team vault, personal refs hit the personal vault, all in one
   command.
4. Neither config leaks names into the other's namespace. The team
   config doesn't know how I name my vaults; my personal overlay
   doesn't know how the team names theirs. The team declares what it
   owns and what it needs; I supply bindings for what's needed.

Concretely:

**Team config** (`tsukumogami/dot-niwa/.niwa/workspace.toml`, safe to
be a public repo):

```toml
[workspace]
name = "tsukumogami"

# The team declares its OWN single vault anonymously (R2).
[vault.provider]
kind    = "infisical"
project = "tsukumogami"

# --- Non-sensitive config the team supplies ---
[env.vars]
LOG_LEVEL = "info"
DEFAULT_ORG = "tsukumogami"

# --- Sensitive values the team supplies (vault-backed) ---
[env.secrets]
ANTHROPIC_API_KEY = "vault://anthropic-api-key"
OPENAI_API_KEY    = "vault://openai-api-key"

# --- Sensitive values the user must supply (error if missing) ---
[env.secrets.required]
GITHUB_TOKEN = "GitHub PAT with repo:read scope"

# --- Sensitive values the user should supply (warning if missing) ---
[env.secrets.recommended]
SENTRY_DSN = "Sentry error reporting"

# --- Non-sensitive values the user may supply (info log if missing) ---
[env.vars.optional]
DEBUG_WEBHOOK_URL = "Personal debug webhook"
```

**Personal overlay** (`dangazineu/dot-niwa/niwa.toml`, always private):

```toml
# Personal single vault declared anonymously (R2).
[global.vault.provider]
kind    = "infisical"
project = "dangazineu-personal"

# --- Satisfy team's env.secrets.required ---
[workspaces.tsukumogami.env.secrets]
GITHUB_TOKEN = "vault://tsukumogami/github-pat"
SENTRY_DSN   = "vault://tsukumogami/sentry-dsn"
# DEBUG_WEBHOOK_URL intentionally omitted; I'll just get an info log.

# --- Override a team non-sensitive value ---
[workspaces.tsukumogami.env.vars]
LOG_LEVEL = "debug"

# --- Same for codespar ---
[workspaces.codespar.env.secrets]
GITHUB_TOKEN = "vault://codespar/github-pat"
```

**What happens on `niwa apply` in a `tsukumogami` workspace:**

- `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` → resolved by the team
  config's `[vault.provider]`; no provider name in the URI because
  the team declared anonymously.
- `GITHUB_TOKEN`, `SENTRY_DSN` → from `[env.secrets]` in my personal
  overlay, resolved by my `[global.vault.provider]` via the folder
  path I chose (`tsukumogami/github-pat`, `tsukumogami/sentry-dsn`).
  The team config has no opinion on where in my vault they live.
  Both are wrapped in `secret.Value` (redacted in logs).
- `LOG_LEVEL` → personal overlay overrides team's `"info"` with
  `"debug"` (from `[env.vars]` — plain string, no redaction).
- `DEBUG_WEBHOOK_URL` → missing, declared `[env.vars.optional]`, info
  log only, apply proceeds.

Same binary, same command, two backends resolved in one pass. Each
config owns only its own vault names and its own URI references. The
contract between them is the key-name vocabulary in
`[env.secrets.required]` / `[env.secrets.recommended]` /
`[env.vars.optional]` and the value tables (`[env.vars]` for
non-sensitive, `[env.secrets]` for sensitive).

Scoping is automatic because the workspace has one source
(`tsukumogami`). For multi-source workspaces, I set
`[workspace].vault_scope = "..."` explicitly (R5).

### US-4: Developer overrides a team secret for a debug session

As a developer debugging an integration, I want to override a specific
team-supplied secret with my own local value for one session, without
editing the team config repo. My personal overlay layer should let me
shadow individual vault references, with the shadow visible in
`niwa status` so I remember to remove it later.

### US-5: Team rotates a secret

As a team lead, when I rotate a team secret upstream in the vault, I want
developers' next `niwa apply` to pick up the new value automatically.
`niwa status` should distinguish "file drifted because user edited it"
from "file stale because vault rotated" so the drift-reason is
actionable.

### US-6: Developer runs with a missing optional secret

As a developer with a personal vault that's temporarily unreachable (no
network, auth expired), I want to explicitly opt into running `niwa apply`
with best-effort resolution. By default niwa should fail hard when a
vault reference can't be resolved; I should be able to override with a
CLI flag for exceptional runs, and mark specific references as
`?required=false` when the secret is genuinely optional.

### US-7: Team-lead locks team-critical secrets from personal shadowing

As a team lead, some team-shared secrets must never be overridden
per-user (e.g., a telemetry endpoint that rolls up to a team
dashboard). I want the team config to declare an allow-list of keys
that personal overlays cannot shadow, with a clear error when a personal
config tries.

### US-8: Developer audits their plaintext-to-vault migration

As a developer migrating an existing workspace to use a vault, I want a
command that scans my config and tells me which env values are still
plaintext vs. already vault-backed, so I can track migration progress.

### US-9: External contributor forks a public niwa config without team vault access

As an external contributor who is not a member of the `tsukumogami`
team but wants to submit a PR against `tsukumogami/dot-niwa` (or use
any public niwa workspace that declares a team vault), I want
`niwa apply` to work without me needing access to the team's vault
provider.

Two paths, both expected to work without changes to the team config:

1. **Override individual secret refs at the key level.** In my
   personal overlay:
   ```toml
   [workspaces.tsukumogami.env.secrets]
   ANTHROPIC_API_KEY = "vault://my-own-anthropic-key"
   OPENAI_API_KEY    = "vault://my-own-openai-key"
   ```
   Personal wins per R7. The team-supplied ref is shadowed only for
   the keys I redeclare. Each override is visible in R31's
   override-visibility diagnostic, so unexpected shadowing is
   auditable.

2. **Skip secrets I don't need.** If my PR only touches docs and
   doesn't exercise code paths that hit the team vault's API:
   ```
   niwa apply --allow-missing-secrets
   ```
   Unresolved refs become empty strings with stderr warnings.

**Why there's no "replace the whole team provider" path.** An earlier
draft included a path where the personal overlay declared
`[workspaces.<scope>.vault.providers.team]` to swap the team's
provider in bulk. This was dropped because (a) it contradicts D-9's
file-local provider scoping (the personal overlay would be using a
name the team config declared), and (b) it opens a supply-chain
attack surface where a compromised personal overlay silently
redirects ALL team vault refs via a single provider swap. Per-key
overrides (path 1) are slightly more verbose for the contributor
(one line per key instead of one provider declaration), but each
override is individually visible and bounded. See R12 for the
enforcement rule.

The default failure mode when a contributor runs `niwa apply` without
either path must be a clear, actionable error: it MUST name the
provider that failed to resolve (e.g., "provider is not accessible:
Infisical auth required") and MUST suggest the two paths above with
copy-pasteable pointers. A contributor must not get a cryptic
"vault reference failed" and have to reverse-engineer the override
mechanics.

Constraint: keys the team has locked via `team_only` (R8) cannot be
shadowed at the personal layer even with plaintext. This is by
design — see R8's rationale. A contributor wanting to PR against
team-locked behavior must negotiate the lock change with the team
first. `niwa apply` surfaces the `team_only` block as a distinct
error from a general provider-auth error so the contributor knows
which path forward applies.

## Requirements

### Functional Requirements

**R1. Pluggable vault provider interface.** niwa MUST support multiple
vault backends through a single interface. v1 ships with one backend:

- **Infisical** (hosted, OOTB, free-tier cloud). Bootstrap is
  `infisical login` → browser OAuth → done. Free tier covers 5
  identities, 3 projects, 3 envs. MIT-licensed core with a self-host
  option (Docker). This is the low-effort path for teams that want
  minimal setup and managed rotation/audit.

**sops + age** (git-native, vendor-neutral, self-hosted-keys) is the
planned v1.1 backend. It validates the pluggable interface from a
fundamentally different angle (local-file decrypt vs. API calls) and
gives teams a zero-cost, zero-vendor option. Shipping one backend in
v1 keeps the docs, bootstrap, and acceptance-criteria surface focused
while the interface still gets its v1 reality check against a real
provider.

Additional backends (Doppler, 1Password, HashiCorp Vault OSS,
Bitwarden Secrets Manager, Pulumi ESC) are out of scope for v1 and
v1.1 but must be addable without changing the interface.

**Interface surface.** The pluggable provider interface MUST expose at
least these operations (exact Go signatures deferred to the design
doc, but the contract is PRD-fixed):

- `Resolve(ctx, ref) -> (secret.Value, version-token, error)`.
  Returns the resolved secret as the opaque `secret.Value` type (R22),
  plus a version-token string that feeds `SourceFingerprint` (R15).
  Providers with no native versioning MUST synthesize the token
  deterministically (e.g., sops uses the encrypted blob's SHA-256).
- `Close() -> error`. Releases any session-scoped resources
  (1Password session tokens, HashiCorp Vault leases, long-lived
  HTTP clients). Stateless backends (sops) implement as a no-op.
- Identity accessors (`Name()`, `Kind()`) for diagnostic messages.

A provider MAY hold authenticated state across `Resolve` calls within
a single niwa command invocation. It MUST NOT persist state across
command invocations (no disk cache; R29).

**R2. Vault provider declaration: anonymous singular or named
multiple.** The workspace config MUST support a top-level `[vault]`
table with two accepted declaration shapes:

- **Anonymous singular**: `[vault.provider]` declares exactly one
  unnamed provider. Used when the file has only one vault and the
  author prefers a name-free URI.
- **Named**: `[vault.providers.<name>]` declares one or more named
  providers. Required when the file has two or more providers;
  allowed (but optional) when the file has one.

Mixing `[vault.provider]` and `[vault.providers.*]` in the same file
MUST be rejected at parse time as ambiguous. Each provider entry
carries a `kind` (which backend) plus provider-specific locator
fields (e.g., `project = "..."` for Infisical, `file = "..."` for
sops).

**R3. `vault://` URI reference scheme, with file-local scoping.** Any
string value that begins with `vault://` MUST be interpreted as a
reference to a secret. The URI form follows the provider-declaration
form in the same config file:

- If the file uses `[vault.provider]` (anonymous): URIs take the form
  `vault://<key>[?required=<bool>]`. No provider name.
- If the file uses `[vault.providers.<name>]` (named, one or more):
  URIs take the form `vault://<name>/<key>[?required=<bool>]`. The
  `<name>` MUST match a key declared under `[vault.providers.*]` in
  this same config file.
- If the file declares no providers, any `vault://` URI is a parse
  error.

**Cross-config references forbidden.** A config file's `vault://`
URIs MUST reference only providers declared in that same config
file. Team configs cannot reference user-declared provider names;
user overlays cannot reference team-declared provider names. The
contract between the two layers is the key-name vocabulary in
`[env.vars]` and the `[env.required]` / `[env.recommended]` /
`[env.optional]` tables (R33), NOT shared provider names.

**Reference-accepting locations.** `vault://` references are accepted
in: `[env.secrets]`, `[claude.env.secrets]`,
`[repos.<name>.env.secrets]`, `[instance.env.secrets]`, `[files]`
source keys, and `[claude.settings]` values. References are also
syntactically accepted in `[env.vars]` and its variants (niwa does
not forbid vault-backing a non-sensitive value), but the
`*.vars` / `*.secrets` split determines guardrail and redaction
behavior, not reference acceptance. References are NOT accepted in:
`[claude.content.*]`, `[env.files]`, `[vault.providers.*]` /
`[vault.provider]` fields, requirement-description tables
(`*.required`, `*.recommended`, `*.optional`), or anywhere an
identifier (workspace name, org, repo URL, group name) lives.

**R4. Per-project personal vault scoping.** niwa MUST support per-
workspace personal vault declarations via the existing
`GlobalConfigOverride.Workspaces` map, keyed by the workspace's source
org name. When a workspace has one source (`len(ws.Sources) == 1`),
niwa resolves personal vault providers and references from
`[workspaces.<ws.Sources[0].Org>]` in the personal overlay.

**R5. Explicit `workspace.vault_scope` escape hatch.** A workspace
config MAY set `[workspace].vault_scope = "<string>"` to override
the implicit source-org scoping. Multi-source workspaces
(`len(ws.Sources) > 1`) MUST set this field or niwa fails to apply.
Zero-source workspaces MAY set this field for personal overlay
targeting. When a zero-source workspace has no `vault_scope` set,
`niwa apply` MUST emit a warning ("workspace has no sources and no
vault_scope — personal overlay vault resolution skipped") and
proceed with team-only resolution. Team-supplied secrets still
resolve normally; only user-supplied secrets from the personal
overlay's per-workspace block are affected.

**R6. Resolution chain.** When resolving a `vault://` reference, niwa
MUST consult vault providers in this order: personal-scoped (from
`[workspaces.<scope>]` in personal overlay) → personal-default (from
`[global]` in personal overlay) → team (from workspace config). First
successful lookup wins.

**R7. Personal-wins conflict resolution.** When the same secret key
would be supplied by both the team vault and a personal vault (e.g.,
both declare `GITHUB_TOKEN` as a vault reference), the personal layer
MUST win. This mirrors the existing `MergeGlobalOverride` precedence.

**R8. `team_only` opt-in for team-controlled keys.** A team workspace
config MAY declare `[vault].team_only = ["KEY1", "KEY2"]`. When a
personal overlay tries to supply a value for any key in this list
(either via a `vault://` reference or a plaintext override), niwa MUST
refuse to apply with an error naming the conflicting key.

**R9. Fail-hard resolution by default, with actionable error
messages.** When a `vault://` reference can't be resolved (provider
unreachable, auth expired, key missing), `niwa apply` MUST fail with
an error that:
- Names the specific provider that failed (e.g., `providers.team`).
- Names the specific key(s) that couldn't resolve.
- Distinguishes between distinct failure modes: (a) provider
  unreachable / auth missing; (b) key not found in an otherwise-
  reachable provider; (c) key blocked by `team_only` (R8).
- For fork-and-PR scenarios (contributor has no team vault access),
  the error MUST include copy-pasteable remediation pointers to
  US-9's three paths: override provider in personal overlay, override
  individual key in personal overlay, or run with
  `--allow-missing-secrets`.
- MUST NOT print the value (or any resolved bytes) of secrets the
  contributor does have access to — the error is about the *missing*
  reference only.

R10 (`--allow-missing-secrets`) and R11 (`?required=false`) are the
opt-outs.

**R10. `--allow-missing-secrets` CLI flag.** `niwa apply` MUST accept
an `--allow-missing-secrets` flag that downgrades unresolved
`vault://` references to empty strings. Each downgraded reference
MUST produce a stderr warning naming the key and the provider that
failed, so that silent identity-pivot (e.g., an empty
`AWS_ACCESS_KEY_ID` falling through to ambient credentials) doesn't
go unnoticed. Intended for debug and CI fallback cases.

**R11. Per-reference `?required=false` query parameter.** A vault URI
MAY include `?required=false` to mark the reference as optional.
Unresolved optional references become empty strings without an error
or a warning.

**R12. `GlobalOverride.Vault` field.** The `GlobalOverride` struct
MUST support an optional `Vault *VaultRegistry` field so a personal
overlay can declare its own providers. Merge semantics: the personal
overlay can **add** new provider names that the team config didn't
declare; it **cannot replace** a team-declared provider name. If the
personal overlay declares a provider whose name matches one already
declared in the team config, `niwa apply` MUST fail with an error:
*"personal overlay cannot override team-declared provider `<name>`
— use per-key overrides in `[env.secrets]` instead."* This prevents
bulk-redirection of all team vault references via a single provider
swap (a supply-chain attack surface flagged during review) while
preserving per-key override flexibility (R7).

**R13. `niwa status --audit-secrets` subcommand.** niwa MUST provide
a command that enumerates all `*.secrets` tables (`[env.secrets]`,
`[claude.env.secrets]`, etc.) across the current workspace, classifies
each value as `vault-ref`, `plaintext` (not vault-backed — a commit
risk), or `empty`, and prints a table. Exits non-zero if any
`*.secrets` value is plaintext AND a vault is configured. `*.vars`
tables are excluded from the audit because they are non-sensitive by
declaration.

**R14. Public-repo plaintext-secret guardrail.** When ANY configured
git remote of the workspace config repo resolves to a public GitHub
repository AND a vault is configured AND any value in `[env.secrets]`
or `[claude.env.secrets]` in the team config is NOT a `vault://`
reference (i.e., a plaintext secret committed to a public repo),
`niwa apply` MUST refuse to proceed with an error listing the
offending keys and recommending migration to vault refs.

Detection MUST enumerate ALL configured git remotes (via
`git remote -v`), not just `origin`. A dev whose `origin` points at
a private fork but whose `upstream` points at a public repo MUST
still be caught. Newly added remotes are re-checked on every apply
(the check is cheap). v1 detection is limited to GitHub HTTPS/SSH
URL patterns (see Out of Scope for non-GitHub hosts).

The guardrail checks only `*.secrets` tables — `*.vars` tables are
non-sensitive by declaration and are never flagged.

**R15. `ManagedFile.SourceFingerprint` field.** The instance state
MUST carry a `SourceFingerprint` per managed file, distinct from the
content hash. The fingerprint is a reduction over the file's
resolution inputs, computed as the SHA-256 of a stable-sorted list
of `(source-id, version-token)` tuples where:
- **Plaintext sources** contribute `(file-path, content-hash)` tuples.
  The content-hash is SHA-256 of the source file's bytes at
  resolution time.
- **Vault sources** contribute `(provider-name, vault-key, version-token)`
  tuples. The `version-token` is an opaque string returned by the
  provider's `Resolve` call (see R1): Infisical's secret version,
  sops's file content-hash, 1Password's version int, HashiCorp
  Vault's lease+version, Bitwarden's revisionDate. Providers with
  no native version MUST synthesize one deterministically (e.g.,
  sops uses the encrypted blob's SHA-256).

**Provenance requirement.** Each version-token MUST carry enough
metadata for a user to trace a rotation back to a specific change
event. The goal is making rotations investigable — a user seeing
`stale` in `niwa status` should be able to answer "what change
caused this?" without re-running the rotation themselves:

- **Git-hosted backends (sops+age, any future git-native backend).**
  The version-token includes the commit SHA of the commit that
  touched the secret-bearing file. niwa derives this at resolve
  time by asking git for the last-modifying commit of the file.
  `niwa status` reports the SHA alongside `stale` so the user can
  `git show <sha>` and see who rotated the secret when.
- **API-hosted backends (Infisical, 1Password, Vault OSS, Bitwarden,
  Doppler, ...).** The version-token includes the provider's native
  version identifier in a shape that resolves back to the provider's
  audit log (e.g., Infisical's secret-version ID paired with a
  pointer to the audit-log entry). `niwa status` surfaces both so
  the user can cross-reference.
- **Synthesized-version backends** (providers with no native
  versioning). Fall back to content-hash of the encrypted blob plus
  best-available metadata (e.g., `.sops.yaml` commit SHA for
  sops).

The design doc defines the exact per-backend version-token
structure. The PRD requires the *property* (rotation is
investigable via provenance that accompanies `stale` reports), not
the specific mechanism.

For a materialized file that blends multiple sources (the common
case — `.local.env` combines workspace env files, discovered repo
env files, `env.vars` entries, and overlay-merged values), the
fingerprint is the SHA-256 of every contributing tuple sorted and
concatenated. The tuple list itself MAY be stored alongside the
fingerprint in `state.json` to enable precise "what changed"
diagnostics; storing only the rollup hash is allowed when state
size is a concern, but loses per-source attribution.

`niwa status` uses the fingerprint to report:
- `drifted` when content hash differs AND source fingerprint unchanged
  (user edited the file; no source changed).
- `stale` when content hash differs AND source fingerprint also differs
  (at least one source changed — vault rotated, plaintext source
  edited upstream, or both).
- `ok` when content hash matches.

**R16. Re-resolution on every apply.** `niwa apply` MUST re-resolve
every `vault://` reference on every invocation. No niwa-internal cache
between commands. This picks up upstream rotations automatically.
Provider CLIs may cache their own auth sessions (out of niwa's scope).

### Non-Functional Requirements

**R20. Zero additional external dependencies on niwa's side.** The
pluggable interface MUST be implemented in niwa's Go code without
pulling in a vault-specific Go library. niwa invokes provider CLIs
(`sops`, `age`, `infisical`) as subprocesses. Users install provider
CLIs themselves; niwa does not bundle them.

(R18 and R19 were earlier numbered here as bootstrap-time and
resolution-time budgets. They moved to the Goals section as product
outcomes because the PRD can't specify a measurement protocol
precise enough to make them binary pass/fail requirements; the
design doc will define concrete benchmarks when the implementation
exists to benchmark.)

### Security Requirements (the 11 "never leaks" invariants)

**R21 (INV-NO-ARGV).** No niwa subcommand accepts a secret value on
argv, flags, or environment variables that would appear in shell
history. All secret values arrive via `vault://` references in config
or interactive TTY prompts.

**R22 (INV-REDACT-LOGS).** All resolved secret values MUST flow through
a `secret.Value` opaque type whose formatters emit `***` for `String`,
`GoString`, `Format("%s"/%q/%v/%+v)`, `MarshalJSON`, `MarshalText`,
and `gob.GobEncoder`. Any stderr, stdout, or structured log output
that prints a `secret.Value` renders the redacted form regardless of
verb. Compile-time and test-time checks enforce that no bare string
originating from vault resolution reaches a formatter.

**Error-wrapping coverage.** The redaction guarantee MUST survive
error wrapping. Concrete requirements:
- Any error wrapped via `fmt.Errorf("...: %w", err)` whose
  `Unwrap()` chain touches a `secret.Value` MUST redact through a
  `secret.Error` wrapper type (or equivalent) whose `Error()`
  method suppresses any secret-derived substring.
- Any provider-CLI stderr captured by niwa (e.g., `infisical`'s
  stderr on auth failure) MUST be scrubbed through the redactor
  before being wrapped into a returned error. Raw provider stderr
  SHOULD NOT be interpolated into error strings without redaction.
- An acceptance test exists that induces an auth error from the
  provider CLI with a known-secret-fragment in stderr, runs the
  resolver, and asserts the fragment does NOT appear anywhere in
  the returned error's `Error()` string.

**R23 (INV-NO-CONFIG-WRITEBACK).** niwa MUST NEVER write a resolved
secret value back into `ConfigDir` (the cloned config repo working
tree). Resolution is read-only with respect to config.

**R24 (INV-FILE-MODE).** Every materialized file that contains a
resolved secret MUST be written with mode `0o600`. This fixes a
pre-existing `0o644` bug in `SettingsMaterializer` and
`EnvMaterializer` where settings and env files were world-readable.

**R25 (INV-LOCAL-GITIGNORED).** Every materialized file that contains
a resolved secret MUST (a) carry the `.local` infix in its filename
AND (b) be covered by an instance-root `.gitignore` matching
`*.local*`. `niwa create` MUST ensure the instance-root `.gitignore`
exists with the required pattern: generating the file if missing,
merging the pattern if present (idempotent). The two mechanisms
(filename convention and gitignore coverage) are the two halves of
one guarantee — removing either one weakens the protection, so they
are stated as a single invariant.

**R26 (INV-NO-CLAUDE-MD-INTERP).** CLAUDE.md, CLAUDE.local.md, and
all materialized Markdown files MUST be treated as opaque text. No
secret interpolation into Markdown content. Secrets reach Claude via
env var injection in `settings.local.json`, not via inline Markdown
templates.

**R27 (INV-NO-STATUS-CONTENT).** `niwa status` MUST NOT print the
content or diff of any managed file. Output is limited to path +
status (`ok | drifted | stale | removed`). Future `--verbose` modes
MUST exclude files flagged as secret-bearing.

**R28 (INV-NO-PROCESS-ENV-PUBLICATION).** niwa MUST NEVER call
`os.Setenv` with a resolved secret. Secrets are project data owned by
the materializer, not niwa's process-level state.

**R29 (INV-NO-DISK-CACHE).** niwa MUST NOT cache resolved secrets on
disk between commands. Provider CLIs may cache their own auth sessions
(out of scope); niwa does not store secret values beyond the lifetime
of a single command invocation.

**R30 (INV-PUBLIC-REPO-GUARDRAIL).** The R14 guardrail is a hard
block: `niwa apply` on a public-remote config repo with plaintext
`*.secrets` values and a vault configured MUST fail unless the user
explicitly overrides with `--allow-plaintext-secrets` (NOT a
default-friendly flag).

**The `--allow-plaintext-secrets` flag is strictly one-shot.** It
affects only the current `niwa apply` invocation. It MUST NOT write
state, persist to config, or be remembered across invocations. Every
`niwa apply` re-evaluates the guardrail from scratch. A user who
needs to bypass for two applies runs the flag twice; there is no
"remember my answer" behavior.

**R31 (INV-OVERRIDE-VISIBILITY).** When the personal overlay shadows a
team-declared vault provider (R12) or a key that appears in the team
config's `[env.vars]` / `[claude.env.vars]`, niwa MUST surface the
shadow so the user can detect unexpected overrides (a compromised
personal overlay repo silently replacing a team provider is otherwise
invisible). Concrete requirements:

- `niwa apply` MUST emit a structured "shadowed" diagnostic to
  stderr naming each overridden provider or key and identifying
  `personal-overlay` as the effective source layer.
- `niwa status` MUST include a shadowed-count summary in its output
  (e.g., "3 keys shadowed by personal overlay").
- `niwa status --audit-secrets` MUST flag every shadowed team key in
  its output so the user can audit for unexpected shadowing.

The diagnostic names the provider or key and the source layer. It
MUST NOT print the shadowed or shadowing values (R22 redaction still
applies). A user who legitimately overrides sees the noise and
confirms it's theirs; a user whose overlay has been tampered with
sees shadowing they didn't expect and has a concrete diagnostic to
investigate.

**Deferred invariants.** An earlier draft included
`INV-EXPLICIT-SUBPROCESS-ENV` (niwa builds child process env
explicitly, never passes `os.Environ()` unfiltered to subprocesses).
It was removed because niwa today has no subprocess-spawn path that
touches resolved secrets — the invariant enforces nothing against
existing code. When niwa later adds a subprocess-spawn path carrying
secrets (e.g., invoking hook scripts or launching Claude Code), this
invariant re-enters scope as part of that feature's design.

### Contract Requirements

**R33. `env.vars` / `env.secrets` split with three-level requirement
sub-tables.** Env values in workspace config are split into two
namespaces by sensitivity:

- `[env.vars]` — non-sensitive configuration. Values are plain
  strings. Guardrail (R14) never checks these. Materialized as
  plain strings.
- `[env.secrets]` — sensitive values. Values SHOULD be `vault://`
  references. Guardrail checks these on public repos. Materialized
  as `secret.Value` (redacted in logs per R22).

Each namespace carries three optional requirement sub-tables for
keys the team expects but does not supply:

- `[env.vars.required]` / `[env.secrets.required]` — keys that MUST
  resolve to a non-empty value by apply time. Missing ≥1 required
  key is a hard error; `niwa apply` fails with a message listing
  every missing key and its description.
- `[env.vars.recommended]` / `[env.secrets.recommended]` — keys the
  workspace expects but can operate without. Missing → loud stderr
  warning; `niwa apply` proceeds.
- `[env.vars.optional]` / `[env.secrets.optional]` — keys that are
  genuinely nice-to-have. Missing → info log (visible only with
  `--verbose`); `niwa apply` proceeds.

Requirement sub-table values are human-readable description strings,
not env values. The actual value comes from whichever layer supplies
it (`*.vars` or `*.secrets` in the team config or personal overlay).

Same split and sub-table pattern MUST be supported for Claude-scoped
env: `[claude.env.vars]` / `[claude.env.secrets]` with the six
requirement sub-tables nested accordingly.

Per-repo, per-instance, and `[files]`-scoped requirement tables are
NOT supported in v1 (deferred; additive and non-breaking to add
later).

The description string values MUST be carried into the error /
warning / info message so missing-secret diagnostics are
self-documenting. A user running `niwa apply` against an unmet
requirement sees the team-authored description telling them what the
key is for.

**R34. `*.required` tables have precedence over
`--allow-missing-secrets`.** The `--allow-missing-secrets` flag (R10)
downgrades unresolved `vault://` references to empty strings. It does
NOT downgrade unresolved `[env.vars.required]` or
`[env.secrets.required]` keys. A missing required key is always a hard
error regardless of flags, because the team config explicitly marked
the key as load-bearing. This separation lets users use
`--allow-missing-secrets` for exploratory runs without accidentally
bypassing team-declared requirements.

## Acceptance Criteria

### Schema

- [ ] `workspace.toml` accepts `[vault.provider]` (anonymous
  singular) with `kind` plus provider-specific fields.
- [ ] `workspace.toml` accepts `[vault.providers.<name>]` (named) with
  `kind` plus provider-specific fields; multiple named providers in
  the same file are accepted.
- [ ] `workspace.toml` rejects a file that declares both
  `[vault.provider]` and `[vault.providers.*]` at parse time.
- [ ] `workspace.toml` accepts `vault://<key>` URIs in a file that
  declares `[vault.provider]`.
- [ ] `workspace.toml` accepts `vault://<name>/<key>` URIs in a file
  that declares `[vault.providers.<name>]` where `<name>` matches.
- [ ] `workspace.toml` rejects `vault://<name>/<key>` URIs where
  `<name>` is not declared in the same file (including names declared
  only in a different config layer) with a parse-time error.
- [ ] `workspace.toml` rejects `vault://` URIs in `[claude.content.*]`
  source paths, `[env.files]` source paths, `[vault.providers.*]` /
  `[vault.provider]` fields, and identifier fields (workspace name,
  source org, repo URL, group name) with parse-time errors.
- [ ] `workspace.toml` accepts `[workspace].vault_scope = "<string>"`.
- [ ] `workspace.toml` accepts `[vault].team_only = ["KEY1", ...]`.
- [ ] `workspace.toml` accepts `[env.vars]` (non-sensitive values)
  and `[env.secrets]` (sensitive values) as sibling tables.
- [ ] `workspace.toml` accepts `[env.vars.required]`,
  `[env.vars.recommended]`, `[env.vars.optional]`,
  `[env.secrets.required]`, `[env.secrets.recommended]`,
  `[env.secrets.optional]` with key→description-string entries.
- [ ] The same vars/secrets split and requirement sub-tables are
  accepted under `[claude.env]`.
- [ ] Values from `*.secrets` tables are materialized wrapped in
  `secret.Value` (R22 redaction); values from `*.vars` are
  materialized as plain strings.
- [ ] The personal overlay (`GlobalOverride`) accepts the
  anonymous-or-named provider declaration and per-workspace
  `[workspaces.<scope>]` blocks.

### Resolution

- [ ] A workspace with `[[sources]] org = "tsukumogami"` and a personal
  config whose `[workspaces.tsukumogami.vault.provider]` declares a
  sops backend resolves a `vault://<key>` URI written in the same
  personal-overlay file against the declared sops provider.
- [ ] An `[env.required]` key in the team config that has no matching
  entry in any resolved `env.vars` source MUST cause `niwa apply` to
  fail with an error listing the key and its description string.
- [ ] An `[env.recommended]` key with no matching resolved value MUST
  emit a stderr warning naming the key and description, and
  `niwa apply` MUST continue.
- [ ] An `[env.optional]` key with no matching resolved value MUST
  emit an info log (visible only under verbose mode) and
  `niwa apply` MUST continue with no warning.
- [ ] `niwa apply --allow-missing-secrets` MUST NOT downgrade
  `[env.required]` misses to warnings; required keys remain a hard
  error even with the flag set.
- [ ] A workspace with 2 sources and no `vault_scope` fails `niwa apply`
  with an error naming the ambiguity.
- [ ] A workspace with 2 sources and `vault_scope = "tsukumogami"`
  resolves personal vault from `[workspaces.tsukumogami]`.
- [ ] When personal overlay shadows a team-supplied key, the personal
  value wins in the resolved env.
- [ ] When personal overlay tries to shadow a key listed in team
  `team_only`, `niwa apply` fails with an error naming the key.
- [ ] A `vault://` reference to a nonexistent key fails `niwa apply`
  with a clear error, unless `--allow-missing-secrets` or
  `?required=false` is set.
- [ ] `niwa apply --allow-missing-secrets` resolves missing references
  to empty strings and emits stderr warnings.
- [ ] `vault://provider/key?required=false` resolves to empty string
  when missing, with no warning.
- [ ] When a contributor without team-vault access runs `niwa apply`
  against a team config declaring a team provider, the error message
  names the specific provider and key, distinguishes provider-auth
  failure from key-not-found, and includes copy-pasteable pointers to
  the two override paths (US-9).
- [ ] A personal overlay declaring a provider with the SAME name as a
  team-declared provider causes `niwa apply` to fail with an error
  naming the collision and pointing to per-key overrides instead
  (R12 enforcement).
- [ ] A contributor can shadow an individual team vault reference by
  declaring a different `vault://` URI or a literal for the same key
  in their personal overlay's `[workspaces.<scope>.env.secrets]`;
  personal value wins per R7.
- [ ] A `team_only` lock (R8) surfaces as a distinct error from
  provider-auth failure and is NOT bypassable by the contributor's
  personal overlay.

### Backends (v1: Infisical; v1.1: sops)

- [ ] `kind = "infisical"` with `project = "<proj-id>"` resolves secrets
  from Infisical Cloud or self-hosted Infisical.
- [ ] Adding a new backend (e.g., sops for v1.1) requires implementing
  a single Go interface and registering it — no changes to the
  resolver or schema.

### Materialization

- [ ] Every materialized file containing resolved vault values is
  written with mode `0o600`.
- [ ] `SettingsMaterializer` and `EnvMaterializer` pre-existing
  `0o644` bug is fixed: settings and env files always write at `0o600`
  regardless of whether they contain vault refs.
- [ ] Every materialized file containing resolved vault values has
  `.local` in its name.
- [ ] `niwa create` writes an instance-root `.gitignore` covering
  `*.local*`, creating it if missing or merging patterns if present.
- [ ] CLAUDE.md / CLAUDE.local.md files never contain secret values.
- [ ] `niwa status` output contains path + status only; no file content,
  no diffs.
- [ ] `ManagedFile.SourceFingerprint` is populated for every managed
  file; rotated-upstream secrets report `stale`, user-edited files
  report `drifted`.

### Security

- [ ] A unit test confirms `secret.Value` formatters emit `***` under
  `%s`, `%v`, `%+v`, and `%q`.
- [ ] A unit test confirms no resolved secret reaches stdout/stderr in
  any error wrapping path, under a test that deliberately induces
  errors.
- [ ] A unit test confirms niwa never calls `os.Setenv` during apply.
- [ ] A functional test confirms `niwa apply` on a config repo with a
  public-matching remote (not necessarily `origin`) and plaintext
  `*.secrets` values fails without `--allow-plaintext-secrets`.
- [ ] The guardrail fires when ANY remote is public, not just `origin`
  — a test with `origin` private and `upstream` public triggers the
  guardrail.
- [ ] A functional test confirms `niwa apply --allow-plaintext-secrets`
  proceeds under the same conditions with a loud warning.
- [ ] Running `niwa apply --allow-plaintext-secrets` once, then running
  `niwa apply` again without the flag, re-triggers the guardrail
  (the flag does not persist to state).
- [ ] A functional test confirms no argv accepts a secret value on any
  subcommand.
- [ ] A personal overlay shadowing a team-declared `[env.vars]` key
  produces a stderr diagnostic during `niwa apply` that names the key
  and identifies `personal-overlay` as the effective source layer.
  The diagnostic MUST NOT print the shadowed or shadowing values.
- [ ] A personal overlay shadowing a team-declared vault provider via
  R12 (same provider name declared in both layers) produces a stderr
  diagnostic during `niwa apply` that names the provider and the
  source layer.
- [ ] `niwa status` output includes a shadowed-count summary when a
  personal overlay shadows any team-declared provider or `[env.vars]`
  key.
- [ ] `niwa status --audit-secrets` flags every shadowed team key in
  its output.

### Audit and Migration

- [ ] `niwa status --audit-secrets` enumerates env values, classifies
  each as `plaintext | vault-ref | empty`, prints a table, and exits
  non-zero if plaintext values are present AND a vault is configured.
- [ ] `niwa status --audit-secrets` exits zero if all env values are
  vault refs or empty, regardless of vault configuration.

### Rotation

- [ ] After a team rotates a vault secret upstream, the next `niwa
  apply` re-resolves the value, re-materializes affected files, and
  reports `rotated <path>` to stderr.
- [ ] `niwa status --check-vault` opt-in subcommand triggers
  re-resolution without materialization and reports which files would
  change if `niwa apply` ran.
- [ ] Default `niwa status` (no flag) is fully offline and hash-based.
- [ ] When a sops-backed secret is rotated (the encrypted file is
  edited and committed), `niwa status` reports `stale` with the
  commit SHA that produced the change, so a user can run
  `git show <sha>` to see the rotation.
- [ ] When an Infisical-backed (or other API-hosted) secret is
  rotated, `niwa status` reports `stale` with the provider's
  native version identifier in a shape that can be resolved back to
  the provider's audit log.

### Bootstrap and Documentation

- [ ] README / niwa docs include a bootstrap walkthrough for the sops
  backend: install age, generate key, publish to
  `.sops.yaml`, first `niwa apply`.
- [ ] README / niwa docs include a bootstrap walkthrough for the
  Infisical backend: `infisical login`, configure provider in
  workspace.toml, first `niwa apply`.
- [ ] `niwa init <name> --from <org/dot-niwa>` in a workspace configured
  with a vault displays a post-clone message pointing to the vault
  bootstrap instructions for the declared backend.
- [ ] The scaffolded `workspace.toml` from `niwa init` includes a
  commented `[vault]` example.

## Out of Scope

- **v1 niwa `vault import` tool.** Automated plaintext-to-vault
  migration is deferred. Users migrate by hand via the provider CLI
  (`sops edit`, `infisical secrets set`) plus `niwa status
  --audit-secrets`.
- **Escape syntax for literal `vault://` strings.** No mechanism in v1
  for TOML string values whose content legitimately begins with
  `vault://`. The expected collision rate is effectively zero (`vault://`
  is not a widely-used URI scheme outside niwa's proposed semantics),
  and shipping an escape now locks a design choice before any concrete
  use case justifies it. Addable later via parse-time unescape without
  breaking existing configs.
- **sops + age backend.** Deferred to v1.1 as the first non-cloud
  backend. Validates the pluggable interface from the git-native
  angle. Gives teams a zero-cost, zero-vendor option. See D-1.
- **Additional vault backends beyond Infisical and sops.** Doppler,
  1Password, HashiCorp Vault OSS, Bitwarden Secrets Manager, Pulumi
  ESC, AWS Secrets Manager, Azure Key Vault all stay deferred beyond
  v1.1. The pluggable interface means they can be added without
  rearchitecting.
- **Secret rotation automation.** niwa does not run scheduled checks,
  does not trigger rotation, and does not maintain rotation
  schedules. Rotation detection via `SourceFingerprint` is reactive,
  not proactive.
- **Daemon or background vault watcher.** niwa is pull-only; no
  `niwa-agent` process that polls vaults.
- **Windows support.** macOS + Linux only for v1. Windows users can
  use WSL.
- **Multi-factor auth prompts mid-command.** niwa relies on the vault
  CLI's own auth machinery. Expired sessions fail the next command
  with a re-auth hint; niwa doesn't prompt for 2FA in-band.
- **Per-file materialization permissions.** Vault-sourced `[files]`
  entries always get `0o600`. Configurable perms are deferred.
- **`[env.files]` vault-backed source paths.** `[env.files]` takes
  filesystem paths only. If a `.env` file itself needs to come from a
  vault, use `[files]` with a `vault://` source key.
- **CLAUDE.md secret interpolation.** Explicitly forbidden per R26.
  Secrets reach Claude via settings env, not via Markdown templates.
- **Non-GitHub source control.** Public-repo guardrail (R14) detects
  via GitHub remote URL patterns only in v1. GitLab, Bitbucket,
  self-hosted Gitea stay in a deferred list.

## Decisions and Trade-offs

Decisions settled during the exploration that inform this PRD. Each
captures what was decided, what alternatives were considered, and why
the chosen option won. Downstream design docs should treat these as
settled.

### D-1. Pluggable backend interface from v1; Infisical as the v1 backend, sops in v1.1

**Decided:** Ship a pluggable provider interface from v1 with
**Infisical Cloud as the sole v1 backend.** sops + age ships in v1.1
as the first non-cloud backend, validating the interface from the
git-native angle. The interface itself is a v1 deliverable regardless.

**Alternatives considered:** (a) ship both Infisical and sops in v1
as peer backends — gives users both options on day one but doubles
the docs, bootstrap, and acceptance-criteria surface for zero
current users; (b) ship sops-only in v1 — cheaper to implement but
leaves the OOTB-cloud-provider gap the user explicitly flagged;
(c) ship Doppler (cleanest OAuth) — closed-source SaaS, conflicts
with niwa's vendor-neutrality.

**Rationale:** Shipping one backend in v1 keeps the surface focused.
Infisical is the right choice because (a) it's the OOTB low-effort
option the user pushed for, (b) its bootstrap exercises every layer
(browser OAuth, API calls, session caching), (c) it validates the
interface against a real hosted provider's quirks. sops is simpler
(subprocess + file decrypt); shipping it in v1.1 gives the interface
a second fundamentally-different consumer quickly without inflating
v1 scope. The pluggable interface ships in v1 regardless, so the
abstraction is proven with one real backend before the second
arrives.

### D-2. Scoping via source org with explicit escape hatch

**Decided:** Implicit scoping by `ws.Sources[0].Org` + explicit
`[workspace].vault_scope` escape hatch for multi-source or borrowed-
scope workspaces.

**Alternatives considered:** (a) always-explicit `vault_scope`
required; (b) full URI with embedded scope
(`vault://personal/tsukumogami/key`); (c) per-provider default + key
namespacing convention.

**Rationale:** The 80% case is single-source workspaces where source
org IS the natural scope. Requiring explicit `vault_scope` for every
workspace is ceremony tax for the common case. Full URIs are verbose
at every reference site. Per-provider default with namespacing couples
team configs to user conventions silently.

### D-3. Personal-wins conflict resolution with `team_only` opt-in

**Decided:** When team and personal overlays supply the same key,
personal wins. Teams may declare `team_only = [...]` to enforce
team-controlled keys.

**Alternatives considered:** (a) team-wins by default; (b) error on
conflict; (c) namespace separation (team keys under `team/`, personal
under `personal/`).

**Rationale:** Personal-wins matches niwa's existing
`MergeGlobalOverride` precedence for `Env.Vars` (v0.5.0). Users' local
overrides should work without team involvement. Teams still have
authority via `team_only` for keys that must not be shadowed (e.g.,
team telemetry endpoints).

### D-4. Fail-hard resolution by default

**Decided:** `niwa apply` fails hard when a vault reference is
unresolvable. Opt-outs: `--allow-missing-secrets` CLI flag,
`?required=false` per-reference query param.

**Alternatives considered:** (a) fall back to empty silently; (b)
prompt interactively; (c) warn but proceed.

**Rationale:** Silent empty is dangerous — downstream tools may hit
rate-limited unauthenticated endpoints without obvious failure.
Interactive prompt breaks non-interactive callers (`niwa init`,
CI). Fail-hard + explicit opt-outs matches direnv's trust-on-first-use
pattern and gives users agency without hiding failures.

### D-5. Top-level `[vault]`, not under `[claude]`

**Decided:** Vault declarations live at the top level of
`workspace.toml`. Not under `[claude]`.

**Alternatives considered:** (a) under `[claude.vault]` following the
`[claude.content]` consolidation pattern; (b) nested under
`[env.vault]`.

**Rationale:** Unlike `[content]`, which produced only CLAUDE.md files
and was 100% Claude-coupled, vault references appear in `[env.vars]`,
`[claude.env.vars]`, `[repos.*.env.vars]`, `[files]`, and
`[claude.settings]` — cross-cutting. Placing vault under `[claude]`
would force users into `[claude]` for non-Claude secrets.

### D-6. URI reference scheme, not binding tables

**Decided:** Secrets are referenced via `vault://provider/key` URIs
inline in existing string slots. Not via a separate `[vault.keys]`
binding table.

**Alternatives considered:** (a) `[vault.keys]` binding table mapping
env var names to vault keys; (b) implicit resolution by naming
convention (auto-resolve any env value starting with `vault://`); (c)
`[claude.vault]`-only scope.

**Rationale:** URIs ride the existing `MergeOverrides` /
`MergeGlobalOverride` pipelines with zero new merge logic — last-
writer-wins per key already handles vault references. A separate
binding table would demand new merge logic for every scope that could
resolve secrets. Implicit resolution (no schema change) removes
discoverability of which provider resolves what.

**Resolution order (required for D-9 compliance).** URIs MUST be
resolved to `secret.Value` (opaque type per R22) *before* the merge
pipeline runs, inside the source file's provider context. The merge
then operates on already-resolved typed values, preserving both
last-writer-wins semantics AND D-9's file-local provider scoping.
Resolving post-merge would flatten the origin and make
`vault://<name>/...` ambiguous when layers declare providers with
the same name. Order: (per-file-parse → per-file-resolve →
cross-layer-merge → materialize), not the naïve (parse → merge →
resolve) that D-6's original phrasing implied.

### D-7. No niwa-internal secret caching

**Decided:** niwa never caches resolved secrets between commands.
Every `niwa apply` re-resolves every reference.

**Alternatives considered:** (a) process-lifetime in-memory cache; (b)
disk cache keyed by vault provider version; (c) OS-keychain-backed
cache.

**Rationale:** Caching introduces its own key-storage problem (where
does it live? at what perms? invalidated how?). Vault provider CLIs
already solve this. If niwa performance becomes an issue at apply
time, the next optimization is a process-lifetime in-memory cache only
— never disk.

### D-9. File-local provider scoping; team and personal overlays can't cross-reference each other's provider names

**Decided:** Each config file's `vault://` URIs may only reference
providers declared in the same file. Team configs cannot write
`vault://personal/...`; personal overlays cannot write
`vault://team/...`. The contract between the two layers is the key
names in `[env.vars]` / `[env.secrets]` and their
`.required` / `.recommended` / `.optional` sub-tables, NOT shared
vault provider names.

**Alternatives considered:** (a) shared "rendezvous" names (team
config could reference `vault://personal/github-pat`, user overlay
supplies the provider named `personal`); (b) symbolic URI scheme
(`user://` or `external://`) for user-supplied refs; (c) no
separation — either layer can reference anything in the other.

**Rationale:** Rendezvous names leak: the team config dictates how
users name their vaults AND what key path they use. A team couldn't
publish its config without accidentally prescribing the user-side
layout. Users couldn't reorganize their vaults without editing team
configs. The symbolic-URI approach adds a new scheme without solving
the root problem (the team still names user-facing refs). File-local
scoping with an `[env.secrets.required]`-style contract keeps each layer
owning only its own namespace and reduces the team/personal coupling
to a vocabulary of key names.

### D-10. `env.vars` / `env.secrets` split with three-level requirement sub-tables

**Decided:** Env values split by sensitivity (`[env.vars]` for non-
sensitive config, `[env.secrets]` for sensitive values), each carrying
three optional sub-tables for team-declared requirements:
- `*.required` — hard error on miss.
- `*.recommended` — loud warning on miss, apply continues.
- `*.optional` — info log on miss, apply continues.

Same pattern for `[claude.env.*]`. Values in sub-tables are
human-readable description strings surfaced in the diagnostic
message. The sensitivity scope comes first, the requirement level
nests under it.

Requirement tables at `[repos.<name>.env.*]`, `[instance.env.*]`,
and `[files.*]` are deferred. Without a concrete user story,
shipping them in v1 is speculative generality.

**Alternatives considered:** (a) single flat `[env.vars]` table with
no vars/secrets distinction — the guardrail (R14) can't tell
`EDITOR = "nvim"` from `GITHUB_TOKEN = "ghp_abc123"` and would
false-positive on every plaintext config value in a public repo;
(b) a `[env].secrets = [...]` list naming which keys are sensitive —
DRY violation (the key is declared in the value table AND in the
secrets list); (c) per-key inline metadata like
`GITHUB_TOKEN = { value = "vault://...", secret = true }` — verbose;
mixes value and metadata; breaks the string-slot convention.

The `vars` / `secrets` table split is the least-ceremony option that
gives the guardrail a clean signal without heuristics or annotations.

**Rationale:** Three levels match the observed pattern in real
workspaces — some env vars block operation, some degrade
functionality, some are pure polish. A binary "required or silent"
forces teams to pick between "apply crashes on polish" or "apply
silently proceeds with broken functionality." The recommended tier
is the pragmatic middle.

The description-string value is a small UX win: when `niwa apply`
reports a missing required key, it says *why* the key is needed in
the team's own words, not just the key name.

### D-11. Anonymous singular vs named multiple provider declarations

**Decided:** A config file declares vault providers using either
`[vault.provider]` (anonymous, exactly one) or
`[vault.providers.<name>]` (named, any count). Mixing in a single
file is a parse error.

**Alternatives considered:** (a) always require naming (every
provider gets a name even if there's only one); (b) always anonymous
with a single provider allowed (no multi-provider files); (c)
implicit "default" name for the sole named provider when URIs omit
the name.

**Rationale:** Always-requiring-names adds ceremony for the 80% case
(one personal vault, one team vault). Always-anonymous prevents
legitimate multi-vault setups (a team with separate prod/sandbox
vaults, or a user with work + home 1Password accounts). Implicit
"default" name for a sole named provider is magic — same URI
behaves differently depending on whether there's one or two
providers. The dual-shape approach keeps anonymous URIs
(`vault://key`) and named URIs (`vault://name/key`) visually
distinct, and the parser can tell at config-load time which shape
the file uses.

## Rollout Notes

The following are release-planning concerns deliberately kept outside
the PRD's Open Questions because they don't change what the PRD
specifies — only the schedule or measurement around its rollout.
Listed here so they're not forgotten when the feature starts landing:

- **Plaintext-deprecation schedule.** R14/R30 defines the public-repo
  plaintext-secret guardrail. Whether it ships as a hard block in
  v1.0 or warns for one or two minor releases before flipping to
  default-on is a release choice. A staged "warn-then-block" rollout
  is friendlier to existing users; a day-one hard block is cleaner
  semantically. Decide during release planning when v1.0 gets cut.
- **Rollout success metrics.** Candidate signals to watch after v1.0:
  number of plaintext values in `tsukumogami/dot-niwa` (target: 0),
  count of `niwa apply --allow-plaintext-secrets` invocations (target:
  near-zero), GitHub secret-scanning alerts on niwa-using repos.
  Pick and instrument when shipping; not a PRD acceptance gate.

## Open Questions

Questions to resolve before the PRD transitions to Accepted:




- **Q-3 Migration UX details.** What does the first-time migration
  walkthrough look like? A dedicated `niwa vault init` subcommand, or
  a combination of manual steps documented in the README? Research
  leaned toward manual steps for v1 (`niwa vault import` is deferred),
  but a guided walkthrough might be worth it for onboarding.

- **Q-4 `team_only` enforcement layer.** Is violation caught at
  parse time (static check against the committed personal overlay) or
  at materialize time (runtime check when resolving)? Static is
  cleaner but requires `niwa status` to load the team config's
  `team_only` list, which means remote read of team repo. Runtime is
  always available.

- **Q-5 Sign-off stakeholders.** Does this PRD need explicit review
  from anyone beyond the niwa maintainer before transitioning to
  Accepted — e.g., security-minded community members, the
  `tsukumogami/tsuku` ecosystem maintainers, or contributors with
  signaled interest in the feature?
