# Exploration Decisions: niwa-session-keep-alive

## Round 1
- **Diagnosis fixed to host-unreachable, not idle:** the user confirmed the host is a
  laptop/desktop that sleeps overnight. This matches the Claude Code Remote ~10-min
  unreachable timeout, and rules out the "inject periodic activity to beat an idle
  timeout" approach (there is no idle timeout to beat).
- **Keep-host-awake is the NECESSARY mechanism:** RC requires the local process running
  and the machine reachable; a sleeping laptop is unreachable during the commute and its
  on-host watcher is also asleep. So auto-reconnect alone cannot deliver morning
  reachability for a sleeping host — a sleep/idle inhibitor held while an opted-in live
  RC dispatch session exists is required.
- **Auto-reconnect is the recovery layer (user picked "faithful"):** on top of keep-awake,
  niwa relaunches the session resumed (`--continue`/`--resume`) with RC re-armed if the
  bridge dies anyway, stopping only on TUI-close/archive. Feature = keep-awake + supervised
  re-arm, both opt-in.
- **Nudge/heartbeat approach eliminated:** pure idle does not disconnect per docs, so
  injecting activity does not address the real cause and pollutes the session.
- **Close/archive signal = jobs-entry-gone (current best proxy):** keep-alive stops when
  `~/.claude/jobs/<id>/state.json` disappears. Whether a network-timeout exit leaves the
  entry present (resumable) is the key unverified fact that makes this proxy safe; flagged
  as a design-time spike input, not a blocker to designing.
- **Opt-in surface modeled on existing precedent:** a `keep_alive_on_dispatch`-style flag /
  `[global]` config, mirroring `remote_control_on_dispatch` and `EphemeralSessionMode`.
- **Accepted architectural cost:** this adds niwa's first long-lived per-session watcher,
  a deliberate departure from the pull-based, daemon-free lifecycle design; the design doc
  must justify and contain that.
