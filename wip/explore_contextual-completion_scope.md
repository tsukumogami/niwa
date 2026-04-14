# Explore Scope: contextual-completion

## Visibility

Public

## Core Question

What does it take for niwa to offer tab-completion that resolves workspace,
instance, and repo names from runtime state, across every command that accepts
such an identifier, with latency that stays imperceptible, a trustworthy test
strategy, and completion wired on by default when niwa is installed?

## Context

niwa is a cobra-based CLI. The `shell-init` command already emits a bash/zsh
wrapper function plus static completion via `GenBashCompletionV2` /
`GenZshCompletion`, so `niwa cre<tab>` -> `niwa create` works today.

The missing piece is **dynamic/contextual** completion: tab-completing workspace
names from the global registry, instance names from the current workspace, and
repo names from the current instance. Several commands accept these identifiers
positionally or via `-w` / `-r` flags.

Delivery matters too. Shell integration is installed via an `install_shell_init`
lifecycle hook in niwa's tsuku recipe (added recently). Users who install niwa
through the recipe flow, or via a standalone installer, should get completion
without a separate opt-in step.

## In Scope

- Dynamic completion for workspace, instance, and repo names
- Command-by-command coverage audit (positional args + flag values)
- Cobra completion mechanism and its cross-shell behavior (bash, zsh)
- Latency envelope of per-keystroke config/filesystem reads
- Disambiguation for context-aware targets (e.g., `niwa go <name>` where name
  could be repo or workspace)
- Test strategy: extending the functional test suite to cover completion
- Install-path integration: niwa's own installer and the tsuku recipe flow

## Out of Scope

- Fish, PowerShell, or other non-bash/zsh shells (niwa already ships bash/zsh
  only)
- Completion for free-form inputs like `niwa init <new-name>` (no existing
  values to suggest)
- Rewriting the existing shell wrapper function (separate concern already
  shipped via the shell-navigation-protocol design)

## Research Leads

1. **How does cobra's dynamic completion mechanism work, and where do bash and zsh diverge?**
   Need to know whether `ValidArgsFunction` + `RegisterFlagCompletionFunc` +
   the `__complete` subcommand Cobra auto-generates are sufficient, or whether
   custom bash/zsh snippets are required. Also whether the existing
   `GenBashCompletionV2` / `GenZshCompletion` output already supports dynamic
   completion or requires regeneration with additional flags.

2. **What is the full command/flag map that should get dynamic completion, and where does each data source live in the codebase?**
   Audit every cobra command in `internal/cli/` that accepts an identifier
   (workspace name, instance name, repo name). Produce a table of
   command -> argument -> data source (e.g., `config.LoadGlobalConfig().Registry`
   for workspace names, `workspace.EnumerateInstances(root)` for instances,
   filesystem walk for repos). This table becomes the scope for the design
   and later implementation.

3. **What is the latency cost of dynamic completion on every tab press?**
   `config.LoadGlobalConfig` (TOML parse), `workspace.EnumerateInstances`
   (directory read), and repo discovery (filesystem walk) run on every keystroke
   that triggers completion. Measure realistic worst cases (many workspaces,
   many instances, many repos). Determine whether raw calls are acceptable or
   whether caching / a lightweight code path is needed. Find the cliff.

4. **How should tab-completion behave for context-aware targets like `niwa go <name>`, where `<name>` could resolve to a repo OR a workspace?**
   Design question: should completion show both sets union'd? Prefixed or
   suffixed to mark kind? Should it favor the higher-priority match in
   `resolveContextAware`? What does a good UX look like when the user is
   inside an instance vs. outside one? Affects the feature's feel more than
   any other decision.

5. **What testing strategy exists for shell completion in cobra projects, and how do we extend niwa's functional test suite to cover it?**
   The shell-navigation feature set up godog-based pwd assertions by invoking
   bash with a sentinel line. Completion needs equivalent black-box coverage
   (invoke `niwa __complete` or drive the completion function directly from a
   wrapped shell, assert the suggestion set). Without it, completion will
   silently regress. Survey approaches used by other cobra-based projects
   and sketch what this would look like in `test/functional/features/`.

6. **How does completion land on a user's machine by default across niwa's install paths?**
   Two paths to cover:
   - niwa's own installer: `niwa shell-init install` exists as a manual
     command. Is it invoked automatically during install (from some other
     install script), or must the user run it themselves?
   - The tsuku recipe for niwa: `install_shell_init` lifecycle hook was
     recently added. What does it emit today — just the wrapper, or the
     wrapper plus completion output from `shell-init bash/zsh`?
   
   Also: are "shell integration" (wrapper for cd navigation) and "completion"
   one artifact or two? The install flow should probably handle them together
   — clarify that first, then map out what changes so completion is on by
   default after either install path. Include idempotency: what happens if a
   user installs, then re-installs, or has an existing niwa integration block
   in their shell RC file.
