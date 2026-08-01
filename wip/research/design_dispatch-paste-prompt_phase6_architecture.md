# Architecture Review

**Verdict:** FAIL

The chosen structure is sound and the hard parts (reader surface, placement, signal lifecycle) are well argued, but four requirements the PRD states explicitly have no mechanism in the design, and one of them is the line-break rule both decision reports told the design to settle and it did not.

## Requirement Coverage

| Req | Mechanism in the design |
|---|---|
| R1 | Arity-selected branch in `runDispatch` + capture seam |
| R2 | Gate evaluated only in the zero-argument branch (stated twice) |
| R3 | "The arity change" (Implementation Approach 4); `cobra.ExactArgs(1)` -> at most one |
| R4 | Hand-rolled bracketed-paste parser; bytes inside a block are literal |
| R5 | **MISSING.** The single line feed at an unterminated paste boundary appears nowhere -- not in the gesture table, not in the data flow, not in the enumerated cross-chunk state. Both decision reports carry it as `pendingBreak`; the design dropped it. |
| R6 | Gesture table: `0x0A`, plus the passive `0x1B 0x0D` / CSI-u encodings |
| R7 | Capture sited before the first instance is created |
| R8 | `ErrCanceled` distinct from `ErrEndOfInput` and from a nil-error return |
| R9 | Function-scoped defer + handler over SIGINT/TERM/HUP/TSTP/CONT |
| R10 | Payload buffer separate from transcript; "grows and truncates only at its end". **Undercut by the CR-collapse rule -- see Blocking 1.** |
| R11 | Capture in `runDispatch`, not in `dispatchLaunch`; verified: `internal/cli/watch.go:579` and `:826` call `dispatchLaunch` directly, so the seam is structurally out of reach. Reachability guard test named. |
| R12 | Implicit in the arity branch (`""` is one argument) |
| R13 | Not mentioned. Structurally free -- capture completes before the attach decision. |
| R14 | `limit` derived in `internal/cli` as argument maximum minus reserve, passed to the core |
| R15 | Not addressed; nothing in the design introduces a platform conditional |
| R16 | "Ceiling enforcement on the capture path" (Implementation Approach 4) |
| R17 | **MISSING.** The design never says what the core does when the buffer crosses `limit`: not that the capture stays open, not that the crossing input is retained, not that submit is refused while above. `limit` is a parameter with no stated semantics. |
| R18 | **MISSING.** No refusal message, no statement of its content. |
| R19 | Hand-rolled reader over raw mode; no per-line limit by construction |
| R20 | Interactivity gate refuses without reading |
| R21 | Gate requires stdin and stderr; `IsStderrTTY` added beside `IsStdinTTY` |
| R22 | Rendering targets stderr only; stdout untouched by the capture. Covered. |
| R23 | No probe in the design; passive-only recognition of the CSI-u newline encodings |
| R25 | Positional path untouched by the arity branch |
| R26 | Capture sited after all preflight checks, before provisioning |
| R27 | Only implied ("the prompt that says how large the input is", Security Considerations). No pre-first-read banner is specified. |
| R28 | Gesture table rows for `0x04`/end-of-input against empty and non-empty buffers |
| R29 | **MISSING.** Nothing about post-capture validation. Note the existing check is `prompt == ""` (`internal/cli/dispatch.go:141`), which does not catch whitespace-only, and it runs at step (1) -- before the capture would occur. |
| R30 | Payload and transcript are separate outputs; neutralizer on the transcript side |
| R31 | Not addressed. See Non-Blocking 2. |
| R32 | Implementation Approach 6 |
| R33 | Implementation Approach 1 (chunked feed, held-open pipe, bounded timeouts) |
| R34 | **PARTIAL.** The derivation half is present. The re-check of the final argument immediately before the worker starts is absent -- and it is live: issue #225 has not landed (`maxPromptBytes = 128 * 1024`, no reserve, and `keepAliveArmingInstruction` is prepended at step (9d), after validation and after provisioning). |
| R35 | Append-only transcript rendered as input is accepted |
| R36 | Gesture table: delete rune / word / line |
| R37 | 1.16 ms for the 130,433-byte single line; transcript cost bounded by terminal width, not payload size |
| R38 | SIGCONT branch restores the held raw state |
| R39 | ISIG cleared so `0x03` is a byte; SIGINT handler as the second arm |
| R40 | Stated in Decision Outcome and in Consequences; pinned by a characterization test |

## Blocking Issues

1. **The CR/LF rule is unstated, and as written the design contradicts itself and R10.** The data-flow section names "a carriage-return flag, so that a `0x0D` at the end of one chunk and a `0x0A` at the start of the next collapse to a single newline" as one of the two risky cross-chunk states. But the same section says "Inside a paste block bytes append to the payload untouched," and the gesture table says `0x0D` outside a paste *submits*. So the collapse rule has nowhere to live: inside a paste it is normalization that R10 forbids ("no other trimming, normalization, or re-encoding"), and outside a paste the CR has already returned before any LF can arrive. Decision 1's sketch resolves it by normalizing CR and CRLF to LF *inside* the paste (`wip/design_dispatch-paste-prompt_decision_1_report.md:398-405`, the `lastWasCR` branch), which is the R10-violating reading. Decision 2 flagged this as the open cross-decision issue and said explicitly that "the design doc must state one rule for both and say plainly that R4 governs line breaks while R10 governs everything else -- or the PRD's R10 exception list needs amending" (`decision_2_report.md:378-390`, restated at `:451`). The design carries the flag forward without stating the rule. An implementer cannot build this: Terminal.app pastes CR-only line breaks, so the choice decides whether a normal multiline paste reaches the worker as `\r`-separated (byte-exact, unreadable downstream) or `\n`-separated (readable, and a documented R10 exception). Either answer is defensible; the design has to pick one, and if it picks normalization R10's exception list needs the amendment decision 2 named.

2. **R5's paste-boundary line feed has no mechanism, and it is a third piece of cross-chunk state the design's enumeration misses.** R5 requires that when a delimited block's final line is unterminated and typed text follows, exactly one line feed is inserted -- and the acceptance criterion compares the returned string exactly. This cannot be done at the end-of-paste marker unconditionally (a bare paste would gain a trailing newline and fail R10); it has to be a deferred obligation set at `ESC[201~` and flushed before the next appended byte. That is state that survives read boundaries just as much as the marker prefix and the CR flag, and the design says "the loop maintains two pieces of cross-chunk state." Both decision reports carry it as `pendingBreak`; the design lost it in synthesis. As written, an implementer building from this document produces a capture that fails R5 and the exact-comparison criterion.

3. **R17 and R18 have no mechanism.** The design gives the core a `limit` parameter and then never states what crossing it does. R17 is specific and was the subject of a PRD revision: the input is refused at the moment it crosses, the capture stays open, the buffer retains everything including the input that crossed, and a buffer above the ceiling is not submittable. R18 constrains the message (both byte counts, direct to a file-and-reference approach, must not say "shorten"). None of this appears. It is also the answer to how a single-return-value core survives a refusal -- the refusal is in-loop state, never an outcome -- which is exactly the thing a reader of the Core Interface section would want stated. Related and equally absent: R29's whitespace-only refusal. The existing empty check is `prompt == ""` at step (1), which runs before the capture and does not catch whitespace, so the capture path needs its own post-capture validation the design never places.

4. **R34's pre-exec re-check is missing, and the dependency is live.** D1 says shipping capture on the uncorrected cap is not an option, and R34 says that if #225 has not landed this work establishes the baseline itself: validate against the derived ceiling before any instance is created, *and* re-check the final argument immediately before the worker process starts. #225 has not landed -- `internal/cli/dispatch.go:81` still has `maxPromptBytes = 128 * 1024` with no reserve, its message still says "shorten it", and `keepAliveArmingInstruction` is prepended at step (9d) after validation and after provisioning, which is the exact defect R34 exists to close. The design covers the derivation and "ceiling enforcement on the capture path" but places no check before `dispatchLaunch`, so a keep-alive-armed dispatch at the ceiling still dies at exec after an instance exists.

## Non-Blocking Observations

1. **`ctx` is decorative as specified.** The core blocks in `stdin.Read`; a context cancellation cannot interrupt a blocking read on a raw-mode tty without a reader goroutine, which the design does not describe and which would bring its own concurrent-read and goroutine-leak questions. Nothing in the PRD requires context cancellation of the capture -- R20/R33's non-blocking guarantee is discharged by the TTY gate, not by a timeout. Either drop the parameter or state that it is carried for signature symmetry with the rest of the command path. Note also that `Pick` (the idiom being mirrored) takes no context, so the parameter is a divergence from the claimed model, not part of it.

2. **A real SIGINT does not produce `ErrCanceled`, and its exit status is not the ordinary error status.** The handler restores and re-raises, so the process dies at 128+N; the in-band `0x03` path is the one that returns `ErrCanceled`. That is consistent with R39 (which scopes itself to R7 and R9) but sits against R31's "abandonment exits non-zero with the command's ordinary error exit status" if a reader takes signal-driven abandonment to be abandonment. Worth one sentence.

3. **Ctrl-Z cannot reach the SIGTSTP handler.** With ISIG cleared, `0x1A` is a payload byte -- which is the point -- so the only path to SIGTSTP is an external `kill -TSTP`. Decision 1 noted this (`decision_1_report.md:507-509`). The suspend/resume criterion therefore has to be driven by an external signal, not by a keypress, and the plan should say so or the scenario will be written against a gesture that does not exist. The SIGTSTP reasoning itself holds: `signal.Reset` provably leaves Go's handler installed for `_SigDefault` signals, the SIGSTOP substitution genuinely stops the process, `fg` sends SIGCONT either way, and restoring the *held raw state* on SIGCONT (rather than calling `MakeRaw` again, as decision 1's sketch does) is the right call because it avoids clobbering the saved pre-capture state.

4. **Suspend mid-paste is not addressed.** An external `kill -TSTP` while a paste is in flight restores the terminal to cooked mode with the remainder of the paste still arriving; those bytes go through the line discipline, and the `ESC[201~` terminator may be consumed or echoed before SIGCONT re-arms. Inherent rather than fixable, but it belongs in Consequences beside the SIGKILL/external-SIGSTOP case.

5. **A marker-like sequence inside a pasted payload is outside the threat model.** Not every terminal filters `ESC[201~` out of pasted content. A crafted payload containing it ends the paste block early, after which a following `\r` is a submit gesture -- a payload-controlled premature dispatch. The blast radius is a truncated prompt, not code execution (the argv guarantee holds), but the Security Considerations section reasons only about display and process control and should name this third case.

6. **The held-back prefix needs to be an accumulator, not a one-boundary carry.** At chunk size one a six-byte marker spans six chunks. The abandoned-prefix path also matters: `ESC [ 2 0 0 X` must replay the held bytes through the gesture table, and one of them is `ESC`, which the gesture table gives meaning to as the first byte of `0x1B 0x0D`. So there are two multi-byte lookahead classes sharing one carry, not one. Testing at chunk size one will catch it; the design should say the carry is a bounded accumulator so it is built that way the first time.

7. **Rune-aware deletion over a byte buffer that may hold invalid UTF-8 is unspecified.** "Remove the last rune" meets a buffer whose fidelity guarantee explicitly admits invalid UTF-8 and NUL. `utf8.DecodeLastRune` returning `(RuneError, 1)` gives the right byte-wise fallback, but the design should say so; an implementer reaching for `[]rune(string(buf))` would silently violate R10 on the deletion path.

8. **`SetBracketedPasteMode` is a method on `*term.Terminal`, not a package function** (verified in `golang.org/x/term@v0.42.0/terminal.go:983`), and `internal/tui/picker.go` does not use it -- the crossvalidation's "the picker already uses all three" is wrong on that third item. The design itself is careful here (it claims only that the picker "drives raw mode with it"), and writing DECSET/DECRST 2004 by hand is trivial, so the no-new-dependency conclusion survives. Flagged because the plan may inherit the crossvalidation's phrasing.

9. **Structural fit is right.** `internal/promptcapture` depends on stdlib plus `golang.org/x/term`; `internal/cli` depends on it; `internal/tui` is untouched, which preserves the byte-for-byte sync obligation declared in `internal/tui/picker.go:5-14`. No cycle, no new inward dependency on `cli`. The `Read`/`read` split matches `Pick`/`pick` (`internal/tui/picker.go:64-75`) faithfully: exported binder over `os.Stdin`/`os.Stderr`, unexported core over injected reader and writer. The one deliberate divergence -- raw mode on the stdin descriptor rather than the stderr descriptor the picker raws -- is correct and the design says why.

10. **`IsStderrTTY` duplicates `tui.IsAvailable()`**, which is the same `term.IsTerminal(int(os.Stderr.Fd()))` call. Adding it to `internal/cli/prompt.go` beside `IsStdinTTY` is the right home given the sync obligation on `internal/tui`, but the duplication deserves a comment so a later reader does not consolidate them back into the synced package.

11. **R27 is only implied.** The pre-first-read banner is inferable from the Security section's reference to "the prompt that says how large the input is," but the criterion is specific (non-empty human-readable text once escape sequences are stripped, before the first read) and the design should name it as a step in the terminal lifecycle rather than leave it to the implementer.

12. **The rejected alternatives are genuine, not strawmen.** Each rejection names a concrete failure tied to a requirement: `ReadLine` loses on replacement-character substitution, empty capture on invalid UTF-8, the 4,096-byte per-line truncation, and owning its echo (R10, R19, R30); bubbletea on dropped invalid UTF-8, a measured 16x re-scan, and eighteen modules (R10, R37); `internal/tui` on the sync header, which the file confirms; full echo on scrolling the developer's failure off screen (the reason R35 exists). The design also retires its own earlier superlinear-echo argument as a harness defect rather than keeping a convenient wrong reason, which is the opposite of a strawman.

## Summary

The structure is right -- a new leaf package with the correct dependency direction, a faithful `Read`/`read` mirror of the picker's idiom, a capture seam sited where abandonment costs nothing and where `niwa watch` structurally cannot reach it, and rejections that name real failures rather than dismissing. The failure is coverage: R5's paste-boundary line feed, R17/R18's oversized refusal, R29's whitespace refusal, and R34's pre-exec re-check have no mechanism, and the CR/LF normalization rule that both decision reports explicitly handed to the design remains unstated while the design's own data-flow sentence contradicts both its gesture table and R10. Three of the four gaps are recoverable by writing down what the decision reports already worked out; the line-break rule needs an actual choice, and if it lands on normalization the PRD's R10 exception list needs to be amended alongside it.
