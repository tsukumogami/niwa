# Phase 2 Research: Existing Solutions for Claude Code Inter-Session Communication

---

## 1. Anthropic Agent Teams (Claude Code v2.1.32+, experimental)

### How It Works

Agent Teams shipped with Claude Opus 4.6 in February 2026 as an experimental feature enabled by setting `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`. One Claude Code session acts as the team lead. The lead creates a team, spawns teammates as separate Claude Code processes, and coordinates work through two shared primitives stored locally:

- **Team config**: `~/.claude/teams/{team-name}/config.json` — lists all members with session IDs, agent IDs, and display pane IDs. Runtime state; auto-overwritten on every state update.
- **Task list**: `~/.claude/tasks/{team-name}/` — numbered JSON files with file locking to prevent race conditions on simultaneous claims.

Inter-session messaging goes through a **mailbox system**. Teammates send messages by calling the `message` tool (directed) or `broadcast` tool (all teammates). Messages are delivered automatically without polling; recipients do not need to call a receive function. Idle notifications fire automatically when a teammate finishes. The lead assigns every teammate a name at spawn time; routing uses that name as the address.

The lead's terminal allows cycling through teammates with Shift+Down and typing directly into any teammate's session. Split-pane mode (tmux or iTerm2) shows all sessions simultaneously. Subagent definitions can be attached to teammates to constrain their tools and model, but `skills` and `mcpServers` from those definitions are not applied — teammates load MCP from project/user settings like any regular session.

### Niwa Use Case Fit

Agent Teams do not cover the niwa topology. The system **requires a lead session to create and spawn all teammates**. Independently-opened sessions — a user who opens four terminal windows manually, each in a different repo directory — cannot join an existing team. The team config lists session IDs assigned at spawn time by the lead; there is no documented external API to inject a session into a running team's mailbox.

Additional blockers specific to niwa:

- **No cross-directory teams**: each teammate is spawned by the lead and inherits the lead's working directory context, then receives a spawn prompt. Teammates working in different repo directories each with their own CLAUDE.md project context is technically possible (the teammate runs in its own session and reads its own CLAUDE.md), but the spawning model assumes the lead initiates everything from a single entry point.
- **Team config is runtime state**: cannot be pre-authored or provisioned by niwa before sessions start. Niwa's provisioning happens at `niwa create`/`niwa apply` time, before any sessions open. The team config is overwritten on every state update, so any pre-authored file would be destroyed.
- **One team per lead session**: a workspace with one coordinator session and five repo sessions fits the topology poorly — the coordinator would be the lead and the repos would be five teammates, but that requires the coordinator to be running before any repo session starts.
- **Experimental flag required**: cannot be enabled at workspace level in a reliable way without every user opting in.

### Limitations

- Experimental, disabled by default.
- No session resumption for in-process teammates after `/resume`.
- Task status can lag; tasks sometimes fail to auto-mark as completed.
- No nested teams; teammates cannot spawn sub-teams.
- Leadership is fixed at team creation time.
- Split-pane mode requires tmux or iTerm2; not supported in VS Code terminal, Windows Terminal, or Ghostty.
- Mailbox API is internal to Claude Code; no documented external interface for injecting messages from outside the team.

### Verdict: Learn From

The mailbox design — automatic delivery without polling, named addressing, shared task list with file-locking for race-free claiming — is exactly the right pattern. But the spawning requirement is a hard blocker for niwa's independently-opened-session model. Niwa should adopt the design patterns (named roles, automatic delivery on registration, shared task primitives) but must build the transport layer itself rather than reusing Agent Teams infrastructure.

---

## 2. MCP Channels (Claude Code research preview, v2.1.80+)

### How It Works

A channel is an MCP server that declares the `claude/channel` capability. When Claude Code starts with the `--channels plugin:<name>` flag, it connects to that MCP server and registers a notification listener. When the server sends a `notifications/claude/channel` notification, the payload appears in the active session as a `<channel source="...">` tag and Claude reacts to it without user intervention. Two-way channels expose a reply tool so Claude can send responses back through the same channel. The channel server mediates the full round-trip: external event in, Claude processes, Claude calls reply tool, server routes response out.

The permission relay extension of this protocol lets a channel server forward tool-use permission prompts to a remote approver and relay the verdict back. This is a supervision primitive, not just a messaging one.

Channels are deployed as Bun scripts, installed via `/plugin install`, and require a `claude.ai` login (no API-key-only environments). During the research preview, `--channels` only accepts plugins from an Anthropic-maintained allowlist; custom channels require `--dangerously-load-development-channels`. Team and Enterprise organizations must explicitly enable channels via admin settings; they are blocked by default.

The currently shipped channels (Telegram, Discord, iMessage) are designed for **human-to-session** relay: a human sends a message from a phone or messaging app, and it arrives in a running Claude Code session.

### Niwa Use Case Fit

Channels are the closest existing Claude Code primitive to a push-based inter-session delivery mechanism. A channel server with an HTTP listener could, in principle, act as a broker: session A sends a message by calling a Bash command or MCP tool that POSTs to the channel server, and the server pushes a `notifications/claude/channel` event into session B's context. This pattern would allow **session-to-session messaging without user intermediation** if both sessions are configured with the channel MCP entry pointing at the same broker.

However, the research preview constraints make this unsuitable for niwa's v1:

- **`claude.ai` OAuth required**: rules out API-key-only environments, CI pipelines, and users who prefer not to link a claude.ai account.
- **`--dangerously-load-development-channels` required for custom channels**: cannot be provisioned reliably by niwa without user friction.
- **Allowlist restriction**: a niwa-custom channel broker would not be on Anthropic's approved allowlist.
- **Plugin infrastructure**: channels are Bun scripts installed via the Claude Code plugin system. Niwa would need to package and install a Bun plugin, adding a Bun runtime dependency.

The protocol design is sound and will likely mature. If channels graduate from research preview and drop the `claude.ai` auth requirement (or allow API keys), this becomes a viable delivery mechanism for niwa. The channel notification model is also the only documented way to push an unsolicited message into a running Claude session's context window — all alternatives require the session to poll.

### Limitations

- `claude.ai` authentication required; no API key support.
- Research preview: `--dangerously-load-development-channels` required for custom channels.
- Allowlist-gated: custom channel must be approved or the development flag used.
- Bun runtime required for channel plugins.
- Human-to-session design; no built-in routing between two Claude sessions.
- Events only arrive while the session has the channel MCP server active.

### Verdict: Learn From (adopt in future if constraints lift)

The notification push model is the right UX — Claude reacts to a message without the user copy-pasting, without a tool call, without polling. Niwa should design its own broker to produce the same user experience (message appears in context; Claude reacts) using an independent transport that works today with API keys. If channels graduate from research preview and allow programmatic injection from non-human sources without the `claude.ai` requirement, revisit adopting them as the delivery path.

---

## 3. Community Tools

### 3a. gastownhall/gastown (Gas Town)

#### How It Works

Gas Town is a Go workspace manager by Steve Yegge, described as a "multi-agent orchestration system for Claude Code, GitHub Copilot, and other AI agents with persistent work tracking." Each workspace has a **Mayor** (a Claude Code instance with full workspace context) and **workers** called Polecats. The system orchestrates agents through tmux sessions and environment variables — Gas Town does not import agent libraries and agents do not import Gas Town code; integration is configuration.

Inter-agent communication uses two mechanisms:

- **Mail**: delivered via Claude Code hooks (`.claude/settings.json` lifecycle hooks). When a hook fires, Gas Town injects mail content into the session context. Without hooks (for non-Claude runtimes), it falls back to `gt mail check --inject` as a startup command, or tmux `send-keys` as the last-resort fallback. The underlying storage appears to be Dolt (a MySQL-compatible versioned database) for transactional backing and query support.
- **Nudge**: a real-time signal (`gt nudge`) that alerts an agent without going through the mail queue. The implementation uses tmux `send-keys` to inject keystrokes into the target pane.

Work assignment happens via `gt sling <bead-id> <rig>`: the system assigns a bead (unit of work) to a rig (agent slot), which spawns a session. Context persistence uses `.events.jsonl` logs; the Seance feature lets a new session query previous sessions' decisions through these logs.

#### Niwa Use Case Fit

Gas Town's topology **requires system-spawned sessions**. Agents are launched via the sling dispatch mechanism; there is no mechanism for an independently-opened terminal session to register itself with an existing Gas Town workspace. The tmux-based nudge mechanism requires the target agent to be in a tmux pane that Gas Town controls.

Gas Town is also more opinionated than niwa about workspace shape: it uses Dolt as a database backend, requires tmux, and is tightly coupled to its own directory and role conventions. There is an open issue requesting headless/sandboxed agent support without the tmux requirement, indicating this constraint is recognized but not resolved.

The architectural analog to niwa is strong — Gas Town is exactly the provisioner-angle: a workspace manager that sets up communication infrastructure alongside code checkout and configuration. Its hook-based mail injection is the closest existing pattern to what niwa would provision via `workspace-context.md` and session hooks.

#### Limitations

- Sessions must be spawned by Gas Town; no independent join mechanism.
- Requires tmux.
- Dolt database dependency.
- Nudge uses tmux `send-keys` keystrokes (fragile if session state is unexpected).
- Complex setup; Dolt install required.

#### Verdict: Learn From

Gas Town validates the provisioner-angle design: workspace manager sets up the communication infrastructure at workspace creation time. Its hook-based mail injection pattern is directly applicable — niwa can inject the broker socket path via `workspace-context.md` and provision Claude Code hook scripts via its existing `HooksMaterializer`. The Dolt+tmux stack is heavier than what niwa needs; the pattern is right, the implementation stack is not portable.

---

### 3b. PatilShreyas/claude-code-session-bridge

#### How It Works

Session Bridge is a bash + jq implementation (9 scripts) that creates a file-based inbox/outbox system under `~/.claude/session-bridge/sessions/<id>/`. Each session runs `/bridge start` to generate a 6-character session ID and write a manifest. Peers exchange IDs manually (copy-paste of the short ID between terminals) and then connect with `/bridge connect <id>`.

Message delivery uses `send-message.sh` to write JSON files atomically (temp file + `mv`) into the recipient's inbox directory. The listener script `bridge-listen.sh` polls the inbox every 3 seconds. Round-trip latency is 5–15 seconds. Message status transitions from `pending` to `read` atomically to prevent duplicate processing.

The system is a Claude Code slash-command suite — the bridge commands (`/bridge start`, `/bridge connect`, `/bridge send`) are `.claude/commands/` scripts that any open session can run.

#### Niwa Use Case Fit

Session Bridge **supports independently-opened sessions** — any session running in any directory can call `/bridge start` and receive an ID. The connection is peer-to-peer without requiring a pre-existing coordinator. Sessions in different working directories work fine since the inbox lives in `~/.claude/` (user home), not the working directory.

The critical limitation for niwa is that it requires **manual ID exchange**: a user must copy the 6-character ID from one terminal and paste it into another to establish a connection. This defeats the "no user intermediation" goal. There is no automatic discovery based on workspace membership — the bridge does not know which sessions belong to the same niwa workspace.

The file-based transport is exactly what prior research identified as the right starting point for niwa's v1: atomic rename-into-place delivery, durable across session restarts, debuggable with `cat`. The 3-second polling interval is acceptable for inter-session coordination at human interaction speeds.

#### Limitations

- Manual ID exchange required for peer connection (no automatic workspace-scoped discovery).
- Single-machine only.
- 3-second polling; 5–15 second round-trip.
- No encryption; plain JSON on local filesystem.
- No message TTL or delivery guarantees beyond file atomicity.
- Bash + jq implementation; not embeddable in a Go tool.
- No broker; each session independently polls its inbox.

#### Verdict: Learn From

The file-based inbox/outbox design is the right pattern. Niwa's contribution is automatic workspace-scoped discovery: provisioning named inboxes per registered session at `niwa create` time, so sessions find each other by role rather than by manually exchanged short codes. The slash-command delivery model (Claude calls `/bridge send` to send a message) is also the right pattern for the tool-call model of back-channel communication. Niwa should not wrap Session Bridge directly; instead, it should build the same file transport natively in Go with workspace-aware discovery layered on top.

---

### 3c. dopatraman/a2a

#### How It Works

`a2a` is a hub-and-spoke relay: a central server runs on port 7800 and maintains an agent registry. Each Claude Code session connects an MCP client to the hub over WebSocket; the MCP client connects to Claude Code over stdio. The session calls `/connect <name>` to register with the hub and receive a unique ID. Agents subscribe to other agents using `/watch <id>`. When an agent emits an event via the `emit` MCP tool, the hub checks which sessions are watching the emitter and broadcasts to their MCP clients, which inject the event as a `<channel>` notification into the target Claude session.

The hub is a Node.js WebSocket server. The MCP client is a separate process per Claude session that bridges stdio (Claude's MCP transport) and WebSocket (hub transport).

#### Niwa Use Case Fit

`a2a` **supports independently-opened sessions** — any session with the MCP client configured can call `/connect` to join the hub. Sessions in different working directories work as long as all sessions are configured with the MCP entry pointing to the hub's port. This is the closest existing tool to what niwa would provision: configure MCP in each session to point at a hub, and sessions find each other through the hub.

The hub must be started separately (`node server.js`) and is not automatically provisioned. For niwa, provisioning the hub process at `niwa apply` time and writing the MCP config entry into each session's `.claude/settings.json` would close this gap. The hub's session registry uses `~/claude-relay/sessions/registry.json`, which is machine-wide rather than workspace-scoped.

The subscribe/watch model is event-driven (one session observes another's work in real-time) rather than directed messaging (send a specific message to a specific role). For niwa's use cases (question/answer, task delegation), directed messaging is more appropriate than event broadcasting.

#### Limitations

- Hub must be started manually; not provisioned at workspace creation.
- Subscribe/watch model (broadcast to watchers) rather than directed address-based routing.
- Machine-wide registry, not workspace-scoped.
- Node.js + WebSocket dependency.
- No message durability; messages lost if hub or recipient is offline.
- No authentication.

#### Verdict: Learn From

The hub-and-spoke architecture with MCP as the session-side transport is the right wiring diagram. Niwa's broker would be a Go process (not Node.js), workspace-scoped rather than machine-wide, with directed role-based routing rather than subscribe/watch, and provisioned at `niwa apply` time with MCP config injected into each session's settings. The `a2a` architecture validates that the MCP-to-broker bridge pattern works with independently-opened Claude sessions.

---

### 3d. bfly123/claude_code_bridge

#### How It Works

`claude_code_bridge` creates a tmux-based multi-agent runtime. A central daemon (`projectaskd`) manages all sessions within a project-scoped runtime anchored to `.ccb/` in the working directory. Agents communicate through a mailbox routing system where async replies land in a "mailbox chain." The implementation is tmux-only on Linux/macOS/WSL; Windows support is planned separately.

Sessions are **not independently joinable** — they are spawned and managed by the daemon within the project runtime. The daemon controls pane layout, message routing, and agent lifecycle.

#### Niwa Use Case Fit

The tmux-only, daemon-managed, project-scoped model conflicts with all three of niwa's key constraints: independently-opened sessions, different working directories, and no daemon requirement. The `.ccb/` directory per project does not generalize to a multi-repo workspace where each repo is in a different directory.

#### Limitations

- tmux required.
- Sessions must be spawned by the daemon; no independent join.
- Single project directory scope (`.ccb/` anchor).
- Linux/macOS/WSL only for current stable implementation.

#### Verdict: Ignore

The implementation model is incompatible with niwa's constraints on all three key dimensions.

---

### 3e. Dicklesworthstone/mcp_agent_mail

#### How It Works

`mcp_agent_mail` is an HTTP FastMCP server (default port 8765) that acts as a mail-like coordination layer for coding agents. Agents connect by configuring the MCP server URL in their Claude Code settings (type: `url`). Any session that points at the server can register as a named agent via `register_agent(project_key, program, model, name?)`. Each agent gets a persistent identity backed by Git for human-readable audit artifacts and SQLite for fast indexing and full-text search.

Tools include `send_message()`, `fetch_inbox()`, `fetch_outbox()`, and resource shortcuts like `resource://inbox/{agent_name}`. Messages are asynchronous; agents do not need to be online to receive — messages queue in the inbox until fetched. File reservation "leases" let agents signal exclusive editing intent (advisory only; the system reports conflicts but does not prevent writes).

A Rust rewrite (`mcp_agent_mail_rust`) provides 34 tools, Git-backed archive, SQLite indexing, and an interactive TUI console, indicating active investment in the project.

#### Niwa Use Case Fit

`mcp_agent_mail` **supports independently-opened sessions** — any session configured with the MCP URL can register and participate. Sessions in different working directories work fine (the server is machine-level, not directory-scoped). The async inbox model means a session can send a message even if the recipient is not currently running — the message queues and is delivered when the recipient next calls `fetch_inbox`.

The gap relative to niwa is provisioning: the server must be started separately and the MCP URL must be added to each session's config manually. Niwa could close this gap by starting the server at `niwa apply` time and writing the MCP entry into each session's `.claude/settings.json` automatically.

The project-scoped registration model (agents register under a `project_key`) maps naturally to niwa's workspace instance concept. However, the server is designed as a standalone service, not as something embedded in a workspace manager.

#### Limitations

- Server must be started separately; not auto-provisioned.
- Machine-level, not workspace-scoped by default (though `project_key` provides logical scoping).
- Python (original) or Rust dependency; not pure Go.
- Advisory-only file leases; no hard coordination enforcement.
- Polling model: agents call `fetch_inbox` to retrieve messages; no push delivery.

#### Verdict: Learn From (possibly adopt for early prototype)

This is the most feature-complete standalone solution. Its HTTP FastMCP transport, named agent identities, async inbox/outbox, and Git-backed audit log are all the right design choices. Niwa could wrap it as an optional backend (provision the server, inject MCP config) rather than building a competing implementation. The Python/Rust dependency is the main friction for a Go-native niwa integration. If niwa needs a working prototype quickly, wrapping `mcp_agent_mail` is faster than building from scratch; if niwa wants a tight Go-native integration with zero extra runtimes, the design patterns are worth adopting directly.

---

### 3f. gvorwaller/claude-relay

#### How It Works

`claude-relay` is a WebSocket hub on port 9999. Each Claude Code session runs an MCP client that connects to the relay over WebSocket. Session identity is resolved in priority order: `CLAUDE_RELAY_SESSION_ID` environment variable, `--client-id` argument, `RELAY_CLIENT_ID` environment variable, or auto-generated `hostname-pid`. A session registry at `~/claude-relay/sessions/registry.json` tracks all registered sessions including offline ones.

Sessions send messages using the `relay_send` MCP tool specifying target peers; recipients retrieve messages with `relay_receive`. The hub broadcasts to specified targets. SSH port forwarding enables cross-machine use.

#### Niwa Use Case Fit

**Independently-opened sessions are supported** — any session with the MCP entry configured connects to the relay on startup. The environment-variable-based session ID is well-suited to niwa's provisioning model: niwa could write `CLAUDE_RELAY_SESSION_ID=<role>` into each session's `.claude/settings.json` env block, giving sessions predictable identities.

The session registry is machine-global rather than workspace-scoped, which is a minor gap for multi-workspace scenarios. The pull model (`relay_receive`) requires sessions to call a tool to get messages rather than receiving them as push notifications.

#### Limitations

- Hub must be started manually; not provisioned at workspace creation.
- Pull model; no push delivery into session context.
- Machine-global registry; not workspace-scoped.
- No authentication by default.
- No message durability; messages in transit are lost if hub crashes.
- Node.js WebSocket dependency.

#### Verdict: Learn From

The environment-variable-based session identity and machine-global session registry are both implementable in niwa's provisioning model. The Node.js WebSocket hub is the wrong technology for a Go-native tool; niwa would build an equivalent in Go. The pull model is acceptable for v1 if combined with a "check inbox on session start" hook.

---

### 3g. mhcoen/mcp-relay

#### How It Works

A minimal MCP server that maintains a shared SQLite buffer (`~/.relay_buffer.db`) with a rolling window of 20 messages (oldest evicted, 8 KB per message limit). Claude Desktop and Claude Code both configure it as an MCP server. Sessions use `/get` or `get` to explicitly fetch the buffer contents. No background sync; no automatic push. This is designed to reduce copy-paste between Claude Desktop and Claude Code, not for full multi-session mesh communication.

#### Niwa Use Case Fit

The scope is too narrow — designed for two sessions (Desktop + Code), not N sessions in a workspace mesh. The 20-message rolling window and 8 KB limit are insufficient for a workspace with multiple concurrent sessions exchanging detailed task context.

#### Verdict: Ignore

Correct pattern (shared MCP-accessible buffer) but scope and capacity are mismatched.

---

### 3h. MACP (Multi-Agent Cognition Protocol)

#### How It Works

MACP is an npm package (`macp-mcp`) that provides a shared SQLite bus for multi-agent project coordination. After running `npx -y macp-mcp init` once in a project, any supported agent host (Claude Code, Codex, OpenCode, Gemini CLI) that opens the project auto-registers and joins the same SQLite workspace. Tools include `macp_poll`, `macp_send_channel`, `macp_send_direct`, and `macp_ack`.

Key properties: one shared SQLite file per project, durable delivery with ACK state, no broker process, no external service. Auto-registration on session open.

#### Niwa Use Case Fit

**Independently-opened sessions are supported** — MACP auto-registers any supported agent that opens the project. The SQLite file is project-scoped, matching niwa's workspace directory model. Durable delivery (messages persist until ACKed) is the right guarantee for a workspace where sessions may be offline when a message is sent.

The limitation is scope: MACP is designed for a single project directory. Niwa's workspace spans multiple repos in different directories; a session working in `public/niwa` and a session in `public/tsuku` would each need a MACP instance, but they need to communicate across repos. MACP has no cross-project routing concept.

Auto-registration is the right design: niwa could adopt this exact model where a session opening in a niwa workspace automatically registers with the workspace broker by finding the broker address from the workspace's `workspace-context.md`.

#### Limitations

- Project-scoped; no cross-directory routing.
- npm/Node.js dependency.
- Only tested with Claude Code, Codex, OpenCode, Gemini CLI.
- No network transport (local SQLite only).

#### Verdict: Learn From

Auto-registration on session open (no explicit `niwa session register` command required) and SQLite-backed durable delivery are both the right patterns for niwa. The cross-repo routing gap is the key differentiator niwa needs to build.

---

## 4. Non-Claude Multi-Agent Frameworks

### AutoGen 0.4

AutoGen 0.4 (released January 2025) redesigned the framework as four layered packages. The `autogen-core` package provides an event-driven runtime; `autogen-ext` adds transport extensions including gRPC (installable without the full framework via `pip install autogen-ext[grpc]`). The gRPC transport enables distributed agents across processes or machines.

**Standalone messaging component**: AutoGen's `autogen-core` runtime can be used independently of the higher-level agent constructs. It provides a publish-subscribe event bus backed by either an in-process runtime or a distributed gRPC runtime. This is the closest to a standalone transport component in any of the major frameworks.

**Works with Claude Code CLI**: No. AutoGen expects to own the LLM call (wrapping the Anthropic API directly). It cannot act as a sidecar to an existing Claude Code session without significant adaptation — the transport would need to be wired to files or MCP tools that Claude Code sessions can read/write.

**Verdict**: Transport patterns are instructive (typed message envelope, correlation IDs, transport-agnostic routing); the library itself is not usable as a Claude Code inter-session transport.

### CrewAI

CrewAI's `DelegateWork` and `AskQuestion` tools model back-channel communication as tool calls — the key pattern that prior research identified as most relevant. These are Python tools with no standalone transport component. CrewAI requires owning the agent execution loop; it cannot be bolted onto Claude Code sessions.

**Verdict**: Design patterns are valuable (tool-call model for back-channel questions); no usable component.

### LangGraph

LangGraph's shared state model (agents communicate by reading and writing a common state object, not by sending messages) is the conceptually different approach. LangGraph Platform adds Postgres-backed persistence and distributed graph execution, but it requires LangGraph to own agent orchestration. Cannot be integrated with Claude Code as a sidecar.

**Verdict**: The shared mutable state model is an alternative design pattern niwa could consider, but the framework is not usable as a transport. The pattern maps to a shared SQLite file rather than a message bus — every session reads and writes workspace state rather than sending directed messages.

---

## 5. Shared MCP Server as Message Broker Pattern

Several tools (a2a, claude-relay, mcp_agent_mail) have independently converged on the same architecture: a standalone server process exposes MCP tools, Claude sessions configure the server URL in their MCP settings, and sessions exchange messages by calling MCP tools on the shared server.

This pattern works with standard Claude Code MCP configuration today (no experimental flags, no `claude.ai` auth). The mechanics:

1. Niwa provisions a broker process at `niwa apply` time (or launches it on first `niwa session register`).
2. Niwa writes the broker's MCP endpoint into each session's `.claude/settings.json` under `mcpServers`.
3. Each session discovers its inbox address from `workspace-context.md` (injected by niwa's `WorkspaceContextMaterializer`).
4. Sessions send messages via a `send_message` MCP tool call specifying recipient role and message type.
5. Sessions retrieve messages via a `fetch_inbox` MCP tool call (poll model) or receive them as MCP notifications (push model, requires the broker to implement the `claude/channel` protocol).

The push vs. pull decision is the main open point. The MCP notification path (`notifications/message`) is available without the channels research preview constraints — it is the normal MCP notification protocol. A broker that sends MCP notifications to connected sessions achieves push delivery without the `claude.ai` auth requirement. The channel-specific `notifications/claude/channel` that creates `<channel source="...">` tags in Claude's context is the constrained research preview feature; plain MCP notifications are not.

**Has anyone built this fully?** `mcp_agent_mail` is the most complete implementation. `a2a` implements the MCP-to-WebSocket-hub bridge. `claude-relay` implements the WebSocket registry + MCP tool wiring. None of them combine workspace-scoped provisioning with automatic session registration and role-based routing in a single package.

---

## Comparison Table

| Solution | How it routes messages | Supports independent sessions | Requires session spawning | Works today (no experimental flags) | Network path |
|---|---|---|---|---|---|
| **Anthropic Agent Teams** | Lead-managed mailbox; named teammate routing | No — sessions must be spawned by lead | Yes, required | No — `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` required | No — same machine only |
| **MCP Channels (`claude/channel`)** | MCP server pushes `notifications/claude/channel` into session context | Yes — if MCP config is provisioned per session | No | No — `--dangerously-load-development-channels` + `claude.ai` OAuth required | Via Telegram/Discord bots; no direct machine-to-machine |
| **Gas Town** | tmux `send-keys` nudge + hook-injected mail via Dolt | No — sessions spawned via `gt sling` | Yes, required | Yes (no flags; requires Dolt + tmux) | No — same machine only |
| **claude-code-session-bridge** | File-based inbox/outbox in `~/.claude/session-bridge/` | Yes — any session can `/bridge start` | No | Yes | No — local filesystem only |
| **dopatraman/a2a** | WebSocket hub + MCP notification injection | Yes — any configured session can `/connect` | No | Yes | Limited — WebSocket hub can be exposed remotely |
| **bfly123/claude_code_bridge** | Daemon-managed mailbox in `.ccb/` | No — sessions spawned by daemon | Yes, required | Yes (requires tmux) | No |
| **mcp_agent_mail** | HTTP FastMCP server; named agent inbox/outbox; SQLite + Git backend | Yes — any session with MCP URL configured can join | No | Yes | No — localhost by default; remote configurable |
| **gvorwaller/claude-relay** | WebSocket hub + MCP `relay_send`/`relay_receive` tools | Yes — any configured session can connect | No | Yes | Yes — SSH port forwarding supported |
| **mhcoen/mcp-relay** | Shared SQLite buffer via MCP; explicit fetch | Yes — Desktop and Code configure same MCP server | No | Yes | No |
| **MACP** | Shared SQLite bus; auto-register on project open; `macp_send_direct` | Yes — auto-registers on session open | No | Yes (npm) | No |
| **AutoGen gRPC transport** | gRPC event bus with pub-sub; typed message envelopes | N/A — not designed for Claude Code sessions | N/A | Yes (Python) | Yes — native gRPC |
| **CrewAI DelegateWork tool** | In-process tool call; no cross-process transport | N/A | N/A | Yes (Python) | No |
| **LangGraph shared state** | Read/write common state object; no message bus | N/A | N/A | Yes (Python) | Via LangGraph Platform (Postgres) |
| **Shared MCP server pattern** | MCP tools (`send_message`, `fetch_inbox`) on a shared HTTP server | Yes — sessions configure the MCP URL | No | Yes | Configurable (HTTP endpoint) |

---

## Synthesis and Recommendation

### What niwa should adopt or integrate

**Nothing should be adopted wholesale.** The closest candidate for adoption is `mcp_agent_mail`: it is the most feature-complete standalone implementation, supports independently-opened sessions, and has the right design (named agent identities, async inbox, Git+SQLite backend). The blockers are the Python/Rust runtime dependency (niwa is Go-native) and the machine-global rather than workspace-scoped registry. Niwa could provision and wrap `mcp_agent_mail` as a backend for a fast prototype, but the dependency friction is high enough that building a Go-native equivalent is the right long-term choice.

**What niwa should build that nothing else does**: workspace-scoped automatic discovery. All existing tools either require manual ID exchange (session-bridge), manual startup (a2a, claude-relay, mcp_agent_mail), or session spawning (Agent Teams, Gas Town, claude_code_bridge). None of them provision the communication infrastructure at workspace creation time and automatically configure each session to participate based on its repo and role. That is niwa's differentiated contribution:

1. **Provisioning at `niwa apply`**: start the broker (a Go process, embedded in niwa or launched as a sidecar), write its MCP endpoint into each session's `.claude/settings.json`, and write the broker address and each session's assigned role into `workspace-context.md` so Claude knows where to send messages and how to identify itself.
2. **Auto-registration**: when a session opens in a niwa workspace, it reads its role from `workspace-context.md` and calls `session_hello` on the broker. No user action required.
3. **Role-based routing**: messages addressed to `role: "coordinator"` or `role: "niwa-worker"` route to the session that registered with that role, not to a session ID that the user must look up.
4. **Cross-repo workspace scope**: the broker is scoped to the workspace instance (`.niwa/channel.sock` at instance root), not to a single project directory, so sessions in `public/niwa` and `public/tsuku` can exchange messages through the same broker.

### What niwa should build differently from existing tools

- **Transport**: file-based inbox with `fsnotify` for push-like delivery (not polling) as the v1 default, matching the durability story from the file-queue analysis. Unix socket for the broker control channel (registration, routing). MCP tool layer on top so Claude sessions interact with the broker via standard MCP tool calls.
- **Provisioning**: `niwa apply` provisions the broker and MCP config; sessions self-register on open. No manual setup step required after workspace creation.
- **Network path**: design the message schema (transport-agnostic JSON envelope from the message-schema lead) so the broker can be swapped for NATS JetStream or an HTTP server when cross-machine transport is needed without changing session-side tool calls.
- **MCP channels integration**: leave a documented migration path. If MCP channels graduate from research preview and drop the `claude.ai` auth requirement, the broker can be reimplemented as a channel plugin and the same `workspace-context.md`-injected broker address becomes the channel URL. The session-side tool call model stays the same.

---

## Summary

No existing tool fully covers niwa's use case. The closest candidates — `mcp_agent_mail` (feature-complete, supports independent sessions, HTTP FastMCP transport) and `MACP` (auto-registration on session open, SQLite durable delivery) — solve the messaging problem but not the provisioning problem: neither is workspace-aware, neither is provisioned at workspace creation time, and neither routes messages by workspace role. The gap niwa fills is real and unoccupied: workspace-scoped automatic provisioning of a communication layer where sessions identify themselves by role (derived from their repo), auto-register when opened, and exchange messages without any manual user action beyond opening a terminal. Anthropic's Agent Teams ship the closest native primitive but require a spawning lead, cannot span independently-opened sessions, and are gated behind an experimental flag — the niwa topology is specifically the gap they do not cover.
