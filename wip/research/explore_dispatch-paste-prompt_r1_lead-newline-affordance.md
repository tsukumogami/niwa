# Lead: How should a user type a multiline prompt by hand, when Enter means submit?

## Findings

### 1. The affordances, byte by byte

Everything below assumes the terminal is in **raw / non-canonical mode** unless stated
otherwise. Section 2 explains why that caveat is the single most important finding in
this document.

| Affordance | Bytes the terminal sends (legacy encoding) | Kitty-protocol encoding | Detectable by a program reading the tty? |
|---|---|---|---|
| Enter | `0x0D` (CR) | `ESC[13u` | yes |
| Ctrl+J | `0x0A` (LF) | `ESC[106;5u` | yes, in raw mode; **no** in canonical mode |
| Ctrl+M | `0x0D` — identical to Enter | `ESC[109;5u` | no, in legacy |
| Shift+Enter | `0x0D` — identical to Enter | `ESC[13;2u` | only if the terminal speaks kitty or `modifyOtherKeys` |
| Alt/Option+Enter | `0x1B 0x0D` (ESC CR), only if Option is configured as Meta | `ESC[13;3u` | yes, when the terminal actually sends it |
| Esc then Enter | `0x1B 0x0D` — **byte-identical to Alt+Enter** | n/a (two separate key events) | yes, but indistinguishable from Alt+Enter except by inter-byte timing |
| Ctrl+O | `0x0F` (SI) | `ESC[111;5u` | yes, but the key is already spoken for (see §5) |
| `\` then Enter | `0x5C 0x0D` — ordinary characters | same | yes, and **works in canonical mode too** |
| Double Enter (submit on blank line) | two `0x0D` | same | yes, works in canonical mode |
| Ctrl+D | `0x04`; in canonical mode this is VEOF, not a byte | `ESC[100;5u` | yes in raw mode; in cooked mode it ends the read |

Two consequences fall straight out of the table.

**Esc-then-Enter is not a separate affordance.** It emits exactly the same two bytes as
Alt+Enter (`0x1B 0x0D`). A program can only tell them apart by an escape-timeout heuristic
(typically 25–50 ms), which is the same fragile trick that makes bare Escape ambiguous in
terminals generally. Implementing "Esc then Enter" is implementing Alt+Enter, plus a race.
The kitty protocol's stated purpose includes resolving exactly this Escape ambiguity
(kovidgoyal/kitty `docs/keyboard-protocol.rst`).

**Ctrl+M is worthless.** Claude Code's keybindings documentation lists Ctrl+M in its
"reserved shortcuts" table with the reason "Identical to Enter in terminals (both send CR)"
— a useful confirmation that a shipped product hit this and gave up on it.

### 2. The cooked-mode wall (the finding that constrains everything else)

In POSIX canonical mode (`ICANON`), the line discipline hands a line to the program when it
sees **NL (`0x0A`)**, EOL, or EOF. `ICRNL` — on by default — translates the CR that Enter
produces into NL. Ctrl+J *is* NL.

Therefore, with a plain `bufio.Scanner(os.Stdin)`:

- Enter and Ctrl+J are **the same event**. Ctrl+J terminates the read exactly like Enter.
- Shift+Enter is invisible even on a kitty-capable terminal: if the app never emitted
  `CSI > 1 u` to request the protocol, the terminal sends legacy `0x0D` anyway; and if it
  did, the `ESC[13;2u` bytes would arrive as literal text inside the line, only delivered
  after a subsequent Enter.
- Alt+Enter delivers `ESC` as a stray literal byte followed by end-of-line.

So: **every chord-based affordance — Ctrl+J, Shift+Enter, Alt+Enter, Ctrl+O — requires
putting the tty into raw mode and running your own line editor.** There is no middle ground.
The affordances that survive cooked mode are only the text-level ones: trailing-backslash
continuation, submit-on-blank-line, and sentinel/heredoc delimiters (`:::`, `!multi`/`!end`).

This is worth stating loudly because every doc in the field — Claude Code's, Codex's, the
blog posts — says "Ctrl+J works in every terminal." That is true, and it is also true only
of applications that already went raw. It is not a property `niwa dispatch` gets for free.

(Bracketed paste is a partial exception: the `ESC[200~` / `ESC[201~` markers arrive as
literal bytes, so a cooked-mode reader *can* accumulate lines until it sees the closing
marker. That path belongs to the bracketed-paste lead, but it means "cooked mode" and "paste
arrives as one unit" are not mutually exclusive.)

### 3. Terminal support for reporting Shift+Enter distinctly

Two competing encodings exist.

**Kitty keyboard protocol.** The application opts in with `CSI > 1 u` (progressive
enhancement flag `0b1`, "disambiguate legacy escape codes") and disables with `CSI < u`.
It can query current state with `CSI ? u`; a terminal that does not implement the protocol
simply never answers, which is the intended graceful-degradation path. Keys are reported as
`CSI keycode ; modifiers u`, where the modifier field is `1 + bitmask` (Shift=1, Alt=2,
Ctrl=4, Super=8). Enter is keycode 13, so Shift+Enter is `ESC[13;2u` and Ctrl+Enter is
`ESC[13;5u`.

**xterm `modifyOtherKeys`.** The older mechanism, enabled with `ESC[>4;2m`. With xterm's
default `formatOtherKeys: 0` the encoding is `CSI 27 ; mod ; code ~` — Shift+Enter is
`ESC[27;2;13~`. With `formatOtherKeys: 1` it switches to the CSI-u shape,
`ESC[13;2u`. An implementation that wants broad coverage has to parse **both** shapes.
`modifyOtherKeys` has no push/pop stack and no query.

Support as of early-to-mid 2026:

| State | Terminals |
|---|---|
| Full kitty protocol | kitty 0.20.0+ (Apr 2021), foot ~1.11+ (Dec 2021), WezTerm 20220624+ (needs `enable_kitty_keyboard = true`), Alacritty 0.13.0+ (Dec 2023), Ghostty 1.0+ (Dec 2024), iTerm2 3.5+, Warp (Feb 2026), VS Code 1.109+ (needs `"terminal.integrated.enableKittyKeyboardProtocol": true`) |
| CSI-u only, via `modifyOtherKeys` | xterm (needs `modifyOtherKeys: 2` and `formatOtherKeys: 1` in `.Xresources`), mintty / Git Bash (on by default since 0.4.0) |
| Nothing | macOS Terminal.app, PuTTY, GNOME Terminal / VTE (gitlab.gnome.org/GNOME/vte issue #2601, patches under review as of Dec 2025 — affects Tilix, Terminator, Guake too), Konsole (requested Nov 2025, no work started), Windows Terminal (microsoft/terminal PR merged, targeted v1.25, unreleased), GNU screen, mosh |

**tmux** forwards CSI-u encoded keys since 3.2 but does not implement the full kitty
protocol (no push/pop/query). It needs three settings, and one of them is a trap:

```
set -s extended-keys on
set -as terminal-features 'xterm*:extkeys'
set -s extended-keys-format csi-u
```

The option is named `extended-keys` but the *feature flag* tmux checks in
`terminal-features` is `extkeys`. Writing `extended-keys` there produces no error — tmux
silently ignores unknown feature flags, `tmux show` reports the setting as present, and
nothing works. Claude Code's own tmux guidance ships the first two lines plus
`set -g allow-passthrough on`.

**Fallback story when the terminal can't report it.** Two, and both are in production use:

1. **Ctrl+J.** Sends `0x0A`, which is a distinct byte from Enter's `0x0D` in every terminal
   ever built. No protocol, no negotiation, no configuration. This is why every tool in §5
   converges on it.
2. **Terminal-side remapping** — make the terminal rewrite the chord into a sequence the app
   already understands, rather than asking it to speak a protocol. Claude Code's
   `/terminal-setup` does exactly this: for VS Code / Cursor / Devin Desktop it writes a
   `keybindings.json` entry using `workbench.action.terminal.sendSequence` with
   `"\r"` — ESC CR, i.e. it turns Shift+Enter into Alt+Enter. Same idea for Alacritty
   and Zed config files. The app therefore only has to detect `0x1B 0x0D`.

Combining those: an application that recognizes **`0x0A`** (Ctrl+J), **`0x1B 0x0D`**
(Alt+Enter, and anything remapped to it), and optionally **`ESC[13;2u`** covers
substantially the entire field without ever emitting a protocol-enable sequence.

### 4. What "no support" actually means for the user

For GNOME Terminal and JetBrains-embedded terminals there is no workaround at all. VTE
exposes no custom key-to-escape-sequence mapping through dconf or gsettings, so there is
nothing to remap. Claude Code's docs are blunt about this: for "gnome-terminal, JetBrains
IDEs such as PyCharm and Android Studio" the Shift+Enter column reads "Not available; use
Ctrl+J or `\` then Enter." Any design that treats Shift+Enter as the primary newline
affordance has a permanent hole for a large slice of Linux desktop users.

### 5. What the tools that face this exact problem settled on

**Claude Code** (the user's own daily driver, so this is the muscle memory that matters):

- `chat:submit` = **Enter**. `chat:newline` = **Ctrl+J** — that is the documented default
  binding, not Shift+Enter.
- `\` then Enter also inserts a newline. The docs say of these two: "Both work in every
  terminal with no setup."
- Shift+Enter works where the terminal allows it; `/terminal-setup` installs it for VS Code,
  Cursor, Devin Desktop, Alacritty, and Zed.
- Option+Enter on macOS, but only after enabling "Use Option as Meta Key".
- Ctrl+G or Ctrl+X Ctrl+E opens an external editor; Ctrl+O is `app:toggleTranscript`;
  Ctrl+D is a hardcoded, unrebindable exit.
- Everything is rebindable in `~/.claude/keybindings.json`, and the docs explicitly document
  the inversion as a supported configuration: "To bind newline to a different key, or to swap
  behavior so Enter inserts a newline and Shift+Enter submits, map the `chat:newline` and
  `chat:submit` actions."
- Relevant to this exploration: Claude Code **collapses large pastes**. Over 800 characters
  or more than two lines and the input shows a placeholder like `[Pasted text #1 +120 lines]`
  while sending the full content on submit.

**OpenAI Codex CLI**: Enter submits, **Ctrl+J** inserts a newline. Shift+Enter and Alt+Enter
are also bound but documented as unreliable because many terminals cannot distinguish them.
A `/keymap` command was added in 0.128.0 for customization. There is a live request
(openai/codex#12129) to invert to Enter=newline / Ctrl+Enter=submit, arguing "the Codex TUI
functions primarily as a multiline prompt editor rather than a classic shell."

**`gum write`** (charmbracelet) is the one clean inversion: **Enter inserts a newline,
Ctrl+D (or Esc) submits**, Ctrl+C cancels. It gets away with it because it is a
single-purpose composer with no conversational loop. Its entire discoverability story is the
placeholder string, and the README's own example spells the terminator out in the prompt:
`gum write --placeholder "Details of this change (CTRL+D to finish)"`.

**aichat** offers four paths at once: bracketed paste (documented as "requires terminal
support for bracketed paste"); `{ctrl,shift,alt}+enter` or `ctrl+j` to insert a newline;
`:::` to open and `:::` to close a multi-line block; and `ctrl+o` to hand off to `$EDITOR`,
which the docs mark as "(recommend)".

**`llm`** (simonw) uses a pure sentinel in chat mode: type `!multi`, then lines, then `!end`.

The convergence is striking. Every terminal chat tool that keeps Enter=submit lands on
**Ctrl+J as the guaranteed newline**, treats Shift+Enter as a best-effort nicety, and ships
a text-level escape hatch (`\`+Enter, `:::`, `!multi`) for terminals that can do neither.
Only `gum`, which has no conversation, inverts.

**Double-Enter-to-submit**: I could not find a single terminal chat tool that uses it. That
absence is itself informative. It is also directly hostile to pasted logs: a log with a blank
line in it would submit early — unless bracketed paste means the pasted newlines never
arrive as key events at all, which is precisely the mechanism this exploration is already
banking on.

### 6. Is there a design that avoids the problem?

**A. Enter submits, bracketed paste handles the block.** The pasted payload's newlines never
reach the key handler, so one Enter submits. A manual newline is needed only when the
annotation must sit on its own line rather than before or after the paste on the same line.
Matches the user's existing Claude Code muscle memory exactly. Still needs a newline chord
for the annotation-on-its-own-line case, which means raw mode.

**B. Enter inserts a newline, Ctrl+D submits** (`gum`'s model). Needs no chord detection for
newline and no terminal capability at all. But Ctrl+D collides with EOF and with the
universal "Ctrl+D exits" reflex — Claude Code hardcodes it to exit and refuses to let you
rebind it — and it inverts the user's daily-driver behavior. It also interacts badly with
cooked mode, where Ctrl+D on a non-empty line flushes the partial line rather than signalling
EOF, so a naive implementation misbehaves in a way that is annoying to debug.

**C. Sentinel / heredoc** (`:::`, `!multi`/`!end`, lone `.`). Works in cooked mode, needs
nothing from the terminal, and is trivially implementable. Costs the user a mode they have to
remember, and a pasted log could in principle contain the sentinel.

**Recommendation: A**, with Ctrl+J and `\`+Enter as the two universal newline affordances and
Shift+Enter recognized opportunistically (`ESC[13;2u`, `ESC[27;2;13~`, and `0x1B 0x0D`) but
never advertised as required. The deciding argument is not abstract discoverability — B is
genuinely easier to explain in one line, and `gum` proves that works. It is that the user
has stated they live in Claude Code, and Claude Code is Enter=submit / Ctrl+J=newline.
Inverting Enter inside a tool sitting next to that one buys a cleaner explanation at the cost
of a mis-fire every time the user's fingers forget which program they are in.

## Implications

**The raw-mode decision is the real fork in this design, not the choice of chord.** If
`niwa dispatch` reads with a plain line reader, it can offer trailing-backslash continuation
and sentinel terminators and nothing else — Ctrl+J will silently behave as Enter, which is a
worse failure than not offering it. If it goes raw, it inherits a line editor's worth of
responsibility (cursor movement, backspace, history, resize, restoring termios on signal) and
should almost certainly take a library rather than hand-roll. Bubble Tea v2 is the obvious
Go candidate: it already implements both `modifyOtherKeys` and kitty enhanced keyboard, and
surfaces the result as `tea.KeyPressMsg` whose `.String()` yields `"shift+enter"`, so the
protocol plumbing in §3 becomes a dependency choice rather than a parsing project.

**The minimum viable detection set is small.** `0x0A`, `0x1B 0x0D`, and `ESC[13;2u`. The
terminal-side-remap trick means an app never has to negotiate a protocol to get Shift+Enter
working for users willing to run one setup step — but shipping a `/terminal-setup` analogue
is almost certainly out of scope for a dispatch subcommand.

**Advertise Ctrl+J, not Shift+Enter.** Shift+Enter is the chord users *want* and the one that
cannot be promised. Every tool in this space that documented Shift+Enter first has an issue
tracker full of people confused about why it submits their prompt (anthropics/claude-code
#9321, #2335; openai/codex #3024, #8673, #20580).

**The annotation case may be smaller than assumed.** With bracketed paste, the paste and the
typed annotation are separated at the source. If the user types the instruction first and
then pastes, or if the copied selection carries a trailing newline (common when selecting
whole lines), no manual newline is needed at all. The chord is insurance for the case where
the annotation must follow the paste on its own line — real, but not the common path.

## Surprises

**Ctrl+J is not universal in the way everyone writes that it is.** In canonical mode it is
byte-for-byte the same event as Enter. Every doc that says "Ctrl+J works in every terminal"
is quietly assuming the application already runs a raw-mode line editor.

**Esc-then-Enter is Alt+Enter.** Same two bytes. Listing them as separate options in a design
doc would be a mistake; they are one affordance with a timing ambiguity.

**Claude Code's own documentation contradicts the protocol-support picture.** Its terminal
config page lists Apple Terminal and Windows Terminal under "Works without setup" for
Shift+Enter. But Terminal.app implements neither kitty nor `modifyOtherKeys`, and Windows
Terminal's kitty PR is merged-but-unreleased. The likely explanation is that Claude Code's
first-run prompt silently runs `/terminal-setup`, which for Apple Terminal enables Option-as-
Meta (giving Alt+Enter, not Shift+Enter), and that Windows Terminal has a default input
binding. Either way, "works without setup" in that table is doing some work that the
protocol-support literature does not account for. Worth not taking at face value.

**The tmux flag-name trap** (`extkeys`, not `extended-keys`, inside `terminal-features`) fails
silently. If niwa ever documents tmux setup, getting this wrong produces a config that looks
correct under `tmux show` and does nothing.

**Nobody uses Ctrl+O for newline.** It is already taken in this exact product category:
`app:toggleTranscript` in Claude Code, external-editor in aichat. Ctrl+O is also SI
(shift-in) historically. It should come off the candidate list.

**Nobody uses double-Enter either.** Zero examples found among terminal chat tools.

## Open Questions

- Does `niwa dispatch` accept a raw-mode line editor dependency? Everything chord-based hinges
  on this, and it is the impl-surface lead's call more than mine.
- Does a terminal mouse-selection copy typically carry a trailing newline? If it usually does,
  the annotation lands on a fresh line for free after the paste and the newline chord becomes
  near-vestigial. I did not find a definitive answer; it plausibly varies by terminal and by
  whether the selection ends mid-line.
- Should niwa ship a `/terminal-setup` analogue that writes Shift+Enter remaps into VS Code /
  Alacritty / Zed configs? My instinct is no — far too much surface for a dispatch subcommand —
  but the remap trick is cheap enough that it deserves an explicit rejection rather than an
  omission.
- Windows / PowerShell behavior is unverified here. Windows Terminal lacks the kitty protocol
  in released builds, and Go's termios handling differs; if Windows is in scope this needs its
  own pass.
- If the design lands on Enter=submit, does the annotation need to support more than one line
  at all? If a single typed line is genuinely enough, `\`+Enter alone may cover it and the
  raw-mode question disappears.

## Summary

Every terminal chat tool that keeps Enter as submit has converged on Ctrl+J as the newline —
Claude Code's documented `chat:newline` default, Codex's too — with `\`+Enter or a `:::`-style
sentinel as the terminal-independent fallback, because Shift+Enter is only reportable via the
kitty protocol or xterm `modifyOtherKeys` and is permanently unavailable in GNOME
Terminal/VTE, Terminal.app, and released Windows Terminal. The finding that actually
constrains the design is that Ctrl+J is indistinguishable from Enter in canonical mode, so
every chord-based affordance requires `niwa dispatch` to take over the tty in raw mode and run
a line editor; only trailing-backslash continuation and sentinel terminators survive a plain
line read. The biggest open question is whether that raw-mode dependency is worth taking at
all, given that bracketed paste already separates the pasted block from the typed annotation
and a single typed line may be all the annotation ever needs.
