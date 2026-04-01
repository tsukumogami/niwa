# Exploration Findings: pull-managed-repos

## Core Question

How should niwa keep managed workspaces fresh after initial creation? This covers
syncing workspace TOML changes (new/removed repos) and pulling latest code from
remotes for existing repos.

## Round 1

### Key Insights

- **Config sync already exists and uses `git pull --ff-only`** (apply-pipeline lead).
  `SyncConfigDir()` in `configsync.go` already pulls workspace TOML from origin
  before apply runs. This pattern is proven and can be extended to repos.

- **Clear insertion point in apply pipeline** (apply-pipeline lead). Pull logic fits
  between clone decision and content installation at `apply.go:207-215`. The Cloner
  currently returns `(cloned bool, err)` -- when `cloned=false`, a pull step can run.

- **Industry consensus: explicit sync, preserve user state** (workspace-manager-patterns
  lead). Every major multi-repo tool (repo, meta, gita, mu-repo, myrepos, mani) uses
  explicit sync commands. None auto-pull on config apply. All preserve dirty working
  trees and non-default branches.

- **Safe default is `git fetch` + `git pull --ff-only`** (git-operations lead). Fetch
  is always safe (updates refs only). Pull with `--ff-only` fails cleanly on divergence.
  niwa already shells out to git (no library); same pattern works for pull.

- **18 state combinations, only one is safe for unattended pull** (dirty-repo-handling
  lead). Clean working tree + default branch + behind remote = safe. Everything else
  should be skipped with a warning by default.

- **TOML drift is undetected** (toml-drift lead). Removed repos are silently orphaned.
  URL and branch changes go unnoticed. `RepoState` only tracks `{URL, Cloned}` --
  lacks group, branch, and verification timestamps.

- **RepoState needs enhancement** (toml-drift lead). Adding branch, group, and
  last-verified fields enables drift detection and better status reporting.

### Tensions

- **Auto-pull vs explicit command**: The user wants zero-friction freshness (favors
  auto-pull in apply). Industry consensus says keep sync separate (explicit command).
  The middle ground is: apply pulls where safe by default, users can opt out.

- **TOML drift scope**: TOML drift detection (removed repos, URL changes) is related
  to code freshness but mechanically different. Coupling them in one command is simpler
  for users but harder to implement and document.

- **Backward compatibility**: Making apply pull by default changes existing behavior.
  Users with dirty repos would see new warnings. Mitigated by the fact that apply
  already auto-pulls config.

### Gaps

- No research on parallel repo syncing (speed optimization for large workspaces)
- No research on `niwa status` integration (showing staleness)
- Default branch detection strategy not fully resolved (`origin/HEAD` vs config)

### Decisions

See `wip/explore_pull-managed-repos_decisions.md`

### User Focus

User's primary concern: Claude sessions exploring sibling repos see stale code.
Wants one low-friction operation to make the workspace current. Willing to accept
flags/config for edge case behavior. Explicitly asked for UX proposals rather than
having a strong preference.

## Accumulated Understanding

niwa needs to close the gap between "workspace created" and "workspace current."
Two dimensions: (1) pulling latest code from remotes into existing clones, and
(2) detecting TOML configuration drift (removed repos, changed URLs/branches).

The safest approach for code freshness is `git fetch` followed by `git pull --ff-only`
on repos that are clean and on the default branch. All other repo states should be
skipped with clear warnings. This matches the existing `SyncConfigDir` pattern and
the industry consensus on non-destructive sync.

The command UX decision is between making apply smarter (pull by default where safe)
vs adding a separate sync command. The user's use case (daily freshness for Claude
sessions) favors integration into apply -- one command to run. The apply pipeline
already has a clear insertion point and already auto-pulls config.

TOML drift (removed repos, URL/branch changes) is a separate concern that should
be addressed with warnings by default and opt-in flags for cleanup. RepoState needs
enhancement to enable drift detection.

The research is sufficient to produce a design doc covering the pull mechanism,
command UX, edge case handling, and state tracking enhancements.

## Decision: Crystallize
