# TODO: workspace-config-sources remaining work

PR #73 / branch `docs/workspace-config-sources`. Tracking the gap between
PLAN-workspace-config-sources.md and what actually shipped on the branch.
Items are ordered by the (amended 2026-04-23) critical path:
Issue 4-followup → Issue 5 → Issue 6-tail → Issue 8 → Issue 7.

## 2026-04-23 — PRD/DESIGN/PLAN amendment in effect

User pushback during Issue 5 planning reframed the contract: `.niwa/` is a
managed assembly, not a directory mirror. Concretely:
- Niwa pulls only files referenced from `workspace.toml` (PRD R10b).
- `instance.json` stays at `<workspace>/.niwa/instance.json` — no relocation
  to `.niwa-state/` (Decision 2 reversed).
- The carry-over of `instance.json` across the swap is now the intentional
  "assembly contract" step, not a band-aid.

What this means for remaining work:
- Issue 4 shipped wholesale-pull semantics. **Issue 4-followup** (new) retools
  the writer/extractor/fallback to manifest-driven.
- Issue 5 shrinks to "schema v3 + registry mirror" — no relocation, no
  dual-path lookup.
- Issue 8 adds one Gherkin scenario asserting unreferenced files don't appear
  in the snapshot.

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

## Issue 4-followup — Manifest-driven fetch retool (NEW)

**Why this issue exists**: Issue 4 shipped wholesale-subpath pull. The
2026-04-23 amendment to PRD R10b says niwa MUST pull only files
referenced from `workspace.toml`. This issue closes the gap.

**Concrete work**:

- [ ] New `internal/workspace/manifest.go`. Define a small package-level
  table of path-bearing fields in the workspace.toml schema. Define
  `BuildManifest(cfg) []string` that walks the table and returns the
  union of (a) the workspace config filename, (b) explicit path
  references, (c) transitively-referenced paths.
- [ ] Modify the tarball extractor to accept a filter (an accept-set or
  callback). Retain a default that mirrors today's "everything under
  `<wrapper>/<subpath>/`" so the workspace-overlay-discovery path that
  doesn't have a manifest keeps working.
- [ ] Rewrite the snapshot writer's GitHub path to do two-phase
  extraction: buffer the tarball, extract `workspace.toml` only, parse
  it, compute manifest, extract manifested files. Discuss buffer-vs-
  refetch tradeoffs in a code comment.
- [ ] Rewrite the git-clone fallback equivalently: clone, read
  `workspace.toml`, compute manifest, copy only manifested files into
  staging.
- [ ] Rename `preserveInstanceState` to something that signals "assembly
  contract step" (e.g., `assembleNiwaLocalState`). Document the closed-
  set niwa-local file list at the call site.
- [ ] Add unit / functional test asserting AC-G1 revised: a tarball with
  `workspace.toml`, `README.md`, and `notes.txt` produces a snapshot
  containing only `workspace.toml` (plus marker, plus state).
- [ ] Add unit / functional test asserting AC-M1 revised: snapshot
  contains exactly the manifested files, marker, and niwa-local state.
- [ ] Audit existing `*_test.go` to confirm none assert presence of
  unreferenced files inside `.niwa/` — should be true today, but
  verify.

**Acceptance**: `find <workspace>/.niwa/ -type f` after apply against a
multi-file source returns only manifested files + provenance marker +
instance.json. `make test-functional` clean.

---

## Issue 5 — Schema v3 + registry mirror (COLLAPSED, mostly done)

**Plan amended 2026-04-23**: the relocation work that originally
dominated Issue 5 is gone. What remains is the schema bump and registry
mirror, both of which already landed on the branch.

**What landed**: schema bumped to 3, `ConfigSource` field added,
`SaveState`/`LoadState` round-trip the new field. Forward-version
rejection lands. Registry mirror fields landed with `PopulateMirror()`.

**Concrete remaining work**:

- [ ] Delete the `TODO(workspace-config-sources Issue 5)` comment in
  `internal/workspace/state.go:19` — relocation is no longer planned.
- [ ] Add `internal/workspace/state_test.go` cases: v2→v3 lazy
  migration preserves unrelated fields (PRD AC-X1), forward-version
  rejection.
- [ ] Confirm the assembly-contract carry-over (renamed in Issue 4-
  followup) is what handles the swap-collision concern; add a comment
  at the swap site that explicitly references the assembly contract
  rather than the (now-deleted) relocation plan.

**Acceptance**: schema test coverage in place; comment cleanup done;
no relocation logic exists in the codebase.

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
