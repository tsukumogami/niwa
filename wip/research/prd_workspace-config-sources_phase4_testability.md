# Testability Review

## Verdict: FAIL

The PRD's ACs are mostly well-formed, but several rely on infrastructure niwa
doesn't have today (a `tarball-fake-server`/HTTP fixture, network-loss
simulation, partial-write fault injection) without naming a fixture strategy,
and a handful are stated in terms a reviewer cannot mechanically verify
(e.g., "no files outside the resolved subpath are present on disk" needs to
say *when* it's checked, against *what* tarball shape).

## Untestable Criteria

1. **AC**: "On `niwa apply` against a `github.com` source, no files outside the resolved subpath are present on disk after materialization."
   - **Why untestable**: `github.com` is a real host. The functional suite uses `localGitServer` (a `file://` bare-repo helper) and has no fixture for the GitHub REST tarball endpoint. A reviewer cannot write the Given without inventing a tarball-server fake. The Phase 2 maintainer notes flag this gap explicitly.
   - **How to fix**: Add a precondition naming the fixture (e.g., "Given a `tarballFakeServer` configured to serve `<bytes>` at `repos/{owner}/{repo}/tarball/{ref}`") and make it a deliverable inside scope. Either commit to building the fixture as part of the work, or rephrase the AC to verify against the fallback (`file://`) path which the existing `localGitServer` already supports.

2. **AC**: "The drift check against `commits/{ref}` returns the cached oid (matching the snapshot provenance) without invoking the tarball endpoint."
   - **Why untestable**: Same fixture gap as above, plus "without invoking the tarball endpoint" requires an observable assertion (request log on the fake) that the PRD doesn't promise to expose. A reviewer cannot tell from the AC alone whether to count HTTP calls, watch a metric, or read a log line.
   - **How to fix**: Specify the observation channel: "The fake tarball server records zero `GET /tarball` requests during the second apply" (assuming the fake exposes a request log). Alternatively, gate on a debug-log line emitted by niwa.

3. **AC**: "When the SHA endpoint reports a different oid, niwa issues the tarball request with `If-None-Match: <stored ETag>`; a 304 response is treated as 'no change' without re-extracting."
   - **Why untestable**: The verification requires a fake that (a) returns 304 on conditional GET and (b) records the `If-None-Match` header. No such fake exists. Also "without re-extracting" needs a measurable signal — file mtime preserved? extraction-count counter? The AC doesn't say.
   - **How to fix**: Name the fixture and the observable signal. Example: "the fake records a single `If-None-Match: <oid>` header on the conditional GET, and the snapshot directory's mtime is unchanged."

4. **AC**: "On a private GitHub repo, a 401 or 403 from the tarball or SHA endpoint surfaces an error naming the underlying API status with a remediation hint pointing at PAT scope documentation."
   - **Why untestable**: A real private GitHub repo cannot be a precondition in CI. Without a fake serving 401/403, this is unverifiable. The phrase "PAT scope documentation" is also a moving target — does the test assert exact URL, exact text fragment, or just that the word "PAT" appears?
   - **How to fix**: Replace with "Given the fake tarball server is configured to return 401 for `repos/{owner}/{repo}/tarball/{ref}`, niwa exits non-zero with stderr containing both '401' and a literal substring naming PAT scope (e.g., 'PAT scope')."

5. **AC**: "After a snapshot refresh interrupted partway (network cut, tarball truncation, disk-full during extraction), the previous snapshot at `<workspace>/.niwa/` is intact."
   - **Why untestable**: niwa today has no fault-injection harness. "Network cut" and "disk-full" aren't reproducible from a Gherkin step without new infrastructure. "Intact" also needs a definition (byte-identical? same provenance marker?).
   - **How to fix**: Pick one injection mechanism and commit to it: e.g., "Given the tarball fake closes the connection after N bytes" plus a niwa-side seam (a context-cancel trigger or a `NIWA_TEST_FAULT=truncate-after:N` env hook). Define "intact" as "the provenance marker's `resolved_commit` is the pre-refresh oid and the file tree byte-matches the pre-refresh tree."

6. **AC**: "When the network is unreachable on `niwa apply` against a ref-less slug, apply continues with the cached snapshot and emits a warning naming the source URL, the cached commit oid, and the cached `fetched_at`. Apply exit code is 0."
   - **Why untestable**: "Network unreachable" has no Gherkin Given today. The closest existing pattern is a `file://` URL pointing to a non-existent path, but that produces different error semantics than DNS/TCP failure.
   - **How to fix**: Either (a) point the slug at a reserved unroutable IP / loopback port that nothing listens on (deterministic across CI), or (b) make the fake tarball server support a "drop next request" mode and use it. Specify which.

7. **AC**: "The plaintext-secrets public-repo guardrail enumerates remotes from the provenance marker; the GitHub-public pattern match runs against the marker tuple. The guardrail does not silently disable on snapshot configs."
   - **Why untestable**: The Given is missing — what triggers the guardrail in the test? "Does not silently disable" is a negative assertion against a behavior that has no observable signal in the AC. A reviewer can't tell whether to assert a specific stderr line, a non-zero exit code, or both.
   - **How to fix**: Rewrite as a positive assertion. "Given a config repo whose provenance marker names host=`github.com`, owner=`<public-org>`, when `niwa apply` evaluates a workspace.toml containing a plaintext secret, the apply exits non-zero with stderr containing 'public repo' and naming the offending key." Add a paired AC for the negative case (private host -> guardrail does not fire).

8. **AC**: "An `InstanceState` with `schema_version` greater than the highest supported version fails to load with a diagnostic naming both versions; niwa does not attempt down-conversion."
   - **Why untestable as written**: "Naming both versions" is verifiable, but "does not attempt down-conversion" is again a negative without an observable. There's no signal a Gherkin step can latch onto.
   - **How to fix**: Drop the second clause or recast it as "the on-disk state file is unchanged after the failed load (byte-identical to before)."

9. **AC**: "The same snapshot posture applies to the personal overlay clone and the workspace overlay clone (no `.git/`, atomic refresh, provenance marker present in each)."
   - **Why partly untestable**: "no `.git/`" and "marker present" are checkable. "Atomic refresh" isn't directly observable from outside without fault injection (tied to AC #5 above).
   - **How to fix**: Split into two ACs — one for the static post-conditions (no `.git/`, marker present in each of the three locations after `niwa apply`) and one cross-referencing the atomic-refresh fault-injection AC once the fixture lands.

10. **AC**: "After a snapshot refresh interrupted partway... the previous snapshot at `<workspace>/.niwa/` is intact."
    - Already covered in #5; flagged again because the symmetric R13 implies the same guarantee for the personal overlay and workspace overlay clones, which the AC doesn't currently assert.

## Missing Test Coverage

1. **R3 (strict slug parser)**: The PRD's ACs cover `owner/repo:` (empty subpath after colon) but not the other rejection cases R3 enumerates: malformed ordering of separators, embedded whitespace, multiple `:` separators, multiple `@` separators. Add one AC per rejection class or one table-driven AC enumerating all rejection inputs.

2. **R4 (subpath resolves to a file)**: One AC verifies the file→parent-dir behavior. No AC verifies the negative — what happens when the file isn't a valid `workspace.toml` or `niwa.toml`? Add an AC.

3. **R10 (provenance marker contents)**: ACs verify the marker exists. No AC verifies the marker actually contains the R11 fields (source URL, parsed tuple, resolved oid, fetched-at, fetch mechanism). Add an AC: "the provenance marker file in `<workspace>/.niwa/` contains keys `source_url`, `host`, `owner`, `repo`, `subpath`, `ref`, `resolved_commit`, `fetched_at`, `fetch_mechanism`."

4. **R20 (lazy registry upgrade preserves data)**: AC verifies mirror fields are populated on next write. No AC verifies that pre-existing fields are preserved across the upgrade — a regression where the upgrade drops `groups` or other unrelated state would slip through. Add an AC.

5. **R26 (positive guardrail behavior)**: As above; the AC asserts a negative without a paired positive case.

6. **R27 (deprecation notice once per process)**: AC says "printed once per process invocation" but doesn't include the test for "the second `--allow-dirty` invocation in a different process also prints the notice" — needed to distinguish "once per process" from "once per workspace lifetime" (cf. the existing one-time-notice infrastructure).

7. **R30 (performance thresholds)**: Both the 5s first-fetch and the 500ms drift check are stated as SHOULDs with no AC. Either add a benchmark AC ("the drift check against the fake SHA endpoint completes in under 500ms wall clock on the standard CI runner") or call out explicitly that performance is not gate-blocked in v1 (acceptable, but should be stated).

8. **R32 (no temporary persistence outside subpath)**: One AC checks the post-condition. Verifying "even temporarily" requires intercepting writes during extraction (e.g., a fuse layer or a wrapped tar reader). If the PRD really means "even transiently," it needs either a stronger fixture or to be downgraded to "no files outside the resolved subpath are present after materialization completes" (which is already what AC verifies). Reconcile the wording.

9. **R34 (provenance readable without specialized tooling)**: No AC. Add one that opens the marker file with a generic tool (`cat`, jq if JSON, toml-cli if TOML) and asserts each documented field is present and human-readable.

10. **Known Limitations coverage**:
    - "Manual edits inside the snapshot are silently discarded on refresh" — no AC verifies this. Add one: "Given a snapshot exists, when a file is modified inside `<workspace>/.niwa/` and `niwa apply` runs against a source whose oid changed, the modification is gone after apply." This is critical because the PRD lists it as expected behavior.
    - "Tampered but-syntactically-valid snapshots are not detected" — out of v1 scope per PRD, no AC needed, but worth a comment in the Out of Scope section that the lack of an AC is intentional.
    - "Slug repo-root sentinel limitation" — the AC chain covers `owner/repo:` rejection but doesn't explicitly verify the documented workaround (maintainer removes a marker, ambiguity resolves). One AC walking the workaround would protect against regressions.
    - "Submodules and LFS in the source" — no AC verifies the documented behavior (silently not expanded). Add one to lock it in.

## Test Fixture Gaps

1. **GitHub tarball fake server (`tarball-fake-server`)**. Required by every AC under "GitHub tarball fetch" (R14, R16) and several under "Snapshot materialization." The Phase 2 maintainer-perspective research already named this as missing infrastructure. The PRD does not commit to building it. Decision needed: is the fixture inside the v1 scope, or are the GitHub-path ACs verified out-of-band (manual QA, separate integration suite)?

2. **HTTP conditional-GET fake** (ETag/`If-None-Match`/304 support). Subset of #1 but specifically called out by R16 and the related AC.

3. **Network-failure injection** (DNS unreachable, TCP refused, partial-read truncation). Required by the offline-default-branch AC (R19), the interrupted-refresh AC (R12), and any future strict-refresh tests. niwa today has no equivalent. Could be partially served by pointing at unroutable IPs or by extending `localGitServer` to support fault modes.

4. **Disk-full / permission-denied injection**. Required by the interrupted-refresh AC (R12). No standard pattern in niwa's test suite today. Could be covered by injecting a `chmod 0444` on the parent directory mid-test, but this needs a steps_test.go primitive.

5. **State-file fixture set for migration tests** (R20, R21, R22). The PRD assumes v1/v2/v3 InstanceState files can be authored by the test for the lazy-upgrade and forward-version-rejection ACs. niwa's existing test suite doesn't have a "given an InstanceState with schema_version=N" step yet — a small but real gap.

6. **A `.git/`-bearing legacy `<workspace>/.niwa/`** (R23). The migration AC ("working tree to snapshot") needs the test to set up a real git working tree inside the sandboxed workspace. This is achievable with `localGitServer` plus an actual `git clone` step, but no current step composes that — needs to be added.

7. **Secrets-guardrail trigger fixture** (R26). The plaintext-secrets guardrail AC requires a config repo whose provenance marker names a public-host tuple AND a `workspace.toml` containing a plaintext secret. Achievable with existing helpers but no current scenario uses this combination.

## Summary

The PRD has 10 ACs that aren't testable as written and roughly 10 requirements
or documented limitations with no testable AC at all. The dominant cause is a
single missing piece of test infrastructure — a GitHub-tarball fake server
with conditional-GET and fault-injection support — that the PRD's GitHub-path
ACs (R14, R16, R32) cannot be verified without. Recommended action: add a
`Test Strategy` subsection to the PRD that names the fixtures (`tarballFakeServer`,
fault-injection seams, state-file factory) as in-scope deliverables, then
revise each flagged AC to reference the fixture and an objective observable.
The known-limitation ACs (silent edit discard on refresh, slug repo-root
ambiguity workaround) should be added so the documented behavior has a
regression guard.
