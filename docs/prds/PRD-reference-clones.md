---
schema: prd/v1
status: Draft
problem: |
  Every niwa instance clones each repo's full history, so a host with several
  instances of a workspace downloads and writes the same history once per
  instance, and a burst of creates competes for one network link. niwa
  already keeps a self-repairing host mirror per repo, but no create borrows
  from it, and nothing yet answers how a user turns borrowing off, makes one
  instance independent, or removes a commit the mirror keeps.
goals: |
  From the second instance on, a create writes and downloads only what is
  that instance's own, every instance still behaves as a full private clone,
  and a mirror that is missing or unusable costs a full clone rather than a
  failed create. Borrowing reaches users by default only after the
  credential-variable defect is fixed and the repair proofs pass against
  borrowers the real create made.
upstream: docs/briefs/BRIEF-reference-clones.md
---

# PRD: reference-clones

## Status

Draft

## Problem Statement

A developer who keeps more than one instance of a multi-repo workspace on a
host pays for every repo's history once per instance. Each create clones
every repo in full, so the second instance downloads and writes exactly what
the first already holds, and a burst of creates for parallel sessions spends
its clone phase competing for the same network link. The history is the same
in every instance; only each instance's own branches and unpushed commits
differ.

niwa already keeps one bare mirror per repo on the host for each workspace,
under `~/.niwa/`, and since v0.43.0 restores a deleted or damaged mirror
from inside the commands it already runs (`DESIGN-mirror-repair.md`). Git
can lend that mirror's objects to every clone through the clone's alternates
file. But no create borrows yet. The mirror-repair work left the borrowing
create, the off setting, the command that makes an instance independent,
mirror generation replacement and the purge guide to this work, and accepted
the mirror's retention of removed commits on the condition that the off
setting works per repo and the guide ships with it
(`DECISION-mirror-retention-residual-risk-2026-11-16.md`).

Borrowing can't reach users yet. niwa#524 drops the git variables that
carry credentials and transport settings (`GIT_SSH_COMMAND`, `GIT_ASKPASS`
and similar) from the mirror's own clone and fetch, so a user whose access
depends on one gets a mirror that can't seed, refresh or restore. And the
repair proofs built their borrowers by hand (`PRD-mirror-repair.md`, R13),
because no borrowing create existed, so they don't yet show that repair
works on the borrowers users would actually have.

## Goals

- A developer's second and later instances on a host arrive faster and take
  much less disk, because each writes and downloads only what is its own.
- Nothing inside an instance looks different: every ref reads to the root,
  commits stay private to the instance until pushed, and git behaves as in a
  full clone.
- Nobody tends the shared history. A missing, damaged or unreachable mirror
  costs a full clone with a notice, never a failed create, and borrowers are
  repaired by the next ordinary command.
- A user can turn borrowing off for a workspace or one repo, make one
  instance independent before moving it, and find the purge steps for a
  commit that must leave the host.
- Borrowing is on by default wherever a workspace root exists, and reaches
  users only once the condition above holds.

## User Stories

1. As a developer with one instance of a twelve-repo workspace, I want my
   next `niwa create` or dispatched session to borrow history from the host,
   so that it arrives in a fraction of the time and disk the first one took.
2. As a maintainer of a workspace with one repo whose history is sensitive,
   I want to turn borrowing off for that repo alone, so that its history is
   never copied outside the instances while the other repos still share.
3. As an operator of a shared build host, I want to turn borrowing off for a
   whole workspace, so that the host holds no copies of history outside the
   instances.
4. As a developer handing an instance to a colleague or archiving it, I want
   one command that makes it independent of the host, so that it keeps its
   whole history after the host's mirror is gone.
5. As a developer whose laptop just lost its network, or whose mirror a
   cleanup tool deleted, I want a new create to clone in full and tell me it
   didn't borrow, so that I'm never blocked by the shared store.
6. As a maintainer who learned a pushed commit leaked a secret, I want the
   guide to tell me where the host keeps history and the purge steps in the
   order that keeps the commit out, so that it doesn't come back on the next
   niwa command.
7. As a developer who keeps a reference instance for weeks, I want a mirror
   replaced while it sits unused to leave it readable, so that I never open
   it to a history it can't read.

## Requirements

### Functional

- **R1. Borrowing create.** When borrowing is on for a repo, every path that
  clones that repo into an instance (`niwa create`, `niwa apply` adding a
  repo, `niwa dispatch`, the SessionStart hook's provisioning, `niwa init`
  when it creates an instance) clones it borrowing from that repo's mirror,
  after the mirror-repair check has run, and only when the check reports the
  mirror usable. The new clone holds only objects the mirror lacks.
- **R2. First mirror.** When a repo has no mirror yet, the create seeds one
  through the mirror-repair feature's seeding entry point and then borrows
  from it, so the first instance's create already builds what later
  instances share.
- **R3. Alternates name the stable path.** A borrowing clone's alternates
  file holds exactly one line, the repo's stable mirror path followed by
  `/objects`, never a resolved store path. niwa writes nothing else inside
  the instance for borrowing: the clone's `origin`, refs, config and hooks
  are what a full clone would have.
- **R4. Fallback.** When the mirror is not usable for a repo at create time
  (missing and can't be seeded, restore failed, busy past the lock wait,
  the remote unreachable for seeding), or a borrowing clone fails part way,
  the create clones that repo in full as niwa does today, leaves no partial
  borrower behind, finishes, and its output names the repo and the reason it
  didn't borrow. Borrowing never makes a create fail that would have
  succeeded without it.
- **R5. Only new clones borrow.** A repo already cloned in an instance is
  never converted to a borrower or back by sync, apply or any other
  command except the one in R8.
- **R6. Off setting, workspace and repo.** A workspace setting turns
  borrowing off for the whole workspace, and a per-repo setting turns it off
  or back on for one repo, the repo's value winning over the workspace's.
  A repo with borrowing off gets a full clone and no alternates entry, and
  niwa seeds, refreshes and restores no mirror for it under `~/.niwa/`. The
  setting governs clones made after it changes; existing clones keep what
  they are.
- **R7. Kept instances count.** Every mirror check, restore and probe, and
  every decision to remove a mirror store, considers every instance on disk
  for the workspace: those under the workspace root and those kept under
  the workspace's host directory, half-created ones included.
- **R8. Make an instance independent.** A user command makes a named
  instance (by default the current one) independent of the host's stores:
  it copies every borrowed object into each of the instance's clones, then
  removes their alternates entries. It changes nothing until the copy is
  complete and each clone passes `git fsck`, checks free space before it
  starts, takes the instance's lock, is safe to run again, covers worktrees
  of those clones, and reports what it changed. Afterwards the instance
  reads its whole history with the mirror deleted.
- **R9. Generations are never retired while named.** A mirror generation
  (one store directory behind a repo's stable path) is replaced by a newer
  one only through the mirror-repair restore or a rebuild for a changed
  remote. niwa removes an old generation only after it has enumerated every
  instance on disk per R7 without error, and never removes a store that any
  clone's alternates names, directly or through the stable path. A failed
  enumeration removes nothing.
- **R10. Workspace destroy.** Destroying a workspace removes its mirrors
  when no instance on disk, kept ones included, still borrows from them, and
  keeps them, saying so, when one does.
- **R11. Purge guide.** A user guide section says where a workspace's
  mirrors live (`~/.niwa/w-<key>/mirrors/`) and gives the purge steps for a
  commit that must not stay on the host, in this order: rotate the secret;
  in each borrowing instance, rewrite or delete any local branch that
  reaches the commit and run `git fetch --prune`; delete the repo's stable
  path and its `.store/<repo-id>-*` directories; ask the forge to drop the
  commit. It says why the order matters, and it documents the off setting
  and the R8 command.
- **R12. Credential and transport variables reach the mirror's network
  calls (niwa#524).** The mirror's clone and fetch keep the user's
  `GIT_SSH_COMMAND`, `GIT_SSH`, `GIT_ASKPASS` and the other inherited
  variables git uses to authenticate to or reach a remote, while local
  reads keep stripping every inherited `GIT_*` variable as the mirror-repair
  design requires.
- **R13. Old stores outlive borrower recovery (niwa#525).** A restore keeps
  every old store until the borrower probe and recovery by id have finished,
  and recovers from a truncated old pack whatever objects remain readable, so
  objects an old store still held are not lost because they were removed
  before the probe asked for them.
- **R14. A different spelling of the same remote is the same remote
  (niwa#522).** Moving a repo's URL between its SSH and HTTPS forms, or
  changing only the user part, is treated as the same remote: the restore
  salvages old stores as for any other fault. A genuinely different host or
  path still rebuilds with no salvage, as today.
- **R15. Repair proofs against real borrowers.** The mirror-repair proofs
  (deleted mirror, damaged mirror, deleted remote commits, and the refresh,
  concurrency, failed-restore and healthy-check scenarios) run against
  borrowers that the borrowing create made, with no hand-written alternates,
  in the functional suite on Linux and macOS, and pass.
- **R16. Off for users until the condition holds.** Until R12 is met with a
  covering scenario and R15 passes, no released niwa borrows for any user:
  the borrowing create runs only where the functional suite enables it, and
  no setting a user can write turns it on. The R6 setting may exist before
  then; setting it to on does nothing until R17.
- **R17. Turn on by default.** Borrowing becomes on by default wherever a
  workspace root exists, as its own last change, made only after R12 (with
  its scenario), R13 and R15 have landed. A layout with no workspace root
  keeps full clones.

### Non-functional

- **R18. Saving.** On a multi-repo workspace in the functional suite's
  sandbox, the second create's write to the volume, read from free space
  before and after, is less than two thirds of the first create's.
- **R19. No new cost when healthy.** Borrowing adds no network call to a
  create beyond what fetching the mirror's missing objects needs, and the
  healthy mirror check stays offline and under 100 ms per mirror.
- **R20. Platforms and isolation.** Everything works on Linux and macOS with
  portable git and no reflink or snapshot support, runs inside commands niwa
  already performs with no daemon, and every test runs in the suite's own
  `$HOME` and local git server with no network.
- **R21. niwa never undoes borrowing by itself.** niwa runs no `git repack
  -a -d`, `--dissociate` or collection in a borrower except through R8, and
  sets no maintenance that would.

## Acceptance Criteria

- [ ] On a fresh sandbox host, `niwa create` of a three-repo workspace seeds
  a mirror for each repo and each clone's
  `.git/objects/info/alternates` is exactly `<stable path>/objects`, with
  the stable path under `~/.niwa/w-<key>/mirrors/`. (R1, R2, R3)
- [ ] A second `niwa create` of the same workspace writes less than two
  thirds of what the first did, read from volume free space before and after
  each. (R18)
- [ ] A commit made in one instance is absent from the other; after it is
  pushed and the other instance fetches, the commit is present there.
  (R1, R3)
- [ ] With the mirror for one repo deleted and its remote unreachable, a
  create still finishes, clones that repo in full with no alternates file,
  and its output names the repo and the reason. (R4)
- [ ] A borrowing clone made to fail after the clone started leaves no
  partial clone, and the create's retry or fallback produces a full clone.
  (R4)
- [ ] An instance created with borrowing off, then synced and applied after
  borrowing is turned on, still has no alternates file. (R5)
- [ ] With the workspace setting off, a create makes full clones, writes no
  alternates file and creates no `mirrors/` entry for any repo. (R6)
- [ ] With the workspace setting on and one repo's setting off, that repo
  gets a full clone, no alternates file and no mirror under `~/.niwa/`,
  while the others borrow. (R6)
- [ ] With an instance moved under the workspace's `kept/` directory, a
  restore's probe reports on that instance's clones, and the old store a
  kept clone needs is not removed. (R7, R9)
- [ ] After the make-independent command runs on an instance and the
  mirror is deleted, `git log --all` and `git fsck` in each of its clones
  and their worktrees come back clean, and none has an alternates file.
  Running the command again changes nothing and says so. (R8)
- [ ] The make-independent command run with too little free space for the
  copy changes nothing and says why. (R8)
- [ ] With enumeration of instances made to fail, a restore removes no old
  store. (R9)
- [ ] A clone whose alternates names a store directory directly keeps that
  store through a restore. (R9)
- [ ] Destroying a workspace with no kept instances removes its `mirrors/`
  directory; with a kept borrowing instance it keeps it and says so. (R10)
- [ ] The user guide has a section naming `~/.niwa/w-<key>/mirrors/`, the
  four purge steps in the R11 order with the reason for the order, the off
  setting at both levels and the make-independent command. (R11)
- [ ] A functional scenario sets `GIT_SSH_COMMAND` to a wrapper that the
  sandbox's remote requires, deletes a borrower's mirror, and sees the next
  command restore it and the wrapper invoked; on niwa before the niwa#524
  fix the same scenario reports the restore failed. (R12)
- [ ] A functional scenario truncates an old store's pack holding objects a
  borrower needs and the remote no longer serves, triggers a restore, and
  sees the borrower's ref read to the root afterwards. (R13)
- [ ] A functional scenario changes a repo's URL from its SSH form to its
  HTTPS form for the same sandbox remote, then damages the mirror, and sees
  the restore salvage the old store rather than rebuild with no salvage.
  (R14)
- [ ] Every mirror-repair proof scenario runs against borrowers made by
  `niwa create` in the functional suite, with no step that writes an
  alternates file by hand, and passes in CI on Ubuntu and macOS. (R15)
- [ ] Before the turn-on change, a released build with no workspace or repo
  setting, and one with the setting set to on, both make full clones and no
  mirror. (R16)
- [ ] The turn-on change is a separate change from every other one in this
  work, merged after the R12, R13 and R15 changes, and after it a create
  with no setting borrows wherever a workspace root exists, while an
  instance with no workspace root makes full clones. (R17)
- [ ] A create with a healthy mirror makes no network call beyond the
  repo's own fetch of missing objects, and the mirror check's logged
  duration stays under 100 ms. (R19)
- [ ] The functional scenarios for this work run on Ubuntu and macOS under
  the suite's sentinels and write nothing outside the sandbox `$HOME`. (R20)
- [ ] No niwa command other than the make-independent command leaves a
  borrower with its borrowed objects copied in; a borrower's config after
  create sets no `gc` or maintenance key. (R21)

## Out of Scope

- **A copy-on-write seed instance.** Held pending a measurement; nothing
  here builds or prepares for it.
- **The shared plugin store.** It shipped separately; this work shares its
  host directory and nothing else.
- **Changes to how the mirror detects and restores damage** beyond R12, R13
  and R14. The mirror-repair contract is settled, and this work calls it.
- **Pruning or collecting the mirror.** It never prunes, by the retention
  decision, and refs it keeps are never trimmed.
- **Other ways to share git state:** one shared repository with worktrees,
  shallow or partial clones, or creating an instance with only some of its
  repos.
- **Converting existing full clones into borrowers.** Instances made before
  borrowing is on keep their full clones (R5).
- **Single-instance layouts with no workspace root.** They keep full clones.
- **A daemon, scheduler or background job.**

## Known Limitations

- A mirror deleted between commands leaves its borrowers broken until the
  next niwa command, the window the mirror-repair design already accepted.
- A local branch on pushed commits that the remote has destroyed is lost
  when the whole mirror is lost; niwa names it unrecoverable. R13 and R14
  narrow when that happens; they don't remove it.
- A person running `gc --prune=now` or `git repack -a -d` in the mirror
  by hand can break borrowers; the mirror's own config guards the common
  case.
- Moving a workspace root changes its host key, so new creates seed new
  mirrors while existing instances keep reading the old ones until those
  are deleted.
- A user who runs `git repack -a -d` in a borrower makes that clone
  independent and gives up its saving; nothing breaks.

## Decisions and Trade-offs

- **niwa#524 is fixed in this work and gates the turn-on.** It is the
  condition the turn-on rests on, alongside R15, so it is a requirement
  (R12) with its own scenario rather than a dependency on outside work.
- **niwa#525 is fixed in this work and also lands before the turn-on.** It
  turns a recoverable partial damage into lost objects only once real
  borrowers depend on the mirror, the fix is small, and leaving it as a
  stated limit would widen the loss case on the day borrowing reaches
  users. Alternative: a named limit, rejected for that reason.
- **niwa#522 is fixed in this work but doesn't gate the turn-on.** It needs
  both a respelled remote URL and a remote that has since destroyed
  commits, so it narrows a rare case; the comparison change is cheap. If
  it slips, it stays a named limit and the turn-on doesn't wait on it.
- **The turn-on is its own last change.** Before it, no user can turn
  borrowing on (R16), so there is no opt-in period and the condition can't
  be bypassed by a setting. Alternative: an opt-in setting for early users,
  rejected here because it would put borrowing in users' hands before the
  condition holds.
- **The first create seeds the mirror (R2).** Otherwise the second create
  would pay for seeding and the saving would start at the third.
- **Only new clones borrow (R5).** Converting existing clones would rewrite
  instances' object stores outside R8, which the mirror-repair contract
  forbids.
- **The off setting has a per-repo form (R6)** because the retention
  decision accepted the mirror's risk on that condition.
