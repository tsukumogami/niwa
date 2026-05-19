# Completeness Review (round 2)

## Verdict: PASS

All 20 round-1 findings are resolved in substantive detail (new Flag Interactions table, Token Presence Semantics table, Notices & Observability table, Appendix A scaffold body, Appendix B success block, exit-code table at R23, stdout/stderr split, R5 fallback promoted into the requirement, R26 explicit "no apply"), and the small set of new gaps found is minor enough to leave to design without re-spinning the PRD.

## Round 1 Resolution Status

- Issue 1 (Sibling init-flag interactions): RESOLVED — the "Flag Interactions" subsection (lines 351-362) enumerates `--overlay`, `--no-overlay`, `--skip-global`, `--no-install-plugins`, `--rebind` (refused, exit 2), and `--no-bootstrap`. Matches the round-1 ask exactly.
- Issue 2 (`GH_TOKEN` unset behavior): RESOLVED — the "Token Presence Semantics" table (lines 364-379) covers 8 token-presence × visibility × outcome rows including "No (unset) / Private / 404 → R11" and "No (unset) / Public / 200 → R13".
- Issue 3 (Exit-code convention): RESOLVED — R23 (lines 316-324) now defines codes 0 (success or clean TTY decline), 1 (step failure with `bootstrap step=<step>:` prefix), 2 (flag validation), 3 (host validation), 4 (NoMarker fail-fast). TTY decline = 0 is now deterministic.
- Issue 4 (stdout vs stderr split): RESOLVED — the "Stdout vs Stderr" subsection (lines 343-349) defines stdout = per-step progress, stderr = success block + notes + errors + prompts, landing-path file = worktree absolute path.
- Issue 5 (Telemetry/Observability): RESOLVED — the "Notices & Observability" subsection (lines 412-433) enumerates 12 trigger × surface × format rows including reporter status/log lines, the rollback notes, and explicit "no new telemetry."
- Issue 6 (Literal scaffold body): RESOLVED — Appendix A (lines 791-832) inlines the literal TOML body with `<placeholder>` tokens, plus a substitution-rules table. R3 now refers to Appendix A as "single source of truth, byte-for-byte after substitution."
- Issue 7 (R5 fallback in requirements): RESOLVED — R5 (lines 155-161) now explicitly states "When `branch_name` is empty or absent (back-compat), session callers fall back to constructing `session/<sid>`."
- Issue 8 (R7 rollback gaps re daemons): RESOLVED — R7 (lines 171-185) now specifies "Daemon shutdown during create-fail rollback follows the same contract as `niwa destroy --instance`: 5s graceful via SIGTERM, then SIGKILL. Daemon-shutdown timeouts do not block the rollback."
- Issue 9 (Bootstrap does not run apply): RESOLVED — R26 (lines 335-340) explicitly states "Bootstrap shall NOT invoke `niwa apply` as part of the chain" and explains channel infrastructure comes from create's pipeline.
- Issue 10 (AC for R6 equivalence): RESOLVED — "`niwa session create` parity (R6)" AC (lines 651-654) asserts the standalone session-create command succeeds against a bootstrap-produced workspace.
- Issue 11 (AC for R17 wording): RESOLVED — two ACs ("Visibility-lookup soft-fail (server error)" and "(network error)" at lines 592-600) explicitly assert the exact R17 note with `<cause>` substituted.
- Issue 12 (AC for R18 commit subject): RESOLVED — the "Happy path with positional name" AC (line 467-468) now asserts "commit subject equals `Initial niwa workspace config`."
- Issue 13 (AC for R20 landing-path mechanism): RESOLVED — R20 (lines 299-302) now names the existing helper (`workspace/landing.go::writeLandingPath`) and the environment variable (`NIWA_RESPONSE_FILE`); happy-path AC asserts "Landing-path file contents equal the worktree absolute path."
- Issue 14 (Integration AC for host-validation): RESOLVED — the "Non-GitHub source" AC (lines 502-505) now asserts "the injectable exec invoker records zero git invocations" — this is an end-to-end behavioral assertion rather than purely unit-level. Combined with the unit-level "Host-check ordering at exec layer" AC (lines 604-607), coverage is solid.
- Issue 15 (AC for R22 argv-injection invariant): RESOLVED — R22 (lines 311-314) mandates the injectable exec invoker; the "No-author / no-GIT_AUTHOR_*" AC asserts argv-element-level invariants; "Host-check ordering at exec layer" asserts zero invocations. The argv-injection slug case is not explicitly tested, but slug validation happens upstream via existing `ValidateInitName`-style checks (referenced in Appendix A's substitution rules table).
- Issue 16 (R13 cross-references R25): RESOLVED — R13 (lines 234-236) now states "R25 (mutual exclusion) runs upstream of R13 and rejects `--bootstrap` + `--no-bootstrap` before R13 fires."
- Issue 17 (R8 three sub-cases each with text): RESOLVED — R8 (lines 187-204) now enumerates three numbered sub-cases each with literal Detail and Suggestion text.
- Issue 18 (AC asserts `.gitkeep`): RESOLVED — happy-path AC line 458-459 lists `<cwd>/my-project/.niwa/claude/.gitkeep` as a zero-byte file; a separate "`.gitkeep` present" AC (lines 571-572) re-asserts it.
- Issue 19 (AC for R3 inline comment wording): RESOLVED — the "Inline comment on `[channels.mesh]`" AC (lines 578-580) asserts the line preceding the block matches exactly the expected comment text.
- Issue 20 (AC for R6 negation / no new precondition): RESOLVED — the "`niwa session create` parity (R6)" AC (lines 651-654) demonstrates the negation: standalone session-create works against a bootstrap-produced workspace "with no re-initialization of state."

Round 1 OPEN count: 0.

## New Issues Found

1. **AC for R9 doesn't assert "no partial state written to disk."** R9 (line 210) states "No partial state shall be written to disk." The "Non-GitHub source" AC (lines 502-505) asserts exit code 3, the R9 exact string, and zero git invocations — but does not assert the absence of `<cwd>/<name>/`, registry entry, or any other filesystem artifact. Suggested fix: append to the AC, "and `<cwd>/bar/` does not exist after the call returns; no registry entry for `bar` exists."

2. **Appendix B column-alignment rule is testable but no AC verifies it.** Appendix B (lines 851-855) states "alignment column is byte position 30, padding with spaces." The "Success block format" AC (lines 661-664) says "matches Appendix B's exact format (line ordering and exact-string comparison)" — but byte-position-30 alignment is a different invariant from exact-string match (the substituted paths will differ across runs). Suggested fix: add an AC that asserts the four label lines (`Workspace bootstrapped at:`, `Instance:`, `Worktree:`, `Branch:`) each have a value starting at byte offset 30, or weaken Appendix B to drop the explicit byte-position claim if it isn't required.

3. **R23 exit code 1 sub-classification by step prefix is asserted only for create and session steps.** The "Rollback at create step" and "Rollback at session step" ACs assert `bootstrap step=create:` / `bootstrap step=session-create:` prefixes. But the "Rollback at init step" AC (lines 549-551) does not assert the `bootstrap step=init:` prefix. R23's table claims all three step-failures emit the literal prefix. Suggested fix: add the prefix assertion to the init-step rollback AC.

4. **R17 cause-string vocabulary is closed but no AC pins the four exact cause values.** R17 (lines 270-278) names four `<cause>` values: `network error`, `authentication`, `not found`, `server error`. Only `server error` and `network error` have ACs. Suggested fix: add ACs (or a table-driven AC) for the `authentication` (401/403 on visibility lookup) and `not found` (404 on visibility lookup) cause strings, so all four arms of the cause vocabulary are pinned.

5. **N4's "durable user-facing contract" has no AC.** N4 (lines 402-404) declares the branch-name format a durable contract. No AC verifies anyone (e.g., session state, success block, R5's fallback) actually agrees on the format. Suggested fix: add a cross-reference assertion AC, "the branch name written to `<sid>.json`'s `branch_name`, the branch name shown in the success block, and the result of `git branch --show-current` all return the same string matching `^niwa-bootstrap/[0-9a-f]{8}$`."

6. **Requirement R6's pass-through claim ("same arguments and environment") has no AC.** R6 (lines 163-168) says "the chain shall pass the same arguments and environment to the internal create call that `niwa create` would receive standalone." The "session create parity" AC (lines 651-654) only checks the user-observable outcome (session-create works), not the argument equivalence. The Flag Interactions table claims pass-through behavior for `--overlay`, `--no-overlay`, `--skip-global`, `--no-install-plugins`. Suggested fix: add a unit-level AC using the injectable exec invoker that captures the create-call argv and asserts each flag value flows through (parameterized by flag).

## Suggested Improvements

1. **Add an N1 latency budget once a measurement exists.** N1 is currently "(Moved to Known Limitations — no fixed latency target in v1.)" That's a reasonable v1 stance, but the Known Limitations note (lines 762-766) could explicitly say "after v1 ships, measure p50/p95 and add a target in v1.1" to make follow-up actionable.

2. **Cross-link Appendix A's substitution table to R14.** R14 says ordering and content live in Appendix A. The substitution-rules table in Appendix A could explicitly note that R14 mandates `<vis-key>` derives from `Repo.Private`, not `Repo.Visibility` — currently that constraint lives in R16, and Appendix A's table doesn't mention R16. A reader of Appendix A in isolation might miss the security invariant.

3. **Strengthen the AC for the byte-equality contract.** The "Scaffold byte-equality" AC (lines 567-570) says "matches Appendix A's golden body literally (with `<placeholder>` substitutions for the workspace)." Spell out the AC mechanism: "the test reads the file, applies a known-value substitution to Appendix A's golden body, and compares with `bytes.Equal`." Otherwise an implementer might interpret "literally" as "by parsed-AST equivalence" and silently lose comment placement.

4. **Consider adding a Future Work bullet about adopting non-GitHub providers under Out of Scope.** "Non-GitHub remotes" is in Out of Scope, but no acceptance test asserts that, e.g., a `--from gitlab.com/owner/repo --bootstrap` invocation produces the exact R9 string for GitLab, Gitea, and Bitbucket separately. The "Non-GitHub source" AC uses GitLab only. Either add fixtures for more providers or note that the check is purely host-string-based and any non-`github.com` host suffices.

## Summary

The revision substantially addresses every round-1 finding with new structured sections (Appendix A/B, Flag Interactions, Token Presence Semantics, Notices & Observability) and tighter requirements (R5 fallback, R7 daemon contract, R8 three sub-cases, R23 exit-code table, R26 explicit no-apply). All 20 round-1 issues are resolved; the 6 new gaps found are narrow AC-coverage holes and one Appendix-B testability nit, none of which warrant another revision cycle. The PRD is ready to move out of Phase 4 review.
