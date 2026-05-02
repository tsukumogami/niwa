# Design Summary: coordinator-loop

## Input Context (Phase 0)
**Source:** /explore handoff
**Problem:** When a coordinator delegates a long-running task, the stall watchdog kills the worker at 15-minute intervals because nothing calls niwa_report_progress during deep workflow work. On each kill, niwa spawns a fresh process — losing all in-session context — and the cycle repeats until max_restarts is exhausted. Three architectural gaps need to be closed: automatic progress heartbeating, context-preserving stall recovery, and a typed error from niwa_ask when the target role has no live session.
**Constraints:** Skills must not carry niwa awareness; fix must be application-agnostic; resume preferred over fresh spawn; stop hook approach confirmed safe (append-only merge, no shirabe conflict).

## Decisions from Exploration
- Skill-level progress reporting fix rejected (abstraction violation)
- Stop hook as primary heartbeat mechanism (workspace.toml [hooks])
- Resume-with-reminder on stall kill, fresh spawn as fallback only
- Typed error from niwa_ask on no-live-session (not ephemeral-spawn fallback)
- Decision protocol enforcement is out of scope (by design)

## Open Questions for Design Phase
1. Stop hook automation: fully automated CLI call vs. reminder output
2. Session ID capture: new MCP tool vs. extended niwa_report_progress vs. post-mortem scan
3. Resume code path: reminder wording, loop cap, session file integrity check
4. niwa_ask error response format and status vocabulary

## Current Status
**Phase:** 0 - Setup (Explore Handoff)
**Last Updated:** 2026-05-01
