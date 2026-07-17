# /brief Discover: niwa-watch-pr-hardening

## Grounding sources

- Dispatch brief (ephemeral, workspace-level): settles the re-dispatch
  semantics for gap #1 -- level-triggered, coalesce-to-current,
  dispatch iff (head SHA changed since last dispatch) AND (no live
  staged session for this PR), never two live sessions per PR, no
  queue. Also: the state contract must let a source declare trigger
  semantics (level vs edge) so an edge-triggered source (message
  streams) is not locked into PR coalescing.
- Upstream roadmap feature entry (private repo -- not citable in the
  public BRIEF): three hardening gaps -- dedup/cursor state with
  re-request re-fire, unblock-time freshness re-validation,
  max-concurrent-staged cap. "Scale to all workspace repos" listed
  there is already shipped (verified in code: WorkspaceScope matches
  explicit owner/repo sets and whole orgs from all cfg.Sources) --
  verify by dogfooding, do not rebuild.
- Merged ED1 code (niwa main): internal/watch/state.go (flat
  handled-set at .niwa/watch-handled keyed owner/repo#number --
  permanent, SHA-blind; StagedRecord{Handle,Owner,Repo,Number,URL,
  DraftPath} without a liveness reference), select.go
  (DefaultPerRunBound=3, per-pass only), internal/cli/watch.go
  (stageReview fetches head SHA via GetPullHead at stage time;
  reapOpportunistically reclaims only unmapped orphans).
- ED1 docs: docs/briefs/BRIEF-niwa-watch-once-pr-review.md (Done),
  docs/prds/PRD-niwa-watch-once-pr-review.md.

## Problem/outcome pair

Problem: the shipped watch --once wedge is safe but not trustworthy
across repeated runs. (1) The handled-set is permanent and SHA-blind:
a PR that gets new commits and a fresh review request never re-fires.
(2) Nothing re-validates a staged review at unblock time: the PR may
be merged, closed, no longer requesting the developer, or force-pushed,
and the stale draft is presented anyway. (3) Only a per-pass bound
exists: repeated runs accumulate live staged agents without limit
(the reaper only reclaims unmapped orphans).

Outcome: a developer can run watch repeatedly and trust the result: a
genuine re-request after new commits re-fires exactly once; a staged
review that went stale discards itself instead of presenting a dead
draft; total live staged agents never exceed a fixed cap regardless of
burst shape.

## Journey sketch (distinct entry points)

1. Re-request after new commits, old session dismissed -> re-fire.
2. New commits while old session still live -> suppressed (never two
   live sessions); dismissal frees re-dispatch.
3. Unblock a staged review whose PR merged/closed/was force-pushed ->
   self-discard, no stale draft presented.
4. Burst of requests across repeated runs -> total-staged cap holds;
   dismissing/completing staged reviews frees capacity.

## Phase

1
