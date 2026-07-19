# Decision 1: Delivery mechanics per worker state

## Question

For a retask against (a) a live-idle worker and (b) a stopped worker (job
entry present), what exact relaunch sequence delivers the follow-up
instruction, and how are busy/attached/gone states detected and refused
(R4/N3)?

## Evidence gathered

- `internal/cli/watch.go:507-610` (`continueReview`): the only shipped
  analog. Sequence is re-validate ids → two-way liveness cross-check
  (`sessionLive` + `instanceHasLiveJob`) → re-assert containment → **stop**
  (`stopSessionFunc`, abort on failure) → **`dispatchLaunch` with
  `--resume <SessionID>` appended to `buildDispatchPassthrough`** → re-capture
  (currently ambiguous, tracked as #211).
- `internal/cli/job_state.go`: `sessionLive` is entry-presence only
  (`state`/`tempo`/TTL deliberately excluded, per its own doc comment). The
  `jobState` struct already decodes `State`, `Tempo`, `InFlight.Tasks`,
  `Block`, `Needs` — the doc comment on `Block`/`Needs` already names them
  "the 'attached' proxy."
- `internal/cli/dispatch_capture.go` (`captureSessionID`/`matchSessionByCwd`):
  ambiguity is a hard error today (two `state.json` claiming one cwd), never
  auto-resolved — R5's newest-registration disambiguation is genuinely new
  work, not reuse.
- Read-only survey of `~/.claude/jobs/*/state.json` (15 real entries, current
  host, redacted content not reproduced here beyond field shapes): confirmed
  fields `state` (`"done"|"running"|"working"|"blocked"`), `tempo`
  (`"idle"|"active"|"blocked"`), `sessionId`, `resumeSessionId` (equal to
  `sessionId` in every sample observed — no rotated-id sample exists on this
  host), `daemonShort`, `createdAt`, `updatedAt`, `firstTerminalAt` (**null**
  on two currently-`working` entries, populated once a job reaches a terminal
  state), `cwd`, `name`, `needs` (populated only on `state:"blocked"`
  entries), `inFlight.tasks`, and — only on two entries —
  **`respawnFlags`**: an array reproducing the original `--bg` launch flags
  (`--name`, `--settings`, `--model`), never a prompt. No sample on this host
  has two entries sharing one `cwd`, and no sample distinguishes a
  `claude stop`-stopped job from a `state:"done"` idle job by any field value
  — see Assumption 1.

## Options considered

### Sub-question A: live-idle worker

**A1. Stop-then-resume via `dispatchLaunch` (continueReview's pattern).**
`stopSessionFunc(ctx, shortID)` (abort on failure) → `dispatchLaunch(ctx,
instancePath, newPrompt, buildDispatchPassthrough(...)+["--resume",
sessionID], nil)`.
- Pros: zero new platform assumptions — this exact sequence is shipped and
  proven in `continueReview`. Reuses both seams (`stopSessionFunc`,
  `dispatchLaunch`) unchanged. Naturally produces the R5 disambiguation input
  (two entries at one cwd) the design must already build for watch.
- Cons: inherits #211's ambiguity as a solved-not-avoided problem — R5 must
  actually ship the newest-registration resolver, not just cite it.

**A2. `claude attach` + inject-and-detach.** Attach a controlling terminal,
send the prompt, detach.
- Pros: would avoid minting a new session id entirely.
- Cons: no such primitive exists — `claude attach` takes over the terminal
  interactively; there is no headless inject-then-detach mode. The research
  file states this plainly: "no channel exists today to inject into an
  already-attached, running session without stop+resume." Rejected — not a
  supported surface (R8).

**A3. `claude respawn` in place of resume.** Skip stop, call `claude respawn
<shortID>` directly on a live-idle job.
- Pros: none identified — respawn is documented (via `respawnFlags`
  structure and the task brief's given fact) to deliver no instruction, so it
  cannot carry the follow-up prompt by itself.
- Cons: fails R2 outright (no instruction delivered) unless chained with a
  further resume step, which collapses back to A1 anyway with an extra
  round-trip. Rejected for the live-idle case.

Verdict: **A1**, no real competition. This is also what the task brief
anticipated ("watch's pattern").

### Sub-question B: stopped worker (job entry present, process not running)

**B1. Resume directly, no stop.** `dispatchLaunch(ctx, instancePath,
newPrompt, passthrough+["--resume", sessionID], nil)` with the stop step
skipped entirely.
- Pros: fewest platform assumptions of the three. It relies on exactly the
  capability `continueReview` already exercises one line after its own stop
  call succeeds — i.e. "`--resume` works starting from a job whose process is
  not currently running" is not a new assumption, it's the same fact A1
  already depends on (immediately after `stopSessionFunc` returns, the
  process is not running, and the very next call is `dispatchLaunch` with
  `--resume`). No new CLI surface (`respawn`) enters the critical path.
- Cons: calling `claude stop` on an already-non-running job is untested
  territory the design never has to characterize (does it no-op, error
  "already stopped," or error some other way?) — B1 sidesteps that question
  by never calling stop on a case-B target, but only if resume itself
  tolerates a job whose daemon is not currently alive. That tolerance is
  unverified for the specific case where the process was never revived by
  this call chain at all (as opposed to A1's case, where niwa's own stop call
  just killed it).

**B2. Respawn-then-stop-then-resume.** `claude respawn <shortID>` (revive,
same id, no instruction) → `stopSessionFunc(shortID)` → `dispatchLaunch`
with `--resume`.
- Pros: if plain `--resume` genuinely requires an alive daemon and errors
  against a truly-dead process, this is the fallback that first makes the
  daemon alive (via the one documented surface built for exactly that:
  reviving a stopped job while preserving its id) before applying the proven
  A1/B1 resume step.
- Cons: three platform calls where B1 needs one; the middle stop
  immediately undoes what respawn just did (revive, then kill), which reads
  as wasteful unless respawn is proven necessary. Also unverified: whether
  `respawn` needs the daemon to already be gone (does it error on an
  already-running job?), and whether the revived job is stable enough to
  survive an immediate stop.

**B3. Respawn only.** `claude respawn <shortID>`, no further delivery step.
- Rejected outright: "respawn... delivers no instruction" (given fact,
  consistent with `respawnFlags` never containing a prompt). Cannot satisfy
  R1/R2 by itself under any circumstance.

Verdict: **B1**, with **B2 as the documented fallback** if a live-gate test
shows plain resume fails against a job whose process was never running under
this call chain. This is the "prefer fewest platform assumptions" reading of
the brief: B1 assumes only what A1 already assumes; B2 adds a new,
unverified primitive to the critical path on a hypothesis with no supporting
evidence in this host's real `state.json` corpus (which shows no distinct
"stopped" state value separate from `"done"` at all — see Assumption 1).

### A/B unification

Given the detection layer (below) cannot actually distinguish a
"live-idle" job from a "stopped, entry-intact" job by any observed
`state.json` field — both read as `sessionLive == true`, `state` in
`{"done"}` or similar non-busy, non-blocked values — the practical
recommendation is to **not branch the relaunch sequence by (a)/(b) at all**.
Run the identical continueReview-mirrored sequence (A1/B1: stop → resume)
against every target that clears the "retaskable" gate. Fail-closed on stop
failure exactly as `continueReview` does (never resume alongside a possibly
still-live prior process). This is not a fourth option so much as the
conclusion of comparing A1 and B1: they are the same sequence, and niwa has
no reliable signal to justify treating them differently. B2 stays reserved
as a fallback path gated behind a specific, identifiable stop-failure class
(see Assumptions), not as a parallel first-class sequence.

### Sub-question C: busy/attached/gone detection and error taxonomy

Reusing exactly the fields already decoded by `jobState`
(`internal/cli/job_state.go:25-54`) — no new state.json fields needed:

| Detected state | Rule | Source fields | Refusal |
|---|---|---|---|
| **Gone** | `sessionLive(jobsDir, sessionID, now) == false` | job entry absent | R4: fails closed, distinct error |
| **Busy** | `state ∈ {"running","working"}` OR `tempo == "active"` OR `inFlight.tasks > 0` | `state`, `tempo`, `inFlight.tasks` | R4: "actively running a turn" |
| **Blocked/attached-proxy** | `state == "blocked"` OR `needs != ""` OR `block` non-nil | `state`, `needs`, `block` | R4: "attached to a terminal" (proxy, see Assumption 2) |
| **Retaskable** | `sessionLive == true` AND none of the above | — | proceeds to relaunch |

Error taxonomy (mirrors the existing `"niwa: error: <verb phrase>: %w"`
house style, one sentinel per cause so tests can assert on cause, not
string-match — matches N3's "target, detected worker state, reason"):

- `ErrRetaskTargetUnknown` — no session mapping resolves the given
  instance-name/session-id.
- `ErrRetaskSessionGone` — mapping resolved, `sessionLive` false.
- `ErrRetaskSessionBusy` — mapping resolved, live, actively running a turn.
- `ErrRetaskSessionBlocked` — mapping resolved, live, blocked/attached-proxy.
- `ErrRetaskCaptureAmbiguous` — post-resume disambiguation could not resolve
  a single surviving entry (tie or invalid id at the shared cwd).
- `ErrRetaskConflict` — lost a concurrent-retask race (N2).

Each wraps `fmt.Errorf("niwa: error: retask %s: %s (%s)", target, reason,
detectedState)` so the target and detected state are always in the message,
not just the sentinel type — satisfying N3 even for callers that don't
inspect `errors.Is`.

## Recommendation

1. **Delivery sequence, both (a) and (b), identical**: re-validate persisted
   ids → two-way liveness cross-check at execution time (`sessionLive` +
   `instanceHasLiveJob`, degrade to no-op/fail-closed on mismatch, mirroring
   `continueReview:526-535`) → classify busy/blocked/gone per the table above
   and refuse with the matching sentinel → `stopSessionFunc(ctx, shortID)`
   (abort the whole retask on failure, no partial resume) →
   `dispatchLaunch(ctx, instancePath, newPrompt,
   buildDispatchPassthrough(...)+["--resume", sessionID], nil)` → re-capture
   via the (new, R5-owned) newest-registration disambiguator → rebind mapping
   atomically → delete superseded job entry's mapping remnants (R3).
2. **Do not put `claude respawn` in the critical path.** Reserve it as a
   documented fallback (B2) behind a specific stop-failure signature that a
   live-gate test identifies as "target was never running" (as opposed to
   "still running, do not proceed") — do not build this branch speculatively
   before that signature is known.
3. **Disambiguation key for R5: use `createdAt`, not `firstTerminalAt`.**
   The task brief's own phrasing ("newest `firstTerminalAt`/registration")
   is ambiguous and the more available field is wrong for this purpose: two
   real samples on this host show `firstTerminalAt: null` on currently-active
   jobs — exactly the state the newly-resumed session will be in
   immediately after relaunch (running, not yet terminal). Selecting "newest
   `firstTerminalAt`" would pick the *older*, terminal (stopped) entry, the
   opposite of what R5 needs. `createdAt` is non-null on every sample
   observed and is monotonic registration order — use it as the tiebreak,
   with an explicit "tie or unparsable timestamp fails closed" rule (already
   in R5's text).
4. **Detection and error taxonomy as specified above** — pure reuse of
   already-decoded `jobState` fields, no state.json schema surface expands.

## Assumptions

1. **No `state.json` field distinguishes "live-idle" from "stopped,
   entry-intact" on this host's real data.** All 15 sampled entries show
   `state ∈ {"done","running","working","blocked"}`; none is a case
   plausibly meaning "process explicitly `claude stop`ped, entry
   retained but idle" as distinct from `"done"`. This assumption is why the
   design collapses R4(a)/(b) into one sequence rather than branching. If
   `claude stop` in fact leaves a *different* marker (unobserved because no
   sample here was ever `claude stop`ped), this collapses back into a real
   two-branch design and B2 becomes load-bearing, not a fallback.
2. **"Attached to a terminal" (R4) has no direct signal; `blocked`/`needs`
   is a *proxy*, not the same concept.** `job_state.go`'s own doc comment
   calls this out. A session can be attached (someone watching) while
   `state:"working"` — that case is already caught by the Busy rule, so the
   only gap is a session attached-but-not-blocked-and-not-busy, which this
   design cannot distinguish from Retaskable. Treating Busy as the umbrella
   refusal for "someone/something is actively engaged with this session" is
   the closest the available surfaces get; flagging this as a known
   detection gap rather than claiming full R4 coverage.
3. **`claude respawn` preserves the session id and delivers no instruction**,
   taken as given from the task brief, corroborated only indirectly: the
   real `respawnFlags` field reproduces launch flags (`--name`,
   `--settings`, `--model`) and never a prompt, consistent with "no
   instruction," but nothing in this read-only survey directly exercises
   `claude respawn` or observes its effect on a job's `sessionId`.
4. **`claude --bg --resume <id>` does not require the target job's daemon
   to be currently running at call time**, inferred from `continueReview`
   calling `dispatchLaunch` with `--resume` immediately after
   `stopSessionFunc` succeeds (i.e., against a process it just killed). This
   is extrapolated to also hold for a job whose process died independently
   (case B) rather than via niwa's own stop call — the same fact, but not
   literally the same code path exercised, so B1's core assumption is
   inferred, not directly observed.
5. **`claude stop` against an already-non-running job entry is either a
   safe no-op or a distinguishable, non-fatal error**, needed only if the
   unification in "A/B unification" turns out wrong and B1 cannot be used
   uniformly. Not verified here.

## Confidence

**Medium.** The detection layer (busy/blocked/gone, error taxonomy) is
high-confidence: built entirely from fields already decoded by
`job_state.go` and cross-checked against 15 real production `state.json`
files with populated `state`/`tempo`/`needs`/`inFlight` values matching the
proposed rules exactly. The relaunch-sequence recommendation (unify (a)/(b),
skip `respawn`) is medium-confidence: it is the reading with the fewest new
platform assumptions and is consistent with everything observable on this
host, but Assumptions 1, 4, and 5 are all things a live Claude Code daemon
would need to confirm or refute — none of the 15 real jobs sampled here was
ever `claude stop`ped or `claude respawn`ed, so the specific state transition
this design leans on (stop/dead-then-resume) is extrapolated from
`continueReview`'s adjacent, already-proven case, not directly observed.
**What would raise confidence to high**: an integration or live-gate test
that (1) `claude stop`s a job, confirms the entry's `state.json` shape
post-stop (does it change at all?), then (2) confirms `claude --bg --resume
<id>` succeeds against that stopped entry without a prior `respawn` call.
That single empirical check resolves Assumptions 1, 4, and 5 together and
would let cross-validation treat this decision's core sequence as settled
rather than inferred.
