# Lead: What would a paste-aware inline prompt cost inside niwa today?

## Findings

### 1. There is no TUI framework. There is a hand-rolled 195-line picker.

`internal/tui/picker.go` is the entire TUI surface. It is not built on bubbletea,
tview, or anything else — it is raw ANSI written by hand:

- `picker.go:23` imports exactly one non-stdlib package: `golang.org/x/term`.
- `picker.go:79-88` sets raw mode via `term.MakeRaw` on the **stderr** fd (not
  stdin), with `defer term.Restore`.
- `picker.go:91-92` hides/shows the cursor with literal `"\x1b[?25l"` /
  `"\x1b[?25h"` writes.
- `picker.go:97-121` is a 3-byte read loop with hand-written escape matchers
  (`isUpArrow` at `picker.go:176`, `isDownArrow` at `:182`, `isEnter` at `:187`,
  `isCtrlC` at `:191`).
- The file carries a provenance header (`picker.go:1-14`) saying it is a
  byte-equivalent copy of `tsukumogami/tsuku@c8f58101 internal/tui/picker.go`,
  maintained by hand because tsuku's `internal/` blocks cross-module import.
  **Anything added to `internal/tui` inherits a mirroring obligation** or must
  explicitly document the divergence.

`tui.IsAvailable()` (`picker.go:44-46`) is just `term.IsTerminal(os.Stderr.Fd())`.

### 2. go.mod: zero new dependencies needed — x/term already ships bracketed paste

`go.mod:5-10` lists four direct requires: BurntSushi/toml, cucumber/godog,
spf13/cobra, and **`golang.org/x/term v0.42.0`**. `grep -ic "bubbletea|charmbracelet|bubbles" go.sum` returns **0**. There is no
Charm anything, vendored or indirect.

The important discovery: **`golang.org/x/term` v0.42.0 already implements
bracketed paste end to end.** In the module cache at
`golang.org/x/term@v0.42.0/terminal.go`:

- `terminal.go:983-987` — `func (t *Terminal) SetBracketedPasteMode(on bool)`
  writes literally `"\x1b[?2004h"` / `"\x1b[?2004l"`. DECSET 2004 is a one-call
  API we already have.
- `terminal.go:172-173` — `pasteStart = ESC [ 2 0 0 ~`, `pasteEnd = ESC [ 2 0 1 ~`.
- `terminal.go:248-253` — the decoder recognizes both markers and flips
  `t.pasteActive`.
- `terminal.go:972-976` — `ErrPasteIndicator` is returned from `ReadLine`
  alongside valid line data when the line consisted only of pasted bytes.
- `terminal.go:131` — `NewTerminal(c io.ReadWriter, prompt string) *Terminal`,
  plus `SetPrompt` (`:885`) and `SetSize` (`:913`).

So the leading candidate (DECSET 2004, paste arrives as one unit) is buildable
with **no new module, no go.mod change, no go.sum churn.**

### 3. But x/term's Terminal is line-at-a-time, and pasted newlines terminate lines

This is the load-bearing constraint and it contradicts the naive reading of
"a pasted block arrives as one unit and a single Enter submits."

`terminal.go:508-512`:

```go
func (t *Terminal) handleKey(key rune) (line string, ok bool) {
	if t.pasteActive && key != keyEnter && key != keyLF {
		t.addKeyToLine(key)
		return
	}
	switch key {
```

A CR or LF **inside** an active paste is *not* added to the line — it falls
through to `case keyEnter, keyLF:` (`terminal.go:579-588`) which returns the line
with `ok = true`. So `ReadLine()` returns once per line of the pasted block, not
once for the whole block.

What makes it still workable is `lineIsPasted` (`terminal.go:808`, `:829`,
`:861-863`): every line produced while `pasteActive` is true comes back with
`err == ErrPasteIndicator`. The reconstruction algorithm is therefore:

> loop `ReadLine()`; accumulate lines whose err is `ErrPasteIndicator`; the first
> line returned with `err == nil` is the user's typed submit and ends input.

That algorithm falls out cleanly for both required cases:

- **Bare pasted log.** If the pasted selection ends with a trailing newline
  (normal when selecting whole lines), every content line returns
  `ErrPasteIndicator`, and the user's single Enter afterward returns an empty
  line with `err == nil` → submit. One Enter, as designed.
- **Annotated log.** If the paste has no trailing newline, `keyPasteEnd` clears
  `pasteActive` (`terminal.go:836`) while the final fragment is still in
  `t.line`. The user then types their instruction, which appends to that same
  line, and Enter submits it with `err == nil`. This works, but note the
  annotation lands *concatenated onto the last pasted line*, not on its own line.
  Whether that is acceptable is a UX decision, not a technical blocker.

### 4. niwa's interactive-input conventions a new prompt must follow

Four call sites define the house style, and they are not fully consistent:

| Site | TTY gate | Reader source | Writer |
|---|---|---|---|
| `destroy.go:116-120` | `IsStdinTTY()` | `os.Stdin` (direct) | `cmd.ErrOrStderr()` |
| `destroy.go:239-252` | `IsStdinTTY() && tui.IsAvailable()` | `os.Stdin` (via `tui.Pick`) | stderr (via `tui.Pick`) |
| `destroy.go:325` | `IsStdinTTY()` | — | — |
| `init.go:290-300` | `IsStdinTTY()` | `cmd.InOrStdin()` | `cmd.ErrOrStderr()` |

The conventions that are consistent and must be honored:

1. **`IsStdinTTY()` is a rebindable package var** — `prompt.go:26-28`, with an
   explicit comment at `:24-25` that it must read `term.IsTerminal` at call time,
   never captured at init, because tests rebind `os.Stdin`.
2. **Non-TTY is a hard, actionable error, never a silent skip.** `destroy.go:117`
   and `init.go:292-296` both return errors that name the flag to use instead.
   A dispatch prompt must do the same: no TTY → tell the user to pass the prompt
   as an argument.
3. **Prompts go to stderr, results to stdout.** `destroy.go:120` writes the
   confirmation prompt to `cmd.ErrOrStderr()`; `destroy.go:135` writes the result
   to `cmd.OutOrStdout()`.
4. **Cancel is a distinct sentinel.** `tui.ErrCanceled` (`picker.go:30`) with the
   documented contract "print Canceled. and exit non-zero without mutating
   state", honored at `destroy.go:252-257`.
5. **Every primitive splits into an exported wrapper + a testable core with
   injected reader/writer.** `Pick` → `pick(stdin io.Reader, stderr io.Writer, ...)`
   at `picker.go:64-75`; `ReadConfirmation(prompt, expected string, in io.Reader, out io.Writer)`
   at `prompt.go:42`; `promptBootstrap(in io.Reader, out io.Writer)` at `init.go:313`.
   This seam is non-negotiable — it is how all three existing prompts are tested.

A wrinkle: `x/term.NewTerminal` takes a single `io.ReadWriter`, not a separate
reader and writer. Satisfying convention (5) means a small adapter struct pairing
an `io.Reader` with an `io.Writer`. Trivial, but it is new code with no precedent
in this repo.

### 5. How interactive input is tested today — two layers, both already built

**Unit layer.** `internal/tui/picker_test.go` is 218 lines and drives `pick()`
with `bytes.NewReader` of raw key bytes against a `bytes.Buffer` stderr — no PTY,
no terminal. `picker_test.go:41` feeds `{'\x1b','[','B','\r'}` directly.
`picker_test.go:203-217` defines a `chunkedReader` that caps each `Read` to N
bytes so the reader's per-iteration buffer sees one escape sequence at a time —
**exactly the harness a bracketed-paste reader needs**, since `ESC[200~` is 6
bytes and must be fed as a unit or across a split to test both. `picker.go:79`
guards the raw-mode branch on `stderr.(*os.File)`, so a `bytes.Buffer` skips
`MakeRaw` entirely and the logic is exercised offline.

`internal/cli/init_bootstrap_test.go:80-86` gives the TTY stub helper:

```go
prev := IsStdinTTY
IsStdinTTY = func() bool { return isTTY }
t.Cleanup(func() { IsStdinTTY = prev })
```

**Functional layer.** `test/functional/` runs godog against the real binary. The
step `^I run "([^"]*)" under a pty with input "([^"]*)"$` is registered at
`suite_test.go:365` and implemented at `steps_init_bootstrap_test.go:143-199`. It
drives the binary under util-linux `script -q -c <cmd> /dev/null` to get a real
PTY so the child's `IsStdinTTY()` returns true. Used today at
`init_bootstrap_failures.feature:53` and `:61`.

The comment at `steps_init_bootstrap_test.go:139-142` is directly relevant to
this exploration:

> `script` is the POSIX util-linux command; it ships on every Linux CI image and
> on macOS via Homebrew. Adding a Go pty library (github.com/creack/pty) was
> considered and rejected to avoid a new dependency.

So there is an established, dependency-free PTY test path. One gap: the input
escape decoder at `steps_init_bootstrap_test.go:181` handles only `\n`:

```go
rawInput := strings.ReplaceAll(input, `\n`, "\n")
```

Feeding `ESC[200~...ESC[201~` from a feature file needs an `\e` (or `\x1b`)
escape added there — a one-line change.

`niwa dispatch` already has functional coverage (`test/functional/features/dispatch.feature`,
steps in `test/functional/dispatch_steps_test.go` with a fake `claude` on PATH),
so a paste scenario has somewhere to live and a fake worker to assert the
captured prompt against.

### 6. `internal/tui/sanitize.go` — relevant, and it exposes a live problem

19 lines. `sanitize.go:7-11` compiles a regexp matching CSI sequences, OSC
sequences, and bare ESC; `SanitizeDisplayString` (`:17-19`) strips them. Its
documented purpose (`:13-16`) is to prevent an externally-sourced string from
repositioning the cursor or overwriting the picker frame. It is applied to every
caller-supplied string at `picker.go:139`, `:145`, `:146`, and tested at
`picker_test.go:155-179`.

This matters here because **pasted terminal logs routinely contain real ANSI
color codes** — that is the whole point of "an error scrolled past and I selected
it." And x/term does *not* filter them:

- During paste, `bytesToKey` skips its entire control-character switch
  (`terminal.go:183` — `if !pasteActive {`), so a raw `0x1b` decodes as an
  ordinary rune.
- `handleKey` (`terminal.go:509-511`) adds it to the line via `addKeyToLine`.
- `addKeyToLine` (`terminal.go:663-678`) echoes unconditionally via
  `writeLine` → `queue` (`terminal.go:270-272`) with **no `isPrintable` filter**.
  `isPrintable` (`terminal.go:276-280`) is only consulted by `visualLength` for
  cursor arithmetic.

Net effect: pasting a colorized log into an x/term `Terminal` echoes the escape
bytes straight back to the terminal, and the cursor-position math treats them as
zero-width. This is precisely the class of thing `SanitizeDisplayString` was
written to stop. Two separate decisions fall out — sanitize the **echo** (yes,
almost certainly, to protect the frame) and sanitize the **stored prompt** (much
less obvious; stripping color codes from a log the user wants an agent to read is
arguably fine, arguably lossy). They should not be conflated.

### 7. Implementation surface, honestly estimated

**Changes to `internal/cli/dispatch.go`:**
- `Args: cobra.ExactArgs(1)` → `cobra.MaximumNArgs(1)` (line `131`).
- `runDispatch` line `138` `prompt := args[0]` becomes a branch: zero args →
  TTY-gate and call the reader; non-TTY zero-args → error naming the positional
  form. ~20 lines.
- `Long:` help text (`:115-130`) needs a paragraph. The `Use:` string
  `"dispatch <prompt>"` becomes `"dispatch [prompt]"`.

**New file — the reader.** Roughly 120-160 lines including this repo's very heavy
doc-comment density (picker.go is 195 lines for ~90 lines of logic;
dispatch_capture.go is 132 lines for ~60). Content: the io.ReadWriter adapter,
`MakeRaw` + deferred `Restore`, `SetBracketedPasteMode(true)` + deferred
`(false)`, the `ReadLine`/`ErrPasteIndicator` accumulation loop, cancel handling,
and an exported wrapper over an injectable core.

Placement is a real decision. `internal/tui/` is the natural home by subject
matter but carries the mirror-with-tsuku obligation from `picker.go:8-14`.
`internal/cli/` is niwa-local and unencumbered. **Do not name it
`dispatch_capture.go`** — that name is taken (`internal/cli/dispatch_capture.go`)
and means session-UUID capture by jobs-dir cwd correlation, an entirely different
thing.

**New test file.** ~150-200 lines, modeled directly on `picker_test.go` including
its `chunkedReader`.

**Functional.** One `\e` escape in `steps_init_bootstrap_test.go:181`, plus a
scenario in `dispatch.feature` (~15 lines) asserting the fake claude received the
multi-line prompt (`dispatch_steps_test.go` already writes launch argv to
`$HOME/dispatch-launch-argv`).

**Docs.** This repo has a PRD and a design doc for essentially every feature
(`docs/prds/` holds 32; `docs/designs/current/`). Convention implies both.

**Total: roughly 400-500 lines of new Go, zero new dependencies.** The genuinely
new logic is small; the bulk is doc comments and tests, matching house style.

Reused wholesale: `golang.org/x/term` (already direct, `go.mod:9`), the
`IsStdinTTY` seam (`prompt.go:26`), `tui.IsAvailable` (`picker.go:44`),
`tui.ErrCanceled` semantics (`picker.go:30`), the raw-mode/defer-restore idiom
(`picker.go:79-92`), `SanitizeDisplayString` (`sanitize.go:17`), the injected
reader/writer test pattern (`picker_test.go`), and the `script`-based PTY
functional harness (`steps_init_bootstrap_test.go:143`).

## Implications

The cost is low and the risk is concentrated in UX, not engineering. Bracketed
paste needs no new dependency — `x/term` v0.42.0, already a direct require, has
`SetBracketedPasteMode`, the `ESC[200~`/`ESC[201~` decoder, and
`ErrPasteIndicator`. That removes the biggest anticipated objection.

The design must be written against `ReadLine`-returns-per-line, not
`one-paste-one-read`. The `ErrPasteIndicator` accumulation loop makes the
intended UX achievable, but it is a loop with real edge cases (trailing newline
or not; annotation concatenating onto the last pasted line), and those edges
should be specified explicitly rather than discovered during implementation.

Placement in `internal/tui` versus `internal/cli` should be decided deliberately
because `internal/tui/picker.go` carries a documented obligation to stay
byte-equivalent with tsuku's copy. A niwa-only paste reader added there either
breaks that invariant or forces a matching tsuku change.

## Surprises

**`x/term` already does bracketed paste.** The exploration framing implied this
might need bubbletea/bubbles. It does not. `SetBracketedPasteMode` at
`terminal.go:983` writes DECSET 2004 directly, and the decoder has been there all
along.

**A pasted newline terminates `ReadLine`.** `handleKey` at `terminal.go:509`
explicitly exempts Enter/LF from the paste-passthrough. "The paste arrives as one
unit" is true at the *marker* level (`pasteActive` spans the whole block) but
false at the `ReadLine` level. This directly qualifies a scope assumption.

**Ctrl-C inside `x/term.ReadLine` returns `io.EOF`, not a distinct sentinel**
(`terminal.go:825-827`), conflating deliberate cancel with a closed stdin. niwa's
own picker went out of its way to separate these (`tui.ErrCanceled`,
`picker.go:26-30`). Matching the house contract requires detecting `0x03` before
handing bytes to `Terminal`, or accepting a behavioral inconsistency between the
picker and the new prompt.

**`maxPromptBytes` is off by one, and paste is what makes it reachable.**
`dispatch.go:81` sets `maxPromptBytes = 128 * 1024` (131072) and `:144` rejects
only `len(prompt) > maxPromptBytes`. The prompt is passed as a single argv element
(`dispatch_launcher.go:66-72`), and Linux caps a single argv string at
`MAX_ARG_STRLEN` = 131072 *including* the NUL. I verified this empirically:

```
131071 OK
131072 FAIL [Errno 7] Argument list too long
131073 FAIL [Errno 7] Argument list too long
```

So a 131072-byte prompt passes niwa's validation and then dies at exec with an
opaque `E2BIG` — the exact outcome the comment at `dispatch.go:75-81` says it
exists to prevent. Compounding it, `dispatch.go:327` prepends
`keepAliveArmingInstruction` to `prompt` *after* the check at `:144`, so an
in-bounds prompt can be pushed out of bounds. Nobody types a 128 KB argument;
pasting a log file is how you get there. Also note the comment's claim that
"ARG_MAX is at least 4096" conflates `ARG_MAX` (total) with `MAX_ARG_STRLEN`
(per-string) — the per-string limit is the binding one here.

**`picker.go` calls `MakeRaw` on the stderr fd, not stdin** (`picker.go:79-88`)
while reading from `os.Stdin` (`:68`). It works because both usually point at the
same tty, but a new reader should decide deliberately which fd it puts into raw
mode rather than copying this.

## Open Questions

- **Placement:** `internal/tui` (subject-matter fit, but inherits the
  mirror-with-tsuku obligation from `picker.go:8-14`) or `internal/cli`
  (unencumbered)? Needs a human call.
- **Annotation placement:** when a paste lacks a trailing newline, the typed
  instruction concatenates onto the last pasted line. Acceptable, or does the
  prompt need to force a line break at `keyPasteEnd`?
- **Sanitization policy:** strip ANSI from the echo (near-certainly yes), from the
  stored prompt (unclear — color codes in a log may be noise or may be signal for
  the agent). Two decisions, currently one word.
- **Cancel semantics:** re-implement `tui.ErrCanceled` by intercepting `0x03`
  before `Terminal`, or accept `io.EOF` conflation?
- **Terminals without DECSET 2004 support:** `SetBracketedPasteMode` writes the
  sequence unconditionally with no capability probe. What is the fallback when
  markers never arrive — does every line just submit immediately?
- **Should `maxPromptBytes` be fixed as part of this work,** or filed separately?
  It is a pre-existing bug, but paste is the feature that makes it hit.

## Summary

niwa has no TUI framework — just a hand-rolled 195-line ANSI picker over
`golang.org/x/term` — but that already-vendored direct dependency (v0.42.0)
ships full bracketed-paste support: `SetBracketedPasteMode` writes DECSET 2004 at
`terminal.go:983`, and `ErrPasteIndicator` marks pasted lines, so an inline paste
prompt costs roughly 400-500 lines of new Go and **zero new dependencies**, with
established seams (`IsStdinTTY`, injected reader/writer, the `script`-based PTY
harness) covering testability. The main correction to the exploration's framing
is that `x/term.ReadLine` returns once *per line* — a pasted newline terminates
the line at `terminal.go:509` — so the design must be an `ErrPasteIndicator`
accumulation loop rather than a single read, which still delivers the intended
one-Enter UX but has real edges around trailing newlines and where an annotation
lands. The biggest open question is whether the new prompt goes in `internal/tui`
(natural fit, but `picker.go:8-14` obliges byte-equivalence with tsuku's copy) or
`internal/cli`; a close second is that `maxPromptBytes` is off by one against
Linux's 131072-byte `MAX_ARG_STRLEN` (verified empirically) and paste is exactly
what makes that latent bug reachable.
