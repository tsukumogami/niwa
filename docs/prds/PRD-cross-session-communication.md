---
status: Delivered
problem: |
  Developers use niwa to manage multi-repo workspaces where work often needs to
  be delegated across repos from one coordinating Claude session. The existing
  mesh design assumes all participating agents were started manually by the
  user and conflates Claude session lifetime with task lifetime. As a result,
  a coordinator cannot dispatch a task to a repo where no Claude has ever run;
  it cannot tell whether a delegated unit of work completed or the worker
  simply exited; and the sender has no way to inspect or cancel tasks it
  queued but that have not yet been consumed. The user ends up re-opening
  terminals in each repo and babysitting worker processes to know what's
  happening.
goals: |
  Niwa provisions a task queue per role at workspace creation time, so any
  known role can receive work immediately — whether or not a Claude session
  has ever been opened there. Tasks are first-class objects with an explicit
  lifecycle (queued, running, completed, abandoned, cancelled); completion is
  an explicit worker action, not a process-exit side effect. Delegators can
  dispatch synchronously, dispatch asynchronously and later query or block on
  completion, and receive progress automatically while work is in flight.
  Senders can inspect, update, or cancel tasks they queued before a worker
  consumes them. Niwa ships a default behavior skill installed into every
  agent so delegation, reporting, and completion follow the same contract out
  of the box, with user overrides at the personal scope.
---

# PRD: Cross-Session Communication

## Status

Delivered

## Problem Statement

Developers using niwa to manage multi-repo workspaces routinely need to
delegate work from one coordinating Claude session to per-repo workers, then
collect results. The existing mesh design assumes every participating session
was already started manually by the user. When the user opens one Claude at
the workspace root and asks it to "add a REST endpoint to the web project and
a matching schema change in the backend project, each in its own PR," the
coordinator has no mechanism to make that happen. It can send a message
addressed to `web`, but nothing consumes it — there is no session registered
under that role, and the daemon's only session-lifecycle verb is to resume
sessions that have previously run.

The design also conflates Claude session lifetime with task lifetime. A worker
is considered "done" when its process exits, regardless of whether the
delegated work succeeded. A worker that hits its context limit mid-task and
exits quietly looks identical to one that completed successfully, because
neither produces a completion signal niwa can distinguish. The delegator has
to reason about process state rather than task state.

Finally, senders cannot manage the queue of tasks they have dispatched. Once
a coordinator calls the send tool, the message is on its way; there is no way
to update a queued task whose recipient has not yet picked it up, no way to
cancel a task that is no longer needed, and no way to list what's in flight.
A coordinator that changes its mind mid-delegation has no recourse.

These gaps make the mesh practical only for scenarios where the user has
already arranged every session and is paying attention to every worker. The
headline scenario — "delegate across repos, go make coffee, come back to PRs"
— is not reachable without addressing all three.

## Goals

1. A coordinator can dispatch work to any known role in the workspace at any
   time, including roles where no Claude has ever run. Niwa launches the
   worker when needed; the caller's tool surface does not change based on
   worker liveness.
2. Tasks are first-class objects with an explicit lifecycle. A task completes
   when the worker explicitly reports completion, not when its process exits.
   Niwa auto-restarts workers that exit without completing, up to a bounded
   cap, then reports abandonment.
3. A delegator can dispatch synchronously and block for the result, dispatch
   asynchronously and query status or await completion later, and receive
   progress events automatically while work is in flight.
4. A sender can list, update, or cancel tasks it has queued, with a clear
   distinction between "still mine to mutate" and "already consumed."
5. Niwa's opinionated surface is narrow: the tool API and the task state
   machine. Message vocabulary, delegation style, and agent-to-agent
   interaction patterns live in a default skill niwa installs into every
   agent, which users can override at the personal scope.

## User Stories

**US1 — Coordinator delegates to a role that has never been opened**
As a coordinator Claude session at the workspace root, I want to dispatch a
task to the `web` role even though no Claude has ever run in the `web`
directory, so that niwa launches a worker to handle the task without me
asking the user to open another terminal.

**US2 — Coordinator delegates asynchronously and awaits later**
As a coordinator with two independent tasks (one for `web`, one for
`backend`), I want to call `niwa_delegate(mode="async")` for each, continue
handling other tool calls in between, then call `niwa_await_task` on each
task ID, so that I can parallelize cross-repo work without polling.

**US3 — Coordinator delegates synchronously**
As a coordinator with a single task whose outcome I need before I can
continue, I want to dispatch it with `mode="sync"` and receive the result as
the tool call's return value, so that my next action naturally depends on the
result without managing task IDs myself.

**US4 — Delegator receives progress while a task runs**
As a delegator that has dispatched async work, I want each worker's
`niwa_report_progress` call to produce a `task.progress` message in my inbox
within seconds, so that I can relay "backend is scaffolding the schema" to
the user without polling or querying.

**US5 — Sender cancels a task it queued**
As a delegator that has queued a task for `backend`, I want `niwa_cancel_task`
to succeed with `{status: "cancelled"}` if the consumer has not yet picked it
up, and to return `{status: "too_late"}` otherwise, so that I can correct an
earlier mistake without a worker doing unnecessary or wrong work.

**US6 — Sender updates a queued task's body**
As a delegator that has queued a task with an incomplete prompt, I want
`niwa_update_task` to replace the body before the consumer picks it up, so
that the worker sees my corrected instruction rather than the original.

**US7 — Worker asks a peer mid-task**
As a worker executing a delegated task in the `web` repo, I want to call
`niwa_ask(to="coordinator", body={...})` and have the call return the
coordinator's reply body, so that I can proceed with authority rather than
guess at an ambiguity.

**US8 — Niwa restarts a worker that exited without completing**
As a delegator, I want niwa to automatically spawn a replacement worker when
a worker process exits while the task's state is still `running`, up to the
configured retry cap, and to mark the task `abandoned` with a
`task.abandoned` message when the cap is reached, so that transient failures
recover without user intervention and stuck tasks don't loop indefinitely.

**US9 — User inspects mesh state**
As a developer running a multi-session workspace, I want `niwa task list` to
show every task with its role, status, restart count, and age, so that I can
see at a glance what's queued, running, completed, or abandoned.

**US10 — Delegator reconnects after closing the coordinator**
As a user who closed the coordinator terminal while tasks were in flight, I
want to reopen the coordinator, see accumulated `task.progress`,
`task.completed`, and `task.abandoned` messages in the inbox on session
start, and be able to query tasks by ID via `niwa_query_task` for any task
delegated by this role, so that no outcomes are lost to disconnection.

## Requirements

### Provisioning

**R1** — The presence of a `[channels.mesh]` section in `workspace.toml`, the
`--channels` flag on `niwa create`/`niwa apply`, or the `NIWA_CHANNELS=1`
environment variable shall opt an instance into mesh provisioning. The flag
overrides the env var, which overrides config. `--no-channels` suppresses
provisioning regardless of env or config. A workspace that is not opted in
shall provision no mesh infrastructure, start no daemon, and omit mesh
information from `niwa status`.

**R2** — `niwa create` and `niwa apply` shall provision the mesh
infrastructure at the instance root:

- `.niwa/roles/<role>/inbox/` — per-role task queue for every known role
- `.niwa/roles/<role>/inbox/in-progress/` — holding area for claimed tasks
- `.niwa/roles/<role>/inbox/cancelled/` — holding area for cancelled tasks
- `.niwa/roles/<role>/inbox/expired/` — holding area for expired messages
- `.niwa/tasks/` — per-task directories keyed by task ID
- `.niwa/sessions/sessions.json` — coordinator registry (workers are not
  registered here)
- `.niwa/daemon.pid` and `.niwa/daemon.log` — daemon lifecycle artifacts

Every written path shall be tracked in `InstanceState.ManagedFiles` so that
drift detection and `niwa destroy` work uniformly.

**R3** — A channel installer function shall run in the provisioning pipeline
after repository clones are complete and before per-repo materializers. It
shall be invoked imperatively from `Applier.runPipeline`.

**R4** — The channel installer shall write `<instanceRoot>/.mcp.json`
declaring a `niwa` MCP server entry that invokes `niwa mcp-serve` with
`NIWA_INSTANCE_ROOT` baked in. The file lives at the directory root (not
under `.claude/`) so Claude Code's MCP discovery, which reads
`<cwd>/.mcp.json` and walks the directory tree upward, finds it both when
a session is launched at the instance root and when it is launched from
any sub-repo. No per-repo mirror is written: the upward walk makes one
copy sufficient, and a per-repo `.mcp.json` would commit the local binary
path and instance root into upstream history.

**R5** — The channel installer shall enumerate the full set of roles from
`workspace.toml` (explicit `[channels.mesh.roles]` entries) and the cloned
repositories (one auto-derived role per repo, named from the repo's
directory basename), and create a per-role inbox directory for every role,
including `coordinator`. Explicit entries take precedence for a given role
name; auto-derivation still applies to repos not listed. A role's inbox
shall be created when the installer first sees the role, whether that is on
the initial `niwa apply` or a later one after new repos are added.

### Roles and Topology

**R6** — Role names shall match `^[a-z0-9][a-z0-9-]{0,31}$`. The name
`coordinator` is reserved for the instance root. Names not matching the
pattern, or an explicit `[channels.mesh.roles]` entry mapping `coordinator`
to a non-root path, shall cause `niwa apply` to fail with a clear error.
When two repos would auto-derive the same role name, `niwa apply` shall
fail with a role-collision error and require the user to add an explicit
`[channels.mesh.roles]` entry to resolve it.

**R7** — At session-start time, an individual session may override its
assigned role via `NIWA_SESSION_ROLE`. The override shall be rejected when
the session is at the instance root and targets a role other than
`coordinator`, or when the target role is already registered by a live
session.

**R8** — Only one session may be registered under a given role at a time.
For the coordinator role, this is enforced by `niwa session register`
rejecting a second registration while the first's PID is alive. For worker
roles, the daemon enforces single-consumer claim on the role's inbox: when
a worker is spawned to claim a queued task, any concurrent attempt to spawn
another worker for the same role shall observe that the inbox no longer
contains a queued envelope (the consumption rename has moved it) and shall
not spawn.

### Skill Installation and Behavior Spec

**R9** — The channel installer shall install a `niwa-mesh` skill at
`<instanceRoot>/.claude/skills/niwa-mesh/` and mirror it into each
`<repoDir>/.claude/skills/niwa-mesh/`. The skill artifact is a directory
containing a `SKILL.md` with YAML frontmatter (`name: niwa-mesh`;
`description` front-loaded with when-to-use guidance fitting within Claude
Code's 1,536-character combined cap; `allowed-tools` listing the niwa MCP
tools) followed by the behavior body. All installed paths shall be tracked
in `InstanceState.ManagedFiles`.

**R10** — The `niwa-mesh` skill body shall contain the following sections
with stable headings: "Delegation (sync vs async)", "Reporting Progress",
"Completion Contract", "Message Vocabulary", "Peer Interaction", and
"Common Patterns". Each section shall describe the default behavior for
agents participating in the mesh. Behavioral guidance that existed in the
prior PRD (immediate-check-on-wake, progress cadence, response-via-tool-
call, message type vocabulary) shall live in this skill body.

**R11** — `niwa apply` shall overwrite the installed skill files
unconditionally when they differ from the installer's output, and shall
emit a drift warning to stderr identifying which file was rewritten. The
overwrite occurs even when drift is detected; users preserve customizations
by placing a copy at `~/.claude/skills/niwa-mesh/`, which Claude Code
resolves ahead of the project-installed copy. Niwa shall not back up
modified files and shall not implement override detection.

**R12** — The `## Channels` section in `workspace-context.md` shall contain
only the following content, in this order: the session's assigned role,
the instance root path, the names of the niwa MCP tools (one per line),
and a single pointer reading "See the `/niwa-mesh` skill for usage
patterns." No behavioral prescriptions, message-vocabulary lists, cadence
recommendations, or examples shall appear in `workspace-context.md`.

### Task Lifecycle

**R13** — A task shall be a first-class object with the following state
machine:

| From    | To         | Triggered By | Trigger Event                                          |
|---------|------------|--------------|--------------------------------------------------------|
| (new)   | queued     | Delegator    | `niwa_delegate` call; envelope written to inbox        |
| queued  | running    | Daemon       | Consumption rename; worker spawned                     |
| queued  | cancelled  | Delegator    | `niwa_cancel_task` call                                |
| running | completed  | Worker       | `niwa_finish_task(outcome="completed", result=...)`    |
| running | abandoned  | Worker       | `niwa_finish_task(outcome="abandoned", reason=...)`    |
| running | abandoned  | Daemon       | Unexpected exit after retry cap; or watchdog cap       |

`running → cancelled` and `queued → completed/abandoned` are not valid
transitions. Terminal states are `completed`, `abandoned`, and `cancelled`.

**R14** — A task shall be materialized as a directory at
`<instanceRoot>/.niwa/tasks/<task-id>/` containing:

- `envelope.json` — delegation payload and metadata (see R15)
- `state.json` — current state, transition timestamps, restart count, last
  progress event summary
- `transitions.log` — append-only NDJSON log of all state changes and
  progress events, each with a UTC timestamp

State changes shall use per-task `flock` on `state.json` plus atomic rename
from a tempfile to prevent concurrent-write corruption.

**R15** — The task envelope schema (version `v=1`) shall be:

- `v` (integer, required) — schema version
- `id` (string, required) — task UUID
- `from` (object, required) — `{role: string, pid: int}`
- `to` (object, required) — `{role: string}`
- `body` (object, required) — delegation payload (delegator-defined)
- `sent_at` (RFC 3339 timestamp, required)
- `parent_task_id` (string, optional) — present when the delegator is
  itself executing a task; niwa populates this automatically
- `deadline_at` (RFC 3339 timestamp, optional) — hard deadline; enforcement
  is **out of scope for v1** (see Out of Scope)
- `expires_at` (RFC 3339 timestamp, optional) — envelope expiry for
  cancellation of queued tasks; default absent

Niwa shall populate `v`, `id`, `from`, `sent_at`, and `parent_task_id`
automatically. The caller provides `to.role`, `body`, and optionally
`expires_at`.

**R16** — The consumption barrier shall be an atomic rename of the queued
envelope from `.niwa/roles/<role>/inbox/<task-id>.json` to
`.niwa/roles/<role>/inbox/in-progress/<task-id>.json`. Only the daemon
performs this rename, and only when it is spawning a worker to consume the
task. The rename is the point at which the task's state transitions from
`queued` to `running`.

**R17** — Completion shall be explicit. A task transitions to `completed`
only when the worker calls `niwa_finish_task` with `outcome="completed"`.
Process exit — regardless of exit code — shall not complete a task. A
worker that exits with code zero without calling `niwa_finish_task` shall
be treated as an unexpected exit (R34).

**R18** — Explicit failure shall be available via `niwa_finish_task` with
`outcome="abandoned"`. This transitions the task to `abandoned`
immediately without consuming a restart slot, and writes a `task.abandoned`
message to the delegator's inbox carrying the provided reason.

### Delegator Tools

**R19** — `niwa_delegate(to, body, mode, expires_at?)` shall dispatch a
task. `mode` shall be `"sync"` or `"async"`; default is `"async"`. In
`async` mode the tool shall return `{task_id: string}` immediately. In
`sync` mode the tool shall block until the task reaches a terminal state
and shall return `{status: "completed", result: <result>}` on completion,
`{status: "abandoned", reason: <reason>, restart_count: <n>}` on
abandonment, or `{status: "cancelled"}` on cancellation. Sync mode has no
tool-level timeout; liveness is bounded by the stalled-progress watchdog
(R36) and the restart policy (R34).

**R20** — `niwa_query_task(task_id)` shall return without blocking a
response containing: `state`, `state_transitions` (array of `{from, to, at}`
records), `restart_count`, `last_progress` (the most recent progress event
summary and body, or `null`), and — if the state is terminal — `result`,
`reason`, or `cancellation_reason` as applicable. Authorization is
enforced: if the caller's role is neither the task's delegator nor the
currently-running executor, the tool shall return
`{status: "forbidden", error_code: "NOT_TASK_PARTY"}`.

**R21** — `niwa_await_task(task_id, timeout_seconds?)` shall block until
the specified task reaches a terminal state, then return the same payload
shape as `niwa_query_task`. `timeout_seconds` is optional; if provided and
reached before the task reaches a terminal state, the tool shall return
`{status: "timeout", current_state: <state>}` without cancelling the task.
Authorization: only the delegator of a task may await it; other callers
receive `{status: "forbidden", error_code: "NOT_TASK_OWNER"}`.

**R22** — Progress events emitted by a worker shall be delivered to the
delegator's inbox as `task.progress` messages within 5 seconds of the
`niwa_report_progress` tool call returning. This is a niwa-owned delivery
guarantee, not a Claude-session behavior. The delegator observes these
messages via `niwa_check_messages` or via any subsequent blocking tool
call that returns. The PRD requires only the inbox landing; delivery to
hook-injected prompts or open tool calls is permitted but not required.

### Worker Tools

**R23** — `niwa_report_progress(task_id, summary, body?)` shall emit a
progress event. `summary` is a string; values longer than 200 characters
shall be truncated to 200 characters with a `…` marker appended before
storage and delivery. `body` is optional; when present it is a structured
payload delivered verbatim. Niwa shall append the event to the task's
`transitions.log` and deliver a `task.progress` message to the delegator's
inbox. The call returns immediately after the `transitions.log` append
succeeds; inbox delivery may occur asynchronously within R22's 5-second
bound. The cadence of progress reports is a skill-owned behavioral
concern (R10), not a niwa requirement.

**R24** — `niwa_finish_task(task_id, outcome, result?, reason?)` shall be
the sole valid terminal-transition tool for a task. `outcome` is required
and shall be `"completed"` or `"abandoned"`. When `outcome="completed"`,
the caller shall provide `result` (arbitrary JSON body) and shall not
provide `reason`; when `outcome="abandoned"`, the caller shall provide
`reason` (arbitrary JSON body, typically a string or
`{reason: string, detail?: string}`) and shall not provide `result`.
A mismatched or missing payload field shall return
`{status: "invalid", error_code: "BAD_PAYLOAD"}` without modifying task
state.

On `outcome="completed"`: niwa shall transition the task to `completed`
atomically, write a `task.completed` message containing the result to the
delegator's inbox, and make the result visible to `niwa_query_task` and
`niwa_await_task`.

On `outcome="abandoned"`: niwa shall transition the task to `abandoned`
atomically, write a `task.abandoned` message carrying the reason to the
delegator's inbox, and shall not increment the restart counter. Explicit
abandonment does not consume a restart slot.

Calling `niwa_finish_task` on a task whose state is already terminal shall
return `{status: "already_terminal", error_code: "TASK_ALREADY_TERMINAL",
current_state: <state>}` without modifying task state.

**R25** — *(Reserved. Earlier drafts defined a separate `niwa_fail_task`
tool; it is merged into R24's `niwa_finish_task` with
`outcome="abandoned"`.)*

### Queue Mutation Tools

**R26** — `niwa_list_outbound_tasks(to?, status?)` shall return tasks the
caller delegated, optionally filtered by target role and/or status. The
response shall include task ID, target role, state, age, and the first 200
characters of `body` as a summary. Tasks delegated by other roles shall
not appear.

**R27** — `niwa_update_task(task_id, body)` shall replace a queued task's
body with a new one. It shall succeed (`{status: "updated"}`) only if the
task is still in `queued` state when the update is attempted. If the task
has already been consumed, the tool shall return `{status: "too_late",
current_state: <state>}`. If the caller's role is not the task's
delegator, the tool shall return `{status: "forbidden", error_code:
"NOT_TASK_OWNER"}`. The update is implemented as a write to the envelope
inside `.niwa/tasks/<task-id>/envelope.json` followed by a synchronized
rewrite of the queued inbox entry; if the daemon's consumption rename
races the update, the update returns `{status: "too_late"}` and the
envelope delivered to the worker is the pre-update version.

**R28** — `niwa_cancel_task(task_id)` shall cancel a queued task via an
atomic rename from `.niwa/roles/<role>/inbox/<task-id>.json` to
`.niwa/roles/<role>/inbox/cancelled/<task-id>.json`. On success, the task
state transitions to `cancelled` and the tool returns `{status:
"cancelled"}`. If the rename fails because the envelope is no longer in
`inbox/<task-id>.json` (the daemon has already claimed it), the tool shall
return `{status: "too_late", current_state: <state>}`. If the caller's
role is not the task's delegator, the tool shall return
`{status: "forbidden", error_code: "NOT_TASK_OWNER"}`. In-flight
cancellation of running tasks is **out of scope for v1**.

### Peer Messaging

**R29** — `niwa_ask(to, body, timeout_seconds?)` shall be a blocking
request-reply tool. Any agent may ask any other agent. When the target
role has no running worker and is not the coordinator, `niwa_ask` shall
be treated as a first-class task: niwa creates a task envelope with
`body.kind = "ask"`, spawns a worker via `claude -p` to consume it, and
the worker's reply (via `niwa_send_message` with matching `reply_to`)
completes the task. Ask-tasks participate in the restart policy (R34),
the stalled-progress watchdog (R36), and `niwa task list`. The default
`timeout_seconds` is 600 (10 minutes); when the timeout fires, the tool
returns `{status: "timeout"}` without cancelling the underlying task.

**R30** — `niwa_send_message(to, type, body, reply_to?, expires_at?)`
shall dispatch a one-way peer message. The sender does not wait for a
reply. `type` is a dotted routing key; niwa validates its format as
`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)*$` (max length 64) and rejects
malformed values with `{status: "invalid", error_code: "BAD_TYPE"}`.
Niwa does not validate the semantics of `type`; vocabulary is skill-
owned (R10). `reply_to` is optional; when present, it correlates this
message with an earlier `niwa_ask`. Sending to a role not in the mesh's
known role set shall return `{status: "rejected", error_code:
"UNKNOWN_ROLE"}`.

**R31** — `niwa_check_messages` shall return all unread messages from
the caller's inbox, formatted as structured markdown preserving message
ID, sender role, type, timestamp, and body. Messages whose `expires_at`
has passed shall not be returned; niwa sweeps them to
`.niwa/roles/<role>/inbox/expired/` on every inbox read before returning
the remaining set. "Unread" means "currently present in the inbox
directory (not in `in-progress/`, `cancelled/`, `expired/`, or `read/`)."
After returning messages, niwa shall move the returned files to
`.niwa/roles/<role>/inbox/read/` via atomic rename so subsequent calls
do not re-surface them.

### Daemon

**R32** — `niwa mesh watch` shall be a persistent daemon scoped to the
workspace instance. It shall watch all per-role inbox directories under
`.niwa/roles/*/inbox/` via fsnotify. When a new task envelope appears in
a role's inbox and no worker is currently running for that role, the
daemon shall perform the consumption rename (R16) and spawn a worker
via:

```
claude -p "You are a worker for niwa task <task-id>. Call niwa_check_messages to retrieve your task envelope."
```

The exact bootstrap string is niwa-owned and fixed in the binary. The
bootstrap prompt shall not contain the task body.

**R33** — The daemon shall spawn `claude -p` with:
- working directory set to the target role's repository directory (or
  the instance root for the coordinator role);
- `--permission-mode=acceptEdits` (so background workers do not block on
  permission prompts);
- `--mcp-config=<instanceRoot>/.mcp.json --strict-mcp-config` (so
  the worker has deterministic MCP access regardless of user-level
  config).

**R34** — The daemon shall auto-restart workers that exit without
completing. "Unexpected exit" is defined as: the worker process has
exited (detected via SIGCHLD where supported, else by a PID liveness
check at interval ≤ 5 seconds) while the task's `state.json` status is
still `running`. Retries shall be capped at 3 restarts (4 total attempts)
per task, with backoff intervals of 30s, 60s, 90s between successive
restart attempts. After the cap, the task shall be transitioned to
`abandoned` and a `task.abandoned` message shall be written to the
delegator's inbox with `reason: "retry_cap_exceeded"` and the final
restart count.

**R35** — Abandonment shall surface via both: (a) a `task.abandoned`
message in the delegator's inbox; (b) `niwa_query_task` returning
`state: "abandoned"` with the reason and final restart count.

**R36** — The daemon shall implement a stalled-progress watchdog. If a
task is in `running` state and no `task.progress` event has been
recorded for longer than the configured stall threshold, the daemon
shall send SIGTERM to the worker process; if the worker has not exited
within the configured SIGTERM grace window, SIGKILL. The resulting
exit shall be treated as an unexpected exit (R34) and shall apply the
restart policy. Defaults are listed in the Configuration Defaults
section.

**R37** — On daemon startup, the daemon shall reconcile outstanding
tasks. For each task with state `running`: if the recorded worker PID
is alive, adopt it (record its PID in state.json with an `adopted_at`
field and poll for exit via the liveness check at interval ≤ 5
seconds, since the new daemon is not the process parent and cannot
`waitpid`); if the PID is dead, treat the task as an unexpected exit
and apply the restart policy. For each task in `queued` state: resume
normal inbox-watching behavior; no explicit reconciliation needed
because fsnotify plus the inbox's current contents determine the next
action.

**R38** — Daemon lifecycle: `niwa apply` shall start the daemon as a
detached background process if and only if no live daemon exists (PID
file check verifying the recorded PID is alive; a flock on the PID
file prevents two `niwa apply` invocations from racing to spawn two
daemons). `niwa destroy` shall send SIGTERM, wait up to the configured
destroy-grace window, send SIGKILL if the daemon has not exited, then
remove the instance directory. The daemon is stateless across restarts;
all durable state is on disk in `.niwa/tasks/` and `.niwa/roles/`.

### Coordinator Registration

**R39** — Coordinator sessions are long-lived interactive sessions
opened by the user. They shall register via `niwa session register`
from a `SessionStart` hook, with a `SessionEntry` written to
`.niwa/sessions/sessions.json` capturing: niwa session UUID, role
(`coordinator`), PID, process start time, Claude session ID (from
`CLAUDE_SESSION_ID` env var or the discovery tiers already in the
codebase), and inbox path.

**R40** — Ephemeral workers spawned via `claude -p` shall not register
as sessions and shall not appear in `sessions.json`. Their presence is
tracked in `.niwa/tasks/<task-id>/state.json` only.

### Observability

**R41** — `niwa session list` shall list coordinator sessions only,
with columns: role, PID, liveness (`live` / `stale` / `dead`),
last-heartbeat age, pending message count.

**R42** — `niwa task list` shall list tasks with columns: task ID,
target role, state, restart count, age, delegator role, body summary
(first 200 characters of `body` rendered as a single line). It shall
accept filters `--role <role>`, `--state <state>`, `--delegator <role>`,
`--since <duration>`, composable (all filters AND together).

**R43** — `niwa task show <task-id>` shall display the task's
`envelope.json`, current `state.json`, and the full `transitions.log`
ordered by timestamp. A non-existent task ID shall cause a non-zero
exit and a `task not found: <id>` error to stderr.

**R44** — `niwa status` detail view shall include a one-line mesh
summary following the template `<queued> queued, <running> running,
<completed_24h> completed (last 24h), <abandoned_24h> abandoned (last
24h)` when channels are configured. When channels are not configured,
no mesh line shall appear.

### Non-Functional

**R45** — Mesh infrastructure shall be implemented in Go with no
external runtime dependencies beyond the Go standard library. This is
verified at build time (CI), not by acceptance criteria.

**R46** — Mesh communication shall be same-machine only for v1. The
envelope schema and tool API are designed so a future network-capable
transport can be introduced without changing the tool surface.

**R47** — All writes to `sessions.json`, per-role inboxes, per-task
state files, and the daemon PID file shall use `flock` advisory
locking for multi-writer coordination and atomic rename from a tempfile
for atomic state transitions. Under concurrent-writer stress testing
(two writers, 1000 iterations), no file shall be observed in a
partial-write state and no update shall be lost.

**R48** — All files under `.niwa/` shall be created with mode `0600`
and directories with mode `0700`, independent of umask. This shall be
verified after `niwa apply` on a process with umask set to `0000`.

**R49** — Task claim shall be at-most-once: only one worker may ever
consume a given task envelope, guaranteed by the atomic rename barrier
(R16). Terminal transitions shall be at-most-once: calling
`niwa_finish_task` on a task whose state is already terminal shall
return an error without modifying state (R24).

**R50** — Structured error responses. All niwa MCP tools shall return
structured error objects with the shape
`{status: string, error_code?: string, detail?: string, current_state?: string}`
where `status` is machine-readable, `error_code` is an enumerated
named constant, `detail` is a human-readable string, and
`current_state` is present when the error relates to task state. Named
error codes used in this PRD: `NOT_TASK_OWNER`, `NOT_TASK_PARTY`,
`TASK_ALREADY_TERMINAL`, `BAD_PAYLOAD`, `BAD_TYPE`, `UNKNOWN_ROLE`,
`MESSAGE_TOO_LARGE`.

### Test Harness

**R51** — Niwa shall provide a deterministic test harness that
decouples functional tests from live Claude Code behavior:

- A `NIWA_WORKER_SPAWN_COMMAND` environment variable overrides the
  binary invoked in place of `claude -p`. Tests set this to a scripted
  fake that calls MCP tools in a defined order.
- All timing thresholds in R34, R36, and R38 shall be overridable via
  environment variables (`NIWA_RETRY_BACKOFF_SECONDS`,
  `NIWA_STALL_WATCHDOG_SECONDS`, `NIWA_SIGTERM_GRACE_SECONDS`,
  `NIWA_DESTROY_GRACE_SECONDS`). Functional tests set these to small
  values to keep run time bounded.

Tests that assert worker behavior shall use the scripted fake, not
`claude -p` itself; the PRD's AC are written against niwa's observable
effects (state transitions, inbox contents, process lifecycle events),
not against LLM output.

### Configuration Defaults

The following values are the normative defaults. Each is configurable
per the mechanism listed; changing the default in code requires
updating this PRD.

| Setting                      | Default        | Configuration Path                                      |
|------------------------------|----------------|---------------------------------------------------------|
| Retry cap (restarts)         | 3 (4 attempts) | `[channels.mesh].retry_cap` + `--retry-cap` per-task    |
| Restart backoff              | 30s / 60s / 90s| Not configurable in v1                                   |
| Stalled-progress watchdog    | 15 minutes     | `[channels.mesh].stall_watchdog` + `NIWA_STALL_WATCHDOG_SECONDS` |
| SIGTERM grace before SIGKILL | 5 seconds      | `NIWA_SIGTERM_GRACE_SECONDS`                             |
| `niwa destroy` grace window  | 5 seconds      | `NIWA_DESTROY_GRACE_SECONDS`                             |
| `niwa_ask` timeout           | 600 seconds    | `timeout_seconds` call argument                          |
| `niwa_delegate` sync timeout | none           | Not configurable (bounded by R34/R36)                    |
| Progress summary max length  | 200 chars      | Not configurable in v1                                   |
| Message type max length      | 64 chars       | Not configurable in v1                                   |

## Acceptance Criteria

Acceptance criteria are organized by area. Each AC is independently
verifiable using the test harness described in R51. AC that reference
worker tool calls assume a scripted worker fake unless otherwise noted;
live `claude -p` is not required to verify correctness.

### Provisioning

- [ ] **AC-P1** With `NIWA_CHANNELS=1` set and no `[channels.mesh]` in
  `workspace.toml`, `niwa apply` provisions `.niwa/roles/coordinator/inbox/`,
  `in-progress/`, `cancelled/`, `expired/`, and `read/`.
- [ ] **AC-P2** With `--channels` passed and no env var or config, apply
  provisions the same structure.
- [ ] **AC-P3** With `--no-channels` passed and `NIWA_CHANNELS=1` set, apply
  provisions no mesh infrastructure and starts no daemon.
- [ ] **AC-P4** With none of the opt-in triggers, apply provisions no mesh
  infrastructure, and `niwa status` detail view contains no mesh line.
- [ ] **AC-P5** After apply on a channeled workspace, `.niwa/tasks/` exists,
  `.niwa/sessions/sessions.json` exists and is empty, and
  `.niwa/daemon.pid` contains the PID of a live process.
- [ ] **AC-P6** `<instanceRoot>/.mcp.json` exists and contains a `niwa`
  entry pointing to `niwa mcp-serve`. No per-repo mirror is written —
  Claude Code's directory-tree walk-up makes the instance-root file
  visible from sub-repo cwds.
- [ ] **AC-P7** `<instanceRoot>/.claude/skills/niwa-mesh/SKILL.md` exists
  after apply, and the same file exists at
  `<repoDir>/.claude/skills/niwa-mesh/SKILL.md` for every cloned repo.
- [ ] **AC-P8** The skill file's YAML frontmatter contains `name:
  niwa-mesh`, a `description`, and an `allowed-tools` list. The body
  contains the six section headings listed in R10.
- [ ] **AC-P9** Adding a new repo to `workspace.toml` and running apply a
  second time creates `.niwa/roles/<new-role>/inbox/` and does not
  duplicate or modify existing role directories.
- [ ] **AC-P10** Running apply a second time with one task queued
  (`.niwa/roles/<role>/inbox/<id>.json` present) and one task in progress
  (`.niwa/roles/<role>/inbox/in-progress/<id>.json` present, state.json
  shows `running`): after apply, both files are byte-identical to their
  pre-apply contents, and `.niwa/sessions/sessions.json` is byte-identical.
- [ ] **AC-P11** Running `niwa destroy` removes the instance directory
  including all mesh infrastructure, after first sending SIGTERM to the
  daemon, waiting up to the destroy-grace window, and SIGKILLing if
  needed. With `NIWA_DESTROY_GRACE_SECONDS=1` and a daemon that ignores
  SIGTERM, destroy completes within ~2 seconds (grace + cleanup).
- [ ] **AC-P12** After apply with a pre-existing
  `~/.claude/skills/niwa-mesh/SKILL.md` whose contents differ from the
  installer's output: the personal-scope file is not modified or removed,
  and the project-scope file under the instance is rewritten to the
  installer's output.
- [ ] **AC-P13** After apply on a workspace whose project-scope
  `.claude/skills/niwa-mesh/SKILL.md` has been hand-edited, a drift
  warning is emitted to stderr identifying the modified path, and the
  file is overwritten with the installer's output.
- [ ] **AC-P14** After apply, no file under `.niwa/` has a mode other
  than `0600` (files) or `0700` (directories), verified on a process
  that set `umask 0000` before running apply.
- [ ] **AC-P15** The `## Channels` section in `workspace-context.md`
  contains the session role, the instance root path, the MCP tool
  names (one per line, matching the set from R10), and the single
  pointer line from R12. It contains no other lines.

### Role Provisioning and Registration

- [ ] **AC-R1** With two explicit `[channels.mesh.roles]` entries that
  both map to non-root repo paths, apply creates both inboxes.
- [ ] **AC-R2** With two repos whose basenames collide and no explicit
  `[channels.mesh.roles]`, apply fails with a non-zero exit and a
  role-collision error to stderr.
- [ ] **AC-R3** An explicit `[channels.mesh.roles]` entry mapping
  `coordinator` to a non-root path causes apply to fail with a
  reserved-name error.
- [ ] **AC-R4** A role name containing an uppercase letter or an
  underscore is rejected by apply.
- [ ] **AC-R5** With a coordinator session registered and alive, a
  second `niwa session register` call targeting the same role returns
  non-zero and identifies the conflicting PID.
- [ ] **AC-R6** With `NIWA_SESSION_ROLE=web` set inside a session
  opened at the instance root, `niwa session register` returns non-zero
  with an error identifying the reserved-root constraint.

### Delegation

- [ ] **AC-D1** `niwa_delegate(to="web", body={...}, mode="async")`
  called from a coordinator session returns a task ID within 100 ms
  and creates both `.niwa/tasks/<task-id>/envelope.json` and
  `.niwa/roles/web/inbox/<task-id>.json` before returning.
- [ ] **AC-D2** Given a test-harness worker that calls
  `niwa_check_messages` on start: after
  `niwa_delegate(to="web", mode="async")`, a worker process is spawned
  by the daemon (observable via process table or test spawn hook), and
  the first `niwa_check_messages` call from that worker returns the
  task envelope with body matching the delegator's input.
- [ ] **AC-D3** The daemon's consumption rename occurs after the
  envelope appears in the inbox and before the worker process is
  spawned (verified by event-ordering assertion on the test spawn
  hook, not by wall-clock bound).
- [ ] **AC-D4** The daemon spawns `claude -p` with working directory
  set to the role's repo directory, `--permission-mode=acceptEdits`,
  `--mcp-config=<instanceRoot>/.mcp.json`, and
  `--strict-mcp-config` (verified via the test spawn hook's captured
  argv and env).
- [ ] **AC-D5** The spawn prompt argv does not contain any field from
  the task's `body` (verified by checking that argv is the fixed
  bootstrap string with only `<task-id>` substituted).
- [ ] **AC-D6** After a scripted worker calls
  `niwa_finish_task(task_id, outcome="completed", result={"ok": true})`,
  the task's `state.json` reads `completed`, a `task.completed` message
  appears in the coordinator's inbox carrying the result, and
  `niwa_query_task` returns `state: "completed"` with that result.
- [ ] **AC-D7** `niwa_delegate(mode="sync")` blocks the tool call until
  the scripted worker calls `niwa_finish_task` with `outcome="completed"`,
  then returns `{status: "completed", result: ...}`.
- [ ] **AC-D8** `niwa_delegate(mode="sync")` returns
  `{status: "abandoned", reason: ..., restart_count: n}` when the
  scripted worker calls `niwa_finish_task` with `outcome="abandoned"`
  instead of completing.
- [ ] **AC-D8a** A scripted worker that calls
  `niwa_finish_task(outcome="completed")` without a `result` field, or
  `niwa_finish_task(outcome="abandoned")` without a `reason` field,
  receives `{status: "invalid", error_code: "BAD_PAYLOAD"}` and the
  task's state does not transition.
- [ ] **AC-D9** `niwa_delegate(mode="sync")` returns
  `{status: "cancelled"}` when the delegator cancels before the daemon
  consumes the task (two-step: delegate async to get task_id, delay
  daemon via fsnotify pause hook, cancel, resume).
- [ ] **AC-D10** `niwa_await_task(task_id)` called from the delegator
  after a prior async delegation blocks until the task reaches a
  terminal state and returns the same payload shape as
  `niwa_query_task`.
- [ ] **AC-D11** `niwa_await_task(task_id, timeout_seconds=2)` with a
  scripted worker that never completes returns
  `{status: "timeout", current_state: "running"}` after ~2 seconds
  and does not cancel the task (verified: the task continues toward
  its next state transition).
- [ ] **AC-D12** `niwa_query_task(task_id)` returns state, transitions
  history, restart count, and last progress without blocking, at any
  point in the lifecycle.
- [ ] **AC-D13** `niwa_query_task(task_id)` called by a role that is
  neither the delegator nor the executor returns
  `{status: "forbidden", error_code: "NOT_TASK_PARTY"}`.
- [ ] **AC-D14** `niwa_await_task(task_id)` called by a role that is
  not the delegator returns
  `{status: "forbidden", error_code: "NOT_TASK_OWNER"}`.
- [ ] **AC-D15** After a scripted worker calls `niwa_report_progress`,
  a `task.progress` message lands in the delegator's inbox within 5
  seconds (observable via inbox file appearance; threshold is
  configurable via test harness).
- [ ] **AC-D16** A scripted worker that calls `niwa_report_progress`
  with a 250-character summary causes the delivered message to contain
  a 200-character summary ending in `…`.
- [ ] **AC-D17** `niwa_report_progress` returns after the event is
  appended to `.niwa/tasks/<task-id>/transitions.log` but may return
  before the inbox message is delivered (the return does not block on
  the inbox write).
- [ ] **AC-D18** A delegate call made from within a running worker
  populates `parent_task_id` in the new task's envelope with the
  current task's ID.

### Queue Mutation

- [ ] **AC-Q1** `niwa_list_outbound_tasks()` from role A returns only
  tasks whose envelope `from.role == "A"`, even when tasks delegated
  by role B exist.
- [ ] **AC-Q2** `niwa_list_outbound_tasks(to="web", status="queued")`
  returns only tasks matching both filters.
- [ ] **AC-Q3** `niwa_update_task(task_id, body)` on a queued task
  returns `{status: "updated"}` and overwrites the body in both
  `.niwa/tasks/<task-id>/envelope.json` and the queued inbox file.
- [ ] **AC-Q4** A worker spawned after `niwa_update_task` has succeeded
  receives the updated body on its first `niwa_check_messages`.
- [ ] **AC-Q5** `niwa_update_task` on a task whose state is `running`
  returns `{status: "too_late", current_state: "running"}` and does
  not modify any files.
- [ ] **AC-Q6** `niwa_update_task` called by a role that is not the
  task's delegator returns
  `{status: "forbidden", error_code: "NOT_TASK_OWNER"}`.
- [ ] **AC-Q7** `niwa_cancel_task(task_id)` on a queued task returns
  `{status: "cancelled"}`, moves the envelope from
  `.niwa/roles/<role>/inbox/<id>.json` to
  `.niwa/roles/<role>/inbox/cancelled/<id>.json`, and transitions the
  task state to `cancelled`.
- [ ] **AC-Q8** `niwa_cancel_task` on a task whose state is `running`
  returns `{status: "too_late", current_state: "running"}`.
- [ ] **AC-Q9** `niwa_cancel_task` called by a non-delegator returns
  `{status: "forbidden", error_code: "NOT_TASK_OWNER"}`.
- [ ] **AC-Q10** **Race window**: With a fsnotify pause hook holding
  the daemon just after it has read the inbox listing but before it
  has performed the consumption rename, `niwa_cancel_task` succeeds
  with `{status: "cancelled"}` and the daemon, on resume, observes
  the missing file and skips the spawn. Inversely, with the daemon
  holding just after the rename, `niwa_cancel_task` returns
  `{status: "too_late"}` and the worker spawns normally.
- [ ] **AC-Q11** **Update race**: Symmetric to AC-Q10 — update succeeds
  if queued at rename time, returns `{status: "too_late"}` if the
  daemon has already renamed.

### Lifecycle and Restart

- [ ] **AC-L1** A scripted worker that exits with code 0 without
  calling `niwa_finish_task` causes the daemon to spawn a replacement
  worker; the task's `restart_count` in `state.json` increments to 1.
- [ ] **AC-L2** A scripted worker that exits with a non-zero code
  without calling `niwa_finish_task` triggers a restart; behavior is
  identical to AC-L1 regardless of exit code.
- [ ] **AC-L3** After 3 restart attempts (4 total worker spawns) for a
  single task all exit without completion, the task transitions to
  `abandoned` with `reason: "retry_cap_exceeded"`, a `task.abandoned`
  message lands in the delegator's inbox carrying the reason and
  `restart_count: 3`, and `niwa_query_task` returns
  `state: "abandoned"` with the same reason and count.
- [ ] **AC-L4** With `NIWA_STALL_WATCHDOG_SECONDS=2` and a scripted
  worker that runs indefinitely without emitting progress, the daemon
  sends SIGTERM to the worker after ~2 seconds without progress. With
  a worker that ignores SIGTERM, SIGKILL is sent after
  `NIWA_SIGTERM_GRACE_SECONDS`. The resulting exit is counted as an
  unexpected exit (restart count increments).
- [ ] **AC-L5** Backoff timing: with `NIWA_RETRY_BACKOFF_SECONDS=1,2,3`
  (override), three successive restarts spawn their workers at ~1s,
  ~2s, and ~3s after the prior exit, measured from state.json
  transition timestamps.
- [ ] **AC-L6** `niwa_finish_task(task_id, outcome="abandoned", reason=...)`
  from a worker transitions the task to `abandoned`, writes a
  `task.abandoned` message carrying the provided reason to the
  delegator's inbox, and does not increment `restart_count`. No
  replacement worker is spawned.
- [ ] **AC-L7** `niwa_finish_task(outcome="completed", result=...)`
  called a second time on an already-completed task returns
  `{status: "already_terminal", error_code: "TASK_ALREADY_TERMINAL",
  current_state: "completed"}` and does not modify `state.json`.
- [ ] **AC-L8** `niwa_finish_task` called after a worker has already
  written `state: "completed"` atomically (via a prior
  `niwa_finish_task` call in another process that then crashed) is
  treated identically to AC-L7 — the state file is authoritative and
  the second call is rejected regardless of its `outcome` argument.
- [ ] **AC-L9** **Daemon crash with live worker**: After killing the
  daemon with SIGKILL (leaving a running worker's PID alive), starting
  a new daemon causes the new daemon to adopt the orphan: state.json
  gains an `adopted_at` field, the task remains in `running`, and when
  the worker eventually calls `niwa_finish_task` with
  `outcome="completed"` the task transitions to `completed` as usual.
- [ ] **AC-L10** **Daemon crash with dead worker**: After killing
  both the daemon and the worker, starting a new daemon causes the
  task to be treated as an unexpected exit; the restart policy applies
  (restart_count increments, replacement spawned per backoff).
- [ ] **AC-L11** **Daemon reconciliation of queued tasks**: After a
  daemon crash with an envelope sitting in
  `.niwa/roles/<role>/inbox/<id>.json`, starting a new daemon
  proceeds to consume and spawn normally (verified by the spawn
  event).

### Peer Messaging

- [ ] **AC-M1** `niwa_ask(to="coordinator", body={...})` from a running
  worker blocks until the coordinator calls `niwa_send_message` with
  matching `reply_to`, then returns the reply body. Verified via two
  scripted MCP clients.
- [ ] **AC-M2** `niwa_ask(to="reviewer", body={...})` against a role
  with no running worker creates an ask-task (visible in
  `niwa task list` with state `queued` then `running`), spawns a
  worker via the same spawn path as delegation, and returns the
  reply body when the spawned worker calls `niwa_send_message` with
  matching `reply_to`.
- [ ] **AC-M3** `niwa_ask(timeout_seconds=2)` with a scripted peer
  that never replies returns `{status: "timeout"}` after ~2s. The
  ask-task's underlying task is not cancelled.
- [ ] **AC-M4** `niwa_send_message(to="web", type="task.progress",
  body={...})` writes a file to `.niwa/roles/web/inbox/` and
  `niwa_check_messages` called from role `web` returns it. After the
  call returns, the message file has been moved to
  `.niwa/roles/web/inbox/read/`.
- [ ] **AC-M5** `niwa_send_message(type="NotValid")` (uppercase)
  returns `{status: "invalid", error_code: "BAD_TYPE"}` and writes
  no file.
- [ ] **AC-M6** `niwa_send_message(to="unknown-role")` returns
  `{status: "rejected", error_code: "UNKNOWN_ROLE"}` and writes no
  file.
- [ ] **AC-M7** `niwa_send_message` with `reply_to` set correlates
  to an earlier `niwa_ask`: the receiving peer's `niwa_ask` call
  returns the body of this message.
- [ ] **AC-M8** A message with `expires_at` set in the past is not
  returned by `niwa_check_messages`; it is moved to `inbox/expired/`
  on the next read.
- [ ] **AC-M9** Peer messaging works for every role pair:
  coordinator↔web, web↔backend, etc. Verified by a matrix test that
  sends a message for each pair in a minimal three-role workspace.

### Observability

- [ ] **AC-O1** `niwa session list` shows the coordinator entry only;
  after a worker has run, `sessions.json` on disk contains no worker
  entry.
- [ ] **AC-O2** `niwa session list` columns are: role, PID, liveness,
  last-heartbeat age, pending message count (verified by matching the
  header row).
- [ ] **AC-O3** `niwa task list` with no filters shows all tasks in
  all states.
- [ ] **AC-O4** `niwa task list --state running` shows only tasks with
  state `running`.
- [ ] **AC-O5** `niwa task list --role web` shows only tasks where
  `to.role == "web"`.
- [ ] **AC-O6** `niwa task list --delegator coordinator` shows only
  tasks where `from.role == "coordinator"`.
- [ ] **AC-O7** `niwa task list --since 1h` shows only tasks whose
  `sent_at` is within the last hour.
- [ ] **AC-O8** `niwa task list --role web --state queued` shows
  tasks satisfying both filters (AND semantics).
- [ ] **AC-O9** `niwa task show <task-id>` displays `envelope.json`,
  `state.json`, and the transitions log; a non-existent ID exits
  non-zero with "task not found: <id>" on stderr.
- [ ] **AC-O10** `niwa status` detail view on a channeled workspace
  contains a line matching the template in R44; on a non-channeled
  workspace the line is absent.

### Concurrency and Durability

- [ ] **AC-C1** Under concurrent-writer stress (two processes each
  issuing 1000 `niwa_report_progress` calls for the same task), no
  `state.json` read shows partial content and no progress event is
  lost from `transitions.log`.
- [ ] **AC-C2** Under concurrent `niwa session register` calls from
  two coordinator-role processes, exactly one succeeds and
  `sessions.json` reflects the winning registration without
  corruption.
- [ ] **AC-C3** A second `niwa apply` attempted while the first is
  running (flock on daemon PID file) blocks or errors cleanly; two
  daemons are never spawned.

## Out of Scope

- **Ad-hoc roles**: V1 roles map 1:1 to cloned repos plus the hardcoded
  `coordinator`. Arbitrary role names without a corresponding repo (e.g.
  `reviewer`) are not supported as standalone roles; ask-spawned workers
  use the target role's repo directory.
- **Deadline enforcement**: The `deadline_at` field is recorded in the
  envelope but not actively enforced in v1. Tasks do not auto-abandon
  when `deadline_at` passes; the stalled-progress watchdog is the
  only time-based abandonment mechanism.
- **Task directory retention / garbage collection**: Task directories
  under `.niwa/tasks/` accumulate indefinitely in v1. A retention policy
  and `niwa mesh gc` command are deferred.
- **Optimistic concurrency tokens on `niwa_update_task`**: Concurrent
  updates by the same sender may overwrite each other. No etag or
  version token in v1.
- **Message body size limits**: Not enforced in v1. The envelope, task
  body, and message body may be of any size the filesystem accepts.
  Enforcement (including the `MESSAGE_TOO_LARGE` error code, reserved
  in R50 for future use) is deferred.
- **Artifact path escape hatch**: The `artifact_path` field for large
  bodies is not implemented; bodies are always inline.
- **Network / cross-machine transport**: Same-machine only for v1.
- **Cross-workspace routing**: Sessions in different niwa instances on
  the same machine cannot message each other.
- **Message encryption and agent authentication**: Messages are
  plaintext JSON under `0600` permissions. Role integrity is the trust
  boundary (see Known Limitations).
- **In-flight task cancellation**: V1 cancellation applies only to
  tasks still in `queued` state.
- **Human-in-the-loop approval before spawning workers**: The
  coordinator's dispatch is trusted; niwa spawns without prompting the
  user.
- **Live observability during delegation** (dashboard, `niwa mesh
  tail`): The user sees progress through the coordinator's chat only.
- **Multiple parallel workers per role**: At most one worker per role
  runs at a time.
- **Native Claude Code Channels push**: Not a v1 design constraint.
- **Fan-out / broadcast delivery**.
- **Hook-based wakeup of running coordinators**: R22 specifies inbox
  delivery of progress only; automatic injection into live coordinator
  sessions is not required.
- **Per-task stalled-progress threshold**: v1 uses a single workspace-
  level default; per-task override is deferred.

## Open Questions

None.

## Known Limitations

- **Sequential per-role execution**: Tasks for the same role queue and
  run one at a time.
- **Worker context carryover**: Each worker is a fresh `claude -p`
  process; it has no memory of prior workers. Delegators must thread
  cross-task context through each task's body.
- **Coordinator liveness constraints the user experience**: The
  coordinator must be alive for `niwa_delegate(mode="sync")` and
  `niwa_await_task` to return to the user. If the user closes the
  coordinator, results accumulate in the inbox for surfacing on next
  session start, and can be retrieved via `niwa_query_task` at any
  time.
- **Daemon does not survive machine restarts**: After reboot or logout,
  `niwa apply` restarts the daemon. Tasks that were `running` at reboot
  are reconciled per R37 when the daemon returns.
- **No in-flight cancellation**: A delegator that wants to stop a
  running task must wait for the worker to exit or the stall watchdog
  to fire.
- **`niwa destroy` is required for clean removal**: Deleting an
  instance directory with `rm -rf` leaves the daemon running until
  fsnotify detects the missing watched directory.
- **Completion is a behavioral contract**: The skill instructs workers
  to call `niwa_finish_task` before exiting. A worker that ignores the
  skill and simply finishes its prompt will be treated as an unexpected
  exit. The restart cap bounds the blast radius.
- **Role integrity is the only trust boundary**: Niwa verifies that
  authorization-gated tools are called by the task's delegator or
  executor by matching the caller's registered role. An agent that
  overrides `NIWA_SESSION_ROLE` to impersonate another role can
  update, cancel, or query tasks belonging to the spoofed role. V1
  does not implement cryptographic message signing or per-agent keys.
- **Hook-injected wake of live coordinators is not guaranteed**: R22
  requires only inbox delivery of progress. A coordinator that is
  mid-tool-call receives progress on its next `niwa_check_messages`
  or the next message-surfacing boundary. There is no "push into a
  live session" mechanism in v1.
- **Adopted orphan polling**: When a new daemon adopts an orphaned
  worker after a daemon crash, it cannot `waitpid` (the original
  parent is gone) and falls back to a PID liveness check at interval
  ≤ 5 seconds. Exit detection for adopted orphans is therefore
  coarser than for daemon-spawned workers.
- **Task directories accumulate**: Without v1 garbage collection, a
  long-lived workspace will see `.niwa/tasks/` grow unbounded. Manual
  cleanup via `rm` is the workaround.
- **`acceptEdits` amplifies prompt-injection blast radius**: Workers are
  spawned with `--permission-mode=acceptEdits` so background processes
  don't block on permission prompts. A worker whose LLM is prompt-
  injected via a malicious task body can therefore write to any file in
  the role's repo directory without prompting — divergent from normal
  Claude Code sessions where edits require confirmation. Users who need
  per-edit confirmation should not enable channel delegation. This is a
  data-plane risk reachable through the feature's intended interface
  (delegation bodies), not a same-UID attacker scenario. Per-role
  permission-mode overrides are deferred to v2.
- **macOS worker authentication is strictly weaker than Linux**: The
  design's worker-auth check walks PPID up one level and compares the
  worker's start time against `state.json.worker.{pid, start_time}`. On
  Linux this defeats naive same-UID PID spoofing. On macOS, `PIDStartTime`
  returns a conservative alive/dead answer without a precise timestamp,
  so the check degrades to PID-match-only. The PRD's "role integrity"
  trust ceiling still holds, but macOS users face strictly weaker
  same-UID process isolation than Linux users. Users requiring the
  strongest local-process isolation should run niwa on Linux.
- **`transitions.log` body retention**: Progress and result bodies are
  appended to `.niwa/tasks/<id>/transitions.log` as part of the audit
  trail. With no v1 garbage collection, this log accumulates all
  delegation output indefinitely. The v1 mitigation is to log only the
  truncated `summary` field from progress events (full bodies remain in
  `state.json.last_progress.body`, which is overwritten per-event);
  terminal bodies (result / reason) are also logged. Users handling
  sensitive delegation content should manually clean completed task
  directories or exclude `.niwa/` from backups. `niwa mesh gc` is v2.
- **Channel delegation cross-pollinates shell env across roles**: The
  daemon inherits the user's shell environment (including credentials
  like `ANTHROPIC_API_KEY`, `AWS_*`, `GH_TOKEN`) and passes it through to
  every worker regardless of role. A repo-scoped secret intended for one
  role is therefore visible to workers in every other role. Per-role
  env filtering is deferred to v2; for v1, treat all shell-exported
  secrets as visible to every delegated worker.

## Decisions and Trade-offs

**Two worker-side lifecycle tools, not three**

Worker-side lifecycle is split on the structural boundary between
non-terminal and terminal transitions: `niwa_report_progress` for
non-terminal updates, `niwa_finish_task(outcome, ...)` for terminal
transitions. An earlier draft had three tools (`niwa_report_progress`,
`niwa_complete_task`, `niwa_fail_task`). The three-tool design was
rejected because completion and abandonment are structurally symmetric
(both terminal, both carry a body, both share the
`TASK_ALREADY_TERMINAL` error path), so splitting them duplicated
machinery without adding clarity at the call site. A single-tool design
(`niwa_update_task_state(kind, ...)`) was also rejected: LLMs pick the
right tool more reliably when each has an unambiguous name, and a single
tool puts one more decision on the agent per call. The two-tool design
splits where the structure actually differs and is the Anthropic-idiomatic
middle ground.

**Task as a first-class object over message-as-task convention**

The prior PRD modeled tasks as a convention on top of typed messages. That
approach had two weaknesses: completion was implicit (a `task.result`
message was conventional but not contractual), and there was no way to
query "is task X done?" without scanning inboxes. Making tasks first-class
— with a per-task directory, explicit state, and explicit completion
tool — gives the delegator a handle it can query and block on, and gives
niwa a clear point at which to decide "the worker exited without
completing; restart or abandon." The cost is a new storage structure
(`.niwa/tasks/`) and a larger tool surface. Worth it for the UX
simplification.

**Delivering task envelope via inbox, not `claude -p` argv**

`claude -p` invocation carries a bootstrap prompt only. The full task
envelope lives in the worker's inbox and is retrieved on the worker's
first tool call. Alternatives considered:

- *JSON envelope in argv*: simpler but opens a prompt-injection surface;
  task body and control-plane instructions share the same text.
- *Stdin*: blocked by an upstream `claude -p` bug where stdin input above
  ~7KB returns empty output. Not viable.
- *Environment variables*: requires a launcher tool and is bounded by
  `ARG_MAX`.

The chosen approach also unifies the spawn path with a future resume path.

**Atomic-rename consumption barrier**

The daemon claims a queued task by renaming its envelope from
`inbox/<id>.json` to `inbox/in-progress/<id>.json`. Cancellation uses the
same pattern (rename to `inbox/cancelled/<id>.json`). This gives
`niwa_cancel_task` two clean outcomes — `cancelled` if its own rename
succeeds, `too_late` if the file is no longer there — with no partial
state.

**Default retry cap: 3 restarts (4 attempts)**

Established job-queue systems use a range of defaults. Three balances two
failure modes: transient issues which should recover without user
intervention, and genuinely stuck tasks which should abandon quickly
rather than loop. Overridable at workspace and per-task scope.

**Abandonment surfaces on both push and pull**

`task.abandoned` message in the delegator's inbox (push) plus
`niwa_query_task` returning `abandoned` (pull). The push matches the
auto-surfaced-progress principle; the pull is the backstop for a
delegator that was offline when the abandonment happened.

**Sender-only ownership of queue mutation**

Only the task's delegator can list, update, or cancel it. The trust
boundary is role integrity in `sessions.json` — niwa verifies the caller's
role matches the envelope's `from.role`. No cryptographic signing in v1.

**Niwa's opinionated surface is the tool API and the task state machine**

Message vocabulary, progress cadence, dispatch patterns — all skill-owned,
not niwa-owned. Users override by placing a skill at the personal scope.
This keeps niwa small and the behavioral layer user-modifiable.

**Workers are ephemeral and unregistered**

A worker is a one-shot `claude -p` process. It does not appear in
`sessions.json`. Its lifetime is bounded by a single task. This sidesteps
the MCP-push-to-idle-sessions bug (workers cannot be idle) and simplifies
registration: the only long-lived session is the coordinator.

**Per-role inboxes, not per-session-UUID inboxes**

The prior PRD keyed inboxes by session UUID, which meant a message to
`web` had no destination until some Claude registered as `web`. The
revised PRD keys inboxes by role and creates them at apply time from the
workspace topology. A task can be queued for `web` before any worker has
ever existed; the daemon spawns one to consume it.

**Worker spawn injects permission-mode and strict-mcp-config**

A background worker cannot answer permission prompts, so `claude -p` is
spawned with `--permission-mode=acceptEdits`. Workers use
`--mcp-config=<instanceRoot>/.mcp.json --strict-mcp-config` to
guarantee the niwa MCP server is present and no user-level MCP servers
leak into the worker. Without these flags, workers hang on first write
or see an indeterminate MCP tool set.

**Test harness via `NIWA_WORKER_SPAWN_COMMAND` and timing overrides**

Functional tests substitute the `claude -p` invocation with a scripted
worker fake and set all timing thresholds to small values. This is the
only way the AC can be verified deterministically; relying on live Claude
introduces LLM-output non-determinism that makes "the worker's first tool
call is X" untestable. The test harness requirement (R51) makes this an
explicit niwa surface rather than an external test-framework concern.

**Channels forward-compatibility is a soft goal, not a hard requirement**

The prior PRD required the v1 design to be forward-compatible with Claude
Code's native Channels protocol. The revised PRD downgrades this to a
design preference: the tool API is independent of the wakeup mechanism,
so a future Channels-based wakeup is possible without breaking the tool
surface. Niwa does not actively forward-declare Channels support.

**`niwa_ask` creates a first-class task**

`niwa_ask` to an idle role was an ambiguous design point: is it a
distinct concept from delegation, or a flavor of it? The revision makes
it a first-class task with `body.kind = "ask"`. This means ask-spawned
workers participate in the restart policy, the watchdog, `niwa task
list`, and all the durability guarantees. The alternative (a parallel
"question" surface with its own lifecycle) would double the machinery.
