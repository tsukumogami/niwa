---
status: Complete
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

Complete

Answered from a live always-on Linux desktop (Claude Code 2.1.207), ~12h of hourly
read-only probing, and the docs. **Key result, human-confirmed:** a ~12h-idle RC
session was NOT reachable from the phone, yet its local `bridgeSessionId` was STILL
present in `state.json`. So the local bridge field is a **stale, unreliable signal**:
niwa cannot detect a dropped RC bridge by reading local state, and the daemon does not
auto-recover an idle-dropped bridge. The one reliable mechanism is to **prevent the
drop with a heartbeat**. The only remaining micro-test (close-vs-archive local diff) is
a design-time follow-up, not a blocker for the direction. Live sessions were not
disturbed (read-only).

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

**Resolved (human-confirmed, 2026-07-14):** the ~12h-idle `feature_4_real_commute` was
**not reachable** from the phone while its local `bridgeSessionId` still showed present —
confirming reading (b). The local bridge field is a stale, unreliable signal; the design is
built around a heartbeat that prevents the drop, not detection.

Still also needing a manual step (not scriptable read-only): diff a throwaway session's
entry across an explicit **agents-TUI close** and a **claude.ai archive**, to see
whether either leaves a local signal distinct from an idle bridge-drop. That decides
whether entry-gone is a safe "developer is done" signal.

### Panel follow-up — platform facts (from the design-review librarian, Claude Code docs)

Two facts surfaced during the design review that sharpen the mechanism and belong on the
record with the other platform findings:

- **No deterministic launch-time arming.** No CLI flag/settings key schedules a cron at
  `claude --bg` startup, and hooks "can't trigger `/` commands or tool calls" — a SessionStart
  hook can only inject text. So a keep-alive wake can only be *nudged* into existence (the
  agent must self-arm a `/loop`/cron); there is no guaranteed arming seam.
- **The supervisor stops an idle process ~1h in.** Scheduled tasks fire only while the process
  runs, and the supervisor stops a session's process ~1 hour after it goes idle/unattached.
  So a keep-alive wake must fire *within* that hour (sub-hourly) and must itself keep the
  process alive. The session-scoped cron is confirmed in-process (dies with the session; no
  host-level task), and its TTL is a fixed 7 days from creation (no renewal).

### Existence proof — this session (human-observed, 2026-07-15)

The session running this very investigation (`~/.claude/jobs/71e6c42b`, name `niwa_keep_alive`,
`kind: bg`, RC on — `bridgeSessionId` present) **was launched by `niwa dispatch` and stayed
alive and reachable for ~1.5 days across many idle gaps, kept alive by its own periodic
self-wakes** (the `session_cron` created by the agent's scheduled-wakeup calls). This is a
live demonstration that the core mechanism works end to end on a real dispatched RC session:

- **Efficacy** (a self-wake keeps the session reachable across idle) — demonstrated.
- **Process survival past the ~1h supervisor stop** — demonstrated (the sub-/~hourly wakes
  kept the process from being paused/reaped for ~1.5 days).
- **Agent-mediated arming is reliable** — the agent armed and re-armed the wake dozens of
  times without failure. The instruction ("schedule a wake every N minutes") is trivial and
  unambiguous.

Two honest caveats: (1) the wakes here were **real, visible working turns**, not the designed
**non-visible / no-op** wake — so what remains to confirm is that a minimal, context-isolated
wake (still a real API round-trip, just hidden from the chat) keeps the bridge warm the same
way; and (2) no single idle gap here exceeded the ~1h wake interval, so a true multi-hour
single-gap survival is still worth one clean measurement. Both are narrower than the original
"is this feasible at all" question — which this session answers yes.

**Programmatic arming is not available on this platform.** Investigation of this host found no
writable cron/schedule file niwa could seed (the schedule lives in the running process,
replayed from the transcript), and Claude Code hooks cannot trigger `/`-commands or tool calls.
So the cron-creation step is necessarily agent-mediated; niwa's deterministic contribution is
the (fully niwa-controlled) arming instruction, not the scheduler call itself.

### No-op wake validation — A/B experiment (human-confirmed, 2026-07-16)

The one residual the existence proof left open — does a **minimal, non-visible no-op** wake
(not real work) keep the bridge warm? — was tested directly with two throwaway `niwa dispatch`
RC sessions left idle overnight:

- **A `keepalive_noop_test`** — armed a no-op ~hourly self-wake (session_cron), otherwise idle.
- **B `keepalive_control`** — no wake, fully idle.

Result after ~18h idle:

- **A stayed alive the entire time** (original process never terminated; the no-op wake fired
  ~hourly for 18h) **and was confirmed reachable from claude.ai** (the human opened and
  successfully steered it).
- **B's process was terminated at ~8h** (no live process; `state: done`) **and was confirmed
  unreachable from claude.ai** (did not respond).

**Conclusion: a non-visible, no-op self-wake is sufficient to keep a dispatched RC session
reachable across a long idle window, at near-zero context cost.** This closes the design's last
open question — the D2 non-visible/no-op wake shape works; no fallback to a visible wake is
needed. (Both the ~1h process-stop survival and the 6–12h+ bridge-idle survival are covered by
this single 18h run.)

### Archive does NOT stop keep-alive (human-confirmed, 2026-07-16)

The PRD's open Known Limitation — what a claude.ai **archive** does to the local job entry —
was tested directly: the surviving `keepalive_noop_test` was archived from the claude.ai web UI
while alive, and its local state was watched.

- **From the UI:** the session was gone (the human confirmed "gone from the UI").
- **Locally: nothing changed.** ~46 min after archive, the `~/.claude/jobs/<id>/state.json`
  entry still existed, `bridgeSessionId` still present, the process still alive, and the
  keep-alive **cron kept firing** (twice more after archive). Archiving is a **UI-only,
  server-side action that does not propagate to the local machine.**

**Implications (negative result):**
- **`niwa reap` does NOT see an archived session as done** — the jobs entry persists, so
  `sessionLive` stays true and the instance is not reclaimed.
- **Keep-alive keeps running on an archived session** — the cron keeps waking a session the
  user can no longer see, for up to the 7-day TTL, and its niwa instance leaks until then.
- **Archive is therefore NOT a valid stop signal.** Only closing/removing the session
  (agents-TUI close / `claude rm`), which removes the local jobs entry, stops keep-alive. The
  design documents this as a limitation: **close, don't just archive, to release keep-alive;**
  the 7-day cron TTL is the backstop for an archived-but-not-closed session.

## Recommendation (feeds the DESIGN)

The empirical result forces a single direction:

- **Keep-host-awake is out.** The host never sleeps; there is no unreachable window.
- **Detect-and-reconnect via local state is out.** The confirmed result — unreachable
  session, `bridgeSessionId` still present — means `state.json` does NOT reflect the
  real bridge state. niwa cannot detect a dropped bridge locally, so it cannot know when
  to reconnect. (F1's `bridgeSessionId` seam detects "was RC ever armed," not "is RC up
  now.")
- **The daemon does not rescue the idle case.** It respawns *dead workers* (F2), but an
  idle-dropped bridge leaves the worker alive with a stale bridge, so no respawn fires
  and the bridge is never re-established. "Lean on the daemon" does not cover this.
- **Prevent the drop with a heartbeat — the one thing that works.** Keep the opted-in
  session active often enough that its bridge never crosses the server-side idle
  threshold. The threshold is under ~12h and needs tuning empirically; nudge
  conservatively (e.g. hourly or more often).
- **`selfWake`/`session_cron` (F4) is the natural vehicle.** Claude Code already has
  session-scoped scheduled self-wake; a keep-alive should ride it (a session waking
  itself periodically) rather than niwa poking from outside — subject to its 7-day TTL,
  which itself bounds max keep-alive duration and must be re-armed.
- **Stop condition.** Since no local field distinguishes closed/archived from
  idle-dropped (F5) and the bridge field is stale, the keep-alive is anchored on niwa's
  own opt-in record plus the one real signal that does exist — the jobs **entry being
  removed** (`claude rm` / TUI close / cleanup). Heartbeat while the entry exists and the
  session is opted-in; stop when the entry is gone. A design-time throwaway-session test
  should still confirm exactly what a claude.ai **archive** does to the local entry.
