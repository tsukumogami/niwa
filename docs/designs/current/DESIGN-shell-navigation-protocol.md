---
status: Accepted
problem: |
  The shell wrapper's stdout-as-cd protocol assumes niwa's entire stdout is
  the landing path. Any other stdout content — from subprocesses, third-party
  libraries, verbose flags, or CI output modes — silently breaks navigation.
  The current implementation already has this bug, and future output modes
  make it worse.
decision: |
  The shell wrapper communicates the landing path to the CLI via a temp file
  whose path is passed in the NIWA_RESPONSE_FILE environment variable. The CLI
  writes the path to that file when the variable is set; the wrapper reads and
  deletes the file after the command exits, then calls cd. Stdout and stderr
  flow to the terminal unmodified.
rationale: |
  Stdout and stderr are shared streams that niwa cannot fully control —
  subprocesses, third-party libraries, and future output modes all write to
  them. A dedicated file descriptor is cleaner conceptually but unreliable (fd
  availability cannot be guaranteed in all shell contexts). The temp file is
  a private channel owned by the wrapper from creation to deletion, eliminating
  the class of problem rather than managing it.
---

# DESIGN: Shell Navigation Protocol

## Status

Accepted

## Context and Problem Statement

`niwa create` and `niwa go` are "cd-eligible" commands: after they run, the
shell should change directory to the workspace or repo they operated on. Because
a subprocess (the niwa binary) cannot change its parent shell's working
directory directly, this requires a protocol between the binary and the shell
wrapper.

The current protocol: the shell wrapper captures all of niwa's stdout via `$()`
and treats the entire output as a directory path to `cd` into. This works when
stdout contains only the path but fails silently whenever anything else is
written to stdout — git clone progress, `fmt.Printf` calls in the workspace
package, or output from third-party libraries.

The fundamental problem is that we can't control what all code paths spawned
from the CLI entry point will print to stdout. Any future log level flag,
verbose mode, CI output mode, or upstream library could add stdout output and
silently break navigation again. The contract must survive this environment: it
needs to let niwa communicate the landing directory reliably even when stdout
contains other output.

## Decision Drivers

- **Shell compatibility**: The mechanism must be parseable by a POSIX shell
  function (bash/zsh) without external tools beyond what ships with the OS.
- **Output transparency**: Progress output, error messages, and diagnostic
  lines should still reach the terminal — the protocol must not suppress them.
- **Future output modes**: The contract must survive `--verbose`, `--debug`,
  `--json`, `NIWA_LOG_LEVEL`, and CI/quiet modes that may be added later.
- **Subprocess independence**: We cannot require that every subprocess niwa
  spawns routes its output to a specific FD. Third-party code will write where
  it writes.
- **Minimal shell complexity**: The shell wrapper function must remain
  maintainable by contributors who aren't shell experts.

## Considered Options

### Decision 1: Protocol Mechanism

The fundamental challenge is that the shell wrapper needs to receive a single
directory path from the niwa binary, but it cannot do so by reading stdout or
stderr — both streams are shared with subprocesses and future output modes that
niwa cannot control. A "private channel" between the wrapper and the binary is
required.

The options split into two families: stream-based protocols (parse a shared
stream and extract the path) and out-of-band protocols (use a channel that
never touches stdout or stderr). Stream-based protocols require the wrapper to
filter or parse a channel that third-party subprocesses may write to without
warning; out-of-band protocols isolate the communication entirely.

A dedicated file descriptor (fd 3 or similar) is a conceptually clean
out-of-band channel, but fd availability cannot be guaranteed — editors, CI
runners, Docker entrypoints, and shell multiplexers may have additional fds
open. Silent corruption of an unrelated open file is a worse failure mode than
a missed `cd`. A temp file avoids this: the channel is a filesystem path the
wrapper creates and controls, and the CLI writes to a path it received from the
wrapper. Neither stdout nor stderr is involved.

#### Chosen: Option D — Temp file via environment variable

Before invoking the niwa binary, the shell wrapper creates a temp file with
`mktemp` and exports its path in `NIWA_RESPONSE_FILE`. The CLI detects this
variable and, on success, writes only the landing path to that file. The
wrapper reads the file after the command exits, calls `cd`, and removes the
file. Stdout and stderr flow unmodified to the terminal throughout.

The wrapper structure:

```sh
niwa() {
    case "$1" in
        create|go)
            local __niwa_tmp __niwa_dir __niwa_rc
            __niwa_tmp=$(mktemp) || { command niwa "$@"; return; }
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
}
```

The CLI side checks `os.Getenv("NIWA_RESPONSE_FILE")` and, when set, writes
the landing path there instead of to stdout. When `NIWA_RESPONSE_FILE` is
absent, the CLI behaves as before (stdout path output), preserving backward
compatibility for scripts that call niwa directly.

#### Alternatives Considered

**Option A (stdout sentinel line):** The wrapper reads niwa's stdout line by
line, extracts a `NIWA_CD=<path>` marker, and passes remaining lines through to
the terminal. Rejected because the CLI cannot redirect stdout from third-party
subprocesses — git, gh CLI, and other tools write directly to fd 1. Any
subprocess output could appear in the filtered stream, and shell loop-based
line filtering in POSIX bash/zsh is fragile with binary output and large
buffers.

**Option B (stderr sentinel line):** Same structure as Option A but on stderr.
Rejected for the same reason: stderr is a shared stream (git already writes
progress there, and the current codebase uses it for diagnostics). The
filtering logic carries the same fragility as Option A.

**Option C (dedicated file descriptor):** The wrapper opens fd 3 before
invoking niwa; niwa writes the path to `/dev/fd/3`. Rejected because fd 3 may
already be open in editor terminals, CI runners, Docker containers, or
multiplexers. If fd 3 is taken, writes either corrupt an unrelated open file or
fail with EBADF — both silent failures. The temp file delivers the same stream
isolation without the fd availability assumption.

### Decision 2: Sentinel Line Format (Evaluated, Not Applicable)

During design, sentinel line formats were evaluated in case a stream-based
protocol was chosen. This decision is not applicable to the chosen temp-file
approach — the file contains only the path with no need to distinguish it from
other output. The evaluation is preserved here for context.

If a sentinel approach were ever adopted (e.g., as a compatibility fallback),
the recommended format is `NIWA_CD=<absolute_path>`, extracted with
`grep '^NIWA_CD=' | cut -d= -f2-`. This matches the idiom used by direnv and
mise, is trivially emitted from Go as `fmt.Fprintf(w, "NIWA_CD=%s\n", path)`,
and handles paths containing `=` correctly because `cut -d= -f2-` returns
everything from the first `=` to end-of-line.

Formats considered and rejected: double-colon prefix (`::niwa-cd::/path`) —
marginally better collision resistance with no practical benefit over a
namespaced key-value; URL-scheme prefix (`NIWA://cd /path`) — embedded space
complicates extraction; non-printable delimiters — `grep`/`sed` behaviour with
control characters varies across POSIX implementations.

## Decision Outcome

**Chosen: D (temp file via env var)**

### Summary

The shell wrapper and niwa binary communicate the landing path through a temp
file whose path is passed via the `NIWA_RESPONSE_FILE` environment variable.
Before running a cd-eligible command (`create`, `go`), the wrapper calls
`mktemp`, exports the path in `NIWA_RESPONSE_FILE`, runs the binary, reads the
temp file, removes it, and calls `cd` if the file contains a valid directory.
Stdout and stderr from the binary and all its subprocesses flow unmodified to
the terminal; the protocol never touches them.

The CLI side is opt-in: when `NIWA_RESPONSE_FILE` is set, cd-eligible commands
write the landing path to that file instead of stdout. When absent, they behave
as before (stdout), so scripts that call `dir=$(niwa go workspace)` directly
continue working without modification. The variable is set only by the shell
wrapper, so interactive users get the new protocol automatically after
re-running `niwa shell-init install`.

`mktemp` failure is handled explicitly: if `mktemp` fails, the wrapper falls
back to running niwa without navigation rather than silently breaking the
command. If the CLI exits non-zero or doesn't write the file (e.g., an error
path before the landing path is determined), the wrapper reads an empty or
absent file and skips `cd`. Neither failure is silent or data-corrupting.

The `shellWrapperTemplate` constant in `internal/cli/shell_init.go` is updated
to the new wrapper body. Users must re-run `niwa shell-init install` once to
pick up the new wrapper — the release notes should call this out explicitly.
The CLI changes to `create.go` and `go.go` are mechanical: check
`NIWA_RESPONSE_FILE`, write there if set, otherwise write to stdout as before.

### Rationale

Stdout and stderr are shared streams that niwa cannot police. Any subprocess
can write to them, and future output modes will add more. The temp-file channel
is owned by the wrapper from creation to deletion — the binary writes to a path
it received, and the wrapper reads from a path it created. No shared stream is
involved. This is the only option in the evaluated set that genuinely eliminates
the class of problem rather than managing it.

## Solution Architecture

### Overview

The protocol separates concerns cleanly: the shell wrapper owns the communication
channel (temp file), the CLI owns the protocol implementation (detecting
`NIWA_RESPONSE_FILE` and writing to it). Neither touches the other's output
streams. Stdout and stderr pass through the wrapper unmodified in both directions.

### Components

**Shell wrapper** (`internal/cli/shell_init.go` → `~/.niwa/env`)

The `niwa()` shell function is the protocol initiator. For cd-eligible commands
(`create`, `go`), it:
1. Creates a temp file with `mktemp`
2. Exports `NIWA_RESPONSE_FILE` pointing to that file
3. Runs `command niwa "$@"` with the env var in scope
4. Reads the temp file content after the binary exits
5. Removes the temp file
6. Calls `builtin cd` if the file contained a valid directory path

For all other commands, it delegates directly to `command niwa "$@"` with no
wrapping.

**Protocol writer** (Go, `internal/cli/`)

A shared helper function (e.g., `writeLandingPath(cmd *cobra.Command, path string) error`)
checks `os.Getenv("NIWA_RESPONSE_FILE")`. If set, it writes the absolute path
to that file and returns. If not set, it writes the path to stdout as before
(preserving scripting compat). Both `create.go` and `go.go` call this helper
instead of writing directly to `cmd.OutOrStdout()`.

### Key Interfaces

**Environment variable contract**

```
NIWA_RESPONSE_FILE=<absolute-path-to-temp-file>
```

- Set by: the shell wrapper before invoking the binary
- Read by: the CLI's `writeLandingPath` helper
- Absent means: write landing path to stdout (backward compat for scripts)
- File content: a single absolute directory path followed by a newline, nothing else

**Shell wrapper pseudocode**

```sh
# cd-eligible path
__niwa_tmp=$(mktemp) || { command niwa "$@"; return $?; }
NIWA_RESPONSE_FILE="$__niwa_tmp" command niwa "$@"
__niwa_rc=$?
__niwa_dir=$(cat "$__niwa_tmp" 2>/dev/null)
rm -f "$__niwa_tmp"
[ $__niwa_rc -eq 0 ] && [ -n "$__niwa_dir" ] && [ -d "$__niwa_dir" ] && builtin cd "$__niwa_dir"
return $__niwa_rc
```

**Go helper**

```go
// writeLandingPath writes path to NIWA_RESPONSE_FILE if set,
// or to stdout otherwise (for backward compat with scripts).
// NIWA_RESPONSE_FILE must point inside $TMPDIR or /tmp; other paths are rejected.
func writeLandingPath(cmd *cobra.Command, path string) error {
    if f := os.Getenv("NIWA_RESPONSE_FILE"); f != "" {
        tmpDir := os.Getenv("TMPDIR")
        if tmpDir == "" {
            tmpDir = "/tmp"
        }
        if !strings.HasPrefix(f, tmpDir+"/") && !strings.HasPrefix(f, "/tmp/") {
            return fmt.Errorf("NIWA_RESPONSE_FILE %q is outside temp directory", f)
        }
        return os.WriteFile(f, []byte(path+"\n"), 0o600)
    }
    fmt.Fprintln(cmd.OutOrStdout(), path)
    return nil
}
```

The CLI must also unset `NIWA_RESPONSE_FILE` from its own environment before
spawning any subprocess, to prevent inheritance:

```go
os.Unsetenv("NIWA_RESPONSE_FILE")
```

This call belongs in the CLI initialisation path (e.g., `PersistentPreRunE` on
the root command) so it runs before any subprocess exec, regardless of which
command is invoked.

### Data Flow

```
Shell wrapper
  → mktemp → /tmp/niwa-XXXX (empty)
  → export NIWA_RESPONSE_FILE=/tmp/niwa-XXXX
  → command niwa create -r myrepo
      → [git clone progress → stderr → terminal]
      → [fmt.Fprintf progress → stderr → terminal]
      → writeLandingPath("/home/user/ws/myrepo")
          → os.WriteFile("/tmp/niwa-XXXX", "/home/user/ws/myrepo\n")
  → cat /tmp/niwa-XXXX → "/home/user/ws/myrepo"
  → rm /tmp/niwa-XXXX
  → builtin cd /home/user/ws/myrepo
```

## Implementation Approach

### Phase 1: CLI protocol writer

Add `writeLandingPath` to `internal/cli/` and update `create.go` and `go.go`
to use it. Remove the direct `fmt.Fprintln(cmd.OutOrStdout(), landingPath)`
calls. Add unit tests: when `NIWA_RESPONSE_FILE` is set, nothing is written to
stdout and the file receives the path; when absent, stdout gets the path.

Deliverables:
- `internal/cli/landing.go` — `writeLandingPath` helper + tests
- Updated `internal/cli/create.go`
- Updated `internal/cli/go.go`

### Phase 2: Shell wrapper update

Update `shellWrapperTemplate` in `internal/cli/shell_init.go` to the new
temp-file-based wrapper. Update `shell_init_test.go` to verify the new wrapper
body.

Deliverables:
- Updated `internal/cli/shell_init.go`
- Updated `internal/cli/shell_init_test.go`

### Phase 3: Fix existing stdout pollution (issue #48)

Route workspace progress output away from stdout. Two distinct pollution types
need separate fixes:

**Direct writes in `apply.go`**: six `fmt.Printf` progress lines ("cloned X",
"pulled Y", "skipped Z") write directly to process stdout. Change these to
`fmt.Fprintf(os.Stderr, ...)`.

**Subprocess stdout routing in `clone.go`, `setup.go`, `sync.go`,
`configsync.go`**: these files set `cmd.Stdout = os.Stdout` on exec.Cmd
instances, causing subprocess stdout (git clone progress, hook output, git
pull) to inherit the process's stdout fd. Change `cmd.Stdout = os.Stdout`
to `cmd.Stdout = os.Stderr` in each.

This is belt-and-suspenders — the new protocol doesn't require clean stdout,
but correct routing is good practice and keeps the old stdout-capture path
working for scripts that call `$(niwa go ...)` directly without the wrapper.

Deliverables:
- Updated `internal/workspace/apply.go` (6 `fmt.Printf` → `fmt.Fprintf(os.Stderr, ...)`)
- Updated `internal/workspace/clone.go` (`cmd.Stdout = os.Stdout` → `cmd.Stdout = os.Stderr`, lines 61 and 70)
- Updated `internal/workspace/setup.go` (`cmd.Stdout = os.Stdout` → `cmd.Stdout = os.Stderr`, line 105)
- Updated `internal/workspace/sync.go` (`cmd.Stdout = os.Stdout` → `cmd.Stdout = os.Stderr`, lines 68 and 88)
- Updated `internal/workspace/configsync.go` (`cmd.Stdout = os.Stdout` → `cmd.Stdout = os.Stderr`, line 42)

## Security Considerations

The primary security surface is the temp file lifecycle and the `NIWA_RESPONSE_FILE`
trust model.

**NIWA_RESPONSE_FILE injection.** A process that can set environment variables
before invoking niwa (e.g., a CI runner propagating the variable from an outer
session) could point `NIWA_RESPONSE_FILE` at an arbitrary file. The destructive
case is concrete: if it points at `~/.bashrc`, niwa overwrites it with a
workspace path string. Two required mitigations:

1. `writeLandingPath` must validate that the path begins with `$TMPDIR` (or
   `/tmp` as fallback) and reject anything outside the temp directory with an
   error rather than silently writing.
2. The Go binary must unset `NIWA_RESPONSE_FILE` from its own environment
   before spawning any subprocess (git, gh, setup hooks). Every child process
   inherits the variable otherwise. A buggy or malicious child that writes to
   the path before the wrapper reads it would redirect the `cd` target.

`NIWA_RESPONSE_FILE` must be documented as an internal protocol variable not
intended for direct use by callers.

**Temp file TOCTOU and symlink attacks.** Between `mktemp` creating the file
and the Go binary writing to it, an attacker could attempt to replace the file
with a symlink. In practice this requires predicting a cryptographically random
suffix — not feasible. On Linux/macOS, the sticky bit on `/tmp` prevents other
users from deleting or replacing the file even if they discover the name.
`os.WriteFile` does not use `O_NOFOLLOW`, so a symlink attack would succeed if
the path were predictable; it is not. For high-assurance environments, the
implementation could open the file with `O_WRONLY|O_TRUNC` after verifying via
`Lstat` that the entry is a regular file, not a symlink.

**Path injection in shell.** The wrapper assigns the file content to
`__niwa_dir` via command substitution and passes it to `builtin cd` with
double-quotes, preventing word splitting and glob expansion. The Go binary's
existing `validateStdoutPath` guard — which rejects non-absolute paths and
paths containing newlines — closes the injection surface before any content
reaches the file. No changes to the current validation logic are required.

**Residual risk.** Low. All identified risks require either an already-compromised
environment (env var injection) or cryptographically infeasible prediction
(symlink). The design does not introduce privilege escalation, network exposure,
or new dependencies.

## Consequences

### Positive

- Navigation is resilient to any stdout/stderr output regardless of source —
  verbose flags, debug modes, CI output, and third-party library output all
  become safe.
- The channel is explicit and owned: wrapper creates it, binary writes to it,
  wrapper reads and deletes it. No hidden coupling through shared streams.
- Backward compatibility for scripts is preserved: `$(niwa go workspace)` still
  captures the landing path on stdout when `NIWA_RESPONSE_FILE` is absent.
- Failure modes are explicit: `mktemp` failure falls back to running niwa
  without navigation; CLI errors skip the file write; wrapper reads an empty file
  and skips `cd`. None are silent or data-corrupting.

### Negative

- Users must re-run `niwa shell-init install` once to pick up the new wrapper.
  Users on the old wrapper continue to see broken navigation until they upgrade.
- A temp file is created and destroyed for every `create` or `go` invocation,
  adding a filesystem operation per call (negligible in practice).
- `NIWA_RESPONSE_FILE` is inherited by every subprocess niwa spawns unless
  explicitly unset. This is mitigated by the required unsetting step in Phase 1.

### Mitigations

- Release notes must prominently document the required `niwa shell-init install`
  re-run. The `shell-init status` command can detect old wrappers and warn.
- Temp file overhead is immaterial for interactive CLI use.
- `NIWA_RESPONSE_FILE` is unset from the Go process environment before any
  subprocess exec (required, not optional — see Security Considerations).
