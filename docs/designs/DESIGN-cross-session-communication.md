---
status: Proposed
upstream: docs/prds/PRD-cross-session-communication.md
problem: |
  Niwa workspaces run multiple Claude sessions simultaneously — one per repo, one at
  the root — but those sessions have no way to exchange messages without the user
  acting as a relay. The core technical challenge is waking a session that is not
  actively making tool calls: polling is useless for idle sessions, Claude Code's
  Channels push protocol is not configurable via file-based settings, and there is
  no hook that fires on arbitrary filesystem events. The solution requires a daemon
  that watches session inboxes via fsnotify and resumes idle sessions using
  `claude --resume`, combined with a blocking MCP tool (`niwa_ask`) that holds a
  goroutine open until the target session responds via `niwa_send_message` into the
  caller's inbox.
decision: |
  Niwa provisions a workspace-scoped session mesh at `niwa apply` time via a new
  ChannelMaterializer. The materializer writes a file-based inbox tree under
  `.niwa/sessions/`, an MCP server entry in `.claude/.mcp.json`, SessionStart and
  UserPromptSubmit hook scripts, and a `## Channels` section in workspace-context.md.
  A persistent daemon (`niwa mesh watch`) watches all session inboxes via fsnotify
  and resumes idle sessions via `claude --resume <claude-session-id>` when messages
  arrive for dead PIDs. The MCP server exposes four tools: `niwa_check_messages` and
  `niwa_send_message` (stateless), and `niwa_ask` and `niwa_wait` (blocking goroutines
  that hold the tool call open until inbox events arrive). Sessions always respond via
  `niwa_send_message` — never via stdout — so the response path is identical whether
  the session was woken by the daemon or by Claude Code's future Channels push protocol.
rationale: |
  The response-via-tool-call constraint is the key forward-compatibility invariant:
  when Channels can wake sessions natively, the daemon's `claude --resume` step is
  removed but the response detection path (`niwa_ask` watching the inbox) is unchanged.
  A file-based inbox was chosen over a Unix socket broker because it is crash-safe,
  requires no broker process to be live for message durability, and can be inspected
  with standard tools. The daemon is stateless (no in-memory message state) so it
  can be restarted without data loss. `niwa_ask` blocking at the MCP tool layer means
  sessions never need polling instructions — they call `niwa_ask` when they need input
  and block naturally, matching how any other tool call works.
---

# DESIGN: Cross-Session Communication

## Status

Proposed

## Context and Problem Statement

When working across multiple repos in a niwa workspace, a common pattern is one Claude
session acting as coordinator (at the workspace root) and per-repo sessions acting as
workers. Currently any exchange — clarifying questions, task delegation, code review
feedback — requires the user to copy-paste messages between terminal windows. The user
is the relay. Parallel multi-session workflows become impractical beyond two sessions.

The technical problem has two distinct halves:

**Transport and routing.** Sessions need to address each other by role (`coordinator`,
`koto`, `shirabe`) without knowing process IDs, socket paths, or session file locations.
The transport must survive session crashes (messages must not live only in memory) and
must work without a network connection.

**Idle session wakeup.** A session at the REPL making zero tool calls will never poll
its inbox. Three mechanisms were evaluated: Claude Code's `notifications/claude/channel`
push protocol requires `claude.ai` OAuth and a CLI flag that cannot be set in any
config file, and has a confirmed bug where idle REPLs display the notification but do
not process it; Claude Code hooks (`SessionStart`, `UserPromptSubmit`) fire only at
session open or on user input, not on arbitrary inbox events; tmux keypress injection
requires sessions to run inside tmux. The only mechanism that works for any idle Claude
session, regardless of how it was opened, is `claude --resume <session-id>` — which
resumes the session with a new user prompt, fires the `SessionStart` hook, and delivers
pending messages via `initialUserMessage`.

The design must connect these two halves: file-based delivery (transport) + daemon that
calls `claude --resume` (wakeup) + blocking MCP tools (session-side API that hides the
complexity) + forward-compatible response routing (response always via `niwa_send_message`
into the inbox, never via stdout, so the Channels upgrade is transparent).

The implementation touches: `internal/workspace/apply.go` (new materializer),
`internal/workspace/workspace_context.go` (new Channels section),
`internal/mcp/server.go` (new blocking tools), `internal/cli/session_register.go`
(Claude session ID registration), new `internal/cli/mesh_watch.go` (daemon), and
`internal/config/config.go` (concrete `[channels.mesh]` config struct).

## Decision Drivers

- Sessions must receive messages and wake up without polling and without user intervention
- Responses travel via `niwa_send_message` tool calls, never stdout — this is the forward-compat invariant for the Channels upgrade path
- Pure Go, no external runtime dependencies beyond stdlib and fsnotify (already in go.mod)
- Crash-safe: messages survive daemon and session crashes (file-based inbox)
- Stateless daemon: restartable without data loss or re-registration
- Claude session ID must be discoverable from within a Claude Code session's shell environment at registration time
- ChannelMaterializer integrates cleanly with the existing `Applier.runPipeline` step 6.5 without breaking existing materializers
- The daemon lifecycle must be manageable as part of workspace lifecycle (`niwa apply` starts, `niwa destroy` stops)
- `niwa_ask` must not leak goroutines on timeout

## Considered Options

_Written during Phase 3 cross-validation._

## Decision Outcome

_Written during Phase 3 cross-validation._

## Solution Architecture

_Written during Phase 4._

## Implementation Approach

_Written during Phase 4._

## Security Considerations

_Written during Phase 5._

## Consequences

_Written during Phase 4._
