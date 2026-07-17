# Design Summary: niwa-watch-pr-hardening

## Input Context (Phase 0)
**Source PRD:** docs/prds/PRD-niwa-watch-pr-hardening.md (In Progress)
**Problem (implementation framing):** Extend the shipped watch dispatch state
(flat handled-set + staged records) to support SHA-aware re-dispatch that
continues a live-idle review session or stages fresh, unblock-time freshness
re-validation, a cross-run total-staged cap, and a per-source trigger-semantics
declaration — reusing niwa's existing state/config/reaper seams, without
weakening containment or determinism.

## Current Status
**Phase:** 6 (final review jury in flight)
**Last Updated:** 2026-07-17
**Execution mode:** --auto (under /scope orchestration, parent-delegated-approval)

## Decisions (all complete, cross-validated)
Grounded in two codebase investigations. Spine: split dispatch state by lifetime
(permanent SHA-aware handled-set + GC'd StagedRecord with session refs) + a new
staged-record GC that closes the monotonic-growth gap.
- D1: state representation + SHA + trigger-semantics + dual-format migration.
- D2 (critical): stop-and-resume by conv id + fresh checkout; idle/attached
  detection built from job state; sequenced LAST + deferrable (Defer fallback).
- D3: deterministic freshness predicate at watcher-pass + session pre-flight.
- D4: cap counts LIVE records (needs GC), config follows watch_sandbox, default 5.

## KEY FINDING to surface to dispatcher (final report)
Continuation (D2/Phase D) is the heavy novel piece: watch captures no session id,
niwa has no idle detection and no in-place prompt primitive — all must be BUILT.
Mitigation: Phase D last + independently deferrable; A-C (SHA state, freshness,
cap) ship real hardening without it. Dispatcher pre-named stop/resume-by-id, so
mechanism was delegated; flag the SCOPE/EFFORT (not relitigate the decision).

## Phase 6 jury (in flight)
design-architecture + design-security reviewers.
