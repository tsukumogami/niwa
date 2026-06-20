# Findings: niwa Default-Behavior Conventions for Managed Worktree PRD

## Question 1: Default-On Behaviors — How niwa Expresses "Applied Automatically"

### Key Mechanism: `InstallWorkspaceRootSettings`

**Location:** `internal/workspace/workspace_context.go:242` — `func InstallWorkspaceRootSettings(cfg *config.WorkspaceConfig, configDir, instanceRoot string, repoIndex map[string]string) ([]string, error)`

**How it works:**
- Called in `runPipeline` (step called from both `Create` and `Apply`) at `internal/workspace/apply.go:1252`
- Runs unconditionally on EVERY create and apply — no opt-out flag in the function signature
- Generates `.claude/settings.json` at the instance root with:
  - Hooks (discovered and merged from config)
  - Permissions (from `effective.Claude.Settings`, which applies defaults)
  - Env vars (resolved from workspace config)
  - Plugins (from `effective.Plugins`)
  - Marketplaces (from `effective.Claude.Marketplaces`)

**Pattern for "applied by default":**
- The configuration layer (`[instance]` section in workspace.toml) defines what should be installed via `InstanceConfig.Claude` (a `ClaudeOverride`)
- The applier materializes that config by calling `InstallWorkspaceRootSettings`
- Because `runPipeline` calls it unconditionally, any settings in `[instance]` are "applied by default by niwa apply"

**Supporting code citations:**
- `internal/config/config.go:238–245` — `InstanceConfig` struct defines what can be overridden at the instance level; uses `ClaudeOverride` (not the full `ClaudeConfig`, so workspace-scoped fields like Marketplaces can also exist at workspace level and flow through).
- `internal/workspace/workspace_context.go:243` — `MergeInstanceOverrides(cfg)` is called FIRST in `InstallWorkspaceRootSettings`, merging `cfg.Instance.Claude` into a working copy before materializing hooks/settings/env.

### One-Time Notices: Visible Surface for Defaults

**Location:** `docs/guides/one-time-notices.md` + `internal/workspace/apply.go` (multiple notice keys like `noticeProviderShadow`, `noticeConfigConverted`, `NoticeIDRank2TeamConfig`)

**Pattern:**
- When a default behavior is disclosed to the user (e.g., workspace overlay discovered, global config snapshot converted, rank-2 deprecation), niwa emits a `note:` line via `a.Reporter.Log()`.
- The notice is recorded in `InstanceState.DisclosedNotices` after first emission (lines 317–321 in apply.go for example).
- On subsequent applies, `noticeDisclosed(opts.existingState, key)` returns true, so the notice is skipped (one-time behavior).

**Key insight for PRD:**
- niwa already has an established pattern for "surfacing what it did" — one-time notices appear on stderr during `niwa apply` output.
- Precedent for visibility: lines 317–321 show `a.Reporter.Log("note: ...")` for config-converted, lines 326–336 show emit for rank-2 team config.
- Workspace-level state tracking: workspace root carries shared notices in `LoadState(workspaceRoot)` (line 417), so notices can fire once per workspace even though instances are created separately.

---

## Question 2: Opt-Out / Escape Hatches — Idiomatic Toggle Patterns

### Instance State Opt-Out Fields

**Location:** `internal/workspace/state.go:97–99`

```go
SkipGlobal     bool    `json:"skip_global,omitempty"`
OverlayURL     string  `json:"overlay_url,omitempty"`
NoOverlay      bool    `json:"no_overlay,omitempty"`
```

These are persisted as `InstanceState` fields (JSON in `.niwa/instance.json`), **not** `[instance]` section in workspace.toml. They are instance-level state, not config.

**How they're set:**

1. **`--skip-global` flag at init time:** `internal/cli/init.go:46` — `initCmd.Flags().BoolVar(&initSkipGlobal, "skip-global", false, "disable global config overlay for this instance")`
   - Stored in init-time state by `niwa init --skip-global` and carried into every apply (line 301 in apply.go)
   - Read by `runPipeline` at line 308: `skipGlobal: initSkipGlobal,`
   - Effect: personal global config NOT synced or applied (line 647 in apply.go: `if a.GlobalConfigDir != "" && !opts.skipGlobal`)

2. **`--no-overlay` flag at init time:** `internal/cli/init.go:48` — `initCmd.Flags().BoolVar(&initNoOverlay, "no-overlay", false, "disable overlay discovery and association for this workspace")`
   - Stored in init-time state, read by every apply
   - Effect: workspace overlay NOT discovered or synced (line 755 in apply.go: `if !opts.noOverlay`)

**Pattern for escape hatches:**
- niwa doesn't expose per-apply flags (`niwa apply --skip-global`) — once set at init, the opt-out persists
- Stored as `bool` fields in instance.json, not as config entries
- This is the idiomatic niwa pattern: instance-level toggles are state, not declarative config

### Why Not `[instance]` Section in workspace.toml?

The `[instance]` section in workspace.toml (`InstanceConfig`, line 238 in config.go) is for DECLARATIVE OVERRIDES:
- `[instance.claude]` — hooks, settings, env, plugins (per-instance Claude config overrides)
- `[instance.env]` — env vars
- `[instance.files]` — managed files

These flow through `MergeInstanceOverrides` and become part of `.claude/settings.json`. They are NOT for opt-out toggles because:
1. They are per-REPO overrides (they fit the same merge model as `[repos.<name>.claude]`)
2. They materialize into output (settings.json, env), not control flow
3. Toggles that suppress features (skip-global, no-overlay) are orthogonal to config merging

**Summary:** Opt-outs are expressed as `bool` flags at init time, persisted as `InstanceState` fields, and carried forward on every apply. There is no declarative toggle in `[instance]` for these because they control apply logic, not config content.

---

## Question 3: Visibility of Behavior to the Developer

### Existing Precedent: One-Time Notices on stderr

**Documented in:** `docs/guides/one-time-notices.md`

**Pattern in apply.go:**
- Line 317–321: config converted notice (once per workspace)
- Line 326–336: rank-2 team config deprecation notice (once per workspace, triggers niwa plugin auto-install)
- Line 448–455: config converted notice (again on Apply, same workspace-level deduplication)
- Line 461–475: rank-2 overlay notice (once per workspace)
- Line 1061–1071: provider shadow notice (once per workspace)

**Key detail:** All one-time notices are WORKSPACE-LEVEL (persisted in workspace-root instance.json via `saveWorkspaceRootDisclosures` at line 346), not instance-level. This allows the notice to fire once even though multiple instances exist.

### Audit Surfaces

niwa already surfaces several behaviors on every apply (not one-time):

1. **Rotation notices** (line 483): `emitRotatedFiles` emits `rotated <path>` for managed files whose vault sources flipped
2. **Deferred warnings** (lines 374–376, 549–552): warnings about drift, sync issues, missing secrets appear on every run
3. **Credential audit lines** (line 1027): `credentialPool.AuditLog().EmitR12Lines(a.Reporter)` emits auth-source decisions on every apply

### Recommendation for "Steer-to-Niwa-Fallback" Visibility

Based on precedent:
- If the fallback is a **one-time disclosure** (e.g., "this workspace detected no managed worktree and will use the fallback for instance-root settings"), emit it once per workspace via `one-time notices` (add a notice key constant, check before emit, append to `newDisclosures`)
- If it's a **behavioral change that happens every time** (e.g., "falling back to instance-root defaults because no managed worktree exists"), emit it as part of the apply output via `a.Reporter.Log()` or `a.Reporter.Warn()` on every run
- If it's **purely informational and never changes** (e.g., "managed worktree unavailable for repos in this group, using workspace-root settings"), emit it once via the one-time notice pattern

**Precedent suggests:** Invisible fallback (no notice) is acceptable IF the behavior is deterministic and doesn't require user action. Visible (one-time notice) is idiomatic IF the fallback is a departure from the expected norm or requires awareness.

---

## Implications for PRD Requirements

1. **Default-On Mechanism:**
   - The PRD should state: "When no managed worktree is available for a repo in a specific group, niwa apply automatically installs workspace-root settings (env, hooks, settings, plugins) into the instance root and applies them to workers in that group."
   - Cite: `InstallWorkspaceRootSettings` is called unconditionally in `runPipeline`, same as all other default content install steps.
   - This is consistent with "applied by default by niwa apply" — no opt-in required, always happens.

2. **Opt-Out:**
   - Instance-level opt-outs should NOT be added to `[instance]` section in workspace.toml.
   - If a developer wants to suppress the fallback for a specific instance, it should be:
     - A command-line flag at `niwa init` time (e.g., `--no-instance-root-fallback`), or
     - Declaratively set in `.niwa/instance.json` manually (not recommended) or via a future `niwa config set instance-flags` command
   - Parallel existing patterns: `--skip-global`, `--no-overlay` are init-time flags that persist.

3. **Visibility:**
   - Whether the fallback is visible or invisible should be decided in the PRD based on:
     - Is this the expected behavior (invisible), or a change from prior releases (visible one-time notice)?
     - Does the developer need to know it happened to understand why workspace-root settings apply to their repo? (If yes → visible)
   - Precedent: rank-2 deprecation notice is visible because it signals an upgrade path; overlay discovery is silent when successful (no notice).

---

## Summary

1. **Default behaviors are installed by unconditional calls in `runPipeline`** (e.g., `InstallWorkspaceRootSettings`), not by conditional logic. Applying to "all instances on every apply" is niwa's standard.
2. **Opt-outs are instance-level state flags** (`SkipGlobal`, `NoOverlay` in `InstanceState`), set at init time and persisted in `.niwa/instance.json`, not in workspace.toml `[instance]` section (which is for declarative config merges, not toggles).
3. **Visibility follows the one-time notice pattern** (documented in `docs/guides/one-time-notices.md`): emit once per workspace, record in `InstanceState.DisclosedNotices`, skip on subsequent applies. Precedent shows this for deprecations and informational disclosures; silent fallback is acceptable for expected behavior.
