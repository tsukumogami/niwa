# Completeness Review

## Verdict: FAIL

The PRD is largely complete and rigorously decided, but several requirements have no acceptance criteria, one acceptance criterion implies behavior with no backing requirement, and at least one open question raised in source issue #117 (multi-worker-per-session transcript selection) is silently dropped rather than explicitly resolved.

## Issues Found

1. **R10 (detach without session_id) has no acceptance criterion.** R10 specifies that `niwa session detach` (no id) prints a usage error explaining detach is operator-only and that normal release is automatic. None of AC15/AC16/etc. exercise this. Suggested fix: add AC "`niwa session detach` with no session_id exits non-zero with a usage error explaining detach is an explicit operator command and release is automatic on Claude Code exit."

2. **R14 (deprecated `mesh list` alias removal) is only half-tested.** AC17 confirms `niwa session list` flagless shows the lifecycle view, but nothing asserts that `niwa mesh list` still works as the coordinator-process-registry direct path. Removing an alias without verifying the non-aliased path would silently break the coordinator workflow. Suggested fix: add AC "`niwa mesh list` (direct invocation) still returns the coordinator process registry view." Also add an AC that the deprecation warning is gone (no `niwa session list` legacy fallback message).

3. **R12 (`niwa_list_sessions` MCP tool) is not fully covered for filter flags.** Scope says filter additions apply to both `niwa session list` AND `niwa_list_sessions`, but R16 only mentions CLI flags (`--attached` / `--available`) and AC19/AC20 only test the CLI. AC22 covers the MCP `attach` sub-object shape, but nothing tests MCP-side filter parameters. Suggested fix: either add MCP filter requirements + ACs, or explicitly drop MCP filter from scope in Out of Scope.

4. **AC28 implies a "UID-mismatch friendly error wrapper" that no requirement defines.** R24 says "No new safeguards are added; existing 0600 file permissions enforce the boundary structurally," but AC28 asserts niwa "wraps in a friendly message". This is contradictory — the requirement says "no new safeguards" while the AC adds new error-wrapping behavior. Suggested fix: either (a) add a requirement that niwa intercepts EACCES on session state files and rewrites it as a UID-mismatch message, or (b) reword AC28 to assert only that the EACCES surfaces clearly without a custom niwa wrapper.

5. **Multi-worker-per-session transcript selection (open question #4 in source issue) is silently resolved.** Source issue explicitly asks: "if the model permits multiple workers per session over its lifetime (sequential or concurrent), the resume semantics need definition. The PRD should investigate whether multi-worker-per-session is possible." The PRD's R1 and R4 assume a single `claude_conversation_id` per session, and the Open Questions section claims "all 7 open questions resolved" — but this question is not in the resolved list. Suggested fix: add an explicit Decision (e.g., D19) stating "today's mesh model is single-worker-per-session; the captured `claude_conversation_id` is reused across worker re-spawns; multi-worker-per-session resume disambiguation is out of scope until a writer for multi-conv-id sessions exists."

6. **Source issue cross-ref #112 (dangling envelopes visibility from inside attached session) is not addressed.** The source issue asks "if a session has dangling tasks when a human attaches, what does the human see? Are the dangling envelopes visible from inside the attached session?" The PRD's Behavior Contract from issue says "mesh queue visibility from inside the attached session: invisible," but the PRD never echoes this contract. R5 mentions catch-up replay AFTER detach but is silent on what the operator sees during attach. Suggested fix: add a requirement (or note under R5) restating the locked-in default — "queued envelopes are invisible to the operator inside the attached Claude Code session; visibility resumes via daemon catch-up replay after detach."

7. **R5 daemon-terminate-on-acquire has no direct AC.** AC1 mentions "terminates the daemon" inline, but no AC isolates that the per-worktree daemon is actually stopped (e.g., via `pgrep` or PID check) during attach. AC3 confirms respawn after detach, but the kill-on-acquire half is implicit. Suggested fix: split AC1 or add "AC1b. While `niwa session attach` is held, the per-worktree daemon process is not running (verifiable via PID liveness check)."

8. **AC14 asserts a polling message format that no requirement specifies.** R6 says "attach shall poll until any running worker reaches a terminal state" but never specifies the stderr format. AC14 then asserts `niwa: waiting for worker on task <task_id>...` is shown. Either elevate the message format into R6, or weaken AC14 to assert only that the operator gets some progress feedback.

9. **R6 polling cadence is unspecified.** "Poll until any running worker reaches a terminal state" — at what interval? Issue source says the wait is unbounded (no `--force`). Add a requirement specifying the poll interval (e.g., "every 1 second") and a Ctrl-C handling note (does Ctrl-C during the poll abort the attach gracefully?).

10. **No AC for SESSION_ATTACHED error code shape.** AC23 confirms the error is returned but doesn't assert the structured error code field, message format, or whether it includes the holder PID. R13 says the message "shall reference `niwa session detach --force`" — confirm via AC text. Suggested fix: tighten AC23 to assert the error_code field equals `SESSION_ATTACHED` and the message contains the recovery command.

11. **No AC for R7's exit code 2 (usage error) or exit code 3 (lock contention).** R7 defines four exit code paths (Claude propagated, validation 1, usage 2, lock 3). AC10 covers lock contention but doesn't assert the exit code is 3. AC5/AC6/AC7/AC8/AC9 cover validation but don't assert exit code 1. No AC asserts exit code 2 for usage errors. Suggested fix: extend the validation ACs to assert exit codes, and add an AC for usage-error exit code 2.

12. **Cross-ref to #115 coordination is not testable.** R11 and R23 promise "coordinated with PR #115" but no AC verifies that both `attach` and `daemon` sub-objects can coexist on `SessionLifecycleState` without schema collision. Suggested fix: add AC "Sessions with both `daemon` and `attach` sub-objects populated round-trip through the V:1 reader/writer without errors."

## Suggested Improvements

1. **Add an AC matrix mapping requirements -> ACs.** The PRD has 25 functional + 5 non-functional requirements and 31 ACs. A traceability table at the bottom of the Acceptance Criteria section would surface gaps automatically and is cheap to maintain.

2. **Make the "demand not validated" Known Limitation a removal trigger.** Limitation #1 says "if future telemetry shows attach is rarely used, the feature should be a candidate for removal." This is a non-actionable signal. Add a concrete success metric to the Goals section (e.g., "≥X attaches per workspace per month after Y weeks of release") so the removal decision has a numeric trigger.

3. **Expand the Persona section.** The PRD has only one persona ("workspace coordinator"). A second persona — the **on-call engineer who inherits a workspace from a colleague** — would surface scenarios the current PRD doesn't address (attaching to a session you didn't create; reading the worker's transcript before deciding to take over). Worth at least a sentence noting the persona is conflated with "coordinator" by design.

4. **Document the Ctrl-C contract during attach.** Operators will reflexively Ctrl-C during the daemon-terminate grace period or during `--force` worker SIGTERM. The PRD doesn't say what happens. Adding an explicit "Ctrl-C during acquire is a clean abort; the lock is not held; the daemon is restarted" line would prevent a future surprise.

5. **State the `attach.lock` and `attach.state` permission bits.** R18 specifies the file paths but not the mode bits. R24 references 0600 from the broader trust model but the PRD should be explicit so reviewers don't have to chase the cross-doc reference.

6. **Add a "Compatibility / Migration" subsection.** Since R14 removes a deprecated alias path, existing scripts may break. A note explaining "users running `niwa session list` expecting the legacy mesh-list output should migrate to `niwa mesh list`" would help the release notes.

## Summary

The PRD is well-decided and exceptionally thorough on rationale (17 numbered Decisions, 5 Known Limitations, full Out of Scope inventory). The completeness gaps are concentrated in two areas: (a) AC coverage is missing for several requirements (R10, R12 MCP filters, R14 mesh-list direct path, R7 exit codes, R5 daemon kill, R13 error code shape), and (b) source issue #117's multi-worker-per-session question is implicitly resolved without acknowledgement, despite the PRD claiming all open questions are closed. None of these are deal-breakers but together they leave enough ambiguity that an implementer would need to make judgement calls — which is the failure mode the completeness gate exists to catch.
