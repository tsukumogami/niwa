# Exploration Decisions: mesh-session-lifecycle

## Round 1

- **Skip standalone session ID threading**: Threading `resume_session_id` through
  `niwa_delegate` would fix context loss cheaply, but it doesn't address dirty workspace
  or parallel sessions, and it creates migration debt when worktrees land. The full session
  model subsumes session continuity — dropping the standalone quick-fix in favor of
  designing the complete model from the start.

- **Design for universal scope, implement mesh-first**: Non-mesh commands are already
  layout-agnostic (one-line fix aside), and non-mesh users have the same stranded-branch
  problem. The PRD should not artificially scope to mesh-only, but the first implementation
  target is mesh because that's where the acute pain is.

- **Per-worktree daemon model**: Each session worktree becomes its own niwa instance with
  its own daemon. This maps cleanly onto the existing "one daemon per instance" model and
  avoids cross-instance coordination complexity. The shared sessions registry
  (`.niwa/sessions/sessions.json` in the main clone) enables `niwa_ask` routing across
  worktree daemons.

- **Four session lifecycle states**: `active`, `pending_merge`, `ended`, `abandoned`.
  `pending_merge` prevents premature worktree cleanup when a PR is open but not yet merged.
  `abandoned` is the force-end path for unpushed work the user explicitly discards.

- **Reserve follow-on extension points in SessionState**: `pr_url` field (PR tracking),
  `coordinator_session_id` in SessionEntry (handoff + crash recovery). These are zero-cost
  to reserve now and unlock high-value follow-ons without requiring a schema migration later.

- **Session summary on-demand, not pre-materialized**: `niwa_session_summary` queries
  existing task state at call time. Simpler and always current; avoids stale data from
  pre-materialization. Design the transitions.log query path to avoid exposing redacted
  progress body fields.

## Round 2 (PRD Phase 2 research)

- **No implicit sessions on untagged niwa_delegate**: `niwa_delegate` without a `session_id`
  continues to spawn a fresh Claude session, exactly as today. Backward compatible; sessions
  are opt-in. Creating implicit anonymous sessions would silently change existing coordinator
  behavior and make it hard to distinguish session-aware from legacy delegations.

- **Shared sessions registry via symlink from worktree daemons**: Workers in session worktrees
  need to reach the coordinator via `niwa_ask`. The mechanism is a shared `.niwa/sessions/sessions.json`
  readable by all worktree daemon instances. Implementation detail (symlink vs. explicit path
  resolution) deferred to design doc. PRD requires the routing to work.

- **Populate SessionEntry.ClaudeSessionID at coordinator registration**: The field already
  exists in types.go but is never written. Filling it at `maybeRegisterCoordinator` time
  enables crash recovery — a new coordinator can find orphaned sessions and optionally resume
  from the prior coordinator's session. Low-cost, high-value for resilience.

- **No auto-pruning of orphaned sessions**: When a coordinator crashes, the daemon surfaces
  orphaned sessions (dead PID) via `niwa_list_sessions` but does not clean them up automatically.
  Orphaned sessions may contain unpushed work; auto-pruning risks data loss. User or new
  coordinator explicitly decides to reclaim or abandon.

- **niwa apply does not update session worktrees**: `niwa apply` continues to operate on the
  main clone only. Session worktrees are managed by session lifecycle (create/end), not by apply.
  Mixing apply semantics with session management would make worktree state harder to reason about.
