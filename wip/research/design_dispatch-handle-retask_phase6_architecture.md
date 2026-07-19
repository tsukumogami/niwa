# Verdict: PASS

Note: an earlier revision of this file recorded a FAIL. That verdict was wrong —
it was produced by spot-checking Go source in the parent checkout
(`public/niwa`), which sits on a pre-#210 branch, instead of this worktree
(`.claude/worktrees/dispatch-handle-retask`, HEAD `d37bc89`, docs on top of the
#210 squash `45d4ce0`). Re-verified against the correct path below; the design's
code claims hold.

## Findings

### 1. Requirements coverage (PASS)

R1-R9 and N1-N4 are each addressed by a concrete architectural element and
nothing is silently dropped:
- R1 command surface → `retask.go` (target resolution over the mapping index +
  short-id prefix, single-argv prompt guard).
- R2 context continuity → `--resume` preserves transcript.
- R3 single-owner rebind → `RebindMapping` (write-new/delete-old) +
  superseded job-entry removal.
- R4 worker-state coverage → default-deny `classifyWorker`
  (retaskable/busy/blocked/gone).
- R5 chainable capture → exclude-known capture on the cwd matcher.
- R6 handle stability → mapping identity survives; `--json` reports both ids.
- R7 watch adoption → `continueReview` adopts the engine, keeping its pre-checks
  and sandbox re-assertion.
- R8 supported surfaces → resume/stop/rm + jobs-dir reads only.
- R9 replaceable seam → `retaskDeliver` package func var.
- N1 fail-closed, N2 concurrency (per-instance flock + reap trylock), N3
  observability (`--json`, error fields carry target/state/reason), N4 no new
  privileges.

### 2. Internal consistency (PASS)

The four decisions compose without contradiction: exclude-known capture collapses
the two-entries-one-cwd case to the single-match shape (Q2) and depends on the
superseded entry being removed last (D5); write-new-then-delete-old (Q3) pairs
coherently with the live-mapping-wins reap preference so every crash window
self-heals; the per-instance flock plus reap trylock closes the stop-window race;
caller-owned rebind (Q4) matches the two distinct stores (SessionMapping vs
StagedRecord). The data-flow diagram matches the component descriptions.

The sentinel-error taxonomy is now consistent: Q1 (line 130) states "Seven
sentinel errors" and names seven (target-unknown, session-gone, busy, blocked,
sandboxed, capture-ambiguous, conflict); Implementation step 3 (lines 305-306)
says "the seven sentinel-error taxonomy." (An earlier revision said "six" in step
3 — that mismatch is fixed.)

### 3. Strawman check (PASS)

Each rejected alternative has a real how-it-works and a rejection tied to a
driver: respawn-based revive (delivers no instruction; unverified id-preservation
under later resume — kept as a documented fallback); remove-first (violates
fail-closed, inverts D5); newest-registration capture (no timestamp signal —
`firstTerminalAt` null on the ranked entries, mtime unstable — and this directly
engages and supersedes the capture-newest idea noted in watch.go's own inline
comment); delete-old-then-write-new (strands a live session); instance-keyed
schema (breaks O(1) session-id lookup, forces migration); O_EXCL locks (staleness
protocol vs flock's kernel ownership); new `internal/retask` package (would export
a dozen internals for zero reuse); engine-owns-rebind (two stores, abstraction for
exactly two impls); interface type (indirection unjustified by one impl). No
strawmen.

### 4. Code-reality (PASS — all named seams verified in the worktree)

- `continueReview` — internal/cli/watch.go:507. Its body is exactly the sequence
  the design generalizes: re-validate ids → two-way liveness cross-check → fetch
  head → `ApplyReviewSettings` re-assertion → `stopSessionFunc` → `dispatchLaunch`
  with `--resume <SessionID>` → `captureReviewSession` re-capture. The comment at
  590-598 documents today's ambiguous re-capture and the once-per-session
  degradation (#211) verbatim to the design's Context #1 claim.
- `captureReviewSession` — watch.go:447. `stopSessionFunc` seam —
  watch.go:463 (`var stopSessionFunc = realStopSession`), alongside the
  established `dispatchCapture` (dispatch.go:90) and `dispatchLaunch`
  (dispatch_launcher.go:14) func vars, exactly as Q4 states.
- `BuildResumePrompt` — internal/watch/continuation.go:103 (fixed template, no
  PR-derived free text — matches the R-SEC prompt-injection posture).
- `StagedRecord` carries `SessionID` (state.go:321, full UUID, `ValidSessionID`
  discipline documented) and `ShortID` (:327), so Q2's "watch passes the staged
  record's known session id as the exclusion" is grounded.
- `matchSessionByCwd` returns `ambiguous` (dispatch_capture.go:78) — the seam the
  exclusion parameter extends.
- `acquireAttachLock` (sessionattach/attach.go:178) is a non-blocking
  `LOCK_EX|LOCK_NB` flock returning `errLockHeld` — the shape Q3's per-instance
  lock mirrors.
- reap join is last-write-wins today: `byPath[m.InstancePath] = m` in a loop
  (reap.go:116-119), so the "today is last-write-wins" claim and the proposed
  live-mapping-wins fix are both accurate.
- `SessionMapping` fields Label/Ephemeral/Origin/KeepAlive exist
  (workspace/session_map.go); `RebindMapping` is a proposed addition (fine).

### 5. Implementation Approach ordering (PASS)

Live-gate verification of the two extrapolated platform behaviors (stop-then-
resume into a dead-process entry; chained exclude-known capture) gates before any
dependent code, with a respawn fallback named. Store/lock groundwork with
crash-interleaving tests precedes the engine; the engine precedes the command;
watch adoption precedes docs. Each phase leaves the tree green.

## Recommended (non-blocking) correction

One claim is stale against this branch: the design (line 128-129 and R-SEC-2)
says "niwa's job-state decoder does not yet decode these fields." #210 (commit
45d4ce0, immediately below the design commits on this branch) already decodes
State, Tempo, InFlight.Tasks, Block, and Needs into `jobState`
(job_state.go:41-53), and already ships a fail-closed positive-idle classifier,
`watch.ClassifySessionActivity` (continuation.go), that returns DetachedIdle only
on `State=="done" && Tempo=="idle"` with nothing in flight and no human prompt —
precisely the default-deny discipline R-SEC-2 argues for. The design's decision is
therefore correct and, if anything, cheaper than described: `classifyWorker`
should reuse the existing decode and `ClassifySessionActivity` rather than "add
and validate new fields." Recommend updating Q1/R-SEC-2/Implementation step 1 to
say the fields and a fail-closed classifier already exist (reuse, not add), and
that the live-gate validates the decode against real retask-state samples. This
does not change the architecture and does not gate the PASS.
