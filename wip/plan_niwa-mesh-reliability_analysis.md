# Plan Analysis: niwa-mesh-reliability

## Source Document

Path: docs/designs/current/DESIGN-niwa-mesh-reliability.md
Status: Accepted
Input Type: design

## Scope Summary

Replace four filesystem-side-channel mechanisms in the niwa mesh
subsystem with explicit API contracts, closing the cluster of nine
issues filed since #92 (#92, #97, #108, #109, #110, #111, #112,
#113, #114) in one coordinated reliability pass. Worker Claude
config inheritance, coordinator routing, daemon health, task
lifecycle truthfulness, and a new redelegate primitive land
together with the niwa-mesh skill text rewrite that brings the
documented contract back into lockstep with the runtime.

## Components Identified

- **`internal/cli/mesh_watch.go`** — daemon and worker spawn:
  `spawnWorker` adds three new argv items
  (`--add-dir <workspaceRoot>`, `--add-dir <repoPath>`,
  `--setting-sources user,project,local`); `handleInboxEvent`
  writes state.json transition for `taskstore_lost` envelopes.
- **`internal/workspace/daemon.go`** — `EnsureDaemonRunning`
  returns typed `ErrDaemonSpawnTimeout` on the 500 ms timeout.
- **`internal/workspace/channels.go`** —
  `InstallChannelInfrastructure` removes the per-repo
  niwa-mesh skill write loop; `buildSkillContent` rewrite to
  align skill text with runtime (Phase 6).
- **`internal/mcp/server.go`** — new `roleRoot(role)` helper;
  `isKnownRole`, `sendMessageWithID`, `handleAsk` switch to it;
  new `niwa_redelegate` tool registration.
- **`internal/mcp/handlers_task.go`** — `handleDelegate`
  gains `required_skills` body peek + `MISSING_SKILLS` error
  code; new `handleRedelegate` handler;
  `maybeRegisterCoordinator` calls added to `handleDelegate`
  and `handleQueryTask`.
- **`internal/mcp/handlers_session.go`** —
  `handleCreateSession` rolls back on `ErrDaemonSpawnTimeout`;
  `handleListSessions` returns wrapper struct with computed
  `daemon: {alive, pid, started_at}` sub-object.
- **`internal/mcp/taskstore.go`** — new
  `WriteAbandonedTaskStub(taskDir, reason)` helper for the
  `taskstore_lost` bootstrap path; existing `UpdateState`
  reused for the present-state.json sub-case.
- **`internal/mcp/types.go`** — `TaskEnvelope.RedelegatedFrom`
  field added (omitempty).
- **`docs/guides/sessions.md`** — new sections on the
  `daemon` sub-object, `taskstore_lost` recovery via
  `niwa_redelegate`, and the worker config inheritance
  contract.

## Implementation Phases (from design)

### Phase 1: Coordinator routing repair

Closes #92 and #109.

Deliverables:
- New `roleRoot(role string) string` helper on `Server`.
- `isKnownRole`, `sendMessageWithID` inbox path, `handleAsk`
  `askRoot` switched to use `roleRoot`.
- `maybeRegisterCoordinator` called from `handleDelegate` and
  `handleQueryTask` (existing `handleCheckMessages` and
  `handleAwaitTask` triggers stay).
- Functional test: a worker session can `niwa_ask(to="coordinator")`
  and reach a live coordinator via the existing `task.ask` flow.
  Same for `niwa_send_message`.
- `@critical` Gherkin scenario in `test/functional/features/`
  per the niwa testing convention.

Independent of Phases 2-6.

### Phase 2: Daemon health propagation

Closes #110 and #111.

Deliverables:
- `EnsureDaemonRunning` returns typed `ErrDaemonSpawnTimeout`
  on the 500 ms timeout.
- `handleCreateSession` rolls back worktree, branch, state on
  that error class; returns `errResult`.
- `handleListSessions` returns a wrapper response struct
  embedding the persisted `SessionLifecycleState` plus a
  computed `daemon: {alive, pid, started_at}` sub-object.
  Probes via `<worktreePath>/.niwa/daemon.pid` +
  `mcp.IsPIDAlive`. NOT a transient field on
  `SessionLifecycleState` — preserves single-writer.
- `docs/guides/sessions.md` updated with both surfaces.
- Functional tests covering #110's three named sub-cases:
  inotify exhaustion, missing/non-executable target binary,
  daemon-internal PID file write failure.

Independent of Phase 1.

### Phase 3: Worker Claude-config inheritance contract

Closes #108. Resolves #97 by elimination.

Deliverables:
- `spawnWorker` (`internal/cli/mesh_watch.go:982-1009`) appends
  `--add-dir <workspaceRoot>`, `--add-dir <repoPath>`,
  `--setting-sources user,project,local` to argv. Both values
  computed from `s.taskStoreRootDir()` (workspace root for
  both daemon types); `<repoPath>` =
  `resolveRoleCWD(s.taskStoreRootDir(), evt.role)`.
- `InstallChannelInfrastructure` removes the per-repo skill
  write loop. Instance-root copy stays.
- Functional tests:
  1. Named-skill availability checklist (niwa-mesh,
     representative `shirabe:*`, representative
     `tsukumogami:*`, user-level skills) on a session worker.
  2. Symmetry test: main-instance and session workers produce
     equivalent named-skill output.
  3. Hook propagation test: a workspace-defined `PreToolUse`
     hook fires inside the worker session.
  4. Skill-leak regression test: no consumer repo working tree
     contains `.claude/skills/niwa-mesh/SKILL.md` after `niwa
     apply`.

Phase 3 unblocks Phase 5's `required_skills` gate.

### Phase 4: Task lifecycle truthfulness

Closes #112.

Deliverables:
- `handleInboxEvent` writes state.json transition to
  `abandoned` with `reason="taskstore_lost"`. Two sub-cases:
  - state.json missing entirely: bootstrap stub via new
    `mcp.WriteAbandonedTaskStub(taskDir, reason)` helper
    (takes per-task flock, creates task dir if needed,
    writes state.json + seeded transition log).
  - state.json present at `state=queued`: transition via
    existing `mcp.UpdateState` flock'd writer.
- `niwa_cancel_task` early state guard added to remove the
  `{too_late, queued}` contradiction.
- Test fixture update: `TestHandleInboxEvent_DanglingEnvelope`
  (`mesh_watch_test.go:743-784`) verifies state.json
  transition for both sub-cases.

Independent of Phase 3.

### Phase 5: Coordinator ergonomics

Closes #113 and #114. Lower priority than Phases 1-4 (per
Decision 3 framing).

Deliverables:
- `niwa_redelegate` MCP tool registration; `handleRedelegate`
  handler that reads source via
  `ReadState(taskDirPath(...))`, validates source state,
  merges body overrides, runs `required_skills` gate, and
  re-enters `createTaskEnvelope` with `from` reset to caller
  and `redelegated_from` on the new envelope.
- `TaskEnvelope.RedelegatedFrom string \`json:"redelegated_from,omitempty"\``.
- `required_skills` body peek and `MISSING_SKILLS` error code
  in `handleDelegate` and `handleRedelegate`. Manifest
  enumerates `<workspaceRoot>/.claude/skills/` and resolves
  enabledPlugins from the workspace settings. Gate fires
  uniformly on `read_only=true` and session-routed paths.
- Response shape includes `source_state_at_fork` so callers
  distinguish recovery flows from active forks.
- Functional tests: redelegate from `abandoned`,
  `taskstore_lost` (envelope.json present),
  `taskstore_lost` (envelope.json missing → `SOURCE_BODY_LOST`),
  `running` source (active fork, parent continues),
  `queued` source (active fork), and `MISSING_SKILLS` typo
  catch.

Depends on Phase 3 (manifest source-of-truth) and Phase 4
(`abandoned` source state for redelegate from dangling tasks).

### Phase 6: Skill text and guides update

Deliverables:
- `buildSkillContent` rewrite per the contract audit:
  - Remove dead `question.ask`, `question.answer`,
    `status.update` vocabulary.
  - Add `task.delegate`, `task.ask` to message vocabulary.
  - Document `taskstore_lost` recovery via `niwa_redelegate`.
  - Document worker config inheritance contract.
  - Fix the "Worker asks coordinator" pattern to reflect the
    new routing path.
- `docs/guides/sessions.md` update for the new `daemon`
  sub-object, `taskstore_lost` recovery, and the worker
  config inheritance contract.

Depends on Phases 1-5 landing so the skill text reflects the
merged runtime.

## Success Metrics

From the design's Consequences > Positive section:

- niwa-mesh skill becomes truthful again — workers taking
  documented patterns literally succeed instead of being
  silently ignored.
- API responses become structurally consistent for
  dangling-class tasks; no more `{too_late, queued}`
  contradiction.
- Skill no longer leaks into consumer PRs; PR diffs reflect
  only the work product.
- Coordinator escalation works: workers can ask the
  coordinator instead of abandoning silently.
- Spawn failures visible synchronously; coordinators no
  longer queue work onto dead sessions.
- `niwa_redelegate` makes recovery cheap.
- Symmetric worker spawn paths.
- Future per-spawn customization path unblocked.

Acceptance gates per phase are functional/integration tests
named in the deliverables above.

## External Dependencies

- **PR #93** (already merged): wired live-coordinator routing
  in `handleAsk`. Phase 1 completes the chain by fixing the
  `isKnownRole` precondition.
- **Issue #116** (`needs-prd`, deferred from this design):
  fleet observability extension to `niwa_list_sessions`
  daemon sub-object (`last_claim_at`, `last_progress_at`,
  `watcher_count`). Out of scope for this plan.
- **Existing primitives reused**: `daemon.pid` write contract,
  `mcp.IsPIDAlive`, `lookupLiveCoordinator`,
  `mcp.UpdateState`, `TaskStateAbandoned`,
  `lookupLiveCoordinator`, `kindDelegator` auth pattern.
- **Claude Code argv flags** (verified in design's
  Verification Notes): `--add-dir`, `--setting-sources`. No
  Claude Code-side changes required.
