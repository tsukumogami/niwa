# Coordinator Loop Flows

Reference for the state machine governing coordinator-worker delegation in niwa.
Covers the seven flows a reader needs to understand system behavior end-to-end.

## Implementation status note

The stop hook and resume-with-reminder path (Flows 2 and 3) are described as
**designed but not yet implemented** — they are the outcome of DESIGN-coordinator-loop.md
(status: Proposed). The current codebase (`internal/cli/mesh_watch.go`) only performs
fresh spawns on unexpected exit; `TaskState.Worker.ClaudeSessionID`, `Worker.ResumeCount`,
and `TaskState.MaxResumes` do not yet exist in `types.go`. The flows below describe
the designed behavior. Discrepancies from current code are flagged inline.

---

## State machine quick reference

```
Task lifecycle states (TaskState.State):
  queued  →  running  →  completed
                      →  abandoned
                      →  cancelled   (delegator-initiated only)

Restart cycle (daemon-internal):
  unexpected exit → retrySpawn
    if Worker.ResumeCount < MaxResumes AND ClaudeSessionID present AND session file valid:
      → resume spawn (Worker.ResumeCount++)
    else:
      → fresh spawn  (RestartCount++, Worker.ResumeCount=0, ClaudeSessionID="")
    if RestartCount >= MaxRestarts:
      → abandoned (reason: retry_cap_exceeded)
```

---

## Flow 1: Happy path — worker completes successfully

**Scenario:** Coordinator delegates a task, worker boots, does its work, and calls
`niwa_finish_task`. Coordinator's `niwa_await_task` returns `completed`.

```
1. COORDINATOR calls niwa_delegate(to="worker-role", body={...})
   → MCP server: createTaskEnvelope
       - writes .niwa/tasks/<id>/envelope.json
       - writes .niwa/tasks/<id>/state.json  [State: queued, MaxRestarts: 3]
       - atomic-renames task.delegate msg → .niwa/roles/worker-role/inbox/<id>.json
   → returns {task_id: "<id>"}

2. COORDINATOR calls niwa_await_task(task_id="<id>", timeout_seconds=600)
   → maybeRegisterCoordinator(): writes sessions.json entry (PID + start_time)
   → registers awaitWaiter[task_id] and questionWaiter[coordinator]
   → race-guard read: state still queued → blocks on select

3. DAEMON (mesh watch): fsnotify fires Create on inbox/<id>.json
   → daemonOwnsInboxFile: type=="task.delegate" → true
   → handleInboxEvent:
       - UpdateState: queued → running [Worker.Role, Worker.SpawnStartedAt set]
       - renames inbox/<id>.json → inbox/in-progress/<id>.json
   → spawnWorker: exec.Command("claude", "-p", "You are a worker for niwa task <id>. Call niwa_check_messages.")
       - Worker env: NIWA_INSTANCE_ROOT, NIWA_SESSION_ROLE=worker-role, NIWA_TASK_ID=<id>
       - Setsid: true (detached process group)
       - cmd.Start() → pid = <worker-pid>
       - UpdateState: backfills Worker.PID, Worker.StartTime
       - starts supervisor goroutine (cmd.Wait)
       - starts watchdog goroutine (stall polling at 2s intervals)

4. WORKER boots, calls niwa_check_messages
   → [DESIGNED] MCP server reads $CLAUDE_SESSION_ID from env, writes to
     TaskState.Worker.ClaudeSessionID via UpdateState
     [CURRENT: not implemented — ClaudeSessionID field does not exist]
   → reads inbox/in-progress/<id>.json (envelope)
   → returns task body wrapped in _niwa_task_body/_niwa_note envelope

5. STOP HOOK fires at each Claude Code turn boundary
   → [DESIGNED] niwa mesh report-progress --task-id $NIWA_TASK_ID
       → reads state.json (env var trust anchor: NIWA_SESSION_ROLE matches worker.role)
       → UpdateState: writes LastProgress.At = now
       → watchdog: detects LastProgress.At advance → resets stallTimer
     [CURRENT: not implemented — stop hook and CLI subcommand do not exist]

6. WORKER does work, calls niwa_report_progress(task_id, summary) [optional]
   → UpdateState: LastProgress = {summary, at: now}
   → delivers task.progress message to coordinator inbox (best-effort)
   → watchdog: sees LastProgress.At advance → resets stallTimer

7. WORKER calls niwa_finish_task(task_id="<id>", outcome="completed", result={...})
   → authorizeFinishTask: kindExecutor check passes (caller PPID + start_time)
   → UpdateState: running → completed [Result set, StateTransitions updated]
   → delivers task.completed msg → .niwa/roles/coordinator/inbox/<msg-id>.json

8. COORDINATOR's MCP server watchRoleInbox: fsnotify Create on task.completed msg
   → taskTerminalKind("task.completed") → EvtCompleted
   → awaitWaiter[task_id] receives taskEvent{Kind: EvtCompleted}
   → moves msg to inbox/read/

9. COORDINATOR's niwa_await_task unblocks
   → re-reads state.json for authoritative payload
   → returns {status: "completed", task_id, result: {...}, restart_count: 0}

10. SUPERVISOR goroutine: cmd.Wait() returns (worker exited cleanly)
    → exitCh ← supervisorExit{taskID, exitCode: 0}
    → handleSupervisorExit: state == completed → log "action=none", return
    → WATCHDOG: waitDone closes → watchdog goroutine exits
```

**State changes:** `queued → running → completed`

---

## Flow 2: Stall kill → resume-with-reminder

**Scenario:** Worker is in the middle of a long single tool call (> 900s). The stop hook
cannot fire during a blocking tool call. Watchdog fires. Session ID is present.
Resume path is taken.

**Note:** This entire flow is designed but not yet implemented. See implementation
status note above.

```
1. Steps 1–4 of Flow 1: task is running, Worker.ClaudeSessionID = "<session-id>"

2. WORKER executes a long-running tool call (> 900s elapsed since last LastProgress.At)
   Stop hook cannot fire: Claude Code fires stop hooks at turn boundaries, not during
   tool execution. LastProgress.At does not advance.

3. WATCHDOG: stallTimer fires (900s elapsed, no LastProgress.At advance)
   → watchdogAlreadyFired = true
   → escalateSignals(trigger="stall"):
       - AppendAuditEntry: "watchdog_signal" SIGTERM to transitions.log
       - syscall.Kill(-pid, SIGTERM)  [sends to entire process group]
       - waits up to sigTermGrace (5s) on waitDone channel
       - if not exited: AppendAuditEntry "watchdog_signal" SIGKILL
       - syscall.Kill(-pid, SIGKILL)

4. SUPERVISOR goroutine: cmd.Wait() returns after SIGTERM/SIGKILL
   → close(waitDone) → watchdog exits
   → exitCh ← supervisorExit{taskID, exitCode: -1 or 137}

5. DAEMON central loop: handleSupervisorExit
   → ReadState: state == running (worker was killed before niwa_finish_task)
   → nextAttempt = RestartCount + 1 = 1
   → 1 <= MaxRestarts (3) → within cap
   → logs "unexpected_exit" to transitions.log (no state change)
   → logs "retry_scheduled" to transitions.log
   → schedules retrySpawn after backoff (default 30s for attempt 1)

6. BACKOFF TIMER fires → retrySpawn(taskID, role, s):
   → captures cur.Worker.ClaudeSessionID = "<session-id>" before overwriting Worker
   → UpdateState:
       - Worker.SpawnStartedAt = now
       - Worker.PID = 0, Worker.StartTime = 0  [zeroed; fresh cmd.Start below backfills]
       (RestartCount NOT incremented — this is a resume, not a fresh spawn)
   → checks Worker.ClaudeSessionID: "<session-id>" present
   → checks Worker.ResumeCount (0) < MaxResumes (2) → resume eligible
   → session file integrity check: ~/.claude/projects/<base64url-cwd>/<session-id>.jsonl
       - must exist, be non-empty, last complete line must parse as valid JSON
   → integrity check passes → spawnWorker(resumeMode=true):
       cmd = "claude --resume <session-id> -p '<reminder>' --permission-mode=... --mcp-config=..."
       reminder: "You were stopped by the stall watchdog. The workspace stop hook resets
                  the watchdog automatically at every turn boundary — you do not need to
                  call niwa_report_progress manually. Do not call niwa_check_messages
                  again — your task envelope is already in your conversation history.
                  Continue your work from where you left off."
   → Worker.ResumeCount++ = 1 (RestartCount stays 0)
   → next.Worker.ClaudeSessionID = captured "<session-id>" (preserved for next resume)
   → backfills Worker.PID, Worker.StartTime after cmd.Start
   → restarts supervisor + watchdog goroutines

7. WORKER session resumes with full conversation history + injected reminder
   → MCP server re-reads $CLAUDE_SESSION_ID → updates TaskState.Worker.ClaudeSessionID
     (same value; idempotent)
   → worker continues from where it left off

8. Normal completion: Flow 1 steps 7–10
```

**State changes:** `running` stays `running` through the kill cycle (state is only
transitioned by the worker calling `niwa_finish_task` or the daemon exhausting retries).

**Edge case:** If the worker called `niwa_finish_task` just before the SIGTERM landed,
`handleSupervisorExit` sees state == `completed` and returns without scheduling a retry.
The watchdog's `AppendAuditEntry` for the signals still runs successfully because
`AppendAuditEntry` is permitted on terminal state (unlike `UpdateState`).

---

## Flow 3: Stall kill → resume cap exhausted → fresh spawn → max restarts

**Scenario:** Worker stalls repeatedly. Resume cap (MaxResumes = 2) is exhausted on
the third stall. Daemon falls back to fresh spawn. Eventually RestartCount hits
MaxRestarts → task abandoned.

**Note:** ClaudeSessionID / ResumeCount / MaxResumes are designed but not yet
implemented. Fresh-spawn retry on unexpected exit IS implemented today.

**RestartCount rule (authoritative):** `RestartCount` increments only on fresh-spawn
transitions, not on resume attempts. `ResumeCount` counts resume attempts within the
current fresh-spawn cycle and resets to 0 each time a fresh spawn occurs. The
`MaxRestarts` budget is consumed only by fresh spawns; `MaxResumes` caps resume
attempts per fresh-spawn cycle.

```
Initial state: RestartCount=0, ResumeCount=0, ClaudeSessionID="<session-id>"

Stall kill #1 (handleSupervisorExit: nextAttempt=0+1=1 <= MaxRestarts=3 → schedule retry):
  retrySpawn: ClaudeSessionID present, ResumeCount(0) < MaxResumes(2) → resume
  → claude --resume <session-id> -p "<reminder>"
  → Worker.ResumeCount++ = 1  (RestartCount stays 0)

Stall kill #2 (handleSupervisorExit: nextAttempt=0+1=1 <= 3 → schedule retry):
  retrySpawn: ClaudeSessionID present, ResumeCount(1) < MaxResumes(2) → resume
  → claude --resume <session-id> -p "<reminder>"
  → Worker.ResumeCount++ = 2  (RestartCount stays 0)

Stall kill #3 (handleSupervisorExit: nextAttempt=0+1=1 <= 3 → schedule retry):
  retrySpawn: ResumeCount(2) >= MaxResumes(2) → resume cap exhausted
  → fresh spawn path:
      Worker.ClaudeSessionID = ""  [zeroed]
      Worker.ResumeCount = 0       [reset]
      RestartCount++ = 1           [only fresh spawns increment this]
      → claude -p "<bootstrap>"  (new session, no prior context)
  MCP server at startup: registers new ClaudeSessionID for the new session

Stall kill #4 (handleSupervisorExit: nextAttempt=1+1=2 <= 3 → schedule retry):
  retrySpawn: new ClaudeSessionID present, ResumeCount(0) < MaxResumes(2) → resume
  → cycle repeats: up to 2 resumes (RestartCount stays 1)

Stall kill #6 (after 2 more resumes):
  retrySpawn: resume cap exhausted again → fresh spawn → RestartCount++ = 2

Stall kill #9 (after 2 more resumes):
  retrySpawn: fresh spawn → RestartCount++ = 3

Stall kill #10 (handleSupervisorExit: nextAttempt=3+1=4 > MaxRestarts=3 → abandon):
  UpdateState: running → abandoned [Reason: {error:"retry_cap_exceeded",
                                     restart_count:3, max_restarts:3}]
  deliverAbandonedMessage → .niwa/roles/coordinator/inbox/<msg-id>.json

COORDINATOR's niwa_await_task unblocks:
  → awaitWaiter receives EvtAbandoned
  → re-reads state.json
  → returns {status: "abandoned", reason: {error:"retry_cap_exceeded",...},
             restart_count: 3, max_restarts: 3, last_progress: {...}}
```

**State changes:** `running → abandoned`

---

## Flow 4: Worker completes task but never calls niwa_finish_task

**Scenario:** Worker finishes its work but the process exits without calling
`niwa_finish_task` (crash, bug, or agent error). The coordinator is blocked on
`niwa_await_task`.

```
1. Worker process exits (exit code 0 or non-zero) without calling niwa_finish_task.
   Task state is still "running" in state.json.

2. SUPERVISOR goroutine: cmd.Wait() returns
   → close(waitDone) → watchdog exits
   → exitCh ← supervisorExit{taskID, exitCode: N}

3. DAEMON: handleSupervisorExit
   → ReadState: state == running (not terminal)
   → classifies as unexpected exit (state is running regardless of exit code)
   → schedules retry or abandons based on RestartCount vs MaxRestarts
   (same path as Flow 3 — the daemon cannot distinguish "finished its work" from
    "crashed midway through")

4. On retry: daemon spawns a fresh worker (or resumes if designed path is active).
   The new worker calls niwa_check_messages and receives the SAME task envelope.
   It may duplicate work or detect prior filesystem state and call niwa_finish_task.

5. If MaxRestarts exhausted without niwa_finish_task being called:
   → task abandoned with reason retry_cap_exceeded
   → coordinator's niwa_await_task returns {status: "abandoned", ...}
```

**What the coordinator sees:** `niwa_await_task` blocks until either (a) a retry
eventually calls `niwa_finish_task`, or (b) RestartCount >= MaxRestarts and the daemon
abandons the task. The coordinator has no way to distinguish "worker is retrying" from
"worker is working normally" — both look like "running" to niwa_query_task. The
`restart_count` field in the niwa_await_task terminal response tells the coordinator
how many retries occurred.

**Edge case:** If the worker's process exits and state.json is simultaneously
transitioning (e.g., worker called niwa_finish_task, the state write committed, but
the worker exited before the MCP server delivered the inbox message), handleSupervisorExit
sees state == completed and returns without retrying. The coordinator's awaitWaiter
is unblocked by the task.completed inbox message delivered by handleFinishTask.

**Open question:** What should the coordinator do when it receives `status: "abandoned"`
with `reason.error == "retry_cap_exceeded"` and `restart_count > 0`? The system
does not surface whether the retries made partial progress or whether the task is
safe to re-delegate from scratch. The coordinator must inspect application-level state
(e.g., files written to the repo) to determine this. There is no niwa-level
"task partially completed" signal.

---

## Flow 5: Coordinator re-delegates to same role after task completes

**Scenario:** A first task completes (or is abandoned). The coordinator calls
`niwa_delegate` again for the same role. What happens to the session? Fresh spawn
or resume?

```
1. First task: completed or abandoned. Worker process has exited.
   The task directory .niwa/tasks/<id1>/ remains with terminal state.

2. COORDINATOR calls niwa_delegate(to="worker-role", body={new work})
   → createTaskEnvelope: new task ID <id2>
       - .niwa/tasks/<id2>/state.json [State: queued]
       - .niwa/roles/worker-role/inbox/<id2>.json

3. DAEMON: detects new inbox file via fsnotify
   → handleInboxEvent: claim → running
   → spawnWorker: always fresh spawn ("claude -p '<bootstrap for id2>'")
     [DESIGNED: no resume path here — resume only applies to stall kills of the
      same task. A new niwa_delegate is a new task with a new task ID.
      The prior session's ClaudeSessionID is stored in <id1>'s state.json and
      is not consulted for <id2>.]

4. NEW WORKER boots. No connection to the prior session.
   Fresh Claude Code process. Prior conversation history is not available.
   Worker reads task body from <id2> envelope.

5. Normal completion: Flow 1 steps 7–10 for task <id2>.
```

**State carried over:** None. Each `niwa_delegate` creates an independent task with
its own state.json, its own task directory, and its own worker process. There is no
"session pool" or "role session" concept — a role is just a directory name that scopes
the inbox and the target for spawning.

**What "same role" means:** The daemon spawns a worker for the role named in the
task's `to` field. Whether that role previously had a running worker is irrelevant
— those workers have exited, and their state.json is terminal. The new task gets a
new spawn.

---

## Flow 6: niwa_ask to a terminated coordinator

**Scenario:** Worker calls `niwa_ask` targeting the coordinator role. The coordinator's
session has already ended (its task completed, or its Claude Code process exited).

```
Current behavior (pre-design):
  Worker calls niwa_ask(to="coordinator", body={question})
  → handleAsk: lookupLiveCoordinator → checks sessions.json
  → entry for coordinator exists but IsPIDAlive(entry.PID, entry.StartTime) == false
    → lookupLiveCoordinator: prunes stale entry, returns ("", false)
  → liveCoord == false → ephemeral spawn path:
      createTaskEnvelope writes task.delegate to coordinator inbox
      → daemon claims and spawns an ephemeral coordinator worker
      → ephemeral worker receives the ask body (question) as its task
      → ephemeral worker calls niwa_finish_task with some answer
      → worker's niwa_ask unblocks with that answer
  Problem: the caller gets a response from an ephemeral process that has no context,
  not from the actual coordinator. The response is confusing and may be wrong.

Designed behavior (DESIGN-coordinator-loop.md Decision 3):
  Worker calls niwa_ask(to="coordinator", body={question})
  → handleAsk: lookupLiveCoordinator → ("", false)
  → returns immediately (before creating any task store entry):
      {status: "no_live_session", role: "coordinator",
       message: "No live session found for role 'coordinator'. The role may have
                 completed its task or not yet started."}
  [CURRENT: NOT implemented. Current code falls through to ephemeral spawn path.]

What the worker should do on "no_live_session":
  The worker receives this synchronously from niwa_ask. Appropriate responses:
  - Log the situation and proceed without an answer (if the question was optional).
  - Call niwa_finish_task(outcome="abandoned", reason={...}) and halt work.
  - Write a file to the repo for the coordinator to inspect post-hoc.
  The worker MUST NOT retry niwa_ask in a tight loop — no_live_session is a
  definitive routing failure, not a transient condition.
```

**Open question:** The design specifies `no_live_session` is returned "before creating
any ask task store entry." This means there is no task_id in the response body. The
worker cannot query the ask status or detect if the coordinator comes back online and
reads a message. If the worker needs an answer and the coordinator may restart, there
is no built-in polling mechanism — the worker must design around this at the
application level.

**Open question:** The design only specifies the worker→coordinator direction. What
happens when a coordinator calls `niwa_ask` targeting a worker role that has no
live session? `lookupLiveCoordinator` only looks up the coordinator role. Worker
sessions are not registered in sessions.json (PRD R39/R40). So for coordinator→worker
`niwa_ask` the live-session check does not apply and the current code falls through
to the ephemeral spawn path regardless. See Flow 7 for the live-worker case.

---

## Flow 7: Coordinator asks worker a question mid-task

**Scenario:** Coordinator calls `niwa_ask` targeting a worker role while that worker
is actively running.

```
1. COORDINATOR calls niwa_ask(to="worker-role", body={question})
   → handleAsk: lookupLiveCoordinator only checks for coordinator sessions.
     Worker sessions are not in sessions.json (PRD R39/R40).
   → liveCoord == false (niwa_ask only does live-session routing to coordinator)
   → [CURRENT] falls through to ephemeral spawn path:
       createTaskEnvelope writes task.delegate to worker-role inbox
       → DAEMON claims, spawns a NEW ephemeral worker for the same role
       → that ephemeral worker receives the question body as its task
       → ephemeral worker calls niwa_finish_task with an answer
       → coordinator's niwa_ask waiter unblocks with that answer

   [ASSUMPTION / OPEN QUESTION: The live-session routing in handleAsk (lookupLiveCoordinator)
    is coordinator-specific by name. For coordinator→worker asks, "live session" lookup
    would need a different lookup path. The design doc does not address coordinator→worker
    asks — only worker→coordinator. Whether a coordinator asking a worker goes through
    live-session routing or always spawns ephemerally is not specified in the design.]

Designed behavior for worker→coordinator asks (Flow 6 live path):
  If the coordinator IS running (live session registered), handleAsk:
  → createAskTaskStore (no task.delegate → daemon does NOT spawn)
  → writeAskNotification → .niwa/roles/coordinator/inbox/<msg-id>.json (type: "task.ask")
  → registers awaitWaiter[ask_task_id]
  → blocks on select (timeout = 600s default)

  COORDINATOR's niwa_await_task (already blocking on its own task):
  → questionWaiter fires: the fsnotify watcher sees task.ask, sends questionEvent
  → niwa_await_task returns early:
      {status: "question_pending", ask_task_id: "<ask-id>",
       from_role: "worker-role", body: {ask_task_id, from_role, _niwa_note, question}}

  COORDINATOR reads the question, calls niwa_finish_task(task_id="<ask-id>",
                                                         outcome="completed", result={answer})
  → UpdateState: queued → completed on the ask task
  → delivers task.completed to worker-role inbox

  WORKER's niwa_ask awaitWaiter fires: returns {status: "completed", result: {answer}}

  COORDINATOR calls niwa_await_task again to re-register its question waiter
  and resume waiting for its own delegated task.
```

**Note on coordinator's state machine during a question:** When a coordinator
receives `question_pending` from `niwa_await_task`, its own task's `awaitWaiter` is
cancelled (the `defer cancelAwait()` runs). The coordinator must call `niwa_await_task`
again after answering to re-register. This is idempotent — `awaitWaiter` re-registration
with the same task_id works correctly because `niwa_await_task` does a race-guard
read before blocking.

**Open question:** If the coordinator answers a question but there are multiple
questions pending simultaneously (two workers each called `niwa_ask`), only one
`questionEvent` is delivered per `niwa_await_task` call (the implementation sends at
most one catch-up question per `scanInboxForQuestions`). The coordinator must call
`niwa_await_task` multiple times to drain all pending questions. There is no batch
question delivery. Whether this is the intended protocol is not explicitly stated in
the design.

**Open question:** The `niwa_await_task` `questionWaiters` map is keyed by role, not
by task_id. Only one question waiter can be registered per coordinator role at a time
(a new registration replaces the previous channel). If the coordinator has delegated
multiple tasks to multiple workers and multiple workers ask questions concurrently,
only the most-recently-registered coordinator waiter receives the question notification.
Questions destined for a coordinator with no waiter stay in the inbox file until the
next `niwa_await_task` call triggers a catch-up scan. This may be fine in practice
but is worth documenting explicitly.

---

## Flow A: Daemon restart mid-task

**Scenario:** The niwa daemon process (`niwa mesh watch`) is killed or crashes while
a worker task is running. The daemon is restarted. What happens to the in-progress task?

```
Initial state: Task is running. Worker process is alive (PID=<worker-pid>).
state.json: {state: "running", worker: {pid: <worker-pid>, ...}}

1. DAEMON process exits (SIGTERM from OS, crash, or user restart).
   Worker process is NOT killed — it is detached (Setsid: true) and continues running.
   The supervisor goroutine and watchdog goroutine die with the daemon.

2. DAEMON restarts: `niwa mesh watch`
   → reads workspace config, re-registers fsnotify watchers on all role inboxes
   → does NOT re-scan existing in-progress tasks by default
     [CURRENT: daemon restart does not adopt orphan workers. The watchdog and
      supervisor goroutines are not recreated for workers already running.]

3. WORKER continues running independently (no daemon supervision).
   - The stop hook still fires at every turn boundary (it's a Claude Code workspace
     hook, not daemon-dependent). report-progress.sh updates last_progress.at in
     state.json — the write succeeds because it goes directly to the filesystem.
   - The worker can still call niwa_finish_task (the MCP server is a separate process
     started by Claude Code, not by the daemon).

4a. WORKER calls niwa_finish_task before daemon re-adopts:
    → UpdateState: running → completed
    → delivers task.completed to coordinator inbox
    → COORDINATOR's niwa_await_task unblocks normally (Flow 1 steps 8–9)
    → state.json has terminal state; no further daemon action needed

4b. WORKER process exits (crash or natural exit) before daemon re-adopts:
    → No supervisor goroutine is watching → no retry is scheduled
    → Task remains in "running" state in state.json indefinitely
    → Coordinator's niwa_await_task blocks until its timeout
    → [GAP: The daemon currently has no mechanism to scan state.json files on
       restart and re-adopt orphan workers. This is a known gap — not addressed
       by DESIGN-coordinator-loop.md. A future design would need a startup sweep
       that: finds tasks in "running" state, checks whether the worker PID is
       still alive, and either re-attaches a supervisor goroutine or schedules
       retrySpawn for dead workers.]

5. If daemon restarts and coordinator calls niwa_await_task:
   → maybeRegisterCoordinator re-registers the coordinator session (new PID)
   → awaitWaiter registered for the task
   → if state is already terminal (4a): race-guard read returns immediately
   → if state is still running (4b): blocks until timeout, then returns {status: "timeout"}
```

**What the coordinator sees:** If the worker completes normally (4a), the coordinator's
`niwa_await_task` returns completed as usual. If the worker dies while the daemon is
down (4b), the coordinator eventually gets `{status: "timeout"}` on the next
`niwa_await_task` call.

**Gap:** Daemon restart does not restart the watchdog for orphan workers. A worker
that is alive but stalled while the daemon is down will not be killed and retried;
it will run until it naturally exits or calls `niwa_finish_task`.

---

## Flow B: niwa_cancel_task on a live worker

**Scenario:** The coordinator calls `niwa_cancel_task` to cancel a task while the
worker process is actively running.

```
1. COORDINATOR calls niwa_cancel_task(task_id="<id>")
   → handleCancelTask: authorizeTaskCall (coordinator must hold delegator token)
   → ReadState: state == running
   → UpdateState: running → cancelled
   → delivers task.cancelled msg → .niwa/roles/worker-role/inbox/<msg-id>.json
   → returns {status: "cancelled"}

2. [CURRENT BEHAVIOR — GAP] The daemon is NOT notified of the cancellation directly.
   The worker process continues running. The daemon's watchdog continues polling
   last_progress.at. The stop hook continues firing.

3. WORKER's MCP server: fsnotify watcher on worker-role inbox fires on task.cancelled
   → the MCP server delivers the message as a result of the worker's next
     niwa_check_messages or niwa_await_task call
   → the worker receives {type: "task.cancelled", ...} and is expected to stop
   → this is advisory: a worker that ignores the message continues running

4. SUPERVISOR goroutine: eventually cmd.Wait() returns when the worker exits
   → exitCh ← supervisorExit{taskID, exitCode}
   → handleSupervisorExit: ReadState → state == cancelled (terminal)
   → logs "action=none" (terminal state; no retry)
   → watchdog exits when waitDone closes

5. [GAP] If the worker ignores the cancellation message and keeps running:
   → The task state is "cancelled" in state.json
   → The coordinator receives {status: "cancelled"} from niwa_cancel_task
   → But the worker process is still alive, consuming resources
   → The daemon's watchdog will NOT kill it — handleSupervisorExit on watchdog
     signal checks state, sees cancelled, takes no action
   → [DESIGN-coordinator-loop.md does not address this gap. A future fix would
      have handleCancelTask signal the daemon to send SIGTERM to the worker PID
      immediately, rather than waiting for the worker to self-terminate.]
```

**What the coordinator sees:** `niwa_cancel_task` returns `{status: "cancelled"}`
immediately. The coordinator can proceed. The worker may still be running if it ignores
or hasn't yet received the cancellation message.

**Gap:** `niwa_cancel_task` does not kill the worker process. It only updates state and
delivers a message. A non-cooperative worker continues running until the watchdog
triggers a stall kill — which, after cancellation, checks state and does nothing.
Workers that don't poll their inbox after cancellation run until they exit naturally.

---

## Flow C: spawnWorker spawn failure

**Scenario:** `spawnWorker` is called (fresh spawn or resume) but `cmd.Start()` fails
— the `claude` binary is not found, is not executable, or the OS rejects the exec.

```
1. DAEMON: retrySpawn calls spawnWorker(...)
   → exec.Command("claude", ...) builds the command
   → cmd.Start() returns error (ENOENT, EACCES, etc.)

2. [CURRENT BEHAVIOR — GAP] spawnWorker returns the error to retrySpawn.
   retrySpawn currently does not handle spawn failure explicitly.

3. The task remains in "running" state in state.json.
   No supervisor goroutine is started (cmd.Start failed).
   No watchdog goroutine is started.
   No retry is scheduled.

4. [GAP] The coordinator's niwa_await_task blocks until its timeout.
   It eventually returns {status: "timeout"}.
   The coordinator has no signal that the spawn itself failed — it looks the same
   as a task that is running slowly.

5. [GAP] The task directory remains with state "running" indefinitely.
   If the daemon is restarted, it does not re-scan for this orphan (same gap as Flow A).

Expected behavior (not yet implemented):
   → spawnWorker returns error → retrySpawn detects spawn failure
   → logs "spawn_failed" to transitions.log
   → if spawn is the initial spawn (RestartCount == 0): task should be abandoned
     immediately with reason {error: "spawn_failed"}
   → if spawn is a retry: treat the same as an unexpected exit — schedule another
     retrySpawn or abandon if RestartCount >= MaxRestarts
   → deliver task.abandoned to coordinator inbox
   → coordinator's niwa_await_task unblocks with {status: "abandoned",
     reason: {error: "spawn_failed"}}
```

**What the coordinator currently sees:** `niwa_await_task` blocks until timeout, then
returns `{status: "timeout"}`. There is no way to distinguish a spawn failure from a
slow start or a blocked worker.

**Gap:** `spawnWorker` failure does not deliver an `abandoned` message. The coordinator
is left blocking. This gap is not addressed by DESIGN-coordinator-loop.md and should
be tracked as a separate issue.
