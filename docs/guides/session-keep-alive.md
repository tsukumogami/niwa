# Session keep-alive

Keep-alive keeps a dispatched remote-control session reachable from claude.ai
across long idle periods. A remote-control worker's bridge drops after several
hours of idle even on an always-on, networked host, and the drop is not visible
in local state — the session looks alive but stops answering from the phone or
the web UI. Keep-alive prevents the drop instead of trying to detect it: at
launch, `niwa dispatch` instructs the worker to schedule a small periodic
self-wake, so the session never idles long enough for its bridge to lapse.

It is opt-in, default off, and applies only to workers that start with remote
control. A dispatch that does not opt in is byte-identical to today.

## Opting in

Three surfaces, resolved as flag > downstream > host default, default off:

1. **Per dispatch**: `niwa dispatch --keep-alive <prompt>`. The flag is
   tri-state — bare `--keep-alive` forces on and `--keep-alive=false` forces
   off, overriding both lower layers in either direction.
2. **Per workspace (downstream)**: `keepAliveOnDispatch = "true"` under
   `[claude.settings]` in the workspace config. niwa materializes it into the
   instance's settings and reads it back at dispatch, the same seam
   `remoteControlAtStartup` rides. A decided downstream value is never
   overridden by the host default.
3. **Host default**: `keep_alive_on_dispatch = true` under `[global]` in
   `~/.config/niwa/config.toml`, mirroring `remote_control_on_dispatch`.

Keep-alive is meaningful only for a remote-control session — the self-wake
exists to keep the RC bridge warm. Requesting it for a worker that starts
without remote control prints a warning and arms nothing; the dispatch still
succeeds.

## How it works

When the opt-in resolves on and the worker starts with remote control, dispatch
prepends a fixed, niwa-authored instruction to the task prompt. The worker's
agent acts on it in its first turn: it schedules exactly one session-scoped
self-wake firing every 30 minutes whose action is a no-op — no tools, no state
changes, no visible output — and skips the arming when one already exists. The
interval is a fixed constant, not user-configurable: it must stay under the
roughly one-hour idle process stop, and sub-hourly wakes at a no-op's cost are
cheap (the wake is non-visible and context-isolated, so it does not clutter the
conversation or consume the session's context).

niwa does nothing at runtime. The wake lives entirely inside the session and
stops on its own when the session's job entry is gone. The opt-in is recorded
on the durable session mapping (`keep_alive` in
`.niwa/sessions/<session-id>.json`), which powers observability only — the
reaper never reads it, so keep-alive can never defer or block reclamation.

## Seeing what is kept alive

`niwa list` marks instances whose sessions are being kept alive right now —
opted in at dispatch and still live:

```
myws+worker-1a2b3c4d (keep-alive)
myws+other-5e6f7a8b
```

With `--json`, the kept-alive record carries `"keep_alive": true`; every other
record keeps the exact pre-keep-alive shape. A kept-alive session that has
since been deleted is not reported — its self-wake died with it.

## Releasing keep-alive: close, don't archive

Keep-alive stops when the session's Claude Code job entry is removed:

| Action | Job entry | Keep-alive |
|---|---|---|
| Close/remove the session (agents TUI close, `claude rm`) | removed | stops; `niwa reap` reclaims the instance |
| `claude stop` | kept | process halted, but the session still reads as live |
| Archive in claude.ai | kept | **keeps running** |

Archiving in claude.ai is a UI-only, server-side action: it does not remove the
local job entry, so the self-wake keeps firing on a session you can no longer
see and its instance stays un-reaped. To release keep-alive promptly, **close
(remove) the session, don't just archive it**. The backstop for an
archived-but-not-closed session is the scheduled wake's own TTL: session-scoped
schedules expire about 7 days after creation, and niwa arms only at launch with
nothing to renew them, so keep-alive lapses on its own by then.

That TTL also bounds the feature overall: a single opt-in keeps a session
reachable for at most about 7 days. That covers the overnight and
multi-day-idle cases keep-alive exists for; a session you want reachable beyond
that needs a fresh dispatch.

## Validation

The mechanism was validated end to end against claude.ai before it shipped: a
dispatched RC session armed with a no-op, non-visible self-wake stayed
reachable across an 18-hour idle window while an identical un-armed control
session's process was stopped at about 8 hours and became unreachable. The
close/remove teardown chain and the archive limitation above were confirmed in
the same test. See `docs/spikes/SPIKE-niwa-session-keep-alive.md`. The
functional suite (`test/functional/features/keep-alive.feature`) covers
everything up to the launch seam — resolution, the injected payload, the
mapping record, and the `niwa list` report; the live reachability effect itself
is not automatable offline.
