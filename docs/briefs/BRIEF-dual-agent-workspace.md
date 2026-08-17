---
schema: brief/v1
status: Accepted
problem: |
  niwa prepares a workspace instance for exactly one coding agent. The agent
  choice is exclusive and made at creation time: preparing for Codex replaces
  the Claude context at the levels niwa owns and skips the repository and
  worktree levels entirely. A developer who wants both agents has to pick one.
outcome: |
  Every prepared instance is usable by both agents at once, with no choice
  made at creation time. A developer runs `claude` exactly as today, and runs
  `codex` in the same instance from any directory inside it and gets the same
  workspace context and skills. The agent choice is which command they type.
motivating_context: |
  Exploration measured live against codex-cli 0.147.0 overturned the premise
  that serving both agents requires niwa to manage a per-instance Codex home.
  Codex discovers project-level context and skills on its own, so one prepared
  instance can serve both agents without niwa touching the developer's Codex
  installation, configuration defaults, or credentials.
---

# BRIEF: dual-agent workspace

## Status

Accepted

The downstream PRD owns the requirements, including the silent-failure cases
the exploration flagged as acceptance-criterion material. The downstream
DESIGN owns the delivery mechanism: where the Codex-readable context and
skills live on disk and how they are kept in sync.

Both framing questions this brief carried in Draft are now closed. An
interactive Codex session was verified to start clean in a prepared repository
-- no prompt, from the repository root and from directories beneath it -- so
the outcome above is measured rather than assumed. Hook injection resolved the
other way and moved to the scope boundary as an exclusion: an interactive
session blocks on a review prompt for a hook it cannot verify, which is the one
failure this feature cannot afford, and nothing niwa ships today needs one.

## Problem Statement

niwa's job is to prepare a multi-repo workspace instance so a coding agent can
do useful work in it: a layered context tree (instance, group, repository,
worktree) and the workspace's skills, materialized where the agent finds them.
A prior increment made OpenAI Codex a selectable alternative to Claude Code,
but it built the selection as an exclusive switch. A workspace declares one
agent, and niwa prepares the instance for that agent and not the other.

Exclusivity has a cost beyond the forced choice. Preparing for Codex today
writes Codex's context only at the instance and group levels and skips the
repository and worktree levels entirely, so the Codex side of the switch
delivers less than the Claude side does. Picking Codex means giving up context
niwa knows how to produce.

The deeper problem is that the choice is front-loaded to the moment the
developer knows least. Which agent a task calls for depends on the task: one
agent's quota runs out mid-afternoon, one model handles a particular problem
better, or the developer simply wants a second opinion on the same code. None
of that is knowable when the workspace is created. A developer with both tools
installed can't use them interchangeably in one instance; switching means
re-provisioning, maintaining a second instance of the same repos, or
hand-carrying context between the two. The workspace should not be the thing
that decides which agent gets used.

## User Outcome

A prepared instance serves both agents, always. There's no agent selection at
creation time and nothing to configure per instance: `niwa create` and
`niwa apply` leave every instance ready for whichever agent the developer
launches, and switching agents is closing one and typing the other command in
the same directory.

For Claude Code, nothing changes. The developer runs `claude` and gets exactly
the context and skills they get today.

For Codex, the instance stops being second-class. The developer runs `codex`
from any directory inside the instance -- the instance root, a directory deep
inside a cloned repository, or a git worktree -- and the session sees the
workspace context for where it's standing, at every layer niwa composes for
Claude, plus the same skills. The session can do real work immediately: no
per-repository setup ritual, and no context files showing up as untracked noise
in `git status`.

The developer's own Codex setup stays theirs. niwa records what it needs for
the directories it manages, and nothing beyond them: Codex behaves exactly as
before outside a niwa instance, its defaults are unchanged, and its credentials
and login are never read or written. Authenticating Codex is something the
developer does once, themselves, however they choose.

## User Journeys

### Mara switches agents mid-task

Mara is deep in a refactor with `claude` when her quota runs out for the day.
In the same terminal, in the same directory, she runs `codex` and keeps
working. The Codex session knows the same things about the workspace, the
repository, and its conventions that the Claude session knew, and her skills
are all present. The outcome she reaches: switching agents costs her the
conversation, not the workspace.

### Theo opens a fresh terminal deep in a repo

Theo comes back after lunch, opens a new terminal, and lands his shell three
directories deep inside one of the instance's cloned repositories, where the
code he's working on lives. He runs `codex` right there. The session picks up
the full layered context for that spot -- instance, group, and repository --
with no environment setup, no wrapper command, and no need to cd anywhere
first. The outcome he reaches: any directory inside the instance is a valid
place to start either agent.

### Iris works in a worktree

Iris uses `niwa worktree` to spin up a branch-isolated checkout for a feature.
She runs `codex` inside the worktree and the session gets what a Claude
session gets there: the workspace context plus the worktree's own framing --
which repo this is, what the branch is for. The outcome she reaches:
worktrees are first-class for both agents, not just for Claude.

### Noah upgrades a workspace that had declared an agent

Noah's workspace config declares Codex as its agent, written back when the
setting was an exclusive switch. He upgrades niwa and runs `niwa apply`.
Nothing breaks and nothing needs migrating: the setting keeps its remaining
meaning for launch-time behavior, and his instances now carry both agents'
context instead of one. The outcome he reaches: the old declaration got him
more, not a breakage.

## Scope Boundary

### IN

- Preparing every instance for both agents unconditionally: the existing
  Claude context tree stays exactly as it is, and a Codex-readable
  counterpart carries the same layered content -- instance, group,
  repository, and worktree levels.
- Delivering the workspace's skills to Codex sessions, with the same content
  Claude sessions see.
- Sessions that work from anywhere: `codex` started in any directory inside
  the instance, including nested directories of cloned repos and niwa-managed
  worktrees, sees the context for where it's standing.
- Sessions that can act: a Codex session in a prepared repository starts able
  to write files, without a per-repository setup step by the developer.
- Clean repositories: the files niwa materializes for Codex never show up in
  a cloned repo's `git status` and never overwrite anything a repository
  ships itself.
- Compatibility with the existing per-workspace agent setting: workspaces
  that declare it keep working with no migration, and it retains its
  launch-time meaning.

### OUT

- **Codex background dispatch.** `niwa dispatch` remains Claude-only; the
  existing refusal for Codex stands. Making dispatch agent-agnostic is
  separate downstream work.
- **Ephemeral session provisioning for Codex.** The per-session instance
  lifecycle (provisioning hooks, the reaper and its liveness proxy) is not
  extended to Codex sessions.
- **Codex credentials and authentication.** niwa binds no API key for Codex
  and never reads or writes the developer's Codex login. The developer
  authenticates Codex themselves, once, however they choose. Re-homing the
  existing secret-binding table is tracked separately as niwa#228.
- **Renaming or re-homing the existing config tables.** The configuration
  surface keeps its current names and locations; this feature changes
  behavior, not the config schema's shape.
- **Any change to the workflow-skills plugin or the orchestration engine.**
  Skills are delivered as they exist; nothing is rewritten for Codex. Named
  subagent types don't exist under Codex and this feature doesn't add them.
- **Any change to how Claude Code sees the workspace.** Claude's context,
  settings, and skills are untouched; this feature adds a second reader, not
  a rework of the first.
- **The cross-repo context gap.** An agent that starts in one repository and
  crosses into another still doesn't pick up the second repo's context
  mid-session; that's tracked separately as niwa#247. This feature improves
  the Codex side incidentally but doesn't claim to close it.

- **Codex hook injection.** niwa injects hooks for Claude today, so a reader
  could reasonably assume the Codex side comes with them. It doesn't. Nothing
  niwa ships currently needs a Codex-side hook, and carrying them would put a
  blocking prompt in the path of the very thing this feature delivers: an
  interactive Codex session refuses to start until the developer reviews a hook
  it can't verify, where a background run would simply skip it. The capability
  is understood and can be added by the increment that first needs one; it buys
  nothing here and costs the clean start.
