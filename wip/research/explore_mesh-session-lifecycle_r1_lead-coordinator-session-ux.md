# Lead: Coordinator session lifecycle UX

## Findings

### 1. Current Coordinator Context and Task Delegation Model

The coordinator operates within the niwa mesh using these MCP tools (in `internal/mcp/server.go` lines 1-16, `handlers_task.go`):

**Existing MCP tools available to coordinators:**
- `niwa_delegate(to, body, mode, expires_at)` — creates a task in target role's inbox (handlers_task.go:103)
- `niwa_query_task(task_id)` — non-blocking snapshot of any task
- `niwa_await_task(task_id, timeout_seconds)` — blocks until terminal state (default 600s timeout)
- `niwa_report_progress(task_id, summary, body)` — update task progress
- `niwa_finish_task(task_id, outcome, result, reason)` — transition task to terminal state
- `niwa_check_messages()` — poll role inbox for messages and task state transitions
- `niwa_ask(to, body, timeout_seconds)` — escalate question to another role
- `niwa_send_message(to, type, body, ...)` — peer messaging

**Key constraint on coordinators:** Each `niwa_delegate` call is currently async-first. The coordinator either:
1. Returns immediately with `task_id` and calls `niwa_await_task` to block later, OR
2. Passes `mode="sync"` and blocks until the worker finishes (no default timeout)

**No session concept exists at the MCP level.** Task IDs are the unit of identity. Coordinators track them explicitly in memory or filesystem state. A coordinator that performs sequential delegations (e.g., `/shirabe:design` → `/shirabe:plan` on the same repo) loses conversational context between calls because each `niwa_delegate` spawns a fresh `claude -p` worker process.

### 2. Current Session Registration and Coordinator Visibility

**Session registration flow** (`session_registry.go:94-141`, `server.go:390`):
- Coordinators auto-register to `sessions.json` when they first call `niwa_await_task` or `niwa_check_messages` (lazy registration).
- The registration writes: role, PID, start time, inbox dir, timestamp (SessionEntry struct, types.go:102-111).
- `maybeRegisterCoordinator()` is called at top of `handleAwaitTask` (handlers_task.go:322) and implicitly on `niwa_check_messages` (server.go:390).
- **Workers cannot register** — only coordinators have a SessionEntry. Workers register their Claude session ID to TaskState.Worker.ClaudeSessionID at MCP server startup (server.go:933-947) for resume-on-stall recovery.

**Coordinator liveness is tracked via:**
- PID and process start-time pair stored in SessionEntry
- `IsPIDAlive(pid, startTime)` check (liveness.go) verifies the process is still running
- Stale entries are auto-pruned when `lookupLiveCoordinator` or subsequent registrations scan the registry

### 3. Worker → Coordinator Communication: Live Ask Routing

When a worker calls `niwa_ask(to='coordinator')` (`server.go:682-774`):
1. `handleAsk` calls `lookupLiveCoordinator(instanceRoot)` (session_registry.go:57-92) to find a live coordinator.
2. **If coordinator is live:** Creates an ask task (no daemon spawn) and writes `task.ask` notification to coordinator's inbox (server.go:717).
3. **If no live coordinator:** Returns `{"status": "no_live_session", ...}` immediately, no task directory created.
4. The coordinator receives the question via `niwa_check_messages` or is interrupted from `niwa_await_task` (handlers_task.go:322-392, with `questionWaiters` channel at line 347-348).
5. Coordinator answers by calling `niwa_finish_task(task_id=<ask_task_id>, outcome=completed, result=answer)`.

**Key finding:** The coordinator is already tracked as a runtime session; workers already know how to find it. There is no loss of identity across task boundaries — the problem is not visibility but **context continuity**.

### 4. Root Cause of Context Loss Between Sequential Tasks

Each `niwa_delegate` spawns a fresh worker process. Evidence from docs/guides/cross-session-communication.md (line 41-45):

> "the daemon sees a new envelope in `.niwa/roles/web/inbox/`, claims it, spawns `claude -p` in the `web` repo directory..."

Each spawned worker:
- Starts with a fresh Claude session (new `.claude/projects/<cwd>/<session_id>.jsonl`)
- Receives a fixed bootstrap prompt: "Call niwa_check_messages to retrieve your task envelope" (DESIGN-coordinator-loop.md line 236)
- Has no knowledge of prior delegations to the same role
- On resume from stall, the daemon injects a reminder but does NOT re-pass prior context from other tasks (DESIGN-coordinator-loop.md lines 231-240)

**The coordinator's problem:** When it delegates `design_task` then `plan_task` sequentially to the same role, the coordinator's own session persists (it stays in memory) but the worker processes are ephemeral. The second worker has no access to the design work from the first worker's session. The coordinator could theoretically pass `{"prior_work": <design output>}` in the `plan_task` body, but there is no standard mechanism for this, and it doesn't solve the shared conversational context loss.

### 5. Coordinator State Model: What Must Persist

The coordinator currently has no explicit session concept. It relies on:
- **CLAUDE.md** for long-term memory across runs
- **In-memory variables** for task IDs and context during a single Claude session
- **Filesystem** (via niwa_send_message, task directories) for inter-process communication

**What the coordinator needs to track:**
- Which tasks belong to which "session" (conceptual grouping)
- Which session maps to which repo/role
- For each session: what is its purpose, its status, did it produce a PR?

**Gap:** There is no coordinator-facing API to:
1. Create a named session anchored to a repo/role
2. Pass session ID to subsequent `niwa_delegate` calls
3. Query "what sessions are active?"
4. End a session with guarantees about cleanup (unpushed worktrees preserved, for example)

### 6. Proposed MCP Tools for Session Lifecycle

Based on the analysis, the coordinator would need these new tools (inferred from the research question):

**Session management:**
```
niwa_create_session(repo, purpose, description="")
  → {session_id, repo, purpose, created_at}
  
niwa_list_sessions()
  → {sessions: [{id, repo, purpose, status, created_at, updated_at}]}
  
niwa_get_session(session_id)
  → {id, repo, purpose, status, created_at, updated_at, tasks: [task_ids]}
  
niwa_end_session(session_id, force=false)
  → {status: "ended" | "blocked_by_unpushed_work", worktree: "preserved"|"cleaned"}
```

**Task delegation within a session:**
```
niwa_delegate(to, body, mode="async", session_id=optional, expires_at=optional)
  → {task_id, session_id} (if session_id was passed)
```

**Storage:** Sessions would be persisted in `.niwa/sessions/<session_id>/` alongside coordinator-session entries in `.niwa/sessions/sessions.json`.

### 7. Coordinator Context Window Loss and Compaction

A critical gap: **If the coordinator is compacted (Claude context window reset), it loses all in-memory session references.**

Current design does not address this. Options:
1. **Write session IDs to CLAUDE.md** — coordinator skill persists and re-reads them on bootstrap
2. **Expose `niwa_list_sessions` in every prompt** — coordinator always has the list available
3. **Embed session metadata in task bodies** — coordinator retrieves it from task envelope responses

Without this, a compacted coordinator cannot know which sessions are active or what work is in progress.

### 8. Worktree Anchoring and Cleanup Guards

The research question asks: "What happens when a session ends without a pushed PR?"

**Current model:** Each worker operates in a single repo clone at `.niwa/roles/<role>/repo/`. All workers for that role share the same checkout. When work switches repos (via a new role), the previous repo stays on whatever branch the last worker left it on (the "stranded repo on feature branch" problem).

**Proposed worktree model:** Each session gets its own worktree under `.niwa/sessions/<session_id>/worktree/`, anchored to the same bare `.git` object store. When a session ends:
- **If work is pushed to a PR:** Worktree can be cleaned up (branch merged or closed).
- **If work is unpushed:** Worktree should be preserved until explicitly cleaned.
- **If coordinator crashes:** Orphaned worktrees remain on disk for recovery.

**Cleanup guard implementation:** `niwa_end_session` would:
1. Check if the session's main branch differs from upstream
2. If different and `force=false`: return `{"status": "blocked_by_unpushed_work", "branches": [...]}` (do not delete worktree)
3. If `force=true`: delete the worktree anyway (user confirms loss)
4. If clean: delete and return `{"status": "ended", "worktree": "cleaned"}`

**Storage:** Per-session state (repo, branch, last_delegated_task_id, etc.) lives in `.niwa/sessions/<session_id>/state.json` alongside the SessionEntry.

## Implications

### New MCP Tools Required
1. **`niwa_create_session(repo, purpose)`** — Create a new session anchored to a repo/role, return session_id
2. **`niwa_list_sessions()`** — List all active coordinator sessions (for context recovery, e.g., after compaction)
3. **`niwa_end_session(session_id, force=false)`** — Clean up a session with guards against loss of unpushed work
4. **Extend `niwa_delegate`:** Accept optional `session_id` parameter so tasks are tagged with their parent session

### Modified Existing Tools
- **`niwa_await_task`:** No change required; it already returns when any task completes. Coordinator can track which task belonged to which session.
- **`niwa_check_messages`:** No change required; workers already send task.progress messages that propagate to coordinator.

### New Infrastructure
- **Session storage:** `.niwa/sessions/<session_id>/` directory with `state.json` (repo, purpose, created_at, tasks, status) and `worktree/` symlink or checkout path.
- **Worktree management:** Daemon must handle per-session worktree creation/deletion on `niwa_create_session` / `niwa_end_session`.
- **Coordinator prompt injection:** Skills need to be updated to create sessions at the start of a multi-task workflow and pass session_id to each `niwa_delegate`.

### Data Structure Changes
**New `SessionState`** (analogous to TaskState):
```json
{
  "v": 1,
  "id": "session-uuid",
  "coordinator_pid": 12345,
  "coordinator_session_id": "claude-session-uuid",
  "repo": "https://github.com/user/repo",
  "purpose": "implement feature X",
  "status": "active" | "ended",
  "worktree_path": ".niwa/sessions/<id>/worktree",
  "created_at": "2026-05-04T...",
  "ended_at": "...",
  "tasks": ["task-id-1", "task-id-2"],
  "last_task_id": "task-id-2"
}
```

### Backward Compatibility
- Existing coordinators using `niwa_delegate` without session_id continue to work (implicit anonymous session per delegation).
- Existing workers that don't reference session IDs are unaffected.
- Worktree layout is opt-in per session; default repo layout remains unchanged.

## Surprises

### 1. Coordinator Already Knows About Itself
The coordinator is already registered as a live session in `sessions.json` when it first calls `niwa_await_task`. Workers can already locate it. The insight: **the visibility/routing problem is solved; the problem is context continuity.**

### 2. Task IDs Are the Primary Unit, Not Sessions
Every operation in niwa is task-scoped: authorization checks use `kindDelegator` + `task_id`, state mutations are per-task. There is no notion of "task groups" or "sessions" anywhere in the MCP or task store layers. Adding sessions means introducing a second grouping layer above tasks.

### 3. Resume Path Already Exists for Workers
Workers' Claude sessions are captured at spawn time (`CLAUDE_SESSION_ID` env var, registered at `server.go:933-947`) and can be resumed on stall kill (DESIGN-coordinator-loop.md Decision 2). **Coordinators don't have this.** If a coordinator is killed, there is no resume mechanism — the session is just gone. This asymmetry suggests sessions may need two different implementations (one for workers, one for coordinators).

### 4. PID-Based Liveness Is Fragile Across Reboots
If a coordinator machine reboots, the PID stored in `sessions.json` becomes meaningless (OS can recycle PIDs). The `IsPIDAlive` check would fail even if the user re-launches the coordinator. Option: extend SessionEntry with a coordinator-session-ID field (already there: `ClaudeSessionID`, types.go:110) and fall back to it when PID check fails.

## Open Questions

### 1. Coordinator Session ID Registration
Should a coordinator write its Claude session ID to SessionEntry when it's registered, for recovery after a coordinator reboot?

**Current state:** `SessionEntry.ClaudeSessionID` field exists (types.go:110) but is not populated by `maybeRegisterCoordinator`. Should it be?

### 2. Should Sessions Be Persistent Across Coordinator Crashes?
If a coordinator crashes mid-session:
- The worktree is left in an inconsistent state
- Tasks may be left in `running` or `queued` state
- The next coordinator to run cannot know the prior context

Options:
- Treat sessions as ephemeral (auto-cleanup on coordinator exit)
- Persist sessions and let next coordinator claim them (needs handoff protocol)
- Require explicit `niwa_end_session` and warn about orphaned sessions

### 3. Single vs. Multiple Parallel Sessions per Coordinator
Can a single coordinator have multiple active sessions simultaneously (e.g., juggling three feature branches in parallel)?

**This design assumes yes**, but the current `maybeRegisterCoordinator` only registers one entry per role. If parallel sessions are needed, the coordinator would need to register multiple SessionEntry objects under different session IDs, not just role-based identity.

### 4. Session-to-PR Tracking
The research mentions session-to-PR tracking as a follow-on feature. How should this work?

**Sketch:** When a session ends successfully (all tasks completed, work pushed), store a link in `.niwa/sessions/<session_id>/pr.json` pointing to the GitHub PR URL. This enables:
- Coordinator to query "what PR came out of this session?"
- Post-session summaries (changes, reviewers, CI status)
- Session history and cross-session analysis

### 5. Coordinator Compaction and Memory
If a coordinator is compacted (context window reset):
- All in-memory task IDs and session references are lost
- Calling `niwa_list_sessions` would recover the list
- But the coordinator's reason for each session (the original goal, the design work from task 1) is only in prior conversation history

Should niwa provide a tool to export session summary? Or should coordinators write summaries to CLAUDE.md explicitly?

### 6. Interaction with Worktree Locking
Git worktrees have a built-in locking mechanism (`git worktree lock`). Should niwa use this to prevent concurrent writes to the same session's worktree?

### 7. Mesh State (.niwa/) Interaction with Worktrees
With multiple worktrees, `.niwa/` is shared (lives in the main checkout, not the worktree). Who owns the inbox, tasks, and sessions state?

**Current assumption:** Shared `.niwa/` per repo; each worktree has its own symlink or mount to it. But this creates race conditions if two sessions write to `.niwa/roles/<role>/inbox/` simultaneously.

**Alternative:** Per-worktree `.niwa/`, with one "canonical" `.niwa/` that holds shared state (sessions, coordinator roles). This is complex and fragile.

## Summary

The coordinator already has live routing to workers via `sessions.json`, but each `niwa_delegate` spawns a fresh worker process that loses the prior context. A proper session lifecycle requires three new MCP tools (`niwa_create_session`, `niwa_list_sessions`, `niwa_end_session`), per-session worktree management, and persistence of session state in `.niwa/sessions/<session_id>/`. The biggest open question is whether sessions survive coordinator crashes and how a new coordinator claims an orphaned session, since current registration is PID-based and breaks on reboot.
