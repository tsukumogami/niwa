---
design_doc: docs/designs/current/DESIGN-niwa-mesh-reliability.md
input_type: design
decomposition_strategy: horizontal
strategy_rationale: "The design is a coordinated reliability pass over an existing system, with phases 1, 2, and 4 independent and phases 3-6 sequenced by logical (not e2e) dependencies. Walking skeleton doesn't fit because there's no e2e flow being introduced — the mesh already exists; this design fixes specific layers."
confirmed_by_user: false
issue_count: 8
execution_mode: single-pr
---

# Plan Decomposition: niwa-mesh-reliability

## Strategy: Horizontal

The design is a coordinated reliability pass with six implementation
phases that map naturally to atomic issues. Phases 1, 2, and 4 are
independent and could ship in parallel. Phase 3 unblocks Phase 5
(logical dependency: required_skills gate is meaningful once
workers actually inherit the workspace skill set). Phase 4 unblocks
the redelegate primitive's `taskstore_lost` source case. Phase 6
(docs) lands last so the niwa-mesh skill text reflects the merged
runtime.

Each phase splits into 1-2 issues based on whether the deliverables
share a tight code path or address distinct user-facing surfaces.

## Issue Outlines

### Issue 1: fix(mesh): route coordinator-targeted role lookups to main instance

- **Type**: standard
- **Complexity**: testable
- **Goal**: Unblock `niwa_ask(to="coordinator")` and
  `niwa_send_message(to="coordinator")` from session workers by
  routing role-existence checks and inbox writes to the main
  instance for coordinator targets, and expand
  `maybeRegisterCoordinator` to fire on `niwa_delegate` and
  `niwa_query_task`.
- **Section**: Decision 4 (Coordinator role visibility) and
  Solution Architecture > Components > `internal/mcp/server.go`.
- **Closes**: #92, #109.
- **Milestone**: niwa mesh reliability
- **Dependencies**: None.

### Issue 2: feat(daemon): return typed spawn-timeout error from EnsureDaemonRunning

- **Type**: standard
- **Complexity**: testable
- **Goal**: Make synchronous spawn failures observable: change
  `EnsureDaemonRunning`'s 500 ms timeout from a silent nil-return
  to a typed `ErrDaemonSpawnTimeout`; have `handleCreateSession`
  roll back the worktree, branch, and session-state file on that
  error class.
- **Section**: Decision Driver "Surgical changes that reuse
  existing primitives" + Phase 2 deliverables.
- **Closes**: #110.
- **Milestone**: niwa mesh reliability
- **Dependencies**: None.

### Issue 3: feat(mesh): expose daemon liveness on niwa_list_sessions

- **Type**: standard
- **Complexity**: testable
- **Goal**: Add a computed `daemon: {alive, pid, started_at}`
  sub-object to each `niwa_list_sessions` row via a wrapper
  response struct (preserves single-writer on the persisted
  `Status` field). Probe via `<worktreePath>/.niwa/daemon.pid` +
  `mcp.IsPIDAlive`.
- **Section**: Phase 2 deliverables; Solution Architecture > Key
  Interfaces > Extended `niwa_list_sessions` shape.
- **Closes**: #111. Defers the broader observability fields
  (`last_claim_at`, `last_progress_at`, `watcher_count`) to #116.
- **Milestone**: niwa mesh reliability
- **Dependencies**: None.

### Issue 4: feat(mesh): inherit workspace Claude config in spawned workers

- **Type**: standard
- **Complexity**: testable
- **Goal**: Append
  `--add-dir <workspaceRoot> --add-dir <repoPath> --setting-sources user,project,local`
  to every `claude -p` spawn in `spawnWorker`, where
  `<workspaceRoot> = s.taskStoreRootDir()` and
  `<repoPath> = resolveRoleCWD(s.taskStoreRootDir(), evt.role)`.
  Remove the per-repo niwa-mesh skill write loop in
  `InstallChannelInfrastructure`. Verify worker config inheritance
  via the named-skill availability checklist (niwa-mesh,
  representative `shirabe:*`, representative `tsukumogami:*`,
  user-level skills) and the symmetry test
  (main-instance vs session worker produce equivalent skill
  output).
- **Section**: Decision 1 (Worker Claude-config inheritance
  contract); Phase 3 deliverables.
- **Closes**: #108. Resolves #97 by elimination.
- **Milestone**: niwa mesh reliability
- **Dependencies**: None.

### Issue 5: feat(mesh): transition taskstore-lost tasks to abandoned

- **Type**: standard
- **Complexity**: testable
- **Goal**: When `handleInboxEvent` detects a `task.delegate`
  envelope whose state.json is missing, transition the task to
  `state="abandoned"` with `reason="taskstore_lost"` instead of
  silent quarantine. Two sub-cases share the per-task flock:
  (a) state.json missing entirely — bootstrap via new
  `mcp.WriteAbandonedTaskStub(taskDir, reason)` helper;
  (b) state.json present at `state=queued` — use existing
  `mcp.UpdateState`. Add an early state guard to
  `niwa_cancel_task` to remove the `{too_late, queued}`
  contradiction.
- **Section**: Decision 2 (Dangling task lifecycle shape); Phase 4
  deliverables.
- **Closes**: #112.
- **Milestone**: niwa mesh reliability
- **Dependencies**: None.

### Issue 6: feat(mesh): add required_skills queue-time gate

- **Type**: standard
- **Complexity**: testable
- **Goal**: Insert a body peek in `handleDelegate` between the
  `UNKNOWN_ROLE` check and `createTaskEnvelope` that reads
  `body.required_skills: string[]`, intersects with the workspace
  skill manifest (workspace `.claude/skills/` plus resolved
  enabledPlugins), and returns
  `errResultCode("MISSING_SKILLS", {missing, available})`
  synchronously when any required skill is absent. The same gate
  fires uniformly on `read_only=true` and session-routed paths.
- **Section**: Decision 3 (`required_skills` placement); Phase 5
  deliverables.
- **Closes**: #113.
- **Milestone**: niwa mesh reliability
- **Dependencies**: Issue 4 (manifest is meaningful once workers
  inherit the workspace skill set).

### Issue 7: feat(mesh): add niwa_redelegate primitive

- **Type**: standard
- **Complexity**: testable
- **Goal**: Add a new MCP tool `niwa_redelegate(source_task_id,
  to?, session_id?, read_only?, body_overrides?, mode?,
  expires_at?)`. The handler reads the source via
  `ReadState(taskDirPath(...))`, allows any source state
  (queued, running, completed, abandoned, cancelled), merges body
  overrides, runs the same `required_skills` gate, and creates a
  new task with `from = caller`, `redelegated_from = source_task_id`.
  Response includes `source_state_at_fork` so callers can
  distinguish recovery flows from active forks. Authorization is
  `kindDelegator` on the source task. Source body lost (envelope
  missing) returns a structured error so the caller can supply
  `body_overrides`.
- **Section**: Decision Outcome + Phase 5 deliverables; Solution
  Architecture > Key Interfaces > niwa_redelegate.
- **Closes**: #114.
- **Milestone**: niwa mesh reliability
- **Dependencies**: Issue 4 (manifest source for the gate),
  Issue 5 (`taskstore_lost` abandoned state for the dangling-source
  redelegate case).

### Issue 8: docs(mesh): rewrite niwa-mesh skill and sessions guide

- **Type**: standard
- **Complexity**: simple
- **Goal**: Update `buildSkillContent`
  (`internal/workspace/channels.go:682-833`) to: remove dead
  `question.ask`/`question.answer`/`status.update` vocabulary; add
  `task.delegate`/`task.ask` to the message vocabulary; document
  `taskstore_lost` recovery via `niwa_redelegate`; document the
  worker config inheritance contract; correct the "Worker asks
  coordinator" pattern to reflect the new routing path. Update
  `docs/guides/sessions.md` with the new `daemon` sub-object,
  `taskstore_lost` recovery, and the inheritance contract. The
  skill rewrite ships only after every preceding issue lands so
  the documented contract matches the merged runtime.
- **Section**: Phase 6 deliverables; cross-references the contract
  audit research file and the design's "Source Issues" section.
- **Closes**: completes the chain (no new issue closure but
  required for the niwa-mesh skill text to match runtime).
- **Milestone**: niwa mesh reliability
- **Dependencies**: Issues 1, 2, 3, 4, 5, 6, 7.

## Issue ordering

Implementation sequence within the single PR:

1. Issue 1 (coordinator routing) — independent
2. Issue 2 (typed timeout) — independent
3. Issue 3 (daemon liveness) — independent
4. Issue 4 (worker config inheritance) — independent; unblocks 6 + 7
5. Issue 5 (taskstore_lost transition) — independent; unblocks 7's dangling-source case
6. Issue 6 (required_skills gate) — after Issue 4
7. Issue 7 (niwa_redelegate) — after Issues 4 + 5
8. Issue 8 (skill text + guides) — last, after all others

Issues 1-5 are mutually independent and can be implemented in any
order. Issues 6, 7, 8 form the dependent tail.

## Execution mode

`single-pr` (per `--single-pr` flag in plan invocation). Phase 4
generates structured outlines (not full issue bodies). Phase 7
writes the PLAN doc with the Issue Outlines section populated; no
GitHub issues or milestone created. PLAN status remains `Draft`.

The implementation work happens on the existing branch
`docs/niwa-mesh-reliability` and lands in PR #115 (or a successor
PR off this branch) — the user explicitly asked for single-pr in
this same branch.
