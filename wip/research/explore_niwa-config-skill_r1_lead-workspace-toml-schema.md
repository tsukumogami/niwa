# Lead: What's the full workspace.toml schema, and what do the existing docs cover?

## Findings

### 1. Ground-truth schema lives in `internal/config`, not in any doc

The authoritative parser is `internal/config/config.go` (`Parse`/`Load`), backed by
`BurntSushi/toml`. Top-level `WorkspaceConfig` (config.go:239-254):

```go
type WorkspaceConfig struct {
    Workspace WorkspaceMeta           `toml:"workspace"`
    Sources   []SourceConfig          `toml:"sources"`
    Groups    map[string]GroupConfig  `toml:"groups"`
    Repos     map[string]RepoOverride `toml:"repos"`
    Content   ContentConfig           `toml:"content"`   // deprecated alias, see below
    Claude    ClaudeConfig            `toml:"claude"`
    Env       EnvConfig               `toml:"env"`
    Files     map[string]string       `toml:"files,omitempty"`
    Instance  InstanceConfig          `toml:"instance,omitempty"`
    Root      RootConfig              `toml:"root,omitempty"`
    Vault     *VaultRegistry          `toml:"vault,omitempty"`
}
```

Field-by-field (rank-1 single-repo relevant subset):

- **`[workspace]`** (`WorkspaceMeta`, config.go:276-307): `name` (required, must
  match `^[a-zA-Z0-9._-]+$` per `NamePattern` at config.go:20), `version`,
  `default_branch`, `content_dir`, `setup_dir`, `default_agent` ("claude"/"codex",
  stored as raw string so `internal/config` doesn't import `internal/agent`;
  validated later by `internal/agent.ParseAgent`), `vault_scope` (PRD D-11,
  selects the `[workspaces.<scope>]` entry in the personal overlay),
  `read_env_example` (`*bool`, nil = enabled/opt-out default),
  `env_example_policy` (`*EnvExamplePolicy`, nil = inherit), `env_output`
  (`OutputTargets`, empty = inherit -> `.local.env` dotenv default).
- **`[claude]`** (`ClaudeConfig`, config.go:28-61): `enabled` (`*bool`),
  `plugins` (`*[]string`), `marketplaces` (`MarketplaceConfigs` -- a custom
  `[]MarketplaceConfig` with a hand-written `UnmarshalTOML`, config.go:113-157,
  accepting both a bare-string list and `[[claude.marketplaces]]` tables with
  `source` (required), `auto_update` (bool, default false), `track` (string,
  default "release" for github sources) -- decoding logic at
  `marketplaceConfigFromTable`, config.go:162-195), `hooks` (`HooksConfig =
  map[string][]HookEntry`, config.go:328-339 -- event name key, e.g.
  `pre_tool_use`, `stop`; each `HookEntry` is `{Matcher string
  toml:"matcher,omitempty", Scripts []string toml:"scripts"}`),
  `settings` (`SettingsConfig = map[string]MaybeSecret`, config.go:341-345 --
  values can be vault-backed via `MaybeSecret`'s `TextUnmarshaler`), `env`
  (`ClaudeEnvConfig`, config.go:222-236: `promote []string`,
  `vars EnvVarsTable`, `secrets EnvVarsTable`), `work_summary_hooks` (`*bool`,
  nil = on; off switch for the three shirabe session-work-summary hooks
  auto-injected when the shirabe plugin is installed), `pr_body_hook`
  (`*bool`, nil = on; off switch for the shirabe pr-body PreToolUse hook),
  `content` (`ContentConfig`, canonical location for the CLAUDE.md content
  hierarchy since v0.7; the deprecated top-level `[content]` alias is
  migrated into `[claude.content]` with a warning inside `Parse`,
  config.go:477-492, and using both simultaneously is a hard parse error).
  `RepoOverride.Claude` / `InstanceConfig.Claude` use the narrower
  `ClaudeOverride` type (config.go:69-75: `Enabled`, `Plugins`, `Hooks`,
  `Settings`, `Env` only -- no `Marketplaces`/`Content`, so writing
  `[repos.<name>.claude.content]` or `[repos.<name>.claude.marketplaces]`
  surfaces as an "unknown config field" warning automatically, per the
  doc comment at config.go:63-68).
- **`[env]`** (`EnvConfig`, config.go:366-374): `files []string`,
  `vars EnvVarsTable`, `secrets EnvVarsTable`. `EnvVarsTable`
  (config.go:197-220, custom `UnmarshalTOML` in `internal/config/env_tables.go:34-74`)
  carries `Values map[string]MaybeSecret` (every top-level key that isn't one
  of the three reserved names) plus `Required`, `Recommended`, `Optional`
  (each `map[string]string`, populated from `[env.vars.required]` /
  `.recommended` / `.optional` sub-tables, or the `[env.secrets.*]` /
  `[claude.env.vars.*]` / `[claude.env.secrets.*]` equivalents -- same type
  reused across all four locations). A scalar value written at a reserved
  sub-table key (e.g. `env.vars.required = "oops"`) is rejected by
  `validateReservedEnvKeys` (env_tables.go:109-151), called from `Parse`
  before other validation (config.go:460-462), with an error naming the
  exact path and the sub-table to move to.
- **`[vault]`** (`VaultRegistry`, `internal/config/vault.go:5-31`;
  `*VaultRegistry` field on `WorkspaceConfig`, nil when the file declares no
  vault providers): either `provider` (`*VaultProviderConfig`, the anonymous
  `[vault.provider]` singular shape) or `providers`
  (`map[string]VaultProviderConfig`, the named `[vault.providers.<name>]`
  shape) -- mutually exclusive, enforced by `Validate` (vault.go:92-129,
  returns an error if both are set, or if any provider has empty `kind`, or
  if a named provider's name fails `NamePattern`), plus `team_only
  []string` (keys whose effective value must come from the team layer only).
  `VaultProviderConfig` (vault.go:38-81) has a typed `Kind string
  toml:"kind"` and an untyped `Config map[string]any toml:"-"` catch-all
  for every other field on the same table (captured via a custom
  `UnmarshalTOML`, vault.go:51-81) -- so `internal/config` never hard-codes
  backend-specific fields like Infisical's `project`/`env`; each backend
  validates its own fields when its `Factory.Open` runs (per the doc comment
  at vault.go:41-46). Helper methods: `IsEmpty()` (vault.go:136-141),
  `KnownProviderNames()` (vault.go:146-158).
- **`[files]`** -- `map[string]string` (source -> destination) at the
  workspace top level (config.go:247), applies per-managed-repo with a
  `.local` infix inserted so output stays gitignored (per
  `file-distribution.md`); also present per-repo as `RepoOverride.Files`
  (config.go:384) with the same semantics.
- **`[instance]`** (`InstanceConfig`, config.go:256-263): `claude
  *ClaudeOverride toml:"claude,omitempty"`, `env EnvConfig
  toml:"env,omitempty"`, `files map[string]string
  toml:"files,omitempty"` -- "Uses the same fields and merge semantics as
  RepoOverride but applies to the instance root directory (above all
  repos)" per the doc comment. `InstanceConfig.Files` (via `[instance.files]`)
  materializes verbatim, no `.local` rewrite, unlike the per-repo `[files]`
  table (contrast documented at `RootConfig`'s comment, config.go:265-273).
- **`[root]`** (`RootConfig`, config.go:271-273): only `files
  map[string]string toml:"files,omitempty"`, for the non-git parent
  directory holding the instance subdirectories -- mirrors `[instance.files]`
  but at the workspace-root level; also verbatim (no `.local` infix,
  "workspace root is not a git repository, so the .local rewrite ... has no
  purpose here").
- **`[repos.<name>]`** (`RepoOverride`, config.go:377-397): `url`, `group`,
  `branch`, `scope`, `claude *ClaudeOverride`, `env EnvConfig`,
  `files map[string]string`, `setup_dir *string`, `read_env_example *bool`
  (nil = inherit from `WorkspaceMeta.ReadEnvExample`), `env_example_policy
  *EnvExamplePolicy` (nil = inherit from workspace policy), `env_output
  OutputTargets` (empty = inherit from `WorkspaceMeta.EnvOutput`). Validation
  (config.go:556-565): repo-override names must match `NamePattern`; an
  explicit repo with `group` set but no `url` is rejected ("explicit repos
  require both url and group").
- **`[content]` / `[claude.content]`** (`ContentConfig`, config.go:399-412):
  `workspace ContentEntry`, `groups map[string]ContentEntry`, `repos
  map[string]RepoContentEntry`, `worktree ContentEntry` (optional
  per-worktree content layer, expanded with `{purpose}`/`{branch}`/
  `{repo_name}`/`{worktree_path}` template vars). `ContentEntry`
  (config.go:415-423): `source string`, plus an unexported-from-TOML
  `OverlayDir` field set only by overlay merging. `RepoContentEntry`
  (config.go:426-433): `source`, `subdirs map[string]string`, plus a
  TOML-excluded `OverlaySource`. Content source paths are validated against
  path traversal / absolute paths by `validateContentSource`
  (config.go:598-613) for every position (`workspace`, `worktree`, each
  group, each repo, each repo subdir); subdir keys are separately validated
  by `validateSubdirKey` (config.go:715-727).
- **`EnvExamplePolicy`** (`internal/config/env_example_policy.go:74-78`):
  `VendorToken *Action`, `Entropy *Action`, `Vars map[string]Action`
  (project-scope only -- workspace top level and per-repo positions; the
  personal/global position carries category keys only, per the doc comment).
  `Action` is a typed string (`"warn"`/`"fail"`, env_example_policy.go:9-16)
  with a `TextUnmarshaler` that rejects any other value
  (env_example_policy.go:21-29). Precedence resolved by
  `EffectiveEnvExamplePolicy` (env_example_policy.go:120-156): per-variable
  (repo, then workspace) -> inline `.env.example` annotation -> per-category
  (repo, then workspace, then global) -> default `warn`.
- **`OutputTargets`/`OutputTarget`** (`internal/config/env_output.go:44-60`):
  `OutputTarget{Path string toml:"path", Format OutputFormat
  toml:"format,omitempty"}`; `OutputTargets` is `[]OutputTarget`. Accepts
  a bare string, a list of bare strings, or a list of `{path, format}`
  tables (format inferred from extension unless explicit, per
  workspace-config-sources.md's table).
- **`MaybeSecret`** (`internal/config/maybesecret.go:24-37`): the sum type
  backing `SettingsConfig` values and `EnvVarsTable.Values` -- `Plain string`
  (raw TOML string, including unresolved `vault://` URIs),
  `Secret secret.Value` (populated post-resolve), `Token
  vault.VersionToken`. `IsSecret()` reports true only once the resolver has
  run (maybesecret.go:43-45); parser-produced values are never "secret" even
  if they start with `vault://`. Implements `TextUnmarshaler`/`TextMarshaler`
  so TOML strings decode directly and re-serialization always redacts
  resolved secrets to `***`.

**Rank-1-specific vs rank-2/org-wide-specific**: the Go struct carries no
rank distinction whatsoever -- the identical `WorkspaceConfig` schema
parses regardless of whether the file was discovered via rank 1
(`.niwa/workspace.toml`), rank 2 (`workspace.toml` at repo root, deprecated),
or the removed rank 3 (`niwa.toml` at repo root). Rank is purely a
source-discovery concept implemented in `internal/config/discover.go`
(marker-file precedence scan), not a schema concept. What differs in
practice for a single-repo rank-1 workspace is usage, not schema:
`[[sources]]`/`[groups.*]` are typically trivial or absent (one repo, often
referenced implicitly rather than through org auto-discovery), and
`[workspace].vault_scope` is usually unnecessary because a single-source
workspace scopes the personal overlay automatically off the source org
(documented explicitly in vault-integration.md, see below).

`GlobalOverride`/`GlobalConfigOverride` (config.go:615-673) is the
personal overlay / global config schema -- `niwa.toml`,
`~/.config/niwa/config.toml`, `[global]` / `[workspaces.<name>]` sections --
a distinct, narrower struct (no `URL`/`Branch`/`Group`/`Scope`/
`Claude.Enabled`; adds `EnvExamplePolicy`, `EnvOutput`, `Vault
*VaultRegistry`). This is not part of `workspace.toml` itself but is
directly relevant to the skill's "wire a new secret" use case since a
personal overlay is often where a developer's own vault provider or
per-workspace secret shadow lives. Parsed by `ParseGlobalConfigOverride`
(config.go:653-673), which also validates `Files`/`Env.Files` path safety
(config.go:677-692) and vault shape per-workspace-entry.

Also worth noting: `Parse` (config.go:449-514) hard-rejects
`NIWA_WORKER_SPAWN_COMMAND` as a TOML key anywhere in the file
(`rejectWorkerSpawnCommandKey`, config.go:698-713) -- intentionally
env-var-only, never committable -- a constraint a config-editing skill should
know about so it never proposes adding that key to `workspace.toml`.

### 2. Example configs found

- **`internal/workspace/scaffold.go:12-107`** (`scaffoldTemplate`) is the
  single richest example in the repo: the exact commented-out template
  `niwa init` writes to `.niwa/workspace.toml`. It exercises nearly every
  block: `[workspace]` (active), then commented examples for `[[sources]]`,
  `[groups.public]`/`[groups.private]`, `[repos.my-repo]`,
  `[repos.external-tool]`, `[claude.content.workspace]`, `[claude]`
  (marketplaces/plugins), `[[claude.hooks.pre_tool_use]]`,
  `[claude.settings]`, `[claude.env]`/`[claude.env.vars]`/
  `[claude.env.secrets]` (including the Codex `OPENAI_API_KEY` dual-agent
  example), `[instance.claude.settings]`, `[env]`/`[env.vars]`/
  `[env.secrets]`, `[files]`, `[instance.files]`, `[root.files]`,
  `[vault.provider]` / `[vault.providers.team]` + `[vault.providers.personal]`,
  `[vault].team_only`. Line 45 points readers at
  `docs/designs/DESIGN-workspace-config.md` for "full schema reference" -- a
  path that doesn't actually exist (see Surprises).
- **`scaffoldFromSourceTemplate`** (scaffold.go:149+) is a second, separate
  literal template for the bootstrap-from-source path (`[workspace]`,
  `[[sources]]`, `[groups.<vis-key>]`). A comment explicitly states "Section
  ordering, blank lines, and comments are part of the byte-equality contract
  -- DO NOT reformat this string" -- i.e. this template is test-pinned
  byte-for-byte, a strong signal that any skill generating/editing similarly
  formatted content should be careful about exact formatting where tests
  might assert on it.
- **No standalone `.toml` fixture files** exist anywhere in the repo (`find
  . -iname "workspace.toml"` returned nothing). Every example config used in
  tests is an inline Go string literal -- `grep -rl "[workspace]"
  --include="*.go" .` matched 20+ files across `internal/workspace/*_test.go`,
  `internal/cli/*_test.go`, `test/functional/*_test.go`, `test/live/*_test.go`.
- **`docs/guides/file-distribution.md:42-60`** has a clean, realistic
  single-repo example of `[files]` / `[instance.files]` / `[root.files]`
  together (an MCP config file distributed at every level, with the
  `.local` vs verbatim distinction explained at lines 17-33).
- **`docs/designs/current/DESIGN-workspace-config.md:391-475`** has a "Full
  schema reference" TOML example, but per Surprises below it is stale and
  should not be treated as a current example.

### 3. Doc coverage: `docs/guides/workspace-config-sources.md` (804 lines)

Section-by-section:
- **Header/status banner** (lines 1-19): flags "Implementation status
  (April 2026)" -- parts of the described contract are aspirational pending
  PR #73's apply-pipeline rewrite; user-facing behavior "reflects the
  eventual contract."
- **What you get / Quick start / Slug grammar** (20-102): source-slug
  grammar `[host/]owner/repo[:subpath][@ref]`, rejected forms table.
- **Discovery rules** (104-126): the rank-1/2/3 marker precedence table
  (`.niwa/workspace.toml` -> rank 1, `workspace.toml` at root -> rank 2,
  `niwa.toml` at root -> rank 3), ambiguity/absence error behavior, the rank-3
  `content_dir` requirement (PRD R8).
- **Snapshot model / Provenance marker / Atomic refresh** (128-185):
  `.niwa/` pure-file-tree model, `.niwa-snapshot.toml` fields, two-rename
  atomic swap steps.
- **Drift detection / Same-run effect** (187-254): SHA-endpoint check,
  ETag-conditional tarball fetch, the "same-run effect" guarantee and its
  three exceptions (legacy git-tree conversion, registered-but-unmarked
  workspace = issue #215, worktrees never reconcile).
- **Failure modes table, Migration from standalone dot-niwa, CLI reference,
  Security model** (255-341): fairly complete operational reference.
- **`.env.example` failure policy** (Section env-example-failure-policy, 342-442):
  full `[env_example_policy]` schema -- `vendor_token`/`entropy` category
  keys, `[env_example_policy.vars]` per-variable sub-table (project-scope
  only), the three-level precedence table (User/Project/Per-repo), full
  precedence-resolution order, inline `.env.example` annotation syntax
  (`# niwa: warn|fail`), `--allow-plaintext-secrets` per-run override, the
  separate `read_env_example = false` toggle.
- **Secret-output targets** (Section env-output, 444-507): full `env_output` schema
  -- three authoring forms (bare string, list of strings, list of tables),
  format/extension-inference table, three-level precedence
  (User/Project/Per-repo, most-specific-wins), git-invisibility guarantees.
- **Source layouts (rank-1, rank-2, rank-3)** (Section source-layouts, 509-654):
  the most directly relevant section for this lead -- Section Single-repo
  workspace (516-554) walks the exact rank-1 scenario with an on-disk tree
  diagram; Section Brain repo (556-564) covers rank-1-at-subpath; Section Overlay
  slug rule (569-590) covers the `<owner>/<repo>-overlay` derivation;
  Section Rank-2 deprecation (592-624) covers the deprecation notice and
  `/niwa:migrate-config` remediation; Section Rank-3 removal (626-653) covers
  the removed layout and clarifies rank-1/2/3 discovery applies to
  workspace sources only, not the personal global-config-overlay
  (`niwa.toml`, unrelated file convention).
- **niwa plugin install** (655-699): auto-install of the migrate-config
  Claude plugin on rank-2 detection, opt-out paths, failure handling.
- **Remote control on dispatch** (700-729): `remote_control_on_dispatch`
  global setting, `remoteControlAtStartup` downstream override under
  `[claude.settings]`/`[instance.claude.settings]`.
- **Claude marketplaces** (Section claude-marketplaces, 730-804): full
  `[claude].marketplaces` schema -- legacy bare-string-list form vs. new
  `[[claude.marketplaces]]` table form, `auto_update` (default false,
  documented as a behavior change), `track` (default "release" for github
  sources, with a documented upstream Claude Code limitation around ref
  pinning), automatic dangling-record healing on `create`/`apply`.

Not covered at all in this doc: `[claude.hooks]`/`HooksConfig` schema
(only a passing mention of "hooks" as a noun at lines 248, 521, 542, 720 --
no structural documentation of `HookEntry{matcher, scripts}` or event
names), `[claude.settings]` beyond the `remoteControlAtStartup` example,
`[files]`/`[instance]`/`[root]` schema (line 248 has a one-word mention;
line 720 mentions `[instance.claude.settings]` in passing), and
`[repos.<name>]` override shape generally.

### 4. Doc coverage: `docs/guides/vault-integration.md` (619 lines)

Section-by-section:
- **What you get / Quick start (Infisical)** (1-86): install -> login ->
  declare `[vault.provider]` (`kind`, `project`) -> reference with
  `vault://KEY` in `[env.secrets]` -> apply. Full walkthrough with exact
  commands.
- **Schema anatomy** (88-212): the core structural reference --
  Section Anonymous singular vs named multiple (90-121): `[vault.provider]`
  (URIs `vault://<key>`) vs `[vault.providers.<name>]` (URIs
  `vault://<name>/<key>`), mixing both is a parse error, cross-file provider
  names never resolve. Section `[env.vars]` vs `[env.secrets]` (122-153): the
  split rationale (public-repo guardrail walks `*.secrets` only),
  `secret.Value` opaque-formatter behavior, notes the same split applies
  under `[claude.env.vars]`/`[claude.env.secrets]`. Section Requirement
  sub-tables (154-180): `required`/`recommended`/`optional` behavior table
  (hard error / stderr warning / silent), `--allow-missing-secrets`
  interaction (downgrades misses to empty strings but never downgrades
  `*.required`). Section `[workspace].vault_scope` (182-198): single-source
  workspaces auto-scope by source org; multi-source workspaces require
  explicit `vault_scope`, apply fails otherwise. Section `[vault].team_only`
  (200-212): example and behavior (personal overlay can't shadow, distinct
  error).
- **Binding multiple agents' keys (OpenAI Codex)** (214-242): agent-neutral
  secret table, `ANTHROPIC_API_KEY`/`OPENAI_API_KEY` coexistence example,
  `default_agent = "codex"` interaction, `AGENTS.md` launch-slice boundary
  note.
- **Personal overlay flow** (244-303): example team-config +
  personal-overlay pair (`[workspaces.<org>.env.secrets]` shadowing),
  conflict-resolution table (personal wins except `team_only`; provider-name
  collision is an error, R12), rationale for no "replace the whole team
  provider" path (design D-9, supply-chain reasoning).
- **Multi-org setup** (305-455): when you need it, how it works
  (`~/.config/niwa/provider-auth.toml`, matched by `(kind, project)`),
  step-by-step walkthrough (machine identity creation through apply), full
  credential file format table (this is a different file's schema,
  not `workspace.toml` -- `[[providers]]` with `kind`/`project`/`client_id`/
  `client_secret`/`api_url`), security notes (0o600 enforcement, no token
  cache, rotation guidance).
- **Plaintext-to-vault migration** (457-519): `niwa status --audit-secrets`
  workflow, moving keys from `[env.vars]` to `[env.secrets]` with a
  before/after example, re-audit and apply steps.
- **Public-repo guardrail** (521-570): what it does/doesn't do (GitHub-only
  v1, marker-read-only detection, `*.secrets`-only scope),
  `--allow-plaintext-secrets` one-shot escape hatch.
- **CLI reference, v1 scope boundaries, Security model, Acceptance
  coverage** (572-619): summary tables and links to
  `vault-integration-acceptance-coverage.md`, the PRD, and the design doc.

This doc is `[vault]`/`[env.secrets]`-complete and spot-checked directly
against the code: mutual exclusivity of `provider`/`providers` matches
`vault.go:96-102`; "each provider MUST declare a non-empty kind" matches
vault.go:103-107,121-126; `team_only` behavior description matches the
field's doc comment at vault.go:27-30. No discrepancies found between this
doc and `vault.go`/`env_tables.go`/`maybesecret.go`.

### 5. Schema change velocity (drift risk)

`internal/config/config.go` has 27 commits from its introduction
(2026-03-28) through the most recent schema-touching commit found
(2026-07-16) -- roughly one schema-relevant commit every 4-5 days over ~3.5
months (repo's most recent commit overall is 2026-08-05, so this cadence is
current as of a few weeks ago). Commits touching config.go, in order:
hooks/settings/env distribution, file distribution + `.local`
renaming, env-block in generated settings, post-clone setup-dir convention,
project-scoped plugin installation, hooks-JSON-format fix, explicit
repo entries outside source discovery, instance-root Claude config,
global config overlay layer, `[content]`->`[claude.content]` rename,
vault-backed secrets, workspace-visibility overlay layer, `.env.example`
lowest-priority defaults, cross-session channels/mesh (later fully removed
Jun 6), `niwa init` workspace-dir creation, machine-identity vault sync,
`.env.example` failure policy, configurable secret-output targets, plugin
marketplace lifecycle/update policy, verbatim file distribution to
instance/root levels, Claude Code Remote enabled-by-default on dispatch,
work-summary hooks default-injection, pr-body hook default-injection, Codex
agent support, remote-control keep-alive opt-in. `vault.go` by contrast has
only 2 commits total -- stable since its Apr 16 introduction.

By comparison, `docs/designs/current/DESIGN-workspace-config.md` -- despite
carrying `status: Current` in its YAML frontmatter -- was last touched
2026-05-02 and has only 3 commits total. Its "Full schema reference" TOML
example and "Go type definitions" Go-code block (lines 391-566) are badly
stale:
- Shows `[hooks]` and `[settings]` as top-level workspace sections
  (lines 453-459, and `HooksConfig`/`SettingsConfig` fields directly on
  `WorkspaceConfig` at lines 486-487) -- the real schema nests both under
  `[claude]` (`ClaudeConfig.Hooks`/`ClaudeConfig.Settings`, config.go:35-36).
- Shows `RepoOverride.Claude` as a bare `*bool` (line 513) -- the real type is
  `*ClaudeOverride` (config.go:382).
- Shows a `[channels]`/`ChannelsConfig`/`TelegramChannelConfig` section
  (lines 465-474, 548-564) that was later removed from the codebase
  entirely (commit 69b735f "refactor!: remove pre-pivot mesh," Jun 6) --
  the doc documents a section that no longer exists.
- Has zero mention of `[vault]`, `[claude.marketplaces]`, `env_output`,
  `[env_example_policy]`, `[claude.content]` (still calls it `[content]`
  pre-rename), `[instance]`, or `[root]` -- all either post-date or coincide
  with the doc's last edit and are entirely absent from its "full schema
  reference."
- Its `RepoOverride` example also omits `SetupDir`, `ReadEnvExample`,
  `EnvExamplePolicy`, `EnvOutput` -- all four added later.

`docs/guides/workspace-config-sources.md` was last touched 2026-08-01
(most recent commit in the repo overall) with 7 total commits;
`docs/guides/vault-integration.md` was last touched 2026-07-15 with 6 total
commits -- both guides track much closer to config.go's actual commit
cadence than the design doc does, consistent with their higher accuracy on
spot-check.

## Implications

- A skill that bakes in a static schema copy (a hand-written field list
  duplicated into the skill's own content) will drift within weeks given the
  ~4-5 day commit cadence on `internal/config/config.go`, and the repo
  already has a live, damning example of this exact failure mode:
  `DESIGN-workspace-config.md`'s "Full schema reference" is stale by 2+
  months, actively wrong (documents a removed `[channels]` section, puts
  `[hooks]`/`[settings]` at the wrong nesting level), and omits roughly five
  major features shipped since its last edit. A skill authored the same way
  -- a prose/table restatement of the schema, not sourced from the struct or
  regenerated -- would very likely follow the same trajectory.
- No single existing doc is schema-complete, so doc-linking alone isn't
  sufficient either. `workspace-config-sources.md` and `vault-integration.md`
  each cover a vertical slice thoroughly and accurately (discovery/rank/
  env-policy/env-output/marketplaces/remote-control; vault/secrets
  respectively) but neither documents `[claude.hooks]`, `[claude.settings]`
  structurally, or `[files]`/`[instance]`/`[root]` in depth (that's
  `file-distribution.md`, a third guide not in scope for this lead) -- and
  hooks/settings specifically have no dedicated guide at all, only PRDs
  and the scaffold template. Since the skill's stated use cases are
  explicitly "add a hook," "wire a new secret," "add a Claude plugin," "add
  instance files" -- hooks and instance-files are exactly the gaps.
- The scaffold template (`internal/workspace/scaffold.go:12-107`, the
  literal string `niwa init` writes to disk) is arguably the single most
  reliable "always roughly current" reference in the repo: it's exercised
  by `niwa init` itself and by scaffold tests, so a drift there would likely
  break a test or at minimum look obviously wrong to anyone running `niwa
  init`, unlike a prose doc that can silently rot. It already demonstrates
  every block the skill needs (hooks, secrets, plugins, files, instance,
  vault). A plausible design: have the skill point to
  `internal/config/config.go` (authoritative, richly doc-commented Go
  struct with TOML tags) plus the scaffold template as "the current worked
  example," rather than maintaining a third, independent copy.
- Given `vault.go`'s much lower change rate (2 commits total vs. config.go's
  27) and `vault-integration.md`'s clean spot-check against it, the
  vault-specific portion of a static schema summary would be comparatively
  low-risk to bake in -- the risk is concentrated in the `[claude]` block
  (hooks/settings/marketplaces/env) and the file-distribution blocks
  (`[files]`/`[instance]`/`[root]`), which have changed far more recently
  and frequently.
- Practically, a skill instructing an agent to edit `workspace.toml` in
  place could reduce drift risk further by having the agent read
  `internal/config/config.go` (or a `niwa` introspection/validate command,
  if one exists -- unconfirmed, see Open Questions) at the time of the edit
  rather than trusting any pre-baked list, static or doc-linked.

## Surprises

- `docs/designs/current/DESIGN-workspace-config.md` carries YAML frontmatter
  `status: Current` yet is the most stale, most actively-wrong
  schema doc found in this investigation -- a design doc's `status` field is
  not a reliable freshness signal in this repo.
- The `scaffoldTemplate` string in `scaffold.go` (line 45) points readers at
  `docs/designs/DESIGN-workspace-config.md` for "full schema reference" -- a
  path that doesn't exist; the real file lives at
  `docs/designs/current/DESIGN-workspace-config.md` (missing the `current/`
  segment). A small dangling-link bug, noted but out of scope to fix here.
- Rank (1/2/3) turns out to be purely a source-discovery concept
  (`internal/config/discover.go` marker-precedence scan) with zero
  representation in the `WorkspaceConfig` Go schema -- there's no
  rank-conditional field, no rank tag, nothing. "Rank-1-specific schema
  fields" isn't a real category; the entire schema is rank-agnostic. This
  runs counter to the framing in the original lead question.
- `vault.go` has changed only twice since its Apr 16 introduction while
  `config.go` changed 27 times in the same window -- the vault schema (and,
  consistent with that, `vault-integration.md`'s coverage of it) is far more
  stable than the rest of the schema. Drift risk is not uniform across the
  file; it's concentrated in `[claude]` and the file-distribution blocks.
- No literal `.toml` fixture files exist anywhere in the repo -- every test
  example is an inline Go string literal. This means there is no
  "canonical realistic example file" to point a skill at outside of the
  scaffold template strings themselves.

## Open Questions

- Should the skill teach editing via the scaffold-template mental model
  (find the right commented block, uncomment, fill in) or via direct
  Go-struct field knowledge (read config.go, construct valid TOML from
  first principles)? The scaffold template's block ordering doesn't
  perfectly match canonical TOML table nesting (e.g., it places
  `[claude.content.workspace]` before the `[claude]` table-of-primitives
  block itself), which could mislead an agent trying to splice fragments
  together naively.
- Is there a `niwa config validate` (or similar dry-run/lint) command the
  skill should invoke after editing `workspace.toml`, to close the
  drift-risk loop without needing a fully-current static reference? Not
  investigated in this pass -- worth a follow-up lead, since a
  validate-after-edit step would meaningfully de-risk any authoring
  approach.
- Should `docs/designs/current/DESIGN-workspace-config.md`'s staleness be
  flagged/fixed as a side effect of this initiative, or is it explicitly out
  of scope? It's cited by the scaffold template as the schema reference of
  record, so its inaccuracy has a live blast radius beyond just this skill
  effort.
- `docs/guides/file-distribution.md` should be read in full alongside these
  two, since the skill's "add instance files" use case depends on it
  directly and it's a third doc the skill will likely need to reference or
  absorb.
- Whether any PRD contains a more current hooks-specific schema narrative
  than either guide -- not read in this pass.

## Summary
`workspace.toml`'s ground truth is `internal/config/config.go` (`WorkspaceConfig`, `ClaudeConfig`/`ClaudeOverride`, `EnvConfig`/`EnvVarsTable`, `VaultRegistry`, `InstanceConfig`, `RootConfig`), which changed 27 times in under 4 months -- real, fast drift risk for any skill that bakes in a static schema copy. `workspace-config-sources.md` and `vault-integration.md` are accurate and thorough for the verticals they cover (discovery/rank/env-policy/marketplaces; vault/secrets) but together leave `[claude.hooks]`, `[claude.settings]`, `[files]`/`[instance]`/`[root]` -- exactly what the skill's hook/file/instance use cases need -- undocumented in guide form, covered only by the Go struct and the `scaffoldTemplate` in `internal/workspace/scaffold.go`. The best concrete evidence for drift risk is `docs/designs/current/DESIGN-workspace-config.md` itself: marked `status: Current`, last touched 2+ months before several major schema additions, and its "full schema reference" section is now actively wrong (documents a removed `[channels]` block, misses `[vault]`/marketplaces/`env_output`/`env_example_policy` entirely).
