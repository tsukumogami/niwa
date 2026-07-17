# Completeness Review

## Verdict: PASS

The PRD is buildable against a forthcoming DESIGN without behavioral guessing; the one clear gap is a requirement (R16) with no verifying acceptance criterion, plus two behavioral interactions worth making explicit.

## Issues Found

1. **R16 has no acceptance criterion (containment/scope verification is unverified).**
   R16 requires the change to preserve `WorkspaceScope` and the containment model
   (sandbox, PreToolUse hooks, post-guard) and states "it verifies these." Every
   other requirement R1-R15 and R17 maps to at least one criterion in the
   Acceptance Criteria list; R16 is the only one with none. Because R16 is a
   verify-not-rebuild requirement (not a deferred mechanism), the verification is
   itself the deliverable and needs a gate. This is security-adjacent, so leaving
   it uncovered is the weakest point in an otherwise complete set.
   *Fix:* Add a criterion, e.g. "A test asserts the staged dispatch still runs
   under `WorkspaceScope` and that the sandbox/PreToolUse-hook/post-guard wiring
   is present and exercised (R16)."

2. **Resume-vs-cap interaction is unspecified.** R11 caps "concurrently live
   staged review *agents*"; R4 resumes an existing live-idle session against new
   activity. Resuming continues an already-counted session, so it should be
   allowed even when the cap is reached, but the PRD never says so. An implementer
   must currently guess whether a resume is gated by the cap.
   *Fix:* One sentence under R11/R12 (or R4) stating resume/continue of an
   existing live session does not consume new cap capacity and is not blocked at
   the cap.

3. **Legacy-migration behavior on new activity is ambiguous.** R17 requires the
   migration to preserve suppression of already-handled PRs, and its AC tests that
   shipped-format PRs "continue to suppress." But R2 keys the re-dispatch decision
   on head-SHA difference, and a migrated legacy entry carries no recorded SHA.
   The PRD does not state what a legacy PR does on the *next* re-request: re-fire
   (treated as SHA-unknown -> new activity -> stage fresh) or stay permanently
   suppressed (the old SHA-blind behavior, which contradicts the feature's goal
   for those PRs). The file format is design-owned, but this is a WHAT-level
   behavioral outcome.
   *Fix:* State the intended one-time behavior for legacy entries, e.g. "a
   migrated legacy entry adopts the first observed head SHA as its
   last-dispatched SHA (no re-fire on that pass); subsequent SHA advances re-fire
   normally."

## Suggested Improvements

1. **Define "oldest" for backfill (R12).** "Backfill oldest-first" leaves the
   ordering key implicit (first-seen time, review-request time, PR number).
   Naming the key removes a small implementer decision without descending into
   mechanism.

2. **User scenario for the base fresh-dispatch / transient-failure path.** The
   four use-cases cover resume, don't-interrupt, stale-discard, and burst, but the
   plain "no surviving session -> stage fresh" and the fail-closed transient
   path (R3, R15) appear only in requirements and AC. A one-line scenario for the
   normal fresh dispatch would round out the narrative; optional.

## Summary

The PRD holds a clean "what" altitude: mechanism (resume mechanism, state file
format, cap default, idle-vs-busy detection, freshness hook point) is correctly
deferred to DESIGN and recorded under Decisions and Trade-offs, so no deferred
item is miscounted as a gap. Requirements trace cleanly to acceptance criteria
except R16, whose containment/scope verification has no gate; two behavioral
interactions (resume vs cap, legacy-migration re-fire) should be stated
explicitly. Out of Scope is thorough and prevents scope creep well. These are
additive fixes, not reworks, so the verdict is PASS.
