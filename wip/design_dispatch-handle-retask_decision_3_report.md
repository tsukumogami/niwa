---
decision: 3
topic: rebind atomicity and race guards for the session-mapping key change
---

# Decision 3: Mapping rebind across a session-id key change, and race guards

## Question

`niwa retask` must rebind the durable mapping (`.niwa/sessions/<session-id>.json`,
keyed by full session UUID) from a superseded session id to the surviving one,
in the same operation that establishes the survivor (R3). How should that
rebind be sequenced, and what guard makes concurrent retask-vs-retask and
retask-vs-reap safe (N1, N2), given the mapping store's existing
write-temp-then-rename atomicity is per-file, not cross-file?

## Options considered

**A. Rebind ordering: write-new-then-delete-old.** `WriteSessionMapping(new)`
then `DeleteSessionMapping(old)`. Crash between the two leaves both files on
disk, both claiming the same `InstancePath`.

**B. Rebind ordering: delete-old-then-write-new.** Crash between the two
leaves zero mapping for a session that (by then) is live — worse: the
instance becomes unresolvable by name/id (no mapping to join against) even
though a live job is rooted there.

**C. Schema change: key the mapping file by instance instead of session id**
(`.niwa/instances/<name>.json` with `SessionID` as a field). Rebind becomes a
single atomic field-overwrite via the existing rename primitive — no
transient two-file window at all.

**D. In-mapping `superseded_by`/`retask_in_progress` marker field**, written
into the JSON before the old session is stopped.

**Concurrency guard candidates:** O_EXCL claim file (this repo's existing
`ReserveID` pattern, `internal/worktree/atomicid.go`), flock on a dedicated
lock file (this repo's existing pattern, `internal/cli/sessionattach/attach.go`),
or relying on jobs-dir state transitions alone (no new guard).

**Reap exclusion candidates:** a marker `reap` reads and skips on, or making
`reap` take the same lock the guard above defines.

## Recommendation

**Rebind ordering: write-new-then-delete-old (Option A), inside a per-instance
flock held for the whole stop→resume→capture→rebind sequence. Reject the
instance-keyed schema change (C); keep a bare marker/lock file, not a JSON
field, for exclusion (reject D as insufficient on its own).**

### Why A over B

Both orderings have a crash window that leaves the store in a transient
non-1:1 state, but the states are not equally bad:

- B's window (delete-old succeeds, write-new not yet) leaves a **live
  session with zero resolvable mapping** — unlistable as ephemeral, unretaskable
  by name, and indistinguishable from a plain unmapped instance until manual
  repair. This is the exact "zero-resolvable-mapping-while-a-session-lives"
  state the invariant in your prompt calls out as forbidden.
- A's window (write-new succeeds, delete-old not yet) leaves **two mapping
  files that both resolve correctly to the live instance**, one of them
  live and one provably dead (the old job entry is already gone by
  construction — stop happens before resume). This is recoverable by a
  *join-time disambiguation rule*, not a rename-ordering trick: any code that
  joins mappings by `InstancePath` (today only `selectReapTargets`'s `byPath`
  map, which currently does a blind last-write-wins assignment — a latent bug
  that's merely unreachable today because nothing yet creates two mappings
  for one instance) must prefer the mapping whose `sessionLive` is true, and
  should opportunistically delete the losing, dead-session duplicate. That
  fix is small, local to `selectReapTargets`, and self-heals the crash
  residue on the very next reap sweep — including the `reapOpportunistically`
  sweep that already runs at the top of every `create`/`dispatch`.

So A degrades to "one harmless stale file, auto-pruned," B degrades to
"orphaned live instance, needs a human." A wins.

### Why not C (instance-keyed schema)

C would make the rebind itself trivially atomic (reuse the existing
write-temp-rename on one file), which is real value — but the cost is
disproportionate to what's being bought. `SessionMapping` is looked up
**by session id** from at least one hot, frequency-sensitive path outside
retask: `instance_from_hook.go`'s SessionEnd teardown, which only knows its
own session id and does an O(1) validated-UUID-path lookup
(`ReadSessionMapping`/`DeleteSessionMapping`, `session_map.go:127-141,190-199`).
Re-keying by instance turns that into an O(n) directory scan (or requires a
second index), touches `sessionMappingPath`'s validation contract (there's no
existing "validate this as a safe instance identifier" primitive as clean as
`sessionUUIDRe`), and ripples through `reap.go:201`'s
`DeleteSessionMapping(workspaceRoot, t.SessionID)` and every test fixture that
constructs a `SessionMapping` by session id. R8 ("no state.json edits...
documented surfaces only") and the design doc's own driver ("reuse over
rebuild... nothing here needs a new architectural pattern") both argue against
a schema migration to solve a problem the lock + join-fix already solves
without touching the schema at all. Reject C.

### Why not D alone (in-mapping marker field)

A `retask_in_progress` field is a good **forensic** signal (why does this
mapping's session look dead?) but it is not a mutual-exclusion primitive: two
concurrent retasks can both read the field as unset and both proceed to set
it, unless the read-then-set is itself made atomic — which just reintroduces
the need for a lock. Use a lock as the actual guard; a JSON field adds a
schema change for no exclusion benefit, so skip it. (If audit trail is wanted
later, the lock file's own existence plus its mtime is already enough forensic
signal — no schema change needed.)

### Concurrency guard: flock, not O_EXCL

This repo has two live precedents:

- `internal/worktree/atomicid.go`'s `ReserveID` — O_EXCL create, used for
  **claiming a never-before-seen name**. If the claiming process crashes,
  the placeholder is simply an unused, harmless leftover.
- `internal/cli/sessionattach/attach.go`'s `acquireAttachLock` — non-blocking
  `flock` (`LOCK_EX|LOCK_NB`) on a dedicated lock file, with a companion
  sentinel (`AttachState`) plus PID-liveness staleness recovery, because that
  lock is held for the life of an **interactive attach session** that can be
  SIGKILLed out from under the lock, needing a recovery dance.

Retask's lock is held only for the duration of one CLI invocation's
stop→resume→capture→rebind sequence (seconds), not an interactive session.
That makes flock strictly better here than an O_EXCL marker: the OS releases
a flock the instant the holding process's file descriptor closes — including
on crash or SIGKILL — with **no staleness-detection dance required**, unlike
attach.go's long-lived lock. Recommendation: a non-blocking flock on
`.niwa/retask-locks/<instance-name>.lock` (or an equivalent per-instance
path), acquired before the first mutating step (stopping the old session) and
released via `defer` after the rebind (write-new + best-effort delete-old)
completes — mirroring `acquireAttachLock`'s exact shape and
`errLockHeld`-style fail-closed error on contention.

Key the lock by **instance identity** (name or path), not session id. The
session id changes mid-operation; keying the lock by something that stays
constant across the whole sequence avoids ever needing to release one lock
and acquire a differently-named one partway through (which would reopen
exactly the race window the lock exists to close).

### Reap exclusion: reap takes the same lock, non-blocking, skip-on-held

Extend `selectReapTargets` (`internal/cli/reap.go:106-174`) so that, for each
ephemeral+dead-by-`sessionLive` candidate it would otherwise destroy, it
first attempts the same non-blocking flock on that instance's retask-lock
path. If the trylock succeeds, no retask is in flight — release immediately
and proceed with the existing eligibility checks unchanged. If it fails
(`EWOULDBLOCK`), a retask holds it — skip the target for this sweep entirely,
regardless of what `sessionLive`/`instanceHasLiveJob` report. This is the
precise mechanism that closes the "stop-before-resume gap looks dead but
shouldn't be reaped" hole: during that gap the old job entry is genuinely
gone and `instanceHasLiveJob` genuinely returns false, so liveness signals
alone cannot protect the instance — only an explicit in-flight marker can.
The check is a pure probe (open, trylock, unlock-if-acquired, close) with no
side effects on the eligibility decision itself, so it composes cleanly with
`selectReapTargets`'s existing "performs NO destruction... unit-testable
against fixture mappings and a fixture jobs tree" contract — add a fixture
case with a held lock file (real flock against a real temp file, following
`TestAttachRunLockHeldByLiveProcess`'s existing test style, which already
proves flock is exercised directly in this codebase's unit tests, no fake
needed) and assert the target is excluded.

### Crash-recovery table (recommended design: A + flock + reap trylock)

| Crash point | State left on disk | How the next niwa command interprets it |
|---|---|---|
| Before lock acquired | Old mapping intact, old session live, no lock file | Fully unaffected. Matches N1 literally. |
| After lock acquired, before stop | Old mapping intact, old session live; lock auto-released (flock dies with the process) | Next retask/reap sees no lock held, old mapping fully valid. Matches N1. |
| After stop, before resume succeeds | Old mapping present but its session is now dead (job entry gone); no new job entry yet; lock auto-released | `sessionLive(old)=false` and `instanceHasLiveJob=false` — the instance genuinely looks fully orphaned. The primary reap sweep (now unblocked, lock gone) reclaims it on the next pass. This is correct disposal of a truly-dead attempt: nothing is running, the transcript persists in Claude's own history (per the PRD's Known Limitations), and the caller simply retries dispatch/retask. Not silent data loss, but it does surface as "my retask vanished" if the crash is unlucky — worth a note in the design doc's limitations, not a blocker for this decision. |
| After resume succeeds (new job live), before capture/write-new | New session live and rooted at the instance path; old mapping still on disk, points at the now-dead old id; lock auto-released | Primary reap sweep would key on the old mapping's dead session id, but the existing defense-in-depth `instanceHasLiveJob` check (`reap.go:163`, already present today, no change needed) spares the instance because a live job *is* rooted there. Net effect: instance survives, but its mapping identity is stale (points at a dead session) until a human or a future repair path re-captures it. This is the one residual gap rebind-ordering cannot close by itself — it is upstream of the write step, inherent to the fork-based delivery mechanism, and should be flagged as a known limitation rather than solved here. |
| After write-new succeeds, before delete-old | Both old and new mapping files present, both claiming the same `InstancePath`; lock auto-released | Any `InstancePath` join (reap's `byPath`, and any future retask target-resolution helper) applies the "prefer the live one" disambiguation this decision requires, resolves correctly to the new mapping. The stale old file is inert and is pruned by the same fix on the next reap sweep (including the opportunistic one at the top of the next `create`/`dispatch`). Fully safe; single visible owner; satisfies R3/R6. |
| After delete-old succeeds | Exactly one mapping, keyed by the new session id | Steady state. |

The only state in this table that isn't either "fully safe" or "self-healing
within one reap sweep" is the resume-succeeded-but-not-yet-captured window,
and that gap exists regardless of which rebind ordering is chosen — it's
upstream of the mapping write entirely. Minimizing the code between
resume-success and write-new (do capture and the mapping write back-to-back,
nothing else in between) is the only ordering-level mitigation available;
full auto-recovery from that state is a separate, larger repair-path question
(recognizing "mapping's session is dead AND `instanceHasLiveJob` is true" as
an implicit re-capture opportunity) that this decision flags but does not
attempt to solve.

## Assumptions

1. The delivery mechanism is stop-then-`--resume` (mirroring
   `continueReview`, `internal/cli/watch.go:507-610`), i.e. the old job entry
   is gone before the new one is created — this is what makes "old mapping's
   session is provably dead" true throughout the analysis above. If a future
   delivery mechanism (R9's channel-path swap) avoids stop entirely, this
   whole rebind analysis narrows to just the write-new/delete-old step with
   no stop-window gap, and gets simpler, not harder.
2. `selectReapTargets`'s `byPath` map (`internal/cli/reap.go:116-121`) today
   does last-write-wins on an `InstancePath` collision with no such collision
   currently reachable in production. This decision treats fixing that join
   to prefer the live mapping as in-scope, small, local work required by R3 —
   not a pre-existing bug this PRD merely inherits and ignores.
3. The retask-lock path is derived from instance identity (name or absolute
   path), lives under the workspace-root `.niwa/` alongside `sessions/`, and
   is never deleted after use (mirroring `attach.lock`'s lifecycle) — its
   mere presence past a run is inert since flock's exclusivity, not the
   file's existence, is the actual guard.
4. "Fail-closed" per N1 is read as: any crash or error strictly before the
   old job is stopped leaves the prior session, job entry, and mapping fully
   intact and usable. Once `stopSessionFunc` succeeds, the prior *session* is
   no longer running by definition of the delivery mechanism (assumption 1) —
   N1's guarantee from that point on is about the *mapping/store* staying
   coherent and self-healing, not about the old session still being alive,
   which the PRD's own Known Limitations section already acknowledges as a
   property of fork-based delivery, not a rebind-ordering failure.
5. Unit tests can exercise the lock and its reap-side trylock directly
   against real temporary files/real flock syscalls (no fake needed), per
   existing precedent (`sessionattach/attach_test.go`'s
   `TestAttachRunLockHeldByLiveProcess`), satisfying the PRD's requirement
   that N2's interleavings be testable through seams.

## Confidence

High on the core recommendation (write-new-then-delete-old + flock keyed by
instance identity + reap taking the same trylock) — it reuses two patterns
already proven in this codebase (`WriteSessionMapping`'s atomic rename,
`acquireAttachLock`'s flock) with no schema change and a small, local fix to
`selectReapTargets`'s join. Medium on the exact reap-side wiring location
(whether the trylock check belongs inside `selectReapTargets` per-candidate,
as recommended, or as a pre-filter before it) — that's an implementation
placement detail for the design doc's code section, not a decision that
changes the safety argument. Medium-low on the residual
resume-succeeded-but-not-yet-captured gap: this decision documents it and
argues it's inherent to the delivery mechanism rather than solvable by
ordering, but whether the design doc wants an explicit follow-up repair path
for it is a scope call outside this question.
