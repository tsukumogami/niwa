---
status: Proposed
problem: |
  After initial workspace creation, niwa has no mechanism to keep managed repos
  current. Existing repos are skipped on apply, leaving clones at their original
  commit. This forces users to manually pull each repo or recreate the workspace,
  which is especially painful when Claude sessions need fresh code in sibling repos.
---

# DESIGN: Pull Managed Repos

## Status

Proposed

## Context and Problem Statement

niwa's `apply` command clones repos that don't exist and skips repos that do.
Once a workspace is created, repos drift from their remotes with no built-in way
to refresh them. The workspace config itself is already synced via `git pull
--ff-only` in `SyncConfigDir()`, but managed repos get no equivalent treatment.

The primary use case is keeping sibling repos current for Claude sessions that
explore code across a multi-repo workspace. The solution must be non-destructive
by default -- it should never discard local work, force branch switches, or
create merge commits without explicit user intent.

Exploration surfaced four command UX approaches, a detailed state matrix for
repo edge cases, and the relationship between code freshness and TOML config
drift. The existing apply pipeline has a clear insertion point for pull logic
at `apply.go:207-215`, and the `SyncConfigDir` pattern provides a proven
template for safe pulls.

## Decision Drivers

- **Non-destructive by default**: Never lose uncommitted work or rewrite history
  without explicit opt-in
- **Low friction**: Users should be able to freshen a workspace in one operation
- **Backward compatibility**: Existing `niwa apply` behavior must not break for
  users who don't want pulling
- **Clarity of mental model**: Users should understand what "apply" does vs what
  "sync" does
- **Safe git operations**: `git fetch` + `git pull --ff-only` is the only combo
  that's non-destructive and fails cleanly
- **State awareness**: The system needs to know repo state (dirty/clean, branch,
  remote relationship) before acting

## Decisions Already Made

These choices were settled during exploration and should be treated as constraints:

- **Git strategy: fetch + pull --ff-only** -- the only non-destructive combo.
  Fetch is always safe; ff-only fails cleanly. Matches the proven SyncConfigDir
  pattern.
- **Default behavior: pull where safe, skip where not** -- only pull repos that
  are clean, on the default branch, and behind remote. Skip everything else with
  warnings. The 18-state matrix collapses to this simple rule.
- **No stash/rebase/merge in defaults** -- stashing risks conflicts on pop,
  rebase rewrites history, merge adds commits. All require explicit opt-in via
  flags.
- **TOML drift is a separate concern** -- detecting removed repos, URL changes,
  and branch changes is related but mechanically different from pulling code.
  Should be addressed with warnings by default and opt-in remediation flags.
- **Shell-out pattern, no git library** -- niwa already uses `exec.Command` for
  all git operations. No reason to add a go-git dependency.
