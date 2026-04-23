# Implementation Plan: Issue 4 — full clone-primitive replacement

## Goal

Make `EnsureConfigSnapshot` (+ a real non-GitHub fallback) the only path that
materializes config dirs and overlays. Delete the legacy `git pull --ff-only`
code that backs SyncConfigDir, CloneOrSyncOverlay, and the Cloner.Clone* call
sites in init/config_set. After this lands, PRD #72 (force-push survival) is
structurally impossible because no code path in apply/init/config_set uses
`git pull --ff-only` against a config dir or overlay.

## Step-by-Step Approach

### Step 1: `internal/workspace/fallback.go` (new)

API (single exported function):
```go
// FetchSubpathViaGitClone implements the non-GitHub branch of
// EnsureConfigSnapshot: shallow-clone the repo into a temp dir,
// resolve HEAD oid, copy <cloneDir>/<src.Subpath> into stagingDir
// using ExtractSubpath-equivalent security (regular files only,
// path containment, refuse symlinks), and return the resolved oid.
//
// stagingDir must exist and be empty. cloneDepth defaults to 1.
// Caller writes the provenance marker into stagingDir and calls
// SwapSnapshotAtomic.
func FetchSubpathViaGitClone(ctx context.Context, src source.Source, stagingDir string) (oid string, err error)
```

Implementation outline:
- `os.MkdirTemp("", "niwa-fallback-")` for clone target. `defer os.RemoveAll`.
- `git clone --depth 1 [--branch <ref>] <CloneURL> <tmp>` (use src.CloneURL()).
- `git -C <tmp> rev-parse HEAD` for oid.
- Walk `<tmp>/<src.Subpath>` (or `<tmp>` when subpath empty); copy each entry into stagingDir at the same relative path. Reuse the same defenses ExtractSubpath uses: skip non-regular entries (symlinks, devices, FIFOs); validate filename (no NUL, no backslash, no `..`); safeJoin into stagingDir.
- Return resolved oid.

`testfault.Maybe("fetch-fallback")` hook at entry for parity with the GitHub path.

### Step 2: Wire fallback into snapshotwriter.go

Two integration points:

1. `materializeAndSwap`: today it always calls `FetchTarball`. Branch on `src.IsGitHub()`:
   - GitHub → existing `FetchTarball + ExtractSubpath` path.
   - Non-GitHub → new `FetchSubpathViaGitClone` (which writes into staging directly; no separate extract step).
   - Provenance marker write + `SwapSnapshotAtomic` are common to both.

2. `refreshSnapshot`: today the non-GitHub branch calls `refreshSnapshotFallback` (no-op stub). Replace with: skip the cheap drift check (no per-host equivalent of `HeadCommit` for non-GitHub), call `materializeAndSwap` (which now handles non-GitHub via Step 1) — or do `git ls-remote` for drift check. Prefer the simpler "always re-clone shallow" until v1 telemetry tells us drift checks are needed.

Delete the `refreshSnapshotFallback` stub.

### Step 3: Delete SyncConfigDir + apply.go fallthrough

`internal/workspace/apply.go`:
- Replace lines 279-292 (the `EnsureConfigSnapshot` pre-step + `SyncConfigDir` fallthrough) with a single `EnsureConfigSnapshot` call that returns its error directly:
  ```go
  if err := EnsureConfigSnapshot(ctx, configDir, a.fetcher(), a.Reporter); err != nil {
      return err
  }
  ```
  Helper `a.fetcher()` returns the FetchClient cast (or nil; `EnsureConfigSnapshot` handles nil). Inline if helper not warranted.
- Same treatment for the global config dir at lines 624-632 — replace `SyncConfigDir` with `EnsureConfigSnapshot`.
- Decision: keep `--allow-dirty` semantics? Today `SyncConfigDir` checks for dirty working tree. Once snapshot is the only model, "dirty" doesn't apply (snapshots are read-only). Remove the dirty check; the deprecation notice already warns this is going away in v1.1.

`internal/workspace/configsync.go`: delete the file. Update any tests that import it.

### Step 4: Rewrite CloneOrSyncOverlay

`internal/workspace/overlaysync.go`:
- New signature: `EnsureOverlaySnapshot(ctx, src source.Source, overlayDir string, fetcher FetchClient, reporter *Reporter) (wasFreshClone bool, err error)`.
- Behavior:
  - If marker exists at overlayDir → drift-check + refresh (`refreshSnapshot` semantics; `wasFreshClone=false`; pull-style failure surfaces as hard error).
  - If marker absent and dir is empty (or doesn't exist) → fresh materialization (`materializeAndSwap` against the parsed source; `wasFreshClone=true`; failure is silent-skip per R37, return nil err).
  - If marker absent and dir contains `.git/` → R28 lazy conversion via `lazyConvertWorkingTree` (treat as drift-check follow-on; `wasFreshClone=false`).
- Callers (in init.go) consume `wasFreshClone` to decide whether to surface failures (today's `wasCloneAttempt` semantics).
- Keep `HeadSHA` (still useful for callers who want the oid).
- Drop `isValidGitDir` (no longer needed — marker presence is the discriminator).

### Step 5: Replace init.go and config_set.go Cloner sites

`internal/cli/init.go:158` (workspace config first-clone via `--from`):
- Today: `cloner.CloneWith(ctx, cloneURL, niwaDir, CloneOptions{Depth: 1}, ...)` produces a `.git/` working tree.
- Replace: parse `--from` slug → call new helper that materializes into `niwaDir` (which is `<workspace>/.niwa`) using `materializeAndSwap` (or its public equivalent). Result: `niwaDir` is a snapshot with marker, not a working tree.
- Helper exposed: `workspace.MaterializeFromSource(ctx, src, configDir, fetcher, reporter) error`.

`internal/cli/init.go:322,340` (overlay first-clones during init):
- Replace `CloneOrSyncOverlay(url, overlayDir)` with `EnsureOverlaySnapshot(ctx, parsedSrc, overlayDir, fetcher, reporter)` per Step 4.

`internal/cli/config_set.go:64`:
- Today: `cloner.Clone(ctx, cloneURL, globalConfigDir, ...)` for the personal-overlay clone.
- Replace: `MaterializeFromSource(ctx, src, globalConfigDir, fetcher, reporter)` — personal overlay gets a marker too.

Cloner usage that remains (workspace-repo clones for `kind: clone` repos) stays untouched — that's a different concern (Step 6 confirms).

### Step 6: Audit sync.go:86

`internal/workspace/sync.go` has the `git pull --ff-only origin <branch>` that backs `Sync()` — used by Applier for `kind: clone` workspace repos (the actual user-code repos in the workspace, not the config dir). Confirmation:
- Caller chain: `Applier.applyClonedRepo` → `Sync` → `git pull --ff-only`.
- This is OUT OF SCOPE for the workspace-config-sources redesign. The PRD's snapshot model is for CONFIG sources, not workspace user repos.
- Action: leave with a one-line comment clarifying scope so future readers don't conflate the two concerns.

### Step 7: Tests

Test surface to update:
- `internal/workspace/configsync_test.go` (if exists) — delete with the file.
- `internal/workspace/overlaysync_test.go` — rewrite for new `EnsureOverlaySnapshot` signature.
- `internal/workspace/snapshotwriter_test.go` — extend with non-GitHub branch coverage.
- New: `internal/workspace/fallback_test.go` — unit tests for `FetchSubpathViaGitClone` using `localGitServer` (test/functional helper) or a `t.TempDir()` bare repo built with exec'd git commands.
- `internal/cli/init_test.go`, `config_set_test.go` — update for new signatures.
- `internal/workspace/apply_test.go` — verify the SyncConfigDir → EnsureConfigSnapshot swap doesn't regress instance.json reads (Issue 5 risk).

Functional tests (`test/functional/...`) that exercise the full apply pipeline will exercise both paths via `tarballFakeServer` (GitHub) and the new fallback (against `localGitServer`).

## Key Decisions (during planning)

- **Drop --allow-dirty semantics from EnsureConfigSnapshot path.** Snapshots are read-only by definition; "dirty" is a working-tree concept. Deprecation notice for the flag remains (Issue 6 already landed it).
- **No drift-check optimization for non-GitHub fallback in v1.** Always re-clone shallow on apply when the source is non-GitHub. `git ls-remote` for HEAD-comparison is a v1.1 optimization; PRD doesn't require it.
- **Public `MaterializeFromSource` helper in workspace package.** init.go and config_set.go need a way to do "first materialization" without an existing dir. Cleaner than exposing `materializeAndSwap` which is currently unexported and named for the refresh case.
- **EnsureOverlaySnapshot returns wasFreshClone, not wasCloneAttempt.** Callers (init.go) need to know whether a soft-skip is appropriate — same semantics as today, renamed for the snapshot model.

## Risks

- **State-file collision (Issue 5 spillover):** `instance.json` lives at `<workspace>/.niwa/instance.json`, same parent the snapshot writer swaps. `SwapSnapshotAtomic` swaps the entire `.niwa` dir; if instance.json is inside, it gets clobbered on first refresh. Mitigation: verify `SwapSnapshotAtomic` either preserves instance.json across the swap (read-then-rewrite into the staging dir) or document this as a known regression resolved by Issue 5's relocation. The latter only works if Issue 5 ships before any user runs apply.
- **Cloner usage in init.go for workspace-repo `kind: clone` and Applier.applyClonedRepo:** these are separate from config-dir clones. Confirm grep doesn't accidentally remove the wrong call sites.
- **Existing tests may rely on `.git/` presence for assertions.** Replacing with marker presence may break test fixtures. Adapt rather than delete.

## Acceptance Criteria (from Plan + TODO)

1. `grep -rn "pull --ff-only" internal/cli internal/workspace` → only matches in sync.go (workspace-repo, with comment).
2. `internal/workspace/configsync.go` deleted.
3. `internal/workspace/fallback.go` exists with `FetchSubpathViaGitClone`.
4. `internal/workspace/overlaysync.go` exports `EnsureOverlaySnapshot`; no `git pull` invocations.
5. `internal/cli/init.go` and `internal/cli/config_set.go` no longer call `Cloner.Clone*` for config dirs.
6. `go test ./...` clean (existing + new fallback tests).
7. wip/TODO-workspace-config-sources.md Issue 4 section: all checkboxes ticked.
