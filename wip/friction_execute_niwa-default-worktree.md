# Friction log: /execute (shirabe) — niwa-default-worktree

Observations on the new `/execute` skill, to report to shirabe later. Append as I go.

## Context
- Running `/execute docs/plans/PLAN-niwa-default-worktree.md` in --auto.
- Constraint: must land on the EXISTING branch `docs/niwa-default-worktree` as part
  of PR #167 (not a fresh branch/PR).

## Observations

### F1 — single-pr path hardwires a new `impl/<slug>` branch + new draft PR
`/execute`'s single-pr `orchestrator_setup` state creates the shared `impl/<slug>`
branch and a new draft PR (SKILL: "create (or reuse, via `status: override`) the
shared `impl/<slug>` branch and a draft PR"). There is no documented option to
target an EXISTING branch / EXISTING PR. The author required landing on the existing
`docs/niwa-default-worktree` branch as part of PR #167 (the same PR that already
carries the brief/prd/design/plan/spike chain). Because the koto orchestrator can't
be pointed at that branch/PR, it could not be used as-is.
- Impact: for the common "I scoped + planned on a branch with an open PR, now
  implement into that same PR" flow, `/execute` forces a second branch/PR and a
  later reconciliation. A `--branch`/`--pr` (or "reuse current branch if it has an
  open PR") option would close this.
- Workaround used: drove the plan's issues directly on the current branch via
  per-issue coder delegation (orchestrate-and-delegate shape, minus koto's
  branch/PR creation), so all code lands in PR #167.

### F2 — `/execute` lives in 0.12.1-dev while the rest of the chain ran on 0.11.0
The other shirabe skills resolved under `…/shirabe/0.11.0/…`; `/execute` resolved
under `…/shirabe/0.12.1-dev/…`. Mixed plugin versions in one chain; worth confirming
that's intended (a dev build shipped alongside a stable one).

### F3 — no obvious "review-then-merge" pause; finalization cascade is merge-coupled
`/execute`'s done-signal is the home PR MERGING (full-run). For a flow where the
author wants to review the implemented PR before merge, the skill's
DRAFT-before-READY cascade (delete PLAN, transition BRIEF/PRD→Done, DESIGN→Current)
is coupled to readiness/merge. Driving manually, we deliberately did NOT run the
cascade so the chain docs stay reviewable in the PR; a documented "implement, stop
before finalization for review" mode would help.

## Execution findings (niwa code, not /execute friction — already fixed in-PR)

### R6 gap caught by the functional test (fixed)
A fresh niwa worktree read as DIRTY because the niwa-authored
`.claude/rules/worktree-imports.md` scaffolding was not in gitexclude coverage
(`niwaExcludePatterns = ["*.local*", ".niwa/"]`). Combined with the design's
non-force `from-hook` remove (dirty→log-and-retain), this would have RETAINED every
delegated worktree on clean agent teardown — orphan accumulation, violating PRD R6.
This is a pre-existing latent niwa issue (regular `niwa worktree destroy` non-force
also refuses a fresh worktree) that automatic teardown would have exposed. Fixed by
excluding the one uncovered path at the worktree scaffolding call site (commit
885b2ce); verified a fresh worktree now reads clean and from-hook tears it down.
