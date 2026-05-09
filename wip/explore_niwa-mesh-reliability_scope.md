# Explore Scope: niwa-mesh-reliability

## Visibility

Public

## Core Question

Nine open issues filed since #92 describe a coherent set of failures in the
niwa mesh subsystem (multi-agent coordination): coordinator routing, worker
plugin inheritance, session and daemon health visibility, the lifecycle of
queued tasks (including the undocumented `dangling` state), and missing
recovery primitives for retry. Are these independent bugfixes, or does the
mesh need a coordinated reliability and observability redesign that ties
them together with a shared model of role registration, session health, and
task state?

## Context

- The mesh is the layer that lets a coordinator (one agent session)
  delegate work to workers (other agent sessions) via the `niwa_*` MCP
  tools and a per-role inbox under `<worktree>/.niwa/roles/<role>/`.
- The contract advertised by the `niwa-mesh` skill (coordinator-to-worker
  delegate, worker-to-coordinator escalation, ask/answer flows, progress
  reporting) is partly aspirational: routing only works coordinator → worker.
  The reverse direction (#92, #109) silently spawns ephemeral processes
  that fabricate replies the coordinator never sees.
- Worker sessions spawn without the workspace's plugin set (#108), so any
  delegation that mandates a workspace skill (`shirabe:*`, `tsukumogami:*`)
  is unrunnable. The natural escape valve — "ask the coordinator for
  guidance" — is the same path that's broken in #109.
- Session creation reports success even when the daemon failed to spawn
  (#110), and `niwa_list_sessions` reports stale `status=active` for dead
  sessions (#111). Together these make silent failure the default.
- When a daemon is missing, queued tasks land in `inbox/dangling/` and
  become unrecoverable through the public API (#112). The coordinator's
  only fix is destroy + redelegate, and there's no `niwa_redelegate`
  primitive to make that cheap (#114). A `required_skills` precondition
  (#113) would block the abandonment cycle at queue time.
- One issue (#97) is a content-leak bug where the niwa-mesh skill itself
  is written into the consumer repo's working tree and shows up in PRs.
  Cousin to the worker-spawn environment shape but distinct in mechanism.

Relevant code surfaces already exist: `internal/mcp/session_registry.go`,
`internal/mcp/session_discovery.go`, `internal/workspace/daemon.go`,
`docs/guides/sessions.md`. The exploration must read these to ground
recommendations in current implementation.

## In Scope

- Coordinator/worker message routing, role registration, and the live-
  session vs. ephemeral-spawn dispatch
- Worker spawn environment: plugin inheritance, skill injection mechanism,
  what the worker's Claude Code config path looks like at startup
- Session lifecycle visibility: synchronous spawn reporting and runtime
  daemon health surfaced through the MCP API
- Task lifecycle: the `dangling` classification, its triggers, its
  surfacing in `niwa_query_task` / `niwa_list_outbound_tasks`, and the
  recovery primitives needed to make it tractable
- Coordinator ergonomics: `required_skills` precondition checks at
  delegation time, `niwa_redelegate` for body reuse
- Documentation alignment: the `niwa-mesh` skill's user-facing contract
  vs. the actual runtime behavior

## Out of Scope

- Cross-workspace mesh (workers in one niwa instance reaching coordinators
  in another) beyond what's needed to fix #109's same-instance case
- Vault, secret, or guardrail subsystems
- Workspace config snapshot / discovery beyond what's needed for plugin
  propagation
- Replacing the inbox-on-disk transport with a different mechanism
  (sockets, gRPC, etc.) — the trade-offs for that are too large for this
  reliability pass

## Research Leads

1. **How does the niwa daemon route inbox messages to live coordinator
   sessions today, and where is the spawn-an-ephemeral-worker fallback
   wired?** (covers #92, #109)
   We need to understand the existing role registration code path
   (`internal/cli/session_register.go`, `internal/mcp/session_registry.go`,
   `internal/mcp/session_discovery.go`) and trace what happens when a
   worker calls `niwa_ask(to='coordinator')`. The fix shape depends on
   whether the coordinator's role/PID is reachable from the worker
   daemon's view of `.niwa/roles/`.

2. **How does the worker spawn path establish the worker's Claude Code
   plugin set, and where (and why) is the niwa-mesh skill file written
   into the worktree?** (covers #108, #97)
   Trace `niwa_create_session` and any `claude -p` or equivalent spawn
   call. Identify whether plugin inheritance happens at all today, what
   environment / config path is passed to Claude Code, and why
   `.claude/skills/niwa-mesh/SKILL.md` ends up in the consumer repo. The
   two issues likely share a common spawn-environment mechanism.

3. **What signals does the daemon spawner have about spawn success and
   runtime liveness, and how could those surface synchronously through
   `niwa_create_session` and `niwa_list_sessions`?** (covers #110, #111)
   Read `internal/workspace/daemon.go` and the session creation API in
   `internal/mcp/`. Identify the daemon's startup log line, PID file write,
   and any heartbeat. Determine the cleanest path to lift that state into
   the MCP response — synchronous wait at create time, plus a
   `daemon` sub-object on list calls.

4. **What is the actual implemented task lifecycle in the daemon, what
   triggers `dangling`, and how is the state visible through the API
   today?** (covers #112)
   Find the watch loop, the dangling-classification logic, and the
   `niwa_query_task` / `niwa_list_outbound_tasks` implementations.
   Identify whether `dangling` is sticky in code or just in observed
   behavior, what user-visible state the API returns, and what shape a
   recovery primitive (`niwa_resurrect_task`) would need.

5. **What does `niwa_delegate` accept today, and what schema and
   precondition-check changes would `required_skills` and
   `niwa_redelegate` introduce?** (covers #113, #114)
   Read the delegate handler, the body schema, and how envelopes are
   written to disk. Identify the cleanest place for a precondition gate
   (which depends on lead 2's plugin-manifest answer) and how
   redelegation reuses an existing on-disk envelope without breaking
   attribution semantics.

6. **What user-facing contract does the `niwa-mesh` skill describe today
   vs. what the runtime actually delivers, and what documentation needs
   to change alongside the fixes?**
   The issues repeatedly cite gaps between the skill text and runtime
   behavior. Inventory the contract surface: ask/answer routing, the task
   state machine (must include `dangling` if it stays), worker
   capabilities (skills available), recovery patterns. This lead frames
   the documentation deltas the design must commit to.
