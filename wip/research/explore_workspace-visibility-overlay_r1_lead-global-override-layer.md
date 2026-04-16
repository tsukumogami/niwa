# Lead: GlobalOverride layer constraints and opportunities

## Findings

### Type System: GlobalOverride vs Private Extension Needs

**What GlobalOverride Carries:**
- `Claude` (ClaudeOverride): Hooks, Settings, Env, Plugins — only the override-capable fields
- `Env`: Files and Vars for environment configuration
- `Files`: Map of source → destination for managed files

**GlobalOverride explicitly does NOT carry:**
- `Sources` (org/repo discovery sources)
- `Groups` (classification groups)
- `Repos` (individual repo configurations)
- `Content` (CLAUDE.md content sources)
- Workspace metadata (name, version, default_branch)

**Source:** `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-5/public/niwa/internal/config/config.go` lines 311–327.

**Private Extension Problem:**
The private workspace extension needs to *add* new repos and groups that don't exist in the public workspace.toml. GlobalOverride's type definition cannot support this — it has no fields for `Sources`, `Groups`, or `Repos`. This is not an oversight; it's by design (see DESIGN-global-config.md Decision 1, Option A rejected). A private extension **cannot be a GlobalOverride**.

**Implication:** A private extension needs a new type, separate from GlobalOverride. It could mirror `WorkspaceConfig` but contain only the additive fields (Sources, Groups, Repos subset) plus override fields (Claude, Env, Files).

---

### Merge Chain: Where Private Extension Sits

**Current three-layer merge chain (per DESIGN-global-config.md lines 95–102):**
1. Workspace defaults (from workspace.toml)
2. Global override (from global config repo, workspace-specific section)
3. Per-repo overrides (MergeOverrides per repo)

**Code location:** `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-5/public/niwa/internal/workspace/apply.go` lines 213–227:
```go
effectiveCfg := cfg
if a.GlobalConfigDir != "" && !opts.skipGlobal {
    overridePath := filepath.Join(a.GlobalConfigDir, GlobalConfigOverrideFile)
    data, readErr := os.ReadFile(overridePath)
    if readErr == nil {
        globalOverride, parseErr := config.ParseGlobalConfigOverride(data)
        if parseErr != nil {
            return nil, fmt.Errorf("parsing global config override: %w", parseErr)
        }
        resolved := ResolveGlobalOverride(globalOverride, cfg.Workspace.Name)
        effectiveCfg = MergeGlobalOverride(cfg, resolved, a.GlobalConfigDir)
    }
}
```

**For private extension to add repos/groups:**
A private extension layer must sit BEFORE global override in the chain:
1. Workspace defaults
2. **Private extension** (adds Sources, Groups, Repos; may override some fields)
3. Global override (refines, can suppress or override private extension additions)
4. Per-repo overrides

Or, private extension could be a parallel synthesis: load both public and private WorkspaceConfig-like structures, merge them into an intermediate `effectiveCfg`, then apply GlobalOverride on top.

**Challenge:** The current apply pipeline (lines 214–227) does not know how to load a private extension because:
- It assumes a single workspace.toml source
- GlobalOverride.Workspaces is already workspace-scoped; a private extension structure would need similar scoping
- The `cfg` parameter passed to `runPipeline` is already the loaded WorkspaceConfig; inserting a private extension load requires threading it through earlier

---

### Apply Pipeline: Sync and Load Steps

**Current global config sync and load (apply.go, lines 204–227):**
- Step 2a (line 206): SyncConfigDir() pulls the global config repo — reusable as-is
- Steps 3a–3c (lines 215–227): Load and parse GlobalConfigOverride, resolve workspace-scoped section, merge into workspace config

**Reusable components:**
1. `SyncConfigDir(dir, allowDirty)` — handles git pull with dirty-state checks, returns nil for non-git dirs. **Reusable directly for private extension sync.**
2. `Cloner` + `ResolveCloneURL` — used by CLI to clone global config repo. **Reusable for private extension registration.**
3. `ParseGlobalConfigOverride` + `validateGlobalOverridePaths` — validates path safety. **Partially reusable** if private extension has similar safety constraints.

**What private extension needs differently:**
- A private extension sync would also call SyncConfigDir() on a different directory (e.g., `$XDG_CONFIG_HOME/niwa/private-workspace/` or `.niwa/.private-workspace`).
- Load and parse would need to handle a new struct (e.g., `PrivateWorkspaceExtension` with Sources, Groups, Repos, Overrides).
- Merge semantics differ: private extension adds new repos/groups, so the merge must union Sources/Groups/Repos rather than override them.

**Where in apply.go to insert:**
Private extension sync should happen after workspace config sync (line 202) and before global config sync (line 205), since:
- Workspace is the baseline; private extends it
- Global overrides both workspace and private
- Discovery (Step 1) happens first; it consumes the merged Sources list

Or, private extension load happens before workspace config is fully resolved, producing an intermediate merged config that is then passed to MergeGlobalOverride.

---

### Merge Semantics: Sources, Groups, Repos

**GlobalOverride merge semantics (override.go, MergeGlobalOverride function, lines 327–436):**
- Hooks: append after workspace hooks
- Settings: global wins per key
- Env.Promote: union (no entries dropped)
- Env.Vars: global wins per key
- Plugins: union, deduplicated
- Files: global wins per key; empty string suppresses workspace

**For private extension (needed):**
- `Sources`: union — private extension adds new orgs/repos lists, workspace sources are not removed
- `Groups`: merge map — private adds new groups; if workspace defines a group with the same name, collision handling needed
- `Repos`: merge map — private adds new repo overrides; if workspace defines a repo override with the same name, private or workspace wins?

**Question for design:** If the public workspace defines `[[groups.public]]` and the private extension defines `[[groups.public]]`, which wins? The safest default is workspace wins (public takes precedence), but this should be explicit.

---

### Hook Script Path Resolution

**GlobalOverride approach (MergeGlobalOverride, lines 336–357):**
Global hook script paths are resolved to absolute paths using `globalConfigDir` before merging. This allows HooksMaterializer to use a single `ctx.ConfigDir` without knowing which config layer each hook came from.

**Code snippet:**
```go
for event, entries := range g.Claude.Hooks {
    absEntries := make([]config.HookEntry, 0, len(entries))
    for _, e := range entries {
        absScripts := make([]string, 0, len(e.Scripts))
        for _, s := range e.Scripts {
            if filepath.IsAbs(s) {
                absScripts = append(absScripts, s)
            } else {
                absScripts = append(absScripts, filepath.Join(globalConfigDir, s))
            }
        }
        absEntries = append(absEntries, config.HookEntry{
            Matcher: e.Matcher,
            Scripts: absScripts,
        })
    }
    ...
}
```

**For private extension:**
The same pattern applies. If private extension carries hooks (via overrides), their script paths must be resolved to absolute paths using the private extension directory (e.g., `.niwa/.private-workspace`). The materializer's `checkContainment(src, ctx.ConfigDir)` is then skipped for absolute paths (see DESIGN-global-config.md "Hook script source directory resolution").

**Reusability:** The hook path resolution logic in MergeGlobalOverride can be factored and reused for private extension merges.

---

### Content Installation: CLAUDE Files

**GlobalOverride content injection (workspace_context.go, lines 187–213):**
- Copies CLAUDE.global.md from global config directory to instance root
- Adds `@CLAUDE.global.md` import directive to workspace CLAUDE.md
- Uses ensureImportInCLAUDE pattern (idempotent)
- Returns nil when file doesn't exist (optional global Claude instructions)

**Code location:** `InstallGlobalClaudeContent(globalConfigDir, instanceRoot)` calls `ensureImportInCLAUDE` at lines 206–213.

**For private extension:**
If private extension carries CLAUDE.md content, it would need:
- Copy CLAUDE.private.md from private extension directory
- Add import directive to workspace CLAUDE.md
- Same optional-file semantics as global

**Open question:** Should private extension content be imported at the same level as global, or at a different visibility layer? If both global and private CLAUDE content exist, the import order determines inheritance semantics.

---

### Config Directory Registration and Path Derivation

**GlobalOverride registration (per DESIGN-global-config.md, Decision 2):**
- Stored in ~/.config/niwa/config.toml under `[global_config]` section (GlobalConfigSource struct)
- Local path derived at runtime: `filepath.Join(xdgConfigHome, "niwa", "global")`
- Never persisted; prevents stale-path inconsistencies if XDG_CONFIG_HOME changes

**Source:** `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-5/public/niwa/internal/config/registry.go` lines 22–24, 194–210.

**For private extension:**
A private extension could be:
1. **Machine-level (like global):** Registered in ~/.config/niwa/config.toml under `[private_workspace]`, derived to `~/.config/niwa/private-workspace/`. Same security semantics as global — user-owned file with 0o600 permissions.
2. **Workspace-level:** Stored in `.niwa/.private-workspace/` alongside workspace.toml. No registration needed; discovered by convention. Less secure (world-readable if .niwa isn't protected), but simpler for team scenarios where private extension is part of the workspace repository.

**Recommendation:** Machine-level registration mirrors global config and maintains consistent security posture. Workspace-level would be simpler for local-only private extensions (convenience feature).

---

### Instance State and Skip Flags

**GlobalOverride instance control (per DESIGN-global-config.md, Decision 1):**
- `InstanceState.SkipGlobal bool` stored in `.niwa/instance.json` (state.go)
- Checked at apply time; if set, global sync/merge skipped entirely
- Set at init time via `--skip-global` flag

**Source:** `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-5/public/niwa/internal/workspace/state.go` (referenced in apply.go line 133).

**For private extension:**
A similar `SkipPrivate bool` flag would be needed in InstanceState. This allows users to:
- Initialize instances with `--skip-private` to exclude private extensions (e.g., in CI/CD)
- Persist the choice across applies
- Have fine-grained control independent of SkipGlobal

---

### Differences: GlobalOverride is Override-Only, Private Extension Must Add

**GlobalOverride philosophy (DESIGN-global-config.md):**
"GlobalOverride is override-only; Plugins union (not replace) is the correct global-layer merge behavior. Replacing workspace plugins would silently disable team tooling."

GlobalOverride is intentionally defensive — it can only refine workspace config, never remove or hide defined repos/groups. This makes sense for a user-wide layer: global config is trusted to enhance workspace defaults, not contradict them.

**Private extension semantics:**
A private extension for hidden repos violates this philosophy. If private extension adds repos that are *not* in the public workspace.toml, it is not overriding; it is extending the baseline. This is fundamentally different from GlobalOverride.

**Consequence:** A private extension type must explicitly allow additive fields (Sources, Groups, Repos). The merge semantics must be union for discovery sources and group/repo maps, not override.

---

### Test and Build Approach

**GlobalOverride has unit test table-driven patterns (override_test.go):**
- MergeGlobalOverride tests verify each field type (hooks, settings, env, plugins, files)
- Path resolution tests verify absolute path handling
- Workspace-scoped resolution (ResolveGlobalOverride) tested

**Private extension tests would need:**
- New type definition tests (parse, validate, path safety)
- Merge semantics tests for Sources (union), Groups/Repos (collision handling)
- End-to-end apply pipeline tests with both private and public repos
- Interaction tests: private adds a repo, global overrides it, per-repo override further refines

---

## Implications

1. **Type System Decision:** GlobalOverride cannot be reused for private extensions. A new `PrivateWorkspaceExtension` struct is needed, carrying `Sources`, `Groups`, `Repos` (additive), plus override fields. This is a breaking point from the GlobalOverride pattern.

2. **Merge Chain Insertion:** Private extension must sit between workspace and global override. The apply pipeline insert point is `runPipeline` — after loading workspace.toml but before applying GlobalOverride. This requires threading a private extension load step through the pipeline.

3. **Reuse Opportunities:**
   - `SyncConfigDir()` — reusable directly for private extension sync
   - `Cloner` + `ResolveCloneURL` — reusable for registration
   - Hook path resolution logic — can be factored into a helper for both GlobalOverride and private extension
   - `ensureImportInCLAUDE` pattern — reusable for CLAUDE content installation

4. **Code Changes Needed:**
   - New type: `PrivateWorkspaceExtension` (mirrors WorkspaceConfig but additive)
   - New struct in registry.go: `PrivateWorkspaceSource` (like GlobalConfigSource)
   - New function: `MergePrivateExtension(ws, private *PrivateWorkspaceExtension)` — union semantics for Sources/Groups/Repos
   - New function: `ResolvePrivateExtension(private, workspaceName string)` — workspace-scoped merging
   - Modified apply.go: insert private extension load after workspace load, before global override
   - Modified state.go: add `SkipPrivate bool` to InstanceState
   - CLI: add `niwa config set private <repo>` and `niwa config unset private` commands

5. **Access Control Risk:** A private workspace extension with its own repos list can hide repos from public view but expose them to users with access to the private repo. This is the intended behavior, but it means the workspace config is no longer the single source of truth for team visibility.

---

## Surprises

1. **GlobalOverride doesn't support Sources/Groups/Repos at all.** This is deeply intentional (rejected explicitly in design options), making private extension a genuinely new pattern rather than a simple "another layer" addition.

2. **Hook path resolution requires absolute paths for multi-source hooks.** The HooksMaterializer uses `ctx.ConfigDir` to find scripts; having hooks from two repos requires one to use absolute paths. This is handled but is a subtle constraint that must be replicated for private extension.

3. **Global config is machine-level, not workspace-level.** Global config registration stores a single URL in ~/.config/niwa/config.toml, applied to all workspaces. If a user registers a different global config for a different machine, the whole registration changes. A private extension could be workspace-level (in .niwa/) to avoid this, but that sacrifices the security property of user-only file permissions.

4. **The merge order for private extension matters for collision handling.** If both public and private define the same group, the order of union (private-first vs workspace-first) determines precedence. GlobalOverride chose "workspace wins" defensively; private extension needs explicit collision rules.

---

## Open Questions

1. **Private extension registration: machine-level or workspace-level?**
   - Machine-level (like global): More secure (0o600 permissions), consistent with global config, but one registration per user globally.
   - Workspace-level (in .niwa/): Simpler setup, per-workspace private extensions, but requires .niwa/ to be protected.
   - **Recommendation for design input:** Consider a hybrid: machine-level registration of *access* to a private extension repo, but per-workspace *activation* (stored in workspace state or as a convention file).

2. **Collision handling for Groups and Repos:**
   - If private and workspace both define `groups.shared`, should workspace win (for stability), private win (for extension), or error (for safety)?
   - Same question for `repos` map: if both define a repo override for "myrepo", which takes precedence?
   - **Recommendation:** Workspace wins (conservative), but make it configurable via an override flag (e.g., `private_override = true` in [instance]).

3. **CLAUDE.md import order for private and global:**
   - Should private CLAUDE content be imported before or after global?
   - Current order: workspace, then global (workspace-context.md import, then CLAUDE.global.md import). Private would be inserted where?
   - **Recommendation:** Workspace > Private > Global (specificity increasing), but this needs alignment with visibility semantics.

4. **Content sources in private extension:**
   - Can private extension define `[claude.content]` entries for repos it adds? Or only for repos in the public workspace?
   - If private adds a repo, can it also provide CLAUDE.local.md content for that repo?
   - **Recommendation:** Yes, private extension can define content for repos it adds. But content defined in public workspace for a repo should not be overrideable by private (avoid data loss surprises).

5. **Fetch access control for private extension:**
   - The private extension repo is only cloned if the user has permission. How is permission verified? (SSH key? GitHub token?)
   - If fetch fails (access denied), should apply abort (like workspace sync) or degrade gracefully (skip private extension)?
   - **Recommendation:** Abort on fetch failure, same as workspace sync failure. Degraded mode is confusing — better to fail fast.

6. **Interaction with marketplace and plugin sources:**
   - Can private extension define new marketplace sources or plugins?
   - Global config does not handle Marketplaces (DESIGN-global-config.md). Should private extension?
   - **Recommendation:** Private extension should support Plugins (it's in Claude.Plugins union) but leave Marketplaces as workspace-only for now (can be added later).

---

## Summary

**Finding:** GlobalOverride is fundamentally override-only and cannot be extended to support a private extension layer that adds new repos and groups; a new type structure is required. **Implication:** Private extension requires new type definitions (PrivateWorkspaceExtension), new merge functions with union semantics for Sources/Groups/Repos, and insertion into the apply pipeline between workspace and global override. **Open question:** Should private extension be machine-level (like global) or workspace-level, and how should collisions between private and public definitions be resolved (workspace wins conservatively, or private wins to enable extension)?
