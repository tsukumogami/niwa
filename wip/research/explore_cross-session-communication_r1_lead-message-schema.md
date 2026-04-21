# Lead: Message schema for inter-session communication

## Findings

### Reference protocols reviewed

Three existing protocols provide useful shape for a minimal message envelope:

**JSON-RPC 2.0** uses a flat envelope with `jsonrpc`, `method`, `id`, `params`, and `result`/`error`. The `id` field drives request/reply correlation: a request carries a client-chosen id, and the response echoes it. Notifications omit `id` entirely. This is the tightest possible envelope — there is no sender identity, no timestamp, and no routing key beyond `method`.

**MCP (Model Context Protocol)** is built on JSON-RPC 2.0 and adds a capability negotiation phase at connection startup. Each side declares what it can do; the protocol then carries tool calls, resource reads, and prompt completions as typed messages. The key addition over bare JSON-RPC is the capability envelope, which lets the receiver know what message types to expect before the first substantive message arrives.

**LSP (Language Server Protocol)** also extends JSON-RPC 2.0 and is notable for its distinction between requests (expect a response), notifications (fire-and-forget), and progress messages (streaming updates for long-running operations). The `$/progress` notification pattern — where the requester sends a `workDoneToken` and the server sends back `$/progress` messages using that token — is directly relevant to status updates and delegation acknowledgments.

### Session identity

The first design question is how a session identifies itself. Three candidates:

1. **PID**: Available immediately, requires no provisioning. Problem: PIDs are recycled. A message sent to PID 14322 that arrives after the session restarts will be delivered to a different process. PIDs also say nothing about which workspace or repo the session is working in.

2. **Workspace instance name + repo path** (e.g., `tsukumogami-6/instance-1/public/niwa`): Stable across restarts, human-readable, and directly maps to niwa's existing identity model (`InstanceState.InstanceName` + repo directory). The downside is that two Claude sessions could be open against the same repo at the same time (edge case, but real).

3. **User-assigned name** (e.g., `coordinator`, `niwa-worker`, `tsuku-worker`): The most ergonomic for routing rules. Requires either the user to assign names at session start or niwa to assign them from workspace config.

The recommendation is a **composite**: workspace instance name + user-assigned role, with PID as a tiebreaker for same-instance disambiguation. This maps onto how `InstanceState` already works: niwa knows the instance name, and a per-session role can be injected via the same `workspace-context.md` mechanism that already communicates workspace shape to sessions.

### Message envelope fields

**Always required:**
- `v` — schema version (integer). Must be first to allow future breaking changes without ambiguity.
- `id` — client-chosen string or null. Present on requests and responses; absent (or null) on fire-and-forget notifications. Used for request/reply correlation.
- `type` — routing key. A dotted string that identifies message category and intent (e.g., `question.ask`, `question.answer`, `task.delegate`, `task.ack`, `review.feedback`, `status.update`). The receiver dispatches on this field first.
- `from` — sender identity. A structured object (see below).
- `sent_at` — ISO 8601 timestamp. Required for ordering and for receivers that wake from sleep and need to detect stale messages.
- `body` — message payload. A type-specific object. Schema is governed by `type`.

**Context-specific:**
- `to` — intended recipient identity. Optional for broadcast; required for directed messages. Same structure as `from`.
- `reply_to` — echoes the `id` of the originating request. Set on response messages; absent on initiating requests.
- `workspace_id` — the niwa workspace instance name (e.g., `tsukumogami-6/instance-1`). Needed when the transport is shared across multiple workspace instances (e.g., a machine-wide broker). Omit when the transport is already scoped per-instance.
- `task_id` — stable identifier for a delegated unit of work. Survives session restarts and can span multiple messages (delegate → ack → progress → result).
- `expires_at` — ISO 8601 deadline. Set by the sender to signal when a pending question no longer needs an answer.

**Session identity object:**
```json
{
  "instance": "tsukumogami-6/instance-1",
  "role": "coordinator",
  "repo": "public/niwa",
  "pid": 14322
}
```

`role` and `repo` are optional; `instance` is required. `pid` is a tiebreaker only — routing logic should not depend on it.

### Proposed schema (JSON)

```json
{
  "v": 1,
  "id": "<uuid-or-null>",
  "type": "<dotted-string>",
  "from": {
    "instance": "<workspace-instance-name>",
    "role": "<optional-role>",
    "repo": "<optional-repo-path>",
    "pid": 0
  },
  "to": {
    "instance": "<workspace-instance-name>",
    "role": "<optional-role>"
  },
  "reply_to": "<id-of-original-request-or-null>",
  "workspace_id": "<workspace-instance-name>",
  "task_id": "<optional-stable-task-uuid>",
  "sent_at": "2026-04-20T12:00:00Z",
  "expires_at": "<iso8601-or-null>",
  "body": {}
}
```

### Example messages for use cases

**Use case 1: Claude B asks a clarifying question mid-implementation**

Request (sent by implementer to coordinator):
```json
{
  "v": 1,
  "id": "q-7f3a2b1c",
  "type": "question.ask",
  "from": {
    "instance": "tsukumogami-6/instance-1",
    "role": "niwa-worker",
    "repo": "public/niwa",
    "pid": 14322
  },
  "to": {
    "instance": "tsukumogami-6/instance-1",
    "role": "coordinator"
  },
  "task_id": "task-issue-69",
  "sent_at": "2026-04-20T12:01:00Z",
  "expires_at": "2026-04-20T12:31:00Z",
  "body": {
    "question": "Should the channel broker persist messages to disk for durability, or is in-memory delivery acceptable for v1?",
    "context": "Implementing the unix socket broker in internal/channel/. The PRD does not specify durability requirements."
  }
}
```

Response (sent by coordinator back to implementer):
```json
{
  "v": 1,
  "id": "a-9b2c4d5e",
  "type": "question.answer",
  "from": {
    "instance": "tsukumogami-6/instance-1",
    "role": "coordinator",
    "pid": 14100
  },
  "to": {
    "instance": "tsukumogami-6/instance-1",
    "role": "niwa-worker",
    "repo": "public/niwa"
  },
  "reply_to": "q-7f3a2b1c",
  "task_id": "task-issue-69",
  "sent_at": "2026-04-20T12:03:30Z",
  "body": {
    "answer": "In-memory is fine for v1. Durability is explicitly out of scope until cross-machine transport.",
    "blocking": false
  }
}
```

**Use case 2: Claude A sends code review feedback after reviewing a PR**

```json
{
  "v": 1,
  "id": "rev-c3f1a8b2",
  "type": "review.feedback",
  "from": {
    "instance": "tsukumogami-6/instance-1",
    "role": "coordinator",
    "pid": 14100
  },
  "to": {
    "instance": "tsukumogami-6/instance-1",
    "role": "niwa-worker",
    "repo": "public/niwa"
  },
  "task_id": "task-issue-69",
  "sent_at": "2026-04-20T14:22:00Z",
  "body": {
    "pr": "https://github.com/tsukumogami/niwa/pull/71",
    "verdict": "changes_requested",
    "comments": [
      {
        "file": "internal/channel/broker.go",
        "line": 42,
        "severity": "required",
        "text": "The socket path must be scoped under the instance's .niwa/ directory, not /tmp. Use InstanceState.Root to construct it."
      },
      {
        "file": "internal/channel/broker.go",
        "line": 87,
        "severity": "suggestion",
        "text": "Consider wrapping the listener accept loop with a context for clean shutdown."
      }
    ]
  }
}
```

**Use case 3: Claude A (coordinator) delegates a task to Claude B (worker)**

```json
{
  "v": 1,
  "id": "del-4e9d2f7a",
  "type": "task.delegate",
  "from": {
    "instance": "tsukumogami-6/instance-1",
    "role": "coordinator",
    "pid": 14100
  },
  "to": {
    "instance": "tsukumogami-6/instance-1",
    "role": "niwa-worker",
    "repo": "public/niwa"
  },
  "task_id": "task-issue-71",
  "sent_at": "2026-04-20T09:00:00Z",
  "body": {
    "issue": "https://github.com/tsukumogami/niwa/issues/71",
    "title": "Implement unix socket broker for inter-session messaging",
    "description": "Implement internal/channel/broker.go per the design in docs/designs/. The broker must listen on .niwa/channel.sock and route messages by recipient role.",
    "acceptance": [
      "broker starts and stops cleanly",
      "messages are delivered to registered recipients",
      "undeliverable messages are queued for up to 60 seconds then dropped"
    ],
    "priority": "high"
  }
}
```

**Use case 4: Status update (fire-and-forget notification)**

```json
{
  "v": 1,
  "id": null,
  "type": "status.update",
  "from": {
    "instance": "tsukumogami-6/instance-1",
    "role": "niwa-worker",
    "repo": "public/niwa",
    "pid": 14322
  },
  "sent_at": "2026-04-20T11:45:00Z",
  "task_id": "task-issue-71",
  "body": {
    "summary": "Finished implementing broker.go. Opening PR now.",
    "state": "in_progress",
    "next": "Create PR and request review"
  }
}
```

### Type vocabulary

A flat namespace of dotted strings is enough for v1:

| `type` | Direction | Reply expected |
|---|---|---|
| `question.ask` | any → any | yes (`question.answer`) |
| `question.answer` | any → any | no |
| `task.delegate` | coordinator → worker | yes (`task.ack`) |
| `task.ack` | worker → coordinator | no |
| `task.result` | worker → coordinator | no |
| `review.feedback` | coordinator → worker | no |
| `status.update` | any → coordinator | no |
| `session.hello` | any → broker | no (registration) |
| `session.bye` | any → broker | no (deregistration) |

`session.hello` and `session.bye` are needed for the discovery mechanism: when a session starts, it sends `hello` to register; when it stops (or the process exits), it sends `bye` or the broker detects the closed connection.

---

## Implications

**Session identity encoding has downstream consequences for session discovery.** If `from.instance + from.role` is the primary address, then two sessions with the same role in the same instance create an ambiguity that the schema cannot resolve. The schema includes `pid` as a tiebreaker, but the discovery design must decide whether duplicate roles are an error, allowed with round-robin delivery, or prevented by convention. This decision cannot be deferred — the schema encodes it implicitly.

**The `type` field is the primary routing key.** It must be validated by the broker before delivery. An unknown type should be rejected with an error response (if `id` is present) or dropped (if `id` is null). This means the broker needs a type registry, which in turn means adding new message types requires updating the broker. Whether the type registry is hardcoded or extensible is a design choice with real maintenance consequences.

**`task_id` enables multi-message task tracking without a database.** A coordinator delegates with `task_id = task-issue-71`, the worker acks with the same ID, sends progress updates with it, and delivers the final result with it. The coordinator can correlate all of these without any external state store. This is the key insight from LSP's `workDoneToken` pattern.

**`expires_at` on questions prevents coordinator stalls.** Without it, a coordinator waiting for an answer to a blocking question has no way to time out gracefully. With it, the broker can discard the message when the deadline passes and the coordinator can proceed on a timeout path.

**The schema is transport-agnostic by design.** Nothing in the envelope assumes unix sockets, named pipes, or any other IPC mechanism. The `from`/`to` fields are logical addresses; the transport resolves them to physical endpoints. This preserves the design path to network transport.

**Niwa's existing `[channels]` placeholder in `WorkspaceConfig` is the right hook.** The config already accepts `map[string]any` under `[channels]`, and the scaffold template includes a commented-out `[channels]` section. The channel provisioning config can live there without touching the rest of the schema. The broker's socket path belongs under `{instance_root}/.niwa/channel.sock`, consistent with how niwa scopes all instance state under `.niwa/`.

---

## Surprises

**The `[channels]` placeholder already exists in the config schema.** `WorkspaceConfig.Channels` is typed as `map[string]any` with the comment "placeholder" (config.go line 105). The test fixture at config_test.go line 88 shows `[channels.telegram]` with a `plugin` key. This signals that channels were anticipated as a plugin-driven feature — the schema was reserved before the design was done. A message broker is a different kind of channel than a Telegram integration, so the `[channels]` key may need to support both plugin-backed channels and the built-in broker. This is a naming collision risk.

**The workspace context file (`workspace-context.md`) is already the mechanism for injecting structured information into sessions.** The `generateWorkspaceContext` function in `workspace_context.go` produces a markdown file listing all repos and groups, installed at the instance root as `workspace-context.md` and imported into CLAUDE.md. This same mechanism could inject the channel endpoint (socket path, broker address) into every session without requiring any new Claude Code primitives. Sessions learn about the channel from their CLAUDE.md context, not from a separate discovery step.

**PID-based identity is fragile in a restart scenario, but the existing state model suggests a better approach.** `InstanceState` already tracks `InstanceName` and `InstanceNumber`, which are stable across restarts. A session that restarts against the same instance can recover its identity from the instance state file, reconnect to the broker, and re-register with the same logical address. This makes restart handling tractable without any new primitives.

---

## Open Questions

1. **How does the broker handle a directed message when the recipient is not currently registered?** Options: queue with TTL, drop immediately, or return an error. This affects the `expires_at` semantics and the coordinator's retry logic.

2. **Should `role` be assigned by the user at session start, by niwa at provisioning time via workspace config, or derived from the repo the session is working in?** User-assigned is most flexible but adds friction. Niwa-assigned via `[channels]` config is consistent with how niwa handles everything else.

3. **Does the broker need to be a separate process, or can it be a lightweight daemon started by `niwa apply`?** Starting the broker at apply time and storing the socket path in instance state is the simplest option, but it means the broker lifetime is tied to when `niwa apply` was last run, not to when sessions are active.

4. **How do sessions register with the broker when they start?** The `session.hello` message type covers the protocol, but the mechanism to find the broker's address needs to be specified. The socket path in `workspace-context.md` is one approach; an environment variable set by niwa is another.

5. **Is the `[channels]` placeholder in workspace config meant for this broker, or only for external plugin-backed channels like Telegram?** If it's for both, the config schema needs to distinguish between `[channels.broker]` (built-in) and `[channels.telegram]` (plugin). If the built-in broker is always-on, it shouldn't live in `[channels]` at all.

6. **What is the delivery guarantee?** At-most-once (send and forget) is simplest; at-least-once requires the sender to retry until acked; exactly-once is out of scope for v1. The schema supports at-least-once via `id`-based deduplication (receiver tracks seen IDs), but the broker design must specify which guarantee it provides.

7. **Should `body` be a typed discriminated union or a free-form object?** A typed union (with the type discriminant in `type`) is more rigorous but requires the schema to enumerate all body shapes. A free-form object is easier to extend but gives the receiver less to validate. Given the small number of message types in v1, a typed union with documented body schemas per type is feasible.

---

## Summary

A minimal message envelope with eight fields — `v`, `id`, `type`, `from`, `to`, `reply_to`, `task_id`, `sent_at` — covers all five use cases, drawing on the JSON-RPC 2.0 request/reply correlation pattern and LSP's progress-token model for multi-message task tracking. The most consequential design decision is how session identity is encoded: a composite of workspace instance name plus user-assigned role is the right anchor, but the system must enforce or tolerate role uniqueness within an instance, and that policy choice ripples into the broker routing design and the discovery mechanism. The biggest open question is whether the built-in broker belongs inside the existing `[channels]` config namespace or outside it, since that namespace appears to be reserved for plugin-backed integrations rather than niwa's own infrastructure.
