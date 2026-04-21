---
status: Proposed
problem: |
  Niwa workspaces contain multiple repos, but the Claude sessions working in each
  repo have no way to communicate with each other without the user acting as a
  manual relay. This design covers how niwa provisions a workspace-aware messaging
  layer so independently-opened sessions can exchange questions, delegate work, send
  review feedback, and post status updates — starting with same-machine, with a
  design path to network transport.
---

# DESIGN: Cross-Session Communication

## Status

Proposed

## Context and Problem Statement

When working across multiple repos in a niwa workspace, a common pattern is to
have one Claude session act as a coordinator (filing issues, reviewing PRs,
orchestrating work) and one or more per-repo sessions act as workers. Currently,
any exchange between these sessions — clarifying questions mid-implementation,
code review feedback, task delegation — requires the user to copy-paste messages
between terminal windows.

The long-term vision is a workspace with N repos and N+1 Claude sessions (one per
repo, one at the workspace root) where the root session can delegate work, field
questions, and follow up on progress without user intermediation.

An exploration round confirmed:

- Demand is validated at high confidence in the Claude Code community (8+ distinct
  GitHub issue threads, canonical issue #24798 labeled `area:core`, never closed as
  "won't fix"). Community workarounds range from bash file-polling scripts to
  production Ed25519-signed HTTP servers.
- Anthropic shipped Agent Teams (Feb 2026, experimental) but it requires sessions
  to be spawned by a single lead. Independently-opened sessions per repo — the niwa
  workspace topology — are not supported.
- Niwa's config schema already has a `[channels]` placeholder in `WorkspaceConfig`
  and a `ChannelMaterializer`-ready provisioning pipeline (`Applier.Create`/`Apply`).
- The `workspace-context.md` injection mechanism already delivers structured context
  into every session via CLAUDE.md — the channel broker address can travel the same
  path without new Claude Code primitives.
- The closest existing analog is gastownhall/gastown (Dec 2025), a Go workspace
  manager with inter-agent mail and nudge primitives provisioned at workspace level.

What's not decided is how to build it: the IPC transport, broker architecture,
integration with Claude Code's own extension points, and config schema design.

## Decision Drivers

- **Zero external dependencies for same-machine use**: The default transport must
  work without running any external service (no Redis, no NATS server, no daemon).
- **Process-restart safety**: Sessions crash and restart due to context bloat. The
  messaging layer must survive individual session restarts without losing messages.
- **Niwa as provisioner, not operator**: Niwa sets up the channel at workspace
  create/apply time. Sessions self-register and discover the channel address from
  the workspace context already injected into their CLAUDE.md.
- **Design path to network transport**: The same-machine implementation should not
  require an architectural rewrite to support cross-machine use later.
- **No new Claude Code primitives required for v1**: The delivery mechanism should
  work today, without depending on MCP channels graduating from research preview or
  Agent Teams supporting independently-opened sessions.
- **Auditable message history**: For debugging and context recovery, messages should
  survive session termination as an inspectable log.

## Decisions Already Made

From exploration (not to be reopened in design):

- **Named pipes, Redis, and nanomsg eliminated**: Named pipes fail on half-duplex
  and blocking-open semantics for N-process meshes. Redis is heavier than NATS with
  no additional benefit for this use case. Nanomsg has no durability. These options
  are off the table.
- **Session identity = workspace instance name + user-assigned role**: PID is a
  tiebreaker only. Routing must not depend on PID (recycled on restart). This maps
  onto niwa's existing `InstanceState` model (`InstanceName`, `InstanceNumber`).
- **File-based queue as the default starting transport**: Zero dependencies,
  crash-safe (atomic `rename` into per-session inbox directories), proven by
  community workarounds. Unix domain sockets are the optional low-latency complement
  for same-machine operation.
- **Phase 1 scopes out automated session spawning**: The communication layer
  (provisioning, registry, transport, schema) is Phase 1. Having niwa spawn and
  supervise Claude processes is a separate, harder problem and a follow-on.

## Open Design Questions

The design must resolve these before implementation:

1. **Transport architecture**: File-based inbox directories, Unix domain socket
   broker, SQLite WAL message store, or a hybrid (files for durability + socket
   for notification)? Each has a different daemon requirement and network-upgrade path.

2. **Broker model**: Does niwa provision a long-lived broker process (started by
   `niwa apply`, PID-tracked)? Or is the system daemonless — sessions communicate
   directly via shared files or a socket they all write to? A broker simplifies
   routing and enables push delivery but requires niwa to manage a process lifecycle
   it currently doesn't have.

3. **MCP channels integration path**: Claude Code's MCP channel primitive
   (`claude/channel` capability) is the most "native" way to push unsolicited
   messages into a running session's context, but currently requires `claude.ai`
   auth and a development flag. Should v1 build on MCP channels (accepting current
   constraints) or an independent transport (more robust today, potentially
   redundant when channels graduate)?

4. **`[channels]` config namespace**: The existing `WorkspaceConfig.Channels`
   placeholder appears designed for plugin-backed external channels (Telegram,
   etc.). Does the built-in inter-session broker live under `[channels.mesh]`
   (alongside `[channels.telegram]`), or in a separate top-level config section
   to avoid semantic confusion?

5. **Session registry concurrency**: Without a daemon, concurrent session writes
   to a `sessions.json` registry require file locking or an atomic-swap protocol.
   If a broker is introduced, it can own the registry. The concurrency model must
   be specified.

6. **Message delivery guarantee**: At-most-once (simplest, no retry), at-least-once
   (sender retries until acked, requires deduplication), or per-message TTL with
   drop on expiry? The schema supports all three via `id`, `reply_to`, and
   `expires_at` fields — the broker behavior must be specified.
