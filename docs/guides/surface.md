# niwa surface

The `niwa surface` listener renders the agent's in-flight changes as
plain HTML pages on `127.0.0.1`. A coordinator agent creates a change
via `niwa_create_change` after handing work off; the operator opens
the URL in a browser to review the diff before the agent moves on.

The surface is per-instance: one HTTP listener serves every session in
the instance. This is a separate process from the per-session MCP
daemon — they share the same `.niwa/` directory but run independently.

## Process topology

Two kinds of process talk to `.niwa/`:

- **Per-session daemon** (`niwa mcp-serve`): one per session, started
  when the session is created, owns the MCP loop for that session's
  worker agent. Lives at the worktree root.
- **Per-instance surface** (`niwa surface serve`): one per instance,
  started by the operator, owns the `127.0.0.1:<port>` HTTP listener
  that renders changes. Lives at the instance root.

The two processes don't call each other. The MCP daemon writes
`.niwa/changes/<id>/state.json` and emits `change_ready` to the audit
log; the surface reads those state files on each request. State on
disk is the only contract.

```
.niwa/
  changes/<id>/state.json      <- written by mcp-serve, read by surface
  changes/<id>/diff.patch
  changes/<id>/transitions.log
  surface.lock                 <- written by surface
  surface.token                <- written by surface, read on each auth check
  surface.port                 <- written by surface, read by mcp-serve for URL
  mcp-audit.log                <- written by mcp-serve and surface
```

## Quick start

Start the listener from the instance root:

```
$ niwa surface serve
niwa surface listening on http://127.0.0.1:54321
token stored at /path/to/instance/.niwa/surface.token (read-only, mode 0600)
```

The listener prints the URL to stderr and stays in the foreground. Open
the URL in a browser to see the index of changes. A change page lives
at `/changes/<change-id>`. Send SIGINT or SIGTERM to shut down.

`niwa_create_change` returns a URL embedded with the live port. If the
surface isn't running when the agent creates the change, the URL
contains the literal `<port>` placeholder; once the surface boots, the
next call to `niwa_query_change` returns the resolved URL.

## Auth model

The listener reads `.niwa/surface.token` on every request and admits
only `Authorization: Bearer <token>` headers that match. The token
file is created mode `0600` on first boot. Tokens passed in cookies or
query parameters are rejected — the auth surface is the request header
only, so a token can't leak through a referrer or a third-party iframe.

At F5 the auth middleware is wired but applied to zero routes (every
F5 route is a GET read). It's there so the F10 mutation API can compose
on the same contract without inventing a new one.

To rotate the token:

```
$ niwa surface serve --rotate-token
```

The flag regenerates `.niwa/surface.token` on boot. Any browser tab
holding the old token must reload to pick up the new one.

## Configuration

The surface reads two keys from `workspace.toml`:

```toml
[changes]
gc_interval_hours = 6     # how often the GC sweep runs (1-168)
gc_abandon_days   = 14    # age at which an unreviewed change is cleaned (1-365)
```

Out-of-range values cause `niwa surface serve` to exit 1 with a
configuration error before the listener accepts requests. Defaults are
`6` and `14`.

## Lifecycle

On boot:

1. Acquire `.niwa/surface.lock`. If the lock holder is dead, reap it
   and retry once.
2. Ensure `.niwa/surface.token` exists (regenerate on
   `--rotate-token`).
3. Bind `127.0.0.1:<port>` (ephemeral by default; `--port N` overrides).
4. Write the bound port to `.niwa/surface.port` atomically.
5. Print the URL and token-file path to stderr.
6. Run the GC sweep once, then start the listener.

On shutdown:

- Catch SIGINT/SIGTERM.
- `http.Server.Shutdown` with a 5-second deadline.
- Remove `.niwa/surface.lock` and `.niwa/surface.port`.
- Leave `.niwa/surface.token` in place so a restart doesn't invalidate
  open browser tabs.

The token contents are never printed to stderr or the audit log. Only
the path to the token file is shown.

## Troubleshooting

### Stale lock

If `niwa surface serve` reports `lock held by PID 12345`, the previous
process likely crashed without cleaning up. Check whether PID 12345 is
alive:

```
$ ps -p 12345
```

If the PID is gone, the surface reaps the stale lock on its next boot
attempt — try the command again. If the PID belongs to a live niwa
process, that's the legitimate holder; only one surface per instance.

### Port collision

`--port N` binds a specific port. If something else owns it, the
listener fails with `bind 127.0.0.1:N: address already in use`. Pick a
different port or omit the flag to let the kernel pick an ephemeral
one.

### Missing `.niwa/surface.port`

The agent returns a URL with `<port>` instead of a real number when
`.niwa/surface.port` doesn't exist. That just means the listener isn't
running. Start it, then re-query the change via `niwa_query_change` to
get a resolved URL.

### Where the audit log goes

Both `niwa surface serve` and `niwa mcp-serve` append to the same
`.niwa/mcp-audit.log`. The schema is v=2 NDJSON; the `kind` field
distinguishes `tool_call` entries (from MCP dispatch) from `event`
entries (change-lifecycle hooks). `tail -f` works fine for live
inspection.
