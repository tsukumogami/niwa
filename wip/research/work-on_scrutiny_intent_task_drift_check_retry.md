# Intent Scrutiny: task_drift_check_retry

**Branch:** fix/drift-check-retry (commit 77ee477)
**Files reviewed:**
- `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/niwa/internal/workspace/snapshotwriter.go`
- `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/niwa/internal/workspace/snapshotwriter_test.go`
- (context) `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/niwa/internal/workspace/reporter.go`
- (context) `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/niwa/internal/workspace/apply.go`
- (context) `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/niwa/internal/github/fetch.go`

The intent the user expressed: turn a single transient GitHub hiccup
during drift check into a silent recovery, with a brief in-line status
during retries, and only warn if recovery genuinely fails.

## 1. The user's actual experience on `niwa create -r niwa`

**End-to-end trace, transient 503 then success:**

1. `cmd/niwa/main.go` → `internal/cli/create.go:139` builds Reporter with
   TTY auto-detection from `os.Stderr`. Interactive shell ⇒ `isTTY=true`.
2. `Applier.Apply` (apply.go:314) calls `EnsureConfigSnapshotWithStatus`
   for the workspace config dir. No prior `Reporter.Status` is in flight
   at this point — the spinner is idle.
3. `refreshSnapshot` calls `headCommitWithRetry`.
4. Attempt 0 returns `(status=503, err="github: HeadCommit returned 503")`;
   `isTransientDriftError` returns true; `Reporter.Status("retrying drift
   check for niwa/niwa (attempt 2 of 4)…")` starts the spinner; sleeps
   500 ms.
5. Attempt 1 succeeds. Loop returns `(oid, status=200, nil)`.
6. `refreshSnapshot` updates `FetchedAt`, returns nil.

**Visible output on TTY:**

The user sees a single in-place line:

```
⠋ retrying drift check for niwa/niwa (attempt 2 of 4)…
```

advancing through spinner frames at ~100 ms cadence, with no permanent
log line written. No "warning:" line is emitted. So far, intent is met.

**However — the spinner does NOT stop after the successful retry.** The
spinner goroutine keeps redrawing the "retrying drift check…" message
until the next `Log` or `Warn` call (which calls `stopSpinner`
internally) or until apply finishes. After this drift check, the next
reporter call in `Apply` is far downstream (apply.go:679 sets a NEW
`Status("syncing config...")` for the *overlay* sync, or apply.go:916
sets `Status("cloning repos...")`). Status overwrites the spinner
message in place, so the user sees it morph from "retrying drift check
for niwa/niwa…" to "syncing config…" to "cloning repos…" — that's fine.

But for the wall-clock duration between successful retry (step 6) and
the next `Status` call, the spinner displays "retrying drift check" even
though we've moved on. On a fast machine that window is short (vault
resolution, manifest parsing) but on a slow path with no progress
events, the user sees a stale, spinning "retrying drift check" message
that lies about the current state. The user's wording "print a
replaceable status note while retrying" implies the note should not
outlive the retrying. Severity: advisory — the message is replaced
eventually, but the visible state during that gap is misleading.

**Visible output on non-TTY (CI, redirect):**

`Reporter.Status` is a no-op when `!isTTY`. The retry path produces
*zero* output on success, exactly matching the user's intent: "successful
retry leaves no permanent log line." On final failure, the existing
`Warn` produces one warning line. Non-TTY behavior is correct.

## 2. The replaceable-status promise

`Reporter.Status` is implemented in `reporter.go:62-77`. The first call
starts a goroutine (`spinLoop`) that ticks at 100 ms; subsequent calls
just update `r.spinMsg` under the mutex. `doTick` writes
`"\r\033[K%s %s"` — carriage return, ANSI clear-line, frame, message —
so each tick rewrites the same line in place. Multiple `Status` calls
collapse into a single growing-then-shrinking spinner line, which is
exactly the "replaceable status note" semantic the user asked for.

Within `headCommitWithRetry`, only a single `Status` call is made per
retry iteration (between attempts). With three retries the user sees up
to three message updates, all rendered to the same in-place line. No
log stack grows. This part of the intent is met.

One implementation detail worth noting: when `Status` is called for the
first time in the retry loop, the goroutine launches and immediately
ticks (`doTick()` before the 100ms ticker), so the user sees the message
appear within roughly the time it takes to `Reporter.Status` to lock the
mutex and start a goroutine — effectively instantly. Good UX on a
flaky-network 503.

## 3. Foundation for follow-on work

`headCommitWithRetry` has the signature:

```go
func headCommitWithRetry(ctx context.Context, fetcher FetchClient, src source.Source, reporter *Reporter) (oid string, status int, err error)
```

It is bound to the `HeadCommit` shape (returns oid+status) and the
status-text it emits is hard-coded to "drift check" (snapshotwriter.go:197).
The two FetchClient methods are `HeadCommit` and `FetchTarball` —
`FetchTarball` returns a different shape (body, etag, status, redirect,
err) so the same helper can't directly retry it. A future task that
wants to retry `FetchTarball` (e.g., 503 during `materializeFromGitHub`
mid-fetch) would need a different helper, but it could share the
`isTransientDriftError` classifier (renamed) and the `driftCheckBackoff`
schedule (also renamed). Modest forking required, not a deep
re-architecture. Severity: advisory — the helper is shaped narrowly but
the *parts* (transient classifier, backoff schedule, ctx-aware sleep
loop) are reasonably reusable. The classifier function is named
`isTransientDriftError` and the var is `driftCheckBackoff`, so the
naming would need to be generalized first; that's not a blocker.

The `isTransientDriftError` classifier intentionally excludes 500. On
GitHub specifically that's a defensible call — generic 500s from GH
tend to be persistent app errors, not transient gateway flaps — but a
future caller retrying `FetchTarball` may want to treat 500 as transient
too (mid-stream tarball failures often present as 500). The narrow set
{0, 502, 503, 504} matches the user's stated AC verbatim, so this is
fine for the current task; just flagging that the next task will
probably want to widen the set.

## 4. PRD R21 alignment

R21: "on network error during drift check, continue with cached snapshot
and warn." The retry-exhausted path in `refreshSnapshot` (snapshotwriter.go:139-147)
is unchanged in structure — same `Warn` format, same fallback
(return nil so the cached snapshot stays). The error wrapping is also
unchanged: `fetcher.HeadCommit` returns `"github: HeadCommit returned
503"` (fetch.go:72), and `headCommitWithRetry` returns that same error
verbatim from the last attempt. The `Warn` format string —

```go
"could not refresh config snapshot for %s: %v; using cached snapshot fetched at %s"
```

— and the args (`prov.SourceURL`, `err`, `prov.FetchedAt.Format(time.RFC3339)`)
are byte-identical to today. Verified by `TestRefreshSnapshot_AllRetriesFailEmitOneWarning`
(snapshotwriter_test.go:309-315) which asserts both the warning count
(exactly 1) and the substring `"could not refresh config snapshot for
org/repo"`. R21 contract preserved.

One subtle point: when context is cancelled mid-retry,
`headCommitWithRetry` returns `ctx.Err()` (snapshotwriter.go:203). That
error then flows into the same `Warn` and `return nil` path. Pre-fix,
ctx cancellation during HeadCommit would also surface as an error from
the underlying http.Client. Both paths behave identically (warn + fall
back to cached). No semantic regression.

## 5. Signal-to-noise on the user's terminal

The status format string is:

```
"retrying drift check for %s/%s (attempt %d of %d)…"
```

→ `retrying drift check for niwa/niwa (attempt 2 of 4)…`

This is unambiguous: a confused user can tell the difference between
"stuck" and "retrying" because the message includes the word "retrying"
plus an attempt counter. Combined with the spinner advancing at 10 fps,
the visible feedback is "we know what we're doing, please wait." The
total wall-clock for the retry path is bounded by 0.5 + 1 + 2 = 3.5
seconds plus four HeadCommit RTTs, which is acceptable for an interactive
`niwa create`. Good signal.

Two minor signal-quality concerns:

a. **Trailing horizontal ellipsis (`…`, U+2026) vs ASCII `...`.** Other
`Status` callers in the codebase use ASCII triple-dot ("syncing config..."
apply.go:679; "cloning repos..." apply.go:916). The retry message uses
the Unicode ellipsis. On most modern terminals this renders fine, but
inconsistency with the surrounding messages is a small style smell.
Severity: advisory.

b. **Counter format "attempt 2 of 4".** Total is 4 attempts (1 initial
+ 3 retries). On the first retry the user sees "attempt 2 of 4", which
is correct but a confused user might wonder "wait, where was attempt 1?"
because they didn't see anything before the failure (the initial attempt
was silent). Not wrong, just slightly counterintuitive. Could be
"retrying (1 of 3)…" to match the user's mental model of "3 retries on
top of the first try", or could stay as-is. Severity: advisory.

## Summary

Net assessment: the implementation matches the user's intent on the
critical experience axis (silent recovery on transient 503, single
warning on full failure, R21 contract preserved). One real intent gap
exists — after a successful retry the spinner doesn't get cleared until
the next reporter call, leaving a stale "retrying drift check" message
visible on the terminal during the gap — but this is an advisory finding,
not a blocker, since (a) the message is eventually replaced or cleared by
downstream `Status` / `Log` calls, (b) the gap is short on the typical
`niwa create` path, and (c) the user did not explicitly call out
"clear the status when retry succeeds" in the AC. Worth a follow-up
issue rather than gating this PR.

## Findings table

| # | Severity  | Concern                                                                                                                |
|---|-----------|------------------------------------------------------------------------------------------------------------------------|
| 1 | advisory  | Spinner is not cleared after successful retry; "retrying drift check…" persists on screen until next `Status`/`Log`.   |
| 2 | advisory  | `isTransientDriftError` and `driftCheckBackoff` are named for drift-check; future `FetchTarball` retry will need to generalize/rename. |
| 3 | advisory  | Status string uses Unicode ellipsis `…` while surrounding `Status` callers use ASCII `...` — minor inconsistency.       |
| 4 | advisory  | "attempt 2 of 4" counter is technically correct but slightly counterintuitive given the first attempt was silent.       |

No blocking findings.
