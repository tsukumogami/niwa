# Decision 2: Scaffolded workspace.toml for local init

## Decision Question

What should the scaffolded workspace.toml look like for local init (no --from)?

## Chosen Approach: Commented template with active and placeholder sections

The scaffold produces a valid, parseable workspace.toml with the minimum required fields active and all optional sections present as commented-out examples. Two variants differ only in the `[workspace]` block:

- `niwa init <name>` -- sets `name = "<name>"`
- `niwa init` (no args) -- sets `name = "workspace"` as a default placeholder

### Scaffolded workspace.toml

```toml
[workspace]
name = "my-project"
# version = "0.1.0"
default_branch = "main"
content_dir = "claude"

# --- Sources: GitHub orgs to discover repos from ---
# Uncomment and configure at least one source before running niwa apply.
#
# [[sources]]
# org = "my-org"
#
# For orgs with many repos, set a threshold or list repos explicitly:
# [[sources]]
# org = "large-org"
# max_repos = 30
# repos = ["repo-a", "repo-b"]

# --- Groups: classify repos into directories ---
# Groups determine the on-disk directory layout. Each repo must match
# exactly one group or it will be excluded with a warning.
#
# Filter by GitHub visibility:
# [groups.public]
# visibility = "public"
#
# [groups.private]
# visibility = "private"
#
# Or list repos explicitly:
# [groups.backend]
# repos = ["api-server", "worker"]

# --- Per-repo overrides ---
# [repos.my-repo]
# branch = "develop"
# scope = "strategic"
# claude = false             # Skip Claude Code configuration for this repo

# --- Content hierarchy ---
# Content files live in the content_dir ("claude/" by default).
# Target paths are derived by convention:
#   workspace content  -> $INSTANCE/CLAUDE.md
#   group content      -> $INSTANCE/{group}/CLAUDE.md
#   repo content       -> $INSTANCE/{group}/{repo}/CLAUDE.local.md
#
# [content.workspace]
# source = "workspace.md"
#
# [content.groups.public]
# source = "public.md"
#
# [content.repos.my-repo]
# source = "repos/my-repo.md"
#
#   [content.repos.my-repo.subdirs]
#   docs = "repos/my-repo-docs.md"

# --- Hooks ---
# Hook scripts distributed to all repos during niwa apply.
#
# [hooks]
# pre_tool_use = ["hooks/gate-online.sh"]
# stop = ["hooks/workflow-continue.sh"]

# --- Settings ---
# Claude Code settings applied to all repos.
#
# [settings]
# permissions = "bypass"

# --- Environment ---
# Environment variables and .env files distributed to repos.
# Secrets should stay in .env files, never inline.
#
# [env]
# files = ["env/workspace.env"]
# vars = { LOG_LEVEL = "debug" }

# --- Channels ---
# Communication channel plugins.
#
# [channels.telegram]
# plugin = "telegram@claude-plugins-official"
# [channels.telegram.access]
# allow_from = ["123456789"]
```

### Directory structure created alongside

For `niwa init my-project`:

```
my-project-root/
  .niwa/
    workspace.toml              # The scaffold above
    claude/                     # Empty content_dir, ready for content files
```

For `niwa init` (detached):

```
<cwd>/
  .niwa/
    workspace.toml              # name = "workspace"
    claude/                     # Empty content_dir
```

### Key design choices

**Only `[workspace]` is active.** Sources, groups, and content are all commented out. This makes the file parseable immediately (`workspace.name` is the only required field) while showing the user every available section through comments. Running `niwa apply` on this scaffold without uncommenting a source would produce a no-op with a message like "no sources configured."

**Comments teach the schema.** Each commented section includes a brief explanation of what it does and a realistic example. The comments follow the grouping from the design doc (sources, groups, repos, content, hooks, settings, env, channels) with `---` separators matching the full schema reference style.

**`default_branch = "main"` is active.** This is a safe default that matches most projects. Users working with a different default branch can change it.

**`content_dir = "claude"` is active and the directory is created.** The empty `claude/` directory signals where content files should go. Creating it avoids a "directory not found" error if the user uncomments a content entry before creating files.

**No `version` field active.** The version field is optional and its semantics aren't defined yet. It's shown as a comment so users know it exists.

**Detached mode uses `name = "workspace"`.** This is a neutral default. The detached workspace won't be registered in the global registry, so the name is purely for template variable expansion (`{workspace_name}`). Users can change it.

### Alternatives considered

**Fully populated with example values.** A scaffold with active (non-commented) sources, groups, and content entries pointing to a fictional org. Rejected because it would fail on `niwa apply` (the org doesn't exist) and require the user to delete lines rather than uncomment them. Uncommenting is a clearer editing gesture than replacing placeholder values.

**Minimal scaffold with separate documentation.** Only `[workspace]` with a comment pointing to docs. Rejected because the scaffold should be self-documenting -- users shouldn't need to context-switch to a docs page to configure their workspace.

**Interactive prompting for source org.** Ask the user for their GitHub org during init and populate `[[sources]]` automatically. Rejected for the local init path because it adds a network dependency and interactive prompting complexity. The `--from` path handles the case where config comes from a remote source. Local init should be fast and offline.
