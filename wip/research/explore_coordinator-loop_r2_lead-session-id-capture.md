# Lead: Session ID capture for resume restart path

## Findings

**Session ID is discovered for coordinators, not workers:**
- `DiscoverClaudeSessionID()` in `internal/mcp/session_discovery.go` implements a three-tier discovery mechanism:
  1. **Tier 1:** `CLAUDE_SESSION_ID` environment variable (regex-validated)
  2. **Tier 2:** PPID walk of `~/.claude/sessions/<pid>.json` (two levels, with cwd cross-check)
  3. **Tier 3:** Project directory scan of `~/.claude/projects/<base64url-cwd>/*.jsonl` by mtime descending
- This function is called only in `internal/cli/session_register.go:runSessionRegister()` (line 53)
- Session ID is stored in `SessionEntry.ClaudeSessionID` (types.go line 110) in the coordinator-session registry at `.niwa/sessions/sessions.json`
- The comment on types.go line 98 explicitly states: "Workers are NOT registered; only coordinators (PRD R39, R40)"

**Worker session ID is never captured:**
- `TaskState` struct (types.go line 261–276) has no `ClaudeSessionID` or similar field
- `TaskWorker` struct (types.go line 238–244) has PID, StartTime, Role, SpawnStartedAt, AdoptedAt — but no session ID
- When `spawnWorker()` launches a worker process (mesh_watch.go line 835), no session ID discovery occurs
- The worker's spawn command includes only: `claude -p "<prompt>" --permission-mode --mcp-config --strict-mcp-config --allowed-tools`
- The spawn does not set a `CLAUDE_SESSION_ID` environment variable that could be discovered post-hoc

**No mechanism for workers to self-report session ID:**
- MCP server (internal/mcp/server.go) implements 11 tools: `niwa_check_messages`, `niwa_send_message`, `niwa_ask`, `niwa_delegate`, `niwa_query_task`, `niwa_await_task`, `niwa_report_progress`, `niwa_finish_task`, `niwa_list_outbound_tasks`, `niwa_update_task`, `niwa_cancel_task`
- None of these tools accept or store a Claude Code session ID
- There is no `niwa_register_session` or `niwa_report_session_id` tool
- Workers cannot call an MCP tool to register their session ID with the daemon

**Session ID discovery at restart time is infeasible under current architecture:**
- When the stall watchdog fires and kills a worker PID, niwa has:
  - The task ID
  - The worker PID (about to be killed)
  - The worker's role
  - The task's directory at `.niwa/tasks/<task-id>/`
- To recover a session ID at kill time, niwa would need to:
  1. Query the killed process (SIGTERM target) for its session ID — but SIGKILL terminates it immediately
  2. Infer the session ID from the worker PID — requires walking `/proc/<pid>/cmdline` or similar, which only reveals the spawn argv, not the session ID
  3. Discover the session ID post-mortem from `~/.claude/projects/<cwd>/*.jsonl` — this requires waiting after the kill, finding the working directory from `NIWA_INSTANCE_ROOT + roles/<role>/` (state.json has no explicit cwd field), base64url-encoding it, then listing project files by mtime
- Option 3 is racy (session files may be written after daemon queries, mtime ordering is unreliable) and requires knowledge of the worker's cwd, which is not reliably stored in TaskState

**Coordinator sessions store session ID; workers do not:**
- SessionEntry (types.go line 102–111) has `ClaudeSessionID` and is used only for coordinators in `niwa session register`
- The session registry at `.niwa/sessions/sessions.json` is read in `session_registry_ask_test.go` to determine if a coordinator is available for `task.ask` delegation
- No parallel TaskState field or worker-session registry exists for worker processes

## Implications

1. **The resume-with-reminder path requires session ID at restart time, but niwa does not capture it:**
   - To execute `claude --resume <session_id> -p "<reminder>"` on a stall kill, the daemon must know the session ID of the killed worker
   - Currently, niwa spawns workers fresh and never learns their session ID
   - The only place worker session IDs could be discovered is `~/.claude/projects/<cwd>/*.jsonl` (Tier 3 of `DiscoverClaudeSessionID`), but this requires the daemon to infer the worker's cwd and scan the filesystem post-kill, which is racy and fragile

2. **Session ID discovery is designed for coordinators, not workers:**
   - `DiscoverClaudeSessionID()` and the three-tier mechanism were built for `niwa session register` (coordinator startup)
   - The function walks the PPID chain to find the Claude Code process that launched the hook script that launched niwa
   - For a worker spawned by the daemon (not by Claude Code directly), the PPID is the daemon, not Claude Code — the walk fails
   - Tier 2 (PPID walk) cannot work for workers; only Tier 3 (project scan) could potentially discover a worker's session ID, and it's unreliable

3. **To implement resume-with-reminder, niwa must capture session ID at spawn time:**
   - The daemon must either:
     a. Inject `CLAUDE_SESSION_ID` into the worker's environment at spawn time, or
     b. Add an MCP tool that workers call on startup (e.g., `niwa_register_worker_session`) to report their session ID, or
     c. Monitor the worker process's stdout for a session-ID announcement at startup
   - Option (a) is impossible: the session ID doesn't exist until Claude Code creates the session (after the daemon calls `cmd.Start()`)
   - Option (b) requires adding a new tool and updating TaskState/TaskWorker to store the reported ID
   - Option (c) requires parsing Claude Code's startup output, which may not be stable across versions

4. **The architecture assumes workers are ephemeral and not resumable:**
   - The current design spawns fresh workers with `-p` and no session tracking
   - TaskState is optimized for task-level restarts (restart_count, max_restarts), not session-level resumption
   - To support resume-with-reminder, the task state must be extended to include a worker session ID field, and a mechanism to populate it must be added

## Surprises

1. **Session ID discovery is asymmetric:** Coordinators (humans, launched by Claude Code) have their session ID discovered via `DiscoverClaudeSessionID()`, but workers (launched by the daemon, in a subprocess context) cannot use the same mechanism because their PPID chain is broken.

2. **No self-registration mechanism for workers:** Despite MCP being a bidirectional protocol, workers have no tool to announce their session ID to the daemon. The MCP tools are one-way: workers call daemon-implemented tools, but the daemon cannot ask workers to report back on demand.

3. **Coordinator sessions and worker sessions are treated differently:** The session registry (`sessions.json`) only stores coordinator sessions, used for `task.ask` delegation. Workers exist only as PID + StartTime + Role tuples in TaskState. This asymmetry means the framework for resuming sessions (which exists for coordinators) does not extend to workers.

4. **The project directory scan (Tier 3) is the only option for worker session discovery, and it's fragile:** Post-mortem session ID recovery via `~/.claude/projects/<cwd>/*.jsonl` requires knowing the worker's cwd, guessing the base64url encoding, and trusting mtime order — all of which are implementation details not guaranteed by Claude Code's contract.

## Open Questions

1. **Should TaskState.Worker be extended to include ClaudeSessionID?** If so, when and how is it populated?

2. **Should workers call an MCP tool on startup to register their session ID?** This would require a new tool like `niwa_register_worker_session(session_id)` and TaskState schema changes.

3. **Can the daemon safely discover a worker's session ID from its stdout during startup?** Would Claude Code emit a standardized session-ID message on startup that niwa could parse?

4. **If session ID discovery fails (worker doesn't report, or stdout parsing fails), should the daemon fall back to fresh spawn or attempt post-mortem recovery?**

5. **What is the cwd of a spawned worker task?** TaskState does not store it explicitly. Should it be recorded in state.json for later Tier-3 scans?

## Summary

Niwa currently captures Claude Code session IDs for coordinators via `DiscoverClaudeSessionID()` and stores them in the session registry, but workers are not captured: they are spawned fresh and their session ID is never recorded. Implementing the resume-with-reminder restart path requires either extending TaskState to store worker session IDs (populated by a new MCP tool or stdout parsing) or accepting post-mortem discovery from the `~/.claude/projects/` directory (fragile). The asymmetry between coordinator and worker session handling suggests a gap in the architecture.
