# Decision D1: UX scope pruning

## Question

The 5 UX research agents proposed 51 raw issues in total (10+10+10+8+13).
Which subset becomes part of this PR's scope vs. deferred to follow-on
PRs, given a ~12h autonomous budget targeting production-grade quality?

## Options considered

- **A (full):** include all 51 issues. Production-grade in breadth.
  Rejected: even at perfect efficiency, ~30 hours of focused
  implementation. Will not finish; partial work would land below the
  quality bar the user explicitly set.
- **B (zero):** ship only the original 8 design issues. UX research
  becomes a follow-on. Rejected: user explicitly scoped UX as
  "full surface" and said "spend a lot of time on UX" — a 0-UX outcome
  contradicts the instruction.
- **C (load-bearing only):** add 5 new issues that close UX gaps the
  mesh redesign creates and that won't ship correctly without them.
  Defer the rest to a follow-on UX-polish PR. **Chosen.**

## Chosen option (C)

Add 5 new issues to the PLAN, plus fold-ins to existing issue ACs:

- **Issue 9** — `feat(mcp): structured error wire format with body`. The
  current `errResultCode(code, msg)` returns a single text content; the
  design's `MISSING_SKILLS` requires a `{missing, available}` JSON body.
  Without this, Issue 6's contract is inexpressible. Blocks Issue 6.
  Source: MCP responses report E1 finding (top-1 finding flagged).
- **Issue 10** — `fix(cli): silence cobra duplicate-error printing and
  align stdout/stderr discipline`. Errors print 2-3 times today (cobra +
  root.go:53-58 + per-handler stderr writes). Cross-cutting bug. Source:
  CLI 5.1, Error messages, First-run 5.4 — flagged by 3 of 5 agents.
- **Issue 11** — `feat(cli): render structured MCP error codes with
  recovery hints`. Helper that parses `error_code:` prefix + body, prints
  message + hint per code. Required for the 3 new design error codes
  (DAEMON_SPAWN_TIMEOUT, MISSING_SKILLS, SOURCE_BODY_LOST) to surface
  usefully on the CLI. Source: CLI 5.5, Error messages 4.1-4.3.
- **Issue 12** — `feat(cli): add niwa task redelegate as CLI mirror`. The
  design names `niwa_redelegate` as the canonical recovery primitive but
  has no CLI mirror — operators must launch Claude to recover. Source:
  CLI 5.4 (load-bearing UX gap).
- **Issue 13** — `feat(cli): add --json + daemon column to niwa session
  list`. The design's `daemon` sub-object on niwa_list_sessions has
  nowhere to land in the CLI today (no JSON, no daemon column). Combines
  CLI 5.2 (--json) and 5.3 (daemon column) into one focused feature.
  Source: CLI report.

## Fold-ins (no new issue numbers, ACs added to existing issues)

- **Issue 2 ACs**: incorporate Error messages report's drafted
  `DAEMON_SPAWN_TIMEOUT` message text following the apply.go:327
  prescriptive template precedent.
- **Issue 6 ACs**: depend on Issue 9 (structured wire format); use the
  drafted `MISSING_SKILLS` message text.
- **Issue 7 ACs**: use the drafted `SOURCE_BODY_LOST` message text.
- **Issue 8 ACs**: incorporate Skill/docs report findings — trim
  frontmatter description (P6.1), restructure Common Patterns
  (P6 / Issue 1.6), align headings with peer skill voice (Issue 1.3),
  fix em-dash inconsistency (Issue 2.1), update the contradictory
  `niwa session list` deprecation note (Issue 2.2).

## Deferred (recorded for a follow-on UX polish PR)

The remaining ~38 raw findings cluster into:
- First-run flow improvements (announcements at apply time, init clean-up
  on failure, README/docs alignment) — 7 issues. Proposed follow-on
  milestone "first-run polish".
- CLI list/show empty-state and short-prefix support — 3 issues. Follow-on
  "CLI ergonomics".
- MCP convention reconciliation (list envelope, lock-timeout code, too_late
  hints) — 6 issues. Follow-on "MCP convention pass".
- Skill / sessions guide structural reorganization beyond the Phase 6
  gaps already captured — 4 issues. Follow-on "docs polish".
- Confirmation prompts on session destroy — 1 issue. Follow-on safety pass.

These will be filed as GitHub issues at PR finalization time, labeled for
the appropriate follow-on milestone. They are not in scope for this PR.

## Status

Confirmed. Total in-scope issues for this PR: 13 (8 original runtime/docs
+ 5 UX). Dependency edges:
- Issue 6 now depends on Issues 4 + 9.
- Issue 11 depends on Issue 9.
- Issue 12 depends on Issue 7.
- Issue 13 depends on Issue 3.
