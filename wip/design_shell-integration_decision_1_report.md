<!-- decision:start id="shell-init-protocol" status="assumed" -->
### Decision: Shell function communication protocol and completions bundling

**Context**

After `niwa create`, users must manually cd into the new workspace. The binary
can't change the parent shell's working directory -- this is a fundamental Unix
constraint. The exploration phase chose the eval-init pattern: users add
`eval "$(niwa init bash)"` to their shell rc file, which defines a `niwa()` shell
function wrapper. The remaining question is the specific protocol the binary uses
to communicate "cd to this path" to that wrapper, and whether cobra-generated
completions should be bundled in the same init output.

Niwa currently prints `Created instance: /path` to stdout. There is no structured
output mode, no completions support, and no shell function wrappers. The only
existing shell integration is `~/.niwa/env` which sets PATH.

**Assumptions**

- Niwa has no stable stdout contract today. No known scripts or tools parse the
  `Created instance: /path` format. Changing stdout content for cd-eligible
  commands is not a breaking change in practice.
- At most 2-3 subcommands will need cd behavior (create, and possibly a future
  go/cd command). The shell function's case statement stays small.
- Users accept adding `eval "$(niwa init bash)"` as a second line in their rc
  file alongside the existing `. ~/.niwa/env` PATH line. These could be merged
  later if desired.

**Chosen: Stdout path with stderr messages (zoxide pattern)**

The binary adopts stdout/stderr discipline for cd-eligible subcommands. For
commands like `create`, human-readable messages (progress, warnings, the
"Created instance" confirmation) go to stderr. The path -- and only the path --
goes to stdout. The shell function wrapper captures stdout, verifies it's a
non-empty directory, and runs `builtin cd`.

The `niwa init <shell>` subcommand emits:
1. A `niwa()` shell function that intercepts cd-eligible subcommands (initially
   just `create`), captures stdout, and cds on success. All other subcommands
   pass through to the binary unchanged.
2. Cobra-generated completion registration for the active shell.

Both pieces are concatenated in a single output, so one `eval` line handles
both navigation and completions.

Concrete output of `niwa init bash`:

```bash
niwa() {
    case "$1" in
        create)
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

# completions (cobra-generated)
```

Binary-side change to `create.go`:

```go
fmt.Fprintln(cmd.ErrOrStderr(), "Created instance:", instancePath)
fmt.Fprintln(cmd.OutOrStdout(), instancePath)
```

**Rationale**

This matches zoxide's protocol exactly -- the most established pattern for
"binary resolves path, shell function does cd." It's race-condition free because
stdout capture is per-process (no shared state between concurrent shells). It
requires zero file I/O beyond the binary's normal execution. The shell function
is under 15 lines. There are no temp files to clean up, no stale state to worry
about, and nothing for concurrent shells to conflict over.

The alternatives all introduce complexity to solve a problem that doesn't exist
(preserving stdout backward compatibility for a tool with no stable output
contract). Directive temp files add race conditions or PID-scoping complexity.
fd 3 adds unfamiliar shell plumbing. The stdout approach is the simplest correct
solution.

Bundling completions in the init output is a freebie -- cobra generates the
completion code, and concatenating it after the wrapper function is trivial.
This gives users completions with zero extra setup, which is better than
requiring a separate `source <(niwa completion bash)` line.

**Alternatives Considered**

- **Directive temp file (~/.niwa/.last-cd)**: Binary writes path to a shared
  file; shell function reads and deletes it. Rejected because concurrent shells
  can cross-read each other's directives (race condition). Also adds file I/O
  overhead and stale-file cleanup concerns on crashes.

- **PID-scoped temp file (/tmp/niwa-cd-$$)**: Fixes the race condition by
  scoping the temp file to the shell's PID. Rejected because it's more complex
  than stdout capture with no corresponding benefit. Still requires file I/O
  and temp file cleanup. No precedent in existing tools.

- **Separate file descriptor (fd 3)**: Binary writes path to fd 3; shell
  function redirects fd 3 to capture it. Rejected because the shell plumbing
  is complex and unfamiliar. Debugging is harder. No known CLI tool uses this
  approach. Technically sound but unnecessarily novel.

**Consequences**

What becomes easier:
- Adding new cd-eligible subcommands: add a case to the wrapper function and
  follow the same stdout/stderr convention in the Go handler.
- Completions are automatically available to any user who has the init line.
- Testing: the binary's stdout is machine-parseable, making integration tests
  simpler (just check stdout for the path).

What becomes harder:
- `niwa create` stdout changes from a human message to a bare path. Anyone
  piping `niwa create` output in scripts sees the path only. This is actually
  more useful, not harder, but it's a behavior change.
- Users who don't add the `eval` line will see a bare path printed instead of
  the "Created instance:" message. The stderr message covers this, but terminals
  that don't show stderr won't show the confirmation. In practice, stderr always
  displays in interactive terminals.

What changes:
- `niwa init bash` and `niwa init zsh` become new subcommands.
- `~/.niwa/env` continues to handle PATH only. The init subcommand is a separate
  integration point with a separate rc-file line.
- The installer should print a message suggesting the `eval` line after install.
<!-- decision:end -->
