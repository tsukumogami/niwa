---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/current/DESIGN-ephemeral-session-instances.md
milestone: "Ephemeral idle-reap fix"
issue_count: 5
---

# PLAN: ephemeral instances reaped on idle/completion instead of only on delete

## Status

Active

This PLAN decomposes the bugfix described by the DESIGN's revised Decision 6
(delete-only teardown, entry-present liveness). The design, guide, spike, PRD, and
BRIEF are already updated in the same PR that introduces this PLAN; the issues
below are the **code and test** changes, to be executed in this worktree
(`shirabe:execute`) after the human approves the scoping PR. No code change lands in
the scoping PR.

## Scope Summary

Make a dispatched background session's ephemeral niwa instance survive completion,
idle, and suspension, and be reclaimed **only when the developer deletes the
session** (the job entry disappears). Two teardown paths currently misread
"finished a task / went idle" as "gone": the reaper's `sessionLive()` and the
`SessionEnd` hook. The fix keys liveness on **job-entry presence alone** and turns
the `SessionEnd` teardown into a no-op, leaving the reaper as the single teardown
path. SessionStart provisioning and the unrelated dispatch backstop are untouched.

## Decomposition Strategy

**Horizontal.** This is a behavior-narrowing bugfix across three small, loosely
coupled code surfaces (the reaper's liveness rule, the SessionEnd hook handler, and
the root materializer) plus their tests. There is no new end-to-end flow to skeleton
— the flow already exists and ships; each issue tightens one surface and inverts the
tests that asserted the old behavior. The issues are sequenced so the two behavioral
changes (liveness rule, SessionEnd no-op) land before the tests that assert them,
and the functional regression guard lands last.

The change is intentionally subtractive: it removes three liveness conditions, one
hook entry, and the now-unused symbols they relied on. The risk is in the *removal*
(leaving a dangling reference or breaking an unrelated reader), so each issue names
the exact symbols it removes and the readers that must stay (`template`, `cwd`,
`dispatchBackstopTTL`).

## Issue Outlines

### Issue 1: fix(reap): key session liveness on job-entry presence alone

**Goal**: Rewrite `sessionLive()` so a session is live iff its `~/.claude/jobs/<id>/`
entry exists, removing the terminal-state, `firstTerminalAt`, and TTL checks that
misfire on idle-but-resumable sessions.

**Acceptance Criteria**:
- [ ] `sessionLive()` returns `true` when the job entry exists and (when recorded)
  the inner `sessionId` matches, regardless of `state`, `firstTerminalAt`, or
  `updatedAt`; returns `false` only when the entry is gone (or `jobsDir` is empty,
  or the `sessionId` mismatches).
- [ ] The `firstTerminalAt`-non-zero check, the `terminalJobStates` check, and the
  `jobLivenessTTL` staleness check are removed from `sessionLive()`.
- [ ] `jobLivenessTTL` (const) and `terminalJobStates` (map) are removed, and the
  `FirstTerminalAt`, `State`, and `UpdatedAt` fields are removed from the `jobState`
  struct — each confirmed (grep) to have no remaining non-test reader. `Template`
  (SessionStart guard) and `Cwd` (dispatch identity-capture) are retained.
- [ ] The `dispatchBackstopTTL` doc comment in `reap.go` that contrasts itself with
  `jobLivenessTTL` is updated so it no longer references a removed symbol;
  `dispatchBackstopTTL` and `selectBackstopTargets` themselves are unchanged.
- [ ] Doc comments on `sessionLive()` / `jobState` / `readJobState` describe the
  entry-present rule. `go build ./...` and `go vet ./...` pass (no unused symbols).

**Dependencies**: None

**Type**: code
**Files**: `internal/cli/job_state.go`, `internal/cli/reap.go`

### Issue 2: fix(instance-hook): make the SessionEnd branch a no-op

**Goal**: Stop `runInstanceHookEnd()` from destroying instances; `SessionEnd` fires
on idle-suspend/`resume`/`clear`, never uniquely on delete, so it must not tear down.

**Acceptance Criteria**:
- [ ] `runInstanceHookEnd()` no longer resolves a mapping, calls
  `destroyInstanceFunc`, or deletes a mapping; it returns cleanly (it may emit a
  single debug line on stderr, but performs no destructive action). It still exits 0.
- [ ] The `SessionEnd` case stays wired in `runInstanceFromHook`'s dispatch (defense
  in depth for already-materialized workspaces whose `settings.json` still fires it).
- [ ] The function doc comment explains that teardown moved to the reaper
  (delete-only) and why `SessionEnd` is not a delete signal.
- [ ] SessionStart provisioning (`runInstanceHookStart`, the guard) is untouched.
- [ ] `go build ./...` / `go vet ./...` pass (no newly-unused helpers; if a helper is
  orphaned by the change, remove it or note why it stays).

**Dependencies**: None

**Type**: code
**Files**: `internal/cli/instance_from_hook.go`

### Issue 3: fix(materialize): stop installing the SessionEnd hook entry

**Goal**: Materialize only the `SessionStart` hook into the workspace-root
`settings.json`, so a freshly-applied workspace carries no dead `SessionEnd`
teardown hook.

**Acceptance Criteria**:
- [ ] `buildSettingsDoc` no longer writes the `SessionEnd` entry under `hooks`; the
  `SessionStart` entry (command + timeout) is unchanged.
- [ ] The `sessionEndEvent` constant is removed if it becomes unused (grep-confirmed),
  or retained with a comment if another reader exists.
- [ ] Doc comments in `materialize.go` and `root_materializer.go` that describe "the
  SessionStart and SessionEnd hook entries" are updated to SessionStart-only; the
  embedded root `CLAUDE.md` text in `root_materializer.go` that says the instance is
  "torn down on SessionEnd" is corrected to the delete-only/reaper wording.
- [ ] Existing root-materialization tests still pass; a test asserting the rendered
  settings contains a `SessionStart` hook and **no** `SessionEnd` hook is added or
  updated.
- [ ] `go build ./...` / `go vet ./...` pass.

**Dependencies**: None

**Type**: code
**Files**: `internal/workspace/materialize.go`, `internal/workspace/root_materializer.go`

### Issue 4: test(unit): invert liveness/teardown unit tests to the new contract

**Goal**: Update the unit tests that asserted reap-on-terminal / destroy-on-SessionEnd
so they assert keep-while-present / no-destroy, and add the entry-gone-is-dead case.

**Acceptance Criteria**:
- [ ] `job_state_test.go`: a `done` / `firstTerminalAt`-stamped / TTL-stale session
  with its entry present reads as **live**; a session whose entry is gone reads as
  **dead**; a running session stays live. (The old `TestSessionLive_StoppedAndFirstTerminalAt`
  expectations are inverted accordingly.)
- [ ] `instance_from_hook_test.go`: `TestSessionEnd_ResolveAndDestroy` and
  `TestSessionEnd_IgnoresCwd` are updated to assert `destroyInstanceFunc` is **never
  called** and the mapping survives; the no-mapping and non-ephemeral cases still
  pass (their rationale shifts to "SessionEnd never destroys").
- [ ] `reap_test.go`: `TestReap_TerminalJobState_Reclaimed` and
  `TestReap_StaleJobState_Reclaimed` are inverted to "spared" (entry present = live);
  `TestReap_DeadEphemeralOrphan_Reclaimed` (entry gone) and
  `TestReap_LiveIdleEphemeral_Spared` stay; `TestReap_MixedWorkspace_*` and
  `TestSelectReapTargets_DeterministicSelection` are reviewed and their "dead" fixture
  switched to entry-gone. The `writeLiveJobState` helper is adjusted for the removed
  `State`/`UpdatedAt` fields (write entry presence; drop removed fields).
- [ ] `go test ./internal/cli/...` passes.

**Dependencies**: Blocked by <<ISSUE:1>>, Blocked by <<ISSUE:2>>

**Type**: code
**Files**: `internal/cli/job_state_test.go`, `internal/cli/instance_from_hook_test.go`, `internal/cli/reap_test.go`

### Issue 5: test(functional): @critical keep-on-idle + reclaim-on-delete scenarios

**Goal**: Replace the "SessionEnd tears it down" functional scenario with a
keep-while-resumable regression guard, and keep a reap scenario that reclaims only
once the job entry is gone.

**Acceptance Criteria**:
- [ ] The `@critical` scenario "SessionStart provisions an instance and SessionEnd
  tears it down" is rewritten so that after piping `SessionEnd` (and/or leaving the
  job entry present), the instance and mapping **still exist** — `SessionEnd` does
  not reclaim.
- [ ] A `@critical` scenario asserts that a completed/idle session (job entry
  present, e.g. terminal state recorded) is **spared** by `niwa reap`.
- [ ] The `@critical` reap scenario where the session's job entry is removed
  (`aDeadSessionForSession`) still reclaims the instance and deletes the mapping —
  this is the delete proxy and already keys on entry-gone, so it is preserved.
- [ ] Any new Gherkin steps (e.g. "the instance still exists" after SessionEnd, or
  "a completed-but-resumable job state exists") are added to
  `ephemeral_session_steps_test.go`.
- [ ] `make test-functional-critical` passes.

**Dependencies**: Blocked by <<ISSUE:2>>, Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `test/functional/features/ephemeral-session.feature`, `test/functional/ephemeral_session_steps_test.go`

## Dependency Graph

## Implementation Sequence

**Critical path**: Issue 1 (or Issue 2) → Issue 4 → Issue 5.

- **Parallelizable, no dependencies**: Issues 1, 2, and 3 touch disjoint files
  (`job_state.go`/`reap.go`, `instance_from_hook.go`, and
  `materialize.go`/`root_materializer.go` respectively) and can be implemented in any
  order or together.
- **Issue 4 (unit tests)** depends on Issues 1 and 2 because it asserts the new
  liveness rule and the SessionEnd no-op. The `reap_test.go` helper edits also
  depend on Issue 1's struct-field removal.
- **Issue 5 (functional)** depends on Issue 2 (the from-hook SessionEnd no-op the
  scenario pipes) and Issue 1 (the reaper liveness the spared/reclaimed scenarios
  exercise). It lands last as the user-facing `@critical` regression guard.

**Final verification (whole PR)**: `go build ./...`, `go vet ./...`,
`go test ./...`, and `make test-functional-critical` all pass; `grep -rn
'jobLivenessTTL\|terminalJobStates\|FirstTerminalAt' internal/` returns no
non-historical references; the materialized root `settings.json` carries a
`SessionStart` hook and no `SessionEnd` hook.
