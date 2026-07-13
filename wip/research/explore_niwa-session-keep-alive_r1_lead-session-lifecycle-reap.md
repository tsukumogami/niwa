# Research: session lifecycle / reap / hooks (r1, lead 2)

## Findings

### Two unrelated "session" systems (do not conflate)
1. **Ephemeral instance sessions** — a Claude Code `--bg` (dispatch) session maps 1:1
   to a niwa instance (clone). This is what `reap`, `instance from-hook`,
   `session_map.go`, `job_state.go` implement. **This is the relevant system.**
2. **Worktree sessions** — `niwa worktree`/`session` git-worktree lifecycle inside an
   instance; explicit `status` (active/ended/abandoned). Different feature, does NOT
   participate in reaping.

### `niwa reap` (ephemeral instances)
- Reclaims ephemeral instances whose backing Claude Code session was **deleted**.
- Pull-based: runs on `niwa reap` and opportunistically at the start of `niwa create`
  (`reapOpportunistically`, reap.go:399). **Nothing schedules it.**
- Primary sweep destroys an instance only when ALL hold: `rec.Ephemeral` true,
  mapping `Ephemeral` true, `sessionLive`==false, `instanceHasLiveJob`==false.
- Backstop: unmapped orphans with dispatch-name pattern older than
  `dispatchBackstopTTL = 30 min` (reap.go:26) — governs ONLY unmapped orphans,
  unrelated to liveness.

### Liveness rule (THE core signal)
- `sessionLive` (`job_state.go:72`): a session is LIVE exactly while
  `~/.claude/jobs/<session-id>/state.json` **exists** (sessionId matches); DEAD once
  gone.
- Deliberately does NOT read the job `state` field, `firstTerminalAt`, or any idle
  TTL (job_state.go:54-59, reap.go:47) — so a live idle-but-resumable session is
  never reaped.
- **Entry present** = running OR idle OR suspended → instance kept.
  **Entry gone** = proxy for "developer deleted the session in Agent View" → reclaimed.

### `niwa instance from-hook` + hook wiring
- Hidden command invoked as the workspace-root SessionStart/SessionEnd hook.
- **SessionStart** → provisions instance (guarded by: `EphemeralSessionMode` opt-in,
  `isBackgroundWorker` (job template=="bg"), re-entrancy). 
- **SessionEnd** → **deliberate no-op** and **not even installed**. Comment: "SessionEnd
  is NOT a deletion signal. Claude fires it on idle-suspend (reason: resume), /clear,
  logout" (instance_from_hook.go:191-207; materialize.go:746-750).
- Other events (Stop, PreCompact) → no-op. No Stop-hook handling.
- Root settings materialized by `root_materializer.go` → only a `SessionStart` command
  entry, merged (not overwritten) with existing hooks; timeout 180s.

### State persistence
- `SessionMapping` (`session_map.go:49`) at `<workspaceRoot>/.niwa/sessions/<id>.json`:
  session_id, instance_name, instance_path, transcript_path, created, ephemeral, label,
  origin ("dispatch"). **No `status` field, no last-activity/idle timestamp.** Liveness
  is derived externally from the jobs entry, not stored.
- Claude job state (`~/.claude/jobs/<id>/state.json`) is read-only external; niwa never
  writes it. Fields read: sessionId, template ("bg"|"claude"), cwd.
- `EphemeralSessionMode` bool in `.niwa/instance.json` — master opt-in.

### Idle detection / timers / daemons?
- **None.** No idle detection, no idle timer, no cron/ticker, no per-session daemon.
  The design explicitly rejects an idle TTL for mapped instances.
- Only loops: bounded dispatch-capture poll (exits on capture), and a foreground
  signal-forwarding wrapper for `niwa session attach` (exits with child). Neither is a
  background daemon.
- niwa runs no persistent process per session; provisioning is synchronous inside the
  SessionStart hook; teardown synchronous inside reap.

### Distinguishing "closed/archived" from "idle"
- niwa **cannot** distinguish TUI-close vs claude.ai-archive vs idle semantically. It
  reduces all of it to one binary proxy: **jobs entry present vs gone.** SessionEnd is
  ignored. "Closed/archived" is inferred only transitively (user deletes session →
  Claude removes jobs entry → next reap notices absence).

## Implications
- A keep-alive that relaunches on process death needs a signal to separate
  "died but user still wants it" from "user closed it." niwa's ONLY current signal
  (jobs entry gone) is used for the latter. If the RC network-timeout exit LEAVES the
  jobs entry present (resumable), niwa gets a clean split:
  **entry present + RC bridge not connected → re-arm; entry gone → stop.** (UNVERIFIED)
- Adding keep-alive means adding niwa's FIRST scheduled/long-lived per-session watcher —
  a deliberate departure from the pull-based, daemon-free design. Architectural weight.
- There is a natural opt-in precedent (`EphemeralSessionMode`, `remote_control_on_dispatch`)
  to model a `keep_alive_on_dispatch` flag on.

## Open Questions
- Does the `--bg` worker's RC network-timeout exit remove or keep the jobs entry?
  (Central — determines whether niwa can cleanly tell "keep alive" from "user closed.")
- Is a per-session watcher acceptable given the daemon-free design, or should keep-alive
  ride an existing surface (e.g. an in-worker hook + `--continue`)?
