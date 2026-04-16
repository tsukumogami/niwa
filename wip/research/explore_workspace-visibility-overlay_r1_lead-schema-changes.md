# Lead: Minimal schema changes for private extension

## Findings

### Registry Entry Stores the Source URL
When `niwa init --from org/repo-name` runs, the global registry (~/.config/niwa/config.toml) stores a `RegistryEntry` with:
- `Root`: absolute path to the workspace root directory
- `Source`: the original source URL (org/repo-name format or GitHub HTTPS URL)

This Source URL is the **only persistent reference** to where the workspace config came from. At apply time (lines 156-164 in init.go), the entry is populated with `entry.Source = source` before writing to the registry. The Source field is then retrieved during `create` command (line 86 in create.go: `configPath = entry.Source`).

**Critical insight**: The Source URL is already tracked per workspace at the registry level. This means niwa has all the information needed to derive the private companion repo name without any schema change to workspace.toml.

### Workspace Config Schema (Current)
The `WorkspaceConfig` struct in config.go lines 62-74 contains:
- `Workspace WorkspaceMeta` (name, version, default_branch, content_dir, setup_dir)
- `Sources []SourceConfig` (org discovery sources)
- `Groups map[string]GroupConfig` (visibility/repo classification)
- `Repos map[string]RepoOverride` (explicit repos with URL + group)
- `Content`, `Claude`, `Env`, `Files`, `Instance`, `Channels` (various config sections)

The `SourceConfig` (lines 100-105) is purely for **discovering repos in a GitHub org**, not for tracking where the config itself came from. It has no URL field, only `Org`, `Repos`, and `MaxRepos`.

### How Niwa Knows the Source at Apply Time
During the apply pipeline (workspace/apply.go):
1. `runApply` loads the config from `configPath` (discovered or from registry)
2. `configDir` is derived as `filepath.Dir(configPath)` (line 83 in apply.go)
3. At line 84, `workspace.SyncConfigDir(configDir, applyAllowDirty)` is called but this only works with git (checks for .git); it does not read or use the Source URL stored in registry

**Gap**: The configDir is passed through the entire apply pipeline (classify, clone, materialize) but niwa never reads back the Source URL from the registry during apply. This is a missed opportunity.

### What Information Would a Convention Need
To look for `owner/dot-niwa-private` when processing `owner/dot-niwa`:
1. Extract `owner` and `repo-name` from the Source URL in the registry
2. Construct `owner/dot-niwa-private` 
3. Clone that repo as a sibling or overlay during apply

The schema change required depends on the discovery mechanism chosen:

**Option A (Pure Convention / Zero Config)**
- No schema change to workspace.toml
- niwa reads registry entry's Source URL at apply time
- Derives private companion by pattern: extract owner, suffix `-private`
- Limitation: No way to opt out without a new flag `--skip-private-extension`
- Limitation: Assumes all teams want this behavior; breaks convention for teams that don't use private companions

**Option B (Explicit Field)**
- Add optional field to workspace.toml: `private_extension = "org/repo-name"`
- niwa clones and loads this overlay during apply
- Explicit, opt-in only
- Schema change: Add one line to `WorkspaceMeta` struct

**Option C (Opt-Out Convention)**
- No schema change for happy path (convention enabled by default)
- Add optional field to disable: `private_extension = false`
- Or explicit opt-in variant: `private_extension = true` (default false)
- More surprising to users who don't know about the feature

### Private Extension Format
The private companion repo only needs to define:
- `[sources]` (additional orgs/repos for private discovery)
- `[groups]` (can extend or override group visibility)
- `[repos]` (explicit repo overrides)
- `[content.repos]`, `[content.groups]` (additional content)
- Possibly `[claude]`, `[env]` for private-specific settings

It should **not** redefine workspace metadata (name, version, etc.) or global settings that belong in the public config. The format would be a subset of workspace.toml, not a full duplicate.

### Merge Semantics
If a private extension is loaded:
1. Parse both public (workspace.toml) and private (workspace-private.toml or from companion repo)
2. Merge at the field level:
   - Sources: append private sources to public sources
   - Groups: extend (new groups from private, but can't remove public)
   - Repos: extend (new repo overrides or enhancements)
   - Content: extend (new content sources)
   - Claude/Env: allow private overrides for global settings

This mirrors how global config overrides already merge (see MergeGlobalOverride in workspace/override.go).

## Implications

**Option A (Pure Convention)** is the most seamless for teams with `owner/dot-niwa` + `owner/dot-niwa-private` repos. It requires:
- Reading the Source URL from registry during apply (new code path)
- No workspace.toml changes
- A flag `--skip-private-extension` to opt out for the few teams that don't want it

**Option B (Explicit Field)** is the safest and most auditable. It requires:
- Adding one field to `WorkspaceMeta`: `private_extension = ""`
- Parsing and loading the private extension during apply
- No unexpected behavior for teams that don't use it

**Option C (Opt-Out)** splits the difference but risks surprising behavior. Teams that don't want a private companion would need to explicitly disable it.

The tradeoff is: **convention vs. clarity**. Option A gives zero-config magic (good UX for adopters), Option B gives explicit intent (good for compliance and debugging).

## Surprises

1. **The Source URL is already tracked** at the registry level (RegistryEntry.Source), not in workspace.toml. This decouples the config file from knowledge of its origin, but it also means that pure convention lookup requires registry access at apply time.

2. **There's no persistent source URL in workspace.toml itself.** A workspace.toml deployed to a non-GitHub source (local file, git ssh://, etc.) has no way to know where to find the private companion without explicit configuration. This argues slightly for Option B.

3. **Global config overrides (niwa.toml) already handle merging** for some fields (Hooks, Settings, Env, Files), so the pattern for merging private extensions is already established in the codebase (see MergeGlobalOverride in workspace/override.go, lines 213-227).

4. **The apply pipeline passes configDir through every step** but never passes or reads back the Source URL. Adding that would require threading a new parameter or reading from registry again (expensive).

## Open Questions

1. **Where should the private extension live?** Inside the same `owner/dot-niwa-private` repo as a separate file (e.g., `workspace-private.toml`)? Or should it be loaded from the repo's .niwa/ directory structure?

2. **Should the private extension be cloned into the workspace?** Or loaded on-demand during apply? Cloning adds storage; on-demand requires network access on every apply.

3. **If a private companion doesn't exist, should apply fail or succeed silently?** Option A (pure convention) would need a flag to control strictness.

4. **How do we handle the case where a workspace is used in multiple places** (deployed in different orgs, or cloned to local)? The registry-based Source URL discovery only works for the original registration.

5. **Should private extensions support the same materializers as public config?** (hooks, settings, env, files). Probably yes, but need to clarify merge order.

## Summary

niwa already stores the workspace's source repo URL in the global registry (RegistryEntry.Source), making pure convention feasible without workspace.toml changes. However, convention requires reading the registry during apply, whereas an explicit field in workspace.toml (Option B) makes the intent portable and auditable regardless of where the config is deployed. The minimal schema change is one optional field (`private_extension = "org/repo-name"`) in `[workspace]` section. Merge semantics should extend (not override) public groups and repos, mirroring the existing global config override pattern. The private companion format is a subset of workspace.toml containing only sources, groups, repos, and content—no workspace metadata redefinition.

