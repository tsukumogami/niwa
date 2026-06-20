# Lead: Delegating Claude Code `EnterWorktree` to External Command

## Findings

### 1. Hook Firing Behavior and VCS System Support

**Citation:** [code.claude.com/docs/en/hooks](https://code.claude.com/docs/en/hooks) and [code.claude.com/docs/en/worktrees](https://code.claude.com/docs/en/worktrees)

**Finding:** The documentation explicitly states that `WorktreeCreate` and `WorktreeRemove` hooks are designed to work with **git-based repositories** and are triggered when a worktree is created via `--worktree` CLI flag or `isolation: "worktree"` in subagent configuration.

The hooks documentation describes them as providing "an extension point for customizing how worktrees are created and cleaned up, allowing you to override or augment git's default worktree commands." This contradicts any claim that they fire "ONLY for non-git VCS."

**Current behavior:** When **no** hooks are configured, Claude Code uses **built-in git logic** automatically. The hooks activate **only when explicitly defined** in settings.

### 2. Hook Contract: stdin/stdout and Exit-Code Semantics

**WorktreeCreate Hook Contract:**
- **stdin:** Standard hook JSON input with event-specific fields for the worktree being created (field name `worktree_name` visible in example)
- **stdout (required):** Must output the **absolute path to the created worktree** on stdout
- **Exit code:** If the hook fails or doesn't return a path, **worktree creation fails**
- **Blocking:** Synchronous — Claude Code waits for hook completion before proceeding

**WorktreeRemove Hook Contract:**
- **stdin:** Standard hook JSON input with worktree path and removal context
- **stdout:** No return value needed; used for cleanup side effects only
- **Exit code:** Exit code 2 or non-zero status is **logged in debug mode but does not prevent removal**
- **Blocking:** Synchronous — removal is synchronous

**Source documentation shows example:**
```bash
#!/bin/bash
input=$(cat)
worktree_name=$(echo "$input" | jq -r '.worktree_name')
git worktree add "/tmp/worktree-$worktree_name"
echo "/tmp/worktree-$worktree_name"
```

### 3. Worktree Detection and cwd Behavior

**Finding:** `EnterWorktree` detects git repositories and uses built-in git logic regardless of where Claude Code is launched from. The documentation does not indicate any "escape hatch" based on cwd.

**Behavior:** When running in a directory that is not a git repository, the worktree documentation explicitly states:

> "Worktree isolation uses git by default. For SVN, Perforce, Mercurial, or other systems, configure [`WorktreeCreate` and `WorktreeRemove` hooks](/en/hooks#worktreecreate) to provide custom creation and cleanup logic."

**Implication:** If Claude Code is launched from a niwa workspace instance root (a non-git directory), **`EnterWorktree` does NOT automatically switch to hook-based mode.** There is no documented "non-git fallback" trigger. The hooks must be explicitly configured.

**Target repo resolution:** The documentation does not detail how `EnterWorktree` resolves whether a target repo is git. However, the overall architecture suggests per-target-repo detection, not workspace-wide detection based on cwd.

### 4. Settings and Permissions Levers Affecting EnterWorktree/ExitWorktree

**Worktree-related settings found:**

| Setting | Type | Scope | Effect |
|---------|------|-------|--------|
| `cleanupPeriodDays` | Integer | User, Project, Local, Managed | Controls automatic removal age for orphaned subagent worktrees (default: 30 days, minimum 1) |
| `worktree.baseRef` | String (`"fresh"` or `"head"`) | User, Project, Local, Managed | Determines whether new worktrees branch from `origin/HEAD` (fresh) or local `HEAD`. Example: `{"worktree": {"baseRef": "head"}}` |

**Settings precedence order** (highest to lowest):
1. **Managed** (cannot be overridden)
2. **Command line arguments** (temporary session overrides)
3. **Local** (`settings.local.json`)
4. **Project** (`.claude/settings.json`)
5. **User** (`~/.claude/settings.json`, lowest)

**Permissions affecting EnterWorktree/ExitWorktree:** The documentation does not explicitly list specific permissions that gate these tools. The standard `permissions.allow`, `permissions.ask`, `permissions.deny` arrays apply, but no worktree-specific permission gates are documented.

**What an external manager can write:** An external process can write both `cleanupPeriodDays` and `worktree.baseRef` into `.claude/settings.json` (project scope). Managed settings require deployment of managed settings via workspace configuration (outside normal .claude/ files).

### 5. Delegation Escape Hatches

**Finding:** There is **ONE** supported mechanism to reroute worktree creation to a command: the `WorktreeCreate` hook.

**Mechanism:** Configure `WorktreeCreate` and `WorktreeRemove` hooks in `.claude/settings.json` (or any settings scope). When configured, these hooks completely **replace** the default git worktree logic:

```json
{
  "hooks": {
    "WorktreeCreate": [
      {
        "type": "command",
        "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/create-worktree.sh"
      }
    ],
    "WorktreeRemove": [
      {
        "type": "command",
        "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/cleanup-worktree.sh"
      }
    ]
  }
}
```

The hook receives JSON stdin and must output the absolute worktree path on stdout.

**Limitation:** This mechanism only works when:
- The repo is git-based (or when you want to replace non-git VCS logic)
- The hooks are explicitly configured in settings
- Claude Code has permission to invoke command hooks (no explicit gate found in docs)

There is **no setting** like `worktree.useCustomHooks` (checked) or any flag to make hooks activate conditionally based on cwd. Hooks are always on when configured.

## Implications

### Feasibility of Delegation

**Can `EnterWorktree` be made to delegate to an external command like `niwa worktree create`?**

**Yes, partially:**

1. **Via WorktreeCreate hooks (supported):** A niwa-managed `.claude/settings.json` in each git repo can configure hooks that invoke `niwa worktree create`. This delegates worktree creation to niwa while preserving Claude Code's hook contract (must return absolute path on stdout).

2. **Per-repo, not workspace-wide:** Hooks are configured per-repo (in `.claude/settings.json`). For a niwa workspace with multiple repos, each repo needs its own hook configuration, or a workspace-level hook configuration that niwa deploys to each repo.

3. **No workspace-level detection trigger:** If Claude Code is launched from the niwa workspace instance root (non-git), it does **not** automatically fall back to hooks for its target repos. Each target repo must have hooks configured independently.

4. **No "reroute EnterWorktree tool itself" escape hatch:** There is no settings lever to redirect the `EnterWorktree` **tool** to an external command or to conditionally activate hooks based on workspace context. The tool always uses built-in git logic unless hooks are configured **in that repo's settings**.

### The Dual-Worktree Problem

**The core tension:** If niwa manages worktrees via `niwa worktree create` (which might delegate to nix-portable, custom checkouts, or other logic) and Claude Code independently creates worktrees via `git worktree add`, you now have two systems that:

- May create worktrees in different locations (claude worktrees under `.claude/worktrees/`, niwa worktrees elsewhere)
- May use different branch strategies (baseRef vs niwa logic)
- May have different cleanup semantics (claude age-based sweep vs niwa's cleanup)

**Mitigation via hooks:** By configuring WorktreeCreate/WorktreeRemove in each repo's `.claude/settings.json` to invoke `niwa worktree create/remove`, you can unify the two systems **within each repo**. However, this requires:

1. **Static hook configuration** in each repo (no workspace-level override possible from outside)
2. **Trust that the hook respects the contract:** the niwa command must output the absolute worktree path on stdout in the exact format Claude Code expects
3. **No ability to gate the tool itself** — if Claude Code is in that repo, it will use the hook if configured, no escape

## Surprises

1. **Hooks fire for git repos, not just non-git VCS.** The documentation describes hooks as replacing "default git behavior," contradicting any assumption that they're only for SVN/Perforce/Hg.

2. **No conditional hook activation.** I expected a setting like `worktree.useCustomHooks` or `worktree.delegate: "niwa"` that would allow niwa to gate whether hooks activate. Such a setting does not exist.

3. **`baseRef` is a top-level worktree setting**, not buried in hooks. It's one of the few documented worktree settings and directly controls where new worktrees branch from.

4. **Non-interactive runs don't auto-cleanup.** Worktrees created with `--worktree` alongside `-p` (non-interactive) are **not** cleaned up automatically, requiring manual `git worktree remove`.

## Open Questions

1. **Exact stdin JSON schema for WorktreeCreate/WorktreeRemove:** The documentation shows `worktree_name` as an example field but does not list all fields. What other fields are present? (e.g., repo path, branch info, isolation reason)

2. **Can a WorktreeCreate hook delegate to another hook?** If niwa installs a `.claude/settings.json` with a WorktreeCreate hook, can that hook chain to another command or must it be self-contained?

3. **Workspace-level hook config in niwa:** Can niwa maintain a "hook template" or "hook defaults" that it deploys to each repo's `.claude/settings.json`, or must hooks be manually configured per-repo?

4. **EnterWorktree tool vs --worktree flag:** Do they use the same hook chain, or are there behavioral differences between the tool and CLI flag?

5. **Managed settings deployment:** Can niwa deploy managed settings via an external config manager (e.g. Vercel's managed settings model), or is managed config out of scope for Claude Code's current architecture?

## Summary

**WorktreeCreate/WorktreeRemove hooks ARE the supported delegation mechanism and fire for git repos by default; they completely replace built-in git logic when configured.** However, hooks must be explicitly configured per-repo in `.claude/settings.json` — there is no workspace-level trigger or conditional activation, and no setting to gate or redirect the EnterWorktree tool itself. **Unifying niwa and Claude Code worktrees requires configuring hooks in every repo to invoke `niwa worktree create/remove`, with the hook contract (absolute path on stdout) upheld by niwa's command.**
