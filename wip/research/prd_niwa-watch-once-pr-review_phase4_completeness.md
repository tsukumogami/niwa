# Completeness Review

## Verdict: FAIL

All prior FAIL issues are resolved, but two requirements still lack acceptance-criteria coverage — R12's poll-failure branch and R13 — which is the same missing-AC standard the prior review failed on.

## Issues Found

1. **R12 poll-failure branch has no AC.** R12 and D6 make "fail loud on a failed GitHub poll (query error, expired auth, host unreachable, rate limit): report the error, exit non-zero, record nothing" a core reliability contract — the point of D6 is that a broken poll must not look like "nothing to review." Yet the ACs cover only success and dispatch-failure paths: AC17 tests a failed *dispatch*, AC6 tests the *empty* result (exit zero). No AC exercises a poll that errors. Fix: add an AC where the GitHub poll fails (simulated auth/rate-limit/unreachable) and assert `watch --once` exits non-zero, prints an error naming the failure, and records no PR as handled.

2. **R13 (staged sessions surfaced in agent view) has no AC.** R13 is a functional requirement — the "developer finds a review waiting" story depends on it — but no acceptance criterion verifies a dispatched session actually appears in the Claude Code agent view. AC7 verifies the draft artifact and halted state, not the surfacing. Fix: add an AC asserting a staged session is discoverable in the existing agent view after a run.

3. **R5 `--detach (-d)` vs R13/D7 `--bg` registration is unreconciled.** R5 mandates dispatch "always with `--detach (-d)`," while R13 and D7 attribute agent-view auto-registration to a `--bg` worker. The PRD never establishes that a `-d` dispatch also carries the registration R13's surfacing depends on. If these are distinct flags, the surfacing loop is not guaranteed. Fix: state that the detached dispatch also registers in the agent view (or reconcile the flag names) so the surfacing contract is coherent.

## Suggested Improvements

1. **R16 (GitHub-only) has no AC:** acceptable as a scope boundary, but a one-line note that host-scope is enforced by the D3 qualifier choice would make the negative scope explicit rather than implicit.
2. **AC15 asserts only the negative (session-cannot-post):** consider an explicit positive AC that the trusted post action, run outside the session, successfully posts the approved draft, so R14's success path is directly verified rather than implied.

## Summary

The revision fully resolves the prior FAIL: R3 now has AC4, failure and handled-set-on-success semantics are covered by R11/R12/AC17, staged-draft discovery is added as R13/D7, R6 and R7's write and fail-closed properties gained AC7/AC10/AC11, and selection-ordering, empty-result, and determinism are covered by R10/AC8/AC6/AC18. The residual gaps are narrow AC-coverage holes — R12's poll-failure branch and R13 have no acceptance criteria — plus an unreconciled `--detach` vs `--bg` registration assumption the surfacing story rests on. Applying the same missing-AC bar the prior review used (which failed R3/R6/R7 for lacking ACs), these warrant a FAIL that a small set of added ACs and one clarification would clear.
