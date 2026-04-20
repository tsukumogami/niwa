---
reviewer: architect-reviewer
design: DESIGN-parallel-clones.md
date: 2026-04-19
verdict: approved-with-findings
---

# Architecture Review: Parallel Repo Clones

## Summary

The design fits the existing architecture. The worker pool is contained entirely
within Step 3 of `runPipeline()`, introduces no new files or packages, and the
two proposed types (`cloneJob`, `cloneResult`) are unexported and local to
`apply.go`. All five decision drivers are satisfied by the described approach.
One blocking issue and two advisory notes are recorded below.

---

## Q1: Are cloneJob/cloneResult sufficient? Is data flow clear?

**Yes, with one gap.**

The `cloneJob` struct carries all fields needed by workers: `cr`, `cloneURL`,
`branch`, `targetDir`, `defaultBranch`, and `noPull`. Comparing against what
the current sequential loop reads at lines 779–803 of `apply.go`:

- `cloneURL` ← `RepoCloneURL(effectiveCfg, cr.Repo.Name, cr.Repo.SSHURL, cr.Repo.CloneURL)`
- `branch` ← `RepoCloneBranch(effectiveCfg, cr.Repo.Name)`
- `targetDir` ← `filepath.Join(groupDir, cr.Repo.Name)`
- `defaultBranch` ← `DefaultBranch(effectiveCfg, cr.Repo.Name)` (used in SyncRepo path)
- `noPull` ← `a.NoPull`

All fields map. The `cloneResult` is also sufficient: `name`, `cloned`,
`syncWarn`, and `err` cover every outcome the orchestrator needs to reconstruct
`repoStates` and defer warnings.

The channel data flow (orchestrator fills jobs → workers drain → workers send
results → orchestrator drains results) is straightforward and well-described.

**Gap — `cloneResult` is missing `cloneURL`.**

After the sequential loop, `repoStates[cr.Repo.Name] = RepoState{URL: cloneURL, Cloned: ...}`
(line 801). The orchestrator reads results by name only; it has no local variable
for the URL at result-read time because each job was sent to an arbitrary worker.
Without `cloneURL string` in `cloneResult`, the orchestrator cannot populate
`RepoState.URL`. This is a **blocking** gap: the field will be silently empty in
the saved state, breaking URL-based re-sync on subsequent applies.

Fix: add `cloneURL string` to `cloneResult` and have workers echo
`job.cloneURL` into it.

---

## Q2: Missing components? Should group directory creation be parallelized?

**No missing components. Group directory creation should remain sequential.**

The design correctly identifies that group directories must exist before workers
attempt to clone into them. `CloneWith` in `clone.go` calls `os.MkdirAll` only
on the *parent* of `targetDir` (line 48), not on the group directory. If the
group directory isn't already present, the `os.MkdirAll(groupDir, 0o755)` at
the current line 775 would fail. Because repos in the same group share a
directory, parallelizing `os.MkdirAll` would require deduplication or
`os.IsExist` handling — unnecessary complexity for a fast local operation.
Sequential creation before launching workers is the right call.

No new interfaces are needed. `Cloner.CloneWithBranch` and `SyncRepo` are
already context-aware (`exec.CommandContext`), so cancellation propagates
without changes to those signatures.

---

## Q3: Are implementation phases correctly sequenced?

**Yes.**

Phase 1 (restructure Step 3 in `apply.go`) followed by Phase 2 (test coverage)
is the correct order. All structural dependencies are resolved before test
changes: `apply_test.go` can't be updated until the new types and function
signatures exist. The functional test scenarios described in Phase 2 are
comprehensive — they cover the happy path, clone failure + cleanup, and apply
sync. No phase dependencies are inverted.

One note: Phase 2 mentions updating `apply_test.go` "if any existing tests
assert on the sequential order." It would be safer to state this as a concrete
prerequisite step: read the current test file and enumerate any tests that
assert on `Status` call order or reporter invocation count, since those will
break deterministically with parallel scheduling.

---

## Q4: Is there a simpler approach?

**The design is already near-minimal. One micro-simplification is available.**

The unbounded goroutine-per-repo approach (rejected in Decision 2) is genuinely
simpler code but the design's rationale for rejecting it is sound: auto-scan
can return 20+ repos and unbounded git processes create real saturation risk.

For typical workspace sizes (3–15 repos), an `errgroup.Group` with
`SetLimit(8)` from `golang.org/x/sync/errgroup` would be simpler than the
manual channel orchestration — but the design explicitly prohibits external
dependencies, so the manual worker pool is the correct choice given that
constraint.

The only simplification available within stdlib: since both `jobs` and `results`
are buffered to `len(classified)`, the workers never block on send. This means
the drain-on-cancel step (step 6 in the data flow) cannot actually deadlock even
without explicit draining — but keeping the drain loop is still good practice
and makes the cancellation path obviously safe. No simplification warranted here.

---

## Q5: Race conditions or deadlock risks?

**One structural deadlock risk in the drain-on-cancel step. Two advisory notes.**

### Blocking: cancel-then-drain can deadlock if result channel is undersized

The design states the orchestrator "drains remaining results (to unblock
workers)" after calling `cancel()`. This works only if the result channel buffer
is large enough to absorb all in-flight results without blocking workers. The
design says `results` is buffered to `len(classified)` (total repo count), which
is sufficient — workers can always send without blocking. This is correct as
specified.

**However**, the design does not explicitly state the buffer size for `jobs` and
`results` in the architecture section (only in the data flow narrative). The
implementation must buffer `results` to `len(classified)`, not to
`cloneWorkers` or some smaller constant. If an implementer uses a smaller buffer
(e.g., buffered to `cloneWorkers`), workers completing after the orchestrator
breaks the result loop would block forever trying to send, creating a goroutine
leak. The design should state the required buffer size as a constraint, not as
an incidental detail. **Blocking** in the sense that the design needs to make
this requirement explicit; the implementation risk is real.

### Advisory: Reporter.Status concurrent-access path

The design correctly identifies that workers must not call `Reporter` methods
directly — all Reporter access is on the orchestrator. `Reporter` has an
internal `sync.Mutex` (`mu`) and a background spinner goroutine, so concurrent
`Status` calls from workers would not corrupt memory, but they would produce
interleaved spinner resets. The design's single-threaded orchestrator model
avoids this entirely. This is architecturally sound and the reasoning is
explicit. No action needed — just noting the Reporter's mutex does not make
concurrent access safe for the *status semantics*, only for the underlying
state.

### Advisory: `SyncRepo` calls the Reporter directly

Looking at the current sequential path (line 793–795 of `apply.go`),
`SyncRepo` is called with `a.Reporter`. Inside `SyncRepo`, `r` receives git
output and is used for progress display. In the parallel design, workers call
`SyncRepo(ctx, targetDir, defaultBranch, a.Reporter)` — multiple workers would
call `SyncRepo` concurrently, all passing the same `*Reporter`. The design says
"workers do not call Reporter methods directly" and attributes Reporter safety
entirely to the orchestrator. This is contradicted by the SyncRepo call.

The reporter's internal mutex protects its fields. Concurrent `Status` calls
from `SyncRepo` across workers will race on the spinner message (last write
wins — not corrupted, but meaningless). This is an advisory issue: the spinner
message integrity is already degraded by the summary-progress design, so
garbled sync-phase status messages don't materially regress from what the design
already accepts. But the design's claim that "all Reporter access is on the
orchestrator" is inaccurate. A comment in the implementation noting this known
concurrent access would prevent future confusion.

---

## Findings Summary

| # | Severity | Finding |
|---|----------|---------|
| 1 | Blocking | `cloneResult` missing `cloneURL` — `RepoState.URL` will be empty in saved state |
| 2 | Blocking | Design must explicitly require `results` buffer = `len(classified)` to prevent goroutine leak on cancel-drain path |
| 3 | Advisory | "Workers do not call Reporter directly" claim is inaccurate — SyncRepo passes `a.Reporter`; note this in implementation comments |
| 4 | Advisory | Phase 2 should enumerate existing Status-assertion tests explicitly before describing updates as conditional |
