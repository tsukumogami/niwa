# Lead: What git operations are needed and what are their failure modes?

## Findings

### Current Git Usage in niwa

Niwa currently uses git via shell-out only (exec.Command), not libraries. Operations:

1. **git clone** (clone.go): Creates fresh repos during initial apply
   - With optional `--depth` for shallow clones
   - Handles branch/tag refs via `--branch` flag
   - Falls back to separate `git checkout` for commit SHAs
   - Assumes target doesn't exist; used only during creation
   - Context-aware via exec.CommandContext (respects cancellation)

2. **git status --porcelain** (destroy.go, configsync.go): Detects dirty working trees
   - Used to warn before destructive operations (destroy, reset)
   - Also used to prevent apply when config directory has uncommitted changes
   - Output is parsed for non-empty status

3. **git pull --ff-only origin** (configsync.go): Already implemented for config sync
   - Runs automatically during apply if config dir is a git repo with origin
   - Safe: fails if there are diverging commits (non-fast-forward)
   - Requires clean working tree (checked first unless `--allow-dirty` flag)
   - Currently only applied to workspace config, not to managed repos

4. **git remote get-url origin** (configsync.go): Checks for remote existence
   - Returns non-zero if remote doesn't exist

### Operations Needed for Pull-Managed-Repos

For keeping managed repos fresh with non-destructive behavior:

| Operation | Use Case | Safety | Implementation Status |
|-----------|----------|--------|----------------------|
| **git fetch** | Update remote refs without touching working tree | Safest: always succeeds if network works | Not implemented |
| **git pull --ff-only** | Fast-forward pull, fails on divergence | Safe: explicit failure on conflicts | Model exists (configsync) |
| **git pull --rebase** | Rebase local commits onto remote | Medium: rewrites local history | Not recommended; risky |
| **git merge** | Explicit merge after fetch | Depends: creates merge commits or fails | Not recommended; adds commits |
| **git status** | Detect uncommitted changes | Safe: read-only | Already used |
| **git log** | Check current branch, commits behind | Safe: read-only | Not implemented |
| **git branch -vv** | Show tracking info | Safe: read-only | Not implemented |

### Failure Modes and Recovery Paths

#### git fetch (proposed default for initial sync)
- **Failure modes:**
  - Network unreachable (remote down, no internet): command fails; no local state changed
  - Invalid remote URL: fails immediately; no local state changed
- **Recovery:** Retry on network restore; doesn't require state cleanup
- **Non-destructive:** Yes. Fetch only updates `.git/refs/remotes/origin/*`; working tree unchanged
- **Recommendation:** Safe to use without user interaction

#### git pull --ff-only (current config model, proposed for repos)
- **Failure modes:**
  - Non-fast-forward (local commits ahead or diverged): fails with "not possible to fast-forward"; working tree unchanged
  - Network error: fails; working tree unchanged
  - Uncommitted changes: fails with "Your local changes would be overwritten"; working tree unchanged
- **Recovery:**
  - Diverged commits: user must manually rebase, merge, or reset
  - Uncommitted changes: user must commit or stash
  - Network: retry
- **Non-destructive:** Yes when it succeeds; fails safely when conditions aren't met
- **Recommendation:** Good for repos on default branch with clean working tree

#### git pull --rebase (not recommended)
- **Failure modes:**
  - Rebase conflicts: leaves repo in rebase state (`.git/rebase-merge/`); user must resolve or abort
  - Uncommitted changes: fails before starting
- **Recovery:** `git rebase --abort` returns to pre-rebase state; conflicts need manual resolution
- **Non-destructive:** Only if no conflicts; otherwise requires user intervention
- **Recommendation:** Too risky for automatic pull without conflict resolution strategy

#### git merge (not recommended)
- **Failure modes:**
  - Merge conflicts: stops with unmerged paths; requires manual resolution or abort
  - Uncommitted changes: fails before starting
- **Recovery:** `git merge --abort` aborts; conflicts need manual resolution
- **Non-destructive:** Only if no conflicts; otherwise creates intermediate state
- **Recommendation:** Creates merge commits and adds complexity

#### git stash + pull + git stash pop (medium risk)
- **Failure modes:**
  - Stash pop conflicts with changes: stash remains; requires manual cleanup
  - Pull fails after stash: stash is left on stack; pull already failed
  - Stash pop succeeds but with conflicts: requires resolution
- **Recovery:** `git stash drop` if corrupted; manual conflict resolution
- **Non-destructive:** No; moving uncommitted work around is destructive in spirit
- **Recommendation:** Avoid for unattended pull; user expects uncommitted changes to be preserved exactly as-is

#### git checkout <branch> (context-losing risk)
- **Failure modes:**
  - Branch doesn't exist: fails
  - Uncommitted changes block switch: fails with "Your local changes..."
  - Branch lost if previous was detached HEAD: not a failure, but loses context
- **Recovery:** `git reflog` to find lost commits; won't help if intentional checkout
- **Non-destructive:** Mostly; loses branch context but preserves working tree
- **Recommendation:** Document that pull-managed-repos doesn't change branches; warns if on non-default

### Non-Destructiveness Scoring

**Strictly non-destructive** (can be retried; will never lose user work):
- git fetch (updates refs only)
- git status (read-only)
- git log (read-only)
- git remote get-url (read-only)

**Conditionally non-destructive** (safe if preconditions met):
- git pull --ff-only (safe if clean + no divergence; fails otherwise)

**Destructive or risky** (loses work or requires recovery):
- git pull --rebase (rewrites history; conflicts are hard)
- git merge (adds commits; conflicts require resolution)
- git stash/pop (moves uncommitted changes; conflicts on pop)
- git checkout (loses branch context; might lose commits if detached)

### Current niwa Pattern

niwa uses shell-out (exec.Command/exec.CommandContext) consistently:
- Stdout/Stderr piped to os.Stdout/os.Stderr for user visibility
- Error handling via cmd.Run() or cmd.Output()
- No async/parallel execution (sequential)
- No git library used (go-git, gitea/git, etc.)

This pattern is simple, transparent, and fits the pull-managed-repos use case well.

## Implications

### Recommended Strategy: Multi-Step, Fetch-First Approach

**Default behavior for pull-managed-repos:**

1. **git fetch** (no preconditions; always safe)
   - Updates remote refs silently
   - Fails only on network/permission issues (same as clone would fail)
   - User sees fetch output; understand what's available remotely

2. **Check local state** (read-only gates)
   - `git status --porcelain` to detect uncommitted changes
   - `git log --oneline -n 1` to show current commit
   - `git rev-list --left-right --count origin/DEFAULT...HEAD` to show commits ahead/behind
   - Decision tree:
     - If dirty: warn, suggest user commit/stash, skip pull
     - If ahead of remote: warn, suggest rebase/reset, skip pull (or allow with flag)
     - If behind: safe to pull

3. **git pull --ff-only origin DEFAULT-BRANCH** (only if safe)
   - Only run if checks pass
   - Fails gracefully if diverged (non-ff)
   - Leaves working tree untouched on failure

**Flags for override:**
- `--force`: skip uncommitted-changes check (but not ahead-of-remote check)
- `--rebase`: use `git pull --rebase` instead (user accepts history rewriting)
- `--merge`: use `git merge` instead (user accepts merge commits)

**Recovery:**
- On pull --ff-only failure: error message with suggestion (`git rebase`, `git reset`, `git pull --rebase`)
- On fetch failure: retry guidance (network timeout? auth issue?)
- All failures leave working tree in valid state; no cleanup needed

### Why This Works for Sibling Repo Exploration

Claude sessions see the latest code because:
1. **Fetch keeps remotes fresh** without touching user's commits
2. **Safe pull (--ff-only) respects user's work** on diverged branches
3. **Warnings make intent explicit** (can't silently rewrite history)
4. **Fails safely** if preconditions aren't met (no data loss)

This matches the core requirement: **non-destructive, with sensible defaults and override options for edge cases.**

## Surprises

1. **niwa already has --allow-dirty flag on apply**, showing prior thought about dirty config dirs. But pull-managed-repos (repo syncing) is orthogonal to this.

2. **git pull --ff-only is the right fit**: Already proven in configsync.go, simple failure modes, explicit about not rewriting history.

3. **No git library dependency** is intentional; shell-out is transparent and matches niwa's minimal-deps philosophy.

4. **Destroy command already checks `git status --porcelain`** to warn before deleting repos. Pattern is established.

5. **Instance state tracks "Cloned: true/false"** but NOT current branch or commit. Pulling assumes default branch; non-default branches are out of scope for unattended sync.

## Open Questions

1. **What is the default branch per repo?** niwa doesn't track it; assumes `origin/HEAD` symlink is accurate. Is this assumption safe across GitHub/GitLab/Gitea?

2. **Scope: pull all repos or only cloned ones?** Current instance state only marks repos as Cloned (during apply). Should sync also pull repos in groups that were skipped during create (e.g., repos with branch checkouts)?

3. **Timing: part of apply or separate command?** Scope document mentions both options. Design impact is whether `niwa apply` auto-pulls or user runs `niwa pull` separately.

4. **Shallow clones (--depth):** niwa supports them. Does pull work on shallow clones? Does it need special handling?

5. **Authentication:** niwa inherits git auth (SSH keys, OAuth tokens). Does pull need special setup, or does it just work if clone worked?

6. **Monorepo repos (multiple checkouts from same URL)?** State tracks repos by name; clone URL can repeat. Pull would run once per instance.

7. **Error reporting:** Should pull failures block apply, or should they warn and continue? Current apply collects errors per instance; can pull errors follow the same pattern?

## Summary

Niwa needs git fetch (safe, always) followed by conditional git pull --ff-only (safe, when preconditions met). This is non-destructive: fetch never changes local work, pull --ff-only fails safely if there's divergence or uncommitted changes. The implementation should follow niwa's existing pattern: shell-out with precondition checks (dirty tree, commits ahead) and clear error messages guiding recovery. This approach ensures Claude sessions see fresh code while respecting user commits and work-in-progress repos.
