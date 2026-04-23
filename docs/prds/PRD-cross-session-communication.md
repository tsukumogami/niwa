---
status: Draft
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
  lifecycle (queued, running, completed, abandoned); completion is an explicit
  worker action, not a process-exit side effect. Delegators can dispatch
  synchronously, dispatch asynchronously and later query or block on
  completion, and receive progress automatically while work is in flight.
  Senders can inspect, update, or cancel tasks they queued before a worker
  consumes them. Niwa ships a default behavior skill installed into every
  agent so delegation, reporting, and completion follow the same contract out
  of the box, with user overrides at the personal scope.
---

# PRD: Cross-Session Communication

## Status

Draft

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
`backend`), I want to dispatch both without blocking, go on to reason about
something else, and later block until both have reached a terminal state, so
that I can parallelize cross-repo work without polling.

**US3 — Coordinator delegates synchronously**
As a coordinator with a single task whose outcome I need before I can
continue, I want to dispatch it and block until it finishes, so that my
next action naturally depends on the result without managing task IDs myself.

**US4 — Delegator receives progress while a task runs**
As a delegator that has dispatched async work, I want progress events from
each worker to arrive in my inbox as they happen, so that I can tell the user
"backend is scaffolding the schema" at the moment it's happening, without
polling or querying.

**US5 — Sender cancels a task it queued**
As a delegator that has queued a task for `backend`, I want to cancel that
task if the consumer has not yet picked it up, so that I can correct an
earlier mistake without a worker doing unnecessary or wrong work. Once a
worker has started the task, cancellation is no longer available in v1.

**US6 — Sender updates a queued task's body**
As a delegator that has queued a task with an incomplete prompt, I want to
update the task's body before the consumer picks it up, so that the worker
sees my corrected instruction rather than the original.

**US7 — Worker asks a peer mid-task**
As a worker executing a delegated task in the `web` repo, I want to ask the
coordinator for a clarifying decision and block until I receive the answer,
so that I can proceed with authority rather than guess.

**US8 — Niwa restarts a worker that exited without completing**
As a delegator, I want niwa to automatically restart workers that exit
without reporting completion, up to a bounded cap, so that a transient Claude
failure doesn't abandon my task, and a genuinely stuck task doesn't loop
indefinitely.

**US9 — User inspects mesh state**
As a developer running a multi-session workspace, I want `niwa task list` to
show me every task with its role, status, restart count, and age, so that I
can see at a glance what's in flight and what's stuck.

## Requirements

### Provisioning

**R1** — The presence of a `[channels.mesh]` section in `workspace.toml`, the
`--channels` flag on `niwa create`/`niwa apply`, or the `NIWA_CHANNELS=1`
environment variable shall opt an instance into mesh provisioning. The flag
overrides the env var, which overrides config. `--no-channels` suppresses
provisioning regardless of env or config. A workspace without any of these
triggers shall provision no mesh infrastructure.

**R2** — `niwa create` and `niwa apply` shall provision the mesh infrastructure
at the instance root:

- `.niwa/roles/<role>/inbox/` — per-role task queue for every known role
- `.niwa/roles/<role>/inbox/in-progress/` — holding area for claimed tasks
- `.niwa/tasks/` — per-task directories keyed by task ID
- `.niwa/sessions/sessions.json` — coordinator registry (workers are not
  registered here)
- `.niwa/daemon.pid` and `.niwa/daemon.log` — daemon lifecycle artifacts

Every written path shall be tracked in `InstanceState.ManagedFiles` so that
drift detection and `niwa destroy` work uniformly.

**R3** — A channel installer function shall run in the provisioning pipeline
after repository clones are complete and before per-repo materializers. It
shall be invoked imperatively from `Applier.runPipeline` and shall not
implement the `Materializer` interface. (The provisioning pipeline is an
internal detail; this requirement names the integration point, not the
abstraction.)

**R4** — The channel installer shall write `<instanceRoot>/.claude/.mcp.json`
declaring a `niwa` MCP server entry that invokes `niwa mcp-serve` with
`NIWA_INSTANCE_ROOT` baked in. The installer shall also mirror the `.mcp.json`
into each repository's `.claude/` directory so that Claude sessions opened
inside a repo pick up the same MCP server.

**R5** — The channel installer shall enumerate the full set of roles from
`workspace.toml` and the cloned repositories, and create a per-role inbox
directory for every role, including `coordinator`, before any worker is
spawned. A role's inbox shall be created when the installer first sees the
role, whether that is on the initial `niwa apply` or a later one after new
repos are added.

### Roles and Topology

**R6** — Roles shall be derived from the workspace topology at apply time by
the following precedence (highest to lowest):

1. Explicit entries in `[channels.mesh.roles]` in `workspace.toml` (role name
   → repo path relative to instance root; `""` designates the instance root)
2. Auto-derivation: the hardcoded `coordinator` role for the instance root;
   one role per cloned repo, named from the repo's directory basename

At session-start time, an individual session may override its assigned role
via `NIWA_SESSION_ROLE`. Role names shall match `^[a-z0-9][a-z0-9-]{0,31}$`.
The `coordinator` name is reserved for the instance root.

**R7** — Only one session may be registered under a given role at a time. The
coordinator role enforces this via the existing session registration flow (a
session at the instance root registers as `coordinator` on start and the
second attempt fails until the first unregisters). Worker roles are
ephemeral: at most one worker process per role may be running at any moment,
enforced by the daemon's single-consumer claim on the role's inbox.

### Skill Installation and Behavior Spec

**R8** — The channel installer shall install a `niwa-mesh` skill at
`<instanceRoot>/.claude/skills/niwa-mesh/` and mirror it into each
`<repoDir>/.claude/skills/niwa-mesh/`. The skill artifact is a directory
containing a `SKILL.md` with YAML frontmatter (`name`, `description` front-
loaded with when-to-use guidance under the Claude Code combined-length cap,
and `allowed-tools` listing the niwa MCP tools) followed by the behavior
body. All installed paths shall be tracked in `InstanceState.ManagedFiles`.

**R9** — The `niwa-mesh` skill body shall describe the default behavior for
agents in the mesh: how to dispatch tasks (sync vs async), when to report
progress, the completion contract (explicit completion or failure call), the
default message vocabulary (task delegation, progress, completion, peer
questions), and patterns for common coordinator and worker scenarios.
Behavioral guidance that existed in the prior PRD's `workspace-context.md`
`## Channels` section (immediate-check-on-wake, progress cadence,
response-via-tool-call, type vocabulary) shall live in this skill body.

**R10** — `niwa apply` shall overwrite the installed skill unconditionally and
emit a drift warning when the on-disk content differs from what the installer
would write. Users override the skill at the personal scope by placing a copy
at `~/.claude/skills/niwa-mesh/`, which Claude Code's standard resolution
orders ahead of the project-installed skill. Niwa shall not implement
override-detection logic.

**R11** — The `## Channels` section in `workspace-context.md` shall contain
only: the session's assigned role, the instance root path, the names of the
niwa MCP tools, and a pointer to the `niwa-mesh` skill. No behavioral
prescriptions, message-vocabulary lists, or cadence recommendations shall
appear in `workspace-context.md`.

### Task Lifecycle

**R12** — A task shall be a first-class object with state transitions
`queued → running → completed | abandoned | cancelled`. A task is materialized
as a directory at `<instanceRoot>/.niwa/tasks/<task-id>/` containing:

- `envelope.json` — delegation payload and metadata (immutable after the task
  transitions to `running`)
- `state.json` — current state, transition timestamps, restart count, last
  progress summary
- `transitions.log` — append-only history of all state changes and progress
  events

State changes shall use per-task `flock` plus atomic rename of `state.json`
to prevent concurrent-write corruption.

**R13** — The consumption barrier shall be an atomic rename of the queued
envelope from `.niwa/roles/<role>/inbox/<task-id>.json` to
`.niwa/roles/<role>/inbox/in-progress/<task-id>.json`. Only the daemon
performs this rename, and only when it is spawning a worker to consume the
task. The rename is the point at which the task's state transitions from
`queued` to `running`.

**R14** — Completion shall be explicit. A task transitions to `completed`
only when the worker calls `niwa_complete_task(task_id, result)`. Process
exit — regardless of exit code — shall not complete a task. A worker that
exits with code zero without calling `niwa_complete_task` is treated as an
unexpected exit (R30).

**R15** — Explicit failure shall be available via
`niwa_fail_task(task_id, reason)`. This transitions the task to `abandoned`
immediately without consuming a restart slot, and writes a `task.abandoned`
message to the delegator's inbox.

### Delegator Tools

**R16** — `niwa_delegate(to, body, mode)` shall dispatch a task. `mode` shall
be `"sync"` or `"async"` (default `"async"`). In `async` mode the tool shall
return immediately with a `task_id`. In `sync` mode the tool shall block
until the task reaches a terminal state (`completed`, `abandoned`, or
`cancelled`) and shall return the final result on completion or a structured
error on abandonment or cancellation. Niwa shall generate the `task_id`
server-side and shall populate `from.role`, `sent_at`, and `parent_task_id`
(if the caller is itself executing a task) automatically.

**R17** — `niwa_query_task(task_id)` shall return the current task state
without blocking. The response shall include: state, state-transition
history summary (counts per state), restart count, most recent progress event
body, and (if terminal) the result or abandonment reason. Any agent may
query any task it delegated or is executing; agents shall not query tasks
they are not a party to.

**R18** — `niwa_await_task(task_id, timeout)` shall block until the specified
task reaches a terminal state, then return the same payload shape as
`niwa_query_task`. `timeout` shall be optional (default: no timeout). Only
the delegator of a task may await it.

**R19** — Progress events emitted by a worker shall be automatically written
as `task.progress` messages to the delegator's inbox, in addition to being
appended to the task's `transitions.log`. This is the auto-surfaced-progress
mechanism: the delegator sees progress on its next `niwa_check_messages`
call, its next hook-injected prompt, or its next response to a blocking
tool — whichever comes first.

### Worker Tools

**R20** — `niwa_report_progress(task_id, summary, body)` shall emit a
progress event. `summary` is a short human-readable string (recommended ≤
200 characters); `body` is an optional structured payload. Niwa shall append
the event to the task's `transitions.log` and deliver a `task.progress`
message to the delegator's inbox. Non-blocking. The cadence of progress
reports is a skill-owned behavioral concern (R9), not a niwa requirement.

**R21** — `niwa_complete_task(task_id, result)` shall be the sole valid
completion signal for a task. `result` is a structured body. Niwa shall
write a `task.completed` message containing the result to the delegator's
inbox, transition the task to `completed`, and make the result visible to
`niwa_query_task` and `niwa_await_task`.

**R22** — `niwa_fail_task(task_id, reason)` shall transition a task to
`abandoned` without triggering a restart attempt. Niwa shall write a
`task.abandoned` message to the delegator's inbox including the reason.

### Queue Mutation Tools

**R23** — `niwa_list_outbound_tasks(to, status)` shall return tasks the
caller delegated, optionally filtered by target role and/or status. The
response shall include task ID, target role, state, age, and a truncated
summary of the body.

**R24** — `niwa_update_task(task_id, body)` shall replace a queued task's
body with a new one. It shall succeed (`{status: "updated"}`) only if the
task is still in `queued` state when the update is attempted. If the task
has already been consumed (state is `running` or terminal), the tool shall
return `{status: "too_late", current_state: <state>}`. Only the task's
delegator may update it.

**R25** — `niwa_cancel_task(task_id)` shall cancel a queued task. It shall
succeed (`{status: "cancelled"}`) only if the task is still in `queued`
state, implemented as an atomic rename of the envelope out of
`.niwa/roles/<role>/inbox/` into `.niwa/tasks/<task-id>/` under a
`cancelled/` marker. If the consumption rename (R13) has already occurred,
`niwa_cancel_task` shall return `{status: "too_late", current_state:
<state>}`. In-flight cancellation of running tasks is out of scope for v1.
Only the task's delegator may cancel it.

### Peer Messaging

**R26** — `niwa_ask(to, body, timeout)` shall be a blocking request-reply
tool. Any agent may ask any other agent. When the target role has no
running worker and is not the coordinator, niwa shall spawn a worker via
`claude -p` to handle the question, using the same mechanism as task
delegation. The target responds via `niwa_send_message` with a matching
`reply_to`, and niwa unblocks the waiting caller. Timeout default is 10
minutes.

**R27** — `niwa_send_message(to, type, body, reply_to)` shall dispatch a
one-way peer message. The sender does not wait for a reply. `type` is a
dotted routing key from a skill-owned vocabulary (niwa validates the format
but not the semantics). `reply_to` is optional; when present, it correlates
this message with an earlier `niwa_ask`.

**R28** — `niwa_check_messages` shall return all unread messages from the
caller's inbox, formatted as structured markdown preserving message ID,
sender role, type, timestamp, and body. Messages whose `expires_at` has
passed shall not be returned; they shall be moved to an `expired/`
subdirectory and not surfaced.

### Daemon

**R29** — `niwa mesh watch` shall be a persistent daemon scoped to the
workspace instance. It shall watch all per-role inbox directories under
`.niwa/roles/*/inbox/` via fsnotify. When a new task envelope appears in a
role's inbox and no worker is currently running for that role, the daemon
shall claim the task via the consumption rename (R13) and spawn a worker
via:

```
claude -p "You are a worker for niwa task <task-id>. Call niwa_check_messages to retrieve your task envelope."
```

The bootstrap prompt shall not contain the task body. The worker retrieves
the full envelope by reading its inbox as its first tool call.

**R30** — The daemon shall auto-restart workers that exit without completing.
"Unexpected exit" is defined as: the worker process has exited (detected via
PID liveness check) while the task's `state.json` status is still `running`.
Retries shall be capped at 3 restarts (4 total attempts) with linear backoff
(30s, 60s, 90s). After the cap, the task shall be transitioned to
`abandoned` and a `task.abandoned` message shall be written to the
delegator's inbox.

**R31** — Abandonment shall surface via both: (a) a `task.abandoned` message
in the delegator's inbox, and (b) the task state query (`niwa_query_task`)
returning status `abandoned` with the reason and final restart count. Both
surfaces shall include the reason string and restart count.

**R32** — The daemon shall implement a stalled-progress watchdog. If a task
is in `running` state and no `task.progress` event has been recorded for
longer than a configurable threshold (default 15 minutes), the daemon shall
send SIGTERM to the worker process, treat the resulting exit as an
unexpected exit (R30), and apply the restart policy.

**R33** — On daemon startup, the daemon shall reconcile outstanding tasks.
For each task with state `running`: if the recorded worker PID is alive,
adopt the orphan and continue watching; if the PID is dead, treat the task
as an unexpected exit and apply the restart policy (R30). For each task in
`queued` state, resume normal inbox-watching behavior.

**R34** — Daemon lifecycle: `niwa apply` starts the daemon as a detached
background process if it is not already running (PID file check against a
live PID). `niwa destroy` sends SIGTERM, waits up to 5 seconds, sends
SIGKILL if needed, then removes the instance directory. The daemon is
stateless across restarts: all durable state is on disk in
`.niwa/tasks/` and `.niwa/roles/`.

### Coordinator Registration

**R35** — Coordinator sessions are long-lived interactive sessions opened by
the user. They shall register via `niwa session register` from a
`SessionStart` hook, with a `SessionEntry` written to
`.niwa/sessions/sessions.json` capturing: niwa session UUID, role
(`coordinator`), PID, process start time, Claude session ID (for hook-based
wakeup when resumed), and inbox path. Hook injection shall follow the
existing channel installer pattern.

**R36** — Ephemeral workers spawned via `claude -p` shall not register as
sessions and shall not appear in `sessions.json`. Their presence is tracked
in `.niwa/tasks/<task-id>/state.json` only.

### Observability

**R37** — `niwa session list` shall list coordinator sessions with role,
PID, liveness, last-heartbeat age, and pending message count, following the
existing `niwa status` column conventions.

**R38** — `niwa task list` shall list tasks with columns: task ID, target
role, state, restart count, age, delegator role, body summary (truncated).
It shall accept filters: `--role <role>`, `--state <state>`, `--delegator
<role>`, `--since <duration>`.

**R39** — `niwa task show <task-id>` shall display a task's full envelope,
current state, and `transitions.log` history, including all progress events
and state changes with timestamps.

**R40** — `niwa status` detail view shall include a one-line mesh summary
(e.g., `3 queued, 2 running, 8 completed (last 24h)`) when channels are
configured.

### Non-Functional

**R41** — Mesh infrastructure shall be implemented in Go with no external
runtime dependencies beyond the Go standard library.

**R42** — Mesh communication shall be same-machine only for v1. The message
envelope and tool API shapes are designed so a future network-capable
transport can be introduced without changing the tool surface.

**R43** — The `niwa task`, `niwa session`, and `niwa mesh` subcommand groups
shall follow the existing cobra pattern, with each subcommand in a separate
file under `internal/cli/`.

**R44** — All writes to `sessions.json`, per-role inboxes, and per-task
state files shall use `flock` advisory locking and/or atomic rename from a
tempfile to prevent concurrent-write corruption.

**R45** — All files under `.niwa/` shall be created with mode `0600` (files)
and `0700` (directories), independent of umask.

**R46** — The consumption barrier (R13) shall guarantee at-most-once task
claim. The completion barrier (R21) shall guarantee at-most-once completion
(subsequent `niwa_complete_task` calls on a completed task shall return an
error).

## Acceptance Criteria

### Provisioning

- [ ] Running `niwa apply` on a workspace with `[channels.mesh]` configured
  creates `.niwa/roles/coordinator/inbox/`, `.niwa/roles/coordinator/inbox/in-progress/`,
  and an equivalent pair for every other role derived from the topology.
- [ ] The same apply creates `.niwa/tasks/`, `.niwa/sessions/sessions.json`
  (empty), and `.niwa/daemon.pid` / `.niwa/daemon.log`.
- [ ] `<instanceRoot>/.claude/.mcp.json` and `<repoDir>/.claude/.mcp.json`
  (for each repo) both exist after apply with a `niwa` MCP server entry.
- [ ] `<instanceRoot>/.claude/skills/niwa-mesh/SKILL.md` exists after apply.
- [ ] `<repoDir>/.claude/skills/niwa-mesh/SKILL.md` exists after apply for
  every cloned repo.
- [ ] Running apply a second time does not duplicate queued tasks, overwrite
  in-progress task state, or reset the sessions registry.
- [ ] Running `niwa destroy` removes all mesh infrastructure.
- [ ] A user-modified `~/.claude/skills/niwa-mesh/SKILL.md` takes precedence
  over the niwa-installed copy when a session resolves the skill.

### Delegation

- [ ] Calling `niwa_delegate(to="web", body={...}, mode="async")` from the
  coordinator returns a task ID immediately and creates
  `.niwa/tasks/<task-id>/envelope.json` and
  `.niwa/roles/web/inbox/<task-id>.json`.
- [ ] Within 2 seconds of the envelope appearing, the daemon performs the
  consumption rename and spawns `claude -p` in the web repo's directory
  with a bootstrap prompt that does not contain the task body.
- [ ] The spawned worker's first `niwa_check_messages` call returns the task
  envelope.
- [ ] When the worker calls `niwa_complete_task`, the task transitions to
  `completed`, a `task.completed` message appears in the coordinator's
  inbox, and `niwa_query_task` returns the result.
- [ ] Calling `niwa_delegate(mode="sync")` blocks the tool call until the
  task reaches a terminal state and returns the result.
- [ ] Calling `niwa_await_task(task_id)` from the delegator after a
  prior async delegation blocks until the task reaches a terminal state.
- [ ] Calling `niwa_query_task(task_id)` at any time returns the current
  state without blocking.
- [ ] Progress events emitted by a running worker appear in the delegator's
  inbox without the delegator calling `niwa_query_task`.

### Queue Mutation

- [ ] `niwa_list_outbound_tasks()` from a delegator lists only that
  delegator's tasks and respects the `to` and `status` filters.
- [ ] `niwa_update_task(task_id, body)` replaces the queued envelope body
  and returns `{status: "updated"}` if the task is still `queued`.
- [ ] `niwa_update_task` returns `{status: "too_late", current_state:
  "running"}` when called after the daemon has performed the consumption
  rename.
- [ ] `niwa_cancel_task(task_id)` transitions a queued task to `cancelled`
  and returns `{status: "cancelled"}`.
- [ ] `niwa_cancel_task` returns `{status: "too_late", current_state: <state>}`
  when called after consumption.
- [ ] Neither `niwa_update_task` nor `niwa_cancel_task` succeed when called
  by an agent that is not the task's delegator.

### Lifecycle and Restart

- [ ] A worker that exits with code 0 without calling `niwa_complete_task`
  triggers a restart attempt.
- [ ] A worker that exits with a non-zero code triggers a restart attempt.
- [ ] After 3 restart attempts (4 total spawns) for a single task, the task
  transitions to `abandoned`, a `task.abandoned` message appears in the
  delegator's inbox, and `niwa_query_task` returns status `abandoned`.
- [ ] A task with no progress events for > 15 minutes (default threshold)
  has its worker SIGTERM'd and counts as an unexpected exit.
- [ ] `niwa_fail_task` transitions a task directly to `abandoned` without
  triggering a restart attempt.
- [ ] After a daemon crash and restart, tasks that were `running` with live
  workers continue normally; tasks that were `running` with dead workers
  apply the restart policy.

### Peer Messaging

- [ ] `niwa_ask(to="coordinator", body={...})` from a running worker blocks
  until the coordinator calls `niwa_send_message` with a matching `reply_to`,
  then returns the reply body.
- [ ] `niwa_ask` to a role with no running worker spawns one via `claude -p`
  and blocks until the spawned worker replies.
- [ ] `niwa_send_message` and `niwa_check_messages` work for any agent pair
  regardless of task context.

### Observability

- [ ] `niwa session list` shows registered coordinators only; ephemeral
  workers do not appear.
- [ ] `niwa task list` shows all tasks with role, status, restart count, age,
  and delegator.
- [ ] `niwa task list --state running` filters to running tasks only.
- [ ] `niwa task show <id>` displays the envelope, state, and full
  transitions log.
- [ ] `niwa status` detail view includes a one-line mesh summary when
  channels are configured.

## Out of Scope

- **Ad-hoc roles**: V1 roles map 1:1 to cloned repos plus the hardcoded
  `coordinator`. Arbitrary role names without a corresponding repo (e.g.
  `reviewer`) are not supported.
- **Network / cross-machine transport**: Same-machine only for v1.
- **Cross-workspace routing**: Sessions in different niwa instances on the
  same machine cannot message each other.
- **Message encryption and agent authentication**: Messages are plaintext
  JSON under `0600` permissions on the local filesystem.
- **In-flight task cancellation**: V1 cancellation applies only to tasks
  still in `queued` state. Cancelling a running worker is a future feature.
- **Human-in-the-loop approval before spawning workers**: The coordinator's
  dispatch is trusted; niwa spawns without prompting the user.
- **Live observability during delegation** (dashboard, `niwa mesh tail`):
  The user sees progress through the coordinator's chat only. A separate
  live-observability mechanism is a future feature.
- **Multiple parallel workers per role**: At most one worker per role runs at
  a time. Additional tasks for the same role queue sequentially (git-conflict
  avoidance).
- **Native Claude Code Channels push**: The Channels-based wakeup path is
  not a v1 design constraint. V1 commits to `claude -p` as the spawn
  mechanism. The tool API is independent of the wakeup mechanism, so a
  future migration is possible, but niwa does not forward-declare support.
- **Fan-out / broadcast delivery**: Sending one message to multiple roles
  with one tool call.
- **Intentional duplicate role registration**: V1 enforces uniqueness.
- **`niwa mesh gc`**: TTL cleanup runs incidentally on apply and destroy.
  A standalone GC command is deferred.

## Open Questions

None.

## Known Limitations

- **Sequential per-role execution**: Tasks for the same role queue and run
  one at a time. If two tasks target `web`, the second waits for the first
  to complete. This is a deliberate choice to avoid concurrent git operations
  in the same working tree.
- **Worker context carryover**: Each worker is a fresh `claude -p` process.
  It has no memory of prior workers in the same role or of the coordinator's
  conversation beyond what the task envelope carries. Delegators that need
  cross-task continuity must thread the relevant context through each task's
  body explicitly.
- **Coordinator liveness constraints the user experience**: The coordinator
  must be alive for `niwa_delegate(mode="sync")` and `niwa_await_task` to
  return to the user. If the user closes the coordinator mid-await, progress
  and completion messages accumulate in the inbox and are surfaced on the
  next coordinator session via the registration hook.
- **Daemon does not survive machine restarts**: `niwa mesh watch` is a user
  process. After reboot or logout, all instance daemons are gone. Recovery:
  `niwa apply` on each affected instance restarts the daemon. Tasks that were
  `running` at reboot time are reconciled per R33 when the daemon returns.
- **No in-flight cancellation**: A delegator that wants to stop a task
  already in `running` state must wait for the worker to exit (or hit the
  stalled-progress watchdog). There is no "kill the worker now" tool in v1.
- **`niwa destroy` is required for clean removal**: Deleting an instance
  directory with `rm -rf` leaves the daemon running until fsnotify detects
  the missing watched directory. Always use `niwa destroy`.
- **Completion is a behavioral contract**: The skill instructs workers to
  call `niwa_complete_task` before exiting. A worker that ignores the skill
  and simply finishes its prompt will be treated as an unexpected exit and
  restarted, potentially repeating completed work. The restart cap bounds
  the blast radius.

## Decisions and Trade-offs

**Task as a first-class object over message-as-task convention**

The prior PRD modeled tasks as a convention on top of typed messages
(`task.delegate`, `task.result`, `task.progress`). That approach had two
weaknesses: completion was implicit (a `task.result` message was the
convention, but nothing in the tool contract required one), and there was
no way to query "is task X done?" without scanning inboxes. Making tasks
first-class — with a per-task directory, explicit state, and explicit
completion tool — gives the delegator a handle it can query and block on,
and gives niwa a clear point at which to decide "the worker exited without
completing; restart or abandon." The cost is a new storage structure
(`.niwa/tasks/`) and a larger tool surface. Worth it for the UX
simplification.

**Delivering task envelope via inbox, not `claude -p` argv**

`claude -p` invocation carries a bootstrap prompt only. The full task
envelope lives in the worker's inbox and is retrieved on the worker's first
tool call. Alternatives considered:

- *JSON envelope in argv*: simpler but opens a prompt-injection surface —
  the task body and the control-plane instructions share the same text.
  A malicious or merely careless body could attempt to override the
  bootstrap.
- *Stdin*: blocked by an upstream `claude -p` bug where stdin input above
  ~7KB returns empty output (closed as "not planned"). Not a viable
  mechanism in practice.
- *Environment variables*: requires a launcher tool and is bounded by
  `ARG_MAX` regardless.

The chosen approach also unifies the spawn path with the (future) resume
path: a worker resumed by a SessionStart hook and a worker spawned by the
daemon both retrieve their task through the same tool call.

**Atomic-rename consumption barrier**

The daemon claims a queued task by renaming its envelope from
`inbox/<id>.json` to `inbox/in-progress/<id>.json` as a single filesystem
operation. This is the maildir pattern and extends the existing advisory-
locking approach in the provisioning layer. It gives `niwa_cancel_task`
two clean outcomes — `cancelled` if its own rename succeeds, `too_late`
if the file is no longer there — with no partial-state middle ground.

**Default retry cap: 3 restarts (4 attempts)**

Established job-queue systems use a range of defaults. The chosen value
balances two failure modes: transient issues (Claude API blip, single
context-exhaustion crash) which should recover without user intervention,
and genuinely stuck tasks (bad prompt, unfixable error) which should abandon
quickly rather than loop. Three is enough attempts to clear most transients
while keeping the blast radius bounded. Overridable per workspace and per
task for users with different tolerances.

**Abandonment surfaces on both push and pull**

When a task is abandoned, niwa writes a `task.abandoned` message to the
delegator's inbox (the push surface) and transitions the task state so
`niwa_query_task` returns `abandoned` (the pull surface). The push matches
the scope's "auto-surfaced progress" principle — the delegator learns
without asking. The pull is the durable backstop for a delegator that was
offline when the abandonment happened.

**Sender-only ownership of queue mutation**

Only the task's delegator can list, update, or cancel it. Other agents
cannot mutate tasks they did not dispatch. The trust boundary is role
integrity in `sessions.json` — niwa verifies the caller's role matches
the envelope's `from.role`. No cryptographic signing in v1.

**Niwa's opinionated surface is the tool API and the task state machine**

Everything above that — message type vocabulary, progress cadence, how a
coordinator decides to dispatch sync vs async, whether workers greet peers
on startup — lives in the `niwa-mesh` skill, not in niwa's tool contracts.
Users who want different behavior override the skill at the personal scope
without niwa implementing override detection. This keeps niwa small and
the behavioral layer user-modifiable.

**Workers are ephemeral and unregistered**

A worker is a one-shot `claude -p` process. It does not appear in
`sessions.json`. Its lifetime is bounded by a single task. This sidesteps
the Claude Code MCP-push-to-idle-sessions bug (workers cannot be idle —
they are started with a task and exit when done) and simplifies the
registration model: the only long-lived session is the coordinator.

**Per-role inboxes, not per-session-UUID inboxes**

The prior PRD keyed inboxes by session UUID, which meant a message to
`web` had no destination until some Claude registered as `web`. The revised
PRD keys inboxes by role and creates them at apply time from the workspace
topology. A task can be queued for `web` before any worker has ever
existed; the daemon spawns one to consume it. This is the change that makes
the coordinator's tool surface uniform regardless of worker liveness.

**Channels forward-compatibility is a soft goal, not a hard requirement**

The prior PRD required the v1 design to be forward-compatible with Claude
Code's native Channels protocol. The revised PRD downgrades this to a
design preference: the tool API is intentionally independent of the wakeup
mechanism, so a future Channels-based wakeup is possible without breaking
the tool surface. But niwa does not actively forward-declare Channels
support, and the `claude -p` spawn path is v1's only spawn path.
