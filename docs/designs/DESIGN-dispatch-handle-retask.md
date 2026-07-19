---
schema: design/v1
status: Planned
problem: |
  Claude Code offers no in-place delivery into a live background
  session, so re-tasking a dispatched worker means a relaunch that
  mints a new session id while the old job entry lingers — ambiguous
  capture, mapping desync, reap races, and (for watch) lost sandbox
  re-assertion. niwa needs one primitive that crosses those hazards
  safely for both the new retask command and watch's continuation.
decision: |
  A shared engine in internal/cli: default-deny worker classification,
  stop-then-resume through the dispatch launch path, exclude-known
  capture keyed on the superseded id already held in the mapping,
  write-new-then-delete-old mapping rebind under a per-instance flock
  the reaper also honors, superseded job entry removed last. The
  delivery step sits behind a func-var seam; rebind stays caller-owned
  (retask command rebinds the session mapping, watch rebinds its
  staged record). Generic retask refuses watch-sandboxed instances.
rationale: |
  Exclude-known needs no ordering signal and collapses capture to the
  already-tested single-match case; write-new-then-delete-old plus
  live-mapping-wins reap preference makes every crash window
  self-heal; the flock pattern mirrors the attach lock and closes the
  stop-window reap race; keeping the engine in internal/cli reuses
  watch's proven seams without exporting internals; the func-var seam
  lets a future platform channel replace fork-based delivery without
  interface change. Refusing sandboxed instances is the only safe
  answer while containment re-assertion requires watch-only context.
upstream: docs/prds/PRD-dispatch-handle-retask.md
---

# DESIGN: Dispatch handle retask

## Status

Planned

## Context and Problem Statement

The PRD requires a `niwa retask <target> <prompt>` command that delivers
a follow-up instruction to a dispatched session with single-owner
semantics, and a shared primitive niwa watch can adopt for chainable
review continuation.

The technical problem: Claude Code offers no in-place delivery into a
live background session, so the only supported route is relaunch-based —
stop the worker's process if needed, resume the transcript into a new
background session, and live with the platform minting a new session id
while the superseded session's job entry lingers. That route crosses
four hazards the design must neutralize:

1. **Capture ambiguity.** niwa correlates sessions to instances by jobs
   cwd. After a resume, two job entries share the instance cwd and
   `captureSessionID` reports ambiguity — today's watch continuation
   returns empty ids and degrades to once-per-session (#211).
2. **Mapping integrity.** The durable mapping under
   `.niwa/sessions/<session-id>.json` is keyed by session id; a rebind
   replaces the mapping's identity, not just a field, and a crash
   between resume and rebind must not strand the instance.
3. **Races.** `reapOpportunistically` runs at the start of every
   create/dispatch and deletes mappings for dead sessions; a retask's
   stop window makes the session look dead. Concurrent retasks against
   one target contend for the same rebind.
4. **Containment.** Watch's review sessions run under a no-egress
   sandbox that must be re-asserted through the same settings-applying
   launch path on every relaunch.

The affected code: `internal/cli/dispatch.go` (launch + capture),
`internal/cli/dispatch_capture.go` (cwd correlation),
`internal/workspace/session_map.go` (mapping store),
`internal/cli/reap.go` (liveness + reclamation), `internal/cli/list.go`
(observability join), and `internal/cli/watch.go` (`continueReview`,
the existing one-shot form of this flow).

## System Model

What this feature is, at the system level, before any implementation
detail:

- **Runtime shape: a CLI verb, not a daemon.** `niwa retask` is a
  short-lived invocation like `dispatch`, `list`, and `reap`: it runs,
  acts, exits. niwa keeps its no-runtime-component model — nothing
  niwa-owned runs between invocations. The long-running processes on
  the host remain Claude Code's own (its daemon and session
  processes); niwa observes them through files and drives them through
  the `claude` CLI.
- **Network surface: none.** No port, no socket, no IPC endpoint. All
  coordination is filesystem-based: read-only reads of Claude Code's
  jobs directory, niwa's own mapping files, and one per-instance lock
  file. Delivery of the instruction itself rides a `claude` CLI
  relaunch, not a connection to the running process.
- **State and survival.** All state is durable on disk: the
  conversation transcript (Claude Code's), the job entry (Claude
  Code's), and the session mapping (niwa's). Consequently:

  | Worker state | Retask behavior |
  |---|---|
  | Live and idle | Delivered (stop, relaunch with context) |
  | Process dead, job entry intact | Delivered (relaunch from the durable transcript) |
  | Host rebooted since dispatch | Delivered (nothing needed lived in memory) |
  | Actively running a turn | Refused — retask never interrupts work |
  | Attached to a terminal | Refused — a human owns the session |
  | Session removed (`claude rm`) | Refused — the association is gone; fail closed, never guess |
  | Watch-sandboxed review session | Refused — only watch can safely re-assert containment |

- **Scale: no per-session cost at rest.** Running 100 sessions on one
  host adds nothing from this feature: no niwa processes, watchers, or
  open descriptors exist between invocations. One retask costs a scan
  of the jobs directory (linear in session count), one flock, and a
  few small file writes. Locks are per-instance, so concurrent retasks
  of different workers never contend; two against the same worker
  serialize deliberately. The ceilings at high session counts are
  Claude Code's own pre-existing ones (daemon process pool and memory,
  the ~1h idle-process stop, OS fd/process limits) — retask changes
  none of them and keeps no session alive that would otherwise stop.
- **Trust boundary: unchanged.** Whoever has the invoking user's
  filesystem access can retask, exactly as they can already dispatch,
  stop, or attach. No new boundary is introduced.

## Decision Drivers

- **Single-owner invariant (R3/R6):** one live session per instance;
  the niwa handle survives while session ids rotate.
- **Chainability (R5/R7):** retask N+1 must work; capture must resolve
  deterministically with two entries on one cwd.
- **Fail-closed (N1/R4):** every failure path leaves prior state
  usable; busy/attached/gone workers are refused with distinct errors.
- **Race safety (N2):** concurrent retask-retask and retask-reap cannot
  corrupt state; interleavings must be testable through seams.
- **Supported surfaces only (R8):** claude resume/stop/respawn and
  jobs-dir reads; no state.json edits, no root, no fenced channels.
- **Replaceable delivery (R9):** the delivery step sits behind one seam
  so a future in-place channel swaps in without interface change.
- **Reuse over rebuild:** watch's continueReview already implements a
  one-shot version; the primitive should generalize it, not duplicate
  it, and watch must keep its sandbox re-assertion.
- **House style:** cobra command with SilenceErrors/SilenceUsage and
  `niwa: error: ...` formatting, `--json` output, seam-injected
  dependencies for offline tests.

## Considered Options

Four decision questions were evaluated independently and
cross-validated.

### Q1 — Delivery sequence per worker state

- **Chosen: one uniform sequence for both retaskable states.**
  Re-validate the mapping's ids, run the two-way liveness cross-check,
  classify the worker, then `stopSessionFunc` (abort the retask if the
  stop fails) and relaunch through the dispatch launch path with
  `--resume <session-uuid>` appended — `continueReview`'s exact
  pattern. Inspection of real job state shows no field distinguishes
  "live-idle" from "stopped with entry intact," so branching on that
  line is neither possible nor necessary: `claude stop` on an
  already-stopped session is a no-op and the resume path is identical.
- **Rejected: respawn-based revive for stopped workers**
  (`claude respawn` then deliver). Respawn preserves the session id
  but delivers no instruction, so it would still need a second
  delivery step; its id-preservation under a subsequent resume is
  unverified; and it adds a platform surface for no removed hazard.
  Kept as a documented fallback if the uniform sequence fails its
  live-gate verification on genuinely dead processes.
- **Rejected: state-dependent branching** (separate live-idle vs
  stopped code paths). No observable field supports the branch; two
  paths mean two test matrices for one behavior.

Worker classification is default-deny (see Security Considerations,
R-SEC-2): gone = `!sessionLive`; a worker is retaskable only when the
decoded job state positively proves it idle (terminal state, no active
tempo, no in-flight tasks, no pending need); busy, blocked, absent, or
undecodable signals all refuse. The decoder and classifier already
exist: watch's continuation work decodes the activity fields and ships
`watch.ClassifySessionActivity`, a fail-closed positive-idle
classifier — `classifyWorker` wraps it rather than inventing a second
classification. Seven sentinel errors
(target-unknown, session-gone, busy, blocked, sandboxed,
capture-ambiguous, conflict) each carry target, detected state, and
reason (N3).

### Q2 — Surviving-session capture

- **Chosen: exclude-known capture.** Before relaunching, retask holds
  the superseded session's full UUID from the mapping. The capture
  seam gains an exclusion parameter: matching jobs whose session id is
  the known-superseded one are ignored, collapsing the two-entries-one-
  cwd case back to the single-match shape the capture design already
  handles. The superseded job entry is removed only after a successful
  rebind (PRD D5); after exclusion, more than one remaining candidate
  is ambiguous and fails closed with nothing removed and the old
  session still resumable.
- **Rejected: remove-first** (`claude rm` the old session before
  relaunch). Violates fail-closed: if the rm succeeds and the resume
  then fails, the worker and its job entry are gone with nothing
  delivered. Also inverts PRD D5.
- **Rejected: newest-registration capture.** There is no timestamp to
  key on — niwa's job-state decoder deliberately omits creation and
  terminal timestamps, and real samples show the tempting
  `firstTerminalAt` field is null exactly on the entries a newest
  heuristic would need to rank. File mtime was considered and rejected
  as an unstable proxy (the daemon rewrites state files continuously).
  Exclusion needs no ordering signal at all.

### Q3 — Mapping rebind and race guards

- **Chosen: write-new-then-delete-old, per-instance flock, reap
  trylock.** The rebind writes the surviving session's mapping file
  (atomic temp-then-rename, the store's existing discipline), then
  deletes the superseded mapping. A crash between the two leaves two
  mapping files whose instance-path collision is resolved in favor of
  the live session on the next reap sweep (a small deliberate fix to
  the reap join, which today is last-write-wins). Concurrency: retask
  takes a non-blocking flock on a per-instance lock file for the
  duration of the invocation — the same shape as the attach lock, and
  flock self-releases on crash. The reaper takes the same lock
  non-blockingly and skips the instance when held, which is what
  protects the stop-to-capture window where liveness signals genuinely
  read dead.
- **Rejected: delete-old-then-write-new.** A crash between the steps
  strands a live session with no resolvable mapping — the exact state
  the single-owner invariant forbids.
- **Rejected: instance-keyed mapping schema.** Re-keying the store by
  instance would make rebind a single-file update, but it breaks the
  O(1) session-id lookup the SessionEnd teardown hook depends on and
  forces a migration for every existing workspace — disproportionate
  to avoiding one two-file window that already self-heals.
- **Rejected: O_EXCL lock files.** They persist after a crash and need
  a staleness protocol; flock's kernel-scoped ownership does not.

### Q4 — Primitive placement and the delivery seam

- **Chosen: engine + command files inside `internal/cli`; rebind stays
  caller-owned; the R9 seam is a package-level func var.**
  `retask_engine.go` implements classify → stop → relaunch →
  exclude-known capture; `retask.go` wires the cobra command, target
  resolution, the lock, and the SessionMapping rebind. The delivery
  seam is `retaskDeliver` (default `resumeDelivery`), taking a request
  (instance path, session ids, prompt, passthrough flags, a PreLaunch
  hook) and returning the surviving ids plus a `Rotated` flag. A
  future in-place channel delivery returns the same ids with
  `Rotated: false`, making capture and rebind no-ops without touching
  callers — the seam contract holds both shapes because
  disambiguation is internal to the resume-based implementation.
- **Rejected: a new `internal/retask` package.** watch.go lives in
  `internal/cli`; extracting the engine would force exporting a dozen
  package-internals (launch, capture, liveness) for zero reuse gain.
  The repo already splits engine files from command files within
  `internal/cli`.
- **Rejected: an engine that owns rebind end-to-end.** The two
  consumers persist to different stores — dispatch's
  `workspace.SessionMapping` vs watch's `watch.StagedRecord` — so a
  generic rebind would need a store abstraction invented for exactly
  two implementations. Instead the engine returns disambiguated ids
  and each caller updates its own record; `continueReview` keeps its
  freshness, cap, liveness, and sandbox re-assertion pre-checks and
  simply consumes the engine's ids in place of today's ambiguous
  re-capture — which is precisely the once-per-session fix.
- **Rejected: an interface type for delivery.** The codebase's
  established test seam is the func var (`stopSessionFunc`,
  `dispatchCapture`, `dispatchLaunch`); an interface adds indirection
  the second implementation does not yet justify.

## Decision Outcome

`niwa retask <target> <prompt>` resolves the target through the
session mapping, takes the per-instance lock, classifies the worker
from decoded job state, and refuses busy/blocked/gone targets with
sentinel errors. For a retaskable worker it stops the process,
relaunches through the dispatch launch path with `--resume`, recovers
the surviving session by exclude-known capture, rebinds the mapping
write-new-then-delete-old, removes the superseded job entry, and
releases the lock. The engine half is shared: watch's `continueReview`
adopts it for stop/relaunch/capture while keeping its own pre-checks,
sandbox re-assertion, and staged-record store — closing its
once-per-session limitation as a side effect. The whole flow uses only
supported CLI surfaces, holds the fail-closed line at every step
before the rebind, and confines fork-awareness behind one delivery
seam so a native in-place channel can replace it wholesale.

## Solution Architecture

Components (all in `internal/cli` unless noted):

- **`retask.go` — command.** Cobra wiring in the dispatch/reap house
  style (SilenceErrors/SilenceUsage, `niwa: error: ...`). Target
  resolution: try instance name against the mapping store's instance
  index, then short id prefix against mapping session ids; ambiguity
  or no match is target-unknown. Flags: `--json` (result record:
  instance, old/new session ids, rotated, state) and the shared
  passthrough set dispatch already accepts where meaningful. Owns:
  lock acquisition, precondition classification call, engine call,
  SessionMapping rebind, superseded job-entry removal, output.
  Refuses instances carrying a watch staged record or a sandbox
  stanza in their settings (R-SEC-1, `sandboxed` sentinel error) —
  only watch's own continuation path can safely re-assert review
  containment. The lock filename is built from the resolved
  `mapping.InstanceName` after a path-component assertion (R-SEC-4),
  and every session id read from a mapping body is re-validated with
  `ValidSessionID` immediately before entering argv or a path
  (R-SEC-3).
- **`retask_engine.go` — engine.** `classifyWorker(jobsDir, ids)` →
  retaskable | busy | blocked | gone (+ reason fields);
  `resumeDelivery(req) (result, error)` implementing stop → relaunch
  (`dispatchLaunch` + `--resume`) → exclude-known capture (extended
  seam on the cwd-correlation matcher); package var
  `retaskDeliver = resumeDelivery` (R9 seam). The PreLaunch hook runs
  between stop and relaunch — watch passes its settings re-assertion
  here.
- **`internal/workspace/session_map.go` — store additions.**
  `RebindMapping(root, oldID, newMapping)`: write-new (atomic rename)
  then delete-old, preserving Label, Ephemeral, Origin, and KeepAlive
  fields onto the surviving mapping (KeepAlive semantics: carried as a
  record of the dispatch-time opt-in; the superseded session's armed
  wake dies with its job entry, per the documented platform behavior).
- **Lock.** `.niwa/locks/<instance>.lock` under the workspace root,
  non-blocking flock, held for the CLI invocation. `selectReapTargets`
  gains the same trylock (skip instance when held) and the
  live-mapping-wins collision preference; the existing
  `instanceHasLiveJob` cwd guard is retained unchanged — the new
  checks compose with it, they do not replace it.
- **Watch adoption.** `continueReview` replaces its inline stop +
  dispatchLaunch + `captureReviewSession` block with an engine call
  carrying `PreLaunch: ApplyReviewSettings(...)` and the staged
  record's known session id as the exclusion; it then saves its staged
  record from the returned ids. Its defer/degrade policies are
  untouched.

Data flow (retask command):

```
resolve target -> flock(instance) -> classify -> [refuse: sentinel error]
  -> stop -> relaunch(--resume, prompt) -> capture(exclude old id)
  -> RebindMapping(write-new, delete-old) -> claude rm old
  -> unlock -> report (human | --json)
```

Failure at any arrow before RebindMapping aborts with prior state
intact (the stopped worker remains resumable; nothing was removed).

## Implementation Approach

1. **Live-gate verification first.** One disposable-host check of the
   two extrapolated platform behaviors: stop-then-resume delivers into
   a genuinely dead-process job entry, and a second resume chains
   after an exclude-known capture. This validates Q1's uniform
   sequence before code lands on it (fallback: respawn-based revive).
2. **Store and lock groundwork.** `RebindMapping` with unit tests over
   crash interleavings (write-new done/delete-old pending, both
   pending); lock helpers; reap trylock + collision preference with
   the interleaving tests N2's criterion names.
3. **Engine.** `classifyWorker` wrapping
   `watch.ClassifySessionActivity` against fixture state files (the
   seven sentinel-error taxonomy); `resumeDelivery` with the exclusion-extended
   capture seam; unit tests drive the seams with fakes.
4. **Command.** `retask.go` wiring, target resolution, `--json`,
   prompt-as-single-argv guard test.
5. **Watch adoption.** Swap `continueReview`'s relaunch block onto the
   engine; staged-record update from returned ids; keep the existing
   functional suite green and extend it with the chaining scenario.
6. **Docs.** `docs/guides/` page for retask semantics (states,
   errors, fork-under-the-hood caveat), CLAUDE.md index line, and the
   upstream-facts note feeding the separate platform feature request.

## Security Considerations

The security review produced four required behaviors, all reflected in
the architecture above, plus a clean bill on the overall privilege
posture.

- **R-SEC-1 — refuse watch-sandboxed instances.** Watch review
  sessions run under a no-egress OS sandbox because they carry
  untrusted PR content, and that containment is re-asserted through
  `ApplyReviewSettings` plus a verification gate that only watch's own
  continuation path knows how to drive (it holds the owner/repo/ask
  context). A generic retask would relaunch the session against
  whatever `.claude/settings.json` sits on disk — which a
  bypass-permissions review worker could have rewritten — restoring
  egress to a transcript full of untrusted content. `niwa retask`
  therefore detects a watch staged record or sandbox stanza and
  refuses with the `sandboxed` sentinel, directing the operator to
  watch.
- **R-SEC-2 — default-deny classification.** A naive classifier
  keying on undecoded fields would read zero values and treat a busy
  worker as retaskable, stopping it mid-turn. The classifier refuses
  unless the decoded state positively proves idleness. Watch's
  continuation work already ships exactly this discipline
  (`watch.ClassifySessionActivity`, fail-closed, positive-idle);
  `classifyWorker` reuses it, and the live-gate step re-confirms the
  field semantics against real state files rather than introducing a
  parallel decoder.
- **R-SEC-3 — session-id revalidation at point of use.** The mapping
  filename's id is validated today, but the JSON body's id is what
  flows into `claude stop` / `--resume` / `claude rm` argv. A corrupt
  or hand-edited body could smuggle a flag-shaped string or an
  unrelated session's UUID. Every id read from a mapping body is
  re-checked with `ValidSessionID` immediately before argv or path
  use.
- **R-SEC-4 — lock filename from validated resolved name.** The lock
  path component is the resolved `InstanceName`, asserted to be a
  bare path component (no separators, not `.`/`..`), never the raw
  user-controlled target string — closing traversal out of
  `.niwa/locks/`.

Clean findings: retask introduces no new trust boundary — every
surface it reads or writes (mapping store, lock dir, jobs dir,
instance settings) is same-user, and anyone able to forge those files
can already run `claude` directly. The prompt argument rides the
existing single-argv, no-shell-interpolation dispatch guard. The flock
pattern self-releases on crash. Privilege posture: no root, no managed
settings, writes confined to the workspace and niwa state (N4).

## Consequences

Positive:

- Every dispatch handle becomes re-taskable with one command, with
  the single-owner invariant enforced instead of documented.
- Watch continuation chains past once-per-session, closing #211's ask
  through shared machinery rather than a watch-only patch.
- The reap join's collision preference and the per-instance lock fix
  latent races that exist today independent of retask.
- The delivery seam localizes fork-awareness, so a future platform
  channel unlock is an implementation swap, not a redesign.

Negative, with mitigations:

- The underlying session id rotates per retask; external references
  to the raw id go stale. Mitigated by handle stability (mapping,
  `--json` reporting both ids) and documented in the guide.
- A crash between resume and rebind can briefly leave a live job with
  a stale mapping — inherent to fork-based delivery, not fixable by
  ordering. Mitigated by the reap sweep's live-mapping-wins self-heal
  and surfaced as a documented limitation.
- A failed `claude rm` of the superseded entry (rebind already
  durable) leaves two job entries on the cwd until the next retask,
  whose exclude-known capture again excludes the recorded old id — the
  ambiguity does not compound, but the stale entry lingers for the
  operator to remove; the command reports it rather than failing the
  completed retask.
- Retask refuses sandboxed review instances, a capability gap
  operators may hit. Deliberate (R-SEC-1); watch remains the driver
  for those sessions.
- Two platform behaviors are extrapolated (uniform stop-then-resume on
  dead-process jobs; chained exclude-known capture). Gated by the
  live-gate verification step before dependent code lands; respawn
  fallback documented.
