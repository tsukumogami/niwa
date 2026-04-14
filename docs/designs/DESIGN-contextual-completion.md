---
status: Proposed
problem: |
  niwa's shell integration emits cobra's static completion today, so subcommand
  names (niwa cre<tab> -> niwa create) already complete. What it does not do is
  resolve identifiers from runtime state: workspace names from the global registry,
  instance names from the current workspace, repo names from the current instance.
  Eleven positions across ten commands accept such identifiers. The design needs
  to specify how completion callbacks are wired into cobra, how they share a small
  set of data-source helpers, how disambiguation works for `niwa go [target]`
  where a name can resolve to a workspace or a repo, and how the test strategy
  extends the existing functional harness to prevent silent regression.
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

## Decisions Already Made

These choices were settled during exploration. Treat them as constraints.

- **Scope v1: 11 of 14 positions.** Include `apply [ws]`, `apply --instance`,
  `create [ws]`, `destroy [inst]`, `go [target]`, `go -w`, `go -r`,
  `reset [inst]`, `status [inst]`, `init [name]` (registry-only).
  Skip `create -r` (data source too murky pre-create), `config set global <repo>`
  (free-form URL/slug), `create --name` (free-form suffix).

- **Disambiguation for `niwa go [target]`: Option B (union + TAB-decorated
  kind).** Candidates look like `"tsuku\trepo"` and `"codespar\tworkspace"`.
  When a name matches both, emit both entries with their respective
  decorations so the user visually sees the shadowing. Matches the existing
  `resolveContextAware` stderr hint. Flag variants (`-w`, `-r`) stay
  undecorated because they are explicit kind opt-ins.

- **No caching layer.** Raw calls on every tab press. Revisit only if
  real-world measurements contradict Lead 3's benchmarks.

- **Destroy/reset complete normally.** No special friction; follow precedent
  from other CLIs.

- **Extract `EnumerateRepos(instanceRoot string) ([]string, error)` in
  `internal/workspace/`.** Migrate `findRepoDir` and its three callers to
  build on top. Completion becomes the fourth consumer.

- **Two-tier test strategy.** Unit tests in `internal/cli/completion_test.go`
  call completion functions directly against a sandboxed
  `XDG_CONFIG_HOME = t.TempDir()`. Functional tests in
  `test/functional/features/completion.feature` invoke `niwa __complete`
  against the existing godog sandbox. New step:
  `aRegisteredWorkspaceExists`. New helper: `completionSuggestions(stdout)`
  that strips the `:<directive>` trailer and the `Completion ended with
  directive:` line, and splits on TAB to drop descriptions.

- **No install-path changes required.** Both `install.sh` and the tsuku
  recipe already ship completion as part of `shell-init bash/zsh`.

- **Cobra directive policy.** All identifier-position completions return
  `ShellCompDirectiveNoFileComp` so cobra does not fall back to filename
  completion when the suggestion list is empty.

- **Flag-dependent completion for `niwa go -r`.** Read the already-parsed
  `-w` flag value via `cmd.Flag("workspace").Value.String()` inside the
  completion function. When `-w` is set, enumerate repos in the sorted-first
  instance of that workspace. When `-w` is unset, scope to
  `workspace.DiscoverInstance(cwd)`.
