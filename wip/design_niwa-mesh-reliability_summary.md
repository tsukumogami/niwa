# Design Summary: niwa-mesh-reliability

## Input Context (Phase 0)

**Source:** /explore handoff (auto mode)

**Problem:** Nine open issues filed since #92 describe a coherent set of
failures in the niwa mesh subsystem (multi-agent coordination):
coordinator routing, worker plugin propagation, niwa-mesh skill leakage,
session and daemon health visibility, the `dangling` task classification,
and missing coordinator ergonomics primitives. The shared root cause is
that niwa stores or relies on filesystem state the API layer either
doesn't read or relies on implicit discovery to find. The fixes
interact, so a single coordinated design is required rather than nine
independent PRs.

**Constraints:**
- Public repo tone (clear to first-time contributors, no internal
  jargon, no competitor names)
- Conventional commits, no AI attribution / co-author lines
- No emojis in code or committed documentation
- Tactical scope (per niwa CLAUDE.md `## Default Scope: Tactical`)
- Bring runtime contract and the niwa-mesh skill text back into
  lockstep in one pass
- Reuse existing primitives where possible (daemon.pid, IsPIDAlive,
  lookupLiveCoordinator, flat task store)
- `Status` field stays single-writer; orthogonal `daemon` sub-object
  for liveness
- No new daemon heartbeat file (existing `daemon.pid` + `IsPIDAlive`
  is sufficient)

## Open Decision Questions (for /shirabe:design)

1. Plugin propagation mechanism for #108 (argv flag vs.
   CLAUDE_CONFIG_DIR env vs. filesystem mirror in scaffoldWorktreeNiwa).
2. Dangling-task lifecycle shape for #112 (real state.json state vs.
   opt-in resurrect primitive vs. both).
3. Skill delivery path for #97 (CLAUDE.local.md injection vs.
   instance-root-only with discovery configured to find it).
4. `required_skills` placement for #113 (inside body vs. top-level
   delegateArgs field).
5. Worker `coordinator` role visibility for #109 fix shape
   (special-case isKnownRole vs. provision a synthetic
   roles/coordinator/inbox/ in worktrees).

## Decisions Already Made (during exploration)

- Treat the cluster as one design, not nine bugfixes.
- #92 and #109 collapse into a single fix (same isKnownRole
  precondition mismatch).
- Status field stays single-writer; add orthogonal `daemon` sub-object.
- No new daemon heartbeat file.
- niwa_redelegate accepts dangling source tasks.

## Current Status

**Phase:** 0 - Setup (Explore Handoff)
**Last Updated:** 2026-05-09
