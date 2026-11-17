---
schema: prd/v1
status: Accepted
problem: |
  Every niwa create downloads each plugin marketplace's release and unpacks a
  full copy of it inside the new instance, even when another instance of the
  same workspace on the host already holds that exact release. A release tag's
  content never changes, so the copies are identical, and a person who runs
  more than one instance pays disk, network and create time again for every
  instance after the first.
goals: |
  From the second instance of a workspace on a host, a create downloads and
  writes no plugin release the host already holds, so it finishes sooner and
  leaves more disk free. Every instance still loads its skills, a release that
  goes missing from the host is fetched again by niwa with nobody acting, and a
  workspace can switch the shared copy off and get today's behaviour back.
upstream: docs/briefs/BRIEF-plugin-release-hold.md
motivating_context: |
  A November 2026 survey of one maintainer's host, taken with `du` over each
  instance and a file-by-file comparison against release tags, found about
  58 MiB of plugin trees in every instance, byte-identical for the same tag,
  adding up to about 7.5 GiB across 134 instances where one copy per release
  would have taken about 350 MiB.
---

# PRD: Plugin releases held once per host

## Status

Accepted

## Problem Statement

niwa delivers skills to an instance from its own copy of each plugin
marketplace the workspace declares. For a marketplace fetched from a release,
that copy lives inside the instance today: every `niwa create`, every
`niwa dispatch` and every instance the SessionStart hook provisions downloads
the release tarball and unpacks a full tree under the instance's
`.niwa/marketplaces/`. Nothing checks whether the host already has that
release.

It almost always does. A release tag's content never changes, so the tree for
a given tag is byte-identical in every instance that pulled it. On a host used
for parallel agent work the duplication is the normal case. Measured across the
134 instances on one such host in November 2026, each instance carried about
58 MiB of plugin trees, and together they held about 7.5 GiB where one copy per
release would have taken about 350 MiB. A coordinator's burst of creates
downloads the same tarballs once per worker.

The cost lands on the person creating instances: a slower create, a fuller
disk, and network traffic for bytes already on the machine. Someone running a
single instance pays nothing extra, but anyone running two or more pays again
for every one after the first.

## Goals

- A person who runs more than one instance of a workspace on a host stops
  paying twice for the same plugin release. From the second instance on, a
  create downloads and writes nothing for releases the host already holds, and
  takes no new disk for plugin trees.
- Nothing else about an instance changes for them. Its skills load as before,
  and it stays on the release it was created with.
- If a held release disappears from the host, they don't have to notice or fix
  anything: niwa fetches it again during an ordinary command, and instances
  already using it keep working.
- A workspace can turn the shared copy off and get a full copy per instance
  again, and when the shared copy can't be used a create still succeeds the old
  way and says it fell back.
- The output is a faster create and more free disk, read by the person who ran
  the create or the dispatch. Nothing is produced for anyone else.

## User Stories

- **A second instance on a busy host.** As a developer with one instance of a
  workspace that declares two plugin marketplaces, I want my next `niwa create`
  (or a coordinator's dispatch that creates a worker) to reuse the releases
  the host already holds, so that the create downloads neither release and the
  new instance's skills work exactly as in the first.
- **A held release goes missing.** As a developer who deleted a release from
  the host's store to free disk (or lost it to a disk problem), I want the next
  ordinary niwa command, such as `niwa apply` or a new create, to notice and
  fetch it again, so that an instance created before the deletion loads its
  skills without my running any repair step.
- **Opting a workspace out.** As a developer whose instances of one workspace
  must be self-contained because they get moved or archived whole, I want to
  turn the shared copy off in that workspace's settings, so that each new
  instance holds its own full copy while my other workspaces keep sharing.
- **The store can't be used.** As a coordinator dispatching a worker while the
  host's store is unreadable or unwritable, I want the worker's create to fetch
  the releases into the instance the old way and say in its output that it fell
  back and why, so that I know the create cost the full price.
- **A burst of workers.** As a coordinator who dispatches several workers of
  one workspace at once, I want the burst to download each release once rather
  than once per worker, and no worker to start on a half-written release, so
  that a burst costs about what one create does for plugins.

## Requirements

Each requirement names the acceptance scenario it serves: the second-create
scenario, the deleted-release scenario, or the store-off and unreachable
scenario. A few serve the constraints around all three.

### Terms

- **Held release**: a release niwa has finished placing in the workspace's
  store and marked complete.
- **Missing release**: a release an instance needs whose directory is absent
  from the store, or present but not marked complete (an interrupted, partial
  or failed placement). Damage to the files of a release already marked
  complete isn't covered; see Known Limitations.
- **In use**: a release is in use when any instance directory on disk under the
  workspace root points at it, whatever that instance's state (running, idle,
  finished or kept).
- **Provisioning commands**: the commands that run niwa's provisioning steps
  for an instance. Today these are exactly `niwa create`, `niwa apply`,
  `niwa dispatch`, `niwa reset`, `niwa init`, the SessionStart hook's
  `niwa instance from-hook`, `niwa watch` when it provisions an instance, and
  `niwa resume` when it restores a removed instance. No other command fetches
  releases; `niwa status`, `niwa list`, `niwa go` and the `niwa worktree`
  commands never do.

### Functional

**R1. One held copy per release, per host and workspace.** When an instance
belongs to a workspace that has a workspace root and the store is on, each
plugin release niwa fetches for skill delivery is kept once in that
workspace's store on the host, and every instance of the workspace that uses
that release uses the held copy instead of a copy of its own. Serves the
second-create scenario.

**R2. A held release is neither downloaded nor written again.** A create whose
plugin releases are all already held downloads none of them and writes no copy
of any release tree into the new instance. A release not yet held is fetched
once, into the store, and then used from there. Serves the second-create
scenario.

**R3. Skills load from the held copy as they do today.** Every place niwa
delivers skills from a release tree today (the instance root, each repo in the
instance, and session worktrees made in the instance) works from the held copy,
and a session sees the same skills it would have seen with a per-instance copy.
Serves the second-create scenario and the deleted-release scenario.

**R4. It rides the creates niwa already performs.** The store is used by the
create that `niwa create`, `niwa dispatch` and the SessionStart hook already
perform, and by the other provisioning commands when they create an instance
(`niwa reset`, `niwa init`, `niwa watch` and `niwa resume`). It needs no new command, no new flag, no background process and no
scheduler. Serves the second-create scenario.

**R5. On by default wherever a workspace root exists.** The store is on for
every workspace that has a workspace root, with no change to the workspace's
configuration needed to enable it. Serves the second-create scenario and the
store-off and unreachable scenario.

**R6. A missing release is fetched again by niwa, with nobody acting.** A
release missing from the host's store is fetched again by niwa with nobody
acting; it can never be a command the user runs by hand. Every provisioning
command (the closed list under Terms) checks the releases in use by the
workspace's instances on disk and fetches any missing one back into the store,
whichever instance the command was run for. Serves the deleted-release
scenario.

**R7. Instances already using a missing release recover.** Once niwa has
fetched a missing release again, an instance created before the release went
missing loads its skills from it, with nothing run inside that instance by the
user. Serves the deleted-release scenario.

**R8. A workspace setting turns the store off.** A setting in the workspace's
configuration turns the store off for that workspace. With it off, the next
create of that workspace writes its own full copy of each release into the
instance, as niwa does today, while other workspaces on the host keep sharing.
Turning it off doesn't break instances that already use the held copy. Serves
the store-off and unreachable scenario.

**R9. An unusable store falls back to today's fetch, and says so.** The store
is unusable when its directory can't be read, can't be written, can't be
created, or a release can't be placed in it completely. In each of those cases
a create doesn't fail on that account: it fetches the releases into the
instance the old way, and its output, including the output a dispatch shows the
person who dispatched, says that it fell back, gives the store's path, and
names the condition (unreadable, unwritable, can't be created, or release not
placed). Serves the store-off and unreachable scenario.

**R10. A failed refetch doesn't fail the command, and a bad download never
becomes held.** If a missing release can't be fetched again (its source is
unreachable, or the download is cut short or corrupt), the command that tried
still completes its other work, its output names the release it couldn't
restore, and nothing it partly wrote is marked held. The next provisioning
command tries again. Serves the deleted-release scenario.

**R11. A release in use is never deleted.** niwa never deletes a held release
that is in use, kept instances included. When and how niwa may delete a
release nobody uses is left to the design (see Decisions and Trade-offs).
Serves the deleted-release scenario by keeping niwa itself from causing it.

**R12. Moving refs keep working.** A marketplace tracked at a moving ref (a
branch or the default branch rather than a release tag) gives an instance,
for the commit the ref resolved to at create time, file-for-file the same
content a per-instance copy fetched at that commit would hold. Whether such
content is held, and how it's named, is left to the design. Serves the
second-create scenario.

**R13. An instance stays on its release.** An instance keeps the release it was
created with for as long as it exists, as today. Holding releases once doesn't
move an existing instance to a newer release. Serves the deleted-release
scenario.

**R14. Single-instance layouts keep today's behaviour.** An instance with no
workspace root above it keeps its own per-instance copy, exactly as today.
Serves the store-off and unreachable scenario as its baseline.

**R15. A release that isn't held and can't be fetched behaves as today.** When
a create needs a release that isn't held and its source is unreachable, the
create does what it does today: it completes, reports the marketplace it
couldn't fetch, and doesn't deliver that marketplace's skills until a later
provisioning command fetches it. Nothing is marked held for it. Serves the
store-off and unreachable scenario.

### Non-functional

**R16. The store lives where host-level state lives.** This PRD applies the
rule for host-level state and does not reopen it: host-level state that niwa
owns, and that is large or must keep its identity across a config refresh,
lives under ~/.niwa/, in a directory keyed by the workspace root's canonical
path; it never lives under the workspace root's .niwa/ or inside an instance.
The plugin store is the first thing placed under that rule. Serves all three
scenarios.

The later git-history store (reference clones and the host mirror they borrow
from) uses the same rule, and can rely on this feature for three things: the
location rule and the store's directory under it, the lock discipline that
keeps concurrent creates safe, and the missing-item refetch pattern of R6. The
design settles the details of each.

**R17. Concurrent creates are safe.** When several creates of a workspace
overlap, as in a coordinator's burst of dispatches, no instance ever uses a
release that isn't held, and a release missing at the start of the burst is
downloaded once rather than once per create. Serves the second-create
scenario.

**R18. No release tree inside a sharing instance.** On a filesystem that
supports symbolic links, an instance that uses held releases contains no file
of any release tree, only links to the held copies, and the plugin content
under the instance's `.niwa/` measures under 1 MiB with `du`. Serves the
second-create scenario.

Create time should drop by about the time the downloads and extraction took.
That is informational, not a requirement: it was estimated at a few seconds
but never measured on a real instance.

## Acceptance Criteria

### The second-create scenario

A reviewer re-runs it this way: on a clean host (no held releases and no niwa
host state for the workspace), take a workspace with a workspace root that
declares two plugin marketplaces fetched from releases. For the steps that need
a newer release later, make at least one of the two a marketplace repository
the reviewer controls (a fork of a real one is enough), so they can push a new
`vX.Y.Z` tag when a step calls for it. Create one instance, then a second,
watching the network during each create (for example through a proxy or a
packet monitor) for release downloads from the marketplaces' source. Then
start a session in each instance and invoke a skill from each marketplace.

- [ ] The first create downloads each of the two releases once. (R1, R2)
- [ ] The second create downloads neither release. (R2)
- [ ] In the second instance, a listing of its plugin content shows only
      symbolic links that resolve into the store and no regular file from
      either release tree, and `du` over its `.niwa/` plugin content reports
      under 1 MiB. (R1, R18)
- [ ] A skill from each marketplace works in both instances, at the instance
      root, in a repo of the instance, and in a session worktree. (R3)
- [ ] The reviewer changed nothing in the workspace's configuration and passed
      no new flag to get this result. (R4, R5)
- [ ] The same three checks (no download, links only, skills work) hold when
      the second instance is made by `niwa dispatch`, and again when it is made
      by the SessionStart hook, instead of `niwa create`. (R4)
- [ ] With neither release held, three creates launched concurrently from
      separate shells, each started before any of them has finished
      downloading, download each release once in total, and every resulting
      instance loads its skills. (R17)
- [ ] A create interrupted (killed) while it's downloading a release that isn't
      held leaves that release not marked held; a following create fetches it
      again, and no instance links to the interrupted copy. (R10, R17)
- [ ] For a marketplace tracked at a moving ref, an instance created with the
      store on and one created with the store off, both while the ref resolves
      to the same commit, hold file-for-file identical plugin content
      (compared with `diff -r` over the resolved trees). (R12)

### The deleted-release scenario

A reviewer re-runs it this way: after the second-create scenario, delete one
of the two releases from the host's store by hand. Without running any repair
or cleanup command, run one provisioning command, then start a session in the
first instance and invoke a skill from that marketplace. Repeat the deletion
before each of the command variants below. To get a newer release, push a new
tag to the marketplace repository the reviewer controls.

- [ ] `niwa apply` in either instance fetches the deleted release again from
      its source, and its output says it restored that release. (R6)
- [ ] The same holds when the one command is `niwa dispatch` of a new worker,
      and when it is the SessionStart hook provisioning a new instance. (R6)
- [ ] The release comes back when the command is a new `niwa create` whose own
      instance uses a newer release (after a new tag is pushed), not only when
      it is run for an instance that uses the deleted one. (R6)
- [ ] After each of those commands, the instance created before the deletion
      loads that marketplace's skills, with nothing run inside it by the
      reviewer. (R7)
- [ ] Deleting only the release's completion mark (leaving its files), then
      running `niwa apply`, also makes niwa fetch it again. (R6)
- [ ] `niwa --help` and the help output of every subcommand it lists show no
      command that repairs, rebuilds, verifies or cleans the store, and the
      user guides under `docs/guides/` describe none. (R6)
- [ ] With the release's source unreachable during the command, the command
      completes, its output names the release it couldn't restore, and a
      later `niwa apply` with the source reachable restores it. (R10)
- [ ] With a download made to fail partway (for example by cutting the network
      mid-transfer), the command names the release it couldn't restore and the
      store holds no complete-marked copy of it afterwards. (R10)
- [ ] After a new tag is pushed and `niwa apply` runs in the first instance,
      that instance's links still resolve to the release it was created with.
      (R13)
- [ ] Keep one instance on disk (finished, with no session running) that uses
      release A while the other instances move to a newer release B. Run
      `niwa create`, `niwa apply`, `niwa dispatch`, the SessionStart hook,
      `niwa reset` and `niwa destroy` on the other instances, and any store
      cleanup the design adds, one after another. After each one, release A is
      present in the store and marked complete, and the kept instance's links
      resolve. (R11)

### The store-off and unreachable scenario

A reviewer re-runs it this way: on one host, set up two workspaces, each with
a workspace root and one instance already created with the store on. Turn the
store off in the first workspace's configuration and create an instance of it,
then create an instance of the second workspace. Next, with the store on in
both, make the first workspace's store unusable in each of the ways listed
below, restoring it between attempts, and create an instance each time,
reading the create's output.

- [ ] With the store off, the new instance of the first workspace holds its own
      full copy of each release, as before this change. (R8)
- [ ] The new instance of the second workspace downloads no release and links
      to that workspace's held copies. (R8)
- [ ] The first workspace's instance created before the store was turned off
      still loads its skills. (R8)
- [ ] With the store directory unreadable (mode `000`), the create succeeds,
      the instance holds its own copy of each release, and the output says it
      fell back, gives the store's path, and names the condition
      "unreadable". (R9)
- [ ] The same with the store directory unwritable (mode `555`) and a release
      not yet held: the output names "unwritable". (R9)
- [ ] The same with the store directory absent and its parent under `~/.niwa/`
      unwritable: the output names "can't be created". (R9)
- [ ] The same when placing a release in the store fails partway (for example
      the disk fills, simulated by a size-limited volume): the output names
      "release not placed", and the store holds no complete-marked copy of it.
      (R9, R10)
- [ ] The same fallback message, with path and condition, appears in the output
      of a `niwa dispatch` whose create fell back. (R9)
- [ ] Three creates launched concurrently while the store is unwritable each
      succeed, each holds its own copy, and each output carries the fallback
      message. (R9, R17)
- [ ] A create that needs a release not yet held, with the release's source
      unreachable, completes, reports the marketplace it couldn't fetch, delivers
      none of that marketplace's skills, and leaves nothing marked held for it;
      a later `niwa apply` with the source reachable fetches it into the store.
      (R15)
- [ ] A single-instance layout with no workspace root holds its own copy and
      creates nothing under `~/.niwa/` for plugins, whether or not the setting
      is present. (R14)

### Location

- [ ] The store sits under `~/.niwa/`, in a directory keyed by the workspace
      root's canonical path, and no release file sits under the workspace
      root's `.niwa/` or inside any instance. (R16)
- [ ] Two workspace roots on the same host get separate store directories. (R16)
- [ ] Reaching the same workspace root through a symbolic link to it, a create
      uses the same store directory as through its real path. (R16)

## Out of Scope

- **Reference clones, the host mirror and a borrowing create.** Sharing git
  history between instances is separate work. It follows the same location rule
  and can reuse this feature's lock and refetch patterns, but not its store.
- **A copy-on-write seed of a whole instance.** It is held and is not part of
  this work.
- **Claude Code's own plugin cache under `~/.claude/plugins`.** Claude Code
  fetches marketplaces again into its own directory; this feature changes only
  the copy niwa keeps for skill delivery.
- **Single-instance layouts with no workspace root.** They keep today's
  per-instance copy (R14).
- **A user-run repair or cleanup command for the store.** Repair is niwa's job,
  done on its own (R6).
- **Changing which release a long-lived instance uses.** An instance stays on
  the release it was created with (R13).
- **Marketplaces niwa keeps as managed repository copies at the workspace
  root.** Those already sit once per workspace, and how long they're kept isn't
  changed here.

## Known Limitations

- The store is per user and per host. Two users on one host, or one user on two
  hosts, each hold their own copy.
- If the home directory is on a different volume from the workspace, nothing
  here suffers, since instances only point at the held copy; it matters for a
  later copy-on-write seed, not for this feature.
- An instance moved to another host, or archived and restored, points at a
  store that isn't there. Its skills come back on the next ordinary command
  that refetches (R6), or the workspace can turn the store off before the move
  (R8).
- Removing a workspace root by hand leaves its store directory under
  `~/.niwa/` behind. The design should say whether niwa's own removal of a
  workspace removes it.
- A release that's marked complete but whose files were later edited or
  damaged in place doesn't count as missing, so niwa won't fetch it again on
  its own. Removing the release directory, or its completion mark, puts it back
  under R6.
- Between a release going missing and the next ordinary command, instances
  that use it can't load that marketplace's skills. niwa runs no background
  check that would close that window.

## Decisions and Trade-offs

**Refetch covers every instance of the workspace, not only the one the command
is about.** The alternative was to restore only the releases the current
instance uses. That would fail the deleted-release scenario whenever the
reviewer's ordinary command is a new create on a newer release, leaving the
older instance broken. Checking the workspace's instances on disk costs a scan
but no download unless something is missing.

**The refetch rides commands that already provision; read-only commands don't
fetch.** `niwa status`, `niwa list` and `niwa go` stay offline and fast. Create,
apply, dispatch and the SessionStart hook already run the provisioning steps
and already heal Claude Code's dangling plugin records, so the refetch adds no
new kind of work to a new kind of command.

**Turning the store off doesn't convert existing instances.** The alternative
was to copy the held releases into each instance on the next apply. That makes
an off switch rewrite instances behind the user's back. Instances that already
use the held copy keep using it, and keep being repaired, until they're
removed; the setting governs creates from then on.

**"Missing" means absent or not marked complete, not damaged.** Checking
every file of every held release on each command would cost a full read of the
store each time. Treating an absent or unfinished release as missing covers a
hand deletion and an interrupted write, which are the cases the deleted-release
scenario and concurrent creates produce; damage in place is a stated limit.

**A failed refetch warns rather than fails.** A release that can't be restored
right now would otherwise block unrelated work. The command names the release
and the next one retries.

**Network and disk are the measure, not seconds.** The acceptance criteria
check downloads and on-disk copies, which are binary. Time savings were
estimated at a few seconds per create but never measured on a real instance,
so no time threshold is set.

**The location rule is applied, not reopened.** Where host-level state lives
was settled before this PRD; R16 states the rule and places the store under
it.

### Left open for the design

These are settled by the design, not here, and don't block the requirements:

- **How releases are keyed in the store**, including a marketplace tracked at
  a moving ref, which needs its ref resolved to a commit before it has a stable
  name.
- **The store's deletion rule**: when niwa may delete a held release. Whatever
  the rule is, it must never delete a release that any instance on disk still
  uses, kept instances included (R11).
- **The setting's name** in the workspace configuration (R8).
