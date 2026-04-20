---
status: Proposed
problem: |
  The clone and sync loop in `niwa apply/create` runs sequentially. For workspaces
  with ten or more repos, users wait for each git clone to finish before the next
  begins. Network I/O is the bottleneck and there's no reason repos can't clone
  concurrently. On a typical broadband connection, cloning ten repos sequentially
  takes three to four times longer than cloning them in parallel.
decision: |
  Parallelize Step 3 of `runPipeline()` using a fixed-size worker pool (cap 8).
  A summary spinner shows overall progress ("cloning repos... 3/10 done"). Each
  worker handles clone-or-sync for one repo and returns results through a channel.
  The orchestrator reads results sequentially and drives all Reporter calls. Clone
  failures cancel the context and stop all workers (fail-fast). Sync failures
  remain soft warnings, consistent with existing behavior.
rationale: |
  A summary spinner integrates with the existing single-line Reporter API without
  changes. A fixed cap of 8 avoids network saturation without adding a CLI flag.
  Fail-fast on clone errors matches current sequential behavior: `niwa create`
  removes the instance directory on error, so the existing cleanup path handles
  partial clone directories left by cancelled workers.
---

# DESIGN: Parallel repo clones in niwa apply/create

## Status

Proposed

## Context and Problem Statement

`niwa apply` and `niwa create` clone and sync workspace repos in Step 3 of
`runPipeline()` (`internal/workspace/apply.go`). The loop is sequential: it
clones or syncs each repo before moving on to the next. For small workspaces
(two or three repos) this is fine. For workspaces with ten or more repos, it's
slow.

Git clone is network-bound. Each clone spends most of its time waiting for data
from GitHub. Running them sequentially means network bandwidth sits idle between
clones. Running them concurrently uses available bandwidth from all connections
simultaneously. On a ten-repo workspace the difference is roughly 3x to 4x
wall-clock time.

The existing `Reporter` API supports one transient status line via a background
goroutine spinner. Any parallelization approach needs to integrate with this or
adapt the display model during clones.

## Decision Drivers

- **No new dependencies**: The implementation must use the Go standard library.
  No external concurrency libraries.
- **Preserve TTY/non-TTY behavior**: Non-TTY output must not regress. The
  existing `Reporter` degrades gracefully on pipes and CI.
- **Consistent error semantics**: Clone failures should behave the same as today.
  `niwa create` is all-or-nothing; `niwa apply` already treats sync failures
  as soft warnings.
- **Testable**: Parallelism must not make tests flaky. The `Cloner` and `SyncRepo`
  interfaces must remain injectable.
- **No new CLI flags**: Parallel clones should be automatic. Users shouldn't need
  to opt in.

## Considered Options

### Decision 1: Display model during parallel clones

When multiple repos clone concurrently, the current single-spinner approach
("cloning foo...") no longer makes sense. Multiple clones run simultaneously
and there's no single repo name to show. The display model needs to communicate
concurrent progress.

#### Chosen: Summary spinner with progress counter

The status line shows overall progress: `⠹ cloning repos... (3/10 done)`. A
counter tracks how many workers have finished (success or failure). Each time
a worker completes, the orchestrator increments the counter and calls
`a.Reporter.Status(...)` with the updated message.

Worker goroutines do not call `Reporter` methods directly. All Reporter access
is on the orchestrator, which reads from the result channel sequentially. This
avoids races without adding mutex overhead to the Reporter.

On non-TTY output, `Reporter.Status` is already a no-op, so the parallel path
degrades silently. The final `applied ws (N repos)` line is unchanged. No
`Reporter` API changes are required.

#### Alternatives Considered

**Multi-line display with ANSI cursor control**: Each active worker occupies
its own status line. The orchestrator uses `ESC[<N>A` cursor-up sequences to
rewrite lines in place as workers complete. Rejected because it requires a new
multi-line reporter type with complex locking, breaks on terminal resize,
doesn't have a clean story for where warnings appear, and can't be tested
without a real TTY. The `DESIGN-clone-output-ux.md` design explicitly deferred
multi-line display.

**Silent during clone, summary at end**: No progress display. Just the final
`applied ws (10 repos)` line. Rejected because large workspace clones take
30-60 seconds. Removing all feedback is a regression from the current per-repo
spinner.

---

### Decision 2: Concurrency cap and control

With workers running concurrently, how many repos should clone simultaneously?
Too few and there's limited speedup. Too many and network saturation, OS file
descriptor limits, or connection throttling from the git host become concerns.

#### Chosen: Fixed constant of 8 workers

A worker pool with a cap of 8 concurrent workers. The cap is a package-level
constant (`const cloneWorkers = 8`). All repos are queued into a buffered
channel; workers pull from the queue and process one repo at a time. When the
workspace has fewer than 8 repos, the effective concurrency is just the repo
count.

Eight workers is high enough to saturate typical broadband on a 10-15 repo
workspace. It's below the per-IP connection limits most git hosts enforce
(typically 10-20 simultaneous connections). The constant can be adjusted if
evidence emerges that it's wrong for a meaningful class of workspaces.

#### Alternatives Considered

**Configurable via `--jobs N` flag**: Adds CLI surface that most users will
never touch. The 8-worker default covers the real distribution of workspace
sizes (most workspaces have 3-15 repos). Can be added later if the need
arises.

**Unbounded (all repos at once)**: Simplest code — spawn a goroutine per repo
with no channel orchestration. Rejected because auto-scan mode can return up
to 10 repos per source and configs with multiple sources can push totals above
20. Spawning 20+ concurrent git processes risks network saturation and may
hit OS file descriptor limits. A bounded pool is minimal complexity for
meaningful safety.

---

### Decision 3: Error handling in the worker pool

When one repo fails to clone, there are three approaches to the remaining
in-flight workers: stop them immediately, let them all finish and collect
errors, or skip the failed repo and continue the pipeline.

#### Chosen: Fail-fast on clone errors

When any worker encounters a clone error, the orchestrator calls `cancel()`
on the shared context. All other workers see `ctx.Done()` and their in-progress
`exec.CommandContext` calls return immediately (git is killed). The orchestrator
returns the first error after draining the result channel.

This matches current sequential behavior: the first clone failure returns an
error, which causes `niwa create` to call `os.RemoveAll(instanceRoot)`. Partial
clone directories left by cancelled workers are within that directory and are
cleaned up as part of this removal. For `niwa apply`, partial directories remain
(same as today — a failed clone during apply was already not cleaned up).

Sync failures continue to produce deferred warnings (not context cancellations),
consistent with current soft-handling.

#### Alternatives Considered

**Continue-on-error**: All workers run to completion. Errors are collected and
returned as a combined error. Rejected because a workspace with missing repos is
not usable regardless of how many repos succeeded. Reporting all failures at once
doesn't help the user more than reporting the first. It also changes semantics
for `niwa create`, which today is all-or-nothing.

**Partial success**: Failed clones are skipped; the pipeline continues without
them. Rejected because a workspace missing expected repos is in an inconsistent
state. The materializer steps (CLAUDE.md installation, per-repo settings) assume
all repos are present in the instance directory.

## Decision Outcome

**Chosen: Summary spinner + fixed pool of 8 + fail-fast**

### Summary

Step 3 of `runPipeline()` is restructured from a sequential loop into a worker
pool. A fixed set of 8 goroutines pull clone jobs from a buffered channel. The
orchestrator tracks completion with a counter and updates the spinner status
message on each result: `cloning repos... (N/total done)`.

Workers call the existing `Cloner.CloneWithBranch` and `SyncRepo` functions
unchanged. The only change at the call site is that workers return results via
a channel rather than calling `a.Reporter` directly. The orchestrator reads
results sequentially and makes all Reporter calls, keeping Reporter access
single-threaded.

Clone errors cancel the context via `context.WithCancel`. Each worker wraps its
git command with the cancellable context, so an in-progress git clone gets killed
when another worker fails. For `niwa create`, the existing `os.RemoveAll(instanceRoot)`
call handles cleanup of partial clone directories. Sync failures return a soft
result (deferred warning), not a context cancellation.

On non-TTY output, the display changes are invisible: `Reporter.Status` is a
no-op and workers silently complete. The final `applied ws (N repos)` log line
is unchanged.

### Rationale

The worker pool pattern is idiomatic Go for bounded parallelism. Using result
channels keeps `Reporter` access single-threaded (the orchestrator), avoiding
data races without adding mutex overhead. Reusing the existing `Cloner` and
`SyncRepo` functions preserves test coverage — those function signatures and
test doubles don't change.

The summary spinner follows from the constraint that multi-line ANSI control is
deferred. A progress counter fits naturally into a single status line and requires
no `Reporter` API changes. Fail-fast matches the current all-or-nothing semantics
for `niwa create` and requires no changes to the error path in the Create method.

## Solution Architecture

### Overview

Step 3 of `runPipeline()` becomes a worker pool. The orchestrator sends clone
jobs into a buffered work channel, 8 worker goroutines consume them, and results
flow back through a result channel. The orchestrator drives all Reporter updates.

### Components

```
runPipeline (orchestrator)
  ├── ctx, cancel = context.WithCancel(ctx)
  ├── jobs     chan cloneJob    (buffered, len = total repos)
  ├── results  chan cloneResult (buffered, len = total repos)
  └── N worker goroutines
        └── calls Cloner.CloneWithBranch / SyncRepo → sends cloneResult
```

### Key interfaces

```go
// cloneJob carries per-repo inputs to a worker.
type cloneJob struct {
    cr            ClassifiedRepo
    cloneURL      string
    branch        string
    targetDir     string
    defaultBranch string
    noPull        bool
}

// cloneResult carries per-repo outputs back to the orchestrator.
type cloneResult struct {
    name     string
    cloned   bool
    syncWarn string // non-empty if sync produced a DeferWarn message
    err      error  // non-nil on clone failure; does not include sync errors
}
```

Workers and the two types are unexported and contained in `apply.go`. No new
files or packages.

### Data flow

1. Orchestrator creates `ctx, cancel := context.WithCancel(ctx)` at the top of
   Step 3 and defers `cancel()`.
2. Orchestrator creates the group directories for all repos (this is fast, local
   I/O — no reason to parallelize).
3. Orchestrator fills the `jobs` channel with one entry per `classified` repo,
   then closes it.
4. Orchestrator starts `min(cloneWorkers, len(classified))` worker goroutines.
5. Each worker pulls from `jobs` until the channel is closed. For each job:
   - Calls `a.Cloner.CloneWithBranch(ctx, ...)` (or the equivalent clone path).
   - On clone success where `!cloned && !job.noPull`: calls `SyncRepo(ctx, ...)`.
   - Sends a `cloneResult` to the results channel.
6. Orchestrator reads `len(classified)` results. After each:
   - Increments done counter.
   - Calls `a.Reporter.Status(fmt.Sprintf("cloning repos... (%d/%d done)", done, total))`.
   - On error: calls `cancel()`, breaks the loop, drains remaining results (to
     unblock workers), and returns the error.
7. On full success, orchestrator accumulates `repoStates` from results and calls
   `a.Reporter.Defer` for any sync warnings.

### Display updates during clone

```
⠹ cloning repos... (0/10 done)    ← orchestrator sets before workers start
⠸ cloning repos... (1/10 done)    ← after first result
⠼ cloning repos... (5/10 done)    ← mid-flight
⠴ cloning repos... (10/10 done)   ← all done; spinner stays until next Log
applied myws (10 repos)            ← Log call stops spinner, clears line
```

## Implementation Approach

### Phase 1: Worker pool in apply.go

Restructure Step 3 of `runPipeline()`. Add `cloneJob` and `cloneResult` types.
Add `cloneWorker` unexported function. Replace the sequential `for _, cr := range classified`
loop with the orchestrator-plus-pool pattern.

No changes to `clone.go`, `reporter.go`, `sync.go`, or their test files. The
`Cloner` and `SyncRepo` call signatures are unchanged; only the call site moves
into a worker goroutine.

Deliverables:
- `internal/workspace/apply.go`: rewritten Step 3, new types, `cloneWorker` func,
  `const cloneWorkers = 8`

### Phase 2: Test coverage

Update `internal/workspace/apply_test.go` if any existing tests assert on the
sequential order of clone operations or on the exact status messages emitted
during Step 3.

Add functional test scenarios for the parallel clone path:

Deliverables:
- `test/functional/features/parallel-clones.feature` (new `@critical` scenarios):
  - Multiple repos all present in instance dir after `niwa create`
  - One repo with a bad URL: create fails and instance dir is removed
  - `niwa apply` with multiple repos: all synced after apply

## Security Considerations

This design parallelizes execution scheduling of existing git clone operations.
It does not change what is cloned, from where, with what credentials, or what
data is exposed. Clone URLs are already validated by earlier pipeline steps before
they reach Step 3. No new permissions, dependencies, or data flows are introduced.

Context cancellation kills git processes mid-clone and leaves partial clone
directories. These are within the instance root, which is owned by the user and
cleaned up by the existing `os.RemoveAll(instanceRoot)` call in Create on failure.
For `apply`, partial directories are harmless: the missing `.git` dir causes a
fresh clone on the next apply.

## Consequences

### Positive

- `niwa apply` and `niwa create` are significantly faster for workspaces with
  multiple repos. A 10-repo workspace that takes 40-60 seconds sequentially
  typically finishes in 10-15 seconds with 8 concurrent workers.
- The progress counter ("cloning repos... 5/10 done") gives clearer feedback
  than the current single-repo spinner during long operations.
- No user-visible surface changes — the improvement is automatic.

### Negative

- Eight simultaneous git clones may briefly saturate network bandwidth for
  users on slow connections or VPNs with limited throughput.
- Context cancellation during `niwa apply` leaves partial clone directories.
  This is a pre-existing issue (sequential clones also left partial state on
  apply failure), but parallelism increases the number of partial dirs that
  can accumulate in one interrupted operation.
- The worker pool adds code complexity to `runPipeline()`. Reasoning about
  when cancel fires and who may call Reporter requires more careful reading.

### Mitigations

- The cap of 8 is conservative for typical office and home broadband. Users who
  observe saturation can file an issue to make the cap configurable.
- Partial directories from interrupted `apply` operations are benign: the next
  apply re-clones cleanly (missing `.git` is the signal). A code comment near
  the cancel call documents this.
- Worker pool complexity is self-contained in Step 3 of `runPipeline()`. A
  comment block explains the orchestrator-worker protocol and the two channel
  roles.
