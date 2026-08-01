# Exploration Decisions: dispatch-paste-prompt

## Round 1

- **Optimize only for interactive paste.** `--prompt-file`, `--clipboard`, and
  scripted piping are out as design drivers. Piping may fall out of the design
  for free but does not shape it.
- **No `$EDITOR`.** Rejected outright; the capture stays inline in the terminal.
- **Support both a bare pasted log and an annotated one** without a mode switch.
  This is what forces the design to handle typing around a pasted block, and is
  the reason raw mode is on the table at all.
- **Cap the prompt and error past it; do not auto-spill to a brief file.** The
  spill design is what an agent should do, and the `/dispatch` skill already
  prescribes it for agents. Humans will not accept indirection to run a command,
  so the human-facing path takes the paste inline up to a hard limit. Supporting
  reason from research: niwa never persists the prompt today (disclosure surface
  is argv and `ps`, not disk), and auto-spilling would create a disk persistence
  surface that does not currently exist, for a case requiring a paste of roughly
  two thousand lines.
- **The `/dispatch` skill's "put giant context in a brief file" guidance is not
  in conflict with this feature.** Different callers, different correct answers.
  Nothing to reconcile in the tree; the tension recorded in round 1 findings is
  withdrawn.
- **`maxPromptBytes` must be fixed as part of this work.** Once the cap is the
  overflow strategy it becomes load-bearing: it currently sits exactly on
  Linux's `MAX_ARG_STRLEN` (131072) rather than below it, and the 638-byte
  keep-alive instruction is prepended after the check, so the effective limit is
  both wrong and state-dependent.
- **Enforce the limit at the paste boundary, with the prompt still alive.**
  Erroring out to the shell after a rejected paste loses work the user cannot
  recover; rejecting the paste while the prompt is still accepting input does
  not.
- **Ctrl+J is the manual-newline binding; Shift+Enter is not pursued.** Ctrl+J
  matches Claude Code's `chat:newline` default and the convergent choice across
  five tools. Shift+Enter is only reportable via the kitty protocol or xterm
  `modifyOtherKeys` and is permanently unavailable in several terminals in
  common use.
- **The new reader does not go in `internal/tui/picker.go`.** That file is a
  byte-for-byte vendored copy of tsuku's with a standing sync contract.
