# PRD Auto-Mode Decisions: repo-git-invisibility

Mode: --auto (parent-orchestrated by /scope). Decisions follow recommended defaults.

## D1 (BRIEF Open Question 1 -- retroactive cleanup)
Decision: niwa guarantees invisibility going forward. On the next apply, the
recorded ignore coverage makes currently-untracked niwa files invisible. niwa
does NOT delete or rewrite files a user has already committed to the repo.
Alternative: actively scrub existing pollution -- rejected (risky; touches
user-tracked content; above the invisibility-guarantee altitude).

## D2 (BRIEF Open Question 2 -- coverage set the test must verify)
Decision: the automated check covers both `niwa apply` (managed repo) and
`niwa session/worktree create`. Re-sync is covered transitively (same
materialization path). Alternative: apply-only -- rejected because the
worktree `.niwa/` scaffold is the most concrete current leak.

## D3 (recording location constraint)
Decision: invisibility is recorded in a location NOT committed to the managed
repo, so the act of recording does not itself become a tracked change. The
exact mechanism is DESIGN territory; the PRD only constrains it to "not a
committed file."
