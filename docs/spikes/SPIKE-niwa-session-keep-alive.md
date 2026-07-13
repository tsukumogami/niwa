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
2.1.207) and the docs. One dynamic question — what the RC process's
host-unreachable-timeout exit does to the jobs entry and bridge, and whether the
daemon re-establishes the bridge on wake — could not be forced inside a short
background session and is specified below as a live overnight test on the real host.
The live sessions on the observed machine were NOT disturbed.

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

## What remains unverified — live overnight test (deferred to the real host)

Run on the actual laptop that sleeps, with a throwaway RC dispatch session (never on a
session you care about):

1. `niwa dispatch "sleep-test; do nothing" --name ka-test` with RC on; confirm the new
   `~/.claude/jobs/<id>/state.json` has a `bridgeSessionId`.
2. Record the entry's `bridgeSessionId`, `state`, `updatedAt`, and the file's mtime.
3. Let the laptop sleep overnight (or cut its network > 15 min to force the timeout).
4. In the morning, BEFORE touching anything, capture: does the entry still exist? is
   `bridgeSessionId` still present / changed / cleared? did `daemon.log` log a respawn?
   is the session reachable from the phone?
5. Separately: close a second throwaway session in the agents TUI, and archive a third
   in claude.ai; diff each entry before/after to see exactly what each action does to
   the local state (entry deleted? a flag set?).

The answers to (4) and (5) decide whether the "detect-and-respawn" layer is needed at
all, and whether entry-gone is a safe close signal.

## Recommendation (feeds the DESIGN)

The spike reframes the feature substantially:

- **The primary mechanism should be keeping the host awake/networked** (a sleep
  inhibitor held while an opted-in live RC session exists). If the host never goes
  unreachable, the ~10-min timeout never fires, the bridge never drops, and F5's
  close-vs-lost ambiguity never has to be resolved at runtime. This sidesteps the crux
  rather than betting on a fragile signal.
- **Lean on the daemon, don't rebuild it.** F2/F3 show Claude Code already respawns dead
  RC workers with RC re-armed. niwa's recovery layer, if any, should be thin: observe
  `bridgeSessionId` presence (F1) and, at most, invoke `claude respawn <id>` — not a
  bespoke supervisor.
- **Treat entry-gone as "developer is done" only after the live test (5) confirms** a
  TUI-close/archive actually removes the entry and a timeout exit does not. Until then,
  the design must not resurrect on entry-gone.
- **`selfWake`/cron (F4)** is a possible lightweight keep-warm lever but has a 7-day TTL
  and is undocumented for this use; treat as secondary.
