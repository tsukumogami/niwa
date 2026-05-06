# Architect Review — `fix/drift-check-retry`

Branch: `fix/drift-check-retry` (commits `77ee477` + `0cdd65a`)
Scope: `internal/workspace/snapshotwriter.go`, `internal/workspace/snapshotwriter_test.go`

Verdict: **fits the existing architecture**. No blocking findings; two advisory observations about future-proofing.

---

## 1. Layering — retry lives in `internal/workspace/`, not `internal/github/`

**Finding: correct layer.**

The `FetchClient` interface in `internal/workspace/snapshotwriter.go:32-35` is a workspace-owned port whose only production implementor happens to be `*github.APIClient`. The retry semantics (transient-status set, backoff schedule, when-to-warn-vs-error) are policy decisions that belong to the *consumer* of the port, not the implementor.

Two concrete signals the layer is right:

- The other `HeadCommit` call site in this same package — `materializeFromGitHub` at `internal/workspace/snapshotwriter.go:446` — uses HeadCommit *best-effort* to populate the resolved-commit marker after a successful tarball extract. If that call returns 503, the caller swallows the error and falls back to oid="" (later replaced by "unknown" at line 355). Retrying *that* call would be wrong: it would stretch the post-tarball window for no user benefit. So the two HeadCommit call sites already have *different* semantics, which means a retry baked into `APIClient.HeadCommit` would be wrong-for-one regardless of which one you optimized for.
- A future `FetchTarball` retry (e.g., during `materializeAndSwap`) needs different policy too — a transient mid-stream failure on a tarball download requires re-issuing the request, not retrying after the body has been partially consumed. That's a distinct retry strategy, not the same one applied to a different verb.

If the policy were generic ("always retry GitHub 5xx N times with backoff X"), `internal/github/` would be the right home. It isn't generic, so it isn't.

The `FetchClient` contract is unchanged — same method set, same signatures (`internal/workspace/snapshotwriter.go:32-35`). `*github.APIClient` continues to satisfy it without modification.

## 2. Interface and function contracts

**Finding: contracts preserved.**

- **`FetchClient.HeadCommit` contract** (`internal/workspace/snapshotwriter.go:33`): unchanged. The retry helper at `:187-209` calls `fetcher.HeadCommit(ctx, src.Owner, src.Repo, ref, "")` with the same arguments and reads the same return tuple as the previous direct call.
- **`refreshSnapshot` / `EnsureConfigSnapshot` PRD R21 contract** (warn-and-cache on transient failure, return nil): preserved. The post-retry branch at `:139-147` is the same shape as before — the only change is that "the err passed in" now reflects the *last* attempt rather than the only attempt. The warn message and `return nil` are unchanged. Verified against PRD R21 at `docs/prds/PRD-workspace-config-sources.md:395-401`.
- **`isTransientDriftError` boundary** (`:169-178`): permanent statuses (401/403/404/500/etc.) bypass the retry by returning on the first attempt — the test `TestRefreshSnapshot_PermanentErrorBypassesRetry` at `internal/workspace/snapshotwriter_test.go:323-352` exercises this.

One subtle observation: `isTransientDriftError(err==nil, status==304)` returns `false` because of the early `if err == nil` guard at `:170`. That's correct — a 304 is success-with-no-drift, and the surrounding `refreshSnapshot` already handles it at `:148`.

## 3. Dependency direction

**Finding: no inversions.**

`headCommitWithRetry` depends on:
- `context` (stdlib)
- `time` (stdlib)
- `fmt` (stdlib)
- `*Reporter` — same package (`internal/workspace/reporter.go`)
- `source.Source` — `internal/source/`. Verified `internal/source` has zero internal imports (grep for `tsukumogami/niwa/internal` in that package returns no matches), so workspace → source is unambiguously downward.
- `FetchClient` — same package interface.

No new package edges introduced. No cycles. No upward references.

## 4. Coupling to caller behavior — `var driftCheckBackoff` as a test seam

**Finding: acceptable for this codebase, with caveats.**

`internal/workspace/snapshotwriter.go:23-27` declares a package-level mutable slice that tests override via `withFastDriftBackoff(t)` at `internal/workspace/snapshotwriter_test.go:68-73`. This pattern is established here — `internal/cli/mesh_watch.go` uses the same approach for three timing seams:

- `mesh_watch.go:413` — `var orphanPollInterval = 2 * time.Second`
- `mesh_watch.go:623` — `var testPausePollInterval = 100 * time.Millisecond`
- `mesh_watch.go:1464` — `var watchdogPollInterval = 2 * time.Second`

So the choice fits a codebase convention, not a one-off improvisation. Two contrasts worth noting:

- `mesh_watch.go` *also* keeps the production retry schedule on a struct (`supervisor.backoffs` at `:388`, set from env at `:1399`), separate from the test seams. The supervisor's *user-facing* configurability lives on the struct; the *test-only* time-warp lives in the package var. The drift-check change conflates the two: production tuning and test override happen through the same `var`. That's fine while the only knob is "tests want zeros," but if a future PRD adds `NIWA_DRIFT_RETRY_BACKOFF_SECONDS` or similar, the cleanest move is to mirror mesh_watch's split (struct field for the configured value, package var only for the test seam, or a single field passed by the caller).
- The package var is mutated by `t.Cleanup`, not in parallel-safe form. Tests in this file don't run with `t.Parallel()`, so it works, but anyone adding `t.Parallel()` to a retry test would see flakes. This is implicit, not enforced.

Neither concern is blocking. The pattern is consistent with the codebase, the tests don't run in parallel, and there is no production knob to expose yet.

A struct-field alternative would have meant adding a field to either `Reporter` (wrong place — Reporter is purely IO) or threading a new `RetryConfig` parameter through `EnsureConfigSnapshot`/`EnsureConfigSnapshotWithStatus`/`refreshSnapshot` — three signatures touched, every caller updated, for a value that today has exactly one production setting. The package var is the proportionate choice.

## 5. Future extension cost — second site (e.g., `FetchTarball` retry)

**Finding: low-to-moderate lift, with one rename to anticipate.**

If `materializeAndSwap` wants similar retry semantics for `FetchTarball` (e.g., a 503 mid-fetch), the work splits as follows:

- **Reusable as-is**: nothing. The names are drift-check-specific:
  - `driftCheckBackoff` (`:23`) — name is bound to the use case.
  - `isTransientDriftError` (`:169`) — name claims drift-check ownership.
  - `headCommitWithRetry` (`:187`) — bound to the HeadCommit shape, can't wrap FetchTarball's `(body, etag, status, redirect, err)` return.

- **Conceptually reusable**: `isTransientDriftError`'s status set (`0, 429, 502, 503, 504`) is generic GitHub-transient classification, not drift-specific. A future caller will likely want the same set, possibly extended. Renaming this to `isTransientGitHubStatus(err, status int) bool` and keeping its body as-is would let the second caller import it without copy-paste. That rename costs one search-replace today (no public API impact — it's lowercase) and saves a duplicate predicate later.

- **Net new for FetchTarball retry**: a separate `fetchTarballWithRetry` helper (different return shape), and a decision about whether to share the backoff schedule (rename `driftCheckBackoff` → `githubRetryBackoff`) or keep two schedules. The `wip/research/work-on_scrutiny_intent_task_drift_check_retry.md:205` advisory note captures this same observation.

Estimate: ~30 lines for the second helper plus rename cleanup if the team wants the predicate shared. Not a refactor — additive.

The current code does not foreclose any of this. The drift-check-specific naming is an honest reflection of "we only need this here today," not an over-fit. Renaming when the second use case shows up is mechanical.

---

## Summary table

| # | Severity | Topic | Finding |
|---|----------|-------|---------|
| 1 | OK        | Layering                  | Retry lives in `internal/workspace/` because the policy is consumer-specific (different from `materializeFromGitHub`'s best-effort HeadCommit at `:446`). |
| 2 | OK        | Interface contracts       | `FetchClient.HeadCommit` and `refreshSnapshot`/PRD R21 contracts preserved. |
| 3 | OK        | Dependency direction      | No new package edges; no inversions. |
| 4 | Advisory  | `var driftCheckBackoff`   | Acceptable per codebase convention (matches `mesh_watch.go` test seams), but conflates "production schedule" with "test override" in one var; revisit if a user-facing knob is added. |
| 5 | Advisory  | Future extension cost     | Names are drift-check-specific. Renaming `isTransientDriftError` → `isTransientGitHubStatus` when (if) a `FetchTarball` retry lands is the only mechanical cleanup; no structural rework needed. |
