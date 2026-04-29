<!-- decision:start id="coordinator-session-registration" status="assumed" -->
### Decision: Coordinator Session Registration for Live Ask Routing

**Context**

`handleAsk` in `internal/mcp/server.go` currently creates a first-class task with `body.kind="ask"` and waits for the daemon to spawn a worker. When a coordinator is actively running, it never receives these questions — the daemon spawns an ephemeral `claude -p` instead, silently bypassing the live session. Fixing this requires `handleAsk` to detect a live coordinator session in `sessions.json` and route the ask there instead of to the daemon's spawn path.

The question is how that registration happens. Three options exist: auto-register on first MCP call, require manual `niwa session register` before use, or lazily register on the first `niwa_await_task` or `niwa_check_messages` call.

The existing hook infrastructure (`mesh-session-start.sh` running `niwa session register`, and `mesh-user-prompt-submit.sh` running `niwa session register --check-only`) already fires on every Claude Code session start and user prompt. The MCP server has `NIWA_SESSION_ROLE` from its env block at startup and the current PID is always available. `IsPIDAlive(pid, startTime)` is already used in `writeSessionEntry` and `isAlreadyRegistered`.

**Assumptions**

- The `session_start` and `user_prompt_submit` hooks fire reliably when Claude Code starts and on each prompt. If a coordinator starts without these hooks installed (pre-apply workspace or manual launch outside the normal flow), the hook-based path fails silently.
- Assumed: coordinators always call either `niwa_await_task` or `niwa_check_messages` before they would receive any ask question that needs routing. If a coordinator never calls either tool, lazy registration still covers the case where the coordinator is actively blocking on `niwa_await_task`.
- The `NIWA_SESSION_ROLE` env var is set to `"coordinator"` in `.mcp.json` for the coordinator session, making it detectable at MCP startup.

**Chosen: Lazy registration on first niwa_await_task or niwa_check_messages call**

When the MCP server processes a `niwa_await_task` or `niwa_check_messages` call and `s.role == "coordinator"`, it checks whether a live session is already registered for the coordinator role. If not, it writes a `SessionEntry` to `sessions.json` using the current PID (obtained via `os.Getpid()`) and start time (via `mcp.PIDStartTime`). The Claude session ID is discovered via `mcp.DiscoverClaudeSessionID` if available. This registration is a side effect of the tool call and transparent to the caller.

The implementation touches `handleAwaitTask` and `handleCheckMessages` in `server.go` (or a shared `maybeRegisterCoordinator` helper), plus `handleAsk` which reads `sessions.json` and calls `IsPIDAlive` to decide whether to queue into the coordinator inbox or create a spawn task. This stays well within the constraint of changes only to `handleAsk` and the session registry path.

**Rationale**

The constraint "coordinators who skip registration must not silently get questions routed to spawn instead of to them" is satisfied because `niwa_await_task` and `niwa_check_messages` are the only two ways a coordinator participates in the mesh. A coordinator that never calls either tool has no mechanism to receive questions anyway — routing to spawn in that case is the correct fallback. Lazy registration therefore provides the same coverage as auto-registration on first MCP call with no additional surface area.

Auto-registration on first MCP call registers earlier (on `initialize` or `tools/list`) but those calls happen before the coordinator has signaled any intent to participate. The PID is the same at that point, but the registration would occur for any MCP client that connects to the niwa server — including tooling that probes the server for capability discovery without running a coordinator session. Lazy registration ties registration to an intentional coordinator action.

Explicit pre-registration via the manual CLI step is already provided by the hook infrastructure, but hooks can fail or be skipped. The constraint rules out relying solely on it: a coordinator whose hook fires but produces no effect (e.g., the hook script is called in a non-coordinator working directory and `deriveRole` returns a non-coordinator name) would be invisible to `handleAsk`. Lazy registration is self-contained inside the MCP server and uses `s.role` (already correctly set from `NIWA_SESSION_ROLE`), making it immune to hook environment issues.

The scope constraint — no changes outside `handleAsk` and the session registry — is met. The lazy registration helper reads `sessions.json`, calls `IsPIDAlive`, and writes via `writeSessionEntry` (or a thin equivalent). `handleAsk` adds a registry lookup before the task-creation path.

**Alternatives Considered**

- **Auto-register on first MCP call**: Register when any MCP method is received (`initialize`, `tools/list`, `tools/call`). Rejected because it registers on capability probes that don't indicate an active coordinator session. The coordinator role check (`s.role == "coordinator"`) would guard against non-coordinator sessions, but the timing is earlier than necessary and couples registration to protocol negotiation rather than to coordinator behavior.
- **Require explicit pre-registration via CLI**: Keep `niwa session register` as the sole registration path and rely on the installed hooks. Rejected because the constraint explicitly rules it out: a coordinator who skips it must not silently get questions routed to spawn. Hooks can fail, be missing in older workspaces, or misbehave when the coordinator launches from an unexpected working directory. Making routing reliability depend on hook correctness violates the stated constraint.

**Consequences**

A coordinator session becomes visible to `handleAsk` on its first `niwa_await_task` or `niwa_check_messages` call. Sessions that never make either call remain invisible, and asks route to spawn — which is the same behavior as today and appropriate for absent coordinators. The `handleAsk` routing change adds one synchronous `sessions.json` read per ask (cheap; the file is small and rarely grows beyond a handful of entries). Stale entries are pruned by the existing `IsPIDAlive` check. The `niwa session register` hook path continues to work alongside lazy registration; double-registration is safe because `writeSessionEntry` is already idempotent for live sessions (`errAlreadyRegistered` handling via `--check-only`).

The implementation does not require any new MCP tools or changes to the daemon event loop.
<!-- decision:end -->
