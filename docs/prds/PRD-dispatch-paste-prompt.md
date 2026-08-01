---
schema: prd/v1
status: Draft
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
  lands them attached to a session already working on it. The capture states
  its size limit while the input is still recoverable, restores the terminal on
  every exit path, and never blocks a scripted or hooked invocation.
upstream: docs/briefs/BRIEF-dispatch-paste-prompt.md
motivating_context: |
  The recurring pattern is that the material most worth handing to a worker --
  the exact text of a failure -- is the material hardest to hand over. The
  result is that dispatching gets reserved for work a developer can describe
  from memory, while the failures scrolling past stay in the terminal.
---

## Status

Draft

Requirements for interactive prompt capture on `niwa dispatch`, downstream of
the Accepted BRIEF. The Decisions and Trade-offs section closes the four
questions that BRIEF deferred. DESIGN owns the capture mechanism, the submit
and newline gestures, where the reader lives, and how cancellation is signalled.

Revised after a three-reviewer jury returned all-FAIL. The revision closes four
requirements that had no acceptance criterion, retags eight criteria that
claimed requirements they did not exercise, replaces four criteria that could
not fail regardless of implementation, and defines five behaviors an
implementer would otherwise have had to invent.

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
  paste is too large while my input is still on screen, so that I can send a
  smaller slice instead of losing it.
- As a developer who changes their mind mid-capture, I want to back out and
  find my terminal working normally, so that the next command behaves.
- As a developer who opens the capture and then realizes I have nothing to
  paste, I want submitting nothing to end the command cleanly rather than
  dispatch an empty task.
- As an operator whose cron job calls `niwa dispatch` with a prompt argument, I
  want that path to behave exactly as it does today, so that nothing I have
  automated starts waiting on input.

## Dependencies

- **D1.** This PRD's size requirements are stated over the baseline established
  by issue #225, which corrects the prompt cap and adds a launcher backstop.
  R14's derivation names terms introduced there. If that work does not land
  first, this feature must carry the same correction, because interactive
  capture removes the outer guard that currently makes the defect nearly
  unreachable. Shipping capture on the uncorrected cap is not an option.

## Requirements

### Entry and argument contract

- **R1.** `niwa dispatch` invoked with no positional argument on an interactive
  session SHALL open a prompt that captures multiline text.
- **R2.** `niwa dispatch` invoked with one positional argument SHALL NOT consult
  the terminal state and SHALL NOT open a capture. (The preservation guarantee
  for that path is R25; this requirement states only that capture is not
  reachable from it.)
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

- **R4.** Multiline input SHALL be captured whole. An embedded newline SHALL NOT
  terminate the capture, submit the input, or truncate it, regardless of whether
  the terminal delimits pasted blocks. This is unconditional; it is not scoped to
  terminals with paste-boundary support.
- **R5.** The developer SHALL be able to type text alongside pasted text and
  send both together, using the same submit gesture as a bare paste. No flag,
  mode, or prior decision SHALL be required to reach either case. The submitted
  text SHALL preserve the boundary between pasted and typed content: typed text
  SHALL NOT be joined onto the final line of a pasted block that did not end in
  a newline.
- **R6.** The capture SHALL provide a means of entering a newline manually,
  distinct from the submit gesture.
- **R28.** End-of-input on a non-empty buffer SHALL submit the accumulated text,
  behaving as a submit gesture. End-of-input on an empty buffer SHALL end the
  command without dispatching.
- **R29.** Submitting an empty or whitespace-only capture SHALL fail with the
  existing empty-prompt error rather than dispatching.
- **R30.** Text the developer submits SHALL be preserved exactly in the prompt,
  including any terminal control sequences it contains. Rendering of the capture
  MAY sanitize control sequences for display; the two SHALL be independent, so
  what is rendered never determines what is sent.

### Cancellation, exit paths, and terminal state

- **R7.** Abandoning the capture SHALL create no instance, no session mapping,
  and no other durable state.
- **R8.** Abandonment SHALL be distinguishable from end-of-input in the captured
  result, so the command reports a cancelled capture rather than dispatching or
  reporting an empty prompt.
- **R9.** On every exit path -- submit, abandonment (however signalled), the
  empty and oversized refusals, and receipt of SIGTERM or SIGHUP -- the
  terminal's mode SHALL be restored to its state before the capture began.
- **R31.** The non-TTY refusal (R20), the oversized refusal (R17), the
  empty-capture refusal (R29), and abandonment SHALL each exit non-zero with the
  command's ordinary error exit status. No new exit code is introduced.

### Reachability

- **R11.** A caller that dispatches without going through the interactive
  command path -- including `niwa watch`, which invokes the launcher directly --
  SHALL never open a capture, under any argument or flag combination.

### Size ceiling

- **R14.** The prompt size ceiling SHALL be `maxArgStringBytes -
  dispatchPromptReserve`, where `maxArgStringBytes` is the largest single argv
  string `execve` accepts on the tightest supported platform and
  `dispatchPromptReserve` is the length of everything niwa may prepend to the
  prompt after validation. It SHALL be expressed as that derivation, not as a
  literal, so a change to either term visibly moves it. On the current baseline
  this evaluates to 130,433 bytes (roughly 127 KB).
- **R15.** A single ceiling SHALL apply on every supported platform. No
  platform-conditional definition SHALL exist.
- **R16.** The ceiling SHALL be enforced against text captured interactively,
  not only against a positional argument.
- **R17.** Input SHALL be refused at the moment it crosses the ceiling, before
  any instance is created. The capture SHALL remain open and SHALL retain the
  text already entered, so the developer can reduce it rather than lose it.
- **R18.** The refusal message SHALL state the size of the input and the limit,
  both in bytes, and SHALL direct the developer to write the text to a file and
  dispatch a prompt referencing that path. It SHALL NOT instruct the developer
  to shorten or re-select the text.
- **R19.** The capture SHALL disable canonical-mode line buffering before its
  first read, so no per-line length limit applies to input. A single line longer
  than the terminal's line-discipline buffer reaches this limit well before it
  reaches R14's ceiling.
- **R10.** The bytes the developer submitted SHALL appear byte-for-byte in the
  worker's argv, in the same single argv element the positional path produces,
  subject only to the prepend accounted for in R14. No trimming, normalization,
  or re-encoding SHALL be applied to the submitted bytes.

### Non-interactive and degraded terminals

- **R20.** When no positional argument is supplied and the session is not
  interactive, the command SHALL fail immediately with a message naming the
  argument form that works, and SHALL NOT read from standard input.
- **R21.** The interactivity test SHALL require both standard input and standard
  error to be terminals, since the capture reads the former and renders to the
  latter while standard output carries the command's existing session hints.
- **R22.** Standard output SHALL remain redirectable without affecting the
  capture's behavior beyond R21's gate.
- **R23.** The command SHALL NOT probe the terminal for capability, and SHALL
  NOT warn about a missing capability.
- **R27.** The capture SHALL render a visible indication that the command is
  waiting for input before its first read, so a promptless invocation from a
  script that inherits a terminal is visibly stalled rather than silently so.

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
- **R33.** The functional-test harness SHALL gain a bounded timeout on its
  terminal-driven step, so a scenario that fails to terminate fails as a step
  rather than exhausting the suite's global deadline.

## Acceptance Criteria

Each criterion names the requirements it verifies. Criteria are filed at the
level whose harness can actually make them fail.

### Unit tests over an injectable capture core

- [ ] Input arriving across multiple reads, split at an arbitrary boundary, is
      captured whole (R4).
- [ ] Input containing embedded newlines and no paste delimiters does not return
      after the first line (R4, R23).
- [ ] One submit gesture returns the captured text for a bare paste (R4).
- [ ] One submit gesture returns paste plus typed text, with the boundary
      preserved and the typed text not joined onto an unterminated final pasted
      line (R5).
- [ ] The manual-newline gesture inserts a newline and does not submit (R6).
- [ ] End-of-input on a non-empty buffer returns the accumulated text; on an
      empty buffer it returns the end-of-input outcome (R28).
- [ ] Abandonment returns a sentinel distinct from both end-of-input and a
      successful submit (R8).
- [ ] Input at exactly the ceiling is accepted; one byte over is refused (R14).
- [ ] The reserve counts against the ceiling: text that would fit only without
      the prepend is refused (R14, R16).
- [ ] After a refusal the capture is still accepting input and the previously
      entered text is retained (R17).
- [ ] The refusal message contains both byte counts and directs the developer to
      a file-and-reference approach; it does not contain "shorten" (R18).
- [ ] Submitted text containing terminal control sequences is returned
      unmodified, while the bytes written to the render target are sanitized
      (R30).
- [ ] Nothing written to the render target is a capability query sequence, and no
      capability warning text is emitted (R23).
- [ ] A visible waiting indication is written to the render target before the
      first read (R27).

### Command-level unit tests over a capture seam

- [ ] With no argument and an interactive session, the capture is invoked and its
      text becomes the launcher's final argv element (R1, R10).
- [ ] A submitted payload containing quotes, backslashes, and dollar signs
      arrives byte-for-byte as one argv element (R10).
- [ ] Over the four combinations of (stdin is a terminal, stderr is a terminal),
      the capture runs only when both are true; the other three refuse without
      reading (R20, R21).
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
- [ ] The non-TTY refusal, the oversized refusal, the empty refusal, and
      abandonment each return a non-zero status (R31).
- [ ] On the positional path, exit codes, messages, and argv construction match
      the pre-change baseline (R25).

### `@critical` functional scenarios

- [ ] A pasted multiline block dispatches, and the launched worker's argv
      contains the pasted text verbatim (R1, R4, R10).
- [ ] `niwa dispatch` with no argument and standard input attached to a pipe that
      is never written to and never closed exits within a bounded time rather
      than blocking (R20).
- [ ] The terminal's mode after an abandoned capture matches its mode before
      (R9).
- [ ] The terminal's mode after a normal submit matches its mode before (R9).
- [ ] An oversized paste followed by abandonment exits non-zero and leaves no
      instance (R17, R26).
- [ ] A single line longer than the line-discipline buffer, fed after the capture
      has started, is captured without hanging (R19).

### Verified by inspection

- [ ] The ceiling is a single derivation with no platform-conditional definition
      (R15).
- [ ] No documentation states or implies that the prompt argument is mandatory:
      the command's usage string, its long help, and the README all describe the
      argument as optional (R32).
- [ ] The terminal-driven functional step carries a bounded timeout, so a
      non-terminating scenario fails as a step (R33).
- [ ] Terminal restoration covers SIGTERM and SIGHUP. Keyboard interrupt is not
      covered by a signal test because a capture in raw mode receives it as an
      input byte rather than as a signal; it is covered by the abandonment
      criterion instead (R9).

### Verified manually before release

- [ ] Capture works inside a terminal multiplexer.
- [ ] A large paste renders without visible corruption.
- [ ] Capture behaves correctly in each terminal named in the supported set (see
      Open Questions).

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
does not probe and does not warn (R23). The guarantee that survives is stated
unconditionally in R4 -- multiline input is never truncated at an embedded
newline, on any terminal -- rather than as a promise conditioned on a fact the
implementation is forbidden to learn.

### Capture and detach compose

The BRIEF flagged this as sitting against the commitment to preserve attach.
They compose (R13). `--detach` has one meaning -- skip the final attach -- and it
is independent of how the prompt was obtained. "Paste a prompt, then fan out
without attaching" is coherent, and the command's own help already describes
detach as the mode for fan-out and scripting. Forbidding the combination would
be code written to prevent something harmless.

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
capture, so a developer will reach for it. Two readings were available: treat
end-of-input as a second submit gesture, or treat it as a terminating condition
that discards the buffer. R28 chooses submission. Discarding would take the
gesture a developer already has muscle memory for and make it the one that loses
their paste, which is the worst available outcome for the feature's central use
case.

### Rendering and payload are independent

R30 separates what is displayed from what is sent. A pasted log can contain
terminal control sequences, and echoing them raw is a display-corruption path.
Sanitizing the payload instead would silently alter the evidence the developer
is trying to hand over. Splitting them costs a little care in the
implementation and avoids both.

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
- A promptless invocation from a script that inherits an interactive terminal
  passes the interactivity gate and opens a capture. This is a caller bug; the
  mitigations are that the capture is visibly waiting (R27) and that abandonment
  is clean (R7, R9).
- Behavior inside multiplexers and the rendering quality of a large paste cannot
  be checked by any harness in this repository. They are manual criteria, which
  means they are checked at release time by a person or not at all.
- Keyboard interrupt during a raw-mode capture arrives as an input byte, not a
  signal, so the signal-restoration guarantee in R9 is exercised for SIGTERM and
  SIGHUP but verified by inspection rather than by a test that sends SIGINT.
- The reserve costs headroom on invocations where nothing is prepended.
  Recovering it would require resolving whether the prepend applies before
  provisioning, which this PRD does not require.

## Out of Scope

- `$EDITOR`, a clipboard flag, and a prompt-file flag, excluded by the BRIEF as
  user-facing properties rather than as implementations.
- Scripted piping as a design driver. Non-blocking behavior is required (R20);
  optimizing for `command | niwa dispatch` is not.
- Reading standard input as the prompt, whether implicitly or behind a new flag.
- Changing how the prompt reaches the worker after capture, including any
  transport change that would raise the ceiling.
- Prompt synthesis, which the dispatch skill owns.
- The capture mechanism, the specific submit and newline gestures, the choice of
  terminal API, and where the reader lives in the tree. These are DESIGN
  decisions; the requirements above constrain them without making them.
- A global no-input flag. If niwa wants one it belongs on the root command.
- Correcting the prompt size cap itself, tracked as issue #225 and stated here
  as dependency D1.
- Changing what the generated workspace context or the dispatch skill tell
  agents. Both instruct agents to pass a positional prompt, which remains
  correct; capture is a human-facing affordance and adding it there would invite
  agents to reach for an interactive path they cannot use.

## Open Questions

- Whether a functional scenario requiring a Linux-specific terminal utility is
  acceptable, given continuous integration runs only on Linux but developers run
  the suite on other platforms. The alternative trades a broken local run for
  silent coverage loss.
- Which terminals the manual-verification criteria name. Without a named set
  that criterion is unfalsifiable.
