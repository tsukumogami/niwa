# Research: where the auxiliary state of a config-directory swap can live

Facts only. No recommendation. Paths are relative to the niwa repo root
(`public/niwa`, worktree `session-store-teardown`). Every claim carries a
`file:line`. Sections marked **Negative** record where I looked and found nothing.

---

## 1. Every config directory niwa refreshes

There are exactly three. The refresh entry point is
`EnsureConfigSnapshotWithStatus` (`internal/workspace/snapshotwriter.go:87`), and
the swap is `SwapSnapshotAtomic` (`internal/workspace/snapshot.go:37`). Enumerating
the non-test callers of the entry point gives the complete set.

### 1a. The workspace root's `.niwa/`

| | |
|---|---|
| Path expression | `<workspaceRoot>/.niwa` |
| Computed by | `config.Discover` — `internal/config/discover.go:19`, returning `filepath.Join(dir, ConfigDir)` at `internal/config/discover.go:28`; `ConfigDir = ".niwa"` at `internal/config/discover.go:12` |
| Also derived as | `filepath.Dir(configPath)` — `internal/workspace/configreload.go:62` |
| Parent | the workspace root, an arbitrary directory on disk |
| Under a per-user config home? | **No.** Under the user's workspace tree. |

`Discover` walks up from `startDir` until it finds `.niwa/workspace.toml`
(`internal/config/discover.go:25-34`), so the workspace root is wherever the user
put it. The registry records it as a free-form path: `RegistryEntry.Root`
(`internal/config/registry.go:160`).

Refresh call sites:
- `internal/workspace/apply.go:489` (`Applier.Create`)
- `internal/workspace/apply.go:640` (`Applier.Apply`)
- `internal/workspace/configreload.go:66` (`ReconcileAndReloadConfig`)
- `internal/cli/init.go:75` → `workspace.MaterializeFromSource`
  (`internal/workspace/snapshotwriter.go:318`), first-time `niwa init --from`

Marker set: `config.TeamConfigMarkerSet()` (`internal/config/discover.go:97-99`).

**This directory is not empty of niwa-local state.** Three carry-overs ride across
the swap because the rotation would otherwise destroy them:
`instance.json` (`preserveInstanceState`, `internal/workspace/snapshotwriter.go:483`;
`StateFile = "instance.json"` at `internal/workspace/state.go:35`),
`dispatch-briefs/` (`internal/workspace/snapshotwriter.go:514`; name constant at
`internal/workspace/snapshotwriter.go:26`), and `sessions/`
(`internal/workspace/snapshotwriter.go:554`; `sessionsDirName = "sessions"` at
`internal/workspace/session_map.go:118`).

### 1b. Overlay clones

| | |
|---|---|
| Path expression | `$XDG_CONFIG_HOME/niwa/overlays/<name>` (fallback `$HOME/.config/niwa/overlays/<name>`) |
| Computed by | `config.OverlayDir` — `internal/config/overlay.go:325`, returning `filepath.Join(configHome, "niwa", "overlays", dirName)` at `internal/config/overlay.go:351` |
| `<name>` | `org + "-" + repo` (`internal/config/overlay.go:340`) or `"file-" + filepath.Base(path)` (`internal/config/overlay.go:334`) |
| Parent | `$XDG_CONFIG_HOME/niwa/overlays/` |
| Under a per-user config home? | **Yes.** |

Refresh goes through `EnsureOverlaySnapshot` (`internal/workspace/overlaysync.go:29`),
which dispatches to `EnsureConfigSnapshotWithStatus`
(`internal/workspace/overlaysync.go:40`) or `MaterializeFromSource`
(`internal/workspace/overlaysync.go:51`). Marker set: `config.OverlayMarkerSet()`
(`internal/workspace/overlaysync.go:35`, defined at `internal/config/discover.go:108-110`).

Call sites computing the dir: `internal/workspace/apply.go:1059` (explicit
`OverlayURL` from state), `internal/workspace/apply.go:1094` (convention discovery
via `DeriveOverlayURL`, `internal/config/overlay.go:242`), `internal/cli/init.go:1044`
(`--overlay`), `internal/cli/init.go:1069` (convention), and
`internal/cli/session_lifecycle_cmd.go:461` (re-derived per `niwa create`).

`overlays/` is created lazily by the only `MkdirAll` of that parent:
`internal/workspace/overlaysync.go:48`.

### 1c. The personal global config clone

| | |
|---|---|
| Path expression | `$XDG_CONFIG_HOME/niwa/global` (fallback `$HOME/.config/niwa/global`) |
| Computed by | `config.GlobalConfigDir` — `internal/config/registry.go:403`, returning `filepath.Join(configHome, "niwa", "global")` at `internal/config/registry.go:412` |
| Parent | `$XDG_CONFIG_HOME/niwa/` |
| Under a per-user config home? | **Yes.** |

Refresh: `internal/workspace/apply.go:964`, with an **empty** `config.MarkerSet{}` —
the no-probe whole-repo mode documented at `internal/workspace/snapshotwriter.go:641-647`
and `internal/workspace/apply.go:944-952`. First-time materialization:
`internal/cli/config_set.go:76`. Its file convention is `niwa.toml`, not
`workspace.toml` (`internal/workspace/apply.go:947-950`).

Wired onto the applier in five places: `internal/cli/init.go:182`,
`internal/cli/create.go:236`, `internal/cli/reset.go:141`,
`internal/cli/instance_from_hook.go:478`, `internal/cli/session_lifecycle_cmd.go:415`.

### Nothing else

**Negative.** I enumerated every non-test caller of `EnsureConfigSnapshot`,
`EnsureConfigSnapshotWithStatus` and `MaterializeFromSource` across `internal/`
and `cmd/`. There are no others. In particular there is no per-*instance* config
directory that gets refreshed: `Applier.Create` and `Applier.Apply` both take the
workspace-root `configDir` as a parameter (`internal/workspace/apply.go:640`,
`internal/cli/init.go:199`, `internal/cli/instance_from_hook.go:501`) and the
per-instance `.niwa/` holds only `instance.json`, never a fetched snapshot.

The in-flight design agrees: `docs/designs/DESIGN-session-store-teardown.md:84`
names "the workspace root's `.niwa/`, an overlay clone, the global clone".

---

## 2. Filesystem constraints on renames

### What the swap does today

`SwapSnapshotAtomic` (`internal/workspace/snapshot.go:37`) performs two renames:

- `os.Rename(target, prev)` where `prev := target + ".prev"` — `internal/workspace/snapshot.go:48,69`
- `os.Rename(staging, target)` where `staging := configDir + ".next"` — `internal/workspace/snapshotwriter.go:355`, `internal/workspace/snapshot.go:77`
- rollback `os.Rename(prev, target)` — `internal/workspace/snapshot.go:79`

All three are sibling-to-sibling by construction, so EXDEV cannot occur today.
`SwapSnapshotAtomic` does **not** validate that `staging` and `target` share a
parent — it rejects only empty and identical paths
(`internal/workspace/snapshot.go:38-46`). The same-parent invariant is a caller
contract, not an enforced one.

### There is no EXDEV handling anywhere

**Negative.** A repo-wide grep for `EXDEV`, `syscall.EXDEV`, `LinkError`,
`cross-device` and `cross device` across `.go` and `.md` returns no non-test
handling. There is no fallback-to-copy on any `os.Rename` in the codebase.

The invariant is stated in prose in exactly one place:
`internal/workspace/codex_trust.go:578-579` — "a temp file beside it (an atomic
rename needs the same filesystem)".

The full rename inventory (16 non-test sites) is uniformly sibling-to-sibling:
`internal/plugin/installer.go:155,162,165`; `internal/workspace/snapshot.go:69,77,79`;
`internal/workspace/niwaplugin.go:191`; `internal/workspace/pluginskills.go:264`;
`internal/workspace/codex_trust.go:618`; `internal/workspace/session_map.go:163`;
`internal/worktree/session_lifecycle.go:87`; `internal/worktree/attach_state.go:99`;
`internal/config/registry.go:395`; `internal/watch/trust.go:179`;
`internal/watch/state.go:198`; `internal/pluginrecord/registry.go:444`.

### What a "swap area outside the sibling namespace" must look like, per directory

For the rename to stay atomic, the *moved-aside snapshot* and the *staging
directory* must both be on the same filesystem as the config directory. The lock
file, the journal, and trash-awaiting-deletion have no such constraint (the lock
and journal are written and read, never renamed into the config dir; trash is
only deleted) — but trash that is produced *by renaming* something out of the
config directory does carry the constraint.

**Overlay clones (1b).** A central area under `$XDG_CONFIG_HOME/niwa/` — e.g.
`$XDG_CONFIG_HOME/niwa/swap/<key>/` — is on the same filesystem as
`$XDG_CONFIG_HOME/niwa/overlays/<name>` in any ordinary configuration. Both are
descendants of `$XDG_CONFIG_HOME/niwa`, which niwa itself creates as one tree
(`internal/workspace/overlaysync.go:48`, `internal/config/registry.go:388`). A
separate mount interposed between `niwa/` and `niwa/overlays/` is possible in
principle and nothing in the code would detect it. **Viable.**

**The global clone (1c).** Same argument, one level shallower:
`$XDG_CONFIG_HOME/niwa/global` and `$XDG_CONFIG_HOME/niwa/swap/` are siblings.
**Viable.**

**The workspace root's `.niwa/` (1a).** A central area under
`$XDG_CONFIG_HOME/niwa/` is **not** filesystem-viable. The workspace root is an
arbitrary path (`internal/config/discover.go:19-37`; `RegistryEntry.Root` at
`internal/config/registry.go:160` is a free-form string), so
`<workspaceRoot>/.niwa` and `$XDG_CONFIG_HOME/niwa/...` are on the same device
only by coincidence. `os.Rename` across them fails with EXDEV, and there is no
copy fallback to absorb it (see the negative above). A workspace on an external
drive, a separate `/home` partition, an NFS or SMB mount, a container bind mount,
or a `$HOME` on a different volume from the project tree all break it. A
same-filesystem swap area for this directory must therefore be *inside the
workspace tree* — the workspace root, or a directory under it — which is the
sibling namespace the two rejected attempts were trying to leave.

One nuance specific to 1a: the workspace root is also where instances live and
where the scanners run. `EnumerateInstances` reads the root
(`internal/workspace/state.go:353-374`) and admits any child whose name passes
`ValidName` (`internal/workspace/state.go:559`, which rejects only control and
format characters — **not** the `[a-zA-Z0-9._-]+` `config.NamePattern` at
`internal/config/config.go:20`) and that contains `.niwa/instance.json`
(`internal/workspace/state.go:368`). So a swap directory placed as a child of the
workspace root is filtered only by the absence of `instance.json` one level down,
not by its name. The design states this and pins it with a test at
`docs/designs/DESIGN-session-store-teardown.md:1383-1393`, and notes that
`EnumerateInstances` uses `os.Stat` with no type check, so a *directory* named
`instance.json` would qualify a child today.

`$TMPDIR` is not a candidate for any of the three: it is routinely a separate
device (tmpfs), and the one place niwa clones into it deliberately **copies** out
rather than renaming (see §3).

---

## 3. What already exists that could serve

### 3a. Per-user scratch / cache / state areas

**Negative, and strongly so.** Nothing in non-test `internal/` or `cmd/` computes
`XDG_CACHE_HOME`, `XDG_STATE_HOME`, `XDG_DATA_HOME`, `~/.cache`, `~/.local/state`
or `~/.local/share`, and there is no `os.UserCacheDir` call anywhere. The only XDG
variable niwa reads is `XDG_CONFIG_HOME`, at four sites:
`internal/config/registry.go:260` (`GlobalConfigPath`),
`internal/config/registry.go:404` (`GlobalConfigDir`),
`internal/config/overlay.go:343` (`OverlayDir`),
`internal/workspace/providerauth.go:245`.

There is no `.niwa/tmp`, no `tmp/`, no `.trash`, and no standalone `staging/`
directory. The `.niwa` children that exist in non-test code are: `sessions`,
`worktrees`, `locks`, `marketplaces`, `plugin`, `claude`, `instance.json`,
`attach.lock`, `attach.state`, `watch-handled`, `dispatch-pending`,
`dispatch-retain`, and the `dispatch-<binary>.<stream>` capture files. No scratch
child.

The one niwa-owned directory under `$HOME` outside `~/.config/niwa` is a lock
store: `internal/workspace/codex_trust.go:637` — `root := filepath.Join(home,
".niwa", "locks")`, with lock names `codex-trust-<sha256(configPath)[:16]>.lock`
(`internal/workspace/codex_trust.go:642`). That is prior art for *hashing an
arbitrary absolute path into a flat per-user namespace*.

### 3b. `os.MkdirTemp` — two sites, both `$TMPDIR`, neither renamed out of

1. `internal/workspace/fallback.go:137` — `os.MkdirTemp("", "niwa-fallback-")`,
   the destination of `git clone --depth 1` for non-GitHub config sources
   (`internal/workspace/fallback.go:142-151`). The clone is then **copied** into
   the caller's staging dir by `copyFromCloneRoot`
   (`internal/workspace/fallback.go:113`) and the temp tree is removed by a
   `defer` at `internal/workspace/fallback.go:111`. This is the existing
   "fetch into `$TMPDIR`, copy into real staging" pattern, and it copies
   precisely because `$TMPDIR` may be another device.
2. `internal/watch/fetch.go:69` — `os.MkdirTemp("", "niwa-watch-fetch-home-")`,
   a throwaway `HOME` for a hardened `git fetch`; removed at
   `internal/watch/fetch.go:73`. Nothing moves out of it.

`os.CreateTemp` appears at four sites, all with the *destination directory* as the
parent: `internal/workspace/codex_trust.go:592`, `internal/watch/trust.go:162`,
`internal/config/registry.go:366`, `internal/watch/state.go:182`. Plus a
hand-rolled `O_EXCL` equivalent with the same rule at
`internal/pluginrecord/registry.go:453-455`.

**Negative.** No `ioutil.TempDir`/`ioutil.TempFile` and no `os.TempDir()` calls.

### 3c. How the plugin installer solves the same problem

`internal/plugin/installer.go:135-177`, documented at
`internal/plugin/installer.go:125-134`.

```
nextPath := installPath + ".next"   // installer.go:136
prevPath := installPath + ".prev"   // installer.go:137
```

`installPath` is `<home>/.claude/plugins/marketplaces/niwa`
(`internal/plugin/embed.go:106`), so both staging and move-aside are **peer entries
inside `<home>/.claude/plugins/marketplaces/`** — the same sibling-suffix scheme
the config-dir swap uses. The shared parent is created first at
`internal/plugin/installer.go:144`.

Sequence: `MaterializeTo(next)` (`:148`) → `Rename(install, prev)` if it exists
(`:155`) → `Rename(next, install)` (`:162`) → best-effort `RemoveAll(prev)`
(`:173`); rollback `Rename(prev, install)` on a failed promote (`:165`).
Crash recovery is an unconditional `RemoveAll` of both fixed names on entry
(`:141-142`) — which only works *because* the names are predictable.

Two differences from the config-dir case that bear on whether it is transferable:
the payload is **embedded**, so there is no network fetch to keep out of the
locked window (`internal/plugin/installer.go:148`); and the parent
(`.../marketplaces/`) is a niwa-owned namespace, not one whose sibling names are
derived from user input.

A second, weaker variant of the same pattern uses `.staging`:
`internal/workspace/pluginskills.go:248` and `internal/workspace/niwaplugin.go:176`.
Both are **not** atomic — each runs `os.RemoveAll(dest)` *before* the rename
(`internal/workspace/pluginskills.go:260`, `internal/workspace/niwaplugin.go:187`),
so a failed rename leaves the destination gone.
`internal/workspace/niwaplugin.go:154-160` records an extra reason for staging
beside rather than rebuilding in place: the delivery is a symlink to the tree's
path, and "a symlink survives its target being replaced only if the replacement
lands back at the same path".

---

## 4. The overlay name derivation, precisely

### The code

`OverlayDir` (`internal/config/overlay.go:325`) has two branches.

**`file://` branch** (`internal/config/overlay.go:329-334`):
```go
path := strings.TrimPrefix(s, "file://")
path = filepath.Clean(strings.TrimSuffix(path, ".git"))
dirName = "file-" + filepath.Base(path)
```

**Everything else** (`internal/config/overlay.go:335-341`): `parseOrgRepo(s)`
(`internal/config/overlay.go:259`), then `dirName = org + "-" + repo`.

`parseOrgRepo` has three sub-branches:
- `https://` (`internal/config/overlay.go:263-281`): drop everything through the
  first `/`, then `strings.SplitN(rest, "/", 3)`; `org = parts[0]`,
  `repo = strings.TrimSuffix(parts[1], ".git")`. Requires both non-empty. **No
  charset check.** Note there is no `:` rejection on this branch.
- `git@` (`internal/config/overlay.go:283-299`): take everything after the *first*
  `:`, then the same `SplitN`/`TrimSuffix`. **No charset check**, and again no `:`
  rejection on the components.
- shorthand (`internal/config/overlay.go:301-316`): `SplitN(s, "/", 3)`; rejects
  only `strings.Contains(parts[0], ":")` and `strings.HasPrefix(s, "/")`
  (`internal/config/overlay.go:308`). `repo` is unrestricted apart from being
  non-empty and slash-free.

### What can appear in a derived name

I ran the two functions verbatim against a table of inputs. Results:

| Input | Derived directory name |
|---|---|
| `acme/tools@swap` | `acme-tools@swap` |
| `acme/tools.lock` | `acme-tools.lock` |
| `acme/tools.prev` | `acme-tools.prev` |
| `acme/tools.next` | `acme-tools.next` |
| `acme/tools:.niwa` | `acme-tools:.niwa` |
| `acme/tools:.niwa@v1` | `acme-tools:.niwa@v1` |
| `acme/..` | `acme-..` |
| `../evil` | `..-evil` |
| `-x/repo` | `-x-repo` |
| `a b/c d` | `a b-c d` |
| `https://github.com/a:b/c` | `a:b-c` |
| `git@h:o:x/r` | `o:x-r` |
| `acme/tools\evil` | `acme-tools\evil` |
| `acme/tools` + embedded newline | name with an embedded newline |
| `file:///sandbox/gitserver/ws-overlay.git` | `file-ws-overlay` |
| `file:///a/b@swap` | `file-b@swap` |
| `file:///` | `file-/` → `filepath.Join` cleans to `file-` |
| `file://` | `file-.` |

**Confirmed: there is no character that cannot appear, with two mechanical
exceptions.**

1. `/` cannot appear in a name produced by the `org-repo` branch, because both
   components are `SplitN` fragments and so are slash-free. It *can* appear in the
   `file://` branch — `filepath.Base(filepath.Clean("/"))` is `"/"`, giving
   `"file-/"` — but `filepath.Join` at `internal/config/overlay.go:351` cleans it
   back to `file-`, so it never escapes the `overlays/` parent.
2. The `org-repo` branch always yields at least three characters containing a `-`
   (both components are non-empty), so the name can never be exactly `.` or `..`.
   The `file://` branch can yield `file-.` and `file-..`, which `filepath.Join`
   preserves as literal names rather than traversal.

Everything else — `@`, `:`, `.`, spaces, backslashes, newlines, a leading `-`,
leading dots — is reachable. Leading `file-` is reachable from both branches
(`file/x` → `file-x`).

**The `@` case is not exotic.** The documented slug grammar is
`[host/]owner/repo[:subpath][@ref]` (`docs/guides/workspace-config-sources.md:80`),
and `--overlay` accepts "org/repo or URL" (`internal/cli/init.go:47`). An overlay
pinned to a ref — `--overlay acme/tools@v1.2.0` — is an ordinary documented input.
`source.Parse` splits the `@ref` off for *fetching*
(`internal/source/parse.go:46-55`), but `OverlayDir` does not call `source.Parse`;
it calls `parseOrgRepo`, which leaves the ref attached. So the directory is
`acme-tools@v1.2.0`. `@` in a derived name is the normal case for a pinned
overlay, not a hostile one.

A related consequence of the same split: convention discovery appends `-overlay`
*after* the ref. `DeriveOverlayURL("acme/brain@v1")`
(`internal/config/overlay.go:242-255`) yields `acme/brain@v1-overlay`, which
`parseOverlaySlug` → `source.Parse` then reads as repo `brain` at ref
`v1-overlay` (`internal/workspace/overlaysync.go:89`, `internal/source/parse.go:46`).
That is a separate latent bug, noted here only because it shows the two parsers
disagree about the same string.

Via convention discovery the derived name always ends in `-overlay`
(`internal/config/overlay.go:254`), so a terminal `@swap` is reachable only
through explicit `--overlay` / a recorded `OverlayURL`. A terminal `.lock` is
reachable through both.

### Sanitizers and charset checks applied to these names

**Negative.** None. I checked every name validator in the repo:

- `config.NamePattern` = `^[a-zA-Z0-9._-]+$` (`internal/config/config.go:20`) —
  applied to workspace names (`internal/config/config.go:672`), group and repo
  keys (`:683`, `:689`), and vault names (`internal/config/vault.go:115`). **Not**
  applied to overlay directory names.
- `workspace.ValidateInitName` (`internal/workspace/validate.go:21`) — applies
  `NamePattern` plus explicit rejection of `.`, `..` and `.niwa`. Only for
  `niwa init <name>`.
- `workspace.ValidName` (`internal/workspace/state.go:559`) and
  `config.validRegistryName` (`internal/config/registry.go:224`) — reject only
  control characters and `Cf`/`Zl`/`Zp`. Permissive; and neither is applied to
  overlay names.
- `source.Parse` (`internal/source/parse.go:25`) rejects whitespace (`:29-33`),
  misordered separators (`:39`), multiple `@` (`:47`) and multiple `:` (`:60`) —
  but it is on the *fetch* path, never on the `OverlayDir` path.

The design doc records the same finding independently at
`docs/designs/DESIGN-session-store-teardown.md:170-177`.

---

## 5. Blast radius of sanitizing the derived name

### The key structural fact: the path is never persisted

Only the **URL** is stored. `InstanceState.OverlayURL`
(`internal/workspace/state.go:101`) and `OverlayCommit` (`:103`) are the only
overlay fields in `instance.json`; `internal/workspace/state_test.go:913` asserts
exactly those keys round-trip. `RegistryEntry` (`internal/config/registry.go:158-167`)
has no overlay field. The provenance marker records `source_url`, not its own path
(`internal/workspace/provenance.go:30,44-45`; written verbatim per
`internal/workspace/overlaysync.go:43-45`). `config.ContentEntry.OverlayDir` is
`toml:"-"`, in-memory only (`internal/config/config.go:540-545`, set at
`internal/workspace/override.go:946`, consumed at `internal/workspace/content.go:126-127`).

This is by documented design: `docs/prds/PRD-workspace-visibility-overlay.md:347`
(R18) — "The local clone path is derived at runtime as
`$XDG_CONFIG_HOME/niwa/overlays/<workspace-id>/` where `<workspace-id>` is derived
from the overlay URL… The local path is never stored in `instance.json`." The
same precedent is stated for the global clone at `internal/config/registry.go:22-24`.

**Consequence:** a changed derivation is internally consistent — every call site
recomputes it from the URL, so lookups do not break in the sense of pointing at a
half-updated pair of records. What changes is that the *old* directory stops being
addressed.

### What actually happens on disk

**A re-clone.** `EnsureOverlaySnapshot` tests for a previous clone purely by path
existence (`internal/workspace/overlaysync.go:37` —
`provenanceMarkerExists(dir) || dotGitExists(dir)`), so the new name reads as a
fresh materialization and the overlay is fetched again
(`internal/workspace/overlaysync.go:48-52`). For a GitHub overlay this is a
tarball fetch; for a non-GitHub one a shallow clone.

**An orphaned directory, permanently.** Nothing lists, scans or garbage-collects
`overlays/`. Verified negative: no `os.ReadDir` in non-test code is rooted at
`overlays/` or at `GlobalConfigDir`'s parent; there is no `niwa doctor` command;
`internal/cli/reset.go` and `internal/cli/reap.go` contain no reference to
`overlay` or to that directory. The only writes to the `overlays/` parent are the
`MkdirAll` at `internal/workspace/overlaysync.go:48` and, inside it, the `.next`
staging (`internal/workspace/snapshotwriter.go:355`) and `.prev` rotation
(`internal/workspace/snapshot.go:48`).

**A silent fallback on the worktree path.** `mergeWorktreeOverlay`
(`internal/cli/session_lifecycle_cmd.go:461`) re-derives the dir per `niwa create`
and, on a missing clone, returns the base config with no error
(`internal/cli/session_lifecycle_cmd.go:466-471`). So on a machine that has not
re-applied, a `niwa create` between the binary upgrade and the next `niwa apply`
quietly drops overlay content instead of failing.

### Tests that would break loudly

`internal/config/overlay_test.go` compares computed paths to literals in nine
places:
- `:109,113` — `OverlayDir("acme/myrepo")` vs `filepath.Join(tmp, "niwa", "overlays", "acme-myrepo")`
- `:127,131` — XDG-unset fallback, same name under `$HOME/.config`
- `:141,145` — `https://github.com/acme/myrepo.git` → `acme-myrepo`
- `:155,159` — `git@github.com:acme/myrepo.git` → `acme-myrepo`
- `:166` — asserts an error on unparseable input
- `:181-189` — `DeriveOverlayURL` round trip → `myorg-myconfig-overlay`
- `:204-213,222` — `file://` table → `file-ws-overlay`
- `:239,247` — `file://` round trip → `file-ws-overlay`
- `:59-66` — `DeriveOverlayURL` table (`file:///…/ws.git` → `file:///…/ws-overlay.git`)

`internal/cli/init_test.go` pre-seeds a clone at a hand-computed path:
- `:596-599,602,606` — `overlayCloneTarget := filepath.Join(xdgDir, "niwa", "overlays", "testorg-testoverlay")`, then `git clone` into it
- `:691-692,698` — `.../overlays/acme-dot-niwa-overlay`

### Tests that would not break

`internal/cli/session_lifecycle_overlay_test.go:28-33` derives the dir by calling
`config.OverlayDir` and `MkdirAll`-ing whatever comes back.
`internal/workspace/characterization_test.go:208,431` does the same and tokenizes
the path as `{OVERLAY}` in its goldens (`:436-439`), so the golden manifests are
name-agnostic. `internal/workspace/apply_test.go:1276-1285` (`setupOverlayDir`) and
its eleven callers use a bare `t.TempDir()`.

**test/functional is a clean negative.** `grep -rn "overlays" test/` returns zero
hits. The suite drives overlays through the binary and asserts on effects
(`test/functional/features/critical-path.feature:80-97`), never on paths. The one
adjacent assertion is a *negative* one at
`test/functional/features/critical-path.feature:244-271` — "the error output does
not contain `myws-overlay`" — which a change that printed the derived name would
fail. Beware a false positive: `test/functional/steps_test.go:1045-1054` and the
many "personal overlay" steps refer to `$XDG_CONFIG_HOME/niwa/global/niwa.toml`,
a different feature.

### Docs that assert on the current name shape

- `docs/prds/PRD-workspace-visibility-overlay.md:347` (R18) — says only "derived
  from the overlay URL"; a sanitized or hashed name still satisfies it.
- `docs/designs/current/DESIGN-workspace-visibility-overlay.md:113` — "a
  **sanitized** `<org>-<repo>` extracted from the URL (e.g.
  `acmecorp-dot-niwa-overlay`)"; `:214` — "Sanitization: parse GitHub URL or
  shorthand, produce `<org>-<repo>`". The design already calls the current
  derivation "sanitization"; the implementation never sanitized anything.
- `docs/designs/current/DESIGN-workspace-visibility-overlay.md:17,168,358` and
  `docs/designs/current/DESIGN-workspace-config-sources.md:797` — repeat the
  `overlays/<name>` layout.
- `docs/designs/current/DESIGN-workspace-visibility-overlay.md:113` also records a
  behavioural contract worth preserving: "If multiple instances use the same
  overlay URL … they share one clone." Any sanitizer must stay a pure function of
  the URL for that to hold.
- `README.md:211-214` and `docs/guides/workspace-config-sources.md:575-587`
  describe the `<repo>-overlay` slug convention, which feeds the name but does not
  pin its on-disk shape.
- `docs/guides/functional-testing.md:123-127` documents that `DeriveOverlayURL`
  supports `file://` so the convention path works offline in tests.

### Code that would need a migration

No lookup code needs changing — every site recomputes from the URL. A migration,
if one were wanted, would need to be a one-shot rename of existing
`overlays/<old>` → `overlays/<new>`, and there is no existing place for it to
live: there is no `niwa migrate`, no startup migration hook, and the only
directory-reclaiming command (`internal/cli/reap.go`) never touches `overlays/`.
Without one, the cost is bounded and self-healing — a single extra fetch per
overlay per machine, plus a dead directory that nothing ever removes.

---

## 6. Prior art for "niwa owns this directory" markers

Four mechanisms exist, plus two that establish ownership by name instead.

### 6a. `.niwa-delivered-tree` — the explicit ownership sentinel

The closest existing analogue to a `D@swap` sentinel.

- Name: `internal/agentplan/skills.go:41` — `const treeMarkerFileName = ".niwa-delivered-tree"`; exported at `:46`. Rationale at `:31-40`: "it is deliberately the only thing niwa adds to a tree it otherwise reproduces verbatim."
- Contents: an owner line — `generationMarker` at `internal/agentplan/composition.go:92`, wired in at `internal/agentplan/skills.go:147` and `internal/agentplan/rootskills.go:140,168,211`.
- Written: `internal/workspace/applyplan.go:342`, at the end of `deliverPlanTree`, on the copy-fallback branch only (the symlink branch returns early at `:332`).
- Read: `treeCopyIsNiwas` at `internal/workspace/applyplan.go:375-382` — reads the file, compares the first line to the expected owner.

Two guards refuse to delete a directory lacking the marker:
- `internal/workspace/applyplan.go:319-325` — before replacing a directory at a delivery path, `if !treeCopyIsNiwas(path, owner) { return errForeignDeliveryTarget }`. Doc at `:292-296`: "Anything else at the path is left exactly as it is and fails the delivery, because replacing a tree means removing it first and that is not a call to make on a guess."
- `internal/workspace/pluginskills.go:550-559` — the de-configured-delivery sweep leaves an unmarked directory alone with a warning: "is not something niwa delivered; leaving it as it is". Called from `internal/workspace/pluginskills.go:487` and `internal/workspace/rootskills_delivery.go:81`.

### 6b. `.niwa/instance.json` — the stamp gating every instance deletion

`StateDir = ".niwa"` (`internal/workspace/state.go:27`),
`StateFile = "instance.json"` (`:35`).

`ValidateInstanceDir` (`internal/workspace/destroy.go:77-89`) is two-sided: the
directory **must** carry `.niwa/instance.json` and **must not** carry
`.niwa/workspace.toml` (which would make it a workspace root). Every
directory-deleting call site checks it first: `internal/workspace/destroy.go:163`
(before `os.RemoveAll` at `:175`), `internal/workspace/destroy_workspace.go:82`
(before `:95`), `internal/cli/destroy.go:104`,
`internal/cli/instance_from_hook.go:517` — the last being the
`destroyInstanceFunc` used by reap (`internal/cli/reap.go:476,742`), watch
(`internal/cli/watch.go:718,800`) and dispatch rollback
(`internal/cli/dispatch.go:517`).

One documented exception: `DestroyWorkspace` deliberately does not validate the
root (`internal/workspace/destroy_workspace.go:41-43` — "that validator is
specifically designed to refuse this operation"), so its final
`os.RemoveAll(abs)` at `:102` is unguarded by any marker.

### 6c. `.niwa-snapshot.toml` — the provenance marker on config-snapshot dirs

`ProvenanceFile = ".niwa-snapshot.toml"` (`internal/workspace/state.go:39`).
Written into staging before the swap (`internal/workspace/snapshotwriter.go:423`)
and refreshed in place on a no-drift check (`:190`). Checked by
`provenanceMarkerExists` (`internal/workspace/snapshotwriter.go:113-115`), which
drives the three-case dispatch at `:92-110`.

Used as an ownership test in exactly the sense this decision needs:
`internal/cli/reset.go:174` — `isClonedConfig` treats the marker's presence as
"niwa cloned this, it is safe to discard and re-materialize"; absent marker and
absent `.git/` means user-authored and not niwa's (doc at `:163-171`). Also read
at `internal/guardrail/githubpublic.go:97` and `internal/cli/apply.go:551`.

### 6d. `.niwa/dispatch-retain` / `.niwa/dispatch-pending`

`internal/cli/dispatch.go:99` and `:80`. The retain marker is read as a delete
guard at `internal/cli/reap.go:613-619`, and `dispatchRetainReason`
(`internal/cli/dispatch.go:1321-1330`) treats an unreadable-but-present file as
present, with the reasoning at `:1317-1320`.

**The most directly relevant precedent for this decision** is
`internal/cli/reap.go:528-534`: the reaper's eligibility signal is the directory
**name**, `isDispatchInstanceName` matching `\+[a-z0-9_]*-[0-9a-f]{8}$`, not a
marker file — adopted "to close the SIGKILL-before-marker window a marker-file-only
gate left open (an instance unmapped AND unmarked -> formerly an unreclaimable
orphan)". That is a recorded verdict that "create the directory, then write a
marker into it, then gate deletion on the marker" has a crash window, and that
niwa chose a name predicate to close it.

### 6e. Ownership by name, and by in-file delimiters

`internal/gitexclude/exclude.go:27-28` — `# >>> niwa managed >>>` /
`# <<< niwa managed <<<` delimit the block niwa rewrites in `.git/info/exclude`,
preserving everything outside (doc at `:22-25`). Same idea at file scope:
`generatedConfigMarker` (`internal/agentplan/payload.go:80`), `generationMarker`
(`internal/agentplan/composition.go:92`), consumed via `OwnerMarker`
(`internal/agentplan/composition.go:167-168`).

### Negatives

No `.niwa-owned` literal exists. The only `.gitkeep` is
`internal/workspace/scaffold.go:271-278`, written so an empty content dir survives
a commit and never read. No `clean`, `prune` or `gc` CLI command exists —
`internal/cli/` contains `destroy.go` and `reap.go` and nothing else of that shape.

---

## 7. Windows

### Does the repo build these packages for Windows today?

Partly, and nothing verifies it in CI.

- **CI matrix is Linux and macOS only**: `.github/workflows/test.yml:53` —
  `os: [ubuntu-latest, macos-latest]`. There is no `GOOS=windows` job.
- **Release artifacts are Linux and macOS only**:
  `.github/workflows/release.yml:50-53` lists `niwa-linux-amd64`,
  `niwa-linux-arm64`, `niwa-darwin-amd64`, `niwa-darwin-arm64`.
- **The installer refuses anything else**: `install.sh:36-42` —
  `case "$OS" in linux|darwin) ;; *) "Unsupported OS"`.
- **`GOOS=windows go build ./...` does not pass today.** Stated as a platform
  acceptance criterion in `docs/prds/PRD-session-store-teardown.md:526-531`:
  per-package `GOOS=windows go build` is required for the packages this change
  touches, but the whole-tree build "is not required and does not pass today:
  `internal/cli/sessionattach` and `internal/promptcapture` already fail to build
  for Windows."
- Docs state the same posture: `docs/guides/vault-integration.md:596` — "macOS and
  Linux only. Windows users should run niwa from WSL";
  `docs/prds/PRD-vault-integration.md:1116`.

The packages that own the config directories — `internal/config` and
`internal/workspace` — do compile for Windows, and the codebase carries
`//go:build !unix` fallbacks: `internal/workspace/codex_trust_lock_other.go`
(no substitute lock, with the reasoning in its header comment) and
`internal/cli/writer_lock_other.go`. The flock implementations are
`//go:build unix`: `internal/workspace/codex_trust_lock_unix.go:22-41`,
`internal/cli/writer_lock_unix.go:72`. One explicit Windows behaviour branch
exists: `internal/workspace/applyplan.go:284` —
`treeDeliveryPrefersCopy = func() bool { return runtime.GOOS == "windows" }`,
because "directory symlinks need elevated privileges on Windows"
(`internal/workspace/applyplan.go:275-283`).

The in-flight design already scopes itself accordingly:
`docs/designs/DESIGN-session-store-teardown.md:355` ("Recovery does not run on
non-unix"), `:1380` ("Non-unix platforms get neither ordering nor crash
recovery"), `:838-839` (no `GOOS=windows` CI job to catch a build-tag mistake).

### Character behaviour on Windows

These are properties of the platform, not of this repo, and nothing in the repo
guards against them:

- **`:` cannot appear in a Windows filename.** It is the drive separator and, on
  NTFS, the alternate-data-stream separator — `name:stream`. A derived overlay
  name containing `:` (reachable from `https://github.com/a:b/c` →
  `a:b-c`, from `git@h:o:x/r` → `o:x-r`, and from any shorthand whose *repo*
  component carries a subpath, `acme/tools:.niwa` → `acme-tools:.niwa`) cannot be
  created as a directory on Windows. On Linux it creates fine.
- **`\` is a path separator on Windows.** `acme/tools\evil` derives
  `acme-tools\evil`, which is one literal directory name on Linux but a
  two-segment path on Windows. `parseOrgRepo` splits only on `/`
  (`internal/config/overlay.go:272,290,304`), so `\` passes through unfiltered and
  the traversal properties of the name differ by platform.
- **`@` is legal on Windows** and behaves the same as on Linux. The `@swap`
  collision is not platform-specific.
- **Trailing dots and spaces are stripped by the Win32 layer.** `acme/tools.` and
  `acme/tools ` are distinct names on Linux but collide with `acme-tools` on
  Windows.
- `internal/config/overlay.go:231-235` (`isProtectedDestination`) calls
  `filepath.ToSlash` before prefix-matching, which is the one place the codebase
  normalizes separators — and it applies to overlay *file destinations*, not to
  the directory name.

`docs/designs/archive/DESIGN-cross-session-communication.md:1193` records a
related caveat: "Windows processes accessing `.niwa/` via WSL's `/mnt/c` path
mapping may bypass Linux UID semantics. Niwa's guarantees apply only to
Linux-native access paths." A WSL workspace root under `/mnt/c` with
`$XDG_CONFIG_HOME` on the Linux-native filesystem is also a concrete instance of
the §2 EXDEV problem.
