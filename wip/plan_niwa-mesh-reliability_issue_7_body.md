---
complexity: testable
complexity_rationale: New MCP tool with `kindDelegator` auth, source body propagation, and a structured response carrying `source_state_at_fork`. Touches the wire schema (new tool registration), `handleRedelegate` handler, and `TaskEnvelope.RedelegatedFrom` field. Multi-state source handling including the `taskstore_lost` recovery path.
---

## Goal

Add the `niwa_redelegate(source_task_id, ...)` MCP primitive that re-fires a previously-delegated task body without rewriting it, accepting any source state (including the `taskstore_lost` abandoned state from <<ISSUE:5>>) and stamping `redelegated_from` on the new envelope for audit chaining. The response carries `source_state_at_fork` so callers distinguish recovery flows from active forks.

## Context

Design: `docs/designs/current/DESIGN-niwa-mesh-reliability.md`

The task store is flat by task_id (`<taskStoreRoot>/.niwa/tasks/<id>/{envelope,state}.json`) and not partitioned by state, so `ReadState(taskDirPath(...))` recovers the source envelope regardless of which inbox subdir the message ended up in. This makes redelegate trivial mechanically.

Per the design's Decision Outcome and Solution Architecture:
- Authorization: `kindDelegator` on `source_task_id` (only the original delegator can re-issue), mirroring the auth pattern of `niwa_cancel_task` and `niwa_update_task`.
- Source state allow-list: any of `queued`, `running`, `completed`, `abandoned`, `cancelled`. The source's state is unchanged by the call. An active source (queued/running) keeps progressing; the new task runs independently.
- Body: source envelope's `body` is reused verbatim unless `body_overrides` is provided (shallow JSON-merge at top level).
- `from`: reset to caller's role/PID (so subsequent `kindDelegator` auth on the new task works correctly).
- `redelegated_from`: a new `TaskEnvelope` field that points to the source task's ID for audit chain traversal.
- Response always carries `source_state_at_fork: <string>` so callers can distinguish recovery (terminal source) from active fork (queued/running source). When `source_state_at_fork` is `queued` or `running`, the caller has explicitly forked active work — the same fan-out shape as multiple parallel `niwa_delegate` calls — and is expected to know whether the body's side effects are safe to run twice.
- Edge case: if the source's `envelope.json` is missing entirely (the `taskstore_lost` recreate-stub case from <<ISSUE:5>>), the handler returns a structured `SOURCE_BODY_LOST` error so the caller can supply the body via `body_overrides` and retry.

The same `required_skills` gate from <<ISSUE:6>> runs in `handleRedelegate` against the merged body before the new envelope is written.

Closes #114.

## Acceptance Criteria

- [ ] New tool registration `niwa_redelegate` in `internal/mcp/server.go` (around the `niwa_delegate` registration at `server.go:264-279`). Wire schema: `source_task_id: string [required]`, plus optional `to`, `session_id`, `read_only`, `body_overrides`, `mode`, `expires_at`.
- [ ] New `handleRedelegate` handler in `internal/mcp/handlers_task.go` that:
  1. Authorizes the caller with `kindDelegator` on `source_task_id` (precedent: `handleCancelTask` at `handlers_task.go:879`).
  2. Reads source via `ReadState(taskDirPath(s.taskStoreRoot(), source_task_id))`.
  3. Validates source state is in `{queued, running, completed, abandoned, cancelled}` (i.e., any legal state). For `taskstore_lost` cases where `envelope.json` is missing, returns `errResultCode("SOURCE_BODY_LOST", ...)` so the caller can re-supply the body via `body_overrides`.
  4. Computes the new body: source body verbatim, or shallow-merged with `body_overrides` if provided.
  5. Runs the same `required_skills` gate from <<ISSUE:6>> against the merged body. Returns `MISSING_SKILLS` on miss.
  6. Calls `createTaskEnvelope` with: new `task_id`, new `sent_at`, `from` reset to caller's role/PID, `redelegated_from` set to source task ID, target role from `to` (or source's `to.role` if not overridden), `session_id` from arg (or source's), `read_only` from arg (or source's), `expires_at` from arg (otherwise omitted, source's `expires_at` is NOT propagated by default since it represents the source's clock, not the new task's).
  7. Returns a response carrying `task_id` (new), `redelegated_from` (source ID), and `source_state_at_fork` (the source's state at the moment of the redelegate call).
- [ ] New `TaskEnvelope` field: `RedelegatedFrom string \`json:"redelegated_from,omitempty"\`` (`internal/mcp/types.go`). Field is omitempty-safe; existing envelopes without the field deserialize cleanly.
- [ ] `niwa_redelegate` is added to `allowed_tools.go:18` (alongside `niwa_delegate`) and the audit log error vocabulary.
- [ ] Audit log captures `niwa_redelegate` calls with `source_task_id` in `arg_keys` (top-level wire field).
- [ ] Functional test (recovery from `abandoned`): redelegate from a previously-completed task that was marked abandoned produces a new task with the source's body verbatim; response carries `source_state_at_fork: "abandoned"`.
- [ ] Functional test (`taskstore_lost` envelope present): redelegate from an abandoned `reason=taskstore_lost` source where envelope.json survived works; new task's body matches source's body.
- [ ] Functional test (`taskstore_lost` envelope missing): redelegate from a source where envelope.json is absent returns `SOURCE_BODY_LOST`. After the caller retries with `body_overrides`, the redelegate succeeds.
- [ ] Functional test (active fork from `running` source): redelegate from a running source produces a new independent task; the source continues to run; response carries `source_state_at_fork: "running"`. Both reach terminal states independently.
- [ ] Functional test (active fork from `queued` source): redelegate from a queued source produces a new task; both are claimable independently; response carries `source_state_at_fork: "queued"`.
- [ ] Functional test (`MISSING_SKILLS` propagation): redelegating with a body whose `required_skills` cannot be satisfied returns `MISSING_SKILLS` and no new task is created.
- [ ] Functional test (auth): a caller whose `s.role` differs from the source's `from.role` is rejected with the standard `kindDelegator` error.
- [ ] `redelegated_from` chains correctly across multiple redelegations: redelegating a task that itself was redelegated produces an envelope whose `redelegated_from` points to the most recent source (callers can traverse the chain by following the field).
- [ ] Must deliver: stable `niwa_redelegate` API including `source_state_at_fork` in the response (required by <<ISSUE:8>> for the niwa-mesh skill text).

## Dependencies

- <<ISSUE:4>> (worker config inheritance) — the redelegate gate reads from the workspace skill manifest <<ISSUE:4>> makes authoritative.
- <<ISSUE:5>> (`taskstore_lost` abandoned transition) — the redelegate handler treats `abandoned, reason=taskstore_lost` as a recovery source; without <<ISSUE:5>>, dangling envelopes have no representable source state.

## Downstream Dependencies

- <<ISSUE:8>> documents `niwa_redelegate` in the niwa-mesh skill text, including the `source_state_at_fork` signal and the `taskstore_lost` recovery flow.
