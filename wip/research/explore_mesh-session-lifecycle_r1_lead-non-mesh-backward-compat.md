# Lead: Non-mesh workspace backward compatibility

## Findings

### Non-mesh commands are layout-agnostic

`niwa go`, `niwa apply`, and related workspace commands are fundamentally stateless
navigation and configuration-application commands. They discover workspace and instance
state via filesystem walks for `.niwa/workspace.toml` and `.niwa/instance.json`, using
`os.Stat()` — not git introspection. The only git-touching code is in
`snapshotwriter.dotGitExists()`, which has the one-line worktree bug noted in the command
compatibility audit. Everything else works identically whether run from a standard checkout
or a worktree.

### Non-mesh users benefit from worktrees too

The "stranded on feature branch" problem (`niwa apply` skips repos on non-default branches)
affects non-mesh users equally. A worktree model — where the main clone always stays on
`main` and active work happens in worktrees — would fix this for everyone, not just mesh
sessions. However, non-mesh users have no coordinator to manage session lifecycle, so the
worktree creation and cleanup would need either a `niwa session` CLI command or manual git
worktree commands.

### Two divergence models

**Mesh-only worktrees:**
- Non-mesh workspaces continue using the current single-checkout model
- Worktrees are created and managed only by the mesh coordinator via new MCP tools
- Two divergent code paths: one for worktree-anchored mesh sessions, one for flat checkouts
- Non-mesh users don't benefit from always-clean main
- Lower migration risk; existing non-mesh users are unaffected

**Universal worktrees:**
- All niwa instances use a worktree layout: main clone stays on `main`, all work happens
  in worktrees
- Single code path for workspace management
- Non-mesh users get always-clean main automatically
- Requires migration strategy for existing workspaces (existing checkouts on feature branches
  would need to be converted or left behind)
- Simpler long-term, more complex transition

### Migration complexity

The main migration concern is existing workspaces where repos are on feature branches.
A `niwa migrate` command could detect this state and offer to:
1. Create a worktree for the current feature branch (preserving work in progress)
2. Reset the main clone to `main`

This is relatively mechanical but requires user confirmation (destructive if done wrong).

### No functional test gap identified

The functional test suite in `test/functional/features/` covers workspace init, create,
and apply workflows. These scenarios use the `localGitServer` helper and test against
the actual binary. Since non-mesh commands are layout-agnostic, they would pass without
changes in a worktree layout.

## Implications

Universal adoption is strategically sound: the codebase is nearly worktree-compatible
today (one-line fix needed), and non-mesh users benefit as much from always-clean main
as mesh users do. The main cost is a migration strategy for existing workspaces, which
is well-scoped and mechanical.

Mesh-only adoption is simpler to ship first but creates divergence that needs to be
resolved eventually. If the universal model is the destination, it's better to start there.

The practical recommendation: design the session/worktree model with universal adoption
in mind, but gate it behind a config flag or migration command so existing users can
opt in explicitly rather than being broken on upgrade.

## Surprises

Non-mesh users have the same stranded-branch problem as mesh users, but it's been
invisible because there's no coordinator to surface it. Universal worktrees would fix
an existing pain point that users may not have named yet.

## Open Questions

1. Should `niwa apply` gain a `--worktree` flag to opt individual instances into the
   worktree model, or should adoption be all-or-nothing per workspace?
2. What happens to a non-mesh user who manually creates a worktree — does niwa handle
   it gracefully today (yes, with the one-line fix)?
3. Is a `niwa session` CLI command warranted for non-mesh users who want to manage
   worktrees without a coordinator?

## Summary

Non-mesh workspace commands are functionally compatible with worktrees today (one-line
fix aside) and non-mesh users share the same stranded-branch pain that motivates the
feature. Universal worktree adoption is strategically sound and simplifies the long-term
codebase, but requires a migration story for existing workspaces. Shipping mesh-only
first is viable as an incremental step, provided the design explicitly anticipates the
universal path.
