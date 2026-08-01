# Exploration Findings: dispatch-paste-prompt

## Core Question

`niwa dispatch` takes its prompt as a single positional argument
(`cobra.ExactArgs(1)`), so there is no way to feed it a multiline block of text
copied out of the terminal. We are figuring out what the input UX should be for
the recurring case: seeing an error scroll past, selecting it with the mouse, and
handing it to a dispatched worker -- and how the user signals input is finished.

## Round 1

### Key Insights

- **The mechanism is settled by convergent prior art.** Claude Code, aichat,
  aider, `gum`, and `huh` independently landed on the same vocabulary: bracketed
  paste makes pasted newlines inert, Enter submits, Ctrl+J inserts a manual
  newline, Shift+Enter is a bonus only where the kitty protocol is negotiated.
  (lead-prior-art)

- **No tool auto-submits at the end of a paste, and that is deliberate.** The
  end marker is the right mechanism for *preserving newlines* and the wrong one
  for *terminating input*. Not auto-submitting is exactly what preserves the
  paste-then-annotate workflow. This answers the original question directly:
  paste lands in a buffer, Enter submits, and the same gesture covers both the
  bare log and the annotated one. (lead-prior-art)

- **Bracketed paste is safe to rely on.** Effectively universal in current
  terminals, transparent over ssh, correctly forwarded by tmux gated on the
  inner app's opt-in, and degrades to an unbracketed paste rather than garbage
  where unsupported. tmux's bad reputation traces to apps that never enabled
  mode 2004. terminfo is useless as a capability check (`xterm-kitty` lacks
  BD/BE despite supporting the feature), so detection must be lazy, not probed.
  (lead-bracketed-paste)

- **Zero new dependencies.** `golang.org/x/term` v0.42.0 is already a direct
  require and implements the whole protocol: `SetBracketedPasteMode` writes
  DECSET 2004 (`terminal.go:983`), the decoder handles split sequences, and
  `ErrPasteIndicator` marks pasted input. niwa has no TUI framework -- the
  195-line `internal/tui/picker.go` is hand-rolled ANSI over `x/term`.
  (lead-impl-surface, lead-bracketed-paste)

- **The prompt has exactly one correct slot in `runDispatch`:** after the
  workspace, agent, and `claude` preflight checks (`dispatch.go:148-187`) and
  before the first creation (`dispatch.go:216`). Abort is then a literal no-op
  outside the deferred-rollback window, and the command fails fast before the
  user types anything. (lead-dispatch-flow)

- **Inline capture beats `$(cat)` on the attach axis, not just ceremony.**
  Reading the terminal rather than draining a pipe keeps stdin a TTY, so the
  default `claude attach` handoff works unchanged. The workaround's forced `-d`
  was structural, not incidental. (lead-dispatch-flow)

- **A real, pre-existing bug that paste makes reachable.** `maxPromptBytes` is
  `128 * 1024` = 131072, which is *exactly* Linux's per-argument
  `MAX_ARG_STRLEN`, not a conservative bound below it. Verified empirically:
  131071 OK, 131072 `E2BIG`. Compounding it, the 638-byte keep-alive
  instruction is prepended at `dispatch.go:327` *after* the check at
  `dispatch.go:144`, so an in-bounds prompt can be pushed out of bounds and die
  at `execve` after provisioning. The code comment asserts a margin that does
  not exist and conflates `ARG_MAX` (total) with `MAX_ARG_STRLEN` (per-string).
  Nobody types a 128 KB argument; pasting a log is how you get there.
  (lead-impl-surface and lead-paste-hazards, independently)

- **The ANSI hazard largely evaporates on the path in scope.** Terminal mouse
  selection copies rendered cells, not the byte stream, so color codes, `\r`
  progress frames, and cursor movement never reach the clipboard. Sanitization
  is an echo-time concern, not a payload concern. (lead-paste-hazards)

- **niwa never persists the prompt anywhere** -- the disclosure surface is argv
  and `ps`, not disk. An inline prompt is a net improvement because it removes
  the shell-history copy that `"$(cat)"` creates. (lead-paste-hazards)

- **The new code cannot live in `internal/tui/picker.go`.** That file's header
  (`picker.go:6-14`) declares it a byte-for-byte vendored copy of tsuku's with a
  standing mirror-or-document-the-divergence contract. (lead-dispatch-flow,
  lead-impl-surface)

- **Ctrl+J is the house standard and already the user's muscle memory**
  (Claude Code's `chat:newline` default). Shift+Enter is a dead end: only
  reportable via the kitty protocol or xterm `modifyOtherKeys`, permanently
  unavailable in GNOME Terminal/VTE, Terminal.app, and released Windows
  Terminal. Ctrl+O is already taken in this product category and should come off
  the list. Nobody uses double-Enter. (lead-newline-affordance)

- **Windows is out of scope entirely** -- `.goreleaser.yaml:10-15` builds only
  linux and darwin, which removes the ConPTY marker-loss class and the strongest
  argument for a heavier input library. (lead-bracketed-paste)

### Tensions

- **`x/term.Terminal.ReadLine` vs a hand-rolled reader -- two agents disagree
  about the same package.** lead-impl-surface designs around an
  `ErrPasteIndicator` accumulation loop over `ReadLine` (which returns once per
  *line*, since `handleKey` at `terminal.go:509` exempts Enter/LF from paste
  passthrough). lead-bracketed-paste argues against `ReadLine` specifically,
  because its repainting line editor is the per-character render cost behind
  every large-paste hang found in the wild (Claude Code, Gemini CLI, Cursor),
  and recommends a small paste-aware reader on `term.MakeRaw` that borrows only
  the marker-parsing idea. Both agree on zero new dependencies; they disagree on
  which part of `x/term` to use.

- **Raw mode: necessary cost or avoidable?** lead-newline-affordance frames raw
  mode as the real fork -- it buys chords but inherits a line editor's worth of
  responsibility (cursor movement, backspace, resize, termios restoration on
  signal) -- and closes by asking whether it is worth taking at all, since a
  single typed annotation line might be covered by `\`+Enter alone. lead-prior-art
  effectively answers yes: Ctrl+J is byte-identical to Enter in canonical mode,
  so every chord affordance requires raw mode, and annotation is in scope. The
  evidence favors taking raw mode, but the cost is real and was named twice.

- ~~**Overflow policy contradicts guidance already in the tree.**~~ **Withdrawn.**
  lead-dispatch-flow flagged the `/dispatch` skill's "Don't paste giant context
  into the prompt. Put it in the brief file" as contradicting this feature. It
  does not: the skill is agent-facing, and an agent will happily do the
  indirection; this feature is human-facing, and a human will not. Different
  callers, different correct answers, nothing to reconcile. Resolved during
  round 1 convergence -- see the decisions file.

- **Echo fidelity is a genuine fork with no free option.** Sanitize the display
  but send raw bytes, and the user is not seeing what they send; sanitize both,
  and the payload loses bytes that may be signal. Also,
  `SanitizeDisplayString` does not currently match CSI sequences whose final
  byte is not a letter -- including the bracketed-paste markers themselves.
  (lead-paste-hazards, lead-impl-surface)

- **Cancel semantics diverge from the house contract.** Ctrl-C inside
  `x/term.ReadLine` returns `io.EOF` (`terminal.go:825-827`), conflating a
  deliberate cancel with closed stdin, whereas niwa's picker went out of its way
  to separate them via `tui.ErrCanceled` (`picker.go:26-30`). Matching the house
  convention means intercepting `0x03` before handing bytes to `Terminal`.
  (lead-impl-surface)

### Gaps

- **Nobody has round-tripped a real log.** A mouse-selected stack trace arrives
  hard-wrapped at the pane width with colors stripped. Whether that degrades the
  payload enough to matter for a dispatched worker is untested and cheap to
  test. This is the single largest unknown about whether the feature delivers
  what the user wants.

- **Whether a mouse selection carries a trailing newline** was not definitively
  answered and plausibly varies by terminal and by whether the selection ends
  mid-line. It determines whether the annotation lands on a fresh line for free
  -- which would make the newline chord near-vestigial and shrink the raw-mode
  argument considerably.

- **`gum`'s current binding is unverified.** Its README says Ctrl+D completes;
  its source on `main` binds Enter to submit and Ctrl+J to newline. Read via
  raw.githubusercontent, not run. Should be confirmed before being cited as
  evidence that Ctrl+D was abandoned.

- **No signal handler anywhere in niwa restores terminal state** -- `picker.go:86`
  relies on `defer` alone. A raw-mode prompt would inherit that gap for
  SIGTERM/SIGHUP. bash repairs a raw terminal at its next prompt; dash does not.

- **Paste-before-ready** -- if the user pastes immediately on launch, some bytes
  may hit the terminal before mode 2004 is enabled. Not discussed anywhere found;
  may be a real edge.

- **Terminal-level paste dialogs** (Windows Terminal `largePasteWarning`, iTerm2
  and GNOME Terminal multi-line confirmations) interrupt the flow before any
  bytes reach niwa. Undecided whether to document or detect.

### Open Questions

1. ~~What happens when a paste exceeds the cap?~~ **Decided:** cap and error at
   the paste boundary, prompt still alive. No auto-spill. See decisions file.
2. ~~Should `maxPromptBytes` be fixed as part of this work?~~ **Decided:** yes,
   it is load-bearing once the cap is the overflow strategy.
3. Hand-roll the reader on `term.MakeRaw`, or build on `x/term.Terminal.ReadLine`?
4. Does the echoed buffer contain the same bytes as the dispatched payload?
5. Where does the code live -- `internal/cli` (unencumbered) or `internal/tui`
   (subject-matter fit, inherits the tsuku mirror obligation)?
6. Is `-` worth shipping as an explicit stdin sentinel, given scripted piping is
   not a design driver but hang-avoidance is?
7. Should piped stdin pre-fill an editable buffer (the `gum write` model) or run
   headless (the `gh`/`jj` model)? Any buffer-opening behavior must be
   conditional on `/dev/tty` being available so scripts never block.
8. Should the interactive path be reachable when `--detach` is set?
9. Should `--label` auto-derive from the paste's first line? If so, sanitization
   becomes mandatory at a surface that is both persisted and rendered.

### Decisions

_Pending user response to the narrowing question._

### User Focus

_Pending._

## Accumulated Understanding

The capture mechanism is no longer the open problem. Six leads converged on the
same answer from different directions: enable bracketed paste, let the pasted
block land inertly in a buffer, submit on Enter, offer Ctrl+J for a manual
newline. Five independently built tools already do exactly this, the user's own
muscle memory from Claude Code matches it, `golang.org/x/term` already ships the
protocol so it costs no new dependencies, and the one correct insertion point in
`runDispatch` makes abort a no-op while preserving the default attach behavior
that the `$(cat)` workaround structurally destroyed.

What remains open splits into three groups. First, implementation-level choices
that need a call but not more research: which part of `x/term` to build on,
where the file lives, whether the echo and the payload are the same bytes, and
how cancel maps to the house `ErrCanceled` convention. Second, one product
decision with real UX consequences -- what happens to an oversized paste, where
"reject and lose it" is the current behavior and the worst option, and where a
spill-to-brief-file design would both sidestep `ARG_MAX` and reconcile the
feature with the `/dispatch` skill's existing guidance. Third, an empirical gap
nobody closed: whether a mouse-selected, hard-wrapped stack trace is still
useful to the worker that receives it.

Separately, the exploration surfaced a live bug worth fixing regardless of what
happens to this feature: `maxPromptBytes` sits exactly on `MAX_ARG_STRLEN`
rather than below it, and the keep-alive prepend evades the check entirely, so
sufficiently large prompts pass validation and then fail at exec after the
instance has already been provisioned.

## Decision: Crystallize

User confirmed round 1 findings are sufficient. Proceeding to artifact type
selection.
