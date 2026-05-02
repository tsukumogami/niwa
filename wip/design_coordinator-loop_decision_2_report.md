# Decision 2: Session ID Capture and Resume-with-Reminder Path

## Question

How does niwa capture the worker's Claude Code session ID for resume-with-reminder recovery — via a new MCP tool called during bootstrap, by extending `niwa_report_progress` to accept the session ID on first call, or via post-mortem filesystem discovery — and how does the watchdog use that ID to resume rather than fresh-spawn?

## Chosen: Option A — New MCP tool `niwa_register_worker_session`

The worker calls `niwa_register_worker_session` once during bootstrap, before beginning real work, passing its Claude Code session ID as a required parameter. The daemon stores the value in a new `TaskState.Worker.ClaudeSessionID` field. When the stall watchdog fires and `Worker.ClaudeSessionID` is non-empty, the daemon checks the session file's integrity before deciding whether to resume or fall back to fresh spawn.

Session ID capture during bootstrap is the only approach that is both agent-cooperative and owned entirely by the niwa control plane. The bootstrap prompt already instructs the worker to call `niwa_check_messages` as its first action; a second mandatory call to `niwa_register_worker_session` extends that same contract without changing the call site in `spawnWorker`. The daemon writes `Worker.ClaudeSessionID` into `state.json` under the task flock using the existing `UpdateState` discipline, so the field is visible to the watchdog immediately and survives daemon restarts via crash reconciliation.

When the stall watchdog fires, the watchdog goroutine in `runWatchdog` records the session ID from the most recently read `TaskState.Worker.ClaudeSessionID` before calling `escalateSignals`. After SIGTERM (and SIGKILL if needed) the supervisor goroutine signals `waitDone` and the central loop calls `handleSupervisorExit`. In `handleSupervisorExit`, when scheduling a retry via `retrySpawn`, the path checks whether a session ID was captured and whether the session file is valid. If both conditions hold, `spawnWorker` is called with a resume flag set, causing it to build the `claude --resume <session_id> -p "<reminder>"` invocation rather than the plain `-p <bootstrap>` invocation. The reminder message is: `"You were stopped by the stall watchdog because niwa_report_progress was not called within the required interval. Call niwa_report_progress with your task ID now, then continue your work."` If the session ID is absent or the file integrity check fails, `spawnWorker` falls back to the standard fresh-spawn path unchanged.

The integrity check before a resume attempt reads the `.jsonl` file at `~/.claude/projects/<base64url-cwd>/<session_id>.jsonl`. A SIGTERM-only termination leaves a complete final line; SIGKILL may leave a partial line. The check verifies: (1) the file exists, (2) it is non-empty, (3) the last 4 KB can be read without a read error, and (4) the last complete line (terminated by `\n`) parses as valid JSON. A truncated final line that fails JSON parse is accepted as long as a preceding complete line validates — the resume mechanism in Claude Code is tolerant of a torn trailing line. If no valid line is found at all, the file is treated as corrupt and the daemon falls back to fresh spawn. This check is intentionally lightweight: it does not parse the full conversation history, only validates that the file is usable.

The infinite-resume loop is capped by a new `TaskState.Worker.ResumeCount` field. The watchdog fires → resume is counted toward `ResumeCount`, not `RestartCount`. A separate cap `MaxResumes` (default 2) is stored alongside `MaxRestarts` in `TaskState`. When `ResumeCount >= MaxResumes`, the next watchdog firing triggers a fresh spawn (resetting `ResumeCount` to 0, incrementing `RestartCount`). The combined retry budget is `MaxResumes` resume attempts per fresh-spawn attempt, up to `MaxRestarts` fresh-spawn attempts. A task that resumes twice, is killed a third time, then fresh-spawns twice and resumes twice more before completing uses: `RestartCount=1`, `ResumeCount` per-attempt cycle. This keeps the existing `restart_count` / `max_restarts` semantics visible to the delegator via `niwa_await_task` unchanged, while the resume sub-cycle is an implementation detail of the daemon restart path.

## Alternatives Considered

**Option B — Extend `niwa_report_progress` to accept optional session ID**: The worker passes its session ID on the first `niwa_report_progress` call. Rejected because it couples a correctness-critical registration event to a best-effort behavioral call. Workers that call `niwa_report_progress` early and often are exactly the workers that don't need resume recovery; the registration event is structurally distinct from progress reporting and belongs in a dedicated bootstrap step. Coupling them produces a confusing dual-role API and leaves a gap for workers that never call `niwa_report_progress` before the first watchdog firing (which is the exact failure mode resume is designed to recover from).

**Option C — Post-mortem discovery from `~/.claude/projects/`**: After the watchdog kills the worker, scan `~/.claude/projects/<base64url-cwd>/` by mtime to find the most recent session file. Rejected for three reasons. First, the worker's CWD is not currently stored in `TaskState` — it is computed at spawn time by `resolveRoleCWD` but never persisted, so the daemon would need a separate lookup at resume time. Second, mtime scanning is inherently racy: if another Claude Code process runs in the same directory (a coordinator or a second task for the same role), the most recent `.jsonl` by mtime may not belong to the killed worker. Third, the discovery happens after the kill, adding latency to the already time-sensitive restart path and creating a window where the filesystem is in a partially-written state after SIGKILL. Option A's in-process registration avoids all three problems and reuses the already-validated `sessionIDRegex` from `session_discovery.go`.

## Required Schema Changes

**`TaskWorker` struct** (in `internal/mcp/types.go`):

- Add `ClaudeSessionID string \`json:"claude_session_id,omitempty"\`` — populated by `niwa_register_worker_session` via `UpdateState`. Zeroed on each fresh spawn (not on resume) so the watchdog always sees the most recently registered session ID.
- Add `ResumeCount int \`json:"resume_count,omitempty"\`` — incremented by the daemon on each resume-spawn attempt. Reset to 0 on each fresh spawn.

**`TaskState` struct** (in `internal/mcp/types.go`):

- Add `MaxResumes int \`json:"max_resumes,omitempty"\`` — defaulting to 2 when zero. The daemon writes this when seeding `state.json` in `createTaskEnvelope`, matching the pattern already used for `MaxRestarts`.

No changes to `TransitionLogEntry`, `TaskEnvelope`, or `SessionEntry`. The schema changes are all `omitempty` so existing `state.json` files without these fields continue to parse correctly — zero values produce the correct fallback behavior (no session ID → fresh spawn; `MaxResumes=0` → default of 2).

## Required New Tools or Modified Tools

**New tool: `niwa_register_worker_session`**

- Input schema: `{ "task_id": string (required), "session_id": string (required) }`.
- Authorization: `kindExecutor` — same check as `niwa_report_progress`.
- Handler validates `session_id` against the existing `sessionIDRegex` from `session_discovery.go`. On validation failure returns `BAD_PAYLOAD` with detail; does not store the value.
- On success, calls `UpdateState` to write `Worker.ClaudeSessionID = session_id` into `state.json`.
- Returns `{"status":"registered","task_id":"..."}`.
- Idempotent: calling it again with the same session ID is a no-op; calling it with a different session ID overwrites the stored value (handles the resume path where Claude Code may begin a new sub-session after `--resume` in edge cases, though this is not expected in practice).
- Registered in `server.go::toolsList()` and `callTool()` switch alongside the existing 11 tools.

**Bootstrap prompt update** in `mesh_watch.go::bootstrapPromptTemplate`:

Changed from: `"You are a worker for niwa task %s. Call niwa_check_messages to retrieve your task envelope."`

Changed to: `"You are a worker for niwa task %s. Call niwa_register_worker_session with your task ID and your current Claude session ID first, then call niwa_check_messages to retrieve your task envelope. You must call niwa_report_progress at least every 10 minutes while working or the watchdog will stop you."`

The session ID is available to the worker as `$CLAUDE_SESSION_ID` in its environment (set by Claude Code itself) or discoverable via the standard `DiscoverClaudeSessionID` mechanism. The worker agent reads this from its environment and passes it to the tool.

No changes to existing tool schemas.

## Resume Path Design

**When session ID is available and session file is valid:**

In `retrySpawn`, after reading `TaskState`, check `cur.Worker.ClaudeSessionID`. If non-empty and `cur.Worker.ResumeCount < resolvedMaxResumes(cur)`:

1. Run the session file integrity check against `~/.claude/projects/<base64url-cwd>/<session_id>.jsonl` using the worker's stored CWD (see assumption below).
2. If integrity check passes: call `spawnWorker` with a `resumeMode=true` flag. `spawnWorker` builds the command as: `claude --resume <session_id> -p "<reminder>" --permission-mode=<mode> --mcp-config=<path> --strict-mcp-config --allowed-tools <tools>`. Increment `Worker.ResumeCount`, zero `Worker.PID` and `Worker.StartTime` (to be backfilled after `cmd.Start`), set `Worker.SpawnStartedAt=now`. Do NOT increment `RestartCount`.
3. Log: `retry task=<id> attempt=resume resume_count=<n> session_id=<id>`.

**When session ID is absent, file is missing, or integrity check fails:**

Call `spawnWorker` with `resumeMode=false` (existing behavior unchanged). Reset `Worker.ResumeCount=0` and increment `RestartCount` as today. Log: `retry task=<id> attempt=fresh_spawn restart_count=<n> reason=<no_session_id|file_missing|file_corrupt>`.

**When `ResumeCount >= MaxResumes`:**

Fall through to fresh spawn immediately, no integrity check needed. Reset `Worker.ResumeCount=0`.

**Reminder message** (constant in `mesh_watch.go`):

```
You were stopped by the stall watchdog because niwa_report_progress was not called within the required interval. Call niwa_report_progress with your task ID now, then continue your work. Subsequent failures to call niwa_report_progress regularly will trigger another watchdog stop.
```

**Transitions.log entries for resume:**

The existing `spawn` entry kind covers both fresh and resume spawns. A `resume=true` boolean field is added to the `TransitionLogEntry` for the spawn entry (JSON: `"resume":true`). This is backward-compatible: existing readers that don't know the field ignore it; new readers can distinguish resume attempts from fresh attempts for diagnostics.

## Assumptions

1. **Worker CWD is derivable at resume time.** The watchdog needs the worker's CWD to locate the session file for the integrity check. `resolveRoleCWD(instanceRoot, role)` is called at spawn time but the result is not stored in `TaskState`. The implementation must either (a) call `resolveRoleCWD` again at resume time using `Worker.Role` (acceptable since the role's repo directory does not change during a task's lifetime), or (b) store the resolved CWD in `TaskState.Worker` as a new `CWD string` field. Option (a) is preferred to avoid an additional schema field.

2. **`claude --resume` accepts `--strict-mcp-config`.** The integrity of the MCP isolation for resume workers is assumed to work the same as for fresh workers. The worker MCP config file is regenerated per spawn (including resume spawns) in `spawnWorker` using `WorkerMCPConfig`, so the `--mcp-config` path always points to a fresh per-task config.

3. **`CLAUDE_SESSION_ID` is available in the worker's environment.** Claude Code is assumed to set `CLAUDE_SESSION_ID` in the worker's process environment. If not, the worker must use the PPID-walk or project-scan tiers of `DiscoverClaudeSessionID`. The bootstrap prompt instructs the worker to pass its session ID; the mechanism by which the worker discovers it is a Claude Code runtime detail outside niwa's control.

4. **Partial JSONL line after SIGKILL does not block `--resume`.** `claude --resume` is assumed to be tolerant of a truncated final line in the session `.jsonl`, as long as preceding lines are valid. This is stated in the background but is an assumption about Claude Code's internal behavior.

5. **`MaxResumes` default of 2 is appropriate.** Two resume attempts per fresh-spawn attempt gives the worker two chances to self-correct before a heavier fresh spawn. This is a tunable default, not a hard design constraint.

## Confidence

High — the chosen approach maps cleanly to existing patterns (`niwa_register_worker_session` follows the same authorization and `UpdateState` discipline as all other worker-side tools), the schema changes are forward-compatible, and the failure modes all have explicit fallbacks to the existing fresh-spawn path.
