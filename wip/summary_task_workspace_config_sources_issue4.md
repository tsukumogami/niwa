# Summary: Issue 4 — Snapshot writer + clone-primitive replacement

## Outcome

Issue 4 from PLAN-workspace-config-sources.md complete under "Option A:
full replacement" (decided via /decision in this session). The legacy
`git pull --ff-only` paths are no longer reachable from any config-dir
or overlay sync code, which makes PRD #72 (force-push survival)
structurally impossible against config sources. Workspace-repo sync
(`kind: clone`) remains on the legacy path by design and is annotated
to clarify scope.

## Files

**New**
- `internal/workspace/fallback.go` — non-GitHub git-clone-and-copy
  fallback. Public `FetchSubpathViaGitClone(ctx, src, stagingDir) (oid, err)`.
  ExtractSubpath-equivalent security: positive type allowlist, filename
  validation (NUL/backslash/`..` rejected), path containment, per-file
  size cap (500 MB), `.git` stripped from snapshot.
- `internal/workspace/fallback_test.go` — whole-repo, subpath,
  missing-subpath, symlink-skip, traversal-rejection, splitLocalPath.

**Modified**
- `internal/workspace/snapshotwriter.go` — `materializeAndSwap`
  dispatches on `src.IsGitHub()`; new public `MaterializeFromSource`
  for first-time materialization; `EnsureConfigSnapshot` now handles
  nil fetcher gracefully (non-GitHub doesn't need it); rename-redirect
  notice still surfaced via reporter.
- `internal/workspace/apply.go` — `EnsureConfigSnapshot` is the only
  sync strategy. Two call sites collapsed (workspace config dir +
  global/personal overlay dir). `cloneOrSync` field signature now
  takes `ctx context.Context` so the closure binds to Applier-wired
  fetcher + reporter without losing cancellation.
- `internal/workspace/overlaysync.go` — `EnsureOverlaySnapshot`
  replaces `CloneOrSyncOverlay`; signature
  `(ctx, urlSlug, dir, fetcher, reporter) (wasFreshClone, err)`.
  `parseOverlaySlug` accepts org/repo, https://, git@, and file:// (the
  last with synthetic owner/repo via `splitLocalPath` so the marker
  schema is satisfied). `HeadSHA` reads provenance marker first, falls
  back to `git rev-parse HEAD` for legacy working trees.
  `ParseSourceURL` exposed as the public alias for init/config-set.
- `internal/cli/init.go` — `--from` clone uses
  `workspace.MaterializeFromSource`; overlay first-clones use
  `workspace.EnsureOverlaySnapshot`. New `parseInitSource` helper
  normalizes slug-or-URL.
- `internal/cli/config_set.go` — personal-overlay clone uses
  `MaterializeFromSource`. Personal overlay now writes a marker, closing
  the "personal overlay reproduces #72 in miniature" gap.
- `internal/workspace/sync.go` — comment added at `PullRepo`
  clarifying it backs the `kind: clone` workspace user-repo path, not
  the config dir (out of scope for this task).
- `internal/workspace/{overlaysync_test,apply_test,apply_vault_test}.go`
  — adapted for new signatures.

**Deleted**
- `internal/workspace/configsync.go` — `SyncConfigDir` was the carrier
  for `git pull --ff-only` against config dirs.

## Test Results

All packages green:
```
ok  internal/cli
ok  internal/config
ok  internal/github
ok  internal/guardrail
ok  internal/secret
ok  internal/secret/reveal
ok  internal/source
ok  internal/testfault
ok  internal/vault, vault/fake, vault/infisical, vault/resolve
ok  internal/workspace
ok  test/functional
```

`go fmt ./...` and `go vet ./...` clean.

## Diff Footprint

411 lines added, 252 lines deleted across 10 files modified, 2 new, 1
deleted. The net growth comes from the new `fallback.go` (~270 lines
including the `cloneAndCopy` shared helper) and `fallback_test.go`
(~175 lines).

## Decisions Captured

3 non-obvious decisions recorded via `koto decisions record`:

1. **Option A: full replacement** over phased deprecation, config-only
   replacement, or interface dispatcher — see `/decision` framework
   output earlier this session.
2. **Drop --allow-dirty semantics from EnsureConfigSnapshot path** —
   snapshots are read-only; dirty is a working-tree concept. Flag
   deprecation notice (already in CLI) covers user-facing transition.
3. **No drift-check optimization for non-GitHub fallback in v1** —
   always re-clone shallow. `git ls-remote` HEAD-comparison is a v1.1
   candidate; PRD scope is GitHub-first-class + git-clone fallback,
   not per-host cheap drift checks.

One in-flight decision documented in code:

4. **Synthesize Owner/Repo from local file paths** (`splitLocalPath`)
   so file:// URLs satisfy the provenance-marker schema without a
   parallel raw-URL marker variant. Keeps the schema uniform across
   sources.

## Deferred (out of scope for this task)

- Issue 5: `instance.json` relocation to `.niwa-state/`. State-file
  collision under `SwapSnapshotAtomic` is a latent risk until Issue 5
  ships (see TODO file). The current `SwapSnapshotAtomic` swaps the
  whole `.niwa/` directory; instance.json will be clobbered on first
  refresh of a snapshot. Mitigation: relocate in Issue 5 before any
  user runs apply against a marker-bearing config dir.
- Issue 6 tail: `niwa status` overlay-slug line (R36) requires markers
  on overlays — unblocked by this task. Still pending.
- Issue 8: Gherkin scenarios for the new ACs.
- Issue 7: PRD/Design status transitions to Done.

## Branch State

`docs/workspace-config-sources` (reused per user instruction). 24
commits already on the branch from prior work; this task adds 1+ more
commits when committed.
