---
status: Proposed
problem: |
  niwa needs a declarative TOML schema to replace a 700-line imperative bash
  installer that hardcodes workspace structure, CLAUDE.md hierarchy, hooks,
  settings, and environment distribution for a single organization. The schema
  must generalize these operations for any multi-repo workspace with layered
  AI context, supporting multi-instance workspaces and per-host overrides.
decision: |
  A workspace.toml file with nested group/repo structure, convention-driven
  content file placement, instance-centric state (JSON per instance, minimal
  global registry), workspace-level hooks/settings/env with per-repo overrides,
  and separate per-host config files for secrets and machine-specific values.
rationale: |
  Structural nesting makes group membership correct by construction. Convention-
  driven content placement eliminates redundant target path declarations while
  keeping the mapping transparent. Instance-centric state puts debuggable info
  where the workspace lives. Separate host config files keep secrets outside the
  workspace tree and survive workspace reset/recreation.
---

# DESIGN: Workspace config format

## Status

Proposed

## Context and Problem Statement

niwa needs a declarative configuration format that expresses the workspace structure currently wired by an imperative 700-line bash installer. The installer performs 27 operations across repo cloning, CLAUDE.md hierarchy generation, per-repo hooks and settings distribution, environment file merging, and plugin registration. All of this is hardcoded for a single organization.

The config must generalize these operations into a TOML schema that any developer can use to define a multi-repo workspace with layered AI context. It must support multi-instance workspaces from the same definition (e.g., tsuku/, tsuku-2/), template variable substitution in content files, and per-host overrides for channel and bot configuration.

## Decision Drivers

- **Parseable by Go TOML libraries**: schema must work with BurntSushi/toml or pelletier/go-toml
- **Content by reference, not inline**: CLAUDE.md content lives in separate files, config points to them
- **Multi-instance required**: same config, multiple workspace instances, isolated state
- **Three-level hierarchy**: workspace > group > repo context inheritance for CLAUDE.md
- **Phased delivery**: v0.1 covers core lifecycle (repos, groups, content), later phases add hooks, env, channels
- **Convention over configuration**: sensible defaults reduce boilerplate for common cases
- **Prior art alignment**: TOML matches tsuku recipe format; patterns from Google repo tool and Nx inform the design

## Considered Options

### Decision 1: Repo and group schema structure

The config must declare which repos to clone and how to organize them into groups. Groups serve double duty: they determine the directory layout on disk (group name = directory name) and provide a level in the CLAUDE.md content hierarchy. The schema needs to handle the common case (6 repos, 2 groups) concisely while scaling to larger workspaces.

Key assumptions: most repos share a single GitHub org and need few per-repo overrides. Group names map 1:1 to directory names. Two groups (public/private) is common but the schema shouldn't limit the count.

#### Chosen: Nested groups with structural repo membership

Repos are declared inside their group using TOML's nested table syntax. A repo physically inside `[groups.public.repos.tsuku]` can't reference a nonexistent group, eliminating an entire class of validation errors. The config structure mirrors the filesystem layout (`groups.public.repos.tsuku` -> `public/tsuku/`), reducing cognitive load.

```toml
[workspace]
name = "tsuku"
org = "tsukumogami"

[groups.public]
visibility = "public"

[groups.public.repos.tsuku]
[groups.public.repos.koto]
[groups.public.repos.niwa]
[groups.public.repos.shirabe]

[groups.public.repos.".github"]
claude = false

[groups.private]
visibility = "private"

[groups.private.repos.vision]
scope = "strategic"

[groups.private.repos.tools]
```

Repos with no overrides are single-line headers. URLs default to `https://github.com/{org}/{name}.git`. The `.github` repo uses TOML quoted keys (valid but unusual) and `claude = false` to skip Claude Code configuration.

Go types map directly: `map[string]GroupConfig` where each group has `Repos map[string]RepoConfig`.

#### Alternatives considered

**Flat `[[repos]]` with group attribute**: All repos in a top-level array, group as a string field. Rejected because stringly-typed group references require validation and produce no visual clustering by group.

**Separate `[[groups]]` and `[[repos]]` arrays**: Groups and repos as independent arrays linked by name reference. Rejected as worst of both worlds: boilerplate of explicit group definitions without the structural safety of nesting.

**Hybrid `[groups]` metadata + `[[repos]]` references**: Groups defined as tables, repos as a flat array referencing them. Rejected because it's still stringly-typed with mixed table paradigms adding inconsistency.

### Decision 2: CLAUDE.md content hierarchy declaration

This is the core differentiator. Claude Code discovers context by walking up parent directories, loading CLAUDE.md from each level. niwa must express which content files go at which level: workspace root, group directory, repo directory, and optionally subdirectories within repos.

The current installer copies source files (with an underscore naming convention to avoid Claude's auto-discovery) to target locations, performing `$WORKSPACE` substitution via sed. Content files are free-form markdown with embedded structured properties like `Repo Visibility: Public` and `Default Scope: Tactical`.

Key assumptions: most workspaces have fewer than 20 repos. Four built-in template variables (`{workspace}`, `{workspace_name}`, `{repo_name}`, `{group_name}`) cover real substitution needs. Users prefer 1:1 source-to-output file mapping over generated content.

#### Chosen: Hybrid with convention-driven placement

Content files are referenced by source path. Target paths are derived from convention, not declared. A `content_dir` field points to the directory containing all content source files.

```toml
[workspace]
name = "tsukumogami"
content_dir = "claude"

[content.workspace]
source = "workspace.md"

[content.groups.public]
source = "public.md"

[content.groups.private]
source = "private.md"

[content.repos.tsuku]
source = "repos/tsuku.md"

  [content.repos.tsuku.subdirs]
  recipes = "repos/tsuku-recipes.md"
  website = "repos/tsuku-website.md"
  telemetry = "repos/tsuku-telemetry.md"

[content.repos.koto]
source = "repos/koto.md"
```

Target paths follow a fixed convention based on whether the directory is a git repository:

- **Non-git directories** (workspace root, group directories) get `CLAUDE.md` — these directories are managed by niwa, not checked into any repo, so there's no gitignore concern:
  - Workspace content -> `$INSTANCE/CLAUDE.md`
  - Group content -> `$INSTANCE/{group}/CLAUDE.md`

- **Git directories** (cloned repos) get `CLAUDE.local.md` — the `.local` suffix keeps generated content out of the repo's version control:
  - Repo content -> `$INSTANCE/{group}/{repo}/CLAUDE.local.md`
  - Subdirectory content -> `$INSTANCE/{group}/{repo}/{subdir}/CLAUDE.local.md`

niwa warns (but does not auto-modify) if a repo's `.gitignore` lacks a `*.local*` pattern when writing `CLAUDE.local.md` files. The user is responsible for adding the pattern to their repo.

When `content_dir` is set and a repo has no explicit `[content.repos.X]` entry, niwa checks for `{content_dir}/repos/{repo_name}.md` and uses it automatically if found. This convention-over-configuration path means a minimal config can omit most content entries.

Template variables are expanded during `niwa apply`:

| Variable | Value |
|----------|-------|
| `{workspace}` | Absolute path to workspace instance root |
| `{workspace_name}` | `[workspace].name` |
| `{repo_name}` | Repository name |
| `{group_name}` | Group the repo belongs to |

#### Alternatives considered

**Explicit file mapping at every level**: Every source-to-target mapping declared in config. Rejected because target paths are derivable from group/repo structure, making explicit targets redundant and verbose.

**Convention-based auto-generation from properties**: Generate CLAUDE.md content from structured config fields (visibility, scope). Rejected because group-level content contains carefully crafted prose that can't be generated from properties without losing nuance.

**Template model with composition**: Define content templates applied to repos via pattern matching, assembled with per-repo content. Rejected because the composition complexity (template ordering, variable catalogs, template proliferation) isn't justified for ~20 lines of shared boilerplate.

### Decision 3: Multi-instance workspace state model

niwa supports multiple instances from the same workspace config. A developer might have tsuku/ (main), tsuku-2/ (hotfix), tsuku-3/ (PR review). Each shares the workspace.toml definition but has independent repo checkouts. The state model must track what niwa manages per instance and provide a global registry for workspace discovery.

Key assumptions: workspace roots have fewer than 20 instance subdirectories. JSON is appropriate for instance state (richer than TOML, not user-edited). The `root` path in instance state is sufficient for context-aware commands.

#### Chosen: Instance-centric state with minimal registry

Each instance is self-contained with a `.niwa/instance.json` state file. The global registry at `~/.config/niwa/config.toml` is a minimal name-to-root-path index. No root-level state directory.

**Registry (`~/.config/niwa/config.toml`):**
```toml
[global]
clone_protocol = "ssh"

[registry.tsuku]
source = "tsukumogami/niwa-tsuku-config"
root = "/home/user/dev/tsuku-root"
```

**Instance state (`.niwa/instance.json`):**
```json
{
  "schema_version": 1,
  "config_name": "tsuku",
  "instance_name": "tsuku-2",
  "instance_number": 2,
  "root": "/home/user/dev/tsuku-root",
  "created": "2026-03-25T10:00:00Z",
  "last_applied": "2026-03-25T10:05:00Z",
  "managed_files": [
    {"path": "CLAUDE.md", "hash": "sha256:abc123", "generated": "2026-03-25T10:00:00Z"}
  ],
  "repos": {
    "public/tsuku": {"url": "git@github.com:tsukumogami/tsuku.git", "cloned": true}
  }
}
```

Context-aware commands walk up to `.niwa/instance.json` (like git walks to `.git/`). Instance enumeration at the workspace root scans immediate subdirectories for `.niwa/` markers. New instance numbering uses max(existing) + 1. Detached workspaces use the same `.niwa/instance.json` with `config_name: null` and `detached: true`.

#### Alternatives considered

**Registry-centric**: All instance state in the global registry. Rejected because it violates the debuggability principle (state far from workspace) and creates a single-file bottleneck as instances grow.

**Hybrid with root-level index**: Registry + root `.niwa/instances.json` + per-instance state. Rejected because the root-level index adds a concept and a file to sync without meaningful benefit at expected scale (single-digit instances per root).

**No registry**: Workspaces discovered by filesystem scanning only. Rejected because it contradicts the requirement for `niwa init <name>` to resolve configs from a registry.

### Decision 4: Hooks, settings, and environment distribution

The config needs to express how Claude Code hooks, settings, and environment variables are distributed to repos. The current system is uniform: every repo gets identical hooks and settings. Per-repo env overrides exist in the schema but aren't used yet.

Key assumptions: secrets stay in `.env` files referenced by path, never inline in TOML. Hook extend semantics (append, not replace) match expected usage. The uniform distribution pattern will remain the 90% case.

#### Chosen: Workspace-level sections with per-repo overrides

Three top-level sections (`[hooks]`, `[settings]`, `[env]`) define workspace defaults. Per-repo overrides are optional and follow overlay semantics (repo values win for env vars, extend for hooks).

```toml
[hooks]
pre_tool_use = ["hooks/gate-online.sh"]
stop = ["hooks/workflow-continue.sh"]

[settings]
permissions = "bypass"

[env]
files = ["env/workspace.env"]
vars = { LOG_LEVEL = "debug" }
```

Per-repo overrides nest under the repo's group path:
```toml
[groups.private.repos.vision.settings]
permissions = "ask"
```

Secrets stay in `.env` files, never in TOML. niwa generates `settings.local.json` and `.local.env` per repo during `niwa apply`. The schema is defined in v0.1 but generation logic ships in v0.2.

#### Alternatives considered

**Unified `[claude]` section**: Hooks, settings, and env grouped under one key. Rejected because it mixes concerns (file references, key-value pairs, enums) and the `[claude]` namespace risks collision with Claude Code's own config.

**Profile-based**: Named profiles that repos reference. Rejected because zero repos need different profiles today, and per-repo overrides (Option A) are more precise than swapping entire profiles.

**Workspace-only with no per-repo overrides**: Simplest schema but rejected because v0.1 schema must accommodate v0.2 per-repo override functionality.

### Decision 5: Per-host overrides and channel configuration

The Telegram bot integration assigns different bots per host and per workspace instance. Bot tokens are secrets that can't live in the committed workspace.toml. Beyond Telegram, per-host config is needed for different API keys, directory preferences, and permission modes across machines.

Key assumptions: `hostname -s` is stable enough for host identification. XDG-style `~/.config/niwa/` is acceptable. The three-layer model (workspace.toml -> host config -> instance state) won't be too many layers for users. This ships in v0.2/v0.3.

#### Chosen: Separate host config file

Per-host overrides live in `~/.config/niwa/hosts/<hostname>.toml`, completely outside the workspace tree. Bot tokens and machine-specific secrets live here, never in workspace.toml.

**Common case (single workspace per host):**
```toml
# ~/.config/niwa/hosts/ryzen9.toml
[channels.telegram.bots]
"1" = "8758431361:AAHHsx2I9..."
"2" = "8667513242:AAFc2Q9Av..."
"3" = "8790426367:AAFlmE-CF..."

[env]
GH_TOKEN = "ghp_xxxx"
```

**Complex case (multiple workspaces per host):**
```toml
# ~/.config/niwa/hosts/ryzen9.toml

# Host-level defaults (apply to workspaces without a specific section)
[env]
ANTHROPIC_API_KEY = "sk-ant-shared..."

# Per-workspace overrides
[workspaces.tsuku.channels.telegram.bots]
"1" = "tsuku-bot-1-token"
"2" = "tsuku-bot-2-token"
"3" = "tsuku-bot-3-token"
"4" = "tsuku-bot-4-token"

[workspaces.tsuku.env]
GH_TOKEN = "ghp_tsukumogami"

[workspaces.my-project.channels.telegram.bots]
"1" = "myproj-bot-1-token"

[workspaces.my-project.env]
GH_TOKEN = "ghp_other_org"
```

The workspace.toml declares non-secret channel config (access rules, plugin references). The host file overlays machine-specific values. Optional `[workspaces.<name>]` sections override host-level defaults for specific workspaces, matched by the `[workspace] name` field from workspace.toml. Bot assignment maps instance suffix to bot key (tsuku-2 gets bot "2"). When a workspace section declares bots, it fully replaces (not merges with) host-level bot defaults.

The three-layer resolution order is: workspace.toml (shared) -> host config host-level (per-machine defaults) -> host config workspace section (per-workspace overrides). Host identity defaults to `hostname -s` with an `$NIWA_HOST` override for edge cases. Host config survives workspace reset/recreation since it lives outside the workspace tree.

#### Alternatives considered

**In-workspace `workspace.toml.local`**: Gitignored file next to workspace.toml. Rejected because it's destroyed on workspace reset (frequent operation) and puts secrets inside the workspace tree.

**Environment variable references with `${VAR}` syntax**: Placeholders in workspace.toml resolved at runtime. Rejected because it can't express multi-bot-per-host routing and pushes structure into naming conventions.

**Inline host routing in workspace.toml**: All bot tokens for all hosts declared in the committed config. Rejected outright because it puts secrets in a committed file.

**Separate host x workspace files**: Per-workspace override files in a subdirectory (`~/.config/niwa/hosts/ryzen9/tsuku.toml`). Rejected because it scatters config across multiple files (4 files for 3 workspaces) and having both a file and directory at the same path level is confusing.

**Bot pool partitioning**: Declare the full bot pool in host config, partition by range in workspace.toml. Rejected because it only solves Telegram, can't handle per-workspace env vars, and requires fragile cross-workspace range coordination.

## Decision Outcome

### Summary

niwa's workspace config is a TOML file (`workspace.toml`) that lives at the workspace root alongside a content directory. The config declares repos nested inside named groups, where each group name doubles as the directory name on disk. Content files for the CLAUDE.md hierarchy are referenced by source path with target locations derived from convention: workspace content goes to the instance root, group content to the group directory, and repo content to `CLAUDE.local.md` inside each repo.

Each workspace instance gets a `.niwa/instance.json` state file tracking managed files (with content hashes for drift detection), cloned repos, and creation metadata. A minimal global registry at `~/.config/niwa/config.toml` maps workspace names to root paths and optional remote sources. Instance enumeration works by scanning the root directory for `.niwa/` markers.

Hooks, settings, and environment variables are declared at workspace level with optional per-repo overrides. Secrets stay in `.env` files referenced by path. niwa generates `settings.local.json`, copies hooks, and merges env files into each repo during `niwa apply`.

Per-host overrides (bot tokens, machine-specific API keys, permission modes) live in `~/.config/niwa/hosts/<hostname>.toml`, outside the workspace tree. The host config supports optional `[workspaces.<name>]` sections for host x workspace overrides (e.g., different bot pools or API keys per workspace on the same machine). The three-layer resolution is: workspace.toml (shared) -> host config host-level defaults (per-machine) -> host config workspace section (per-workspace on this machine).

### Rationale

The five decisions reinforce each other through consistent conventions. Group names define directory structure (Decision 1), which determines where content files land (Decision 2), which is tracked by instance state (Decision 3). Hooks and settings distribute uniformly by default (Decision 4) but can be overridden per-host (Decision 5) without touching the shared config.

The main trade-off is explicitness over brevity. Content files are referenced individually rather than auto-generated, which means more lines in the config but no hidden generation logic. This aligns with the convention-over-configuration driver: the convention is where files land, not whether they exist.

The phased delivery constraint shaped every decision. The v0.1 schema includes `[hooks]`, `[settings]`, `[env]`, and `[channels]` sections that parse but don't generate output until v0.2/v0.3. This lets the schema stabilize before the full implementation lands.

## Solution Architecture

### Config file hierarchy

```
workspace-root/
  workspace.toml              # Shared config (committed to git)
  claude/                     # Content source directory
    workspace.md              # -> $INSTANCE/CLAUDE.md
    public.md                 # -> $INSTANCE/public/CLAUDE.md
    private.md                # -> $INSTANCE/private/CLAUDE.md
    repos/
      tsuku.md                # -> $INSTANCE/public/tsuku/CLAUDE.local.md
      tsuku-recipes.md        # -> $INSTANCE/public/tsuku/recipes/CLAUDE.local.md
      koto.md                 # -> $INSTANCE/public/koto/CLAUDE.local.md
      ...
  tsuku/                      # Instance 1
    .niwa/instance.json
    public/
      tsuku/
      koto/
    private/
      vision/
  tsuku-2/                    # Instance 2
    .niwa/instance.json
    ...

~/.config/niwa/
  config.toml                 # Global registry
  hosts/
    ryzen9.toml               # Per-host overrides
    laptop.toml
```

### Full schema reference

```toml
# workspace.toml

[workspace]
name = "tsuku"                        # Workspace name (used for registry, instance naming)
org = "tsukumogami"                   # Default GitHub org for URL shorthand
default_branch = "main"              # Default branch for all repos
content_dir = "claude"               # Directory containing content source files

# --- Groups and repos ---

[groups.public]
visibility = "public"

[groups.public.repos.tsuku]
[groups.public.repos.koto]
[groups.public.repos.niwa]
[groups.public.repos.shirabe]

[groups.public.repos.".github"]
claude = false                        # Skip Claude Code configuration

[groups.private]
visibility = "private"

[groups.private.repos.vision]
scope = "strategic"

[groups.private.repos.tools]

# --- Content hierarchy ---

[content.workspace]
source = "workspace.md"

[content.groups.public]
source = "public.md"

[content.groups.private]
source = "private.md"

[content.repos.tsuku]
source = "repos/tsuku.md"

  [content.repos.tsuku.subdirs]
  recipes = "repos/tsuku-recipes.md"
  website = "repos/tsuku-website.md"
  telemetry = "repos/tsuku-telemetry.md"

# --- Hooks and settings (v0.2) ---

[hooks]
pre_tool_use = ["hooks/gate-online.sh"]
stop = ["hooks/workflow-continue.sh"]

[settings]
permissions = "bypass"

# --- Environment (v0.2) ---

[env]
files = ["env/workspace.env"]

# --- Channels (v0.3) ---

[channels.telegram]
plugin = "telegram@claude-plugins-official"

[channels.telegram.access]
allow_from = ["7902893668"]

[channels.telegram.access.groups."-1003723666197"]
require_mention = true
```

### Go type definitions

```go
type WorkspaceConfig struct {
    Workspace WorkspaceMeta              `toml:"workspace"`
    Groups    map[string]GroupConfig     `toml:"groups"`
    Content   ContentConfig             `toml:"content"`
    Hooks     HooksConfig              `toml:"hooks"`
    Settings  SettingsConfig           `toml:"settings"`
    Env       EnvConfig                `toml:"env"`
    Channels  ChannelsConfig           `toml:"channels"`
}

type WorkspaceMeta struct {
    Name          string `toml:"name"`
    Org           string `toml:"org,omitempty"`
    DefaultBranch string `toml:"default_branch,omitempty"`
    ContentDir    string `toml:"content_dir,omitempty"`
}

type GroupConfig struct {
    Visibility string                   `toml:"visibility,omitempty"`
    Repos      map[string]RepoConfig   `toml:"repos"`
}

type RepoConfig struct {
    URL      string         `toml:"url,omitempty"`
    Branch   string         `toml:"branch,omitempty"`
    Scope    string         `toml:"scope,omitempty"`
    Claude   *bool          `toml:"claude,omitempty"`
    Hooks    *HooksConfig   `toml:"hooks,omitempty"`
    Settings *SettingsConfig `toml:"settings,omitempty"`
    Env      *EnvConfig     `toml:"env,omitempty"`
}

type ContentConfig struct {
    Workspace ContentEntry                    `toml:"workspace"`
    Groups    map[string]ContentEntry         `toml:"groups"`
    Repos     map[string]RepoContentConfig    `toml:"repos"`
}

type ContentEntry struct {
    Source string `toml:"source"`
}

type RepoContentConfig struct {
    Source  string            `toml:"source,omitempty"`
    Subdirs map[string]string `toml:"subdirs,omitempty"`
}

type HooksConfig struct {
    PreToolUse []string `toml:"pre_tool_use,omitempty"`
    Stop       []string `toml:"stop,omitempty"`
}

type SettingsConfig struct {
    Permissions string `toml:"permissions,omitempty"`
}

type EnvConfig struct {
    Files []string          `toml:"files,omitempty"`
    Vars  map[string]string `toml:"vars,omitempty"`
}

type ChannelsConfig struct {
    Telegram TelegramChannelConfig `toml:"telegram"`
}

type TelegramChannelConfig struct {
    Plugin string                       `toml:"plugin,omitempty"`
    Access TelegramAccessConfig         `toml:"access"`
}

type TelegramAccessConfig struct {
    AllowFrom []string                           `toml:"allow_from,omitempty"`
    Groups    map[string]TelegramGroupConfig     `toml:"groups,omitempty"`
}

type TelegramGroupConfig struct {
    RequireMention bool `toml:"require_mention,omitempty"`
}

// Host config types (~/.config/niwa/hosts/<hostname>.toml)

type HostConfig struct {
    Channels   HostChannels              `toml:"channels"`
    Env        map[string]string         `toml:"env"`
    Settings   map[string]string         `toml:"settings"`
    Workspaces map[string]WorkspaceScope `toml:"workspaces"`
}

type WorkspaceScope struct {
    Channels  HostChannels          `toml:"channels"`
    Env       map[string]string     `toml:"env"`
    Settings  map[string]string     `toml:"settings"`
}

type HostChannels struct {
    Telegram HostTelegramConfig `toml:"telegram"`
}

type HostTelegramConfig struct {
    Bots map[string]string `toml:"bots"`
}
```

**Implementation note:** In v0.1, `[hooks]`, `[settings]`, `[env]`, and `[channels]` sections should parse and validate but not generate output. Consider using `map[string]any` for sections whose consumers don't ship until v0.2/v0.3, switching to typed structs when the generation logic lands.

### Command flow: `niwa apply`

1. Find workspace.toml by walking up from cwd (or use registry if invoked with a name)
2. Parse workspace.toml into `WorkspaceConfig`
3. Load host config from `~/.config/niwa/hosts/<hostname>.toml` if it exists
4. Merge host overrides onto workspace config (host wins on conflict)
5. Determine target instance (from cwd if inside one, or create new)
6. For each group: create group directory if missing
7. For each repo in each group: clone if missing, verify URL matches
8. Install workspace CLAUDE.md (expand template variables, write to instance root)
9. Install group CLAUDE.md files (expand variables, write to group directories)
10. Install repo CLAUDE.local.md files (expand variables, warn if `*.local*` missing from .gitignore)
11. Install subdirectory CLAUDE.local.md files
12. (v0.2) Copy hooks, generate settings.local.json, merge env files per repo
13. (v0.3) Configure channel state directories from host config
14. Update `.niwa/instance.json` with managed file hashes and repo state

## Implementation Approach

### v0.1: Core lifecycle

- Parse workspace.toml (full schema, validate all sections)
- `niwa init <name>` - register a workspace config in the global registry
- `niwa create` - create a new instance (clone repos, install content hierarchy)
- `niwa apply` - idempotent sync (update content files, clone missing repos)
- `niwa status` - show instance state, detect drift via content hashes
- `niwa reset` - destroy and recreate an instance
- `niwa destroy` - remove an instance

### v0.2: Claude Code integration

- Generate `settings.local.json` per repo from `[settings]` config
- Copy hook scripts to `.claude/hooks/` per repo
- Merge env files into `.local.env` per repo
- Per-repo overrides for hooks, settings, env

### v0.3: Channels and per-host config

- Read `~/.config/niwa/hosts/<hostname>.toml`
- Telegram bot state directory creation and access config
- Per-host env and settings overrides
- Bot-to-instance assignment by suffix key

## Security Considerations

- **Secrets isolation**: Bot tokens and API keys live in `~/.config/niwa/hosts/` (mode 600) or `.env` files, never in workspace.toml. The config format enforces this by design: `[channels.telegram]` in workspace.toml holds only access rules, not tokens.
- **Gitignore awareness**: niwa warns if a repo's `.gitignore` lacks a `*.local*` pattern when writing `.local.md` or `.local.env` files, but does not modify the repo's `.gitignore` — that's the repo owner's responsibility. `.niwa/` is added to `.gitignore` at the instance root (a non-git directory managed by niwa). This separation keeps niwa from making unsolicited changes inside repos users control.
- **Content drift detection**: instance.json tracks SHA-256 hashes of all managed files, enabling detection of unintended modifications (e.g., a user accidentally editing a generated file). This is drift detection, not tamper-proofing: an attacker who can modify CLAUDE.md can also modify instance.json.
- **Template expansion must use plain string replacement**, not Go's `text/template` or similar engines that support method calls. This keeps the attack surface minimal: only the 4 declared variables are expanded, with no code execution path.
- **Name validation**: Group and repo names become directory components. They must be constrained to a safe character set (`[a-zA-Z0-9._-]+`) to prevent directory traversal via names like `../../.ssh`.
- **Path traversal validation**: Content source paths must be validated to stay within `content_dir`. Subdirectory keys must stay within the repo directory. niwa rejects paths containing `..` or absolute path components in source references. The implementation must use `filepath.EvalSymlinks` before containment checks to prevent symlink-based traversal.
- **Trust model**: workspace.toml and its referenced files (content, hooks, env) should be treated with the same trust as executable scripts. They direct file writes, git clones, and (in v0.2) hook installation. Content files are particularly sensitive: they become CLAUDE.md instructions that shape AI agent behavior, making a malicious content file a prompt injection vector. Users should only register workspace configs from sources they trust.
- **Remote config pinning**: When `niwa init` fetches from a remote source, it should pin to a specific commit or tag rather than tracking a branch. A `--review` flag should show what the config will do before registering it.
- **Host config permissions**: `~/.config/niwa/hosts/*.toml` files should be created with restrictive permissions (600). niwa warns if permissions are too open.
- **Absolute paths in committed files**: The `{workspace}` variable expands to an absolute filesystem path, which appears in committed CLAUDE.md files at workspace and group levels. This is an intentional trade-off: Claude Code needs absolute paths for its context model. Users should be aware this exposes their directory structure in committed files.

## Consequences

### Positive

- Workspace setup becomes reproducible: share workspace.toml and content files, any developer can `niwa create` an identical workspace
- Content hierarchy is explicit and auditable: every CLAUDE.md mapping is visible in the config or follows a known convention
- Multi-instance support enables parallel workflows (main work, hotfix, review) from a single config
- Phased schema means early adopters get a stable format that grows with them

### Negative

- Content duplication across repo files (shared boilerplate like "Repo Visibility" headers) isn't addressed in v0.1. Mitigation: optional template system can be added later without schema changes.
- Three configuration layers (workspace.toml, host config, instance state) add complexity. Mitigation: host config is optional and only needed for channel integration. Most users only interact with workspace.toml.
- Nested group structure in TOML produces long key paths (`[groups.public.repos.tsuku]`). Mitigation: repos with no overrides are single-line headers, keeping the common case concise.
