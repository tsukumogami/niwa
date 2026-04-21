# Cross-Session Communication

niwa workspaces often run multiple Claude sessions at once — one per repo, one at
the workspace root. By default those sessions have no way to exchange messages without
the user acting as a relay. The session mesh removes that constraint: sessions can send
typed messages to each other, ask questions and block for answers, and coordinate tasks
without any user involvement.

## When to use it

The session mesh is useful when:

- A coordinator session needs to delegate work to per-repo workers and collect results
- A worker needs to ask the coordinator a clarifying question before continuing
- Two sessions are working on related changes and need to hand off information

For simple one-off workflows where the user is present and can copy-paste, the session
mesh adds more complexity than it's worth. It shines in long-running automated workflows
where sessions run in parallel and need to synchronize.

## Enabling the session mesh

There are three ways to enable the mesh, ordered by commitment level:

**Flag** — one-off, no config changes needed:

```bash
niwa create my-instance --channels
niwa apply --channels
```

Use `--no-channels` to skip mesh provisioning even if the env var or config says
otherwise.

**Env var** — persist the preference for yourself without touching shared config:

```bash
export NIWA_CHANNELS=1
```

Any subsequent `niwa create` or `niwa apply` will provision the mesh. Set this in your
shell profile to make it permanent.

**workspace.toml** — persist the preference for the whole team:

```toml
[channels.mesh]
```

A bare `[channels.mesh]` section with no sub-keys is valid and provisions the full mesh.
Roles are auto-derived from the workspace topology (see [Roles](#roles) below).

The flag overrides the env var, which overrides the config. `--no-channels` suppresses
provisioning regardless of env var or config.

### Roles

Roles are the addresses sessions use to reach each other — `niwa_send_message(to="coordinator", ...)`.

By default, roles are derived from the workspace topology:

- A session running at the instance root gets the role `coordinator`
- A session running in `<instance-root>/myrepo/` gets the role `myrepo`

This works without any configuration for typical layouts. If a repo's directory name
doesn't make a useful role name, you can override it two ways:

- At session start: pass `--role <name>` to the relevant command, or set
  `NIWA_SESSION_ROLE=<name>` in the environment
- In workspace.toml: add an explicit `[channels.mesh.roles]` map

The explicit map takes precedence over auto-derivation:

```toml
[channels.mesh]

[channels.mesh.roles]
coordinator = ""          # "" means the instance root
worker = "tools/worker"   # relative path from the instance root
```

Role names become the addresses used in MCP tool calls. They must match
`^[a-zA-Z0-9._-]{1,64}$`.

### Config reference

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `[channels.mesh]` | section | — | Presence enables the mesh. No sub-keys required; roles are auto-derived from directory names if `[channels.mesh.roles]` is omitted. |
| `[channels.mesh.roles]` | map[string]string | — | Optional. Role name → repo path (relative to instance root; `""` for root). Overrides auto-derivation for any listed role. |
| `message_ttl` | string | `"24h"` | How long consumed messages are kept in `inbox/read/` before the TTL sweep deletes them. Standard Go duration syntax. |

Example with a custom TTL and explicit roles:

```toml
[channels.mesh]
message_ttl = "48h"

[channels.mesh.roles]
coordinator = ""
koto = "tools/koto"
shirabe = "tools/shirabe"
```

## What `niwa apply` provisions

When `[channels.mesh]` is present, `niwa apply` (and `niwa create`) provisions the
following at step 4.75 of the pipeline, before any per-repo materializers run:

**Filesystem layout:**

```
<instance-root>/
├── .niwa/
│   ├── sessions/
│   │   └── sessions.json          # session registry (preserved on re-apply)
│   ├── hooks/
│   │   ├── mesh-session-start.sh  # runs niwa session register
│   │   └── mesh-user-prompt-submit.sh  # runs niwa session register --check-only
│   ├── daemon.pid                 # written by the daemon after it starts
│   └── daemon.log                 # daemon stdout/stderr
└── .claude/
    └── .mcp.json                  # registers niwa mcp-serve with NIWA_INSTANCE_ROOT
```

**workspace-context.md additions:**

A `## Channels` section is appended that explains how roles are assigned (auto-derivation
rules and any explicit overrides), lists the four MCP tool names, and gives behavioral
instructions. This section is idempotent — re-applying won't duplicate it.

**Hook injection:**

Two hooks are injected into every repo's Claude Code hooks via `HooksMaterializer`:

- `SessionStart` → calls `niwa session register` (records PID and Claude session ID)
- `UserPromptSubmit` → calls `niwa session register --check-only` (keeps the registry
  up to date while the session is active)

These hooks run before any user-defined hooks for the same events.

**Daemon:**

After writing all files, `niwa apply` checks whether a live daemon is already running
(by reading `.niwa/daemon.pid` and verifying the PID). If not, it spawns `niwa mesh
watch --instance-root=<path>` as a detached background process (`Setsid: true`). The
daemon writes its PID file atomically — only after its fsnotify watch loop is
established — so `niwa apply` never sees a half-started daemon as live.

Running `niwa apply` multiple times is safe. If the daemon is already alive, nothing is
spawned. If `sessions.json` already exists, it's left untouched.

## The four MCP tools

Sessions interact with the mesh via four tools exposed by `niwa mcp-serve`:

### niwa_check_messages

Reads all message files from this session's inbox and returns them formatted as
markdown. Call this at idle points or every ~10 tool calls while working.

```
niwa_check_messages()
```

Returns: formatted list of messages, or "No new messages."

### niwa_send_message

Sends a typed message to another session by role. The message is written to the
recipient's inbox as a JSON file via atomic rename, so it survives crashes.

```
niwa_send_message(
  to="coordinator",
  type="task.result",
  body={"summary": "done", "files_changed": 3},
  reply_to="<msg-id>",    # optional: ID of message being answered
  task_id="<uuid>",       # optional: stable task identifier for correlation
  expires_at="2026-04-21T18:00:00Z"  # optional: ISO 8601 expiry
)
```

`type` and `to` must match `^[a-zA-Z0-9._-]{1,64}$`. Common types: `question.ask`,
`question.answer`, `task.delegate`, `task.result`.

### niwa_ask

Sends a question to another session and blocks until a reply arrives — or until the
timeout expires. The calling session is blocked (the tool call stays open) for the
duration, so you don't need polling loops.

```
niwa_ask(
  to="coordinator",
  body={"question": "Which approach should I use for the auth refactor?"},
  timeout=300  # seconds; default 600
)
```

Returns: the body of the reply message, or an error with code `ASK_TIMEOUT` if no
reply arrives within the timeout.

**How it works:** `niwa_ask` writes a `question.ask` message to the recipient's inbox,
registers an internal reply channel keyed by the sent message ID, then blocks on that
channel. When the recipient calls `niwa_send_message` with `reply_to=<msg-id>`, the
server detects the matching reply, moves the file to `inbox/read/`, and unblocks the
waiting call.

If the recipient session's process is dead when the message arrives, the daemon resumes
it via `claude --resume <session-id>`. If the session is alive but busy, messages queue
in the inbox; `niwa_ask` will time out if the busy session doesn't respond within the
timeout window. This is the accepted tradeoff — see [Busy session behavior](#busy-session-behavior).

### niwa_wait

Blocks until a threshold number of messages matching optional type and/or sender filters
have arrived in the inbox. Useful for a coordinator collecting results from multiple
workers.

```
niwa_wait(
  types=["task.result"],    # empty = accept any type
  from=["worker-a", "worker-b"],  # empty = accept from anyone
  count=2,                  # wait for 2 matching messages; default 1
  timeout=600               # seconds; default 600
)
```

Returns: the accumulated messages once the count threshold is reached, or an error with
code `WAIT_TIMEOUT`.

## Daemon lifecycle

The mesh watch daemon (`niwa mesh watch`) is a long-running background process. You
don't normally interact with it directly.

| Event | What happens |
|-------|-------------|
| `niwa apply` | Starts the daemon if not already alive; idempotent |
| `niwa create` | Same as apply — starts the daemon after provisioning |
| `niwa destroy` | Sends SIGTERM, waits up to 5 seconds, sends SIGKILL if needed, then removes the instance directory |
| Machine restart | Daemon is gone; run `niwa apply` to restart it |
| Daemon crash | Run `niwa apply` to restart; messages queued in inboxes are preserved |

**After a reboot:** `niwa apply` is the recovery path. Run it from the instance
directory to restart that instance's daemon, or from the workspace root to restart
daemons for all instances at once.

**If the daemon isn't running:** Sessions can still exchange messages. `niwa_ask` and
`niwa_send_message` write to the filesystem regardless of daemon state. The only thing
the daemon provides is autonomous wakeup of idle sessions. If a session is open, it
will receive messages via the `UserPromptSubmit` hook; if it's closed, it won't be
resumed until the daemon is back or the user opens it manually.

## Session registration

When a Claude session opens, the `SessionStart` hook runs `niwa session register`.
This command:

1. Creates a session UUID and inbox directory at `.niwa/sessions/<uuid>/inbox/`
2. Discovers the Claude session ID (used by the daemon to resume idle sessions) by
   checking `CLAUDE_SESSION_ID` env var, then reading `~/.claude/sessions/<ppid>.json`,
   then scanning `~/.claude/projects/<base64url-cwd>/`
3. Writes a `SessionEntry` to `sessions.json` with role, PID, start time, inbox path,
   and Claude session ID

### Graceful degradation when session ID discovery fails

If none of the three discovery methods finds a Claude session ID, the field is left
empty and a warning is logged. The session can still receive messages — other sessions
can send to it and it will read them the next time it calls `niwa_check_messages` or
when it opens manually. The only capability that's lost is the daemon's ability to
resume it autonomously.

## Busy session behavior

If a target session's process is alive when a message arrives, the daemon takes no
action — it only resumes dead sessions. Messages queue in the inbox. `niwa_ask` will
time out if the busy session doesn't complete its current task and reply within the
timeout window.

For coordinator sessions that are actively in use (PID alive, user at the terminal),
the `UserPromptSubmit` hook fires on each user message and keeps the session's
registration current. The `## Channels` section in `workspace-context.md` instructs
coordinators to call `niwa_wait` periodically to check for incoming messages.

The `timeout` parameter on `niwa_ask` is configurable per call. For time-sensitive
exchanges, use a shorter timeout and retry.

## Removing an instance with mesh configured

Use `niwa destroy` rather than `rm -rf` for instances with `[channels.mesh]` configured.
`niwa destroy` stops the daemon cleanly before removing the directory. If you use
`rm -rf`, the daemon detects the missing sessions directory via fsnotify and exits on its
own, but this is an unclean termination.

```bash
# Correct
niwa destroy my-instance

# Incorrect for mesh instances — daemon will self-exit, but uncleanly
rm -rf my-instance
```

## Security

- All files under `.niwa/sessions/` are created with mode `0600` (files) and `0700`
  (directories), independent of umask.
- `ClaudeSessionID` values are validated against `^[a-zA-Z0-9_-]{8,128}$` before
  being written to `sessions.json` or passed to `exec.Command`.
- `type` and `to` fields in messages are validated against `^[a-zA-Z0-9._-]{1,64}$`.
- `sessions.json` reads and writes are protected by an advisory lock at
  `.niwa/.sessions.lock`.
- Message bodies are never written to log output; only message IDs and types appear in
  `daemon.log`.

Message signing (HMAC-SHA256 with a per-instance shared key) is planned as a follow-on.
