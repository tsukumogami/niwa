---
schema: brief/v1
status: Draft
problem: |
  Developers who dispatch background Claude Code sessions with niwa and have
  them message each other can't count on those messages arriving unattended.
  Whether a message is delivered or held for a human depends on session
  lifecycle details they never chose, so unattended fan-outs stall.
outcome: |
  A developer who opts in on their machine gets every peer message delivered
  to the sessions niwa dispatched, including after resumes, without thinking
  about permission modes. One who doesn't opt in keeps today's behavior, minus
  the stalls that resuming a session used to cause.
motivating_context: |
  From 2026-09-02, peer messages between niwa-dispatched sessions began
  arriving as approval prompts instead of being delivered. A fix restored
  delivery for freshly dispatched sessions, and the prompts came back on
  2026-09-09 once sessions were resumed rather than freshly launched.
---

# BRIEF: Unattended peer messages for dispatched sessions

## Status

Draft

## Problem Statement

A developer running several background Claude Code sessions through
`niwa dispatch` usually wants them to cooperate. Workers report results to a
coordinator, a coordinator hands follow-up work back to a worker, a
long-running session checks in with one launched later. The sessions do this
with Claude Code's cross-session messaging, and for any of it to work in the
background, a message one session sends has to reach the other while nobody is
watching either of them.

Today it often doesn't. Some messages arrive. Others wait inside the receiving
session for a person to approve them. Which of the two happens isn't something
the developer decides, and it's hard to predict: it depends on whether the two
sessions currently agree about how much they may do without asking, and that
agreement breaks for reasons the developer never chose. The most common is
simply that one session was resumed and the other was launched fresh. Nothing
in the dispatch output says a message is waiting, so the first sign is usually
a fan-out that quietly stopped making progress.

The developer also can't tell niwa what they want here. They might be happy for
every session they dispatch to take messages from its peers. They might want
that for most dispatches but not for one that reads text written by strangers.
Either way the result is left to chance, and the only workaround is to watch
every session and approve prompts by hand, which defeats the point of
dispatching the work to run in the background.

## User Outcome

A developer who has opted in dispatches sessions that talk to each other and
stops thinking about delivery. Workers' reports reach the coordinator, the
coordinator's follow-ups reach the workers, and that stays true when any of
them is resumed hours later. They don't need to know which permission mode each
session ended up in, because delivery no longer hinges on it. When the behavior
is on, the dispatch output says so, so they can tell afterwards which sessions
were accepting messages unattended.

The choice is theirs and it's cheap to change. They make it once for their
machine, and they can reverse it for a single dispatch, such as a session that
will read untrusted content, without touching the machine setting. A developer
who never opts in sees nothing new, except that resuming a dispatched session no
longer strands the messages sent to it.

A developer who also wants workers' messages to reach their own interactive
session unattended learns what that takes, and what it costs, once, at the
moment they opt in, rather than discovering it from a stalled run.

## User Journeys

### Journey 1: A coordinator collecting reports across a resume

A developer runs a coordinator session that dispatches four workers to
investigate separate parts of a problem. The workers finish at different times
and send their findings to the coordinator, which the developer closed overnight
and resumed in the morning. Every report is waiting in the coordinator's
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

### Journey 3: Excluding one sensitive dispatch

The same developer, with the machine default on, dispatches a session to triage
issues filed by strangers, whose text may try to steer whatever reads it. They
switch the behavior off for that one dispatch. That session's incoming messages
go back to needing approval, every other session keeps the machine default, and
the dispatch output shows which choice applied.

### Journey 4: A developer who never opts in

A developer who has never changed a setting resumes a dispatched session that a
sibling session then messages. The message behaves the way it would have if both
sessions had been launched fresh: resuming no longer changes the outcome. They
get this without making any decision at all.

## Scope Boundary

### In scope

- Keeping a session's launch posture when niwa resumes or re-enters it, so a
  resumed session and a freshly launched peer are treated alike.
- An opt-in, per-machine setting that lets sessions niwa dispatches accept
  messages from peer sessions without an approval prompt, off unless the
  developer turns it on.
- A per-dispatch override that works in both directions.
- Carrying the chosen behavior into every way niwa brings a session back, not
  only its first launch.
- A visible note in the dispatch output whenever the behavior is active.
- A one-time explanation, at opt-in, of what the developer must do for their own
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
- Granting a session anything beyond receiving a message. Whatever a message
  asks a session to do still goes through that session's own permissions.
- Containment for dispatched sessions comparable to what `niwa watch` applies to
  review sessions, such as a sandbox and outbound network limits. It's closely
  related and matters more once this lands, but it's separate work.
- Making dispatched session names unique, even though a duplicate name can route
  a message to the wrong session. That's tracked as its own piece of work.
- Removing the permission value niwa writes into instance settings that current
  Claude Code versions no longer honor. Also tracked separately.
- Changing which permission mode dispatched sessions run in.
- Anything that needs a change to Claude Code itself.

## Open Questions

- What the machine setting and the per-dispatch control are called. Whatever the
  name is, it mustn't suggest a boundary niwa can't enforce. The PRD settles it.
- Whether a workspace should be able to set a default for its dispatches,
  between the machine setting and the per-dispatch control. That changes who can
  turn the behavior on, so the PRD decides.
- What happens when a dispatch launches an agent that can't receive the
  behavior: warn and continue, or stay silent. The PRD decides.

None of these questions block the brief.

## References

- [DESIGN: dispatch permission mode](../designs/current/DESIGN-dispatch-permission-mode.md)
  records the earlier regression and the fix that restored delivery for freshly
  dispatched sessions.
- [Remote control on dispatch](../guides/remote-control-on-dispatch.md) and
  [session keep-alive](../guides/session-keep-alive.md) are the two existing
  guides for a host-level, off-by-default, per-dispatch-overridable behavior of
  this kind.
- [DESIGN: niwa mesh removal](../designs/current/DESIGN-niwa-mesh-removal.md)
  explains why niwa removed its own agent-messaging layer, which this feature
  doesn't reintroduce.
