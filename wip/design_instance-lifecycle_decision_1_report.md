# Decision: How should the Applier be refactored to separate create from apply?

## Status: decided

## Context

The current `Applier.Apply()` method in `internal/workspace/apply.go` handles both
first-run creation and subsequent re-application in a single 189-line method. It
loads existing state opportunistically (line 40-41), runs the full pipeline, and
patches `Created`/`InstanceNumber` from prior state if it existed. This conflation
makes it hard to add create-specific behavior (instance directory naming, number
assignment) and apply-specific behavior (cleanup of removed repos/groups, managed
file reconciliation).

## Decision

**Extract a shared pipeline function; implement Create and Apply as thin callers
that set up preconditions and post-process results.**

### Design

```
Applier
  Create(ctx, cfg, configDir, workspaceRoot) (string, error)
  Apply(ctx, cfg, configDir, instanceRoot) error
  runPipeline(ctx, cfg, configDir, instanceRoot, opts) (*pipelineResult, error)
```

**`runPipeline`** (unexported) contains the shared logic currently in `Apply`:
discover repos, classify, clone, install content, build managed-file list. It
accepts a `pipelineOpts` struct for behavioral differences:

```go
type pipelineOpts struct {
    existingState *InstanceState  // nil for create, loaded for apply
}

type pipelineResult struct {
    classified   []ClassifiedRepo
    repoStates   map[string]RepoState
    managedFiles []ManagedFile
    warnings     []string
}
```

**`Create`**:
1. Compute instance directory name (config name + optional suffix like `tsuku-2/`).
2. Assign next instance number via `NextInstanceNumber`.
3. Create the instance directory.
4. Call `runPipeline` with `existingState: nil`.
5. Write fresh `InstanceState` with `Created = now`.
6. Return the instance root path.

**`Apply`**:
1. Load existing state from `instanceRoot` -- error if missing (not an instance).
2. Call `runPipeline` with the loaded state.
3. Diff previous state repos against current classified repos:
   - Repos in state but not in classified: remove managed files for that repo.
   - Groups in state but not in classified: remove managed files + empty group dir.
4. Reconcile managed files: remove files from previous state that are no longer
   generated (group/repo was removed from config).
5. Write updated `InstanceState` preserving `Created` and `InstanceNumber`.

### Key behaviors

| Behavior | Create | Apply |
|----------|--------|-------|
| Instance dir | Creates new | Requires existing |
| Instance number | Assigned fresh | Preserved from state |
| Clone repos | Always clones all | Skips existing (idempotent) |
| Content files | Generates all | Regenerates all (overwrite) |
| Removed repos | N/A (fresh start) | Deletes managed files |
| Removed groups | N/A | Deletes managed files, removes empty dir |
| State file | Fresh with Created=now | Updates LastApplied, preserves Created |
| Drift detection | Skipped | Warns on modified managed files |

### File changes

- `internal/workspace/apply.go`: Refactor `Apply` into `Create`, `Apply`, and
  `runPipeline`. Add `pipelineOpts`, `pipelineResult` types. Add cleanup helper
  for removed repos/groups.
- `internal/cli/apply.go`: Route to `Create` or `Apply` based on whether
  instance state exists at the target path.
- `internal/cli/init.go`: No changes needed -- init only scaffolds config, it
  does not run the pipeline.
- `internal/workspace/apply_test.go`: Split existing `TestApplyIntegration` into
  create and apply variants. Add test for repo removal cleanup.

## Alternatives considered

### A: Mode flag on Apply

Add a `mode` parameter to the existing `Apply` method:
```go
func (a *Applier) Apply(ctx, cfg, configDir, root string, mode ApplyMode) error
```

Rejected because it keeps the method monolithic and adds conditional branching
throughout. The behavioral differences (directory creation, cleanup) are distinct
enough to warrant separate entry points.

### B: Separate Applier structs (Creator + Applier)

Two structs sharing an embedded pipeline type. Rejected as over-engineered --
both operations need the same GitHub client and cloner, and the shared pipeline
is better expressed as a method than inheritance.

### C: Strategy pattern with interface

Define a `LifecycleStrategy` interface with `PrePipeline` and `PostPipeline`
hooks, pass it to a generic `Run` method. Rejected as unnecessary abstraction
for exactly two strategies with well-understood differences.

## Confidence

High. The current code already has the seams for this split: the existing state
check (lines 40-41, 166-169) is the natural branch point, and the pipeline steps
(lines 43-161) are already sequential without mode-dependent branching. The
refactor is mechanical.
