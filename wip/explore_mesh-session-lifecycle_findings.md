# Exploration Findings: mesh-session-lifecycle

## Core Question

Should niwa mesh support coordinator-managed session lifecycles, where multiple
sequential tasks share a Claude context, and is git worktree the right physical
anchoring model? This touches two overlapping problems: task-scoped sessions discard
context between delegations (design → plan fails), and the main clone of each repo
gets stranded on a feature branch when work switches repos, because `niwa apply`
only merges clean repos on default branches.

## Round 1

### Key Insights

- **Worktree compatibility is almost free** (Lead 1 + 6): One line changed in
  `snapshotwriter.dotGitExists()` (check existence, not `IsDir()`). Every other
  path-discovery mechanism, the registry, hooks, and flock-based task state machine
  all work unchanged in a worktree layout. The mesh was already designed for concurrent
  parallel workers.

- **Session ID threading is insufficient on its own** (Lead 3): Adding `resume_session_id`
  to `niwa_delegate` is low-cost and would fix the design→plan context loss. But it
  silently breaks under parallel sessions (two workers corrupting the same JSONL), doesn't
  fix the dirty-workspace problem, and creates migration debt when worktrees land.

- **The dirty-workspace behavior is documented, not a bug** (Lead 4): `niwa apply`
  intentionally skips repos on non-default branches or with dirty state. There is zero
  branch-reset logic in the codebase; branch state is only set at initial clone time.
  Worktrees fix this at the source by keeping the main clone permanently on `main`.

- **Each worktree becomes its own niwa instance** (Lead 2): `.niwa/` is instance-scoped.
  A session worktree placed alongside the main clone gets its own `instance.json`, its own
  daemon, its own inbox. This maps onto the existing "one daemon per instance" model with
  no architectural mismatch — only per-session daemon lifecycle overhead.

- **Three new MCP tools are needed** (Lead 5): `niwa_create_session`, `niwa_list_sessions`,
  `niwa_end_session`. Session state lives in `.niwa/sessions/<session_id>/state.json`.
  `niwa_delegate` gains an optional `session_id` parameter. Coordinator liveness
  (`SessionEntry.ClaudeSessionID`) already exists as a field but is never populated.

- **Non-mesh users have the same stranded-branch problem** (Lead 7): `niwa go`/`niwa apply`
  are layout-agnostic and already compatible with worktrees. Universal adoption is
  strategically sound; non-mesh users benefit equally from always-clean main.

- **Five follow-ons are naturally enabled** (Lead 8): session→PR tracking (`pr_url` field),
  session summary for compacted coordinators (`niwa_session_summary`), session handoff,
  session audit history, session-scoped resource cleanup with lifecycle states. Most need
  only extension points reserved now.

### Tensions

- **Quick fix vs. full model**: Session ID threading fixes problem 1 immediately and cheaply.
  The full worktree model is more complex but fixes both problems and doesn't create debt.
  Resolution: skip standalone threading; design the full model.

- **Per-worktree daemon vs. shared daemon**: Per-worktree is cleaner and maps onto the
  existing model. Shared daemon avoids per-session overhead but needs new cross-instance
  coordination. Resolution: per-worktree daemon, with shared sessions registry for routing.

- **Mesh-only vs. universal scope for V1**: Mesh-only is lower risk. Universal is correct
  long-term. Resolution: design universal, implement mesh-first.

### Gaps

- **niwa_ask routing across worktree daemons**: Workers in worktree-bound daemons need to
  find the live coordinator. Currently `lookupLiveCoordinator` reads from the main instance's
  `sessions.json`. Design needs to specify how coordinator registration is shared across
  instance roots.

- **Coordinator crash recovery**: PID-based session registration is fragile across reboots.
  `SessionEntry.ClaudeSessionID` exists but is empty. Orphaned worktrees after coordinator
  crash have no automated recovery path.

- **Non-mesh session management UX**: Non-mesh users don't have a coordinator. If they
  want always-clean main, they need a `niwa session` CLI command or raw git worktree
  commands. Not designed yet.

### Decisions

See `wip/explore_mesh-session-lifecycle_decisions.md` for all decisions made this round.

### User Focus

User confirmed: the design→plan context loss has occurred in practice. Worktrees were
chosen over session ID threading because they solve both problems and avoid migration
debt. User requested auto mode to make architectural decisions from evidence and converge
to a PRD.

## Accumulated Understanding

The niwa mesh today creates a fresh Claude session for every `niwa_delegate` call.
This is the correct behavior for isolated tasks, but breaks multi-step coordinator
workflows where a sequence of delegations (design → plan → implement) should share
context. Two problems are entangled: (1) context loss between sequential task delegations
to the same repo agent, and (2) the main clone of each repo getting stranded on a feature
branch when work switches focus.

The solution is coordinator-managed sessions: a session is a first-class object that
anchors a Claude context to a git worktree for a specific repo. Within a session, multiple
sequential (or parallel) task delegations share the same Claude conversation context. The
coordinator creates sessions, delegates tasks within them, and ends them when the work is
done or pushed to a PR.

This maps cleanly onto the existing niwa instance model: each worktree is its own niwa
instance with its own daemon. The mesh infrastructure (flock-based task state, hook
execution, coordinator routing) requires minimal changes. The primary new surface area
is: three MCP tools (`niwa_create_session`, `niwa_list_sessions`, `niwa_end_session`),
session state schema, and a shared sessions registry for cross-instance coordinator routing.

The model is designed for universal adoption (mesh and non-mesh) but will be implemented
mesh-first. Non-mesh users benefit from the same always-clean-main property and the same
codebase handles both when the session CLI (`niwa session start/end`) is added.

## Decision: Crystallize
