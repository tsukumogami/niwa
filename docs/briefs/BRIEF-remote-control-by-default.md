---
schema: brief/v1
status: Done
problem: |
  When a developer dispatches a background worker with `niwa dispatch`, they are
  by definition not at that worker's keyboard, so they cannot turn on Claude Code
  Remote for it the way they would for an interactive session. The host default
  is off, and the only lever is a per-session settings key, so a dispatched worker
  runs unsteerable unless the developer remembers to enable remote-control by hand
  for every single dispatch.
outcome: |
  A developer dispatches a worker and can immediately watch and steer it from
  Agent View / claude.ai / mobile, because niwa turned remote-control on by
  default -- configured once at the host level and applied only to dispatched
  workers -- while still being able to turn it off for a specific workspace
  or instance when they don't want it.
motivating_context: |
  A feasibility spike (docs/spikes/SPIKE-remote-control-by-default.md, Complete)
  proved the mechanism end-to-end: launching a `claude --bg` worker with the
  settings key `remoteControlAtStartup: true` (and nothing else) makes it
  live-steerable from Agent View / mobile, and `claude --settings` outranks the
  worker's project settings.json. This brief frames the feature now that the
  blocking question is settled.
---

# BRIEF: remote-control by default on dispatched workers

## Status

Done

The downstream PRD owns the requirements; the downstream DESIGN owns the host-toggle
placement and the dispatch-time injection mechanism. This brief stops at the
developer-facing framing.

## Problem Statement

niwa launches Claude Code sessions in several ways. One of them, `niwa dispatch`,
launches a *background* worker -- a session the developer hands a task to and walks
away from, monitoring it later from Agent View, claude.ai, or their phone. The whole
value of a dispatched worker is that the developer is not sitting at its keyboard.

Claude Code Remote -- the bridge that makes a session live-steerable from claude.ai
and mobile -- is governed by a per-session setting, `remoteControlAtStartup`, and it
is off unless explicitly turned on. For an interactive session the developer can flip
it on themselves (a flag, a slash command). For a dispatched worker they cannot: the
worker starts headless and runs unattended, so by the time the developer wants to
watch or steer it, the moment to enable remote-control has already passed. The host's
default is off, and there is no niwa-level way to say "every worker I dispatch should
start steerable."

The result is a gap exactly where remote-control matters most. The sessions a
developer most wants to monitor and redirect from afar -- the unattended ones -- are
the sessions that arrive unsteerable. The only workaround is to remember, on every
single `niwa dispatch`, to thread the right setting through by hand. Nobody wants a
per-dispatch ritual for something that should be a once-set preference.

## User Outcome

A developer sets one host-level niwa preference -- "dispatched workers start with
remote-control on" -- and from then on every worker they launch with `niwa dispatch`
is immediately watchable and steerable from Agent View, claude.ai, and mobile,
without any per-dispatch step. The default is applied only to dispatched workers, so
the developer's interactive sessions and other niwa-launched sessions are untouched
and keep behaving exactly as before.

The default is a default, not a mandate. When the developer doesn't want a particular
workspace or instance's dispatched workers to start steerable, they turn it off
there, and that downstream choice wins over the host default. So the common case
(dispatch and immediately monitor) costs nothing, and the exception (this corner of my
work should stay private/local) is still expressible. The developer never has to
hand-enable remote-control for a worker again, and never has to accept it where they
don't want it.

## User Journeys

### A developer dispatches a worker and immediately watches it from their phone

A developer has set the host preference once. They run `niwa dispatch "investigate the
flaky test"` from their workspace and close the laptop. On their phone, the worker is
already there in Agent View, live -- they read its progress over coffee, and when it
heads down the wrong path, they send it a steering message and it course-corrects.
They never enabled anything for this specific worker; dispatching it was enough.

### A developer opts a sensitive workspace out of the default

The same developer works in one workspace whose dispatched workers they would rather
keep off the remote-control bridge. They set remote-control off in that workspace's
niwa config. From then on, workers dispatched from that workspace start unsteerable
while every other dispatch still starts steerable. The host default stays on
everywhere else; the one place they said "not here" is honored.

### A developer on an ineligible host is told why, not left guessing

A developer whose host can't actually use Claude Code Remote (for example, signed in
with an API key rather than a claude.ai account) dispatches a worker expecting it to be
steerable. Instead of the worker silently starting without the bridge, niwa surfaces a
clear, one-line reason that remote-control could not be enabled for the dispatch, so
the developer understands the gap and what would close it rather than wondering why the
worker never appeared as steerable.

## Scope Boundary

In scope: a single host-level niwa preference that defaults `niwa dispatch` workers to
start with Claude Code Remote on; scoping that effect to dispatched workers only; a
downstream (workspace or instance) override that turns it off and wins over the
host default; and a clear message when the host cannot use remote-control.

Out of scope: enabling remote-control on interactive root/instance sessions, ephemeral
SessionStart-hook sessions, or `niwa apply` instances; cross-machine or remote
dispatch; building or changing Claude Code Remote itself (niwa only flips a switch the
harness already provides); and anything that would grant remote-control entitlement the
host account doesn't already have (login, scopes, subscription, org policy, feature
rollout remain server-side preconditions).

