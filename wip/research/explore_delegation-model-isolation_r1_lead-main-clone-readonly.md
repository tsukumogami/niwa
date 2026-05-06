# Lead: Main clone as read-only artifact

## Findings

### What `niwa apply` assumes about the main clone

`niwa apply` operates on the main clone exclusively and treats it as a pull-sync target, not as a read-only artifact. The apply pipeline (`internal/workspace/apply.go` → `cloneWorker` → `SyncRepo`) calls `SyncRepo` for every already-cloned repo that is not fresh. `SyncRepo` (`internal/workspace/sync.go`) runs `git fetch`, inspects the working tree, and conditionally runs `git pull --ff-only`. The pull is skipped when any of these conditions hold:

- working tree is dirty (`status.Clean == false`)
- current branch is not the configured default (`status.OnDefault == false`)
- no upstream tracking branch exists
- repo is ahead of or diverged from remote

These are **skip conditions, not guard conditions**. Apply does not fail when a repo is on a non-default branch — it silently skips the pull and logs a warning. The `AllowDirty` field is declared on the `Applier` struct but is unused in the sync path (it was deprecated with the snapshot model; the CLI prints a deprecation notice when the flag is passed). There is no enforcement that the main clone be clean or on the default branch before or after an apply run.

Apply is also responsible for installing managed content (CLAUDE.md, hooks, settings, env files) per-repo into the main clone directories. It does this regardless of git state — even if a repo is on a feature branch, apply still materializes configuration files there. So apply has two modes of interaction with the main clone: (1) git sync (branch-and-cleanliness-gated), and (2) config file installation (unconditional).

The PRD (R15) states explicitly: "Apply operates on the main clone only." The design doc (`DESIGN-mesh-session-lifecycle.md`, Decision 4 rationale) adds: "Mixing apply semantics with session lifecycle management was rejected because it makes worktree state harder to reason about." Apply's current code matches this: session worktrees nested three levels under `<instance>/.niwa/worktrees/` are invisible to `EnumerateInstances` (scans immediate subdirectories of the workspace root) and `EnumerateRepos` (two-level scan from instanceRoot). No code changes were needed to exclude them.

### What would enforcing "main clone is read-only" require

Today there is no enforcement. The main clone is a convention-only artifact — nothing prevents a worker spawned without a `session_id` from checking out a branch, committing, or modifying files. The `SyncRepo` skip-on-dirty behavior is a sync guard, not a write guard.

To enforce read-only at the code level, options include:

1. **Guard in `niwa_delegate`:** When called without `session_id`, reject the delegation (or at minimum emit a warning) unless the caller explicitly opts out of isolation. This would need the delegate handler (`handlers_task.go:handleDelegate`) to check for the absence of `session_id` and either block or warn.

2. **Guard in the daemon's worker spawn path:** Before spawning a Claude worker in a non-session context (i.e., directly in the main clone), verify the main clone is clean and on the default branch. If not, abort or warn. The daemon spawn path is in `internal/cli/mesh_watch.go` (not read in this investigation).

3. **Filesystem-level lock:** Use a git hook (pre-commit) installed by `HooksMaterializer` that refuses commits in the main clone. This would require apply to install such a hook and would affect all users, including those who haven't adopted sessions.

4. **`instance.json` marker:** Add a field to `InstanceState` indicating the main clone should be treated as read-only, which the daemon or apply could check before proceeding. No such field exists today.

None of these guards exist. The codebase has no code that enforces read-only status on the main clone. Enforcement would require new code.

### What operations on the main clone are currently legitimate

From the codebase:

- **`git fetch`** — called unconditionally by `SyncRepo` for every non-fresh repo during apply.
- **`git pull --ff-only`** — called when the repo is clean, on the default branch, and behind the remote.
- **Config file installation** — `HooksMaterializer`, `SettingsMaterializer`, `EnvMaterializer`, `FilesMaterializer`, and content install functions write files into the repo directory.
- **Initial `git clone`** — `CloneWithBranch` clones the repo if it doesn't exist.

These are the only apply-layer operations. Notably, apply never commits or branches — it only reads, fetches, pulls, and writes config files into working trees.

In the session model, untagged `niwa_delegate` calls (no `session_id`) still target the main clone via the main instance daemon. Workers spawned this way have the main clone's root as their working directory and can freely write, commit, and branch there.

### The PRD's stated intent for the main clone

Goal 3 of the PRD states: "The main clone of every repo in a workspace always stays on `main`. All active work happens in session-specific worktrees." This is a stated invariant but not an enforced one. The PRD's backward-compatibility rationale (Decision: "No implicit sessions on untagged niwa_delegate") explicitly preserves the old behavior where a `niwa_delegate` without `session_id` runs in the main clone.

The PRD frames the session model as the mechanism that *enables* the main clone to stay clean — by routing work into worktrees — rather than as something that enforces cleanliness. The result is: the invariant holds only if all callers opt into sessions. The design trusts coordinators to use sessions; it does not police the main clone.

### Failure modes under convention-only enforcement

Without code enforcement, the following failure modes exist:

1. **Untagged delegation contaminates the main clone.** A coordinator that calls `niwa_delegate` without `session_id` spawns a worker directly in the main clone. That worker can check out branches, commit, and leave the repo on a feature branch. Apply will then skip the pull for that repo on every subsequent run.

2. **Human opens Claude directly in a repo's main clone.** Nothing prevents a human from `cd <instance>/repos/<repo> && claude`. That Claude session operates against the main clone, can commit, and can stray from the default branch. Apply skips, workspace drifts.

3. **Apply's config installation into a dirty clone.** If the main clone is on a feature branch and apply runs, managed files (hooks, settings, env) are still installed into the feature branch's working tree. They get committed on that branch, not on main, creating a divergence in config state.

4. **Session branch left in main clone.** `niwa_destroy_session` uses `git branch -d` by default, which refuses deletion of unmerged branches. Session branches accumulate in the main clone's git history. This is by design but compounds with failure mode 1: a worker on the main clone that branches from a stale feature branch produces confusing history.

5. **No recovery path without manual intervention.** The PRD notes "there is no automated path back to main" for repos stranded on feature branches. The session model fixes this for session-tagged work (worktrees are isolated), but untagged work leaves the same manual recovery problem the PRD was written to solve.

### What simplifies if the main clone is always clean

If the main clone were always on the default branch and clean (whether by convention or enforcement):

- **`SyncRepo` becomes unconditional.** The dirty/non-default-branch skip conditions would be dead code. Apply's sync phase would be simpler: fetch + pull for every repo, every time.
- **Config installation is branch-agnostic.** Managed files written into the main clone would always land on main, eliminating the edge case where apply installs hooks/settings onto a feature branch.
- **`niwa go <repo>` always lands you on main.** Navigation to the main clone would have a predictable, safe starting state.
- **Daemon's worker spawn path could assert cleanliness.** A pre-spawn check that aborts if the main clone is dirty would be a reliable guard rather than a best-effort warning.
- **Apply's `AllowDirty` field could be removed entirely.** It's already deprecated; enforced cleanliness would make the field permanently meaningless.
- **Instance state reasoning simplifies.** The `Repos` map in `InstanceState` records only URL and clone presence; if the main clone's git state is guaranteed, the state model doesn't need to track branch or cleanliness.

The primary complication is that `niwa apply` itself writes managed files into the main clone on every run. Those writes make the working tree "dirty" (untracked/modified files) if the repo doesn't gitignore them. The `*.local*` gitignore pattern installed by `EnsureInstanceGitignore` covers `.local`-infix files (hooks, settings), but content files (CLAUDE.md, workspace-context.md) are not gitignored by default. A strict cleanliness invariant would require either gitignoring all managed files or accepting that apply-generated files are not considered "dirty" for the purposes of this invariant.

## Implications

The session model provides the architectural mechanism for keeping the main clone clean but does not enforce it. The gap is the backward-compatibility decision: untagged `niwa_delegate` continues to target the main clone. If niwa has no users yet, that decision is revisable without migration debt.

Making sessions the default (or mandatory) for all delegations would close the gap at the delegation layer. It would not close the human-in-main-clone gap — that requires either a git pre-commit hook installed by apply, a shell integration warning, or documentation-only guidance.

The `AllowDirty` deprecation path already points toward a cleaner apply model. Completing that cleanup (removing the field entirely) is easier if the main-clone-always-clean invariant holds.

The observation that apply installs config files unconditionally regardless of git state is the most concrete blocker: enforcing read-only at the git level (no commits) is achievable, but enforcing "clean working tree" would conflict with apply's own behavior unless managed files are excluded from the dirtiness check.

## Surprises

1. **`AllowDirty` is a no-op field.** It is declared on `Applier` but not used anywhere in the sync path. The CLI flag that sets it prints a deprecation warning. The field has effectively been deleted from the logic while remaining in the struct.

2. **Apply installs config files even when the repo is on a non-default branch.** The skip conditions in `SyncRepo` gate only the `git pull` — not the materializer phase. Hooks, settings, and content files are written to the repo directory regardless of git state. This is an existing inconsistency: apply promises to leave non-default-branch repos alone (no pull), but silently modifies their working trees with config artifacts.

3. **The layout isolation for session worktrees was solved without code changes.** Worktrees placed at `<instance>/.niwa/worktrees/` are naturally invisible to `EnumerateInstances` (immediate-children scan) and `EnumerateRepos` (two-level scan). The layout choice did the work; no scan code was modified. This means the boundary is structural, not guarded — a worktree placed at the wrong depth would accidentally be discovered.

4. **`niwa apply` has no concept of sessions.** There is no session-awareness in any of the apply code. Session worktrees are invisible by layout convention, not by filtering logic. This makes the apply-session boundary extremely clean — but also means apply has no way to, for example, propagate config updates into existing worktrees (acknowledged as a known limitation in the PRD).

## Open Questions

1. **Is making `session_id` mandatory for `niwa_delegate` feasible at this stage?** The PRD cited backward compatibility as the reason for opt-in sessions, but niwa has no production users. What is the actual cost of flipping to sessions-by-default?

2. **How should untagged delegation behave if sessions become default?** Option A: auto-create an ephemeral session per delegation (no continuity, but isolation). Option B: require explicit `session_id`. Option C: warn but allow. Each has different coordinator prompt implications.

3. **How do humans working in the main clone get stopped or warned?** The session model doesn't address this at all. A human running `claude` inside a repo's main clone faces the exact same dirty-main problem as an untagged delegation. Should `niwa session create` be a required step before any direct work? Should the shell integration intercept `claude` invocations from inside a main clone?

4. **Does the materializer's unconditional file installation into non-default-branch clones need to be fixed separately?** Currently apply writes config files (hooks, settings, CLAUDE.md) to a repo directory regardless of what branch it's on. This is a quiet correctness problem even without the read-only invariant discussion.

5. **Should `EnsureInstanceGitignore` be extended to gitignore all niwa-managed files, not just `*.local*`?** If apply always writes into the main clone, the only way to keep the working tree "clean" after apply is to exclude those files from git status tracking.

## Summary

`niwa apply` has no enforcement of a clean main clone — it skips git pulls on dirty or non-default-branch repos but still installs config files unconditionally, and neither it nor the delegation layer prevents untagged workers from committing directly to the main clone. The PRD declares "main clone always stays on main" as a goal but explicitly preserves the old untagged delegation behavior for backward compatibility, leaving the invariant convention-only. If sessions become default rather than opt-in, the main technical gap closes at the delegation layer; the human-in-main-clone case would still require a separate guard (git hook, shell integration warning, or documentation) that does not currently exist.
