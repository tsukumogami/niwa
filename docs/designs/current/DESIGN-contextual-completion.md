---
status: Planned
problem: |
  niwa's shell integration emits cobra's static completion today, so subcommand
  names (niwa cre<tab> -> niwa create) already complete. What it does not do is
  resolve identifiers from runtime state: workspace names from the global registry,
  instance names from the current workspace, repo names from the current instance.
  Ten positions across ten commands accept such identifiers and are in scope for v1
  (four additional free-form positions are deferred).
decision: |
  Attach cobra `ValidArgsFunction` / `RegisterFlagCompletionFunc` closures for
  10 identifier positions across 10 commands. Closures live in
  `internal/cli/completion.go` as thin wrappers over new data-listing functions
  in the data-owning packages (`workspace.EnumerateRepos`,
  `config.ListRegisteredWorkspaces`). `niwa go [target]` decorates candidates
  with kind + instance qualifier (`tsuku -- repo in 1`, `codespar -- workspace`);
  flag variants (`-w`, `-r`) stay undecorated. `-r` under `-w` mirrors runtime
  by scoping to the sorted-first instance. No caching. Destructive commands
  complete normally. Two-tier test strategy (unit + functional via
  `niwa __complete`). Both existing install paths already ship completion, so
  no installer changes are required.
rationale: |
  Cobra v1.10.2's `__complete` pipeline is already wired through the emitted
  bash V2 and zsh scripts, so the only engineering work is attaching
  completion closures. Splitting closures (in `cli`) from data-listing
  functions (in `workspace` / `config`) keeps completion discoverable, avoids
  a new package, and cleans up four copies of the "iterate Registry keys and
  sort" idiom duplicated across existing commands. The chosen decoration and
  disambiguation strategy mirrors the existing `resolveContextAware` stderr
  hint and stays under bash V2's rendering threshold. Mirroring runtime in
  `-r` scoping prevents the "completion offers X, command refuses X" bug
  flagged in Decision Drivers. Latency measurements (Lead 3 in the
  exploration) showed raw calls stay under 5ms at realistic scale, so no
  caching layer is needed.
---

# DESIGN: Contextual Completion

## Status

Planned

## Context and Problem Statement

niwa is a cobra-based CLI for managing multi-repo workspaces. The
`niwa shell-init` command already emits a bash/zsh wrapper function plus
`GenBashCompletionV2` / `GenZshCompletion` output, so static subcommand
completion works today. Cobra v1.10.2's dynamic-completion pipeline is also
wired up end-to-end: the generated scripts call `niwa __complete <args>` on
every tab press and parse a `:<directive>` trailer — the only missing piece is
that niwa doesn't yet attach `ValidArgsFunction` or `RegisterFlagCompletionFunc`
closures to any command. As a result, `niwa go tsuk<tab>` produces nothing.

Fourteen identifier positions across ten commands are candidates for dynamic
completion (full table in `wip/research/explore_contextual-completion_r1_lead-command-flag-coverage-map.md`).
Those positions fall into three kinds, each backed by a single data source:

1. **Workspace names** — `config.LoadGlobalConfig().Registry` (the global
   config at `$XDG_CONFIG_HOME/niwa/config.toml`). 6 positions.
2. **Instance names** — `workspace.EnumerateInstances(root)` plus
   `workspace.LoadState(dir).InstanceName`. 5 positions.
3. **Repo names** — a two-level group scan currently inlined in
   `internal/cli/repo_resolve.go:findRepoDir`. 3 positions.

Two positions stay out of v1 because their data source is murky at completion
time (`niwa create -r` before the instance exists) or free-form (`niwa config
set global <repo>`, which accepts arbitrary URLs/slugs); `niwa init [name]`
completes only registered workspace names (the re-clone-from-registry case).

The architectural questions that remain open:

- **Where does the shared repo-enumeration helper live?** `findRepoDir`
  short-circuits on the first match and returns "ambiguous" for duplicates.
  Completion needs the full list. The helper should live in
  `internal/workspace/` alongside `EnumerateInstances`, and the existing
  callers (`findRepoDir`, `niwa create`, `niwa go` context-aware, `niwa go -r`)
  should migrate to it.

- **How does `niwa go [target]` handle repo/workspace collisions?** The runtime
  resolver (`resolveContextAware` in `internal/cli/go.go`) picks repo over
  workspace when both match, and prints a stderr hint. Completion must mirror
  this priority *visibly* — a user seeing a single candidate `tsuku` should
  know whether they'll land in the repo or the workspace.

- **How does `niwa go -r` scope its completion when `-w` is set?** The runtime
  picks the sorted-first instance of the named workspace and enumerates its
  repos. Completion should match that, which requires reading the already-parsed
  `-w` flag value from `cmd.Flag("workspace").Value`.

- **How does the test strategy cover completion without flaking?** `__complete`
  output includes a `:<directive>` trailer line and optionally a
  `Completion ended with directive: ...` trailer. Exact-match assertions
  against raw stdout fail. The functional harness needs a completion-specific
  parser and a `aRegisteredWorkspaceExists` step that writes to the already-
  sandboxed `XDG_CONFIG_HOME`.

## Decision Drivers

- **Correctness under cobra's protocol**: the design must use cobra's native
  `ValidArgsFunction` + `RegisterFlagCompletionFunc` + `ShellCompDirective`
  flags correctly, particularly `NoFileComp` (default fallback to filenames
  would be nonsense for identifier positions).

- **Unsurprising UX across shells**: decoration via TAB-separated descriptions
  renders well on zsh/fish/bash-V2 but degrades to plain candidates on bash V1.
  The decoration must be an enhancement, not load-bearing — users on macOS
  default bash should still get usable completion.

- **Latency budget under 100ms per tab press**: Lead 3 measurements show
  ~2ms cold start, ~3ms for a 500-workspace TOML, <5ms for scoped filesystem
  walks even at 100k repo dirs. The design must not introduce a code path
  that enumerates all repos across all instances across all workspaces — the
  only way to cross the 100ms bar at realistic scale.

- **Test trustworthiness**: the shell-navigation feature showed that
  template-only unit tests miss real behavior. Completion needs both unit
  tests (calling completion functions directly with a sandboxed
  `XDG_CONFIG_HOME`) and functional tests (invoking `niwa __complete` against
  the godog sandbox), mirroring the two-tier coverage approach.

- **Delivery without install-path changes**: both niwa's `install.sh` and the
  in-repo tsuku recipe at `.tsuku-recipes/niwa.toml` already emit
  `shell-init`'s output as part of their install flow. The design must not
  require changes to either — adding `ValidArgsFunction` to cobra commands is
  sufficient because the generated scripts already dispatch to `__complete`.

- **Minimize surface area of new helpers**: three helper functions
  (`completeWorkspaceNames`, `completeInstanceNames`, `completeRepoNames`)
  cover 10 of the 14 identifier positions niwa exposes; the bare positional
  of `niwa go` needs a fourth specialized closure. The other four positions
  are free-form and stay out of v1. Duplication across command files is a
  smell; the helpers belong in a single `internal/cli/completion.go`.

- **No friction on destructive commands**: `niwa destroy` and `niwa reset`
  should complete normally, matching `git branch -D`, `docker rm`, and
  `kubectl delete pod`. Adding completion-time confirmation is a separable UX
  concern.

## Considered Options

Eight decisions shape this design. Five were settled during exploration (cited
to the relevant research file); three were evaluated with fresh decision-skill
analysis during design (cited to the corresponding decision report). Each is
listed with its alternatives, chosen option, and rationale.

### Decision 1: Disambiguation strategy for `niwa go [target]`

When a user types `niwa go tsu<TAB>`, the name `tsuku` may resolve to a repo
in the current instance, a registered workspace, or both. The runtime
(`resolveContextAware` in `internal/cli/go.go`) picks repo over workspace when
both match and prints a stderr hint. Completion needs to mirror this priority
*visibly* so the user can pick consciously.

**Alternatives considered:**

- **Option A: Plain union.** Return the union of repos and workspaces,
  undecorated. Simple; matches git's approach. Silent collisions — a user
  sees `tsuku` without knowing the CLI will pick repo over workspace.
- **Option C: Priority-only.** Return only the higher-priority set (repos
  when inside an instance, workspaces otherwise). Hides workspace names from
  users inside an instance, breaking discoverability for `-w <ws>`.
- **Option E: Priority-first + decoration on collision.** Emit undecorated
  repos, then append workspaces as decorated if they collide with a repo
  name. Preserves discoverability and surfaces shadowing loudly, but extra
  implementation complexity without telemetry-validated need.

**Chosen: Option B — union with TAB-decorated kind.**

Return the union of repos and workspaces, each with a description after TAB
(`tsuku\trepo in 1`, `codespar\tworkspace`). Cobra renders descriptions on
bash V2, zsh, and fish; bash V1 drops them silently and users see undecorated
names — no worse than Option A. When a name resolves both ways, emit both
entries so the user sees the collision explicitly.

**Rationale:** Mirrors the existing `resolveContextAware` stderr hint (the
CLI already treats collisions as "worth mentioning, not fatal"). Graceful
cross-shell degradation. Discoverability wins over keystroke economy given
the small candidate set (tens, not thousands). Source:
`wip/research/explore_contextual-completion_r1_lead-context-aware-disambiguation.md`.

### Decision 2: Caching policy

Dynamic completion runs as `niwa __complete <args>` on every tab press — a
fresh process that parses the global config and walks the filesystem. The
question is whether to memoize reads.

**Alternatives considered:**

- **In-memory cache within a single `__complete` invocation.** Useless: each
  tab press is a new process.
- **On-disk cache keyed by `stat` mtime of config.toml and workspace roots.**
  Skip `ReadDir`s on cache hit. Adds a cache file, cache-invalidation logic,
  and test surface.
- **Dedicated lightweight code path** that skips most of `rootCmd` init to
  reduce cold-start cost.

**Chosen: No caching.**

Raw calls per tab press. Revisit only if real-world measurements contradict
the benchmarks.

**Rationale:** Measured latency (Lead 3): Go cold-start + cobra init is ~2ms;
a 500-workspace TOML parses in ~3ms; scoped filesystem walks stay under 5ms
even at 100k repo dirs. The only path that crosses the 100ms perceptibility
bar is enumerating every repo across every instance across every workspace,
which no correctly-scoped completion handler needs. Caching would be a
solution looking for a problem. Source:
`wip/research/explore_contextual-completion_r1_lead-completion-latency.md`.

### Decision 3: Destructive-command completion behavior

`niwa destroy [instance]` and `niwa reset [instance]` delete or recreate
instances. Auto-completing to a valid instance and pressing Enter is a
single-keystroke way to destroy work.

**Alternatives considered:**

- **Suppress completion** on destructive commands. Users must type the
  instance name in full.
- **Gate completion behind an env var** like `NIWA_COMPLETE_DESTRUCTIVE=1`
  (precedent: docker's `DOCKER_COMPLETION_SHOW_CONTAINER_IDS`). Off by
  default.
- **Require confirmation on selected-via-completion values.** The CLI
  detects the completion path and asks "Really destroy instance N?" even
  with the existing `--force` flag behavior.

**Chosen: Complete normally.**

`niwa destroy <TAB>` and `niwa reset <TAB>` enumerate instances with no
special treatment.

**Rationale:** Precedent from `git branch -D`, `docker rm`, and
`kubectl delete pod` — none add completion-time friction. The `--force` flag
and two-keystroke behavior (name + Enter) are the existing safety model.
Adding completion-time friction is a separable UX change; if user reports
warrant it, it can be addressed independently without changing this design.
Source: coverage map in
`wip/research/explore_contextual-completion_r1_lead-command-flag-coverage-map.md`.

### Decision 4: Completion helper organization

The three completion helpers (`completeWorkspaceNames`,
`completeInstanceNames`, `completeRepoNames`) need a home. The extracted
`EnumerateRepos` helper also needs a location.

**Alternatives considered:**

- **Option 1: `internal/cli/completion.go` alone.** Closures live in a single
  cli file; raw enumeration stays inline in command files.
- **Option 2: New `internal/completion/` package.** Closures exported from a
  dedicated package; command files import it.
- **Option 3: Per-command attachment.** Each command defines its own closure;
  no shared helpers. Violates the "three shared helpers cover most positions"
  constraint.

**Chosen: Option 4 — closures in `internal/cli/completion.go`; data-listing
functions in the packages that own the data.**

- `internal/cli/completion.go` holds `completeWorkspaceNames`,
  `completeInstanceNames`, `completeRepoNames` — each a thin wrapper that
  resolves cwd/workspace context and produces cobra's
  `([]string, ShellCompDirective)` result.
- `workspace.EnumerateRepos(instanceRoot string) ([]string, error)` is added
  next to `workspace.EnumerateInstances`, which it mirrors in shape.
- `config.ListRegisteredWorkspaces() ([]string, error)` is added, returning
  sorted registry keys. Errors are returned (rather than swallowed into an
  empty list) so that non-completion call sites can distinguish "registry
  missing" from "registry empty"; completion closures collapse the error
  into an empty list per Implicit Decision C.

**Rationale:** Three closures live in one cli file (discoverable by `grep`);
raw data enumeration lives with the package that owns the data; tests split
naturally (closures tested in `cli/completion_test.go`, data fns in their
own packages). `config.ListRegisteredWorkspaces` deduplicates four existing
copies of the "iterate Registry keys and sort" idiom in `go.go` and
`create.go` — net LOC is neutral or negative. Zero new import edges because
`cli` already imports both `config` and `workspace`. Source:
`wip/design_contextual-completion_decision_4_report.md`.

### Decision 5: Test strategy

Completion logic without tests will silently regress. How do we cover it?

**Alternatives considered:**

- **Unit tests only.** Fast, but doesn't catch wiring bugs (closure attached
  to wrong flag, directive lost in middleware).
- **Functional tests only.** Slower, less focused — any failure could be
  anywhere.
- **Add shell-live integration tests.** Spawn bash, send TAB, observe
  readline output. No cobra-using project does this because the generator is
  cobra's responsibility; ROI is poor.

**Chosen: Two-tier — unit + functional.**

- **Tier 1 (unit):** `internal/cli/completion_test.go` calls completion
  closures directly with a sandboxed `XDG_CONFIG_HOME = t.TempDir()`. Covers
  prefix filtering, malformed config, empty registry, and directive correctness.
- **Tier 2 (functional):** `test/functional/features/completion.feature`
  invokes `niwa __complete <args>` against the existing godog sandbox
  (sandboxed HOME, XDG_CONFIG_HOME, TMPDIR). Catches wiring bugs. Adds:
  - Step `aRegisteredWorkspaceExists` that writes a registry entry.
  - Helper `completionSuggestions(stdout)` that strips cobra's
    `:<directive>` trailer and the `Completion ended with directive:` line,
    and splits each candidate on TAB to drop descriptions.

**Rationale:** Reuses the existing functional harness — `buildEnv` already
routes `XDG_CONFIG_HOME` to the sandbox, which is where
`config.LoadGlobalConfig` reads. No new scaffolding. Unit tests run under
`go test ./...`; functional tests run under `make test-functional`. Source:
`wip/research/explore_contextual-completion_r1_lead-completion-test-strategy.md`.

### Decision 6: Install-path changes

For completion to ship on by default, we need to understand both install
paths.

**Alternatives considered:**

- **Modify `install.sh` to call `niwa shell-init install`** instead of
  inlining the env file content. Reduces duplication but adds a dependency
  on the newly-installed binary during install.
- **Update the tsuku recipe** to do additional wiring.
- **No changes required.**

**Chosen: No changes required.**

Both install paths already emit completion as part of their existing
shell-integration wiring.

**Rationale:**

- `install.sh` writes `~/.niwa/env` with a live
  `eval "$(niwa shell-init auto)"` line, sourced from the user's rc file.
  `shell-init auto` emits the wrapper plus cobra-generated completion in a
  single blob. Every new shell picks up the current binary's completion.
- The in-repo tsuku recipe at `.tsuku-recipes/niwa.toml` runs an
  `install_shell_init` post-install action that captures
  `niwa shell-init bash/zsh` output into
  `$TSUKU_HOME/share/shell.d/niwa.{bash,zsh}`. Cobra's completion scripts
  call back into `niwa __complete`, so runtime name resolution works even
  though the bootstrap script was captured at install time.

For dynamic completion specifically, the tsuku-captured bootstrap is stable
— only the wrapper itself would need regeneration if the emitted template
ever changes. Adding `ValidArgsFunction` closures requires no install-path
work. Source:
`wip/research/explore_contextual-completion_r1_lead-install-path-integration.md`.

### Decision 7: `niwa go -r` scoping when `-w` is set

The runtime (`resolveWorkspaceRepo`) looks up `<ws>` in the registry,
enumerates its instances, sorts them lexicographically, picks `instances[0]`,
and resolves the repo inside that single instance. Completion could mirror
that or diverge.

**Alternatives considered:**

- **Option 2: Union across all instances.** Enumerate every instance and
  union their repos. Maximum discoverability, but breaks symmetry — completion
  would suggest repos the command rejects.
- **Option 3: Require instance narrowing.** Return empty unless an
  `--instance` flag is also set. No such flag exists; inventing one
  expands scope.
- **Option 4: Current-instance priority with fallback.** If cwd is inside an
  instance of `<ws>`, use that; otherwise fall back to Option 1. Breaks
  symmetry too, and the cwd-inside-matching-workspace case is already served
  better by bare `-r` (no `-w`) which correctly uses cwd.

**Chosen: Option 1 — mirror runtime (sorted-first instance only).**

`completeRepoNames` with `-w <ws>` set: look up `<ws>` in the registry,
enumerate instances, sort, pick `[0]`, enumerate repos via
`workspace.EnumerateRepos`.

**Rationale:** Symmetry with runtime is a correctness property — every
suggestion must resolve. Only Option 1 satisfies it.
`resolveWorkspaceRepo` commits to exactly one instance and rejects any repo
not resolvable there; any broader suggestion set would produce "suggested
but rejected" results, the exact bug Decision Drivers call out. Lowest
latency and simplest implementation as well. Trade-off accepted: users with
drift between instances won't see repos unique to non-first instances — but
the runtime would reject those names anyway. If cross-instance resolution
is later desired, the fix belongs in `resolveWorkspaceRepo`, not in
completion. Completion inherits the existing lexicographic-vs-numeric sort
quirk (`"1" < "10" < "2"`) unchanged. Source:
`wip/design_contextual-completion_decision_7_report.md`.

### Decision 8: Description content in decorated candidates

Given Option B from Decision 1, what goes after the TAB in each candidate?

**Alternatives considered:**

- **Option 1: Kind only** (`tsuku -- repo`, `codespar -- workspace`).
  Shortest, but description blindness sets in fast.
- **Option 2: Kind + absolute path** (`tsuku -- repo: /ws-root/1/tsukumogami/tsuku`).
  Informative but long (40-80 chars), breaks bash V2 tabular layout.
- **Option 4: Kind + relative path** (`tsuku -- repo: ./tsukumogami/tsuku`).
  Middle ground, but "relative to what" is ambiguous; character budget
  often crosses 30 chars.
- **Option 5: No descriptions.** Reverses Option B silently.

**Chosen: Option 3 — kind + instance qualifier.**

- Repos: `tsuku\trepo in 1` (kind plus the instance number the repo lives in).
- Workspaces: `codespar\tworkspace` (kind only — workspaces have no instance
  dimension; `niwa go <ws>` navigates to the workspace root, not a specific
  instance).

**Rationale:** Stays under the 30-char wrapping threshold for bash V2's
tabular layout. Adds context that genuinely disambiguates when multiple
instances exist (instance numbers are user-facing throughout niwa). The
asymmetry between repo and workspace descriptions is semantically honest —
workspaces don't belong to an instance. No additional filesystem work
needed: instance number is derivable from the instance root path the
completion helper already has. Renders cleanly across zsh, fish, bash V2;
bash V1 drops descriptions silently, which is acceptable because candidate
names alone still resolve correctly. Source:
`wip/design_contextual-completion_decision_8_report.md`.

## Decision Outcome

The eight decisions compose into a coherent implementation plan:

1. **Surface wiring (Decision 4, 5).** A new `internal/cli/completion.go`
   holds three thin closure helpers. Each command's `init()` registers
   `ValidArgsFunction` or `RegisterFlagCompletionFunc` with the appropriate
   helper. 10 of 14 identifier positions get dynamic completion in v1
   (skipping `create -r`, `create --name`, `init --from`, and
   `config set global <repo>` — all free-form or pre-existence positions).

2. **Data layer (Decision 4).** Two new exported functions:
   `workspace.EnumerateRepos(instanceRoot) ([]string, error)` consolidates
   the two-level group scan that `findRepoDir` inlines today; four existing
   callers (`findRepoDir`, `niwa create`, `niwa go` context-aware,
   `niwa go -r`) migrate to it. `config.ListRegisteredWorkspaces() ([]string, error)`
   returns sorted registry keys and replaces four existing copies of the
   "iterate Registry keys and sort" idiom.

3. **Context-aware completion for `niwa go [target]` (Decisions 1, 7, 8).**
   `completeGoTarget` unions repos from the current instance (via
   `workspace.DiscoverInstance(cwd)` + `workspace.EnumerateRepos`) with
   workspaces from the registry. Each candidate is decorated with its kind
   and, for repos, an instance qualifier (`repo in 1`). Collisions produce
   two candidates, one per kind. `-w <ws>` completion returns undecorated
   workspace names. `-r` completion scopes to the sorted-first instance of
   the `-w` value (read via `cmd.Flag("workspace").Value`) or, if `-w` is
   unset, to `workspace.DiscoverInstance(cwd)`.

4. **Performance (Decision 2).** No caching. Raw per-tab-press calls stay
   under 5ms at realistic scale per measured benchmarks. The only path that
   would cross the 100ms bar (enumerate everything everywhere) is not
   reachable from any correctly-scoped completion handler in this design.

5. **Destructive operations (Decision 3).** `destroy` and `reset` complete
   normally; safety is the existing `--force` flag's responsibility.

6. **Tests (Decision 5).** Unit tests in `internal/cli/completion_test.go`
   drive closures directly; functional tests in
   `test/functional/features/completion.feature` invoke `niwa __complete`
   against the existing godog sandbox. One new step
   (`aRegisteredWorkspaceExists`) and one new helper
   (`completionSuggestions`).

7. **Delivery (Decision 6).** No changes to `install.sh` or the in-repo
   tsuku recipe — both already emit completion as part of their shell
   integration wiring. Attaching `ValidArgsFunction` closures is sufficient
   to ship completion to every user on their next shell start.

8. **Operational conventions.** All identifier-position completions return
   `ShellCompDirectiveNoFileComp` to prevent cobra from falling back to
   filename completion on empty results. Completion closures swallow errors
   silently (return empty list) rather than producing stderr output or
   `ShellCompDirectiveError` — shell completion semantics.

## Out of Scope

- **`create -r <repo>` completion.** The repo list lives in the workspace
  config, which may not be cloned at completion time (the command runs
  during instance creation). Revisit if users request it.
- **`config set global <repo>` completion.** The value is a free-form
  URL or `org/repo` slug with no enumerable source.
- **`create --name <suffix>` completion.** The suffix becomes part of a new
  directory name; suggesting existing values invites collisions.
- **Shell completion beyond bash and zsh.** `shell-init` already excludes
  fish / PowerShell; adding them is a separate design.
- **`ShellCompDirectiveKeepOrder` for recency ordering.** Alphabetical is
  sufficient for v1; MRU ordering would need a usage-tracking store.
- **Shadowing via duplicated entries on bash V1** (Option E from
  Decision 1). Defer until usage shows collisions are common.

## Solution Architecture

### Overview

Dynamic completion in niwa is a thin layer over cobra's native completion
pipeline. Cobra's generated shell scripts (already emitted by `niwa
shell-init bash/zsh`) dispatch every tab press to `niwa __complete <args>`,
which walks the command tree, finds the relevant `ValidArgsFunction` or
`RegisterFlagCompletionFunc` closure, and returns suggestions plus a
directive. This design attaches closures; nothing else in the pipeline
changes.

### Components

Three layers:

1. **Closure layer (`internal/cli/completion.go`).** Three package-private
   functions with cobra's `ValidArgsFunction` signature:
   - `completeWorkspaceNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)`
   - `completeInstanceNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)`
   - `completeRepoNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)`
   Plus one specialized closure for `niwa go [target]`:
   - `completeGoTarget(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)` — unions repos and workspaces with decoration.

2. **Data layer (`internal/config/`, `internal/workspace/`).** Two new
   exported functions:
   - `config.ListRegisteredWorkspaces() ([]string, error)` — sorted registry
     keys from `GlobalConfig.Registry`.
   - `workspace.EnumerateRepos(instanceRoot string) ([]string, error)` —
     sorted repo directory names (two-level group scan, matching
     `findRepoDir`'s traversal pattern).

   Existing functions already present:
   - `config.LoadGlobalConfig() (*GlobalConfig, error)`
   - `workspace.EnumerateInstances(workspaceRoot string) ([]string, error)`
   - `workspace.LoadState(instanceRoot string) (*State, error)`
   - `workspace.DiscoverInstance(cwd string) (string, error)`

3. **Wiring (per-command `init()` blocks).** Each cobra command declares its
   completion function inline at registration:
   ```go
   func init() {
       rootCmd.AddCommand(applyCmd)
       applyCmd.ValidArgsFunction = completeWorkspaceNames
       applyCmd.RegisterFlagCompletionFunc("instance", completeInstanceNames)
       // ...
   }
   ```

### Key Interfaces

**Cobra completion signature (closure layer):**

```go
type CompletionFunc = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)
```

Closures return:
- `[]string` — filtered candidates (possibly TAB-decorated with a
  description per Decision 8), each matching `toComplete` as a prefix.
- `cobra.ShellCompDirective` — always includes `ShellCompDirectiveNoFileComp`.

**Data layer additions:**

```go
// internal/config/registry.go
func ListRegisteredWorkspaces() ([]string, error) // sorted, nil on missing config
```

```go
// internal/workspace/state.go
func EnumerateRepos(instanceRoot string) ([]string, error) // sorted, deduped, sanitized
```

`EnumerateRepos` contract:

- Scans two levels of subdirectories under `instanceRoot`: each immediate
  child of `instanceRoot` is treated as a group directory, and each of its
  children that is a directory is treated as a repo.
- Skips control directories at the top level: `.niwa` and `.claude`. Skips
  any entry whose name begins with `.` at either level (matches the
  implicit convention in `findRepoDir`).
- Skips any entry whose name contains `\t`, `\n`, or characters that
  satisfy `unicode.IsControl` / `unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)`
  (see Security Considerations — Unicode sanitization).
- Returns repo names (no group prefix, no paths). Duplicate names across
  groups are returned once (a crossed-group collision is surfaced to the
  user by the runtime's ambiguous-name handling in `findRepoDir`, not here).
- Returns `[]string{}`, not nil, when the instance root is empty or
  unreadable-but-existing; returns `(nil, err)` when `os.ReadDir` on
  `instanceRoot` itself fails.
- Sort order is `sort.Strings` — stable, case-sensitive, byte-lexicographic.

`findRepoDir` keeps its existing contract (short-circuit on first match,
return "ambiguous" error on cross-group collision) by delegating internal
enumeration to `EnumerateRepos` but continuing to resolve paths and detect
ambiguity in its own body. The extraction is a refactor, not a
behavior change.

### Data Flow

```
user presses TAB
     │
     ▼
shell wrapper (loaded via shell-init)
     │
     ▼  spawns `niwa __complete <args>`
cobra __complete subcommand (auto-registered on rootCmd)
     │
     ▼  dispatches to ValidArgsFunction or RegisterFlagCompletionFunc
closure in internal/cli/completion.go
     │
     ├─── os.Getwd() for cwd (completion closures only; command closures
     │    receive cwd via cmd.Flag() or os.Getwd())
     │
     ▼
data-layer calls:
  - config.LoadGlobalConfig() + ListRegisteredWorkspaces()
  - workspace.DiscoverInstance(cwd) + EnumerateRepos(instanceRoot)
  - workspace.EnumerateInstances(workspaceRoot) + LoadState(...)
     │
     ▼  returns []string + ShellCompDirective
cobra prints one suggestion per line + :<directive> trailer
     │
     ▼
shell wrapper renders candidates in the completion menu
```

Per-tab-press cost (measured, Lead 3): ~2ms cold start, ~3ms for a
500-workspace TOML, <5ms for scoped filesystem walks even at 100k repo dirs.

### Completion coverage table (v1)

| Command | Position | Kind | Closure |
|---------|----------|------|---------|
| `niwa apply [workspace-name]` | positional | workspace | `completeWorkspaceNames` |
| `niwa apply --instance <name>` | flag | instance | `completeInstanceNames` |
| `niwa create [workspace-name]` | positional | workspace | `completeWorkspaceNames` |
| `niwa destroy [instance]` | positional | instance | `completeInstanceNames` |
| `niwa go [target]` | positional | repo + workspace | `completeGoTarget` (decorated) |
| `niwa go -w <workspace>` | flag | workspace | `completeWorkspaceNames` |
| `niwa go -r <repo>` | flag | repo | `completeRepoNames` (reads `-w` via `cmd.Flag`) |
| `niwa reset [instance]` | positional | instance | `completeInstanceNames` |
| `niwa status [instance]` | positional | instance | `completeInstanceNames` |
| `niwa init [name]` | positional | workspace (registry-only) | `completeWorkspaceNames` |

### Test layout

```
internal/cli/completion_test.go          Tier 1 — unit tests
test/functional/features/completion.feature   Tier 2 — functional tests
test/functional/steps_test.go             augmented with:
  - aRegisteredWorkspaceExists
  - theCompletionOutputContains
  - theCompletionOutputDoesNotContain
  - completionSuggestions (helper)
test/functional/suite_test.go             augmented to register the new steps
```

## Implementation Approach

### Phase 1: Data layer

Add the two enumeration helpers first. These have no dependencies and can be
verified with unit tests before completion closures exist.

Deliverables:
- `internal/config/registry.go` — add `ListRegisteredWorkspaces() ([]string, error)`.
- `internal/workspace/state.go` — add `EnumerateRepos(instanceRoot string) ([]string, error)`.
- Both enumeration helpers (including `EnumerateInstances`) apply the
  filesystem-name sanitization filter (ASCII controls + Unicode
  Cf/Zl/Zp categories — see Security Considerations).
- Unit tests for both, including sanitization coverage.
- Migrate existing call sites: four "iterate Registry keys and sort"
  copies in `go.go` and `create.go` replace inline code with
  `config.ListRegisteredWorkspaces()`. `findRepoDir` (and its three callers)
  migrate to `workspace.EnumerateRepos` for listing semantics.

### Phase 2: Closure layer + completion tests

Add `internal/cli/completion.go` with the four closures. Wire into each
command's `init()` via `ValidArgsFunction` / `RegisterFlagCompletionFunc`.

Deliverables:
- `internal/cli/completion.go` with four closures.
- Wiring edits to `apply.go`, `create.go`, `destroy.go`, `go.go`, `reset.go`,
  `status.go`, `init.go`.
- `internal/cli/completion_test.go` — unit tests covering prefix filtering,
  empty registry, missing config, directive correctness, `-r` reading `-w`,
  and `completeGoTarget` decoration/collision behavior.

### Phase 3: Functional tests

Add a new feature file and the step implementations. No production code
changes — this phase only validates that Phase 2's wiring is correct
end-to-end.

Deliverables:
- `test/functional/features/completion.feature` with scenarios covering
  the headline paths (workspace completion, instance completion, repo
  completion in current instance, `go` decoration, `go -r` under `-w`).
- `test/functional/steps_test.go` additions: `aRegisteredWorkspaceExists`,
  `theCompletionOutputContains`, `theCompletionOutputDoesNotContain`,
  helper `completionSuggestions(stdout string) []string`.
- `test/functional/suite_test.go` step registrations.

### Phase 4: Polish

Verify cross-shell behavior on bash V2, zsh, and bash V1 (manual smoke
test). Update `docs/` if any user-facing documentation describes shell
integration (a brief note that completion is on by default).

Deliverables:
- `make test-functional` runs cleanly; new completion scenarios pass.
- No changes to `install.sh` or `.tsuku-recipes/niwa.toml` (confirmed from
  exploration).
- Doc snippet update (if applicable).

## Implicit Decisions

Re-reading Solution Architecture and Implementation Approach, three
implementation choices were made in prose without explicit decision
treatment. Each had a viable alternative; documenting them keeps future
readers from wondering.

### Implicit Decision A: `EnumerateRepos` returns repo names, not paths

The helper returns `[]string` of repo directory names (e.g.,
`["api", "web"]`) rather than paths or `(group, name)` pairs.

- **Alternative:** return full paths or tuples so callers can distinguish
  `group-a/api` from `group-b/api`.
- **Chosen:** names only, dedupe on collision. Completion needs the string
  the user will type, which is the repo name. The runtime
  (`resolveContextAware`) already rejects traversal characters, so names
  are safe to surface verbatim. Ambiguous cross-group names are rare in
  practice and surfaced to the user via the same stderr hint the runtime
  already produces.

### Implicit Decision B: `completeGoTarget` as a specialized closure, not a composition

The design adds a dedicated `completeGoTarget` closure rather than
composing `completeRepoNames` and `completeWorkspaceNames` at the
per-command wiring layer.

- **Alternative:** wire `ValidArgsFunction` to an inline lambda that calls
  both helpers and concatenates.
- **Chosen:** explicit closure. Decoration logic (Option 3 from Decision 8)
  and collision handling (Option B from Decision 1) live in one named
  function that's easy to find and test. The other completion positions use
  the shared helpers directly; only `go [target]`'s bare positional needs
  this special shape, and naming it surfaces that intent.

### Implicit Decision C: Completion closures swallow errors silently

Shell completion functions return `[]string{}, ShellCompDirectiveNoFileComp`
on error rather than `ShellCompDirectiveError`.

- **Alternative:** return `ShellCompDirectiveError` so the shell surfaces a
  stderr message when completion fails. Provides debuggability but breaks
  the user's flow on every tab press during (e.g.) transient filesystem
  errors.
- **Chosen:** silent empty. Cobra's shell wrappers deal with `Error`
  directive by printing a stderr banner that the user's completion menu
  interrupts, which is worse UX than no suggestions. This matches the
  convention in cobra's own `completions_test.go`.

## Security Considerations

**Threat model.** The completion subprocess runs with the user's own UID,
reads only files the interactive CLI already reads
(`$XDG_CONFIG_HOME/niwa/config.toml` and the workspace/instance filesystem
tree), and writes only to the shell's completion buffer. No network I/O,
no sub-process execution, no writes to the filesystem. An attacker's only
reachable surface is whatever they can place under the user's workspace
root or `$XDG_CONFIG_HOME`, which means the attacker already has the
privileges that matter.

**Shell wrapper assumption.** The "no sub-process execution" claim depends
on the wrapper scripts cobra emits (`GenBashCompletionV2` and
`GenZshCompletion`) treating completion output as data, not as code to
`eval`. Bash V2 and zsh both do. Any custom shell integration that
re-evaluates candidates (e.g., a user's personal `complete -F` wrapper
around niwa's wrapper) would break this assumption; that's out of niwa's
threat model.

**Filesystem name sanitization.** `EnumerateInstances` and
`EnumerateRepos` must filter out entries whose names contain:

- `\t` or `\n` — cobra's `__complete` protocol uses these as delimiters.
- ASCII control characters (`unicode.IsControl`, includes < 0x20 and
  0x7F–0x9F).
- Unicode format / line / paragraph separators (`unicode.In(r, unicode.Cf,
  unicode.Zl, unicode.Zp)`) — rejects bidi-override (U+202A–U+202E),
  zero-width joiners (U+200B–U+200D), NEL (U+0085), line / paragraph
  separators (U+2028 / U+2029), BOM (U+FEFF), and similar spoofing
  codepoints.

Without this filter, a crafted repo or instance name could surface as
phantom candidates under cobra's parser and under the test parser. On
shared or team workspaces — where "attacker owns workspace root" is a
weaker assumption than single-user — a crafted repo name could also
visually spoof a familiar one via bidi override. The filter has no effect
on legitimate niwa-created directories (niwa allocates numeric instance
names and rejects traversal characters in repo names during clone).

**Symlink semantics.** Enumeration uses `os.Stat` (follows symlinks) to
match existing `findRepoDir` behavior. A symlinked instance directory
pointing at a slow or remote filesystem could cause a tab press to hang;
this is a denial-of-service on the user's own shell, not a privilege
boundary crossing. Users who manually create symlinks into workspaces
accept the same latency risk at runtime today. No change from
pre-feature behavior.

**Error handling.** Per Implicit Decision C, closures return an empty
candidate list on any error rather than `ShellCompDirectiveError`. This
avoids leaking pathnames or stack traces into the shell's completion
banner on transient filesystem failures.

**Resource caps (deferred).** `EnumerateInstances` and `EnumerateRepos`
walk `os.ReadDir` output unbounded; a workspace root with 100k instance
dirs would produce a multi-second tab press and a megabyte-sized
candidate list. Similarly, `LoadGlobalConfig` will parse an arbitrarily
large `config.toml`. These are DoS against the user's own shell, not
privilege boundary crossings, and realistic workspaces stay far below
any cap that would matter. Tracked as a follow-up: apply a cap (e.g.,
1000 entries) in the enumeration helpers if telemetry shows real
workloads approaching it.

**Privilege drop on `sudo niwa`.** If the user runs `sudo niwa <TAB>`,
completion runs as the UID sudo elevates to, reading that UID's
`$XDG_CONFIG_HOME` and `$HOME`. This is the standard sudo semantic
(niwa does not otherwise track the invoking user), but it means
completion results may not match the user's expectations if niwa is
typed under sudo. No mitigation planned; sudo-elevating `niwa` is not a
supported invocation pattern.

**Destructive commands.** Decision 3's choice to complete normally is a
UX trade-off, not a security one. Instance numbers are allocated by niwa
itself; an attacker who could inject a spoofed instance into the sorted
list already has workspace write access, at which point the workspace is
already compromised and completion behavior is the least of the user's
problems.

## Consequences

### Positive

- **Tab-completion works for every command identifier users actually
  type**: workspaces, instances, and repos. Typing `niwa go ts<TAB>`
  produces the right suggestions immediately, including disambiguation
  when a name collides.
- **Code cleanup alongside feature work.** Four duplicated copies of the
  "iterate Registry keys and sort" idiom collapse into
  `config.ListRegisteredWorkspaces()`. The inlined two-level group scan in
  `findRepoDir` becomes `workspace.EnumerateRepos` — a named function
  other features can use.
- **No install-path work.** Every existing niwa user gets completion on
  their next shell start, via the same shell-init machinery that already
  ships the wrapper function. No migration, no environment variable
  opt-in, no new documentation.
- **Low implementation risk.** Cobra's pipeline is stable and well-tested;
  this design only attaches closures. No new frameworks, no new
  dependencies.
- **Design-doc trail for future contributors.** Every decision has a
  recorded alternative and rationale. If UX expectations change (e.g.,
  someone wants paths in descriptions later), the design explicitly names
  the trade-off that ruled them out.

### Negative

- **Cross-shell UX varies.** Bash V1 (macOS default) silently drops
  descriptions, so users on that shell see undecorated candidates and lose
  the `repo in 1` / `workspace` disambiguation. No completion-time signal
  distinguishes a repo from a workspace of the same name.
- **Drift between instances hides repos.** `niwa go -w ws -r <TAB>`
  enumerates only the sorted-first instance's repos (Decision 7). If a
  repo exists only in instance 2 but not in instance 1, completion won't
  surface it. Matches runtime behavior but may surprise users who expect
  completion to be more permissive.
- **Lexicographic sort quirk propagates.** Sorted-first instance uses
  lexicographic order, which puts `"1" < "10" < "2"`. Completion inherits
  this quirk unchanged. Pre-existing runtime behavior, but now more
  visible.
- **Every tab press spawns a fresh `niwa` process.** ~2ms cold start on
  Linux; untested on WSL, macOS, or machines with enterprise AV hooks
  that add process-exec latency. Could feel sluggish in hostile
  environments.
- **Destructive commands have no completion-time friction.** A user
  `niwa destroy <TAB> <Enter>` destroys an instance in two keystrokes.
  Matches precedent from other CLIs, but is the feature that most
  deserves a follow-up UX review.

### Mitigations

- **Bash V1 description loss:** acknowledged in Decision 1 rationale.
  Candidate names still resolve correctly, so users only lose the
  decoration enhancement, not functionality. Option E (duplicated entries
  on bash V1) stays in the deferred list.
- **Drift between instances:** the workaround is to navigate into the
  desired instance and use bare `-r` (no `-w`), which correctly scopes to
  cwd. If future usage shows the limitation hurts, `resolveWorkspaceRepo`
  itself can be extended; completion will track.
- **Sort quirk:** tracked separately from completion. A future fix to
  `EnumerateInstances` sort order propagates through this design
  automatically.
- **Cold-start latency:** if user reports surface sluggishness, add the
  on-disk cache sketched in Lead 3's research (keyed by config.toml and
  workspace-root mtimes). Data-layer functions are already factored for
  easy memoization.
- **Destructive-command footgun:** a follow-up design can add
  confirmation, an env-var gate, or completion suppression. Separable and
  non-blocking.
