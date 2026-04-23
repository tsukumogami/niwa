<!-- decision:start id="snapshot-swap-and-state-placement" status="assumed" -->
### Decision: Snapshot atomic-swap sequence and `instance.json` placement

**Context**

PRD R12 commits to "at no point is `<workspace>/.niwa/` absent or partially
populated" during snapshot refresh, and R13 requires the same posture
symmetrically across the team-config clone, the personal-overlay clone, and
the workspace-overlay clone. Today `<workspace>/.niwa/instance.json` lives
inside the directory that the new refresh path needs to swap. Two coupled
sub-questions must be answered together:

A. The rename sequence that satisfies R12 on Linux and macOS, given that
   POSIX `rename(2)` on a non-empty target directory fails on macOS/BSD and
   requires `renameat2(RENAME_EXCHANGE)` (Linux 3.15+) for a true atomic
   swap.

B. The on-disk location of `instance.json` once `<workspace>/.niwa/` becomes
   the disposable snapshot the refresh path renames. PRD Out of Scope
   defers placement; the contract is "state survives snapshot refresh."

The design must also preserve five existing properties: `DiscoverInstance`
walks up looking for the state marker; `EnumerateInstances` scans
workspace-root subdirectories for the marker; `EnsureInstanceGitignore`
writes `<workspace>/.gitignore` outside `.niwa/` already; backwards-compat
loading of v2 state files at the legacy path; and the v3 schema migration
the PRD R24 commits to.

**Assumptions**

- niwa runs on Linux and macOS only. Windows is not a v1 target (consistent
  with workspace context and existing code's POSIX assumptions in
  `gitutil.go` and the `os/exec` git invocations).
- A best-effort sequence with a sub-microsecond non-atomic window between
  two `rename(2)` syscalls is acceptable given PRD R12 explicitly permits
  it ("On platforms where atomic directory-swap is not available, niwa
  MUST use a documented best-effort sequence ... and accept the brief
  sub-microsecond non-atomic window").
- No second niwa process is reading the snapshot concurrently with refresh
  on the same workspace. niwa today has no cross-process locking and the
  PRD does not introduce one. Concurrent readers of `<workspace>/.niwa/`
  are workspace consumers (Claude Code agents, hooks, the contributor's
  editor) that read individual files, not directory-level observers
  watching for absence.
- `instance.json` is written exclusively by the niwa CLI process running
  apply / init / destroy. No external tooling writes to that path today;
  the PRD does not change this.
- Filesystem behavior is uniform within a single workspace root: refresh
  and state writes happen on the same filesystem (no cross-mount renames).
  This holds because everything lives under `<workspace>/`.

**Chosen: Two-rename swap on a sibling staging directory, with `instance.json`
relocated to a sibling subdirectory `<workspace>/.niwa-state/instance.json`
(option A.1 + B.2)**

The refresh path materializes the new snapshot at a sibling staging path,
then performs a deterministic two-`rename(2)` swap. The state file
`instance.json` moves out of the snapshot directory entirely into a sibling
subdirectory at `<workspace>/.niwa-state/instance.json`, so the swap never
needs to relocate or preserve it.

**Snapshot swap sequence (refresh path)**

The refresh primitive operates against a logical snapshot path (e.g.,
`<workspace>/.niwa/`) and goes through these steps in order:

1. **Stage**: materialize the new content into a sibling staging directory
   `<workspace>/.niwa.next/` (for the team-config clone), or the
   equivalent sibling for the personal-overlay clone and workspace-overlay
   clone. The staging path lives in the same parent directory as the
   target, on the same filesystem, so the subsequent `rename(2)` calls
   are guaranteed to be intra-filesystem.
2. **Fsync**: `fsync` the staging directory's contents and the staging
   directory itself before announcing it as ready, so a crash between
   stage and swap leaves a consistent staging tree (or no staging tree).
3. **Side-rename old**: `rename("<workspace>/.niwa", "<workspace>/.niwa.prev")`.
   This removes the canonical name from the directory entry. On both
   Linux and macOS this is a single atomic syscall against an existing
   non-empty source going to a non-existent target.
4. **Promote new**: `rename("<workspace>/.niwa.next", "<workspace>/.niwa")`.
   The target name does not exist (was renamed away in step 3), so this
   is a single atomic syscall on both platforms.
5. **Fsync parent**: `fsync` the workspace root directory entry so the
   rename pair is durable.
6. **Cleanup**: `os.RemoveAll("<workspace>/.niwa.prev")`.

The non-atomic window is between steps 3 and 4: a sub-microsecond window
where `<workspace>/.niwa/` does not exist as a directory entry. PRD R12
explicitly permits this. Cleanup (step 6) is best-effort; if the process
crashes or is killed before cleanup completes, the next refresh's preflight
removes any orphaned `.niwa.prev` and `.niwa.next` siblings before staging.

**Recovery / preflight**

Before staging in step 1, the refresh primitive runs a preflight cleanup:
- If `<workspace>/.niwa.prev` exists and `<workspace>/.niwa` exists, the
  prior refresh crashed during cleanup. Remove `.niwa.prev`.
- If `<workspace>/.niwa.next` exists, the prior refresh crashed during or
  before staging. Remove `.niwa.next`.
- If `<workspace>/.niwa` does NOT exist but `<workspace>/.niwa.prev` does,
  the prior refresh crashed in the sub-microsecond window between steps 3
  and 4. Recover by `rename(.niwa.prev, .niwa)` before proceeding. This
  is the only crash window where the canonical name was missing on disk.

This preflight is idempotent and runs before every refresh. It also runs
at niwa-process startup for any workspace it loads via `DiscoverInstance`
(cheap stat checks, no work when in steady state).

**`instance.json` placement**

State moves to a sibling subdirectory:
`<workspace>/.niwa-state/instance.json`. The directory form (rather than a
flat sibling file `<workspace>/.niwa-state.json`) is chosen so future
state-adjacent files (e.g., per-workspace lock files, telemetry hashes if
ever added, debug breadcrumbs) have a natural home without proliferating
top-level dotfiles in the workspace root.

The constants in `internal/workspace/state.go` change:
```go
const (
    StateDir  = ".niwa-state"  // was ".niwa"
    StateFile = "instance.json"
)
```

`statePath`, `LoadState`, `SaveState`, `DiscoverInstance`, and
`EnumerateInstances` all become path-correct by virtue of using the
constants. The snapshot directory keeps a separate, hard-coded constant
`SnapshotDir = ".niwa"` for the refresh primitive.

**Backwards-compatibility shim (legacy state location)**

Existing instances have `<workspace>/.niwa/instance.json`. A migration
shim runs lazily on the first apply against any v2 state file:

1. `LoadState(dir)` first looks at `<dir>/.niwa-state/instance.json`. If
   present, return it.
2. If absent, fall back to `<dir>/.niwa/instance.json` (legacy location).
   If found, load it and mark the in-memory state with a "needs
   relocation" flag.
3. The next `SaveState(dir, state)` call:
   - Creates `<dir>/.niwa-state/` (mode 0755) if absent.
   - Writes `instance.json` to the new location atomically (write to
     `<dir>/.niwa-state/instance.json.tmp`, fsync, rename).
   - On success, removes `<dir>/.niwa/instance.json` from the legacy
     location.
   - Emits a one-time `note:`-prefixed disclosure via the existing
     `DisclosedNotices` mechanism: `note: instance state file moved to
     .niwa-state/instance.json (was .niwa/instance.json)`.

`DiscoverInstance` and `EnumerateInstances` get the same dual-path read:
they look at `<dir>/.niwa-state/instance.json` first, then fall back to
the legacy `<dir>/.niwa/instance.json`. This keeps every read path
working before the first save migration completes. After the first save,
the legacy file no longer exists and the dual-read collapses to the new
location.

This dovetails with PRD R28 (legacy working-tree-to-snapshot conversion):
a `<workspace>/.niwa/` with both `.git/` AND `instance.json` triggers
both migrations in the same apply — the snapshot conversion swaps the
directory, and the state-relocation shim moves `instance.json` out
beforehand so it survives the swap.

**Rationale**

*Why two-rename instead of `renameat2(RENAME_EXCHANGE)`:* the Linux-only
variant is non-portable (would require a cgo build path or syscall
shimming) and would still need a fallback for macOS, doubling the code
paths under test. The PRD's explicit acceptance of "brief sub-microsecond
non-atomic window" makes the fallback the right baseline; adding the
Linux-fast-path complexity would optimize a window the PRD already
accepted as fine.

*Why two-rename instead of three-rename with explicit recovery:* the
preflight cleanup at refresh start is the recovery, run idempotently, so
there is no need for inline recovery in the swap itself. Three-rename
adds steps without changing the failure surface — the only crash window
that produces an unrecoverable state is between steps 3 and 4 of the
two-rename sequence, and that window exists in any sequence that uses
two non-`RENAME_EXCHANGE` renames.

*Why not symlink swap:* a workspace-local symlink (`<workspace>/.niwa` →
`<workspace>/.niwa-snapshots/<oid>/`) would let `os.Symlink` + `os.Rename`
do an atomic swap with no missing-name window, but it imposes ongoing
costs that outweigh the saved microsecond:
- `find`, `rsync`, and contributor scripts that do not follow symlinks
  have to special-case `.niwa/` to dereference it. Today users navigate
  into `.niwa/` and treat it as a normal directory.
- The PRD R10 contract is that `<workspace>/.niwa/` IS the snapshot.
  Adding indirection contradicts the contributor mental model the PRD
  is establishing.
- Stale-snapshot garbage collection becomes a new cross-cutting concern
  (when do we delete `<workspace>/.niwa-snapshots/<old-oid>/`?). The
  two-rename sequence cleans up in step 6, no separate GC needed.
- PRD Out of Scope rules out the multi-workspace shared cache; a
  workspace-local symlink target accomplishes none of the things a
  shared cache would.

*Why sibling subdirectory `<workspace>/.niwa-state/` rather than a flat
sibling file:* the directory form makes room for adjacent state-related
files without cluttering the workspace root. niwa already has the
established pattern of `<workspace>/.gitignore` (sibling of `.niwa/`,
written by `EnsureInstanceGitignore`); adding a `.niwa-state/` sibling
extends that pattern symmetrically. A single flat file
`<workspace>/.niwa-state.json` would force any future state-adjacent
file (e.g., a lock file) into yet another top-level dotfile; the
directory form contains them.

*Why not keep `instance.json` inside the snapshot dir but exclude it from
swap:* this requires the swap mechanism to read `instance.json` from the
old `.niwa/`, write it into `.niwa.next/` before promotion, then delete
the old. That couples state I/O into the refresh primitive — and worse,
it puts the canonical state file inside a directory whose contents PRD
R10 says is "exactly the source subpath's regular files plus the
provenance marker." Putting `instance.json` in there would either
violate R10 or require yet another exclusion rule. Outside the snapshot
is structurally cleaner.

*Why not `$XDG_STATE_HOME/niwa/<workspace-hash>/`:* niwa's mental model
is that a workspace is a self-contained directory. Splitting state to
`$XDG_STATE_HOME` means moving the workspace directory to a different
machine drops its state, breaks `DiscoverInstance` (which walks up the
filesystem tree), and forces hash-based lookup logic. The status quo is
that `<workspace>/.niwa/instance.json` makes the workspace portable;
moving state to `<workspace>/.niwa-state/` preserves that property.

**Symmetric application to all three clone sites (R13)**

The two-rename swap and `<sibling>-state/` placement principle applies
symmetrically:

- **Team-config clone**: target `<workspace>/.niwa/`, staging
  `<workspace>/.niwa.next/`, backup `<workspace>/.niwa.prev/`. State at
  `<workspace>/.niwa-state/instance.json` (the existing instance state
  file, relocated).
- **Personal-overlay clone**: today materialized at
  `~/.config/niwa/overlays/<config-name>/`. The same sibling-staging
  pattern applies: stage at `<parent>/<config-name>.next/`, swap, backup
  at `<parent>/<config-name>.prev/`. The personal overlay does not have
  an `instance.json` (it is shared across all instances of a config), so
  sub-question B does not apply to this site; only the swap does.
- **Workspace-overlay clone**: today materialized at the workspace overlay
  location (per `internal/workspace/overlaysync.go`). Same sibling-staging
  pattern. Workspace overlay also has no per-instance state file inside
  it; the workspace's own `instance.json` already covers it via
  `OverlayCommit` and related fields, which already live outside the
  overlay directory in the relocated `<workspace>/.niwa-state/instance.json`.

The single shared refresh primitive (call it `swapSnapshotAtomic(target,
staging string) error`) is implemented once in
`internal/workspace/snapshot.go` (new file) and called by all three sites.
Its preflight cleanup runs against the target's parent directory looking
for `.next` and `.prev` siblings of the target's basename.

**Alternatives Considered**

- **A.3 `renameat2(RENAME_EXCHANGE)`**: Linux-only atomic directory swap.
  Rejected because non-portable to macOS, would still need a fallback
  identical to the chosen sequence, and PRD explicitly accepts the
  brief non-atomic window of the fallback. Adding the Linux-fast-path
  complexity optimizes an already-accepted window.
- **A.2 Three-rename with explicit recovery**: same as the chosen
  two-rename but with inline recovery between steps. Rejected because
  the idempotent preflight cleanup at refresh start handles the same
  recovery without coupling it into the swap, and the only
  unrecoverable-mid-swap crash window is identical in both sequences.
- **A.4 Symlink swap to a content-addressed directory**: atomic swap
  with no missing-name window. Rejected because (a) it contradicts the
  PRD R10 mental model where `<workspace>/.niwa/` IS the snapshot, (b)
  imposes ongoing costs on contributors and scripts that do not follow
  symlinks, (c) introduces stale-snapshot GC as a new concern, and (d)
  PRD Out of Scope explicitly rules out a multi-workspace shared cache,
  removing the only motivation for the indirection.
- **B.1 Sibling file `<workspace>/.niwa-state.json`**: simpler than a
  sibling subdirectory but precludes adding adjacent state-related
  files in the future without proliferating top-level dotfiles. Rejected
  in favor of the subdirectory form for forward compatibility.
- **B.3 `<workspace>/.niwa/.state/instance.json`** (inside snapshot but
  excluded from swap): puts state inside the directory whose contents
  PRD R10 strictly defines. Rejected because either it violates R10
  ("only source files + provenance marker") or it forces a second
  exclusion rule to permit it. Couples state I/O into the refresh
  primitive.
- **B.4 `$XDG_STATE_HOME/niwa/<workspace-hash>/instance.json`**:
  fully-external state. Rejected because it breaks workspace
  portability (move the workspace, lose state), breaks
  `DiscoverInstance` upward-walk semantics, and adds hash-based lookup
  complexity. niwa's "the workspace directory is the workspace" model
  is preserved by keeping state in a sibling.

**Consequences**

*What changes:*
- A new `internal/workspace/snapshot.go` defines `swapSnapshotAtomic`
  and the preflight cleanup; `configsync.go`, `overlaysync.go`, and
  the new tarball-fetch path call it.
- `state.go` constants change: `StateDir = ".niwa-state"` (was `.niwa`).
  The snapshot directory name moves to a separate constant
  `SnapshotDir = ".niwa"` for the refresh primitive.
- `LoadState`, `SaveState`, `DiscoverInstance`, and
  `EnumerateInstances` gain a dual-path lookup that tries
  `.niwa-state/instance.json` first and falls back to legacy
  `.niwa/instance.json`. The fallback is removed in v1.1 (after the
  ~3-month migration window).
- The first `SaveState` against any workspace with legacy state
  performs the relocation and emits a one-time `note:` disclosure.
- The PRD R24 schema-v3 migration writes to the new location
  unconditionally (no v3 file ever lives at the legacy path).
- The PRD R28 same-URL working-tree-to-snapshot conversion runs the
  state relocation BEFORE the snapshot swap so `instance.json` is not
  inside the directory the swap is about to rename away.

*What becomes easier:*
- The refresh primitive has zero awareness of state files. Refresh
  swaps a snapshot; state lives elsewhere; the two concerns are fully
  decoupled.
- All three clone sites use the same single-entry-point primitive,
  satisfying R13 with no per-site special-case logic.
- Crash recovery is centralized in one preflight function, not
  duplicated across each call site.
- The PRD R30 `niwa reset` and R31 plaintext-secrets guardrail can
  read the provenance marker from inside `<workspace>/.niwa/` without
  worrying about whether the directory just got rename-swapped — they
  always read the canonical name, and the refresh primitive guarantees
  it points at a complete tree at every observable instant.

*What becomes harder:*
- One new top-level dotfile-shaped entity in the workspace root
  (`<workspace>/.niwa-state/`). Contributors must learn that snapshot
  content lives in `.niwa/` while instance state lives in
  `.niwa-state/`. Mitigated by the one-time `note:` disclosure on
  migration, by the docs guide, and by the symmetry with the existing
  `<workspace>/.gitignore` sibling pattern.
- Test fixtures that hand-author `<workspace>/.niwa/instance.json`
  must be updated to use `.niwa-state/instance.json`. The
  state-file-factory Gherkin step (PRD Test Strategy) writes to the
  new path.
- The legacy-fallback read paths in `LoadState`,
  `DiscoverInstance`, and `EnumerateInstances` carry maintenance cost
  until v1.1 removes them. Mitigated by clear comments naming the
  removal milestone and by a test that asserts the fallback fires
  exactly once per workspace lifetime.
<!-- decision:end -->
