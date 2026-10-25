---
schema: brief/v1
status: Accepted
problem: |
  niwa removes instances and worktrees on signals that can't tell a paused
  session from an abandoned one, so a held worker or a run waiting for its
  successor loses its directory, and coming back means a hand-made directory
  and a resume without niwa's flags and trust.
outcome: |
  A coordinator or developer can pause a dispatched worker, or leave a run
  for a successor, and find its instance and worktree still there on return;
  once nothing needs them, the next reap takes them. A worker whose directory
  went anyway comes back through niwa, trusted and in its dispatched posture.
---

# BRIEF: Instances and worktrees outlive the sessions that need them

## Status

Accepted

## Problem Statement

People who run agent work through niwa now hold sessions open on purpose. A
coordinator dispatches a worker, the worker reaches a checkpoint, and the
coordinator holds it there for hours or a day while something upstream lands.
A workflow run that an engine like koto drives can outlive the Claude Code
session that started it: the session stops, and a successor picks up the run
later, sometimes from another instance. In both cases the directory the work
lives in has to stay put until the work is done or has moved somewhere else.

niwa can't see either case. It decides whether an instance lives from the
Claude Code job entry alone. Every `niwa dispatch`, `niwa create` and `niwa
watch` starts a reap of the whole workspace, so a reap runs far more often
than anyone asks for one. `niwa destroy` removes the directory of a session
that's still running and only keeps the session's row. And the sweep of
worktrees niwa's WorktreeCreate hook made removes a held worker's worktree
once its transcript has gone a day without a write, which is exactly what a
paused worker looks like. Nothing tells niwa that a session still needs a
path. What does know which paths a run is bound to is the workflow engine
driving the run, koto here, and niwa is deliberately free of any engine.

When a directory goes early, the cost lands on the person. The session can't
be resumed in place because its working directory is gone. Today they make a
placeholder directory by hand and run `claude --resume` from it, which brings
the worker back without the permission mode, environment and session name
`niwa dispatch` launched it with, and without the trust grant niwa withdrew
when it removed the instance (tsukumogami/niwa#369). Messages from a
coordinator then wait for approval, and the worker stops on tool calls, with
nothing saying why.

There's a quieter half too. In this organization's workspace, koto can't
tell when niwa has removed a session, so the record koto keeps of that
agent session stays open and keeps holding runs that nobody will come back
to. And a worker a coordinator dispatches starts with no link to the
coordinator, so asking koto which runs a coordinator's workers hold returns
nothing, and the work a coordinator spawned can't be traced back to it.

## User Outcome

A coordinator holding a dispatched worker, or a developer who walked away from
a workflow run, finds the instance and any hook-made worktree exactly as they
left them, however many reaps ran in between and however long the transcript
sat idle. The directory goes on the next reap once nothing needs it: the job
was removed, the run finished, or a successor took the run over from another
instance. Nobody has to remember which instances to protect.

An operator who tries to destroy an instance whose session is still running
is stopped and told which session it is, and can still go ahead when that's
what they mean. A worker whose directory went anyway comes back through niwa
in a directory that is there and trusted, in the posture it was dispatched
with, and nobody makes a directory by hand or approves its tool calls one by
one.

When niwa spares or removes an instance for a reason that came from outside
niwa, the operator reads that reason in niwa's own output. A developer whose
workspace runs no workflow engine sees niwa behave as it does today. And a
coordinator in this organization's workspace can ask koto which runs its
dispatched workers hold and get an answer, while runs held by a session niwa
has removed stop being kept on its account.

## User Journeys

### A held worker survives the reaps around it

A coordinator holds a dispatched worker at a checkpoint, keeping its Claude
Code session listed, for two days while other workers are dispatched into
the same workspace. Each of those dispatches reaps the workspace, and the held worker
sits in a worktree niwa's hook made with a transcript nobody writes to. The
worker's instance and worktree are still there when the coordinator sends the
go. Once the coordinator removes the worker's job after it finishes, the next
reap takes both.

### A run waiting for its successor keeps its instance

A developer's workflow run is paused, and the Claude Code session that drove
it has been deleted, so niwa's job-entry rule alone would reclaim the
instance. No successor has picked the run up yet. Reaps leave the instance
alone and say in niwa's output that koto reported a live run bound there. When a successor continues the run from
another instance, or the run finishes, the next reap takes the old instance.

### An operator destroys an instance whose session is running

An operator cleaning up runs `niwa destroy` on an instance and gets a refusal
naming the running session, with nothing removed. They decide the session can
go and rerun with the force flag, and the instance is removed as it is today.

### A worker whose directory is gone comes back through niwa

A held worker's instance was destroyed before the coordinator sent the go. The
coordinator runs niwa's resume verb for the worker. niwa restores the
directory, re-grants the trust it withdrew, and resumes the session with the
permission mode, environment and name it was dispatched with. The coordinator
messages the worker with no approval hold.

### A coordinator traces what its workers hold

After a day of dispatching workers through niwa, a coordinator wants to know
which workflow runs its workers still hold before it decides what to wait
for. It asks koto for its own lineage and sees each worker listed under it,
with the runs each one holds. A worker niwa has since reaped shows as
closed, and the runs it held are no longer kept on its account.

## Scope Boundary

**In:**

- niwa: a way for a workspace to tell niwa, before a reap or a destroy
  removes an instance, that something still needs it and why, with no
  engine-specific code in niwa and today's behaviour when nothing is
  declared.
- koto: answering that question for a path from the runs bound under it,
  counting a run continued elsewhere as no longer needing its old path.
- niwa: a held worker's hook-made worktree lives as long as the worker's
  session is listed, however idle its transcript.
- niwa: destroying an instance whose session is running needs an explicit
  override, and the refusal names the session.
- niwa: bringing a dispatched worker back through niwa with its dispatched
  posture, including when its instance directory is gone
  (tsukumogami/niwa#369), with no placeholder directory.
- dot-niwa: declaring the two seams niwa already ships for koto, so koto's
  agent-session records close when niwa removes a session and a dispatched
  worker is linked to its coordinator.

**Out:**

- The continues relation itself, its retention and import behaviour, which
  koto is building separately; this feature reads it and waits for it.
- Removal guards inside koto (prune floors, `KOTO_HOME` isolation, read
  fixes) and a coordinator successor's takeover of a run; both are separate
  koto and shirabe work.
- Retiring the workarounds people use today (hand-made directories, the
  host-wide koto freeze); that follows once this and its neighbours land.
- niwa reading koto's store, links or anchors directly, or shipping any
  koto-specific code path. The seam is engine-neutral by design.
- Changing which instances reap may consider at all: the ephemeral marker,
  the dispatch-name backstop's TTL, and developer instances keep their rules.
- Worktrees made with `niwa worktree create`, which reap never removes today
  and still won't.
- Codex and other agents' resume and liveness rules beyond keeping today's
  behaviour; the resume verb covers what niwa dispatch launches.
