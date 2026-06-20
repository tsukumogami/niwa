---
status: Draft
question: |
  Can niwa install a Claude Code WorktreeCreate/WorktreeRemove hook that Claude
  Code actually fires for an agent operating inside a workspace repo, and at what
  settings scope (workspace-root .claude/settings.json, per-repo .claude/settings.json,
  or user/managed) must the hook live for that to happen?
timebox: "1 session (~half a day)"
---

# SPIKE: niwa as the default worktree mechanism (Claude Code hook delegation)

## Status

Draft

## Question

Can niwa install a Claude Code `WorktreeCreate` / `WorktreeRemove` hook that Claude
Code actually invokes when an agent creates a worktree while operating inside a
workspace repo, and at what settings scope must that hook be installed for Claude
Code to honor it?

The answer is go/no-go for routing the native `EnterWorktree` tool to `niwa worktree
create`, and it determines where niwa must write the hook.

## Context

In a niwa-managed workspace, two worktree mechanisms can coexist and compete:

- **Claude Code's built-in worktree tool** (`EnterWorktree` / `--worktree`) creates
  a bare `git worktree` under `.claude/worktrees/<name>/` at the repo root, with no
  secret materialization, no CLAUDE content sync, and no session tracking.
- **niwa's worktree lifecycle** (`niwa worktree create`) places a worktree at
  `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/` and materializes the full workspace
  context: vault-resolved secrets, repo CLAUDE content, workspace-context imports,
  git-exclude coverage, worktree hooks, and a session-lifecycle state file.

The goal is one mechanism, not two. The ideal is that the native tool transparently
produces niwa worktrees.

Claude Code's hook system makes this possible in principle. The hooks reference and
worktrees guide (code.claude.com/docs/en/hooks, code.claude.com/docs/en/worktrees)
state that a `WorktreeCreate` hook fires "when a worktree is being created via
`--worktree` or `isolation: \"worktree\"`" and "replaces the default git worktree
logic entirely" — and that this applies to git repositories, not only non-git VCS.
The hook receives stdin JSON (`session_id`, `cwd`, `hook_event_name`,
`worktree_path`, `worktree_name`), must print the worktree path to stdout, and any
non-zero exit fails creation. `WorktreeRemove` fires on session exit / subagent
finish and cannot block.

niwa already owns the install surface: `InstallWorkspaceRootSettings()`
(`internal/workspace/workspace_context.go`) writes the workspace-root
`.claude/settings.json` and installs hook scripts on every `niwa apply`, and
instance-root settings are fully generated (no user-merge conflict).

The one unverified link is **settings scope**: Claude Code creates worktrees "at
your repository root" and reads project settings from the repo's `.claude/`. It is
not yet confirmed that a hook written to the workspace-root `.claude/settings.json`
(a parent of the repo) is in the cascade an in-repo agent honors. The answer decides
the design's install strategy.

This spike unblocks a follow-on **delegation-first design doc** that will decide the
design-level questions out of scope here: the stdin -> `niwa worktree create`
adapter and repo resolution from `cwd`/`git_dir`; branch-name reconciliation
(Claude's `worktree-<name>` vs niwa's `session/<sid>`); reconciling the non-blocking
`WorktreeRemove` with niwa's dirty/attached destroy guards; and the
`permissions.deny: ["EnterWorktree","ExitWorktree"]` + CLAUDE.md guidance fallback
for hosts/versions that don't honor the hook.

## Approach

Planned investigation steps (investigation not yet started):

1. **Baseline the contract.** In a throwaway git repo, configure a minimal
   `WorktreeCreate` hook in that repo's `.claude/settings.json` that logs its stdin
   to a file, creates a worktree at a known path, and echoes that path. Trigger it
   via `claude --worktree test` and via an in-session "work in a worktree" request
   (`EnterWorktree`). Confirm the hook fires in a git repo, capture the exact stdin
   JSON, and confirm stdout-path and non-zero-exit-fails semantics.
2. **Test the scope ladder.** Move the same hook up the settings hierarchy and
   re-test which scope an in-repo agent honors:
   - per-repo `<repo>/.claude/settings.json`
   - workspace-root `<instanceRoot>/.claude/settings.json` (where niwa writes today),
     with the agent launched both from the workspace root and from inside the repo
   - user `~/.claude/settings.json` (control)
   Record, for each, whether the hook fires for an agent whose project root is the
   repo.
3. **Map to niwa's installer.** Confirm whether `InstallWorkspaceRootSettings()` can
   write the hook at whichever scope step 2 proves necessary, or whether niwa needs a
   new per-repo settings-install path. Note any settings-precedence interactions.
4. **Smoke-test `WorktreeRemove`.** Confirm it fires on exit/subagent-finish and that
   a non-zero exit does not block (informs the detach-not-destroy design decision).

Out of scope for this spike (deferred to the design): the production adapter,
branch-name policy, full reconciliation of niwa's destroy guards, and the fallback
wiring. The spike only needs enough of a hook to prove firing and scope.

## Findings

Investigation not yet started.

Established by prior exploration (carry-forward, not yet validated by hands-on test):

- Live Claude Code docs confirm `WorktreeCreate`/`WorktreeRemove` fire in git repos
  and replace default git worktree logic — contradicting the earlier assumption that
  the hooks fire only for non-git VCS. This is the basis for the spike being worth
  running at all.
- The hook stdin/stdout/exit contract is documented and fits delegation.
- niwa already materializes worktrees at the workspace instance root and already
  writes the workspace-root `.claude/settings.json` plus hook scripts on apply.

The open, untested variable is settings scope (step 2). Note for the investigator:
when a `WorktreeCreate` hook is configured, Claude Code does not process
`.worktreeinclude`, so the hook (i.e. `niwa worktree create`) must itself materialize
any local files — which niwa already does.

## Recommendation

Pending investigation.

Decision criteria for the follow-on design:

- **Go (proceed to delegation-first design)** if the hook fires for an in-repo agent
  at a settings scope niwa can write (workspace-root preferred; per-repo acceptable
  since niwa already materializes per-repo content). The design then specifies the
  adapter, branch-name policy, remove semantics, and the deny+steer fallback.
- **Go with narrowed scope** if the hook only fires at per-repo or user scope: design
  proceeds but the install strategy shifts to that scope, and the fallback's role
  grows for any repo niwa can't reach.
- **No-go on delegation (fall back to deny+steer as the default)** if Claude Code
  does not honor a niwa-installed hook at any scope niwa controls. In that case the
  committed fallback — `permissions.deny: ["EnterWorktree","ExitWorktree"]` plus
  CLAUDE.md guidance steering agents to `niwa worktree create`, installed by
  `niwa apply` — becomes the primary mechanism, and a Claude Code feature request for
  honoring worktree hooks in git repos at workspace scope is the longer-term path.
