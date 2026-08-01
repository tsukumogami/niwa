# /brief Discovery: dispatch-paste-prompt

## Problem Candidate

When a command fails in the terminal, the error text is already on screen -- and
that is exactly the material a developer cannot hand to a dispatched worker.
`niwa dispatch` takes its prompt as a single positional argument, so getting a
multiline error into it means retyping or summarizing (which discards the specific
detail that made the error diagnosable), or wrapping it in shell quoting the
developer has to get right on the first try, or abandoning the handoff and
dealing with the failure inline. The text most worth handing off is the text
hardest to hand off.

## Outcome Candidate

A developer sees a failure, selects it, runs `niwa dispatch`, pastes, and presses
Enter -- and lands in a session already working on it. No intermediate file, no
shell quoting, no terminator to remember, and no need to decide in advance
whether they want to say something about the error before sending it.

## Grounding Anchor

conversation only

Substantial prior grounding exists in the `/explore` round-1 artifacts at
`wip/explore_dispatch-paste-prompt_{scope,findings,decisions}.md` and the six
research files under `wip/research/explore_dispatch-paste-prompt_r1_lead-*.md`.
Those are non-durable; this BRIEF is where the framing becomes durable.

## Journey Sketch

- **Bare handoff.** A build or test run fails; the developer selects the output
  with the mouse, runs `niwa dispatch`, pastes, and presses Enter. The log alone
  is the whole instruction.
- **Annotated handoff.** Same entry, but the developer adds a line of their own
  context -- what changed, what they already ruled out -- around the pasted log
  before sending.
- **Oversized paste.** The developer pastes more than the prompt can carry and is
  told at the moment of the paste, while the prompt is still accepting input, so
  they can paste a smaller slice instead of losing what they had.
- **Abandoned handoff.** The developer changes their mind mid-capture and backs
  out; nothing has been provisioned and the terminal is left in working order.

## Open Questions for Drafting

- Keep the BRIEF at framing altitude. Bracketed paste, the submit key, the
  manual-newline chord, and which terminal API carries the capture are DESIGN
  decisions, not framing. The BRIEF should describe the gesture and the outcome,
  not the mechanism.
- The framing risk to defend against in Phase 2: "`niwa dispatch` cannot accept
  piped input" is a missing feature, not a problem. The problem is the handoff
  gap; piping and pasting are candidate mechanisms for closing it.
- Scope boundary should record what the author explicitly pushed out during
  scoping: `$EDITOR`, a `--clipboard` flag, a `--prompt-file` flag, and scripted
  piping as a design driver.
- The oversized-paste journey rests on a cap. The cap's correctness is a known
  defect today (`maxPromptBytes` sits exactly on Linux's per-argument limit, and
  a later prepend evades the check), which the PRD and DESIGN will need to carry.
  The BRIEF states the journey, not the byte count.
