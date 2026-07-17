---
upstream: docs/prds/PRD-niwa-watch-pr-hardening.md
status: Proposed
problem: |
  Placeholder — filled in Phase 6.
decision: |
  Placeholder — filled in Phase 6.
rationale: |
  Placeholder — filled in Phase 6.
---

# DESIGN: niwa watch PR-wedge hardening

## Status

Proposed

## Context and Problem Statement

The shipped `niwa watch --once` verb (ED1) is a stateless, deterministic
poll-and-dispatch pass. Its durable state is two flat structures under `.niwa/`:
a SHA-blind handled-set (`internal/watch/state.go`, `.niwa/watch-handled`,
lines keyed `owner/repo#number`) and one staged-review record per dispatched PR
(`.niwa/watch/<handle>.json`, `StagedRecord{Handle,Owner,Repo,Number,URL,
DraftPath}`). Selection is a pure function bounded per pass by
`DefaultPerRunBound` (`internal/watch/select.go`). The accepted PRD
(`PRD-niwa-watch-pr-hardening.md`) requires four behavioral changes on top of
this: SHA-aware re-dispatch that continues a live-idle review session or stages
fresh, unblock-time freshness re-validation, a total-staged concurrency cap
counted across runs, and a per-source trigger-semantics declaration on the
dispatch state.

The technical problem is that none of these can be built on the current state
model as-is. The handled-set has no room for a head SHA; the staged record has
no reference that would let the watcher find, probe the liveness of, judge the
idle/attached status of, or continue an already-dispatched session; nothing runs
at the moment a staged review is unblocked; and the only concurrency bound is a
per-pass constant, not a count of live staged agents. The design must decide how
the dispatch state is represented and migrated, what session-continuation and
liveness primitives niwa can offer (and which must be built), where freshness
re-validation hooks into the staged session's lifecycle, and how the cap is
counted and configured — all without weakening the shipped containment model or
the multi-repo scope, and keeping the watcher deterministic (no model in the
poll, decision, freshness check, or cap).

## Decision Drivers

- **Level-triggered coalescing is the PR wedge's semantics, but not universal.**
  The state contract must let a source declare level vs edge so a future
  edge-triggered source is not forced into PR coalescing (PRD R13).
- **Never two live sessions per PR; retain reviewer context when it survives.**
  The re-dispatch decision must continue a detached-and-idle session rather than
  discard its accumulated context, and must never interrupt a busy/attached one
  (PRD R4-R6).
- **Determinism.** No model participates in the poll, the re-dispatch decision,
  freshness re-validation, or the cap (PRD R14).
- **Fail-closed and fail-loud.** A transient failure must not suppress a review,
  must not look like "nothing to stage", and must not record a PR handled at a
  SHA it did not stage (PRD R15).
- **Preserve, don't rebuild.** The shipped containment (sandbox/hooks/post-guard)
  and multi-repo scope carry over unchanged (PRD R16).
- **Migrate the shipped flat state without a re-fire storm** and without losing
  suppression of already-handled PRs (PRD R17).
- **Follow existing niwa patterns.** Reuse the settings-merge seam, the global
  config pattern (`watch_sandbox`), the staged-record store, and the reaper
  rather than introducing parallel machinery.

## Considered Options

<!-- Phase 3 -->

## Decision Outcome

<!-- Phase 3/4 -->

## Solution Architecture

<!-- Phase 4 -->

## Implementation Approach

<!-- Phase 4 -->

## Security Considerations

<!-- Phase 5 -->

## Consequences

<!-- Phase 4/6 -->
