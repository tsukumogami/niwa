# Exploration Decisions: enterworktree-hook-bypass (niwa#221)

## Round 1

- **#221 is retargeted to the defects that reproduce.** Direct reproduction on
  both 2.1.215 and 2.1.220 showed the per-repo `WorktreeCreate` hook fires from
  inside a git repo, so the harness never regressed. The non-monotonic
  harness-detection work the issue originally asked for is dropped as
  unnecessary — it guards a defect that does not exist.
- **The merged spike is not patched, it is rewritten or deleted.** Its central
  finding is false, and a correction note on top of a false root cause still
  reads as a false root cause. The corrected reproduction evidence needs a home
  that a future reader can trust end to end.
- **The delegation model is not reopened.** The spike's layered recommendation
  asked whether transparent delegation is still achievable at all. It is — it
  works today — so that question is moot rather than deferred.
- **Scope covers four items:** hook-command durability (F3), partial-failure
  rollback (F4), the spike and design corrections, and the `ApplyToWorktree`
  worktree-delegation plumbing gap (F6).
- **Next artifact: the full `/shirabe:scope` chain**, terminating in a PLAN.
  Two open technical choices carry into it — how the hook command resolves niwa
  durably, and whether a failed `from-hook` rolls back or retains a
  failure-marked session.

## Decision: Crystallize
