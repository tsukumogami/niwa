# Research: Claude Code RC idle-disconnect behavior (r1, lead 3)

Sources: https://code.claude.com/docs/en/remote-control.md, hooks.md

## Findings

### How RC works
- Opt-in. Activated by `claude remote-control` (server mode), `claude --remote-control`,
  `/remote-control`, or `remoteControlAtStartup: true` (v2.1.203+).
- Local `claude` process registers with the Anthropic API and polls for work.
  claude.ai/code + mobile route messages to the local session over HTTPS/TLS.
- Session runs LOCALLY; only the control interface is remote. Process must stay up.

### Idle / disconnect lifecycle (THE ROOT CAUSE)
- **Network-outage timeout ~10 min:** if the machine is awake but unreachable for
  ~10 minutes, the Remote Control process **times out and exits**.
- **Local process must stay running:** close terminal / quit / stop `claude` → session
  ends immediately.
- **No documented user-inactivity timeout:** docs say a session stays alive as long
  as the local process runs AND the network is reachable. Pure idle (no prompts) with
  network OK does NOT disconnect per docs.
- **Sleep/wake:** "if your laptop sleeps or your network drops, the session reconnects
  automatically when your machine comes back online" — BUT this only helps if the
  outage was under the ~10 min window. A multi-hour overnight sleep/network drop
  exceeds it → the RC process has already exited → nothing to reconnect to.

**Most likely explanation of the user's symptom:** overnight the host sleeps or
loses network for >10 min → the RC bridge times out and the `claude` process exits
→ session is dead/unreachable by morning. The "auto-reconnect on wake" does not
save a session whose process already exited.

### States: closed vs archived
- Docs do NOT formally distinguish "closed" vs "archived" for RC.
- States seen: Active/Connected; Offline (session exists, machine unreachable —
  grayed-out computer icon in claude.ai/code list); Ended/Exited (`/exit` or process
  stop); Resumed.
- No explicit "archive" action; ended sessions linger in the claude.ai/code list
  until manually removed or aged out by `cleanupPeriodDays` (default 30).

### Keep-alive primitives
- **None built in.** No heartbeat, no keep-alive flag, no SDK method to extend an RC
  session's lifetime.
- What exists: `--continue` (resume most recent RC session from that dir);
  automatic reconnect **when the machine comes back online**;
  `$CLAUDE_CODE_BRIDGE_SESSION_ID` env var set while an RC connection is active
  (v2.1.199+) — lets hooks/scripts detect connection state.

### Hooks
- No hooks for RC idle or network status ("There are no built-in hooks for Remote
  Control session status or network connectivity changes").
- `SessionEnd` fires on termination (reasons: clear, resume, logout,
  prompt_input_exit, bypass_permissions_disabled, other).
- `$CLAUDE_CODE_BRIDGE_SESSION_ID` presence = RC connection active (poll it to detect).

## Implications
- niwa **cannot prevent** the disconnect via any supported keep-alive primitive.
- Realistic niwa strategies:
  1. **Keep the host reachable** — prevent sleep / hold network (out of niwa's
     usual remit; host-level).
  2. **Detect-and-relaunch** — watch for the worker/bridge dying and relaunch the
     session **resumed** (`--continue`/`--resume`) with `remoteControlAtStartup`
     re-armed, so it re-registers with the API and becomes reachable again.
  3. **Inject periodic activity** — a nudge that keeps the process from ever going
     idle. But per docs pure idle doesn't disconnect, so this likely does NOT
     address the real (network-timeout) cause. Lower value.
- `$CLAUDE_CODE_BRIDGE_SESSION_ID` and `SessionEnd` are the detection seams a niwa
  supervisor could use.

## Tension (for convergence)
- The user's mental model is "goes idle → disconnected." Docs say pure idle does
  NOT disconnect; the real trigger is almost certainly network/sleep >10 min.
  If so, a "keep-alive nudge" is the wrong fix and "detect-and-relaunch-resumed"
  (or keep-host-awake) is the right one. This reframing needs user confirmation
  and possibly empirical checking of what actually dies overnight.

## Open Questions
- When the RC process exits on network timeout, is the Claude Code job entry left
  resumable (present) or removed? Determines whether niwa sees it as "idle/live"
  or "deleted." (lead 2)
- Does the user's host actually sleep overnight, or stay awake with network up?
  Changes the diagnosis and the fix.
