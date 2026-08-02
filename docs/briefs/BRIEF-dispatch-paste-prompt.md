---
schema: brief/v1
status: Done
problem: |
  `niwa dispatch` carries its prompt as a single command argument, so the
  multiline error text a developer is looking at is hard to hand over and,
  past the size one argument holds, impossible -- the developer is turned
  back and told to park the text in a file and point at it themselves.
outcome: |
  A developer who hits a failure selects it, runs `niwa dispatch`, pastes,
  and sends in one gesture, landing in a session already working on it --
  with the option to add their own context first, and with no file to make,
  no quoting to get right, and no size to think about.
motivating_context: |
  The recurring pattern: a command fails, the output is right there on
  screen, and the developer wants that exact text working in its own
  session. Today they either give up on dispatching and handle it inline,
  or retype the parts they think matter. The material most worth handing
  off is the material hardest to hand off. The first release of this
  feature carried that material only up to the size a single command
  argument can hold, and refused the rest by asking the developer to do
  by hand the one thing niwa is better placed to do itself.
---

## Status

Done

Framing for inline prompt capture on `niwa dispatch`. Stops at the feature's
problem, outcome, journeys, and scope boundary. The downstream PRD owns how a
prompt too large for one command argument reaches the worker, the behavior
when the terminal cannot carry the capture, and the acceptance criteria; the
DESIGN owns the capture mechanism, the submit and newline gestures, where the
reader lives in the tree, and where a spilled prompt is written.

Phase 4 jury returned both-PASS. Three of the four open questions this brief
carried in Draft -- the behavior when the terminal cannot carry an interactive
capture, whether capture composes with a request not to attach, and what a
non-interactive invocation does beyond not hanging -- resolve in the downstream
PRD's Decisions and Trade-offs section. The fourth, the size ceiling and how
the developer learns of it, was answered there and then dissolved by the second
amendment below: there is no ceiling for the developer to learn about.

Edited in place after acceptance. The Problem Statement originally claimed the
detach flag was mandatory for the `"$(cat)"` workaround, on the reasoning that
standard input had been spent feeding the prompt. That is wrong: a command
substitution leaves standard input attached to the terminal, verified by
probe, so the default attach works. Detachment is only forced in the piped
variant. The correction matters downstream -- left standing, it would send a
designer looking for a consumed file descriptor that was never consumed.

Amended in place a second time, after the feature shipped, to remove the size
ceiling from the framing. The original brief treated the ceiling as a fact of
the world and asked only that the developer learn about it early enough to
recover. Using the shipped capture showed the ceiling is not a fact of the
world: it is a property of one transport, and niwa is better placed to work
around it than the developer is. The refusal message already names the remedy
-- write the text to a file and reference the path -- which is a set of steps
niwa can take itself without the developer ever seeing them. The amendment
replaces the ceiling journey with an oversized-handoff journey, moves size
handling into the In list, and narrows the Out list so that "the developer
creates a file" stays excluded while "niwa creates a file" does not.

The downstream PRD and DESIGN were re-opened by the same amendment; both
carried the ceiling as a settled decision.

## Problem Statement

`niwa dispatch` launches a background worker in a fresh instance and takes
the task as its prompt. The prompt arrived as a single positional argument
and nothing else.

That worked when the developer could say what they wanted in a sentence. It
stopped working for the case that recurs most: a command has just failed, the
error or stack trace is on screen, and that exact text is what the worker
needs. Getting it into the argument meant one of three unappealing moves.
Retyping it was out. Summarizing it discarded the specific line numbers,
paths, and symbol names that made it diagnosable in the first place -- the
developer ended up handing over their guess about the error instead of the
error. Wrapping it in shell quoting meant getting the quoting right on the
first try, against text full of quotes, backslashes, and dollar signs that
the shell will happily reinterpret.

There was a workaround, and its shape showed the size of the gap. A developer
who knew it could write `niwa dispatch "$(cat)"`, paste, and press Ctrl-D --
cited here as evidence of the gap, never as a candidate gesture. It took two
pieces of knowledge, neither discoverable from the command's help: that a
command substitution can stand in for the argument at all, and that the
capture ends on an end-of-input key rather than on Enter. It also ran blind --
nothing echoed back what was captured, so the developer submitted a prompt
they had not seen. And it was fragile in a way that punished the obvious
variation: piping into it (`some-command | niwa dispatch "$(cat)"`) leaves
standard input an exhausted pipe, so the default attach has nothing to read
from and the flow only works with detachment added.

The felt result was that dispatching got reserved for work the developer
could describe from memory, and the failures scrolling past in the terminal --
the things most worth handing to a worker -- stayed in the terminal.

Interactive capture closed that gap, and closing it exposed a second one
inside it. The second gap was always true and only became felt once the
capture made it reachable: the argument cap applied to the `"$(cat)"`
workaround too, but a developer hitting it there read it as the shell's
problem rather than niwa's. A prompt reaches the worker as one command
argument, and an
operating system caps how long a single argument can be. So the command has a
size limit, and past it the paste is refused. The refusal is not unhelpful --
it names the remedy: write the text to a file, then dispatch a prompt that
references the path. But that remedy is a sequence of steps with no judgment
in it. Nothing about choosing the filename, writing the bytes, or composing
the pointer sentence requires anything the developer knows and niwa doesn't.
The limit belongs to a transport niwa picked; asking the developer to route
around it is asking them to compensate for an internal decision they never
made and cannot see.

The cost lands exactly where the feature is most useful. The pastes that fit
comfortably are the ones a developer could have summarized anyway -- a panic,
a failing test. The ones that do not fit are whole runs and whole build logs:
the cases where the developer genuinely does not know which part matters,
which is precisely why they wanted to hand over all of it. So the limit is at
its most expensive in the situation the feature exists to serve, and it is
felt as a wall rather than as a constraint, because from where the developer
stands there is no difference between a prompt niwa passes along and a prompt
niwa parks in a file and points at.

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

Size never enters it. The developer who pastes eight lines and the developer
who pastes an entire CI run take the same actions and get the same result: a
session working on the text they handed over. Whether that
text travelled as an argument or was parked in a file the worker reads is
niwa's business, decided from the size of what was submitted, and it changes
nothing the developer has to do. What they can still see, when they look at
the session later, is enough of the text to recognize which handoff it was.

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
prompt, using the same gesture as the developer in "Handing off a bare
failure" rather than a different mode. The worker starts with both the
evidence and the elimination already done.

### Handing off text that cannot be excerpted

A developer looks at a CI run whose jobs execute in parallel, so the failure's
cause and its symptom are interleaved with unrelated output and separated by
thousands of lines. There is no excerpt to take: any slice small enough to
select by hand is a slice that has cut the relationship the worker needs. They
hand over the whole run. Nothing warns them, nothing asks them to cut it down,
and nothing asks them to put it somewhere first. The session comes up with the
run available to it, and the developer never learns that this handoff took a
different road than the eight-line one they sent an hour earlier.

### Recognizing a session after the fact

A developer who fanned out three workers over a morning comes back and looks
at the list. The one they dispatched a whole build log to is identifiable
from its opening line -- enough of the pasted text is visible to tell it apart
from the other two -- rather than showing only a filename they never chose.
They can still reach the full text if they want it.

### Dispatching oversized text from a script

An automation author already assembles a large prompt and passes it as an
argument, the way every scripted caller does. Their prompt grows past the
size a single argument can hold. The dispatch keeps working, unchanged, with
no flag to add and no new failure to handle -- because the decision about how
a large prompt travels is made from the prompt's size rather than from how it
arrived.

### Backing out mid-capture

A developer working over SSH on a remote host starts a capture, then thinks
better of it -- wrong repository, or the failure turns out to be their own
uncommitted change. They abandon the capture. No instance has been created,
nothing needs reclaiming, and the terminal is left in working order for the
next command.

## Scope Boundary

The Out list rules out user-facing properties, not implementations. Named
mechanisms appear only to illustrate the property being excluded; which
mechanism carries the capture, and which carries an oversized prompt, are the
DESIGN's call among whatever survives these boundaries.

### In

- **Interactive capture in the terminal the developer is already in.**
  Getting a multiline prompt into `niwa dispatch` without leaving the shell.
- **One gesture for both the bare paste and the annotated one.** No mode to
  choose, and no decision the developer has to make before they start.
- **Size handled by niwa rather than by the developer.** A prompt too large
  to travel as one command argument still dispatches, and the developer is
  neither told about the limit nor asked to work around it. How the text
  travels is chosen from its size at the moment it is submitted, on every
  path a prompt can arrive by.
- **Enough of the text visible afterwards to tell one session from
  another.** A handoff that travelled the long way is still recognizable
  from the outside, not reduced to an opaque reference.
- **A clean abandonment.** A capture the developer backs out of leaves the
  terminal usable and the workspace unchanged. A prompt niwa parks
  somewhere on the developer's behalf is reclaimed by the same lifecycle
  that reclaims everything else a dispatch creates.
- **The existing attach behavior, preserved.** The developer stays attached
  to the session they just started, rather than being detached as the piped
  form of the old workaround forced.

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
  exists to avoid. This excludes a file the developer writes, names, or
  passes -- not a file niwa writes for itself while carrying text the
  developer already submitted, which is invisible from the outside and is
  what the In list above admits.
- **A user-facing surface for the size decision.** No flag to force or
  forbid the long road, no threshold to tune, no prompt asking the
  developer to choose, and no path they are expected to type. A decision
  the developer can influence is a decision they have to understand, which
  reintroduces the thing being removed.
- **Any promise about what the worker does with a large prompt.** Carrying
  the whole run to the worker is this feature's job; whether the worker
  reads all of it, and how well it reasons over that much text, is not
  something the handoff can guarantee. Removing niwa's limit does not
  remove the worker's.
- **Scripted piping as a design driver.** A piped invocation must not hang
  or misbehave, but shaping the feature around `command | niwa dispatch`
  optimizes for a caller that does not have this problem. What is excluded
  is the piping shape, not large arguments from scripted callers -- those
  travel by the same size-driven route as everything else. An agent that
  writes a brief file and references it is still doing something sensible;
  it is no longer doing something it has to do.
- **A second way to start a worker.** How the prompt reaches the worker is
  now in scope, but only as a size-driven choice between two forms of the
  same handoff. The worker is still started the same way, in the same kind
  of instance, with the same lifecycle; nothing here adds a mode, a
  protocol, or a second entry point.
- **Prompt synthesis.** Turning a conversation into a well-formed task brief
  is the `/dispatch` skill's job and stays there. This feature carries text
  the developer already has.

## Downstream Artifacts

- `docs/prds/PRD-dispatch-paste-prompt.md` -- the requirements contract for
  the capture and, after this amendment, for how an oversized prompt reaches
  the worker.
- `docs/designs/current/DESIGN-dispatch-paste-prompt.md` -- the capture
  mechanism, the gesture set, the terminal lifecycle, and where a spilled
  prompt is written.

## References

- `docs/briefs/BRIEF-instance-dispatch.md` -- the framing for `niwa
  dispatch` itself, which this feature extends at its input surface.
- `docs/prds/PRD-instance-dispatch.md` -- the requirements contract the
  dispatch command was built against, including its prompt handling.
