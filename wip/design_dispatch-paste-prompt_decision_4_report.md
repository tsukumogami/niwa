# Decision 4: Rendering

**Question.** How is captured input rendered so the developer sees what will be
sent, without replaying control sequences and without the superlinear echo cost
measured on one candidate path?

**Requirements.** R27, R30, R35, R37.

## Findings that constrain the answer

Four things were verified in this worktree before weighing options.

### F1. `SanitizeDisplayString` is fail-safe against ESC-introduced sequences and blind to every other control byte

`internal/tui/sanitize.go` is three alternatives: a CSI form ending in
`[A-Za-z]`, an OSC form, and a bare `\x1b`. Behavior against a spread of real
inputs (probe at `$CLAUDE_JOB_DIR/tmp/sanprobe`):

| Input | Output | Verdict |
|---|---|---|
| `\x1b[200~hello` | `[200~hello` | ESC removed, residue visible |
| `hello\x1b[201~` | `hello[201~` | same |
| `\x1b[?2004h` | `` | fully stripped |
| `\x1b[31mred\x1b[0m` | `red` | fully stripped |
| `\x1b]0;pwned\x07after` | `after` | fully stripped |
| `\x1bPq#0;2;0;0;0\x1b\\rest` | `Pq#0;2;0;0;0\rest` | ESC removed, residue visible |
| `abc\rXYZ` | `abc\rXYZ` | **unchanged** |
| `abc\bX` | `abc\bX` | **unchanged** |
| `ding\x07` | `ding\x07` | **unchanged** |
| `a\x0cb`, `a\x0bb`, `a\x00b`, `a\x7fb` | unchanged | **unchanged** |
| `a\x9b31mb` (8-bit CSI) | unchanged | **unchanged** |

The prior report's claim is confirmed: CSI sequences whose final byte is not a
letter are not matched as sequences, and that includes both bracketed-paste
markers, whose final byte is `~`. But the consequence is milder than the claim
implies. The trailing `|\x1b` alternative removes the introducer, so the residue
(`[200~`, `Pq#0;2;...`) is inert printable text. Against anything ESC
introduces, the function is fail-safe: worst case is visual garbage, never a
cursor move.

The real gap is the row block at the bottom. CR, BS, BEL, VT, FF, NUL and DEL
need no ESC and pass through untouched. CR alone returns the cursor to column 0
and lets the next bytes overwrite what is already there — and CR is precisely
what pasted line breaks are on most terminals (the r1 research documents
Terminal.app's "paste newlines as carriage returns" defaulting on). A capture
that ran pasted content through `SanitizeDisplayString` would hand the terminal
a cursor-control primitive on the most common byte in the payload. This is a
display-corruption path R30 forbids.

(Aside, out of scope: the picker has the same latent hole. `render()` at
`picker.go:132` repaints with an absolute `\x1b[%dA` computed from the choice
count, so a `\r` in a caller-supplied description desynchronizes the frame. Worth
an issue against both copies; not this feature's work.)

### F2. `sanitize.go` is itself a synced copy, in three places

`picker.go`'s header binds that file to `tsukumogami/tsuku@c8f58101`. The
obligation extends to `sanitize.go`: diffing niwa's against
`public/tsuku/internal/tui/sanitize.go` shows the code identical and only the doc
comments diverging, and tsuku's comment records a third copy —
"Mirrors internal/progress/sanitize.go". Any behavior change to this function is
a three-way mirror-or-document-the-divergence obligation, incurred for a change
tsuku has no use for.

### F3. Neutralization is not where the cost is

A byte-wise neutralizer (prototype at `$CLAUDE_JOB_DIR/tmp/neut`) processes the
full 130,433-byte ceiling in roughly 200µs, and the pathological all-ESC payload
(which doubles in length) in the same order. Whatever the render model, escaping
is free.

### F4. The cost is write syscalls, not bytes

Writing 130,433 bytes to a pipe with an eager draining reader
(`$CLAUDE_JOB_DIR/tmp/wcost`):

| Strategy | Time |
|---|---|
| One write | 318µs |
| One write per character | 124ms |
| Bounded summary (~120 bytes) | 14µs |

Roughly 400x, and this is a pipe: no terminal parsing, no scrollback, no
flow control. The earlier 20,000-byte stall is consistent with this plus PTY
flow control — the writer blocks once the PTY output buffer fills — rather than
with any superlinear algorithm. Two corollaries: per-character echo is
disqualified on syscall count alone, and bulk echo is cheap *for the program*
while still handing the terminal 130 KB to parse, render and scroll.

**Incidental but decisive for Decision 1:** `x/term.Terminal` truncates every
line at `maxLineLength = 4096` runes and silently discards the rest
(`terminal.go:363` and `:653`, confirmed on v0.42.0 and reproduced — a
130,433-byte single line returns 4,096 bytes with `err == nil`). That kills
`ReadLine` as the capture surface under R19 and R10 independently of any echo
question, and it also means the earlier probe's 12,000-vs-20,000 comparison was
never comparing echo volumes: both echoed 4,096 characters.

## Options Considered

### (a) Full echo of every byte

Two sub-variants, and they fail differently.

*Per-character* is what a line editor does and is what every large-paste hang in
the r1 research traces to. 124ms of pure syscall overhead at the ceiling before
the terminal does any work, and it deadlocks against PTY flow control. Fails R37
outright.

*Batched* — neutralize the block, write it in one syscall — costs the program
318µs and passes R37 if R37 is read as a statement about program time. It still
loses, for three reasons that are not about speed:

- It hands the terminal ~2,400 lines to render and scroll. R37 says "no
  perceptible stall," and perceptibility is the terminal's rendering, not niwa's
  write. Over ssh or in a multiplexer this is seconds.
- It scrolls away the failure the developer was about to paste. The feature
  exists because the error is on screen; the rendering destroys the screen.
- It makes R36 unimplementable without a line editor. Deleting from a buffer
  whose full text is on screen means repainting an unbounded region, which
  requires width tracking, wrap arithmetic and scroll-region management — the
  thing the PRD's scope boundary is trying not to build.

Worst case is also worse than the ceiling: neutralization expands, and an
all-control payload doubles 130,433 bytes to 260,866.

### (b) Compact placeholder for pasted blocks, full echo of typed characters

The r1 recommendation. Bounded by construction, so R37 is satisfied with room to
spare, and typed echo at human rate is trivially cheap.

The problem is R35. For the central case — a bare paste, no annotation — the
developer sees `[pasted 47 lines, 2.1 KB]` and nothing else. That is extent, not
identity: it does not distinguish the log they meant to paste from the one still
in the clipboard from a previous selection, and it does not tell them whether the
mouse selection actually caught the stack trace or stopped short. R35's purpose
clause is "so the developer can see what will be sent," and the PRD's own
revision history says six criteria were rewritten because "a violating
implementation could have passed" them. A pure byte counter is exactly that
implementation.

### (c) Full echo with a threshold above which it collapses to a placeholder

Inherits (a)'s screen-destruction and deletion problems for everything under the
threshold, and (b)'s identity problem above it, with a discontinuity at an
arbitrary byte count. It also puts R37's "SHALL NOT take materially longer than
the same number of bytes across many lines" clause right at the boundary, where a
just-under-threshold single line gets the expensive path and a just-over one
gets the cheap path. Nothing recommends it over (d).

### (d) Append-only transcript: one bounded render per input event

The recommendation. Rendering is a log of what the capture accepted, not a
reproduction of the buffer. Each input event appends at most a fixed number of
lines, none of which exceed the terminal width. The cursor is never moved up, and
nothing already on screen is ever rewritten.

Where (b) reports only extent, (d) reports extent *and* identity: it includes the
block's first and last line verbatim (neutralized, width-truncated). For a
failure log those are the two most identifying lines that exist — the first `---
FAIL:` and the trailing `FAIL <package>` — so the developer can tell at a glance
whether the right thing landed. Cost is O(terminal width), not O(payload):
14µs at the ceiling.

## Recommendation

### The model

**Append-only transcript. One bounded render per input event. The cursor never
moves up or left except for single-character backspace over typed text, and
nothing on screen is ever repainted.**

The unit of rendering is the input event, which the reader already distinguishes
for Decision 1 and Decision 2:

- **A read of at most 64 bytes** (a keystroke, or a small paste that fits on a
  line) is echoed verbatim, neutralized. The model splits it on LF and emits
  `\r\n` between the neutralized pieces. 64 is far above any single key,
  including multi-byte escape-sequence keys, and no human types fast enough to
  exceed it in one read.
- **A delimited paste block, or any read above 64 bytes**, appends a summary:

  ```
  [pasted 2431 lines, 130433 bytes]
    first: === RUN   TestApplyReconcilesWorkspaceRoot
     last: FAIL	github.com/tsukumogami/niwa/internal/apply	0.114s
  ```

  The count line states the block's line count and byte count; it appends
  `; buffer NNN bytes` whenever the buffer total differs from the block size
  (that is, from the second block onward, or when typed text preceded it). The
  two excerpt rows are the block's first and last non-empty line, neutralized and
  truncated by rune count to the terminal width less the label, with a trailing
  `...` when truncated. A single-line block renders one row labelled `text: `
  instead of two.
- **Single-character deletion over typed text** emits `\b \b`. Bounded, and the
  standard.
- **Bulk deletion** appends a notation line rather than erasing:
  `[removed 2431 lines, 130433 bytes; buffer 1203 bytes]`.
- **The R17 oversized refusal** appends its message as ordinary transcript text
  after the block that crossed. The block still renders its summary and excerpt
  first, because R17 keeps the buffer and the developer needs to see what is in
  it.

Bytes go to standard error (R21, R22), after `MakeRaw`, so every line ending in
the render path is `\r\n` — `MakeRaw` clears `OPOST` and a bare `\n` staircases.

Terminal width enters in exactly one place: truncating excerpt rows. Get it from
`term.GetSize(fd)` (a `TIOCGWINSZ` ioctl, not a terminal query, so R23 is
unaffected) and fall back to 80 on error. **The design does not otherwise need to
care about width**, because append-only rendering has no wrap arithmetic to get
wrong. Truncation counts runes, not display columns; a CJK-heavy or
combining-heavy excerpt may wrap one extra row. That is not worth a `wcwidth`
table. `\b \b` is likewise wrong at a wrap boundary and leaves one stale
character when backspacing across it — accepted, and noted under Risks.

Under no circumstances may the renderer emit `\x1b[6n` or any other query. The
temptation exists only for a width-aware renderer, and this one is not.

### The neutralization rule (R30), at byte level

**Escape into visible form. Do not strip, do not placeholder.**

Stripping is the wrong answer twice over: a payload of nothing but control bytes
renders as nothing at all, which satisfies R35 vacuously, and stripping silently
joins text that was not adjacent. Escaping keeps the developer informed that
something was there — the `less` and `cat -v` convention, which developers
already read fluently.

Contract: input is a run of buffer bytes containing no LF; output contains no
line break. Line structure belongs to the render model, not to the escaper.
Walking the input:

1. `0x09` (TAB) passes through unchanged. It only advances the cursor forward
   within the line, and Go test output is tab-indented — rendering it `^I` would
   make excerpts unreadable.
2. Any other byte below `0x20`, and `0x7F`, is emitted as caret notation: `^`
   followed by `byte XOR 0x40`. So ESC becomes `^[`, CR `^M`, LF `^J`, BS `^H`,
   NUL `^@`, DEL `^?`. Two printable ASCII characters, forward-only.
3. `0x20`–`0x7E` passes through unchanged.
4. A byte at or above `0x80` is UTF-8 decoded. If it decodes to a code point in
   `U+0080`–`U+009F` (the C1 controls, which include the 8-bit CSI `0x9B` the
   current function misses), it is emitted as `\xNN` with `NN` the uppercase hex
   of the code point.
5. A byte at or above `0x80` that is not the start of a valid UTF-8 sequence is
   emitted as `\xNN`. An invalid byte is never passed through: it can
   desynchronize the terminal's UTF-8 decoder, and on a terminal in a non-UTF-8
   locale it can be re-read as a C1 control.
6. Any other valid code point passes through unchanged.

The invariant this buys, which is what should actually be tested: **every byte
the escaper emits is either printable ASCII, TAB, or part of a valid non-C1
UTF-8 sequence — nothing moves the cursor up, left, or to column 0, and nothing
changes terminal state.** That is a single-pass property test over arbitrary
input, and it is strictly stronger than "no ANSI sequences survive."

Verified against the same inputs as F1: `\x1b[200~payload\x1b[201~` renders
`^[[200~payload^[[201~`; `line1\rOVERWRITE` renders `line1^MOVERWRITE`;
`\x9b31m` renders `\x9B31m`; `caf\xc3\xa9 \xff\xfe` renders `café \xFF\xFE`.

R30's "what is rendered SHALL NOT determine what is sent" should be structural,
not careful: the escaper takes a `[]byte` and appends to a separate output slice,
and the render function receives a read-only view of the buffer. Neither has a
handle capable of mutating it. The buffer is appended to by the reader alone.

### Write something new; leave `SanitizeDisplayString` alone

Put the escaper in the new capture package. Three reasons:

1. **Different jobs.** `SanitizeDisplayString` deletes escapes from short,
   mostly-trusted strings so a picker frame is not corrupted. The capture escapes
   arbitrary untrusted bytes so the developer can see they were there. Deleting
   and escaping are different functions; merging them means a mode flag on a
   function whose entire value is that it has no options.
2. **The three-way mirror (F2).** Changing this function's behavior obliges a
   matching change in tsuku's `internal/tui/sanitize.go` and
   `internal/progress/sanitize.go`, for a capability tsuku does not want.
3. **Shape.** A regex with a `|\x1b` alternative is right for a 40-byte instance
   name and wrong as the hot path over untrusted payloads. The byte-wise pass is
   both faster and easier to state an invariant about.

File a separate issue for the C0 gap in `SanitizeDisplayString` (F1's bottom
block, plus 8-bit CSI), mirrored across all three copies. That is a real latent
bug in the picker, and it is not this feature's to fix.

### The R27 waiting text, literally

Written to standard error immediately after entering raw mode and enabling
bracketed paste, before the first read:

```
niwa dispatch: waiting for the task text.
Paste or type it, then press Enter to send. Ctrl-C cancels. Limit 130433 bytes.
```

(Enter, Ctrl-C and the newline gesture are Decision 2's to name; the sentence
shape holds whatever it decides, and the second line should name the manual
newline gesture once Decision 2 fixes it.)

Every byte is printable ASCII plus the line break. **The banner contains no
escape sequence at all**, so R27's criterion — "the bytes written before the
first read contain non-empty human-readable text once escape sequences are
stripped" — passes by construction rather than by measurement. The only escape
bytes written before the first read are the mode-set `\x1b[?2004h` and whatever
`MakeRaw` does via termios (no bytes at all), and `\x1b[?2004h` is fully matched
and removed by a strict ANSI stripper (verified in F1). It is a mode set, not a
query, so R23 holds.

One trap for whoever writes that test: it must strip with a *strict* ANSI
stripper, not with the capture's own escaper. The escaper renders `\x1b[?2004h`
as the visible text `^[[?2004h`, which would leave the criterion passing for the
wrong reason.

Byte counts are rendered as plain digits, no thousands separators — Go has no
separator formatter outside `golang.org/x/text/message`, and adding a dependency
for commas is not worth it. The ceiling appears as `130433`, matching R18's
refusal message.

### What the developer sees for a large paste

Paste a 130,433-byte CI log into a fresh capture and press Enter:

```
niwa dispatch: waiting for the task text.
Paste or type it, then press Enter to send. Ctrl-C cancels. Limit 130433 bytes.
[pasted 2431 lines, 130433 bytes]
  first: === RUN   TestApplyReconcilesWorkspaceRoot
   last: FAIL	github.com/tsukumogami/niwa/internal/apply	0.114s
```

Four lines, ~120 bytes, 14µs, nothing scrolled away. Type an annotation after the
paste and it echoes character by character on the next line, at typing speed.
Paste a 210 KB log instead and the same block renders, followed by:

```
Input is 210004 bytes; the limit is 130433 bytes. Write the text to a file and
dispatch a prompt that references that path.
```

with the capture still open and the buffer intact (R17), and no occurrence of
"shorten" (R18). Note in passing that the *existing* message at
`internal/cli/dispatch.go:145` does say "shorten it rather than relying on
truncation" and must change on both paths under R25's baseline.

## Why the alternatives lose

**(a) per-character** fails R37 on syscall count alone: 124ms at the ceiling into
a pipe with an eager reader, before the terminal parses a byte, and it deadlocks
against PTY flow control, which is the most plausible reading of the earlier
20-second measurement.

**(a) batched** survives R37 read narrowly as program time, and loses on R37 read
as the developer experiences it — the terminal still renders and scrolls 2,400
lines — and independently on R36, because deleting from a fully-echoed buffer
requires repainting an unbounded screen region, which is the line editor the PRD
puts out of scope. It also defeats R35 in practice: the developer can see only
the last screenful, and the failure they were reading is gone.

**(b) placeholder-only** satisfies R37 and R27 and fails R35. "Sees what will be
sent" is not satisfied by a byte count, and the PRD has already rejected one
round of criteria a violating implementation could pass. (d) is (b) plus two
lines of excerpt, which is the whole difference between reporting extent and
reporting identity, at no meaningful cost.

**(c) threshold** takes (a)'s screen destruction below the line and (b)'s
identity failure above it, and adds a discontinuity exactly where R37's
"materially longer than the same bytes across many lines" clause is measured.

**Stripping instead of escaping** loses because a control-only payload would
render as nothing, which passes R35's criterion while showing the developer
nothing, and because stripping joins non-adjacent text — the same class of
silent misrepresentation R30 exists to prevent.

**Widening `SanitizeDisplayString`** loses on the mirror obligation in F2 and on
the mode-flag it would need to serve two different jobs. The C0 gap it does have
is a real bug worth its own issue.

## Risks

- **The excerpt is a summary, and a reviewer may read R35 strictly.** The design
  should say plainly that R35 is satisfied at the level of extent plus identity,
  not byte-for-byte reproduction, and rest that on R30's own concession that what
  is rendered does not equal what is sent. If a jury insists on byte-for-byte,
  the requirement and R37 cannot both be met and the PRD needs the contradiction
  resolved, not the design.
- **First and last line are a heuristic.** A paste whose identifying content is
  in the middle — a single deep frame in an otherwise uniform trace — reads as
  ambiguous. Mitigated by the byte and line counts, not solved.
- **Bidi and homoglyph content is not neutralized.** `U+202E` passes through
  (verified) and reverses the rest of its excerpt row. Escaping Unicode format
  characters would make ordinary non-ASCII logs unreadable, and the blast radius
  is one truncated row of a transcript — it cannot reach argv, which is preserved
  verbatim by design. Worth a sentence in the design, not a rule.
- **`\b \b` at a wrap boundary** leaves a stale character when backspacing across
  a wrapped line. Fixing it means tracking width and wrap position, which
  reintroduces everything append-only rendering avoids. Accept and document.
- **Rune-count truncation is not column-count truncation.** A wide-character
  excerpt can wrap one extra row. Harmless; append-only rendering does not care.
- **The manual criteria are the only check on any of this.** "A large paste
  renders without visible corruption" cannot be automated in this repository, per
  the PRD's own Known Limitations. The invariant in the neutralization rule is
  the part that *is* automatable, and the design should lean on it hard, because
  it is the only mechanical guard.
- **Terminals with their own large-paste dialogs** (Windows Terminal, iTerm2,
  GNOME Terminal) interrupt before any byte reaches niwa. Nothing the rendering
  model can do; noted so the manual criteria are not read as niwa failures.

## Summary

Rendering should be an append-only transcript that emits one bounded record per
input event — verbatim neutralized echo for reads of 64 bytes or fewer, and for
a pasted block a count line plus its first and last line, width-truncated — so
that the developer gets both the extent and the identity of what they pasted at
O(width) cost rather than O(payload), which is what reconciles R35 with R37 once
you accept that full echo fails not on speed but on scrolling the failure away
and on making R36's deletion require a line editor. Neutralization escapes rather
than strips: TAB passes, every other C0 byte and DEL becomes caret notation, C1
controls and invalid UTF-8 become `\xNN`, and the resulting invariant — nothing
emitted moves the cursor up, left, or to column 0 — is both testable and
strictly stronger than the ANSI-stripping `SanitizeDisplayString` does today,
which was verified to leave CR, BS, BEL and 8-bit CSI intact and which should be
left alone because it is a three-way synced copy shared with tsuku. R27 is
satisfied by a two-line all-printable banner naming the command, the submit and
cancel gestures, and the 130433-byte limit, which survives escape-stripping
trivially because it contains no escape bytes to begin with.
