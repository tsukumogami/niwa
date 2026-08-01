# Decision 2: Gestures

Scope: the submit, manual-newline, deletion, and cancellation gestures for the
interactive capture, plus the byte-level detection rule for each once the
terminal is in raw mode. Requirements in play: R5, R6, R8, R28, R36, R39, with
hard dependencies on R4 and R10 that are called out where they bind.

## Ground truth: what raw mode actually delivers

Everything below rests on what `term.MakeRaw` does, which is verified source,
not recollection. `golang.org/x/term` v0.42.0 is already a direct dependency
(`go.mod:9`); `makeRaw` in `term_unix.go:22-43` replicates `cfmakeraw`:

```
Iflag &^= IGNBRK | BRKINT | PARMRK | ISTRIP | INLCR | IGNCR | ICRNL | IXON
Oflag &^= OPOST
Lflag &^= ECHO | ECHONL | ICANON | ISIG | IEXTEN
Cflag &^= CSIZE | PARENB ; Cflag |= CS8
Cc[VMIN] = 1 ; Cc[VTIME] = 0
```

Consequences that decide this document:

| Cleared flag | What stops happening | What the reader sees instead |
|---|---|---|
| `ICRNL` (and `INLCR`, `IGNCR`) | CR is no longer translated to NL on input | **Enter arrives as `0x0D` (CR), never `0x0A`.** Confirmed |
| `ICANON` | no line discipline, no `VEOF` | **Ctrl-D arrives as `0x04` (EOT), a data byte.** There is no EOF event from a key |
| `ISIG` | no `VINTR`/`VQUIT`/`VSUSP` | **Ctrl-C is `0x03`, Ctrl-\\ is `0x1C`, Ctrl-Z is `0x1A`** — all bytes, no signals |
| `IEXTEN` | no `VLNEXT`/`VDISCARD` | Ctrl-V (`0x16`) and Ctrl-O (`0x0F`) are ordinary bytes |
| `IXON` | no software flow control | Ctrl-S (`0x13`) does not freeze the terminal |
| `ECHO` | nothing is echoed | the capture must render everything itself (Decision 4) |
| `OPOST` | no `\n` -> `\r\n` on output | our own writes need explicit `\r\n`, as `picker.go` already does |
| `VMIN=1, VTIME=0` | — | `Read` blocks for at least one byte, then returns everything buffered; large pastes arrive in big chunks, which is what R37 wants |

Byte table for every candidate gesture, in this mode:

| Gesture | Bytes | Notes |
|---|---|---|
| Enter / Return | `0x0D` | `0x0D 0x0A` only if the terminal is in LNM (`ESC[20h`), which is off by default |
| Ctrl-J | `0x0A` | distinct from Enter in every terminal ever built; no protocol, no config |
| Ctrl-M | `0x0D` | byte-identical to Enter; unusable (Claude Code lists it as reserved for exactly this reason) |
| Shift+Enter | `0x0D` legacy; `ESC[13;2u` (kitty) or `ESC[27;2;13~` (xterm `modifyOtherKeys`, `formatOtherKeys:0`) | reportable only after the app negotiates a protocol; unavailable in GNOME Terminal/VTE, Terminal.app, Konsole, PuTTY, released Windows Terminal |
| Alt/Option+Enter | `0x1B 0x0D` | only when Option is configured as Meta; byte-identical to Esc-then-Enter |
| Ctrl-D | `0x04` | |
| Ctrl-C | `0x03` | |
| Backspace | `0x7F` (DEL) on Linux/macOS default; `0x08` (BS) on some terminals and under some `stty erase` conventions | must accept both |
| Ctrl-W | `0x17` | tty `VWERASE` conventionally |
| Ctrl-U | `0x15` | tty `VKILL` conventionally |
| Ctrl-Z | `0x1A` | a byte, not `SIGTSTP`, because `ISIG` is cleared |
| Esc | `0x1B` | ambiguous with the lead byte of every CSI sequence, including the paste markers |
| paste start / end | `ESC[200~` / `ESC[201~` = `1B 5B 32 30 30 7E` / `1B 5B 32 30 31 7E` | 6 bytes each, arriving only while the app has set DEC mode 2004 |

## Options Considered

### Submit

The candidate everyone reaches for first is **Enter (`0x0D`)**, which is what
five surveyed tools do (Claude Code, Codex, aichat, huh, current `gum write`),
and what the round-1 newline-affordance lead recommended. **R4 as accepted
forecloses it**, and this is the single most consequential finding in this
report.

R4 says a line break "SHALL NOT terminate the capture, submit the input, or
truncate it, whether it arrives as a carriage return, a line feed, or both, and
**whether or not the terminal delimits pasted blocks**." Its acceptance
criterion, filed as a unit test over the injectable core, is:

> A paste whose line breaks are line feeds does not return after the first line;
> the same paste with carriage-return breaks also does not (R4).

That test feeds the core a CR-delimited byte stream with no paste markers — the
undelimited-terminal case — and requires it not to return. If `0x0D` submitted,
that test fails by construction. The neighbouring R5 criterion says "For a
**delimited** paste..." when it means delimited; this one deliberately does not.
The PRD's Decisions section makes the same commitment in prose: "multiline input
is never truncated at a line break, on any terminal, whichever byte carries it."

Two attempts to keep Enter-submits were considered and rejected:

- **Burst/quiescence heuristic** — treat a CR arriving inside a fast byte burst
  as data and an isolated CR after idle as submit. Timing-based, so it is
  non-deterministic under the injectable core the acceptance criteria are filed
  against (a unit test feeds bytes at memory speed; the rule would pass or fail
  on feed rate). Rejected.
- **Submit only outside a bracketed paste** — this is what a marker-aware reader
  naturally does, and it is correct on a delimiting terminal. On a
  non-delimiting terminal no marker ever arrives, every pasted CR is outside a
  paste, and the first line submits. Exactly the case R4 names. Rejected.

Remaining candidates for submit:

- **Ctrl-D (`0x04`)**, plus `io.EOF` from the reader, treated identically.
  R28 already mandates that end-of-input on a non-empty buffer submits, so this
  gesture is required to be a submit gesture whatever else is chosen. Making it
  *the* submit gesture costs nothing new and removes a second concept.
- **Ctrl-S (`0x13`)** — free in raw mode since `IXON` is cleared, but users have
  a decade of muscle memory that Ctrl-S freezes a terminal, and it is still
  intercepted by outer layers in some multiplexer/ssh configurations before the
  inner app sees it. Rejected.
- **Double Enter (submit on a blank line)** — no surveyed tool uses it, and it
  is directly hostile to the payload: any log containing a blank line submits
  early on a non-delimiting terminal, which is the R4 violation again in a new
  costume. Rejected.
- **A sentinel line (`:::`, `!end`)** — both tools that shipped one (`llm`,
  aider) had to add custom delimiters because real payloads contain the
  sentinel, and our payload is by definition arbitrary error output. Rejected.

### Manual newline

Given the above, Enter is free to be the newline, which inverts the question
R6 was written for. Candidates:

- **Enter (`0x0D`)** — the gesture a developer will press anyway. Costs nothing
  and works on every terminal.
- **Ctrl-J (`0x0A`)** — the convergent choice across Claude Code (`chat:newline`
  default), Codex, aichat, `gum write`, and `huh`. Verified as a fact of the
  encoding, not a convention: `0x0A` is a different byte from `0x0D` in every
  terminal.
- **Shift+Enter** — requires negotiating the kitty protocol (`CSI > 1 u`) or
  xterm `modifyOtherKeys` (`ESC[>4;2m`), and parsing two encodings
  (`ESC[13;2u` and `ESC[27;2;13~`). Permanently unavailable in GNOME
  Terminal/VTE, Terminal.app, Konsole, PuTTY, and released Windows Terminal.
- **Alt+Enter (`0x1B 0x0D`)** — free to recognise, and it is what Claude Code's
  `/terminal-setup` remaps Shift+Enter *into* for VS Code, Cursor, Alacritty,
  and Zed.
- **`\` then Enter** — a text-level escape that works even in cooked mode. Its
  cost here is that it makes a literal backslash-at-end-of-line unrepresentable,
  in a payload that is full of Windows paths and regexes.

### Deletion

R36 requires the capability, not a richness. Candidates, cheapest first:
Backspace only; Backspace + Ctrl-W (word); Backspace + Ctrl-W + Ctrl-U (line
kill); any of those plus cursor movement (arrows, Ctrl-A/Ctrl-E) and
insert-in-the-middle; a paste-granular undo that removes the last pasted block
whole.

### Cancellation, and the ISIG fork

Two coherent terminal-mode choices, and they are not independent of the
gestures:

- **Clear `ISIG`** (plain `term.MakeRaw`, what `picker.go` already does).
  Ctrl-C is byte `0x03`, handled in-band by the same loop that handles every
  other gesture. Ctrl-\\ and Ctrl-Z likewise become bytes.
- **Keep `ISIG`** (a hand-built "cbreak" termios: clear `ICANON`/`ECHO`, keep
  `ISIG`). Ctrl-C raises `SIGINT` asynchronously mid-`read`.

## Recommendation

**Submit: Ctrl-D (`0x04`) outside a paste, and `io.EOF` from the reader, treated
identically.** One submit gesture, satisfying R5's "same gesture whether or not
you typed alongside the paste" trivially. On a non-empty buffer it returns the
accumulated bytes; on an empty buffer it returns the end-of-input outcome
(R28). This is also the gesture the workaround this feature replaces
(`niwa dispatch "$(cat)"`, paste, Ctrl-D) already taught, which the PRD calls
out by name.

**Manual newline: Enter (`0x0D`) and Ctrl-J (`0x0A`), both outside a paste, each
appending exactly one `0x0A` to the buffer.** `0x0D 0x0A` arriving adjacently
appends one `0x0A`, not two — that is R4's "or both" clause. Alt+Enter
(`0x1B 0x0D`) and the two Shift+Enter encodings (`ESC[13;2u`,
`ESC[27;2;13~`) are recognised as newline if they happen to arrive, but the
capture never emits a protocol-enable sequence to ask for them (R23 forbids
probing, and negotiating would also mean tracking and restoring a second piece
of terminal state). Recognising them costs a few branches and prevents a
remapped Shift+Enter from landing a stray `0x1B` in the payload.

**Deletion: Backspace (`0x7F` and `0x08`), Ctrl-W (`0x17`), Ctrl-U (`0x15`).**
Rune-aware, append-only, tail-only:

- Backspace removes the last *rune*, not the last byte, so a multi-byte UTF-8
  sequence never gets split.
- Ctrl-W removes the trailing run of non-whitespace, then any whitespace before
  it (`VWERASE` semantics).
- Ctrl-U removes back to and not including the previous `0x0A`; if the buffer
  already ends in `0x0A`, it removes that `0x0A` too, so repeated Ctrl-U walks
  up line by line.

No cursor movement, no insertion in the middle, no undo. The buffer stays a
plain `[]byte` that only ever grows at the end or shrinks from the end, which is
what keeps R37 (no perceptible stall at 130,433 bytes) a non-event: there is no
re-layout, no rope, no line index to rebuild.

**Cancellation: Ctrl-C (`0x03`) outside a paste, returning `tui.ErrCanceled`;
plus a `signal.Notify` handler for `SIGINT` that produces the same outcome.**
Both arms of R39's "whether it arrives as a signal or as an input byte" are
covered, and the observable outcome is identical. Escape (`0x1B`) is
deliberately not bound to anything.

**ISIG: clear it — use `term.MakeRaw` unchanged.** The decisive argument is not
ergonomics, it is correctness of the payload. With `ISIG` set, a pasted log
containing byte `0x03` kills the process and a pasted `0x1A` suspends it, mid-
capture, with the buffer lost — and R30 explicitly requires pasted control
sequences to survive into the prompt. `IXON` and `IEXTEN` carry the same hazard
for `0x13` and `0x16`. `MakeRaw` clears all four together, and it is what the
existing picker does, so the capture and the picker share one mode discipline.

**Three outcomes from the capture core**, which is what makes R8 mechanical:

| Outcome | Trigger | Return |
|---|---|---|
| submit | `0x04` or `io.EOF`, buffer non-empty | `(buf, nil)` |
| end-of-input on empty | `0x04` or `io.EOF`, buffer empty | `(nil, tui.ErrEndOfInput)` |
| abandonment | `0x03`, or `SIGINT` | `(nil, tui.ErrCanceled)` |

`tui.ErrCanceled` already exists (`internal/tui/picker.go:30`) and `destroy.go`
already treats it as a user-driven abort without printing its string, so reusing
it is consistent and needs no change to `picker.go` — which matters, since that
file is a maintained byte-equivalent copy of tsuku's and divergence has a cost.
`ErrEndOfInput` is new and belongs in the capture's own file in the same package.
A whitespace-only buffer submits and is then refused by the existing empty-prompt
error (R29); a genuinely empty buffer never reaches that path (R28). Both exit
non-zero (R31), by different messages, which is the distinguishability R8 asks
for.

**Ctrl-Z (`0x1A`)**: with `ISIG` cleared this is a byte, so it must be handled
explicitly rather than silently appended to the payload. Recommend routing it to
the same self-suspend path Decision 5 owns — restore the terminal, re-raise
`SIGTSTP` with the default disposition, re-establish raw mode and DEC 2004 on
`SIGCONT`. Note that R38's suspend/resume criterion needs that `SIGCONT` handler
regardless of this binding, because an external `kill -TSTP` reaches the process
either way.

### The paste-boundary line feed (R5)

Detection, with no probing: the capture writes `ESC[?2004h` once at start and
`ESC[?2004l` on every exit path. Setting a DEC private mode is not a probe — no
reply is expected and an unsupporting terminal ignores it silently, which is
precisely the "do nothing if the markers do not arrive" behaviour R23 wants. The
reader then observes markers if they come and behaves identically if they never
do.

State: one boolean, `pendingBreak`.

1. On `ESC[200~`: `pasteActive = true`. **Every gesture byte above is inert
   while `pasteActive` is true** — `0x04`, `0x03`, `0x7F`, `0x15`, `0x17`,
   `0x0D`, `0x0A`, `0x1A` are all data. This is exactly the rule
   `x/term`'s `bytesToKey(b, pasteActive)` encodes, and its own test asserts it:
   `"abc\x1b[200~de\177f\x1b[201~\177\r"` yields `"abcde\177"` — the DEL inside
   the paste survives, the one outside deletes.
2. On `ESC[201~`: `pasteActive = false`, and
   `pendingBreak = len(buf) > 0 && buf[len(buf)-1] != '\n'`.
3. On the next event that appends to the buffer — a typed rune, a typed line
   break, or a new paste's payload — append one `0x0A` first and clear
   `pendingBreak`. If that triggering event was itself a line break, append
   nothing further: the boundary already supplied the break the developer asked
   for, so paste-then-type and paste-then-Enter-then-type both yield exactly one
   `0x0A`.
4. On a deletion event: clear `pendingBreak` without appending. Deleting
   immediately after a paste means the developer is trimming the paste's tail;
   inserting a break first would be nonsense.
5. On submit or abandonment: discard `pendingBreak`. A bare paste followed by
   Ctrl-D therefore returns exactly the pasted bytes, with no trailing `0x0A`
   the developer did not paste.

This matches both unit criteria word for word: "the returned string equals the
pasted bytes, one line feed, then the typed bytes -- compared exactly" for the
delimited-then-typed case, and "one submit gesture returns the captured text for
a bare paste" for the bare case. It also inserts nothing when the paste already
ended in a newline, which is the common mouse-selection case (whole-line
selections usually carry their trailing newline).

Two parsing constraints that gesture detection depends on and that Decision 1
must honour in the reader:

- **Markers split across reads.** A 6-byte marker can straddle a `read()`
  boundary, so detection must run over a carried byte stream with a partial-
  sequence remainder, not per-read. `picker.go:97` reads into a 3-byte buffer,
  which is physically incapable of holding a marker — an explicit
  do-not-copy precedent. tmux raises its escape-delay floor to 500ms on a
  partial paste-end specifically because this is common.
- **Never drop the tail of a chunk after `ESC[201~`.** Terminals routinely
  deliver `ESC[201~\r` in one PTY write (iTerm2's "paste with newline",
  Terminal.app's "paste newlines as carriage returns"); a Cursor CLI bug exists
  for exactly this. Under this recommendation that trailing `\r` is a manual
  newline rather than a submit, so the specific Cursor failure is neutered — but
  a reader that discards the remainder of the chunk would still silently lose
  typed bytes.

### The discoverability consequence

Because Enter does not submit, the capture is inverting the behaviour of the
user's daily driver, and R27's pre-read text is the only thing standing between
that and confusion. R27 already requires human-readable waiting text; this
decision makes its *content* load-bearing rather than decorative. It must name
the submit gesture, e.g.:

```
Paste or type your prompt.  Ctrl-D sends   Ctrl-C cancels
```

`gum write`'s entire discoverability story was its placeholder string, and its
issue #423 is a user who found Ctrl-D as submit illogical *and still used it*
once told. The rendering is Decision 4's; the requirement that the string names
Ctrl-D is this decision's.

### The raw-mode fd

`picker.go:79-87` sets raw mode on the **stderr** fd while reading **stdin**.
That works only because both refer to the same tty. The line discipline that
decides whether we see `0x0D` or `0x0A` belongs to the device stdin is open on,
so the capture should call `term.MakeRaw` on the stdin fd. Under R21 both are
terminals, but nothing guarantees they are the *same* terminal, and gesture
detection is exactly the thing that breaks if the wrong device is raw. Flagged
here because it is a gesture-correctness dependency; the lifecycle mechanics
belong to Decision 5.

## Why the alternatives lose

**Enter as submit loses to R4, not to taste.** It is the better gesture on every
other axis — it matches Claude Code, Codex, aichat, `gum`, and `huh`; it needs no
explanation; it is what the round-1 lead recommended. R4 as accepted is
unconditional across delimiting and non-delimiting terminals, and its unit
criterion feeds CR-delimited bytes with no markers and requires no return. There
is no implementation of Enter-submits that passes that test. **If the team wants
Enter-submits, R4 has to be reopened and narrowed to delimiting terminals** — and
that trade is real, because it would mean a multiline paste on GNU screen or a
legacy console submits its first line and dumps the rest onto the shell prompt.
This report recommends keeping R4 and taking the inversion, but the choice
belongs upstream and should be made explicitly rather than absorbed.

**Shift+Enter loses on terminal support and on R23.** Getting it reported means
emitting `CSI > 1 u` or `ESC[>4;2m` — capability negotiation in all but name,
against a requirement that forbids probing — and then parsing two encodings, and
tracking a third piece of terminal state to restore. It is permanently
unavailable in GNOME Terminal/VTE, Terminal.app, Konsole, PuTTY, and released
Windows Terminal; tmux forwards CSI-u only after three settings, one of which
(`extkeys` inside `terminal-features`, not `extended-keys`) fails silently when
written wrong. Recognising the encodings passively costs nothing and is
recommended; depending on them is not.

**Ctrl-M loses because it is Enter.** Same byte, no way to tell them apart
without the protocol above.

**Ctrl-O loses because it is taken** in this exact product category —
`app:toggleTranscript` in Claude Code, external-editor in aichat — and
historically it is SI.

**Esc-then-Enter is not a distinct option.** It emits `0x1B 0x0D`, byte-identical
to Alt+Enter; the only way to separate them is an escape-timeout race. Listing
them separately in a design doc would be a mistake.

**Double Enter loses on evidence and on R4.** No surveyed terminal chat tool
uses it, and on a non-delimiting terminal a pasted log containing one blank line
submits early — the same truncation R4 exists to forbid.

**Sentinel lines lose on payload collision.** Both `llm` and aider had to bolt on
custom delimiters (`!multi abc`, `{tag`...`tag}`) because real payloads contain
the sentinel; ours is arbitrary error output.

**Backspace-only deletion loses to R17's promise.** R17 says the developer can
"delete down to a submittable size". A buffer 600 bytes over 130,433 is a
handful of log lines: Ctrl-U a few times and it submits. Backspace alone makes
that 600 keypresses. Ctrl-U is what turns R17 from a sentence into a behaviour,
and it is roughly thirty lines of code.

**Cursor movement, mid-buffer insertion, and paste-granular undo lose on cost
against R37.** They convert the buffer from an append-and-truncate `[]byte` into
a structure with a line index and a cursor that must be maintained across a
130 KB single-line paste — the exact shape of the superlinear echo cost the PRD's
Known Limitations already measured on one candidate library path (12,000 bytes
fine, 20,000 bytes not finishing in 20 seconds). They also buy nothing R36 or
R17 asks for.

**Keeping `ISIG` loses on three counts.** It corrupts the payload: a pasted log
containing `0x03` or `0x1A` kills or suspends the process mid-capture, against
R30's requirement that pasted control sequences survive. It makes R39
untestable at the level its criterion is filed at: with `ISIG` cleared, the
"interrupt as an input byte" arm is a unit test that feeds `0x03` to the
injectable core and asserts `ErrCanceled`, while with `ISIG` set the only test is
a real process, a real signal, and a race between the handler's restore and the
reader's in-flight `read`. And it diverges from `picker.go`, which already uses
`MakeRaw`, for no gain — `SIGINT` still needs a `signal.Notify` handler either
way, because `kill -INT` from another process reaches us regardless of termios.

## Risks

**R4 and R10 collide on line-break normalization, and this decision depends on
the resolution.** R10 forbids "trimming, normalization, or re-encoding" beyond
the prepend and R5's single line feed. R4 requires CR, LF, and CRLF to all be
line breaks and requires CRLF not to become two breaks — which is normalization.
The recommendation above normalizes every line break outside a delimited paste to
one `0x0A`. Inside a delimited paste the same question arises for Decision 1
(Terminal.app pastes CR by default; preserving those byte-for-byte yields a
prompt whose lines overwrite each other in most consumers). The design doc must
state one rule for both and say plainly that R4 governs line breaks while R10
governs everything else — quotes, backslashes, dollar signs, ESC sequences,
UTF-8 — or the PRD's R10 exception list needs amending. Left implicit, a reviewer
can read R10 as forbidding what R4 requires.

**The Enter inversion is the feature's largest usability risk and it is not
recoverable by documentation alone.** A developer whose muscle memory says Enter
sends will press Enter, see a newline, press it again, and eventually find
Ctrl-D. The mitigation is R27's hint naming Ctrl-D, and the fact that the
gesture is the one the existing workaround already taught. It is still a real
cost and it is directly caused by holding R4 unconditional.

**Terminal-side `ESC[?2004h` state leaks on a hard kill.** If the process dies
without writing `ESC[?2004l` — `SIGKILL`, `os.Exit` past a `defer` — the terminal
stays in bracketed-paste mode and the next program sees literal `00~` before
pasted text. This is a known, widely reported symptom with an open Claude Code
issue of its own. `stty sane` does not clear it; only `printf '\e[?2004l'` or
`reset` does. Under readline shells it self-heals at the next prompt, which is
not a reason to skip teardown. Decision 5 owns the discipline.

**A control byte inside an undelimited paste is indistinguishable from a
gesture.** On a terminal that does not bracket, a pasted `0x03` cancels and a
pasted `0x04` submits. Terminals are supposed to filter ASCII controls out of
pasted data (xterm's `allowPasteControls` defaults off), so this is mostly
theoretical — but it is unfixable in principle, because the whole point of the
markers is that without them there is no distinction to make.

**A paste containing its own end marker truncates the capture.** Demonstrated
against kitty in 2018 by splitting `ESC[201~` across clipboard content. Blast
radius here is small — the payload becomes a prompt string, never a shell
command — but the remainder lands as gestures and typed text rather than as
prompt content, so the developer gets a silently short prompt. Worth a test.

**`x/term`'s `Terminal` has a latent stall** at `readBuf := t.inBuf[len(t.remainder):]`
— if `remainder` fills all 256 bytes, `Read` gets a zero-length buffer and spins.
It is reachable through the unknown-sequence fallback. This is an argument for
borrowing `x/term`'s marker-parsing *idea* and its `bytesToKey(b, pasteActive)`
gating rule while writing our own scanner, rather than driving
`Terminal.ReadLine` — which is Decision 1's call, but the gesture rules above are
stated over a byte stream precisely so they survive either choice.

**Backspace ambiguity.** Most terminals send `0x7F`; some send `0x08`, and a
user with `stty erase ^H` will have configured their terminal accordingly.
Accepting both is correct and costs nothing, but it means `0x08` can never be
bound to anything else.

## Summary

Raw mode via `term.MakeRaw` — verified to clear `ICRNL`, `ICANON`, `ISIG`,
`IEXTEN`, and `IXON` — makes Enter arrive as `0x0D`, Ctrl-D as the data byte
`0x04`, and Ctrl-C as the data byte `0x03`, and R4's unconditional
no-truncation-at-a-line-break guarantee plus its CR-delimited unit criterion
rules out Enter as the submit gesture on any terminal that does not bracket
pastes. The recommendation is therefore Ctrl-D (and `io.EOF`) to submit, Enter
and Ctrl-J to insert exactly one line feed with Alt+Enter and the two
Shift+Enter encodings recognised passively but never negotiated, Backspace plus
Ctrl-W plus Ctrl-U for tail-only deletion on an append-only byte buffer, and
Ctrl-C returning the existing `tui.ErrCanceled` in-band alongside a `SIGINT`
handler that produces the same outcome — with `ISIG` cleared, because a pasted
log containing `0x03` must not kill the capture. The paste boundary sets a
single `pendingBreak` flag at `ESC[201~` when the buffer does not already end in
a newline and flushes exactly one `0x0A` before the next appended byte, so a
bare paste returns its bytes unchanged while a paste followed by typing returns
pasted bytes, one line feed, then the typed bytes; the open cross-decision issue
is that R10's byte-for-byte guarantee and R4's CR/CRLF handling both describe
line breaks and must be reconciled in one stated rule.
