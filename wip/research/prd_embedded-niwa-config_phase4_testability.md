# Testability Review

## Verdict: FAIL

The PRD is mostly well-instrumented, but several criteria are non-mechanical (require subjective judgment or undefined contracts), several requirements (R7, R16) have no AC, and the "empty `.niwa/`" edge case that the upstream PRD spelled out as AC-D7 is silently dropped.

## Untestable Criteria

1. **AC-D5** ("The on-disk `<workspace>/` is removed (or unchanged if the init was aborted pre-Mkdir)"): the parenthetical gives the implementation two acceptable end states with no rule for which one applies when. A test cannot assert "removed OR unchanged" without re-reading the implementation. -> Pick one contract: e.g., "the on-disk `<workspace>/` does not exist after the failed init" (and require pre-Mkdir abort), or split into two ACs keyed on which phase aborted.

2. **AC-M4** ("prints a suggested slug to stdout and asks the user to confirm via stdin (interactive mode), or applies the rewrite immediately when `--yes` is passed"): the contract for the suggestion is undefined. The PRD never says what slug is inferred from what input, e.g., if the current `source_url` is `acme/dot-niwa`, what slug does the probe suggest? The probe target isn't even specified (probe the current source? a different source the user must pass?). The interactive-confirmation prompt string is unspecified, so stdin/stdout fixturing is impossible. -> Specify (a) which source is probed when `--to` is omitted, (b) the deterministic rule that maps probe result to suggested slug, (c) the exact prompt substring and the accepted "yes" tokens, and (d) the exit code and stderr substring when the probe fails to find an unambiguous target.

3. **AC-P2** ("No temporary files outside the staging area contain content from outside `.niwa/`"): "outside the staging area" is not a defined path. A test cannot enumerate "all temporary files" without an implementation-internal probe. -> Bind to a concrete observable: e.g., "after init succeeds, `find $TMPDIR/niwa-* -type f` returns no paths" or "the niwa process opens no file outside `$workspace/` and `$TMPDIR/niwa-*` during init" (strace-based) — and accept that the implementation must keep all scratch under a named prefix.

4. **AC-G1 / AC-G2 / AC-G3** ("contains a section titled X **or equivalent heading anchor**"): the "or equivalent" clause makes the assertion subjective. Either the heading is fixed (grep-able) or it isn't. -> Drop "or equivalent"; require an exact heading anchor (e.g., `#single-repo-workspace`, `#brain-repo`, `#niwa-migrate-source`).

5. **AC-R2** ("a section named (or a section heading anchor containing) `niwa.toml`"): same subjectivity issue as AC-G* plus no required content — a one-word heading with empty body would pass. -> Require an exact anchor AND a substring assertion on the body (e.g., must contain the words "rank 3" and "migrate to `.niwa/workspace.toml`").

6. **AC-B1** ("the on-disk `.niwa/` (if it was a legacy working tree) is converted per upstream R28"): the verification predicate hides behind "per upstream R28". A test plan reader without the upstream PRD cannot know what "converted" means observably. -> Inline the observable, e.g., "after apply, `<workspace>/.niwa/.git/` does not exist AND the provenance marker file at `<workspace>/.niwa/<marker-path>` exists with `fetch_mechanism = github-tarball`."

## Missing Test Coverage

1. **R7** (decompression-bomb cap and security defenses apply to the probe pass) has no dedicated AC. AC-P3 covers truncated tarballs; nothing covers an oversized tarball that should be rejected by the cap during the probe scan. -> Add an AC: "Given a `tarballFakeServer` source whose decompressed size exceeds the 500 MB cap, `niwa init` exits non-zero with the standard cap-exceeded diagnostic AND no snapshot is materialized; the failure occurs during the probe pass (verifiable by tarball-bytes-read counter)."

2. **R16** (no observable latency over pre-PRD code path) has no AC. R16 is explicitly a non-functional requirement, but the PRD's own conventions promise binary pass/fail. Either downgrade R16 to a Known Limitation or add a request-count AC (already partially covered by AC-P1's "exactly one tarball request" — make this dependency explicit by tagging AC-P1 as also verifying R16).

3. **Empty `.niwa/` subdirectory edge case (upstream AC-D7 analog)** is dropped. Upstream AC-D7 said: source has `.niwa/` directory but no `.niwa/workspace.toml` inside it, AND a root `workspace.toml` → discovery resolves to rank 2. The new PRD's AC list never restates this. With rank-3 removed, the rule is presumably the same, but it's not asserted. -> Add: "Given a `tarballFakeServer` source containing a `.niwa/` directory (no `.niwa/workspace.toml` inside) AND a root `workspace.toml`, discovery resolves to rank 2; `source_subpath = ""` and the whole-repo materializes. No ambiguity error fires."

4. **The dual edge case** (root has `workspace.toml` AND a `.niwa/` directory that contains no `workspace.toml`): equivalent to the above; explicitly call it out so testers know an empty `.niwa/` is not a rank-1 marker by mere directory presence.

5. **AC-D3 ambiguity bypass**: covers explicit subpath beating discovery when all three layouts exist. Missing the inverse: an explicit subpath against a source that has neither rank-1 nor rank-2 markers but DOES have a `workspace.toml` at the explicit subpath → should succeed silently with no discovery error. -> Add an AC or expand AC-D3.

6. **R9** ("`niwa migrate-source` MUST NOT trigger an apply, MUST NOT delete the on-disk `.niwa/`, MUST NOT touch the source repo"): AC-M1 covers the on-disk-`.niwa/` part. Nothing covers "MUST NOT trigger an apply" or "MUST NOT touch the source repo" as separate observables. -> Add an AC asserting the source-repo fixture's request count remains zero after `migrate-source` returns (no network calls to the source), and that no snapshot mutation occurs (no `<workspace>/.niwa.next/` staging directory created).

7. **AC-M3 ordering**: asserts that after `migrate-source --to ... && niwa apply --force`, the new state is correct. Doesn't assert what happens if the user runs `niwa apply` *without* `--force` between the two — R10 says it must refuse. -> Add an AC: "Between `migrate-source` and `apply --force`, a bare `niwa apply` exits non-zero with the upstream-R26 source-URL-changed diagnostic."

8. **AC-D4** asserts non-zero exit and stderr substrings but does not assert that the on-disk `<workspace>/.niwa/` is unchanged. Since AC-D7 (network-error case) does assert this, the same predicate should apply to the "explicit subpath has no workspace.toml" case for consistency with R5. -> Extend AC-D4 with the byte-identical predicate.

## Summary

The PRD's discovery and backwards-compatibility ACs are tight and mechanical. The migration-tooling ACs (AC-M*) have one significant hole (AC-M4's inferred-slug contract is unspecified), and the documentation ACs (AC-G*, AC-R2) lean on subjective "or equivalent heading" language that prevents grep-based verification. R7 and R16 lack dedicated ACs, and the upstream PRD's "empty `.niwa/` directory" edge case is silently dropped — a tester relying only on this PRD would not exercise it. Fix AC-M4, AC-D5, AC-G*, AC-R2, and add ACs for R7 plus the empty-`.niwa/` edge case to reach PASS.
