# Testability Review (round 2)

## Verdict: PASS

The revision resolves all 33 round-1 issues with pinned exact-substring assertions, named test fixtures per AC, explicit exit codes, and AC coverage for every previously-uncovered requirement; a small number of secondary gaps remain but none block a writer from authoring a complete test plan from the AC alone.

## Round 1 Resolution Status

### Untestable Criteria (15)

- Issue U1 (401/403 fixture): RESOLVED — ACs "401 auth error" and "403 auth error" name `tarballFakeServer` returning the specific status, and both assert "stderr contains the R10 substring" where R10 pins an exact substring.
- Issue U2 (404 three causes): RESOLVED — R11 now lists three exact substrings (one per cause), and three separate 404 ACs (typo, zero-commit, private-no-token) each assert "stderr contains all three R11 substrings."
- Issue U3 (404 zero-commit fixture): RESOLVED — AC "404 (zero-commit case)" explicitly names `tarballFakeServer returns 404; GH_TOKEN unset, simulating GitHub's response for a no-commit repo." Behavior table in Token Presence Semantics row 8 covers it too.
- Issue U4 (Ambiguous markers anchor): RESOLVED — R12 specifies "verbatim" output of `(*config.AmbiguousMarkersError).Error()`, and the AC asserts "stderr contains the exact verbatim string returned by today's `(*config.AmbiguousMarkersError).Error()`." Asserting against the live error type is a deterministic anchor.
- Issue U5 (TTY harness): RESOLVED — AC explicitly tags "functional test with pty helper" and the non-TTY case uses "pipe `/dev/null` to stdin." The pty helper requirement is named, not hand-waved.
- Issue U6 (TTY decline exit code): RESOLVED — R13's table pins exit 0 on the `N` reply; AC "TTY prompt No" asserts "exit 0; no scaffolding; no on-disk state."
- Issue U7 (visibility fixture endpoint): RESOLVED — Fixture conventions section names `tarballFakeServer` for both tarball and `/repos/{owner}/{repo}` metadata. Adversarial-fixture AC describes the exact JSON to return.
- Issue U8 (network error fixture): RESOLVED — AC "Visibility-lookup soft-fail (network error)" specifies "close the fake server before bootstrap reaches the metadata endpoint." Separate AC covers 500-server-error path.
- Issue U9 (Worktree label prominence): RESOLVED — Appendix B pins exact format including column-aligned values at byte position 30, and the "Worktree label in success block" AC asserts the literal line `Worktree: <absolute-path>`.
- Issue U10 (Allow-list scoping reframe): RESOLVED — AC reframed as "after success, `<instanceRoot>/<group>/` contains only `foo/`; `bar/` and `baz/` are absent." Three repos in the fake org are explicit.
- Issue U11 (commit identity seam): RESOLVED — R22 commits to "an injectable exec invoker (interface or function field)" and the "No-author / no-GIT_AUTHOR_*" AC asserts argv and env via the recorder.
- Issue U12 (Classifier ordering): RESOLVED — N2 enumerates the 5-arm precedence list, and the "Classifier ordering" AC is a table-driven test against that list.
- Issue U13 (Mutual exclusion wording): RESOLVED — R25 pins the exact string `--bootstrap and --no-bootstrap are mutually exclusive`, and the AC asserts "the exact R25 string."
- Issue U14 (Rollback fault injection): RESOLVED — Both rollback ACs name the trigger (clone failure via `localGitServer`; daemon-spawn timeout via fault injection). Injectable exec invoker provides the seam.
- Issue U15 (Success block ordering): RESOLVED — Appendix B fixes line order explicitly ("Lines must appear in the order shown"), and AC "Success block format" asserts "line ordering and exact-string comparison."

### Missing Test Coverage (18)

- Issue M1 (R2 unchanged behavior): RESOLVED — AC "R2 regression check (no-flag baseline)" added.
- Issue M2 (R3 inline comment): RESOLVED — AC "Inline comment on `[channels.mesh]`" asserts the exact comment line.
- Issue M3 (R5 deterministic): RESOLVED — R5 prose explains "each `niwa session create` invocation generates a fresh `<sid>`, retries always produce distinct branch names; no collision-detection logic is required." Equivalent assertion semantically.
- Issue M4 (R5 stored in session state): RESOLVED — AC "Branch-name stored in session state" asserts the `branch_name` field equals `niwa-bootstrap/<sid>`.
- Issue M5 (R14 section ordering golden): RESOLVED — Appendix A provides the golden literal body and substitution rules; AC "Scaffold byte-equality" asserts byte-for-byte match.
- Issue M6 (R14 `[claude.content.workspace]` hint and footer): RESOLVED — Appendix A includes the commented hint and schema-doc URL footer; AC "Scaffold byte-equality" covers them.
- Issue M7 (R15 `.gitkeep`): RESOLVED — AC "`.gitkeep` present" asserts zero-byte file.
- Issue M8 (R17 exact note line): RESOLVED — R17 pins exact text with `<cause>` enumeration; AC asserts "stderr contains the exact R17 note with `<cause>` = `server error`/`network error`."
- Issue M9 (R20 landing-path file contract): RESOLVED — R20 specifies file path, env var (`NIWA_RESPONSE_FILE`), and format (single line, no trailing newline); AC asserts "Landing-path file contents equal the worktree absolute path."
- Issue M10 (R22 git invocation safety): RESOLVED — R22 mandates `exec.CommandContext` with separate args, and ACs "Host-check ordering at exec layer" and "No-author / no-GIT_AUTHOR_*" exercise the invoker pattern. Combined with R22's injectable seam.
- Issue M11 (R23 exit codes pinned per step): RESOLVED — R23 provides exit-code table (0/1/2/3/4) and every failure AC names the expected exit code explicitly.
- Issue M12 (R24 no-push): RESOLVED — AC "No-push assertion" asserts "no `git push` invocation across the happy path."
- Issue M13 (R25 mutual exclusion wording): RESOLVED — see Issue U13.
- Issue M14 (N1 latency budget): RESOLVED — Acknowledged as dropped in Known Limitations; N1 marked "Moved to Known Limitations — no fixed latency target in v1." Explicit flag, not silent omission.
- Issue M15 (N2 typed error wiring): RESOLVED — N2 says "String-matching against error text is not acceptable," and AC "Classifier ordering" is the table-driven test against `errors.As` arms.
- Issue M16 (N5 no-secret-written): PARTIAL — N5 declares the invariant ("No user secrets shall be written to disk") but no AC explicitly asserts grepping scaffold + state files for the token value. See New Issues #1.
- Issue M17 (R8 three sub-cases): RESOLVED — R8 enumerates three sub-cases with exact Detail+Suggestion text, and three "Conflict sub-case" ACs cover each.
- Issue M18 (already-niwa-configured remote): RESOLVED — Out of Scope list explicitly says "Adopting an already-configured remote. That's today's clone path and stays unchanged. Bootstrap fires only on `*config.NoMarkerError` plus explicit user intent." The classifier-precedence list (N2) and Token Semantics table imply the existing-config path is the no-bootstrap-flag, no-prompt happy path. Acceptable; the AC "R2 regression check" exercises an existing-config remote without `--bootstrap`.

## New Issues Found

1. **N5 secret-on-disk assertion not concrete**: N5 asserts "No user secrets shall be written to disk" as an invariant, but no AC mechanizes the check. Suggested fix: add an AC under "Test-seam and invariant assertions" reading "**No-secret-on-disk**: with `GH_TOKEN=ghp_TESTSENTINEL`, after a successful bootstrap, recursively grep `<cwd>/<name>/` for the literal string `ghp_TESTSENTINEL` and assert zero matches."

2. **Fixture tag missing on three "Test-seam" ACs**: The Test-fixture conventions section says "Each AC names the fixture(s) required," but the following ACs do not explicitly name their fixture(s): "Cleanup-defer at create-fail" (says "force `git fetch` to fail" but does not say whether via injectable exec invoker or `localGitServer`), "Cleanup-defer at init-fail" (no fixture named), and "Classifier ordering" (no fixture named — unit test against constructed error chains, which should be stated). Suggested fix: add `(fixture: injectable exec invoker)` or `(unit; no fixture)` tags to those three ACs.

3. **R13 prompt text — `Y` casing ambiguity**: R13 specifies the prompt `[Y/n]` and "Proceed only on Y" but does not clarify whether `y` (lowercase) is also accepted. Convention in CLI prompts is yes; the AC "TTY prompt Yes" uses `y\n`. Suggested fix: clarify R13 ("Proceed on `y`, `Y`, or empty line; decline otherwise") or pin the AC to assert both lowercase and uppercase if applicable. Minor.

4. **R7 daemon-shutdown timeout is unobservable in AC**: R7 says "Daemon-shutdown timeouts do not block the rollback" but no AC asserts this — e.g., a test where the daemon's SIGTERM handler ignores the signal and the rollback still completes within bounded time. Suggested fix: add an AC under "Rollback" reading "**Daemon-shutdown timeout during create-fail rollback**: fault-inject a daemon that ignores SIGTERM; assert rollback completes within 6s (5s grace + jitter) and exits 1."

5. **R8 sub-case 3 file/symlink/directory variant coverage**: AC "Conflict sub-case 3" tests only the `file` variant (`touch <cwd>/bar`). R8 sub-case 3 enumerates `file|symlink|directory` distinctly in the Detail string. Suggested fix: extend the AC to a table-driven test covering all three variants, or split into three ACs.

6. **R6 parity assertion is one-way**: AC "`niwa session create` parity (R6)" asserts that *standalone* session-create works on a bootstrap-produced workspace, but R6 says "the chain shall pass the same arguments and environment to the internal create call that `niwa create` would receive standalone." A direct argument/env-equivalence assertion (e.g., recorder snapshot from chained-create vs standalone-create) is absent. Suggested fix: add a unit test asserting equality of the create-call argument struct between standalone and chained invocations.

7. **R19 blank lines around success block**: R19 says "preceded by one blank stderr line and followed by one blank stderr line" but the "Success block format" AC says "ignoring `<placeholder>` runtime substitution" — does it also assert the blank-line padding? Suggested fix: state explicitly that the AC asserts the leading and trailing blank stderr line.

## Summary

Round 2 is a substantial improvement: every round-1 untestable criterion now has a named fixture and exact-substring assertion, every previously-missing test coverage gap has an AC (with one partial), and the introduction of Appendices A/B as canonical golden bodies eliminates the wording-drift risk that dominated round 1. The remaining new issues are secondary (N5 mechanization, three minor fixture tags, three small AC tightenings) and do not block a test author from writing a complete test plan from the AC alone — they are polish items that would close the last 5% of ambiguity.
