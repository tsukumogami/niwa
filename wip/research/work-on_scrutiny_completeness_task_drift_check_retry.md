# Completeness review: task_drift_check_retry

Branch: `fix/drift-check-retry` (commit `77ee477`)
Diff: `git diff main...HEAD`
Files changed:
- `internal/workspace/snapshotwriter.go`
- `internal/workspace/snapshotwriter_test.go`

Each AC is checked verbatim against the diff. The "evidence" cites file:line in
the post-merge tree.

---

## AC1 — Retry loop wraps `HeadCommit` (up to 3 retries / 4 attempts) on transient failures

**Implementation:**
- `internal/workspace/snapshotwriter.go:19-25` — `driftCheckBackoff` slice with 3
  entries (500ms, 1s, 2s); the comment explicitly states "len()==3 means up to
  3 retries on top of the initial attempt for 4 attempts total."
- `internal/workspace/snapshotwriter.go:138` — `refreshSnapshot` now calls
  `headCommitWithRetry(...)` instead of `fetcher.HeadCommit` directly.
- `internal/workspace/snapshotwriter.go:185-207` — `headCommitWithRetry`
  computes `attempts := len(driftCheckBackoff) + 1` (= 4), iterates
  `for i := 0; i < attempts; i++`, calls `fetcher.HeadCommit` each pass, and
  exits early on success or when `isTransientDriftError` returns false.
- `internal/workspace/snapshotwriter.go:166-176` — `isTransientDriftError`
  returns true for `status == 0` (transport error) and `502/503/504`.

**Verification (test):**
- `internal/workspace/snapshotwriter_test.go:354-378`
  (`TestRefreshSnapshot_TransientStatusCodes`) — exercises 502, 503, 504
  separately; expects `headCalls == 2` (1 fail + 1 success retry).
- `internal/workspace/snapshotwriter_test.go:281-321`
  (`TestRefreshSnapshot_AllRetriesFailEmitOneWarning`) — feeds 4 transient
  errors and asserts `headCalls == 4` ("1 initial + 3 retries").

**Status: PASS.**

---

## AC2 — Permanent failures (401/403/404) bypass retry loop

**Implementation:**
- `internal/workspace/snapshotwriter.go:166-176` — `isTransientDriftError`'s
  switch lists only `0, 502, 503, 504`. Any other non-zero status (including
  401/403/404 and 5xx outside the 502/503/504 set) returns false.
- `internal/workspace/snapshotwriter.go:200` — when
  `!isTransientDriftError(err, status)`, the loop returns immediately with the
  first attempt's result.

**Verification (test):**
- `internal/workspace/snapshotwriter_test.go:323-352`
  (`TestRefreshSnapshot_PermanentErrorBypassesRetry`) — 401 response, asserts
  `headCalls == 1` "no retry on 401" and exactly one warning is emitted.
- 403 and 404 are not individually tested, but the behavior is determined by
  the same `isTransientDriftError` switch that 401 already covers; the AC
  groups them together and the implementation treats them identically.

**Status: PASS.** (Advisory: 403 and 404 covered by the same code path as 401
but no dedicated tests; the AC says "bypass the retry loop and behave exactly
as today," which is satisfied by the switch statement and the 401 test.)

---

## AC3 — Replaceable `Reporter.Status(...)` note during retries; success leaves no permanent log line

**Implementation:**
- `internal/workspace/snapshotwriter.go:201-204` — between retry attempts, when
  `reporter != nil`, calls
  `reporter.Status(fmt.Sprintf("retrying drift check for %s/%s (attempt %d of %d)…", ...))`.
- `Reporter.Status` (`internal/workspace/reporter.go:62-77`) is replaceable by
  design: it overwrites `r.spinMsg` and the spinner line redraws in place. On
  success, the loop returns without calling `Warn`/`Log`, so no permanent line
  is written. The TTY spinner line is cleared by `stopSpinner` on the next
  `Log`/`Warn` (or remains transient until the writer flushes).

**Verification (test):**
- `internal/workspace/snapshotwriter_test.go:240-279`
  (`TestRefreshSnapshot_RetrySucceedsOnAttempt2`) — uses
  `NewReporterWithTTY(&buf, false)` (non-TTY), runs one transient + one success,
  and asserts `!strings.Contains(buf.String(), "warning:")` confirming no
  warning line on successful retry.

**Status: PASS.** (Advisory: the test runs with `isTTY=false`, so it does not
exercise the spinner replacement path directly — `Status` is a no-op on
non-TTY, see `reporter.go:63-65`. The AC's "replaceable" property is structural
(it's the same `Status` API already used elsewhere) rather than separately
verified; the AC's "successful retry leaves no permanent log line" is the part
that is directly tested.)

---

## AC4 — On total failure, existing warning emitted unchanged (same wording, same `prov.FetchedAt` formatting)

**Implementation:**
- `internal/workspace/snapshotwriter.go:139-147` — the warn block is unchanged
  from `main`:
  ```
  reporter.Warn("could not refresh config snapshot for %s: %v; using cached snapshot fetched at %s",
      prov.SourceURL, err, prov.FetchedAt.Format(time.RFC3339))
  ```
  Confirmed by comparing with `git show main:internal/workspace/snapshotwriter.go`
  at lines 137-138 of main — identical format string and arguments.

**Verification (test):**
- `internal/workspace/snapshotwriter_test.go:281-321`
  (`TestRefreshSnapshot_AllRetriesFailEmitOneWarning`) — asserts:
  - `warnCount == 1` (exactly one warning line, line 309-312)
  - `strings.Contains(buf.String(), "could not refresh config snapshot for org/repo")`
    (line 313-315) — same wording.
  - Cached `workspace.toml` is preserved (line 318-320).

**Status: PASS.**

---

## AC5 — Backoff bounded and short (500ms, 1s, 2s)

**Implementation:**
- `internal/workspace/snapshotwriter.go:22-25` — `driftCheckBackoff` is
  initialized to exactly `{500ms, 1s, 2s}` (matches the suggested schedule).
- `internal/workspace/snapshotwriter.go:205-209` — between attempts the loop
  waits via `time.After(driftCheckBackoff[i])` with a `ctx.Done()` cancel path.
- Total worst-case wait is bounded at 3.5s; comment on test helper (`snapshotwriter_test.go:71-73`) calls this out: "production schedule (~3.5s total)".

**Status: PASS.**

---

## AC6 — Non-TTY output unchanged (no spinner, no extra log lines on success)

**Implementation:**
- `Reporter.Status` is a documented no-op on non-TTY
  (`internal/workspace/reporter.go:62-65`: `if !r.isTTY { return }`). The retry
  status calls at `snapshotwriter.go:201-204` therefore produce no output when
  the reporter is non-TTY.
- The retry loop only adds `Status` calls (no `Log`/`Warn`) on the path between
  attempts, and only adds `Warn` on terminal failure (which already existed).
  No new permanent lines are written on success.

**Verification (test):**
- `internal/workspace/snapshotwriter_test.go:240-279`
  (`TestRefreshSnapshot_RetrySucceedsOnAttempt2`) — non-TTY reporter
  (`NewReporterWithTTY(&buf, false)`); confirms no warning line in buffer on
  success. Because `Status` is a no-op on non-TTY, this also implicitly
  confirms no spinner output.
- `internal/workspace/snapshotwriter_test.go:354-378`
  (`TestRefreshSnapshot_TransientStatusCodes`) — passes a `nil` reporter;
  confirms the code path is safe with no reporter at all (the `reporter != nil`
  guard at `snapshotwriter.go:201` prevents a panic).

**Status: PASS.**

---

## AC7 — Unit tests cover (a) retry succeeds on attempt 2, no warning; (b) all retries fail, exactly one warning matching today's format; (c) 401 not retried

**Evidence:**
- (a) `TestRefreshSnapshot_RetrySucceedsOnAttempt2`
  (`snapshotwriter_test.go:240-279`): scripts `[503, 200]`, asserts
  `headCalls == 2`, asserts no `"warning:"` substring in output.
- (b) `TestRefreshSnapshot_AllRetriesFailEmitOneWarning`
  (`snapshotwriter_test.go:281-321`): scripts `[503, 503, 503, 503]`, asserts
  `headCalls == 4`, asserts `warnCount == 1`, asserts
  `"could not refresh config snapshot for org/repo"` substring is present
  (today's wording), and asserts the cached snapshot is preserved.
- (c) `TestRefreshSnapshot_PermanentErrorBypassesRetry`
  (`snapshotwriter_test.go:323-352`): scripts a single 401 response, asserts
  `headCalls == 1` and `warnCount == 1`.

The fake fetcher at `snapshotwriter_test.go:21-66` was extended with scripted
per-call responses (`headErrs`, `headStatuses`, `headOIDs` slices indexed by
`headCalls`), and a `withFastDriftBackoff` helper at lines 70-76 zeroes the
backoff durations so retry tests run instantly.

**Status: PASS.**

---

## Summary

All 7 ACs are implemented and verified by tests in the diff. No blocking
omissions.

Advisory notes (do not violate any AC):
- AC2 lists 401/403/404 explicitly, but only 401 has a dedicated test. The
  switch statement covers all three identically, so behavior is correct.
- AC3 and AC6: the spinner-replacement behavior (`Status` overwriting prior
  status text in a TTY spinner line) is structural — it's the standard
  `Reporter.Status` contract used elsewhere in the package — and is not
  directly exercised in a TTY test in this diff. The success-path-emits-no-
  warning portion is directly tested.
