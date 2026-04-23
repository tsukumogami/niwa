<!-- decision:start id="daemon-architecture" status="assumed" -->
### Decision: Daemon Architecture

**Context**

`niwa mesh watch` is the persistent per-instance daemon that consumes
queued envelopes from `.niwa/roles/<role>/inbox/`, spawns `claude -p`
workers, and supervises them through the task lifecycle defined in the
PRD (R32, R34, R36, R37, R38). It must survive its own crashes,
reconcile tasks left running by a prior daemon, honor a stall-progress
watchdog (default 15 min), respect a 3-restart cap with 30/60/90s
backoff, and interoperate with sender-side cancellation via atomic
rename. All of this must work with Go stdlib + fsnotify only, with no
in-memory authoritative state — `state.json` on disk is the source of
truth.

The decision space centered on how the daemon's internal goroutines
should be organized and how worker exit is detected in two distinct
situations: (1) workers the daemon itself spawned, where it holds an
`*exec.Cmd` handle, and (2) adopted orphans inherited from a crashed
predecessor daemon, where only the PID is known. SIGCHLD-based
reaping, per-concern goroutine splits, and a single giant event loop
were all considered. The chosen shape is a central event-handler
goroutine paired with one per-task supervisor goroutine for
daemon-spawned workers, plus a central ticker for adopted orphans.

**Assumptions**

- `state.json` is the authoritative current-state document per
  Decision 1 of this design; `transitions.log` is observability only.
- The PRD's R36 wording ("the resulting exit shall be treated as an
  unexpected exit (R34) and shall apply the restart policy") means
  watchdog-triggered kills DO consume a retry slot. Only explicit
  `niwa_finish_task(outcome="abandoned")` bypasses the counter.
- Running under --auto mode without user confirmation on
  sub-questions; status marked `assumed`.

**Chosen: Central event loop + per-task supervisor goroutine**

The daemon runs one central event-handler goroutine that owns:

- fsnotify watches on every `.niwa/roles/<role>/inbox/` directory
- A `taskEvent` channel receiving all per-task events from
  supervisors (exit, spawn-failed, watchdog-fired) and from the
  orphan poller (orphan-exit)
- A 2-second adopted-orphan poll ticker
- SIGTERM / shutdown handling and the drain sequence already in
  `mesh_watch.go`
- Sole write access to every `.niwa/tasks/<id>/state.json` inside
  the daemon process (flock remains required because the MCP server
  also writes `state.json` on worker tool calls)

For every daemon-spawned running task, one per-task supervisor
goroutine is launched at spawn time. The supervisor:

1. `cmd.Start()` has already happened in the central loop; the
   supervisor receives the `*exec.Cmd` handle.
2. Calls `cmd.Wait()` in a dedicated sub-goroutine, funneling the
   exit into a local `exit` channel.
3. Holds a `time.Timer` for the stall watchdog, armed with
   `NIWA_STALL_WATCHDOG_SECONDS` (default 15 minutes).
4. Listens on a `progressTick` channel poked by the central loop
   whenever it observes that `state.json.last_progress_at` for this
   task has advanced (detected on the 2s orphan/progress tick).
5. On watchdog fire: sends SIGTERM to the worker, arms a grace timer
   (default 5s via `NIWA_SIGTERM_GRACE_SECONDS`); on grace expiry,
   sends SIGKILL. Whichever path exits first wins.
6. On either natural exit or watchdog-induced exit, sends a
   `taskEvent{kind: exit, taskID, exitCode, watchdogTriggered}`
   on the shared channel and returns.

Adopted orphans — running tasks whose daemon-spawning predecessor
crashed — do not get a supervisor goroutine. The central loop's 2s
tick iterates the adopted-orphan list: for each entry, it calls
`mcp.IsPIDAlive(pid, startTime)` (the existing liveness primitive).
If alive, nothing changes; if dead, the central loop synthesizes an
`orphan_exit` `taskEvent` and processes it identically to a
supervisor-sent `exit` event. The stall watchdog for adopted orphans
runs on the same 2s tick (central loop reads
`last_progress_at` + `stall_watchdog_seconds` and fires SIGTERM from
the central loop if exceeded).

**Startup reconcile** happens before any fsnotify watcher is
registered. The daemon scans `.niwa/tasks/` and, for each task whose
`state.json.state == "running"`:

- If `IsPIDAlive(worker_pid, worker_start_time)` is true, mark the
  task adopted: set `state.json.adopted_at = now()` and append to
  the in-memory adopted-orphan list. No new worker is spawned.
- If the PID is dead, synthesize an `orphan_exit` event (no timer,
  dispatched immediately through the central loop). The
  unexpected-exit handler applies: increment `restart_count`,
  schedule the backoff timer anchored at "now" (not at the
  historical exit time, which is unknown).

Queued-state tasks do not need reconciliation, but the daemon must
perform a **catch-up scan**: after registering all fsnotify watchers,
iterate each `inbox/` directory once and synthesize an `inbox_create`
event for every pre-existing envelope, because fsnotify does not fire
for files that existed before the watcher was added.

**Worker-exit detection**: `cmd.Wait()` for daemon-spawned workers
(idiomatic Go; no SIGCHLD handler — attempting SIGCHLD + `Wait4(-1)`
races with `os/exec`'s own bookkeeping). For adopted orphans, PID
liveness polling at 2s intervals (within the R37 ≤ 5s ceiling).

**Double-spawn avoidance on crash recovery**: `restart_count` is the
retry-slot counter. The central loop writes the incremented
`restart_count` to `state.json` BEFORE spawning the retry. If the
daemon crashes between the counter write and the spawn, the next
daemon's reconcile sees `running` + dead PID + already-incremented
counter and uses the existing slot — no double-increment, and at most
one respawn per slot. A `spawn_started_at` timestamp written
immediately before `cmd.Start()` distinguishes "slot allocated" from
"slot allocated + spawn completed", so the reconciler knows whether
to reuse the current `restart_count` or start fresh.

**Crash recovery source**: `state.json` + current filesystem contents
(task directories + inbox contents). `transitions.log` is NOT
replayed; it serves `niwa task show` and debugging only. On any
conflict between `state.json` and `transitions.log`, `state.json`
wins.

**Cancellation race**: handled implicitly by atomic rename semantics.
When the central loop attempts the consumption rename
(`inbox/<id>.json` → `inbox/in-progress/<id>.json`) and the sender
has already renamed to `cancelled/`, `os.Rename` returns ENOENT. The
central loop logs and moves on; no spawn, no state transition. This
matches PRD AC-Q10.

**Rationale**

- **`cmd.Wait()` per worker is irreducible in Go**. Alternatives
  based on SIGCHLD fight the `os/exec` contract. Given that a
  goroutine per worker exists anyway, housing the stall watchdog
  and the SIGTERM/SIGKILL escalation in that same goroutine keeps
  per-task state local, removes "map of timers" bookkeeping in the
  central loop, and makes each task's lifetime a small, testable
  unit.
- **Single writer to `state.json` inside the daemon** removes
  internal write-race reasoning. Flock between daemon and MCP server
  is still required (per R47 and Decision 1) because the MCP server
  writes on `niwa_finish_task` and `niwa_report_progress`. Flock
  protects corruption; funneling daemon-internal writes through the
  central loop protects against logic races (e.g., watchdog firing
  concurrently with natural exit).
- **Adopted orphans are polled, not supervised**. They have no
  `*exec.Cmd` handle; the only portable liveness check is
  `kill(0)` + `/proc/<pid>/stat` (already implemented). A central
  2s ticker iterating a small list (bounded by the number of roles,
  which is typically < 10) costs nothing. Per-orphan supervisor
  goroutines would add code with no payoff.
- **Progress-tick via polling `last_progress_at`** avoids extra IPC
  between daemon and MCP server. The PRD explicitly chose file
  coupling over Unix sockets (prior-art Decision 3 rejects named
  pipes). The ≤ 2s poll slop is invisible against a 15-min default
  watchdog window and tolerable for the 2-second
  `NIWA_STALL_WATCHDOG_SECONDS` override used by AC-L4.
- **Watchdog kill consumes a retry slot**. The PRD text is
  unambiguous (R36: "treated as an unexpected exit (R34) and shall
  apply the restart policy"). Treating watchdog kills as a separate
  abandonment path would have contradicted the spec.
- **`state.json` wins over `transitions.log` on crash recovery**.
  Replaying the log would be more code, more risk of divergence,
  and no extra guarantee. `state.json` is updated via flock + atomic
  rename (Decision 1); it is reliable.
- **Catch-up scan on startup** closes the fsnotify-doesn't-fire-for-
  pre-existing-files gap. Without it, envelopes queued before the
  daemon started would sit forever.

**Alternatives Considered**

- **Alternative A — Single event loop with embedded per-task timers**:
  One goroutine owns fsnotify, timers, SIGCHLD-surrogate channels, and
  reconciliation. Rejected because once `cmd.Wait()` per worker is
  conceded (unavoidable in Go), A degenerates to "central loop plus
  goroutines that just forward exits" — which is Alternative C with
  the per-task logic inside-out. The central `select` and its timer
  map grow faster than the per-task supervisor of C.
- **Alternative B1 — SIGCHLD + `syscall.Wait4(-1, ...)`**: Traditional
  Unix daemon shape. Rejected because it conflicts with
  `os/exec.Cmd`'s expectation that the owner calls `cmd.Wait()`;
  using both produces ECHILD races and broken `Cmd` state. A
  bespoke fork/exec wrapper would be required; stdlib does not
  document a safe coexistence path.
- **Alternative B2 — Per-concern goroutines (fsnotify, spawn
  supervisor, watchdog, exit reaper), `cmd.Wait()` per worker, no
  SIGCHLD**: Rejected because it degenerates to C with multiple
  writers to `state.json`, gaining flock contention inside the
  daemon and opening logic-race windows (watchdog and exit-reaper
  both deciding to write a transition for the same event).
- **Sub-option C1 — fsnotify watches on each task's
  `transitions.log`** to reset the stall timer: Rejected because
  it proliferates fsnotify watches (one per running task, added
  and removed dynamically) with no benefit over the 2s
  `state.json.last_progress_at` poll.
- **Sub-option C3 — Unix socket / named pipe between daemon and
  MCP server** for progress notifications: Rejected in line with
  prior-art Decision 3, which explicitly removed IPC in favor of
  file coupling. Adds a single-point-of-failure the file-based
  design specifically avoids.

**Consequences**

What becomes easier:

- Reasoning about task state transitions: they all originate in the
  central loop.
- Testing the daemon: per-task supervisor has narrow inputs
  (`progressTick`, context, exit channel) and narrow outputs
  (`taskEvent`). The central loop's event handlers can be unit
  tested by feeding synthetic events.
- Supporting new worker-control features later (e.g., pause/resume):
  add a method on the per-task supervisor; the central loop dispatches
  a new event kind.

What becomes harder:

- Goroutine leak hunting: more goroutines to audit on shutdown.
  Mitigation: the per-task supervisor is the only long-lived
  goroutine per task; its exit is synchronous with the `taskEvent`
  it sends. Central loop's shutdown sequence waits on all
  supervisors before exiting (same `sync.WaitGroup` pattern as the
  current daemon).
- Exact stall-timer precision: a task can stall at most
  `stall_watchdog_seconds + ~2s` before SIGTERM is sent, due to
  the progress-tick poll slop. This is a minor, documented
  consequence (matches R37's ≤ 5s ceiling on the adopted-orphan
  poll philosophy).

Operational implications:

- On machine sleep/wake, `time.Timer`s fire at their monotonic
  deadline, so a 15-minute watchdog armed just before sleep fires
  immediately on wake. This is acceptable: the task truly did stall
  for that wall-clock duration.
- After `rm -rf` on the instance directory (rather than
  `niwa destroy`), fsnotify errors still trigger the existing
  "sessions directory removed" exit path in `mesh_watch.go`
  extended to watch `.niwa/roles/` instead.
- The per-task supervisor's SIGTERM/SIGKILL escalation must run
  even during daemon shutdown; the drain sequence must give
  supervisors time to complete their escalation before the daemon
  exits (5-second drain is already the pattern).
<!-- decision:end -->
