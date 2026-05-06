# QA Validation: drift-check retry fix

Branch: `fix/drift-check-retry`
Commits validated: 77ee477, 0cdd65a, 6c8658e
Working dir: `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/niwa`

## Summary

All seven acceptance criteria pass. The targeted unit test set, the full
workspace test suite, the reporter tests, and `go build ./...` all run clean.
Tests genuinely exercise the retry loop (assertions on `headCalls` and
`spinFrames` confirm the production code path is hit, not stubbed). The
`driftCheckBackoff` slice is a real schedule (500ms / 1s / 2s ≈ 3.5s worst
case) and the test override (`withFastDriftBackoff`) only mutates it for the
duration of a single test via `t.Cleanup`, so there is no leak into
production behavior.

---

## Section 1 — Unit test execution

**Command:**
```
go test ./internal/workspace/ -run "TestRefreshSnapshot_|TestEnsureConfigSnapshot_NetworkErrorPreservesCachedSnapshot" -v -count=1
```

**Result:**
```
=== RUN   TestEnsureConfigSnapshot_NetworkErrorPreservesCachedSnapshot
--- PASS: TestEnsureConfigSnapshot_NetworkErrorPreservesCachedSnapshot (0.00s)
=== RUN   TestRefreshSnapshot_RetrySucceedsOnAttempt2
--- PASS: TestRefreshSnapshot_RetrySucceedsOnAttempt2 (0.01s)
=== RUN   TestRefreshSnapshot_AllRetriesFailEmitOneWarning
--- PASS: TestRefreshSnapshot_AllRetriesFailEmitOneWarning (0.00s)
=== RUN   TestRefreshSnapshot_PermanentErrorBypassesRetry
--- PASS: TestRefreshSnapshot_PermanentErrorBypassesRetry (0.00s)
=== RUN   TestRefreshSnapshot_TransientStatusCodes
=== RUN   TestRefreshSnapshot_TransientStatusCodes/status_429
=== RUN   TestRefreshSnapshot_TransientStatusCodes/status_502
=== RUN   TestRefreshSnapshot_TransientStatusCodes/status_503
=== RUN   TestRefreshSnapshot_TransientStatusCodes/status_504
--- PASS: TestRefreshSnapshot_TransientStatusCodes (0.00s)
PASS
ok  github.com/tsukumogami/niwa/internal/workspace  0.036s
```

**Verdict:** PASS — 5 top-level tests / 8 sub-cases all green.

**Genuineness check:** `headCalls` is asserted in every test, so the
fake fetcher's increment is the source of truth that the retry loop
ran the expected number of times:
- `TestEnsureConfigSnapshot_NetworkErrorPreservesCachedSnapshot`: asserts
  `headCalls == len(driftCheckBackoff)+1 == 4` (transport error → all
  retries exhausted).
- `TestRefreshSnapshot_RetrySucceedsOnAttempt2`: asserts `headCalls == 2`
  (one transient 503, then success — proves the loop exits early on success).
- `TestRefreshSnapshot_AllRetriesFailEmitOneWarning`: asserts `headCalls == 4`
  and `warnCount == 1`.
- `TestRefreshSnapshot_PermanentErrorBypassesRetry`: asserts `headCalls == 1`
  for HTTP 401 (proves permanent classification short-circuits the loop).
- `TestRefreshSnapshot_TransientStatusCodes`: per-status assertion that 429,
  502, 503, 504 each cause exactly one retry to recover (`headCalls == 2`).

---

## Section 2 — Behavior trace (code walk)

### AC1: refreshSnapshot retries up to 3 times on transient failures

Satisfied at `internal/workspace/snapshotwriter.go`:
- Line 23-27: `driftCheckBackoff = []time.Duration{500ms, 1s, 2s}` — 3-element
  slice → 3 retries.
- Line 138: `refreshSnapshot` calls `headCommitWithRetry` (not the bare
  fetcher).
- Line 193: `attempts := len(driftCheckBackoff) + 1` — 4 attempts total.
- Line 194-208: the loop body calls `fetcher.HeadCommit`, classifies via
  `isTransientDriftError`, and on transient outcomes either retries (sleeps
  `driftCheckBackoff[i]`) or returns the last error after exhaustion.

### AC2: Permanent failures bypass retry

Satisfied at `internal/workspace/snapshotwriter.go`:
- Line 169-178: `isTransientDriftError` returns true only for `status` in
  `{0, 429, 502, 503, 504}`. 401/403/404/500 fall through to `return false`.
- Line 196: `if !isTransientDriftError(err, status) || i == attempts-1`
  returns immediately on permanent error (first attempt, no sleep, no Status
  message). Confirmed by `TestRefreshSnapshot_PermanentErrorBypassesRetry`
  (`headCalls == 1` for status 401).

Important nuance: `isTransientDriftError` requires `err != nil` (line
170-172). A 200/304 with `err == nil` cannot be misclassified as transient —
the retry loop only triggers when `fetcher.HeadCommit` actually fails.

### AC3: Replaceable Status line during retries; no permanent log on success

Satisfied:
- Line 199-202: between attempts, `reporter.Status(...)` is called with a
  message like `"retrying drift check for org/repo (retry 1 of 3)..."`.
- `Reporter.Status` (`reporter.go:62-77`) is a no-op on non-TTY, and on TTY
  uses the spinner goroutine. The spinner's render path
  (`doTick`, `reporter.go:103-112`) emits `\r\033[K` (carriage-return +
  ANSI clear-to-end-of-line) before each frame, which is the standard
  "replaceable line" sequence.
- On successful retry, `headCommitWithRetry` returns with `err == nil`;
  `refreshSnapshot` proceeds to the no-drift / drift branches without
  calling `reporter.Warn`. No permanent log line is emitted.
  `TestRefreshSnapshot_RetrySucceedsOnAttempt2` asserts
  `!strings.Contains(buf.String(), "warning:")`.
- ASCII text confirmed (commit 0cdd65a renamed an earlier non-ASCII variant):
  the message is plain ASCII per the source.

### AC4: All-failures path emits the existing warning unchanged

Satisfied:
- Line 138-147: the catch block in `refreshSnapshot` is unchanged from the
  pre-retry version. After `headCommitWithRetry` exhausts retries it returns
  the last error; `refreshSnapshot` calls
  `reporter.Warn("could not refresh config snapshot for %s: %v; using cached snapshot fetched at %s", ...)`.
- The test
  `TestRefreshSnapshot_AllRetriesFailEmitOneWarning` asserts exactly
  `warnCount == 1` and that the substring
  `"could not refresh config snapshot for org/repo"` is present.

### AC5: Backoff bounded ≈ 3.5s worst case

Satisfied:
- Line 23-27: schedule sums to `0.5 + 1.0 + 2.0 = 3.5s` of sleeps. Plus four
  HeadCommit round-trips (each subject to `c.HTTPClient` timeout, not part
  of the backoff). The caller-context cancellation is honored at line 205-207
  (`select { case <-time.After(...); case <-ctx.Done(): return ctx.Err() }`).

### AC6: Non-TTY output unchanged

Satisfied:
- `Reporter.Status` returns immediately on non-TTY (`reporter.go:63-65`):
  `if !r.isTTY { return }`. So on piped output, no spinner, no extra
  characters between attempts.
- On success, no `Log`/`Warn` is invoked. On all-fail, the single existing
  `Warn` line is emitted unchanged. Verified by reading
  `TestRefreshSnapshot_RetrySucceedsOnAttempt2` (uses
  `NewReporterWithTTY(&buf, false)` and asserts no `"warning:"` substring).

### AC7: Unit tests cover the three required cases

| AC sub-case | Test |
|---|---|
| (a) success on retry | `TestRefreshSnapshot_RetrySucceedsOnAttempt2` |
| (b) all-fail single warning | `TestRefreshSnapshot_AllRetriesFailEmitOneWarning` |
| (c) 401 bypass | `TestRefreshSnapshot_PermanentErrorBypassesRetry` |

Bonus coverage: `TestRefreshSnapshot_TransientStatusCodes` parameterizes
over {429, 502, 503, 504} and confirms each is treated as transient; the
pre-existing `TestEnsureConfigSnapshot_NetworkErrorPreservesCachedSnapshot`
was upgraded (commit 6c8658e) to assert `headCalls == 4` so a future
regression that disables the retry loop would fail this test.

### Test-only artifact check

The test override is:
```
var driftCheckBackoff = []time.Duration{
    500 * time.Millisecond,
    1 * time.Second,
    2 * time.Second,
}
```
This is a real production schedule. The fast-backoff helper
(`snapshotwriter_test.go:71-76`) saves the original via
`t.Cleanup(func() { driftCheckBackoff = orig })`, so a test that fails
mid-run can't leave `{0,0,0}` in the global. Confirmed by reading the
helper source.

**Verdict:** All ACs traceable to the implementation; no test-only
artifact leaks into production.

---

## Section 3 — Status TTY replaceability

The spinner render in `reporter.go:103-112`:
```
fmt.Fprintf(r.w, "\r\033[K%s %s", frame, r.spinMsg)
```
emits `\r\033[K` (CR + ANSI EL "erase to end of line") before each frame.
That sequence is exactly the standard replaceable-line pattern.

Existing reporter tests cover this:
- `TestReporterSpinnerTickFormat` (lines 167-188): asserts each `doTick`
  output starts with `"\r\033[K"`.
- `TestReporterMultipleStatusUpdates` (lines 226-242): calls `Status` three
  times then `Log("complete")`, asserts the buffer ends with
  `"\r\033[Kcomplete\n"` — confirms each prior `Status` line was overwritten,
  not appended.
- `TestReporterTTYLogClearsStatus` and `TestReporterTTYWarn` confirm that
  switching from spinner mode to a permanent line clears the spinner first.

I ran:
```
go test ./internal/workspace/ -run TestReporter -v -count=1
```
All 14 reporter tests passed.

**No new dedicated TTY-mode unit test exists for the retry path
specifically** (i.e., a test that asserts `Status` is invoked between retries
with the exact expected message). This is a coverage gap but a small one —
the production code (line 199-202) calls `reporter.Status(...)` with a
literal format string, and the existing TTY tests already prove that
`Status` produces the replaceable behavior. A focused test would be nice
to have but is not required to be confident in correctness; per the task
instructions I am not adding it.

**Verdict:** PASS — the carriage-return + ANSI-clear sequence is verified
by existing tests; the retry path delegates to the validated `Status`
mechanism.

---

## Section 4 — Smoke build

**Command:**
```
go build ./...
```

**Result:** clean build, no output, exit 0.

**Verdict:** PASS.

---

## Section 5 — Manual mental walkthrough

Scenario: `niwa create -r niwa` against a workspace with an existing snapshot.
GitHub returns transient 503 once, then 200 with the cached oid (no drift).

Code flow:
1. `apply.go` calls `EnsureConfigSnapshot` → `refreshSnapshot` (case 1, marker
   present).
2. `refreshSnapshot` calls `headCommitWithRetry`.
3. Attempt 0: `HeadCommit` returns `(oid="", status=503, err="github: HeadCommit returned 503")`.
4. `isTransientDriftError(err, 503) == true`, `i == 0 != attempts-1 == 3`,
   so we sleep.
5. Before sleep, `reporter.Status("retrying drift check for niwa/<repo> (retry 1 of 3)...")`.
6. After 500ms sleep, attempt 1: `HeadCommit` returns
   `(oid=<same as cached>, status=200, err=nil)`.
7. `isTransientDriftError(nil, 200) == false` → return immediately.
8. Back in `refreshSnapshot`, oid matches cached → update `FetchedAt` and
   write marker.

What the user sees:
- **TTY mode**: Briefly (during the ~500ms wait), a spinner line shows
  `⠋ retrying drift check for niwa/<repo> (retry 1 of 3)...`. The frame
  cycles every ~100ms. When the second `HeadCommit` succeeds, the next
  `Log` or `Warn` (none here, since success is silent) would clear the
  line; absent that, the next CLI output line (the apply summary line)
  is preceded by `\r\033[K` only when a Log is invoked. The spinner
  goroutine continues until `Log`/`Warn`/`stopSpinner` runs or the process
  exits — which is fine since `apply` always emits a summary `Log` line.
  No "warning:" line on a successful retry.
- **Non-TTY (piped)**: nothing is printed during the retry. On success,
  nothing is printed. On all-fail, the existing single `warning:` line is
  emitted, exactly as before this fix.

**Verdict:** matches AC3 and AC6.

---

## Section 6 — Regression check (full workspace suite)

**Command:**
```
go test ./internal/workspace/ -count=1
```

**Result:**
```
ok  github.com/tsukumogami/niwa/internal/workspace  5.861s
```

**Verdict:** PASS — no regressions.

---

## Acceptance Criteria Summary

| AC | Status | Notes |
|----|--------|-------|
| 1. Retries up to 3 times on transient | PASS | `driftCheckBackoff` length 3 → 4 attempts total. |
| 2. Permanent failures bypass retry | PASS | `isTransientDriftError` whitelist {0,429,502,503,504}; tested for 401. |
| 3. Replaceable Status line; no log on success | PASS | `Reporter.Status` uses `\r\033[K` per existing tests; success path emits no log. |
| 4. All-fail warning unchanged | PASS | `Reporter.Warn` and message text unchanged from pre-fix. |
| 5. Bounded ≤ ~3.5s | PASS | 0.5+1+2=3.5s sleep budget plus 4 round-trips, ctx-cancellable. |
| 6. Non-TTY unchanged | PASS | `Status` early-returns on non-TTY; success silent; all-fail = same single warning. |
| 7. Unit tests cover (a)(b)(c) | PASS | Three named tests + parameterized status-code coverage + upgraded existing test. |

## Scenarios

- Scenarios run: 6 (5 unit-test invocations covering 8 cases, 1 full-suite, 1 build, 1 reporter-suite, 1 mental walkthrough)
- Counted as: 6 distinct validation steps, all passing
- Failures: 0
