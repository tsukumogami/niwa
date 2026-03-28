---
status: Accepted
problem: |
  niwa treats the workspace root as the instance -- repos are cloned directly
  alongside .niwa/. The PRD defines a two-level model where workspace roots
  contain config and instances are subdirectories with their own repo clones.
  Five lifecycle commands are missing: create, status, reset, destroy, and a
  redesigned apply with dual-scope context detection.
decision: |
  Refactor the Applier into shared runPipeline with Create and Apply as thin
  callers. Apply detects scope via marker files (instance.json first, then
  workspace.toml). Status shows summary from root, detail from instance.
  Reset and destroy share a CheckUncommittedChanges safety gate.
rationale: |
  The existing Applier already has the pipeline seams for this split. Marker
  files already disambiguate root from instance. Instance state already tracks
  everything status needs. The refactor is mechanical, not architectural.
---

# DESIGN: Instance lifecycle

## Status

Proposed

## Context and Problem Statement

niwa can init a workspace and apply config, but treats the workspace root as the instance -- repos are cloned directly alongside `.niwa/`. The PRD defines a two-level model: workspace roots contain config, instances are subdirectories with their own repo clones. This separation enables parallel workflows (main, hotfix, review) from a single config.

Five commands need to work together: `niwa create` (new instance), `niwa apply` (converge instance to config), `niwa status` (show health), `niwa reset` (destroy + recreate), and `niwa destroy` (remove permanently). Currently only apply exists, and it does too much -- it creates directories, clones repos, AND installs content in a single pass with no instance awareness.

`niwa apply` should work at two scopes: from the workspace root it applies to all instances; from within an instance it applies to that one. This requires context detection (am I at the root or inside an instance?) and instance discovery (which instances exist?).

## Decision Drivers

- **PRD R3**: workspace roots and instances are separate concepts; instances are subdirectories
- **Existing Applier**: the core pipeline (discover, classify, clone, install content) is already implemented and tested
- **Instance state exists**: `.niwa/instance.json` with managed file hashes, repo state, numbering -- already implemented
- **Zero users**: no backwards compatibility concern; apply can be refactored freely
- **Context-aware commands**: apply from root = all instances; apply from instance = that one
- **Instance naming**: first instance uses config name (e.g., `tsuku/`), subsequent are numbered (`tsuku-2/`) or named (`tsuku-hotfix/`)

## Considered Options

### Decision 1: Applier refactoring

The current `Applier.Apply()` method handles both first-run creation and subsequent re-application in a single method. It needs to split into create (fresh instance) and apply (idempotent convergence) while sharing the core pipeline.

#### Chosen: Shared runPipeline with Create and Apply as separate methods

Extract the shared logic (discover repos, classify, clone, install content) into an unexported `runPipeline` method. `Create` and `Apply` become thin entry points that handle their distinct pre/post-processing.

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

#### Alternatives considered

**Mode flag on Apply**: Add a `mode` parameter to the existing method. Rejected because it keeps the method monolithic with conditional branching for distinct operations.

**Separate structs (Creator + Applier)**: Two structs sharing an embedded pipeline type. Rejected as over-engineered -- both need the same GitHub client and cloner.

**Strategy pattern**: Define a `LifecycleStrategy` interface with pre/post hooks. Rejected as unnecessary abstraction for exactly two well-understood strategies.

### Decision 2: Context detection for apply's dual scope

Apply needs to determine whether it's running from a workspace root (apply to all instances) or from within an instance (apply to that one).

#### Chosen: Marker-file detection with instance-first priority

Detection order:
1. If `--instance <name>` flag provided, resolve from workspace root
2. Try `DiscoverInstance(cwd)` -- if found, apply to that single instance
3. Try `config.Discover(cwd)` -- if found, apply to all instances via `EnumerateInstances`
4. Error if neither found

This works because `.niwa/` directories are already disambiguated: workspace root has `workspace.toml`, instances have `instance.json`. These never coexist.

```go
type ApplyScope struct {
    Mode      ApplyMode       // ApplySingle, ApplyAll, ApplyNamed
    Instances []string        // absolute paths to instance roots
    Config    string          // path to workspace.toml
}
```

CLI usage:
- `niwa apply` -- detect from cwd
- `niwa apply myworkspace` -- resolve workspace by name, apply all instances
- `niwa apply --instance dev` -- from cwd, target specific instance
- `niwa apply myworkspace --instance dev` -- both

#### Alternatives considered

**Root-first detection**: Try config.Discover first. Rejected because running apply from within an instance would unexpectedly apply to ALL instances instead of the current one.

**Separate commands (apply-one, apply-all)**: Rejected as unnecessary command proliferation. Context detection handles this naturally.

### Decision 3: Status command

`niwa status` displays workspace health with context-aware views.

#### Chosen: Dual view -- summary from root, detail from instance

**Summary view** (from workspace root):
```
Instances:

  tsuku      5 repos   0 drifted   applied 2h ago
  tsuku-2    5 repos   1 drifted   applied 3d ago
```

**Detailed view** (from within instance or with argument):
```
Instance: tsuku
Config:   tsuku
Root:     /home/user/dev/workspace/tsuku
Created:  2026-03-25 14:30
Applied:  2026-03-27 09:15

Repos:
  niwa       cloned
  tsuku      cloned
  vision     missing

Managed files:
  CLAUDE.md                          ok
  public/CLAUDE.md                   ok
  public/niwa/CLAUDE.local.md        drifted
```

A `ComputeStatus(state *InstanceState) (*InstanceStatus, error)` function does the work. The CLI handles context detection (reusing the same pattern as apply) and formatting.

Skipped for v0.1: `--json` flag (struct is already serializable), color output, exit codes for drift.

#### Alternatives considered

**Show file hashes**: Too noisy for default view. Status labels (ok/drifted/removed) convey what matters.

**Verify repo contents (git status)**: Out of scope. niwa manages workspace structure, not repo working state.

### Decision 4: Reset and destroy

Both are destructive operations on instance directories.

#### Chosen: RemoveAll with shared CheckUncommittedChanges safety gate

Both commands accept an optional instance name. If no name is given, they detect the current instance via `DiscoverInstance(cwd)`. This lets users run `niwa destroy` or `niwa reset` from within an instance without specifying a name.

**Instance resolution** (shared by both):
1. If `<instance>` arg provided: find from workspace root by name (scan instances, match InstanceName)
2. If no arg: `DiscoverInstance(cwd)` to detect current instance
3. Error if neither resolves

**Instance validation** (before any destructive action):
- Target directory must contain `.niwa/instance.json` (prevents deleting arbitrary directories)
- State file must parse successfully (prevents acting on corrupt state)
- Target must not be the workspace root (`.niwa/workspace.toml` present = root, not instance)

**`niwa destroy [instance]`**: resolve target, validate, check uncommitted changes (unless `--force`), `os.RemoveAll(instanceDir)`.

**`niwa reset [instance]`**: same resolution and safety check, capture config source, destroy, then re-run create + apply pipeline.

**`CheckUncommittedChanges(instanceDir)`**: loads state, runs `git -C <repo> status --porcelain` for each cloned repo, returns list of dirty repo names.

Reset of local-only workspaces (no remote source) errors with a clear message since destroying the instance would lose the config.

`DestroyInstance` validates `.niwa/instance.json` exists and `.niwa/workspace.toml` does NOT exist before calling `RemoveAll` (safety against deleting arbitrary directories or workspace roots).

#### Alternatives considered

**Selective cleanup (delete only managed files)**: More surgical but adds complexity for no benefit. Instance directories are niwa-owned.

**Soft delete (rename to backup)**: Adds recovery but complicates directory structure. Users have git for repo recovery.

**Reset via git clean/reset each repo**: Preserves clone time but introduces partial states. Clean re-clone is simpler and more predictable.

## Decision Outcome

### Summary

The Applier splits into `Create` (fresh instance) and `Apply` (idempotent convergence) sharing a `runPipeline` method. Context detection uses marker files -- `instance.json` for single-instance scope, `workspace.toml` for all-instances scope. Status shows summary tables from root, detailed repo/file views from instance. Reset and destroy use `os.RemoveAll` with uncommitted-changes safety checks.

### Rationale

The existing code already has the seams for this refactoring. The pipeline steps (discover, classify, clone, install content) are already sequential and mode-independent. Marker files already disambiguate root from instance. Instance state already tracks everything status needs. The four decisions reinforce each other: Create produces the state that Apply reads, Status displays, and Reset/Destroy clean up.

## Solution Architecture

### Command flow overview

```
niwa create [--name <name>]
  1. Find workspace root (config.Discover)
  2. Compute instance name (config name + numbering)
  3. Create instance directory
  4. Run pipeline (discover, classify, clone, install content)
  5. Write fresh instance state

niwa apply [workspace] [--instance <name>]
  1. Resolve scope (ResolveApplyScope)
  2. For each instance in scope:
     a. Load existing state (error if not found)
     b. Run pipeline with existing state
     c. Diff state: clean up removed repos/groups
     d. Update instance state

niwa status [instance]
  1. Detect context (instance or root)
  2. Load state(s), compute drift
  3. Display summary or detail view

niwa reset [instance] [--force]
  1. Resolve target: if <instance> given, resolve by name from root;
     if no arg, use DiscoverInstance(cwd) to detect current instance
  2. Validate target is an instance (must have .niwa/instance.json)
  3. Check uncommitted changes
  4. Capture config source
  5. Destroy instance directory
  6. Re-run create + apply

niwa destroy [instance] [--force]
  1. Resolve target: same as reset (name arg or detect from cwd)
  2. Validate target is an instance (must have .niwa/instance.json)
  3. Check uncommitted changes
  4. Remove instance directory
```

### Package changes

**Refactored: `internal/workspace/apply.go`**
- `Apply` -> split into `Create`, `Apply`, `runPipeline`
- Add `pipelineOpts`, `pipelineResult` types
- Add cleanup logic for removed repos/groups in Apply

**New: `internal/workspace/scope.go`**
- `ResolveApplyScope(cwd string, instanceFlag string) (*ApplyScope, error)`
- `ApplyScope`, `ApplyMode` types

**New: `internal/workspace/status.go`** (extend existing)
- `ComputeStatus(state *InstanceState) (*InstanceStatus, error)`
- `InstanceStatus`, `RepoStatus`, `FileStatus` types

**New: `internal/workspace/destroy.go`**
- `ResolveInstanceTarget(cwd, nameArg string) (string, error)` -- resolve instance dir from name or cwd
- `ValidateInstanceDir(dir string) error` -- confirm .niwa/instance.json exists, workspace.toml does NOT
- `CheckUncommittedChanges(instanceDir string) ([]string, error)`
- `DestroyInstance(instanceDir string) error` -- validates before RemoveAll

**New CLI commands:**
- `internal/cli/create.go` -- cobra create subcommand
- `internal/cli/status.go` -- cobra status subcommand
- `internal/cli/reset.go` -- cobra reset subcommand
- `internal/cli/destroy.go` -- cobra destroy subcommand

**Refactored: `internal/cli/apply.go`**
- Use `ResolveApplyScope` for context detection
- Loop over instances in scope, call `Applier.Apply` for each

## Security Considerations

- **Destroy safety**: `DestroyInstance` validates `.niwa/instance.json` exists AND `.niwa/workspace.toml` does NOT exist before `os.RemoveAll`, preventing accidental deletion of arbitrary directories or workspace roots. The validation runs before any destructive operation.
- **Uncommitted changes**: reset and destroy warn before deleting repos with uncommitted work. `--force` is explicit opt-in to data loss.
- **Instance validation**: `ValidateInstanceDir` confirms the target is a real instance, not a workspace root or random directory. This catches mistakes like running destroy from the wrong directory.
- **No elevated permissions**: all operations use user-level filesystem access. No sudo, no system directories.

## Consequences

### Positive

- Complete workspace lifecycle: create, use, update, reset, destroy
- Apply from root converges all instances with one command
- Status gives visibility into workspace health without manual inspection
- Zero backwards compatibility concern enables clean API design

### Negative

- Apply refactoring touches tested code. Mitigation: the pipeline logic doesn't change, only the entry points.
- Reset of local-only workspaces isn't supported (config would be lost). Mitigation: clear error message with alternative (`destroy + init`).
- `CheckUncommittedChanges` runs `git status` in each repo, which can be slow for large repos. Mitigation: parallelizable in the future, acceptable for v0.1.
