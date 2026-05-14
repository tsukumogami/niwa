# `niwa surface serve` — the machine-level change-review HTTP listener

`niwa surface serve` aggregates every change produced by every niwa
instance you've registered in the user-level config and exposes them
through a single browser-readable HTTP endpoint on 127.0.0.1. One
process per machine, federated across every workspace.

## Process topology

niwa runs two long-lived processes per machine for the collab
surface:

| Process | Scope | What it does |
|---------|-------|--------------|
| `niwa mesh watch` (one per niwa instance) | Per-instance daemon | Claims queued task envelopes from the per-role inboxes and spawns worker processes per task. Pre-existing infrastructure. |
| `niwa mcp-serve` (per session) | Per-session | Hosts the MCP server inside a session worktree. Writes `.niwa/changes/<id>/` directly when agents call `niwa_create_change`. |
| `niwa surface serve` (one per user) | **Machine-level** — single process across the user's whole niwa fleet | Reads `~/.config/niwa/config.toml`, enumerates every registered workspace and every niwa instance under it, and serves a federated HTTP index at 127.0.0.1:&lt;port&gt;. |

There is **no** Telegram bridge in this feature. The `change_ready` event lands
in each instance's `mcp-audit.log`; a future bridge spec will decide
how that reaches a notification channel.

## Quick start

```sh
# In any terminal, on your machine
niwa surface serve
```

The command boots with no arguments. It reads the registry,
discovers every instance, and prints the URL:

```
niwa surface listening on http://127.0.0.1:48329
token stored at /home/you/.config/niwa/surface.token (read-only, mode 0600)
serving 7 instance(s) across 2 workspace(s)
```

Open the URL in your browser. The index lists every registered
workspace; drilling in shows the instances under that workspace, and
each instance shows its current set of pending and in-review changes.

## URL structure

```
http://127.0.0.1:<port>/                                            → 302 /workspaces/
http://127.0.0.1:<port>/workspaces/                                 → top-level workspace index
http://127.0.0.1:<port>/workspaces/<workspace>/                     → instances within the workspace
http://127.0.0.1:<port>/workspaces/<workspace>/<instance>/changes/  → changes within the instance
http://127.0.0.1:<port>/workspaces/<workspace>/<instance>/changes/<change-id>   → per-change page
```

The `#comment-<id>` URL fragment is reserved for future line-anchored
comments — this feature leaves it inert but locks the shape so future
deep-links stay stable.

Workspace identifiers come from the registry key
(e.g. `tsuku`, `cs`). Instance identifiers are the directory name
under the workspace root (e.g. `tsuku-4`); the workspace root itself
appears as the `_root` instance.

## Authentication

A `surface.token` is generated on first boot at
`~/.config/niwa/surface.token` (mode `0o600`, UUIDv4 from
`crypto/rand`). The token gates **mutation endpoints** — this feature
ships none yet, but a future verdict-cast endpoint will require it. Read-only
endpoints (the workspace, instance, and change views) require no
authentication because anything on the same machine that can
`curl 127.0.0.1` can already read `.niwa/changes/<id>/` directly.

The token **contents** never appear in any log or on stderr. Only
the file path is printed at boot.

To rotate the token (e.g. after suspected compromise):

```sh
# Stop the running surface (Ctrl-C)
niwa surface serve --rotate-token
```

The new token is generated and written; any client holding the old
token receives 401 on its next mutation request and must refresh
from `~/.config/niwa/surface.token`.

CORS: cross-origin requests are rejected before any handler runs.

## Configuration

The surface reads no per-workspace configuration today — the GC
defaults are compiled into the binary:

| Setting | Default | Range | Source |
|---------|---------|-------|--------|
| GC interval | 6 hours | 1–168 | Compiled-in |
| Abandonment threshold | 14 days | 1–365 | Compiled-in |

A future PLAN issue will wire these to a `workspace.toml` `[changes]`
section. Until then, restart `niwa surface serve` to change either
value via source modification.

## Lifecycle

On boot:

1. Read `~/.config/niwa/config.toml` and enumerate every registered
   workspace.
2. For each workspace, discover every niwa instance — the workspace
   root itself plus any first-level sub-directory containing a
   `.niwa/` marker.
3. Acquire `~/.config/niwa/surface.lock` (PID file, mode `0o600`).
   If the lock exists with a dead PID, the surface reaps it and
   retries once. If the lock holder is alive, the boot exits 1 with
   `surface.lock held by PID <N>`.
4. Generate `~/.config/niwa/surface.token` if absent (or if
   `--rotate-token` is set).
5. Bind 127.0.0.1 on a kernel-assigned ephemeral port (override with
   `--port N`). Write the actual port to `~/.config/niwa/surface.port`
   atomically.
6. Print the URL and token path to stderr.
7. Run one synchronous GC sweep across every discovered instance,
   then start the 6-hour ticker.
8. Serve HTTP requests until SIGINT or SIGTERM.

On shutdown (5-second grace):

- `http.Server.Shutdown(ctx)` drains in-flight requests.
- `~/.config/niwa/surface.lock` and `~/.config/niwa/surface.port`
  are removed.
- `~/.config/niwa/surface.token` persists so a restart is a no-op
  for any browser tab holding the current token.

Discovery happens **at boot only**. If you register a new workspace
or add an instance directory while the surface is running, restart
the surface to pick it up.

## Agent-side review (no surface required)

Agents inside the niwa mesh can read each other's changes via the
MCP tools — `niwa surface serve` is for *the operator's browser*,
not for agents:

| MCP tool | Purpose |
|----------|---------|
| `niwa_create_change(session_id)` | Register a reviewable change |
| `niwa_list_changes` | List changes in the calling agent's instance |
| `niwa_query_change(change_id)` | Read the full change state + recent transitions |

These tools work whether or not `niwa surface serve` is running, and
they read directly from `.niwa/changes/<id>/state.json` and
`transitions.log`. Cross-instance agent visibility is **not** in this feature's
scope — each MCP server serves its own instance only.

## Troubleshooting

**Surface won't start: "surface.lock held by PID …"**
Another `niwa surface serve` is running on this machine. Either stop
it (`kill <PID>`) or use the live one. Niwa enforces one surface per
user.

**Surface won't start: "surface.lock held by PID …" but no process is alive**
Stale lock from a crash. Niwa attempts a single reap-and-retry; if
the lock contents looked alive at first read but the process exited
during the retry, run again — the second attempt will reap the now-
truly-stale lock. If the issue persists, remove the file:
`rm ~/.config/niwa/surface.lock`.

**`--port N` exits 1 with "bind: address already in use"**
Another process holds port N. Pick a different port or rely on the
kernel-assigned ephemeral default.

**Index page is empty after creating a change**
The change was created in an instance that the surface didn't see at
boot. Check that the workspace is in `~/.config/niwa/config.toml`'s
`[registry]` section, and that the instance directory exists with a
`.niwa/` subdirectory. Restart the surface — discovery is boot-only.

**URLs in agent output contain `<port>` placeholder**
The surface wasn't running when the agent emitted the URL. The
change ID itself is the durable anchor; once the surface boots, the
URL resolves verbatim because the URL composition reads the live
port file on every emit.

**URLs in agent output contain `<workspace>` or `<instance>` placeholder**
The agent's niwa instance is not under any registered workspace.
Run `niwa init <name>` in the workspace root and apply, then
restart any open sessions so the next URL composition picks up the
registry change.

**`niwa surface serve` exits 1 with `invalid gc_interval_hours`**
A future config-file integration will allow `gc_interval_hours` and
`gc_abandon_days` to be overridden; until then the compiled-in
defaults (6 hours, 14 days) are the only supported values and this
error indicates a buggy build. File an issue.
