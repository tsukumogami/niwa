# Decision 1: Reader surface

Which terminal-reading surface carries the interactive prompt capture, and what
loop shape satisfies R4 (line breaks never terminate), R19 (no per-line limit),
R37 (no perceptible stall, single line as cheap as many), R10/R30 (byte-exact
payload, sanitized rendering) at once.

Two earlier agents reached opposite conclusions.
`explore_dispatch-paste-prompt_r1_lead-impl-surface.md` argued FOR
`x/term.Terminal.ReadLine` with an `ErrPasteIndicator` accumulation loop;
`explore_dispatch-paste-prompt_r1_lead-bracketed-paste.md` argued AGAINST it on
per-character render cost. **Both are partly wrong.** I measured the surfaces
directly. Every number below is from a prototype run under
`$CLAUDE_JOB_DIR/tmp` against `golang.org/x/term v0.42.0` (the version in
`go.mod:9`) and `bubbletea v1.3.10`; nothing was written into the repo.

## Correction to the prior evidence, first

Two measured "facts" carried forward from PRD phase 2 are artifacts of the test
harness, not properties of any reader. They have to be retired before the
options can be compared, because one of them was the strongest argument on the
table.

**The "20,000-byte single line did not complete in 20s" result is a `script`
bug, not a reader property.** I reproduced it, then bisected it:

| payload as one line, fed in one burst | x/term reader | hand-rolled reader |
|---|---|---|
| under `script -q -c … /dev/null` (the repo's PTY harness shape) | 4,000 OK; **12,000 hangs** | 4,000 and 12,000 OK; **20,000 hangs** |
| under a pty driven with non-blocking writes + `select`, retrying short writes (what a real terminal emulator does) | 130,433 OK in 0.03s | 130,433 OK in 0.00s |
| under `script`, same 130,433 bytes fed in 2 KB chunks 20 ms apart | — | **OK, 130,433 captured** |

Instrumenting the child showed it receives 4095, 4095, 2, 1 bytes and then
nothing, with ~11,800 bytes still pending — `script` does a short write to the
pty master and does not retry the remainder. The threshold differs between the
two readers (12,000 vs 20,000) only because they write back different amounts
of echo, which changes how much slack the pty buffers have. **Neither reader
has a single-line size limit, and the burst rate is what breaks, not the
payload size.** This eliminates the fact that PRD phase 2 used to make R19 and
R37 look like discriminators between candidates. They still discriminate — just
for entirely different reasons, given below.

The consequence for the design is a testability constraint rather than a
surface constraint: **any PTY acceptance criterion that feeds a large payload
must throttle the feed**, or it hangs regardless of which reader is chosen.
That belongs in the plan alongside the timeout the phase-2 research already
recommended for `test/functional/steps_init_bootstrap_test.go:143`.

**The "per-character render cost is superlinear" argument against `ReadLine` is
also wrong as stated.** x/term's own CPU cost is linear:

| payload | as one line | as 40-byte lines |
|---|---|---|
| 2,000 B | 0.25 ms | 0.15 ms |
| 32,000 B | 2.28 ms | 2.26 ms |
| 130,433 B | **9.56 ms** | 25.60 ms |

`addKeyToLine` (`terminal.go:665-679`) writes only `t.line[t.pos:]`, which is a
one-rune slice while the cursor sits at the end of the line, so the echo is
O(1) per byte, not O(line). x/term passes R37 on arithmetic. It fails on
five other requirements instead.

## Options Considered

### (a) `x/term.Terminal.ReadLine` with an `ErrPasteIndicator` accumulation loop

Wrap stdin/stderr in an `io.ReadWriter`, `NewTerminal`, `SetBracketedPasteMode(true)`,
then loop: accumulate lines returned with `err == ErrPasteIndicator`, stop on
`err == nil`. Zero new dependencies, and the loop shape does match the target UX.

**R4 (CR, LF, or both; delimited or not) — fails twice.**

*CRLF split across a read boundary.* `readLine` consumes the LF of a CRLF pair
only when the LF is already sitting in the same buffer as the CR
(`terminal.go:843`: `if key == keyEnter && len(rest) > 0 && rest[0] == keyLF`).
When the pair straddles a read, `len(rest) == 0` and the LF becomes its own
empty line. Measured, driving the identical payload two ways:

```
CRLF one chunk                -> "alpha\nbeta"
CRLF split between CR and LF  -> "alpha\n\nbeta"
```

A spurious blank line, appearing nondeterministically depending on where the
pty happens to chunk. R10 says the submitted bytes appear byte-for-byte; this
inserts one that was never pasted.

*Undelimited terminals.* R4 requires capture to work "whether or not the
terminal delimits pasted blocks." Without markers everything is typed input,
which goes through `handleKey`'s `default:` branch and hits
`if len(t.line) == maxLineLength { return }` (`terminal.go:653`,
`maxLineLength = 4096` at `terminal.go:363`). Measured:

```
unbracketed line of   4096 bytes -> captured   4096 lost      0 err=<nil>
unbracketed line of  20000 bytes -> captured   4096 lost  15904 err=<nil>
unbracketed line of 130433 bytes -> captured   4096 lost 126337 err=<nil>
```

Silent. `err=<nil>`. The user submits, and 126 KB of their failure is gone with
no indication.

**R19 (no per-line limit) — fails.** Same `maxLineLength = 4096`, same measurement.
It is bypassed inside a bracketed paste (`terminal.go:509` short-circuits before
the `default:` branch), so the cap is invisible in the happy path and fires
exactly on the degraded terminals R4 names.

**R37 — passes.** 9.56 ms for 130,433 bytes on one line, 25.60 ms for the same
across 3,260 lines. Single-line is *faster*. But see R30 for what those bytes do
once they reach a real terminal.

**R10 / R30 (byte-exact payload) — fails, badly, and this is the disqualifier.**
Inside a paste, `bytesToKey` skips its control-character switch but still falls
through to the unknown-sequence scan at `terminal.go:260`, which swallows
everything up to the first `[a-zA-Z~]` and returns `keyUnknown` (0xd800).
`string([]rune{0xd800})` is U+FFFD. Measured, on a bracketed paste:

```
LOSS SGR color           want="\x1b[31mFAIL\x1b[0m"    got="�FAIL�"
LOSS OSC title           want="\x1b]0;title\arest"     got="�itle\arest"
LOSS invalid utf8 0x80   want="a\x80b"                 got=""
LOSS invalid utf8 0xff   want="a\xffb"                 got=""
```

ANSI colour codes — which is what a pasted terminal log *is*, and the motivating
use case for the whole feature — become replacement characters. The OSC case
also eats `0;t` from the middle of the payload.

Worse, a single invalid UTF-8 byte returns the **entire capture empty**.
`bytesToKey` yields `utf8.RuneError`, `readLine` breaks out of its inner loop
(`terminal.go:~838`) and will not re-parse the saved remainder until *after* the
next `Read` returns. In a live terminal that means the capture freezes until the
user presses another key; at stream end it returns `"", io.EOF` and discards
everything typed so far. Truncated output, hexdumps, and latin-1 filenames all
put invalid UTF-8 into a pasted log routinely.

**R30 (rendering neutralizes control sequences) — structurally impossible.**
The echo happens inside `addKeyToLine` → `writeLine` → `queue`
(`terminal.go:673`, `:687`, `:270`) with no hook, and `echo` is an unexported
field (`terminal.go:89`) with no setter — `ReadPassword` flips it internally and
restores it. There is no way to interpose `tui.SanitizeDisplayString` between
the payload and the terminal, and no way to turn the echo off. For a 130,433-byte
paste x/term writes **133,703 bytes back to the terminal** (measured), dumping
the entire payload on screen and scrolling ~1,600 lines. R35 wants the input
rendered; it does not want it re-emitted verbatim, and R30 explicitly requires
neutralization.

**R8 / R28 — fails.** Ctrl-C and Ctrl-D both `return "", io.EOF`
(`terminal.go:822`, `:826`), so abandonment is indistinguishable from
end-of-input. And end-of-input on a non-empty buffer discards it rather than
submitting:

```
stream "abc"                   -> line="" err=EOF
stream "\x1b[200~abc\x1b[201~" -> line="" err=EOF
```

**R17 / R36 — structurally impossible.** Once `ReadLine` has returned a line
into the accumulator, that line is outside the editor. Backspace can only reach
the line currently being edited. R17 promises the developer can "delete down to
a submittable size" after an oversized paste; with this surface, every line but
the last is unreachable.

**Bonus livelock.** `bytesToKey`'s fallback returns the *whole* buffer as
remainder when it finds no `[a-zA-Z~]` (`terminal.go:265`). If that fills all
256 bytes of `inBuf`, then `readBuf := t.inBuf[len(t.remainder):]`
(`terminal.go:869`) is zero-length and `Read` returns `(0, nil)` forever. I
reproduced it: 51 consecutive zero-length reads before I broke the loop
artificially. Against a real `os.Stdin` this is a 100%-CPU spin with no
recovery. Trigger: an ESC followed by 255 bytes containing no ASCII letter.

### (b) A hand-rolled reader over `term.MakeRaw` that parses the markers itself

`term.MakeRaw` on the stdin fd, write `\x1b[?2004h`, read into a 64 KB buffer,
scan each chunk for `\x1b[200~` / `\x1b[201~`, append payload bytes to a
`[]byte`, render a bounded status line to stderr. Still zero new dependencies —
it uses the same three `x/term` functions `internal/tui/picker.go:79-88` already
uses (`IsTerminal`, `MakeRaw`, `Restore`); it just does not use `term.Terminal`,
the line editor.

I built this and drove it at chunk sizes 1, 3, and 4096 so that every marker is
split across a read boundary at some point.

**R4 — passes at every chunk size**, but only after a fix I had to make. My
first version collapsed CRLF by peeking at the next byte in the same chunk —
the same mistake x/term makes — and produced `"one\n\ntwo\n\nthree"` at chunk
size 1. Carrying a `lastWasCR` flag *across* chunk boundaries fixes it, and all
three chunk sizes then produce `"one\ntwo\nthree"` for CR-only, LF-only, and
CRLF payloads alike. **This hazard is intrinsic to any chunked reader and the
design must state the fix explicitly**, because it is invisible in a one-chunk
test.

Undelimited terminals: with no markers, bytes are appended the same way; there
is no line editor and no cap, so the only difference is that the developer
supplies their own line breaks via R6, which is exactly what R5 says.

**R19 — passes.** 130,433 bytes on one line captured intact.

**R37 — passes, with parity.** 130,433 bytes fed at 4,095 bytes per read (what
a pty actually delivers):

```
handrolled 130433B as single line   -> captured=130433 elapsed=1.157ms
handrolled 130433B as lines of 40   -> captured=130434 elapsed=1.185ms
```

2.4% apart. R37's "shall not take materially longer as a single line" is met
with room to spare, and it is 8x faster than x/term in absolute terms.

**R10 / R30 — passes.** Byte-exact on SGR colour, OSC sequences, invalid UTF-8
(`0x80`, `0xff`), NUL, tab, BEL, DEL, backspace, emoji, and an embedded `0x03`
or `0x04` inside a paste. The reader never decodes runes; it moves bytes. And
because rendering is a separate, bounded status line, `tui.SanitizeDisplayString`
(`internal/tui/sanitize.go:17`) applies to the display without touching the
payload — which is precisely R30's "what is rendered SHALL NOT determine what is
sent." Echo for a 130 KB paste: **2,152 bytes**, versus x/term's 133,703.

**R8 / R28 / R17 / R36 — passes.** The buffer is one `[]byte` under the reader's
own control, so abandonment and end-of-input are distinct outcomes, EOF on a
non-empty buffer submits, the ceiling is checked after every chunk while the
buffer is retained, and deletion reaches the whole buffer rather than one line.

**Cost.** ~150 lines of parsing logic. Two behaviours in it are subtle enough to
need explicit tests: the marker-prefix carry and the cross-chunk CR state.

### (c) `charmbracelet/bubbletea`

A real TUI framework with first-class paste support: `tea.KeyMsg` with
`.Paste == true` in v1, `WithoutBracketedPaste()` to disable.

**R4 — passes.** `detectBracketedPaste` (`key_sequences.go:83`) locates the end
marker with `bytes.Index` over the accumulated buffer and hands back the whole
block, so line breaks inside it never terminate anything.

**R19 — passes.** No per-line cap.

**R37 — passes, but it is the slowest of the three and the only one with a
genuinely quadratic path.** `readAnsiInputs` (`key.go:558`) reads into a
`var buf [256]byte`. Until the paste-end marker arrives, `detectBracketedPaste`
returns `(true, 0, nil)` — "short read, want more" — and the outer loop copies
the entire accumulated buffer into a freshly allocated slice
(`key.go:590-591`) *and* re-scans it with `bytes.Index`, once per 256 bytes
read. That is O(N²) in both copying and scanning. Measured: 0.36 ms at 2 KB,
3.5 ms at 32 KB, 18.06 ms at 130,433 B. Still under any reasonable "perceptible
stall" threshold, so it passes — but it is 16x the hand-rolled reader, and the
whole paste must be buffered before the model sees a single byte, so nothing can
be rendered mid-paste.

**R10 / R30 — fails, mildly but unfixably.** `detectBracketedPaste` decodes the
payload to runes and drops anything that will not decode:
`if r != utf8.RuneError { k.Runes = append(k.Runes, r) }`
(`key_sequences.go:112-115`). Measured: `a\x80b` → `ab`. It does preserve ANSI
colour codes correctly, which is better than x/term. But R10 says byte-for-byte,
and the dropping is inside an unexported function.

**R23 — passes.** I captured everything bubbletea writes on a full program run:
60 bytes, all DECSET/DECRST (`\x1b[?25l`, `\x1b[?2004h`, mouse-off on teardown).
No OSC 11 background query, no DA1, no capability probe. The concern that a
framework would probe the terminal is unfounded for this version.

**Cost.** `go get` added **18 modules**. niwa currently has 4 direct and 9
indirect requires (`go.mod:5-22`); this roughly triples the dependency graph of
a workspace CLI that hand-rolls its own 195-line picker to avoid exactly that.

### (d) Canonical-mode bulk read — the `$(cat)` workaround, promoted into the binary

Read stdin to EOF with no raw mode and no rendering. Worth stating because it is
the status quo the PRD's problem statement describes, and it is the cheapest
thing that could work.

Passes R10/R30 trivially (no parsing, no rendering) and R4 (nothing interprets
line breaks). **Fails R19 outright**: in canonical mode N_TTY's line buffer is
4096 bytes and delivers at most 4095 per read, so a single line longer than that
blocks the writer — this is the "4090-byte line hangs" result from PRD phase 2,
which *was* a real measurement and *is* a real canonical-mode property, unlike
the 20,000-byte result. Also fails R35 (nothing is rendered), R36 (no deletion),
R17 (no refusal while the input is recoverable), and R27 in spirit.

## Recommendation

**Option (b): a hand-rolled reader over `term.MakeRaw` that parses the
bracketed-paste markers itself.** Zero new dependencies, byte-exact payload,
bounded rendering, and every requirement in scope satisfied. Place it in
`internal/cli/` rather than `internal/tui/` — `internal/tui/picker.go:8-14`
carries a documented obligation to stay byte-equivalent with tsuku's copy, and
this reader is niwa-only.

Three properties of the loop are load-bearing and easy to get wrong; the
pseudocode makes each explicit.

**Raw mode goes on the stdin fd, not stderr.** `picker.go:79-88` puts stderr in
raw mode while reading from stdin; it works only because both usually point at
the same tty. R21 already requires both to be terminals, and the reader reads
stdin, so stdin is the correct fd. Render to stderr (R21, R22).

**A marker split across a read boundary is held back, and CR state crosses
chunks.** Two different pieces of cross-chunk state, both required, both
invisible to a single-chunk test.

**Rendering is a bounded status line, never the payload.** This is what makes
R30's "what is rendered SHALL NOT determine what is sent" a structural property
rather than a discipline.

```go
// Sentinels. Distinct outcomes, per R8.
var ErrCaptureCanceled = errors.New(...)   // Ctrl-C / signal: abandon (R7, R39)

type capture struct {
    buf          []byte   // the payload, byte-exact; never rune-decoded
    pasteActive  bool
    pendingBreak bool     // paste ended mid-line; owe one LF before typed text (R5)
    lastWasCR    bool     // CRLF collapse state, carried ACROSS chunks (R4)
    overCeiling  bool     // set by R17, cleared when deletion brings it back under
}

func readPrompt(in *os.File, out io.Writer, ceiling int) (string, error) {
    // R21/R22: caller has already gated on IsTerminal(stdin) && IsTerminal(stderr).
    old, err := term.MakeRaw(int(in.Fd()))
    if err != nil { return "", err }

    restore := sync.OnceFunc(func() {
        io.WriteString(out, "\x1b[?2004l")   // terminal-side state; stty cannot see it
        term.Restore(int(in.Fd()), old)      // termios
    })
    defer restore()

    // R9: defer does not cover signals. R38: re-arm raw mode after resume.
    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGCONT)
    defer signal.Stop(sigs)
    go func() {
        for s := range sigs {
            if s == syscall.SIGCONT {
                term.MakeRaw(int(in.Fd()))          // R38
                io.WriteString(out, "\x1b[?2004h")
                continue
            }
            restore()
            signal.Reset(s); syscall.Kill(os.Getpid(), s.(syscall.Signal))  // R9
        }
    }()

    // R27: human-readable text BEFORE the first read, not just control bytes.
    fmt.Fprint(out, "Paste the failure, then press Enter to dispatch.\r\n")
    io.WriteString(out, "\x1b[?2004h")
    c := &capture{}
    c.render(out)                              // R35

    readBuf := make([]byte, 64*1024)           // NOT 256; NOT 3 as picker.go uses
    var carry []byte                           // partial marker held across reads
    for {
        n, err := in.Read(readBuf)
        if n > 0 {
            chunk := append(carry, readBuf[:n]...)
            carry = nil
            // Hold back any suffix that is a proper prefix of either marker,
            // so a 6-byte marker split across two reads is still recognized.
            // Never more than 5 bytes, so this cannot stall (contrast x/term's
            // 256-byte remainder, terminal.go:869).
            if k := markerPrefixLen(chunk); k > 0 {
                carry, chunk = chunk[len(chunk)-k:], chunk[:len(chunk)-k]
            }
            outcome := c.feed(chunk)           // see below
            c.render(out)                      // bounded: counts, not payload
            switch outcome {
            case submit:
                if len(c.buf) > ceiling { c.overCeiling = true; c.renderRefusal(out); break }  // R17
                return string(c.buf), nil      // R10: bytes untouched
            case cancel:
                return "", ErrCaptureCanceled  // R8
            case endOfInput:
                if len(c.buf) == 0 { return "", ErrCaptureEmpty }  // R28
                return string(c.buf), nil                          // R28
            }
        }
        if err != nil {
            if len(carry) > 0 { c.feed(carry) }
            if len(c.buf) == 0 { return "", ErrCaptureEmpty }
            return string(c.buf), nil          // R28: EOF on non-empty submits
        }
    }
}

// feed consumes one chunk. Every byte is data unless it is a marker or, when
// no paste is active, one of the four gestures.
func (c *capture) feed(chunk []byte) outcome {
    for i := 0; i < len(chunk); {
        if chunk[i] == 0x1b && i+6 <= len(chunk) {
            if !c.pasteActive && bytes.Equal(chunk[i:i+6], pasteStart) {
                c.pasteActive, c.pendingBreak = true, false; i += 6; continue
            }
            if c.pasteActive && bytes.Equal(chunk[i:i+6], pasteEnd) {
                c.pasteActive = false
                // R5: block ended mid-line -> owe exactly one LF before typed text.
                c.pendingBreak = len(c.buf) > 0 && c.buf[len(c.buf)-1] != '\n'
                i += 6; continue
            }
        }
        b := chunk[i]
        if c.pasteActive {
            // R4: inside a paste EVERY byte is literal. CR, LF and CRLF all
            // collapse to one LF. 0x03 and 0x04 are data here, not gestures.
            wasCR := c.lastWasCR
            c.lastWasCR = false
            switch {
            case b == '\r': c.buf = append(c.buf, '\n'); c.lastWasCR = true
            case b == '\n': if !wasCR { c.buf = append(c.buf, '\n') }   // <-- the fix
            default:        c.buf = append(c.buf, b)
            }
            i++; continue
        }
        c.lastWasCR = false
        switch b {
        case '\r': return submit            // Enter
        case '\n': c.appendByte('\n')       // Ctrl-J: manual newline, R6
        case 0x03: return cancel            // R39 (ISIG is off, so this is a byte)
        case 0x04: return endOfInput        // R28
        case 0x7f: c.deleteLast()           // R36
        default:   c.appendByte(b)
        }
        i++
    }
    return none
}

// appendByte pays the R5 debt before the first typed byte after a paste.
func (c *capture) appendByte(b byte) {
    if c.pendingBreak { c.buf = append(c.buf, '\n'); c.pendingBreak = false }
    c.buf = append(c.buf, b)
}
```

`render` writes `\r\x1b[2K` plus a line/byte count and the gesture hint — cost
independent of payload size, and any payload-derived text in it goes through
`tui.SanitizeDisplayString` first.

The testable seam the phase-2 research asked for falls out directly:
`readPrompt(in, out, ceiling)` takes an injected reader and writer, and because
nothing but `MakeRaw` needs an `*os.File`, guarding that one call the way
`picker.go:79` does lets the entire parse run against a `bytes.Reader` and a
`bytes.Buffer` with no terminal — which is how I got every fidelity number above.

## Why the alternatives lose

**(a) `x/term.Terminal.ReadLine`** loses on five binding requirements, three of
them structurally rather than fixably. It mangles the payload — SGR colour codes
become U+FFFD (`terminal.go:260`), and one invalid UTF-8 byte returns the entire
capture empty — which defeats R10 and R30 on the exact content the feature
exists to carry. It cannot sanitize its own echo, because the echo is internal
to `addKeyToLine` and `echo` is unexported with no setter (`terminal.go:89`), so
R30's split between what is rendered and what is sent is unreachable; instead it
writes all 133,703 bytes of a 130 KB paste back to the screen. It silently
truncates at 4,096 bytes per line on undelimited terminals
(`terminal.go:363`, `:653`), which is exactly the case R4 and R19 name. It
returns `io.EOF` for both Ctrl-C and Ctrl-D (`terminal.go:822`, `:826`),
collapsing R8's required distinction, and discards a non-empty buffer at
end-of-input against R28. And because accumulated lines live outside the editor,
R17's promise that the developer can delete down to a submittable size cannot be
honoured. The `ErrPasteIndicator` loop shape that
`lead-impl-surface.md` correctly identified as matching the UX is real — it is
just attached to a line editor that fails the payload-fidelity requirements the
PRD was written around. Notably, the argument
`lead-bracketed-paste.md` used to reject it (superlinear per-character render
cost) is measurably false; the correct reason to reject it is fidelity, not speed.

**(c) bubbletea** is the closest call and would work. It handles R4, R19, R37
and R23 correctly, and it has better suspend/resume and signal machinery than
anything we would write. It loses on three counts: it drops invalid UTF-8 from
the payload (`key_sequences.go:112-115`), violating R10's byte-for-byte
guarantee in an unexported function we cannot reach; it is the only candidate
with a genuinely quadratic path, re-copying and re-scanning the whole
accumulated buffer once per 256 bytes read (`key.go:558`, `:590`), giving 18 ms
where the hand-rolled reader takes 1.2 ms; and it costs 18 new modules against a
`go.mod` with 4 direct requires. For a one-shot, single-field capture with no
persistent UI, a full render-loop framework is a large amount of machinery to
buy a result that is strictly worse on the requirement that matters most.

**(d) canonical-mode bulk read** fails R19 by construction — a line longer than
N_TTY's 4096-byte buffer blocks — and offers no rendering, no deletion, and no
recoverable refusal, so R35, R36 and R17 are all out of reach. It is the
workaround the PRD is replacing.

## Risks

**The two cross-chunk state variables are the whole correctness surface.** I
shipped the CRLF bug in my own first draft and only caught it by driving the
parser at chunk size 1. Both `lastWasCR` and the marker-prefix carry must be
tested at chunk sizes 1, 3, and larger-than-payload; a single-chunk test passes
while both are broken. This is the strongest argument for the injectable seam.

**A paste containing its own end marker truncates the prompt.** `\x1b[201~`
inside pasted content ends the block early and the remainder is treated as typed
input. Terminals are supposed to strip ESC from pastes but this is per-terminal
policy and has been gotten wrong before (kitty, 2018). Blast radius here is
small — the payload becomes a prompt string, never a shell command — but the
developer would see a truncated prompt with the tail appended as annotation. The
render makes it visible before submit, which is the mitigation R35 buys.

**Byte-at-a-time backspace does not really satisfy R17's promise.** R36's
capability requirement is met, but a developer 4 KB over the ceiling is not going
to press backspace 4,000 times. The gesture is decision 4's call; flagging it
here because R17's guarantee ("delete down to a submittable size rather than
lose what they pasted") is only meaningful with a coarse deletion — Ctrl-U, or
delete-last-paste-block — and that should be a stated requirement of the render
decision, not an implementation afterthought.

**R38 has a hole that cannot be closed.** SIGCONT re-arms raw mode on resume,
but `MakeRaw` clears ISIG, so Ctrl-Z does not suspend the capture in the first
place; the reachable path is an external `kill -STOP`, and SIGSTOP is not
catchable. The handler covers resume, which is what R38 actually requires, but
the window between STOP and CONT leaves the terminal in raw mode. No surface
choice changes this.

**`os.Exit` and SIGKILL still leak mode 2004.** The `sync.OnceFunc` restore
covers normal return, panic, and the three catchable signals. Nothing covers
SIGKILL. The mitigation is the short raw-mode window plus the fact that readline
shells re-enable 2004 at the next prompt anyway; it is not a reason to skip
teardown, and it is not specific to this option.

**The functional harness will hang on a large payload unless the feed is
throttled.** Not a risk of the surface, but a risk of writing R19/R37 criteria
against `script` without knowing this. Feed in 2 KB chunks with a small delay,
and add the bounded timeout the phase-2 research already recommended.

## Summary

The hand-rolled reader over `term.MakeRaw` is the right surface: it captures a
130,433-byte single line in 1.16 ms with byte-exact fidelity across ANSI colour
codes, invalid UTF-8, NUL and embedded control bytes, echoes 2 KB instead of
133 KB, and keeps the payload and the rendering structurally separate the way
R30 requires — all using the same three `x/term` functions the existing picker
already calls, with no new dependency. `x/term.Terminal.ReadLine` is
disqualified not by speed (its cost is linear, and the 20-second hang in the
prior evidence turned out to be a `script` short-write artifact that affects
every reader equally) but by fidelity: it turns pasted colour codes into U+FFFD,
returns an empty capture on a single invalid UTF-8 byte, silently truncates at
4,096 bytes per line on undelimited terminals, conflates Ctrl-C with Ctrl-D, and
offers no hook to sanitize its own echo. bubbletea would work but drops invalid
UTF-8, is 16x slower via a quadratic re-scan of the accumulated buffer, and
costs 18 new modules; the two pieces of cross-chunk state in the recommended
loop — a held-back marker prefix and a CR flag that survives a read boundary —
are where the real implementation risk lives and must be tested at chunk size 1.
