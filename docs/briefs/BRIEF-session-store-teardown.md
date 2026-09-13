---
schema: brief/v1
status: Accepted
problem: |
  Developers can't trust niwa's session records when tearing down a finished
  worker or starting several at once: teardown can't find the id they hold,
  so the merged-branch check never runs, and parallel provisioning can fail
  or silently drop the only handle back into a running worker.
outcome: |
  Teardown by the id a developer already has applies the merged-branch check,
  or says plainly there was nothing to check. Several dispatches launched at
  once each end up recorded and reachable, and none fails because of another.
motivating_context: |
  Developers reclaim finished workers with a fixed sequence: destroy the
  worker's worktrees, stop and remove the Claude session, then reap. Only the
  first step refuses to delete an unmerged branch, and today it can't resolve
  any id a developer has, so the sequence runs with its one safety check
  silently skipped. The same developers routinely launch dispatches in
  parallel, which is exactly when the second failure bites. Reported as
  issues #292 and #297.
---

## Status

Accepted

Framing for making niwa's session records dependable during worker teardown
and during concurrent provisioning. The two problems share one brief because
they share one cause and one place: both leave the workspace's session
records untrustworthy at a session's lifecycle boundaries, and both are fixed
in the same store. It stops at the problem, the outcome, the journeys, and
the scope boundary. The downstream PRD owns which ids teardown accepts and
the acceptance criteria; the DESIGN owns how ids resolve and how concurrent
refreshes are kept from colliding.

## Problem Statement

niwa keeps two kinds of session record, and a developer tearing down or
starting workers depends on both. Each dispatched session gets a mapping at
the workspace root that says which instance it became; it's the only durable
record of where that worker lives, and it's what `niwa list` and the reaper
read. Each niwa-managed worktree inside an instance gets its own lifecycle
record, which is what `niwa worktree destroy` reads before it removes the
worktree and deletes its branch only if that branch is merged.

At teardown those two records don't meet. The ids a developer actually holds
for a finished worker are the Claude session id and the short handle
`claude agents` shows, and neither is a worktree id. Every form the developer
can try fails. The step that would have refused to throw away an unmerged
branch is skipped, and the reap that follows deletes the instance, branch and
all. Nothing about the failure says the guard didn't run; a skipped check and
a passed check look the same from the outside.

At provisioning the mappings aren't safe either. Every way of creating an
instance can refresh the workspace's shared configuration directory, and the
mappings live inside that directory. Two provisions that run at once can step
on each other's refresh, so one of them fails outright. Worse, a mapping
written by one process while another is refreshing can vanish when the
refresh completes. Nothing fails in that case: the worker keeps running, but
nothing can find it any more, and the reaper can no longer tie its instance
to a session. Developers launch parallel dispatches as a matter of course, so
this isn't an edge case they can avoid.

## User Outcome

A developer cleaning up a finished worker runs worktree teardown with the id
they already have in front of them, from wherever they happen to be, and niwa
finds the worker's worktrees and applies the merged-branch check before
anything is deleted. When the worker has no niwa-managed worktree, the
developer is told that plainly instead of getting an error that looks like a
failed safety check.

A developer who launches several dispatches at once gets one working instance
and one recorded mapping per dispatch, and none of them fails because another
was provisioning at the same moment. Every worker they started stays
reachable through `niwa list` and resumable afterwards, and the reaper keeps
the join it needs between each instance and its session.

## User Journeys

### Reclaiming finished workers from the workspace root

A developer is clearing out workers whose PRs have merged. The trigger is a
list of finished sessions from `claude agents` and a terminal sitting at the
workspace root; they run worktree teardown for each worker, using an id that
list gave them, before stopping and reaping it. The outcome: each worker's
niwa-managed worktrees are found and torn down with the merged-branch check
applied, an unmerged branch is kept with a warning that names it, and a
worker with no such worktrees is reported as having nothing to tear down. The
reap that follows no longer runs past a check that silently didn't happen.

### A cleanup script working through a batch

An automated cleanup script reclaims every finished worker on a schedule,
running the same teardown, stop, remove and reap sequence without a person
watching. The trigger is the script reading session ids from `claude agents
--json` and passing them to teardown one after another. The outcome: the
script can tell from teardown's exit status and output whether a guard ran,
whether a branch was kept, or whether there was nothing to guard, so it never
reads a failed lookup as a clean result and never reaps past a kept branch
without the warning reaching its log.

### Destroying one worktree from inside an instance

A developer working inside an instance has finished with one of its
worktrees. The trigger is `niwa worktree list` showing the worktree's own id,
which they pass to teardown from inside the instance. The outcome, from the
developer's side: the habit they already have keeps working. The worktree is
torn down with the same guard as always, and they aren't asked to
disambiguate or retype anything.

### Launching several workers at once

A developer starts four pieces of work together, backgrounding four
`niwa dispatch --detach` commands in one shell line. Their workspace's
configuration comes from a source niwa re-materializes on every provision,
such as a local or self-hosted repository. The outcome: all four commands
succeed, four instances exist, and four mappings are recorded, each one
findable through `niwa list` and resumable. No run fails because another was
provisioning, and none quietly loses its mapping.

### Reaping while other work is still starting

A developer runs `niwa reap` to reclaim finished workers while other
dispatches they launched a moment ago are still provisioning in the same
workspace. The outcome: the reaped workers stay reaped, with none of their
mappings reappearing afterwards, and every live worker's mapping is still
there once the other dispatches finish.

## Scope Boundary

### In

- Resolving the ids a developer or script holds for a dispatched session to
  the niwa-managed worktrees that session's instance owns, wherever teardown
  is run from.
- Keeping teardown by a worktree's own id working as it does today.
- Telling the developer plainly, in a form a script can also act on, when a
  session has no niwa-managed worktree to tear down and when an id matches
  nothing.
- Every command that refreshes the workspace's configuration directory
  (dispatch, create, apply, the ephemeral-session hook) running concurrently
  with the others without failing because of another's refresh.
- Keeping the session mappings intact through a concurrent refresh: none
  lost, and none a reap removed brought back.

### Out

- A merged-branch check for branches niwa doesn't manage as worktrees:
  worktrees Claude Code creates on its own and branches in an instance's own
  repository clones. Reap still deletes those with the instance. This work
  makes teardown honest about what it covers rather than extending the
  check; widening it is a separate concern.
- Redesigning `niwa reap` or the ephemeral-session lifecycle beyond what
  these two fixes need.
- Separating configuration-source content from niwa's own local state inside
  the configuration directory, so refreshes no longer have to carry that
  state across. That's the longer-term convention-aware fetch in #74.
- Guaranteeing that files other tools write into the configuration directory
  survive a concurrent refresh. niwa can coordinate its own writers; a file a
  skill writes with its own tools is outside niwa's control.
- Worktree attach not resuming a conversation (#265) and push credentials
  for dispatched workers (#279). Both touch the same commands and neither is
  about the session records.

## References

- `docs/briefs/BRIEF-ephemeral-session-instances.md` introduces the
  session-to-instance mapping and the reaper that reads it.
- `docs/briefs/BRIEF-instance-dispatch.md` covers the dispatch feature whose
  mappings this work protects.
- `docs/guides/worktree.md` documents worktree teardown and its merged-branch
  behavior.
