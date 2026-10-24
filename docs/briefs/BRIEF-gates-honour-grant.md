---
schema: brief/v1
status: Accepted
problem: |
  niwa records the named authorities a dispatched session holds, but nothing that
  decides a merge, an issue close or a release reads that record. The gate decides
  merges from a list of instance names in workspace config, so every coordinator
  hand-off needs a config change and an apply, and a refusal never says what was missing.
outcome: |
  Whoever dispatches a session decides at dispatch what it may land, and that holds:
  a successor coordinator granted merge, close and release does all three as its first
  actions with no config change and no person asked, a session refused an action reads
  which authority it lacks, and no child is granted more than its parent.
---

# BRIEF: gates-honour-grant

## Status

Accepted

## Problem Statement

Since authority grants landed, `niwa dispatch` can hand a worker named
authorities (`merge-pr`, `close-issue`, `cut-release`), directly with `--grant`
or through a role, and it writes a record into the worker's instance before
the worker starts, saying what the worker holds and where each authority came
from. That record is the only place a dispatcher states what a session may
land. Nothing that decides a landing reads it.

The workspace's gate is a `PreToolUse` hook, `gate-online.sh`, that spots
merges, issue closes and releases in the commands a session runs. It denies
every merge unless the session's instance is named in
`GATE_MERGE_EXEMPT_INSTANCE`, and it asks a person before every close and
every release. Four things follow from that, and each one is felt by whoever
is running a coordinator:

1. **A coordinator hand-off is a configuration change.** The exemption names
   instances, and a successor coordinator runs in a new instance. Before it
   can merge anything, someone has to change the workspace configuration to
   name the new instance, drop the old one, and run `niwa apply` so every
   hook sees the new list. The grant the successor was dispatched with
   doesn't help.
2. **The grant is ignored at the moment it matters.** A session dispatched
   without `merge-pr` but running in a listed instance can merge. A session
   dispatched with `close-issue` is still asked to wait for a person before
   every close.
3. **A refusal says nothing useful.** A blocked session learns it was blocked,
   not which authority it lacked or what it holds, so it can't tell whether to
   hand the step back, ask for a different dispatch or stop.
4. **Delegation can widen authority.** A dispatched session can dispatch a
   child with authorities it doesn't hold itself. niwa reads the parent's
   record at dispatch and records the link, but compares nothing.

shirabe's `/coordinate` makes the same gap visible from the other side. It
reads its merge posture from the hook's text, finds merges reserved for a
person, and hands every merge to one, whatever the coordinator was granted. A
successor coordinator that should land its own work instead queues it for
someone.

So the dispatcher's decision about what a session may land is written down
before the session starts, and then has no effect on anything the session is
allowed or refused.

## User Outcome

The person or coordinator who dispatches a session decides, at dispatch, what
that session may land, and the decision holds without anyone touching the
workspace configuration afterwards.

A reviewer who dispatches a successor coordinator with merge, close and
release authority sees it merge a ready pull request it didn't author, close
an issue and cut a release as its first actions, with no configuration change,
no `niwa apply` between dispatch and action, and no person asked. A session
refused an action reads, in the refusal, the grant it holds and the authority
it lacks, so it knows to hand the step back to whoever dispatched it. A
dispatched session that asks for a child wider than itself is refused before
anything launches. Any dispatched session can read back what it was granted,
with where each authority came from, and a session granted nothing reads that
it holds nothing.

Nothing gets weaker. A session niwa didn't dispatch, a host without niwa, and
a dispatched session whose record is missing or broken all get exactly what
today's hook gives them. Enforcement is as strong as today's hook and no
stronger: a session acting as the operator outside the toolkit can still go
around it, as it can today.

## User Journeys

### A reviewer hands the coordinator seat to a successor

A reviewer is replacing a coordinator and dispatches its successor with a role
carrying merge, close and release authority. The trigger is the dispatch
itself; there is no follow-up configuration pull request. The successor's
first actions merge a ready pull request another session wrote, close the
issue it resolved and cut the release, and the gate lets each through because
the successor's own grant holds the authority. The reviewer approves nothing
by hand.

### A worker reaches past its grant

A worker dispatched with merge authority alone finishes its pull request and
tries to close the issue and cut a release. The gate refuses both. Each
refusal names what the worker holds (merge) and what it lacks (close, or
release), so the worker hands the step back to the coordinator that
dispatched it, which closes the issue itself or dispatches a session granted
to. The worker's grant doesn't change while it runs.

### A coordinator delegates, and over-reaches once

A coordinator holding merge authority dispatches a worker asking for merge and
close. niwa refuses the dispatch before any instance exists, naming the
coordinator's own grant. The coordinator re-dispatches asking for merge alone;
that worker launches and can merge.

### A resumed coordinator checks what it may land

A coordinator that was dispatched yesterday is resumed this morning with a
ready pull request waiting. Before deciding whether to merge it or hand it
on, it asks niwa what it holds. It reads each authority with its source, the
dispatch or a named role, and gets the same answer the gate will give, so it
merges the pull request itself rather than queueing it for a person. A worker
it dispatched with nothing reads that it holds no named authority, and the
brief the coordinator wrote for that worker claims nothing more.

### An operator runs a coordinator by hand

An operator starts `/coordinate` in a session they opened themselves, or on a
laptop with shirabe and koto but no niwa. There's no grant to read, so
`/coordinate` takes its posture from the workspace's settings and hooks as it
does today, and hands a merge the hook reserves to a person. The owner's own
merging session, which isn't dispatched either, keeps what today's exemption
gives it, and nothing about it changes.

## Scope Boundary

### IN

- The session's own read-back of its grant, through one niwa lookup the gate,
  `/coordinate` and the session all use, instead of each parsing niwa's
  session files.
- The workspace gate (`gate-online.sh` in dot-niwa, and its tests) honouring
  the grant for merging, closing an issue and cutting a release, with refusals
  naming the grant held and the authority lacking, and the level's own
  refusals still applying on top.
- Moving dispatched sessions off the instance-name exemption list: for a
  dispatched session the gate reads the grant, never the list. A session niwa
  didn't dispatch, including the owner's own merging session, keeps what the
  list gives it today.
- Telling the dispatched session apart from any other session in the same
  instance, so a session started by hand inside a granted instance doesn't
  inherit the grant.
- Refusing at dispatch a child that asks for a named authority its dispatcher
  doesn't hold, before anything launches.
- `/coordinate` taking its merge, close and release posture from its own
  grant, granting each session it dispatches, and handing a step its worker
  lacks the grant for back to the dispatcher rather than to a person; with no
  grant, today's posture read.
- Making `cut-release` grantable in the workspace this is exercised in, since
  its ceiling leaves it out today.
- Every piece working with niwa and no koto installed.

### OUT

- **Stronger enforcement.** Stopping a session that edits its own record,
  environment or settings, acts from a process outside `niwa dispatch`, or
  hides its identity; refusing outright rather than falling back when a
  record is broken; checking the grant inside shirabe's own merge and close
  scripts, which the hook can't see; wrapping every command the grant
  forbids. All of that is a separate, later effort.
- **The rest of never-wider.** Capping a child's authority level by its
  parent's, walking older ancestors, and what a dispatch with no verifiable
  parent may confer. This work builds only the named-authority comparison
  against the immediate dispatcher's record.
- **Author exclusion at the merge gate.** Whether an author may merge its own
  work, and who decides, is separate work; the gate here asks whether the
  session holds the authority and whether its level lets it settle the point.
- **New gated actions.** Teardown and other actions joining merge, close and
  release later.
- **Routing a refusal.** Where a refused session should go beyond "back to
  the dispatcher" is separate work; the refusal here names what's held and
  what's lacking.
- **Moving the gate into niwa.** The gate stays a workspace hook; niwa
  provides the lookup.
- **The koto decision record.** No gate reads it, and how a grant reaches it
  is that record's own question.
- **shirabe's dispatch rework already in flight.** This work reads and
  passes a grant through `/coordinate`'s dispatch; it doesn't redesign how
  `/coordinate` dispatches.
