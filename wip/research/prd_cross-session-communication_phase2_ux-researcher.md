# Phase 2 Research: UX Researcher

## Lead 1: UX Patterns for Pull-Based Message Delivery

### Findings

**How existing tools surface messages to a running agent session**

Gas Town (gastownhall/gastown) is the closest existing reference. It provisions `mail` and `nudge` primitives at workspace creation time: each agent has an inbox directory, and `nudge` sends a filesystem-level notification that wakes a polling agent. Gas Town's lesson is that the surfacing mechanism matters as much as the transport. Agents that poll on a timer (every 15+ minutes, as reported in community workarounds using shared markdown files) miss messages during active work periods and receive stale ones during idle periods. Timer-based polling creates a bimodal failure: too fast and it clutters context, too slow and it defeats the purpose.

Community workarounds follow three patterns, in order of sophistication:
1. Shared markdown files with manual refresh (high latency, no structure, prone to merge conflicts)
2. tmux send-keys injection (low latency but brittle — assumes terminal layout, breaks on resize)
3. Custom HTTP servers or FastAPI services (low latency, structured, but require a persistent daemon the user must manage separately)

None of these is workspace-native. The user must set them up per-project and teach each Claude session how to use them through manual system prompt editing.

**What a Claude session needs to know to participate in the mesh**

From the existing codebase research: niwa already injects `workspace-context.md` into each session's CLAUDE.md via an `@import` directive. This is the right delivery path for channel configuration. A session needs at minimum:
- The socket path (or equivalent broker address) for the workspace instance
- Its own role within the mesh
- The message types it should expect to receive and how to respond to them

This breaks into two components: structural knowledge (broker address, own role) delivered once via `workspace-context.md`, and behavioral knowledge (how to check for messages, how to send them) delivered via system prompt instructions that travel alongside the channel config.

The system prompt section is non-optional. Without explicit instructions, Claude will not poll for messages proactively — it will respond to them only if a user asks, which defeats the purpose. The instructions must tell Claude: (a) how often to check, (b) what tool to call, (c) what to do when a message arrives, and (d) how to signal receipt.

**Pull model specifics: polling cadence and triggering**

The pull model requires Claude to decide when to check. Three triggering strategies:

1. **Periodic (every N tool calls)**: predictable latency, easy to reason about. The downside is that in heavy tool-use sessions (file editing, running tests), N=5 tool calls might mean a check every 30 seconds. In idle sessions, it might mean never. The count-based trigger doesn't map well to wall-clock time.

2. **On idle**: checking when Claude finishes a unit of work and is about to ask the user for input. This is the lowest-friction point — Claude is already "stopping" — and aligns with how Gas Town's nudge primitive works. The risk is that "idle" is ambiguous: Claude may pause for 3 seconds between tool calls without truly being idle.

3. **On explicit user request**: lowest latency when the user knows a message should be there, highest latency otherwise. Works as a fallback but cannot be the primary mechanism if the goal is reducing user-as-relay burden.

The practical recommendation from studying multi-agent frameworks is a hybrid: check on idle (at natural task boundaries) plus a count-based backstop (every M tool calls, where M is configurable). This mirrors how CrewAI handles the delegation loop — workers don't poll in a tight loop, they check when transitioning between states.

**What the notification looks like inside Claude's context window**

Three formats are possible when a message arrives:

1. **Raw JSON**: complete and inspectable, but noisy. Claude will spend context processing envelope fields it doesn't need for most message types.

2. **Formatted summary with structured fields**: a short markdown block that calls out sender, type, and the key body field. For a `question.ask`, this might be:
   ```
   [MESSAGE from coordinator | question.ask | id: q-7f3a2b1c]
   Question: Should the channel broker persist messages to disk?
   Context: Implementing unix socket broker in internal/channel/.
   Reply by: 2026-04-20T12:31:00Z
   ```
   This is what channels-style MCP notifications produce today. It lets Claude react without parsing JSON.

3. **Directive injection**: the message is reformatted as an imperative instruction ("The coordinator has asked: ..."). This is highest-context-efficiency but loses the structured metadata (id, reply_to) that Claude needs to send a properly correlated response.

Format 2 (formatted summary) is the right default. It preserves the `id` needed for `reply_to` correlation while giving Claude a human-readable frame. The MCP tool result — what Claude actually reads after calling the poll tool — should use this format. The raw JSON should be available as a secondary field for debugging but should not be the primary surface.

**How Claude signals receipt and action**

In CrewAI's model, a worker signals that it has received a task by calling `task.ack` — a structured tool call rather than a natural-language statement. This keeps the audit trail machine-readable. For niwa's pull model, the sequence should be:

1. Claude calls the poll tool (e.g., `niwa_check_messages`)
2. Tool returns a formatted message summary (or "no new messages")
3. If a message requires a response, Claude calls a send tool (e.g., `niwa_send_message`) with the appropriate `reply_to`
4. The send tool returns a confirmation (message ID accepted by broker)

The send tool call is itself the receipt signal — the broker records that a reply was sent. There is no separate "ack" step for the receiver to perform. This keeps the interaction model to two tool calls (poll, then optionally send) rather than three.

**Latency vs. friction tradeoff**

The fundamental tension: lower polling interval means lower latency but more context-window noise ("no new messages" returned repeatedly). Higher interval means less noise but potentially minutes of delay on time-sensitive questions.

Community workarounds that use file polling report 15+ minute intervals as the practical minimum to avoid disrupting Claude's work stream. Gas Town addresses this with filesystem push notifications rather than polling — the agent is woken when a message arrives rather than checking on a schedule. Niwa's file-based transport can support this via `inotify` on Linux (already available, zero additional dependencies), which eliminates the polling interval tradeoff entirely: Claude checks when notified, not on a schedule.

For v1 pull model (no push notification), a count-based trigger of M=10 tool calls and an idle-state check is a reasonable default. The system prompt instructions should make M configurable by the user in `workspace-context.md` so they can tune it per workspace.

### Implications for Requirements

**Broker address and role delivery:**
- The system shall inject the broker socket path and the session's assigned role into `workspace-context.md` at `niwa create`/`niwa apply` time, using the existing `@import` mechanism so every session's CLAUDE.md automatically includes it.
- The system shall include a system prompt section in `workspace-context.md` that instructs Claude how to use the messaging tools, with the polling cadence expressed as a configurable default.

**Poll tool behavior:**
- The system shall provide an MCP tool named `niwa_check_messages` that returns pending messages for the calling session, scoped to the current workspace instance and the session's registered role.
- The tool shall return messages formatted as structured summaries (sender, type, key body fields, expiry if set), not raw JSON, as its primary output.
- The tool shall return a clearly distinguished "no new messages" response (not an empty array or null) so Claude can recognize the idle state without further parsing.
- The tool shall not require the session's PID as a parameter; it shall resolve the session's identity from the workspace instance and registered role alone.

**Send tool behavior:**
- The system shall provide an MCP tool named `niwa_send_message` that accepts at minimum: `to` (role string), `type` (dotted routing key), `body` (type-specific object), and optionally `reply_to` (message ID) and `expires_at` (ISO 8601 deadline).
- The send tool shall return a confirmation including the assigned message ID, so Claude can log it as evidence of dispatch.
- The send tool shall reject messages with unrecognized `type` values synchronously (not silently drop them) so Claude knows the send failed.

**Polling cadence:**
- The system prompt instructions injected into `workspace-context.md` shall direct Claude to check for messages at idle points (before asking the user a question) and as a backstop every M tool calls, where M defaults to 10 and is overridable in `workspace.toml`.
- The system shall document the tradeoff between polling frequency and context-window noise so users can make an informed choice when overriding the default.

**Message format:**
- The system shall format incoming messages as structured markdown summaries inside the MCP tool result, preserving `id`, `from.role`, `type`, `sent_at`, and any `expires_at` field, plus the human-relevant body fields.
- The system shall not surface raw JSON envelopes as the primary format in Claude's context.

**Receipt signaling:**
- The system shall treat a successful `niwa_send_message` call as the receipt signal for messages that require a response. No separate acknowledgment tool call is required.
- The broker shall record the sent reply's `reply_to` field and make this association queryable for observability purposes.

### Open Questions

1. **Push vs. pull for v1**: Should the file-based transport use `inotify` (Linux) to wake Claude on message arrival rather than requiring Claude to poll? `inotify` is zero-dependency and eliminates the polling interval problem entirely, but it requires MCP server-side wiring that may complicate the v1 scope. Is push notification a v1 requirement or a v2 enhancement?

2. **"Idle" definition**: When the system prompt instructs Claude to check for messages "at idle points," what counts as idle? The answer is session-type-specific (a session mid-implementation is rarely idle; a coordinator waiting for a worker is almost always idle). Should the system prompt instructions differentiate by role?

3. **Context-window budget for messages**: If a session receives a large batch of messages (e.g., 5 review comments with code snippets), how does the system prevent the poll tool result from consuming an unreasonable portion of the context window? Should there be a per-call limit on returned messages (e.g., oldest 3 unread), with pagination?

4. **System prompt instructions ownership**: Who writes and maintains the behavioral instructions injected into `workspace-context.md`? If niwa generates them, they must be versioned alongside the tool schema. If the user writes them (with niwa providing a template), they're more flexible but harder to keep consistent. Which model does the PRD require?

5. **Tool naming**: `niwa_check_messages` and `niwa_send_message` are descriptive but verbose. Should the MCP server namespace matter (e.g., just `check_messages` within the `niwa` MCP server)? This affects how the system prompt instructs Claude to call them.

---

## Lead 3: Role/Identity UX

### Findings

**Auto-derive from repo name: edge cases**

The obvious algorithm — use the last path segment of the repo directory as the role — breaks in several real cases:

1. **Monorepo with multiple components**: a repo named `tsuku` contains `cli/`, `recipes/`, `website/`. Three sessions all working in the same repo would get the role `tsuku`. The role is a collision waiting to happen.

2. **Repo names that are too long or contain hyphens at the wrong level**: `tsukumogami-workspace-manager-cli` is unwieldy as a role. Users won't reference it naturally. CrewAI's experience with long role strings shows that humans stop using them as addresses and fall back to saying "the first session" or "the one in the terminal on the left."

3. **Workspace root session**: the coordinator session has no repo — it runs from the instance root. An auto-derived role from the root directory would be the instance name (e.g., `tsukumogami-6`), which is long and unstable across workspaces. A hardcoded convention of `coordinator` for the root session is simpler and works for the primary use case, but it hardcodes an assumption about workspace topology.

4. **Repos with generic names**: repos named `backend`, `frontend`, `api`, `core` are common in multi-repo workspaces. These are short enough to be usable, but they're also so generic that two different workspaces on the same machine could have a session with role `backend`. The role must always be scoped to the workspace instance to avoid cross-workspace routing errors.

5. **Group-prefixed names**: niwa organizes repos into groups (`public/niwa`, `private/tsuku`). The role could be `niwa`, `public/niwa`, or just the last segment `niwa`. Consistency matters: if some roles are `public/niwa` and others are `tsuku`, the `to` address field becomes error-prone.

The practical recommendation from CrewAI's role-string experience is: short, unambiguous, lowercase, no slashes. The last repo directory segment is a good default. When it's ambiguous (monorepo, multiple sessions), the user must provide an explicit override.

**Role visibility format: what other sessions see**

When session A sends `to: { role: "coordinator" }`, the broker resolves this to a physical endpoint. The role is the public address — it must be usable as a first-class identifier in system prompts and tool call arguments.

Format options:
- **Short name only** (`niwa`, `coordinator`): easy to type, fits in tool arguments, but requires instance scoping at the broker level (the role `niwa` in workspace A must not collide with `niwa` in workspace B).
- **Full path** (`tsukumogami-6/instance-1/niwa`): globally unique, easy to route, but impossible to type from memory and ugly in system prompt instructions.
- **Both**: a display name (short) and a canonical ID (full). The tool argument accepts the short name; the broker resolves it within the current workspace context.

"Both" is the right answer. The `to` field in the tool argument should accept short role names. The broker resolves them within the workspace instance. The full canonical form is available in `niwa session list` output for debugging.

**How a user finds out what role a session has**

Without a discovery mechanism, users will forget what role each session was assigned, especially across multiple windows. Three natural surfaces:
1. The terminal prompt when the session starts (if niwa writes a startup notice into `workspace-context.md`)
2. `niwa session list` in a separate terminal
3. The session's own `workspace-context.md` (which it reads at startup)

All three should agree. The startup notice in `workspace-context.md` is the most reliable because it's visible to Claude (who can state the role if asked) and to the user (who sees it when the session loads). The notice should say: "This session's role in the workspace mesh is: [role]. Other sessions can send you messages using this address."

`niwa session list` should show: instance name, repo path, assigned role, registration timestamp, liveness status (alive / stale / unknown). This mirrors the pattern of `niwa status` showing workspace shape.

**Duplicate role handling**

If two sessions both register with role `coordinator`, the broker has three options:

1. **Silent last-wins**: the second registration overwrites the first. Messages sent to `coordinator` go to the second session. The first session's messages are silently dropped. This is the worst outcome — the first session has no indication it was displaced.

2. **Error on duplicate**: the second `niwa session register` call fails. The user sees an error and must choose a different role. Clean but potentially frustrating if the first session crashed without deregistering.

3. **Suffix disambiguation**: the broker assigns `coordinator` to the first session and `coordinator-2` to the second. Both sessions are reachable; the second session is told its effective role is `coordinator-2`. The user must know to update the `to` addresses in any routing rules that expected a single coordinator.

Option 2 (error on duplicate) is the right default. The error message should include the existing session's registration timestamp and whether it appears live (PID check), with a hint: "The existing coordinator session (PID 14100, registered 3 minutes ago) is still active. If it crashed, run `niwa session unregister coordinator` to clear it." This gives the user a recovery path without silent data loss.

**Whether roles change during a session's lifetime**

Roles should be set once at session start and not change. The reasons:
1. Other sessions cache the `to` address in their instructions. A mid-session role change invalidates those cached addresses without any notification mechanism.
2. The broker must update its routing table on a role change. If the session crashes immediately after a role change, the registry may hold the new role with a stale socket path.
3. Clarity: the user expects a session's identity to be stable. A session that changes its role is indistinguishable from two different sessions from the user's perspective.

The one exception: a session should be able to re-register with the same role after a restart (crash recovery). Re-registration with the same identity should succeed silently, replacing the stale socket path in the registry.

**NIWA_SESSION_ROLE as the override mechanism**

`NIWA_SESSION_ROLE` is the right override mechanism. It follows the existing pattern (niwa uses env vars for runtime overrides, `workspace.toml` for workspace-level defaults). The env var should be checked first; auto-derivation from repo name is the fallback.

The workspace.toml `[channels]` config could also define a default role per repo:
```toml
[channels.mesh.roles]
"public/niwa" = "niwa-worker"
"public/koto" = "koto-worker"
```

This workspace-level assignment is deterministic and doesn't require the user to remember to set an env var each time they open a session. It also enables niwa to inject the correct role into `workspace-context.md` at `niwa apply` time, before any session starts.

**The workspace root session's role**

The workspace root session has no repo. Auto-derivation would yield the instance name, which is poor. The right approach: reserve `coordinator` as the conventional role for the root session, make it the default, and document it. Users who want a different name can override with `NIWA_SESSION_ROLE` or a `[channels.mesh]` config entry.

### Implications for Requirements

**Auto-derivation:**
- The system shall auto-derive a session's role from the last path segment of the repo directory it is running in (e.g., `public/niwa` → role `niwa`).
- The system shall assign the role `coordinator` by default to sessions running from the workspace instance root (no repo directory).
- The system shall document both defaults in `workspace-context.md` so the user can read them from within the session.

**Override precedence:**
- The system shall respect the following role resolution order, highest to lowest: `NIWA_SESSION_ROLE` environment variable, `[channels.mesh.roles]` entry in `workspace.toml` for the session's repo path, auto-derived from repo name, built-in default (`coordinator` for root).
- The system shall make the resolved role visible to the session via `workspace-context.md` before the session's first tool call.

**Role format:**
- The system shall use short role names (last path segment or user-provided string) as the address format in MCP tool arguments and system prompt instructions.
- Role names shall be restricted to lowercase alphanumeric characters and hyphens, with a maximum length of 32 characters, enforced at registration time.
- The system shall scope role uniqueness to the workspace instance: the same role name may exist in different workspace instances without conflict.

**Duplicate role handling:**
- The system shall reject a `niwa session register` call with a role that is already registered to a live session (PID still exists) in the same workspace instance, returning an error that includes the existing session's registration timestamp and PID.
- The system shall accept re-registration with the same role if the existing entry's PID is no longer alive (stale entry cleanup).
- The error message for duplicate registration shall include a recovery command (`niwa session unregister <role>`) to clear stale entries manually.

**Role immutability:**
- The system shall not allow a session to change its role after initial registration without first unregistering.
- The system shall allow re-registration with the same role after a session restart, treating it as crash recovery rather than a duplicate.

**Session discovery:**
- `niwa session list` shall display: workspace instance name, repo path, assigned role, registration timestamp, and liveness status (alive / stale / unknown) for each registered session.
- Liveness shall be determined by PID existence check (`kill -0 <pid>`), not by heartbeat, for v1.
- The session's own role shall appear in `workspace-context.md` as a human-readable notice, e.g.: "Your role in this workspace mesh is: niwa-worker. Other sessions address you as 'niwa-worker'."

**Monorepo handling:**
- When a session's working directory is inside a monorepo (a repo that contains multiple component subdirectories), the system shall not auto-derive distinct roles for each component. The user must override via `NIWA_SESSION_ROLE` or `[channels.mesh.roles]` to create component-level roles.
- The system shall not attempt to infer component boundaries from directory structure.

**workspace.toml configuration:**
- The system shall support a `[channels.mesh.roles]` table in `workspace.toml` mapping repo paths to role strings, evaluated at `niwa apply` time and injected into each repo's `workspace-context.md`.

### Open Questions

1. **Monorepo strategy**: For a monorepo where the user genuinely wants multiple Claude sessions (one per component), the auto-derive approach produces a collision. Should the PRD require a documented pattern for this case (e.g., "set `NIWA_SESSION_ROLE=cli-worker` in one terminal, `NIWA_SESSION_ROLE=recipes-worker` in another")? Or is the workspace.toml `[channels.mesh.roles]` table sufficient, given that a monorepo typically has a fixed set of components?

2. **Role validation timing**: Should role name format validation (lowercase, alphanumeric, hyphens, max 32 chars) happen at `niwa apply` time (when `workspace.toml` is parsed) or at `niwa session register` time (when the session starts)? Early validation catches config errors before any session opens; late validation catches env var overrides. Both may be needed.

3. **`coordinator` as a reserved word**: If `coordinator` is the built-in default for the root session, should it be reserved (no other session can use it without explicit intent) or just conventional? A user who deliberately names a repo `coordinator` would get a confusing collision.

4. **Cross-instance role uniqueness**: The PRD currently scopes roles to a workspace instance. But `niwa session list` without an explicit instance flag should probably show sessions across all instances. Does the display need to qualify roles by instance to avoid visual ambiguity (e.g., `tsukumogami-6::coordinator` vs. `tsukumogami-7::coordinator`)?

5. **Role in system prompt vs. workspace-context.md**: The role should appear in `workspace-context.md` so Claude knows its own address. But should it also be in the system prompt (a separate injection)? Putting it in `workspace-context.md` means Claude reads it as context, which it may or may not retain across long conversations. A dedicated system prompt line is more durable but requires a different injection mechanism.

6. **Stale entry TTL**: The duplicate-registration error says "if the PID is no longer alive, accept re-registration." But PID checks can be wrong on some systems (PID reuse, container boundaries). Should stale entries also have a time-based fallback: if the entry is older than T minutes and the PID is not verifiably alive, treat it as stale regardless?

---

## Summary

Pull-based message delivery requires two things the current niwa architecture does not yet provide: behavioral instructions embedded in `workspace-context.md` that tell Claude when and how to poll (not just the broker address), and a formatted message surface inside MCP tool results that preserves the correlation IDs Claude needs to send structured replies without exposing raw JSON. Role/identity UX should default to short last-segment names from the repo path with `coordinator` reserved for the root session, enforce uniqueness with an error-on-duplicate policy (not silent last-wins), and expose the resolved role to the session via `workspace-context.md` before the first tool call — these three decisions together make the role system safe enough for v1 without requiring a configuration step that would create friction for new users.
