---
status: Proposed
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
  (To be filled after Phase 4.)
rationale: |
  (To be filled after Phase 4.)
---

# DESIGN: Cross-Session Communication

## Status

Proposed

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

*(To be populated in Phases 2–3 via the decision protocol.)*

## Decision Outcome

*(To be populated in Phase 4.)*

## Solution Architecture

*(To be populated in Phase 4.)*

## Implementation Approach

*(To be populated in Phase 4.)*

## Security Considerations

*(To be populated in Phase 5.)*

## Consequences

*(To be populated in Phase 4.)*
