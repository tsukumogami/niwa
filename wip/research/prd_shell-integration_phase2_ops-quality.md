# Non-Functional Requirements: Shell Integration

Ops/Quality analysis extracting testable NFRs from the design doc and security
reviews.

## 1. Performance

### NFR-PERF-1: Shell startup latency

**Design claim:** "niwa shell-init auto adds a subprocess spawn to shell startup
... typically under 10ms" (Consequences section).

**Testable?** Yes. Measure wall-clock time of `niwa shell-init auto` in
isolation across bash and zsh. The 10ms claim is for the subprocess exec, not
the eval of the output (which adds parse time for the wrapper function and cobra
completions).

**Proposed acceptance criterion:** `time niwa shell-init auto` completes in
under 50ms on a cold start (no page cache advantage), under 15ms on a warm
start. The 10ms figure in the design is aspirational and likely warm-only.
Testing should capture both p50 and p99 across 100 invocations.

**Risk:** Cobra completion registration may add non-trivial parse time during
eval. The design does not quantify eval cost, only exec cost. If completions
grow (more subcommands, more flags), eval time grows linearly.

### NFR-PERF-2: Concurrent shell safety

**Design claim:** "Race-condition free (stdout capture is per-process)."

**Testable?** Yes. Launch N shells in parallel, each running `niwa create` via
the wrapper. Verify each shell lands in the correct directory. The stdout
protocol is inherently per-process (command substitution captures only the
child's stdout), so this should always pass. The test validates the claim
rather than defending against a known risk.

**Proposed acceptance criterion:** 10 concurrent `niwa go <workspace>` calls in
separate shell processes each return the correct path. No cross-contamination.

## 2. Safety

### NFR-SAFE-1: Path containment for `go` subcommand

**Design claim:** "`go` command must validate that the resolved path falls
within the instance root using logical-path validation (pre-symlink resolution
with filepath.Rel)."

**Testable?** Yes. Unit test: `niwa go <workspace> ../../etc` must return a
non-zero exit code with empty stdout. The validation uses `filepath.Clean` +
`filepath.Rel` on the logical path (before symlink resolution), permitting
symlinked repos while blocking `../` traversal in the argument.

**Proposed acceptance criteria:**
- `niwa go ws ../../etc` exits non-zero, stdout empty
- `niwa go ws legitimate-repo` (which is a symlink to /opt/code) succeeds
- `niwa go ws ./nested/../legitimate-repo` succeeds (path normalizes within root)

**Status:** The design doc prescribes this. The Phase 6 review specifies the
policy (logical-path, not physical-path validation). This is a hard requirement,
not an aspiration.

### NFR-SAFE-2: Stdout protocol invariant (single absolute path)

**Design claim:** "cd-eligible commands must emit exactly one line containing an
absolute directory path to stdout."

**Testable?** Yes. The Go code should validate before printing:
1. Path is absolute (`filepath.IsAbs`)
2. Path contains no newline characters
3. Path is a single line

The Phase 6 review notes the current behavior is "accidentally safe" (the `-d`
check fails on multiline strings because no directory has a newline in its name)
but recommends explicit Go-side validation.

**Proposed acceptance criteria:**
- Go unit test: if resolved path somehow contains `\n`, command exits non-zero
  with error to stderr, empty stdout
- Integration test: `niwa create` stdout is exactly one line matching
  `^/[^\n]+$`
- Integration test: `niwa go` stdout is exactly one line matching `^/[^\n]+$`

**Status:** Explicit validation is a hard requirement (Phase 6 recommendation
accepted into the design's Security Considerations section).

### NFR-SAFE-3: Shell function quoting integrity

**Design claim:** The wrapper uses `builtin cd "$__niwa_dir"` with double
quotes to prevent word splitting, glob expansion, and metacharacter injection.

**Testable?** Partially. Test with workspace names containing spaces, glob
characters (`*`, `?`), and shell metacharacters (`$()`, backticks). The
double-quoting should neutralize all of these.

**Proposed acceptance criteria:**
- Workspace with space in name: cd succeeds
- Workspace with `$` in name: cd succeeds, no variable expansion
- The generated shell function output passes `shellcheck` (bash) with no errors

**Status:** Design invariant. The shell function template is static and
compiled into the binary, so this is verifiable by inspection and shellcheck.

## 3. Error Handling

### NFR-ERR-1: Failed create produces no cd

**Design claim:** "On failure, stdout is empty; exit code is non-zero." The
shell function checks exit code 0 AND non-empty stdout AND `-d` passes.

**Testable?** Yes. If `niwa create` fails (bad manifest, network error, invalid
--from), the shell function must leave the user in their original directory.

**Proposed acceptance criteria:**
- `niwa create --from nonexistent-workspace` exits non-zero, stdout is empty,
  shell cwd is unchanged
- `niwa create --from valid --cd nonexistent-repo` exits non-zero, stdout is
  empty, shell cwd is unchanged (create succeeds but --cd resolution fails;
  the design says "errors, no cd")

**Open question:** Does `--cd nonexistent-repo` roll back the create, or does
the workspace exist but the user isn't navigated to it? The design says "errors,
no cd" but doesn't specify whether the workspace creation itself is rolled back.
This needs clarification in the PRD.

### NFR-ERR-2: Missing repo for --cd flag

**Design claim:** "niwa create --from example --cd nonexistent -- errors, no cd."

**Testable?** Yes. But the error semantics need tightening:
- Does the binary exit non-zero? (Required for the shell function to skip cd.)
- Is stdout empty? (Required for the shell function's non-empty check.)
- Is the error message on stderr? (Required for the user to see it.)

**Proposed acceptance criteria:**
- Exit code is non-zero
- Stdout is empty
- Stderr contains an actionable error message naming the unresolved repo

### NFR-ERR-3: Unrecognized shell for `auto` detection

**Design claim:** "Fails silently (empty output) if shell is unrecognized."

**Testable?** Yes. Unset both `$BASH_VERSION` and `$ZSH_VERSION`, run
`niwa shell-init auto`. Output must be empty. Exit code should be 0 (the env
file uses `2>/dev/null` and the `eval` of empty string is harmless).

**Proposed acceptance criteria:**
- Empty stdout when neither variable is set
- Exit code 0 (silent failure, not error)
- Shell startup is not disrupted (the `eval ""` is a no-op)

**Status:** Hard requirement. Silent failure is a deliberate design choice, not
a gap.

### NFR-ERR-4: Binary hang during eval (shell startup DoS)

**Design claim (Phase 6 review):** If the niwa binary hangs,
`eval "$(niwa shell-init auto 2>/dev/null)"` blocks shell startup indefinitely.

**Testable?** Yes, but only as a recovery procedure test, not a prevention
test. You can't unit-test "the binary doesn't hang" -- that's a general
reliability property.

**Proposed acceptance criteria:**
- Documentation includes recovery instructions: `bash --norc` / `zsh --no-rcs`
- The env file's `command -v` guard means removing niwa from PATH is sufficient
  to disable init on next shell

**Status:** Design aspiration, not a testable NFR. The design explicitly says
"does not warrant a timeout wrapper" and compares to the same risk in direnv
and mise. The Phase 6 review recommends documenting recovery, which is the
realistic mitigation. A timeout wrapper was considered and rejected due to
coreutils dependency on macOS.

## 4. Upgrade Path

### NFR-UPG-1: Existing users get shell integration automatically

**Design claim:** "Existing users keep their `. "$HOME/.niwa/env"` line in rc
files unchanged. The env file is already overwritten on each install, so the
delegation happens automatically on upgrade."

**Testable?** Yes, as an integration test of the install.sh flow.

**Proposed acceptance criteria:**
- Start with a pre-shell-integration env file (PATH-only)
- Run install.sh (which overwrites env file)
- New env file contains the `command -v` guard and `niwa shell-init auto` delegation
- Existing `.bashrc`/`.zshenv` source line is unchanged
- New shell session has the wrapper function loaded

### NFR-UPG-2: command -v guard enables independent deployment

**Design claim:** "The command -v guard makes it safe to deploy the env file
change before the niwa shell-init subcommand ships."

**Testable?** Yes. Write the new env file content, then test with a niwa binary
that doesn't have the `shell-init` subcommand. PATH should still be set. No
errors should appear (the `2>/dev/null` suppresses them).

**Proposed acceptance criteria:**
- Env file with delegation, but niwa binary lacks shell-init: PATH is set,
  no errors, no wrapper function (graceful degradation)

## 5. Compatibility

### NFR-COMPAT-1: Bash and zsh required

**Design claim:** "Both bash and zsh required" (Decision Drivers). Fish deferred.

**Testable?** Yes. The `niwa shell-init bash` and `niwa shell-init zsh` outputs
must produce valid, functional shell code for their respective shells.

**Proposed acceptance criteria:**
- `niwa shell-init bash | bash -n` exits 0 (valid bash syntax)
- `niwa shell-init zsh | zsh -n` exits 0 (valid zsh syntax) -- note: zsh -n
  may not exist; alternative is `zsh -c 'eval "$(niwa shell-init zsh)"'`
- Wrapper function works in bash 3.2+ (macOS default) and bash 5.x (Linux)
- Wrapper function works in zsh 5.x
- `niwa shell-init fish` either errors clearly or outputs nothing (not
  broken fish code)

### NFR-COMPAT-2: Shell integration is optional

**Design claim:** "niwa must work without the shell function wrapper."

**Testable?** Yes. All niwa commands must function when invoked as
`command niwa <args>` without the wrapper. The only difference is the user
doesn't get auto-cd.

**Proposed acceptance criteria:**
- `command niwa create --from example` succeeds, prints path to stdout
- `command niwa go example` succeeds, prints path to stdout
- User can manually `cd $(command niwa go example)` as a workaround

## Summary Table

| ID | Category | Testable | Type |
|----|----------|----------|------|
| NFR-PERF-1 | Performance | Yes (benchmark) | Aspiration (10ms claim needs bounds) |
| NFR-PERF-2 | Performance | Yes (concurrency test) | Hard (protocol is per-process by design) |
| NFR-SAFE-1 | Safety | Yes (unit + integration) | Hard requirement |
| NFR-SAFE-2 | Safety | Yes (unit + integration) | Hard requirement |
| NFR-SAFE-3 | Safety | Yes (shellcheck + integration) | Hard requirement |
| NFR-ERR-1 | Error handling | Yes (integration) | Hard requirement |
| NFR-ERR-2 | Error handling | Yes (integration) | Hard requirement (needs spec tightening) |
| NFR-ERR-3 | Error handling | Yes (unit) | Hard requirement |
| NFR-ERR-4 | Error handling | Partially (recovery docs only) | Aspiration (inherent to eval-init) |
| NFR-UPG-1 | Upgrade path | Yes (integration) | Hard requirement |
| NFR-UPG-2 | Upgrade path | Yes (integration) | Hard requirement |
| NFR-COMPAT-1 | Compatibility | Yes (syntax + runtime) | Hard requirement |
| NFR-COMPAT-2 | Compatibility | Yes (integration) | Hard requirement |

## Open Questions for PRD

1. **--cd failure semantics:** Does `niwa create --from x --cd bad-repo` roll
   back the created workspace, or leave it in place without navigating? The
   design says "errors, no cd" but is silent on rollback.

2. **Performance budget:** The 10ms claim should be refined. Is it wall-clock
   for the exec only, or end-to-end including eval? What's the budget for
   completion registration parse time as the CLI grows?

3. **Fish timeline:** The design defers fish. Should the PRD capture a
   compatibility matrix with explicit "not supported" for fish, or leave it
   open?
