---
schema: design/v1
status: Proposed
problem: |
  niwa removes a stopped instance only when its session has been deleted or a
  person names it to `niwa destroy`, and nothing in niwa can be told when a
  unit's work is finished. `niwa reap`, which also runs before every dispatch,
  create and watch provisioning, checks liveness and nothing else: it never
  calls the loss scan (`workspace.ScanInstance`), so deleting a worker's
  session by hand turns the next dispatch into an unchecked delete. Destroy
  does scan, but it removes the session before the directory, so a session
  whose worktree sits inside the instance survives it. There is no stopped
  state, no size, no age and no over answer anywhere in niwa's records, so
  `niwa list` can't say which units are done or what they cost, and finished
  units pile up until a person clears each one.
decision: |
  niwa gains one removal rule, coded once in a new `internal/removal` package,
  and one bounded pass that every removal niwa makes on its own goes through:
  the opportunistic reap before dispatch, create and watch, `niwa reap`, the
  backstop, failed-dispatch cleanup, the trim and the new `niwa retire`. A
  workspace names an over command in `[session.over]`, which niwa runs with
  the `[session.before_remove]` calling shape and reads only by exit status; on
  exit 0 niwa writes the retire mark at `<instance>/.niwa/retired.json` in the
  earlier workspace-level remover's format. A marked unit is reaped once the
  rule passes, worktrees before the session and never forced. Per-unit records
  (`keep.json`, `discard.json`, `pids/`, `unit.json`) sit in the instance's
  `.niwa/`; host state (the kept set) sits at `~/.niwa/w-<key>/kept-files/`.
  `niwa list` and a fixed dispatch line show every stopped unit nobody retired
  with its answer, age and size. Delivery comes in three parts: the rule and
  worktrees; the over question, mark and listing; the keep record, kept set
  and launch record.
rationale: |
  Putting the rule in one package that every removal calls is the only way
  the verdict-only mode, the retire, `niwa reap` and the pass before dispatch
  can give the same answer, which the PRD requires and a plugin depends on.
  Copying the existing removal-gate pattern for the over seam keeps niwa free
  of koto and forge calls and gives workspaces a contract they already know.
  Following the earlier remover's mark format, rather than inventing niwa's
  own, lets marks written there count here and leaves that work free to settle
  the points it still holds open. Keeping per-unit records inside the instance
  makes them go with the instance and survive `niwa apply`, while the kept set
  follows niwa's host-state location rule in a directory of its own, so it
  never collides with the whole-instance `kept/` directory the plugin release
  store already scans. A single 30-second budget per pass, with fair ordering,
  caps what a dispatch waits for without ever changing what the rule decides.
upstream: docs/prds/PRD-over-remover.md
decision_provenance: inline-resolved
---

# DESIGN: niwa removes a finished unit that a workspace command says is over

## Status

Proposed

## Context and Problem Statement

A unit of work in niwa is a session, the instance it runs in, its Claude Code
job directory (`~/.claude/jobs/<id>/`) and its transcript
(`~/.claude/projects/<encoded cwd>/<session>.jsonl`). niwa removes a stopped
instance on two signals only, and neither one asks whether the work is done.

The first is the reap. `niwa reap` (`internal/cli/reap.go`, a Cobra command
with no flags and no arguments) and its opportunistic form,
`reapOpportunistically` (reap.go:985-995), run `reapWorkspace` and
`sweepHookWorktrees`. `reapOpportunistically` is called before every
`niwa create` (create.go:182-185), every `niwa dispatch` (dispatch.go:594-596)
and every review instance `niwa watch` provisions (watch.go:792), with errors
swallowed. `reapWorkspace` (reap.go:610-717) runs a mapped sweep
(`selectReapTargets`) that joins instance records with session mappings in
`<root>/.niwa/sessions/` and targets an ephemeral instance once its Claude job
entry is gone, a stored-mapping walk, a launch-record sweep, and a backstop
(`reapBackstop`) for unmapped dispatch-named instances older than 30 minutes
that don't carry `.niwa/dispatch-retain`. Each target is taken under the
instance lock (`takeInstanceLock`, instance_lock.go), re-read, offered to the
`[session.before_remove]` gate, and removed by `workspace.DestroyInstance`
(destroy.go:166-186), which prunes plugin records, withdraws trust grants and
runs `os.RemoveAll`. The guards are liveness only. Nothing on the reap path
calls `workspace.ScanInstance`; its only callers are in `niwa destroy`. That is
tsukumogami/niwa#300. So the reap is safe only while a session's job entry
exists, and a person who deletes a worker's session by hand turns the next
dispatch into an unchecked delete of that worker's instance.

The second is `niwa destroy` (`internal/cli/destroy.go`). It takes the
instance lock, refuses a running session or a veto in `destroyPreflight`, runs
`workspace.ScanInstance` unless `--force`, then runs `claude rm` on each
mapped session (`removeOwningSessionsListed`, destroy_session.go:183-231) and
only after that removes the directory. niwa's worktrees live under
`<instance>/.niwa/worktrees/` and go with the directory, so at the moment
`claude rm` runs, a session whose worktree sits inside the instance still has
that worktree, Claude Code refuses to drop it, and the destroy finishes with
the session left behind. Destroy's scan also counts every untracked file as
loss and has no mode that shows what it found without acting.

Neither path has a way to learn that a unit is finished. The only workspace
hook near removal, `[session.before_remove]`, can veto a removal but can't
start one. niwa keeps no stopped state, no size, no age and no answer about a
unit, so `niwa list` (list.go, `--json` only) prints a name, a session name
and a resume command, and nothing that says which of forty instances are done
or how much disk they hold. On a host running parallel agent work, finished
instances, clones, multi-gigabyte build trees, transcripts and dispatch
briefs stay until a person looks at each one, and that doesn't happen.

The accepted PRD (`docs/prds/PRD-over-remover.md`) states what has to change.
In implementation terms: a workspace names one command that answers whether a
stopped unit is over, niwa reads only its exit status and writes a durable
mark on yes, and every removal niwa makes on its own, the reap of a deleted
session's instance included, goes through one rule that never takes a path it
can't regenerate. A workspace that names no command loses nothing it didn't
ask to lose, sees its stopped units with answer, age and size, and has one
command to retire a unit under the same rule. The shape of that rule, and the
formats of the command contract and the retire mark, come from an earlier
workspace-level remover built first in one workspace outside niwa. That work
hasn't landed, so every point taken from it is an open dependency here, listed
in its own section below.

## Decision Drivers

- **D1. One rule, coded once.** The reap of a deleted session's instance, the
  reap of a marked unit, the backstop, failed-dispatch cleanup, the trim and a
  retire must all reach the same verdict for the same unit, and a workflow
  plugin must be able to ask niwa for that verdict instead of carrying a copy
  (R10, R29).
- **D2. Unique work is never taken, and doubt keeps.** A unit goes only when
  every path in it is regenerable, handed forward or declared disposable; a
  git error, an unreachable remote, an unreadable path or a missing record
  keeps (R11, R14, R15). The verdict-only mode must change nothing on disk,
  `.git` included (R29).
- **D3. niwa never decides over on its own.** It reads an exit status and
  nothing else, and the pass makes no koto or forge call to decide over (R3,
  R4).
- **D4. The earlier remover's formats are followed, not settled here.** niwa
  reads and writes the retire mark in that work's format and place, and treats
  every other point taken from it as open until it lands (R6, open
  dependencies).
- **D5. Bounded, and only inside niwa commands.** No daemon, timer or
  background process; each all-unit pass finishes inside a fixed budget, and a
  unit it doesn't reach waits for a later pass, never forever (R21, R34).
- **D6. The listing is local.** `niwa list` reads answer, age and size from
  local state, with no network call and no over run (R36).
- **D7. Host state follows the location rule.** Large host-level state niwa
  owns lives under `~/.niwa/`, in a directory keyed by the workspace root's
  canonical path, never under the root's `.niwa/` or inside an instance (R26).
- **D8. A reap on a mark is undoable for a known window.** niwa holds the
  launch record a restore replays before it reaps on a mark, and refuses a
  restore past the window with the date it ended (R18, R32, R33).
- **D9. No force, worktrees first.** The pass never passes niwa's `--force` or
  Claude Code's flags that discard unpushed work or force-remove a worktree,
  and it removes worktrees before the session so none is left behind (R16,
  R17).
- **D10. A user of niwa alone is no worse off on scratch than with destroy.**
  The project owner chose job scratch disposable by default where no handoff
  brief exists, with a setting to make it unique (R28, R35).
- **D11. Today's paths keep working.** The `[session.before_remove]` veto still
  applies to every removal, deleting a session still leads to its reap, and
  `niwa destroy` keeps its own checks (R20, Out of Scope).

## Considered Options

### Decision 1: Where the rule lives and how every removal reaches it

Resolved inline.

The PRD lists six places niwa removes something without a person naming it,
and wants the same answer from all of them and from the verdict-only mode.
Today each place carries its own guards: the mapped sweep and backstop in
reap.go, the worktree sweep in reap_worktrees.go, destroy's preflight and
scan, the watch's discard. Adding a loss scan to each would give six copies of
the rule. The question is where the rule's code lives, what drives it, and how
the time budget the PRD asks for is enforced around commands and network
calls that each have their own timeouts.

The existing loss scan, `workspace.ScanInstance` (scan.go), is close but not
the rule. It counts every untracked file as loss, contacts the forge to find
unlanded pull requests, skips nested repositories it isn't told about, and has
no idea of build trees, generated files, job scratch, patterns, discard
records, keep records or handoff briefs. Destroy depends on it as it is, and
the PRD leaves destroy's checks alone.

Key assumptions:
- The rule can run on git plumbing alone (`git status --porcelain=v2`,
  `git for-each-ref`, `git stash list`, `git ls-remote`, `git worktree list`,
  `git merge-tree --write-tree`) with no forge call. Git 2.38 or newer is
  needed for `merge-tree --write-tree`; an older git makes the
  merged-by-content check undecidable, which keeps.
- Those plumbing calls give correct answers with system and global git
  configuration ignored and with the command-bearing settings below
  overridden, so the rule never needs to run a program a repository names.
- `claude agents --json --all` and one process listing per pass are cheap
  enough to run before each dispatch (destroy already runs the first).
- A 30-second budget is acceptable latency added to a dispatch in the worst
  case; typical over runs take a few seconds.
- A rename within one directory is atomic, and a process already inside the
  tree keeps its working directory and descriptors across it, so it shows up
  under the new path in the next process listing.

#### Chosen: a new `internal/removal` package driven by one pass orchestrator

The rule moves into a new package, `internal/removal`. Its entry point,
`removal.Evaluate(ctx, unit, policy) Verdict`, reads and never writes: it
returns exactly one of reap, keep, or keep and trim, every check that keeps in
the fixed order R11 sets (liveness; a keep record's hold; a missing or
unreadable record the rule needs, the launch record for a reap on a mark
included; each repository in path order, with git errors and unreachable
remotes first, then commits and refs no live remote ref holds, stashes,
modified tracked files, untracked files; paths outside every repository; named
files missing or changed), the evidence for each, and the class of every path
(tracked, pushed, build tree, generated, disposable or unique, with a
git-ignored or job-scratch notice where that applies). Every check runs even
after one keeps, so the verdict-only mode can name them all, and the verdict
names the first.

The package also settles the mechanics the PRD left to the design:

- **Repositories are found by walking.** The walk covers the instance and the
  unit's job directory, finds every `.git` directory or file (clones,
  worktrees, submodules, nested `git init` repositories), and doesn't descend
  into a build tree except to sum its size. A nested repository the outer one
  ignores is still a repository with its own checks, so its unpushed commit
  keeps the unit rather than passing as a git-ignored path. The walk uses
  `Lstat` and never follows a symlink: a link is a path of its own, classed by
  its own name, and nothing behind it is read, summed or searched for
  repositories. It stays on the filesystem the instance sits on; a directory
  on another device keeps the unit with "another filesystem at <path>" as
  evidence. A `.git` file whose `gitdir:` (or the `commondir` it names)
  resolves outside the instance and the job directory keeps the unit with
  "external git directory" as evidence and is not followed.
- **Git runs in a fixed, hardened environment.** Every git call niwa's
  removal makes (the rule's checks, the scratch fetch, the re-read, the
  tombstone check and `git worktree remove` in the pass and in
  `runDestroyInstance`) is built by one function, `gitCommand` in
  `internal/removal/git.go`, and no other code in the package or the pass
  starts git. Decision 8 gives the settings and the reasons. In short: no
  inherited environment, system and global configuration ignored, hooks,
  fsmonitor, pagers, askpass and credential helpers turned off, only `https`
  and `ssh` allowed, `--git-dir` and `--work-tree` passed from the walk, and a
  repository whose own configuration names a filter, diff or merge program
  kept with that key as evidence rather than run. Network calls take an
  explicit, validated URL and run against the scratch object store, never
  inside the unit's repository.
- **Held means a live remote ref holds it.** A local commit is held when it is
  an ancestor of a tip `git ls-remote` returns for one of the repository's
  remotes. Merged by content means `git merge-tree --write-tree` of the
  remote default branch's live tip and the local branch yields the default
  branch's own tree, which is what a squash merge leaves. When the live tip's
  objects aren't in the clone, the check fetches them into a scratch object
  store outside the unit (a temporary bare repository whose
  `objects/info/alternates` points at the clone's objects), so nothing under
  the unit's `.git` changes, which the verdict-only mode needs. Each network
  call runs under a 10-second context; an error or timeout keeps with the git
  error, credentials scrubbed, as evidence. A remote whose URL isn't `https`,
  `ssh` or the scp-like `host:path` form, or whose host or path starts with
  `-`, is never contacted and keeps with "remote URL not allowed" as
  evidence.
- **Build trees** are git-ignored directories carrying `CACHEDIR.TAG` with the
  standard signature line (cargo's `target/` and any tool following the Cache
  Directory Tagging convention), plus directories a workspace declares for a
  repository in `[repos.<name>] build_trees`, given as exact paths relative to
  the repository root with a trailing slash and no wildcards.
- **Generated paths** are the files niwa or Claude Code writes again on
  provisioning: `instance.json` and the other files under `<instance>/.niwa/`
  that niwa manages, the files `managed_files` in `instance.json` lists,
  context files, plugin and marketplace trees, and a job directory's
  `state.json` and `timeline.jsonl`.
- **Processes** come from one listing per pass: `/proc/<pid>/cwd` and
  `/proc/<pid>/fd` on Linux, `lsof -n -P -w -F pn` on macOS. A failed listing
  is undecidable and keeps every unit it would have cleared.

A pass orchestrator, `runPass(ctx, root, mode, scope)` in a new
`internal/cli/pass.go`, drives the rule. `reapOpportunistically` and
`runReap` call it for every unit; `niwa retire` calls it for one; the
verdict-only mode calls it with acting turned off. The five things
`reapWorkspace` does today become sources of candidates feeding the same
loop: units whose session is gone (today's mapped sweep), stopped units the
over command may answer, marked units, unmapped dispatch instances (the
backstop, which still honors `.niwa/dispatch-retain`), and trim candidates.
Failed-dispatch cleanup and the watch's discard call `removal.Evaluate` for
their one instance before `destroyInstanceFunc`, and the hook worktree sweep
evaluates each worktree's repository checks before `worktree.DestroySession`.
The before-remove gate, the instance lock, the re-read before removal and the
on-reap step stay as they are and wrap the rule's verdict. The removal itself
gains two guards. `workspace.DestroyInstance` `Lstat`s the instance directory,
refuses a symlink, and refuses any path that isn't a direct child of the
resolved workspace root. And the last step before it renames the instance to a
tombstone and checks again, so a writer that arrives after the re-read finds
no path to write to.

The tombstone closes most of the gap between the last check and the delete.
The re-read comes before seconds of worktree removal, `claude rm`, on-reap
and `os.RemoveAll`, and the instance lock binds only niwa, so a process the
listing missed or a helper that starts late could otherwise write into the
instance after the re-read and have that write deleted unseen. After on-reap,
and immediately before `DestroyInstance`, `actReap` calls a new
`workspace.TombstoneInstance(root, name)`, which renames `<root>/<instance>`
to `<root>/.niwa-tombstone-<instance>-<8 hex>` in the same directory. A
writer that opens a path under the old name from then on gets "no such
file". niwa then repeats the cheap checks on the tombstone: a fresh process
listing for working directories and open files under it,
`git status --porcelain=v2` of every repository the walk found, and the
hashes of the records in `.niwa/`. Any difference renames it back with
`workspace.UntombstoneInstance` and keeps the unit with "changed during the
pass", left as the delete-then-reap path leaves one, its sessions and
worktrees already gone and the unit listed. Otherwise `DestroyInstance`
removes the tombstone, which is a direct child of the root and passes its
checks. A pass that finds a tombstone at its start (one an interrupted pass
left) renames it back when the instance name is free and evaluates it as
usual, or keeps and lists it when the name is taken. Instance enumeration
skips names starting with `.niwa-`, as it skips `.niwa` and `.niwa-launch`
today.

The budget is one deadline. An all-unit pass gets 30 seconds from its start,
the figure the existing removal gate already uses per pass
(`defaultRemovalGateBudget`), and every command and call inside it shares
that deadline: over runs, loss scans, the gate (its budget becomes the
smaller of 30 seconds and what remains), the on-reap step and removals.
Units are taken oldest-first by the time the pass last reached them, units
never reached first, so a unit left over by one pass is near the front of the
next. An over run starts only when the remaining budget covers its per-run
timeout, a loss scan only when 10 seconds remain, and a removal only when 5
seconds remain; a removal already started finishes. A unit the pass doesn't
start is "not reached": it gets no answer and no mark from that pass, and the
count goes to the dispatch line and `niwa reap`'s output. A retire's
one-unit pass isn't bound by the all-unit budget; its commands keep their
own timeouts. The budget caps how many units a pass handles and never what
the rule decides about one.

#### Alternatives Considered

**Extend `workspace.ScanInstance` and call it from every removal site.** This
reuses a scanner destroy already trusts and gives #300 its narrowest fix.
Rejected because the scanner's classes are destroy's, not the rule's: it
counts every untracked and ignored path as loss, calls the forge for unlanded
pull requests (which D3 forbids in the pass), and knows nothing of build
trees, patterns, discard and keep records or briefs. Bending it to the rule
would change destroy's checks, which the PRD keeps (D11), and leave six
callers each deciding how to combine the scan with liveness and records, so
the verdicts would still drift (D1).

**Run the earlier workspace-level remover's pass from niwa.** That remover
already implements the rule as shell, with its own check order and evidence.
Rejected because it lives in one workspace, pins its own binaries, reads its
own state outside niwa, and decides over with koto and forge calls, all of
which niwa must not depend on (D3); and a shell pass called from Go can't share
niwa's lock, deadline or re-read.

**Give each step its own budget, as today.** Today's gate gets 30 seconds per
sub-pass and on-reap 30 seconds per step, so up to about 90 seconds of gate
time alone. Rejected because the PRD bounds the whole pass with one figure
(R34), and a per-step budget can't promise that; it also lets an early step
starve later units without saying so.

**Delete straight after the re-read, with no tombstone.** No extra step.
Rejected because the window it leaves spans every acting step, seconds long,
and nothing stops a write by path in it.

**Make the tree read-only before deleting.** Writes fail outright. Rejected
because it costs a second full walk, a process running as the same user can
change the mode back, and a failure part-way leaves a half read-only
instance the person has to repair.

**Stop processes inside the instance before deleting.** Rejected because the
pass never signals a process; a live process keeps its unit.

### Decision 2: The over seam and the retire mark

Resolved inline.

niwa needs a command a workspace names, a way to run it that can't hang a
dispatch, a mark that says a unit is over, and answers to three questions the
PRD left open: whether later work in a unit voids a mark, whether one pass may
both mark and reap a unit, and what niwa does when the configured command
writes a mark itself. The earlier remover's contract shapes all of it: exit 0
for over, 1 for not over with the reason on the last output line, 2 for a
usage error, 3 when a fact couldn't be read, 124 on its deadline; the
instance path as an argument or in `NIWA_REMOVE_PATH`; a 20-second default
deadline; a check-only form that writes nothing and a form that also writes
the mark. Its mark is `<instance>/.niwa/retired.json`, a JSON file with a
schema name namespaced to that remover, the instance's name and creation
time, when and by what it was marked, and a basis (sessions, runs, branches,
pull requests).

Key assumptions:
- niwa leaves files it doesn't manage under `<instance>/.niwa/` alone through
  `niwa apply`, which the earlier remover checked in niwa's source and this
  design keeps true.
- A workspace configures the check-only form of an over command; one that
  configures the mark-writing form gets the same result, because niwa removes
  any mark written during a call that didn't exit 0.
- Session activity can be read as the later of the transcript's modification
  time and the `updatedAt` Claude Code gives for the session.

#### Chosen: a `[session.over]` command run like the removal gate, and niwa writing the earlier format

A workspace names the command in a new `[session.over]` table with the three
fields the existing session commands have: `command`, `timeout` (default
`"20s"`, following the earlier position) and `env` (names passed through,
validated by `ValidateSessionEnvName`). It's parsed into a new
`SessionOverConfig` beside `SessionOnReapConfig` and
`SessionBeforeRemoveConfig` in `internal/config/session.go`, checked by
`validateSessionDeclaration` and `checkTimeout`, and loaded at pass time by
`loadWorkspaceConfig` (on_reap.go:68-81), as the other two are. Leaving it
out turns marking off.

The runner, `overSeam` in a new `internal/cli/over.go`, copies `removalGate`
(removal_gate.go:69-209): `/bin/sh -c` from the workspace root, in its own
process group, with a fixed environment of `PATH`, `HOME`,
`NIWA_REMOVE_PATH` (the instance path, the variable the earlier contract
already reads) and `NIWA_SESSION_IDS`, plus the configured names. On the
per-run deadline niwa sends the group `SIGTERM`, then `SIGKILL` two seconds
later. Exit 0 is over and niwa writes the mark; exit 1 is not over; every
other outcome (another exit code, a signal, a command that can't start, the
deadline) is unknown and keeps. The last non-empty line of standard output,
cleaned by `sanitizeRemovalGateText` and cut to 200 characters, is the
reason, stored for the listing. After any outcome but exit 0, niwa removes
`<instance>/.niwa/retired.json` if it is a regular file owned by the user
whose `marked_at` is later than the moment the call began, so no mark exists
after any answer but over, whoever wrote it. The question and this clean-up
both run under the instance lock, which is a requirement rather than an
accident of order: a `niwa retire` writing a mark at the same moment waits on
the lock, so the clean-up can't remove the mark it just wrote.

niwa asks only about units that are stopped, carry no valid mark, aren't
held by a keep record, and haven't been asked since they last worked (a
unit's `unit.json` keeps the time of its last answer, and a unit restored
after a reap isn't asked again until its session has worked and stopped
again). A yes is never asked again; the mark stands until the reap.

niwa writes the mark in the earlier format and place, atomically (a temp file
made exclusively in `<instance>/.niwa/`, mode 0600, then a rename), with the
schema name that work uses, `instance_name`, `instance_created` (from
`instance.json`'s `created`), `marked_at` in UTC RFC 3339, `marked_by` naming
niwa's version and whether the over command or a person's retire wrote it,
and a basis of the sessions niwa read and the branches and heads its
repository walk found. The basis carries no runs and no pull requests,
because niwa reads neither. A person's `niwa retire` writes the same file.
Whichever path writes it, niwa records the mark's SHA-256, size and
`marked_at` in the unit's ledger outside the instance (Decision 9) before the
rename.

A mark counts when it is a regular file, not a symlink, owned by the user,
no larger than 64 KiB, parses, carries the expected schema name, names this
instance's directory and creation time, isn't dated in the future, and
matches the ledger's copy by hash and `marked_at`. Anything else is no mark,
and a mark that parses but has no matching ledger entry is listed as "mark
not written by niwa", so a mark the unit's own agent writes counts for
nothing. Later work voids a mark, following the earlier
position: a session in the unit active after `marked_at`, or a branch in the
basis whose head moved or vanished, makes the mark void. The reader leaves a
voided file in place, the listing shows the unit unmarked with "mark voided"
and why, and the next yes or retire overwrites it. Deleting an untracked file
voids nothing, so the PRD's "marked and clean" recipe holds.

There is no undo interval between a mark and its reap, following the earlier
position: a unit the pass marks can be reaped in the same pass when the rule
passes it. The verdict-only mode shows that outcome ahead of time, and after
the reap the undo is a restore inside the window.

#### Alternatives Considered

**Let the configured command write the mark, as the earlier remover's full
form does.** niwa would only read marks. Rejected because niwa then can't
promise that no mark exists after any answer but over (a killed command may
have written one), the mark's contents would vary by command, and a person's
retire would need a second writer anyway (D4 wants one format, whoever
writes it).

**A niwa-owned mark, `niwa.retire/v1`, at a path of niwa's choosing.** Cleaner
naming inside niwa's namespace. Rejected because the PRD makes niwa follow
the format and place the earlier remover settles (R6), and a second format
would make marks written there count for nothing here.

**Marks that later work never voids.** Simpler to read and test. Rejected
because a unit resumed or retasked after its mark would be reaped on a stale
statement that its work was over, and the earlier position voids it for that
reason.

**A fixed undo interval, such as one pass or one hour, between mark and
reap.** Gives a person a moment to see a mark before the unit goes. Rejected
because the earlier position has none, the PRD follows it, and the restore
window already gives a longer and well-defined undo; the verdict-only mode
covers the person who wants to see first.

### Decision 3: The takeover key an over command reads

Resolved inline.

An over command that answers for a unit whose work moved to a successor needs
to know that the successor's takeover has landed, meaning the successor
finished taking over, not only that it started. The PRD wants this design to
name the key and the seam's documentation to carry it, rather than leave it
to the shirabe command's later work (R5). niwa itself never reads the key;
naming it is about the contract a plugin's command follows. The earlier
remover reads no such key.

koto already has the two pieces. `koto init SUCC --continues PRED --supersede`
appends a `session_superseded` event naming the successor to the
predecessor's log (the fence), creates the successor, copies every key under
each `--carry` prefix into it, and only then writes
`koto/superseded-carry-complete` in the successor (koto's
`src/cli/init_supersede.rs`, `CARRY_COMPLETE_KEY`). A re-run that finds the key
carries nothing, and a carry that stops leaves it unwritten.

Key assumptions:
- `koto/superseded-carry-complete` stays koto's marker for a finished carry.
  It has been read from koto's source, not yet seen in a live run, so the seam
  documentation's example is checked by a functional test of the over seam
  against a real `koto init --supersede`.
- A unit with no koto run has no takeover to find, so its command answers
  unknown with "no takeover found".

#### Chosen: the predecessor's fence naming a successor that holds `koto/superseded-carry-complete`

The seam documentation, a new `docs/guides/over-command.md`, defines a
takeover as landed when both hold: the unit's own koto session carries a
`session_superseded` fence naming a successor, and that successor's context
holds `koto/superseded-carry-complete`. Only then may an over command answer
over for a unit on the grounds that it was replaced. A fence without the key
is a takeover in progress and answers not over; no fence answers on the
unit's own run. The guide carries the exit contract, the environment, the
reason line and this key in one place.

#### Alternatives Considered

**A niwa-defined takeover file in the predecessor's instance.** niwa would
own the format and could document it fully. Rejected because the successor
runs in another instance and would have to write into its predecessor's,
which nothing else does, and because koto already writes a marker at exactly
the point the PRD names.

**Treat the fence alone as landed.** Easier to read, one log event. Rejected
because the fence is written before the carry, so a successor that crashed
mid-carry would let the predecessor's unit go with keys the successor never
received, which is the case the PRD's "finished taking over" excludes.

### Decision 4: The records a unit carries, and who writes them

Resolved inline.

The rule needs per-unit inputs the PRD names but niwa doesn't have: a keep
record that holds a unit or names files to keep, a handoff brief's file list
and time, a discard record that answers for paths, pid records for the
background processes a unit starts, and somewhere to keep each unit's last
answer, time stopped and size so `niwa list` can stay local. Three of these
had no builder in the earlier remover: the pid record writer, the command that
writes a discard record, and the listing behind the dispatch line. The
earlier remover keeps its keep and discard records in its own state outside
the instance, where niwa can't read them.

Key assumptions:
- Records under `<instance>/.niwa/` survive `niwa apply` and go with the
  instance at removal, as `instance.json` does.
- The handoff brief is written by separate work as the `brief` member of the
  unit's `keep.json`, with its file list in `files`, which is how that work
  plans it. This design reads it and writes none.
- A brief's claim that it holds a file's bytes is taken at its word; the brief
  writer checks the hash when it stores them.
- The unit's agent can write any file under its instance, records included,
  so a record is trusted only as far as a copy outside the instance vouches
  for it (Decision 9).

#### Chosen: one directory of small records per unit, inside the instance

Each unit's records sit in its instance's `.niwa/` directory:

- **`keep.json`, the keep record and handoff brief.** It follows the earlier
  remover's fields (owner, reason, written and review dates, files by path and
  SHA-256) and adds a `protects` list drawn from `instance` (a hold),
  `build_tree` (the trim skips this unit's build trees) and `files`, a byte
  count per file, an `in_brief` flag for a file whose bytes travel inside the
  brief, and an optional `brief` member carrying the time the brief was
  written. A record with neither `protects` nor files reads as a hold, which is
  the earlier form. A hold keeps the unit whatever its answer; past its review
  date it still keeps and is listed as due for review. The session working
  after the record's `written_at` ends a hold but not the named files. With a
  brief, files it names are handed forward, scratch and loose root files older
  than the brief that it didn't name are disposable, and a file whose later of
  modification time and inode change time is after the brief keeps the unit
  and is named as newer than the brief. People and the brief writer write it;
  niwa ships no command for it in this design.
- **`discard.json`, the discard record, and `niwa discard`.** A person runs
  `niwa discard <instance> <path>... [--patch <file>]` from outside every
  instance; the command refuses when the instance guard (Decision 6) finds it
  running inside one. niwa checks each path with `removal.ResolveRecorded`
  (below) against the instance or, with a `job:` prefix, the unit's job
  directory, and appends it with the person's name and the time. A path
  matches exactly or as a directory prefix. A patch, when given, must resolve
  inside the instance the same way and be a regular file; niwa stores its hash
  and moves it to the kept set at reap like a named file, so what was
  discarded stays readable. The fields follow the earlier remover's
  hand-written form. niwa records the written file's hash in the ledger, and
  a `discard.json` that doesn't match it is read as absent, so its paths stay
  unique.
- **`pids/<pid>.json`, pid records, and `niwa helper`.** The program that
  starts a background process for a unit (a watch, a keep-alive helper) runs
  `niwa helper <pid> --purpose <text>` from inside the instance right after
  starting it. niwa refuses unless the named process is alive, is a child of
  the process that ran `niwa helper` (its parent pid is niwa's own parent
  pid), has its working directory inside the instance, and isn't a session
  process `claude agents` lists or an ancestor of niwa. It then writes the
  record in the `niwa.pid/v1` format niwa documents, taking the pid, the
  process start time (so a reused pid never matches) and the command from the
  operating system rather than the caller, plus the purpose, and records its
  hash in the ledger. A live process whose pid and start time match a record
  with a matching ledger entry is the unit's own helper: while the session is
  working it changes nothing, and once the session is stopped it no longer
  counts as liveness, and the listing names it. The pass never signals it;
  whoever registered it accepts that the helper ends on its own when the
  instance goes. A pid record with no matching ledger entry is absent. Without
  a valid record, any live process with its working directory in the instance
  keeps the unit, as the PRD requires.
- **`unit.json`, the unit record behind the listing.** Written by the pass
  for each unit it reaches: the last answer (over, not over, unknown, no
  command configured), its exit status and reason, when it was asked, when the
  unit stopped (the later of its transcript's modification time and its job
  entry's update time), its size and when that was measured, the last verdict
  with the first keeping check and its paths, a disagreement's step and
  message, a voided mark's reason, whether the last pass reached it, and the
  time of a restore. `niwa list` and the dispatch line read only this. Its
  hash goes in the ledger too; a `unit.json` that doesn't match is treated as
  never reached, so the unit is asked again and placed first in the order.

Every record is untrusted input on read, because the unit's agent can write
anything under its instance. Each reader in `internal/removal/records.go`
opens the file with `O_NOFOLLOW`, requires a regular file owned by the user,
caps its size (64 KiB for the mark, `discard.json`, `pids/*.json`,
`unit.json` and `entry.json`, 1 MiB for `keep.json`), decodes into a fixed
struct, and rejects an unknown schema name. A failure always lands on the
keeping side: an unreadable `keep.json` keeps the unit as a missing record
the rule needs, and an unreadable mark, discard record or pid record is
absent. Every path a record names (`keep.json` files and brief files,
`discard.json` paths and patch, `entry.json` files) goes through
`removal.ResolveRecorded(base, path)` on every read, not only when niwa
writes it: the path must be relative, non-empty and free of `..`, NUL and
backslash; a `job:` prefix sets the base to the unit's own job directory and
nothing else; and each component is checked from the base down with `Lstat`,
so a symlink anywhere in the path, the last component included, or a
component on another device fails it. A path that fails is named in the
verdict, and the record keeps the unit as unreadable. niwa writes every
record it owns with mode 0600.

The discard command and the pid record format are the two builders the
earlier remover left open; the listing behind the dispatch line is the unit
record plus Decision 6's output. All three are settled here, and none is left
to the shirabe command's later work.

#### Alternatives Considered

**Keep the records in the workspace root's `.niwa/`, keyed by instance
name.** One place to list them, and they'd survive the instance. Rejected
because the root's `.niwa/` is recopied when the configuration snapshot swaps,
a record that outlives its instance has nothing to describe, and the location
rule forbids large host state there; per-unit records belong with the unit.

**Read the earlier remover's state directory directly.** It already holds
keep and discard records for one workspace. Rejected because that directory
exists only where that remover is installed, so every other workspace would
get nothing (D4 says follow its format, not its install).

**Leave pid records unwritten and every live process keeping its unit.** No
new format. Rejected because the PRD gives this design the writer, and a
helper that outlives its session would then keep a finished unit forever with
no way for its starter to say otherwise.

**A `niwa keep` command for keep records.** Symmetric with `niwa discard`.
Rejected for now because the record's form is still an open dependency on the
earlier remover, its main writer is the separate brief work, and a command
fixed before the form lands would need changing; a hand-written record works
for a person meanwhile.

### Decision 5: The kept set, the launch record and the restore window

Resolved inline.

When a unit with named files is reaped, those files have to land somewhere
outside every instance, unchanged by hash, and come back on a restore. A reap
on a mark needs a launch record to replay, which today only `niwa dispatch`
writes, so a unit the SessionStart hook provisioned can't be reaped on a mark
at all. A restore needs a window with an end date. Two existing positions
pull against the PRD here. The earlier remover's kept set never expires: only
a person removes an entry. The PRD instead marks an entry due on its date,
shows it, and removes it at the first pass at least a day after it was first
shown (R27). And `docs/designs/DESIGN-plugin-release-hold.md`, which
tsukumogami/niwa#536 (an open draft) implements, plans for teardown work to
keep whole finished instances at `~/.niwa/w-<key>/kept/<instance>/` and scans
that directory for plugin links (`hostdir.InstanceDirs`), while this kept set
holds only the files keep records name.

Key assumptions:
- The workspace key is `mirror.WorkspaceKey` ("w-" plus the first 32 hex of
  the SHA-256 of the canonical root, internal/mirror/layout.go:48-60), which
  tsukumogami/niwa#536 moves to `internal/hostdir`; whichever change lands
  second uses the other's home for it.
- Claude Code's transcript retention is `cleanupPeriodDays` in
  `~/.claude/settings.json`, 30 days when unset.
- A session the SessionStart hook provisioned can be resumed with
  `claude --resume` from its instance path with no permission mode, settings
  or environment replayed, since niwa didn't launch it with any.

#### Chosen: a separate `kept-files/` directory under the location rule, launch records for hook sessions, and window dates on the launch record

The kept set lives at `~/.niwa/w-<key>/kept-files/`, one entry per unit and
move: `kept-files/<instance>/<UTC time of the move>/` holding `files/` with
each named file at its in-unit path, the keep record with every path
rewritten to its place there, and `entry.json` (schema `niwa.kept/v1`: the
instance, its creation time, when it was moved, the due date, when it was
first shown due, and each file's original path, SHA-256 and size). A move
within one filesystem is a rename; across volumes it's a copy, a hash check
and a delete of the source, and a failed write or a hash mismatch keeps the
unit with nothing removed. The move takes only regular files: each source
path passes `removal.ResolveRecorded` again at move time, is opened with
`O_NOFOLLOW`, and anything else (a symlink, a device, a directory) keeps the
unit with the path named, so a keep record can't point the move at a file
outside the instance. Entry directories are created 0700, `entry.json` and
the rewritten keep record 0600, and each file keeps its permission bits with
setuid, setgid and sticky cleared. A per-workspace lock in `~/.niwa/locks/`
guards the entries; every lock file there is created 0600 with `O_NOFOLLOW`.

Using `kept-files/` rather than `kept/` resolves the overlap with the plugin
release store. This design never keeps a whole instance outside the root,
so it writes nothing into `kept/`, and the release store's scan of that
directory finds no entries from it; its entries carry no
`.niwa/marketplaces/` links that scan would need to count. If whole kept
instances ever arrive, they keep the `kept/` name the release store already
reads, and the two never share a directory or an instance name. This is
named as an overlap with tsukumogami/niwa#536 so whichever lands second
checks the other's layout.

The due-date clash is reconciled by store. niwa's kept set follows R27: an
entry's due date is the keep record's review date, or the end of the unit's
restore window when the record names none; on that date the pass marks it
due, the listing and the dispatch line show it, and the first pass at least
a day after it was first shown removes it. The earlier remover's own kept
set, in that remover's state, keeps its never-expires rule; niwa never reads
or removes it, and its entries aren't carried into niwa's. A person who wants
an entry kept longer gives the record a later review date. If the earlier
work lands with never-expires as a rule for every form, the requirement it
would change is R27, and that change goes to the PRD rather than into this
design.

When niwa removes a whole workspace (`DestroyWorkspace`), it leaves
`kept-files/` in place and prints its path, as the release store leaves a
store kept instances still use, because these are files a person asked to
keep and nothing could show them as due once the workspace is gone.

`niwa instance from-hook` writes a launch record for the sessions it
provisions, in the same `niwa.launch/v1` store at `<root>/.niwa-launch/`,
keyed by a launch id it mints in the dispatch-id shape
(`authority.ValidDispatchID`), with the agent, the session, the instance name
and the time, and a new optional `origin` field set to `hook`. The session
mapping gains a `launch_id` field that names it. `resolveLaunchRecord`
already matches by instance name and session, so `niwa resume` finds it
unchanged.

The launch record also carries the window. At reap, the pass writes
`reaped_at`, `restore_until` (the reap time plus the window) and the path of
the unit's kept-set entry into it. The window is the workspace's
`restore_window` setting when set, otherwise Claude Code's transcript
retention read at reap time. A `restore_window` under seven days is a
configuration error from `validateSessionDeclaration`, naming the setting and
the seven-day minimum, so every command that loads the configuration exits
non-zero and no pass runs. `niwa resume` refuses a record past
`restore_until` with the date the window ended. Inside it, before the session
starts, it moves the entry's files back to their original paths with a hash
check, and refuses, leaving everything in place and naming the file, when one
doesn't match. The restore trusts no path it reads: every original path in
`entry.json` and the rewritten keep record passes `removal.ResolveRecorded`
against the fresh instance, and every source path must resolve inside the
entry's own `files/` directory. Missing parent directories are created one
component at a time, each opened with `O_DIRECTORY|O_NOFOLLOW` so no link
is followed, and each file is created with `O_CREAT|O_EXCL|O_NOFOLLOW`,
never over an existing file; a path that fails any of this refuses the
restore the same way a hash mismatch does, naming the file. When the entry already left because its review date came
before the window's end, the restore goes ahead without those files and says
which entry was gone, since the person who set that date chose it. The
restored instance carries no mark.

#### Alternatives Considered

**Use `~/.niwa/w-<key>/kept/<instance>/` as the earlier plans proposed.** One
name for everything kept. Rejected because the release store treats every
real directory there as an instance whose plugin links count and stops its
sweep when one can't be read, so file-only entries would sit in a scan built
for something else, and a later whole-instance keeper would collide on the
same instance names.

**Never expire niwa's kept set, like the earlier remover's.** Nothing a person
kept would ever leave without them. Rejected because R27 requires due-date
removal after a day's notice, and an unbounded store on every host is the
problem this feature exists to fix.

**Leave hook-provisioned sessions without a launch record.** No change to the
hook. Rejected because such a unit can then never be reaped on a mark or
restored, so a user of niwa alone with hook sessions has only the unchecked
route of deleting the session, which the project owner's job-scratch choice
was made alongside.

**Keep window dates in the unit record or the kept set.** Rejected because
both are gone or optional after a reap, and the launch record is already the
one store that outlives the instance and that `niwa resume` reads.

### Decision 6: Names, the listing, the dispatch line and the job-scratch default

Resolved inline.

The PRD left the names of the mark, the retire command and every setting,
the dispatch line's wording, and the verdict-only mode's surface to this
design. The project owner chose the job-scratch default: where a unit has no
handoff brief, its job scratch is disposable by default, a workspace setting
shaped like the ignored-files setting makes it unique, a workspace that
configures an over command for shirabe's workflows should set it unique in
the same configuration, the verdict-only mode lists such scratch as
disposable with a job-scratch notice, and the later idle-time setting ships
off.

Key assumptions:
- `niwa list --json` consumers iterate an array of instance records, so new
  fields can be added to each record but the top-level shape stays an array.
- A person runs `niwa retire` from a shell outside every instance, while a
  worker runs inside its own.

#### Chosen: `[session.removal]` beside `[session.over]`, `niwa retire`, a verdict-only flag on reap, and one fixed dispatch line

The names are:

| Thing | Name |
|-------|------|
| Retire mark | `<instance>/.niwa/retired.json` (the earlier remover's place) |
| Over command | `[session.over]` `command`, `timeout`, `env` |
| Retire command | `niwa retire <instance>` |
| Person-only override of "not over" | `niwa retire --override-not-over` |
| Verdict-only mode | `niwa reap --verdict-only [<instance>] [--json]` |
| Discard record command | `niwa discard <instance> <path>... [--patch <file>]` |
| Pid record command | `niwa helper <pid> --purpose <text>` |
| Record ledger | `~/.niwa/w-<key>/ledger/<instance>.json` |
| Ignored-files setting | `[session.removal] ignored_files = "disposable"` or `"unique"` |
| Job-scratch setting | `[session.removal] job_scratch = "disposable"` or `"unique"` |
| Trim switch | `[session.removal] trim = true` or `false` |
| Idle grace | `[session.removal] trim_after = "24h"` |
| Restore window | `[session.removal] restore_window = "30d"` (unset by default) |
| Workspace disposable patterns | `[session.removal] disposable = [{ pattern = "...", review = "..." }]` |
| Declared build output | `[repos.<name>] build_trees = ["target/"]` |
| Kept set | `~/.niwa/w-<key>/kept-files/` |

`job_scratch` sits next to `ignored_files` and takes the same two values.
Its default, `"disposable"`, is the project owner's choice. It applies only
where a unit has no handoff brief; with a brief, the brief's rule applies
whatever the setting says. A workspace that configures `[session.over]` for
shirabe's workflows should set `job_scratch = "unique"` in the same file, and
the over seam's guide says so. The later idle-time setting, which ships off,
would sit in the same table; this design doesn't define it. Its shape, as
the project owner chose it: a workspace that turns it on gets a mark on any
unit stopped longer than the setting that no over command answers, counted
from the unit's last stop or restore, and never on a unit answered not over.

`trim_after` defaults to 24 hours. The shipped disposable patterns are
`*.swp`, `*.swo`, `*~`, `.#*`, `.DS_Store`, `__pycache__/` and
`.pytest_cache/`. Pattern syntax is glob, as in `path.Match`, plus `**` for
any number of directories; a pattern with no slash matches a base name
anywhere, one with a slash matches the path from the repository or instance
root, and a trailing slash matches a directory and everything under it.
Each workspace entry needs a non-empty `review` naming the review that added
it, and one without is a configuration error. niwa doesn't check what the
field names; it is an audit trail for the people who edit the configuration,
not a control.

`niwa retire <instance>` resolves the instance by name at the root (or from a
path given as an argument), takes its lock, and refuses when a session in it
is working, saying so. When the unit's last answer was not over it refuses
unless `--override-not-over` is given; the pass never sets the flag. The whole
command, like `niwa discard`, refuses when the instance guard finds it running
inside an instance, so a worker can't retire itself or launder its own work
through either command. The guard, `insideInstance` in a new
`internal/cli/guard.go`, answers yes when the working directory resolves
inside any instance of the workspace, when `NIWA_DISPATCH_ID` or
`NIWA_REAP_KEY` (which niwa sets in the sessions it dispatches) is in the
environment, or when any ancestor process from the pass's process listing has
its working directory inside an instance. It is advisory: an agent that
detaches from its session and changes directory gets past it, and the rule
still decides what goes. Past those checks, retire writes the mark and runs the
one-unit pass, and prints one of `niwa: retired <name> and removed it` or
`niwa: retired <name>; kept by <check>: <paths>`, naming the first keeping
check and every path behind it, or the step and message of a disagreement. A
retire stops nothing that runs.

`niwa list` adds a line per instance under the name, read from `unit.json`
and the records, never from the network or the over command:

```
tsuku+fix-cache-3f2a91c0
  stopped 3d 4h, 2.1 GiB, not over: pull request open
  resume: niwa resume tsuku+fix-cache-3f2a91c0
tsuku+bench-77c10d2e
  retired, kept by untracked files: notes.txt
tsuku+docs-1b9e0c44
  held by keep record (owner dana): rerun pending; review due 2026-12-01
kept set: 3 entries, 1 due (niwa list --kept)
```

A unit with no command configured shows `no over command configured`; an
unknown answer shows `unknown (exit 3)` and the reason; a disagreement shows
`retired, kept by a disagreement: <step> refused: <message>`. The JSON form
keeps its array and adds a `unit` object to each record with `state`,
`stopped_since`, `stopped_seconds`, `size_bytes`, `size_measured_at`,
`answer`, `reason`, `retired`, `mark_voided`, `verdict`, `kept_by` (check,
paths, evidence), `disagreement` (step, message), `held` (owner, reason,
review, due) and `not_reached`. `niwa list --kept [--json]` lists the kept set
by entry, file path and size, never file contents.

Every string that comes from a unit (the over command's reason, git error
evidence, a keep record's owner and reason, paths) passes
`removal.ScrubEvidence` before niwa stores or prints it: userinfo is stripped
from every URL (`https://user:token@host/x` becomes `https://host/x`), known
token shapes (`ghp_`, `gho_`, `github_pat_`, `glpat-`, and the value after
`Authorization:` or `Bearer`) become `[redacted]`, terminal control and escape
sequences are removed by `sanitizeRemovalGateText`, and the result is cut to
200 characters for a reason and 400 for evidence. `niwa list`, `--json`
verdicts, retire and the dispatch line apply it again on read, because
`unit.json` and the records are agent-writable and may not hold what the pass
wrote.

Each `niwa dispatch` prints one line to standard error after its pass, with
or without an over command:

```
niwa: 3 stopped units not retired, 4.2 GiB; 1 kept-set entry due; 0 units not reached (niwa list)
```

The template is `niwa: <N> stopped unit[s] not retired, <size>; <D> kept-set
entr(y|ies) due; <U> unit[s] not reached (niwa list)`, with sizes in binary
units to one decimal place (`0 B`, `412 MiB`, `4.2 GiB`). The count and size
leave out marked units the rule keeps, since someone retired them; the
listing shows those. `niwa reap` prints the same three counts with its own
reaped and trimmed counts.

The verdict-only mode prints, for each unit or the one named, the verdict,
every keeping check in order with its paths and evidence, the over answer it
got and the mark it would write with the verdict that mark would lead to, and
the class of every path. A directory whose contents all share one class is
one line, so a clean clone is one tracked line and a build tree one line. A
git-ignored path is listed as disposable with a `git-ignored` notice, and
scratch that's disposable only because the unit has no brief as disposable
with a `job scratch` notice. With `--json` it prints a `niwa.verdicts/v1`
array with the same fields, which is the form a workflow plugin asks for one
unit. It writes no mark, removes and trims nothing, moves nothing into the
kept set, and fetches into the scratch object store only.

#### Alternatives Considered

**Put the settings at the top level as `[removal]`.** Shorter keys. Rejected
because the over command, the veto and on-reap already sit under
`[session]`, which is workspace-scoped and read at removal time rather than at
apply, and the removal settings share both properties.

**A `niwa unit` command group (`niwa unit retire`, `niwa unit discard`,
`niwa unit verdicts`).** Groups the new verbs. Rejected because the PRD keeps
the trim and the verdict-only mode inside the pass rather than as verbs of
their own, `niwa reap` is where a person already looks at what the pass would
do, and two new top-level verbs are easier to discover than a group.

**A dispatch line that prints only when something is listed.** Quieter.
Rejected because R23 asks for the line at each dispatch whether or not a
command is configured, and a fixed line is easier for a person and a test to
read than one that comes and goes.

**Job scratch unique by default.** Keeps the scratch check for everyone.
Rejected by the project owner, because a user of niwa alone has no brief, so
a retire would mostly answer kept and push that user back to deleting the
session, which checks nothing.

### Decision 7: Delivery in three parts

Resolved inline.

The work touches the reap, destroy, dispatch, create, watch, list, resume, the
SessionStart hook and the configuration. Shipping it in one change would put
niwa's riskiest behavioral change, reap becoming stricter and marked units
being reaped, behind a review too large to read. The question is whether it
comes in parts, and which.

Key assumptions:
- Each part leaves niwa safe on its own: no part removes anything the rule
  wouldn't, and no part depends on a later one to undo a loss.

#### Chosen: the rule and worktrees; the over question, mark and listing; the keep record, kept set and launch record

Delivery comes in three parts. The first is the rule and worktrees: the
`internal/removal` package, the pass orchestrator and budget, the reap's loss
scan for mapped units and the backstop (#300), failed-dispatch and watch
cleanup through the rule, worktrees before the session in the pass and in
destroy, the pattern lists, the discard record and `niwa discard`, and the
verdict-only mode without over answers, along with the hardened git
environment, recorded-path validation, the record ledger for discard
records, evidence scrubbing and the tombstone before removal, since the
first part already runs git in agent-written repositories and deletes
instances. The second is the over question, the mark and the listing:
`[session.over]`, the mark writer and reader with its ledger entry,
`niwa retire` and the instance guard, the unit record, `niwa list`'s new
fields, the dispatch line, the trim with its settings and the root apply
lock, pid records through `niwa helper`, and over answers in the verdict-only
mode. The third is the keep record, the kept set and the launch record:
`keep.json` holds and named files, the handoff brief reader, the kept set
with its permissions and due dates, launch records for hook sessions, the
restore window and the validated restore of kept files. The Implementation Approach
follows these parts.

#### Alternatives Considered

**One change.** Nothing half-done ever ships. Rejected because a single
review of every removal path at once is where a loss slips through, and the
first part alone closes #300, which is the change most users notice.

**Ship the over seam first.** It's the headline. Rejected because a mark
reaped by a rule that doesn't yet scan for loss would take unique work; the
rule must land before anything starts more removals.

### Decision 8: How niwa runs git in a repository the unit's agent controls

Resolved inline.

The rule runs `git status`, `for-each-ref`, `stash list`, `ls-remote`,
`worktree list`, `merge-tree` and a fetch inside repositories whose `.git`
the unit's agent can write, automatically, before every dispatch. Left to
git's defaults, a repository's own configuration turns those calls into code
execution or network traffic of the agent's choosing: `core.fsmonitor`,
`core.hooksPath`, `core.pager`, `core.sshCommand`, `core.askPass`,
`credential.helper`, filter, diff and merge drivers, and `ext::` or `file://`
remote URLs. The question is how to run the calls the rule needs without
running anything the repository names.

Key assumptions:
- Reading configuration (`git config --list`) runs no program, so niwa can
  look at a repository's settings safely before running anything else in it.
- Command-line `-c` settings take precedence over every file and are passed
  on to the git processes git itself starts.
- The person's own global git configuration is trusted, but it isn't a fixed
  environment: what it holds varies by host.

#### Chosen: one hardened command builder, a refusal on program-naming config, and network calls from the scratch store

`gitCommand` in `internal/removal/git.go` builds every git call niwa's
removal makes. It starts git by absolute path, resolved once per pass from a
`PATH` with every entry inside the workspace root removed, with no inherited
environment except `HOME`, and sets:

- `LANG=C`, `GIT_CONFIG_NOSYSTEM=1` and `GIT_CONFIG_GLOBAL=/dev/null`, so only
  the repository's own configuration and niwa's overrides apply;
- `GIT_TERMINAL_PROMPT=0` and `GIT_ASKPASS` set to the empty string, which
  stops git from running `core.askPass` or `SSH_ASKPASS`;
- `GIT_ALLOW_PROTOCOL=https:ssh`, so `ext::`, `file://`, local paths and
  every other transport are refused by git itself;
- `GIT_SSH_COMMAND="ssh -o BatchMode=yes -o ForwardAgent=no"`, which
  overrides any `core.sshCommand`;
- `GIT_PAGER=cat`, `GIT_OPTIONAL_LOCKS=0` (status never rewrites the index,
  so nothing under the unit's `.git` changes), `GIT_NO_LAZY_FETCH=1` (a
  partial clone can't fetch missing objects from its promisor remote during
  `merge-tree`) and `GIT_CEILING_DIRECTORIES` set to the workspace root;
- the arguments `--no-pager`, `--git-dir` and `--work-tree` taken from the
  walk (which overrides any `core.worktree`), and `-c core.fsmonitor=false
  -c core.hooksPath=/dev/null -c core.pager=cat -c credential.helper=
  -c submodule.recurse=false -c fetch.recurseSubmodules=false`.

`GIT_NO_LAZY_FETCH` needs git 2.45; with an older git, niwa treats a
repository whose configuration sets `extensions.partialClone` as undecidable
for the merged-by-content check, which keeps.

Before any other call in a repository, niwa reads its effective settings with
`git config --list --show-scope --includes` under that environment. When a
key from the repository's own scope or a file it includes names a program
git might run during the rule's calls (`filter.*.clean`, `filter.*.smudge`,
`filter.*.process`, `diff.external`, `diff.*.textconv`, `diff.*.command`,
`merge.*.driver`, `credential.*.helper`), niwa runs nothing more there and
keeps the unit with "repository configuration names a program: <key>" as
evidence. A repository git refuses for ownership (`safe.directory`) keeps
with git's message; niwa never adds a `safe.directory` entry.

Network calls never run inside the unit's repository. niwa reads each remote
URL from the setting listing, validates it (Decision 1), and runs
`ls-remote` and the fetch with that literal URL against the scratch object
store, whose configuration niwa writes. The repository's `insteadOf`,
`uploadpack`, credential and proxy settings therefore never apply to them.
No credential helper runs, except that for a URL exactly equal to the clone
URL the workspace configuration gives that repository, niwa adds the person's
own helpers, read beforehand with `git config --global --get-all
credential.helper`, as `-c credential.helper=<value>` after the reset. A
remote the agent added gets no credentials: a public one still answers, a
private one fails with an authentication error and keeps.

#### Alternatives Considered

**Run git with the person's own global configuration and only the `-c`
overrides.** Private remotes would authenticate everywhere as they do in the
person's shell. Rejected because the global configuration varies by host and
can include files and settings niwa can't vet, so the rule's environment and
its results would differ between hosts, and because a helper the person
scopes broadly would hand a stored token to any host an agent-written URL
names.

**Override each driver the configuration names with an empty value instead
of refusing.** Repositories with a per-repository LFS install would stay
reapable. Rejected because whether an empty value disables a driver differs
between keys and git versions, and a driver missed in an include is code
execution; refusing fails toward keeping.

**No credentials for any network call.** The smallest trust surface.
Rejected because every unit cloned from a private `https` remote would then
read as unreachable and keep forever, which defeats the feature for most
private work.

**Read repositories with a Go git library instead of the git binary.** No
program runs at all. Rejected because it means a new dependency and
reimplementing status, worktree and `merge-tree` semantics the rule relies
on matching git exactly.

### Decision 9: Where niwa keeps the trusted copy of a unit's records

Resolved inline.

Three records loosen what the rule keeps: the mark (a reap candidate), the
discard record (unique paths become disposable) and a pid record (a live
process stops counting once the session stops). All three sit in the
instance, where the unit's agent can write them. A prompt-injected agent
could write a mark, a discard record covering its unpushed files and a pid
record for a live helper, and have its own work deleted at the next pass,
with discarded paths outside the kept set. The question is how niwa tells a
record it wrote from one the agent wrote.

Key assumptions:
- The agent writes inside its instance in normal work; writing into
  `~/.niwa/` takes a deliberate step outside it, which Claude Code's
  permission settings can deny.
- Every writer of these three records can be a niwa command: the pass and
  `niwa retire` for the mark, `niwa discard`, and a starter that runs
  `niwa helper`.

#### Chosen: a per-unit ledger under the workspace's host directory, and `niwa helper` for pid records

niwa keeps a ledger per unit at `~/.niwa/w-<key>/ledger/<instance>.json`
(schema `niwa.ledger/v1`: the instance name and creation time, and for each
record niwa wrote, its path under `.niwa/`, SHA-256, size, write time and
writer: over, retire, discard, helper or pass). Under the instance lock, niwa
writes the record to a temp file, records its ledger entry, and only then
renames the record into place, all atomically and 0600, so no reader ever
sees a record niwa wrote without its entry. A
mark, discard record, pid record or `unit.json` whose hash doesn't match its
entry is treated as niwa never wrote it: no mark, no discard record, no
helper, never reached. The records stay in the instance in the earlier
remover's forms, so anything reading that place still finds them; the ledger
only vouches. The pass removes the ledger file after `DestroyInstance`, and a
ledger whose instance is gone or whose creation time differs is ignored and
removed. `DestroyWorkspace` removes `ledger/` with the workspace, unlike
`kept-files/`, since a ledger has nothing to vouch for once its instances are
gone. `niwa helper` replaces hand-written pid records, with the checks in
Decision 4, so a pid record can only name a process its own caller started.

#### Alternatives Considered

**Move the discard and pid records out of the instance into host state.**
Only niwa would ever write them. Rejected because the earlier remover's form
places the discard record with the unit, and a host copy of the whole record
gains nothing over a hash of it while adding a second place to keep in step.

**Keep the hashes in the launch record.** One host store for each unit.
Rejected because not every unit has a launch record until phase 6 lands,
and the launch record sits under the workspace root, one `cd ..` from the
instance.

**Accept agent-written records, as the first draft did.** Simplest, and the
rule still gates every reap. Rejected because the discard record isn't gated
by the rule; it is an input that tells the rule which paths don't matter.

## Decision Outcome

niwa gets one removal rule and one pass. The rule, `removal.Evaluate` in
`internal/removal`, reads a unit (its instance, its job directory, its
sessions and processes, and its records) and returns a verdict with every
keeping check in a fixed order and the class of every path. It runs on git
plumbing and local files, fetches only into a scratch object store outside
the unit, and keeps on anything it can't decide. The pass, `runPass` in
`internal/cli/pass.go`, gathers candidates where `reapWorkspace` gathers them
today and adds stopped units and marked units, gives every all-unit pass one
30-second deadline shared by everything inside it, takes units oldest-reached
first, and counts the ones it didn't start. `reapOpportunistically`,
`niwa reap`, `niwa retire`, failed-dispatch cleanup, the watch's discard and
the hook worktree sweep all go through it or through `removal.Evaluate`.

On top of the rule sits the seam. A workspace names its over command in
`[session.over]`; niwa runs it like the removal gate, in its own process
group under a 20-second default deadline, with the instance path in
`NIWA_REMOVE_PATH`, and reads exit 0 as over, 1 as not over and anything else
as unknown. On over, niwa writes `<instance>/.niwa/retired.json` in the
earlier workspace-level remover's format; after any other answer it removes
a mark written during the call. A mark is a statement, not a permission: the
marked unit is tested at every pass and reaped only when the rule passes it,
the same pass included. Later session activity or a moved branch head voids
a mark. A person retires a unit with `niwa retire`, which needs
`--override-not-over` only when the last answer was not over. An over command
that answers for a replaced unit reads koto's fence and
`koto/superseded-carry-complete` to know the successor finished taking over.

Removal is never forced. Once the rule passes a unit and the gate allows it,
niwa re-reads the sessions, processes and git state and keeps the unit when
any changed. Otherwise it moves named files into the kept set, removes the
worktrees inside the instance with an unforced `git worktree remove`, runs
`claude rm` on the sessions without either override flag, runs on-reap,
removes the instance through `workspace.DestroyInstance`, and writes the
reap time and the window's end into the launch record. A refusal from the
gate, from niwa or from Claude Code is a disagreement: the unit stays,
marked, and the listing names the step and its message. `niwa destroy` gains
the same worktrees-first order.

Each unit's records sit in its instance's `.niwa/`: the mark, `keep.json`
(holds, named files, build-tree protection and the handoff brief),
`discard.json` written by `niwa discard`, pid records in `pids/` written by
whatever starts a background helper, and `unit.json`, where the pass writes
the last answer, time stopped, size and verdict. `niwa list` and a fixed
dispatch line read only those, so the listing is local. The kept set lives at
`~/.niwa/w-<key>/kept-files/`, apart from the release store's `kept/`, with
due dates shown a day before entries leave. The SessionStart hook writes
launch records, so hook sessions can be reaped on a mark and restored, and
`niwa resume` refuses past `restore_until`, which defaults to Claude Code's
transcript retention and can't be set below seven days.

Everything in an instance is treated as written by an untrusted agent. Every
git call goes through one hardened builder that runs nothing the repository
names and contacts only validated `https` and `ssh` URLs from the scratch
store. The mark, the discard record, pid records (now registered through
`niwa helper`) and `unit.json` count only when they match a ledger niwa keeps
at `~/.niwa/w-<key>/ledger/`. Every recorded path is resolved without
following a symlink on every read and again at restore, which never
overwrites. The instance is renamed to a tombstone and checked once more
before it's deleted, and every string a unit supplies is scrubbed of
credentials and control sequences before niwa stores or prints it.

The decisions hold together because each one keeps a single owner for each
fact. The rule is in one package, so the verdict-only mode, a retire and the
pass can't disagree. The mark has one format and one writer path, so a mark
counts the same whoever wrote it and none survives an answer but over. Every
per-unit fact lives with the unit and goes with it, while the only host
state, the kept set, the ledger and the launch records, sits where the
location rule and `niwa resume` already look. The budget is one deadline, so it bounds a
dispatch without any step needing to know about the others. The accepted
cost is that niwa follows formats it doesn't own yet, so parts of this design
move when the earlier remover lands; the open dependencies say which.

## Solution Architecture

### Overview

```
niwa dispatch / create / watch ──> reapOpportunistically ─┐
niwa reap [--verdict-only]  ───────────────────────────────┼─> runPass(ctx 30s, root, mode, scope)
niwa retire <instance>  ──> write mark ──> runPass(one) ───┘        │
                                                                    │ per unit, oldest-reached first
                    ┌───────────────────────────────────────────────┤
                    v                                               v
      overSeam.ask  ([session.over])                  removal.Evaluate(unit, policy)
      exit 0 -> write retired.json                     -> verdict, checks, path classes
      exit 1 / other -> clear call's mark                         │
                    │                                              v
                    └──────────> unit.json  <──── act: keep | trim | reap
                                     │              (lock, gate, re-read, kept set,
                                     v               worktrees, claude rm, on_reap,
                         niwa list / dispatch line   tombstone + check, DestroyInstance,
                                                     ledger, launch record)
```

Failed-dispatch cleanup and the watch's discard call `removal.Evaluate` for
their one instance; the hook worktree sweep evaluates a worktree's repository
checks before removing it. Both boxes write their record together with its
entry in the unit's ledger outside the instance, and every git call under
`removal.Evaluate` and the acting steps goes through `gitCommand`.

### Components

**`internal/removal` (new).** The rule, coded once.

- `rule.go`: `Evaluate`, the check order as constants (`CheckLiveness`,
  `CheckHold`, `CheckRecords`, `CheckRepository`, `CheckOutside`,
  `CheckNamedFiles`), and the `Verdict` and `Check` types.
- `walk.go`: the walk of the instance and job directory, repository
  discovery, path classes, build-tree detection, generated paths, size; it
  `Lstat`s every entry, follows no symlink, stays on one filesystem, and
  keeps on a `.git` file whose git directory lies outside the unit.
- `git.go`: `gitCommand`, the only builder of git commands, with the fixed
  environment and overrides of Decision 8; the program-naming configuration
  check; remote URL validation; per-repository checks, held by live refs,
  merged by content, the scratch object store, 10-second network contexts.
- `paths.go`: `ResolveRecorded(base, path)`, the read-time check every
  recorded path passes (relative, no `..`, `job:` bound to the unit's job
  directory, `Lstat` on each component, no symlink, one device).
- `scrub.go`: `ScrubEvidence`, which strips URL userinfo and known token
  shapes, removes control sequences and bounds length.
- `patterns.go`: the shipped list and the glob matcher with `**`.
- `records.go`: readers and writers for `retired.json`, `keep.json`,
  `discard.json`, `pids/`, and `unit.json`, with the validity rules each
  carries, size caps, `O_NOFOLLOW` opens, strict decoding, 0600 writes and
  the ledger match.
- `procs.go`: the per-pass process listing (`/proc` on Linux, `lsof` on
  macOS, resolved by absolute path like git) and attribution to instances and
  build trees, pid records applied; process ancestry for the instance guard.
- `brief.go`: the handoff brief's freshness test (later of mtime and ctime).

**`internal/cli/pass.go` (new).** `runPass` with three modes (act on every
unit, act on one, verdict-only), the deadline, the ordering, candidate
gathering (moved out of `selectReapTargets`, `selectStoredMappingTargets`
and `selectBackstopTargets`), the acting steps (`actReap`, `actTrim`), the
re-read before removal, the tombstone rename and check before
`DestroyInstance`, the recovery of tombstones an interrupted pass left, and
the counts the dispatch line and reap print.
`reapWorkspace` becomes a thin call into it; `sweepLaunchRecords` and
`sweepHookWorktrees` stay as steps of the pass.

**`internal/cli/over.go` (new).** `overSeam`, built like `removalGate`:
`load`, `active`, `ask`, `exec`, with output capture, last-line cleaning, the
per-run timeout, process-group kill, and the mark clean-up after a non-zero
answer, run under the instance lock.

**`internal/cli/retire.go`, `internal/cli/discard.go` (new).** The two
person-facing commands, both refusing when `insideInstance` answers yes, and
both writing their record's ledger entry.

**`internal/cli/helper.go` (new).** `niwa helper <pid> --purpose <text>`:
checks the process is the caller's child inside the instance and not a
session, writes `pids/<pid>.json` from the operating system's view of the
process, and records it in the ledger.

**`internal/cli/guard.go` (new).** `insideInstance`, the advisory check by
working directory, `NIWA_DISPATCH_ID` and `NIWA_REAP_KEY`, and process
ancestry.

**`internal/cli/reap.go`.** Gains `--verdict-only`, `--json` and an optional
instance argument allowed only with `--verdict-only`; prints the reaped,
trimmed, kept and not-reached counts.

**`internal/cli/list.go`.** Reads `unit.json`, `keep.json` and the mark for
each record (`annotateFromUnitRecords` beside `annotateFromSessionMappings`),
checks each against the ledger, passes every stored string through
`ScrubEvidence` again on read, prints the state line and the kept-set
summary, adds `--kept`, and adds the `unit` object to
`workspace.InstanceRecord` (state.go:470-511).

**`internal/cli/dispatch.go`.** Prints the dispatch line after
`reapOpportunistically` at step (5).

**`internal/cli/destroy.go`, `destroy_session.go`.** `runDestroyInstance`
removes worktrees inside the instance after the scan and before
`removeOwningSessionsListed`; unforced destroy uses unforced
`git worktree remove`, and `--force` keeps its meaning and removes them with
git's own force before the session, since the person already accepted the
loss. Those worktree calls go through `removal.gitCommand`; destroy's
existing scan, `workspace.ScanInstance`, is left as it is.

**`internal/cli/instance_from_hook.go`.** Writes the hook session's launch
record and the mapping's `launch_id`.

**`internal/cli/resume.go`.** Checks `restore_until`, re-validates every
path in `entry.json` and the rewritten keep record with `ResolveRecorded`,
moves kept-set files back with a hash check, creating parents with
`O_DIRECTORY|O_NOFOLLOW` and files with `O_CREAT|O_EXCL|O_NOFOLLOW`, and
clears nothing else.

**`internal/workspace/destroy.go`, `tombstone.go` (new), `ledger_store.go`
(new).** `DestroyInstance` `Lstat`s the instance directory, refuses a symlink
and any path that isn't a direct child of the resolved root.
`TombstoneInstance` and `UntombstoneInstance` do the tombstone rename of
Decision 1.
`LedgerStore` reads and writes `~/.niwa/w-<key>/ledger/<instance>.json`
atomically with mode 0600 and removes an entry after its instance is
destroyed.

**`internal/workspace/launch_store.go`.** `LaunchRecord` gains optional
`Origin`, `ReapedAt`, `RestoreUntil` and `KeptEntry`; the schema name stays
`niwa.launch/v1` since every field added is optional.

**`internal/workspace` (apply lock).** Root `niwa apply` holds an exclusive
lock at `~/.niwa/locks/a-<workspace key>.lock`; the trim tries it without
blocking and skips trimming when it's held.

**`internal/config/session.go`, `config.go`.** `SessionOverConfig`,
`SessionRemovalConfig` (with its enums, durations, the seven-day floor and
pattern validation), and `RepoOverride.BuildTrees`.

**Docs and tests.** `docs/guides/over-command.md` (the seam contract and the
takeover key), updates to `docs/guides/ephemeral-session-instances.md`, and
`@critical` functional scenarios in a new
`test/functional/features/over-remover.feature` plus the held-unit scenario
in `ephemeral-session.feature`.

### Key Interfaces

Configuration:

```toml
[session.over]
command = 'exec my-over-check "$NIWA_REMOVE_PATH"'
timeout = "20s"
env = ["KOTO_HOME"]

[session.removal]
ignored_files = "disposable"   # or "unique"
job_scratch = "disposable"     # or "unique"; set "unique" with an over command for shirabe's workflows
trim = true
trim_after = "24h"
restore_window = "14d"         # unset: Claude Code's cleanupPeriodDays; minimum 7d
disposable = [
  { pattern = "*.orig", review = "https://github.com/acme/app/pull/12" },
]

[repos.app]
build_trees = ["dist/"]
```

The over command's contract: run as `/bin/sh -c <command>` from the
workspace root with `PATH`, `HOME`, `NIWA_REMOVE_PATH` and
`NIWA_SESSION_IDS` set; exit 0 over, 1 not over, anything else unknown; the
last non-empty line of standard output is the reason; on the deadline the
process group gets `SIGTERM`, then `SIGKILL` two seconds later.

Per-unit files under `<instance>/.niwa/`:

```
retired.json          the retire mark, the earlier remover's format
keep.json             keep record and handoff brief
discard.json          discard record (niwa discard)
pids/<pid>.json       pid records (niwa.pid/v1, written by niwa helper)
unit.json             the unit record (niwa.unit/v1), written by the pass
```

Every file here is agent-writable and read as untrusted: `O_NOFOLLOW`, a
regular file owned by the user, size-capped, strictly decoded, every path in
it through `ResolveRecorded`, and the mark, `discard.json`, pid records and
`unit.json` valid only with a matching ledger entry.

```json
{"schema":"niwa.keep/v1","instance":"tsuku+bench-77c10d2e","owner":"dana",
 "reason":"benchmark results","written":"2026-11-20","written_at":"2026-11-20T14:02:11Z",
 "review":"2026-11-21","protects":["files"],
 "files":[{"path":"bench/results.csv","sha256":"9f2c...","bytes":20480,"in_brief":false}],
 "brief":{"written_at":"2026-11-20T14:02:11Z"}}

{"schema":"niwa.discard/v1","instance":"tsuku+bench-77c10d2e","owner":"dana",
 "paths":[{"path":"notes.txt","written_at":"2026-11-20T14:05:00Z"}],
 "patch":{"path":"discarded.patch","sha256":"41ab..."}}

{"schema":"niwa.pid/v1","pid":48211,"started":"2026-11-20T13:10:04Z",
 "command":"koto","purpose":"watch for review comments","written_at":"2026-11-20T13:10:04Z"}

{"schema":"niwa.unit/v1","answer":"not_over","exit":1,"reason":"pull request open",
 "asked_at":"2026-11-20T15:00:02Z","stopped_since":"2026-11-17T10:41:00Z",
 "size_bytes":2254857830,"size_measured_at":"2026-11-20T15:00:03Z",
 "verdict":"keep","kept_by":{"check":"liveness","paths":[],"evidence":""},
 "disagreement":null,"mark_voided":null,"reached_at":"2026-11-20T15:00:03Z",
 "restored_at":null}
```

Host files:

```
~/.niwa/locks/a-<workspace key>.lock                 root apply lock
~/.niwa/locks/k-<workspace key>.lock                 kept-set lock
~/.niwa/w-<workspace key>/kept-files/<instance>/<UTC>/   0700
    files/<in-unit path>                             named files, unchanged, no setuid bits
    keep.json                                        paths rewritten, 0600
    entry.json                                       niwa.kept/v1: due, first_shown_due, files; 0600
~/.niwa/w-<workspace key>/ledger/<instance>.json     niwa.ledger/v1, 0600
<root>/.niwa-launch/<launch id>.json                 niwa.launch/v1 + origin, reaped_at,
                                                     restore_until, kept_entry
<root>/.niwa-tombstone-<instance>-<8 hex>/           an instance between its last check and delete
```

```json
{"schema":"niwa.ledger/v1","instance":"tsuku+bench-77c10d2e",
 "instance_created":"2026-11-20T09:12:40Z",
 "records":{"retired.json":{"sha256":"c03e...","bytes":612,"written_at":"2026-11-20T15:00:02Z","writer":"over"},
            "discard.json":{"sha256":"41ab...","bytes":233,"written_at":"2026-11-20T14:05:00Z","writer":"discard"}}}
```

Every git call niwa's removal makes (Decision 8):

```
env (nothing else inherited):
  PATH=<pass PATH without entries under the workspace root>  HOME=<home>  LANG=C
  GIT_CONFIG_NOSYSTEM=1  GIT_CONFIG_GLOBAL=/dev/null
  GIT_TERMINAL_PROMPT=0  GIT_ASKPASS=
  GIT_ALLOW_PROTOCOL=https:ssh
  GIT_SSH_COMMAND="ssh -o BatchMode=yes -o ForwardAgent=no"
  GIT_PAGER=cat  GIT_OPTIONAL_LOCKS=0  GIT_NO_LAZY_FETCH=1
  GIT_CEILING_DIRECTORIES=<workspace root>
args:
  /abs/git --no-pager --git-dir=<from walk> --work-tree=<from walk>
    -c core.fsmonitor=false -c core.hooksPath=/dev/null -c core.pager=cat
    -c credential.helper= -c submodule.recurse=false -c fetch.recurseSubmodules=false
network calls (ls-remote, fetch): --git-dir=<scratch store>, literal validated URL,
  plus -c credential.helper=<person's helper> only for the workspace's clone URL
keep without running: a repository-scope filter.*.{clean,smudge,process},
  diff.external, diff.*.{textconv,command}, merge.*.driver or credential.*.helper
```

Commands and outputs:

```
niwa retire <instance> [--override-not-over]
  niwa: retired <name> and removed it
  niwa: retired <name>; kept by <check>: <paths>
  niwa: cannot retire <name>: its session is working
  niwa: cannot retire <name>: its over command answered not over (<reason>); pass --override-not-over to retire it anyway
  niwa: cannot retire from inside an instance; run it from a shell outside the workspace's instances

niwa discard <instance> <path>... [--patch <file>]
  niwa: cannot discard from inside an instance; run it from a shell outside the workspace's instances
niwa helper <pid> --purpose <text>
  niwa: cannot record <pid>: it is not a child of the calling process
niwa reap [--verdict-only [<instance>] [--json]]
niwa list [--json] [--kept]
niwa resume <target>
  niwa: cannot resume <name>: its restore window ended on 2026-12-20

dispatch line:
  niwa: <N> stopped unit[s] not retired, <size>; <D> kept-set entr(y|ies) due; <U> unit[s] not reached (niwa list)
```

Go entry points:

```go
package removal

func Evaluate(ctx context.Context, u Unit, p Policy) Verdict
func ResolveRecorded(base Base, path string) (string, error) // Base: the instance or the unit's job directory
func ScrubEvidence(s string, max int) string

type Verdict struct {
    Kind   Kind      // Reap, Keep, KeepAndTrim
    Checks []Check   // every keeping check, in rule order
    Paths  []PathClass
    Trim   []string  // build trees the trim would remove
    SizeBytes int64
}

package cli

func runPass(ctx context.Context, root string, mode passMode, scope passScope) passResult
```

### Data Flow

An all-unit pass, before a dispatch:

1. Load the workspace configuration (`loadWorkspaceConfig`) and build the
   policy: settings, patterns, build trees, the over seam and the gate. A load
   error ends the pass with nothing removed; a `restore_window` under seven
   days fails the command before the pass.
2. Start the 30-second deadline. Resolve git and `lsof` to absolute paths
   from a `PATH` with workspace entries removed. Read `claude agents --json
   --all` once, the job entries, the session mappings, the launch records,
   the ledgers and one process listing.
3. Enumerate units: every instance, with its sessions and job directories.
   A tombstone left by an interrupted pass is renamed back when its name is
   free, or kept and listed. Sort by `reached_at` in a ledger-matched
   `unit.json`, never-reached first.
4. For each unit while the deadline allows: try its instance lock without
   blocking (held means skip it this pass, no failure). Read its records as
   untrusted input, each size-capped, checked against the ledger, and every
   path in them through `ResolveRecorded`. Decide liveness. A working unit is
   noted as working and left alone.
5. For a stopped unit with no valid mark, no hold and no answer since it last
   worked, and with an over command configured, ask the seam if the remaining
   budget covers its timeout. Exit 0 writes the mark and its ledger entry;
   any other outcome clears a mark written during the call, still under the
   instance lock. Store the answer, scrubbed.
6. Run `removal.Evaluate`, every git call built by `gitCommand`. A unit is a reap candidate when it carries a valid
   mark, when its session is gone (the delete-then-reap path, which needs no
   mark and no launch record), or when the backstop finds it unmapped.
   Anything else that's stopped and has idle build trees is a trim candidate.
7. For a reap: ask the gate (step names as today: `reap`, `reap-backstop`,
   and the new `retire`), re-read sessions, processes and git state, and keep
   with "changed during the pass" when anything differs. Then move named files
   and a discard patch into the kept set (regular files only, re-resolved,
   0700 entry), remove worktrees inside the instance, `claude rm` the
   sessions, run on-reap, rename the instance to its tombstone and repeat the
   process, git status and record checks there (any change renames it back
   and keeps), `DestroyInstance` on the tombstone, remove the ledger, delete
   the mapping, and write `reaped_at`, `restore_until` and `kept_entry` into
   the launch record. Any refusal is a disagreement, kept and named with its
   message scrubbed.
8. For a trim: check the root apply lock, no keep record protecting build
   trees, no process with its working directory in the tree or a file under it
   open, the newest write older than `trim_after`, and the gate (step
   `trim`); then remove
   each tree. The instance, checkout and session stay.
9. Write `unit.json` (0600) with the answer, time stopped, size, verdict,
   scrubbed evidence and `reached_at`, and its hash into the ledger.
10. Handle the kept set: mark entries due on their date and note in
    `entry.json` when each was first shown, and remove entries first shown due at least a day before.
11. Stop when the deadline is reached; count the units not started. Print the
    dispatch line.

A retire runs steps 1, 4, 6 to 9 for one unit after writing its mark, with no
over question and no all-unit deadline. The verdict-only mode runs steps 1 to
6 for every unit or the one named, asks the seam where step 5 would, clears
any mark the call wrote, computes the verdict the mark would lead to, and
prints; it writes nothing else. `niwa resume` reads the launch record, checks
`restore_until`, provisions the instance by name, re-validates every kept-set
path against the fresh instance and the entry's `files/`, moves the files
back with a hash check and without following links or overwriting anything,
and starts the session.

## Implementation Approach

The phases follow the three parts of Decision 7: phases 1 and 2 are the rule
and worktrees, phases 3 and 4 the over question, mark and listing, and
phases 5 and 6 the keep record, kept set and launch record. Each phase ends
with niwa safe to release.

### Phase 1: The rule and reap's loss scan

The rule package and the pass, wired into every reap path, with no new
reason to remove anything. Reap gets stricter on purpose: an instance whose
session was deleted but that holds unique work is now kept and listed.

Deliverables:
- `internal/removal` with `Evaluate`, the walk, path classes, build-tree
  markers, generated paths, git checks with the scratch object store,
  patterns, `discard.json` reading and `.niwa/dispatch-retain` as a hold for
  unmapped instances.
- `gitCommand` with the hardened environment and overrides, the
  program-naming configuration check, remote URL validation and network
  calls against the scratch store with the clone-URL credential rule; the
  walk's no-symlink, one-filesystem and external-git-directory rules;
  `ResolveRecorded`; `ScrubEvidence`; size-capped, `O_NOFOLLOW`, strictly
  decoded record readers.
- `LedgerStore` and the ledger match for `discard.json`.
- `internal/cli/pass.go` with the deadline, ordering, re-read, the tombstone
  rename and check with tombstone recovery, and the mapped sweep and backstop
  routed through the rule; kept instances printed with their first keeping
  check. `DestroyInstance`'s `Lstat` and root-child checks; lock files 0600
  with `O_NOFOLLOW`.
- `[session.removal]` with `ignored_files`, `job_scratch` and `disposable`;
  `[repos.<name>] build_trees`.
- `niwa discard`, refusing inside an instance through `insideInstance`.
- `niwa reap --verdict-only [<instance>] [--json]` without over answers.
- Unit tests per check and class; `@critical` functional scenarios for
  reap's loss scan (clone and worktree), an unreachable remote, a nested
  repository, the path-class cases and the overlapping-pass and held-lock
  cases.
- Security scenarios: a repository with `core.fsmonitor`, `core.hooksPath`,
  `core.sshCommand`, `core.pager` and `credential.helper` set to a script
  that writes a marker file runs none of them through a reap and the
  verdict-only mode; a `filter.x.clean` in repository config keeps the unit
  with the key as evidence; an `ext::` and a `file://` remote are never
  contacted and keep; a symlink inside the instance pointing at a file
  outside is never read or summed; a `discard.json` written by hand without a
  ledger entry releases nothing; a `discard.json` path through a symlink or
  with `..` keeps; a `.git` file pointing outside keeps; a write into the
  instance after the re-read renames the tombstone back and keeps; a
  credential in a remote URL never appears in `unit.json` or any output.

### Phase 2: Worktrees before the session, and every other removal

Deliverables:
- Worktrees inside the instance removed before `claude rm` in the pass and
  in `runDestroyInstance`, through `gitCommand`.
- Failed-dispatch cleanup and the watch's discard calling `removal.Evaluate`
  before `destroyInstanceFunc`; the hook worktree sweep evaluating each
  worktree's repository checks.
- Functional scenarios: a clean unit holding a niwa worktree leaves no session
  behind after a reap and after `niwa destroy`; failed-dispatch cleanup keeps
  `partial.txt`; no `claude` call a wrapper logs carries an override flag,
  and no `git` call a wrapper logs carries `--force` outside `niwa destroy
  --force`.

### Phase 3: The over seam, the mark and the retire command

Deliverables:
- `[session.over]`, `overSeam`, and the mark writer, reader and voiding
  rules, with the mark's ledger entry and the clean-up under the instance
  lock.
- `niwa retire` with `--override-not-over`, refusing inside an instance
  through `insideInstance` (`internal/cli/guard.go`).
- Marked-unit reaps in the pass, needing the launch record dispatch already
  writes; a marked unit without one kept with that reason.
- `docs/guides/over-command.md` with the contract and the takeover key, and a
  functional test of the key against a real `koto init --supersede`.
- Functional scenarios for the three answers, a timeout leaving no process and
  no mark, a held marked unit not asked, a mark that doesn't parse, a copied
  mark, a removed mark, a mark written by hand with no ledger entry, a retire
  run from inside an instance and from a process whose ancestor sits in one,
  a disagreement, and the six kinds kept and listed.

### Phase 4: The listing, the dispatch line, the trim and pid records

Deliverables:
- `unit.json` written by the pass with its ledger entry; `niwa list` state
  lines and the JSON `unit` object, re-scrubbed on read; the dispatch line;
  reap's counts.
- Over answers in the verdict-only mode, with the mark each would write.
- The trim with `trim` and `trim_after`, the root apply lock, and the process
  checks for build trees.
- `niwa helper` with its child-of-caller and not-a-session checks and its
  ledger entry; pid record reading in liveness; the pid record format and
  `niwa helper` in the guide.
- Functional scenarios for the no-command dispatch line, the idle build tree,
  the time budget and units not reached, the local listing with the network
  off, a pid record written by hand that doesn't release a live process, a
  `niwa helper` call naming a process that isn't the caller's child, and a
  `unit.json` carrying terminal escapes that the listing prints clean.

### Phase 5: Keep records, the handoff brief and the kept set

Deliverables:
- `keep.json` holds, review dates, build-tree protection and named files;
  holds ended by later work.
- The handoff brief reader and the newer-than-the-brief test.
- `keep.json` and brief file paths through `ResolveRecorded` on every read.
- The kept set at `~/.niwa/w-<key>/kept-files/` with the cross-volume move
  of regular files only, 0700 entry directories and 0600 metadata, setuid
  bits cleared, due dates, the day's notice, `niwa list --kept` showing paths
  and sizes only, and the line for due entries; `DestroyWorkspace` leaving it
  in place and printing its path.
- Functional scenarios for the keep-record, newer-scratch and
  held-unit cases, including the read-only kept-set volume, and a keep record
  naming a symlink to a file outside the instance, which keeps the unit and
  copies nothing.

### Phase 6: Launch records for hook sessions, the restore window and restore

Deliverables:
- `niwa instance from-hook` writing launch records; the mapping's
  `launch_id`.
- `reaped_at`, `restore_until` and `kept_entry` on the launch record;
  `restore_window` with the seven-day floor.
- `niwa resume` refusing past the window and restoring kept files first,
  every path re-validated, parents created without following links, files
  created with `O_CREAT|O_EXCL|O_NOFOLLOW`.
- Functional scenarios for restore inside and past the window, with and
  without the setting, for an `entry.json` edited to name `../` or an
  absolute path and a fresh instance with a symlink where a kept file goes
  (both refuse and write nothing outside the instance), and for the
  three-day setting failing the configuration load.

## Open Dependencies on the Earlier Stage

Each point below comes from the earlier workspace-level remover, which hasn't
landed. The position given is where that work stands now. niwa follows it as
this design describes, and if it changes before landing, this design follows
the change rather than settling the point on its own.

1. **The over command's contract.** Current position: `over <instance>`, or
   the path in `NIWA_REMOVE_PATH` with no argument; exit 0 over (and that form
   writes the mark), 1 not over, 2 usage, 3 a fact couldn't be read, 124 on
   its deadline; the reason on the last output line; a 20-second default
   deadline, and a backstop in its own pass of the deadline plus 10 seconds.
   Its check-only entry point, `over check`, writes nothing and is named as the
   form niwa calls. Settled by that work's command contract as it merges.
   niwa's side: it runs the configured command with the removal gate's calling
   shape, reads only the exit status, uses 20 seconds as the default per-run
   timeout, and makes the backstop unnecessary by writing the mark itself and
   clearing any mark written during a call that didn't exit 0.
2. **The retire mark's schema, place and voiding.** Current position:
   `<instance>/.niwa/retired.json`, with a schema name namespaced to that
   remover; required name, creation time, marked-at and marked-by fields and a
   basis; later session activity or a moved branch head voids it. Its reader
   also counts a mark only when its hash and marked-at time match a copy in
   that remover's own launch record outside the instance, which niwa can't
   read. Unsettled there: its launch record names a branch's repository by
   path while the mark names it by a bound key. Settled by that work's mark
   reader and by its step that first compares the two shapes. niwa's side:
   same file, same fields, same voiding, the repository named by its path
   inside the instance until that step settles one shape, and the same kind of
   outside copy, kept in niwa's own ledger (Decision 9), so neither reader
   counts a mark the unit's agent wrote, and each counts only marks its own
   writer made. niwa's reader is pinned to that remover's exact schema name
   and field set and reads any other schema version as no mark, so a change
   in that format can only make niwa count fewer marks; a conformance fixture
   taken from that work's own writer joins the tests when it lands.
3. **The keep record's form and the kept set's layout.** Current position:
   keep records written by hand into that remover's own state, never expiring
   (past the review date they still keep and are only flagged due), and a kept
   set of one directory per unit and move with a manifest, which never
   expires. Settled by that work's keep-record reader, detailed but not built,
   and its kept-set move, not yet built. niwa's side: the same fields in
   `<instance>/.niwa/keep.json` plus `protects`, `bytes`, `in_brief` and
   `brief`; a kept set at `~/.niwa/w-<key>/kept-files/` whose entries go due
   and leave as R27 requires (the first clash, reconciled in Decision 5).
4. **The pattern list's format.** Current position: one entry per line, the
   pattern, a tab, and the review that added it; a line naming no review is a
   parse error that keeps every unit; no pattern syntax is defined. Settled by
   that work's list format and the first additions from its review of shadow
   passes. niwa's side: the shipped list and the workspace's `disposable`
   entries, each with a required review, in the glob-plus-`**` syntax Decision
   6 defines, offered as the syntax that work adopts.
5. **The launch record.** Current position: that remover writes its own per
   unit, because it can't change niwa, naming the session, job directory,
   transcript, repositories, branches, a copy of the last mark and a
   handoff-brief field that is always empty so far. Settled by that work's
   launch-record step and by whether it moves to niwa's record once niwa writes
   one for every unit. niwa's side: its own `niwa.launch/v1` record serves the
   reap-on-a-mark check and restore, written for hook sessions too; the
   handoff brief is read from `keep.json`.
6. **The pid record.** Current position: nobody writes one; that work leaves
   the writer to the work that follows it, which is this. Settled here: the
   starter of a background helper runs `niwa helper`, which writes
   `pids/<pid>.json` in the `niwa.pid/v1` format with a ledger entry, and
   niwa reads it. Open only in that the earlier remover's liveness check
   adopts this format when it lands.
7. **The discard record.** Current position: the unit's owner writes it by
   hand, naming paths in the unit and optionally a patch file; no command
   writes one. Settled here: `niwa discard` writes `discard.json` with that
   form's fields and a ledger entry, and a hand-written one counts for
   nothing in niwa. Open only in that the form may change before that work
   lands, in which case `niwa discard` follows it.
8. **The undo interval.** Current position: none; a fresh mark can carry a
   reap in the same pass, and only a unit seen for the first time waits one
   interval, because that remover's launch record is new. Settled by that
   work's pass order as it merges. niwa's side: no interval; the restore
   window is the undo.
9. **The interim scratch copy.** Current position: a 48-hour copy of a unit's
   job directory and loose root files, skipped in shadow passes, made for
   every reaped unit because the handoff-brief field is always empty there.
   Settled by that work's acting step. niwa doesn't carry it: a unit with no
   brief lets its scratch go by the project owner's default, and a workspace
   that wants it kept sets `job_scratch = "unique"`.
10. **The takeover key.** Current position: that remover reads no key saying a
    successor took over. Settled here by Decision 3: the fence naming a
    successor that holds `koto/superseded-carry-complete`. Open only in that
    the key has been read from koto's source and not yet seen in a live run,
    which the seam's functional test closes.

## Relation to Other Work

**tsukumogami/niwa#300**, the reap's missing loss scan, is closed by phase 1:
the mapped sweep and the backstop evaluate the rule before removing, a scan
error keeps, and the other non-interactive removals (failed-dispatch cleanup,
the watch's discard) go through the same rule.

**tsukumogami/niwa#458** asks for a way to keep one chosen instance from reap.
The holding form of `keep.json` is that first-class marker, and `niwa list`
shows the unit as held with its owner and reason. `.niwa/dispatch-retain`
keeps its meaning for the backstop and is read as a hold by the rule.

**tsukumogami/niwa#459** asks for a functional test of reap keeping a marked
instance and reclaiming an unmarked one. The phase 5 scenario with two
dispatched units, one held, both sessions deleted, is that test, in
`ephemeral-session.feature`.

**tsukumogami/niwa#536** implements `docs/designs/DESIGN-plugin-release-hold.md`
and reads `~/.niwa/w-<key>/kept/` as a directory of whole kept instances.
This design's kept set sits in `kept-files/` beside it and writes nothing to
`kept/`, and both compute the same workspace key; that's the overlap to check
when either lands.

**Destroy leaving sessions behind.** Today `niwa destroy` runs `claude rm`
before it removes the directory, so a session whose worktree sits inside the
instance still has that worktree when Claude Code is asked to drop it, the
request is refused, and the session outlives the destroy. Phase 2 removes the
worktrees first, in destroy and in the pass.

**A worktree-cleanup policy proposed separately** would remove a worktree
seven days after its spawn finished whatever it holds, archive at teardown,
and run a periodic `gc` verb that has since been dropped. Its time-based
deletion would remove exactly the worktrees this rule keeps for holding
unique work. This design's removal rule takes precedence for every removal
niwa makes: a worktree is a path like any other and goes only when the rule
passes it. Superseding that policy is outside this design.

**The delete-only teardown decision** in
`docs/guides/ephemeral-session-instances.md` and
`docs/designs/current/DESIGN-ephemeral-session-instances.md`, that a finished
session's instance is kept until the session is deleted, holds for every
workspace with no over command: nothing goes unasked but idle build trees.
A workspace that names a command changes it for itself.

**`docs/designs/DESIGN-instances-outlive-sessions.md`** describes the
before-remove gate and the launch records `niwa resume` replays. This design
keeps the gate at every removal, adds the `retire` step name, and extends the
launch record with optional fields.

**A workspace hook that denies `niwa reap`** is untouched; the workspace
decides when to lift it now that reap applies the rule.

**The shirabe command's later work** ships an over command for shirabe's
workflows against the contract in `docs/guides/over-command.md`. It inherits
the six kinds of unit a shipped command never marks, which niwa shows as
whatever answer and reason the command gives, each kept and listed with its
size and time stopped until a person retires it:

| Kind of unit | Answer | Shown as |
|--------------|--------|----------|
| A merged pull request whose workflow run is still live | not over | `not over: run not finished` with the run's state |
| A run that ended blocked, waiting on a person | not over | `not over: blocked, waiting for a person` |
| An abandoned pull request that is still open | not over | `not over: pull request open` |
| A session whose work a person moved elsewhere by hand | unknown | `unknown: no takeover found` |
| A top session started by a person, with nothing above it | unknown | `unknown: no evidence` |
| A plain chat with no workflow run | unknown | `unknown: no evidence` |

niwa doesn't tell these apart; it shows the reason the command printed. A
not-over unit goes only through `niwa retire --override-not-over`, an unknown
one through a plain `niwa retire`, and either way the rule decides what goes.

## Security Considerations

The trust boundary is the instance. The unit's agent is untrusted, and every
file in its instance and job directory is untrusted input: the repositories
and their `.git` directories, the records under `.niwa/`, `unit.json`, and
anything a kept-set entry carries back at restore. The pass runs as the
person, automatically, before every dispatch, create and watch, so whatever
it runs or deletes on the strength of that input acts with the person's
rights. Each part below gives the risk, what the design does about it, and
what is left.

### External artifact handling

niwa reads four kinds of untrusted input: the over command's exit status and
output, the per-unit records, the git repositories inside the instance, and
the kept-set entries it reads back at restore. Git is the largest. A
repository's configuration can make `git status` or a fetch run a program
(`core.fsmonitor`, hooks, a pager, `core.sshCommand`, askpass, credential
helpers, filter, diff and merge drivers) or contact a host of the agent's
choosing through an `ext::` or `file://` URL. The records are the second: a
mark, a discard record and a pid record together could turn an agent's own
unpushed work into something the next pass deletes.

Every git call goes through `gitCommand` (Decision 8): no inherited
environment, system and global configuration ignored, hooks, fsmonitor,
pagers, askpass and credential helpers off, only `https` and `ssh` allowed,
`--git-dir` and `--work-tree` taken from the walk, and a repository whose own
configuration names a filter, diff, merge or credential program kept without
running anything more in it. Network calls use the literal, validated URL
against the scratch store, so no repository setting applies to them. The
mark, `discard.json`, pid records and `unit.json` count only when they match
the ledger outside the instance (Decision 9), and pid records come only
through `niwa helper`, which accepts a process only when it is the caller's
own child. Every record is read with `O_NOFOLLOW`, a size cap and strict
decoding, and any failure lands on the keeping side. The over command's
reason, git evidence and every other string a unit supplies pass
`ScrubEvidence` and `sanitizeRemovalGateText` before they're stored and again
when `niwa list`, `--json` or the dispatch line reads them back.

What is left: git still parses objects, the index and configuration the
agent wrote, and a parser flaw in git is outside niwa's reach. The refusal
covers the program-naming keys git has today; one a later git release adds
would need adding, though the fixed overrides and the scratch-store network
calls don't depend on that list. The handoff
brief is written by the unit's own workflow by design, so a brief can make
job scratch and loose root files older than it disposable, and no ledger can
vouch for it.

### Permission scope

The pass deletes directories, moves files between volumes, and runs
`git worktree remove`, `claude rm` and on-reap as the person. A recorded
path through a symlink could have the kept-set move copy a secret from
outside the instance into a directory that persists (a `keep.json` naming
`bench/link` pointing at `~/.ssh/id_ed25519`), or a restore could write
outside the fresh instance. The walk could follow a link or a mount out of
the instance, `os.RemoveAll` could be handed a symlinked or misplaced
directory, and a write landing between the last check and the delete is
lost unseen.

Every recorded path passes `ResolveRecorded` on every read: relative, no
`..`, `job:` bound to the unit's own job directory, each component `Lstat`ed
with no symlink and no change of device. The kept-set move takes regular
files only, and the restore re-validates every path, creates parents and
files with `O_NOFOLLOW` and never overwrites. The walk never follows a link,
stays on one filesystem and keeps a unit whose git directory sits outside it.
`DestroyInstance` refuses a symlink and anything that isn't a direct child of
the root. The tombstone rename (Decision 1) takes the instance's path away
from late writers and repeats the cheap checks before the delete, renaming
back on any change. Lock files in `~/.niwa/locks/` are created 0600 with
`O_NOFOLLOW`, and the mark clean-up after an over answer runs under the
instance lock. niwa uses no sudo, opens no listener and passes no force flag
to `claude` or to git outside `niwa destroy --force`, which a functional
test checks. `niwa retire`, `niwa discard` and `--override-not-over` refuse
inside an instance, by working directory, by `NIWA_DISPATCH_ID` and
`NIWA_REAP_KEY`, and by process ancestry.

What is left: after the tombstone check, a process that already held a
descriptor inside the tree and that the process listing missed (`lsof` on
macOS can miss a short-lived one) can still write there before
`os.RemoveAll` finishes. The agent runs as the person, so niwa can't stop it
from writing outside its instance; whether it can is up to Claude Code's
permission settings, and the instance guard is advisory against an agent
that detaches and changes directory. The over command is code the workspace
chose, with the same trust as hooks and the removal gate; it now runs more
often, inside every dispatch, under its fixed environment, process-group kill
and the shared 30-second budget.

### Supply chain or dependency trust

niwa downloads nothing new. Its trust inputs are the over command, which
comes from workspace configuration like the hooks it already runs; the git
and `lsof` binaries; git's version, since `merge-tree --write-tree` needs
2.38 and `GIT_NO_LAZY_FETCH` 2.45; and formats borrowed from the earlier
remover, which hasn't landed.

Git and `lsof` are resolved once per pass to absolute paths from a `PATH`
with every entry under the workspace root removed, so an instance can't put
a binary in front of them. An older git makes the checks that need it
undecidable, which keeps. niwa's mark reader is pinned to the earlier
remover's exact schema name and field set and reads any other version as no
mark, so a change there can only make niwa count fewer marks, and a
conformance fixture from that work's writer joins the tests when it lands.
The `review` field on a workspace `disposable` pattern is free text niwa
doesn't check; it is an audit trail, not a control. The koto marker
`koto/superseded-carry-complete` is read only by over commands, never by
niwa.

What is left: the binaries on the person's own `PATH` are trusted, and an
over command can do anything its author wrote; niwa bounds its environment
and its time, not its behavior.

### Data exposure

The kept set copies files a person asked to keep, which can include `.env`
files, keys or results, into `~/.niwa/w-<key>/kept-files/`, where they stay
until their due date and survive `DestroyWorkspace` on purpose. Git error
text can carry a remote URL with a token in it into `unit.json`, the listing,
`--json` verdicts and the dispatch output. `ls-remote` and the scratch fetch
contact every remote a repository names, which tells those hosts the
machine's address and could offer them credentials.

Kept-set entry directories are 0700 and their metadata 0600, nothing is
copied through a link, and `niwa list --kept` shows paths and sizes, never
contents. `ScrubEvidence` strips URL userinfo and known token shapes and
bounds length before anything is stored or printed. Network calls offer no
credentials except the person's own helper for the clone URL the workspace
configuration names, and ssh runs in batch mode with agent forwarding off.
`unit.json`, the records niwa writes, the ledger and the launch-record fields
are 0600. `NIWA_SESSION_IDS` and `NIWA_REMOVE_PATH` go only to the configured
over command, whose fixed environment keeps other secrets out unless the
workspace lists them in `env`. Transcript modification times and sizes are
read, never copied or sent, and no telemetry is added.

What is left: a remote the agent added still learns the machine's address
and, over ssh, which public keys the person's ssh agent offers. Scrubbing
knows URL userinfo and a fixed set of token shapes, so a secret in another
shape inside a git error could still reach `unit.json`, which is 0600. Kept
files are exactly as sensitive as the files a keep record named.

### Data loss

The feature deletes on its own, so a wrong verdict loses work nobody chose to
lose. The rule keeps on doubt throughout: every check runs, any error or
undecidable answer keeps, a record that fails validation lands on the
keeping side, removal is never forced, and the gate, the re-read and the
tombstone check stand between the verdict and the delete. A forged mark,
discard record or pid record counts for nothing without its ledger entry.
The verdict-only mode shows every outcome before it happens, named files go
to the kept set with a hash check, and the restore window brings the session
and those files back.

What is left: the restore window restores the session and kept files, not
paths the rule found disposable or a discard record released (a discard
patch is kept). An agent that writes outside its instance can forge a ledger
entry as easily as a record. And a brief written by the unit's own workflow
can release scratch and loose root files older than it.

## Consequences

### Positive

- Every removal niwa makes on its own gives the same verdict, and a person or
  a plugin can see it first with `niwa reap --verdict-only`.
- Deleting a worker's session by hand no longer risks its unpushed work; the
  reap keeps the instance and names the path.
- A workspace that names an over command sees finished units go at the next
  dispatch with nobody acting, and can restore one for a known window.
- A user of niwa alone sees every stopped unit with answer, age and size, a
  count at every dispatch, and has one command that removes a clean unit
  without `--force`.
- Idle build trees in units that stay are trimmed by default, which is most
  of the disk a kept unit holds.
- Destroy and reap no longer leave sessions behind when an instance holds a
  worktree.
- Hook-provisioned sessions become restorable and can be reaped on a mark.

### Negative

- A dispatch can wait up to 30 seconds longer than today when stopped units
  are many or over commands are slow.
- Reap now keeps instances it used to remove, so hosts that relied on
  deleting sessions to free disk see kept instances listed instead.
- niwa follows formats it doesn't own: the mark's schema name, the keep and
  discard record fields and the over contract can still move.
- niwa counts only marks it wrote itself, so a mark the earlier remover or a
  person writes by hand into `retired.json` reads as no mark in niwa, and
  each remover ignores the other's marks.
- A repository whose own configuration names a filter, diff, merge or
  credential program (a per-repository LFS install, for one) keeps its unit
  until a person removes the setting or destroys the instance.
- A private `https` remote other than the workspace's clone URL gets no
  credentials, so a repository that pushed only there reads as unreachable
  and keeps.
- A helper's starter has to run `niwa helper`; a pid record written by hand
  no longer releases a live process, and a helper that daemonizes before
  registering can't be registered.
- A worker can no longer retire itself or write its own discard record:
  `niwa retire` and `niwa discard` refuse inside an instance.
- The ledger is a second host store per unit; a ledger lost or out of step
  makes every mark, discard record and pid record in its unit count for
  nothing.
- A reap takes a little longer for the tombstone check, and a restore
  refuses outright when a kept file's destination already exists in the
  fresh instance.
- With job scratch disposable by default, a retire of a unit with no brief
  lets its scratch go, and the user least likely to know scratch exists gives
  up that check unless they set it unique.
- The PRD's check that a hook-provisioned unit has no launch record stops
  arising once phase 6 lands.
- Measuring size by walking each stopped unit costs time on large build trees.
- A helper with a pid record keeps running after its unit is reaped, with its
  working directory gone.

### Mitigations

- The budget only caps how many units a pass reaches, never what it decides,
  and units not reached are counted on the dispatch line and taken first at
  the next pass.
- Every kept instance is listed with the check and path that keep it, so the
  fix (push, discard, retire) is named where the person looks.
- Each borrowed format is listed as an open dependency with what settles it,
  and the per-unit record readers accept the earlier forms (a keep record with
  no `protects` reads as a hold).
- A mark never skips the rule, and a mark with no ledger entry is listed as
  "mark not written by niwa", so a person sees why it didn't count and can
  run `niwa retire`; an over command's exit 0 writes niwa's own mark.
- A kept unit's listing names the configuration key that stopped the rule,
  so the fix (drop the setting, or `niwa destroy`) is named where the person
  looks.
- The listing names the authentication error and the remote; an `ssh` remote
  works with the person's own ssh agent, and the clone URL keeps the person's
  helper.
- The guide documents `niwa helper` and says to run it before a helper
  detaches; an unregistered helper keeps its unit, which is the safe side.
- The over command, which runs from the workspace root, answers for workers;
  a person retires from a shell outside the instances.
- Every failure of the ledger lands on keeping, and the next over answer,
  retire or discard writes a fresh entry.
- The tombstone check reuses the pass's own listing code and runs only for
  units already passing the rule; a refused restore leaves the kept-set entry
  in place and names the file, so nothing is lost.
- The verdict-only mode marks scratch it would let go with a `job scratch`
  notice, and the over seam's guide tells shirabe workspaces to set
  `job_scratch = "unique"`.
- The hook-session check's setup moves to a unit whose launch record was
  removed by hand, a PRD amendment made when phase 6 lands.
- Size is measured in the rule's own walk and stored in `unit.json`, so the
  listing and dispatch line read it rather than walking again.
- A pid record is the starter's statement that its helper ends on its own; a
  helper with no record keeps its unit.
