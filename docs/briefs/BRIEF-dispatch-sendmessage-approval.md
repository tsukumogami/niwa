---
schema: brief/v1
status: Accepted
problem: |
  Developers who dispatch background Claude Code sessions with niwa and have
  them message each other can't count on those messages arriving unattended.
  Whether a message is delivered or held for a human depends on whether the two
  sessions happen to run in the same permission posture, which they often don't,
  so unattended fan-outs stall.
outcome: |
  A developer who opts in on their machine gets every peer message to the
  sessions niwa dispatched delivered without a prompt, whatever posture the
  sender runs in and across restarts, without thinking about permission modes.
  One who doesn't opt in sees no change.
motivating_context: |
  From 2026-09-02, peer messages between niwa-dispatched sessions began arriving
  as approval prompts instead of being delivered, after a Claude Code release
  changed which settings decide a session's permission posture. A niwa fix
  restored the posture for new dispatches, and the prompts came back on
  2026-09-10 (UTC) between a session dispatched before that fix and one
  dispatched after it.
---

# BRIEF: Unattended peer messages for dispatched sessions

## Status

Accepted

Three framing questions are left to the PRD. What the machine setting and the
per-dispatch control are called, where the name mustn't suggest a boundary niwa
can't enforce. Whether a workspace can set a default for its dispatches, between
the machine setting and the per-dispatch control, which changes who can turn the
behavior on. And what happens when a dispatch launches an agent that can't
receive the behavior: warn and continue, or stay silent.

Revised on 2026-09-10 after a live experiment. The earlier text blamed the way
niwa brings sessions back after a resume. Claude Code in fact restores a
session's launch settings when it restarts or reopens it, and the holds come
from sessions in different permission postures messaging each other. A later
review also corrected Journey 3: switching the behavior off returns a session to
Claude Code's default, which doesn't hold messages from sessions in the same
posture.

## Problem Statement

A developer running several background Claude Code sessions through
`niwa dispatch` usually wants them to cooperate. Workers report results to a
coordinator, a coordinator hands follow-up work back to a worker, a
long-running session checks in with one launched later. The sessions do this
with Claude Code's cross-session messaging, and for any of it to work in the
background, a message one session sends has to reach the other while nobody is
watching either of them.

Today it often doesn't. Some messages arrive. Others wait inside the receiving
session for a person to approve them. Which of the two happens depends on
whether the two sessions run in the same permission posture, and a developer's
sessions routinely don't: one was dispatched before a niwa or Claude Code
upgrade and another after it, one is the developer's own session, one runs in a
workspace that asks for a different posture. Nothing in the dispatch output says
a message is waiting, so the first sign is usually a fan-out that quietly
stopped making progress.

The developer also can't tell niwa what they want here. They might be happy for
every session they dispatch to take messages from its peers. They might want
that for most dispatches but not for one they intend to steer themselves.
Either way the result is left to chance, and the only workaround is to watch
every session and approve prompts by hand, which defeats the point of
dispatching the work to run in the background.

## User Outcome

A developer who has opted in dispatches sessions that talk to each other and
stops thinking about delivery. Workers' reports reach the coordinator, the
coordinator's follow-ups reach the workers, and that stays true when any of
them is restarted or reopened later. They don't need to know which permission
mode each session ended up in, because delivery to those sessions no longer
hinges on it. When the behavior is on, the dispatch output says so, so they can
tell afterwards which sessions were accepting messages unattended.

The choice is theirs and it's cheap to change. They make it once for their
machine, and they can switch it off for a single dispatch without touching the
machine setting. A developer who never opts in sees nothing new.

A developer who also wants workers' messages to reach their own interactive
session unattended learns what that takes, and what it costs, once, when the
behavior first takes effect on their machine, rather than discovering it from a
stalled run.

## User Journeys

### Journey 1: A coordinator collecting reports across a restart

A developer who has turned the behavior on for their machine runs a coordinator
session, dispatched with niwa, that dispatches four workers to investigate
separate parts of a problem. The workers finish at different times and send
their findings to the coordinator, which the developer left idle overnight and
came back to in the morning. Every report is waiting in the coordinator's
conversation, none of them behind an approval prompt, and the developer spends
the morning reading results instead of approving them.

### Journey 2: Opting in on a machine

A developer who dispatches background work every day on their own laptop has
been approving peer-message prompts by hand and decides to stop. They turn the
behavior on once for the machine. Their next dispatch prints a line saying the
session will accept peer messages without asking. At the same moment they're
told, once, how to extend the behavior to their own interactive session, and
that doing so applies to every Claude Code session they run, so they decide
knowing the cost.

### Journey 3: Switching it off for one dispatch

The same developer, with the machine default on, dispatches a session they
intend to steer themselves and switches the behavior off for that one dispatch.
The session keeps Claude Code's default handling of messages from other
sessions: a message from a session in a different posture waits for their
approval, and the dispatch output shows the machine setting was overridden.
Every other session keeps the machine default. The guide tells them what the
default doesn't do: sessions in the same posture as this one can still reach it
without a prompt, so switching the behavior off isn't a way to isolate a
session.

### Journey 4: Workers alongside a coordinator from before an upgrade

A developer who has opted in keeps a long-running coordinator that was
dispatched before they upgraded, and dispatches fresh workers alongside it. The
coordinator runs with permission prompts on and the new workers with them off.
When the coordinator hands a worker its next task, the task arrives straight
away, because the worker accepts it. When the worker reports back, the report
waits for approval inside the coordinator, which was never given the behavior.
The developer already knows why from the explanation they were shown once, and
dispatching a fresh coordinator clears it.

## Scope Boundary

### In scope

- An opt-in, per-machine setting that lets sessions niwa dispatches accept
  messages from peer sessions without an approval prompt, off unless the
  developer turns it on.
- A per-dispatch override that works in both directions.
- The behavior staying in effect when a dispatched session is restarted or
  reopened.
- A visible note in the dispatch output whenever the behavior is active, and
  when a dispatch turns it off.
- A one-time explanation of what the developer must do for their own
  interactive session and what that costs.

### Out of scope

- Changing the developer's personal Claude Code settings for them, or checking
  whether they've done it. niwa explains the step; the developer takes it. The
  setting applies machine-wide to every session they run, and it isn't niwa's to
  own.
- Limiting acceptance to peers in the same workspace, or on the same machine. A
  reader might expect that boundary, but messages are addressed by session name
  across a developer's whole account and niwa sits nowhere in that path, so it
  can't enforce one. Offering it would promise protection that doesn't exist.
- Isolating a dispatched session so that every message into it waits for
  approval. Switching the behavior off returns a session to Claude Code's
  default, which only holds messages from sessions in a different posture.
- Granting a session anything beyond receiving a message. Whatever a message
  asks a session to do still goes through that session's own permissions.
- Making sessions niwa didn't launch accept messages, including sessions niwa
  dispatched before this feature existed. Each session's inbound behavior is
  fixed when it's launched.
- Changing how niwa resumes or reopens sessions. Claude Code already restores a
  session's launch settings when it restarts or reopens it.
- Containment for dispatched sessions comparable to what `niwa watch` applies to
  review sessions, such as a sandbox and outbound network limits. It's closely
  related and matters more once this lands, but it's separate work.
- Making dispatched session names unique, even though a duplicate name can route
  a message to the wrong session. That's separate work.
- Removing the permission value niwa writes into instance settings that current
  Claude Code versions no longer honor. That's separate work too.
- Changing which permission mode dispatched sessions run in.
- Anything that needs a change to Claude Code itself.

## References

- [DESIGN: dispatch permission mode](../designs/current/DESIGN-dispatch-permission-mode.md)
  records the earlier regression and the fix that restored the permission
  posture for freshly dispatched sessions.
- [Remote control on dispatch](../guides/remote-control-on-dispatch.md) and
  [session keep-alive](../guides/session-keep-alive.md) are the two existing
  guides for a host-level, off-by-default, per-dispatch-overridable behavior of
  this kind.
- [DESIGN: niwa mesh removal](../designs/current/DESIGN-niwa-mesh-removal.md)
  explains why niwa removed its own agent-messaging layer, which this feature
  doesn't reintroduce.
