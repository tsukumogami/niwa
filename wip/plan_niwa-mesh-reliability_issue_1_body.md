---
complexity: testable
complexity_rationale: Cross-cutting refactor affecting role-existence checks and inbox routing for coordinator-targeted calls; touches three call sites in `internal/mcp/server.go` and adds two new auto-registration trigger points in `internal/mcp/handlers_task.go`. Behavior change with auth implications (workers reaching the main instance's coordinator role).
---

## Goal

Unblock `niwa_ask(to="coordinator")` and `niwa_send_message(to="coordinator")` from session workers by introducing a `roleRoot(role)` helper that redirects coordinator-targeted role lookups and inbox writes to the main instance, and expand coordinator auto-registration to fire on `niwa_delegate` and `niwa_query_task`.

## Context

Design: `docs/designs/current/DESIGN-niwa-mesh-reliability.md`

`isKnownRole(role)` at `internal/mcp/server.go:768-778` does `os.Stat(<s.instanceRoot>/.niwa/roles/<role>/)`. For a session worker, `s.instanceRoot` is the worktree, where `scaffoldWorktreeNiwa` only creates the worker's own role dir — never `roles/coordinator/`. So workers fail at `UNKNOWN_ROLE` before the live-coordinator routing logic in `handleAsk` (added by PR #93 at `server.go:817-819`) can run. The same gap applies to `sendMessageWithID` whose inbox path also anchors to `s.instanceRoot` (`server.go:719`).

`maybeRegisterCoordinator` only fires from `niwa_check_messages` and `niwa_await_task`. A coordinator that uses only `niwa_delegate` + `niwa_query_task` (a fan-out-then-poll pattern) never registers, so even with the precondition fixed, ask-routing falls through to `no_live_session`.

Closes #92 and #109.

## Acceptance Criteria

- [ ] New helper `roleRoot(role string) string` on `*mcp.Server` returns `s.mainInstanceRoot` when `role == "coordinator" && s.mainInstanceRoot != ""`, else `s.instanceRoot`.
- [ ] `isKnownRole` (`server.go:768-778`) consults `roleRoot(role)` instead of bare `s.instanceRoot`.
- [ ] `sendMessageWithID` (`server.go:719`) consults `roleRoot(args.To)` when computing `inboxDir`.
- [ ] `handleAsk`'s `askRoot` selection (`server.go:817-819`) routes through the same `roleRoot` helper rather than inline conditional (refactor with no behavior change here).
- [ ] `maybeRegisterCoordinator` is called from `handleDelegate` and `handleQueryTask` at handler entry (existing `handleCheckMessages` and `handleAwaitTask` triggers stay).
- [ ] Functional test: a session worker can call `niwa_ask(to="coordinator", body=...)` and receive an answer from the live coordinator via the existing `task.ask` flow. Same path works for `niwa_send_message(to="coordinator", ...)`.
- [ ] `@critical` Gherkin scenario in `test/functional/features/` exercises the worker → live-coordinator path end-to-end.
- [ ] No call site in `internal/mcp/server.go` computes `<s.instanceRoot>/.niwa/roles/...` directly for `coordinator` outside the helper (grep).
- [ ] Worktree daemon's `watched_roles count=N` log line is unchanged (no synthetic `coordinator/` directory created in worktrees).
- [ ] Must deliver: clean `roleRoot` helper with `coordinator`-targeted redirect, plus expanded auto-registration trigger set (required by <<ISSUE:8>> for the niwa-mesh skill rewrite).

## Dependencies

None. Phase 1 is independent of all other issues and can ship first.

## Downstream Dependencies

- <<ISSUE:8>> rewrites the niwa-mesh skill text to reflect the `roleRoot` redirect and the corrected "Worker asks coordinator" pattern. The skill text waits on this issue's runtime so the documented contract matches what ships.
