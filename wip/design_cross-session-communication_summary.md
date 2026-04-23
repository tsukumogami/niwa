# Design Summary: cross-session-communication

## Input Context (Phase 0)

**Source PRD:** `docs/prds/PRD-cross-session-communication.md`

**Problem (implementation framing):** Replace the existing mesh wholesale.
Route messages by role (not session UUID) through inboxes provisioned at
apply time; track task state in per-task directories independent of process
lifetime; spawn ephemeral `claude -p` workers on demand via a stateless
daemon; expose a task-first-class MCP tool surface (`niwa_delegate`,
`niwa_query_task`, `niwa_await_task`, `niwa_report_progress`,
`niwa_finish_task`, `niwa_list_outbound_tasks`, `niwa_update_task`,
`niwa_cancel_task`, `niwa_ask`, `niwa_send_message`, `niwa_check_messages`)
with deterministic testability via `NIWA_WORKER_SPAWN_COMMAND`. Reuse the
existing Applier pipeline integration, hook injection, hybrid activation,
and Claude session-ID discovery machinery for the coordinator. Install a
default `niwa-mesh` skill into every agent at apply time.

**Prior-art designs (deleted once this design is accepted):**
- `docs/designs/current/DESIGN-cross-session-communication.md` — the
  currently-implemented mesh (`claude --resume` wakeup, per-session-UUID
  inboxes, manual registration). Provides reusable mechanism for:
  InstallChannelInfrastructure pipeline insertion, hybrid activation
  model, Claude session-ID discovery (for coordinator), `sessions.json`
  advisory locking. Core mesh model is replaced.
- `docs/designs/DESIGN-channels-integration-test.md` — proposed `@channels-e2e`
  tests via pre-registered coordinator running `claude -p`. Approach is
  obsolete under the new model (`claude -p` is the worker, not the
  coordinator); coverage intent is preserved via `NIWA_WORKER_SPAWN_COMMAND`
  test harness + a small `@channels-e2e` set that exercises real `claude -p`
  workers.

## Current Status

**Phase:** 0 — Setup (PRD)
**Last Updated:** 2026-04-22
