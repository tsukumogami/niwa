# Research: dispatch + RC wiring (r1, lead 1)

## Findings

### `niwa dispatch` end-to-end
- Command in `internal/cli/dispatch.go`. Flags: `--label`, `--name/-n`, `--model`,
  `--permission-mode`, `--agent`, `--detach/-d`.
- `runDispatch` (dispatch.go:123): validates prompt, resolves workspace root
  (`workspace.ClassifyCwd`), preflights `claude` on PATH, generates ephemeral
  instance name `<config>+<slug>-<8hex>`, opportunistic reap, provisions instance,
  drops `.niwa/dispatch-pending` marker, loads global config, resolves model,
  builds passthrough argv, RC default-fill, **launches worker**, captures the
  worker's session UUID+short id (30s timeout), writes durable `SessionMapping`
  (Origin `"dispatch"`, Ephemeral `true`), prints attach/logs/stop hints, then
  attaches unless `--detach`.
- **Launched process** (`dispatch_launcher.go:31`, `buildClaudeBgArgs:66`):
  `claude --bg [passthrough...] <prompt>` with `cmd.Dir = instanceDir`, inheriting
  `os.Environ()`. Passthrough: `--model`, `--permission-mode`, `--agent`, `--name`,
  optionally `--settings <json>` for RC.
- `--detach/-d`: skips the final terminal `claude attach <shortID>`; background
  worker launched identically. Fan-out/scripting mode.

### Remote Control (RC)
- RC = **Claude Code Remote**, requires first-party **claude.ai login** (NOT API key)
  — `dispatch_remotecontrol.go:21`.
- Enabled by injecting Claude Code settings key **`remoteControlAtStartup: true`**.
  Constant: `config.go:346` `RemoteControlAtStartupKey`.
- Three agreeing sites: materializer (`materialize.go:601-615`, only when user sets
  `[claude.settings] remoteControlAtStartup`), dispatch argv injection
  (`dispatch.go:258-260`, `--settings {"remoteControlAtStartup":true}`), read-back
  resolver.
- Two ways a dispatched session gets RC:
  (a) downstream `[claude.settings] remoteControlAtStartup = "true"` → materialized.
  (b) host default `[global] remote_control_on_dispatch = true`
      (`registry.go:30-36`) → `resolveDispatchRemoteControl` (`dispatch_remotecontrol.go:37-50`)
      injects `--settings` on dispatch. Downstream decision (even "off") wins;
      `ANTHROPIC_API_KEY` present precludes RC (prints warning, no inject).
- **Transport is opaque to niwa.** niwa only flips the boolean; there is no
  websocket, claude.ai session object, or reconnection code in niwa. The RC
  bridge lifecycle lives entirely inside Claude Code / Claude Code Remote.

### Session lifecycle tracking (two unrelated notions)
- **(A) Dispatch/ephemeral liveness** (`job_state.go`): binary present/absent, NOT
  a state machine. `sessionLive` (:72-91) = LIVE while `~/.claude/jobs/<id>/state.json`
  exists (sessionId matches); DEAD once gone. Deliberately ignores the job `state`
  field, `firstTerminalAt`, and any idle TTL (:56-60) — so a live idle-but-resumable
  session is NOT reaped. Entry-present = running OR idle; entry-gone = user deleted
  the session. No "archived" concept.
- **(B) Named worktree-session states** (`worktree/session_lifecycle.go:164-169`):
  `active` / `ended` / `abandoned`. Separate feature (`.niwa/sessions/<sid>.json`).
  Plus attach availability (`attach_state.go`): available/attached/stale from a live
  PID lock.

### Keep-alive / heartbeat / idle-timeout / reconnect
- **None exists.** Repo-wide grep found zero implementations. Only "idle" mentions
  are the `job_state.go:56-59` doc comments stating liveness must NOT consult an
  idle TTL. Only dispatch-path timeout is `dispatchCaptureTimeout = 30s` (one-shot
  session-id capture, not keep-alive).

## Implications
- niwa's role in RC is a single boolean flip at launch; it holds no live connection.
- Any keep-alive niwa adds must live OUTSIDE the worker process (niwa launches
  `claude --bg` and returns) OR be injected into the worker's own config/hooks.
- niwa already tracks a durable `SessionMapping` and knows the session UUID + jobs
  dir, and already has a present/absent liveness check it can build on.

## Open Questions
- What actually drops the RC connection on idle — the worker process exiting, a
  Claude Code Stop lifecycle, or a cloud-side RC timeout? (lead 2 / lead 3)
- Is there a supported way to re-arm RC on a still-live session without a full
  relaunch? (lead 3)
