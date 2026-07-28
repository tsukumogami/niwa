# Explore scope: enterworktree-hook-bypass (niwa#221)

## Visibility

Public

## Scope

Tactical

## Topic

niwa#221 claims Claude Code's `EnterWorktree` stopped honoring niwa's per-repo
`WorktreeCreate` hook, producing niwa-invisible native worktrees. Pre-scoping
reproduction attempts on both the version the spike tested (2.1.215) and the
currently installed version (2.1.220) contradict that claim. This exploration
establishes what the real defect is and which artifact should carry the fix.

## Pre-scoping empirical results

All runs used headless `claude -p` invocations that call `EnterWorktree` and
report the verbatim result, with the resulting `git worktree list` and hook
logs inspected afterwards.

| # | Setup | Harness | Result |
|---|-------|---------|--------|
| 1 | Non-git dir, `WorktreeCreate` hook in its `.claude/settings.json` | 2.1.220 | Hook fired; `cwd` in stdin was the non-git dir; hook stdout path honored. **Q2 = yes** |
| 2 | Git repo, `permissions.deny:["EnterWorktree","ExitWorktree"]` + `defaultMode: bypassPermissions` | 2.1.220 | Tool absent from the session entirely. **Q1 = yes** |
| 3 | Git repo, `WorktreeCreate` hook in `.claude/settings.local.json` | 2.1.220 | Hook fired; no native worktree created |
| 4 | Same as 3 | **2.1.215** | Hook fired; no native worktree created. **Falsifies the spike's root cause** |
| 5 | Git repo, `WorktreeCreate` hook that exits non-zero | 2.1.215 | `EnterWorktree` failed loudly; no native fallback |
| 6 | Git repo, no `WorktreeCreate` hook | 2.1.220 | Native worktree at `<repo>/.claude/worktrees/<name>` on branch `worktree-<name>`, `locked` — the exact reported symptom |
| 7 | Real workspace, `cwd` = `public/niwa`, niwa **0.19.2** hook | 2.1.220 | Hook fired; `from-hook` failed: `claude.env: promoted key "GH_TOKEN" not found in resolved env vars` |
| 8 | Same, hook repointed at niwa built from **HEAD** | 2.1.220 | Full success: niwa worktree under `.niwa/worktrees/`, session recorded, visible to `niwa worktree list` |

## Leads (to be confirmed with the user)

Not yet fixed — awaiting the convergence conversation on how to re-frame #221.
