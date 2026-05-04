# Lead: Worktree and .niwa/ mesh state interaction

## Findings

### .niwa/ is instance-scoped, not workspace-scoped

Each instance directory has its own `.niwa/` containing `instance.json`, `daemon.pid`,
`roles/`, and `tasks/` directories. Discovery works via `DiscoverInstance()` which walks
up from cwd looking for `.niwa/instance.json`. Under a worktree-per-session model, each
worktree placed alongside the main clone would be its own instance root, with independent
`.niwa/` state. This eliminates race conditions from two sessions writing to the same
state store, but requires per-worktree daemon lifecycle and explicit coordinator-managed
session-to-worktree mappings.

### Mesh state is atomic-safe under sequential access

The task store uses flock + atomic rename for task state mutations (`UpdateState` acquires
`LOCK_EX`, mutates, writes to a `.tmp` file, fsyncs, renames into place, fsyncs again,
then unlocks). Role inboxes are watched by one daemon per instance. Shared `.niwa/` across
concurrent worktrees is feasible if the coordinator enforces sequential dispatch, or if
the flock protocol covers all access patterns — but per-worktree isolation is cleaner and
eliminates the risk entirely.

### Daemon ownership is tightly bound to instance lifecycle

The daemon is spawned per-instance by `niwa apply --channels` and killed by `niwa destroy`.
Under worktree-per-session, either each worktree runs its own daemon (adding per-session
lifecycle complexity), or a single daemon must watch multiple instance roots (requiring a
registry to route inbox events to the correct session). The coordinator must explicitly
track session→worktree→daemon mappings if parallel sessions are desired.

### Hooks and settings under worktrees

Claude Code hooks live in `.claude/settings.json` at the instance root. All worktrees
of the same repo share the same instance root, so they share hook configuration. Hooks
are stateless (read-only scripts) and tagged with `NIWA_TASK_ID` env var, so parallel
execution from multiple worktrees is safe — each worker's stop hook writes to its own
task state directory.

## Implications

The two viable models are:

1. **Per-worktree instance**: each worktree gets its own `.niwa/`, its own daemon, and
   independent task state. Clean isolation, no race conditions. Requires daemon lifecycle
   per session and a coordinator-managed mapping of session→worktree→daemon.

2. **Shared instance, isolated tasks**: all worktrees share the main clone's `.niwa/`,
   relying on flock-based atomicity for concurrent access. Simpler operationally, but
   requires the coordinator to ensure no two sessions write to the same role inbox
   simultaneously.

Option 1 (per-worktree instance) is safer and aligns with the existing daemon ownership
model. Option 2 is viable but introduces a coupling that the current design doesn't need.

## Surprises

The existing daemon model is already "one daemon per instance root." Worktrees as
independent instance roots maps cleanly onto this — there is no architectural mismatch,
just operational complexity in managing more daemon processes.

## Open Questions

1. Should niwa provide a `niwa session start` / `niwa session end` command to manage
   worktree+daemon lifecycle, or should the coordinator do this directly via git CLI?
2. If per-worktree daemons, how does the coordinator's `niwa_ask` routing work? Workers
   in worktree daemons need to find the coordinator's live session in the main daemon's
   `sessions.json`.
3. Is a single-daemon multi-root model worth exploring, or is per-worktree-daemon simpler?

## Summary

`.niwa/` is instance-scoped, so each worktree placed as an independent instance root gets
its own daemon, task store, and inbox — cleanly avoiding race conditions at the cost of
per-session daemon lifecycle overhead. The coordinator must track session→worktree→daemon
mappings explicitly, since niwa has no cross-instance coordination layer today. The key
architectural question is whether niwa should provide a `session` command abstraction to
manage this complexity, or leave it entirely to coordinator skills.
