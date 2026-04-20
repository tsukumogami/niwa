# Design Summary: parallel-clones

## Input Context (Phase 0)

**Source:** Freeform topic
**Problem:** The clone and sync loop in `runPipeline()` (`apply.go` Step 3, lines 772-805)
is sequential. Each git clone blocks until complete before the next begins. Network I/O is
the bottleneck. For workspaces with 10+ repos, users wait 30-60 seconds when parallel cloning
could cut that to 8-15 seconds.
**Constraints:** No new dependencies, preserve TTY/non-TTY behavior, no CLI surface change,
consistent error semantics (fail-fast on clone, soft on sync failure).

## Decisions (Phase 1)

1. Display model during parallel clones — standard
2. Concurrency cap and control — standard
3. Error handling in the worker pool — standard

## Decision Results (Phase 2)

1. Summary spinner with progress counter — chosen over multi-line ANSI and silent mode
2. Fixed constant of 8 workers — chosen over configurable flag and unbounded concurrency
3. Fail-fast on clone errors — chosen over continue-on-error and partial success

## Cross-Validation (Phase 3)

No conflicts. Fail-fast (Decision 3) aligns with summary spinner (Decision 1): when context
is cancelled, the orchestrator stops updating the spinner and returns the error. Fixed cap
(Decision 2) is compatible with both display model and error handling choices.

## Security Review (Phase 5)

**Outcome:** Option 3 — N/A with justification
**Summary:** No new attack surfaces. Clone URLs are unchanged; parallelism only affects
execution scheduling, not what is cloned or from where.

## Current Status

**Phase:** 6 - Final Review
**Last Updated:** 2026-04-19
