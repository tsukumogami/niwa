<!-- decision:start id="task-storage-and-state-transition-atomicity" status="assumed" -->
### Decision: Task Storage and State-Transition Atomicity

**Context**

The cross-session-communication PRD (R13–R18, R37, R47, R49) makes tasks
first-class objects materialized under `.niwa/tasks/<task-id>/`. Each task
directory holds `envelope.json` (the delegation payload), `state.json`
(current state plus audit-friendly fields), and `transitions.log`
(append-only history). Multiple processes mutate a task's storage:
the daemon spawns, reconciles, and drives restart/abandon transitions;
the worker reports progress and calls the terminal transition tool; the
delegator queries, updates a queued body, or cancels. Workers are
ephemeral `claude -p` processes that do not register in
`sessions.json` (R40), so the authorization boundary for worker-initiated
transitions cannot piggyback on the coordinator's session registry.

This decision commits to: the field set in `state.json`, the `flock`
scope, the exact write-order for state transitions, the
`transitions.log` format and writer semantics, and the mechanism by
which an ephemeral worker proves it is authorized to mutate task X and
only task X.

**Assumptions**

- Assumed: Linux-only for v1 (matching the rest of niwa's `/proc`-based
  `PIDStartTime` support). If ported to macOS, `pidStartTime` already
  falls back to a conservative "alive" answer and the design's
  start-time fields degrade gracefully (stored as `0`).
- Assumed: the worker inherits environment variables from the
  `claude -p` process that the daemon spawns, and `claude -p` propagates
  its environment into the `niwa mcp-serve` child it launches per
  `.mcp.json`. Both are standard Unix fork/exec semantics; niwa already
  relies on this for `NIWA_INSTANCE_ROOT` (R4).
- Assumed: `syscall.Flock` on a regular file descriptor is adequate for
  in-process coordination across niwa, daemon, and `mcp-serve`
  processes. Flock is advisory, but every niwa-authored writer uses the
  same lock, which is the PRD's stated tolerance (R47 — "advisory
  locking").
- Assumed: the daemon is the sole writer performing the inbox
  consumption rename (R16). `niwa_cancel_task`, `niwa_update_task`, and
  the daemon's claim path are the only three operations that touch the
  queued inbox entry, and they coordinate via the same per-task lock
  plus the rename's filesystem atomicity.
- Assumed: `state.json` is readable only to the owning user (0600 per
  R48), so persisting a proof-of-spawn token inside it does not expose
  the token outside the trust boundary.

**Chosen: Per-task `.lock` with authoritative `state.json`, append-only
NDJSON `transitions.log`, and env-delivered spawn token for worker
auth.**

A task's on-disk layout is:

```
.niwa/tasks/<task-id>/
├── .lock              # zero-byte coordination file (flock target)
├── envelope.json      # immutable except for body via niwa_update_task
├── state.json         # authoritative state + audit-friendly fields
└── transitions.log    # NDJSON append-only audit trail
```

**1. `state.json` field set (authoritative schema v=1):**

```json
{
  "v": 1,
  "task_id": "<uuid>",
  "state": "queued | running | completed | abandoned | cancelled",
  "state_transitions": [
    {"from": null,      "to": "queued",  "at": "RFC3339"},
    {"from": "queued",  "to": "running", "at": "RFC3339"}
  ],
  "restart_count": 0,
  "max_restarts": 3,
  "last_progress": {
    "summary": "truncated to 200 chars + …",
    "body":    { /* arbitrary JSON or absent */ },
    "at":      "RFC3339"
  } ,
  "worker": {
    "pid":        12345,
    "start_time": 8765432,
    "token":      "32-hex-char random token",
    "spawned_at": "RFC3339",
    "adopted_at": "RFC3339 | null"
  },
  "delegator_role": "coordinator",
  "target_role":    "web",
  "result":              { /* present only in completed */ },
  "reason":              { /* present only in abandoned */ },
  "cancellation_reason": { /* present only in cancelled */ },
  "updated_at": "RFC3339"
}
```

`last_progress`, `worker`, `result`, `reason`, and `cancellation_reason`
are nullable/absent by state. `delegator_role` and `target_role` are
duplicated from `envelope.json.from.role` and `envelope.json.to.role`
so that authorization checks on query/mutation tools never need to
re-open `envelope.json` (which the delegator can mutate via
`niwa_update_task`; only `body` is mutable, but concentrating
authorization-relevant data in `state.json` keeps invariants
local). `state_transitions` is duplicated (also appears in
`transitions.log`) so `niwa_query_task` (R20) is a single-file read.

**2. `flock` scope: per-task `.lock` file.**

Every writer to any file inside a task directory acquires an exclusive
flock on `.niwa/tasks/<task-id>/.lock`. Readers do not lock (they read
the atomically renamed `state.json`; stale-read-of-old-version is
acceptable because the rename is atomic and the old version is
internally consistent).

Rationale for scope:
- state.json updates and transitions.log appends must be serialized
  *together* under one lock, because a transition appended without a
  matching state.json update would leave a cross-file inconsistency
  observable to `niwa task show` (which prints both).
- `niwa_update_task` (R27) must mutate `envelope.json` inside the same
  critical section as a "state == queued" check; otherwise the daemon's
  consumption rename could interleave.
- One lock per task isolates tasks from each other: two workers in two
  different tasks never contend.
- A dedicated `.lock` file (not flock-on-state.json) decouples the
  lock file's existence from the presence of state.json (during
  creation, there is a moment before state.json is written where a
  reader/writer racing the creator would see "no state.json"; holding
  the lock via a separate file closes that window).

**3. Write-order for state transitions (single critical section):**

```
1.  f = open(".lock", O_CREATE|O_RDWR, 0600)
2.  flock(f, LOCK_EX)                                  // blocks
3.  current = read_json("state.json")
4.  validate: current.state == expected_from_state
5.  new    = mutate(current)                           // append to state_transitions,
                                                       // bump restart_count, set worker token, etc.
6.  write_json_atomic("state.json.tmp", new, 0600)     // write + fsync
7.  rename("state.json.tmp", "state.json")             // POSIX atomic
8.  fsync(taskDir)                                     // persist rename
9.  appendNDJSON("transitions.log", event, 0600)       // O_APPEND|O_WRONLY|O_CREATE,
                                                       // write + fsync
10. close(f)                                           // releases flock
```

- **state.json is authoritative.** If step 9 fails or crashes, readers
  still see a consistent current state; the audit trail lags. The
  `state_transitions` array inside `state.json` captures the same
  history redundantly, so `niwa task show` degrades gracefully.
- **Rename is the commit point.** Step 7 is the moment at which the
  transition becomes externally visible; steps 1–6 are reversible if
  the process dies.
- **Parent-directory fsync (step 8)** ensures the rename survives a
  crash between step 7 and step 9 on durability-strict filesystems.
- **Single critical section** (flock held for all steps) lets
  multi-writer contention degrade to fairness-via-flock-queue rather
  than lost updates.

For the consumption rename (R16), the daemon's order is: acquire task
lock → attempt inbox rename → on success, update state.json + log →
release. If the inbox rename fails (cancel or update beat it), the
daemon releases the lock with state.json unchanged and moves on.

For `niwa_cancel_task` (R28): acquire lock → attempt inbox rename to
`cancelled/` → on success, update state.json + log → release. The
inbox rename is the serialization point against the daemon.

For `niwa_update_task` (R27): acquire lock → check state == queued
(via state.json) → write envelope.json atomically → write the queued
inbox entry atomically → release. The daemon cannot consume while
the lock is held; cancel cannot cancel while the lock is held.

**4. `transitions.log` format and append semantics.**

- **Format**: newline-delimited JSON (NDJSON), one event per line.
  Event schema (v=1):

  ```json
  {"v":1,"kind":"state_transition","at":"RFC3339","from":"queued","to":"running","actor":{"kind":"daemon","pid":1234}}
  {"v":1,"kind":"progress","at":"RFC3339","summary":"…","body":{...},"actor":{"kind":"worker","pid":5678}}
  {"v":1,"kind":"spawn","at":"RFC3339","worker_pid":5678,"attempt":1,"actor":{"kind":"daemon","pid":1234}}
  {"v":1,"kind":"unexpected_exit","at":"RFC3339","worker_pid":5678,"exit_code":0,"actor":{"kind":"daemon","pid":1234}}
  {"v":1,"kind":"adoption","at":"RFC3339","worker_pid":5678,"actor":{"kind":"daemon","pid":9999}}
  {"v":1,"kind":"watchdog_signal","at":"RFC3339","signal":"SIGTERM","actor":{"kind":"daemon","pid":1234}}
  {"v":1,"kind":"cancelled_by_delegator","at":"RFC3339","actor":{"kind":"delegator","role":"coordinator","pid":4321}}
  {"v":1,"kind":"update_body","at":"RFC3339","actor":{"kind":"delegator","role":"coordinator","pid":4321}}
  ```

- **Writer semantics**: multi-writer, but serialized by the per-task
  flock. No writer opens `transitions.log` without holding the lock.
  Writes use `os.OpenFile(path, O_APPEND|O_WRONLY|O_CREATE, 0600)`,
  `Write(line)`, `Sync()`, `Close()`. Because writes are under flock,
  POSIX `O_APPEND` atomicity limits (PIPE_BUF) do not apply — any
  line size is safe.
- **Readers** (`niwa task show`, daemon reconciliation, debugging)
  open read-only and stream lines; they never lock.
- **Append-only invariant**: the file is opened O_APPEND exclusively;
  no truncation or rewrite ever happens. This is enforced by code
  review, not by filesystem immutable bits, for v1.

**5. Worker authorization (proof-of-spawn).**

Layered. Different tool groups use different auth sources.

**Group A — role-owned tools** (`niwa_query_task`, `niwa_await_task`,
`niwa_list_outbound_tasks`, `niwa_update_task`, `niwa_cancel_task`):
caller's role is read from `NIWA_SESSION_ROLE` (the same mechanism
the coordinator uses; R7). mcp-serve matches the caller's role against
`state.json.delegator_role` (for `NOT_TASK_OWNER`-gated tools) or
against `delegator_role` OR `target_role` (for `NOT_TASK_PARTY`-gated
tools like `niwa_query_task`, per R20). No per-task token is required
for these because the PRD's threat model (Known Limitations: "Role
integrity is the only trust boundary") explicitly permits role-level
auth for queries.

**Group B — worker-exclusive tools** (`niwa_report_progress`,
`niwa_finish_task`): require proof-of-spawn via two environment
variables set by the daemon at `claude -p` spawn time:

- `NIWA_TASK_ID=<task-id>` — the task the worker was spawned to
  consume. Must match `args.task_id`. Prevents cross-task tampering
  (a worker for task X cannot call `niwa_finish_task(Y, ...)`).
- `NIWA_TASK_TOKEN=<32-hex-char crypto/rand>` — proof the worker was
  spawned by niwa's daemon, not impersonated. Must equal
  `state.json.worker.token`.

On every Group-B tool call, mcp-serve:
1. Reads `NIWA_TASK_ID` and `NIWA_TASK_TOKEN` from its own env
   (inherited from worker → `claude -p` → `mcp-serve`).
2. Fails with `{status:"forbidden", error_code:"NOT_TASK_EXECUTOR"}`
   if either is unset or if `NIWA_TASK_ID != args.task_id`.
3. Acquires the per-task flock, reads `state.json`.
4. Fails with `NOT_TASK_EXECUTOR` if
   `NIWA_TASK_TOKEN != state.json.worker.token`.
5. Proceeds with the transition.

**Token rotation**: on daemon-driven restart (R34), the daemon
generates a new token, writes it to `state.json.worker.token` (under
the lock), and spawns the replacement worker with the new
`NIWA_TASK_TOKEN`. Any zombie instance of the previous worker that
comes back with its old token gets rejected at step 4.

**Adopted orphans (R37)**: when a new daemon adopts an orphan worker,
it does not rotate the token (it didn't spawn the worker). The
token already in `state.json.worker.token` remains authoritative; the
adopted worker, still holding its original `NIWA_TASK_TOKEN` env var,
continues to pass the check. `state.json.worker.adopted_at` is set as
an audit marker.

**Defense-in-depth PID check**: when `state.json.worker.pid` is
non-zero, mcp-serve also verifies `os.Getppid() == worker.pid`. This
catches the edge case where a token leaked via `/proc/<pid>/environ`
to a co-tenant process: the co-tenant's PPID would not match. (This
is belt-and-suspenders; the primary authority is the token.)

**Error code**: the PRD reserves six error codes in R50. This decision
adds one — `NOT_TASK_EXECUTOR` — which must be added to R50's
enumeration when the design lands. It is distinct from `NOT_TASK_OWNER`
(which applies to the delegator of a task) and `NOT_TASK_PARTY`
(which applies to non-parties reading task state).

**Rationale**

- **state.json as authoritative, transitions.log as audit.** Two
  alternatives were considered: event-sourcing (transitions.log is
  truth, state.json is a cache) and dual-authority (both files must
  match). Event-sourcing forces every reader to replay the log, which
  `niwa_query_task` (R20) needs to answer in a single file read for
  latency. Dual-authority doubles the crash-consistency surface. A
  committed-state-file plus append-log is the simplest model that
  preserves the PRD's `state_transitions` array requirement without
  forcing replay.
- **Per-task `.lock` file beats flock-on-state.json.** Holding a lock
  on the file you are about to atomically rename-over is a common
  but subtle bug: after rename, the fd points to the pre-rename
  inode, which no longer governs future writers. A dedicated `.lock`
  sidesteps this entirely. `.lock` is never renamed or unlinked
  during a task's life; it is the stable serialization anchor.
- **Per-task scope (not per-file, not per-workspace) matches the
  workload.** Two tasks have no shared state; global locking would
  serialize spawns across roles unnecessarily. Per-file locking
  cannot atomically span state.json + transitions.log.
- **NDJSON over binary/CBOR/protobuf.** The PRD names the file
  `transitions.log` and specifies "append-only NDJSON log" (R14).
  NDJSON is line-oriented (tail-readable), human-debuggable, and
  trivially generated by `encoding/json`.
- **Sync-before-close for durability.** fsync of `state.json` (the
  data file), of its parent directory (the rename), and of
  `transitions.log` gives crash-consistency up to the filesystem's
  durability promise without pulling in a database.
- **Token-based worker auth sized to v1's threat model.** The PRD
  accepts role-level integrity as the only trust boundary (Known
  Limitations: "An agent that overrides `NIWA_SESSION_ROLE` to
  impersonate another role can update, cancel, or query tasks
  belonging to the spoofed role"). A stronger-than-role mechanism
  for terminal transitions is still worthwhile — otherwise any
  process with `NIWA_SESSION_ROLE=web` could call
  `niwa_finish_task(...)` on any web task and falsely terminate it.
  The token binds "worker that was actually spawned for task X" to
  "caller of niwa_finish_task(X, ...)". It fits the PRD's
  "authorization boundary" note in the decision context. It costs
  nothing to generate (`crypto/rand`), nothing to store (32 hex
  chars in state.json), and nothing to transport (one env var). It
  does not introduce a shared secret or a key management problem:
  the token lives exactly as long as the task, and its trust anchor
  is the 0600 file mode on `state.json`.
- **PID PPID check as defense in depth.** Even if a token were
  somehow read out of a co-tenant's environ, the PPID check would
  catch impersonation attempts. This is cheap (`os.Getppid()`) and
  adds no config surface.

**Alternatives Considered**

- **Global `.niwa/tasks.lock` (workspace-wide serialization).**
  Rejected: serializes unrelated tasks. Under a coordinator
  dispatching to four roles, every transition contends on one lock.
  The PRD's stress test (R47) explicitly scales to 1000 iterations
  of concurrent writers; a global lock would make per-task latency
  proportional to workspace-wide activity.
- **Per-file locks (state.json.lock + transitions.log.lock).**
  Rejected: the two files must mutate together to maintain the
  invariant that `state.json.state_transitions` and
  `transitions.log` don't diverge. Two locks admit an interleaving
  where writer A appends to the log while writer B is mid-rename of
  state.json, producing a log entry whose `from` state never
  appeared in state.json.
- **flock on state.json itself.** Rejected: the fd becomes stale
  after atomic rename. Requires releasing the lock before rename and
  re-acquiring after, which is a non-atomic window.
- **State as directory sentinel files (`running/`, `completed/`).**
  Rejected: structured fields (`restart_count`, `last_progress`,
  `worker.token`) need a container. JSON file is the natural fit.
- **Event-sourced state (transitions.log is truth).** Rejected:
  `niwa_query_task` would need log replay on every call. Defer-able
  for v2 if the state.json snapshot ever becomes a bottleneck, but
  for v1 the snapshot is strictly simpler.
- **Binary transitions log (CBOR or length-prefixed JSON).**
  Rejected: the PRD specifies NDJSON in R14. No performance
  motivation to deviate.
- **Role-only worker auth (no token).** Rejected: any process in
  the workspace that sets `NIWA_SESSION_ROLE=web` could finish or
  abandon any task targeted at `web`. This violates the decision
  context's explicit requirement: "a worker executing task X should
  be able to complete X but not tamper with task Y."
- **PID-only worker auth (no token, match state.json.worker.pid
  against os.Getppid()).** Rejected: relies on the implementation
  detail that `claude -p` spawns `mcp-serve` directly. If Claude
  Code ever pools or proxies MCP subprocesses, the PPID check
  silently stops working. Token auth is stable under such changes.
- **Cryptographic signing (JWT-style tokens, per-agent keys).**
  Rejected: the PRD defers this (Out of Scope: "Message encryption
  and agent authentication"). A 128-bit per-task random token is
  the pragmatic middle ground.
- **Token via argv.** Rejected: R33 fixes the spawn argv exactly,
  and R478 (body-in-argv prohibition, via the "Delivering task
  envelope via inbox" decision in the PRD) extends that discipline
  to avoid argv-borne control-plane data. Env vars are the
  idiomatic spawn-time credential channel.

**Consequences**

What becomes easier:
- `niwa_query_task` is a single-file, lock-free read (atomic rename
  guarantees consistency).
- `niwa task show` renders both state and history from state.json
  alone if transitions.log is missing; the two together provide
  cross-checked audit.
- Daemon reconciliation (R37) has one file to open per task to
  answer "who's running? what's their PID? what's their start
  time?"
- Cross-file invariants (state.json vs transitions.log vs
  envelope.json) are enforced by one lock, not by distributed
  coordination.
- Worker authorization has a crisp test: token matches, or it does
  not. No need to reason about role impersonation in the terminal
  path.

What becomes harder:
- state.json carries several fields duplicated from
  envelope.json (delegator_role, target_role). Drift is possible if
  a future `niwa_update_task` ever mutates envelope.from.role — it
  must not. (This decision assumes the PRD's constraint that only
  `body` is mutable holds.)
- Adding a new event kind to `transitions.log` is a schema change
  (mitigated by the `kind` discriminator and `v=1` prefix on each
  line).
- The daemon must fsync state.json, its parent directory, and the
  log on every transition. On slow disks this bounds task-
  transition rate; measured cost is ≈ 1 fsync-round-trip per
  transition. Acceptable for the PRD's load profile (one worker
  per role, 1000-iteration stress in R47).
- A new error code `NOT_TASK_EXECUTOR` is introduced and must be
  added to the PRD's R50 enumeration before the design is accepted.
- `state.json.worker.token` is a user-readable file containing a
  128-bit secret; 0600 mode is load-bearing. A regression in the
  file-mode discipline (R48) would leak the token to local users
  — but the same regression would leak all other niwa state, and
  the existing AC-P14 verifies file modes under a `umask 0000`
  adversarial setting.

This decision integrates with four downstream design decisions:
the daemon's reconciliation behavior (R37), the MCP tool auth layer
(R20/R21/R24), the spawn path (R33), and the worker-inbox retrieval
path (R16, R26–R28). It does not foreclose a future v2 that replaces
state.json with a sqlite-backed store or transitions.log with a
stream-to-stdout debug sink; the per-task directory remains the
portable unit.
<!-- decision:end -->
