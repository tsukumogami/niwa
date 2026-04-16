# Phase 2 Research: Architecture Perspective

## Lead: Discovery Mechanism and Architecture Requirements

### Findings

**GlobalOverride cannot serve private extensions (by design):**
`GlobalOverride` struct (`internal/config/config.go`) intentionally excludes `Sources`, `Groups`, `Repos`, and `Content` — these fields have no meaning in an override-only layer. A private extension that adds hidden repos requires a new, separate type with union semantics. This is a fundamental architectural constraint, not a gap.

**Option A (pure convention) is blocked by registry architecture:**
`RegistryEntry.Source` stores the workspace config repo URL in `~/.config/niwa/config.toml`. While it's technically possible to derive `<source-repo>-private` from this URL, the registry is not accessible during the apply pipeline's `runPipeline` function — the pipeline receives `configDir` (a filesystem path), not the source URL. Threading the source URL through would require refactoring the pipeline signature. Additionally, manually-cloned `.niwa/` directories (without `niwa init --from`) have no Source URL in the registry — pure convention would silently do nothing for these workspaces.

**Option B aligns with the established GlobalOverride pattern:**
`GlobalOverride` uses an explicit registration pattern: `niwa config set global <repo>` stores the URL in `~/.config/niwa/config.toml` under `[global_config]` (a `GlobalConfigSource` struct); the local path is derived at runtime as `filepath.Join(xdgConfigHome, "niwa", "global")`. This explicit registration:
- Is portable — works regardless of how the workspace was initialized
- Uses `0o600` file permissions for the config file
- Decouples the registered URL from the derived local path (no stale-path attacks)
- Follows the principle in `DESIGN-global-config.md`: "users must run `niwa config set global` explicitly"

**Recommended registration pattern for private companion:**
- `niwa config set private <repo>` stores URL in `~/.config/niwa/config.toml` under `[private_workspace]`
- Local path derived at runtime: `filepath.Join(xdgConfigHome, "niwa", "private")`
- Sync via existing `SyncConfigDir()` — already handles git pull + dirty-state checks
- Parse new `PrivateWorkspaceExtension` struct from the cloned directory
- Merge step inserted after workspace config load, before GlobalOverride in `runPipeline`

**Why not Option C (opt-out by default):**
`DESIGN-global-config.md` explicitly rejected implicit defaults for GlobalOverride registration — the feature is "completely inert when not configured." The same principle applies: auto-discovering a `<config-repo>-private` companion without explicit registration surprises users who don't know the feature exists and creates unexpected behavior for teams that coincidentally have a `-private` repo without intending it as a niwa companion.

**Storage location:**
Machine-scoped (`~/.config/niwa/private/`) mirrors GlobalOverride storage, provides consistent `0o600` security, and works across all instances of the same workspace. Workspace-scoped storage (`.niwa/.private-workspace/`) would require protecting `.niwa/` and would not be shared across workspace instances.

**CLAUDE.private.md injection pattern:**
`InstallGlobalClaudeContent()` in `workspace_context.go` copies `CLAUDE.global.md` and injects `@CLAUDE.global.md` into the workspace CLAUDE.md via `ensureImportInCLAUDE()` (idempotent). A new `InstallPrivateClaudeContent(privateConfigDir, instanceRoot)` function would follow the same pattern. Import order in CLAUDE.md: workspace → private → global (increasing specificity and privacy level).

**Pipeline insertion point:**
After workspace config sync (step 2), before GlobalOverride (step 2a in DESIGN-global-config.md):
```
2. Sync workspace config
2a. [NEW] If private registered + !SkipPrivate: SyncConfigDir(privateConfigDir)
2b. If sync fails + never previously cloned: silent skip
2c. If sync fails + previously cloned: abort with error
3. Load WorkspaceConfig
3a. [NEW] If private registered + !SkipPrivate: parse PrivateWorkspaceExtension
3b. [NEW] MergePrivateExtension(ws, private) → intermediate
4. [EXISTING] If global registered + !SkipGlobal: MergeGlobalOverride(intermediate, global)
```

### Recommendation

**Option B (explicit field in workspace.toml) is NOT recommended over registration-only.** The key insight: `[workspace] private_extension = "org/repo"` adds the companion's existence to the public config. This doesn't leak what the companion *contains*, but it does say "there is a private companion" — a disclosure some teams may want to avoid. A workspace.toml field is also unnecessary because the user already runs `niwa config set private <repo>` to register the companion on their machine; the workspace.toml doesn't need to reference it.

**Actual recommendation: Registration-only, no workspace.toml field.**
- `niwa config set private <repo>` — machine-level registration, no workspace.toml change required
- The companion is silently ignored if not registered or not accessible
- Teams that want to document the companion's existence can add a comment to workspace.toml, but no field is required
- This mirrors how GlobalOverride works today

If portability across machines is required (e.g., team documentation), an *optional* `private_extension_hint = "org/repo"` field in workspace.toml could serve as non-operative documentation only — niwa reads it as a suggestion but only activates if the user has registered the companion. But this optional field should be in the PRD as an open question.

### Implications for Requirements

- R: `niwa config set private <repo>` — registers the private companion (clones to `~/.config/niwa/private/`)
- R: `niwa config unset private` — unregisters and removes the local clone
- R: `niwa init --skip-private` — stores `SkipPrivate: true` in `.niwa/instance.json`
- R: Private companion sync failure when no prior successful clone → silent skip (no error, no warning)
- R: Private companion sync failure when prior successful clone exists → abort apply with error
- R: `CLAUDE.private.md` in companion directory → copied to instance root + `@CLAUDE.private.md` injected into workspace CLAUDE.md
- R: Import order in workspace CLAUDE.md: workspace context → private context → global context
- R: Private companion parsing must validate Files and Env.Files paths using same rules as GlobalOverride

### Open Questions

- Should an optional `private_extension_hint` field in workspace.toml serve as non-operative documentation of the companion's location? (Pros: helps new team members know to register; Cons: slightly discloses companion existence in public config)
- Should `niwa status` show whether a private companion is registered and its sync state? (Leaks companion existence to status output observers — security vs UX tradeoff)

---

## Summary

The architecture analysis strongly favors registration-only (no workspace.toml field) for the private companion — the established GlobalOverride pattern uses explicit `niwa config set` commands, and adding a workspace.toml field would disclose the companion's existence in the public config. The private companion follows the GlobalOverride pattern exactly: machine-level registration in `~/.config/niwa/config.toml`, clone to `~/.config/niwa/private/`, sync via existing `SyncConfigDir()`, merge in the pipeline between workspace load and GlobalOverride. The critical new behavior is graceful degradation: silent skip on first-time clone failure (the GlobalOverride model uses fatal-on-sync-failure, which must be changed for the private companion).
