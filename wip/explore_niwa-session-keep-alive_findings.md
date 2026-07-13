# Exploration Findings: niwa-session-keep-alive

## Decision: Crystallize

## Core Question

Can niwa optionally keep an RC-enabled dispatch session reachable from the phone
across long idle periods, stopping only once the user has explicitly closed it in the
Claude agents TUI or archived it in the claude.ai web UI?

## Round 1

### Key Insights

- **niwa's RC role is a single boolean flip at launch, nothing more.** dispatch runs
  `claude --bg [flags] <prompt>`, optionally injecting `--settings
  {"remoteControlAtStartup":true}`, captures the session id once, and then tracks the
  session only by presence/absence of `~/.claude/jobs/<id>/state.json`. There is no
  niwa-side connection, poll loop, heartbeat, daemon, or reconnect. The RC bridge
  lifecycle lives entirely inside Claude Code. (lead 1, lead 2)

- **The real disconnect trigger is host-unreachable > ~10 min, not "idle."** Per the
  Claude Code Remote docs, a session stays alive as long as the local process runs and
  the network is reachable; pure user-inactivity does NOT disconnect. But if the machine
  is unreachable (sleep / network drop) for ~10 minutes, the RC process **times out and
  exits.** "Auto-reconnect on wake" only rescues outages shorter than that window. So an
  overnight sleep or network gap almost certainly kills the process, and by morning
  there is nothing to reconnect to. (lead 3)

- **niwa has exactly one lifecycle signal, and it is overloaded.** "Jobs entry present"
  = live/idle/resumable → keep. "Jobs entry gone" = the proxy for *"the user deleted the
  session"* (TUI-close / archive) → reap. niwa cannot semantically tell close vs archive
  vs idle vs process-death apart — it only sees present vs gone. SessionEnd is explicitly
  ignored (the hook is a no-op and isn't even installed). (lead 2)

- **A clean keep-alive signal MIGHT already exist — if the death leaves the entry
  behind.** If the RC network-timeout exit leaves `~/.claude/jobs/<id>/state.json`
  present (session still resumable), niwa gets a clean split: *entry present + RC bridge
  not connected → re-arm RC; entry gone → user closed it → stop.* This is the linchpin
  and is currently UNVERIFIED. (lead 2, lead 3)

- **Useful detection seams exist:** `$CLAUDE_CODE_BRIDGE_SESSION_ID` is set only while an
  RC connection is active (v2.1.199+); `--continue`/`--resume` reopen a session;
  `SessionEnd` fires on termination. These are the primitives a supervisor would use.
  (lead 3)

### Tensions

- **User's model vs. the evidence.** The user frames it as "goes idle → disconnected."
  The docs say idle alone doesn't disconnect; the culprit is host-unreachable >10 min.
  If true, a "keep-alive nudge" (inject activity) is the *wrong* fix; "keep the host
  reachable" or "detect death and relaunch resumed" is the right one. Needs the user's
  observation of what the overnight host actually does (sleeps? laptop vs server? ISP
  drop?).

- **Faithful keep-alive vs. niwa's daemon-free design.** niwa is deliberately pull-based:
  no timers, no per-session daemon, reap runs only on demand / at `niwa create`. A
  detect-and-reconnect supervisor is niwa's first long-lived per-session watcher — real
  architectural weight against a design that explicitly rejected idle TTLs and tickers.

- **"Don't resurrect what the user closed."** Keep-alive must stop on TUI-close/archive.
  But if process-death and user-close both surface to niwa as the same "entry gone" (or
  are otherwise indistinguishable), an aggressive relaunch could revive a session the
  user meant to end. The safety of the whole feature hinges on a reliable close/archive
  signal.

### Gaps

- **Empirical unknown:** what happens to `~/.claude/jobs/<id>/state.json` when the `--bg`
  RC worker exits on network timeout — does the entry persist (resumable) or vanish? Code
  research can't answer this; it needs an experiment or the user's knowledge.
- **Overnight host behavior unknown:** does the user's machine sleep, or stay up with
  intermittent network? Determines whether "keep host awake" even applies.
- **Archive semantics:** claude.ai "archive" is not exposed to niwa in any documented
  way; whether archive removes the jobs entry locally is unknown.

### User Focus

- **Host = laptop/desktop that sleeps overnight.** Confirms the ~10-min host-unreachable
  timeout as the cause; rules out any "beat an idle timeout" approach.
- **Ambition = faithful auto-reconnect.** Wants niwa to keep the session reachable and
  relaunch-resumed with RC re-armed on death, stopping on TUI-close/archive.
- **Derived insight (stated to user):** for a sleeping laptop, auto-reconnect alone is
  insufficient — the machine is unreachable during the commute and the on-host watcher is
  asleep too. Keeping the host awake/networked is the NECESSARY precondition; auto-reconnect
  is the recovery layer. The feature is both, opt-in.

## Accumulated Understanding

niwa treats Remote Control as an opaque Claude Code feature it enables with one settings
boolean at dispatch time; it holds no live connection and no scheduler. The disconnect
the user hits is almost certainly Claude Code Remote's ~10-minute host-unreachable
timeout firing overnight (sleep or network gap), which exits the local `claude --bg`
process — not a pure-idle timeout. Claude Code exposes no supported keep-alive/heartbeat;
the only levers are keeping the host reachable, or detecting the death and relaunching
the session **resumed** with RC re-armed. Either lever, to be safe, needs a reliable way
to tell "the user is done with this session" (TUI-close / claude.ai-archive) from "the
process died but the user still wants it." niwa's single existing signal — jobs-entry
present vs gone — may supply exactly that split, *if* a network-timeout exit leaves the
entry resumable; that is the key unverified fact. Building this also means adding niwa's
first per-session background watcher, a deliberate departure from its pull-based,
daemon-free lifecycle model, so the design must weigh faithfulness against that weight.
