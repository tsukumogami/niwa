---
status: Current
problem: |
  After initial workspace creation, niwa has no mechanism to keep managed repos
  current. Existing repos are skipped on apply, leaving clones at their original
  commit. This forces users to manually pull each repo or recreate the workspace,
  which is especially painful when Claude sessions need fresh code in sibling repos.
decision: |
  niwa apply gains default pull behavior: for each existing repo, fetch from origin,
  then fast-forward pull if the repo is clean and on its configured default branch.
  Dirty repos, non-default branches, and diverged repos are skipped with warnings.
  A --no-pull flag restores the old skip-only behavior. Default branch is resolved
  via config (per-repo override -> workspace default_branch -> "main").
rationale: |
  Apply already auto-pulls workspace config, so extending to repos is a natural
  evolution. Default-on with opt-out matches the daily-freshness use case without
  requiring users to remember a flag. Config-first branch detection is deterministic,
  offline-safe, and aligned with niwa's declarative model. The fetch + ff-only
  strategy is the only non-destructive combination that fails cleanly.
---

# DESIGN: Pull Managed Repos

## Status

Accepted

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

## Considered Options

### Decision 1: How should pull be triggered?

niwa needs a way to pull latest code into existing repo clones. The central
question is where this behavior lives in the CLI surface. The user wants
"one low-friction operation" for daily workspace freshness. But apply's existing
behavior (skip existing repos) is relied on by users and scripts, and changing
defaults has backward compatibility implications.

Config auto-pull already happens inside apply via `SyncConfigDir()`, which means
apply already reaches the network and modifies local state. This blurs the
boundary between "apply config" and "sync repos" -- the precedent is already set.

#### Chosen: apply pulls by default, --no-pull opt-out

`niwa apply` gains pull-where-safe behavior as the new default. On each apply,
after the clone-or-skip decision, niwa runs `git fetch` followed by
`git pull --ff-only` for repos that are clean and on their default branch.
Repos that are dirty, on a non-default branch, ahead of remote, or diverged
are skipped with a warning. Users who need the old behavior use `--no-pull`.

This matches the user's stated goal of zero-friction daily freshness. Since
apply already auto-pulls config, extending to repos is a natural evolution
rather than a conceptual break. The `--no-pull` flag provides an escape hatch
for CI pipelines, scripted workflows, and constrained network environments.

The safe-skip strategy limits blast radius: only repos in the "clean + default
branch + behind remote" state are touched. Everything else gets a warning
message, not a failure.

#### Alternatives Considered

**Separate `niwa sync` command**: Each command has one job (apply = config,
sync = repos). Matches industry consensus (repo, meta, gita all use separate
sync). Rejected because it scores zero on low friction -- the user must
remember a second command every session. The config auto-pull precedent
already breaks the clean separation.

**`niwa apply --pull` flag**: Opt-in flag that adds pulling to apply. Fully
backward compatible. Rejected because opt-in friction compounds on daily use.
The user would need to type `--pull` every session or set up a shell alias,
pushing the problem to user configuration.

**apply always pulls, no opt-out**: Same as the chosen approach but without
`--no-pull`. Rejected because it's strictly worse -- same benefits but no
escape hatch for environments where pulling is undesirable.

### Decision 2: How should niwa determine each repo's default branch?

Pull only happens on repos that are on their "default branch." niwa needs a
reliable way to resolve what that means for each repo. The answer must work
offline, respect per-repo configuration, and handle repos cloned before this
feature existed.

niwa's config already has both per-repo branch overrides (`[repos.X] branch`)
and a workspace-level `default_branch` setting. The clone path already resolves
branches via `RepoCloneBranch()`. The question is whether pull should use the
same config-driven resolution or query git for the remote's actual default.

#### Chosen: Config-first resolution

Three-tier fallback: per-repo `[repos.X] branch` if set, then workspace
`default_branch` if set, then "main". This extends the existing
`RepoCloneBranch()` function to include the workspace default as a middle
tier.

Config-first is deterministic, works offline, and aligns with niwa's
declarative model. Users read the TOML and know exactly what branch niwa
considers "default" for each repo. Repos using non-"main" defaults (like
"master") need an explicit config entry, but that's workspace-specific
knowledge that belongs in the TOML.

#### Alternatives Considered

**Git-first (query `origin/HEAD`)**: Always correct for the remote's current
state after a fetch. Rejected because `refs/remotes/origin/HEAD` is
unreliable -- it may not exist (many clones don't set it), may be stale,
and introduces non-determinism that contradicts niwa's declarative model.

**Hybrid (config -> git -> "main")**: Three-level mixed-source fallback.
Rejected because the git tier inherits the unreliability problems and makes
debugging harder. When pull doesn't happen, users have to figure out which
of three tiers resolved the branch.

**Record at clone time**: Store the branch in RepoState during initial clone.
Rejected because it introduces a migration burden for existing repos, conflates
"branch used to clone" with "default branch," and creates a config/state drift
detection problem. If config always wins over state (which it should), this
reduces to Option A with unnecessary bookkeeping.

## Decision Outcome

**Chosen: 1D + 2A**

### Summary

`niwa apply` becomes a full workspace convergence command: it syncs config
(already implemented), clones missing repos (already implemented), and now
also pulls latest code into existing repos that are safe to update.

For each repo in the apply pipeline, after the existing clone-or-skip
decision, niwa checks the repo's state. It runs `git fetch origin` to
update remote refs, then inspects: is the working tree clean
(`git status --porcelain` returns empty)? Is the current branch the
configured default (resolved via per-repo override -> workspace
`default_branch` -> "main")? Is the repo behind its remote tracking branch
(`git rev-list --count --left-right @{u}...HEAD` shows only right-side
commits)? If all three conditions hold, niwa runs
`git pull --ff-only origin <default-branch>`. If any condition fails, the
repo is skipped with a message explaining why (dirty working tree, on
feature branch, ahead of remote, diverged).

The `--no-pull` flag disables all repo pull behavior, restoring the current
apply semantics for scripts and CI that depend on apply being fast and
offline. When `--no-pull` is set, the pipeline skips fetch and state
checking entirely.

Output during pull shows per-repo status: "pulled X (3 commits)" for
successful pulls, "skipped X (dirty working tree)" or "skipped X (on
branch feature/foo, not main)" for skipped repos, and "skipped X (up to
date)" for repos already current.

### Rationale

The two decisions reinforce each other. Default-pull (1D) means repos stay
fresh without user intervention, and config-first branch detection (2A)
means the pull eligibility check is fast, offline-capable, and predictable.
Together they create a workflow where `niwa apply` is the single command
for "make my workspace current" -- both config and code.

The key trade-off is backward compatibility: existing `niwa apply` users
will see new output (fetch/pull status per repo) and slightly slower
execution (network I/O per repo). This is mitigated by the `--no-pull`
escape hatch and the fact that apply already does network I/O for config
sync. The safe-skip strategy ensures no user loses work from the default
behavior change.

## Solution Architecture

### Overview

The pull feature adds a repo sync step to the existing apply pipeline. After
the clone-or-skip decision for each repo (Step 3 in `runPipeline()`), a new
`syncRepo()` function runs if the repo already existed and `--no-pull` is not
set. The sync function inspects repo state via git commands, decides whether
to pull, and reports the outcome.

### Components

**`internal/workspace/sync.go`** (new file)

Core pull logic, isolated from the apply pipeline:

- `RepoSyncStatus` struct: captures repo state (clean/dirty, current branch,
  ahead/behind counts)
- `InspectRepo(repoDir, defaultBranch string) (RepoSyncStatus, error)`:
  runs `git status --porcelain`, `git rev-parse --abbrev-ref HEAD`, and
  `git rev-list --count --left-right @{u}...HEAD` to classify repo state.
  If the repo has no upstream tracking branch (`@{u}` fails), treat as
  "no remote tracking" and skip pull with a warning.
- `FetchRepo(ctx, repoDir string) error`: runs `git fetch origin`
- `PullRepo(ctx, repoDir, branch string) (int, error)`: runs
  `git pull --ff-only origin <branch>`, returns number of new commits
- `SyncRepo(ctx, repoDir, defaultBranch string) (SyncResult, error)`:
  orchestrates fetch -> inspect -> conditional pull, returns a result
  describing what happened and why

**`SyncResult`** struct:

```go
type SyncResult struct {
    Action  string // "pulled", "skipped", "up-to-date", "fetch-failed"
    Reason  string // empty for pulled/up-to-date, explanation for skipped
    Commits int    // number of new commits pulled (0 if skipped)
}
```

**`internal/workspace/apply.go`** (modified)

The `runPipeline()` method gains a `noPull bool` parameter. In the repo loop
(around line 207), after the clone-or-skip branch, if `!cloned && !noPull`,
call `SyncRepo()` and print the result. The existing "skipped (already exists)"
message is replaced with the sync result message.

**`internal/cli/apply.go`** (modified)

Add `--no-pull` flag to the apply command. Pass it through to `Applier.Apply()`
and `Applier.Create()` via a `NoPull` field on the `Applier` struct (following
the same pattern as `AllowDirty`).

**`internal/workspace/override.go`** (modified)

Extend `RepoCloneBranch()` to check `cfg.Workspace.DefaultBranch` as the
middle fallback before returning empty string. This gives the three-tier
resolution: per-repo branch -> workspace default_branch -> "" (caller
defaults to "main").

### Key Interfaces

```go
// SyncRepo orchestrates the fetch-inspect-pull cycle for a single repo.
// It returns a SyncResult describing the action taken.
// If defaultBranch is empty, "main" is used.
func SyncRepo(ctx context.Context, repoDir, defaultBranch string) (SyncResult, error)

// InspectRepo checks working tree, current branch, and remote status.
func InspectRepo(repoDir, defaultBranch string) (RepoSyncStatus, error)
```

### Data Flow

```
niwa apply (CLI)
  |
  v
SyncConfigDir()          -- pull workspace TOML (existing)
  |
  v
runPipeline(noPull)
  |
  for each repo:
  |   |
  |   v
  |   CloneWithBranch()  -- clone if missing (existing)
  |   |
  |   if already existed && !noPull:
  |   |   |
  |   |   v
  |   |   SyncRepo(ctx, targetDir, defaultBranch)
  |   |     |
  |   |     v
  |   |     FetchRepo()          -- git fetch origin
  |   |     |
  |   |     v
  |   |     InspectRepo()        -- check dirty, branch, ahead/behind
  |   |     |
  |   |     v
  |   |     PullRepo()           -- git pull --ff-only (if eligible)
  |   |     |
  |   |     v
  |   |     SyncResult           -- "pulled 3 commits" / "skipped (dirty)"
  |   |
  |   v
  |   print result
  |
  v
Install content, hooks, settings (existing)
```

## Implementation Approach

### Phase 1: Sync core

Add `sync.go` with `InspectRepo()`, `FetchRepo()`, `PullRepo()`, and
`SyncRepo()`. Add `sync_test.go` with tests for each repo state combination
(clean/dirty x default-branch/other x behind/ahead/diverged/up-to-date).
The tests should use real git repos created in temp directories.

Deliverables:
- `internal/workspace/sync.go`
- `internal/workspace/sync_test.go`

### Phase 2: Branch resolution

Extend `RepoCloneBranch()` to include workspace `default_branch` as the
middle fallback. Update existing tests. Add a `DefaultBranch()` helper
that applies the full three-tier resolution with "main" as the final
fallback. The pipeline must use `DefaultBranch()` (not `RepoCloneBranch()`)
when passing the branch to `SyncRepo`, since `RepoCloneBranch()` returns
empty string without the "main" fallback.

Deliverables:
- Modified `internal/workspace/override.go`
- Modified `internal/workspace/override_test.go`

### Phase 3: Pipeline integration

Wire `SyncRepo()` into `runPipeline()`. Add `--no-pull` flag to CLI. Replace
"skipped (already exists)" message with sync result output. Pass `noPull`
through the Applier methods.

Deliverables:
- Modified `internal/workspace/apply.go`
- Modified `internal/cli/apply.go`
- Modified `internal/workspace/apply_test.go`

### Phase 4: Documentation

Update README or help text to describe the new pull behavior, `--no-pull`
flag, and config-first branch resolution.

Deliverables:
- Updated CLI help text

## Security Considerations

The pull operation uses the same git credentials as the initial clone --
no new authentication surfaces are introduced. The `--ff-only` strategy
prevents merge commits that could introduce unexpected code. niwa never
runs `git pull` with `--rebase` or stashes user changes, so no local
history is rewritten.

The fetch and pull commands shell out to `git` via `exec.CommandContext`,
inheriting the user's git configuration and SSH/credential setup. No
credentials are stored or logged by niwa.

Repo paths are derived from the workspace TOML and instance state, both
of which are under user control. No user-supplied input is interpolated
into shell commands beyond the repo directory path, which is already
validated during the clone step.

One behavioral note: git's `post-merge` hook fires after successful pulls.
This is standard git behavior, but worth noting since users may think of
`niwa apply` as purely declarative. Repos with post-merge hooks will
execute them during apply. This is the same trust model as clone (which
can trigger `post-checkout` hooks).

## Consequences

### Positive

- Workspaces stay fresh with zero additional user effort. Running
  `niwa apply` before a Claude session ensures all safe-to-update repos
  have latest code.
- Consistent with existing config auto-pull behavior. Apply becomes a
  single convergence command for the entire workspace.
- Non-destructive by construction. The fetch + ff-only + state-check
  chain means no data loss is possible in the default path.

### Negative

- **Slower apply**: Every apply now does `git fetch` for each existing repo.
  For workspaces with many repos on slow connections, this adds latency.
- **Changed default behavior**: Existing users and scripts see new output
  and network activity from apply.
- **Warning noise**: Workspaces with many feature branches or dirty repos
  will produce skip warnings on every apply.

### Mitigations

- `--no-pull` restores the old behavior for scripts and CI.
- Fetch failures are non-fatal: a repo that can't be reached is skipped
  with a warning, not an error that stops the pipeline.
- Skip messages are concise (one line per repo) and use a consistent
  format so they're easy to grep or suppress.
