---
status: Current
upstream: docs/prds/PRD-config-distribution.md
problem: |
  niwa's apply pipeline installs CLAUDE.md content but doesn't distribute
  Claude Code operational configuration (hooks, settings) or environment
  files. The config schema exists but nothing materializes to disk. The
  distribution step needs to be extensible and support convention-based
  auto-discovery.
decision: |
  Interface-based materializer pattern in the apply pipeline. Claude Code
  hooks and settings namespaced under [claude.*] with convention-based
  auto-discovery from .niwa/hooks/ directory structure. Env stays top-level
  (tool-agnostic) with auto-discovery from .niwa/env/. Three materializers
  run per-repo in fixed order (hooks -> settings -> env).
rationale: |
  The materializer interface matches the existing content installation
  pattern. Namespacing under [claude] makes the tool-specific config
  explicit and future-proof for multi-tool support. Convention-based
  auto-discovery mirrors the existing content auto-discovery pattern and
  minimizes required TOML config.
---

# DESIGN: Config distribution

## Status

Proposed

## Context and Problem Statement

niwa's apply pipeline discovers repos, classifies them into groups, clones, and installs CLAUDE.md content. But it doesn't distribute Claude Code operational configuration: hooks (shell scripts in `.claude/hooks/`), settings (`settings.local.json`), or environment files (`.local.env`). The config schema and per-repo merge logic already exist in `override.go`, but nothing materializes the merged results to disk.

The PRD (docs/prds/PRD-config-distribution.md) defines 17 requirements including convention-based auto-discovery for hooks and env files, `[claude.*]` namespacing for tool-specific config, and an extensible distribution mechanism.

## Decision Drivers

- **Convention over configuration**: auto-discover hooks from `.niwa/hooks/` and env from `.niwa/env/` without explicit TOML entries
- **Tool namespacing**: hooks and settings under `[claude.*]` since they're Claude Code specific; env at top level (tool-agnostic)
- **Extensibility**: adding a new distribution type should not require modifying the core pipeline
- **Existing infrastructure**: merge logic in `override.go`, content auto-discovery pattern in `content.go`
- **Per-repo overrides**: hooks extend, settings replace, env files append + vars replace

## Considered Options

### Decision 1: Extensibility pattern

#### Chosen: Interface-based materializers

A `Materializer` interface registered on `Applier` in a slice. The pipeline loops through them per-repo after content installation.

```go
type MaterializeContext struct {
    Config    *config.WorkspaceConfig
    Effective EffectiveConfig
    RepoName  string
    RepoDir   string
    ConfigDir string
}

type Materializer interface {
    Name() string
    Materialize(ctx MaterializeContext) ([]string, error)
}
```

Three concrete materializers: `HooksMaterializer`, `SettingsMaterializer`, `EnvMaterializer`. Each returns written file paths for managed-file tracking.

#### Alternatives considered

**Function slices**: no `Name()` for error messages or state tracking.

**Event/plugin system**: over-engineering for 3 known synchronous materializers.

**Hardcoded steps**: adding a fourth type means editing the pipeline.

### Decision 2: Config schema with namespacing and auto-discovery

#### Chosen: `[claude.*]` namespace with convention-based auto-discovery

**TOML schema:**

```toml
# Claude Code specific -- namespaced
[claude.hooks]
pre_tool_use = ["hooks/gate-online.sh"]
stop = ["hooks/workflow-continue.sh"]

[claude.settings]
permissions = "bypass"

# Tool-agnostic -- top level
[env]
files = ["env/workspace.env"]
vars = { LOG_LEVEL = "info" }
```

**Go types:**

```go
type WorkspaceConfig struct {
    // ... existing fields ...
    Claude   ClaudeConfig `toml:"claude"`
    Env      EnvConfig    `toml:"env"`
}

type ClaudeConfig struct {
    Hooks    HooksConfig    `toml:"hooks"`
    Settings SettingsConfig `toml:"settings"`
}

// HooksConfig maps event names to lists of script paths.
type HooksConfig map[string][]string

// SettingsConfig maps setting keys to values.
type SettingsConfig map[string]string

// EnvConfig has files (paths to .env sources) and vars (inline key-value pairs).
type EnvConfig struct {
    Files []string          `toml:"files,omitempty"`
    Vars  map[string]string `toml:"vars,omitempty"`
}
```

Per-repo overrides nest under `[repos.<name>.claude.*]` and `[repos.<name>.env]`:

```go
type RepoOverride struct {
    // ... existing fields ...
    Claude *ClaudeConfig `toml:"claude,omitempty"`
    Env    *EnvConfig    `toml:"env,omitempty"`
}
```

**Auto-discovery** (PRD R2, R7):

Hooks auto-discovery scans `.niwa/hooks/`:
- A file `hooks/{event}.sh` maps to that event (e.g., `hooks/stop.sh` -> stop hook)
- Files in `hooks/{event}/` directory map to that event (e.g., `hooks/pre_tool_use/gate.sh`)
- Explicit `[claude.hooks]` config for an event overrides auto-discovered hooks for that event

Env auto-discovery:
- `.niwa/env/workspace.env` is used when no `[env].files` is declared
- `.niwa/env/repos/{repoName}.env` is appended for that repo without explicit `[repos.<name>.env]`
- Explicit config overrides auto-discovery

This mirrors the existing content auto-discovery pattern (PRD R2 from the workspace-config design: "when content_dir is set and a repo has no explicit entry, niwa checks for `{content_dir}/repos/{repo_name}.md`").

#### Alternatives considered

**Flat `[hooks]`/`[settings]`**: ambiguous -- git has hooks, niwa could have lifecycle hooks. Not future-proof for multi-tool support.

**Generic `[[settings]]` with path+format**: too ugly -- writing JSON key paths inside TOML. Better to let niwa understand Claude Code's settings schema and generate the right JSON.

**No auto-discovery**: requires listing every hook and env file explicitly. Verbose for the common case where directory structure already conveys the mapping.

### Decision 3: File writing per materializer

#### Chosen: Direct file writes with fixed ordering

**Hooks materializer** (PRD R1-R3):
1. Auto-discover hooks from `.niwa/hooks/` directory
2. Merge with explicit `[claude.hooks]` config (explicit wins per-event)
3. Merge with per-repo overrides (extend: repo hooks appended)
4. For each event/script: resolve source relative to configDir, validate containment, copy to `{repoDir}/.claude/hooks/{event}/{scriptName}`, chmod 0755
5. Return list of installed hook paths (needed by settings materializer)

**Settings materializer** (PRD R4-R5):
1. Read merged `[claude.settings]` config
2. Build Claude Code settings.local.json:
   - `permissions.defaultMode` from settings `permissions` key
   - `hooks` object from installed hook paths (received via MaterializeContext)
3. Write to `{repoDir}/.claude/settings.local.json`

**Env materializer** (PRD R6-R8):
1. Auto-discover workspace env from `.niwa/env/workspace.env`
2. Auto-discover per-repo env from `.niwa/env/repos/{repoName}.env`
3. Merge with explicit `[env]` config (explicit overrides auto-discovery)
4. Merge with per-repo overrides (files append, vars replace)
5. Parse KEY=VALUE from each source file, overlay inline vars
6. Write to `{repoDir}/.local.env`

Fixed ordering: hooks -> settings -> env. Settings needs installed hook paths from hooks materializer.

**Claude skip** (PRD R12): repos with `claude = false` skip hooks and settings materializers. Env materializer still runs since it's tool-agnostic.

#### Alternatives considered

**Symlinks for hooks**: breaks if config dir moves, not self-contained.

**Single monolithic function**: not extensible, hard to test.

## Decision Outcome

### Summary

The apply pipeline gains a materializer loop after content installation. Three materializers run per-repo in fixed order. Claude Code hooks and settings are namespaced under `[claude.*]` in the TOML config with a `ClaudeConfig` wrapper struct. Env stays at the top level. Both hooks and env support convention-based auto-discovery from directory structure, minimizing required TOML config. Explicit config overrides auto-discovery.

### Rationale

The `[claude]` namespace makes tool-specific config explicit, leaving room for future tools. Auto-discovery mirrors the existing content pattern and reduces boilerplate -- a minimal workspace.toml with just `[claude.settings]` and the right directory structure gets full hooks and env distribution. The materializer interface keeps the pipeline clean and each distribution type independently testable.

> **Note (niwa 0.9.4 — DESIGN-coordinator-loop.md Phase 1, Proposed):**
> `DESIGN-coordinator-loop.md` depends on the hook merge semantics defined here.
> It installs a workspace-level `[claude.hooks] stop` entry — a `report-progress.sh`
> script generated at apply time — that resets the stall watchdog at every turn
> boundary. The `HooksMaterializer` concatenation behavior (workspace hooks
> appended, per-repo hooks extended) ensures this coexists with any
> application-level stop hooks without replacement or conflict.

## Solution Architecture

### Pipeline integration

In `runPipeline`, between content installation (Step 6) and managed file hashing (Step 7):

```
Step 6.5: For each classified repo (with claude enabled for hooks/settings):
  1. Compute EffectiveConfig via MergeOverrides
  2. Build MaterializeContext (includes installed hook paths from previous materializer)
  3. For each materializer: call Materialize, collect written files
```

The MaterializeContext carries state between materializers. After hooks materializer runs, it records installed hook paths on the context so settings materializer can reference them.

```go
type MaterializeContext struct {
    Config         *config.WorkspaceConfig
    Effective      EffectiveConfig
    RepoName       string
    RepoDir        string
    ConfigDir      string
    InstalledHooks map[string][]string // event -> installed script paths, set by hooks materializer
}
```

### Auto-discovery functions

```go
// DiscoverHooks scans configDir/hooks/ for hook scripts.
// Returns HooksConfig mapping event names to script paths.
func DiscoverHooks(configDir string) (config.HooksConfig, error)

// DiscoverEnvFiles scans configDir/env/ for env source files.
// Returns workspace file path and per-repo file paths.
func DiscoverEnvFiles(configDir string) (workspaceFile string, repoFiles map[string]string, error)
```

Discovery runs once per apply (not per-repo). Results are merged with explicit config before the per-repo materializer loop.

### File layout

Config directory (`.niwa/`):
```
.niwa/
  hooks/
    pre_tool_use/
      gate-online.sh
    stop.sh
  env/
    workspace.env
    repos/api.env
```

Output per repo:
```
{repo}/
  .claude/
    hooks/
      pre_tool_use/
        gate-online.sh    # copied, chmod +x
      stop/
        stop.sh           # copied, chmod +x
    settings.local.json   # generated from [claude.settings] + hook refs
  .local.env              # merged from env sources + vars
```

### Package changes

**New: `internal/workspace/materialize.go`**
- `MaterializeContext` struct (with `InstalledHooks`)
- `Materializer` interface
- `HooksMaterializer` -- auto-discover + merge + copy + chmod
- `SettingsMaterializer` -- build JSON from settings + hook refs
- `EnvMaterializer` -- auto-discover + merge + parse + write

**New: `internal/workspace/discover.go`** (or extend existing)
- `DiscoverHooks(configDir)` -- scan hooks/ for event-named files/dirs
- `DiscoverEnvFiles(configDir)` -- scan env/ for workspace.env and repos/*.env

**Modified: `internal/config/config.go`**
- Add `ClaudeConfig` wrapper struct with `Hooks` and `Settings`
- Move hooks/settings under `Claude ClaudeConfig` on WorkspaceConfig
- Update `RepoOverride` to nest under `Claude *ClaudeConfig`
- `Env` stays at top level on both

**Modified: `internal/workspace/override.go`**
- Update `MergeOverrides` and `EffectiveConfig` for `ClaudeConfig` nesting
- Hooks: extend (append per-repo to workspace)
- Settings: replace per-key
- Env: files append, vars replace

**Modified: `internal/workspace/apply.go`**
- `Applier` gains `Materializers []Materializer`
- `NewApplier` initializes default materializers
- `runPipeline` gains Step 6.5 materializer loop
- Auto-discovery runs once before the per-repo loop

## Security Considerations

- **Source path containment**: hook scripts and env files resolved relative to configDir. Materializers validate paths stay within configDir using existing `checkContainment` logic.
- **Executable permissions**: only hook scripts get chmod 0755. Settings (0644) and env (0644) are not executable.
- **No secret injection**: env values in workspace.toml are configuration, not secrets. Secrets belong in .env files referenced by path. niwa writes whatever is configured without special handling.
- **Auto-discovery scope**: DiscoverHooks and DiscoverEnvFiles only scan within `.niwa/`. No filesystem traversal outside the config directory.

## Consequences

### Positive

- Unblocks tsuku adoption (3 blocking gaps resolved)
- Minimal config needed: hooks and env auto-discovered from directory structure
- `[claude.*]` namespace future-proofs for multi-tool support
- Extensible: new materializers slot in without pipeline changes
- All distributed files tracked for drift detection

### Negative

- Settings materializer depends on hooks output (ordering dependency). Mitigated by fixed slice ordering and InstalledHooks on context.
- Auto-discovery adds implicit behavior. Mitigated by explicit config always overriding convention, and `niwa status` showing what's installed.
- Hook scripts copied not symlinked -- changing source requires re-apply. Intentional for self-contained repos.
