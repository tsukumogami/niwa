---
schema: brief/v1
status: Accepted
problem: |
  A dispatch handle is launch-and-observe only. Neither a headless
  coordinator nor an operator can hand a running worker a follow-up task
  through it; the only available maneuver forks a new session, orphaning
  the original and letting two sessions own one instance.
outcome: |
  Whoever holds a dispatch handle re-tasks the worker with one command.
  The worker continues with its accumulated context in the same
  instance, exactly one session owns that instance afterward, and
  nothing is left orphaned for the reaper or the operator to chase.
motivating_context: |
  A coordinator agent dispatched several niwa workers, then needed to
  re-task one. Its best option, `claude --resume <session-id> --bg`,
  copied the conversation into a new session and left the original
  idle. The same platform behavior independently degraded niwa watch's
  review continuation to once-per-session (#211). Spike work confirmed
  the gap is real and platform-side: Claude Code offers no supported
  headless way to push a turn into a live background session.
---

# BRIEF: Dispatch handle retask

## Status

Accepted

Framing produced by a /scope chain grounded in a completed exploration
with live spikes against Claude Code 2.1.214. Accepted after jury
all-PASS and operator review on the PR. The downstream PRD owns the
requirements articulation; the questions deferred at acceptance
(command surface, keep-alive coupling, channel-path adoption, sandboxed
watch interaction, superseded-session cleanup) resolve in the PRD's
Decisions and Trade-offs section.

## Problem Statement

`niwa dispatch` hands back a session handle, but the handle is
launch-and-observe only: it can attach a terminal, tail logs, stop the
worker, or reap the instance. It cannot deliver a follow-up
instruction. Once the initial task prompt is consumed, the only ways to
give the worker more work are a human attaching interactively or
steering from claude.ai — both unavailable to a headless coordinator.

The workaround everyone reaches for, `claude --resume <session-id>
--bg "<new task>"`, behaves like a fork: the transcript is inherited,
but a new session id is minted, the original session stays behind as an
idle orphan, and the durable session-to-instance mapping now points at
a session that will never work again. Two sessions can end up sharing
one instance directory and one branch. The pattern has bitten twice
independently: a coordinator re-tasking dispatched workers, and niwa
watch's review continuation, which degrades to once-per-session because
the re-captured ids go ambiguous after a resume (#211).

The root cause sits in the platform. Claude Code has no supported
headless channel that delivers a prompt to a live background session by
id: resuming a live background session forks unconditionally, the
remote-control bridge only serves human clients, and the inbound
channel mechanism that would fit is restricted to approved plugin
channels on unattended sessions. niwa cannot remove that constraint,
but it currently does nothing to manage it either — every consumer
rediscovers the fork-and-orphan failure on their own.

## User Outcome

Whoever holds a dispatch handle — a headless coordinator, an operator
at a terminal, or niwa's own watch loop — hands the worker its next
task with a single niwa command and moves on. The worker picks the task
up with its accumulated context. Afterward there is exactly one live
session bound to the instance: the mapping is accurate, `niwa list`
tells the truth, no stale session lingers for the reaper, and no second
session contends for the instance's branch. Where the platform forces a
new session id underneath, the niwa handle remains the stable identity
and the swap is invisible to the caller.

## User Journeys

### Coordinator re-tasks an idle worker

A coordinator agent dispatched a worker for a task and later receives
follow-up work for the same context. It runs the retask command against
the handle it recorded at dispatch time. The worker continues in its
instance with full context; the coordinator's bookkeeping still holds
one handle, one session, one instance.

### Operator iterates without attaching

An operator reviews a dispatched worker's output from `niwa list` and
logs, and wants a revision without attaching a terminal to the session.
They run the retask command with the follow-up instruction and check
back later. The session they steer is the same one keep-alive and
remote control know about.

### Watch chains a review continuation

A watched PR receives new commits while its review session sits
detached and idle. The watcher re-tasks the existing review session
with the new head instead of driving its own stop-and-resume. The
reviewer's accumulated context carries across pushes repeatedly, rather
than degrading after the first continuation the way the current
once-per-session behavior does (#211).

### Re-task after a long idle

A worker's process was stopped by the platform's idle supervisor hours
ago, but its session and instance are intact. The retask command
revives the session first (respawn preserves the session id), then
delivers the instruction. The caller does not need to know whether the
worker was running, idle, or stopped.

## Scope Boundary

In scope:

- A niwa command that delivers a follow-up instruction to a dispatched
  session through its handle.
- Safe ownership semantics: after a retask, exactly one live session is
  bound to the instance, the durable mapping is updated atomically, and
  no orphaned session or stale job entry is left behind.
- Working against idle and stopped workers, including revive-on-retask.
- Handling the platform's fork-on-resume behavior when it is forced:
  the niwa handle stays stable while the underlying session id rotates,
  and the superseded session is cleaned up.
- Adopting the same primitive in niwa watch's continuation path so
  chaining stops degrading (closes the gap #211 describes).
- Documenting the platform constraints and the retask semantics.

Out of scope:

- Interrupting a worker mid-turn. Delivery is queue-shaped: an
  instruction lands as the worker's next turn.
- The human steering paths — terminal attach and claude.ai remote
  control — including keep-alive itself. Those already exist; this
  feature is the programmatic path.
- Building and distributing an approved channel plugin. The platform's
  channel mechanism is the natural end state, but third-party channels
  are currently blocked on unattended sessions; the design should stay
  compatible without depending on the unlock.
- Changes to Claude Code itself. The precise platform gaps are recorded
  and belong in an upstream feature request, tracked separately.
- Re-tasking sessions niwa did not dispatch, or sessions in another
  workspace.

## References

- `docs/briefs/BRIEF-instance-dispatch.md` — the dispatch feature this
  extends; defines the handle and mapping the retask command operates
  on.
- `docs/briefs/BRIEF-niwa-session-keep-alive.md` and
  `docs/guides/session-keep-alive.md` — the reachability half of the
  story; retask is the addressability half.
- `docs/designs/current/DESIGN-niwa-watch-pr-hardening.md` — the watch
  continuation design whose residuals (#211) this feature would
  subsume.
- tsukumogami/niwa#211 — chainable continuation across multiple pushes;
  the watch-side symptom of the same gap.
