# Testability Review

## Verdict: FAIL

The acceptance criteria are detailed, well-organized, and cover most of the surface, but several criteria reference error messages "case-A/B/C" without defining the strings, depend on subjective UX assertions ("transcript visible", "first interactive prompt shows the worker's last message"), and a handful of requirements (R5 catch-up replay, R6 grace-period config, R14 mesh-list alias removal, R21 latency budget, R25 non-Linux fallback) have no corresponding AC.

## Untestable Criteria

1. **AC1** ("...launches Claude Code with the worker's transcript visible"): "transcript visible" is observational and undefined — what process state, file artifact, or stdout marker constitutes "visible"? -> Replace with a concrete check: e.g., "the launched process's argv contains `claude --resume <conversation_id>`" and/or "the process's CWD equals the worktree path". Move the transcript-content assertion to AC2 only.

2. **AC2** ("first interactive prompt shows the worker's last message"): Inspecting Claude Code's TTY rendering inside a functional test is unreliable, and "last message" is not defined (last assistant turn? last tool result? last user prompt?). -> Either (a) assert the resume happens against the captured `claude_conversation_id` (process-argv check) and trust Claude's own behavior, or (b) seed a known-content fixture transcript and grep the rendered output for a specific marker string.

3. **AC4** ("the daemon picks up the new envelope (catch-up replay handles any envelopes queued during attach)"): The parenthetical is two assertions in one — pickup of a post-detach envelope, and replay of envelopes enqueued during attach — but only the first is testable as written. -> Split into AC4a (post-detach delegate is processed) and AC4b (an envelope written to the inbox while attached is processed within N seconds of detach).

4. **AC7 / AC8 / AC9** ("case-A / case-B / case-C error message"): The PRD never defines the literal text or shape of these three messages, so a test author cannot assert on them. R4 only enumerates the three cases, not the message content. -> Add the exact error message strings (or a regex) for each case to R4 or to the AC, e.g., AC7 stderr matches `niwa: error: session <id> has no captured conversation id (worker may have crashed before MCP registration)`.

5. **AC10** ("identifying the holder's PID, the start time, and the recovery command"): Acceptable as written but fragile without a reference example; "start time" format is unspecified (Unix epoch? RFC3339? human-relative?). -> Pin the format, e.g., "stderr contains `pid=<int>`, `started=<RFC3339 timestamp>`, and the literal substring `niwa session detach --force`".

6. **AC14** ("Stderr shows `niwa: waiting for worker on task <task_id>...` periodically"): "periodically" is unbounded — 1s? 30s? Once? -> Specify the cadence: "the waiting line is emitted at least once per 5 seconds while the worker is alive."

7. **AC21** (default sort): testable in principle, but "creation time descending" tiebreaker presumes deterministic creation timestamps. -> Add: "with three sessions created in known order, the listing order matches the documented sort key" — fixture-driven rather than relying on real timestamps.

8. **AC26 / AC27** ("prints a `git status` summary", "same style of warning as `branch_warning`"): "summary" and "same style" are subjective. -> Specify the exact stderr prefix (e.g., `warning: worktree has uncommitted changes`) and either inline a sample line format or reference the `branch_warning` test fixture as the contract.

9. **AC28** ("UID-mismatch error before attempting any state read"): "before attempting any state read" is an internal-ordering assertion that's hard to verify from the outside. -> Either drop the ordering clause and only assert the user-visible error, or test via a deterministic side-effect (e.g., no read access events on the state file in an strace or audit hook).

10. **AC29** (documentation AC): "documenting" a section is subjective and not mechanically testable. -> Convert to a checklist-style assertion: "section heading `Human-in-the-Loop: Attaching to a Session` exists in `docs/guides/sessions.md` and contains subsections X, Y, Z" so a docs-lint or grep test can verify it.

11. **AC31** ("Unit tests cover..."): Self-referential — an AC that mandates unit tests is not itself a verifiable user-visible behavior. -> Acceptable as a process gate, but flag it as a CI/coverage requirement, not a behavioral AC.

## Missing Test Coverage

1. **R5 catch-up replay of envelopes queued during attach**: AC4 alludes to this in a parenthetical but no AC explicitly enqueues an envelope while the lock is held and asserts it is processed after detach. -> Add a dedicated AC.

2. **R6 configurable grace period via `NIWA_DESTROY_GRACE_SECONDS`**: AC13 verifies the default-grace SIGTERM→SIGKILL flow but does not exercise the env-var override. -> Add: "with `NIWA_DESTROY_GRACE_SECONDS=1`, attach --force escalates to SIGKILL within ~1 second."

3. **R6 SIGKILL escalation on a non-terminating worker**: AC13 only covers the SIGTERM step; no AC asserts that a worker that ignores SIGTERM is SIGKILLed.

4. **R7 exit-code semantics**: R7 specifies four distinct codes (1 validation, 2 usage, 3 lock contention, propagated capped at 125). Only the propagation case is implicitly covered. -> Add ACs that assert exit code 1 on validation failure, 2 on usage error, 3 on lock contention.

5. **R10 detach with no session_id**: R10 specifies a usage-error message; no AC verifies it.

6. **R11 / R18 sentinel JSON shape on disk**: R11 and R18 define the sentinel file fields (`owner_pid`, `owner_start_time`, `started_at`, `lock_path`); no AC asserts the file is written with those keys, atomically, with 0600 perms.

7. **R14 deprecated `mesh list` alias removal**: AC17 says the default is the lifecycle view but does not assert that the deprecated alias path is gone (e.g., `niwa mesh list` still works directly per R14, but the deprecation warning path on `niwa session list` should not fire).

8. **R21 acquire-to-launch latency budget (under 5 seconds)**: No AC measures latency.

9. **R22 / R23 / R24 / R25 non-functional requirements**: No-new-deps, no-V-bump, single-UID-by-reference, and Linux-only fallback are declarative; only R25's fallback behavior on non-Linux is plausibly testable and has no AC.

10. **R12 MCP `attach` sub-object omitted (not null) when no lock**: AC22 covers the populated case and says "omits the field when no lock is held", but does not explicitly assert the JSON does not contain a `null` value. -> Strengthen AC22 to assert key absence rather than null-ness.

11. **R13 `SESSION_ATTACHED` error code**: AC23 covers the error but does not assert the literal code string `SESSION_ATTACHED` in the structured MCP response.

12. **R19 opportunistic reaping by readers**: R19 says `niwa session list`, `attach`, and `detach` may opportunistically reap stale locks. AC11/AC12/AC15 cover the attach/detach paths; no AC covers `niwa session list` reaping a stale sentinel.

13. **R20 untracked-files warning**: R20 enumerates uncommitted, untracked, and unpushed. AC26 covers uncommitted, AC27 covers unpushed; untracked-only is missing.

14. **Concurrent-attach FIFO rejection (Known Limitation 4)**: No AC verifies that a third concurrent attempt also fails fast (vs. queuing).

15. **`abandoned` status rejection**: AC6 covers `ended`; R2 says "anything other than active" is rejected, but `abandoned` (called out in Out of Scope) has no AC asserting the rejection error.

16. **Daemon respawn timing on detach**: AC3 asserts the daemon is respawned but not within a deadline; a flaky test could pass with the respawn happening 60s later.

## Summary

The PRD is structurally strong — 31 ACs grouped by concern, with explicit happy-path, validation, contention, force, discovery, destroy-interaction, worktree-state, multi-user, docs, and coverage buckets. It fails testability primarily because three pre-flight error messages are referenced but never defined ("case-A/B/C"), several criteria use observational language ("transcript visible", "summary", "periodically", "same style") without concrete assertions, and roughly a dozen requirements (notably R5 catch-up replay, R6 grace-period env var and SIGKILL escalation, R7 niwa-side exit codes, R10 usage error, R11/R18 sentinel file shape, R21 latency, R20 untracked files) lack matching ACs. Tightening the message-text definitions and adding the missing-coverage ACs would flip this to PASS without restructuring the document.
