---
schema: brief/v1
status: Done
problem: |
  Every niwa create downloads each plugin marketplace's release and writes a
  full copy of it into the new instance, even when another instance on the
  same host already holds that exact release. The copies are identical, so a
  host with many instances pays gigabytes of disk and seconds per create for nothing.
outcome: |
  From the second instance on, a create fetches and writes no plugin release
  the host already holds, so it finishes sooner and leaves more disk free.
  Every instance still loads its skills, and a release that goes missing from
  the host is fetched again by niwa without the user doing anything.
motivating_context: |
  A November 2026 survey of one maintainer's host, taken with `du` over each
  instance and a file-by-file comparison of trees against their release tags,
  found two plugin trees of about 58 MiB in every instance, byte-identical for
  the same tag, adding up to several GiB across the instances on disk. Sharing them is the cheapest of the
  ways to make a niwa instance lighter, and it shares its home on the host
  with the git-history store that follows it.
---

# BRIEF: Plugin releases held once per host

## Status

Done

The framing below is ready for the requirements. Three things are left to the
design on purpose: how a held release is named on disk (including a
marketplace tracked at a moving ref, which needs a commit before it has a
stable name), when niwa may delete one, and what the setting that turns the
store off is called.

## Problem Statement

niwa delivers skills to an instance from its own copy of each plugin
marketplace the workspace declares. Today that copy lives inside the instance:
every `niwa create`, every `niwa dispatch` and every instance the SessionStart
hook provisions downloads the release tarball for each marketplace and unpacks
a full tree under the instance's `.niwa/marketplaces/`. Nothing checks whether
the host already has that release.

It almost always does. A plugin release tag's content never changes, so the
tree for a given tag is byte-identical in every instance that pulled it. On a
host used for parallel agent work the duplication is the rule, not the edge
case. Measured with `du` across the 134 instances on one such host in
November 2026, each instance carried about 58 MiB of plugin
trees, and the instances on disk together held about 7.5 GiB of them where one
copy per release would have taken about 350 MiB. A burst of creates from a
coordinator downloads the same two tarballs once per worker.

The cost lands on the person creating instances: a slower create, a fuller
disk, and network traffic for bytes already sitting on the same machine. A
person running a single instance pays nothing extra today, but anyone who runs
two or more pays again for every one after the first.

## User Outcome

A person who runs more than one instance of a workspace on a host stops paying
twice for the same plugin release. When they create a second instance, whether
by hand, by dispatching a worker, or through the SessionStart hook, the create
downloads and writes nothing for releases the host already holds. It finishes
a few seconds sooner and takes no new disk for plugins.

Nothing else about the instance changes for them. Its skills load as before,
from where niwa delivers them today. If a held release disappears from the
host, because someone deleted it or the disk was damaged, they don't have to
notice or fix anything: the next niwa command fetches it again and instances
that were already linked to it keep working.

A person who wants each instance to carry its own copy can switch the shared
store off for a workspace and get today's behaviour back. And when the store
can't be used at all, a create still succeeds the old way and tells them it
fell back.

## User Journeys

### A second instance on a busy host

A developer has one instance of a workspace that declares two plugin
marketplaces. They run `niwa create` for a second instance (or a coordinator
dispatches a worker, which creates one). niwa sees that the host already holds
both releases, downloads neither, and points the new instance at the held
copies. The worker's session starts and its skills work exactly as in the
first instance.

### A held release goes missing

A developer short on disk deletes one release from the host's store by hand,
or a disk problem removes it. The developer later runs an ordinary niwa command, such as
`niwa apply` or a new create. niwa notices the release is gone, fetches it
again from its source, and puts it back. An instance created before the
deletion loads its skills again without the developer running any repair
step.

### Opting a workspace out

A developer wants instances of one workspace to be fully self-contained, for
example because the instances are moved or archived whole. They turn the
store off in that workspace's settings. The next create of that workspace
writes its own full copy of each release into the instance, as niwa does
today, while other workspaces on the host keep sharing.

### The store can't be used

A coordinator dispatches a worker while the host's store is unreadable or
unwritable, for instance because of a permissions problem. The worker's
create does not fail on that account: it fetches the releases into the
instance the old way and says, in its output, that it fell back and why, so
the coordinator reading the dispatch output knows the create cost the full
price.

## Scope Boundary

**In scope:**

- Holding each plugin release niwa fetches for skill delivery once per host
  and workspace, and having every instance of that workspace use the held copy.
- The creates that already provision plugins: one a person runs by hand, one
  a dispatch runs for a worker, and one the session-start hook runs, with no
  new command and no workspace config change needed to get the benefit.
- On by default wherever a workspace root exists, with a workspace setting to
  turn it off.
- Detecting a release that is missing from the store and fetching it again
  during ordinary niwa commands, with nobody running a repair.
- Falling back to a per-instance fetch, and saying so, when the store can't be
  used.
- A rule for when niwa may delete a held release that never removes one an
  instance on disk still uses, kept instances included.
- Placing the store according to the rule for where niwa keeps large
  host-level state, which the git-history store will follow too.

**Out of scope:**

- Sharing git history between instances (reference clones and the host mirror
  they borrow from). That is separate work that reuses this feature's location
  rule but not its store.
- A copy-on-write seed of a whole instance. It is held, not part of this work.
- Claude Code's own plugin cache under the user's Claude directory. Claude Code
  fetches marketplaces again into its own directory; this feature changes only
  the copy niwa keeps for skill delivery.
- Single-instance layouts with no workspace root. They keep today's
  per-instance copy.
- A user-run repair or cleanup command for the store. Repair is niwa's job, on
  its own.
- Changing which release a long-lived instance uses. An instance stays on the
  release it was created with, as today.
- Marketplaces niwa already keeps as managed repository copies at the
  workspace root. How long those are kept is not changed here.
