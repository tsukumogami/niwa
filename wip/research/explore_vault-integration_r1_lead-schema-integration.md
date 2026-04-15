# Lead: Schema integration shape

## Findings

### Current schema recap

Confirmed from `internal/config/config.go` (post-consolidation layout):

- `WorkspaceConfig` holds: `Workspace`, `Sources`, `Groups`, `Repos`,
  `Content` (deprecated alias, migrated into `Claude.Content` at parse), `Claude`,
  `Env`, `Files`, `Instance`, `Channels` (placeholder).
- `ClaudeConfig` carries: `Enabled`, `Plugins`, `Marketplaces`,
  `Hooks`, `Settings`, `Env` (a `ClaudeEnvConfig` with `Promote` + `Vars`),
  `Content`.
- `ClaudeOverride` is the narrower shape used at override positions (repo,
  instance, global). It intentionally omits `Content` and `Marketplaces` so
  those show up as unknown-field warnings when placed under a repo/global
  override. This is load-bearing: it means any vault concept placed under
  `Claude` must decide explicitly whether it's workspace-only or overridable.
- `ClaudeEnvConfig` = `{ Promote []string; Vars map[string]string }`. `Vars`
  values are plain strings today.
- `EnvConfig` = `{ Files []string; Vars map[string]string }`. Used at
  workspace level (`[env]`), per-repo (`[repos.<name>.env]`), per-instance
  (`[instance.env]`), and inside `GlobalOverride.Env`.
- `GlobalOverride` = `{ Claude *ClaudeOverride; Env EnvConfig; Files map[string]string }`.
  Loaded from a second file via `ParseGlobalConfigOverride` (flat `[global]`
  plus per-workspace `[workspaces.<name>]`). Merge precedence (from
  `override.go`): workspace baseline -> `MergeGlobalOverride` applies global on
  top -> `MergeOverrides` then applies per-repo override per-repo; `Env.Vars`
  uses "later wins per key."
- There is no existing vault concept anywhere in `internal/`. The word only
  appears in PRD/design docs.

### Options evaluated

#### Option 1: New top-level `[vault]` table with binding declarations

```toml
[vault]
provider = "infisical"
project   = "tsukumogami"

[vault.keys]
GITHUB_TOKEN = "github-token"
```

Pros: fully discoverable; separate namespace; easy schema to validate; easy
to render in `niwa status`; easy to reject at parse time if provider isn't
compiled in.

Cons: adds a whole new top-level; duplicates information if both team and
personal configs carry `[vault]`; doesn't help with per-repo vault scoping;
introduces a second pathway to "how a key ends up in env" that doesn't use
the existing `Env.Vars` merge pipeline; by declaring a binding table it
implicitly demands that vault be the only thing that can populate those
keys, which collides with the layered-override story (can a personal layer
replace a team vault key with a plaintext override for a debug session?).

#### Option 2: Nested under `[env.vault]` / `[claude.env.vault]`

Same binding shape as Option 1 but placed under the existing env blocks.
Means each scope gets its own vault table.

Pros: rides the existing merge pipeline (Env lives at workspace, repo,
instance, global); team and personal can each declare their own vault
without colliding at a top level; respects the rule "vault materializes
into env."

Cons: forces duplication of provider config across every env scope that
wants to resolve secrets (workspace `[env.vault]`, `[claude.env.vault]`,
`[repos.<foo>.env.vault]`). Vault isn't actually env-specific — a vault
value can legitimately land in a `[files]` mapping (e.g., an SSH key file)
or a `[claude.settings]` value. Scoping vault to env draws the wrong
boundary.

#### Option 3: Per-secret URI references in existing `[env].vars`, plus a single `[vault]` top-level for provider config

```toml
[vault]
# Provider registration. Multiple named vaults allowed.
[vault.providers.team]
kind    = "infisical"
project = "tsukumogami"

[vault.providers.personal]
kind     = "op"        # 1Password
account  = "my.1password.com"
vault_id = "dangazineu-dev"

[env.vars]
GITHUB_TOKEN = "vault://team/github-token"
NPM_TOKEN    = "vault://personal/npm-token"

[claude.env.vars]
ANTHROPIC_API_KEY = "vault://team/anthropic-key"

[repos.niwa.env.vars]
# Per-repo vault ref. Inherits provider registry from workspace.
GITHUB_TOKEN = "vault://personal/github-tsukumogami-pat"

[files]
"vault://personal/ssh-tsukumogami-ed25519" = ".ssh/tsukumogami_ed25519"
```

Pros:

- Minimal schema addition: one new top-level (`[vault]`) for provider
  registry; the reference mechanism reuses every existing string-valued
  slot (`env.vars`, `claude.env.vars`, `files` keys).
- Existing `MergeOverrides`, `MergeGlobalOverride`, `MergeInstanceOverrides`
  pipelines already do last-writer-wins per key. A personal-overlay layer
  that rewrites `GITHUB_TOKEN = "vault://..."` or even a plaintext literal
  for a debug session Just Works — no new merge logic.
- Vault isn't confined to env. Any string value in the schema can be a
  `vault://` reference; resolution happens at materialize time via a single
  interpolation pass.
- `vault://<name>/<key>` mirrors how developers already think about secret
  references from direnv / sops / mise / 1Password's `op://` URIs.
- Public-repo compatibility: only `vault://` URIs appear in the TOML, never
  resolved values. The TOML is safe to commit.
- Per-repo override, instance override, and global overlay all already
  carry `Env` and `Files` — no struct changes needed to `RepoOverride`,
  `InstanceConfig`, or `GlobalOverride` for the reference mechanism. Only
  the top-level `[vault]` registry needs to be addable to `GlobalOverride`
  as an optional field.

Cons:

- Requires a URI parser and an interpolation pass in `materialize.go` /
  `apply.go`.
- "Is this a literal or a reference?" is a runtime decision based on the
  string's prefix. Needs a dedicated escape for literal values that
  legitimately start with `vault://` (unlikely in practice, but call it
  out).
- A typo like `vualt://...` silently becomes a literal. Mitigation: warn
  on any unresolved value that matches `^[a-z]+://` but isn't a recognized
  scheme.

#### Option 4: Implicit resolution by naming convention (no `[vault]` top-level)

Any value in `[env.vars]` whose string begins with `vault://` is auto-resolved
against a provider inferred from heuristics (env var, global config,
presence of a CLI on PATH).

Pros: smallest schema footprint.

Cons: no declaration of which provider/project/workspace a vault URI
resolves against — the resolution context lives entirely outside the TOML,
which means the same `workspace.toml` behaves differently on two machines
without any way to see why. Bad for reproducibility (core tsuku value) and
bad for team onboarding. Rejected.

#### Option 5: Dedicated `[claude.vault]` block only

Vault lives under `[claude]` and only materializes into Claude's
`settings.local.json` env. Workspace `[env]` stays plaintext.

Pros: consistent with the `[claude]` consolidation pattern.

Cons: too narrow. Team secrets routinely need to reach beyond Claude —
test harness env, CI-parity local scripts, `[files]` mappings for SSH keys
and gcloud credentials, `[env.files]` that source vault-resolved `.env`
files. Scoping vault exclusively to Claude forces users back to plaintext
for everything else, which defeats the "dot-niwa can be public" goal.
Rejected.

### Sub-question answers

1. **How does `GlobalOverride` extend?** With Option 3, the primary vehicle
   is unchanged: personal vault secrets ride through `Env.Vars` and
   `Claude.Env.Vars` as `vault://` URI strings, which the existing override
   merge already handles. What `GlobalOverride` does need is a way to
   declare *additional* provider registrations (personal vaults the
   team config doesn't know about). Add one optional field:
   `Vault *VaultRegistry` on `GlobalOverride`, and symmetrically a
   `WorkspaceConfig.Vault VaultRegistry` on the workspace struct. Merge is
   per-provider-name last-writer-wins (personal can replace a team
   `providers.personal` entry, or add a new one). The binding URIs never
   move; only the provider registry does.

2. **Is there a `[claude.vault]` concept?** No. Vault is workspace-level
   (with `GlobalOverride` layering). The `[claude.env.vars]` block already
   accepts any string, so `vault://...` values work there without a
   Claude-specific registry. Keeping a single registry avoids the "which
   provider map is this URI resolved against" ambiguity.

3. **Public-repo compatibility — the line:**
   - **Can be vault-backed (safe in public TOML):** all values in
     `[env.vars]`, `[claude.env.vars]`, `[repos.<name>.env.vars]`,
     `[instance.env.vars]`; any key in `[files]`, `[repos.<name>.files]`,
     `[instance.files]` (keys are sources); `[claude.settings]` values
     (e.g., an API base URL pointing at a vault-resolved org-specific
     endpoint); `[env.files]` source paths when the path itself contains a
     vault reference is *not* supported — env.files takes a filesystem
     path, not a vault ref, by design.
   - **Must stay plaintext in TOML:** `[vault.providers.<name>].kind`,
     `account`, `project`, `vault_id`, and any other provider-locator
     field. Workspace metadata (`name`, `default_branch`), repo URLs,
     group memberships. Anything that identifies a vault — never anything
     that *unlocks* one. Auth tokens for the vault itself live in
     OS keychain / the provider's own auth dance, never in `workspace.toml`
     or the global override TOML.
   - **Must never appear, period:** concrete secret values (API tokens,
     PATs, SSH private keys) — same rule as today, except now there's a
     viable alternative to plaintext.

4. **Interpolation interaction with `{workspace}` / `{repo_name}` templates
   in `[content]`:** content `source` paths are validated against
   `validateContentSource` which already rejects absolute paths and `..`.
   A `vault://` scheme would fail validation (absolute-ish, no relative
   interpretation). Recommendation: keep content templates vault-free.
   Content is about which file to copy, not what value to inject into it.
   A CLAUDE.md that needs a secret should reference an env var at runtime,
   not have the secret inlined at materialize time — otherwise the
   generated CLAUDE.md sitting on disk *is* the leak. This is a firm
   boundary: vault resolves into env and into `[files]` destinations only.

5. **Per-repo `[repos.<name>.env]` vault handling:** inherits the
   workspace-level `[vault.providers.*]` registry. No per-repo registry is
   needed — per-repo only supplies URI strings. If a repo needs a
   provider that workspace doesn't declare, either lift the declaration to
   the `GlobalOverride` or add it to the workspace. Refusing per-repo
   registries keeps the resolution context close to the top of the config.

6. **`[files]` sourced from vault:** yes, supported. A `[files]` key that is
   a `vault://` URI means "fetch the secret, write its bytes to the
   destination file, apply 0600 perms." Explicitly documented behavior, not
   implicit. This is necessary for SSH keys and gcloud credentials that
   some tools require as on-disk files. `[env.files]` (source = a `.env`
   file path) stays filesystem-only.

## Recommendation

**Option 3** — per-value `vault://` URI references in existing string slots,
backed by a single top-level `[vault.providers.*]` registry that layers via
`GlobalOverride`.

### Concrete TOML syntax

```toml
# Workspace-level provider registry.
[vault.providers.team]
kind    = "infisical"
project = "tsukumogami"

# Optional: opt-in strictness. When true, unresolvable vault:// refs
# fail niwa apply. When false, they become empty strings with a warning.
[vault]
strict = true
```

Reference form in any string value:

```
vault://<provider-name>/<path/to/secret>[?field=<field>]
```

- `<provider-name>` must match a key under `[vault.providers.*]`.
- `<path/to/secret>` is provider-defined.
- Optional `?field=` selects a sub-field for structured secrets
  (e.g., 1Password item fields).

### Scenario (a): team declares a vault for shared API tokens

`workspace.toml` (in a public `tsukumogami/dot-niwa`):

```toml
[workspace]
name = "tsukumogami"

[vault.providers.team]
kind    = "infisical"
project = "tsukumogami"

[env.vars]
ANTHROPIC_API_KEY = "vault://team/anthropic-api-key"
OPENAI_API_KEY    = "vault://team/openai-api-key"

[claude.env]
promote = ["ANTHROPIC_API_KEY"]

[claude.env.vars]
# Claude-only secret never promoted to process env.
SENTRY_DSN = "vault://team/claude-sentry-dsn"
```

### Scenario (b): personal overlay declares vaults for two orgs with different PATs

Personal global config at e.g. `dangazineu/dot-niwa/niwa.toml`:

```toml
[global.vault.providers.personal-tsukumogami]
kind     = "op"
account  = "my.1password.com"
vault_id = "dangazineu-tsukumogami"

[global.vault.providers.personal-codespar]
kind     = "op"
account  = "my.1password.com"
vault_id = "dangazineu-codespar"

# Per-workspace scoping: pick the right PAT per workspace.
[workspaces.tsukumogami.env.vars]
GITHUB_TOKEN = "vault://personal-tsukumogami/github-pat"

[workspaces.codespar.env.vars]
GITHUB_TOKEN = "vault://personal-codespar/github-pat"
```

`ResolveGlobalOverride` already picks the right `[workspaces.<name>]` entry;
no merge-logic changes needed. The union of workspace `[vault.providers.*]`
and global `[vault.providers.*]` happens in `MergeGlobalOverride` with
last-writer-wins per provider name.

### Scenario (c): user overrides a team vault value locally for a debug session

Personal config, in the same `niwa.toml`:

```toml
[workspaces.tsukumogami.env.vars]
# Override the team vault ref with a different vault ref (safe to commit).
ANTHROPIC_API_KEY = "vault://personal-tsukumogami/scratch-anthropic-key"

# Or, for a genuinely throwaway session, a plaintext literal.
# NOTE: this file is in the PERSONAL config repo, not the team one.
# It is still committed, so even here vault:// is recommended.
# OPENAI_API_KEY = "sk-debug-literal-do-not-commit"
```

Merge order: workspace `[env.vars]` (`vault://team/...`) -> global overlay
(`vault://personal-tsukumogami/...`) -> per-repo override if any. Last
writer wins per key; the existing pipeline handles it.

### Vault-backed vs plaintext boundary

| Location                              | vault-backed? | Notes                                       |
|---------------------------------------|---------------|---------------------------------------------|
| `[env.vars]` values                   | yes           | resolves into process env                    |
| `[claude.env.vars]` values            | yes           | resolves into settings.local.json env        |
| `[repos.<n>.env.vars]` values         | yes           | inherits provider registry                   |
| `[instance.env.vars]` values          | yes           |                                              |
| `[files]` source keys                 | yes           | writes secret bytes to destination, 0600     |
| `[claude.settings]` values            | yes           | rare but legal (e.g., org-specific URL)      |
| `[env.files]` source paths            | no            | filesystem path only                         |
| `[claude.content.*]` sources          | no            | template path, not a value                   |
| `[vault.providers.*]` fields          | no            | must be literal (locator, not secret)        |
| `[workspace]` metadata                | no            | name, default_branch, content_dir            |
| `[sources].org`, `[repos.*].url`      | no            | repo identifiers                             |
| `[groups]` memberships                | no            | classification metadata                      |
| Anywhere a vault auth token lives     | **forbidden** | keychain / provider auth only                |

Rule of thumb: *identifiers* stay plaintext; *secrets* go through vault.

### Implication for GlobalOverride

Add one optional field to `GlobalOverride`:

```go
type GlobalOverride struct {
    Claude *ClaudeOverride    `toml:"claude,omitempty"`
    Env    EnvConfig           `toml:"env,omitempty"`
    Files  map[string]string   `toml:"files,omitempty"`
    Vault  *VaultRegistry      `toml:"vault,omitempty"` // new
}

type VaultRegistry struct {
    Strict    *bool                       `toml:"strict,omitempty"`
    Providers map[string]VaultProvider    `toml:"providers,omitempty"`
}

type VaultProvider struct {
    Kind string `toml:"kind"`
    // Provider-kind-specific locator fields captured as a free-form map.
    // Parsed and validated by the provider implementation.
    Config map[string]any `toml:",inline"`
}
```

Symmetrically add `Vault VaultRegistry` to `WorkspaceConfig`.

`MergeGlobalOverride` grows one block: per-provider-name last-writer-wins
for `Vault.Providers` and pointer semantics for `Vault.Strict`. No changes
to `Env`, `Files`, or `Claude` merge logic — those already carry vault
URIs as opaque strings.

Personal vault refs for a user thus travel through two channels:

1. **Provider registry** — added to the global overlay once per provider,
   layered onto workspace.
2. **Reference strings** — placed in `Env.Vars`, `Claude.Env.Vars`, or
   `Files` via the existing `GlobalOverride` fields; no schema additions.

## Surprises

- The `ClaudeOverride`-vs-`ClaudeConfig` split already gives us a
  precedent for "this field is workspace-only, not overridable." Vault
  fits the same mold: `VaultRegistry` exists on `WorkspaceConfig` and
  `GlobalOverride`, but *not* on `RepoOverride` or `InstanceConfig`. That
  falls out naturally without runtime validation because of how
  BurntSushi/toml reports unknown fields.
- `[files]` taking a `vault://` key is a stretch of the current mental
  model (keys are "source paths") but is the only clean way to handle
  file-shaped secrets like SSH keys. Worth calling out in the PRD that
  this is a deliberate widening of the `[files]` contract.
- `validateContentSource` already rejects paths with `..` or absolute
  prefixes. A `vault://` scheme accidentally winds up rejected for content
  sources with no extra code, which is the right answer — vault in
  content is disallowed. Nice alignment.
- The personal-overlay's per-workspace scoping
  (`[workspaces.<name>]`) solves sub-question 4 of the parent exploration
  (per-org PAT scoping) *for free* when paired with Option 3. The vault
  architecture doesn't need its own scoping mechanism.

## Open Questions

1. **Literal strings that start with `vault://`.** Probably theoretical,
   but escape syntax (`\vault://...` or `raw:vault://...`) should be
   decided before shipping so we don't have to break users later.
2. **Unknown provider name.** When a URI references a provider that isn't
   registered at any layer, is that a parse-time error or materialize-time
   error? Leaning materialize-time so that partial configs still parse,
   with a hard failure in `niwa apply --strict` (default).
3. **Caching and TTL.** Out of scope for this lead (owned by the
   security-runtime lead) but interacts with schema: is TTL a
   per-reference field (`vault://team/x?ttl=1h`) or a per-provider field?
   Proposal: per-provider only; per-reference TTL is scope creep.
4. **`[files]` vault-sourced perms.** Fixed 0600 for all vault-sourced
   files, or configurable per entry? Fixed is simpler and safer; configurable
   can come later if a tool genuinely requires different perms.
5. **Diff behavior in `niwa status`.** Should `niwa status` display
   `vault://team/github-token (resolved)` or redact the provider name too?
   Probably show the URI as-is (it's already in the committed TOML) but
   never show the resolved value.

## Summary

Recommend Option 3: a single top-level `[vault.providers.*]` registry plus `vault://<provider>/<key>` URI references in any existing string slot — `[env.vars]`, `[claude.env.vars]`, `[repos.*.env.vars]`, `[instance.env.vars]`, and `[files]` source keys. `GlobalOverride` grows one optional `Vault *VaultRegistry` field for personal-overlay provider declarations; all reference strings ride the unchanged `Env`/`Claude.Env`/`Files` merge pipeline, so per-workspace personal PAT scoping works out of the existing `[workspaces.<name>]` layering for free. The vault-backed / plaintext boundary: identifiers stay plaintext, secrets flow through vault, and `[claude.content.*]` / `[env.files]` / `[vault.providers.*]` fields are explicitly vault-free.
