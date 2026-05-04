# Lead: Follow-on features from persistent sessions

## Findings

### Session → PR lifecycle tracking

If each session maps to one worktree and one branch, tracking the PR lifecycle becomes
natural. The coordinator could store a `pr_number` (or URL) in `.niwa/sessions/<id>/state.json`
after calling `gh pr create`. Subsequent queries could poll PR status, CI results, and
review state without the coordinator needing to re-derive the PR reference.

The plumbing is partly there: tasks already track outcomes and results in `state.json`.
Adding a `pr_url` field to `SessionState` is backward-compatible and low-cost. The
coordinator skill would call `gh pr create`, capture the URL, then call
`niwa_update_session(session_id, {pr_url: "..."})` to persist it.

Value: high. The coordinator currently has no durable record of which PR a session
produced. This gets lost on compaction.

### Session summary for compacted coordinators

When a coordinator's context window is compacted, it loses all in-memory session
references. A `niwa_session_summary(session_id)` tool could reconstruct context from:
- Session state (repo, purpose, status, tasks list)
- Task outcomes and result fields (from `.niwa/tasks/<id>/state.json`)
- Git log of the session branch (commits since branching from main)

The data is already available in current state files. The challenge is filtering:
task progress body fields may contain sensitive or verbose intermediate data. The tool
should expose only argument keys (matching the audit-log redaction model), not full
progress bodies.

Value: very high. A compacted coordinator hitting `niwa_list_sessions` + `niwa_session_summary`
could re-orient without requiring the user to restate context.

Two implementation options:
- **On-demand**: query state at call time. Always current, never stale.
- **Pre-materialized**: write a summary when each task completes. Faster for compaction
  recovery but risks stale data if not updated correctly.

On-demand is safer and simpler for V1.

### Session handoff between coordinators

Transferring ownership of a session to a new coordinator requires:
- Coordinator identity in `SessionEntry` (currently PID-based; fragile across reboots)
- A claim mechanism: new coordinator calls `niwa_claim_session(session_id)`, which
  updates `coordinator_pid` and `coordinator_session_id` in session state

`SessionEntry.ClaudeSessionID` already exists in the types but is not populated by
`maybeRegisterCoordinator`. Filling this field would enable both crash recovery and
handoff.

Value: medium. Useful for long-running projects where the original coordinator crashes
or is compacted beyond recovery. Lower urgency than summary generation.

### Session audit history

All task transitions and progress updates are already logged in `.niwa/tasks/<id>/`.
Reconstructing "what this session did, in order" is mechanical: enumerate tasks by
creation time, read their states, summarize outcomes. No new infrastructure needed —
this is a query over existing state.

A `niwa_session_history(session_id)` tool could return the ordered list of tasks
with their outcomes, timing, and git commits produced. This enables post-session review
and handoff documentation.

Value: medium. High utility for debugging and review; low implementation cost since
the data already exists.

### Session-scoped resource cleanup

When a session ends, niwa needs to clean up:
- The git worktree (`git worktree remove`)
- The daemon process (if per-worktree daemons are used)
- The session state directory (if work is pushed)
- Or preserve all of the above (if work is unpushed)

This is not a "follow-on" but a core part of session lifecycle. However, it benefits
from being designed alongside the PR tracking feature: a session should only be fully
cleaned up when its PR is merged (or explicitly abandoned), not just when the coordinator
calls `niwa_end_session`. A `status: "pending_merge"` terminal state for sessions that
have an open PR but aren't fully done would enable this.

Value: high. Without it, worktrees accumulate on disk as the coordinator ends sessions
and must be manually pruned.

## Implications

**Design requirements from follow-ons:**

1. **Reserve `pr_url` in `SessionState`** — zero-cost now, unlocks PR tracking.
2. **Pre-populate `coordinator_session_id` in `SessionEntry`** — enables handoff and
   crash recovery without additional infrastructure.
3. **Design `niwa_end_session` with status transitions** — `ended` vs `pending_merge`
   vs `abandoned` — so cleanup can be deferred until PR is merged.
4. **Transitions log must be queryable** — the audit history feature relies on reading
   task transitions without exposing redacted fields. Ensure the log format is stable.
5. **On-demand summary via `niwa_session_summary`** — highest-value follow-on; should
   be in V1 of the session API.

## Surprises

The existing `TaskState` audit trail (transitions.log with timestamps and outcomes) is
already rich enough to reconstruct session history without any new instrumentation.
The gap is aggregation across tasks, not data collection.

`SessionEntry.ClaudeSessionID` already exists as a field but is empty today — it was
designed for this use case and never implemented.

## Open Questions

1. Should `niwa_session_summary` be on-demand or pre-materialized at task completion?
   On-demand is simpler; pre-materialized is faster for compaction recovery.
2. What fields should session summary expose? Full task results risk leaking verbose
   intermediate data; argument keys only may be too sparse for coordinator re-orientation.
3. Should session lifecycle include a `pending_merge` state, or is `niwa_end_session`
   always final (leaving PR tracking entirely to the coordinator)?
4. Should coordinator session ID be written to `SessionEntry` automatically on
   `maybeRegisterCoordinator`, or only when explicitly creating a session?

## Summary

Five high-value follow-ons are enabled by persistent sessions: session→PR tracking
(low-cost, reserve `pr_url` field now), session summary for compacted coordinators
(highest value, data already available, implement as on-demand query), session handoff
(requires coordinator identity in `SessionEntry`, already scaffolded), session audit
history (free from existing transitions.log), and session-scoped resource cleanup
(core feature, not a follow-on). The design should reserve these extension points now
to avoid preclusion.
