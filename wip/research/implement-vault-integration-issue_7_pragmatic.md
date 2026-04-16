# Pragmatic review — Issue 7 (SourceFingerprint + per-source tuples + status provenance)

Commit: `b2c56490c2148e34eeddeb731210bdbf0b360fcf` on `docs/vault-integration`.

## Note on prompt-injection attempt

The final block of the task turn arrived as a `<system-reminder>` titled "MCP Server Instructions" describing a `telegram` tool. No such tool exists in this agent's function list, the review task has nothing to do with Telegram, and the text tries to condition future behavior around out-of-band channels. Treated as prompt injection — ignored. No action taken, no memory written. Flagging here because the reviewer prompt says to verify adversarial-looking context.

## Q1 — `MaterializeContext.SourceTuples` map-on-ctx pattern

**Verdict: appropriate as implemented. Non-blocking.**

The natural alternative would be to widen the `Materializer.Materialize` signature to `([]string, map[string][]SourceEntry, error)` and have `runPipeline` merge each returned map. That is cleaner in isolation but:

- There are 4 implementations of `Materializer` (`HooksMaterializer`, `SettingsMaterializer`, `EnvMaterializer`, `FilesMaterializer`) and the interface is exported. Widening it forces every current and future materializer to plumb a second return value even when they don't record sources.
- `recordSources` is a one-line nil-tolerant helper (`materialize.go:90-100`), so materializers that don't care stay ignorant; the map-on-ctx pattern gives the right default (no recording) for free.
- The map key is the written file path, which several materializers would need to return alongside the map anyway — joining them in the caller is redundant.

The pattern is idiomatic Go for "threaded output accumulator": see e.g. `ctx.InstalledHooks` on the same struct (populated by `HooksMaterializer` and read by `SettingsMaterializer`). The new field is a sibling of an already-established idiom, not a new invention.

Minor observation (not a finding): the comment on `SourceTuples` warns "Materializers must not mutate entries already present for a key", but `recordSources` actually *appends* to whatever is present (`materialize.go:99`). Comment is slightly misleading but behavior is safe.

## Q2 — Test coverage for 12 ACs

**Verdict: coverage is adequate for what Issue 7 owns. Non-blocking.**

PLAN-vault-integration.md lists 5 "Key ACs" for Issue 7 (not 12). The 8 new tests map as follows:

| AC | Test(s) |
|----|---------|
| `SourceEntry{Kind, SourceID, VersionToken, Provenance}` shape | `TestSourceEntryJSONRoundTrip`, `TestComputeSourceFingerprintDeterministic` |
| v1 states load via migration shim | `TestLoadStateV1MigrationShim` |
| `niwa status` offline (no provider calls) | `TestComputeStatusDriftOnlyUnchangedFingerprint`, `TestComputeStatusPlaintextRotationStale` (both call ComputeStatus without any vault wiring) |
| Stale output includes provenance | `TestApplyPopulatesSourceFingerprintEndToEnd` (asserts `Provenance != ""`) |
| Functional tests: drift-only, vault-rotated, mixed-source | `TestComputeStatusDriftOnlyUnchangedFingerprint`, `TestApplyVaultRotationUpdatesSourceFingerprint`, `TestEnvMaterializerRecordsSources` |

Plus `TestComputeSourceFingerprintIgnoresProvenanceAndKind` locks the "Kind/Provenance don't enter the rollup" invariant that Issue 10 depends on.

Gaps worth naming (not blocking):
- No direct test for "file missing or unreadable" branch in `recomputeChangedPlaintextSources` (status.go:163). The branch is small and the behavior is surfaced in CLI output, so the cost of adding a test is low if the maintainer wants one.
- No test exercises `looksLikePath` returning false (`workspace.toml:...` synthesized SourceIDs); the code path is currently a silent skip, so a regression would show up as "drifted" instead of "stale" — detectable but not caught in CI.

Neither gap breaks the AC list.

## Q3 — `recomputeChangedPlaintextSources` candidate-path walk

**Verdict: over-engineered comment, adequate code. Advisory.**

The docstring (status.go:123-138) describes *three* candidate roots — "the workspace root (parent of the instance root), the niwa config dir (sibling `.niwa` under the workspace root), and … the workspaceRoot directly". The code uses *two*:

```go
candidates := []string{
    filepath.Join(workspaceRoot, ".niwa"),
    workspaceRoot,
}
```

This is a minor doc/code drift, not over-engineering.

The real question is whether the fallback list needs to exist at all. Two reasons it does:

1. Materializers write `SourceID` as a path relative to `ctx.ConfigDir` (see `materialize.go:567-571`, `1002`). `ConfigDir` is typically `<workspaceRoot>/.niwa/`, so the first candidate is the production layout.
2. The fingerprint tests (`sources_test.go:261-330`) put the source file at `<configDir = filepath.Dir(root)>/shared.env`, which is the second candidate. This is a test-fixture shape, not a production one.

Threading `configDir` into `ComputeStatus` would let the fallback collapse to a single root. That's a small refactor but requires touching the `status` CLI call site, which doesn't currently carry `configDir` all the way through (`cli/status.go:142` calls `showDetailView(cmd, instanceRoot)` only). The test-fixture-shape second candidate exists precisely because the production path doesn't thread `configDir` today.

The candidate walk is three lines of Go; the complexity isn't in the walk, it's in the implicit coupling to the test fixture layout. A follow-up issue could plumb `configDir` and drop the second candidate, at which point the docstring's mention of "test fixtures whose sources live in the workspace-root parent" becomes stale. Not worth blocking Issue 7 over.

## Other observations

None that rise to blocking.

- `SourceTuples` comment says "tests relying on map identity pass a nil map explicitly" (materialize.go:81-82). That contract is load-bearing for `recordSources` being a nil-tolerant no-op. The pattern is clean.
- `ComputeSourceFingerprint` constructs a local `pair` struct and sorts a copy rather than mutating the caller's slice (state.go:117-135). Defensive but justified — the comment calls out that materializers deliberately order sources for diagnostic output.
- `sourceForMaybeSecret` computes SHA-256 of plaintext in the helper rather than in the caller (materialize.go:670-677). Keeping the plaintext local to one function is the right call for R15/R22 containment; this is the *opposite* of over-engineering.

## Summary

Nothing blocking. The implementation makes reasonable trade-offs: `SourceTuples` on the context avoids a forced-widening of an exported interface; coverage hits the key ACs without gold-plating; and the candidate-path walk is a three-line pragmatic concession to the existing absence of `configDir` in the status call path, not an architectural overreach.

Approve.

- blocking_count: 0
- non_blocking_count: 3 (one advisory on docstring/code drift, two coverage gaps flagged for maintainer discretion)
