# Decision: Materializer Extensibility Pattern

**Status**: decided
**Chosen**: Interface-based materializers (Alternative 1)
**Date**: 2026-03-27

## Context

niwa's apply pipeline (in `internal/workspace/apply.go`) runs: discover repos,
classify, clone, install content, write state. A new step is needed after content
installation that distributes hooks, settings, and env files to each repo. This
step should allow future distribution types to be added without modifying the
pipeline loop.

The pipeline currently collects `writtenFiles []string` across steps and builds
`ManagedFile` entries from them. Each materializer needs the same pattern:
receive config, produce files, report what it wrote.

## Alternatives Evaluated

### 1. Interface-based materializers (chosen)

Define a `Materializer` interface, register instances on `Applier`, pipeline
calls each one per-repo.

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

The `Applier` holds a `[]Materializer` slice, initialized in `NewApplier` with
the three concrete types (hooks, settings, env). The pipeline's per-repo loop
calls each materializer and collects returned file paths into `writtenFiles`.

**Pros**: Clean contract. Each materializer is independently testable. Adding a
new one means writing a struct and appending it to the slice. The `Name()` method
supports state tracking and error messages. Matches the existing content
installation pattern (function takes config, returns file paths).

**Cons**: Slightly more ceremony than bare functions. Requires a context struct.

### 2. Function-based hooks

A `[]func(MaterializeContext) ([]string, error)` slice on `Applier`.

**Pros**: Less boilerplate than an interface. No new types beyond the context.

**Cons**: No `Name()` for error messages or state tracking. Anonymous functions
are harder to test in isolation. Can't declare managed file prefixes or
categories. Loses self-documentation -- reading the Applier struct doesn't tell
you what materializers exist.

### 3. Event/plugin system

Publish/subscribe on "repo applied" events.

**Pros**: Maximum decoupling. Could support external plugins someday.

**Cons**: Massive over-engineering for 3 known materializers. Introduces async
concerns, ordering ambiguity, and error propagation complexity. The pipeline is
inherently synchronous and sequential per-repo. No current or foreseeable need
for external plugin loading.

### 4. Hardcoded steps

Add `installHooks()`, `installSettings()`, `installEnv()` calls directly in
`runPipeline`.

**Pros**: Simplest. No abstraction overhead. Easy to understand.

**Cons**: Adding a fourth materializer means editing `runPipeline` and the
per-repo loop. Duplicates the pattern of "get effective config, resolve source
paths, write files, collect paths" three times. Harder to test each distribution
type independently since they're inline in the pipeline.

## Decision

**Interface-based materializers** (Alternative 1). The reasoning:

1. **Fits the existing pattern.** The codebase already has this shape:
   `InstallWorkspaceContent`, `InstallGroupContent`, `InstallRepoContent` each
   take config + paths and return `[]string` of written files. The interface
   formalizes this for the repo-level distribution step.

2. **Right level of abstraction.** With exactly 3 materializers today and a
   stated need for extensibility, an interface is the minimum viable abstraction.
   It's not speculative -- it solves the real problem of avoiding copy-paste in
   the per-repo loop without introducing event systems or plugin registries.

3. **State tracking needs `Name()`.** The requirement that materializers declare
   which managed files they produce maps naturally to a named interface. Function
   slices would need a parallel name slice or wrapper struct, which converges on
   an interface anyway.

4. **Testability.** Each materializer gets its own `_test.go` file. The pipeline
   tests can use a mock materializer. This matches the project's existing test
   structure (e.g., `content_test.go`, `override_test.go`).

## Implementation Sketch

The `MaterializeContext` struct and `Materializer` interface go in a new file
`internal/workspace/materialize.go`. Concrete implementations (`HooksMaterializer`,
`SettingsMaterializer`, `EnvMaterializer`) each get their own file or share one.

In `runPipeline`, between Step 6 (repo content) and Step 7 (build managed files),
a new loop calls each materializer for each classified repo:

```go
// Step 6.5: Materialize hooks, settings, env per repo.
for _, cr := range classified {
    if !ClaudeEnabled(cfg, cr.Repo.Name) {
        continue
    }
    effective := MergeOverrides(cfg, cr.Repo.Name)
    repoDir := filepath.Join(instanceRoot, cr.Group, cr.Repo.Name)
    mctx := MaterializeContext{
        Config: cfg, Effective: effective,
        RepoName: cr.Repo.Name, RepoDir: repoDir, ConfigDir: configDir,
    }
    for _, m := range a.Materializers {
        files, err := m.Materialize(mctx)
        if err != nil {
            return nil, fmt.Errorf("materializing %s for %s: %w", m.Name(), cr.Repo.Name, err)
        }
        writtenFiles = append(writtenFiles, files...)
    }
}
```

`NewApplier` initializes the default slice:

```go
func NewApplier(gh github.Client) *Applier {
    return &Applier{
        GitHubClient:  gh,
        Cloner:        &Cloner{},
        Materializers: []Materializer{
            &HooksMaterializer{},
            &SettingsMaterializer{},
            &EnvMaterializer{},
        },
    }
}
```

## Risks

- **Context struct growth.** If future materializers need more fields, the
  `MaterializeContext` grows. Mitigated by keeping it a plain struct (not an
  interface) so additions are backward-compatible.

- **Ordering dependencies between materializers.** Not a concern today since
  hooks, settings, and env are independent. If ordering ever matters, the slice
  order is explicit and deterministic.
