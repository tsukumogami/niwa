# Vault Integration

A walkthrough for teams adopting vault-backed secrets in niwa workspaces.
The vault feature keeps team configs publishable without leaking secrets
and lets each developer layer their own per-project credentials on top.

## What you get

- **Publishable team configs.** Move `org/dot-niwa` from private to
  public without any secret value reaching git history. The guardrail
  blocks `niwa apply` when a public-GitHub remote still has plaintext
  in `[env.secrets]`.
- **Per-project personal scoping.** A developer with separate GitHub
  PATs for `tsukumogami` and `codespar` binds each one in their
  personal overlay and niwa picks the right credential automatically.
- **Infisical as the v1 backend.** `infisical login` gets a new team
  member from zero to a successful `niwa apply` in under two minutes.
  The provider interface is pluggable; sops + age is deferred to v1.1.
- **Safety invariants enforced at the type system.** Secrets are
  opaque `secret.Value`s that redact under every standard Go
  formatter. Materialized files write `0o600` unconditionally.
  Provider stderr is scrubbed before it reaches any error chain.

## Quick start (Infisical)

Target: from `infisical login` to a successful `niwa apply` in under
two minutes.

### 1. Install the Infisical CLI

Follow the vendor instructions at
[infisical.com/docs/cli/overview](https://infisical.com/docs/cli/overview).
Verify with `infisical --version`.

### 2. Log in

```bash
infisical login
```

A browser opens for OAuth. Your session token is cached by the
Infisical CLI; niwa never sees it and never writes it anywhere.

### 3. Declare the provider in `workspace.toml`

Add a top-level `[vault.provider]` block:

```toml
[vault.provider]
kind    = "infisical"
project = "<your-infisical-project-id>"
```

### 4. Reference secrets with `vault://`

```toml
[env.secrets]
ANTHROPIC_API_KEY = "vault://ANTHROPIC_API_KEY"
```

The URI points at a key inside the Infisical project declared above.

### 5. Apply

```bash
niwa apply
```

niwa resolves `vault://ANTHROPIC_API_KEY` against the Infisical CLI,
materializes the `.local.env` file at mode `0o600`, and writes an
instance-root `.gitignore` covering `*.local*`. When a team secret
rotates upstream between two applies, the second apply re-resolves
and prints `rotated <path>` to stderr for every materialized file
whose vault-backed sources changed.

If your current directory is not a git repository (or is a git repo
with no remotes configured), the public-repo guardrail can't
enumerate remotes and silently skips with a warning. See
[§Public-repo guardrail](#public-repo-guardrail) for what the
guardrail actually checks.

## Schema anatomy

### Anonymous singular vs named multiple

A config file declares its providers one of two ways. Mixing both in
the same file is a parse error.

**Anonymous** — one provider, no name:

```toml
[vault.provider]
kind    = "infisical"
project = "tsukumogami"
```

URIs in this file take the form `vault://<key>`.

**Named** — one or more providers, each with a key under `[vault.providers.*]`:

```toml
[vault.providers.prod]
kind    = "infisical"
project = "prod-secrets"

[vault.providers.sandbox]
kind    = "infisical"
project = "sandbox-secrets"
```

URIs in this file take the form `vault://<name>/<key>`, where `<name>`
must match a declared provider in the same file. Cross-file provider
names never resolve — team configs can't reference `vault://personal/...`
and personal overlays can't reference `vault://team/...`.

### `[env.vars]` vs `[env.secrets]`

Non-sensitive values live in `[env.vars]`:

```toml
[env.vars]
LOG_LEVEL   = "info"
DEFAULT_ORG = "tsukumogami"
```

Sensitive values live in `[env.secrets]`:

```toml
[env.secrets]
ANTHROPIC_API_KEY = "vault://ANTHROPIC_API_KEY"
```

The split matters because the public-repo guardrail walks
`*.secrets` only. A plaintext literal in `[env.secrets]` blocks
apply on a public remote; the same literal in `[env.vars]` does
not. Putting a secret in `[env.vars]` by mistake is exactly what
the split exists to prevent — use `[env.secrets]` whenever a value
is sensitive.

Values that resolve from `[env.secrets]` are wrapped in the opaque
`secret.Value` type. Every formatter (`%s`, `%v`, `%+v`, `%q`) emits
`***` and `MarshalJSON` emits `"***"`. Values from `[env.vars]` are
plain strings.

The same split applies under `[claude.env]`:
`[claude.env.vars]` and `[claude.env.secrets]`.

### Requirement sub-tables

Each of `[env.vars]`, `[env.secrets]`, `[claude.env.vars]`,
`[claude.env.secrets]` accepts three optional sub-tables. Values are
human-readable descriptions, not env values. niwa surfaces the
description in the diagnostic when a key is missing.

```toml
[env.secrets.required]
GITHUB_TOKEN = "GitHub PAT with repo:read scope"

[env.secrets.recommended]
SENTRY_DSN = "Sentry error reporting"

[env.vars.optional]
DEBUG_WEBHOOK_URL = "Personal debug webhook"
```

| Sub-table | Behavior on miss |
|-----------|------------------|
| `*.required` | Hard error; `niwa apply` fails. Error names the key, the scope (e.g. `env.secrets`), and the description string. |
| `*.recommended` | Stderr warning per missing key; apply continues. |
| `*.optional` | Silent in v1; apply continues. Info-log output will land when a verbose flag is added. |

`--allow-missing-secrets` downgrades vault misses to empty strings
but does NOT downgrade `*.required` misses. A required key remains
a hard error even with the flag set.

### `[workspace].vault_scope`

Single-source workspaces scope the personal overlay automatically:
the source org IS the scope. Multi-source workspaces need an
explicit setting so niwa knows which `[workspaces.<scope>]` block
in the personal overlay applies:

```toml
[workspace]
name        = "my-monorepo"
vault_scope = "tsukumogami"
```

Apply fails on a multi-source workspace that has a personal overlay
registered and no `vault_scope`. The error names the declared source
orgs so the remediation is obvious (pick one, or pick a string that
targets the default `[global]` block).

### `[vault].team_only`

Keys a team does not want shadowed by personal overlays — for
example, a telemetry endpoint that rolls up to a team dashboard:

```toml
[vault]
team_only = ["TELEMETRY_ENDPOINT", "SHARED_METRICS_KEY"]
```

A personal overlay that tries to shadow a `team_only` key fails
with a distinct error. Personal-wins (see below) does not apply
to the allow-list.

## Personal overlay flow

The personal overlay is a separate config repo (named via
`niwa config set global <org/repo>`) that layers on top of the team
config. Personal values win when they shadow a team key, except for
`team_only` keys.

### Example: different PATs per source org

**Team config** — `tsukumogami/dot-niwa/.niwa/workspace.toml`:

```toml
[env.secrets.required]
GITHUB_TOKEN = "GitHub PAT with repo:read scope"
```

**Personal overlay** — `dangazineu/dot-niwa/niwa.toml`:

```toml
[global.vault.provider]
kind    = "infisical"
project = "dangazineu-personal"

[workspaces.tsukumogami.env.secrets]
GITHUB_TOKEN = "vault://tsukumogami/github-pat"

[workspaces.codespar.env.secrets]
GITHUB_TOKEN = "vault://codespar/github-pat"
```

When `niwa apply` runs in a `tsukumogami` workspace, it picks
`vault://tsukumogami/github-pat` from the personal overlay's
Infisical project. In a `codespar` workspace, it picks
`vault://codespar/github-pat`. Same binary, same command, different
credential resolved per project.

### Conflict resolution

| Situation | Winner |
|-----------|--------|
| Both layers set the same `[env.vars]` key | Personal |
| Both layers set the same `[env.secrets]` key | Personal |
| Personal tries to shadow a `team_only` key | Error |
| Personal declares a provider name the team already uses | Error (R12) |

Each shadow is visible in three places: a stderr diagnostic during
`niwa apply`, the `niwa status` summary line, and the
`--audit-secrets` SHADOWED column. Diagnostics name the key and the
overlay layer; they never print the shadowing or shadowed value.

### Why there's no "replace the whole team provider" path

File-local provider scoping (design D-9) rules out a single-line
personal-overlay knob that redirects all team vault refs to a
user-supplied provider. Two reasons. First, cross-file provider
name references couple the team config to the user's vault layout.
Second, a bulk-swap would open a supply-chain surface where a
compromised personal overlay silently redirects every team secret.
Per-key shadowing (the pattern above) is slightly more verbose but
each override is individually visible in the shadow diagnostics.

## Multi-org setup

### When you need this

The Infisical CLI session can only be logged into one organization at a
time. If all your vault providers point at projects in the same org,
`infisical login` is enough and you can skip this section entirely.

You need multi-org auth when your workspace references Infisical
projects across different organizations. Common scenarios:

- A team org for shared secrets plus a personal org for your own PATs.
- Two team orgs (different products or companies) plus a personal org.
- Any mix where a single `niwa apply` needs to reach more than one org.

### How it works

niwa reads an optional credential file at
`~/.config/niwa/provider-auth.toml` (must be `0o600`). For each vault
provider declared in your workspace or personal overlay, niwa checks
whether the credential file has a matching entry (same `kind` and
`project`). When it finds one, niwa authenticates via Infisical's
machine-identity API (a single HTTPS POST) and passes the resulting
short-lived token to the CLI with `--token`. Providers without a
matching entry fall back to the CLI's stored session, which is the
single-org default.

The pattern: log into whichever org you use most often with
`infisical login`. Create credential entries only for the other org(s).
You never need an entry for the org you're already logged into.

### Walkthrough: two-org setup

Suppose your team config references an Infisical project in the
Tsukumogami org, and your personal overlay references a project in your
own org. You log into Tsukumogami via `infisical login` (the org you
use most). The personal org needs a credential entry.

#### 1. Create a machine identity in Infisical

Open the Infisical dashboard for your personal org. Go to
**Organization Settings > Machine Identities** and create a new
identity. Give it read access to the project your personal overlay
references.

#### 2. Add universal auth to the identity

On the identity's detail page, enable **Universal Auth**. This gives
you a `client_id`.

#### 3. Create a client secret

Under the universal auth section, generate a new client secret. Copy
both the `client_id` and `client_secret` — you won't see the secret
again.

#### 4. Create the credential file

```bash
touch ~/.config/niwa/provider-auth.toml
chmod 0600 ~/.config/niwa/provider-auth.toml
```

niwa refuses to read this file if permissions aren't exactly `0o600`.

#### 5. Add the provider entry

Edit `~/.config/niwa/provider-auth.toml`:

```toml
[[providers]]
kind          = "infisical"
project       = "ffffffff-gggg-hhhh-iiii-jjjjjjjjjjjj"   # your personal project ID
client_id     = "11111111-2222-3333-4444-555555555555"
client_secret = "abcdef01..."
```

The `project` value must match the project ID in your workspace or
personal overlay's `[vault.provider]` block. You can find it in the
Infisical dashboard URL or project settings.

#### 6. Apply

```bash
niwa apply
```

niwa authenticates against your personal org via the credential entry,
uses the CLI session for the Tsukumogami org, and resolves secrets from
both.

### Credential file format

The file uses a `[[providers]]` array. Each entry is a flat TOML table
with a required `kind` field and backend-specific fields:

```toml
# ~/.config/niwa/provider-auth.toml
# Machine-identity credentials for multi-org auth.
# NEVER commit this file.

[[providers]]
kind          = "infisical"
project       = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
client_id     = "11111111-2222-3333-4444-555555555555"
client_secret = "abcdef01..."

[[providers]]
kind          = "infisical"
project       = "ffffffff-gggg-hhhh-iiii-jjjjjjjjjjjj"
client_id     = "..."
client_secret = "..."
api_url       = "https://infisical.corp.example.com/api"
```

For Infisical entries:

| Field | Required | Description |
|-------|----------|-------------|
| `kind` | Yes | Must be `"infisical"` |
| `project` | Yes | Infisical project UUID (used for matching) |
| `client_id` | Yes | Machine identity client ID |
| `client_secret` | Yes | Machine identity client secret |
| `api_url` | No | API base URL for self-hosted instances. Defaults to `https://app.infisical.com/api` |

niwa matches credential entries to workspace providers by the
`(kind, project)` pair. Each workspace provider is checked against the
credential file; if no match is found, that provider uses the CLI
session as usual.

Backends that don't need credential entries — sops, or single-org
Infisical where `infisical login` is enough — simply have no entries in
the file. The file itself is optional; single-org users never create it.

### Security notes

- **File permissions**: niwa checks that `provider-auth.toml` is
  exactly `0o600` before reading. If the file is world-readable or
  group-readable, apply fails with an error telling you to fix
  permissions.
- **Never commit this file.** It contains long-lived credentials.
  It lives in `~/.config/niwa/`, outside any git repo.
- **client_secret stays off argv.** niwa sends `client_id` and
  `client_secret` via HTTPS POST body to the Infisical API. The
  resulting JWT is passed to the CLI subprocess via `--token`, but the
  secret itself never appears in the process table.
- **No token cache.** niwa re-authenticates on every apply (~100ms per
  org). This avoids stale-token edge cases at a small latency cost.
- **Rotate periodically.** Infisical machine-identity client secrets
  don't expire by default. Rotate them via the Infisical dashboard on
  a schedule that fits your security policy.

## Plaintext-to-vault migration

There is no `niwa vault import` helper in v1. Migration is manual,
provider-specific, and auditable by `niwa status --audit-secrets`.

### 1. Enumerate existing plaintext values

```bash
niwa status --audit-secrets
```

Output is a table with columns KEY / CLASSIFICATION / TABLE /
SHADOWED. Every row classified as `plaintext` is a migration target.
The command exits non-zero when plaintext values are present AND a
vault is configured, so this doubles as a CI gate.

### 2. Push each value into the vault

For Infisical, run from your shell (the command never goes through
niwa):

```bash
infisical secrets set ANTHROPIC_API_KEY <value> --projectId <your-project>
```

Or use the Infisical web UI. niwa has no opinion on how values
land in the vault — only on how they're referenced afterwards.

### 3. Move keys from `[env.vars]` to `[env.secrets]`

Edit `workspace.toml`. Before:

```toml
[env.vars]
ANTHROPIC_API_KEY = "sk-ant-..."
```

After:

```toml
[env.secrets]
ANTHROPIC_API_KEY = "vault://ANTHROPIC_API_KEY"
```

### 4. Re-audit

```bash
niwa status --audit-secrets
```

Exit zero confirms every value is either a `vault://` ref or empty.

### 5. Apply

```bash
niwa apply
```

niwa resolves the refs and re-materializes the affected files. When
the next apply detects that an upstream vault value has rotated (the
provider returns a new `VersionToken` for a key a managed file
depends on), the affected file's stored `SourceFingerprint` changes
and stderr carries a `rotated <path>` line per affected file.

## Public-repo guardrail

### What it does

Before resolving, niwa reads the config-source identity from the
provenance marker (`.niwa/.niwa-snapshot.toml`) and pattern-matches
the recorded `host`/`owner`/`repo` against the GitHub URL shape.
When the marker says `host = "github.com"` and the team config has
plaintext values in `*.secrets` tables, apply fails with an error
naming the offending keys.

The detection is marker-read only — no authenticated API call, no
latency cost. It works against the snapshot-model `.niwa/` directory;
no `.git/` is required.

### What it doesn't do

- Detection is GitHub-only in v1. GitLab, Bitbucket, and
  self-hosted Gitea are in a deferred list.
- Non-GitHub hosts (`github.mycorp.com`, `gitlab.com`,
  `bitbucket.org`) do NOT trigger the guardrail.
- When the provenance marker is missing — either because the config
  directory was never sourced from a remote (local-only workspace)
  or because the marker file was hand-deleted — the guardrail
  emits a warning and proceeds. It doesn't block apply on
  workspaces that aren't tracked.
- The guardrail walks `*.secrets` tables only. Plaintext in
  `[env.vars]` is allowed; that's exactly why the vars/secrets
  split exists.

### `--allow-plaintext-secrets`

A strictly one-shot escape hatch:

```bash
niwa apply --allow-plaintext-secrets
```

The flag bypasses the guardrail for a single invocation. It's NOT
persisted to state. Running apply once with the flag, then running
apply again without it, triggers the guardrail a second time. Use
it only for exceptional circumstances where the plaintext value is
known to be safe (a throwaway scratch workspace, a test account
token with no production access).

## CLI reference

| Surface | Purpose |
|---------|---------|
| `niwa apply --allow-missing-secrets` | Downgrade unresolved `vault://` references to empty strings with stderr warnings. Does NOT override `*.required` misses. |
| `niwa apply --allow-plaintext-secrets` | Bypass the public-repo guardrail for one invocation. No state persistence. |
| `niwa status` (default) | Fully offline. Reads `state.json`, reports per-file drift and a shadowed-count summary. No provider calls. |
| `niwa status --audit-secrets` | Classify every `*.secrets` value as plaintext / vault-ref / empty. Exits non-zero when plaintext values AND a vault are present. |
| `niwa status --check-vault` | Re-resolve every `vault://` reference against the configured providers and compare fingerprints to stored state. Does NOT materialize. |
| `vault://key?required=false` | Mark an individual URI as optional. A miss resolves to empty string with no warning or error. |

## v1 scope boundaries

- **Infisical only.** sops + age is deferred to v1.1.
- **GitHub-only guardrail.** GitLab, Bitbucket, self-hosted Gitea
  are deferred.
- **macOS and Linux only.** Windows users should run niwa from WSL.
- **Migration is manual.** There is no `niwa vault init` or
  `niwa vault import` subcommand in v1. The audit command tracks
  progress; the provider CLI does the upload.

## Security model

The vault integration defends a narrow perimeter: the accidents
that happen during normal development. It doesn't defend against
root attackers, compromised provider CLI binaries, or compromised
vault services.

The "never leaks" invariants (R21 through R31 in the PRD) are
enforced at three layers: the type system (`secret.Value` opaque
formatters), the pipeline (`secret.Error` + context-scoped
`Redactor`, `vault.ScrubStderr`), and filesystem permissions
(`0o600` materialization, `.local` + `.gitignore` maintenance).
The complete list covers argv rejection, log/stderr redaction,
CLAUDE.md interpolation refusal, status-content redaction, no
process-env publication, no disk cache, the public-repo guardrail,
and override-visibility diagnostics.

For the full threat model (what's trusted, what's out of scope,
what the invariants defend), see
[PRD-vault-integration §Threat Model](../prds/PRD-vault-integration.md).
For how each invariant is realized in code, see
[DESIGN-vault-integration §Security Considerations](../designs/DESIGN-vault-integration.md).

## Acceptance coverage

See [vault-integration-acceptance-coverage.md](vault-integration-acceptance-coverage.md)
for the full PRD-AC-to-test mapping.
