# Explore Scope: niwa-ask-live-coordinator

## Visibility

Public

## Core Question

How should `niwa_ask(to='coordinator')` route to an already-running coordinator session rather
than spawning an ephemeral process? The design centers on piggybacking questions onto the
coordinator's existing polling/blocking calls (`niwa_check_messages`, `niwa_wait`), making
delivery transparent to both sides without any new notification mechanism.

## Context

Issue #92. The `handleAsk` inbound routing path was intentionally removed and never replaced.
The coordinator is always bypassed: any ask to `coordinator` spawns an ephemeral `claude -p`
to fabricate an answer, silently breaking approval gates.

The user-proposed direction: both `niwa_check_messages` and `niwa_wait` become delivery
points for pending questions. The coordinator picks up questions naturally at its next
"attention point" — either during progress polling or while blocking on task completion.
`niwa_wait` needs to return early when a question arrives (not just when the task completes)
to prevent the coordinator-worker deadlock where each waits on the other. No timeout/fallback
spawn — questions queue until the coordinator next contacts the daemon.

## In Scope

- Fix inbound routing: `niwa_ask(to='coordinator')` queues the question instead of spawning
- `niwa_check_messages` surfaces pending questions alongside task updates
- `niwa_wait` returns early when a question arrives, allowing the coordinator to answer and re-wait
- Session liveness tracking: daemon knows whether to queue vs spawn
- Coordinator response mechanism (new tool or existing channel)
- niwa-mesh skill documentation updates for the new polling loop pattern

## Out of Scope

- Outbound direction (coordinator → worker delegation) — already works
- Changing `niwa_ask` tool signature from the caller's perspective
- Multi-coordinator topologies
- Timeout / fallback-to-spawn for unanswered questions

## Research Leads

1. **How do `niwa_check_messages`, `niwa_wait`, and `handleAsk` work today?**
   Understand the existing data model, control flow, and daemon-side state before touching
   anything. The implementation details will determine what changes are surgical vs structural.

2. **How does the daemon track that a coordinator session is active and reachable?**
   Liveness detection is what separates "queue for live session" from "spawn ephemeral."
   What registration mechanism exists or needs to be added? How does stale detection work?

3. **What does the coordinator receive when a question is pending during `niwa_wait`?**
   The semantic change — "done or question, whichever comes first" — and what the
   coordinator's re-wait loop looks like after it responds to a question.

4. **What tool does the coordinator use to answer a worker's question?**
   Is there a `niwa_respond` tool to add, or does answering happen through an existing
   channel? What's the API shape that makes this transparent and low-friction?

5. **What changes are needed in the niwa-mesh skill documentation?**
   Understand what existing skill docs cover the coordinator polling loop and what
   new patterns need describing.
