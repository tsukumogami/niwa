# Lead: What is the minimal-ideal workspace.toml for a brand-new workspace?

## Findings

### Current scaffold (verbatim)

`internal/workspace/scaffold.go` produces this body. The only interpolation is `%s` (the workspace name); everything else is constant. The function also creates `.niwa/claude/` (empty content dir) alongside the file.

```toml
[workspace]
name = "%s"
# version = "0.1.0"
default_branch = "main"
content_dir = "claude"

# --- Sources: GitHub orgs to discover repos from ---
# Uncomment and configure at least one source before running niwa apply.
#
# [[sources]]
# org = "my-org"

# --- Groups: classify repos into directories ---
# [groups.public]
# visibility = "public"
#
# [groups.private]
# visibility = "private"

# --- Per-repo overrides ---
# [repos.my-repo]
# claude = false
#
# --- Explicit repos (from outside source orgs) ---
# [repos.external-tool]
# url = "git@github.com:other-org/tool.git"
# group = "private"

# --- Claude Code configuration, content hierarchy, environment ---
# See docs/designs/DESIGN-workspace-config.md for full schema reference.
# [claude.content.workspace]
# source = "workspace.md"
# [claude]
# marketplaces = ["my-org/my-plugins"]
# plugins = ["my-tool@my-plugins"]
# [[claude.hooks.pre_tool_use]]
# matcher = "Bash"
# scripts = ["hooks/pre_tool_use/gate.sh"]
# [claude.settings]
# [claude.env]
# promote = ["GH_TOKEN"]
# [claude.env.vars]
# EXTRA_FLAG = "settings-only"
# [claude.env.secrets]
# ANTHROPIC_API_KEY = "vault://team/ANTHROPIC_API_KEY"
# --- Instance root overrides (workspace-level Claude Code session) ---
# [instance.claude.settings]
# permissions = "ask"
# [env]
# [env.vars]
# LOG_LEVEL = "debug"
# [env.secrets]
# GITHUB_TOKEN = "vault://team/GITHUB_TOKEN"
# [files]
# "extensions/design.md" = ".claude/shirabe-extensions/"
# [channels]
#
# --- Vault providers (optional) ---
# Pick ONE shape. The anonymous singular shape lets vault:// URIs omit
# the provider name (e.g., vault://API_KEY). The named shape allows
# multiple providers; URIs must name one (e.g., vault://team/API_KEY).
#
# [vault.provider]
# kind = "infisical"
# project_id = "your-project-id"
# env = "prod"
#
# OR:
#
# [vault.providers.team]
# kind = "infisical"
# project_id = "team-project"
# [vault.providers.personal]
# kind = "sops"
# key_path = "keys/personal.age"
#
# [vault]
# team_only = ["CRITICAL_TOKEN"]
```

Defaults applied:
- `name` defaults to `"workspace"` when empty (see `defaultWorkspaceName`).
- `default_branch` and `content_dir` are emitted as hard-coded active values.
- Every other section is commented-out example text.

### Schema survey: workspace.toml field by field

Compiled from `internal/config/config.go` (struct tags and `validate()` rules) and the docs guides.

#### Top-level structure (`WorkspaceConfig`)

| Section | Required | Notes |
|---|---|---|
| `[workspace]` | Yes (name) | Identity + base settings. |
| `[[sources]]` | At least one for `niwa apply` to discover repos (commented examples are accepted but apply produces nothing). | `SourceConfig.Org` is required when the section is present. |
| `[groups.<name>]` | No | Visibility classification; values: `public`, `private`. Validates the group name against `NamePattern` (`[a-zA-Z0-9._-]+`). |
| `[repos.<name>]` | No | Per-repo override; explicit repos require both `url` and `group`. |
| `[claude]` | No | Plugins, marketplaces, hooks, settings, env, content. |
| `[claude.content.*]` | No | CLAUDE.md hierarchy; source paths must be relative and may not contain `..`. |
| `[env]` | No | Workspace-level env files, vars, and secrets. |
| `[instance]` | No | Workspace-instance root overrides (claude/env/files). |
| `[channels]` | No | Mesh / agent channel configuration; presence enables, fields optional. |
| `[vault.provider]` (anonymous) OR `[vault.providers.<name>]` (named) | No | Vault providers; exclusive shapes. |
| `[files]` | No | Files to install at workspace root. |

#### `[workspace]` keys (`WorkspaceMeta`)

| Key | Required? | Source/derivation | Notes |
|---|---|---|---|
| `name` | Required (`validate()` rejects empty). | Derivable from positional `niwa init <name>` arg. | Must match `^[a-zA-Z0-9._-]+$`. |
| `version` | Optional, free-form string. | User. | Scaffold writes `# version = "0.1.0"` commented. |
| `default_branch` | Optional, scaffold emits `"main"`. | Derivable from remote (`refs/remotes/origin/HEAD`) but `main` is the universal default for fresh repos. | Used as the default branch for cloned repos. |
| `content_dir` | Optional, defaults to `"claude"` in scaffold. | Defaultable. | Doc warns this is required for rank-3 subpath sources. |
| `setup_dir` | Optional. | User. | Rarely needed. |
| `vault_scope` | Optional. | User. | Selects which `[workspaces.<scope>]` block in the personal overlay applies. |
| `read_env_example` | Optional bool. | User. | Opt-in/out of `.env.example` pre-pass. |

#### Other section keys (highlights)

- `[[sources]]`: `org` (req), `repos` (whitelist subset), `max_repos` (cap).
- `[groups.<name>]`: `visibility` ("public"/"private"), optional explicit `repos` list.
- `[repos.<name>]`: `url`, `group`, `branch`, `scope`, `claude` (table), `env`, `files`, `setup_dir`, `read_env_example`.
- `[claude]`: `enabled`, `plugins`, `marketplaces`, `hooks`, `settings`, `env`, `content`.
- `[claude.env]`: `promote` (list), `vars` (map + required/recommended/optional sub-tables), `secrets` (map + same sub-tables).
- `[vault.provider]` anon shape: `kind` (req: e.g. `infisical`, `sops`), plus provider-specific fields (`project_id`, `env`, `key_path`, etc.).
- `[channels.mesh]`: presence enables mesh; `roles` is optional. Roles auto-derived from topology otherwise.

### Classification: derivable vs defaultable vs user-must-fill

Given `niwa init <name> --from <org/repo>`, init already has:

- Positional `<name>` (when given).
- `--from` slug, parsed into `(host, org, repo, subpath, ref)`.
- Resolved clone URL via `workspace.ResolveCloneURL`.
- A GitHub API client (`github.NewAPIClient`) capable of querying `default_branch` and visibility.

| Field | Class | Notes |
|---|---|---|
| `workspace.name` | Derivable | Positional arg; falls back to `"workspace"` literal. |
| `workspace.default_branch` | Defaultable | `"main"` is correct for ~all newly-created GitHub repos; only diverges for pre-existing legacy repos. |
| `workspace.content_dir` | Defaultable | `"claude"` (scaffold default already canonical). |
| `workspace.version` | User (cosmetic) | No functional effect. |
| `workspace.vault_scope` | User | Only meaningful when an overlay declares scoped workspaces. |
| `[[sources]] org` | Derivable from `--from` | The natural default is "use my org" — the same org as the source URL. For `dangazineu/commuter` the obvious source is `org = "dangazineu"`. |
| `[groups.<name>] visibility` | Hybrid | When `--from`'s remote visibility is known via GitHub API, defaulting to a single `public` or `private` group matching the source is plausible. Multi-group only makes sense when both visibilities exist. |
| `[repos.<name>]` overrides | User | No good default; this is intent. |
| `[claude.*]` (marketplaces, plugins, hooks) | User | No defaults; teams pick. |
| `[claude.content.workspace] source` | Defaultable to `"workspace.md"` | Convention used by every real config seen. Scaffold currently leaves it commented. |
| `[env]` files/vars/secrets | User | Workspace-specific. |
| `[vault.*]` | User | But: "pre-wire an empty `[vault.provider]` stub" is debatable. |
| `[channels]` | User | Mesh adoption is explicit opt-in. |
| `[instance]` overrides | User | Power feature. |
| `[files]` mappings | User | Workflow-specific. |
| Overlay association | Derivable | `init.go` already calls `config.DeriveOverlayURL(source)` for clone mode; nothing to add to workspace.toml itself (overlay URL is in instance state). |

### Real-world references

`tsukumogami/dot-niwa` (only public live workspace.toml in the workspace) is the closest "reference example for niwa workspace configuration" — the dot-niwa README explicitly calls it that. Pattern points:

1. **Active sections only.** No commented scaffolding remains. Sections are present iff used.
2. **`[workspace]`**: `name` + `content_dir`. Notably omits `default_branch` (relies on niwa default).
3. **`[[sources]]`** has exactly one entry pointing at the same org that hosts the dot-niwa repo. Strong evidence that the source-org default mirroring the `--from` org is the natural starter shape.
4. **`[groups.public]`** with `visibility = "public"`. A single group is the minimum useful shape; private appears in the (private) overlay, not the base.
5. **`[claude]`**: marketplaces + plugins + hooks + settings. All workflow-specific — no obvious default.
6. **`[env]`**: declares required/recommended/optional **descriptions** (not values) using the three-table classification. The values resolve via vault from the overlay. This is the canonical "advertise needs, don't store secrets" pattern.
7. **`[claude.content.*]`**: explicit, granular — one entry per repo + subdir.
8. **No `[vault.*]` in the base config** — providers live exclusively in the private overlay. Strong signal: a fresh scaffold should NOT pre-wire vault entries; advertising env.secrets needs is the team-config concern, supplying providers is the personal/overlay concern.

The PRD `PRD-config-distribution.md` shows a "starter" shape (~7 sections, including a representative `[repos.api]` override) — fuller than dot-niwa's, intended as documentation, not scaffold output.

### Why the current scaffold is unbalanced

- The active body is exactly three lines: `name`, `default_branch`, `content_dir`. **None of these are sources/groups, so `niwa apply` on the scaffold output is a no-op** until the user uncomments and edits `[[sources]]` and `[groups.*]`.
- The commented examples cover 14 sections — they're useful documentation but the user must read, identify, uncomment, and edit. For a "create my workspace" experience this is friction; for a "show me the schema" experience it's helpful.
- The scaffold doesn't use any context init already has. `--from dangazineu/commuter` knows the org; the scaffold can't reflect it because Scaffold receives only `(dir, name)`.

## Proposed Minimal-Ideal Scaffold

Two cases differ in what's derivable, so the proposal is parameterized:

### Case A — `niwa init <name>` (no `--from`)

No remote context. Best the scaffold can do is name + the canonical defaults, with example sections commented for fast discovery. Trim today's commented blob: keep one block per concept, drop verbose alternatives.

```toml
[workspace]
name = "<name>"           # derived from positional arg, else "workspace"
content_dir = "claude"

# Add at least one source before running `niwa apply`:
# [[sources]]
# org = "your-github-org"

# Classify discovered repos by visibility:
# [groups.public]
# visibility = "public"

# CLAUDE.md content hierarchy (file lives under .niwa/claude/):
# [claude.content.workspace]
# source = "workspace.md"

# See https://github.com/tsukumogami/niwa/blob/main/docs/guides/workspace-config-sources.md
# for the full schema (claude.*, env.*, vault.*, files, channels, instance).
```

Rationale per section:
- `[workspace]` active: identity is mandatory; `content_dir = "claude"` is the convention used by every real example.
- `default_branch`: **omit**. niwa's runtime default is `main`; emitting it as an active line invites users to think they need to edit it. dot-niwa omits it. Drop from scaffold.
- `[[sources]]` commented with a placeholder: the single highest-priority next step. Scaffold without `--from` cannot guess the org.
- `[groups.public]` commented, single example: matches dot-niwa shape. Drop the `[groups.private]` line — adding a private group is the user's call after the first apply works.
- `[claude.content.workspace]` commented with `source = "workspace.md"`: matches every real config and pairs naturally with the pre-created empty `.niwa/claude/` directory.
- One link to the full schema reference replacing the 60+ lines of commented examples. The scaffold becomes a runway, the docs are the manual.
- **Do not pre-wire vault**: dot-niwa demonstrates that base configs declare *needs* (env.secrets descriptions) and overlays supply *providers*. A fresh scaffold has neither yet; commented vault examples are deferred to the linked docs.
- **Do not pre-wire `[claude]`/plugins**: dot-niwa's `marketplaces = ["tsukumogami/shirabe"]` is opinionated tooling, not universal. A truly minimal scaffold leaves this empty.

### Case B — `niwa init <name> --from <org/repo>`

The lead notes this is the scaffold-then-stage-then-push case. With the source org known and (optionally) the remote's default-branch/visibility queryable, the scaffold can be more concrete:

```toml
[workspace]
name = "<name>"
content_dir = "claude"

# Source: discover repos from the same org that hosts this config repo.
[[sources]]
org = "<derived-org>"

# Single visibility group matching the source repo's visibility.
[groups.<public|private>]
visibility = "<public|private>"

# CLAUDE.md content hierarchy: drop a workspace.md in .niwa/claude/ to populate.
# [claude.content.workspace]
# source = "workspace.md"

# See https://github.com/tsukumogami/niwa/blob/main/docs/guides/workspace-config-sources.md
# for the full schema (claude.*, env.*, vault.*, files, channels, instance).
```

Rationale per derived value:
- `[[sources]].org` active: 90% case is "manage my org's repos." User edits to add more sources rather than to remove a wrong default.
- `[groups.<vis>]` active: takes one piece of info from the GitHub API (`repos/get` returns `private` bool) and emits a matching group. Defaults to `[groups.public]` when visibility lookup fails (network/auth) — safe because public is the more permissive option.
- `[claude.content.workspace]` stays commented: a freshly-scaffolded workspace has no content files yet; commenting documents the convention without producing a missing-file warning on first apply.

### What we deliberately omit

| Section | Rationale for omission |
|---|---|
| `[claude.hooks.*]` examples | Tooling-specific; one team's `gate-online.sh` is another's noise. Link to docs. |
| `[claude.settings] permissions = "bypass"` | Security-sensitive; do not nudge users toward bypass-by-default. |
| `[claude.env.secrets]` examples with `vault://` | Requires a working vault provider; pre-wiring invites a broken first apply. dot-niwa puts these in active sections only because its overlay provides the vault. |
| `[vault.provider]` / `[vault.providers.<name>]` | Same: no provider, no working URI. The init-time stderr "this workspace declares a vault, bootstrap with X" message in `emitVaultBootstrapPointer` is the right way to surface vault setup, gated on the user actually declaring one. |
| `[channels.mesh]` | Opt-in feature; presence enables. Commenting it adds confusion ("did I enable channels?"). |
| `[instance.*]`, `[files]`, `[repos.*]`, `[env.*]` | Power-user shapes; linked docs cover them. |
| `default_branch = "main"` | Niwa default; emitting it suggests it's tunable per-workspace, which is rarely true. |
| `# version = "0.1.0"` | Unused by validation; cosmetic noise. |

## Implications

1. **`workspace.Scaffold` signature widens.** Today it takes `(dir, name)`. To support derived `[[sources]] org` and `[groups.<vis>]`, it needs to accept a struct with `Org`, `Visibility`, and possibly `DefaultBranch`. Either pass it explicitly from `runInit` (clean) or expose a `ScaffoldOptions` type.
2. **Two scaffold templates instead of one.** The "no `--from`" path keeps the commented-example shape (smaller, with a doc link); the "with `--from`" path emits active values. Could be one template with conditional rendering, but two is clearer.
3. **GitHub API call at init time.** For visibility-aware group emission, init needs `repos/get` against `--from`. Init already has `github.NewAPIClient(resolveGitHubToken())`. Adding one more call is cheap. **Fallback when call fails: emit `[groups.public]`**, do not block init.
4. **Doc link replaces 60+ lines of commented examples.** Trade-off: less discoverability for offline users; more clarity for online users. The link goes to the public guide that is the source of truth anyway.
5. **The "niwa-managed empty remote" target stays empty until `niwa create`.** The scaffold proposal is what lands on the user's disk and gets pushed into their `dangazineu/commuter` config repo. `niwa create` is what then materializes repos. Nothing changes about that flow.
6. **No new fields needed in workspace.toml.** The minimal-ideal scaffold is a subset of the existing schema with smarter defaults.
7. **Pre-created `.niwa/claude/` directory still valuable** but currently the scaffold leaves `[claude.content.workspace]` commented, so the directory sits empty with no link. Proposed scaffold keeps the directory and the commented entry so the convention is documented inline.

## Surprises

1. **Today's scaffold ships `default_branch = "main"` as an active value even though it duplicates niwa's runtime default.** Every real workspace.toml I found omits it, including the reference example dot-niwa. Smells like leftover defensive emission.
2. **The scaffold's commented vault block is duplicated across the workspace doc tree.** Same anon-vs-named explanation appears in `internal/cli/init.go`'s helper, in the docs guide, and in the scaffold template. A doc link consolidates the canonical version.
3. **No real public workspace.toml exists for niwa, koto, shirabe, or tsuku.** Only `dot-niwa` (the workspace meta-repo) has one. That makes dot-niwa's `.niwa/workspace.toml` the only public reference example, and the niwa README's "Configure" section the only other public source-of-truth shape.
4. **Init already runs `config.DeriveOverlayURL(source)` and probes for the convention overlay before the user even sees the scaffolded file.** The overlay association is implicit and not represented in workspace.toml at all — it's instance state. So the scaffold doesn't need any overlay-related content.
5. **`[claude.env.secrets.required]` is a *description map*, not a value map.** dot-niwa uses it to advertise needed secrets ("ANTHROPIC_API_KEY = description") without committing the addresses. This is the right pattern for a public base config — but only when the user actually has secrets to declare, so it's not scaffold material.
6. **Init's R10/R16-R20 path auto-installs the niwa Claude plugin when the source is a rank-2 layout** (whole-repo). For the "init new empty source" flow this never fires — the source repo `dangazineu/commuter` will be a rank-1 (`.niwa/` subpath) layout when pushed back. So plugin auto-install is irrelevant to the scaffold question.

## Open Questions

1. **Should the scaffold emit `[[sources]] org = "<org>"` as an active line or as a commented `# org = "<org>"` placeholder?** Active is more useful (works immediately); commented avoids surprising a user whose intent is multi-org. Lean active for `--from` mode — the org came from the user's own argument, not a guess.
2. **For `niwa init <name>` (no `--from`), should we read `git config remote.origin.url` of `cwd` if it exists?** When the user runs init inside an existing clone, deriving the org is feasible. But this is a corner case; the scoping doc says "empty GitHub remote first, niwa second," so cwd is likely outside any git repo.
3. **Should we emit a commented `[claude.content.workspace] source = "workspace.md"` and *also* create an empty `.niwa/claude/workspace.md` stub?** That makes the convention discoverable by ls. But empty files trigger "did you forget to fill this in?" friction. Proposal omits the stub file; just keep the empty directory.
4. **What's the "right" GitHub API call for visibility?** `repos/get` returns `private` bool. But it requires auth for private repos — failure mode for unauthed init should be "assume public, link to docs."
5. **Should the scaffold be aware of "this is the empty-source bootstrap flow" and emit a different shape than the `--from <existing-config-repo>` flow?** They look identical at the `Scaffold` API boundary but diverge in intent: the empty-source case is "I'm authoring," the cloned case shouldn't scaffold at all (it materializes from the source). Re-reading `init.go`, the cloned case takes `modeClone` and runs `MaterializeFromSource`, not `Scaffold`. So the proposal is **only** about `modeScaffold` and `modeNamed` paths. The empty-source bootstrap flow described in the exploration scope likely needs a *new* mode where init scaffolds locally and then arms a stage-and-push step — out of scope for this lead but worth flagging.
6. **Does pre-creating `.niwa/claude/` help or hurt the empty-source push?** Pushing an empty directory requires a placeholder (`.gitkeep`). Today's scaffold creates the dir but no placeholder, so `git add .niwa/` will silently drop it. Either add a `.gitkeep` or document that content files come later. The minimal-ideal scaffold doesn't need to resolve this — the question is about the push flow, not the scaffold contents — but if the empty-source flow auto-stages, a `.gitkeep` matters.

## Summary

Today's `workspace.Scaffold` produces a three-line active config (`name`, `default_branch = "main"`, `content_dir = "claude"`) plus ~60 lines of commented examples covering every schema section; the only real-world public reference (`tsukumogami/dot-niwa/.niwa/workspace.toml`) confirms `default_branch` is redundant and that the natural minimum is just `name` + `content_dir` plus an active `[[sources]]` and one `[groups.*]` block. For the `niwa init <name> --from <org/repo>` flow, init already has every input needed (org from the slug, visibility from one GitHub API call) to emit active `[[sources]] org = "<derived>"` and `[groups.<vis>]` lines, replacing today's bulky commented blob with a short scaffold plus a single doc link. Vault, plugins, marketplaces, hooks, instance overrides, and channels should not be pre-wired — they require user intent and (for vault) a working provider; the dot-niwa pattern of "advertise needs in the base, supply providers in the overlay" is the right model and only kicks in once the user adds something to advertise.

Summary (3 sentences for caller):

Today's scaffold writes a three-line active `[workspace]` block (`name`, the redundant `default_branch = "main"`, `content_dir = "claude"`) plus ~60 lines of commented examples covering every section, but the only public reference workspace.toml in the ecosystem (`tsukumogami/dot-niwa`) shows that the real minimum is `name` + `content_dir` plus one active `[[sources]]` and one `[groups.<vis>]` block. For `niwa init <name> --from <org/repo>`, init already has the source org and can cheaply query the remote's visibility, so the proposed minimal-ideal scaffold emits those two sections active (derived from inputs niwa already has), drops `default_branch`, keeps a single commented `[claude.content.workspace]` line as a convention hint, and replaces the rest of the commented examples with one link to the schema docs. Vault, plugins, marketplaces, hooks, instance overrides, and channels should not be pre-wired — they require user intent or working infrastructure, and the dot-niwa "advertise needs in the base, supply providers in the overlay" pattern only applies once the user has needs to advertise.
