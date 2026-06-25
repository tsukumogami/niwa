# Decision D4 — How should `niwa dispatch` guarantee no UNRECLAIMABLE orphan instance on partial failure?

Decision research for DESIGN-instance-dispatch. Public visibility. All citations are
`file:line` against the niwa worktree at `.niwa/worktrees/niwa-ed11e932`.

---

## The problem, verified

The reclamation sweep (`selectReapTargets`, `internal/cli/reap.go:90-145`) reclaims an
instance only when it can join an on-disk instance record against a session mapping by
`instance_path` (`reap.go:100-105,116-123`) AND both sides carry the ephemeral marker
(`reap.go:112,129`). An instance with **no mapping** is skipped — explicitly, with the
comment that "reaping on the marker alone would risk an instance whose session is still
live" (`reap.go:117-123`).

The dependence on the mapping is total. The on-disk `Ephemeral` flag the reaper reads is
itself **derived from the mapping store**: `EnumerateInstanceRecords` resolves
`Ephemeral` via `ephemeralInstancePaths`, which scans `.niwa/sessions/*.json` and marks a
directory ephemeral only when some mapping points at it (`state.go:401-419,437-467`).
There is no on-disk ephemeral signal independent of the mapping today.

The mapping can only be written **after** launch. `WriteSessionMapping` rejects any key
that is not a canonical lowercase UUID (`session_map.go:71-76,83-87`, regex at `:20`), and
that UUID exists only once Claude assigns it post-launch (`prd_instance-dispatch_phase2_code-facts.md`
Q2). So the dispatch sequence has an unavoidable window:

```
create instance  ->  [ launch  ->  capture UUID ]  ->  write mapping
        \________________ NO MAPPING window ________________/
```

Any death inside that window leaves an instance on disk with no mapping. It is
`Ephemeral:false` to the enumerator, invisible to the reaper, and reclaimable by **nothing** —
a permanent orphan. The PRD names this the central atomicity hazard (R32) and lists three
failure points: launch fails (R33), capture fails/times out (R34), mapping write fails
(R35). The PRD's design-constraints section already leans toward command self-rollback and
explicitly defers the reaper-marker alternative "to the DESIGN to consider but not
required" (PRD lines 395-402).

---

## Option A — Command self-rollback (deferred cleanup guarded by a success flag)

On any failure between instance-create and durable-mapping-write, the command calls the
destroy path (`realDestroyInstance` / `workspace.DestroyInstance`,
`instance_from_hook.go:414-419`) to remove the just-created instance before returning the
error. Implement with a Go `defer` that fires unless a `success` flag was set after the
mapping write lands.

**Pros**
- Directly satisfies R33/R34/R35 for the common failure modes (launch error, capture
  timeout, mapping-write error) — these are all ordinary returned errors, exactly what a
  deferred guard catches. The destroy path already exists and is the same force-destroy
  the hook teardown and the reaper use (`destroyInstanceFunc` -> `realDestroyInstance`,
  `instance_from_hook.go:102-105,414-419`).
- Zero change to reclamation semantics. R38 stays trivially true: an in-flight instance is
  never reapable because it never gets an ephemeral mapping until success, and a
  mapping-less instance is invisible to the sweep by construction (`reap.go:117-123`). The
  opportunistic sweep another dispatch triggers cannot touch it.
- Smallest blast radius. New logic lives entirely inside the new command; it touches no
  shared `reap.go` / `state.go` code that the hook path and `niwa list` depend on.
- Backward compatible. No schema change, no new on-disk artifact, no change to what
  `niwa list` or existing reaper tests observe.

**Cons / risk**
- **Does not cover SIGKILL** (or `kill -9`, OOM-kill, power loss, panic that bypasses
  defers). A Go `defer` does not run when the process receives an uncatchable signal. If
  the command is SIGKILLed inside the no-mapping window, the orphan persists with no
  cleanup — the exact unreclaimable orphan R32 forbids. This is the load-bearing gap.
- The deferred destroy is itself fallible (e.g. `.niwa/sessions` parent unwritable, or
  destroy races a concurrent operation). R35 anticipates this and requires the outcome be
  surfaced to the developer (R42); a rollback that itself fails must report "instance NOT
  reclaimed" loudly rather than swallow it.
- For R34 specifically, rollback must also **stop the launched session** before destroying
  its instance, else a live `claude --bg` keeps running against a deleted tree. The defer
  must sequence: stop session, then destroy instance.

## Option B — Teach the reaper to reclaim mapping-less ephemeral instances via an on-disk marker

Stamp the instance directory at create time (a sentinel file, or an `instance.json` field)
so the reaper can identify a dispatch-created-but-unmapped instance and reclaim it after a
TTL.

**Pros**
- Closes the SIGKILL gap. A marker written to disk at create time survives the process
  death that kills a deferred cleanup. This is the **only** option of the three that
  reclaims a hard-killed orphan, because it is the only one whose recovery agent (the
  reaper) is a separate process from the one that died.
- Self-healing and idempotent: any later sweep finds and reclaims the orphan with no
  operator step (matches the spirit of R30's crash/reboot recovery).

**Cons / risk**
- **Changes reclamation semantics**, which the existing code deliberately forbids:
  `reap.go:117-123` refuses to reap on the marker alone precisely because there may be no
  session to declare dead. A mapping-less instance has **no session id**, so the existing
  liveness rule (`sessionLive`, keyed on the session's job state) cannot run on it. The
  reaper would need a second, marker-plus-TTL-only reclamation path that bypasses the
  liveness check entirely. That is a genuine new reclamation mode, not a tweak.
- **Direct R38 conflict unless TTL-gated.** R38 requires the opportunistic sweep NOT
  reclaim an in-flight dispatch's not-yet-mapped instance. A marker-based reaper makes
  every in-flight instance look reapable. The only thing separating "in-flight, 2 seconds
  old" from "orphaned by SIGKILL, will never be mapped" is elapsed time. So this path is
  safe only behind a TTL strictly longer than the worst-case dispatch wall-clock (which is
  dominated by instance-create/clone cost — see PRD R45 / Known Limitations, an
  uncapped-from-this-PRD quantity). Pick the TTL too short and you reap a healthy
  in-flight dispatch (R38 violation); too long and orphans linger. This coupling is the
  main reason it is heavier than it looks.
- Larger blast radius: edits shared `reap.go` selection logic and the
  `EnumerateInstanceRecords`/`ephemeralInstancePaths` derivation (or adds a parallel
  marker scan), code the hook path and `niwa list` share. New tests for the marker path,
  the TTL gate, and the R38 in-flight-protection interaction.
- The marker must be removed on the success path too, or a successfully-mapped instance
  carries a redundant "unmapped orphan" marker that confuses a later reader.

## Option C — Provisional mapping then finalize

Write a placeholder mapping at create time, rewrite with the real UUID after capture.

**Assessment: not possible without store changes — reject.** `WriteSessionMapping`
constructs the on-disk path from the session id and rejects any non-UUID key **before
touching the filesystem** (`session_map.go:71-76,83-87`). At create time there is no UUID
(that is the whole premise of the problem), so there is no valid key to write a placeholder
under. Forcing it would mean either (a) inventing a synthetic UUID — which then has to be
reconciled/migrated to the real UUID, and risks a colliding-prefix false match in the
liveness guard (`isBackgroundWorker` / `sessionLive` both compare the recorded `sessionId`,
`instance_from_hook.go:292-295`, `job_state.go:99-101`) — or (b) relaxing the store's key
validation, which weakens the security invariant that a session id flowing from untrusted
hook stdin is always a validated UUID before it becomes a path component
(`session_map.go:14-27`). Both are larger, riskier surgery than Option B for strictly less
benefit (a provisional mapping with no real session id still can't pass the liveness rule,
so the reaper still couldn't reclaim it without the same semantics change Option B needs).
Option C buys nothing Option B doesn't, at higher cost. Drop it.

---

## The SIGKILL gap — verdict

**The SIGKILL gap is real and Option A alone does NOT close it.** A Go deferred cleanup
does not run on SIGKILL/SIGSEGV-equivalent, OOM-kill, `kill -9`, or power loss. If the
dispatch process dies that way inside the no-mapping window, Option A leaves exactly the
unreclaimable orphan R32 prohibits. No amount of care in the deferred function changes
this — the function never runs.

Whether this **must** be closed depends on how strictly R32's "every terminal state" is
read. R32 says every terminal state SHALL be (a) mapped, (b) cleaned up, or (c)
guaranteed-reclaimable. A SIGKILL-orphan is none of the three under Option A alone, so a
strict reading of R32 is **not** fully satisfied by Option A by itself. R33-R35, by
contrast, are each phrased around a failure the command "returns" from — they are
satisfied by Option A, because a returning command runs its defers.

This is the same class of hazard the existing hook path already lives with: `Create` is
not transactional and a SIGKILL mid-pipeline leaves a half-built dir the reaper does not
clean (`prd_instance-dispatch_phase2_code-facts.md` Q4). Dispatch does not get to be
sloppier than the hook, but it inherits a pre-existing non-guarantee rather than inventing
a new one.

---

## Recommendation: Option A as the primary mechanism, with Option B's on-disk marker as a defense-in-depth reaper backstop. A reaper backstop IS needed.

Adopt **Option A** as the primary, synchronous guarantee: a deferred rollback guarded by a
success flag, sequencing stop-session-then-destroy-instance, and surfacing the rollback
outcome per R42. It is the cheapest path that satisfies R33/R34/R35 and keeps R36/R37/R38
trivially intact, with the smallest blast radius and full backward compatibility. It
handles every failure the command can return from — the overwhelmingly common case.

But Option A alone leaves the SIGKILL orphan, and a strict reading of R32 ("every terminal
state ... no unreclaimable instance") is not met without a process-external recovery agent.
So **also add a minimal Option-B marker as a backstop**, scoped tightly:

- At create time, stamp the dispatch instance with an on-disk marker (a sentinel file or an
  `instance.json` field) recording "dispatch-created, awaiting mapping" plus a creation
  timestamp.
- On the success path, the marker is cleared (or rendered moot) the moment the durable
  mapping is written.
- Extend the reaper with a second, narrow reclamation path: an instance carrying the
  marker, with **no mapping**, older than a TTL strictly longer than the worst-case
  dispatch wall-clock, is reclaimed. This path runs **only** for marker-present +
  mapping-absent instances; the existing mapping+liveness path is unchanged for everything
  else.

This composition is the standard try-then-sweep pattern: A is the fast in-process
guarantee; B is the slow out-of-process safety net for the uncatchable-death case. The TTL
gate is exactly what reconciles B with R38 — an in-flight dispatch's instance is younger
than the TTL, so the opportunistic sweep skips it; a true SIGKILL-orphan ages past the TTL
and gets reclaimed. Recommend setting the TTL conservatively (well above observed
instance-create cost) and documenting it as the same class of TTL backstop the existing
liveness window already is (PRD Known Limitations).

If the DESIGN judges the SIGKILL orphan an acceptable, documented Known Limitation (mirroring
the existing hook path's non-transactional `Create`), then Option A alone is a defensible
minimum and B can be deferred to a follow-up. But that is a conscious downgrade of R32 from
"guaranteed" to "best-effort plus a documented gap," and it should be called out as such in
the DESIGN rather than left implicit. The recommendation here is to do both, because the
marker is cheap relative to the audit-trail cost of a class of orphan that nothing can ever
reclaim.

### Why not B alone
B alone would mean every dispatch's instance spends its whole life (until TTL) reapable-on-
marker, relying solely on a TTL race to avoid reaping healthy in-flight work — fragile
against R38 and slow to clean even normal failures (you wait out the TTL instead of rolling
back immediately). A is the right primary; B is the right backstop. Layering them gives the
immediate rollback for returnable failures and the eventual sweep for the uncatchable ones.
