# TODO: workspace-config-sources remaining work

PR #73 / branch `docs/workspace-config-sources`. Tracking the gap between
PLAN-workspace-config-sources.md and what actually shipped in the 24 commits
already on the branch. Items are ordered by the original critical path
(Issue 4 → 5 → 6-tail → 8 → 7) — pick them off the top.

---

## Issue 4 — Snapshot writer + clone-primitive replacement (DONE)

Landed under "Option A: full replacement" (decided via /decision in
this session). Summary of what shipped:

- `internal/workspace/fallback.go` — `FetchSubpathViaGitClone` does
  shallow git clone + subpath copy + ExtractSubpath-equivalent security
  (positive type allowlist, filename validation, path containment,
  per-file size cap, .git stripped from snapshot).
- `internal/workspace/snapshotwriter.go` — `materializeAndSwap`
  dispatches on `src.IsGitHub()`: GitHub uses tarball, everything else
  routes through `FetchSubpathViaGitClone`. New public
  `MaterializeFromSource` for first-time materialization. The
  `refreshSnapshotFallback` stub is gone.
- `internal/workspace/configsync.go` — DELETED. `SyncConfigDir` was the
  carrier for `git pull --ff-only` against config dirs.
- `internal/workspace/apply.go` — `EnsureConfigSnapshot` is now the only
  sync strategy (no fallthrough to legacy primitives) for both the
  workspace config dir and the personal-overlay (global) config dir.
- `internal/workspace/overlaysync.go` — rewritten as
  `EnsureOverlaySnapshot(ctx, urlSlug, dir, fetcher, reporter) (wasFreshClone, err)`.
  Overlays now write provenance markers and refresh through the same
  pipeline. `HeadSHA` reads the marker first, falls back to
  `git rev-parse HEAD` for legacy working trees during the migration.
  `ParseSourceURL` exposed for init/config-set callers.
- `internal/cli/init.go` — `--from` clone uses
  `workspace.MaterializeFromSource` (no more `Cloner.CloneWith`).
  Overlay first-clones use `EnsureOverlaySnapshot`. New helper
  `parseInitSource` normalizes slug-or-URL into a typed source.
- `internal/cli/config_set.go` — personal-overlay clone uses
  `MaterializeFromSource`. Personal overlay now gets a marker too,
  closing the "personal overlay reproduces #72 in miniature" gap.
- `internal/workspace/sync.go:86` — confirmed workspace-repo path
  (`kind: clone` user repos), not config-dir. Out of scope; comment
  added to clarify scope split for future readers.
- Tests: `overlaysync_test.go` rewritten for `EnsureOverlaySnapshot`;
  `apply_test.go` + `apply_vault_test.go` `cloneFn` signatures
  updated for the ctx-bearing field type; new `fallback_test.go`
  covers whole-repo extraction, subpath filtering, missing-subpath
  rejection, symlink-skip, traversal rejection, and `splitLocalPath`.
  All `go test ./...` packages green.

**Acceptance**: ✅ `grep -rn "pull --ff-only" internal/cli internal/workspace`
returns only `internal/workspace/sync.go:86` (workspace-repo, scope-
documented). ✅ `Cloner.CloneWith` and `CloneOrSyncOverlay` no longer
called from the CLI for config dirs. ✅ `go test ./...` clean.

---

## Issue 5 — instance.json relocation (PARTIAL)

**Plan said**: bump schema to v3, add `ConfigSource` block, *and* relocate
`instance.json` from `<workspace>/.niwa/instance.json` to
`<workspace>/.niwa-state/instance.json`. v2 files lazy-upgrade. Dual-path
lookup during the transition. One-time `note:` on first relocation.

**What landed**: schema bumped to 3, `ConfigSource` field added,
`SaveState`/`LoadState` round-trip the new field. Forward-version
rejection lands. Registry mirror fields landed with `PopulateMirror()`.

**Why partial**: there is a literal `TODO(workspace-config-sources Issue 5)`
comment in `internal/workspace/state.go:19` for the path relocation. The
file still lives at `<workspace>/.niwa/instance.json`, which means the new
snapshot path `<workspace>/.niwa/` and the state file collide — every
`SwapSnapshotAtomic` call must currently preserve the state file across
the rename, or the swap clobbers it. This is a latent bug waiting to
trip on the first apply that swaps a snapshot in.

**Concrete remaining work**:

- [ ] Rename the constant in `internal/workspace/state.go:19` from
  `.niwa` to `.niwa-state`; introduce `LegacyStateDir = ".niwa"` for
  fallback reads.
- [ ] Update `LoadState`, `DiscoverInstance`, `EnumerateInstances` to try
  the new path first, fall back to the legacy path. Audit ~30 callers
  (per the compaction summary) — most go through these three entry points,
  but some `cmd/niwa/...` wiring may construct paths directly.
- [ ] In `SaveState`: write to the new path; if the legacy file exists,
  `os.Rename` it out of `.niwa/` and emit a one-time
  `DisclosedNotices.Emit("state-relocated", ...)` `note:`.
- [ ] Verify `SwapSnapshotAtomic` no longer needs to preserve
  `instance.json` after relocation — and document the invariant in a
  comment at the swap site.
- [ ] Add `internal/workspace/state_test.go` cases: v2→v3 lazy migration
  preserves unrelated fields (PRD AC-X1), forward-version rejection,
  dual-path lookup with both files present (new wins), legacy-only
  workspace upgrades on next save.
- [ ] Verify all `*_test.go` callers of `SaveState`/`LoadState` are
  unaffected (they use `t.TempDir()` so the path change is transparent,
  but confirm).

**Acceptance**: fresh workspaces have no `.niwa/instance.json`; existing
v2 workspaces migrate on next save with a one-time notice; both formats
load.

---

## Issue 6 — CLI updates tail (PARTIAL)

**What landed**: `niwa init` validates `--from` via `source.Parse`;
`niwa apply` URL-change `--force` gate; `--allow-dirty` deprecation
notice; `niwa reset` reads provenance marker; guardrail reads marker
tuple.

**Concrete remaining work**:

- [ ] **`niwa config set global`**: `internal/cli/config_set.go:64-65`
  still uses `Cloner.Clone` for the personal-overlay clone. Rewire to
  the snapshot writer so the personal overlay also gets a provenance
  marker (and is subject to the same drift-check + force-push survival
  guarantees as the workspace config). Without this, the personal
  overlay reproduces #72 in miniature.
- [ ] **`niwa status` overlay slug line (R36)**: `status.go` shows the
  source line for the workspace config (R20 — landed) but doesn't show
  the discovered overlay slug on its own line. Needs a second
  `fmt.Fprintf` keyed off the overlay's provenance marker (once overlays
  write one — depends on Issue 4 overlay rewrite).
- [ ] **`niwa apply` R28 lazy conversion notice**: verify the one-time
  `note:` actually fires. The conversion code path exists in
  `lazyConvertWorkingTree` but the notice emission needs an audit trail
  — grep for `DisclosedNotices` use around the conversion call.
- [ ] **`niwa apply` R27 name match**: when URL changes are forced,
  validate the new source's `[workspace].name` matches the registered
  name and refuse with diagnostic if not. Verify this is in
  `checkConfigSourceURLChange` or add it.
- [ ] **`internal/cli/*_test.go`** updates for the new behaviors above.

**Acceptance**: all four `niwa` commands (`init`, `apply`,
`config set global`, `status`) produce or read provenance markers
end-to-end without touching `.git/`.

---

## Issue 8 — Test infrastructure tail (PARTIAL)

**What landed**: `test/functional/tarball_fake_server.go` and
`tarball_fake_server_test.go` (the Go-level integration test).

**Concrete remaining work**:

- [ ] `test/functional/state_factory.go`: `WriteInstanceStateAtVersion(dir
  string, version int, body string) error` so Gherkin steps can plant a
  v2 state file and assert v3 lazy-upgrade.
- [ ] `test/functional/steps_workspace_config_sources.go`: Gherkin step
  bindings for: configuring `tarballFakeServer` responses, asserting
  request counts (e.g. "the second apply made zero tarball requests"),
  asserting marker file contents, triggering URL-change scenarios,
  asserting deprecation notices on stderr.
- [ ] `test/functional/features/workspace-config-sources.feature` with
  `@critical`-tagged scenarios:
  - subpath fetch happy path
  - **force-push survival (PRD #72 regression)** — the headline scenario
  - ambiguous-discovery rejection (`workspace.toml` AND `niwa.toml`
    both present at root)
  - explicit-subpath bypass of ambiguous discovery
  - v2-to-v3 state migration preserves unrelated fields
  - URL-change `--force` gate refuses without flag, accepts with it
  - same-URL legacy working-tree lazy conversion (no `--force` needed)
- [ ] `make test-functional` passes (and `make test-functional-critical`
  exercises the `@critical` scenarios above).

**Acceptance**: PRD #72 regression has a Gherkin scenario that fails
against `main` and passes against this branch.

---

## Issue 7 — Final cleanup (PENDING)

Do this last, after 4/5/6/8 are done.

- [ ] `go fmt ./...` clean.
- [ ] `go vet ./...` clean.
- [ ] `go test ./...` clean.
- [ ] `make test-functional` clean.
- [ ] `wip/` empty (this file gets deleted as the last step of the last
  remaining-work commit).
- [ ] PRD frontmatter + body status: `In Progress` → `Done`. Use
  `.claude/plugins/cache/shirabe/shirabe/0.4.1-dev/scripts/transition-status.sh`.
- [ ] Design frontmatter + body status: `Accepted` → `Done`. Same script.
  This will move the file into `docs/designs/current/`.
- [ ] Final commit pushed; PR description updated to reflect that the
  legacy clone path is gone and #72 is closed structurally.

---

## Things deliberately out of scope for this branch

- **Per-host adapters** (GitLab, Bitbucket, Gitea, GHE) — PRD Out of Scope
  for v1; ship GitHub-tarball + git-clone fallback only.
- **Polishing** the legacy `internal/workspace/sync.go:86` working-tree
  sync — that's the workspace-repo sync, not the config-dir sync. Out of
  scope unless Issue 4 audit reveals it touches config dirs.
