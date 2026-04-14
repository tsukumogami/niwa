---
status: Proposed
problem: |
  niwa's shell integration emits cobra's static completion today, so subcommand
  names (niwa cre<tab> -> niwa create) already complete. What it does not do is
  resolve identifiers from runtime state: workspace names from the global registry,
  instance names from the current workspace, repo names from the current instance.
  Eleven positions across ten commands accept such identifiers.
decision: |
  Attach cobra `ValidArgsFunction` / `RegisterFlagCompletionFunc` closures for
  11 identifier positions across 10 commands. Closures live in
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

Proposed

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
  should cover 13 of the 14 identifier positions. Duplication across command
  files is a smell; the helpers belong in a single `internal/cli/completion.go`.

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
  no shared helpers. Violates the "three helpers cover 13 of 14 positions"
  constraint.

**Chosen: Option 4 — closures in `internal/cli/completion.go`; data-listing
functions in the packages that own the data.**

- `internal/cli/completion.go` holds `completeWorkspaceNames`,
  `completeInstanceNames`, `completeRepoNames` — each a thin wrapper that
  resolves cwd/workspace context and produces cobra's
  `([]string, ShellCompDirective)` result.
- `workspace.EnumerateRepos(instanceRoot string) ([]string, error)` is added
  next to `workspace.EnumerateInstances`, which it mirrors in shape.
- `config.ListRegisteredWorkspaces() []string` is added, returning sorted
  registry keys.

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
   helper. 11 of 14 identifier positions get dynamic completion in v1
   (skipping `create -r`, `config set global <repo>`, `create --name` — all
   free-form or pre-existence positions).

2. **Data layer (Decision 4).** Two new exported functions:
   `workspace.EnumerateRepos(instanceRoot) ([]string, error)` consolidates
   the two-level group scan that `findRepoDir` inlines today; four existing
   callers (`findRepoDir`, `niwa create`, `niwa go` context-aware,
   `niwa go -r`) migrate to it. `config.ListRegisteredWorkspaces() []string`
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
