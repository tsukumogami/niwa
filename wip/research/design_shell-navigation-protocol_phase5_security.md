# Security Review: shell-navigation-protocol

## Dimension Analysis

### External Artifact Handling

**Applies:** No

This feature has no interaction with external artifacts. The landing path is derived entirely from the local filesystem: workspace registry entries in `~/.config/niwa/config.toml`, directories discovered by walking up from `cwd`, and instance directories created by the tool itself. No downloads, no HTTP requests, no external inputs are processed as part of the navigation protocol. The temp file is written by the Go binary and read by the same shell wrapper — both are under user control.

### Permission Scope

**Applies:** Yes (low severity)

The feature requires no permissions beyond what niwa already holds. The Go binary reads from `~/.config/niwa/config.toml` and the local filesystem (both already required for all niwa commands). The shell wrapper creates a temp file via `mktemp` (user-owned, in `$TMPDIR` or `/tmp`), reads it, and deletes it.

No privilege escalation is involved. The `builtin cd` call in the shell wrapper operates in the user's own shell process — it cannot escape the user's own session or affect other users.

One narrow concern: `mktemp` creates the file in a world-accessible temp directory. On Linux, `/tmp` is typically sticky-bit protected, so other users cannot read or delete the file, but this is worth verifying (see Temp File Security below).

### Supply Chain or Dependency Trust

**Applies:** No

The protocol introduces no new dependencies. The shell wrapper uses only POSIX shell builtins (`mktemp`, `cat`, `rm`, `cd`). The Go side uses only the standard library (`os.Getenv`, `os.WriteFile`). No third-party packages are added.

### Data Exposure

**Applies:** Yes (low severity)

The temp file contains a single absolute filesystem path. That path reveals the user's home directory layout and workspace naming conventions. However, the exposure is minimal: the file is created with mode `0o600` (owner-read/write only), lives briefly in `/tmp`, and is deleted immediately after the `cd`. The file is never transmitted over a network and is never logged.

The landing path is also printed to stderr (`go: workspace "..."` or `Created instance: ...`) in the current implementation, so the path is already visible to the user. The temp file doesn't expose anything that isn't already shown in normal output.

On shared-machine environments the temp directory location matters more (see below).

### Temp File Security

**Applies:** Yes

This is the primary security surface for this design. Several attack patterns are worth considering.

#### TOCTOU on the temp file

The shell wrapper calls `mktemp`, passes the path to the binary, then reads from it. Between the binary writing the file and the wrapper reading it, another process could replace the file contents. In practice this window is sub-millisecond and the attacker would need to know the exact temp file name (randomized by `mktemp`). The sticky-bit on `/tmp` prevents deletion or replacement by other users even if they discover the filename. Severity: low on standard Linux/macOS systems; theoretical on unusual configurations.

Mitigation that could be applied: the Go binary could verify that the file it's about to write is owned by the current user and has mode `0o600`, refusing to write if not. This adds defense in depth but is not critical given `mktemp`'s guarantees.

#### Symlink attack on the temp file path

If an attacker can predict the temp file path before `mktemp` creates it, they could create a symlink at that path pointing to an arbitrary file, causing `os.WriteFile` in the Go binary to write the landing path to the attacker's target. `mktemp` creates the file atomically (open with `O_CREAT|O_EXCL`), so the file always exists before the binary opens it. `os.WriteFile` does not follow the existing file entry into a symlink by default — it truncates and writes. On Linux, `O_NOFOLLOW` is not used by `os.WriteFile`, so if the temp entry is a symlink, Go will follow it. However, creating a symlink at a `mktemp`-generated path requires guessing a cryptographically random suffix, which is not feasible in practice.

Mitigation for high-assurance environments: open the temp file with `os.OpenFile` using `O_WRONLY|O_TRUNC` after verifying it is not a symlink (`Lstat` vs `Stat` comparison). This is defense in depth.

#### NIWA_RESPONSE_FILE set by a malicious process

If a parent process (e.g., a compromised build tool or CI runner) sets `NIWA_RESPONSE_FILE` to an arbitrary path before invoking niwa, the Go binary will write the landing path to that arbitrary path. This means the protocol can be triggered without the shell wrapper.

Consequences:
- The landing path (an absolute directory path) gets written to an attacker-controlled file.
- The attacker cannot control *what* gets written — only the Go binary decides the path. An attacker cannot use this to write arbitrary content anywhere.
- If `NIWA_RESPONSE_FILE` points to a sensitive file (e.g., `~/.bashrc`), niwa would overwrite it with a workspace path string. This is destructive but not a code-execution vector on its own.
- If `NIWA_RESPONSE_FILE` points to a file the user does not own and lacks write permission to, `os.WriteFile` will fail and return an error, which the `writeLandingPath` helper propagates.

Severity: low. An attacker who can inject environment variables into the user's shell has already compromised the session; the damage from this vector (writing a path string to a file) is marginal compared to that baseline.

Mitigation: document the intended use of `NIWA_RESPONSE_FILE` and note that it is only meaningful when set by the niwa shell wrapper. Optionally, validate that the path points into `$TMPDIR` or `/tmp` before writing. This prevents a confused-deputy scenario where another tool inadvertently sets the variable.

#### Path injection via file content

The wrapper reads the temp file with `cat "$__niwa_tmp"` and assigns it to `__niwa_dir`. If the landing path contains shell metacharacters, this could be dangerous. However, the assignment form `__niwa_dir=$(cat ...)` does not expand or evaluate the file content — the string is captured as-is. The subsequent `builtin cd "$__niwa_dir"` double-quotes the variable, preventing word splitting and glob expansion.

The Go binary's `validateStdoutPath` already rejects paths containing newlines. Combined with double-quoting in the shell, this closes the injection surface.

One edge case: a path containing a literal `$(...) ` sequence. Command substitution within `$(cat ...)` does not occur — the content is the captured output of `cat`, not evaluated as a shell expression. Safe.

Severity: none with the current `validateStdoutPath` guard in place.

#### Note on the current implementation vs. the design

The current codebase (`shell_init.go`) uses the older stdout protocol (`__niwa_dir=$(command niwa "$@")`), not the temp file protocol described in the design. The Go commands (`go.go`, `create.go`) write the landing path to stdout (`fmt.Fprintln(cmd.OutOrStdout(), landingPath)`) and validate it with `validateStdoutPath`. The temp file design is a proposed change, not yet implemented. This review applies to the proposed design.

## Recommended Outcome

**OPTION 2 (document considerations)**

The design is sound. No architectural changes are required. The temp file risks identified (TOCTOU, symlink) are theoretical in practice, mitigated by `mktemp`'s atomic creation and the sticky-bit on `/tmp`. The one actionable item is to document that `NIWA_RESPONSE_FILE` is an internal protocol variable and optionally add a path prefix check in `writeLandingPath` to prevent confused-deputy writes outside the temp directory. This can be done as a small defensive addition rather than a redesign.

If the codebase targets high-assurance or shared-machine environments, the symlink hardening (open with `O_NOFOLLOW`) and the `NIWA_RESPONSE_FILE` prefix validation are worth adding. For a typical developer workstation, they are belt-and-suspenders.

## Summary

The shell navigation protocol is safe for its intended use case. The only non-trivial security surface is the temp file lifecycle and the trust model around `NIWA_RESPONSE_FILE`: a process with the ability to inject environment variables could redirect the landing path write, but can only cause niwa to write a workspace path string — not arbitrary content — to the redirected file, and only if the binary has write permission there. The existing `validateStdoutPath` guard (newline rejection, absolute path enforcement) addresses path injection in the shell wrapper. No design changes are required; documenting `NIWA_RESPONSE_FILE` as an internal variable and optionally adding a `/tmp` prefix check are the only recommended improvements.
