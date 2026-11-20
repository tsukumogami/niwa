---
schema: brief/v1
status: Draft
problem: |
  niwa removes a stopped unit only when its session is deleted or a person runs
  destroy, and never asks whether the unit's work is finished; reap runs no loss
  scan. Finished instances, transcripts, dispatch briefs and build trees pile up,
  and the only safe way to clear them is a person choosing each one.
outcome: |
  A niwa user configures a command that says whether a unit is over, and from then
  on a stopped unit it calls over, holding nothing unique, is removed at the next
  niwa command with nobody acting. With nothing configured, nothing goes unasked:
  every stopped unretired unit is listed with its size, and one command retires it.
motivating_context: |
  niwa keeps a finished session's instance until the session is deleted, by a
  recorded decision made after an earlier version reaped instances the moment a
  worker went idle. That stops the old loss but leaves every finished unit on disk.
  A workspace-level remover, built first in one workspace outside niwa, showed the
  shape of the fix and fixed the over command's contract and the retire mark's
  format. This feature carries that remover into niwa for every workspace.
---

# BRIEF: niwa removes a finished unit that a workspace command says is over

## Status

Draft

The framing depends on a workspace-level remover built first in one workspace,
whose command contract and retire-mark format this feature adopts as written.
That earlier stage isn't finished. If its contract or mark changes before it
lands, this brief follows it rather than defining its own. Names (the mark, the
retire command, the settings) are proposals until the design picks them.

## Problem Statement

In niwa a unit of work is a session, the instance it runs in, its job directory
and its transcript. When the work is done, nothing in niwa knows. niwa removes a
stopped instance on two signals only. `niwa reap`, which also runs before every
`niwa dispatch` and `niwa create`, removes an instance once its session has been
deleted. `niwa destroy` removes one a person names. Neither asks whether the
unit's work is finished, and the workspace has no way to tell niwa what finished
means for it. The `[session.before_remove]` command can veto a removal; it can't
start one.

The two removal paths are also uneven about loss. Reap checks liveness and
nothing else: it never scans for unpushed or uncommitted work (niwa#300), so a
person who deletes a worker's session by hand turns the next dispatch into an
unchecked delete of that worker's instance. Destroy does scan, but it counts
every untracked file as loss, has no mode that reports without acting, and
removes the session before the directory. When the instance still holds a niwa
worktree, Claude Code refuses to drop the session, so a destroy can pass every
check and still leave the session behind.

The result is that finished units stay. A stopped worker whose pull request
merged a week ago still holds its instance, its clone, a multi-gigabyte build
tree, its transcript and the brief it was dispatched with. `niwa list` shows it
with no state, no age and no size, so nobody can tell at a glance which of forty
instances are done or what they cost. The only safe way to clear them is for a
person to look at each one and decide, which on a host running parallel agent
work doesn't happen, and the stock grows until the disk fills.

## User Outcome

A niwa user on any workspace configures one command that says whether a unit is
over. From then on, a stopped unit the command calls over, holding nothing
unique, is removed at the next niwa command with nobody acting. niwa reads only
the command's exit status, so it needs neither koto nor a forge, and it never
decides on its own that a unit is over. A unit that holds anything niwa can't
regenerate, or whose answer niwa can't read, is kept and named with the reason.
A unit removed this way can still be restored, inside a window, from its
transcript.

A user who configures nothing loses nothing they didn't ask to lose. No
instance, transcript or dispatch brief goes without someone asking for it.
Every stopped unit nobody has retired is listed with its size, a line at each
dispatch says how many there are and how much disk they hold, and one command
retires a unit and lets the same test decide whether it goes. Build trees left
idle in units that stay are trimmed, so the disk a kept unit holds shrinks to
what it actually needs.

A person who wants to keep something from a unit that goes writes a keep record
naming it, and finds it later in a kept set on the host, unchanged. A person who
wants to see what niwa would do runs a verdict-only mode and gets every verdict
with nothing touched.

## User Journeys

### A workspace command answers over, not over, and fails

A reviewer sets up a fresh workspace with three stopped instances and configures
a sample command that exits 0 for the first instance, 1 for the second and 3 for
the third. They run one `niwa dispatch`, then `niwa list`. The first instance is
gone. The second is still there, shown as not over, and the third is shown as
unknown; each line says how long the unit has been stopped and how much disk it
holds. This shows that only a yes removes anything and that a failing command
keeps.

### A workspace with no command configured

A reviewer on a workspace with no over command stops two sessions and dispatches
again. The dispatch prints a line counting the stopped units nobody has retired
and their total size. They retire one unit that holds an untracked file,
`notes.txt` in its clone, and see it kept with that path named. They retire a
clean unit and see it removed, without passing `--force` to anything. This shows
that a retire runs the same test as the configured command's yes, and that the
test, not a flag, is what lets a unit go.

### A keep record that names one file

A reviewer writes a keep record for a stopped unit naming one file,
`bench/results.csv`, and retires the unit. After the next pass the instance is
gone, and the host's kept set holds that file, with the same hash it had in the
instance, and nothing else of the instance. When the record's date arrives, the
entry is shown as due in the listing and in the dispatch line, and it's removed
only at a later pass, after it has been shown. This shows that a named file
survives a reap byte for byte and doesn't leave unannounced.

### A scratch file newer than the handoff brief

A reviewer takes a stopped unit whose handoff brief names one file in its job
scratch, `plan.md`. After the brief was written they change a second scratch
file, `draft.md`, then retire the unit. It's kept, and the reason names
`draft.md` as newer than the brief. This shows that a brief lets go only of what
existed when it was written, and that later work keeps the unit.

### A verdict-only run on mixed units

A reviewer on a workspace with a mix of units (one marked and clean, one marked
with an unpushed branch, one unmarked, one with an idle build tree) runs the
verdict-only mode. It prints, for each unit, the verdict it would get and the
class of every path in it: tracked, pushed, build tree, generated, disposable,
unique. A before-and-after listing of the instances and their contents shows
nothing on disk changed. This shows that a person can see the rule's answer for
their own units before trusting it to act.

### An idle build tree in a unit that isn't over

A reviewer leaves a cargo `target/` directory, marked with `CACHEDIR.TAG`, idle
in a stopped instance that the command calls not over. After the idle grace
they run any niwa command that makes the pass, such as `niwa create`. The build
tree is gone; the instance, its checkout and its session are all still there,
and the session resumes as before. This shows that the trim is a verdict of the
same pass and never takes the unit with it.

### Restoring a reaped unit

A reviewer picks a dispatched unit that was reaped inside its restore window and
runs `niwa resume` on it. They get a working session, rooted at the instance's
original path in a fresh clone. They then pick a unit reaped longer ago than its
window and run the same command; niwa refuses and says the window ended, and
when. This shows that a reap is undoable for a known period and that the refusal
past it explains itself.

## Scope Boundary

**In scope:**

- A way for a workspace to tell niwa what "over" means for it, by naming one
  command niwa asks about each stopped unit. A workspace that names none gets
  no automatic marking.
- A durable record that a unit is over, readable by niwa whether niwa or the
  workspace's own earlier remover wrote it.
- One removal rule that every removal niwa makes on its own follows: nothing
  that holds unique work goes, including work inside worktrees, and removal
  happens in the cleanup niwa already does around dispatching and creating
  instances (the gap tracked in niwa#300).
- A view of every stopped unit nobody has retired, with the answer it got, how
  long it has sat and how much space it takes, and a one-line count of those
  units each time a person dispatches.
- A way for a person to retire one unit, so the same rule decides it.
- A way to keep chosen files from a unit that is otherwise finished (the
  question niwa#458 and niwa#459 leave open), held on the host outside any
  instance and shown as due before they go.
- Freeing the space idle build output takes in a stopped unit that isn't over,
  on by default and possible to turn off.
- A dry run that says what would happen to every unit and changes nothing.
- Bringing a removed unit back for a while after it goes, and a plain refusal
  once that window has passed.

**Out of scope:**

- Shipping any particular over command. niwa defines the seam and never infers
  over from koto, a forge or anything else; a workflow plugin such as shirabe
  ships its own command later.
- Expiring transcripts. Removing a reaped unit's transcript, launch record and
  dispatch brief at the end of the window is later work; this feature leaves
  transcripts where Claude Code keeps them.
- A setting that retires a unit after it has sat idle for some time. It depends
  on reap's loss scan landing here first, and it reverses niwa's recorded
  delete-only decision for every user, so it is separate.
- The interim scratch copy the earlier workspace-level remover keeps for units
  that stopped without a handoff brief. That copy stays with the workspace that
  built it and isn't carried into niwa.
- Writing the handoff brief. This feature reads a brief's file list and time; the
  brief itself comes from separate work.
- Settling the default for job scratch when no handoff brief exists. For a user
  of niwa alone, a retire will mostly answer kept, because every regular file in
  job scratch counts as unique and that user has no brief to name or release it.
  That is stricter than today, where an unforced destroy removes the session and
  its job directory unchecked. Whether to leave it, or treat such scratch as
  disposable by default with a setting to make it unique, is the owner's call,
  and the requirements carry it as open.
- Lifting any workspace hook's denial of `niwa reap`. Each workspace decides that
  for itself once reap applies the removal rule.
- A daemon or timer. The pass runs only inside niwa commands, so a host where no
  niwa command runs makes no pass.

A shipped over command will never mark six kinds of unit, and the listing has to
carry them for as long as they sit: a merged pull request whose run is still
live, a run that ended blocked, an abandoned pull request that is still open, a
session replaced by hand, a top session with nothing above it, and a plain chat.
Each stays listed, with its reason, size and time stopped, until a person retires
it.

The requirements and the design settle the rest, none of which changes the
framing: the names of the mark, the retire command and the settings; the kept
set's exact path under the rule for host-level state; the pass's time budget
inside a dispatch and what happens to units it doesn't reach; the dispatch
line's wording; a launch record for sessions the SessionStart hook provisions,
without which those units can't be restored; whether later work in a unit voids
its mark; who writes the record of background processes a unit started; which
command writes a discard record; and which key an over command reads to learn
that a successor's takeover has landed.

## References

- `docs/guides/ephemeral-session-instances.md`: today's delete-only teardown and
  the `[session.before_remove]` gate.
- `docs/designs/current/DESIGN-ephemeral-session-instances.md`: why reap keys on
  the session being deleted.
- `docs/designs/DESIGN-instances-outlive-sessions.md`: the removal gate and
  launch records `niwa resume` replays.
- `docs/designs/current/DESIGN-niwa-destroy.md`: destroy's loss scan.
- `docs/designs/DESIGN-plugin-release-hold.md`: the location rule for host-level
  state the kept set follows.
