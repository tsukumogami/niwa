# Testability Review

**Verdict:** FAIL

Five criteria cannot fail as filed -- including the two that carry the size-ceiling
message and the non-blocking guarantee -- and eight requirements have no criterion
at all, so the set can pass green while the feature's headline behaviors are absent.

## Criteria Assessment

Verified against the actual harness, not only the Phase 2 research:
`test/functional/steps_init_bootstrap_test.go:143-199` (PTY step),
`test/functional/steps_test.go:144-168` (`runNiwa`, never sets `cmd.Stdin`),
`internal/cli/prompt.go:26` (`IsStdinTTY` is the only TTY seam; there is no
`IsStderrTTY`), `internal/cli/dispatch.go:131,142,144-145` (`ExactArgs(1)`,
the empty-prompt error, the current over-length message),
`internal/cli/dispatch_launcher.go:31-33` (launcher already rejects an empty
prompt), `internal/cli/dispatch_keepalive.go:33` (the reserve string).

### Level 1 -- unit tests over an injectable capture core

| # | Criterion | Assessment |
|---|---|---|
| 1 | Paste block accumulates; embedded newlines do not submit (R4) | Verifiable as filed. `picker_test.go` precedent. |
| 2 | One submit gesture returns text for a bare paste (R4, R5) | Verifiable as filed. |
| 3 | One submit returns paste + typed text, no mode change (R5) | Verifiable as filed, but under-specified -- see Issue 6. |
| 4 | Manual-newline gesture inserts, does not submit (R6) | Verifiable as filed. |
| 5 | Escape sequence split across two reads reassembles (R4) | Verifiable as filed. `chunkedReader` (`picker_test.go:203-217`) is the exact tool. |
| 6 | Exactly-at-ceiling accepted, one byte over refused (R14, R17) | Verifiable as filed; add a golden value so it is not tautological (Improvement 3). |
| 7 | Reserve counted against the ceiling (R14, R16) | Verifiable as filed. The 638-byte constant is real. |
| 8 | Refusal leaves the capture accepting input (R17) | Verifiable as filed. |
| 9 | Abandonment returns a sentinel distinct from EOF (R8) | Verifiable as filed. |
| 10 | Single line past the line-discipline buffer captured without hanging (R19) | **Needs a different level.** Not verifiable here at all -- see Issue 1. |
| 11 | Captured result and rendered prompt are separable (R10, R22) | Verifiable as filed, but it does not verify R22 -- see Issue 4. |

### Level 2 -- command-level tests over a capture seam

| # | Criterion | Assessment |
|---|---|---|
| 12 | No arg + interactive: capture invoked, text becomes final argv element (R1, R10) | Verifiable as filed. |
| 13 | No arg + non-interactive: errors naming the argument form, provisions nothing (R20) | Verifiable as filed, but leaves R20's "SHALL NOT read stdin" and all of R21 unchecked -- Issues 2 and 3. |
| 14 | Positional arg: capture never invoked, terminal never consulted (R2, R25) | Verifiable as filed. `stubTTY` (`init_bootstrap_test.go:82-87`) with a fatal-on-call variant. |
| 15 | Abandoned capture provisions nothing (R7, R26) | Verifiable as filed. `f.provisionCalled == 0` idiom. |
| 16 | `niwa dispatch ""` fails with the existing error (R12) | Verifiable as filed. |
| 17 | `--detach` composes both ways (R13) | Verifiable as filed. |
| 18 | The launcher entry point cannot reach the capture (R11) | **Not verifiable as written** -- see Issue 5. |

### Level 3 -- `@critical` functional scenarios

| # | Criterion | Assessment |
|---|---|---|
| 19 | Paste dispatches; worker argv contains the pasted text verbatim (R1, R4, R10) | Verifiable, but not writable today -- three harness changes are prerequisites the PRD never names (Improvement 1). |
| 20 | Over-ceiling refused with a message stating both sizes, no instance remains (R17, R18, R26) | **Cannot fail on the part that matters, and is a suite-hang trap** -- Issue 7. |
| 21 | Terminal mode after an abandoned capture matches before (R9) | Verifiable as filed (`stty -g` diff confirmed), but covers one of R9's five exit paths -- Issue 8. |
| 22 | No arg, no terminal, fails without blocking (R20) | **Cannot fail on the blocking half** -- Issue 2. |

### Verified manually before release

| # | Criterion | Assessment |
|---|---|---|
| 23 | Degraded terminal: multiline input not truncated at first newline (R23, R24) | **Both halves are automatable at Level 1** -- Issue 9. |
| 24 | Capture works inside a terminal multiplexer | Genuinely manual, but unfalsifiable as written -- no multiplexer named, no pass condition. |
| 25 | A large paste renders without visible corruption | Genuinely manual, but unfalsifiable -- "large" and "corruption" both undefined. |

### Requirements with no criterion at any level

R3 (two-or-more positional args remain an error), R9's signal clause and its
normal-submit clause, R15 (a single ceiling on every platform), R21 (the
stderr half of the interactivity gate), R22 (stdout redirectable), R24's second
half (the developer can see what was captured before anything is created),
R27 (the capture is evidently waiting), and the single-argv-element half of R10.

## Issues Found

1. **R19's criterion is filed at a level where it cannot fail, and the level it
   belongs at is the one the harness hangs on.** R19 is a property of the
   *terminal's* canonical-mode line discipline. An injectable core driven by
   `bytes.NewReader` has no line discipline, so a unit test feeding an 8 KB
   single line passes trivially and proves nothing. The Phase 2 measurement is
   unambiguous about where the failure actually lives: `ESC[200~` + 4090 bytes
   = 4096 hits the N_TTY canonical limit, the closing marker is discarded, and
   the reader waits forever -- *unless* the child reaches `term.MakeRaw` before
   the bytes arrive, in which case 12000 bytes on one line is fine. The PTY step
   sets `cmd.Stdin = strings.NewReader(rawInput)` (`steps_init_bootstrap_test.go:181`)
   and so loses that race every time.
   *Fix:* restate R19 as what it actually requires -- the capture disables
   canonical-mode line buffering before its first read, so no per-line length
   limit applies -- and move the criterion to Level 3 with a PTY step that
   delays the input feed. If the delayed-feed step is not built, say so and
   move R19 to manual verification. Do not leave it at Level 1.

2. **The non-blocking guarantee has no criterion that can fail.** Criterion 22
   says the command "fails without blocking," but `runNiwa`
   (`steps_test.go:144-168`) never sets `cmd.Stdin`, so `os/exec` hands the
   child `/dev/null`. An implementation that violates R20 by reading stdin gets
   an immediate EOF and exits anyway. The scenario passes either way. Given that
   "no scripted, hooked, or piped invocation blocks" is a stated goal and the
   Known Limitations section leans on it, this is the most consequential
   unfalsifiable criterion in the set.
   *Fix:* attach stdin to a pipe that is never written to and never closed, and
   assert the command exits within a few seconds. That distinguishes "did not
   read" from "read and got EOF." A Level 2 variant -- assert the reader seam
   was not invoked -- is cheaper and should exist too, but only the pipe version
   tests the guarantee.

3. **R21 has no criterion, and it is the requirement that needs one most.**
   R21 requires the gate to check *both* stdin and stderr. Criterion 13 says
   only "a non-interactive session," which does not distinguish the two. This
   matters concretely: `IsStdinTTY` (`prompt.go:26`) is the only TTY seam in the
   package; there is no `IsStderrTTY`. The stderr half is new code
   (`destroy.go:239` reaches it via `tui.IsAvailable()`, which checks
   `os.Stderr` at `picker.go:45`), and new code with no criterion is code that
   ships wrong.
   *Fix:* a four-row Level 2 table over (stdin TTY, stderr TTY), asserting
   capture runs only for (true, true) and the other three refuse.

4. **R22 is credited to a criterion that tests something else.** Criterion 11
   checks that the returned string differs from the bytes written to the
   writer. R22 says stdout stays redirectable without affecting capture
   behavior. Those are different claims, and the PTY harness genuinely cannot
   check the second one -- `steps_init_bootstrap_test.go:193` assigns
   `stdout.String() + stderr.String()` into `s.stderr`, collapsing the streams.
   Filing the proxy at Level 1 is the right instinct; labelling it R22 is what
   makes the coverage look complete when it is not.
   *Fix:* drop R22 from criterion 11's label, and add a Level 2 criterion:
   with both TTY checks stubbed true and a non-terminal stdout writer, the
   capture still runs and its rendering lands on the err writer.

5. **R11's criterion asserts a property of the call graph, which no test can
   fail.** "The launcher entry point cannot reach the capture" is a claim that
   no path exists -- unwritable as an assertion, and already true today:
   `realDispatchLaunch` takes the prompt as a parameter and rejects an empty
   one (`dispatch_launcher.go:31-33`). The PRD itself calls this constraint
   load-bearing because `niwa watch` calls the launcher directly and a
   cron-driven sweep must not get an interactive read. That is the thing worth
   testing, and it is testable.
   *Fix:* stub the reader seam with a `t.Fatal`-on-call and drive the `niwa
   watch` launcher path end-to-end, asserting the stub is never invoked. That
   fails the day someone moves capture into the launcher, which is the whole
   point.

6. **R5 does not say where the typed annotation lands, so criterion 3 passes on
   a broken result.** Phase 2 reproduced the behavior: with a paste that has no
   trailing newline, typed text concatenates onto the *last pasted line*
   (`"alpha\nbeta please fix this"`). For the motivating case -- a stack trace
   plus "I already ruled out the config loader" -- that runs the developer's
   note onto the final stack frame. R5 requires only that both are "sent
   together," which that satisfies.
   *Fix:* either state the intended placement in R5 and assert it, or accept
   the current behavior and file criterion 3 as a characterization test that
   pins it, so a future change is deliberate.

7. **The ceiling scenario is satisfied by the pre-feature error message, and it
   hangs.** Two independent problems in one criterion. First, today's message
   already states both sizes: `"dispatch prompt is too long (%d bytes, limit
   %d); shorten it rather than relying on truncation"` (`dispatch.go:145`). The
   criterion asks for "a message stating both sizes," which that passes -- while
   R18's actual content is that the message must *name a concrete alternative
   rather than instruct the developer to shorten the text*, i.e. precisely the
   clause the current message violates. The criterion cannot fail on the change
   R18 requires. Second, R17 says the refusal leaves the capture still
   accepting input, so a PTY scenario that pastes an oversized payload and
   stops has no terminating gesture -- and Phase 2 measured that
   `exec.CommandContext` in the PTY step carries godog's deadline-free context,
   so the scenario burns `go test`'s 10-minute default and panics the whole
   suite.
   *Fix:* assert the alternative-naming clause explicitly and assert the
   absence of "shorten it"; move the message-content check to Level 1 where the
   writer is a buffer; keep the functional scenario to "oversized paste, then
   abandon, exit non-zero, no instance remains" so it terminates. Add a
   `context.WithTimeout` to the PTY step regardless.

8. **R9 lists five exit paths and one is checked.** Criterion 21 covers
   abandonment. Normal submit, error, and SIGINT/SIGTERM/SIGHUP are unchecked.
   The signal clause needs particular attention: raw mode suppresses `ISIG`, so
   Ctrl-C arrives as byte `0x03` and never becomes a signal -- Phase 2 confirmed
   this. R9's SIGINT clause is therefore only reachable by an external `kill`,
   which is not what a reader assumes it means, and no criterion produces one.
   *Fix:* extend the verified `stty -g` before/after technique to normal submit
   (cheap, same step) and add one signal scenario that `kill -TERM`s the child.
   If a signal-sending step is out of scope, say in R9 that the signal clause
   is verified by inspection and note that keyboard interrupt arrives as a byte,
   not a signal.

9. **The first manual item parks two automatable behaviors.** R23 -- the command
   does not probe and does not warn -- is a pure assertion over the bytes
   written to an injected writer: no DECRQM query sequence, no warning text.
   That is Level 1 today. R24's core is also Level 1: a terminal lacking
   paste-boundary support simply delivers the pasted bytes with `\r` line
   endings and no `ESC[200~` markers, which is exactly what a fixture without
   the markers produces. Feed `line1\rline2\r` and assert the capture does not
   return after the first line. Phase 2's "no harness can produce a terminal
   that ignores `ESC[?2004h`" is true of the PTY layer and false of the
   injectable core, where you just omit the markers.
   *Fix:* move R23 and R24's no-truncation property to Level 1. What genuinely
   stays manual is the fidelity question -- whether a real DECSET-less terminal
   delivers the byte stream the fixture assumes -- which is a much smaller and
   more honest manual claim.

10. **Nothing checks that a pasted payload stays one argv element.** R10 says
    the captured text reaches the worker "as the same single argv element the
    positional path produces." Criterion 19 checks the argv *contains* the text.
    The property that matters for a feature whose entire purpose is pasting
    untrusted text full of quotes, backslashes, dollar signs, and newlines is
    that it does not split or interpolate. `dispatch_launcher_test.go:32-48`
    already tests exactly this for the positional path and is the ready-made
    model.
    *Fix:* add a Level 1 or Level 2 criterion feeding a metacharacter-laden
    multiline paste and asserting `reflect.DeepEqual` on the constructed argv.

11. **R3 and R15 have no criteria.** R3's two-or-more-args error is a one-line
    Level 2 test and the change is real -- `dispatch.go:131` is
    `cobra.ExactArgs(1)` today and must become `MaximumNArgs(1)`. R15's
    single-ceiling-everywhere is a code-shape requirement; a grep-style check
    that the constant has no platform-conditional definition is the honest form.
    *Fix:* add the R3 criterion at Level 2; either add R15's check or state that
    R15 is enforced by review.

12. **R27 has no criterion and is not specific enough to get one.** "Evident
    that the command is waiting for input" cannot fail a test. The behavior it
    is protecting -- a promptless invocation from a script that inherits a
    terminal is visibly stalled -- is named as a Known Limitation with R27 as its
    mitigation, so an unverifiable R27 leaves that limitation unmitigated in
    practice.
    *Fix:* restate as "the capture writes a prompt to stderr before its first
    read of stdin" and assert the writer is non-empty at that point.

## Suggested Improvements

1. **Name the harness prerequisites.** Three of the four functional criteria
   cannot be written against today's suite: `\e`/`\x1b` expansion at
   `steps_init_bootstrap_test.go:181` (only `\n` is expanded, so no feature file
   can emit a bracketed-paste marker), a generated-payload PTY step (a Gherkin
   quoted string cannot carry the ceiling payload), and a DocString variant of
   `theLaunchedClaudeWasInvokedWith` (`dispatch_steps_test.go:350-364` takes a
   single-line string, and the fake records argv as one line to
   `$HOME/dispatch-launch-argv`, which a multiline prompt breaks). A PRD does not
   normally specify test tooling, but criteria that are unwritable on the day the
   PRD is accepted should say so, or the Level 3 group reads as available when
   it is not.

2. **Add a step timeout as a stated prerequisite, not an aside.** The PTY step's
   `exec.CommandContext` uses godog's context, which carries no deadline. Every
   criterion in the Level 3 group is one malformed input away from a ten-minute
   CI hang that panics the suite. One `context.WithTimeout` converts the worst
   failure mode from a mystery hang into a step failure. Worth naming in the PRD
   because two of the four Level 3 criteria (R19's, if it moves there, and the
   ceiling one) are exactly the shapes that trigger it.

3. **Make the ceiling criterion non-tautological.** A test computing the expected
   ceiling from `maxArgStringBytes - dispatchPromptReserve` passes no matter what
   those constants are. Pair the boundary test with a golden assertion on the
   resulting number, so R14's stated purpose -- "a change to either term visibly
   moves it" -- is actually enforced rather than described.

4. **Resolve the macOS question rather than carrying it.** The PRD's first Open
   Question is load-bearing for this review: `iRunUnderPTYWithInput` errors
   rather than skips when `script -c` is unusable, and `-c` is a util-linux flag
   that BSD `script` does not have. Adding three PTY scenarios makes `make
   test-functional` a Linux-only command in practice. Recommendation: skip with
   a reason when `script` is not util-linux. CI is ubuntu-only
   (`.github/workflows/test.yml:28`) so coverage is unchanged there, and a
   developer on a Mac gets a working suite instead of a red one.

5. **Answer the third Open Question in the PRD, since it decides a criterion's
   strength.** Whether the ceiling error is asserted by exact substring or loose
   match determines whether Issue 7's first half is fixed. House style
   (`init_bootstrap_failures.feature:18`) favors exact substrings, and an exact
   assertion is what catches the "shorten it" wording R18 forbids.

6. **Give the manual list named terminals and pass conditions.** The PRD already
   concedes in Open Questions that without a named set the manual criteria are
   unfalsifiable. Naming three (one multiplexer, one modern emulator, one
   without bracketed paste) plus a stated pass condition per item costs a
   sentence and converts three decorative lines into a release checklist.

## Summary

The four-level structure is the right shape and most criteria sit at the right
level, but the set is not yet strong enough to gate the feature. Five criteria
cannot fail as filed -- R19's at the unit layer where no line discipline exists,
the non-blocking one against a `/dev/null` stdin, the ceiling message against an
error string that already satisfies it, R11's against a launcher that already
has no read path, and R22's against a criterion measuring something else -- and
eight requirements including R3, R15, R21, R27 and R9's signal clause have no
criterion at all. The manual list is honest about the genuinely unreachable
things (DECSET-less terminals, multiplexers, render quality) but bundles R23 and
R24's core with them, and both are straightforwardly automatable at the unit
layer once you see that omitting the bracketed-paste markers from a fixture
*is* the degraded terminal.
