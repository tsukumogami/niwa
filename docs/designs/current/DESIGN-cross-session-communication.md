---
status: Implemented
upstream: docs/prds/PRD-cross-session-communication.md
problem: |
  Niwa must provision a workspace-scoped mesh that lets any agent dispatch
  tasks to any known role, track each task's lifecycle independently from
  the Claude process that executes it, and give senders programmatic control
  over the queue of undelivered tasks. The existing implementation routes
  messages through per-session-UUID inboxes created at session-registration
  time, uses `claude --resume` to wake idle sessions, and has no notion of
  task state distinct from process state. None of that survives the new PRD,
  which requires per-role inboxes provisioned at apply time, ephemeral
  `claude -p` workers spawned on demand, and a first-class task state
  machine with explicit completion. The design must replace the existing
  mesh wholesale while preserving the provisioning, activation, and hook-
  injection machinery that already works.
decision: |
  Messages route through per-role inboxes provisioned at apply time from the
  workspace topology. Tasks are first-class per-directory state machines
  under `.niwa/tasks/<task-id>/` with an authoritative `state.json`, an
  append-only NDJSON `transitions.log`, and a dedicated `.lock` flock
  target. A central-loop daemon (`niwa mesh watch`) claims queued envelopes
  via atomic rename and spawns ephemeral `claude -p` workers supervised by
  per-task goroutines; adopted orphans (surviving a daemon crash) are
  polled centrally via `IsPIDAlive`. Per-session stdio MCP servers serve
  eleven tools — task delegation, query, await, update, cancel, progress,
  finish, plus peer messaging — with a uniform `authorizeTaskCall` helper
  that verifies `NIWA_TASK_ID` env + role match + (Linux-mandatory) PPID
  start-time match against state.json. A flat uniform `niwa-mesh` skill is
  installed at both the instance root and each repo's `.claude/skills/`,
  kept idempotent via sha256 ContentHash in `InstanceState.ManagedFiles`. A
  literal-path `NIWA_WORKER_SPAWN_COMMAND` override plus integer-second
  timing env vars and daemon pause hooks make every acceptance criterion
  verifiable by a scripted MCP-client fake; two residual `@channels-e2e`
  scenarios exercise real `claude -p` to prove MCP loadability and
  bootstrap-prompt effectiveness.
rationale: |
  The seven decisions hang together because each roots in the same
  filesystem-as-durable-state principle: state on disk, stateless daemon,
  single-lock-per-task, atomic rename as the commit point. The
  cross-validation between task storage and MCP topology decisions produced
  the design's most consequential refinement — replacing an initially-
  proposed per-task crypto token with the PPID + start-time check that
  `state.json` already enables. The token added no protection beyond what
  start-time freshness already rotates per spawn, with the same same-UID
  trust ceiling; the PPID check reuses existing niwa primitives
  (`IsPIDAlive`, `PIDStartTime`) and introduces no new file schema. The
  test harness design is load-bearing for the whole design's
  implementability: without `NIWA_WORKER_SPAWN_COMMAND` + timing overrides
  + daemon pause hooks, most acceptance criteria would be probabilistic; with
  them, they are deterministic in seconds. Migration is deliberately
  simple: pre-1.0 posture means no user has an envelope-preservation
  contract, so blind-rewrite with a one-line warning is the right amount
  of care.
---

# DESIGN: Cross-Session Communication

## Status

Implemented

## Context and Problem Statement

Niwa's cross-session communication PRD (upstream) defines a model where any
agent can delegate work to any known role in the workspace — including roles
that have no running Claude session — and where tasks are first-class
objects with an explicit lifecycle independent of the processes that execute
them. The technical problem the design must solve has four distinct layers:

**Message transport and routing.** Agents address each other by role, not by
process ID or session UUID. Inboxes must exist at `niwa apply` time, keyed
by role derived from the workspace topology, so an envelope queued for
`web` has a destination whether or not a Claude has ever run there.
Transport must survive daemon crashes and process kills: no message may
live only in memory. Same-machine only for v1, with a layout that would not
need changing if a network transport were later added.

**Task state machine.** A task is not a message. It has states
(`queued → running → completed | abandoned | cancelled`) that transition
through a mixture of caller actions (`niwa_delegate`, `niwa_cancel_task`,
`niwa_finish_task`), daemon actions (consumption rename; restart cap;
stalled-progress watchdog), and system events (unexpected worker exit).
Every transition must be atomic and crash-safe. Completion is an explicit
tool call — `niwa_finish_task(outcome="completed", ...)` — not a process
exit. The design must give the delegator a handle it can query, block on,
mutate (while queued), or cancel (while queued), and must guarantee
at-most-once claim and at-most-once terminal transition under concurrent
callers.

**Worker spawn and daemon lifecycle.** No Claude runs in a repo by default.
When a task is queued for a role with no running worker, a persistent
daemon (`niwa mesh watch`) claims the task via an atomic rename and spawns
a fresh `claude -p` as the worker. Workers are ephemeral: one worker per
task, no registration in `sessions.json`, no persistence beyond the task
directory. The daemon must auto-restart workers that exit without
completing (up to a bounded cap), run a stalled-progress watchdog to kill
runaway workers, and recover cleanly when the daemon itself crashes and
restarts — including adopting orphaned workers whose daemon died. All
without becoming a single point of failure: the inbox and task directories
are the durable state; the daemon is stateless and can restart.

**Developer ergonomics: skill installation and testing.** The tool API is
narrow by design. Message vocabulary, progress cadence, and coordination
patterns live in a `niwa-mesh` skill that niwa installs into every agent's
skill directory, overridable at the personal scope. The test harness must
decouple niwa's correctness from live Claude behavior: a
`NIWA_WORKER_SPAWN_COMMAND` environment variable substitutes a scripted
fake for `claude -p`, and all timing thresholds are overridable via env
vars so functional tests run in seconds, not minutes. A small set of
`@channels-e2e` scenarios still exercises real `claude -p` as an
integration sanity check — but every other AC is verified against niwa's
observable surface.

The existing niwa codebase already has working mechanisms for: pipeline
integration (`Applier.runPipeline` with an insertion point at step 4.75),
hook injection through `HooksMaterializer`, hybrid activation
(`--channels`/`--no-channels` flags, `NIWA_CHANNELS` env var, personal
overlay), managed-file tracking via `InstanceState.ManagedFiles`, advisory
locking via `flock` on state files, and Claude session ID discovery (env
var → `~/.claude/sessions/<ppid>.json` → project-dir scan) needed for the
coordinator's resume path. These survive the new design and are reused.
The existing message routing, session-UUID-keyed inbox layout, four-tool
MCP surface, and `claude --resume` wakeup path do not survive and are
replaced.

The implementation touches: `internal/workspace/apply.go` (channel
installer rewrite), `internal/workspace/channels.go` (per-role inbox and
skill installation logic), `internal/cli/mesh_watch.go` (daemon rewrite),
`internal/cli/session_register.go` (coordinator-only registration path),
`internal/mcp/server.go` (new tool surface: `niwa_delegate`,
`niwa_query_task`, `niwa_await_task`, `niwa_report_progress`,
`niwa_finish_task`, `niwa_list_outbound_tasks`, `niwa_update_task`,
`niwa_cancel_task`, plus revised `niwa_ask`, `niwa_send_message`,
`niwa_check_messages`), new task-state storage under `.niwa/tasks/`, new
`niwa task` CLI subcommand group, and a new default `niwa-mesh` skill
installer.

## Decision Drivers

Derived from PRD constraints and the existing codebase:

- **Addressable-by-role at apply time.** Per-role inbox directories must
  exist before any worker has ever run. Roles derive from workspace
  topology (`coordinator` at the instance root plus one role per repo) at
  `niwa apply` time, with optional explicit overrides via
  `[channels.mesh.roles]`.
- **Task state survives daemon crashes.** Task state lives on disk in
  per-task directories under `.niwa/tasks/<task-id>/`. Every state
  transition uses `flock` plus atomic rename from a tempfile. No in-memory
  state is authoritative.
- **At-most-once claim via a single rename.** The consumption barrier is
  an atomic rename from `inbox/<id>.json` to `inbox/in-progress/<id>.json`.
  `niwa_cancel_task` uses the same pattern (rename to `inbox/cancelled/`).
  Two clean outcomes per operation: success or `too_late`, never partial.
- **Completion is an explicit tool call, not process exit.** Workers must
  call `niwa_finish_task` to transition a task out of `running`. Process
  exit alone (any exit code) counts as unexpected and triggers restart.
- **Deterministic testability.** Every AC in the PRD must be verifiable
  without involving a live Claude subprocess. Niwa provides the
  `NIWA_WORKER_SPAWN_COMMAND` override and env-var-configurable timing
  thresholds. Integration tests (`@channels-e2e`) are a separate, small
  set that exercises real `claude -p` for sanity coverage, not for
  correctness.
- **Pure Go, stdlib only (+ fsnotify).** No new runtime dependencies
  beyond what is already in `go.mod`.
- **Zero-friction provisioning.** A bare `[channels.mesh]` section,
  `--channels` flag, `NIWA_CHANNELS=1`, or channels in the personal
  overlay each activate the mesh. `--no-channels` suppresses. Priority
  order is flag > env > config.
- **Coordinator is long-lived; workers are ephemeral.** The coordinator
  registers via a `SessionStart` hook and appears in `sessions.json`;
  workers do not. This asymmetry drives two different spawn/registration
  paths that nonetheless share the same MCP tool surface.
- **Forward-compatible tool API.** The tool contracts (arguments, return
  shapes, error codes) do not encode any assumption about how workers are
  started or woken. If Claude Code's native push wakeup ever becomes
  viable, only the spawn path changes.
- **Narrow niwa surface; behavior in skill.** Niwa owns the tool API and
  the task state machine; message vocabulary and coordination patterns
  live in `niwa-mesh` skill content, installed at `<instanceRoot>/.claude/
  skills/niwa-mesh/` and mirrored into each repo.
- **Idempotent apply with drift warning.** Installed skill content is
  overwritten on every apply; drift to user-edited project-scope copies
  triggers a warning but is still overwritten. Users preserve
  customizations via the personal-scope skill at `~/.claude/skills/`,
  which Claude Code's resolution orders ahead of the project-scope copy.
- **Clean integration with existing Applier pipeline.** The channel
  installer runs at step 4.75 (between `InstallWorkspaceContext` and the
  group CLAUDE.md step), as a plain function invoked imperatively, not as
  a `Materializer` interface implementation. Tracked files flow through
  `InstanceState.ManagedFiles` for drift detection and `niwa destroy`
  cleanup.
- **Structured error shapes.** Every MCP tool error returns a structured
  object with `status`, `error_code`, `detail`, and optional
  `current_state` fields, drawing from a closed enumeration
  (`NOT_TASK_OWNER`, `NOT_TASK_PARTY`, `TASK_ALREADY_TERMINAL`,
  `BAD_PAYLOAD`, `BAD_TYPE`, `UNKNOWN_ROLE`).
- **`0600/0700` file mode under `.niwa/`.** All files under the niwa
  instance directory are created with restrictive modes regardless of
  umask, per PRD R48.
- **Single-worker-per-role invariant.** At most one worker for a given
  role may be running at any moment, enforced by the atomic consumption
  rename. The daemon's spawn decision reads the inbox listing; concurrent
  spawn attempts observe an empty inbox and skip.

## Considered Options

### Decision 1: Task Storage and State-Transition Atomicity

The PRD materializes tasks as per-task directories under `.niwa/tasks/<task-id>/`, each holding `envelope.json`, `state.json`, and `transitions.log` (R14, R47, R49). Multiple processes write concurrently: the daemon (spawn, reconcile, restart), the worker (progress, completion), and the delegator (query, update, cancel). Workers are ephemeral `claude -p` processes that do not register in `sessions.json` (R40), so worker authorization cannot piggyback on the coordinator's session registry.

This decision commits to: the `state.json` field set, the `flock` scope, the exact write-order for state transitions, the `transitions.log` format, and the authorization mechanism for worker-initiated transitions.

Key assumptions: Linux-only for v1's strict PID/start-time semantics (macOS degrades gracefully — `PIDStartTime` returns a conservative "alive" answer); `claude -p` propagates env to the `niwa mcp-serve` child (already relied on for `NIWA_INSTANCE_ROOT`); `syscall.Flock` is sufficient advisory locking per R47.

#### Chosen: Per-task `.lock` file, authoritative `state.json`, append-only NDJSON `transitions.log`

A task's on-disk layout is:

```
.niwa/tasks/<task-id>/
├── .lock              # zero-byte coordination file; flock target
├── envelope.json      # immutable except for body via niwa_update_task
├── state.json         # authoritative state + audit-friendly fields
└── transitions.log    # NDJSON append-only audit trail
```

`state.json` is the single source of truth for authorization and query operations; `transitions.log` is a redundant audit trail. Both mutate together under an exclusive flock on `.lock` (a dedicated file, not on `state.json` itself — holding a lock on a file you are about to atomically rename produces a stale fd after the rename).

**`state.json` schema (v=1):**

```json
{
  "v": 1,
  "task_id": "<uuid>",
  "state": "queued | running | completed | abandoned | cancelled",
  "state_transitions": [{"from": null, "to": "queued", "at": "RFC3339"}],
  "restart_count": 0,
  "max_restarts": 3,
  "last_progress": {"summary": "…", "body": {}, "at": "RFC3339"},
  "worker": {
    "pid": 12345,
    "start_time": 8765432,
    "role": "web",
    "spawn_started_at": "RFC3339",
    "adopted_at": "RFC3339 | null"
  },
  "delegator_role": "coordinator",
  "target_role": "web",
  "result": {},              // present only when state == completed
  "reason": {},              // present only when state == abandoned
  "cancellation_reason": {}, // present only when state == cancelled
  "updated_at": "RFC3339"
}
```

`delegator_role` and `target_role` are duplicated from `envelope.json` so authorization never needs to reopen the envelope (which `niwa_update_task` can mutate — only `body`, but concentrating auth data in `state.json` keeps invariants local). `state_transitions` is duplicated in both files so `niwa_query_task` is a single-file read.

**Write-order for every state transition:**

```
flock(.lock, LOCK_EX)
  read state.json
  validate: current.state == expected_from_state
  mutate (append state_transition, bump restart_count, set worker fields, etc.)
  write state.json.tmp ; fsync
  rename state.json.tmp → state.json
  fsync parent directory
  append line to transitions.log (O_APPEND | O_WRONLY | O_CREATE, 0600) ; fsync
unlock
```

`state.json` is authoritative; if the `transitions.log` append fails, readers still see a consistent state. The rename is the externally-visible commit point. Parent-directory fsync ensures the rename survives a crash before the log append. A single critical section per transition guarantees that `state.json.state_transitions` and `transitions.log` never diverge.

**`transitions.log` format:** NDJSON, one event per line, with a `kind` discriminator:

```json
{"v":1,"kind":"state_transition","at":"…","from":"queued","to":"running","actor":{"kind":"daemon","pid":1234}}
{"v":1,"kind":"progress","at":"…","summary":"…","body":{…},"actor":{"kind":"worker","pid":5678}}
{"v":1,"kind":"spawn","at":"…","worker_pid":5678,"attempt":1,"actor":{…}}
{"v":1,"kind":"unexpected_exit","at":"…","worker_pid":5678,"exit_code":0,"actor":{…}}
{"v":1,"kind":"adoption","at":"…","worker_pid":5678,"actor":{…}}
{"v":1,"kind":"watchdog_signal","at":"…","signal":"SIGTERM","actor":{…}}
```

All writers append under the per-task flock; no log writer opens the file without the lock.

**Worker authorization is environment-anchored, state.json-verified** (see Decision 3 for the authorization code path). No separate crypto token: `state.json.worker.start_time` rotates on every spawn, and the PPID + start-time check on Linux provides cross-task and cross-spawn isolation equivalent to what a per-task token would give.

#### Alternatives Considered

**Global `.niwa/tasks.lock` (workspace-wide serialization).** Rejected: serializes unrelated tasks; contradicts R47's 1000-iteration concurrent-writer stress; per-task latency would scale with workspace-wide activity.

**Per-file locks (`state.json.lock` + `transitions.log.lock`).** Rejected: admits interleavings where `state.json.state_transitions` and `transitions.log` diverge — a log entry whose `from` state never appeared in `state.json`.

**flock on `state.json` itself.** Rejected: atomic rename invalidates the locked fd. Requires a non-atomic lock-drop-rename-reacquire window.

**Event-sourced state (`transitions.log` is truth, `state.json` is a cache).** Rejected: `niwa_query_task` (R20) would need log replay on every call. Deferrable to v2 if the snapshot ever becomes a bottleneck.

**Binary transitions log (CBOR, length-prefixed JSON).** Rejected: PRD R14 specifies NDJSON; no performance motivation to deviate.

**Per-task crypto token in `state.json.worker.token`, exported as `NIWA_TASK_TOKEN`.** Rejected during cross-validation against Decision 3: the token provides no protection beyond what `state.json.worker_start_time` already rotates per spawn under 0600 mode. Both mechanisms have the same same-UID trust ceiling (the PRD's stated trust boundary). Retained as a migration path if the PPID + start-time check ever proves fragile; the tool API and authorization semantics would be identical.

### Decision 2: Daemon Architecture

The daemon spawns workers in response to queued envelopes, supervises them via exit events, runs the stalled-progress watchdog, applies the restart cap with backoff, adopts orphaned workers whose previous daemon crashed, and reconciles task state on startup. All without becoming a single point of failure: durable state lives on disk.

This decision commits to: the daemon's goroutine structure, the worker-exit detection mechanism, the orphan-adoption path, the watchdog / restart-cap interaction, and crash-recovery semantics.

Key assumptions: `state.json` is authoritative (Decision 1); PRD R36's "treated as an unexpected exit (R34)" means watchdog-triggered kills consume a retry slot; a 2s poll of `state.json.last_progress_at` is compatible with the 15-minute default watchdog.

#### Chosen: Central event loop + per-task supervisor goroutine

A single central goroutine owns: fsnotify watches on `.niwa/roles/*/inbox/`; a 2s ticker for adopted-orphan polling (via `IsPIDAlive`); a central task-event channel; all `state.json` writes for spawn decisions.

Each daemon-spawned worker gets a dedicated supervisor goroutine that:

- Calls `cmd.Wait()` — the idiomatic Go exit-detection path; SIGCHLD fights `os/exec`.
- Runs the stall-progress watchdog locally (timer-based; reset on detecting progress via 2s poll of `state.json.last_progress_at`).
- On stall: SIGTERM, wait `NIWA_SIGTERM_GRACE_SECONDS`, SIGKILL if still alive.
- Reports the exit event to the central loop, which writes the state transition.

**Adopted orphans** (workers whose previous daemon died, discovered at startup reconciliation via `state.json.worker_pid` being alive but not a child of the new daemon) have no `*exec.Cmd` handle. They are tracked in a central list; the central loop polls each orphan's `IsPIDAlive` every 2s, and when a PID transitions to dead (or start_time diverges), treats it as an unexpected exit via the same code path as a supervised worker.

**Restart semantics.** `restart_count` increments before the next `cmd.Start` (not after exit), with a `spawn_started_at` timestamp that distinguishes "retry slot allocated" from "retry slot allocated AND process started." Crash recovery between state.json update and `cmd.Start` detects the discrepancy (PID field unset or dead; `spawn_started_at` present) and allocates a fresh retry.

**Watchdog-triggered kills consume a retry slot.** PRD R36 is unambiguous: "treated as an unexpected exit (R34) and shall apply the restart policy." A runaway worker hits the retry cap like any other exit without completion.

**Catch-up inbox scan on startup.** After fsnotify registration, the daemon performs a one-shot listing of each role's `inbox/` to handle envelopes that landed before the watch was active.

#### Alternatives Considered

**Single event loop with embedded per-task timers.** All state, all timers, all fsnotify events through one `select`. Rejected: degenerates to the chosen option once `cmd.Wait()` must run per-worker (unavoidable in Go); the central `select` and timer map grow faster than the supervisor model.

**SIGCHLD + `syscall.Wait4(-1, ...)`.** Use POSIX SIGCHLD to detect exits centrally. Rejected: fights `os/exec.Cmd`'s contract; produces `ECHILD` races and broken `Cmd` state; requires a bespoke fork/exec wrapper; non-portable across Unix variants.

**Multi-goroutine per concern with concurrent `state.json` writers.** Separate goroutines for fsnotify, watchdog, exit reaping, each writing `state.json`. Rejected: multiple writers contending on per-task flock within the daemon itself; logic races between watchdog decision and exit detection.

**Per-task fsnotify watch on `transitions.log` for progress ticks.** Rejected: proliferates watches per running task with no benefit over the 2s poll.

**Unix-socket IPC between daemon and MCP server.** Rejected: see Decision 3 — filesystem-only coordination is preferred; an IPC layer adds a single-point-of-failure the file-based design specifically avoids.

### Decision 3: MCP Topology and Caller Authentication

The niwa MCP server must serve eleven tools under a model where workers are ephemeral `claude -p` processes not registered in `sessions.json` (R40) but nonetheless authorized for task-specific mutations. Coordinators are registered (R39) and authorized as delegators. The design must pick a server topology, a coordination mechanism with the stateless daemon, and an identity channel tight enough to defeat prompt-injection-driven control-plane attacks while accepting the PRD's stated ceiling ("role integrity is the only trust boundary" against same-UID malicious processes).

Key assumptions: Claude Code inherits env from the parent `claude -p` process into the MCP subprocess (already relied on for `NIWA_INSTANCE_ROOT`, `NIWA_SESSION_ROLE`, `NIWA_SESSION_ID`); fsnotify's ~10ms latency is well under R22's 5-second bound; one worker per task per lifetime (R8).

#### Chosen: Per-session stdio MCP server + filesystem coordination + env-var task identity + state.json authorization

The MCP server remains a stdio subprocess spawned per Claude session by Claude Code from `.claude/.mcp.json`. Same for coordinators, peer sessions, and ephemeral workers. No per-instance daemon-hosted MCP server; no Unix-socket relay.

Coordination between MCP server instances and the daemon is filesystem-only. The daemon writes per-task state (Decision 1); MCP tool calls read it under shared flock. The existing fsnotify `notifyNewFile` pathway is extended to route `task.completed`, `task.abandoned`, `task.cancelled` messages to a new `awaitWaiters map[string]chan taskEvent` (keyed by `task_id`) for `niwa_await_task` and synchronous `niwa_delegate`.

**Worker identity via env at spawn time:**

```
NIWA_INSTANCE_ROOT=<root>
NIWA_SESSION_ROLE=<target-role>   # the role whose inbox this worker claims
NIWA_TASK_ID=<task-id>            # identifies this spawn's task
```

The MCP server reads these once on startup into `Server.{instanceRoot, role, taskID}`. Coordinators have empty `taskID`; peer sessions have their registered role from the `SessionStart` hook (existing pattern).

**Authorization helper `authorizeTaskCall(taskID, kind)`** reads `envelope.json` + `state.json` under shared flock:

- **`kindDelegator`** (for `niwa_await_task`, `niwa_update_task`, `niwa_cancel_task`): require `envelope.from.role == s.role`. Else `NOT_TASK_OWNER`.
- **`kindExecutor`** (for `niwa_finish_task`, `niwa_report_progress`): require `s.taskID == task_id` AND `state.json.worker.role == s.role`. Plus, as a **mandatory-on-Linux hardening step**, the MCP server calls `PPIDChain(1)` to get the PID exactly one level up (its direct parent — the `claude -p` worker process by topological construction), verifies its start time via `PIDStartTime` (`internal/mcp/liveness.go`), and requires a match with `state.json.worker.pid` and `state.json.worker.start_time`. `PPIDChain(1)` is the committed depth — a future proxy layer between `claude -p` and `mcp-serve` would land on the wrong PID and the check would fail closed (not silently pass); the token-file fallback (see Alternatives) is the migration path if that ever occurs. On macOS where `PIDStartTime` is conservative, the check degrades to PID match only — strictly weaker than Linux and called out as a PRD Known Limitation. If state is terminal: `TASK_ALREADY_TERMINAL`.
- **`kindParty`** (for `niwa_query_task`): accept if either delegator or executor check passes; else `NOT_TASK_PARTY`.
- **Non-task-specific tools** (`niwa_delegate`, `niwa_ask`, `niwa_send_message`, `niwa_check_messages`, `niwa_list_outbound_tasks`): no per-task authorization. Caller's role from `s.role` is written as `envelope.from.role` on new envelopes. When `s.taskID` is non-empty (delegation from a running worker), `parent_task_id` auto-populates to `s.taskID` per R15.

**Cross-session waiter signaling.** The extended `notifyNewFile` inspects message type; on `task.completed|abandoned|cancelled`, it routes to `awaitWaiters[body.task_id]`. `niwa_await_task` registers with `defer cancel()` before checking `state.json` for an already-terminal state (race guard mirroring `handleAsk`).

**Error codes are PRD R50's six.** D1's initial proposal of `NOT_TASK_EXECUTOR` is retired by this resolution: executor-identity mismatch returns `NOT_TASK_PARTY` with the caller informed that they are not this task's party. No PRD changes needed.

#### Alternatives Considered

**Single per-instance daemon-hosted MCP server with Unix-socket relays.** Every `niwa mcp-serve` invocation becomes a JSON-RPC relay proxying to a server embedded in `niwa mesh watch`. Rejected: fsnotify's ~10ms latency beats R22's 5s bound by 500x; adds hundreds of LOC for IPC layer and reconnect logic; one crash takes down all tool calls; violates the stateless-daemon principle of Decisions 1–2; crash recovery still needs filesystem state.

**Per-session stdio + token-file identity (`NIWA_TASK_TOKEN` + `.niwa/tasks/<T>/worker.token`).** Rejected: `worker_start_time` + `NIWA_TASK_ID` provide equivalent defense once the PPID-hardening step is mandatory on Linux — both rotate per spawn; both are 0600-readable to the owning user; same trust ceiling. Retained as a contingency migration path if the PPID check proves fragile; API and auth semantics are identical.

### Decision 4: Worker Spawn Contract

The daemon spawns `claude -p` for each queued task. The contract must pass the task ID to the worker (for inbox retrieval by the LLM and for auth by the MCP subprocess), fix argv so tests can assert on it, and accommodate the `NIWA_WORKER_SPAWN_COMMAND` override (R51) for scripted-fake testing.

Key assumptions: MCP server runs per-worker as a stdio subprocess launched by Claude Code from `.claude/.mcp.json` (Decision 3 confirmed); `claude -p` tolerates R33's flags alongside `-p <prompt>`; Go's `exec.Cmd.Env` last-wins semantics for duplicate keys.

#### Chosen: Literal-path override + dual task-ID propagation (argv + env) + pass-through env with niwa-owned last-wins overrides

`NIWA_WORKER_SPAWN_COMMAND`, when set, is a literal path to a binary that substitutes for `claude` in the spawn. The daemon composes argv, env, and CWD identically whether the binary is `claude` or an override. The scripted fake therefore exercises the real spawn path.

**Argv (fixed with one template slot):**

```
claude -p "You are a worker for niwa task <task-id>. Call niwa_check_messages to retrieve your task envelope."
        --permission-mode=acceptEdits
        --mcp-config=<instanceRoot>/.claude/.mcp.json
        --strict-mcp-config
```

The bootstrap prompt is niwa-owned and fixed in the binary (R32). The only substitution is `<task-id>`. No task body leaks into argv.

**Dual task-ID propagation.** Both argv and env carry the task ID:

- Argv: so the LLM can refer to the ID in its reasoning (retrieving the envelope, passing the ID to `niwa_finish_task`).
- Env: `NIWA_TASK_ID=<task-id>`, so the MCP subprocess (which sees only env, not the LLM's prompt context) can perform the authorization check (Decision 3).

Both are load-bearing; dropping either breaks a different consumer.

**Env strategy.** Pass through the daemon's env, then apply niwa-owned last-wins overwrites:

```
<daemon's env, including PATH, HOME, USER, locale, ANTHROPIC_API_KEY, etc.>
NIWA_INSTANCE_ROOT=<baked at apply time>
NIWA_SESSION_ROLE=<target role>
NIWA_TASK_ID=<task id>
```

Pass-through covers system env. Overwrites are idempotent because niwa-owned keys appear once in the daemon's env (niwa controls its own lifecycle). This matches niwa's existing pattern for `NIWA_INSTANCE_ROOT` etc. in other code paths.

**CWD.** Target role's repository directory, or the instance root for the coordinator role (`niwa_ask`-driven ask-spawns targeting `coordinator`).

#### Alternatives Considered

**Template `NIWA_WORKER_SPAWN_COMMAND` with `{task_id}` / `{instance_root}` substitution.** Rejected: requires a template-DSL parser in production code; strips or duplicates R33's fixed flags; loses the `NIWA_TASK_ID` env channel, forcing the MCP server to parse `/proc/self/cmdline` or env-ad-hoc substitution.

**Argv + JSON envelope on stdin + minimal env whitelist.** Rejected: PRD already rejected stdin envelope delivery (Claude `-p` stdin bug at >7KB); a whitelist adds maintenance burden without safety gain over pass-through with overwrites.

**CLI flag `--worker-spawn-command` on `niwa mesh watch`.** Rejected: contradicts R51's env-var form; functional tests invoke the daemon indirectly via `niwa apply`; shell-profile users expect persistent env vars.

### Decision 5: Skill Installation and Content

`niwa apply` installs a `niwa-mesh` skill at `<instanceRoot>/.claude/skills/niwa-mesh/` and mirrors it into each `<repoDir>/.claude/skills/niwa-mesh/` (R9). The skill content describes default behavior for delegation, progress reporting, completion, message vocabulary, peer interaction, and common patterns (R10). User overrides go at the personal scope (`~/.claude/skills/niwa-mesh/`), which Claude Code's resolution orders ahead of project scope (R11).

Key assumptions: Claude Code's skill frontmatter field set is `{name, description, allowed-tools}`; workers read their task envelope, not `workspace-context.md`, so hardcoding `coordinator` as the role in the instance-root `## Channels` section covers the only reader.

#### Chosen: Flat uniform `SKILL.md` with hash-based idempotency

One `SKILL.md` per installation path. No per-role variants; no `references/` subdirectory.

**Frontmatter:**

```yaml
---
name: niwa-mesh
description: >-
  Delegate tasks across niwa workspace roles. Use when the user asks to
  dispatch work to another agent, check task status, receive progress,
  report completion, or exchange peer messages. …[rest front-loaded within
  Claude Code's 1,536-character combined cap with when-to-use guidance]
allowed-tools:
  - niwa_delegate
  - niwa_query_task
  - niwa_await_task
  - niwa_report_progress
  - niwa_finish_task
  - niwa_list_outbound_tasks
  - niwa_update_task
  - niwa_cancel_task
  - niwa_ask
  - niwa_send_message
  - niwa_check_messages
---
```

**Body (six required sections per R10):**

- **Delegation (sync vs async).** When to use each mode; how `niwa_delegate(mode="sync")` blocks vs. `niwa_delegate(mode="async")` returning `task_id` for later `niwa_await_task` / `niwa_query_task`.
- **Reporting Progress.** Recommended cadence: every 3–5 minutes OR every ~20 tool calls, whichever is sooner. Use `niwa_report_progress` with a ≤200-char `summary` and an optional structured `body`.
- **Completion Contract.** Must call `niwa_finish_task(outcome="completed"|"abandoned", ...)` before exiting. Process exit alone triggers restart.
- **Message Vocabulary.** Default types: `task.progress`, `task.completed`, `task.abandoned`, `question.ask`, `question.answer`, `status.update`. Format constraints (`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)*$`).
- **Peer Interaction.** `niwa_ask` for blocking request/reply; `niwa_send_message` for one-way peer messages. Workers can ask the coordinator; the coordinator can ask workers (spawning a task).
- **Common Patterns.** Coordinator-delegates-and-collects (`niwa_delegate` × N, then `niwa_await_task` × N); worker-asks-for-clarification (`niwa_ask(to="coordinator", …)` blocking inside the worker's tool sequence).

**Idempotency via ContentHash.** Niwa computes sha256 of the installer's output and compares against `InstanceState.ManagedFiles.ContentHash` (existing field). If the on-disk file matches, skip the write (mtime stable; no spurious git diffs). If the hash differs, overwrite and emit a drift warning to stderr identifying the path. Every written file — instance-root and per-repo — is tracked for `niwa destroy` cleanup.

**`workspace-context.md` Channels section** shrinks to four items: role (hardcoded `coordinator`), `NIWA_INSTANCE_ROOT`, the MCP tool names (one per line), and one pointer line: "See the `/niwa-mesh` skill for usage patterns." All behavior lives in the skill.

#### Alternatives Considered

**Per-role SKILL.md variants (coordinator vs worker).** Rejected: R10's six sections are content-symmetric across roles; doubles template maintenance for no measured trigger-precision gain; non-breaking to split later if data justifies.

**Layered skill with `references/` subdirectory.** Rejected: 3–6 KB body fits comfortably in a flat SKILL.md; `references/` multiplies ManagedFiles entries for no on-demand-loading benefit.

**Unconditional overwrite (no idempotency check).** Rejected: mtime churn and git diffs on every apply; sha256 hash check is trivially cheap.

**Per-repo workspace-context.md variants for per-role `Role:` values.** Rejected: workers don't read workspace-context.md; per-repo copies solve a nonexistent problem.

### Decision 6: Test Harness Contract and Integration Test Scope

PRD R51 specifies `NIWA_WORKER_SPAWN_COMMAND` as a binary override for `claude -p` and four env-var timing overrides. The harness must enable every AC to be verified deterministically without involving a live Claude. A small residual `@channels-e2e` set covers what the harness cannot.

Key assumptions: Decision 3's env-based worker identity inherits to the scripted fake exactly as to `claude -p`; coordinator-surface coverage comes from direct MCP-JSON-RPC `@critical` tests; integer-second precision is sufficient for every AC timing.

#### Chosen: Literal-path override + MCP-client scripted fake + daemon pause hooks + integer-second overrides + two residual `@channels-e2e` scenarios

**`NIWA_WORKER_SPAWN_COMMAND` is a literal path.** The daemon's spawn path substitutes binary path only; argv, env, CWD, process group are identical to real `claude -p`. The override contract is the same for production users and for tests.

**Scripted fake is an MCP client.** A Go program built alongside the test suite, invoked by the daemon with the same argv and env it would pass to real `claude -p`. The fake:

1. Reads `NIWA_INSTANCE_ROOT` and `NIWA_TASK_ID` from env.
2. Connects to the niwa MCP server via stdio (same transport as the real worker's LLM would via Claude Code's routing).
3. Calls `niwa_check_messages` to retrieve the task envelope.
4. Exercises the scripted scenario (e.g., emit progress, succeed, fail, or stall) via MCP tools.
5. Calls `niwa_finish_task` or `niwa_fail_task` to transition.

This exercises the full authorization path, not a filesystem bypass.

**Daemon pause hooks for race-window AC.** Niwa-owned env vars (`NIWA_TEST_PAUSE_BEFORE_CLAIM=1`, `NIWA_TEST_PAUSE_AFTER_CLAIM=1`) let tests interrupt the daemon at the consumption-rename boundary, making race-window AC (Q10/Q11, L9/L10) deterministic. Set by the test; honored by the daemon; invisible to production (env var absent = no-op). A test that asserts cancel-wins-race sets `NIWA_TEST_PAUSE_BEFORE_CLAIM`, drives the daemon to the paused state, calls `niwa_cancel_task`, then releases; asserts cancel returns `{status: "cancelled"}`. Symmetric structure for update races.

**Timing overrides are integer-seconds.** Comma-separated for backoff (`NIWA_RETRY_BACKOFF_SECONDS=1,2,3` sets 1s between first retry, 2s before second, 3s before third). Shell-portable; Gherkin-friendly; Go-parseable with `strings.Split + strconv.Atoi`.

**Two residual `@channels-e2e` scenarios** (each runs real `claude -p`; skipped when `claude` not on PATH or `ANTHROPIC_API_KEY` unset; excluded from `@critical`):

- **MCP-config loadability.** Real `claude -p` opens a session; first MCP tool call succeeds. Proves `.mcp.json` resolves and the niwa MCP server launches under Claude Code's actual tool-resolution path.
- **Bootstrap-prompt effectiveness.** The real daemon spawns real `claude -p` with niwa's bootstrap prompt; the worker calls `niwa_check_messages` and retrieves its envelope. Proves the bootstrap prompt gets the LLM to do the right first action.

Every other AC runs through the harness against the scripted fake, deterministically, in seconds.

#### Alternatives Considered

**Command-template `NIWA_WORKER_SPAWN_COMMAND` with `{task_id}` / `{instance_root}` substitution.** Rejected: adds templating DSL to production; duplicates info already in argv/env; creates a masking surface where template bugs hide spawn-path bugs.

**Embedded Go test fake registered at runtime.** Rejected: requires test-only hooks in production code; bypasses the real spawn path (no second OS process exercising SIGCHLD, `IsPIDAlive`, orphan adoption); defeats the purpose.

**Fake writes directly to filesystem.** Rejected: bypasses the MCP tool handler and its authorization path (the dominant failure class); leaves the real tool surface untested in scripted scenarios.

**Hybrid fake (MCP + direct-FS as equal peers).** Rejected: two idioms double maintenance; daemon pause hooks plus Go-test process control already handle the narrow race-AC set where pure-MCP is insufficient.

**JSON-encoded per-attempt backoff override.** Rejected: adds a JSON parser to production for a test-only feature; comma-separated integers are dependency-free and equivalent.

**Millisecond-precision timing overrides (`_MS` suffixes).** Rejected: every AC tolerance is whole seconds; `_MS` variants are additive later.

**Zero live-Claude coverage.** Rejected: would miss MCP-config-loadability and bootstrap-prompt-effectiveness regressions that niwa owns and the harness cannot exercise by construction.

**Three-plus live-Claude scenarios (as in the obsolete prior proposal).** Rejected: the third scenario (a multi-message content assertion from the prior proposal) duplicates MCP-loadability coverage and adds LLM-content flake without covering a distinct niwa surface.

### Decision 7: Provisioning Pipeline and Migration

The existing mesh lives under `.niwa/sessions/<uuid>/inbox/`; the new mesh lives under `.niwa/roles/<role>/inbox/` + `.niwa/tasks/`. The pipeline integration point (step 4.75 of `Applier.runPipeline`) is preserved, but the content the installer writes is rewritten. Instances provisioned under the old layout need a graceful upgrade path — though no user has an envelope-preservation contract at pre-1.0.

Key assumptions: pre-1.0 posture means discarding queued-but-unconsumed old-schema envelopes on upgrade is acceptable (with a one-shot stderr warning); runtime artifacts (envelopes, per-task state, `transitions.log`) are NOT in `ManagedFiles` — only installer-written files are (R2's "every written path" is read as "every path the installer writes"); role-collision detection (AC-R2) is enforced inside the installer before any directory creation.

#### Chosen: Hybrid blind-rewrite with opportunistic cleanup

A ~40-60 LOC migration helper runs at the top of the rewritten `InstallChannelInfrastructure`:

1. **Detect old layout.** If `.niwa/sessions/` contains any `<uuid>/` subdirectory (old layout signature) and `.niwa/roles/` is absent (new layout signature): treat as pre-1.0 upgrade.
2. **One-shot warning.** Log to stderr: `"niwa: upgrading mesh layout. Discarding N queued envelopes from the previous mesh version; any in-flight conversations are abandoned. Run 'niwa destroy && niwa create --channels' for a fresh start. See docs/guides/cross-session-communication.md for details."`
3. **Cleanup.** Recursively remove `.niwa/sessions/<uuid>/` directories. Leave `.niwa/sessions/sessions.json` in place (coordinator registry survives; registered coordinators re-register on next SessionStart hook).
4. **Provision the new layout.** Create `.niwa/roles/<role>/inbox/{in-progress,cancelled,expired,read}/` for every enumerated role; create `.niwa/tasks/`; write `.claude/.mcp.json` at instance and per-repo; install the `niwa-mesh` skill (Decision 5); write the minimal `## Channels` section; inject `SessionStart` + `UserPromptSubmit` hooks via the existing `HooksMaterializer` pipeline.
5. **ManagedFiles discipline.** Every installer-written file goes into `InstanceState.ManagedFiles` so `niwa destroy` cleans up uniformly. Runtime artifacts (`.niwa/tasks/<id>/*`, `.niwa/roles/*/inbox/<id>.json`) are NOT tracked — they are created by the daemon and the MCP tool handlers.

**Idempotency (AC-P10).** A second `niwa apply` on the new layout: installer computes ContentHash on skill files → matches → skip write; checks `.mcp.json` presence and content → matches → skip; role inboxes present → skip; `sessions.json` preserved; in-progress envelopes byte-identical; per-task state files untouched.

**`niwa destroy`** is mechanism-unchanged: terminate the daemon (SIGTERM, then SIGKILL after `NIWA_DESTROY_GRACE_SECONDS`), then remove the instance directory.

#### Alternatives Considered

**Detect-and-migrate.** Preserve queued envelopes by converting old-schema messages to the new per-task-directory shape. Rejected: old envelopes may be semantically unprocessable by the new daemon (no `task_id` concept in the old schema); ~100-200 LOC + test matrix become dead code after the upgrade window; low payoff against pre-1.0 user base.

**Destroy-and-recreate (refuse-with-instructions).** Apply fails with "run `niwa destroy` first." Rejected: strict reading of "no broken instance after upgrading" — the instance is non-functional until the user runs destroy+apply manually. Saves ~20-40 LOC vs. the chosen option but worsens UX.

**Blind-rewrite + garbage-collect via ManagedFiles.** Rely on `cleanRemovedFiles` to clean up old `.niwa/sessions/<uuid>/` dirs. Rejected: those dirs are absent from `ManagedFiles` (created by the old session-register hook at runtime, not by the installer), so `cleanRemovedFiles` cannot see them. Leaves permanent filesystem litter.

## Decision Outcome

**Chosen:** the single chosen option per decision above, forming a unified approach.

### Summary

The revised niwa mesh keeps the existing per-session stdio MCP server topology and the existing pipeline integration point (`Applier.runPipeline` step 4.75) and rewrites every other mechanism. Messages flow through per-role inboxes (`.niwa/roles/<role>/inbox/`) provisioned at `niwa apply` time from the workspace topology — one inbox per cloned repo plus `coordinator` at the instance root. Tasks are first-class per-directory state machines under `.niwa/tasks/<task-id>/` with `state.json` (authoritative), NDJSON `transitions.log` (audit trail), and `.lock` (flock target for atomic state transitions across both files). The daemon spawns ephemeral `claude -p` workers on demand, supervises them via `cmd.Wait()` in per-task supervisor goroutines, adopts orphans across daemon restarts via 2-second `IsPIDAlive` polling, and enforces a retry cap of 3 (4 total attempts) with 30/60/90s backoff and a 15-minute stalled-progress watchdog (all configurable via the PRD's env vars). The worker spawn contract is a fixed argv (bootstrap prompt with `<task-id>` substitution plus the R33 flags) plus pass-through env with `NIWA_INSTANCE_ROOT`, `NIWA_SESSION_ROLE`, and `NIWA_TASK_ID` last-wins overrides. A flat uniform `niwa-mesh` skill is installed at both instance-root and per-repo `.claude/skills/` with hash-based idempotency via `InstanceState.ManagedFiles.ContentHash`. The test harness pairs the literal-path spawn override with an MCP-client-driven scripted fake and integer-second timing overrides (plus niwa-owned daemon pause hooks for race-window AC); two residual `@channels-e2e` scenarios cover MCP-config loadability and bootstrap-prompt effectiveness with real `claude -p`. A ~40-60 LOC migration helper at the top of the rewritten `InstallChannelInfrastructure` discards old-layout `.niwa/sessions/<uuid>/` directories (with a one-line stderr warning) on instances upgraded from the prior mesh; `sessions.json` is preserved so coordinator registration survives.

The authorization model is the design's load-bearing invariant. Every task-lifecycle tool call verifies three things under shared flock on `.niwa/tasks/<task-id>/.lock`: (a) the caller's `NIWA_TASK_ID` env var matches the `task_id` argument; (b) the caller's role (from `NIWA_SESSION_ROLE`) matches `state.json.worker_role`; (c) on Linux, walking PPID up one level produces a PID whose start time matches `state.json.worker_pid` and `state.json.worker_start_time` (mandatory check; on macOS the check degrades to PID match only — explicitly within the PRD's trust ceiling). Delegator-side tools (`niwa_await_task`, `niwa_update_task`, `niwa_cancel_task`) verify `envelope.from.role` matches the caller's role. Errors use the PRD's R50 enumeration (`NOT_TASK_OWNER`, `NOT_TASK_PARTY`, `TASK_ALREADY_TERMINAL`, `BAD_PAYLOAD`, `BAD_TYPE`, `UNKNOWN_ROLE`); no new codes introduced — D1's initially-proposed `NOT_TASK_EXECUTOR` was retired when cross-validation adopted D3's PPID-hardening over a separate crypto token.

The daemon sequences concretely. fsnotify on `.niwa/roles/*/inbox/` fires; central goroutine acquires the per-task lock, performs the consumption rename into `in-progress/`, writes `state.json` with `worker.{spawn_started_at, role}` (pid/start_time to be backfilled), releases the lock, spawns `claude -p` via `exec.Command`, re-acquires the lock to backfill the real PID and start_time, starts a supervisor goroutine with `cmd.Wait()`. On `niwa_report_progress` from the worker: MCP server holds the lock, appends to `transitions.log`, updates `state.json.last_progress_at`, releases. On `niwa_finish_task`: MCP server holds the lock, runs `authorizeTaskCall(kindExecutor)`, atomically rewrites `state.json` with the terminal state plus `result` or `reason`, appends the transition, releases — then writes a `task.completed` or `task.abandoned` message to the delegator's inbox via atomic rename. The supervisor's `cmd.Wait()` unblocks when the worker exits; if `state.json.state == "running"` at that moment (no terminal transition recorded), the daemon treats it as unexpected — either starts a backoff timer for the next retry or, if the cap is reached, writes `abandoned` and a `task.abandoned` message. The stalled-progress watchdog is a per-supervisor `time.Timer` reset on every progress update; when it fires, SIGTERM the worker, wait `NIWA_SIGTERM_GRACE_SECONDS`, SIGKILL. Adopted orphans (discovered during startup reconciliation when `state.json.worker_pid` is alive but not a child of the new daemon) are tracked in a central list with no supervisor; the central loop's 2-second tick polls `IsPIDAlive` per orphan, and a transition to dead or diverged-start-time drives the same unexpected-exit path.

### Rationale

The seven decisions form a coherent stack because each is rooted in the same filesystem-as-durable-state principle. `state.json` is the single source of truth for authorization and query (Decision 1); the daemon is stateless across restarts because all state lives on disk (Decision 2); MCP-daemon coordination is filesystem-only because IPC adds a failure mode the file-based design is specifically designed to avoid (Decision 3); worker spawn is a one-way data flow from daemon-written env + state.json to the worker's MCP subprocess (Decision 4); the skill is idempotent-by-content-hash because that matches how every other managed file is tracked (Decision 5); the test harness exercises the real spawn path because doing otherwise under-tests the thing most likely to break (Decision 6); the migration helper just removes old-layout artifacts because preserving pre-1.0 queued envelopes across a schema break does not pay for itself (Decision 7).

The cross-validation between Decision 1 and Decision 3 produced the design's most consequential refinement: Decision 1's initial crypto-token worker-auth was replaced by Decision 3's PPID + start-time check (made mandatory on Linux, best-effort elsewhere). The token provided no marginal protection over the start-time freshness already rotated in `state.json.worker` per spawn, and its same-UID trust ceiling is identical. The trade-off accepted is platform dependency: on macOS, `PIDStartTime` is conservative (returns "alive" without precise timestamp), so the check degrades to PID match only — explicitly within the PRD's documented ceiling.

The combination is deterministically testable because Decision 6's scripted fake runs through the same spawn path (Decision 4's literal-path override) and exercises the same MCP topology (Decision 3's per-session stdio) with the same authorization code path (Decision 3's `authorizeTaskCall`) under the same lock (Decision 1's per-task `.lock`). A failing AC therefore points to one layer, not a multi-layer interaction. The integration-test residual (Decision 6's two `@channels-e2e` scenarios) covers exactly the niwa surface the harness cannot exercise — Claude Code's MCP-config resolution and real-LLM bootstrap-prompt behavior — without introducing LLM-content flake.

## Solution Architecture

### Overview

The mesh is five cooperating components: a provisioning step that writes the workspace-scoped infrastructure at `niwa apply` time; a persistent daemon (`niwa mesh watch`) that claims queued envelopes and spawns ephemeral `claude -p` workers; per-session stdio MCP servers that expose the tool surface and enforce authorization; the filesystem under `.niwa/` that holds durable state; and the `niwa-mesh` skill installed into every agent that encodes behavioral defaults. A user delegates from the coordinator chat; niwa spawns a worker in the target repo; the worker reports progress and completion via MCP tools; the coordinator sees results in its inbox or via blocking / querying calls.

### Components

```
niwa apply
  └─ InstallChannelInfrastructure (internal/workspace/channels.go, step 4.75)
       - migration helper (detect + remove old .niwa/sessions/<uuid>/)
       - create .niwa/roles/<role>/inbox/{in-progress,cancelled,expired,read}/
       - create .niwa/tasks/
       - write <instanceRoot>/.claude/.mcp.json and <repoDir>/.claude/.mcp.json
       - install niwa-mesh skill to <instanceRoot>/.claude/skills/niwa-mesh/
         and each <repoDir>/.claude/skills/niwa-mesh/ (sha256 idempotent)
       - write minimal ## Channels section to workspace-context.md
       - inject SessionStart + UserPromptSubmit hooks via cfg.Claude.Hooks
       - track every written path in InstanceState.ManagedFiles
  └─ SpawnDaemon (after SaveState)
       exec niwa mesh watch --instance-root=<instanceRoot>

niwa mesh watch (internal/cli/mesh_watch.go)
  ├─ central goroutine
  │    - fsnotify watcher on .niwa/roles/*/inbox/
  │    - 2s ticker: adopted-orphan IsPIDAlive poll
  │    - central taskEvent channel (supervisors report exits here)
  │    - state.json writer for spawn/state transitions
  │    - catch-up inbox scan on startup (for envelopes that predate watch)
  │    - reconciliation on startup (reads .niwa/tasks/*/state.json; classifies
  │      running tasks into adopted-orphan list or unexpected-exit recovery)
  ├─ per-task supervisor goroutines (daemon-spawned workers)
  │    - cmd.Wait() on worker process
  │    - stall watchdog (time.Timer; reset on progress poll; SIGTERM → SIGKILL)
  │    - reports exit event to central goroutine
  └─ PID file: .niwa/daemon.pid (flock-guarded; <pid>\n<start_time>\n)

niwa mcp-serve (internal/mcp/server.go, per-session stdio subprocess)
  ├─ Server struct reads env at startup: instanceRoot, role, taskID
  ├─ peer-message tools: niwa_send_message, niwa_check_messages, niwa_ask
  ├─ task delegator tools: niwa_delegate, niwa_query_task, niwa_await_task,
  │     niwa_list_outbound_tasks, niwa_update_task, niwa_cancel_task
  ├─ task worker tools: niwa_report_progress, niwa_finish_task
  ├─ per-session fsnotify watcher on .niwa/roles/<own-role>/inbox/
  │     (extends existing internal/mcp/watcher.go's notifyNewFile)
  ├─ authorizeTaskCall(taskID, kind) helper: shared flock on task .lock,
  │     envelope.json + state.json reads, PPIDChain(1) + start_time check on Linux
  └─ waiter maps (in-process, all values are size-1 buffered chans):
       waiters      map[msgID]chan toolResult        (niwa_ask reply)
       awaitWaiters map[taskID]chan taskEvent        (niwa_await_task, sync niwa_delegate)

claude -p worker (spawned by daemon via exec.Command)
  ├─ argv: fixed bootstrap prompt with <task-id> substitution + R33 flags
  ├─ env: daemon's env + NIWA_INSTANCE_ROOT + NIWA_SESSION_ROLE=<role>
  │       + NIWA_TASK_ID=<task-id>  (last-wins via exec.Cmd.Env)
  ├─ CWD: role's repo dir (or instance root for coordinator)
  └─ first tool call is niwa_check_messages → retrieves envelope → executes task
     → emits niwa_report_progress as it works → calls niwa_finish_task before exit

Filesystem layout:
  .niwa/
  ├── roles/
  │   └── <role>/
  │       └── inbox/
  │           ├── <task-id>.json        (queued envelopes; atomic rename in)
  │           ├── in-progress/<task-id>.json  (claimed by daemon)
  │           ├── cancelled/<task-id>.json    (cancelled by delegator)
  │           ├── expired/<msg-id>.json        (expired messages)
  │           ├── read/<msg-id>.json           (peer messages after check_messages)
  │           └── <msg-id>.json                (peer messages awaiting read)
  ├── tasks/
  │   └── <task-id>/
  │       ├── .lock                    (flock coordination file)
  │       ├── envelope.json
  │       ├── state.json               (authoritative state)
  │       └── transitions.log          (NDJSON audit trail)
  ├── sessions/
  │   └── sessions.json                (coordinator registry; workers not listed)
  ├── daemon.pid
  └── daemon.log
```

### Key Interfaces

**`Server` struct additions** (`internal/mcp/server.go`):

```go
type Server struct {
    // ... existing fields ...
    instanceRoot string           // from NIWA_INSTANCE_ROOT
    role         string           // from NIWA_SESSION_ROLE
    taskID       string           // from NIWA_TASK_ID (empty for coordinators)

    waitersMu    sync.Mutex
    waiters      map[string]chan toolResult  // keyed by reply_to msgID; size-1 buffered
    awaitWaiters map[string]chan taskEvent   // keyed by task_id; size-1 buffered
}
```

**Task ID generation.** Task IDs are UUIDv4 generated via `crypto/rand` (not `math/rand`) to prevent pre-computation by a same-UID attacker attempting to pre-seed a `.niwa/tasks/<id>/` directory. Verified by a unit test asserting generated IDs match the UUIDv4 regex and do not repeat across 10 000 samples.

**`taskEvent` type** — the in-process message carried on waiter channels and between daemon goroutines:

```go
type taskEventKind int

const (
    evtCompleted taskEventKind = iota
    evtAbandoned
    evtCancelled
    evtProgress    // non-terminal; used internally by awaitWaiter (ignored) but also by daemon supervisor → central loop
    evtUnexpectedExit
    evtAdopted
)

type taskEvent struct {
    TaskID    string
    Kind      taskEventKind
    ExitCode  int              // valid when Kind == evtUnexpectedExit
    Result    json.RawMessage  // valid when Kind == evtCompleted
    Reason    json.RawMessage  // valid when Kind == evtAbandoned
    At        time.Time
}
```

The per-session MCP server's `awaitWaiters` dispatches `evtCompleted`, `evtAbandoned`, or `evtCancelled` to the waiter's channel on receipt of the matching inbox message. The daemon's central loop consumes all event kinds from per-task supervisors for state-transition decisions.

**`TaskStore.UpdateState` — transactional state mutation.** A single helper exported from `internal/mcp/taskstore` encapsulates the flock → read → validate → mutate → tmp+rename → fsync-parent → append-log → unlock sequence so every caller (daemon, MCP tool handlers) applies the same discipline:

```go
// UpdateState atomically transitions a task's state. The mutator function
// receives the current state and returns the new state plus a transition
// log entry. All writes happen under per-task flock on .lock.
//
// Returns:
//   - ErrStateMismatch: mutator's expected "from" state didn't match on-disk state
//   - ErrAlreadyTerminal: task is already completed/abandoned/cancelled
//   - ErrCorruptedState: state.json parse failed (fail-closed)
//   - nil: state.json and transitions.log both written durably
func UpdateState(taskDir string, mutator func(cur *TaskState) (*TaskState, *TransitionEntry, error)) error
```

`authorizeTaskCall` uses a read-only variant (`ReadState(taskDir string) (*TaskEnvelope, *TaskState, error)`) that acquires a shared flock. Both `UpdateState` and `ReadState` fail closed on torn / malformed JSON (return `ErrCorruptedState`, which callers surface as `NOT_TASK_PARTY` for auth path and as an internal error for admin paths).

All flock acquisitions use a 30-second bounded timeout (`syscall.Flock` with a retry loop) to prevent indefinite deadlock against a hung holder. Timeout surfaces as `ErrLockTimeout`; callers treat it as retryable.

**Task envelope** (`.niwa/tasks/<task-id>/envelope.json`, PRD R15 v=1):

```json
{
  "v": 1, "id": "<uuid>",
  "from": {"role": "coordinator", "pid": 1234},
  "to":   {"role": "web"},
  "body": {},
  "sent_at": "RFC3339",
  "parent_task_id": "<uuid>",
  "expires_at": "RFC3339"
}
```

**Task state** (`.niwa/tasks/<task-id>/state.json` v=1): schema from Decision 1.

**`authorizeTaskCall(taskID string, kind accessKind) (*envelope, *taskState, *toolResult)`** in `internal/mcp/server.go`:

```go
const (
    kindDelegator accessKind = iota  // niwa_await_task, niwa_update_task, niwa_cancel_task
    kindExecutor                      // niwa_finish_task, niwa_report_progress
    kindParty                         // niwa_query_task
)
```

Returns `(*envelope, *taskState, nil)` on success, or `(nil, nil, *toolResult)` with a `NOT_TASK_OWNER` / `NOT_TASK_PARTY` / `TASK_ALREADY_TERMINAL` error. Acquires shared flock on `.niwa/tasks/<task-id>/.lock`, reads both `envelope.json` and `state.json`, performs role / task-ID / PPID-start-time checks per Decision 3.

**Daemon PID file** (`.niwa/daemon.pid`):

```
<pid>\n
<start_time_jiffies>\n
```

Written atomically (`daemon.pid.tmp` → rename) after the daemon's fsnotify watches and central goroutine are ready. `niwa apply` uses a flock on `.niwa/daemon.pid.lock` before reading the PID file to prevent two concurrent applies from racing to spawn two daemons.

**Spawn contract** (daemon → `claude -p`): fixed argv with one `<task-id>` slot; env carries `NIWA_INSTANCE_ROOT`, `NIWA_SESSION_ROLE`, `NIWA_TASK_ID` as last-wins overrides; CWD is the role's repo directory.

**`NIWA_*` environment variables** (R51 + this design):

| Variable | Default | Purpose |
|---|---|---|
| `NIWA_CHANNELS` | unset | opt-in (`1`) or suppression (`0`) |
| `NIWA_INSTANCE_ROOT` | (set by niwa) | instance root path; inherited by workers |
| `NIWA_SESSION_ROLE` | (set by niwa) | caller's role for MCP auth |
| `NIWA_TASK_ID` | (set by daemon on worker spawn) | task identity for executor auth |
| `NIWA_WORKER_SPAWN_COMMAND` | unset (uses `claude`) | literal path to substitute for `claude -p` |
| `NIWA_RETRY_BACKOFF_SECONDS` | `30,60,90` | comma-separated backoffs between restart attempts |
| `NIWA_STALL_WATCHDOG_SECONDS` | `900` | stalled-progress threshold (15 min) |
| `NIWA_SIGTERM_GRACE_SECONDS` | `5` | SIGTERM → SIGKILL escalation |
| `NIWA_DESTROY_GRACE_SECONDS` | `5` | `niwa destroy` daemon-shutdown grace |
| `NIWA_TEST_PAUSE_BEFORE_CLAIM` | unset | daemon pause hook for race-window tests |
| `NIWA_TEST_PAUSE_AFTER_CLAIM` | unset | daemon pause hook for race-window tests |

### Data Flow

**Delegation, happy path (sync):**

```
Coordinator calls niwa_delegate(to="web", mode="sync", body={...})
  → MCP server (coordinator's session) creates task dir .niwa/tasks/<T>/
  → writes envelope.json, state.json (state=queued), inserts in
    .niwa/roles/web/inbox/<T>.json via atomic rename
  → registers awaitWaiters[T]
  → blocks in select on awaitCh and timeout (no sync timeout; bounded by R34/R36)

Daemon central goroutine (fsnotify fires):
  → acquires .niwa/tasks/<T>/.lock
  → consumption rename: inbox/<T>.json → inbox/in-progress/<T>.json
  → writes state.json (state=running, worker.{pid:0, start_time:0, role:web,
    spawn_started_at}, transitions append)
  → releases lock
  → starts supervisor goroutine, calls cmd.Start() via exec.Command("claude", ...)
  → re-acquires lock, backfills worker.{pid, start_time} from cmd.Process.Pid
    and PIDStartTime, writes state.json, releases lock

Worker (claude -p):
  → LLM reads bootstrap prompt with <task-id>
  → calls niwa_check_messages → MCP server returns envelope from
    .niwa/roles/web/inbox/in-progress/<T>.json (or per-task dir)
  → LLM executes the task: git ops, file writes, PR creation, etc.
  → calls niwa_report_progress("scaffolding schema...") periodically
    → MCP server: authorizeTaskCall(T, kindExecutor) → flock → append
      transitions.log → update state.json.last_progress → unlock
    → also writes .niwa/roles/coordinator/inbox/<msg-uuid>.json for coordinator
  → final: calls niwa_finish_task(T, outcome="completed", result={...})
    → MCP server: authorizeTaskCall(T, kindExecutor) → flock → write
      state.json (state=completed, result) → append transitions → unlock
    → writes .niwa/roles/coordinator/inbox/<msg-uuid>.json (type=task.completed,
      body={task_id:T, result})
  → LLM exits; worker process terminates

Supervisor goroutine:
  → cmd.Wait() returns
  → sends {taskID:T, exitCode:0} to central taskEvent channel

Central goroutine:
  → acquires lock on T; reads state.json; state == completed (already terminal);
    no action needed
  → releases lock

Coordinator's MCP server:
  → notifyNewFile fires for .niwa/roles/coordinator/inbox/<msg-uuid>.json
  → parses message; type == task.completed; body.task_id == T
  → looks up awaitWaiters[T]; sends taskEvent{completed, result}
  → coordinator's handleDelegateSync returns {status:"completed", result}

Coordinator's Claude chat:
  → tool result appears inline; LLM presents it to the user.
```

**niwa_ask to a role with no running worker:**

```
Coordinator calls niwa_ask(to="reviewer", body={...})
  → MCP server creates task envelope with body.kind="ask", writes to
    .niwa/roles/reviewer/inbox/<T>.json, state=queued
  → registers awaitWaiters[T] (or a variant keyed on reply_to)
  → blocks in select

Daemon spawns worker for T (same path as delegation).

Worker (claude -p in reviewer's repo):
  → LLM reads bootstrap, calls niwa_check_messages, sees body.kind=="ask"
  → skill instructs LLM to answer and call niwa_send_message(
      to="coordinator", reply_to=<question msg id>, body={"answer":...})
  → Worker then calls niwa_finish_task with outcome="completed"

Coordinator's MCP server:
  → notifyNewFile fires for the answer message; waiter matches via reply_to
  → handleAsk returns the answer body
```

**Cancel vs claim race (AC-Q10):**

```
Delegator calls niwa_cancel_task(T) at the same moment the daemon
acquires .niwa/tasks/T/.lock for the claim path.

Path A (delegator wins):
  → niwa_cancel_task acquires lock first
  → atomic rename inbox/<T>.json → inbox/cancelled/<T>.json; writes state.json
    (state=cancelled); releases lock
  → returns {status:"cancelled"}
  → Daemon then acquires lock, attempts consumption rename on <T>.json,
    finds it missing (ENOENT), treats as "cancellation won the race," skips
    spawn, releases lock.

Path B (daemon wins):
  → Daemon acquires lock first
  → consumption rename; writes state.json (state=running); spawns worker;
    releases lock
  → niwa_cancel_task acquires lock second
  → atomic rename on inbox/<T>.json fails (ENOENT); reads state.json (running);
    returns {status:"too_late", current_state:"running"}
```

**Worker crash + restart:**

```
Worker process crashes mid-task (segfault, OOM, LLM hits context limit and
exits without calling niwa_finish_task).
  → Supervisor goroutine's cmd.Wait() returns with exit error
  → reports to central goroutine: {taskID:T, exitCode:<nonzero>}

Central goroutine:
  → acquires lock on T
  → reads state.json: state == running, no terminal transition recorded
  → classifies as unexpected exit; bumps restart_count; appends
    transitions.log ("unexpected_exit")
  → if restart_count < max_restarts:
      * schedule retry: time.AfterFunc(backoff[restart_count-1], spawnFunc)
      * releases lock
  → else:
      * writes state.json (state=abandoned, reason="retry_cap_exceeded")
      * writes task.abandoned message to delegator's inbox
      * releases lock

After backoff elapses:
  → central goroutine spawns fresh worker for T via exec.Command
  → backfills state.json.worker.{pid, start_time} under lock
  → new supervisor goroutine takes over
```

**Daemon crash + orphan adoption:**

```
Daemon process killed (SIGKILL, OOM, machine restart).
Worker process that was running at the time may still be alive.

User runs niwa apply (or the old daemon is restarted by automation).
Channel installer spawns a fresh niwa mesh watch.

New daemon startup:
  → acquires flock on .niwa/daemon.pid.lock; writes its own PID
  → reconciliation pass: lists .niwa/tasks/*/state.json
  → for each task with state == running:
      * read worker.{pid, start_time}
      * if IsPIDAlive(pid, start_time) == true:
          - add to adopted-orphan list; writes state.json.worker.adopted_at
          - central loop's 2s ticker will poll this orphan
      * if IsPIDAlive(pid, start_time) == false:
          - classifies as unexpected exit; bumps restart_count;
            schedules retry or abandons (same path as supervisor-reported exit)
  → registers fsnotify watches on .niwa/roles/*/inbox/
  → catch-up scan: any envelopes in inbox/<T>.json (no in-progress/ version,
    state.json state == queued) are treated as fresh; claim + spawn.
```

## Implementation Approach

### Phase 1: Storage primitives and state machine types

Foundation layer. No external behavior changes yet.

Deliverables:
- `internal/mcp/types.go` — update `Message` envelope schema for v=1 per PRD R15; add `TaskEnvelope`, `TaskState`, `StateTransition` types.
- `internal/mcp/taskstore.go` (new) — `OpenTaskLock(taskDir)`, `ReadState(taskDir)`, `WriteState(taskDir, new *TaskState)` with the flock → tmp → rename → fsync → append-log → unlock sequence. Exported as a small, testable package with unit tests exercising concurrent writers.
- `internal/mcp/liveness.go` — extend with `PIDStartTime` graceful macOS degradation (already partially exists); add `PPIDChain(n int) ([]int, error)` helper that walks up `n` PPID levels.
- `internal/mcp/auth.go` (new) — `authorizeTaskCall(s *Server, taskID string, kind accessKind) (*TaskEnvelope, *TaskState, *ToolResult)` with the role / task-ID / PPID-start-time checks from Decision 3.

### Phase 2: Channel installer rewrite

Rewrite provisioning. Does not yet hook in the new MCP tools or daemon — just writes the directory structure, skill, and mcp.json so existing tests see the new layout.

Deliverables:
- `internal/workspace/channels.go` — replace `InstallChannelInfrastructure` body:
  - Migration helper: detect + remove `.niwa/sessions/<uuid>/` directories; emit one-shot stderr warning; preserve `sessions.json`.
  - Create `.niwa/roles/<role>/inbox/{in-progress,cancelled,expired,read}/` for every role.
  - Create `.niwa/tasks/`, `.niwa/daemon.pid` placeholder (empty).
  - Write `<instanceRoot>/.claude/.mcp.json`; mirror into each `<repoDir>/.claude/.mcp.json`.
  - Write `niwa-mesh/SKILL.md` to instance-root and per-repo; sha256 hash check vs `InstanceState.ManagedFiles.ContentHash` for idempotency; drift warning on mismatch; overwrite.
  - Write minimal `## Channels` section to `workspace-context.md`.
  - Inject SessionStart + UserPromptSubmit hook entries into `cfg.Claude.Hooks` for the coordinator role only (workers do not use these hooks).
  - Track every written path in `InstanceState.ManagedFiles`.
- `internal/workspace/channels_test.go` — rewrite unit tests for the new layout; assert on directory existence, file contents (sha256), ManagedFiles entries, and idempotent re-apply (byte-identical files; no drift warning on second run).
- `docs/guides/cross-session-communication.md` — update the user-facing guide to describe the new tool surface and task model.

### Phase 3: MCP server tool surface

Expose the new tool set. Per-session stdio server architecture is unchanged; adds the task-lifecycle tools.

Deliverables:
- `internal/mcp/server.go`:
  - `Server` struct additions: `taskID string`, `awaitWaiters map[string]chan taskEvent`.
  - Startup reads `NIWA_TASK_ID` from env into `s.taskID`.
  - Dispatcher adds the new tool handlers: `handleDelegate`, `handleQueryTask`, `handleAwaitTask`, `handleListOutboundTasks`, `handleUpdateTask`, `handleCancelTask`, `handleReportProgress`, `handleFinishTask`.
  - Revises `handleAsk` to create a first-class task (body.kind="ask") when target role has no running worker.
  - Revises `handleSendMessage` to drop delivery-status return; message is written via atomic rename and that's the success criterion.
  - `notifyNewFile` extension: inspects type; routes `task.completed/abandoned/cancelled` to `awaitWaiters[body.task_id]`; existing `reply_to` path for ask remains.
  - All task-lifecycle handlers call `authorizeTaskCall` at the top; all use the per-task `.lock` via `taskstore`.
- `internal/mcp/server_test.go` — unit tests with in-process MCP client exercising each tool handler directly (handler-level unit tests; full cancel-vs-claim race coverage requires the daemon's pause hooks and is deferred to Phase 4b+6).
- `internal/mcp/handlers_task.go` (new file; handlers grouped here for readability).

### Phase 4a: Daemon core — spawn, wait, classify

Build the minimum daemon that can claim a queued envelope, spawn a worker, and observe its exit. No backoff, no watchdog, no orphan adoption yet.

Deliverables:
- `internal/cli/mesh_watch.go`: central goroutine structure with fsnotify on `.niwa/roles/*/inbox/`; `taskEvent` channel; catch-up inbox scan on startup; consumption-rename claim path under per-task `TaskStore.UpdateState`; spawn via `exec.Command` with fixed argv + env overrides (Decision 4); `exec.LookPath("claude")` resolved once at startup and logged at INFO; `claude` binary path is absolute for every subsequent spawn; per-task supervisor goroutine calling `cmd.Wait()` and reporting exit events.
- `internal/cli/mesh_watch_test.go`: unit tests for: clean spawn-and-finish path; supervisor-reported unexpected exit; `exec.LookPath` resolution and logging.

### Phase 4b: Restart cap + backoff + unexpected-exit classification

Deliverables:
- Restart-cap enforcement with linear backoff via `time.AfterFunc` using `NIWA_RETRY_BACKOFF_SECONDS` (comma-separated integers).
- Exit-event classification: compare `state.json.state` to expected `"running"` at `cmd.Wait()` return; classify as unexpected if state.json is still `running`; bump `restart_count`; schedule retry or transition to `abandoned` with `reason: "retry_cap_exceeded"`.
- Tests: flapping worker reaches retry cap; fast-completing worker doesn't trigger restart; `niwa_fail_task` from worker doesn't consume a restart slot.

### Phase 4c: Stall watchdog + SIGTERM/SIGKILL escalation

Deliverables:
- Per-supervisor `time.Timer` reset on every detected `niwa_report_progress` (via 2 s poll of `state.json.last_progress.at`).
- SIGTERM on stall; SIGKILL after `NIWA_SIGTERM_GRACE_SECONDS`.
- Watchdog-triggered exits classified as unexpected (consume retry slot).
- Defensive reap: if `state.json.state` is terminal but the worker process is still alive (worker hung after `niwa_finish_task`), the supervisor sends SIGTERM after a short grace (`NIWA_SIGTERM_GRACE_SECONDS` applies).
- Tests with `NIWA_STALL_WATCHDOG_SECONDS=2`: stall triggers SIGTERM; SIGTERM-resistant worker gets SIGKILL after grace.

### Phase 4d: Reconciliation + adopted-orphan polling

Deliverables:
- Startup reconciliation: list `.niwa/tasks/*/state.json`; for each `running` task, read `worker.pid` and `worker.start_time`; if `IsPIDAlive(pid, start_time) == true` add to adopted-orphan list and write `worker.adopted_at` under lock; if false, drive unexpected-exit classification.
- Central loop 2 s ticker: per orphan, re-check `IsPIDAlive(pid, start_time)` (both PID existence AND start_time match; divergent start_time classified as unexpected exit to defend against PID-reuse).
- `daemon.pid` + `daemon.pid.lock` flock (lock owned by the daemon; `niwa apply` acquires shared lock before reading the PID file to prevent racing two concurrent applies into two daemons).
- Tests: daemon-kill-with-live-worker → new daemon adopts; daemon-kill-with-dead-worker → unexpected exit path applied; PID reuse with divergent start_time classified as dead.

### Phase 4e: Test-harness hooks

Deliverables:
- `NIWA_WORKER_SPAWN_COMMAND` honored: when set to a literal path, the daemon substitutes it for the resolved `claude` path; argv, env, CWD unchanged.
- `NIWA_TEST_PAUSE_BEFORE_CLAIM` / `NIWA_TEST_PAUSE_AFTER_CLAIM` hooks: when set, the daemon blocks at the named point until a filesystem marker file is removed, making race-window tests deterministic.
- Tests: fake binary invocation; pause hooks assert consumption-rename is paused.

### Phase 4 notes (shared across 4a–4e)

- `internal/cli/destroy.go` receives its grace-window update once in Phase 4a (uses `NIWA_DESTROY_GRACE_SECONDS`); destroy sends SIGKILL to worker PGIDs FIRST (not grace then kill) to minimize the attack window for compromised workers during teardown, then SIGTERM→SIGKILL the daemon. This is a security hardening beyond the PRD's minimum (`SIGTERM → grace → SIGKILL` applies to the daemon).
- All timing thresholds read from env at daemon startup with PRD-configured defaults; no hot-reload in v1.
- Flock acquisition in the daemon uses a 30 s bounded timeout.

### Phase 5: CLI subcommand surface

New observability commands.

Deliverables:
- `internal/cli/task.go` (new) — `niwa task` cobra subcommand group.
  - `niwa task list [--role --state --delegator --since]` (per PRD R42).
  - `niwa task show <task-id>` (per PRD R43).
- `internal/cli/session.go` — simplify `niwa session list` to list coordinators only (workers removed per R40).
- `internal/cli/status.go` — add one-line mesh summary to status detail view per PRD R44.

### Phase 6: Test harness

Scripted fake + Gherkin helpers.

Deliverables:
- `test/functional/worker_fake/` — Go binary, takes scripted scenario from env vars or stdin; connects as MCP client; exercises scenarios (happy-path completion; progress-then-fail; progress-then-crash; long-stall; ask-response).
- `test/functional/steps_test.go` — step helpers:
  - `runWithFakeWorker(scenario string)` — sets `NIWA_WORKER_SPAWN_COMMAND` to the compiled fake's path.
  - `pauseDaemonAt(hook string)` — sets the appropriate `NIWA_TEST_PAUSE_*`; releases via fs marker.
  - `setTimingOverrides(map[string]string)` — sets `NIWA_RETRY_BACKOFF_SECONDS` etc. to small values.
- `test/functional/features/mesh.feature` — rewrite existing scenarios for the new tool surface; add scenarios for: sync/async delegation; sender-side queue mutation; restart cap; abandonment; daemon crash recovery; race windows.
- `test/functional/suite_test.go` — register new steps.

### Phase 7: @channels-e2e scenarios

Residual live-Claude integration coverage.

Deliverables:
- `test/functional/features/mesh.feature` — two `@channels-e2e` scenarios:
  - "coordinator delegates to web; real claude -p worker retrieves envelope and completes" (MCP-config loadability + bootstrap-prompt effectiveness).
  - "coordinator dispatches to never-registered role; daemon spawns worker and task completes" (end-to-end production flow).
- Both tagged `@channels-e2e`; skipped when `claude` not on PATH or `ANTHROPIC_API_KEY` unset.

### Phase 8: Documentation

Sync docs with the new model.

Deliverables:
- `docs/guides/cross-session-communication.md` — rewrite with the new tool surface, task lifecycle, worker spawn model, override mechanisms.
- `docs/guides/functional-testing.md` — add a "Testing the mesh" section covering the harness: `NIWA_WORKER_SPAWN_COMMAND`, timing overrides, daemon pause hooks.
- Delete the two prior-art design files (`docs/designs/current/DESIGN-cross-session-communication.md` and `docs/designs/DESIGN-channels-integration-test.md`).
- Move the accepted `docs/designs/DESIGN-cross-session-communication.md` to `docs/designs/current/` as part of the acceptance transition.

## Consequences

### Positive

- **Uniform tool surface regardless of worker liveness.** A coordinator calls `niwa_delegate(to="web")` the same way whether `web` has ever been opened or not. Niwa spawns the worker when needed. This is the headline win the PRD was written to deliver.
- **Task-level semantics decoupled from process lifetime.** A task completes only via explicit `niwa_finish_task`; a crashing worker is a restart event, not a silent success. The delegator has a real handle (`task_id`) for query, await, update, cancel.
- **Crash-safe at every level.** Messages live in files, not memory; the daemon is stateless across restarts; per-task state is recovered via `state.json` on daemon restart; `cmd.Wait()` + atomic renames preserve at-most-once claim and at-most-once completion.
- **Deterministic testability.** The test harness (`NIWA_WORKER_SPAWN_COMMAND` + timing overrides + daemon pause hooks) makes every PRD AC verifiable in seconds without involving a live Claude; race-window AC are reproducible on demand via pause hooks.
- **Zero-config in the common case.** Workspaces with descriptive repo names get a full mesh from `--channels` or `NIWA_CHANNELS=1` alone; no role map needed; hybrid activation covers team, personal, and one-off needs.
- **Forward-compatible tool API.** The tool contracts encode no assumption about how workers are started or woken. If Claude Code's Channels push ever becomes viable, only the daemon's spawn code changes; the tool surface is unaffected.
- **No new runtime dependencies.** Go stdlib + fsnotify (already in `go.mod`). No new IPC layer, no cryptographic tokens, no database.
- **Observability.** `niwa task list`, `niwa task show`, and the `transitions.log` NDJSON audit trail give a complete record of every task's lifecycle for debugging and post-mortems.

### Negative

- **Per-task fsync cost bounds transition rate.** Each state transition fsyncs `state.json`, the parent directory, and `transitions.log` — approximately 3 fsync round-trips per transition. On slow disks this is user-observable under pathological load (many concurrent delegations). Within PRD R47's 1000-iteration concurrent-writer stress profile, still well under latency budgets.
- **macOS worker-auth degradation.** `PIDStartTime` is conservative on macOS (returns "alive" without a precise timestamp), so the PPID + start-time check degrades to PID match only. This is weaker than the Linux path but within the PRD's trust ceiling ("role integrity is the only trust boundary"). Documented as a Known Limitation.
- **Stalled-progress watchdog has a 15-minute default.** A worker stuck in a non-progressing loop burns tokens until the watchdog fires. The threshold is configurable (`NIWA_STALL_WATCHDOG_SECONDS`) but a lower default would produce false-positives on legitimately-slow operations.
- **Task directories accumulate indefinitely.** `.niwa/tasks/<task-id>/` directories are not garbage-collected in v1. A long-lived workspace with thousands of completed tasks grows unbounded. Manual `rm` is the workaround; `niwa mesh gc` is v2.
- **Daemon doesn't survive machine restarts.** After reboot, all instance daemons are gone. User must run `niwa apply` to restore. Tasks that were `running` at reboot time go through adopted-orphan reconciliation when the daemon returns (if the worker process survived) or through unexpected-exit path (if it did not).
- **Pre-1.0 migration discards queued envelopes.** Instances provisioned under the old mesh lose any unconsumed envelopes on upgrade; the migration helper emits a one-line stderr warning but does not preserve state. Pre-1.0 posture makes this acceptable; users with critical in-flight work should `niwa destroy` + `niwa create --channels` on their own schedule.
- **No in-flight task cancellation.** A delegator that wants to stop a running task waits for the worker to exit or the watchdog to fire. `niwa_cancel_task` only works for `queued` tasks. In-flight cancellation is v2.
- **Delegator liveness bounds sync semantics.** `niwa_delegate(mode="sync")` and `niwa_await_task` only return if the coordinator's session is alive. Results accumulate in the inbox for asynchronous pickup via `niwa_check_messages` or `niwa_query_task` on the next session start, but sync behavior requires a live coordinator.
- **Role integrity is the only trust boundary against same-UID processes.** An agent that overrides `NIWA_SESSION_ROLE` can act on tasks belonging to the spoofed role. Per-agent keys / cryptographic signing is out of scope.
- **PPID-walk assumes claude → mcp-serve spawn topology.** If Claude Code ever introduces a helper layer between `claude -p` and `niwa mcp-serve`, the `PPIDChain(1)` check lands on the wrong PID and fails closed (auth rejects legitimate workers). This is the safe direction — the check does not silently pass — but real workers start failing `niwa_finish_task`. Mitigated by: (a) Decision 3 retains the token-file alternative as a migration path with identical API; (b) functional tests via the scripted fake assert that `PPIDChain(1)` lands on the PID the daemon recorded; regression will surface in CI before users are affected.
- **`worker.pid == 0` race window on first tool call.** Between the daemon's consumption-rename (writes `state.json` with `worker.pid=0, spawn_started_at=now`) and the subsequent backfill after `cmd.Start()` (writes `worker.pid=<real>, worker.start_time=<real>`), a freshly-spawned worker's first task-authorized tool call will fail `authorizeTaskCall` with `NOT_TASK_PARTY`. The happy path (worker's first call is `niwa_check_messages`, which is not task-authorized) hides this window. Scripted workers calling `niwa_report_progress` or `niwa_finish_task` as their first action must retry on `NOT_TASK_PARTY` for a brief initial window (millisecond-scale under normal operation). The test harness's scripted fake implements retry-with-backoff on this error for up to 2 seconds before surfacing.
- **Completion is a behavioral contract, not structurally verified.** A worker LLM that calls `niwa_finish_task(outcome="completed", result={"ok":true})` without actually performing the delegated work will mark the task complete and exit. Niwa has no way to detect this from the outside. The restart cap bounds the blast radius of a worker that fails in an obvious way (crash, timeout, early exit), but not of one that returns a plausible-looking false result. Documented as a PRD Known Limitation; a v2 heuristic ("reject finish_task if no progress event was emitted") is tracked but deferred.
- **Single-worker-per-role sequential execution limits parallelism.** Two queued tasks for the same role run one after the other; the daemon does not parallelize within a role (this is the git-conflict-avoidance design choice). In workflows where one role has many independent quick tasks (e.g., "run 20 test-case generators in the `tests` repo"), the latency is sum-of-tasks rather than max. PRD Out of Scope.
- **MCP server process count scales with session count.** Each Claude session — coordinator plus every running worker — runs its own stdio `niwa mcp-serve` subprocess with its own fsnotify watch, its own waiter map, its own JSON decoder. For a coordinator delegating to three parallel roles, the process count during peak work is ≥ 4 (1 coordinator claude + 1 coordinator mcp-serve + 3 worker claudes + 3 worker mcp-serves = ~8). Visible in `htop` but not a scaling concern at realistic session counts.
- **Hand-rolled `/proc/<pid>/stat` parsing on Linux.** `PIDStartTime` and `PPIDChain` rely on parsing `/proc/<pid>/stat` fields 22 (starttime) and 4 (ppid). The Go stdlib does not wrap this. If future kernels change the `/proc/<pid>/stat` layout, the parsing needs updating. Existing code in `internal/mcp/liveness.go` already has this dependency; this design extends it but does not introduce it.
- **Removed roles leave orphan inbox directories.** If a user removes a repo from `workspace.toml` and re-runs `niwa apply`, the now-unreferenced `.niwa/roles/<old-role>/inbox/` directory is not garbage-collected. Queued envelopes for the removed role survive but are never consumed. Manual cleanup (`rm -rf .niwa/roles/<old-role>/`) is the v1 workaround; GC is v2.
- **Instance root must be on a local POSIX filesystem.** The atomic-rename + flock + parent-directory-fsync pattern assumes local filesystem semantics (ext4/xfs/btrfs/apfs/tmpfs all qualify; ext4's `data=ordered` default is the reference model). NFS, SMB, sshfs, and other network filesystems have varying atomic-rename and advisory-flock semantics and are unsupported in v1. Placing `.niwa/` on tmpfs is allowed but trades durability for the lifetime of the filesystem.

### Mitigations

- **fsync cost**: acceptable within PRD's stated stress profile; if a future workload saturates this, batching or write-coalescing is a local optimization.
- **macOS degradation**: documented as Known Limitation; PRD's trust ceiling already accepts role-based integrity.
- **Watchdog default**: 15 minutes is conservative; users with known-slow tasks bump `NIWA_STALL_WATCHDOG_SECONDS` per invocation; users with known-fast tasks lower it.
- **Task dir accumulation**: documented as Known Limitation; `niwa mesh gc` command and retention policy are tracked as v2 work.
- **Reboot recovery**: `niwa apply` is the documented recovery path; daemon reconciliation handles stale state. A future `niwa mesh start` subcommand could expose targeted per-instance restart.
- **Migration warning**: explicit stderr warning on first post-upgrade apply; documented in user-facing guide; pre-1.0 posture makes envelope loss the accepted tradeoff for a clean break.
- **In-flight cancellation**: documented as Known Limitation; `niwa_fail_task` from inside a running worker is the v1 path for a worker that wants to bail; watchdog catches runaways.
- **Delegator liveness**: `niwa_query_task` is the pull-surface backstop; `task.completed` / `task.abandoned` messages accumulate in the delegator's inbox for offline pickup.
- **Role spoofing**: per-agent cryptographic identity is out of PRD scope; documented as Known Limitation and a deliberate choice for v1.
- **PPID assumption**: the token-file alternative (Decision 3's rejected C-path) is explicitly retained as a drop-in replacement if Claude Code's MCP topology changes; the MCP tool contract and authorization semantics would not change.

## Security Considerations

### Trust Model

The design operates under two distinct trust boundaries, which must not be conflated:

1. **Process-level (same-UID) trust.** Niwa relies on standard Unix filesystem permissions (0600 files, 0700 directories under `.niwa/`, independent of umask) to prevent cross-UID access. Processes running under the same UID as the user are trusted to cooperate; the mesh is not hardened against a malicious same-UID attacker. Per-agent cryptographic identity, message signing, and encryption are explicit Out of Scope items for v1. This is the PRD's stated "role integrity is the only trust boundary" ceiling, and it applies to `NIWA_SESSION_ROLE` spoofing, advisory flock bypass, and PID-based auth degradation on macOS.

2. **Data-plane (task body) trust.** Task bodies are written by the delegating LLM and read by the executing LLM. Neither process is adversarial at the UID level, but the *content* flowing between them is untrusted input: a coordinator LLM can be prompt-injected by earlier user input; a delegator can emit a body that tries to hijack a worker's behavior. This is not a same-UID attacker problem — it is a data-plane attack through the feature's intended interface. The bootstrap-prompt-via-argv + body-via-inbox separation (Decision 4) and `--permission-mode=acceptEdits` (Decision 4) interact to define the blast radius of a prompt-injected worker.

The distinction matters because the same sentence ("within PRD trust ceiling") has been mis-applied to both boundaries. Same-UID risks (role spoofing, flock bypass) are consciously accepted. Data-plane risks (prompt injection into a `acceptEdits`-enabled worker) are reachable by any user of the feature, not only by a malicious local-UID attacker, and are surfaced as explicit PRD Known Limitations rather than quietly accepted.

### Worker Authorization

Worker-initiated task-lifecycle tools (`niwa_finish_task`, `niwa_report_progress`) are authorized by a three-factor check performed by the MCP server under shared flock on `.niwa/tasks/<task-id>/.lock`:

1. `NIWA_TASK_ID` env matches the caller's `task_id` argument.
2. `NIWA_SESSION_ROLE` env matches `state.json.worker.role`.
3. On Linux, a PPID walk from the MCP server up to its parent `claude -p` process produces a PID whose start time matches `state.json.worker.{pid, start_time}`. This check is mandatory on Linux and defeats naive role-spoofing attempts by same-UID processes that merely set env vars.

On macOS, where `PIDStartTime` returns a conservative alive/dead answer without a precise timestamp, the PPID check degrades to PID-match-only. This is **strictly weaker** than the Linux path: a same-UID attacker can `exec` a fake `mcp-serve` whose direct parent PID happens to match a legitimately-spawned worker's PID and pass the check. **Surfaced as a PRD Known Limitation** ("macOS worker authentication is strictly weaker than Linux") rather than framed as equivalent security. Users requiring the strongest same-UID process isolation should run niwa on Linux.

If Claude Code's MCP subprocess spawning topology ever changes (pooling, proxying), the PPID walk could silently stop working. Decision 3 retains a per-task crypto token (`NIWA_TASK_TOKEN` + `.niwa/tasks/<id>/worker.token`) as a drop-in migration path with identical API surface.

### Role Spoofing

A process that sets `NIWA_SESSION_ROLE` can dispatch envelopes attributed to that role and can mutate (update, cancel, query) delegated tasks belonging to that role. The PPID-start-time check defeats worker-side spoofing on Linux but does not protect delegator-side tools. This is the acknowledged v1 trust ceiling.

### Prompt Injection (Data-Plane Attacks)

Task bodies are written by the delegating LLM and read by the executing LLM. The bootstrap prompt does **not** contain the body; the worker retrieves the body via its first `niwa_check_messages` tool call, isolating delegator-controlled content from niwa's control-plane instructions in argv. The niwa-mesh skill explicitly instructs workers to treat body content as untrusted input, and the MCP server's `niwa_check_messages` response wraps retrieved bodies inside a stable outer envelope with a clear "untrusted content" boundary marker so the worker's LLM has a visual demarcation.

These defenses are behavioral and structural at the argv/env layer. They do **not** prevent prompt-injection attacks that operate within the worker LLM's tool-call semantics. The residual risks flow through the feature's intended interface:

- A malicious body can influence a worker LLM to call `niwa_finish_task(completed, result={fake})` without performing the actual work. Completion is a behavioral contract; niwa cannot detect false-completion structurally. Surfaced as a PRD Known Limitation ("Completion is a behavioral contract"). A v2 heuristic — reject `niwa_finish_task` on a task that emitted zero progress events — is tracked but out of scope.
- A malicious body can attempt to impersonate niwa control-plane messages. The final line of defense is Claude Code's rendering of tool-response content and the MCP server's outer envelope wrapping on `niwa_check_messages`.
- **`acceptEdits` amplifies every prompt-injection attack into a filesystem-write primitive.** A prompt-injected worker writes to files in the role's repo directory without prompting — divergent from normal Claude Code sessions where edits require confirmation. This is a real risk reachable through the feature's data plane (malicious delegation body), not a same-UID local-process attack. **Surfaced as a PRD Known Limitation** ("`acceptEdits` amplifies prompt-injection blast radius"). Per-role permission-mode overrides are v2; until then, users who cannot accept this blast radius should disable channel delegation entirely.

### File Mode Discipline

All writes under `.niwa/` use mode 0600 for files and 0700 for directories, applied via explicit `os.Chmod` after open/mkdir to override any umask. PRD AC-P14 verifies this with `umask 0000`. The implementation should additionally:

- Use `O_NOFOLLOW` on opens of `state.json`, `envelope.json`, `.lock`, and inbox files to defeat same-UID symlink tampering.
- Validate that the instance root resolves under the user's home directory (warn on non-home placement; sharing an instance directory under `/tmp` is unusual and worth surfacing).

Advisory `flock` can be bypassed by same-UID processes. The design accepts this within the trust ceiling; malformed concurrent reads fail closed as authorization errors rather than granting access.

### Binary Resolution (Supply Chain)

The daemon spawns `claude -p` for every worker. The binary is resolved **once at daemon startup** via `exec.LookPath("claude")` (or whatever `NIWA_WORKER_SPAWN_COMMAND` points at, when set). The resolved absolute path, its owning UID, and its mode bits are logged at INFO to `.niwa/daemon.log` on startup, and the absolute path is reused verbatim for every subsequent spawn. The daemon does **not** re-resolve per-spawn; a `PATH` change during the daemon's lifetime has no effect on which binary runs.

This matters because a naive per-spawn resolution would walk the user's current `PATH` on every spawn, which can be shifted by the same shell-init hooks that projects commonly install (`direnv`, `mise`, `asdf`, `./bin/` prepends). Without resolve-once, a repo's `.envrc` that prepends `./bin` to `PATH` would cause `claude` to resolve to a repo-local binary — and the daemon would then spawn that binary for workers of *every* role, not just the one whose `.envrc` was trusted. Resolve-once contains the blast radius of a poisoned `PATH` to the state of the daemon's own process env at startup.

`NIWA_WORKER_SPAWN_COMMAND` overrides the default binary. It is **env-var only**: the config parser for `workspace.toml` rejects any key named `NIWA_WORKER_SPAWN_COMMAND` (at any nesting) with a parse error, so a malicious clone cannot turn a poisoned `workspace.toml` into arbitrary code execution at apply time. A unit test verifies this rejection; the test is a regression gate, not documentation-only. Code executed via the override runs at the user's UID with the same privileges the daemon already has — the trust boundary is "the user's own shell environment."

**Operational guidance:** do not set `NIWA_WORKER_SPAWN_COMMAND` in shell profiles shared across repositories. The daemon's startup log line is the audit trail for what will actually run.

### Data Exposure

`envelope.json`, `state.json`, `transitions.log`, `sessions.json`, and inbox files may contain sensitive LLM output (results, code, progress bodies). All are mode 0600 and readable only by the owning user. Concrete v1 defenses:

- **`transitions.log` logs only the truncated progress `summary` field, not the full progress `body`.** The summary is already capped at 200 characters per R23; the full body stays in `state.json.last_progress.body`, which is overwritten per progress event (not accumulated). Terminal bodies (`result` for completed, `reason` for abandoned) are still logged, because they are needed for `niwa_query_task` after terminal transition. This mitigation does not eliminate body retention entirely — completed tasks still have their result in both `state.json` and `transitions.log` — but it bounds non-terminal accumulation to summary-only. See the corresponding PRD Known Limitation ("`transitions.log` body retention") for the v2 plan.
- **The daemon log (`.niwa/daemon.log`) logs state transitions, spawn/exit events, spawn-binary resolution, and watchdog decisions at INFO level only.** Bodies, summaries, results, reasons, and message contents are **never** written to `daemon.log`. A DEBUG verbosity is not defined in v1. Implementation must include a regression test that greps `daemon.log` in a full run and asserts it contains no body/result/reason field content.
- `transitions.log` is still not garbage-collected in v1; `.niwa/tasks/<id>/` directories accumulate. See the PRD Known Limitation.
- Exclude `.niwa/` from backups that may be shared or archived to less-protected storage. Backup tools that honor `CACHEDIR.TAG` can be informed via a future sentinel file (out of v1 scope).

### Daemon Compromise

A compromised daemon can exfiltrate every envelope and result in the workspace, forge state transitions, and spawn workers with crafted arguments in any role's repo. Blast radius equals the user's full workspace-level authority. Defense-in-depth applied at implementation time:

- Strict schema validation (`v=1`, enumerated state values, UUID-shaped IDs) on every `state.json`, `envelope.json`, and log read, failing closed on any anomaly. This hardens the reconciliation-on-startup path against poisoned instance roots.
- Regex-validate task IDs and message IDs at every file-path construction point to defeat path-traversal through fsnotify event names.
- Task IDs are UUIDv4 (or `crypto/rand`-derived) to prevent pre-computation by a same-UID attacker attempting to pre-seed a `.niwa/tasks/<id>/` directory.
- Operational guidance: do not run `niwa apply` against instance roots of unknown provenance. If `.niwa/` was received from another machine (sync, tarball), `niwa destroy` and re-create.

### Denial of Service

No per-caller rate limits in v1. A buggy or hostile LLM can flood a target inbox, exhaust disk, or stall the daemon with rename churn. Mitigations are v2: rate limits, per-role queue caps, disk-space watermarks. For v1, the restart cap (3) and stalled-progress watchdog (15 min default) bound the blast radius of a single runaway worker.

**fsnotify queue overflow.** Linux's per-watch `inotify` queue is bounded by `fs.inotify.max_queued_events` (default 16384). A same-UID process that bulk-creates files in `.niwa/roles/*/inbox/` can overflow the queue, triggering `IN_Q_OVERFLOW`. The daemon handles overflow by: (a) detecting the overflow event; (b) running the catch-up inbox scan to re-enumerate files; (c) processing any found envelopes through the normal claim path. The scan is the durable backstop that fsnotify depends on in the steady state; the design does not need a separate recovery path.

### Containerized and Shared-UID Environments

The design's trust model assumes a conventional single-user interactive Unix environment. The following deployment contexts weaken the `.niwa/` perimeter and are treated as out-of-scope for v1's security guarantees:

- **Dev containers, CI runners, and shared build hosts** where multiple logical actors share a single UID. A niwa instance inside a container shares its UID with every other process in that container, collapsing the same-UID trust boundary to "every container process is trusted." Users running niwa inside such environments should treat the entire container as one logical user.
- **User namespaces** (`CLONE_NEWUSER` / rootless containers) let unprivileged users create sub-UIDs; a process inside a user namespace may appear same-UID from outside while being isolated inside. Interaction with the mesh's filesystem trust is undefined.
- **WSL2 interop.** Windows processes accessing `.niwa/` via WSL's `/mnt/c` path mapping may bypass Linux UID semantics. Niwa's guarantees apply only to Linux-native access paths.

Users operating in these environments should either avoid channel delegation or treat every process sharing the niwa UID as trusted.

### Defense-in-Depth Implementation Commitments

These are not negotiable design choices — they are required implementation properties enforced by tests:

- **`O_NOFOLLOW` on opens.** `state.json`, `envelope.json`, `.lock`, and inbox files are opened with `O_NOFOLLOW` (`syscall.O_NOFOLLOW` on Linux/macOS) to defeat same-UID symlink tampering. A regression test creates a symlink in place of `state.json` and asserts the authorizer fails closed.
- **Strict schema validation on all reads.** Every `state.json` and `envelope.json` read validates `v == 1`, state value against the enumerated set, UUID-shaped IDs (via regex), and size bounds. Fail-closed on any anomaly returns `ErrCorruptedState` → auth-path callers surface `NOT_TASK_PARTY`; admin-path callers return an internal error. A regression test asserts a malformed JSON file in `.niwa/tasks/<id>/` does not panic the daemon or the MCP server.
- **Path validation at every file-path construction.** Task IDs and message IDs are regex-validated against a UUIDv4 shape before being concatenated into any path, defeating fsnotify-event-based path-traversal attempts.
- **`NIWA_WORKER_SPAWN_COMMAND` rejected from `workspace.toml`.** A regression test loads a `workspace.toml` containing a `NIWA_WORKER_SPAWN_COMMAND` key and asserts the parser errors. Documentation alone is not the defense.
- **Worker-side permission to `destroy`.** `niwa destroy` sends SIGKILL directly to worker PGIDs before handling the daemon; the grace period applies only to the daemon (for clean state flush). This minimizes the attack window in which a compromised worker, under `acceptEdits`, can write exfiltration data during teardown. The PRD's `NIWA_DESTROY_GRACE_SECONDS` applies to the daemon phase, not to workers.

### Known Trust-Ceiling Items (v2 candidates)

- Per-agent cryptographic identity, signed envelopes, message encryption.
- In-flight task cancellation (currently only queued tasks can be cancelled).
- `niwa mesh gc` for task directory retention.
- Per-caller rate limiting on `niwa_delegate` / `niwa_send_message`.
- Structural verification that `niwa_finish_task(completed)` is accompanied by genuine work (heuristic, e.g., presence of at least one `niwa_report_progress` event).
