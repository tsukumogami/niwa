# Lead: Non-pushed-work detection

## Findings

### State catalog (what to check, command per state)

Ranked roughly by "loss severity" (highest first), each row giving the detection method. All commands run with `git -C <dir>` to scope to a working tree; switch to `git --git-dir=<gitdir> --work-tree=<wt>` only if a worktree's gitdir resolution fails.

| # | State | Severity | Detection (porcelain) | Notes |
|---|-------|----------|------------------------|-------|
| 1 | Modified working-tree files (unstaged) | High — typed lines of code in flight | `git status --porcelain=v1` lines starting with ` M`, ` D`, ` T`, ` R`, ` A`, etc. (second column non-space) | Today's `CheckUncommittedChanges` already covers this implicitly. |
| 2 | Staged but uncommitted | High — about-to-commit work | `git status --porcelain=v1` lines with first column non-space (`M `, `A `, `D `, `R `) | Same `git status` invocation; classify by column. |
| 3 | Untracked files | Medium — could be new code or junk | `git status --porcelain=v1` lines starting with `??` | Respects `.gitignore`. Add `--ignored` only if we want to surface ignored but-on-disk artifacts (out of scope). |
| 4 | Local commits ahead of upstream | High — typed and committed but never pushed | `git for-each-ref --format='%(refname:short) %(upstream:track) %(upstream)' refs/heads` | Single command lists *every* local branch with its tracking state and ahead-count, e.g. `feature/foo [ahead 2]`. Far cheaper than per-branch `rev-list`. |
| 5 | Local-only branches (no upstream) | High — entire branch invisible to remote | Same `for-each-ref` call: rows with empty `%(upstream)` | The branch's commits are uniquely lost if its only ref disappears with the directory. Quantify with `git rev-list --count <branch> --not --remotes` to get "commits not reachable from any remote-tracking ref". |
| 6 | Stash entries | High — explicitly-saved WIP | `git stash list --format=%gd %gs` (or just count lines) | Stashes are tied to the repo's reflog; they vanish with the directory. Cheap: one shell call per repo. |
| 7 | Detached HEAD with non-remote-reachable commits | High — orphan commits, easy to lose | `git symbolic-ref -q HEAD` returns non-zero ⇒ detached. Then `git rev-list --count HEAD --not --branches --remotes` to count commits not reachable from any local branch *or* remote-tracking ref. | Only flag when count > 0 (a detached HEAD on a tagged commit isn't a loss). |
| 8 | Local-only tags (lightweight or annotated, never pushed) | Medium — usually intentional | `git for-each-ref refs/tags --format='%(refname:short) %(*objectname)'` cross-referenced with `git ls-remote --tags origin` | Cross-ref is one extra network call per repo, which we want to avoid (offline-friendly). De-scope unless we trivially fold it into the for-each-ref pass. **Recommend: skip for v1.** |
| 9 | Worktree working-tree state (1–3 above, but in the worktree) | High | Same `git status --porcelain` run with `git -C <worktree-path>` | See "Worktree handling" below. |
| 10 | Worktree branches with unpushed commits | High | Same `for-each-ref` from the *primary* repo also lists branches checked out in worktrees (HEAD of worktree = a branch ref) | Worktree-checked-out branches show up in `for-each-ref` of the parent repo because refs are shared. |
| 11 | Submodule states (1–10 above) | Variable | `git submodule foreach --recursive '...'` then re-run the catalog inside | **De-scope for v1**: niwa-managed repos rarely use submodules; deep recursion explodes the cost; we can warn ("submodules detected, not scanned") instead. |
| 12 | Reflog entries unique to this repo | Low — almost always recoverable noise | `git reflog --all` | Skip. False-positive heavy; reflog includes every checkout. |

**Single-pass design**: the practical detector boils down to 3 git invocations per working tree (status, for-each-ref, stash list) plus 2 conditional ones (symbolic-ref + rev-list when detached). The worktree enumeration is one extra command per repo.

### Worktree handling

Niwa **does** create worktrees today, in `internal/mcp/handlers_session.go` line 188:
```go
exec.Command("git", "-C", repoPath, "worktree", "add", worktreePath, "-b", branchName)
```
under `<instanceRoot>/.niwa/worktrees/<repo>-<session-id>/`, on a `session/<id>` branch. So the destroy guardrail must scan worktrees: an active session left running would otherwise vanish silently.

Users may also create their own worktrees ad-hoc anywhere — most commonly inside the cloned repo dir but technically anywhere on disk. We can only see the ones the primary git repo knows about.

**Enumeration**: `git -C <repoDir> worktree list --porcelain` produces records like:
```
worktree /abs/path/to/wt
HEAD <sha>
branch refs/heads/feature/foo
```
or `detached` instead of `branch ...`. The first record is the primary working tree (the repo dir itself); subsequent records are linked worktrees. We scan each.

**Important nuance**: linked worktrees outside `instanceDir` (e.g., user manually `git worktree add /tmp/wt`) are not deleted by `os.RemoveAll(instanceDir)`. But the `.git/worktrees/<name>/` admin file inside the primary repo *will* be deleted, leaving the external worktree orphaned and unrecoverable as a worktree (though its files remain on disk). We should:
- Surface external worktrees in the dirty report so the user knows they exist.
- **Not** treat external worktrees as a hard block — they're outside our deletion scope. Just inform.

The branch checked out in a worktree shares refs with the primary repo, so `for-each-ref refs/heads` in the primary repo already gives us the ahead-count for worktree branches. We don't need to re-run `for-each-ref` per worktree, only `git status --porcelain` and `git stash list` (each worktree has its own working tree and its own stash stack).

### Cost estimate and parallelism

Current pattern: niwa already runs git operations across repos via a bounded worker pool. See `internal/workspace/apply.go:1093-1140` — `cloneWorkers = 8` (line 154), buffered job/result channels, `cancel` on first error. Same shape we should use here.

**Per-repo cost**: 3 base commands + ~1 worktree-list + 2 conditional + N×2 per linked worktree. Realistic cap: ~10 git invocations per repo on the upper end.

**Walltime model**: each `exec` is dominated by process startup + git's index read. On a warm cache, `git status --porcelain` on a small repo is ~20–50ms; the heavier ones (large index) hit ~100–200ms. Call it 100ms average.

For the user's example (3 instances × 5 repos = 15 repos, ~6 commands per repo = 90 invocations):
- Sequential: 90 × 100ms = **9 seconds**. Too slow for a destroy confirmation prompt.
- Parallel at workers=8: ceil(90/8) × 100ms ≈ **1.2 seconds**. Acceptable.
- Per-instance parallelism (3 instances × 8 workers each = 24 in flight): ~400ms. Best.

**Recommendation**: reuse the existing bounded-worker pattern. Run repo scans in parallel within an instance with a worker count of 8 (matches `cloneWorkers`). Run instances *sequentially* — they're already a multiplicative factor, and the user is waiting at a prompt, not a long-running command. The total is bounded at low single-digit seconds even for big workspaces.

**Reporter integration**: emit a `Status("scanning for unpushed work... (3/15 repos)")` line so the user sees progress. The reporter's spinner goroutine (see `internal/workspace/reporter.go`) handles redraw.

### Output shape (data + display)

```go
// In a new file, e.g. internal/workspace/scan.go.

// LossKind enumerates categories of work that would be lost on directory deletion.
type LossKind string

const (
    LossWorkingTreeDirty   LossKind = "dirty"        // modified or staged
    LossUntracked          LossKind = "untracked"
    LossUnpushedCommits    LossKind = "unpushed"     // branch ahead of upstream
    LossLocalOnlyBranch    LossKind = "local-only"   // branch with no upstream
    LossStash              LossKind = "stash"
    LossDetachedOrphan     LossKind = "detached"     // detached HEAD with unreferenced commits
    LossExternalWorktree   LossKind = "external-wt"  // linked worktree outside instance dir
)

// Loss is one finding for one ref-or-state in one repo.
type Loss struct {
    Kind     LossKind
    Branch   string // branch name, or "" for stash/dirty/untracked
    Detail   string // human summary: "3 modified files", "2 commits", "1 stash", etc.
    Path     string // worktree path if not the primary, else ""
}

// RepoScan groups Losses for one repo.
type RepoScan struct {
    Name     string  // repo dir name (key in state.Repos)
    Losses   []Loss
    Skipped  string  // non-empty reason if we couldn't scan: "broken (.git missing)", "no clone"
}

// InstanceScan groups RepoScans for one instance.
type InstanceScan struct {
    InstanceName string
    InstanceDir  string
    Repos        []RepoScan
}

// HasLoss returns true if any repo in any instance has a non-empty Losses list.
func (s InstanceScan) HasLoss() bool { ... }
```

**Human-readable format** (printed by the destroy command, not the workspace package):

```
The following instances have unpushed work:

  instance-a:
    tsuku/niwa:
      working tree: 3 modified, 1 staged
      branch feature/foo: 2 commits ahead of origin/feature/foo
      1 stash
    tsuku/koto:
      worktree at .niwa/worktrees/koto-abc123:
        branch session/abc123: 5 commits, no upstream
        working tree: 1 modified

  instance-b:
    tsuku/shirabe:
      branch local-experiment: 4 commits, no upstream
      detached HEAD: 1 orphan commit

  instance-c (clean)

Type "tsuku-workspace" to confirm deletion (or Ctrl-C to abort):
```

Notes on the format:
- One blank line between instances. Two-space indent per level.
- Repos with **zero** losses are omitted (they don't add signal).
- Instances with zero losses get a `(clean)` line so the user can confirm the scan ran and saw them.
- Worktrees nest under their repo with a `worktree at <relpath>:` header.
- `detached HEAD: N orphan commit(s)` only appears when the orphan count is > 0.
- The confirmation token is the workspace name (resolved via `EffectiveConfigName` like elsewhere) — typed verbatim.

### Edge cases

| Case | Detector behavior |
|------|-------------------|
| Repo cloned per state but `.git` removed/corrupt | `RepoScan.Skipped = "git directory missing or corrupt"`. Treat as **dirty** for the prompt (better safe than silently delete). User can `--force` past it. |
| State says cloned, dir doesn't exist | Today: silently skipped. New behavior: same — nothing to lose. No `RepoScan` emitted. |
| Instance dir present, no `.niwa/instance.json` (orphan) | `LoadState` already errors. Surface as `InstanceScan{Skipped: "orphan: no instance.json"}`. Treat as dirty (force user to confirm) — we don't know what's in there. |
| Submodules in a repo | v1: detect their presence (`git submodule status` exits 0 with output) and emit a single `Loss{Kind: "submodule", Detail: "N submodules not scanned"}` informational line. Don't recurse. |
| `git` binary missing | Same failure mode as today; bubble the exec error up. Destroy refuses to proceed unless `--force` is given (and `--force` already skips the scan entirely). |
| Worktree's gitdir broken (e.g., `git worktree list` errors) | Skip the worktree, emit a Loss with Kind=`external-wt`/`broken-wt`, Detail=`could not enumerate`. |
| Worktree path overlaps the instance dir we're about to delete (`.niwa/worktrees/...`) | Normal case for niwa-created session worktrees. Treat as a regular nested worktree, no special handling. |
| Worktree path is *outside* the instance dir | Emit `LossExternalWorktree` with the absolute path. Don't block, but inform — we can't see its working tree state without scanning, but we shouldn't surprise the user that this exists. |
| Repo on a tag, not a branch | `for-each-ref refs/heads` already lists branches; the tag isn't a "lost branch" because tags are explicit. No special handling. |

### Code organization (new helper vs. extension)

Per-instance destroy still uses the narrower `CheckUncommittedChanges` (out of scope for this change). The new workspace-wide guardrail wants different output (per-repo, per-state classification) and per-instance grouping, so it gets its own file:

- New file: `internal/workspace/scan.go`
  - Types: `LossKind`, `Loss`, `RepoScan`, `InstanceScan`.
  - `ScanInstance(instanceDir string) (InstanceScan, error)` — sequential per-repo scan within an instance.
  - `ScanInstancesParallel(instanceDirs []string) ([]InstanceScan, error)` — bounded worker pool, calls `ScanInstance` per worker.
  - `(InstanceScan).Format(w io.Writer)` and `FormatScans(scans []InstanceScan, w io.Writer, workspaceName string)` for the human-readable rendering, kept package-internal so the CLI can call a thin wrapper.

- `CheckUncommittedChanges` stays as-is in `destroy.go`. The per-instance destroy path keeps using it for backward compatibility (and the `--force` flag's existing semantics).

- The CLI side (`internal/cli/destroy.go`) gains a workspace-self-destroy branch that calls `workspace.ScanInstancesParallel`, prints via `FormatScans`, and reads the typed-confirmation line.

This keeps `destroy.go` focused on the per-instance lifecycle and isolates the "what would I lose" reasoning in a single new file with its own tests.

## Implications

- The detector is at heart a *single git command per state per working tree*, totaling under 10 invocations per repo. Process-startup-bound, embarrassingly parallel.
- Reusing the existing worker-pool pattern (`cloneWorkers = 8`) keeps the new code in line with how niwa already does multi-repo work, and brings the wall-clock cost on a 15-repo workspace under 2 seconds — fine for an interactive prompt.
- The biggest information-design choice is what to **omit** from the report. Untracked files alone produce noise (every node_modules, every editor swap file). I lean toward including them by default but collapsing to a count (`untracked: 47 files`); a future flag could expose details.
- Worktree enumeration is non-optional because niwa creates worktrees under `.niwa/worktrees/` for sessions. A user who has an active session running cannot have their workspace silently deleted.
- The data structure is deliberately a *flat list of Losses per repo* rather than a typed struct with one field per state. Adding a new LossKind later (say, "submodule with unpushed work") is a single new constant plus a detector branch — no schema migration.

## Surprises

- **Niwa already creates worktrees**, in the MCP session lifecycle. I expected to find no worktree usage and have to argue for the scan from first principles; instead, the destroy guardrail is a real gap today — destroying an instance with a live session would silently delete its worktree branch.
- **`git for-each-ref` is the unsung hero**: a single command gives us the branch list with upstream-tracking state and ahead-counts. Iterating branches with separate `rev-list --count` calls would be the obvious naive approach and would multiply the cost by branch count.
- The existing `CheckUncommittedChanges` not only ignores unpushed commits and stashes, it doesn't even classify what `git status` returned — it just checks for any non-empty output. Even the per-instance `niwa destroy` is weaker than I assumed.
- `cloneWorkers = 8` and the buffered-channel pattern in `apply.go:1093` is a clean reusable pattern; we don't need `errgroup` or any new dependency.

## Open Questions

- **Should `--force` skip the scan entirely, or run it and just not block?** Today's `--force` skips the check. For v1, keep that — `--force` means "I know what I'm doing." A future flag (`--no-confirm` vs. `--force`) could split intent.
- **What's the confirmation token?** Workspace name is the obvious choice. Should we accept a fixed string like `DESTROY` instead, to avoid users muscle-memorying the workspace name? The user's lead specified workspace name, so go with that for v1.
- **Untracked files: noisy or worth surfacing?** Lean toward "include but collapse to count." Could be flag-gated.
- **External worktrees: block or warn?** Probably warn-only — they're outside our deletion scope and blocking would be surprising. The user can clean them up manually.
- **Submodules with unpushed work**: include only the "N submodules not scanned" informational line, or recursively scan? v1 leans toward inform-only; revisit if real-world workspaces use submodules heavily.
- **Network access**: do we attempt `git fetch` first to compare against the *current* remote, or trust the local remote-tracking refs? Lean toward trusting local — fetch makes the scan slow and online-only, and the user can run `niwa sync` first if they want fresh tracking data.

## Summary

The detector is a per-repo run of three to five git plumbing commands (`status --porcelain`, `for-each-ref refs/heads`, `stash list`, `worktree list --porcelain`, plus a conditional detached-HEAD check) producing a flat list of typed `Loss` records per repo, grouped per instance — with worktree scanning mandatory because niwa itself creates session worktrees under `.niwa/worktrees/`. Cost is bounded: at the existing `cloneWorkers=8` parallelism the realistic 15-repo workspace finishes in under two seconds, well inside an interactive prompt budget. The biggest still-open design choice is the noise/signal trade-off for untracked files and submodules — surface them as collapsed counts in v1, leave a flag for detail.
