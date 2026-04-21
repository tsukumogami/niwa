# Lead: Claude Code inter-session communication primitives

## Findings

### 1. Non-interactive / print mode (`-p`)

`claude -p "prompt"` runs a single prompt non-interactively and exits. Input comes from stdin or an argument; output goes to stdout in `text`, `json`, or `stream-json` format. This makes Claude Code usable as a subprocess in pipelines, but it does not provide bidirectional communication between two running sessions — it is a one-shot request/response against the Anthropic API.

Session state persists to disk (`~/.claude/projects/<encoded-cwd>/*.jsonl`). A second `claude -p` call can `--continue` (most recent session) or `--resume <id>` (specific session) to pick up conversation history, but this requires sequencing — the first process must finish before the second resumes. There is no mechanism for two `claude -p` processes to exchange messages while both are running.

`--input-format stream-json` enables a multi-turn NDJSON conversation over stdin within a single `claude -p` invocation, but this is a single-process streaming interface, not cross-process communication.

### 2. Session management

Sessions are identified by UUID, stored as `.jsonl` transcript files, and scoped to a working directory path. The Agent SDK (Python `claude_agent_sdk`, TypeScript `@anthropic-ai/claude-agent-sdk`) provides `listSessions()`, `getSessionMessages()`, `getSessionInfo()`, `renameSession()`, and `tagSession()` functions to read and annotate sessions on disk.

Nothing in the session API allows one running session to inject messages into another running session's transcript in real time. Sessions are append-only transcripts; there is no IPC mechanism between them. Cross-host resume requires manually moving the `.jsonl` file.

### 3. MCP servers

MCP (Model Context Protocol) servers are subprocesses spawned by Claude Code at startup. They communicate with their parent session over stdio and expose tools and resources. A Claude session can connect to any number of MCP servers simultaneously.

**Key capability**: an MCP server runs as a local process and can hold state, expose tools, and push notifications to its parent session. This is the richest existing primitive for external integration.

**What MCP cannot do natively**: one Claude session's MCP server is not directly reachable by a different Claude session. Each session spawns its own MCP subprocess. There is no built-in MCP registry or service-discovery mechanism that would let session A discover and call session B's MCP tools.

**HTTP transport**: MCP supports HTTP transport in addition to stdio. An MCP server can listen on a local HTTP port. A second Claude session could theoretically connect to that port if configured with its URL — but there is no built-in mechanism in Claude Code to auto-discover or connect to a sibling session's MCP server. This would require explicit coordination (a registry, a known port, or a provisioner like niwa).

### 4. Channels (research preview, v2.1.80+)

Channels are MCP servers that declare a `claude/channel` capability, causing Claude Code to register a notification listener. When the MCP server calls `mcp.notification({ method: 'notifications/claude/channel', ... })`, the payload appears in the session's context as a `<channel source="...">` tag and Claude reacts to it.

Two-way channels expose an MCP tool (e.g. `reply`) that Claude can call to send messages back. The channel server mediates the exchange: external event → push to session → Claude calls reply tool → channel server routes response.

**Permission relay**: a two-way channel can also receive `notifications/claude/channel/permission_request` events and send back verdicts (`allow` / `deny`), enabling remote approval of tool calls.

**What channels can and cannot do for inter-session communication**:
- A channel server is a local process that can receive HTTP POST requests from any source, including another Claude session running a Bash command.
- A session can push events to a channel by having Claude run `curl localhost:<port>` (if the channel has an HTTP listener). This is ad-hoc — it requires the sending session to know the channel's port and craft the right payload.
- Channels are designed for human-to-Claude relay (Telegram, Discord, CI webhooks), not Claude-to-Claude messaging. There is no built-in routing between sessions.
- Channels require `claude.ai` authentication (OAuth), not API keys. This is a deployment constraint.
- Custom channels are not on the approved allowlist; they require `--dangerously-load-development-channels` during the research preview.

A channel server with an HTTP listener could serve as a simple message broker between two Claude sessions if both sessions are configured to know its address. This is the closest existing primitive to a relay.

### 5. Hooks

Hooks are shell commands, HTTP endpoints, prompt evaluations, or agent invocations that fire at lifecycle events: `PreToolUse`, `PostToolUse`, `SessionStart`, `SessionEnd`, `Stop`, `UserPromptSubmit`, `Notification`, `SubagentStart`, `SubagentStop`, `TeammateIdle`, `TaskCreated`, `TaskCompleted`, and others.

Hooks receive a JSON payload on stdin (for command hooks) or in the request body (for HTTP hooks) and can inject context back into the session via stdout. `PreToolUse` hooks can block tool calls (exit 2) or modify tool inputs. All hooks can inject `additionalContext` into the Claude context window (capped at 10,000 characters).

**What hooks can do for inter-session communication**:
- A `Stop` or `PostToolUse` hook could write a file or POST to a local HTTP endpoint when session A finishes work, and session B's `SessionStart` or a polling mechanism could pick it up.
- A `UserPromptSubmit` hook could intercept a message, forward it to another process, and inject the response back as `additionalContext` before Claude sees the prompt.
- HTTP hooks can call any local HTTP server, so a broker service could receive events from multiple sessions.

**What hooks cannot do**: hooks run in response to events inside one session; they cannot push unsolicited messages into a different running session. They have no concept of session identity or session routing.

### 6. Agent Teams (experimental, v2.1.32+)

Agent teams are the only built-in Claude Code primitive that explicitly provides inter-session communication. One session acts as a team lead; it spawns teammates as separate Claude Code processes. Each teammate has its own context window.

**Communication mechanism**: a mailbox system. Teammates can `message` a specific teammate by name or `broadcast` to all. Messages are delivered automatically; the lead does not poll. Teammates share a task list stored in `~/.claude/tasks/{team-name}/`. The team config in `~/.claude/teams/{team-name}/config.json` lists all members with their session IDs.

**What agent teams can do**:
- True peer-to-peer messaging between named Claude sessions.
- Shared task list with dependency tracking and file-locking to prevent races.
- Lead can assign tasks; teammates can self-claim.
- Hooks (`TeammateIdle`, `TaskCreated`, `TaskCompleted`) enforce quality gates.
- Teammates can be spawned with specific subagent definitions (specialized roles).

**Limitations relevant to niwa**:
- Experimental and disabled by default; requires `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`.
- The lead creates the team; niwa cannot provision a team independently of a running lead session.
- No session resumption for in-process teammates (the most common mode).
- Team config is runtime state — cannot be pre-authored or provisioned externally.
- No nested teams; teammates cannot spawn their own teams.
- The lead is fixed at team creation; leadership cannot be transferred.
- Teammates load skills and MCP from project/user settings, not from the team config.
- One team per lead session.
- All teammates start with the lead's permission mode.
- The messaging API is internal to Claude Code; there is no documented external API to inject messages into a running team session from an outside process.

### 7. Remote Control (v2.1.51+)

Remote Control connects `claude.ai/code` or the Claude mobile app to a local Claude Code session. The session runs on the local machine; the web/mobile interface is a window into it. Messages are routed through the Anthropic API over TLS. Requires `claude.ai` OAuth (no API key support).

Remote Control is designed for human-to-session relay from a different device, not session-to-session communication. It has no API for programmatic injection of messages from another local process.

### 8. The `--bare` flag and subprocess spawning

`--bare` skips hooks, skills, MCP servers, CLAUDE.md discovery, and OAuth/keychain reads at startup. It is the recommended mode for scripted/SDK calls and will become the default for `-p`. In bare mode, Claude only has access to Bash, file read, and file edit tools unless additional flags are passed. This is useful for spawning lightweight subprocess agents but does not add any IPC capability.

### Gaps — what cannot be done with existing primitives

- **No programmatic inter-session message injection**: nothing lets process A push a message into a running interactive session B without user involvement, except through the channels primitive (which requires the channel server to be pre-configured in session B's MCP config).
- **No session discovery**: Claude Code has no built-in registry of which sessions are running against which directories. The `~/.claude/sessions/` directory contains per-PID metadata (`sessionId`, `cwd`, `startedAt`), but it is not a reliable live registry (entries persist after sessions exit, there is no locking or cleanup protocol).
- **No structured inter-session protocol**: agent teams have a mailbox, but it is internal to Claude Code and has no documented external API. A third party cannot inject into the mailbox.
- **Channel primitive is close but constrained**: a channel HTTP listener is the closest thing to a message bus, but it requires per-session MCP configuration, `claude.ai` auth, and the `--dangerously-load-development-channels` flag during the research preview.
- **No cross-directory agent teams**: agent teams spawn teammates in the same working directory as the lead. Each niwa workspace repo is a different directory; teammates cannot span directories with different project contexts in the current model.

## Implications

The existing Claude Code surface area does not provide a ready-made inter-session communication layer that niwa can simply activate. The closest primitives are:

1. **MCP with HTTP transport** — the most promising building block. Niwa could provision a local MCP broker server per workspace at a known address (e.g. a Unix socket or fixed local port). Each repo's Claude session is configured with an MCP entry pointing at the broker. The broker implements routing: session A calls an MCP tool to `send_message(to="repo-b", body="...")`, and the broker pushes a `notifications/claude/channel` event to session B. This requires niwa to write `.mcp.json` or `settings.json` entries for each session, which is already within niwa's provisioning scope.

2. **Channels as the notification path** — the `claude/channel` capability is the only documented way for an external process to push an unsolicited message into a running Claude session's context. Building the broker as a channel server (rather than a plain MCP tool server) gives Claude the ability to receive messages reactively, not just on tool calls. The `claude.ai` auth requirement and the `--dangerously-load-development-channels` flag are blockers for production use today; both may be relaxed as channels graduate from research preview.

3. **File-based coordination as a fallback** — a shared file or SQLite database (investigated in lead 1) combined with hooks (e.g. a `Stop` hook that writes a "ready for next task" record, and a `SessionStart` hook that reads pending messages) would work without any Claude Code extension points, but it is polling-based and lacks the reactivity of the channel/MCP approach.

4. **Agent teams are not directly usable by niwa** — the team model requires a single lead to create the team. Niwa would need to either (a) provision a workspace-root Claude session that acts as the lead and spawns repo sessions as teammates, or (b) wait for agent teams to expose an external API for joining a pre-existing team. Option (a) is feasible but means all communication goes through the lead, not peer-to-peer.

## Surprises

- **`--session-id` flag exists**: a caller can specify the UUID for a new session. This means niwa could pre-register session IDs and use them for routing before sessions start, if it also provisioned the broker.
- **Sessions file at `~/.claude/sessions/`**: each running Claude process writes a JSON file with its PID, session ID, and `cwd`. This is a weak live registry — it persists stale entries, but a provisioner could use it as a starting point for session discovery with a liveness check (kill 0 on the PID).
- **Channels can relay permission prompts**: the `claude/channel/permission` capability lets a channel server relay tool-use approval prompts to a remote endpoint, where they can be approved or denied. In a multi-session workspace, this could let the root coordinator session approve or deny tool calls in worker sessions — a supervision primitive not just a messaging one.
- **`--brief` flag exposes `SendUserMessage` tool**: this flag, documented but not prominent, enables an `Agent`-to-user communication tool. Its interaction with inter-session patterns is unclear.
- **Agent team mailbox uses the task directory**: `~/.claude/tasks/{team-name}/` stores tasks as numbered JSON files with a `.lock` file. This is a file-based queue, not a socket. In principle, niwa could read/write these files directly to coordinate teams — but the schema is undocumented and marked as runtime state that will be overwritten.
- **Channels require `claude.ai` auth**: this rules out API-key-only environments (CI, automated pipelines without a human subscription) from using channels in production today.

## Open Questions

1. **Will channels graduate from research preview?** The `--dangerously-load-development-channels` flag and `claude.ai` auth requirement are the primary blockers for using channels as a production message bus. What is Anthropic's timeline?

2. **Is the agent team mailbox accessible from outside Claude Code?** The task directory format is documented as runtime state. Is there an external API or documented protocol for injecting messages into a running team?

3. **Can MCP HTTP transport serve as a cross-session bus today?** Specifically: can session A, configured with `mcp_server_b` pointing to session B's MCP HTTP endpoint, call session B's tools directly — and can session B push channel notifications back to session A? This would require MCP servers to expose HTTP endpoints that other sessions can call, which is supported by the MCP protocol but not tested in this investigation.

4. **What is the `~/.claude/sessions/` lifecycle?** Do entries get cleaned up when a session exits, or does the directory accumulate stale files? A reliable live registry would be valuable for niwa's session discovery needs.

5. **Does `--session-id` affect where the session file is stored?** If niwa pre-assigns session IDs, it needs to know the path to the transcript so it can configure routing in the broker before sessions start.

6. **What happens to agent team peers if the lead exits?** Can teammates continue operating and communicating with each other, or does the team collapse? This matters for the coordinator-failure resilience of any niwa-provisioned team topology.

7. **Is there a way to join an existing agent team from outside?** The team config lists member session IDs. Could an independently started Claude session be added to an existing team's mailbox without being spawned by the lead?

## Summary

Claude Code has no built-in inter-session communication layer: sessions are isolated transcript files, and nothing lets one running session push messages into another. The closest usable primitive is the MCP channel protocol, where a local server process can push events into a configured session's context and receive replies via MCP tools — but it requires per-session MCP configuration, `claude.ai` auth, and a development flag in the current research preview. Agent teams provide the only native peer-to-peer messaging, but they are experimental, require a lead session to create the team, cannot span multiple working directories, and expose no external API for programmatic participation. The main implication for niwa is that building the communication layer means provisioning a local MCP broker server and writing MCP config entries into each session's workspace — work that is within niwa's provisioning scope but relies on primitives that are still maturing. The biggest open question is whether channels will graduate from research preview soon enough (and drop the `claude.ai` auth requirement) to be the broker transport, or whether the initial implementation should use a simpler file-based or Unix-socket bus independent of Claude Code's own extension points.
