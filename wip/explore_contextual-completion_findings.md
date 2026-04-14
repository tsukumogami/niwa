# Exploration Findings: contextual-completion

## Core Question

What does it take for niwa to offer tab-completion that resolves workspace,
instance, and repo names from runtime state, across every command that accepts
such an identifier, with latency that stays imperceptible, a trustworthy test
strategy, and completion wired on by default when niwa is installed?

## Round 1

### Key Insights

- **Cobra v1.10.2 already provides the whole dynamic-completion pipeline.**
  The bash V2 and zsh scripts niwa already emits in `internal/cli/shell_init.go`
  call `niwa __complete <args>` on every tab press and parse a `:<directive>`
  trailer. The only engineering work is attaching `ValidArgsFunction` and
  `RegisterFlagCompletionFunc` closures to each command. No shell-script
  changes, no regeneration with different flags. [Lead 1]

- **14 identifier positions across 10 commands reduce to 3 data sources.**
  Workspace names (`GlobalConfig.Registry` from
  `$XDG_CONFIG_HOME/niwa/config.toml`) cover 6 positions; instance names
  (`workspace.EnumerateInstances` + `LoadState`) cover 5; repo names (a
  two-level group scan currently inlined in `findRepoDir`) cover 3. Three
  helper functions cover 13 of the 14 positions. Edge case: `niwa go -r`
  completion depends on the resolved `-w` flag, which a completion function
  reads from `cmd.Flag("workspace").Value`. [Lead 2]

- **Latency is not a problem at realistic scale.** Lead 3's measurements:
  Go cold-start + cobra init is ~2ms; a 500-workspace TOML parses in ~3ms;
  scoped filesystem walks stay under 5ms even at 100k repo dirs. Only a
  pathological "enumerate every repo across every instance across every
  workspace" walk crosses the 100ms perception threshold — and no sensible
  completion handler needs that. No caching layer is needed. [Lead 3]

- **For context-aware positions, Option B (union + TAB-decorated kind) wins
  on discoverability without hurting cross-shell rendering.** Cobra's
  `name\tdescription` protocol renders as `name  -- description` in
  zsh/fish/bash-V2 and silently drops the description in bash V1. This
  mirrors the existing `resolveContextAware` stderr hint and lets users see
  "tsuku (repo)" and "tsuku (workspace)" when both exist. Flag variants
  (`-w`, `-r`) stay undecorated since they are explicit kind opt-ins.
  [Lead 4]

- **Testing fits the existing harness with minimal new scaffolding.**
  The godog `buildEnv` already sandboxes `XDG_CONFIG_HOME`, which is exactly
  where `config.LoadGlobalConfig` looks. A new step
  `aRegisteredWorkspaceExists` writes a registry entry; a new helper
  `completionSuggestions` strips the `:<directive>` trailer and
  TAB-descriptions. Unit tests in `internal/cli/completion_test.go` call
  completion funcs directly; functional tests invoke `niwa __complete`.
  [Lead 5]

- **Completion already ships on both install paths.** `install.sh` writes
  `~/.niwa/env` that evals `niwa shell-init auto` on every shell start, so
  completion updates with every new shell. The in-repo tsuku recipe at
  `.tsuku-recipes/niwa.toml` has an `install_shell_init` post-install hook
  that bakes `niwa shell-init bash/zsh` output into
  `$TSUKU_HOME/share/shell.d/niwa.{bash,zsh}`. Both paths already concatenate
  the wrapper function and cobra completion into a single `shell-init`
  output — "shell integration" and "completion" are the same artifact. No
  install-path work is required for this feature. [Lead 6]

### Tensions

- **Static bake (Path B) vs live eval (Path A) freshness.** Path B caches
  shell-init output at install time; binary upgrades outside tsuku leave the
  cache stale. This would matter only if the emitted wrapper template
  changes; for dynamic completion specifically, the baked cache just
  bootstraps the `__complete` dispatch, which calls back into the current
  binary on PATH. So practical impact for this feature is near zero.
  [Leads 1, 6]

- **Bash V1 loses decorations.** macOS ships bash 3.2, where cobra's
  description column is dropped silently. Users on that shell see
  undecorated candidates. Lead 4's verdict: acceptable — no worse than git
  today. Lead 4's Option E (emit duplicate entries for colliding names,
  one per kind) would recover the signal but adds implementation complexity
  for unverified user pain. Defer.

- **Destroy/reset completion as a footgun.** Lead 2 flagged that
  auto-completing `niwa destroy <tab>` + Enter too easily triggers
  destruction. No tension with any other lead; it is a standalone UX
  judgment. Decision (this round): match precedent (git, docker, kubectl all
  complete without friction) and do not gate completion on confirmation.

### Gaps

- Cold-filesystem latency (no sudo to drop caches). WSL2 virtiofs and
  macOS APFS not measured. Process-spawn hooks from enterprise AV can
  add 50-200ms per exec and were not measured. Not blocking — re-measure
  if users report sluggishness.

- End-to-end verification that the tsuku revert branch
  (`fix/43-revert-install-shell-init`) is obsolete after tsuku's #2225
  fix. Orthogonal to this feature; worth confirming before release.

### Decisions

See `wip/explore_contextual-completion_decisions.md` for the full list with
rationale. Summary:

- Scope v1 to 11 of 14 positions (skip `create -r`, `config set global`,
  `create --name`).
- Option B decoration for `niwa go [target]`; undecorated for flag variants.
- No special treatment for `destroy` / `reset` completion.
- No caching layer.
- 2-tier test strategy (unit + functional).
- Extract `EnumerateRepos(instanceRoot)` helper in `internal/workspace/`.
- No install-path changes required.
- Produce a design doc as the next artifact.

### User Focus

Running in `--auto` mode; no interactive checkpoint was taken. The scope file
and the user's follow-up message already framed the problem completely:
contextual completion across every command that benefits, plus delivery via
niwa's installer and the tsuku recipe. Both were addressed directly by the
leads.

## Accumulated Understanding

niwa's dynamic completion feature is **smaller than it looks**. The cobra
v1.10.2 machinery, the already-emitted bash V2 and zsh scripts, the
functional test harness, and both install paths are all ready to host
dynamic completion with no structural changes. The remaining engineering is:

1. Extract one helper (`EnumerateRepos`) in `internal/workspace/`.
2. Attach three completion helper functions
   (`completeWorkspaceNames`, `completeInstanceNames`, `completeRepoNames`)
   to 11 command positions via `ValidArgsFunction` and
   `RegisterFlagCompletionFunc`.
3. For `niwa go [target]`, decorate candidates with kind (`\tworkspace` or
   `\trepo`) so bash V2 / zsh / fish users see disambiguation at completion
   time. Flag variants stay undecorated.
4. Wire `cmd.Flag("workspace").Value` into the `-r` completion so repo
   suggestions scope correctly when `-w` is present.
5. Add unit tests in `internal/cli/completion_test.go` and a new
   `test/functional/features/completion.feature` with an
   `aRegisteredWorkspaceExists` step and a stdout parser that drops cobra's
   directive trailer.

The install-path question resolves to "already works." The UX decision
around destructive commands (`destroy`, `reset`) defaults to completing
normally per common CLI precedent. The cross-shell decoration degrades
cleanly on bash V1 without anyone losing function.

The biggest remaining unknowns are orthogonal: cold-fs / WSL / AV latency
on platforms we did not measure (deferred), and end-to-end verification of
the tsuku revert branch (deferred to release).

## Decision: Crystallize
