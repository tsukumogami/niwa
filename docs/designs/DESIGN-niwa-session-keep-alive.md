---
upstream: docs/prds/PRD-niwa-session-keep-alive.md
status: Accepted
problem: |
  Dispatched remote-control sessions lose reachability after a few hours of idle,
  and niwa has no way to keep an opted-in session's remote connection from lapsing.
  The disconnect is not observable from niwa's local state, so the fix must prevent
  the lapse rather than detect and repair it — within niwa's launch-time-only,
  daemon-free relationship to the sessions it dispatches.
decision: |
  Arm a periodic self-wake on the dispatched session at launch (riding Claude Code's own
  scheduled self-wake / session-cron primitive), delivered as a non-visible, context-isolated
  wake so the session stays active enough that its remote bridge never idles out without
  cluttering the conversation or eating its context. niwa turns this on via the existing
  dispatch injection path when the session is opted in, records the opt-in on the durable
  session mapping, and does nothing at runtime; the wake is self-contained in the session and
  stops when the session's job entry is gone.
rationale: |
  Prevention is the only reliable option: the spike proved a dropped bridge is not
  locally observable and the daemon does not recover an idle drop, so detect-and-
  reconnect cannot be built. Riding the session's own self-wake keeps niwa daemon-free
  (no new per-session watcher) and makes the heartbeat stop automatically when the session
  ends; delivering it as a non-visible, context-isolated wake keeps the cost to context
  footprint (not tokens) and holds that footprint near zero, since the session is kept alive
  precisely so its context stays usable for the developer to resume.
---

# DESIGN: niwa session keep-alive

## Status

Accepted

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
- **Context footprint is the real cost, not tokens (PRD R11).** Token billing for the
  heartbeat is immaterial. The load-bearing constraint is that the keep-alive must **not
  accumulate turns in the session's main context window** — the session is kept alive
  precisely so the developer can resume real work on it, so a heartbeat that fills its
  context (or forces an overnight compaction) defeats the purpose. The wake must be delivered
  so it neither clutters the visible conversation nor eats the usable context.
- **Side-effect-free (PRD R11).** The heartbeat must not do the session's work or alter the
  conversation's on-disk / working state.
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

There is **no programmatic seam** to arm a recurring self-wake — confirmed from docs *and* by
inspecting the host. No CLI flag or settings key schedules a cron at startup; Claude Code hooks
"can't trigger `/` commands or tool calls" (a `SessionStart` hook can only inject *text*); and
there is **no writable cron/schedule file** niwa could seed — the schedule lives in the running
process (replayed from the transcript), created only by the in-session scheduler tool. So the
scheduler call is **necessarily agent-mediated**: niwa injects an instruction and the agent
itself calls `/loop` / `CronCreate` in its first turn. niwa's deterministic contribution is the
instruction (which it fully controls); the scheduler call is the agent's.

This is far less fragile than it sounds, and is **demonstrated**: the very session that
produced this design was `niwa dispatch`-launched, RC-enabled, and stayed alive and reachable
for ~1.5 days by exactly this agent-armed self-wake, arming and re-arming it dozens of times
without failure (see the spike's Existence Proof). B1/B2 are therefore two *channels* for the
same proven nudge — not a risky unknown.

**B1. SessionStart `additionalContext` nudge (preferred channel).** niwa already materializes
a workspace-root `SessionStart` hook (`niwa instance from-hook`) that injects
`additionalContext`. Keep-alive can add its self-arm instruction there — a channel *separate
from the developer's task prompt*, so the task prompt is untouched. Still a nudge (the agent
must act on it), but cleaner than polluting the prompt.

**B2. Task-prompt augmentation (fallback channel).** niwa prepends the self-arm instruction
to the dispatched prompt itself; dispatch fully controls the final prompt argv
(`dispatch.go:124` → `buildClaudeBgArgs`, a single argv element, so it is argv-safe under
D8). Rejected as primary because it pollutes the session's first turn. Used only if the
SessionStart channel proves unreliable in Phase 0.

Neither is a settings key — see Decision B-note below; keep-alive does **not** add a
`--settings` flag.

**B-note — no second `--settings`.** RC is injected as a discrete `--settings <json>` argv
pair and niwa never merges settings JSON. A second `--settings` for keep-alive would be
passed verbatim alongside RC's, and the CLI's repeated-`--settings` behavior is undocumented
(likely last-wins), so it could clobber `remoteControlAtStartup` and break the very bridge
keep-alive protects. Since arming is a text nudge (B1/B2), not a setting, keep-alive avoids
the `--settings` channel entirely and this collision does not arise.

### Decision C — the opt-in surface plumbing

**C1. Follow `remote_control_on_dispatch` (chosen), plus a new per-dispatch flag layer.** A
`--keep-alive` dispatch flag plus a `[global] keep_alive_on_dispatch *bool` config key,
resolved flag > downstream > host-default, default off, recorded on the durable session
mapping. The config key + resolver + argv wiring copy the RC feature closely; the config
field mirrors `RemoteControlOnDispatch *bool` (a tri-state pointer, nil = today's behavior)
exactly. **One part is genuinely new, not a copy:** RC has *no* per-dispatch flag, and the
librarian confirmed the codebase has no bool tri-state flag pattern to borrow (`--model`
uses a plain empty-string check, and `cmd.Flags().Changed` is used nowhere). So the
per-dispatch `--keep-alive` override — which R2 requires to work in both directions — needs
a new flag mechanism (a `*bool` flag or a `Flags().Changed` check) that /plan must specify as
net-new work, not "mirror an existing flag." The alternative surfaces (flag-only, config-only)
were settled in the PRD's Decisions.

### Decision D — the wake's context footprint

The keep-alive exists to preserve a session for the developer to resume; a heartbeat that
consumes that session's context works against its own goal. How the wake is delivered decides
its context cost.

**D1. A main-thread `/loop` (rejected).** Each wake is a normal turn appended to the
session's own conversation. Over a night these accumulate in the context window and force
compaction — clutter in exactly the context we are trying to keep usable. Rejected.

**D2. A non-visible, context-isolated wake (chosen).** Deliver the wake so its work stays out
of the main conversation: as a background/meta turn and/or delegated to a throwaway isolated
sub-task, so only a minimal trace — or nothing — reaches the session's context. This is a
real, available shape, grounded in observed Claude Code behavior: background self-wake turns
are recorded as `isMeta: true` (hidden from the visible chat), and sub-agent work lives in a
separate transcript so the main thread stays `isSidechain: false` and un-bloated. (This very
design session's own scheduled wake-ups are all `isMeta` and its research sub-agents left no
turns in the main transcript — direct evidence the isolated/meta shape exists and works.)

The remaining unknown is the **floor**: whether a context-isolated/meta wake still keeps the
bridge warm. If yes, context cost is near-zero; if keeping the bridge warm turns out to
require real main-thread work, D2's sub-task isolation still keeps it *minimal* rather than
zero. Which shape is lightest-that-still-works is a Phase 0 measurement.

## Decision Outcome

Arm an **in-session sub-hourly self-wake** on opted-in dispatched RC sessions (A1), armed at
launch through the **SessionStart `additionalContext` nudge** (B1, prompt-augmentation B2 as
fallback), delivered as a **non-visible, context-isolated wake** (D2) so it does not clutter
the session's conversation or eat its context, with the opt-in plumbed by **following
`remote_control_on_dispatch` plus a new flag layer** (C1). niwa adds no runtime component and
no reaper coupling; the wake stops automatically when the session's job entry is gone.

**The core mechanism is demonstrated, not hypothetical.** The session that produced this
design was `niwa dispatch`-launched with RC and stayed reachable for ~1.5 days on exactly this
agent-armed self-wake (spike Existence Proof). That live run demonstrates the two behaviors
that were the feature's biggest unknowns:

1. **Efficacy** — a self-wake kept the session reachable across idle (demonstrated ~1.5 days).
2. **Process survival** — the sub-/~hourly wakes kept the process from the ~1h supervisor
   stop (demonstrated). The interval must stay under ~1h; that constraint is real but met.

The one residual that remained — whether a **non-visible / no-op** wake (D2), not the
existence proof's visible working turns, keeps the bridge warm — has now been **validated
directly** (spike, No-op wake validation): a throwaway session armed with a no-op non-visible
wake stayed reachable from claude.ai across ~18h idle while an un-armed control died. So the D2
wake shape works at near-zero context cost; no visible-wake fallback is needed. The plumbing,
stop-on-gone, and observability are low-risk and well-precedented.

**One validated limitation shapes the stop signal:** testing confirmed that **archiving a
session in claude.ai does not stop keep-alive** — it is a UI-only action that does not remove
the local jobs entry, so the cron keeps firing and `niwa reap` still sees the session as live.
Only closing/removing the session (agents-TUI close / `claude rm`), which removes the jobs
entry, stops keep-alive. This is documented as a limitation (see Consequences); the 7-day cron
TTL backstops an archived-but-not-closed session.

**Cadence rationale.** Two constraints set the interval. The idle-drop floor is 6–12h, but
the **~1h supervisor process-stop is the tighter bound**: the wake must fire within that hour
or the process is paused and nothing fires. So the interval is a fixed constant **well under
one hour** (e.g. ~30 minutes), giving margin against the ~1h stop plus the cron's ±30-min
jitter — Phase 0 fixes the exact value against the measured stop timing. Over a 12h window
that is ~24 minimal wakes; at ~100–200 tokens each that is roughly 2–5 thousand tokens per
12h window — still orders of magnitude below a working session. The design fixes the R11
ceiling at **≤ ~5,000 keep-alive tokens per 12h window**; the interval is a fixed constant,
not user-configurable. (Token cost is immaterial regardless — per the PRD, context footprint,
not tokens, is the tracked cost; see Decision D.)

## Solution Architecture

Five components. The config/resolver/mapping plumbing closely follows the
`remote_control_on_dispatch` feature; the per-dispatch flag and the arming nudge are net-new.

1. **Opt-in resolution.** Add a `GlobalSettings.KeepAliveOnDispatch *bool` config key
   (`registry.go`, tag `keep_alive_on_dispatch,omitempty`), mirroring `RemoteControlOnDispatch`
   exactly, and a `resolveDispatchKeepAlive` resolver (`dispatch_keepalive.go` following
   `dispatch_remotecontrol.go:37-50`) for the downstream > host-default half. **New work:** a
   `--keep-alive` per-dispatch flag registered in `dispatch.go` `init()` — since R2 needs
   both-direction override, it must distinguish "unset" from "explicit false" (a `*bool` flag
   or a `Flags().Changed` check), a pattern not present in the codebase today (RC has no flag;
   `--model` uses a string-empty check). The flag layers on top of the RC-style resolver as
   flag > downstream > host-default, default off. Keep-alive is meaningful only when RC is on;
   requesting it for a non-RC session is a no-op plus a warning (R3).

2. **Arming at launch (a nudge, not a setting).** When resolution is on, niwa injects a fixed
   self-arm instruction via the SessionStart `additionalContext` channel (B1) — the workspace
   hook niwa already materializes — or, as fallback, prepends it to the task prompt argv
   before `dispatchLaunch` (`dispatch.go:263`, B2). It does **not** append a `--settings` flag
   (see Decision B-note). The instruction is a fixed niwa-authored constant (no untrusted
   input; the B2 prompt path is a single argv element, preserving the D8 no-shell-
   interpolation guard). Whether the nudge reliably arms a surviving recurring wake is a
   Phase 0 output.

3. **The in-session heartbeat.** The armed session runs a **sub-hourly** self-wake (interval
   fixed under the ~1h supervisor stop; see Cadence rationale) whose action is a near-no-op,
   delivered in the **context-isolated / non-visible shape** from Decision D2 — a
   background/meta wake and/or a throwaway isolated sub-task — so it neither appears in the
   visible conversation nor accumulates turns in the session's main context. It rides Claude
   Code's session-scoped cron (7-day TTL, fixed from creation, no renewal). The exact
   lightest-shape-that-keeps-the-bridge-warm-and-survives-the-supervisor-stop is fixed by the
   Phase 0 measurement.

4. **Durable opt-in record.** Add `KeepAlive bool json:"keep_alive,omitempty"` to
   `SessionMapping` (`session_map.go:49-66`), set in the mapping literal at
   `dispatch.go:285-293`. `omitempty` keeps legacy mappings byte-identical (same discipline
   as `Origin`). This record is informational — it powers observability, not reaping.

5. **Observability (R10).** Extend `niwa list` (which already emits per-instance
   `{name, path, ephemeral}` from the session mapping, `list.go`) to surface the
   `SessionMapping.KeepAlive` flag, joined with the existing liveness signal (`sessionLive`),
   so the report shows opted-in sessions that are still live.

**Stop-on-gone and reaper cooperation.** Nothing new is wired into the reaper.
`sessionLive`/`instanceHasLiveJob` (`job_state.go`) key purely on the Claude Code job entry,
independent of `bridgeSessionId` (librarian-confirmed), and the reaper reads no timestamp, so
an hourly wake cannot defer reaping — R9 holds by construction. The cron is librarian-confirmed
**session-scoped and in-process** (no host-level task), so it cannot fire against a gone
session — R7 holds. The wake stops when the session's job entry is gone; `claude rm` and a
TUI close roster-remove the session, which tears down its in-process cron (Phase 0(a)
confirms `state.json` is physically removed, not just de-listed). **A claude.ai archive is NOT
a stop signal** — testing confirmed archiving is UI-only and does not remove the local jobs
entry, so keep-alive keeps firing and the reaper still sees the session as live (see
Consequences). The reaper does **not** read `SessionMapping.KeepAlive`; keep-alive must never
suppress reaping.

## Implementation Approach

- **Phase 0 — Validation.** The core mechanism and its key residuals were validated ahead of
  implementation (see the spike). **Already confirmed:** the existence-proof session (agent-armed
  self-wake kept a dispatched RC session alive ~1.5 days, past the ~1h supervisor stop); the
  **non-visible / no-op wake** (a throwaway session on a no-op wake stayed reachable from
  claude.ai ~18h while an un-armed control died — so the D2 shape works at near-zero context
  cost, no visible-wake fallback needed); and the **archive behavior** (archiving in claude.ai
  does not stop keep-alive — see Consequences). **Remaining confirmations for the
  implementation** (refinements, not feasibility gates):
  - **(a) Close/remove teardown — confirmed.** Tested: `claude rm` removes the `state.json`
    entry (whereas `claude stop` leaves it), and `niwa reap` then reclaims the instance
    ("Reaped 2 orphaned ephemeral instance(s)"). R6 stop-on-entry-gone and R9 reaper cooperation
    both hold for the close/remove path.
  - **(b) Finished/`done` sessions.** Reconfirm the no-op wake holds for a session that went
    `done`/idle overnight (the validation covered this, but pin the shipped cadence to it).
  - **(c) Arming-channel choice.** Fix which nudge channel niwa uses — B1 SessionStart
    `additionalContext` for a `--bg` worker if it fires reliably, else B2 prompt. Agent-arming
    itself is proven (existence proof + no-op test both armed via the agent).
- **Phase 1 — Opt-in plumbing.** Add the config key, the resolver, and the
  `SessionMapping.KeepAlive` field following the `remote_control_on_dispatch` files/tests, plus
  the net-new `--keep-alive` tri-state flag (no existing pattern to copy — see Solution
  Architecture #1).
- **Phase 2 — Arming wiring.** Wire the resolved opt-in to the arming nudge channel fixed in
  Phase 0 (B1 SessionStart `additionalContext`, or B2 prompt fallback) at the dispatch launch
  point.
- **Phase 3 — Observability.** Extend `niwa list` to surface kept-alive live sessions (R10).
- **Phase 4 — Tests.** Unit tests following the RC suite (resolver precedence, flag override
  both directions, arming-injection, mapping round-trip, non-RC no-op warning) plus one
  end-to-end reachability test that reproduces the Phase 0 validation as an automated check
  where feasible.

## Security Considerations

- **Injection safety.** The arming payload (SessionStart `additionalContext`, or the B2
  prompt text — never a `--settings` flag) is a fixed, niwa-authored constant with no
  untrusted input; the B2 prompt path is a single argv element, preserving the existing D8
  no-shell-interpolation guard. There is no new path for user-controlled data to reach a
  shell.
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
- **Resource-exhaustion consideration.** The wake interval is a fixed sub-hourly constant, not
  user-configurable — the opt-in is boolean, so there is no surface through which a
  token-draining short interval could be set. The per-session cost is bounded by construction
  (~48 minimal wakes/day; token cost immaterial, context cost minimized by D2).
- **Idempotent arming.** The arming instruction directs the agent to create exactly one
  session-scoped cron and to no-op if one is already present, so a re-delivered nudge cannot
  stack crons. Because arming is a nudge (not deterministic), Phase 0 must confirm the agent
  honors the once-only instruction under both channels; under B1 the SessionStart injection
  fires once per session start, bounding delivery.
- **Archive does not stop the wake (confirmed limitation).** Testing confirmed archiving a
  session in claude.ai is UI-only and leaves the local jobs entry intact, so keep-alive keeps
  waking a session the user considers done, and its instance stays un-reaped, until the session
  is closed/removed or the 7-day cron TTL lapses. This is a bounded resource leak, not a
  security escalation (the wake still runs only under the session's own already-authorized
  identity and its fixed no-op action), but it is documented so users know to *close*, not just
  archive, to release keep-alive (see Consequences / Known behavior).

## Consequences

**Positive.**
- Solves overnight reachability within niwa's daemon-free model — no new per-session runtime.
- The config/resolver/mapping plumbing closely follows a tested, existing feature, so that
  part is low-risk.
- Stops cleanly and automatically on session end; requires zero reaper changes (R9 by
  construction, librarian-confirmed).

**Negative / risks.**
- **The core mechanism is validated** — the existence-proof session (~1.5 days) plus a direct
  A/B test where a non-visible **no-op** wake kept a dispatched session reachable from claude.ai
  ~18h while an un-armed control died. Efficacy, process-survival, and the near-zero-context
  no-op shape are all confirmed; no visible-wake fallback is needed. This is the strongest kind
  of de-risking — the feature works, measured end to end.
- **Archive does not release keep-alive (confirmed limitation).** Archiving a session in
  claude.ai is UI-only: it does not remove the local jobs entry, so keep-alive keeps waking the
  session and `niwa reap` keeps its instance, until the session is *closed/removed* or the 7-day
  cron TTL lapses. Users must **close (not just archive)** to release keep-alive promptly; the
  7-day TTL is the backstop. niwa cannot detect archive locally (it is server-side and the local
  bridge field is stale), so this is documented behavior, not a bug to fix in v1.
- **Arming is agent-mediated (no programmatic seam).** There is no cron-arming flag/hook and no
  writable schedule file (investigated); the agent must self-arm from an injected instruction.
  This is proven reliable (the existence-proof session armed/re-armed dozens of times), but it
  is not a deterministic API call, and the B2 channel mildly pollutes the first turn.
- **Finished/`done` sessions** — the common morning case is a session that went idle overnight,
  subject to the ~1h process-stop; the existence proof survived this, but Phase 0(3) reconfirms
  it for the no-op shape. Note the "no effect except a keep-alive marker" property (R8/R11)
  holds strictly for *non-opted* sessions; an opted-in session's entry-removal timing differs
  from a control by design.
- **Bounded duration.** Claude Code's cron carries a ~7-day TTL and niwa arms only at launch
  with no runtime component to renew it, so a single opt-in keeps a session alive for at most
  ~7 days before the wake lapses. Acceptable for the overnight/commute use case; documented,
  not silently assumed.
- **Context footprint depends on the Phase 0 shape.** Token billing is immaterial (~2–5k
  tokens per 12h window at the sub-hourly cadence). The tracked cost is main-context growth:
  the non-visible/isolated wake (D2) targets near-zero, but if Phase 0 finds the bridge only
  stays warm with real main-thread work, the footprint is minimal rather than zero — that
  measured floor is the honest number the feature ships with.

**Mitigations.** The Phase 0 gate proves or kills the two platform unknowns — and fixes the
lightest wake shape by measuring context growth — before any plumbing ships; the fixed,
non-configurable sub-hourly cadence bounds cost by construction; the non-visible/isolated wake
(D2) keeps the session's conversation and context clean; the SessionStart nudge keeps the task
prompt clean; the
7-day bound is documented as a Known Limitation carried from the PRD.
