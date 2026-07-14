---
status: In Progress
problem: |
  Developers who dispatch remote-control sessions with `niwa dispatch` to run
  unattended lose reachability after a few hours of idle: the session's remote
  connection lapses and it can no longer be reached or steered from the Claude
  mobile app or claude.ai, even on an always-on, networked host. niwa offers no
  way to keep a session that the developer has not finished with reachable
  across that idle gap.
goals: |
  Give developers an opt-in way to keep a dispatched RC session reachable across
  long idle stretches, so a session left running is still reachable and steerable
  from a phone later, and have that keep-alive release on its own once the
  developer is done with the session. Non-participating dispatches are unchanged.
upstream: docs/briefs/BRIEF-niwa-session-keep-alive.md
motivating_context: |
  A recurring workflow: dispatch a session to work overnight, then check and
  steer it from a phone during the morning commute. An empirical spike
  (docs/spikes/SPIKE-niwa-session-keep-alive.md) confirmed the disconnect is an
  idle bridge-drop on a reachable host, that niwa cannot observe the drop from
  local state, and that the daemon does not recover it — which shapes these
  requirements toward keeping the connection from lapsing rather than detecting
  and repairing it.
---

## Status

In Progress

## Problem Statement

A developer can launch a background session with `niwa dispatch` and Claude Code
Remote enabled, then monitor and steer it from the Claude mobile app or claude.ai.
The intended use is to dispatch work that runs unattended — overnight, for example —
and pick it up from a phone later.

That use breaks after a few hours of idle. The session's remote connection lapses
and it stops being reachable, and this happens even when the host is an always-on
desktop that never sleeps and stays on the network. The affected user is the
developer who relied on the session still being reachable; the cost is a session
that was meant to carry through the idle gap but is unreachable by the time they
open their phone.

niwa cannot help today. It enables remote control by setting one Claude Code option
at launch and thereafter tracks the session only as present-or-gone; it holds no
live connection and takes no action when a session stops being reachable. There is
no way for a developer to tell niwa "keep this session reachable until I say I am
done with it," and niwa has no notion that an unclosed, idle session is one worth
keeping alive rather than letting lapse.

## Goals

- A developer can opt a dispatched RC session into being kept reachable across long
  idle stretches, and rely on it still being reachable and steerable from a phone
  after the idle gap.
- The keep-alive releases on its own once the developer is done with the session —
  closing it in the agents TUI, and (pending validation of the local effect) archiving
  it in claude.ai — without holding a session alive that the developer deliberately ended.
- A developer who does not opt in — and every existing dispatch — sees no change in
  behavior.

## User Stories

- As a developer running work overnight, I want to mark a dispatched RC session to
  be kept alive, so that when I check it from my phone in the morning it is still
  reachable and holds its context.
- As a developer who finished with a kept-alive session, I want closing it in the
  agents TUI (and, once the archive path is validated, archiving it in claude.ai) to
  stop the keep-alive, so that a session I deliberately ended is not held open or
  relaunched.
- As a developer who did not opt in, I want dispatch to behave exactly as it does
  today, so that the feature never changes runs I did not ask it to.
- As an operator managing a workspace, I want to set a workspace-wide default for
  whether dispatched RC sessions are kept alive, so that I do not have to pass a
  flag on every dispatch.

## Requirements

Functional:

- **R1.** `niwa dispatch` SHALL accept an explicit per-dispatch opt-in that marks
  the dispatched RC session as one to keep alive.
- **R2.** niwa SHALL support a workspace-level configuration default that opts
  dispatched RC sessions into keep-alive, overridable per dispatch in both directions.
  The default value SHALL be off.
- **R3.** Keep-alive SHALL apply only to sessions that have remote control enabled.
  Requesting keep-alive for a session without remote control SHALL be a no-op that
  surfaces a clear warning, not an error.
- **R4.** While a keep-alive session is live, niwa SHALL keep its remote connection
  from lapsing due to idle, so that the session remains reachable from the Claude
  mobile app and claude.ai for as long as it stays live (subject to the keep-alive
  vehicle's lifetime; renewal beyond that is Out of Scope).
- **R5.** Because a dropped remote connection is not observable from niwa's local
  state (the local remote-bridge identifier persists after the connection has
  actually dropped) and is not recovered by the Claude Code daemon, the feature
  SHALL keep the connection active proactively and SHALL NOT depend on detecting a
  dropped connection to do its job.
- **R6.** Keep-alive for a session SHALL stop once that session is no longer live by
  niwa's existing liveness signal (the Claude Code job entry is gone). Closing the
  session in the agents TUI (or `claude rm`) is expected to produce this; the exact
  local effect of a TUI close / claude.ai archive on the job entry is confirmed in the
  design's Phase 0 (it is inferred, not yet documented). A session the developer has
  ended SHALL NOT be kept alive.
- **R7.** The feature SHALL NOT recreate, relaunch, or resurrect a session whose
  entry has been removed. It keeps a live session live; it never revives a gone one.
- **R8.** A dispatch that is not opted in SHALL behave exactly as it does today, and
  keep-alive machinery SHALL be active only while at least one opted-in live session
  exists, imposing no effect otherwise.
- **R9.** Keep-alive SHALL NOT prevent niwa's reaper from reclaiming a genuinely
  ended ephemeral instance; it must not keep a session's liveness signal alive past
  the developer ending the session.
- **R10.** A developer SHALL be able to see which sessions are currently being kept
  alive.

Non-functional:

- **R11.** Keep-alive SHALL be lightweight: it SHALL NOT materially consume the
  session's model-token budget and SHALL NOT alter the session's working state or
  interfere with the work it is doing.

## Acceptance Criteria

- [ ] Dispatching an RC session with the per-dispatch opt-in marks it for keep-alive;
      dispatching without it does not (R1).
- [ ] With the workspace default set on, a dispatched RC session is kept alive
      without a per-dispatch flag; the per-dispatch override turns it off for a
      single dispatch, and with the default off a per-dispatch flag turns it on (R2).
- [ ] Requesting keep-alive for a non-RC session performs no keep-alive and prints a
      warning; the dispatch still succeeds (R3).
- [ ] An opted-in RC session and an equivalent non-opted RC session are both left idle
      for at least 12 hours (at or beyond the reported ~6–12h disconnect window; the spike
      confirmed one unreachable data point at ~12h). After the idle period, a steering
      command issued from claude.ai / the mobile app is accepted by the opted-in session,
      while the non-opted session returns unreachable (R4, R5).
- [ ] Closing an opted-in session in the agents TUI stops its keep-alive, and the
      session is not relaunched or resurrected afterward (verified by the job entry
      staying absent and no new session appearing) (R6, R7).
- [ ] For a dispatch without opt-in, the session's transcript, on-disk files, and
      job-entry metadata match a control dispatch on current `niwa dispatch` (a diff
      shows no difference beyond timestamps), and no keep-alive timer or process is
      present in niwa's state or the process list when no opted-in live session exists
      (R8).
- [ ] After a kept-alive session is ended, niwa's reaper reclaims its ephemeral
      instance on the next reap as it would today (R9).
- [ ] `niwa` reports which live sessions are currently kept alive, and the report
      matches the set that was opted in and is still live (R10).
- [ ] Over a 12-hour keep-alive window, keep-alive-attributable model-token usage stays
      at or below a small fixed ceiling (the design fixes the number at roughly 5,000 tokens
      for the window at the sub-hourly cadence, far below the volume a working turn consumes),
      measured via the session's token accounting; and the session's conversation transcript
      and on-disk files show no change from keep-alive beyond its own non-visible wake records
      (a diff against a control run shows no other difference) (R11).

## Out of Scope

- The mechanism that delivers keep-alive (how the connection is kept active, whether
  by riding Claude Code's own scheduled self-wake or otherwise, and any tuning of the
  keep-alive interval). That is the design's decision.
- Non-dispatch sessions: interactive foreground sessions a developer runs directly.
- Changing Claude Code Remote's own connection lifecycle or idle timeout.
- Detecting and repairing an already-dropped connection. The spike established a
  dropped bridge is not locally observable and the daemon does not recover it; this
  PRD requires preventing the lapse instead (R5), so a detect-and-reconnect capability
  is deliberately excluded.
- Keeping a session reachable across a host power-off or shutdown. Only the idle case
  on a running host is in scope.
- Renewing keep-alive across the lifetime of its underlying vehicle (for example,
  re-arming across a scheduled-self-wake time-to-live). Whether a single opt-in persists
  across such a boundary, and how, is the design's to decide.
- Keep-alive survival across a niwa or Claude Code daemon restart while a session is
  still live. Preserving keep-alive across a control-plane restart is not required here.

## Decisions and Trade-offs

These resolve the Open Questions the upstream BRIEF deferred to this PRD.

- **Opt-in surface: per-dispatch flag plus a `[global]` workspace default, default
  off.** Alternatives were flag-only (no workspace default) and config-only (no
  per-dispatch control). Chose both for parity with the existing
  `remote_control_on_dispatch` preference and so an operator can set a default while a
  developer keeps per-dispatch control. Default off keeps existing behavior unchanged.
- **"Still-live" signal is the Claude Code job entry's presence, not the remote-bridge
  identifier.** The spike proved the local bridge identifier is stale — it persists
  after the connection has actually dropped (a ~12h-idle session was confirmed
  unreachable while its local bridge id still showed present). Reading it would give
  false "still connected" readings, so the feature keys "still live" off the existing
  present-or-gone job-entry signal instead.
- **Prevent the lapse rather than detect-and-reconnect.** Because the drop is not
  locally observable and the daemon does not re-establish an idle-dropped bridge,
  detection-based recovery cannot be built reliably from local state. The feature
  keeps the connection from lapsing in the first place (R5).

## Known Limitations

- **claude.ai archive vs. TUI close.** The stop condition keys off the local job entry
  being removed (R6), which is what an agents-TUI close / `claude rm` produces. Whether
  archiving a session in the claude.ai web UI removes the local job entry the same way
  is unconfirmed; if it does not, keep-alive may continue until the entry is otherwise
  removed. A design-time throwaway-session test should confirm the archive path.
- **Idle threshold and duration bound.** The exact idle interval that triggers the
  drop is not precisely known (observed to occur within roughly 6–12 hours); the design
  must keep the connection active on a conservative interval. If the chosen vehicle is
  Claude Code's scheduled self-wake, its multi-day time-to-live bounds how long a single
  keep-alive persists before it must be renewed.
