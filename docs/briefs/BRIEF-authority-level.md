---
schema: brief/v1
status: Accepted
problem: |
  An operator can't say how much a dispatched session may decide on its own.
  Every worker settles every decision point the same way whatever the
  workspace, the one per-machine knob governs only tool-call approval, and
  nothing records what authority a dispatch was given or lets one be narrowed.
outcome: |
  An operator sets how much authority a workspace's dispatched sessions get,
  narrows it for one dispatch, and reads back what each dispatch resolved to.
  A session that reaches a decision point learns whether it settles it itself
  or hands it to a person, and tools that enforce authority have one answer to read.
motivating_context: |
  The same toolkit serves a solo maintainer who lets agents finish their own
  work and a team that wants a person at every gate. Both run the same
  decision points; what differs is who may decide. niwa launches every
  delegated session, so niwa is where that answer has to be set and recorded.
---

# BRIEF: Authority level for dispatched sessions

## Status

Accepted

## Problem Statement

A session that `niwa dispatch` launches runs as the operator and decides
whatever it reaches. A solo maintainer who wants agents to accept their own
documents and merge their own green pull requests gets that. A team that wants
a person at the merge gets it too, because nothing distinguishes the two
workspaces. The worker has no way to learn which kind of workspace it's in, so
a skill that reaches a decision point either always settles it or always asks,
depending on how the brief was written rather than on what the workspace wants.

The only authority setting niwa offers today is the permission mode, set per
machine with `[global] dispatch_permission_mode` or per dispatch with
`--permission-mode`. It decides how tool calls get approved. It says nothing
about who settles a decision point, so it can't stand in for one, and on that
setting the machine already outranks the workspace in both directions, which is
the wrong shape for a team's policy about who decides.

Three more gaps follow from the first:

- There's no way to give one dispatch less room than the workspace's usual, for
  a risky change or an unfamiliar repository, without editing shared config.
- Nothing records what a dispatch was allowed. After the fact, an operator
  can't read whether a session that merged its own work was permitted to.
- A tool that would keep an author from approving its own work, or apply an
  organization's rule about who decides a given point, has no authoritative
  place to learn who may decide.

## User Outcome

An operator who runs dispatched sessions sets, once per workspace, how much
those sessions may decide without a person. When one dispatch needs a shorter
leash, they narrow it for that dispatch alone, and no dispatch can get more
room than the workspace allows. Afterwards they read,
for each dispatch, the authority it ran under and where that value came from.

A session that reaches a decision point asks niwa who decides it and gets a
plain answer: settle it yourself, hand it to the session that dispatched you,
or wait for a person, plus whether the author is excluded from deciding. At the
autonomous end it finishes unattended work; at the strict end the same point
goes to a person. The solo maintainer keeps today's behavior with nothing to
configure, and nothing in this feature adds a step that only produces paperwork.

## User Journeys

### A solo maintainer keeps working unattended

A maintainer running their own workspace has never set an authority level.
They dispatch a worker to deliver a feature. The worker reaches the merge
decision, asks niwa, and is told it settles the point itself with author
exclusion off. It merges its own green pull request. The maintainer reads the
dispatch afterwards and sees it ran at the autonomous level, from the default.

### A team lead sets the strict end for a shared workspace

A team lead declares the strict level in the workspace config everyone clones.
A teammate dispatches a worker to the same kind of unit. At the merge decision
niwa tells the worker a person decides and the author is excluded, so the
worker stops with the pull request ready and waits. The teammate can see the
dispatch resolved to the strict level from the workspace config.

### An operator narrows one risky dispatch

An operator in an autonomous workspace is about to dispatch a worker against a
repository it hasn't touched before. They pass a narrower level for that one
dispatch. The worker hands its decision points to the dispatching session
instead of settling them. A second attempt, in a strict workspace, to dispatch
with a wider level than the workspace allows is refused with a message naming
the workspace's level, and no instance is created.

### A later enforcement feature reads the answer

A maintainer building author-exclusion enforcement needs to know, at each
decision point, whether the author may decide. Rather than inventing its own
setting, their code asks niwa for the decision point's resolution and reads the
author-exclusion value and the authority it came from. When an organization
later declares strictness per decision point, the same question returns the
declared answer without that code changing.

## Scope Boundary

### In

- A workspace-wide authority level an operator sets in workspace config, with
  a default that keeps today's behavior.
- A per-dispatch setting that can only narrow the workspace's level, and the
  refusal of a dispatch that asks for more.
- How the authority level resolves against the machine's existing permission
  setting, stated so neither silently widens the other.
- A durable record of each dispatch's resolved level and its source, and a way
  to read it back.
- A per-decision-point answer a session or tool can ask for: who decides, and
  whether the author is excluded, derived from the level until an
  organization's declaration exists, with a clear place for that declaration to
  be read instead.

### Out

- An organization-wide declaration of strictness per decision point and named
  deciders. That belongs to organizational governance work; this feature only
  leaves the place it plugs into.
- Capping a child dispatch at its parent's level (never wider down the tree).
  The record here is what that later work reads, but enforcement across parent
  and child is separate.
- Telling a session, in its prompt or context, what it may do. A session asks
  when it reaches a decision point; briefing it up front is separate work.
- Routing a refused or escalated decision to a specific person or session.
  This feature says who decides, not how the question gets there.
- Enforcing author exclusion at acceptance and merge, and locking the strict
  end so nothing below can widen it. Both build on the answer this feature
  exposes.
- Changing the permission mode niwa forwards. Tool-call approval stays the
  machine's and the dispatch flag's, unchanged.
- Making the toolkit's own skills consult the answer. Wiring shirabe's
  decision points to it is follow-up work in that repository.
