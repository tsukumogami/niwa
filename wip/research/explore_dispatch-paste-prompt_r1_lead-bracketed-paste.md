# Lead: How does bracketed paste actually behave in practice, and what does a Go program need to do to consume it reliably?

## Findings

### 1. The protocol: what is sent, by whom, and when

Bracketed paste is DEC private mode 2004. Four sequences, and only four:

| Sequence | Bytes | Direction | Meaning |
|---|---|---|---|
| DECSET 2004 | `ESC [ ? 2 0 0 4 h` (`\x1b[?2004h`) | app -> terminal | enable |
| DECRST 2004 | `ESC [ ? 2 0 0 4 l` (`\x1b[?2004l`) | app -> terminal | disable |
| paste start | `ESC [ 2 0 0 ~` (`\x1b[200~`) | terminal -> app | next bytes are pasted |
| paste end | `ESC [ 2 0 1 ~` (`\x1b[201~`) | terminal -> app | paste over |

The framing is exactly `\x1b[200~` + payload + `\x1b[201~`, with no length prefix, no
checksum, and no acknowledgement. Both markers are 6 bytes.
([xterm ctlseqs / xterm-paste64](https://invisible-island.net/xterm/xterm-paste64.html),
[Wikipedia](https://en.wikipedia.org/wiki/Bracketed-paste),
[cirw.in](https://cirw.in/blog/bracketed-paste))

Critical property: **the mode is terminal-side state, set by whichever program currently
owns the tty.** The terminal sends the markers only while some program has enabled the
mode. Enabling is a write to the tty (stdout or stderr, either works — it goes to the
same device); it is not a termios setting and `stty` cannot see it.

Origin is worth knowing because it shapes the semantics: xterm added it in 2002 as a
user contribution so Emacs could turn off auto-indent during a paste. It was never
designed as a security boundary, and Thomas Dickey has said so explicitly — "the fix
for this is to not paste into a command-interpreter from an unfiltered source."

### 2. Confirmed empirically: bash turns the mode OFF before running your command

I ran an interactive bash under `script` and grepped the emitted control bytes
(`$CLAUDE_JOB_DIR/tmp/out.raw`). The byte stream around each command is:

```
\x1b[?2004h  <prompt>  cat /dev/null \r
\x1b[?2004l  \r  \x1b[?2004h  <prompt>  exit \r
\x1b[?2004l  \r  exit
```

Readline enables 2004 when it takes the terminal for a prompt and disables it before
handing the terminal to the child process. So the scope brief's claim is correct and now
has direct evidence: **when `niwa dispatch` starts, bracketed paste is OFF.** A bare `cat`
genuinely never sees the markers. niwa must enable the mode itself, and must disable it
again on the way out.

Readline enables `enable-bracketed-paste` by default as of readline 8.1 / bash 5.1
(2020-2021); before that it was opt-in via `~/.inputrc`. Distros can also flip the
default at build time with `--disable-bracketed-paste-default`, so "modern bash" is not
a guarantee — but it does not matter for us, since niwa sets the mode itself and does not
inherit bash's.
([Chris Siebenmann](https://utcc.utoronto.ca/~cks/space/blog/unix/BashBracketedPasteChange),
[qlyoung](https://qlyoung.net/blog/gnu-readline-bracketed-paste.html))

### 3. Multiplexers and ssh

**ssh is transparent.** It is a byte pipe; the sequences ride through untouched in both
directions. Nothing to configure. (The clipboard lives on the *local* machine, so a paste
into a remote session is still generated locally and framed by the local terminal — the
whole flow is local-terminal -> ssh -> remote app.)

**tmux fully supports it, and gates the pass-through on the inner app.** From tmux source:

- `tty-keys.c` `tty_default_raw_keys[]` maps `"\033[200~"` -> `KEYC_PASTE_START` and
  `"\033[201~"` -> `KEYC_PASTE_END`. On start it sets `tty->flags |= TTY_BRACKETPASTE`;
  on end it clears it. The payload between them is buffered in `tty->in` and delivered as
  one `key_event` to `server_client_handle_key()`.
- `input-keys.c` re-emits the markers to the pane **only if the pane's application has
  enabled mode 2004**:
  ```c
  if (KEYC_IS_PASTE(key) && (~s->mode & MODE_BRACKETPASTE))
          return (0);
  ```
  tmux tracks `MODE_BRACKETPASTE` per pane (per `screen`), so a paste into a pane running
  a program that did not opt in arrives unbracketed — which is the correct behavior.
- tmux's own buffers are separate: `paste-buffer -p` brackets the buffer contents, again
  only if the pane's app asked for it
  ([tmux commit f4fdddc](https://github.com/tmux/tmux/commit/f4fdddc9306886e3ab5257f40003f6db83ac926b)).

Notable tmux detail found in the same source: when tmux sees a *partial* paste-end
sequence it raises its escape-time delay to at least 500ms (`if (delay < 500) delay = 500`)
to wait for the rest. That is a strong hint that split markers are common enough in
practice that tmux special-cases them.

**GNU screen is the weak link.** Support was proposed by Hayaki Saito in March 2013 on
screen-devel and landed only much later; older screen swallows DECSET 2004 and never
emits markers. The terminfo entries on this machine corroborate the split:

```
screen           (no BD/BE)
screen-256color  (no BD/BE)
tmux-256color    BD=\E[?2004l  BE=\E[?2004h
alacritty        BD=\E[?2004l  BE=\E[?2004h
xterm-256color   BD=\E[?2004l  BE=\E[?2004h  PE=\E[201~  PS=\E[200~
xterm-kitty      (no BD/BE)
```

Note `xterm-kitty` has no BD/BE even though kitty absolutely supports bracketed paste.
**terminfo BD/BE is not a usable capability probe** — it produces false negatives.

### 4. Terminals without support, and what happens if you enable it anyway

The failure is benign in the direction that matters. DECSET of an unrecognized private
mode is ignored by every conformant terminal — no error, no echo, no garbage. The paste
then simply arrives **unbracketed**: raw bytes with `\r` line separators and no framing.
So enabling 2004 costs nothing on an unsupporting terminal; you just get no delimiters.

Support today is close to universal: xterm, all VTE terminals (gnome-terminal, since
VTE 0.23.3), urxvt, kitty, alacritty, WezTerm, Ghostty, iTerm2, macOS Terminal.app,
mintty, foot, VS Code's integrated terminal, and Windows Terminal (support added via
[microsoft/terminal PR #9034](https://github.com/microsoft/terminal/pull/9034), shipping
around 2021, solid by 1.17). PuTTY supports it and has sanitised control characters out
of pastes since 0.71 (2019).

Real gaps: GNU screen below the patch, pre-2005 xterm, VTE < 0.23.3, older Terminator
builds, and the legacy Windows conhost console. In each case, as above, you get an
unbracketed paste rather than corruption.

**The garbage case is the opposite failure and it is a state leak, not a missing-support
problem.** If a program enables 2004 and dies without sending `\x1b[?2004l` — crash,
SIGKILL, `os.Exit` past a `defer` — the terminal stays in bracketed-paste mode. The next
program to own the tty receives `\x1b[200~` it does not understand, and a naive consumer
renders the tail as literal `00~` before the pasted text. This is exactly the widely
reported "my terminal prepends 00~ when pasting" symptom, and Claude Code has an open
issue for its own instance of it
([anthropics/claude-code#39272](https://github.com/anthropics/claude-code/issues/39272),
[shivankaul.com](https://shivankaul.com/blog/paste-bracketing-iterm2)).

### 5. Distinguishing pasted newlines from typed ones — and the failure modes

The distinction is purely positional: a `\r`/`\n` between `\x1b[200~` and `\x1b[201~` is
pasted; one outside is typed. That is the entire mechanism, which is why the parser's
correctness *is* the feature.

Five concrete failure modes, all with evidence:

**(a) Markers split across `read()` boundaries.** The 6-byte marker can straddle two
reads. A parser with per-read state, or one that only recognises a marker when all 6
bytes are contiguous in the current buffer, will miss it. tmux's 500ms escape-time bump
exists for precisely this. niwa's existing picker (`internal/tui/picker.go:99`) reads into
`buf := make([]byte, 3)` — that buffer is physically too small to ever hold a paste
marker, so it is a precedent to *not* copy.

**(b) The terminating Enter arriving in the same chunk as the paste end.** Some terminals
and paste modes (iTerm2's "paste with newline", Terminal.app's "paste newlines as
carriage returns") deliver `\x1b[201~\r` in one PTY write. A parser that ends the paste
and then discards the rest of the chunk drops the submit key. This is a filed bug against
Cursor CLI: ["does not submit a bracketed-pasted prompt when Enter arrives in the same
PTY input chunk"](https://forum.cursor.com/t/cursor-cli-does-not-submit-a-bracketed-pasted-prompt-when-enter-arrives-in-the-same-pty-input-chunk/166674).
Directly relevant, because "paste then press Enter once" is our target UX.

**(c) Large pastes overrunning the input path.** Claude Code
[#50012](https://github.com/anthropics/claude-code/issues/50012): 500+ line paste on
Windows Terminal + PowerShell yields only the last 1-3 lines, and no paste placeholder —
the markers are lost mid-stream under throughput, diagnosed as raw-stdin buffer overflow
above ConPTY. Claude Code [#34529](https://github.com/anthropics/claude-code/issues/34529)
is a PTY-layer freeze on paste. Gemini CLI
[#25998](https://github.com/google-gemini/gemini-cli/issues/25998) hangs its UI on a 5 KiB
paste. The lesson is that the *reader* must drain fast and cheaply and must not do
per-character rendering work.

Conversely, bracketing *helps* throughput: kitty
[#5869](https://github.com/kovidgoyal/kitty/issues/5869) reports unbracketed pastes over
1024 bytes dropping/repeating characters on macOS, a problem the bracketed path does not
have because the terminal can stream it as one framed unit.

**(d) A paste containing its own end marker.** This is the security case. Daniel
Colascione demonstrated it against kitty in June 2018 by splitting `\033[201~` across the
clipboard content; kitty's single-pass strip did not remove it and the shell executed the
remainder. Fix commit: "More robustly strip bracketed paste termination sequence." xterm's
mitigation is `allowPasteControls` (patch #292, default off, permitting only printable
chars plus newline/tab/formfeed) and `disallowedPasteControls` (patch #333). Terminals are
*supposed* to filter ASCII controls 0-8, 11, 12, 14-31 from pasted data — which strips ESC
(27) and therefore makes an embedded marker impossible — but this is per-terminal policy,
configurable, and historically has been gotten wrong. **The consumer must assume a
malicious or merely unlucky paste can contain `\x1b[201~` and must not treat that as
authorization to run anything.** For niwa the blast radius is small: the payload becomes a
prompt string, not a shell command. But an embedded end marker would truncate the captured
prompt and dump the remainder onto the command line, which is a real correctness and
surprise problem.

**(e) CR vs LF.** Terminals do not agree. Most send `\r` (0x0D) for newlines in a paste,
since that is what Enter sends; macOS Terminal.app has an explicit "Paste newlines as
carriage returns" advanced setting, on by default. In raw mode `ICRNL` is off, so the app
sees the raw byte. Ghostty
[discussion #9592](https://github.com/ghostty-org/ghostty/discussions/9592) documents apps
collapsing multi-line pastes to one line from mishandling this. **The consumer must
normalize CR and CRLF to LF itself.**

### 6. Go library landscape

**`golang.org/x/term` — already a direct dependency of niwa (`go.mod`, v0.42.0) — has a
complete bracketed-paste implementation.** This is the most important practical finding.
In `terminal.go`:

- `func (t *Terminal) SetBracketedPasteMode(on bool)` writes `\x1b[?2004h` / `\x1b[?2004l`.
- `pasteStart = []byte{ESC,'[','2','0','0','~'}` / `pasteEnd = ...'2','0','1','~'` and a
  `pasteActive bool` on the Terminal.
- `bytesToKey(b []byte, pasteActive bool)` gates *every* key-decoding branch on
  `!pasteActive`, so inside a paste Ctrl-A, arrows, Ctrl-C, Ctrl-D and friends are treated
  as literal data. Its test asserts this: input `"abc\x1b[200~de\177f\x1b[201~\177\r"`
  yields the line `"abcde\177"` — the DEL inside the paste survives verbatim, the one
  outside deletes.
- `readLine()` handles split sequences correctly: `bytesToKey` returns `utf8.RuneError`
  for a partial sequence, the leftover is kept in `t.remainder` (aliased into
  `inBuf [256]byte`) and the next `Read` appends after it. Failure mode (a) is solved.
- `ErrPasteIndicator` is returned as the *error* alongside a valid line when that whole
  line was pasted.

The semantics matter for our design and are non-obvious: **`ReadLine` still returns one
line per newline even inside a paste** (`handleKey` early-returns for everything except
`keyEnter`/`keyLF`). A 40-line paste is 40 `ReadLine` calls, the first 39 returning
`ErrPasteIndicator`. There is a neat consequence: once `\x1b[201~` arrives, `pasteActive`
goes false, so the user's terminating Enter produces a final line with `err == nil`.
"Accumulate while `err == ErrPasteIndicator`, stop on `err == nil`" is almost exactly the
loop the target UX wants, and it naturally supports a typed annotation mixed with a pasted
block.

Two caveats on `x/term.Terminal`:
- It is a full echoing line editor with history and repainting. The large-paste hangs in
  (c) are precisely this class of per-character render cost. It repaints per key.
- A latent stall: `readBuf := t.inBuf[len(t.remainder):]` — if `remainder` ever fills all
  256 bytes, `Read` gets a zero-length buffer and spins. `bytesToKey`'s unknown-sequence
  fallback scans for the first `[a-zA-Z~]` and returns `RuneError` with the *whole* buffer
  as remainder if none is found, so an ESC followed by 250+ bytes containing no letter
  could get there. Terminals strip ESC from pastes by default, so this is a corner, but it
  is a corner in a stdlib-adjacent dependency, not something we control.

Other Go options, for completeness:
- **bubbletea** — bracketed paste is on by default, disabled with `WithoutBracketedPaste()`,
  runtime toggles `EnableBracketedPaste()` / `DisableBracketedPaste()`. v1 surfaces it as
  `tea.KeyMsg` with `.Paste == true`; v2 renamed it to a distinct `tea.PasteMsg`.
  `bubbles/textinput` and `bubbles/textarea` both handle it. This is the most complete
  implementation, but it is a large new dependency tree for a workspace CLI that currently
  ships four direct deps and hand-rolls its picker.
- **chzyer/readline, peterh/liner** — no bracketed-paste handling found. Both predate the
  feature's ubiquity.
- **reeflective/readline** — modern, handles it, but again a heavy dependency.

### 7. Terminal state on abnormal exit, and the restoration discipline

Two independent pieces of state get modified and both must be restored:

1. **termios** (raw mode). Restored with `term.Restore(fd, oldState)`.
2. **DEC private mode 2004** (and cursor visibility, alt screen, mouse reporting if used).
   Restored only by writing `\x1b[?2004l`. `stty sane` does **not** clear it — it is
   terminal-side, not termios. `reset` clears it but re-initializes everything, which is
   heavy-handed. The minimal repair a user can run is
   `printf '\e[?2004l'`.

The standard discipline, in order:

- `defer term.Restore(...)` plus `defer` writing `\x1b[?2004l`, deferred *before* the
  corresponding enable so unwinding is LIFO-correct. This covers normal return and panic.
  niwa's picker already models exactly this shape at `internal/tui/picker.go:81-92`.
- **`defer` does not cover signals.** In raw mode `ISIG` is off, so Ctrl-C arrives as byte
  0x03 rather than SIGINT and the program handles it in-band. But SIGTERM, SIGHUP (terminal
  window closed), and SIGQUIT still kill the process with defers unrun. A `signal.Notify`
  handler that restores and then re-raises with the default disposition is required for a
  correct implementation. niwa's picker does *not* do this today.
- `os.Exit` anywhere in the call path skips all defers. So does SIGKILL and a hard panic in
  another goroutine that races the unwind. Nothing can fully close the SIGKILL hole; the
  mitigation is to keep the raw-mode window as short as possible.
- Belt-and-braces used by several TUIs: emit the full restore string
  (`\e[?2004l\e[?1l\e[?25h`) unconditionally on exit rather than tracking whether the mode
  was enabled — disabling an already-disabled mode is a no-op.

There is one saving grace specific to our case: after `niwa dispatch` exits, bash's next
prompt sends `\x1b[?2004h` anyway, so a leaked *enabled* state is self-healing under
readline shells. It is not self-healing for a user who exits into a non-readline consumer,
or under fish/zsh configurations that differ, so it is not an excuse to skip the teardown.

## Implications

1. **Bracketed paste is viable and the mechanism is sound.** Support is effectively
   universal in 2026, ssh is transparent, tmux forwards it correctly and gates on the inner
   app's opt-in, and the degraded case on an unsupporting terminal is a clean "no markers"
   rather than corruption. The "paste, then press Enter once" UX is achievable.

2. **niwa can build this with zero new dependencies.** `golang.org/x/term` v0.42.0 is
   already a direct dep and already implements the protocol, the split-sequence buffering,
   the "control chars are literal inside a paste" rule, and an `ErrPasteIndicator` signal.
   That materially lowers the cost of the interactive-paste path relative to adopting
   bubbletea.

3. **The design must not assume markers will arrive.** Every code path needs a defined
   behavior for the unbracketed case (old screen, legacy conhost, a pipe). The natural
   fallback given the scope constraints is a sentinel line or Ctrl-D, used only when no
   paste-start was ever seen. Detection is easiest done *lazily* — enable the mode, and if
   a paste-start shows up, use the bracketed path — rather than probing. A DECRQM probe
   (`ESC [ ? 2004 $ p`, reply `ESC [ ? 2004 ; 1 $ y`) exists but needs a read timeout, is
   not universally implemented, and terminfo BD/BE is demonstrably unreliable
   (`xterm-kitty` lacks it).

4. **Two hard requirements fall out of the failure modes:** normalize CR/CRLF to LF in the
   captured payload, and never discard the tail of a read chunk after `\x1b[201~` — the
   terminating Enter frequently rides in the same chunk.

5. **Treat the pasted payload as untrusted data, never as a control channel.** An embedded
   `\x1b[201~` must at worst truncate the prompt, never escape into argv or a shell. Since
   the payload flows to `dispatch`'s prompt (already length-capped at
   `maxPromptBytes = 128 * 1024`, `internal/cli/dispatch.go:80`), the exposure is limited —
   but the parser should strip or reject stray control bytes on its own rather than trusting
   the terminal to have filtered them.

6. **Do not echo the payload character-by-character.** Every large-paste hang found in the
   wild (Claude Code, Gemini CLI, Cursor) traces to per-character render cost or a slow
   reader in the input path. Read into a large buffer, append to a byte slice, and echo a
   compact placeholder — `[pasted 47 lines, 2.1 KB]` — instead of the text. This also
   sidesteps `x/term.Terminal`'s repainting line editor, which argues for hand-rolling a
   small paste-aware reader on top of `term.MakeRaw` rather than using
   `x/term.Terminal.ReadLine`, and reusing only the marker-parsing idea.

7. **Restoration discipline is a first-class requirement, not a detail.** Deferred
   `term.Restore` + `\x1b[?2004l`, plus a `signal.Notify` handler for SIGINT/SIGTERM/SIGHUP
   that restores and re-raises. The existing picker's lack of signal handling is a gap the
   new code should not inherit — and arguably worth fixing there too, since picker.go is a
   maintained copy of tsuku's.

## Surprises

- **`golang.org/x/term` already ships all of this and niwa already depends on it.** I
  expected to be recommending a new dependency. The `bytesToKey(b, pasteActive)` signature
  and the `ErrPasteIndicator` return-error-with-valid-data convention are unusual enough
  that they are easy to miss reading the package docs.

- **The `ErrPasteIndicator` loop shape matches the desired UX almost exactly.** Pasted
  lines come back with `ErrPasteIndicator`, and the user's terminating Enter comes back
  with `err == nil` because `pasteActive` has already flipped false. "Accumulate on
  ErrPasteIndicator, submit on nil" satisfies both the bare-log and annotated-log cases
  without a sentinel.

- **terminfo is useless as a capability check.** `xterm-kitty` has no BD/BE despite kitty
  supporting the feature; `screen`/`screen-256color` correctly lack it but
  `tmux-256color` has it. Any design that gates on terminfo will misbehave on kitty.

- **tmux is better behaved than its reputation.** The recurring "tmux breaks bracketed
  paste" folklore is mostly about panes whose *application* did not enable mode 2004 —
  tmux's `(~s->mode & MODE_BRACKETPASTE)` check is doing the right thing. Since niwa will
  enable the mode explicitly, tmux is a non-issue.

- **Bracketing improves large-paste reliability rather than costing it.** kitty #5869 shows
  *unbracketed* pastes over 1 KiB dropping characters on macOS. The framed path is the more
  robust one.

- **The scope brief's "a bare `cat` never sees it" is exactly right, and I have byte-level
  proof** — bash emits `\x1b[?2004l` immediately before running each command.

## Open Questions

1. ~~**Does the whole payload survive on Windows?**~~ **Closed.** `.goreleaser.yaml:10-15`
   builds only `linux` and `darwin` on amd64/arm64. niwa does not ship Windows, so the
   ConPTY marker-loss class (Claude Code #50012) is out of scope entirely. This also
   removes the strongest argument for a heavier input library.

2. **What is the largest paste we must support, and what does the terminal do at that
   size?** Windows Terminal has `largePasteWarning`/`multiLinePasteWarning` dialogs;
   iTerm2 and GNOME Terminal have their own multi-line paste confirmations. These interrupt
   the flow before any bytes reach niwa. Worth deciding whether we document them or try to
   detect the resulting stall.

3. **How faithful is a mouse-selected log, really?** A terminal mouse selection copies the
   *rendered* text, so long log lines come back hard-wrapped at the pane width, and ANSI
   colors are gone (the selection buffer is plain text). That may be fine, or it may
   materially degrade the payload for the "error scrolled past" case. Nobody has looked at
   what an actual wrapped Go stack trace looks like after a round trip. This deserves a
   real test with a representative log.

4. **Does the terminating Enter conflict with a paste that has a trailing newline?**
   Selecting a full line with the mouse usually includes the trailing newline, so the paste
   ends `...\r\x1b[201~`. Under the `ErrPasteIndicator` loop that produces a pasted empty
   final line and then waits for the user's Enter — probably correct, but the empty-line
   handling needs an explicit decision and a test.

5. **How does an annotated paste actually feel?** The mechanism supports typing before or
   after the paste, but nobody has tried it. Does the user type the instruction first and
   then paste, or paste and then type? The answer changes whether we need multi-line typed
   input (which reintroduces the terminator problem for the typed portion).

6. **Should the fallback terminator exist at all?** If bracketed paste is the only
   supported path, the unbracketed case must fail loudly with a clear message rather than
   hanging on a read that never ends. Deciding "fail with guidance" versus "fall back to
   Ctrl-D" is a UX call that has not been made.

## Summary

Bracketed paste (DECSET 2004) is universally supported in current terminals, transparent
over ssh, correctly forwarded by tmux gated on the inner app's opt-in, and degrades to a
plain unbracketed paste rather than garbage where unsupported — and `golang.org/x/term`,
already a direct niwa dependency, implements the whole protocol including split-sequence
buffering and an `ErrPasteIndicator` signal whose loop shape ("accumulate on
ErrPasteIndicator, submit on nil") matches the target "paste then press Enter once" UX
almost exactly. This means the feature is buildable with zero new dependencies, provided
the reader normalizes CR/CRLF to LF, never discards the tail of a read chunk after
`\x1b[201~` (the terminating Enter routinely arrives in the same chunk), echoes a compact
placeholder instead of the payload, treats the payload as untrusted data that may contain
its own end marker, and restores both termios and mode 2004 on signals as well as on
normal exit. The biggest open question is fidelity rather than mechanism: a mouse-selected
log arrives hard-wrapped at the pane width with colors stripped, and nobody has checked
whether a real wrapped stack trace survives that round trip well enough to be useful to a
dispatched worker.
