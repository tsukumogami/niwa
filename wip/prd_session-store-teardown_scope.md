# /prd Scope: session-store-teardown

## Problem Statement
Developers running many dispatched sessions in one workspace hit two failures
in niwa's session records. `niwa worktree destroy` can't resolve the ids a
developer holds for a dispatched session (the Claude session UUID or its 8-hex
handle), so the merged-branch guard never runs before `niwa reap` deletes the
instance (#292). Concurrent provisioning refreshes the shared workspace-root
config dir through one fixed staging path and a copy-then-swap carry-over, so a
provision can fail with a missing staging path or silently lose a session
mapping (#297).

## Initial Scope
### In Scope
- Which ids `worktree destroy` accepts, how each resolves to an instance and
  its worktree records, and what it reports when there is nothing to destroy
  or the id matches nothing.
- Keeping `worktree destroy <8hex-worktree-id>` inside an instance working.
- Concurrent config-dir refreshes (dispatch, create, apply, ephemeral-session
  hook) not failing because of each other.
- No session mapping lost or resurrected across a concurrent refresh; the
  same for a concurrent reap delete.
- Tests that fail on today's main for both.

### Out of Scope
- Merged-branch checks for Claude-native worktrees and instance-clone branches
  (stated as a limitation, not fixed).
- Redesigning reap or the ephemeral-session lifecycle.
- #74's convention-aware fetch.
- #265, #279, #264, #278, #283-#285.
- Files written into the config dir by tools other than niwa.

## Research Leads
1. Id resolution for teardown: every id form a developer or script can hold
   (from `claude agents --json`, `niwa list`, the dispatch output), how each
   maps to a workspace-root mapping and then to an instance's lifecycle
   records, the 8-hex handle/worktree-id collision, what other lifecycle
   readers (`go`, `attach`, `detach`, `apply`, from-hook remove) share the
   resolveInstanceRoot conflation, and existing destroy tests.
2. Concurrent refresh surface: every niwa code path that writes or deletes
   under the workspace-root `.niwa/` (mappings, instance.json, briefs,
   provenance marker, disclosed notices) and whether it can land inside
   another process's refresh window; existing flock helpers and non-unix
   fallbacks; functional-test support for a file:// config source and
   parallel runs; whether the race reproduces deterministically in a unit test.

## Coverage Notes
- Who: developers and cleanup scripts reclaiming workers; developers launching
  parallel dispatches. Current situation and gap: measured and verified during
  /scope discovery. Why now: parallel dispatch is routine and the cleanup
  sequence relies on destroy. Success: the two tests named in the task brief.
- Open: whether `apply` at the workspace root also re-writes instance.json in
  the root config dir during a refresh; whether reap/list readers need the lock.
