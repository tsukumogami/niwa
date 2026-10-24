# /brief discovery: niwa-watch-slack-source

Invoked under `/scope` sub-agent dispatch (`rationale: fresh-chain`,
`suppress_status_aware_prompt: true`). The parent owns the approval prompt at
the chain boundary, so Phase 5 hands back rather than prompting.

## Grounding

Upstream framing exists but is thin and unmerged, which is why this brief is
being written after a design rather than before one:

- The parent roadmap's Slack-skeleton entry sequences the feature and states
  the slice, but routes it `needs-design` with no `needs-brief` or `needs-prd`.
- The parent strategy's Slack block states the value ("answer a teammate's code
  question by pre-staging a research agent grounded in the workspace's repos")
  and is the source of the problem framing here. It also oversells latency,
  which the exploration corrected; this brief takes the corrected reading.
- The exploration that preceded the design settled the mechanism. Mechanism is
  not framing: the design encodes HOW, and several WHAT-altitude choices ended
  up decided inside it for want of an upstream to check against. Those are
  surfaced as Open Questions here rather than silently ratified.

## Problem/outcome pair

**Problem.** A developer's inbound work arrives on two queues. One -- review
requests -- is already staged as researched, drafted work waiting in the agent
view. The other -- questions asked in chat -- gets nothing, even though it is
the queue where the answer most depends on reading across the workspace's
repos. So chat questions are answered slowly (after a context switch) or
shallowly (from memory).

**Outcome.** A mention the developer has not read yet already carries a drafted,
workspace-grounded answer. Their work becomes read-judge-release rather than
switch-research-compose-reply, and declining is free: nothing is said in their
name without an explicit release.

## Framing-shift answer (R4)

Not shifted -- never written at this altitude. The gap is absence, not drift.
Signal 1 of the EITHER-gate (no BRIEF at the canonical path) is what fires.

## Journeys considered

Kept five distinct entry points: the overnight question (the base case), the
steer (draft is off-target), the follow-up (conversation continues without a
new mention), the decline (nothing posted), and the retire (thread has gone to
chatter). Rejected as non-distinct: "two people ask in the same thread" (a
variant of the follow-up) and "the question needs no answer" (the agent's
no-op, which is internal behavior rather than a user journey).

## Deferred to the PRD

Three requirement-altitude questions the design currently answers on its own
authority. Recorded as Open Questions so the PRD resolves them rather than
inheriting them:

1. Channel cardinality in v1 (the roadmap says one channel; the settled binding
   is a list; the design shipped an uncapped list).
2. The latency the outcome actually requires, which sets the poll cadence.
3. Required behavior when the human-approval gate cannot be established.
