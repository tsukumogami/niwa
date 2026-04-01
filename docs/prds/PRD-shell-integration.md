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

**US-5**: As a developer, when I run `niwa create --from <template>`, my shell
automatically changes to the new workspace directory.

**US-6**: As a developer, I can use `niwa create --from <template> --cd <repo>`
to land directly in a specific repo within the new workspace.

**US-7**: As a developer working inside an instance, I can run `niwa go <repo>`
to jump to a different repo within my current instance without repeating the
workspace name.

**US-8**: As a developer, I can run `niwa go` (no arguments) to return to the
root of my current instance.

**US-8a**: As a developer, I can run `niwa go <workspace>` to jump to a different
workspace's instance. If I'm already in that workspace, I stay in my current
instance. If I'm outside it, I land in the first instance.

**US-8b**: As a developer with multiple instances of a workspace, I can use
`niwa go --instance <name>` to jump to a specific instance by name.

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

**R9 (create landing directory)**: `niwa create` defaults to landing at the
instance root. A `--cd <repo>` flag overrides the landing target to a specific
repo within the instance, resolved after the classification pipeline completes.

**R10 (go command)**: `niwa go` is a real binary subcommand that resolves
navigation targets and prints a directory path to stdout. Without the shell
wrapper, the path is printed but cd doesn't happen. The command uses
context-aware resolution:
- No arguments: current instance root from cwd
- Single argument: try as repo name in current instance first, then as workspace
  name in global registry. If both match, prefer current-instance repo
  (`--workspace` flag forces registry lookup)
- Two arguments (target + repo): resolve target as workspace, then find repo
  within an instance of that workspace
- `--instance <name>` flag: select a specific instance when multiple exist
- `--workspace <name>` flag: disambiguate when a single arg could be a repo
  name or workspace name

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

### Navigation
- [ ] `niwa create --from x` with wrapper: shell cwd changes to instance root
- [ ] `niwa create --from x --cd repo` with wrapper: shell cwd changes to repo directory
- [ ] `niwa create --from x --cd nonexistent`: non-zero exit, no cd, workspace still created
- [ ] `niwa create` failure: non-zero exit, empty stdout, no cd
- [ ] `niwa go` (inside instance): cwd changes to current instance root
- [ ] `niwa go` (outside any instance): error with "not inside a workspace instance"
- [ ] `niwa go <repo>` (inside instance): cwd changes to repo dir in current instance
- [ ] `niwa go <workspace>` (outside workspace): cwd changes to an instance of that workspace
- [ ] `niwa go <workspace>` (inside that workspace): stays in current instance
- [ ] `niwa go <workspace> <repo>`: cwd changes to repo dir within workspace instance
- [ ] `niwa go --instance <name>`: cwd changes to specific named instance
- [ ] `niwa go --workspace <name>`: forces registry lookup even if name matches a repo
- [ ] `niwa go <arg>` where arg matches both a repo and workspace: prefers current-instance repo, suggests --workspace
- [ ] `niwa go ../../etc`: error, path traversal rejected
- [ ] `niwa go` without wrapper: path printed to stdout, hint on stderr suggesting shell-init install
- [ ] `cd $(niwa go <repo>)`: works without wrapper as manual workaround

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
- **Interactive repo selection**: no TUI or fzf-style pickers. The --cd flag and
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
- **--cd failure doesn't roll back create**: `niwa create --from x --cd bad-repo`
  creates the workspace successfully but doesn't navigate. The workspace exists;
  the user must navigate manually or use `niwa go`.
- **No usage tracking for multi-instance workspaces**: `niwa go <name>` with
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

**D4: --cd failure leaves the workspace in place.** Creating the workspace is a
successful operation. Failing to resolve --cd is a navigation error, not a
creation error. The workspace exists and can be navigated to with `niwa go`.

**D5: Predecessor flag rename (-c to --cd).** The predecessor's `-c <repo>` flag
becomes `--cd <repo>` for clarity. The single-letter flag was ambiguous and didn't
follow niwa's flag naming conventions.

**D6: go is a real binary subcommand, not wrapper-only.** `niwa go` resolves paths
in the binary and prints to stdout, following the zoxide pattern. Without the
wrapper, the path is still printed and useful for scripting (`cd $(niwa go ...)`).
This preserves R14 (niwa works without wrapper).

**D7: Context-aware single-arg resolution for go.** `niwa go <arg>` tries
current-instance repo first, then registry workspace lookup. This avoids
redundant workspace names for the common case (jumping between repos in the
current instance). `--workspace` flag available for disambiguation.

**D8: No-args go targets instance root, not workspace root.** The instance root
is where CLAUDE.md lives and is the natural "home" for a working session. The
workspace root is a container directory with no working content.

**D9: Multi-instance disambiguation uses "prefer current, fall back to first."**
No visit tracking introduced. If cwd is inside an instance of the target workspace,
stay in it. Otherwise pick the first (lowest-numbered) instance. `--instance` flag
for explicit selection.
