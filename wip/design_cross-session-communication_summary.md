# Design Summary: cross-session-communication

## Input Context (Phase 0)
**Source:** /explore handoff
**Problem:** Niwa workspaces contain multiple repos with independently-opened Claude sessions that have no way to communicate without the user acting as a manual relay. Niwa should provision a workspace-aware messaging layer so sessions can exchange messages (questions, delegation, review feedback, status updates) without user intermediation.
**Constraints:**
- Same-machine first; design path to network transport required
- No external dependencies for same-machine v1 (no Redis, no NATS daemon)
- Sessions are independently opened by the user, not spawned by a lead
- Must survive individual session restarts (crash-safe messaging)
- Niwa provisions the channel at workspace create/apply time; sessions self-register
- Session spawning is out of scope for Phase 1

## Current Status
**Phase:** 0 - Setup (Explore Handoff)
**Last Updated:** 2026-04-20
