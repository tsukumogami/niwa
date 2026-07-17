# /prd Scope: niwa-watch-pr-hardening

Upstream: docs/briefs/BRIEF-niwa-watch-pr-hardening.md (Accepted). The BRIEF is
the scope of record; this PRD translates its framing into numbered, testable
requirements at "what" altitude, deferring mechanism to DESIGN.

## Problem (from BRIEF)
The shipped `niwa watch --once` wedge is not trustworthy across repeated runs:
SHA-blind permanent handled-set never re-fires a genuine re-request (and
discards the reviewer session's accumulated context); no unblock-time freshness
re-validation; per-pass bound only, so live staged agents accumulate unbounded.

## The decided re-dispatch behavior (settled with dispatcher; see BRIEF)
Keyed on new activity (head SHA moved since last dispatch):
- no surviving session -> fresh (dismissed/crashed-reaped count as none)
- live AND idle session -> resume it against new activity, retain context
- live BUT busy/attached -> do not interrupt; defer to idle or next run
Invariants: coalesce-to-latest (no queue), never two live sessions per PR.
Resume mechanism deferred to DESIGN.

## Coverage checklist
- [x] Gap 1: SHA-aware state + resume/fresh decision matrix
- [x] Gap 2: unblock-time freshness re-validation + self-discard
- [x] Gap 3: total-staged cap across runs
- [x] Trigger-semantics declaration (level vs edge) on state contract
- [x] Non-functional: determinism, fail-closed/loud, migration, containment unchanged, multi-repo verify-not-rebuild

## Research leads
Grounded in read code: internal/watch/state.go (handled-set, StagedRecord),
select.go (DefaultPerRunBound), internal/cli/watch.go (stageReview, GetPullHead,
reapOpportunistically). No new research agents needed; the code is read and the
BRIEF settles the behavior.
