# Maintainer Review — `fix/drift-check-retry` (77ee477 + 0cdd65a)

Scope: maintainability only. The diff adds a retry loop around `fetcher.HeadCommit`
in the snapshot refresh path, plus four new tests. The mechanics are a small,
self-contained change; most findings below are about whether the next developer
will read the code correctly without follow-up questions.

Files reviewed:
- `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/niwa/internal/workspace/snapshotwriter.go`
- `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/niwa/internal/workspace/snapshotwriter_test.go`

---

## 1. Naming

| Symbol | Verdict | Notes |
|---|---|---|
| `headCommitWithRetry` | Clear | Tells the reader "this is a thin wrapper over HeadCommit that adds retry". Good. |
| `isTransientDriftError` | Clear, with one nit | The "drift" qualifier in the name is helpful (it's not a generic transient classifier — the status set is specific to this code path). |
| `driftCheckBackoff` | Clear | Reads as "the backoff schedule for drift checks". Length-encodes attempt count, which is unusual but explained in the doc. See finding 3.1. |
| `withFastDriftBackoff` | Clear in context | Matches the Go convention of `withX(t)`-style test helpers. Good. |

No name-behavior mismatches. **No findings here.**

---

## 2. Implicit contracts

### 2.1 `headCommitWithRetry` defaults `ref` to "HEAD" silently — Advisory
**Location:** `internal/workspace/snapshotwriter.go:188-191`

```go
ref := src.Ref
if ref == "" {
    ref = "HEAD"
}
```

The wrapper substitutes "HEAD" when `src.Ref` is empty, mirroring what
`materializeFromGitHub` (line 416-419) already does. This is consistent with the
rest of the file, so the next developer will probably guess right. But the doc
comment on `headCommitWithRetry` (lines 180-186) doesn't mention it.

**Misread risk:** low — anyone reading the loop will see the substitution
inline. **Not blocking**, but adding one line ("Empty src.Ref is treated as
HEAD, matching materializeFromGitHub.") in the doc would prevent the
divergent-twins trap (finding 4.1) from getting worse if either site changes.

### 2.2 `withFastDriftBackoff` mutates a package-level global — Advisory
**Location:** `internal/workspace/snapshotwriter_test.go:66-73`

```go
func withFastDriftBackoff(t *testing.T) {
    t.Helper()
    orig := driftCheckBackoff
    driftCheckBackoff = []time.Duration{0, 0, 0}
    t.Cleanup(func() { driftCheckBackoff = orig })
}
```

The doc comment explains *why* (zero out so we don't sleep ~3.5s) but doesn't
flag that this is a global mutation, which makes the helper unsafe to call from
a `t.Parallel()` test alongside any other test that exercises drift checks.

Today no test in this file calls `t.Parallel()`, so this is purely latent.
But "test helper that mutates a package global" is exactly the trap a future
developer can step into when they add a parallel test six months from now and
get a flaky timing result.

**Recommendation:** Either rename to `withFastDriftBackoffSerial` (signal in
the name) or add one line to the doc comment: `Not safe under t.Parallel():
mutates a package-level slice.` **Not blocking** because the hazard is latent.

### 2.3 `attempts` counter assumes `driftCheckBackoff` non-empty assumption — Advisory
**Location:** `internal/workspace/snapshotwriter.go:192-207`

```go
attempts := len(driftCheckBackoff) + 1
for i := 0; i < attempts; i++ {
    oid, _, status, err = fetcher.HeadCommit(ctx, src.Owner, src.Repo, ref, "")
    if !isTransientDriftError(err, status) || i == attempts-1 {
        return oid, status, err
    }
    ...
    select {
    case <-time.After(driftCheckBackoff[i]):
```

If a future developer sets `driftCheckBackoff = []time.Duration{}`, then
`attempts == 1` and the loop does exactly one attempt with no retry. That
behaves correctly. So this is safe — but the test helper sets it to
`{0, 0, 0}` rather than `nil`, suggesting the author believed the
length-as-retry-count contract requires a non-empty slice. It doesn't, but the
discrepancy invites confusion.

**Misread risk:** low. **Not blocking.** Mentioning here only because if
finding 3.1 is acted on (clearer doc on `driftCheckBackoff`), this resolves.

---

## 3. Comments

### 3.1 `driftCheckBackoff` doc — accurate but mathy — Advisory
**Location:** `internal/workspace/snapshotwriter.go:19-22`

```go
// driftCheckBackoff is the wait schedule used by headCommitWithRetry.
// Its length determines the number of retries (e.g., len()==3 means
// up to 3 retries on top of the initial attempt for 4 attempts total).
// Tests override this slice to skip real waits.
```

Accurate. The "len()==3 means 3 retries on top of the initial attempt for 4
attempts total" phrasing is correct — `attempts := len(driftCheckBackoff) + 1`
on line 192. **Not a blocking finding**, but for a future reader the relationship
would be more obvious if the slice itself were named e.g. `driftCheckRetryBackoff`
(making it explicit these are *retry* delays, not initial-attempt delays).

### 3.2 `isTransientDriftError` doc accurately matches the switch — No finding
**Location:** `internal/workspace/snapshotwriter.go:162-178`

The comment lists "status == 0", "429", "502/503/504" as transient and "401/403/404/500/etc."
as permanent. The switch on line 174 covers exactly `0, 429, 502, 503, 504`. They
match. The 500-is-permanent / 502-503-504-is-transient split is a deliberate
calibration call (GitHub gateway/upstream errors recover quickly; a true 500
suggests something is broken on the API server side and isn't expected to
self-heal in seconds). The comment captures that reasoning. Good.

### 3.3 `headCommitWithRetry` doc — accurate — No finding
**Location:** `internal/workspace/snapshotwriter.go:180-186`

The phrase "replaceable Reporter.Status note" matches the actual Reporter
contract: `Status` redraws the line on TTY (line 62 of reporter.go) and is a
no-op on non-TTY. The doc's claim that "the final error returned matches the
most recent attempt and feeds the existing warn-and-cache fallback unchanged"
matches the loop body and the call site at line 138-147. Accurate.

---

## 4. Divergent twins

### 4.1 Two `ref == "" → "HEAD"` substitutions — Advisory
**Location:** `internal/workspace/snapshotwriter.go:188-191` and `:416-419`

```go
// headCommitWithRetry, lines 188-191:
ref := src.Ref
if ref == "" {
    ref = "HEAD"
}

// materializeFromGitHub, lines 416-419 (unchanged by this PR):
ref := src.Ref
if ref == "" {
    ref = "HEAD"
}
```

Identical logic in two places. This was already a divergent-twin hazard in the
codebase before this PR — but the PR doubles it down by introducing a third
`HeadCommit` call site (the retry wrapper) without consolidating. The next
developer who needs to change "HEAD" to "main" or to a configured default
will fix one site and miss the other.

**Recommendation:** Extract a tiny helper (`refOrHead(src.Source) string`) or
move the substitution into a `Source.RefOrHead()` method.

**Not blocking** because the current value is a constant and behavior is
identical, but flag in case the author wants to clean it up while the context
is still fresh.

---

## 5. Test readability

### 5.1 Scripted-response slices are ergonomic — No finding
**Location:** `internal/workspace/snapshotwriter_test.go:30-64`

The three parallel slices (`headErrs`, `headStatuses`, `headOIDs`) plus the
"falls back to singleton fields" rule on line 31 is a small DSL, but each test
that uses it is short and the meaning is obvious from context (e.g., line
253-256 in `TestRefreshSnapshot_RetrySucceedsOnAttempt2`):

```go
headErrs:     []error{errors.New("github: HeadCommit returned 503"), nil},
headStatuses: []int{503, 200},
headOIDs:     []string{"", "abc"},
```

A builder pattern would be heavier than necessary for four tests. The
approach is reasonable as-is.

### 5.2 Test names accurately describe what they exercise — No finding

| Test | Exercises | Match? |
|---|---|---|
| `TestRefreshSnapshot_RetrySucceedsOnAttempt2` | First 503, second 200; asserts headCalls==2 | Yes |
| `TestRefreshSnapshot_AllRetriesFailEmitOneWarning` | Four 503s; asserts headCalls==4 and exactly 1 "warning: " line | Yes |
| `TestRefreshSnapshot_PermanentErrorBypassesRetry` | One 401; asserts headCalls==1 | Yes |
| `TestRefreshSnapshot_TransientStatusCodes` | Subtests for 429/502/503/504 | Yes |

All test names match their assertions. Good.

### 5.3 `TestEnsureConfigSnapshot_NetworkErrorPreservesCachedSnapshot` now silently retries — Advisory
**Location:** `internal/workspace/snapshotwriter_test.go:213-236`

The test was modified to call `withFastDriftBackoff(t)` (line 214). Without
this, the test would sleep ~3.5s in CI. The change is correct, but the test's
*name* (`NetworkErrorPreservesCachedSnapshot`) and *body* don't reveal that 4
HeadCommit calls now happen instead of 1. The test asserts cache survival and
no error propagation — both still hold — but if a future developer reads this
test to understand the retry behavior, they'd miss that it exercises the retry
path implicitly.

**Misread risk:** low. **Not blocking.** Could be made explicit by adding an
assertion like:

```go
if fetcher.headCalls != 4 {
    t.Errorf("expected 4 attempts (1 initial + 3 retries), got %d", fetcher.headCalls)
}
```

This would lock in the retry-on-transport-error contract that the rename of the
test would otherwise hide.

---

## 6. Diff hygiene (commit messages)

Commit messages from `git reflog`:
- `77ee477` — `fix(workspace): retry transient drift-check failures before warning`
- `0cdd65a` — `fix(workspace): treat HTTP 429 as transient and use ASCII status text`

Both subjects follow conventional commits and are specific. The split is
reasonable: the first commit introduces the retry mechanism, the second
expands the transient set and (per the subject) replaces non-ASCII status
characters. **No findings.** I did not have access to the full bodies in this
review session, so the bodies aren't reviewed here.

---

## Summary

| Severity | Count |
|---|---|
| Blocking | 0 |
| Advisory | 5 |

**Blocking:** none. The retry mechanism is small, contained, and well-tested.
The next developer can read it and understand it without external context.

**Advisory:**
1. (2.1) `headCommitWithRetry` silently substitutes `ref="HEAD"` for empty —
   undocumented in the wrapper's doc comment.
2. (2.2) `withFastDriftBackoff` mutates a package global without flagging
   non-parallel-safety.
3. (3.1) `driftCheckBackoff` name doesn't reveal that entries are *retry*
   delays (slice length acts as retry count).
4. (4.1) `ref == "" → "HEAD"` substitution duplicated between
   `headCommitWithRetry` and `materializeFromGitHub`.
5. (5.3) `TestEnsureConfigSnapshot_NetworkErrorPreservesCachedSnapshot`
   now exercises the retry path implicitly; an explicit headCalls assertion
   would lock that in.

None of these would cause the next developer to form a wrong mental model and
ship a bug. They would tighten things up while the context is still fresh.
