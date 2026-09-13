---
schema: design/v1
status: Planned
problem: |
  `niwa worktree destroy` finds its records through `resolveInstanceRoot`,
  which treats the workspace root as an instance, and it accepts only an 8-hex
  worktree id, never the Claude session id or handle a developer holds, so from
  the root it can never reach a dispatched session's worktrees. Separately, the
  snapshot writer rotates the whole workspace-root `.niwa/` through fixed
  `.next` and `.prev` paths with no ordering, while mapping writers, watch
  state writers, the reaper and `instance.json` writers touch the same
  directory.
decision: |
  A new `internal/configdir` package owns the lock, staging and recovery layout
  of a refreshed config directory. Each such directory gets a sibling
  `<dir>.lock` flock. A refresh fetches unlocked into a private staging
  directory created under a shared lock, and takes the lock exclusive only to
  recover, carry local state over and swap; every niwa write into the directory
  (mappings, watch state, `instance.json`) goes through one entry point that
  takes the lock exclusive, runs recovery and never recreates the directory;
  mapping reads take it shared. Recovery decides by which side holds the
  snapshot marker. Worktree teardown gets a resolver in `internal/cli` that
  matches a session id, handle or worktree id against one locked mapping
  snapshot and the current instance, refuses ambiguity, and destroys each
  active worktree of the session's instance through the existing guarded
  `DestroySession`; `resolveInstanceRoot` refuses a multi-instance workspace
  root for every other worktree command. A leaf `internal/testhook` registry
  lets tests hold each race point deterministically.
rationale: |
  Holding the lock only for local copies and renames keeps a slow fetch from
  delaying anyone else while, on Linux and macOS, ordering every niwa write and
  destructive mapping read against the swap, so no mapping write is lost, no
  delete is undone and the directory is never left without its configuration;
  it reuses the flock pattern niwa already ships and adds no module. Putting
  the root refusal in the resolver all nine worktree callers share fixes them
  all, including completion and the hook, with no per-command checks. Keeping
  teardown resolution in `internal/cli` leaves `internal/worktree` a leaf. A
  test-only registry is the cheapest seam that reaches across packages and
  cannot be triggered from a release binary.
upstream: docs/prds/PRD-session-store-teardown.md
user_visible_surface: true
---

# DESIGN: session-store-teardown

## Status

Planned

## Context and Problem Statement

Two stores share the name `.niwa/sessions/` and nothing else. The dispatch
session-mapping store lives in the workspace root's `.niwa/`, keyed by the
agent's session id (a UUID), written by `WriteSessionMapping`
(`internal/workspace/session_map.go`) from `niwa dispatch` and the
ephemeral-session hook, deleted by `niwa reap`, and read by `niwa list` and
both reaper sweeps. The worktree lifecycle store lives in each instance's
`.niwa/`, keyed by an 8-hex id, written by `CreateSession` and read by every
`niwa worktree` subcommand through `ReadSessionLifecycleState`
(`internal/worktree/session_lifecycle.go`).

The teardown half of the problem is a resolution gap. `runSessionDestroy`
(`internal/cli/session_lifecycle_cmd.go`) resolves its directory through
`resolveInstanceRoot` (`internal/cli/session.go`), which walks up to the first
`.niwa/instance.json`. The workspace root carries one (`niwa init` writes root
state there), so from the root the walk stops at the root and the worktree
commands treat it as an instance. Its `.niwa/sessions/` holds only UUID-named
mapping files, which the lifecycle reader skips, so `worktree list` shows an
empty store and `destroy`, `apply`, `attach`, `detach` and `niwa go` fail with
a "no such file" or "invalid session ID" error. `workspace.ClassifyCwd`
already tells a root from an instance by the presence of
`.niwa/workspace.toml`, but the worktree commands don't use it. And the value a
developer holds is never an 8-hex worktree id: it is the session UUID, or the
Claude handle (the agent's record-directory name, the `id` field of
`claude agents --json`). Nothing in a lifecycle record links it to a session
(`ParentSessionID` and `ClaudeConversationID` have no production writer), so
the only path from a session to its worktrees is mapping, then instance, then
that instance's lifecycle store.

The concurrency half is an ordering gap in one function. Every refresh of a
config directory (the workspace root's `.niwa/`, an overlay clone, the global
config clone) ends in `materializeAndSwap`
(`internal/workspace/snapshotwriter.go`). It stages at the fixed path
`configDir + ".next"` after deleting whatever is there, fetches into it (a
GitHub tarball or a `git clone`), writes the provenance marker, copies
`instance.json`, `dispatch-briefs/` and `sessions/` in from the live
directory, and calls `SwapSnapshotAtomic` (`internal/workspace/snapshot.go`),
which deletes a fixed `target + ".prev"`, renames live to `.prev`, staging to
live, and deletes `.prev`. Nothing orders two of these against each other, or
against a niwa write into the live directory: a mapping written or deleted
between the carry-over copy and the rename, a watch state write
(`internal/watch/state.go`), or an `instance.json` write. Several of those
writers create `.niwa` with `MkdirAll`, so one landing inside the rename
window recreates the directory and makes the swap fail. A non-GitHub source
re-materializes on every refresh and one dispatch refreshes twice, so parallel
dispatches always overlap. Two further defects sit in the same code: the
swap's preflight deletes `.prev` even when the live directory is missing, so a
crash between the two renames loses the only copy of the old snapshot on the
next refresh; and the refresh reads the live marker with no ordering, so it
can fail with ENOENT while another refresh is mid-swap. Separately,
`SaveState` writes `instance.json` with a truncating write, and
`saveWorkspaceRootDisclosures` (`internal/workspace/apply.go`) rewrites the
root file from a start-of-command read whose error `Create` discards, so a
torn read can drop `ephemeral_session_mode`.

In the single-instance layout, where the workspace root is itself the only
instance, the lifecycle records share the rotated directory with the mappings;
the PRD (`docs/prds/PRD-session-store-teardown.md`) leaves their concurrency
out of scope and keeps that layout's worktree commands working as today.

## Decision Drivers

- **Teardown contract (PRD R1-R10).** Session id and handle, in addition to
  the worktree id and `--by-path`; the same result from the root, any
  instance, or a worktree; refusal on any ambiguity; a fixed outcome table of
  exit codes 0, 1, 3 and 4 with stable output; and, at a multi-instance root,
  no subcommand treating the root as an instance.
- **Concurrency guarantees (R11-R16).** No refresh fails because of another; a
  mapping written or deleted around a refresh ends as its writer left it; the
  directory is never left without its configuration, whatever niwa wrote in
  the meantime; the reaper and teardown never act on a mid-swap read; an
  interrupted refresh self-heals.
- **Waiting bounds (R17-R19).** A 30-second, test-overridable bound with a
  clear error; wait time independent of network fetch time; a command never
  waits on itself, though `niwa dispatch` refreshes twice and writes a mapping
  in one process.
- **Root state (R22-R23).** `instance.json` writes are never visible half
  done; a failed root-state read never turns into a root-state write.
- **Platforms and footprint (R20-R21).** Linux and macOS get the guarantee
  through advisory file locking; other platforms build with a documented gap;
  no new module; no behavior change beyond the PRD's.
- **Deterministic tests (R24-R25).** The race tests must fail on today's code
  by forcing the interleaving, not by repetition.
- **Implementation fit.** Reuse `ClassifyCwd` and the existing flock pattern
  (`internal/workspace/codex_trust_lock_unix.go` with its `_other.go` no-op);
  keep `internal/worktree` a leaf that does not import `internal/workspace`;
  give the swap's file names one owner, since four call sites in three
  packages need them.

## Considered Options

### Decision 1: How refreshes of a config directory are ordered

This decision answers the concurrency guarantees and the waiting bounds: what
orders a refresh against another refresh, against niwa's in-place writes, and
against the mapping reads that decide to destroy something.

Key assumptions: no production path refreshes one directory from two
goroutines of one process at once (`Apply` refreshes per instance
sequentially, and `niwa watch` never refreshes config); concurrently running
niwa versions older than this change get no guarantee.

#### Chosen: A short per-directory lock around carry-over and swap, with the fetch unlocked in private staging

**One owner for the layout.** A new leaf package, `internal/configdir`, owns
every name the swap uses, the lock, the snapshot-marker check, the swap journal
and the recovery rules. `SwapSnapshotAtomic`, the snapshot writer, the mapping
store, watch state and `config.Discover` all call it rather than spelling the
names themselves. The claim is scoped to workspace configuration directories:
`internal/plugin`'s installer runs a `.next`/`.prev` rotation of its own that
this change does not touch and does not route through the package.

**Two siblings, and everything else inside one of them.** Config directory `D`
gets exactly two siblings: the lock file `D + ".lock"` and a working directory
`D + "@swap"`. The moved-aside snapshot, the staging directory, trash and kept
stray directories all live *inside* `D@swap`, under random names, rather than
beside `D`.

The `@` is load-bearing. Overlay clones share the directory that holds `D`, and
`config.OverlayDir` names them `<org> + "-" + <repo>` with neither component
charset-checked, so an overlay named `acme/tools.prev` lands byte-identical to
where a `<dir>.prev` scheme would put `acme-tools`'s previous snapshot. A forge
host's names cannot contain `@`, so no overlay can produce `D@swap`. Everything
niwa rotates is therefore out of the namespace it shares with data it must not
touch, and recovery never has to decide whether a sibling is its own.

This also removes the fixed rename destination. The swap renames `D` to
`D@swap/prev-<random>`, not to `D + ".prev"`, so there is no guessable path
whose pre-existence fails the rename. That matters because this design drops
`SwapSnapshotAtomic`'s current unconditional preflight delete of `.prev`: with a
fixed name and no preflight, one stray file at a predictable path would fail
every refresh of the workspace forever, which is a worse failure than the one
being fixed. With a random destination inside `D@swap`, a collision is a
coincidence rather than a target, and it retries once with a fresh name.

The legacy fixed `<dir>.prev` an older binary may leave behind is never read,
renamed or deleted by this code. Older binaries still run their own preflight
clear, so the path self-heals under the code that created it, and the new code
treating it as invisible is what keeps recovery out of an older binary's swap.

**The lock.** The lock for config directory `D` is the sibling file
`D + ".lock"` (`<root>/.niwa.lock`, `$XDG_CONFIG_HOME/niwa/overlays/<name>.lock`,
`$XDG_CONFIG_HOME/niwa/global.lock`), created mode 0600 and never unlinked,
because unlinking a flock file lets a waiter on the old inode and a new opener
on a fresh one both think they hold it. Being a sibling, it survives every
swap, needs no agreement on `$HOME` between the hook, dispatch and tests, and
reaches the same inode through any spelling of `D`. The helper follows the
existing Codex trust lock: a non-blocking `flock` polled every 20 ms against a
deadline, `LOCK_SH` or `LOCK_EX`, a 30-second bound held in a variable tests
override, and a timeout error naming `D` and saying another niwa command is
using it; its non-unix twin is a documented no-op.

Creating the file 0600 says nothing about a file that already exists, since
`O_CREAT` changes neither the mode nor the owner of one. The descriptor is
therefore `fstat`ed after the open and refused unless it is a regular file owned
by the invoking user with no group or world write bit. That matters for the
global lock, whose path follows the environment-controlled `XDG_CONFIG_HOME`,
and for a workspace root under a group-writable parent, where another user could
otherwise pre-create the lock 0666 and hold it until every niwa command on that
workspace times out.

A lock is only a lock while both parties hold the same inode. After `flock`
succeeds the helper `fstat`s the descriptor, `stat`s the path, and requires the
same `(dev, ino)`; if they differ it closes, reopens and retries within the
bound. Without that check, an `rm` or a tidy-up script landing between one
process's open and another's leaves the two flocking different inodes while both
believe they are serialized, and every ordering guarantee in this design
evaporates with nothing reporting an error.

Because `flock` contends per open file description and each acquisition opens a
fresh descriptor, a second acquisition of the same lock in one process would
wait on itself until the bound; the package therefore keeps a process-local set
of held lock paths and fails a nested acquisition at once with
`nested acquisition of <D>.lock`. That set detects a misuse; it does not grant
reentry. It also enforces an order: config directories are ranked global,
then overlay, then workspace root, and an acquisition that would take a lock
ranked at or below one this process already holds is refused the same way.
`Apply` touches all three in one command, so without the rule two processes
taking them in opposite orders would each wait out the full bound and both fail.
The detector already tracks the held set; the ordering rule is one comparison
against it.

**Two entry points.** `configdir.Mutate(D, fn)` takes the lock exclusive, runs
recovery, calls `fn`, releases, then deletes any trash recovery or `fn`
produced. `configdir.Read(D, fn)` takes it shared and calls `fn`. Every caller
goes through one of the two, so "every exclusive holder recovers first" and
"deletes happen outside the lock" hold without each caller remembering them.
Callers:

| Operation | Entry | Covers |
|---|---|---|
| Refresh probe of the marker or `.git`, `ReadProvenance`, creating and locking its staging directory | `Read` | the reads and the staging creation |
| Refresh fetch (`HeadCommit` retries, tarball, `git clone`) | none | |
| Refresh carry-over and swap | `Mutate` | local copies and renames |
| No-drift marker rewrite | `Mutate` | re-read, compare, atomic write |
| Post-swap reload in `ReconcileAndReloadConfig` | `Read` | the `config.Load` |
| `WriteSessionMapping`, `DeleteSessionMapping` | `Mutate` | the write-and-rename, or the remove |
| `ListSessionMappings`, `ReadSessionMapping` | `Read` | the directory scan |
| Watch state writes (`writeHandledState`, `SaveStagedRecord`) | `Mutate` | the write |
| `SaveState` into a refreshed config directory (root disclosures, `niwa init`, the single-instance `Apply`) | `Mutate` | the atomic write |

Nothing upgrades shared to exclusive. Because the read locks live inside the
store functions, every current and future reader of the mapping store decides
on a snapshot taken while no swap ran. Every in-place writer requires `D` to
exist and creates only its own leaf directory with `os.Mkdir`; none calls
`MkdirAll` on `D`, so none can recreate a directory a swap has moved aside. A
refresh creates `D`'s parent, when missing (a first overlay or global clone),
before taking the lock.

**Staging and recovery.** Each refresh creates a private staging directory,
locked by the refresh for its life, while holding the shared lock, so a
recovery sweep (which runs only under the exclusive lock) never meets one whose
owner has not yet locked it.

**Recovery acts on a journal, not on what a directory looks like.** The
tempting scheme is to recognize niwa's own rotation by content -- a previous
snapshot is a directory carrying the provenance marker. That does not work, and
the reason is worth stating because it is not obvious: an overlay clone *is* a
refreshed config directory, maintained by this same pipeline, so it carries the
provenance marker too. `EnsureOverlaySnapshot` even uses the marker's presence to
decide the clone already exists. The marker establishes that a directory is
niwa's; it cannot establish *which* directory it belongs to or *what role* it
holds. Role has to come from a record written at swap time.

So the swap writes one. Under the exclusive lock, before the first rename, it
writes `D@swap/journal.json`: the moved-aside path, the staging path, a random
token, the pid, and the start time. It removes the journal under the lock after
the second rename. Recovery reads the journal and acts only on the paths that
journal names. There is no scan for look-alike directories, and no inference
from a name.

That single mechanism closes three separate problems. A directory that merely
resembles a previous snapshot is never touched, because no journal names it. An
older binary mid-swap is never disturbed, because it writes no journal, so its
in-flight `<dir>.prev` is invisible to this code -- without that, a new binary's
`Mutate` (now on the path of every mapping write, every watch write and every
`instance.json` write) would see the old binary's moved-aside directory, read it
as a crash, rename it back, and break a swap that was progressing normally. That
is the ordinary condition during an upgrade, with a long-running `niwa watch` or
a backgrounded dispatch, and it is the failure most likely to be met in
practice. And recovery gains the one thing state inspection cannot give it:
evidence that a swap actually started.

Given the journal, the rules are short. If the journal's staging lock is still
held, its owner is alive and recovery leaves everything alone. Otherwise the
owner died: if the live directory is missing, rename the moved-aside directory
back; if the live directory exists, the second rename had already landed, so
trash the moved-aside one; if the live directory exists but the journal records
that it was recreated after the move, keep it under a stray name and rename the
snapshot back. Then remove the journal. Staging directories no journal names,
left by a crash before the journal was written, are trashed when their lock can
be taken. Every removal under the lock is a rename to a trash name inside
`D@swap`, deleted after release, so a large git-clone tree never holds up a
reaper sweep or a mapping write.

**The threat model, stated once.** A process running as the invoking user with
write access to the workspace root is *outside* this design's threat model, and
the journal is not a defense against one. Such a process can rewrite
`.niwa/workspace.toml` directly; nothing niwa does to its own auxiliary paths
changes what it can reach. What the journal defends against is niwa destroying
or promoting data that is legitimately not its own -- a neighbouring overlay
clone, an older binary's in-flight swap, a user's stray file. That is an
accident-prevention boundary, not a security boundary, and the distinction
matters: the untrusted inputs this design does defend against are the *contents*
of files it parses and prints (mapping fields, lifecycle records), because those
flow into git arguments and terminal output, which is a different question from
who can write where.

A failed check inside recovery is a warning, not a failure of the operation that
triggered it. Recovery runs at the head of every exclusive section, so making it
fatal would mean one unexpected entry inside `D@swap` permanently failing every
mapping write, watch write and state write on that directory. It reports once
per directory per process on stderr and lets the callback run. Only a rename
that still fails after its retry is fatal, and only to the operation that needed
it.

**Recovery does not run on non-unix.** There the lock is a no-op, so running a
multi-step rename dance with no exclusion would be worse than today, where
nothing renames a moved-aside snapshot back at all. On those platforms the
exclusive entry point runs its callback without recovery, an interrupted
refresh needs manual cleanup, and the fallback file says so. This is a
different trade from the existing Codex trust lock's no-op, which guards a
single atomic replacement rather than a rename sequence.

`config.Discover` gains one narrow branch that runs the moved-aside repair, so
the next command of any kind finds its configuration. It is the one place
discovery can take a lock and rename a directory; it repairs at most one
candidate per command, so the bound applies once rather than once per directory
the walk passes through, and it must never be reached from inside an entry
point's callback, which the nested detector would turn into an immediate error.

**No nesting, and fairness.** The lock is taken only inside the two entry
points, whose callbacks never call another lock-taking function, so a
dispatch's reap, two refreshes and mapping write are one acquisition after
another and R19 holds; the nested-acquisition detector and the ordering rule
turn any future violation into an immediate error instead of a 30-second stall
or a deadlock between two commands. Polling
non-blocking tries gives no fairness, so an exclusive waiter can lose rounds to
overlapping shared readers. Shared holds are short but not all equal: the
mapping scan is one directory read, while the post-swap reload spans a config
parse and staging creation spans a mkdir and a lock. The bound turns sustained
starvation into an error rather than a hang, which is what this design accepts,
and a test checks an exclusive waiter behind repeated shared holds gets through
within the bound. Watch's state writes add exclusive traffic on the root
directory for state no refresh carries across, which is the cost of keeping
them from recreating that directory mid-swap; the follow-up that makes watch
state survive a refresh is where that stops being a trade.

#### Alternatives Considered

**The same lock held for the whole refresh, fetch included**: simpler, with a
fixed staging path and no liveness check. Rejected because it breaks R18 by
construction: another command's mapping write waits out up to 3.5 seconds of
drift-check backoff plus the tarball or clone, and four parallel dispatches'
eight fetches serialize toward the 30-second bound.

**Move the mapping store out of the rotated directory, with unique staging
and prev paths and no lock**: removes the lost-write race for mappings.
Rejected because moving local state out of the config directory is niwa issue
#74, out of scope, and needs a migration; and unique paths alone still let two
unordered swaps collide (one renames live aside, the other sees no target and
swaps in, the first's second rename fails with ENOTEMPTY), which is exactly
the failure R11 forbids.

**Merge mappings back from `.prev` after the swap, with delete tombstones and
no lock**: rejected because it inherits the unordered-swap collision, does not
stop a writer's `MkdirAll` from recreating `.niwa` in the rename window, does
not fix a mid-swap empty read, and needs its own tombstone collection.

**Swap with an atomic directory exchange (`RENAME_EXCHANGE` on Linux,
`RENAME_SWAP` on macOS, from the `golang.org/x/sys` module niwa already
requires)**: closes the window in which the directory is absent. Rejected as
the ordering mechanism because it does nothing to order a mapping write
against the carry-over, and filesystem support varies; it remains a possible
later hardening of the chosen swap.

### Decision 2: How `niwa worktree destroy` resolves its target, and where the root refusal lives

This decision answers the teardown contract. Nine callers find their records
through `resolveInstanceRoot`: create, apply, destroy and list, attach and
detach, `niwa go <repo> <id>`, completion, and the WorktreeRemove hook. What it
settles is where the resolution and the root refusal live.

Key assumptions: `NIWA_INSTANCE_ROOT` stays a verbatim override and is never
refused (niwa sets it only to an instance root); `destroy --by-path` at a
multi-instance root keeps today's result, taking R10's "as today" literally;
`niwa worktree list --json` at the root still prints `[]` on stdout, since R21
keeps existing output.

#### Chosen: A destroy resolver in `internal/cli`, with the root refusal centralized in `resolveInstanceRoot`

`discoverInstanceRoot` is rebuilt on `workspace.ClassifyCwd`: inside a
worktree or instance it returns the instance; at a workspace root it returns
the root only in the single-instance layout, and otherwise returns the
sentinel `errAtWorkspaceRoot`, whose text is R10's line. The layout test is a
new `workspace.IsSingleInstanceLayout(root)`: no child instance, and the root's
`instance.json` names an instance. The second condition matters because every
registered `niwa init` writes a root `instance.json` without an instance name,
so a freshly initialized workspace, or one whose instances were all reaped,
would otherwise count as single-instance and have worktrees created inside the
rotated directory. `internal/cli/apply.go` switches to the same helper, so it
gets the same fix. Because `Execute` prints plain errors verbatim with exit 1,
create, apply, attach, detach and `niwa go` produce R10's behavior with no
edit (for `niwa go` only the message changes, and it gains no session
resolution), completion already turns a resolver error into no candidates, and
the hook already logs it and exits 0. Only `worktree list` adds a branch that
prints the redirect and exits 0. Because the rebuild changes resolution for
every caller inside instances too, a table test pins it across every layout:
an instance root, a repository clone inside it, a niwa worktree under
`<instance>/.niwa/worktrees/`, a worktree Claude Code created under
`<repo>/.claude/worktrees/`, the multi-instance root, a freshly initialized
root with no instance, the single-instance root, a directory outside any
workspace, and a run with `NIWA_INSTANCE_ROOT` set.

Destroy's positional form gets its own resolver in a new
`internal/cli/worktree_destroy_resolve.go`:

- `resolveDestroyScope` classifies the working directory into an instance
  directory (inside an instance or worktree, or a single-instance root) and a
  workspace root.
- `loadMappingsForDestroy` makes the one mapping-store read: the locked
  `ListSessionMappings`, filtered to entries whose `session_id` field is a valid
  UUID, so the single-instance layout's lifecycle files, which share that
  directory, never become fake mappings. It applies the existing exported
  `watch.IsSafeHandle` to the `handle` field too. A handle is matched as a string
  and then printed back in R9's ambiguity line, and it comes from the same
  untrusted file as everything else in the mapping; `IsSafeHandle` exists for
  exactly this ("callers use it to validate a captured short id before it becomes
  a CLI argument") and allows only `[A-Za-z0-9_-]{1,128}`. A mapping whose handle
  fails it keeps its session-id match and loses its handle match, which closes
  the injection path into stderr without dropping the record.
- `matchMappings` applies R4 (exact session id, recorded handle, or a unique
  8-hex prefix for a mapping with no handle), deduplicated by session id so a
  Codex mapping whose handle is its id is one match.
- `resolveDestroyTarget` is pure: given the scope, the value and the snapshot,
  it returns a worktree id or one mapping, or an `ExitCodeError` with code 3
  (no match) or 4 (ambiguity, with the R5 guidance when a worktree is among
  the matches). `--by-path` bypasses the resolver but shares its no-match
  error, so a path resolving to no worktree exits 3 rather than today's 1; its
  message does not change. Both routes reach the same outcome, so they must not
  differ by which one located the target. Codes 3 and 4 are `destroy`'s own:
  `attach` already exits 3 when the attach lock is held and `detach --force`
  exits 4 after killing a live holder, and per-subcommand code tables are
  already this codebase's pattern -- `niwa init` reuses 3 and 4 for meanings of
  its own. The worktree guide gains a second table bound to `destroy`, and
  `destroy --help` names its codes inline the way `attach` does. Nothing in the
  repository branches on a worktree-family exit code today, so the moved code
  breaks no caller; it still ships announced as a behavior change rather than
  folded in as a fix.
- `checkSessionInstance` applies R7's ordered checks against the same
  snapshot and returns the enumerated instance directory, not the recorded
  string: instance location (lexically, and against the root's resolved
  path), existence (`Lstat`; missing means the exit-0 "no longer exists"
  outcome), membership among `EnumerateInstances`, and R6 through a new
  `workspace.NewestMappingPerInstance` that `niwa list` also adopts.
  `EnumerateInstances` returns lexical joins of whatever root it was handed and
  resolves nothing, so both sides of the membership comparison are resolved
  before it is made; comparing a symlink-resolved recorded path against an
  unresolved set would mismatch wherever the workspace root itself sits under a
  symlink, which on macOS is the default for anything under `/tmp`. R6's
  newest-mapping rule is a usability rail, not one of these checks: it reads the
  untrusted `created` field, so it keeps teardown off a superseded session but
  bounds nothing against a crafted store.
- `destroySessionWorktrees` lists the instance's lifecycle records, keeps the
  active ones in worktree-id order, re-checks the instance directory with
  `Lstat`, and calls `worktree.DestroySession` for each, printing R9's lines,
  continuing past refusals, and returning exit 1 if any worktree was refused.

**The record's own fields are validated where they are used, which means
`internal/worktree` changes after all.** A lifecycle record is a JSON file any
process running as the user can write, and today nothing checks its path or
branch fields: `DestroySession` passes `worktree_path` to
`git worktree remove --force` and the record's branch name to `git branch -d`
as positional arguments, with no containment test and no end-of-options marker,
and the uncommitted-changes guard reports a missing or empty path as clean,
so a record naming another tree of the same repository passes a guard about a
different directory. Teardown by session multiplies what that is worth: one
command from the workspace root now reaches every active record in an instance
rather than the single id a developer typed.

Putting those checks in `internal/cli` would guard values the callee re-reads
from disk for itself, and would leave destroy-by-id and the WorktreeRemove hook
unguarded, so they go next to the git calls instead. `DestroySession` requires
the record's worktree path to be non-empty and, after resolution, to lie
under the instance's own worktrees directory, and treats a missing worktree
directory as a refusal rather than as a clean tree. The dirty-tree guard fails
closed on *any* `stat` error, not only on `ENOENT`: today a permission error or
an `ELOOP` on a symlink loop leaves the error untested and falls through to the
git call. The reads that feed it resolve through a root opened on the instance
directory, so a path swapped for a symlink after the check cannot redirect them.
This narrows rather than closes the window for the paths that still resolve by
string, and the design says so rather than claiming a closure.

The branch name gets an exact pattern, not a judgement call, because it is the
only barrier between a wholly unvalidated JSON field and two git argv positions
plus a copy-pasteable command line. It must be a valid ref under
`git check-ref-format`'s rules as applied here: non-empty; no byte below 0x20 and
no DEL; no space; none of `~`, `^`, `:`, `?`, `*`, `[`, `\`; no `..`; no leading
`-`; no trailing `.lock`; no leading or trailing `/` and no `//`; and not the
single character `@`. It is passed after `--`.

The same argv exists a third time, in `workspace.DefaultDestroySession`, the
`niwa init --bootstrap` rollback, which parses the record with its own inline
struct and runs `git branch -D` with no validation and no `--`. Its only caller
acts on a record the bootstrap just wrote, so the exposure is small, but the
reasoning for putting the checks beside the git calls applies there identically
and it is routed through the same validator rather than left as the one copy
that is still unguarded.

Every value interpolated into an outcome, refusal or ambiguity line has control
characters stripped rather than being quoted, so the literal shapes R9 pins are
preserved. No existing helper can do this: the dispatch path deliberately
*refuses* rather than strips (and its comment says so), `internal/tui`'s
sanitizer and every hand-rolled stripper here match Cc only, and the one
adequate stripper is unexported inside `internal/cli`, which `internal/worktree`
cannot import because `internal/cli` imports it. So this change adds a small leaf
package, `internal/safetext`, that both can import, stripping C0, C1, DEL,
U+2028, U+2029 and the Cf bidi and zero-width block. The Cf coverage is the
point: U+202E and U+200B pass every Cc-only stripper in this repository, and the
line that most needs the protection is the kept-branch warning, which carries a
record-supplied branch name into a paste-ready `git branch -D` line and which
teardown by session now emits once per worktree instead of once per command.
The branch pattern above already rejects most of what would reach it; the
stripper is the second layer, and the mapping handle's `IsSafeHandle` filter is
the third.

A worktree-only match takes today's `DestroySession` path byte for byte.
`--force` with a session id or handle is refused as a usage error (exit 2);
forcing stays available one worktree at a time. Three behavior changes for
existing invocations are called out in the pull request: a worktree id matching
nothing, or a non-hex value, exits 3 instead of 1 (sanctioned by the PRD); a
destroy by worktree id now reads the root mapping store, so under contention
past the bound it can fail with R17's error; and a lifecycle record whose path
or branch field fails the new validation is refused rather than acted on, which
changes nothing for records niwa wrote.

#### Alternatives Considered

**The same resolver, with the root refusal added per command**: a smaller
change to `session.go`. Rejected because it takes six near-identical checks,
still leaves completion and the hook resolving the root as an instance unless
they get checks too, and lets the next worktree subcommand inherit the bug by
default.

**A `--session <id>` flag, with the positional argument staying worktree-id
only**: avoids the collision by syntax. Rejected because R1 and R10 require
the positional argument to accept the session id and handle, the PRD already
settled collisions by refusal, and scripts would have to pick a flag by the
shape of an id.

**Resolution inside `internal/worktree` behind a mapping-lookup interface**:
keeps orchestration next to `DestroySession`. Rejected because the interface
would carry the workspace-root location rule, instance enumeration and
newest-mapping ordering with a single implementation in `internal/workspace`,
keeping the leaf rule only on paper while pushing exit-code and output policy
into a package that has never owned it.

### Decision 3: A test-only seam that forces the races deterministically

This decision answers the deterministic-test driver. The holdable points are:
a refresh between carry-over and swap, the gap between the two renames, a
refresh held in its fetch, a mapping read landing mid-swap, a process killed
at the first two points, and a root `instance.json` read forced to fail. The
existing seams do not reach: `testfault` can only return errors or truncate
streams through an environment variable, and package-level override variables
such as `driftCheckBackoff` reach only their own package, while the reaper and
destroy live in `internal/cli` and the swap they must land inside lives in
`internal/workspace`.

Key assumptions: the directory lock contends between goroutines of one process
(one open file description per acquisition); hook points can be placed in
today's code with no behavior change, in the first commit, so the new tests can
be shown failing against it.

#### Chosen: Named in-process hook points in a leaf `internal/testhook` registry, with a re-executed test binary for kills

`internal/testhook` declares named points and two calls. Production code calls
`Hit(point) error`, one atomic load that returns nil when no hook is set, and
propagates its error rather than discarding it. Only `_test.go` code calls
`Set(point, fn) (restore func())`, which panics if the point already has a
hook (the registry is process-global, so hook-using tests do not run
`t.Parallel`) and is restored through `t.Cleanup`. The package reads no
environment and registers nothing in `init()`. A guard test parses every
non-`_test.go` Go file in the module (`cmd/`, `internal/`, `test/`) with
`go/parser` and fails if any refers to `testhook.Set` at all, which catches
aliases and wrappers as well as calls. The division with `testfault` is: that
package keeps its existing environment-driven error faults on the fetch path;
`testhook` carries the in-process holds and the one in-process failure (the
root-state read) that must not be reachable from the environment.

Points: `snapshot-carried-over` (after carry-over, before the swap, inside the
exclusive section), `snapshot-moved-aside` (between the two renames),
`configdir-lock-contended` (the first failed try of a lock acquisition), and
`root-state-read` (beside the root `LoadState` in both `Create` and `Apply`).

A test holds a refresh in a goroutine on a channel at a point, runs the other
side (a mapping write or delete, a watch state write, a second refresh, the
reaper, a destroy lookup) in another goroutine, waits for either that side to
finish or the contended hook to fire, and releases the hold. On today's code
the other side finishes during the hold and the assertion fails every time; on
the fixed code it parks on the lock, the contended hook fires, and the
assertions pass. A fake fetcher that blocks on a channel holds a refresh in its
fetch with no new hook. A kill is real: the test re-executes its own binary
with a helper-only marker that only `_test.go` code reads, the child blocks at
the point, and the parent sends SIGKILL, so the kernel drops the lock and no
deferred cleanup runs; these tests skip on non-unix, matching the platform gap.

#### Alternatives Considered

**Unexported hook variables in `internal/workspace`, with kills simulated by a
panicking hook**: the codebase's existing pattern. Rejected in that form
because `internal/cli` tests cannot set another package's unexported variable,
and a panic runs deferred unlocks and cleanup, so it doesn't reproduce a kill.
The chosen option keeps the mechanism and fixes both gaps.

**Pause and exit specs in `testfault`**: would allow cross-process forcing in
functional tests. Rejected because it puts a hang and a process exit behind an
environment variable release binaries honor, and the PRD accepts in-process
forcing as the proof across commands.

**A hooks interface threaded from callers**: rejected because refresh has
eight production call sites and the contended signal would also have to reach
the mapping writer, the reaper and destroy, widening production signatures for
test-only benefit.

**Hooks compiled only under a build tag**: rejected because unit tests run
untagged, so a tag would either change every test invocation or silently
compile the forced tests out.

### Decision 4: `instance.json` guards

This decision answers the root-state driver without reordering how `Create`
and `Apply` handle state; the stale read-modify-write itself is a separate
follow-up. Three callers write an `instance.json` inside a refreshed config
directory: root disclosures, `niwa init`, and `Apply` in the single-instance
layout.

Key assumption: a torn or failed root read is transient and rare, so losing a
notice key on that path (it reappears on the next run) is acceptable.

#### Chosen: The lock and the atomic write inside `SaveState`, plus skip-after-failed-read

`SaveState` writes a uniquely named temp file in the same `.niwa/` directory,
sets it to 0644 to match today's file, and renames it over `instance.json`.
When the target `.niwa` is a refreshed config directory (a sibling
`.niwa.lock` exists, or the directory is a workspace root or a single-instance
root), the write goes through `configdir.Mutate` and does not `MkdirAll` an
existing workspace's `.niwa`; instance directories, which no refresh rotates,
keep today's unlocked path. Putting the rule inside `SaveState` covers every
caller, not only root disclosures. `saveWorkspaceRootDisclosures` also returns
without writing when its `existing` state is nil and the root `instance.json`
exists.

#### Alternatives Considered

**Fail `Create` when the root read fails**: never writes from a failed read.
Rejected because it turns a transient torn read during parallel dispatch into
a failed provision, against R11, and changes the result of an invocation that
succeeds today, against R21.

**Lock only around the root disclosure write**: the smallest change. Rejected
because `niwa init` and the single-instance `Apply` write the same file in the
same directory and would keep the `MkdirAll` hazard.

**Also move the root read under the lock, closing the stale read-modify-write
here**: rejected for this change because it holds the lock across most of
`Create` and `Apply` or reorders their state handling, which breaks R18 and
widens the change past the PRD; it stays a follow-up issue.

## Decision Outcome

**Chosen: a short per-directory lock with an unlocked fetch, owned by
`internal/configdir` + a CLI destroy resolver with the root refusal in
`resolveInstanceRoot` + an `internal/testhook` registry + a locked, atomic
`SaveState` with skip-after-failed-read**

### Summary

Every config directory niwa refreshes gets two siblings: a lock file, and a
`@swap` working directory that holds everything the swap rotates. One package
owns both. A refresh probes the marker and creates its private staging
directory under a shared lock, fetches with no lock, then takes the lock
exclusive for the local work: recover from any interrupted swap by reading the
swap journal, move dead staging and old snapshots to trash names, copy
`instance.json`, `dispatch-briefs/` and `sessions/` in from the live directory,
write the journal, and swap; the journal is removed and the trash deleted after
the lock is released. Every niwa write into the directory (mappings, watch
state, `instance.json`) goes through the same exclusive entry point and never
recreates the directory; mapping reads take the lock shared. So, on Linux and
macOS, a write lands either before the carry-over, and rides across, or after
the swap; a delete is never undone; the reaper and teardown always see a whole
store; and the directory is never left without its configuration. Windows gets
none of that, as the fallback states. A
process that waits longer than 30 seconds (a test-overridable variable) fails
with an error naming the directory; a process that tries to take a lock it
already holds fails at once. Nothing holds the lock across a network fetch or
a large delete, so four parallel dispatches fetch concurrently and queue only
for their swaps.

`niwa worktree destroy <value>` classifies where it is running, reads the
mapping store once, and matches the value as a worktree id of the current
instance or a session by id, recorded handle, or unique 8-hex prefix. No match
exits 3; more than one candidate exits 4 and names them. A single session runs
R7's path checks (a missing instance directory exits 0, "no longer exists"),
the newest-mapping rule, and then destroys each active worktree in worktree-id
order through the existing guarded `DestroySession`, continuing past refusals
and exiting 1 if any refused; `--force` with a session is refused. The other
worktree subcommands at a multi-instance root now say where to run instead of
treating the root as an instance; `worktree list` there exits 0. Tests hold
each race point on a channel through `internal/testhook`, and kill a
re-executed child for the crash cases.

### Rationale

The lock and the resolver meet at one call. Teardown's only mapping read is
the locked `ListSessionMappings`, so the rule that no destructive decision
rests on a mid-swap read is enforced once, inside the store, for the reaper
and destroy alike. The test registry is shaped by the same boundary: the
lock's contended point and the swap's points live below `internal/cli` while
the reaper and destroy tests live in it, and a leaf registry is what lets one
test hold the swap in one package and observe the waiter in another. Keeping
the lock short, with deletes pushed outside it and every caller funneled
through two entry points, is what makes the combination cheap and hard to
misuse enough to put under every mapping write and read.

## Solution Architecture

### Overview

Two independent changes meet at the mapping store. The concurrency change adds
a config-directory package that owns the lock and the swap's layout, reshapes
the snapshot writer around it, and routes every niwa writer and the mapping
readers of a refreshed config directory through it. The teardown change adds a
resolver in the CLI layer and a root refusal in the shared instance resolver. A
test-only registry spans both.

### Components

```
internal/testhook (new leaf)      named points; Hit / Set
internal/safetext (new leaf)      control-character stripping for printed values
        ^            ^
        |            |
internal/configdir (new leaf) ----+        lock, @swap layout, journal, recovery
        ^                         |
        |                         |
internal/config ------------------+        Discover: moved-aside repair branch
        ^
internal/workspace                         snapshotwriter, snapshot, session_map,
        ^                                   state, apply, configreload
internal/watch                             state writers through Mutate
        ^
internal/cli                               session.go, worktree_destroy_resolve.go,
                                            session_lifecycle_cmd.go, list.go, apply.go
internal/worktree (leaf)                   DestroySession + argument validation
```

- **`internal/testhook`** (new): `type Point string`; constants
  `SnapshotCarriedOver`, `SnapshotMovedAside`, `ConfigDirLockContended`,
  `RootStateRead`; `Hit(Point) error`; `Set(Point, func() error) (restore
  func())`, which panics on a second registration. Imports only `sync` and
  `sync/atomic`; no environment reads, no `init()`.
- **`internal/configdir`** (new): the layout (`LockPath`, `SwapDir`, and the
  random `prev-`, `next-`, `trash-` and `stray-` names inside it),
  `HoldsSnapshot(dir)`, `Acquire(dir, mode)` with the nested-acquisition
  detector and the lock-ordering rule, `Mutate(dir, fn)` and `Read(dir, fn)`,
  `NewStaging(dir) (*Staging, error)` (called inside `Read`; creates the staging
  directory inside `D@swap` 0700 with `snap/` 0755 and a `lock` file created
  `O_CREAT|O_EXCL` and held exclusive), the journal (`writeJournal`,
  `readJournal`, `clearJournal`), `Recover(dir) (trash []string, err error)`
  implementing the rules below, and `RecoverMovedAside(dir)` for discovery.
  `configdir_unix.go` opens lock files with the repository's existing
  `O_NOFOLLOW|O_NONBLOCK` pair (`internal/workspace/contextprobe_unix.go`), so a
  planted symlink fails cleanly and a planted FIFO cannot block the open before
  the deadline logic runs; it then `fstat`s the descriptor and refuses anything
  that is not a regular file owned by the invoking user with no group or world
  write bit, and after `flock` succeeds it re-checks `(dev, ino)` against the
  path and reopens on a mismatch. It polls
  `syscall.Flock(LOCK_SH|LOCK_NB or LOCK_EX|LOCK_NB)` every 20 ms, calls
  `testhook.Hit(ConfigDirLockContended)` on the first busy try, and returns
  `timed out after 30s waiting for another niwa command using <dir> (lock
  <dir>.lock)`. Ownership checks live in the unix file too, since the type they
  need does not exist on other platforms, and `recover.go` is split the same
  way. Note that no production code performs an ownership check today -- the
  repository's only `syscall.Stat_t` read fills a `%d` in a diagnostic -- so this
  is new code with no precedent to copy and no `GOOS=windows` CI job to catch a
  build-tag mistake in it; the Windows build is checked per package in this
  change's own criteria. `configdir_other.go` returns a no-op release and a
  `Recover` that does nothing, with a comment stating that the platform gets
  neither ordering nor crash repair. The package copies the two marker filenames
  it tests for rather than importing them (it sits below the packages that define
  them), with a test that fails if either copy drifts, because a wrong marker
  name makes recovery pick the wrong side.
- **`internal/safetext`** (new leaf): `Strip(string) string`, removing C0, C1,
  DEL, U+2028, U+2029 and the Cf bidi and zero-width block. It is a leaf so that
  `internal/worktree`, which composes the kept-branch warning, and
  `internal/cli`, which prints the resolver's lines, can both import it. The
  existing candidates cannot serve: `internal/tui`'s sanitizer and every
  hand-rolled stripper in the repository match Cc only, and the one with the
  right coverage is unexported in `internal/cli`.
- **`internal/workspace/snapshotwriter.go`**: `refreshSnapshot` and
  `EnsureConfigSnapshotWithStatus` read the marker and `.git` inside
  `configdir.Read`; the no-drift branch re-reads and rewrites the marker inside
  `Mutate` with an atomic write. `materializeAndSwap` creates `D`'s parent if
  missing, creates its staging inside `Read`, fetches into `snap/` unlocked,
  then inside `Mutate` writes the marker, runs the three `preserve*` copies,
  calls `testhook.Hit(SnapshotCarriedOver)`, and swaps; afterwards it removes
  its staging directory. The three `preserve*` helpers switch from `Stat` and
  `ReadFile` to `Lstat` and refuse a symlinked source: today a symlink at
  `<configDir>/sessions` would be walked and its target's contents promoted by
  the swap to become the mapping store.
- **`internal/workspace/snapshot.go`**: `SwapSnapshotAtomic` takes its paths
  from `configdir`, writes the swap journal before its first rename and clears
  it after its second, moves `D` aside to `D@swap/prev-<random>` rather than to
  the fixed `<dir>.prev`, loses its unconditional preflight delete (there is no
  longer a fixed destination to clear), calls `testhook.Hit(SnapshotMovedAside)`
  between its two renames, and moves the old snapshot to a trash name instead of
  deleting it in place. It never reads or writes `<dir>.prev`, so an older
  binary's in-flight swap is invisible to it.
- **`internal/workspace/session_map.go`**: `WriteSessionMapping` and
  `DeleteSessionMapping` run inside `Mutate` on `<root>/.niwa`, create only
  `sessions/` (0700) with `os.Mkdir`, and fail if `.niwa` is missing;
  `ListSessionMappings` and `ReadSessionMapping` run inside `Read`. The
  resolver's filter is on each record's `session_id` field, since the store
  returns bodies rather than filenames. New
  `NewestMappingPerInstance([]SessionMapping) map[string]SessionMapping` keyed
  by `filepath.Clean(InstancePath)`, with the latest `Created` and
  first-in-session-id order on ties; `niwa list` adopts it, which changes its
  matching only for a hand-edited unclean path.
- **`internal/workspace/state.go`**: `SaveState` writes a unique temp file,
  sets 0644, renames it over `instance.json`, and goes through `Mutate`
  without `MkdirAll` when the target is a refreshed config directory. New
  `IsSingleInstanceLayout(root) bool`: no child instance, and the root's
  `instance.json` has a non-empty `instance_name`.
- **`internal/workspace/apply.go`**: `Create` and `Apply` call
  `testhook.Hit(RootStateRead)` beside their root `LoadState`;
  `saveWorkspaceRootDisclosures` returns early when `existing` is nil and the
  root `instance.json` exists.
- **`internal/workspace/configreload.go`**: the post-swap `config.Load` runs
  inside `Read`.
- **`internal/watch/state.go`**: `writeHandledState` and `SaveStagedRecord`
  run inside `Mutate` on `<root>/.niwa`, require `.niwa` to exist, and create
  only their own leaf with `os.Mkdir`.
- **`internal/config/discover.go`**: when `.niwa/workspace.toml` is missing
  but `.niwa.lock` and a `.niwa@swap/` holding a journal exist in a candidate
  directory owned by the current user, call `configdir.RecoverMovedAside` and
  re-check. At most one candidate per command is repaired, so a walk cannot
  multiply the bound.
- **`internal/worktree/worktree.go`**: `DestroySession` validates the record it
  read before either git call. The worktree path must be non-empty and resolve
  under `<instanceRoot>/.niwa/worktrees/`; the branch name must match the ref
  pattern in Decision 2 and is passed after `--`; a record whose worktree
  directory is missing is refused rather than reported clean by the
  uncommitted-changes guard, which now fails closed on any `stat` error rather
  than only on `ENOENT`. Record reads resolve through a root opened on the
  instance directory, and the kept-branch warning goes through
  `safetext.Strip`. `EffectiveBranchName` and the store's shape are unchanged,
  so a record niwa wrote behaves exactly as before.
- **`internal/workspace/bootstrap.go`**: `DefaultDestroySession`, the
  `niwa init --bootstrap` rollback, is the third copy of the destroy argv. It
  routes through the same validator rather than remaining the one unguarded
  copy.
- **`internal/cli/session.go`**: `discoverInstanceRoot` rebuilt on
  `ClassifyCwd` and `IsSingleInstanceLayout`; `errAtWorkspaceRoot` sentinel.
- **`internal/cli/worktree_destroy_resolve.go`** (new): the six functions of
  Decision 2.
- **`internal/cli/session_lifecycle_cmd.go`**: `runSessionDestroy`'s
  positional branch calls the resolver and refuses `--force` with a session;
  `runSessionLifecycleList` handles `errAtWorkspaceRoot` with exit 0.
- **`internal/cli/list.go`**, **`internal/cli/apply.go`**: adopt
  `NewestMappingPerInstance` and `IsSingleInstanceLayout`.

**Recovery rules**, run by `configdir.Recover` at the start of every `Mutate`
on unix, and not at all on other platforms. Recovery reads
`D@swap/journal.json` and acts only on the paths it names. It never scans for
directories that look like a moved-aside snapshot, and it never touches anything
outside `D@swap` except `D` itself.

0. No journal: nothing was interrupted. Go to rule 4. Anything at the legacy
   `D.prev` or `D.next` is left untouched, because it belongs to a binary this
   code cannot coordinate with; that binary clears it with its own preflight.
1. A journal exists and the staging directory it names still holds its `lock`
   (tried `LOCK_EX|LOCK_NB`, opened without `O_CREAT`): the owner is alive.
   Leave everything and clear nothing.
2. The owner is gone and `D` is missing: the swap was killed between its
   renames. Rename the journal's moved-aside path back to `D`, then remove the
   journal.
3. The owner is gone and `D` exists: the second rename landed. If the journal
   records that `D` was recreated after the move, rename `D` to
   `D@swap/stray-<random>` and keep it, then rename the moved-aside path back;
   otherwise rename the moved-aside path to a trash name. Remove the journal.
4. Staging directories inside `D@swap` that no journal names are trashed when
   their `lock` can be taken and left alone when it cannot. Leftover trash
   directories are folded into this run's trash list.

A path the journal names that is missing, or is not a directory, is a warning
rather than an error: recovery reports it once per directory per process and
lets the callback run. Making it fatal would fail every mapping write, watch
write and state write on the directory for as long as the entry existed. Only a
rename that still fails after one retry with a fresh name is fatal, and only to
the operation that needed it.

Trash is deleted after the lock is released, so two processes can end up
removing the same tree: the second sees `ENOENT` and `ENOTEMPTY` on entries the
first already took. Post-lock trash deletion is best-effort and its errors are
discarded, matching what `SwapSnapshotAtomic` already does with its old
snapshot. Surfacing them would turn a successful command into a failure for a
directory nobody needs any more.

### Key Interfaces

```go
// internal/configdir
type Mode int
const (Shared Mode = iota; Exclusive)
var Timeout = DefaultTimeout // 30 * time.Second
func LockPath(dir string) string                                // <dir>.lock
func SwapDir(dir string) string                                 // <dir>@swap
func HoldsSnapshot(dir string) bool                             // provenance marker, regular file
func Acquire(dir string, mode Mode) (release func(), err error) // errors on nested or out-of-order acquisition
func Mutate(dir string, fn func() error) error                  // EX, Recover (unix), fn, release, delete trash
func Read(dir string, fn func() error) error                    // SH, fn, release
func NewStaging(dir string) (*Staging, error)                   // call inside Read; inside <dir>@swap
func Recover(dir string) (trash []string, err error)            // caller holds EX; no-op on non-unix
func RecoverMovedAside(dir string) error                        // takes EX itself; one candidate

type Journal struct {
	MovedAside string // <dir>@swap/prev-<random>
	Staging    string // <dir>@swap/next-<random>
	Token      string
	PID        int
	StartedAt  time.Time
	Recreated  bool // D reappeared after the first rename
}

// internal/safetext
func Strip(s string) string // C0, C1, DEL, U+2028/9, Cf bidi and zero-width

// internal/testhook
type Point string
func Hit(p Point) error
func Set(p Point, fn func() error) (restore func()) // _test.go only; panics if already set

// internal/workspace
func NewestMappingPerInstance(ms []SessionMapping) map[string]SessionMapping
func IsSingleInstanceLayout(root string) bool

// internal/cli (unexported)
var errAtWorkspaceRoot error
func resolveDestroyScope() (destroyScope, error)
func loadMappingsForDestroy(workspaceRoot string) ([]workspace.SessionMapping, error)
func matchMappings(ms []workspace.SessionMapping, v string) []workspace.SessionMapping
func resolveDestroyTarget(s destroyScope, v string, ms []workspace.SessionMapping) (destroyTarget, error)
func checkSessionInstance(root string, m workspace.SessionMapping, all []workspace.SessionMapping) (instanceDir string, gone bool, err error)
func destroySessionWorktrees(cmd *cobra.Command, instanceDir string, m workspace.SessionMapping, git worktree.GitInvoker) error

// internal/worktree (unexported)
func validateSessionRecord(instanceRoot string, r *SessionRecord) error // path containment, branch shape
```

On-disk additions, per refreshed config directory: exactly two siblings, the
lock file `<dir>.lock` (regular file, 0600, never removed) and the working
directory `<dir>@swap/`. Everything else lives inside `@swap`: `journal.json`
while a swap is in flight, `next-<random>/` during a fetch, `prev-<random>/`
between the two renames, `trash-<random>/` awaiting deletion, and a kept
`stray-<random>/` only after recovery found the live directory recreated
mid-swap. Two siblings rather than five, and neither is a name an overlay clone
can produce.

### Data Flow

A refresh of `D`: create `D`'s parent if missing. `Read`: probe the marker;
create and lock the staging directory `D@swap/next-X/`. Fetch into
`D@swap/next-X/snap`. `Mutate`: recover; write the marker into `snap/`; carry
over from `D`; `Hit(SnapshotCarriedOver)`; write `D@swap/journal.json`; rename
`D` to `D@swap/prev-X`; `Hit(SnapshotMovedAside)`; rename `snap` to `D`; rename
`prev-X` to `D@swap/trash-Y`; clear the journal; release; delete
`D@swap/trash-*`. Remove `D@swap/next-X`.

A niwa write into `D` (mapping, watch state, `instance.json`): `Mutate`:
recover; `os.Mkdir` its leaf if absent; write a temp file; rename; release. It
therefore lands wholly before another refresh's carry-over (and is copied
across, for the three carried paths) or wholly after its swap, and it can never
recreate `D`.

A teardown by session: classify cwd; `Read`: scan mappings; match; R7 checks
against the snapshot, yielding the enumerated instance directory; for each
active worktree of that instance, re-`Lstat` the directory and call
`DestroySession`, which validates the record's own worktree path and branch
before it runs git. No directory lock is needed there: in the multi-instance
layout, lifecycle records live in the instance's own `.niwa/`, which no refresh
rotates.

## Implementation Approach

All phases land as commits in one pull request. Phase 1 is deliberately red:
its tests are recorded failing against today's code and turn green in the
phase named for each.

### Phase 1: Test seam and failing tests on today's code

Add `internal/testhook` with its guard test, and place three hook points in
today's code with no behavior change: `snapshot-carried-over`,
`snapshot-moved-aside`, and `root-state-read` in `Create` and `Apply`. Add the
forced tests for mapping write, mapping delete, watch state write during a
swap, refresh-versus-refresh, the reaper's mapping read, the two kill points,
and the root-state read, and record their failing `go test` output for the
pull request. Depends on nothing.

Deliverables:
- `internal/testhook/testhook.go`, `testhook_test.go`
- hook calls in `snapshotwriter.go`, `snapshot.go`, `apply.go`
- forced tests in `internal/workspace`, `internal/watch` and `internal/cli`

### Phase 2: Config-directory package

Add `internal/configdir` with the `@swap` layout helpers, `HoldsSnapshot`, the
lock with its owner-and-mode check, its post-`flock` inode re-check, its
nested-acquisition detector and ordering rule and its contended hook, `Mutate`
and `Read`, staging creation, the journal, and the recovery rules. Unit tests
cover contention within one process, nested and out-of-order acquisition failing
at once, a pre-existing lock file that is group-writable or another user's,
a lock file replaced between open and `flock`, timeout wording, an exclusive
waiter behind repeated shared holds, a dispatch-shaped sequence of acquisitions
under a 1-second bound, each recovery rule, a live journal whose staging lock is
held, a journal naming a path that no longer exists (warning, callback still
runs), and an overlay-shaped directory at every name the swap could otherwise
have used. Depends on Phase 1 for the contended hook.

Deliverables:
- `internal/configdir/configdir.go`, `configdir_unix.go`, `configdir_other.go`,
  `journal.go`, `recover_unix.go`, `recover_other.go`, tests
- `internal/safetext/safetext.go` and its table test

### Phase 3: Snapshot writer and in-place writer ordering

Reshape `materializeAndSwap` and `SwapSnapshotAtomic` around the package, moving
the swap's moved-aside destination into `@swap` and adding the journal write and
clear; route the marker reads, the no-drift rewrite and the post-swap reload
through it; switch the three `preserve*` helpers to `Lstat` and make them refuse
a symlinked source; route the mapping store and the watch state writers through
`Mutate` and `Read` and stop them recreating `.niwa`; add the `config.Discover`
branch. Add the root-scanner invariant test: a lock file and a populated
`@swap` beside the root leave `EnumerateInstances` and both reaper sweeps
unchanged. The mapping, watch, refresh, reaper and kill tests from Phase 1 turn
green. Depends on Phase 2.

Deliverables:
- `snapshotwriter.go`, `snapshot.go`, `session_map.go`, `configreload.go`,
  `internal/watch/state.go`, `internal/config/discover.go`

### Phase 4: `instance.json` guards

Make `SaveState` atomic and route refreshed-config-directory writes through
`Mutate`; add the skip-after-failed-read rule. The root-state tests from
Phase 1 turn green. Depends on Phase 2.

Deliverables:
- `state.go`, `apply.go`

### Phase 5: Root refusal

Add `IsSingleInstanceLayout` and switch `internal/cli/apply.go` to it; rebuild
`discoverInstanceRoot` with its layout table test; add the `worktree list`
branch. Depends on nothing earlier and can land before Phase 2; it changes
behavior for nine callers, so it lands on its own commit.

Deliverables:
- `internal/cli/session.go`, `session_lifecycle_cmd.go`, `apply.go`;
  `internal/workspace/state.go`

### Phase 6: Teardown resolution

Add `NewestMappingPerInstance` and switch `list.go` to it; add the destroy
resolver and wire `runSessionDestroy`, applying `IsSafeHandle` to the handle
field alongside the UUID filter and resolving both sides of the
`EnumerateInstances` membership comparison; validate the lifecycle record's
worktree path and branch inside `DestroySession`, make the uncommitted-changes
guard fail closed on any `stat` error, and route the same validation through
`workspace.DefaultDestroySession`; print through `safetext.Strip`. Table tests
for `matchMappings` and `resolveDestroyTarget`, command tests for every R9
outcome and the R10 table, record-validation tests covering a path escaping the
instance, an empty path, a `--upload-pack=`-shaped branch, a branch with a
control character and one with U+202E, an unsafe handle, and a symlinked
workspace root, plus the forced teardown-read test. Depends on Phase 3 (the
locked mapping read) and Phase 5.

Deliverables:
- `internal/cli/worktree_destroy_resolve.go`, `session_lifecycle_cmd.go`,
  `list.go`; `internal/workspace/session_map.go`,
  `internal/workspace/bootstrap.go`; `internal/worktree/worktree.go`

### Phase 7: Functional coverage and docs

Add functional scenarios for destroy by session id and handle from the
workspace root (unmerged branch kept), and recompose the four-way parallel
dispatch with a local config source. Update `docs/guides/worktree.md` for the
new id forms, outcome table and root behavior, and describe the `.niwa.lock`
file, the `.niwa@swap` working directory and what a leftover stray inside it
means in `docs/guides/workspace-config-sources.md`. Depends on Phases 3 and 6.

Deliverables:
- `test/functional/features/*.feature`, step additions
- `docs/guides/worktree.md`, `docs/guides/workspace-config-sources.md`

## Security Considerations

niwa runs as the invoking user with no elevated privilege, and this design adds
no operation the user couldn't already perform. The questions are whether
crafted or corrupt local files can make niwa act outside the workspace, and
whether the new files widen what other users can see or block.

**The boundary, stated once, because the rest of this section depends on it.**
A process running as the invoking user with write access to the workspace root
is outside the threat model. Such a process can rewrite
`.niwa/workspace.toml` directly, so nothing niwa does to its own auxiliary paths
changes what it can reach, and a defense that assumed otherwise would be
decoration. What *is* in the model is the content of files niwa parses and then
uses as arguments or prints: mapping fields and lifecycle records flow into git
argv positions and into a terminal, and those are attacker-influenced in the
ordinary case of a buggy or hostile tool writing a record, not only under a
deliberate attack. The auxiliary-path rules below are accident prevention --
keeping niwa from destroying a neighbouring overlay clone, an older binary's
in-flight swap, or a user's own file -- and they are described as that rather
than as a security control.

**Destroy input and mapping contents.** The value passed to
`niwa worktree destroy` is only compared as a string against fields of the
loaded mappings and against worktree ids of the current instance; it never
becomes a path until a lifecycle read that first requires eight lowercase hex
characters. Mapping files can be written by any process running as the user,
so their fields are untrusted. The resolver keeps only mappings whose
`session_id` field is a valid UUID, and drops the `handle` match for any mapping
whose handle fails the existing `watch.IsSafeHandle` (`[A-Za-z0-9_-]{1,128}`),
which is exported for exactly this purpose. It accepts a recorded
`instance_path` only if it is lexically a direct child of the workspace root
other than `.niwa`, exists, and is one of the directories `EnumerateInstances`
returns -- with both sides of that comparison resolved, since
`EnumerateInstances` returns lexical joins and resolves nothing, and comparing a
resolved path against an unresolved set mismatches under a symlinked root. That
membership test is the containment invariant, and it holds only because the
enumeration returns direct children of the workspace root: a mapping naming a
directory elsewhere is refused because it is not in the set, not because the
string was inspected. The path handed to `DestroySession` is that enumerated
directory, not the recorded string, and it is re-checked with `Lstat` right
before each destroy. That check narrows the window rather than closing it -- a
directory swapped for a symlink between the check and the git call would still
be followed -- which is why the record's own fields are validated at the point
of use rather than trusted because the instance path was checked. R6's
newest-mapping rule is not part of this: it reads the untrusted `created` field,
so it is a usability rail against acting on a superseded session and bounds
nothing against a crafted store.

Values interpolated into R9's lines have control characters stripped rather than
quoted, because R9 fixes those lines' literal shapes and quoting would change
them. The stripper is new: nothing in the repository does this job today, the
dispatch path deliberately refuses rather than strips, and every existing
stripper here is Cc-only, which leaves U+202E and U+200B intact. A new leaf
package `internal/safetext` covers C0, C1, DEL, U+2028, U+2029 and the Cf bidi
and zero-width block, and is a leaf precisely so that `internal/worktree` can
import it -- the kept-branch warning is composed there, carries a
record-supplied branch name into a paste-ready `git branch -D` line, and is now
emitted once per worktree rather than once per command.

**Scope of teardown by session.** Teardown by session applies the guards
`niwa worktree destroy` already has to each active lifecycle record of one
instance, and never removes the mapping, the instance or its clones. Those
guards were written for records niwa itself wrote, so this design adds the
validation they assumed: before any git call, `DestroySession` requires the
record's worktree path to be non-empty and to resolve under the instance's
worktrees directory, requires the branch name to match the exact ref pattern in
Decision 2 -- non-empty; no byte below 0x20 and no DEL; no space; none of `~ ^ :
? * [ \`; no `..`; no leading `-`; no trailing `.lock`; no leading or trailing
`/` and no `//`; not the single character `@` -- and passes it after `--`. A
record whose worktree directory is missing is refused rather than reported clean;
today the uncommitted-changes guard fails open on a missing tree, and it now
fails closed on any `stat` error rather than only on `ENOENT`, since a permission
error or an `ELOOP` currently falls through to the git call too. The same
validation is applied to `workspace.DefaultDestroySession`, the
`niwa init --bootstrap` rollback, which is a third copy of the same argv with no
checks and no `--`. `--force` is refused with a session id or handle (usage error,
exit 2); forcing stays available one worktree at a time through the worktree id
or `--by-path`, so a mistyped or prefix-matched id can't discard a whole
instance's uncommitted work and unmerged branches.

**The lock file.** `<dir>.lock` is opened with `O_NOFOLLOW|O_NONBLOCK`, so a
planted symlink can't redirect the open and a planted FIFO can't block it before
the timeout logic is reached. Creating it 0600 says nothing about a file that
already exists, since `O_CREAT` changes neither mode nor owner, so the
descriptor is `fstat`ed and refused unless it is a regular file owned by the
invoking user with no group or world write bit. Without that check the global
lock, whose path follows the environment-controlled `XDG_CONFIG_HOME`, and a
workspace root under a group-writable parent both let another user pre-create
the lock 0666 and hold it until every niwa command times out. After `flock`
succeeds, the descriptor's `(dev, ino)` is compared against the path's; a
mismatch means the file was replaced between open and lock, so niwa closes,
reopens and retries within the bound rather than proceeding on a private inode
while another process holds a different one.

**Why the auxiliary paths moved out of the sibling namespace.** Overlay clones
are named `<org>-<repo>` in the same per-user directory, with neither component
charset-checked, so a repository can legitimately be named `tools.prev` and land
byte-identical to where a `<dir>.prev` scheme puts a previous snapshot. The
obvious defense -- recognize niwa's own directories by the provenance marker --
does not work, and it is worth being explicit about why, because an earlier
draft of this design claimed it did: an overlay clone *is* a refreshed config
directory maintained by this same pipeline, so it carries the marker too, and
`EnsureOverlaySnapshot` uses the marker's presence to decide the clone exists.
The marker proves a directory is niwa's; it cannot prove which one it belongs to
or what role it plays.

So niwa takes exactly two sibling names -- `<dir>.lock` and `<dir>@swap` -- and
`@` is a character no forge org or repo name can contain, so neither collides
with an overlay. Everything rotated lives inside `@swap` under random names. A
directory that merely resembles a moved-aside snapshot is never acted on,
because recovery reads the swap journal and touches only the paths it names,
rather than inferring role from a name or from content. That is also what keeps
recovery out of a concurrently running older binary's swap: an older binary
writes no journal, so its in-flight `<dir>.prev` is invisible here. Without
that, a new binary's `Mutate` -- now on the path of every mapping, watch and
`instance.json` write -- would read the old binary's moved-aside directory as a
crash and rename it back mid-swap, which is the ordinary condition during an
upgrade with a long-running `niwa watch` or a backgrounded dispatch.

Each refresh stages in a directory inside `@swap` (0700), holding `lock` created
`O_CREAT|O_EXCL` and `snap/` at 0755, so the live directory keeps its mode after
the swap. Removals rename to a trash name and delete with `safeRemoveAll`, which
removes a top-level symlink without following it. Because there is no fixed
rename destination left, no pre-existing entry at a guessable path can fail a
rename, and the one remaining collision case -- two random names colliding --
retries once with a fresh name. A journal-named path that is missing or is not a
directory is warned about rather than treated as fatal, since recovery runs at
the head of every exclusive section and a fatal reading would wedge every write
on the directory.

None of this defends against a same-user process with write access to the
workspace root, and it is not meant to: such a process can forge a journal as
easily as anything else, and can rewrite `workspace.toml` directly without
bothering. The rules keep niwa from destroying data that is legitimately not
its own.

Recovery runs only on unix, because the lock it depends on exists only there;
running the renames unserialized would be worse than leaving the directory as
the crash left it. A Windows user whose refresh is killed mid-swap still has to
re-run the command that repairs it, which is the behavior that exists today.

**Symlinked sources in the snapshot.** The three `preserve*` carry-over helpers
use `Lstat` and refuse a symlinked source. Without that, a symlink planted at
`<configDir>/sessions` would be walked and its target's contents copied into the
staging directory, and the swap would promote them to be the mapping store. The
same reasoning drives the R7 destroy-side rule that a dangling symlink or a file
at an instance path is a refusal rather than a "gone" verdict: a "gone" verdict
lets the caller reclaim, and the two sides must not disagree about what counts
as an instance directory.

**Recovery during configuration discovery.** `config.Discover` repairs a
moved-aside `.niwa` only when the candidate directory, its `.niwa.lock` and its
`.niwa@swap` belong to the current user and the swap directory holds a journal,
and repairs at most one candidate per command, so a deep walk cannot turn into
an unbounded sequence of renames. A walk up through a shared directory therefore
never renames another user's files, and never loads another user's directory as
workspace configuration because of this branch.

This does make discovery lock-taking, and discovery is reached from paths that
previously only read: shell completion, the SessionStart hook and `niwa watch`,
across sixteen call sites. A TAB press can therefore block for up to the bound
if another command holds the lock. The bound is what keeps that an annoyance
rather than a hang, and the one-repair limit keeps it to a single wait rather
than one per ancestor directory.

**File modes and data exposure.** Lock files are empty. The mapping store
keeps 0700 for `sessions/` and 0600 for its files, both in the writer (which
now creates only the `sessions/` leaf, still 0700) and through the snapshot
carry-over, which already restores the source mode. The atomic `SaveState`
writes a uniquely named temp file, sets it to 0644 to match today's
`instance.json`, and renames it into place. Error messages name directories,
lock paths, session ids and instance paths only, all on the invoking user's own
stderr.

**Test-only seam.** `internal/testhook` is compiled into release binaries, but
nothing outside Go code can reach it: there is no environment variable, flag or
file trigger. With no hook set, `Hit` returns nil, and a guard test parses
every non-test Go file and fails if any refers to `Set`. The re-exec helper the
crash tests kill is recognized only by code in `_test.go` files, so its marker
does nothing in a release binary. Pause and exit faults were kept out of
`testfault` on purpose, because that package honors an environment variable in
release builds.

**Denial of service.** No lock wait is unbounded: a command that can't get the
lock within 30 seconds fails with an error naming the directory. Another user
cannot hold the lock as long as the file is niwa's own 0600 file, and the
owner-and-mode check above is what makes that true of a pre-existing file rather
than only of one niwa created. A process running as the same user can keep niwa
failing by holding the lock, but such a process could as well kill niwa. flock
doesn't queue waiters fairly, so a steady stream
of overlapping shared readers can in principle push a writer to the bound.
Shared holds are not all the same length: a mapping scan is one directory read,
but a config parse and a staging creation are longer, and `niwa watch` adds
exclusive traffic of its own. Reaching the bound still needs sustained
overlapping commands against one config directory, which is a local,
same-user condition, and it is accepted. Non-unix platforms get neither
ordering nor crash recovery, as stated at the fallback.

**The new siblings versus the root scanners.** `<root>/.niwa.lock` and
`<root>/.niwa@swap` are direct children of the workspace root, where
`EnumerateInstances` and both reaper sweeps scan. They must never be mistaken
for instances. Enumeration requires `<child>/.niwa/instance.json`, and the
carry-over puts that file at `snap/instance.json` inside staging, so the
invariant holds by one path segment -- and this change is what reshapes that
layout, so it is stated here and pinned by a test that creates a lock file, a
swap directory with staging, trash and stray inside it, and asserts
`EnumerateInstances` and both reaper sweeps return nothing extra. Worth noting
for that test: `EnumerateInstances` uses `os.Stat` with no type check, so a
*directory* named `instance.json` would qualify a child today.

**Kept stray directories.** A stray is kept deliberately, never swept, and rule
3 fires whenever an in-place write lands inside a swap window -- which, until
this change is everywhere, is the ordinary condition it exists to detect. They
therefore accumulate. The config-sources guide says what they are and that
removing one is safe once its contents have been checked; nothing deletes one
automatically, because the whole point is that niwa could not tell what it was.

**Dependencies.** No module is added. The lock uses `syscall.Flock` from the
standard library, as niwa's existing Codex trust lock does; `os.OpenRoot`, used
for the record reads, is standard library too and available at the `go 1.25.3`
this module already declares.

## Consequences

### Positive

- Parallel provisioning is safe for mappings: no lost write, no resurrected
  delete, no directory left without its configuration, and no refresh failing
  because of another, with waits measured in local file operations rather than
  fetch time.
- A crash between the two renames, or a stray write during a swap, no longer
  destroys the previous snapshot; the next command repairs it and keeps what
  was written.
- Teardown accepts the ids developers and scripts actually hold, from
  anywhere in the workspace, with a stable, script-readable outcome contract,
  and every other worktree subcommand stops treating the root as an instance.
- The swap's file names have one owner (for workspace configuration
  directories; `internal/plugin`'s own `.next`/`.prev` rotation is untouched),
  and the two entry points make recovery-first and delete-outside-the-lock
  automatic.
- niwa's auxiliary paths leave the namespace it shares with overlay clones, so a
  repository whose name collides with a swap suffix is no longer at risk of
  being deleted or promoted -- a hazard that exists in today's code, which
  clears `<dir>.prev` unconditionally.
- Recovery acts on a journal rather than on what a directory looks like, so it
  leaves a concurrently running older binary's swap alone instead of undoing it
  mid-flight.
- `DestroySession` stops trusting the record it just read: a crafted or corrupt
  lifecycle record can no longer point git at a path outside the instance, pass
  a branch name that reads as a flag, or slip past a dirty-tree guard that
  today returns clean for a missing worktree.
- The race tests are deterministic and fail on today's code.

### Negative

- Every mapping read and write, watch state write and refreshed-directory
  `instance.json` write now takes a file lock, and a destroy by worktree id
  inside an instance now reads the root store, so under contention past the
  bound it can fail (exit 1).
- Three new internal packages (`testhook`, `configdir`, `safetext`), and two
  persistent siblings beside each refreshed config directory: `<dir>.lock` and
  `<dir>@swap/`.
- Kept stray directories accumulate inside `@swap` and nothing sweeps them,
  because niwa could not tell what they were in the first place.
- A swap now writes and clears a journal file, so an interrupted swap leaves one
  more artifact than before -- which is the point, but it is one more thing a
  user may find.
- `config.Discover` can now take a lock and rename a directory, though only in
  the narrow moved-aside case; that makes shell completion, the SessionStart
  hook and `niwa watch` lock-taking, so a TAB press can wait out the bound.
- An exclusive waiter can be starved by a stream of shared readers until the
  bound.
- Readers of config content during a long pipeline can still hit the rename
  window, and the ephemeral-session hook's mode read is not locked.
- Non-unix platforms keep today's races, and get no crash repair either.
- The instance directory is re-checked with `Lstat` immediately before each
  destroy, which narrows the swap window rather than closing it.
- A reap that removes an instance between teardown's read and its destroys
  shows up as per-worktree errors and exit 1.
- `internal/worktree` stops being an unchanged leaf: record validation lands
  there, so a record written by an older niwa is read by stricter code.
- niwa now has two flock helpers doing similar work (the Codex trust lock and
  `internal/configdir`).

### Mitigations

- The lock is held only for local copies and renames, never across a fetch or
  a large delete; the 30-second bound makes a stuck holder or a starved waiter
  an error, not a hang; nested acquisition fails at once.
- The lock files are regular files and are ignored by instance enumeration, and
  the invariant is pinned by a test rather than left to hold by coincidence.
  Deleting one is harmless only while no niwa command holds it; the post-`flock`
  `(dev, ino)` re-check is what turns the unsafe case into a retry instead of a
  silent loss of ordering. Trash is swept on the next refresh; a stray directory
  is kept deliberately and documented in the config-sources guide.
- The discovery branch requires ownership checks and fires only when
  `workspace.toml` is missing next to both `.niwa.lock` and a journal-bearing
  `.niwa@swap`.
- The unlocked-reader window and the hook read are listed in the PRD's Known
  Limitations; the atomic exchange swap is a possible later hardening that
  closes the window on supporting filesystems.
- The non-unix gap is documented at the no-op fallback, matching niwa's
  existing lock; running recovery's renames unserialized would be worse than
  leaving the directory as the crash left it.
- The remaining swap window is why the record's own fields are validated where
  they are used rather than trusted because the instance path was checked.
- The validation accepts every shape niwa writes today -- a worktree under the
  instance's worktrees directory and a branch name from `EffectiveBranchName` --
  so only a record niwa did not write is refused.
- The branch pattern is spelled out byte by byte rather than described, because
  it is the only barrier between an unvalidated JSON field and two git argv
  positions; `safetext.Strip` and the handle's `IsSafeHandle` filter are the
  second and third layers on what reaches a terminal.
- The reap race fails closed: nothing is deleted wrongly, and exit 1 is R9's
  "refused" signal that tells a script to look.
- The Codex trust lock can move onto `internal/configdir`'s lock primitive in
  a later change; nothing here depends on it.
