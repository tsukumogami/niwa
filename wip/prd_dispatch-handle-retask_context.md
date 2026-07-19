# PRD Context: dispatch-handle-retask

Upstream: docs/briefs/BRIEF-dispatch-handle-retask.md (Accepted).

## Questions the brief deferred to this PRD (resolve in Decisions and Trade-offs)

1. Command surface: name (`niwa retask`?) and whether it takes a handle,
   a session id, or both.
2. Whether a retask should imply or require keep-alive for workers
   expected to receive follow-ups.
3. How the command detects and adopts the platform channel path if the
   allowlist restriction lifts, without changing its interface.
4. How retask interacts with sandboxed watch review sessions, whose
   settings are re-asserted per continuation today.
5. Whether the superseded session after a forced fork is removed
   immediately or retained briefly for audit.

## Verified platform constraints (spikes, claude 2.1.214, 2026-07-18)

- No supported headless in-place push into a live --bg session.
- `--resume` of a live bg session forks unconditionally (even after
  `claude stop`, when relaunched with --bg); `--fork-session` is the
  explicit flag but the fork happens regardless for live bg targets.
- `claude respawn <id>` preserves the session id and restarts MCP
  servers.
- MCP channel mechanism (`claude/channel` capability under
  `capabilities.experimental`, `--channels server:<name>` opt-in) is the
  right delivery shape but third-party channels are blocked on headless
  sessions: the bg daemon's flag-forwarding whitelist strips
  `--dangerously-load-development-channels` and `--managed-settings`;
  print mode runs no channel subsystem; `--plugin-dir` plugins
  (marketplace `inline`) hit the approved-channels allowlist.
- Prior art on main: watch ED2 continuation (#210) does
  stop -> resume -> recapture -> rebind; #211 is the capture-newest
  disambiguation it needs to chain past once-per-session.
- Dispatch already captures session UUID + short id and records the
  durable mapping (internal/cli/dispatch.go, internal/workspace/
  session_map.go).

## Operator decisions on record

- Final solution must not rely on sudo/root.
- Chain directive: continue through /prd -> /design -> /plan on branch
  worktree-dispatch-handle-retask, keep PR #212 updated and green, stop
  for operator review when the PLAN is ready.
