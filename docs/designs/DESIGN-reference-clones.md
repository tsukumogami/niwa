---
schema: design/v1
status: Planned
problem: |
  Every niwa instance clones each repo in full, so a host with several
  instances of a workspace downloads and writes the same history once per
  instance. niwa already keeps a self-repairing bare mirror per repo on the
  host, but nothing borrows from it, and borrowing needs an off setting, a
  way to make one instance independent, a safe way to retire old mirror
  generations, and three mirror fixes before users can rely on it.
decision: |
  The clone pool clones each repo with `git clone --reference-if-able`
  against the repo's stable mirror path and rewrites the alternates file to
  that one stable-path line, falling back to a full clone with a notice on
  any failure. A `share_git_history` setting at workspace and repo level
  chooses who borrows, and a build-tag constant keeps borrowing off in every
  release build until a last turn-on change. A restore no longer removes old
  stores; it records them, and a sweep removes a retired store only when a
  strict scan of every instance on disk proves no alternates line names it.
  `niwa dissociate` copies borrowed objects in and drops alternates. The
  mirror keeps remote-access variables on network calls, salvages damaged
  packs with a recovering unpack, and compares remotes by host and path.
rationale: |
  `git clone --reference` keeps every full-clone detail git sets up, so the
  only difference is one rewritten file. Gating on a build-tag constant means
  no setting or environment variable can turn borrowing on early, and the
  turn-on is a few deleted lines. Removing stores only after a scan that
  must succeed puts the safety argument in what is on disk rather than in a
  list made earlier or in a timer; a store is lost only through a bug, never
  through a race with a borrower that was missed.
upstream: docs/prds/PRD-reference-clones.md
decision_provenance: inline-resolved
---

# DESIGN: reference-clones

## Status

Planned

## Context and Problem Statement

Every repo an instance holds is a plain `git clone`. `Cloner.CloneWith`
(`internal/workspace/clone.go`) runs `git clone [--depth N] [--branch ref]
<url> <dir>`, the clone pool in `runPipeline` (`internal/workspace/apply.go`)
calls it through `cloneWithRetry`, and every path that provisions an instance
reaches that pool: `niwa create`, `niwa apply` after a repo is added,
`niwa reset`, `niwa dispatch` and the SessionStart hook through
`provisionInstance`, `niwa watch`, `niwa resume`, and `niwa init --bootstrap`.
So a host with several instances of one workspace downloads and writes each
repo's whole history once per instance, though only each instance's branches
and unpushed commits differ.

The mirror-repair feature (PR #511, v0.43.0) already keeps one bare mirror per
repo per workspace at a stable path,
`~/.niwa/w-<key>/mirrors/<repo-id>`, a symlink to the current store
`.store/<repo-id>-<UTC stamp>-<rand>`. `mirror.EnsureHealthy` checks it and
restores it, and `ensureMirrors` calls that once per pipeline, just before
the clone pool. `mirror.Seed` builds a first mirror and has no caller yet.
Nothing borrows: the call finds no borrower and returns. This design adds the
borrowing clone and everything the mirror-repair design left to this work,
which `DESIGN-mirror-repair.md` lists under "Contract for reference clones":
the off setting, the command that makes an instance independent, replacing a
mirror generation without breaking a borrower, and finding every borrower
before an old generation goes.

The technical problem has five parts.

**A borrowing clone that is otherwise a full clone.** A borrower's
`.git/objects/info/alternates` must hold exactly one line, the stable path
followed by `/objects`, never a resolved store path, because a restore swaps
the store behind the link and removes old ones. `git clone --reference`
through the symlink records the resolved store, so the create can't simply
pass the stable path to git and stop. Apart from that file and the objects
it lets the clone omit, the clone has to be what a full clone would be: the
same `origin` URL, refs, local config and hooks (R3). The mirror check, and
any restore, runs first, and the clone borrows only when the result is
usable; when a repo has no mirror the create seeds one first (R1, R2). When
the mirror isn't usable for any reason, or the borrowing clone fails part
way, the create removes the partial clone, clones in full and names the repo
and the reason in its output (R4). The clone-pool workers report through a
no-op Reporter, so that notice has to travel back in `cloneResult`. Only new
clones borrow: sync and apply never convert an existing clone either way
(R5).

**Who borrows.** A workspace setting turns borrowing off, a per-repo setting
turns it off or on for one repo, and the repo's value wins in both directions;
a repo with borrowing off gets a full clone, no alternates entry and no mirror
seeded for it (R6). The retention decision
(`DECISION-mirror-retention-residual-risk-2026-11-16.md`) accepted the
mirror's keeping of removed commits on the condition that this per-repo form
exists. On top of the setting sits the rollout rule: no release build
borrows for any user until the turn-on change lands (R16). Only the
functional suite's test build borrows before then, and setting the option to
on does nothing in a release build. The turn-on makes borrowing the default
wherever a workspace root exists, as its own last change, merged after the
fixes for niwa#524 (R12, with its scenario) and niwa#525 (R13) and the
rerun of the repair proofs against real borrowers (R15) (R17). Gating the
turn-on on niwa#525 is this project's own addition: it is stricter than the
condition the turn-on was required to meet, and it only restricts.

**Every borrower, found every time.** The mirror callers list borrowers with
`EnumerateInstances`, which needs an instance state file, so it misses
instances kept under `~/.niwa/w-<key>/kept/` and half-created ones. R7 asks
that every check, restore, probe and store removal consider every instance on
disk for the workspace, both of those included. R9 asks that a generation (one
store behind a stable path) be replaced only through the mirror-repair
restore or its rebuild for a changed remote, that an old one be removed only
after an enumeration that succeeded, and that no store a clone's alternates
names, directly or through the stable path, ever be removed. A failed
enumeration removes nothing. R10 extends that to destroying a workspace:
`DestroyWorkspace` leaves `mirrors/` alone today, and it must remove the
mirrors when no instance on disk borrows from them, or keep them and say so.

**Leaving the host.** A user command makes a named instance, by default the
current one, independent of the host's stores (R8). It takes the instance's
lock and refuses if another command holds it. It checks that free space
covers the mirror's pack sizes for the instance's repos. Then it copies every
borrowed object into each clone and removes the alternates file only once
that copy is done and `git fsck` passes with the mirror unreachable. A failure
part way leaves every clone still borrowing and readable. It covers worktrees,
skips a clone that doesn't borrow, and reports each clone it changed. No other
niwa command undoes borrowing, and niwa sets no `gc.*` or `maintenance.*` key
in a borrower (R21). A user guide section documents where mirrors live, the
purge steps in their safe order (rotate the secret; rewrite or delete local
branches that reach the commit and `git fetch --prune` in each borrower;
delete the stable path and its stores; ask the forge to drop the commit), why
that order, the off setting at both levels and this command (R11).

**Three mirror defects that matter once real borrowers exist.** `gitEnv`
(`internal/mirror/git.go`) strips every inherited `GIT_*` variable from every
mirror process, network calls included, so a user whose access depends on
`GIT_SSH_COMMAND` gets a mirror that can't seed, refresh or restore (niwa#524).
R12 asks that the seed, refresh, restore and fetch-by-id keep a named list of
remote-access variables: `GIT_SSH`, `GIT_SSH_COMMAND`, `GIT_SSH_VARIANT`,
`GIT_ASKPASS`, `GIT_PROXY_COMMAND`, `GIT_SSL_CAINFO`, `GIT_SSL_CAPATH`,
`GIT_SSL_NO_VERIFY`, `GIT_HTTP_PROXY_AUTHMETHOD` and `GIT_CONFIG_COUNT` with
its numbered key and value entries. Local reads keep stripping everything.
`rebuild` removes old stores before the borrower probe and recovery by id run
(niwa#525). R13 asks that no old store go before those finish, and that a
truncated old pack yield every object still readable, so a borrower ref whose
objects survive reads to the root and one that needs a lost object is named
unrecoverable. `recordURL` keeps the URL's scheme, so the SSH and HTTPS forms
of one repo compare as different remotes and the restore salvages nothing
(niwa#522). R14 asks that a change between those forms, or in the user part
alone, count as the same remote, while a change of host or path still
rebuilds with no salvage.

Behind all of it sit the proofs. The mirror-repair feature file builds its
borrowers by hand, with a step that writes the alternates file. R15 asks that
every scenario in it run against borrowers `niwa create` made, with no such
step, on Linux and macOS, and pass. The non-functional requirements are a
saving a user can see (the second create writes less than two thirds of the
first, and no borrower holds a pack a mirror store holds; R19), Linux and
macOS with the git niwa already requires, no git extension, no reflink, no
daemon, and tests that stay in the suite's sandbox (R20), and several
concurrent first creates producing one mirror per repo with every clone
borrowing from it or falling back (R18).

## Decision Drivers

- **A borrower is indistinguishable from a full clone** apart from its
  alternates line and the objects it omits (R3), and that line always names
  the stable path (R3, R9).
- **Borrowing never fails a create** that would have succeeded without it
  (R4), and the notice reaches the user on every path, the SessionStart hook
  included, whose stderr Claude Code discards.
- **No store is removed while anything names it**, and a failed scan removes
  nothing (R7, R9, R10, R13). Losing a borrower's objects is the one outcome
  that can't be repaired.
- **The rollout condition can't be bypassed** by a setting, an environment
  variable or a config file in a release build (R16, R17). The test build is
  the only place borrowing runs before the turn-on.
- **The turn-on is one small change** that removes a build-time hold and
  touches no logic, so its merge order is easy to read from history (R17).
- **Settings follow the precedent users already know**: `[workspace]
  share_plugin_releases` (`SharePluginReleases *bool`, nil meaning on) and
  the per-repo `*bool` overrides `ReadEnvExample` and `WorktreeSetup` with
  their repo-then-workspace-then-default cascade.
- **Reuse the mirror-repair contract unchanged** beyond R12 to R14:
  `EnsureHealthy`, `Seed`, `Result.Usable`, the per-mirror lock, the log.
- **The mirror package never enumerates instances itself**; callers pass
  clone paths in, which keeps the import direction one way.
- **Plain git only**, at the version niwa already requires (R20), with no
  `gc.*` or `maintenance.*` keys written into a borrower (R21).
- **Tests run in the sandbox**: the suite's own `$HOME`, local git server and
  no network (R20), with failures injected through the test build only.
- **Small, separable changes**, so the defect fixes, the borrowing create,
  the command and the rerun land as separate reviewable pieces ahead of the
  turn-on.

## Considered Options

### Decision 1: The borrowing clone, its fallback and its notice

Resolved inline.

A create has to produce a clone whose alternates file holds exactly
`<stable path>/objects` (R3) and which otherwise matches a full clone: the
same `origin` URL, refs, `git config --local --list` output and hooks. It
must check or seed the mirror first and borrow only from a usable one (R1,
R2), fall back to a full clone with a named reason whenever borrowing can't
happen or fails part way (R4), and never convert an existing clone (R5). The
clone-pool workers report through a no-op Reporter, so the reason has to
travel back in the worker's result.

Key assumptions:
- `git clone --reference` through the stable link writes the store's
  resolved path into alternates, a few milliseconds after it creates the
  target directory and before the fetch. This was measured with git 2.39.5,
  macOS `/private` prefix included.
- Rewriting the alternates file after the clone is safe: git holds no lock
  on it and reads it when a process loads its object database.

#### Chosen: `git clone --reference-if-able`, then rewrite alternates, guarded by the link target

`Cloner.CloneWith` gains a `Reference` option. When it's set the clone runs
`git clone --reference-if-able <stable path> [--depth N] [--branch ref] <url>
<dir>`. git builds the clone exactly as it builds a full one and fetches only
what the reference lacks. Once git exits 0 the create overwrites
`.git/objects/info/alternates` with the one stable-path line, through a
temporary name, an fsync and a rename, and reads it back.

A guard closes the race with a restore. The create reads the stable link's
target before the clone and again after the rewrite. If the target changed,
a restore swapped the store while the clone ran: the create removes the
clone, retries the borrowing clone once, and then clones in full. A missing
alternates file after a borrowing clone (`--reference-if-able` found nothing
at the path), a rewrite that fails, or a read-back that doesn't match all
count as a fallback: the clone is removed and redone in full. So no clone is
ever left naming a resolved store.

The decision to borrow is made per repo before the clone job starts, from
the setting (Decision 2) and the mirror result. `ensureMirrors` now calls
`mirror.Seed` for a borrowing repo with no mirror, and `EnsureHealthy` for
the rest, so the first create builds what later ones share (R2), and
concurrent first creates serialize on the per-mirror lock (R18). A clone
borrows only when `Result.Usable()` is true. A failed borrowing attempt is
followed by `os.RemoveAll` of the target and a full clone through the same
retry wrapper. Only a full clone that also fails fails the create, exactly as
today.

`cloneResult` gains `borrowed bool` and `fallback string`. The fallback is a
fixed vocabulary: `mirror-busy`, `mirror-restore-failed`,
`mirror-seed-failed`, `borrow-clone-failed`, `store-changed`,
`invalid-repo-id`. The pool's collector runs on the command's real Reporter
and prints one warning per fallback repo naming the repo and the reason. On
the SessionStart hook path the same lines join the mirror notices that
`provisionInstance` already carries into the `additionalContext` JSON. A repo
with borrowing turned off is a full clone with no notice, since that's the
user's choice.

Only new clones borrow (R5). The borrowing choice exists only inside the
clone job, and that runs only for a repo directory with no `.git`. Sync,
apply on an existing repo and every other command never call it.

#### Alternatives Considered

- **Write alternates before fetching** (`git init`, write the line, add the
  remote, fetch, set upstream, check out). It never records a resolved path,
  but it rebuilds by hand what `git clone` does: default-branch detection,
  `--branch` and `--depth`, upstream config and the `HEAD` symref. Every
  difference breaks R3's identical-config check. Rejected.
- **`GIT_ALTERNATE_OBJECT_DIRECTORIES` during a plain clone, then write the
  file.** The clone's fetch negotiation doesn't treat the variable's objects
  as already present the way `--reference` does, so the whole history is
  downloaded anyway. Rejected: no saving.
- **`git clone --shared` from the mirror, then point origin at the remote and
  fetch.** The refs come from the mirror, which can lag the remote, and the
  clone's config is a local clone's until rewritten. R3 breaks. Rejected.

### Decision 2: The off setting, the release gate and the test build

Resolved inline.

R6 asks for a workspace setting and a per-repo setting, the repo's value
winning in both directions, with a repo turned off getting a full clone, no
alternates entry and no mirror seeded for it. R16 asks that no release build
borrow for any user until the turn-on, that only the functional suite's test
build borrow before then, and that the setting set to on do nothing until
R17. R17 asks for the turn-on as its own last change. The fault hook R4's and
R8's criteria rely on, the shortened lock wait and R12's environment probe
must exist only in the test build.

Key assumptions:
- The functional suite always runs the binary `make build-test` produces,
  which is built with `-tags niwa_functional`.
- Someone who builds niwa with that tag is building a test binary, which is
  outside what R16 protects.

#### Chosen: `share_git_history` at both levels, a build-tag constant, tagged test hooks

**Keys.** `[workspace] share_git_history` and `[repos.<name>]
share_git_history`, both `*bool`, beside the shipped `share_plugin_releases`.
The cascade follows `ReadEnvExample` and `WorktreeSetup`: the repo's value
when set, else the workspace's, else the build's default. The new
`RepoOverride` field is added to `deepCopyRepos`
(`internal/vault/resolve/deepcopy.go`), which copies field by field and would
otherwise drop it silently. The overlay merge follows the existing rule for
repo fields.

**Release gate.** Two files in `internal/workspace`: `borrow_release.go`
(`//go:build !niwa_functional`) declares `const borrowReleased = false`, and
`borrow_functional.go` (`//go:build niwa_functional`) declares `true`. The
cascade's answer is ANDed with `borrowReleased`, so a release build makes
full clones and seeds no mirror for borrowing whatever any config says. The
setting parses in every build; the guide documents it as taking effect once
borrowing ships. A unit test in the untagged build asserts that a create with
the setting on, and with it unset, makes full clones and seeds no mirror,
which is R16's criterion. The cascade's own default is already on wherever a
workspace root exists; the constant is what holds it off. The turn-on (R17)
deletes the two files and the AND, and nothing else changes.

**Test hooks**, each in a tagged file with an untagged no-op twin:
- **Faults.** `testfault.Maybe` is active in every build, so it isn't called
  directly. A new `internal/testhook` package has `Fault(label) error`, which
  calls `testfault.Maybe` in the tagged build and returns nil otherwise. The
  labels are `borrow-clone` (fails a borrowing clone after git started, for
  R4) and `dissociate-copy` (fails the copy for a named clone, for R8).
- **Lock wait.** `NIWA_TEST_MIRROR_LOCK_WAIT=<duration>`, read only in the
  tagged build, shortens the five-minute and ten-second waits, on the
  shorten-only `NIWA_TEST_VAULT_TIMEOUT` precedent.
- **Environment probe.** `NIWA_TEST_GIT_ENV_PROBE=<file>`, tagged build only:
  each mirror git process appends one JSON line with its kind (network or
  local) and the `GIT_*` entries of the environment it was given, so R12's
  criteria can see exactly what reached git, each variable with its value.
  The probe exists only in the test build and its file lives in the suite's
  sandbox `$HOME`; a release build has no code that writes it.

Four checks keep the hooks and the gate honest. Every borrowing entry point
goes through one function, `borrowEnabled(cfg, repo)`, which the untagged R16
test covers. An untagged test sets all three `NIWA_TEST_*` variables above and
checks that a create's behaviour doesn't change. A unit test parses the
package sources and fails if any file without the `niwa_functional` tag
names those variables. And a check in CI fails if the release configuration
ever passes `-tags niwa_functional`. `niwa version` prints "test build" when
the tag is set, so a stray test binary is easy to spot.

#### Alternatives Considered

- **Keys named `reference_clones`, `use_mirror` or `borrow_objects`.** The
  first is the internal name and means nothing to a user; the second reads
  as turning off the mirror check, which keeps running for any remaining
  borrower (R6); the third names a git mechanism. Rejected.
- **An environment variable or a hidden config key as the gate.** Any user
  can set either, which is an opt-in before the condition holds, and R16 says
  no setting a user can write turns borrowing on. Rejected.
- **Keeping the borrowing code on a branch until the turn-on.** The fixes and
  the rerun (R12, R13, R15) need the borrowing create on main to test
  against, and a long-lived branch rots. Rejected.

### Decision 3: Retiring a mirror generation and finding every borrower

Delegated to /decision.

A restore builds a new store, salvages from the old ones, swaps the stable
link under the per-mirror lock, and today removes the old stores before it
probes the borrowers. Removing a store a clone still reads breaks it in a way
`git status` hides: `git log` exits 128 and `git fsck` reports missing
objects. That can't be undone. Every borrower list a restore receives today
is incomplete: `EnumerateInstances` needs the instance state file, which a
create writes only after cloning, and it ignores `kept/`. `ensureMirrors`
builds clone paths from the current config, and `niwa worktree create` passes
one clone. R7 asks that every check, restore, probe and removal consider every
instance on disk, kept and half-created ones included. R9 asks that a store be
removed only after an enumeration that succeeded and never while any clone's
alternates names it, directly or through the stable path. R10 asks that
destroy keep mirrors a remaining borrower uses and say so.

Key assumptions:
- git resolves a stable-path alternates line when a process loads its object
  database. A borrower naming `mirrors/app/objects`, with `mirrors/app`
  linking to store A, reports store A's path from `git count-objects -v`
  (git 2.39.5). So a git command already running in a borrower keeps reading
  the old store after a swap.
- Kept instances will sit at `kept/<instance>/<group>/<repo>` with alternates
  unchanged; nothing moves an instance into `kept/` yet. The scan helper
  follows wherever the teardown work puts them.
- Only niwa's instance clones under the workspace root and `kept/` name a
  mirror store, the same stated limit the plugin release-hold design accepts.
- Config validation rejects group names `.` and `..`.
- One hour covers nearly all git commands that run in a borrower. A longer
  one that spans a restore fails loudly and works on a rerun; it's never left
  silently corrupt.

#### Chosen: retire by record, remove by a scan-gated sweep past a minimum age

**Discovery.** One helper in `internal/hostdir`, moved there from
`internal/cli`'s `instanceClones`, lists every clone on disk. It starts from
`hostdir.InstanceDirs(home, root)`: the root's real directory children except
`.niwa`, plus every real directory under `~/.niwa/w-<key>/kept/`. No state
file is needed, so half-created instances count. It then takes every real
directory two levels under each instance, skipping `.niwa`. It reads the
disk, not the config, so a repo with borrowing off, or one the config no
longer names, still counts (R6).

The helper has two modes. The lenient mode feeds `Options.Instances` for the
check and probe at every call site: `ensureMirrors`, the hook's recorded
mirrors check, and `niwa worktree create`, which now passes the whole walk
instead of one clone. `Recorded` runs over it too, so a mirror whose repo left
the config while a clone still names it keeps being checked and restored. The
strict mode is the only one that can authorize a removal. A missing alternates
file names nothing; anything else counts as a failure: an error listing the
root, `kept/`, an instance or a group; a symlink or non-directory where a
root, instance or group directory should be; an alternates file that isn't a
regular file, is over 64 KiB, or holds a line that is quoted or doesn't
parse; a resolution error other than not-exist; or a directory that vanished
between listing and reading. Alternates files are opened without following a
symlink and read through a bounded reader. A leftover
`alternates.niwa-dissociate` (Decision 4) is read as an alternates file, so
it pins what it names. A strict read resolves each line (a relative one against the clone's
`.git/objects`, then symlinks) and says a line pins a store when it resolves
into that store's directory, checked with `os.SameFile` on its `objects`
directory, whether the line names the stable path or a `.store/` directory
directly. Linked worktrees share their clone's object database and need no
read of their own.

**The scan crosses the package line as a callback.** `mirror.Options` gains
`Scan func() ([]string, error)`, which every caller sets to the strict
helper. `internal/mirror` calls it under the mirror's lock at the moment it
decides a removal, so the answer is never older than the lock, and the
package still never enumerates instances. A nil `Scan` removes nothing. This
is the plugin store's `Sweep(ctx, scan)` contract.

**Replacing a generation.** Only `rebuild` makes a new one, reached from the
restore and from the refresh's damaged-pack path (R9's first half, as today).
Under the exclusive lock it builds the store, swaps the link, records the
state file, then probes and recovers borrowers by id from a fresh lenient walk
taken under the lock (R13). After that it removes no store that was ever
linked. It writes `.meta/<repo-id>.stores` instead, through a dot-prefixed
temporary name and a rename. The name has no `.json` suffix, because
`Recorded` reads any `.meta/<x>.json` as repo id `<x>`. The file holds one
entry per store: the origin URL it was cloned from (stripped as `recordURL`
strips it), whether it was ever linked, and when a replaced one was retired.
A store's entry is marked linked before the swap, so a crash between the two
keeps it. A store whose entry says it was never linked (a crash after the
rename into `.store/` and before the swap) is removed under the lock with no
scan, since no clone can have resolved the link to it.

**Salvage reads the record.** `rebuild` salvages from, and pins kept refs
from, only stores whose recorded URL matches the current remote, and never
from a store with no entry. That closes a bug any retention would create:
today `fromOtherRemote` reads only the new state file, so an old remote's
store that outlived a url-changed rebuild would be salvaged into the new
remote's store at the next fault. On the first run after upgrade, the linked
store's entry is seeded from the state file.

**Retiring.** A sweep runs inside `EnsureHealthy` for each mirror whose record
lists a retired store at least an hour old. It takes the lock with
`TryFileLock`, skipping when busy, calls `Scan`, and removes by exact name
each such store that no line pins and that neither the link nor the state file
names. Before any removal the recorded name must match the store-name pattern
`<repo-id>-<UTC stamp>-<rand>` for this mirror's validated repo id, and an
`Lstat` must show a real directory directly inside a real `.store/`; a name
that fails either is left alone and logged, never joined into a path for
`RemoveAll`. A nil scan or any error removes nothing and leaves the record. A pinned
store stays and is looked at again later; a last-swept time in the record
limits a mirror to one scan an hour. The hour is a margin for borrower git
processes that resolved the old store before the swap and for a create
stalled between resolving the link and writing its line. It never decides
whether a store a line names can go: only the scan does.

**Space pressure.** When the restore's free-space check would refuse, the
restore first runs the strict scan under its lock, removes retired stores no
line pins whatever their age, and checks again. A refused restore leaves every
borrower broken, which is worse than failing a long-running command.

**Workspace destroy (R10).** `DestroyWorkspace` computes the mirrors directory
and each mirror's lock path before it removes the root, as it already does for
the plugin store. After the root is gone the only instances left are in
`kept/`, so it runs the strict helper over `kept/` alone and decides per
mirror. It keeps the whole mirror and prints the mirror's path and the reason
when a kept clone pins it ("a kept instance borrows from it"), when
`TryFileLock` finds the lock held ("busy"), or when the scan failed ("kept
instances couldn't be read"). Otherwise it removes the link, then each store
the record and the state file name, by exact name with the sweep's
validation, then the state file and the record, then `mirrors/` once empty,
then the key directory if that leaves it empty. It never removes by a
`<repo-id>-*` glob, because repo ids `app` and `app-extra` would collide
under it; a store directory no record names is left and reported.

#### Alternatives Considered

- **The caller's snapshot with a completeness flag, removal inside the
  restore.** The list predates the lock, so a crashed create's half-created
  instance whose alternates names a resolved store loses that store, and
  skipped stores linger until the next restore. Rejected.
- **Two-phase retirement with a grace period as the safety argument.** Time
  only makes safety likely where the scan proves it, and a 24-hour grace kept
  damaged stores around and doubled disk use. Its record and sweep shape were
  kept. Rejected as the safety argument.
- **Never retire automatically.** Retired stores are almost all duplicate
  bytes. Generations pile up and eat the free space a needed restore
  requires. Rejected.
- **A shared lock held by creates across `git clone --reference`.** The scan,
  the create's link-target guard and the hour close the window together,
  without writer starvation or reworking `internal/filelock`. Rejected.
- **A borrower registry.** R7 defines a borrower by what's on disk, and a
  registry drifts from the disk. Rejected.

### Decision 4: The make-independent command

Resolved inline.

R8 asks for a user command that makes a named instance, by default the current
one, independent of the host's stores. It refuses with nothing changed when
another command holds the instance's lock or when free space is below the
mirror's pack sizes for the instance's repos. It copies every borrowed object
into each clone, removes a clone's alternates file only after that copy has
finished and `git fsck` passes with the mirror unreachable, and leaves every
clone still borrowing and readable after a failure part way. It covers
worktrees, skips clones that don't borrow, and reports each clone it changed.
R21 makes it the only niwa path that copies borrowed objects into a borrower.

Key assumptions:
- `git repack -a -d` without `-l` writes objects reached through alternates
  into the clone's own pack, as git documents for `git clone --dissociate`.
- `git fsck` on one instance's clones finishes in reasonable time; R8 asks
  for `git fsck` rather than `--connectivity-only`.

#### Chosen: `niwa dissociate [instance]`, copy everything first, then verify and drop

The name is git's own word for this step (`git clone --dissociate`), so a user
who searches git's documentation meets the same idea. It's a top-level
command beside `destroy`, `reset` and `apply`, takes the same instance
argument and defaults to the instance the working directory is in.

1. Resolve the instance and take `takeInstanceLock(instancePath,
   "dissociate")`, a non-blocking try; held means refuse, nothing changed.
2. List the instance's clones. A clone borrows when its alternates file
   exists; one that doesn't is skipped. Nothing to do prints "nothing to
   change" and exits 0.
3. Sum the pack sizes the mirror state files record for those repos (a
   1 GiB floor per repo when none is recorded, as the restore does) and
   compare with free space on the instance's volume. Short means refuse,
   nothing changed.
4. Copy phase, every borrowing clone, alternates left in place: `git -C
   <clone> repack -a -d`. A failure stops here. Every clone still has its
   alternates file, so every clone still borrows and reads; one already
   repacked just holds duplicates. The `dissociate-copy` fault label fails
   this step in the test build.
5. Verify and drop phase, once every copy succeeded, per clone: rename the
   alternates file to `alternates.niwa-dissociate`, run `git -C <clone>
   fsck` with `GIT_ALTERNATE_OBJECT_DIRECTORIES` unset, remove the renamed
   file on a pass, or rename it back, report the clone and exit non-zero on a
   failure.
6. Report each clone changed.

A crash inside step 5 can leave `alternates.niwa-dissociate` behind. The
strict scan reads that file as alternates, so the store it names stays
pinned, and the next `niwa dissociate` on the instance finishes the step for
that clone: it runs `git fsck` with the file still aside and removes it on a
pass, or renames it back on a failure. Every git call the command makes runs
with every inherited `GIT_*` variable stripped and with `-c gc.auto=0 -c
core.fsmonitor=false -c core.hooksPath=<niwa's empty hooks directory>`, so a
clone's own config can't start a collection or run a hook mid-copy.

#### Alternatives Considered

- **`niwa instance unshare`.** The `instance` group is hidden and serves the
  hook only. Rejected.
- **`niwa apply --dissociate`.** Apply reconciles config; folding a one-off
  object copy into it mixes a migration into a routine command. Rejected.
- **`niwa detach`.** `niwa worktree detach` already exists with another
  meaning. Rejected.
- **One pass that repacks and drops alternates clone by clone.** A failure
  part way leaves a mixed instance, which R8 rules out. Rejected.

### Decision 5: The three mirror defects

Resolved inline.

R12 (niwa#524) asks that the mirror's seed, refresh, restore and fetch by id
keep a named list of remote-access variables while local reads keep
stripping every inherited `GIT_*` variable. R13 (niwa#525) asks that no old
store be removed before the borrower probe and recovery by id finish, and that
a truncated old pack yield every object still readable. R14 (niwa#522) asks
that the SSH and HTTPS spellings of one remote, or a change in the user part
alone, count as the same remote, while a change of host or path still rebuilds
with no salvage.

Key assumptions:
- `git unpack-objects -r` recovers the objects before a damaged region of a
  pack stream; R13's scenario proves it on a pack truncated halfway.
- A state file written by v0.43.0 has no remote identity; it's computed from
  the stored URL on read.

#### Chosen: two environments, removal moved to the sweep with a recovering unpack, a remote identity

**niwa#524.** `localEnv` is today's `gitEnv`. A new `networkEnv` starts from it
and copies back from the parent environment exactly the variables R12 names:
`GIT_SSH`, `GIT_SSH_COMMAND`, `GIT_SSH_VARIANT`, `GIT_ASKPASS`,
`GIT_PROXY_COMMAND`, `GIT_SSL_CAINFO`, `GIT_SSL_CAPATH`, `GIT_SSL_NO_VERIFY`,
`GIT_HTTP_PROXY_AUTHMETHOD`, and `GIT_CONFIG_COUNT` with `GIT_CONFIG_KEY_<n>`
and `GIT_CONFIG_VALUE_<n>` for each n below the count. The count is parsed as
the plain decimal git reads; one that doesn't parse, or is above 64, is
dropped with its entries. `gitExecNetwork` (the clone, fetch and fetch by id)
uses `networkEnv`; every other mirror process uses `localEnv`.
`networkEnv` also sets `GIT_TERMINAL_PROMPT=0` itself, whatever the user's
environment holds, because a mirror network call runs inside niwa commands
and the SessionStart hook, where a credential prompt can't be answered and
would hang the command. It's niwa's own setting, outside R12's inherited
list; credential helpers and `GIT_ASKPASS` still work.

The config entries are filtered by an allowlist, compared case-folded,
because environment config overrides a repository's config file and some keys
would undo the mirror's own guards. Kept: keys under `http.` and `https.`
(proxy, TLS, `http.extraHeader`), `credential.`, `url.<base>.insteadOf`,
`protocol.`, `ssh.`, `core.sshCommand`, `core.gitProxy` and `core.askPass`.
Every other entry is dropped and the rest renumbered. Two groups matter most.
`include.*` and `includeIf.*` stay out because an include tells git to read
another config file, and that file could set any key at all, so no filter on
the entries would hold. `remote.*` stays out because the mirror's correctness
rests on its own remote settings: an environment entry for
`remote.origin.fetch` or `remote.origin.prune` would change which refs a
mirror fetch keeps and could delete the refs that keep removed commits
reachable, which is what lets a borrower recover by id. The same reasoning
drops `gc.*`, `maintenance.*`, `fetch.prune*`, `core.hooksPath`,
`core.fsmonitor`, `core.alternateRefsCommand`, `uploadpack.*`,
`safe.directory` and `extensions.*`. So of the `GIT_CONFIG_COUNT` entries
R12 names, the mirror keeps the ones that carry remote access, which is what
R12's list is for; an entry for any other key is not passed. No environment
value is ever written to the mirror log.

**niwa#525.** Decision 3 already moves every removal out of the restore: a
restore removes no store that was ever linked (except, under space pressure,
retired stores the scan shows unpinned, which it removes only after the
probe and recovery of an earlier restore), and the sweep removes retired
stores later, so removal always follows the probe and recovery, and the mirror
log shows the probe's lines before any removal line. During salvage, an old
store whose pack fails `verify-pack` is fed through `git unpack-objects -r`
under `localEnv` into a temporary object directory under the mirror's own
`.tmp/`, on the same volume, after a free-space check against the pack's
size. Every object it can read is written there and checked with `git
cat-file -e` before it joins the salvage, and the directory is removed on every
exit path. The probe then reads each borrower ref: all objects
present means it reads to the root; otherwise recovery by id from the remote;
and a ref neither can complete is named unrecoverable, as today.

**niwa#522.** `remoteIdentity(url)` parses both the scp-like `user@host:path`
form and URL forms and returns the lowercased host, the port when it isn't the
scheme's default, and the path with a trailing `.git` and slashes removed.
Scheme and user aren't part of it. The state file keeps the stripped URL for
display and gains the identity, and `urlChanged` compares identities. The
same identity spelled differently is not a fault on its own: the mirror's
`remote.origin.url` takes the config's spelling at the next refresh, and a
restore for any other fault salvages as usual. A different host or path is
`url-changed`, rebuilt with no salvage, as today.

#### Alternatives Considered

- **Pass the whole parent environment to network calls.** It brings
  `GIT_DIR`, `GIT_ALTERNATE_OBJECT_DIRECTORIES`, `GIT_INDEX_FILE` and the
  like into a mirror process, which is what the strip exists to stop.
  Rejected.
- **Translate the user's credential setup into `-c` options.** It guesses at
  every transport. Rejected.
- **Reorder the removal inside `rebuild` only.** It fixes the order but throws
  away a truncated pack's readable half, which is most of what R13 asks. It's
  also superseded by Decision 3. Rejected.
- **Strip only the scheme from URLs.** It still treats `git@host:o/r` and
  `ssh://git@host/o/r` as different. Rejected.
- **Compare remotes by asking git (`git ls-remote` on both).** It needs the
  network in a check that must stay local and under 100 ms. Rejected.

## Decision Outcome

The five decisions fit together around one rule: a borrower's alternates
file always names the stable path, and nothing removes a store unless a scan
of the disk, taken under the mirror's lock, proves no line resolves into it.

The create (Decision 1) checks or seeds the mirror, borrows with
`git clone --reference-if-able`, rewrites the one line, and guards against a
swap during the clone by reading the link target before and after. Every
failure turns into a full clone with a reason the user sees. Whether a repo
borrows at all is the setting's answer (Decision 2), ANDed with a constant
that's false in every release build, so until the turn-on only the test build
borrows. That same build carries the fault labels, the shortened lock wait
and the environment probe the scenarios need.

The mirror side (Decisions 3 and 5) changes what a restore does with old
stores: it keeps them, records them in `.meta/<repo-id>.stores`, and leaves
their removal to a sweep that runs an hour later at the earliest, only through
the strict scan passed in as `Options.Scan`. Discovery reads every instance on
disk through `hostdir.InstanceDirs`, so kept and half-created instances count.
Salvage reads damaged packs with a recovering unpack and only from stores of
the same remote, compared by host and path. Network calls keep the user's
remote-access variables, minus any config entry that would override the
mirror's own guards.

`niwa dissociate` (Decision 4) is the one way out: copy everything, verify
with the mirror out of reach, then drop the line. Once an instance has
dissociated, the scan no longer counts it, so a repo turned off can have its
mirror removed after the last borrower leaves.

## Solution Architecture

### Overview

Borrowing is a change to how one repo is cloned, plus the mirror-side rules
that make it safe to keep. No new process runs: every step happens inside
the create, apply, dispatch, hook, worktree and destroy commands niwa already
has, and a new `niwa dissociate`.

### Components

- **`internal/workspace/clone.go`**: `CloneOptions.Reference`; the borrowing
  clone, the alternates rewrite and read-back, and the link-target guard.
- **`internal/workspace/apply.go`**: `ensureMirrors` seeds or checks the
  borrowing repos and returns per-repo usability; the clone pool chooses
  borrow or full per repo; `cloneResult` carries `borrowed` and `fallback`;
  the collector prints fallback warnings.
- **`internal/workspace/borrow_release.go`, `borrow_functional.go`**: the
  `borrowReleased` constant behind the build tag.
- **`internal/config`**: `WorkspaceMeta.ShareGitHistory *bool`,
  `RepoOverride.ShareGitHistory *bool`, and the cascade
  `ShareGitHistoryEnabled(repo)`; `deepCopyRepos` and the overlay merge
  carry the new field.
- **`internal/hostdir`**: the clone walk in lenient and strict modes, and the
  alternates reader that resolves a line and tests it against a store with
  `os.SameFile`.
- **`internal/mirror`**: `Options.Scan`; the `.stores` record; `rebuild`
  keeps stores and records them; the sweep in `EnsureHealthy`; space-pressure
  removal; salvage through `git unpack-objects -r` and only from stores of
  the same remote; `localEnv` and `networkEnv`; `remoteIdentity`.
- **`internal/workspace/destroy_workspace.go`**: the per-mirror keep or
  remove decision after the root is gone.
- **`internal/cli/dissociate.go`**: the new command.
- **`internal/testhook`**: `Fault`, the lock-wait override and the
  environment probe, each with a tagged file and an untagged no-op.
- **`docs/guides/`**: the user guide section (R11).
- **`test/functional/features/`**: `reference-clones.feature`, and
  `mirror-repair.feature` reworked to use create-made borrowers (R15).

### Key Interfaces

```go
package workspace

type CloneOptions struct {
	Depth     int
	Branch    string
	Reference string // stable mirror path; "" means a full clone
}

type cloneResult struct {
	// existing fields ...
	borrowed bool
	fallback string // "" or one of the fixed reasons
}
```

```go
package config

type WorkspaceMeta struct {
	// existing fields ...
	ShareGitHistory *bool `toml:"share_git_history"`
}
type RepoOverride struct {
	// existing fields ...
	ShareGitHistory *bool `toml:"share_git_history"`
}
// ShareGitHistoryEnabled: repo value, else workspace value, else true
// wherever a workspace root exists. Callers go through borrowEnabled, which
// ANDs this with borrowReleased until the turn-on deletes it.
func (c *WorkspaceConfig) ShareGitHistoryEnabled(repo string) bool
```

```go
package hostdir

// Clones lists every clone two levels under every instance directory on
// disk for the workspace, kept and half-created ones included.
func Clones(home, workspaceRoot string) []string
// StrictClones is the same walk, failing on any unreadable directory,
// alternates file over 64 KiB, or entry that vanished mid-walk.
func StrictClones(home, workspaceRoot string) ([]string, error)
// Pins reports whether any alternates line in clones resolves into store.
func Pins(clones []string, store string) (bool, error)
```

```go
package mirror

type Options struct {
	// existing fields ...
	Scan func() ([]string, error) // strict clone walk; nil removes nothing
}
```

```
~/.niwa/w-<key>/mirrors/.meta/<repo-id>.stores   0600, JSON
{"schema": 1, "last_swept": "2026-11-22T03:00:00Z",
 "stores": [
   {"name": ".store/niwa-20261116T101500Z-3f9a", "url": "https://github.com/tsukumogami/niwa.git",
    "identity": "github.com/tsukumogami/niwa", "linked": true, "retired_at": "2026-11-22T02:00:00Z"},
   {"name": ".store/niwa-20261122T020000Z-81c2", "url": "https://github.com/tsukumogami/niwa.git",
    "identity": "github.com/tsukumogami/niwa", "linked": true}]}
```

```toml
[workspace]
share_git_history = false   # no repo borrows unless its own setting says so

[repos.secrets-repo]
share_git_history = false   # this repo never borrows and gets no mirror
```

```
niwa dissociate [instance]
```

### Data Flow

**A create.** `runPipeline` reaches `ensureMirrors`. For each repo whose
setting cascade says borrow and with `borrowReleased` true: a repo with no
mirror gets `mirror.Seed`; the rest get `EnsureHealthy` with `Options.Instances`
from the lenient walk and `Scan` set to the strict walk. Each repo leaves
with a usable or unusable result. The clone pool starts. A usable repo's job
reads the link target, runs the borrowing clone, rewrites and reads back the
alternates line, and reads the link target again; any mismatch or failure
removes the target and runs a full clone, setting `fallback`. The collector
prints one warning per fallback. The hook path puts the same lines in its
`additionalContext` JSON.

**A restore.** Under the exclusive lock: build the new store with salvage from
same-remote stores (damaged packs through `git unpack-objects -r`), mark it
linked in `.stores`, swap the link, write the state file, then probe and
recover borrowers from a fresh lenient walk. Mark the replaced store retired
with the time. Remove nothing that was ever linked, except under space
pressure as Decision 3 describes. Log each step.

**A sweep.** On a later `EnsureHealthy`, for a mirror whose `.stores` has a
retired store at least an hour old and a `last_swept` at least an hour old:
`TryFileLock`, call `Scan`, and remove each such store no clone pins and that
neither the link nor the state file names. Update `last_swept`. Any error
removes nothing.

**Dissociate.** Lock the instance, list its borrowing clones, check space,
repack every clone, then per clone set alternates aside, `git fsck`, and drop
or restore the file.

**Destroy.** Compute mirror paths and locks, remove instances and root as
today, then per mirror scan `kept/` strictly and keep or remove.

## Implementation Approach

The order serves R17: the turn-on is last and depends on the fixes for
niwa#524 and niwa#525 and on the rerun. Everything before it ships dark.

### Phase 1: Remote-access variables on network calls (niwa#524)

`localEnv` and `networkEnv` in `internal/mirror/git.go`, the config-key
filter, `internal/testhook` with all three test hooks (the fault labels, the
lock-wait override and the environment probe, which later phases' scenarios
use too), and the functional scenario with an
SSH-style wrapper reached only through `GIT_SSH_COMMAND`.

Deliverables:
- `internal/mirror/git.go`, `internal/testhook/`
- a scenario and unit tests for R12's three criteria

### Phase 2: Same remote, different spelling (niwa#522)

`remoteIdentity`, the state-file field and the `urlChanged` comparison, with
the SSH-to-HTTPS scenario and the different-remote scenario.

Deliverables:
- `internal/mirror/state.go`, `internal/mirror/check.go`
- R14's scenarios

### Phase 3: Borrower discovery and retirement (niwa#525)

The `hostdir` clone walk in both modes, `Options.Scan`, the `.stores` record,
`rebuild` keeping and recording stores, salvage through the recovering unpack
and only from same-remote stores, the sweep, space-pressure removal, every
call site passing the walk, and `DestroyWorkspace`'s per-mirror decision.

Deliverables:
- `internal/hostdir/`, `internal/mirror/restore.go`, `internal/mirror/mirror.go`,
  `internal/workspace/apply.go`, `internal/cli/instance_from_hook.go`,
  `internal/cli/session_lifecycle_cmd.go`, `internal/workspace/destroy_workspace.go`
- R7, R9, R10 and R13 scenarios

### Phase 4: The setting and the release gate

`share_git_history` at both levels with the cascade, `deepCopyRepos`, the
overlay merge, the `borrowReleased` constant, the fault and lock-wait hooks,
and the untagged unit test for R16.

Deliverables:
- `internal/config/`, `internal/vault/resolve/deepcopy.go`,
  `internal/workspace/borrow_*.go`, `internal/testhook/`

### Phase 5: The borrowing create

`CloneOptions.Reference`, the alternates rewrite and guard, seeding in
`ensureMirrors`, the per-repo choice, the fallback and its notice, on every
path that provisions an instance. Depends on Phases 3 and 4.

Deliverables:
- `internal/workspace/clone.go`, `internal/workspace/apply.go`,
  `internal/cli/instance_from_hook.go`
- `reference-clones.feature`: R1 to R6, R18, R19's pack check, R21

### Phase 6: `niwa dissociate` and the guide

The command and the user guide section with the purge steps, the setting at
both levels and the command (R8, R11).

Deliverables:
- `internal/cli/dissociate.go`, `docs/guides/`
- R8's scenarios

### Phase 7: Repair proofs against real borrowers

Every scenario in `mirror-repair.feature` reworked so its borrowers come from
`niwa create` in the test build, with the hand-written alternates step
removed, passing on Ubuntu and macOS (R15). Depends on Phase 5.

Deliverables:
- `test/functional/features/mirror-repair.feature`,
  `test/functional/mirror_steps_test.go`

### Phase 8: Turn on by default

Delete the two `borrow_*.go` files and the AND, so the cascade's default (on
wherever a workspace root exists) takes effect, and update the guide. Merged
only after Phases 1, 3, 6 and 7: R17 names the fixes for niwa#524 and niwa#525
and the rerun, and Phase 6 is added because the guide and the per-repo off
setting are the conditions the retention decision was accepted on. Its pull
request states the residual risks below for the reviewer to accept. Gating on Phase 3's niwa#525 fix is this project's
own addition, stricter than the condition the turn-on had to meet. Phase 2's
niwa#522 fix doesn't gate it; if it slips, it stays a named limit.

Deliverables:
- `internal/workspace/borrow_*.go` removed, `internal/config/` default,
  `docs/guides/`

## Security Considerations

**What a borrower trusts.** A borrower reads objects from a store under the
user's own `~/.niwa/`, made with mode 0700 directories and 0600 files by the
mirror-repair design, so no other local user can write objects a borrower
reads. git checks every object's hash on read, so a damaged store shows as a
missing or corrupt object, never as different content under the same id.

**Removal is the dangerous step.** The scan-gated sweep is the only path that
removes a store a clone might name, and it refuses on any read error, so an
unreadable `kept/` or a vanished directory costs disk, never a borrower's
objects. Removal is by exact name and never follows a link.

**The environment on network calls.** R12 hands the mirror's network calls
the user's own transport and credential settings, which is what makes them
work for a user who needs them. They're taken from the environment niwa runs
in, never from the remote. For the SessionStart hook and dispatched workers
that environment includes what the workspace config sets through the
settings niwa generates, so a workspace config author can set them there;
that author already chooses the commands and hooks those sessions run, so
this adds no new trust. A connection with `GIT_SSL_NO_VERIFY` or a hostile
proxy could put extra objects or refs into the shared mirror, but a borrower
never takes a ref from the mirror: its refs come from its own fetch of the
remote, and git reads a borrowed object only by the id one of those refs
reaches, checking its hash. So a mirror fetch made that way can add unused
bytes, not change what a borrower checks out. Config entries pass only
through the allowlist in Decision 5, compared case-folded, so `include.path`,
`remote.*`, `gc.*`, `core.hooksPath` and the like can't make a mirror fetch
load another config file, prune or run a hook. No environment value reaches
the mirror log; the test probe, which does record values so R12's criteria
can check them, exists only in the test build and writes inside the sandbox.
Local reads, the recovering unpack and `niwa dissociate` keep stripping every
`GIT_*` variable.

**Retention.** The mirror keeps every commit it ever fetched, by the settled
retention decision. The per-repo off setting and the guide's purge steps,
whose order matters because a borrower ref that still reaches a removed
commit lets the next restore fetch it back by id while the remote serves it,
are the two conditions that decision was accepted on, and both ship here.

**Test hooks don't ship.** The fault labels, lock-wait override and
environment probe exist only in the `niwa_functional` build. A release build
has no code that reads their variables.

**Output.** Fallback notices use a fixed vocabulary and the repo name from
the workspace config, never text the remote chose. A repo name that doesn't
match the mirror's repo-id pattern already clones in full, and its notice
prints a fixed placeholder ("a repo whose name niwa can't mirror") instead of
the name, since notices go to a terminal and into the hook's
`additionalContext`, which an agent reads.

**Details the implementation holds to.**
- The strict scan treats a clone whose `.git` is a symlink or a gitfile, or
  whose `.git/objects/info` is a symlink, as a scan failure; it skips blank
  and `#` comment lines in alternates, as git does.
- Store names in `.stores` are recorded without the `.store/` prefix, and
  each removal `Lstat`s `.store` itself as well as the store.
- The alternates temporary file is created with `O_EXCL`.
- The recovering unpack's free-space check allows twice the pack's size for
  loose objects.
- The new fault labels are reached only through `internal/testhook`; a unit
  test fails if any package calls `testfault.Maybe` with them directly.
- The mirror log notes, without values, when a network call honoured
  `GIT_SSL_NO_VERIFY` or a proxy command.

**Paths the sweep and destroy remove.** Only names recorded in
`.meta/<repo-id>.stores` that match the store-name pattern for a validated
repo id, and that `Lstat` shows as real directories directly inside a real
`.store/`. Nothing is removed by glob or through a link.

**What borrowing changes for a user.**
- Instances of one workspace are no longer a boundary between copies of
  history: every borrower reads the same store. A repo whose history must not
  be shared that way gets the per-repo off setting.
- `niwa dissociate` copies everything a clone reaches into it, a removed
  secret included, so purging a commit from a dissociated clone means a fresh
  clone. The guide says so.
- A clone copied by hand to another place keeps an alternates line the scan
  can't see; once its store is retired and swept, that copy breaks. The guide
  says to run `niwa dissociate` before copying an instance.
- The mirror's network calls now honour `GIT_SSH_COMMAND`, `GIT_ASKPASS`,
  `GIT_SSL_NO_VERIFY` and the rest of R12's list from whatever environment
  started niwa, the SessionStart hook and dispatched workers included.

**Residual risk the turn-on accepts.** Before Phase 8 merges, its pull request
lists these for the reviewer to accept explicitly: instances of a workspace no
longer hold separate copies of history; the mirror keeps commits removed from
the remote, as the retention decision already accepted on the two conditions
this work ships; and `niwa dissociate` copies those retained objects into the
clone it frees. `niwa dissociate` acts only on alternates lines that resolve
into one of this workspace's mirror stores; a clone whose alternates names
anything else is reported and left as it is.
- The remote comparison treats the SSH and HTTPS spellings of one host and
  path as one remote, so salvage crosses that change; a different host or
  path never shares a store. A site that served different content per
  transport for one path would have its objects mixed in one store.
- Every borrower can read every object the shared mirror ever fetched, not
  only what its own branches reach.
- The instance lock covers niwa commands only, so `niwa dissociate` should run
  on an idle instance; a git command a user runs in it meanwhile is not
  blocked.
- Under space pressure a restore removes unpinned retired stores at once, so a
  long-running git command in a borrower that still reads one can fail; it
  works on a rerun.

## Consequences

### Positive

- A second and later instance writes and downloads only what's its own, and
  the clone phase of a burst no longer competes for the whole history.
- The borrower looks like a full clone in every way a user or a script can
  check, apart from one file.
- Borrowing can't fail a create, and a user always sees why a repo didn't
  borrow.
- Old generations go away by themselves once nothing names them, so disk use
  stays near one store per repo.
- The rollout condition is enforced by the build, not by a setting a user
  could flip.

### Negative

- Outside space pressure, a store can't be removed sooner than an hour after
  it's replaced, so a host briefly holds two generations after each restore.
- A kept instance that borrows pins its mirror's store indefinitely, so a
  long-kept instance holds disk until it's dissociated or deleted.
- The strict scan reads every clone's alternates file on every sweep and every
  destroy, which grows with the number of instances on disk.
- A git command that runs longer than an hour across a restore can fail
  partway.
- `niwa dissociate` needs free space for the whole history of each repo it
  covers.

### Mitigations

- The space-pressure path removes unpinned retired stores at once when a
  restore would otherwise be refused.
- The guide tells a user with a long-kept instance to dissociate it, and
  destroy says which mirrors a kept instance keeps.
- The sweep runs at most once an hour per mirror, and only when a retired
  store exists.
- A failed long command is loud and works on a rerun; nothing is left silently
  corrupt.
- `niwa dissociate` checks space first and refuses with nothing changed.
