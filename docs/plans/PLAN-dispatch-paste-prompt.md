---
schema: plan/v1
status: Active
execution_mode: single-pr
milestone: dispatch-paste-prompt
issue_count: 6
upstream: docs/designs/DESIGN-dispatch-paste-prompt.md
---

## Status

Active

Single-pr plan for interactive prompt capture on `niwa dispatch`, downstream of
the Accepted DESIGN. No GitHub milestone or issues are created; the outlines
below are the unit of work and land in one pull request.

## Scope Summary

Build a new `internal/promptcapture` package that reads a multiline prompt from
the terminal in raw mode, and wire it into `runDispatch` behind an arity-selected
branch so that `niwa dispatch` with no argument on an interactive session opens a
capture. The prompt size ceiling already exists on the main branch; this work
applies it to the capture path rather than rebuilding it.

## Decomposition Strategy

**Horizontal.** The design's components have well-defined interfaces and one
clear prerequisite ordering: the capture core is a pure function over an injected
reader and writer, the terminal lifecycle wraps it, and the command wiring
consumes both behind a seam. There is no runtime integration risk that early
end-to-end exercise would surface, because the pieces do not interact at a
distance -- the core never touches the terminal, and the command never touches
raw bytes.

The one adjustment to a pure layer-by-layer order: the test harness work comes
first rather than last. The functional harness currently feeds a terminal-driven
step in one burst and does a short write it never retries, so any scenario
written against it fails for reasons unrelated to the code under test. Building
on a harness that lies is worse than building bottom-up.

## Issue Outlines

### 1. Prepare the functional harness for terminal-driven scenarios

**Goal**: Make the functional suite capable of exercising an interactive capture
without producing false failures or hanging the suite.

**Work**: Feed terminal-driven input in chunks rather than one burst, and retry
short writes. Add a step that attaches standard input to a pipe held open and
never written to. Add a step that sends a signal to the running binary. Put a
bounded timeout on every step that supplies the binary a standard input the step
does not control.

**Acceptance Criteria**:
- A terminal-driven step feeding 130,433 bytes completes rather than stalling.
- A scenario whose input never terminates fails as a step within its timeout
  instead of exhausting the suite deadline.
- The held-open-pipe step distinguishes a command that does not read standard
  input from one that reads and receives end-of-input.

**Complexity**: testable

**Dependencies**: None

### 2. Build the capture core

**Goal**: A pure reader that turns a byte stream into a submitted prompt, with no
terminal dependency.

**Work**: Implement `read(ctx, stdin, stderr, limit)` in a new
`internal/promptcapture` package, mirroring the existing picker's exported-entry
plus unexported-core split. Parse bracketed-paste markers. Carry the three pieces
of cross-chunk state the design names: a held-back marker prefix, a
carriage-return flag, and a deferred paste-boundary break. Normalize line breaks
inside a pasted block to a single line feed and leave every other byte alone.
Route bytes outside a paste through the gesture table. Implement the ceiling
refusal as loop state -- append, then check, then mark unsubmittable -- with
bounded retention that refuses an over-bound append in full. Implement deletion
by rune, word, and line. Implement the neutralizer and the bounded transcript.

**Acceptance Criteria**: The fourteen core criteria in the PRD's first
acceptance-criteria group, plus:
- Every test that exercises marker parsing, the carriage-return flag, or the
  deferred break also runs at chunk size one.
- A pasted block containing the end-of-paste byte sequence is characterized, not
  merely left undefined.
- The submitted payload and the transcript bytes are asserted separately in every
  test that involves control characters.

**Complexity**: critical

**Dependencies**: None

### 3. Own the terminal lifecycle

**Goal**: Enter raw mode and bracketed-paste mode, and restore both on every path
out, including the ones no `defer` reaches.

**Work**: Put the standard input descriptor into raw mode -- deliberately not the
standard error descriptor the existing picker raws. Hold the pre-capture state
and the raw state. Install handlers for SIGINT, SIGQUIT, SIGTERM, SIGHUP,
SIGTSTP, and SIGCONT. On suspend, restore and raise SIGSTOP directly rather than
resetting the disposition; on resume, re-enter raw mode only if the process is in
the foreground process group. Treat end-of-input after a hangup as abandonment.
Tear every handler down before the function returns, so the attach handoff
inherits a clean terminal and default dispositions.

**Acceptance Criteria**:
- Terminal state after submit, abandonment, interrupt, termination, hangup, and
  suspend-then-resume each match the state before the capture began.
- No handler remains installed after the capture returns.
- A capture resumed while in the background does not alter terminal settings.

**Complexity**: critical

**Dependencies**: <<ISSUE:2>>

### 4. Wire the capture into the command

**Goal**: `niwa dispatch` with no argument on an interactive session captures a
prompt; every other invocation behaves exactly as it does today.

**Work**: Relax the argument contract from exactly one positional argument to at
most one. Add a standard-error terminal check beside the existing standard-input
one, and gate the capture on both. Add the capture seam as a package-level
function variable. Call it after the workspace, agent, and binary preflight checks
and before the first instance is created. Pass the existing derived ceiling into
the core, and reuse the existing rejection message text for the in-capture
refusal. Refuse a whitespace-only submission with the existing empty-prompt error.

**Acceptance Criteria**: The thirteen command-level criteria in the PRD's second
acceptance-criteria group. Additionally:
- The capture and the argument path quote the same limit and offer the same
  advice, asserted against one source rather than two literals.

**Complexity**: testable

**Dependencies**: <<ISSUE:2>>, <<ISSUE:3>>

### 5. Guard the launcher against ever reaching the capture

**Goal**: A cron-driven review sweep can never inherit an interactive read.

**Work**: A test that drives the launcher path `niwa watch` uses, with the capture
seam stubbed to fail the test if called, asserting the stub is never invoked.

**Acceptance Criteria**:
- The test fails if the capture is moved into the launcher.
- The test exercises the real `niwa watch` call path rather than a reconstruction
  of it.

**Complexity**: simple

**Dependencies**: <<ISSUE:4>>

### 6. Document the new entry point and its one degradation

**Goal**: A developer can discover the capture, and is not surprised by the
terminal case where it degrades.

**Work**: Update the command's usage string and long help to describe the prompt
argument as optional and name the capture. Update the README's dispatch example.
Document that on a terminal which does not delimit pasted blocks, a multiline
paste submits at its first line break -- and that this is why the argument form
remains available.

**Acceptance Criteria**:
- No usage string, help text, or README passage states or implies that the prompt
  argument is mandatory.
- The degradation is documented where a developer looking at dispatch would find
  it, not only in the design document.

**Complexity**: simple

**Dependencies**: <<ISSUE:4>>

## Implementation Sequence

**Critical path.** Outline 2, then 3, then 4. The capture core is the longest and
riskiest piece and everything downstream waits on its interface, so it starts
first and its cross-chunk state is where review attention belongs.

**Parallelizable.** Outline 1 has no code dependency and can proceed alongside
outline 2; it must land before the terminal-driven scenarios in outline 4 are
written, or those scenarios will fail on the harness rather than on the code.
Outlines 5 and 6 are independent of each other once the wiring exists.

**Sequencing note.** The prompt size ceiling landed upstream while this chain was
in design. Outline 4 applies the existing constant and reuses the existing
rejection message; it does not re-derive either, and a test asserts the two paths
quote one source rather than two literals.
