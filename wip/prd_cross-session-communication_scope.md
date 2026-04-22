# /prd Scope: cross-session-communication (revision)

## Problem Statement

Developers use niwa to manage multi-repo workspaces where work often needs to be
delegated across repos from one coordinating Claude session. The current mesh
design assumes all agents were started manually; it provides no way for a
coordinator to dispatch a task to a repo where no Claude has ever run. Users
must open one Claude per role before any delegation, which defeats the
coordination benefit. Additionally, the existing mesh conflates Claude session
lifetime with task lifetime, has no first-class notion of task state, and
gives senders no way to manage a queue of not-yet-consumed tasks.

## Initial Scope

### In Scope

- Topology-aware provisioning: roles enumerated from `workspace.toml` + cloned
  repos at `niwa apply`; per-role task queues created up front
- **Task as a first-class concept** in the tool surface: lifecycle is
  `queued -> running -> completed | abandoned`; completion is explicit
- Session end != task end: niwa auto-restarts up to a cap, then marks
  `abandoned` and notifies the delegator
- Delegator-side capabilities: sync delegate, async delegate, query status,
  await completion later, auto-surfaced progress
- **Sender-side queue control**: inspect, update, cancel
  queued-but-not-yet-consumed tasks
- Peer-to-peer messaging (any agent <-> any agent); delegation is not
  coordinator-only
- Niwa installs a default `niwa-mesh` skill into every agent at apply time;
  defines message types, progress cadence, delegation style, completion
  contract. Users override via their own skill.
- `workspace-context.md` holds only: tool names, role, instance paths. All
  behavioral guidance lives in the skill.

### Out of Scope

- Native Claude Code Channels push (blocked by the known idle-session bug; v1
  stays on `claude -p`)
- Cross-workspace routing, network transport, message encryption
- Live observability during delegations (no `niwa mesh tail`, no dashboard)
- Human-in-the-loop approval gate before niwa spawns a worker
- In-flight task cancellation (only pre-consumption cancellation is in v1)
- Agent-to-agent authentication / message signing

## Research Leads

1. **Skill installation mechanics**: How niwa writes/updates a skill into every
   agent's skill directory idempotently on every `niwa apply`, how precedence
   works when users override, what the skill artifact actually contains.
2. **Claude -p payload & task-conveyance shape**: What gets embedded in the
   spawn prompt vs. what goes through the tool/queue, prompt size limits in
   practice, how structured task metadata (task_id, reply_to) reaches the
   worker through a string prompt.
3. **Task-state durability and restart semantics**: Where task state lives,
   what survives a daemon crash, how "worker exited without completion" is
   detected reliably, what the retry-cap defaults should be.
4. **Queue mutation semantics**: Sender-side tools to inspect/update/cancel
   queued tasks, the race condition between "sender cancels task" and "daemon
   picks up task to spawn worker," what guarantees we give the sender.

## Coverage Notes

- Primary user surface is intentionally narrow: one chat with the coordinator.
  The rest of the mesh is invisible unless an agent chooses to report.
- The existing PRD's typed message vocabulary (`question.ask`, `task.delegate`,
  `review.feedback`, etc.) moves from **niwa-owned requirement** to
  **niwa-mesh-skill-owned default**. Users can redefine.
- Niwa's opinionated surface reduces to: the tool API + the task state machine.
  Everything else is skill.
- This is a **revision** of the existing Accepted PRD. The final artifact
  stays at `docs/prds/PRD-cross-session-communication.md`.

## Resolved Phase 1 Decisions

Captured during scoping conversation with the user:

- **Observability during delegation**: relay through coordinator's chat only; no
  side channel (`niwa mesh tail` etc.) in v1.
- **Clarification interruptions**: prompting concern; routed through the
  coordinator via peer messaging, user interacts only with coordinator.
- **Approval gate before spawning**: none; trust the coordinator.
- **Worker context inheritance**: prompting concern; each delegation's prompt
  is the coordinator's responsibility.
- **Launch mode**: always headless `claude -p`; ephemeral workers.
- **Concurrency within a role**: tasks queue sequentially per role (git conflict
  avoidance). Cross-role parallelism is allowed.
- **Role set**: roles map 1:1 to repos plus a hardcoded `coordinator` role at
  the instance root. No ad-hoc roles in v1.
- **Restart on unexpected exit**: niwa auto-restarts up to a cap, then marks
  `abandoned` and surfaces to delegator.
- **Workspace-context.md vs skill**: niwa ships a default skill installed in
  every agent; `workspace-context.md` holds only tool surface.
