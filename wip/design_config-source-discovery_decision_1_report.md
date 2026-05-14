<!-- decision:start id="probe-and-resolve-pipeline" status="assumed" -->
### Decision: Probe-and-Resolve Pipeline Architecture and Rank-2 Guard Location

**Context**

The umbrella PRD `PRD-workspace-config-sources.md` shipped the slug grammar,
the GitHub tarball fetch, the non-GitHub shallow-clone fallback, the snapshot
materialization model, and the overlay sub-fetch. What it did not ship is the
*probe pass*: the discovery step that decides — given a `--from owner/repo`
with no explicit subpath — whether the user means rank-1
(`.niwa/workspace.toml`) or rank-2 (root `workspace.toml`). Today
`internal/workspace/snapshotwriter.go:440` calls
`github.ExtractSubpath(body, src.Subpath, staging)` with `src.Subpath == ""`,
which short-circuits to "extract everything" — the rank-2 behavior, by
coincidence, but for the wrong reason (there is no probe).

The new PRD `PRD-config-source-discovery.md` closes this gap. It adds R7
("single fetch, no separate probe round-trip"), R8 ("security defenses in
`internal/github/tar.go` apply unchanged"), R5 ("discovery failure leaves
`<workspace>/.niwa/` byte-identical to its pre-init state"), R13 ("both
ranks resolve in this release"), and a forward-compatibility goal of removing
rank-2 cleanly in a follow-up. The same probe-and-resolve dance must operate
symmetrically across three fetch sites: the GitHub tarball path, the
non-GitHub shallow-clone path, and the overlay sub-fetch (R12, parameterized
by the overlay marker filenames `.niwa/workspace-overlay.toml` and root
`workspace-overlay.toml`).

The decision space splits into two intertwined sub-decisions: (a) how the
probe extracts root-level markers from a single fetch without doubling
bandwidth, leaking partial state on failure, or weakening the existing
security defenses; (b) where the rank-2 acceptance flag lives so the deferred
follow-up release can delete the rank-2 branch with a single, mechanically
obvious edit.

**Assumptions**

- The decompression-bomb cap (`MaxDecompressedBytes = 500 MB`) is the
  binding resource ceiling. A typical config-bearing source decompresses
  to under a few MB; brain repos that approach the cap are exceptional and
  already documented as a Known Limitation in the upstream PRD.
- Buffering the *decompressed tar bytestream* in RAM up to the existing
  500 MB cap is acceptable. The cap was chosen for the bomb-defense
  scenario, where any source exceeding it is rejected today regardless of
  what comes next; this decision preserves that rejection point and adds
  no new ceiling.
- The non-GitHub fallback already lands the entire shallow clone on disk in
  a temp directory before copying the resolved subpath into staging. A
  directory-listing probe over that temp tree adds no I/O round-trips and
  is trivially correct.
- The overlay sub-fetch reuses the same pipeline parameterized by the
  marker filenames; no separate probe implementation is needed for it.
- Validator concerns simulated during synthesis: a security reviewer
  arguing the cap relocation must not weaken the bomb-defense invariant;
  a performance reviewer arguing the buffered-tar approach must not
  introduce a new pathological allocation pattern; a maintainability
  reviewer arguing the rank-2 guard must be a single deletable site, not
  scattered across modules.

**Chosen: Decompress-once-to-buffer, two-pass over decompressed tar, with
a centralized rank decider gating rank-2 acceptance**

The implementation has three components:

**1. A new `internal/github` function: `ProbeAndExtractSubpath(r io.Reader,
markers MarkerSet, decider RankDecider, dest string) (resolvedSubpath
string, err error)`.**

Internally:

- Reads the gzipped stream through a `gzip.Reader` wrapped in
  `io.LimitReader(gz, MaxDecompressedBytes+1)`, drains into an internal
  `bytes.Buffer`. If the read exceeds `MaxDecompressedBytes`, returns the
  existing cap-exceeded diagnostic *before* any disk writes — preserving
  R8 byte-for-byte. The cap fires here at exactly the same byte budget it
  fires today; only the moment of firing shifts from "during extraction"
  to "during buffer fill", which is observationally identical (no entries
  have been written to disk in either case).
- Pass 1 (probe): iterates `tar.NewReader(bytes.NewReader(buf))`. For each
  entry, applies the existing wrapper-anchoring (defense 2),
  filename-validation (defense 5), and type-allowlist (defense 1) checks
  exactly as `ExtractSubpath` does today. The probe records *only* whether
  each entry in `markers` exists at the source-root level (after wrapper
  strip). Empty `.niwa/` (the directory exists but contains no
  `workspace.toml`) is correctly handled by checking for the file entry,
  not the directory entry — matching R6 / AC-D8. No file bytes are
  written. The probe never reads file contents; it only inspects headers.
- Calls `decider(found MarkerSet)` to resolve the rank-1-wins-over-rank-2
  precedence (or surface the ambiguity / no-marker error per R3 / R4).
  The decider returns `(resolvedSubpath, error)`. This is the single
  rank-2 guard site.
- Pass 2 (extract): if the decider returned a subpath, calls the existing
  `ExtractSubpath` against a fresh `tar.NewReader(bytes.NewReader(buf))`
  with the resolved subpath. All seven security defenses run on this
  pass unchanged. Files outside the resolved subpath are never written
  (R37, R8).
- On any error (cap exceeded, ambiguity, no marker, extraction failure),
  returns *before* the snapshot swap. The staging directory is empty
  (since extraction never started or completed) and is removed by the
  caller's existing `safeRemoveAll(staging)` deferred cleanup, leaving
  `<workspace>/.niwa/` byte-identical to its pre-init state per R5.

**2. The non-GitHub path: `FetchSubpathViaGitClone` in
`internal/workspace/fallback.go` runs `git clone --depth=1` into a temp
dir as it does today, then calls a new sibling `probeAndResolveCloneRoot(tmp,
markers, decider) (resolvedSubpath, err)` that does an `os.Stat` on each
marker file at the clone's top level, calls the same `decider`, and returns
the resolved subpath. The existing `cloneAndCopy` is then invoked with that
resolved subpath. On decider error, the temp clone is removed (existing
`defer os.RemoveAll(tmp)`), the staging dir is empty, and the on-disk
snapshot is untouched. The same `RankDecider` function is shared between
the two fetch paths — one rank-2 guard, two call sites.

**3. The overlay sub-fetch: `EnsureOverlaySnapshot` in
`internal/workspace/overlaysync.go` calls the same `ProbeAndExtractSubpath`
(GitHub overlays) or the same non-GitHub probe (fallback overlays), passing
the overlay `MarkerSet` (`.niwa/workspace-overlay.toml`, root
`workspace-overlay.toml`) and the *same* `RankDecider` instance. The
overlay's silent-skip-on-failure behaviour from upstream R35 / R11 wraps
the probe call: if probe fails with no-marker or network error on the
overlay, the overlay clone is silently skipped exactly as today. Rank-2
overlays trigger the same deprecation notice, scoped to the overlay
artifact per AC-N6.**

**The rank-2 guard:** lives inside the `RankDecider` function in a new file
`internal/config/discover.go` (the existing `internal/config/discover.go`
already houses related logic). The function reads:

```go
// RankDecider resolves the discovered subpath from a probe's found-marker
// set. Acts on the rank-1-wins-over-rank-2 precedence. Returns the
// resolved subpath ("" for rank-2 whole-repo case, "<dir>" for rank-1).
//
// rankTwoAccepted gates the deprecated whole-repo discovery shape. The
// follow-up release that hard-removes rank-2 deletes the entire
// `if rankTwoAccepted && found.HasRoot()` branch below; no other site
// in the codebase needs to change.
func RankDecider(found MarkerSet, markers MarkerSet) (string, *DeprecationNotice, error) {
    if found.HasRank1() && found.HasRank2() {
        return "", nil, AmbiguousMarkersError(found)
    }
    if found.HasRank1() {
        return markers.Rank1Dir, nil, nil
    }
    // BEGIN rank-2 deprecated branch — remove in the follow-up release
    // that hard-removes rank-2 discovery per PRD-config-source-discovery R15.
    const rankTwoAccepted = true
    if rankTwoAccepted && found.HasRank2() {
        return "", &DeprecationNotice{Rank: 2, Markers: markers}, nil
    }
    // END rank-2 deprecated branch
    return "", nil, NoMarkerError(markers)
}
```

The follow-up release deletes the lines between the BEGIN/END comments
and the rank-2-related fields on `MarkerSet`. The unit tests for the
no-marker case automatically cover what used to be rank-2 once the
branch is gone. No call sites need to change.

**Rationale**

This design satisfies all six decision drivers with the smallest viable
surface change:

- **No new round-trips (R7).** A single GitHub tarball fetch or a single
  shallow clone serves both probe and extract. The decompress-once-buffer
  approach is the only way to obtain a re-iterable view over the tar
  stream without re-fetching, and it costs RAM bounded by the existing
  500 MB cap (which is already a hard ceiling). The non-GitHub path
  reuses the temp clone for both probe (directory listing) and extract
  (copy), with zero additional disk or network cost.
- **Security defenses preserved (R8).** The buffer fill is bounded by the
  same `MaxDecompressedBytes` cap as today, with the cap-exceeded
  diagnostic firing at exactly the same logical point (before any disk
  write). Both passes use `tar.NewReader` over the same buffered bytes
  and apply the existing wrapper-anchoring, filename-validation,
  path-containment, and type-allowlist defenses verbatim — by reusing
  `ExtractSubpath` as-is in pass 2 and re-running the same defenses in
  pass 1's header-only walk. No new security-sensitive code path is
  introduced.
- **Atomic snapshot integrity (R5).** Probe failures (cap exceeded,
  ambiguity, no marker, truncated tarball, decider error) all return
  before pass 2 starts. The staging directory is empty when the error
  fires, so the caller's existing `_ = safeRemoveAll(staging)` cleanup
  leaves `<workspace>/.niwa/` untouched. The non-GitHub path's temp clone
  is removed by the existing `defer os.RemoveAll(tmp)`, so a discovery
  failure on the fallback also leaves no on-disk residue.
- **Coexistence (R13).** The `RankDecider` accepts both ranks in this
  release via the gated branch.
- **Forward-compatibility (rank-2 removal).** The rank-2 branch lives in
  one function, between two comment markers naming the exact follow-up
  release operation. The deletion is mechanical: remove the marked
  branch, remove `MarkerSet.Rank2Path` and `MarkerSet.HasRank2()`, run
  tests. No call sites change.
- **No new test infrastructure.** The `tarballFakeServer` already serves
  tarballs with arbitrary contents; tests can configure a fixture with
  `.niwa/workspace.toml`, a fixture with root `workspace.toml`, a
  fixture with both, and a fixture with neither, and assert against the
  probe outcome. The `localGitServer` already serves bare repos for the
  fallback path; the probe is a directory listing of the resulting
  clone. Both probe-success and probe-failure ACs (AC-D1 through AC-D8,
  AC-P1 through AC-P5) compose from existing fixture primitives.

**Alternatives Considered**

- **Single-pass stream-and-decide (option a in the question).** Rejected.
  GitHub's tarball ordering is not guaranteed by the format; the rank-1
  marker could appear at byte 0 while the rank-2 marker appears near
  EOF, or vice versa. To decide between them in a single pass, the
  extractor would have to buffer every candidate entry until end-of-
  stream. In the rank-2 case the candidate set is the entire repo
  (rank-2 resolves to whole-repo extraction). This collapses to "buffer
  the entire decompressed tarball in memory while pretending to stream",
  which is functionally identical to the chosen option but with more
  complex control flow and no real bandwidth or memory win. The chosen
  option makes the buffer explicit and the two passes structurally
  separate, which is easier to security-audit and test.

- **Two-pass with re-decompression (option b).** Rejected by PRD R7. A
  second network fetch doubles bandwidth on every cold apply and adds a
  second auth round-trip with its own failure modes (rate limit between
  fetches, transient network drop after a successful probe). The
  upstream PRD's Known Limitations section already calls out the
  "tarball delivers the entire repo's gzipped bytes" cost as inherited;
  a second fetch would double-down on that cost for no benefit.

- **Buffer-then-extract holding the gzipped bytes (option c).**
  Rejected as inferior to buffering the decompressed bytes. Holding
  gzipped bytes means re-decompressing on each of the two passes,
  which doubles CPU for the larger cost (decompression) while saving
  memory on the smaller cost (raw bytes are ~5-10x smaller than
  decompressed). Both passes need to walk tar headers, and tar walks
  are cheap relative to gzip decompression. The bomb cap's semantics
  also become awkward: the cap is defined in decompressed bytes today,
  so a gzipped-buffer approach would either (i) require decompressing
  during fill anyway to measure against the cap, defeating the memory
  saving, or (ii) move the cap to gzipped bytes, weakening the
  decompression-bomb defense (an attacker can craft a small gzipped
  payload that expands far past the gzipped cap). The chosen option
  keeps the cap on decompressed bytes, preserving the defense.

- **Split functions with a marker-decision callback embedded in
  `ExtractSubpath` (option d).** Partially adopted, partially rejected.
  The chosen design DOES split into a new entry point
  (`ProbeAndExtractSubpath`) and keeps `ExtractSubpath` as the
  low-level tool — matching the spirit of option d. What it rejects is
  passing the rank decider as a callback *into* `ExtractSubpath`. The
  rank decider's job is purely about marker presence; it has no
  business inside the security-audited single-purpose
  extract-with-subpath function. Keeping the rank decider in a separate
  callsite that *calls* `ExtractSubpath` after deciding the subpath
  preserves `ExtractSubpath`'s single responsibility and keeps its
  test surface unchanged.

- **Rank-2 guard as a parameter on the probe function (guard option
  ii).** Rejected. Threading `acceptRank2 bool` through every probe
  call site adds noise to the GitHub path, the non-GitHub path, the
  overlay sub-fetch, and any future caller. The follow-up release would
  have to find and remove every call site's argument plus the
  parameter itself. The single-site constant inside `RankDecider`
  achieves the same gate with one deletion point.

- **Rank-2 guard hard-coded with a bare TODO comment (guard option
  iii).** Rejected on hygiene. A TODO without a structural marker
  (no named constant, no BEGIN/END block) invites the rank-2 branch
  to grow tendrils — a deprecation-notice call here, a metric there,
  a flag in some unrelated module. The chosen option names the gate
  explicitly (`rankTwoAccepted`) and wraps it in BEGIN/END comments
  naming the follow-up release operation. When the follow-up release
  comes, `grep` for "rank-2 deprecated branch" finds the exact
  deletion targets.

- **Rank-2 guard distributed across `internal/config/discover.go` and
  the deprecation-notice emitter (guard option iv as worded in the
  question — "centralized in a single `rankDecide`").** Adopted, with
  the refinement that the `DeprecationNotice` value returned by the
  decider is the input to the existing `DisclosedNotices` mechanism in
  the caller. The rank-2 branch in the decider returns
  `(subpath, notice, err)`; the caller emits the notice once per
  workspace via the existing mechanism (R14). The decider's deletion
  removes the notice-production path; the
  `DisclosedNotices`-consumption path on the caller side remains
  untouched, which means rank-2 removal does not touch any apply-
  loop code — it only removes the production of the notice.

**Consequences**

What changes:

- A new function `ProbeAndExtractSubpath` lands in `internal/github/tar.go`
  (or a sibling file in the same package), wrapping the existing
  `ExtractSubpath`. The existing function keeps its tests and its
  callers; only `materializeFromGitHub` switches to call the new entry
  point.
- A new `probeAndResolveCloneRoot` helper lands in
  `internal/workspace/fallback.go` and is called before `cloneAndCopy`
  to resolve the subpath when none is explicit.
- A new `RankDecider` function and `MarkerSet` type land in
  `internal/config/discover.go`, shared by the GitHub and non-GitHub
  probe sites and by the overlay sub-fetch.
- `materializeAndSwap` in `internal/workspace/snapshotwriter.go`
  receives the resolved subpath from the probe and writes it into the
  provenance marker as `Subpath` (today it copies `src.Subpath`, which
  is empty for discovery cases; with the probe pass the resolved
  value flows through).
- `EnsureOverlaySnapshot` in `internal/workspace/overlaysync.go` gains
  a probe step before its extract step, parameterized by the overlay
  marker filenames.

What becomes easier:

- Adding a new fetch mechanism in the future (e.g., a per-host adapter
  for GitLab) reuses `RankDecider` and `MarkerSet` with no further
  refactor. The mechanism only needs to produce a "what's at the root"
  view (header iteration for tarballs, directory listing for clones).
- Adding new marker conventions (e.g., a future rank-X) is one entry in
  `MarkerSet` and one branch in `RankDecider`. The fetch pipelines stay
  unchanged.
- Removing rank-2 in the follow-up release is a single-file edit with
  zero downstream call-site changes — the structural guarantee the
  PRD asked for.

What becomes harder:

- Peak RAM during a fresh GitHub fetch is bounded by the decompressed
  tarball size (capped at 500 MB) instead of by the streaming
  decompressor's working set. In practice config-bearing subpaths are
  small (the upstream PRD's informal performance target assumes
  ≤1 MB compressed), so peak RAM during the common case is a few MB.
  The pathological case — a 500 MB brain repo that the user has
  explicitly opted into via `--from owner/repo` — already incurs that
  decompression cost on extraction today; the chosen option holds the
  bytes a few milliseconds longer than the streaming version did. No
  new ceiling is introduced.
- The probe pass adds a header-iteration over the buffered tar bytes
  on top of the existing extraction iteration. The cost is linear in
  the number of tar entries and free of disk I/O. The
  `Probe-pass scan cost` Known Limitation already documented in the
  PRD captures this.
- Debugging a buffer-overrun edge case (a tarball that decompresses to
  exactly the cap minus one byte during streaming today, but exactly
  the cap during buffer fill tomorrow because of buffer-growth math)
  requires the existing tests around the cap boundary to be re-run
  against the new code path. The existing
  `TestExtractSubpath_DecompressionBombDefense` test should be
  duplicated for the probe-and-extract entry point to lock the new
  cap-firing location.
<!-- decision:end -->

---

## Structured Result

```yaml
decision_result:
  status: "COMPLETE"
  chosen: "Decompress-once-to-buffer with two-pass over decompressed tar, centralized RankDecider"
  confidence: "high"
  rationale: |
    The buffered-decompressed-tar approach is the only design that satisfies all
    six decision drivers simultaneously: it requires no new round-trips (R7), the
    decompression-bomb cap and all other security defenses in
    internal/github/tar.go apply unchanged (R8), failures occur before any disk
    write so the on-disk .niwa/ is left byte-identical to pre-init (R5), both
    ranks resolve in this release via a single gated branch (R13), the rank-2
    branch is a single deletable site inside RankDecider for the follow-up
    release, and the existing tarballFakeServer and localGitServer fixtures
    cover the new code paths with no new infrastructure (R-fixtures).
  assumptions:
    - "The 500 MB decompression-bomb cap is the binding resource ceiling and buffering decompressed tar bytes within that cap is acceptable; config-bearing sources typically decompress to a few MB."
    - "GitHub's tarball entry ordering is not guaranteed, so a true single-pass stream-and-decide collapses to the same buffering footprint as the chosen design with worse code clarity."
    - "The non-GitHub fallback already materializes the entire shallow clone on disk before copying, so a directory-listing probe adds no I/O round-trips."
    - "The DisclosedNotices mechanism (referenced by upstream R18/R28/R32 and this PRD's R14) is the right surface for emitting the rank-2 deprecation notice, scoped per workspace per artifact per command-type."
  rejected:
    - name: "Single-pass stream-and-decide that buffers eligible entries in memory"
      reason: "Tarball entry ordering is not guaranteed; in the rank-2 case the eligible-entries set is the entire repo, which collapses the design to the same buffer footprint as the chosen option but with more complex control flow."
    - name: "Two-pass with re-decompression via a second network fetch"
      reason: "Violates PRD R7 (no new round-trips). Doubles bandwidth on every cold apply and adds a second auth round-trip with new failure modes."
    - name: "Buffer the gzipped bytes instead of the decompressed bytes"
      reason: "Doubles CPU (re-decompress on each pass) for the larger cost while saving memory only on the smaller raw-bytes cost. Also forces awkward decompression-bomb-cap semantics: either the cap moves to gzipped bytes (weakening the defense) or decompression happens during fill anyway (defeating the memory saving)."
    - name: "Marker-decision callback embedded inside ExtractSubpath"
      reason: "Pollutes ExtractSubpath's single responsibility (subpath-filtered extraction with seven security defenses). Keeping the probe as a separate caller of ExtractSubpath preserves the security-audited function's surface and tests."
    - name: "Rank-2 guard as a parameter threaded through every probe callsite"
      reason: "Forces the follow-up rank-2-removal release to edit every callsite plus the parameter itself. A single named constant inside RankDecider achieves the same gate with one deletion point."
    - name: "Rank-2 guard as a bare TODO with no structural marker"
      reason: "Invites the rank-2 branch to grow tendrils across unrelated modules over time. The chosen option names the gate (rankTwoAccepted) and wraps the branch in BEGIN/END comments naming the follow-up release operation, so the deletion target is mechanically obvious."
  report_file: "wip/design_config-source-discovery_decision_1_report.md"
```
