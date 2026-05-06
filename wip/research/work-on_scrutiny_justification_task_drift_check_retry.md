# Justification review: task_drift_check_retry

Branch: `fix/drift-check-retry` (commit `77ee477`)
Diff: `git diff main...HEAD`
Files changed:
- `internal/workspace/snapshotwriter.go`
- `internal/workspace/snapshotwriter_test.go`

This review evaluates the *rationale* behind each non-trivial design choice. It
does not re-evaluate completeness, AC coverage, or test quality (those are
covered by the parallel completeness and intent reviews). Each section asks: is
the stated reason a real trade-off, or is it a shortcut that doesn't survive
scrutiny?

---

## D1 — Helper function `headCommitWithRetry` instead of inline retry loop

**Stated rationale:** "50+ line `refreshSnapshot` already exists, helper isolates
classification + backoff + status updates."

**Assessment:** Sound.

`refreshSnapshot` already has multiple concerns (provenance read, GitHub vs
non-GitHub dispatch, fetcher-nil short-circuit, drift detection, marker refresh,
re-materialization). Adding ~20 more lines of retry mechanics inline would push
it past the threshold where readers stop tracking control flow. The helper has
a clear, narrow contract (single status int + err out, one classifier
dependency) and is independently testable. The diff at
`snapshotwriter.go:138` shows the call site is a one-liner, which preserves
`refreshSnapshot`'s top-level structure exactly. This is a routine
extract-method refactor; the alternative (inline) was viable but slightly worse
for the same reasons cited.

**Verdict: SOUND.**

---

## D2 — Package-level `var driftCheckBackoff` overridable in tests

**Stated rationale:** "Avoids real wall-clock waits in test suite without an
injectable abstraction."

**Assessment:** Sound, with one minor caveat about test parallelism.

The full alternative (inject a clock or a backoff function via a struct field)
would require threading a parameter through `refreshSnapshot`,
`EnsureConfigSnapshot`, and both `Applier.Apply`/`Applier.Create` call sites
just to satisfy tests. That's a non-trivial diff for an internal seam. A
package-level `var` is the standard Go idiom for this kind of test-local
override (cf. `time.Now` shims in `time_test.go`-style packages).

The `withFastDriftBackoff` helper at `snapshotwriter_test.go:68-73` mutates
package-level state, which means tests using it cannot be `t.Parallel()` (or,
more precisely, two parallel tests that both call `withFastDriftBackoff` would
race on the slice pointer). None of the affected tests call `t.Parallel()`, so
the bug is latent. The slice is restored via `t.Cleanup`, which is correct.

There is no actual harm here under current test layout, but the helper's
docstring (`snapshotwriter_test.go:66-67`) doesn't mention the
non-parallel-safe constraint. A future contributor adding `t.Parallel()` to
one of these tests would get a flaky failure that is hard to diagnose.

**Verdict: SOUND.** (Advisory: doc the non-parallel constraint on
`withFastDriftBackoff`, or use `t.Setenv`-style scoping that fails loudly under
`t.Parallel`.)

---

## D3 — Classifier picks only `{0, 502, 503, 504}`; explicitly excludes 500 and 429

**Stated rationale:** "Spec says 502/503/504 + transport; 500 is genuine API
error not transient."

**Assessment:** Defensible for 500; **questionable for 429**.

500 is the right call. GitHub's API returns 500 for genuine internal failures
that almost always recur on retry (malformed payloads, internal type errors).
Treating 500 as transient would mask real bugs and waste 3.5s before
warning. Spec compliance + behavioral defensibility align here.

429 (rate-limit / "secondary rate limit") is the gap. Look at the production
HeadCommit error path at `internal/github/fetch.go:71-72`:
```go
if resp.StatusCode != http.StatusOK {
    return "", newETag, resp.StatusCode, fmt.Errorf("github: HeadCommit returned %d", resp.StatusCode)
}
```
A 429 response would arrive at `headCommitWithRetry` as `(err != nil, status =
429)`. The current classifier returns `false` for 429, so the retry loop bails
on the first attempt and surfaces a one-shot warning. The user is then expected
to either retry the whole `niwa apply` manually or wait it out without any
hint. This is the *exact* failure mode the retry feature is designed to absorb
gracefully — and 429 is much more common in CI environments and unauthenticated
calls than 502/503/504. (GitHub's docs flag rate-limit responses as "should be
retried after the Retry-After header.")

The plan rationale ("spec says 502/503/504 + transport") is a literal reading
of the AC. But the AC describes a *minimum* set of transient codes; the
justification that 429 is somehow not transient does not hold up. The code's
implementation does match the spec; the *justification* is shaky because
"spec only listed these" is not the same as "we considered other transient
codes and excluded them on merit."

This is a **borderline blocking** finding — the existing behavior pre-fix is
unchanged for 429, so this is technically not a regression. But the explicit
purpose of the change is to absorb transient drift-check failures, and 429 is
arguably the most common transient drift-check failure for users without a
high-rate-limit token. Calling this an advisory because (a) the AC was met
literally, and (b) the existing pre-fix warning behavior on 429 is preserved.

**Verdict: WEAK / hidden assumption.** The "spec only said X" reasoning
doesn't engage with whether the spec's enumeration was complete, and the diff
inherits that gap. Recommend extending `isTransientDriftError` to include 429
(GitHub returns it for both primary rate-limit and secondary rate-limit; the
backoff schedule is short enough that even without honoring `Retry-After` we
won't make the situation worse). If the team prefers to ship as-is, the plan
should at least record that 429 was considered and consciously deferred — the
current rationale doesn't show that consideration happened.

---

## D4 — No `Reporter.ClearStatus()` method; "Log/Warn already clear the spinner"

**Stated rationale:** "Existing Log/Warn already call stopSpinner via
`reporter.go:139`, so subsequent caller logs naturally clear the spinner;
documented in risks."

**Assessment:** **Weak — the claim has gaps on the success path.**

The claim is true in the narrow sense that `Reporter.Log` and `Reporter.Warn`
*do* call `stopSpinner` (`reporter.go:138-141, 146-148`). But the claim that
"subsequent caller logs naturally clear the spinner" assumes a Log/Warn fires
soon after `headCommitWithRetry` returns successfully. Trace the success-path
through to find when that next Log/Warn actually happens:

After `refreshSnapshot` returns nil following a successful retry:

1. **No-drift sub-path** (`snapshotwriter.go:148-156`): writes provenance,
   returns nil. **No `Log`/`Warn` emitted.** The "retrying drift check…"
   spinner message is the last thing in the spinner state.
2. **Drift sub-path**: calls `materializeAndSwap`. In `materializeAndSwap`
   (`snapshotwriter.go:333-336`), the only `Log` call is gated on a rename
   redirect (`if redirectNotice != nil && reporter != nil`). On a regular drift
   refresh with no rename, no `Log` is called either.

So in **both** sub-paths of a successful retry, `EnsureConfigSnapshot` returns
without calling `Log`/`Warn`. Control returns to the caller in `apply.go`. The
two production call sites are:

- `apply.go:314` (team config refresh in `Apply`). What happens next?
  - `EnsureInstanceGitignore` (no reporter calls)
  - `LoadState` x2 (no reporter calls)
  - The drift-check loop at lines 341-350 calls `a.Reporter.DeferWarn(...)`
    on each warning — but `DeferWarn` (`reporter.go:158-160`) just appends
    to `r.deferred`; it does NOT call `stopSpinner`.
  - Then `runPipeline` runs. Inside `runPipeline`, the first reporter call
    is conditional: `a.Reporter.Status("syncing config...")` at `apply.go:679`,
    only when `a.GlobalConfigDir != "" && !opts.skipGlobal`. If neither is
    set, the first reporter call is `a.Reporter.Status(fmt.Sprintf("cloning
    repos... (0/%d done)", total))` at `apply.go:916` — which is *after* repo
    discovery and classification. That can be 100ms–several seconds away.

- `apply.go:681` (global config refresh in `runPipeline`). Here the
  immediately-prior call is `a.Reporter.Status("syncing config...")` at
  `apply.go:679`, which sets a new spinner message *before*
  `EnsureConfigSnapshotWithStatus` runs. But the new retry helper *also* calls
  `Reporter.Status`, overwriting "syncing config..." with "retrying drift
  check…". When the retry succeeds, the spinner is left showing the retry
  text, not "syncing config…". The next Log/Warn that clears it is at
  `apply.go:683` (`Warn` on syncErr — only if there's an error) or `apply.go:689`
  (`Log` only if `converted && !sliceContains(...)`). In the common
  recover-and-success case, neither fires.

The user-visible consequence on a TTY: after a transient failure recovers, the
spinner line keeps showing "retrying drift check for X/Y (attempt 2 of 4)…"
for somewhere between ~100ms (next Status call) and tens of seconds (next
Log/Warn) of stale text, until the next `Status`/`Log`/`Warn` call replaces or
clears it. Eventually `Status("cloning repos... (0/N done)")` overwrites it via
the in-place spinner redraw — the user briefly sees "retrying…" then it
flickers to the cloning status. The summary line at apply.go:438/440 finally
calls `Log` and stops the spinner cleanly.

This is a *cosmetic* glitch, not a correctness issue. The user sees a stale
"retrying…" message for a beat after the retry succeeds. The plan's claim
"documented in risks" is acceptable IF the risk doc actually says "after a
successful retry, the spinner shows the retry text until the next Status/Log
call replaces it; on the no-drift path with no Global config, that can be
several seconds." The plan summary in the prompt doesn't quote the risk doc,
so I can't verify the risk was articulated at that level of precision; the
claim "Log/Warn already call stopSpinner" elides this gap.

A `ClearStatus()` method (or a single `reporter.stopSpinner()` call at the
return point of `headCommitWithRetry` after a recovery) would close it cleanly
without the user ever seeing the stale text. The cost is one new method on
`Reporter`. The plan's choice to skip it is *defensible* because the consequence
is a brief cosmetic flicker, not a wrong-output bug — but the rationale as
stated ("Log/Warn naturally clear the spinner") is inaccurate for the
no-immediate-Log/Warn paths that dominate after a successful retry.

**Verdict: WEAK rationale.** The stated reason understates the gap. Either:

1. Add `ClearStatus()` and call it at `headCommitWithRetry`'s success-after-
   retry exit — small, contained, removes the cosmetic glitch entirely; or
2. Reword the rationale to "the spinner persists for a short window after
   recovery until the next Status/Log call; this is acceptable cosmetic
   flicker because (a) Status calls happen frequently in the apply pipeline
   and (b) the alternative requires a new public Reporter method." That's an
   honest trade-off, not the current rationale.

Marking advisory because it's a UX polish issue, not a correctness bug.

---

## D5 — Backoff via `select { case <-time.After(d): case <-ctx.Done(): }`

**Stated rationale:** "ctx-aware abort instead of bare `time.Sleep`."

**Assessment:** Sound.

`time.Sleep` is uncancelable; using `select` with `<-ctx.Done()` correctly
honors `ctx` cancellation (e.g., user hits Ctrl-C during the 2s backoff). The
implementation at `snapshotwriter.go:201-204` returns `ctx.Err()` on
cancellation, which propagates as the err to `refreshSnapshot`, which then
falls through to the existing warn-and-cache fallback. That's the right
behavior on cancel. No leaked goroutine because `time.After` returns a single
value once and the channel is GC'd.

One nit: `time.After` allocates a `*time.Timer` that fires regardless of
whether the select picked `ctx.Done()`. For a max of 3 retries with up to 2s
each, the leak is bounded. If this loop ever ran inside a tight outer loop
(it doesn't), the right pattern would be `t := time.NewTimer(d); defer t.Stop()`.
For the scale here, `time.After` is fine.

**Verdict: SOUND.**

---

## D6 — Backoff schedule {500ms, 1s, 2s} (≈3.5s total worst case)

**Stated rationale:** Implicit in plan ("bounded and short" matches AC5).

**Assessment:** Sound for typical apply, with a small caveat.

3.5s is short enough not to be perceived as a hang and long enough that GitHub
edge transients (502/503/504 from a load-balancer flap) usually settle. The
exponential-ish progression {500ms, 1s, 2s} is the standard "exponential
backoff with jitter omitted because the call volume is too low to cause
thundering-herd" pattern.

Potential concern flagged by the prompt: cumulative slowness across multiple
config-snapshot calls in a single apply. There are two production callers in
`apply.go`:

- Line 314: `EnsureConfigSnapshotWithStatus` for `configDir` (team config).
- Line 681: `EnsureConfigSnapshotWithStatus` for `a.GlobalConfigDir` (personal
  overlay), inside `runPipeline`.

If both hit terminal-failure transients (1 initial + 3 retries each), worst
case is 7s of sleep before any further work, then the warn-and-cache fallback
in both paths. That's tolerable for an interactive CLI ("apply is briefly
stalled" — the retry note exists precisely to explain why), and the failure
mode degrades to the same warn-and-cache that pre-fix code had after the first
attempt.

The tighter concern would be a `niwa apply` that loops over many overlays
(e.g., a future per-instance overlay refactor); the current architecture has
exactly two snapshot calls, so 7s is the actual ceiling.

The schedule is also conservative: there's no jitter. For a tool with a single
caller per machine, jitter is unnecessary. If `niwa apply` were ever invoked
en-masse (CI fanout pointing at the same upstream repo), un-jittered backoff
would synchronize retries — but the population is small enough that this is
hypothetical.

**Verdict: SOUND.**

---

## D7 — Extended `fakeFetcher` with scripted slice fields; singletons preserved

**Stated rationale:** "Existing `fakeFetcher` extended with scripted slice
fields (`headErrs`, `headStatuses`, `headOIDs`); singletons preserved for
backward compat."

**Assessment:** Sound, with one mild trap in the precedence rule.

The fake's `HeadCommit` at `snapshotwriter_test.go:37-64` checks slices first;
any single non-empty slice triggers slice mode for *all* return values. The
fall-through to singletons happens only when all three slices are exhausted at
the current call index. Read literally, the contract is:

> If `idx < len(headErrs) || idx < len(headStatuses) || idx < len(headOIDs)`,
> use slices for whichever still has entries; default missing fields from the
> singletons.

The precedence rule has one subtle gotcha: **the singleton `headErr` is NOT
consulted in slice mode.** Look at lines 40-58: when slice mode is triggered,
the err is taken from `headErrs[idx]` *only* (or stays nil if `idx >=
len(headErrs)`). So a test that sets `headErr: someErr, headStatuses:
[200, 200]` will *not* see `someErr` on either call — it'll see two
successes — because slice mode is on (status slice is non-empty) and `headErr`
is shadowed.

This is unlikely to trip anyone using the fake in this PR (the new tests use
slice mode end-to-end, the legacy tests still use singleton-only mode), but
the behavior could surprise future contributors who set `headErr` thinking it
is a "fallback default" while also adding e.g. `headStatuses` for control. The
docstring at lines 30-34 ("Falling off the end of a slice falls back to the
singleton fields above") implies the singletons ARE the fallback — but that's
true only when ALL slices are exhausted, not per-field.

**Verdict: SOUND** (the existing tests don't hit the trap; the new tests use
slices for everything they need to control). **Advisory:** tighten the
docstring to "When ANY of these slices is non-empty, the function operates in
slice mode and the singletons are not consulted; only when all three slices
are exhausted do the singletons take over." Or, less invasively, name the
slices and singletons such that the precedence is obvious.

---

## D8 — No `@critical` Gherkin scenario added

**Stated rationale:** "Drift-check is internal robustness, not a CLI command
surface."

**Assessment:** Sound and consistent with the project's testing conventions.

Per `public/niwa/CLAUDE.md`:

> When you ship a user-facing CLI command or fix a regression in the init →
> create → apply workflow, add a `@critical` Gherkin scenario in
> `test/functional/features/`.

This change does not add a CLI command or fix a regression in init/create/
apply — it adds resilience to a sub-pipeline of `apply` that already has its
own behavior under network failure (warn + use cached). The unit tests
exercise the new logic against the fake fetcher, and the existing apply
functional tests will continue to pass because the warn-and-cache path's
output format is unchanged. A `@critical` scenario for transient-then-recover
would require simulating mid-test 502s through the local git server fake,
which is more plumbing than the marginal coverage justifies.

**Verdict: SOUND.**

---

## Summary

| Decision | Verdict | Severity |
|----------|---------|----------|
| D1 helper extraction | Sound | — |
| D2 package-level backoff var | Sound (advisory: docstring on parallel-safety) | Advisory |
| D3 classifier {0,502,503,504} excluding 429 | Weak rationale; missing 429 | Advisory (borderline) |
| D4 no ClearStatus(), "Log/Warn naturally clear" | Weak rationale (gap on success-no-Log paths) | Advisory |
| D5 ctx-aware select for backoff | Sound | — |
| D6 schedule {500ms,1s,2s} | Sound | — |
| D7 fakeFetcher slice/singleton precedence | Sound (advisory: docstring tightening) | Advisory |
| D8 no @critical scenario | Sound | — |

**Blocking findings: 0.**

**Advisory findings: 3 substantive (D3, D4, plus mention of D2/D7 docstring
nits).**

The diff ships correct behavior; the *justification* for two of the choices
(D3, D4) doesn't survive the scrutiny questions in the prompt. D3 is
borderline because the missing 429 case is the most common real-world
transient drift-check failure for unauthenticated/low-rate-limit users — the
"spec only said X" reasoning doesn't show that 429 was considered and
consciously excluded. D4's "Log/Warn naturally clear the spinner" is
factually incomplete for the common success-after-retry control flow, where
the spinner persists with stale "retrying…" text until a downstream Status or
Log call replaces it; this is cosmetic, not a correctness bug, but the stated
rationale understates the gap. Neither finding warrants blocking the PR — the
behavior matches the AC and degrades gracefully — but both deserve to be
named in the plan's "trade-offs accepted" section rather than presented as
fully-considered design choices.
