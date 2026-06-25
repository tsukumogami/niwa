# Consistency & Architecture Review

**Verdict:** PASS

The four documents agree, the orphan-window invariant survives intact into the PLAN, and every code reference the PLAN makes resolves against the actual niwa codebase.

## Blocking Findings
1. none

## Non-Blocking Notes

1. **"Inside-repo" is descriptive, not a distinct class.** PLAN Issue 4 AC (line 113-115) lists "workspace-root, inside-instance, inside-worktree, and inside-repo all resolve to the enclosing workspace root." `ClassifyCwd` (internal/workspace/cwd_classify.go) has only four classes: `CwdInsideInstance`, `CwdAtWorkspaceRoot`, `CwdInsideWorktree`, `CwdOutside` — there is no `CwdInsideRepo`. This is not a defect: a repo checkout inside an instance/worktree resolves through `DiscoverInstance`/`config.Discover` and yields a non-empty `WorkspaceRoot`, and the dispatch command only consumes `class.WorkspaceRoot` (exactly as `runReap` already does). The PRD's R7/R9 phrasing ("worktree or a repo checkout") is satisfied. The PLAN should not imply a fourth resolving class exists, but the behavior it asserts is achievable.

2. **Opportunistic-reap trigger is dispatch's responsibility, not inherited.** `reapOpportunistically` is called inside `runCreate` (create.go:141), but dispatch calls `realProvisionInstance`/`applier.Create` directly, which does NOT call it. The DESIGN ("Triggers the existing opportunistic reap the way `runCreate` does") and PLAN Issue 4/5 correctly treat this as something dispatch must invoke itself. Worth an explicit AC line in Issue 4 that dispatch calls `reapOpportunistically` (R12), since the create path it reuses does not bring it along for free. Non-blocking because the requirement is captured (R12, Issue 5 AC line 152) and the function is public-in-package.

3. **R38 young-instance protection depends on the opportunistic reap running before the current dispatch's own instance is created OR on the TTL gate.** Either way it holds: `runCreate` reaps before creating, and the backstop's TTL gate spares any young marked instance regardless of ordering. PLAN Issue 5 AC explicitly tests marked+unmapped+young -> spared. No window is left open.

## Summary
All four docs hold the additive promise consistently: the BRIEF/PRD scope the hook path (niwa#171/#172) as untouched, the DESIGN's D4/D5 add the reaper backstop as a *separate* scan (not a modification of `selectReapTargets`/`sessionLive`), and PLAN Issue 5 modifies reap.go additively with an AC asserting existing reaper behavior and tests stay green. The core orphan-window invariant — write mapping BEFORE removing the marker, backstop reaps only mapping-absent+marker+old — is preserved verbatim in PLAN Issue 4 (write mapping then remove marker) and Issue 5 (reap only on mapping-absent AND marker-present AND age>TTL), and the R38 young-in-flight protection is grounded in the verified fact that `EnumerateInstanceRecords` derives `Ephemeral` solely from the mapping store, so an unmapped orphan is `Ephemeral:false` and the backstop must be a separate scan. Every reuse claim (realProvisionInstance with customName branch, destroyInstanceFunc, WriteSessionMapping, ValidSessionID, ClassifyCwd, the sessionattach `cmd.Dir` supervisor, selectReapTargets/reapWorkspace, job_state.go's struct) resolves to real code with the claimed shape, and the 6-issue horizontal decomposition is correctly ordered, correctly dependency-gated, and each layer is independently unit-testable, realistic for one PR.
