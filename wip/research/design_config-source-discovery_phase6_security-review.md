# Security Review

## Verdict: PASS

The design preserves the existing security defenses and introduces no fundamentally new attack surface, but it does relax peak-RAM bounds and leaves the decompression-bomb cap semantics described inconsistently with the current implementation; these should be reconciled before merge.

## Threats Identified

1. **Memory pressure before the 500 MB cap fires**: The design buffers the decompressed tar into a `bytes.Buffer` bounded by `io.LimitReader(gz, MaxDecompressedBytes+1)`. `bytes.Buffer` grows geometrically (double-and-copy), so peak heap during the final doubling is `~2 * MaxDecompressedBytes` (~1 GB). Today's streaming extractor never holds more than a working-set window. **Mitigation**: design documents this as a Negative consequence but offers no engineering mitigation (e.g., `bytes.Buffer.Grow(int(MaxDecompressedBytes))` to pre-allocate and skip the doubling, or use a backing file). Live but bounded.

2. **Cap-semantics description inconsistent with current code**: The Security Considerations section claims "The cap is *not* moved to gzipped bytes (rejected alternative); it stays on the decompressed stream." However, `internal/github/tar.go:63` today wraps the *raw input* with `io.LimitReader(r, bytesBudget+1)` *before* `gzip.NewReader`, meaning today's cap is on the gzipped (compressed) bytes, not decompressed. The design's wording is reversed. The proposed `ProbeAndExtractSubpath` signature `gzip.Reader wrapped in io.LimitReader(gz, MaxDecompressedBytes+1)` (Decision 1, step 1) would actually move the cap to decompressed bytes — a real semantic change. A gzipped tarball under 500 MB compressed that decompresses past 500 MB is accepted today (gzip ratio depends on contents, but typically 5-10x for text) — under the new design it would be rejected. **Mitigation**: this is arguably a *stronger* defense (a true decompressed-bytes cap is what the constant name promises), but it is a behaviour change masquerading as a preservation claim. Should be called out.

3. **Type allowlist on probe pass — implementation discipline required**: Design states pass 1 "applies the existing allowlist," but the probe records "marker exists" by entry name. A maliciously crafted tarball with a symlink entry named `.niwa/workspace.toml` could trigger a positive probe result (rank-1) if the implementation records the marker before checking the type allowlist. The subsequent extract pass would skip the symlink (correctly), leading to a confusing "subpath not found" error or, worse, a snapshot missing the marker file. **Mitigation**: design text mandates type-allowlist-then-record ordering, but it is implementation discipline, not a structural guarantee. Phase 2 tests must include a "symlink-with-marker-name" case.

4. **`niwa source inspect` private-repo information disclosure**: Threat is bounded — the command uses the invoking user's `GH_TOKEN`, so it can only see what the user can already see. **Mitigation**: design correctly identifies this. The JSON output exposes only `markers_found_at_root` (filenames, not contents), the slug the user typed, and the resolved rank. No new disclosure.

5. **Dual-entry tarball poisoning (probe vs. extract divergence)**: A crafted tarball with duplicate or contradictory entries for the same logical marker file could theoretically cause the probe to record one result while extract behaves differently. Since both passes operate on the same buffered bytes via fresh `tar.NewReader(bytes.NewReader(buf))`, they observe identical header streams in identical order. Duplicate entries are equally "duplicated" on both passes. **Mitigation**: structurally satisfied by the shared buffer.

6. **Probe writes outside staging**: Design explicitly states pass 1 "never writes to disk." **Mitigation**: structural — pass 1 does not call `os.OpenFile` or `os.MkdirAll`. Verified.

7. **Deprecation notice for snapshot that never landed**: Decision 3 and Step 5 of the Decision Outcome explicitly order notice emission *after* `SwapSnapshotAtomic` succeeds. Implementation approach phase 5 deliverables list `internal/cli/init.go` and `internal/workspace/apply.go` call-site changes that capture `rank` from the return tuple and call `EmitRank2Notice` after promotion. **Mitigation**: design is consistent across decision, outcome, and phase sections.

8. **Probe failure leaving partial staging dir**: Design relies on "caller's existing `_ = safeRemoveAll(staging)` deferred cleanup." Pass 1 writes nothing, pass 2's failures already trigger the existing cleanup. **Mitigation**: preserved.

## Residual Risks

1. **Peak-RAM regression on small-memory hosts**: A 500 MB-decompressing tarball can drive niwa to ~1 GB resident during the buffer's final geometric doubling. CI runners, small VMs, and constrained dev environments may OOM where the previous streaming extractor would not. The design surfaces this but does not bound it.

2. **Cap-semantics drift between docs and implementation**: The design narrative claims preservation of the existing cap behaviour while actually proposing a behaviour change (gzipped-bytes cap → decompressed-bytes cap). Implementers reading only the Security Considerations section may not realize they are tightening the cap; auditors comparing pre/post behaviour will see a real-world rejection that the docs say should not happen.

3. **Skill-driven `niwa source inspect` invocation on attacker-supplied slugs**: If a user runs the migration skill against a slug provided by an untrusted source (issue ticket, README, etc.), the probe fetches that repo's tarball and consumes ~1 GB peak RAM at worst case before the cap fires. The same risk exists today for `niwa init --from`, but `source inspect` *normalises* the pattern of probing arbitrary slugs — increasing the likelihood of this exposure.

4. **JSON schema versioning is doc-only**: The `schema_version: 1` field is purely declarative — niwa has no code that validates consumers' schema version expectations. The mitigation pointer to documentation under `#niwa-source-inspect-schema` is real (Phase 6 lists `docs/guides/workspace-config-sources.md` updates) but provides no runtime safety; tool authors who fail to read it get silent breakage on future bumps.

## Recommendations

1. **Reconcile cap semantics**: Either (a) keep the cap on the gzipped input to preserve today's behaviour, with a clear note that 500 MB is a compressed-bytes cap, or (b) move the cap to decompressed bytes (which matches `MaxDecompressedBytes` naming) and explicitly call out the behaviour change as intentional tightening. The current "stays on the decompressed stream" wording is wrong about today's code.

2. **Pre-allocate the buffer or use a temp-file backing**: Call `bytes.Buffer.Grow(int(MaxDecompressedBytes))` before reading to skip geometric doubling, bringing peak RAM down to ~500 MB instead of ~1 GB. Alternatively, back the buffer with a temp file under the staging dir's parent to remove the RAM bound entirely (at a one-time disk write cost). This is a small implementation change with a meaningful operational impact.

3. **Phase 2 test coverage for type-allowlist on probe**: Add an explicit test case `TestProbeAndExtractSubpath_RejectsSymlinkMarker` that places a `tar.TypeSymlink` entry named `wrap/.niwa/workspace.toml` and asserts the probe does *not* record rank-1. This locks in the design's "probe applies the existing allowlist" claim against accidental implementation drift.

4. **Phase 2 test coverage for duplicate-entry tarballs**: Add `TestProbeAndExtractSubpath_DuplicateMarkerEntries` to confirm a tarball with two `wrap/.niwa/workspace.toml` entries probes consistently with extract, and a tarball with one rank-1 entry and one rank-2 entry resolves rank-1 (the precedence rule).

5. **Document the `source inspect` slug-probe attack surface**: Note in the Security Considerations section that `niwa source inspect` invokes the same fetch path as `init`/`apply`, so any slug the user supplies — even via untrusted channels — triggers a tarball fetch and ~1 GB peak RAM under the worst case.

6. **Make the deprecation notice ordering claim verifiable at code-review time**: Phase 4 and Phase 5 deliverables both touch `internal/cli/init.go` and `internal/cli/apply.go`. The phase summaries should state explicitly that `EmitRank2Notice` is called *after* `MaterializeFromSource` / `EnsureConfigSnapshotWithStatus` returns — not inside the materializer — so reviewers can confirm the post-promotion ordering invariant from the diff alone.

## Summary

The probe-then-extract design correctly preserves six of the seven security defenses by structure (no writes during probe pass; shared buffer ensures probe and extract see identical bytes; existing extract logic runs unchanged on pass 2). The two real concerns are operational rather than exploit-level: a ~2x peak-RAM regression during the buffer's geometric doubling phase, and an internally inconsistent description of the decompression-bomb cap semantics that hides what may be an intentional behaviour change. The deprecation-notice-after-promotion ordering claim is verified consistent across Decision 3, the Decision Outcome step list, and the Phase 4/5 deliverables; the `niwa source inspect` command introduces no novel auth or disclosure path beyond what the existing fetch already permits.
