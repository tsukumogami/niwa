---
schema: prd/v1
status: Done
problem: |
  niwa plans to save disk by letting instances borrow git objects from one
  bare mirror per repo on the host. A borrower holds no copy of what it
  borrows, so a deleted or damaged mirror breaks every borrower late and
  partially, and nothing in niwa would notice or fix it. The workspace owner
  has ruled that reference clones aren't built until a tested design shows
  niwa repairing the mirror by itself, with no repair the user runs by hand.
goals: |
  A missing or damaged mirror is noticed by the next ordinary niwa command
  and restored with nobody acting, borrowers read their history and pass an
  integrity check again, the fate of pushed-then-deleted commits is stated
  and shown, and a test anyone can re-run proves all of it before reference
  clones are built.
absorbed:
  - docs/briefs/BRIEF-mirror-repair.md
---

# PRD: mirror-repair

## Status

Done

Absorbed [BRIEF-mirror-repair](docs/briefs/BRIEF-mirror-repair.md); carried in Absorbed Brief.

## Absorbed Brief

**Why this exists.** Reference clones would let niwa instances borrow git
objects from one bare mirror per repo on the host instead of each holding a
full copy. A borrower keeps none of what it borrows, so a deleted or damaged
mirror breaks every borrower late and partially, and today nothing would
notice or fix it unless a person did. The workspace owner ruled that
reference clones aren't built until a tested design shows the mirror
repairing itself, and that a repair the user runs by hand doesn't count.

**The outcome a user should get.** The owner deletes or damages a mirror
and the next ordinary niwa command, run by them or by an agent, notices and
restores it with nobody acting; the borrowing instances read their history
to the end and pass an integrity check. The owner knows ahead of time what
happens to a local branch built on pushed commits the remote later deleted,
and can decide to build reference clones on the evidence of a test anyone
can run again.

**Who it serves.** The owner, who runs no janitor and no scheduler; agent
sessions in borrowing instances that have no reason to suspect the mirror; a
developer whose local branch sits on a colleague's squash-merged work; and a
reviewer deciding whether reference clones may go ahead.

**Where it ends.** In: noticing and restoring a broken mirror during
commands niwa already runs, keeping commits the remote dropped, the
deleted-remote-commits case, the location of the mirror, and the test. Out:
reference clones and the borrowing create, the plugin store, the seed,
generation retirement, the make-independent command, and any repair a
person runs by hand.

## Problem Statement

Every niwa instance holds a full clone of each repo it manages. The cheapest
saving on a host with many instances is a reference clone: one bare mirror
per repo on the host, and each instance borrows objects from it through
git's alternates file instead of keeping its own. That makes the mirror a
single point of failure. When it's deleted or damaged, every borrower breaks,
late and partially: `git status` and a no-op fetch still succeed with a
warning, and `git log` dies at the first borrowed commit. The failure shows
up deep inside whatever a person or agent was doing in that instance.

The people who run niwa have no janitor and no scheduler, and many sessions
in instances are agents that have no reason to suspect the mirror. A mirror
can still disappear: someone deletes a directory to free space, or a disk
fault damages a pack. So the workspace owner set a gate: reference clones
aren't built until a tested design shows niwa noticing a broken or missing
mirror and restoring it with nobody acting. A repair the user has to run by
hand was considered and ruled out.

One case a plain rebuild can't cover. A mirror rebuilt from the remote holds
only what the remote still has. A local branch in a borrower, built on
commits that were pushed and later deleted from the remote (a squash-merged
pull request whose branch was then deleted is the everyday example), may
depend on objects that only the mirror held. An instance's own unpushed work
isn't exposed, because its objects never leave that instance.

This PRD sets the requirements the design must meet. It doesn't build
reference clones; it builds the mirror's ability to look after itself and
the test that proves it, so the reference-clone work can rely on both.

## Goals

- A deleted mirror is noticed and restored by an ordinary niwa command, and
  its borrowers recover, with nobody running a repair.
- A damaged mirror gets the same outcome.
- What happens to a borrower's local branch on pushed-then-deleted commits,
  when the mirror is then lost, is decided and shown by test: it survives,
  or it's a stated limit with the reason.
- The proof is a test anyone can run again, isolated from real instances.
- The later reference-clone work knows exactly what it can rely on.

## User Stories

- As the workspace owner, I want a mirror I deleted to free space to come
  back by itself the next time I use niwa, so that the instances borrowing
  from it don't break while I'm not looking.
- As an agent session working in a borrowing instance, I want the mirror
  repaired before any niwa command I trigger depends on it, so that my later
  `git log` succeeds and I never need to know the mirror broke.
- As a developer with a local branch on a colleague's pushed work, I want to
  know ahead of time whether that branch survives the remote deleting those
  commits and the mirror being lost, so that a loss is never a surprise.
- As a reviewer deciding whether reference clones may be built, I want to
  run one documented test that shows deletion, damage and the deleted-commit
  case, so that the decision rests on evidence rather than a claim.

## Requirements

The gate the owner set asks for three proofs. Each requirement names the
ones it serves:

- **Proof A, deleted mirror.** With two borrowing instances in the test
  setup, a reviewer deletes the mirror, runs an ordinary niwa command, and
  sees niwa notice and restore it; a history read and an integrity check
  then come back clean in each borrower.
- **Proof B, damaged mirror.** The same reviewer damages the mirror instead
  of deleting it and gets the same outcome, having run no repair.
- **Proof C, deleted remote commits.** A reviewer builds a local branch in a
  borrower on pushed commits, deletes those commits from the remote, then
  deletes the mirror, and sees what the design says happens to that branch:
  it survives, or the design names the case as a stated limit with the
  reason.

### Functional

- **R1. Detection without anyone acting.** niwa checks a mirror's health
  during commands it already runs: at least `niwa create`, `niwa apply`,
  `niwa dispatch` and the SessionStart hook path, plus any command the design
  adds. There is no daemon, no schedule, and no command a person must run
  for the check to happen. (Proofs A, B)
- **R2. What counts as broken.** The check treats each of these five faults
  as broken: (1) the mirror directory is missing; (2) the directory exists
  but its object store is missing or empty; (3) a pack file or its index
  file is deleted; (4) a pack file is truncated or has bytes overwritten;
  (5) the repository's `HEAD` or `config` is missing or unreadable by git.
  The design may cover more faults; it may not cover fewer. (Proofs A, B)
- **R3. Automatic restore.** When the check finds a mirror broken, niwa
  restores it before the command does anything that depends on it, with no
  prompt. Borrowers aren't re-cloned. The only change niwa may make inside a
  borrower is rewriting its alternates entry to name the restored mirror.
  (Proofs A, B)
- **R4. Borrowers recover.** After a restore, in every instance borrowing
  from that mirror, `git log` over every local and remote-tracking ref reads
  to the root and `git fsck` reports no missing or corrupt object. (Proofs
  A, B)
- **R5. One restore at a time.** When two niwa commands find the same mirror
  broken at once, the result is one sound mirror. No borrower ever sees a
  half-built mirror in the mirror's place. (Proofs A, B)
- **R6. A failed restore isn't worse than no restore.** If the restore can't
  finish (the remote is unreachable, the disk is full), niwa removes nothing
  from the mirror's location until a complete replacement is ready, leaves
  every borrower's alternates entry and objects as they were, says so in its
  output, and tries again on the next command. A command whose own work doesn't need the
  mirror still completes. (Proofs A, B)
- **R7. The repair is visible afterwards.** Each detection and restore is
  recorded where the owner can find it later (command output and niwa's
  log), naming the mirror, what was wrong, and whether the restore worked.
  (Proofs A, B)
- **R8. The mirror keeps what the remote drops.** While a mirror exists, it
  never discards a commit because no remote branch points at it any more.
  Its refresh, the fetch niwa runs to bring the mirror up to date with the
  remote, never prunes, and niwa never runs a collection in the mirror that
  drops unreachable objects. (Proof C)
- **R9. The deleted-remote-commits case is decided.** The design states what
  happens to a borrower's local branch built on pushed commits that the
  remote later deleted, when the mirror is then deleted: either the branch
  survives (and the design says what keeps its objects), or it's a stated
  limit with the reason. The test shows that outcome. (Proof C)
- **R10. Unpushed work is never touched.** Neither detection nor restore
  deletes or rewrites any object, ref or file inside an instance, other than
  the alternates entry niwa owns. An instance's own unpushed commits are
  untouched by every path in this feature. (Proofs A, B, C)
- **R11. Location.** The mirror lives in niwa's host-level state for the
  workspace: under the user's `~/.niwa/`, in a directory keyed by the
  workspace root's canonical path, and never under the workspace root's
  `.niwa/` or inside an instance. That rule is already settled for all
  host-level state niwa owns; this feature applies it and doesn't reopen
  it. (Proofs A, B, C)

### Test

- **R12. A test anyone can run again.** niwa's functional test suite carries
  scenarios for Proofs A, B and C. Each scenario sets up a remote, a mirror
  and two borrowing instances on its own, inside the suite's isolated home
  directory, and touches no real instance, clone or mirror on the host. The
  design documents the one command a reviewer runs to execute them, and CI
  runs them on Linux and macOS. (Proofs A, B, C)
- **R13. Borrowers without reference clones.** Because the borrowing create
  is later work, the test builds its borrowers itself, the way a reference
  clone would, so the scenarios prove repair against the real git behaviour
  borrowers will have. (Proofs A, B, C)

### Non-functional

- **R14. Cheap when healthy.** A check that finds the mirror healthy makes
  no network call and takes under 100 ms per mirror on the CI runners.
- **R15. A contract for reference clones.** The design states what the
  reference-clone work can rely on from this feature: where a mirror lives,
  the call that checks and restores it, the guarantees in R3 to R10, and
  what a new create does when a mirror can't be restored (falls back to a
  full clone).

## Acceptance Criteria

- [ ] A functional scenario creates a mirror and two borrowers, deletes the
      mirror, runs an ordinary niwa command, and asserts that niwa reported
      a repair, the mirror exists again, and `git log` over every ref and
      `git fsck` succeed in both borrowers. (R1, R3, R4; Proof A)
- [ ] The deletion scenario runs once for each command R1 names (`niwa
      create`, `niwa apply`, `niwa dispatch`, and the SessionStart hook
      path), and passes for each. (R1)
- [ ] Four functional scenarios each produce one of R2's faults (2) to (5)
      by a stated file operation (empty the object store; delete one pack
      file; truncate one pack file to half its size; delete `HEAD`), run an
      ordinary niwa command with no repair step, and assert the same outcome
      as the deletion scenario. (R2, R3, R4; Proof B)
- [ ] In each repair scenario, nothing inside a borrower changes except its
      alternates entry: its refs, index and own object files are identical
      before and after. (R3)
- [ ] A functional scenario builds a local branch in a borrower on pushed
      commits, deletes them from the remote, deletes the mirror, runs an
      ordinary niwa command, and asserts the outcome the design states. If
      the design says the branch survives, `git log` on it reads to the
      root and `git fsck` is clean. If the design names a stated limit, the
      scenario asserts niwa's output names that branch as unrecoverable and
      its remote-tracking ref as a stale ref, and every ref in the borrower
      niwa doesn't report still reads clean. (R8, R9; Proof C)
- [ ] After each repair scenario, niwa's log holds an entry naming the
      mirror, the fault found and whether the restore worked. (R7)
- [ ] In each scenario the mirror is under the sandbox home's `~/.niwa/`
      directory keyed by the workspace root, and nothing mirror-related is
      written under the workspace root's `.niwa/` or inside an instance
      other than its alternates entry. (R11)
- [ ] Before any fault is introduced, each test borrower's alternates entry
      names the mirror and the borrower holds none of the mirror's objects
      itself. (R13)
- [ ] A functional scenario deletes remote branches, runs the mirror's
      refresh, and asserts the mirror still holds the dropped commits.
      (R8)
- [ ] A scenario starts two niwa commands against the same broken mirror at
      once and asserts one sound mirror results and both borrowers recover.
      (R5)
- [ ] A scenario makes the remote unreachable, breaks the mirror, runs a
      command that doesn't need the mirror, and asserts the command
      succeeds, reports the failed restore, and a later command with the
      remote back restores the mirror. (R6)
- [ ] In every scenario, an unpushed commit made in a borrower beforehand is
      still present and reachable afterwards. (R10)
- [ ] The scenarios run in CI on Linux and macOS with the home and XDG
      directories pointed at sentinels, and the job fails if anything is
      written outside the suite's sandbox. (R11, R12)
- [ ] With the remote made unreachable, a healthy-mirror check succeeds
      (so it made no network call) and the suite measures it under 100 ms
      per mirror. (R14)
- [ ] The design names, for each of Proofs A, B and C, the scenario that
      proves it and the command a reviewer runs. (R12)
- [ ] The design carries the contract R15 asks for. (R15)

## Out of Scope

- Reference clones and the create path that borrows from the mirror. That's
  the next feature, and it waits on this one.
- The shared plugin store, though it sits beside the mirror and a missing
  plugin release falls under the same kind of gate.
- A copy-on-write seed for new instances, which is on hold.
- Replacing a mirror by generation, finding every borrower before an old
  generation is removed, the command that makes an instance independent of
  the mirror, and the setting that turns borrowing off. Those belong to the
  reference-clone work.
- A repair a person runs as the way the mirror gets fixed. A manual route
  may exist for diagnosis; it doesn't meet R1.
- Single-instance layouts, which have no workspace root to key a mirror by.
- Building any of this now. Implementation, and any run of the test on a
  real host's instances, is a later unit.

## Known Limitations

- A mirror rebuilt from the remote can only hold what the remote still has.
  R8 keeps dropped commits for as long as the mirror exists; whether a
  borrower's local branch on such commits survives the mirror's loss is
  R9's decision, and if the answer is a stated limit, the loss needs two
  independent events: the remote deleting the commits, and the mirror being
  deleted or damaged afterwards.
- Detection runs only when niwa runs. A mirror deleted between two niwa
  commands leaves borrowers broken until the next one; the owner accepted
  this shape when ruling out a daemon and a scheduler.

## Decisions and Trade-offs

- **Repair happens inside commands niwa already runs, not in a daemon or a
  scheduled job.** A daemon was out because the people this serves run no
  scheduler, and a by-hand repair was ruled out by the owner. The cost is
  the window in the second known limitation.
- **Borrowers in the test are built by the test.** Waiting for the
  reference-clone create would invert the gate this feature exists to
  satisfy. Building borrowers the way git builds a reference clone keeps the
  proof honest about git's behaviour.
- **The mechanism is the design's.** This PRD fixes what must be true and
  how it's checked; how detection, restore, locking and retention work is
  settled in the design.
