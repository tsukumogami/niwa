---
schema: design/v1
status: Proposed
upstream: docs/prds/PRD-dispatch-paste-prompt.md
problem: |
  `niwa dispatch` takes its prompt as one positional argument, so a developer
  cannot hand a dispatched worker the multiline error already on their screen
  without retyping it, summarizing it into a guess, or getting shell quoting
  right on the first attempt. The requirements are settled; what remains is how
  to read a multiline prompt from a terminal without a per-line size limit,
  without replaying a pasted log's control sequences into the display, and
  without leaving the terminal altered for the next command.
decision: |
  A new `internal/promptcapture` package reads the prompt directly over
  `term.MakeRaw`, parsing bracketed-paste markers itself rather than delegating
  to a line editor. The payload accumulates as raw bytes while a separate
  append-only transcript renders bounded, neutralized records to standard error.
  `runDispatch` gains an arity-selected branch and a capture seam sited after
  every preflight check and before the first instance is created, so abandonment
  costs nothing and the launcher stays unreachable from any interactive path.
rationale: |
  The reader was chosen on fidelity rather than speed: measured against pasted
  colour codes, invalid UTF-8, NUL bytes, and a 130,433-byte single line, the
  library line editor corrupts the payload where a hand-rolled loop does not,
  and it offers no hook to sanitize its own echo -- which the requirement to
  preserve the payload while neutralizing the display makes decisive. Placement
  outside `internal/tui` avoids a standing byte-for-byte sync obligation with
  another repository, and siting the capture before provisioning turns abandon
  into a no-op rather than a rollback.
---

## Status

Proposed

Downstream of `docs/prds/PRD-dispatch-paste-prompt.md` (In Progress). Five
decision questions were investigated independently; the cross-validation that
reconciled them is summarized under Decision Outcome.

## Context and Problem Statement

`niwa dispatch` launches a background worker and takes its task as a single
positional argument. The requirements for interactive capture are settled
upstream. The technical problem is narrower and has four parts that interact.

Reading multiline input from a terminal is not a solved problem in the standard
library. Canonical mode imposes a per-line buffer limit and hands back one line
at a time; escaping it means raw mode, which means owning key decoding, echo,
and terminal restoration. The obvious shortcut -- a line editor from
`golang.org/x/term` -- brings its own echo, which is exactly the thing that must
be separable here, because a pasted log's control sequences have to survive into
the payload while never reaching the screen.

Bracketed paste is what makes the central gesture work. When a terminal delimits
a pasted block, the bytes between the markers are inert, so Enter can submit
without truncating a paste at its first line. The markers arrive split across
read boundaries, contain bytes the payload may also contain, and are absent
entirely on older terminals -- a state the implementation is forbidden to probe
for and must simply survive.

Terminal state is borrowed, not owned. Raw mode and paste mode are process-wide
effects on a device the shell will use next. Leaking either is a failure the
developer experiences on their following command, and the ways to leak are
signals, suspend, and the handoff to `claude attach` that follows a successful
dispatch.

Finally, the capture must be unreachable from the paths that are not interactive.
`niwa watch` invokes the launcher directly on a cron timer; a capture placed one
layer too deep would give a scheduled sweep an interactive read that never
returns.

## Decision Drivers

- **Payload fidelity is non-negotiable.** The submitted bytes reach the worker
  byte-for-byte. Any component that normalizes, replaces, or drops bytes on the
  way through is disqualified regardless of its other merits.
- **The display is adversarial input.** A pasted log is untrusted with respect to
  the terminal: it can contain cursor movement, colour, and bytes that would
  otherwise kill or suspend the process.
- **Restoration failures are silent and land elsewhere.** They surface on the
  developer's next command, so the discipline has to hold across signals and
  suspend, not just clean returns.
- **The non-interactive paths must not change.** Structurally, not carefully.
- **No new dependency without cause.** `golang.org/x/term` is already a direct
  requirement and the existing picker already drives raw mode with it.
- **Testability at the boundary that carries the risk.** The reader's cross-chunk
  state is where correctness is hard; the design should make it drivable without
  a terminal.

## Considered Options

### Reader surface

**Chosen: a hand-rolled reader over `term.MakeRaw`.** Parses bracketed-paste
markers itself, accumulates the payload as bytes, and emits rendering separately.
Measured at 1.16 ms for a 130,433-byte single line, byte-exact across ANSI colour
codes, invalid UTF-8, NUL, and embedded control bytes.

**Rejected: `x/term.Terminal.ReadLine` with an `ErrPasteIndicator` loop.** This
was the round-one recommendation and it loses on fidelity, not performance. It
replaces pasted colour codes with the Unicode replacement character, returns an
empty capture when the input contains a single invalid UTF-8 byte, truncates at
4,096 bytes per line on terminals without paste delimiters, conflates the cancel
and end-of-input keys, and owns its echo with no hook to neutralize it -- which
makes the payload-versus-display split the requirements demand impossible to
implement on top of it.

**Rejected: bubbletea.** Works, but drops invalid UTF-8, re-scans the accumulated
buffer on each event (measured 16x slower), and costs eighteen new modules for a
component that needs three functions the repository already imports.

An earlier argument against `ReadLine` on superlinear echo cost was retired
during this phase: the supporting measurement was a short-write defect in the
test harness, which affects every candidate reader equally. The rejection stands
on fidelity alone.

### Placement

**Chosen: a new `internal/promptcapture` package.** Mirrors the existing
`Pick`/`pick` split -- an exported entry point that binds the real terminal, and
an unexported core taking an injected reader and writer.

**Rejected: `internal/tui`.** Every file in that package is a byte-for-byte
mirror of another repository's copy under a standing sync obligation declared in
its header. A niwa-only reader added there either breaks that invariant or forces
a matching change elsewhere.

**Rejected: `internal/cli` directly.** Workable, but it puts a terminal state
machine in the package that holds command wiring, and it makes the core harder to
test in isolation.

### Rendering

**Chosen: an append-only transcript of bounded records.** Reads of 64 bytes or
fewer echo verbatim after neutralization; a pasted block emits a count line plus
its first and last line, width-truncated. Cost is proportional to terminal width
rather than to payload size.

**Rejected: full echo of every byte.** Not on speed -- it is linear -- but because
130 KB of replay scrolls the developer's own failure off the screen, and because
supporting deletion against a full-echo display requires a line editor the
requirements do not ask for.

**Rejected: reusing `SanitizeDisplayString`.** It leaves carriage return,
backspace, bell, and 8-bit control sequences intact, and it lives in the synced
package. A stronger neutralizer belongs in the new package.

### Cancellation and terminal mode

**Chosen: clear ISIG via `term.MakeRaw`, handle the interrupt as a byte, and also
install a signal handler.** A pasted `0x03` or `0x1A` must not kill or suspend
the process mid-capture, which the payload-preservation requirement makes
mandatory. Both arms produce the same observable outcome.

**Rejected: keeping ISIG set.** Simpler signal story, but a pasted log containing
an interrupt or suspend byte would terminate the capture and lose the buffer.

## Decision Outcome

The capture is a new package that owns raw mode on standard input, renders to
standard error, and returns three outcomes -- submitted text, end-of-input on an
empty buffer, or abandonment. `runDispatch` selects it by argument arity and
calls it after every preflight check and before the first instance exists.

The gesture set reconciles the independently-derived recommendations with the
author's decision that Enter submits:

| Gesture | Bytes outside a paste | Effect |
|---|---|---|
| Submit | `0x0D` | Submit the buffer |
| Submit, alias | `0x04` or end of input, buffer non-empty | Submit the buffer |
| End of input | `0x04` or end of input, buffer empty | End without dispatching |
| Newline | `0x0A` | Append one newline |
| Newline, passive | `0x1B 0x0D` and the two CSI-u encodings | Append one newline if they arrive; never negotiated |
| Cancel | `0x03`, or SIGINT | Abandon |
| Delete rune | `0x7F`, `0x08` | Remove the last rune |
| Delete word | `0x17` | Remove the trailing non-whitespace run, then preceding whitespace |
| Delete line | `0x15` | Remove back to and including the previous newline |

Inside a bracketed-paste block every byte is literal. That is what makes Enter
safe as a submit gesture, and it is also why the requirement it rests on is
scoped to terminals that delimit pastes: where markers never arrive, a pasted
newline is a typed newline and the paste submits at its first line. That
degradation is stated in the requirements, documented for developers, and pinned
by a characterization test rather than mitigated.

Three conclusions were reached independently by more than one decision and are
recorded as settled: no new dependency is needed; the payload and the rendering
must be produced by separate code paths; and `internal/tui` is the wrong home for
new code because of its sync obligation.

## Solution Architecture

### Components

```
internal/promptcapture/
  promptcapture.go       Read (binds os.Stdin/os.Stderr) + read (injectable core)
  neutralize.go          byte-level display neutralization
  terminal.go            raw-mode and paste-mode lifecycle, signal handling
internal/cli/
  prompt.go              + IsStderrTTY, beside the existing IsStdinTTY
  dispatch.go            + dispatchPromptCapture seam, arity branch, gate
```

### Core interface

```go
// Read runs the capture against the real terminal.
func Read(ctx context.Context, limit int) (string, error)

// read is the testable core: tests inject a reader and a writer.
func read(ctx context.Context, stdin io.Reader, stderr io.Writer, limit int) (string, error)

var ErrCanceled    = errors.New("promptcapture: canceled")
var ErrEndOfInput  = errors.New("promptcapture: end of input on an empty buffer")
```

`limit` is a parameter rather than a package constant: the ceiling is derived in
`internal/cli` from the argument-length maximum minus the reserve, and the core
has no business re-deriving it.

### Data flow

Bytes arrive from standard input in chunks. The loop maintains two pieces of
cross-chunk state: a held-back prefix, when a chunk ends partway through what may
be a paste marker, and a carriage-return flag, so that a `0x0D` at the end of one
chunk and a `0x0A` at the start of the next collapse to a single newline. Inside
a paste block bytes append to the payload untouched. Outside it, bytes route
through the gesture table.

Two outputs leave the loop and never mix: the payload buffer, which grows and
truncates only at its end, and the transcript, which appends bounded neutralized
records to standard error.

### Neutralization rule

Tab passes through. Every other C0 control byte and DEL renders in caret
notation; C1 controls and invalid UTF-8 render as hex escapes. The resulting
invariant is testable and stronger than stripping: nothing emitted to the
transcript moves the cursor up, left, or to column zero.

### Terminal lifecycle

Raw mode is applied to the standard input descriptor -- deliberately unlike the
existing picker, which raws the standard error descriptor while reading standard
input, correct only because the two usually name the same device. Two states are
held: the pre-capture state every exit path restores, and the raw state a resume
returns to.

Restoration runs from a function-scoped defer plus a handler covering SIGINT,
SIGTERM, SIGHUP, SIGTSTP, and SIGCONT. Suspend has one non-obvious step:
resetting SIGTSTP to its default disposition does not reliably suspend the
process in Go, so the handler restores the terminal and raises SIGSTOP itself,
leaving re-entry into raw mode to the SIGCONT branch. That sequence was verified
end-to-end on a pseudo-terminal; the textbook idiom silently fails to suspend.

All handlers are torn down before `runDispatch` reaches the attach handoff, so
`claude attach` inherits a clean terminal and default signal dispositions.

### Call site and gate

The capture sits after the workspace, agent, and binary preflight checks and
before the first instance is created. Abandonment at the prompt is therefore
outside the rollback window entirely -- nothing exists to roll back -- and the
command fails fast on a missing binary or wrong workspace before the developer
types anything.

The interactivity gate requires both standard input and standard error to be
terminals. It is evaluated only in the zero-argument branch, so the
positional-argument path never consults terminal state.

## Implementation Approach

1. **Harness prerequisites.** Add the chunked feed, the held-open-pipe step,
   bounded timeouts on any step supplying standard input, and a signal-sending
   step. Without the chunked feed the terminal-driven scenarios fail on a harness
   defect rather than on the code.
2. **The capture core.** The reader loop and its cross-chunk state, driven by
   injected reader and writer, with the neutralizer. Tested at chunk size one,
   which is where the marker-splitting and carriage-return-flag bugs live.
3. **Terminal lifecycle.** Raw mode, paste mode, the signal set, and the
   suspend/resume sequence.
4. **Command wiring.** The arity change, the interactivity gate and its new
   standard-error seam, the capture seam, and the ceiling enforcement on the
   capture path.
5. **Reachability guard.** The test that drives the launcher path used by
   `niwa watch` with the capture seam stubbed to fail on call.
6. **Documentation.** Usage and long help, the README, and the degradation on
   terminals without paste delimiters.

## Security Considerations

**Pasted content is untrusted with respect to the terminal.** A log can contain
cursor movement, screen clears, and colour. Echoing it raw would let pasted
content redraw the display -- including over the prompt that says how large the
input is. The neutralizer is the mitigation, and its invariant (nothing emitted
moves the cursor up, left, or to column zero) is the security property, not a
cosmetic one.

**Pasted content is untrusted with respect to process control.** With ISIG set, a
pasted `0x03` terminates the process and a pasted `0x1A` suspends it, mid-capture,
losing the buffer. Clearing ISIG is what makes the payload-preservation
requirement satisfiable; `IXON` and `IEXTEN` carry the same hazard for flow
control and literal-next and are cleared by the same call.

**The payload becomes an autonomous worker's opening instruction.** This is not
new -- the positional path has always had it -- and the mitigation is unchanged:
the prompt is one argv element, never passed through a shell, so the content
cannot escape into command construction. The capture does not widen this surface;
it changes only where the bytes come from. A developer pasting a log they did not
write should know it will be read as instructions, which is a property of
dispatch rather than of this feature.

**Terminal state is a shared resource.** Failing to restore raw mode leaves the
next command in the developer's shell without line editing or signal handling.
The failure is silent, delayed, and attributed elsewhere. This is why restoration
is a signal-handled discipline rather than a defer, and why suspend and resume
are covered rather than assumed.

**No new external input, no new privilege, no new persistence.** The capture
reads a terminal the process already owns, writes to a stream it already writes
to, and adds no file, socket, or credential surface. The prompt is still never
written to disk.

## Consequences

### Positive

- The gesture matches what five independently built terminal tools converged on
  and what the developer already knows from other tools.
- Abandonment is free: no instance exists to reclaim, by construction rather than
  by rollback.
- The default attach survives, because the capture reads the terminal rather than
  draining a pipe, removing the detachment the existing workaround forces.
- No new dependency.
- The payload-versus-display split is structural, which makes both the fidelity
  guarantee and the display-safety property testable without a terminal.

### Negative

- A terminal state machine with signal handling is genuinely more code than
  calling a line editor, and it is code that is hard to get right at the chunk
  boundaries.
- On terminals without paste delimiters, a multiline paste submits at its first
  line and the remainder reaches the shell. This is documented and pinned by a
  test rather than mitigated, and it is the accepted cost of Enter-submits.
- The transcript shows a bounded summary of a large paste rather than its full
  text, so the developer confirms extent and identity rather than reading every
  line back.
- niwa acquires its first signal-handling code, which is a new class of thing to
  maintain.

### Mitigations

- The cross-chunk state is tested at chunk size one, which is the boundary where
  the marker and newline bugs live.
- The degradation on non-delimiting terminals is stated in the requirements,
  documented where developers will find it, and pinned by a characterization
  test so a future change to it is deliberate.
- Terminal restoration is asserted by comparing terminal state before and after
  across submit, abandonment, interrupt, termination, and suspend/resume.
