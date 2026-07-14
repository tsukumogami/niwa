---
upstream: docs/prds/PRD-niwa-session-keep-alive.md
status: Proposed
problem: |
  Dispatched remote-control sessions lose reachability after a few hours of idle,
  and niwa has no way to keep an opted-in session's remote connection from lapsing.
  The disconnect is not observable from niwa's local state, so the fix must prevent
  the lapse rather than detect and repair it — within niwa's launch-time-only,
  daemon-free relationship to the sessions it dispatches.
decision: |
  Arm a token-light periodic self-wake on the dispatched session at launch (riding
  Claude Code's own scheduled self-wake / session-cron primitive), so the session
  pings often enough that its remote bridge never idles out. niwa turns this on via
  the existing dispatch settings-injection path when the session is opted in, records
  the opt-in on the durable session mapping, and does nothing at runtime; the wake is
  self-contained in the session and stops when the session's job entry is gone.
rationale: |
  Prevention is the only reliable option: the spike proved a dropped bridge is not
  locally observable and the daemon does not recover an idle drop, so detect-and-
  reconnect cannot be built. Riding the session's own self-wake keeps niwa daemon-free
  (no new per-session watcher), makes the heartbeat stop automatically when the session
  ends, and keeps the token cost to a bounded periodic no-op rather than real work.
---

# DESIGN: niwa session keep-alive

## Status

Proposed

## Context and Problem Statement

`niwa dispatch` launches a background `claude --bg` worker and, when remote control is
enabled, injects the `remoteControlAtStartup` setting at launch. After that, niwa's
relationship to the session is launch-time-only: it captures the session id once and
thereafter tracks the session solely by the presence or absence of the Claude Code job
entry (`~/.claude/jobs/<id>/state.json`). niwa runs no per-session daemon, holds no live
connection, and takes no scheduled action — its lifecycle is pull-based (reaping runs on
demand and at the next `niwa create`).

The technical problem: an opted-in session's remote bridge idles out after several hours
even on an always-on, networked host, and niwa must keep that from happening without
adopting a live supervisor. An accompanying empirical spike
(`docs/spikes/SPIKE-niwa-session-keep-alive.md`) established three load-bearing facts:

1. A dropped bridge is **not observable locally** — the `bridgeSessionId` field in the
   job state persists after the connection has actually dropped (a ~12h-idle session was
   confirmed unreachable while its local bridge id still showed present).
2. The Claude Code **daemon does not recover** an idle-dropped bridge; it respawns dead
   *workers*, but an idle drop leaves the worker alive with a stale bridge, so no respawn
   fires.
3. Claude Code sessions carry a **self-wake / session-cron primitive** (`selfWake: true`,
   an in-flight `session_cron` was observed) — a session can schedule its own wake-ups.

Together these mean the fix must (a) prevent the lapse rather than detect it, and (b) do
so without niwa growing a per-session watcher. The design question is how niwa arms a
prevention mechanism at launch and how that mechanism keeps the bridge warm cheaply and
stops cleanly.

## Decision Drivers

- **Prevent, don't detect (PRD R4/R5).** Detection from local state is impossible; the
  mechanism must keep the connection active proactively.
- **Stay daemon-free.** niwa is deliberately pull-based with no per-session process; the
  design should not introduce niwa's first long-lived per-session supervisor if it can
  be avoided.
- **Token-light and side-effect-free (PRD R11).** The heartbeat must not do the session's
  work, consume a meaningful token budget, or alter conversation/on-disk state.
- **Stops on session end, cooperates with the reaper (PRD R6/R7/R9).** Keep-alive must
  end when the job entry is gone and must never resurrect a gone session or block reaping.
- **Opt-in, default off, zero effect on non-participants (PRD R1/R2/R8).** The arming must
  ride the existing opt-in surface and change nothing for sessions that do not opt in.
- **Fit the existing dispatch injection model.** niwa can only reliably act at launch
  (settings/argv/hook injection); the arming should use that seam, not a new runtime path.

## Considered Options

### Decision A — the prevention vehicle

**A1. An in-session periodic self-wake, armed at dispatch (chosen).** niwa arms the
dispatched session to wake itself on a periodic tick (riding Claude Code's session-scoped
`/loop` / cron primitive), so the session stays active often enough that its remote bridge
never idles out. The mechanism lives entirely inside the session: niwa arms it at launch
and does nothing afterward. It stops on its own when the session ends (the cron is
session-scoped and dies with the job entry).

**A2. A niwa-side external heartbeat supervisor.** niwa runs a periodic process that pokes
each opted-in session from outside (attach/respawn/poll). Rejected: it reintroduces niwa's
first long-lived per-session runtime component, directly against the daemon-free driver;
and there is no external "keep the bridge warm" API — an outside poke is not known to reset
whatever server-side idle the drop is caused by, so it is both heavier and less certain than
A1.

**A3. Detect the drop and reconnect.** Watch for a dropped bridge and re-establish it.
Rejected outright by the spike: a dropped bridge is not observable from local state (the
`bridgeSessionId` field is stale) and the daemon does not recover it, so there is no
reliable local signal to trigger on. This is why the PRD requires prevention (R5).

### Decision B — how the self-wake is armed at launch

The spike/research found no CLI flag, settings key, or documented hook that schedules a
cron at `claude --bg` startup; the two available launch-time seams are:

**B1. A launch-time arming seam that does not alter the task prompt (preferred).** niwa
arms the wake through a channel separate from the session's work prompt — a materialized
`SessionStart`-style hook or a settings-carried instruction — so the session's first turn
and working state are untouched (better for R11). Feasibility of arming a recurring wake
this way is the open item the validation spike must confirm.

**B2. Prompt augmentation (fallback).** niwa prepends a short instruction to the dispatched
prompt telling the agent to create the heartbeat loop. dispatch already fully controls the
final prompt argv (`dispatch.go:124` → `buildClaudeBgArgs`), so this is mechanically
trivial and argv-safe. Rejected as the primary because it pollutes the session's first turn
and depends on the agent reliably setting up the loop — a mild but real R11 tension. Kept as
the fallback if B1 proves infeasible.

### Decision C — the opt-in surface plumbing

**C1. Mirror `remote_control_on_dispatch` (chosen).** A `--keep-alive` dispatch flag plus a
`[global] keep_alive_on_dispatch` config key, resolved flag > downstream > host-default,
default off, recorded on the durable session mapping. This reuses an established, tested
pattern in the codebase (see Solution Architecture). The alternative surfaces (flag-only,
config-only) were already settled in the PRD's Decisions; C1 implements that decision by
copying the nearest precedent 1:1.

## Decision Outcome

Arm an **in-session hourly self-wake** on opted-in dispatched RC sessions (A1), armed at
launch through the **least-invasive available seam** (B1 preferred, B2 fallback), with the
opt-in plumbed by **mirroring `remote_control_on_dispatch`** (C1). niwa adds no runtime
component and no reaper coupling; the wake stops automatically when the session's job entry
is gone.

This outcome is **gated on an efficacy validation** (Implementation Approach, Phase 0): it
is not yet proven that a local self-wake resets the server-side idle that causes the 6–12h
drop. If validation shows a self-wake does not keep the bridge reachable, the feature is not
buildable with current Claude Code primitives and the honest outcome is to stop and report
that — the design does not pretend otherwise. Every other part of the design (plumbing,
stop-on-gone, observability) is low-risk and well-precedented; the single load-bearing
unknown is efficacy, so the design front-loads proving it.

**Cadence rationale.** The observed drop is 6–12h, so an hourly wake sits comfortably below
the floor with wide margin. Over a 12h idle window that is ~12 minimal wakes; at a per-wake
cost on the order of 100–200 tokens (to be measured in Phase 0), that is roughly 1–2 thousand
tokens per 12h window — orders of magnitude below a working session. The design fixes the
PRD-deferred R11 ceiling at **≤ ~2,000 keep-alive tokens per 12h window** (superseding the
PRD's illustrative "hundreds"); the interval is a fixed hourly constant, not configurable. A
short 5–10 min interval is unnecessary here (that guards a ~10-min *network* timeout, which
does not apply to an always-on host) and would multiply token cost.

## Solution Architecture

Five components, four of which are near-copies of the `remote_control_on_dispatch` feature.

1. **Opt-in resolution.** Add a `--keep-alive` flag (registered in `dispatch.go` `init()`,
   as a tri-state via `cmd.Flags().Changed` so "explicit off" differs from "unset") and a
   `GlobalSettings.KeepAliveOnDispatch *bool` config key (`registry.go`, tag
   `keep_alive_on_dispatch,omitempty`). A new `resolveDispatchKeepAlive` (a
   `dispatch_keepalive.go` mirroring `dispatch_remotecontrol.go:37-50`) resolves
   flag > downstream > host-default, default off — the flag-fill half borrowed from the
   `--model` precedent (`dispatch.go:222-225`), the downstream/host half from the RC
   resolver. Keep-alive is meaningful only when RC is on; requesting it for a non-RC session
   resolves to a no-op plus a warning (R3).

2. **Arming at launch.** When resolution is on, niwa arms the self-wake at the dispatch
   launch seam (`dispatch.go:248-261`, alongside the RC `--settings` append). B1: emit the
   arming via a hook/settings channel; B2 fallback: augment the prompt argv before
   `dispatchLaunch` (`dispatch.go:263`). The arming payload is a fixed, trivial heartbeat
   instruction — no untrusted input, a single argv element (preserving the D8 no-shell-
   interpolation guard).

3. **The in-session heartbeat.** The armed session runs an hourly self-wake whose action is
   a near-no-op (a minimal fixed wake that cycles a turn without doing the session's work or
   touching files). It rides Claude Code's session-scoped cron, so it persists across
   idle/resume for the cron's lifetime and requires nothing from niwa at runtime.

4. **Durable opt-in record.** Add `KeepAlive bool json:"keep_alive,omitempty"` to
   `SessionMapping` (`session_map.go:49-66`), set in the mapping literal at
   `dispatch.go:285-293`. `omitempty` keeps legacy mappings byte-identical (same discipline
   as `Origin`). This record is informational — it powers observability, not reaping.

5. **Observability (R10).** `niwa` reports which live sessions are kept alive by joining
   `SessionMapping.KeepAlive == true` with the existing liveness signal (`sessionLive`), so
   the report shows opted-in sessions that are still live.

**Stop-on-gone and reaper cooperation.** Nothing new is wired into the reaper.
`sessionLive`/`instanceHasLiveJob` (`job_state.go`) key purely on the Claude Code job entry;
the self-wake is session-scoped and dies when that entry is removed (TUI close / `claude rm`)
— so keep-alive stops automatically (R6) and cannot resurrect a gone session (R7). The
reaper does **not** read `SessionMapping.KeepAlive`; keep-alive must never suppress reaping,
which preserves R9. Because the mechanism is purely in-session, this property holds by
construction.

## Implementation Approach

- **Phase 0 — Efficacy validation (mandatory gate).** On a throwaway RC dispatch session,
  arm the hourly self-wake and confirm the session is still reachable from claude.ai past
  the 12h mark (where an un-armed session is confirmed unreachable). In the same run,
  determine whether B1 (hook/settings arming) works or the B2 prompt fallback is needed,
  measure the per-wake token cost (to confirm the ≤ ~2,000 tokens / 12h ceiling), confirm the
  session-scoped wake actually tears down when the job entry is removed (`claude rm` /
  TUI close), and confirm what a claude.ai **archive** does to the local job entry (the PRD's
  open Known Limitation). If the self-wake does not keep the bridge reachable, STOP: the
  feature is infeasible with current primitives; record that and do not ship the plumbing.
- **Phase 1 — Opt-in plumbing.** Add the flag, the config key, the resolver, and the
  `SessionMapping.KeepAlive` field, mirroring the `remote_control_on_dispatch` files and
  their tests 1:1.
- **Phase 2 — Arming wiring.** Wire the resolved opt-in to the arming seam chosen in Phase 0
  (B1 or B2) at the dispatch launch point.
- **Phase 3 — Observability.** Add the `niwa` report of kept-alive live sessions (R10).
- **Phase 4 — Tests.** Unit tests mirroring the RC suite (resolver precedence, argv/prompt
  injection, mapping round-trip, non-RC no-op warning) plus one end-to-end reachability test
  that reproduces the Phase 0 validation as an automated check where feasible.

## Security Considerations

- **Injection safety.** The arming payload (settings/hook or prompt text) is a fixed,
  niwa-authored constant with no untrusted input, passed as a single argv element — the
  existing D8 no-shell-interpolation guard is preserved. There is no new path for
  user-controlled data to reach a shell.
- **Heartbeat action scope.** The self-wake cycles a turn under the session's own permission
  mode. To avoid the heartbeat doing anything with tools, its action is a fixed trivial
  no-op prompt; it must never carry dynamic or externally-influenced content. This keeps the
  wake from being a lever to run unintended tool calls.
- **No new secrets or auth surface.** RC already requires a first-party claude.ai login;
  keep-alive introduces no new credential, token, or network endpoint. It only changes how
  often an already-authorized session wakes.
- **Opt-in, default off, no non-participant effect.** A non-opted dispatch is byte-identical
  to today (R8); the feature adds no attack surface to sessions that do not opt in.
- **No reaping subversion.** Keep-alive is not coupled to the reaper and cannot hold an
  ended session alive or resurrect one (R7/R9); it cannot be used to keep an instance
  un-reclaimed against the user's intent.
- **Resource-exhaustion consideration.** The wake interval is a fixed hourly constant, not
  user-configurable — the opt-in is boolean (mirroring `remote_control_on_dispatch`), so
  there is no surface through which a token-draining short interval could be set. The
  per-session cost is bounded by construction (~24 minimal wakes/day).
- **Idempotent arming.** niwa arms exactly one session-scoped wake per opted-in session; the
  arming is idempotent and does not self-re-arm or schedule additional crons, so there is no
  amplification path from the arming seam.
- **Archive must stop the wake.** The Phase 0 validation must confirm that archiving a
  session in claude.ai ends the local job entry (or otherwise stops the wake); if archive
  leaves the entry present, keep-alive would keep waking a session the user considers done.
  Confirming this closes the "wakes a user-dead session" gap and is a gate on shipping
  (see the archive Known Limitation carried from the PRD).

## Consequences

**Positive.**
- Solves overnight reachability within niwa's daemon-free model — no new per-session runtime.
- Four of five components are near-copies of a tested, existing feature, so plumbing risk is
  low.
- Stops cleanly and automatically on session end; requires zero reaper changes.

**Negative / risks.**
- **Efficacy is unproven** until the Phase 0 validation; the whole feature is contingent on a
  local self-wake actually resetting the server-side idle. This is the one real risk and is
  deliberately gated first.
- If B1 arming proves infeasible, the B2 prompt fallback mildly pollutes the session's first
  turn (an R11 tension, bounded to one small fixed instruction).
- **Bounded duration.** Claude Code's cron carries a ~7-day TTL and niwa arms only at launch
  with no runtime component to renew it, so a single opt-in keeps a session alive for at most
  ~7 days before the wake lapses. Acceptable for the overnight/commute use case; documented,
  not silently assumed.
- Non-zero token cost (~1–2 thousand tokens per 12h idle window per session at hourly),
  immaterial against a working session but not literally zero.

**Mitigations.** The Phase 0 gate proves or kills efficacy before any plumbing ships; the
fixed, non-configurable hourly cadence bounds token cost by construction; preferring B1 keeps
the session prompt clean; the 7-day bound is documented as a Known Limitation carried from the
PRD.
