---
complexity: testable
complexity_rationale: State-machine extension touched by the daemon — adds a daemon-side state.json writer path for the `taskstore_lost` case. Two flock'd writer paths share discipline; flock regressions could corrupt state. New helper `mcp.WriteAbandonedTaskStub` plus an early state guard on `niwa_cancel_task`. Behavior change visible across five MCP tools.
---

## Goal

Convert the daemon's `dangling` filesystem quarantine into a real `state.json` transition: when `handleInboxEvent` detects a `task.delegate` envelope whose state.json is missing, transition the task to `state="abandoned"` with `reason="taskstore_lost"`, so every read API surfaces a structurally consistent terminal state. Add an early state guard to `niwa_cancel_task` to remove the `{too_late, queued}` contradiction.

## Context

Design: `docs/designs/current/DESIGN-niwa-mesh-reliability.md`

`dangling` is not a state today — it's a filesystem quarantine in `<role>/inbox/dangling/` triggered by `handleInboxEvent` (`internal/cli/mesh_watch.go:776-803`) iff a `task.delegate` envelope's `<mainInstance>/.niwa/tasks/<id>/state.json` is missing. The API layer reads only state.json, so `niwa_query_task` and `niwa_list_outbound_tasks` report `state="queued"` for these envelopes; `niwa_cancel_task` returns the contradictory pair `{status:"too_late",current_state:"queued"}` because it only renames `inbox/<id>.json` and treats ENOENT as "daemon already claimed".

The five-state alphabet (`internal/mcp/types.go:171-189`) stays — `dangling` is NOT introduced as a sixth state. Instead the daemon writes the existing `TaskStateAbandoned` with `reason="taskstore_lost"` (a new typed reason consistent with the existing `retry_cap_exceeded` shape).

Two sub-cases share the per-task flock:
- **state.json missing entirely** (the dominant `taskstore_lost` repro per the design's research): `mcp.UpdateState` cannot be used — its first step is `readStateLocked`, which fails when state.json is missing. A new helper `mcp.WriteAbandonedTaskStub(taskDir, reason)` takes the per-task flock, creates the task directory if needed, and writes the bootstrap state.json with `state=abandoned`, `reason="taskstore_lost"`, and a seeded transition log (`unknown -> abandoned`).
- **state.json present at `state=queued`** (rare hand-seeded case): existing `mcp.UpdateState` performs the `queued -> abandoned` transition with the same reason annotation.

The `inbox/dangling/` subdir stays as a forensic side-effect. Operators recover via `niwa_redelegate` (<<ISSUE:7>>), which already lists `abandoned` as an allowed source state.

Closes #112.

## Acceptance Criteria

- [ ] New helper `mcp.WriteAbandonedTaskStub(taskDir, reason string) error` in `internal/mcp/taskstore.go`: takes the per-task flock (same flock semantics as `UpdateState`), creates the task directory if needed via `os.MkdirAll`, writes a stub state.json with `state=abandoned`, `reason=<reason>`, and `state_transitions: [{from: "unknown", to: "abandoned", at: <now>}]`. Does NOT write or fabricate envelope.json.
- [ ] `handleInboxEvent` (`internal/cli/mesh_watch.go:776-803`) is updated:
  - When state.json is missing for a `task.delegate` envelope, call `mcp.WriteAbandonedTaskStub(taskDir, "taskstore_lost")` BEFORE renaming the envelope to `inbox/dangling/`. The rename remains as forensic preservation.
  - When state.json is present (the rare hand-seeded case), call `mcp.UpdateState` to transition `queued -> abandoned` with `reason="taskstore_lost"`.
- [ ] `niwa_cancel_task` (`internal/mcp/handlers_task.go`) gets an early state guard: if state.json shows a terminal state, return `{state, reason}` directly without attempting the inbox rename. Removes the `{status:"too_late",current_state:"queued"}` contradiction surfaced by #112.
- [ ] `niwa_query_task` returns `{state: "abandoned", reason: "taskstore_lost"}` for a previously-dangling envelope (no per-handler change required — state.json already drives this).
- [ ] `niwa_list_outbound_tasks` returns `state="abandoned"` for the same task (no per-handler change required).
- [ ] `niwa_await_task` exits immediately via the existing terminal-state short-circuit at `handlers_task.go:427-430` (no per-handler change required).
- [ ] `niwa_update_task` refuses cleanly via the existing terminal-state guard (no half-mutation).
- [ ] `TestHandleInboxEvent_DanglingEnvelope` (`mesh_watch_test.go:743-784`) is extended to verify state.json transition for both sub-cases (missing-entirely vs. present-at-queued).
- [ ] New test: `niwa_cancel_task` against a task in `state="abandoned, reason=taskstore_lost"` returns the terminal state directly with no `too_late` framing.
- [ ] No new task-state constant added; `validTaskStates` and `isTaskStateTerminal` are unchanged.
- [ ] `inbox/dangling/<id>.json` files continue to be created as forensic preservation; the daemon no longer treats them as the primary state signal.
- [ ] Must deliver: `taskstore_lost`-classified tasks reach `state="abandoned"` (required by <<ISSUE:7>> — `niwa_redelegate` from a `taskstore_lost` source is the documented recovery path).

## Dependencies

None. Phase 4 is independent of <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>, <<ISSUE:4>> and can ship in parallel.

## Downstream Dependencies

- <<ISSUE:7>> (niwa_redelegate) treats `taskstore_lost` `abandoned` as a valid source state for redelegation.
- <<ISSUE:8>> documents the new `taskstore_lost` lifecycle and recovery path in the niwa-mesh skill and `docs/guides/sessions.md`.
