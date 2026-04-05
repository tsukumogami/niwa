# Decision 1: Global config representation and merge layer

## Chosen: Option B — New GlobalOverride struct

## Rationale

Option B matches the existing pattern (`RepoOverride` / `InstanceConfig` as bounded override structs applied on top of a `WorkspaceConfig` baseline), keeps `WorkspaceConfig` stable, and produces a merge function with the same shape as the two already in `override.go`. The global config TOML schema is a strict subset of what workspace config supports, which is exactly what a bounded struct encodes. Option A conflates two distinct roles (base config vs override layer) behind one type, making the subset constraint invisible and the merge ambiguous. Option C introduces interface dispatch for two concrete cases, which adds indirection without benefit.

## How It Works

### Types

Add `GlobalOverride` to `internal/config/` (either a new file `global_override.go` or appended to `config.go`):

```go
// GlobalOverride holds the fields from global config that may overlay
// workspace defaults. It mirrors RepoOverride / InstanceConfig but applies
// user-wide and is keyed by workspace name for per-workspace sections.
type GlobalOverride struct {
    Claude *ClaudeConfig     `toml:"claude,omitempty"`
    Env    EnvConfig         `toml:"env,omitempty"`
    Files  map[string]string `toml:"files,omitempty"`
}
```

The global config TOML (`~/.config/niwa/personal/global.toml` or similar) has a flat `[global]` section and named `[workspaces.<name>]` sections:

```toml
[global]
[global.claude.hooks.pre_tool_use]
scripts = ["hooks/my-hook.sh"]

[global.env]
files = ["~/.secrets.env"]

[workspaces.my-workspace]
[workspaces.my-workspace.env]
vars = { DEPLOY_ENV = "staging" }
```

Add a `GlobalConfigOverride` top-level struct to parse this file:

```go
type GlobalConfigOverride struct {
    Global     GlobalOverride            `toml:"global"`
    Workspaces map[string]GlobalOverride `toml:"workspaces"`
}
```

The registry already has `GlobalConfig` in `registry.go`; it gains a new field to record where the global override config repo is registered (URL + local clone path). This is a separate addition to `GlobalConfig` / `GlobalSettings` and does not affect override types.

### Merge chain

The three-layer chain is:

```
workspace defaults  →  global overlay  →  per-repo overlay
```

Two new functions in `override.go`:

**1. Resolve the effective global override for a workspace run:**

```go
// ResolveGlobalOverride merges the flat [global] section and the matching
// [workspaces.<name>] section from the parsed global config file.
// The workspace-specific section wins over the flat global section per field.
func ResolveGlobalOverride(g *config.GlobalConfigOverride, workspaceName string) config.GlobalOverride {
    result := applyGlobalOverride(config.GlobalOverride{}, g.Global)
    if ws, ok := g.Workspaces[workspaceName]; ok {
        result = applyGlobalOverride(result, ws)
    }
    return result
}
```

**2. Apply a `GlobalOverride` on top of a `WorkspaceConfig` baseline, producing an intermediate `WorkspaceConfig`:**

```go
// MergeGlobalOverride returns a shallow-merged WorkspaceConfig with global
// override fields applied. The result is passed to MergeOverrides in place
// of the original ws, so the per-repo layer sees global values as defaults.
// The original ws is not mutated.
func MergeGlobalOverride(ws *config.WorkspaceConfig, g config.GlobalOverride) *config.WorkspaceConfig {
    // Copy ws by value (shallow — Repos/Groups maps are not mutated by this function).
    merged := *ws

    if g.Claude != nil {
        // Hooks: append global after workspace.
        // Settings: global wins per key.
        // Claude env promote: union.
        // Claude env vars: global wins per key.
        // Plugins: union (global adds to workspace).
        merged.Claude = mergeClaudeGlobal(ws.Claude, *g.Claude)
    }

    // Env files: append global after workspace.
    // Env vars: global wins per key.
    merged.Env = mergeEnvGlobal(ws.Env, g.Env)

    // Files: global wins per key; empty string removes workspace mapping.
    merged.Files = mergeFilesGlobal(ws.Files, g.Files)

    return &merged
}
```

The existing `MergeOverrides(ws, repoName)` call in `apply.go` is then replaced by:

```go
intermediate := ws
if globalOverride != nil {
    intermediate = MergeGlobalOverride(ws, resolvedGlobal)
}
effective := MergeOverrides(intermediate, cr.Repo.Name)
```

`intermediate` is computed once per apply run (not per repo), since the global overlay and workspace-name resolution are the same for all repos.

### End-to-end flow

1. `apply.go` loads `WorkspaceConfig` from `.niwa/workspace.toml` (existing).
2. `apply.go` loads `GlobalConfigOverride` from the registered global config clone directory (new; skipped when `SkipGlobal` is set in instance state or global config is not registered).
3. `ResolveGlobalOverride(globalCfg, ws.Workspace.Name)` produces a single `GlobalOverride` for this workspace (new).
4. `MergeGlobalOverride(ws, globalOverride)` produces `intermediate *WorkspaceConfig` (new).
5. For each repo: `MergeOverrides(intermediate, repoName)` produces `EffectiveConfig` (existing, unchanged signature).

### Zero-impact guarantee

When global config is not registered or `SkipGlobal` is set, steps 2–4 are skipped and `intermediate = ws`. The call to `MergeOverrides` is identical to today. No existing behavior changes.

### Plugins merge semantics

`MergeGlobalOverride` unions plugins (global adds to workspace list, deduplicated). This differs from `RepoOverride` behavior where `Plugins != nil` replaces entirely. The asymmetry is intentional (confirmed in exploration) and is isolated to `mergeClaudeGlobal`, making it easy to document and test in isolation.

### Testability

Each piece is independently testable:

- `ResolveGlobalOverride`: table-driven tests over flat vs workspace-specific section combinations.
- `MergeGlobalOverride`: table-driven tests over each field type (hooks append, settings win, env files append, env vars win, plugins union, files win/remove).
- Integration: existing `TestMergeOverrides*` tests are unchanged; new integration tests verify the three-layer chain produces the right `EffectiveConfig`.

## Alternatives Rejected

**Option A (Reuse WorkspaceConfig as global config type):** `WorkspaceConfig` carries fields that are meaningless in a global override context: `Workspace` metadata (`name`, `version`, `default_branch`), `Sources`, `Groups`, `Repos`, `Content`, `Instance`, `Channels`. Accepting a `*WorkspaceConfig` as a global override creates a schema that the TOML parser will silently ignore most of; worse, the `validate()` function requires `workspace.name`, so the global config file must either include a dummy name or bypass validation. The merge function `MergeGlobal(ws, global *WorkspaceConfig)` is ambiguous: which fields of `global` are used? All of them? Only some? The subset is implicit. Option A trades a small type definition for pervasive ambiguity and a leaky schema.

**Option C (Generalize merge chain to N layers via interface):** The codebase has exactly two override types (`InstanceConfig`, `RepoOverride`) and is adding one more (`GlobalOverride`). An `Overrider` interface introduces polymorphic dispatch for a problem that doesn't benefit from polymorphism: each layer has different fields (e.g., `RepoOverride` has `URL`, `Branch`, `Scope`; `GlobalOverride` does not), so a shared interface would have to be narrow to the point of uselessness or require type assertions internally. The existing code is explicit and readable precisely because each merge function names its inputs. Option C is the right call at four or more layers with genuinely shared logic; it's over-engineering for three.

## Assumptions

- The global config file is parsed from a directory cloned by `SyncConfigDir()`, so it has a known local path on disk at apply time. The parser can be a straightforward `ParseGlobalConfigOverride(data []byte)` analogous to the existing `Parse()`.
- `MergeGlobalOverride` does not need to handle `Marketplaces` (workspace-wide, not per-layer) or content sources (no merge semantics defined, deferred per exploration findings).
- Plugins union (not replace) is the correct merge behavior for the global layer, as confirmed in the exploration phase. This is assumed to remain the decision; if reversed, `mergeClaudeGlobal` is the only function that changes.
- The `GlobalConfigOverride` struct lives in `internal/config/` alongside the existing types, not in `internal/workspace/`. This keeps TOML parsing co-located with type definitions.

## Open Questions

- **File name for global config TOML**: what is the conventional filename inside the global config repo? (`global.toml`? `config.toml`? `niwa.toml`?) This affects the path passed to the parser in `apply.go` but not the type design.
- **`[global]` vs flat top-level**: the schema above uses `[global]` + `[workspaces.<name>]`. An alternative is a flat top-level `[claude]`, `[env]`, `[files]` with `[workspaces.<name>]` sections. The flat form is simpler to write; `[global]` is more explicit. Either works with `GlobalConfigOverride`; the struct field tag changes but the type does not.
- **`GlobalConfigOverride` naming**: the struct name should reflect that this parses the entire global config TOML file, not just one override layer. Alternative: `GlobalConfigFile`. Naming is cosmetic but should be decided before the type is referenced in multiple call sites.
- **Validation**: should `ParseGlobalConfigOverride` run a validate step (e.g., no path traversal in file sources)? The `Parse()` function for workspace config validates content sources. Global config files are user-owned and could contain the same fields. Assume yes, but the specific validation rules need confirmation.
