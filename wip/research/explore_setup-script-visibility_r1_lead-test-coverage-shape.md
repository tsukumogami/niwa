# Lead: What does the existing test suite already assert about setup scripts and about the Reporter, and what shape does the regression test need to take so it fails on today's `main` and passes after the fix?

## Findings

### 1. `internal/workspace/setup_test.go` — every test and what it pins

Nine tests, 198 lines. Four cover `ResolveSetupDir` (pure config resolution, no
Reporter involvement). Five cover `RunSetupScripts`. **Not one of them asserts on
reporter output.** Every `RunSetupScripts` call passes
`NewReporterWithTTY(&bytes.Buffer{}, false)` and then discards the buffer — in
four of the five cases the buffer is constructed inline and is unreachable
afterwards. The suite verifies behavior exclusively through the returned
`*SetupResult` and through marker files on disk.

| Test | Line | What it pins |
|---|---|---|
| `TestResolveSetupDirDefault` | :12 | default is `scripts/setup` |
| `TestResolveSetupDirWorkspaceOverride` | :19 | `[workspace] setup_dir` wins over default |
| `TestResolveSetupDirRepoOverride` | :31 | `[repos.X] setup_dir` wins over workspace |
| `TestResolveSetupDirRepoDisable` | :47 | repo override `""` disables (returns `""`) |
| `TestRunSetupScriptsDisabled` | :59 | `setupDir == ""` → `result.Disabled` |
| `TestRunSetupScriptsMissingDir` | :66 | missing dir → `result.Skipped` |
| `TestRunSetupScriptsEmptyDir` | :74 | empty dir → `result.Skipped` |
| `TestRunSetupScriptsSuccess` | :83 | two scripts, ordered `01`/`02`, both `Error == nil`, both ran with cwd = repo root (marker files) |
| `TestRunSetupScriptsStopOnError` | :122 | **acceptance criterion 5, within-repo half**: first script exits 1 → `len(result.Scripts) == 1`, `Scripts[0].Error != nil`, and `02-never-runs.sh` provably did not run (marker file absent) |
| `TestRunSetupScriptsNonExecutableWarning` | :147 | non-executable file yields an `Error` but does **not** stop the loop — the next script still runs (`continue`, not `break`, at `setup.go:101`) |
| `TestRunSetupScriptsLexicalOrder` | :177 | three scripts sort `01`/`05`/`10` |

**Signature/shape sensitivity.** All five `RunSetupScripts` tests call the
function positionally as `RunSetupScripts(repoDir, setupDir, r)`. Adding a
*field* to `SetupResult` or to `ScriptResult` (e.g. a captured-output slice)
breaks nothing — Go struct literals here are all keyed, and the tests only read
named fields. Adding a *parameter* to `RunSetupScripts` (e.g. an options struct
or a repo-name string for the prefix) breaks all five call sites mechanically
(one-line edits each). The cheapest-to-absorb shape is therefore: keep the
signature, add fields to the result structs, or add a variadic/options
parameter.

One real coupling worth naming: `result.RepoName` is derived inside
`RunSetupScripts` as `filepath.Base(repoDir)` (`setup.go:47`) and is **never
read** — `apply.go:1592` uses `cr.Repo.Name` from the classified repo instead.
If the fix needs the repo-name prefix the design promises, `RepoName` is the
existing-but-dead field to use, and no test pins its value today.

### 2. `internal/workspace/reporter_test.go` — 243 lines, 14 tests

There is no shared test helper. Every test is the same three lines:
`var buf bytes.Buffer` → `NewReporterWithTTY(&buf, isTTY)` → assert on
`buf.String()`. That is the whole capture apparatus, and it is what any new
test should copy.

What is pinned:

- **Non-TTY**: `Status` writes literally nothing (`:10`, asserts `buf.Len() == 0`).
  `Log` writes exactly `"hello world\n"` and is asserted to contain **no `\r` or
  `\x1b` byte at all** (`:20`). `Warn` likewise, with the `warning: ` prefix
  (`:34`). `Writer()` routes through `Log` (`:49`).
- **TTY**: `Status` sets `needsClear` and starts the spinner goroutine (`:65`,
  reaching into unexported `r.spinStop` under `r.mu`). `Log` after `Status`
  asserts the buffer **ends with** `"\r\033[Kcloned foo\n"` — a `HasSuffix`
  check, explicitly commented "regardless of how many spinner frames the
  goroutine wrote before being stopped" (`:83`). `Warn` (`:114`) and `Writer()`
  (`:128`) use the same suffix idiom.
- **Spinner mechanics**: `doTick` is called directly, with no goroutine, to
  pin the frame format `\r\033[K<frame> <msg>` without timing dependence
  (`:170`); frame cycling wraps (`:192`); `doTick` is a no-op when `spinMsg`
  is empty (`:216`).
- **Deferred messages**: **nothing.** `Defer`, `DeferWarn`, and `FlushDeferred`
  have zero direct coverage in `reporter_test.go`. They are exercised only
  indirectly, six times, and always via the same two-step idiom —
  `a.Reporter.FlushDeferred()` then `bytes.Contains(buf.Bytes(), ...)`:
  `apply_worktree_refresh_test.go:111,178,223,259,294` and
  `apply_test.go:3152` (inside the `healWithBaseDir` helper). The clearest
  model to copy is `TestRefreshWorktreeEnvs_LockedSkippedAndForwardCarried`
  (`apply_worktree_refresh_test.go:141`), which asserts "a warning naming the
  locked worktree must be deferred" by flushing and substring-matching.

The `HasSuffix`-not-`Contains` idiom across every TTY test is the suite already
telling us that the raw TTY buffer is not safely substring-assertable. See §5.

### 3. The test that will break when the fix lands

`internal/workspace/gitutil_test.go:149`,
`TestRunCmdWithReporter_AllLinesViaStatus`, **pins today's defect as intended
behavior**:

```go
// Script output is transient — permanent newline-terminated lines must not appear.
if strings.Contains(out, "fatal: this is fine\n") {
    t.Errorf("runCmdWithReporter: script output should be transient, not permanent: %q", out)
}
```

Any fix that makes script output permanent — via `Log`, via a new Reporter
method, or via replaying buffered lines through `DeferWarn` inside
`runCmdWithReporter` — fails this assertion. It is a deliberate pin, not an
accident: the docstring on `runCmdWithReporter` itself (`gitutil.go:87-92`)
says "all output is treated as transient progress… On non-TTY output Status is
a no-op, so script output is silent in piped/CI contexts." The implementation
comment *documents the bug as a feature*. Both the test and that comment have to
change in the same commit as the fix, and the design-doc reconciliation the
scope calls for should cover this comment too.

`TestRunGitWithReporter_StripEscapesInOutput` (`:176`) is the one place the
suite does substring-match a script line out of a TTY buffer
(`strings.Contains(out, "hello")`) — and it works only because that script
emits exactly one line. See §5 for why that does not generalize.

### 4. Apply-pipeline (Step 6.75) coverage: none

`grep` for `RunSetupScripts` across all `*_test.go` returns hits in
`setup_test.go` only. Step 6.75 (`apply.go:1580-1596`) has no direct test.

`TestCreateIntegration` (`apply_test.go:76`) and `TestApplyIntegration`
(`apply_test.go:223`) *do* execute Step 6.75 — they call `applier.Create(...)`
and `applier.Apply(...)`, which run the full `runPipeline` — but they never
create a `scripts/setup` directory, so `RunSetupScripts` returns `Skipped` and
the loop `continue`s at `apply.go:1587`. These two tests are nonetheless the
ready-made seam for the cross-repo half of acceptance criterion 5: both
pre-create the repo directories on disk (`apply_test.go:308-318`, two repos in
two groups, with `.git` markers so the cloner skips them), so dropping a failing
`scripts/setup/01-fail.sh` into the first repo and a marker-writing script into
the second proves continue-to-next-repo through the real pipeline. `Applier` has
a public `Reporter` field explicitly documented as replaceable
(`apply.go:50-53`), so a test can swap in `NewReporterWithTTY(&buf, isTTY)`
before calling `Apply`.

Note that no test currently asserts on Step 6.75's deferred warning at all —
`a.Reporter.DeferWarn(...)` at `apply.go:1592` is unpinned, and neither
`Create` nor `Apply` visibly calls `FlushDeferred` in a way these tests check.

### 5. Measured reproduction of the issue-#239 probe

I wrote a throwaway `internal/workspace/zz_probe_test.go`, ran it, and deleted
it; `git status --short --untracked-files=all` is clean apart from this findings
file. Three probes, all real output.

**Probe A — the issue's claim, both TTY modes.** Script writes
`PROBE-STDOUT-LINE` to stdout, `NIWA-PROBE-MARKER-STDERR-LINE` to stderr, exits 3.

```
isTTY=false
  script-failed        = true
  script-error         = "exit status 3"
  buffer-len           = 0
  buffer-raw           = ""
  stderr-line-present  = false
  stdout-line-present  = false
  apply-deferred-out   = "warning: setup script scripts/setup/01-fail.sh failed for probe-repo: exit status 3\n"
  deferred-has-marker  = false

isTTY=true
  script-failed        = true
  script-error         = "exit status 3"
  buffer-len           = 41
  buffer-raw           = "\r\x1b[K⠋ NIWA-PROBE-MARKER-STDERR-LINE\r\x1b[K"
  stderr-line-present  = true      <-- but see below
  stdout-line-present  = false
  apply-deferred-out   = "warning: setup script scripts/setup/01-fail.sh failed for probe-repo: exit status 3\n"
  deferred-has-marker  = false
```

The issue's report reproduces exactly for `isTTY=false`: empty buffer, nothing
of the script survives, and the single deferred warning carries only
`exit status 3` — no explanation of *why*.

The `isTTY=true` row is where the issue's summary is slightly off in a way that
matters for test design: the marker **is** present in the raw buffer, but it is
immediately followed by `\r\x1b[K`, which erases it from the actual terminal.
Byte-wise present, operator-wise invisible. `PROBE-STDOUT-LINE` — the *first*
line the script emitted — is gone entirely. Stable across `-count=8`.

**Probe B — how much survives on the TTY path.** Script writes N numbered lines
to stderr and exits 3:

```
lines=1   survived=1  which=[LINE-001]  buflen=20  raw="\r\x1b[K⠋ LINE-001\r\x1b[K"
lines=2   survived=1  which=[LINE-002]  buflen=20  raw="\r\x1b[K⠋ LINE-002\r\x1b[K"
lines=50  survived=1  which=[LINE-004]  buflen=20  raw="\r\x1b[K⠋ LINE-004\r\x1b[K"
```

Stable across `-count=3`. **Exactly one line of a fifty-line script ever reaches
the buffer, and *which* one is a scheduling artifact.** The mechanism: script
lines arrive at `r.Status` in microseconds while `spinLoop` ticks at 100 ms
(`reporter.go:87`), so `spinMsg` is overwritten many times between renders; only
whatever `spinMsg` happens to hold at a tick gets drawn, and `stopSpinner`
(`reporter.go:131`) then erases it. `LINE-004` of 50 is deterministic on this
machine but is not a contract.

**Probe C — validating a robust assertion helper.** Detail in §6; it cleanly
returns `""` for today's behavior in both modes and the full marker line for
each hypothesised fix.

### 6. Shape of the minimal regression test

The naive form — write a marker to stderr, exit non-zero, assert
`strings.Contains(buf.String(), marker)` in both TTY modes — **is not safe**.
Probe B shows the TTY half would pass on today's `main` whenever the marker
happens to be the surviving line, which for a one-line script is *always*. That
half would be a false green against acceptance criterion 2, and it would stay
green for the wrong reason after the fix.

The fix is to assert on *permanent* output rather than raw bytes. The reporter
makes this a clean split, and the split is load-bearing enough to justify a
small helper in `reporter_test.go`:

```go
// permanentOutput returns only the reporter output that stays on screen.
// The buffer is a sequence of segments delimited by the CR+erase control
// string. Spinner ticks write a segment with no newline (each is overwritten
// by the next); Log/Warn write newline-terminated segments (permanent).
// On non-TTY the delimiter never appears, so the whole buffer is returned.
func permanentOutput(raw string) string {
    const clear = "\r\x1b[K"
    var keep []string
    for _, seg := range strings.Split(raw, clear) {
        if strings.Contains(seg, "\n") {
            keep = append(keep, seg)
        }
    }
    return strings.Join(keep, "")
}
```

This is sound because of three facts already in the code, not by convention:
`doTick` never writes a newline (`reporter.go:111`); `Log` always appends one
(`reporter.go:141`); and `stripEscapes` (`gitutil.go:19`) removes CSI sequences
from script lines *before* they reach the reporter, so a hostile script cannot
inject a fake `\x1b[K` delimiter into a permanent segment.

Measured behavior of the helper (probe C, both modes):

| routing | isTTY | raw | `permanentOutput` | marker in permanent |
|---|---|---|---|---|
| today (`Status`) | false | `""` | `""` | **false** |
| today (`Status`) | true | `"\r\x1b[K"` | `""` | **false** |
| via `Log` prefixed | false | `"myrepo: …\nmyrepo: MARKER-LINE\n"` | same | true |
| via `Log` prefixed | true | `"\r\x1b[Kmyrepo: …\nmyrepo: MARKER-LINE\n"` | `"myrepo: …\nmyrepo: MARKER-LINE\n"` | true |
| via `DeferWarn` replay | false | `"warning: … exit status 3\n  MARKER-LINE\n"` | same | true |
| via `DeferWarn` replay | true | `"\r\x1b[Kwarning: … exit status 3\n  MARKER-LINE\n"` | same | true |

It returns `""` for today's behavior in **both** modes — so a `Contains` check on
`permanentOutput` fails on today's `main` in both, which is precisely acceptance
criterion 2 — and it is agnostic to which of the candidate fixes lead 1 lands on.

Proposed test, in `setup_test.go`:

```go
// TestRunSetupScriptsFailureOutputIsVisible is the issue-239 regression: when a
// setup script fails, the operator must be able to read what it said, on a TTY
// and off one. Asserted against permanent (non-transient) reporter output,
// because a spinner frame that is immediately erased is not "visible".
func TestRunSetupScriptsFailureOutputIsVisible(t *testing.T) {
    const marker = "cannot find required tool: frobnicate"

    for _, isTTY := range []bool{false, true} {
        name := map[bool]string{false: "nonTTY", true: "TTY"}[isTTY]
        t.Run(name, func(t *testing.T) {
            tmpDir := t.TempDir()
            setupDir := filepath.Join(tmpDir, "scripts", "setup")
            if err := os.MkdirAll(setupDir, 0o755); err != nil { t.Fatal(err) }
            body := "#!/bin/sh\necho '" + marker + "' >&2\nexit 3\n"
            if err := os.WriteFile(filepath.Join(setupDir, "01-fail.sh"), []byte(body), 0o755); err != nil {
                t.Fatal(err)
            }

            var buf bytes.Buffer
            r := NewReporterWithTTY(&buf, isTTY)
            result := RunSetupScripts(tmpDir, "scripts/setup", r)
            r.FlushDeferred()   // drains any replay-on-failure path
            r.stopSpinner()     // joins the goroutine so buf is race-free

            if len(result.Scripts) != 1 || result.Scripts[0].Error == nil {
                t.Fatalf("expected exactly one failed script, got %+v", result.Scripts)
            }
            if got := permanentOutput(buf.String()); !strings.Contains(got, marker) {
                t.Errorf("failed script's stderr not visible to the operator (isTTY=%v)\npermanent output: %q\nraw: %q",
                    isTTY, got, buf.String())
            }
        })
    }
}
```

Two mechanical notes. `stopSpinner()` must be called before reading `buf` in the
TTY subtest — the spinner goroutine writes to the same buffer concurrently, and
`bytes.Buffer` is not safe for concurrent use, so without the join `go test
-race` will flag it. It is unexported but the test is in-package, and
`reporter_test.go:78` already relies on stopping the spinner (`r.Log("done") //
stop goroutine before test exits`) for the same reason. Order matters:
`FlushDeferred` before `stopSpinner`, since `FlushDeferred` calls `Log` which
itself stops the spinner and emits the clear sequence; calling `stopSpinner`
first is harmless but the reverse leaves a trailing clear segment that
`permanentOutput` discards anyway.

For acceptance criterion 5, the two existing tests are already enough for the
within-repo half (`TestRunSetupScriptsStopOnError`,
`TestRunSetupScriptsNonExecutableWarning`) and neither needs to change. The
cross-repo half — "one repo's failure does not stop the next repo" — has **no
test today at any level**. It should get one, built on the `TestApplyIntegration`
scaffold described in §4: two pre-created repos, a failing script in the first,
a marker-writing script in the second, assert the marker file exists and that
exactly one deferred warning naming the first repo was flushed.

### 7. Functional (Gherkin) suite

**Convention.** `docs/guides/functional-testing.md` (168 lines). godog/Cucumber
driving the compiled binary. One `.feature` file per area under
`test/functional/features/` (29 files today); steps implemented in
`steps_test.go`, registered in `initializeScenario` in `suite_test.go`. Each
scenario gets a fresh sandbox from the `Before` hook: sandboxed `$HOME`,
`$TMPDIR`, and a `workspaceRoot` placed under `os.TempDir()` (deliberately not
inside the repo, so `CheckInitConflicts` does not fire on a developer machine
that itself lives in a niwa workspace).

**`@critical`** selects the fast, fully-offline subset run by
`make test-functional-critical`; `make test-functional` runs everything. The
guide's rule (lines 12-18) is: add a `@critical` scenario whenever you ship a
user-facing CLI command **or fix a regression in the init → create → apply
workflow** — "if you had to manually run `niwa <command>` to verify your change
works, write a scenario so the next person doesn't have to." Scenarios that need
external tooling gate on availability and skip rather than fail, to keep
`@critical` offline (guide lines 162-165).

**`localGitServer`** (`test/functional/localrepo_test.go`) creates real bare git
repos served over `file://` — no network. Three helpers: `Repo(name)` (empty
bare repo, for source repos to clone), `ConfigRepo(name, toml)` (bare repo
containing `workspace.toml`, the `niwa init --from` target), and
`OverlayRepo(name, toml)`. URLs are stored in `s.repoURLs[name]` and referenced
from workspace.toml bodies via the `{repo:<name>}` placeholder, which the
`aConfigRepoExistsWithBody` step interpolates — so source repos must be declared
before the config repo that references them.

**Existing setup-script coverage: none.** `grep -i setup test/functional/features/*.feature`
returns zero hits across all 29 files.

**Does this change qualify for `@critical`?** Yes, on the guide's own terms.
This is a regression fix squarely inside the init → create → apply workflow —
Step 6.75 runs on every `create` and every `apply` — and its whole subject is
what the CLI prints to a terminal, which is exactly the black-box behavior unit
tests cannot see. The unit test in §6 exercises `RunSetupScripts` in isolation
with a synthetic TTY flag; only a functional scenario proves that the real
binary, with real stderr, actually shows the operator the failure. It also
pins whichever exit-code decision lead 2 lands on, which is a CLI contract.

Sketch (`test/functional/features/setup-scripts.feature`):

```gherkin
Feature: repo setup scripts report their failures visibly
  A repo's scripts/setup/ scripts run after clone during create and apply.
  When one fails, its output must reach the operator — including off a TTY,
  where the functional suite (like CI, cron, and background agents) runs.

  Design: docs/designs/current/DESIGN-post-clone-scripts.md

  @critical
  Scenario: a failing setup script surfaces its own output and is not silent
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the repo "tools/app" exists in instance "ws"
    When I write an executable file "scripts/setup/01-fail.sh" in repo "tools/app" of instance "ws" with body:
      """
      #!/bin/sh
      echo 'cannot find required tool: frobnicate' >&2
      exit 3
      """
    And I run "niwa apply ws"
    Then the error output contains "01-fail.sh"
    And the error output contains "cannot find required tool: frobnicate"
    And the error output contains "app"
    # exit-code expectation is whatever lead 2 decides; pin it here explicitly
    # rather than leaving it implied.
    And the exit code is 0
```

Most of the vocabulary already exists: `the error output contains "…"`
(`suite_test.go:255`), `the exit code is (\d+)` (`:247`), `the repo "…" exists
in instance "…"` (`:233`), `I write to file "…" in repo "…" of instance "…" with
body:` (`:272`). **One new step is required.** The existing write-file step
writes mode `0o644` (`steps_test.go:1132`), which would exercise the
non-executable-warning path instead of the failure path, and it does not
`MkdirAll` the parent, so `scripts/setup/` would not exist. A sibling
`I write an executable file …` that creates parents and writes `0o755` is the
minimal addition — `steps_test.go` already writes `0o755` scripts elsewhere
(`:135`, `:251`), so it matches house style.

A second scenario should cover cross-repo resilience end to end (two repos, the
first with a failing script, assert the second's marker file exists and the
instance is complete). That one need not be `@critical` if runtime is a concern,
but it is the black-box proof of the Decision 2 invariant.

### 8. Test baseline

`go test ./...` from the repo root on branch `docs/setup-script-visibility`:
**exit 0, all 26 packages pass, no pre-existing failures.** `internal/workspace`
takes 13.4 s, `internal/cli` 7.8 s, everything else under 1.4 s; total wall
clock well under a minute. `test/functional` reports `ok … 0.030s` under plain
`go test` because the godog suite gates on `NIWA_TEST_BINARY` and is driven via
the `make test-functional*` targets instead.

## Implications

1. **The naive regression test does not satisfy acceptance criterion 2.** A
   `strings.Contains(buf.String(), marker)` assertion on the TTY path passes on
   today's `main` for any single-line script (measured, deterministic across
   runs). The test must assert on permanent output, and the ~10-line
   `permanentOutput` helper in `reporter_test.go` is the cheapest way to do it.
   Whoever implements this should verify the new test red on `main` before
   writing the fix, not after.

2. **`TestRunCmdWithReporter_AllLinesViaStatus` (gitutil_test.go:149) is a
   tripwire the fix must deliberately cross.** It asserts that script output is
   *not* permanent. So does the docstring on `runCmdWithReporter`
   (gitutil.go:87-92). Both need updating in the same commit; a fix that leaves
   the comment saying "script output is silent in piped/CI contexts" recreates
   the documentation drift this whole exploration is about.

3. **Adding fields to `SetupResult`/`ScriptResult` costs nothing; adding a
   positional parameter to `RunSetupScripts` costs five one-line edits.** And
   `SetupResult.RepoName` already exists, is already populated, and is read by
   nobody — it is the natural carrier for the repo-name prefix the design
   promises, at zero test cost.

4. **Cross-repo resilience (acceptance criterion 5, second half) is currently
   unproven at every level.** Stop-on-first-error-within-repo has a good test;
   continue-to-next-repo has none. Step 6.75 has no test at all. Both
   `TestCreateIntegration` and `TestApplyIntegration` already run the real
   pipeline over two pre-created repo directories, so the fixture cost is low.

5. **A `@critical` Gherkin scenario is warranted**, and it needs exactly one new
   step (`I write an executable file …`, mode `0o755` with `MkdirAll`). The
   functional suite runs the binary with non-TTY stderr, which is the exact
   configuration where today's behavior is 100 % silent — so the scenario is a
   real regression gate, not a formality. It is also the natural place to pin
   whichever exit-code decision lead 2 produces.

6. **`Defer`/`DeferWarn`/`FlushDeferred` have no direct unit coverage.** If the
   chosen fix routes failure output through the deferred channel (buffer and
   replay on failure — a strong candidate given lead 4's output-volume concern),
   it will be building on an untested primitive. Worth two small tests in
   `reporter_test.go` alongside the main work.

## Surprises

- **On a TTY, exactly one line of a fifty-line failing script reaches the
  output buffer, and which line is a goroutine-scheduling artifact.** I expected
  either "all lines, overwritten" or "none". The reality — one arbitrary line,
  then erased — is worse than the issue describes and makes the TTY path
  actively misleading rather than merely lossy: an operator can catch a glimpse
  of line 4 of 50 and reasonably conclude that is where it failed.

- **The stdout line vanished while the stderr line survived**, in probe A. Not a
  stream-priority effect — both go through the same `io.Pipe` — just an artifact
  of stdout being emitted first and overwritten before the goroutine's first
  tick. It would be easy to misread that single data point as "stderr is
  handled, stdout is dropped," which is not what is happening.

- **The suite already contains a test asserting the bug is correct behavior**,
  with a comment explaining why, and the production docstring agrees with it.
  This is not sloppiness — it was a coherent reading of "setup script output is
  transient progress" at the time. It does mean the fix cannot be purely
  additive, and it is direct evidence for the exploration's framing that this is
  a design divergence rather than an oversight.

- **`SetupResult.RepoName` is computed and never read.** A vestige of the
  design's repo-name-prefix promise that got as far as the struct and no
  further.

- **The design doc's Security Considerations claim "Script paths are validated
  to stay within the repo directory."** `setup.go` does no such validation. It
  happens to be true — `os.ReadDir` entry names cannot contain a separator — but
  it is an accident of the implementation, not an enforced check, and the doc
  reads as if it were deliberate. Same category as the two claims the scope
  already identified; worth folding into the reconciliation.

## Open Questions

1. Does `permanentOutput` belong in `reporter_test.go` as a test helper, or does
   the reporter itself want a testable notion of "what stays on screen"? The
   helper depends on the exact `\r\x1b[K` control string, which is currently a
   `fmt.Fprintf` literal in two places (`reporter.go:111`, `:131`). Extracting
   it to a package constant would let the helper reference it instead of
   duplicating the bytes. Low stakes, but it is the difference between a helper
   that breaks silently if the escape changes and one that cannot.

2. If the fix buffers script output and replays it only on failure (lead 4's
   likely recommendation), where does the buffer live — a new field on
   `ScriptResult`, or entirely inside `runCmdWithReporter`? That determines
   whether `setup_test.go` can assert on captured output structurally (stronger,
   order-independent) or must keep asserting on the reporter buffer. My
   suggested test asserts on the buffer specifically because it is agnostic to
   this choice; a structural assertion would be better but cannot be written
   until lead 1 lands.

3. Should the `@critical` scenario pin the exit code at all before lead 2
   decides? I have written it as `Then the exit code is 0` to make the current
   contract explicit rather than implied — but if lead 2 recommends a non-zero
   exit or a `--strict` flag, that line changes and the scenario becomes the
   place that documents the new contract. Worth confirming the scenario is
   written *after* lead 2 concludes, not before.

4. Does the second (cross-repo resilience) functional scenario earn `@critical`,
   or does its two-repo, two-clone setup cost more than the fast subset should
   carry? The guide gives no runtime budget and I did not measure
   `make test-functional-critical`.

## Summary

`setup_test.go`'s nine tests pin ordering, stop-on-error, the non-executable
skip and the disabled/skipped paths entirely through the returned `SetupResult`
and marker files — not one assertion touches reporter output, and every test
throws its buffer away; `reporter_test.go`'s fourteen tests pin Status/Log/Warn
in both TTY modes but leave `Defer`/`FlushDeferred` uncovered, and Step 6.75 of
the apply pipeline has no test at all. I reproduced the defect with a throwaway
in-package probe (since deleted, `git status` clean): off-TTY the buffer is
literally empty, and on-TTY exactly one line of a fifty-line failing script ever
renders before `stopSpinner` erases it — which line is a scheduling artifact —
so the obvious `Contains(buf, marker)` regression test would pass on today's
`main` in TTY mode and fail acceptance criterion 2; the robust form asserts on a
`permanentOutput(raw)` helper that keeps only newline-terminated segments
between `\r\x1b[K` delimiters, measured to return `""` for today's behavior in
both modes and the full line for every candidate fix. The fix must also
deliberately rewrite `TestRunCmdWithReporter_AllLinesViaStatus`
(gitutil_test.go:149) and the `runCmdWithReporter` docstring, both of which
currently assert the bug is intended; a `@critical` Gherkin scenario is
warranted under the guide's init→create→apply regression rule and needs one new
`I write an executable file …` step because the existing write-file step writes
mode `0o644`; and `go test ./...` is green at exit 0 across all 26 packages.
