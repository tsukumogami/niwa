# Lead: What does a "disposable snapshot" look like on disk, and how does it interoperate with subpath sourcing?

## Findings

### Current consumption model

Today the team config clone lives at `<workspace>/.niwa/` and the personal
overlay clone lives at `$XDG_CONFIG_HOME/niwa/overlays/<org>-<repo>/`. Both are
full `git clone` working trees: `init` does `cloner.CloneWith(... Depth: 1)`
into `<workspace>/.niwa/` (`internal/cli/init.go:135-140`), and apply `git
pull --ff-only`s on every run (`internal/workspace/configsync.go:42`,
`internal/workspace/overlaysync.go:45`).

What apply actually *reads* from those dirs is a small, well-bounded set of
flat-file shapes — never anything that needs git plumbing:

- `configDir` is passed as a plain path through the pipeline
  (`internal/workspace/apply.go:864-1028`). The pipeline only ever does file
  I/O against it.
- Specific things read from `configDir`:
  - `workspace.toml` — parsed by `config.Load` at init/apply time
    (`internal/cli/init.go:144`, called via `runPipeline`).
  - `hooks/` — `DiscoverHooks(configDir)` walks the subtree
    (`internal/workspace/discover.go:21-69`).
  - `env/workspace.env` and `env/repos/*.env` — `DiscoverEnvFiles(configDir)`
    (`internal/workspace/discover.go:78-125`).
  - `content/` (or whatever `cfg.Claude.Content.Dir` resolves to) — read by
    `InstallWorkspaceContent`, `InstallGroupContent`, `InstallRepoContent`
    (`internal/workspace/content.go`).
  - `setup/` per repo — `RunSetupScripts` reads the dir and shells out
    (`internal/workspace/setup.go:46`).
  - `niwa.toml` for the global override
    (`internal/workspace/apply.go:638-648`).
- For the overlay clone the only direct-read entry point is
  `workspace-overlay.toml` plus its referenced `content/` files
  (`internal/workspace/apply.go:511-562`).
- The pipeline also calls `validateWithinDir(configDir, ...)` on every path
  before reading, ensuring nothing escapes the clone root
  (`internal/workspace/discover.go:129-150`).

Critically, **nothing in the apply pipeline calls `git` against the configDir
itself.** The only git invocations against `<workspace>/.niwa/` are
`SyncConfigDir` (the failing pull) and the dirty-tree check that gates it.
Both are pure refresh-machinery that lives entirely outside the
config-consumption path. The same is true for the overlay (`HeadSHA` reads
the SHA for state, that is the only post-clone git call).

This means apply only requires that the snapshot present a tree of plain
files at the expected relative paths. Whether those files arrived via git
clone, tarball extract, raw HTTP fetch, or `cp -R` is invisible to every
materializer.

### Options

#### Option A — Pure file tree, no `.git` at all

Layout (team config materialized at the canonical `<workspace>/.niwa/` path):

```
.niwa/
├── instance.json              # niwa-managed state (already there today)
├── workspace.toml             # the source's config
├── hooks/
│   ├── PreToolUse.sh
│   └── PostToolUse/
│       └── notify.sh
├── env/
│   ├── workspace.env
│   └── repos/
│       └── service-a.env
├── content/
│   ├── workspace.md
│   ├── groups/
│   └── repos/
└── .niwa-snapshot.toml        # provenance sidecar (see below)
```

- **Drift detection.** Provenance file records `commit_oid` (or `etag`,
  `content_hash`) and `fetched_at`. Refresh = call the fetch mechanism, ask
  it for the current oid, compare. If equal, skip the swap entirely (cheap
  no-op on every apply where the source has not moved).
- **Offline operation.** Trivially works: nothing in the read path calls the
  network. Apply just consumes whatever bytes are on disk. The fetch mechanism
  can fail-soft and warn "couldn't refresh snapshot, using cached version
  fetched at <ts>".
- **Corruption recovery.** Detected by re-running the fetch and full-tree
  replace. Worst case the user `rm -rf .niwa/` and re-runs `niwa init` (or
  `niwa apply` if we add a self-heal that re-fetches when the marker is
  missing). No git refs to repair, no half-merged state.
- **Inspection without temptation.** Strongest of any option: there is
  literally no `.git`, so `git status` fails inside the dir and people can't
  even attempt to commit. The `.niwa-snapshot.toml` marker reinforces the
  message.
- **Subpath compatibility.** Best of any option: the on-disk shape is
  identical whether the source was `https://github.com/foo/bar.git` (root)
  or `https://github.com/foo/bar.git//configs/team-x` (subpath). The whole
  brain repo is never materialized — only the addressed slice. `subpath = "/"`
  is the degenerate case.
- **Atomic replacement.** Refresh writes to `.niwa.next/` (sibling), then
  `os.Rename` swaps it into place after `os.Rename(.niwa, .niwa.prev)`,
  followed by `RemoveAll(.niwa.prev)`. Apply that is mid-read holds open
  file descriptors; the swap is atomic on POSIX. (`instance.json` survives
  the swap by being moved into the new tree before the rename — see
  Implications.)
- **Provenance metadata.** `.niwa-snapshot.toml` at the snapshot root:
  ```
  source_url    = "https://github.com/acme/dot-niwa.git"
  subpath       = "/"
  ref           = "main"
  commit_oid    = "a1b2c3d4..."
  fetched_at    = "2026-04-22T14:32:11Z"
  mechanism     = "git-archive"   # or "tarball", "git-shallow-clone", etc.
  schema        = 1
  ```

#### Option B — Shallow `.git` (one-commit working tree)

Layout: same as today — full file tree plus a `.git/` dir containing exactly
one commit (`git init && git add -A && git commit && git gc --prune=now`),
or a `git clone --depth=1` followed by detaching from the remote.

- **Drift detection.** Same as Option A, plus the option to use
  `git rev-parse HEAD` against the recorded provenance.
- **Offline operation.** Works.
- **Corruption recovery.** Worse than A: a partially-written `.git/` (say,
  refs but no objects) leaves the dir in a state where git commands fail
  but file reads still work. The fetch mechanism has to detect and rebuild.
- **Inspection without temptation.** Bad — the dir looks exactly like a
  working tree. `git status` succeeds. Users will commit. Issue #72 already
  documents this exact failure: people commit to `.niwa/` and it silently
  drifts.
- **Subpath compatibility.** Awkward: if you cherry-pick a subpath you can't
  put it inside a `.git`-rooted tree without lying about provenance (the
  commit hash refers to the whole repo, not the slice). Either each
  subpath gets a freshly-init'd one-commit history (and the SHA is now
  meaningless against upstream) or you preserve the upstream commit OID
  but the working tree contents don't match it.
- **Atomic replacement.** Same as A — directory rename.
- **Provenance metadata.** Could live in `git config --local
  niwa.source-url`, etc., but that's more obscure than a TOML sidecar.

#### Option C — Content-addressed cache with symlink

Layout:

```
$XDG_CACHE_HOME/niwa/snapshots/
└── sha256:8f3c9d…/
    ├── workspace.toml
    ├── hooks/
    ├── env/
    ├── content/
    └── .niwa-snapshot.toml

<workspace>/.niwa -> $XDG_CACHE_HOME/niwa/snapshots/sha256:8f3c9d…/
```

(Hash key is the SHA-256 of the resolved tuple `(source_url, subpath,
commit_oid)`. Snapshot dirs are immutable; refresh creates a new one and
re-points the symlink.)

- **Drift detection.** Same provenance check; refresh resolves the new
  commit oid, computes the new cache key, fetches into that path if not
  present, repoints the symlink.
- **Offline operation.** Works for the currently-pointed snapshot. Older
  cached snapshots also remain readable (could power `niwa rollback`).
- **Corruption recovery.** Each cache entry is content-addressed by the
  source tuple, so a corrupt entry can be deleted and re-fetched without
  affecting other workspaces.
- **Inspection without temptation.** Following the symlink lands you in a
  path under `~/.cache/...` whose name is a hash — extremely strong "this
  is not where you edit" signal. `git` is not present.
- **Subpath compatibility.** Excellent: cache key naturally includes
  subpath, so two workspaces sourcing different slices of the same brain
  repo at the same commit get distinct cache entries. Two workspaces
  sourcing the *same* slice of the same brain repo at the same commit
  share a single on-disk artifact (multi-workspace dedup, like
  `/nix/store`).
- **Atomic replacement.** Symlink swap (`os.Symlink` + `os.Rename`) is
  atomic. Old snapshot stays intact for as long as some workspace points
  at it; GC pass deletes orphans.
- **Provenance metadata.** Same `.niwa-snapshot.toml` inside the cache
  entry. The cache key is itself derived from the provenance tuple.

#### Option D — Extracted tarball (essentially Option A with a different fetcher)

Functionally identical to A on disk — the directory is just files. The
only differences are operational:

- The fetch mechanism uses `git archive` or `tar -xz` instead of `git clone`.
- Provenance must record the archive checksum in addition to the commit oid
  (the archive may not be reproducible byte-for-byte across runs of
  `git archive` but the included file contents are).

Same drift/offline/corruption/temptation/atomic story as A.

#### Option E — Bare repo cache + on-demand worktree

Layout:

```
$XDG_CACHE_HOME/niwa/bare/
└── github.com_acme_dot-niwa.git/  # bare clone (objects only, no working tree)

<workspace>/.niwa/                  # checked-out worktree from the bare clone
```

(`git worktree add ../bare/... main` creates the worktree.)

- **Drift detection.** `git fetch` in the bare repo, compare oids.
- **Offline operation.** Works for the currently-checked-out commit.
- **Corruption recovery.** Worktrees are coupled to the bare clone via
  `.git/worktrees/<name>` entries — if either side gets out of sync, repair
  is fiddly.
- **Inspection without temptation.** Bad — `<workspace>/.niwa/` is still a
  full working tree. `.git` is a file pointing at the bare clone but most
  users won't notice.
- **Subpath compatibility.** Painful — git worktrees check out the whole
  tree at a ref. Sparse-checkout could narrow it but adds another layer of
  git config that has to survive the swap.
- **Atomic replacement.** Hard — `git worktree` operations aren't atomic
  the way a directory rename is. To swap atomically you'd basically be
  doing Option C anyway.
- **Provenance metadata.** Could derive from git, same caveats as B.

### Peer-tool patterns

- **chezmoi** uses a single source directory under
  `~/.local/share/chezmoi/`. The source dir IS a git working tree, but the
  apply path treats it as a flat-file tree with attribute-encoded prefixes
  (`dot_bashrc` etc.). Detection-of-upstream is `git pull` + a local
  `EntryState` record per applied target (analogous to niwa's
  `ManagedFile.ContentHash`). chezmoi's design accepts that the source dir
  IS where users edit — that is exactly the divergence niwa is trying to
  *avoid*. Worth borrowing: the per-target state-entry pattern (already
  present as `ManagedFile`).
- **Helm** keeps fetched chart packages and index metadata under
  `$XDG_CACHE_HOME/helm/repository/`. Indexes are `index.yaml`; charts are
  `.tgz` archives. There is no working tree exposed to the user. Drift
  detection is "fetch new index, compare etag/version". Worth borrowing:
  cache-under-XDG-with-no-working-tree posture.
- **Terraform** writes to `.terraform/modules/` and tracks every installed
  module in `.terraform/modules.json` (key, source, version, dir, root).
  The dir is gitignored and treated as disposable; users never edit it.
  The `modules.json` manifest is the closest peer to a `.niwa-snapshot.toml`
  sidecar. Worth borrowing: explicit JSON/TOML manifest enumerating every
  fetched source with origin metadata.
- **Nix flakes** materialize build outputs as content-addressed paths under
  `/nix/store/<hash>-<name>/`, with a `result` symlink in the user's working
  dir pointing at the active output. Multiple workspaces sharing the same
  inputs at the same revisions naturally dedup. Worth borrowing: the
  content-addressed cache + symlink pattern is exactly Option C.
- **GitHub Actions reusable workflows** are not cloned at all — the workflow
  body is fetched and inlined into the caller's run context. Composite
  actions get cloned into per-job temp dirs that are torn down after the
  job. Worth borrowing: snapshots are explicitly per-run/disposable, not
  user-facing.

The strongest cross-tool signal: **none of these tools expect users to edit
inside the materialized cache.** They all treat it as derived state. The
ones that get this most cleanly (Helm, Terraform, Nix) put the cache under
`$XDG_CACHE_HOME` so the file path itself signals "this is generated."

## Comparison Matrix

| Criterion | A: Pure tree at .niwa | B: Shallow `.git` | C: Content-addressed + symlink | D: Tarball extract | E: Bare + worktree |
|---|---|---|---|---|---|
| Drift detection | sidecar oid | git rev-parse | sidecar oid (cache key) | sidecar oid | git rev-parse |
| Offline reads | yes | yes | yes (all cached entries) | yes | yes |
| Corruption recovery | rm + refetch | git plumbing | drop one cache entry | rm + refetch | worktree repair |
| Inspection: not-a-worktree signal | strong (no .git) | weak (looks like clone) | strongest (hashed cache path) | strong | weak |
| Subpath compatibility | native | awkward (commit OID lies) | native (subpath in cache key) | native | needs sparse-checkout |
| Atomic replacement | dir rename | dir rename | symlink swap | dir rename | not atomic |
| Multi-workspace sharing | none | none | native (cache dedup) | none | bare repo shared |
| Provenance | TOML sidecar | git config + sidecar | TOML sidecar (cache-key matches) | TOML sidecar | git config |
| Migration cost from today | low (same path) | trivial | medium (path moves, symlink added) | low | medium |
| Implementation complexity | low | low | medium (cache GC) | low | medium |

## Recommendation

**Option A (pure file tree at `<workspace>/.niwa/` with a `.niwa-snapshot.toml`
provenance marker) is the right default for the team config; Option C
(content-addressed cache under `$XDG_CACHE_HOME` with a symlink at
`<workspace>/.niwa/`) is the right design for the personal overlay and a
forward path for the team config when multi-workspace sharing becomes
desirable.**

Rationale: Option A solves the actual problem in #72 (no `.git` to diverge,
nothing to commit to, atomic full-tree replace), preserves the existing
`<workspace>/.niwa/` path so init/apply/instance-discovery don't have to
move, and works identically for whole-repo and subpath sources because the
on-disk shape is independent of how the bytes were fetched. The personal
overlay is a stronger fit for Option C because it is *already* a per-user
shared resource (today's `$XDG_CONFIG_HOME/niwa/overlays/<org>-<repo>/` is
keyed by URL, intentionally shared across workspaces) — content-addressing
adds the crucial property that two workspaces pointing at the same overlay
ref share a single immutable snapshot, and the symlinked location at
`<workspace>/.niwa/overlay/` (or wherever apply expects it) keeps the apply
read paths uniform with the team config. A staged migration is natural:
ship Option A for the team config first (smallest blast radius, fixes #72),
then introduce the cache layer underneath in a later release where the team
snapshot becomes a symlink into the same cache used by the overlay.

The team config and personal overlay should ultimately have the **same
shape** (both materialized as plain file trees with the same provenance
sidecar), differing only in *where* the bytes live: workspace-local for
team config, shared cache for overlay. This symmetry lets a single
`OpenSnapshot(path)` helper read either kind, and `niwa status` can render
both with the same provenance line.

## Implications

- **Registry schema** (`config.RegistryEntry`): keep `SourceURL` but allow
  it to encode subpath and ref (`url//subpath@ref` or three discrete TOML
  fields). The snapshot mechanism is what consumes them; the registry just
  stores the canonical tuple.
- **State file** (`InstanceState`): add a `SnapshotProvenance` struct
  mirroring the `.niwa-snapshot.toml` (commit_oid, ref, fetched_at,
  mechanism). Today's `OverlayCommit` field generalizes — apply can do its
  drift-warned-since-last-apply check against either source or overlay
  uniformly. Removing `git` calls from the read path means
  `internal/workspace/gitutil.go`'s `HeadSHA` is no longer reachable for
  the snapshot dirs (still needed for managed source repos).
- **`niwa status` output**: gains a "config snapshot" line per source
  showing `<source_url>[//subpath][@ref] (fetched <relative-time>, oid
  <short>)`. Removes the today-implicit "config has uncommitted changes"
  warning since edits to the snapshot are explicitly disposable.
- **Error messages**: today's "config directory has uncommitted changes
  / Use --allow-dirty" message disappears. New failure mode is "snapshot
  refresh failed, using cached version fetched at <ts>" — distinguishes
  network failure from corrupt snapshot.
- **`SyncConfigDir` and `CloneOrSyncOverlay`** become thin shims over the
  fetch mechanism + atomic-swap logic; both lose their `--allow-dirty`
  parameter and the dirty-tree check entirely.
- **Init must write the provenance sidecar**, since today's init only does
  `git clone`. The first sidecar bootstraps the drift-detection contract
  for subsequent applies.
- **`.gitignore` and `EnsureInstanceGitignore`**: `.niwa/` is already
  outside any working tree the user controls in normal usage. The Option A
  shape has no `.git` so `git status` from an outer repo at the workspace
  root won't see internal noise. Worth verifying that the outer
  `<workspace>/.gitignore` (if any) excludes `.niwa/` either way — this
  pattern likely deserves to be added to the workspace-root `.gitignore`
  template.

## Surprises

1. **The apply pipeline does not need git at all to consume the config.**
   Every read of `configDir` and `overlayDir` is plain `os.ReadFile` /
   `os.ReadDir`. The git dependency is purely refresh-machinery. This makes
   the migration from "working tree" to "pure file tree" much smaller than
   it sounds — no materializer needs to change.
2. **Today's overlay path (`$XDG_CONFIG_HOME/niwa/overlays/<org>-<repo>/`)
   is already keyed by source URL.** That is most of the way toward
   content-addressing — it just needs the ref/oid included in the key to
   become genuinely immutable, and a symlink to make it look uniform with
   the team config from the apply pipeline's point of view.
3. **The `OverlayCommit` field in `InstanceState` is the only existing
   provenance niwa records about its config sources.** Generalizing this
   to a sidecar inside the snapshot dir (so `niwa status` doesn't have to
   crack `instance.json` to render it, and so the snapshot is
   self-describing if discovered out of band) is a pure additive change.
4. **`<workspace>/.niwa/instance.json` lives inside the snapshot dir
   today.** Under Option A's atomic-replace strategy the state file has to
   either move to a sibling location (e.g. `<workspace>/.niwa-state.json`)
   or be carefully migrated across the swap. Splitting "snapshot
   contents" from "niwa-managed state" is probably worth doing on its own
   merits regardless of which option we pick.

## Open Questions

- Where should `instance.json` live once `.niwa/` becomes a fully
  niwa-managed disposable artifact? Sibling file
  (`<workspace>/.niwa-state.json`)? Subdirectory excluded from the swap
  (`<workspace>/.niwa/.state/`)? Move it under `$XDG_STATE_HOME/niwa/`
  keyed by workspace path?
- Should the snapshot directory be made read-only (`chmod -R a-w`) after
  refresh to make the "do not edit" contract enforced rather than
  conventional? Trade-off: stronger guard, but breaks any tooling that
  wants to `cd .niwa && grep` modifying access times etc.
- For Option C, what's the cache GC story? Reference-counted by registry
  entries pointing into the cache? Time-based eviction? Lazy GC on the
  next `niwa apply`? The answer affects whether `instance.json` should
  also record which cache entry it depends on.
- Does `niwa init` keep cloning into `.niwa/` (and convert to a snapshot
  at the next apply), or does init become snapshot-aware from the start
  (and `.niwa/` is never a working tree, even momentarily)? The latter
  is cleaner but couples init to the new fetch mechanism.
- For subpath sources, where does the user-visible name of the snapshot
  come from? `<workspace>/.niwa/` is fine for one source, but if a future
  design supports multiple `[[sources.config]]` blocks, the path scheme
  needs a slot for source identity.

## Summary

Recommended snapshot shape is a pure file tree under `<workspace>/.niwa/`
with no `.git`, marked by a `.niwa-snapshot.toml` provenance sidecar
(commit oid, ref, fetched-at, mechanism), refreshed via atomic
sibling-rename — and the same shape used for the personal overlay, but
materialized via a symlink into a content-addressed cache under
`$XDG_CACHE_HOME/niwa/snapshots/sha256:.../` so multiple workspaces share
one immutable snapshot per (source, ref) tuple. The main trade-off is
implementation cost vs. sharing benefit: Option A alone fixes #72 with a
near-trivial migration, while layering Option C underneath buys
multi-workspace dedup and rollback capability at the price of a cache-GC
story we don't have today. The biggest open question is where
`instance.json` lives once `.niwa/` is a fully-disposable directory the
refresh path may rename out from under it — sibling file, sibling
subdirectory, or `$XDG_STATE_HOME` are all defensible and the answer
shapes how `init`, `apply`, and `DiscoverInstance` interact going forward.
