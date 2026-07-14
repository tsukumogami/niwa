---
status: In Progress
question: |
  For an optional keep-alive over niwa-dispatched remote-control sessions, can niwa
  reliably tell a still-active session that merely lost its RC connection apart from
  one the developer deliberately closed (agents TUI) or archived (claude.ai) — and
  how much of the keep/respawn/re-arm machinery already exists in Claude Code's
  daemon versus needing to be built in niwa?
timebox: "1 session, static observation + docs; one live overnight test deferred to the real host"
---

# SPIKE: niwa session keep-alive — RC lifecycle and the close-vs-lost signal

## Status

In Progress

The statically-observable questions are answered from a live machine (Claude Code
2.1.207) and the docs. The dynamic question is now under **direct live observation on
the real host** (see "Live observation" below): the host is an always-on Linux desktop
that never sleeps, yet its idle RC sessions still get disconnected after several hours —
so a read-only watcher is recording what changes locally at the moment a bridge drops.
The live sessions on the observed machine are NOT disturbed (read-only).

### Diagnosis correction (load-bearing)

The exploration's original diagnosis — "a laptop that sleeps overnight trips the ~10-min
host-unreachable timeout" — is **wrong for the reported environment.** The host is an
always-on Linux desktop that never sleeps and stays networked; its RC sessions still
disconnect after hours of idle. Consequences:

- **Keep-host-awake is NOT the fix** — the host is already always awake and reachable.
- The Claude Code docs claim pure idle with network up does not disconnect; the observed
  behavior contradicts that. The real trigger is an **idle-driven bridge drop on a
  reachable host** (server-side idle expiry of the bridge, a token/session TTL, or the
  daemon retiring an idle worker — the live watch will narrow this).
- The viable mechanisms are therefore **(a) a heartbeat/nudge that keeps the session
  active so the bridge never idles out**, and/or **(b) detect the dropped bridge and
  re-establish it** (`claude respawn <id>`, RC re-armed via stored `respawnFlags`). The
  earlier-deprioritized "nudge" approach is back in contention precisely because the
  cause is idle, not unreachability.

## Question

The design crux (surfaced during `/scope` on this topic): niwa's only existing
session signal is present-or-gone of `~/.claude/jobs/<id>/state.json`, and that same
"gone" is the proxy for "the developer closed the session." If a lost RC connection
also removes the entry, niwa cannot tell "keep this alive" from "the developer is
done." Before designing around a signal, verify what signals actually exist, and
whether Claude Code's own daemon already delivers the keep/respawn behavior.

## Method

- Inspected `~/.claude/jobs/<id>/state.json` across 5 real background sessions on the
  host (3 RC, 1 non-RC, 1 partial), read-only.
- Inspected the running `claude daemon` process tree, `~/.claude/daemon.log`, and
  `daemon.status.json`, read-only.
- Cross-checked against Claude Code docs (remote-control, cli-reference,
  scheduled-tasks) and changelog.
- Did NOT experiment on the host's live sessions; the dynamic transition is left as
  a scripted live test.

## Findings

### F1 — RC-connectivity is observable from outside the session (verified)
`state.json` carries `bridgeSessionId` (`cse_...`) **only** for sessions launched
with `remoteControlAtStartup:true`; the non-RC session lacked it. So an outside
process (niwa) can read "does this session have an RC bridge" directly from the jobs
state.json — it does not need the in-session `$CLAUDE_CODE_BRIDGE_SESSION_ID` env var.
This is the RC-connectivity detection seam the design needs.

### F2 — Claude Code already runs a respawn supervisor (verified)
Sessions are `backend: "daemon"`, managed by a persistent `claude daemon run` process
with pooled `bg-pty-host`/`bg-spare` workers. `daemon.log` shows a supervisor loop:
`[bg] bg adopt: adopted=N respawned=M dead=K` and post-takeover prewarm bursts that
respawn stale workers. A `claude respawn <id>` command and `claude daemon status` /
`claude daemon stop --any` exist (cli-reference). The daemon respawns dead background
workers on its own.

### F3 — the RC-rearmed resume recipe is persisted by Claude Code (verified)
Each RC entry stores `resumeSessionId` and `respawnFlags`, and those flags **already
contain** `--settings {"remoteControlAtStartup":true}` (plus `--name`, `--model`).
So a respawn re-arms RC by construction; niwa would not need to reconstruct the launch
command.

### F4 — a scheduled self-wake primitive exists (verified)
RC entries carry `selfWake: true`; the observed session had an in-flight
`session_cron`. Claude Code has session-scoped cron/`/loop`/`/schedule` (7-day TTL on
recurring tasks per docs). Not documented as a keep-alive, but it is a real
"periodically wake the session" primitive that could keep a session active.

### F5 — no local signal distinguishes "closed/archived" from "worker died" (verified gap)
The docs do not expose a close-vs-archive lifecycle, and the state.json carries no
`closed`/`archived` flag. `claude rm` removes a job (changelog notes it used to linger
in the daemon roster). So at the local-state level, "developer closed it" and "the
worker died" are NOT distinguishable by a dedicated field — both converge on the entry
being removed or the worker being absent. This confirms the crux: niwa cannot safely
lean on entry-gone alone to mean "the developer is done."

### F6 — dynamic timeout/wake behavior (inferred, NOT directly observed)
Docs: a host unreachable > ~10 min times out and the RC process exits; sessions
"reconnect automatically when your machine comes back online." Changelog fixes around
sleep/wake ("background sessions silently stopping mid-turn after sleep/wake") imply
recovery is real but imperfect. Inference: the jobs entry persists across a timeout
exit and the daemon respawns with RC re-armed, but whether the respawn re-establishes
a reachable bridge (and how fast) is unconfirmed.

## Live observation (in progress on this always-on host)

Because the host never sleeps, the idle-disconnect reproduces here without any sleep
step. A read-only watcher (`wip/research/spike_niwa-session-keep-alive_timeseries.md`)
snapshots every RC session's `bridgeSessionId`/`state`/`firstTerminalAt` every 30 min
and records what changes when a bridge drops. Baseline (2026-07-13T20:07Z) had 5 live
RC bridges, including idle `done`/`blocked` sessions still holding their bridge. The
watch answers, from real data:

1. When an idle RC session finally disconnects, does `bridgeSessionId` get **cleared**,
   does the **entry get removed**, or does neither (only a server-side change invisible
   locally)?
2. Does the daemon log a respawn / does the bridge come back on its own?
3. How long does idle-to-disconnect actually take here?

### Result (after ~12h of hourly probing, 2026-07-13T20:07Z → 2026-07-14T07:57Z)

**The local `bridgeSessionId` did NOT clear on idle.** The cleanest specimen,
`feature_4_real_commute` (9a06b95e), went terminal at 2026-07-13T19:42 and its
`updatedAt` stayed frozen at 19:42:44 (genuinely untouched) for **~12 hours**, yet its
`bridgeSessionId` remained present in `state.json` the entire time. `commuter_wip`
(idle ~11.7h) likewise held its bridge. Across the full watch, **zero** watched RC
sessions showed a local `BRIDGE-CLEARED` or idle `ENTRY-REMOVED` event; the only
removal seen was the iteration-1 completion-cleanup (`bg settled ... killed`).

By the reported timing (idle RC sessions disconnect within ~6–12h), a session idle
since 19:42 should have dropped by ~07:42. It did not drop *locally*. Two readings
remain, and they are NOT separable from inside the host:

- **(b) The local field is stale** — the RC connection dropped server-side but
  `state.json` still shows the old `bridgeSessionId`. If so, **niwa cannot detect a
  dropped bridge by reading `state.json`**, and the design must PREVENT the drop
  (heartbeat) rather than detect-and-reconnect.
- **(a) The connection did not actually drop** for these sessions — in which case the
  disconnect trigger is narrower/different than assumed and needs re-characterizing.

**Decisive missing datum (needs the human):** check from the phone / claude.ai whether
a ~12h-idle session (e.g. `feature_4_real_commute`) is *actually reachable right now*.
- Not reachable, but local bridge still present → confirms (b): local state is a stale
  signal; design around a heartbeat.
- Reachable → the local bridge is trustworthy and the disconnect is rarer/slower than
  the 6–12h estimate; re-scope the trigger.

Still also needing a manual step (not scriptable read-only): diff a throwaway session's
entry across an explicit **agents-TUI close** and a **claude.ai archive**, to see
whether either leaves a local signal distinct from an idle bridge-drop. That decides
whether entry-gone is a safe "developer is done" signal.

## Recommendation (feeds the DESIGN)

With the always-on-host correction, the feature reshapes as follows:

- **Keep-host-awake is out.** The host never sleeps; there is no unreachable window to
  prevent. Do not build a sleep inhibitor.
- **The cause is an idle bridge-drop on a reachable host.** The two candidate mechanisms
  are (a) a **heartbeat** that keeps the session active so the bridge never idles out,
  and (b) **detect-and-re-establish** the dropped bridge. The live watch decides which is
  feasible: if the drop clears `bridgeSessionId` but leaves the entry resumable, (b) is
  clean; if a heartbeat prevents the drop entirely, (a) is simpler. They may combine.
- **Lean on the daemon, don't rebuild it.** F2/F3 show Claude Code already respawns dead
  RC workers with RC re-armed via stored `respawnFlags`. niwa's recovery layer, if any,
  should be thin: read `bridgeSessionId` presence (F1) to detect a dropped bridge and, at
  most, invoke `claude respawn <id>` — not a bespoke supervisor.
- **`selfWake`/cron (F4)** is the most promising heartbeat primitive: Claude Code already
  has session-scoped scheduled wake. A keep-alive could ride it (7-day TTL, undocumented
  for this use) rather than niwa poking the session from outside.
- **The close-vs-lost signal (F5) still gates safety.** Until a throwaway-session test
  shows that an agents-TUI close / claude.ai archive leaves a local signal distinct from
  an idle bridge-drop, the design must not treat entry-gone (or bridge-gone) as
  "developer is done" and must not auto-resurrect on it.
