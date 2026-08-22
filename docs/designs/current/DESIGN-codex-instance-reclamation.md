---
schema: design/v1
status: Accepted
problem: |
  `niwa dispatch` provisions a fresh niwa instance for every dispatched
  worker, agent-neutrally, and `niwa reap` reclaims the instance when the
  session is finished with. For Claude the rule is record presence: Claude
  Code removes a session's job-state record when the developer deletes the
  session, so record-gone means reclaimable. Codex never removes a
  rollout, so its declared liveness is LivenessNone, and the reaper --
  correctly -- spares every Codex-dispatched instance rather than guess,
  on both the mapped sweep and the unmapped backstop. Safe, but
  permanent: those instances leak forever and only `niwa destroy` clears
  them. Closing that leak is this design's job.
decision: |
  Codex declares a third liveness kind, LivenessRecordActivity, and the
  reaper reads it through the declaration switch it already has. The rule
  is two signals in a fixed order. Rollout mtime staleness past a 24-hour
  grace period (overridable via NIWA_REAP_IDLE_GRACE) is the trigger:
  nobody has worked in this session for long enough. A non-blocking probe
  of Codex's own per-session writer lock is the guard: never reclaim a
  session with a live writer attached. Staleness is tested first with a
  plain stat, and the lock is probed only for a record already past the
  grace period. The same rule reaches the unmapped backstop, so its Codex
  sparing becomes temporary too. The process-cwd check the predecessor
  design named as the eventual fix is not built and is no longer the
  recommended route for this problem.
rationale: |
  Every element rests on measurement against codex-cli 0.147.0. The
  writer lock is a real BSD advisory flock, taken at session bootstrap
  and held unbroken for the whole turn -- 157.8s and 67.3s across two
  runs polled twice a second, with zero free samples -- and the kernel
  releases it when the holder dies, so a stale file cannot read as a live
  session. A clean exit unlinks the lock file, so a leftover file means
  abnormal death, never routine litter. Rollout mtime stops moving for
  good when a session ends and moves again on `codex exec resume`, which
  appends to the same inode. Together those make "stale and unheld" mean
  "nobody came back" -- a question presence alone cannot answer for an
  agent with no delete verb. The lock probe beats a process scan on cost,
  precision, stale-file immunity, and portability; flock is a pattern
  niwa already ships under a unix build tag.
---

# DESIGN: codex instance reclamation

## Status

Accepted

This design owns the reclamation rule for Codex-dispatched instances:
the liveness signal, the guard-and-trigger split, the order of the two
checks, the new declaration kind, the grace period, and the backstop's
adoption of the same rule. It supersedes part of Decision 7 of
DESIGN-codex-background-dispatch.md -- specifically its two "explicitly
not built" paragraphs, which named the mapped-path proxy as the next
feature's work and a process-level cwd check as the route that would
eventually close the backstop. The proxy is designed here, on a
different and better signal; the process check remains unbuilt and is no
longer the recommended route for this problem. Everything else in that
decision stands, including the reasoning that rejected process-based
liveness inference, which this design builds on rather than overturns.

## Context and Problem Statement

`niwa dispatch` provisions a fresh niwa instance for every dispatched
worker, for Claude and Codex alike, and the reclamation sweep runs at
the top of every `create` and `dispatch` to take back the instances
whose sessions are finished with. The sweep has two halves. The mapped
sweep joins instances against their session mappings and applies the
launching agent's declared liveness rule (`selectReapTargets`,
`livenessUnreadable`). The unmapped backstop ages dispatch-named
instances that never got a mapping, sparing any with a live worker
(`selectBackstopTargets`, `instanceHasLiveJob`,
`instanceHasRecordedSession`).

For Claude both halves terminate. Claude Code removes a session's
job-state record when the developer deletes the session, so
`LivenessRecordPresence` holds: record gone, session gone, instance
reclaimable. Codex removes nothing. A rollout is written once and stays
forever, so the predecessor design declared `LivenessNone` -- honestly,
because presence of a never-removed record proves only that a session
once existed -- and the reaper spares every Codex-dispatched instance on
every sweep, saying so on stderr each time (`reportSparedInstances`).

That was the right refusal and it has the wrong steady state. The
sparing is permanent. A developer who dispatches five Codex tasks a day
accrues five directories a day that nothing but `niwa destroy` will ever
remove, each holding cloned repositories, and the sweep's own report
trains them to ignore it because the message never changes and never
resolves. The predecessor design declared this cost, published it in
the guide, and named closing it as the next feature's work. This is
that feature.

The question is what signal can honestly say a Codex session is
finished with, given that the agent will never say so itself. The
answer turned out to be sitting in Codex's own store, and it was found
by measurement rather than by reading documentation, because none
exists.

## Measured Behavior (codex-cli 0.147.0, Linux)

These measurements were taken for this design. Everything below builds
on them, so they're recorded with their numbers rather than summarized.

1. **Codex takes a real BSD advisory lock at session bootstrap.**
   Established by strace: `flock(42</...>/thread-writer-locks/01a02697-
   ....lock>, LOCK_EX|LOCK_NB) = 0`, against
   `$CODEX_HOME/thread-writer-locks/<thread-id>.lock`. The binary's own
   module for this is `thread-store/src/local/writer_lock.rs`. There is
   no `LOCK_UN` anywhere in the trace: the lock is released implicitly
   when the file descriptor closes at process exit.

2. **The lock is held continuously for the whole turn.** Polling
   `flock -n` from another process every 0.5s across two runs showed
   157.8s and 67.3s of unbroken held state with zero free samples in
   between, and release within about 0.3s of the worker exiting. The
   lock isn't taken per write and dropped between them; it's the
   session's writer, attached for the duration.

3. **A SIGKILLed worker leaves the `.lock` file on disk, and the
   kernel releases the lock anyway.** After the kill, `/proc/locks`
   loses the row and `flock -n` on the leftover file succeeds. A dead
   holder cannot read as a live session, which is the property that
   makes this signal trustworthy where file-existence checks are not.

4. **On clean exit the lock file is unlinked.** Five clean exits each
   left only `.coordination.lock` in the directory. So a `.lock` file
   present with no holder is produced only by abnormal death -- it is
   not routine litter.

5. **The lock file gets a fresh inode on every open.** Unlinked at
   exit, re-created by the next writer. A checker must open the path
   fresh on every probe and must never cache a file descriptor or an
   inode: a cached fd points at an orphaned inode the next writer will
   never touch, and probing it answers a question about a file nobody
   uses anymore.

6. **A probe that opens with `O_CREAT` manufactures evidence.** The
   file it creates is indistinguishable from a crash leftover, so the
   next observer sees an abnormal-death artifact that never happened.
   The checker opens without `O_CREAT` and treats ENOENT from the
   open itself as the answer -- no writer. Not a stat first: a
   stat-then-open pair can race the file's own unlink-and-recreate
   cycle, and the create-free open asks the same question in one
   call.

7. **Rollout mtime tracks session activity exactly.** It advances
   while a session is being written to, and stops permanently when the
   session ends: five rollouts re-stat'd seven minutes after their
   sessions finished each showed an mtime equal to its own last JSON
   line's embedded timestamp to the millisecond -- no compaction, no
   state-database writeback, nothing revisits the file. And it advances
   again on `codex exec resume`, which appends to the same rollout
   inode and creates no new file.

8. **`.coordination.lock` is not a liveness signal.** It is a separate
   directory-wide blocking `LOCK_EX`, held only for the instant of a
   registry mutation. It never appeared in `/proc/locks` across a full
   live run, and its mtime never moved. Nothing in this design reads
   it.

### Correction to the standing spike

`docs/spikes/SPIKE-codex-discovery-mechanics.md` finding 18 states:
"Stale `.lock` files sitting in that directory are routine." That is
false, and this design depends on its falsity, so the correction
belongs on the record. Clean exit unlinks the file (measurement 4);
a lingering `.lock` file is produced only by abnormal death. The single
lock file that prompted the spike's claim was measured while this
design was prepared and found to be *currently held* -- by a live
interactive session that had been running for five days. That is not a
stale file; it is the opposite, and it is also positive evidence for
something this design needs: the lock does not decay on long-lived
sessions. A writer attached for five days is still a writer, and the
probe still sees it.

## Decision Drivers

- **Never reclaim a session with a live writer.** The failure this
  whole area exists to prevent is destroying the directory a worker is
  writing in. Every rule below is shaped so that its unsafe direction
  requires two independent signals to be wrong at once.
- **The signal must survive the process being gone.** A finished
  `codex exec` leaves a resumable session with no process at all --
  the predecessor design's Decision 7 established that process absence
  is not session death, and nothing here re-litigates it.
- **Measured behavior is authoritative.** The eight measurements above
  and the spike's findings 17 and 18 constrain the design; where a
  prior document's claim contradicted measurement, the measurement won
  and the document is corrected.
- **The rule reaches the reaper through the declaration.** The reaper
  switches on `SessionRecords`' liveness kind, never on an agent
  constant. A Codex-shaped fix lands as a declaration a third agent
  could also make, or it recreates the hardcoded parallel pass the
  contract exists to prevent.
- **Honest costs, stated.** Where this design's semantics differ from
  Claude's, or a race is narrowed rather than eliminated, the
  difference is written down instead of rounded to zero.

## Considered Options

### Decision 1 -- the liveness signal is the writer lock, not a process scan

Decision 7 of DESIGN-codex-background-dispatch.md named the proper fix
for the backstop's permanent sparing as an agent-neutral check of
whether any live process has a working directory inside the instance,
with `internal/worktree/procinfo_linux.go` as the beginning of the
machinery and portability beyond Linux as the open question that made
it too large for that feature to carry. This design does not build
that check, and not because it shrank. The writer-lock probe is better
on every axis that made the process scan attractive.

**Chosen: probe Codex's own per-session writer lock with a
non-blocking flock attempt against the declared lock path.**

- **Cost.** The probe is O(1) against a known path -- stat, open,
  `flock` with the non-blocking bit, release, close. The process scan
  walks every process on the host and reads each one's working
  directory, for every instance the sweep considers, on every sweep,
  under commands the developer ran for another reason.
- **Precision.** The lock answers "is a writer attached to *this*
  session" -- the exact question the reaper is asking. The process
  scan answers "is anything running in this directory", which catches
  a stray shell the developer left open and misses nothing the lock
  misses. Where the two disagree, the lock is answering the question
  and the scan is approximating it.
- **Stale-file immunity.** The kernel releases the lock when its
  holder dies (measurement 3), so a crashed worker cannot pin an
  instance. A liveness rule built on file existence would need its own
  staleness heuristic for exactly this case; the lock needs none,
  because the signal is the kernel's lock table, not the file.
- **Portability.** The process scan's open question -- what a
  non-Linux host offers instead of `/proc` -- dissolves rather than
  gets answered. `flock` is available on Linux and macOS alike, and
  niwa already ships this exact non-blocking-acquire pattern in
  `internal/workspace/codex_trust_lock_unix.go` under a
  `//go:build unix` tag. The probe is a smaller instance of code the
  repository already carries.

Two of the measurements above are construction rules for the probe,
not background. The lock file gets a fresh inode on every open
(measurement 5), so the checker opens the path anew on every probe and
caches nothing across probes -- no fd, no inode. And the checker
opens without `O_CREAT`, treating ENOENT from the open as "no writer"
(measurement 6): a probe that creates the file manufactures a phantom
lock file indistinguishable from a crash leftover, planting false
evidence in a store niwa does not own -- and the create-free open
beats a stat-then-open pair, which could race the file's own
unlink-and-recreate cycle between the two calls.

**What supersession means here, precisely.** Decision 7's "explicitly
not built" paragraph is superseded: the reclamation gap it deferred is
closed by this design, and the process check it recommended as the
eventual closer is no longer the recommended route for this problem.
The process check itself remains unbuilt. One thing it would catch,
and this design does not: a Claude worker whose job-state file went
missing while the worker still runs.
The lock probe is Codex's signal and says nothing about Claude; the
record-presence rule reads a store that, in that failure, is lying.
That case is not addressed here, and Decision 7's naming of the
process check stays on the record for whoever eventually addresses it.

**Rejected: the process-cwd scan.** For the four axes above. It is
strictly more machinery answering a less precise question, and it
carries an unanswered portability problem this route simply does not
have.

### Decision 2 -- the lock is the guard, staleness is the trigger, and they cannot be the same thing

Decision 7 rejected inferring liveness from a process on the grounds
that a finished `codex exec` leaves a resumable session with no
process at all -- process absence is not session death. That reasoning
is correct and survives contact with the lock: lock-absence means the
session is not being written to *right now*, which is not the same as
the session being *finished with*. Every Codex session spends almost
all of its life with the lock free -- the turn ends, the worker exits,
the lock releases, and the developer may come back tomorrow. A rule
that reclaimed on lock-absence alone would reap every resumable
session minutes after its turn ended, which is precisely the data loss
the predecessor design refused to risk.

So the two signals hold two different jobs, and neither can do the
other's.

**The lock is the guard.** Never reclaim a session with a live writer.
It is the safety property, absolute and instantaneous: whatever the
mtime says, a held lock means hands off. It cannot be the trigger
because it is almost always free.

**Rollout-mtime staleness is the trigger.** Nobody has worked in this
session for longer than the grace period. What makes staleness a
genuine "abandoned" signal rather than a "finished" one is measurement
7's second half: mtime advances again on `codex exec resume`, because
a resume appends to the same rollout inode. A developer who keeps
coming back to a session keeps resetting its clock, and keeps the
instance. A session whose mtime stopped moving a day ago is one nobody
resumed in a day -- not one whose turn happened to end.

Why the guard still earns its place when the trigger already waited a
day: mtime advances *while a session is written to*, so a live
writer's rollout is normally fresh and the trigger never fires under
one. Normally. A worker stuck in a very long tool call could hold the
lock for longer than the grace period without appending a line, and in
that corner the trigger fires and the guard is the only thing standing
between the sweep and a live session's directory. Two independent
signals, both of which must say "gone" -- that redundancy is the
design, not an accident of having found two signals.

**Rejected: lock-absence alone.** Stated above -- it reaps every
finished-but-resumable session, the exact error Decision 7's reasoning
forbids.

**Rejected: mtime alone.** It is almost always right and its one
failure -- the long-running quiet writer -- destroys a live session's
working directory. A guard that costs one syscall pair is cheap
insurance against a failure whose cost is somebody's in-flight work.

### Decision 3 -- staleness first, lock second

The two checks run in a fixed order: staleness is tested first, with a
plain `stat` of the rollout, and the lock is probed only for a record
already past the grace period. Two reasons; either alone would
settle it.

**It makes the probe rare.** A stat is a read with no side effects,
safe to take against every mapped Codex session on every sweep. The
probe acquires a lock, however briefly, in a directory Codex owns.
Ordering the checks means the probe runs only against sessions
untouched for a day or more -- for a developer actively working, that
is zero probes per sweep.

**It nearly eliminates a real race.** Any lock niwa takes, however
briefly, could make a concurrent `codex exec resume` fail: spike
finding 18 measured the store-conflict refusal -- "thread `<id>`
already has an active writer" -- that a second writer receives during
session bootstrap. If niwa's probe holds the lock at the instant a
resume tries to acquire it, the resume is the second writer and gets
that error. Confining the probe to sessions nobody has touched in over
a day makes the collision require a developer resuming a session at
the exact syscall-width moment niwa probes it, having not touched it
for the entire grace period -- vanishingly unlikely.

The residual risk is stated rather than claimed away. The window is
one acquire-release syscall pair, and the consequence of losing the
race is a resume that fails cleanly and succeeds on retry: finding 18
also measured that a refused resume leaves the rollout byte-identical,
forks nothing, and spends nothing. The worst case is one confusing
error message, phrased in Codex's internal vocabulary, once, for a
developer who wins a lottery nobody wants to win. That is an accepted
cost, not an eliminated one.

**Rejected: probing every mapped session on every sweep.** The
ordering costs nothing -- the trigger has to be evaluated anyway --
and unordered probing multiplies the race window by the number of
sessions and the frequency of sweeps for no information the sweep can
use: a fresh session's lock state changes nothing about a sweep that
will spare it on mtime regardless.

**Rejected: reading the kernel's lock table instead of probing.**
Reading `/proc/locks` would observe held-ness without ever acquiring
anything, closing the race entirely. It is also Linux-only -- the
exact portability question Decision 1 chose this signal to dissolve --
and it requires mapping device and inode numbers back to paths, which
is more machinery than the race's measured consequence justifies.

### Decision 4 -- the rule is a third LivenessKind, reached through the declaration

The reaper already decides a mapped instance's fate by switching on
the declared liveness kind, never on an agent constant: the gate reads
the mapping's recorded agent, resolves its launch spec, and asks the
declaration (`livenessUnreadable`). That structure is the predecessor
designs' central deliverable, and this design lands inside it rather
than beside it.

**Chosen: `LivenessRecordActivity` joins `LivenessRecordPresence` and
`LivenessNone` as a third member of `LivenessKind`, and
`SessionRecords` gains the description of where the writer lock
lives** -- the lock directory as path components under the same root
the records resolve against, and the suffix appended to a session id
to name its lock file. The description stays a description: the
declaration names paths and the consuming layer walks them, the same
split the record-reading surface already keeps. Codex's row declares
the new kind with the lock directory Codex actually uses; Claude's row
is untouched. A third agent whose store behaves like Codex's --
records never removed, activity readable, a writer lock held while
attached -- declares the same kind and inherits the whole rule,
which is the test of whether this is a declaration or a Codex branch
wearing one's clothes.

The kind's semantics are sharpest against its siblings.
`LivenessRecordPresence` proves a session is gone: the agent removes
the record on delete, so absence is the developer's own verb observed.
`LivenessRecordActivity` proves no such thing, and says so: it asks
for a grace period rather than an event, because an agent with no
delete verb never produces the event. The kind's honesty is that it
names a different question -- "abandoned long enough, with no writer
attached" -- rather than pretending to answer the presence question
with worse evidence.

One asymmetry was found while making this change, and it is recorded
as an omission rather than taken as permission. The capability
catalog's own comment says outright that the set is closed and adding
a member is a product decision rather than an implementation detail,
and the reason kinds are enumerated under the same closed posture.
`LivenessKind` carries no such guardrail sentence. Adding a member to
it is nonetheless the same kind of act -- the completeness checks over
launch specs treat the kinds as a closed set, and a new member changes
what every future declaration is allowed to claim -- so this design
treats the addition as a product decision it is making explicitly,
and notes that the missing comment should say what its siblings' say.

**Rejected: hardcoding the Codex rule in the reaper.** A branch on
the recorded agent at the sweep site is the parallel hardcoded pass
the whole contract exists to prevent, and it leaves the next
never-deleting agent to rediscover and re-implement the rule beside
it.

**Rejected: a pair of booleans on the record description** -- "records
removed on delete" and "activity readable" -- instead of a third kind.
Booleans compose into states nobody declared (removed-on-delete *and*
activity-readable reads as two rules with no stated precedence), and
the reaper's gate would decode a truth table where it now reads a
name. A closed kind keeps illegal states unrepresentable.

### Decision 5 -- the grace period is 24 hours, and it changes what reclamation means

**Chosen: a default grace period of 24 hours, overridable through
`NIWA_REAP_IDLE_GRACE`,** following the precedent
`NIWA_DESTROY_GRACE_SECONDS` set for tuning a lifecycle wait through
the environment rather than through configuration surface a workspace
would have to grow.

Be clear about what this introduces, because it is a real difference
from Claude's semantics and not a detail of tuning. A
Claude-dispatched instance is kept until the developer deletes the
session -- without bound, a week or a month, because deletion is an
explicit verb and niwa waits for it. Codex has no delete verb, so
that semantic is simply not available: the choice is between a
bounded grace period and a permanent leak, and this design chooses
the bound. A developer who walks away from a Codex session for three
days comes back to a reclaimed instance; the same walk away from a
Claude session costs nothing. That asymmetry is the honest shape of
the two agents' stores, not a bug in either rule.

Why 24 hours rather than reusing the existing 30-minute
`dispatchBackstopTTL`: the two constants bound different things. The
backstop TTL bounds an in-flight dispatch that never got its mapping
written -- seconds to tens of seconds of legitimate work, where 30
minutes is already generous. The grace period bounds a human deciding
whether to come back to a session: overnight is the obvious unit of
walking away, and a period shorter than a night's sleep would reclaim
sessions people fully intend to resume tomorrow morning. The two
numbers measure different clocks and sharing a constant would couple
them for no reason beyond the coincidence of both being durations.

And the cost, stated plainly. Reclaiming the instance ends practical
resumability. The rollout survives -- it lives in Codex's own store
and niwa never touches it, so `codex exec resume` will still open the
conversation -- but resuming a dispatched session in practice means
working in the instance it ran in, and that directory is gone. The
conversation without its working tree is a transcript, not a session.
That is why the period is generous rather than aggressive, why it can
be widened per-host through the environment, and why each idleness
reclamation prints a stderr line saying whose session went how long
untouched: a developer who loses an instance to the grace period
should learn it from the sweep's own output, not from a resume
landing in a directory that no longer exists.

**Rejected: reusing `dispatchBackstopTTL`.** Stated above -- it
bounds a different clock.

**Rejected: no override.** A fixed period bakes one team's rhythm
into everyone's tooling. A developer who dispatches on Friday and
resumes on Monday needs three days, and telling them to change their
week is not an answer the environment variable doesn't give more
cheaply.

**Rejected: unbounded, matching Claude.** That is the status quo
under a different name -- with no event to wait for, unbounded
waiting is the permanent leak this design exists to close.

### Decision 6 -- the same rule reaches the unmapped backstop

The backstop ages unmapped dispatch-named instances -- the orphan a
killed niwa leaves when the worker outlives the mapping that was
never written -- and its guard against reclaiming a live worker's
directory asks two questions: `instanceHasLiveJob` reads Claude's
harness state, and `instanceHasRecordedSession` asks every launchable
agent's declared store whether a session is rooted in the instance.

Today that second guard returns a permanent spare for any agent whose
records are never removed: the record stays, so the answer stays yes,
and an unmapped instance a Codex worker once ran in is spared by every
future sweep forever. The predecessor design named this as the mapped
path's declared cost reaching the backstop, and reported it the same
way. With an activity rule available, the backstop stops inheriting
the cost: for a recorded session whose agent declares
`LivenessRecordActivity`, `instanceHasRecordedSession`'s "yes" is
qualified by the same trigger and guard the mapped path uses --
rollout mtime within the grace period, or a held writer lock, spares
the instance; a stale, unheld session no longer does. The Codex
sparing becomes temporary exactly as the mapped path's does, under
one rule stated once. A within-grace spare is deliberately silent:
sparing that ends on its own needs no notice, where the permanent
sparing it replaces had to be reported on every sweep precisely
because nothing else would ever surface it.

The backstop's existing age gate is unchanged and still runs first: an
unmapped instance younger than `dispatchBackstopTTL` is never a
candidate, whatever its sessions say, because it may be a dispatch
still in flight.

**Rejected: leaving the backstop permanent.** It would close the leak
on the mapped path and keep it open on the orphan path, where the
instances are by definition the ones nothing tracks. Half a fix here
is a leak that merely requires a crash to start.

## Decision Outcome

Codex declares `LivenessRecordActivity`, the third member of
`LivenessKind`, and the reaper's existing declaration switch gains the
arm that reads it -- no agent constant appears at any sweep site, and
Claude's `LivenessRecordPresence` path is untouched. The rule the arm
applies is two signals in a fixed order: rollout-mtime staleness past
the grace period is the trigger, evaluated first with a plain stat,
and a non-blocking probe of Codex's own per-session writer lock is the
guard, taken only for a record already past the grace period. Both
must say "gone" before an instance is reclaimed, and every
unanswerable question -- an unreadable store, a stat that fails, a
probe that cannot say -- resolves to sparing. The default grace period
is 24 hours, overridable through `NIWA_REAP_IDLE_GRACE`. The unmapped
backstop applies the same rule through `instanceHasRecordedSession`,
so its Codex sparing becomes temporary exactly as the mapped path's
does. The process-cwd check the predecessor design named stays
unbuilt, and the one case it would have caught -- a Claude worker
whose job-state file went missing -- stays open, explicitly.

## Solution Architecture

The pieces land where their kind already lives, and the read direction
is unchanged: the declaration describes, the consuming layer walks.

**The declaration.** `SessionRecords` gains two fields describing the
writer lock: the lock directory as path components, and the suffix
appended to a session id to name its lock file. The lock directory is
a sibling of the record store under the agent's own home directory,
not a child of it -- Codex keeps rollouts and writer locks in two
directories side by side under `$CODEX_HOME` -- so the description
resolves it against the same root the records resolve against, and it
follows the same home-relocation override: an agent whose environment
variable moves its state directory moves both stores together, and the
description tracks them together. Both fields are empty for an agent
that takes no such lock, and are read only when the declared liveness
kind is `LivenessRecordActivity`; the completeness checks over launch
specs hold that a declaration of the new kind actually describes a
lock, and that a declaration of either older kind does not carry one
that nothing will read.

**The probe.** A `//go:build unix` file holds the flock attempt, with
a non-unix counterpart that answers "unknown" rather than guessing --
the same build-tag split `internal/workspace/codex_trust_lock_unix.go`
already ships for the trust lock, mirrored rather than invented. An
"unknown" answer flows into the sweep as unreadable liveness, which
spares: a platform where the lock cannot be probed gets the
predecessor design's behavior, a permanent spare with a reason, not a
guess. The probe itself is one open without create -- ENOENT from it
is the no-writer answer -- then one non-blocking exclusive acquire,
immediate release, close. Nothing cached, nothing written.

**The gate.** The reaper's per-mapping gate now returns one of three
verdicts -- live, gone, unreadable -- rather than a boolean plus a
spare. The three-way split is load-bearing: "cannot be read" and "is
gone" must not collapse into one answer, because one of them destroys
a directory and the other one writes a line to stderr, and a boolean
that means "not live" would let an error path fall through to the
destructive side. The gate absorbs the sweep's former
unreadable-agent helper rather than sitting beside it: the shapes
that helper answered for -- an unrecognized agent, an agent niwa
launches no worker for, a declaration with no readable signal -- land
on the same unreadable verdict as an activity read that fails, so one
function owns the whole question.

The sweep, per Codex mapping, in order:

1. Resolve the mapping's recorded agent to its launch spec; no spec,
   or a declaration the sweep cannot act on, is the unreadable verdict
   and the instance is spared with the reason reported
   (`reportSparedInstances`).
2. Find the session's rollout by working-directory correlation
   (`recordForInstance`): the record whose recorded cwd resolves to
   the instance directory, exactly the correlation the dispatch
   capture makes and for the same reason -- niwa gave this worker a
   directory nothing else was using. Two records claiming the
   directory is a state to decline, not to pick a winner in, and a
   mapping whose session has no matching record at all is the
   unreadable verdict with its own reason: the mapping says a session
   of this agent's ran here and the agent's own store no longer
   describes it.
3. Stat the matched rollout. A stat that fails is unreadable. An
   mtime within the grace period is live -- the trigger has not
   fired, and the lock is never probed.
4. For a rollout stale past the grace period, probe the writer lock:
   open the lock path without creating it, ENOENT meaning no writer;
   a file that opens gets one non-blocking acquire attempt, released
   immediately. A held lock is live, whatever the mtime said. Unheld
   or absent, the verdict is gone.
5. Gone reclaims: destroy the instance, delete the mapping, and print
   one line to stderr naming the instance, the agent, and how long
   the session went untouched. The line prints after the destroy
   succeeds, so it reports something that happened rather than
   something intended; the count on stdout stays a bare count. There
   is no warning before the reclamation -- the grace period is the
   warning -- so this notice is where the developer learns the
   instance was reclaimed for idleness, rather than from a resume
   into a missing directory.

The backstop's half (`selectBackstopTargets`) runs its existing gates
first -- dispatch-named, unmapped, older than `dispatchBackstopTTL`,
no live Claude job (`instanceHasLiveJob`) -- and then
`instanceHasRecordedSession` applies the same correlation, trigger,
and guard (steps 2 through 4) to any recorded session whose agent
declares the activity kind, instead of returning its former
unconditional yes.

## Implementation Approach

The change lands in the order the reads flow, each piece confronted by
the checks that already police its layer:

1. The declaration: `LivenessRecordActivity`, the two lock-description
   fields on `SessionRecords`, and Codex's row declaring the kind with
   the measured lock directory and suffix. The spec completeness suite
   is extended in the same change so a declaration of the new kind
   without a lock description, or of an old kind with one, fails on
   arrival.
2. The probe, unix and non-unix halves. Two construction rules are
   carried from the measurements into the probe rather than left to
   call sites: it opens the lock path without `O_CREAT` and treats
   ENOENT as "no writer", because a probe that creates the file
   manufactures a crash-leftover artifact in a store niwa does not own
   (measurement 6); and it caches no file descriptor and no inode
   across probes, because the lock file is a fresh inode on every open
   and a cached handle probes a file no writer will ever touch again
   (measurement 5).
3. The reaper's mapped gate: the three-verdict shape, the
   staleness-then-probe order, and the grace-period read --
   `NIWA_REAP_IDLE_GRACE` resolved where it is used, a value that
   will not parse or is not positive falling back to the 24-hour
   default.
4. The backstop: `instanceHasRecordedSession` qualified by the same
   trigger and guard for activity-kind sessions.
5. The notice and the guide: each instance reclaimed on idleness
   prints one stderr line after its destroy succeeds, naming the
   instance, the agent, and how long the session went untouched.
   `reportSparedInstances` is unchanged -- a within-grace spare is
   silent by design, since sparing that ends on its own needs no
   notice. The guide's reclamation caveat is rewritten from "never
   reclaimed" to the grace-period behavior in the same change, so the
   published story and the shipped behavior flip together.

The reap tests drive a real flock rather than a stub. A stubbed lock
would only assert the test's own idea of what a lock is -- held-ness,
release-on-death, the fresh-inode behavior -- which is precisely the
part measurement had to establish and the part a regression would
silently break. The tests take and release real locks in a temporary
store shaped like Codex's and assert the sweep's verdicts against
them, with the clock and the grace period injected so staleness is
constructed rather than waited for.

The two functional scenarios -- a stale unheld session reclaimed, a
stale held session spared -- use the fake codex binary and a real
flock, so they execute in CI rather than going pending the way
`@codex-live` scenarios do. The live-binary path stays what it is
everywhere else in this feature's lineage: a manual sanity check
against a real `codex`, never the only coverage.

Exit criteria: the declaration suite and spec completeness checks
green with the Codex row declaring the new kind; the reap tests green
against real locks, including the SIGKILL shape (file present, lock
released, verdict gone) and the clean-exit shape (file absent, verdict
gone); both functional scenarios executing in CI; `gofmt -l .`,
`go vet ./...`, `go test -race ./...`.

## Security Considerations

- **The probe takes an exclusive lock on a file in the developer's
  own Codex state directory, briefly.** That is the one way this
  feature can affect a running session: a resume that tries to acquire
  the writer lock at the instant niwa holds it fails with Codex's
  store-conflict error. The window is one acquire-release syscall
  pair, the failure is clean and retriable (spike finding 18: nothing
  forked, nothing spent), and Decision 3 confines the probe to
  sessions already idle past the grace period, so the collision
  requires a resume of a day-untouched session at a syscall-width
  moment. Stated as narrowed, not eliminated.
- **The feature deletes directories, so the failure direction that
  matters is reclaiming something live.** Every unanswerable question
  resolves to sparing: an unrecognized agent, an absent spec, a stat
  error on the rollout, a platform where the lock cannot be probed, a
  probe that errors rather than answers -- all land on the unreadable
  verdict, which spares and reports. Reclaiming requires two
  independent positive answers: stale past the grace period, and no
  writer holding the lock.
- **A typo cannot shorten the window before instances are
  destroyed.** `NIWA_REAP_IDLE_GRACE` is read from the environment,
  and a value that will not parse, or is not positive, falls back to
  the 24-hour default -- never to zero, which would make every idle
  Codex instance instantly eligible on the strength of a malformed
  string.
- **niwa writes nothing into the agent's state directory.** It reads
  the rollout's mtime and probes the lock directory; the probe opens
  without `O_CREAT`, so it cannot create a lock file, and the flock it
  takes leaves no trace once released. The one mutation this feature
  performs -- destroying the instance and its mapping -- happens
  entirely inside directories niwa owns. The rollout itself is never
  touched: a reclaimed session's conversation survives in Codex's own
  store.
- **The session id in the lock path comes from a record niwa did not
  write.** The activity check does not use the mapping's stored id at
  all: the record is found by working-directory correlation, and the
  id is read out of the agent's own rollout JSON -- a file in a
  directory anything on the host can drop a file into. So the id is
  validated where it is used: `recordActivityLive` rejects any id
  that fails `workspace.ValidSessionID` before joining it into the
  lock path, and a planted record carrying a path-shaped id reads as
  unanswerable and spares rather than steering the probe outside the
  declared lock directory.

## Consequences

What a developer now sees. A Codex-dispatched instance is reclaimed by
the ordinary sweep once its session has been idle past the grace
period -- by default, a day after the last turn or resume -- instead
of accumulating until a manual `niwa destroy`. While the session is
fresh, or whenever a writer is attached, the sweep spares the
instance silently -- sparing that ends on its own needs no notice,
unlike the permanent sparing it replaces, which was reported on every
sweep because nothing else would ever surface it. When the sweep does
reclaim, it prints one stderr line per instance, after the destroy
succeeds, saying which agent's session had gone how long untouched;
the count on stdout stays a bare count, and nothing warns before the
reclamation -- the grace period is the warning. Reclamation ends
practical resumability, so that fact arrives through the notice, not
through a failed attempt to pick the work back up. A developer who
needs longer than a day sets
`NIWA_REAP_IDLE_GRACE` in their environment and gets exactly the
window they asked for.

Positive:

- The permanent leak is closed on both reaper paths -- mapped sweep
  and unmapped backstop -- under one rule declared once, and the
  sweep's stderr note about instances "no sweep will reclaim" stops
  being the permanent fixture it was training developers to ignore.
- The safety property is stronger than the leak fix needed: the guard
  is the kernel's own lock state, immune to stale files, checked at
  the last moment before action, and paired with a trigger that
  already waited a day. Reclaiming a live session requires both
  independent signals to be wrong at once.
- The portability question that stalled the process-scan route never
  arises. The probe is a pattern the repository already ships for
  unix hosts, aimed at one known path.
- A future agent with a never-deleting store has a declaration to
  make rather than a rule to re-invent.

Negative, accepted:

- Codex reclamation is bounded where Claude's is event-driven. A
  developer can lose an instance by walking away longer than the
  grace period, which cannot happen to a Claude session. Mitigated by
  the generous default, the override, and the report -- not
  eliminated.
- The probe can, in a syscall-wide window, cause a concurrent resume
  to fail once with a store-conflict error. Decision 3 confines the
  window to sessions idle past the grace period and the failure is
  clean and retriable; the risk is stated rather than zero.
- The rollout outliving the instance means a reclaimed session still
  resumes as a conversation with no working tree. The report names
  the reclamation; nothing can give the directory back.

## What this does not do

- It does not address the missing-job-state Claude case. A Claude
  worker whose job-state record disappears while the worker runs is
  invisible to the record-presence rule, and the process-cwd check
  that would catch it remains unbuilt. That gap predates this design
  and survives it, now explicitly.
- It does not change Claude's rule at all. `LivenessRecordPresence`,
  `sessionLive`, and the mapped sweep's behavior for Claude mappings
  are untouched; no grace period applies to an agent whose store
  observes the delete verb directly.
- It does not give the declaration table an axis for launch context.
  The predecessor design recorded that the contract cannot express
  where-from scoping; nothing here adds it, and the liveness kinds
  stay a per-agent, per-store vocabulary.

## References

- docs/designs/current/DESIGN-codex-background-dispatch.md -- the
  predecessor: Decision 7 declared `LivenessNone`, built the sparing
  and its report, and named this design's problem as the next
  feature's work. Its "explicitly not built" paragraphs are superseded
  here; its rejection of process-based liveness inference is built on.
- docs/designs/current/DESIGN-agent-capability-contract.md -- the
  declaration model this design's kind extends: states, reason kinds,
  and the closed-set posture Decision 4 measures itself against.
- docs/spikes/SPIKE-codex-discovery-mechanics.md -- findings 17
  (rollout stability and mtime behavior), 18 (the writer lock and the
  mid-turn resume refusal; its "stale lock files are routine" sentence
  is corrected above), and 19 (detached launch mechanics).
- internal/workspace/codex_trust_lock_unix.go -- the non-blocking
  flock pattern the probe reuses, already shipped under a unix build
  tag.
- internal/worktree/procinfo_linux.go -- the beginning of the
  process-scan machinery the predecessor named and this design leaves
  unbuilt.
- docs/guides/codex-agent.md -- the published guide whose reclamation
  caveat this design retires in favor of the grace-period behavior.
