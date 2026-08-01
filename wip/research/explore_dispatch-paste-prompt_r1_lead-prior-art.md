# Lead: How do comparable CLIs capture multiline input inline, and what termination affordance did each one pick?

Round 1. Evidence is web sources plus one locally verified tool (`gh`). Everything
marked "verified locally" was run in this worktree; everything else is cited.

## Findings

### The two families

Every tool surveyed falls into one of two families, and the split is clean:

1. **Non-interactive plumbing** — take the text from a file, a flag, or stdin.
   `gh -F -`, `hub -F -`, `jj describe --stdin`, `git commit -F -`, `llm` reading
   a pipe. Termination is EOF; there is no interactive story at all.
2. **Interactive TUI capture** — run a text area on the terminal. Here the whole
   design question is what submits and what inserts a newline, and the modern
   answer has converged (see the cross-cutting section).

Notably, the tools that had a "prompt for a long message" problem *before* good
bracketed-paste support existed (git, hub, gh, glab) all punted to `$EDITOR`.
The tools built after it (Claude Code, aichat, aider, gum, huh) all capture inline.

---

### Claude Code REPL prompt

The docs give an explicit "Multiline input" table
(https://code.claude.com/docs/en/interactive-mode), reproduced verbatim:

| Method           | Shortcut       | Context                                                                    |
| :--------------- | :------------- | :------------------------------------------------------------------------- |
| Quick escape     | `\` + `Enter`  | Works in all terminals                                                     |
| Option key       | `Option+Enter` | After enabling Option as Meta on macOS                                     |
| Shift+Enter      | `Shift+Enter`  | Native in iTerm2, WezTerm, Ghostty, Kitty, Warp, Apple Terminal, Windows Terminal |
| Control sequence | `Ctrl+J`       | Works in any terminal without configuration                                |
| Paste mode       | Paste directly | For code blocks, logs                                                      |

Plus a tip: "For VS Code, Cursor, Devin Desktop, Alacritty, and Zed, run
`/terminal-setup` to install the binding."

So: **Enter submits. Pasting is a first-class, separately-listed input method** —
"Paste mode / Paste directly / For code blocks, logs" is literally a row in the
multiline table. There is no terminator ceremony for a paste; the paste lands in
the buffer as multiline text and Enter still submits. `Ctrl+G` or `Ctrl+X Ctrl+E`
opens the prompt in `$EDITOR` as an escape hatch.

Large pastes collapse to a `[Pasted text #N +X lines]` chip. This is where the
complaints live, and there are many:

- #35581 asks for a configurable fold threshold or `paste.fold: false`, and is
  marked duplicate of #23702, #24333, #23134 — a recurring request. The
  motivating use case is dictation: the user needs to *see* the text to catch
  transcription errors before sending.
- #48829, #56722, #11033, #3412, #76801 all ask for some way to expand, view, or
  edit a collapsed paste before submitting.
- #49337 and #49673 are truncation bugs: "more than a few hundred lines / a few
  thousand characters" pasted into v2.1.111/v2.1.112 was **silently trimmed**,
  a regression from the prior behavior where the chip was shown and the full
  content was sent.

The truncation reports matter for us: reading a large bracketed paste off a pty
is not a single read. It arrives in chunks and a naive reader loses the tail (or
head). Whatever we build needs to buffer until the paste-end marker with a
quiescence timeout, and needs a test with a multi-KB payload.

Relevant meta-source: Jesse Vincent's "Your Terminal Can't Tell Shift+Enter from
Enter" (https://blog.fsck.com/agent-blog/2026/02/26/terminal-keyboard-protocol/,
Feb 2026) explains why. Enter and Shift+Enter both send `0x0D`; the Kitty
keyboard protocol disambiguates them as `ESC[13u` vs `ESC[13;2u`. Full support:
Kitty, foot, WezTerm, Alacritty, Ghostty, iTerm2, Warp, VS Code ≥1.109. No
support: macOS Terminal.app, PuTTY, GNOME Terminal, Konsole, Windows Terminal
(merged, unreleased). His three recommendations for CLI authors:

> Enable the protocol explicitly (with `mode: 'enabled'`, not auto-detect) —
> auto-detection fails inside tmux.
> Always provide a fallback: "Ctrl+J (which sends 0x0A, line feed — distinct
> from Enter's 0x0D) works everywhere as a newline insertion key."
> "The #1 support request you will get is 'Shift+Enter doesn't work.'"

That last line is a direct warning against making Shift+Enter load-bearing.

Issue #1259 ("Support Shift+Enter for multiline input (industry standard)",
May 2025, now closed) is the user-side view: "This goes against the industry
standard where virtually all modern tools use: Shift+Enter for newlines, Enter
for submit. Tools that use Shift+Enter for multiline: ChatGPT, GitHub Copilot
Chat, VSCode, Slack, Discord, Jupyter, etc."

---

### `gh issue create` / `gh pr create`

Verified locally (`gh issue create --help`):

```
-b, --body string          Supply a body. Will prompt for one otherwise.
-F, --body-file file       Read body text from file (use "-" to read from standard input)
-e, --editor               Skip prompts and open the text editor to write the title and body in.
                           The first line is the title and the remaining text is the body.
    --recover string       Recover input from a failed run of create
```

`gh` **never captures multiline inline**. The interactive path prompts for the
body with a one-key menu that hands off to the editor. Reported prompt text from
cli/cli threads:

- with a template: `[(e) to launch editor, (enter/return) to keep empty, (d) to accept default]`
- without: `Body [(e) to launch nvim, enter to skip]`

The multiline pain is well documented in cli/cli:

- #595 "Multi line body with flags" — the recommended answer is ANSI-C quoting:
  `gh issue create --title "test" --body $'Greetings\n\n- [ ] This\n- [ ] is a\n- [ ] multiline test'`
- #5869 "Cannot create issue with more than 1 checkbox or newlines" — user:
  "The second checkbox is in plain text, instead of a checkbox... For that
  matter, I also can't add newlines, eg paragraphs."
- Discussion #6355 is someone who wrote `--body-file-` instead of `--body-file -`
  and could not figure out the stdin syntax. Maintainer answer: "long-form
  command-line flags and their values are either separated by a space
  (`--flag value`) or with an equals sign (`--flag=value`)."
- #3887 "RFE: gh issue create option to open editor without prompt" and #5048
  "allow straight-to-editor mode" — both eventually produced `--editor`.

`--recover` is worth stealing conceptually: gh persists what you typed so a
failed API call doesn't destroy it. Losing a 400-line pasted log because
`niwa dispatch` failed to provision would be a much worse papercut than Ctrl-D.

---

### `jj describe` (jujutsu)

From the man page (https://man.archlinux.org/man/extra/jujutsu/jj-describe.1.en):

- `-m, --message <MESSAGE>`
- `--stdin` — help text verbatim: "Read the change description from stdin"
- `--editor` — "Open an editor to edit the change description", and it can force
  an editor open even when `--stdin` or `--message` was given.

This is the most interesting non-interactive data point, because jj is a modern
CLI designed years after `git commit -F -` existed and it still chose an explicit
**named flag** (`--stdin`) rather than the `-F -` overload. It also supports
`--stdin --edit`-style composition: read the pipe, then open the editor on top of
it. jj has no inline multiline capture at all — no TUI text area.

---

### `git commit` (the editor counterexample)

Default is `$EDITOR` on a template file with `#`-commented instructions and an
explicit abort rule (empty message aborts the commit). Non-interactive paths are
`-m`, `-F <file>`, and `-F -` for stdin. Termination in the editor case is
"save and quit"; the abort affordance is "write an empty message".

The relevant lesson is not the editor (explicitly out of scope) but the **abort
semantics**: every tool in this space needs an unambiguous "never mind" that is
distinct from "submit empty". `git` uses empty-message-aborts; `gum write` and
`huh` use Esc; Claude Code uses Ctrl+C / double-Esc.

---

### `hub` (predecessor of gh)

`hub pull-request` (https://hub.github.com/hub-pull-request.1.html): `-m <MESSAGE>`,
`-F <FILE>` with `-F -` for stdin, and `--edit` to open the editor on top of a
message received on stdin — the same read-then-edit composition jj later chose.
No message flags at all means "open the editor". Again: no inline capture.

---

### `glab` (GitLab CLI)

From https://docs.gitlab.com/cli/issue/create/, verbatim:

> `-d, --description string     Issue description. Set to "-" to open an editor.`
> `--no-editor              Don't open editor to enter a description. If set to true, uses prompt.`

This is a genuine one-off and a trap: in `gh`, `-` after a file flag means
**stdin**; in `glab`, `-` after `--description` means **open an editor**. Same
sigil, opposite meaning. Also note `--no-editor` falls back to a single-line
prompt, which is `glab`'s only inline path and cannot take multiple lines.

---

### `gum write` (charmbracelet)

The README still says:

> "Prompt for some multi-line text (`ctrl+d` to complete text entry)."

**The source disagrees with the README.** Reading `write/write.go` on `main`, the
default keymap is now:

- `enter` — submit
- `ctrl+j` — insert newline
- `esc` — quit
- `ctrl+e` — open editor (writes a temp file, reads it back, restores cursor line)
- `ctrl+c` — abort

So gum migrated off Ctrl+D onto Enter-submits/Ctrl+J-newline and did not update
the README. Treat the "ctrl+d" line in every gum tutorial as stale. (I could not
verify this by running gum — it is not installed here. This should be confirmed
against a real binary before it's load-bearing for a decision.)

The complaint that likely drove the change is issue #423 (Sept 17, 2023),
"Allow additional shortcuts for submitting gum write", which asked for Ctrl+S
and Ctrl+X and argued:

> "Ctrl+d is a unixy way of exiting a program, it doesn't feel logical to me as
> a 'submit'."

The same issue asked for the shortcuts to be documented in `gum write --help`,
because they weren't. Issue #642 is the flip side: a user confused by the
`enter submit` help line appearing under the prompt.

Non-TTY behavior is notable. `write/command.go` reads stdin unconditionally at
startup and uses it as the *initial value* of the text area:

```go
in, _ := stdin.Read(stdin.StripANSI(o.StripANSI))
if in != "" && o.Value == "" {
    o.Value = strings.ReplaceAll(in, "\r", "")
}
```

So `some-command | gum write` pre-fills the editor with the piped text and still
runs the TUI. That is exactly the "paste a log, then annotate it" shape, achieved
without any paste handling at all.

---

### `huh` (charmbracelet forms library — the likely Go implementation)

Default keymap for the `Text` field (`keymap.go` on `main`), verbatim:

```go
Prev:    key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "back"))
Next:    key.NewBinding(key.WithKeys("tab", "enter"), key.WithHelp("enter", "next"))
NewLine: key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"), key.WithHelp("alt+enter / ctrl+j", "new line"))
Editor:  key.NewBinding(key.WithKeys("ctrl+e"), key.WithHelp("ctrl+e", "open editor"))
Submit:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit"))
```

Same shape as gum, with `alt+enter` added alongside `ctrl+j`. huh #413 objects to
`ctrl+e` on the grounds that "in most command line input situations ctrl+e goes
to the end of the line as a standard emacs keybinding" — worth noting if we ever
add an editor escape hatch. huh #73 complains you can't override the Text keymap.

---

### `llm` (simonw)

Two paths, documented at https://llm.datasette.io/en/stable/usage.html:

- **Piped**: `echo 'Ten names for cheesecakes' | llm`, and when you combine a pipe
  with arguments — `cat myscript.py | llm 'explain this code'` — "the resulting
  prompt will consist of the piped content followed by the arguments." That is
  precisely the annotate-a-log shape, solved by concatenation rather than by an
  interactive editor.
- **Sentinel, in `llm chat`**: type `!multi` to start, `!end` on its own line to
  finish. And the detail that matters:

  > "If your pasted text might itself contain a `!end` line, you can set a custom
  > delimiter using `!multi abc` followed by `!end abc` at the end."

  `!edit` opens `$EDITOR`.

That caveat is the sentinel design's tell. The moment you choose a sentinel you
must also ship an escape hatch for payloads containing the sentinel, and the user
has to *notice* they need it. For our case — pasting arbitrary error logs and
stack traces — a collision is not hypothetical.

---

### `aichat` (sigoden)

The Chat REPL Guide (https://github.com/sigoden/aichat/wiki/Chat-REPL-Guide) lists
all four strategies at once, verbatim:

> "Press `ctrl+o` to edit buffer with an external editor (recommend)."
> "Paste multi-line text (requires terminal support for bracketed paste)."
> "Type `:::` to start multi-line editing, type `:::` to finish it."
> "Use hotkey `{ctrl,shift,alt}+enter` or `ctrl+j` to insert a newline directly."

Note the explicit caveat that paste "requires terminal support for bracketed
paste" — aichat does not assume it, and the `:::` sentinel exists as the fallback
for terminals where it isn't available.

---

### `aider`

From https://aider.chat/docs/usage/tips.html, verbatim:

> "Paste a multi-line message directly into the chat."
> "Enter `{` alone on the first line to start a multiline message and `}` alone
> on the last line to end it."
> "Or, start with `{tag` (where 'tag' is any sequence of letters/numbers) and end
> with `tag}`."
> "Use Meta-ENTER to start a new line without sending the message (Esc+ENTER in
> some environments)."
> "Use `/paste` to paste text from the clipboard into the chat."
> "Use the `/editor` command (or press `Ctrl-X Ctrl-E` if your terminal allows)
> to open your editor to create the next chat message."
> "Use multiline-mode, which swaps the function of Meta-Enter and Enter, so that
> Enter inserts a newline, and Meta-Enter submits your command."

Aider independently reinvented llm's custom-delimiter escape (`{tag` … `tag}`
mirrors `!multi abc` … `!end abc`) — two tools converging on the same workaround
is strong evidence the sentinel collision problem is real in practice, not
theoretical.

`/paste` (read the system clipboard directly, bypassing the terminal entirely) is
the most interesting one-off in the whole survey — see Surprises.

Aider's complaint threads mirror Claude Code's: #901 "Aider executes when
pressing Shift+Enter", #1143 asking for Shift-Enter for multi-line, #2473 "A
simple way to swap Enter and Meta-Enter", #3739 about single-Enter execution in
multiline mode. `--multiline` / `/multiline-mode` exists precisely because users
kept disagreeing about which key should submit.

---

### `fzf --print-query`

fzf is the architectural reference for the non-TTY question, not the multiline
question. From junegunn/fzf#3741:

> "It opens `/dev/tty` which is always the terminal (and not stdin/stdout which
> might not be seen by the user)"

So `find . | fzf | xargs vlc` works: candidate data arrives on piped stdin, the
interactive UI is driven off `/dev/tty`, and the result goes to clean stdout.
`--filter='query'` is the explicit non-interactive escape: read stdin, print
matches, exit, no UI. The same issue notes that capturing stderr makes the UI
invisible-but-functional, because fzf renders its UI to stderr/tty to keep stdout
clean.

Bubble Tea has the same capability via `WithInputTTY`, so a Go implementation can
do this.

---

## Cross-cutting patterns

**Convergent (strong evidence these feel natural):**

1. **Enter submits; the paste carries its own newlines.** Claude Code, aichat,
   aider, gum (current source), huh. Bracketed paste is what makes this possible:
   the terminal wraps pasted text in `ESC[200~` … `ESC[201~`, so pasted `\n` never
   looks like a keypress. Confirmed at the library level — bubbletea enables
   bracketed paste by default (`WithoutBracketedPaste` disables it), and
   `bubbles/textarea` `insertRunesFromUserInput()` explicitly splits pasted
   content on `\n` into real lines rather than treating them as submit.
2. **Ctrl+J is the universal manual-newline key.** Claude Code, aichat, gum, huh
   all bind it; the fsck.com post recommends it explicitly because `0x0A` is
   distinct from Enter's `0x0D` in every terminal. Shift+Enter and Alt+Enter are
   offered *in addition*, never alone.
3. **An `$EDITOR` escape hatch on a key chord.** Ctrl+E (gum, huh, aichat's
   ctrl+o), Ctrl+G / Ctrl+X Ctrl+E (Claude Code, aider). Even the tools that
   capture inline keep this. Out of scope for us, but it is unanimous.
4. **A non-interactive stdin path, always.** `gh -F -`, `hub -F -`,
   `jj --stdin`, `git -F -`, `llm` piped, `gum write` pre-filled from stdin,
   `fzf --filter`. Every single tool has one. None of them make the interactive
   path the only path.
5. **Pipe-content-plus-argument concatenation.** `cat x | llm 'explain this'` and
   `cmd | gum write` both produce "here is the payload, here is my instruction"
   without any interactive machinery.

**One-offs (weak evidence, or actively cautionary):**

- **Ctrl+D as submit** — gum's old behavior, and the thing #423 complained about
  in exactly the words our exploration uses ("the Ctrl-D ceremony"). gum appears
  to have moved off it. No tool surveyed *added* Ctrl+D recently.
- **Sentinel lines** — `!multi`/`!end` (llm), `:::` (aichat), `{`/`}` (aider).
  Real, but always a fallback next to paste, and two of the three had to add a
  custom-delimiter escape because payloads collide with the sentinel.
- **`-` meaning "open an editor"** (glab) versus `-` meaning stdin (everyone
  else). Do not copy glab here.
- **Reading the system clipboard directly** (aider `/paste`, Claude Code's
  `Ctrl+V` image paste). Sidesteps the terminal entirely. Cross-platform cost is
  real (X11/Wayland/macOS/WSL) but it makes termination a non-question: the
  clipboard has a known length.
- **Auto-submitting when the paste ends.** *No tool surveyed does this.* Every
  paste-aware tool leaves the buffer open after the paste completes, which is
  exactly what makes "paste a log, then type an instruction, then Enter" work.

**Non-TTY behavior, summarized:**

| Tool | stdin not a TTY |
| :-- | :-- |
| `gh` | `-F -` reads stdin; interactive prompts are skipped, missing required values become errors |
| `jj describe` | `--stdin` explicitly; `--editor` can still force an editor |
| `git commit` | `-F -` reads stdin |
| `hub` | `-F -` reads stdin, `--edit` layers the editor on top |
| `gum write` | reads piped stdin as the text area's *initial value*, then still runs the TUI |
| `fzf` | data from piped stdin, UI from `/dev/tty`, result to clean stdout; `--filter` for fully headless |
| `llm` | piped stdin becomes the prompt; combined with args, pipe content comes first |
| `aichat` | piped stdin becomes the prompt, no REPL |

---

## Implications

**The Ctrl-D ceremony is a solved problem, and the solution is not a better
terminator — it is bracketed paste plus Enter-submits.** Five independently
developed tools landed on the same three-key vocabulary: Enter submits, Ctrl+J
inserts a newline, paste is inert. This directly satisfies both required
workflows from the exploration scope — a bare pasted log is paste-then-Enter, and
an annotated log is paste-then-type-then-Enter — with no terminator ceremony in
either case and no mode to enter or leave.

**Do not make Shift+Enter load-bearing.** It requires the Kitty keyboard protocol,
which macOS Terminal.app, GNOME Terminal, Konsole, PuTTY, and current Windows
Terminal do not support. The fsck.com post's warning ("The #1 support request you
will get is 'Shift+Enter doesn't work'") is corroborated by the volume of
Claude Code #1259 and aider #901/#1143/#2473. Bind it if the protocol is
negotiated, but Ctrl+J must be the documented answer.

**Do not choose a sentinel line as the primary mechanism.** Both tools that did
(llm, aider) had to bolt on custom delimiters because real payloads contain the
sentinel — and our payload is by definition arbitrary error output. A sentinel
is a defensible *fallback* for terminals without bracketed paste, which is
exactly the role aichat gives `:::`.

**Keep a non-interactive path even though the exploration scoped it out.** Every
tool surveyed has one, and the existing `niwa dispatch -d "$(cat)"` workaround is
already that path. jj's `--stdin` is the cleanest precedent (explicit named flag,
no `-` overloading). fzf's `/dev/tty` trick — available in Go via bubbletea's
`WithInputTTY` — means accepting piped stdin and running an interactive capture
are not mutually exclusive; and `gum write`'s "piped stdin pre-fills the text
area" is arguably the single best fit for `niwa dispatch`, since it makes
`some-failing-command 2>&1 | niwa dispatch` open an editable buffer already
containing the log.

**Budget for the large-paste bug.** Claude Code shipped a regression (#49337,
#49673) where multi-KB pastes were silently trimmed. Reading a bracketed paste
off a pty means reading until `ESC[201~` with a quiescence timeout, not a single
read. This needs an explicit test with a several-hundred-line payload.

**Consider `--recover`.** gh persists typed input so a failed create doesn't lose
it. If dispatch fails to provision an instance after the user pasted 400 lines,
losing that text is worse than the papercut we set out to fix.

**Implementation is off-the-shelf.** niwa is Go + cobra; `charmbracelet/huh`'s
Text field already ships the exact convergent keymap (Enter submit, alt+enter /
ctrl+j new line, ctrl+e editor), and `bubbles/textarea` already splits pasted
newlines into real lines. This is closer to wiring than to building.

---

## Surprises

**gum's README contradicts gum's source.** Every tutorial says `ctrl+d` completes
`gum write`; the current source binds Enter to submit and Ctrl+J to newline. If
we cite gum as a Ctrl+D precedent we would be citing a tool that appears to have
already abandoned it, in response to a complaint worded almost identically to our
own. Flagged as needing verification against a real binary — gum is not installed
in this worktree.

**No tool auto-submits at the end of a paste.** I expected at least one to treat
the bracketed-paste end marker as "input complete". None do. That is not an
oversight — it is what preserves the annotate-after-paste workflow the
exploration explicitly requires. Paste-boundary detection is the right mechanism
for *preserving newlines*, and the wrong mechanism for *terminating input*.

**`gum write` reading piped stdin as the initial buffer value.** This quietly
solves "bare paste vs annotated paste" without any paste handling: the pipe
supplies the log, the TUI lets you add the instruction. It suggests a design
where `niwa dispatch` with no argument opens a buffer, and `cmd | niwa dispatch`
opens the *same* buffer pre-filled.

**glab inverts the `-` convention** — `-d -` opens an editor rather than reading
stdin. A live example of how a small deviation from convention becomes a
permanent papercut.

**The biggest CLI in this space (`gh`) never solved inline capture at all**, and
its issue tracker (#595, #5869, #3887, #5048, #6408, discussion #6355) is a
multi-year record of users working around that. gh's answer is "use `$EDITOR` or
`$'...'` quoting" — which is the option this exploration has already rejected.

**Aider's `/paste` reads the system clipboard directly.** Worth naming as an
alternative that makes termination a non-question, though the cross-platform
clipboard story (X11 vs Wayland vs macOS vs WSL, and the remote-SSH case where
the clipboard is on the wrong machine) is a real cost. The SSH case probably
kills it for niwa.

---

## Open Questions

1. **Does gum's current release actually bind Enter/Ctrl+J?** I read `main` via
   raw.githubusercontent and could not run the binary. Needs confirmation before
   this is used as evidence in a decision.
2. **What does Claude Code do when a paste arrives with no TTY, or inside tmux
   without `extended-keys on`?** The docs cover terminals, not multiplexers, and
   tmux is where the fsck.com post says auto-detection breaks.
3. **What is Claude Code's paste-collapse threshold in lines?** Not documented in
   any issue I found. If we adopt a chip, we need a number and it should be
   configurable from day one, given how many duplicate requests that generated.
4. **Should `niwa dispatch` with piped stdin open a buffer (gum) or run headless
   (gh/jj)?** Both are defensible and the tools split on it. This is a product
   call, not a research finding — but note that a piped invocation inside a
   script must never block on a TUI, so any buffer-opening behavior needs to be
   conditional on `/dev/tty` being available.
5. **Which terminals in the user's actual environment negotiate bracketed paste
   and the Kitty protocol?** aichat's docs hedge ("requires terminal support for
   bracketed paste") for a reason. The bracketed-paste lead should own this; the
   answer determines whether a sentinel fallback is needed at all.
6. **How do we handle a paste that arrives while the process is still starting?**
   If the user pastes immediately on launch, some of the payload may hit the
   terminal before the app enables bracketed paste. Not seen discussed anywhere;
   may be a real edge in practice.

---

## Summary

Five independently built tools — Claude Code, aichat, aider, gum, and huh —
converged on the same vocabulary for inline multiline capture: bracketed paste
makes pasted newlines inert, Enter submits, and Ctrl+J is the universal
manual-newline key, with Shift+Enter offered only as a bonus where the Kitty
keyboard protocol is negotiated; nobody auto-submits at the end of a paste,
which is exactly what preserves the paste-then-annotate workflow. The
implication is that the Ctrl-D ceremony has a well-trodden replacement rather
than needing a novel terminator, and it is close to off-the-shelf for a Go CLI
(`huh`'s Text field already ships that keymap), while sentinel lines should be
demoted to a fallback since both tools that tried them had to add custom
delimiters for payloads that contain the sentinel. The biggest open question is
whether to keep the non-interactive path headless like `gh`/`jj` or pre-fill an
editable buffer from piped stdin the way `gum write` does — the latter would make
`failing-command 2>&1 | niwa dispatch` land the log in an annotatable buffer for
free.
