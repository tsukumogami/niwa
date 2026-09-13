# Design Summary: session-store-teardown

## Input Context (Phase 0)
**Source PRD:** docs/prds/PRD-session-store-teardown.md
**Problem (implementation framing):** worktree teardown resolves its store through a root-conflating walk and accepts only worktree ids; the snapshot writer rotates the root .niwa through fixed staging/prev paths with no ordering against other refreshes or niwa's own writers.

## Decisions (Phase 2-3)
1. Refresh ordering: short per-config-dir flock (new internal/configdir owns lock + layout + recovery), fetch unlocked in private staging, Mutate/Read entry points.
2. Destroy resolution: resolver in internal/cli; root refusal centralized in resolveInstanceRoot; IsSingleInstanceLayout requires a named instance.
3. Test seam: leaf internal/testhook named hook points; SIGKILL of a re-executed test binary for crash cases.
4. instance.json guards: atomic locked SaveState plus skip-after-failed-read.

All four confirmed by the coordinator (option A plus two root-state guards, with one blocking fix on watch's writers).

## Security Review (Phase 5)
**Outcome:** Option 2 (document considerations).
**Summary:** no privilege or dependency added and the test seam is unreachable at runtime; the gaps were in recovery and sweep handling of planted or name-colliding paths, lock-file symlinks, discovery-time ownership, and --force with a session id. All are written into Security Considerations.

## Phase 6 reviews
- Architecture: pass with two blocking fixes (single-instance rule must require a named instance; one owner for the swap's file names). Both applied.
- Structural format: pass with advisories. Applied.

## Current Status
**Phase:** 6 - Final review
**Last Updated:** 2026-09-13
