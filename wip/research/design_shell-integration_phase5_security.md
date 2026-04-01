# Security Review: shell-integration

## Dimension Analysis

### External Artifact Handling

**Applies:** No

The shell integration design does not download, execute, or process external
artifacts. The `niwa init` subcommand generates shell code from templates
compiled into the binary itself. The `go` subcommand resolves paths from the
local global registry (a config file niwa already manages). No network requests,
no fetched scripts, no remote code execution.

The `create` command does clone git repos, but that behavior is pre-existing and
not introduced by this design. This review covers only the shell integration
layer.

### Permission Scope

**Applies:** Yes
**Severity:** Low

The shell function uses `builtin cd` to change the parent shell's working
directory. This is the explicit purpose of the feature and matches the
permission model of tools like zoxide and direnv. The design does not:

- Write to files outside niwa's own state directory
- Execute binaries beyond the niwa binary itself
- Require elevated privileges
- Open network connections

The `eval` in the env file executes output from `niwa init auto`. This is a
trust boundary: the user trusts the niwa binary to produce safe shell code. This
is the same trust model as every eval-init tool (direnv, mise, starship, etc.)
and is appropriate given that the user installed niwa intentionally.

**Risk:** If the niwa binary were compromised (supply chain attack), `eval`
would execute arbitrary code in every new shell. This is inherent to the
eval-init pattern, not specific to this design.

**Mitigation:** No design change needed. The `2>/dev/null` on the eval line
suppresses errors but does not suppress malicious stdout. This is standard
practice. Users who want to audit can run `niwa init bash` directly and inspect
the output.

### Supply Chain or Dependency Trust

**Applies:** No (beyond the binary itself)

The shell integration generates code from compiled-in templates. There are no
additional dependencies, no downloaded scripts, no third-party shell plugins.
The trust boundary is the niwa binary, which is the same trust boundary that
already exists for all niwa commands.

### Data Exposure

**Applies:** Yes
**Severity:** Low

The stdout protocol exposes filesystem paths (workspace instance directories) to
the shell function. These paths are:

- Local filesystem paths the user already knows about (they created the workspace)
- Not transmitted over any network
- Not logged to any file (captured into a shell variable and used for cd)

The `niwa init auto` subcommand reads `$BASH_VERSION` and `$ZSH_VERSION`
environment variables to detect the shell. These are standard, non-sensitive
variables.

No telemetry, no network transmission, no sensitive data exposure.

### Shell Injection

**Applies:** Yes
**Severity:** Low (with one consideration)

The shell function captures the binary's stdout into a variable and uses it as a
`cd` target:

```bash
__niwa_dir=$(command niwa "$@")
builtin cd "$__niwa_dir" || return
```

The critical question: can the niwa binary be made to output something other than
a directory path to stdout for cd-eligible commands?

**Analysis of the `create` path:** The binary constructs the instance path from
`~/.niwa/instances/<name>`, where `<name>` comes from workspace.toml or a
command-line argument. The path is assembled with `filepath.Join` in Go, which
normalizes path separators but does not sanitize against shell metacharacters.

However, the shell function's `builtin cd "$__niwa_dir"` uses double quotes,
which prevents word splitting and glob expansion. The only injection risk would
be if the path contained a newline, which could cause `cd` to receive a
different argument than intended -- but `builtin cd` takes only one argument
(the first line), and the `-d` directory check would fail on a partial path.

**Analysis of the `go` path:** The workspace name comes from the global registry
file, which niwa itself writes. An attacker who can modify `~/.niwa/config.toml`
already has write access to the user's home directory and doesn't need a shell
injection vector.

**Analysis of the `init` output:** The generated shell code is a static template
with no user-controlled interpolation. The template is compiled into the binary.
No injection vector exists in the init output itself.

**Residual risk:** If a workspace name or path contained shell metacharacters
(e.g., `$(cmd)` or backticks), the double-quoting in the shell function
prevents execution. The `-d` test would fail on any non-directory string. The
design is sound.

**Recommendation:** Document that cd-eligible commands must output exactly one
line containing an absolute path, and that the shell function relies on
double-quoting for safety. This is worth stating explicitly in the design even
though the current implementation handles it correctly.

### Path Traversal

**Applies:** Yes
**Severity:** Low

The `go` command resolves workspace paths from the global registry and repo
paths within a workspace instance. Could a malicious registry entry cause
navigation to an unintended location?

**Analysis:** The global registry (`~/.niwa/config.toml`) is a user-owned file.
If an attacker can write to it, they already have the user's home directory
access. The `go` command reads the `Root` field from registry entries, which
contains an absolute path set during `niwa init`. The design does not describe
any path validation beyond what `filepath.Join` provides.

For `niwa go <name> <repo>`, the repo is resolved relative to the instance root.
If the repo argument were `../../etc`, `filepath.Join(instanceRoot, "../../etc")`
would resolve to `/etc`. The shell function would then `cd /etc`, which is
surprising but not a privilege escalation (the user can already `cd /etc`).

**Recommendation:** The `go` command should validate that the resolved path is
within the instance root directory (or is the instance root itself). This
prevents the `go` command from being used as an arbitrary directory navigator,
which would violate the principle of least surprise even though it doesn't
create a privilege escalation. Use `filepath.Rel` or prefix checking after
`filepath.EvalSymlinks` to confirm containment.

## Recommended Outcome

OPTION 2 - Document considerations

The design is fundamentally sound and follows established patterns. Two items
should be documented in the Security Considerations section:

1. **Stdout protocol contract:** cd-eligible commands must emit exactly one line
   containing an absolute directory path. The shell function depends on
   double-quoting to prevent injection. This invariant should be stated
   explicitly.

2. **Path containment for `go` subcommand:** The `go <name> <repo>` form should
   validate that the resolved repo path falls within the workspace instance
   root. This prevents path traversal via crafted repo arguments (e.g.,
   `../../sensitive-dir`). While not a privilege escalation, it violates least
   surprise and the command's intended scope.

Neither item requires architectural changes to the design. Both are
implementation-level validations that fit naturally into the existing structure.

## Summary

The shell integration design follows the well-established eval-init pattern used
by zoxide, direnv, and mise. The attack surface is small: generated shell code
is static (no user interpolation), stdout capture uses proper quoting, and the
trust boundary is the niwa binary itself. The two actionable findings are
documenting the stdout protocol's safety invariant and adding path containment
validation to the `go` subcommand's repo argument resolution. Neither requires
design-level changes.
