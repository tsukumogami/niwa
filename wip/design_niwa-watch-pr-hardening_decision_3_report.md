# Decision 3 (standard): Freshness re-validation hook point and mechanism

## Question
Where/when does the deterministic open/still-requesting/not-force-pushed check run,
and how does a stale staged review self-discard — keeping it deterministic (R14)?

## Evidence
- The watcher already polls open + still-requesting PRs (`SearchReviewRequestedPRs`,
  watch.go:150) and the head SHA (`GetPullHead`, watch.go:186). Freshness reuses data
  the pass already fetches — plus an ancestry test (dispatched SHA still an ancestor
  of head) to distinguish force-push/rebase from ordinary advancement (PRD R9).
- The review session drafts and waits; the human may unblock it between watch passes,
  so a watcher-only check can miss the exact unblock moment.
- R14 forbids a model judgment; the discard must be code-driven.

## Decision
One deterministic freshness predicate, evaluated at two hook points:

- **Predicate** `freshness(record, poll) -> ok | reason`: PR open? developer still in
  the requested reviewers? dispatched head SHA still an ancestor of the current head
  (not force-pushed/rebased away)? Pure over data the poll already returns plus one
  ancestry check. Deterministic; no model.
- **Hook A — watcher pass (primary).** On each `runWatchOnce`, over live staged
  records, evaluate the predicate; a failing record is discarded: prune the record,
  stop/reap its instance, so the stale draft never reaches the human. This also
  drives the staged-record GC (Decision 4) and frees cap capacity.
- **Hook B — session pre-flight (unblock-time complement).** The staged review
  session runs the same deterministic check (a niwa subcommand invoked as its first
  step / an instance hook) before presenting the draft, so a review unblocked between
  passes self-discards with the failing reason. The check is code; the agent only
  invokes it and honors its exit — the discard is not a model judgment.

## Alternatives
- Session-side only: rejected — a session the human never re-engages never re-checks;
  the watcher-pass prune is the reliable sweep.
- Watcher-pass only: rejected — misses the true unblock moment between passes; PRD
  says "at unblock time". Hook B closes that.
- Let the agent judge staleness from the cloned PR: rejected — violates R14 (model
  judgment); the deterministic predicate is the contract.
