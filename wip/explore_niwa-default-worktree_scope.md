# Explore Scope: niwa-default-worktree

## Visibility

Public

## Core Question

In a niwa-managed workspace, how do we make niwa's worktree mechanism the single
default so Claude Code agents don't end up with two competing worktree systems
(Claude's built-in in-repo `.claude/worktrees/` vs niwa's workspace-root
`.niwa/worktrees/`)? Ideally Claude's `EnterWorktree` tool delegates to
`niwa worktree create`; if true delegation is infeasible, deny the built-in tool
and steer agents to niwa. The non-negotiable: one mechanism, not competing ones.

## Context

- Issue tsukumogami/niwa#166 (enhancement) already investigated the Claude Code
  side: `WorktreeCreate`/`WorktreeRemove` hooks exist but per the docs fire only
  for non-git VCS (they *replace* git); in a git repo `EnterWorktree` uses
  built-in git logic and never invokes hooks. So naive hook delegation is
  blocked. A `permissions.deny: ["EnterWorktree","ExitWorktree"]` lever exists
  but is blunt. Options A (hook delegation), B (deny+steer), C (CC feature
  request) are laid out in the issue.
- Grounding finding: niwa ALREADY materializes worktrees at the workspace
  instance root. `CreateSession` (internal/worktree/worktree.go:194) places each
  worktree at `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`, adds it to the real
  repo via `git -C <repoPath> worktree add`, scaffolds `.niwa/`, records
  git-exclude coverage, and writes a session-lifecycle state file. Full
  create/apply/destroy/attach lifecycle documented in `docs/guides/worktree.md`.
- `niwa apply` / `niwa worktree apply` already writes the workspace-root
  `.claude/settings.json` via `InstallWorkspaceRootSettings()` and installs hook
  scripts — so niwa is already positioned to write worktree policy automatically.

## User Direction (scoping answers)

- **Integration goal:** ideally true delegation; deny+steer is an acceptable
  fallback only if delegation is impossible. **Hard constraint: no competing
  alternatives** — must not end with both worktree systems live.
- **Threads (all selected):** non-git-VCS hook trigger, Claude/niwa worktree
  collision, surfacing niwa's existing model, secrets/CLAUDE-sync parity.
- **Artifact:** let exploration decide.

## In Scope

- Feasibility of routing Claude Code worktree creation through niwa.
- The non-git workspace-root angle as a possible hook-trigger escape hatch.
- Collision/competition between Claude's and niwa's worktree locations.
- niwa's existing worktree value (secrets, CLAUDE sync, scaffolding) and how it's
  surfaced.
- The `niwa apply` settings/hook injection surface for worktree policy.

## Out of Scope

- Re-designing niwa's worktree lifecycle itself (create/destroy/attach work).
- Non-Claude-Code agent harnesses.
- Changes to how secrets are materialized (only how they reach worktrees).

## Research Leads

1. **Can Claude Code's `EnterWorktree` be made to delegate to an external command
   (`niwa worktree create`) in a git repo, and does running from the non-git
   workspace instance root change whether `WorktreeCreate`/`WorktreeRemove` hooks
   fire?**
   This is the crux of true delegation. Confirm the "hooks fire for non-git VCS
   only" limitation against current Claude Code behavior/docs, probe the exact
   hook contract, and test whether the non-git workspace root is an escape hatch
   or a dead end. Enumerate every settings/permissions lever that affects
   EnterWorktree.

2. **How do Claude's in-repo `.claude/worktrees/` and niwa's workspace-root
   `.niwa/worktrees/` collide, and what does "competing alternatives" look like
   concretely?**
   Map the failure mode the user wants to eliminate: double worktrees, repo
   pollution, branch/state drift, what `git worktree list` shows, and how niwa
   apply would need to reconcile or suppress one side.

3. **What does niwa's existing worktree lifecycle materialize that bare git
   worktrees don't (secrets, CLAUDE content sync, `.niwa` scaffolding,
   git-exclude), and how is it surfaced to agents/users today?**
   Establishes the value proposition that justifies the switch and the
   discoverability gap (CLAUDE.md guidance, command UX) that lets agents reach
   for the wrong tool.

4. **What is the exact mechanism by which `niwa apply` writes workspace-root
   `.claude/settings.json` and installs hooks, and what would it take to inject
   worktree policy (deny rules, delegation hooks, or guidance) automatically?**
   This is the implementation surface for option B and the dormant-hook path of
   option C. Identify where policy would be written and how it interacts with
   settings precedence.
