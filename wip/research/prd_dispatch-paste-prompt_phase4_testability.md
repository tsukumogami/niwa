# Testability Review

**Verdict:** FAIL

The revision fixed most of what the last round found, but three criteria still
pass against implementations that violate their requirement, one is filed at a
level whose harness cannot make it fail, R17 and R31 contradict each other so
one criterion cannot be authored at all, and five of the six `@critical`
scenarios depend on harness changes the PRD does not require.

## What the revision got right

Before the objections, four things are worth recording, because they were the
previous round's findings and they are genuinely closed:

- **Stream separation is filed at the right level.** R22's criterion ("its
  rendering goes to the error stream") sits at the command layer, not in a
  Gherkin scenario. That is correct and non-obvious: `iRunUnderPTYWithInput`
  writes `stdout.String()` into both `s.stdout` and `s.stderr`
  (`test/functional/steps_init_bootstrap_test.go:193`), so a functional
  scenario asserting "the prompt went to stderr" would pass no matter which
  stream the implementation used. The PRD does not make that mistake.
- **The non-TTY criterion no longer passes trivially.** `runNiwa` never sets
  `cmd.Stdin` (`test/functional/steps_test.go:149-154`), so Go hands the child
  `/dev/null` and any "does not block" scenario using the ordinary `I run
  "..."` step succeeds against an implementation that reads stdin — it just
  gets instant EOF. Specifying a never-written, never-closed pipe removes that.
  See Issue 8 for what is still missing around it.
- **The R14 boundary is stated as a derivation with the computed value
  alongside it,** which is what makes a boundary test meaningful at all. The
  arithmetic checks out: `keepAliveArmingInstruction` measures 638 bytes
  (`internal/cli/dispatch_keepalive.go:33`, measured, not read off the
  research), and 131071 − 638 = 130433, consistent with `maxArgStringBytes`
  meaning the largest usable argv string rather than the raw 32-page figure.
- **The SIGINT-as-a-byte fact is stated in the document** rather than papered
  over with a signal test that could never fire. Raw mode suppresses `ISIG`;
  the PRD says so in R9's inspection note and in Known Limitations.

## Criteria Assessment

### Unit tests over an injectable capture core

| Criterion (R) | Assessment |
|---|---|
| Split across reads (R4) | Verifiable as filed. `chunkedReader` (`internal/tui/picker_test.go:203-217`) is the existing idiom. "an arbitrary boundary" should be "every boundary" — a table over split points — but that is wording, not level. |
| Embedded newlines, no delimiters (R4, R23) | **Decorative.** See Issue 1. |
| One submit gesture, bare paste (R4) | Verifiable as filed. |
| Paste plus typed text, boundary preserved (R5) | Verifiable but under-specified. See Issue 6. |
| Manual-newline gesture (R6) | Verifiable as filed. |
| End-of-input on non-empty / empty buffer (R28) | Verifiable as filed. Discriminating: the naive implementation returns the same `io.EOF` in both cases. |
| Abandonment sentinel (R8) | Verifiable as filed, and genuinely discriminating — the Phase 2 probe measured `x/term` collapsing `0x03` into `io.EOF`, so an implementation that does not intercept the byte fails. |
| Exactly the ceiling accepted, one over refused (R14) | **Decorative as filed.** See Issue 3. |
| The reserve counts against the ceiling (R14, R16) | **Wrong level.** See Issue 4. |
| After a refusal, text retained (R17) | **Decorative.** See Issue 2. |
| Refusal message contents (R18) | Verifiable as filed. Byte counts and the absence of "shorten" are mechanical. Note the current message at `internal/cli/dispatch.go:145` contains "shorten it rather than relying on truncation", so this criterion has real work to do. |
| Control sequences preserved, render sanitized (R30) | Verifiable, but stricter than its requirement. See Issue 7. |
| No capability query, no capability warning (R23) | Verifiable but weak — "capability query sequence" is not defined, so the test asserts absence of whichever sequences the author thinks of. Naming them (DA1, DECRQM `ESC[?2004$p`) makes it mechanical. |
| Visible waiting indication before first read (R27) | **Decorative.** See Issue 5. |

### Command-level unit tests over a capture seam

| Criterion (R) | Assessment |
|---|---|
| Capture invoked, text becomes final argv element (R1, R10) | Verifiable as filed; matches `installDispatchFakes` (`internal/cli/dispatch_test.go:85-159`). |
| Quotes, backslashes, dollar signs byte-for-byte in one argv element (R10) | Verifiable as filed; `dispatch_launcher_test.go:32-48` is the existing model. |
| Four combinations of (stdin TTY, stderr TTY) (R20, R21) | Verifiable, but presumes a stderr-TTY seam that does not exist: `IsStdinTTY` is a `var` (`internal/cli/prompt.go:26`) but `tui.IsAvailable` is a plain `func` (`internal/tui/picker.go:44`). DESIGN can make it a var; worth naming so it does not get missed. |
| Non-terminal stdout, capture still runs, rendering to stderr (R22) | Verifiable as filed, and correctly placed here rather than in a scenario. |
| Positional argument: seam never invoked, terminal never consulted (R2) | Verifiable as filed, given a counting stub on the TTY check. |
| Two or more positional args error (R3) | Verifiable as filed. |
| Abandoned capture provisions nothing (R7, R26) | Verifiable as filed (`f.provisionCalled == 0`, the idiom at `dispatch_test.go:434`). |
| Empty/whitespace submission (R29) | Verifiable as filed. |
| `niwa dispatch ""` (R12) | Verifiable as filed. |
| `--detach` composition (R13) | Verifiable as filed via `attachCalled`. |
| `niwa watch` launcher path never invokes the stub (R11) | Verifiable but near-tautological: it fails only for the single wrong implementation that puts capture inside `dispatchLaunch`. That is the mistake the Decisions section names, so it earns its place — but it does not cover a future `watch` that starts calling `runDispatch`. |
| Four refusals exit non-zero (R31) | **Unauthorable.** See Issue 9 — R31 and R17 disagree about whether the oversized case exits at all. |
| Positional path matches pre-change baseline (R25) | Verifiable only if the goldens are recorded first. See Issue 10. |

### `@critical` functional scenarios

| Criterion (R) | Assessment |
|---|---|
| Pasted multiline block reaches the worker's argv (R1, R4, R10) | Buildable only with two unstated harness changes — control-byte escapes and a DocString argv assertion. See Issues 11 and 12. |
| Never-written, never-closed pipe exits within a bounded time (R20) | Buildable, and it terminates for a correct implementation, but needs a new step and its own deadline. See Issue 8. |
| Terminal mode after abandonment matches before (R9) | Buildable only with a step variant that drops the hardcoded `exec`. See Issue 13. |
| Terminal mode after submit matches before (R9) | Same. |
| Oversized paste then abandonment (R17, R26) | Needs a generated-payload step; 130 KB does not fit in a quoted Gherkin string. Unstated. |
| Single over-long line fed after the capture has started (R19) | **Not buildable as filed.** See Issue 14. |

### Verified by inspection

| Criterion (R) | Assessment |
|---|---|
| Single derivation, no platform-conditional definition (R15) | Honest use of inspection. |
| No documentation implies the argument is mandatory (R32) | **Partly parked.** See Issue 15. |
| Terminal-driven step carries a bounded timeout (R33) | Honest category, but a 10-minute timeout satisfies it. See Issue 16. |
| Restoration covers SIGTERM and SIGHUP (R9) | **Weak.** See Issue 17. |

### Verified manually before release

Multiplexer behavior and large-paste rendering are correctly manual — no
harness in this repo can produce a terminal that ignores `ESC[?2004h`, and the
PRD says so in Known Limitations. The third item ("each terminal named in the
supported set (see Open Questions)") is explicitly unfalsifiable while that
question is open; see Issue 18.

## Issues Found

1. **The embedded-newline criterion does not name the byte, and CR versus LF is
   exactly where R4 breaks.** In raw mode `\r` (0x0D) and `\n` (0x0A) are
   distinct bytes, and which one an unbracketed paste delivers is
   terminal-dependent — which is precisely the condition R4 promises to survive
   and R23 forbids detecting. A test that feeds only `\n` passes an
   implementation whose submit gesture is bare `\r`, and that implementation
   truncates a multi-line paste at its first line on any terminal without
   DECSET 2004. Passing-but-violating implementation: submit on `0x0D`, treat
   `0x0A` as literal text. *Fix:* the criterion must feed both bytes — a paste
   whose line breaks are LF and a paste whose line breaks are CR — and require
   that neither returns after the first line. If DESIGN wants bare Enter as the
   submit gesture, that tension has to surface here rather than in the field.

2. **R17's retention criterion passes while losing the paste.** "the previously
   entered text is retained" leaves unstated whether "previously entered" means
   the buffer before the overflowing chunk or the whole buffer including it.
   Passing-but-violating implementation: on overflow, discard the entire
   oversized paste and leave the buffer as it was — for the central use case
   (the paste *is* the whole input) "the previously entered text" was empty, so
   the criterion holds while the developer loses exactly what R17 exists to
   save. *Fix:* state the rule in the requirement (I would keep the whole
   buffer including the overflow, over-limit, and let the developer delete —
   that is what "reduce it rather than lose it" reads as), then write the
   criterion over a concrete sequence: type A, paste B where A+B exceeds the
   ceiling, assert the buffer afterwards and that a subsequent submit of a
   reduced buffer succeeds.

3. **The ceiling boundary test can be written so it cannot fail.** If the test
   computes its expected limit from the same constant the implementation uses
   (`feed(limit)` accepted, `feed(limit+1)` refused), it passes for any value of
   that constant — including a mis-set one, which is the defect issue #225
   exists to fix. *Fix:* split into two criteria — one asserting the constant
   equals 130,433 on the current baseline (an independently stated number, so
   changing either derivation term breaks the test and forces a visible
   update), and one asserting accept/refuse either side of it.

4. **The reserve criterion is filed at a level whose harness cannot make it
   fail.** It sits under "unit tests over an injectable capture core", where the
   core's signature takes the limit as a parameter (`readPastePrompt(in, out,
   limit)` is the shape the criteria imply). At that level the reserve
   arithmetic belongs to the caller and the test injects whatever limit it
   likes. Passing-but-violating implementation: the command layer passes
   `maxArgStringBytes` with no reserve subtracted; the core test still passes,
   and the prompt still dies at exec after provisioning. *Fix:* move it to the
   command level, asserting that a prompt sized between ceiling and
   ceiling+reserve is refused before `provisionCalled` increments — an extension
   of the existing `TestDispatch_OverLongPrompt_Errors`
   (`internal/cli/dispatch_test.go:424-437`).

5. **"A visible waiting indication" is satisfied by invisible bytes.**
   Passing-but-violating implementation: write `ESC[?2004h` before the first
   read and nothing else. Bytes were written to the render target before the
   read, the criterion holds, and the developer stares at a blank terminal —
   which is the exact failure R27 exists to prevent (the Known Limitations
   section leans on R27 as the mitigation for a script that inherits a
   terminal). *Fix:* assert that the bytes written before the first read
   contain non-empty human-readable text once escape sequences are stripped.

6. **R5's "boundary preserved" is not specific enough to assert.** The criterion
   says the typed text must not be joined onto an unterminated final pasted
   line, but not what separates them. Two implementations — insert one `\n`,
   insert `\n\n` — both satisfy it and produce different payloads, and R30
   ("preserved exactly") plus R10 ("byte-for-byte in the worker's argv") make
   the payload load-bearing. *Fix:* state the separator in R5 and write the
   criterion as an exact string comparison.

7. **R30's criterion is stricter than R30.** The requirement says rendering
   *MAY* sanitize control sequences; the criterion says the render target's
   bytes *are* sanitized. An implementation that echoes raw satisfies R30 and
   fails the criterion. *Fix:* pick one. Given the PRD's own argument in
   Decisions ("echoing them raw is a display-corruption path"), promote R30 to
   SHALL sanitize the render and keep the criterion as written.

8. **The never-written-never-closed-pipe scenario needs a step that does not
   exist, and a deadline R33 does not cover.** `runNiwa` never assigns
   `cmd.Stdin` (`test/functional/steps_test.go:149-154`), so a new step must
   create an `os.Pipe`, hand the read end to the child, and hold the write end
   open in the parent for the process's lifetime. That is buildable and it does
   terminate for a correct implementation — a pipe is not a terminal, so R20's
   gate fires immediately and the command exits without reading. But a
   *violating* implementation blocks forever, and R33's timeout is scoped to
   "its terminal-driven step" — this scenario is not terminal-driven. *Fix:*
   widen R33 to every step that hands the binary a stdin it does not control,
   and name the pipe step among the harness changes.

9. **R31 and R17 contradict each other, so the R31 criterion cannot be
   authored.** R31 lists "the oversized refusal (R17)" among the paths that
   SHALL exit non-zero with the ordinary error status. R17 says the capture
   SHALL remain open after input crosses the ceiling. Both cannot hold: either
   crossing the ceiling terminates the command or it does not. *Fix:* drop the
   oversized refusal from R31's list and say that the oversize path exits only
   via whatever the developer does next (submit a reduced buffer, or abandon —
   already covered by the abandonment entry and by the functional criterion
   "oversized paste followed by abandonment exits non-zero").

10. **The R25 baseline criterion is unfalsifiable unless the goldens are
    recorded first.** "match the pre-change baseline" names a comparison target
    that stops existing the moment the change lands. Compounding it: D1 defines
    the baseline as post-#225, and #225 has not landed — neither
    `maxArgStringBytes` nor `dispatchPromptReserve` exists in `internal/cli`
    today, and the current message at `dispatch.go:145` still says "shorten it",
    which R18 forbids. *Fix:* state that the criterion is a characterization
    test whose expected values are captured from the post-#225 binary before
    capture work begins. Also worth resolving deliberately: R18 forbids
    "shorten" in the capture's refusal while R25 freezes the positional path's
    message, which leaves the same overflow condition giving different guidance
    depending on how the prompt arrived.

11. **No control byte can be expressed from a feature file, and every
    `@critical` scenario needs at least one.** The PTY step expands only `\n`
    (`test/functional/steps_init_bootstrap_test.go:181`). Bracketed-paste
    markers need `ESC`; abandonment needs `0x03`; a Ctrl-D or Ctrl-J gesture
    needs `0x04`/`0x0A`. Without this the paste scenario cannot even be typed
    into the file — and worse, a scenario that omits its submit gesture hangs,
    because `script` does not propagate stdin EOF into the pty (measured in
    Phase 2 at `rc=124` after 20s). *Fix:* R33 should name escape expansion
    (`\e`, `\xNN`) alongside the timeout.

12. **The verbatim-argv assertion needs a DocString step.**
    `theLaunchedClaudeWasInvokedWith` is registered as `"([^"]*)"`
    (`test/functional/dispatch_steps_test.go:369`) — single-line only, so a
    multi-line captured prompt cannot be asserted verbatim. The data is
    available (the fake writes the full argv line to
    `$HOME/dispatch-launch-argv`), only the step shape is missing. Unstated in
    R33.

13. **The two terminal-mode scenarios need a step variant that drops `exec`.**
    The step hardcodes `cd <root> && exec <binary> ...`
    (`steps_init_bootstrap_test.go:170`), which leaves no room for the
    `stty -g` before/after compound command the Phase 2 research verified. A
    sibling step without the `exec` is the fix; R33 does not mention it.

14. **R19's criterion is not buildable in this harness as filed, and the PRD
    does not say what it needs.** The step assigns `cmd.Stdin =
    strings.NewReader(rawInput)` (`:182`) — a pre-filled reader handed to
    `script` before the child has reached `MakeRaw`. There is no way to feed
    "after the capture has started". Phase 2 measured the consequence directly:
    a 4090-byte pasted line hangs without a delay and succeeds with one. Three
    problems follow. (a) A new step is required and R33 names only a timeout.
    (b) A sleep-based delay is a race, so the scenario is flaky by
    construction. (c) The workable line length sits in a narrow measured band —
    12,000 bytes completed, 20,000 did not finish in 20 seconds — so the
    scenario is tuned to the machine it was measured on. *Fix:* I would move
    R19 off the functional level entirely: assert at the unit level that the
    raw-mode switch is issued before the first read (observable through the
    injected writer or a recording terminal-mode seam), and keep a single
    non-racy PTY scenario for "raw mode actually engages" — the thing the unit
    layer structurally cannot reach, because a `bytes.Buffer` render target
    skips `MakeRaw` the way `internal/tui/picker.go:79` already does. If the
    functional criterion stays, R33 must require the delayed-feed step and the
    PRD must name the line length.

15. **R32 parks automatable content in inspection.** The command's usage string
    and long help are Go strings on the cobra command; asserting that `Use`
    describes the argument as optional and that the long help does not call it
    required is a three-line unit test. Only the README genuinely needs a
    human. *Fix:* split the criterion — usage and long help at the unit level,
    README by inspection.

16. **R33's own criterion is satisfied by a timeout that defeats it.** "carries
    a bounded timeout" holds for a 9-minute-59-second bound, which still lets a
    hung scenario consume `go test`'s default 10-minute deadline and panic the
    suite — the failure mode R33 exists to prevent. *Fix:* name the bound in
    R33 (30 seconds is comfortably above the 2.07s measured for a 205 KB
    payload).

17. **The R9 signal criterion inspects for handlers, not for restoration.**
    Passing-but-violating implementation: a SIGTERM handler that logs and calls
    `os.Exit` without restoring the termios state — handlers exist, inspection
    passes, the terminal is left in raw mode. *Fix:* at minimum, state what the
    inspector checks (that the restore executes on the signal path before the
    process exits, with no exit path around it). Better, promote it: Phase 2
    verified that diffing `stty -g` before and after under `script` works, so a
    scenario that signals the child is buildable, if fiddly, given the
    `exec`-free step variant from Issue 13.

18. **One manual criterion is unfalsifiable by its own admission.** "each
    terminal named in the supported set (see Open Questions)" points at a
    question the PRD leaves open. It cannot ship in that state. *Fix:* close
    the question before the PRD leaves Draft, or delete the criterion — the
    PRD's own Known Limitations already concedes these are "checked at release
    time by a person or not at all", and an unnamed set means the second.

19. **No requirement obliges DESIGN to provide the seams 27 criteria assume.**
    Fourteen criteria are filed "over an injectable capture core" and thirteen
    "over a capture seam", but nothing in the Requirements section says the
    capture must be exercisable without a terminal. DESIGN is explicitly told it
    owns "the capture mechanism, the choice of terminal API, and where the
    reader lives", so a design that reads `os.Stdin` directly satisfies every
    requirement and makes most of the acceptance criteria unwritable. *Fix:* add
    a requirement that the capture core is drivable from an injected reader and
    writer with no terminal present, and that the command-level entry into it is
    a swappable seam. The repo idiom for both already exists
    (`internal/tui/picker.go:64-75`, `internal/cli/dispatch.go:93`).

20. **Minor: the requirement numbering skips R24.** R23 is followed by R25 and
    no R24 appears anywhere in the document. Either a requirement was dropped
    during revision or it is a renumbering artifact; worth confirming nothing
    was lost.

## Would the feature work if every criterion passed?

No, and the gap is the feature's central promise. R4 states unconditionally
that an embedded newline never terminates the capture, "regardless of whether
the terminal delimits pasted blocks", and R23 forbids the implementation from
finding out whether the terminal does. Every criterion that exercises R4 either
feeds bytes the test author chose at the unit level (Issue 1: the author will
reach for `\n`) or drives a PTY where bracketed paste demonstrably works. The
unbracketed real-terminal case — the one R4 was written for — is checked
nowhere, and the manual criterion that would catch it names no terminals
(Issue 18). An implementation that submits on bare `\r` passes the entire
criteria set and truncates a developer's paste to its first line on any
terminal without DECSET 2004.

The second, smaller gap: nothing verifies that the capture path leaves the
developer attached to a working session. The `--detach` criterion checks
`attachCalled` against a fake, which is the right level for flag composition,
but the goal statement ("lands them attached to a session already working on
it") has no end-to-end check. Given the existing `@critical` dispatch scenarios
cover attach on the positional path, this is acceptable — worth noting, not
worth blocking on.

## Suggested Improvements

1. **Give the harness changes their own requirement block rather than one
   line.** R33 currently names a timeout; the criteria as written need five
   things: the timeout, control-byte escape expansion, a generated-payload
   step, a DocString argv assertion, an `exec`-free step variant, and a held-open
   stdin pipe step. Listing them makes the functional criteria buildable and
   makes their cost visible — six harness changes for six scenarios is a real
   number a reader should see before agreeing to it.

2. **Cut the `@critical` set to what the harness carries without a race.** The
   repo's rule (CLAUDE.md, `docs/guides/functional-testing.md:12`) demands a
   `@critical` scenario for a user-facing command; it demands one, not six.
   Paste-reaches-the-worker plus terminal-restored-after-abandonment are the two
   that only a real pty can produce. The pipe scenario is worth keeping because
   it is cheap and guards a hard requirement. R19's belongs at the unit level
   (Issue 14), and the oversize scenario is largely duplicated by the
   command-level refusal test.

3. **Name the Linux-only cost in the document, not just in Open Questions.**
   `iRunUnderPTYWithInput` errors rather than skips when `script` is not
   util-linux (`steps_init_bootstrap_test.go:148-150`), and `-c` is a util-linux
   flag. CI is ubuntu-only, so every PTY scenario added widens the gap between
   "passes in CI" and "passes on a contributor's Mac". Suggestion 2 keeps that
   gap at three scenarios instead of six, which may be enough to make the open
   question moot.

4. **State the retention rule and the paste/typed separator as requirements,
   not as criteria.** Issues 2 and 6 are both cases where the criterion is
   trying to carry a decision the requirement did not make. Criteria should
   assert; requirements should decide.

## Summary

The revision closed the previous round's structural problems — stream
separation is filed where it can fail, the non-TTY criterion no longer passes
against a stdin-reading implementation, and the ceiling is a derivation with a
checkable value — but five criteria still hold against implementations that
violate their requirement (the waiting indication, the retention rule, the
embedded-newline byte, the ceiling boundary, and the SIGTERM restoration
inspection), the reserve criterion sits at a level where the limit is an
injected parameter, and R17 and R31 disagree about whether crossing the ceiling
exits the command at all. Separately, five of the six `@critical` scenarios
rest on harness changes the PRD does not require: R33 names a timeout, while the
criteria need control-byte escapes, a generated payload, a DocString argv
assertion, an `exec`-free variant, and a held-open stdin pipe — and R19's
"fed after the capture has started" is not expressible at all against a step
that hands `script` a pre-filled `strings.NewReader`. Fixing the five
decorative criteria, resolving R17 against R31, moving the reserve and R19 to
levels that can fail them, and adding a requirement that the capture be
drivable without a terminal would make this pass.
