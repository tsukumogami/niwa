# Explore Scope: niwa-session-keep-alive

## Visibility

Public

## Core Question

Can niwa optionally keep a remote-control-enabled dispatch session alive across
long idle periods, so that a session left running overnight is still reachable
from the Claude mobile app / claude.ai web UI the next morning? The keep-alive
should apply only to sessions the user has not explicitly closed (in the Claude
agents TUI) or archived (in the claude.ai web UI).

## Context

- The user runs RC-enabled sessions launched via `niwa dispatch`.
- Pattern: leave a session working overnight, check on it from a phone during the
  morning commute, steer it further.
- Observed problem: after a session goes idle for several hours, its remote-control
  connection is dropped, so it is no longer reachable from the phone.
- The user wants an *optional* keep-alive that prevents this idle disconnect for
  sessions that are still "live" (not deliberately closed/archived).

## In Scope

- niwa-side mechanism to keep a dispatched RC session connected/reachable while idle.
- Detecting the "still live" vs "closed/archived" terminal states so keep-alive
  stops when the user is done.
- Making it opt-in (per-dispatch flag and/or workspace config).

## Out of Scope

- Changing Claude Code's own remote-control internals (we can only wrap/drive it).
- Non-dispatch sessions (interactive foreground sessions the user runs directly).

## Research Leads

1. **What exactly is niwa's dispatch + remote-control wiring, and where could a
   keep-alive hook attach?** Need the precise launch path, RC-enabling flag/env,
   and any existing per-session lifecycle process.

2. **What causes the idle disconnect, and what would actually prevent it?** Is it
   the local process exiting, the Claude Code idle/Stop lifecycle, or a cloud-side
   RC connection timeout? The fix differs for each.

3. **How can niwa reliably detect the terminal states — "closed in the agents TUI"
   and "archived in claude.ai" — versus merely idle?** Keep-alive must stop on
   these; otherwise it resurrects sessions the user meant to end.

4. **What keep-alive strategies are viable (heartbeat/nudge vs detect-and-reconnect
   vs periodic wake), and what are their trade-offs** in cost, correctness, and
   fit with niwa's existing reap/hook machinery?
