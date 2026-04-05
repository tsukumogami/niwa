---
status: Planned
upstream: docs/prds/PRD-global-config.md
problem: |
  The niwa apply pipeline loads a single workspace config and materializes it to
  disk. There is no mechanism to layer a second config source on top, so user-specific
  hooks, env vars, plugins, and Claude instructions cannot be managed by niwa without
  committing them to the shared team workspace repo.
decision: |
  Introduce a GlobalOverride struct (mirroring RepoOverride) parsed from a
  user-owned GitHub-backed TOML file. A new MergeGlobalOverride function applies
  it between workspace defaults and per-repo overrides, producing a three-layer
  chain. Registration and unregistration are handled by a new `niwa config set/unset
  global` command tree. CLAUDE.global.md is injected via the existing @import
  pattern, identical to how workspace-context.md is handled.
rationale: |
  Each decision reused the nearest existing pattern rather than introducing new
  abstractions: GlobalOverride mirrors RepoOverride; MergeGlobalOverride mirrors
  MergeOverrides; the config subcommand tree mirrors the existing cobra structure;
  InstallGlobalClaudeContent mirrors InstallWorkspaceContext. The three-layer merge
  order is made explicit at the call site in apply.go rather than hidden in
  call order. When global config is not registered or the instance was initialized
  with --skip-global, no new code paths execute.
---

# DESIGN: Global config

## Status

Accepted

## Context and Problem Statement

The niwa apply pipeline is structured around a single config source: `.niwa/workspace.toml`, synced from a team-owned GitHub repo. The pipeline loads this config, resolves per-repo overrides with `MergeOverrides()`, and materializes results to disk (hooks, env files, settings, CLAUDE.md content, managed files). The whole pipeline assumes one config source and one merge layer.

To support a global config layer, the pipeline needs a second config source -- a user-owned GitHub repo -- that is synced independently and whose values overlay workspace defaults before per-repo overrides are applied. This is an insertion into the middle of the existing apply pipeline, not an extension of its endpoints.

The affected system boundaries are:

- **Config storage** (`internal/config/registry.go`): `GlobalConfig` needs a new section to hold the global config repo URL and local clone path.
- **Sync** (`internal/workspace/configsync.go`): already stateless and reusable; needs to be called for a second directory.
- **Merge** (`internal/workspace/override.go`): a new intermediate layer (workspace → global → per-repo) requires either a new merge function or a generalised merge chain.
- **Apply** (`internal/cli/apply.go`, `internal/workspace/apply.go`): the orchestration sequence gains a sync step and a config-loading step for global config.
- **Instance state** (`internal/workspace/state.go`): a per-instance `SkipGlobal` flag determines whether apply uses the global layer for that instance.
- **CLI** (`internal/cli/`): a new `niwa config` subcommand handles registration and unregistration; `niwa init` gains `--skip-global`.

## Decision Drivers

- **Reuse over new abstractions.** `SyncConfigDir()` already handles git pull with dirty-state checks. The merge machinery in `override.go` already handles all field types. Both should be extended, not replaced.
- **Consistent error semantics.** Global config sync failure must abort apply, same as workspace config sync failure. No degraded-mode fallback.
- **Zero impact on existing workspaces.** Workspaces with no global config registered, or instances initialized with `--skip-global`, must behave identically to today. The feature is completely inert when not configured.
- **Unambiguous merge precedence.** The three-layer order (workspace defaults → global overrides → per-repo overrides) must be explicit in code, not implicit in call order.
- **Testable opt-out.** The `SkipGlobal` flag in instance state must be inspectable without running apply -- i.e., stored in `.niwa/instance.json`, not inferred from the absence of global config registration.
- **Schema parsability.** Global config TOML must map to a subset of existing config types where possible, to avoid duplicating field definitions and merge logic.

## Considered Options

### Decision 1: Global config type representation and merge chain

The global config TOML file contains a subset of workspace config fields (hooks, env vars, plugins, managed files) in user-wide and per-workspace sections. The merge chain must go workspace → global → per-repo.

Three structural options were evaluated: reuse `WorkspaceConfig` as the global config type, introduce a bounded `GlobalOverride` struct mirroring `RepoOverride`, or generalize to an N-layer `Overrider` interface.

**Key assumptions:**
- The global config file is parsed from a directory cloned by `SyncConfigDir()`, with a known local path at apply time.
- `MergeGlobalOverride` does not handle `Marketplaces` or content sources (excluded by PRD).
- Plugins union (not replace) is the correct global-layer merge behavior.
- `GlobalConfigOverride` lives in `internal/config/` alongside existing types.

#### Chosen: Option B — New `GlobalOverride` struct

Add a `GlobalOverride` struct to `internal/config/` that captures only the fields a global config can set:

```go
type GlobalOverride struct {
    Claude *ClaudeConfig     `toml:"claude,omitempty"`
    Env    EnvConfig         `toml:"env,omitempty"`
    Files  map[string]string `toml:"files,omitempty"`
}

type GlobalConfigOverride struct {
    Global     GlobalOverride            `toml:"global"`
    Workspaces map[string]GlobalOverride `toml:"workspaces"`
}
```

Two new functions in `override.go` implement the three-layer chain:

- `ResolveGlobalOverride(g *config.GlobalConfigOverride, workspaceName string) config.GlobalOverride` — merges the flat `[global]` section with the matching `[workspaces.<name>]` section, with workspace-specific values winning per field.
- `MergeGlobalOverride(ws *config.WorkspaceConfig, g config.GlobalOverride, globalConfigDir string) *config.WorkspaceConfig` — applies the resolved override on top of a `WorkspaceConfig` baseline, resolving global hook script paths to absolute paths, and returning a new intermediate `*WorkspaceConfig` without mutating the original.

The call site in `apply.go` becomes:

```go
intermediate := ws
if globalOverride != nil {
    intermediate = MergeGlobalOverride(ws, resolvedGlobal, globalConfigDir)
}
effective := MergeOverrides(intermediate, cr.Repo.Name)
```

`intermediate` is computed once per apply run, not per repo. When global config is absent or `SkipGlobal` is set, the block is skipped and `MergeOverrides` receives the original `ws` unchanged.

Merge semantics per field type:
- **Hooks**: global appended after workspace hooks.
- **Env files**: global appended after workspace env files.
- **Env vars**: global wins per key (global value takes precedence).
- **Plugins**: union (global adds to workspace list, deduplicated). This differs from `RepoOverride` semantics intentionally -- replacing workspace plugins would silently disable team tooling.
- **Managed files**: global wins per key; empty string suppresses the workspace-managed file.

#### Alternatives Considered

**Option A — Reuse `WorkspaceConfig` as global config type.** `WorkspaceConfig` carries fields that have no meaning in a global override: `Workspace` metadata (name, version, default_branch), `Sources`, `Groups`, `Repos`, `Content`, `Instance`, `Channels`. The parser silently ignores unknown fields; the validate function requires `workspace.name`, so the global config file must include a dummy value or bypass validation. The merge function `MergeGlobal(ws, global *WorkspaceConfig)` is ambiguous about which fields of `global` apply. Option A trades a small type definition for schema confusion and implicit constraints.

**Option C — Generalize merge chain to N layers via `Overrider` interface.** The codebase has two override types and is adding one more. An interface for three concrete types with different fields requires either a narrow shared interface (nearly useless) or type assertions internally. The explicit, per-type merge functions in `override.go` are readable precisely because they name their inputs. Generalization is appropriate at four or more layers with genuinely shared logic, not here.

---

### Decision 2: `niwa config` subcommand structure

The CLI needs two operations: register a global config repo (`niwa config set global <repo>`) and unregister it (`niwa config unset global`). Three structural options were evaluated against the existing cobra codebase conventions.

**Key assumptions:**
- `GlobalConfig` or `GlobalSettings` gains a new field for the repo URL, stored under `[global_config]` in the machine-level TOML.
- Clone destination (`$XDG_CONFIG_HOME/niwa/global/`) is fixed.
- `workspace.ResolveCloneURL` handles `org/repo` shorthand and full HTTPS/SSH URLs.
- `workspace.Cloner` is reused without modification.

#### Chosen: Option A — `niwa config` subcommand tree

Three new files follow the existing one-command-per-file convention:

- `internal/cli/config.go` — `configCmd` group (no `RunE`), registered under `rootCmd`.
- `internal/cli/config_set.go` — `configSetCmd` group and `configSetGlobalCmd` leaf.
- `internal/cli/config_unset.go` — `configUnsetCmd` group and `configUnsetGlobalCmd` leaf.

`runConfigSetGlobal` behavior:
1. Load current machine config via `config.LoadGlobalConfig()`.
2. Resolve clone URL from the `<repo>` argument using `workspace.ResolveCloneURL`.
3. Determine clone destination: `filepath.Join(configHome, "niwa", "global")`.
4. Clone with `workspace.Cloner{}` (same as `init.go`).
5. Update the `[global_config]` section in `GlobalConfig`.
6. Save via `config.SaveGlobalConfig(cfg)`.

If global config is already registered, the command silently replaces the registration and re-clones from the new URL. If the new repo is unreachable, the command fails and the prior registration is preserved.

`runConfigUnsetGlobal` behavior:
1. Load config; if no global config registered, exit with a message.
2. `os.RemoveAll` the clone directory.
3. Clear `[global_config]` in `GlobalConfig`.
4. Save config.

#### Alternatives Considered

**Option B — `niwa global` top-level command.** Every existing niwa top-level command is an action verb (`apply`, `init`, `create`, `destroy`, `status`). `global` is a subject noun, making it the only non-verb top-level command. The `niwa global list` ambiguity (global workspaces? global config entries?) is a maintenance hazard.

**Option C — `niwa init --global-config <repo>` flag.** Registration and workspace initialization are independent operations. Coupling them requires users to re-initialize a workspace just to register or change global config. Unregistration has no clean home. This option solves half the problem.

---

### Decision 3: `CLAUDE.global.md` injection mechanism

At apply time, the global config's `CLAUDE.global.md` must be made available in each instance's CLAUDE.md context hierarchy. Three injection mechanisms were evaluated.

**Key assumptions:**
- `globalConfigDir` is the local clone path, already resolved before `runPipeline` is called.
- `CLAUDE.global.md` is copied verbatim (no template expansion).
- The instance workspace `CLAUDE.md` is already present when the injection step runs (created by `InstallWorkspaceContent`).

#### Chosen: Option A — Copy file + inject `@import` (mirrors workspace-context.md pattern)

A new `InstallGlobalClaudeContent(globalConfigDir, instanceRoot string) ([]string, error)` function in `workspace_context.go`:

1. Derives `src = filepath.Join(globalConfigDir, "CLAUDE.global.md")`.
2. Returns `nil, nil` if the file does not exist (global Claude instructions are optional).
3. Copies the file verbatim to `filepath.Join(instanceRoot, "CLAUDE.global.md")`.
4. Calls `ensureImportInCLAUDE(claudePath, "@CLAUDE.global.md")` to add the import directive.
5. Returns `[]string{dest}` for tracking in `writtenFiles`.

This is a direct parallel of `InstallWorkspaceContext`, which already does the same copy-then-import pattern for `workspace-context.md`. `ensureImportInCLAUDE` is already idempotent; no modifications needed.

`runPipeline` in `apply.go` calls `InstallGlobalClaudeContent` after `InstallWorkspaceContext` (Step 4.5+) when `globalConfigDir != ""`.

When global config is unregistered, `cleanRemovedFiles` deletes the copied `CLAUDE.global.md`. The `@import` line in `CLAUDE.md` is left in place (Claude Code silently ignores imports that reference non-existent files, consistent with current behavior for workspace-context.md).

#### Alternatives Considered

**Option B — Treat as a managed file entry in global config TOML.** The files materializer doesn't call `ensureImportInCLAUDE` for any file it copies -- adding that awareness would bleed CLAUDE.md-specific logic into a general-purpose materializer. Option A keeps the concern localized to `workspace_context.go`, which already owns the `ensureImportInCLAUDE` pattern.

**Option C — New `global` content level in `ContentConfig`.** Adds a new struct field in `ContentConfig`, new merge semantics between global and workspace content, and new code paths in the content installation pipeline. The PRD says global Claude content is additive and does not replace or override shared workspace CLAUDE.md content -- this is exactly what the simpler copy+import achieves with no new infrastructure.

## Decision Outcome

The three decisions compose into a coherent end-to-end flow with minimal new surface area:

A user runs `niwa config set global <repo>` once per machine (D2). The command stores the registration in `~/.config/niwa/config.toml` under `[global_config]` and clones the repo to `$XDG_CONFIG_HOME/niwa/global/`. On every subsequent `niwa apply`, the pipeline:

1. Reads `SkipGlobal` from instance state (D1, D3). If set, the global layer is skipped entirely.
2. Syncs the global config clone via `SyncConfigDir()` -- the same function used for workspace config (D1).
3. Parses `GlobalConfigOverride` from the clone directory (D1).
4. Calls `ResolveGlobalOverride(globalCfg, ws.Workspace.Name)` to merge the flat `[global]` section with any `[workspaces.<name>]` override for the current workspace (D1).
5. Calls `MergeGlobalOverride(ws, resolvedGlobal, globalConfigDir)` to produce an intermediate `*WorkspaceConfig`; global hook script paths are resolved to absolute paths at this step (D1).
6. Calls `MergeOverrides(intermediate, repoName)` for each repo -- unchanged from today (D1).
7. Calls `InstallGlobalClaudeContent(globalConfigDir, instanceRoot)` after workspace context installation to copy `CLAUDE.global.md` and add the `@import` directive (D3).

`niwa init --skip-global` stores `SkipGlobal: true` in `.niwa/instance.json`. All subsequent applies on that instance skip steps 2–7 without requiring the flag again.

## Solution Architecture

### New types (`internal/config/`)

```go
// GlobalOverride holds the fields from global config that may overlay
// workspace defaults. Fields missing from the TOML are zero values and
// are treated as "not set" by merge functions.
type GlobalOverride struct {
    Claude *ClaudeConfig     `toml:"claude,omitempty"`
    Env    EnvConfig         `toml:"env,omitempty"`
    Files  map[string]string `toml:"files,omitempty"`
}

// GlobalConfigOverride is the top-level struct for the global config TOML file.
// ParseGlobalConfigOverride parses TOML bytes into this struct, validates
// path-traversal safety on Files destination values and Env.Files source paths,
// and returns an error for any invalid paths.
type GlobalConfigOverride struct {
    Global     GlobalOverride            `toml:"global"`
    Workspaces map[string]GlobalOverride `toml:"workspaces"`
}
```

`registry.go` gains a new field in `GlobalSettings` (or a sibling struct):

```go
// GlobalConfigSource stores the registered global config repo. LocalPath is
// not persisted -- it is always derived as filepath.Join(xdgConfigHome, "niwa", "global")
// to prevent stale-path inconsistencies if XDG_CONFIG_HOME changes.
type GlobalConfigSource struct {
    Repo string `toml:"repo"`
}
```

stored under `[global_config]` in `~/.config/niwa/config.toml` (created with mode `0o600`).

`InstanceState` in `state.go` gains:

```go
SkipGlobal bool `json:"skip_global,omitempty"`
```

### New functions (`internal/workspace/override.go`)

```go
// ResolveGlobalOverride merges the flat [global] section with the matching
// [workspaces.<name>] section, returning a single GlobalOverride.
func ResolveGlobalOverride(g *config.GlobalConfigOverride, workspaceName string) config.GlobalOverride

// MergeGlobalOverride applies a GlobalOverride on top of a WorkspaceConfig
// baseline and returns a new *WorkspaceConfig. The original ws is not mutated.
//
// Hook script paths from the GlobalOverride are resolved to absolute paths
// (filepath.Join(globalConfigDir, script)) before being merged into the result.
// This allows HooksMaterializer to use a single ctx.ConfigDir (the workspace
// config directory) without needing to know which config repo each hook came
// from. The source-side checkContainment guard in HooksMaterializer is skipped
// for absolute paths; the destination-side check remains in place.
//
// Merge semantics by field:
//   - Claude.Hooks: global hooks appended after workspace hooks; script paths
//     resolved to absolute paths using globalConfigDir.
//   - Claude.Settings: global wins per key (global value takes precedence).
//   - Claude.Env.Promote: union (both sets promoted).
//   - Claude.Env.Vars: global wins per key.
//   - Claude.Plugins: union, deduplicated (global adds to workspace list).
//   - Env.Files: global appended after workspace env files.
//   - Env.Vars: global wins per key.
//   - Files: global wins per key; empty string suppresses the workspace mapping.
//
// Plugins union semantics differ from RepoOverride (where Plugins != nil
// replaces). The asymmetry is intentional: replacing workspace plugins would
// silently disable team tooling when a user adds a personal plugin.
func MergeGlobalOverride(ws *config.WorkspaceConfig, g config.GlobalOverride, globalConfigDir string) *config.WorkspaceConfig
```

### New function (`internal/workspace/workspace_context.go`)

```go
func InstallGlobalClaudeContent(globalConfigDir, instanceRoot string) ([]string, error)
```

### New CLI files (`internal/cli/`)

- `config.go` — `configCmd` group
- `config_set.go` — `configSetCmd` group, `configSetGlobalCmd` leaf
- `config_unset.go` — `configUnsetCmd` group, `configUnsetGlobalCmd` leaf

### Modified files

| File | Change |
|------|--------|
| `internal/config/registry.go` | Add `GlobalConfigSource` struct; add field to `GlobalConfig`; use `0o600` in `SaveGlobalConfigTo` |
| `internal/config/config.go` | Add `GlobalOverride`, `GlobalConfigOverride`, `ParseGlobalConfigOverride` |
| `internal/workspace/override.go` | Add `ResolveGlobalOverride`, `MergeGlobalOverride` |
| `internal/workspace/workspace_context.go` | Add `InstallGlobalClaudeContent` |
| `internal/workspace/apply.go` | Thread `globalConfigDir` through `runPipeline`; add sync, parse, merge, install steps |
| `internal/cli/apply.go` | Read `SkipGlobal` from instance state; derive `globalConfigDir` from XDG_CONFIG_HOME |
| `internal/cli/init.go` | Add `--skip-global` flag; persist to `InstanceState` |
| `internal/workspace/state.go` | Add `SkipGlobal bool` to `InstanceState` |

### Apply pipeline sequence (modified)

```
1. Load instance state (existing)
2. Sync workspace config — SyncConfigDir() (existing)
   2a. [NEW] If !SkipGlobal && global registered: SyncConfigDir(globalConfigDir)
   2b. [NEW] If sync fails: abort with error identifying cause
3. Load WorkspaceConfig from .niwa/workspace.toml (existing)
   3a. [NEW] If !SkipGlobal && global registered: parse GlobalConfigOverride
   3b. [NEW] ResolveGlobalOverride(globalCfg, ws.Workspace.Name)
   3c. [NEW] intermediate = MergeGlobalOverride(ws, resolvedGlobal, globalConfigDir)
4. For each repo: MergeOverrides(intermediate, repoName) (existing, intermediate replaces ws)
5. Materialize to disk (existing)
   5a. InstallWorkspaceContent (existing)
   5b. InstallWorkspaceContext (existing)
   5c. [NEW] If !SkipGlobal: InstallGlobalClaudeContent(globalConfigDir, instanceRoot)
   5d. cleanRemovedFiles (existing, picks up CLAUDE.global.md automatically)
```

## Implementation Approach

The implementation has one foundation block, two parallel mid-layer blocks, and a final integration block. Blocks 2 and 3 can start only after Block 1 completes, since both depend on the types and registry fields it defines.

**Block 1: Config types and registry (foundation)**
- Add `GlobalOverride`, `GlobalConfigOverride` to `internal/config/config.go`.
- Add `ParseGlobalConfigOverride` with path-traversal validation on `Files` destination values and `Env.Files` source paths (same logic as `validateContentSource`).
- Add `GlobalConfigSource` (repo URL only; local path derived at runtime) to `internal/config/registry.go`; implement `LoadGlobalConfig`, `SaveGlobalConfigTo` with `0o600` file permissions.
- Add `SkipGlobal bool` to `InstanceState` in `internal/workspace/state.go`.
- Unit tests: parse round-trip, path-traversal rejection (absolute paths, `..` traversal).

**Block 2: Merge functions (depends on Block 1)**
- Implement `ResolveGlobalOverride` and `MergeGlobalOverride(ws, g, globalConfigDir)` in `override.go`.
- `MergeGlobalOverride` resolves global hook script paths to absolute paths using `globalConfigDir` before merging.
- Table-driven unit tests for each field type: hooks append (with absolute path verification), `Settings` global-wins, `Env.Promote` union, env vars global-wins, plugins union, files suppress.

**Block 3: `niwa config` CLI commands (depends on Block 1)**
- Add `config.go`, `config_set.go`, `config_unset.go` to `internal/cli/`.
- Add `--skip-global` to `niwa init`; persist to `InstanceState`.
- Tests: registration stores repo URL; unregistration deletes clone and clears config; `--skip-global` sets flag in state.

**Block 4: Apply integration (depends on Blocks 2 and 3)**
- Thread `globalConfigDir` (derived from `$XDG_CONFIG_HOME/niwa/global`) into `apply.go` and `runPipeline`.
- Implement sync step 2a, parse+merge steps 3a-3c, and `InstallGlobalClaudeContent` step 5c.
- Integration tests: apply with global config produces expected effective config; global hook scripts are installed; `SkipGlobal` instance is unaffected; sync failure aborts.

## Security Considerations

**Arbitrary file write via global config TOML.**
The `files` field in `GlobalOverride` maps source keys to destination paths inside the instance root. Malicious or mistaken destination paths (e.g., `../../.ssh/authorized_keys`) could write outside the instance directory. `ParseGlobalConfigOverride` must validate all destination values in the `files` map and all source paths in `Env.Files` using the same path-traversal rejection logic as `validateContentSource` in `internal/config/config.go`. The `FilesMaterializer` performs runtime `checkContainment` checks as a second line of defense, but parse-time rejection produces clearer error messages and catches issues before any disk writes occur. This validation is required in Block 1 and must be covered by unit tests for both absolute paths and `..`-traversal attempts.

**Hook script source directory resolution with two config repos.**
After `MergeGlobalOverride`, the intermediate `WorkspaceConfig` contains hooks from both the workspace config repo and the global config repo. `HooksMaterializer` resolves hook script paths relative to a single `ctx.ConfigDir`. `MergeGlobalOverride` resolves global hook script paths to absolute paths using `globalConfigDir` before merging, so the materializer can use the workspace `ctx.ConfigDir` without modification. The source-side `checkContainment(src, ctx.ConfigDir)` guard in `HooksMaterializer` is not applicable to already-absolute paths and is skipped for them; the destination-side check (`checkContainment(targetPath, ctx.RepoDir)`) remains in place for all hooks regardless of origin.

**Arbitrary command execution via global hooks.**
Global hooks are shell scripts that run at apply time. A compromised global config repo could inject malicious hook scripts. This risk is identical to workspace config today -- the threat model is that the user owns and trusts their global config repo, just as they own and trust their workspace config repo. No new mitigation is needed beyond what applies to workspace hooks.

**Plugin trust boundary.**
Workspace config has no mechanism to block or remove plugins added via global config. A global plugin is unioned with workspace plugins and applied to all global-config-enabled instances. For CI/CD environments or shared machines where only approved plugins should run, `--skip-global` at init time is the only control. Operators running niwa in automated pipelines should initialize instances with `--skip-global`.

**Credential exposure via `GlobalConfigOverride` TOML.**
The global config TOML may contain env vars with secrets. The file lives in a user-owned GitHub repo; users should not store sensitive values as inline `vars` in a public repo. Niwa does not add new transmission vectors. Any error paths added in the global sync or merge steps must not log env var values; the existing `EnvMaterializer` and `resolveClaudeEnvVars` already satisfy this constraint.

**`config.toml` file permissions.**
`SaveGlobalConfigTo` must create `~/.config/niwa/config.toml` with mode `0o600` using `os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)` rather than `os.Create`, which produces world-readable files under most umasks. The registered repo URL is not sensitive in most contexts, but restricted permissions prevent exposure of any future fields and are the correct baseline for a user configuration file.

**Machine-level config tamper.**
`~/.config/niwa/config.toml` is a user-owned file on the local machine. No remote party can modify it. Registration and unregistration require the user to run `niwa config set/unset global` explicitly. Since `LocalPath` is derived at runtime from `$XDG_CONFIG_HOME` rather than read from config, there is no tamper risk from a modified `local_path` field.

## Consequences

**Positive:**
- Developers can manage personal preferences and workspace-specific credentials via niwa without touching the shared team workspace repo.
- The three-layer merge order is explicit at the call site in `apply.go`, not hidden in function call order.
- All new functions follow existing patterns; a contributor familiar with `MergeOverrides` or `InstallWorkspaceContext` can understand the global equivalents immediately.
- Zero behavior change for existing workspaces: the global config code paths are guarded by config registration and `SkipGlobal`, making the feature completely inert when not configured.
- `--skip-global` at init time gives CI/CD operators a clean way to opt out of user-specific config on shared machines.

**Negative:**
- The plugins union semantics in `MergeGlobalOverride` differ from `RepoOverride` semantics (where `Plugins != nil` replaces). This asymmetry must be documented at the call site to avoid confusion for future contributors.
- The `@import` line in `CLAUDE.md` is not removed when global config is unregistered. Claude Code silently ignores missing imports, so there is no functional regression, but the stale line may confuse users who inspect their CLAUDE.md.
- Plugins from both global config and workspace config are unioned without conflict detection. If the same plugin is declared in both, it may be installed twice depending on plugin installation semantics. This is a known limitation acknowledged in the PRD.
- An instance initialized with `--skip-global` cannot re-enable global config without re-initialization (intentional v1 constraint).

**v1 limitations:**
- Concurrent `niwa config unset global` and `niwa apply` on the same machine can produce a race: `os.RemoveAll` on the clone directory while apply is reading it. This is an unlikely operational edge case (both operations require explicit user action) and is not mitigated in v1. Users running niwa in parallel processes on the same machine should serialize these operations.

**Mitigations:**
- Document the union vs replace asymmetry in a code comment on `mergeClaudeGlobal`.
- A `removeImportFromCLAUDE` function is a low-effort follow-on if stale imports become a reported pain point.
- Plugin deduplication can be added to the union logic if double-installation is reported.
