# Design Decisions: cross-session-communication

## Phase 0

- **Rewrote existing design doc skeleton**: The existing DESIGN-cross-session-communication.md predated the daemon-managed lifecycle architecture. It contained open questions that have all been resolved by the PRD revision. Decision: rewrite from scratch rather than continue from the stale skeleton. The old doc had 6 open questions; all 6 are now settled architectural decisions in the PRD.

- **Proceeded with Draft PRD**: Phase 0 setup instructions say to stop if PRD is not Accepted. User explicitly directed `/design` on this PRD. Decision: treat Draft PRD as the upstream and proceed. Note in wip summary.

- **Removed stale wip solution proposal**: `wip/prd_cross-session-communication_solution-proposal.md` was superseded by the PRD rearchitecture. Deleted before starting design phase.

## Phase 1

- **4 independent decision questions**: Daemon lifecycle, Claude session ID discovery, niwa_ask blocking mechanism, ChannelMaterializer integration. No coupling between them — all can run in parallel.
