# Security Review: parallel-clones (Phase 6 — Architect Review)

## Scope

This reviews the security claims in the Phase 5 analysis and the DESIGN doc's
Security Considerations section against the actual source code
(`internal/workspace/apply.go` Step 3, `internal/workspace/clone.go`).

The current code is sequential (Step 3, lines 772–805 of apply.go). This
review evaluates the proposed parallelization, not an existing implementation.

---

## Finding 1: Partial-clone directory left by cancelled `niwa apply` — not a security issue, but the claim is incomplete

**Phase 5 conclusion:** "partial directories are harmless — missing `.git` is the signal."

**What the code actually does:** `CloneWith` in `clone.go:44` checks for `.git`
at the start and skips the clone if present. If the context is cancelled
mid-clone, git is killed by `exec.CommandContext`. Git's own clone semantics
leave the target directory in a partially populated state — objects/ and
refs/ subdirectories may exist, but `.git` may be missing or incomplete
depending on when the kill fires.

On the next `apply`, `CloneWith` re-checks for `.git`. If the partial clone
left a `.git` dir (git writes `.git` early, before the full object transfer),
`cloned` returns `false` and the code falls through to `SyncRepo`. `SyncRepo`
calls `git fetch` on what is actually a corrupted repo. `git fetch` on a
half-written object store can produce confusing error messages or silently
succeed with an incomplete history.

**Assessment:** This is not an attacker-controlled scenario — the URLs come
from the workspace config validated in earlier pipeline steps. But the claim
that "missing `.git` is the signal" is not fully accurate. The real signal
is "`.git` is present AND the repo is healthy." The design should document
that a partially-written `.git` can survive cancellation and `SyncRepo` will
be attempted on it.

**Severity:** Advisory for the security review (no new attack surface). The
design's Consequences section acknowledges partial dirs; the claim just needs
a more precise description.

---

## Finding 2: Worker pool race with context cancellation — no privilege escalation, but a correctness gap

**Phase 5 conclusion:** N/A — race conditions not analyzed.

**What the design proposes:** When one clone fails, the orchestrator calls
`cancel()` and then drains the result channel. Workers receive `ctx.Done()`,
their `exec.CommandContext` git calls return, and workers send a `cloneResult`
with a nil-or-context-cancelled error. The orchestrator drains to unblock
them.

**The gap:** The design says "the orchestrator returns the first error after
draining the result channel." However, if a worker's git process is in the
middle of writing to `targetDir` when the kill signal arrives, the directory
state is non-deterministic. For `niwa create`, `os.RemoveAll(instanceRoot)`
cleans all of it — this is correct and the clean-up path is sound. For
`niwa apply`, there is no clean-up. Multiple workers can each leave a partial
clone directory behind in a single interrupted run.

The specific question asked — "could a cancelled context leave a partial clone
from an attacker-controlled source?" — is answered by checking when URLs are
validated. In the current pipeline, `RepoCloneURL` at line 779 pulls URLs from
`effectiveCfg`, which was resolved and merged before Step 3. The workspace
config itself was loaded from disk or cloned from a user-controlled source in
earlier steps. There is no point in Step 3 where a worker receives a URL it
hasn't already received from the same single source of truth. Parallelization
doesn't open a TOCTOU window on the URL because the URL is computed once
per classified repo before workers start, packed into a `cloneJob` struct, and
never re-derived.

**Assessment:** No attacker-controlled-URL race. The partial-directory
accumulation on `apply` failure is a pre-existing issue amplified (not
created) by parallelism — the design acknowledges this in Consequences.

**Severity:** Not a security finding. Advisory on the partial-dir claim
(same as Finding 1).

---

## Finding 3: "Not applicable" justifications — are they sound?

### External Artifact Handling — "No"

The design claims no new external inputs. This is correct. Clone URLs are
determined in Steps 1–2 before Step 3 runs. Workers consume pre-computed
`cloneJob` structs. No worker resolves or fetches a URL it derived itself.

**Verdict: Justification sound.**

### Permission Scope — "No"

Workers use `exec.CommandContext(ctx, "git", ...)` — identical to the
sequential path. No new filesystem permissions. The `os.RemoveAll` cleanup
path for `niwa create` already exists and is not new.

One nuance: eight concurrent git processes each open file descriptors and
potentially hold locks. On systems with a low `ulimit -n`, running 8 workers
simultaneously could exhaust file descriptors, causing a clone to fail with
a confusing `EMFILE` error. This is a reliability concern, not a privilege
concern. The design mentions OS file descriptor limits as a reason for the
cap of 8 (correctly), but doesn't note that the cap only mitigates — it
doesn't eliminate — the risk on constrained systems.

**Verdict: Justification sound for security. Missing a reliability note.**

### Supply Chain / Dependency Trust — "No"

Only Go standard library is used. Correct.

**Verdict: Justification sound.**

### Data Exposure — "No"

Workers do not log credentials. `runGitWithReporter` pipes git stderr to the
reporter. The reporter emits those lines to the user's terminal. This is
unchanged from the sequential path — git clone already prints progress to
stderr. The summary spinner exposes only a count, not names or URLs.

One nuance worth noting: with 8 concurrent workers, git output from multiple
clones can interleave in the reporter. The design routes all `Reporter` calls
through the orchestrator (single-threaded on the result channel), so the
output won't interleave in the printed results. But git's own stderr output
during clone (before the worker sends its result) is captured by
`runGitWithReporter` — how that capture works under concurrent execution
matters.

Looking at `clone.go:64`: `runGitWithReporter(r, cmd)` is called from within
the worker goroutine, and `r` is the shared `*Reporter`. If
`runGitWithReporter` writes directly to the reporter while multiple workers
run concurrently, there is a data race on the reporter unless the reporter
itself is protected. The design's stated invariant is "Worker goroutines do
not call Reporter methods directly" — but `CloneWithBranch` is called from
the worker and it internally calls `runGitWithReporter(r, cmd)`. Whether that
violates the invariant depends on what `runGitWithReporter` does with `r`.

This is a structural concern that the design needs to resolve explicitly:
either `runGitWithReporter` must not call Reporter methods (and instead buffer
output, forwarding it via the result channel), or workers must be given a
no-op reporter and git output is discarded during parallel clones.

**Verdict: Justification conditionally sound.** The "workers don't call
Reporter directly" invariant conflicts with passing `a.Reporter` into
`CloneWithBranch` unless the implementation introduces a per-worker buffering
reporter. This is not a security issue, but it is a design gap that needs
resolution before implementation.

---

## Finding 4: Reporter concurrency — structural gap (not in Phase 5)

This finding has no security implication but affects the structural soundness
of the design's stated safety guarantee.

`CloneWithBranch(ctx, cloneURL, targetDir, branch, a.Reporter)` passes the
shared reporter to the worker. `CloneWith` calls `runGitWithReporter(r, cmd)`.
If `runGitWithReporter` uses the reporter to stream git progress lines
(which the current sequential path does — the "cloning foo..." spinner is
driven by these calls), then multiple workers will call `a.Reporter` methods
concurrently, which contradicts the design's stated concurrency model.

The design must either:
1. Pass a no-op or nil reporter to workers, and discard per-repo git progress
   during parallel clones (replacing it with the summary counter), or
2. Give each worker a buffered reporter that collects output and sends it
   back via `cloneResult`, where the orchestrator replays it.

Option 1 is simpler and consistent with the design's UX decision to show only
"cloning repos... (N/M done)" during parallel clones. Option 2 adds
complexity without UX benefit given that multi-line display was explicitly
deferred.

**Severity: Blocking** for implementation (the stated invariant doesn't hold
with the current `Cloner` interface if git output is forwarded through the
reporter). This needs to be addressed in Phase 1 (apply.go restructuring) to
avoid a data race.

---

## Summary of Residual Risk

| Finding | Security? | Severity |
|---------|-----------|----------|
| Partial `.git` after cancel on apply | No | Advisory — improve documentation |
| URL race via cancelled context | No | None — URLs are pre-validated, no TOCTOU |
| N/A justifications | Sound | No issues for ExternalArtifact, Permissions, SupplyChain |
| Reporter concurrency vs. stated invariant | No (data race) | Blocking for implementation |

**Escalation:** No security escalation required. The design introduces no new
attack surface. The residual risk worth escalating to implementers is the
reporter concurrency gap (Finding 4): if `runGitWithReporter` calls reporter
methods from worker goroutines, the design's stated thread-safety model is
violated. The fix is straightforward (pass a no-op reporter to workers) but
must be explicit in the implementation spec.
