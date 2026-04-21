---
status: Draft
problem: |
  Developers using niwa to manage multi-repo workspaces routinely run multiple Claude
  sessions simultaneously — one per repo and one at the workspace root. When sessions
  need to exchange information (clarifying questions mid-implementation, task delegation,
  code review feedback, status updates), the user must manually copy-paste between
  terminals. This "human as relay" pattern breaks focus, limits how autonomously Claude
  sessions can operate, and makes it impractical to run more than two sessions in
  parallel. No existing tool provisions this communication layer automatically at
  workspace creation time for independently-opened sessions working across different
  directories.
goals: |
  Niwa provisions a workspace-scoped messaging layer when a workspace is created or
  updated. Sessions that open inside the workspace find their role and broker address
  already configured, register themselves without any user action, and can exchange
  directed messages by role without the user acting as an intermediary. The user can
  observe mesh state with a single command and receive structured error feedback when
  something goes wrong.
---

# PRD: Cross-Session Communication

## Status

Draft

## Problem Statement

Developers using niwa to manage multi-repo workspaces routinely run multiple Claude
sessions simultaneously — one per repo and one at the workspace root. When sessions
need to exchange information (clarifying questions mid-implementation, task delegation,
code review feedback, status updates), the user must manually copy-paste messages
between terminals. This "human as relay" pattern breaks focus, limits autonomy, and
makes parallel multi-session workflows impractical beyond two sessions.

The problem is well-documented in the Claude Code community: canonical issue #24798
has 8+ independent reporter threads and has never been closed as "won't fix."
Community workarounds range from shared markdown files polled every 15 minutes to
production HTTP servers with Ed25519-signed messages. Anthropic shipped Agent Teams
(February 2026) as a partial answer, but its architecture requires sessions to be
spawned by a single lead — independently-opened sessions per repo (the niwa topology)
are not covered.

No existing tool provisions this capability automatically at workspace creation time.
All community tools require manual startup, manual ID exchange, or session spawning by
a controlling process.

## Goals

1. Sessions find their communication role and broker address already configured when
   they open inside a niwa workspace — no manual setup step beyond running
   `niwa create` or `niwa apply`.
2. Sessions can send directed messages to each other by role, and receive messages by
   polling a tool, without the user acting as a relay.
3. Delivery failures (offline recipients, expired messages, registry corruption) surface
   structured errors to the sending session rather than silent data loss.
4. The user can inspect mesh state (which sessions are registered, what's live, pending
   message counts) with a single command.

## User Stories

**US1 — Coordinator delegates work**
As a coordinator Claude session at the workspace root, I want to send a task delegation
message to the niwa-worker session by its role so that the worker receives its
assignment without the user copy-pasting instructions between terminals.

**US2 — Worker asks a clarifying question**
As a worker Claude session mid-implementation, I want to send a question to the
coordinator and receive an answer so that I can continue working without interrupting
the user or making an assumption I'd have to undo later.

**US3 — Coordinator reviews a PR and delivers feedback**
As a coordinator Claude session that has reviewed a pull request, I want to send
structured review feedback to the worker session that opened the PR so that the worker
receives the comments directly without the user reading and relaying them.

**US4 — User inspects the mesh**
As a developer running a multi-session workspace, I want to run `niwa session list` and
see which sessions are registered, which are alive, and how many pending messages each
has so that I can understand the communication state at a glance.

**US5 — Sender learns a message was not delivered**
As a coordinator Claude session that sent a question to a worker, I want to learn that
the question expired unread when I next check my messages so that I don't wait
indefinitely for an answer that won't arrive.

## Requirements

### Provisioning

**R1** — The `[channels]` section in `workspace.toml` shall be the opt-in mechanism
for the workspace mesh. A workspace without a `[channels]` config shall not provision
any mesh infrastructure.

**R2** — `niwa create` and `niwa apply` shall provision the mesh infrastructure when
`[channels]` is configured: create the `<instance-root>/.niwa/sessions/` directory,
initialize an empty `sessions.json` registry, create per-session inbox directories as
sessions register, and create an `artifacts/` subdirectory for large-payload references.

**R3** — A `ChannelMaterializer` shall be added to the provisioning pipeline alongside
existing materializers (`HooksMaterializer`, `SettingsMaterializer`). It shall be
activated when `cfg.Channels` is non-empty and shall write all mesh infrastructure
within the existing `Applier.runPipeline` call.

**R4** — The `ChannelMaterializer` shall write `<instance-root>/.claude/.mcp.json`
declaring a `niwa` MCP server entry that invokes `niwa mcp-serve` with
`NIWA_INSTANCE_ROOT` baked in at apply time. This file shall be tracked in
`InstanceState.ManagedFiles` so drift detection and cleanup work automatically. The
server shall declare `capabilities.experimental["claude/channel"]` so it is
forward-compatible with Claude Code's native Channels push protocol.

**R5** — The `ChannelMaterializer` shall append a `## Channels` section to
`workspace-context.md` containing: the sessions registry path, the MCP tool names
(`niwa_check_messages`, `niwa_send_message`), the registration command, the session's
assigned role, and behavioral instructions directing Claude to: (a) call
`niwa_check_messages` immediately after registering; (b) check for messages at natural
idle points and as a backstop every M tool calls (default M=10, overridable in
`workspace.toml`); (c) emit `task.progress` messages at a defined cadence (default: every
5 minutes of wall time or every 20 tool calls, whichever comes first) while executing
a delegated task.

**R5a** — The `ChannelMaterializer` shall write a `SessionStart` hook entry into each
session's `.claude/settings.json` (via `HooksMaterializer`). The hook shall call
`niwa session register` and, if pending messages exist, return a JSON response with
`initialUserMessage` directing Claude to call `niwa_check_messages` before starting
work. This ensures a session that was offline when messages were sent sees them
immediately at startup without requiring polling.

**R5b** — The `ChannelMaterializer` shall write a `UserPromptSubmit` hook that checks
the inbox on every user prompt submission and injects a `additionalContext` reminder
when unread messages are present. This ensures that an idle session which the user
manually activates (by typing anything) surfaces pending messages in Claude's next
context window, even if the session never polled during its idle period.

### Session Identity and Registration

**R6** — `niwa session register` shall be a new CLI subcommand under a `niwa session`
subcommand group. It shall accept a `--repo <name>` flag (defaulting to the repo
inferred from the current working directory) and write a `SessionEntry` to
`sessions.json` under `<instance-root>/.niwa/sessions/`. It shall print the assigned
session ID and role to stdout on success.

**R7** — Role assignment shall follow this precedence order (highest to lowest):
`NIWA_SESSION_ROLE` environment variable; `[channels.mesh.roles]` entry in
`workspace.toml` for the session's repo path; auto-derived from the last path segment
of the repo directory (e.g., `public/niwa` → `niwa`); built-in default `coordinator`
for sessions running from the instance root with no repo.

**R8** — Role names shall be restricted to lowercase alphanumeric characters and
hyphens, with a maximum length of 32 characters. The `coordinator` role name shall be
reserved — only the root-session default or an explicit override may use it. Violations
shall be rejected at registration time with a clear error message.

**R9** — If a role is already registered by a live session (verified by PID existence
and process start time match), `niwa session register` shall fail with an error
identifying the conflicting PID and registration timestamp, and shall include the
command to clear the stale entry (`niwa session unregister <role>`). If the existing
entry's PID is dead or recycled, the system shall silently reclaim the role and
register the new session.

**R10** — All writes to `sessions.json` shall use advisory file locking (`flock` or
equivalent) or an atomic rename-from-tempfile pattern to prevent concurrent update
loss when multiple sessions register simultaneously.

**R11** — Liveness checks shall use both PID existence (`kill(0)`) and recorded process
start time (from `/proc/<pid>/stat` on Linux, `sysctl` on macOS) to detect recycled
PIDs. Stale entries (dead PID or mismatched start time) shall be pruned automatically
on every `sessions.json` read, before any routing lookup.

**R12** — `niwa session unregister` shall remove the calling session's entry from
`sessions.json` and its inbox directory. Cleanup of expired message files shall run as
part of unregister. `niwa session unregister` shall also run as part of `niwa destroy`.

### Messaging Tools

**R13** — `niwa mcp-serve` shall be a new CLI subcommand that starts a stdio MCP
server exposing at minimum two tools: `niwa_check_messages` and `niwa_send_message`.
The server shall be stateless across invocations — it reads from and writes to the
filesystem on each tool call, with no in-memory state between calls.

**R14** — `niwa_check_messages` shall return all unread messages in the calling
session's inbox directory, formatted as structured markdown summaries (not raw JSON)
preserving: `id`, `from.role`, `type`, `sent_at`, `expires_at` if set, and the
human-relevant body fields for each message type. It shall return a clearly
distinguished "no new messages" indicator (not an empty array or null). Messages whose
`expires_at` has passed shall be skipped, moved to the `expired/` subdirectory, and an
expiry notification shall be written to the original sender's inbox.

**R15** — `niwa_send_message` shall accept: `to` (role string), `type` (dotted routing
key from the defined vocabulary), `body` (type-specific object), and optionally
`reply_to` (message ID for correlation) and `expires_at` (ISO 8601 deadline). It shall
write the message to the recipient's inbox via atomic rename and return a `status` field
of either `queued` (recipient offline at send time) or `delivered` (recipient PID alive
at send time), plus the assigned message ID.

**R16** — `niwa_send_message` shall reject messages with unrecognized `type` values
synchronously with a structured error response. Defined type vocabulary for v1:
`question.ask`, `question.answer`, `task.delegate`, `task.ack`, `task.result`,
`task.progress`, `review.feedback`, `status.update`, `session.hello`, `session.bye`.

**R17** — `niwa_send_message` shall return structured error objects with a
machine-readable `error_code` field and a human-readable `detail` string. Defined error
codes: `MESH_NOT_PROVISIONED`, `RECIPIENT_NOT_REGISTERED`, `RECIPIENT_OFFLINE` (inbox
exists, PID dead), `INBOX_UNWRITABLE`, `MESSAGE_TOO_LARGE`. All send errors shall be
non-fatal to the calling session.

### Message Lifecycle

**R18** — The message envelope shall use the following schema (v1): `v` (schema
version, integer), `id` (client-chosen UUID string), `type` (dotted routing key),
`from` (object: `instance`, `role`, `repo`, `pid`), `to` (object: `instance`, `role`),
`reply_to` (message ID, optional), `task_id` (stable task UUID, optional), `sent_at`
(ISO 8601), `expires_at` (ISO 8601, optional), `body` (type-specific object).

**R19** — Messages shall have a soft size limit of 64 KB (warning returned in the MCP
tool response) and a hard limit of 1 MB (rejected with `MESSAGE_TOO_LARGE`). Message
types that may carry large content (`task.delegate`, `review.feedback`) shall support an
optional `artifact_path` field in `body` pointing to a file in
`<instance-root>/.niwa/sessions/artifacts/`, to be used when content exceeds the soft
limit.

**R20** — Expired unread messages shall be moved to an `expired/` subdirectory rather
than deleted. Expired messages shall be retained for a minimum of 24 hours and shall be
inspectable via `niwa session log`.

**R21** — When a message expires before being read, the system shall write an expiry
notification to the original sender's inbox on the next read cycle. The expiry
notification shall include the original `id`, `type`, `sent_at`, and the recipient role.

### Observability

**R22** — `niwa session list` shall display a table following the existing `niwa status`
column-alignment conventions, with columns: session role, repo path, PID, liveness
status (`live` / `stale` / `dead`), last-heartbeat age (using `formatRelativeTime`),
and pending message count. It shall be runnable from inside an instance directory or
from the workspace root with an optional instance name argument.

**R23** — `niwa session log [role]` shall list messages in the specified session's inbox
directory sorted by arrival time, showing sender role, message type, `sent_at`, and
first 80 characters of the body summary. It shall accept a `--since <duration>` flag.

**R24** — `niwa status` detail view shall include a one-line mesh summary (e.g.,
`3 sessions (2 live)`) when channels are configured in `workspace.toml`. Full session
detail remains in `niwa session list` only.

### Non-functional

**R25** — All mesh infrastructure shall be implemented in Go with no external runtime
dependencies beyond the Go standard library. No Python, Rust, Node.js, or npm
dependencies are permitted.

**R26** — Mesh communication is same-machine only for v1. The message schema and
transport interfaces shall be designed to allow a future network-capable broker (Unix
socket → TCP) without changes to the MCP tool layer or message schema.

**R27** — The `niwa session` subcommand group shall follow the existing cobra pattern
from `internal/cli/config.go`, with each subcommand in a separate file under
`internal/cli/`.

**R28** — The v1 design shall be forward-compatible with Claude Code's native
`claude/channel` delivery protocol. Concretely: the message envelope schema, role-based
addressing model, and session-side tool names (`niwa_check_messages`,
`niwa_send_message`) shall remain stable when the delivery path is upgraded from
file-based polling to Channels push delivery. The `ChannelMaterializer` shall write the
MCP server entry in a way that can be replaced with a Channels broker entry in a
subsequent `niwa apply` without requiring any changes to session-side configuration or
behavioral instructions.

## Acceptance Criteria

### Provisioning

- [ ] Running `niwa apply` on a workspace with `[channels]` configured creates
  `<instance-root>/.niwa/sessions/`, an empty `sessions.json`, and an `artifacts/`
  subdirectory.
- [ ] Running `niwa apply` on a workspace without `[channels]` creates none of the
  above.
- [ ] `<instance-root>/.claude/.mcp.json` exists after `niwa apply` on a channeled
  workspace and contains a `niwa` MCP server entry pointing to `niwa mcp-serve`.
- [ ] `workspace-context.md` contains a `## Channels` section after `niwa apply` on a
  channeled workspace, including the MCP tool names and the session's assigned role.
- [ ] Running `niwa apply` a second time (idempotent) does not duplicate entries or
  overwrite existing session registry state.
- [ ] Running `niwa destroy` removes all mesh infrastructure including `sessions.json`,
  inbox directories, and `.mcp.json`.

### Session Registration

- [ ] `niwa session register` in a repo directory registers a session with role derived
  from the repo's last path segment, prints the session ID and role to stdout, and
  creates an inbox directory at `<instance-root>/.niwa/sessions/<session-id>/inbox/`.
- [ ] `niwa session register` with `NIWA_SESSION_ROLE=reviewer` registers with role
  `reviewer` regardless of the repo name.
- [ ] `niwa session register` in the instance root (no repo) registers with role
  `coordinator`.
- [ ] A second `niwa session register` call for the same role while the first session's
  PID is alive returns a non-zero exit code and an error message that includes the
  conflicting PID and the `NIWA_SESSION_ROLE` override hint.
- [ ] A `niwa session register` call for a role whose existing entry has a dead PID
  succeeds silently, replacing the stale entry.
- [ ] Concurrent `niwa session register` calls from multiple processes do not corrupt
  `sessions.json` (no lost update, no partial write).

### Messaging

- [ ] Calling `niwa_send_message` with `to.role = "niwa-worker"` writes a JSON message
  file to the niwa-worker session's inbox directory via atomic rename and returns
  `{"status": "delivered", "id": "<uuid>"}` when the recipient is live.
- [ ] Calling `niwa_send_message` when the recipient is not registered returns
  `{"error_code": "RECIPIENT_NOT_REGISTERED", "detail": "..."}` without creating any
  file.
- [ ] Calling `niwa_check_messages` returns a structured markdown summary for each
  unread message in the calling session's inbox, preserving `id`, `from.role`, `type`,
  `sent_at`, and body fields.
- [ ] Calling `niwa_check_messages` when the inbox is empty returns a "no new messages"
  indicator, not an empty array or null.
- [ ] Sending a message larger than 1 MB returns `{"error_code": "MESSAGE_TOO_LARGE",
  "detail": "..."}` and writes no file.
- [ ] Sending a message with an unrecognized `type` returns a synchronous error response
  and writes no file.
- [ ] A message with `expires_at` set in the past is not returned by
  `niwa_check_messages`; it is moved to `expired/` and an expiry notification is written
  to the sender's inbox.
- [ ] The expiry notification appears in the sender's inbox on the sender's next
  `niwa_check_messages` call and includes the original `id`, `type`, and `sent_at`.

### Observability

- [ ] `niwa session list` prints a table with columns for role, repo, PID, liveness,
  last-heartbeat age, and pending message count for each registered session.
- [ ] `niwa session list` shows `dead` liveness for a session whose PID no longer
  exists, and `live` for one that does.
- [ ] `niwa session log coordinator` lists messages in the coordinator's inbox sorted
  by arrival time.
- [ ] `niwa session log coordinator --since 1h` lists only messages that arrived in the
  last hour.
- [ ] `niwa status` detail view shows a one-line mesh summary when channels are
  configured, and no mesh line when they are not.

## Out of Scope

- **Network/cross-machine transport**: same-machine only for v1. The message schema
  and transport interfaces are designed for future network upgrade, but no network
  transport is implemented.
- **Automated session spawning**: the user opens Claude sessions manually. Niwa
  provisions the channel and injects configuration; it does not start or manage Claude
  processes.
- **MCP Channels as delivery path**: Claude Code's native `claude/channel` protocol is
  the right long-term delivery mechanism — push notifications arrive in session context
  without polling, which is the ideal UX. It is out of scope for v1 because the research
  preview requires `claude.ai` OAuth and `--dangerously-load-development-channels`. The
  v1 design is explicitly forward-compatible: the message schema, role-based addressing,
  and session-side tool names are stable across the delivery path upgrade. When Channels
  graduates from research preview, niwa's provisioning step writes a Channels broker
  entry instead of a stdio MCP server entry; sessions notice no difference.
- **Agent Teams integration**: Anthropic's Agent Teams requires a spawning lead session
  and does not support independently-opened sessions. Not a target for v1.
- **Real-time push delivery via inotify**: `inotify`-based wake-up for instant message
  notification is a v2 enhancement. V1 uses a pull model via the MCP poll tool.
- **Intentional duplicate roles / fan-out delivery**: two sessions sharing the same
  role for broadcast scenarios. V1 enforces uniqueness. Fan-out is a v2 concern.
- **Cross-workspace routing**: sessions in different niwa workspace instances on the
  same machine cannot send messages to each other. Mesh scope is per workspace instance.
- **Message encryption**: messages are plaintext JSON on the local filesystem. Encryption
  is out of scope for same-machine v1.
- **`niwa mesh gc`**: garbage collection of expired artifacts and aged-out messages.
  TTL cleanup runs on `niwa apply` and `niwa session unregister` for v1. A standalone
  GC command is deferred.

## Open Questions

None. All open questions resolved via the decision protocol.

## Known Limitations

- **Pull model latency**: messages arrive in the recipient's context at the next poll
  cycle, not instantly. With a 10-tool-call default cadence at human interaction speeds,
  round-trip latency is typically 30–120 seconds. Users with tighter latency requirements
  can lower the poll cadence in `workspace.toml`, accepting more context-window noise.
- **PID liveness false positives**: on systems with high PID recycling rates, the
  start-time liveness check may still produce false positives in edge cases. The system
  will wrongly treat a new process as the old session, blocking re-registration. The
  recovery path is `niwa session unregister <role>` followed by re-registration.
- **Session self-registration required**: sessions must call `niwa session register` at
  startup (directed by the `workspace-context.md` behavioral instructions). A session
  that opens without reading its CLAUDE.md (e.g., bare mode) will not be registered and
  cannot receive messages until it registers explicitly.
- **Context-window budget**: large message batches returned by `niwa_check_messages`
  consume context-window tokens. Sessions receiving many messages simultaneously (e.g.,
  after an extended offline period) may see significant context usage from the poll
  result.

## Decisions and Trade-offs

**File-based inbox over Unix socket broker as the v1 transport**

A file-based inbox (atomic `rename` into per-session directories) was chosen over a
Unix socket broker. File-based delivery is crash-safe (messages survive process
crashes), requires no daemon or supervised process, and has been validated by community
tools (session-bridge, mcp_agent_mail) at the interaction speeds niwa targets.
A Unix socket broker would provide lower latency and native push semantics but requires
niwa to manage a process lifecycle it currently lacks. The file-based approach can be
replaced by a broker in v2 when push delivery becomes a priority, without changing the
MCP tool interface or message schema.

**Hooks solve idle sessions; Channels is still the future push path**

The key user concern is idle sessions: a Claude session sitting at the prompt waiting
for work has no tool-call cadence, so polling is useless for it. Prototyping confirmed
two things that change the architecture:

First, `notifications/claude/channel` (the Channels push protocol) **cannot** solve
this today. The `--channels` flag cannot be configured via `settings.json` — it is
CLI-flag-only and requires `claude.ai` OAuth. Even with both in place, there is a
confirmed open bug (GitHub #44380) where channel notifications display in the terminal
but the idle REPL does not interrupt to process them. Channels is not a viable
mechanism for waking idle sessions in v1.

Second, Claude Code's hook system solves the idle session problem cleanly. The
`SessionStart` hook fires when a session opens (including resume and `/clear`) and its
response can include `initialUserMessage` — a synthetic first user turn that Claude
processes before any user input. Niwa's `SessionStart` hook calls `niwa session register`
and, if the inbox has pending messages from when the session was offline, injects an
`initialUserMessage` directing Claude to call `niwa_check_messages`. The
`UserPromptSubmit` hook fires when the user types anything into an idle session, and its
`additionalContext` field injects a pending-message reminder into Claude's next context
window. Together these hooks eliminate the idle-session gap without OAuth, without the
Channels flag, and without tmux.

The v1 delivery model is therefore:

1. **Session start / resume**: `SessionStart` hook injects `initialUserMessage` with pending count → Claude calls `niwa_check_messages` immediately
2. **User-activated idle session**: `UserPromptSubmit` hook injects `additionalContext` → Claude sees pending count in next context
3. **Active session (working)**: behavioral instructions in `workspace-context.md` → poll every M tool calls
4. **`notifications/claude/channel` push**: declared as a server capability; fires via fsnotify when new messages arrive; displayed in terminal as a best-effort signal while the idle-wakeup bug exists in Claude Code

The message schema, role-based addressing, and tool names are stable across all four
paths. When Channels lifts the OAuth requirement and fixes the idle-wakeup bug, path 4
becomes the primary mechanism and paths 1–3 become fallbacks, with no session-side changes.

**Error-on-duplicate roles over silent routing**

When two sessions attempt to register the same role, the system rejects the second
registration rather than silently routing to the first-registered session. Duplicate
roles are almost always a user error (two terminals opened in the same repo, or a stale
registry entry after a crash). Silent last-wins or round-robin routing would produce
message loss or split-brain states that are harder to diagnose. The error message
includes a recovery path, keeping the UX friction low.

**Auto-derive role from repo path, not explicit config required**

Roles default to the last path segment of the repo directory (`public/niwa` → `niwa`),
with `coordinator` reserved for the root session. This zero-friction default means new
users get a working mesh without writing any config. Explicit override is available via
`NIWA_SESSION_ROLE` or `[channels.mesh.roles]` in `workspace.toml` for cases where
auto-derive is ambiguous (monorepos, generic repo names).

**Go-native implementation because provisioning integration matters, not just runtime purity**

The most feature-complete community tool (`mcp_agent_mail_rust`) was evaluated as a
potential backend. It is not a fit: its message schema is a flat mail-style model with
no typed routing keys, no structured `from`/`to` objects, no per-message TTL, no
delivery status, and no liveness tracking. Adopting it would require niwa to either
discard the envelope design or maintain a translation layer indefinitely. The Rust
runtime dependency is a secondary concern; the schema incompatibility is the blocker.

The deeper reason to build natively in Go is provisioning integration. Niwa's value is
wiring the mesh into the workspace at `niwa apply` time — that means modifying
`InstanceState.ManagedFiles`, extending the `Applier.runPipeline`, writing
`workspace-context.md` with session-specific behavioral instructions, and transitioning
the delivery path to Channels when it matures. These are tight integrations with niwa's
internals that an external binary cannot provide. A Go-native implementation makes the
provisioner and the broker the same binary, which simplifies the Channels upgrade path:
when niwa writes a Channels server entry, it points at itself rather than coordinating
with an external process. Design patterns from community tools (named agent identities,
async inbox, ACK-based state) are adopted directly; their implementations are not.

**Workspace-scoped broker over machine-global registry**

Community tools (claude-relay, a2a, mcp_agent_mail) use machine-global registries.
Niwa's broker is scoped to the workspace instance (`<instance-root>/.niwa/sessions/`),
so sessions in different workspaces cannot accidentally message each other and workspace
teardown (`niwa destroy`) cleanly removes all mesh state. Cross-workspace messaging is
a future concern that can be addressed with a machine-level router layer if needed.

**Task progress heartbeats are a mandatory protocol, not advisory**

The behavioral instructions in `workspace-context.md` require worker sessions to emit
`task.progress` messages at a defined cadence while executing delegated tasks. Advisory
heartbeats are the failure mode being solved — a coordinator that cannot detect worker
crashes stalls indefinitely. Making heartbeats mandatory surfaces abandonment as an
observable `suspect` state rather than an invisible deadlock. The implementation cost
is one extra instruction in the `## Channels` behavioral section plus one periodic
`niwa_send_message` tool call.

**Pending messages surface at registration time, not discarded on restart**

When a session re-registers after a crash, `niwa session register` includes a pending
message count in its output when the inbox is non-empty, and the `workspace-context.md`
behavioral instructions direct Claude to call `niwa_check_messages` immediately after
registering. The durability guarantee of the file-based inbox is only meaningful if
pending messages are surfaced to the restarting session. Re-execution risk is mitigated
by requiring workers to check git state before acting on a delegated task — an existing
Claude Code best practice.

**Config namespace: `[channels.mesh]`, not a top-level `[mesh]` key**

`WorkspaceConfig.Channels map[string]any` exists in `internal/config/config.go` as a
reserved placeholder for extensible channel configuration. Using `[channels.mesh]`
is consistent with that field name, matches all existing requirement text in this PRD,
and imposes no migration cost. A top-level `[mesh]` key would split the concept and
require a new struct field with no benefit. The distinction between built-in mesh
(`[channels.mesh]`) and future plugin-backed channels (`[channels.telegram]`) is
documented by naming convention.
