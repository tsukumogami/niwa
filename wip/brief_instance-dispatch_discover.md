# Brief Discover: instance-dispatch

Visibility: Public. Auto-mode under /scope parent orchestration. Discovery is
grounded in the prior /explore (wip/explore_ephemeral-session-fixes_findings.md),
not a fresh conversation.

## Problem / Outcome pair

- Problem: fanning out Claude Code background agents from a niwa workspace has no
  reliable one-step path to put each worker in its own fully-configured instance.
  The automatic hook path can't deliver instance config (cd can't re-root) and
  mis-targets workers; the manual pre-create-then-launch path works but is tedious
  and error-prone.
- Outcome: one niwa command dispatches a background worker that boots rooted in a
  fresh ephemeral instance (full settings/plugins/hooks/env), appears in Agent View
  for management, and is reclaimed automatically.

## Grounding facts (from explore)

- Hook can't re-root; dispatch-time cwd is the only lever.
- `claude --bg` from inside an instance: full fidelity + Agent View registration.
  User-verified end-to-end.
- Reuses applier.Create, WriteSessionMapping, reapWorkspace, session-attach supervisor.
- `claude --bg`: detach+return; scrape `backgrounded · <short-id>` then jobs-dir for
  full UUID; argv-only prompt; settings from launch cwd.
- Teardown reaper-primary (root SessionEnd hook doesn't fire for instance-rooted).

## Scope boundary anchor

NET-NEW, ADDITIVE. Existing hook path (#171/#172) untouched. Both coexist.

## Corner cases to name (frame in brief; PRD specifies)

launch location (root / instance / worktree / repo / unrelated dir); cleanup paths
(normal end, `claude stop`, Agent-View delete, crash, reboot); id-capture failure /
ambiguity under concurrency; instance-naming race; worker dispatching sub-workers;
argv-only prompt limits; partial-failure orphans + reap reclamation.

## Artifact decision

Produce a durable BRIEF (the feature is net-new and feeds a full PRD->DESIGN->PLAN
chain). Not a pass-forward.
