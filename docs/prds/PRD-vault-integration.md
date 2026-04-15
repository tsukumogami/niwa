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
(introduced in v0.5.0) that layers a personal config repo over the team
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
  their personal config repo and have niwa pick the right one
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
- **Pluggable backend from v1.** sops + age ships as the default
  (zero-cost, git-native, vendor-neutral) and Infisical ships as the
  hosted-backend peer in v1. The interface allows additional backends
  (Doppler, 1Password, HashiCorp Vault OSS, Bitwarden Secrets Manager)
  to be added without rearchitecting.

## User Stories

### US-1: Team lead publishes the team config repo

As a small-team lead maintaining `org/dot-niwa`, I want to move all
plaintext API tokens into a vault so that I can flip the repo to public
without leaking secrets. I expect niwa to detect plaintext values in my
committed config and refuse to apply on a public remote once a vault is
configured, so I can't accidentally re-introduce plaintext.

### US-2: Team member bootstraps vault access via GitHub org

As a new developer joining `tsukumogami`, I want to unlock the team
vault using only credentials I already have (my GitHub org membership),
so that my first `niwa apply` succeeds without asking a teammate for a
shared password or service token. I expect the bootstrap to take less
than 10 minutes end-to-end.

### US-3: Developer uses different PATs for different orgs

As a developer whose personal config declares PATs for both `tsukumogami`
and `codespar`, I want niwa to pick the `tsukumogami` PAT when I
`niwa apply` in a `tsukumogami` workspace and the `codespar` PAT in a
`codespar` workspace, so that I never have to manually switch credentials
between sessions. The scoping should be automatic in the 80% case
(single-source workspaces) and explicit for multi-source workspaces.

### US-4: Developer overrides a team secret for a debug session

As a developer debugging an integration, I want to override a specific
team-supplied secret with my own local value for one session, without
editing the team config repo. My personal config layer should let me
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
that personal configs cannot shadow, with a clear error when a personal
config tries.

### US-8: Developer audits their plaintext-to-vault migration

As a developer migrating an existing workspace to use a vault, I want a
command that scans my config and tells me which env values are still
plaintext vs. already vault-backed, so I can track migration progress.

## Requirements

### Functional Requirements

**R1. Pluggable vault provider interface.** niwa MUST support multiple
vault backends through a single interface. v1 ships with two backends:
sops + age (git-native, no external service) and Infisical (hosted, with
self-host option). Additional backends (Doppler, 1Password, HashiCorp
Vault OSS, Bitwarden Secrets Manager) are out of scope for v1 but must
be addable without changing the interface.

**R2. Top-level `[vault.providers.*]` registry.** The workspace config
MUST support a top-level `[vault]` table with a `providers` sub-table
registering named vault backends. Each provider entry carries a `kind`
(which backend) plus provider-specific locator fields (e.g.,
`project = "..."` for Infisical, `config = ".sops.yaml"` for sops).

**R3. `vault://<provider>/<key>` URI reference scheme.** Any string
value in a workspace config that begins with `vault://` MUST be
interpreted as a reference to a secret in the named provider. The URI
format is `vault://<provider>/<key>[?required=<bool>]`. References are
accepted in: `[env.vars]`, `[claude.env.vars]`, `[repos.<name>.env.vars]`,
`[instance.env.vars]`, `[files]` source keys, and `[claude.settings]`
values. References are NOT accepted in: `[claude.content.*]`,
`[env.files]`, `[vault.providers.*]` fields, or anywhere an
identifier (workspace name, org, repo URL, group name) lives.

**R4. Per-project personal vault scoping.** niwa MUST support per-
workspace personal vault declarations via the existing
`GlobalConfigOverride.Workspaces` map, keyed by the workspace's source
org name. When a workspace has one source (`len(ws.Sources) == 1`),
niwa resolves personal vault providers and references from
`[workspaces.<ws.Sources[0].Org>]` in the personal config.

**R5. Explicit `workspace.vault_scope` escape hatch.** A workspace
config MAY set `[workspace].vault_scope = "<string>"` to override
the implicit source-org scoping. Multi-source workspaces
(`len(ws.Sources) > 1`) MUST set this field or niwa fails to apply.
Zero-source workspaces MAY set this field for personal overlay
targeting.

**R6. Resolution chain.** When resolving a `vault://` reference, niwa
MUST consult vault providers in this order: personal-scoped (from
`[workspaces.<scope>]` in personal config) → personal-default (from
`[global]` in personal config) → team (from workspace config). First
successful lookup wins.

**R7. Personal-wins conflict resolution.** When the same secret key
would be supplied by both the team vault and a personal vault (e.g.,
both declare `GITHUB_TOKEN` as a vault reference), the personal layer
MUST win. This mirrors the existing `MergeGlobalOverride` precedence.

**R8. `team_only` opt-in for team-controlled keys.** A team workspace
config MAY declare `[vault].team_only = ["KEY1", "KEY2"]`. When a
personal config tries to supply a value for any key in this list
(either via a `vault://` reference or a plaintext override), niwa MUST
refuse to apply with an error naming the conflicting key.

**R9. Fail-hard resolution by default.** When a `vault://` reference
can't be resolved (provider unreachable, auth expired, key missing),
`niwa apply` MUST fail with a clear error naming the reference, unless
overridden by R10 or R11.

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
content hash. The fingerprint captures the resolution inputs (config
reference + vault version/etag metadata). `niwa status` uses the
fingerprint to report:
- `drifted` when content hash differs AND source fingerprint unchanged
  (user edited the file).
- `stale` when content hash differs AND source fingerprint also differs
  (upstream vault rotated).
- `ok` when content hash matches.

**R16. Re-resolution on every apply.** `niwa apply` MUST re-resolve
every `vault://` reference on every invocation. No niwa-internal cache
between commands. This picks up upstream rotations automatically.
Provider CLIs may cache their own auth sessions (out of niwa's scope).

**R17. Escape syntax for literal `vault://` strings.** A string value
whose literal content legitimately begins with `vault://` MUST be
escapable via a `raw:` prefix: `raw:vault://...` decodes to the
literal string `vault://...`. This covers the edge case where a
non-niwa tool uses `vault://` for its own URI scheme.

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
a `secret.Value` opaque type whose default formatters (`String`,
`GoString`) emit `***`. Any stderr, stdout, or structured log output
that prints a `secret.Value` renders the redacted form. Compile-time
and test-time checks enforce that no bare string originating from
vault resolution reaches a formatter.

**R23 (INV-NO-CONFIG-WRITEBACK).** niwa MUST NEVER write a resolved
secret value back into `ConfigDir` (the cloned config repo working
tree). Resolution is read-only with respect to config.

**R24 (INV-FILE-MODE).** Every materialized file that contains a
resolved secret MUST be written with mode `0o600`. This fixes a
pre-existing `0o644` bug in `SettingsMaterializer` and
`EnvMaterializer` where settings and env files were world-readable.

**R25 (INV-LOCAL-INFIX).** Every materialized file that contains a
resolved secret MUST carry the `.local` infix in its name so it
matches the existing niwa/Claude gitignore conventions.

**R26 (INV-GITIGNORE-ROOT).** `niwa create` MUST ensure the instance
root has a `.gitignore` that covers `*.local*`. If the file is
missing, `niwa create` generates it. If present, niwa merges the
required patterns in (idempotent).

**R27 (INV-NO-CLAUDE-MD-INTERP).** CLAUDE.md, CLAUDE.local.md, and
all materialized Markdown files MUST be treated as opaque text. No
secret interpolation into Markdown content. Secrets reach Claude via
env var injection in `settings.local.json`, not via inline Markdown
templates.

**R28 (INV-NO-STATUS-CONTENT).** `niwa status` MUST NOT print the
content or diff of any managed file. Output is limited to path +
status (`ok | drifted | stale | removed`). Future `--verbose` modes
MUST exclude files flagged as secret-bearing.

**R29 (INV-NO-PROCESS-ENV-PUBLICATION).** niwa MUST NEVER call
`os.Setenv` with a resolved secret. Secrets are project data owned by
the materializer, not niwa's process-level state.

**R30 (INV-NO-DISK-CACHE).** niwa MUST NOT cache resolved secrets on
disk between commands. Provider CLIs may cache their own auth sessions
(out of scope); niwa does not store secret values beyond the lifetime
of a single command invocation.

**R31 (INV-EXPLICIT-SUBPROCESS-ENV).** When niwa eventually spawns a
subprocess that needs secrets (vault CLI calls, future hook scripts),
the child env MUST be built explicitly from the resolved map. niwa
does not pass `os.Environ()` to child processes without filtering.

**R32 (INV-PUBLIC-REPO-GUARDRAIL).** The R14 guardrail is a hard block:
`niwa apply` on a public-remote config repo with plaintext env values
and a vault configured MUST fail unless the user explicitly overrides
with `--allow-plaintext-secrets` (NOT a default-friendly flag).

## Acceptance Criteria

### Schema

- [ ] `workspace.toml` accepts `[vault.providers.<name>]` with `kind`
  plus provider-specific fields.
- [ ] `workspace.toml` accepts `vault://<provider>/<key>` URI values in
  `[env.vars]`, `[claude.env.vars]`, `[repos.<name>.env.vars]`,
  `[instance.env.vars]`, `[files]` source keys, and `[claude.settings]`
  values.
- [ ] `workspace.toml` rejects `vault://` URIs in `[claude.content.*]`
  source paths, `[env.files]` source paths, `[vault.providers.*]`
  fields, and identifier fields (workspace name, source org, repo URL,
  group name) with parse-time errors.
- [ ] `workspace.toml` accepts `[workspace].vault_scope = "<string>"`.
- [ ] `workspace.toml` accepts `[vault].team_only = ["KEY1", ...]`.
- [ ] The personal config (`GlobalConfigOverride`) accepts a `[vault]`
  block and per-workspace `[workspaces.<scope>]` blocks.
- [ ] `raw:vault://...` literal escape is accepted wherever string
  values appear.

### Resolution

- [ ] A workspace with `[[sources]] org = "tsukumogami"` and a personal
  config with `[workspaces.tsukumogami.vault.providers.personal] kind = "sops"`
  resolves `vault://personal/github-pat` via the sops provider scoped
  to `tsukumogami`.
- [ ] A workspace with 2 sources and no `vault_scope` fails `niwa apply`
  with an error naming the ambiguity.
- [ ] A workspace with 2 sources and `vault_scope = "tsukumogami"`
  resolves personal vault from `[workspaces.tsukumogami]`.
- [ ] When personal config shadows a team-supplied key, the personal
  value wins in the resolved env.
- [ ] When personal config tries to shadow a key listed in team
  `team_only`, `niwa apply` fails with an error naming the key.
- [ ] A `vault://` reference to a nonexistent key fails `niwa apply`
  with a clear error, unless `--allow-missing-secrets` or
  `?required=false` is set.
- [ ] `niwa apply --allow-missing-secrets` resolves missing references
  to empty strings and emits stderr warnings.
- [ ] `vault://provider/key?required=false` resolves to empty string
  when missing, with no warning.

### Backends (v1: sops + Infisical)

- [ ] `kind = "sops"` with `config = ".sops.yaml"` resolves secrets from
  sops-encrypted files in the config repo.
- [ ] `kind = "infisical"` with `project = "<proj-id>"` resolves secrets
  from Infisical Cloud or self-hosted Infisical.
- [ ] Each backend can be used standalone (team uses sops, or team uses
  Infisical; users' personal configs may mix backends).
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
- **CLAUDE.md secret interpolation.** Explicitly forbidden per R27.
  Secrets reach Claude via settings env, not via Markdown templates.
- **Non-GitHub source control.** Public-repo guardrail (R14) detects
  via GitHub remote URL patterns only in v1. GitLab, Bitbucket,
  self-hosted Gitea stay in a deferred list.

## Decisions and Trade-offs

Decisions settled during the exploration that inform this PRD. Each
captures what was decided, what alternatives were considered, and why
the chosen option won. Downstream design docs should treat these as
settled.

### D-1. Pluggable backend interface from v1

**Decided:** Ship a pluggable provider interface from v1, with sops+age
as the default backend and Infisical as the hosted peer.

**Alternatives considered:** (a) ship sops-only in v1, add Infisical in
v2 with a refactor; (b) ship one commercial backend with free tier; (c)
ship sops-only forever (vendor-neutral orthodoxy).

**Rationale:** If niwa commits to sops alone, the Infisical integration
becomes a later refactor where the abstraction is exposed late and
risks being wrong. The interface is small (`Resolve(key) -> Secret +
metadata`); the cost of building it from v1 is one extra indirection.
Vendor-neutrality is a stated niwa value, so Infisical must be a peer,
not "the upgrade path."

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

**Decided:** When team and personal configs supply the same key,
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

### D-8. `raw:` prefix for literal `vault://` escape

**Decided:** A string literal that legitimately begins with `vault://`
is escaped via a `raw:` prefix: `raw:vault://...` decodes to
`vault://...`.

**Alternatives considered:** (a) `\vault://...` backslash escape; (b)
no escape (any string starting with `vault://` is always a reference).

**Rationale:** Backslash escape collides with shell quoting
expectations. No-escape risks user confusion if a downstream tool
happens to use `vault://` for its own URI scheme. `raw:` is rare
enough not to collide and mnemonic enough to read naturally.

## Open Questions

Questions to resolve before the PRD transitions to Accepted:

- **Q-1 Personas sign-off.** This draft's eight user stories cover the
  three archetypes identified during exploration (indie, team-lead,
  team-member). Are there other archetypes that need explicit stories
  before shipping — e.g., CI-only caller, external contributor to a
  public dot-niwa, new-dev who doesn't have GitHub org access on day
  one?

- **Q-2 v1 scope boundary.** Does v1 ship both sops+age AND Infisical
  as peer backends, or does sops ship in v1.0 and Infisical in v1.1?
  The decision affects the pluggable interface's validation surface.
  Leaning toward both in v1.0 so the interface is battle-tested from
  the start.

- **Q-3 Plaintext-deprecation timeline.** When does the public-repo
  plaintext-secret guardrail (R14/R32) become the default? On day one
  (feature-flag off → hard guardrail) or staged through a release
  cycle (warn for two minor releases, then error)?

- **Q-4 Rollout metrics.** What's the success signal? Number of
  plaintext values in `tsukumogami/dot-niwa` drops to zero? Number of
  GitHub secret-scanning alerts? Number of users running
  `niwa apply --allow-plaintext-secrets` stays below some threshold?

- **Q-5 Source-org detection for workspace with no sources.** A
  workspace created with `niwa init <name>` (no `--from`) has no
  `[[sources]]`. The implicit scoping falls back to empty. Should
  `vault_scope` default to the workspace name in this case, or require
  explicit declaration?

- **Q-6 Migration UX details.** What does the first-time migration
  walkthrough look like? A dedicated `niwa vault init` subcommand, or
  a combination of manual steps documented in the README? Research
  leaned toward manual steps for v1 (`niwa vault import` is deferred),
  but a guided walkthrough might be worth it for onboarding.

- **Q-7 `team_only` enforcement layer.** Is violation caught at
  parse time (static check against the committed personal config) or
  at materialize time (runtime check when resolving)? Static is
  cleaner but requires `niwa status` to load the team config's
  `team_only` list, which means remote read of team repo. Runtime is
  always available.

- **Q-8 Sign-off stakeholders.** Does this PRD need explicit review
  from anyone beyond the niwa maintainer before transitioning to
  Accepted — e.g., security-minded community members, the
  `tsukumogami/tsuku` ecosystem maintainers, or contributors with
  signaled interest in the feature?
