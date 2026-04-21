# Design Summary: cross-session-communication

## Input Context (Phase 0)
**Source PRD:** docs/prds/PRD-cross-session-communication.md (status: Draft — user explicitly directed design phase)
**Problem (implementation framing):** Niwa needs a ChannelMaterializer that provisions a file-based session mesh at apply time, a daemon that resumes idle sessions via `claude --resume`, and blocking MCP tools (`niwa_ask`, `niwa_wait`) that hold goroutines open waiting for inbox events — all connected by the invariant that sessions respond via `niwa_send_message` into the inbox, never via stdout.

**Architecture decisions already settled by PRD:**
- File-based inbox as transport (atomic rename, crash-safe)
- Daemon (`niwa mesh watch`) watches all inboxes via fsnotify, resumes dead-PID sessions via `claude --resume`
- `niwa_ask`: blocking tool, holds goroutine open watching caller's inbox for reply_to match
- `niwa_wait`: blocking tool, collects N messages of specified types before returning
- Sessions respond via `niwa_send_message` (not stdout) — forward-compat with Channels wakeup path
- `[channels.mesh]` namespace in workspace.toml
- SessionStart hook: registers session + delivers pending messages as initialUserMessage
- UserPromptSubmit hook: fallback for live coordinator sessions

**Open design questions (for Phase 2):**
1. Daemon lifecycle management (standard)
2. Claude session ID discovery (standard)
3. niwa_ask blocking mechanism (critical)
4. ChannelMaterializer integration point in runPipeline (standard)

## Phase 1 Decomposition
**Decision count:** 4 (within 1-5 range, proceed normally)
**All independent:** no coupling requiring sequential execution

## Security Review (Phase 5)
**Outcome:** Option 2 - Document considerations
**Summary:** No blocking architectural changes required. Key implementation-time mitigations: file permissions (0700/0600 on .niwa/sessions/), input validation for ClaudeSessionID and message routing fields, sessions.json file locking for concurrent writes, message retention TTL in inbox/read/, binary path resolution via exec.LookPath at daemon start, and graceful SIGTERM shutdown. HMAC signing is tracked as a follow-on requirement.

## Current Status
**Phase:** 5 - Security complete
**Last Updated:** 2026-04-21
