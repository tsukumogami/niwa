# Decision 2: Context Detection for Apply's Dual Scope

## Question

How should context detection work for `niwa apply`'s dual scope (root vs instance)?

## Decision

**Use marker-file-based detection with instance-first priority.**

Detection order in `apply`'s RunE:

1. If `--instance <name>` flag (or positional arg) is provided, resolve from workspace root
2. Else, try `DiscoverInstance(cwd)` -- if found, apply to that single instance
3. Else, try `config.Discover(cwd)` -- if found, treat as workspace root: `EnumerateInstances()` and apply to all
4. Else, return an error

This works because `.niwa/` directories are already disambiguated by their contents:
- Workspace root: `.niwa/workspace.toml` (config)
- Instance root: `.niwa/instance.json` (state)

These never coexist in the same `.niwa/` directory.

## Scope Resolution Function

Add a `ResolveApplyScope` function in `internal/workspace/` that returns a tagged union:

```go
type ApplyScope struct {
    Mode      ApplyMode
    Instances []string  // absolute paths to instance roots
    Config    string    // path to workspace.toml (always set)
}

type ApplyMode int

const (
    ApplySingle ApplyMode = iota  // from within an instance
    ApplyAll                       // from workspace root, all instances
    ApplyNamed                     // from workspace root, targeting one by name
)
```

The CLI layer calls `ResolveApplyScope(cwd, instanceFlag)` and the workspace package handles detection logic. The CLI never does discovery itself beyond passing cwd and flags.

## Instance Name Resolution (--instance flag)

When `--instance <name>` is given from workspace root:

1. `config.Discover(cwd)` to find workspace root
2. `EnumerateInstances(root)` to get all instance directories
3. `LoadState(dir)` for each, match `InstanceName == name`
4. Error if not found or ambiguous

This reuses existing infrastructure without adding new indexes or lookups.

## Interaction with Existing Workspace Name Arg

The current `apply [workspace-name]` positional arg resolves through the global registry to find a workspace root. This remains as-is. The `--instance` flag layers on top:

- `niwa apply` -- detect scope from cwd
- `niwa apply myworkspace` -- resolve workspace by name from registry, apply all instances
- `niwa apply --instance dev` -- from cwd, resolve workspace root, apply only "dev" instance
- `niwa apply myworkspace --instance dev` -- both: resolve workspace, target instance

## Why Instance-First Detection

When cwd is inside an instance, the user almost certainly wants to apply to that instance. Walking up to workspace root and applying everything would be surprising. The `DiscoverInstance` walk-up already stops at the first `.niwa/instance.json` it finds, which won't accidentally reach the workspace root (since workspace root has `workspace.toml`, not `instance.json`).

## Edge Cases

- **cwd is workspace root itself**: `DiscoverInstance` finds nothing (root has `workspace.toml`, not `instance.json`). Falls through to `config.Discover`, which succeeds. Apply-all mode.
- **No instances exist yet**: `EnumerateInstances` returns empty list. Apply should create instances based on config (this is decision 1's territory -- the create/apply split).
- **Detached instances**: Instances with `detached: true` in state should be skipped during apply-all. The `--instance` flag can still target them explicitly.

## Confidence

High. The marker files already provide clean disambiguation. The detection order (instance-first) matches user intent in every scenario. The `--instance` flag gives explicit control when the implicit detection isn't what the user wants.
