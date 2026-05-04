# Lead: niwa apply branch cleanup behavior

## Findings

### What "clean" means in `niwa apply`

"Clean" is defined at `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-3/public/niwa/internal/workspace/sync.go:37`:
- Working tree with no staged or unstaged changes (`git status --porcelain` returns empty)

This is checked by `InspectRepo()` at line 32-37. The check is purely about git index/working directory state, NOT about branch position.

### What happens when a repo is NOT clean

At `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-3/public/niwa/internal/workspace/sync.go:122-124`, if working tree is dirty:
```
if !status.Clean {
    return SyncResult{Action: "skipped", Reason: "dirty working tree"}, nil
}
```

The repo is **skipped silently** during `niwa apply`. No error, no pull, the repo stays exactly where it is.

### What happens when a repo is on a non-default branch (the stranded case)

At `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-3/public/niwa/internal/workspace/sync.go:126-131`:
```
if !status.OnDefault {
    return SyncResult{
        Action: "skipped",
        Reason: fmt.Sprintf("on branch %s, not %s", status.CurrentBranch, defaultBranch),
    }, nil
}
```

The repo is **also skipped** without any branch-reset logic. This is the stranded-on-feature-branch case.

### Control flow for sync during apply

At `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-3/public/niwa/internal/workspace/apply.go:1390-1401` in `cloneWorker()`:
1. If repo was freshly cloned (`cloned == true`), no sync happens at all
2. If repo already existed (`cloned == false`) and `--no-pull` is not set, call `SyncRepo()`
3. `SyncRepo()` runs `FetchRepo()` first to sync refspecs from remote
4. Then `InspectRepo()` checks both Clean and OnDefault status
5. If either check fails, returns "skipped" with reason
6. If both pass AND repo is not ahead/behind, either pulls or reports "up-to-date"

The skipped result is returned but **NOT treated as an error** — it's a deferred warning at best (line 1393).

### Is there any path that resets repos to main?

**No.** Search across the codebase reveals:
- `clone.go`: only does `git clone` with optional `--branch` at checkout time
- `sync.go`: only runs `git fetch` and `git pull --ff-only`; no `git checkout`, `git reset`, or `git switch`
- `apply.go`: no branch management after cloning
- `cli/apply.go`: no branch reset logic

**The only place branch is set is at clone time**, via `CloneWithBranch()` at `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-3/public/niwa/internal/workspace/apply.go:1379`:
```go
cloned, err := a.Cloner.CloneWithBranch(ctx, job.cloneURL, job.targetDir, job.branch, noop)
```

But this only applies to fresh clones. For existing repos, the branch is never touched.

### Does `niwa go` have branch-related assumptions?

No. `niwa go` (at `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-3/public/niwa/internal/cli/go.go`) is purely a navigation command. It:
- Locates the workspace root or repo directory via registry/instance discovery
- Never touches git state
- Makes no assumptions about branch position

### What is `niwa apply` actually applying?

From `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-3/public/niwa/internal/cli/apply.go:50-66`:
- Discovers workspace configuration
- For each managed repo: clones missing repos and pulls latest changes into existing repos that are **both clean AND on default branch**
- Installs workspace content (CLAUDE.md, env, settings, hooks) into repos

The branch state itself is **not managed** — it's a **precondition** for applying content. If a repo is on a feature branch, content installation is deferred (no error, just skipped pulls).

### Existing handling of the "stranded on feature branch" case

At `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-3/public/niwa/internal/cli/apply.go:50-66`, the help text explicitly documents the skip condition:

```
Repos with uncommitted changes or on non-default branches are skipped with a warning.
```

This is **documented behavior**, not a bug. However:
- The warning is only emitted by `SyncRepo()` returning `Action: "skipped"` 
- The cloneWorker treats this as a deferred sync warning (line 1392-1393)
- There is **no forced return to main** and no error if a repo is stranded

## Implications

**Worktrees would fix the stranded-on-feature-branch case, but with a major caveat:**

The problem is NOT that `niwa apply` fails to reset repos — it's that it **doesn't try**. The current design assumes:
1. Fresh clones go to the configured default branch
2. Existing repos stay where they are unless they're clean and on default
3. If you leave a repo on a feature branch, subsequent applies just skip it

A worktree model would fix this by:
- Keeping the main clone on `main` (always)
- Giving each active session its own worktree (initially on `main`)
- When a session changes branches, only that worktree is affected
- The main clone remains pristine for the next `niwa apply`

**But this shifts the problem**: instead of "stranded on feature branch in shared clone," you get "session's worktree never returns to main." A worktree per session solves the coordination problem (main always clean for new applies), but a session-lifecycle manager would need to **explicitly reset worktrees to main when a session closes** or switches contexts.

## Surprises

1. **No branch reset logic exists at all.** The codebase has no `git checkout main`, `git reset`, or `git switch` for workspace repos. The only branch manipulation is at initial clone time.

2. **The skip is silent on pull failure.** At `/home/dangazineu/dev/niwaw/tsuku/tsukumogami-3/public/niwa/internal/workspace/apply.go:1392-1393`, when `SyncRepo()` returns `"skipped"`, it only emits a deferred warning if the action was `"fetch-failed"`. A skipped pull due to wrong branch produces no warning at all — it's silently collected in `syncWarn` and deferred, with no guarantee it reaches the user unless they check stderr carefully.

3. **"Clean" is narrowly defined.** It's only about uncommitted changes (`git status --porcelain`), not branch tracking state, divergence, or detached HEAD. A detached HEAD with no local edits is "clean" by this definition.

4. **The "only merges clean" constraint doesn't exist in code.** The help text says "Repos with uncommitted changes or on non-default branches are skipped with a warning," but there's **no special treatment for merge-in-progress or rebase-in-progress states**. Git treats those as dirty, so they'd be caught by the `!status.Clean` check anyway.

## Open Questions

1. **How do users discover they're stranded?** The skip warning is deferred and not guaranteed to reach stderr. Should `niwa status` surface this explicitly?

2. **Is there a reset-to-main command?** The codebase has no `niwa reset` for individual repos. There's a `niwa reset` CLI command (`/home/dangazineu/dev/niwaw/tsuku/tsukumogami-3/public/niwa/internal/cli/reset.go`), but need to check if it resets branches or just clears instance state.

3. **How would a worktree model integrate with multi-instance layouts?** If the main clone stays on main, does each instance get one worktree, or does each session get its own? The proposal says "session-managed lifecycle," which implies per-session worktrees, but that scales O(sessions) not O(instances).

4. **What triggers the need to return to main?** In the worktree model, is returning to main on `niwa apply` of a **different** instance, or on explicit session close, or both?

## Summary

`niwa apply` **does not reset repos to main**; it skips pulling on repos that are either dirty or on non-default branches, leaving them stranded without trying to recover them. The skip condition checks `OnDefault` (current branch == configured default) at sync time, but there is zero code that enforces or resets the branch — the only branch manipulation happens at clone time for fresh repos. A worktree model would fix the coordination problem (keeping main clone always on main) but **shifts the recovery burden to a session-lifecycle manager** that must explicitly reset worktrees when closing or switching contexts, since the worktree itself has no mechanism to auto-recover on idle.

