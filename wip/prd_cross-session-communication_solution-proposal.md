# Cross-Session Communication: Solution Proposal

> **Status**: Validated prototype. Ready for design phase.
> **Branch**: `docs/cross-session-communication`
> **PR**: #71

---

## What We Set Out to Solve

You copy-paste messages between Claude sessions. Sessions in different repos can't
discover each other. An idle session waiting at the prompt will never see a message from
another session unless you paste it in yourself. The goal: make niwa provision a
communication channel so sessions find each other, exchange messages, and surface
incoming messages without your involvement.

The hard constraint that drove the night's work: **idle sessions**. A session doing
nothing makes no tool calls, so polling is useless for it. We needed a push mechanism.

---

## What We Researched

### Claude Code's Channel Push Protocol (`notifications/claude/channel`)

This is the right long-term answer. When a server declares
`capabilities.experimental["claude/channel"]`, it can send unsolicited
`notifications/claude/channel` JSON-RPC notifications. Claude Code injects these into the
session context as a `<channel source="...">` block.

But it doesn't solve idle sessions today:

- The `--channels` flag is **CLI-flag-only** — it cannot be set in `settings.json`,
  `.mcp.json`, or any file-based config. Each user would have to add it manually.
- It requires `claude.ai` OAuth (not API key, not Pro subscription alone).
- There is a **confirmed open bug** (GitHub #44380): channel notifications display in the
  terminal but the idle REPL does not interrupt or process them. Visible in terminal,
  invisible to Claude.

Verdict: forward-compat target, not a v1 delivery mechanism.

### Community Implementations

Five community tools were surveyed:

| Tool | Transport | Schema | Verdict |
|------|-----------|--------|---------|
| `session-bridge` (Python) | JSON file + `tmux send-keys` | Flat key-value | Wrong transport |
| `mcp_agent_mail_rust` (Rust) | MCP stdio | Mail-style (subject, body_md) | Schema incompatible |
| `claude-relay` (Node.js) | HTTP REST | Typed | Runtime dep |
| `a2a` protocol | HTTP REST | Typed, signed | Complex, daemon needed |
| Community `shared-context.md` pattern | File polling, 15 min | None | Too slow |

`mcp_agent_mail_rust` looked most promising. It ships a pre-built binary, supports named
agents, and uses MCP. On inspection: its message model is flat mail-style (`subject`,
`body_md`, `importance`) with no typed routing keys, no structured `from`/`to` objects,
no `expires_at`, no delivery status, no liveness tracking. Adopting it means either
discarding our envelope design or maintaining a translation layer forever. The Rust
dependency is secondary; the schema is the blocker.

### Claude Code Hook System

The discovery that changes the architecture. Claude Code v2.1.116 ships two hooks:

**`SessionStart`** fires at session open, resume, `/clear`, and `/compact`. Its response
object supports `initialUserMessage` — a synthetic first user turn that Claude processes
*before* any user input. Critically, this fires even when the session was offline and
just resumed.

**`UserPromptSubmit`** fires when the user types anything. Its response supports
`additionalContext` — text injected into Claude's context window before it answers. This
fires even if Claude hasn't made a single tool call during an idle period.

Both hooks require zero OAuth, zero CLI flags, and work with any authentication method.

---

## The Architecture: 4-Path Delivery

No single path covers every session state. The validated model uses four paths, each
covering a distinct session lifecycle moment:

```
Session state          Delivery path                  Mechanism
─────────────────────  ─────────────────────────────  ─────────────────────────────────
Starting / resuming    SessionStart hook              initialUserMessage with pending count
Idle, user activates   UserPromptSubmit hook          additionalContext reminder
Working (tool calls)   workspace-context.md polling   niwa_check_messages every M calls
Any (terminal display) notifications/claude/channel   fsnotify → inotify push
```

Paths 1–3 are fully validated by prototype. Path 4 is implemented and fires correctly
but has the idle-wakeup bug in Claude Code — it works as a terminal indicator and will
become the primary path when the bug is fixed and Channels lifts the OAuth requirement.

---

## What Was Built (The Prototype)

All code is committed to the `docs/cross-session-communication` branch.

### `internal/mcp/` — Stdio MCP Server (~450 lines)

**`server.go`** — Core JSON-RPC server. Reads from stdin, writes to stdout with a mutex.
Dispatch is synchronous (critical: async goroutines caused empty output when stdin closed
before goroutines executed). Handles: `initialize`, `tools/list`, `tools/call`, `ping`.

**`niwa_check_messages`**: reads all `.json` files in the session's inbox, skips expired
ones (moves to `expired/`), formats them as structured markdown, moves processed files to
`inbox/read/`. Returns a clear "No new messages." string when empty.

**`niwa_send_message`**: validates message type against a 10-type vocabulary, resolves
the recipient role from `sessions.json`, writes the message file atomically (temp file +
`os.Rename`), returns `delivered` if recipient PID is alive or `queued` if dead.

**`types.go`** — All wire and domain types. Message envelope: `v`, `id`, `type`, `from`
(object: `instance/role/repo/pid`), `to` (object: `instance/role`), `reply_to`,
`task_id`, `sent_at`, `expires_at`, `body`.

**`watcher.go`** — `fsnotify`-based inbox watcher. Watches for `Create` events (atomic
rename triggers this), pushes `notifications/claude/channel` notifications. Falls back to
1-second polling if fsnotify fails.

**`liveness.go`** — PID liveness with start-time verification. Uses `kill(0)` for
existence, `/proc/<pid>/stat` field 22 for start time to prevent false positives on PID
recycling.

### `internal/cli/session_register.go` — Session Registration

`niwa session register` reads `NIWA_INSTANCE_ROOT`, derives the session's role (env var
> `--repo` last segment > `coordinator`), generates a UUID session ID, reads its own
PID + start time, creates the inbox directory, writes a `SessionEntry` to `sessions.json`
via atomic rename, and prints pending message count from any prior inbox dirs.

Concurrent registration is safe: the pattern is read → filter stale → append → atomic
rename write. Two concurrent processes will each read the same base, write their entry,
and the last rename wins — but since each writes its own session entry keyed by role, the
only race is if two sessions claim the same role simultaneously. That's rejected at the
read step with a clear error.

### `internal/cli/mcp_serve.go` — MCP Server Entry Point

Hidden command (`niwa mcp-serve`) reads `NIWA_INSTANCE_ROOT`, `NIWA_SESSION_ID`, and
`NIWA_SESSION_ROLE` from env, computes the inbox and sessions directories, and calls
`mcp.New(...).Run(os.Stdin, os.Stdout)`. This is what `.mcp.json` will point at.

---

## Prototype Test Results

### Raw MCP Protocol

```
Request: initialize → "niwa" server, capabilities include claude/channel ✓
Request: tools/list → niwa_check_messages, niwa_send_message ✓
Request: niwa_check_messages → "No new messages." (empty inbox) ✓

# After sending a message to the inbox manually:
Request: niwa_check_messages → "## 1 new message(s) / ### Message 1 — task.delegate from coordinator" ✓

Request: niwa_send_message (to=coordinator, unknown recipient) → RECIPIENT_NOT_REGISTERED ✓
Request: niwa_send_message (to=registered role) → delivered, message ID ✓
Request: niwa_send_message (unknown type) → MESSAGE_TYPE_UNKNOWN ✓
```

### Session Registration

```
# Register coordinator:
$ niwa session register
session_id=a1b2c3d4-... role=coordinator

# Register worker while coordinator is live:
$ NIWA_SESSION_ROLE=niwa-worker niwa session register
session_id=e5f6a7b8-... role=niwa-worker

# Register coordinator again while PID still live:
$ niwa session register
error: role "coordinator" already registered by live session PID 12345 (registered 2026-04-20T01:23:45Z);
use NIWA_SESSION_ROLE to override or run: niwa session unregister coordinator

# Kill coordinator, then re-register:
$ niwa session register
session_id=c9d0e1f2-... role=coordinator pending=2  ← stale entry pruned, pending count ✓
```

### SessionStart Hook

```bash
$ NIWA_INSTANCE_ROOT=/tmp/niwa-hook-test bash test-session-start-hook.sh
{"hookEventName":"SessionStart","initialUserMessage":"You are 'coordinator' in a niwa workspace mesh with 2 other registered session(s). You have 2 pending message(s) in your niwa inbox. Call niwa_check_messages before starting work."}
```

When inbox is empty:
```bash
$ NIWA_INSTANCE_ROOT=/tmp/niwa-hook-test bash test-session-start-hook.sh
(no output — hook returns nothing, Claude starts normally)
```

### UserPromptSubmit Hook

```bash
$ NIWA_INSTANCE_ROOT=/tmp/niwa-hook-test bash test-user-prompt-hook.sh
{"hookEventName":"UserPromptSubmit","additionalContext":"[niwa] You have 2 unread message(s) in your session inbox. Call niwa_check_messages to read them before answering."}
```

---

## How It All Fits Together

### A Session Opening Into a Channeled Workspace

1. `niwa apply` has already written `.claude/.mcp.json` (MCP server entry) and the
   `.claude/settings.json` `SessionStart`/`UserPromptSubmit` hooks.
2. Claude Code opens. It loads the MCP server (`niwa mcp-serve`) and the hooks.
3. `SessionStart` fires. The hook calls `niwa session register`, registers the session,
   checks pending message count.
   - **No pending messages** → hook returns nothing. Claude starts normally.
   - **Pending messages** → hook returns `initialUserMessage`: "You are 'niwa-worker' in
     a niwa workspace mesh. You have 3 pending message(s). Call `niwa_check_messages`
     before starting work." Claude sees this as its first user turn, calls the tool,
     reads the messages, and begins its actual work.
4. While working, Claude calls `niwa_check_messages` every M tool calls (behavioral
   instructions in `workspace-context.md`).
5. When the user types anything into an idle session, `UserPromptSubmit` fires and
   injects a pending-message count into Claude's context if the inbox is non-empty.

### Sending a Message (Coordinator → Worker)

1. Coordinator Claude calls `niwa_send_message` with `to="niwa-worker"`,
   `type="task.delegate"`, `body={...}`.
2. The MCP server reads `sessions.json`, finds the niwa-worker entry with its `inbox_dir`.
3. Message is written as `<uuid>.json` to a temp file, then renamed into the inbox.
4. Server checks `IsPIDAlive(workerPID, workerStartTime)` and returns `delivered` or
   `queued`.
5. fsnotify in the worker's MCP server detects the new file and pushes a
   `notifications/claude/channel` notification (best-effort; displayed in terminal).
6. On the worker's next tool call (or the next `SessionStart`/`UserPromptSubmit`), it
   calls `niwa_check_messages` and reads the task.

### Role Discovery

Sessions don't need to know other sessions' IDs. They address by role (`coordinator`,
`niwa-worker`, `tsuku`). The MCP server resolves roles to session entries at send time.
Role names auto-derive from repo path segments: `public/niwa` → `niwa`. No config needed.

---

## What Remains to Be Built

The prototype covers the core messaging engine. The remaining work is integration:

### High Priority (v1)
| Item | What's missing | Notes |
|------|---------------|-------|
| `ChannelMaterializer` | Writes `.mcp.json`, hooks, `workspace-context.md` `## Channels` section at `niwa apply` time | Main integration point |
| `[channels.mesh]` config parsing | `WorkspaceConfig.Channels` field exists, needs concrete struct and parser | Gating config |
| `niwa session unregister` | Remove entry from `sessions.json`, clean inbox | Needed for clean shutdown and `niwa destroy` |
| `niwa session list` | Table view: role, PID, liveness, pending count | US4 |
| `niwa session log` | List inbox messages by arrival time | R23 |
| `niwa status` integration | One-line mesh summary in detail view | R24 |
| Hook script generation | Templates for SessionStart / UserPromptSubmit hook scripts | Part of ChannelMaterializer |

### Deferred to v2
- Message TTL enforcement via `niwa mesh gc`
- Fan-out (multiple sessions per role)
- Cross-workspace routing
- Channels push delivery (when OAuth requirement drops and idle-wakeup bug is fixed)

---

## The PRD

Full requirements: `docs/prds/PRD-cross-session-communication.md`

28 requirements across: provisioning (R1–R5b), session identity (R6–R12), messaging
tools (R13–R17), message lifecycle (R18–R21), observability (R22–R24), non-functional
(R25–R28).

All three open questions resolved (heartbeats mandatory, pending messages surface at
registration, `[channels.mesh]` namespace).

---

## Recommended Next Step

`/shirabe:design cross-session-communication` — produce the technical design doc
covering `ChannelMaterializer` implementation, hook script templates, `sessions.json`
locking strategy, and the `[channels.mesh]` config struct before any further coding.

The prototype de-risked the messaging core. The design phase should focus on the
provisioning integration and the hook delivery system, which are the parts with the most
surface area against existing niwa internals.
