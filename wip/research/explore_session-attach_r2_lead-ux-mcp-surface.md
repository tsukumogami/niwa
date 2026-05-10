# Lead: MCP tool surface review

## Findings

### Current MCP tools relevant to sessions

The MCP server (`internal/mcp/server.go:228-401`, `toolsList`) registers 14 tools across three families. Six are directly relevant to session-attach UX. Each row gives input shape, output shape, and call direction (coordinator vs. worker) inferred from the implementation.

| Tool | Input | Output | Caller |
|------|-------|--------|--------|
| `niwa_create_session` | `{ repo, purpose, parent_session_id? }` | `{ session_id, worktree_path, daemon_warning? }` | Coordinator only. `handleCreateSession` (`handlers_session.go:146-229`) creates a worktree and starts a daemon; workers have no business creating sessions. |
| `niwa_destroy_session` | `{ session_id, force? }` | `{ session_id, status, branch_warning? }` | Coordinator. `handleDestroySession` (`handlers_session.go:237-309`) force-kills workers and removes the worktree. Also reachable via the `niwa session destroy` CLI through `DestroySessionDirect` (`server.go:148-153`). |
| `niwa_list_sessions` | `{ repo?, status? }` | JSON-array of `SessionLifecycleState` (file shape from `session_lifecycle.go:30-47`) | Both. Coordinator polls to discover live sessions; a worker can also call it (no role check in `handleListSessions`, `handlers_session.go:26-50`). The CLI `niwa session list` reads the same files directly without going through MCP. |
| `niwa_delegate` | `{ to, body, mode?, expires_at?, session_id?, read_only? }` | async: `{ task_id }`. sync: terminal-state JSON | Coordinator (and any worker delegating downstream). Carries `session_id` to route into a session worktree's inbox via `resolveCreationInboxDir` (`handlers_task.go:265-293`). |
| `niwa_ask` | `{ to, body, timeout_seconds? }` | answer body or `no_live_session` JSON | Worker → coordinator (live-coordinator path at `server.go:792-888`). Not directly relevant to attach but shares the lifecycle-state file with attach state. |
| `niwa_send_message` | `{ to, type, body, reply_to?, task_id?, expires_at? }` | `{ id, to }` | Both — generic peer messaging. Not session-aware. |

The CLI bypasses MCP for attach-shaped operations: `CreateSessionDirect` and `DestroySessionDirect` (`server.go:139-153`) are CLI-callable wrappers that share the MCP handler. Any future `AttachSessionDirect` would be the same shape, but the user-imposed guidance is **CLI only — no MCP tool**.

### What needs to surface via MCP for attach

The coordinator needs three signals:

1. **Discovery: "is session X attached right now?"** — answered by an additive field on `niwa_list_sessions` output (the `SessionLifecycleState` shape). Round 1's coordinator-awareness lead established that this is filesystem-readable from the coordinator's instance root, so MCP just surfaces what's already on disk. No new tool needed.

2. **Delegation outcome: "I delegated and the task is sitting; is that because attach is holding it?"** — also answered by the same `niwa_list_sessions` poll plus `niwa_query_task` returning `state == queued` for longer than expected. The coordinator's recovery loop (already documented in mesh skill content) handles this: re-await, on timeout poll list, on attach-held continue waiting.

3. **Stop signal: "the human's terminal died; I want to release the lock."** — this is the only ambiguous case. See sub-question 3 below.

For (1) and (2), the existing tool set is sufficient — no new MCP surface.

### Coordinator notification model: poll, not push

Round 1's coordinator-awareness lead already established that there is no push-to-coordinator notification path for session state today (`watcher.go:202-205` only pushes per-message-type for the *coordinator's own inbox*, not session state). The design doc (`DESIGN-cross-session-communication.md:353`) intentionally chose filesystem-only coordination as a principle.

**Recommendation: do not add a push channel for attach.** Polling via `niwa_list_sessions` is sufficient because:

- Attach is a slow-moving signal (humans attach for minutes/hours; sub-second notification is overkill).
- The coordinator's existing flow is "delegate → `niwa_await_task`" with a re-await loop on timeout. Adding "if timeout, call `niwa_list_sessions` to see if target is attached" is a one-line skill update, not an architectural change.
- `notifications/claude/channel` push is per-inbox; it would require a new architectural primitive ("session lifecycle events") that doesn't exist for any other session state transition (status, daemon liveness from PR #115, conversation ID). Attach is the wrong feature to introduce that primitive on.

The user's "no new MCP tools" guidance is consistent with this: a push channel would require a new tool/notification type, and the polling-only model means we don't need one.

### `niwa_list_sessions` schema change

The exact JSON shape change to `SessionLifecycleState` (the only struct `niwa_list_sessions` returns, defined at `session_lifecycle.go:30-47`):

```json
{
  "v": 1,
  "session_id": "ab12cd34",
  "status": "active",
  "creation_time": "...",
  "worktree_path": "...",
  "claude_conversation_id": "...",
  "creator_pid": 12345,
  "creator_start_time": 167890123,

  "daemon": {                    // from PR #115 / issue #111
    "alive": true,
    "pid": 23456,
    "started_at": "..."
  },

  "attach": {                    // NEW for issue #117
    "held": true,
    "owner_pid": 34567,
    "owner_start_time": 167890456,
    "started_at": "2026-05-09T10:00:00Z"
  }
}
```

Key shape decisions:

- **Sub-object, not flat field** (matches PR #115's `daemon` sub-object decision rather than competing with it). Round 1's state-model lead also recommended this on the precedent of #111.
- **`attach` is omitted entirely** (using `omitempty` on the Go struct) when no attach has ever happened on this session. When held, all fields populate. When released-cleanly, the writer can either delete the sub-object or leave a `held: false` skeleton; the PRD should commit to one — round 1's lock-semantics lead points at `attach.state` being a sentinel JSON file written on attach and removed on detach, which means the sub-object should be **absent when not attached** (read-time derivation: file exists → `held: true`; file missing → field omitted).
- **`held` is the boolean** the coordinator's skill content checks. PID liveness (via `IsPIDAlive(owner_pid, owner_start_time)`) lets stale locks read as `held: false` even if the file is still on disk — round 1's state-model lead established this as the precedent.
- **No `availability` enum**. Round 1's coordinator-awareness lead recommended "do not collapse daemon health and attach state into one enum because it loses the diagnostic." A nested `attach` sub-object is the cheapest way to keep the two axes orthogonal.

### Schema version bump: not required

PR #115 adds a `daemon` sub-object **without bumping `v` from 1 to 2**. That sets the precedent: optional additive fields/sub-objects don't require a `v` bump. The contributor note in `docs/guides/sessions.md:303-308` says "if you add fields, increment V and handle the zero-value when reading existing state files" — but PR #115 explicitly chose not to do that for `daemon`, and the round 1 state-model lead's recommendation to bump V to 2 was based on reading the contributor note literally. The actual landed precedent is **no bump for additive optional fields**.

The PRD should commit to **no `v` bump for `attach`** to match the PR #115 precedent. If round 1 lands first and bumps V, attach follows. If PR #115 lands first without bumping, attach also doesn't bump. Either way, the two land cleanly together because the underlying decision (bump or no-bump) is consistent across both.

### `niwa_delegate` behavior on attached session

This is the most nuanced sub-question. Today, `niwa_delegate(session_id=X)` resolves the session via `resolveCreationInboxDir` (`handlers_task.go:261-293`), which reads `SessionLifecycleState` and returns `SESSION_INACTIVE` if status is not `active`. **Attached sessions remain `Status: active`** (attach is the orthogonal axis), so the gate does not fire — the envelope writes successfully.

Per round 1's coordinator-awareness lead: the daemon is `TerminateDaemon`'d for the attach duration (round 1 lock-semantics lead recommendation), so the envelope sits in the worktree's inbox until detach. On detach, `EnsureDaemonRunning` re-spawns the daemon, the catch-up scan path picks up the queued envelope, and delivery proceeds.

**MCP contract recommendation: `niwa_delegate` returns success-as-normal.** Specifically:

- Async mode: returns `{ task_id }` exactly as today. The task is real, the envelope is on disk in the right inbox, the only difference is the daemon will not claim it until attach releases.
- Sync mode: blocks on the awaitWaiter exactly as today. The wait is just longer.

**Why not return a `queued_for_attach` hint?**

1. The hint adds nothing actionable. The coordinator's recovery is the same regardless: keep the awaitWaiter or the async task; if it sits in `queued` longer than expected, poll `niwa_list_sessions` to check `attach.held` and decide whether to wait or `niwa_cancel_task`.

2. It would race. By the time `niwa_delegate` returns, the human may have detached. A `queued_for_attach` hint would be stale. The authoritative signal is whatever the coordinator reads on the *next* poll.

3. It would inconsistent with `daemon.alive: false`. If the daemon is dead (PR #115 case #112 — the dangling territory), `niwa_delegate` doesn't return a "queued because daemon dead" hint either; the coordinator infers it from polling. Attach should match the existing pattern.

So the MCP contract of `niwa_delegate` does **not** change for attached sessions. The behavior change is entirely on the daemon side: the lock-aware claim skip + release-triggered re-scan documented in round 1's coordinator-awareness lead.

### `niwa_destroy_session` on attached session

This question is implied by the issue but worth pinning. Today `handleDestroySession` runs `killSessionWorkers` (force-kills any running workers, `handlers_session.go:320-366`) and `git worktree remove --force`. If a human is attached, this would kill the human's `claude` process and yank the worktree out from under them.

Recommended contract:

- `niwa_destroy_session(session_id=X)` on an attached session: return a new `SESSION_ATTACHED` error code with the `owner_pid` from the attach state. Do not destroy.
- `niwa_destroy_session(session_id=X, force=true)`: destroy regardless. Existing `force` already means "force git branch delete even if unmerged" — extending it to "also force destroy through an attach lock" matches the existing escape-hatch semantic. The destroy path's existing `killSessionWorkers` will SIGKILL the human's `claude --resume` process as a side effect; that is the cost of `--force`.

This is a small extension to the existing tool, not a new tool. Confirms the user's "no new MCP tools" guidance.

### Programmatic detach: out of scope

Sub-question 3 asks whether the coordinator should be able to call `niwa session detach <id> --force` via MCP. The user's guidance is "operator-only" and that holds. Reasoning:

1. The detach lock is `flock` on `<worktree>/.niwa/attach.lock` (round 1 lock-semantics lead). Implicit release happens when the holding process's fd closes — i.e., when the human's `niwa session attach` process exits. The lock is *self-releasing* on terminal disconnect (shell exit closes fds). The "stuck attach" scenario only arises if the holding process is still running but the human is gone (e.g., orphaned via SSH disconnect with `tmux`/`screen`).

2. The recovery primitive for that case is **PID-based stale-lock detection** (round 1 state-model lead) — readers see `attach.held: true` but `IsPIDAlive(owner_pid, owner_start_time) == false` and treat the session as effectively unattached. This works without any MCP call.

3. `niwa session detach <id> --force` covers the genuinely-stuck case (human's process is alive but unwanted, e.g., wedged terminal). That's a human-typing-CLI concern. A coordinator that wanted to do this would have to know whether a human is actually unreachable, which is a judgment call the coordinator is not equipped to make.

4. Adding `niwa_detach_session` MCP would tempt coordinators to "clean up" attached sessions on their own initiative — a security and UX footgun. Keep it CLI-only.

Confirmed: no programmatic detach via MCP. No `niwa_attach_session` MCP either (sub-question 7 — the user explicitly excluded it, and there is no found case where a coordinator should programmatically take over a Claude conversation as a worker).

### `niwa_ask`, `niwa_send_message` interaction with attached session

`niwa_ask` is worker → coordinator. The coordinator role lives on the main instance, not in a session worktree, so attach state on a session does not affect ask routing. No change.

`niwa_send_message` writes directly to a role's inbox via atomic rename. If the recipient role lives in an attached session (the worktree role), the message lands but the daemon won't process it until detach. Same UX as `niwa_delegate` — message-level inbox queueing during attach is silent and recovers via daemon re-scan on release. The MCP contract of `niwa_send_message` does not change.

## Implications

**Confirmed: the user's "no new MCP tools" guidance holds.**

The investigation found zero cases that warrant a new MCP tool. Every coordinator-relevant signal (attach state, queued-because-attached, stale-lock recovery) flows through additive extensions to existing tools or through filesystem reads that the coordinator already does.

**Exact MCP schema changes:**

1. **`niwa_list_sessions` output** — add an `attach` sub-object to `SessionLifecycleState` (Go struct at `internal/mcp/session_lifecycle.go:30-47`):
   ```go
   type AttachInfo struct {
       Held           bool   `json:"held"`
       OwnerPID       int    `json:"owner_pid,omitempty"`
       OwnerStartTime int64  `json:"owner_start_time,omitempty"`
       StartedAt      string `json:"started_at,omitempty"`
   }

   type SessionLifecycleState struct {
       // ... existing fields ...
       Attach *AttachInfo `json:"attach,omitempty"`
   }
   ```
   Source of truth for the `attach` field is the round 1 lock-semantics lead's `<worktree>/.niwa/attach.state` sentinel file. `handleListSessions` derives the field at read-time by stat-ing that file and applying `IsPIDAlive`. The lifecycle JSON is **not** the authoritative writer for attach state — keeping the lifecycle file single-writer-per-session preserves the existing concurrency model. The derived `attach` field is computed during `niwa_list_sessions` synthesis, parallel to how PR #115 derives `daemon` from `daemon.pid` + a liveness probe.

2. **No `v` bump.** Match PR #115's precedent: additive optional sub-objects don't bump V.

3. **`niwa_destroy_session` error path** — new error code `SESSION_ATTACHED` returned when `attach.held == true` and `force != true`. `force=true` continues to destroy, killing the attached `claude` process via existing `killSessionWorkers`. This is a new error-code addition, not a new tool, and matches the existing `force` extension pattern (`force` already gates `git branch -D` over `git branch -d`).

4. **`niwa_delegate` contract: unchanged.** The envelope writes, the task ID returns, the awaitWaiter blocks in sync mode. Daemon-side queueing is invisible to MCP callers. No new error code, no new field in the response.

5. **`niwa_ask`, `niwa_send_message`, `niwa_query_task`, `niwa_await_task`, `niwa_create_session`: unchanged.**

**No new MCP tools. No notifications. No push channel. No `niwa_attach_session`. No `niwa_detach_session`.**

## Surprises

1. **PR #115 sets the precedent of NOT bumping `v` for additive sub-objects**, contradicting the contributor note in `docs/guides/sessions.md:303-308`. Round 1's state-model lead recommended bumping V to 2, but the actual landed precedent (the `daemon` sub-object) is no bump. The PRD should resolve this contradiction explicitly: state that adding `attach` as an `omitempty` sub-object does not require a V bump, matching PR #115. If PR #115's reviewer pushes back and forces a V bump on landing, attach follows. The two features are coupled on this decision.

2. **The `attach` sub-object is computed at read-time, not stored in the lifecycle JSON.** This is the cleanest split per round 1's lock-semantics lead: `<worktree>/.niwa/attach.state` is the on-disk source of truth (atomic write on attach, deleted on detach); the lifecycle JSON's `attach` sub-object is a *projection* of that file onto the listing response. This keeps the lifecycle JSON's existing single-writer model intact (no third writer alongside coordinator and per-worktree daemon). PR #115's `daemon` sub-object follows the same projection pattern, deriving from `daemon.pid` plus a liveness probe.

3. **`SESSION_INACTIVE` does not need to extend to attach state.** The round 1 state-model lead's Option A (use `Status` for attach) would have collapsed attach into the existing `SESSION_INACTIVE` gate — but Option B (orthogonal field) is the recommended path, and that means `niwa_delegate` succeeds normally on attached sessions. The "queue happens daemon-side, invisibly" model holds.

4. **`niwa_list_sessions` is callable from workers, not just coordinators.** I expected a role check; there isn't one. This means an attached worker (the human's `claude` inside the worktree) could call `niwa_list_sessions` and see itself listed with `attach.held: true`. This is fine but surprising; the PRD should not bother gating it.

## Open Questions

1. **Should `attach.held: false` ever appear, or is the field always either populated-with-`held:true` or absent?** Round 1's lock-semantics lead leans toward "absent when not attached" (no sentinel file). If we ever want to surface "this session was attached and detached at T" historically, a populated-but-`held:false` would help. Probably defer — no use case for history surfacing today.

2. **`niwa_destroy_session(force=true)` on an attached session writes a `branch_warning` if unmerged. Does it also write an `attach_killed_warning`?** Probably yes, mirroring `branch_warning`. The PRD should commit either way; cheapest is to add an `attach_killed: true` field to the destroy response when force-destroyed through an active attach.

3. **`mcp-audit.log` (per `DESIGN-mcp-call-telemetry.md`) — does an attach state change need an audit entry?** No — attach is not an MCP call. The CLI process's flock acquisition is filesystem-level, not MCP-mediated. Audit logging stays scoped to MCP tool invocations.

4. **Coordinator skill content updates** — the niwa-mesh skill should teach coordinators "if `niwa_await_task` times out and the target is in a session, call `niwa_list_sessions` and check `attach.held`." This is a skill-text change, not an MCP contract change, and is owned by PR #115's phase 6 (skill rewrite). Attach should land its skill text in the same drop or as a follow-up.

## Summary

The MCP contract change is minimal and entirely additive: a new optional `attach` sub-object on `niwa_list_sessions` output (computed at read-time from a sentinel file in the worktree, matching PR #115's `daemon` projection pattern), a new `SESSION_ATTACHED` error code from `niwa_destroy_session` when force is not set, and no change at all to `niwa_delegate` / `niwa_ask` / `niwa_send_message` / `niwa_create_session` / `niwa_query_task` / `niwa_await_task`. The user's "no new MCP tools, CLI-first" guidance holds under closer inspection — every attach-relevant signal flows through coordinator-side polling of the existing tools or filesystem reads of files the coordinator already accesses, and no programmatic attach/detach surface emerged as necessary. The biggest open question is whether to bump `SessionLifecycleState.V` from 1 to 2: PR #115 sets the no-bump precedent for its `daemon` sub-object despite the contributor note saying otherwise, so attach should follow PR #115's lead and the two features land their schema additions on the same V.
