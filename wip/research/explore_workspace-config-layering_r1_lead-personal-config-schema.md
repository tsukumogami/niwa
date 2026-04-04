# Lead: Personal config schema design

## Findings

### Current Workspace Config Structure

The workspace config is stored in `.niwa/workspace.toml` (location: `internal/config/discover.go` lines 9-13). A `WorkspaceConfig` (lines 40-51 in `internal/config/config.go`) contains:

1. **Workspace metadata** (`WorkspaceMeta`): name, version, default_branch, content_dir, setup_dir
2. **Sources**: List of GitHub orgs for repo discovery
3. **Groups**: Classification of repos by visibility/purpose
4. **Repos**: Per-repo overrides (`RepoOverride` struct, lines 114-124)
5. **Content**: CLAUDE.md references for workspace, groups, and repos
6. **Claude**: Workspace-level hooks, plugins, settings, env vars, marketplaces
7. **Env**: Global environment files and variables
8. **Files**: Arbitrary file mappings
9. **Instance**: Instance-root-level overrides (same semantics as RepoOverride)
10. **Channels**: Placeholder for channel configs

### Current Merge Semantics

The merge logic in `internal/workspace/override.go` defines how per-repo overrides combine with workspace defaults (`MergeOverrides`, lines 27-122):

- **Settings**: repo values win per key (override workspace)
- **Env files**: repo appends to workspace (order matters)
- **Env vars**: repo wins per key
- **Hooks**: repo extends (concatenate lists per event key)
- **Claude env promote**: repo extends (union of keys)
- **Plugins**: repo replaces workspace entirely (nil = inherit)
- **Files**: repo wins per source key; empty string removes mapping
- **Enabled**: per-repo `claude.enabled` flag controls whether Claude content is installed

The same semantics apply to instance-level overrides via `MergeInstanceOverrides` (lines 127-212).

### Workspace Identification Mechanism

Niwa identifies a workspace through layered discovery and registration:

1. **Discovery** (`internal/config/discover.go`): Walk up from cwd looking for `.niwa/workspace.toml`
2. **Global Registry** (`internal/config/registry.go`): `~/.config/niwa/config.toml` maps workspace names to `(source, root)` tuples
   - `GlobalConfig` structure (lines 12-15): contains `Global` settings and `Registry` map
   - `RegistryEntry` (lines 23-26): `Source` (path to workspace.toml) and `Root` (workspace directory)
3. **Instance State** (`internal/workspace/state.go`): `.niwa/instance.json` in each instance stores `ConfigName` (pointer to string, lines 29)
   - `InstanceState` records `ConfigName`, `InstanceName`, `InstanceNumber`, `Root`, creation/last-applied times, managed files, repo states

**Scope Resolution** (`internal/workspace/scope.go`):
- If `--instance` flag: registry lookup → enumerate instances → match by `InstanceName`
- If cwd is in instance: use that instance (single)
- If cwd is in workspace root: enumerate all instances (all)
- Workspace name is stored in `workspace.toml` as `[workspace].name` (required field, validated with alphanumeric+dots/hyphens/underscores)

### Configurable Fields by Level

**Per-Repo** (`RepoOverride`):
- URL, Branch, Scope, Group (metadata)
- Claude (enabled flag, hooks, settings, env, plugins)
- Env (files, vars)
- Files (mappings)
- SetupDir override

**Instance-Level** (`InstanceConfig`):
- Claude (hooks, settings, env)
- Env (files, vars)
- Files (mappings)

**Not mergeable at repo level:**
- Marketplaces (workspace-wide only, line 24 in config.go)
- Sources, Groups, Content definitions (workspace-level definitions)

### Personal Config Implication: Scope Selection

For a personal config to work, niwa must identify **which workspace section to apply**. Currently, niwa determines this by:
1. Finding the workspace root (`.niwa/workspace.toml` defines `workspace.name`)
2. Storing that name in instance state (`ConfigName` field)
3. Using the name to select rules from the workspace config

A personal config layer needs a **matching mechanism** to know which per-workspace overrides to apply. Options:
- Match by workspace name (`workspace.name` field in workspace.toml) — simplest, portable
- Match by workspace config source path — ties personal config to specific repos
- Match by instance number — not portable across machines
- Match by instance name — requires instance metadata before personal config is pulled

**The workspace name is the ideal identifier** because:
- It's portable across machines (stored in shared team config)
- It's available immediately (no instance state needed yet)
- It's validated and unique within a workspace root
- Personal config can use workspace.name to select per-workspace sections

## Implications

1. **Personal config schema must be indexed by workspace name** (not instance number or path). The minimal personal config structure should support:
   - Global-level overrides (apply to all workspaces)
   - Named per-workspace sections (apply only when workspace.name matches)

2. **Merge order is critical**: Personal config must be merged AFTER workspace config but BEFORE repo-level overrides. Timeline:
   - Load workspace.toml from team repo
   - Load personal.toml from user's personal repo (keyed by workspace.name)
   - At `niwa apply` time, merge personal config into workspace config
   - Then apply per-repo overrides (existing MergeOverrides logic)

3. **Personal config should reuse existing field types** from `RepoOverride` and `InstanceConfig` (hooks, settings, env, files) to maintain consistent merge semantics. This minimizes new merge logic.

4. **Not all workspace fields are "personal"**: Marketplaces, Sources, Groups, and Content definitions are team-wide. Personal config should likely focus on Claude hooks/settings/env, general env vars, and file mappings — the same fields that are mergeable in RepoOverride.

5. **Storage location**: `~/.config/niwa/personal.toml` (following XDG_CONFIG_HOME pattern) makes it machine-local and portable via dotfile sync, separate from the team workspace repo.

## Surprises

1. **Plugins can be completely replaced per-repo** (not appended): `override.Claude.Plugins != nil` causes workspace plugins to be discarded entirely. This is unusual compared to hooks (which append) or settings (which merge per-key). Personal config design must account for this "replace or inherit" semantics.

2. **Instance overrides exist and use identical merge semantics to repo overrides** (`MergeInstanceOverrides`). This suggests a third layer already exists above repo-level. Personal config sits conceptually between workspace and instance levels, requiring clear ordering.

3. **ConfigName is optional** in InstanceState (pointer type). This appears to support "detached" instances (created outside a named workspace). Personal config discovery must handle instances without a ConfigName gracefully.

4. **Workspace name validation is strict** (alphanumeric + dots/hyphens/underscores only, validated in `validate()` function). This is good for personal config keying but limits how creative teams can be with naming.

## Open Questions

1. **Enabling/disabling personal config per workspace**: Should personal config be opt-in (user explicitly selects which workspace to apply it to) or opt-out (apply to all unless user says no)? Related: should there be a `[workspace.name]` section syntax or flatten all per-workspace overrides at top level?

2. **Conflict resolution semantics**: When both workspace config and personal config define the same hook/setting/env var, what's the merge order? Does personal config always win, or should workspace config be able to set hard constraints that personal config can't override?

3. **Hooks file path resolution**: Personal config hooks will have paths relative to `~/.config/niwa/`. During apply, these paths need to be resolved correctly. Should the personal config hooks materializer special-case the config path, or should all hooks always be relative to `configDir` (potentially a symlink in the workspace)?

4. **Per-workspace hook discovery**: Currently hooks are discovered once per workspace (line 303 in apply.go). Should personal config contribute additional hooks to the discovered set, or replace them? Mixed discovery order?

5. **Plugins conflict**: If workspace defines `plugins = ["a@1.0"]` and personal config defines `plugins = ["b@2.0"]`, does personal config replace workspace plugins entirely, or should there be a way to extend/merge? Current RepoOverride semantics say "replace", but is that the right model for personal config?

## Summary

**Personal config should be a layer between workspace and repo overrides, indexed by workspace name, stored in `~/.config/niwa/personal.toml`, and reusing the merge semantics of RepoOverride for fields like hooks, settings, env, and files.** Niwa identifies which workspace section to apply by reading `workspace.name` from the team workspace.toml at apply time, making the personal config portable across machines and independent of instance state. **Key open question: should personal config override or extend workspace config, and how should conflicts in hooks/plugins/settings be resolved?**
