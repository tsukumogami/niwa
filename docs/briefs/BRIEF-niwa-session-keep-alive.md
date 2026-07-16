---
schema: brief/v1
status: Accepted
problem: |
  Remote-control-enabled sessions launched with `niwa dispatch` lose their
  remote connection after a few hours of idle and become unreachable from
  the phone, even on an always-on, networked host. A session left running
  overnight is unreachable by the next morning.
outcome: |
  A developer can mark a dispatched RC session to be kept alive, leave it
  overnight, and still reach and steer it from the phone the next morning.
  The keep-alive releases once the developer closes or removes the session;
  archiving it in claude.ai alone does not release it.
motivating_context: |
  The recurring pattern: a developer dispatches a session to work overnight
  and only checks on it from a phone during the morning commute. The value
  of dispatch-plus-remote-control collapses if the session cannot survive
  the idle gap between "left it running" and "opened the phone."
---

## Status

Accepted

Framing for an optional keep-alive over `niwa dispatch` remote-control
sessions. Stops at the feature's problem, outcome, journeys, and scope
boundary. The downstream PRD owns the opt-in surface, the "still-live"
signal, and the acceptance criteria; the DESIGN owns the mechanism (a
heartbeat that keeps the session active so its connection never idles out
— see docs/spikes/SPIKE-niwa-session-keep-alive.md).

Edited in place after review + live testing: archiving in claude.ai was found NOT
to release keep-alive — it is a UI-only action that leaves the local session state
intact, so keep-alive keeps running until the session is closed/removed. The release
path is therefore closing or removing the session, not archiving; the archive
behavior is documented downstream as a known limitation.

## Problem Statement

`niwa dispatch` can launch a background session with Claude Code Remote
enabled, so a developer can monitor and steer it from the Claude mobile
app or the claude.ai web UI. The intended pattern is to dispatch work
that runs unattended overnight and pick it up from a phone the next
morning.

That pattern breaks on long idle stretches. After a few hours with no
activity, the session's remote connection drops and it stops being
reachable from the phone — and this happens even when the host is an
always-on desktop that never sleeps and stays on the network. By the time
the developer opens their phone on the commute, the session that was
supposed to still be working is no longer reachable. Whatever the exact
trigger, the felt result is the same: an idle session that was meant to
carry through the night does not.

niwa knows nothing about this today. It enables remote control by
flipping one Claude Code setting at launch and then tracks the session
only as present-or-gone; it holds no live connection and takes no action
when a session stops being reachable. The gap the developer feels is that
there is no way to tell niwa "this session matters overnight — keep it
reachable until I say I'm done with it." Whether "I'm done" is expressed
by closing the session in the agents TUI or by archiving it in claude.ai,
niwa has no notion that an unclosed session left idle is one it should be
keeping alive rather than letting lapse.

## User Outcome

A developer who dispatches an overnight RC session and opts it into
keep-alive finds that session still reachable and steerable from their
phone the next morning, instead of finding it dropped. The developer does
not have to babysit the session, disable sleep by hand, or re-dispatch
the work — the session they left running is the session they resume.

The keep-alive is theirs to end, not something that lingers. Once the
developer signals they are finished — by closing the session in the
agents TUI or archiving it in claude.ai — the keep-alive stops holding on
and the session is allowed to lapse like any other. A developer who never
opts in sees no change at all: dispatch behaves exactly as it does today.

## User Journeys

### Overnight session survives to the morning commute

A developer dispatches an RC session at night to grind through a long
task and opts it into keep-alive, then walks away. The trigger is the
session sitting idle for hours — long enough that its remote connection
would normally lapse. The outcome the developer reaches: at 8am, phone in
hand on the train, the session is still listed as reachable, still holds
its context, and accepts the next steer.

### Finishing with a session releases the hold

A developer decides a kept-alive session is done — the work landed, or
they want to stop it. The trigger is the developer closing or removing the
session in the Claude agents TUI. The outcome: niwa observes the session is
no longer one the developer wants held, stops keeping it alive, and does not
relaunch or resurrect it. A session the developer deliberately closed stays
ended. (Archiving in claude.ai alone does not release it — closing does.)

### A non-participating dispatch is untouched

A developer runs a normal `niwa dispatch` without opting into keep-alive,
or an operator who has not enabled the feature at all. The trigger is any
ordinary dispatch. The outcome: behavior is identical to today — nothing
holds the host awake on that session's behalf, no keep-alive machinery
runs, and the session lapses on idle exactly as it would now. Keep-alive
is a thing you ask for, never a default that changes existing runs.

## Scope Boundary

### In

- An explicit, opt-in way to mark a dispatched RC session as one niwa
  should keep reachable across long idle/unreachable windows.
- Keeping an opted-in, still-live RC dispatch session reachable through
  the overnight idle gap so it can be resumed from a phone in the morning.
- Releasing the keep-alive when the developer closes or removes the session
  in the agents TUI, so a deliberately-ended session is not held or relaunched.
  (Archiving in claude.ai alone does not release it — see the Status note.)
- Leaving every non-opted-in dispatch, and every session the feature is
  off for, behaving exactly as it does today.

### Out

- The mechanism that delivers reachability — a heartbeat that keeps the
  session active so its connection never lapses, versus detecting a
  dropped connection and re-establishing it. That is the DESIGN's
  decision, not the framing.
- Non-dispatch sessions: interactive foreground sessions a developer runs
  directly are outside this feature's boundary.
- Changing Claude Code Remote's own connection lifecycle or timeout. The
  feature works with that behavior as given, not against it.
- Keeping a host awake or running any keep-alive machinery when no live,
  opted-in session exists.
- The exact opt-in surface, the precise "still-live" signal, and the
  acceptance criteria — those are the downstream PRD's to settle.

## References

- `docs/briefs/BRIEF-remote-control-by-default.md` — prior framing for
  remote control over dispatched sessions.
- `docs/briefs/BRIEF-instance-dispatch.md` — the dispatch feature this
  keep-alive rides on.
- `docs/briefs/BRIEF-ephemeral-session-instances.md` — the session
  lifecycle (present-or-gone liveness, reaping) the keep-alive must
  cooperate with.
