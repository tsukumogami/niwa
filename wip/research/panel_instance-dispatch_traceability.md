# Traceability Audit

**Verdict:** PASS

Both prior blocking gaps are fixed and verified against the current files: R39/R40 now trace PRD->DESIGN->PLAN, R36/R37 have an implementing PLAN AC, and the phantom-D9 citation now resolves to a real decision.

## Blocking Findings

1. none

## Non-Blocking Notes

1. **R29 (reclaim after stop/delete) still under-tested in PLAN.** DESIGN's teardown narrative covers it (stop/delete drives the job state non-live, the same reaper path as R28/R30); PLAN Issue 6 still fabricates only `done` (R28) and past-TTL (R30) job states, not a stop/delete-driven non-live state. Same code path as the covered cases, so substantially covered; only the dedicated case is absent. Carried over unchanged from the prior audit, not a regression.
2. **Adequately-covered uncited requirements (unchanged).** R4 (ephemeral-mode independence -- exercised by [offline] criteria with the master switch off), R15 (prompt as initial prompt -- D8/launcher argv, Issues 2/4), R18 (short-id scrape -- DESIGN D3 deliberately subsumes it under R19/R21 cwd-correlation), R26 (mapping-write-failure -- folded into Issue 4's R32-R35 rollback matrix), R44 (no new dependency -- suite runs in existing CI), R45 (bounded wait -- Issue 3 AC "bounded timeout returns a capture failure, not a hang"). Traceable without an explicit R# cite.

## Coverage summary

All R1-R46 present in the PRD (irregular order: R42-R45 follow R46; no number missing or duplicated). After the fixes, PLAN ACs explicitly cite 39 of 46 requirements; the 7 not cited (R4, R15, R18, R26, R29, R44, R45) are all adequately covered indirectly. The two previously-blocking gaps are closed and verified:

- **R39 + R40:** DESIGN now has a "Hook-path coexistence (R39, R40)" subsection (lines 202-213) tying the re-entrancy no-op to the dispatch-created instance being a valid, indistinguishable niwa instance via `realProvisionInstance`; PLAN Issue 6 adds an AC asserting the existing SessionStart hook no-ops against a dispatch-created instance, citing **(R39, R40)**. Traceable PRD->DESIGN->PLAN.
- **R36 + R37:** PLAN Issue 4 adds a concurrency AC -- N dispatches produce N distinct instances/mappings, state file parses cleanly, exactly N ephemeral mappings -- citing **(R36, R37)**. The DESIGN already architected this via D2.

DESIGN decisions: D1-D9 all defined and each reflected in the PLAN. **D9 ("Test seams") is now a real decision** in Considered Options (lines 190-200), so PLAN Issue 6's `(D9)` citation resolves (phantom-D9 finding cleared). D1 Issue 4, D2 Issue 4, D3 Issues 1/3, D4 Issues 4/5, D6 Issue 1, D7 Issues 2/4, D8 Issue 2, D9 Issue 6; D5 realized via D4's backstop scan in Issue 5.

Side fixes confirmed: PLAN Issue 4's "inside-repo" wording no longer implies a distinct `ClassifyCwd` class (now "a repo checkout classifies as inside-instance or inside-worktree"), and an explicit R12 opportunistic-reap AC was added to Issue 4. No in-scope BRIEF/PRD-out-of-scope item is silently pulled in, and no PLAN AC introduces new scope beyond the PRD/DESIGN.
