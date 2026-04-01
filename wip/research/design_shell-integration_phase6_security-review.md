# Security Review (Phase 6): shell-integration

Review of the Phase 5 security analysis against the full design document.

## Question 1: Attack Vectors Not Considered

The Phase 5 review covers the main surface well. Three additional vectors deserve mention:

### 1a. Shell startup denial-of-service

If the niwa binary hangs or takes abnormally long (disk full, corrupted state, deadlock bug), `eval "$(niwa init auto 2>/dev/null)"` blocks shell startup indefinitely. The `2>/dev/null` suppresses error output but does not impose a timeout. Unlike a broken PATH export (which is instant), a hung subprocess prevents the user from opening new terminal sessions.

**Severity:** Low. Unlikely under normal conditions, but when it happens the impact is high (locked out of new shells). The user must manually edit their rc file or spawn a shell with `--norc` to recover.

**Recommendation:** Consider wrapping the eval in a timeout or documenting the `--norc` recovery path. A practical approach: the env file could use `timeout 2 niwa init auto` (coreutils timeout) or background the init with a very short deadline. However, adding `timeout` introduces a dependency on coreutils, which may not be present on macOS without Homebrew. The simplest mitigation is documenting recovery: "If shell startup hangs after installing niwa, launch a shell with `bash --norc` or `zsh --no-rcs` and remove the source line from your rc file."

### 1b. Multiline stdout corruption

The design states cd-eligible commands output "exactly one line containing an absolute directory path." The Phase 5 review notes that `builtin cd` takes only the first line if a newline is present, and the `-d` check would fail on a partial path. This analysis is incomplete.

If the binary emits two lines -- a valid directory path on line 1 and something else on line 2 -- the command substitution `$()` captures both lines. The variable `__niwa_dir` would contain `"/valid/path\nsomething"`. The `-d` test operates on the full multiline string, which on most shells would fail (no directory has a literal newline in its name). However, `builtin cd` in bash receives the entire multiline string as a single argument (double-quoted), and bash's `cd` will fail because no such directory exists. So the behavior is safe but for the wrong reason -- it relies on filesystem behavior rather than explicit validation.

**Severity:** Informational. Current behavior is accidentally safe, but a Go-side invariant (validate that the emitted path contains no newlines before printing) would make the contract explicit rather than relying on filesystem semantics.

**Recommendation:** Add a Go-side check in each cd-eligible command: if the resolved path contains a newline character, write an error to stderr and exit non-zero. This makes the single-line contract enforceable rather than assumed.

### 1c. TOCTOU between -d check and cd

The shell function checks `-d "$__niwa_dir"` then calls `builtin cd "$__niwa_dir"`. Between these two operations, the directory could be removed or replaced with a symlink to a different location. This is a classic time-of-check-time-of-use gap.

**Severity:** Informational. The user already has filesystem access and the window is microseconds. No privilege boundary is crossed. This is not exploitable in practice.

**Recommendation:** None needed. Documenting this as a known non-issue is sufficient.

## Question 2: Are Mitigations Sufficient?

The Phase 5 mitigations are appropriate for the identified risks:

- **Double-quoting in shell function**: Correct. Prevents word splitting, glob expansion, and command substitution within the cd argument. This is the primary injection defense and it is sound.

- **Path containment for `go` subcommand**: The design doc already includes this recommendation (filepath.Rel or prefix checking after filepath.EvalSymlinks). The Phase 5 review echoes it. One gap: the design mentions `filepath.EvalSymlinks` but does not address what happens if the symlink target is outside the instance root. If a repo within the instance is a symlink to `/opt/shared-code`, should `go` allow it? The implementation needs a policy decision: validate the logical path (before symlink resolution) or the physical path (after resolution). Validating the physical path is more secure but may break legitimate symlinked repos.

  **Recommendation:** Validate the logical path (pre-symlink resolution) using `filepath.Rel` on the `filepath.Clean`-ed path. This permits symlinked repos while still preventing `../../` traversal in the argument itself. Document this decision explicitly.

- **Eval trust boundary**: The Phase 5 review correctly identifies this as inherent to the eval-init pattern. No additional mitigation is needed or possible without abandoning the pattern entirely.

## Question 3: "Not Applicable" Justification Review

### External Artifact Handling: "Not Applicable"

**Verdict: Correctly marked.** The init subcommand generates code from compiled templates. The go subcommand reads local config files. No external artifacts are fetched or processed by the shell integration layer.

### Supply Chain or Dependency Trust: "Not Applicable (beyond the binary itself)"

**Verdict: Correctly marked, with a nuance.** The Phase 5 review is right that the shell integration itself adds no new dependencies. However, the env file delegation creates a tighter coupling between the binary and shell startup. A compromised binary update (via tsuku or manual download) now has a direct path to shell-level code execution on every terminal open, not just when the user explicitly runs `niwa`. This is inherent to eval-init and not unique to niwa, but it slightly elevates the impact of a supply chain compromise compared to a non-eval-init tool.

This does not change the "not applicable" classification -- the supply chain trust boundary is the binary, which already existed -- but it's worth noting that the blast radius of a binary compromise increases with this design.

### Data Exposure: "Low"

**Verdict: Correctly classified.** Filesystem paths are not sensitive in this context.

## Question 4: Residual Risk Assessment

Two items warrant awareness but not escalation:

1. **Shell startup hang (1a above)**: Low probability, moderate impact. Mitigated by documentation rather than code. Acceptable for an early-stage project with a small user base. If niwa's user base grows, revisit with a timeout mechanism.

2. **Increased blast radius of binary compromise**: The eval-init pattern means a compromised niwa binary executes code in every new shell session. This is the same risk profile as zoxide, direnv, and mise. It is inherent to the pattern and accepted by the design. No escalation needed, but the project should ensure binary distribution integrity (checksums, signed releases) as it matures.

No residual risk requires design changes or escalation. The design is sound.

## Summary

The Phase 5 security analysis is thorough and its conclusions are correct. Three minor gaps were identified:

1. Shell startup hang if the binary becomes unresponsive (recommend documenting recovery)
2. Multiline stdout should be explicitly validated in Go code rather than relying on filesystem behavior
3. Path containment for `go` should specify logical-path validation policy for symlinked repos

All findings are low severity or informational. No design-level changes are required. The eval-init pattern is well-established and niwa's implementation follows it correctly.
