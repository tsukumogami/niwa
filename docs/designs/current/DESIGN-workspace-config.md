---
status: Current
problem: |
  niwa needs a declarative TOML schema to replace a 700-line imperative bash
  installer that hardcodes workspace structure, CLAUDE.md hierarchy, hooks,
  settings, and environment distribution for a single organization. The schema
  must generalize these operations for any multi-repo workspace with layered
  AI context, supporting multi-instance workspaces and multi-org sources.
decision: |
  A workspace.toml file in .niwa/ at the workspace root, with source-based
  repo discovery, group classification filters, convention-driven content file
  placement, and workspace-level hooks/settings/env with per-repo overrides.
rationale: |
  Source auto-discovery reduces boilerplate for small orgs. Group filters derive
  membership from GitHub metadata instead of requiring explicit listing.
  Convention-driven content placement eliminates redundant target path
  declarations while keeping the mapping transparent. Storing the config in
  .niwa/ keeps it portable to GitHub and separate from instances.
---

# DESIGN: Workspace config format

## Status

Accepted

## Context and Problem Statement

niwa needs a declarative configuration format that expresses the workspace structure currently wired by an imperative 700-line bash installer. The installer performs 27 operations across repo cloning, CLAUDE.md hierarchy generation, per-repo hooks and settings distribution, environment file merging, and plugin registration. All of this is hardcoded for a single organization.

The config must generalize these operations into a TOML schema that any developer can use to define a multi-repo workspace with layered AI context. It must support multi-instance workspaces from the same definition (e.g., tsuku/, tsuku-2/), template variable substitution in content files, and multi-org source declarations.

## Decision Drivers

- **Parseable by Go TOML libraries**: schema must work with BurntSushi/toml or pelletier/go-toml
- **Content by reference, not inline**: CLAUDE.md content lives in separate files, config points to them
- **Multi-instance required**: same config, multiple workspace instances, isolated state
- **Three-level hierarchy**: workspace > group > repo context inheritance for CLAUDE.md
- **Phased delivery**: the schema is defined upfront but not all sections need implementation at once
- **Convention over configuration**: sensible defaults reduce boilerplate for common cases
- **Prior art alignment**: TOML matches tsuku recipe format; patterns from Google repo tool and Nx inform the design

## Considered Options

### Decision 1: Sources, groups, and repo discovery

The config must declare where repos come from and how they're organized into groups. Groups determine the directory layout on disk (group name = directory name) and provide a level in the CLAUDE.md content hierarchy. The schema should minimize boilerplate for the common case (small org, all repos included) while scaling to multi-org workspaces.

Key assumptions: most workspaces pull from one or two GitHub orgs. Group names map 1:1 to directory names. Repo membership in groups can often be derived from GitHub metadata (visibility) rather than declared explicitly.

#### Chosen: Sources with auto-discovery, groups as classification filters

Repos come from **sources** (GitHub orgs). By default, niwa auto-discovers all repos in a source org via the GitHub API. Groups are **filters** that classify repos by metadata (e.g., visibility) or by explicit listing — they don't contain repo declarations.

Auto-discovery has a threshold (default: 10 repos per source, configurable via `max_repos`). If an org exceeds the threshold, niwa errors and requires explicit repo listing in the source.

**Classification rules:**
- A repo matching no group is **excluded** with a warning
- A repo matching multiple groups is an **error**

```toml
[workspace]
name = "tsuku"

# --- Sources: where repos come from ---

[[sources]]
org = "tsukumogami"
# All repos auto-discovered (org has <10 repos)

[[sources]]
org = "large-org"
max_repos = 30                    # Override threshold for this source
repos = ["repo-a", "repo-b"]     # Or list explicitly if preferred

# --- Groups: how repos are classified ---

[groups.public]
visibility = "public"             # Filter: matches repos with public visibility

[groups.private]
visibility = "private"            # Filter: matches repos with private visibility

# --- Per-repo overrides ---

[repos.".github"]
claude = false                    # Skip Claude Code configuration

[repos.vision]
scope = "strategic"
```

Groups can also use explicit repo lists instead of (or in addition to) metadata filters:

```toml
[groups.infra]
repos = ["terraform-modules", "deploy-scripts"]

[groups.services]
repos = ["api-gateway", "user-service"]
```

niwa queries the GitHub API during `niwa create` and `niwa apply` to discover repos and resolve group membership. This means these commands require network access.

#### Alternatives considered

**Nested groups with structural repo membership**: Repos declared inside their group using TOML nesting (`[groups.public.repos.tsuku]`). Rejected because it requires listing every repo explicitly under its group, duplicating information that's already available from GitHub metadata (visibility). The nesting also produces verbose configs for the common case where repos need no overrides.

**Flat `[[repos]]` with group attribute**: All repos in a top-level array, group as a string field. Rejected because it still requires explicit listing of every repo with manual group assignment.

**No auto-discovery, sources only for URL resolution**: Sources provide the org for URL shorthand but repos are always listed explicitly. Rejected because it doesn't reduce boilerplate for small orgs where "include everything" is the intent.

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

[claude.content.workspace]
source = "workspace.md"

[claude.content.groups.public]
source = "public.md"

[claude.content.groups.private]
source = "private.md"

[claude.content.repos.tsuku]
source = "repos/tsuku.md"

  [claude.content.repos.tsuku.subdirs]
  recipes = "repos/tsuku-recipes.md"
  website = "repos/tsuku-website.md"
  telemetry = "repos/tsuku-telemetry.md"

[claude.content.repos.koto]
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

When `content_dir` is set and a repo has no explicit `[claude.content.repos.X]` entry, niwa checks for `{content_dir}/repos/{repo_name}.md` and uses it automatically if found. This convention-over-configuration path means a minimal config can omit most content entries.

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

Per-repo overrides use the top-level `[repos]` section:
```toml
[repos.vision.settings]
permissions = "ask"
```

Secrets stay in `.env` files, never in TOML. niwa generates `settings.local.json` and `.local.env` per repo during `niwa apply`. The schema is defined here; generation logic is implemented when the hooks and env features land.

> **Note (niwa 0.9.4 — DESIGN-coordinator-loop.md Phase 1, Proposed):** The
> `[hooks] stop` entry is given a concrete implementation in that design: a
> `report-progress.sh` script generated at `workspace apply` time, using the
> absolute path to the niwa binary resolved from the host environment (not a
> PATH-dependent `niwa` name). It calls `niwa mesh report-progress --task-id
> $NIWA_TASK_ID` at every Claude Code turn boundary, resetting the stall
> watchdog automatically. Hook merge semantics concatenate this hook with any
> application-level stop hooks — no conflict or override occurs.

#### Alternatives considered

**Unified `[claude]` section**: Hooks, settings, and env grouped under one key. Rejected because it mixes concerns (file references, key-value pairs, enums) and the `[claude]` namespace risks collision with Claude Code's own config.

**Profile-based**: Named profiles that repos reference. Rejected because zero repos need different profiles today, and per-repo overrides (Option A) are more precise than swapping entire profiles.

**Workspace-only with no per-repo overrides**: Simplest schema but rejected because the schema must accommodate per-repo overrides even if their generation logic lands later.

### Decision 7: Config and content file location

workspace.toml and its referenced files (content, hooks, env) need a home that's version-controlled, shareable via GitHub, and discoverable by `niwa apply`. The workspace root itself is a plain directory containing instances — it's not a git repo. The config can't live flat in the root because it needs to be portable to GitHub as a repo.

#### Chosen: .niwa/ directory at the workspace root

workspace.toml and its content directory live in `.niwa/` at the workspace root. When initialized from a remote source (`niwa init --from <org/repo>`), `.niwa/` is the git checkout of the config repo. When scaffolded locally, it's a plain directory. All relative paths in workspace.toml (content_dir, hook paths, env file paths) resolve from `.niwa/`.

Discovery: `niwa apply` walks up from cwd looking for `.niwa/workspace.toml`.

```
tsuku-root/
  .niwa/                          # Config directory (git checkout if from remote)
    workspace.toml
    claude/
      workspace.md
      public.md
      repos/tsuku.md
    hooks/
    env/
  tsuku/                          # Instance 1
    CLAUDE.md
    public/
      CLAUDE.md
      tsuku/
        CLAUDE.local.md
```

#### Alternatives considered

**Flat in workspace root**: workspace.toml and claude/ sit directly at the root alongside instances. Rejected because the root isn't a git repo, so the config can't be pushed to GitHub without extra steps. Also clutters the root with config files mixed in with instance directories.

**Inside a managed repo**: Config lives in one of the repos niwa manages (like the current bash installer in the tools repo). Rejected because it creates a circular dependency — the config defines the repos, so it can't live inside one.

**Named subdirectory**: A visible directory like `niwa-config/` instead of a dotdir. Rejected because it adds a visible directory that isn't an instance, and doesn't reuse the `.niwa/` convention.

## Decision Outcome

### Summary

niwa's workspace config is a TOML file (`workspace.toml`) that lives in `.niwa/` at the workspace root, alongside a content directory and any hook or env files it references. When the config comes from a remote source, `.niwa/` is the git checkout of the config repo.

Repos are discovered from source orgs via the GitHub API, with auto-discovery for small orgs (up to 10 repos by default) and explicit listing for larger ones. Groups are classification filters — they match repos by metadata (e.g., GitHub visibility) or by explicit listing. Each group name doubles as the directory name on disk. Repos matching no group are excluded with a warning; repos matching multiple groups cause an error.

Content files for the CLAUDE.md hierarchy are referenced by source path with target locations derived from convention: workspace and group content goes as `CLAUDE.md` in non-git directories, repo content goes as `CLAUDE.local.md` in git directories.

A minimal global registry at `~/.config/niwa/config.toml` maps workspace names to root paths and optional remote sources.

Hooks, settings, and environment variables are declared at workspace level with optional per-repo overrides. Secrets stay in `.env` files referenced by path. niwa generates `settings.local.json`, copies hooks, and merges env files into each repo during `niwa apply`.

### Rationale

The decisions reinforce each other through consistent conventions. The config lives in `.niwa/` (Decision 7), which declares source orgs and classification groups (Decision 1). Groups define directory structure, which determines where content files land (Decision 2). Hooks and settings distribute uniformly by default (Decision 4) with optional per-repo overrides.

The main trade-off is explicitness over brevity. Content files are referenced individually rather than auto-generated, which means more lines in the config but no hidden generation logic. This aligns with the convention-over-configuration driver: the convention is where files land, not whether they exist.

The schema is designed to be complete upfront. `[hooks]`, `[settings]`, `[env]`, and `[channels]` sections parse and validate from the start, even before their generation logic is implemented. This lets the schema stabilize before the full implementation lands.

## Solution Architecture

### Config file hierarchy

The config directory (`.niwa/`) lives at the workspace root and is a git checkout when initialized from a remote source. Content files, hook scripts, and env files are versioned alongside workspace.toml. All relative paths in workspace.toml resolve from `.niwa/`.

**Config repo (what you author and commit):**

```
.niwa/                            # Config repo (git checkout)
  workspace.toml                  # Workspace definition
  claude/                         # Content source directory
    workspace.md                  # -> $INSTANCE/CLAUDE.md
    public.md                     # -> $INSTANCE/public/CLAUDE.md
    private.md                    # -> $INSTANCE/private/CLAUDE.md
    repos/
      tsuku.md                    # -> $INSTANCE/public/tsuku/CLAUDE.local.md
      tsuku-recipes.md            # -> $INSTANCE/public/tsuku/recipes/CLAUDE.local.md
      koto.md                     # -> $INSTANCE/public/koto/CLAUDE.local.md
      ...
  hooks/                          # Hook scripts referenced by [hooks]
    gate-online.sh
  env/                            # Env files referenced by [env]
    workspace.env
```

**Workspace on disk (after `niwa create`):**

```
tsuku-root/                       # Workspace root
  .niwa/                          # Config repo (as above)
  tsuku/                          # Instance 1
    CLAUDE.md                     # Generated from claude/workspace.md
    public/
      CLAUDE.md                   # Generated from claude/public.md
      tsuku/                      # Cloned repo
        CLAUDE.local.md           # Generated from claude/repos/tsuku.md
      koto/
        CLAUDE.local.md
    private/
      CLAUDE.md                   # Generated from claude/private.md
      vision/
        CLAUDE.local.md
  tsuku-2/                        # Instance 2
    ...
```

**Global config:**

```
~/.config/niwa/
  config.toml                     # Global registry
```

### Full schema reference

```toml
# workspace.toml

[workspace]
name = "tsuku"                        # Workspace name (used for registry, instance naming)
default_branch = "main"               # Default branch for all repos
content_dir = "claude"                # Directory containing content source files

# --- Sources: where repos come from ---

[[sources]]
org = "tsukumogami"                   # Auto-discover all repos (org has <10)

# For larger orgs, override the threshold or list repos explicitly:
# [[sources]]
# org = "large-org"
# max_repos = 30
# repos = ["repo-a", "repo-b"]

# --- Groups: how repos are classified ---

[groups.public]
visibility = "public"                 # Filter: matches repos with public visibility

[groups.private]
visibility = "private"                # Filter: matches repos with private visibility

# Groups can also list repos explicitly:
# [groups.infra]
# repos = ["terraform-modules", "deploy-scripts"]

# --- Per-repo overrides ---

[repos.".github"]
claude = false                        # Skip Claude Code configuration

[repos.vision]
scope = "strategic"

# --- Content hierarchy ---

[claude.content.workspace]
source = "workspace.md"

[claude.content.groups.public]
source = "public.md"

[claude.content.groups.private]
source = "private.md"

[claude.content.repos.tsuku]
source = "repos/tsuku.md"

  [claude.content.repos.tsuku.subdirs]
  recipes = "repos/tsuku-recipes.md"
  website = "repos/tsuku-website.md"
  telemetry = "repos/tsuku-telemetry.md"

# --- Hooks and settings ---

[hooks]
pre_tool_use = ["hooks/gate-online.sh"]
stop = ["hooks/workflow-continue.sh"]

[settings]
permissions = "bypass"

# --- Environment ---

[env]
files = ["env/workspace.env"]

# --- Channels ---

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
    Sources   []SourceConfig            `toml:"sources"`
    Groups    map[string]GroupConfig     `toml:"groups"`
    Repos     map[string]RepoOverride   `toml:"repos"`
    Content   ContentConfig             `toml:"content"`
    Hooks     HooksConfig              `toml:"hooks"`
    Settings  SettingsConfig           `toml:"settings"`
    Env       EnvConfig                `toml:"env"`
    Channels  ChannelsConfig           `toml:"channels"`
}

type WorkspaceMeta struct {
    Name          string `toml:"name"`
    DefaultBranch string `toml:"default_branch,omitempty"`
    ContentDir    string `toml:"content_dir,omitempty"`
}

type SourceConfig struct {
    Org      string   `toml:"org"`
    Repos    []string `toml:"repos,omitempty"`     // Explicit repo list (required if org exceeds max_repos)
    MaxRepos int      `toml:"max_repos,omitempty"` // Auto-discovery threshold (default: 10)
}

type GroupConfig struct {
    Visibility string   `toml:"visibility,omitempty"` // Filter by GitHub visibility
    Repos      []string `toml:"repos,omitempty"`      // Explicit repo list (alternative to filters)
}

type RepoOverride struct {
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

```

**Implementation note:** `[hooks]`, `[settings]`, `[env]`, and `[channels]` sections should parse and validate before their generation logic is implemented. Consider using `map[string]any` for sections whose consumers don't exist yet, switching to typed structs when the generation logic lands.

### Command flow: `niwa apply`

1. Find `.niwa/workspace.toml` by walking up from cwd (or use registry if invoked with a name)
2. Parse workspace.toml into `WorkspaceConfig`
3. Query GitHub API for each source org to discover repos (respecting `max_repos` threshold)
4. Classify repos into groups by matching filters (visibility) or explicit group repo lists
5. Warn on repos matching no group (excluded); error on repos matching multiple groups
6. Determine target instance (from cwd if inside one, or create new)
7. For each group: create group directory if missing
8. For each classified repo: clone if missing, verify URL matches
9. Install workspace CLAUDE.md (expand template variables, write to instance root)
10. Install group CLAUDE.md files (expand variables, write to group directories)
11. Install repo CLAUDE.local.md files (expand variables, warn if `*.local*` missing from .gitignore)
12. Install subdirectory CLAUDE.local.md files
13. Copy hooks, generate settings.local.json, merge env files per repo

## Scope

This design covers the workspace.toml schema, content hierarchy model, and configuration layering (hooks, settings, env). It does not cover command UX (init scaffolding, apply convergence logic, status output format), per-host overrides, plugin orchestration, shell integration, or the adopt command. See the niwa roadmap for the full feature set and delivery sequencing.

## Security Considerations

- **Secrets isolation**: API keys and tokens live in `.env` files referenced by path, never inline in workspace.toml.
- **Gitignore awareness**: niwa warns if a repo's `.gitignore` lacks a `*.local*` pattern when writing `.local.md` or `.local.env` files, but does not modify the repo's `.gitignore` — that's the repo owner's responsibility. `.niwa/` is added to `.gitignore` at the instance root (a non-git directory managed by niwa). This separation keeps niwa from making unsolicited changes inside repos users control.
- **Content drift detection**: instance.json tracks SHA-256 hashes of all managed files, enabling detection of unintended modifications (e.g., a user accidentally editing a generated file). This is drift detection, not tamper-proofing: an attacker who can modify CLAUDE.md can also modify instance.json.
- **Template expansion must use plain string replacement**, not Go's `text/template` or similar engines that support method calls. This keeps the attack surface minimal: only the 4 declared variables are expanded, with no code execution path.
- **Name validation**: Group and repo names become directory components. They must be constrained to a safe character set (`[a-zA-Z0-9._-]+`) to prevent directory traversal via names like `../../.ssh`.
- **Path traversal validation**: Content source paths must be validated to stay within `content_dir`. Subdirectory keys must stay within the repo directory. niwa rejects paths containing `..` or absolute path components in source references. The implementation must use `filepath.EvalSymlinks` before containment checks to prevent symlink-based traversal.
- **Trust model**: workspace.toml and its referenced files (content, hooks, env) should be treated with the same trust as executable scripts. They direct file writes, git clones, and hook installation. Content files are particularly sensitive: they become CLAUDE.md instructions that shape AI agent behavior, making a malicious content file a prompt injection vector. Users should only register workspace configs from sources they trust.
- **Remote config pinning**: When `niwa init` fetches from a remote source, it should pin to a specific commit or tag rather than tracking a branch. A `--review` flag should show what the config will do before registering it.

- **Absolute paths in committed files**: The `{workspace}` variable expands to an absolute filesystem path, which appears in committed CLAUDE.md files at workspace and group levels. This is an intentional trade-off: Claude Code needs absolute paths for its context model. Users should be aware this exposes their directory structure in committed files.

## Consequences

### Positive

- Workspace setup becomes reproducible: share workspace.toml and content files, any developer can `niwa create` an identical workspace
- Content hierarchy is explicit and auditable: every CLAUDE.md mapping is visible in the config or follows a known convention
- Multi-instance support enables parallel workflows (main work, hotfix, review) from a single config
- Upfront schema definition means early adopters get a stable format that grows with them

### Negative

- Content duplication across repo files (shared boilerplate like "Repo Visibility" headers) isn't addressed by this design. Mitigation: optional template system can be added later without schema changes.
- Auto-discovery requires GitHub API access, adding a network dependency to `niwa create` and `niwa apply`. Mitigation: the threshold (default: 10) limits API calls, and discovered repos can be cached for offline re-applies.
- Auto-discovery requires GitHub API access during `niwa create` and `niwa apply`. Mitigation: discovered repos can be cached in instance state for offline re-applies. The threshold (default: 10) prevents runaway discovery in large orgs.
