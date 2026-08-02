---
schema: prd/v1
status: In Progress
problem: |
  A developer who hits a failure in the terminal cannot hand that failure to a
  dispatched worker. `niwa dispatch` takes its prompt as a single positional
  argument, so the error text on screen has to be retyped, summarized down to a
  guess, or wrapped in shell quoting that must be right on the first try. The
  known workaround takes two undiscoverable pieces of knowledge and runs blind,
  since nothing echoes back what was captured before it is sent. And because a
  single argument is all the transport there is, text past the size one holds
  was refused outright, with the developer told to park it in a file and point
  at it -- a chore with no judgment in it that niwa is better placed to do.
goals: |
  Running `niwa dispatch` with no prompt opens an interactive capture in the
  terminal the developer is already in. Pasting a failure and sending it takes
  one gesture, whether or not the developer adds context of their own, and
  lands them attached to a session already working on it. The capture shows
  what it has taken, restores the terminal on every exit path, and never
  blocks a scripted or hooked invocation. A prompt too large to travel as one
  command argument still dispatches: niwa writes it to a file inside the
  instance and hands the worker a pointer plus a leading excerpt, so no size
  limit is ever surfaced to the developer.
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

Amended after the feature shipped, in place rather than superseded. The
amendment reverses one decision -- that the command commits to failure-shaped
pastes and refuses whole-log ones -- and everything that hung off it. The
problem, the goals, the user stories, and roughly four fifths of the
requirements are unchanged, which is why this is an amendment and not a new
PRD: the feature's identity did not change, one of its answers did. R17, R18,
and the original R14 through R16 are retired rather than reused, following the
same numbering discipline as the earlier rounds, and the replacements begin at
R43. R10 was consumed by the earlier revisions and is not reused; the
amendment's fidelity requirement is R51.

The status moved back from Done to In Progress, which is the honest reading:
the capture shipped and its criteria hold, but every criterion covering R43
through R60 describes work that has not been built. Leaving the document at
Done would assert that acceptance criteria are met when roughly a third of them
have never run.

The reversal came from using the shipped command. The refusal message told the
developer to write the text to a file and dispatch a prompt referencing that
path -- a sequence of steps containing no judgment the developer has that niwa
lacks. From where the developer stands there is no difference between a prompt
niwa passes along and a prompt niwa parks in a file and points at, so the
ceiling was an internal transport property surfaced as a user-facing wall. The
upstream BRIEF was re-opened by the same amendment.

This section records requirements only. The DESIGN owns where the file is
written, how the excerpt is bounded, and which layer makes the decision.

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

Two facts sharpened the original timing. The command's prompt size cap was
mis-set (issue #225): it sat exactly on Linux's per-argument `execve` limit
rather than below it, and the keep-alive instruction is prepended after the
check, so a prompt could pass validation and then die at exec after an instance
had been provisioned. That band was nearly unreachable while a caller's own
exec capped what they could pass in. Interactive capture removed that outer
guard -- niwa builds the string itself -- which turned a latent defect into a
reachable one. Both were corrected before capture shipped.

Correcting the cap made it enforceable, and enforcing it exposed the second
problem this PRD now answers. The rest of this section describes the shipped
behavior the amendment supersedes.

A prompt reached the worker only as one argv element, so a prompt larger than
one argv element was refused. The refusal named the remedy -- write the text to
a file, dispatch a prompt referencing the path -- and the remedy was a sequence
of mechanical steps: choose a filename, write the bytes, compose the pointer
sentence. Nothing in it required knowledge the developer has and niwa does not.
The limit belonged to a transport niwa chose, and asking the developer to route
around it asked them to compensate for a decision they never made and could not
see.

The cost concentrates where the feature is most useful. Measured payloads: a Go
panic is about 5.6 KB, a failing `go test ./...` about 7.7 KB, a CI failure
excerpt about 9.2 KB -- all comfortably inside the argv limit. A whole run is
not: `go test -v ./...` measures about 326 KB and a full CI log about 582 KB.
Those are exactly the cases where the developer does not know which part
matters, which is why they wanted to hand over all of it. So the refusal fired
hardest on the pastes the feature exists to carry, and to the developer it read
as a wall rather than as a constraint.

## Goals

- A developer hands off a failure without leaving the terminal, creating a
  file, or getting shell quoting right.
- The bare paste and the annotated paste use the same gesture, with no mode to
  choose and no decision required before starting.
- The developer sees what has been captured before it is sent.
- The developer stays attached to the session they just started, as they would
  with a prompt passed as an argument.
- A prompt too large to travel as one argv element dispatches anyway, by a
  route the developer neither chooses, configures, nor learns about.
- A worker started from a spilled prompt opens on text that says what it is
  about, rather than on a bare path the developer never picked. This is scoped
  to the worker's opening instruction, which is the surface this PRD owns.
  niwa's own listing shows instance names and has never shown prompt text, so
  `--name` remains the way to label a dispatch on niwa's surfaces.
- The terminal is left in working order on every exit path, including
  interruption.
- No scripted, hooked, or piped invocation blocks or changes behavior, except
  that an oversized argument now succeeds where it previously failed.

## User Stories

- As a developer mid-refactor whose test suite just broke, I want to select the
  failing output and hand it straight to a worker, so that I do not have to
  summarize an error I do not yet understand.
- As a developer picking up a CI failure I did not cause, I want to add what I
  already know alongside the pasted log, so that the worker does not re-derive
  paths I have eliminated.
- As a developer triaging a failure I cannot localize, I want to paste the
  whole run and have it dispatch, so that I do not have to guess which slice
  matters before I have understood the failure.
- As a developer who comes back to a morning's worth of dispatched workers, I
  want the one I handed a whole build log to open on text from that log rather
  than on a filename, so that reading its first message tells me which handoff
  it was.
- As an automation author whose assembled prompt has outgrown a single
  argument, I want the dispatch to keep working with no flag to add and no new
  failure to handle, so that a prompt that grew over time does not become an
  outage.
- As a developer who changes their mind mid-capture, I want to back out and
  find my terminal working normally, so that the next command behaves.
- As a developer who opens the capture and then realizes I have nothing to
  paste, I want submitting nothing to end the command cleanly rather than
  dispatch an empty task.
- As an author of a hook that runs `niwa dispatch` with no prompt by mistake, I
  want the command to fail immediately rather than wait forever on input that
  will never arrive, so that a scheduled job fails visibly instead of hanging.
- As an operator whose cron job calls `niwa dispatch` with a prompt argument, I
  want that path never to wait on input, so that nothing I have automated
  starts hanging. Whether an oversized prompt of mine now dispatches instead of
  erroring is a change I welcome; blocking on a terminal read is not.

## Dependencies

- **D1.** The capture requirements were stated over the baseline established by
  issue #225, which corrected the prompt cap and added a check immediately
  before exec. **Satisfied, and partly superseded:** the correction merged
  before implementation began, and two of its three parts survive. The
  corrected `maxArgStringBytes` derivation becomes R44's spill threshold, and
  the pre-exec check becomes R55's assertion. Only the reserve is retired: R45
  moves the decision after the prepend, which is what the reserve existed to
  compensate for.
- **R34.** Retired. It required this work to establish the #225 baseline if
  that issue had not landed first. It landed, the conditional never fired, and
  the requirement it would have created is superseded by R45.

- **D2.** This amendment supersedes a requirement in a sibling artifact that is
  already at status Done. `docs/prds/PRD-instance-dispatch.md` R43 requires the
  command to "fail clearly when a prompt exceeds the operating system's
  argument-length limit rather than truncating it silently", restated in
  `docs/designs/current/DESIGN-instance-dispatch.md`. The collision is literal
  and needs stating plainly: that requirement mandates the refusal this PRD's
  R43 forbids. Both halves of its intent survive here -- the prompt is still
  never silently truncated (R51), and an over-limit argv string is still
  refused rather than surfacing as an exec error (R55). What changes is that
  the developer no longer reaches that refusal, because the spill happens
  first. R56 covers correcting those two artifacts along with the user-facing
  documentation.

The one normative requirement D1 leaves behind is **R55**, which is stated with
the other size requirements rather than here.

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
  entered. The gesture is DESIGN's; the capability is required because a
  capture the developer can see (R35) but cannot correct is worse than no
  rendering at all. It no longer serves a size refusal, since there is none.
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
- **R37.** Input SHALL be accepted without perceptible stall, including when it
  arrives as a single line, at every size up to and including a whole
  continuous-integration log. Accepting a payload SHALL NOT take materially
  longer than accepting the same number of bytes spread across many lines, and
  the per-byte cost SHALL NOT grow with how much has already been entered.

### Cancellation, exit paths, and terminal state

- **R7.** Abandoning the capture SHALL create no instance, no session mapping,
  and no other durable state.
- **R8.** Abandonment SHALL be distinguishable from end-of-input in the captured
  result, so the command reports a cancelled capture rather than dispatching or
  reporting an empty prompt.
- **R9.** On every path that ends the command -- submit, abandonment, the
  empty-capture refusal, and receipt of SIGINT, SIGQUIT, SIGTERM, or SIGHUP --
  the terminal's mode SHALL be restored to its state before the capture began.
  With the oversized refusal retired, this list is exhaustive over the paths
  that can end a command with a capture open. The R49 backstop is not on it,
  because it reports and leaves the capture running.
- **R38.** If the capture is suspended and resumed, the terminal's mode SHALL be
  re-established on resume, so a suspended capture does not leave the terminal
  altered for a foregrounded shell.
- **R39.** Receipt of SIGINT during a capture SHALL restore the terminal and
  abandon cleanly, satisfying R7 and R9. Whether the interrupt reaches the
  process as a signal or as an input byte follows from DESIGN's terminal-mode
  choice; the observable outcome does not.
- **R31.** The non-TTY refusal (R20), the empty-capture refusal (R29), and
  abandonment SHALL each exit non-zero with the command's ordinary error exit
  status. No new exit code is introduced. The R49 backstop has no exit status of
  its own, because it does not end the command -- the developer's next action
  does.

### Reachability

- **R11.** A caller that dispatches without going through the interactive
  command path -- including `niwa watch`, which invokes the launcher directly --
  SHALL never open a capture, under any argument or flag combination.

### Size and transport

Requirements R14 through R18 in earlier revisions defined a refusal ceiling and
the message that carried it. They are retired, not renumbered: R14, R15, and
R16 are replaced by R43 through R46 below, and R17 and R18 have no successor
because there is no refusal for them to describe. R42's retention bound is
replaced by R49's memory backstop, which is a different thing at a different
magnitude.

- **R43.** No size limit SHALL be surfaced to the developer on any path. There
  SHALL be no refusal, no warning, and no prompt to reduce input, at any size
  the command accepts.
- **R44.** When the argv string the worker would receive -- the submitted
  prompt plus everything niwa prepends to it -- would exceed
  `maxArgStringBytes`, the largest single argv string the operating system
  accepts on the tightest supported platform, the submitted prompt SHALL be
  written to a file and the worker SHALL receive a pointer to that file in
  place of the prompt. The threshold SHALL be expressed as that derivation, not
  as a literal.
- **R45.** The decision required by R44 SHALL be made against the FINAL argv
  string, after every prepend, rather than against the submitted prompt alone.
  Consequently no reserve SHALL be held back from the developer's input, and
  no user-facing prompt ceiling SHALL remain reachable through the CLI.
- **R58.** Everything niwa prepends to a prompt SHALL ride the argv element on
  both paths. When a prompt spills, the pointer element SHALL carry the
  prepends, so a spilled dispatch with keep-alive resolved on still reaches the
  worker with the arming instruction. Only the developer's own submitted text
  moves into the file. Without this, a session recorded and reported as kept
  alive would launch without ever having been armed.
- **R46.** A single threshold SHALL apply on every supported platform. No
  platform-conditional definition SHALL exist.
- **R47.** The spill SHALL apply identically to a prompt supplied as a
  positional argument and to one captured interactively. The two paths SHALL
  NOT differ in threshold, in file format, or in the pointer they produce.
- **R48.** The spill SHALL apply on every path that launches a worker,
  including the launcher path `niwa watch` drives directly. No launch path
  SHALL be able to reach `execve` with an argv string over
  `maxArgStringBytes`. On the `niwa watch` path the spill is a structural
  guarantee rather than a live behavior: watch builds its prompts from fixed
  templates far below the threshold, so it SHALL NOT spill in practice, and a
  test SHALL pin that so a template grown past the threshold is caught as a
  change rather than discovered as a spilled file.
- **R49.** The capture SHALL hold a memory backstop, expressed as a multiple of
  `maxArgStringBytes` and at least 64 times it -- roughly 8 MB on the current
  baseline, more than an order of magnitude above the largest log the Problem
  Statement measures. Stating it as a derivation with a floor keeps it from
  being quietly tuned down into the wall this amendment removes. Crossing it
  SHALL refuse the append in full, retain none of it, and say the input was not
  retained -- a partially retained paste is worse than none, because it looks
  complete and is not. The backstop is a process-safety bound, not a product
  limit: it exists so an unbounded buffer cannot exhaust memory, and it SHALL
  NOT be described to the developer as a size limit on prompts.
- **R50.** No flag, configuration setting, environment variable, or interactive
  prompt SHALL control whether a prompt spills, where it is written, or how
  large the excerpt is. The decision SHALL be derived from the final argv
  string's length and nothing else, so that the same prompt in the same
  workspace always takes the same route.
- **R55.** The pre-exec check the #225 baseline added SHALL survive: no argv
  string over `maxArgStringBytes` SHALL reach `execve`, and a violation SHALL
  be reported with a message naming that limit rather than surfacing as an
  operating-system exec failure. Under R44 it becomes unreachable in normal
  operation, which is the point -- it guards against a future prepend that
  forgets the spill decision, exactly as it once guarded against one that
  forgot the reserve.
- **R57.** The spill SHALL be reachable through a seam a test can replace, so
  that R55's assertion remains constructible after R48 makes it otherwise
  unreachable.
- **R59.** A spilled prompt's filename SHALL be unique within its instance, so
  that two launches into the same instance cannot collide. This is not
  hypothetical: `niwa watch`'s continuation path launches repeatedly into an
  instance it did not create and does not replace, so an instance can host more
  than one launch over its life.
- **R51.** The bytes the developer submitted SHALL reach the worker
  byte-for-byte -- in the argv element when they fit, and in the spill file
  when they do not -- subject only to three stated exceptions: the single line
  feed R5 may insert at a paste boundary, the line-break normalization required
  by R41, and the prepends R58 keeps in argv. No other trimming, normalization,
  or re-encoding SHALL be applied, and the spill file SHALL carry no header,
  footer, or wrapper around the submitted bytes.
- **R52.** The pointer the worker receives SHALL name the file by a path the
  worker can resolve regardless of its working directory, SHALL instruct the
  worker to read that file as its task, and SHALL carry a leading excerpt of
  the submitted text. The excerpt SHALL be bounded above so the pointer stays
  small, and bounded BELOW so it does its job: two dispatches whose submitted
  prompts differ SHALL produce different argv elements whenever their texts
  differ within the excerpt's length, and the lower bound SHALL be large enough
  that ordinary failure output -- a first stack frame, a first assertion line --
  fits inside it. The excerpt SHALL be truncated on a character boundary and
  SHALL be labelled as a prefix, so a worker cannot mistake a truncated stack
  trace for a whole one.
- **R53.** A spilled prompt SHALL be written inside the instance the worker is
  launched into, so that the existing rollback-on-failure and reclamation
  lifecycle removes it. No spilled prompt SHALL outlive its instance, and a
  dispatch that fails after the spill SHALL leave no spilled prompt behind.
  The file SHALL NOT be deleted once the worker has been launched: the worker
  is daemon-backed and the launch call returns before it has read anything, so
  a post-launch delete would race the read it exists to serve. Instance
  reclamation is the disposal mechanism, and it is the only one.
- **R54.** The spilled file SHALL be created readable and writable by its owner
  only, and by no group or other, matching the mode the existing in-instance
  dispatch marker uses. The directory holding it SHALL be no more permissive.
- **R19.** No per-line length limit SHALL apply to captured input. A single line
  longer than the terminal's line-discipline buffer SHALL be captured intact,
  without truncation and without hanging.
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

- **R25.** On the positional-argument path, the only behavior that SHALL change
  is that a prompt which previously produced the oversized error now
  dispatches. For every prompt that dispatched before, exit codes, messages,
  and argv construction SHALL be unchanged. No invocation that succeeded SHALL
  begin to fail, and no invocation SHALL begin to spill that would previously
  have fit.
- **R26.** Failures reachable before provisioning SHALL be raised before
  provisioning, so a rejected capture never leaves an instance to reclaim. The
  spill is not such a failure: it happens after provisioning by necessity,
  since the file lives inside the instance (R53), and a spill that fails SHALL
  roll the dispatch back like any other post-provisioning failure.

### Supporting changes

- **R32.** Documentation stating or implying that the prompt argument is
  mandatory SHALL be updated: the command's own usage and long help, and the
  repository README.
- **R56.** Every artifact stating or implying that a large prompt will be
  refused, or that a caller must write a file to avoid the argument limit,
  SHALL be corrected. Five are known: the command's long help, the repository
  README, the `/dispatch` skill niwa installs (whose guidance warns that long
  prompts risk the argument-length limit), `docs/prds/PRD-instance-dispatch.md`
  R43, and `docs/designs/current/DESIGN-instance-dispatch.md` where it restates
  that requirement. The two upstream artifacts are at terminal status and SHALL
  be annotated in place rather than rewritten, naming this PRD as the
  superseding requirement. Writing a brief file remains the recommended
  practice for agents, for reasons that have nothing to do with size; the
  correction is to stop citing a limit that no longer refuses anything.
- **R60.** Code comments citing requirement numbers from
  `docs/prds/PRD-instance-dispatch.md` SHALL be disambiguated where this PRD
  reuses the same number for a different meaning. `dispatch_launcher.go` cites
  "R43" for the empty-prompt rejection and `dispatch.go` cites "R16, R13";
  under this PRD those numbers mean unrelated things, and a reader following
  either lands on the wrong requirement.
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
- [ ] Typed input is echoed to the render target as it is entered; a pasted
      block is represented by a bounded record naming its extent rather than by
      its bytes (R35).
- [ ] End-of-input on a non-empty buffer returns the accumulated text; on an
      empty buffer it returns the end-of-input outcome (R28).
- [ ] Abandonment returns a sentinel distinct from both end-of-input and a
      successful submit (R8).
- [ ] Deleting entered text reduces the buffer (R36).
- [ ] Over the sample {0, 1, 131,070, 131,071, 131,072, 614,400} bytes, every
      input is accepted, is submittable, and is returned byte-for-byte (R43).
- [ ] Over that same sample, and over the bytes written before the first read,
      nothing on the render target names a byte ceiling, states a maximum, or
      asks the developer to remove or shorten input. Checked as a substantive
      property, with a secondary lint for the substrings "limit", "too long",
      and "too large". The R49 refusal is the one message exempt from the lint,
      and it is covered by its own criterion below (R43, R49).
- [ ] Six appends of 614,400 bytes each -- 3.6 MB cumulative, well past the
      point where any earlier retention bound would have fired -- are all
      accepted, so the backstop is genuinely far above what a developer
      produces rather than reachable by pasting twice (R43, R49).
- [ ] The backstop constant is at least 64 times `maxArgStringBytes`, asserted
      against the derivation rather than a copied literal, the way the existing
      exec-limit constants are pinned (R49).
- [ ] An append crossing the backstop is refused in full, the buffer is
      unchanged, and the refusal says the input was not retained. The refusal
      text names no byte ceiling and gives no size advice: it does not tell the
      developer to write a file, reference a path, shorten, or re-select (R49).
- [ ] Submitted text containing terminal control sequences is returned
      unmodified, while the bytes written to the render target contain no
      executable control sequence from the input (R30).
- [ ] Nothing written to the render target is a capability query sequence, and no
      capability warning text is emitted (R23).
- [ ] The bytes written before the first read contain non-empty human-readable
      text once escape sequences are stripped (R27).
- [ ] A single line of 614,400 bytes is accepted whole, without truncation and
      without hanging (R19).
- [ ] Total bytes copied while accepting 614,400 bytes is within 4x the byte
      count, and accepting 614,400 bytes allocates within 4x what accepting
      61,440 bytes allocates per byte. Gating on a work counter rather than on
      wall time, because this document already records one bogus superlinear
      measurement produced by timing a harness rather than the code (R37).
- [ ] As a wall-clock backstop that will not flake: 614,400 bytes are accepted
      in under two seconds on the in-memory reader (R37).
- [ ] Any ratio-of-timings comparison between input shapes is a benchmark that
      does not gate continuous integration (R37).

### Command-level unit tests over a capture seam

- [ ] The spill threshold equals `maxArgStringBytes` (131,071 on the current
      baseline), asserted against the derivation rather than a copied literal,
      and no user-facing prompt ceiling is reachable through the CLI (R44,
      R45).
- [ ] The threshold constant is declared exactly once, in a file carrying no
      build constraints, and `GOOS=darwin` and `GOOS=linux` builds both vet
      clean -- so a platform-conditional definition fails rather than passing
      on whichever platform continuous integration happens to run (R46).
- [ ] A prompt whose final argv string is exactly `maxArgStringBytes` does not
      spill; one byte more does (R44).
- [ ] A prompt just under the threshold that crosses it once the keep-alive
      instruction is prepended DOES spill, and the same prompt with keep-alive
      unarmed does not -- so the decision is made against the final string, not
      the submitted one (R45).
- [ ] With keep-alive armed and the prompt spilled, the worker's argv element
      still begins with the arming instruction, and the session mapping's
      keep-alive flag matches what was actually sent (R58).
- [ ] The same oversized payload supplied as a positional argument and returned
      from the capture seam produces byte-identical spill file contents, and
      pointer text that is identical once each run's instance directory is
      replaced by a placeholder -- same instruction wording, same in-instance
      filename shape, same excerpt bytes (R47).
- [ ] An oversized prompt produces a spill file whose bytes equal the submitted
      bytes exactly, with no header, footer, or trailing newline added (R51).
- [ ] The worker's argv element for an oversized prompt contains the spill
      file's path, an instruction to read it, a fixed marker constant
      delimiting the excerpt, and at most N bytes of excerpt with N asserted
      against its derivation. The path satisfies `filepath.IsAbs`. The whole
      argv element stays under `maxArgStringBytes`. A cut falling mid-character
      moves back to a character boundary (R52).
- [ ] Two dispatches whose submitted prompts differ within the excerpt's length
      produce different argv elements, so the excerpt cannot be degraded to a
      single byte while still passing (R52).
- [ ] The spill file's mode is 0600 and the directory holding it is no more
      permissive than 0700 (R54).
- [ ] The spill file lives under the instance the worker is launched into, and
      destroying that instance removes it (R53).
- [ ] Two launches into the SAME instance produce two spill files, neither
      overwriting the other (R59).
- [ ] The spill file still exists after the launch call returns, so a worker
      that reads it later finds it (R53).
- [ ] A dispatch that fails after the spill leaves no instance and no spill file
      (R53, R26).
- [ ] With the spill write forced to fail, the dispatch reports the failure and
      leaves no instance behind (R26).
- [ ] With the spill seam stubbed to a no-op, an over-ceiling argv string is
      refused before exec with an error naming `maxArgStringBytes`, not an
      opaque exec error (R55, R57).
- [ ] The prompts `niwa watch` builds from its review and resume templates are
      below the threshold and do not spill, pinned so a template grown past it
      is caught as a change (R48).
- [ ] Driving the launcher path used by `niwa watch` with an oversized prompt
      spills rather than failing, and no argv element handed to exec exceeds
      `maxArgStringBytes` on any launch path (R48, R55).
- [ ] The command's registered flag set matches a golden list, so a new flag
      fails the golden and has to be justified (R50).
- [ ] The spill decision's call graph contains no environment or configuration
      lookup, asserted by source inspection (R50).
- [ ] Running the spill decision under a clean environment, and again with every
      documented niwa environment variable set and a config file setting every
      known key, produces the same decision, the same path shape, and the same
      excerpt length (R50).
- [ ] A positional prompt of 614,400 bytes dispatches, writes nothing
      size-related to standard error, and exits zero (R43, R47).
- [ ] With no argument and an interactive session, the capture is invoked and its
      text becomes the launcher's final argv element (R1, R51).
- [ ] A submitted payload containing quotes, backslashes, and dollar signs
      arrives byte-for-byte as one argv element (R51).
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
- [ ] On the positional path, for every prompt that dispatched before this
      change, exit codes, messages, and argv construction match goldens
      recorded beforehand; the only golden that changes is the oversized one,
      which moves from an error to a dispatch (R25).

### `@critical` functional scenarios

- [ ] A pasted multiline block dispatches, and the launched worker's argv
      contains the pasted text verbatim (R1, R4, R51).
- [ ] A positional prompt larger than `maxArgStringBytes` dispatches; the
      worker's argv names a file inside the instance; that file's contents
      equal the prompt byte-for-byte; and the fake worker resolves the path
      from a working directory other than the instance, so an instance-relative
      path fails the scenario (R44, R51, R52, R53).
- [ ] Reclaiming the instance behind a spilled dispatch removes the spill file
      along with it (R53).
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
- [ ] A capture followed by abandonment exits non-zero and leaves no instance,
      at any input size (R26, R43).
- [ ] A single line longer than the line-discipline buffer, fed after the capture
      has started, is captured without hanging (R19).

### Verified by inspection

- [ ] No documentation states or implies that the prompt argument is mandatory:
      the command's usage string, its long help, and the README all describe the
      argument as optional (R32).
- [ ] A grep over a fixed file list -- the long help in `internal/cli/`, the
      README, `internal/workspace/rootskills/dispatch/SKILL.md`,
      `docs/prds/PRD-instance-dispatch.md`, and
      `docs/designs/current/DESIGN-instance-dispatch.md` -- finds no surviving
      claim that a large prompt is refused or that a caller must write a file
      to stay under an argument limit, and finds a superseding annotation on
      the two upstream artifacts (R56).
- [ ] No code comment cites a requirement number whose meaning differs between
      this PRD and `docs/prds/PRD-instance-dispatch.md` without naming which
      document it means (R60).
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

### The ceiling became a route, not a wall

**Superseded, and the reasoning is kept because it shows why the reversal was
available.** The original decision stated the ceiling as a derivation and
committed the command to failure-shaped pastes: a Go panic at about 5.6 KB, a
failing `go test ./...` at about 7.7 KB, a CI failure excerpt at about 9.2 KB,
all comfortably inside it, against `go test -v ./...` at about 326 KB and a full
CI log at about 582 KB, which were rejected with an actionable message. The
rejected alternative was named at the time: "serving those would mean changing
how the prompt reaches the worker."

That alternative is now taken. What made the reversal cheap is the shape of the
message the old decision leaned on: it told the developer to write the text to a
file and dispatch a prompt referencing that path. Everything in that instruction
is mechanical. Choosing a filename, writing the bytes, composing the pointer
sentence -- none of it needs anything the developer knows and niwa doesn't. A
remedy that a program can carry out on the user's behalf, in full, is not a
remedy; it is a deferred implementation.

So R44 keeps the derivation and changes what crossing it means. Above the
threshold, niwa performs the remedy itself: the text goes to a file inside the
instance and the worker receives a pointer plus a leading excerpt. Below it,
nothing changes. The guarantee the old decision protected -- one argv element,
never through a shell -- is preserved in both branches, because the pointer is
also a single argv element built from discrete parts rather than concatenated
into a command line.

That guarantee is worth stating precisely, because the pointer does embed
developer-supplied bytes: R52's excerpt is untrusted text inside a
niwa-authored instruction. The protection is not that the element is free of
untrusted content -- the whole prompt always was untrusted -- but that it is
never handed to a shell, so nothing inside it can become command structure.
DESIGN owns how the excerpt is delimited within the instruction so a worker
can tell where niwa's words end and the developer's text begins.

The alternative of raising the transport rather than routing around it was
checked and is not available. `claude --bg` accepts a task prompt as an argv
element and by no other route; its stdin and file input modes are all gated
behind `--print`, which is a different, non-background mode. There is no
larger-argument path to reach for, so a file plus a pointer is the transport,
not a preference among several.

### The excerpt exists because a pointer alone loses the session's identity

R52 carries a bounded prefix of the text alongside the pointer. Without it, a
dispatched session's opening instruction is a path the developer never chose,
and a morning of fanned-out workers becomes a list of indistinguishable rows.
With it, the session still announces what it is about.

The excerpt is deliberately not a summary. Having niwa describe the text would
mean dispatch starts interpreting a prompt it currently only carries, which is a
different and much larger commitment -- and a wrong summary is worse than a
literal prefix, because it is confidently wrong. A prefix has one failure mode,
truncation, and R52 addresses it by labelling the excerpt as a prefix so a
worker cannot mistake a cut stack trace for a whole one.

### The reserve disappears because the decision moved after the prepend

The old design held back `dispatchPromptReserve` from the developer's input so
that a single early check could cover a prepend applied much later, whose
outcome was not knowable before the instance existed. That is a real constraint
while the check is a refusal: refusing must happen before provisioning, and the
prepend is decided after, so the only sound answer is to reserve the worst case
up front and charge it to the developer.

Making the transport decision instead of a refusal dissolves the constraint. The
decision no longer has to be early, because nothing is being denied -- and once
it can be late, it can be made against the final argv string, after every
prepend, where no estimate is needed (R45). The developer stops paying for a
prepend that may not happen. The pre-exec check survives as R55, no longer as a
backstop against a forgotten reserve but as one against a forgotten spill.

### No knob

R50 forbids a flag, setting, or prompt controlling the spill. The reasoning is
the same one that motivates the whole amendment: a decision the developer can
influence is a decision they have to understand, and the point is to stop
requiring them to understand it. A `--prompt-file`-shaped escape hatch would
also reintroduce the BRIEF's excluded shape by the back door.

The cost is that a developer who wants their prompt inline for some reason
cannot force it. No such reason has come up, and the behavior is derivable from
size alone, so a developer who needs to know can compute it.

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

### A non-interactive invocation never reads, and structurally so

The BRIEF asked what a non-interactive invocation does beyond not hanging. It
never opens a capture and never reads from a terminal, and the requirements are
written so this is structural rather than careful: the interactivity check
lives only in the
zero-argument branch (R2, R20), and capture is unreachable from any caller that
does not go through the interactive command path (R11). That second constraint
is load-bearing -- `niwa watch` calls the launcher directly, so capture logic
placed there would give a cron-driven review sweep an interactive read.

What a non-interactive invocation does with the prompt it was given is a
separate question, and there the answer did change: an oversized positional
argument now spills rather than erroring (R44, R47, R48). The invariant that
survives is about reading, not about outcomes -- no path that lacks a terminal
acquires one.

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

- **A spilled prompt is written to disk, and the earlier design's claim that the
  prompt never touches disk no longer holds.** That property was load-bearing
  enough that SIGQUIT is handled specifically to keep a core dump from carrying
  the payload. The amendment gives it up for prompts above the threshold. The
  mitigations are that the file is owner-only (R54), lives inside an instance
  directory that already holds dispatch state at the same permissions, and is
  removed when the instance is (R53). It remains a genuine reduction: a large
  pasted secret now exists as a file for the life of the session, where before
  it existed only in process memory. The positional path was already worse --
  the whole prompt lands in shell history -- but the capture path was better,
  and is now better only below the threshold.
- **A spilled prompt is not the worker's literal opening message.** The worker
  receives an instruction to read a file. It must spend a tool call to get the
  text, and a worker that ignores the instruction proceeds on the excerpt
  alone. Nothing in this PRD can guarantee the read happens.
- **Removing niwa's limit does not remove the worker's.** A 582 KB log fits in
  the file and may not fit usefully in the worker's context. The handoff is
  what this feature owns; comprehension is not.
- **A reclaimed instance takes the spilled prompt with it.** A developer who
  wants the text after `niwa reap` has run has their clipboard and their
  scrollback and nothing from niwa. This follows from R53 and is the accepted
  cost of not accumulating unreclaimed files.
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
- The R49 backstop is a real bound, just an unreachable one. A pathological
  producer feeding the capture from something other than a human paste can
  still hit it, and gets a refusal that names no product limit because there
  isn't one.
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
- Any user-facing control over the spill: no flag, no configuration key, no
  threshold to tune, no path to supply (R50).
- Prompt synthesis, which the dispatch skill owns. The excerpt R52 carries is a
  literal prefix, not a description; having niwa characterize the text is a
  different commitment and stays out.
- Any promise about what the worker does with a large prompt, including whether
  it reads the spilled file at all and how well it reasons over that much text.
- Retaining a spilled prompt beyond its instance, and any archive, log, or
  audit trail of dispatched prompts.
- Compressing, chunking, or splitting a large prompt, and any change to how the
  worker process is started.
- The capture mechanism, the specific submit, newline, deletion, and
  cancellation gestures, the choice of terminal API, where the reader lives in
  the tree, the spilled file's name and location within the instance, the
  excerpt's size, and which layer of the command makes the spill decision.
  These are DESIGN decisions; the requirements above constrain them without
  making them.
- A global no-input flag. If niwa wants one it belongs on the root command.
- Teaching agents to use the interactive capture. The generated workspace
  context and the dispatch skill instruct agents to pass a positional prompt,
  which remains correct; capture is a human-facing affordance and adding it
  there would invite agents to reach for an interactive path they cannot use.
  R56 corrects only the stale claim that a long prompt risks refusal, and
  leaves the brief-file recommendation standing on its own merits.

Correcting the prompt size cap was tracked separately as issue #225 and landed
before capture shipped. Most of that correction survives: its threshold becomes
R44's and its pre-exec check becomes R55's. Only the reserve it introduced is
retired, by R45.
