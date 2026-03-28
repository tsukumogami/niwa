# Decision 2: Typed Config Structs for Hooks, Settings, and Env

## Status: implemented

## Question

How should typed config structs replace the `map[string]any` placeholders in
`WorkspaceConfig` and `RepoOverride`?

## Chosen Approach

Three new types in `internal/config/config.go`:

- **`HooksConfig`** (`map[string][]string`): maps hook event names to script path lists.
- **`SettingsConfig`** (`map[string]string`): maps setting keys to string values.
- **`EnvConfig`** (struct with `Files []string` and `Vars map[string]string`): replaces the flat `map[string]any` that previously used a magic `"files"` key.

Both `WorkspaceConfig` and `RepoOverride` now use these types instead of
`map[string]any`. `EffectiveConfig` in `override.go` mirrors the same types.

## Changes Made

### `internal/config/config.go`
- Added `HooksConfig`, `SettingsConfig`, `EnvConfig` type definitions.
- Updated `WorkspaceConfig` fields: `Hooks`, `Settings`, `Env` use typed structs.
- Updated `RepoOverride` fields: same typed structs.
- Removed "placeholder" comments from Hooks, Settings, Env (Channels remains a placeholder).

### `internal/workspace/override.go`
- `EffectiveConfig` uses the new typed fields.
- `MergeOverrides` rewritten with typed merge logic:
  - Settings: repo wins per key (same semantics, now type-safe).
  - Env files: repo files appended after workspace files (was a magic `"files"` key check, now `append(result.Env.Files, ...)`).
  - Env vars: repo wins per key (new explicit field, previously lumped into the generic map).
  - Hooks: lists concatenated per event (same semantics, now `[]string` instead of `[]any`).
- Replaced `copyMap` with `copyHooks`, `copySettings`, `copyEnv` for proper deep copying.
- Removed `appendSliceValues` and `toSlice` helpers (no longer needed).
- Added `slices` import for `slices.Clone`.

### `internal/workspace/override_test.go`
- All tests rewritten to construct typed structs instead of `map[string]any`.
- Replaced `TestMergeOverridesEnvNonFilesWin` with `TestMergeOverridesEnvVarsWin` and `TestMergeOverridesEnvVarsMerge` covering the new `Vars` field.
- Added `TestMergeOverridesEnvMutationSafety` for env-specific mutation safety.
- Removed type assertions (`.([]any)`) since fields are now statically typed.

### `internal/config/config_test.go`
- `fullConfig` TOML updated with `vars = { LOG_LEVEL = "debug" }`.
- Assertions updated to check typed fields directly.
- Added assertion for `env.vars.LOG_LEVEL`.

## Key Design Decisions

1. **`HooksConfig` as named map type** rather than a struct with fixed fields. Hook event names are open-ended (any event Claude Code supports), so a map is appropriate. The value type `[]string` gives type safety without limiting which events exist.

2. **`SettingsConfig` as `map[string]string`** rather than a struct with a `Permissions` field. Settings will grow over time; a map keeps forward compatibility without requiring struct changes for each new setting.

3. **`EnvConfig` as a struct** with `Files` and `Vars` fields. Unlike hooks and settings, env has two structurally different sub-fields. A struct makes the TOML mapping natural (`[env]\nfiles = [...]\nvars = { ... }`) and eliminates the previous magic `"files"` key detection in merge logic.

4. **Deep copy for hooks** uses `slices.Clone` per event to prevent append mutation of workspace-level slices during merge. The mutation safety test verifies this.
