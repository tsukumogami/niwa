---
schema: prd/v1
status: In Progress
problem: |
  niwa removes a stopped unit of work only when its session is deleted or a
  person runs destroy, and it never asks whether the unit's work is finished.
  Reap runs no loss scan, so deleting a session by hand turns the next dispatch
  into an unchecked delete. Finished instances, clones, build trees and
  dispatch briefs pile up on every host, `niwa list` can't say which are done
  or what they cost, and the only safe way to clear them is a person choosing
  each one.
goals: |
  A workspace names one command that says whether a stopped unit is over, and
  from then on a unit it calls over, holding nothing unique, goes at the next
  niwa command with nobody acting, and can be restored for a while after. Every
  removal niwa makes on its own follows one rule that never takes unique work.
  A workspace that names no command loses nothing it didn't ask to lose: its
  stopped units are listed with their answer, age and size, one command retires
  a unit under the same rule, and idle build trees are trimmed.
motivating_context: |
  niwa keeps a finished session's instance until the session is deleted, by a
  recorded decision made after an earlier version reaped instances the moment a
  worker went idle. That stopped the old loss and left every finished unit on
  disk. An earlier workspace-level remover, built first in one workspace outside
  niwa, showed the shape of the fix and set out a command contract and a retire
  mark format. This PRD states what niwa must do to carry that remover for every
  workspace, and treats every point of the earlier remover's contract as an open
  dependency until that work lands.
absorbed:
  - docs/briefs/BRIEF-over-remover.md
---

# PRD: niwa removes a finished unit that a workspace command says is over

## Status

In Progress

The requirements depend on an earlier workspace-level remover, built first in
one workspace, whose command contract and retire-mark format this work follows
once that remover settles them. It hasn't landed. Every point this PRD takes
from it is listed under "Open dependencies on the earlier stage" and stays open
until it lands; this PRD doesn't settle any of them. Names used here (the retire
mark, the retire command, the settings) are placeholders until the design picks
them. What the design settles, and the job-scratch and idle-setting defaults
the project owner chose, are listed under Decisions and Trade-offs.

Amended after acceptance: the project owner chose the default for job scratch
in a unit with no handoff brief, the default of the idle-time setting, and how
the verdict-only mode shows job scratch. R1, R4, R11, R24, R28, R29 and R35,
the newer-scratch and verdict-only scenarios, Out of Scope, Known Limitations
and Decisions and Trade-offs carry the choice. The amended requirements stand
as accepted.

Amended after acceptance: R26 and R13 now say that paths a discard record
releases wait in the unit's kept-set entry under `discarded/` until the restore
window ends, and then go, where R26 had said nothing but the files a keep
record names goes into the kept set. Removing them with the instance would drop
the safety net for paths a person has just released, so a discard faked
through a terminal an agent controls still loses nothing until the window
ends. The amended requirements stand as accepted.

Amended after acceptance (2026-11-22): Known Limitations said a unit the
SessionStart hook provisions has no launch record, so a mark alone never
reaps it. The design writes a launch record for hook sessions too, and
`niwa resume` restores them, so that limitation now covers only a unit whose
launch record is gone. The restore scenario's last check, which started a
hook session to get a unit with no launch record, is amended for the same
reason: a hook unit now has a launch record, so the check shows it reaped
on a retire and restored by `niwa resume`, and gets a unit with no launch
record by removing a hook unit's record by hand. R18 is unchanged: a unit
with no launch record is still kept from a reap on a mark. The amended text
stands as accepted.

Amended after acceptance (2026-11-22): a review instance that `niwa watch`
provisions isn't removed by the rule in this release. The rule judges the
PR's fetched copy in it like any other repository, and that copy nearly
always keeps the instance, so it stays listed until a person destroys it. Telling
niwa's own fetch apart from what a review agent wrote, without handing an
agent a path it could use to make niwa delete or skip anything, needs work
that moves to a follow-up, tsukumogami/niwa#551. Known Limitations also
gains a second line: a watch stage that fails after its fetch, with its
instance kept, records that PR head as handled so it isn't staged into a
second instance, which leaves the head with no review until it moves;
tsukumogami/niwa#551 restores the retry for a stage that fails before the
review agent launches. R15 is unchanged: the watch's removals still apply
the rule, which keeps the instance; no requirement or acceptance check
expected a review instance to be removed, and Known Limitations gains both
lines. The amended text stands as accepted.

Absorbed [BRIEF-over-remover](docs/briefs/BRIEF-over-remover.md); carried in Absorbed Brief.

## Absorbed Brief

The feature exists because nothing in niwa knows when a unit's work is done.
niwa removes a stopped instance only after its session is deleted or when a
person names it to destroy, reap runs no loss scan, and destroy can pass every
check and still leave a session behind when the instance holds a worktree. So
finished instances, clones, build trees, transcripts and dispatch briefs stay
on the host, `niwa list` can't tell anyone which of them are done or what they
cost, and the only safe way to clear them is a person choosing each one. This
document's Problem Statement states that in full. The framing came out of a
recorded decision to keep a finished session's instance until the session is
deleted, made after an earlier version reaped instances the moment a worker
went idle, and out of a workspace-level remover built first outside niwa,
whose command contract and retire-mark format this feature adopts as they
settle.

The outcome asked for is a user who names one command that says whether a unit
is over and from then on sees finished units go at the next niwa command with
nobody acting, nothing unique lost and a window to restore; and a user who
names nothing, who loses nothing unasked, sees every stopped unit with its
answer, age and size and a count at each dispatch, retires one unit under the
same test, and has idle build trees trimmed. That's this document's Goals.

The seven scenarios (a command that answers over, not over and fails;
a workspace with no command; a keep record naming one file; a scratch file
newer than the handoff brief; a verdict-only run; an idle build tree in a unit
that isn't over; a restore inside and past the window) are the User Stories,
and each is worked out step by step as an acceptance scenario. The plugin
author's story is added beside them.

The boundary held the feature to the seam, the mark, one removal rule, the
listing and dispatch line, the retire command, keep records and the kept set,
the trim, the verdict-only mode and restore. It left out shipping any over
command, transcript expiry, an idle-time retirement setting, the earlier
remover's interim scratch copy, writing the handoff brief, lifting a
workspace's hook denial of `niwa reap`, and any daemon or timer; those are this
document's Out of Scope. It named the six kinds of unit a shipped over command
never marks, which R24 keeps listed until a person retires them, and the points
it left to the requirements and the design, which Decisions and Trade-offs and
the open dependencies carry. One of those points, the job-scratch default for a
unit with no handoff brief, the project owner has since chosen, along with the
default of the idle-time setting; Decisions and Trade-offs records both.

## Problem Statement

In niwa a unit of work is a session, the instance it runs in, its job directory
and its transcript. When the work is done, nothing in niwa knows. niwa removes
a stopped instance on two signals only. `niwa reap`, which also runs before
every `niwa dispatch` and `niwa create`, removes an instance once its session
has been deleted. `niwa destroy` removes one a person names. Neither asks
whether the unit's work is finished, and a workspace has no way to tell niwa
what finished means for it. The `[session.before_remove]` command can veto a
removal; it can't start one.

The two removal paths are also uneven about loss. Reap checks liveness and
nothing else. It never scans for unpushed or uncommitted work (niwa#300), so a
person who deletes a worker's session by hand turns the next dispatch into an
unchecked delete of that worker's instance. Destroy does scan, but it counts
every untracked file as loss, has no mode that reports without acting, and
removes the session before the directory. When the instance still holds a niwa
worktree, Claude Code refuses to drop the session, so a destroy can pass every
check and still leave the session behind.

So finished units stay. A stopped worker whose pull request merged a week ago
still holds its instance, its clone, a multi-gigabyte build tree, its
transcript and the brief it was dispatched with. `niwa list` shows it with no
state, no age and no size, so nobody can tell at a glance which of forty
instances are done or what they cost. The only safe way to clear them is for a
person to look at each one and decide. On a host running parallel agent work
that doesn't happen, and the stock grows until the disk fills.

## Goals

- A niwa user on any workspace configures one command that says whether a unit
  is over. From then on, a stopped unit the command calls over, holding nothing
  unique, is removed at the next niwa command with nobody acting.
- niwa reads only the command's exit status, so it needs neither koto nor a
  forge, and it never decides on its own that a unit is over. A unit that holds
  anything niwa can't regenerate, or whose answer niwa can't read, is kept and
  named with the reason.
- A unit removed this way can be restored, inside a window, from its
  transcript, and a restore past the window is refused with a reason.
- A user who configures nothing loses nothing they didn't ask to lose. No
  instance, transcript or dispatch brief goes without someone asking. Every
  stopped unit nobody has retired is listed with its size, a line at each
  dispatch says how many there are and how much disk they hold, and one command
  retires a unit and lets the same test decide whether it goes.
- Build trees left idle in units that stay are trimmed, so a kept unit holds
  only the disk it needs.
- A person who wants to keep a file from a unit that goes names it in a keep
  record and finds it later, unchanged, in a kept set on the host. A person who
  wants to see what niwa would do runs a verdict-only mode and gets every
  verdict with nothing touched.

## User Stories

These are the brief's seven scenarios. Each has a short name the requirements
and acceptance criteria use.

- **The three-answers scenario.** As a workspace maintainer who has configured
  an over command, I want a dispatch to remove the stopped unit my command
  calls over and keep the one it calls not over and the one it fails on, each
  listed with its answer, time stopped and size, so that only a yes removes
  anything and a failing command keeps.
- **The no-command scenario.** As a niwa user with no over command, I want each
  dispatch to tell me how many stopped units nobody has retired and how much
  disk they hold, and a retire command that keeps a unit holding an untracked
  file (naming the path) and removes a clean one without `--force`, so that the
  test, not a flag, decides what goes.
- **The keep-record scenario.** As a developer finishing a benchmark run, I
  want to name `bench/results.csv` in a keep record and find that file, with the
  same hash, in the host's kept set after the unit is reaped, and to see the
  entry shown as due before it ever leaves, so that a named file survives a
  reap byte for byte and doesn't go unannounced.
- **The newer-scratch scenario.** As a coordinator whose worker wrote a handoff
  brief naming `plan.md`, I want a scratch file the worker changed after the
  brief, `draft.md`, to keep the unit and be named as newer than the brief, so
  that a brief lets go only of what existed when it was written.
- **The verdict-only scenario.** As a maintainer about to turn removal on, I
  want a mode that prints, for each unit, the verdict it would get and the class
  of every path in it, and changes nothing on disk, so that I can check the
  rule's answer on my own units before trusting it to act.
- **The idle-build-tree scenario.** As a developer with a paused worker that
  isn't over, I want its idle cargo `target/` directory removed at the next niwa
  command after the idle grace, with the instance, its checkout and its session
  left as they were, so that kept units stop holding gigabytes they can rebuild.
- **The restore scenario.** As a developer who needs a reaped worker back, I
  want `niwa resume` to give me a working session at the instance's original
  path inside the restore window, and a refusal that says when the window ended
  once it has passed, so that a reap is undoable for a known period.

One more story isn't among the brief's scenarios, and the cross-cutting checks
cover it:

- **The plugin author.** As the author of a workflow plugin that ships an over
  command, I want the seam's documented contract and takeover key, and a
  machine-readable way to ask niwa for the rule's answer on one unit, so that
  neither my command nor my plugin carries its own copy of the removal rule.

## Requirements

Each requirement names the scenario or scenarios it serves. A few also serve
the cross-cutting checks at the end of the acceptance criteria.

### Terms

- **Unit**: a session, the instance it runs in, its job directory and its
  transcript.
- **Stopped**: the unit's session is neither running nor working, and no live
  process has its working directory anywhere in the instance. How niwa reads
  this from Claude Code's records is the design's.
- **Over command**: the one command a workspace names to answer whether a
  stopped unit is over.
- **Retire mark**: a durable record inside a unit that says the unit is over.
  A unit carries one when the over command answered yes or a person retired it.
- **Retire**: writing the retire mark for a unit.
- **Reap**: the removal of a unit nothing keeps. The instance and its session
  go; the transcript stays where Claude Code keeps it.
- **The pass**: the work niwa does for every unit wherever it runs its
  opportunistic reap today (before each `niwa dispatch` and `niwa create`), when
  `niwa reap` runs, and for one unit when a person retires it.
- **Verdict**: the pass's decision for a unit, exactly one of reap, keep, or
  keep and trim.
- **Build tree**: a git-ignored directory that carries a build-tree marker or
  that a repository declares as build output. The markers include at least
  cargo's `CACHEDIR.TAG`; the full list, and how a repository declares build
  output, are the design's.
- **Unique path**: a path the removal rule (R11) doesn't count as regenerable.
- **Keep record**: a record a person or a handoff brief writes for one unit,
  either holding the unit against reap or naming files to keep after it goes.
- **Kept set**: the host directory, outside every instance, that holds the
  files keep records name once their units are reaped.
- **Handoff brief**: a statement a unit writes for its successor, with a file
  list and the time it was written. This work reads briefs; it doesn't write
  them.

### Functional

#### The over seam

**R1. A workspace setting names the over command.** A workspace's
configuration can name one over command, configured the same way as the
existing session commands (a command, a timeout, and the names of environment
variables passed through). Leaving it out turns marking off: no unit gets a
retire mark except from a person. The idle-time setting, which belongs to later
work and ships off, is the only other route to a mark, and only for a workspace
that turns it on. A workspace that names an over command for shirabe's
workflows should also set job scratch unique (R28) in the same configuration.
Serves the three-answers and no-command scenarios.

**R2. niwa asks only about stopped units with no mark and no hold.** In each
pass niwa runs the over command once for each unit that is stopped, carries no
valid retire mark and isn't held by a keep record. It doesn't ask about a
working unit, a marked unit or a held one. A unit the pass doesn't reach is
asked at a later pass (R34). Serves the three-answers scenario.

**R3. niwa reads only the exit status.** An exit of 0 means over, and niwa
writes the retire mark. An exit of 1 means not over. Anything else, including
a timeout, a command that can't start and a command killed by a signal, means
unknown, and the unit is kept. No mark exists after any answer but over. The
command runs under a per-run deadline, whose default the design sets, and on
expiry niwa ends it and its process group. When the command states a reason in its output, niwa records it
for the listing (R22); how a reason is stated is an open dependency. Serves the
three-answers scenario.

**R4. niwa holds no second test of over.** niwa never decides that a unit is
over on its own account. It infers nothing from koto, a forge, a pull
request's state, a unit's age or how long it has sat idle, and the pass makes
no koto or forge call to decide whether a unit is over. The only routes to a
retire mark are an over answer of yes and a person's retire. A person deleting
a unit's session counts as that person retiring what remains, as it does today,
and the removal rule still decides what goes (R15). The idle-time setting the
project owner chose belongs to later work and ships off; nothing in this
feature ships it. A workspace that turns it on makes that choice itself: the
setting marks a unit stopped longer than the setting that no over command
answers, counted from its last stop or restore, and never marks one the
command answered not over. Serves the three-answers and no-command scenarios.

**R5. The design names the takeover key.** The design names the key an over
command reads to learn that a successor's takeover has landed, meaning the
successor finished taking over and not only that it started, and the over
seam's documentation carries that key. It isn't left to the later work that
ships a workflow plugin's over command. Serves the three-answers scenario and
the plugin author.

#### The retire mark

**R6. One mark, in the format the earlier remover settles.** niwa follows the
retire-mark format and place that the earlier workspace-level remover settles;
it doesn't settle either itself. It reads a mark written in that format and
writes the same format, at the same place in the unit, both on an over answer
and on a person's retire. A mark counts the same whoever wrote it. Serves the
three-answers and no-command scenarios.

**R7. A mark's life ends with the reap, and a bad mark is no mark.** A marked
unit is tested again at every pass and reaped once the removal rule lets it go.
A person can remove the mark at any time before the reap. The mark goes with
the instance at reap, so a restored unit starts unmarked. A mark that can't be
read, doesn't parse, or names another instance counts as no mark. Whether later
work in the unit voids a mark is an open dependency. Serves the no-command and
restore scenarios.

#### The retire command

**R8. One command retires one unit and runs the pass for it.** A person runs a
retire command naming a unit. niwa writes the retire mark and runs the pass for
that unit at once, so the removal rule decides whether it goes. The command's
output says either that the unit was removed or that it was kept, and for a
kept unit it names the first check that kept it, in the order R11 sets, and
every path behind that check. Retiring a unit whose session is working is refused, and the
refusal says so. A retire stops nothing that is running. Serves the no-command
and keep-record scenarios.

**R9. Overriding "not over" takes a person's flag, and never skips the rule.**
Retiring a unit whose last answer was not over needs an explicit flag that only
a person passes; without it the retire is refused and says why. The flag
overrides the answer only. It never skips any part of the removal rule, and the
pass itself never uses it. Serves the no-command scenario.

#### The removal rule

**R10. Every removal niwa makes on its own follows one rule.** The reap of a
deleted session's instance, the reap of a marked unit, the backstop for unmapped
dispatch instances, the trim, and the removal a retire starts all apply the
same removal rule, coded once in niwa. A workflow plugin that needs the rule's
answer asks niwa for it through the verdict-only mode's machine-readable output
for one unit (R29), rather than carrying its own copy. Serves the no-command
and verdict-only scenarios and the plugin author.

**R11. A unit goes only when every path in it is regenerable or resolved.**
The rule covers the instance and the job directory alike. A path is
regenerable when it is tracked and unmodified; committed work that the remote's
live refs hold or that equals merged work by content; a build tree; a file niwa
or Claude Code generates and can generate again (instance configuration,
context files, plugin and marketplace trees, a job's `state.json` and
`timeline.jsonl`); a clean, fully pushed clone wherever it sits, worktrees
included; an empty directory; a path matching a declared-disposable pattern
(R13); or, in a unit with no handoff brief and unless a workspace setting says
otherwise, a regular file in job scratch (R28). Everything else is unique:
untracked files, stashes, local-only refs, loose files outside a repository,
and job scratch where R28 counts it as unique.
A unique path resolves in exactly three ways: it is handed forward (its bytes
are inside a handoff brief or moved to the kept set), it is declared
disposable, or it keeps the unit. Every repository in the instance is found by
walking it, not from a fixed list. A unit whose session is working, or that has
a live process with its working directory in the instance, isn't stopped and
keeps whatever its paths hold; the keep names the session or the process.

The rule's checks run in a fixed order, and "the first check that kept" a unit
means the first in this order that keeps it:

1. Liveness: the session is working, or a live process has its working
   directory in the instance.
2. A keep record holds the unit (R25).
3. A record the rule needs is missing or unreadable, including the launch
   record a reap on a mark needs (R18).
4. Each repository found in the instance, in path order: a git error or an
   unreachable remote; commits no remote ref holds, and local-only refs;
   stashes; modified tracked files; untracked files.
5. Paths outside every repository: loose files, job scratch that counts as
   unique (R28), and files newer than a handoff brief (R28).
6. A file a keep record names that is missing or whose hash doesn't match
   (R26).

A step that can't be decided keeps at that step (R14). The verdict-only mode
names every check that would keep the unit, in this order (R29). Serves the
no-command, newer-scratch and verdict-only scenarios.

**R12. Git-ignored files outside build trees block nothing by default.** A
git-ignored path that isn't a build tree is disposable by default, as niwa's
loss scan treats it today, so a project's `.env` or `.venv` blocks no reap. The
verdict-only mode lists each such path as disposable and marks it as
git-ignored, so it stands apart from a path disposable by pattern. A workspace
setting makes them unique instead. Serves the verdict-only scenario.

**R13. Declared disposable means a reviewed pattern list or a discard
record.** A path is declared disposable when it matches a pattern in the list
niwa ships or in a workspace's own list in its configuration, or when a discard
record written for that unit names it. The shipped list includes at least
editor swap and backup files, `.DS_Store`, `__pycache__` and `.pytest_cache`;
its full contents are a deliverable of the design. Nothing joins either list on
a guess. A path a discard record names isn't removed with the instance: it
waits in the kept set under `discarded/` until the restore window ends, and
then goes (R26). The list's format and the discard record's form
and command are open dependencies. Serves the no-command and verdict-only
scenarios.

**R14. Anything niwa can't decide keeps the unit.** A step of the rule that
fails or can't be decided ends with keep and records the step's evidence. A
git error, an unreachable remote, an unreadable file or directory, and a
missing record the rule needs all keep. Serves the no-command and verdict-only
scenarios.

**R15. Reap scans for loss before it removes anything.** Every reap, in the
pass before dispatch and create and in `niwa reap`, applies the removal rule
before removing anything, both for a unit whose session was deleted and for an
unmapped instance the backstop finds. An instance holding a unique path is
kept and listed with that path, and a scan error keeps (niwa#300). The same
applies to every other removal niwa makes without a person naming the instance,
such as cleanup after a failed dispatch. Serves the no-command scenario.

**R16. The pass never forces.** The pass removes a unit only through niwa's
own unforced removal path. It never passes niwa's `--force`, and never passes
Claude Code's flags that discard unpushed work or force-remove a worktree. A
disagreement is a refusal by a removal step after the rule has passed the unit:
the `[session.before_remove]` veto, niwa's own unforced removal, or Claude
Code's removal of the session or a worktree. A disagreement keeps the unit and
leaves its mark in place for the next pass to test again. `niwa list` shows the
unit as kept by a disagreement, naming the step that refused and the message it
gave, and the output of a retire whose pass hit one says the same. Serves the
no-command scenario.

**R17. Worktrees go before the session, so the session goes too.** When a unit
passes the rule, niwa removes the worktrees inside its instance before it
removes the session, so a session whose worktree sits inside the instance is
removed with it and none is left behind. The rule reads the job directory
before the session is removed, never after. `niwa destroy` follows the same
order, so a destroy that passes its own checks also leaves no session behind.
Serves the no-command scenario.

**R18. A unit reaped on a mark must be restorable.** The pass reaps a marked
unit only when niwa holds the launch record a restore replays (R32). A marked
unit with no launch record is kept and listed with that reason. A person
deleting the unit's session still reaps it under the rule, as today. Serves the
restore scenario.

**R19. A unit that changes during the pass is kept.** niwa holds the
instance's lock while it acts on a unit, so two passes that overlap, as in a
burst of dispatches, never act on the same unit at once. A pass that finds a
unit's lock held leaves that unit for a later pass and doesn't fail. After the
last command niwa runs for a unit, the `[session.before_remove]` veto included,
and before it removes anything, niwa reads the session, the processes and the
git state again, and keeps the unit when any of them changed since its check.
The keep reason says the unit changed during the pass and names what changed.
Serves the three-answers scenario.

**R20. Today's veto and today's delete-then-reap path keep working.** The
`[session.before_remove]` command still vetoes every removal the pass makes,
trims and retires included, and deleting a unit's session still leads to its
reap, now under the removal rule. A vetoed retire or reap is a disagreement
(R16); a vetoed trim leaves the build tree in place. Serves the no-command and
idle-build-tree scenarios.

#### Where the pass runs

**R21. The pass runs inside niwa commands only.** The pass runs wherever niwa
runs its opportunistic reap today, which includes before each `niwa dispatch`
and `niwa create`, when `niwa reap` runs, and for one unit when a person
retires it. niwa adds no daemon, no timer and no background process for it, so
a host where no niwa command runs makes no pass. Serves the three-answers and
idle-build-tree scenarios.

#### The listing and the dispatch line

**R22. The listing shows each stopped unit's answer, age and size.** `niwa
list` shows every stopped unit nobody has retired with the answer it last got
(not over, unknown, or no command configured), how long it has been stopped,
the disk its instance and job directory take, and the reason, when one was
recorded. A marked unit the rule keeps is shown with the check and the path
that keep it. The machine-readable output of `niwa list` carries the same
fields. Serves the three-answers and no-command scenarios.

**R23. Each dispatch prints one line about stopped units.** Each `niwa
dispatch` prints one line counting the stopped units nobody has retired and
their total size, and the kept-set entries that are due (R27), whether or not
an over command is configured. A marked unit the rule keeps isn't in that
count or that size, since someone retired it; the listing shows it (R22). The
line points at the listing. Serves the no-command and keep-record scenarios.

**R24. A unit the command doesn't call over stays listed until a person
retires it.** A unit answered not over or unknown stays in the listing, with
its reason, size and time stopped, for as long as it sits. No date expires it
and nothing in this feature removes it unasked. The idle-time setting, later
work that ships off, could mark a unit answered unknown for a workspace that
turns it on, and never one answered not over. This covers the six kinds of
unit a shipped over command will never mark: a unit whose pull request merged
while its workflow run is still live; a unit whose run ended blocked, waiting
on a person; a unit whose pull request was abandoned but is still open; a unit
whose work a person moved to another session by hand, so no recorded successor
took it over; a top session, started by a person rather than dispatched, with
no parent session to hand back to; and a plain chat, a session started with no
workflow run at all. niwa doesn't tell these kinds apart: each reaches it as a
unit answered not over or unknown, which is how the acceptance check stands in
for them. In this feature each goes only when a person retires it. Serves the three-answers
scenario.

#### The keep record and the kept set

**R25. A keep record holds a unit or names files to keep.** A person, or a
handoff brief, writes a keep record for one unit. A record that holds the unit
keeps it from reap whatever its answer, and the unit is listed as kept, with
the record's reason and owner. Past the record's review date the unit is still
kept and is listed as due for review, never reaped. A record that names files
and doesn't hold the unit lets the unit go and keeps only those files. A record
can also protect the unit's build trees from the trim (R30). The session
working again ends a hold but doesn't drop the named files. The record survives
`niwa apply`. Serves the keep-record and idle-build-tree scenarios.

**R26. Named files land in the kept set unchanged.** When a unit with a keep
record that names files is reaped, niwa moves the record and the files it names
into the kept set before removing the instance, and rewrites each file's path
in the record to its place there. Each file lands with the same SHA-256 it had
in the instance; across volumes the move is a copy, a hash check and a delete.
A named file that is missing or whose hash doesn't match keeps the unit. Paths
a discard record releases (R13) also go into the unit's kept-set entry, under
`discarded/`, where they wait until the restore window ends and then go; a
restore doesn't put them back. Nothing else of the instance goes into the kept
set, and a unit with neither a keep record nor a discard record leaves nothing
behind. The kept set follows niwa's rule for host-level state:
under `~/.niwa/`, in a directory keyed by the workspace root's canonical path,
never under the workspace root's `.niwa/` or inside an instance. Serves the
keep-record scenario.

**R27. A kept-set entry is shown as due before it goes.** Each kept-set entry
carries a date. On that date it is marked due and shown in the listing and in
the dispatch line. It is removed at the first pass at least a day after it was
first shown as due, never before it has been shown. Serves the keep-record
scenario.

#### The handoff brief and job scratch

**R28. A brief lets go of what existed when it was written, and names what is
newer.** When a unit has a handoff brief, niwa reads its file list and the time
it was written. A file the brief names is handed forward: when the brief holds
its bytes it goes with the brief, and when the brief names it only by path it
moves to the kept set as a keep record's named file does (R26). Job-scratch
files and loose root files that existed when the brief was written and that it
didn't name are disposable. A file created or changed after the brief was
written, by its modification time or its inode change time, whichever is
later, is unique and keeps the unit, and the keep reason names that file as
newer than the brief. Without a brief, job-scratch files are disposable by
default, and a workspace setting, shaped like the ignored-files setting (R12),
makes them unique, so they keep the unit and are named; loose root files stay
unique as R11 says. Where a brief exists, the rule above applies whatever that
setting says: a file the brief names is handed forward, a file older than the
brief is answered for, and a file newer than the brief keeps the unit. A
workspace that names an over command for shirabe's workflows should set job
scratch unique in the same configuration. The project owner chose this default
(Decisions and Trade-offs). Serves the newer-scratch and verdict-only
scenarios.

#### The verdict-only mode

**R29. A verdict-only mode reports every verdict and changes nothing.** A
verdict-only mode prints, for each unit, the verdict it would get, every check
that would keep it in the order R11 sets, and the class of every path in it:
tracked, pushed, build tree, generated, disposable or unique. A job-scratch
file that is disposable only because the unit has no handoff brief is listed as
disposable and marked as job scratch, as a notice, in the same way R12 marks a
git-ignored path, so a person can see what a retire would let go before
setting job scratch unique. It asks the over
command where the pass would, reports the answer and the mark it would write,
and writes no mark. It runs for every unit or for one unit a person names, and
has a machine-readable form that carries the same fields. It removes nothing,
trims nothing, moves nothing into the kept set, and leaves every file on disk
as it was. Serves the verdict-only scenario and the plugin author.

#### The trim of idle build trees

**R30. The trim is a verdict of the pass, and the unit stays.** For a stopped
unit the pass keeps, niwa removes each build tree only when all of these hold:

- its newest write is older than the idle grace (R31);
- no build process is running on it, where a build process is any live process
  whose working directory is inside the tree or that holds a file under it
  open (how niwa finds such processes is the design's);
- no root `niwa apply` is in flight;
- no keep record protects that unit's build trees;
- the `[session.before_remove]` veto allows it (R20).

The instance, its checkout and its session stay. The session keeps its id, its
transcript and its working directory, so it can be resumed as it could before
the trim. The trim is part of the pass, not a command of its own. Serves the
idle-build-tree scenario.

**R31. The trim is on by default, and a setting turns it off.** The trim runs
with no configuration. A workspace setting turns it off, and another sets the
idle grace. With no grace setting, a default grace applies, whose figure the
design sets. Serves the idle-build-tree scenario.

#### Restore

**R32. A reaped unit can be restored inside its window.** `niwa resume` on a
unit reaped within its restore window gives a working session, rooted at the
instance's original path in a fresh clone. Files the unit's keep record moved
to the kept set go back to their original paths in the restored instance,
unchanged by hash, before the session starts. The restored unit carries no
mark, and the over command isn't asked about it again until its session has
worked after the restore and stopped again. Serves the restore scenario.

**R33. Past the window, a restore is refused and says why.** A restore of a
unit reaped longer ago than its window is refused, and the refusal says the
window ended and gives the date it ended. The window defaults to Claude Code's
transcript retention. A workspace setting may shorten it, to no less than seven
days. A setting below seven days is a configuration error: a niwa command that
loads the configuration exits non-zero with an error naming the setting and the
seven-day minimum, and runs no pass. Serves the restore scenario.

### Non-functional

**R34. The pass is bounded.** Each pass a niwa command runs for every unit,
before a dispatch or create and in `niwa reap`, finishes within a time budget.
The budget is a fixed bound whose figure the design sets. The over command, its
re-tests of marked units and the loss scans all run inside that budget, and
each over run has its own per-run deadline inside it (R3). A unit the pass
doesn't reach in time is kept for this pass, gets no mark and no answer from
it, and is taken up at a later pass, so no unit waits forever behind others.
The dispatch line, and the output of `niwa reap`, say how many units weren't
reached. The budget caps how many units a pass handles, never what the rule
decides. Serves the three-answers scenario.

**R35. With no over command, nothing goes unless a person retired it.** On a
workspace with no over command, the pass removes no instance, transcript,
dispatch brief or job directory unless a person retired that unit, by the
retire command or by deleting its session (R4). The only removal nobody asks
for is the trim of idle build trees (R30), which removes no unique work. Serves
the no-command scenario.

That doesn't make the default no stricter than today everywhere. Reap becomes
stricter on purpose: it keeps an instance holding unique work that it removes
today (R15). A retire is no stricter than today's unforced destroy about job
scratch: an unforced destroy removes a session and its job directory without
checking the job's scratch, and where a unit has no handoff brief its scratch
is disposable by default (R28), so a user of niwa alone gets a retire that
removes a clean finished unit. A workspace that wants scratch checked sets it
unique. The project owner chose this default; Decisions and Trade-offs records
why.

**R36. The listing stays local.** `niwa list` reads the answer, time stopped
and size from local state. It makes no network call and doesn't run the over
command. Serves the three-answers and no-command scenarios.

## Acceptance Criteria

Each scenario starts from a test workspace on a test host the reviewer can
throw away. Dispatching "a trivial task" means a `niwa dispatch` whose task
writes no file and opens no pull request; the reviewer waits until its session
has stopped. Where a step needs the over command, the reviewer uses a sample
shell script that reads the instance path niwa passes it, appends one line per
call to a log file outside the workspace, with a timestamp as each call starts
and ends, and exits with a code chosen per instance. Where a step names a
setting, record or command whose name the design picks, the reviewer uses the
design's name. Where a step needs a figure the design sets (the time budget,
the per-run deadline, the idle grace's default, an undo interval between a mark
and its reap), the step uses the design's figure.

Some setups write a record whose form the earlier stage hasn't settled: a keep
record, a handoff brief, a discard record or a retire mark. Each such setup
states only what the record says (the unit, the files and their hashes, the
dates, the owner and reason), so the reviewer can write it in whatever form
that work settles and the design documents.

A unit is made "marked and clean" by giving its clone an untracked file
`hold.txt`, retiring the unit (the retire writes the mark and keeps the unit
for `hold.txt`), and then deleting `hold.txt` by hand. Checks that use this
recipe assume that deleting an untracked file doesn't void a mark; if the
earlier stage's rule on voiding settles otherwise, the recipe follows it.

### The three-answers scenario

Setup: on a fresh workspace with no over setting in its configuration, dispatch
three trivial tasks and wait until all three sessions have stopped. Run one
`niwa dispatch` of a trivial task with the setting still absent. Then configure
the sample over command to exit 0 for the first instance, 3 for the third, and
1 for the second and every other instance, printing a one-line reason before it
exits. Record `niwa list`
and `claude agents --json --all`.

Action: run one `niwa dispatch` of another trivial task. If the design sets an
undo interval for a unit that one pass both marks and reaps, run further
trivial dispatches until that interval has passed; "the reaping dispatch" below
is the dispatch at which it ends, and is the first dispatch when the design
sets none. Then run `niwa list` and `niwa list --json`, run one more `niwa
dispatch`, and read the sample command's call log.

- [ ] The dispatch run before the command was configured wrote no mark in any
      instance and removed none of them. (R1)
- [ ] After the reaping dispatch, the first instance's directory is gone and
      its session no longer appears in `claude agents --json --all`; its
      transcript is still on disk. (R3, R6, R17)
- [ ] The second instance is listed as not over and the third as unknown, each
      with how long it has been stopped, its size, and the reason the sample
      command printed. (R3, R22)
- [ ] `niwa list --json` carries the same answer, time stopped and size for
      both. (R22)
- [ ] The call log shows one call per stopped unit on the first configured
      dispatch. Once the first instance is marked, no later pass calls the
      command for it, whether or not it has been reaped yet; no pass calls it
      for the new worker while that worker is still working. (R2)
- [ ] The unit of one more trivial dispatch, once stopped, given an untracked
      file and retired before any other niwa command runs, so that it is marked
      and kept, gets no call at the next dispatch. (R2)
- [ ] With the sample command changed to sleep past its configured timeout for
      the second instance, the next dispatch lists that instance as unknown, no
      mark exists in it, and no process of the sample command is left running.
      (R3)
- [ ] With the command changed to exit 0 for every instance, while a keep
      record holding the second instance exists, the call log shows no call for
      the second instance and it stays listed as kept. (R2, R25)
- [ ] Repeating the setup through configuring the command, then running `niwa
      reap` in place of the first configured dispatch, its elapsed time
      measured with `time` is no more than the design's time budget plus the
      elapsed time of `niwa reap`, measured the same way, on a workspace with no
      stopped units. (R34)
- [ ] The over seam's documentation names the key an over command reads for a
      successor's takeover having landed. (R5)

### The no-command scenario

Setup: on a workspace with no over command configured, dispatch two trivial
tasks and wait until both sessions have stopped. In the first unit's clone,
create an untracked file `notes.txt`. Record a listing of the workspace root,
the two instances and their job directories.

Action: run `niwa dispatch` of another trivial task. Retire the first unit,
then retire the second. Never pass `--force`, or the person-only override flag,
to any command.

- [ ] The dispatch prints one line counting two stopped units nobody has
      retired and their total size. (R23)
- [ ] The dispatch removes neither unit, nor any transcript or job directory,
      and the before and after listings differ only by the new worker. (R1,
      R35)
- [ ] The retire of the first unit reports it kept, names the untracked-file
      check and `notes.txt` as the path that keeps it, and leaves the instance,
      its session and its job directory in place. (R8, R11)
- [ ] Between the two retires, `niwa list` shows the first unit as marked and
      kept, with the untracked-file check and `notes.txt`, and the second as
      stopped with the answer "no command configured", its time stopped and its
      size. `niwa list --json` carries the same fields for both. (R22)
- [ ] A `niwa dispatch` run between the two retires prints a line whose count
      and size include the second unit and leave out the first, which is
      marked and kept. (R23)
- [ ] The retire of the second unit reports it removed. Its instance and its
      session are gone, and its transcript stays. (R8, R10, R16)
- [ ] With `notes.txt` deleted, the next dispatch removes the first unit with no
      second retire, since its mark is still in place. This assumes deleting an
      untracked file doesn't void a mark; if the earlier stage's rule on voiding
      settles otherwise, this check follows it. (R7)
- [ ] Repeating the setup, then removing the first unit's mark by hand after the
      retire and before the next dispatch, leaves the unit listed and unmarked
      after that dispatch. (R7)
- [ ] A mark file holding text that doesn't parse, placed by hand in a clean
      stopped unit, leaves the unit unmarked in the listing and unremoved. (R7)
- [ ] A mark copied by hand from one unit into another clean stopped unit, so
      that it names the first unit's instance, leaves the second unit unmarked
      in the listing and unremoved by the next dispatch. (R7)
- [ ] A retire of a unit whose session is working is refused, and the output
      says the session is working. (R8)
- [ ] With `notes.txt` matched by a pattern added to the workspace's disposable
      list, the retire reports the unit removed. Repeating the setup with a
      discard record that says it is for the first unit and names `notes.txt`,
      and no pattern, the retire also reports it removed. (R13)
- [ ] With a `[session.before_remove]` command that exits 1 and prints
      `held by test`, the retire of a clean stopped unit reports it kept by a
      disagreement, naming the veto and `held by test`; `niwa list` shows the
      same, and the unit stays marked. (R16, R20)
- [ ] In a third, clean stopped unit that holds a niwa worktree inside its
      instance, a retire reports it removed, and its session is gone from
      `claude agents --json --all`. The same holds for `niwa destroy` on such
      a unit. (R17)

### The keep-record scenario

Setup: dispatch a task that writes `bench/results.csv` and two other untracked
files into its clone, and wait until it has stopped. Record the SHA-256 of
`bench/results.csv`. Write a keep record that says: it is for this unit; it
doesn't hold the unit; it names one file, `bench/results.csv`, with that
SHA-256; its owner and reason; and a date one day ahead.

Action: delete the two other untracked files, then retire the unit (its pass
runs at once). List the kept set. Then, once the record's date has arrived (on
a test host, by setting the date or the clock forward), run `niwa dispatch`,
run another dispatch the same day, and run a third one at least a day later.

- [ ] After the retire, the instance is gone and the kept set holds
      `bench/results.csv` and the keep record, and no other file from the
      instance. (R26)
- [ ] The file in the kept set has the SHA-256 recorded at setup. (R26)
- [ ] The kept set sits under `~/.niwa/`, in a directory keyed by the
      workspace root's canonical path, and nothing for it sits under the
      workspace root's `.niwa/` or inside an instance. (R26)
- [ ] Repeating the setup with a separate volume mounted at the kept set's
      directory (a disk image on macOS, a tmpfs on Linux; `df` on the kept set
      and on the instance shows two different filesystems), the retire leaves
      the instance gone and `bench/results.csv` in the kept set with the
      SHA-256 recorded at setup. With that volume remounted read-only before
      the retire, the retire reports the unit kept, `bench/results.csv` is
      still in the instance with the recorded SHA-256, and the kept set gains
      no file. (R14, R26)
- [ ] On the first dispatch after the date, the entry is shown as due in `niwa
      list` and in the dispatch line, and it's still on disk. (R23, R27)
- [ ] After the second dispatch the same day it's still on disk; after the
      third, at least a day after it was first shown, it's gone. (R27)
- [ ] Repeating the setup but changing `bench/results.csv` after the record was
      written, so its hash no longer matches, keeps the unit, and the reason
      names that file. (R26)
- [ ] A clean stopped unit with no keep record, once reaped, leaves no entry in
      the kept set. (R26)
- [ ] Two clean dispatched units, one with a keep record that says it holds
      that unit, with an owner and a reason: after both sessions are deleted by
      hand and `niwa reap` runs, the unheld unit is gone and the held unit
      remains, listed as kept with its reason and owner. After the hold is
      removed, the next `niwa reap` removes it. (R20, R25)
- [ ] A holding record whose review date has passed still keeps the unit, and
      the listing shows it as due for review. (R25)
- [ ] After `niwa apply` in a held unit's instance, the keep record is still
      there and still holds. (R25)
- [ ] A stopped unit whose only untracked file is `bench/results.csv`, with a
      keep record that says it holds the unit and names that file with its
      SHA-256: run the resume command `niwa list`
      shows for it, send the session one prompt, and wait until it has stopped
      again. `niwa list` no longer shows the unit as held. A retire then reports
      it removed, and the kept set holds `bench/results.csv` with the recorded
      SHA-256. (R25, R26)

### The newer-scratch scenario

Setup: dispatch a task whose job scratch ends up holding `plan.md` and
`draft.md`, and let its session stop. Record the SHA-256 of `plan.md`. Write a
handoff brief for the unit that says: it is for this unit; the time it was
written; and a file list naming `plan.md` by path, without holding its bytes.
After the brief was written, change `draft.md`.

Action: retire the unit and read its output. Then repeat the setup without
changing `draft.md` and retire again.

- [ ] With `draft.md` changed, the retire reports the unit kept, and the reason
      names `draft.md` as newer than the brief. (R28)
- [ ] Without the change, the retire reports the unit removed, and the kept set
      holds `plan.md` with the SHA-256 recorded at setup. (R11, R26, R28)
- [ ] A third scratch file copied in after the brief with its modification time
      set earlier than the brief (for example with `touch -t`) also keeps the
      unit and is named, since its inode change time is later. (R28)
- [ ] Repeating the setup without changing `draft.md`, but with a brief that
      holds `plan.md`'s bytes rather than naming it only by path, the retire
      reports the unit removed, the kept set gains no copy of `plan.md`, and
      the bytes the brief holds have the SHA-256 recorded at setup. (R28)
      This check waits on where a brief stores the bytes it holds (open
      dependencies): it applies once the brief work defines that location,
      and until then the retire reports the unit kept with `plan.md` listed
      as "brief copy not verified".
- [ ] Repeating the setup with no handoff brief, the retire reports the unit
      removed, and the kept set gains nothing. (R11, R28)
- [ ] Repeating that with the workspace setting that makes job scratch unique,
      the retire reports the unit kept and names `plan.md` and `draft.md`.
      (R28)
- [ ] With that setting and the brief, each check above gives the same result
      as without it. (R28)

### The verdict-only scenario

Setup: on a workspace with an over command configured, make four stopped units.
The first is marked and clean, by the recipe in the preamble. The second gets a
commit on a new branch that is never pushed, and is then retired, which marks
it and keeps it for that branch. The third is unmarked and clean, and the
sample command answers yes for it. The fourth is answered not over and holds a
cargo `target/` directory with `CACHEDIR.TAG`, whose files are set back past
the idle grace with `touch -t`. Add a git-ignored `.env` and a `.DS_Store` to
the first unit's clone, and a file `scratch.md` to the first unit's job
scratch, with no handoff brief. Record a listing of every file in every instance and
job directory, with size, modification time and SHA-256.

Action: run the verdict-only mode, then record the same listing again. Then
run one `niwa dispatch`. Each check below that changes the setup repeats it
from the start.

- [ ] The output gives reap for the first unit; keep for the second, naming the
      unpushed-commits check and the branch; for the third, the yes answer, the
      mark it would write, and the verdict the pass would reach with that mark;
      and keep and trim for the fourth. (R29)
- [ ] The third unit's verdict matches what the dispatch run after the second
      listing does to it: reap when that dispatch removes the unit, keep when it
      leaves it in place, and a keep names the undo interval as the reason. The
      design's undo interval decides which happens; the check is that the two
      agree. (R10, R29)
- [ ] For each unit the output gives the class of every path: tracked, pushed,
      build tree, generated, disposable or unique. The `.env` is listed as
      disposable and marked as git-ignored, `.DS_Store` as disposable with
      no such mark, and `scratch.md` as disposable and marked as job scratch.
      (R11, R12, R13, R28, R29)
- [ ] A unit given both an untracked file and an unpushed commit, then
      verdict-only run for it, lists the unpushed-commits check before the
      untracked-file check, each with its path. (R11, R29)
- [ ] The two listings are identical, and before the dispatch the third unit
      carries no mark. (R29)
- [ ] With the workspace setting that makes ignored files unique, the first unit's
      verdict becomes keep, naming `.env`. (R12)
- [ ] With the workspace setting that makes job scratch unique, the first
      unit's verdict becomes keep, naming `scratch.md`. (R28, R29)
- [ ] With the first unit's remote made unreachable, its verdict becomes keep
      and the output carries the git error as evidence. (R14)

### The idle-build-tree scenario

Setup: dispatch a task that builds a cargo project in its clone, leaving a
git-ignored `target/` directory with `CACHEDIR.TAG`, and let its session stop.
Configure the over command to answer not over for that instance. Make the tree
older than the idle grace, by waiting or by setting its files' times back with
`touch -t`. Leave a git-ignored directory with no build-tree marker beside it.

Action: record the session's id. Run `niwa create`. Then run the resume
command `niwa list` shows for the unit, send the session one prompt, and wait
until it has stopped. Each check below that changes the setup repeats it from
the start.

- [ ] After `niwa create`, `target/` is gone, and the instance, its checkout,
      the ignored directory with no marker, and the session are all still
      there. (R21, R30)
- [ ] After the resume, `claude agents --json --all` lists a session with the
      id recorded before the trim and its working directory at the instance's
      path, and its transcript holds both the messages from before the trim and
      the new prompt. (R30)
- [ ] The trim happened with no trim setting in the configuration. (R31)
- [ ] With the trim turned off by the workspace setting, the same steps leave
      `target/` in place. (R31)
- [ ] With the idle grace set longer than the tree's age, `target/` stays; with
      it set shorter than the tree's age, the same tree is trimmed. (R31)
- [ ] With no grace setting, a tree set back past the design's default grace is
      trimmed, and one set back to an hour short of it stays. (R31)
- [ ] A tree whose newest write is younger than the idle grace stays. (R30)
- [ ] A `sleep 600` started from a shell whose working directory is
      `target/debug` keeps the tree for as long as it runs. (R30)
- [ ] A process started from a shell outside the instance that holds a file
      under `target/` open (for example `tail -f` on one of its files) keeps the
      tree for as long as it runs; after it ends, the next pass trims the tree.
      (R30)
- [ ] With a root `niwa apply` started and then held mid-run (for example with
      `kill -STOP` on its process), a `niwa reap` is started from another
      shell. `target/` stays in place for as long as the apply is held, whether
      or not that reap has returned. Once the apply is resumed and has
      finished, the next pass trims it. (R30)
- [ ] A keep record that says it is for this unit and protects its build trees
      keeps `target/` through the pass. (R25, R30)
- [ ] With a `[session.before_remove]` command that exits 1, `target/` stays.
      (R20, R30)
- [ ] No niwa command offers trimming as a subcommand of its own. (R30)

### The restore scenario

Setup: on a workspace with no over command configured and the restore window
set to seven days, dispatch two trivial tasks and let both stop. Create an
untracked file `keep.txt` in the first unit's clone and record its SHA-256.
Write a keep record that says: it is for the first unit; it doesn't hold the
unit; it names `keep.txt` with that SHA-256; an owner and reason. Retire the
first unit, so that it is reaped, then retire the second, which is clean, so
that it is reaped too. Then configure the sample over command to exit 1 for
every instance.

Action: right away, with both units inside their window, run `niwa resume` on
the first unit, and run one `niwa dispatch` of a trivial task before sending
the restored session anything. Send the restored session one prompt, wait until
it has stopped, run another `niwa dispatch`, and read the sample command's call
log. Only then move the test host's clock forward eight days, and run `niwa
resume` on the second unit.

- [ ] For the first unit, `niwa resume` gives a session that `claude agents
      --json --all` lists with its working directory at the instance's original
      path, in a fresh clone of the instance's repositories, and that answers a
      prompt sent to it. (R32)
- [ ] `keep.txt` is back at its original path in the restored instance, with
      the SHA-256 recorded at setup, before the session starts. (R32)
- [ ] The restored unit carries no mark. The call log shows no call for it at
      the dispatch run before the prompt, and one call for it at the dispatch
      run after its session worked and stopped again. (R7, R32)
- [ ] For the second unit, `niwa resume` exits non-zero and says the window
      ended, giving the date it ended, which is seven days after the reap. No
      instance is created at the unit's path. (R33)
- [ ] Repeating the setup with no restore-window setting, and moving the clock
      forward to one day short of the transcript retention Claude Code is
      configured with on the test host (its `cleanupPeriodDays` setting) in
      place of eight days, `niwa resume` on the second unit gives a working
      session. Repeating it with the clock moved to one day past that
      retention, the resume is refused, and the date it gives is the retention
      period after the reap. (R33)
- [ ] With the restore window set to three days in the workspace
      configuration, the next `niwa apply` exits non-zero with an error naming
      the restore-window setting and the seven-day minimum, and no instance or
      kept-set entry changes. (R33)
- [ ] Start a Claude Code background session at the workspace root, so the
      SessionStart hook provisions its instance and writes its launch record,
      and let it stop. A retire of that unit, which is clean, reaps it, and
      `niwa resume` inside its window brings it back with its session
      started. Repeat with a second such unit whose launch record is then
      removed by hand: a retire of it reports it kept with the reason that it
      has no launch record, and the next dispatch keeps it and lists it with
      that reason. (R18)

### Cross-cutting checks

- [ ] **Reap's loss scan.** Dispatch a trivial task, commit on a new branch in
      its clone without pushing, delete its session by hand and run `niwa
      dispatch`. The instance is still there, listed with the branch as the
      reason. The same holds for an unmapped dispatch-named instance the
      backstop finds, and when the remote is unreachable during the scan.
      (R14, R15, R20)
- [ ] **Failed-dispatch cleanup.** With a wrapper for `claude` on `PATH` that
      writes an untracked file `partial.txt` into its working directory and
      then exits non-zero, run `niwa dispatch` of a trivial task. The dispatch
      fails, its instance is still there, and `niwa list` shows it kept with
      `partial.txt` as the path. With the wrapper changed to exit non-zero
      without writing anything, a failed dispatch leaves no instance behind.
      (R15)
- [ ] **No force.** With a wrapper for `claude` on `PATH` that records its
      arguments, run the no-command and keep-record scenarios. No recorded call
      carries a flag that discards unpushed work or force-removes a worktree,
      and niwa's own removal is never run with `--force`. When niwa's removal is
      made to refuse a marked, clean unit by a `[session.before_remove]` command
      that exits 1, the unit stays marked, `niwa list` shows it kept by a
      disagreement naming the veto and its message, and the veto's own call log
      shows it called again for that unit at the next pass. (R16, R20)
- [ ] **The six kinds.** Configure the sample command to answer not over, with
      a reason, for three stopped units and unknown, with a reason, for three
      more, standing for the six kinds R24 names. After passes on two different
      days, all six are still listed with their reason, size and time stopped.
      A retire with the person-only flag removes a clean one; without it, the
      retire of a not-over one is refused and says why. With the flag, the
      retire of a not-over unit whose clone holds an untracked `notes.txt`
      reports it kept and names `notes.txt`. (R9, R24)
- [ ] **Path classes.** On a workspace with no over command, give each of
      these its own stopped unit and retire it. Each of the following reports
      the unit kept and names the check and the path or process: a stash made
      with `git stash push -u`; a tag created locally and never pushed; a file
      at the instance root outside every repository; a directory in the clone
      made unreadable with `chmod 000`, with the error as evidence; a `sleep
      600` started from a shell whose working directory is inside the instance.
      Each of the following reports the unit removed: a clean worktree whose
      branch is pushed; a local branch whose commits differ from the remote's
      but whose content equals the merged default branch, as after a squash
      merge; an empty directory. A unit whose session is working when `niwa
      reap` runs gets no call in the log, no mark, and nothing removed. (R2,
      R11, R14)
- [ ] **Nested repositories.** On a workspace with no over command, give each
      of these its own stopped unit and retire it. A repository made with `git
      init` at `tools/nested` inside the clone, holding one commit and no
      remote, with `tools/nested/` listed in the clone's `.git/info/exclude`
      so the outer repository ignores it: the retire reports the unit kept,
      naming the unpushed-commits check and `tools/nested`, not a git-ignored
      disposable path. A clean, fully pushed clone of the workspace repository's
      remote made at `extra/other` under the instance root, outside every
      repository niwa provisioned: the retire reports the unit removed, not
      kept for loose files. (R11)
- [ ] **No second test.** With no over command and wrappers for `koto` and `gh`
      on `PATH` that record calls, run a dispatch on a workspace with a unit
      whose pull request merged and whose koto run is terminal. The unit stays
      unmarked and listed, and neither wrapper records a call from niwa. (R4)
- [ ] **No daemon.** After any pass, no niwa process remains running, and no
      launchd, systemd or cron entry was added. A host left a day with no niwa
      command run shows no change in any instance. (R21)
- [ ] **Overlapping passes.** Three dispatches started at once from separate
      shells on a workspace with one marked, clean unit remove it once; the
      other passes report it gone or skip it, and none fails. (R19)
- [ ] **A held lock.** With a `[session.before_remove]` command that logs each
      call and sleeps 30 seconds, start `niwa reap` on a workspace with one
      marked, clean unit, and while the veto sleeps run `niwa reap` from a
      second shell. The second reap exits zero without removing the unit, and
      the veto's log shows one call for it; when the first reap ends, the unit
      is gone. (R19)
- [ ] **A change during the pass.** With a `[session.before_remove]` command
      that writes an untracked file `late.txt` into a marked, clean unit's
      clone and then exits 0, the pass keeps the unit, and the reason says it
      changed during the pass and names `late.txt`. (R19)
- [ ] **One rule.** The verdict-only mode, the retire, `niwa reap` and the pass
      before dispatch give the same verdict for the same unit, on one unit of
      each verdict, and the verdict-only mode's machine-readable output for
      that one unit carries the same verdict and the same first keeping check.
      (R10, R29)
- [ ] **Time budget.** Take the time budget B and per-run deadline D the design
      sets. Configure the sample command to sleep 0.9 D and then exit 1, and
      make more stopped units than B divided by 0.9 D, plus five. Run `niwa
      reap` timed with `time`. Its elapsed time is no more than B plus the
      elapsed time of `niwa reap` on a workspace with no stopped units. The
      count of units not reached that its output gives equals the number of
      stopped units with no call in that pass's log, and is at least one; none
      of those units carries a mark or an answer from that pass. After as many
      further `niwa reap` runs as there are stopped units, every unit has at
      least one call in the log. Repeating the setup with `niwa dispatch` of a
      trivial task in place of the first `niwa reap`, the dispatch line's count
      of units not reached equals the number of stopped units with no call in
      that pass's log, and is at least one. (R23, R34)
- [ ] **Local listing.** With the network off, `niwa list` shows every field of
      R22, and the sample command's call log gains no line. (R36)

## Out of Scope

- **Shipping an over command.** niwa defines the seam and never infers over. A
  workflow plugin such as shirabe ships its own command later. A workspace
  that configures that command for shirabe's workflows should set job scratch
  unique (R28) in the same configuration.
- **Expiring transcripts.** Removing a reaped unit's transcript, launch record
  and dispatch brief when its window ends is later work. This feature leaves
  transcripts where Claude Code keeps them; a restore past the window is
  refused even when the transcript is still on disk.
- **A setting that retires a unit after it has sat idle for some time.** It
  belongs to a later milestone, which depends on reap's loss scan landing here
  first. The project owner chose its shape: it ships off, and a workspace that
  turns it on gets a mark on any unit stopped longer than the setting that no
  over command answers, counted from its last stop or restore, and never on a
  unit answered not over. Left off, it changes niwa's recorded delete-only
  decision for nobody.
- **The earlier remover's interim scratch copy.** The copy that remover keeps
  for units that stopped without a handoff brief stays with the workspace that
  built it and isn't carried into niwa. In niwa a unit with no handoff brief
  lets its job scratch go by default; a workspace that wants it kept sets job
  scratch unique (R28).
- **Writing the handoff brief.** This feature reads a brief's file list and
  time; the brief itself comes from separate work.
- **Lifting a workspace hook's denial of `niwa reap`.** A workspace that blocks
  `niwa reap` through its own hooks decides for itself, under its own
  authority, when to lift that once reap applies the removal rule.
- **A daemon or timer.** The pass runs only inside niwa commands (R21).
- **Changing what `niwa destroy` counts as loss.** A person's destroy keeps
  its own checks. It gains only the worktree order of R17. The retire command
  is the path through the removal rule.

## Open dependencies on the earlier stage

Each point below comes from the earlier workspace-level remover, built first in
one workspace, which hasn't landed. The position given is where that work
stands now. Each one stays open until it lands, and if it changes before then,
this work follows it rather than settling the point on its own.

- **The over command's exit and output contract.** Today it exits 0 for over,
  1 for not over with the reason on the last output line, 2 for a usage error,
  3 when a fact couldn't be read, and 124 on its deadline, and everything but 0
  and 1 keeps. It takes the instance path as an argument or in
  `NIWA_REMOVE_PATH`, the variable `[session.before_remove]` already reads, and
  its default deadline is 20 seconds. It has two entry points: one that only
  checks and writes nothing, and one that also writes the mark itself. Since
  niwa writes the mark here (R3, R6), niwa needs the check-only form, and the
  two forms' roles have to be settled.
- **The retire mark's schema and where it sits.** Today the mark is a JSON
  file in the instance's own `.niwa/` directory, with a schema name namespaced
  to the earlier remover rather than to niwa. It records the instance's name
  and creation time, when it was marked, what marked it, and a basis (the
  sessions, runs, branches and pull requests it looked at), and that remover
  treats a mark with no basis as no mark. It also checks each mark against a
  copy kept in its own state outside the instance, which niwa can't read, so a
  reader that checks only the file accepts marks the earlier remover would
  refuse.
- **Whether later work voids a mark.** Today, yes: session activity after the
  mark's time, or a branch head that moved, voids it. The reader leaves a voided
  file in place, and the next yes overwrites it.
- **The keep record's form and the kept set's layout.** Today a keep record is
  written by hand into the earlier remover's own state outside the instance,
  with the unit, owner, reason, written and review dates, and files by path and
  SHA-256. A record with no files holds the unit, and still holds past its
  review date while it's listed as due; a record naming files keeps only those.
  Its kept set is one directory per unit and move, with the files at their
  in-unit paths and a manifest, and it never expires: only a person removes it.
  That last point differs from R27, and the two have to be reconciled.
- **The pattern list's format.** Today it's one entry per line, the pattern
  then a tab then the review that added it, and a line naming no review is a
  parse error that keeps every unit. The pattern syntax itself isn't specified.
- **The launch record.** Today the earlier remover writes its own launch record
  per unit, because it can't change niwa: it names the session, job directory,
  transcript, repositories, branches, a copy of the last mark, and a handoff
  brief field that is always empty so far. niwa's own launch record names the
  session and instance but no branches. Which record niwa relies on for R18 and
  R32, and where it finds a unit's handoff brief, are open.
- **Who writes the pid record.** Today nobody: the earlier remover leaves it to
  later work. Until something writes pid records for the background processes a
  unit starts, any such process keeps its unit.
- **The discard record command.** Today the unit's owner writes a discard
  record by hand, naming paths in that unit and optionally a patch file. No
  command writes one.
- **The undo interval when one pass both marks and reaps.** Today there's
  none: a unit seen at an earlier pass can be marked and reaped in the same
  pass, and only a unit seen for the first time waits one pass. After that the
  only undo is a restore.

One more key isn't in the earlier remover at all: the key an over command reads
to learn that a successor's takeover has landed. That remover reads no such
key. R5 requires the design to name it.

One point is open on the separate work that writes the handoff brief rather
than on the earlier remover: where a brief stores the bytes of a file it
holds. Today that work defines no such location, so niwa has no copy it can
check a brief's claim against (R28). The brief work settles it by defining
that location; until it does, a file a brief says it holds counts as unique
and keeps its unit.

## Related work

- **niwa#300**, reap's missing loss scan. R15 closes it: every reap scans
  before it removes, in the mapped sweep and in the backstop, and a scan error
  keeps.
- **niwa#458**, how an operator keeps one chosen instance from reap. The
  holding form of the keep record (R25) is the first-class marker it asks for,
  shown in `niwa list` as kept.
- **niwa#459**, a functional test of reap keeping a marked instance and
  reclaiming an unmarked one. The keep-record scenario's check with two
  dispatched units, one held, is that test.
- **Worktree cleanup.** A policy that deletes worktrees after a fixed time,
  whatever they hold, can't live beside the removal rule: the rule keeps a
  worktree that holds unique work for as long as it holds it, and a time-based
  deletion removes exactly that work. Worktrees fall under R11 like every other
  path.
- **Destroy leaving sessions behind.** Today `niwa destroy` removes the session
  before the directory, so a session whose worktree sits inside the instance
  survives the destroy. R17 closes that for the pass and for destroy.
- `docs/guides/ephemeral-session-instances.md` and
  `docs/designs/current/DESIGN-ephemeral-session-instances.md` record why reap
  keys on the session being deleted. That path stays (R20).
- `docs/designs/DESIGN-instances-outlive-sessions.md` describes the removal
  gate and the launch records `niwa resume` replays, which R18, R20 and R32
  build on.
- `docs/designs/current/DESIGN-plugin-release-hold.md` states the rule for host-level
  state the kept set follows (R26). It also assumes that entries under the
  workspace's host-level `kept/` directory are whole instances whose plugin
  links count; a kept set holding only named files has to be reconciled with
  that assumption when the design picks the kept set's path.

## Known Limitations

- A restore doesn't bring back what the rule let go: the worktree, git-ignored
  files outside build trees, the build tree, loose files that were declared
  disposable, job scratch let go because the unit had no handoff brief, and
  the job entry. It's rebuilt from the transcript in a fresh
  clone.
- A file a worker wrote by hand into a build tree goes with the trim. That's
  an accepted loss of treating build trees as regenerable.
- A host where no niwa command runs makes no pass, so its finished units stay
  until someone runs one.
- The kept set is per user and per host. Whether niwa's own removal of a
  workspace removes that workspace's kept set is the design's to say.
- A unit with no launch record is never reaped on a mark alone (R18). Units
  the SessionStart hook provisions get a launch record from this feature, as
  dispatched ones already do, so this covers a unit whose record is gone, such
  as one removed by hand.
- Until something writes pid records, a background watch or keep-alive a unit
  started keeps that unit.
- A review instance `niwa watch` provisions stays kept and listed until a
  person destroys it; its removal through the rule is tsukumogami/niwa#551.
- A watch stage that fails after its fetch leaves that PR head handled with
  no review until the head moves; tsukumogami/niwa#551 restores the retry
  for a stage that fails before the review agent launches.
- The listing measures disk use by walking each stopped unit, which costs time
  on large build trees. R36 keeps it local, but its speed on a host with many
  units is unmeasured.

## Decisions and Trade-offs

**niwa reads an exit status, and nothing else decides over.** The alternative
was for niwa to infer over from koto and the forge. That would tie niwa to
both and put workflow knowledge inside niwa, which the seam exists to avoid. A
workspace with no command gets no marking at all unless it turns on the later
idle-time setting, which ships off, so the safe default holds.

**A marked unit is tested again every pass, and an unmarked one is never
reaped on its own.** A mark is a statement that the work is over, not
permission to lose anything. Keeping the two apart lets a unit stay marked
while it waits for a push or a discard, and lets a person undo a retire before
the reap.

**Ignored files outside build trees are disposable by default.** niwa's loss
scan already treats them that way. Counting them unique by default would block
every unit holding a `.env` or `.venv` and push people toward force, which this
feature removes from the pass. A workspace that wants them unique sets it.

**The trim is on by default.** It removes only what a build can produce again,
never a unit, and it's the one thing a user of niwa alone gets with no
configuration. The setting that turns it off keeps the choice with the
workspace.

**Units the command doesn't call over have no expiry by default.** An
age-based removal would take exactly the units whose state is unclear. They
stay listed with a reason until a person retires them, and the dispatch line
keeps them in view. The later idle-time setting is off for that reason: a
workspace that turns it on accepts that a unit no command answers is marked
once it has been stopped longer than the setting, and even then a unit
answered not over is never marked, and the removal rule still decides what
goes.

**The PRD states the strictness tension instead of claiming the default is no
stricter than today.** Reap gets stricter on purpose. A retire's treatment of
job scratch was stricter than an unforced destroy until the project owner
chose the default below, which brings it level with destroy where no handoff
brief exists.

**Job scratch in a unit with no handoff brief is disposable by default, and
the idle-time setting ships off.** The project owner chose this, as three
parts shipped together. Before the choice, R11 counted every regular file in
job scratch as unique, and a user of niwa alone has no handoff brief, so a
retire would mostly have answered kept, which is stricter than today's
unforced destroy (R35) and would push that user back to deleting the session,
the route that checks nothing.

1. Where a unit has no handoff brief, its job-scratch files are disposable by
   default, and a workspace setting, shaped like the ignored-files setting
   (R12), makes them unique. A workspace that configures an over command for
   shirabe's workflows should set job scratch unique in the same
   configuration, so a worker in those workflows that has no brief is still
   protected. Where a brief exists, R28 applies whatever the setting says: a
   file the brief names is handed forward, a file older than the brief is
   answered for, and a file newer than the brief keeps the unit.
2. The idle-time setting ships off. A workspace that turns it on gets a mark
   on any unit stopped longer than the setting that no over command answers,
   counted from the unit's last stop or restore, and the mark then goes
   through the same removal rule. It never overrides a "not over". Left off,
   it changes niwa's recorded delete-only decision for nobody. The setting
   itself belongs to a later milestone.
3. The verdict-only mode lists job-scratch paths it counts as disposable as a
   notice, with a job-scratch mark, as it marks git-ignored files (R29), so a
   user who wants to see what a retire would let go can.

The cost is that the user least likely to know scratch exists gives up the
scratch check on a retire unless they set it unique; the verdict-only notice
keeps that visible. The alternatives not chosen were leaving job scratch
unique, which keeps a retire stricter than destroy; shipping the idle-time
setting on by default with a long grace, which reverses the delete-only
decision for every user before reap's loss scan and restore have run outside
one workspace; keeping the idle-time setting opt-in with scratch still unique,
which would mostly mark units and then keep them; and letting scratch go only
on a retire by hand, which adds a second scratch rule.

A launch record for sessions the SessionStart hook provisions is still needed
beside this default. Without one, such a unit can't be restored, so R18 keeps
it from a reap on a mark, and that user's only route is deleting the session.

**The design settles these, and the requirements hold whatever it picks.**

| Item | Where it's used |
|------|-----------------|
| Names: the retire mark, the retire command, the over setting, the trim and idle-grace settings, the ignored-files setting, the job-scratch setting, the restore-window setting | Throughout |
| The kept set's path under the rule for host-level state | R26 |
| The dispatch line's wording | R23 |
| The pass's time budget figure, beside today's 30-second cap per pass on the removal gate's command | R34 |
| The over command's default per-run deadline | R3 |
| The idle grace's default | R31 |
| The full list of build-tree markers, and how a repository declares build output | Terms |
| The full shipped list of disposable patterns | R13 |
| How niwa finds a process that holds a file under a build tree open | R30 |
| The undo interval between a mark and its reap, following the earlier stage's position on it | R29, three-answers scenario |
| How the instance lock and the final re-read are built | R19 |

**A person's destroy keeps its own checks.** Folding destroy into the removal
rule would change a command people already rely on. The retire command gives
the rule's answer for one unit, and the verdict-only mode gives the report
destroy lacks.
