---
status: Proposed
upstream: docs/prds/PRD-config-distribution.md
problem: |
  niwa's apply pipeline installs CLAUDE.md content but doesn't distribute
  Claude Code operational configuration (hooks, settings, env files). The
  config schema and per-repo merge logic exist in override.go but nothing
  materializes the merged results to disk. Placeholder map[string]any types
  need typed structs.
decision: |
  Interface-based materializer pattern in the apply pipeline. Three concrete
  materializers (hooks, settings, env) run per-repo after content installation.
  Typed config structs replace map[string]any placeholders. Each materializer
  returns written file paths for managed-file tracking.
rationale: |
  The materializer interface matches the existing content installation pattern
  (function takes config, returns file paths). Typed structs catch config
  errors at parse time. Fixed materializer ordering (hooks -> settings -> env)
  lets settings reference installed hook paths. Adding future distribution
  types means implementing one interface, not modifying the pipeline.
---

# DESIGN: Config distribution

## Status

Proposed

## Context and Problem Statement

niwa's apply pipeline discovers repos, classifies them into groups, clones, and installs CLAUDE.md content. But it doesn't distribute Claude Code operational configuration: hooks (shell scripts in `.claude/hooks/`), settings (`settings.local.json`), or environment files (`.local.env`). The config schema and per-repo merge logic already exist in `override.go`, but nothing materializes the merged results to disk.

The current placeholder types (`map[string]any`) need to become typed structs, and the apply pipeline needs a step that writes configuration files into each repo after content installation. This step should be extensible -- today it's hooks/settings/env, but future distribution types (plugins, workspace scripts, shirabe extensions) should slot in without modifying the core pipeline.

## Decision Drivers

- **Merge logic exists**: `override.go` already implements extend-for-hooks, win-for-settings/env semantics
- **Placeholder types**: `Hooks`, `Settings`, `Env` on WorkspaceConfig and RepoOverride are `map[string]any` -- need typed structs
- **Extensibility**: adding a new distribution type should not require modifying apply.go's pipeline
- **install.sh parity**: hooks go to `.claude/hooks/`, settings to `.claude/settings.local.json`, env to `.local.env`
- **Per-repo overrides**: repo-level config overlays workspace defaults per existing merge semantics
- **Source files live in .niwa/**: hook scripts and env files are referenced by relative path from .niwa/

## Considered Options

### Decision 1: Extensibility pattern

The apply pipeline needs a per-repo distribution step that's easy to extend. Today it's 3 materializers; future ones (plugins, scripts) should slot in without modifying the pipeline loop.

#### Chosen: Interface-based materializers

A `Materializer` interface with `Name()` and `Materialize()`. Concrete implementations registered on `Applier` in a slice. The pipeline loops through them per-repo.

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

`NewApplier` initializes the slice: `[HooksMaterializer, SettingsMaterializer, EnvMaterializer]`. The pipeline calls each per-repo and collects returned file paths into `writtenFiles` for managed-file hashing.

#### Alternatives considered

**Function slices**: `[]func(ctx) ([]string, error)`. Rejected -- no `Name()` for error messages or state tracking. Anonymous functions harder to test.

**Event/plugin system**: publish/subscribe on "repo applied". Rejected -- massive over-engineering for 3 known materializers with synchronous, sequential needs.

**Hardcoded steps**: inline `installHooks()`, `installSettings()`, `installEnv()` in runPipeline. Rejected -- adding a fourth type means editing the pipeline and duplicating the per-repo loop pattern.

### Decision 2: Typed config structs

The `map[string]any` placeholders need typed structs for compile-time safety and clear TOML mapping.

#### Chosen: Map types for hooks/settings, struct for env

```go
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

Map types for hooks and settings preserve forward compatibility for new event names and setting keys. Struct for EnvConfig distinguishes files (list of paths) from vars (key-value pairs) without magic key detection. Merge semantics in `MergeOverrides` updated to use typed fields.

#### Alternatives considered

**All structs**: explicit fields for each hook event (PreToolUse, Stop). Rejected because new Claude Code hook events would require code changes.

**All maps**: `map[string]any` for env too. Rejected because env has two distinct sub-types (file list vs key-value map) that a struct expresses better.

### Decision 3: File writing per materializer

Each materializer uses standard `os` functions (ReadFile, WriteFile, MkdirAll) following the existing `installContentFile` pattern.

#### Chosen: Direct file writes with fixed ordering

**Hooks materializer**: for each event/script in the merged HooksConfig, copy the source script from configDir to `{repoDir}/.claude/hooks/{event}/{scriptName}`, chmod +x.

**Settings materializer**: build a JSON object from merged SettingsConfig, add hook references from installed hooks, write to `{repoDir}/.claude/settings.local.json`. Runs after hooks materializer so it can reference installed hook paths.

**Env materializer**: parse each env file from configDir (KEY=VALUE format), overlay inline vars, write merged result to `{repoDir}/.local.env`. Repo entries override workspace entries (already handled by MergeOverrides).

Fixed ordering: hooks -> settings -> env. Settings depends on hooks output (needs installed paths for hook references).

#### Alternatives considered

**Symlinks for hooks**: link to source in .niwa/. Rejected -- breaks if config dir moves, repos aren't self-contained, complicates drift detection.

**Virtual filesystem**: wrap writes behind io/fs interface. Rejected -- existing code uses os functions directly, tests use t.TempDir().

## Decision Outcome

### Summary

The apply pipeline gains a materializer loop after content installation. Three materializers (hooks, settings, env) run per-repo in fixed order, each receiving the merged effective config and returning written file paths. Typed config structs replace map[string]any placeholders. Adding future distribution types means implementing the Materializer interface and appending to the Applier's slice.

### Rationale

The interface matches the existing pattern (content installers take config, return file paths). Typed structs catch config errors at parse time. Fixed ordering handles the settings-depends-on-hooks case simply. Each materializer is independently testable. The pipeline loop is unmodified when adding new materializers.

## Solution Architecture

### Pipeline integration

In `runPipeline`, between content installation (Step 6) and managed file hashing (Step 7):

```
Step 6.5: For each classified repo (with claude enabled):
  1. Compute EffectiveConfig via MergeOverrides
  2. Build MaterializeContext
  3. For each materializer: call Materialize, collect written files
```

### File layout per repo

After apply with hooks, settings, and env configured:

```
{repo}/
  .claude/
    hooks/
      pre_tool_use/
        gate-online.sh        # copied from .niwa/hooks/
      stop/
        workflow-continue.sh  # copied from .niwa/hooks/
    settings.local.json       # generated from [settings] + hook refs
  .local.env                  # merged from [env] files + vars
  CLAUDE.local.md             # existing content installation
```

### Package changes

**New: `internal/workspace/materialize.go`**
- `MaterializeContext` struct
- `Materializer` interface
- `HooksMaterializer`, `SettingsMaterializer`, `EnvMaterializer` implementations

**Modified: `internal/workspace/apply.go`**
- `Applier` gains `Materializers []Materializer` field
- `NewApplier` initializes default materializers
- `runPipeline` gains Step 6.5 materializer loop

**Already modified: `internal/config/config.go`**
- `HooksConfig`, `SettingsConfig`, `EnvConfig` typed structs (already implemented by decision 2 agent)

**Already modified: `internal/workspace/override.go`**
- `MergeOverrides` uses typed fields (already implemented)

## Security Considerations

- **Source path containment**: hook scripts and env files are resolved relative to configDir (`.niwa/`). Materializers must validate paths stay within configDir, same as content source validation.
- **Executable permissions**: only hook scripts get chmod +x. Settings and env files get standard 0644.
- **No secret injection**: env vars in workspace.toml are configuration values, not secrets. Secrets belong in per-host config (out of scope for this design). The env materializer writes whatever is in the config without special secret handling.

## Consequences

### Positive

- Unblocks tsuku adoption of niwa (the 3 blocking gaps are resolved)
- Extensible: future materializers (plugins, scripts) slot in without pipeline changes
- Typed config catches errors at parse time instead of runtime
- Managed file tracking covers all distributed files (drift detection works for hooks/settings/env too)

### Negative

- Settings materializer needs to know about hooks output (ordering dependency). Mitigated by fixed slice ordering.
- Hook scripts are copied, not symlinked. Changing a source script requires re-running apply. This is intentional -- repos should be self-contained after apply.
