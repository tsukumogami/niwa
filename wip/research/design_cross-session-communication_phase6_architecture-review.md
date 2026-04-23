# Architecture Review: cross-session-communication

## Verdict

NEEDS_REVISION

The design is substantially ready to implement and the seven Considered-Options sections do the heavy architectural lifting. The Solution Architecture, Implementation Approach, and Consequences sections mostly extend and catalog what the decisions already committed to. But a handful of specific gaps would stop a developer from starting cleanly on Phase 1-4, and a small number of Consequences are understated or missing. None of these require re-opening decisions; they can be closed with targeted edits (new interfaces named, a few phases re-split, three or four negatives added).

Severity-summary of issues found:

- **High (blocks implementation start):** 4
- **Medium (needs clarification before the implementing phase):** 7
- **Low (polish / doc drift):** 5

## Implementability

The Solution Architecture is concrete in most places — `Server` struct additions are typed, the `authorizeTaskCall` signature is given, the filesystem layout is drawn, the daemon sequence is spelled out (lines 543, 711-762) — but a developer starting Phase 1 today would hit the following vague spots:

**High — undefined `taskEvent` shape.** The central channel of the daemon architecture (Decision 2) and the MCP server's `awaitWaiters map[string]chan taskEvent` (lines 320, 649) depend on a type that is never defined. What fields does `taskEvent` carry? At minimum: task_id, kind (completed/abandoned/cancelled/progress/exit), exit code, timestamp, optionally `result`/`reason` bodies. Without this the Phase 1 `types.go` deliverable is incomplete and the notifyNewFile → awaitWaiters bridge in Phase 3 cannot be coded.

**High — daemon↔MCP waiter coupling is hand-waved.** The design says (line 320) "the existing fsnotify `notifyNewFile` pathway is extended to route `task.completed/abandoned/cancelled` messages to `awaitWaiters`." But the daemon writes the `task.completed` file, while the MCP server (a different process, per-session stdio) owns the awaitWaiters map. So the MCP server must itself watch `.niwa/roles/<its role>/inbox/` via its own fsnotify — is that watcher already in place? The existing codebase has a watcher (`internal/mcp/watcher.go`) and `notifyNewFile` — the design should state explicitly that every per-session MCP server runs its own fsnotify watch on its own role's inbox and routes type-based messages to waiter maps. The one sentence "extends `notifyNewFile`" understates that the entire delegator-side sync-delegate, await-task, and ask path depend on this existing watcher being reliable.

**High — `PPIDChain(n int)` walks up how many levels, in practice?** Decision 3 says "the MCP server walks PPID up one level" (line 335) but the Phase 1 deliverable (line 872) specifies `PPIDChain(n int)` as a helper. The actual call site should be named — probably `n=1` — and the expectation when `n>1` (to tolerate a future proxy layer) is left implicit. A developer writing auth.go needs to know which PID the check compares against `state.json.worker.pid`: the MCP server's direct parent? Its grandparent? The design assumes the topology `claude -p` → `niwa mcp-serve`, so the mcp-serve's parent PID is the worker PID. This should be stated in the auth section alongside the interface.

**High — `expires_at` on tasks vs `expires_at` on messages.** The task envelope (line 664) includes `expires_at`. But there is no design for who enforces it and when. R15 allows `expires_at` on envelopes; R31 sweeps expired messages on inbox read. For task envelopes, which have their own `.niwa/tasks/<id>/` dir and are moved out of the inbox on consumption rename, there is no equivalent enforcement point. Either the design should say "task `expires_at` is honored only while the envelope is queued" (documented and tested) or explicitly defer it to v2 (like `deadline_at`). Currently it is ambiguous.

**Medium — exact `state.json` field name discrepancies.** `state.json` schema in Decision 1 (lines 199-222) uses `"worker": {"pid", "start_time", "role", "spawned_at", "adopted_at"}` and `"last_progress": {"summary", "body", "at"}`. The Data Flow text (line 724) writes `worker.{pid:0, start_time:0, role:web, spawn_started_at}`. `spawned_at` vs `spawn_started_at` — these appear to be meant as the same field but the naming differs. Decision 2's text says `spawn_started_at` distinguishes "retry slot allocated" (line 292). A spec-level drift this small is confusing at implementation time. Pick one name and use it throughout.

**Medium — `niwa_wait` vs `niwa_await_task` appears twice.** The component diagram (line 593) mentions `niwa_wait` as a message tool; the task tools list (line 595) mentions `niwa_await_task`. The Server-struct additions (line 647) include `typeWaiters map[string]*typeWaiter` keyed by wait-UUID. Is there a separate `niwa_wait` message-typed-wait tool being preserved from the old implementation? The PRD does not list it. If it is being retained (historical compatibility?), the Solution Architecture should say so explicitly; if not, remove it from the component list. Phase 3 deliverables ("dispatcher adds the new tool handlers") do not list `handleWait`.

**Medium — skill `allowed-tools` list includes `niwa_wait` but no design for it.** Line 429 has `niwa_wait` in the installed skill's `allowed-tools`. If this is a legacy carry-over, either the tool surface needs to include it deliberately or the skill must be trimmed.

**Medium — daemon PID file startup ordering.** Line 688 says "daemon.pid is written atomically after the daemon's fsnotify watches and central goroutine are ready. `niwa apply` uses a flock on `.niwa/daemon.pid.lock` before reading the PID file." What writes `daemon.pid.lock`? Is it the daemon, or `niwa apply`? The existing daemon already has a `.niwa/daemon.pid` pattern but the new `daemon.pid.lock` is added without an owner. Phase 4 deliverables should name the file and its creation path.

**Medium — `niwa_finish_task` interaction with still-running supervisor.** The happy-path sequence (line 739) says worker calls `niwa_finish_task` → MCP server writes `state.json` terminal → worker exits → supervisor `cmd.Wait()` returns → central goroutine observes state == completed and "no action needed" (line 752). But what if the worker never exits (leaks, hung loop) after `niwa_finish_task`? The task is in a terminal state but a process is still running. Does the supervisor SIGTERM? The design does not say. Should `niwa_finish_task` signal the daemon to proactively reap?

**Medium — role enumeration for migration helper.** Phase 2 creates `.niwa/roles/<role>/inbox/...` "for every enumerated role" (line 882). The role list comes from `workspace.toml` (explicit `[channels.mesh.roles]`) plus auto-derived-per-repo (R5). On apply-2 after the user has added a repo, the installer creates the new role's inbox (AC-P9). Nothing in the design says what happens to an inbox directory whose role has been removed from `workspace.toml` (e.g., user deleted a repo mapping). Is it GC'd? Preserved indefinitely? This would be observable via `niwa task list` showing orphaned role context. Likely not v1 scope, but the Consequences should mention it.

**Low — `mode="sync"` awaitWaiter registration vs timeout.** Line 719 says "blocks in select on awaitCh and timeout (no sync timeout; bounded by R34/R36)." If there is no timeout, the `select` loop is just `<-awaitCh`. Why `select` at all? Presumably to also consume context.Done() from the MCP RPC context. This is worth stating.

**Low — migration helper enumeration of "N queued envelopes."** Line 516 says the stderr warning reports "N queued envelopes." To count them from the old layout, the helper walks `.niwa/sessions/<uuid>/inbox/` (old layout). Cheap but worth confirming the counting path.

**Low — per-repo `.mcp.json` identical to instance-root copy, or different?** Line 884 says "mirror into each `<repoDir>/.claude/.mcp.json`." Are these byte-identical? R4 suggests yes ("same MCP server"). If so, idempotency check reads straightforward. If the per-repo copy includes a different `NIWA_SESSION_ROLE` env hint, the design should say so.

## Missing Components or Interfaces

The design names five components (installer, daemon, worker, MCP server, filesystem) but a few interfaces are needed that cross those boundaries:

**High — message-type dispatcher is undefined.** The existing `notifyNewFile` already handles `reply_to` for `niwa_ask` (line 904: "existing `reply_to` path for ask remains"). It is being extended with task-message routing. A developer needs to know: is there a single central `type`-switch routine in `notifyNewFile`, or do individual handlers register for types? The Server struct adds `awaitWaiters` but does not name a registration API. Define a small interface like `RegisterTaskWaiter(taskID) (ch <-chan taskEvent, cancel func())` so the race-guard pattern (line 339) is mechanical.

**Medium — `TaskStore` package boundary.** Phase 1 promises a `taskstore` package with `OpenTaskLock`, `ReadState`, `WriteState` (line 871). But Decision 1's write-order (line 228) specifies a seven-step sequence (flock→read→validate→mutate→tmp→rename→fsync-parent→log→unlock). The package API should either expose this as `UpdateState(ctx, dir, mutator func(*TaskState) error, logEntry *TransitionEntry) error` — a single closure-style transaction — or the callers have to reimplement the sequence. Phase 3 handlers each do their own flock+write; they should call one helper. Name and sign this function in the Solution Architecture.

**Medium — the daemon's "catch-up inbox scan on startup" mechanism.** Line 296 and line 859 both mention a catch-up scan. How does it coordinate with fsnotify-in-flight events for the same inbox files? Is there a small race where both the scan and fsnotify fire on the same file? Most implementations deduplicate by the per-task lock — the second observer does the consumption rename and observes ENOENT. That is fine, but the design should state it so the developer does not add a de-dup set.

**Medium — the daemon's adopted-orphan liveness poll for `IsPIDAlive`.** Line 290 says the central loop polls orphans every 2s. But line 515 (PRD R37) allows intervals up to 5s. Why 2s? Worth stating: 2s is a compromise between responsiveness and wakeup cost. More importantly, the poll has to check both `IsPIDAlive(pid)` AND compare the live process's start_time against `state.json.worker.start_time` — a PID reuse on a busy system could otherwise misclassify a new, unrelated process as the orphan. This fine detail is critical for correctness on long-lived workspaces; it should be called out.

**Medium — `niwa_ask` → task creation interface.** Line 767-784 shows the ask flow creating a task with `body.kind="ask"`. The handler `handleAsk` does double duty: (a) if target role has a running worker, it is a peer message; (b) if not, it is a first-class task. How does `handleAsk` know which branch? Via the daemon? Via reading `state.json` of any running task? Likely simpler: create the ask-task always (a running worker will be spawned or an existing session will respond via peer messaging). The design does not say.

**Low — `niwa status` mesh line source.** Phase 5 deliverable: "add one-line mesh summary to status detail view per PRD R44" (line 937). The summary counts (queued, running, completed-last-24h, abandoned-last-24h) must be derived from `.niwa/tasks/*/state.json`. This is a full-filesystem walk. Performance-wise fine for v1; worth acknowledging.

**Low — no explicit interface for `niwa_delegate(mode="sync")` cancellation.** AC-D9 requires sync delegate returning `{status: "cancelled"}` when the delegator cancels before consumption. The path is clear (cancel returns cancelled; awaitCh receives cancelled event). But the spec does not say `niwa_cancel_task` is callable from within the same coordinator session that issued the sync delegate — presumably yes (same session, same role), but the sync delegate's caller is blocked in its own tool call, so by definition it cannot. A test that covers AC-D9 probably uses a second MCP client; the design should hint at how.

## Phase Sequencing

Eight phases are roughly well-sequenced but a few reorders and splits are warranted:

**High — Phase 3 (MCP server) depends on Phase 4 (daemon) for the full sync-delegate test path.** Phase 3 says "cancel-vs-claim race using the new daemon pause hooks" in unit tests (line 905). Pause hooks live in the daemon (Phase 4 line 923). A developer implementing Phase 3 cannot run those tests until Phase 4 is done. Either (a) move daemon pause-hook scaffolding into Phase 3 as a mock/stub, (b) explicitly acknowledge that Phase 3 unit tests exercise only the MCP path and the cancel-vs-claim integration comes in Phase 4, or (c) merge those specific tests into Phase 4. The cleanest fix: Phase 3's test list should explicitly say "claim-race tests deferred to Phase 4."

**High — Phase 2 (installer) writes `.claude/.mcp.json` but the `niwa mcp-serve` behavior rewrite is Phase 3.** The installer writes the mcp config that points at `niwa mcp-serve` (line 884). If apply is run with a Phase-2-only binary, the mcp server will load the old tool surface. That is OK for isolated development but creates a broken intermediate state on main. Since the PR will squash-merge, this is not a release concern but is a CI concern: Phase 2's functional tests (`channels_test.go`) must not exercise `niwa mcp-serve` end-to-end, only filesystem state. Currently the phase does not distinguish.

**Medium — Phase 4 is very large.** Phase 4 rewrites the daemon with: central loop structure, reconciliation, catch-up scan, consumption claim, per-task supervisor, cmd.Wait, stall watchdog, SIGTERM/SIGKILL, adopted-orphan polling, restart cap, backoff, exit classification, `NIWA_WORKER_SPAWN_COMMAND` honoring, and `NIWA_TEST_PAUSE_*` hooks (lines 912-924). That is ~12 sub-features. For "chunks that are too big for one commit" — this is the clearest candidate for a split:
  - Phase 4a: Central loop + claim path + supervisor + cmd.Wait (basic spawn→wait→classify).
  - Phase 4b: Restart cap + backoff + unexpected-exit classification.
  - Phase 4c: Stall watchdog + SIGTERM/SIGKILL escalation.
  - Phase 4d: Adopted-orphan polling + reconciliation-on-startup + catch-up scan.
  - Phase 4e: NIWA_WORKER_SPAWN_COMMAND + NIWA_TEST_PAUSE_* hooks.

Each sub-phase has its own test set. Splitting aids review and avoids a 2000-line diff.

**Medium — Phase 6 (test harness) depends on Phase 3 handlers AND Phase 4 daemon.** Phase 6's scripted fake connects as MCP client (line 944), so Phase 3 must be wired. Phase 6's `pauseDaemonAt` uses Phase 4 hooks. Currently Phase 6 is after 5 (CLI surface), which is fine, but the design could say explicitly that Phase 6 cannot proceed until 3 and 4 are both green.

**Medium — Phase 5 (CLI subcommands) depends on `state.json` schema which is in Phase 1, but its test paths exercise the full stack.** `niwa task list` reads all `state.json` files (line 934). If Phase 1 is done, Phase 5 can start; but `niwa task list` with actual running tasks requires Phase 4. Phase 5 should use test-fixture state.json files for unit testing, and full e2e coverage moves to Phase 6.

**Low — Phase 8 (documentation) at the end is fine but has a subtle dependency on every other phase's naming stability.** If a field name changes during Phase 4, the Phase 8 guide update has to track it. Not a sequencing problem — just flagging that deferring docs to Phase 8 risks doc drift during implementation.

**Low — no Phase for wip/ cleanup.** Per CLAUDE.md, `wip/` artifacts must be cleaned before PR merge. Phase 8 deletes old design files (line 969) but does not say "remove wip/ artifacts produced during the design phase." Not a design concern per se, just a reminder for the Phase-8 checklist.

## Overlooked Simpler Alternatives

The decisions explored sensible alternatives but a couple of simplifications deserve a second look:

**Medium — single global daemon.pid + single daemon-wide lock instead of per-task `.lock` for every claim.** Decision 1 rejected global `.niwa/tasks.lock` because it serializes unrelated tasks (line 259). True for a fully parallel design. But the daemon architecture (Decision 2) says the central goroutine is the only `state.json` writer for spawn decisions (line 281). If the daemon is already single-writer for the claim path, the per-task lock is only needed for MCP-server writes (progress, finish, update). A simpler scheme: per-task lock for worker/delegator-side writes, but claim path uses only the atomic rename itself as the serialization primitive (rename is atomic; either it succeeds or it observes ENOENT). This would remove the "write state.json with pid:0 under lock, release, spawn, re-acquire, backfill" three-step dance in Data Flow (lines 724-728) which currently creates an auth edge case (line 124 of the security review). Worth re-examining or explicitly defending the chosen complexity.

**Medium — event-sourced `transitions.log` as the single source with a cached `state.json`.** Decision 1 rejects this because "niwa_query_task would need log replay on every call." True, but the log is per-task and small (tens of entries max before a terminal state). A replay cost of a few hundred microseconds is negligible. The benefit is losing a whole class of "state.json and transitions.log diverged" bugs by construction. The decision says "deferrable to v2" — fine, but this is the single biggest structural simplification available and the v1 choice is a real complexity cost. The argument for v1 could be framed more strongly than "query would need replay" (which it would not at any perceptible cost).

**Medium — skip PPID-walk, rely on state.json.worker.start_time freshness + env TaskID match.** Decision 3 made PPID-walk mandatory on Linux. But the MCP server reads `NIWA_TASK_ID` from its startup env; the daemon set that env at worker-spawn time; the daemon also wrote `state.json.worker.start_time` at the same spawn. A same-UID attacker who wants to impersonate must either (a) write a new `state.json.worker.start_time` (blocked by the flock + validator that checks `from==expected` state), or (b) guess the task_id (UUIDv4, unguessable). PPID-walk adds depth-in-defense but the simpler check is nearly as strong and platform-independent. The design acknowledges this ("crypto token alternative retained as migration path", line 269) but the migration path has identical semantics to "just delete the PPID check." Worth considering removing it in v1 to simplify auth.go and avoid the whole macOS degradation narrative.

**Low — simpler migration: hard-fail with a clear error message instead of blind-rewrite.** Decision 7's chosen path emits a warning then nukes old state (line 516). The rejected "destroy-and-recreate" alternative is described as "worsens UX" (line 529). But for pre-1.0 users (the stated migration audience), a hard-fail with "run `niwa destroy && niwa create --channels`" is arguably clearer than silent data loss behind a stderr line. The design's choice is defensible but the UX argument cuts both ways.

## Consequences Honesty

The Consequences section is detailed but misses or understates a few negatives:

**High — no mention of the `state.json.worker.pid==0` race window.** Security review (line 124) notes that between steps 2-3 of the daemon's spawn sequence, a worker is alive but `state.json.worker.pid == 0`. Any authorizer call in that window rejects with `NOT_TASK_PARTY`. For a well-behaved worker doing `niwa_check_messages` as its first call (a non-task-authorized read), this is invisible. But for a scripted fake that calls `niwa_report_progress` very early, this is a race window. The Consequences should list this as a "minor startup-window constraint" or the Solution Architecture should reorder the spawn to backfill before exec.

**High — Consequences understates the `transitions.log` and `.niwa/tasks/` data retention risk.** Listed as "task dirs accumulate" (line 990), but the security review (lines 67-73) observed that long-lived workspaces accumulate every progress body, result body, and reason body in plaintext. For a year-old workspace, this could be gigabytes of sensitive LLM content. The negative understates both the storage scale and the privacy dimension. Should explicitly say: "v1 `transitions.log` and result bodies persist indefinitely; users who handle sensitive data should manually `rm -rf .niwa/tasks/<id>/` on completed tasks or wait for v2 gc."

**Medium — no negative for "no structural verification of completion."** The worker calls `niwa_finish_task(completed)` and niwa trusts it. A compromised/hallucinating worker can mark a task completed without doing the work (security review lines 89, 97). PRD Known Limitation ("Completion is a behavioral contract") is acknowledged, but this deserves a negative in the Consequences: "Niwa cannot detect a worker that marks a task complete without doing the work; completion is a behavioral contract only."

**Medium — no negative for "single-worker-per-role invariant limits parallelism."** The design commits to single-worker-per-role (line 168, PRD Out of Scope "multiple parallel workers per role"). This is a real UX limit: a role that has two independent tasks queued must run them sequentially. In a CI-like workflow this is often the bottleneck. Should be a named negative.

**Medium — no negative for the "MCP server per session" cost model.** Each Claude session spawns its own stdio MCP server (Decision 3). For a workspace with one coordinator and, say, five concurrently-running workers, that is six MCP server subprocesses. Each has its own fsnotify watch, its own waitersMu, its own JSON decoder loop. Process count is not huge, but for someone watching `htop` this is a visible footprint. Worth mentioning.

**Medium — "forward-compatible tool API" is overstated if the skill ships `niwa_wait`.** The Consequences list "forward-compatible tool API" (line 981). But `niwa_wait` in `allowed-tools` (line 429) is a tool that is not documented in the tool surface. If it is a legacy carry-over, the "forward compatible" claim is slightly weakened.

**Low — "No new runtime dependencies" claim should mention the Go version bump risk.** Not material, but the PPID-walking code and `PIDStartTime` rely on `/proc/<pid>/stat` parsing on Linux. Go stdlib has nothing to help here — the implementation is hand-rolled. A negative/trade-off: "hand-rolled platform-specific /proc parsing; maintenance burden when future kernels change `/proc/<pid>/stat` layout."

**Low — "Daemon doesn't survive machine restarts" (line 991) is listed but the recovery path leans on the user knowing to run `niwa apply`.** Could mention that a future `niwa mesh start` or user-level systemd unit is a clean v2 improvement.

## Specific Concerns

### Per-task `.lock` + atomic-rename + NDJSON log pattern across filesystems

**Robust on ext4, xfs, btrfs**: all support atomic rename on the same filesystem, all honor `fsync` on files and parent directories, all support advisory `flock`. ext4's default `data=ordered` preserves metadata order needed for "tmp exists before rename commits."

**tmpfs concerns**: tmpfs supports atomic rename and flock but has no persistence — this matters only if the instance root is placed under `/tmp` or similar. `fsync` is a no-op on tmpfs; that is fine because there is no durability requirement if the filesystem is itself ephemeral. The security review (line 194) already flags "shared tmp dirs" as a symlink-race concern; the design should note tmpfs-specifically that "instance roots on tmpfs trade durability for speed but remain correct as long as the daemon outlives the task."

**NFS / SMB / sshfs** (the truly worst case): `flock` semantics vary wildly, atomic rename may not be atomic, parent-directory fsync may be unreliable. The design does not say "local filesystems only" but effectively requires it. Worth an explicit note: "Instance root MUST reside on a local POSIX filesystem. Network filesystems are unsupported in v1."

**Cross-filesystem rename**: the consumption rename moves `inbox/<id>.json` → `inbox/in-progress/<id>.json`, both under `.niwa/roles/<role>/inbox/` — same filesystem by construction. OK. But if a user has a bind mount or zfs sub-volume inside `.niwa/`, this could break. Niche; a doc note suffices.

**Verdict on this concern:** Solid for the common case; needs one sentence in the design naming "local POSIX filesystem only."

### Central-goroutine + per-task-supervisor edge cases

**Rapid spawn+exit cycles (flapping worker).** A worker that exits within 50ms of spawn, four times in a row, hits the restart cap quickly and abandons. The 30s/60s/90s backoff means even a fast-failing worker takes 3+ minutes to abandon. During that time, the central goroutine processes four exit events and spawns four supervisors. No tight-loop risk. OK.

**Stuck flock holders.** If the MCP server process holding the flock on `.lock` hangs (never releases), the daemon's next state-transition attempt blocks indefinitely on `flock(LOCK_EX)`. This is a real concern: a hung MCP server (prompt-injection-driven infinite loop) could deadlock all operations on its task. Defense: `flock` with `LOCK_EX | LOCK_NB` in a short retry loop, then timeout with an error? The design does not say. On Linux, a hung process holding a flock is released only on process exit. If the worker's LLM hangs and the watchdog fires, the SIGKILL releases the lock. OK in the happy path, but if the daemon itself is what is hung holding the lock, there is no second watchdog. Worth a design note: "All flock acquisitions in the daemon use a 30s timeout with NOT-OK-on-timeout classification."

**SIGINT mid-spawn.** User hits Ctrl-C on `niwa apply`. What about the daemon that was just starting? The existing pattern uses `Setsid: true` (security review line 26), so the daemon detaches from the controlling terminal. SIGINT to the parent does not propagate to the daemon. OK. But what if the user sends SIGINT to the daemon directly (e.g., via `niwa destroy`)? The design says SIGTERM then SIGKILL after grace (line 523). No explicit mention of SIGINT; presumably treated the same as SIGTERM. Worth confirming in the destroy code path.

**Signal race: SIGKILL daemon while mid-spawn.** If the daemon is SIGKILL'd between writing `state.json` (task=running, pid=0) and exec'ing the worker, the task is in "running" with no worker. Next daemon startup sees this: `state.json.worker.pid == 0` should be classifiable as "spawn never completed, retry." Decision 2 line 292 hints at this: "PID field unset or dead; spawn_started_at present" triggers "allocates a fresh retry." Good. The design handles it; could be more explicit in Data Flow.

**Verdict on this concern:** Mostly handled. Add an explicit "flock acquisition uses timeout" note; clarify SIGINT behavior.

### Per-session stdio MCP server + awaitWaiters map

**Timeout races with notifications.** A delegator calls `niwa_await_task(task_id, timeout_seconds=2)`. The server registers awaitWaiters[T]. At T+1.9s, the worker calls finish_task (writes state.json, writes inbox message). At T+2.0s, the timeout fires. The inbox file arrives at T+2.05s via fsnotify. What wins?

Per the design (line 339): "register with `defer cancel()` before checking `state.json` for an already-terminal state (race guard mirroring `handleAsk`)." Good. But the symmetric race — waiter fires AND fsnotify fires simultaneously — needs the chan receive pattern to be idempotent. If awaitCh is size-1 buffered, both sender and receiver can execute once; if unbuffered, the second send blocks forever. The design does not specify channel buffer size.

**Cleanup:** `defer cancel()` presumably removes the waiter entry from `awaitWaiters`. Under racy timeout+notification, the cancel might run AFTER notifyNewFile has already dispatched to the channel — but the channel is no-longer-referenced by the map, so the goroutine reading it exits cleanly. OK if the waiter closure stores the channel locally before `defer`. Worth reviewing the implementation pattern during Phase 3.

**Process death mid-await.** If the MCP server process dies (OOM, SIGKILL) while holding an awaitWaiter, the delegator's tool call is simply terminated by Claude Code on its next check. No cleanup needed — the whole process is gone. OK.

**Two-delegators wait on same task.** Should not happen in normal flow (one delegator per task), but if it did, the second registration would replace the first in the map — the first never gets its event. The design should either (a) guarantee one awaiter per task by authorization (delegator is single) or (b) use a fan-out channel / multi-subscriber pattern. Delegator is role-based, and only one session per role, so this is impossible. Worth a one-liner to confirm.

**Verdict on this concern:** Race-guard pattern is correct in principle but channel buffering and multi-waiter semantics need stating.

### Migration helper idempotency

**Safe against re-running apply repeatedly?** The helper's first step (line 515) is to detect old layout: "if `.niwa/sessions/` contains any `<uuid>/` subdirectory AND `.niwa/roles/` is absent." After a successful first migration, `.niwa/roles/` exists (newly created), so the second apply does NOT hit the migration path. The detection condition naturally converges. Idempotent. 

**Edge case 1: partial first migration.** If the first apply crashes after removing `.niwa/sessions/<uuid>/` but before creating `.niwa/roles/`, the next apply will re-trigger migration (roles still absent) but have nothing to remove. The helper must tolerate "old layout already absent." Step 3 says "recursively remove `.niwa/sessions/<uuid>/` directories" — if none exist, the loop iterates zero times. OK as long as the implementation uses `for each dir in glob(...)` not `require at least one`.

**Edge case 2: user manually creates `.niwa/roles/` with no sessions.** Detection condition treats this as "not an upgrade" (roles present). Correct.

**Edge case 3: user has both old `.niwa/sessions/<uuid>/` AND new `.niwa/roles/` (torn upgrade from a botched partial).** Detection says "not an upgrade" (roles exist). Old dirs persist as clutter. The helper does not clean them up. Unclear intent. Low severity since this state is reachable only through manual mucking. Worth one line: "If both layouts coexist, the helper does nothing; user must manually clean up remnants."

**Edge case 4: stderr warning "N queued envelopes" on every apply after a botched migration.** Since the helper only runs on detection, the warning only fires once (the first full migration). After migration, the condition is false. Good.

**Verdict on this concern:** The migration helper is idempotent by virtue of its detection condition; the partial-state edge cases are benign.

### Other specific concerns

**Task-ID generation.** The security review (line 247) notes: "design should explicitly commit to UUIDv4." The design uses `<uuid>` throughout but never says UUIDv4 specifically. A `crypto/rand` UUIDv4 is the right call. Phase 1 deliverable should name it.

**Role enumeration order.** `.niwa/roles/<role>/inbox/` is created for every known role. In what order? Sorted alphabetically? In workspace.toml order? For idempotent apply (AC-P9, AC-P10), order must be deterministic to avoid ManagedFiles churn.

**Daemon log verbosity.** Security review (lines 73-75) recommends: "log state transitions, spawn/exit events, spawn-binary paths, but not envelope or result bodies." The Security Considerations section in the design (line 1061) captures this. Implementation Phase 4 should add a log-format spec.

## Summary

The design is structurally sound and the seven Considered-Options decisions are well-defended; most of the Solution Architecture, Implementation Approach, and Consequences read like natural consequences of those decisions. A NEEDS_REVISION verdict is warranted because (1) four interfaces need naming before Phase 1 can start cleanly (taskEvent type, TaskStore transactional API, per-role MCP fsnotify watcher, PPIDChain depth), (2) Phase 4 should be split into 4-5 smaller commits given its scope, and (3) three negatives are missing from the Consequences (spawn PID=0 race window, no structural completion verification, sequential per-role execution limit). None of these are structural; all are edits to the existing sections that sharpen the hand-off to implementers. The specific edge-case concerns (filesystem portability, flock timeouts, waiter channel buffering, migration idempotency) are mostly handled by the existing design but a few one-line clarifications would lock them in.
