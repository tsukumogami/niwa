---
schema: prd/v1
status: Draft
problem: |
  A developer who hits a failure in the terminal cannot hand that failure to a
  dispatched worker. `niwa dispatch` takes its prompt as a single positional
  argument, so the error text on screen has to be retyped, summarized down to a
  guess, or wrapped in shell quoting that must be right on the first try. The
  known workaround requires three undiscoverable pieces of knowledge and forces
  detachment, dropping the developer at a shell instead of the session they
  wanted to watch.
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
and newline gestures, where the reader lives, and how cancel is signalled.

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
- As an operator whose cron job calls `niwa dispatch` with a prompt argument, I
  want that path to behave exactly as it does today, so that nothing I have
  automated starts waiting on input.

## Requirements

### Functional

- **R1.** `niwa dispatch` invoked with no positional argument on an interactive
  terminal SHALL open a prompt that captures multiline text.
- **R2.** `niwa dispatch` invoked with one positional argument SHALL behave
  exactly as it does today, and SHALL NOT consult the terminal state. The
  argument path is byte-identical to current behavior.
- **R3.** The argument contract SHALL accept zero or one positional argument.
  Two or more remain an error. Because arity selects the path, no invocation
  can request both a positional prompt and a capture.
- **R4.** A single paste of multiline text SHALL be captured whole. Newlines
  inside the pasted text SHALL NOT terminate the capture.
- **R5.** The developer SHALL be able to type text alongside the pasted block
  and send both together, using the same submit gesture as a bare paste. No
  flag, mode, or prior decision SHALL be required to reach either case.
- **R6.** The capture SHALL provide a means of entering a newline manually,
  distinct from the submit gesture.
- **R7.** Abandoning the capture SHALL create no instance, no session mapping,
  and no other durable state.
- **R8.** Abandonment SHALL be distinguishable from end-of-input in the
  captured result, so the command reports a cancelled capture rather than
  treating it as an empty prompt.
- **R9.** On every exit path -- normal submit, abandonment, error, and receipt
  of SIGINT, SIGTERM, or SIGHUP -- the terminal's mode SHALL be restored to its
  state before the capture began.
- **R10.** The captured text SHALL reach the worker unchanged, as the same
  single argv element the positional path produces.
- **R11.** Capture SHALL be reachable only through the command's own run path,
  never through the launcher, so callers that invoke the launcher directly
  cannot inherit an interactive read.
- **R12.** `niwa dispatch ""` SHALL continue to fail with the existing
  empty-prompt error. An explicit empty argument is not a capture trigger.
- **R13.** `--detach` SHALL compose with capture. When set, the capture runs and
  the command returns without attaching; when unset, the developer is attached
  as they are on the argument path. No flag combination is newly rejected.

### Size ceiling

- **R14.** The prompt size ceiling SHALL be `maxArgStringBytes -
  dispatchPromptReserve`, where `maxArgStringBytes` is the largest single argv
  string `execve` accepts on the tightest supported platform and
  `dispatchPromptReserve` is the length of everything niwa may prepend to the
  prompt after validation. It SHALL be expressed as that derivation, not as a
  literal, so a change to either term visibly moves it.
- **R15.** A single ceiling SHALL apply on every supported platform.
- **R16.** The ceiling SHALL be enforced against text captured interactively,
  not only against a positional argument.
- **R17.** Input exceeding the ceiling SHALL be refused while the capture is
  still accepting input, before any instance is created, leaving the
  developer's session recoverable.
- **R18.** The refusal message SHALL state the size of the input, state the
  limit, and name a concrete alternative rather than instructing the developer
  to shorten the text.
- **R19.** A single line longer than the terminal's line-discipline buffer
  SHALL be captured without hanging. A minified stack frame or a long
  serialized payload reaches this before it reaches R14's ceiling.

### Non-interactive and degraded terminals

- **R20.** When no positional argument is supplied and the session is not
  interactive, the command SHALL fail immediately with a message naming the
  argument form that works, and SHALL NOT read from standard input.
- **R21.** The interactivity test SHALL require both standard input and
  standard error to be terminals, since the capture reads the former and
  renders to the latter while standard output carries the command's existing
  session hints.
- **R22.** Standard output SHALL remain redirectable without affecting the
  capture's behavior beyond R21's gate.
- **R23.** The command SHALL NOT probe the terminal for capability, and SHALL
  NOT warn about a missing capability.
- **R24.** On a terminal lacking paste-boundary support, multiline input SHALL
  NOT be silently truncated at its first newline, and the developer SHALL be
  able to see what was captured before any instance is created.

### Non-functional

- **R25.** No behavior on the positional-argument path SHALL change: same exit
  codes, same messages, same argv construction.
- **R26.** Failures reachable before provisioning SHALL be raised before
  provisioning, so a rejected capture never leaves an instance to reclaim.
- **R27.** The capture SHALL make it evident that the command is waiting for
  input, so a promptless invocation from a script that inherits a terminal is
  visibly stalled rather than silently so.

## Acceptance Criteria

### Verifiable as unit tests over an injectable capture core

- [ ] A pasted multiline block accumulates into one captured string; embedded
      newlines do not submit (R4).
- [ ] One submit gesture returns the captured text for a bare paste (R4, R5).
- [ ] One submit gesture returns paste plus typed text for an annotated paste,
      with no mode change between the two cases (R5).
- [ ] The manual-newline gesture inserts a newline and does not submit (R6).
- [ ] An escape sequence split across two reads is reassembled correctly (R4).
- [ ] Input at exactly the ceiling is accepted; one byte over is refused (R14,
      R17).
- [ ] The reserve is counted against the ceiling, so text that fits only
      without the prepend is refused (R14, R16).
- [ ] The refusal leaves the capture accepting input rather than returning
      (R17).
- [ ] Abandonment returns a sentinel distinct from end-of-input (R8).
- [ ] A single line longer than the line-discipline buffer is captured without
      hanging (R19).
- [ ] The captured result and the rendered prompt are separable: what is
      returned is not what is drawn (R10, R22).

### Verifiable as command-level unit tests over a capture seam

- [ ] With no argument and an interactive session, the capture is invoked and
      its text becomes the launcher's final argv element (R1, R10).
- [ ] With no argument and a non-interactive session, the command errors naming
      the argument form and provisions nothing (R20).
- [ ] With a positional argument, the capture is never invoked and the terminal
      state is never consulted (R2, R25).
- [ ] An abandoned capture provisions nothing (R7, R26).
- [ ] `niwa dispatch ""` fails with the existing empty-prompt error (R12).
- [ ] `--detach` with a capture returns without attaching; without `--detach`,
      the attach path runs (R13).
- [ ] The launcher entry point cannot reach the capture (R11).

### Verifiable as `@critical` functional scenarios

- [ ] A pasted multiline block dispatches, and the launched worker's argv
      contains the pasted text verbatim (R1, R4, R10).
- [ ] Input past the ceiling is refused with a message stating both sizes, and
      no instance remains (R17, R18, R26).
- [ ] The terminal's mode after an abandoned capture matches its mode before
      (R9).
- [ ] `niwa dispatch` with no argument and no terminal fails without blocking
      (R20).

### Verified manually before release

- [ ] Capture behaves acceptably on a terminal without paste-boundary support:
      multiline input is not truncated at the first newline (R23, R24).
- [ ] Capture works inside a terminal multiplexer.
- [ ] A large paste renders without visible corruption.

## Decisions and Trade-offs

This section closes the four open questions carried forward from
`docs/briefs/BRIEF-dispatch-paste-prompt.md`.

### The size ceiling is derived, and commits to failure-shaped pastes

The BRIEF required the PRD to state the ceiling rather than inherit today's,
because today's is wrong in both value and coverage. The ceiling is stated as a
derivation (R14) rather than a number so that a change to either term moves it
visibly.

The trade-off is which payloads this commits to serve. Measured: a Go panic is
about 5.6 KB, a failing `go test ./...` about 7.7 KB, a CI failure excerpt about
9.2 KB -- all far under the ceiling. A whole run is not: `go test -v ./...`
measures about 326 KB and a full CI log about 582 KB. Serving those would mean
changing how the prompt reaches the worker, which touches the guarantee that
the prompt is a single argv element never passed through a shell. This PRD
commits to failure-shaped pastes and rejects whole-log pastes with an
actionable message (R18). Since essentially everyone who sees that error will
be someone who pasted an entire run, the message matters more than the number.

The corrected cap itself is being fixed independently under issue #225. This
PRD states its requirement over that baseline and adds the half that fix does
not cover: applying the ceiling to the capture path (R16) and the wording of
the refusal (R18).

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
dispatch instead of an error. Every comparable tool that accepts input this way
makes it an explicit opt-in; if niwa ever wants one, it should be decided on
its own merits rather than acquired as a side effect.

When the session is interactive but the terminal lacks paste-boundary support,
there is nothing honest to say: enabling the capability is a silent no-op and
the resulting state is indistinguishable from ordinary typing. So the command
does not probe and does not warn (R23), and the requirement becomes a property
of the submit rule instead -- multiline input must not be truncated at its first
newline, and the developer must see what was captured before anything is
created (R24).

### Capture and detach compose

The BRIEF flagged this as sitting against the commitment to preserve attach.
They compose (R13). `--detach` has one meaning -- skip the final attach -- and it
is independent of how the prompt was obtained. "Paste a prompt, then fan out
without attaching" is coherent, and the command's own help already describes
detach as the mode for fan-out and scripting. Forbidding the combination would
be code written to prevent something harmless. The PRD therefore specifies two
exit shapes, which are the two the command already has.

### The non-interactive path is unchanged, and structurally so

The BRIEF asked what a non-interactive invocation does beyond not hanging. The
answer is that it does exactly what it does today, and the requirements are
written so that this is structural rather than careful: the interactivity check
lives only in the zero-argument branch (R2, R20), and capture is unreachable
from the launcher (R11). That second constraint is load-bearing -- `niwa watch`
calls the launcher directly, so capture logic placed there would give a
cron-driven review sweep an interactive read.

The argument contract widens from exactly one positional argument to at most
one (R3), matching the optional-positional-with-interactive-fallback shape
`niwa destroy` already uses. Two alternatives were rejected: a `-` sentinel,
which has no precedent in this codebase and pulls the feature toward the piping
shape the BRIEF excludes, and an explicit opener flag, which would reintroduce
the mode-choice the BRIEF rules out. Arity-based selection also makes "a
positional argument supplied alongside a capture" impossible by construction
rather than an error case to specify.

### The interactivity gate covers standard error, not standard output

Requiring both standard input and standard error to be terminals (R21) follows
the existing gate in `niwa destroy`, and differs deliberately from tools that
gate on standard output: niwa renders prompts to standard error and reserves
standard output for the session hints a caller may want to redirect. The cost
is that redirecting standard error from an interactive terminal disables the
capture. That is judged not worth supporting, since the capture's own display
would go to the redirect target.

## Known Limitations

- The ceiling is unreachable for the payloads this feature exists to serve and
  reachable only by pasting a whole run. The refusal is therefore a real path
  for a real user, not a defensive check, and its wording carries the weight.
- A promptless invocation from a script that inherits an interactive terminal
  passes the interactivity gate and opens a capture. This is a caller bug; the
  mitigations are that the capture is visibly waiting (R27) and that
  abandonment is clean (R7, R9).
- Behavior on terminals without paste-boundary support, inside multiplexers,
  and the rendering quality of a large paste cannot be checked by any harness
  in this repository. They are stated as manual-verification criteria rather
  than automated ones, which means they are checked at release time by a person
  or not at all.
- The 638-byte reserve costs headroom on invocations where nothing is actually
  prepended. Recovering it would require resolving whether the prepend applies
  before provisioning, which is a restructuring this PRD does not require.

## Out of Scope

- `$EDITOR`, a clipboard flag, and a prompt-file flag, excluded by the BRIEF as
  user-facing properties rather than as implementations.
- Scripted piping as a design driver. Non-blocking behavior is required (R20);
  optimizing for `command | niwa dispatch` is not.
- Reading standard input as the prompt, whether implicitly or behind a new
  flag.
- Changing how the prompt reaches the worker after capture, including any
  transport change that would raise the ceiling.
- Prompt synthesis, which the dispatch skill owns.
- The capture mechanism, the specific submit and newline gestures, the choice
  of terminal API, and where the reader lives in the tree. These are DESIGN
  decisions; the requirements above are written to constrain them without
  making them.
- A global no-input flag. If niwa wants one it belongs on the root command.
- Correcting the prompt size cap itself, which is tracked as issue #225.

## Open Questions

- Whether a functional scenario that requires a Linux-specific `script`
  implementation is acceptable, given continuous integration runs only on
  Linux but developers run the suite on macOS. The alternative trades a broken
  local run for silent coverage loss.
- Which terminals the manual-verification criteria name. Without a named set
  those criteria are unfalsifiable.
- Whether the developer is shown a confirmation of captured content before
  dispatch, which would strengthen R24 but overlaps whatever the capture
  displays at the size boundary.
