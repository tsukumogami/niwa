---
schema: brief/v1
status: Accepted
problem: |
  niwa elevates a dispatched Codex worker for exactly the process it
  starts -- a deliberate per-invocation grant. Nothing carries that grant
  past that process, so the session a developer steps back into is
  evaluated as untrusted and reverts to read-only with per-command approval.
outcome: |
  A worker niwa dispatched keeps the posture niwa granted it when somebody
  steps back into it, through every re-entry command niwa builds or
  prints -- and niwa still writes nothing into the developer's own Codex
  configuration.
motivating_context: |
  Measured against the real binary (codex-cli 0.149.0, isolated CODEX_HOME,
  zero model turns): a session resumed without the launch-time grant records
  a read-only sandbox before its first model request; resumed with it, the
  posture is identical to the launch turn. Reported as tsukumogami/niwa#273.
---

# BRIEF: codex dispatch posture persistence

## Status

Accepted

This brief frames the problem and its boundary. The downstream PRD owns
the requirements, including the exact set of re-entry surfaces and how an
explicitly requested posture interacts with the grant.

## Problem Statement

When niwa dispatches a background Codex worker into a freshly provisioned
instance, it has to make the worker writable in a directory Codex has
never seen. It does that with a per-invocation override: arguments on the
worker's own command line that mark the working directory trusted for
that one process. The choice is deliberate, and the reasoning is recorded
beside `WorkdirGrantArgs` in `internal/agentplan/dispatch.go`: overriding
trust for one process vouches for exactly the worker niwa is starting,
and the grant is gone when that worker exits. Writing the developer's own
configuration instead would vouch for anything anybody ever runs in that
directory afterwards, including sessions niwa never launched and has no
opinion about.

The defect is that nothing carries the override past the process it was
handed to. Every way back into the session -- the resume hint dispatch
prints, the resume command `niwa list` prints -- is built from the
agent's resume verb and the session handle alone. The next process
re-evaluates posture from the developer's configuration, where the
instance root is not trusted, and the session silently drops to read-only
with per-command approval. The worker a developer steps back into is not
the worker they dispatched.

This was measured, not inferred from reading configuration code. Against
the real binary (codex-cli 0.149.0, isolated CODEX_HOME, zero model
turns), a resumed session records its resolved sandbox policy before the
first model request: without the grant, read-only; with the same grant
niwa passes at launch, workspace-write, identical to the launch turn. Two
controls rule out the competing explanations. The drop is not subagent
threads failing to inherit -- subagent threads write their own separate
session records, and the drop reproduces on a plain resume with no
subagent involved. And it is not decay within a process -- the process
niwa launched stays workspace-write for its whole life, and the first
read-only turn appears only at the next process's bootstrap.

## User Outcome

A developer who steps back into a session niwa dispatched finds the
worker in the posture niwa launched it with: writable in the instance it
was dispatched into, no approval prompt per command. That holds through
every path back in that niwa builds or prints, not just the one printed
at dispatch time. The developer's own Codex configuration stays exactly
as it was -- niwa keeps vouching per process, never in a file that
outlives the session. And a developer who names a posture deliberately
still gets the one they named: the grant is niwa's default for its own
workers, not the last word over an explicit ask.

## User Journeys

### Maya resumes the session dispatch told her about

Maya dispatches a task with `--detach` and dispatch prints the session id
with a resume hint. She comes back after lunch and pastes the hint. Today
the worker she lands in is read-only and asks permission for every
command, though it wrote freely all morning. With this feature, the
resumed session holds the same write posture as the launch turn, and her
own Codex configuration still doesn't trust the instance directory.

### Rafael comes back through niwa list

Rafael dispatched something days ago and the terminal that printed the
hint is long closed. He runs `niwa list`, finds the session, and copies
the resume command it prints. Different door, same guarantee: any
re-entry command niwa constructs carries what the worker needs to keep
its granted posture, so the surface he happened to come through doesn't
decide what the session can do.

### Priya asks for a posture on purpose

Priya wants to look around inside a finished worker's session without
letting it write anything, so she adds an explicit sandbox choice to the
resume command niwa gave her. The session comes up in the posture she
named. niwa's grant elevates its own workers by default; it doesn't
overrule a developer who made a deliberate choice.

## Scope Boundary

### IN

- Every command niwa builds or prints that steps back into a session it
  launched -- the hint dispatch prints and the resume command `niwa list`
  prints among them. The set is defined by what re-enters a session, not
  enumerated per command.
- The grant reaching those commands through the agent's own launch
  declaration, so an agent that needs no grant is unaffected and a future
  agent that needs one gets this behavior without new per-agent wiring.
- A test that fails when the grant stops reaching a resumed session.
- The Codex guide: what posture a resumed session holds, and why niwa
  grants per process instead of writing the developer's configuration.

### OUT

- **Writing trust into the developer's `~/.codex/config.toml` or any
  other persistent registry.** This would also fix resume, which is why
  it's worth excluding explicitly: it fixes it by vouching for every
  future session anyone starts in that directory, exactly the exposure
  the per-invocation design refuses. The reasoning recorded beside the
  grant stands; this feature extends the grant's reach, not its blast
  radius.
- **Changing what posture niwa grants.** The content of the grant --
  workspace-write, no network -- is untouched. This work is about the
  grant surviving re-entry, not about what it says.
- **Subagent threads spawned inside a worker process.** They inherit the
  resolved configuration of the process that spawned them, so they're
  fixed by fixing that process. Nothing niwa could put on a separate
  command line reaches them, and the measurement confirmed they were
  never the source of the reported drop.

## Open Questions

- How an explicitly requested posture and the grant compose on the same
  re-entry command -- the brief commits to the developer's ask winning,
  and the PRD owns the precise precedence.
- Whether any re-entry surface beyond the two printed commands exists
  today or arrives with in-flight work -- the PRD owns the enumeration
  against the tree as it stands then.

## References

- tsukumogami/niwa#273 -- the report this brief frames.
- `internal/agentplan/dispatch.go` -- the per-invocation grant and the
  recorded reasoning for it.
- `docs/guides/codex-agent.md` -- the published guide covering what a
  Codex session gets and what niwa writes into the developer's own Codex
  configuration.
