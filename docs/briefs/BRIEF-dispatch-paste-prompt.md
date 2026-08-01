---
schema: brief/v1
status: Accepted
problem: |
  `niwa dispatch` takes its prompt as a single positional argument, so the
  multiline error text a developer is looking at cannot be handed to a
  dispatched worker without retyping it, summarizing away the detail that
  made it diagnosable, or getting shell quoting right on the first try.
outcome: |
  A developer who hits a failure can select it, run `niwa dispatch`, paste,
  and send in one gesture, landing in a session already working on it --
  with the option to add a line of their own context first, and without an
  intermediate file or any shell quoting.
motivating_context: |
  The recurring pattern: a command fails, the output is right there on
  screen, and the developer wants that exact text working in its own
  session. Today they either give up on dispatching and handle it inline,
  or retype the parts they think matter. The material most worth handing
  off is the material hardest to hand off.
---

## Status

Accepted

Framing for inline prompt capture on `niwa dispatch`. Stops at the feature's
problem, outcome, journeys, and scope boundary. The downstream PRD owns the
size ceiling and how it is communicated, the behavior when the terminal
cannot carry the capture, and the acceptance criteria; the DESIGN owns the
capture mechanism, the submit and newline gestures, and where the reader
lives in the tree.

Phase 4 jury returned both-PASS. The four open questions this brief carried in
Draft -- the size ceiling and how the developer learns of it, the behavior when
the terminal cannot carry an interactive capture, whether capture composes with
a request not to attach, and what a non-interactive invocation does beyond not
hanging -- resolve in the downstream PRD's Decisions and Trade-offs section.

Edited in place after acceptance. The Problem Statement originally claimed the
detach flag was mandatory for the `"$(cat)"` workaround, on the reasoning that
standard input had been spent feeding the prompt. That is wrong: a command
substitution leaves standard input attached to the terminal, verified by
probe, so the default attach works. Detachment is only forced in the piped
variant. The correction matters downstream -- left standing, it would send a
designer looking for a consumed file descriptor that was never consumed.

## Problem Statement

`niwa dispatch` launches a background worker in a fresh instance and takes
the task as its prompt. The prompt arrives as a single positional argument.

That works when the developer can say what they want in a sentence. It stops
working for the case that recurs most: a command has just failed, the error
or stack trace is on screen, and that exact text is what the worker needs.
Getting it into the argument means one of three unappealing moves. Retyping
it is out. Summarizing it discards the specific line numbers, paths, and
symbol names that made it diagnosable in the first place -- the developer
ends up handing over their guess about the error instead of the error.
Wrapping it in shell quoting means getting the quoting right on the first
try, against text full of quotes, backslashes, and dollar signs that the
shell will happily reinterpret.

There is a workaround, and its shape shows the size of the gap. A developer
who already knows it can write `niwa dispatch "$(cat)"`, paste, and press
Ctrl-D. That is what a developer does today and nothing this feature
proposes to keep -- it is cited here as evidence of the gap, not as a
candidate gesture. It takes two pieces of knowledge, neither discoverable
from the command's help: that a command substitution can stand in for the
argument at all, and that the capture ends on an end-of-input key rather than
on Enter. It also runs blind -- nothing echoes back what was captured, so the
developer submits a prompt they have not seen. And it is fragile in a way
that punishes the obvious variation: piping into it (`some-command | niwa
dispatch "$(cat)"`) leaves standard input an exhausted pipe, so the default
attach has nothing to read from and the flow only works with detachment added.

The felt result is that dispatching gets reserved for work the developer can
describe from memory, and the failures scrolling past in the terminal -- the
things most worth handing to a worker -- stay in the terminal.

## User Outcome

A developer who hits a failure hands it off in one gesture. They select the
output with the mouse the way they already do, run `niwa dispatch`, paste,
and send. The session starts on that text, and they are attached to it and
watching -- not returned to a shell wondering whether it worked.

Nothing has to be decided in advance. The developer who wants to say
something about the error -- what changed, what they already ruled out --
types it alongside the pasted text before sending, using the same gesture
as the developer who sends the log bare. Neither has to know a flag, an
end-of-input key, or a quoting rule, and neither has to leave the terminal
or create a file to carry text they are already looking at.

When the text is too large to carry, the developer finds out while their
input is still in front of them and can send a smaller slice, rather than
losing the paste and starting over.

## User Journeys

### Handing off a bare failure

A developer mid-refactor runs the test suite and watches it break. They
select the failing output with the mouse, run `niwa dispatch`, paste, and
send. The pasted text is the entire instruction -- they add nothing, because
the log says everything they know. The session comes up working on that
output and the developer is attached to it, watching it start.

### Handing off a failure someone else hit

A developer picks up a CI failure they did not cause, on a branch they have
not been working in. The log alone would send a worker down paths that are
already ruled out, and the developer knows things it does not show -- which
dependency moved, which suspicion has already been eliminated. They paste
the log and type that context alongside it, in one continuous pass at the
prompt, using the same gesture as the developer above rather than a
different mode. The worker starts with both the evidence and the elimination
already done.

### Running past the size ceiling

A developer triaging a failure they cannot yet localize pastes the whole CI
run rather than an excerpt, because they do not know which part matters.
Rather than the input being silently truncated, or accepted and then failing
after an instance has already been created, they are told at the moment of
the paste, while the prompt is still accepting input. They send a smaller
slice instead, without having lost their place or created anything that
needs cleaning up.

### Backing out mid-capture

A developer working over SSH on a remote host starts a capture, then thinks
better of it -- wrong repository, or the failure turns out to be their own
uncommitted change. They abandon the capture. No instance has been created,
nothing needs reclaiming, and the terminal is left in working order for the
next command.

## Scope Boundary

The Out list rules out user-facing properties, not implementations. Named
mechanisms appear only to illustrate the property being excluded; which
mechanism carries the capture is the DESIGN's call among whatever survives
these boundaries.

### In

- **Interactive capture in the terminal the developer is already in.**
  Getting a multiline prompt into `niwa dispatch` without leaving the shell.
- **One gesture for both the bare paste and the annotated one.** No mode to
  choose, and no decision the developer has to make before they start.
- **A ceiling the developer learns about while their input is still
  recoverable.** Being told at the moment of the paste rather than after.
- **A clean abandonment.** A capture the developer backs out of leaves the
  terminal usable and the workspace unchanged.
- **The existing attach behavior, preserved.** The developer stays attached
  to the session they just started, rather than being detached as the
  present workaround forces.

### Out

- **Solutions that take the developer out of the terminal.** An editor
  buffer -- `$EDITOR`, the way `git commit` does it -- would solve
  termination and annotation at once, and it is still excluded: the round
  trip is heavier than the gesture this feature exists to shorten.
- **Solutions that assume the clipboard is on the same machine as the
  shell.** Reading the system clipboard directly (a `--clipboard` flag) would
  make termination a non-question, but it fails whenever the developer is
  working over SSH, which is common enough to disqualify it as the primary
  path.
- **Solutions that require the developer to create a file first.** Pointing
  the command at a path (a `--prompt-file` flag) is the shape this feature
  exists to avoid.
- **Scripted piping as a design driver.** A piped invocation must not hang
  or misbehave, but shaping the feature around `command | niwa dispatch`
  optimizes for a caller that does not have this problem. Agents already
  have a better path: they write a brief file and reference it.
- **Changing how the prompt reaches the worker** once captured, or what the
  worker does with it. The prompt's journey after capture is unchanged.
- **Prompt synthesis.** Turning a conversation into a well-formed task brief
  is the `/dispatch` skill's job and stays there. This feature carries text
  the developer already has.

## References

- `docs/briefs/BRIEF-instance-dispatch.md` -- the framing for `niwa
  dispatch` itself, which this feature extends at its input surface.
- `docs/prds/PRD-instance-dispatch.md` -- the requirements contract the
  dispatch command was built against, including its prompt handling.
