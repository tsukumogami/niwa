# Explore Scope: dispatch-paste-prompt

## Visibility

Public

## Core Question

`niwa dispatch` takes its prompt as a single positional argument
(`cobra.ExactArgs(1)`), so there is no way to feed it a multiline block of text
you just copied out of your terminal. We are figuring out what the input UX
should be for the case that actually recurs: seeing an error or log scroll past,
selecting it with the mouse, and handing it to a dispatched worker. The open
question is how the command should capture that text inline and how the user
signals that the input is finished.

## Context

- Today `dispatch.go:131` declares `Args: cobra.ExactArgs(1)`; nothing in the
  dispatch path reads stdin. The only stdin wiring is `dispatch.go:106`, which
  hands the terminal to `claude attach` after a successful launch.
- The prompt is passed to `claude` as a discrete argv element
  (`dispatch_launcher.go:66-70`), never through a shell, so multiline content is
  already safe once it reaches the command; only the *capture* is missing.
- `maxPromptBytes` caps the prompt at 128 KB (`dispatch.go:81`).
- Existing workaround: `niwa dispatch -d "$(cat)"`, paste, Ctrl-D. It works but
  requires `-d` (attach and a consumed pipe cannot share stdin) and the Ctrl-D
  ceremony is the papercut that makes it go unused.
- Enter cannot terminate a raw multiline read from a TTY: newlines are part of
  the pasted payload, so the terminator has to be out-of-band (EOF, a sentinel
  line, a key chord, or paste-boundary detection).
- Bracketed paste (DECSET 2004) is the one mechanism that makes "paste, then
  Enter" work, because the terminal delimits the pasted block for the program.
  A bare `cat` never sees it: bash disables bracketed paste before running a
  command.
- niwa has a small TUI at `internal/tui/picker.go` but no `$EDITOR` precedent
  anywhere in the codebase.

## In Scope

- Interactive paste into a waiting prompt as the primary path.
- Supporting both a bare pasted log and a log the user annotates with an
  instruction, without extra ceremony for either.
- Inline capture that keeps the user in the terminal.
- How the input is terminated, and what happens on abort.
- Graceful behavior when the terminal or session cannot support the primary
  mechanism.

## Out of Scope

- Launching `$EDITOR`. The user explicitly wants to stay inline.
- A `--clipboard` flag shelling out to `pbpaste`/`wl-paste`/`xclip`.
- A `--prompt-file` flag.
- Scripted piping (`make 2>&1 | niwa dispatch`) as a design driver. It may fall
  out of the design for free, but it is not what we are optimizing for.
- Changing how the prompt reaches the `claude` binary once captured.
- Prompt content, brief synthesis, or anything the `/dispatch` skill owns.

## Research Leads

1. **How does bracketed paste actually behave in practice, and what does a Go
   program need to do to consume it reliably?**
   This is the mechanism the whole "paste then Enter" UX rests on. We need to
   know how DECSET 2004 is enabled and torn down, how it survives tmux, screen,
   and ssh, which terminals lack it, and how a program distinguishes a pasted
   newline from a typed one. If it is unreliable in the user's real environment,
   the rest of the design changes.

2. **How do comparable CLIs capture multiline input inline, and what termination
   affordance did each one pick?**
   Several tools have solved this already. Worth knowing what the Claude Code
   REPL, `gh issue create`, `jj describe`, `gum write`, `llm`, `aichat`, and
   similar tools do, which of them are paste-aware, and where users complain
   about the choice. Convergent prior art is the strongest evidence for what
   feels natural.

3. **What would a paste-aware inline prompt cost inside niwa today?**
   Map the implementation surface: what `internal/tui/picker.go` already pulls
   in, whether the existing dependency set gives us a multiline paste-aware
   input for free or whether this means new dependencies, and how much raw
   terminal handling would have to be written by hand.

4. **How does an inline prompt fit dispatch's existing control flow?**
   The prompt would own the terminal and then hand it to `claude attach`.
   Investigate the interaction with `--detach`, the argv-vs-prompt precedence
   when a positional argument is also supplied, what an abort at the prompt
   should do given dispatch's provisioning happens after prompt validation, and
   whether the non-TTY case needs a defined behavior even though piping is not
   a design driver.

5. **What are the hazards of accepting arbitrary pasted terminal output as the
   prompt?**
   Pasted logs carry ANSI escapes, control characters, and possibly very large
   payloads. Investigate the 128 KB cap against realistic stack traces and CI
   logs, whether escape sequences need stripping before the text becomes argv or
   is echoed back, how terminal state is restored if the user hits Ctrl-C
   mid-paste, and what the practical concerns are when untrusted log content
   becomes a worker's opening instruction.

6. **How should a user type a multiline prompt by hand, when Enter means
   submit?**
   Annotating a pasted log is in scope, so the user will be typing around the
   paste. If Enter submits, manual newlines need another affordance. Investigate
   what is actually detectable in a raw terminal (Shift+Enter, Alt+Enter,
   Ctrl-J, trailing backslash, Esc then Enter), what terminals report reliably,
   and what the tools in lead 2 settled on.
