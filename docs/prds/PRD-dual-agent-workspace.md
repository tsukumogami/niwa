---
schema: prd/v1
status: Accepted
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

Accepted

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

The upstream BRIEF's User Outcome section carries the full picture of what a
developer experiences; the goals below state what this PRD holds the
implementation to.

- One preparation serves both agents: `niwa create` and `niwa apply` take no
  agent choice and leave every instance ready for whichever agent the
  developer launches.
- The Claude side is a strict invariant, not a goal to approximate: what a
  Claude session sees is byte-for-byte what it sees today.
- The Codex side reaches parity where it was second-class: every context
  layer niwa composes, the same skills, sessions that work from any directory
  and can act immediately.
- The safety properties hold throughout: cloned repositories stay git-clean,
  and the developer's own Codex setup stays theirs.

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
  extra step. "Current" includes refresh: when the workspace's configured
  content changes, a re-apply delivers the new content, not yesterday's.

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
  suppress the repository's file. This holds in the degenerate case too: when
  the workspace has nothing to say at a layer, the repository's own content
  still reaches the session undiminished.

- **R7.** Every context layer present for a location SHALL reach the session
  whole; no layer may arrive truncated. This requirement names the innermost
  layer, the repository or worktree the session is standing in, because it is
  the one at greatest risk: context delivered from an outer layer (instance or
  group) SHALL NOT crowd out, truncate, or displace it.

### Functional: skills

- **R8.** The workspace's skills SHALL be available to Codex sessions with the
  same content Claude sessions see: the same set of skills, each resolvable
  under the same name, with content delivered unmodified.

### Functional: sessions that can act

- **R9.** A Codex session started in a prepared repository SHALL be able to
  write files immediately, with no per-repository setup step by the developer.

- **R10.** An interactive Codex session SHALL start in a prepared repository
  without any trust, review, or approval prompt: nothing niwa materializes
  introduces one, and the preparation is sufficient that Codex raises none of
  its own for the prepared directories. Prompts that belong to the developer's
  own Codex setup, such as a first-run login, are outside niwa's reach (R13)
  and outside this requirement.

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
  once, however they choose. Scoped, additive entries whose effect is confined
  to paths inside niwa-managed instances are consistent with this requirement;
  anything global, destructive, or touching the developer's own settings is
  not.

### Non-functional: compatibility

- **R14.** Workspaces that declare the existing per-workspace agent setting
  SHALL keep working with no migration. The setting retains its launch-time
  meaning (which agent a niwa-launched session runs) and no longer gates which
  agents' context is materialized. Workspaces that never declared it SHALL
  also be unaffected: no config change is required to get dual-agent
  preparation.

## Acceptance Criteria

Two conventions apply throughout. First, criteria phrased as what a session
"sees" or "receives" are decided offline, against the single context file
Codex's first-match rule selects for the working directory: it exists, is
non-empty after stripping whitespace, carries a distinct sentinel from each
layer that should be present, and is not shadowed by a higher-precedence
candidate. Second, checks that need a live Codex session gate on `codex`
being on PATH and skip rather than fail when it's absent, following the
repo's existing pattern for `claude`-gated scenarios; a live check is never
the sole coverage for a mechanism, except where noted for the interactive
start, where a live check carries information nothing else can.

- [ ] After `niwa create` and `niwa apply` on a workspace with no
      agent-related configuration, the context Codex selects at the instance
      root carries the instance-level workspace content (R1, R4).
- [ ] The set of paths niwa materializes for Claude today — the `CLAUDE.md`
      tree, `.claude/`, the settings file, and the skills tree — is
      content-identical between a pre-change and post-change apply of the
      same workspace config. The only tests modified are the exclusivity
      assertions in `test/functional/features/codex-agent.feature` and the
      unit tests in `internal/workspace/content_test.go` and
      `internal/workspace/root_materializer_test.go` that assert the other
      agent's file is absent; no other test is modified or deleted (R2).
- [ ] A Codex session started at least three directories deep inside a cloned
      repository, from a shell with no `NIWA_*` variables and no `CODEX_HOME`
      set, sees the instance, group, and repository context for that
      location; with the fixture repository shipping a committed context file
      in an intermediate directory, that file's content is delivered too, so
      the check exercises per-directory selection rather than a repo-root
      read (R4, R5).
- [ ] A Codex session in a worktree created by `niwa worktree` receives a
      sentinel that appears only in the instance-level content, plus the
      worktree's own framing (its repository and branch) — so a collapse to
      current-directory-only discovery fails the check (R3, R4, R5).
- [ ] In a repository that ships its own committed agent-facing context file,
      the single context file Codex selects for that directory carries both
      the workspace context and the repository's own content, no
      higher-precedence candidate shadows it, and the committed file is
      byte-identical after `niwa apply` (R6, R12).
- [ ] In a workspace that configures no context content at any layer, a
      repository shipping its own committed context file still delivers that
      file's content to a session: niwa writes no empty or whitespace-only
      file that would claim the directory's single context slot and suppress
      the repository's own (R6).
- [ ] With instance- and group-level context together exceeding 32768 bytes
      — the documented default of Codex's context budget, which is shared
      across the whole chain and consumed outermost-first — a marker at the
      end of the repository-level context still reaches the session. Offline:
      the context budget niwa's materialization declares covers at least the
      byte size of the full composed chain on disk. Live, gated: a session
      started in that repository reports the marker (R7).
- [ ] Every file niwa delivers for skills is byte-identical to its source,
      with no file added or omitted; the delivered root carries the plugin
      manifest and every `references/` and `scripts/` directory the source
      has; and every skill a Claude session sees resolves for Codex under the
      same namespace and name (R8).
- [ ] A Codex session started in a freshly prepared repository can create a
      file there on its first attempt, with no setup command run by the
      developer beforehand (live, gated) (R9).
- [ ] After `niwa apply`, the developer's Codex config carries exactly one
      per-project trust entry for each cloned repository, keyed by a path that
      resolves to that repository's actual root — a present-but-miskeyed entry
      fails — and after three successive applies the count is unchanged. A
      session in a niwa-managed worktree of that repository is trusted by the
      same entry, with no separate worktree entry written (R9, R13).
- [ ] An interactive Codex session started in a prepared repository, from the
      repository root and from a nested directory, reaches its ready state
      under a PTY with no input supplied and shows no trust, review, or
      approval prompt (live, gated). Offline: niwa has written no hook
      definitions and no hook-state entries anywhere (R10).
- [ ] `git status` reports a clean working tree in every cloned repository
      and every niwa-managed worktree of a prepared instance, immediately
      after `niwa apply` and again, live and gated, after a Codex session has
      run there (R11, R12).
- [ ] The developer's Codex credential and login files are byte-identical and
      mtime-unchanged after `niwa create` and `niwa apply`; and with the
      credential file made unreadable (mode `000`) in a sandboxed home, both
      commands still exit zero (R13).
- [ ] Given a pre-existing developer Codex config carrying the developer's
      own settings, after `niwa create` and `niwa apply` that file differs
      from its prior content only by the addition of per-project entries
      whose path keys all resolve inside a niwa instance; no pre-existing key
      is removed, reordered, or altered, and no global key that changes Codex
      behavior outside niwa instances is written — so a repository outside
      any instance discovers its own context and trust state exactly as
      before preparation (R13).
- [ ] `niwa apply` on a workspace whose config declares the per-workspace
      agent setting succeeds with no migration step — exit zero, no prompt,
      no error — and the resulting instance meets the criteria above (R14).
- [ ] In a workspace declaring the per-workspace agent setting as Codex, a
      niwa-launched session still selects Codex, and `niwa dispatch` still
      refuses (R14).
- [ ] `niwa apply` on a workspace whose config predates the agent setting
      entirely also produces an instance serving both agents (R1, R14).
- [ ] Re-running `niwa apply` on an already-prepared instance leaves every
      criterion above still passing, and append-shaped state does not
      accumulate: after three applies, niwa-managed blocks and per-project
      entries each appear exactly once (R3, R13).
- [ ] After changing the workspace's configured context content and
      re-running `niwa apply`, the new content is present in the Codex-facing
      context and the previous content is absent; the same holds for a
      worktree after `niwa worktree apply` (R3).

## Out of Scope

All eight exclusions are settled in the upstream BRIEF's Scope Boundary,
which carries the full reasoning. In one line each:

- **Codex background dispatch.** `niwa dispatch` stays Claude-only; its
  existing refusal stands.
- **Ephemeral session provisioning for Codex.** The per-session instance
  lifecycle isn't extended to Codex sessions.
- **Codex credentials and authentication.** No API key bound, no login
  touched (see Decisions and Trade-offs); the secret-binding table is tracked
  as niwa#228.
- **Codex hook injection.** None shipped (see Decisions and Trade-offs).
- **Renaming or re-homing the existing config tables.** Behavior changes;
  the schema's shape doesn't.
- **Changes to the workflow-skills plugin or the orchestration engine.**
  Skills are delivered as they exist; no Codex subagent types.
- **Changes to how Claude Code sees the workspace.** Untouched (R2).
- **The cross-repo context gap.** Tracked as niwa#247; improved
  incidentally, not closed.

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
