---
schema: plan/v1
status: Draft
execution_mode: single-pr
upstream: docs/designs/current/DESIGN-niwa-mesh-reliability.md
milestone: "niwa mesh reliability"
issue_count: 8
---

# PLAN: niwa mesh reliability

## Status

Draft

## Scope Summary

Replace four filesystem-side-channel mechanisms in the niwa mesh
subsystem with explicit API contracts, closing the cluster of nine
issues filed since #92 (#92, #97, #108, #109, #110, #111, #112, #113,
#114) in one coordinated reliability pass. Worker Claude config
inheritance, coordinator routing, daemon health, task lifecycle
truthfulness, and a new redelegate primitive land together with the
niwa-mesh skill text rewrite that brings the documented contract
back into lockstep with the runtime.

## Decomposition Strategy

**Horizontal decomposition.** The design is a coordinated
reliability pass over an existing system, with phases 1, 2, and 4
independent and phases 3-6 sequenced by logical (not e2e)
dependencies. Walking skeleton doesn't fit because there's no e2e
flow being introduced — the mesh already exists; this design fixes
specific layers. The design's six implementation phases map
naturally to atomic issues (1-2 issues per phase based on whether
the deliverables share a tight code path or address distinct
user-facing surfaces).

## Pending Additions (UX expansion)

The user explicitly scoped the implementation to cover the **full
surface** UX (CLI commands, MCP tools, first-time setup flow), not
just the MCP/agent surface this plan currently captures.
Implementation will begin with a multi-agent UX research pass over
the existing CLI surface (`niwa session list/destroy/go`,
`niwa task list/show/cancel`, `niwa mesh list`, `niwa apply`,
`niwa init`) and the runtime error rendering paths. Findings will
be appended to this PLAN doc as additional Issue Outlines (likely
3-5 more issues for CLI polish, error-message review, and
first-run flow) before any implementation issue past Issue 1
begins, so the final plan reflects the actual scope. The eight
issues below are the runtime/MCP/docs spine; the UX additions will
be numbered Issue 9, 10, ... when surfaced.

## Issue Outlines

### Issue 1: fix(mesh): route coordinator-targeted role lookups to main instance

**Goal**: Unblock `niwa_ask(to="coordinator")` and
`niwa_send_message(to="coordinator")` from session workers by
introducing a `roleRoot(role)` helper that redirects
coordinator-targeted role lookups and inbox writes to the main
instance, and expand coordinator auto-registration to fire on
`niwa_delegate` and `niwa_query_task`.

**Acceptance Criteria**:
- New helper `roleRoot(role string) string` on `*mcp.Server` returns
  `s.mainInstanceRoot` when `role == "coordinator" && s.mainInstanceRoot != ""`,
  else `s.instanceRoot`.
- `isKnownRole`, `sendMessageWithID` inbox path, and `handleAsk`
  `askRoot` selection switch to the helper.
- `maybeRegisterCoordinator` is called from `handleDelegate` and
  `handleQueryTask` (existing `handleCheckMessages` and
  `handleAwaitTask` triggers stay).
- Functional test: a session worker can call
  `niwa_ask(to="coordinator")` and `niwa_send_message(to="coordinator")`
  and receive routing through the existing `task.ask` flow.
- `@critical` Gherkin scenario in `test/functional/features/`
  exercises the worker → live-coordinator path end-to-end.
- Worktree daemon's `watched_roles count=N` log line is unchanged
  (no synthetic `coordinator/` directory created in worktrees).

**Closes**: #92, #109.

**Dependencies**: None.

### Issue 2: feat(daemon): return typed spawn-timeout error from EnsureDaemonRunning

**Goal**: Make synchronous spawn failures observable by returning a
typed `ErrDaemonSpawnTimeout` from `EnsureDaemonRunning`'s 500 ms
wait and rolling back the worktree, branch, and session-state file
in `handleCreateSession` when that error class fires.

**Acceptance Criteria**:
- New error sentinel `ErrDaemonSpawnTimeout` exported from
  `internal/workspace/daemon.go`.
- `EnsureDaemonRunning` returns `ErrDaemonSpawnTimeout` (instead of
  nil) on the 500 ms PID-file poll timeout.
- `handleCreateSession` rolls back worktree, branch, and session
  state on `ErrDaemonSpawnTimeout`; returns `errResult` with
  structured error code `DAEMON_SPAWN_TIMEOUT`.
- Successful spawn paths unchanged.
- Functional tests covering #110's three named sub-cases:
  inotify exhaustion, missing/non-executable target binary,
  daemon-internal PID file write failure.

**Closes**: #110.

**Dependencies**: None.

### Issue 3: feat(mesh): expose daemon liveness on niwa_list_sessions

**Goal**: Add a computed `daemon: {alive, pid, started_at}`
sub-object to each `niwa_list_sessions` row by introducing a
wrapper response struct that embeds the persisted
`SessionLifecycleState` plus the daemon health fields probed via
`<worktreePath>/.niwa/daemon.pid` and `mcp.IsPIDAlive`.

**Acceptance Criteria**:
- `handleListSessions` returns a wrapper response struct with the
  `daemon` sub-object; `SessionLifecycleState` itself is NOT
  modified (preserves single-writer on the persisted file).
- `daemon.alive`, `daemon.pid`, `daemon.started_at` populated from
  `daemon.pid` file + `mcp.IsPIDAlive`.
- When `daemon.pid` is missing or empty, response carries
  `{alive=false, pid=0, started_at=""}`.
- `status` field keeps its lifecycle-marker meaning; doesn't mutate
  on daemon liveness.
- Functional test: a session whose daemon was killed reports
  `daemon.alive=false` while `status=active`.

**Closes**: #111. Defers `last_claim_at`, `last_progress_at`,
`watcher_count` to #116 (`needs-prd`).

**Dependencies**: None.

### Issue 4: feat(mesh): inherit workspace Claude config in spawned workers

**Goal**: Make every worker spawned by niwa inherit the same Claude
Code configuration a user would see by running `claude` directly in
the role's repo, by appending
`--add-dir <workspaceRoot> --add-dir <repoPath> --setting-sources user,project,local`
to every `claude -p` invocation, and remove the per-repo niwa-mesh
`SKILL.md` writes.

**Acceptance Criteria**:
- `spawnWorker` (`mesh_watch.go:982-1009`) appends three argv items:
  `--add-dir <workspaceRoot>`, `--add-dir <repoPath>`,
  `--setting-sources user,project,local`. Both `<workspaceRoot>`
  and `<repoPath>` derived from `s.taskStoreRootDir()` (the latter
  via `resolveRoleCWD(s.taskStoreRootDir(), evt.role)`, deliberately
  different from `cmd.Dir`).
- `InstallChannelInfrastructure` removes the per-repo skill write
  loop; only the instance-root copy at line 341 stays.
- Functional test (named-skill availability checklist): a session
  worker can invoke each of: niwa-mesh; one representative
  `shirabe:*`; one representative `tsukumogami:*`; user-level
  skills.
- Functional test (symmetry): main-instance and session workers
  produce equivalent named-skill output.
- Functional test (hook propagation): a workspace-defined
  `PreToolUse` hook fires inside the worker session.
- Functional test (skill-leak regression): no consumer repo working
  tree contains `.claude/skills/niwa-mesh/SKILL.md` after `niwa apply`.

**Closes**: #108. Resolves #97 by elimination.

**Dependencies**: None.

### Issue 5: feat(mesh): transition taskstore-lost tasks to abandoned

**Goal**: Convert the daemon's `dangling` filesystem quarantine
into a real `state.json` transition — when `handleInboxEvent`
detects a `task.delegate` envelope whose state.json is missing,
transition the task to `state="abandoned"` with
`reason="taskstore_lost"`. Add an early state guard to
`niwa_cancel_task` to remove the `{too_late, queued}` contradiction.

**Acceptance Criteria**:
- New helper `mcp.WriteAbandonedTaskStub(taskDir, reason string) error`
  in `internal/mcp/taskstore.go` (per-task flock'd, creates task
  dir if needed, writes state.json with seeded transition log).
- `handleInboxEvent` calls `WriteAbandonedTaskStub` for the
  state.json-missing sub-case; calls existing `mcp.UpdateState`
  for the state.json-present sub-case.
- `niwa_cancel_task` early state guard removes the
  `{too_late, queued}` contradiction.
- `inbox/dangling/` files continue to be created as forensic
  preservation; not the primary state signal.
- `TestHandleInboxEvent_DanglingEnvelope` extended for both
  sub-cases.
- No new task-state constant added; `validTaskStates` unchanged.

**Closes**: #112.

**Dependencies**: None.

### Issue 6: feat(mesh): add required_skills queue-time gate

**Goal**: Add a queue-time `required_skills` precondition gate to
`niwa_delegate` (and `niwa_redelegate`) that reads
`body.required_skills: string[]`, intersects with the workspace
skill manifest, and returns
`errResultCode("MISSING_SKILLS", {missing, available})`
synchronously when any required skill is absent.

**Acceptance Criteria**:
- `handleDelegate` peeks `body.required_skills` between
  `UNKNOWN_ROLE` check and `createTaskEnvelope`. Empty list = no-op.
- Manifest enumerates `<workspaceRoot>/.claude/skills/` plain skills
  plus resolves enabledPlugins from workspace settings.
- On miss, returns `MISSING_SKILLS` with `{missing, available}`
  body; no task ID allocated.
- Same gate runs in `handleRedelegate` against the merged body.
- Gate fires uniformly on `read_only=true` and session-routed
  paths.
- No top-level field on `delegateArgs`; no schema change to MCP
  wire-level descriptor.
- Functional tests: typo catch, match, omitted, read_only path.

**Closes**: #113.

**Dependencies**: Issue 4 (manifest meaningful only after workers
inherit the workspace skill set).

### Issue 7: feat(mesh): add niwa_redelegate primitive

**Goal**: Add a new MCP tool `niwa_redelegate(source_task_id, ...)`
that re-fires a previously-delegated task body without rewriting
it, accepting any source state and stamping `redelegated_from` on
the new envelope. Response carries `source_state_at_fork` so
callers distinguish recovery flows from active forks.

**Acceptance Criteria**:
- New tool registration in `internal/mcp/server.go`. Schema:
  `source_task_id (required)`, optional `to`, `session_id`,
  `read_only`, `body_overrides`, `mode`, `expires_at`.
- New `handleRedelegate` handler with `kindDelegator` auth on
  source.
- Source state allow-list: any of `{queued, running, completed, abandoned, cancelled}`.
- `from` reset to caller; `redelegated_from` points to source;
  source state unchanged.
- `body_overrides` shallow-merged into source body. Same
  `required_skills` gate runs against merged body.
- `SOURCE_BODY_LOST` error when source envelope.json missing
  (`taskstore_lost` recreate-stub case).
- Response includes `source_state_at_fork` (string).
- New `TaskEnvelope.RedelegatedFrom` field with `omitempty`.
- Functional tests: recovery from `abandoned`, `taskstore_lost`
  with envelope present, `taskstore_lost` envelope missing →
  `SOURCE_BODY_LOST`, active fork from `running`, active fork
  from `queued`, `MISSING_SKILLS` propagation, auth.
- `redelegated_from` chains correctly across multiple
  redelegations.

**Closes**: #114.

**Dependencies**: Issue 4 (manifest source for the gate),
Issue 5 (`taskstore_lost` abandoned state for dangling-source
redelegate).

### Issue 8: docs(mesh): rewrite niwa-mesh skill and sessions guide

**Goal**: Bring the niwa-mesh skill text and `docs/guides/sessions.md`
back into lockstep with the runtime that issues 1-7 deliver.

**Acceptance Criteria**:
- `buildSkillContent` (`internal/workspace/channels.go:682-833`):
  remove `question.ask`/`question.answer`/`status.update`; add
  `task.delegate`/`task.ask` to message vocabulary; replace
  "Worker asks coordinator" pattern; replace spawn-fallback prose
  with `no_live_session` contract; add `taskstore_lost`
  recovery via `niwa_redelegate` paragraph; add worker config
  inheritance contract paragraph; add `niwa_redelegate` API doc
  including `source_state_at_fork` and `MISSING_SKILLS` gate.
- `docs/guides/sessions.md`: new section on `daemon` sub-object;
  new section on `DAEMON_SPAWN_TIMEOUT` synchronous failure
  contract; new section on `taskstore_lost` recovery; new section
  on worker config inheritance contract.
- After `niwa apply`, regenerated SKILL.md contains all contract
  changes.
- No reference to `dangling` as a user-visible state remains.
- No reference to `question.ask`/`question.answer`/`status.update`
  remains.

**Closes**: completes the documentation tail; no new issue closure.

**Dependencies**: Issues 1, 2, 3, 4, 5, 6, 7.

## Dependency Graph

```mermaid
graph TD
    I1["Issue 1: coordinator routing repair"]
    I2["Issue 2: typed spawn-timeout"]
    I3["Issue 3: daemon liveness sub-object"]
    I4["Issue 4: worker Claude config inheritance"]
    I5["Issue 5: taskstore_lost transition"]
    I6["Issue 6: required_skills gate"]
    I7["Issue 7: niwa_redelegate primitive"]
    I8["Issue 8: skill text + sessions guide"]

    I4 --> I6
    I4 --> I7
    I5 --> I7
    I1 --> I8
    I2 --> I8
    I3 --> I8
    I4 --> I8
    I5 --> I8
    I6 --> I8
    I7 --> I8

    classDef done fill:#90EE90
    classDef ready fill:#87CEEB
    classDef blocked fill:#FFFFE0
    classDef needsDesign fill:#DDA0DD
    classDef needsPrd fill:#FFD700
    classDef needsSpike fill:#FFA07A
    classDef needsDecision fill:#F0E68C
    classDef tracksDesign fill:#E6E6FA
    classDef tracksPlan fill:#F5DEB3

    class I1,I2,I3,I4,I5 ready
    class I6,I7,I8 blocked
```

**Legend**: Blue = ready (no dependencies), Yellow = blocked
(waiting on a predecessor), Green = done.

## Implementation Sequence

### Critical path

Three equal-length critical paths of 3 issues:

1. Issue 4 → Issue 6 → Issue 8
2. Issue 4 → Issue 7 → Issue 8
3. Issue 5 → Issue 7 → Issue 8

Length: 3. Issues 1, 2, 3 land alongside but do not extend the
critical path.

### Parallelization opportunities

- **Immediate (5 issues)**: Issues 1, 2, 3, 4, 5 — all
  independent. In single-pr mode they still land on one branch but
  the implementation can pick them up in any order.
- **After Issue 4**: Issue 6 unblocks.
- **After Issues 4 + 5**: Issue 7 unblocks.
- **After all of 1-7**: Issue 8 (docs) lands.

### Recommended commit order

1. Issue 1 (coordinator routing repair)
2. Issue 2 (typed daemon-spawn timeout)
3. Issue 3 (daemon liveness sub-object)
4. Issue 4 (worker Claude config inheritance) — the core
   architectural shift
5. Issue 5 (taskstore_lost transition) — independent of 1-4
6. Issue 6 (required_skills gate) — after 4
7. Issue 7 (niwa_redelegate) — after 4 + 5
8. Issue 8 (skill text + sessions guide) — last, after all
   runtime issues

UX research findings (per the user's full-surface scope) will be
folded in as additional issues numbered Issue 9, 10, ... and
slotted into the appropriate position in this sequence based on
their dependencies.
