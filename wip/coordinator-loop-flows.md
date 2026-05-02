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
   → UpdateState:
       - RestartCount++ = 1
       - Worker.SpawnStartedAt = now
       - Worker.PID = 0, Worker.StartTime = 0  [zeroed; fresh cmd.Start below backfills]
   → checks Worker.ClaudeSessionID: "<session-id>" present
   → checks Worker.ResumeCount (0) < MaxResumes (2) → resume eligible
   → session file integrity check: ~/.claude/projects/<base64url-cwd>/<session-id>.jsonl
       - must exist, be non-empty, last complete line must parse as valid JSON
   → integrity check passes → spawnWorker(resumeMode=true):
       cmd = "claude --resume <session-id> -p '<reminder>' --permission-mode=... --mcp-config=..."
       reminder: "You were stopped by the stall watchdog. The workspace stop hook resets
                  the watchdog automatically at every turn boundary — you do not need to
                  call niwa_report_progress manually. Continue your work from where you left off."
   → Worker.ResumeCount++ = 1 (RestartCount stays 1)
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

```
Stall kill #1 (Flow 2 steps 3–6):
  RestartCount=1, ResumeCount=1 → claude --resume <session-id> -p "<reminder>"

Stall kill #2: worker stalls again
  → retrySpawn: RestartCount=2 would be next
    But: RestartCount was 1 from prior cycle, now:
    retrySpawn bumps RestartCount → 2
    ResumeCount=1 < MaxResumes(2) → resume eligible
  → claude --resume <session-id> -p "<reminder>"
  → Worker.ResumeCount++ = 2

Stall kill #3: worker stalls again
  → retrySpawn: RestartCount++ = 3
    ResumeCount=2 >= MaxResumes(2) → resume cap exhausted
  → fresh spawn path:
      - Worker.ClaudeSessionID = ""  [zeroed]
      - Worker.ResumeCount = 0       [reset]
      - claude -p "<bootstrap>"  (new session, no prior context)
  → Worker.ResumeCount does NOT increment on fresh spawn

  [OPEN QUESTION: Does fresh spawn after resume exhaustion still increment RestartCount,
   or does ResartCount only track fresh-spawn cycles? The design says "ResumeCount
   resets to 0 and RestartCount increments" on fresh spawn after cap, suggesting
   RestartCount=3 here consumes one of the MaxRestarts budget.]

Stall kill #4 (or unexpected exit on attempt 3):
  → retrySpawn: nextAttempt = RestartCount + 1 = 4 > MaxRestarts (3)
  → over cap → abandon:
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

**Open question:** The design is ambiguous about how RestartCount interacts with the
resume sub-cycle. The description says "ResumeCount resets to 0 and RestartCount
increments" when falling back to fresh spawn after resume exhaustion — but retrySpawn
already bumps RestartCount on every call (whether resume or fresh). If resume attempts
and fresh-spawn attempts all increment RestartCount, MaxRestarts = 3 is consumed by
3 total spawn events regardless of whether they were resumes or fresh. This may be
the intent (budget covers all restart events), but it needs clarification. The alternative
reading is that RestartCount only increments on fresh spawns and MaxResumes caps are
per-fresh-spawn-cycle — which is how the design summary describes it but the implementation
would need an extra condition in retrySpawn to skip the RestartCount bump on resume.

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
