# Verdict: PASS

## Re-review (revision 2)

All three FAIL-driving changes verified in the revised Acceptance Criteria
section:

- **N2 now covered** by a new (unit) criterion: concurrent retask-vs-retask
  asserts exactly one succeeds, the loser fails closed with a concurrency error,
  and the surviving mapping matches the winner; retask-vs-reap drives both
  interleavings through the liveness and store seams and asserts the
  reaped-instance-with-live-session state is unreachable. Binary, observable, and
  automatable offline — seam injection makes the race deterministic instead of
  timing-dependent. The prior coverage gap is closed.
- **R6 now explicitly covered** in the three-consecutive-retasks criterion: the
  caller must use only the dispatch-time instance name and never the rotated
  session id. No longer merely implicit.
- **Verification-level labels present on all eleven criteria** —
  (unit)/(integration)/(live gate) — with the levels defined at the top of the
  section. The offline-unit vs live-daemon vs live-gate split is now unambiguous.

No contradictions introduced by the revision. Residual minor notes (naming the
observation surface for the context-continuity clauses and the byte-identity
clause) are non-blocking and were never FAIL drivers.

## Findings (original review, retained for history)

### Acceptance criteria — per-criterion assessment

- **AC1 (live-idle retask, list --json shows one session, ids match mapping).**
  Binary and observable: exit success, `niwa list --json` session count == 1,
  session ids == mapping-on-disk. Requires a live Claude Code daemon (dispatched
  worker + resume) but is automatable in an integration harness. The "resumes it
  with full prior context" clause needs an operationalized probe (worker recalls
  something from the prior transcript) to be a clean pass/fail — verifiable, but
  the PRD should say how continuity is observed rather than leaving it to the
  tester. Not marked as an integration/live check.

- **AC2 (stopped worker revive, same post-conditions).** Binary, observable,
  reuses AC1's post-conditions. Live-daemon dependent, unmarked. OK.

- **AC3 (superseded job entry gone, `claude agents --json` shows no second
  session for the cwd).** Fully observable (file/daemon state). Good, concrete.
  Live-daemon dependent, unmarked.

- **AC4 (three consecutive retasks, accumulated context forward).** Binary count
  + continuity probe. Same continuity-observation caveat as AC1. Also the only
  thing exercising R6 (handle stability) — implicitly, since the same target is
  reused three times. Live-daemon dependent, unmarked.

- **AC5 (busy worker refused, run not interrupted, mapping unchanged).**
  Observable (exit non-zero, error text names busy state, mapping bytes
  unchanged) but hard to automate deterministically: it requires holding a worker
  reliably mid-turn while a second process races it. This needs a controllable
  long-running turn, i.e. timing coordination, not a pure offline unit. Not
  flagged as an integration/timing-sensitive gate.

- **AC6 (unknown target / ambiguous capture / gone job entry → distinct error
  per cause, state unchanged).** Binary and observable per cause. Ambiguous-
  capture and gone-entry branches are largely unit-testable against a fabricated
  jobs dir; unknown-target is offline. Good, but the criterion mixes offline-
  unit-testable causes with a live-ish one without saying so.

- **AC7 (watch continuation drives 2nd/3rd re-request at newer heads, staged
  record holds valid ids each time).** Observable via the staged record's ids,
  but this is a heavy live integration: real PR heads, pushes, an undismissed
  review session. Automatable only end-to-end; not marked as such.

- **AC8 (sandboxed retask re-asserts egress-denial, verified by existing live
  gate on a disposable host).** Correctly and explicitly flagged as a live gate
  on a disposable host. This is the model the other live criteria should follow.

- **AC9 (prompt delivered without shell interpretation; `$(...)`, quotes,
  newlines byte-identical).** Strong, concrete, binary. Observing "arrives
  byte-identical" needs the delivery target to echo/record the received argument
  — the PRD should point at that observation surface, but the check itself is
  clean.

- **AC10 (unit tests: capture disambiguation, two entries sharing cwd; newest
  wins; tie/invalid fails closed).** Explicitly a unit test, fully offline,
  binary. Model criterion.

### Coverage gaps (requirement → AC)

- **N2 (concurrency safety) is uncovered.** No acceptance criterion exercises
  "two concurrent retasks against the same target cannot both succeed" or "retask
  concurrent with `niwa reap` never yields a reaped instance with a live
  session." This is a stated fail-closed safety guarantee, and it is a runtime
  behavioral property — not inherently verifiable by inspection (you can inspect
  for a lock, but not that the lock actually serializes the two winners and the
  loser fails closed). This is the primary FAIL driver.

- **R6 (handle stability) is only implicitly covered** by AC4 reusing the same
  target three times. There is no criterion asserting the instance-name / mapping
  identity is byte-stable across a retask while the underlying session id rotates.
  Given Known-Limitations calls out session-id rotation, an explicit "handle
  unchanged, rotated session id not required by caller" check is warranted.

- **R8, R9, N4 are inspection-only** (supported-surface use, single delivery
  seam, no new privileges). Acceptable as inherently verifiable by code
  inspection; no runtime AC needed. Noted for completeness.

- **N3 keep-alive reporting sub-clause** ("including keep-alive reporting") is not
  explicitly checked by any AC; AC1 checks session identity but not keep-alive
  fields. Minor.

### Feasibility / live-vs-offline labeling

The PRD distinguishes exactly two criteria as their true class: AC10 (unit) and
AC8 (live gate on disposable host). The remaining live-daemon-dependent criteria
(AC1–AC5, AC7, AC9) and the timing-sensitive AC5 are not marked as
integration/live checks. None are impossible to automate, so this is a labeling
weakness rather than an infeasibility, but per the review rubric the live vs
offline split should be explicit so a downstream implementer knows AC1–AC7 cannot
run in a pure offline unit suite.

### Contradictions

None found. AC2 (stopped-worker success) matches R4(b); AC5 (busy refused)
matches R4 and Out-of-Scope; no criterion contradicts another or a requirement.

## Required changes

1. **Add an acceptance criterion covering N2.** e.g. "Two `niwa retask` processes
   launched simultaneously against the same target: exactly one exits 0, the
   other exits non-zero with a contention/lock error; final state shows one live
   session and a consistent mapping. A retask racing `niwa reap` never leaves a
   reaped instance bound to a live session." Mark it as an integration/timing
   check.

2. **Add (or fold into AC4) an explicit R6 handle-stability check:** after a
   retask the instance name and mapping-file identity are unchanged and the caller
   operates on the worker using the original handle without the rotated session
   id.

3. **Label the live/integration criteria.** Tag AC1–AC5, AC7, AC9 as requiring a
   live Claude Code daemon (and AC5/AC7 as timing/end-to-end), the way AC8 and
   AC10 are already distinguished, so the offline-unit vs live split is
   unambiguous.

4. **Minor: name the observation surface** for the context-continuity clauses
   (AC1, AC4) and the byte-identity clause (AC9) so "full prior context" and
   "arrives byte-identical" are checked against a defined output rather than
   tester judgment.
