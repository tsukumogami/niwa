# Reference Clones

niwa keeps one host mirror per repo and workspace, and a new clone can borrow
git objects from it instead of downloading its own copy of the whole history.
This guide says where the mirrors live, how to turn borrowing off, how to make
an instance independent of the mirrors with `niwa dissociate`, and how to purge
a commit that must not stay on the host.

Borrowing is on by default wherever a workspace root exists: instances that
live in their own directories under a workspace root, the layout `niwa create`
makes, borrow with no config change. A single-instance layout, where the
instance is itself the root and there's no workspace root above it, keeps
making full clones. Clones made
before borrowing was turned on stay full clones; only clones made after it
borrow.

## Where the mirrors live

A workspace's mirrors live under `~/.niwa/w-<key>/mirrors/`, where `<key>` comes
from a hash of the workspace root's path:

```
~/.niwa/w-<key>/mirrors/
├── <repo>                         # the stable path, a link into .store/
├── .store/<repo>-<stamp>-<rand>/  # the store the link names now, and older ones
└── .meta/                         # state files, store records, mirror.log
```

A borrowing clone's `.git/objects/info/alternates` holds one line, the stable
path followed by `/objects`. It never names a `.store/` directory, so a
restore that builds a new store and moves the link keeps every borrower
reading. Only new clones borrow: a clone that already exists is never turned
into a borrower, or back into a full clone, by `niwa apply` or any other
command except `niwa dissociate`.

## Turning borrowing off

`share_git_history` turns borrowing off for the whole workspace or for one
repo:

```toml
[workspace]
name = "myws"
share_git_history = false     # no repo borrows...

[repos.app]
share_git_history = true      # ...except app, which still does

[repos.secrets]
share_git_history = false     # this repo never borrows and gets no mirror
```

The repo's value wins over the workspace's in both directions. With the
workspace off and a repo on, only that repo borrows. With the workspace on
and a repo off, that repo gets a full clone with no alternates file, and niwa
seeds no mirror for it. Leaving both unset means on. To turn borrowing off
for one repo and keep it for the rest, set only that repo's
`share_git_history = false`.

The setting governs clones made after it changes. Clones that already borrow
keep borrowing. A repo you turn off while an instance still borrows from its
mirror keeps that mirror checked and restored, because those clones break
without it. Once its last borrower is dissociated (see below), `niwa apply`
stops checking and restoring that mirror, and you can delete it.

## Making an instance independent: `niwa dissociate`

```
niwa dissociate [instance]
```

With no argument it acts on the instance the current directory is in; from the
workspace root, name the instance. It copies every object each clone borrows
into the clone's own pack, then removes the clone's alternates file once
`git fsck` passes without it. Worktrees of those clones share the clone's
objects, so they're covered too. It lists each clone it changed, and a run
with nothing left to change prints `nothing to change`.

Run it:

- **Before copying or archiving an instance.** A clone copied by hand keeps an
  alternates line that niwa's retention scan can't see. Once the store it
  reads is retired and removed, the copy can't read its history.
- **On an instance you keep for a long time.** A borrowing instance keeps the
  mirror's disk in use until it's dissociated or deleted.

A dissociated clone holds every object it reaches, a removed secret included,
because it copies what the mirror held. Purging a commit from a dissociated
clone means making a fresh clone.

What it refuses, with nothing changed:

- another niwa command holds the instance's lock;
- free space on the instance's volume is below the mirror pack sizes of the
  instance's repos, as the mirror state files record them, counting 1 GiB for
  a repo with no size recorded;
- a clone's alternates file can't be read for certain.

Every clone is copied before any alternates file is removed, so a failure part
way leaves every clone still borrowing and readable, and running the command
again is safe. A clone that doesn't borrow is left alone. A clone whose
alternates file names anything outside this workspace's mirrors is reported
and left as it is.

While it checks a clone, the command renames the clone's alternates file to
`alternates.niwa-dissociate`. If it's interrupted at that point, the next
`niwa dissociate` on the instance finishes the step: it runs `git fsck` with
the file still aside, then removes the file when the check passes or renames
it back to `alternates` when it fails.

The instance lock covers niwa commands only, so run `niwa dissociate` on an
idle instance: a git command you run in the instance meanwhile isn't blocked.

## Purging a commit that must not stay on the host

The mirror keeps commits the remote has dropped, so a secret pushed by mistake
stays on the host after it's removed upstream. To purge it, do these steps in
this order:

1. **Rotate the secret.** Once it's rotated, every copy left anywhere is
   worthless, whatever happens to the steps below.
2. **Clean every borrowing instance.** In each instance that borrows from the
   repo's mirror, rewrite or delete every local branch that reaches the
   commit, then run `git fetch --prune`.
3. **Delete the mirror.** Remove the repo's stable path,
   `~/.niwa/w-<key>/mirrors/<repo>`, and its `.store/<repo-id>-*` directories.
4. **Ask the forge to drop the commit.** A hosting service can keep a commit
   reachable by id after every branch that named it is gone.

Why the order matters: the next niwa command restores a deleted mirror, and a
restore fetches back, by id, any commit a borrower's ref still reaches while
the remote still serves it. Cleaning the borrowers before deleting the mirror
keeps the commit from coming back, and asking the forge last means the commit
is gone from the remote only after nothing on the host can ask for it.

Dissociated clones and clones copied by hand hold their own copy of every
object they reach. Steps 2 and 3 don't reach those: replace them with a fresh
clone.
