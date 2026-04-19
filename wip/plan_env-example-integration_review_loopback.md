---
review_result:
  verdict: "loop-back"
  loop_target: 4
  round: 1
  confidence: "high"
  critical_findings:
    - category: "B"
      description: "Issue 4 step 5 contains a contradictory qualifier: 'The exclusion set also walks Effective.Repos[ctx.RepoName].Env.Secrets.{...} for every repo entry.' The indexed access [ctx.RepoName] means only the current repo's override, but 'for every repo entry' implies iterating all repos. The design doc's data flow (step 5) specifies walking the current repo's config layer only. An implementer following 'for every repo entry' would build an exclusion set that incorrectly includes secrets declared for other repos."
      affected_issue_ids: [4]
      correction_hint: ""
    - category: "C"
      description: "Issue 1: Pattern 6 (interface name drift) — `effectiveReadEnvExample` is declared with signature `(ws WorkspaceMeta, repoName string) bool`, but WorkspaceMeta carries no per-repo overrides (those live on WorkspaceConfig.Repos), so the function cannot implement per-repo override. Issue 4 calls it as `effectiveReadEnvExample(ctx.Effective, ctx.RepoName)` where ctx.Effective is EffectiveConfig, not WorkspaceMeta. A correct implementation using either WorkspaceConfig or EffectiveConfig would fail Issue 1's stated signature AC."
      affected_issue_ids: [1, 4]
      correction_hint: "Reconcile the function signature to use a type that carries both workspace-level and per-repo ReadEnvExample fields (e.g., the full WorkspaceConfig or EffectiveConfig). Update both Issue 1's signature AC and Issue 4's call-site AC to name the same type."
    - category: "C"
      description: "Issue 1: Pattern 4 (state-without-transition) — The TOML round-trip tests cover workspace=false, per-repo=true overriding workspace=false, and all-nil → true. The suppression direction is untested: workspace=true with per-repo=false (repo opts out while workspace enables). A wrong implementation that always returns the workspace value would pass all three stated tests."
      affected_issue_ids: [1]
      correction_hint: "Add a fourth test: workspace read_env_example = true, per-repo read_env_example = false must resolve to false. This covers the suppression direction and catches an implementation that ignores per-repo override."
    - category: "C"
      description: "Issue 3: Pattern 2 (mock-swallowed) — The AC phrases 'every prefix in envPrefixBlocklist' and 'every pattern in envSafeAllowlist' imply iterating the implementation's own variable to generate test cases. A wrong implementation with a shorter blocklist (e.g., missing ASIA or github_pat_) would still pass all tests because the test table is derived from the same slice the implementation declares. Tests cannot detect missing entries."
      affected_issue_ids: [3]
      correction_hint: "Require each specific prefix and allowlist pattern to be hard-coded by name in the test table (not range-iterated from the implementation variable). The test file should contain a literal check for each of the 16 blocklist prefixes and each allowlist pattern, so a shortened implementation variable causes test failures."
    - category: "C"
      description: "Issue 4: Pattern 7 (existence-without-correctness) — 'apply.go sets EnvMaterializer.Stderr = os.Stderr' tests only that the field is assigned. The stderr() helper falls back to os.Stderr when nil, so setting it explicitly vs. leaving it nil produces identical observable behavior. An implementation that omits the assignment satisfies every other AC without failing this one."
      affected_issue_ids: [4]
      correction_hint: "Replace with an AC that tests warning-routing behavior: inject a test io.Writer into EnvMaterializer.Stderr and assert that symlink/parse/classification warnings reach that writer rather than going to os.Stderr. This tests the actual contract, not the assignment."
    - category: "C"
      description: "Issue 5: Pattern 3 (happy-path only — missing enumerateGitHubRemotes error path) — No AC covers what apply does when enumerateGitHubRemotes itself fails (e.g., ctx.RepoDir is not a git repo, or the remote URL is malformed). A wrong implementation that panics or returns a misleading error on that failure would pass all stated ACs."
      affected_issue_ids: [5]
      correction_hint: "Add an AC: when enumerateGitHubRemotes returns an error for a repo, apply emits a warning and skips the guardrail check for that repo (or documents and tests the intended behavior), so the failure mode is defined and testable."
    - category: "C"
      description: "Issue 5: Pattern 2 (mock-swallowed) — The AC 'private-remote repo with high-entropy key → fail with classification error, no guardrail error' requires distinguishing classification error from guardrail error. If the test only checks err != nil, a wrong implementation that unconditionally fires the guardrail would produce two errors but still pass because the error is non-nil."
      affected_issue_ids: [5]
      correction_hint: "Require the test to assert the error message does NOT contain guardrail language (e.g., 'public remote') and DOES contain classification language (e.g., 'probable secret'). Alternatively, assert on the error count or type, or use a captured stderr buffer to verify exactly which messages were emitted."
    - category: "C"
      description: "Issue 6: Pattern 7 (existence-without-correctness) — 'SourceKindEnvExample constant is defined' has no assertion about its value. An implementation that sets SourceKindEnvExample equal to an existing constant (e.g., SourceKindVault) would define the constant and satisfy this AC while silently corrupting vault attribution in niwa status --verbose output."
      affected_issue_ids: [6]
      correction_hint: "Add an AC asserting SourceKindEnvExample has a unique string value distinct from all existing SourceKind constants, and that niwa status --verbose displays specifically '.env.example' (not a vault or plaintext label) as the source for keys originating from .env.example."
  summary: "Loop-back required at Phase 4. Eight findings across Category B (1) and Category C (7): an AC wording contradiction in Issue 4, a function signature inconsistency between Issues 1 and 4, a missing suppression-direction test in Issue 1, a mock-swallowed blocklist test in Issue 3, an existence-only AC in Issue 4, two error-path gaps in Issue 5, and a value-uniqueness gap in Issue 6. All are fixable by regenerating the affected issue bodies."
---

# Plan Review: env-example-integration

Round 1 review result: loop-back at Phase 4.

Eight findings across Categories B and C require issue body corrections. All are fixable at Phase 4 (regenerating affected issue bodies). The design doc is sound; all findings are in generated issue body text.
