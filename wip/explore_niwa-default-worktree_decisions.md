# Exploration Decisions: niwa-default-worktree

## Round 1

- **Reject the issue's "delegation is blocked" premise.** Live-doc verification
  (code.claude.com/docs/en/hooks, /worktrees) shows `WorktreeCreate`/`WorktreeRemove`
  fire in git repos and "replace the default git worktree logic entirely." Option A
  (true delegation) is viable today. Rationale: the issue's blocker rested on the
  "non-git VCS only" reading, which the current docs contradict.

- **Committed approach: delegation-first with deny+steer fallback.** Primary path is
  niwa-installed `WorktreeCreate`→`niwa worktree create` and `WorktreeRemove`→detach/
  cleanup hooks so the native `EnterWorktree` tool transparently produces niwa
  worktrees. Fallback is `permissions.deny: ["EnterWorktree","ExitWorktree"]` +
  CLAUDE.md guidance for hosts/versions that don't honor the hook. Both installed by
  `niwa apply`. Rationale: best satisfies the "one mechanism, not competing ones"
  constraint while degrading gracefully.

- **Eliminate the "B as default, A as opt-in" stance.** Delegation is the primary
  mechanism, not experimental. Rationale: user explicitly prefers delegation; deny+
  steer is only a safety net.

- **Validate before designing: settings-discovery scope is the key unknown.** Whether
  the workspace-root `.claude/settings.json` niwa writes is in the hook cascade for an
  agent running *inside* a repo is unconfirmed. Resolve with a quick spike (configure a
  real `WorktreeCreate` hook, confirm Claude Code fires it from niwa's install
  location, and find the correct scope — workspace-root vs per-repo settings) before
  writing the design. Rationale: user chose "quick spike first"; this is the only
  feasibility risk left and it determines the design's install strategy.
