# Architecture Review: Pull Managed Repos

## 1. Is the architecture clear enough to implement?

Yes. The design doc specifies concrete function signatures, file locations, data
flow, and a phased plan. An implementer can start coding from Phase 1 without
ambiguity on what to build.

Two areas need minor clarification before implementation:

- **`rev-list --left-right` parsing**: The design mentions
  `git rev-list --count --left-right @{u}...HEAD` but doesn't specify what
  happens when there is no upstream tracking branch set. Repos cloned by niwa
  via `git clone --branch X` will have tracking set up automatically, but repos
  added to the workspace after manual clone may not. `InspectRepo` needs to
  handle the "no upstream" case explicitly (skip with a message like "no
  tracking branch configured").

- **Error vs warning boundary**: The design says fetch failures are non-fatal
  (skip with warning), but doesn't clarify whether `git pull --ff-only` failure
  (e.g., ff-only fails because of divergence that wasn't caught by the
  ahead/behind check) should be a warning or an error. Given the non-destructive
  philosophy, this should be a warning -- the inspect step should catch most
  cases, but ff-only failure is the safety net.

## 2. Are there missing components or interfaces?

### Missing: `noPull` plumbing through Applier methods

The design says `runPipeline()` gains a `noPull bool` parameter, and the CLI
passes it through `Applier.Apply()` and `Applier.Create()`. But today:

- `Applier.Apply()` signature is `(ctx, cfg, configDir, instanceRoot) error`
- `Applier.Create()` signature is `(ctx, cfg, configDir, workspaceRoot) (string, error)`
- `runPipeline()` takes `pipelineOpts` struct

The cleanest approach is adding `NoPull bool` to `pipelineOpts` rather than
adding a parameter to every public method. Then `Apply` needs to accept it
somehow -- either via a new `ApplyOpts` struct parameter, or by adding it to
`pipelineOpts` which is already passed through. The design should specify this
plumbing path.

**Recommendation**: Add `NoPull bool` to `pipelineOpts`. Have `Apply()` and
`Create()` accept an `ApplyOptions` struct (or add `NoPull` to `Applier` as a
field set before calling Apply). Given that `--allow-dirty` already exists as a
flag that doesn't flow through Applier, the simplest pattern is to match that:
add `NoPull` as a field on `Applier` itself, set it from CLI before calling
Apply/Create.

### Missing: `defaultBranch` parameter threading

`SyncRepo` needs the default branch for a repo. The design says it's resolved
via the three-tier config chain, but doesn't show where the resolution call
happens. Looking at the pipeline code at apply.go:204:

```go
branch := RepoCloneBranch(cfg, cr.Repo.Name)
```

This returns the per-repo branch or empty string. The design proposes extending
this to include workspace `default_branch`, but the variable is called `branch`
and is currently only used for cloning. For sync, the caller needs the resolved
branch with "main" as final fallback. The design's proposed `DefaultBranch()`
helper handles this, but it needs to be called in the pipeline loop and passed
to `SyncRepo`. This is straightforward but should be explicitly called out in
the Phase 3 deliverables.

### Not missing but worth noting: `SyncConfigDir` as template

The design correctly identifies `SyncConfigDir` (configsync.go) as the pattern
to follow. That function does dirty-check then `pull --ff-only` in one step
without a separate fetch. The new sync.go splits fetch from pull, which is
better (fetch is always safe, and the inspection happens between fetch and
pull). No issue here, just noting the intentional divergence from the template.

## 3. Are the implementation phases correctly sequenced?

Yes, the four phases have correct dependency ordering:

- **Phase 1 (sync core)**: Pure functions with no pipeline dependency. Can be
  fully tested in isolation with temp git repos.
- **Phase 2 (branch resolution)**: Modifies `RepoCloneBranch()` which Phase 3
  depends on for resolving the default branch. Must come before Phase 3.
- **Phase 3 (pipeline integration)**: Wires everything together. Depends on
  Phase 1 (SyncRepo exists) and Phase 2 (DefaultBranch resolution works).
- **Phase 4 (docs)**: No code dependency.

One optimization: Phases 1 and 2 are independent and could be done in parallel
or in either order. But the proposed 1-then-2 sequence is fine since Phase 1
is the larger chunk and establishing the core sync functions first gives Phase 2
a clearer target for what "main" fallback means.

## 4. Are there simpler alternatives we overlooked?

### Alternative: Reuse SyncConfigDir directly

`SyncConfigDir` already does dirty-check + ff-only pull. Could we just call
`SyncConfigDir(repoDir, false)` for each repo instead of building the full
inspect/fetch/pull pipeline?

**Verdict: No.** SyncConfigDir doesn't check which branch the repo is on (it
pulls whatever branch is current), doesn't do a separate fetch step, and
doesn't report ahead/behind status. For managed repos that may be on feature
branches, skipping the branch check would pull the wrong branch or fail
confusingly. The richer state inspection is justified.

### Alternative: Skip fetch, just pull

Instead of fetch-then-inspect-then-pull, just run `git pull --ff-only origin
<branch>` directly and handle the failure modes.

**Verdict: Tempting but worse.** Without fetch first, the rev-list comparison
uses stale remote refs, so the ahead/behind classification could be wrong. The
inspect step is what makes the skip messages accurate ("you're 2 commits ahead"
vs a generic "pull failed"). The two-step approach is worth the extra exec.

### Alternative: Use `pipelineOpts` for all new options

Instead of the `Applier` field or new `ApplyOptions` struct, add `NoPull` to
`pipelineOpts` and have CLI set it through the existing call chain.

**Verdict: This is actually the simplest approach.** `pipelineOpts` already
exists as the internal options struct for the pipeline. Adding `NoPull bool` to
it requires zero new types and only touches `runPipeline`'s callsites (Apply
and Create). The CLI passes it by constructing `pipelineOpts{noPull: true}` in
the Apply/Create methods, which in turn need a way to receive it. Adding a
`NoPull` field to `Applier` that both `Apply` and `Create` read into
`pipelineOpts` is the smallest change.

## 5. Does the proposed insertion point (apply.go ~line 207) actually work?

**Yes, with a minor correction on line numbers.**

The design references "apply.go:207-215" as the insertion point. Looking at
the current code:

```go
// Line 206: targetDir := filepath.Join(groupDir, cr.Repo.Name)
// Line 207: cloned, err := a.Cloner.CloneWithBranch(ctx, cloneURL, targetDir, branch)
// Line 208: if err != nil {
// Line 209:     return nil, fmt.Errorf("cloning repo %s: %w", cr.Repo.Name, err)
// Line 210: }
// Line 211: if cloned {
// Line 212:     fmt.Printf("cloned %s into %s\n", cr.Repo.Name, targetDir)
// Line 213: } else {
// Line 214:     fmt.Printf("skipped %s (already exists)\n", cr.Repo.Name)
// Line 215: }
```

The sync logic belongs in the `else` branch (lines 213-214), replacing the
"skipped (already exists)" message. The structure would become:

```go
if cloned {
    fmt.Printf("cloned %s into %s\n", cr.Repo.Name, targetDir)
} else if !opts.noPull {
    result, syncErr := SyncRepo(ctx, targetDir, defaultBranch)
    // handle result/error, print status
} else {
    fmt.Printf("skipped %s (already exists)\n", cr.Repo.Name)
}
```

This insertion point is correct and clean. The `cloned` bool from
`CloneWithBranch` already distinguishes new clones from existing repos, which
is exactly the signal needed to decide whether to sync. The `targetDir` and
`branch` variables are already in scope.

**One thing to watch**: The `branch` variable at line 204 comes from
`RepoCloneBranch()` which currently returns empty string (not "main") when
there's no override. After Phase 2 changes, it will return the workspace
default or still empty. The `SyncRepo` caller needs to apply the final "main"
fallback, which is what the proposed `DefaultBranch()` helper does. Make sure
the pipeline uses `DefaultBranch()` (not `RepoCloneBranch()`) for the sync
call.

## Summary of Recommendations

1. **Add `NoPull` as a field on `Applier`** (or to `pipelineOpts` via a small
   plumbing change). The design should specify the exact plumbing path since
   `Apply()` and `Create()` signatures need to remain clean.

2. **Handle "no upstream tracking branch"** in `InspectRepo`. This is a real
   edge case for manually cloned repos added to a workspace.

3. **Treat `git pull --ff-only` failure as a warning**, not a pipeline error.
   The inspect step is the primary guard; ff-only failure is the safety net.

4. **Use `DefaultBranch()` (not `RepoCloneBranch()`)** when calling `SyncRepo`
   in the pipeline loop. The design describes this helper but doesn't show
   it being called at the insertion point.

5. **Phase sequencing is correct.** No changes needed.

6. **No simpler alternatives missed.** The fetch/inspect/pull pipeline is the
   right level of complexity for the requirements. Reusing `SyncConfigDir`
   would be simpler but unsafe for repos on feature branches.
