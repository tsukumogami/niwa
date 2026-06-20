# Lead: How do Claude Code's in-repo `.claude/worktrees/` worktrees and niwa's workspace-root `.niwa/worktrees/` worktrees collide?

## Findings

### 1. Worktree Placement, Branch Naming, and Git Operations

**niwa's placement:**
- Worktrees are placed at `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/` where `<instanceRoot>` is the workspace instance root (not inside any individual repo directory).
- Each worktree is a git worktree added to the underlying repo via `git -C <repoPath> worktree add <wtPath> -b <branch>` (worktree.go:207).
- Branch naming: By default branches are `session/<sid>` where `<sid>` is an 8-character lowercase hex ID. The `BranchPrefix` parameter in `CreateSessionParams` allows overriding this (e.g., `niwa-bootstrap/<sid>` for bootstrap orchestrator). The chosen branch is persisted in `SessionLifecycleState.BranchName` on disk.
- Session state is recorded at `<instanceRoot>/.niwa/sessions/<sid>.json` (session_lifecycle.go:78, 100).

**Claude Code's placement (from documentation and discovery code):**
- Claude Code places worktrees at `.claude/worktrees/` inside the repo directory (i.e., relative to the repo root).
- Claude Code's `EnterWorktree` tool uses built-in git logic for git repos (not hooks — worktree hooks only fire for non-git VCS per the scope doc).
- `DiscoverClaudeSessionID()` (session_discovery.go:26–56) is the discovery mechanism: it tries CLAUDE_SESSION_ID env var, then ~/.claude/sessions/<ppid>.json PPID walk, then ~/.claude/projects/<encoded-cwd>/ project scan.

**Key difference:** niwa places worktrees at the workspace-instance level (`<instanceRoot>/.niwa/worktrees/`), while Claude Code places them in-repo (`<repo>/.claude/worktrees/`). Both call `git worktree add` against the SAME underlying git repository.

---

### 2. Git Worktree Collision: Multiple Checkouts of the Same Branch

**The collision mechanism:**

Git's worktree system maintains the invariant that **only one worktree can check out a given branch at a time**. This is enforced by `.git/worktrees/` lockfiles. When both niwa and Claude Code independently create worktrees, both call `git worktree add` against the same repo:

```
git -C <repoPath> worktree add <niwa_path> -b <session/XXXX>    # niwa creates worktree #1
git -C <repoPath> worktree add <claude_path> -b <some-branch>   # Claude Code tries to create worktree #2
```

**If the two systems try to check out the same branch, the second `git worktree add` fails** with "fatal: 'branch' is already checked out elsewhere." This is the hard collision.

**`git worktree list` output when both exist:**
- If they check out different branches: both appear in `git worktree list` (one from niwa, one from Claude Code).
- If they attempt the same branch: the second attempt errors and the worktree is not created.

**Pollution and state drift:**
- niwa records session state in `<instanceRoot>/.niwa/sessions/<sid>.json` with the worktree path and branch name.
- Claude Code creates worktree state at `.claude/worktrees/` inside the repo, not accessible to niwa's session tracking.
- `.claude/worktrees/` directories created by Claude Code show as untracked files when the parent repo's `.git/info/exclude` doesn't cover them (niwa's worktrees manage this via `gitexclude.EnsureRepoExclude()`; Claude Code's in-repo worktrees are not covered by niwa's gitignore logic).
- Two worktrees on diverging branches can accumulate independent commits, and niwa's destroy (`git branch -d` vs `-D` logic) has no visibility into Claude-created worktree branches.

**State file drift:**
- niwa's lifecycle state lives at `<instanceRoot>/.niwa/sessions/` and is consulted by `niwa worktree list`, `attach`, `destroy`, `apply`.
- Claude Code's attach/detach state lives at `<repo>/.claude/worktrees/` (from Claude Code's perspective, not visible to niwa).
- An orphaned Claude-created worktree (no niwa session state) cannot be managed by `niwa worktree destroy`.
- Conversely, if niwa destroys a worktree that Claude Code thinks is still active, Claude Code's state becomes stale.

**Concrete failure modes:**
1. User starts `niwa worktree create niwa "feature"` → creates at `<instance>/.niwa/worktrees/niwa-abc123/` on branch `session/abc123`.
2. Later, user accidentally (or via an agent) runs Claude Code's `EnterWorktree` inside that repo → Claude Code tries to create a new worktree at `.claude/worktrees/...`.
3. If Claude Code picks the same branch, `git worktree add` fails. If Claude Code picks a different branch, two worktrees exist pointing to the same repo, each checking out different state. Commits in one don't appear in the other until merged.
4. `niwa worktree list` shows only the niwa-created session; Claude Code's worktree is invisible. `niwa worktree destroy abc123` will succeed (removing the niwa worktree) but leave the Claude worktree behind, orphaned.
5. Next `niwa apply` may not know to clean up the abandoned Claude worktree, leaving `.claude/worktrees/` visible in `git status`.

---

### 3. Cleanup and Lifecycle Drift

**niwa's perspective:**
- `niwa worktree list` reads from `<instanceRoot>/.niwa/sessions/` only (session_lifecycle.go:116, `ListSessionLifecycleStates`).
- `niwa worktree destroy` calls `git worktree remove --force` and `git branch -d/-D` (worktree.go:330, 344) using the branch name from the session state file (`EffectiveBranchName()`).
- If a Claude-created worktree exists, niwa has no record of it and cannot destroy it.

**Claude Code's perspective:**
- When Claude Code creates a worktree in-repo, it records state in `.claude/worktrees/` (or similar, exact location TBD by Claude Code internals).
- Claude Code's `ExitWorktree` cleans up its own worktree directory but has no knowledge of niwa's session state files.
- If niwa creates a session, then Claude Code creates an overlapping worktree, and then Claude Code's exit fires, it will not consult niwa's session lifecycle to coordinate cleanup.

**Scenarios:**
- **Orphaned niwa session:** Claude Code is running inside a niwa-created worktree, Claude Code crashes before calling `ExitWorktree`, niwa session remains active in `<instanceRoot>/.niwa/sessions/abc123.json` even though the worktree directory still exists but is "orphaned" from Claude's perspective.
- **Orphaned Claude worktree:** niwa creates a session, user runs Claude Code's `EnterWorktree` in the same repo (creating a Claude worktree on a different branch), then `niwa worktree destroy` removes the niwa session. Claude worktree is left behind, invisible to niwa, blocking future `niwa apply` or causing `git status` pollution.

---

### 4. Documented Lifecycle and Claude Code Interaction

**niwa's documented worktree lifecycle (docs/guides/worktree.md:41–65):**
- `niwa worktree create <repo> <purpose>` → branch `session/<id>`, worktree at `<instance>/.niwa/worktrees/<repo>-<id>/`, CLAUDE content installed, state file written.
- `niwa worktree apply <id>` → re-sync content (idempotent).
- `niwa worktree destroy <id>` → mark ended, remove directory, delete branch (with merge check).
- State file remains on disk after destroy so `niwa worktree list --status ended` still shows closed worktrees.

**Claude Code interaction mentioned:**
- "Installs the owning repo's CLAUDE content into the worktree, the same class of accessories `niwa apply` installs" (worktree.md:18).
- Writes `.claude/rules/worktree-imports.md` that @imports workspace-context.md (worktree.md:23–24).
- "If shell integration is active, the shell navigates into the new worktree directory on success" (worktree.md:36).
- Worktree is launched as its own Claude Code project root with workspace context imported (worktree.md:24).

**No documented conflict resolution or coordination with Claude's `EnterWorktree`.**

---

### 5. Reconciliation Code in niwa

**Search result:** No reconciliation or detection of foreign (Claude-created) worktrees exists in the current codebase.

- `worktree.go` has no code that detects or skips Claude-created worktrees.
- `session_discovery.go` detects Claude session IDs (for logging or session tracking), but does not scan `.claude/worktrees/` or reconcile with Claude worktree state.
- `CreateSession` validates the repo exists (findRepoInWorkspace) but does not check for pre-existing Claude worktrees or blocks their creation.
- No "reconcile foreign worktrees" function exists.

---

### 6. Workspace Root Settings Installation (Potential Integration Surface)

**`InstallWorkspaceRootSettings()` (workspace_context.go:242):**
- Called during `niwa apply` to build and write `<instanceRoot>/.claude/settings.json`.
- Merges discovered hooks, copies hook scripts to `.claude/hooks/`, resolves env vars, and builds a settings JSON document.
- Hook scripts are discovered from the config repo and installed to `<instanceRoot>/.claude/hooks/<event>/`.

**Potential for worktree policy injection:**
- Settings can declare `permissions.deny: ["EnterWorktree", "ExitWorktree"]` to block Claude Code's worktree tools.
- Settings can declare hooks (though "WorktreeCreate" / "WorktreeRemove" only fire for non-git VCS per the scope doc).
- No current code in `InstallWorkspaceRootSettings()` writes worktree-denial rules or delegates-to-niwa guidance.

**Gap:** The infrastructure to write policies automatically exists, but the policy content does not.

---

## Implications

1. **Two systems, one repo:** niwa and Claude Code each maintain independent worktree directories (workspace-level vs. in-repo), each calling `git worktree add` against the same repo. The git invariant (one branch = one worktree) becomes a collision point.

2. **State fragmentation:** Session state is fragmented across two independent registries (`<instanceRoot>/.niwa/sessions/` and Claude Code's `.claude/worktrees/`), with no shared view.

3. **Cleanup asymmetry:** niwa can destroy its own sessions but leaves Claude-created worktrees orphaned. Claude Code's exit doesn't know about niwa sessions.

4. **No detection:** niwa has zero visibility into Claude-created worktrees and no code path to detect or coordinate with them.

5. **Integration surface exists but is unused:** `InstallWorkspaceRootSettings()` can write policies (deny rules, guidance text) to the workspace-root `.claude/settings.json` on every `niwa apply`. This infrastructure can inject niwa-first steering or blocking rules without niwa changes.

---

## Surprises

1. **niwa worktrees are workspace-rooted, not in-repo:** I expected niwa to use in-repo `.niwa/worktrees/` (parallel to Claude's `.claude/worktrees/`). Instead, niwa centralizes all worktrees at `<instanceRoot>/.niwa/worktrees/` with session state at `<instanceRoot>/.niwa/sessions/`. This is architecturally cleaner (one place to list all active sessions) but creates a second placement tier that Claude Code is unaware of.

2. **Branch state is fully recorded on disk:** The `BranchName` field in `SessionLifecycleState` captures the exact branch (including bootstrap-prefix variants). This allows destroy and warning paths to resolve to the right ref even after the worktree is gone. It's load-bearing for multi-prefix support but not documented as such.

3. **Reconciliation is not even a stub:** I expected at least a `TODO` or skipped code path for detecting foreign worktrees. There's nothing. The answer to "what if Claude Code creates a worktree?" is "niwa doesn't know."

---

## Open Questions

1. **Can `git worktree list` be parsed to detect foreign worktrees?** Yes, `git worktree list --porcelain` exists (undocumented but functional in practice), but no niwa code uses it. Could a future `niwa worktree destroy --force` command scan for orphaned non-niwa worktrees and warn or remove them?

2. **What branch names would Claude Code pick?** Claude Code's in-repo worktree creation likely auto-generates branch names (or uses a fixed naming scheme). If Claude Code always uses a different prefix than `session/`, branch collision might be rare. But the scope doc treats it as a hard collision, so Claude's naming is either unpredictable or shares the `session/` prefix.

3. **Can worktree hooks be made to fire in git repos?** The scope doc says hooks only fire for non-git VCS. Is this a Claude Code limitation, or a documented design? If hooks could be extended to git repos, true delegation (worktree create hook → `niwa worktree create`) would be possible.

4. **What's the cost of denying EnterWorktree/ExitWorktree?** A one-line `permissions.deny: ["EnterWorktree", "ExitWorktree"]` in workspace-root settings would prevent the collision entirely, but would also block legitimate use of Claude's worktree tool outside niwa-managed workspaces. Is this acceptable?

---

## Summary

niwa places worktrees at `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/` on branches like `session/abc123`, recording state at `<instanceRoot>/.niwa/sessions/<sid>.json`. Claude Code places worktrees at `<repo>/.claude/worktrees/` and maintains state inside the repo, calling `git worktree add` on branches of its own choosing. Both systems call `git worktree add` against the same underlying repo, creating a hard collision if they attempt the same branch and leaving orphaned worktrees if they diverge. niwa has no visibility into Claude-created worktrees, no reconciliation code, and no automatic policy to prevent collision; the integration surface exists (workspace-root `.claude/settings.json`) but is unused.
