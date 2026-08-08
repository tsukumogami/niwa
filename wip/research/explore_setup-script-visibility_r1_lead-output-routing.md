# Lead: How should setup-script output be routed so it survives both TTY and non-TTY runs, given `Reporter`'s existing contract?

## Findings

### 1. `Reporter` in full: methods, callers, invariants

`internal/workspace/reporter.go` is 195 lines and defines seven public methods plus
two internal ones. Fields: `w io.Writer`, `isTTY bool`, `needsClear bool`,
`deferred []string`, and a `mu sync.Mutex` guarding `spinMsg`, `spinFrame`,
`spinStop`, `spinDone` (reporter.go:25-36).

| Method | Location | Behavior | Callers (non-test) |
|---|---|---|---|
| `Status(msg)` | reporter.go:62-77 | **no-op when `!isTTY`**; on TTY sets `spinMsg` and starts `spinLoop` if not running | gitutil.go:106 (`runCmdWithReporter`), apply.go:804, apply.go:1333, apply.go:1353, snapshotwriter.go:239 |
| `Log(fmt, ...)` | reporter.go:137-142 | stops spinner on TTY, then `Fprintf(w, fmt+"\n")` — durable in **both** modes | 23 sites, all in `internal/workspace` and `internal/plugin` |
| `Warn(fmt, ...)` | reporter.go:146-148 | `Log("warning: "+fmt)` | 7 sites, incl. gitutil.go:69 (`runGitWithReporter`), apply.go:806 |
| `Defer(fmt, ...)` | reporter.go:153-155 | appends to `r.deferred`, prints nothing now | apply.go:1209, apply.go:1246 |
| `DeferWarn(fmt, ...)` | reporter.go:158-160 | appends `"warning: "+fmt` to `r.deferred` | 18 sites, all in apply.go — including **apply.go:1592, the setup-script failure site** |
| `FlushDeferred()` | reporter.go:165-170 | replays `r.deferred` through `Log`, clears | apply.go:502 (end of `Create`), apply.go:694 (end of `Apply`) |
| `Writer()` | reporter.go:175-177 | `io.Writer` adapter; each `Write` becomes one `Log` | apply.go:624, 1233, 1268, 1280, 1529, 1885 (guardrail/materializer `Stderr` sinks) |

**The invariant the TTY/non-TTY split protects** is stated in the type doc
(reporter.go:16-24) and is narrower than "two output modes": *`Status` is the only
method whose output is disposable.* Everything else — `Log`, `Warn`, `Defer`,
`DeferWarn`, `Writer` — is append-only and byte-identical in both modes.
`TestReporterNonTTYLog`/`Warn`/`Writer` (reporter_test.go:19-60) pin that the
non-TTY forms contain no `\r` or `\x1b` at all, and `DESIGN-clone-output-ux.md:622`
records the reason: "Non-TTY output is identical to today — no risk of breaking CI
pipelines or log parsers."

So the fix does **not** need a new durable channel. `Log` already is one. The scope
constraint ("don't rework Reporter") and the fix point in the same direction.

Two incidental observations: `needsClear` (reporter.go:28) is written at :68 and
:127 and never read by production code — only by three test assertions
(reporter_test.go:69, 95, 239). It is a dead field. And `FlushDeferred` /
`Defer` / `DeferWarn` touch `r.deferred` with no lock, unlike every other
mutable field.

### 2. `runGitWithReporter`'s classify-and-attach pattern, precisely

`internal/workspace/gitutil.go:53-85`. Mechanism:

1. `pr, pw := io.Pipe()`; `defer pw.Close()` immediately (gitutil.go:54-55). Both
   `cmd.Stdout` and `cmd.Stderr` point at `pw`, so the two streams are **merged and
   interleaved in write order** — there is no way to tell stdout from stderr
   downstream.
2. A goroutine runs `bufio.NewScanner(pr)`; each line goes through
   `stripEscapes` (CSI `\x1b\[[0-9;]*[A-Za-z]` and OSC `\x1b\][^\x07]*\x07`,
   gitutil.go:13-23) before anything else.
3. **The classifier** is `isGitErrorLine` (gitutil.go:28-33): `strings.TrimLeft(line, " \t")`
   then `HasPrefix` on exactly three literals — `"fatal:"`, `"error:"`, `"warning:"`.
   Nothing else. There is no severity model, no regex, no stderr/stdout distinction.
4. A matching line is **both** printed immediately via `r.Warn` **and** appended to a
   local `errorLines []string` (gitutil.go:68-70). Non-matching lines are dropped on
   the floor (gitutil.go:71-72) with the comment that niwa emits its own curated
   completion messages.
5. Ordering/lifecycle: `runErr := cmd.Run()`, then `pw.Close()`, then `<-done`
   (gitutil.go:77-79). The `<-done` join is what makes the goroutine's writes to
   `errorLines` visible to the main goroutine without a lock.
6. **The wrap** is one line: `return fmt.Errorf("%w\n%s", runErr, strings.Join(errorLines, "\n"))`
   (gitutil.go:82), and only when `runErr != nil && len(errorLines) > 0`. It wraps with
   `%w`, so `errors.Is`/`errors.As` against `*exec.ExitError` still works — which
   matters, because `isPermanentCloneError` (apply.go:~2234) inspects clone errors.

**What the caller does with it.** Callers do nothing special: they treat it as an
ordinary error and let the message carry. `clone.go:64` and `:70` return it up
through `cloneWithRetry` → `cloneWorker` → apply.go:1338
(`fmt.Errorf("cloning repo %s: %w", ...)`) → aborts the run and prints via cobra.
`sync.go:68`/`:92` do the same. The value delivered is entirely in the error's
`Error()` string. `TestRunGitWithReporter_EmbedsDiagnostic` (gitutil_test.go:132-147)
pins only that the message is not the bare `"exit status N"`.

This same `%w\n%s`-with-captured-output idiom already appears three more times
outside gitutil: `bootstrap.go:253` and `:261`
(`fmt.Errorf("...: %w\n%s", addErr, out)` over `CombinedOutput()`), and
`fallback.go:151`. It is an established niwa convention, not a one-off.

### 3. `runCmdWithReporter` — what actually happens today

`gitutil.go:93-116` is the same pipe/scanner/join skeleton with two deletions: no
classifier, and **no capture**. Every stripped line goes to `r.Status(line)`
(gitutil.go:106) and is then discarded.

Off-TTY the line dies at reporter.go:63 (`if !r.isTTY { return }`) before touching
the writer. On a TTY the situation is worse than "transient": `spinMsg` is
overwritten by every line, but it is only ever *rendered* by `doTick`, which runs
once at goroutine start and then on a 100 ms ticker (reporter.go:84-97). A script
that emits 500 lines in 200 ms renders **two or three of them**; `stopSpinner`
(reporter.go:131) then erases the last one with `\r\033[K`. So on a TTY the operator
sees a flickering sample of the output and zero of it afterwards.

Only one call site: `setup.go:107`, inside `RunSetupScripts`. On failure the raw
`err` (a bare `*exec.ExitError`) is stored in `ScriptResult.Error` (setup.go:108-111),
the loop `break`s (setup.go:112), and apply.go:1590-1595 turns it into
`DeferWarn("setup script %s/%s failed for %s: %v", ...)` — which renders as
`warning: setup script scripts/setup/01-x.sh failed for repo: exit status 1`
and nothing more. That is the whole defect in one line.

`RunSetupScripts` has exactly one production caller (apply.go:1584), in a
**sequential** `for _, cr := range classified` loop at apply.go:1581.

### 4. The design docs contradict *themselves*, not just the code

This was the biggest surprise. `DESIGN-clone-output-ux.md` — the doc that
introduced `Reporter` and both helpers — specifies `runCmdWithReporter` **four
times, and one of them disagrees with the other three**:

- Components table, line 381-382: "runCmdWithReporter: same pipe pattern, all lines
  via reporter.Log, no classifier (setup scripts)"
- Components table, line 393-394: "`setup.go` … uses runCmdWithReporter (all lines →
  reporter.Log; no git-specific classifier for arbitrary scripts)"
- Data Flow, line 508: "`RunSetupScripts` … → `runCmdWithReporter(reporter, script-cmd)`
  / all output → `reporter.Log(line)`"
- Phase 4, line 575-576: "`runCmdWithReporter`: for non-git subprocesses (setup
  scripts). No classifier — all lines route through `r.Log`."
- **Key Interfaces, line 435-438** (the Go doc comment block): "No line
  classification — all output through **r.Status** (transient; silent in
  non-TTY/CI contexts)."

The implementation (commit `2de1b56`, "feat: add TTY progress spinner, git error
routing, and one-time notice suppression", 2026-04-19) followed the Key Interfaces
block and copied its comment nearly verbatim into gitutil.go:87-92, including the
phrase "so script output is silent in piped/CI contexts." The divergence was
authored inside the design document, then faithfully implemented. Reconciling the
docs therefore means fixing an internal inconsistency in `DESIGN-clone-output-ux.md`
as well as the promise in `DESIGN-post-clone-scripts.md:132`.

Also note `DESIGN-clone-output-ux.md:322-350` ("Decision Outcome") lists `setup.go`
among the call sites using `runGitWithReporter` — a third variant of the story.

### 5. Prior art for buffer-and-replay, and the standing rejection

Nothing in the Reporter/apply path buffers subprocess output for later replay. The
closest patterns are one-shot whole-output captures that get embedded in an error:
`bootstrap.go:252,261`, `fallback.go:149`, `worktree/worktree.go:208`,
`watch/fetch.go:153`, `gitexclude/exclude.go:125`, `scan.go:387`, and
`vault/infisical/subprocess.go:64-65` (which keeps stdout and stderr in separate
`bytes.Buffer`s and returns both). So "capture into a buffer, surface it only when
the command fails" is well-established in this codebase — just never through the
Reporter.

**`DESIGN-clone-output-ux.md:271-276` explicitly rejected buffer-and-replay**, for
two reasons: (i) "no inline progress display during the operation (contradicts the
design goal)", and (ii) "the replayed buffer on failure contains raw
`\r`-terminated frame noise that renders incorrectly through Reporter."

Both reasons are already neutralized for setup scripts by the current code, and
this needs to be stated explicitly in whatever artifact this exploration produces:

- (i) does not apply if `Status(line)` is *kept* alongside buffering. The buffer is
  additive; the spinner keeps doing exactly what it does now.
- (ii) does not apply because buffering would happen **after** `bufio.Scanner`'s
  `\n` splitting and **after** `stripEscapes`. The `\r` frame noise the doc worried
  about is discarded by the scanner before any buffer would see it — the doc's own
  Decision 2 says so at line 258-260.

The rejection was also reasoned about *git* output specifically, where niwa
substitutes its own curated completion lines. Setup scripts have no curated
substitute, which is the asymmetry the original decision missed.

### 6. Routing options and their consequences

**(a) New `Reporter` method that writes durably in both modes.**
Whatever it did would be `Log` plus a prefix, since `Log` is already the
both-modes-durable channel. Adds public API surface for no new capability, and
touches the file the scope says not to rework. A prefix helper local to `setup.go`
costs less. *Unless* the method also carried dim/indent styling for sub-output, in
which case it would be a real (if optional) addition. Recommend against.

**(b) Reuse `Log` directly, per line.** Smallest conceptual change and matches what
three of the four design-doc passages actually specified. Spinner interaction is
benign *if* `Status` is dropped from the loop rather than kept alongside: the first
`Log` calls `stopSpinner`, which closes `spinStop` and blocks on `<-spinDone`
(reporter.go:129-130), and nothing restarts the spinner for the rest of the script.
If instead both `Status` and `Log` were called per line, every line would spawn and
join a goroutine — pathological for a chatty script. Ordering stays correct either
way: one scanner goroutine, sequential `Scan()`. **Behavior change: successful
applies become as loud as the loudest setup script.** This is exactly research
lead 4's concern and the only reason not to pick (b) outright.

**(c) Buffer per script, replay only on failure.** Keep `Status(line)` for the
unchanged TTY spinner and additionally append the stripped line to a `[]string`.
On non-zero exit, replay through `Warn`/`Log` prefixed with repo and script name.
Success output is unchanged from today in both modes; failure output is complete
in both modes. Ordering is correct — the replay happens inside `RunSetupScripts`
right after `cmd.Run()` returns, so it lands before the next script or repo. Needs
an explicit cap (tail-N lines or N bytes) or a runaway script pins its whole
output in memory; that cap is a design decision that belongs in the doc, not in an
undocumented constant. Contradicts the standing rejection in
`DESIGN-clone-output-ux.md:271-276` on paper, but see section 5 — both stated
reasons are void here, and saying so explicitly is required.

**(d) Wrap the failure error with the captured output, `runGitWithReporter`-style.**
Mechanically (c) minus the printing: buffer the lines, and on failure
`return fmt.Errorf("%w\n%s", runErr, strings.Join(lines, "\n"))`. **This requires
zero changes in `apply.go`** — the existing `DeferWarn(... %v", ..., sr.Error)` at
apply.go:1592 formats the wrapped error, `FlushDeferred` routes it through `Log`,
and `Log` is durable in both modes. Smallest diff of any option, reuses an idiom
that already appears four times in the repo, and `%w` preserves error-identity for
any `errors.Is` inspection. Consequence: the script's output appears in the
deferred block at the end of the run rather than inline where it happened. That is
acceptable because the `DeferWarn` line already names the repo, the setup dir and
the script, so the block is self-describing — and deferred-warning-at-the-end is
already niwa's convention for setup failures. Cosmetic wart: only the first line of
a multi-line deferred message carries the `warning: ` prefix.

**(e) Hybrid — recommended.** (d) as the failure channel, plus the two things the
design promises and the code never delivered:
- Buffer stripped lines during the run with an explicit cap; keep `Status` for the
  TTY spinner so success-path UX is untouched.
- Attach the captured tail to the returned error (d), so apply.go:1590-1595 needs
  no edit and the detail lands in exactly one place — no risk of printing it twice.
- Emit a durable per-script line via `Log` *before* each `exec.Command` runs
  (`setup.go:104`). This satisfies `DESIGN-post-clone-scripts.md:137-144`'s promised
  progress lines and its Security Considerations claim at line 277-278 that niwa
  "prints each script name before execution" — currently false. It is a handful of
  lines and is independently required by the scope, and it also supplies the
  repo-name prefix promised at `DESIGN-post-clone-scripts.md:132`.

The one thing (e) does *not* give is streaming visibility of a long-running
successful script off-TTY. If that turns out to matter, it is a `--verbose` gate on
top of (b), not a different failure design — and there is no `--verbose` today
(section 7).

### 7. Flag plumbing and what governs `isTTY`

There is **no `--verbose` and no `--quiet` anywhere in the CLI.** `--no-progress` is
the only flag that reaches the Reporter: registered on
`rootCmd.PersistentFlags()` at `internal/cli/root.go:61`, stored in the package
var `noProgress` (root.go:17), and consumed at exactly two sites:

```
internal/cli/apply.go:152   applier.Reporter = workspace.NewReporterWithTTY(os.Stderr, !noProgress && term.IsTerminal(int(os.Stderr.Fd())))
internal/cli/create.go:167  applier.Reporter = workspace.NewReporterWithTTY(os.Stderr, !noProgress && term.IsTerminal(int(os.Stderr.Fd())))
```

Every other production construction uses plain `NewReporter(w)`, which auto-detects
via `term.IsTerminal` on the fd if `w` is an `*os.File` and otherwise defaults to
`false` (reporter.go:41-47): `cli/init.go:176,640,1046,1069`, `cli/plugins.go:47`,
`cli/destroy.go:131,342`, `cli/reset.go:125`, `cli/config_set.go:76`,
`cli/instance_from_hook.go:366`, and the `NewApplier` default at apply.go:174.

Two consequences that matter here. First, `cli/instance_from_hook.go:366` — the
SessionStart ephemeral-instance path — builds `NewReporter(os.Stderr)` in a hook
context where stderr is a pipe, so `isTTY=false` and setup output is dropped
without any flag being involved. Second, `--no-progress` currently makes the
problem *worse*: it forces `isTTY=false` on an interactive terminal, which under
today's routing silences setup output entirely. Under any of (b)/(c)/(d)/(e) that
stops being true, which is itself an argument for the change —
`DESIGN-clone-output-ux.md:337-339` says `--no-progress` "suppresses the status
line without affecting completion lines", and today it suppresses real content.

`status --verbose` (`internal/cli/status.go:28`) is a local flag on that one
command and never reaches a Reporter. `internal/workspace/required.go:144` carries
the comment "no verbose flag yet; when one lands the loop can emit an info line" —
confirming none exists.

### 8. Concurrency

**The shared `Reporter` is deliberately kept out of the parallel phase.**
`Applier.cloneWorker` (apply.go:2175-2211) opens with
`noop := NewReporterWithTTY(io.Discard, false)` and passes *that* to
`cloneWithRetry` and `SyncRepo`; results come back over a channel and the main
goroutine does all the `DeferWarn`ing (apply.go:1343, 1346). So during the
concurrent clone phase, `a.Reporter` is touched only by the collecting loop.

The one genuine cross-goroutine use is inside the two gitutil helpers: the scanner
goroutine calls `r.Warn` (gitutil.go:69) or `r.Status` (gitutil.go:106) while the
main goroutine is blocked in `cmd.Run()`. That is safe, but **safe by blocking, not
by locking**, and nothing documents the invariant.

The locking itself is uneven. `r.mu` guards `spinMsg`/`spinFrame`/`spinStop`/`spinDone`,
but the actual `Fprintf(r.w, ...)` in `Log` (reporter.go:141) and the clear in
`stopSpinner` (reporter.go:131) happen **outside** the mutex. They avoid racing the
spinner only because `stopSpinner` closes `stop` and blocks on `<-done`
(reporter.go:129-130) before writing, so the spinner goroutine is provably gone.
`r.deferred` (reporter.go:29) is appended by `Defer`/`DeferWarn` with no
synchronization at all.

Practical implication for the routing fix: **option (d) keeps every `Defer`/`DeferWarn`
on the main goroutine and is race-free by construction.** Options (b) and (c) print
from the scanner goroutine; that is safe today for the same blocking reason
`runGitWithReporter` is, but it leans on an undocumented invariant, and anything
that later runs setup scripts concurrently across repos (apply.go:1581 is
sequential today, but the clone loop right above it is not) would break it. A
one-line comment recording "the scanner goroutine may call Reporter only while the
main goroutine is inside cmd.Run()" is worth adding whichever option wins.

There is no `Reporter.Status` spinner interaction that corrupts interleaved `Log`
output — `TestReporterTTYLogClearsStatus` (reporter_test.go:79-97) pins the
`"\r\033[Kcloned foo\n"` suffix.

### 9. Existing test coverage and what would have to change

`internal/workspace/setup_test.go` (194 lines) covers `ResolveSetupDir`'s four
resolution paths, disabled/missing/empty dirs, success ordering, stop-on-error,
non-executable skip, and lexical order. Every one of them passes
`NewReporterWithTTY(&bytes.Buffer{}, false)` and **never inspects the buffer.** No
assertion anywhere touches script output.

The blocker is elsewhere: **`gitutil_test.go:151-171`
(`TestRunCmdWithReporter_AllLinesViaStatus`) actively pins the defect.** It asserts
that `"fatal: this is fine\n"` must *not* appear in the buffer, with the comment
"Script output is transient — permanent newline-terminated lines must not appear."
Any of options (b)/(c)/(e)-with-inline-printing makes this test fail by design; it
must be rewritten, not worked around. Option (d) alone leaves it passing, since the
lines go into the error rather than the writer — worth knowing, because a fix that
doesn't touch this test is a fix that didn't change the printing path.

`TestRunGitWithReporter_RoutesLinesThrough` (gitutil_test.go:108-127) has a stale
doc comment claiming informational lines route "through `r.Log`"; the
implementation discards them (gitutil.go:71-72). Harmless but confusing.

`test/functional/features/` has 29 feature files and **not one mentions setup
scripts** (`grep -rln "setup_dir\|scripts/setup" test/` returns nothing). Per the
repo's CLAUDE.md convention — "when you ship a user-facing CLI command or fix a
regression in the init → create → apply workflow, add a `@critical` Gherkin
scenario" — this is a regression in the create/apply workflow, so a `@critical`
scenario is warranted and would be the first functional coverage setup scripts have
ever had.

## Implications

The scope's constraint ("fix the routing, not the reporter's design") is not just
acceptable here, it is the *only* reading consistent with the code: `Log` is
already a both-modes-durable channel with append-only, escape-free, CI-safe
semantics pinned by tests. No new `Reporter` method is needed. The work is confined
to `internal/workspace/gitutil.go` and `internal/workspace/setup.go`, plus at most a
progress-line addition in `setup.go` — `apply.go` need not change at all under the
recommended option.

The recommended shape is (e): buffer the already-stripped lines with an explicit
cap, keep `Status` so the TTY success path is untouched, attach the captured tail to
the returned error with `fmt.Errorf("%w\n%s", ...)` exactly as `runGitWithReporter`
does at gitutil.go:82, and add the per-script `Log` line before execution that both
design docs already promise. That delivers visibility in both modes, keeps a
successful apply as quiet as it is today, reuses an idiom the repo already uses four
times, requires no `apply.go` edit, and stays clear of the unguarded `r.deferred`
slice by keeping all `DeferWarn` calls on the main goroutine.

The doc reconciliation is larger than the scope assumed. It is not "the
implementation drifted from `DESIGN-post-clone-scripts.md`" — it is that
`DESIGN-clone-output-ux.md` contradicts itself in four places, the implementer
followed the one that says `Status`, and `DESIGN-post-clone-scripts.md`'s Decision 2
and Security Considerations were never satisfied by any version. Whatever artifact
this exploration produces has to fix an internal inconsistency in one design doc and
an unmet promise in another, and it has to state explicitly why
`DESIGN-clone-output-ux.md:271-276`'s rejection of buffer-and-replay does not bind
here (both stated reasons are already void — see section 5).

The buffer cap is the one number that must not become an undocumented constant. It
is the direct answer to research lead 4 and belongs in the design text with its
rationale.

## Surprises

1. **The design doc disagrees with itself.** Four passages of
   `DESIGN-clone-output-ux.md` say `runCmdWithReporter` routes through `Log`; the
   Key Interfaces block at line 435-438 says `Status` and calls it "silent in
   non-TTY/CI contexts". The implementation copied that one block, comment and all.
   The defect was designed in, not introduced by drift.

2. **A test actively defends the defect.** `gitutil_test.go:151-171` asserts that
   setup-script output must *not* appear as permanent lines. Any inline-printing fix
   fails an existing green test.

3. **On a TTY the output is not merely transient — it is mostly never drawn.**
   `Status` only sets a string; rendering happens on `doTick`'s 100 ms ticker
   (reporter.go:87). A fast, chatty script renders two or three of hundreds of lines.
   The scope's "overwritten and then cleared" understates it.

4. **`--no-progress` currently suppresses real content, not just animation.** It
   forces `isTTY=false` at cli/apply.go:152 and cli/create.go:167, which today makes
   setup output vanish on an interactive terminal — directly contrary to
   `DESIGN-clone-output-ux.md:337-339`.

5. **The failure path needs no `apply.go` change.** apply.go:1590-1595 already
   formats `sr.Error` with `%v` into a `DeferWarn`, and `FlushDeferred` → `Log` is
   durable in both modes. Enriching the error at gitutil.go:115 is sufficient end to
   end.

6. **`needsClear` is dead** (reporter.go:28, written at :68 and :127, read only by
   tests), and `r.deferred` is the one mutable Reporter field with no lock.

7. **Setup scripts have zero functional-test coverage** across 29 feature files.

## Open Questions

- What is the cap, and what shape — last N lines, N bytes, or head+tail with an
  elision marker? Needs a number and a stated rationale in the design.
- Should a *successful* script's output ever be visible? (e) says no, which means a
  passing-but-suspicious script stays opaque. If the answer is yes, it needs a
  `--verbose` flag, and none exists today — that is new CLI surface and arguably a
  separate change.
- Deferred block versus inline: (d) puts failure output in the flushed block at the
  end of the run. Is that acceptable, or does the operator need it adjacent to the
  repo it belongs to? The `DeferWarn` message already names repo + dir + script, so
  I lean deferred, but it is a UX call.
- `bufio.Scanner`'s 64 KB default token limit is unhandled in both helpers —
  `scanner.Err()` is never checked at gitutil.go:73 or :107. A single >64 KB line
  makes the scanner exit, `pr.Close()` fires, and the script's next write gets
  EPIPE, likely changing its exit status. Pre-existing, orthogonal to this fix, but
  it becomes more visible once output is actually surfaced.
- Should the per-script `Log` line print on every apply, including the common
  no-op re-apply? Chatty on a 10-repo workspace with setup scripts everywhere.
  Possibly gate it on the script actually being about to run (it already is) and
  accept the noise, since `DESIGN-post-clone-scripts.md:277-278` treats it as a
  security affordance.
- Does the stale doc comment on `TestRunGitWithReporter_RoutesLinesThrough`
  (gitutil_test.go:108-110, claiming `r.Log` routing) indicate a *third* intended
  behavior for git lines that also got lost? Low stakes, but worth a glance while
  the file is open.

## Summary

`Reporter` already has the durable both-modes channel this needs — `Log` — so no
new method and no reporter rework is required; the entire fix lives in
`gitutil.go`'s `runCmdWithReporter` and `setup.go`, and under the recommended
approach `apply.go` does not change at all, because apply.go:1592 already formats
the script error through `DeferWarn` → `FlushDeferred` → `Log`. The recommendation
is to mirror `runGitWithReporter` exactly: buffer the already-escape-stripped lines
with an explicit documented cap, keep `Status` so the TTY success path is untouched,
and attach the captured tail to the returned error via `fmt.Errorf("%w\n%s", ...)`
— plus the per-script progress line before execution that both design docs promise
and neither got. Two things will surprise the implementer:
`DESIGN-clone-output-ux.md` specifies `Log` in four places and `Status` in one and
the code followed the odd one out, and `gitutil_test.go:151-171` is a currently-green
test that actively asserts the defective behavior.
