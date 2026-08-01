---
schema: prd/v1
status: In Progress
problem: |
  A developer who hits a failure in the terminal cannot hand that failure to a
  dispatched worker. `niwa dispatch` takes its prompt as a single positional
  argument, so the error text on screen has to be retyped, summarized down to a
  guess, or wrapped in shell quoting that must be right on the first try. The
  known workaround takes two undiscoverable pieces of knowledge and runs blind,
  since nothing echoes back what was captured before it is sent.
goals: |
  Running `niwa dispatch` with no prompt opens an interactive capture in the
  terminal the developer is already in. Pasting a failure and sending it takes
  one gesture, whether or not the developer adds context of their own, and
  lands them attached to a session already working on it. The capture shows
  what it has taken, states its size limit while the input is still
  recoverable, restores the terminal on every exit path, and never blocks a
  scripted or hooked invocation.
upstream: docs/briefs/BRIEF-dispatch-paste-prompt.md
motivating_context: |
  The recurring pattern is that the material most worth handing to a worker --
  the exact text of a failure -- is the material hardest to hand over. The
  result is that dispatching gets reserved for work a developer can describe
  from memory, while the failures scrolling past stay in the terminal.
---

## Status

In Progress

Requirements for interactive prompt capture on `niwa dispatch`, downstream of
the Accepted BRIEF. The Decisions and Trade-offs section closes the four
questions that BRIEF deferred. DESIGN owns the capture mechanism, the submit
and newline gestures, where the reader lives, and how cancellation is signalled.

Revised twice after jury rounds returned all-FAIL. Requirement numbers R24 and
the original R2/R25 split are retired rather than reused, so the numbering stays
auditable against those rounds. The second revision resolves a contradiction
between the oversized refusal and the exit-path enumeration, states three
behaviors that had been assumed rather than required (rendering of captured
input, deletion of entered text, responsiveness), and rewrites six criteria that
a violating implementation could have passed.

## Problem Statement

`niwa dispatch` launches a background worker in a fresh instance and takes the
task as its prompt, arriving as a single positional argument. That works when a
developer can state the task in a sentence and stops working for the case that
recurs most: a command has just failed, the stack trace is on screen, and that
exact text is what the worker needs.

Getting it into the argument means retyping it, summarizing it down to the
developer's guess about the error rather than the error itself, or wrapping it
in quoting that has to survive text full of quotes, backslashes, and dollar
signs. A workaround exists -- `niwa dispatch "$(cat)"`, paste, Ctrl-D -- and its
shape measures the gap: two pieces of knowledge, neither discoverable from the
command's help, and a capture that runs blind, since nothing echoes back what
was taken before it is sent. Its obvious variation is worse: piping into the
substitution leaves standard input an exhausted pipe, so the default attach has
nothing to read and the flow only works once detachment is added.

Two facts sharpen the timing. The command's prompt size cap is currently
mis-set (issue #225): it sits exactly on Linux's per-argument `execve` limit
rather than below it, and the keep-alive instruction is prepended after the
check, so a prompt can pass validation and then die at exec after an instance
has been provisioned. Today that band is nearly unreachable, because a caller's
own exec caps what they can pass in. Interactive capture removes that outer
guard -- niwa would be building the string itself -- which turns a latent defect
into a reachable one.

## Goals

- A developer hands off a failure without leaving the terminal, creating a
  file, or getting shell quoting right.
- The bare paste and the annotated paste use the same gesture, with no mode to
  choose and no decision required before starting.
- The developer sees what has been captured before it is sent.
- The developer stays attached to the session they just started, as they would
  with a prompt passed as an argument.
- Oversized input is refused while it is still recoverable, with a message that
  names what to do instead.
- The terminal is left in working order on every exit path, including
  interruption.
- No scripted, hooked, or piped invocation blocks or changes behavior.

## User Stories

- As a developer mid-refactor whose test suite just broke, I want to select the
  failing output and hand it straight to a worker, so that I do not have to
  summarize an error I do not yet understand.
- As a developer picking up a CI failure I did not cause, I want to add what I
  already know alongside the pasted log, so that the worker does not re-derive
  paths I have eliminated.
- As a developer triaging a failure I cannot localize, I want to be told my
  paste is too large while my input is still on screen and removable, so that I
  can cut it down and send a slice instead of starting over.
- As a developer who changes their mind mid-capture, I want to back out and
  find my terminal working normally, so that the next command behaves.
- As a developer who opens the capture and then realizes I have nothing to
  paste, I want submitting nothing to end the command cleanly rather than
  dispatch an empty task.
- As an author of a hook that runs `niwa dispatch` with no prompt by mistake, I
  want the command to fail immediately rather than wait forever on input that
  will never arrive, so that a scheduled job fails visibly instead of hanging.
- As an operator whose cron job calls `niwa dispatch` with a prompt argument, I
  want that path to behave exactly as it does today, so that nothing I have
  automated starts waiting on input.

## Dependencies

- **D1.** This PRD's size requirements are stated over the baseline established
  by issue #225, which corrects the prompt cap and adds a check immediately
  before exec. R14's derivation names terms introduced there. Interactive
  capture removes the outer guard that currently makes the defect nearly
  unreachable, so shipping capture on the uncorrected cap is not an option. If
  #225 has not landed when this work starts, R34 states what this work must
  carry instead.
- **R34.** If the #225 baseline is absent, this work SHALL itself establish it:
  the prompt SHALL be validated against R14's derived ceiling before any
  instance is created, and the final argument SHALL be re-checked immediately
  before the worker process is started, so that a prepend which fails to declare
  itself in the reserve is reported against niwa's own named limit rather than
  surfacing as an operating-system exec failure after provisioning.

## Requirements

### Entry and argument contract

- **R1.** `niwa dispatch` invoked with no positional argument on an interactive
  session SHALL open a prompt that captures multiline text.
- **R2.** `niwa dispatch` invoked with one positional argument SHALL NOT consult
  the terminal state and SHALL NOT open a capture.
- **R3.** The argument contract SHALL accept zero or one positional argument.
  Two or more SHALL remain an error naming the expected form, so a developer who
  forgot to quote a multi-word prompt gets a diagnostic rather than a capture.
  Because arity selects the path, no invocation can request both a positional
  prompt and a capture.
- **R12.** `niwa dispatch ""` SHALL continue to fail with the existing
  empty-prompt error. An explicit empty argument is not a capture trigger.
- **R13.** `--detach` SHALL compose with capture. When set, the capture runs and
  the command returns without attaching; when unset, the developer is attached
  as they are on the argument path. No flag combination is newly rejected.

### Capture behavior

- **R4.** On terminals that delimit pasted blocks, multiline input SHALL be
  captured whole: a line break inside a pasted block SHALL NOT terminate the
  capture, submit the input, or truncate it, whether it arrives as a carriage
  return, a line feed, or both.
- **R40.** On terminals that do not delimit pasted blocks, a pasted line break is
  indistinguishable from a typed one, so a multiline paste submits at its first
  line break and the remainder reaches the shell. The command SHALL NOT probe for
  the capability (R23) and SHALL NOT attempt to detect this state. The
  degradation SHALL be documented where a developer will find it.
- **R35.** Input accepted so far SHALL be rendered as it is entered, so the
  developer can see what will be sent before sending it.
- **R30.** Text the developer submits SHALL be preserved exactly in the prompt,
  including any terminal control sequences it contains. The rendering required
  by R35 SHALL neutralize control sequences so that pasted content cannot alter
  the display. What is rendered SHALL NOT determine what is sent.
- **R36.** The capture SHALL allow the developer to remove text they have
  entered. The gesture is DESIGN's; the capability is required, because R17's
  refusal promises the developer can reduce their input.
- **R5.** The developer SHALL be able to type text alongside pasted text and
  send both together, using the same submit gesture as a bare paste. No flag,
  mode, or prior decision SHALL be required to reach either case. On terminals
  that delimit pasted blocks, the end of a pasted block SHALL be treated as a
  line boundary: if the block's final line was unterminated, exactly one line
  feed SHALL be inserted before subsequent typed text. On terminals that do not
  delimit pasted blocks this is not detectable and the developer supplies the
  break themselves via R6.
- **R6.** The capture SHALL provide a means of entering a newline manually,
  distinct from the submit gesture.
- **R28.** End-of-input on a non-empty buffer SHALL submit the accumulated text,
  behaving as a submit gesture. End-of-input on an empty buffer SHALL end the
  command without dispatching.
- **R29.** Submitting an empty or whitespace-only capture SHALL fail with the
  existing empty-prompt error rather than dispatching.
- **R37.** Input up to the R14 ceiling SHALL be accepted without perceptible
  stall, including when it arrives as a single line. Accepting a payload the
  ceiling admits SHALL NOT take materially longer than accepting the same number
  of bytes spread across many lines.

### Cancellation, exit paths, and terminal state

- **R7.** Abandoning the capture SHALL create no instance, no session mapping,
  and no other durable state.
- **R8.** Abandonment SHALL be distinguishable from end-of-input in the captured
  result, so the command reports a cancelled capture rather than dispatching or
  reporting an empty prompt.
- **R9.** On every path that ends the command -- submit, abandonment, the
  empty-capture refusal, and receipt of SIGINT, SIGTERM, or SIGHUP -- the
  terminal's mode SHALL be restored to its state before the capture began. The
  oversized refusal is not on this list because it does not end the command
  (R17).
- **R38.** If the capture is suspended and resumed, the terminal's mode SHALL be
  re-established on resume, so a suspended capture does not leave the terminal
  altered for a foregrounded shell.
- **R39.** Receipt of SIGINT during a capture SHALL restore the terminal and
  abandon cleanly, satisfying R7 and R9. Whether the interrupt reaches the
  process as a signal or as an input byte follows from DESIGN's terminal-mode
  choice; the observable outcome does not.
- **R31.** The non-TTY refusal (R20), the empty-capture refusal (R29), and
  abandonment SHALL each exit non-zero with the command's ordinary error exit
  status. No new exit code is introduced. The oversized refusal has no exit
  status of its own, because the developer's next action determines how the
  command ends.

### Reachability

- **R11.** A caller that dispatches without going through the interactive
  command path -- including `niwa watch`, which invokes the launcher directly --
  SHALL never open a capture, under any argument or flag combination.

### Size ceiling

- **R14.** The prompt size ceiling SHALL be `maxArgStringBytes -
  dispatchPromptReserve`, where `maxArgStringBytes` is the largest single argv
  string the operating system accepts on the tightest supported platform and
  `dispatchPromptReserve` is the length of everything niwa may prepend to the
  prompt after validation. It SHALL be expressed as that derivation, not as a
  literal, so a change to either term visibly moves it. On the current baseline
  it evaluates to 130,433 bytes (roughly 127 KB).
- **R15.** A single ceiling SHALL apply on every supported platform. No
  platform-conditional definition SHALL exist.
- **R16.** The ceiling SHALL be enforced against text captured interactively,
  not only against a positional argument, and the reserve SHALL be subtracted on
  both paths.
- **R17.** Input SHALL be refused at the moment it crosses the ceiling, before
  any instance is created. The capture SHALL remain open and SHALL retain the
  entire buffer including the input that crossed the ceiling, so the developer
  can delete down to a submittable size (R36) rather than lose what they pasted.
  A buffer above the ceiling SHALL NOT be submittable.
- **R18.** The refusal message SHALL state the size of the input and the limit,
  both in bytes, and SHALL direct the developer to write the text to a file and
  dispatch a prompt referencing that path. It SHALL NOT instruct the developer
  to shorten or re-select the text.
- **R19.** No per-line length limit SHALL apply to captured input. A single line
  longer than the terminal's line-discipline buffer SHALL be captured intact,
  without truncation and without hanging.
- **R10.** The bytes the developer submitted SHALL appear byte-for-byte in the
  worker's argv, in the same single argv element the positional path produces,
  subject only to three stated exceptions: the prepend accounted for in R14, the
  single line feed R5 may insert at a paste boundary, and the line-break
  normalization required by R41. No other trimming, normalization, or re-encoding
  SHALL be applied.
- **R41.** Line breaks inside a pasted block SHALL be normalized to a single line
  feed: a lone carriage return, and a carriage return followed by a line feed,
  each become one line feed. This is required because terminals differ in which
  byte they deliver for a pasted line break, and a prompt whose lines are
  separated by carriage returns reaches the worker as one unreadable line --
  defeating the purpose of carrying the error verbatim. No other byte inside a
  pasted block SHALL be altered.

### Non-interactive and degraded terminals

- **R20.** When no positional argument is supplied and the session is not
  interactive, the command SHALL fail immediately with a message naming the
  positional-argument form that works, and SHALL NOT read from standard input.
- **R21.** The interactivity test SHALL require both standard input and standard
  error to be terminals, since the capture reads the former and renders to the
  latter while standard output carries the command's existing session hints.
- **R22.** Standard output SHALL remain redirectable without affecting the
  capture's behavior beyond R21's gate.
- **R23.** The command SHALL NOT probe the terminal for capability, and SHALL
  NOT warn about a missing capability.
- **R27.** Before its first read the capture SHALL render human-readable text
  indicating that the command is waiting for input, so a promptless invocation
  from a script that inherits a terminal is visibly stalled rather than silently
  so. Control sequences alone do not satisfy this.

### Preservation

- **R25.** Relative to the baseline established by issue #225, no behavior on
  the positional-argument path SHALL change: same exit codes, same messages,
  same argv construction. The R14 ceiling applies to both paths, which is part
  of that baseline rather than a change this feature introduces.
- **R26.** Failures reachable before provisioning SHALL be raised before
  provisioning, so a rejected capture never leaves an instance to reclaim.

### Supporting changes

- **R32.** Documentation stating or implying that the prompt argument is
  mandatory SHALL be updated: the command's own usage and long help, and the
  repository README.
- **R33.** Every functional-test step that hands the binary a standard input the
  step does not control SHALL carry a bounded timeout, so a scenario that fails
  to terminate fails as a step rather than exhausting the suite's global
  deadline. This covers both the terminal-driven step and the held-open-pipe
  step R20's criterion requires.

## Acceptance Criteria

Each criterion names the requirements it verifies, and is filed at the level
whose harness can make it fail.

### Unit tests over an injectable capture core

- [ ] A delimited paste whose line breaks are line feeds does not return after
      the first line; the same delimited paste with carriage-return breaks also
      does not (R4).
- [ ] Undelimited input containing a line break returns at that break, pinning
      the documented degradation so a future change to it is deliberate (R40).
- [ ] Input arriving across multiple reads, split at an arbitrary boundary, is
      captured whole (R4).
- [ ] One submit gesture returns the captured text for a bare paste (R4).
- [ ] For a delimited paste ending mid-line followed by typed text, the returned
      string equals the pasted bytes, one line feed, then the typed bytes --
      compared exactly (R5).
- [ ] The manual-newline gesture inserts a newline and does not submit (R6).
- [ ] Input accepted so far appears on the render target as it is entered (R35).
- [ ] End-of-input on a non-empty buffer returns the accumulated text; on an
      empty buffer it returns the end-of-input outcome (R28).
- [ ] Abandonment returns a sentinel distinct from both end-of-input and a
      successful submit (R8).
- [ ] Deleting entered text reduces the buffer, and a buffer reduced from above
      the ceiling to below it becomes submittable (R36, R17).
- [ ] Typing A, then pasting B where A+B crosses the ceiling: the refusal fires,
      the buffer still contains A and B, and a submit attempt is refused until
      the buffer is reduced (R17).
- [ ] The refusal message contains both byte counts and directs the developer to
      a file-and-reference approach; it does not contain "shorten" (R18).
- [ ] Submitted text containing terminal control sequences is returned
      unmodified, while the bytes written to the render target contain no
      executable control sequence from the input (R30).
- [ ] Nothing written to the render target is a capability query sequence, and no
      capability warning text is emitted (R23).
- [ ] The bytes written before the first read contain non-empty human-readable
      text once escape sequences are stripped (R27).
- [ ] A single line of 130,433 bytes is accepted, and accepting it takes no more
      than a small constant multiple of the time taken to accept the same byte
      count split across many lines (R19, R37).

### Command-level unit tests over a capture seam

- [ ] The ceiling constant equals 130,433 on the current baseline, stated
      independently of the implementation's own derivation (R14, R15).
- [ ] A prompt one byte over the ceiling is refused and a prompt at the ceiling
      is accepted (R14).
- [ ] A prompt sized between the ceiling and the ceiling plus the reserve is
      refused before provisioning, on both the capture path and the argument
      path (R16, R26, R34).
- [ ] With no argument and an interactive session, the capture is invoked and its
      text becomes the launcher's final argv element (R1, R10).
- [ ] A submitted payload containing quotes, backslashes, and dollar signs
      arrives byte-for-byte as one argv element (R10).
- [ ] A pasted block whose line breaks are carriage returns, and one whose line
      breaks are carriage-return line-feed pairs, each arrive with single line
      feeds; every other byte in the block is unaltered (R41).
- [ ] Over the four combinations of (stdin is a terminal, stderr is a terminal),
      the capture runs only when both are true; the other three refuse without
      reading, and the refusal names the positional-argument form (R20, R21).
- [ ] With both terminal checks true and a non-terminal standard output, the
      capture still runs and its rendering goes to the error stream (R22).
- [ ] With a positional argument, the capture seam is never invoked and the
      terminal state is never consulted (R2).
- [ ] Two or more positional arguments produce an argument error, not a capture
      (R3).
- [ ] An abandoned capture provisions nothing (R7, R26).
- [ ] An empty or whitespace-only submission fails with the empty-prompt error
      and provisions nothing (R29).
- [ ] `niwa dispatch ""` fails with the existing empty-prompt error (R12).
- [ ] `--detach` with a capture returns without attaching; without `--detach`,
      the attach path runs (R13).
- [ ] Driving the launcher path used by `niwa watch` with the capture seam
      stubbed to fail on call never invokes the stub (R11).
- [ ] The non-TTY refusal, the empty refusal, and abandonment each return a
      non-zero status (R31).
- [ ] On the positional path, exit codes, messages, and argv construction match
      goldens recorded from the issue #225 baseline before this change (R25).

### `@critical` functional scenarios

- [ ] A pasted multiline block dispatches, and the launched worker's argv
      contains the pasted text verbatim (R1, R4, R10).
- [ ] `niwa dispatch` with no argument and standard input attached to a pipe that
      is never written to and never closed exits within a bounded time rather
      than blocking (R20, R33).
- [ ] The terminal's mode after an abandoned capture matches its mode before
      (R9).
- [ ] The terminal's mode after a normal submit matches its mode before (R9).
- [ ] The terminal's mode after the capture receives SIGTERM matches its mode
      before; likewise for SIGHUP (R9).
- [ ] The terminal's mode after an interrupt during capture matches its mode
      before, and no instance is created (R39, R7).
- [ ] The terminal's mode after the capture is suspended and resumed matches its
      mode before (R38).
- [ ] The terminal's mode after an empty-capture refusal matches its mode before
      (R9).
- [ ] An oversized paste followed by abandonment exits non-zero and leaves no
      instance (R17, R26).
- [ ] A single line longer than the line-discipline buffer, fed after the capture
      has started, is captured without hanging (R19).

### Verified by inspection

- [ ] No documentation states or implies that the prompt argument is mandatory:
      the command's usage string, its long help, and the README all describe the
      argument as optional (R32).
- [ ] Every functional step that supplies the binary a standard input it does not
      control carries a bounded timeout (R33).

### Verified manually before release

Against each of GNOME Terminal, Terminal.app, iTerm2, and Ghostty, and inside
tmux:

- [ ] A multiline paste is captured whole and is not truncated at its first line
      (R4).
- [ ] A large paste renders without visible corruption (R35, R37).
- [ ] The capture is visibly waiting before any input is given (R27).

## Decisions and Trade-offs

This section closes the four open questions carried forward from
`docs/briefs/BRIEF-dispatch-paste-prompt.md`.

### The size ceiling is derived, and commits to failure-shaped pastes

The BRIEF required the PRD to state the ceiling rather than inherit today's,
because today's is wrong in both value and coverage. The ceiling is stated as a
derivation (R14) so that a change to either term moves it visibly, with the
computed value given so a reader can tell whether the limit is 130 KB or 130
bytes.

The trade-off is which payloads this commits to serve. Measured: a Go panic is
about 5.6 KB, a failing `go test ./...` about 7.7 KB, a CI failure excerpt about
9.2 KB -- all far under. A whole run is not: `go test -v ./...` measures about
326 KB and a full CI log about 582 KB. Serving those would mean changing how the
prompt reaches the worker, which touches the guarantee that the prompt is a
single argv element never passed through a shell. This PRD commits to
failure-shaped pastes and rejects whole-log pastes with an actionable message.

That makes R18's wording load-bearing, because essentially everyone who sees the
error will be someone who pasted an entire run. The named alternative is to
write the text to a file and dispatch a prompt referencing that path. This is
ordinary prompt text pointing at a file, not the `--prompt-file` flag the BRIEF
excluded, so it survives the scope boundary -- and it is what the dispatch skill
already tells agents to do with large context.

### The oversized refusal is not an exit

An earlier revision listed the oversized refusal both as a state the capture
survives and as a path that ends the command, which cannot both be true. R17
settles it: crossing the ceiling refuses the input and leaves the capture open,
holding the whole buffer, including the text that crossed. The developer deletes
down to a submittable size (R36) or abandons. Discarding the overflowing paste
instead would satisfy the letter of "retain the text already entered" while
losing exactly what the requirement exists to save, since in the central case
the paste is the entire input.

### A non-interactive session refuses; a limited terminal degrades

The BRIEF asked what happens when the terminal cannot carry the capture. These
are two different questions with two different answers.

When the session is not interactive, the command refuses (R20). Every existing
non-TTY gate in niwa refuses with guidance rather than falling back, and
refusing satisfies the never-block requirement structurally: there is no read,
so there is nothing to block on. The rejected alternative was reading standard
input as the prompt. That would add a second implicit non-interactive channel
alongside the argument with no defined resolution when both are present, and it
would turn a mis-written hook's redirect from `/dev/null` into a silent empty
dispatch instead of an error.

When the session is interactive but the terminal lacks paste-boundary support,
there is nothing honest to say: enabling the capability is a silent no-op and
the resulting state is indistinguishable from ordinary typing. So the command
does not probe and does not warn (R23).

An earlier revision stated R4's no-truncation guarantee unconditionally, which
had a consequence that only became visible once the gestures were chosen: if a
pasted line break cannot be distinguished from a typed one, then Enter cannot be
the submit gesture, because submitting on Enter would truncate a paste at its
first line on exactly those terminals. That inverted the gesture set -- Ctrl-D
to submit, Enter to insert a newline -- which is the ceremony the BRIEF names as
part of the problem, and which five independently built terminal chat tools
rejected in favor of Enter.

The trade was taken deliberately: R4 is scoped to terminals that delimit pasted
blocks, R40 states the resulting degradation plainly and requires it be
documented, and a characterization criterion pins the degraded behavior so a
future change to it is a decision rather than an accident. Bracketed paste is
effectively universal in current terminals, transparent over remote sessions,
and forwarded by multiplexers, so the terminals this exposes are old ones. The
one behavior that genuinely does depend on paste boundaries is the separator R5
inserts, which is
therefore scoped to terminals that provide them.

### Capture and detach compose

The BRIEF flagged this as sitting against the commitment to preserve attach.
They compose (R13). `--detach` has one meaning -- skip the final attach -- and it
is independent of how the prompt was obtained. "Paste a prompt, then fan out
without attaching" is coherent, and the command's own help already describes
detach as the mode for fan-out and scripting.

### The non-interactive path is unchanged, and structurally so

The BRIEF asked what a non-interactive invocation does beyond not hanging. It
does exactly what it does today, and the requirements are written so this is
structural rather than careful: the interactivity check lives only in the
zero-argument branch (R2, R20), and capture is unreachable from any caller that
does not go through the interactive command path (R11). That second constraint
is load-bearing -- `niwa watch` calls the launcher directly, so capture logic
placed there would give a cron-driven review sweep an interactive read.

The argument contract widens from exactly one positional argument to at most one
(R3), matching the shape `niwa destroy` already uses. Two alternatives were
rejected: a `-` sentinel, which has no precedent here and pulls the feature
toward the piping shape the BRIEF excludes, and an explicit opener flag, which
would reintroduce the mode-choice the BRIEF rules out.

### End-of-input submits rather than discards

The workaround this feature replaces taught developers that Ctrl-D ends a
capture, so a developer will reach for it. R28 makes it a submit gesture rather
than a discard. Discarding would take the gesture a developer already has muscle
memory for and make it the one that loses their paste.

### Rendering and payload are independent

R35 requires the capture to show what it has taken, which the Problem Statement
names as a defect of the workaround rather than a nicety. R30 then splits
display from payload: a pasted log can contain terminal control sequences, and
echoing them raw is a display-corruption path, while sanitizing the payload
would silently alter the evidence the developer is handing over. Neutralizing on
the way to the screen and preserving on the way to the worker costs a little
care and avoids both.

### The interactivity gate covers standard error, not standard output

Requiring both standard input and standard error to be terminals (R21) follows
the existing gate in `niwa destroy`, and differs deliberately from tools that
gate on standard output: niwa renders prompts to standard error and reserves
standard output for the session hints a caller may want to redirect. The cost is
that redirecting standard error from an interactive terminal disables the
capture. That is judged not worth supporting, since the capture's own display
would go to the redirect target.

## Known Limitations

- The ceiling is unreachable for the payloads this feature exists to serve and
  reachable only by pasting a whole run. The refusal is a real path for a real
  user, not a defensive check, and its wording carries the weight.
- An earlier revision recorded an echo cost that appeared superlinear on a
  single long line. Instrumented re-measurement retired that: the child received
  4095, 4095, 2, 1 bytes and then nothing, because the test harness does a short
  write to the terminal and never retries the remainder. It is a harness defect,
  affects every candidate reader, and is not a property of any library. Driven
  by a terminal that retries short writes, a 130,433-byte single line is
  captured in about a millisecond. R37 stands as a requirement, but it does not
  rest on that measurement, and the functional harness needs the chunked feed
  described in R33's neighbourhood or its terminal-driven scenarios will fail
  for reasons unrelated to the code under test.
- A promptless invocation from a script that inherits an interactive terminal
  passes the interactivity gate and opens a capture. This is a caller bug; the
  mitigations are that the capture is visibly waiting (R27) and that abandonment
  is clean (R7, R9).
- On a terminal that does not delimit pasted blocks, a multiline paste submits at
  its first line break and the remainder reaches the shell (R40). This is the
  cost of taking Enter as the submit gesture, and it is not detectable from
  inside the process. It is documented rather than mitigated.
- Behavior inside multiplexers and the rendering quality of a large paste cannot
  be checked by any harness in this repository. They are manual criteria, which
  means they are checked at release time by a person or not at all.
- The reserve costs headroom on invocations where nothing is prepended.
  Recovering it would require resolving whether the prepend applies before
  provisioning, which this PRD does not require.
- The terminal-driven scenarios depend on a terminal utility whose behavior
  differs between platforms. Continuous integration runs on one of them, so the
  criteria are enforced there; a developer running the suite on another platform
  may find those scenarios unavailable. Whether they skip or fail on such a host
  is an implementation choice, but they should not fail silently.

## Out of Scope

- `$EDITOR`, a clipboard flag, and a prompt-file flag, excluded by the BRIEF as
  user-facing properties rather than as implementations.
- Scripted piping as a design driver. Non-blocking behavior is required (R20);
  optimizing for `command | niwa dispatch` is not.
- Reading standard input as the prompt, whether implicitly or behind a new flag.
- Changing how the prompt reaches the worker after capture, including any
  transport change that would raise the ceiling.
- Prompt synthesis, which the dispatch skill owns.
- The capture mechanism, the specific submit, newline, deletion, and
  cancellation gestures, the choice of terminal API, and where the reader lives
  in the tree. These are DESIGN decisions; the requirements above constrain them
  without making them.
- A global no-input flag. If niwa wants one it belongs on the root command.
- Changing what the generated workspace context or the dispatch skill tell
  agents. Both instruct agents to pass a positional prompt, which remains
  correct; capture is a human-facing affordance and adding it there would invite
  agents to reach for an interactive path they cannot use.

Correcting the prompt size cap is tracked separately as issue #225. The
correction is not excluded from this work: R34 requires it here if that issue
has not landed first.
