# Cross-Session Communication

## Overview

The niwa mesh lets a coordinating Claude session delegate work to per-repo
workers and collect results without the user acting as a relay. A coordinator
delegates a task to a role; niwa launches an ephemeral `claude -p` worker in
the target repo; the worker finishes, reports back, and exits. Tasks are
first-class objects with an explicit lifecycle — completion is a tool call, not
a process-exit side effect — and delegators can query, update, cancel, or
block on any task they own.

## Quickstart

Enable the mesh for an instance and delegate a task:

```bash
# Provision per-role inboxes, .mcp.json, the niwa-mesh skill, and start the
# mesh watch daemon.
niwa create my-workspace --channels
cd my-workspace

# Open a coordinator session. Launch Claude from the instance root
# (the workspace directory above); that is where niwa writes `.mcp.json`,
# and Claude Code's MCP discovery loads `<cwd>/.mcp.json` only — it does
# not walk parent directories. Sub-repo cwds work too if you pass the
# config path explicitly: `claude --mcp-config=<instance>/.mcp.json`.
claude
```

From the coordinator:

```
niwa_delegate(
  to="web",
  body={"goal": "add a /health endpoint returning 200 OK"},
  mode="sync"
)
```

The daemon sees a new envelope in `.niwa/roles/web/inbox/`, claims it, spawns
`claude -p` in the `web` repo directory, and streams the worker's
`niwa_report_progress` calls back to the coordinator's inbox as
`task.progress` messages. When the worker calls `niwa_finish_task`, the tool
call returns with the worker's result payload.

Async mode returns the task ID immediately:

```
{"task_id": "f1e2d3c4-..."}
```

Follow up with `niwa_query_task`, `niwa_await_task`, `niwa_update_task`, or
`niwa_cancel_task`.

## Tool Reference

The MCP server exposes eleven tools. Eight drive the task lifecycle; three
handle peer messaging. Every authorization-gated call checks the caller's
role and (for worker-only tools) the per-task `PPIDChain(1)` and
`start_time`.

### niwa_delegate

Creates a task and inserts it into the target role's inbox.

```
niwa_delegate(
  to="web",              // target role
  body={...},            // task payload (JSON object)
  mode="async",          // "async" (default) or "sync"
  expires_at="..."       // optional RFC3339 deadline
)
```

Async returns `{"task_id": "<uuid>"}`. Sync blocks until the task reaches a
terminal state and returns the terminal payload.

### niwa_query_task

Non-blocking snapshot of a task.

```
niwa_query_task(task_id="<uuid>")
```

Returns current state, full transitions history, restart count, last progress,
and (for terminal tasks) `result` / `reason` / `cancellation_reason`. Callable
by delegator or executor; non-parties receive `NOT_TASK_PARTY`.

### niwa_await_task

Blocks until the task reaches a terminal state.

```
niwa_await_task(task_id="<uuid>", timeout_seconds=600)
```

Default timeout is 600 seconds. Only the delegator may await; others receive
`NOT_TASK_OWNER`. On timeout, returns a `{"status":"timeout", "current_state":...}`
shape so the caller can re-await without losing progress context.

### niwa_report_progress

Worker-only. Records a non-terminal progress update.

```
niwa_report_progress(
  task_id="<uuid>",
  summary="extracted schema, generating migration",
  body={...}             // optional structured detail
)
```

`summary` truncates to 200 characters with an ellipsis marker. Only the
summary is logged to `transitions.log`; the full body lives in
`state.json.last_progress.body` and is overwritten per event.

### niwa_finish_task

Worker-only. Terminal transition.

```
niwa_finish_task(
  task_id="<uuid>",
  outcome="completed",   // or "abandoned"
  result={...},          // required when outcome="completed"
  reason={...}           // required when outcome="abandoned"
)
```

`completed` and `abandoned` are mutually exclusive — supplying both fields or
neither returns `BAD_PAYLOAD`. A second call on a terminal task returns
`{"status":"already_terminal", "error_code":"TASK_ALREADY_TERMINAL", "current_state":...}`.

### niwa_list_outbound_tasks

Lists tasks delegated by the caller.

```
niwa_list_outbound_tasks(to="web", status="running")
```

Both filters are optional. Returns one row per task with `task_id`, `to_role`,
`state`, `age_seconds`, and a 200-char single-line body summary.

### niwa_update_task

Rewrites a queued task's body.

```
niwa_update_task(task_id="<uuid>", body={...})
```

Returns `{"status":"updated"}` while the task is still `queued`. Once the
daemon has claimed the envelope, returns `{"status":"too_late",
"current_state":...}`. Non-delegators receive `NOT_TASK_OWNER`.

### niwa_cancel_task

Cancels a queued task.

```
niwa_cancel_task(task_id="<uuid>")
```

Atomic rename to `inbox/cancelled/<id>.json`. Returns `{"status":"cancelled"}`
on success, `{"status":"too_late"}` once the daemon has claimed the envelope.
Non-delegators receive `NOT_TASK_OWNER`.

### niwa_ask

Creates a first-class task with body wrapped as `{"kind":"ask", "body":...}`
and blocks on the worker's `niwa_finish_task` result.

```
niwa_ask(
  to="coordinator",
  body={"question": "which auth scheme should web use?"},
  timeout_seconds=600
)
```

Unifies peer Q&A with the task-lifecycle model, so every ask-and-reply inherits
retry, cancellation, and observability semantics.

### niwa_send_message

Routes a typed message to another role's inbox.

```
niwa_send_message(
  to="backend",
  type="task.result",
  body={...},
  reply_to="<msg-id>",       // optional
  task_id="<uuid>",          // optional
  expires_at="..."           // optional RFC3339
)
```

`type` must match `^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)*$` (for example,
`task.progress`, `question.ask`) and be 64 chars or fewer. Unknown roles
return `UNKNOWN_ROLE`; malformed types return `BAD_TYPE`.

### niwa_check_messages

Reads unread messages from the caller's role inbox.

```
niwa_check_messages()
```

Returns messages formatted as markdown. Expired messages are swept to
`inbox/expired/` before the read; returned files are moved to `inbox/read/` via
atomic rename. Call at idle points and every ~10 tool calls while working.

## Task Lifecycle

Every task lives in `.niwa/tasks/<task-id>/` with three files: `state.json`
(authoritative state), `transitions.log` (NDJSON audit trail), and `.lock`
(flock target for atomic transitions).

### State machine

```
            queued ────cancel──▶ cancelled  (terminal)
              │
           claim by daemon
              │
              ▼
           running ──finish(completed)──▶ completed  (terminal)
              │   ─finish(abandoned)───▶ abandoned   (terminal)
              │
           unexpected exit / stall
              │
    restart_count < cap? ──yes──▶ running
              │
             no
              ▼
          abandoned (terminal, reason="retry_cap_exceeded")
```

### Completion contract

A task is `completed` only when the worker calls
`niwa_finish_task(outcome="completed", result=...)`. A worker that exits — with
any exit code — while `state.json.state == "running"` is classified as an
unexpected exit. The daemon increments `restart_count` and schedules the next
spawn after the configured backoff (default `30,60,90` seconds between
attempts). After three restarts (four attempts total), the task transitions to
`abandoned` with `reason="retry_cap_exceeded"`.

### Stall watchdog

Each running worker has a per-supervisor timer seeded to
`NIWA_STALL_WATCHDOG_SECONDS` (default 900). Every `niwa_report_progress` call
resets the timer. On expiry, the daemon sends SIGTERM, waits
`NIWA_SIGTERM_GRACE_SECONDS` (default 5), then SIGKILL. The resulting exit
consumes a retry slot.

### Crash recovery

On daemon startup, every task with `state == "running"` is classified against
the worker PID:

- **Live orphan** (PID alive, `start_time` matches): adopted; central loop
  polls `IsPIDAlive` every 2 seconds.
- **Dead worker** (PID gone, or PID alive but `start_time` diverges — PID
  reuse): routed to the unexpected-exit path with retry.
- **Spawn never completed** (pid == 0, `spawn_started_at` set): fresh retry
  without bumping `restart_count`.

## Worker Spawn Model

Workers are ephemeral `claude -p` processes. The daemon owns spawn, not a
persistent session registry.

- **Resolution.** `exec.LookPath("claude")` runs once at daemon startup. The
  absolute path, owning UID, and mode bits are logged to `.niwa/daemon.log` at
  INFO. The daemon reuses that absolute path for every spawn — `PATH` changes
  after startup have no effect.
- **Argv.** Fixed shape: `-p "<bootstrap prompt with <task-id>>"
  --permission-mode=acceptEdits --mcp-config=<instanceRoot>/.mcp.json
  --strict-mcp-config`. The bootstrap prompt references the task ID; the task
  body never appears in argv — the worker retrieves it via
  `niwa_check_messages` on its first tool call.
- **Env.** Daemon env pass-through plus last-wins niwa overrides:
  `NIWA_INSTANCE_ROOT`, `NIWA_SESSION_ROLE`, `NIWA_TASK_ID`.
- **CWD.** The target role's repo directory, or the instance root for the
  `coordinator` role.
- **Process group.** `Setsid: true` so the daemon can signal the worker's
  entire process group during destroy.
- **No session registration.** Workers are not recorded in `sessions.json`;
  `niwa session list` shows coordinators only.
- **Inbox addressing.** One inbox per role at
  `.niwa/roles/<role>/inbox/{,in-progress,cancelled,expired,read}/`. Role
  names must match `^[a-z0-9][a-z0-9-]{0,31}$`.

## Override Mechanisms

The mesh can be enabled per invocation, per user, or per workspace.

| Mechanism | Scope | Priority | Example |
|-----------|-------|----------|---------|
| `--channels` / `--no-channels` flag | Per invocation | Highest | `niwa create --channels` |
| `NIWA_CHANNELS=1` env var | Per user | Middle | `export NIWA_CHANNELS=1` |
| `[channels.mesh]` in workspace.toml | Per workspace | Lowest | `[channels.mesh]` section |
| Personal overlay skill | Per user | N/A (orthogonal) | `~/.claude/skills/niwa-mesh/SKILL.md` |

`--no-channels` always wins. `--channels` wins over the env var, which wins
over the config.

### workspace.toml

```toml
[channels.mesh]
message_ttl = "48h"

[channels.mesh.roles]
coordinator = ""                # "" = instance root
web = "services/web"            # relative path from instance root
backend = "services/backend"
```

A bare `[channels.mesh]` section with no sub-keys is valid — roles are
derived from the workspace topology (one role per cloned repo plus
`coordinator` at the instance root). An explicit `[channels.mesh.roles]` map
overrides the topology for any listed role.

The parser **rejects** any `NIWA_WORKER_SPAWN_COMMAND` key in `workspace.toml`
with a parse error. That override is env-only so a poisoned clone cannot turn
it into arbitrary code execution at apply time.

### Personal overlay

A user can override the `niwa-mesh` skill at the personal scope by placing a
skill at `~/.claude/skills/niwa-mesh/SKILL.md`. `niwa apply` does not touch
this path; the personal-scope skill takes precedence in Claude Code's usual
skill lookup.

## Operational Guidance

### PATH resolution is frozen at daemon startup

The daemon resolves `claude` once via `exec.LookPath` and reuses the absolute
path for every spawn. A `PATH` change after startup has no effect; the daemon
logs the resolved path, owning UID, and mode bits at startup — that log line
is the audit trail for what will actually run.

Do not set `NIWA_WORKER_SPAWN_COMMAND` in shell profiles shared across
repositories. That variable substitutes a literal binary path for `claude`; a
leaked value in a profile silently replaces every worker's runtime. The intent
is testing, not persistent reconfiguration. When you need it, set it only for
the specific command that launches the daemon.

### macOS vs Linux worker authentication

On Linux, the executor check walks `PPIDChain(1)` (the MCP server's parent =
the `claude -p` worker) and compares the worker's `start_time` against
`state.json.worker.{pid, start_time}`. This defeats naive same-UID PID
spoofing.

On macOS, `PIDStartTime` returns an alive/dead answer without a precise
timestamp, so the check degrades to PID-match-only. The role-integrity trust
ceiling still holds, but same-UID process isolation is strictly weaker. Users
needing the strongest local isolation should run niwa on Linux.

### Backup exclusion

`.niwa/` contains live task envelopes, `state.json`, and the append-only
`transitions.log` — which includes terminal result / reason bodies and
progress summaries. Exclude `.niwa/` from shared or cloud backups if tasks
carry content you don't want replicated.

### Migration from pre-1.0 instances

The old mesh stored per-session inboxes under `.niwa/sessions/<uuid>/`. On the
first `niwa apply` under the new model, if the installer sees any
`.niwa/sessions/<uuid>/` directories and no `.niwa/roles/` layout, it:

1. Emits one stderr warning ("migrating pre-1.0 mesh layout; queued envelopes
   will be discarded").
2. Removes the old session directories.
3. Preserves `sessions.json` so coordinator registration survives.

Pre-1.0 queued envelopes are not carried forward — the wire format changed.

### Destroy order

`niwa destroy` now signals workers first:

1. List all `running` tasks, SIGKILL each worker's process group (negative PID
   signal).
2. SIGTERM the daemon, wait `NIWA_DESTROY_GRACE_SECONDS` (default 5), SIGKILL
   if needed.
3. Remove the instance directory.

Killing workers first minimizes the window during which an
`acceptEdits`-enabled worker could still write to the filesystem while the
instance is being torn down. **Always use `niwa destroy` to tear down a
channeled instance.** `rm -rf` of the instance directory does not signal
the daemon — it keeps running, holding fsnotify watches against a now-
missing path, and you'll need `pgrep -af "niwa.*mesh watch"` plus a manual
`kill` to clean it up. Tracking proper teardown semantics is in issue #75.

### Coordinator restart

Inside the MCP server, `niwa_await_task` waits via `awaitWaiters` —
an in-memory map keyed by task_id, populated when the coordinator
calls `niwa_await_task` and woken by the recipient watcher when a
terminal envelope arrives in the coordinator's inbox. This map is
intentionally not persisted: a coordinator session that crashes
mid-await loses its wake-up channel.

`state.json` on disk is the authoritative record. The worker's
`niwa_finish_task` writes the terminal state correctly regardless of
whether anyone is listening, so on coordinator crash and restart the
recovery path is:

1. From a new coordinator session, run `niwa task list` (or call
   `niwa_query_task(task_id=<id>)` from the LLM coordinator) to read
   the terminal state from disk.
2. Optionally run `niwa_check_messages`. The daemon-spawned worker's
   terminal-event message (`task.completed`, `task.abandoned`, or
   `task.cancelled`) was written to the coordinator's role inbox at
   completion time; any envelope that arrived before the crash is
   still there to be surfaced.

If you find yourself wanting `awaitWaiters` to survive coordinator
crashes, the underlying issue is almost always an LLM coordinator
that doesn't know to re-query. The skill text and operational
guidance above tell it to do exactly that.

### Coordinator question handling

Workers sometimes need clarification mid-task. A worker calls
`niwa_ask(to="coordinator", body={"question": "..."})` and blocks until
the coordinator responds. The coordinator receives the question through
one of two paths depending on whether it is blocking or polling:

**Blocking path** (`niwa_await_task`): When a live coordinator session is
registered in `sessions.json`, niwa routes the question directly to the
coordinator's inbox instead of spawning an ephemeral worker. If the
coordinator is mid-`niwa_await_task`, the call returns immediately with:

```json
{
  "status": "question_pending",
  "ask_task_id": "<uuid>",
  "from_role": "web",
  "body": {
    "_niwa_note": "This is a question from a worker. To answer, call niwa_finish_task...",
    "question": { "question": "Which auth scheme should I use?" }
  }
}
```

**Polling path** (`niwa_check_messages`): A coordinator that calls
`niwa_check_messages` instead of blocking on `niwa_await_task` receives
the question as a `type == "task.ask"` message with the same body shape.

In both cases, answering the question is the same: call
`niwa_finish_task(task_id=<ask_task_id>, outcome="completed", result={...})`.
Use `ask_task_id` — not the delegated task's ID — as the `task_id`.

**Coordinator re-wait loop (worked example)**:

1. Coordinator delegates a task to `web`:
   ```
   niwa_delegate(to="web", body={"goal": "add /health endpoint"}, mode="async")
   // → {"task_id": "task-abc"}
   ```

2. Coordinator blocks waiting for completion:
   ```
   niwa_await_task(task_id="task-abc", timeout_seconds=600)
   ```

3. Mid-task, the web worker needs clarification:
   ```
   niwa_ask(to="coordinator", body={"question": "TCP or HTTP health check?"})
   ```

4. `niwa_await_task` returns to the coordinator:
   ```json
   {"status": "question_pending", "ask_task_id": "ask-xyz", "from_role": "web",
    "body": {"question": {"question": "TCP or HTTP health check?"}}}
   ```

5. Coordinator answers:
   ```
   niwa_finish_task(task_id="ask-xyz", outcome="completed",
                    result={"answer": "HTTP, return 200 OK"})
   ```

6. Coordinator re-waits on the original task:
   ```
   niwa_await_task(task_id="task-abc", timeout_seconds=600)
   ```

7. The worker's `niwa_ask` call unblocks with the answer. The worker
   continues and eventually calls `niwa_finish_task(task_id="task-abc", ...)`.

8. `niwa_await_task` returns with the terminal result.

The re-wait loop pseudocode:

```
result = niwa_await_task(task_id=delegated_task_id)
while result.status == "question_pending":
    niwa_finish_task(task_id=result.ask_task_id, outcome="completed", result=...)
    result = niwa_await_task(task_id=delegated_task_id)
// result is now terminal: completed, abandoned, or timeout
```

### Watcher overflow

Both watchers in the system — the daemon's central inbox watcher and
each MCP server's role-inbox watcher — use Linux `inotify` via the
`fsnotify` library. The kernel's per-process inotify event queue is
bounded (default 16384 events; see `/proc/sys/fs/inotify/max_queued_events`).
If sustained filesystem activity exceeds the drain rate, the queue
overflows and events are dropped silently.

In a niwa workspace, a dropped `task.completed` notification is the
most painful symptom: the worker writes the terminal envelope to the
coordinator's role inbox, but the coordinator's MCP server never wakes,
and `niwa_await_task` hangs to its timeout. At documented usage scales
(a handful of roles, tens of tasks per day) overflow is unrealistic; at
high-volume use it's a real failure mode.

If you suspect a dropped event:

1. Check `niwa task list` and look for tasks that show `state: completed`
   on disk while a coordinator session is still waiting on them.
2. Restart the daemon and the affected coordinator session. Both
   watchers reload their state from disk on startup, so any envelope
   that landed during the dropped window gets picked up on the rescan.

A periodic resync inside the watchers is tracked as a separate
follow-up.

### Long-running tasks and `niwa_await_task` timeouts

`niwa_await_task` defaults to `timeout_seconds=600` (10 minutes). Real
coding tasks routinely run longer. When the default elapses, the call
returns `{"status":"timeout"}` with the current task state, which an
LLM coordinator may misread as failure if the prompt doesn't tell it
what to do next.

Two patterns keep long tasks coordinated:

1. **Set an explicit timeout up front.** When you delegate work you
   expect to take more than 10 minutes, follow up with
   `niwa_await_task(task_id=<id>, timeout_seconds=<estimated_minutes * 60 + buffer>)`.
   A 30-minute task wants `timeout_seconds=2400` (30 × 60 + 600s buffer).
2. **Re-await on timeout.** If the call does return `status:"timeout"`,
   re-call `niwa_await_task(task_id=<id>)` instead of giving up. The
   worker is still progressing; the next call resumes the wait against
   the same on-disk state.

The same guidance is in the `niwa-mesh` skill's "Common Patterns"
section so an LLM following the skill encounters it inline.

### Log growth and rotation

Three files under `.niwa/` grow append-only with no built-in rotation:

| File | Per-event size | Driver |
|------|----------------|--------|
| `<instance>/.niwa/mcp-audit.log` | ~250 B per `tools/call` | every MCP tool invocation |
| `<instance>/.niwa/tasks/<task>/transitions.log` | ~200 B per state change | task lifecycle events |
| `<instance>/.niwa/tasks/<task>/stderr.log` | variable | worker stderr (depends on the worker's verbosity) |

At low usage (one developer, dozens of tasks per day) growth is invisible. At
sustained heavy use these files accumulate without bound. If you hit the
disk-fill condition before automatic rotation lands as a separate feature,
the supported workarounds are:

1. **Destroy and recreate the instance** — `niwa workspace destroy && niwa create`.
   This is the cleanest path because it also clears completed-task state.
2. **Manually rotate `mcp-audit.log`** while the daemon is stopped. Stop the
   daemon, move the file aside (`mv mcp-audit.log mcp-audit.log.1`), restart;
   the next call recreates it.

Per-task `transitions.log` and `stderr.log` are less interesting for live
rotation since they're scoped to a single task; they go away when the task
directory is reaped.



See the [PRD's Known Limitations section](../prds/PRD-cross-session-communication.md#known-limitations)
for the full list. The ones that most affect operators:

- **`acceptEdits` blast radius.** Workers run with
  `--permission-mode=acceptEdits` so background processes don't stall on
  permission prompts. A prompt-injected worker can write anywhere in the
  role's repo without confirming. Users who need per-edit confirmation should
  not enable channel delegation. Per-role permission-mode overrides are
  deferred.
- **`transitions.log` body retention.** Progress summaries (200 chars) and
  terminal result / reason bodies are appended to
  `.niwa/tasks/<id>/transitions.log` as an audit trail. There's no v1 garbage
  collection; the log accumulates indefinitely. Manually clean completed task
  directories, or exclude `.niwa/` from backups, if bodies are sensitive.
- **Env cross-pollination across roles.** The daemon inherits the user's
  shell environment and passes it through to every worker regardless of role.
  A secret intended for one role (for example, `AWS_*` credentials for
  `backend`) is visible to workers in every other role. Per-role env filtering
  is deferred — treat all shell-exported secrets as visible to every
  delegated worker.
- **macOS degradation.** As noted above, worker authentication falls back to
  PID-match-only on macOS.

## Cross-references

- [PRD: Cross-Session Communication](../prds/PRD-cross-session-communication.md)
  — problem statement, user stories, requirements, acceptance criteria.
- [Design: Cross-Session Communication](../designs/current/DESIGN-cross-session-communication.md)
  — rationale, rejected alternatives, sequence diagrams, security model.
- [Functional Testing Guide](functional-testing.md) — end-to-end test patterns,
  including the "Testing the mesh" section that covers
  `NIWA_WORKER_SPAWN_COMMAND`, timing overrides, and daemon pause hooks.
