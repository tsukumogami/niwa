---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-workspace-config-sources.md
issue_count: 9
---

# PLAN: Workspace Config Sources

## Status

Active

## Scope Summary

Implements the design doc's 11 implementation phases as 9 atomic issue
outlines, all landing on a single PR (the same PR carrying the PRD and
design). Single-pr mode per the user's "all in this same branch"
instruction; no GitHub issues or milestone created.

## Decomposition Strategy

**Horizontal** (layer-by-layer). The design's package boundaries are
already crisp — `internal/source/` is a leaf, `internal/testfault/` is
a leaf, the snapshot primitive composes both, the GitHub fetch path
extends an existing package, and the CLI surface composes everything.
Walking-skeleton was considered but rejected: integration risk is
manageable because each layer has a well-defined interface (per the
design's Key Interfaces section), and the failure modes are testable
at each layer's boundary. A horizontal sequence lets each layer be
fully unit-tested before the next consumes it.

The decomposition follows the design's Implementation Approach
verbatim, with the design's Phase 1+2 collapsed into one issue
(both leaf packages of similar small scope) and the design's Phases
9-10 collapsed into "CLI updates + .git replacement" (related concerns
touching the same set of CLI files).

## Implementation Sequence

Critical path runs Issue 1 → 2 → 3 → 4 → 5 → 6 → 7. Issues 8 (test
infrastructure) and 9 (documentation) can land in parallel with Issues
4-7 once the foundation (Issues 1-3) is in place.

```mermaid
graph TD
    I1[Issue 1: source + testfault]
    I2[Issue 2: snapshot + provenance]
    I3[Issue 3: github fetch + tar]
    I4[Issue 4: snapshot writer + clone replacement]
    I5[Issue 5: state v3 + registry mirror]
    I6[Issue 6: CLI updates + .git replacement]
    I7[Issue 7: final cleanup + push]
    I8[Issue 8: test infrastructure]
    I9[Issue 9: documentation]

    I1 --> I2
    I1 --> I3
    I2 --> I4
    I3 --> I4
    I4 --> I5
    I5 --> I6
    I6 --> I7
    I3 --> I8
    I8 --> I7
    I1 --> I9
    I9 --> I7
```

## Issue Outlines

### Issue 1: Foundation packages (`internal/source/`, `internal/testfault/`)

**Complexity**: testable

**Goal**: build two leaf packages that the rest of the redesign
depends on. `internal/source/` is the canonical slug parser
(typed `Source` struct, `Parse`/`String` round-trip, methods for
clone/tarball/commits URLs and overlay derivation). `internal/testfault/`
is the test-only fault-injection seam (`Maybe(label)` reads
`NIWA_TEST_FAULT`).

**Dependencies**: none

**Acceptance criteria**:
- `internal/source/source.go` defines `Source` struct with five fields (Host, Owner, Repo, Subpath, Ref) and methods `String`, `CloneURL`, `TarballURL`, `CommitsAPIURL`, `OverlayDerivedSource`, `DisplayRef`.
- `internal/source/parse.go` defines `Parse(string) (Source, error)` that satisfies PRD R3 strict parsing rules: rejects empty subpath after colon, malformed separator order, embedded whitespace, multiple `:` separators, multiple `@` separators.
- Round-trip exact for whole-repo slugs: `Parse(s).String() == s` for `s = "org/repo"`, `"org/repo@v1"`, `"org/repo:.niwa"`, `"org/repo:.niwa@v1"`.
- `internal/source/source_test.go` table-driven coverage of all R3 rejection cases plus round-trip property.
- `internal/testfault/testfault.go` defines `Maybe(label string) error` that returns nil unless `NIWA_TEST_FAULT` matches a fault spec for the label; spec format `<spec>@<label>[,<spec>@<label>]*`; supported specs `truncate-after:N`, `error:<message>`.
- `internal/testfault/testfault_test.go` covers default (env unset) no-op, single fault, multiple labels, malformed spec.
- Tests pass via `go test ./internal/source/... ./internal/testfault/...`.

### Issue 2: Snapshot primitive + provenance marker

**Complexity**: critical

**Goal**: build the atomic-swap primitive and the provenance marker
reader/writer. These are the workspace-package primitives that all
three clone sites compose with.

**Dependencies**: Issue 1 (uses `internal/testfault.Maybe`)

**Acceptance criteria**:
- `internal/workspace/snapshot.go` defines `swapSnapshotAtomic(target, staging string) error` implementing the two-rename swap with idempotent preflight cleanup of stale `.next/.prev/`.
- The swap calls `testfault.Maybe("snapshot-swap")` at start; injected faults leave the previous snapshot intact (preflight on next call cleans up).
- `internal/workspace/snapshot_test.go` covers happy path, preflight cleanup of stale dirs, fault-injection mid-swap, target-doesn't-exist (treats as fresh staging-only swap).
- `internal/workspace/provenance.go` defines `Provenance` struct, `WriteProvenance(snapshotDir string, p Provenance) error`, and `ReadProvenance(snapshotDir string) (Provenance, error)`. TOML format at `.niwa-snapshot.toml`.
- Marker fields: `source_url`, `host`, `owner`, `repo`, `subpath`, `ref`, `resolved_commit`, `fetched_at` (RFC 3339), `fetch_mechanism`.
- `internal/workspace/provenance_test.go` covers round-trip, missing required fields, malformed TOML.
- Constants added: `SnapshotDir = ".niwa"`, `StateDir = ".niwa-state"` (rename from previous `.niwa`).
- Tests pass via `go test ./internal/workspace/...`.

### Issue 3: GitHub fetch + tar extraction

**Complexity**: critical

**Goal**: extend `internal/github/APIClient` with the two new
methods and add the streaming tar extractor with all 7 security
defenses from the design's Security Considerations section.

**Dependencies**: Issue 1 (calls `testfault.Maybe`)

**Acceptance criteria**:
- `APIClient.HeadCommit(ctx, owner, repo, ref, etag) (oid, newETag string, statusCode int, err error)` issues `GET /repos/{owner}/{repo}/commits/{ref}` with `Accept: application/vnd.github.sha`.
- `APIClient.FetchTarball(ctx, owner, repo, ref, etag) (body io.ReadCloser, newETag string, statusCode int, redirect *RenameRedirect, err error)` issues `GET /repos/{owner}/{repo}/tarball/{ref}` with `If-None-Match: <etag>` when etag is non-empty; follows 301 once with chain inspection for `RenameRedirect{OldOwner, OldRepo, NewOwner, NewRepo}`.
- `NewAPIClient()` reads `NIWA_GITHUB_API_URL` env var when set; defaults to `https://api.github.com`. `GH_TOKEN` read once at construction.
- `internal/github/tar.go` exports `ExtractSubpath(r io.Reader, subpath, dest string) error` enforcing all 7 security defenses: positive type allowlist (TypeReg, TypeDir only), wrapper anchoring, subpath filter, path-containment check, filename validation (no NUL/`..`/leading-`/`/non-`/` separators), 500 MB decompression-bomb cap with per-entry and cumulative tracking via `io.LimitReader`.
- Calls `testfault.Maybe("fetch-tarball")` at request start, `testfault.Maybe("extract-entry")` per tar entry.
- `internal/github/client_test.go` and `internal/github/tar_test.go` cover both with httptest.Server (no live GitHub calls).
- Token never appears in error messages, log lines, or surfaced types (security invariant).
- Tests pass via `go test ./internal/github/...`.

### Issue 4: Snapshot writer + clone-primitive replacement

**Complexity**: critical

**Goal**: rewrite the three clone sites (`SyncConfigDir`,
`CloneOrSyncOverlay`, init's `Cloner.CloneWith` invocation) to compose
source + fetch + extract + provenance + atomic swap. Add the git-clone
fallback for non-GitHub hosts.

**Dependencies**: Issues 2 + 3

**Acceptance criteria**:
- `internal/workspace/configsync.go` rewritten: parses `Source` from registry, dispatches to `internal/github` for `github.com` hosts or to fallback for others, writes provenance marker into staging dir, calls `swapSnapshotAtomic`.
- `internal/workspace/overlaysync.go` rewritten with the same composition; silently skips on fetch failure (preserves today's behavior).
- `internal/workspace/fallback.go` (new) implements git-clone-fallback: clones to `os.MkdirTemp` dir, copies subpath into staging with the same security discipline as `ExtractSubpath` (regular files only, path containment), removes temp dir on success.
- All three clone sites call `swapSnapshotAtomic` to land the snapshot at canonical path.
- No `git pull --ff-only` invocations remain in `configsync.go` or `overlaysync.go`.
- The `Cloner.CloneWith` call site in `internal/cli/init.go` is replaced by a call to the new snapshot writer.
- `go test ./internal/workspace/...` passes (existing tests adapted; new tests for fallback path added).

### Issue 5: State schema v3 + registry mirror fields

**Complexity**: testable

**Goal**: bump `InstanceState` to schema v3 with `config_source` block;
relocate `instance.json` to `.niwa-state/`; add registry mirror fields
with lazy migration on next save.

**Dependencies**: Issue 4 (uses snapshot writer for the state-relocation
path)

**Acceptance criteria**:
- `InstanceState.SchemaVersion` bumps to 3; new `ConfigSource *ConfigSource` field with the documented 8-tuple plus URL.
- `LoadState`, `DiscoverInstance`, `EnumerateInstances` gain dual-path lookup (new path `<workspace>/.niwa-state/instance.json` first; fallback `<workspace>/.niwa/instance.json`).
- `SaveState` writes to the new path; on first save after migration, also `os.Rename` the legacy file out of `.niwa/` and emit a one-time `note:` via `DisclosedNotices`.
- v2 state files load successfully and lazy-upgrade on next save (per PRD R24, R28, R34).
- `schema_version > 3` rejected with diagnostic naming both versions; on-disk file unchanged.
- `RegistryEntry` gains `SourceHost`, `SourceOwner`, `SourceRepo`, `SourceSubpath`, `SourceRef` fields with `omitempty`. Lazy-populate from `source_url` on read; persist on next save with stderr warning if mirror disagreed (per PRD R22).
- `internal/workspace/state_test.go` covers v2→v3 lazy migration (preserves unrelated fields per PRD AC-X1), forward-version rejection, dual-path lookup.
- `internal/config/registry_test.go` covers lazy mirror upgrade, mirror reconciliation when hand-edited.
- All existing `go test ./...` continues to pass.

### Issue 6: CLI updates + `.git/` replacement + overlay discovery

**Complexity**: critical

**Goal**: wire the canonical `Source` parser through the CLI surface;
implement R26-R28 migration UX; replace the two `.git/`-dependent
guards; implement R35 overlay slug derivation; update `niwa status`.

**Dependencies**: Issue 5

**Acceptance criteria**:
- `niwa init`: parses `--from <slug>` via `internal/source.Parse`; uses snapshot writer; writes registry with parsed mirror fields.
- `niwa config set global`: same parsing + snapshot writer for the personal overlay clone.
- `niwa apply`: detects URL change against the on-disk provenance marker; refuses without `--force` when the on-disk dir is a legacy working tree (R26-R27); validates new source's `[workspace].name` matches registered name (R27); same-URL legacy working trees lazy-convert without `--force` (R28); emits one-time `note:` for conversions (R28).
- `niwa apply --allow-dirty` succeeds with stderr deprecation notice naming v1.1 removal (R32); notice printed once per process invocation.
- `niwa status` detail view displays source line with `(default branch)` annotation when ref-less (R20); displays overlay slug on its own line when an overlay was discovered (R36).
- `niwa reset`'s `isClonedConfig` reads provenance marker instead of `.git/` (R30); displays the URL it's about to re-fetch from (per security model).
- `internal/guardrail/githubpublic.go` `CheckGitHubPublicRemoteSecrets` reads provenance marker tuple instead of `git remote -v` (R31); fail-open on missing marker.
- Auto-discovered workspace overlay slug derived via `Source.OverlayDerivedSource()` per R35 (basename + `-overlay` rule).
- `internal/cli/*_test.go` updated for new behaviors. New tests cover the URL-change detection paths.
- All `go test ./...` passes.

### Issue 7: Final cleanup + push

**Complexity**: simple

**Goal**: run all checks, clean up wip/, transition PRD + design to
Done, push final commit.

**Dependencies**: Issues 6, 8, 9 all complete

**Acceptance criteria**:
- `go fmt ./...`, `go vet ./...`, `go test ./...` all clean.
- `wip/` empty (CI cleanup rule).
- PRD frontmatter + body status transitioned: In Progress → Done.
- Design frontmatter + body status transitioned: Accepted → Done.
- Final commit pushed to `docs/workspace-config-sources` branch.

### Issue 8: Test infrastructure (`tarballFakeServer`, scenarios)

**Complexity**: testable

**Goal**: build the test helpers and write Gherkin scenarios for the
new acceptance criteria. Can land in parallel with Issues 4-7 once
Issue 3 (which defines the GitHub client API the fake mirrors) is in.

**Dependencies**: Issue 3

**Acceptance criteria**:
- `test/functional/tarball_fake_server.go` defines `tarballFakeServer` helper around `httptest.NewServer` with methods to configure responses, status codes, ETags, redirects, and inspect the request log.
- `test/functional/state_factory.go` provides `WriteInstanceStateAtVersion(dir string, version int, body string) error` Gherkin-step backing.
- `test/functional/steps_workspace_config_sources.go` adds steps for: configuring `tarballFakeServer` responses, asserting request counts, asserting marker contents, triggering URL-change scenarios, asserting deprecation notices.
- `test/functional/features/workspace-config-sources.feature` covers `@critical` scenarios for: subpath fetch happy path, force-push survival (PRD #72 regression), ambiguous-discovery rejection, explicit-subpath bypass, v2-to-v3 state migration, URL-change `--force` gate, same-URL lazy conversion.
- `make test-functional` passes.

### Issue 9: Documentation

**Complexity**: simple

**Goal**: write the new guide and update existing ones per the PRD's
documentation outline.

**Dependencies**: Issue 1 (for early reference to source slug grammar);
otherwise can land in parallel with Issues 2-8.

**Acceptance criteria**:
- `docs/guides/workspace-config-sources.md` (new) covers: what you get, slug grammar, discovery rules, snapshot model, drift detection, provenance marker, failure modes, migration. Mirrors the structure of `vault-integration.md`.
- `docs/guides/functional-testing.md` updated with one paragraph about `tarballFakeServer`.
- `docs/guides/vault-integration.md` updated to reference the marker (not `git remote -v`) for the public-repo guardrail.
- `README.md` updated: shared-workspace-configs section reframes `.niwa/` as a snapshot, not a git checkout.
- `CLAUDE.md` (niwa-specific) adds the new guide to the Contributor Guides list.
