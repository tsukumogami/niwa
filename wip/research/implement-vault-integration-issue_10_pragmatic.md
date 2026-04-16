# Pragmatic Review — Issue 10 (CLI flags + status subcommands + bootstrap pointer)

Commit `26655fab4938a752d2867390851f5b74f650a022` on `docs/vault-integration`.

Scope reviewed:
- `internal/cli/apply.go` + `apply_test.go`
- `internal/cli/init.go` + `init_test.go`
- `internal/cli/status.go`
- `internal/cli/status_audit.go` + `status_audit_test.go`
- `internal/cli/status_check_vault.go` + `status_check_vault_test.go`
- `internal/workspace/apply_vault_test.go`

## Summary

Production code is largely proportionate to the AC (two flags on `apply`, two subcommands on `status`, one stderr pointer from `init`). Helpers in `status_audit.go` and `status_check_vault.go` are pulling their weight — each has a clearly delineated responsibility (classify, collect, print, load-shadows; resolve-source, detect-rotations, parse-id, print). Two small dead-weight items in `status_audit.go`. Test surface is adequate and not meaningfully redundant; one trivially-pure helper test is advisory-only.

## Blocking findings

None.

## Advisory findings

### A1. Dead `classResolved` branch in `classifyMaybeSecret`
`internal/cli/status_audit.go:179-190` — the author's own comment on lines 176-178 states: "in the normal CLI path cfg is pre-resolve and classResolved is unreachable." The `if ms.IsSecret()` branch and the `classResolved` constant exist but are provably unreachable from `runAuditSecrets` (which calls `config.Load` and never runs the resolver). A unit test (`TestClassifyMaybeSecret`) also never exercises it. Either remove the constant + branch, or add a comment-only guard saying "kept for future post-resolve audits." Advisory because the dead path is small and inert.

### A2. Identical branches in `showSummaryView`
`internal/cli/status.go:160-162` — `driftLabel := "drifted"` followed by `if status.DriftCount == 0 { driftLabel = "drifted" }`. Both branches assign the same string. Collapse to the single assignment (or pick the intended singular/plural). Not introduced by Issue 10 — pre-existing — but it sits directly in the file touched by this issue. Advisory.

### A3. `TestApplyCmd_AllowFlagsThreadToApplier` asserts cobra's own behavior
`internal/cli/apply_test.go:108-133` — exercises `applyCmd.ParseFlags([...])` and asserts the package-level bools flip. This re-verifies cobra's flag-binding rather than niwa code. The adjacent `TestApplyCmd_HasAllowMissingSecretsFlag` / `TestApplyCmd_HasAllowPlaintextSecretsFlag` already lock in the flag registration. The "thread to applier" claim in the test name isn't actually verified (runApply is never called). Consider removing or rewriting to invoke `runApply` with a stub Applier. Advisory — low-cost test, not harmful.

## Things I checked and did NOT flag

- **`sortedSecretKeys` (`status_audit.go:196`)** — a 6-line helper used once, but the comment explains why the cross-package helper isn't borrowed (cli → workspace forbidden direction). Keeping the local copy is the right call; inlining into `collectAuditEntries` would hurt readability. Pulls its weight.
- **`loadShadowsForAudit` (`status_audit.go:85`)** — single-caller, but the nil-on-any-error semantics is load-bearing (audit must not fail when no instance state exists). The helper name documents that intent better than inline code. Pulls its weight.
- **`refFromSourceID` / `detectVaultRotations` / `printVaultRotations`** — each has a single responsibility and each is exercised by a named test. The cache inside `detectVaultRotations` is the dedup contract the docstring promises. No over-decomposition.
- **`vaultKindsDeclared` + `bootstrapCommandFor`** — two tiny helpers feeding `emitVaultBootstrapPointer`. Splitting sort/dedup from kind→command mapping is the minimum viable shape for a v1 switch with a known future extension (sops et al.). Not speculative; both are reachable and tested.
- **16 tests across the three new test files** — I counted redundancy carefully:
  - `status_audit_test.go` (7 tests): classify (unit), walk-all-tables, shadowed-column, exit-non-zero-with-vault, exit-zero-without-vault, exit-zero-happy-path, shadowed-reads-state. Each asserts a distinct contract; the exit-behavior split is justified (three different guardrail states).
  - `status_check_vault_test.go` (5 tests): parseSourceID (unit), rotated-reports, identical-no-change, default-offline-invariant, malformed-id. Distinct contracts.
  - `init_test.go` adds 4 vault-bootstrap tests: infisical-specific, unknown-kind fallback, no-vault no-op, dedup+sort. Each covers a different branch. Not redundant.
  - `apply_test.go` adds 3 flag tests; A3 above is the one I'd merge, but they aren't duplicative — `HasFlag` tests are registration, `ThreadToApplier` is parse-behavior.
- **Apply flag plumbing** — two struct fields on `Applier` for two CLI flags, straightforward mapping. Integration coverage lives in `internal/workspace/apply_vault_test.go` and `internal/guardrail/githubpublic_test.go`. No speculative knobs.
- **`emitVaultBootstrapPointer` only fires in `modeClone`** — deliberate (scaffolded template is commented-out examples). Documented in the call site comment. Not a bug.

## Verdict

Ship. The two advisory items (A1 dead branch, A2 pre-existing identical-branches typo) are worth a follow-up commit if convenient but don't gate the PR.
