---
schema: brief/v1
status: Draft
problem: |
  A developer's inbound work arrives on two queues and only one is staged.
  Review requests reach the agent view already researched and drafted; chat
  questions arrive as raw text, though their answers depend most on reading
  across the workspace's repos. So they get answered late, or from memory.
outcome: |
  A mention the developer has not read yet already carries a drafted answer
  researched against the actual repos. Their work shrinks from
  switch-research-compose-reply to read-judge-release, and declining costs
  nothing: no answer is sent in their name without an explicit release.
motivating_context: |
  The technical design for this feature was written first, from a settled
  exploration, because the parent roadmap routes the feature straight to a
  design. Writing it surfaced several requirement-altitude choices that had no
  upstream to be checked against and were decided inside the design by default.
  This brief exists to put the framing under those choices and hand the
  contested ones to the downstream PRD.
---

# BRIEF: niwa watch Slack source

## Status

Draft

Three Open Questions are recorded below for the downstream PRD to resolve. They
must be closed or removed before this brief transitions to Accepted.

## Problem Statement

A developer working across a multi-repo workspace has two streams of inbound
work, and the toolkit only stages one of them.

Review requests are handled: a poll finds the pull requests where the developer
is the directly-requested reviewer, and a contained agent has already read the
change and drafted a review by the time the developer looks. The work arrives
pre-chewed.

Questions asked in chat are not handled at all, and they are the harder half.
When a teammate asks why a retry path behaves the way it does, or which service
owns a config key, the answer is not in the chat thread and not reliably in the
developer's head -- it is spread across repos the developer would have to go and
read. That produces two bad outcomes and no good one. Either the developer
context-switches out of what they were doing, clones or greps across
repositories, reconstructs the answer, and writes it up -- an expensive
interruption for a question that was cheap to ask -- or they answer from memory,
quickly and approximately, and the questioner acts on something that used to be
true. The common failure is neither: the message sits unanswered while the
developer intends to look into it properly later.

Nothing in the toolkit currently turns "someone asked me a question in a
channel" into staged, grounded work, even though every ingredient exists. The
machinery that stages review requests ships and runs. What is missing is the
framing of chat as a work queue worth staging, and the shape of what a staged
chat answer should be.

The gap is worth closing now because the machinery is already proven on the
easier queue, so the incremental cost is a second source rather than a second
system -- and because the value asymmetry is stark. A staged review saves the
developer reading time on work they were going to do anyway. A staged chat
answer changes whether the question gets a real answer at all.

## User Outcome

The developer opens their agent view and a mention they have not read yet
already has a drafted answer attached to it, researched against the repos that
actually hold the answer. They read the draft, judge it, and either release it
into the thread or throw it away. That is the whole interaction.

Three properties make that outcome the one worth building rather than a
faster version of the status quo.

**The developer's job changes shape.** It goes from
switch-research-compose-reply to read-judge-release. The expensive parts --
noticing the question, deciding which repos bear on it, reading them,
assembling an answer -- have already happened. What remains is the part only the
developer can do: deciding whether the answer is right and whether it should be
said.

**Declining is free.** Nothing reaches the thread without an explicit release,
and the developer sees the exact text before it is sent. A draft that is wrong,
half-informed, or simply not worth sending costs one glance and a decline. This
is what makes a fallible drafting step safe to run automatically: its output is
a proposal, never an act.

**The answer is the developer's, not a bot's.** It lands in the thread from
their own account, in a form they approved. The teammate is talking to their
colleague, who happened to have help composing the reply -- not to an assistant
the colleague deployed at them.

What the developer should not experience: being pinged about it, being expected
to respond quickly, or discovering that something was said on their behalf.

## User Journeys

### The overnight question

A teammate mentions the developer in a watched channel at 11pm, asking why a
retry path gives up after three attempts. Nobody is awake. The next poll admits
the thread, reads it, and stages a contained agent that researches the relevant
repos and drafts an answer citing the actual code. In the morning the developer
opens their agent view, finds the drafted answer alongside the question, reads
it, and releases it into the thread. The teammate's first indication that
anything unusual happened is that the answer arrived with specifics.

### The steer

The developer reads the draft and it is answering the wrong question -- the
teammate meant the client's retry, not the server's. Instead of discarding it,
the developer sends the agent back with that correction. The agent revises
against the same thread and re-surfaces a new draft. The developer releases the
second one. The correction costs a sentence, not a re-research.

### The follow-up

The developer releases an answer, and the teammate replies in the same thread:
"that explains the client, but what sets the server's ceiling?" No new mention.
The thread is already admitted, so the follow-up re-fires on its own: a fresh
draft is staged against the whole conversation so far, including the answer just
given. The developer returns to a second draft waiting. The conversation
continues at the developer's tempo without either party re-establishing context.

### The decline

A drafted answer is confidently wrong -- it read a deprecated code path. The
developer declines it. Nothing is posted, nothing is queued for later, and the
teammate never sees that a draft existed. The developer answers by hand, or
does not answer at all. The failed draft cost one read.

### The retire

A thread the developer was mentioned in months ago has turned into ongoing
chatter among other people. It keeps producing drafts the developer keeps
throwing away. The developer retires the thread, and it stops being watched.
Their inbox goes back to carrying only threads that are still theirs.

## Scope Boundary

### In

- Answering in channels the workspace operator has explicitly listed.
- A mention of the developer as the trigger that admits a conversation.
- An author allowlist deciding whose messages may start work: if nobody is
  listed, nothing is answered.
- The whole thread as the context an answer is drafted from, including messages
  from people who cannot themselves trigger work.
- Follow-up messages in an admitted thread continuing the conversation without
  requiring a new mention.
- A drafted answer the developer reads before anything is sent, with the exact
  text visible at the moment of approval.
- Posting into the thread from the developer's own identity, on their explicit
  release.
- Retiring a thread so it stops producing work.

### Out

- **Ambient monitoring of channel traffic.** Messages that do not mention the
  developer are never work, however relevant they look. Deciding relevance by
  judgment rather than by an explicit signal is a different and much harder
  feature, and getting it wrong floods the queue the feature exists to make
  useful.
- **Direct messages.** Only listed channels. DMs carry a different privacy
  expectation and would need their own framing.
- **Posting without an explicit human release.** Not a configuration option,
  not a trusted-channel exemption. If the release cannot be offered, the answer
  waits as a draft.
- **Real-time answering.** The developer's expectation is a staged draft they
  return to, not a fast reply. Nothing here should be measured in seconds, and
  the feature should not be built as though it were.
- **Slack-side controls.** No buttons, slash commands, or interactive messages
  in the thread. Every control the developer needs lives where they already
  review staged work.
- **Steering the agent toward particular repos.** The agent works out which
  parts of the workspace bear on the question. Operator-supplied hints are a
  precision refinement for later, once whole-workspace grounding is shown to be
  insufficient.
- **Reading Slack live during drafting.** The agent works from the conversation
  as captured when the work was staged. Reading Slack live during the work is a wider
  capability, with its own consequences for what else that work could reach.
- **Multiple Slack workspaces.** One Slack workspace per niwa workspace.
- **Other question sources.** CI failures and issue threads are separate future
  queues that would reuse this feature's admission shape.

## Open Questions

These are framing details the downstream PRD resolves. Each is currently
answered inside the technical design on the design's own authority, which is
the wrong altitude for it -- the PRD should settle them and the design should
cite them.

1. **How many channels does the first version answer in?** The parent roadmap
   scopes the initial slice to one channel and pushes multi-channel to the
   hardening feature that follows, while the settled shape of the setting is a
   list of channels. Whether "multi-channel" means "more than one entry in the
   list" or "many channels at volume" decides whether the first version caps
   the list. The design currently leaves it uncapped.

2. **What latency does the outcome actually require?** The outcome is a draft
   the developer returns to, which admits a wide range of poll cadences. The
   requirement needs a floor -- if a question asked mid-morning must be answered
   before end of day, that is a different cadence than "by tomorrow morning" --
   because the cadence sets both the cost and whether the overnight journey is
   the typical case or the only one.

3. **What must happen when the release gate cannot be offered?** On a machine
   where the developer cannot be offered that approval, the feature can either
   stage a draft the developer posts by hand or refuse to run at all. The
   design currently chooses the former. That is a product decision about
   whether a degraded version is better than none, and it should be made as
   one.

## Downstream Artifacts

- `docs/prds/PRD-niwa-watch-slack-source.md` (planned) -- the requirements
  contract; owns the three Open Questions above.
- `docs/designs/DESIGN-niwa-watch-slack-source.md` -- the technical design.
  Exists in Proposed status, written before this brief; it is revised to cite
  the PRD once the PRD resolves the open questions.

## References

- `docs/designs/current/DESIGN-niwa-watch-once-pr-review.md` -- the review-request
  queue this feature's framing is the second instance of.
- `docs/designs/current/DESIGN-niwa-watch-pr-hardening.md` -- the durable state
  and attention controls the staged-work model depends on.
