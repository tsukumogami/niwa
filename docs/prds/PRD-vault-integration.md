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

## Goals

- **Team configs can be publishable.** A team using niwa can move their
  `dot-niwa` repo from private to public without any secret value ever
  appearing in git history, current content, or future content.
- **Per-org personal secret scoping works end-to-end.** A developer with
  separate PATs for `tsukumogami` and `codespar` can declare both in
  their personal overlay repo and have niwa pick the right one
  automatically when working on a workspace in either org.
- **New-member bootstrap is under 10 minutes.** A new developer joining
  `tsukumogami` who already has GitHub org membership can run
  `niwa init tsukumogami --from tsukumogami/dot-niwa` plus one bootstrap
  step (generate age key, publish via PR) and be fully productive on
  `niwa apply` within 10 minutes.
- **Zero new leak classes.** The feature ships with 12 "never leaks"
  invariants enforced by the type system, pipeline, and filesystem
  permissions. Fixes a pre-existing `0o644` bug where materialized env
  and settings files were world-readable.
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
| New-member bootstrap under 10 minutes | R18, US-2 (two-path bootstrap), R1 (CLI-exec interface means no niwa-specific auth bootstrap) |
| Zero new leak classes | R21–R30 (the "never leaks" invariants) |
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

Depending on which backend the team chose, the bootstrap looks like:

- **Infisical** (hosted, OOTB): `infisical login` → browser OAuth →
  done. Under 2 minutes end-to-end. My GitHub identity (or any IdP
  Infisical supports) unlocks vault access.
- **sops + age** (vendor-neutral, self-hosted-keys): install `age` +
  `sops`, generate my own age key pair, open a PR to add my public
  key to the team's `.sops.yaml`, wait for the team lead to merge and
  re-encrypt. Target 10 minutes once the PR is merged, budgeted for
  the team-lead review delay.

Either path must avoid requiring a shared service token distributed
via Slack or 1Password-to-1Password hand-off.

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

# The team declares its OWN single vault anonymously. Because there's
# exactly one provider in this file, naming is optional (R2).
[vault.provider]
kind    = "infisical"
project = "tsukumogami"

# Team-supplied secrets: team-declared provider, team-declared keys.
[env.vars]
ANTHROPIC_API_KEY = "vault://anthropic-api-key"
OPENAI_API_KEY    = "vault://openai-api-key"

# Team-required secrets the USER must supply from their own overlay.
# The team names what it NEEDS and describes why, without naming the
# user's vaults or keys. Apply fails if unset at resolve time.
[env.required]
GITHUB_TOKEN = "GitHub PAT with repo:read scope"

# Recommended: apply continues with a loud stderr warning if unset.
[env.recommended]
SENTRY_DSN = "Sentry error reporting — optional but helpful"

# Optional: apply continues with an info log if unset.
[env.optional]
DEBUG_WEBHOOK_URL = "Personal debug webhook — entirely optional"
```

**Personal config** (`dangazineu/dot-niwa/niwa.toml`, always private):

```toml
# Personal single vault declared anonymously (R2).
[global.vault.provider]
kind    = "infisical"
project = "dangazineu-personal"

# Per-org bindings. Key names here must match the team's
# [env.required]/[env.recommended]/[env.optional] tables (or
# [env.vars] for team-supplied defaults I want to override).
# I name my vault paths however I want -- the team config doesn't
# care and can't see the names.
[workspaces.tsukumogami.env.vars]
GITHUB_TOKEN = "vault://tsukumogami/github-pat"
SENTRY_DSN   = "vault://tsukumogami/sentry-dsn"
# DEBUG_WEBHOOK_URL intentionally omitted; I'll just get an info log.

[workspaces.codespar.env.vars]
GITHUB_TOKEN = "vault://codespar/github-pat"
```

**What happens on `niwa apply` in a `tsukumogami` workspace:**

- `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` → resolved by the team
  config's `[vault.provider]`; no provider name in the URI because
  the team declared anonymously.
- `GITHUB_TOKEN`, `SENTRY_DSN` → resolved by my personal
  `[global.vault.provider]` via the folder path I chose
  (`tsukumogami/github-pat`, `tsukumogami/sentry-dsn`). The team
  config has no opinion on where in my vault they live.
- `DEBUG_WEBHOOK_URL` → missing, declared `[env.optional]`, info
  log only, apply proceeds.

Same binary, same command, two backends resolved in one pass. Each
config owns only its own vault names and its own URI references; the
contract between them is the key-name vocabulary in `[env.required]`
/ `[env.recommended]` / `[env.optional]` (and `[env.vars]` for
team-supplied defaults).

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

Three distinct paths, all expected to work without changes to the team
config:

1. **Replace the team provider in my personal overlay.** In my
   personal `dot-niwa/niwa.toml`:
   ```toml
   [workspaces.tsukumogami.vault.providers.team]
   kind = "sops"
   config = ".sops.yaml"
   ```
   My local sops store now answers what the team's Infisical project
   would have answered. Vault refs in the team config
   (`vault://team/...`) resolve against my backend without any edit
   to the team config itself. (This rides R12's per-provider-name
   last-writer-wins merge in `GlobalOverride.Vault.Providers`.)

2. **Override individual secret refs at the key level.** In my
   personal overlay:
   ```toml
   [workspaces.tsukumogami.env.vars]
   ANTHROPIC_API_KEY = "vault://personal/my-own-anthropic-key"
   # Or, for a throwaway test session (personal repo is private, OK):
   # OPENAI_API_KEY = "sk-my-personal-key"
   ```
   Personal wins per R7. The team-supplied ref is shadowed only for
   the keys I redeclare.

3. **Skip secrets I don't need.** If my PR only touches docs and
   doesn't exercise code paths that hit the team vault's API:
   ```
   niwa apply --allow-missing-secrets
   ```
   Unresolved refs become empty strings with stderr warnings.

The default failure mode when a contributor runs `niwa apply` without
any of the three above must be a clear, actionable error: it MUST name
the provider that failed to resolve (e.g., "provider `team` is not
accessible: Infisical auth required") and MUST suggest the three
paths above with copy-pasteable pointers. A contributor must not get
a cryptic "vault reference failed" and have to reverse-engineer the
override mechanics.

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
vault backends through a single interface. v1 ships with two peer
backends, positioned for different team preferences:

- **Infisical** (hosted, OOTB, free-tier cloud). Bootstrap is
  `infisical login` → browser OAuth → done. Free tier covers 5
  identities, 3 projects, 3 envs. MIT-licensed core with a self-host
  option (Docker). This is the low-effort path for teams that want
  minimal setup and managed rotation/audit.
- **sops + age** (git-native, vendor-neutral, self-hosted-keys).
  Secrets live encrypted inside the team repo; decryption uses a local
  age key pair; no external service. This is the path for teams that
  want zero-cost, zero-vendor, fully-offline operation and are
  comfortable managing age keys.

Neither backend is "the default"; both are supported in v1 and users
pick per team. Additional backends (Doppler, 1Password, HashiCorp Vault
OSS, Bitwarden Secrets Manager, Pulumi ESC) are out of scope for v1 but
must be addable without changing the interface.

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

**Reference-accepting locations.** References are accepted in:
`[env.vars]`, `[claude.env.vars]`, `[repos.<name>.env.vars]`,
`[instance.env.vars]`, `[files]` source keys, and `[claude.settings]`
values. References are NOT accepted in: `[claude.content.*]`,
`[env.files]`, `[vault.providers.*]` fields, or anywhere an
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
targeting.

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
an `--allow-missing-secrets` flag that downgrades unresolved references
to empty strings with stderr warnings. Intended for debug and CI
fallback cases.

**R11. Per-reference `?required=false` query parameter.** A vault URI
MAY include `?required=false` to mark the reference as optional.
Unresolved optional references become empty strings without an error
or a warning.

**R12. `GlobalOverride.Vault` field.** The `GlobalOverride` struct
MUST support an optional `Vault *VaultRegistry` field so a personal
config can declare its own providers. Merge semantics: per-provider-
name last-writer-wins (personal can add new providers or replace a
team-declared provider for the same name).

**R13. `niwa status --audit-secrets` subcommand.** niwa MUST provide
a command that enumerates all `[env]`, `[claude.env]`, and
`[repos.*.env]` entries across the current workspace, classifies each
value as `plaintext`, `vault-ref`, or `empty`, and prints a table.
Exits non-zero if any plaintext values are present AND a vault is
configured.

**R14. Public-repo plaintext-secret guardrail.** When the workspace
config's source git remote resolves to a public GitHub repository AND
a vault is configured AND plaintext values are present in `[env.vars]`
or `[claude.env.vars]`, `niwa apply` MUST refuse to proceed with an
error listing the plaintext keys and recommending migration. Detection
uses the git remote URL.

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

**R18. Bootstrap under 10 minutes (sops backend).** A new developer
joining a team with the sops+age backend must complete: install niwa +
sops + age, generate an age key, publish the public key to the team's
`dot-niwa` repo via a PR, wait for the team lead to re-encrypt and
merge. End-to-end target: under 10 minutes once the PR is merged. This
is a usability budget; the PRD does not prescribe the UX to achieve
it, but the design doc that follows must demonstrate that the budget
is met.

**R19. Apply-time resolution under 5 seconds for workspaces with
≤ 20 vault references.** Resolution happens once per `niwa apply` per
reference. Total resolution time MUST stay under 5 seconds in the 80%
case. Large workspaces with > 20 references may exceed this and should
emit a progress indicator.

**R20. Zero additional external dependencies on niwa's side.** The
pluggable interface MUST be implemented in niwa's Go code without
pulling in a vault-specific Go library. niwa invokes provider CLIs
(`sops`, `age`, `infisical`) as subprocesses. Users install provider
CLIs themselves; niwa does not bundle them.

### Security Requirements (the 12 "never leaks" invariants)

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

**R30 (INV-PUBLIC-REPO-GUARDRAIL).** The R14 guardrail is a hard block:
`niwa apply` on a public-remote config repo with plaintext env values
and a vault configured MUST fail unless the user explicitly overrides
with `--allow-plaintext-secrets` (NOT a default-friendly flag).

**Deferred invariants.** An earlier draft included
`INV-EXPLICIT-SUBPROCESS-ENV` (niwa builds child process env
explicitly, never passes `os.Environ()` unfiltered to subprocesses).
It was removed because niwa today has no subprocess-spawn path that
touches resolved secrets — the invariant enforces nothing against
existing code. When niwa later adds a subprocess-spawn path carrying
secrets (e.g., invoking hook scripts or launching Claude Code), this
invariant re-enters scope as part of that feature's design.

### Contract Requirements

**R33. Three-level env requirement tables: required / recommended /
optional.** The team config declares which env vars the workspace
expects, without naming where they come from. Three tables, each
mapping key names to human-readable description strings:

- `[env.required]`: keys that MUST resolve to a non-empty value by
  apply time. Missing ≥1 required key is a hard error; `niwa apply`
  fails with a message listing every missing key and its description.
- `[env.recommended]`: keys the workspace expects but can operate
  without. Missing recommended keys emit a loud stderr warning
  naming each missing key; `niwa apply` proceeds.
- `[env.optional]`: keys that are genuinely nice-to-have. Missing
  optional keys emit an info log (visible only with `--verbose` or
  equivalent); `niwa apply` proceeds without warning.

Same pattern MUST be supported for Claude-scoped env declarations:
`[claude.env.required]`, `[claude.env.recommended]`,
`[claude.env.optional]`. These apply to env vars consumed by Claude
Code via `settings.local.json` (promoted or set directly).

Per-repo, per-instance, and `[files]`-scoped requirement tables are
NOT supported in v1. Teams that need a requirement only inside a
specific repo or instance declare it at the workspace level and
accept that the requirement applies across the workspace. If concrete
use cases for scoped requirements emerge post-v1, adding
`[repos.<name>.env.required]`, `[instance.env.required]`, or
`[files.required]` tables is additive and non-breaking.

The description string values MUST be carried into the error /
warning / info message so missing-secret diagnostics are
self-documenting. A user running `niwa apply` against an unmet
requirement sees the team-authored description telling them what the
key is for.

**R34. `[env.required]` has precedence over `--allow-missing-secrets`.**
The `--allow-missing-secrets` flag (R10) downgrades unresolved
`vault://` references to empty strings. It does NOT downgrade
unresolved `[env.required]` keys. A missing required key is always a
hard error regardless of flags, because the team config explicitly
marked the key as load-bearing. This separation lets users use
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
- [ ] `workspace.toml` accepts `[env.required]`, `[env.recommended]`,
  `[env.optional]` with key→description-string entries.
- [ ] The same three tables are accepted under `[claude.env]`.
- [ ] `workspace.toml` parse-rejects `[repos.<name>.env.required]`,
  `[instance.env.required]`, and `[files.required]` (plus their
  `.recommended` / `.optional` siblings) as unknown-field warnings.
  These locations are reserved for future expansion; v1 does not
  accept them.
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
  the three override paths (US-9).
- [ ] A contributor can replace the team provider entirely by
  declaring `[workspaces.<scope>.vault.providers.team]` in their
  personal overlay with a different `kind`; subsequent
  `niwa apply` resolves team refs via the overlay's provider without
  any edit to the team config.
- [ ] A contributor can shadow an individual team vault reference by
  declaring a literal or a different `vault://` URI for the same key
  in their personal overlay's `[workspaces.<scope>.env.vars]`
  (et al.); personal value wins per R7.
- [ ] A `team_only` lock (R8) surfaces as a distinct error from
  provider-auth failure and is NOT bypassable by the contributor's
  personal overlay.

### Backends (v1: sops + Infisical)

- [ ] `kind = "sops"` with `config = ".sops.yaml"` resolves secrets from
  sops-encrypted files in the config repo.
- [ ] `kind = "infisical"` with `project = "<proj-id>"` resolves secrets
  from Infisical Cloud or self-hosted Infisical.
- [ ] Each backend can be used standalone (team uses sops, or team uses
  Infisical; users' personal overlays may mix backends).
- [ ] Adding a new backend requires implementing a single Go interface
  and registering it — no changes to the resolver or schema.

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
- [ ] A functional test confirms `niwa apply` on a public-remote config
  with plaintext values and a vault configured fails without
  `--allow-plaintext-secrets`.
- [ ] A functional test confirms `niwa apply --allow-plaintext-secrets`
  proceeds under the same conditions with a loud warning.
- [ ] A functional test confirms no argv accepts a secret value on any
  subcommand.

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
- **Additional vault backends beyond sops and Infisical.** Doppler,
  1Password, HashiCorp Vault OSS, Bitwarden Secrets Manager, Pulumi
  ESC, AWS Secrets Manager, Azure Key Vault all stay deferred. The
  pluggable interface means they can be added post-v1 without
  rearchitecting, but they are not v1 deliverables.
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

### D-1. Pluggable backend interface from v1, with Infisical + sops as peer backends

**Decided:** Ship a pluggable provider interface from v1 with **two
peer backends — Infisical Cloud as the OOTB hosted option, sops+age as
the vendor-neutral self-hosted-keys option.** Neither is framed as
"the default"; users pick per team.

**Alternatives considered:** (a) ship sops-only in v1, add Infisical in
v2 with a refactor; (b) ship Infisical-only in v1 and defer sops for
OSS-purists; (c) ship one commercial backend with free tier (Doppler
has the cleanest OAuth UX but is closed-source SaaS); (d) ship sops as
"default" with Infisical as "the upgrade" (earlier draft of this PRD).

**Rationale:** The OOTB-low-effort axis and the vendor-neutral axis
pull in different directions, and neither should be sacrificed.
Shipping both peer backends means indie developers and small teams get
a true browser-OAuth bootstrap (Infisical) as a first-class option,
while OSS-conscious teams get a fully-local-keys option (sops) without
the hosted-service-dependency. The interface is small (`Resolve(key)
-> Secret + metadata`); the cost of building it from v1 is one extra
indirection vs. later refactoring once a second backend is added.
Doppler was rejected as a third v1 backend because it's closed-source
SaaS, which conflicts with niwa's stated vendor-neutrality goals.

**Implementation ordering.** If implementation must be sequential,
Infisical lands first: its bootstrap exercises every layer (browser
OAuth, API calls, session caching in the provider CLI) and battle-tests
the interface. sops is simpler (subprocess + file decrypt) and follows.
Sequencing does not change what ships in v1.0 — both are v1 peers.

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
names in `[env.vars]` / `[env.required]` / `[env.recommended]` /
`[env.optional]` (and the Claude / repos / instance / files
variants), NOT shared vault provider names.

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
scoping with an `[env.required]`-style contract keeps each layer
owning only its own namespace and reduces the team/personal coupling
to a vocabulary of key names.

### D-10. Three-level env requirements: required / recommended / optional

**Decided:** The team config declares expected env var names and
their failure policy via three tables:
- `[env.required]` — hard error on miss.
- `[env.recommended]` — loud warning on miss, apply continues.
- `[env.optional]` — info log on miss, apply continues.

Same pattern for `[claude.env.*]`. Values are human-readable
description strings surfaced in the diagnostic message.

Requirement tables at `[repos.<name>.env.*]`, `[instance.env.*]`,
and `[files.*]` scopes are deferred. Without a concrete user story
asking for repo- or instance-scoped requirements, shipping them in
v1 is speculative generality. If such use cases emerge (e.g., a repo
that strictly needs a particular PAT type), adding the tables is
additive and non-breaking.

**Alternatives considered:** (a) single `[env.required]` binary
table (required vs silent — too blunt); (b) per-key inline flag
syntax in `[env.vars]` like `GITHUB_TOKEN = "required"` (mixes value
and metadata, ugly); (c) rely on downstream tools to error on
missing env (too implicit; niwa can't self-document needs).

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

## Open Questions

Questions to resolve before the PRD transitions to Accepted:

- **Q-1 Personas sign-off.** This draft's nine user stories cover the
  three archetypes identified during exploration (indie, team-lead,
  team-member). Are there other archetypes that need explicit stories
  before shipping — e.g., CI-only caller, external contributor to a
  public dot-niwa, new-dev who doesn't have GitHub org access on day
  one?


- **Q-3 Plaintext-deprecation timeline.** When does the public-repo
  plaintext-secret guardrail (R14/R30) become the default? On day one
  (feature-flag off → hard guardrail) or staged through a release
  cycle (warn for two minor releases, then error)?

- **Q-4 Rollout metrics.** What's the success signal? Number of
  plaintext values in `tsukumogami/dot-niwa` drops to zero? Number of
  GitHub secret-scanning alerts? Number of users running
  `niwa apply --allow-plaintext-secrets` stays below some threshold?

- **Q-5 Source-org detection for workspace with no sources.** R5
  already specifies that zero-source workspaces *may* set
  `vault_scope` explicitly for personal overlay targeting. The open
  question is narrower: when zero-source AND no `vault_scope`, should
  niwa apply silently fall back to "no personal overlay resolution"
  (treat as team-only), or warn that the workspace has no scope and
  personal refs will be unresolvable? Leaning warn — silent fallback
  hides a real misconfiguration.

- **Q-6 Migration UX details.** What does the first-time migration
  walkthrough look like? A dedicated `niwa vault init` subcommand, or
  a combination of manual steps documented in the README? Research
  leaned toward manual steps for v1 (`niwa vault import` is deferred),
  but a guided walkthrough might be worth it for onboarding.

- **Q-7 `team_only` enforcement layer.** Is violation caught at
  parse time (static check against the committed personal overlay) or
  at materialize time (runtime check when resolving)? Static is
  cleaner but requires `niwa status` to load the team config's
  `team_only` list, which means remote read of team repo. Runtime is
  always available.

- **Q-8 Sign-off stakeholders.** Does this PRD need explicit review
  from anyone beyond the niwa maintainer before transitioning to
  Accepted — e.g., security-minded community members, the
  `tsukumogami/tsuku` ecosystem maintainers, or contributors with
  signaled interest in the feature?
