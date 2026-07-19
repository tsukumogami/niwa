---
decision: 2
title: Capture disambiguation strategy after resume
---

## Question

When retask resumes a session, Claude mints a new session id while the
superseded job entry still exists at the same instance cwd, so a naive
post-resume `captureSessionID` re-run is ambiguous (the exact failure
documented as #211 in `continueReview`, `internal/cli/watch.go:583-599`).
How does retask deterministically recover the surviving session's ids, and
is the superseded job entry removed BEFORE the resume, AFTER via
disambiguated capture, or both?

## Options considered

**A. Remove-first.** `claude rm <oldShortID>` the superseded session before
relaunching, so capture never sees two entries.

Rejected. Two independent facts in the existing artifacts rule this out,
not just a style preference:

- R4 (`docs/prds/PRD-dispatch-handle-retask.md:98-102`) defines a stopped
  worker as retaskable only "with its job entry intact." `claude rm`
  removes the jobs-dir entry (`docs/spikes/SPIKE-niwa-session-keep-alive.md:257-260`:
  "after `claude rm` on both throwaway sessions, their
  `~/.claude/jobs/<id>` entries were gone"). Removing the entry before the
  resume attempt is confirmed to succeed takes the worker out of the state
  R4 defines as retaskable — if the subsequent resume then fails for any
  reason, there is no longer a "stopped worker with job entry intact" to
  fall back to, even though the transcript itself survives independently
  under `~/.claude/projects/...` (`docs/prds/PRD-dispatch-handle-retask.md:210-211`,
  `internal/cli/sessionattach/preflight.go:38-40`).
- This directly violates N1 (`PRD:130-133`): "leaves the prior session,
  job entry, and mapping intact and usable" on any failure before rebind.
  `sessionLive` (`internal/cli/job_state.go:93-112`) keys purely on
  job-entry presence, so once `rm` runs, `sessionLive` on the old id goes
  false regardless of whether the resume that was supposed to replace it
  ever lands.
- It's also already foreclosed by the PRD itself: **D5**
  (`PRD:252-257`) already decided "superseded session removed immediately
  ... after a successful rebind," not before. Re-litigating remove-first
  would contradict a closed decision, not extend it.

**B. Capture-newest.** Keep both entries live through the relaunch, extend
capture with a newest-registration-wins heuristic, validate, then `rm` the
superseded entry after rebind.

Rejected as the disambiguation mechanism specifically (the after-rebind
`rm` timing it proposes is correct and is kept — see Recommendation). The
research agent's fact-check found **no timestamp field to key "newest"
on**: `internal/cli/job_state.go`'s `jobState` struct decodes only
`SessionID`, `Template`, `Cwd`, `State`, `Tempo`, `InFlight.Tasks`,
`Block`, `Needs` — no `createdAt`, no `firstTerminalAt`. The latter is
referenced only in a comment (`job_state.go:78`, echoed in
`docs/guides/ephemeral-session-instances.md:206`) as a field niwa
*deliberately does not decode*, because liveness is designed to be
timestamp-free. Building "newest wins" would mean adding a new field to
the decode surface and trusting the platform's registration-order
semantics for it — a heuristic, and a new piece of platform surface area,
where an exact key is already available for free (see C). It also cuts
against the codebase's established philosophy: `captureSessionID`'s own
doc comment (`dispatch_capture.go:12-20`) is explicit that cwd-correlation
works *because* it's an exact key (unique `instanceDir`), "stronger than
the probabilistic short id" — "newest" reintroduces exactly the kind of
probabilistic tiebreak the original design avoided.

**C. Exclude-known.** Capture with an exclusion list containing the
already-known superseded session id, rather than a newest heuristic.

Selected as the disambiguation mechanism. Retask already knows the old
session id before it ever calls resume — it had to resolve the target to
a `SessionMapping` (`internal/workspace/session_map.go:49-73`) to get the
id to pass to `--resume` in the first place. Passing that same id as an
exclusion set to an extended `matchSessionByCwd`/`captureSessionID`
(skip any `state.json` whose `js.SessionID` equals a known-superseded id
before counting matches/ambiguity) turns "two entries share this cwd"
back into the same single-match exact-key case `captureSessionID` was
originally designed for (`dispatch_capture.go:15`, D3). No new field, no
timestamp trust, no heuristic — the seam change is additive (an optional
exclude parameter) and the existing zero-match/timeout and
invalid-sessionId-keeps-polling behavior (`dispatch_capture.go:60-67,
107-113`) is unchanged.

**Ordering hybrids.** The two axes (remove-first vs. remove-after;
newest vs. exclude-known) are independent, so the real space is 2×2, not
a spectrum. Remove-first was rejected outright above on R4/N1/D5 grounds
regardless of which disambiguation mechanism it's paired with, so the
only hybrid worth naming is the one under Recommendation: **remove-after
(D5) + exclude-known (C)**.

## Recommendation

Remove-after + exclude-known, concretely:

1. Resolve the target to its current `SessionMapping`; this yields the
   superseded session's full UUID (`mapping.SessionID`) before any resume
   call is made.
2. Resolve that session's *current* jobs-dir short id live, by scanning
   `state.json` entries for `js.SessionID == mapping.SessionID` (the
   inverse of `matchSessionByCwd`'s cwd-keyed scan, keyed on session id
   instead). This avoids needing a new `ShortID` field on
   `SessionMapping`, which the schema does not carry today.
3. Stop the old session (`stopSessionFunc`-style seam, mirroring
   `continueReview` step 4, `watch.go:559-566`) if it's live. The job
   entry is untouched by `stop` — only its process dies.
4. Launch resume (`dispatchLaunch` + `--resume <mapping.SessionID>`),
   minting a new session id, mirroring `continueReview` step 5.
5. Capture with the old id excluded: extend
   `captureSessionID`/`matchSessionByCwd` with an `exclude
   map[string]bool` (or a single string) of known-superseded session ids.
   Ambiguity is now ">1 match after excluding known ids," not ">1 match"
   — the common case collapses back to exactly one match (the new
   session), the same shape a fresh dispatch capture sees.
6. Re-validate the captured id (`ValidSessionID`, `IsSafeHandle`) before
   it becomes a mapping write or a future CLI argument — mirrors
   `continueReview` step 0 (`watch.go:511-524`).
7. Rebind the mapping atomically: `WriteSessionMapping` the new struct
   (reusing the existing temp-then-rename writer,
   `session_map.go:96-122`), then delete the old mapping file
   (`DeleteSessionMapping`, idempotent, `:190-199`) — the store is keyed
   by session id, not instance, so the old file does not disappear on its
   own.
8. Only *after* the rebind succeeds, `claude rm <oldShortID>` the
   superseded job entry (D5's already-decided ordering). `rm` only
   touches the jobs-dir registration; the transcript survives
   independently under `~/.claude/projects/...`, so this is safe and
   stays within R8's "documented surfaces only" constraint (no
   `state.json` edits).

**Tie/invalid fail-closed shape**: after excluding the known-old id(s),
zero matches keeps polling until timeout (existing behavior, unchanged).
More than one match is ambiguity → fail closed: return an error, do
**not** rebind the mapping, do **not** `rm` anything. The old session is
stopped (step 3 already ran) but its job entry and mapping remain present
— `sessionLive` still reports it live-and-resumable, satisfying N1's
"prior session, job entry, and mapping intact and usable" for every
failure path from step 5 onward. This mirrors the accepted precedent in
`continueReview` itself: a failure after its own stop-before-resume step
degrades to "skip this pass, re-decide next pass" rather than being
treated as a full N1 violation — the entry enduring past a stop is what
makes that degrade safe.

**Unit-test seam changes needed**: (a) `matchSessionByCwd` and
`captureSessionID` gain an exclude-set parameter (or a sibling function
wrapping them) — both are already pure over injected `jobsDir`/clock/poll
and fully offline-testable via fakes, so this is additive, not a rewrite.
(b) A new small helper for "resolve current short id for a known session
id" (step 2) needs the same fake-jobs-dir test treatment as
`matchSessionByCwd`. (c) The `claude rm` step needs a seam var
(`rmSessionFunc`, mirroring `stopSessionFunc`, `watch.go:463`) so tests
drive the after-rebind cleanup without a real `claude` binary.

## Assumptions

1. Retask resolves the target to a `SessionMapping` (and therefore knows
   the superseded session's full UUID) strictly before calling resume —
   this is what makes "exclude-known" possible without any new schema
   field; if a future revision resolves the target some other way, this
   recommendation would need re-checking.
2. `SessionMapping` does not carry a `ShortID` field today
   (`session_map.go:49-73`), so the superseded session's short id must be
   resolved live from jobs-dir at retask time (step 2) rather than read
   off the mapping; this is a small new lookup, not a schema change.
3. `claude rm` removes only the jobs-dir job entry and does not affect
   the transcript, based on `docs/spikes/SPIKE-niwa-session-keep-alive.md:257-260`
   and the PRD's own "transcript is preserved" language
   (`PRD-dispatch-handle-retask.md:210-211`); this recommendation treats
   that as established fact rather than something this decision needs to
   re-verify, since it is exactly what D5 already relied on.
4. Concurrency safety (N2 — two concurrent retasks against the same
   target) is **not** solved by exclude-known capture on its own: two
   concurrent retasks reading the same starting mapping would both
   legitimately exclude the same old id and could each mint a competing
   new session at the same cwd, which would then be ambiguous against
   *each other*. This decision assumes N2 is closed by a separate
   mechanism (e.g., a lock or compare-and-swap on the mapping write) that
   this report does not design; exclude-known composes with whatever that
   mechanism is but does not substitute for it.
5. `firstTerminalAt` (or any other Claude Code state.json timestamp) is
   assumed to genuinely not be surfaced through niwa's current
   `jobState` decode, based on the research agent reading
   `internal/cli/job_state.go` in full; it was not independently
   re-verified against a live `claude` binary's actual `state.json`
   shape in this session.

## Confidence

High on rejecting remove-first (A) and newest-registration (B) in favor
of remove-after + exclude-known (C) — each rejection is backed by a
specific requirement (R4, N1), an already-closed PRD decision (D5), or a
verified absence of the field the rejected option depends on
(`firstTerminalAt`), not just a stylistic preference. Medium on the
precise mechanics of step 2 (resolving the old short id live rather than
storing it) and on the concurrency interaction noted in Assumption 4 —
both are consistent with the evidence gathered but weren't cross-checked
against a live Claude Code daemon in this session.
