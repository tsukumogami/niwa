# Clarity Review

## Verdict: PASS

The PRD is exceptionally specific: requirements name exact commands, files, error codes, and behaviors; acceptance criteria are binary; minor ambiguities exist but none would cause two implementers to ship materially different things.

## Ambiguities Found

1. **R21 (Non-Functional, line 231-233)**: "under 5 seconds in the happy path on a typical developer machine. Includes daemon-terminate grace period." -> "Typical developer machine" is undefined; the SLO is unmeasurable without a reference environment. The "5 seconds" budget also potentially conflicts with R6's "default 5 seconds" destroy grace period — if the grace period alone consumes the budget, the SLO is unachievable in steady state. -> Specify the reference environment (e.g., "Linux x86_64, NVMe-class storage, 4+ CPU cores") and clarify whether the 5s budget is measured with `NIWA_DESTROY_GRACE_SECONDS=0` or whether the worker is expected to terminate in less than 5s in the happy path so the grace period does not bind.

2. **AC2 (line 263-264)**: "the worker's last message" -> "Last message" is ambiguous: the last assistant turn, the last tool use, the last user prompt, or simply the most recent transcript entry? A reviewer checking this AC could disagree with the implementer about what "last message" means. -> Restate as "the most recent message in the worker's transcript (matching the final entry persisted by Claude Code prior to attach)."

3. **AC14 (line 308-310)**: "Stderr shows `niwa: waiting for worker on task <task_id>...` periodically." -> "Periodically" has no defined cadence; an implementer could print every 100ms, every 30s, or once. Reviewers cannot verify this objectively. -> Specify the interval (e.g., "every 5 seconds") and whether the message is repeated or printed once with a trailing spinner.

4. **AC26 (line 348-351)**: "exits with the propagated Claude exit code (does not abort)" -> AC26 lives under "Worktree State on Detach" but talks about Claude's exit code, conflating the natural-detach path with the explicit `niwa session detach` operator command. It is unclear whether this AC applies to the natural exit-Claude-Code release path, the explicit `detach` command (which has no Claude process to inherit from), or both. -> Split into two ACs: one for the natural release path on Claude exit, one for the explicit `detach <id>` command, each with its own exit-code expectation.

5. **AC22 (line 331-333)**: "returns `attach` sub-object with `owner_pid`, `owner_start_time`, `started_at`, `lock_path` when a lock is held; omits the field when no lock is held." -> R12 (line 184-186) says "The sub-object shall be omitted (not null) when no lock is held," but R11 (line 176-180) states the field is "populated when an attach lock is held." It is ambiguous whether a stale lock (PID dead, sentinel present) counts as "held" for purposes of inclusion in the MCP response. -> State explicitly: stale locks (sentinel present, owner PID dead) are reported with the sub-object included plus a derived `stale: true` flag, OR are reported as omitted with a separate signal — pick one and document it.

6. **AC28 (line 360-362)**: "fails fast with a UID-mismatch error before attempting any state read" -> "Fails fast" is subjective and the parenthetical undermines the requirement: the AC says "before attempting any state read" but then says "the existing 0600 file permissions produce an `EACCES` that niwa wraps." Reading the file to get EACCES *is* a state-read attempt. -> Clarify whether niwa performs an explicit `os.Geteuid()` check before any I/O, or whether it relies on the EACCES from the first state read and re-frames it. The AC currently allows both interpretations.

7. **R20 / AC26-AC27 (lines 222-226, 348-354)**: "if the worktree has uncommitted changes, untracked files, or unpushed commits on the session branch, detach shall print a `git status` summary as a warning to stderr" -> R20 lists three trigger conditions (uncommitted, untracked, unpushed) but AC26 only covers "uncommitted edits" and AC27 only covers "unpushed commits." Untracked files have no acceptance criterion. -> Add an AC for untracked-files-only worktrees, or merge AC26/AC27 into a single AC enumerating all three trigger conditions.

8. **R7 (line 159-162) vs Known Limitation 5 (lines 489-494)**: "exit codes 1 (validation failure), 2 (usage error), or 3 (lock contention)" -> The mapping from specific failure modes (e.g., R4's three pre-flight errors, R2's wrong-status rejection, AC5's not-found, AC28's UID mismatch) to these three buckets is not specified. Two implementers could classify "session not found" as validation (1) or usage (2) reasonably. -> Add a table mapping each named error condition (R2, R3, R4a/b/c, AC5, AC28, etc.) to its specific exit code.

9. **R6 (line 154-159)**: "poll until any running worker reaches a terminal state before proceeding" -> "Poll" cadence is unspecified, and "terminal state" is not defined in this PRD (it is presumably the existing task-status terminology, but a reviewer not steeped in niwa internals would have to dig). -> Define poll interval (consistent with AC14's "periodically") and either link to or enumerate the terminal states (e.g., `complete`, `failed`, `cancelled`).

10. **AC31 (line 383-385)**: "Unit tests cover lock acquisition, stale-lock detection, pre-flight validation (all three error cases), and the `AVAILABILITY` column rendering" -> "Cover" has no coverage threshold; an implementer could write one test per item and claim AC met. The list also omits behaviors with explicit ACs: force-on-running-worker (AC13), exit-code propagation (R7), default sort (AC21), MCP surface (AC22, AC23). -> Either replace "cover" with "tests for each named behavior listed in the Functional Requirements" or enumerate the test cases expected.

## Suggested Improvements

1. **Add a glossary or definitions section**: Terms like "active session," "terminal state," "session branch," "transcript file," "envelope," "catch-up replay" recur throughout. New contributors would benefit from a one-line definition for each, especially since the PRD references PR #115 and DESIGN-cross-session-communication.md as load-bearing context.

2. **Promote the R7 exit-code mapping into a dedicated table**: Listing each named error condition alongside its exit code, error-code constant (e.g., `SESSION_ATTACHED`), and stderr message template would let implementers and test authors work from a single source of truth and would let Known Limitation 5 ("exit-code asymmetry") become trivially understood instead of requiring readers to assemble the mapping themselves.

3. **Specify cadence/timing constants**: R21 (5s SLO), R6 (grace period 5s, configurable), AC14 ("periodically"), and R6's poll loop all have implicit or named timing. Collecting them into a single "Timing and Limits" subsection — with environment variables, defaults, and SLO targets — eliminates the chance of inconsistent values being chosen by implementers.

4. **Add an explicit AC for the `--force` interaction with destroy on an attached session**: AC23 covers no-force, AC24 covers `niwa session destroy <id> --force`, but the symmetric case (`niwa_destroy_session` MCP with `force: true` against an attached session) is implied rather than stated. Make it explicit.

5. **Clarify the interaction between AC11 (clean kernel release) and AC12 (sentinel reaping)**: Today they read as two ways the same scenario can occur. Distinguish "process exited cleanly, kernel released flock, sentinel still on disk" (AC12) from "process exited cleanly, kernel released flock, sentinel was removed by exiting process" (AC11) so the implementer knows whether the exit path must remove the sentinel.

## Summary

The PRD is unusually rigorous and would not produce wildly divergent implementations: 31 acceptance criteria, 25 numbered requirements, 18 documented decisions with alternatives, and explicit Out of Scope and Known Limitations sections leave little room for misinterpretation. The ambiguities identified are concentrated in non-functional requirements (timing, "typical machine," "periodically"), cross-AC consistency (R20's three trigger conditions vs AC26/AC27's two), and exit-code mapping (R7 leaves the binning rule implicit). These can be resolved with small targeted edits and do not warrant a FAIL verdict; the structural scaffolding is sound.
