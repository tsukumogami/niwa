---
status: Draft
problem: |
  After niwa create, users must manually cd into the workspace directory because
  compiled binaries can't change the parent shell's working directory. There's no
  way to jump to a specific repo within a workspace either. The predecessor tools
  (newtsuku/resettsuku) solved this with shell functions, establishing UX patterns
  that niwa users expect but that niwa doesn't yet provide.
goals: |
  Users can create workspaces and land in them with a single command, navigate
  between workspaces and repos, and manage shell integration lifecycle (install,
  uninstall, status) -- all optionally, with niwa working fully without shell
  integration.
source_issue: 31
---

# PRD: Shell Integration

## Status

Draft

## Problem Statement

Niwa is a compiled Go binary. When a user runs `niwa create`, the binary creates
a workspace instance directory but can't change the parent shell's working
directory -- a fundamental Unix constraint. The user must manually cd to the new
workspace, copy-pasting the path from niwa's output.

The predecessor tools (newtsuku and resettsuku) were shell functions that handled
this transparently: create a workspace and land in it, reset a workspace and
return to the same repo. Niwa users coming from these tools expect the same
single-command workflow.

Beyond navigation, there's no shell completion support for niwa commands, and no
structured way to navigate between existing workspaces or repos within them.

## Goals

1. **Single-command workspace entry**: `niwa create` lands the user in the new
   workspace without extra steps
2. **Workspace and repo navigation**: users can jump to any workspace or repo
   within a workspace by name
3. **Shell completions**: tab completion for niwa commands and arguments
4. **Optional by design**: niwa works fully without shell integration; users
   opt in explicitly and can opt out at any time
5. **Predecessor parity**: preserve the core UX contract from newtsuku (create +
   navigate) while improving on it (lifecycle commands, completions, go command)

## User Stories

### Setup

**US-1**: As a new user running install.sh, I get shell integration automatically
so that `niwa create` lands me in the workspace without extra configuration.

**US-2**: As a CI operator, I can install niwa without the shell function wrapper
(`--no-shell-init`) so my environment stays predictable and minimal.

**US-3**: As a user who installed niwa via tsuku, I can enable shell integration
with `niwa shell-init install` without re-installing niwa.

**US-4**: As an existing niwa user, I get shell integration automatically when I
upgrade, without changing my shell config files.

### Daily Use

**US-5**: As a developer, when I run `niwa create`, my shell automatically
changes to the new instance directory.

**US-6**: As a developer, I can use `niwa create -r <repo>` to land directly in
a specific repo within the new instance.

**US-6a**: As a developer, I can run `niwa create <workspace>` from anywhere to
create a new instance in a named workspace without being inside it.

**US-7**: As a developer working inside an instance, I can run `niwa go <repo>`
to jump to a different repo within my current instance.

**US-8**: As a developer, I can run `niwa go` (no arguments) to return to the
root of my current workspace.

**US-8a**: As a developer, I can run `niwa go <workspace>` to jump to a different
workspace's root directory.

**US-8b**: As a developer, when a name matches both a repo in my current instance
and a registered workspace, `niwa go <name>` prefers the repo. I can use
`niwa go -w <name>` to force workspace resolution.

**US-9**: As a developer, I get tab completions for niwa subcommands, flags,
workspace names, and repo names.

### Lifecycle Management

**US-10**: As a user, I can check whether shell integration is active with
`niwa shell-init status`.

**US-11**: As a user, I can disable shell integration with
`niwa shell-init uninstall` and re-enable it with `niwa shell-init install`.

**US-12**: As an advanced user, I can run `niwa shell-init bash` to see the exact
shell code being evaluated, for auditing or manual setup.

## Requirements

### Functional Requirements

**R1 (Shell function wrapper)**: When shell integration is active, a `niwa()`
shell function intercepts cd-eligible subcommands (`create`, `go`), captures the
binary's stdout (a directory path), and calls `cd` to that path. All other
subcommands pass through to the binary unchanged.

**R2 (Stdout protocol)**: cd-eligible commands print a single absolute directory
path to stdout on success. Human-readable output (progress, confirmations, errors)
goes to stderr. On failure, stdout is empty and exit code is non-zero.

**R3 (shell-init subcommand)**: `niwa shell-init <bash|zsh|auto>` prints shell
code to stdout containing the wrapper function and cobra-generated completions.
`auto` detects the shell from environment variables (`$BASH_VERSION`,
`$ZSH_VERSION`). Unrecognized shells produce empty output with exit code 0.

**R4 (shell-init install)**: `niwa shell-init install` writes the shell
integration delegation block to `~/.niwa/env` (creating the file if needed) and
adds the source line to shell rc files if absent. Idempotent.

**R5 (shell-init uninstall)**: `niwa shell-init uninstall` rewrites `~/.niwa/env`
to contain only the PATH export, removing the delegation block. The source line
in rc files is preserved (still needed for PATH).

**R6 (shell-init status)**: `niwa shell-init status` reports whether the wrapper
is loaded in the current shell (`_NIWA_SHELL_INIT` env var) and whether
`~/.niwa/env` contains the delegation block.

**R7 (install.sh --no-shell-init)**: install.sh accepts `--no-shell-init` to
write a PATH-only env file without the delegation block. The source line is still
added to rc files.

**R8 (Env file delegation)**: `~/.niwa/env` bootstraps PATH and optionally
delegates to `niwa shell-init auto` via a `command -v` guard. The file is
overwritten on each install.sh run, serving as the automatic upgrade mechanism.

**R9 (create command)**: `niwa create` accepts an optional positional argument
(workspace name) and an optional `-r/--repo <repo>` flag:
- No positional arg: discovers workspace from cwd, creates instance, lands at
  instance root
- With workspace arg (`niwa create <workspace>`): resolves workspace via global
  registry, creates instance, lands at instance root. Works from anywhere.
  Unrecognized workspace name produces an error listing registered workspaces.
- With `-r <repo>` flag: overrides the landing target to a specific repo within
  the new instance, resolved after the classification pipeline completes
- The `--name <suffix>` flag for custom instance naming is unchanged
- Positional arg combined with `-r` is valid: `niwa create <workspace> -r <repo>`

**R10 (go command)**: `niwa go` is a real binary subcommand that resolves
navigation targets and prints a directory path to stdout. Without the shell
wrapper, the path is printed but cd doesn't happen. Long-form flag aliases
`--workspace` and `--repo` are provided for scripting readability. The command
uses context-aware resolution with two flags (`-w`, `-r`):
- No arguments: workspace root from cwd (walks up to find workspace.toml)
- Single positional argument (`niwa go <target>`): tries as repo name in current
  instance first, then as workspace name in global registry. If both match,
  prefers current-instance repo. "Inside instance" means cwd is anywhere within
  an instance directory tree, including group subdirectories.
- `-w <name>` flag: forces workspace resolution via registry, bypassing
  repo lookup. Use to disambiguate when a name matches both a repo and a
  workspace
- `-r <repo>` flag: explicit repo targeting within the current instance. Equivalent
  to the single-arg repo case but unambiguous
- `-w <workspace> -r <repo>`: repo within the first instance of a named workspace.
  If the workspace has zero instances, error with guidance to run `niwa create`.
- No multi-positional forms. Workspace and repo targeting beyond the single-arg
  case uses flags. Passing more than one positional argument is an error.
- Positional arg combined with `-w` or `-r` is an error: use one targeting
  mechanism, not both

**R10a (go resolution feedback)**: On successful resolution, `niwa go` prints a
short resolution trace to stderr indicating which path was taken (e.g.,
`go: repo "niwa" in tsukumogami-4` or `go: workspace "codespar"`). When a name
matches both a repo and a workspace and the repo wins, the trace includes a hint:
`go: repo "tsuku" in tsukumogami-4 (also a workspace; use -w to navigate there)`.

**R10b (error messages)**: All error messages from go and create must include
recovery guidance. Errors state what failed, why, and what command to try instead.
When a registry lookup fails, the error lists registered workspace names. When
repo lookup fails, the error suggests `niwa status` to list available repos.

**R11 (Runtime hint)**: When a cd-eligible command runs and `_NIWA_SHELL_INIT` is
unset, niwa prints a hint to stderr suggesting `niwa shell-init install`. The hint
fires only on cd-eligible commands, not every invocation.

**R12 (Completions)**: Shell completions are bundled in the `niwa shell-init`
output. A single eval line provides both the wrapper function and completions.
Completions cover subcommands, flags, and where feasible, dynamic arguments
(workspace names for `go`, repo names for `go <workspace>`).

### Non-Functional Requirements

**R13 (Shell compatibility)**: Shell integration must work in bash (3.2+ and 5.x)
and zsh (5.x). Fish is explicitly not supported in v1.

**R14 (Optionality)**: All niwa commands must work without the shell function
wrapper. The wrapper adds auto-cd behavior; it doesn't gate functionality.

**R15 (Startup performance)**: `niwa shell-init auto` must complete in under 50ms
cold / 15ms warm. Shell startup latency from the eval is kept minimal.

**R16 (Concurrent shell safety)**: The stdout protocol must be per-process with no
shared state. Multiple shells running cd-eligible commands simultaneously must not
interfere with each other.

**R17 (Path safety)**: The `go` command must validate that resolved repo paths
fall within the instance root directory. Path traversal via arguments like
`../../etc` must be rejected.

**R18 (Stdout invariant)**: cd-eligible commands must validate that the emitted
path is a single line, absolute, and contains no newline characters before
printing to stdout.

**R19 (Upgrade safety)**: The `command -v` guard in the env file must allow the
delegation block to be deployed before the `shell-init` subcommand exists in the
binary. Missing or old binaries degrade gracefully to PATH-only.

## Acceptance Criteria

### Setup
- [ ] install.sh writes env file with PATH + delegation block by default
- [ ] install.sh with `--no-shell-init` writes PATH-only env file
- [ ] `niwa shell-init install` creates env file and adds source line for tsuku users
- [ ] `niwa shell-init install` is idempotent (no duplicates on repeated runs)
- [ ] Upgrade from pre-shell-integration version: new env file content loads on next shell, no rc file changes
- [ ] Old binary + new env file: PATH set, no errors, no wrapper (graceful degradation)

### Navigation: niwa create
- [ ] `niwa create` (inside workspace) with wrapper: creates instance, cwd changes to instance root
- [ ] `niwa create tsukumogami` (from anywhere): creates instance in named workspace, cwd changes to instance root
- [ ] `niwa create nonexistent` (unknown workspace): error listing registered workspaces
- [ ] `niwa create -r niwa` with wrapper: creates instance, cwd changes to repo directory
- [ ] `niwa create tsukumogami -r niwa`: creates instance in named workspace, cwd changes to repo
- [ ] `niwa create -r nonexistent`: non-zero exit, no cd, instance still created; error message includes instance path
- [ ] `niwa create` (outside workspace, no arg): error with recovery guidance listing registered workspaces
- [ ] `niwa create` failure: non-zero exit, empty stdout, no cd
- [ ] `niwa create` without wrapper: path printed to stdout, hint on stderr

### Navigation: niwa go (no args)
- [ ] `niwa go` (inside repo): cwd changes to workspace root
- [ ] `niwa go` (inside instance root): cwd changes to workspace root
- [ ] `niwa go` (inside group directory): cwd changes to workspace root
- [ ] `niwa go` (inside workspace root): no-op (already there)
- [ ] `niwa go` (outside any workspace): error with recovery guidance listing registered workspaces

### Navigation: niwa go <target> (single arg, context-aware)
- [ ] `niwa go <repo>` (inside instance): repo found, cwd changes to repo dir; stderr shows resolution trace
- [ ] `niwa go <repo>` (inside group directory): treated as inside instance, repo found
- [ ] `niwa go <repo>` (inside workspace root, no instance): no instance context, falls through to registry, not a workspace name; error suggests using `-w -r` or navigating to an instance first
- [ ] `niwa go <repo>` (outside workspace): registry lookup, not found; error lists registered workspaces
- [ ] `niwa go <workspace>` (from anywhere): registry lookup, cwd changes to workspace root; stderr shows resolution trace
- [ ] `niwa go <workspace>` (inside that workspace): registry lookup, cwd changes to workspace root
- [ ] `niwa go <workspace>` where workspace dir was deleted but registry entry remains: error with guidance to re-init or clean registry
- [ ] `niwa go <name>` where name matches both repo and workspace (inside instance): prefers repo; stderr hint mentions workspace match
- [ ] `niwa go <name>` where name matches both repo and workspace (outside instance): prefers workspace

### Navigation: niwa go with flags
- [ ] `niwa go -w <name>` / `niwa go --workspace <name>`: forces registry lookup, cwd changes to workspace root
- [ ] `niwa go -w <name>` where name also matches a repo in current instance: workspace wins (flag forces it)
- [ ] `niwa go -r <repo>` / `niwa go --repo <repo>` (inside instance): cwd changes to repo dir in current instance
- [ ] `niwa go -r <repo>` (outside instance): error with guidance to use `-w -r` or navigate to an instance
- [ ] `niwa go -w <workspace> -r <repo>`: cwd changes to repo in first instance of named workspace
- [ ] `niwa go -w <workspace> -r <repo>` with zero instances in workspace: error with guidance to run `niwa create`

### Navigation: invalid input and edge cases
- [ ] `niwa go foo bar` (multiple positional args): error with usage hint
- [ ] `niwa go foo -w bar` (positional + `-w` flag): error explaining mutual exclusivity
- [ ] `niwa go foo -r bar` (positional + `-r` flag): error explaining mutual exclusivity
- [ ] `niwa go ../../etc`: error, path traversal rejected
- [ ] `niwa go` without wrapper: path printed to stdout, hint on stderr suggesting shell-init install
- [ ] `cd $(niwa go <target>)`: works without wrapper as manual workaround

### Lifecycle
- [ ] `niwa shell-init status`: reports wrapper loaded state + env file state
- [ ] `niwa shell-init uninstall`: env file reverts to PATH-only
- [ ] After uninstall + new shell: `type niwa` reports external command, not function
- [ ] `niwa shell-init bash` output passes `bash -n` syntax check
- [ ] `niwa shell-init zsh` output is valid zsh
- [ ] `niwa shell-init auto` in unknown shell: empty output, exit 0
- [ ] Shell function sets `_NIWA_SHELL_INIT=1`

### Completions
- [ ] Tab completion works for subcommands and flags
- [ ] Single eval line provides both wrapper and completions

### Safety
- [ ] Stdout from cd-eligible commands is exactly one absolute path line
- [ ] Concurrent `niwa create` in separate shells: no cross-contamination
- [ ] Paths with spaces and special characters: cd works correctly (double-quoting)

## Out of Scope

- **Fish shell support**: deferred to a future version. `niwa shell-init fish`
  should produce empty output or a clear "not supported" message, not broken code.
- **Tsuku post-install shell mechanism**: tsuku platform decision, not niwa's
  responsibility. If tsuku later adds shell.d/ sourcing, niwa benefits
  automatically.
- **Interactive repo selection**: no TUI or fzf-style pickers. The `-r` flag and
  go command cover repo navigation.
- **apply navigation**: `niwa apply` is non-destructive (updates in place), so
  the user's cwd stays valid. No cd behavior needed, unlike resettsuku which
  destroyed and recreated the workspace.
- **Changes to niwa's core commands** beyond stdout/stderr protocol changes for
  create and the new go command.

## Known Limitations

- **Env file is overwritten on install**: users who customize `~/.niwa/env` lose
  customizations on upgrade. This is documented, not mitigated.
- **Shell startup hang**: if the niwa binary hangs during `shell-init auto`, new
  terminals block. Recovery: `bash --norc` or `zsh -f`. This is inherent to the
  eval-init pattern (same risk as direnv, mise, zoxide).
- **-r failure doesn't roll back create**: `niwa create -r bad-repo` creates the
  instance successfully but doesn't navigate. The instance exists; the user must
  navigate manually or use `niwa go`.
- **No direct instance targeting in v1**: navigating to a specific instance (e.g.,
  tsukumogami-4 vs tsukumogami-2) requires manual cd. A `-i` flag can be added
  later if demand arises.
- **No usage tracking for multi-instance workspaces**: `niwa go <workspace>` with
  multiple instances falls back to the first instance if no usage data exists.

## Decisions and Trade-offs

**D1: niwa owns shell integration, not tsuku.** Tsuku has no post-install shell
mechanism, and adding one costs 200+ LOC across two repos with only one consumer.
Cobra handles completions per-tool. Tsuku generalization deferred.

**D2: Shell integration is optional with explicit lifecycle.** install.sh defaults
to enabled (with `--no-shell-init` opt-out). For other install methods,
`niwa shell-init install` is the explicit opt-in. This balances convenience for
install.sh users with control for everyone.

**D3: apply has no navigation behavior.** Unlike resettsuku (which deleted and
recreated), niwa apply updates in place. The user's cwd stays valid, so post-apply
cd is unnecessary. This is a deliberate departure from the predecessor.

**D4: -r failure leaves the instance in place.** Creating the instance is a
successful operation. Failing to resolve `-r` is a navigation error, not a
creation error. The instance exists and can be navigated to with `niwa go`.

**D5: Unified -r flag for repo targeting across go and create.** Both `niwa go`
and `niwa create` use `-r <repo>` for repo targeting. This replaces the
predecessor's `-c <repo>` flag and the earlier `--cd <repo>` proposal. The shared
flag means the same thing in both commands: "land in this repo."

**D6: go is a real binary subcommand, not wrapper-only.** `niwa go` resolves paths
in the binary and prints to stdout, following the zoxide pattern. Without the
wrapper, the path is still printed and useful for scripting (`cd $(niwa go ...)`).
This preserves R14 (niwa works without wrapper).

**D7: Context-aware single-arg resolution for go.** `niwa go <target>` tries
current-instance repo first, then registry workspace lookup. This keeps the
common case short (repo jumps within an instance) while still supporting
cross-workspace navigation. The `-w` flag forces workspace resolution for the
rare collision case where a name matches both a repo and a workspace.

**D8: No-args go targets workspace root.** `niwa go` with no arguments navigates
to the workspace root (the directory containing workspace.toml). This is
consistent with `niwa go <workspace>` which also targets the workspace root.

**D9: Two flags only: -w and -r.** The go and create commands share `-r` for repo
targeting. The go command adds `-w` for workspace disambiguation. No `--instance`
flag in v1 -- direct instance targeting is the least common navigation case and
can be added later without changing the existing surface. Multi-instance
disambiguation for workspace lookups uses "prefer current instance if inside one,
fall back to first (lowest-numbered)."

**D10: create accepts optional workspace positional arg.** `niwa create` takes an
optional workspace name as a positional argument, resolved via the global registry.
This lets users create instances from outside the workspace (`niwa create
tsukumogami`). Without the argument, workspace discovery from cwd is used (existing
behavior).
