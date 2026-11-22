---
schema: brief/v1
status: Draft
problem: |
  Every niwa instance holds a full private copy of every repo's git history,
  so each create on a host downloads and writes all of it again, though the
  history is the same in every instance and only each instance's own work
  differs. A second instance costs as much disk and network as the first.
outcome: |
  From the second instance on, a create writes and downloads only what is
  that instance's own, while every instance still reads its whole history
  and keeps its commits to itself. A user can turn the sharing off, detach
  one instance from it, and remove what the host keeps.
---

# BRIEF: reference-clones

## Status

Draft

## Problem Statement

A niwa user who keeps more than one instance of a multi-repo workspace on a
host pays for every repo's history once per instance. `niwa create`,
`niwa dispatch` and the SessionStart hook clone each repo in full, so the
second instance downloads and writes exactly what the first already holds.
On a host that keeps several instances at once, or creates a burst of them
for parallel sessions, the repeated history is most of what an instance
costs on disk, and the clone phase of a burst competes for the same network
link the whole time. The history is identical across instances; the only
part that differs is each instance's own branches and unpushed commits.

The user has no way today to hold that shared history once per host while
every instance still behaves as a full private copy. git can lend objects
from one repository to another through its alternates file, and niwa
already keeps a host-level mirror healthy: the mirror-repair work (PR #511,
released in v0.43.0) restores a deleted or damaged mirror with nobody
acting. But nothing in niwa borrows from that mirror yet, so the user gets
none of the saving, and a borrowing create brings questions the repair work
left open on purpose: how a user turns borrowing off, how one instance is
made to stand on its own again, how an old mirror is replaced without
breaking an instance that still reads from it, and how a user removes a
commit the mirror keeps.

Turning this on for users carries a condition of its own. One defect found
in the repair work's review, niwa#524, drops the git variables that carry
credentials and transport settings from the mirror's clone and fetch, and
the repair test built its borrowers by hand because no borrowing create
existed yet. A user should not get sharing until both are behind it.

## User Outcome

A niwa user who creates a second instance on a host sees it arrive faster
and take much less disk: the create writes and downloads only what is that
instance's own, and the host holds each repo's history once. Inside every
instance nothing looks different. A session reads every ref back to the
root, commits in one instance never show in another until they're pushed
and fetched, and git commands behave as they do in a full clone.

The user never has to tend the shared history. When it's missing, damaged or
unreachable, a create falls back to a full private clone and says so, and an
instance that already borrows from it is repaired by the next ordinary niwa
command. A user who doesn't want sharing, for a whole workspace or for one
sensitive repo, turns it off and gets full clones. A user about to move or
archive one instance makes it independent of the host first, and it keeps
its whole history after the host's copy is gone. A user who has to remove a
commit from the host, a leaked secret for instance, finds where the shared
history lives and the steps that keep the commit out in niwa's guide.

The sharing is on by default wherever a workspace root exists, once the
condition above holds.

## User Journeys

### A second instance on a busy host

A developer who already has one instance of a twelve-repo workspace runs
`niwa create`, or dispatches a session that creates one through the hook.
The create finds the workspace's mirrors healthy, borrows from them, and
fetches only what the mirror lacks. The instance arrives in a fraction of
the time and disk the first one took, and its sessions work exactly as
before.

### Turning sharing off for a repo or a workspace

A maintainer whose workspace includes a repo with sensitive history sets the
off setting for that repo. The next create clones that repo in full, writes
no alternates entry for it and keeps no mirror of it under `~/.niwa/`, while
the other repos still borrow. An operator of a shared build host who wants
no host-level copies at all sets it for the whole workspace and gets full
clones everywhere.

### Making one instance independent

A developer handing an instance to a colleague on another machine, or
archiving it before the host is wiped, runs the command that makes it
independent. niwa copies the borrowed objects into the instance and drops
its alternates entries. The developer then deletes the host's mirror and
the instance still reads its whole history.

### The mirror is gone or unreachable at create time

A developer on a laptop that has just lost its network, or whose mirror was
deleted by a disk cleanup tool, dispatches a new session. The create doesn't
fail: it clones in full when it can, as niwa does today, and its output says
the instance doesn't borrow and why. Instances that already borrow are
restored by the repair the next ordinary command runs.

### Purging a commit from the host

A maintainer learns a pushed commit leaked a secret and the forge has
dropped it. They open niwa's guide, find where the workspace's mirrors
live, and follow the purge steps in the order that keeps the commit from
coming back: rotate the secret, clear the borrowing instances' refs that
reach it, delete the mirror's stores, ask the forge to drop the commit.

### A long-lived instance across a mirror replacement

A developer keeps a reference instance for weeks while dispatched sessions
come and go around it. One of those creates replaces a repo's mirror with a
new generation. When the developer next opens the kept instance, its history
reads in full, because niwa found every instance on disk that still named the
old generation, the kept one included, and didn't retire it while any did.

## Scope Boundary

### IN

- A borrowing create: every command that provisions an instance clones each
  repo against the workspace's host mirror, seeding a repo's first mirror
  when it has none, so the clone holds only what the mirror lacks.
- The fallback to a full clone, with a notice, whenever the mirror isn't
  usable.
- An off setting that works for a whole workspace and for a single repo; a
  repo with it set gets a full clone, borrows nothing and has no mirror on
  the host.
- A way to make one instance independent of the host's shared history.
- How a repo's mirror is replaced by a newer generation, and how niwa finds
  every instance on disk, kept ones included, that still borrows from one, so
  no generation is retired while any of them names it.
- A user guide section saying where a workspace's shared history lives and
  giving the purge steps in the order that keeps a commit out.
- The rollout condition: niwa#524 fixed with a covering test, and the
  mirror-repair proofs rerun against borrowers the real create made, both
  before sharing is turned on for any user; turning it on is the last step.
- A stated place for niwa#525 and niwa#522, which each, while open, add to the commits a
  restore can lose: fixed in this work, a stated dependency, or a named
  limit with its reason.

### OUT

- A copy-on-write seed instance, copied whole on filesystems that support
  it. It is held pending a measurement and isn't part of this work.
- The shared plugin store, which shipped separately and already shares the
  per-workspace host directory this work uses.
- Changes to how the mirror detects and restores damage beyond the named
  issues. That contract is settled; this work calls it.
- Pruning or garbage-collecting the mirror. It never prunes, by the
  retention decision, and this work doesn't revisit that.
- Sharing git state any other way: one shared repository with worktrees,
  shallow or partial clones, or creating an instance with only some of its
  repos.
- Single-instance layouts with no workspace root, which keep today's full
  clones.
- A daemon, scheduler or background job. Everything runs inside commands
  niwa already performs.

## References

- `docs/designs/current/DESIGN-mirror-repair.md`: the mirror this work
  borrows from, and its contract for reference clones.
- `docs/prds/PRD-mirror-repair.md`: the repair requirements, including the
  borrowers its test builds by hand.
- `docs/decisions/DECISION-mirror-retention-residual-risk-2026-11-16.md`:
  the retention risk accepted on two conditions this work delivers.
- `docs/designs/current/DESIGN-plugin-release-hold.md`: the shared plugin
  store, which uses the same host directory, lock scheme and scan of
  instances on disk.
