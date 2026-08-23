# Pending scenarios reporting as PASS: mechanism, blast radius, and fix

Checkout examined: `public/niwa/.claude/worktrees/root-orientation` (read-only; a
local build was produced under `/home/dgazineu/.claude/jobs/e40f3334/tmp/niwa-test`
and one live `go test` run was executed there — nothing was committed or pushed).

## 1. Why pending renders as PASS — the exact mechanism

`test/functional/suite_test.go` wires godog into `go test` with `TestingT: t` set
(line 189), which puts godog into "subtest" mode. godog v0.15.1 (`go.mod`:
`github.com/cucumber/godog v0.15.1`) then maps each scenario ("pickle") onto a
real Go subtest via `t.Run`:

```go
// github.com/cucumber/godog@v0.15.1/suite.go:632-641
dt := &testingT{name: pickle.Name}
ctx = setContextTestingT(ctx, dt)
if s.testingT != nil {
    s.testingT.Run(pickle.Name, func(t *testing.T) {
        dt.t = t
        ctx, err = s.runSteps(ctx, pickle, pickle.Steps)
        if s.shouldFail(err) {
            t.Errorf("%+v", err)
        }
    })
}
```

The subtest's pass/fail is decided entirely by whether `t.Errorf` is called, which
is gated by `shouldFail`:

```go
// suite.go:591-600
func (s *suite) shouldFail(err error) bool {
    if err == nil || errors.Is(err, ErrSkip) {
        return false
    }
    if errors.Is(err, ErrUndefined) || errors.Is(err, ErrPending) {
        return s.strict
    }
    return true
}
```

`s.strict` comes from `godog.Options.Strict` (`internal/flags/options.go:42`,
default `false`). niwa's `suite_test.go` builds `opts := &godog.Options{Format:
"pretty", Paths: paths, TestingT: t}` and never sets `Strict`. So `s.strict` is
always `false`, `shouldFail(ErrPending)` is always `false`, `t.Errorf` is never
called for a pending scenario, and the subtest — having recorded no failure —
reports `--- PASS`. This isn't a bug in the gating steps; it's godog's documented
default (`--godog.strict` / `Options.Strict`: "Fail suite when there are pending
or undefined or ambiguous steps," off by default) combined with the fact that
Go's subtest reporter only has two states it can reach here: pass (no error) or
fail (`Errorf` called). Godog never calls the real `*testing.T`'s `Skip`/`SkipNow`
anywhere in its source — confirmed by grep; the only thing it does to the real
`*testing.T` is `t.Errorf` on failure and `dt.t.Log/Logf` for logging. There is no
third state reachable through this wiring.

One extra, load-bearing wrinkle for later: godog's own `testingT` shim
(`testingt.go`) *does* expose `Skip`/`SkipNow` via `godog.T(ctx)`, but calling it
only flips an internal `dt.skipped` flag and converts to `ErrSkip` internally —
`shouldFail` treats `ErrSkip` exactly like `nil` (first line of `shouldFail`), so
it *also* renders as `PASS`, and it still never touches the real `*testing.T`.
**There is no way, through godog's public API in this TestingT/subtest wiring, to
make a scenario show up as a real Go `SKIP`.** See section 5.

## 2. Full inventory of gating steps

`grep -rn ErrPending test/functional` finds five call sites in two files; there
are no other gating/skip mechanisms in the functional suite (no `godog.ErrSkip`,
no bare `t.Skip` inside step definitions — the only `t.Skip` in the whole
functional package is `TestFeatures`'s own top-level guard for
`NIWA_TEST_BINARY` unset, `suite_test.go:172`, which is a real Go `SKIP`, not a
per-scenario one).

| Step (regex) | Func | File:line | Condition | Scenarios / tags gated |
|---|---|---|---|---|
| `^claude is available$` | `claudeIsAvailable` | `test/functional/steps_test.go:1279` | `claude` not on PATH, or `ANTHROPIC_API_KEY` unset | 1 scenario, `@claude-integration`, in `features/workspace-imports.feature:32` ("claude sees workspace context from workspace root but not from sub-repo") |
| (same gate, second call site) | `runClaudeP` | `test/functional/steps_test.go:1317` | `claude` not on PATH | called from the two `iRunClaudeP...` steps used by the same scenario; redundant with the gate above for that scenario, but is the only gate if a future scenario calls `iRunClaudePFromInstanceRoot`/`iRunClaudePFromRepoInInstance` without first asserting `claude is available` |
| `^codex is available$` | `codexIsAvailable` | `test/functional/codex_agent_steps_test.go:1688` | `codex` not on PATH, or no login credential file (`$CODEX_HOME/<cred>` or `~/.codex/<cred>`) found | 2 scenarios, `@codex-live`, in `features/codex-agent.feature:932` ("a live Codex session writes a file on its first attempt") and `:967` ("a live interactive Codex session starts clean from the root and from a nested directory") |
| `^the Codex sandbox can run here$` | `theCodexSandboxCanRunHere` | `test/functional/codex_agent_steps_test.go:1743` | non-Linux is fine (`nil`); on Linux, `unshare` missing from PATH, or `unshare --user --map-root-user true` fails (no unprivileged user namespaces — true in most CI containers) | 1 of the 2 `@codex-live` scenarios ("...writes a file on its first attempt") — the other (`interactive session`) does not use this gate |

Total: 175 scenarios in the suite (`grep -rc "Scenario:" test/functional/features/*.feature` sums to 175). Of those, exactly 3 carry a pending-capable gate: 1 under `@claude-integration`, 2 under `@codex-live`. That reconciles with the observed "175 PASS / 0 SKIP, 174 actually executed" — one of the three gates tripped on the machine that produced that grep summary.

## 3. What godog offers, and its exact option names

- `godog.Options.Strict bool` (`internal/flags/options.go:42`) — "Fail suite when there are pending or undefined or ambiguous steps." Also exposed as a CLI flag `-godog.strict` (`flags.go:124`) / `--strict` in the internal flag set (`internal/flags/flags.go:42`), but niwa's `suite_test.go` builds `Options` by hand rather than parsing flags, so only the `Strict` struct field matters here — the CLI flag is irrelevant to this suite.
- The "pretty" formatter (`internal/formatters/fmt_base.go:95-148`) already **does** report pending/undefined/ambiguous counts in its own end-of-run summary, independent of `Strict` and independent of Go's PASS/FAIL lines:

  ```
  2 scenarios (1 passed, 1 pending)
  30 steps (19 passed, 1 pending, 10 skipped)
  30.594245933s
  --- PASS: TestFeatures (30.62s)
      --- PASS: TestFeatures/a_live_Codex_session_writes_a_file_on_its_first_attempt (0.00s)
  ```
  (verbatim tail from a live run below). So the *information* that a scenario went pending is already printed to `go test -v` output every time — it's just a different, non-Go-idiomatic summary line that a grep for `--- PASS:`/`--- SKIP:` never looks at. Godog also prints, inline, `TODO: write pending definition` under the specific step that returned `ErrPending`, and lists undefined-step snippets at the very end. None of this is wired to exit status unless `Strict` is set.
- `formatters.Formatter` interface has a `Pending(*Pickle, *PickleStep, *StepDefinition)` callback (`formatters/fmt.go:72`) that any custom/wrapping formatter can implement to collect pending scenario names programmatically — this is the hook a "list pending scenarios by name" fix would use.

## 4. Empirical confirmation (live run)

Built `niwa-test` from this checkout and ran just the gated tag:

```
NIWA_TEST_BINARY=.../niwa-test NIWA_TEST_TAGS=@codex-live go test -v ./test/functional/...
```

Output (tail):
```
2 scenarios (1 passed, 1 pending)
30 steps (19 passed, 1 pending, 10 skipped)
30.594245933s
--- PASS: TestFeatures (30.62s)
    --- PASS: TestFeatures/a_live_Codex_session_writes_a_file_on_its_first_attempt (0.00s)
    --- PASS: TestFeatures/a_live_interactive_Codex_session_starts_clean_from_the_root_and_from_a_nested_directory (30.57s)
```
This sandbox happens to have `codex` on PATH with a usable credential, so
`codex is available` passed; the sandbox's kernel doesn't allow the unprivileged
user-namespace probe, so `the Codex sandbox can run here` went pending, and the
*first* scenario's actual write assertion never ran — yet it reports identically
to the second scenario, which ran fully live and really wrote a file. Nothing in
the Go-level output tells them apart.

## 5. What CI sees

`.github/workflows/test.yml`'s `Functional tests` step runs `make test-functional`
(Makefile line 27-30), which is `go test -v ./test/functional/...` with **no**
`NIWA_TEST_TAGS` filter — the full, untagged 175-scenario suite, on
`ubuntu-latest` only. Nothing in `test.yml` (or any other workflow —
`grep -rn codex .github/workflows/*.yml` returns nothing, in any file) ever
installs a `codex` CLI or seeds a Codex login. So:

- The 2 `@codex-live` scenarios are **permanently and unconditionally pending in
  CI, on every run, forever** — not an occasional flake, a structural blind spot.
  These are exactly the scenarios that assert a live Codex session actually
  writes a file and starts clean with no trust prompt: the two places the suite
  claims to validate real Codex behavior end-to-end never execute in CI.
- The `@claude-integration` scenario is better off, but only by accident of
  timing: `make test-functional` runs *before* the `Install Claude CLI` step, so
  during the untagged full-suite run this scenario also goes pending. It does
  get real coverage, though, via a *separate* job step, `Claude integration
  tests` (`run: make test-functional-claude-integration`), gated on
  `ANTHROPIC_API_KEY` being set as a repo secret, which explicitly re-runs
  `NIWA_TEST_TAGS=@claude-integration` after `claude` is installed. So this
  scenario runs twice: once pending (silently, inside the full suite) and once
  for real (in its own job, when the secret exists). `@codex-live` has no
  equivalent second job at all — there's no `test-functional-codex-live`
  Makefile target and no CI job that would need one.
- CI's green checkmark is not lying about the 173 real scenarios, but it is
  structurally incapable of ever telling anyone that the two Codex-write/
  Codex-interactive scenarios have not executed since the day they were
  written. A regression that broke real Codex writes would show green
  indefinitely until someone runs `make test-functional` by hand on a machine
  with `codex` installed and working unprivileged namespaces.
- There's a working precedent in this repo for doing this honestly:
  `test/live/dispatch_live_test.go:90` is a *plain* (non-godog) Go test that does
  `t.Skip("live claude not available (claude not on PATH); skipping live
  dispatch test")` — a real Go `SKIP`, visible to any tool that trusts Go's own
  PASS/FAIL/SKIP vocabulary. The functional/godog suite cannot reach that same
  state through its current wiring (see next section).

## 6. Ranked fixes, with what each would break

**Note on option (d) as literally stated ("convert ErrPending to `t.Skip`")**:
this is not achievable as a local, low-risk edit. Section 1 shows godog's
`TestingT`/subtest wiring never calls the real `*testing.T`'s `Skip`/`SkipNow`
in its own source, and the `godog.T(ctx)` shim's `Skip()` only sets an internal
flag that `shouldFail` treats the same as `nil` — so it still prints `PASS`. To
get a real Go `SKIP` per scenario, one of two structural changes is needed:
either stop using `TestingT`/subtests for gated scenarios (losing per-scenario
`go test -run` filtering and the current pretty output for them), or fork/patch
godog's `runPickle` to call `t.Skip()` on the real `*testing.T` when a scenario's
only failure is `ErrPending`/`ErrSkip`. Both are far from "smallest fix."
Ranked below with that reality factored in.

1. **Cheapest and lowest-risk: print an explicit, named pending-scenario summary,
   sourced from godog's `Pending` formatter callback, and hard-fail only the
   "explicit tag, zero real scenarios" case.**
   Implementation sketch in `suite_test.go`: wrap the `"pretty"` formatter in a
   small type that also implements `Pending(pickle, step, def)` and appends
   `pickle.Name` to a slice; register it with `godog.Format(...)` and reference
   it as `opts.Format`. After `suite.Run()`, if the slice is non-empty, print a
   single grep-able line, e.g. `PENDING SCENARIOS (2): a live Codex session
   writes a file on its first attempt; a live interactive Codex session starts
   clean from the root and from a nested directory` to stderr — this makes the
   existing-but-buried godog summary ("N pending") into a name-level signal any
   grep or CI log scan can catch, with zero change to pass/fail semantics for
   anyone. Additionally, when `NIWA_TEST_TAGS` was set (an explicit ask for a
   specific tag) and *every* scenario matched by that tag ended up in the
   pending list, call `t.Fatal` — this is option (c), layered on top, and it is
   safe: it only fires when someone explicitly asked for a tag and got nothing
   real back, which never happens on a developer laptop running the untagged
   default `make test-functional` (that never sets `NIWA_TEST_TAGS`), and it
   would only fire in CI once a dedicated `test-functional-codex-live` job (see
   fix 3) exists and its environment is broken (e.g., `codex` install step
   silently failed). **What it does not fix**: the existing default CI job
   (`make test-functional`, no tag filter) still reports `--- PASS` for the two
   `@codex-live` scenarios — this only makes the pending-ness *visible in the
   log*, not load-bearing on exit status, unless paired with fix 3.
   **What it would break**: nothing existing — pure addition to the summary,
   opt-in failure condition that can't trigger under current CI/dev usage.

2. **Add a dedicated CI job (and Makefile target) for `@codex-live`, mirroring
   the existing `test-functional-claude-integration` pattern.**
   Add `test-functional-codex-live: build-test` to the Makefile
   (`NIWA_TEST_TAGS=@codex-live go test -v ./test/functional/...`), and a CI step
   analogous to "Install Claude CLI" / "Claude integration tests" that installs
   `codex`, seeds a login (needs a secret analogous to `ANTHROPIC_API_KEY`, plus
   solving the `unshare`/user-namespace gate inside the GitHub Actions
   container — `ubuntu-latest` runners may or may not permit unprivileged user
   namespaces; this needs verification, and is the part of this fix most likely
   to need its own follow-up). **This is the only fix that actually makes the
   two Codex-write/Codex-interactive scenarios execute for real in CI on a
   regular basis** — everything else in this list only changes how loudly their
   *absence* is reported. **What it would break**: nothing in existing jobs; it's
   additive. Cost is higher than 1 or 3 because it requires provisioning a real
   Codex login as a CI secret and validating the sandbox/user-namespace
   preconditions hold on the runner, which may not be immediately possible
   (unlike Claude, which only needed an API key).

3. **Enable `godog.Options.Strict = true`, but only for tag-filtered
   invocations, via a new env var (e.g. `NIWA_TEST_STRICT=1`) read in
   `suite_test.go` next to the existing `NIWA_TEST_TAGS` handling.**
   `if os.Getenv("NIWA_TEST_STRICT") != "" { opts.Strict = true }`, then set that
   var only on the new `test-functional-codex-live` target from fix 2 (or on
   `test-functional-claude-integration`, which is currently exempt from this
   problem only by lucky timing, not by design). Once real coverage exists (fix
   2) and `Strict` is set for that job, `shouldFail` returns `true` for
   `ErrPending`/`ErrUndefined` and `t.Errorf` runs — a `codex` install that
   silently breaks turns the job red instead of pending-but-green.
   **What it would break if applied globally (i.e., if someone were tempted to
   just flip `Strict = true` unconditionally in `suite_test.go` instead of
   scoping it to a tag-filtered run)**: every developer laptop without `codex`
   on PATH, without an `ANTHROPIC_API_KEY`, or without a working unprivileged
   user-namespace sandbath would start failing the entire `make test-functional`
   run (not just the 3 gated scenarios but the whole `TestFeatures` parent test,
   since a failed subtest still fails the suite via godog's non-zero exit and
   `suite_test.go`'s own `t.Fatal("functional tests failed")`). It would also
   break the *existing* default CI job the moment it runs, because `codex` and
   `claude` are genuinely absent at that point in the workflow (`claude` isn't
   installed until a later step; `codex` is never installed at all) — CI would
   go permanently red until fix 2 lands alongside it. So global strict mode is
   not viable standalone; it must be scoped to an invocation that also
   guarantees the gate's precondition is met (fix 2's dedicated job).

4. **Convert the gates to real Go `t.Skip` (the option as literally proposed).**
   As established in section 1 and the note above, this isn't reachable inside
   godog's current `TestingT` subtest wiring without a structural change:
   forking/patching `github.com/cucumber/godog` (vendoring a patched copy, or
   upstreaming a change to call `t.Skip()` on the real `*testing.T` when a
   scenario's terminal state is `ErrPending`/`ErrSkip` and not `Strict`), or
   abandoning `TestingT: t` for the gated scenarios specifically (e.g., run
   `@codex-live`/`@claude-integration` through a second, separate `go test`
   entry point that doesn't use godog's subtest integration and instead has its
   own thin wrapper that pre-checks the gate with plain Go `t.Skip()` before
   invoking godog non-subtest-style for just that scenario) — both are
   substantially more invasive than fixes 1-3 and would need their own design
   pass. Not recommended as the "smallest" fix; listed only because the prompt
   asked for it to be weighed.

## Recommendation

Implement fix 1 first (cheap, safe, immediately makes pending scenarios visible
by name in every CI log and every local run, and adds a guardrail for future
tag-filtered jobs) together with fix 2 (the only fix that closes the actual gap
— today, the two `@codex-live` scenarios have likely never executed in CI since
they were written). Fix 3 is the natural follow-up once fix 2's job exists, to
turn "codex install silently broke" into a red CI job instead of a green one.
Skip fix 4 unless a `SKIP` is specifically demanded by tooling elsewhere.
