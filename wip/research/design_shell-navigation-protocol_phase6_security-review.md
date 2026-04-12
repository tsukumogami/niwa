# Security Review: shell-navigation-protocol (Phase 6 - Final Review)

**Reviewer:** Code Review Agent
**Date:** 2026-04-11
**Input:** Design document at `docs/designs/current/DESIGN-shell-navigation-protocol.md`,
phase 5 security analysis at `wip/research/design_shell-navigation-protocol_phase5_security.md`,
and current implementation in `internal/cli/` (go.go, create.go, shell_init.go).

---

## Summary Judgment

The phase 5 analysis is solid on the risks it covers. No critical gaps were
found. Three issues merit attention: one attack vector is missing from the
analysis (env var subprocess leakage), one "not applicable" judgment needs a
narrow correction (Decision 2 sentinel format), and the `NIWA_RESPONSE_FILE`
path-prefix mitigation is described as optional but should be treated as a
required implementation item given the destructive failure mode it prevents.

---

## Question 1: Attack Vectors Not Considered

### 1a. NIWA_RESPONSE_FILE leaks into subprocesses (Missing from Phase 5)

The design's Consequences section notes this under "Negative": `NIWA_RESPONSE_FILE`
leaks into the subprocess environment. Any child process spawned by niwa
(git, gh CLI, workspace setup scripts) inherits the variable. If a malicious or
buggy subprocess reads `NIWA_RESPONSE_FILE` and writes to that path, it could
corrupt the temp file content before the wrapper reads it, causing niwa to `cd`
to an attacker-controlled directory. The path the subprocess would need to write
is a valid directory path that passes the `[ -d "$__niwa_dir" ]` check in the
shell wrapper — so the attacker must control both a subprocess spawned by niwa
and a directory on the filesystem.

Severity: Low-to-medium. Requires a compromised or malicious subprocess, but
unlike the env-var injection scenario (which requires compromising the parent
shell), this path is triggered from inside niwa's own execution chain. The
phase 5 analysis mentions the env-var injection vector from a parent but doesn't
address injection from a child.

Mitigation: The Go CLI can unset `NIWA_RESPONSE_FILE` from `os.Environ` before
exec'ing any subprocess, or set it in the process environment only for the
`writeLandingPath` call and not inherit it. Alternatively, the design's own
Consequences section mentions unsetting the var before subprocesses as a future
option — this should be elevated to a recommended implementation step, not a
deferred concern.

### 1b. Shell wrapper's `cat` output captured by outer $() (edge case)

The design wrapper uses `__niwa_dir=$(cat "$__niwa_tmp" 2>/dev/null)`. If niwa
is invoked from *another* shell function that captures its output (e.g., a user
who wraps `niwa` in a custom function doing `result=$(niwa go ...)`), the `cat`
output is still captured internally by the inner wrapper before returning, not
leaked. This is not a security concern, just a usability note: the outer caller
would get no stdout (because cd-eligible commands write to the file, not stdout)
unless `NIWA_RESPONSE_FILE` is absent. This is documented in backward compat
behavior. No security gap.

### 1c. Race between rm and re-use of same temp path

The wrapper calls `rm -f "$__niwa_tmp"` after reading. If the process is killed
between the `cat` and the `rm`, the temp file persists with mode `0o600` and
content that is a directory path. This is low-sensitivity data (a workspace
path) and the file is owner-read-only. No security issue.

---

## Question 2: Are Mitigations Sufficient?

### 2a. NIWA_RESPONSE_FILE path-prefix check — insufficient framing

The phase 5 analysis and the design document describe validating that
`NIWA_RESPONSE_FILE` points inside `$TMPDIR` or `/tmp` as an optional
mitigation ("optionally validate", "implementation should validate"). The
destructive failure case is concrete: if `NIWA_RESPONSE_FILE` is set to
`~/.bashrc`, niwa will silently overwrite it with a workspace path string,
destroying shell configuration. This isn't a theoretical edge case — CI
environments and build tools commonly propagate environment variables
indiscriminately.

Assessment: the mitigation should be required, not optional. The prefix check
is a two-line addition to `writeLandingPath` and eliminates the entire
confused-deputy overwrite class. Treating it as optional understates the risk.

### 2b. Symlink attack via O_NOFOLLOW — framing is accurate

The analysis correctly characterizes this as theoretical (unpredictable random
suffix) and correctly recommends `Lstat` verification only for high-assurance
environments. The `os.WriteFile` symlink-follow behavior is accurately described.
The mitigation framing is appropriate.

### 2c. Path validation scope — one gap

`validateStdoutPath` rejects non-absolute paths and paths containing newlines.
It does not reject paths containing null bytes (`\x00`). On Linux, paths with
embedded null bytes passed to `os.Chdir` (ultimately called by `builtin cd`)
would be truncated at the null, which could redirect to an unintended parent
directory. However, `os.WriteFile` in Go would write the null byte into the
file, and `cat` piped through `$(...)` in bash/zsh will stop reading at the
null byte (command substitution strips trailing newlines but behavior with nulls
is shell-defined). In practice this is not exploitable through niwa's own
path-resolution logic, which derives paths from `filepath.Join` and
`os.Getwd` — neither of which can produce null-containing paths. Not a
practical risk, but `validateStdoutPath` could be hardened with a null-byte
check for completeness.

---

## Question 3: "Not Applicable" Justifications

### 3a. External Artifact Handling — "Not Applicable" is correct

Landing paths are derived from local config and filesystem state only. No
external data is processed by the navigation protocol. The judgment is sound.

### 3b. Supply Chain or Dependency Trust — "Not Applicable" is correct

No new dependencies are introduced. `mktemp`, `cat`, `rm`, and `builtin cd`
are POSIX shell builtins or standard utilities. The Go side uses only
`os.Getenv` and `os.WriteFile`. The judgment is sound.

### 3c. Decision 2 (Sentinel Format) "Not Applicable" — partially incorrect

The design document marks Decision 2 as "Evaluated, Not Applicable" because the
temp-file protocol makes a sentinel format unnecessary. This is correct for the
primary protocol. However, the design explicitly preserves the stdout path output
as the backward-compatibility path when `NIWA_RESPONSE_FILE` is absent (scripts
calling `dir=$(niwa go workspace)` directly). That path uses the current raw
stdout protocol — not a sentinel format — and it is still in use in the
implementation today.

The "not applicable" label is accurate for the temp-file channel. But it
implicitly treats the stdout backward-compat path as safe by default. The
phase 5 analysis does cover this under "Path injection in shell" and concludes
it is safe (absolute path check + newline rejection + double-quoting). The
assessment is correct. The "not applicable" framing on Decision 2 does not
create a security gap, but a reader of the design who only reads the headers
might conclude the stdout path has no security considerations. This is a
documentation clarity issue, not a security gap.

---

## Question 4: Residual Risk Escalation

The phase 5 analysis concludes "Low. No privilege escalation, network exposure,
or new dependencies." This conclusion is accurate. No finding in this review
changes the residual risk level from Low.

However, two items should be tracked as required implementation work rather
than documentation notes:

1. **NIWA_RESPONSE_FILE path-prefix validation** — required, not optional.
   Without it, a confused-deputy overwrite of sensitive files is possible.
   This belongs in the `writeLandingPath` implementation spec, not in
   post-launch documentation.

2. **NIWA_RESPONSE_FILE subprocess leakage** — the design's Consequences
   section defers unsetting the variable to "if isolation becomes a concern
   in the future." Given that niwa spawns git, gh CLI, and workspace setup
   scripts, this concern is present now. The recommended action is to unset
   `NIWA_RESPONSE_FILE` from the environment before exec'ing any subprocess,
   which is straightforward and eliminates the child-injection vector entirely.

Neither item requires a design revision. Both are implementation-level
additions to the `writeLandingPath` helper and the subprocess exec wrappers.

---

## Issue Summary

| Severity | Item | Action |
|----------|------|--------|
| Important | NIWA_RESPONSE_FILE leaks into subprocesses (child injection vector not covered in phase 5) | Unset before exec; elevate in implementation spec |
| Important | Path-prefix check framed as optional; should be required | Mandate in writeLandingPath spec |
| Suggestion | validateStdoutPath missing null-byte check | Low-risk hardening |
| Clarification | Decision 2 "not applicable" correct but may mislead readers about stdout backward-compat path | Add a note in the design |
