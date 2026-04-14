# Decision: Completion helper organization

## Context

The contextual-completion design adds shell completion for 14 identifier
positions across `niwa` subcommands. Three helper functions
(`completeWorkspaceNames`, `completeInstanceNames`, `completeRepoNames`) are
expected to cover 13 of those 14 positions. We need to decide where these
helpers live and how they relate to a new `EnumerateRepos(instanceRoot)` helper
that must be extracted from the inline scan currently embedded in
`internal/cli/repo_resolve.go:findRepoDir`.

The extracted `EnumerateRepos` has four consumers: `findRepoDir` itself,
`niwa create` (via `findRepoDir`), `niwa go` context-aware resolution, and
`niwa go -r`. Completion becomes the fourth (really fifth) consumer. Package
layout today is: `internal/cli` (commands), `internal/config` (registry,
`LoadGlobalConfig`, `LookupWorkspace`, `Registry` map), `internal/workspace`
(`EnumerateInstances`, `DiscoverInstance`, state, apply, etc.). There is no
`internal/completion` package today. The `cli` package already imports both
`config` and `workspace`, so cycle risk is zero for any option that keeps
completion logic inside `cli`.

## Options Considered

### Option 1: internal/cli/completion.go
- Description: Add a single file `internal/cli/completion.go` in the existing
  `cli` package that defines the three helpers with cobra's
  `ValidArgsFunction` signature: `func(cmd *cobra.Command, args []string,
  toComplete string) ([]string, cobra.ShellCompDirective)`. Per-command files
  (`go.go`, `create.go`, `apply.go`, ...) simply reference these helpers by
  name when wiring `ValidArgsFunction`. `EnumerateRepos` is added to
  `internal/workspace/` next to `EnumerateInstances` (both are workspace-state
  enumerations over the filesystem, so they belong together).
- Pros:
  - Discoverability: a contributor grepping for `completeWorkspaceNames` or
    looking in `internal/cli` finds everything in one file.
  - No new package, no new import edge; matches the existing "one file per
    concern" pattern already used in `internal/cli` (e.g., `repo_resolve.go`,
    `landing.go`, `hint.go`, `token.go`).
  - Tests in `internal/cli/completion_test.go` can call helpers directly as
    package-private functions; no need to export anything.
  - `EnumerateRepos` lives with its siblings in `workspace/`, keeping the
    workspace package the single source of truth for filesystem layout.
  - Scales: additional helpers (e.g., `completeConfigKeys`) just add functions
    to the same file.
- Cons:
  - The `internal/cli` package keeps growing. It's already 26 files, and
    adding completion makes it the de-facto kitchen-sink package.
  - Completion closures must re-derive context (cwd, workspace root) in each
    helper, but that's intrinsic to cobra's completion contract regardless of
    where the helpers live.

### Option 2: New internal/completion/ package
- Description: Create `internal/completion/` exporting `WorkspaceNames()`,
  `InstanceNames(cwd string)`, `RepoNames(cwd, workspace string)` that each
  return `([]string, cobra.ShellCompDirective)`. Command files import
  `completion` and wire `ValidArgsFunction: completion.RepoNames`.
  `EnumerateRepos` still lives in `internal/workspace/`.
- Pros:
  - Clear separation of concerns; the `cli` package shrinks slightly.
  - Forces an API boundary that makes helpers easy to reason about in
    isolation.
- Cons:
  - Adds a new package for ~3 small functions. Violates the "minimize surface
    area" constraint from the design drivers.
  - Functions must be exported (capital-letter names), which leaks
    completion's shape into a public-ish API despite being an internal
    package.
  - The `completion` package would import `config` and `workspace`, and the
    `cli` package would import `completion` — adds two import edges for no
    behavioral gain.
  - Tests must either live in `internal/completion/` (fine) or re-export
    helpers for `cli` tests to call — more friction.
  - Discoverability is slightly worse: a contributor looking at `go.go` sees
    `completion.RepoNames` and must jump packages to read the logic. Not
    terrible, but not as direct as in-package helpers.

### Option 3: Per-command attachment
- Description: Each command defines its own completion closure inline:
  `completeGoTarget` in `go.go`, `completeApplyWorkspace` in `apply.go`,
  `completeCreateWorkspace` in `create.go`, etc. No shared helpers; each
  command re-implements the enumeration logic via `workspace.EnumerateInstances`,
  `workspace.EnumerateRepos`, and `config.LoadGlobalConfig`.
- Pros:
  - Locality: everything for a command lives in one file.
  - Allows command-specific filtering (e.g., omit workspaces with no
    instances) without branching logic in a shared helper.
- Cons:
  - Violates the design's explicit constraint: "three completion helpers
    should cover 13 of 14 identifier positions." This option forces ~6-8
    closures instead of 3.
  - Heavy duplication: workspace-name enumeration appears in `go.go`,
    `create.go`, `apply.go`, and others — every call site re-writes
    `LoadGlobalConfig()` + sort + error swallow.
  - Test surface explodes: each closure needs its own test setup, or tests
    are skipped entirely and completion quality degrades.
  - Future additions (new commands wanting workspace completion) must copy
    the closure or refactor to a helper anyway — this defers the work rather
    than avoids it.

### Option 4: Split (closures in cli, data fns in workspace/config)
- Description: The three completion closures
  (`completeWorkspaceNames`, `completeInstanceNames`, `completeRepoNames`)
  live in `internal/cli/completion.go`, but each is a thin wrapper around a
  raw data-listing function in the package that owns the data:
  `config.ListRegisteredWorkspaces()` (new, returns `[]string`),
  `workspace.EnumerateInstances()` (exists), `workspace.EnumerateRepos()`
  (new). The closures convert data-layer results into cobra's
  `([]string, ShellCompDirective)` shape and handle the cwd/workspace context
  resolution.
- Pros:
  - Same discoverability as Option 1 (completion in one `cli` file).
  - Data-listing functions are reusable outside completion: e.g.,
    `config.ListRegisteredWorkspaces()` could replace the ad-hoc
    `for name := range globalCfg.Registry { names = append(...) }` pattern
    duplicated in `go.go` (3 copies) and `create.go` (1 copy).
  - Keeps raw-data concerns with data-owning packages (workspace filesystem
    enumeration in `workspace`, registry enumeration in `config`).
  - Tests split naturally: closures tested in `cli/completion_test.go`, raw
    functions tested in their own packages.
- Cons:
  - One extra data helper (`config.ListRegisteredWorkspaces`) beyond what's
    strictly required for completion. Minor scope creep — but it replaces
    existing duplicated code, so net LOC is likely neutral or negative.
  - Two places to look when tracing a completion bug (closure in `cli`, data
    fn in `config`/`workspace`). In practice this is how `resolveWorkspaceRoot`
    already works (it wraps `config.LoadGlobalConfig` + `LookupWorkspace`),
    so the pattern is familiar.

## Decision
Chosen option: 4

### Rationale

Option 4 is the best fit against the stated constraints:

1. **Minimize surface area of new helpers.** Three completion helpers, as
   required. The added `workspace.EnumerateRepos` was already decided; the
   added `config.ListRegisteredWorkspaces` is a small (~5 line) helper that
   replaces duplication already present in `go.go` and `create.go`. Net new
   surface is minimal, and it removes existing duplication.

2. **Import hygiene.** `cli` already imports `config` and `workspace`; no new
   import edges are introduced. No cycle risk.

3. **Discoverability.** A contributor opening `go.go` sees
   `ValidArgsFunction: completeGoTarget` and one `grep` finds the closure in
   `internal/cli/completion.go`. Following the closure leads to
   `workspace.EnumerateInstances` / `EnumerateRepos` and
   `config.ListRegisteredWorkspaces` — each in the package a new contributor
   would expect.

4. **Test ergonomics.** Closures are package-private in `cli`, so
   `internal/cli/completion_test.go` (Lead 5's preference) can call them
   directly. Data functions get their own tests in
   `internal/config/registry_test.go` and
   `internal/workspace/state_test.go` — both already exist.

5. **Extension.** New commands needing completion add one line in their
   command file. New completion domains (e.g., recipe names in a future
   `niwa apply --recipe`) follow the same pattern: data fn in the owning
   package, thin closure in `cli/completion.go`.

6. **No duplication.** `workspace.EnumerateRepos` removes the inline scan in
   `findRepoDir`. `config.ListRegisteredWorkspaces` removes four copies of
   the "iterate-and-sort Registry keys" idiom. Completion closures become the
   only new code, and they're small.

Option 1 is a very close second — it shares most of Option 4's properties and
is simpler. The reason Option 4 wins is that the closures already need to
produce sorted `[]string` output from the workspace registry, and that exact
idiom is duplicated four times in the existing codebase. Introducing
`config.ListRegisteredWorkspaces()` as part of this work cleans up
technical debt at essentially zero marginal cost. If that cleanup is deemed
out of scope, Option 1 is the fallback and is strictly better than Options 2
and 3.

### Trade-offs accepted
- One additional small helper in `config` (`ListRegisteredWorkspaces`) beyond
  the strict minimum for completion. We accept this because it deduplicates
  four existing call sites.
- Completion logic is split across two layers (closure + data fn). A
  contributor debugging "why is my workspace not showing up in completion?"
  must check both the closure (filtering, context derivation) and the data
  fn (enumeration). Mitigated by keeping both layers small and well-named.
- The `internal/cli` package continues to grow. Accepted because creating a
  new package for three helpers (Option 2) adds more friction than it
  removes.

## Rejected alternatives

- **Option 1 (completion.go only, no data-fn split):** Very close second.
  Rejected only because it leaves existing "iterate Registry keys and sort"
  duplication in place. If scope needs tightening, fall back to this option
  — it still satisfies all constraints cleanly.
- **Option 2 (new `internal/completion/` package):** Rejected. Creates a new
  package for three small functions, forces them to be exported, and adds
  two import edges (`cli -> completion`, `completion -> workspace/config`)
  without behavioral benefit. Violates the "minimize surface area"
  constraint.
- **Option 3 (per-command closures):** Rejected. Directly violates the
  design driver "three completion helpers should cover 13 of 14 identifier
  positions." Produces 6-8 near-duplicate closures, multiplies the test
  surface, and defers the inevitable refactor to a helper.

## Assumptions

- `workspace.EnumerateRepos(instanceRoot string) ([]string, error)` returning
  sorted repo directory names (or paths — TBD during implementation) is the
  right signature. If instead repos are returned as `(group, repo)` pairs,
  the closure layer absorbs any formatting; the decision stands.
- The completion closures need cwd context (for instance discovery) and can
  obtain it from `os.Getwd()` inside the closure. Cobra doesn't hand cwd to
  `ValidArgsFunction`, so this is unavoidable regardless of option chosen.
- `config.ListRegisteredWorkspaces()` is in-scope for this work. If the PR
  grows too large and we split it, fall back to Option 1 and defer the
  registry-listing cleanup to a follow-up.
- Lead 5's test location preference (`internal/cli/completion_test.go`) is
  firm. If that moves, Option 2 becomes marginally more attractive because
  it pairs naturally with `internal/completion/completion_test.go`.
