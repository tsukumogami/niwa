# /brief Discovery: session-store-teardown

## Problem Candidate
A developer who runs many dispatched sessions in one workspace can't rely on
niwa's session records when tearing those sessions down or starting new ones in
parallel. The worktree teardown command can't find the record for the id the
developer actually holds (the Claude session UUID or its 8-hex handle), so the
one step that refuses to delete an unmerged branch never runs before the reaper
removes the instance. Separately, parallel provisioning in the same workspace
can fail with a missing staging path, or silently drop a session mapping,
which is the only handle back into a running worker.

## Outcome Candidate
A developer tearing down a finished worker can point the worktree teardown at
the id they have and get its merged-branch guard applied, or a clear statement
that there's nothing to guard. A developer launching several dispatches at once
gets every worker's mapping recorded and no provisioning failure caused by
another concurrent provision.

## Grounding Anchor
conversation only (task brief from the coordinator; issues #292 and #297 in this
public repo)

## Journey Sketch
- A janitor/developer cleaning up finished workers runs `niwa worktree destroy`
  with the session id or handle from `claude agents`, from the workspace root,
  before `niwa reap`.
- A developer inside an instance destroys one of its worktrees by its 8-hex
  worktree id (today's working path; must keep working).
- A developer fires four `niwa dispatch --detach` at once against a workspace
  whose config source re-materializes on every provision.
- The reaper (or `niwa list`) reads the mapping store while a refresh is
  swapping the config directory.

## Open Questions for Drafting
- Which ids `worktree destroy` accepts, and how an 8-hex value is disambiguated
  between a worktree id and a handle: PRD/design.
- Whether dispatch briefs (written by the /dispatch skill, not niwa) get the
  same protection as mappings: design limitation.
- The guard covers only niwa-managed worktrees; Claude-native worktrees and
  instance-clone branches remain unguarded before reap: must be stated as OUT.
