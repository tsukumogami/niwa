# PRD Scope: dispatch-handle-retask

Upstream: docs/briefs/BRIEF-dispatch-handle-retask.md (Accepted).
Execution: parent-orchestrated by /scope, non-blocking (operator
pre-approved); decisions resolve from the brief and
wip/prd_dispatch-handle-retask_context.md.

## Problem statement

The dispatch handle cannot deliver a follow-up instruction to a running
worker. The PRD must specify a niwa retask capability with safe
ownership semantics (one live session per instance, atomic mapping
rebind, no orphans) that works for idle and stopped workers, adopts the
fork-tolerant path today, and stays forward-compatible with the
platform channel path.

## Research leads

1. Integration surface: exact niwa functions, files, and invariants a
   retask touches — dispatch capture, session mapping, sessionLive,
   reap, list, keep-alive marker, watch staged records and
   continueReview, #211 capture ambiguity. What must requirements
   preserve or change?
2. CLI conventions: how existing niwa commands shape arguments, flags,
   confirmation, --json output, exit codes, and errors, so retask's
   surface requirements match the house style.
