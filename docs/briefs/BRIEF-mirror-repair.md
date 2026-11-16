---
schema: brief/v1
status: Accepted
problem: |
  A planned host-level git mirror would let niwa instances borrow history
  instead of each holding a full copy, but deleting or damaging it breaks
  every borrower late and partially, and today nothing would notice or fix
  that unless a person did. Borrowing is not safe to ship until that changes.
outcome: |
  When the mirror is deleted or damaged, the next ordinary niwa command
  notices and restores it with nobody running a repair, and the instances
  that borrowed from it read their history and pass an integrity check again.
  A reviewer can re-run a test that shows this before reference clones exist.
motivating_context: |
  niwa instances are getting heavy on disk, and the cheapest saving found is
  reference clones that borrow objects from one mirror per repo on the host.
  The workspace owner made a ruling: no reference clones until a tested design
  shows the mirror repairing itself, and a repair the user runs by hand doesn't
  count. This brief frames that gate as a feature of its own.
---

# BRIEF: mirror-repair

## Status

Accepted

Framing for the self-repairing host mirror. It precedes the requirements
and design that settle how niwa detects and restores a broken mirror.

## Problem Statement

Every niwa instance holds a full clone of each repo it manages, so a host
running many instances keeps many copies of the same history. The saving
that costs least to build is a reference clone: one bare mirror per repo on
the host, with each instance borrowing objects from it through git's
alternates file instead of holding its own. That saving comes with a single
point of failure. A borrower doesn't hold the objects it borrows, so when the
mirror is deleted or damaged, every borrower breaks, and it breaks late and
partially: `git status` and a no-op fetch still succeed with a warning, and
`git log` dies on the first borrowed commit. Nobody finds out until a command
deep inside some session fails for a reason that has nothing to do with what
that session was doing.

niwa today has no host mirror and no notion of one being broken. A mirror
can disappear for reasons niwa doesn't control: a person deletes a directory
under their home to free space, or a disk error damages a pack file. The
people who run niwa have no janitor and no scheduler, and many of the
sessions running in instances are agents with no reason to suspect the
mirror. If restoring the mirror takes a person noticing and running a
command, borrowing will eventually leave instances broken with nobody
watching.

There's a second gap a plain rebuild doesn't close. A mirror rebuilt from
the remote holds only what the remote still has. When a borrower has a local
branch built on commits that were pushed and later deleted from the remote,
as happens when a pull request is squash-merged and its branch deleted, those
commits may live only in the mirror. If the mirror goes, a rebuild can't
bring them back. An instance's own unpushed work isn't at risk, since its
objects stay in that instance; the exposure is pushed work the remote has
since dropped.

## User Outcome

The workspace owner can delete or damage a host mirror and watch niwa put
it back on its own. The next ordinary niwa command they or an agent run
notices the mirror is missing or broken, restores it from the remote, and
the instances that borrowed from it work again: their history reads to the
end and an integrity check comes back clean. Nobody types a repair command.

The owner knows ahead of time what happens to a local branch built on
pushed commits the remote later deleted: either it survives the mirror's
loss, or the documentation states it as a limit and says why. And because
the proof is a test anyone can run again rather than a claim, the owner can
decide to build reference clones on evidence.

## User Journeys

### The owner deletes a mirror to free disk space

The workspace owner, low on disk, deletes the mirror directory for one repo
under niwa's host-level state while two instances borrow from it. Later they
run `niwa create` for a new instance (or an agent session starts through the
SessionStart hook). niwa notices the mirror is gone before it does anything
that depends on it, rebuilds it from the remote, and finishes the command.
In each of the two existing instances, `git log` reads to the root and
`git fsck` reports nothing missing.

### A disk error damages a mirror

A pack file in the mirror is truncated by a failed write; nobody notices.
An agent working in a borrowing instance triggers an ordinary niwa command
in the course of its session. niwa finds the mirror damaged, replaces it
with a sound copy, and the agent's later `git log` succeeds. The agent never
learns there was a problem, and the owner can see afterwards, in niwa's
output or log, that a repair happened.

### A squash-merged branch outlives its remote commits

A developer in a borrowing instance starts a local branch on top of a
colleague's pushed feature branch. The colleague's pull request is
squash-merged and its branch is deleted from the remote. Some time later
the mirror is deleted. The developer's local branch either still resolves to
the end of its history, or the developer finds the case named as a known
limit, with the reason, in the documentation they were pointed to, so the
loss isn't a surprise.

### A reviewer re-runs the proof before reference clones are built

A reviewer deciding whether reference clones can go ahead runs the test the
design defines: two borrowing instances, then a deleted mirror, then a
damaged one, then the squash-merge case. They run only ordinary niwa
commands and the git reads that check the result, and they see each outcome
the design promises. The test runs in isolation, so it touches no real
instance or mirror on the reviewer's host.

## Scope Boundary

**IN:**

- Noticing a host mirror that is missing or damaged, during commands niwa
  already runs, with no daemon, schedule or user action.
- Restoring that mirror so its borrowers work again, including when two
  niwa commands find it broken at the same moment.
- How the mirror keeps commits no remote branch points at any more, and
  what happens to a local branch built on pushed commits the remote later
  deleted when the mirror is then lost: survives, or a stated limit with
  the reason.
- A reproducible test, with two borrowing instances, that shows detection,
  repair and recovery for a deleted mirror and for a damaged one, plus the
  deleted-remote-commits case.
- The mirror's place under niwa's host-level state, following the location
  rule already settled for that state, and what the later reference-clone
  work can rely on from this feature.

**OUT:**

- Reference clones themselves and the create path that borrows from the
  mirror. That's the next feature, and it waits on this one.
- The shared plugin store, even though it sits beside the mirror and the
  same repair gate applies to a missing plugin release.
- A copy-on-write seed for new instances, which is on hold.
- Retiring old mirror generations and finding every borrower before one is
  deleted, and a command that makes an instance independent of the mirror.
  Those belong to the reference-clone work.
- A repair a person runs by hand as the way the mirror gets fixed. A manual
  route can exist for diagnosis, but it doesn't satisfy this feature.
- Protecting an instance's own unpushed work. Its objects never leave the
  instance, so the mirror's loss doesn't touch it.
- Building any of it now: this brief, and the documents that follow it,
  scope the work. Implementation is a later unit.
