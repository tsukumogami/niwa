---
schema: brief/v1
status: Draft
problem: |
  Once a tree of dispatched sessions finishes and its instances are reaped,
  niwa keeps no local record of which session dispatched which, nothing at
  all about the person the tree worked for, and no mark on the parent ids it
  did record saying whether niwa saw that parent or took the caller's word.
outcome: |
  An operator reads, from a niwa record in the workspace and after every
  session in a tree is gone, which session asked for each dispatch, which
  person the tree acts for, and beside every parent link whether niwa
  observed it or the caller handed it. A tool that reads authority
  relations gets the same answers and never mistakes a handed parent for
  an observed one.
motivating_context: |
  Dispatched sessions now dispatch sessions of their own: a coordinator
  hands work to workers, and a worker can hand a slice on again. Authority
  grants gave each instance a record of what it was granted and which
  dispatch made it, but that record goes with the instance at reap, and
  later work (capping a child at its parent's authority, routing a
  session's questions to whoever directs it, attributing decisions to a
  person) needs the tree to still be readable after the sessions end.
---

## Status

Draft

## Problem Statement

A person starts a session, that session runs `niwa dispatch` to hand work to
a second session, and the second hands a slice of it to a third. Each
dispatch gets its own instance. While the instances exist, niwa can say a
little about how they relate: the instance's authority record names the
dispatch that made it, the dispatching instance's record when there was one,
and the calling session's id, and the session mapping at the workspace root
repeats the dispatch id and the calling session's id. When the sessions
finish and `niwa reap` removes their instances, both go with them. The only
copy of the parent that survives is an attribute on the worker's telemetry,
which lives in whatever collector the worker was pointed at, if any, and
which no local command reads.

So an operator looking back at a finished tree has nothing in the workspace
to read. They can't say which session asked for a given dispatch, they can't
walk from a leaf up to the session a person was typing into, and they can't
say who the tree was working for: no record anywhere names the person a
dispatched session acts on behalf of.

The parent ids niwa does record carry a second problem: nothing says how far
they can be trusted. The calling session's id is read from a variable in the
caller's environment, which the caller controls; a Codex worker inherits its
dispatcher's value, so its own dispatches name its grandparent. The parent
dispatch comes from the working directory, or from a `--parent-from` path the
caller supplies. niwa writes all of these the same way, with no mark saying
which ones it checked against its own records and which ones it took on the
caller's word. A tool that reasons about authority, such as one that caps a
child at its parent's grant or routes a session's questions to whoever
directs it, would have to treat a claimed parent as a fact.

## User Outcome

An operator who comes back after a tree of sessions has finished, and after
every instance in it has been reaped, reads from a niwa record in the
workspace the whole chain for any dispatch: this session was dispatched by
that one, which was dispatched from the session a person started. The same
record names the person the tree acts for, at every level, so a session two
levels down answers "on whose behalf?" the same way its parent does. Every
session below the first level has a parent on the record, which is where its
escalations go.

Beside every parent link the operator reads how niwa came by it: niwa
observed it, matching the caller against what niwa itself launched; the
caller handed it, with nothing niwa holds to check it against; or the caller
claimed a parent niwa's own records contradict, recorded and marked
unverified. A tool that reads authority relations gets these answers in a
stable form, and when it reads a handed parent it shows it as handed rather
than as something niwa saw.

None of this needs any other tool installed. The record is niwa's own and
reads the same in a workspace that never set up a workflow engine.

## User Journeys

### An operator reconstructs a finished tree

A reviewer, working in a workspace where dispatch is on, starts session A
from their own session, and A dispatches B. Both finish, and `niwa reap`
removes both instances. The next day the reviewer asks niwa about B and
reads that B's parent is A, that A's parent is the reviewer's own session,
and that both acted for the reviewer. Nothing about the answer depends on the
instances, the session mappings, or a telemetry backend still existing.

### A tool reads one link and how far to trust it

A coordinator, or a gate script, needs to know who directs session B before
it routes a question or allows an action. It asks niwa for B's lineage in
machine-readable form and gets B's parent, the parent's parent, the person
the tree acts for, and for each parent link whether niwa observed it or the
caller handed it. When the link is handed, the tool shows it as handed and
decides accordingly; it never has to guess from the shape of the record.

### A dispatch claims a parent it didn't come from

A session runs `niwa dispatch` with the calling-session variable set to some
other session's id, by mistake or on purpose. Later, the reviewer reads that
dispatch's record and sees either that niwa refused it at the time, or that
the claimed parent is there marked unverified, beside what niwa's own records
said the parent was. A tool reading the record doesn't treat the claim as an
observed parent.

### A session started with no parent session

A dispatch starts from something other than a person's session: a watcher
reacting to a pull request, or a person running `niwa dispatch` in a plain
terminal. The record says plainly that there is no parent session, so the
session sits at the first level, and still names the person it acts for, so
"on whose behalf" has an answer even at the root.

## Scope Boundary

### In scope

- A record, kept by niwa in the workspace, of each dispatch's place in the
  tree: the dispatch, the session it started, the session that asked for it,
  and the dispatch that session belonged to, if any.
- The person a dispatched session acts on behalf of, on the same record, at
  every level of the tree, including sessions with no parent session.
- A mark beside every recorded parent saying how niwa came by it: observed,
  handed, or claimed against niwa's own records. The written statement of
  where that trust boundary sits and why.
- What happens when a dispatch claims a parent other than the one it came
  from.
- How long the record outlives the instance, and what removes it.
- A way for a person to read a dispatch's lineage, and a stable
  machine-readable form for tools that read authority relations.
- Lineage of any depth, and dispatches whose parents and grandparents are
  long gone.

### Out of scope

- Capping a child's authority at its parent's. That check reads this record
  but is separate work; this feature records, it doesn't refuse wider grants.
- Hardening the trust boundary. A parent niwa can't check is recorded as
  handed, not made checkable by signatures or a broker process; enforcement
  stays where it is today.
- Reading, or depending on, any workflow engine's own session records. A
  workflow engine may keep its own record of which run started which; niwa
  neither reads it nor requires it, and a niwa user needs no other tool
  installed.
- Carrying the person onto recorded decisions, classifying where a session
  escalated, and telling a session at start who directs it. Each reads this
  record and each is separate work.
- Lineage that crosses machines or workspaces, or a copy kept in a cloud
  store. The record is local to the workspace that dispatched.
- Changing which credentials a session holds. The person a session acts for
  is recorded as a person, not as the login whose token the session uses.
- Guessing a parent from the process tree, or reviving a registry of live
  sessions. A wrong parent is worse than none.
