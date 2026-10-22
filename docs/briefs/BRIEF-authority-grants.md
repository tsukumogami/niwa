---
schema: brief/v1
status: Draft
problem: |
  An operator who launches sessions with `niwa dispatch` can't say how much
  those sessions may do on their behalf. Every dispatched session settles
  every decision point the same way, actions like merging, closing issues
  and cutting releases can't be handed to one session and withheld from
  another, and nothing records afterwards what a dispatch was allowed.
outcome: |
  An operator states once per workspace how much authority dispatched
  sessions get and which named actions may be handed out, narrows or grants
  per dispatch, and reads back what each dispatch holds and where each part
  came from. A dispatched session can read its own grant, and a request past
  the workspace's ceiling is refused before anything launches.
motivating_context: |
  Handing a coordinating session's job to a successor shows the gap most
  sharply. A workspace hook that denies merges unless the session's instance
  is named in a config variable means every succession takes a config change
  and a re-apply before the successor can merge, and closing issues or
  cutting releases can't be handed over at all. A dispatch should carry
  named authorities (merge, close issues, cut releases, or a role bundling
  them) that niwa understands. This feature is about declaring and tracking
  that authority in the right format; stronger enforcement is separate,
  later work. An earlier unmerged draft, pull request #401, covered the
  level half and is input to this one.
---

# BRIEF: authority level and named grants for dispatched sessions

## Status

Draft

This brief frames the feature. The PRD owns the requirements and the design
owns the record's shape and where niwa resolves it.

## Problem Statement

`niwa dispatch` launches sessions that run as the operator and can do
anything the operator can. When one of them reaches a decision point, such
as accepting its own requirements document or merging its own green pull
request, nothing tells it whether this workspace wants it to settle the
point or hand it to someone. A solo maintainer who lets agents finish their
own work and a team that wants a person at every merge get the same worker,
and the difference lives only in how each brief happens to be worded.

The actions that matter most can't be handed out one at a time either. An
operator who wants one session to merge pull requests, close issues and cut
releases, and another session to do none of that, has no way to say so to
niwa. The closest lever today is a workspace hook that denies merges unless
the session's instance name appears in a config variable, and it asks a
person before every issue close or release with no way to hand either over.
So when a coordinating session hands its job to a successor, someone edits
the workspace config and re-applies it before the successor can merge, and
the closes and releases still go to a person.

An operator reviewing finished work can't tell what a session was allowed
when it was dispatched, or whether a permission came from that dispatch or
from a broader setting, so a session that merged its own work can't be
checked against what it was meant to hold.

## User Outcome

An operator decides once, for everyone who shares the workspace, how much
dispatched sessions may decide and which actions may ever be handed to one.
When they launch a session they can give it less room, or hand it exactly
the actions it needs, without editing shared config, and they're told
before anything launches if they asked for more than the workspace allows.

Afterwards the operator can answer, for any dispatch, "what was this
session allowed, and why?" A dispatched session can answer the same
question about itself, so it acts on what it holds instead of guessing from
its brief. The same decision point settles itself at the autonomous end and
goes to a person at the strict end. A workspace that configures nothing
behaves as it does today.

## User Journeys

### A solo maintainer keeps today's behaviour

A maintainer who has never heard of authority levels runs `niwa dispatch`
in a workspace with no authority settings. Their workers keep settling
their own decision points and hold no named authority, and nothing they
relied on changes.

### A workspace owner declares the policy once

The owner of a team workspace wants a person at every decision point by
default, wants merging and issue closing to be grantable but releases not,
and wants a "coordinator" role that bundles what a coordinating session
needs. They write that into the workspace config, check what niwa made of
it, and every teammate who clones the workspace gets the same policy.

### A coordinating session hands off to its successor

A coordinating session dispatches its successor with the coordinator role.
The successor's first action is to read back its own grant, and it knows
it holds merge, close and release, each because of the role, with nobody
having edited the workspace config between the two sessions. Whether the
workspace's gates let those actions through is later work that reads this
same grant.

### An operator narrows one risky dispatch

An operator working in an autonomous workspace dispatches a session into
an unfamiliar repository at the strict end. When that session reaches a
merge decision it asks niwa who decides and is told a person does, while
an otherwise identical session dispatched at the workspace's default is
told to settle it itself.

### A dispatch asks for too much

An operator, or an old script, dispatches a session with release authority
in a workspace whose ceiling leaves releases out, either directly or
through a role that names it. The dispatch is refused before any instance
is created, and the refusal tells them what they asked for and the limit
it exceeds.

## Scope Boundary

**In:**

- How much a dispatched session may decide (the authority level): set per
  workspace, limited per machine, narrowed per dispatch, never widened.
- Named authorities (at least merging pull requests, closing issues and
  cutting releases), roles that bundle them, and a workspace ceiling on
  what may be granted, granted per dispatch.
- A record of what each dispatch resolved to and where each part came
  from, readable by the operator and by the session itself.
- How these settings sit beside niwa's existing machine-level permission
  setting.
- A per-decision-point answer of who settles it, which differs at the
  level's two ends.

**Out:**

- Gates that honour the grant. The workspace hook keeps deciding merges,
  closes and releases exactly as today until separate work makes it read
  the record this feature writes.
- Capping a child dispatch at its parent's grant or level. A session can
  still dispatch a child with anything inside the workspace's ceiling
  until separate never-wider work lands.
- Stronger enforcement: credential scoping, wrapping forbidden commands,
  or a record a session can't edit. The grant is a declared, tracked
  record at today's enforcement strength.
- Changing the permission mode. Tool-call approval keeps its own setting
  and order.
- Changing what a session is told at start, or how an escalated decision
  reaches the person or session who decides it.
- Per-decision-point strictness declared by an organization. The level
  applies to every point until that declaration exists.
- Changing anything a grant holds while its session runs. A wider grant
  means a new dispatch.
