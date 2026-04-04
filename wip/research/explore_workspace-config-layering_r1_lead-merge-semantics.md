# Lead: Merge semantics for personal config overlay

## Findings

### 1. Current merge architecture (override.go, lines 27-122)

The workspace already implements a two-layer merge system via `MergeOverrides(ws, repoName)`:
- **Base layer**: workspace-level defaults from `WorkspaceConfig` fields
- **Override layer**: per-repo overrides from `[repos.{name}]` in workspace.toml

Key function signature (internal/workspace/override.go:27):
```go
func MergeOverrides(ws *config.WorkspaceConfig, repoName string) EffectiveConfig
```

This produces `EffectiveConfig` containing the merged result (lines 13-18).

### 2. Per-field merge semantics (override.go)

The current implementation defines explicit merge rules:

**Settings (scalar/map, per-key win):**
- Lines 54-59: repo values override workspace values per key
- Test: `TestMergeOverridesSettingsWin` (override_test.go:147-168) confirms unset keys are preserved

**Hooks (lists, concatenation/extension):**
- Lines 62-67: repo hook entries are **appended** to workspace entries for the same event key
- Test: `TestMergeOverridesHooksExtend` (override_test.go:227-259) shows `[ws-gate.sh, repo-gate.sh]`
- New event keys from repo are added independently

**Claude Env Promote (lists, union):**
- Lines 70-80: repo promote values are unioned with workspace (duplicates excluded)
- Test: `TestMergeOverridesClaudeEnvPromoteUnion` (override_test.go:320-348)

**Claude Env Vars (map, per-key win):**
- Lines 83-88: repo values override workspace values per key
- Test: `TestMergeOverridesClaudeEnvVarsWin` (override_test.go:288-318)

**Env Files (lists, appended):**
- Lines 97-99: repo env files are **appended** to workspace files (order preserved: workspace first)
- Test: `TestMergeOverridesEnvFilesAppend` (override_test.go:170-187) shows `[ws.env, repo.env]`

**Env Vars (map, per-key win):**
- Lines 102-107: repo values override workspace values per key
- Test: `TestMergeOverridesEnvVarsMerge` (override_test.go:205-225)

**Files (managed files map, per-key with removal):**
- Lines 110-119: repo values override workspace per key; empty string (`""`) **deletes** the workspace mapping
- Tests: `TestMergeOverridesFilesOverride` (493-519), `TestMergeOverridesFilesRemoval` (521-543)

**Plugins:**
- Lines 91-93: repo plugins **replace** workspace plugins entirely (nil = inherit workspace)
- If repo sets `plugins`, it completely overrides; no merging

### 3. Instance-level overrides (override.go:127-212)

`MergeInstanceOverrides(ws)` applies identical semantics to `[instance]` overrides, which apply to the instance root directory (above all repos). Same merge rules apply.

### 4. Fields that are NOT currently overridable

**Workspace-level only (no per-repo override support):**
- `Sources` (GitHub org discovery) — defined at WorkspaceConfig top level only
- `Groups` (repo classification) — top level only
- `Content` (CLAUDE.md source paths) — top level only; Content.Repos sub-entries exist but are **not merged** with RepoOverride
- `Marketplaces` — explicitly marked "workspace-wide. Not merged from per-repo overrides." (config.go:23)
- `WorkspaceMeta` (workspace.name, version, default_branch, etc.) — immutable top-level metadata

**Per-repo fields that are NOT ConfigMergeable:**
- `URL`, `Group`, `Branch`, `Scope`, `SetupDir` — these are repo metadata overrides (clone URL, group assignment, branch) not configuration merges

### 5. EffectiveConfig composition (override.go:13-18)

The merged result is a flattened structure:
```go
type EffectiveConfig struct {
    Claude  config.ClaudeConfig
    Env     config.EnvConfig
    Files   map[string]string
    Plugins []string
}
```

This becomes the input to materializers (apply.go:321, `MergeOverrides` call inside the repo loop).

### 6. Content/CLAUDE.md handling (content.go)

**Content sources are NOT currently merged via merge semantics:**
- Workspace-level source: `Content.Workspace.Source` (config.go:128)
- Group-level sources: `Content.Groups[groupName].Source` (config.go:129)
- Repo-level sources: `Content.Repos[repoName].Source` (config.go:130)

These are **hierarchical selection** (repo-specific > group > workspace fallback) NOT merged. RepoOverride has no Content field (only Repos[name] which is metadata). Personal config would need explicit handling.

### 7. What copy semantics are used

All deep copies (lines 289-349) ensure mutations don't affect workspace-level config:
- `copyHooks()`: deep copy of HooksConfig (list of lists)
- `copySettings()`: shallow copy of SettingsConfig map
- `copyEnv()`: deep copy of EnvConfig (Files list + Vars map)
- `copyClaudeEnv()`: deep copy with deduplicated Promote union

Appending creates new slices; maps are shallow-copied but values are immutable strings.

## Implications

### For personal config overlay design:

1. **List fields (hooks, env files) should append, not replace** — consistent with current workspace→repo semantics. A personal config adding hooks extends the chain: `[workspace hooks] → [personal hooks] → [repo hooks]`.

2. **Map/scalar fields (settings, env vars) should use per-key win with complete inheritance** — personal layer wins on conflict but doesn't delete workspace values. Workspace settings not overridden by personal are preserved.

3. **Plugins field needs special handling** — currently an all-or-nothing replacement. Personal plugins likely should extend (union) not replace, but current semantics support replacement. Decision needed.

4. **Marketplaces must be workspace-only** — comment on line 23 of config.go explicitly blocks merging. Personal config cannot override marketplace configuration.

5. **Content sources are not currently merged** — CLAUDE.md sources use hierarchical selection, not merging. Personal CLAUDE.md content likely needs a new semantics (e.g., personal prepend, or separate personal-only content block).

6. **Files field supports removal via empty string** — enables personal config to suppress workspace file distributions if needed (set to `""`).

7. **InstanceConfig already demonstrates "third layer" pattern** — workspace + instance overrides exist. Personal config could follow this same structure: workspace → instance → personal (or workspace → personal → instance depending on precedence desire).

### For merge direction (personal → workspace precedence):

The current repo override layer already establishes: **repo wins on per-key conflicts for maps, appends for lists**. Personal config should follow the same rule relative to workspace, making it a true "higher priority" layer. This supports portable personal preferences overriding shared config.

## Surprises

1. **Plugins are all-or-nothing, not per-entry** — unlike hooks (which are per-event, allowing per-event additions), plugins is a single list that's completely replaced if set at repo level. This may need rethinking if personal + workspace plugins should merge.

2. **Content sources have no merge semantics** — the only "content" that applies is a single source per level (workspace/group/repo). Personal CLAUDE.md content would need explicit design; it's not covered by the current merge infrastructure.

3. **Enabled flag is boolean, not mergeable** — `claude.enabled` is a single bool that only appears on RepoOverride (line 21 of config.go), not on workspace-level Claude config. Disabling Claude at the repo level is a binary choice, not mergeable with personal preference.

4. **SetupDir is nullable on RepoOverride** — it's `*string` (line 123 of config.go), suggesting it can be explicitly cleared, but there's no merge function for it. Only used for repo-specific resolution, not in EffectiveConfig merge.

## Open Questions

1. **Should personal config be registered per-workspace or globally?** Current lead says "registered once per machine, stored in `~/.config/niwa/config.toml`" but also mentions "per-workspace personal overrides". Does a personal config repo hold both global settings + per-workspace sections? How does niwa identify the current workspace to select the right section?

2. **How should personal CLAUDE.md content layer work?** Content sources are not currently merged. Should personal config add a new content source that's prepended? Or a separate `[personal.content]` section? Or append to the content hierarchy?

3. **Should personal config use the same schema as workspace config's `[repos]` section?** I.e., `[personal.repos.{name}]` with the same fields, or a flatter `[personal]` that applies to all repos?

4. **What about global personal settings that apply across all workspaces?** The lead mentions "global personal preferences + per-workspace personal overrides" but the current merge infrastructure is per-repo. Is there a workspace-level personal override layer too?

5. **Should personal config be able to override `Sources`, `Groups`, or other workspace-only fields?** Currently not in scope, but worth confirming: personal config should probably be limited to per-repo/instance overrides (Claude config, Env, Files) not structural changes.

6. **How to handle plugin merging?** Should personal + workspace plugins be unioned (like promote lists), or should personal completely replace (current repo-level semantics)? Depends on use case (shared plugins vs personal tool preferences).

## Summary

The workspace already implements a two-layer merge system (workspace + per-repo overrides) with distinct semantics per field type: maps use per-key wins, lists append, and special cases like plugins replace entirely. A personal config overlay should follow these same semantics relative to the workspace layer, establishing: workspace (base) → personal (additive/overriding) → per-repo (further overriding). Content sources currently use hierarchical selection not merging and would need new semantics. Plugins are all-or-nothing replacements and may need rethinking if personal + workspace should both apply.
