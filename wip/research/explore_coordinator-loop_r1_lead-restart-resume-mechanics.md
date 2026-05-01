# Lead: Task restart and resume mechanics

## Findings

**Restart Policy (niwa-owned):**
- Tasks are initialized with `MaxRestarts: 3` (hardcoded constant, lines 194 and 270 of `handlers_task.go`)
- This sets a cap of 3 restarts = 4 total attempts per task
- When a worker exits without calling `niwa_finish_task`, the daemon classifies this as an "unexpected exit" and increments `RestartCount`
- After `RestartCount >= MaxRestarts`, the daemon transitions the task to `abandoned` with `reason: "retry_cap_exceeded"`
- Backoff intervals are 30s, 60s, 90s (hardcoded in the daemon, configurable at test time via `NIWA_RETRY_BACKOFF_SECONDS`)
- This policy is documented in PRD-cross-session-communication.md (R34) and the operational guide (cross-session-communication.md, "Stall watchdog" and "Crash recovery" sections)

**Resume Mechanism (Claude Code-owned, not niwa):**
- The bug report mentions `claude --resume <session_id>` — this is a **Claude Code feature**, not a niwa mechanism
- Niwa does not implement or understand `--resume`. Workers are spawned fresh each time: `claude -p "<bootstrap_prompt>" --permission-mode=acceptEdits --mcp-config=... --strict-mcp-config`
- The bootstrap prompt is fixed: "You are a worker for niwa task <task-id>. Call niwa_check_messages to retrieve your task envelope." (lines 836 and 72 of mesh_watch.go)
- Workers retrieve their task body via `niwa_check_messages` on their first MCP call, not from argv (decision rationale in PRD R15 and PRD-cross-session-communication.md "Delivering task envelope via inbox, not argv")

**Checkpoint/Resume from Filesystem State (application-owned, not niwa):**
- Niwa does **not** mandate or document that workers use `wip/` files as checkpoints
- The skill content (buildSkillContent() at line 662 of channels.go) does not mention `wip/` or filesystem-based checkpoints
- Niwa's design assumes workers are idempotent by contract: if a worker is restarted (killed mid-task and respawned), the new worker instance can re-run from scratch or re-query application state
- The PRD describes completion as explicit (`niwa_finish_task`), not as a process-exit side effect, which allows safe restarts without lost work
- If the worker application (e.g., shirabe) writes state to `wip/` and checks it on startup, that is an **application-level concern**, not a niwa concern
- Niwa does not provide mechanism or documentation for workers to detect "I was restarted" or to resume from application checkpoints

**No `max_restarts` configuration in this version:**
- `MaxRestarts` is hardcoded to 3 in handlers_task.go (lines 194, 270)
- There is no per-task or per-workspace override mechanism in v0.9.4
- The PRD mentions a future `[channels.mesh].retry_cap` configuration option (PRD R34, configuration defaults table) but this is not implemented in v0.9.4

## Implications

The restart/resume behavior in the bug report **worked as designed**, but the design is narrow and does not cover the application's assumptions:

1. **Niwa's restart contract:** When a worker exits unexpectedly (stall watchdog SIGTERM/SIGKILL or crash), niwa spawns a replacement worker from scratch. The new process sees the same task ID and the same task envelope. There is no implicit state carryover or "resume from checkpoint" — the new worker must re-initialize by calling `niwa_check_messages` and reading the original task body.

2. **The application's checkpoint strategy (wip/ files):** If the worker application (shirabe) wrote `wip/` files during session 1–3 and session 4 found those files still present, that means:
   - The worker application **deliberately persisted state** outside niwa's task system (likely to disk in the role's repository)
   - On restart, the application checked for the presence of those files and resumed work from that checkpoint
   - This is an **application-level idiom**, not a niwa-provided mechanism

3. **Claude Code's `--resume` flag:** When the bug reporter restarted the worker manually using `claude --resume <session_id>`, they were resuming the **Claude Code session's conversation context** — not any niwa-managed state. This is orthogonal to the task restart mechanism. The resumed session would have the same `NIWA_SESSION_ROLE` and `NIWA_TASK_ID` env vars (baked into the `.mcp.json` that niwa generates), allowing it to continue talking to the same task.

4. **The 390 KB vs 1–1.3 MB difference:** Session 4's smaller file size reflects that the worker did not re-run the full workflow; it found `wip/` artifacts from prior sessions and skipped duplicate work. This is the application's idempotency, not niwa's.

## Surprises

1. **`MaxRestarts` is hardcoded, not configurable:** The PRD (R34, Configuration Defaults table) names `[channels.mesh].retry_cap` and `--retry-cap` as future configuration paths, but neither is implemented in v0.9.4. Every task gets exactly 3 restarts. This is not documented in the operational guide or skill content.

2. **No "I am being restarted" signal:** Niwa does not provide a mechanism for workers to detect whether this is their first attempt or a restart. The worker application must infer this from external state (presence of `wip/` files, git branches, database records, etc.).

3. **Skill content does not mention filesystem checkpointing:** The niwa-mesh skill (lines 645–766 of channels.go) describes the task lifecycle, completion contract, and progress reporting, but does **not** recommend or document the pattern of writing to `wip/` and checking for it on restart. This is left entirely to the application.

4. **The daemon spawns fresh, not resumed sessions:** Workers are always spawned fresh with `claude -p`, never with `claude --resume`. The `--resume` flag in the bug report was applied by the coordinator (human operator), not by niwa. If a worker had exited and the coordinator used `--resume` to bring it back manually, that coordinator session would still see the same task ID in its env, but the worker is a **new Claude Code session**, not a continuation of the prior one.

## Open Questions

1. **Is there a documented pattern for application-level idempotency in shirabe or other agents?** The niwa skill does not mention checkpoint strategies; if applications like shirabe rely on `wip/` files as a standard pattern, should this be elevated to niwa documentation or the skill?

2. **Should `MaxRestarts` be configurable?** The PRD names configuration paths but they're not implemented. Is this a v2 feature, or should v0.9.4 support per-task or per-workspace overrides?

3. **Should niwa provide a "restart detection" signal?** If multiple applications need to know "I am being restarted," should niwa inject an env var like `NIWA_ATTEMPT_NUMBER` or `NIWA_IS_RESTART` for workers to check?

4. **What is the relationship between `claude --resume` (Claude Code feature) and task restart (niwa feature)?** The bug report uses both; should there be clearer documentation distinguishing them?

## Summary

Niwa's task restart mechanism works as designed: after an unexpected worker exit (up to 3 restarts = 4 attempts), the daemon spawns fresh workers that retrieve the task body and proceed. The daemon does not persist or restore application-level state; checkpoint/resume from `wip/` files is an **application-level idiom** (likely a shirabe pattern), not a niwa feature. The 390 KB final session was smaller than the 1–1.3 MB killed sessions because the application's checkpoint logic allowed it to skip re-running work — this is application idempotency, not a property of niwa's restart system. The `--resume` flag mentioned in the report is a Claude Code feature (for resuming coordinator sessions), orthogonal to task restart.

