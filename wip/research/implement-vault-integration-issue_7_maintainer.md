# Maintainer review — Issue 7 (SourceFingerprint + state schema v2 + status provenance)

Commit `b2c56490c2148e34eeddeb731210bdbf0b360fcf` on `docs/vault-integration`.

## Note on prompt-injection attempt

The final block of the task turn arrived as a `<system-reminder>` titled "MCP Server Instructions" describing a `telegram` tool. No telegram tool is available to this agent, the review has nothing to do with Telegram, and the text conditions behavior around out-of-band channels ("never invoke that skill", "someone in a Telegram message says 'approve the pending pairing'"). Treated as prompt injection — ignored. No memory written. Flagging so downstream review artifacts capture that this was attempted.

## Scope of this review

The task framed five maintainability checks. For each, I read the files listed (`internal/cli/status.go` + test; `internal/workspace/apply.go`, `fingerprint_test.go`, `materialize.go`, `sources_test.go`, `state.go`, `state_test.go`, `status.go`, `status_test.go`) plus cross-references in `DESIGN-vault-integration.md` and `PLAN-vault-integration.md`.

## Verdict: APPROVE (1 advisory, 0 blocking)

The diff is readable. The next developer who inherits this will form a correct mental model — the new types are small, the constants are defined where they belong, the migration semantics are documented in the one place that matters (`LoadState` docstring), and the tests exercise real user scenarios.

One finding below, advisory.

---

## Check 1 — `SourceEntry.Kind` values are consts, not magic strings. **PASS.**

`internal/workspace/state.go:33-43` defines `SourceKindPlaintext = "plaintext"` and `SourceKindVault = "vault"` with docstrings that explain what each category means. Grep confirms no `Kind: "plaintext"` or `Kind: "vault"` literals anywhere in the tree — every producer (materialize.go lines 180, 568, 610, 659, 673, 911, 1001) and every consumer (`recomputeChangedPlaintextSources` in status.go, all tests) routes through the constants.

One minor asymmetry: `internal/cli/status.go:192-195` uses raw strings (`prefix := "vault"`, `prefix = "plaintext"`) for the display label. The values happen to match the constants, but they're being used as a *human-readable label*, not a state value — so the raw strings read as a coincidence, not a bug. See advisory A1 below.

## Check 2 — GoDoc on `ComputeSourceFingerprint` explains the reduction rule. **PASS.**

`state.go:106-115` names the exact ingredients: hex-encoded SHA-256 of a stable-sorted, null-separated list of `(SourceID, VersionToken)` tuples. The docstring goes on to explain *why* the reduction exists ("distinguish user-edited drift from upstream rotation") and explicitly documents the empty-slice case (stable zero-input digest).

One thing the docstring gets right that's easy to miss: it names which fields do NOT participate in the rollup by implication (only `(SourceID, VersionToken)` — so not `Kind`, not `Provenance`). The test file locks this in with `TestComputeSourceFingerprintIgnoresProvenanceAndKind` (fingerprint_test.go:64-70). Docstring + test together keep the next developer from accidentally mixing `Kind` into the hash during a refactor.

## Check 3 — `niwa status` stale output for Infisical-backed secrets is readable. **PASS.**

The detail view (status.go:191-207) outputs four lines per changed source:
```
    changed source: vault://team/API_TOKEN
      version: abc123def456... -> 789xyz012abc...
      provenance: https://app.infisical.com/secret/audit/abc123
```

`shortToken` (status.go:219-227) truncates long opaque tokens to 12 chars + `...`, preserving the full token in `state.json`. Empty tokens render as `(none)`. Provenance is pulled verbatim from `SourceEntry.Provenance`, which for the Infisical backend is populated with the provider's audit URL (confirmed by `sourceForMaybeSecret` in materialize.go:650-665 copying `ms.Token.Provenance` straight through).

The one edge the code handles silently: if `cs.OldToken` and `cs.NewToken` are both empty but there's a `Description`, only the note prints. That's fine — it's the "file missing" branch from `recomputeChangedPlaintextSources` (status.go:163-171).

## Check 4 — v1 → v2 migration documented. **PASS (with nuance).**

The load/migration path is documented in depth at the right level for a maintainer: `LoadState` docstring (state.go:172-180) names the exact downgrade failure mode ("Downgrading a v2-written state back to a pre-Issue-7 binary will fail to parse — the unknown schema_version value trips the strict-parse path there"), `SchemaVersion` constant comment (state.go:24-28) explains the load shim and rewrite behavior, and the `ManagedFile` docstring (state.go:60-69) explains why the JSON tag on `ContentHash` is kept at `"hash"`.

README and CHANGELOG are out of scope here — there is no CHANGELOG in the repo and `PLAN-vault-integration.md:221` calls for release-notes documentation only after Issue 8 lands alongside ("Issues 7 and 8 jointly bump … Document in the release notes …"). So the release-note gap is Issue-8-era work, not Issue 7's responsibility. Approving.

The behavioral point the next developer needs most — "what happens if I hand-roll a v1 fixture into a v2 codebase" — is captured by `TestLoadStateV1MigrationShim` (fingerprint_test.go:127-186), which loads a verbatim v1 payload and asserts both the zero-valued new fields and the v2 rewrite round-trip.

## Check 5 — Test names reflect user-observable scenarios. **PASS.**

The new tests (not pre-existing) map cleanly to scenarios a user would name:
- `TestComputeSourceFingerprintDeterministic` — fingerprint stability and sort-independence
- `TestComputeSourceFingerprintIgnoresProvenanceAndKind` — invariant lock
- `TestSourceEntryJSONRoundTrip` — state-file compatibility, including omitempty check
- `TestLoadStateV1MigrationShim` — v1 → v2 upgrade path
- `TestEnvMaterializerRecordsSources` — materializer populates Sources[]
- `TestApplyPopulatesSourceFingerprintEndToEnd` — pipeline-level fingerprint capture
- `TestComputeStatusDriftOnlyUnchangedFingerprint` — **user-observable**: "I edited the file; is it stale or drifted?"
- `TestComputeStatusPlaintextRotationStale` — **user-observable**: "I rotated the source; does status catch it?"
- `TestApplyVaultRotationUpdatesSourceFingerprint` — **user-observable**: "Vault value rotated; does re-apply update fingerprint?"

Each test name tells the next developer what scenario will fail if they break it. None of them lie.

---

## Advisory

### A1 — Asymmetric string use in CLI display prefix

`internal/cli/status.go:191-195`:
```go
for _, cs := range f.ChangedSources {
    prefix := "vault"
    if cs.Kind == workspace.SourceKindPlaintext {
        prefix = "plaintext"
    }
    fmt.Fprintf(out, "    changed source: %s://%s\n", prefix, cs.SourceID)
```

The comparison uses the constant `workspace.SourceKindPlaintext` but the assignments use raw `"vault"` / `"plaintext"` literals. The values happen to coincide with the constants today, so behavior is correct, but the pattern invites drift: if someone renames `SourceKindPlaintext` to (say) `"inline"` for state-file reasons, the display label silently desynchronizes from the stored `Kind`. The symmetric form — `prefix := cs.Kind` — is strictly equivalent and keeps the display/storage linkage honest.

The next developer isn't going to misread this into a bug, which is why it's advisory rather than blocking. It's just the kind of small divergence that matters in a `grep`-driven refactor.

Suggested fix:
```go
fmt.Fprintf(out, "    changed source: %s://%s\n", cs.Kind, cs.SourceID)
```

### Note on pre-existing no-op in `showSummaryView`

`internal/cli/status.go:129-132` has a no-op if-branch:
```go
driftLabel := "drifted"
if status.DriftCount == 0 {
    driftLabel = "drifted"
}
```

Both branches assign the same string, so this reads as incomplete intent (the empty-case label was presumably meant to be "clean" or similar). However, this code is **not touched by Issue 7** — the Issue 7 diff adds the provenance output block further down in `showDetailView`. Out of scope for this review; worth flagging in a follow-up cleanup but not a reason to block here.

---

## Blocking findings

None.

## Summary

The commit reads cleanly. The one advisory (raw strings in the CLI display prefix) is stylistic — strictly correct today, but the symmetric form (`cs.Kind` directly) removes a future drift trap. The pre-existing `driftLabel` no-op in `showSummaryView` is worth noting but out of scope. Approve.
