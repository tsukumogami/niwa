---
status: Proposed
problem: |
  niwa needs to configure Claude Code at the workspace instance root (above all
  repos) with context, hooks, settings, env, and plugins. The instance root is
  a non-git directory with different constraints than repos. Several decisions
  about how workspace-root Claude config works have been made experimentally
  but aren't documented, and there's no way to override workspace-level shared
  config specifically for the instance root session.
decision: |
  Use @import in CLAUDE.md for level-scoped workspace context (not inherited
  by child repos). Use settings.json (not .local, non-git dir) for hooks,
  permissions, and env. Add [instance] section to workspace.toml for overriding
  the shared [claude] config at the instance root level, using the same
  override semantics as [repos.X.claude].
rationale: |
  The @import trick exploits Claude Code's relative path resolution -- imports
  fail silently in child repos where the file doesn't exist. settings.json
  works in non-git dirs. The [instance] section mirrors [repos.X] override
  patterns for consistency, and fills the gap where the instance root couldn't
  deviate from shared config.
---

# DESIGN: Workspace Root Claude Configuration

## Status

Proposed

## Context and Problem Statement

niwa configures Claude Code per-repo with hooks, settings, env, files, and
plugins. But workspaces have a level above repos: the instance root. Running
Claude Code from the instance root is useful for cross-repo work (searching,
comparing, creating issues), but it needs its own Claude Code configuration.

Three problems need solving:

1. **Workspace context**: the instance root needs instructions telling Claude
   it's in a multi-repo workspace, listing repos and navigation guidance. This
   context must NOT be inherited by child repo sessions.

2. **Settings, hooks, and env at the instance root**: Claude Code needs
   permissions, hooks (gate-online), and env vars (GH_TOKEN) to work properly
   from the instance root. The instance root is a non-git directory, so the
   `.local` naming conventions don't apply.

3. **Overriding shared config for the instance root**: the workspace-level
   `[claude]` config is the shared default for all repos. But the instance
   root may need different config (different permissions, different plugins,
   additional hooks). Today there's no `[instance]` section to override the
   shared defaults at this level.

## Decision Drivers

- Instance root is non-git: `.local` naming isn't needed, `settings.json` works
- Workspace context must not leak into repo sessions
- Override semantics should be consistent with existing `[repos.X]` patterns
- The solution must work with `claude -p` (non-interactive) from the instance root

## Considered Options

### Decision 1: Level-scoped workspace context (established)

How to provide workspace-specific instructions that Claude Code sees at the
instance root but not in child repos.

#### Chosen: @import in CLAUDE.md

Generate `workspace-context.md` at the instance root and prepend
`@workspace-context.md` to the instance root's `CLAUDE.md`. Claude Code's
`@import` resolves relative to the CLAUDE.md file's location. When a child
repo inherits the parent CLAUDE.md through directory traversal, the `@import`
line is visible but the file doesn't exist relative to the child, so the
import silently fails.

**Experimentally verified:**
- `@import` in non-git directories: works
- `@import` inherited by child git repos: fails silently (correct behavior)
- Content is auto-generated from classified repos (groups, names, paths)

#### Alternatives Considered

**AGENTS.md**: Experimentally shown to not be loaded reliably by Claude Code.
10/10 runs returned UNKNOWN for AGENTS.md content. Rejected.

**claudeMdIncludes in settings**: `claudeMdIncludes` in both `settings.json`
and `settings.local.json` failed 10/10 runs in both git and non-git
directories. Rejected.

**Conditional instructions in CLAUDE.md**: Write workspace instructions
directly in CLAUDE.md with "ignore this if you're in a repo" guidance.
Rejected because it clutters every repo session's context window and relies
on the model following the conditional.

### Decision 2: Settings file for non-git instance root (established)

How to configure hooks, permissions, and env at the instance root which
is not a git repository.

#### Chosen: settings.json (not .local)

Use `.claude/settings.json` at the instance root. Experimentally verified
that `settings.json` works in non-git directories for permissions, hooks,
and env. `.local` naming is irrelevant since there's no git repo to
accidentally commit to.

Plugins use `claude plugin install --scope local` which writes
`enabledPlugins` to `.claude/settings.local.json`. This also works in
non-git directories.

**Experimentally verified:**
- `settings.json` with `bypassPermissions`: works in non-git (3/3 runs)
- Hooks in `settings.json`: work in non-git
- `claude plugin install --scope local`: works in non-git

Hook scripts at the instance root don't need `.local` renaming (no git).
They're installed as-is (e.g., `gate-online.sh`, not `gate-online.local.sh`).

#### Alternatives Considered

**settings.local.json only**: The `.local` variant works for plugins but
is the wrong convention for a non-git directory. The entire purpose of
`.local` is to stay out of git. In a non-git directory, `settings.json`
is the correct file.

**git init the instance root**: Initializing a git repo just so
`settings.local.json` works. Rejected because it creates an unnecessary
git repo that would confuse users and pollute the workspace.

### Decision 3: Instance root config override

How to let users override the shared `[claude]` config specifically for
the instance root, without affecting repos.

#### Chosen: [instance] section in workspace.toml

Add an `[instance]` section to workspace.toml that follows the same
override pattern as `[repos.X]`:

```toml
[claude]
plugins = ["shirabe@shirabe", "tsukumogami@tsukumogami"]

[claude.settings]
permissions = "bypass"

[instance.claude]
plugins = ["shirabe@shirabe", "tsukumogami@tsukumogami", "cross-repo-tool@marketplace"]

[instance.claude.settings]
permissions = "ask"
```

**Override semantics** (identical to `[repos.X.claude]`):
- Hooks: extend (concatenate)
- Settings: replace per key
- Plugins: `*[]string` -- nil inherits, non-nil replaces entirely
- Env promote: union
- Env vars: replace per key

When `[instance]` is absent or empty, the instance root uses the
workspace `[claude]` defaults (same behavior as a repo with no override).

#### Alternatives Considered

**No override (always use shared config)**: The instance root always gets
the same config as repos. Rejected because the instance root has different
needs -- it's a non-git multi-repo context, not a single-repo dev session.
Users may want different permissions (more cautious across repos) or
additional plugins.

**Separate top-level `[workspace-root]` section**: A different name than
`[instance]` to avoid confusion with the instance lifecycle concept.
Rejected because `[instance]` is already the term niwa uses for the
directory where repos are cloned (instance.json, EnumerateInstances,
etc.). The instance root config logically lives in `[instance]`.

## Decision Outcome

Three mechanisms work together:

1. **Workspace context** via `@import` in CLAUDE.md provides level-scoped
   instructions that don't leak into repos.

2. **settings.json** (not `.local`) at the instance root configures hooks,
   permissions, env, plugins, and marketplaces for Claude Code in the
   non-git directory. The settings materializer writes `enabledPlugins`
   and `extraKnownMarketplaces` declaratively -- Claude Code's startup
   reconciler handles materialization automatically. Also sets
   `includeGitInstructions: false` since the instance root has no git.

3. **`[instance]` section** in workspace.toml lets users override the
   shared `[claude]` config for the instance root. The override merges
   with workspace defaults using the same semantics as `[repos.X]`.

When generating instance root settings, niwa resolves:
workspace `[claude]` defaults -> `[instance.claude]` overrides -> effective config.

**Note:** The declarative approach for plugins and marketplaces (via
`enabledPlugins` and `extraKnownMarketplaces` in settings.json) should
eventually replace the CLI-based approach used for repos too. See #35.

## Solution Architecture

### Overview

One new config type (`InstanceConfig`), one new merge function
(`MergeInstanceOverrides`), and updates to the pipeline steps that generate
instance root files.

### Components

**`config.InstanceConfig`**: new section in workspace.toml.

```go
type InstanceConfig struct {
    Claude *ClaudeConfig `toml:"claude,omitempty"`
    Env    EnvConfig     `toml:"env,omitempty"`
    Files  map[string]string `toml:"files,omitempty"`
}
```

**`WorkspaceConfig.Instance`**: new field.

```go
type WorkspaceConfig struct {
    // ... existing fields ...
    Instance InstanceConfig `toml:"instance,omitempty"`
}
```

**`MergeInstanceOverrides`**: resolves effective config for the instance
root by merging workspace `[claude]` with `[instance.claude]`.

```go
func MergeInstanceOverrides(ws *WorkspaceConfig) EffectiveConfig
```

**Pipeline Step 4.5**: generates `workspace-context.md`, `settings.json`,
and hooks at the instance root. Uses `MergeInstanceOverrides` to resolve
effective config. Plugins and marketplaces are written declaratively into
settings.json (no CLI calls).

The generated `settings.json` includes:
- `permissions` (from claude.settings)
- `hooks` (from claude.hooks, with scripts copied to .claude/hooks/)
- `env` (from claude.env promote + vars)
- `enabledPlugins` (from claude.plugins)
- `extraKnownMarketplaces` (from claude.marketplaces, mapped to the format
  Claude Code expects)
- `includeGitInstructions: false` (hardcoded, since instance root is non-git)

### Data Flow

```
workspace.toml
    |
    +-- [claude] (shared defaults)
    |
    +-- [instance.claude] (overrides for instance root)
    |
    v
MergeInstanceOverrides
    |
    v
Step 4.5:
    +-- workspace-context.md (auto-generated, @imported)
    +-- .claude/settings.json (hooks, permissions, env, plugins, marketplaces)
    +-- .claude/hooks/{event}/ (hook scripts, no .local rename)
```

## Implementation Approach

### Phase 1: InstanceConfig type and merge

- Add `InstanceConfig` struct and `Instance` field on `WorkspaceConfig`
- Implement `MergeInstanceOverrides`
- Config parsing tests

### Phase 2: Pipeline integration

- Update Step 4.5 to use `MergeInstanceOverrides` for effective config
- `InstallWorkspaceRootSettings` takes effective config, not raw workspace config
- Plugin install at instance root uses effective plugins
- Hook scripts at instance root: no `.local` rename (non-git)
- Tests for override behavior

### Phase 3: Scaffold and documentation

- Update scaffold template with commented `[instance]` example
- Update workspace-context generation

## Security Considerations

The instance root `settings.json` may contain env vars (like GH_TOKEN) in
plaintext. This is the same security posture as repos' `settings.local.json`.
The instance root directory is not a git repo, so there's no risk of
accidentally committing secrets.

Hook scripts at the instance root are not `.local`-renamed since there's
no git to worry about. They're plain copies from the config directory.

## Consequences

### Positive

- Claude Code works fully from the workspace root (hooks, permissions,
  plugins, env, context)
- Instance root config is independently overridable
- Consistent override semantics across instance and repo levels
- Non-git directory handled correctly (settings.json, no .local naming)

### Negative

- New config section adds conceptual surface area
- Two settings files at instance root: settings.json (niwa-managed) and
  settings.local.json (claude plugin install --scope local)

### Mitigations

- `[instance]` is optional -- workspace defaults apply when absent
- The two settings files serve different purposes and are both managed
  automatically
