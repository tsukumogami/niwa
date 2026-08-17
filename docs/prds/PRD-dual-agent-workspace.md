---
schema: prd/v1
status: Draft
problem: |
  niwa prepares a workspace instance for exactly one coding agent, chosen at
  creation time. Preparing for Codex replaces the Claude context at the levels
  niwa owns and skips the repository and worktree levels entirely, so the Codex
  side of the switch delivers less than the Claude side. A developer with both
  tools installed can't use them interchangeably in one instance: switching
  means re-provisioning or hand-carrying context, and the choice is forced at
  the moment the developer knows least about which agent a task will need.
goals: |
  Every prepared instance serves both agents at once, with no agent choice at
  creation time. A developer runs `claude` exactly as today and runs `codex` in
  the same instance, from any directory inside it, and the session sees the
  layered workspace context for where it's standing plus the workspace's
  skills, and can write files immediately. Nothing niwa materializes for Codex
  dirties a cloned repository or touches the developer's own Codex setup.
upstream: docs/briefs/BRIEF-dual-agent-workspace.md
motivating_context: |
  Exploration measured live against codex-cli 0.147.0 overturned the premise
  that serving both agents requires niwa to manage a per-instance Codex home.
  Codex discovers project-level context and skills on its own, so one prepared
  instance can serve both agents without niwa touching the developer's Codex
  installation, configuration defaults, or credentials. An interactive Codex
  session was verified to start clean in a prepared repository, so the outcome
  this PRD requires is measured, not assumed.
---

# PRD: dual-agent workspace

## Status

Draft

The upstream BRIEF (docs/briefs/BRIEF-dual-agent-workspace.md, Accepted) owns
the framing: problem, outcome, journeys, and scope boundary. This PRD owns the
requirements, including the silent-failure cases the exploration flagged. The
downstream DESIGN owns the delivery mechanism: where the Codex-readable
context and skills live on disk and how they are kept in sync.

## Problem Statement

niwa's job is to prepare a multi-repo workspace instance so a coding agent can
do useful work in it: a layered context tree (instance, group, repository,
worktree) and the workspace's skills, materialized where the agent finds them.
A prior increment (docs/prds/PRD-interactive-codex-session.md) made OpenAI
Codex a selectable alternative to Claude Code, but built the selection as an
exclusive switch. A workspace declares one agent, and niwa prepares the
instance for that agent and not the other.

Exclusivity costs more than the forced choice. Preparing for Codex today
writes Codex's context only at the instance and group levels and skips the
repository and worktree levels entirely, so picking Codex means giving up
context niwa knows how to produce. And the choice is front-loaded to creation
time, when the developer knows least. Which agent a task calls for depends on
the task: one agent's quota runs out mid-afternoon, one model handles a
particular problem better, or the developer wants a second opinion on the same
code. None of that is knowable when the workspace is created. Switching agents
today means re-provisioning, maintaining a second instance of the same repos,
or hand-carrying context between the two.

This affects any developer who has both agents installed and works in a
niwa-managed workspace. The workspace should not be the thing that decides
which agent gets used.

## Goals

- A prepared instance serves both agents, always. There's no agent selection
  at creation time and nothing to configure per instance; switching agents is
  closing one and typing the other command in the same directory.
- For Claude Code, nothing changes: the developer runs `claude` and gets
  exactly the context and skills they get today.
- For Codex, the instance stops being second-class: a session started anywhere
  inside the instance sees the layered workspace context for where it's
  standing, at every layer niwa composes for Claude, plus the same skills, and
  can do real work immediately.
- The instance stays safe to hold: cloned repositories stay git-clean, and the
  developer's own Codex installation, configuration defaults, and credentials
  are never touched.

## User Stories

These follow the journeys in the upstream BRIEF's User Journeys section, which
carries the full narratives.

- As a developer whose Claude quota ran out mid-task, I want to run `codex` in
  the same directory and keep working with the same workspace context and
  skills, so that switching agents costs me the conversation, not the
  workspace.

- As a developer opening a fresh terminal deep inside a cloned repository, I
  want `codex` started right there to pick up the full layered context for
  that spot with no environment setup or wrapper command, so that any
  directory inside the instance is a valid place to start either agent.

- As a developer working in a niwa-managed worktree, I want a Codex session
  there to get what a Claude session gets, the workspace context plus the
  worktree's own framing, so that worktrees are first-class for both agents.

- As a maintainer of a workspace that declared an agent back when the setting
  was an exclusive switch, I want `niwa apply` after upgrading to keep working
  with no migration, so that the old declaration gets me more, not a breakage.

## Requirements

### Functional: both agents, unconditionally

- **R1.** Every instance niwa prepares (`niwa create`, `niwa apply`) SHALL be
  usable by both Claude Code and Codex. Preparation for both agents is
  unconditional: no agent selection exists at creation or apply time, and no
  per-instance configuration is needed to enable either agent.

- **R2.** What niwa prepares for Claude Code SHALL be unchanged: the context
  tree, settings, and skills a Claude session sees are byte-for-byte identical
  to today. This feature adds a second reader, not a rework of the first.

- **R3.** The existing instance lifecycle commands SHALL maintain the
  Codex-facing materialization the same way they maintain Claude's: creating
  an instance, re-applying it, and creating or applying a worktree each leave
  both agents' materializations current, with no Codex-specific command or
  extra step.

### Functional: layered context for Codex

- **R4.** A Codex session started in any directory inside a prepared instance
  SHALL see the workspace context for where it's standing, composed from the
  same layers niwa materializes for Claude: instance, group, repository, and,
  in a worktree, the worktree's own framing (which repo it is, what the branch
  is for).

- **R5.** R4 SHALL hold from any working directory: the instance root, a
  directory nested arbitrarily deep inside a cloned repository, and a
  niwa-managed worktree, with no environment preparation, wrapper command, or
  shell integration required.

- **R6.** When a cloned repository ships its own agent-facing context file, a
  Codex session in that repository SHALL receive both the workspace's context
  and the repository's own content. niwa SHALL NOT modify, replace, or
  suppress the repository's file.

- **R7.** The context for the repository or worktree the session is standing
  in SHALL reach the session in full. Context delivered from an outer layer
  (instance or group) SHALL NOT crowd out, truncate, or displace the innermost
  layer's content.

### Functional: skills

- **R8.** The workspace's skills SHALL be available to Codex sessions with the
  same content Claude sessions see: the same set of skills, each resolvable
  under the same name, with content delivered unmodified.

### Functional: sessions that can act

- **R9.** A Codex session started in a prepared repository SHALL be able to
  write files immediately, with no per-repository setup step by the developer.

- **R10.** An interactive Codex session SHALL start in a prepared instance
  without prompting the developer for trust, review, or approval of anything
  niwa materialized.

### Functional: clean repositories

- **R11.** Nothing niwa materializes for Codex SHALL appear in a cloned
  repository's `git status`, in any state: not untracked, not modified, not
  staged.

- **R12.** niwa SHALL NOT overwrite any file a repository ships itself.

### Functional: the developer's Codex setup

- **R13.** niwa SHALL NOT modify the developer's Codex installation or its
  configuration defaults, SHALL NOT change how Codex behaves outside
  niwa-managed instances, and SHALL never read or write the developer's Codex
  credentials or login state. The developer authenticates Codex themselves,
  once, however they choose.

### Non-functional: compatibility

- **R14.** Workspaces that declare the existing per-workspace agent setting
  SHALL keep working with no migration. The setting retains its launch-time
  meaning (which agent a niwa-launched session runs) and no longer gates which
  agents' context is materialized. Workspaces that never declared it SHALL
  also be unaffected: no config change is required to get dual-agent
  preparation.

## Acceptance Criteria

- [ ] After `niwa create` and `niwa apply` on a workspace with no
      agent-related configuration, a Codex session started at the instance
      root sees the instance-level workspace context (R1, R4).
- [ ] The files, content, and settings a Claude session sees in a prepared
      instance are byte-for-byte identical before and after this change, and
      the existing test suite passes unmodified except for tests that assert
      the old exclusivity (R2).
- [ ] A Codex session started in a directory nested at least three levels deep
      inside a cloned repository sees the instance, group, and repository
      context for that location, from a plain shell with no niwa-set
      environment (R4, R5).
- [ ] A Codex session started inside a worktree created by `niwa worktree`
      sees the workspace context plus the worktree's framing: the repository
      it belongs to and its branch (R3, R4, R5).
- [ ] In a repository that ships its own committed agent-facing context file,
      a Codex session receives both the workspace context and the
      repository's own content, and the committed file is byte-identical
      after `niwa apply` (R6, R12).
- [ ] A marker placed at the end of the repository-level context content is
      visible to a Codex session started in that repository, even when the
      instance- and group-level context is large (R7).
- [ ] Every skill available to a Claude session in the instance is available
      to a Codex session under the same name, and a sampled skill's content
      matches what the Claude session sees exactly (R8).
- [ ] A Codex session started in a freshly prepared repository can create a
      file there on its first attempt, with no setup command run by the
      developer beforehand (R9).
- [ ] An interactive Codex session starts in a prepared repository, from the
      repository root and from a nested directory, without any trust or
      review prompt (R10).
- [ ] `git status` in every cloned repository of a prepared instance reports
      a clean working tree, immediately after `niwa apply` and again after a
      Codex session has run there (R11).
- [ ] `niwa create` and `niwa apply` complete without reading or writing the
      developer's Codex credential or login files, verified in a sandboxed
      home with sentinel files in place (R13).
- [ ] `niwa apply` on a workspace whose config declares the per-workspace
      agent setting succeeds with no migration step, and the resulting
      instance serves both agents per the criteria above (R14).
- [ ] `niwa apply` on a workspace that has never declared any agent setting
      also produces an instance serving both agents (R1, R14).
- [ ] Re-running `niwa apply` on an already-prepared instance leaves all of
      the above criteria still passing (R3).

## Out of Scope

Each exclusion is settled in the upstream BRIEF's Scope Boundary; the reasons
are summarized here and detailed there.

- **Codex background dispatch.** `niwa dispatch` remains Claude-only and its
  existing refusal for Codex stands. Making dispatch agent-agnostic is
  separate downstream work.
- **Ephemeral session provisioning for Codex.** The per-session instance
  lifecycle (provisioning hooks, the reaper and its liveness proxy) is not
  extended to Codex sessions.
- **Codex credentials and authentication.** niwa binds no API key for Codex
  and never touches the developer's Codex login (see Decisions and
  Trade-offs). Re-homing the existing secret-binding table is tracked
  separately as niwa#228.
- **Codex hook injection.** niwa injects hooks for Claude today; the Codex
  side ships none (see Decisions and Trade-offs).
- **Renaming or re-homing the existing config tables.** This feature changes
  behavior, not the config schema's shape.
- **Changes to the workflow-skills plugin or the orchestration engine.**
  Skills are delivered as they exist; nothing is rewritten for Codex. Named
  subagent types don't exist under Codex and this feature doesn't add them.
- **Changes to how Claude Code sees the workspace.** Claude's context,
  settings, and skills are untouched (R2).
- **The cross-repo context gap.** An agent that starts in one repository and
  crosses into another still doesn't pick up the second repo's context
  mid-session; that's tracked separately as niwa#247. This feature improves
  the Codex side incidentally but doesn't claim to close it.

## Decisions and Trade-offs

- **Codex hook injection is out of scope.** The upstream BRIEF carried this as
  an open question and closed it as an exclusion. Two reasons. First, nothing
  niwa ships today needs a Codex-side hook, so carrying the machinery would
  deliver nothing. Second, and decisively: an interactive Codex session blocks
  on a review prompt for a hook it cannot verify, while a background run
  merely skips it, so injecting hooks would put a blocking prompt directly in
  the path of the clean interactive start this feature exists to deliver
  (R10). The alternative, shipping hook injection now because the mechanism is
  understood, was rejected: it buys nothing today and costs the clean start.
  The capability can be added by the increment that first needs a Codex-side
  hook.

- **niwa binds no API key for Codex.** The upstream BRIEF carried this as an
  open question and closed it as an exclusion. With a working subscription
  login, an exported API key is inert: the session uses the login and never
  contacts the metered endpoint. With a broken login, the same key silently
  becomes a metered fallback behind a green health check, turning a loud
  failure into a quiet billed one. Leaving the key unbound keeps that failure
  loud, which is the safer default for a tool that prepares workspaces
  unattended. The alternative, binding the key for parity with the Claude
  side, was rejected for exactly that silent-fallback risk. The existing
  secret-binding table's own problems are tracked separately as niwa#228.
