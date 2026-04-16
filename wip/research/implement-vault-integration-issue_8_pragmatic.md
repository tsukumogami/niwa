# Pragmatic Review — Issue 8 (shadow detection + diagnostics)

Commit: `5ffd02adbb00b899a32c7e4e4e90e7d182fefa62` on `docs/vault-integration`.

## Summary

Overall the commit is well-scoped to Issue 8's acceptance criteria. Detection
code in `internal/workspace/shadows.go` and `internal/vault/shadows.go` is pure,
bounded, and exactly what the plan asked for. The one clear instance of scope
creep is `renderShadowedColumn` in `internal/cli/status.go`, which belongs to
Issue 10.

- Blocking: 1
- Non-blocking (advisory): 1

## Blocking

### B-1. `renderShadowedColumn` is Issue 10 scope, not Issue 8

`internal/cli/status.go:242` — `renderShadowedColumn(shadows, kind, name)` has
zero production callers. Its only reference is
`internal/cli/status_test.go:261-266`. Both the helper's own doc comment
(status.go:231-241) and the `wip/implement-vault-integration-state.json`
decision log admit it is "sketched for Issue 10 --audit-secrets".

Plan confirms this: Issue 10 AC (`docs/plans/PLAN-vault-integration.md:143`)
is the one that introduces `--audit-secrets SHADOWED column shows
"yes (personal-overlay[, scope=<s>])" or "no"`. Issue 8 ACs
(PLAN lines 116-120) do not mention a SHADOWED column at all — only the
summary line and stderr format.

Additional smell: the helper already *knows* it is under-specified — its own
docstring admits "Threading scope into Shadow is a follow-up once Issue 10
designs the column header." That is a design question Issue 10 should settle,
not Issue 8. Landing the helper now locks in a signature
(`(shadows, kind, name) -> string`) before the consuming surface exists to
validate it, and adds a ~10-line block plus a dedicated test that will very
likely be rewritten in Issue 10.

**Fix:** delete `renderShadowedColumn` and `TestRenderShadowedColumn`. Keep
the summary line (status.go:216-224) — that one is Issue 8 scope and has a
real caller. Delete the backward reference to `renderShadowedColumn` at
status.go:214-215 in the comment above the summary line.

## Advisory (non-blocking)

### A-1. Test count is appropriate; minor overlap only

16 tests across the three new test files. I re-checked the list against the
invariants being guarded:

- `internal/workspace/shadows_test.go` (7 tests): compile-time-no-secret-field,
  nil inputs, env-var, env-secret, claude-env (two kinds in one test), files,
  settings, per-workspace scope, no-secret-leak. Each hits a distinct config
  shape (different struct paths into the team/overlay config). Removing any
  of these loses coverage of a real Shadow.Kind branch in `DetectShadows`.
- `internal/vault/shadows_test.go` (5 tests): collision, anonymous (empty
  name), no-collision, nil-bundles, sorted. Each targets a behavior the
  callers depend on (anonymous and nil-bundles in particular are
  precondition-sensitive for `apply.go`). No redundancy.
- `internal/cli/status_test.go` (4 new/changed relevant tests, with
  subtests): `TestStatusSummaryLineReflectsShadowCount` (3 subtests covering
  zero/one/many plural forms), `TestRenderShadowedColumn` (covered above in
  B-1).

The count is driven by the product of (shadow kind) × (boundary case),
which is inherent to the detector's surface area. Only `TestRenderShadowedColumn`
is redundant for Issue 8 — and it is redundant in the sense that the whole
helper is premature (see B-1), not that the test is a duplicate.

## Non-findings (intentionally not flagged)

- `teamSourceDefault` / `personalSourceDefault` constants for
  `"workspace.toml"` / `"niwa.toml"`: the comment at shadows.go:66-75 calls
  out that Issue 7+ may replace these with per-struct SourceFile fields.
  That is genuine forward-looking documentation for a real follow-up, not
  speculative flexibility — the constants have a real caller now.
- `ShadowLayerPersonalOverlay` exported constant: used by `status.go` and
  by tests; legitimate single-source-of-truth for the layer string.
- `ProviderShadow.TeamSource` / `PersonalSource` left blank at detection
  time, with the comment explaining that the CLI diagnostic substitutes
  defaults. That is a deliberate separation of concerns (pure detector vs.
  attribution policy), not gold-plating — the `apply.go` call site at
  line 327-328 confirms both fields are hard-coded `"workspace.toml"` and
  `"niwa.toml"` in the current caller, which is fine for v1.
- `TestShadowHasNoSecretValueField` reflective check: looks defensive, but
  the PRD R22 requirement is exactly "no secret bytes in diagnostics" and
  this is the structural half of that guarantee. Keep it.

## Verdict

Approve with one blocking change: remove `renderShadowedColumn` and its test.
Everything else is in scope and well-sized for Issue 8.
