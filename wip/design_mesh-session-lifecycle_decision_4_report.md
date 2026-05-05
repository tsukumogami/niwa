# Decision 4: Shell Wrapper Extension for `niwa session create` CWD Navigation

## Status

Decided

## Question

How does the shell wrapper extension support `niwa session create` CWD navigation
and `niwa go <repo> <session-id>`, given the current wrapper only intercepts
`$1 in (create|go)`?

## Context

The shell wrapper in `internal/cli/shell_init.go` (`shellWrapperTemplate`) emits a
shell function that matches `$1` against `create|go`. When matched, it runs the
binary with `NIWA_RESPONSE_FILE` pointing at a temp file, reads that file after the
binary exits, and `cd`s to the path it contains. All other `$1` values fall through
to a bare `command niwa "$@"`.

Two new behaviors are needed:

- **R16**: `niwa session create` must navigate the shell to the new session worktree.
  `$1 == "session"` does not match the current pattern.
- **R26**: `niwa go <repo> <session-id>` must navigate to the session worktree.
  `$1 == "go"` already matches. The Go-side change (resolving the path from the
  second positional arg) is sufficient. No wrapper change needed for R26.

Only R16 requires a wrapper change.

## Options Evaluated

### Option A: Intercept `session` at `$1`, dispatch `$2`

Extend the match to `create|go|session`. Inside the `session)` arm, check `$2`:
if `create`, run with `NIWA_RESPONSE_FILE` and cd; for all other subcommands
(`destroy`, `list`, `tree`), run without wrapping. The wrapper grows by one nested
`case` block.

### Option B: Flatten `session create` to a hidden alias `niwa session-create`

Add a hidden cobra command `niwa session-create` that is identical to
`niwa session create` but accessible as a top-level command. `niwa session create`
internally execs `niwa session-create`. The wrapper intercepts `session-create`.
Users never type the alias.

### Option C: Universal response-file interception

Always pass `NIWA_RESPONSE_FILE` to every niwa invocation. Commands that don't
navigate write nothing; the wrapper reads an empty file and skips the `cd`.
Simpler pattern match, small overhead on every call.

## Decision

**Option A.**

## Rationale

R26 needs no wrapper change — `go` already matches, and the path resolution moves
entirely to the Go binary. Only R16 requires extending the wrapper.

Option A adds exactly one nested `case` to the shell function. The dispatch on `$2`
is idiomatic POSIX shell and is easy to read. The `session destroy`, `session list`,
and `session tree` arms fall through to `command niwa "$@"` without any temp-file
overhead, matching the constraint that only cd-eligible commands run with
`NIWA_RESPONSE_FILE`. Logic stays in the Go binary: the `session create` handler
calls `writeLandingPath` exactly as `create` and `go` do today; the shell function
has no knowledge of what the path contains.

Option B splits a single user command into two cobra commands and relies on an
internal exec, making the command tree harder to understand in `--help` output and
debug sessions. It adds coupling between the CLI layer and the shell integration
layer without any benefit over A.

Option C runs `mktemp` on every niwa invocation regardless of whether it navigates.
This adds measurable overhead to high-frequency calls like `niwa status`, `niwa mesh
list`, and tab completion probes. It also changes the shell function's contract: the
current guarantee is that the default arm has zero temp-file overhead, which the
existing test (`TestShellWrapperTemplate_ProtocolStructure`) explicitly asserts.
Relaxing that guarantee for marginal simplification is not a good trade.

## Chosen Implementation

### Wrapper template change

Before (line 41 in `internal/cli/shell_init.go`):

```sh
    case "$1" in
        create|go)
            local __niwa_tmp __niwa_dir __niwa_rc
            __niwa_tmp=$(mktemp) || { command niwa "$@"; return $?; }
            NIWA_RESPONSE_FILE="$__niwa_tmp" command niwa "$@"
            __niwa_rc=$?
            __niwa_dir=$(cat "$__niwa_tmp" 2>/dev/null)
            rm -f "$__niwa_tmp"
            if [ $__niwa_rc -eq 0 ] && [ -n "$__niwa_dir" ] && [ -d "$__niwa_dir" ]; then
                builtin cd "$__niwa_dir" || return
            fi
            return $__niwa_rc
            ;;
        *)
            command niwa "$@"
            ;;
    esac
```

After:

```sh
    case "$1" in
        create|go)
            local __niwa_tmp __niwa_dir __niwa_rc
            __niwa_tmp=$(mktemp) || { command niwa "$@"; return $?; }
            NIWA_RESPONSE_FILE="$__niwa_tmp" command niwa "$@"
            __niwa_rc=$?
            __niwa_dir=$(cat "$__niwa_tmp" 2>/dev/null)
            rm -f "$__niwa_tmp"
            if [ $__niwa_rc -eq 0 ] && [ -n "$__niwa_dir" ] && [ -d "$__niwa_dir" ]; then
                builtin cd "$__niwa_dir" || return
            fi
            return $__niwa_rc
            ;;
        session)
            case "$2" in
                create)
                    local __niwa_tmp __niwa_dir __niwa_rc
                    __niwa_tmp=$(mktemp) || { command niwa "$@"; return $?; }
                    NIWA_RESPONSE_FILE="$__niwa_tmp" command niwa "$@"
                    __niwa_rc=$?
                    __niwa_dir=$(cat "$__niwa_tmp" 2>/dev/null)
                    rm -f "$__niwa_tmp"
                    if [ $__niwa_rc -eq 0 ] && [ -n "$__niwa_dir" ] && [ -d "$__niwa_dir" ]; then
                        builtin cd "$__niwa_dir" || return
                    fi
                    return $__niwa_rc
                    ;;
                *)
                    command niwa "$@"
                    ;;
            esac
            ;;
        *)
            command niwa "$@"
            ;;
    esac
```

The `create|go` arm is unchanged. The new `session)` arm delegates `session create`
through the response-file protocol and falls through all other session subcommands.

### Go-side changes

- `niwa session create` handler calls `writeLandingPath(worktreePath)` and
  `hintShellInit(cmd)`, identical to `create` and `go`.
- `niwa go` handler gains optional second-argument resolution to a session worktree
  path. No wrapper change needed.

### Test impact

`TestShellWrapperTemplate_ProtocolStructure` checks that the default arm contains no
`mktemp` or `NIWA_RESPONSE_FILE`. This remains true: the new `session)` arm is not
the default arm. The test for `"create|go)"` presence updates to
`"create|go|session"` — or the check is loosened to verify each token separately,
since the pattern now spans multiple `case` arms.

`TestShellInitBash_ValidSyntax` and `TestShellInitZsh_ValidSyntax` check for
`"create|go)"`. This string is still present in the updated template; no change
needed to those tests unless the reviewers want to add a check for `"session)"`.

## Assumptions

- The tsuku recipe for niwa distributes the updated wrapper automatically; users
  do not need to manually reinstall.
- `niwa session destroy`, `niwa session list`, and `niwa session tree` will not
  navigate the shell, so they correctly fall through to the unwrapped arm.
- The duplicated temp-file block is acceptable at this scale. If a third
  cd-eligible top-level command is added in the future, the pattern warrants
  extraction into a shared shell helper function. That refactor is out of scope here.

## Rejected Options

| Option | Reason |
|--------|--------|
| B (hidden alias) | Splits one command into two cobra entries; adds internal exec complexity; no benefit over A |
| C (universal interception) | Adds mktemp overhead to every invocation; contradicts the existing test contract for the default arm; trades performance for marginal wrapper simplification |
