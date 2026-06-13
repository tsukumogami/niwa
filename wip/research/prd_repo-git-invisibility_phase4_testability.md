# Testability Review

## Verdict: PASS

The acceptance criteria are concrete enough that a test plan can be written from them alone -- nearly every AC names an executable command (`niwa apply`, `niwa session create`) and an objective, machine-checkable assertion (`git status --porcelain` is empty), with no reliance on subjective judgment.

## Untestable Criteria

None are strictly untestable. Two are weak and should be tightened:

1. **AC7 ("The functional test fails when a niwa-written file is added that escapes coverage -- verified by construction")**: This is a meta-claim about the test's design, not an independently runnable assertion. "Verified by construction" means "trust that the assertion is `git status` empty, not an allowlist" -- a reviewer can confirm it by reading the test, but it isn't exercised at runtime, so a regression in the test harness (e.g. the test stops asserting, or asserts the wrong tree) would not be caught. -> Make it executable: add a negative test that deliberately writes an uncovered file into the fixture tree and asserts the functional test reports a non-empty `git status`. That converts "by construction" into a runtime guarantee.

2. **AC3 ("No file tracked by the managed repository ... is changed by niwa to achieve invisibility")**: Testable but underspecified on *how* to verify. The natural method (run niwa, then `git status --porcelain` shows no modified tracked file, and the committed `.gitignore` is byte-identical before/after) is implied but not stated. -> Specify the verification: snapshot tracked-file hashes (or `.gitignore` content) before the run and assert equality after, in addition to the empty-porcelain check. Without this, AC3 and AC1 collapse into the same test and the "tracked file unchanged" intent (distinct from "untracked file absent") goes unverified.

## Missing Test Coverage

1. **R3 re-sync path has no acceptance criterion.** R3 explicitly covers "after any re-sync of an existing worktree," and the Decisions section calls re-sync "covered transitively because it uses the same materialization path." But AC2 only exercises *initial* `niwa session create`. "Covered transitively" is a design assertion, not a verified one. -> Add an AC: after creating a worktree and re-running the sync/apply against the existing worktree, `git status --porcelain` is still empty.

2. **R2's "records coverage in a not-committed location" is only verified indirectly.** AC1 and AC3 prove the *effect* (clean status, no tracked change) but no AC asserts that coverage actually landed in a non-committed location and is *doing* the ignoring (vs. the tree being clean for some other reason). A test where niwa writes no files at all would pass AC1. -> Add an AC asserting that a niwa-authored file genuinely exists in the working tree (so the clean status is meaningful) and that removing/clearing the recorded coverage makes it reappear in `git status`.

3. **Error and edge conditions are absent.** All seven ACs are happy-path. No AC covers: the coverage location being read-only / unwritable; the managed repo being in a detached or dirty state when niwa runs; a worktree whose parent repo already has the `*.local*` pattern committed (does niwa still stay clean and idempotent rather than double-record?); or concurrent/interrupted apply runs. R6 (idempotency) is the only robustness AC and it covers only the clean repeat case. -> At minimum add an AC for the "coverage location already has unrelated user content" failure-adjacent case beyond AC5 (e.g. malformed existing content) and one for the repo-already-has-pattern case to confirm no regression or duplication.

4. **AC5 ("pre-existing user content preserved") does not bound *how*.** It says content is "preserved" but not whether ordering, comments, or trailing newlines are preserved -- R5 says "without discarding or reordering," which is stronger than AC5's wording. -> Align AC5 with R5: assert pre-existing lines retain their relative order and that only niwa's entries are appended.

## Summary

The PRD passes: a competent engineer could write a runnable test plan from the acceptance criteria alone, because each AC pairs a concrete niwa command with an objective `git status --porcelain` assertion and avoids subjective or external-dependency checks. The gaps are coverage, not testability -- the re-sync path (R3) has no AC despite being an explicit requirement, R2's "coverage recorded out-of-tree" is only proven by its effect rather than directly, AC7's "verified by construction" should be made a runtime negative test, and every AC is happy-path with no error/edge conditions (unwritable coverage location, dirty repo, repo that already has the pattern). Tightening AC3/AC5 wording to match R4/R5 and adding the re-sync and negative-test ACs would close the gaps.
