# Lead: What acceptance criteria for an interactive terminal capture are actually verifiable in this repo, and by what harness?

## Findings

### 1. The test infrastructure: three layers, all already built

**Makefile targets** (`Makefile`):

| Target | Line | What it runs |
|---|---|---|
| `test` | `Makefile:7-8` | `go test ./...` — unit tests |
| `build-test` | `Makefile:12-13` | builds `niwa-test` for the functional suite |
| `test-functional` | `Makefile:17-21` | full godog suite with `NIWA_TEST_BINARY` set |
| `test-functional-critical` | `Makefile:24-29` | same, with `NIWA_TEST_TAGS=@critical` |
| `test-functional-claude-integration` | `Makefile:32-35` | needs a real `claude` + `ANTHROPIC_API_KEY` |
| `test-install` | `Makefile:42-45` | `NIWA_TEST_PATHS=features/install-integration.feature` |
| `test-live` | `Makefile:54-55` | `-tags live`, real claude lifecycle |

`NIWA_TEST_PATHS` (`suite_test.go:87-90`) is the knob for running one feature
file, which is how I timed things below.

**CI** (`.github/workflows/test.yml`) runs on `ubuntu-latest` only
(`test.yml:28`): `go vet` (`:49`), `go test ./...` (`:52`), then
`make test-functional` (`:55`) — the FULL suite, not just `@critical`. So
anything landed in `test/functional/features/` runs on every PR touching Go
files, `Makefile`, or `test/functional/**` (`test.yml:14-23`). There is no
macOS job.

**The sandbox.** `suite_test.go:111-183` gives every scenario a fresh
`$HOME`, `$XDG_CONFIG_HOME`, `$TMPDIR`, a `workspaceRoot` under
`os.TempDir()`, a `localGitServer` (`localrepo_test.go`), and a
`sharedBinDir` carrying hermetic stubs. `testState` (`suite_test.go:22-70`)
carries `stdout`, `stderr`, `exitCode`, `pathPrefix` (for a fake `claude`),
and `envOverrides`.

**Critical detail for this feature:** `runNiwa` (`steps_test.go:144-168`)
never sets `cmd.Stdin`. Go's `os/exec` then gives the child `/dev/null`, so
`IsStdinTTY()` returns **false** in every ordinary functional scenario. The
non-TTY path is therefore testable with the plain, fast, already-registered
`I run "..."` steps — no PTY needed.

`docs/guides/functional-testing.md` documents all of this, including the
"@critical scenario whenever you ship a user-facing CLI command" rule and the
anatomy template.

### 2. The `@critical` convention: 99 scenarios across 27 feature files

`grep -h "@critical" test/functional/features/*.feature | wc -l` → **99**.
Distribution is uneven and roughly tracks blast radius:
`critical-path.feature` 12, `worktree-delegation.feature` 8,
`shell-navigation.feature` 8, `init_bootstrap_failures.feature` 8,
`dispatch.feature` 6, `completion.feature` 6.

**The house shape**, read off `dispatch.feature` and
`init_bootstrap_failures.feature`:

1. A file-level `Feature:` block with a prose paragraph explaining the fake
   infrastructure and a `Design:` / `PRD:` pointer
   (`dispatch.feature:1-20`, `init_bootstrap_failures.feature:1-9`).
2. A `# --- section comment ---` before each scenario or group
   (`dispatch.feature:22`, `:41`, `:59`).
3. Scenario names state what regresses, not what is called
   ("a launch failure rolls the dispatch instance back",
   `dispatch.feature:62`).
4. Setup is `Given a clean niwa environment` → `a local git server is set up`
   → `a config repo "myws" exists with body:` → `I run niwa init from config
   repo "myws"` → `Then the exit code is 0` (`dispatch.feature:26-34`).
5. Assertions are `the exit code is N` plus one or two domain assertions.
   Failure-mode scenarios assert the **exact** user-visible substring
   (`init_bootstrap_failures.feature:18`, `:27`, `:41`).
6. Scenarios needing a real `claude` gate on availability and skip rather than
   fail (`docs/guides/functional-testing.md`, "Testing the worktree commands").

**A `@critical` scenario for paste capture would look like** (using only
existing steps plus the two harness extensions named in §3):

```gherkin
  @critical
  Scenario: an interactive paste becomes the dispatched worker's prompt
    Given a clean niwa environment
    And a local git server is set up
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
    When I run "niwa dispatch --detach" under a pty with input "\e[200~panic: nil map\ngoroutine 1:\e[201~\n"
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    And the launched claude was invoked with "panic: nil map"
```

Every `Then` here already exists (`dispatch_steps_test.go:369-380`), and
`aDispatchInstanceWasCreatedWithAWellFormedInstanceFile` falls back to
`findDispatchInstance(s.workspaceRoot)` when `lastDispatchInstancePath` is
unset (`dispatch_steps_test.go:170-173`), which is exactly the state a PTY run
leaves it in — so the PTY step composes with the dispatch assertions without
modification.

**And yes, the harness can drive an interactive terminal.** See §3.

### 3. The PTY harness: verified, and I measured its limits

**It exists and it is real.** `iRunUnderPTYWithInput` at
`test/functional/steps_init_bootstrap_test.go:143-199`, registered at
`suite_test.go:365`. It:

- requires util-linux `script` on PATH, erroring (not skipping) when absent
  (`steps_init_bootstrap_test.go:148-150`);
- interpolates `{repo:<name>}` placeholders (`:154-156`);
- builds `cd <workspaceRoot> && exec <binary> <args>` and runs it as
  `script -q -c <innerCmd> /dev/null` (`:170`, `:178`) — the `exec` is
  deliberate, per the comment at `:166-168`;
- feeds input via `cmd.Stdin = strings.NewReader(rawInput)` after expanding
  **only** `\n` (`:181-182`);
- collapses stdout and stderr: `s.stderr = stdout.String() + stderr.String()`
  (`:193`), because `script` interleaves both on one pty surface.

It is used today by two `@critical` scenarios
(`init_bootstrap_failures.feature:53`, `:61`). I ran that feature file:
**passes in 9.55s**. The dispatch feature runs in 1.33s. `go test ./...`
passes clean (all packages ok, `internal/cli` 6.47s).

**I then built a probe** replicating the proposed capture (raw mode via
`term.MakeRaw`, `SetBracketedPasteMode(true)`, an
`ErrPasteIndicator`-accumulating `ReadLine` loop over an `io.ReadWriter`
adapter) and drove it under exactly the harness's
`script -q -c … /dev/null` invocation. Results:

| Probe | Outcome |
|---|---|
| `ESC[200~a\nb\nc ESC[201~ \n` | `TTY=true`, DECSET 2004 emitted, 3 lines accumulated, one Enter submits. Bracketed paste works end to end through this harness. |
| Repeat 20× | Byte-identical output every run on this machine. Deterministic. |
| Paste ending in `\n` inside the markers, then Enter | Captured `"alpha\nbeta\n"` — the bare-paste case, one Enter. |
| Paste with no trailing newline, then typed text, then Enter | Captured `"alpha\nbeta please fix this"` — the annotation lands **concatenated onto the last pasted line**, confirming the exploration's UX edge empirically. |
| `0x03` (Ctrl-C) | `ReadLine` returns `io.EOF`, not a distinct sentinel. Raw mode suppresses `ISIG`, so no `SIGINT` reaches the process. Confirms the `tui.ErrCanceled` gap. |
| **No terminating gesture (input hits EOF)** | **HANGS.** `script` does not propagate its stdin EOF to the pty; measured `rc=124` after a 20s external timeout. |
| Single pasted line of 4000 bytes | OK, 4000 bytes captured. |
| Single pasted line of 4090 bytes | **HANGS** — `ESC[200~` (6 bytes) + 4090 = 4096 hits the N_TTY canonical line limit; the closing marker is discarded, so the reader waits forever. |
| Same 4090-byte line, with a 0.5s delay before feeding | OK. The child reaches `MakeRaw` first, canonical mode never applies. 12000 bytes on one line also OK with the delay; 20000 did not complete inside 20s. |
| **2000 lines × 40 bytes (~82 KB)** | OK — 84000 bytes captured, byte-exact. |
| **5000 lines × 40 bytes (~205 KB), no delay** | OK — 210000 bytes captured, byte-exact, in **2.07s**. |

**What that means concretely:**

- The binding constraint is **per-line length, not total size**. Total payload
  well past the 128 KB `maxPromptBytes` cap goes through cleanly and fast, as
  long as no single line exceeds ~4090 bytes. Real pasted logs are
  many short lines, so a **size-ceiling acceptance criterion is PTY-testable**.
- **Any PTY scenario whose input does not end in a submit gesture hangs.**
  There is no timeout in the step — `exec.CommandContext(ctx, …)`
  (`:178`) uses the godog scenario context, which carries no deadline. A
  malformed scenario burns `go test`'s 10-minute default and panics the whole
  suite. This is the single biggest hazard of writing PTY criteria.
- **Stream separation is not assertable under the PTY harness.** `:193`
  writes the same bytes into both `s.stdout` and `s.stderr`, so
  "the prompt goes to stderr, the result to stdout" — a real house convention
  (`destroy.go:120` vs `:135`) — cannot be checked there. It must be checked
  at the unit layer with injected writers.

**Three harness gaps a paste scenario needs closed** (all small):

1. `steps_init_bootstrap_test.go:181` expands only `\n`. Feeding
   `ESC[200~`/`ESC[201~` from a feature file needs `\e` (or `\x1b`) added —
   one line.
2. Quoted Gherkin strings can't carry a 130 KB payload. A size-ceiling
   scenario needs a generator step, e.g.
   `When I run "<cmd>" under a pty pasting <N> lines of <M> bytes`.
3. `theLaunchedClaudeWasInvokedWith` (`dispatch_steps_test.go:350-364`) takes
   a single-line quoted string. Asserting a multi-line captured prompt reached
   the worker verbatim needs a DocString variant. The fake claude already
   records the full argv line to `$HOME/dispatch-launch-argv`
   (`dispatch_steps_test.go:54`), so the data is there.

**Portability note.** The step's comment (`:139-142`) claims `script` "ships
on every Linux CI image and on macOS via Homebrew," and records that
`github.com/creack/pty` was considered and rejected to avoid a dependency.
`-c` is a util-linux flag; macOS's BSD `script` uses
`script [-adkpqr] [file [command ...]]` and has no `-c`. Because
`exec.LookPath("script")` succeeds on macOS (BSD `script` is present), the
step would **fail rather than skip** on a developer's Mac. CI is
ubuntu-only (`test.yml:28`) so this never bites CI, but every PTY scenario
added widens the gap between "passes in CI" and "passes on a Mac laptop." I
could not verify BSD behavior from this Linux host; it is a known-flag-set
argument, not a measurement.

**Nothing else in the repo does PTY testing.** `grep` for `creack/pty`,
`termios`, or `pty` across the tree returns only `golang.org/x/term` usage
in `internal/tui/picker.go`, `internal/cli/prompt.go`, and this one step.
`go.mod` has no pty dependency.

### 4. What becomes unit-testable behind a seam

The repo's seam idiom is a package-level `var` holding a func, swapped and
restored via `t.Cleanup`. `installDispatchFakes`
(`internal/cli/dispatch_test.go:85-159`) swaps six at once: `lookClaude`
(`dispatch.go:87`), `provisionInstanceFunc`, `dispatchLaunch`
(`dispatch_launcher.go:14`), `dispatchCapture` (`dispatch.go:93`),
`dispatchAttach` (`dispatch.go:100`), `destroyInstanceFunc`. `stubTTY`
(`init_bootstrap_test.go:82-87`) does the same for `IsStdinTTY`
(`prompt.go:26`).

The second idiom is **exported wrapper over an injectable core**:
`Pick` → `pick(stdin io.Reader, stderr io.Writer, …)`
(`internal/tui/picker.go:64-75`), `ReadConfirmation(prompt, expected, in, out)`
(`prompt.go:42`), `promptBootstrap(in, out)` (`init.go:313`).
`picker.go:79` guards the raw-mode branch on `stderr.(*os.File)`, so a
`bytes.Buffer` skips `MakeRaw` entirely and the decode logic runs offline —
that is what makes `picker_test.go` work with no terminal at all, including
its `chunkedReader` (`picker_test.go:203-217`) that caps each `Read` so
multi-byte escape sequences arrive one at a time.

**Two seams, and what each buys.** They are complementary, and the PRD's
criteria are cleanest if the design provides both:

*Seam A — the core function:*
```go
// exported wrapper used by runDispatch
func ReadPastePrompt() (string, error)
// injectable core, no terminal required
func readPastePrompt(in io.Reader, out io.Writer, limit int) (string, error)
```

*Seam B — the package-level var, wired into `runDispatch`:*
```go
var dispatchReadPrompt = ReadPastePrompt
```

Behaviors that become unit-testable with **no terminal at all** via Seam A,
driven by `bytes.NewReader` of raw bytes plus a `chunkedReader`, exactly as
`picker_test.go` does today:

1. A bracketed-paste block bounded by `ESC[200~`/`ESC[201~` accumulates into
   one multi-line string.
2. A single Enter after a paste that ended in a newline submits (bare-paste
   case).
3. Typed text after `ESC[201~` appends, then Enter submits (annotated case) —
   including *where* the annotation lands relative to the last pasted line.
4. The manual-newline chord (Ctrl+J, `0x0a`) inserts a newline instead of
   submitting.
5. Escape sequences split across `Read` boundaries still decode
   (`chunkedReader` with `chunk: 3` splits `ESC[200~` mid-marker).
6. The byte limit rejects at the paste boundary while the prompt stays alive —
   assert the returned prompt still contains the pre-overflow text and the
   writer received the rejection notice.
7. The limit counts what is actually sent, i.e. the `keepAliveArmingInstruction`
   prefix (measured: **638 bytes**, `dispatch_keepalive.go:33`) that
   `dispatch.go:327` prepends *after* the current check at `:144`.
8. Cancel returns a distinct sentinel rather than bare `io.EOF` (the probe
   confirms x/term collapses Ctrl-C into `io.EOF`, so this needs explicit
   `0x03` interception to satisfy the `tui.ErrCanceled` house contract at
   `picker.go:26-30`).
9. Immediate EOF on empty input returns a clean error, not a hang.
10. ANSI escapes inside pasted content: whether they are stripped from the
    echo (assert against the `bytes.Buffer` writer) and whether they survive
    in the returned prompt (assert the return value) — two separate assertions,
    which is the right shape given the exploration flagged these as two
    decisions currently written as one word.
11. Prompt/echo text goes to the writer and the result to the return value —
    the stream-separation criterion the PTY harness cannot check.

Behaviors that become testable via **Seam B**, in `dispatch_test.go` style:

12. Zero positional args + `IsStdinTTY()==true` calls the reader; the captured
    string reaches `dispatchLaunch` as the final argv element.
13. Zero positional args + `IsStdinTTY()==false` errors immediately, naming
    the positional form, and calls neither the reader nor `provisionInstanceFunc`
    (assert `f.provisionCalled == 0`, the exact idiom at `dispatch_test.go:434`).
14. A positional prompt still bypasses the reader entirely (regression guard).
15. Flag-combination rejections (`--detach` interplay, positional + capture)
    resolve before anything is provisioned.
16. Cancel leaves the workspace unchanged: `f.provisionCalled == 0`,
    `f.launchCalled == 0`, exit non-zero.
17. Prompt-size enforcement at the command layer, extending
    `TestDispatch_OverLongPrompt_Errors` (`dispatch_test.go:424-437`) to cover
    the keep-alive-prefix arithmetic.

That is 17 of the plausible criteria, all offline, all in the sub-10-second
`go test ./internal/cli` budget.

### 5. What genuinely needs a real terminal

| Behavior | Fakeable? | Verdict |
|---|---|---|
| `IsStdinTTY()` returns true on a real tty | No | PTY, but already covered transitively by the two existing `init_bootstrap_failures` scenarios. Not worth a dedicated scenario. |
| `term.MakeRaw` succeeds and the capture works in raw mode | No — `picker.go:79`'s `*os.File` guard means a `bytes.Buffer` skips `MakeRaw` entirely, so unit tests never exercise it | **PTY warranted.** This is the one thing the unit layer structurally cannot reach. |
| DECSET 2004 is actually written to the terminal | Partly (a `bytes.Buffer` sees the bytes) | Unit test suffices for "the bytes were emitted." |
| Terminal state restored after submit / after abandonment | No | **PTY warranted, and I verified it is assertable.** Wrapping the binary as `stty -g > before; niwa …; stty -g > after` under `script` and diffing produced IDENTICAL both after a normal submit and after Ctrl-C. The child still sees `TTY=true` and still receives the pasted input without the `exec` (verified) — so a sibling step that drops the `exec` at `steps_init_bootstrap_test.go:170` and runs a compound command works. |
| End-to-end: paste → capture → the worker's argv | No | **PTY warranted.** This is the `@critical` scenario, and it is the one CLAUDE.md's rule actually demands. |
| Terminals lacking DECSET 2004 support | No, and not even by PTY — `script` gives a fully capable pty | **Manual verification only.** No harness in this repo can produce a terminal that ignores `ESC[?2004h`. |
| Real terminal emulator paste behavior (iTerm2, Ghostty, tmux, Terminal.app) | No | **Manual.** tmux in particular re-wraps bracketed paste; nothing here models that. |
| Rendering/redraw quality of a large paste (flicker, wrapping, cursor math) | No | **Manual.** Also note x/term's echo cost appears superlinear — a 20000-byte single line did not complete in 20s under the probe while 12000 did. Worth a manual look, not a test. |
| Ctrl-C delivered as a signal rather than a byte | No | Raw mode suppresses `ISIG`; the probe confirms it arrives as `0x03`. Unit-testable once intercepted. |

### 6. Patterns a new test must follow to fit in

From `internal/cli/dispatch_test.go`:

- `setupDispatchWorkspace(t)` (`:34-51`) builds a workspace root with
  `.niwa/workspace.toml` and resolves symlinks so `ClassifyCwd` matches;
  `chdir(t, root)` (`:56-65`) restores cwd on cleanup.
- `installDispatchFakes(t, root)` (`:85-159`) swaps every seam, **resets the
  package-level flag vars to zero**, and restores everything in one
  `t.Cleanup`. A new `dispatchReadPrompt` seam must be added to both the swap
  list and the restore list — missing the restore leaks across tests, which is
  why the existing function is exhaustive.
- `runDispatchCmd(t, prompt)` (`:168-177`) builds a fresh `cobra.Command`,
  sets Out/Err buffers and a context, and calls `runDispatch(cmd,
  []string{prompt})`. **A capture test needs a zero-args variant** —
  `runDispatchCmdNoArgs(t)` calling `runDispatch(cmd, nil)` — because the
  current helper always passes exactly one positional.
- Assertions are on the recorded counters (`f.provisionCalled`,
  `f.launchCalled`, `f.attachCalled`, `f.destroyCalled`) plus the strings the
  fakes captured. `TestDispatch_OverLongPrompt_Errors` (`:424-437`) is the
  closest model for a "reject before touching anything" test.
- `dispatch_launcher_test.go` is the model for the pure-helper layer: table-free,
  one behavior per test, `reflect.DeepEqual` on the argv slice, and a
  security-shaped test (`:32-48`) asserting a metacharacter-laden prompt stays
  one argv element. A paste reader should get an equivalent adversarial test.
- File naming: **do not call it `dispatch_capture.go`.** That name is taken
  (`internal/cli/dispatch_capture.go`) and means session-UUID recovery by
  jobs-dir cwd correlation — an unrelated "capture." Its test
  (`dispatch_capture_test.go`) is a good model for subtests-with-`t.TempDir()`
  structure, but the name collision would be actively confusing.
- Comment density: every exported and unexported function in this package
  carries a multi-paragraph doc comment explaining *why*, often citing a design
  decision number. Tests carry a comment naming the regression they guard.

## Recommendation

**Write the PRD's acceptance criteria at three levels, and say which level
each one lives at.** All three harnesses exist today; only the third needs
(small) extension.

**Level 1 — unit tests over an injectable core** (`readPastePrompt(in, out,
limit)`, driven by `bytes.NewReader` + `chunkedReader`, modeled on
`internal/tui/picker_test.go`). This should carry the bulk of the contract:
paste-block accumulation, the one-Enter submit for both the bare and annotated
cases, the manual-newline chord, escape sequences split across reads, the size
ceiling rejecting at the paste boundary with the prompt still alive, the
keep-alive prefix counting against the ceiling, cancel returning a distinct
sentinel, EOF on empty input, ANSI-in-paste handling for echo and for the
stored prompt separately, and prompt-to-writer / result-to-return separation.
Roughly 11 of the criteria, all offline, all fast. **Write the criteria in
terms of the captured string and the writer's contents**, never in terms of
what a terminal displays.

**Level 2 — command-level unit tests over a `dispatchReadPrompt` seam**
(`internal/cli/dispatch_test.go` style). Six more criteria: TTY-yes calls the
reader and the captured text reaches the launcher's final argv element;
TTY-no errors immediately naming the positional form and provisions nothing;
a positional prompt bypasses the reader; the rejected flag combinations fail
before provisioning; cancel leaves `provisionCalled == 0`; the size check
accounts for the 638-byte keep-alive prefix.

**Level 3 — exactly two `@critical` PTY scenarios in `dispatch.feature`.**
CLAUDE.md's rule ("add a `@critical` Gherkin scenario when you ship a
user-facing CLI command") demands at least one, and the PTY harness genuinely
carries it — I verified bracketed paste, one-Enter submit, and a 205 KB
multi-line payload all work through `script -q -c`. Keep it to two, because
each one is a hang risk:

1. **Paste reaches the worker.** Paste a short multi-line block, assert exit 0,
   a well-formed instance, and that the launched claude's argv contains the
   pasted text. Uses only existing dispatch steps plus the `\e` escape fix.
2. **The size ceiling rejects.** Generate a payload past the cap (many short
   lines — measured at 210 KB in 2.07s), assert the exact error substring and
   that no dispatch instance remains. Needs a generator step.

Optionally a third for **terminal restoration**, which I verified is assertable
via `stty -g` before/after under `script`. It is the only way to check the
"terminal restored on abandonment" property, and that property is squarely in
the PRD's scope ("Clean abandonment: terminal restored, workspace unchanged").
I would include it; the workspace-unchanged half is already covered at Level 2.

**The non-TTY criterion should NOT use the PTY harness.** `runNiwa`
(`steps_test.go:144-168`) leaves stdin at `/dev/null`, so a plain
`When I run "niwa dispatch"` scenario already exercises the non-TTY path,
fast and with zero hang risk — exactly the shape of
`init_bootstrap_failures.feature:34-41`. That is also the cheapest guard for
the hard requirement that a scripted or hooked invocation must never block.

**Three harness changes are prerequisites**, and the PRD should say so:
add `\e` expansion at `steps_init_bootstrap_test.go:181`; add a generated-payload
PTY step; add a DocString variant of `theLaunchedClaudeWasInvokedWith`. I would
also **add an explicit timeout to `iRunUnderPTYWithInput`** (a
`context.WithTimeout` around `:178`) — measured behavior is that `script` does
not propagate stdin EOF, so a scenario missing its submit gesture hangs until
`go test`'s 10-minute panic and takes the whole suite with it. That is a
one-line change that converts the worst failure mode from "CI mystery hang"
into "step failed after 30s."

**Leave to manual verification, and say so in the PRD:** behavior in terminals
that do not support DECSET 2004; behavior under tmux and in specific emulators;
and rendering quality (flicker, wrapping, cursor arithmetic) for a large paste.
None of these can be produced by any harness in this repo, and writing criteria
against them would be decoration. A PRD line of the form "verified manually
against <named terminals> before release" is honest; a Gherkin scenario would
not be.

## Open Questions

1. **Is a macOS-failing PTY scenario acceptable?** CI is ubuntu-only, but
   `iRunUnderPTYWithInput` errors rather than skips when `script -c` is
   unusable. Three new PTY scenarios makes `make test-functional` a
   Linux-only command in practice. The alternative — skip instead of error
   when `script` is not util-linux — trades a broken Mac run for silent
   coverage loss. Needs a call.
2. **Which terminals count as "supported" for the manual-verification list?**
   The PRD needs a named set, or the manual criterion is unfalsifiable.
3. **Should the size ceiling be asserted as an exact number in a Gherkin
   step?** An exact-substring assertion on the byte count couples the feature
   file to the constant; a looser "is too long" substring does not. The house
   style at `init_bootstrap_failures.feature:18` favors exact strings.
4. **Does the annotation-placement edge** (typed text concatenating onto the
   last pasted line, which I reproduced) get a stated requirement? If yes it is
   a Level-1 criterion; if it is accepted as-is it should still be a
   characterization test so a future change is a deliberate one.
5. **Ctrl-C as `ErrCanceled` vs `io.EOF`:** the house contract
   (`picker.go:26-30`) wants a distinct sentinel, x/term gives `io.EOF`.
   Whether the PRD requires the distinction determines whether criterion 8
   exists at all.

## Summary

The repo has everything needed: a godog functional suite whose ordinary steps
leave stdin at `/dev/null` (so the non-TTY path is testable for free), a
seam-and-injected-reader unit idiom that `internal/tui/picker_test.go` already
uses to drive an ANSI key decoder with no terminal, and a real
`script -q -c` PTY harness at `test/functional/steps_init_bootstrap_test.go:143`
that two `@critical` scenarios already use. I built a probe of the proposed
capture and drove it through that harness: bracketed paste works end to end,
one Enter submits, a 205 KB multi-line payload round-trips byte-exact in 2.07
seconds, and terminal restoration is assertable by diffing `stty -g` before and
after — but a single pasted line over ~4090 bytes and any input lacking a
terminating gesture both hang, and the harness collapses stdout into stderr so
stream separation cannot be checked there. The recommendation is roughly 17
criteria at the unit layer, two or three `@critical` PTY scenarios, four small
harness fixes (an `\e` escape, a generated-payload step, a DocString argv
assertion, and a timeout on the PTY step), and an explicit manual-verification
line for DECSET-2004-less terminals, tmux, and rendering quality — which no
harness in this repo can reach.
