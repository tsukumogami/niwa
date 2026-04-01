---
status: Proposed
problem: |
  After niwa create, users must manually cd into the workspace directory because
  a compiled binary cannot change the parent shell's working directory. The tool
  needs shell integration that wraps certain commands with a shell function to
  enable transparent navigation.
decision: |
  Add a `niwa shell-init <shell|auto>` subcommand that emits a shell function
  wrapper and cobra completions. The wrapper intercepts `create` and a new `go`
  command, capturing stdout (a bare directory path) and calling cd. The existing
  ~/.niwa/env file delegates to shell-init via a command -v guard, so existing
  users get shell integration automatically on upgrade.
rationale: |
  Follows the eval-init pattern proven by zoxide, direnv, and mise. The stdout
  protocol is race-condition free and requires under 15 lines of shell code. Env
  file delegation avoids rc file changes for existing users. Tsuku generalization
  was considered but deferred -- no second consumer exists, and cobra handles
  completions per-tool.
---

# DESIGN: Shell Integration

## Status

Proposed

## Context and Problem Statement

Issue #31 identifies a UX gap: after `niwa create`, users land in the same
directory they started in and must manually cd to the new workspace. This is a
fundamental constraint of compiled binaries -- child processes cannot modify the
parent shell's working directory.

Exploration confirmed that the eval-init pattern (`eval "$(tool init shell)"`) is
the dominant modern approach for compiled CLIs needing shell integration. Zoxide
is the closest analog: binary resolves a path, shell function does cd. The
pattern also handles completions, which cobra already generates for free.

The exploration also resolved a broader question: whether tsuku should provide a
general post-install shell integration mechanism. Research found tsuku has no
such capability today (no action for sourceable files, no auto-sourcing), and
generalizing would cost 200+ lines across two repos with no second consumer.
Cobra's built-in completion commands remove completions as a validating use case.
Niwa should own its shell integration.

The remaining design questions are:
- Binary-to-shell communication protocol (how the binary tells the wrapper "cd here")
- Shell-init subcommand structure and output format
- Relationship to the existing ~/.niwa/env file and install.sh
- Which subcommands the shell function intercepts
- Whether completions bundle into the init output

## Decision Drivers

- **Proven patterns over novel design**: the eval-init pattern is battle-tested
  across zoxide, direnv, mise, starship, and others
- **Minimal shell code**: the binary should generate shell glue, not maintain
  hand-written bash/zsh scripts
- **Both bash and zsh required**: fish support can be deferred
- **Transparent UX**: users should type `niwa create` and land in the workspace
  without remembering special syntax
- **Fragility and race conditions**: the communication protocol between binary and
  shell function must handle concurrent shells, failed commands, and output format
  changes
- **Install simplicity**: adding shell integration should be a one-line rc file change
- **Independence from tsuku**: niwa must work when installed standalone

## Decisions Already Made

These choices were settled during exploration and should be treated as constraints:

- **Niwa owns shell integration, not tsuku**: tsuku has no post-install shell mechanism,
  and adding one costs 200+ LOC across two repos with only one consumer (niwa).
  Completions are handled per-tool by cobra, removing the second use case.
- **Eval-init pattern is the right approach**: proven by zoxide, direnv, mise. Lets the
  binary version its shell output, handles multiple shells via a single subcommand,
  and is a known convention.
- **Tsuku generalization is deferred**: not rejected, but not warranted by current
  evidence. If more tools need post-install shell functions, revisit.

## Considered Options

### Decision 1: Init subcommand protocol and completions bundling

What does `niwa shell-init <shell>` emit, and how does the shell function know when to cd?

Key assumptions:
- Niwa has no stable stdout contract. No known scripts parse the `Created instance:`
  format. Changing stdout for cd-eligible commands is not a breaking change.
- At most 2 subcommands need cd behavior. The shell function's case statement stays small.

#### Chosen: Stdout path with stderr messages (zoxide pattern)

cd-eligible subcommands (initially `create` and `go`) print the target directory
path to stdout and human-readable messages to stderr. The shell function captures
stdout, verifies it's a non-empty existing directory, and runs `builtin cd`.

`niwa shell-init <shell>` emits two things concatenated:
1. A `niwa()` wrapper function that intercepts cd-eligible subcommands, captures
   stdout, and cds on success. All other subcommands pass through.
2. Cobra-generated completion registration for the active shell.

One `eval` line handles both navigation and completions.

Binary-side change for `create.go`:

```go
fmt.Fprintln(cmd.ErrOrStderr(), "Created instance:", instancePath)
fmt.Fprintln(cmd.OutOrStdout(), instancePath)
```

Shell function output for bash:

```bash
export _NIWA_SHELL_INIT=1

niwa() {
    case "$1" in
        create|go)
            local __niwa_dir
            __niwa_dir=$(command niwa "$@")
            local __niwa_rc=$?
            if [ $__niwa_rc -eq 0 ] && [ -n "$__niwa_dir" ] && [ -d "$__niwa_dir" ]; then
                builtin cd "$__niwa_dir" || return
            fi
            return $__niwa_rc
            ;;
        *)
            command niwa "$@"
            ;;
    esac
}
```

This matches zoxide's protocol exactly. Race-condition free (stdout capture is
per-process), zero file I/O, under 15 lines of shell code.

#### Alternatives Considered

- **Directive temp file (~/.niwa/.last-cd)**: Binary writes path to a shared file;
  shell function reads and deletes it. Rejected: concurrent shells can cross-read
  directives (race condition), adds file I/O overhead, stale files on crash.

- **PID-scoped temp file (/tmp/niwa-cd-$$)**: Fixes the race condition by scoping
  to shell PID. Rejected: more complex than stdout with no benefit, requires file
  I/O and cleanup, no precedent in existing tools.

- **Separate file descriptor (fd 3)**: Binary writes path to fd 3; shell function
  redirects to capture. Rejected: complex and unfamiliar shell plumbing, harder to
  debug, no known CLI uses this. Technically sound but unnecessarily novel.

### Decision 2: Env file migration strategy

What happens to `~/.niwa/env` and `install.sh` when `niwa shell-init` is introduced?

Key assumptions:
- Existing user base is very small (early-stage project).
- install.sh remains the primary installation method.
- `niwa shell-init` includes PATH setup in its output, making the env file's PATH
  export redundant once init runs (but kept as a safety net).

#### Chosen: Env file delegates to niwa shell-init

The env file becomes a stable entrypoint that bootstraps PATH and delegates to
`niwa shell-init`. install.sh updates `~/.niwa/env` on each install to contain:

```sh
# niwa shell configuration
export PATH="$HOME/.niwa/bin:$PATH"
if command -v niwa >/dev/null 2>&1; then
  eval "$(niwa shell-init auto 2>/dev/null)"
fi
```

Existing users keep their `. "$HOME/.niwa/env"` line in rc files unchanged. The
env file is already overwritten on each install, so the delegation happens
automatically on upgrade. `niwa shell-init auto` detects the running shell via
environment variables ($BASH_VERSION, $ZSH_VERSION).

The `command -v` guard makes it safe to deploy the env file change before the
`niwa shell-init` subcommand ships. If the binary doesn't support init yet, PATH is
still set correctly.

#### Alternatives Considered

- **Env file as bootstrap + separate eval line**: install.sh appends a second line
  (`eval "$(niwa shell-init bash)"`) to rc files alongside the existing source line.
  Rejected: two integration lines splits the source of truth and complicates
  install.sh with shell-specific eval logic.

- **Eval line with absolute path, env file removed**: Drop the env file entirely;
  write `eval "$("$HOME/.niwa/bin/niwa" init bash)"` to rc files. Cleanest end
  state but rejected: leaves existing users with a dead source line pointing to
  a missing env file.

- **Replace source line with eval line on upgrade**: install.sh finds and replaces
  the old source line in rc files. Rejected: modifying existing lines in user rc
  files is fragile and a common source of installer bugs.

### Decision 3: Shell function command scope

Which subcommands does the shell function intercept?

Key assumptions:
- The `go` command can reuse workspace registry infrastructure from `apply.go`
  (resolveRegistryScope, LoadGlobalConfig, LookupWorkspace).
- Two intercepted subcommands (create and go) represent the steady state.

#### Chosen: Intercept `create` + new `go` command

The shell function intercepts two subcommands:
- **`create`**: navigates to the new workspace instance after creation
- **`go [workspace] [repo]`**: navigates to existing workspaces and repos within
  them. Without arguments, navigates to the current workspace root. With a
  workspace name, resolves via the global registry. With both, navigates to
  the repo directory within the instance.

Both use the same stdout protocol. The binary handles all resolution logic; the
shell function only reads the path and calls cd.

#### Alternatives Considered

- **Intercept `create` only**: Addresses post-create navigation but leaves repo-level
  navigation (half of issue #31) unresolved. Would require shell function changes
  later when `go` is inevitably added. The incremental cost of including `go` now
  is small.

- **Generic directive protocol (intercept everything)**: Wraps all niwa invocations
  and checks for cd directives after every command. Introduces output-buffering risks
  for interactive commands. Over-engineered when only 2 commands need cd behavior.

- **Intercept `create` + `apply`**: `apply` has no clear single navigation target
  (can target multiple instances) and doesn't address repo-level navigation.

### Decision 4: Shell integration activation for non-install.sh installs

When niwa is installed via tsuku (or `go install`, or manual binary placement),
install.sh doesn't run and `~/.niwa/env` is never created. How does the shell
function wrapper get loaded?

Key assumptions:
- Users who install via tsuku already have `eval $(tsuku shellenv)` in their rc
  file and are comfortable with the eval-line pattern.
- The shell function is an enhancement, not a requirement. Niwa works without it.

#### Chosen: Document the eval line + runtime hint

For non-install.sh installs, niwa's documentation and tsuku's post-install message
tell users to add `eval "$(niwa shell-init auto)"` to their shell config. This is
the same requirement direnv, mise, and zoxide have.

As a quality-of-life enhancement, the shell-init output sets `_NIWA_SHELL_INIT=1`.
When niwa runs a cd-eligible command (`create`, `go`) and this variable is unset,
it prints a hint to stderr:

```
hint: shell integration not detected. For auto-cd and completions, add to your shell config:
  eval "$(niwa shell-init auto)"
```

The hint fires only on cd-eligible commands when the wrapper is missing — targeted
and not noisy.

#### Alternatives Considered

- **Piggyback on tsuku's shellenv**: Extend tsuku's shellenv to source a `shell.d/`
  directory for installed tools. Rejected: requires tsuku changes with no second
  consumer. Valid future tsuku feature but not niwa's problem to solve.

- **Recipe runs install.sh via run_command**: Have the tsuku recipe execute
  install.sh. Rejected: the env file is useless without the source line in rc
  files, and `run_command` can't modify user rc files.

## Decision Outcome

The four decisions compose into a clean architecture:

1. **Protocol**: cd-eligible commands print a bare path to stdout, messages to stderr.
   The shell function captures stdout and cds. Zoxide's pattern, proven and simple.

2. **Distribution (install.sh)**: The existing `~/.niwa/env` file delegates to
   `niwa shell-init auto`, so install.sh users get shell integration automatically
   on upgrade without changing their rc files.

3. **Distribution (other methods)**: Users add `eval "$(niwa shell-init auto)"` to
   their shell config, same as direnv/mise/zoxide. A runtime hint on cd-eligible
   commands prompts users who haven't set this up.

4. **Scope**: The wrapper intercepts `create` and a new `go` command, covering
   both post-create navigation and workspace/repo jumping. All other commands pass
   through to the binary unchanged.

The decisions reinforce each other: the stdout protocol keeps the shell function
simple enough that intercepting two commands is trivial. The env-file delegation
and the runtime hint cover both installation paths. And limiting scope to two
commands keeps the wrapper auditable and testable.

## Solution Architecture

### Overview

Shell integration adds a thin layer between the user's shell and the niwa binary.
The binary gains an `init` subcommand that generates shell-specific wrapper code.
Two commands (`create` and `go`) adopt a stdout protocol where they print a
directory path to stdout on success. The wrapper function captures this path and
calls `cd`. Everything else passes through unchanged.

### Components

```
User's shell
    |
    v
~/.niwa/env (sourced from .bashrc/.zshenv)
    |
    +-- export PATH="$HOME/.niwa/bin:$PATH"
    +-- eval "$(niwa shell-init auto 2>/dev/null)"  [guarded by command -v]
            |
            v
        niwa shell-init auto
            |
            +-- Detects shell ($BASH_VERSION / $ZSH_VERSION)
            +-- Emits niwa() wrapper function
            +-- Emits cobra completion registration
            |
            v
        niwa() shell function [now in parent shell]
            |
            +-- create|go --> capture stdout, cd if directory
            +-- *         --> pass through to binary
```

### Key Interfaces

**`niwa shell-init <shell|auto>` subcommand** (`internal/cli/shell_init.go`)
- Arguments: `bash`, `zsh`, or `auto` (detects from environment)
- Output: shell code to stdout (wrapper function + completions)
- No side effects (prints only, writes no files)

**Stdout protocol for cd-eligible commands**
- cd-eligible commands print a single directory path to stdout on success
- Human-readable output goes to stderr
- On failure, stdout is empty; exit code is non-zero
- The shell function checks: exit code 0 AND stdout non-empty AND path is a directory

**`niwa go [workspace] [repo]` subcommand** (`internal/cli/go.go`)
- `niwa go` -- resolve current workspace root from cwd, print to stdout
- `niwa go <name>` -- resolve workspace by name via global registry. If multiple
  instances exist for the workspace (e.g., `tsuku`, `tsuku-2`), print the most
  recently used instance. If no usage data exists, print the first (original)
  instance. Error if the workspace name is not found.
- `niwa go <name> <repo>` -- resolve repo directory within the selected instance,
  print to stdout
- Error messages to stderr; empty stdout + non-zero exit on failure

**`niwa shell-init auto` shell detection**
- Checks `$ZSH_VERSION` first (set in zsh)
- Falls back to `$BASH_VERSION` (set in bash)
- Fails silently (empty output) if shell is unrecognized

### Data Flow

For `niwa create --from example`:

```
1. Shell function intercepts "create"
2. Runs: command niwa create --from example
   - Binary clones repos, writes state, creates instance at /home/user/.niwa/instances/example
   - Stderr: "Cloning repo-a... Cloning repo-b... Created instance: /home/user/.niwa/instances/example"
   - Stdout: "/home/user/.niwa/instances/example"
   - Exit code: 0
3. Shell function captures stdout into __niwa_dir
4. Checks: exit 0, non-empty, -d passes
5. Runs: builtin cd /home/user/.niwa/instances/example
6. User's shell cwd is now the workspace
```

For `niwa go example api-service`:

```
1. Shell function intercepts "go"
2. Runs: command niwa go example api-service
   - Binary reads global config, finds instance for "example"
   - Locates repo "api-service" within the instance
   - Stderr: (nothing on success)
   - Stdout: "/home/user/.niwa/instances/example/api-service"
   - Exit code: 0
3. Shell function captures stdout, verifies directory exists
4. Runs: builtin cd /home/user/.niwa/instances/example/api-service
```

## Implementation Approach

### Phase 1: Init subcommand and stdout protocol

Add `niwa shell-init <shell|auto>` that generates the wrapper function and completion
registration. Change `create.go` to use stdout/stderr discipline.

Deliverables:
- `internal/cli/shell_init.go` -- init subcommand with bash/zsh/auto support
- `internal/cli/create.go` -- move human output to stderr, path to stdout
- Tests for init output (valid bash/zsh syntax) and create stdout protocol

### Phase 2: Env file delegation

Update `install.sh` to write the new env file content with `command -v` guard
and `niwa shell-init auto` delegation.

Deliverables:
- `install.sh` -- updated env file template
- Test that env file works when niwa is not yet on PATH (PATH-only fallback)

### Phase 3: Go command

Add `niwa go [workspace] [repo]` using the same stdout protocol.

Deliverables:
- `internal/cli/go.go` -- go subcommand with workspace/repo resolution
- Tests for workspace resolution via global registry
- Update init output to intercept `go` alongside `create`

## Security Considerations

The design follows the eval-init pattern used by zoxide, direnv, and mise. The
attack surface is small: generated shell code is static (no user-controlled
interpolation), stdout capture uses proper double-quoting, and the trust boundary
is the niwa binary itself.

Four implementation-level considerations:

**Stdout protocol contract.** cd-eligible commands must emit exactly one line
containing an absolute directory path to stdout. The Go side should validate that
the path contains no newlines before emitting (filesystem paths can't contain
newlines, but explicit validation prevents accidental multi-line output from being
misinterpreted). The shell function depends on double-quoting
(`builtin cd "$__niwa_dir"`) to prevent word splitting, glob expansion, and shell
metacharacter injection. The `-d` directory check provides secondary validation.

**Path containment for `go` subcommand.** The `go <name> <repo>` form resolves
a repo directory relative to the workspace instance root. Without validation, a
crafted repo argument like `../../etc` would resolve outside the instance via
`filepath.Join`. The `go` command must validate that the resolved path falls
within the instance root using logical-path validation (pre-symlink resolution
with `filepath.Rel`). This permits symlinked repos while blocking `../` traversal.

**Shell startup hang.** If the niwa binary hangs during `eval "$(niwa shell-init auto)"`,
new terminal sessions block indefinitely. The `2>/dev/null` suppresses stderr but
does not prevent hangs. Recovery: `bash --norc` or `zsh -f` opens a shell without
rc files. This is inherent to the eval-init pattern (same risk exists with direnv,
mise, etc.) and does not warrant a timeout wrapper.

**Binary compromise blast radius.** If the niwa binary is compromised, the eval
in the env file executes arbitrary code in every new shell. This is the standard
trust model for eval-init tools and is not unique to this design. Users can audit
by running `niwa shell-init bash` directly.

## Consequences

### Positive

- Users get transparent post-create navigation with no special syntax to remember
- Completions come for free via the same init mechanism
- Existing users get shell integration automatically on upgrade (env file overwrite)
- The stdout protocol is simple enough that adding future cd-eligible commands
  is a one-line change in the init output
- No dependency on tsuku or any external tooling

### Negative

- `niwa create` stdout changes from human-readable to a bare path. Scripts that
  parsed the old "Created instance:" format would break (no known consumers, but
  the change is not backward-compatible in principle).
- The shell function shadows the niwa binary. Users who want to call the binary
  directly must use `command niwa` -- though this is standard practice with
  shell wrappers (same as zoxide).
- `niwa shell-init auto` adds a subprocess spawn to shell startup. This is one exec
  call per new shell, typically under 10ms.

### Mitigations

- The stdout change ships alongside the init subcommand in the same release, so
  the shell function is available to handle the new format from day one.
- Shell startup cost is negligible (single exec). If it becomes a concern,
  the env file could cache the init output, but this is premature optimization.
- The `command -v` guard in the env file means the init subcommand can ship
  independently of the binary -- if the binary predates init, PATH still works.
