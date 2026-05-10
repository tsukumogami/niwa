# /prd Scope: session-attach

## Problem Statement

When a worker agent in a niwa mesh session hits an interesting edge case —
abandons mid-task, makes a questionable decision, hits a constraint, or
stalls — the workspace coordinator has only three recovery options today:
let the worker complete on its own, send `niwa_send_message` (one-way and
unacknowledged), or destroy the session and restart from scratch. There is
no human-in-the-loop primitive that lets the operator step into a session,
see what the agent has done, prompt it interactively, fix things manually,
and hand it back. This forces a binary choice between trusting an agent
that's already off-track and discarding accumulated context that may
represent significant work.

## Initial Scope

### In Scope

- **`niwa session attach <session_id>`** command that locks a session
  against further mesh use, launches Claude Code with the worker's full
  transcript history via `claude --resume`, and releases the lock on exit.
- **`niwa session detach <session_id> [--force]`** companion command as
  the operator escape hatch for stale locks (SSH disconnect, terminal
  crash). Auto-detach on Claude exit is the normal release path.
- **`AVAILABILITY` column** on `niwa session list` output, orthogonal to
  the existing `STATUS` column. Values: `available`, `attached`, `stale`.
- **State-model extension**: nested `attach` sub-object on
  `SessionLifecycleState` (mirrors PR #115's `daemon` sub-object precedent).
- **`niwa_list_sessions` MCP tool** returns the new `attach` sub-object
  (computed at query time from the on-disk attach.state sentinel).
- **`niwa_destroy_session` MCP tool** returns a new `SESSION_ATTACHED`
  error code when destroy is attempted on an attached session without
  `--force`.
- **Filter additions** on `niwa session list` and `niwa_list_sessions`:
  `--attached` / `--available` flags.
- **`niwa session list` flagless default** flips from the deprecated
  `mesh list` alias to the lifecycle view.
- **Daemon coordination**: per-worktree daemon is suspended for the
  duration of attach and respawned on detach, using existing
  `TerminateDaemon` / `EnsureDaemonRunning` helpers.
- **Pre-flight transcript validation** before exec'ing `claude --resume`,
  with three distinct niwa-shaped error messages (no conv_id captured,
  transcript missing, transcript empty).
- **`@critical` Gherkin functional test** for the attach → detach → mesh
  resume golden path.
- **Documentation**: `docs/guides/sessions.md` updated; new attach-specific
  guide if warranted.

### Out of Scope

- Cross-workspace-instance session discovery (`niwa session list` stays
  scoped to current instance, per issue #117 lock-in).
- Multi-user shared-machine semantics (single-UID is declared by reference
  to `DESIGN-cross-session-communication.md`; no new safeguards).
- Transcript editing or splicing (the user attaches and continues; they
  do not surgically edit prior conversation).
- Programmatic / MCP-based attach (this is a human-driven CLI feature).
- A new MCP tool for attach (existing tool extensions are sufficient).
- Heartbeat protocol for detecting SSH-disconnected-but-still-alive niwa
  attach processes (SIGHUP-handler + `niwa session detach --force` cover
  the v1 cases).
- Forensic attach to `ended` sessions (worktree is gone after destroy;
  refused with a clear error).
- `abandoned` session attach semantics (status has no writer today;
  defer until a writer is added).
- Coordinator push-notification on attach state change (filesystem-visible
  state file is sufficient via polling).
- Fixing related issues #108/#109/#111/#112 (these are addressed by
  PR #115 / `docs/niwa-mesh-reliability`).
- Plugin installation, Claude Code config inheritance differences between
  attach and worker (this PRD assumes attach inherits the user's normal
  Claude Code config, the same as any direct `claude` invocation).

## Research Leads

The exploration already conducted 12 deep research investigations
(7 round-1 + 5 round-2). Their findings are at:

1. `wip/research/explore_session-attach_r1_lead-transcript-persistence.md`
2. `wip/research/explore_session-attach_r1_lead-state-model.md`
3. `wip/research/explore_session-attach_r1_lead-lock-semantics.md`
4. `wip/research/explore_session-attach_r1_lead-multi-user-safety.md`
5. `wip/research/explore_session-attach_r1_lead-coordinator-awareness.md`
6. `wip/research/explore_session-attach_r1_lead-discovery-ux.md`
7. `wip/research/explore_session-attach_r1_lead-adversarial-demand.md`
8. `wip/research/explore_session-attach_r2_lead-ux-cli-tone.md`
9. `wip/research/explore_session-attach_r2_lead-ux-peer-patterns.md`
10. `wip/research/explore_session-attach_r2_lead-ux-scenarios.md`
11. `wip/research/explore_session-attach_r2_lead-ux-mcp-surface.md`
12. `wip/research/explore_session-attach_r2_lead-transcript-failure-modes.md`

Synthesis lives in `wip/explore_session-attach_findings.md`. Decisions
log lives in `wip/explore_session-attach_decisions.md`. Crystallize
rationale lives in `wip/explore_session-attach_crystallize.md`.

The PRD does not need a fresh Phase 2 research round — it can draft
directly from these artifacts. The Phase 2 step in this auto run is a
no-op synthesis that points back at the explore work.

## Coverage Notes

- All 6 PRD coverage dimensions (Who, Current, Missing/Broken, Why now,
  Boundaries, Success criteria) were touched during exploration. The
  scope above reflects them concretely.
- The adversarial-demand finding ("not validated, not validated-as-absent")
  is documented as an explicit assumption in the PRD's Known Limitations
  section.
- The `--force` semantic asymmetry (attach: kill worker; detach: steal
  lock) is documented explicitly in the PRD to preempt the symmetry
  instinct.
