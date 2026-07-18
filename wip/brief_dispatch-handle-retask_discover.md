# Brief Discover: dispatch-handle-retask

## Problem/outcome pair (from exploration findings)

Problem: a dispatch handle is launch-and-observe only. Neither a headless
coordinator nor an operator can hand a running worker a follow-up task
through it. The only available maneuver (`claude --resume <id> --bg`)
forks: context is copied but a new session id is minted, the original
session goes orphan, and two sessions can end up owning one instance.
niwa watch hit the same wall independently (ED2 continuation degrades to
once-per-session, #211).

Outcome: `niwa retask <handle> "<prompt>"` (name TBD) hands the worker a
follow-up; the worker continues with its accumulated context in the same
instance, exactly one session owns the instance afterward, nothing is
orphaned, and the mapping/observability stay truthful.

## Platform constraints (verified by spikes, 2026-07-18, claude 2.1.214)

- No supported headless in-place push into a live `--bg` session exists.
- MCP channel mechanism (`claude/channel` capability + `--channels`) is
  the right shape but third-party channels are fenced to
  Anthropic-approved plugins on headless sessions (dev flag and
  --managed-settings both stripped by the bg daemon's forwarding
  whitelist; print mode runs no channel subsystem).
- `claude respawn <id>` preserves the session id; `--resume` of a live
  bg session forks unconditionally (even after stop, when relaunched
  with --bg).
- Fork-tolerant rebind (stop -> resume -> recapture -> rebind) is the
  buildable-now path; watch ED2 (#210, on main) is prior art and needs
  the #211 capture-newest disambiguation to chain.

## Chain context

Parent: /scope (state wip/scope_dispatch-handle-retask_state.md).
Operator directive: pause after BRIEF is on a PR for review.
