---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-worktree-env-provisioning.md
milestone: "Worktree environment provisioning"
issue_count: 4
---

# PLAN: worktree environment provisioning

## Status

Active

## Scope Summary

Make `niwa worktree create`/`apply` inherit the instance clone's
already-materialized environment instead of resolving secrets, and make `niwa
apply` the single refresh that keeps every clone and worktree consistent.
Implements DESIGN-worktree-env-provisioning (decisions A1 copy-clone-bytes and
B2 clone-then-inherit fan-out).

## Decomposition Strategy

**Walking skeleton.** The change is an integration across three seams -- the CLI
worktree path (`applyContentToWorktree`), `internal/workspace` materialization
(`ApplyToWorktree` / the new inherit primitive), and worktree enumeration in the
`internal/worktree` leaf. Issue 1 is a thin end-to-end slice: a standalone
`niwa worktree create` inherits the clone's env with the resolution fork
removed. It exercises the full path (config target resolution -> copy ->
git-exclude posture) and is itself the fix for the reported failure, so the
integration risk surfaces first. The remaining issues thicken: the `niwa apply`
fan-out to existing worktrees (Issue 2), the settings/files secret-ref audit
(Issue 3), and user docs (Issue 4).

## Issue Outlines

### Issue 1: feat(worktree): inherit instance env instead of resolving

**Goal**: Add an `inheritEnvOutputs` primitive in `internal/workspace` and wire
`niwa worktree create`/`apply` to inherit the clone's resolved env target
file(s) by byte-copy, removing live secret resolution from the worktree path.

**Acceptance Criteria**:
- [ ] `applyContentToWorktree` no longer calls `resolve.BuildBundle` (x2) or
  `ResolveAndMergeEffectiveConfig`; the vault-free `mergeWorktreeOverlay` stays,
  and the global env_output rung is read from the parsed global override.
- [ ] `ApplyToWorktree` no longer runs `EnvMaterializer`; it calls the inherit
  primitive, which resolves targets via `config.EffectiveEnvOutput` and copies
  bytes from `<instanceRoot>/<group>/<repo>/<target>` to the worktree target.
- [ ] Worktree env output is byte-identical to the clone's for dotenv, json, and
  shell formats and for multiple/custom target names (R2, R3).
- [ ] Both the clone source path and the worktree dest path pass through
  `safeTargetPath`; files written at 0600; for custom (non-`*.local*`) names the
  primitive asserts git-exclude BEFORE writing and refuses on a non-git tree
  (`IsGitRepo`) (N3).
- [ ] `niwa worktree create` succeeds offline / with a broken or wrong-org
  secret-source session and performs no secret-provider call (R1, R4, N1).
- [ ] When a configured env target is absent from the clone, create exits
  non-zero with an error naming `niwa apply` (R8); the "repo has no env at all"
  case is distinguished and is not an error.
- [ ] `niwa worktree apply` on an existing worktree re-syncs its env via the same
  inherit primitive (no resolution) and matches the clone (R5).
- [ ] A `@critical` functional scenario covers offline create matching the clone.

**Dependencies**: None

**Type**: code
**Files**: `internal/workspace/worktree_content.go`, `internal/cli/session_lifecycle_cmd.go`

### Issue 2: feat(apply): refresh existing worktrees in one pass

**Goal**: Add a post-clone worktree-refresh step to `runPipeline` that runs the
inherit primitive for every live worktree, with edge-state skip-with-warning and
the managed-file forward-carry invariant.

**Acceptance Criteria**:
- [ ] After the clone materializer loop, apply enumerates
  `worktree.ListSessionLifecycleStates` and includes a worktree only when it is
  `active` AND in `repoIndex` AND its dir exists AND not attached
  (`ReadAttachState(..., reapStale=false)`) AND git-registered; each included
  worktree is refreshed via the inherit primitive (R6).
- [ ] A locked, detached, or missing-dir worktree is skipped with a warning
  naming it; the apply still succeeds (R7).
- [ ] A skipped-but-live worktree's prior managed-file entries are carried
  forward into `result.managedFiles` so `cleanRemovedFiles` does not delete its
  live secret file; an absent (destroyed) worktree's entries are pruned.
- [ ] After rotating a secret and running `niwa apply`, a pre-existing worktree
  shows the new value and equals the clone (R6, N2 chain).
- [ ] Functional scenarios cover rotation-refresh and skip-of-locked/missing.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/workspace/apply.go`

### Issue 3: fix(worktree): handle secret refs in settings/files materializers

**Goal**: Audit whether the settings/files materializers can carry `vault://`
refs that the removed standalone resolution previously handled; extend
inherit-by-copy to their outputs if so, or add a guard plus a test if not.

**Acceptance Criteria**:
- [ ] The audit result is recorded (do claude.settings / files keys resolve
  `vault://` in the standalone worktree path?).
- [ ] If secret-bearing settings/files exist: the standalone worktree path emits
  resolved values matching the clone (no literal `vault://` written), via the
  same inherit-by-copy treatment.
- [ ] If none exist: a test asserts the standalone path writes no unresolved
  `vault://` into settings/files outputs, documenting the no-op.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/workspace/worktree_content.go`, `internal/workspace/materialize.go`

### Issue 4: docs(worktree): document env inheritance and apply-refresh

**Goal**: Update the worktree guide to describe that worktree create inherits the
instance environment, that `niwa apply` refreshes all worktrees, and the
create-before-first-apply behavior.

**Acceptance Criteria**:
- [ ] `docs/guides/worktree.md` states worktree create/apply inherit the
  instance's materialized env and perform no secret resolution.
- [ ] The guide states `niwa apply` refreshes existing worktrees alongside
  clones, and names the create-before-apply (R8) error and its remedy.
- [ ] No working-scratch paths referenced; writing-style clean.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>

**Type**: docs
**Files**: `docs/guides/worktree.md`

## Dependency Graph

## Implementation Sequence

- **Critical path:** Issue 1 -> Issue 2 -> Issue 4. Issue 1 is the skeleton and
  the prerequisite for everything; it also resolves the reported failure on its
  own.
- **Parallelizable:** once Issue 1 lands, Issue 2 and Issue 3 are independent of
  each other and can proceed in parallel.
- **Last:** Issue 4 (docs) after Issue 1 and Issue 2 settle the behavior it
  documents.
