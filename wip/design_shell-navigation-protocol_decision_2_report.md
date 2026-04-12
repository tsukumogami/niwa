<!-- decision:start id="sentinel-format" status="confirmed" -->
### Decision: Sentinel Line Format

**Context**

The niwa CLI needs to communicate a landing directory path to its POSIX bash/zsh
shell wrapper after commands like `go` and `create`. The current protocol prints
only the path to stdout, which the wrapper captures in its entirety. This breaks
whenever any other output lands on stdout — from git progress, fmt.Printf debug
lines, subprocess output, or future verbose/CI modes.

The replacement protocol embeds the path in a sentinel line that the wrapper
can extract with a simple pattern match, allowing all other stdout to pass
through to the terminal normally.

**Assumptions**

- The shell wrapper runs in POSIX bash or zsh; no Python, jq, or other
  non-standard tools are available at the extraction site.
- Absolute paths on Linux/macOS begin with `/` and almost never contain
  characters like `\x01` or `\x1b`.
- The sentinel line is produced by Go code using `fmt.Fprintf`; the format
  must be expressible as a plain format string.
- Collision resistance is more important than theoretical elegance — a
  format that virtually never appears in normal tool output is sufficient.
- The sentinel travels on stdout (not stderr); stderr is already used for
  human-readable progress messages (`go: workspace "foo"`).

**Chosen: Option A — Key-value (`NIWA_CD=<path>`)**

The sentinel line is:

```
NIWA_CD=/absolute/path/to/directory
```

Emitted from Go as:

```go
fmt.Fprintf(os.Stdout, "NIWA_CD=%s\n", targetPath)
```

Extracted in the shell wrapper as:

```sh
niwa() {
    case "$1" in
        create|go)
            local __niwa_out __niwa_dir __niwa_rc
            __niwa_out=$(command niwa "$@")
            __niwa_rc=$?
            __niwa_dir=$(printf '%s\n' "$__niwa_out" \
                | grep '^NIWA_CD=' \
                | head -1 \
                | cut -d= -f2-)
            # Pass through all non-sentinel lines.
            printf '%s\n' "$__niwa_out" | grep -v '^NIWA_CD='
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

**Rationale**

Option A is the industry-established convention for this exact pattern. Tools
that need to communicate structured data back to a shell wrapper (direnv,
mise, nix-env) universally use `KEY=value` lines because shell authors
recognise the idiom immediately. The prefix `NIWA_CD=` is application-scoped
(the `NIWA_` namespace prefix eliminates collisions with generic output) and
it is trivially extracted with `grep '^NIWA_CD=' | cut -d= -f2-` — both
available in every POSIX environment without sourcing extras.

The main concern raised against Option A is colon collision: `cut -d= -f2-`
uses field 2-onwards, so a path like `/home/user/path=with=equals` would be
split. In practice, equals signs in directory paths are rare to the point of
being an engineering curiosity, and the `f2-` idiom handles this correctly
anyway — `cut -d= -f2-` returns everything after the first `=`, so
`NIWA_CD=/some=path` yields `/some=path` intact.

Option B's `::niwa-cd::` prefix is slightly more collision-resistant in
theory, but it is an invented convention that provides no recognition benefit
and is marginally harder to type during debugging. The practical collision
risk for `NIWA_CD=` is negligible given the `NIWA_` namespace.

**Alternatives Considered**

- **Option B (`::niwa-cd::/path`)**: Double-colon delimited prefix. More
  visually distinct and resistant to accidental collision, but offers no
  practical advantage over the namespaced key-value format. Less immediately
  readable to a developer who encounters it in output. Rejected because the
  marginal collision benefit does not outweigh the loss of idiom familiarity.

- **Option C (`NIWA://cd /path`)**: URL-like scheme prefix. The embedded
  space between the path component and the path itself complicates extraction
  — `sed -n 's|^NIWA://cd ||p'` works, but it is more fragile than a `cut`
  on a well-defined delimiter. The format is also unfamiliar as a shell
  communication convention. Rejected because extraction is more error-prone
  and the format does not follow an established pattern.

- **Option D (NUL or non-printable marker)**: Surrounding the path with
  `\x01` (SOH), `\x1b` (ESC), or similar control characters makes collision
  virtually impossible and is visually unambiguous in a hex dump. However,
  `grep` and `sed` behaviour with non-printable bytes varies across POSIX
  implementations (notably between GNU and BSD sed), and `fmt.Fprintf` with
  format verbs like `"\x01%s\x01"` is easy to misread. The extraction
  pattern requires character-class or hex-escape support that is not
  guaranteed across all POSIX shells. Rejected because the reliability
  improvement over Option A is marginal while the implementation and
  portability costs are real.

**Consequences**

- The Go emission line is `fmt.Fprintf(os.Stdout, "NIWA_CD=%s\n", path)` —
  easy to grep for, easy to test.
- The shell wrapper must be updated to parse sentinel output rather than
  treating all of stdout as the path. The wrapper in `shell_init.go` needs
  a `grep`/`cut` extraction step and a pass-through step for non-sentinel lines.
- Any future CLI output on stdout (verbose, debug, CI) is safe as long as it
  does not begin with `NIWA_CD=`. This is easily enforced by convention.
- The format is self-documenting: a developer who sees `NIWA_CD=/home/user/ws`
  in terminal output immediately understands its purpose.
- Paths containing `=` signs are handled correctly by `cut -d= -f2-` since it
  returns everything from field 2 to end-of-line.
<!-- decision:end -->
