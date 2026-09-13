# Testability Review

## Verdict: FAIL
Most criteria are concrete enough to turn directly into unit and godog scenarios, but a handful can't be written as tests yet: one is an either/or, one relies on probabilistic repetition, one depends on an unstated timeout, and the script-facing output contract is never pinned down. On top of that, several requirements (R9's full breadth, R12, R14's refresh half, R16 on macOS, R17) have no criterion at all. The fixes are small and mechanical.

## Untestable Criteria

1. **"`niwa worktree list`, `apply`, `attach`, `detach` and `go` run at the workspace root ... each either resolves a session per R1-R3 or prints a message saying to run inside an instance or pass a session id."**: The expected result is an either/or, and the Open Questions section leaves `list` undecided, so a tester can't write the expected output for any of the five commands. "No longer read the mapping store as worktree records" is an internal property, not observable behavior. No exit code is given either. -> Pin one behavior per command, e.g. "`list` at the root with no id prints `<fixed phrase>` and exits non-zero; `attach <session-id>` resolves per R1-R3". Resolve the open question before the PRD is accepted. Replace "no longer read" with the observable check: with at least one mapping in the root store, none of the five commands prints an empty result or exits 0 with no output.

2. **"Two concurrent refreshes of one configuration directory with a non-GitHub source both complete without error, repeated enough times in the test that the pre-fix code fails it."**: "Enough times" is undefined, and a race test that only fails some of the time is flaky by nature. It can pass on pre-fix code on a quiet CI runner, and there's no way to tell "fixed" apart from "didn't hit the window". -> Force the interleaving the way the next two criteria do: a test seam holds refresh A between staging and swap while refresh B runs. If a repetition test stays, give the iteration count and require it to fail deterministically (N out of N runs, or at least once in a fixed seed/count) against the pre-fix commit.

3. **"A refresh whose fetch is held for longer than the bounded wait does not block a concurrent mapping write for that long"** and **"A command blocked past the bounded wait exits non-zero with a message naming the configuration directory."**: The PRD never gives the bounded wait's value or says whether a test can override it. Without that, the tester can't choose a hold duration, and a real multi-second timeout makes the suite slow. The second criterion also doesn't say what "blocked" means (which lock, held by what) or require the "another niwa command is using it" wording from R15. -> State the default bound and a test override (env var or package-level variable). Describe the blocking setup ("a second process holds the configuration-directory lock for bound + 1s"). Require the message to contain the directory path and a fixed phrase, and assert the command returns within bound plus a small slack, so "rather than hanging" gets checked too.

4. **"The reaper, run while a refresh of the root configuration directory is swapping it, does not destroy a mapped instance it would otherwise spare."**: "While ... swapping" is a window a few microseconds wide. Unlike the mapping criteria, this one doesn't say the ordering is forced or that the test must fail on pre-fix code. It also leaves out the precondition that makes the bug real: the reaper's backstop sweep only reclaims unmapped dispatch instances older than 30 minutes, so the test has to backdate the instance. -> Rewrite as "with a mapped dispatch instance aged past the backstop threshold, a reaper whose mapping-store read is forced to land mid-swap leaves the instance and its mapping in place; the test fails on pre-fix code."

5. **"From inside an instance, `niwa worktree destroy <worktree-id>` behaves exactly as before the change."**: "Exactly as before" has no oracle unless the tester goes and reads the old code. -> Tie it to an artifact: "the existing destroy unit and functional tests pass unchanged", plus an explicit check that stdout, stderr and exit code match for the merged, unmerged, attached and uncommitted cases.

6. **Message-content criteria ("produces a message saying so", "a message saying the instance was already removed", "a message naming the id", "is reported")**: Without fixed wording or a stream (stdout or stderr), a tester can only assert "some output appeared", and the scripting user story can't be checked at all (see Missing Test Coverage 1). -> Name a stable phrase or keyword for each outcome and the stream it goes to. The "no niwa-managed worktree" wording also has to meet the Known Limitations rule that it not imply branches were checked, and that rule is only checkable if the text is fixed.

7. **"A mapping whose instance path is outside the workspace is reported and nothing is destroyed."**: There's no exit code, and "outside the workspace" is narrower than R7, which requires the path to name an *instance* of the same workspace. A path inside the workspace root that isn't an instance (the root itself, the `.niwa` directory, a repo directory) falls outside this criterion. -> Give the exit code and add cases for a path inside the root that isn't an instance, a path in another workspace's instance, and a symlink that escapes the root.

8. **"with two such mappings the command refuses and names both"** (the handle-less prefix case): There's no exit code and no "without destroying anything", unlike the parallel worktree/handle ambiguity criterion. -> Add "exits non-zero and destroys nothing".

## Missing Test Coverage

1. **Script-distinguishable outcomes (Goal 2 and the cleanup-script user story)**: "Destroyed" and "nothing to destroy" both exit 0, and so does "destroyed but kept an unmerged branch". The distinction therefore rests entirely on output, and no criterion defines that output as a stable contract. Missing AC: one criterion giving, for each of destroyed, destroyed-with-kept-branch, nothing-to-destroy, instance-already-removed and no-such-session, the exit code and a fixed, grep-able marker a script can match.

2. **R9 breadth**: The ACs only cover two refreshes of "one configuration directory" and parallel `niwa dispatch`. R9 names `niwa create`, `niwa apply`, the ephemeral-session hook, overlay directories and the global configuration directory. Missing AC: concurrent refresh of an overlay directory and of the global configuration directory, and a mixed-command case (e.g. `dispatch` alongside `apply` or the hook).

3. **R10, second half**: The AC checks the mapping is present afterwards, but not that "a mapping write never fails because a refresh moved the directory under it". Missing AC: the forced-ordering write returns success (no error, exit 0 at the command level).

4. **R12 (configuration directory never left missing its content)**: No criterion. Missing AC: after a forced write-during-swap, the configuration directory contains its expected configuration files (e.g. `workspace.toml` is present and parses).

5. **R14, refresh half**: R14 says a slow fetch doesn't hold up another command's mapping write *or refresh*. The AC covers only the write. Missing AC: with refresh A's fetch held, refresh B of the same directory completes within the bound.

6. **R16 on macOS, and the fallback documentation**: The ACs cover a Linux run and a non-unix build. macOS, which R16 names explicitly, isn't covered. Neither is the requirement that the fallback gap be documented where it's defined. Missing AC: the ordering tests run on darwin (CI matrix or a manual step), and the non-unix fallback file carries a comment stating that no ordering is provided (checkable in review).

7. **R17 (no new prompts, unchanged exit codes, no new dependency)**: Only the inside-instance destroy case touches this. Missing AC: `go.mod`/`go.sum` gain no new module, the existing unit and functional suites pass unchanged, and no teardown or provisioning path reads stdin (e.g. run with stdin closed).

8. **R2's attach guard**: The AC covers the uncommitted-changes refusal, but not the refusal for an attached worktree. Missing AC: a session whose instance holds an attached worktree exits non-zero, leaves it in place, and reports the others.

9. **R1, handle equal to the session id**: "For an agent whose handle is its session id, the one string serves as both" has no AC. Missing AC: a mapping whose recorded handle equals its session id resolves by that string, and isn't reported as ambiguous with itself.

10. **R3 with a handle**: Resolution from inside another instance and from inside a worktree is tested only with the full session id. Missing AC: the handle form resolves from both locations. From inside an instance this is exactly where R5's collision check applies, so the non-colliding case needs coverage too.

11. **R4 edge cases**: Nothing says what happens when the value isn't exactly 8 hex characters (shorter prefix, uppercase, non-hex). Nothing covers a value that equals one mapping's recorded handle and is also a prefix of a handle-less mapping's session id. And nothing checks that a mapping *with* a recorded handle isn't matched by its session-id prefix. Missing AC: expected result for each.

12. **R5 guidance text**: R5 requires the refusal to tell the developer to pass the full session id or run from the other location. The AC checks only that both matches are named. Missing AC: the refusal message includes that guidance.

13. **Parallel dispatch success criteria (Goal 3, user story 4)**: The AC checks four exits of 0 and four distinct mappings. The goal also promises a working instance and a mapping that's reachable and resumable. Missing AC: each of the four mappings points at an existing instance directory, and `niwa list` shows all four.

14. **Reaped-mapping resurrection end to end (Goal 4, user story 5)**: The forced-ordering delete test covers the store level. There's no command-level check that a `niwa reap` running alongside a provisioning `niwa dispatch` leaves the reaped mapping gone and the live one present. Missing AC, or an explicit note that the unit-level test is considered sufficient.

15. **"No file changes" scope**: The no-worktree criterion says "with no file changes" without naming which files. Missing detail: which trees are compared (instance directory, worktree records, root mapping store).

## Summary
The teardown criteria are the strongest part of this PRD: each gives a setup, an action and an observable result, and nearly all can go straight into a godog scenario using the fake claude binary and the file:// git server. The concurrency criteria mostly get it right by forcing the interleaving. The exceptions are the repeated-race test, the reaper test and the timeout tests, which need a forced ordering and a stated bound. What keeps this from passing is the R8 either/or, the missing stable output contract the scripting goal depends on, and requirements (R9's overlay/global/mixed-command scope, R12, R14's refresh half, R16 on macOS, R17, the attach guard) that no criterion exercises. Adding those ACs and fixing the eight criteria above would make the PRD fully test-plannable.
