# Spike observations: jobs state + daemon (empirical, host = this machine)

Environment: Claude Code 2.1.207; no `ANTHROPIC_API_KEY` (RC not precluded);
`claude` at ~/.local/bin/claude. Observed 2026-07-13.

## Finding 1 — `bridgeSessionId` is an outside-observable RC-connectivity signal
`~/.claude/jobs/<id>/state.json` contains `bridgeSessionId` (value `cse_...`)
**only for sessions launched with `remoteControlAtStartup:true`**:
- 71e6c42b (this session), 32544a2d (commuter_wip), d12be1a5 (niwa_teleport):
  all have `respawnFlags` containing `--settings {"remoteControlAtStartup":true}`
  AND a `bridgeSessionId`.
- 4be9ad6d (a non-RC bg session): NO `bridgeSessionId`, respawnFlags lack the RC
  settings.
Implication: whether a session has an RC bridge is readable from the jobs
state.json by any outside process — no need for the in-session
`$CLAUDE_CODE_BRIDGE_SESSION_ID` env var. This is the detection seam the design's
"is RC connected?" question needs. (Whether the field is CLEARED when the bridge
drops is the key dynamic unknown — see Open.)

## Finding 2 — sessions are daemon-managed with a respawn supervisor
- `backend: "daemon"`, `daemonShort` on every bg entry.
- A persistent `claude daemon run` process (pid 89127, ~2.5h uptime) spawned by
  `claude agents`, with pooled `bg-pty-host` / `bg-spare` worker processes.
- daemon.log shows a supervisor loop: `[bg] bg adopt: adopted=N respawned=M dead=K`
  and `post-takeover prewarm burst — respawned X/Y stale workers`. `respawn`
  appears 19x in the log. On 2026-07-13T17:29:59 it logged `adopted=0 respawned=0
  dead=2` (two workers marked dead).
Implication: Claude Code ALREADY has a daemon that adopts and respawns background
workers. This is the existing machinery closest to keep-alive.

## Finding 3 — the RC-rearmed resume recipe is stored, not reconstructed
Each RC entry has `resumeSessionId` (== sessionId) and `respawnFlags` that INCLUDE
`--settings {"remoteControlAtStartup":true}` (plus `--name`, `--model`). So
"relaunch resumed with RC re-armed" is literally `claude` + respawnFlags +
resume — the command is persisted by Claude Code itself.

## Finding 4 — a self-wake / cron mechanism exists
This session's entry has `selfWake: true` and `inFlight: {tasks:1, kinds:
['session_cron']}` (the session_cron is this session's own ScheduleWakeup). So
Claude Code has a built-in scheduled self-wake primitive.

## Other state fields
`state` ∈ {working, blocked}; `firstTerminalAt` (null while non-terminal; set when
a session first went terminal — 4be9ad6d had it). niwa deliberately ignores these
for liveness (per exploration). `bridgeOutboundOnly: false`.

## Still OPEN (dynamic, not observable statically; needs live test or docs)
- When the RC process exits after >10min host-unreachable: does the jobs entry
  PERSIST (resumable) or get DELETED? Does `bridgeSessionId` get cleared?
- Does the daemon automatically respawn RC sessions and re-establish the bridge
  when the host returns online after sleeping — or is manual --resume needed?
- Does closing in the agents TUI vs archiving in claude.ai leave a LOCAL signal
  distinguishable from "worker died but session still wanted"?
(Do NOT experiment on the live commuter_wip / niwa_teleport sessions to answer
these — use a throwaway dispatched session or the real overnight scenario.)
