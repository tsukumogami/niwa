---
schema: brief/v1
status: Accepted
problem: |
  Fanning out Claude Code background agents from a niwa workspace has no reliable
  one-step path to put each worker in its own fully-configured instance. The
  automatic hook path cannot deliver instance config and mis-targets workers; the
  manual pre-create-then-launch path works but is tedious and easy to get wrong.
outcome: |
  A developer runs one niwa command with a task prompt and gets a background worker
  running in its own isolated ephemeral instance -- loading that instance's full
  configuration natively, visible in Agent View for management, and reclaimed
  automatically when the session ends.
motivating_context: |
  A prior exploration established that a Claude Code SessionStart hook can never
  re-root a session's settings resolution, so the existing hook-based isolation is
  structurally capped at file-tree separation. The user then verified that launching
  `claude --bg` from inside an instance directory delivers full per-instance
  configuration AND still registers the session in Agent View -- the mechanism this
  brief frames a command around.
---

# BRIEF: niwa instance-dispatch command

## Status

Accepted

This brief frames a net-new, additive niwa command. The downstream PRD owns the
requirements (command interface, flags, the full corner-case requirement set); this
brief frames the problem, outcome, journeys, and boundary. The existing
SessionStart/SessionEnd hook auto-provisioning is explicitly out of scope and is left
untouched.

## Problem Statement

niwa creates ephemeral workspace instances (`niwa create` -> `tsuku`, `tsuku-2`, ...),
each a full clone with its own materialized Claude Code configuration. When a developer
fans out Claude Code background agents to work in parallel, each worker should run in
its own instance so the workers do not collide on branches and files and so each worker
operates with the configuration its instance carries.

There is no reliable one-step way to get there today. Two paths exist and both fall
short:

- The **automatic path** is a workspace-root SessionStart hook that provisions an
  instance and injects an instruction to `cd` into it. But Claude Code resolves a
  session's settings, plugins, hooks, and environment at launch from the launch
  directory, and a mid-session `cd` does not re-root that resolution. So the worker
  never loads its instance's plugins, skills, hooks, or environment -- it gets a
  separate working tree but not its instance's configuration. The same hook also keys
  its background-worker detection on a launch-profile signal that does not reliably
  distinguish a dispatched worker, so the common case is silently skipped.

- The **manual path** -- pre-create an instance with `niwa create`, then launch a
  background worker from inside that instance directory -- does work: a worker launched
  from inside the instance boots rooted there with full configuration, and still shows
  up in Agent View for unified management. But it is several manual steps every time,
  the steps are easy to get wrong (wrong directory, forgotten cleanup), and nothing
  records the instance-to-session relationship or reclaims the instance afterward.

The gap is the absence of a single, reliable command that performs the manual path's
correct sequence -- create an instance, launch a worker rooted in it, record the
relationship, and let existing reclamation clean it up -- so a developer does not have
to assemble it by hand or fall back to the automatic path that cannot deliver
configuration.

## User Outcome

A developer who wants a background agent to work in isolation runs one niwa command,
passing the task. A fresh ephemeral instance is created, a Claude Code background
worker is launched already rooted inside it -- so the worker loads that instance's full
configuration (settings, plugins, skills, hooks, environment) the same as if the
developer had started Claude there by hand -- and the session is registered in Agent
View so the developer can list, attach to, inspect, and stop it alongside every other
session. The developer does not pick a directory, does not run a separate create step,
and does not have to remember to tear the instance down: when the session ends, by any
means, the instance is reclaimed without manual cleanup. Fanning out several agents is
running the command several times, and the workers stay isolated from each other.

## User Journeys

### Solo dispatch from the workspace root

A developer working at a niwa workspace root wants an agent to take on a task without
disturbing their own working tree. They run the dispatch command with the task prompt.
A new ephemeral instance is created, a background worker starts inside it with the
instance's full configuration, and the developer is told the session is running and how
to attach to it. The developer continues their own work uninterrupted; the agent
operates in its isolated instance.

### Parallel fan-out

A developer wants several agents working different tasks at once. They run the dispatch
command several times in quick succession. Each invocation produces its own distinct
instance and its own worker; the workers never share a working tree, and concurrent
invocations do not collide on instance identity. All of the resulting sessions appear
together in Agent View.

### Hands-off reclamation after the session ends

A dispatched worker reaches the end of its task -- or the developer stops it from the
terminal, deletes it from the Agent View UI, or the session dies because the process
crashed or the machine rebooted. In every case the developer does nothing special to
clean up: niwa's existing reclamation sweep recognizes the instance's backing session
is gone and reclaims the instance, so isolated runs do not leave a growing pile of
abandoned instances behind.

### Dispatch from somewhere other than the root

A developer runs the dispatch command from inside an existing instance, inside a
worktree, inside a single repo checkout, or from a directory unrelated to any niwa
workspace. The command resolves which workspace the new instance belongs to from where
it was run, and when the location does not belong to a niwa workspace it fails with a
clear message rather than creating something in the wrong place or silently doing
nothing.

## Scope Boundary

### In

- A new, additive niwa command that, in one invocation: creates an ephemeral instance,
  launches a Claude Code background worker rooted inside that instance, recovers and
  records the worker's session identity, and records the instance-to-session
  relationship so existing reclamation can reclaim it.
- Defining the command's behavior across the launch locations a developer may invoke it
  from (workspace root, inside an instance, inside a worktree, inside a repo, an
  unrelated directory).
- Defining behavior for the session lifecycle's reclamation paths: a session that ends
  normally, is stopped from the terminal, is deleted in Agent View, crashes, or is lost
  to a reboot.
- Naming the corner cases the command must survive so the downstream PRD can write
  requirements against them: session-identity capture failing or being ambiguous under
  concurrent dispatch; instance-naming races between concurrent invocations; a
  dispatched worker itself invoking the command (nesting); the limits of an
  argv-only task prompt; and partial failure (instance created but the worker fails to
  launch, or the worker launches but the relationship is not recorded) with the
  resulting orphan reclaimed by the existing sweep.

### Out

- Modifying or retiring the existing SessionStart/SessionEnd hook auto-provisioning
  (tracked separately as tsukumogami/niwa#171 and tsukumogami/niwa#172). This command
  is additive; both paths coexist for now, deliberately, so the team can observe how
  Agent View evolves before deciding whether the hook path should be retired.
- Dispatching a worker into an instance from inside the Agent View UI itself. Per-worker
  working directories at Agent-View dispatch time are an unshipped Claude Code
  capability (tracked upstream as anthropics/claude-code#60975 and #31940); this command
  works from the terminal, which is what registers a launched session into Agent View
  today.
- Changing the instance model itself -- how `niwa create` clones, materializes
  configuration, or numbers instances. The command consumes the existing creation path
  unchanged.
- Cross-machine or remote dispatch. The command launches a local background session on
  the same host.
- Building a new reclamation mechanism. Teardown reuses the existing reclamation sweep;
  the command's job is to record the relationship that sweep already keys on, not to
  invent a parallel teardown.

## Open Questions

These framing details are deferred to the downstream PRD's Decisions and Trade-offs
section; none blocks the framing.

- The exact command verb and flag surface (whether the task prompt is positional, how
  an optional human-friendly label is supplied or derived, whether the command attaches
  to the session after launching or returns immediately).
- How aggressively reclamation should run relative to dispatch (the existing sweep runs
  opportunistically on instance creation; whether dispatch should also trigger it, or
  rely on the existing trigger, is a requirement detail).
- How a long, multi-line, or special-character task prompt is delivered given the
  background launch accepts the prompt only as a single argument.

## References

- docs/designs/current/DESIGN-ephemeral-session-instances.md -- the existing
  hook-based ephemeral-instance feature this command sits alongside (and deliberately
  does not modify).
- docs/guides/ephemeral-session-instances.md -- the contributor guide for the existing
  feature: the mapping store, `niwa reap`, and the reclamation sweep this command reuses.
- docs/guides/worktree.md -- the `niwa session attach` supervisor precedent (launching
  Claude rooted in a chosen directory) the command generalizes.
