---
schema: design/v1
status: Proposed
problem: |
  Every create fetches each github plugin marketplace's tarball into the new
  instance's .niwa/marketplaces/, a tree of about 58 MiB that is identical in
  every instance on the same release. Nothing names a release on disk, so
  nothing can be held once, found again, or deleted safely, and a host with
  many instances of a workspace pays the download and the disk once per
  instance.
decision: |
  Each release is resolved to a commit and held once per workspace at
  ~/.niwa/w-<key>/plugins/github.com/<owner>/<repo>/<commit>/, beside PR
  #511's mirrors/ and under the same workspace key, its tree made read-only
  and sealed. An instance holds only a symlink to it. A per-release flock
  serializes placement and linking. Every provisioning command scans the
  links of every instance on disk, kept ones included, refetches a release
  that is absent, unmarked or no longer matches its seal, and sweeps one no
  instance links to unless it is the newest of its source by commit time.
  [workspace] share_plugin_releases = false
  turns placement off, and an unusable store falls back to today's fetch
  with a notice naming the path and condition.
rationale: |
  A commit names a moving ref, survives a moved tag, and lets a refetch
  reproduce the exact tree, so one scheme covers every track. The instance's
  link is its only pointer into the store, so reading links on disk answers
  both what is missing and what is in use with no record to drift. A lock per
  release lets a burst download once without serializing unrelated
  releases. Reusing #511's key, lock primitive and notice channel gives the
  git-history store one host layout instead of two.
upstream: docs/prds/PRD-plugin-release-hold.md
decision_provenance: inline-resolved
---

# DESIGN: plugin-release-hold

## Status

Proposed

## Context and Problem Statement

niwa delivers plugin skills from a tree it fetches itself, so skill delivery
never depends on another tool's plugin directory. For a marketplace whose
source is `org/repo`, `ensureMarketplaceContent` in
`internal/workspace/pluginskills.go` picks a ref with `marketplaceContentRef`
(the latest stable `vX.Y.Z` tag by default, `HEAD` for `track = "main"`,
anything else verbatim), downloads the GitHub tarball at that ref, extracts it
into `<instance>/.niwa/marketplaces/<name>.staging`, and renames it to
`<instance>/.niwa/marketplaces/<name>`. Once the manifest
(`.claude-plugin/marketplace.json`) is there, it is never fetched again, which
is what keeps an instance on the release it was created with. Step 6.2 of
`runPipeline` resolves the trees once per apply; repo, instance-root and
session-worktree deliveries all link to paths inside that directory, and the
worktree path passes no fetcher, so it only reuses what the instance holds.

The tree is the same for every instance on the same release, about 58 MiB for
the two marketplaces a typical workspace declares. Measured across 134
instances on one host, that came to about 7.5 GiB where one copy per release
would have taken about 350 MiB, and a burst of dispatches downloads the same
tarballs once per worker.

### What has to change

Four things in the code stand between today and one copy per release:

1. **Nothing names a release on disk.** The directory is named after the
   marketplace, not the ref, and a marketplace tracked at `HEAD` or a branch
   has no stable name until its ref is resolved to a commit. The PRD asks
   that such a marketplace give an instance, for the commit its ref resolved
   to at create time, exactly the content a per-instance copy fetched at that
   commit would hold (R12), and that an instance keep its release for life
   (R13).
2. **The obvious home is taken and unsafe.** `<workspace root>/.niwa/marketplaces/`
   holds the managed `repo:` copies, and the workspace root's `.niwa/` is
   recopied file by file when the config snapshot is swapped
   (`internal/workspace/snapshot_carryover.go`). The PRD applies the rule for
   host-level state and does not reopen it (R16): *host-level state that niwa
   owns, and that is large or must keep its identity across a config refresh,
   lives under ~/.niwa/, in a directory keyed by the workspace root's
   canonical path. It never lives under the workspace root's .niwa/ or inside
   an instance.* The plugin store is the first thing placed under that rule.
3. **A shared release can go missing under instances that use it.** A person
   frees disk by hand, or a disk problem removes it. The PRD requires niwa to
   fetch it back during ordinary provisioning commands with nobody acting,
   never through a command a person runs (R6), and requires instances created
   before the loss to load their skills again with nothing run inside them
   (R7). A refetch that fails must not fail the command, and nothing partly
   written may count as held (R10).
4. **Deleting a shared release is now a cross-instance question.** The
   managed copy keeps the current and previous checkout and deletes the rest,
   which would delete an old kept instance's release. The PRD forbids
   deleting any release that any instance directory on disk under the
   workspace root still points at, whatever that instance's state (R11).

### Boundaries

The provisioning commands are exactly `niwa create`, `niwa apply`,
`niwa dispatch`, `niwa reset`, `niwa init`, the SessionStart hook's
`niwa instance from-hook`, `niwa watch` when it provisions, and `niwa resume`
when it restores a removed instance. All of them reach `Applier.Create` or
`Applier.Apply` and so `runPipeline`. `niwa status`, `niwa list`, `niwa go`
and the `niwa worktree` commands fetch nothing and stay that way.

Out of scope: the managed `repo:` copies at the workspace root, Claude Code's
own plugin directory under `~/.claude/plugins` (Claude registers github
marketplaces with its own clone; this design changes only niwa's tree),
single-instance layouts with no workspace root (R14), and the git-history
store. PR #511 (the mirror-repair design, still a draft) settles where the
git mirror lives, how it is locked and how it is repaired; this design reuses
its workspace key and lock primitive and does not redefine either.

## Decision Drivers

From the requirements:

- **Held once, never downloaded twice (R1, R2, R18).** From the second create
  on, a create whose releases are all held downloads none and writes no tree
  into the instance; on a filesystem with symlinks the instance holds only
  links, and its plugin content measures under 1 MiB.
- **Same skills, same places (R3).** Delivery at the instance root, in each
  repo and in session worktrees works from the held copy unchanged.
- **No new surface (R4, R5).** The store rides the creates niwa already
  performs, needs no new command, flag, daemon or scheduler, and is on
  wherever a workspace root exists with no config change.
- **Nobody repairs (R6, R7, R10).** Missing means the release directory is
  absent or present without its completion mark; any provisioning command
  restores any missing release any instance on disk uses; a failed restore
  warns, names the release and retries next time.
- **Never delete what is in use (R11)**, kept instances included.
- **Moving refs and pinned instances (R12, R13).**
- **An off switch and a loud fallback (R8, R9, R14, R15).** A workspace
  setting turns it off for that workspace alone; an unreadable, unwritable or
  uncreatable store, or a release that can't be placed, falls back to today's
  per-instance fetch and says so with the store's path and the condition,
  including in dispatch's output.
- **Concurrency (R17).** Overlapping creates download a missing release once,
  and none uses a release that isn't held.
- **Location (R16)** as quoted above.

Implementation drivers: reuse PR #511's workspace key, `filelock.WaitFileLock`
and lock-name scheme so the host has one layout; reuse the GitHub client and
`github.ExtractSubpath` with its existing path, symlink and size defences;
keep a healthy check offline and cheap enough to run on every provisioning
command; fail open on the command and closed on deletion; and give the
git-history store one written contract for what it can take from this one.

## Considered Options

### Decision 1: Release key and on-disk layout

Resolved inline.

A store needs a name for each release that is known before any download (or
held releases cost a download anyway), stable for a moving ref (R12), and
reproducible by a refetch years later (R7, R13). It also has to say what the
instance holds in place of its tree.

Key assumptions:
- GitHub's `GET /repos/{owner}/{repo}/commits/{ref}` with the sha media type,
  already wrapped as `APIClient.HeadCommit` and part of the applier's
  `FetchClient`, resolves a tag (annotated ones peeled), a branch or `HEAD`
  to a 40-hex commit in one small request.
- A GitHub tarball's single top-level directory is
  `<owner>-<repo>-<abbreviated commit>`. If that ever changes, the check on
  it is dropped and the commit-pinned URL alone carries identity.

#### Chosen: source and resolved commit, with the instance holding a link

niwa picks the ref exactly as `marketplaceContentRef` does today, resolves it
to a commit with `HeadCommit`, and from then on uses only the commit: the
tarball is fetched at the commit, never at the ref, so the content matches its
key even if the ref moves between the two requests. A held release is the
directory

```
~/.niwa/w-<key>/plugins/github.com/<owner>/<repo>/<commit>/
  tree/        the extracted marketplace, read-only
  held.json    the completion mark and seal, read-only
```

Owner and repo are lowercased (GitHub names are case-insensitive) and must
match GitHub's name rules; the commit is 40 lowercase hex. `held.json`
records the schema version, the source, the ref asked for and the tag when
there is one (each kept only if it matches a strict ref pattern, otherwise
left empty), the commit, the commit's time, when it was placed, the niwa
version, and a seal: the tree's entry count, total size and the newest
inode change time (ctime) among its entries once it was made read-only.
Messages say "acme/skills v1.4.0" when a clean tag is recorded and the short
commit otherwise. The commit time is the modification time GitHub's archive
gives every entry (git archive stamps entries with the committer date), so a
refetch of the same commit reproduces it exactly. The `github.com` segment
leaves room for another forge without a migration.

After extraction and before the mark is written, niwa strips every write bit
from the tree: directories become 0555, files 0444, or 0555 where the
archive marked them executable. Agents read skills through the links; none
of them is meant to write there, and a plain write through a link from any
instance now fails instead of reaching every instance.

The instance's `<instance>/.niwa/marketplaces/<name>` becomes a symlink whose
absolute target is `.../<commit>/tree`. `ResolvePluginTrees` keeps returning
roots under the instance path, so the repo, root and worktree deliveries link
through it as they do today, and that one link is the instance's only pointer
into the store. Retargeting a link is the change; nothing that reads skills
learns about the store.

#### Alternatives Considered

**Key by the tag string.** Readable, and no resolve request. Rejected because
a moving ref has no stable name, so it needs a commit anyway, and a tag that
is moved or deleted and recreated would make a refetch produce different
content under the same name, moving an old instance to new skills behind its
back.

**Tags by name, moving refs by commit.** Rejected because it keeps both code
paths and the moved-tag hazard.

**Hash of the extracted tree.** Rejected because the hash is known only after
the download it is supposed to avoid.

**Hold release tags only, and keep a per-instance copy for moving refs.**
Rejected because it keeps two instance layouts and duplicates exactly the
marketplaces that change most often.

**A relative link, or hardlinks.** A relative path from an instance to the
home directory breaks when either moves, and hardlinks fail across volumes
and leave release files inside the instance, against R18.

### Decision 2: Placement, locking and repair

Resolved inline.

R17 asks that a burst of creates download a missing release once and that no
instance ever link to a half-written one. R10 asks that an interrupted or
corrupt download is never marked held. R6 and R7 ask that any provisioning
command restore any missing release that any instance on disk uses. PR #511
adds a waiting flock, `filelock.WaitFileLock`, and names its locks in
`~/.niwa/locks/` by hashing the workspace key and the item's id.

Key assumptions:
- `flock` works where `~/.niwa/locks/` lives, as niwa's instance locks
  already assume.
- A tarball download and extraction finishes well inside two minutes, so a
  holder past that is stuck rather than slow.
- An instance's directory exists under the workspace root before Step 6.2
  runs, so a concurrent scan sees it.

#### Chosen: a lock per release, staged placement, and a repair pass over every instance's links

**Placement.** `Ensure` takes the release's lock,
`~/.niwa/locks/p-<first 32 hex of sha256(workspace key + "/" + release id)>.lock`,
with `WaitFileLock` and a two-minute wait, where the release id is
`github.com/<owner>/<repo>/<commit>`. Under the lock it checks again whether
the release is held. If it is, it links the instance and releases the lock.
If not, it clears any staging directory a killed placement of the same
release left (`.tmp/<release hash>-*`, by exact name) and any release
directory that exists without a valid mark (renamed into `.trash/` first,
then removed), downloads the tarball at the commit into
`.tmp/<release hash>-<rand>/`, extracts it into `tree/` with
`github.ExtractSubpath`, checks that the wrapper names the commit and that
the manifest exists, strips the tree's write bits, takes the seal, writes
`held.json` (0444) to a temporary name, syncs and renames it, syncs the
directory, and renames the staging directory to `<commit>`. The instance is
linked under the same lock: a temporary link is made and renamed over
`<instance>/.niwa/marketplaces/<name>`, where the instance is the one the
command runs for and `.niwa/marketplaces` is checked with `Lstat` to be a
real directory. Any failure before the final rename removes only the staging
directory, so a release is either absent or complete, sealed and marked.

**Held, offline.** A release is held when `held.json` parses, names this
commit and source, and `tree/.claude-plugin/marketplace.json` exists. That
is three stats and a short read; it never reads the tree. Repair and the
sweep use this check.

**Verified, on use.** Whenever a command links an instance to a release, and
once per command for each release Repair finds in use, niwa also walks the
tree with `Lstat` and compares it with the seal: the same entry count and
total size, no write bit on any entry, and no ctime newer than the sealed
one. ctime can't be set back from user space, so any write, chmod, rename or
added file inside the tree moves it. A release that fails the walk is
treated as missing: under its lock it is renamed into `.trash/` and fetched
again at its commit, and the restore is reported as a tampered release. The
walk reads metadata only, never file contents; at a few thousand entries per
release it is an estimate of milliseconds, not a measured figure.

**Repair.** Once per provisioning command per workspace, before the current
instance's own placement, `Repair` lists every instance on disk, kept ones
included (Decision 3's scan), reads each entry of
`<instance>/.niwa/marketplaces/`, and keeps the symlinks whose target parses
as a release link. For each distinct release it checks held offline and
verifies the seal; a missing or tampered one goes through the same locked
placement, fetched at its commit. Repair writes only inside the store: it
never creates, renames or removes anything inside an instance. A link whose
target names another home or workspace key (an instance moved here) gets
its release held here, and is relinked to this store's path by that
instance's own next provisioning command, which is the only writer of its
links. Each restore is reported with the release and the instances it
served; a failed one is a warning naming the release, nothing it wrote is
marked held, and the next provisioning command tries again.

Repair is pinned to the source a link names. It fetches only releases whose
`owner/repo` is a github marketplace source the effective workspace config
declares, and before fetching a commit it didn't just resolve from the
config, it checks that the commit belongs to that repository's own history:
the commit must equal a tag or branch tip that `git ls-remote` of the source
lists, or the compare endpoint (the one `APIClient.CompareCommits` already
calls) must report it `identical` to or `behind` one of those refs, trying
the default branch and then stable tags, newest first, at most 20 calls. This
matters because GitHub serves a tarball for a commit that exists only in a
fork of the repository, since forks share storage; a commit only a fork
holds is never behind a ref of the source, so it is refused and reported,
and nothing from it reaches the store under the source's name. A link to an
undeclared source is reported and left alone.

#### Alternatives Considered

**One lock for the whole store.** Rejected because it serializes unrelated
releases in a burst and lets a slow sweep stall every create.

**No lock, first rename wins.** Rejected because every create of a burst
would download, which fails R17.

**Repair only the current instance's releases.** Rejected because a new
create on a newer release would leave an older instance broken, the case the
PRD's decision on refetch scope was written against.

**Hash every file of a held release on each use.** Rejected because it reads
about 58 MiB per release per command. The metadata walk against the seal
catches every write made through the normal file calls for the cost of
`Lstat`s; what it misses is in Security Considerations.

**A background checker.** Rejected by R4 and R6.

### Decision 3: The deletion rule and the in-use scan

Resolved inline.

This is the one question where getting it wrong hurts an instance: delete a
release some instance still links to, and that instance loses its skills
until the next refetch. Never deleting is safe but lets the store grow with
every release and, for a marketplace tracked at `main`, with every commit,
with no way back but a person deleting by hand. R11 fixes the boundary: never
delete a held release that any instance on disk points at, whatever that
instance's state, kept ones included. The damage from a wrong delete is
recoverable (a refetch at the commit restores the same bytes), but it costs
a download and a window without skills.

Instances live in two places. Live ones, and kept ones the instance
retention design leaves in place, are immediate children of the workspace
root. Separate teardown work keeps a finished instance outside the root, at
`~/.niwa/w-<key>/kept/<instance>/`, under the same location rule and the same
workspace key this design computes. Both places hold instances whose links
must count.

Key assumptions:
- Every instance of a workspace on disk is either an immediate child
  directory of the workspace root or an entry of `~/.niwa/w-<key>/kept/`. A
  later feature that keeps instances anywhere else adds that place to
  `hostdir.InstanceDirs`, and until it does, its instances don't protect
  their releases.
- An instance points into the store only from
  `<instance>/.niwa/marketplaces/<name>` (Decision 1).
- Links are made only under the release's lock (Decision 2).

#### Chosen: a scan-based sweep over the root and the kept directory, keeping the newest release of each source

The sweep runs once per provisioning command per workspace, after Repair and
after the current instance is linked. Its candidates are held releases that
are not the newest held release of their source, plus leftover staging and
trash entries. Newest means the latest commit time recorded in `held.json`,
ties broken by commit hash. The commit time comes from the archive itself,
so a refetch reproduces it: a restored old release stays old, and a newer
unused one does not become a candidate because an older one was restored.
For each candidate release the sweep takes that release's lock, scans, and
deletes only if nothing links to it.

The scan (`hostdir.InstanceDirs`) reads two directories: the workspace root
and, when it exists, `~/.niwa/w-<key>/kept/`. In each it lists entries with
`Lstat` semantics, as `EnumerateInstances` does: a real directory is a
candidate instance, and a symlink or any other entry type is skipped, never
followed, so nothing outside those two directories is read or written
through the scan. It skips `.niwa` at the root and does not require
`.niwa/instance.json`, so an instance half-created, half-destroyed or
half-moved into `kept/` still counts. For each instance it reads the entries
of `<instance>/.niwa/marketplaces/` (itself checked with `Lstat` to be a real
directory) and counts any symlink whose target parses as that release,
through this store's path or another home's, as a use. If the root or an
existing `kept/` directory can't be listed, or any instance there that has a
`.niwa` directory can't be read, the sweep stops for that command and
deletes nothing. A release with no use has write bits restored on its own
entries only, is renamed into `.trash/` (so a reader sees the whole release
or nothing) and removed; empty owner and repo directories go with it.

`niwa destroy` deletes nothing from the store; the next provisioning command
sweeps what it freed. When niwa removes a whole workspace
(`DestroyWorkspace`, reached by `niwa destroy --force` at the root and by
`niwa destroy` at an empty root), it computes the store path from the
workspace root before removing anything, since `CanonicalPath` of a root
that is gone could resolve differently. After the root is removed, it
removes `~/.niwa/w-<key>/plugins/` only when `~/.niwa/w-<key>/kept/` is
absent or empty; with kept instances still there, it leaves the store in
place and prints its path, because those instances link into it and nothing
would repair them once the workspace is gone. It removes `~/.niwa/w-<key>/`
only if that leaves it empty, so a sibling such as the mirrors directory is
left to its own feature. A workspace root removed by hand leaves its store
behind.

#### Alternatives Considered

**Never delete automatically.** No risk to R11 at all. Rejected because a
moving-ref marketplace grows the store by a full tree per commit with no
bound, and the only way to reclaim it would be a person deleting by hand,
which is the habit this feature is meant to end.

**A reference count written at link and destroy time.** Rejected because a
record drifts from disk (an instance removed with `rm -rf`, a crash between
the two writes, an instance restored from an archive), and a count that
drifts toward zero deletes a release in use. The PRD defines in use by what
is on disk, so the rule reads disk.

**Expire releases unused for N days.** Rejected because the oldest releases
are exactly the ones old kept instances hold; made safe by a scan, the age
adds nothing but delay.

**Keep the last N releases per source**, as the managed copy keeps two.
Rejected because it deletes a kept instance's old release.

**Order "newest" by when a release was placed.** Rejected because a restore
places an old release again and would make it look newest, leaving a newer
unused release open to deletion; the commit time survives a refetch.

**Delete at `niwa destroy` when the destroyed instance was the last user.**
Rejected as the trigger because it misses instances removed any other way and
needs the same scan to know it was the last.

### Decision 4: The off setting and the fallback

Resolved inline.

R5 puts the store on wherever a workspace root exists with no config change;
R8 asks for a workspace setting that turns it off for that workspace alone,
without breaking instances already linked; R9 asks for a fallback that says
what happened; R14 keeps today's behaviour with no workspace root. niwa's off
switches are pointer booleans where an absent key means on, such as
`read_env_example` in `[workspace]`, which also has a per-repo override, and
`work_summary_hooks` in `[claude]`. `internal/fallbacknotice` already carries
a provisioning run's fallback notices to the terminal for create, apply, init
and reset, to dispatch's and watch's stderr, and into the SessionStart hook's
injected context.

Key assumptions:
- A workspace that turned the store off wants no notice about it.
- The key merges from the workspace overlay like other `[workspace]`
  booleans.

#### Chosen: `[workspace] share_plugin_releases`, and four named conditions that fall back

`WorkspaceMeta.SharePluginReleases *bool`, TOML `share_plugin_releases`, read
through `SharePluginReleasesEnabled()`: absent or `true` is on, `false` is
off.

```toml
[workspace]
name = "acme"
share_plugin_releases = false
```

It governs placement and linking only. With it off, a create writes its own
full tree into the instance exactly as today, while Repair and the sweep
still run, so instances already linked keep being repaired until they are
removed and are never converted.

The fallback is decided by what failed:

| Condition (as printed) | When |
|------------------------|------|
| `unreadable` | the plugins directory, or a held release's mark, can't be read (permission or I/O error) |
| `unwritable` | the release isn't held and its staging directory can't be created |
| `can't be created` | the plugins directory is absent and creating it fails |
| `release not placed` | the download succeeded but extraction, the mark or the rename failed (a full disk, say), or the lock wait ran out, or locking is unsupported |

Each falls back to today's per-instance fetch into a real directory at the
instance path and records a plugin-store notice with the marketplace, the
store's path (home shown as `~`) and the condition, for example:

```
niwa: plugin store ~/.niwa/w-3f9a.../plugins is unwritable; fetched acme/skills v1.4.0 into this instance instead
```

A notice carries only fields niwa validated: the lowercased owner and repo,
the short commit, the condition from the fixed list, the store path niwa
built, and a tag only when it matches `v?[0-9]+\.[0-9]+\.[0-9]+` (otherwise
the short commit stands in). A ref or tag string the remote chose never
reaches the terminal, dispatch's output or the SessionStart hook's context
unchecked; anywhere else one is shown (a verbatim `track` echoed back, say),
it is cut to printable ASCII and 100 bytes first.

A source that can't be reached is not a store condition: a create needing a
release that isn't held reports the marketplace it couldn't fetch, delivers
none of its skills and marks nothing held, as today (R15). With no workspace
root the store code is never entered and nothing is written under `~/.niwa/`
for plugins (R14).

#### Alternatives Considered

**`[claude] plugin_store`.** Rejected because `[claude]` governs Claude
Code's own registration, while these trees feed every agent's tree delivery
and the instance root.

**A new `[stores]` table.** Rejected because one key does not need a table,
and `[workspace]` already holds this kind of switch, including the per-repo
override form the git-history store will want.

**A key in the global niwa config.** Rejected because R8 is per workspace and
the other workspaces on the host keep sharing.

**Fail the create, or fall back silently.** Both rejected by R9.

## Decision Outcome

Every github marketplace a workspace declares is held once per host and
workspace, keyed by the commit its track resolves to. A create resolves the
ref with one small request, finds
`~/.niwa/w-<key>/plugins/github.com/<owner>/<repo>/<commit>/` already marked
held, and makes `<instance>/.niwa/marketplaces/<name>` a symlink to its
`tree/`; nothing is downloaded and the instance holds only links. When the
release isn't held, the create takes that release's lock, downloads at the
commit into a staging directory, makes the tree read-only, seals it, writes
the completion mark inside it and renames it into place, then links, all
under the lock, so a burst of creates downloads once and the waiters find
the release complete. The key directory is PR #511's, so the plugins
directory sits beside the mirrors directory, and the lock is #511's waiting
flock under its naming scheme with a `p-` prefix.

Every provisioning command also reads the marketplace links of every instance
on disk once, both under the workspace root and under the kept directory,
and restores any release that is absent, unmarked or no longer matching its
seal by fetching it at its commit, after checking that the commit belongs to
the declared source's own history. An instance created before the loss gets
back exactly its release, and repair writes nothing outside the store. The
same scan decides deletion: a release no instance links to, and that isn't
the newest of its source by commit time, is deleted under its lock after a
fresh scan; an unreadable instance or kept directory stops deletion for that
command. Removing the workspace through niwa removes its plugins directory
unless kept instances still link into it. `[workspace]
share_plugin_releases = false` sends new creates back to a per-instance copy
without touching instances already linked, and a store that is unreadable,
unwritable, can't be created, or can't take a release falls back to the same
per-instance copy with a notice that reaches the terminal, dispatch's output
and the SessionStart hook's context.

The pieces hold each other up. The commit key is what lets one link per
instance carry everything: it names the release for the scan, the lock, the
refetch and the deletion check, so no record has to agree with the disk. The
lock that serializes placement is the same one that guards linking and
deletion, which closes the race between a create linking a release and a
sweep deleting it. Repair and the sweep share one pass over the instances, so
the price of keeping the store honest is a few hundred `readlink` calls and
one metadata walk per release in use per command on a large host, and no
network unless something is missing.

## Solution Architecture

### Overview

A new package, `internal/pluginstore`, owns the layout, the held check,
locked placement, link parsing, repair and the sweep. `internal/workspace`
calls it from `ensureMarketplaceContent` (placement for the instance at hand)
and from `runPipeline` (repair before Step 6.2, sweep after Step 6.2b), and
from `DestroyWorkspace`. It depends on `internal/filelock` (PR #511's leaf
with `WaitFileLock` and `CanonicalPath`) and on a small leaf for host
directories, and never imports `internal/workspace`; callers pass in the
workspace root, the fetch functions and the declared sources.

### Components

**`internal/hostdir`** (a leaf): `WorkspaceKey(root)` returns `w-` and the
first 32 hex digits of the sha256 of `filelock.CanonicalPath(root)`, the
definition PR #511 gives in `internal/mirror`; `WorkspaceDir(home, root)`;
`EnsurePrivateDir(dir)` (0700, owned by the user, a symlink refused); and
`InstanceDirs(home, root)`, the scan of Decision 3 over the root's children
and the entries of `~/.niwa/w-<key>/kept/`, real directories only. Whichever
of the two features lands second moves #511's `WorkspaceKey` and
`ensurePrivateDir` here unchanged and has `internal/mirror` import them, so
there is one definition of the key.

PR #511 is a draft, and this design follows whatever it merges rather than
what it says today. If its key, its lock primitive, its lock-name scheme or
its notice channel change before it lands, this design takes the merged
form: the feature that lands second adopts the first one's `internal/filelock`
and `internal/hostdir` as they are, or changes both call sites in the same
commit. A unit test resolves the key directory through both stores' path
helpers for the same root and fails if they differ, so a divergent key is a
test failure rather than a silent second layout. If #511 never lands, this
work's first phase carries the key, `CanonicalPath`, `WaitFileLock` and the
lock-name scheme itself, as #511 drafts them.

**`internal/pluginstore`**: the store. It never runs git and never follows a
link it read from an instance; it only parses link targets. The only write it
makes outside the store is the one link `Ensure` places for the instance the
command runs for.

**`internal/config`**: `WorkspaceMeta.SharePluginReleases` and its accessor.

**`internal/workspace`**: `ensureMarketplaceContent` gains the store branch,
taken when `workspaceRootHoldsState(filepath.Dir(instanceRoot))` and the
setting is on; `runPipeline` calls `Repair` and `Sweep` once per command per
workspace key (a set on the `Applier`, so a root-level apply over many
instances runs them once); `DestroyWorkspace` computes the store path first
and removes the plugins directory once the root is gone and no kept instance
remains.

**`internal/fallbacknotice`**: a plugin-store note kind (marketplace, store
path, condition, or a restored or restore-failed event), so the existing
rendering reaches every provisioning surface.

### Key Interfaces

```
~/.niwa/
  locks/p-<sha256(workspace key + "/" + release id)[:32]>.lock    0600
  w-<sha256(CanonicalPath(workspace root))[:32]>/                  0700, the workspace key
    mirrors/                                      PR #511's, untouched here
    kept/<instance>/                              the teardown work's, only read here
    plugins/                                      0700
      github.com/<owner>/<repo>/<commit>/         a held release, 0700
        tree/                                     the marketplace: dirs 0555, files 0444 or 0555
        held.json                                 the completion mark and seal, 0444
      .tmp/<release hash>-<rand>/                 a placement in progress
      .trash/<release hash>-<rand>/               a release being removed

<instance>/.niwa/marketplaces/<name>  ->  ~/.niwa/w-<key>/plugins/github.com/<owner>/<repo>/<commit>/tree
```

Owner and repo match `[a-z0-9][a-z0-9-]{0,38}` and `[a-z0-9._-]{1,100}`
(not `.` or `..`) after lowercasing; a source that doesn't is fetched into the
instance as today. Nothing niwa creates under `plugins/` other than
`github.com` starts without a dot, so no source can take `.tmp` or `.trash`.

```json
{"schema": 1, "source": "acme/skills", "ref": "v1.4.0", "tag": "v1.4.0",
 "commit": "3f9a2c1e...", "commit_time": "2026-11-02T08:41:10Z",
 "placed_at": "2026-11-17T10:15:02Z", "niwa": "0.60.0",
 "seal": {"entries": 2841, "bytes": 60817408, "max_ctime_ns": 1763374502000000000}}
```

`ref` and `tag` are stored only when they match
`[A-Za-z0-9][A-Za-z0-9._/-]{0,99}` with no `..`; otherwise they are empty.
`placed_at` is informational and ordering never reads it.

```go
package pluginstore

type Release struct {
	Owner, Repo string // lowercased GitHub names
	Commit      string // 40 lowercase hex
	Ref, Tag    string // what was asked for, and the tag when there is one
}

func (r Release) ID() string // "github.com/<owner>/<repo>/<commit>"

type Condition string // "", "unreadable", "unwritable", "can't be created", "release not placed"

type Store struct {
	Home, WorkspaceRoot string
	LockWait            time.Duration // two minutes
	// Fetch streams the tarball at the commit. The caller wraps
	// FetchClient.FetchTarball; the store never sees a token.
	Fetch func(ctx context.Context, owner, repo, commit string) (io.ReadCloser, error)
}

func Dir(home, workspaceRoot string) string // ~/.niwa/w-<key>/plugins

// Held reports whether r is complete and marked, reading no tree file.
func (s Store) Held(r Release) (bool, Condition)

// Verify walks r's tree with Lstat and compares it with the seal; false
// means r is treated as missing.
func (s Store) Verify(r Release) (bool, error)

// Ensure places r if it is missing or fails Verify, and points link at its
// tree, both under r's lock. link must sit in a real .niwa/marketplaces
// directory of the instance the command runs for. A non-empty Condition
// means the caller falls back.
func (s Store) Ensure(ctx context.Context, r Release, link string) (restored bool, cond Condition, err error)

// Reachable reports whether commit is a tag or branch tip of owner/repo, or
// an ancestor of one by the compare endpoint; Repair fetches nothing else.
type Reachable func(ctx context.Context, owner, repo, commit string) (bool, error)

// ParseLink reads a link target as a release link, whatever home or key
// prefixes it, by its github.com/<owner>/<repo>/<commit>/tree tail.
func ParseLink(target string) (Release, bool)

type RepairResult struct {
	Release   Release
	Instances []string
	Restored  bool
	Err       error
}

// Repair restores every missing or tampered release that an instance in
// instances links to, whose source is in declared and whose commit passes
// reachable. It writes only inside the store.
func (s Store) Repair(ctx context.Context, instances []string, declared map[string]bool, reachable Reachable) []RepairResult

// Sweep deletes held releases nothing links to, keeping the newest of each
// source by commit time; any scan error deletes nothing.
func (s Store) Sweep(ctx context.Context, scan func() ([]string, error)) (removed []Release, err error)
```

```go
package config

// SharePluginReleases is the off switch for the host plugin store. nil (the
// key absent) means on. Workspace-scoped.
SharePluginReleases *bool `toml:"share_plugin_releases,omitempty"`

func (w WorkspaceMeta) SharePluginReleasesEnabled() bool
```

### Data Flow

A provisioning command for instance *I* of a workspace with root *W*:

1. **Gate.** If *W* has no `.niwa/workspace.toml`, skip everything below;
   today's path runs (R14).
2. **Repair, once per command.** `hostdir.InstanceDirs(home, W)` over the
   root and `kept/`; read each `<instance>/.niwa/marketplaces/` entry;
   `ParseLink` each symlink; check each distinct release with `Held` and
   `Verify`; restore missing or tampered ones into the store at their commit
   once `Reachable` passes. Nothing in any instance is written. Report
   restores, refusals and failures through the notice collector. No network
   unless something is missing.
3. **Place for *I* (Step 6.2).** For each github marketplace: pick the ref as
   today, resolve it with `HeadCommit`. If `<I>/.niwa/marketplaces/<name>` is
   already a real directory with a manifest (an instance from before this
   change, or one made with the store off), keep it. If it is a link that
   parses, keep the release it names (R13): `Ensure` it, which restores it if
   Repair could not and rewrites the link to this store's path if it named
   another. Otherwise, with the setting on, `Ensure` the resolved release and
   link it; with it off, fetch into the instance as today.
4. **Fall back.** A non-empty condition from step 3 means a per-instance
   fetch at the same commit into a real directory, and a notice. An existing
   dangling link for *I* whose release can't be restored because the store is
   unusable is replaced by a per-instance copy at the commit it named, so *I*
   stays on its release.
5. **Sweep, once per command, after Step 6.2b.** Candidates (not the newest
   of their source by commit time), then for each: lock, rescan the root and
   `kept/`, delete if unused. Leftover `.tmp/` entries are cleared under
   their release's lock; `.trash/` entries get their write bits back and are
   removed.
6. **Report.** Notices render on every surface `internal/fallbacknotice`
   already reaches: the terminal for create, apply, init and reset;
   dispatch's and watch's stderr; the SessionStart hook's injected context.

`niwa destroy --force` at the root (`DestroyWorkspace`): compute
`Dir(home, root)` and the kept directory while the root still exists; after
the root is removed, remove the plugins directory if `kept/` is absent or
empty (otherwise print its path and leave it), then the key directory if
empty.

### What the git-history store can rely on

The reference-clone and host-mirror work places its own state under the same
rule. From this design it can rely on:

- **Location and directory key.** `~/.niwa/w-<first 32 hex of sha256 of the
  workspace root's canonical path>/`, computed by one function,
  `hostdir.WorkspaceKey`, the definition PR #511 already uses for
  `mirrors/`. Each store takes its own subdirectory (`plugins/`,
  `mirrors/`); nothing lives under the workspace root's `.niwa/` or inside an
  instance. The same root reached through a symlink gets the same key, and
  two roots get two directories. This follows whatever key PR #511 merges,
  and a unit test fails if the two stores ever resolve different key
  directories for one root.
- **Lock discipline.** One lock per item, in `~/.niwa/locks/`, named by
  hashing the workspace key and the item's id with a per-store prefix (`p-`
  here, `m-` in #511), taken with `filelock.WaitFileLock` and a bounded wait.
  Whatever creates a pointer to an item does so under that item's lock, and
  whatever deletes the item rechecks for pointers under the same lock.
- **Missing-item detection and refetch with nobody acting.** A cheap offline
  check runs inside every provisioning command, through `runPipeline`, for
  every item any instance on disk points at, kept ones included, and a
  missing item is fetched again under its lock, into the store only, with the
  result reported. There is no repair command, no flag and no daemon.
- **The in-use check over every instance on disk.** `hostdir.InstanceDirs`
  lists the real directories among the workspace root's children and among
  the entries of `~/.niwa/w-<key>/kept/`, half-made and half-moved instances
  included, symlinked entries skipped. An unreadable root, kept directory or
  instance deletes nothing. The pointer is read from the instance side, so
  no record can drift from disk.
- **The off-setting pattern.** A `[workspace]` boolean named
  `share_<what>`, absent meaning on, that governs new placements only and
  leaves already-linked instances working and repaired; a store that is per
  repo adds the same key under `[repos.<name>]`, as `read_env_example`
  already does.
- **Fallback, and saying so.** When the store can't be used, the create does
  what it did before the store existed, and records a notice with the
  store's path and a named condition through `internal/fallbacknotice`,
  which already reaches the terminal, dispatch's output and the SessionStart
  hook's context.

It can't rely on the plugin store's contents or its keep-the-newest rule,
which exist because a release is immutable and refetchable in full; a mirror
generation is neither, and its replacement and retirement stay with that
work.

## Implementation Approach

Each phase is one commit. Nothing in this scoping work builds the store, the
mirror or reference clones, and nothing here was built or run against a real
host's instances or stores; every figure above comes from the PRD's survey.

### Phase 1: Host directory leaf

`internal/hostdir` with `WorkspaceKey`, `WorkspaceDir`, `EnsurePrivateDir` and
`InstanceDirs`, unit tests for the symlinked root, the trailing slash, a
half-created instance, a kept instance and a half-kept one under `kept/`, a
symlinked child skipped, an unreadable child and an unreadable `kept/`, and
the test that both stores resolve one key directory. This phase depends on
PR #511's first phase (`internal/filelock` with `WaitFileLock`) as it merges;
if this work starts first, it lands that phase as #511 then specifies it,
and if #511 never lands, this phase carries that code itself.

Deliverables:
- `internal/hostdir/hostdir.go`, `hostdir_test.go`

### Phase 2: The setting

`share_plugin_releases` on `WorkspaceMeta`, its accessor, the overlay merge,
and the config reference entry.

Deliverables:
- `internal/config/config.go`, its tests

### Phase 3: Store core

`internal/pluginstore`: layout, name validation, `held.json` with its seal
and commit time, read-only modes, `Held`, `Verify`, `Ensure` with staged
placement under the lock, `ParseLink`. Unit tests: a killed placement leaves
nothing held, a wrapper naming another commit is refused, ten concurrent
`Ensure` calls fetch once, a mark-less directory is replaced, a tree with an
edited, added or chmodded file fails `Verify`, a hostile tag is stored empty,
each of the four conditions is returned for its cause.

Deliverables:
- `internal/pluginstore/{layout,place,link}.go` and tests

### Phase 4: Placement wiring and fallback

`ensureMarketplaceContent` resolves the commit, calls `Ensure`, links, and
falls back with a notice; the plugin-store note kind in
`internal/fallbacknotice` and its rendering on every surface.

Deliverables:
- `internal/workspace/pluginskills.go`, `internal/fallbacknotice/*`

### Phase 5: Repair, sweep and workspace removal

`Repair` with the `Reachable` check (ls-remote match, then bounded compare
calls), `Sweep` ordered by commit time, their once-per-command calls in
`runPipeline`, and the plugins directory removal in `DestroyWorkspace` with
the key computed first and the kept-instance guard.

Deliverables:
- `internal/pluginstore/{repair,sweep}.go`, `internal/workspace/apply.go`,
  `internal/workspace/destroy_workspace.go`

### Phase 6: Functional proof

The feature file and steps below, the fake server additions (a stalled
tarball response, request counts per ref), and a
`test-functional-plugin-release-hold` Makefile target.

Deliverables:
- `test/functional/features/plugin-release-hold.feature`,
  `test/functional/pluginstore_steps_test.go`,
  `test/functional/tarball_fake_server.go`, `Makefile`

### Phase 7: Guide

A section in `docs/guides/repo-marketplaces.md` on where held releases live,
the setting, the fallback notice, and that niwa restores a missing release by
itself. It describes no repair, rebuild, verify or clean command, because
there is none.

Deliverables:
- `docs/guides/repo-marketplaces.md`

### Test strategy

The proof is a godog feature, `test/functional/features/plugin-release-hold.feature`,
whose scenarios are tagged `@critical`, with steps in
`test/functional/pluginstore_steps_test.go`. Each scenario gets the suite's
own `$HOME`, so the store is `$HOME/.niwa/w-*/plugins/` inside the sandbox
and CI's sentinels catch any write to a real home. GitHub is the suite's
`tarballFakeServer` (wired by `NIWA_GITHUB_API_URL`), which already answers
`HeadCommit` and `FetchTarball` per owner, repo and ref and logs every
request, so "downloaded" means a logged tarball request. For the default
release track, the sandbox's git config rewrites `https://github.com/` to the
suite's local git server with `url.<base>.insteadOf`, so `git ls-remote
--tags` reads tags the scenario pushed; the step "release vX.Y.Z of
acme/skills is published" pushes the tag there and registers its tarball and
commit with the fake. Interruption, overlap and a failed placement use the
existing `NIWA_TEST_FAULT` seam with two new labels, `plugin-store-download`
(`hold:`, `truncate-after:`) and `plugin-store-place` (`error:`). The fake
gains a compare endpoint answer per commit, for the fork scenario. Skills are
checked by resolving the delivered links at the instance root, in a repo and
in a session worktree, the same assertion the root-skill and Codex features
use.

The three Evidence scenarios map to scenarios this way:

| Evidence | Scenarios |
|----------|-----------|
| A second create downloads nothing | `A second instance downloads no held release` (outline over `niwa create`, `niwa dispatch` with the fake `claude`, and the SessionStart hook step): one tarball request per release across both creates; the second instance's `.niwa/marketplaces/*` are symlinks into the store with no regular file, and its plugin content is under 1 MiB; skills resolve at root, repo and worktree. Also `Three overlapping creates download each release once`, `An interrupted create leaves its release unheld`, and `A moving ref holds the same files with the store on and off` (`diff -r`). |
| A deleted release is refetched while the older instance still loads skills, with no repair | `A deleted release is restored by <command>` (outline over `niwa apply`, `niwa dispatch`, the hook, and `niwa create` of an instance on a newer release): one new tarball request at the old commit, output names the restored release, the first instance's links resolve with nothing run inside it. Also `A deleted completion mark counts as missing`, `A failed refetch names the release and the next apply restores it` (fake 503, then `truncate-after`), `An instance stays on its release after a newer tag`, `A kept instance's release survives every command` (outline over a kept instance left in place under the root and one moved into `~/.niwa/w-<key>/kept/`; create, apply, dispatch, hook, reset, destroy, each followed by a check that the release is held and the kept instance's links resolve), `A release only a kept instance uses is restored` (the instance under `kept/`), `An unreadable kept directory deletes nothing`, `A symlinked child of the root is neither read nor written`, `A tampered held release is fetched again` (a file edited after `chmod u+w`), `A commit only a fork holds is refused` (the fake serves the tarball, the compare call says diverged, nothing is placed), `A newer unused release survives the restore of an older one`, and `No command repairs the store` (walks `niwa --help` and every listed subcommand's help). |
| Store off, or unreachable, falls back | `With the store off a create holds its own copy` (and the other workspace still links, and the first workspace's older instance still loads skills), `An unusable store falls back and says so` (outline: mode 000 for `unreadable`, mode 555 with an unheld release for `unwritable`, absent with an unwritable parent for `can't be created`, the `plugin-store-place` fault for `release not placed`), `A dispatch shows the fallback notice`, `Three creates on an unwritable store each fall back`, `An unheld release with its source down is reported and nothing is held`, `A single-instance layout writes nothing under ~/.niwa`, and the location scenarios (two roots get two directories; a symlinked root gets the same one). |

How a reviewer reruns them. From the repo root, after `make build-test`:

```
make test-functional-plugin-release-hold
```

or the raw form:

```
NIWA_TEST_BINARY=$PWD/niwa-test NIWA_TEST_PATHS=features/plugin-release-hold.feature go test -v -count=1 -timeout 60m -run TestFeatures ./test/functional/...
```

By hand, against real GitHub and on a scratch home rather than the
reviewer's own (`export HOME=$(mktemp -d)`, with a GitHub token exported):

- **Second create.** Initialise a workspace with a root that declares two
  github marketplaces, one a fork the reviewer controls. Run `niwa create`
  twice with `HTTPS_PROXY` pointing at a logging proxy, and count
  `/tarball/` requests: two on the first create, none on the second. Then
  `find <second instance>/.niwa/marketplaces -type f` prints nothing, `du -sh`
  of it is under 1 MiB, and a session in each instance runs a skill from each
  marketplace. Repeat the second create with `niwa dispatch` and through a
  SessionStart-provisioned session.
- **Deleted release.** Remove one
  `~/.niwa/w-*/plugins/github.com/<owner>/<repo>/<commit>` directory, run
  `niwa apply` in either instance, read the "restored" line, and run the
  skill in the first instance without touching it. Repeat with `niwa
  dispatch`, the hook, and a `niwa create` after pushing a new tag to the
  fork; then delete only `held.json` and run `niwa apply` again. For a kept
  instance, move the first instance into `~/.niwa/w-*/kept/`, push a new tag,
  create and destroy instances on the newer release, and check after each
  command that the old release is still held and the kept instance's links
  resolve.
- **Off and unreachable.** Add `share_plugin_releases = false` under
  `[workspace]` in one workspace, create, and find a real tree in the new
  instance while a second workspace still links. Turn it back on, then
  `chmod 000`, then `chmod 555` with a new tag pushed, then remove the plugins
  directory with its parent made read-only, creating each time and reading
  the notice's path and condition. A full disk can be simulated with a small
  disk image mounted over the plugins directory.

The chmod scenarios assume a non-root user, as CI runs.

## Security Considerations

The store turns a per-instance cache into content every instance of a
workspace reads, and the skills in it are instructions and scripts agents
act on. It also adds a path that deletes directories and a repair that
fetches on the strength of a link found on disk. The design defends against
a malformed or hostile tarball, a write through one instance's link reaching
every instance, a planted or stale link, a symlink planted among the
workspace's instances, a commit that exists only in a fork, and hostile ref
names reaching an agent's context.

- **One instance's write reaching every instance.** Before this design a
  write into a plugin tree stayed in one instance; now every instance of the
  workspace links to the same tree, so an agent session in any of them,
  including a prompt-injected one in a session worktree, could change skills
  for all of them. The held tree is made read-only before it is marked
  (directories 0555, files 0444 or 0555), so a plain write through a link
  fails. A process that changes the modes first and then writes moves the
  ctime of what it touched, which can't be set back from user space; the
  seal check that runs whenever a command links a release and in every
  repair pass then treats the release as missing, moves it aside and fetches
  it again at its commit, and the output says the release was tampered with.
- **Downloaded content.** Tarballs come from the GitHub API exactly as
  today, with the user's token, now fetched by commit rather than by ref. The
  extractor is `github.ExtractSubpath`, which already rejects path traversal
  and absolute names, skips symlink entries, narrows modes and caps the
  decompressed size. The store adds two checks: the tarball's wrapper
  directory must name the commit asked for, and the manifest must exist
  before the mark is written. Pinning by commit means a tag moved upstream
  never changes what an existing instance runs.
- **Commits from a fork.** GitHub serves a tarball for a commit that exists
  only in a fork of a repository, because forks share storage, and the
  wrapper check would pass. A create only fetches commits it resolved from a
  ref of the declared source, so it is not exposed. Repair, which starts
  from a commit found in a link, fetches it only after the commit is shown
  to belong to the source's own history: it equals a tag or branch tip that
  `git ls-remote` of the source lists, or the compare endpoint reports it
  identical to or behind one of them. A fork-only commit is never behind a
  ref of the source, so it is refused and reported. A moving-ref release
  whose commit was later force-pushed off every branch is refused the same
  way; that instance keeps reporting the release until it is reset.
- **The scan and repair stay inside their bounds.** `InstanceDirs` lists the
  workspace root and the kept directory with `Lstat` semantics and skips any
  symlinked entry, so a symlink planted among the instances is neither read
  nor written through. Repair writes only inside the store. The one write
  outside it is the link `Ensure` makes for the instance the command runs
  for, at a path whose `.niwa/marketplaces` directory is checked with
  `Lstat` to be a real directory.
- **Paths niwa writes and deletes.** Every directory the store makes at its
  own levels is created with `EnsurePrivateDir` (0700, owned by the user, a
  symlink at the path refused), and locks open with `O_NOFOLLOW` as
  `WaitFileLock` does. Owner, repo and commit are validated against fixed
  patterns before they become path components. Deletion only ever renames a
  directory the store's own walk found at `github.com/<owner>/<repo>/<commit>`
  (by `Lstat`, never through a link) into `.trash/`, restores write bits on
  the entries inside it, and removes it there.
- **Strings the remote chose.** Tag and ref names come from the remote.
  `held.json` keeps them only when they match a strict ref pattern, notices
  name a tag only when it is a clean `vX.Y.Z`, and any other ref shown
  anywhere is cut to printable ASCII and 100 bytes. The SessionStart hook's
  context receives only validated owner and repo, a short commit, a fixed
  condition word and the store path niwa built.
- **Deletion fails closed.** Any error listing the root or the kept
  directory, or reading an instance there, stops the sweep for that command,
  and the recheck under the release's lock means a concurrent create can't
  link to a release mid-delete. Which release is kept as newest depends on a
  commit time a tampered `held.json` could change, but that choice only
  decides among releases nothing links to; protection of releases in use
  never reads `held.json`.
- **Workspace removal.** `DestroyWorkspace` computes the store path before
  it removes the root, so a root that no longer exists can't resolve to
  another workspace's key or to nothing, and it leaves the store in place
  while kept instances still link into it.
- **Data exposure.** A private marketplace's tree moves from inside the
  instance to `~/.niwa/w-<key>/plugins/`, readable only by the user. Stores
  are per workspace key, so one workspace never links another's releases.
  `held.json` records the source and ref, never a URL with credentials or the
  token. Removing the workspace through niwa removes its releases; a root
  removed by hand leaves them, which the guide says.

Residual risk accepted: a process running as the user that rewrites the tree
and then rewrites `held.json` with a new seal defeats the check, and its
change reaches every instance linked to the release until the release is
deleted. That takes deliberate work against niwa's own files rather than a
write through a link, and the same process could already rewrite every
instance's per-instance tree one at a time. Damage that keeps every entry's
size, count and ctime (bit rot below the filesystem) is the PRD's stated
limit for damage in place.

## Consequences

### Positive

- From the second instance on, a create downloads and writes no release the
  workspace already holds, and a burst downloads each release once.
- A missing, unmarked or tampered release is restored by the next
  provisioning command with nobody acting, for live and kept instances
  alike, and a moved instance is relinked by its own next command.
- A write through one instance's link can't change another instance's
  skills: the tree is read-only and sealed.
- Moving refs are held like tags, and an instance stays on exactly its
  commit for life.
- The host gets one layout for niwa-owned state, shared with the mirror
  work, and the git-history store gets a written contract.

### Negative

- Every create makes one commit-resolve request per github marketplace even
  when everything is held, and an offline create can't use held releases
  because it can't learn the current commit.
- Every provisioning command scans each instance's marketplace links, a few
  hundred `readlink` calls on a large host, and walks the metadata of each
  release in use, an unmeasured cost estimated in milliseconds.
- Between a release going missing and the next provisioning command,
  instances linked to it lack that marketplace's skills.
- A stuck lock holder costs a create two minutes before it falls back.
- An instance moved anywhere other than the root or the kept directory
  stops counting as a user, so its release can be swept.
- A moving-ref release whose commit was force-pushed off every branch can't
  be restored by repair.
- A workspace removed while kept instances remain leaves its store behind,
  as does a workspace root removed by hand.
- A tree rewritten together with its seal, or damaged without changing any
  entry's size, count or ctime, is not detected.

### Mitigations

- The resolve request is about 40 bytes and replaces no download; offline
  behaviour is today's (R15), and a later change could fall back to the
  newest held release of the source.
- The scan reads only `.niwa/marketplaces/` entries and runs once per command,
  not per instance; the walk reads no file contents.
- The refused restore names the release and the source, and `niwa reset`
  gives that instance a fresh release.
- A later feature that keeps instances somewhere new adds that place to
  `hostdir.InstanceDirs`, which the git-history contract states.
- The missing window is the one the PRD accepted in ruling out a background
  checker; every provisioning command closes it.
- The two-minute wait ends in the ordinary fallback, with a notice, so the
  create still succeeds.
- A moved-back instance's link is repaired by refetch, because the link
  names the release by commit.
- `niwa destroy --force` at the root removes the store once no kept instance
  remains and prints its path when one does; the guide names the directory
  for a root removed by hand.
- Deleting a damaged release directory, or only its `held.json`, puts it back
  under automatic repair, and the guide says so.
