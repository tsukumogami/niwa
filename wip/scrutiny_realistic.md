# Realistic-application scrutiny: cross-session-communication

Severity codes: real (will hit), likely (probably hits), unlikely (edge case).

## 1. Audit log + on-disk artifact growth

**1a. `mcp-audit.log` — no rotation.** *likely.* Append-only, no `niwa mesh audit prune`. ~90 MB/year/instance at 1000 calls/day. Decision 3 explicitly accepts. **Mitigation:** ship a prune subcommand or accept and document `truncate -s 0`.

**1b. `ReadAuditLog` reads whole file into memory** (`audit_reader.go:38`). *unlikely today, real once exposed.* Test-only consumer at present. At 90 MB this is ~150 MB heap. **Mitigation:** add a streaming `Walk(filter, fn)` API before any user-facing reader ships.

**1c. Per-task `transitions.log` retention is the real leak.** *real.* Each completed task keeps its full directory + bodies forever. After 1000 tasks: 1000 dirs, multi-MB. `handleListOutboundTasks` walks them all per call. **Mitigation:** add `niwa task gc --older-than 7d`.

**1d. Per-task `stderr.log` unbounded.** *likely.* No rotation; chatty workers on long tasks dominate disk faster than the audit log. **Mitigation:** size cap + truncate at terminal transition.

## 2. fsnotify watchers — no dropped-event recovery

*likely under burst.* Both daemon (`mesh_watch.go:458`) and per-session MCP server (`watcher.go:58`) only react to `Create`, swallow `watcher.Errors` silently, and have no periodic resync after the startup catch-up scan. Linux `fs.inotify.max_queued_events` default 16 384; a burst (e.g., a `niwa_check_messages` storm or a flurry of progress messages) can overflow. Overflowed events vanish — `task.completed` notifications are lost — and `niwa_await_task` hangs to its 600 s timeout. **Mitigation:** 30 s periodic resync scan in both watchers; surface `fsnotify.Errors` at WARN with a counter.

## 3. `niwa_await_task` 600 s default

*real.* A 20-minute task exceeds the default. The handler returns `{"status":"timeout","current_state":"running"}` (`handlers_task.go:300`); coordinator does not crash. **But** whether the LLM correctly re-awaits depends on prompt heuristics, and `SKILL.md` doesn't script the re-await loop. UX risk: coordinator gives up or hallucinates. **Mitigation:** raise default to 1800 s (matches stall watchdog), or add an explicit re-await example to the skill.

## 4. Retry-cap user-facing signal

*likely.* `handleSupervisorExit` writes `state=abandoned reason=retry_cap_exceeded` and best-effort-delivers `task.abandoned` to the delegator. Worker stderr lives at `~/.niwa/tasks/<id>/stderr.log` — unreferenced by `niwa task show`. Users see "abandoned" with no easy diagnostic. **Mitigation:** include a `stderr_tail` (last 500 B) in the abandon reason, or have `niwa task show` print the tail.

## 5. Restart safety after `kill -9`

*real, mostly handled.* `reconcileRunningTasks` (`mesh_watch.go:1673`) re-classifies into orphan / fresh-retry / dead. Half-claimed envelopes in `inbox/in-progress/` are decorative once `state.json` has moved past `queued` — state is authoritative. Kernel releases the flock on `kill -9`, so `daemon.pid.lock` recovers. `daemon.pid` stays stale on disk until rewritten, but `EnsureDaemonRunning` validates via `IsPIDAlive`. **Accept and document.**

## 6. `awaitWaiters` lost on coordinator crash

*real.* In-memory map (`server.go:66`); coordinator's `claude` crashing nukes it. Worker's eventual `niwa_finish_task` still writes terminal state and delivers `task.completed` to the coordinator inbox. New coordinator starts cold — must call `niwa_check_messages` or `niwa task show` to learn the result. No deadlock, no automatic resumption. **Mitigation:** document; ensure `niwa-mesh` skill prompts the LLM to scan `niwa_list_outbound_tasks` after fresh starts.

## 7. Bootstrap-prompt canary

*likely over model lifecycle.* Prompt is a string constant (`mesh_watch.go:85`). A future Claude that changes its tool-call heuristics could silently stop calling `niwa_check_messages`; the worker exits, daemon retries to cap, surfaces as `retry_cap_exceeded` with no "the LLM didn't follow instructions" diagnostic. The two `@channels-e2e` scenarios catch this only when run with `ANTHROPIC_API_KEY` against the model users actually use. **Mitigation:** make `@channels-e2e-bootstrap` a release-gate; emit a daemon WARN line tagged "worker exited before any niwa_* tool call" by cross-referencing the audit log per `task_id`.

## 8. Other concerns

- **acceptEdits + arg-keys-only audit.** *likely.* Injected `body={"goal":"rm -rf"}` audits as `arg_keys=["body","to"]`. Forensics impossible. Consider opt-in `NIWA_AUDIT_FULL_ARGS=1`.
- **Env cross-pollination.** `ANTHROPIC_API_KEY` reaches every worker. Documented gap.
- **`NIWA_WORKER_SPAWN_COMMAND` shell-rc poisoning.** WARN-log per spawn when override is active.
- **Disk-full.** `state.json` tmp+rename safe; `transitions.log` appends fail silently. Accept.
- **Two coordinators, one instance.** No arbitration; awaiter races surprise. Out of scope per PRD.

---

## Summary

Correctness is solid: flock + atomic rename + reconciliation handle the hard crash cases. The real-world tax is **operability** — no rotation/GC for audit / transitions / stderr logs, no fsnotify resync, no diagnostic when the bootstrap silently fails, 600 s await mismatched to real tasks. None are correctness bugs; all are felt by week two.
