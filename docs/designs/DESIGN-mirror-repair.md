---
schema: design/v1
status: Accepted
problem: |
  niwa will let instances borrow git objects from one bare mirror per repo
  on the host. A borrower keeps none of what it borrows, so a deleted or
  damaged mirror breaks every borrower late and partially, and nothing in
  niwa would notice or fix it. Reference clones wait until niwa can be shown
  restoring the mirror by itself, with no repair a person runs by hand.
decision: |
  Each mirror sits at a stable path under ~/.niwa/<workspace key>/mirrors/,
  a symlink to a store directory, and borrowers' alternates name that path.
  create, apply, dispatch, the SessionStart hook and worktree create run an
  offline check against a pack manifest; a broken mirror is rebuilt from the
  remote under a per-mirror lock, intact packs salvaged, and swapped in with
  one atomic rename. The mirror never prunes. A local branch on commits the
  remote deleted survives when the remote still serves them by id;
  otherwise niwa names it unrecoverable, a stated limit.
rationale: |
  A stat and a 20-byte read per pack keep a healthy check offline and well
  under 100 ms; the symlink swap is the one atomic way to replace a
  directory, so no borrower sees a half-built mirror and none is touched.
  No-prune refresh plus reflogs keeps dropped commits while the mirror
  exists. Copying objects into borrowers or hooking their ref updates would
  break the rule that niwa changes nothing in an instance but its alternates.
upstream: docs/prds/PRD-mirror-repair.md
decision_provenance: inline-resolved
---

# DESIGN: mirror-repair

## Status

Accepted

## Context and Problem Statement

niwa is going to save disk on hosts with many instances by keeping one bare
mirror per repo on the host and letting each instance's clone borrow objects
from it through git's alternates file (`.git/objects/info/alternates`). A
borrower keeps none of what it borrows. This design settles how niwa keeps
that mirror usable without anyone looking after it: how it notices a mirror
that is gone or damaged, how it restores one, how it keeps commits the remote
has dropped, and how a test shows all of it. Nothing here builds the
borrowing create itself; that's the reference-clone work, which waits on this
design and is the main reader of its contract.

### What breaks, measured

A deleted or damaged mirror doesn't fail cleanly. On git 2.39 (macOS, APFS),
with the mirror deleted, `git status` and a no-op `git fetch` in a borrower
still succeed with a warning; `git log` dies at the first commit whose
objects lived only in the mirror, deep inside whatever a person or agent was
doing. Other measured behaviour: a reference clone of an up-to-date mirror
holds no objects of its own, and later fetches write only to the borrower.
`git gc` in a borrower leaves borrowed objects alone, while `git repack -a
-d` silently copies them all in. A mirror that ran a pruning fetch and then
`gc --prune=now` corrupted a borrower whose branch sat on a dropped commit.
A fetch into the mirror is safe while borrowers read from it.

### Where the work lands in niwa today

niwa has no host mirror, alternates or `--reference` code. Its one bare clone,
the per-workspace marketplace copy under the workspace root's
`.niwa/marketplaces/`, is fetched with `--prune`, which rules it out. Host
state under the user's home today is `~/.niwa/locks` and `~/.niwa/env`. A
config refresh replaces the workspace root's `.niwa/` wholesale, so host state
with an identity of its own can't live there.

`niwa create`, `niwa apply`, `niwa dispatch` and the SessionStart hook (`niwa
instance from-hook`) all go through the applier's create or apply pipeline,
which clones missing repos with plain `git clone` from a pool of workers and
updates existing ones with `fetch` plus `pull --ff-only`. The functional suite
(godog features under `test/functional/features/`) gives each scenario its own
`$HOME` and a local git server of bare repos on `file://` URLs. CI runs it on
Ubuntu and macOS with the real home and XDG directories pointed at sentinels,
failing the job if anything is written there.

### The technical problem

Four things have to hold at once, and they pull against each other:

1. **Detection is cheap and offline.** A healthy mirror is confirmed with no
   network call in well under a tenth of a second, yet the check catches all
   five listed faults, including a pack overwritten in place, which a
   directory listing can't see.
2. **Repair is atomic and serialized.** Borrowers read the mirror at any
   moment, so no half-built store may sit in its place, two commands that find
   the same fault produce one sound mirror, and a failed restore leaves things
   no worse than before.
3. **Retention outlives the remote.** A mirror that follows the remote
   faithfully drops a deleted branch's commits, which a borrower's local
   branch may still need.
4. **The case a rebuild can't fix is decided.** A mirror rebuilt from the
   remote holds only what the remote still has, so a local branch on pushed
   commits the remote deleted, followed by the mirror's loss, either survives
   because something keeps its objects or is a stated limit with its reason.

## Decision Drivers

From the requirements:

- **Nobody acts (R1):** the check runs inside create, apply, dispatch and
  the SessionStart hook; no daemon, schedule or by-hand repair.
- **Five faults at least (R2):** mirror directory missing; object store
  missing or empty; a pack or index deleted; a pack truncated or
  overwritten; `HEAD` or `config` missing or unreadable.
- **Restore before use, borrowers untouched (R3, R10):** no prompt, no
  re-clone, and nothing in a borrower changes but its alternates entry.
- **Borrowers recover in full (R4):** every ref reads to the root and
  `git fsck` is clean.
- **One restore at a time (R5)** and **failure no worse than nothing (R6):**
  nothing at the mirror's location is removed until a replacement is ready,
  and a command that doesn't need the mirror finishes.
- **Visible afterwards (R7)** in output and niwa's log.
- **Keep what the remote drops (R8)** and **decide the deleted-commits case
  (R9)**, shown by test.
- **Location (R11):** under `~/.niwa/`, keyed by the workspace root's
  canonical path.
- **A rerunnable test (R12, R13)** on Linux and macOS, borrowers made the
  way a reference clone would be.
- **Cheap when healthy (R14):** no network, under 100 ms per mirror.
- **A contract for reference clones (R15).**

Implementation drivers: reuse niwa's lock directory, clone pipeline and test
git server rather than new infrastructure; assume only portable git, with no
reflinks or snapshots; fail open on the command and closed on the data; and
give the next feature one package and one entry point.

## Considered Options

### Decision 1: Detection and restore

Resolved inline.

The check has to confirm a healthy mirror offline and well under 100 ms
(R14), yet see all five of R2's faults, including a pack overwritten in
place. The restore has to replace a whole object store under borrowers that
may be reading it, serialize against a second command that found the same
fault (R5), and leave everything as it was when it can't finish (R6).

Key assumptions:
- git resolves a symlink in an alternates path on Linux and macOS, since it
  opens the path with ordinary file calls.
- If the mirror's state file is lost, the workspace config still names the
  repo's URL.

#### Chosen: a stable symlink over a manifest-checked store, swapped under a per-mirror lock

Each mirror has a stable path, `~/.niwa/<workspace key>/mirrors/<repo-id>`,
a symlink to a store directory beside it. Borrowers' alternates name the
stable path, never the store. A state file beside it records the clone
URL and a manifest of the store's packs (name, size, ctime, trailing
checksum), rewritten after every refresh or restore.

The check reads local files only. The link resolves to a directory, the
object store isn't empty, every manifest pack is there with its `.idx`, each
pack's size, ctime and trailer match, `HEAD` and `config` exist, and one `git
rev-parse --verify --quiet HEAD^{commit}` against the store succeeds, which
runs git's own parser over both. ctime can't be set from user space, so any
in-place write moves it, and a pack whose ctime moved gets a full `git
verify-pack` before the check passes. A deep verify of every pack runs at
most once a day per mirror during refresh, which is where silent bit rot is
caught.

The restore takes the mirror's lock, checks again, clones `--mirror` with an
empty `--template=` into a temporary directory, passing
`-c transfer.fsckObjects=true -c fetch.fsckObjects=true` and both unpack
limits on the command line so the clone itself runs under them, writes the
retention config, salvages the old store, writes the new state file and
manifest to a temporary name in `.meta/`, renames the temporary directory to
its final name, renames a new symlink over the old one, renames the state
file into place, and only then removes the old store. A lock-free check that
lands between the swap and the state rename sees a mismatch, waits for the
lock, and passes on its recheck, so no second restore follows. A failure
before the store's rename removes only the temporary directory. One between
that rename and the swap leaves an unused `.store/` directory; the next
restore, under the lock, salvages from it and removes it by exact name,
never one the link names. Salvage copies only regular files (by `Lstat`,
never through a link) named like a pack and its `.idx` or a loose object, keeps those that
pass `git verify-pack` or hash correctly, and never copies the old `config`,
hooks, `info/`, alternates or `packed-refs`. Before any move or delete, the
link's target must be a relative `.store/<repo-id>-<stamp>-<rand>` naming a
real directory the user owns; one that isn't is fault (1), never followed.
Every git call in a mirror, old store or borrower runs with the fixed
environment Security Considerations lists, since a store's config isn't
trusted after a fault.

The manifest makes truncation and deletion a stat and a short read per pack.
The symlink rename is POSIX's one atomic way to replace a directory, so no
borrower sees a half-built mirror and repair never touches a borrower.

#### Alternatives Considered

**Full `git fsck` or `verify-pack` on every command.** Rejected because it
costs seconds on a large mirror, which breaks R14.

**Re-fetch into the damaged store in place.** Rejected because there's no
atomic boundary: a borrower can read the store mid-repair, and a fetch into
a repo with a corrupt pack can fail or spread the fault.

**Alternates naming the store, with repair rewriting each borrower's entry.**
Rejected because niwa would have to find every borrower, which is the
reference-clone work's job, and borrowers would name a vanished path for a
window.

**Rename the old store aside, then the new one into place.** Rejected
because two renames leave an instant with nothing at the path.

### Decision 2: Retention

Resolved inline.

A mirror that follows the remote faithfully drops a deleted branch's
commits, and a borrower's local branch may still sit on them. R8 asks that
while the mirror exists it never discards a commit because no remote branch
points at it: its refresh never prunes, and niwa never runs a collection in
it that drops unreachable objects.

Key assumptions:
- Refs for deleted remote branches accumulate slowly enough that ref reads
  stay cheap once packed.

#### Chosen: a no-prune refresh with reflogs kept forever

The refresh is `git fetch --no-prune` with the mirror refspec
`+refs/*:refs/*`, so a branch the remote deleted keeps its ref in the mirror.
The mirror's config sets `fetch.prune=false`, `gc.auto=0`,
`maintenance.auto=false`, `gc.pruneExpire=never`,
`core.logAllRefUpdates=always`, `gc.reflogExpire=never` and
`gc.reflogExpireUnreachable=never`, so a force-pushed branch's earlier tip
stays reachable through the reflog. `transfer.fsckObjects=true` and
`fetch.fsckObjects=true` refuse a malformed object from the remote before
every borrower reads it. `transfer.unpackLimit=1` and `fetch.unpackLimit=1`
make every fetch land as a pack, since git otherwise explodes a small fetch
into loose objects the manifest doesn't track and the check can't see go
missing. niwa never runs `gc`, `repack -d`, `prune` or
`maintenance` in a mirror, and every refresh and restore rewrites the whole
config from this fixed set, the remote URL and the refspec, so no other key
survives. Every commit the mirror ever fetched stays reachable, so even a
person's plain `git gc` in the mirror won't drop one.

#### Alternatives Considered

**Git's default two-week prune grace.** Rejected because R8 says never; the
grace only delays the loss.

**A `refs/niwa/kept/*` namespace written on every refresh.** Rejected for
routine retention because no-prune plus reflogs does the same with less code
and no extra refs to list. A restore uses that namespace only, to pin both
commits it salvages from the old store and commits Decision 3 recovers by
id.

**`gc.pruneExpire=never` alone.** Rejected because a person's `git gc`
still drops the objects once the ref is gone, and nothing names them.

### Decision 3: Pushed commits the remote deleted

Delegated to /decision.

A local branch built on pushed commits lives on the mirror's copy of them.
When the remote deletes them (a squash-merged pull request whose branch is
then deleted is the everyday case), retention keeps them while the mirror
exists, but a rebuild holds only what the remote still serves. R9 asks what
happens to that branch, and to the borrower's stale remote-tracking ref,
when the mirror is then lost. R4 wants every ref to read to the root after a
restore; R3 and R10 forbid changing anything in a borrower but alternates.

An experiment on git 2.39.5 set the facts. After the remote pruned the
commits, a rebuild left the local branch and its remote-tracking ref
unreadable and every other ref clean, and a read-only probe found exactly
those two. Over protocol v2, `git fetch origin <sha>` fetched a commit
reachable only from a hidden ref, or deleted but not yet collected; it
failed once the remote expired its reflogs and pruned.

Key assumptions:
- niwa can list every borrower of a mirror by scanning the workspace's
  instances, since a mirror is keyed per workspace.
- Hosted forges serve a by-id fetch for any commit a ref still reaches, pull
  request refs included. Serving commits no ref reaches is untested on any
  hosted forge and claimed only as best effort; if forges refuse more, more
  cases fall inside the limit and nothing else changes.
- Adding object files inside a borrower counts as a change under R3.

#### Chosen: stated limit, narrowed by recovering missing commits by id from the remote

The branch survives whenever the remote still serves its base commits from
anywhere, and the restored mirror keeps them. Otherwise it's a stated limit,
reported ref by ref, with nothing in the borrower touched.

After the link swap, still under the lock, niwa probes each borrower
read-only: `git rev-list --objects <ref> --not <mirror tips>` over its
branches, remote-tracking refs, tags and `HEAD`, so the walk covers only what
the mirror doesn't. For each ref that fails, a `cat-file` parent walk finds
the first missing commit. niwa checks each id is full-length hex, so none
reads as an option, and fetches them from the repo's own remote into the
mirror over protocol v2, deduplicated, within at most two rounds, 32 ids,
60 s a fetch and 120 s in all, one id at a time if a batch fails, and pins
each as `refs/niwa/kept/<sha>`, the namespace that also holds commits
salvaged from the old store. A second probe alone decides each ref's
fate. A ref still failing is unrecoverable; that's the limit, not a failed
restore, so the restore counts as done and doesn't retry.

This cuts both ways. A commit the remote removed, one holding a secret
included, but still serves by id comes back into the mirror and is pinned
whenever a borrower's ref still reaches it.

The report names instance, repo, mirror, ref and missing commit with the
reason: built on commits pushed and later deleted from the remote; the mirror
that kept them was lost and the remote no longer serves them. For a local
branch it lists its unpushed commits, says their changes are intact, and
names the files they inherit from the lost base, with whether the working
tree still holds each. A stale remote-tracking ref is reported at lower
severity; the user's `git fetch --prune` clears it.

R3 and R10 hold as written, since the only writes go into the mirror, and
more cases survive than a plain rebuild allows: squash-merged and
force-pushed pull requests, unpruned deleted branches, hidden refs.

#### Alternatives Considered

**Plain stated limit.** The same probe and report, no fetch. Rejected
because it gives up commits the remote still serves to avoid a step that runs
only during a restore and degrades to this option on failure.

**Localize into the borrower.** When the remote drops a ref, write a pack of
the dropped commits into each borrower that reaches them (about 40 ms
measured). Rejected because it breaks R3's "only alternates changes" and the
criteria of R10, R11 and R13, leaves a window before the next refresh, and
copies objects into many borrowers, spending the disk the mirror saves.

**A reference-transaction hook in borrowers.** Rejected because installing
it is a file change in the borrower, `core.hooksPath` bypasses it, and it
fires when the borrower moves a ref, not when the remote deletes commits.

**Refs for borrower tips in the mirror, or a second keep store.** Rejected
because refs in the store die with it, and a second store shares the failure
domain (same disk, same `~/.niwa`, same person freeing space) while doubling
what repair looks after.

### Decision 4: Test setup

Resolved inline.

R12 wants scenarios that build their own remote, mirror and two borrowers in
the sandbox home, and R13 wants borrowers built the way a reference clone
would be, since that create doesn't exist yet.

Key assumptions:
- niwa finds a mirror from a borrower's alternates entry and resolves its URL
  from the workspace config, so a mirror can be built on first use.
- A duration niwa logs is a fair measure of the check; process start-up
  isn't counted.

#### Chosen: one feature file over the suite's sandbox, borrowers converted from niwa's own clones

`test/functional/features/mirror-repair.feature`, tagged `@mirror-repair`,
with steps in `test/functional/mirror_steps_test.go`, on the per-scenario
`$HOME` and `localGitServer`. The background runs `niwa create` twice and a
step writes each clone's alternates to name the mirror's stable path. The
next command finds borrowers naming a missing mirror, which is fault (1), and
builds it. A step then runs `git repack -a -d -l` and `git prune-packed` in
each borrower and asserts it holds none of the mirror's objects. Only niwa
writes the mirror, its state file and manifest.

Faults are file operations. The commands are `niwa create` of a third
instance, `niwa apply` at the root, `niwa dispatch` with the fake `claude`,
and the existing SessionStart hook step. Timing reads the check's duration
from the mirror log. All of it sits in the sandbox, under CI's sentinels.

#### Alternatives Considered

**Go unit tests only.** Rejected because they don't run the compiled binary
through create, apply, dispatch and the hook, as R12 asks.

**The test writes the mirror and manifest.** Rejected because it couples the
test to private formats and skips the code that writes them.

**A hidden `niwa mirror` command to seed the mirror.** Rejected because a
person could mistake it for the by-hand repair the requirements rule out.

**The `the command finished within N seconds` step.** Rejected because
whole-process time at one-second resolution can't show 100 ms.

## Decision Outcome

niwa keeps a mirror usable from inside the commands it already runs. Each
mirror sits behind a stable symlink that borrowers name in their alternates,
over a store whose packs a manifest records. Before `niwa create`, `niwa
apply`, `niwa dispatch` or the SessionStart hook clones or syncs anything,
the check compares the store with the manifest, offline. A broken mirror is
rebuilt beside the old one from the remote, topped up with every intact pack
the old store holds, and put in place by renaming a new link over the old, so
no borrower is re-cloned or edited. One lock per mirror serializes this; a
failure leaves the old link and store in place, and the next command retries.

Retention keeps what the remote drops while the mirror exists. When the
mirror is then lost, a borrower's local branch on pushed commits the remote
later deleted survives if the remote still serves those commits from
anywhere (a hidden pull request ref, an unpruned object): niwa probes the
borrowers read-only, fetches the missing commits by id into the mirror and
pins them. If the remote has really destroyed them, it's a stated limit:
niwa names the branch unrecoverable, lists its unpushed commits and the files
they inherited from the lost base, and leaves the borrower alone. The reason
is that a mirror rebuilt from the remote holds only what the remote still
has, and a second copy would mean writing into borrowers, which R3 and R10
forbid and which spends the disk the mirror saves. The loss takes two
independent events, the remote destroying the commits and then the whole
store going; recovery by id narrows it and salvage covers partial damage. R4
holds with that exception, and `git fsck` there also flags reflog entries
naming the lost commits.

An instance's unpushed work is never at risk, because its objects stay in
that instance: borrowers write new objects to their own store, never the
mirror, and nothing here deletes or rewrites anything inside an instance. The
one edge is an unpushed commit on a lost base, which keeps its own changes
but not what it inherits from the base, and the report names it.

## Solution Architecture

### Overview

A new package, `internal/mirror`, owns the layout, state file, check,
restore, refresh, probe and log. Its entry point, `EnsureHealthy`, is called
by the create and apply pipeline, `niwa worktree create` and the
SessionStart hook. A second one, `Seed`, builds a repo's first mirror under
the same lock and fixed config, with nothing to salvage; this feature owns
it, and the borrowing create calls it. A new waiting lock in
`internal/workspace` serializes work on each mirror. Until the
reference-clone work lands no instance borrows, so `EnsureHealthy` finds
nothing to do; the functional scenarios exercise it with borrowers they
build.

### Components

**`internal/mirror`**: layout helpers, state file and manifest, the fault
checks, restore with salvage, the refresh and its daily deep verify, the
borrower probe and recovery by id, and the log writer. It shells out to git.

**`WaitFileLock`** in `internal/workspace/file_lock_unix.go` polls the same
`flock` as `TryFileLock` until a caller-chosen timeout. Today's only exported
lock, `TryFileLock`, never waits, and the waiting `acquireFileLock` is
unexported with a fixed 30 s limit. It opens the lock file with
`O_NOFOLLOW` and mode 0600 and refuses anything but a regular file the user
owns. Without `flock` it returns `ErrLockUnsupported`, and a restore is
skipped with a warning.

**Call sites.**
- `runPipeline` in `internal/workspace/apply.go`, after the effective config
  is final and just before the group directories and clone pool. `Create`
  and `Apply` both reach it, so `niwa create`, `niwa apply`, `niwa dispatch`
  and `niwa instance from-hook` provisioning a new instance all run the
  check, with the classified repos, URLs from `RepoCloneURL` and refresh on.
  A root-level apply reaches it once per instance; later checks in the same
  command find the mirror healthy.
- `runInstanceHookStart` in `internal/cli/instance_from_hook.go`, on the paths
  that resolve a workspace root but don't provision (a resumed dispatch
  session, a session the guard declines). These check every mirror recorded
  under the workspace key with no refresh, loading the config only for a
  broken mirror, to get its URL.
- `niwa worktree create`, before checkout, for its repo, since a worktree
  reads its instance clone's objects.

**Reporting.** Results go to the command's `Reporter` as warnings on stderr.
Every string the remote chose (ref names, file names) is cut to printable
ASCII, capped at 256 bytes and escaped the same way for those lines as for
the log, so a hostile file name can't write escape sequences to a terminal.
Claude Code discards the SessionStart hook's stderr, so from-hook carries the
results out of `provisionInstance` and puts each result's notice in the
`additionalContext` JSON it prints on stdout. On the paths that print
nothing today, it prints that JSON only when there's a notice. The notice is
a fixed template (mirror, fault, result, a count of unrecoverable refs and
the mirror log's path) with no name the remote chose; full detail goes to
the log. The hook waits 10 seconds
for a lock, then reports `busy`.

**Mirror log.** niwa has no log file today. This design adds one: JSON lines
at `~/.niwa/<workspace key>/mirrors/.meta/mirror.log`, one line per check and
per restore, refresh or unrecoverable ref, each naming the mirror, the fault,
the result and the check's duration. Strings are cut to 256 bytes, so each
line stays one `O_APPEND` write and a ref name can't forge one. The file
opens with `O_NOFOLLOW`, mode 0600, and at 1 MiB rotates to `mirror.log.1`.
A write failure is a warning.

### Key Interfaces

```
~/.niwa/
  locks/m-<sha256(workspace key + "/" + repo-id)[:32]>.lock   0600
  empty-hooks/                                      core.hooksPath for mirror git calls, 0700
  w-<sha256(CanonicalPath(workspace root))[:32]>/   the workspace key, 0700
    mirrors/
      <repo-id>        symlink -> .store/<repo-id>-<UTC stamp>-<rand>
      .meta/<repo-id>.json                   state file, 0600
      .meta/mirror.log, mirror.log.1         0600
      .store/<repo-id>-<UTC stamp>-<rand>/   the store, a bare repo
      .tmp/<repo-id>-<rand>/                 a restore in progress
      .link-<rand>                           a new link before its rename
```

Modes are set explicitly, not through the umask, and tightened at each check
if they've widened. The repo id is the repo's name as the workspace config
keys it and must match `[A-Za-z0-9][A-Za-z0-9._-]{0,99}`; a repo whose name
doesn't clones in full. No id starts with a dot, so none can take niwa's own
names (`.meta`, `.store`, `.tmp`, `.link-*`). The lock name hashes the
workspace key (the `w-<hash>` directory name) and repo id rather than the
canonical stable path, because
`CanonicalPath` resolves symlinks and would give each store its own lock. A
borrower's alternates line is `<stable path>/objects`.

```go
package mirror

func WorkspaceKey(workspaceRoot string) string
func StablePath(home, workspaceRoot, repoID string) string
func LockPath(home, workspaceRoot, repoID string) string
type Repo struct{ ID, URL string } // URL from the workspace config
type Options struct {
	Home, WorkspaceRoot string
	Command  string        // logged: create, apply, dispatch, from-hook, worktree
	Refresh  bool          // fetch a healthy mirror (create and apply)
	LockWait time.Duration // default 5 minutes; 10 seconds in the hook
}
type Fault string   // "", missing, objects-empty, pack-missing, pack-changed, repo-unreadable, url-changed
type Outcome string // healthy, restored, restore-failed, busy
type Result struct {
	RepoID, StablePath string
	Fault              Fault
	Outcome            Outcome
	Err                error
	CheckDuration      time.Duration
	Unrecoverable      []LostRef
}
func (r Result) Usable() bool   // healthy or restored
func (r Result) Notice() string // fixed template, counts and log path only; "" when healthy
type LostRef struct {
	Instance, Repo, Ref, MissingCommit string
	RemoteTracking                     bool
	OwnCommits, LostFiles              []string
}

// Mirrored keeps candidates with a state file, a link, a .store/<repo-id>-*
// directory, or a borrower whose alternates name the stable path.
func Mirrored(home, workspaceRoot string, candidates []Repo) []Repo
// Recorded returns the repos with a state file, a link or a store under the
// workspace key, so a mirror whose state file is lost is still found.
func Recorded(home, workspaceRoot string) []Repo
// EnsureHealthy checks each mirror and restores any that's broken. Nothing
// it returns should fail the caller's command.
func EnsureHealthy(ctx context.Context, opts Options, repos []Repo) []Result
// Seed builds the first mirror for a repo that has none, under the same
// lock and fixed config, and returns what EnsureHealthy would.
func Seed(ctx context.Context, opts Options, repo Repo) Result
```

```go
package workspace

// WaitFileLock polls until the lock is free, ctx is done or timeout passes;
// on timeout it returns ErrLockHeld wrapped with the holder line.
func WaitFileLock(ctx context.Context, path, holder string, timeout time.Duration) (release func(), err error)
```

The state file, written to a temporary name and renamed into place
(`trailer` is the pack's full trailing checksum, shortened here):

```json
{"schema": 1, "repo_id": "niwa", "url": "https://github.com/tsukumogami/niwa.git",
 "store": ".store/niwa-20261116T101500Z-3f9a",
 "last_refresh": "2026-11-16T10:15:02Z", "last_deep_verify": "2026-11-16T10:15:02Z",
 "packs": [{"name": "pack-5c1e0b.pack", "size": 48213377,
            "ctime_ns": 1763288102000000000, "trailer": "9f0c2d7e"}]}
```

A restore clones from the workspace config's URL; the state file's `url` is
a cross-check, never followed. Userinfo, query string and fragment are
stripped before a URL is written, and the comparison uses the same stripped
form. A mismatch is fault `url-changed`: niwa reports it and rebuilds the
mirror from the config's URL with no salvage and no relink, rather than
fetching the new remote into the old store, and the probe then reports
what the new remote doesn't serve. A workspace recreated at the same path
gets the same key and the same treatment, repo by repo. A repo the config
no longer names isn't restored. `store` is validated like the link's target.

A log line; `event` is `check`, `restore`, `refresh` or `unrecoverable`, and
`result` is `healthy`, `restored`, `restore-failed` or `busy`:

```json
{"time":"2026-11-16T10:15:02Z","command":"apply","mirror":"niwa","event":"restore","fault":"pack-changed","result":"restored","check_ms":6,"took_ms":4210}
```

### Data Flow

1. **Find.** `Mirrored` keeps the call site's repos that have a state file,
   a link, a store, or a borrower (one stat and short read of each instance
   clone's alternates file). On today's hosts nothing is kept and the call
   returns.
2. **Check.** Lock-free, per mirror, as Decision 1 describes; a moved ctime
   triggers `verify-pack` on that pack only, and packs the manifest doesn't
   list are ignored. A lost state file counts as fault (1). The duration
   goes to the log.
3. **Refresh,** when healthy and asked. Under `TryFileLock` (busy means
   skip): rewrite the config, `git fetch --no-prune`, a deep verify if the
   last is a day old, a new manifest. Failure is a warning.
4. **Restore,** when broken. `WaitFileLock` with `LockWait`; a timeout is
   `busy` and touches nothing. Under the lock: check again; clear leftover
   `.tmp/` directories by exact name, never through a link; if only the link
   is gone and the recorded store passes validation and the check, relink
   with no network. Otherwise check free space against the old store's pack
   sizes (a 1 GiB floor when none are known) and report `restore-failed`
   without cloning if it's short, then run Decision 1's sequence, salvaging
   from every `.store/<repo-id>-*` directory and pinning the old store's
   readable refs the new clone lacks as `refs/niwa/kept/<sha>`. The link is
   made as `.link-<rand>` exclusively, renamed over, directory synced. Old
   stores are removed by exact name, never one the link or the new state
   file names. A real directory at the stable path is moved into `.store/`
   and salvaged, never deleted.
5. **Probe and recover,** still under the lock, as Decision 3 describes.
6. **Report.** Log first, then a `Reporter` warning naming mirror, fault and
   result, or a line in the hook's context. A create that would borrow does
   so only when `Usable()` is true; every other command carries on whatever
   the result.

### Contract for reference clones

What the reference-clone work can rely on, as R15 asks:

- **Where.** `~/.niwa/w-<first 32 hex of sha256 of the workspace root's
  canonical path>/mirrors/<repo-id>`, a path that stays the same across
  restores and is a symlink to the current store.
- **The call.** `mirror.EnsureHealthy`; `Result.Usable()` says whether a
  create may borrow.
- **First build.** `mirror.Seed` builds a repo's first mirror under the same
  lock and fixed config, with nothing to salvage. This feature owns it; the
  borrowing create calls it for a repo that has no mirror yet.
- **The guarantees.** A broken mirror is restored before the command depends
  on it, with no prompt and no re-clone (R3). Afterwards every ref in every
  borrower reads to the root and `git fsck` is clean, except refs niwa
  reports unrecoverable (R4). Two commands yield one sound mirror and no
  borrower sees a half-built one (R5). A restore that can't finish removes
  nothing at the mirror's location, leaves borrowers alone, says so, retries
  next command, and lets a command that doesn't need the mirror complete
  (R6). Each check and restore is in the output and the mirror log (R7). The
  mirror never prunes and niwa never collects in it (R8). A branch on deleted
  pushed commits survives the mirror's loss when the remote still serves
  them, and is otherwise named as unrecoverable (R9). Nothing inside an
  instance is written by this feature (R10).
- **Fallback.** A new create clones in full, as today, when `Usable()` is
  false.
- **Alternates.** The borrowing create writes `<stable path>/objects` into
  alternates itself. `git clone --reference` through a symlink records the
  resolved store path, which a later restore removes.
- **Not here.** Replacing a mirror by generation, finding every borrower
  before an old generation is removed, the make-independent command and the
  setting that turns borrowing off stay with the reference-clone work.

### Proofs and how a reviewer runs them

| Proof | Scenarios in `test/functional/features/mirror-repair.feature` | Command |
|-------|---------------------------------------------------------------|---------|
| A, deleted mirror | `A deleted mirror is restored by <command>` (outline: create, apply, dispatch, SessionStart hook) | `make test-functional-mirror-repair` |
| B, damaged mirror | `A damaged mirror is restored by an ordinary command` (outline: empty object store, deleted pack, pack truncated to half, deleted HEAD) | same |
| C, deleted remote commits | `A branch on deleted remote commits survives while the remote serves them`; `A branch on commits the remote destroyed is reported unrecoverable` | same |

The raw form, from the repo root, after `make build-test`:

```
NIWA_TEST_BINARY=$PWD/niwa-test NIWA_TEST_PATHS=features/mirror-repair.feature go test -v -count=1 -timeout 60m -run TestFeatures ./test/functional/...
```

Deleting the mirror removes the stable path and the store behind it. For
survival the remote keeps the deleted branch's tip under a hidden ref, like
a pull request ref; for the limit it also expires its reflogs and runs `gc
--prune=now`, or stock git would still serve the commit. CI runs the feature
in its `make test-functional` job on Ubuntu and macOS under the sentinels.

Every PRD acceptance criterion maps to a named scenario in the feature file,
or to a step every scenario runs (borrowers unchanged but for alternates,
the unpushed commit still reachable, nothing mirror-related outside the
sandbox `~/.niwa/`). Beyond the three proofs above, the scenarios are: `The
refresh keeps commits the remote deleted`; `Two commands restore one broken
mirror once`; `A failed restore changes nothing and the next command
retries`; `A healthy check needs no network and stays under 100 ms`.

## Implementation Approach

Each phase is one commit. Nothing here builds reference clones or the shared
plugin store; the borrowing create is the reference-clone work.

1. **Waiting lock.** `WaitFileLock`, its non-unix stub, unit tests.
2. **Layout, state and log.** `internal/mirror` with the path helpers, repo
   id and link target validation, the 0700 and 0600 modes, the fixed git
   environment every later phase calls git through, the state file's atomic
   write, and the JSON-lines log with rotation.
3. **Check.** `Mirrored`, `Recorded` and the fault checks, with unit tests
   for each of the five faults and one timing the healthy path.
4. **Restore, seed and refresh,** with tests that fail each step and find the old
   link and store intact, and that plant a symlink, a hostile config and an
   outside link target and see none followed.
5. **Probe and recovery.** Probe, bounded fetch by id, pins, `LostRef`.
6. **Call sites.** `runPipeline`, `niwa worktree create`, `Reporter` warnings.
7. **Hook notice.** Results into `additionalContext` through the fixed
   template, and the check on the hook's non-provisioning paths.
8. **Proof.** The feature file, `mirror_steps_test.go` and the
   `test-functional-mirror-repair` Makefile target.

## Security Considerations

Every instance reads the mirror, so the risks are a bad object or path
reaching many borrowers and data staying on the host longer than expected.
The design defends against a hostile remote, a damaged store and a malformed
repository, not against a process running as the same user, which can
already edit borrowers, or the mirror, manifest and state file together. The
manifest guards against accidents and isn't tamper evidence; restored
content has the same trust as a fresh clone.

- **Paths and files.** niwa follows, moves or removes only a validated
  `.store/` target, and repo ids can't take niwa's dot-prefixed names.
  Directories are 0700 and files 0600; locks and the log open without
  following links, and log lines are JSON-encoded and capped.
- **Salvage** copies only regular, verified pack and loose-object files.
  Verification checks each file's own checksums and object hashes; it isn't
  a full `fsck`, so a well-formed but malformed object in an old pack is
  carried over as it was, with the trust it had before the damage.
- **Git invocation.** Every call starts from the parent's environment with
  every inherited `GIT_*` variable removed (`GIT_DIR`, `GIT_EXEC_PATH` and
  `GIT_ALTERNATE_OBJECT_DIRECTORIES` among them), then adds only the ones
  niwa sets. Every call sets `core.fsmonitor=false`, `core.hooksPath` to
  `~/.niwa/empty-hooks/` (a 0700 directory niwa owns and checks is empty,
  never a shared temp directory), `protocol.ext.allow=never` and
  `GIT_TERMINAL_PROMPT=0`. Local reads also set `GIT_CONFIG_NOSYSTEM=1`,
  `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_OPTIONAL_LOCKS=0` and empty credential
  and ssh helpers. Network calls keep the user's own config for credentials,
  as today's clones do, and run only after niwa rewrote the mirror's config
  from its fixed set, so no key a tampered store set survives.
- **Remote input.** The URL comes from the workspace config, never the state
  file, and is stored with its userinfo, query string and fragment
  stripped, so a token in any of them never reaches disk. A changed URL
  rebuilds the mirror rather than mixing two remotes in one store. Fetch by
  id sends only validated ids, only to the repo's own remote.
  `fsckObjects` refuses malformed objects, including during the restore's
  clone, which gets it with `-c` because the new store has no config yet.
- **Terminal and agent context.** Remote-chosen text (ref and file names)
  reaches the terminal only cut to printable ASCII, capped and escaped, as
  in the log. The SessionStart notice carries no such text at all: counts
  and the log's path, so nothing the remote chose reaches the model's
  context through it.
- **Lock waits and disk.** A command waits for another's restore (five
  minutes, ten seconds in the hook) rather than racing it. A stuck holder
  costs that wait, then the command reports the mirror busy and carries on;
  it's an availability trade made for R5's one-restore-at-a-time rule. A
  restore checks free space before cloning and reports instead of filling
  the disk.

The mirror never prunes, so it keeps every commit it fetched until the mirror
directory is deleted, including one the remote later removed because it held
a secret. A person purges it by deleting the repo's stable path and its
`.store/<repo-id>-*` directories, or all of `~/.niwa/w-<key>/mirrors/`; with
no old store to salvage, the next command rebuilds from what the remote
still serves. After a purge, a removed commit can come back: if the remote
still serves it by id and a borrower's ref still reaches it, the recovery
by id fetches and pins it again. To keep it out, purge the mirror and ask
the forge to drop the commit; refs on commits the remote no longer serves
are reported unrecoverable.

**Residual risk for the owner.** The mirror keeps commits a remote removed,
secrets included, until someone deletes it by hand; nothing in niwa expires
them, because R8 forbids it. The workspace owner accepts that as a standing
property of the host with this design. Whether a repo can opt out of
mirroring is left to the reference-clone work's setting that turns borrowing
off.

## Consequences

### Positive

- A deleted or damaged mirror is restored by the next ordinary command with
  nobody acting, and borrowers recover untouched, since their path is fixed.
- The healthy path is a few stats, short reads and one git call, offline.
- Salvage means partial damage loses nothing retention kept.
- A branch on deleted pushed commits survives more often than a plain
  rebuild allows, and when it doesn't, the report names ref, commits, files.
- The reference-clone work gets one call and a written contract.

### Negative

- Detection runs only when niwa runs; a mirror deleted between commands
  leaves borrowers broken until the next one. Mitigation: that's the window
  the workspace owner accepted in ruling out a daemon, and every command R1
  names closes it.
- Fetching by id commits no ref reaches is best effort on hosted forges, and
  a branch on commits the remote destroyed is lost with the mirror.
  Mitigation: the fetch is bounded and degrades to the plain limit, reported
  by name; each `unrecoverable` log line is the cue to revisit localizing.
- The ctime check doesn't see silent bit rot; only the daily deep verify
  does, so a rotted pack can go unnoticed for up to a day. Mitigation: a
  pack the daily verify finds bad is restored and salvaged like any fault.
- Refs for deleted branches and `refs/niwa/kept/` pins accumulate in the
  mirror and are never cleaned. Mitigation: refs stay packed, so reads stay
  cheap; trimming them would give up R8.
- The post-restore probe's cost scales with the number of borrowers, and the
  restore holds the lock through it and the fetch budget. Mitigation: the
  probe walks only what the mirror lacks, only after a restore, and a waiter
  gives up after its `LockWait` and carries on, touching nothing.
- A restore inside the SessionStart hook delays the session by a clone.
  Mitigation: the hook waits at most 10 seconds for a busy lock, and a
  healthy mirror costs it only the offline check.
- Every commit the mirror fetched, a since-removed secret included, stays
  until a person deletes the mirror, and the recovery by id can bring one
  back while the remote serves it. Mitigation: the purge and forge request
  Security Considerations describes.
- A remote failing git's fsck can't be mirrored, and a restore needs disk
  for a second store. Mitigation: either case leaves `Usable()` false, so a
  create clones in full, and the free-space check reports before cloning.
