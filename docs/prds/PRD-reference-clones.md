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
  users only after niwa#524 is fixed with a covering scenario and the repair
  proofs pass against borrowers the real create made (R16, R17).

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
  clones that repo into an instance (`niwa create`, `niwa apply` after a repo
  is added to the config, `niwa dispatch`, the SessionStart hook's
  provisioning, and `niwa init` when it creates an instance) clones it
  borrowing from that repo's mirror. The mirror-repair check, and any
  restore it triggers, runs first; the clone borrows only when the check
  reports the mirror usable. The new clone holds only objects the mirror
  lacks.
- **R2. First mirror.** When a repo has no mirror yet, the create seeds one
  through the mirror-repair feature's seeding entry point and then borrows
  from it, so the first instance's create already builds what later
  instances share.
- **R3. Alternates name the stable path, and nothing else changes.** A
  borrowing clone's alternates file holds exactly one line, the repo's
  stable mirror path followed by `/objects`, never a resolved store path.
  (A clone whose alternates names a store directly exists only by hand
  editing; R9 still protects it.) Apart from that file and the objects it
  lets the clone omit, the clone is
  what a full clone would be: the same `origin` URL, refs, config keys and
  hooks.
- **R4. Fallback.** When the mirror is not usable for a repo at create time
  (missing and seeding failed, restore failed, busy past the mirror-repair
  lock wait of five minutes, or ten seconds in the SessionStart hook), or a
  borrowing clone fails after it started, the create removes any partial
  clone, clones that repo in full as niwa does today, finishes, and its
  output names the repo and the reason it didn't borrow. Borrowing never
  makes a create fail that would have succeeded without it.
- **R5. Only new clones borrow.** A repo already cloned in an instance is
  never converted to a borrower or back by sync, apply or any other
  command except the one in R8.
- **R6. Off setting, workspace and repo.** A workspace setting turns
  borrowing off for the whole workspace, and a per-repo setting turns it off
  or on for one repo; the repo's value wins over the workspace's, in both
  directions. A repo with borrowing off gets a full clone and no alternates
  entry, and niwa seeds no mirror for it. The setting governs clones made
  after it changes. A repo turned off while some instance still borrows from
  its mirror keeps that mirror checked and restored until no instance on
  disk names it; the guide (R11) tells the user to make those instances
  independent (R8) and then delete the mirror.
- **R7. Kept instances count.** Every mirror check, restore and probe, and
  every decision to remove a mirror store, considers every instance on disk
  for the workspace: those under the workspace root and those kept under
  the workspace's host directory, half-created ones (a directory with no
  instance state file) included.
- **R8. Make an instance independent.** A user command makes a named
  instance (by default the current one) independent of the host's stores:
  it copies every borrowed object into each of the instance's clones, then
  removes their alternates files. Before it changes anything it takes the
  instance's lock (refusing, with nothing changed, when another command
  holds it) and checks that free space covers the mirror's pack sizes for
  the instance's repos (refusing, with nothing changed, when it doesn't). A
  clone's alternates file is removed only after its copy has finished and
  `git fsck` passes with the mirror unreachable; a failure part way leaves
  every clone still borrowing and readable. It covers worktrees of those
  clones, does nothing to a clone that doesn't borrow, and reports each
  clone it changed. Afterwards the instance reads its whole history with
  the mirror deleted.
- **R9. Generations are never retired while named.** A mirror generation
  (one store directory behind a repo's stable path) is replaced by a newer
  one only through the mirror-repair restore or its rebuild for a changed
  remote. niwa removes an old generation only after it has enumerated every
  instance on disk per R7 without error, and never removes a store that any
  clone's alternates names, directly or through the stable path. A failed
  enumeration removes nothing.
- **R10. Workspace destroy.** Destroying a workspace removes its mirrors
  when no instance on disk, kept ones included, still borrows from them, and
  keeps them, saying so, when one does.
- **R11. Guide.** A user guide section says where a workspace's mirrors live
  (`~/.niwa/w-<key>/mirrors/`) and gives the purge steps for a commit that
  must not stay on the host, in this order: rotate the secret; in each
  borrowing instance, rewrite or delete any local branch that reaches the
  commit and run `git fetch --prune`; delete the repo's stable path and its
  `.store/<repo-id>-*` directories; ask the forge to drop the commit. It
  says why the order matters, and it documents the off setting at both
  levels and the R8 command.
- **R12. Remote access variables reach the mirror's network calls
  (niwa#524).** The mirror's seed, refresh, restore and fetch-by-id keep the
  user's inherited `GIT_SSH`, `GIT_SSH_COMMAND`, `GIT_SSH_VARIANT`,
  `GIT_ASKPASS`, `GIT_PROXY_COMMAND`, `GIT_SSL_CAINFO`, `GIT_SSL_CAPATH`,
  `GIT_SSL_NO_VERIFY`, `GIT_HTTP_PROXY_AUTHMETHOD`, and `GIT_CONFIG_COUNT`
  with its `GIT_CONFIG_KEY_<n>` and `GIT_CONFIG_VALUE_<n>` entries. Local
  reads of the mirror keep stripping every inherited `GIT_*` variable, as
  the mirror-repair design requires.
- **R13. Old stores outlive borrower recovery (niwa#525).** A restore
  removes no old store until the borrower probe and recovery by id have
  finished. From a truncated old pack it salvages every object that can
  still be read and verified, so a borrower ref whose objects all survive
  reads to the root afterwards, and a ref that needs an object neither the
  salvage nor the remote can supply is reported unrecoverable by name.
- **R14. A different spelling of the same remote is the same remote
  (niwa#522).** A URL change between a repo's SSH form
  (`git@host:owner/repo.git`) and HTTPS form (`https://host/owner/repo.git`),
  or in the user part alone, is treated as the same remote: the restore
  salvages old stores as for any other fault. A change of host or path
  still rebuilds with no salvage, as today.
- **R15. Repair proofs against real borrowers.** Every scenario in the
  mirror-repair feature file (the deleted mirror, damaged mirror and
  deleted remote commits proofs, and the refresh, concurrency,
  failed-restore and healthy-check scenarios, the last with its 100 ms
  bound) runs against borrowers made by `niwa create`, with no step that
  writes an alternates file by hand, in the functional suite on Linux and
  macOS, and passes.
- **R16. Off for users until the turn-on.** Until the R17 change lands,
  which is after the R12 fix with its scenario, the R13 fix and the R15
  rerun, a niwa built for release borrows for no user: the borrowing create runs only in the functional
  suite's test build, and no setting a user can write turns it on. The R6
  setting may exist before then; setting it to on does nothing until R17.
- **R17. Turn on by default, last.** Borrowing becomes on by default
  wherever a workspace root exists, as its own change, merged only after
  the changes for R12 (with its scenario), R13 and R15. A layout with no
  workspace root keeps full clones.
- **R18. Concurrent first creates.** Several creates of the same workspace
  started together on a host with no mirror yet produce one mirror per
  repo, and every clone they make either borrows from it or, per R4, is a
  full clone.

### Non-functional

- **R19. Saving.** On a multi-repo workspace, the second create writes less
  than two thirds of what the first did, read from volume free space before
  and after each, and the second instance's clones hold no pack the mirror
  already holds.
- **R20. Platforms and isolation.** Everything works on Linux and macOS with
  the git version niwa already requires, no git extension, and no reflink or snapshot support, runs inside commands niwa
  already performs with no daemon, and every test runs in the suite's own
  `$HOME` and local git server with no network.
- **R21. niwa never undoes borrowing by itself.** No niwa command but the
  R8 command copies a borrower's borrowed objects into it, and niwa sets no
  `gc.*` or `maintenance.*` key in a borrower.

## Acceptance Criteria

- [ ] On a fresh sandbox host, `niwa create` of a three-repo workspace seeds
  a mirror for each repo under `~/.niwa/w-<key>/mirrors/`, and each clone's
  `.git/objects/info/alternates` contains exactly one line,
  `<stable path>/objects`, where the stable path is the symlink and not its
  target. (R1, R2, R3)
- [ ] The same alternates check passes for a repo cloned by each of
  `niwa apply` after the repo is added to the config, `niwa dispatch`, the
  SessionStart hook's provisioning and `niwa init`. (R1)
- [ ] With a repo's mirror deleted before a second `niwa create`, the
  create's output and the mirror log show a restore before the clone, and
  the new clone borrows from the restored mirror. (R1)
- [ ] A borrowing clone and a full clone of the same repo at the same commit
  have the same `origin` URL, the same `git for-each-ref` output and the
  same `git config --local --list` output, and neither has a hook file the
  other lacks. (R3)
- [ ] With one repo's mirror deleted and the sandbox remote for that repo
  stopped, a create finishes with exit 0, clones the other repos borrowing,
  and its output names the repo it cloned in full and why; that clone has no
  alternates file. (R4)
- [ ] With another process holding a repo's mirror lock for longer than the
  lock wait (shortened by the test build), a create falls back to a full
  clone of that repo and names it; the SessionStart hook's provisioning does
  the same after its ten-second wait. (R4)
- [ ] With the test build's fault hook making a borrowing clone fail after
  it started, the create still finishes, the repo's clone is a full clone
  with no alternates file, and no partial directory is left beside it. (R4)
- [ ] An instance created with borrowing off, then synced and applied with
  borrowing on, still has no alternates file in any clone. (R5)
- [ ] With the workspace setting off, a create makes full clones, writes no
  alternates file and creates no `mirrors/` entry. (R6)
- [ ] With the workspace setting off and one repo's setting on, that repo
  borrows and the others are full clones; with the workspace setting on and
  one repo's setting off, that repo is a full clone with no mirror and the
  others borrow. (R6)
- [ ] With a repo turned off after an instance borrowed from it, deleting
  that repo's mirror and running `niwa apply` restores it; after the
  instance is made independent, the next command neither checks nor restores
  that mirror. (R6, R8)
- [ ] With a borrowing instance moved under the workspace's `kept/`
  directory and a half-created instance directory beside the others, a
  restore's probe lists both instances' clones in the mirror log. (R7)
- [ ] After the make-independent command runs on an instance and the
  mirror is deleted, `git log --all` and `git fsck` in each of its clones
  and a worktree of one of them exit 0, and no clone has an alternates file;
  a second run reports nothing to change. Run from inside an instance with
  no instance named, the command acts on that instance and lists each clone
  it changed. (R8)
- [ ] The make-independent command refuses and changes nothing when another
  command holds the instance's lock, and when free space is below the
  mirror's pack sizes for the instance's repos. (R8)
- [ ] With the test build's fault hook failing the copy for the second of
  three clones, every clone still has its alternates file and passes
  `git fsck` with the mirror present. (R8)
- [ ] With the `kept/` directory made unreadable, a restore removes no old
  store. (R9)
- [ ] A clone whose alternates names a `.store/` directory directly keeps
  that store through a restore and through a rebuild for a changed remote.
  (R9)
- [ ] Destroying a workspace with no kept instances removes its `mirrors/`
  directory; with a kept borrowing instance it keeps it and says so. (R10)
- [ ] The user guide has a section naming `~/.niwa/w-<key>/mirrors/`, the
  four purge steps in the R11 order with the reason for the order, the off
  setting at both levels and the make-independent command. (R11)
- [ ] A functional scenario puts the sandbox remote behind an SSH-style
  wrapper reached only through `GIT_SSH_COMMAND`, creates a borrowing
  instance, deletes the mirror, and sees the next command restore it with
  the wrapper invoked. (R12)
- [ ] With each variable R12 lists set in niwa's environment, a test-build
  probe of the environment the mirror's seed, refresh and restore pass to
  git shows each of them with its value, and shows no other inherited
  `GIT_*` variable. (R12)
- [ ] With `GIT_DIR` and `GIT_ALTERNATE_OBJECT_DIRECTORIES` set to unrelated
  paths in the environment, the healthy check of a borrower's mirror still
  reports healthy, and the mirror's local git calls run with neither
  variable set. (R12)
- [ ] A functional scenario truncates an old store's pack halfway, where the
  first half holds every object one borrower ref needs and the second half
  an object another ref needs, with the remote no longer serving either;
  after the restore the first ref reads to the root and the second is
  reported unrecoverable by name. In the mirror log for that restore, the
  probe's lines come before the line removing the old store. (R13)
- [ ] A scenario changes a repo's URL from its SSH form to its HTTPS form
  for the same sandbox remote, damages the mirror, and sees the restore
  salvage the old store; a scenario that points the repo at a different
  sandbox remote sees a rebuild with no salvage. (R14)
- [ ] Every scenario in the mirror-repair feature file runs against
  borrowers made by `niwa create`, no step writes an alternates file by
  hand, and the feature passes in CI on Ubuntu and macOS. (R15)
- [ ] Before the turn-on change, a test of the release build configuration
  (built without the test build's tag) shows the borrowing create disabled
  both with no setting and with the setting set to on, so a create makes
  full clones and no mirror. (R16)
- [ ] After the turn-on change, a create with no setting borrows wherever a
  workspace root exists, and an instance with no workspace root makes full
  clones. The merge history shows the turn-on change merged after the
  changes for R12, R13 and R15. (R17)
- [ ] Three `niwa create` runs of the same workspace started together on a
  host with no mirror produce one stable path and one store per repo, and
  every clone either borrows from it or is a full clone named in that
  create's output. (R18)
- [ ] By hand, on a real multi-repo workspace and host, a reviewer reads
  volume free space before and after two creates and sees the second write
  less than two thirds of the first; in the functional suite, the second
  instance's clones hold no pack file whose name a mirror store holds. (R19)
- [ ] The functional scenarios for this work run on Ubuntu and macOS under
  the suite's sentinels and write nothing outside the sandbox `$HOME`. (R20)
- [ ] After create, sync, apply and a restore, each borrower holds no pack
  file whose name a mirror store holds, and `git config --local --list` in
  it shows no `gc.` or `maintenance.` key. (R21)

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
- **Failures are injected through the test build.** The R4 and R8
  failure-part-way criteria rely on a fault hook present only in the
  functional suite's test build, the same build R16 lets borrow before the
  turn-on; a release build carries neither.
- **The saving is checked two ways (R19).** Volume free space is the figure
  a user feels but is too noisy for CI, so the suite checks that a borrower
  holds none of the mirror's packs, and the free-space figure is a reviewer's
  hand check.
- **Names are the design's.** The off setting's keys, the make-independent
  command's name and the fault hook's shape are settled in the design; the
  requirements fix only their behaviour.
- **The merge order is checked from history (R17).** The turn-on is a
  release decision, so its criterion reads the merge order rather than a
  test; that is the check the rollout condition needs.
