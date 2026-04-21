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
  directed messages by role without the user acting as an intermediary. When a session
  needs input from another session, it calls a blocking ask tool that returns the answer
  directly — no polling, no inbox management, no timing coordination. The mesh daemon
  handles routing and wakes idle sessions as needed. The user can observe mesh state with
  a single command and receive structured error feedback when something goes wrong.
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
2. Sessions can send directed messages to each other by role. When a session needs input
   from another session, it calls a blocking ask tool that waits for the answer — the
   calling session does not poll or manage delivery timing itself.
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
As a worker Claude session mid-implementation, I want to call a blocking ask tool that
sends my question to the coordinator and returns the answer directly so that I can
continue working immediately — without polling, without managing inbox timing, and
without the user copy-pasting between terminals.

**US6 — Coordinator waits for multiple workers**
As a coordinator Claude session that has delegated tasks to several workers, I want to
block on a single wait call until all expected results arrive so that I can aggregate
them and report to the user without writing a polling loop.

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
`workspace-context.md` containing: the sessions registry path, the available MCP tool
names (`niwa_check_messages`, `niwa_send_message`, `niwa_ask`, `niwa_wait`), the
registration command, the session's assigned role, and behavioral instructions directing
Claude to: (a) call `niwa_check_messages` immediately when woken by the daemon with
pending messages; (b) use `niwa_ask` when a blocking answer is required from another
session — do not poll or manage inbox timing manually; (c) use `niwa_send_message` for
one-way messages (task.result, task.progress, session.bye) where no synchronous reply is
expected; (d) always deliver answers and task results via `niwa_send_message` tool calls,
never as plain conversational output — the mesh daemon detects responses in the inbox, not
in stdout; (e) emit `task.progress` messages at a defined cadence (default: every 5
minutes of wall time or every 20 tool calls, whichever comes first) while executing a
delegated task.

**R5a** — The `ChannelMaterializer` shall write a `SessionStart` hook into each session's
`.claude/settings.json`. The hook shall call `niwa session register` (updating the PID
and Claude session ID on every start or resume) and, if pending messages exist, return a
JSON response with `initialUserMessage` directing Claude to call `niwa_check_messages`
before doing anything else. This fires both when a user opens a session manually and when
the daemon wakes an idle session via `claude --resume` — in both cases, pending messages
are surfaced as the session's first action.

**R5b** — The `ChannelMaterializer` shall write a `UserPromptSubmit` hook as a fallback
for interactive (coordinator-style) sessions. When the user types anything, the hook
checks the inbox and injects an `additionalContext` reminder if unread messages are
present. This applies only when messages arrive for an interactive session that the
daemon cannot resume (because its PID is alive). It is not the primary delivery path.

### Session Identity and Registration

**R6** — `niwa session register` shall be a new CLI subcommand under a `niwa session`
subcommand group. It shall accept a `--repo <name>` flag (defaulting to the repo
inferred from the current working directory) and write a `SessionEntry` to
`sessions.json` under `<instance-root>/.niwa/sessions/`. The entry shall include the
niwa session UUID, role, PID, process start time, inbox directory path, registration
timestamp, and Claude Code session ID (see R32). It shall print the assigned session ID
and role to stdout on success.

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

**R13** — `niwa mcp-serve` shall be a new CLI subcommand that starts a stdio MCP server
exposing four tools: `niwa_check_messages`, `niwa_send_message`, `niwa_ask`, and
`niwa_wait`. The server shall be stateless across tool calls for the two one-way tools
(`niwa_check_messages`, `niwa_send_message`) — reading from and writing to the filesystem
with no in-memory state between calls. The two blocking tools (`niwa_ask`, `niwa_wait`)
hold an open goroutine watching the inbox until the reply or timeout condition is met.

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
of either `queued` (recipient PID dead at send time) or `delivered` (recipient PID alive
at send time), plus the assigned message ID. `niwa_send_message` is for one-way messages
where the sender does not wait for a reply. For request-reply exchanges, use `niwa_ask`
(R30).

**R16** — `niwa_send_message` shall reject messages with unrecognized `type` values
synchronously with a structured error response. Defined type vocabulary for v1:
`question.ask`, `question.answer`, `task.delegate`, `task.ack`, `task.result`,
`task.progress`, `review.feedback`, `status.update`, `session.hello`, `session.bye`.

**R17** — `niwa_send_message` shall return structured error objects with a
machine-readable `error_code` field and a human-readable `detail` string. Defined error
codes: `MESH_NOT_PROVISIONED`, `RECIPIENT_NOT_REGISTERED`, `RECIPIENT_OFFLINE` (inbox
exists, PID dead), `INBOX_UNWRITABLE`, `MESSAGE_TOO_LARGE`. All send errors shall be
non-fatal to the calling session.

**R29** — `niwa mesh watch` shall be a persistent daemon scoped to the workspace instance.
It shall watch all session inbox directories via fsnotify and, when a message arrives for
a session whose PID is dead, resume that session using `claude --resume <claude-session-id>`
as a background subprocess so the session processes its pending messages and responds. When
a message arrives for a session whose PID is alive, the daemon shall take no action — the
message waits in the inbox for the live session's `niwa_ask` reply-watch goroutine or its
next `niwa_check_messages` call. The daemon is stateless: if it crashes, messages are not
lost; restarting it resumes normal operation. The daemon's lifecycle (start on provision,
stop on destroy) is managed by `niwa apply` / `niwa destroy`.

**R30** — `niwa_ask` shall be a blocking MCP tool that sends a `question.ask` message to a
target role and does not return until the target responds with a `question.answer` bearing a
matching `reply_to`. Internally, `niwa_ask` writes the question to the target's inbox,
then watches the caller's own inbox for the reply. The daemon is responsible for waking the
target if its PID is dead. `niwa_ask` shall accept: `to` (role string), `body` (question
payload), and optionally `timeout` (ISO 8601 duration, default 10 minutes) and `task_id`.
It shall return the full response body on success, or a structured timeout error if no reply
arrives within the deadline.

**R31** — `niwa_wait` shall be a blocking MCP tool that returns when one or more messages
matching a filter arrive in the calling session's inbox. It shall accept: `types` (list of
message types to accept), `from` (optional list of sender roles), `count` (minimum number of
matching messages to collect before returning, default 1), and `timeout` (ISO 8601 duration).
The coordinator uses `niwa_wait` after delegating tasks to multiple workers: it specifies
`types=["task.result"]`, `from=["koto","shirabe"]`, `count=2`, and blocks until both results
arrive, rather than polling in a loop.

**R32** — `niwa session register` shall record the session's Claude Code session ID in
`SessionEntry` alongside the niwa UUID, PID, and start time. The Claude session ID is
required by the daemon to resume idle sessions via `claude --resume`. The registration hook
shall locate the session ID from the environment variable `CLAUDE_SESSION_ID` if set, or
from the most-recently-modified session file under `~/.claude/projects/<encoded-cwd>/`. If
the session ID cannot be determined, the `claude_session_id` field shall be left empty and
the daemon shall skip resume for that session, falling back to SessionStart hook delivery
when the session next opens manually.

**R33** — Sessions shall deliver all answers and task results via `niwa_send_message` tool
calls. Responding via plain conversational output is not a valid delivery mechanism: the
daemon detects responses in the recipient's inbox file, not in the subprocess stdout.
This constraint is a forward-compatibility requirement: when Claude Code's Channels protocol
can wake sessions natively (replacing `claude --resume`), the response path is identical —
the awakened session calls `niwa_send_message`, and the waiting `niwa_ask` goroutine detects
the reply in the inbox regardless of how the session was woken. Workspace-context.md
behavioral instructions (R5) shall make this explicit.

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
`claude/channel` delivery protocol. The upgrade path is: when Channels can wake idle
sessions natively, the daemon's `claude --resume` step is removed and a Channels push
notification takes its place. The response path is unchanged in both cases: the awakened
session calls `niwa_send_message`, and the waiting `niwa_ask` goroutine detects the reply
in the inbox. The message envelope schema, role-based addressing model, tool names, and
behavioral instructions shall require no changes when the wakeup mechanism changes. The
`ChannelMaterializer` shall write the MCP server entry in a way that supports this
transition in a subsequent `niwa apply`.

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
- [ ] Calling `niwa_ask` writes a `question.ask` message to the target's inbox, blocks
  the tool call, and returns the response body when the target calls `niwa_send_message`
  with a matching `reply_to`.
- [ ] `niwa_ask` returns a structured timeout error if no reply arrives within the
  specified timeout without leaving any dangling goroutines.
- [ ] Calling `niwa_wait` with `types=["task.result"]`, `from=["koto","shirabe"]`,
  `count=2` blocks until two `task.result` messages from those roles arrive, then returns
  both as a batch.
- [ ] `niwa session register` records the Claude session ID in `sessions.json` when
  `CLAUDE_SESSION_ID` is set; leaves `claude_session_id` empty and logs a warning when it
  cannot be determined.

### Daemon

- [ ] `niwa mesh watch` starts without error and watches all inbox directories under
  `<instance-root>/.niwa/sessions/`.
- [ ] When a `*.json` message file appears in a session's inbox whose PID is dead, the
  daemon runs `claude --resume <claude-session-id>` as a background subprocess within 2
  seconds of file creation.
- [ ] When a message arrives for a session whose PID is alive, the daemon takes no action
  and the file remains in the inbox.
- [ ] If the daemon crashes and restarts, no messages are lost and no sessions are
  double-resumed.
- [ ] `niwa destroy` terminates the daemon and removes its PID file.

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
- **Initial session spawning**: the user opens Claude sessions manually in each repo.
  The daemon resumes idle sessions (`claude --resume`) when messages arrive for them, but
  does not create sessions that have never been opened. Starting the initial session for
  each role is a user action, not a niwa action.
- **MCP Channels as wakeup path**: Claude Code's native `claude/channel` protocol is the
  right long-term wakeup mechanism — push notifications arrive without requiring
  `claude --resume`. It is out of scope for v1 because it requires `claude.ai` OAuth and
  a CLI flag that cannot be set via config files, and has a confirmed open bug where
  idle REPLs display the notification but do not process it. The v1 design is explicitly
  forward-compatible: when Channels lifts those constraints, the daemon's `claude --resume`
  step is replaced by a Channels push, with no change to the response path (sessions still
  respond via `niwa_send_message` into the inbox).
- **Agent Teams integration**: Anthropic's Agent Teams requires a spawning lead session
  and does not support independently-opened sessions. Not a target for v1.
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

- **Busy session serialization**: the daemon only resumes sessions whose PID is dead. If a
  session is actively running when a message arrives for it, the message queues in the inbox
  and is not delivered until the session exits and the daemon resumes it. Concurrent message
  injection into a running session is not supported in v1. In practice this means if session
  A is blocked in `niwa_ask` waiting for B, and C simultaneously sends a message to A, C's
  message waits until A finishes its exchange with B. This is the accepted tradeoff for the
  `claude --resume` wakeup model.
- **Live coordinator delivery gap**: the daemon cannot inject messages into a live
  interactive session (the coordinator at the REPL). Messages for a live coordinator queue
  in the inbox and surface via the `UserPromptSubmit` hook when the user next types
  something, or when the coordinator calls `niwa_wait` explicitly. This is acceptable
  because the coordinator is the session the user is watching.
- **Claude session ID discovery**: if `CLAUDE_SESSION_ID` is not set in the environment
  and the session file cannot be reliably determined (e.g., multiple recent sessions for
  the same directory), the daemon cannot resume the session and falls back to SessionStart
  hook delivery on next manual open.
- **PID liveness false positives**: on systems with high PID recycling rates, the
  start-time liveness check may still produce false positives in edge cases. The system
  will wrongly treat a new process as the old session, blocking re-registration. The
  recovery path is `niwa session unregister <role>` followed by re-registration.
- **Session self-registration required**: sessions must call `niwa session register` at
  startup (via the SessionStart hook). A session that opens without loading its CLAUDE.md
  (e.g., bare mode) will not be registered and cannot send or receive messages until it
  registers explicitly.
- **Context-window budget**: a session resumed by the daemon with many queued messages will
  consume context-window tokens reading them. Sessions with extended offline periods may see
  significant context usage from the first `niwa_check_messages` call after resume.

## Decisions and Trade-offs

**File-based inbox over Unix socket broker as the v1 transport**

A file-based inbox (atomic `rename` into per-session directories) was chosen over a
Unix socket broker. File-based delivery is crash-safe (messages survive process
crashes) and inspectable with standard tools. The daemon (`niwa mesh watch`) is a
required process for idle-session wakeup, but it is stateless — it reads from and writes
to the filesystem and can be restarted without message loss or coordination. A Unix
socket broker would be the broker and the transport in one process, creating a single
point of failure. The file-based approach separates durability (inbox files) from
delivery (daemon + `claude --resume`), so each can fail independently.

**Daemon-managed wakeup via `claude --resume`; response always via tool call**

The fundamental problem with idle sessions is that a session at the REPL makes no tool
calls, so polling-based delivery never fires. Three alternative mechanisms were evaluated:

*`notifications/claude/channel`* cannot solve this today. The `--channels` flag is
CLI-flag-only (cannot be configured via `settings.json`) and requires `claude.ai` OAuth.
There is also a confirmed open bug where idle REPLs display channel notifications but do
not process them. Channels is the right long-term mechanism but is not viable in v1.

*Hook-based delivery* (SessionStart `initialUserMessage`, UserPromptSubmit
`additionalContext`) works for specific moments: session open/resume, or when the user
manually types something. But if an idle session (PID alive, at the REPL) receives a
message from another agent, neither hook fires. The user must intervene.

*Daemon + `claude --resume`* is the correct solution. The daemon watches all session
inboxes via fsnotify. When a message arrives for a session whose PID is dead, the daemon
runs `claude --resume <claude-session-id>` as a background subprocess. The SessionStart
hook fires, detects pending messages, and injects an `initialUserMessage` directing
Claude to call `niwa_check_messages`. The session reads its messages, responds via
`niwa_send_message`, and exits. The calling session's `niwa_ask` goroutine detects the
reply in its inbox and returns the answer to Claude.

The critical design choice is that **responses always travel through `niwa_send_message`
into the inbox, never via subprocess stdout**. This is a forward-compatibility
requirement: when Claude Code's Channels protocol can wake sessions natively, the daemon's
`claude --resume` step is replaced by a Channels push notification, but the response path
is identical — the awakened session calls `niwa_send_message`, and the waiting `niwa_ask`
goroutine detects the reply in the inbox. No session-side changes are needed when the
wakeup mechanism changes.

The v1 delivery model is:

1. **Message for idle session** (PID dead): daemon detects inbox file → runs
   `claude --resume` → SessionStart hook injects `initialUserMessage` → session calls
   `niwa_check_messages` → session responds via `niwa_send_message` → exits
2. **Message for live coordinator** (interactive, PID alive): message queues → surfaces
   via `niwa_wait` if coordinator is blocking, or `UserPromptSubmit` hook if user types
3. **Future Channels wakeup**: Channels push replaces step 1's `claude --resume` → session
   responds via `niwa_send_message` (unchanged) → `niwa_ask` detects reply (unchanged)

Sessions do not poll. They are started with a task, use `niwa_ask` when they need input,
emit progress via `niwa_send_message`, and exit when done.

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
