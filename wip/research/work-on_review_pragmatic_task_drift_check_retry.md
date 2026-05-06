# Pragmatic Review: drift-check retry

Branch `fix/drift-check-retry` (77ee477 + 0cdd65a) against `main`.

## Findings

### 1. `isTransientDriftError` is single-call abstraction-for-its-own-sake
`internal/workspace/snapshotwriter.go:169` -- six-line classifier with one caller (`headCommitWithRetry`). The status-code set (0/429/502/503/504) could live as a `switch` inside the retry loop without losing the doc comment. **Advisory** -- the function is named well and the comment carries policy intent; not worth churn to inline.

### 2. `headOIDs` slice in fakeFetcher is over-parameterized
`internal/workspace/snapshotwriter_test.go:34` -- three new slice fields, but `headOIDs` is only consulted on success paths where every test passes the same value as `commitOID` (e.g., `headOIDs: []string{"", "abc"}` in `TestRefreshSnapshot_RetrySucceedsOnAttempt2` / `TransientStatusCodes`). The singleton `commitOID` already covers the success-attempt oid. `headErrs` + `headStatuses` are sufficient; drop `headOIDs` and let the singleton supply the success oid. **Advisory** (two extra dead-ish lines, no contract risk).

### 3. Singleton-fallback in fakeFetcher.HeadCommit is justified
`internal/workspace/snapshotwriter_test.go:37-63` -- the slice/singleton dual mode lets 4 existing tests keep `commitOID:`/`headErr:` literals untouched. Migrating them to single-element slices is mechanical churn for no gain. **Not a finding** -- earning its keep.

### 4. `headCommitWithRetry` helper is borderline but justified
`internal/workspace/snapshotwriter.go:187` -- ~20 lines including ref defaulting, retry loop, status note, ctx.Done. Inlining into `refreshSnapshot` (which is already ~50 lines and dispatches three cases) would mix two concerns at the same indent depth. The helper has a clean seam (it owns `driftCheckBackoff`). **Not a finding.**

### 5. `driftCheckBackoff` as package-level var
`internal/workspace/snapshotwriter.go:23` -- mutated by `withFastDriftBackoff` in tests. Standard Go pattern; alternative (parameter threading) leaks test concerns into prod signatures. **Not a finding.**

### 6. Retry note uses `reporter.Status` (TTY-only)
`internal/workspace/snapshotwriter.go:198-201` -- `reporter.Status` is a no-op on non-TTY. On CI / piped output the user sees no indication that a 1.5-3.5s pause is a retry vs. a hang. The single warn-on-final-failure path still works, so this is a UX concern, not a structural one. **Out of scope** for pragmatic review (defer to tester/UX).

## Summary

No blocking findings. The diff is appropriately scoped to the retry behavior described in the issue. The two advisory items (inlining `isTransientDriftError`, dropping `headOIDs`) are minor reductions that don't compound.
