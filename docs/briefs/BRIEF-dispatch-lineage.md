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

<Phase 3 will name the concrete journeys that exercise the feature.>

## Scope Boundary

<Phase 3 will record what this feature holds in and pushes out.>
