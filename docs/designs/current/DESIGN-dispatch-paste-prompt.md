---
schema: design/v1
status: Current
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
  A prompt too large to travel as one argv element is not refused: the launcher
  splits its prompt parameter into a niwa-authored prefix and the developer's
  body, writes the body to a per-launch file inside the instance, and hands the
  worker a pointer plus a fenced excerpt in place of it.
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

Current

Downstream of `docs/prds/PRD-dispatch-paste-prompt.md` (In Progress). Five
decision questions were investigated independently; the cross-validation that
reconciled them is summarized under Decision Outcome.

Amended after the feature shipped, alongside the PRD amendment that reverses
the size ceiling into a spill route. Four further decision questions were
investigated for the amendment -- where the spill decision lives, what the
pointer element contains, where the file goes, and what becomes of the
capture's limit machinery -- and two of them returned conflicting answers that
Cross-Validation reconciles.

Three claims in the shipped design are retired by the amendment and are called
out where they appear rather than deleted: that the prompt is never written to
disk, that the reader's `limit` parameter belongs to the caller, and that the
size ceiling is a refusal. The Security Considerations section carries the
first of those as a stated reduction rather than a preserved property.

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

- **Payload fidelity is non-negotiable except where an exception is stated.** The
  submitted bytes reach the worker byte-for-byte, apart from three exceptions the
  requirements name: the instruction niwa prepends, the separator inserted at a
  paste boundary, and line-break normalization inside a paste. A component that
  normalizes, replaces, or drops any other byte is disqualified regardless of its
  other merits -- and, decisively, a component that makes the exceptions
  impossible to confine is disqualified too.
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

### Where the spill decision lives

**Chosen: `realDispatchLaunch`, with its prompt parameter split into a
niwa-authored `prefix` and the developer's `body`.** The launcher is the single
function that hands a string to `execve`, and three call sites reach it, so the
guarantee that no launch path exceeds the argument limit becomes a property of
one function rather than a rule three callers must remember. Splitting the
parameter is what lets that depth coexist with keeping niwa's prepends in argv:
`runDispatch` currently concatenates the keep-alive instruction onto the prompt
before calling the launcher, which destroys the distinction the spill needs.

**Rejected: the decision in `runDispatch` only.** Cheapest by a wide margin and
touches no existing test, but the `niwa watch` paths would then refuse rather
than spill, the filename-uniqueness requirement loses its motivation entirely
(a fresh instance per dispatch can never host two spills), and the test seam
that keeps the pre-exec assertion constructible becomes decorative.

**Rejected: the launcher without splitting the parameter.** Cannot be built. By
the time the launcher sees the string it is `keepAliveArmingInstruction +
userText`, so it would write the arming instruction into the file and hand the
worker a pointer that arms nothing. Prefix-matching the constant to strip it
back off would couple the exec layer to the keep-alive feature and break
silently the day a second prepend is added.

**Rejected: a helper called by all three callers.** Preserves the existing test
surface and does reach the watch paths, but leaves the coverage guarantee as a
discipline: a fourth caller that forgets the helper does not spill, it refuses,
which is what the requirements forbid.

### Uniqueness token

**Chosen: one 16-hex-character `crypto/rand` token per launch, naming both the
spill file and the excerpt's fence.** This is where two decisions collided. The
pointer investigation wanted an unpredictable fence, because the excerpt is
arbitrary developer text and a fixed marker constant is forgeable by the very
content it delimits -- and the whole point of the feature is carrying whole CI
logs, so "no realistic payload contains our marker" is not a claim worth
making. The placement investigation wanted an instance-scoped counter, because
it read the cross-path criterion as demanding byte-identical pointer text.

The counter loses because its only advantage was satisfying that reading, and
the criterion now normalizes the uniqueness token as well as the instance
directory. The nonce's advantage cannot be recovered any other way: the token
is minted after the prompt bytes are read, so no submitted text can contain it
regardless of who wrote it. One source of unpredictability does both jobs.

**Rejected: a content hash.** Satisfies the cross-path criterion perfectly and
fails uniqueness outright -- `niwa watch`'s continuation prompts are fixed
templates that hash identically on every pass, so the second launch overwrites
the first.

**Rejected: `os.CreateTemp`.** Audited `O_EXCL` loop, correct mode, fewer
lines. It loses only because its names are not normalizable to a stable shape
in the cross-path comparison, which is a thinner reason than it looks; if that
criterion is ever relaxed to compare shape, this becomes the answer.

### The capture's limit machinery

**Chosen: delete the concept. `Read` loses its parameter; the memory backstop
becomes a package constant.** The shipped comment justifying the parameter --
"the ceiling is derived in `internal/cli` from the argument-length maximum
minus the reserve, and the core has no business re-deriving it" -- is void in
every clause. The reserve is retired, the caller no longer has a ceiling, and a
memory backstop is not caller policy about the prompt but a statement about how
much the reader will hold in its own address space.

The decisive argument is enforceability. A constant can be asserted by a test
against its derivation; a call-site argument cannot. A future caller passing a
small number would compile, pass every test, and silently reinstate the wall
the amendment removes.

**Rejected: keep the parameter and pass the backstop from `internal/cli`.**
Smallest diff and no new package, but it leaves a public API whose only
parameter is a number no caller has an opinion about, and it preserves exactly
the knob the requirement was written to prevent.

**Rejected: a round literal.** `8 << 20` is not more principled than
`64 * maxArgStringBytes`, only less tamper-evident -- and a round number is
precisely how the cap regressed before, when a bound that "reads like a
conservative bound" turned out to be exactly the operating system's limit.

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

### The spill, added by the amendment

Above the argument limit the prompt does not fail, it changes vehicle. The
launcher receives the developer's text and niwa's prefix separately, and when
their combined length would exceed what `execve` accepts for one argument it
writes the text to a per-launch file inside the instance and composes a pointer
in its place. Below the limit nothing changes at all.

The payload-versus-display split the shipped design established turns out to
carry the amendment too, and this is the load-bearing conclusion of the
cross-validation. The **file** gets the developer's bytes raw, because carrying
the error verbatim is the whole point. The **excerpt** in the pointer goes
through the same neutralizer the transcript uses, because an argv element
cannot contain a NUL byte -- verified by probe, `exec.Command` with an embedded
NUL returns `invalid argument` -- so an unsanitized excerpt would carry a
launch failure into the one path whose purpose is that a large prompt always
dispatches. The neutralizer needs one widening for this use: line feed must
pass through, or a stack trace arrives as a single line of `^J`. A line feed
cannot introduce an escape sequence, so the neutralizer's stated property
survives verbatim and only its layout set widens.

That probe also exposed a defect the amendment does not cause and should not
leave standing: a below-threshold prompt containing a NUL dies at exec today
with an opaque message, and neither the size check nor the pre-exec assertion
catches it. It is one paste away, since the capture preserves raw control bytes
by design. The amendment fixes it where the bytes still travel in argv; above
the threshold it disappears on its own.

## Solution Architecture

### Components

```
internal/execlimit/
  execlimit.go           MaxArgString, the one declaration of the per-string
                         exec cap, in a file with no build constraints
internal/promptcapture/
  promptcapture.go       Read (binds os.Stdin/os.Stderr) + read (injectable core)
  neutralize.go          byte-level display neutralization
  terminal.go            raw-mode and paste-mode lifecycle, signal handling
internal/cli/
  prompt.go              + IsStderrTTY, beside the existing IsStdinTTY
  dispatch.go            + dispatchPromptCapture seam, arity branch, gate
  dispatch_launcher.go   + split prompt parameter, spill seam, pre-exec assertion
  dispatch_spill.go      + spill write, pointer composition, excerpt rendering
```

`internal/execlimit` exists so the exec cap has one declaration that both
`internal/cli` and `internal/promptcapture` can reach. It is real ceremony for
one constant, and it drags a package whose doc comment makes a point of its
stdlib-only isolation into a niwa-internal dependency. The honest defence is
that the reader does not need to know about `execve`; it needs a number, and
the requirement chose to express that number as a multiple of an `execve` fact
so it cannot be quietly tuned down. If a reviewer prefers the smaller diff, the
fallback is to duplicate the page arithmetic in `promptcapture` and assert the
floor from a cross-package test -- same tamper-evidence, one duplicated
expression, no new package. That is a live option, not a strawman.

### Core interface

```go
// Read runs the capture against the real terminal.
func Read() (string, error)

// read is the testable core: tests inject a reader and a writer, and a
// backstop small enough to drive the refusal path without allocating 8 MB.
func read(stdin io.Reader, stderr io.Writer, backstop int) (string, error)

// maxBufferBytes is the memory backstop, not a prompt ceiling. Stated as a
// derivation with a floor so it cannot be quietly tuned down into the wall
// this feature exists to remove.
const backstopMultiple = 64
const maxBufferBytes = backstopMultiple * execlimit.MaxArgString

var ErrCanceled    = errors.New("promptcapture: canceled")
var ErrEndOfInput  = errors.New("promptcapture: end of input on an empty buffer")
```

The shipped signature took a `limit` parameter, justified on the grounds that
the ceiling was derived in `internal/cli` and the core had no business
re-deriving it. That reasoning is void: the reserve is retired, the caller no
longer has a ceiling to pass, and what remains is a memory bound that belongs
to the reader rather than to its caller. The parameter survives only on the
unexported core, where it is a test seam.

```go
// dispatchLaunch's prompt is split so the spill can move only the developer's
// bytes. prefix is niwa-authored text that always rides argv.
func realDispatchLaunch(ctx context.Context, instanceDir, prefix, body string,
    passthrough, env []string) error

// spillPrompt is the seam that keeps the pre-exec assertion constructible:
// a test stubs it to a no-op and drives an over-limit string at the guard.
var spillPrompt = writeSpillFile
```

### Data flow

Bytes arrive from standard input in chunks. The loop maintains **three** pieces
of state that survive a read boundary, and all three are where the implementation
risk lives:

- **A held-back prefix**, when a chunk ends partway through what may be a paste
  marker. The bytes are not appended until the loop knows whether they complete a
  marker or are payload.
- **A carriage-return flag**, so a `0x0D` ending one chunk and a `0x0A` opening
  the next collapse to one line feed rather than two breaks.
- **A pending line break**, set when a pasted block ends on an unterminated line
  and flushed immediately before the next byte is appended. Setting it at the
  end-of-paste marker unconditionally would give a bare paste a trailing newline
  it never had; deferring it is what makes R5's separator appear only when typed
  text actually follows.

Inside a paste block, line-break bytes are normalized per R41 -- a lone carriage
return and a carriage-return line-feed pair each become one line feed -- and every
other byte appends untouched. Outside a paste block, bytes route through the
gesture table.

R41 is a deliberate, stated exception to byte-for-byte fidelity, and it is the
one place the design alters pasted bytes. Terminals differ in which byte they
deliver for a pasted line break; Terminal.app sends carriage returns. Preserving
them exactly would hand the worker a stack trace whose lines are separated by
carriage returns, which renders as a single overwritten line and defeats the
purpose of carrying the error verbatim. The alternative -- normalizing at the
worker instead -- was rejected because it would put terminal-specific knowledge
in a component that has never needed it.

Two outputs leave the loop and never mix: the payload buffer, which grows and
truncates only at its end, and the transcript, which appends bounded neutralized
records to standard error.

### The memory backstop in the loop

The shipped loop carried a transport ceiling and a retention bound derived from
it. The ceiling is gone; what remains is one bound, and it is about memory
rather than about prompts.

Crossing it refuses the append in full and retains none of it: a partially
retained paste is worse than none, because it looks complete and is not.
Reading continues -- the backstop reports and does not end the command. The
refusal names no byte ceiling and gives no size advice, because under the spill
there is nothing for the developer to do about size. It tells them their state
instead: that nothing from this input survived, that what they entered before is
unchanged, and that the capture is still open.

Two properties of the shipped check survive the change in magnitude and one
does not.

The check stays **cumulative**, measured over the live payload the process is
holding. A per-append bound would bound nothing, since many appends just under
it are unbounded in total.

The **unit of refusal** is now stated, because at 8 MB the distinction becomes
reachable. For a pasted block the unit is the block. For typed input it is one
byte -- and on a terminal without paste delimiters every byte of a paste
arrives as typed input, so a per-byte refusal would emit millions of lines and
stall the capture. The refusal is therefore edge-triggered: a flag set on the
crossing and cleared when a deletion brings the buffer back under. That is the
same edge-trigger the shipped over-ceiling mark used, repurposed to the bound
that survives.

The **over-ceiling mark itself** goes, and nothing replaces it. Re-pointing it
at the backstop would be dead code by construction: the append that would cross
the backstop is refused, so the buffer can never exceed it, so the mark could
never be set. If it somehow did fire it would be exactly the wall the amendment
removes, at a size the requirements say must not be described as a product
limit.

The neutral size *reports* survive and should: the byte count after a delete
and the extent line after a paste state what the developer has, not what they
are allowed.

Whitespace-only input is refused at submit with the command's existing
empty-prompt error. The existing check runs before the capture and compares
against the empty string, so it does not cover this; the capture path validates
its own result. This is a content rule, not a size one, and is unaffected.

### Where the transcript costs real time

Two costs in the shipped rendering are invisible at 130 KB and are not at 8 MB.
Both are fixed here because they are what a developer actually feels, not
because a benchmark asks.

The transcript writes to bare standard error, so every echo is an unbuffered
write syscall. On the path where a paste is indistinguishable from typing, a
582 KB paste becomes roughly 582,000 syscalls. Wrapping the transcript writer
and flushing once per read chunk fixes it, and costs nothing in latency: a read
boundary is where the terminal delivers input anyway.

Rendering a pasted block converts the whole block to a string and, for a long
single line, converts the entire line to a rune slice before slicing it to
terminal width. A 614 KB single line allocates 2.4 MB to display 100
characters. Finding the first and last line by byte scan, and neutralizing only
as much as the display can hold, makes the cost proportional to the terminal
width, which is what the shipped design claimed for it in the first place.

Two further optimizations -- collapsing the paste buffer into the payload
buffer with a mark-and-rollback, and hand-rolling the growth curve -- were
considered and are **not** taken. They touch the paste-boundary state machine,
which is the most delicate code in the package, and they exist to move an
allocation multiple rather than to fix anything a developer experiences. The
per-byte cost is already independent of what has been entered, which is the
property the requirement is actually about.

### Neutralization rule

Tab passes through. Every other C0 control byte and DEL renders in caret
notation; C1 controls and invalid UTF-8 render as hex escapes.

The property to implement and test is the escaping rule itself, not a summary of
it. Because no escape-introducing byte survives, the transcript cannot carry an
operating-system command, a device-control or application-program string, a mode
set, or a cursor movement -- an implementation that only guaranteed "the cursor
never moves up, left, or to column zero" would satisfy a weaker property and
still leak a clipboard write or a title change.

The rule also protects the payload, which is the stronger reason for it. A
terminal only answers sequences it renders; because the transcript renders none,
an embedded device query cannot provoke a response that would arrive back on
standard input and land inside the capture.

### Terminal lifecycle

Raw mode is applied to the standard input descriptor -- deliberately unlike the
existing picker, which raws the standard error descriptor while reading standard
input, correct only because the two usually name the same device. Two states are
held: the pre-capture state every exit path restores, and the raw state a resume
returns to.

Restoration runs from a function-scoped defer plus a handler covering SIGINT,
SIGQUIT, SIGTERM, SIGHUP, SIGTSTP, and SIGCONT. Suspend has one non-obvious step:
resetting SIGTSTP to its default disposition does not reliably suspend the
process in Go, so the handler restores the terminal and raises SIGSTOP itself,
leaving re-entry into raw mode to the SIGCONT branch. That sequence was verified
end-to-end on a pseudo-terminal; the textbook idiom silently fails to suspend.

SIGQUIT is handled for a reason worth stating: its default action dumps core, and
the core would contain the captured payload. Leaving it unhandled would put the
prompt on disk, which nothing else in this path does.

The SIGCONT branch checks that the process is in the foreground process group
before re-entering raw mode. A capture that is suspended and then resumed in the
background must not stamp raw settings onto the terminal the shell is using.

End of input arriving after a hangup is treated as abandonment rather than
submission, so closing a terminal window mid-capture cannot dispatch a
half-composed prompt.

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

### The spill, outside the loop

The corrected cap and the pre-exec check are both on the main branch. The
amendment keeps them and retires the third piece the same change introduced.

**The reserve is gone, and the reason is worth stating because it explains why
the change is small.** The reserve existed because a *refusal* has to happen
before provisioning while the keep-alive prepend is decided after -- so the only
sound answer was to reserve the worst case up front and charge it to the
developer. Making the decision a route rather than a refusal dissolves that
constraint: nothing is being denied, so the decision no longer has to be early,
and once it can be late it can be made against the final argv string where no
estimate is needed. The developer stops paying for a prepend that may not
happen.

**Where the decision sits.** The keep-alive prepend depends on instance
settings that do not exist until the instance does, so the decision cannot move
earlier than that point. It lands between the prepend and the launch, inside
the rollback window: a failed spill write returns an error, the deferred
destroy fires, and the instance takes any partial file with it.

**What the file is.** `<instance>/.niwa/dispatch-prompts/prompt-<token>.txt`,
the directory created at `0700` and the file opened `O_CREATE|O_EXCL|O_WRONLY`
at `0600`.

The subdirectory is not decoration. The instance's own state directory is
created world-readable and a later create call does not lower an existing
directory's mode, so the requirement that the containing directory be no more
permissive than the file is unsatisfiable without a directory niwa creates
itself. There is precedent: the instance's sessions and worktrees directories
are both created at `0700`. It must not be named `worktrees`, because the
worktree classifier walks up looking for exactly that shape and would
misclassify a worker whose working directory landed inside.

The extension is `.txt` and specifically not `.md`: the content is a pasted log
carried verbatim, and calling it markdown invites the reading agent to
interpret backtick fences that are just log output.

**The workspace root's state directory would have destroyed the file.** The
config snapshot is rotated wholesale on every apply, carrying only two things
into staging -- and the code comment there records that this was found the hard
way, on precisely this failure: the dispatch skill writes a brief, then `niwa
dispatch` refreshes the same directory before the worker can read it. The
instance's state directory is structurally clear of that machinery, and every
other walker in the tree skips it explicitly.

**Disposal is instance reclamation and nothing else.** The file is never
deleted after a successful launch, because the worker is daemon-backed: the
launch call returns long before the worker registers -- the command then polls
the jobs directory for up to thirty seconds just to discover the session exists
-- so a post-launch delete would race the read it exists to serve.

The one path that launches repeatedly into an instance it did not create
accumulates at most one file per pass, in an instance the reaper backstop
already owns. No sweep: a pre-launch sweep would race the read of a
still-running prior worker, and the shared launcher cannot know whether its
caller has quiesced anything. Bounded accumulation inside a doomed directory
beats a race against a read. In practice the case does not arise, because that
path builds its prompts from fixed templates far below the threshold -- which a
test pins, so a template grown past it is caught as a change rather than
discovered as a spilled file.

**Create the directory with a single-level create, not a recursive one.** A
recursive create under a deleted instance root would rebuild the chain and
leave a directory matching the dispatch-name signature but carrying no instance
metadata -- which the destroy validator then refuses to act on, making it
unreclaimable by both the rollback and the reaper. That is the exact failure
the dispatch design works hardest to prevent.

### What the worker receives

The pointer element is niwa's prepends first, unchanged, then: a sentence
saying the text was too large to pass as an argument; a numbered instruction to
read the file in full, stating that it is the complete task and nothing else in
the message is; the absolute path on its own line; a sentence framing what
follows as a quoted prefix rather than instructions; the fenced excerpt; and a
closing line saying how many bytes of how many were quoted.

The prepends stay first because the keep-alive instruction opens "before
starting the task below" and closes "then proceed with the task" -- a forward
reference a reordering would dangle. It also keeps the message's first bytes
niwa's own fixed text on every path, which matters for the next paragraph.

The excerpt is 4096 bytes. The floor is measured rather than guessed: a real Go
panic with its first frame is 126 bytes and a `go test` first-failure block is
55, so 4096 clears the required 512-byte floor by a wide margin and real output
by more. At the top it is 3.1% of the argument limit, so the pointer stays
small with room to spare. Bytes only, with no line cap -- a line cap would let
two prompts differing at byte 3000 but after the fortieth line produce
identical argv elements, which is the distinguishability failure the floor
exists to forbid.

The fence is a fixed prefix plus the launch's random token, on its own line at
each end, and the token is redacted from the excerpt as a belt. A fixed marker
constant is forgeable by the very text it delimits, and "no realistic payload
contains our marker" is not a claim worth making about whole CI logs. The token
is minted after the bytes are read, so no submitted text can contain it. The
ordering above is the other half of the defence: even a worker that treats
forged post-fence text as instructions has already read niwa's framing.

The path is absolute, because a relative path stops resolving the moment the
worker or one of its subagents changes directory. The cost is that the instance
path embeds the home directory and lands in the worker's transcript -- minor,
and already true of the guard binary path baked into settings hooks.

Cutting the excerpt walks back to a character boundary rather than converting
the string to runes, which for a 614 KB line would allocate 2.4 MB to produce
4 KB. Invalid UTF-8 is handled by the neutralizer rather than by the cut: the
capture preserves invalid bytes in the payload deliberately, so the excerpt
renders them as hex escapes while the file keeps them raw.

The wording mirrors what niwa already tells agents to do for exactly this
pattern in the dispatch skill, and takes its numbered-list shape from the
review prompt builder. niwa recommending one phrasing to agents and using a
different one itself would be indefensible.

## Implementation Approach

1. **Harness prerequisites.** Add the chunked feed, the held-open-pipe step,
   bounded timeouts on any step supplying standard input, and a signal-sending
   step. Without the chunked feed the terminal-driven scenarios fail on a harness
   defect rather than on the code.
2. **The capture core.** The reader loop and its three pieces of cross-chunk
   state, the ceiling refusal and its message, deletion, and the neutralizer,
   driven by injected reader and writer. Tested at chunk size one, which is where
   the marker-splitting, carriage-return and pending-break bugs live.
3. **Terminal lifecycle.** Raw mode, paste mode, the signal set, and the
   suspend/resume sequence.
4. **Command wiring.** The arity change, the interactivity gate and its new
   standard-error seam, the capture seam, the derived ceiling and its reserve,
   validation before provisioning on both paths, and the re-check immediately
   before the worker starts.
5. **Reachability guard.** The test that drives the launcher path used by
   `niwa watch` with the capture seam stubbed to fail on call.
6. **Documentation.** Usage and long help, the README, and the degradation on
   terminals without paste delimiters.

### Added by the amendment

7. **The exec-limit constant moves** into its own package so the reader and the
   command can both reach it, and the reader's `limit` parameter is deleted
   from the exported entry point. Mechanical but wide: it touches every stub of
   the capture seam.
8. **The capture's ceiling machinery comes out.** The over-ceiling mark, the
   submit block, the recovery message, and the two ceiling-flavoured refusals
   go; the cumulative bound stays with its magnitude changed and its refusal
   edge-triggered. The banner loses its size clause. This is the step that
   deletes tests rather than rewriting them, and the deletions should land in
   the same commit as the behavior they describe.
9. **The transcript's per-byte costs.** Buffer the writer, flush per read
   chunk, and bound the paste-summary rendering to the display width. Ahead of
   the spill because it is independently correct and because the spill's tests
   feed payloads large enough to make the current costs hurt.
10. **The launcher's prompt parameter splits** into prefix and body, the spill
    seam is introduced behind it, and the pre-exec assertion grows a NUL
    clause. Every call site updates, including the two in `niwa watch`.
11. **The spill itself.** The directory create, the `O_EXCL` write, the token,
    the pointer composition, and the excerpt rendering.
12. **The upstream annotations.** The two instance-dispatch artifacts at
    terminal status, the dispatch skill's stale caution, and the code comments
    citing requirement numbers that now mean something else.

Steps 7 through 9 are independently shippable and leave the command's behavior
unchanged; 10 and 11 are the behavior change and belong together, because the
split parameter without the spill is a refactor with no purpose and the spill
without the split writes the keep-alive instruction into the file.

## Security Considerations

**Pasted content is untrusted with respect to the terminal.** A log can contain
cursor movement, screen clears, and colour. Echoing it raw would let pasted
content redraw the display -- including over the prompt that says how large the
input is. The neutralizer is the mitigation, and its invariant (nothing emitted
moves the cursor up, left, or to column zero) is the security property, not a
cosmetic one.

**Pasted content is untrusted with respect to process control.** With ISIG set, a
pasted `0x03` terminates the process, a pasted `0x1C` terminates it and dumps
core containing the payload, and a pasted `0x1A` suspends it mid-capture. Each
loses the buffer. Clearing ISIG is what makes the payload-preservation
requirement satisfiable and captures all three as ordinary bytes; `IXON` and
`IEXTEN` carry the same hazard for flow control and literal-next and are cleared
by the same call.

**The payload becomes an autonomous worker's opening instruction.** Argv
construction is unchanged -- the prompt is one element, never passed through a
shell, so content cannot escape into command construction -- but it would be too
strong to say the surface is untouched. This feature exists to move text a
developer has *not* authored, and often has not read in full, into a worker's
opening instruction, and the bounded transcript means the middle of a large paste
is never displayed. Provenance is the surface, and it does shift. The control
that applies is the worker's own permission boundary, not anything in the
capture: a dispatched worker is constrained by what it is allowed to do, and that
constraint is where pasted instructions are contained. Worth knowing when pasting
a log from a source you do not control.

**Terminal state is a shared resource.** Failing to restore raw mode leaves the
next command in the developer's shell without line editing or signal handling.
The failure is silent, delayed, and attributed elsewhere. This is why restoration
is a signal-handled discipline rather than a defer, and why suspend and resume
are covered rather than assumed.

**Retention above the ceiling is bounded.** The oversized refusal keeps the whole
buffer so the developer can delete down to a submittable size, which without a
bound would let a pathological paste grow until the process is killed -- leaving
the terminal raw, since no handler runs. Retention is capped at a stated multiple
of the ceiling; an append that would exceed the cap is refused in full and
retained not at all, rather than truncated, so a partial log is never mistaken
for a whole one.

**A forged paste-end marker submits early.** A pasted payload containing the
end-of-paste byte sequence ends the block from the reader's point of view, so the
remainder is interpreted as typed input and its first line break submits. This is
the same failure mode as a terminal without paste delimiters, arriving by a
different route, and it is documented alongside that degradation rather than
detected.

**The prompt now touches disk above the threshold, and the claim this section
used to make is retired.** The shipped design stated that the prompt is never
written to disk, and handled SIGQUIT specifically so a core dump could not
carry the payload. The amendment gives that up: a spilled prompt exists as a
file for the life of the instance. This is a real reduction, not a
reclassification. A large pasted secret that previously lived only in process
memory now has a path someone can read.

What holds it down: the file is owner-only, in a directory niwa creates
owner-only, inside an instance directory that already holds dispatch state at
the same mode; it is never copied, synced, or logged; and it is removed when
the instance is. The SIGQUIT handling stays and is still worth having, because
below the threshold the payload is still memory-only and a core dump would
still carry it.

For calibration rather than comfort: the positional path was always worse, since
the whole prompt lands in shell history. What changes is that the capture path
used to be strictly better than that and is now better only below the
threshold.

**A NUL byte in the prompt breaks exec, and did before this change.** An argv
element cannot contain a NUL -- verified by probe. The capture preserves raw
control bytes in the payload by design, so a binary-contaminated log is one
paste away from a launch that fails with an opaque operating-system error, with
neither the size check nor the pre-exec assertion catching it. The assertion
grows a NUL clause so the failure names its cause. Above the threshold the
problem disappears, since the bytes travel in a file and only the neutralized
excerpt reaches argv.

**The excerpt is untrusted text inside a niwa-authored instruction.** The
protection is not that the argv element is free of untrusted content -- the
whole prompt always was untrusted -- but that it is never handed to a shell, so
nothing inside it can become command structure. What the fence adds is a
different property: that the developer's text cannot appear to *end* the
quotation and continue as instructions. A fixed marker constant would not
provide it, since the pasted text can contain any byte sequence; the per-launch
random token does, because it is minted after the bytes are read.

**No new external input, no new privilege.** The capture reads a terminal the
process already owns and writes to a stream it already writes to. The spill
adds a file surface and nothing else: no socket, no credential, no network.

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
- The prompt reaches disk above the threshold, retiring a property the shipped
  design named and defended.
- A spilled worker must spend a tool call to reach its task, and one that
  ignores the instruction proceeds on a 4 KB excerpt. The excerpt's own safety
  measures make that worse rather than better: fencing it and rendering escapes
  as caret notation are exactly what stop it reading as the task.
- A developer who pastes a colourized test run gets an opening message littered
  with `^[[0;31m`, which looks like corruption. Stripping the sequences instead
  would read better and would break the neutralizer's stated property, since it
  requires recognizing escape sequences and a malformed one then survives.
- The exec-limit constant moves into a package created for it, pulling a
  stdlib-only reader into a niwa-internal dependency for the sake of one
  number.
- 4096 is a judgment call. It is justified against measured payloads and clears
  the floor widely, but nothing derives it.

### Mitigations

- The cross-chunk state is tested at chunk size one, which is the boundary where
  the marker and newline bugs live.
- The degradation on non-delimiting terminals is stated in the requirements,
  documented where developers will find it, and pinned by a characterization
  test so a future change to it is deliberate.
- Terminal restoration is asserted by comparing terminal state before and after
  across submit, abandonment, interrupt, termination, and suspend/resume.
- The spill's failure modes all route to the existing rollback: a write that
  fails for any reason returns an error inside the window, and the deferred
  destroy takes the instance and any partial file. No write-then-rename dance,
  which would buy nothing here -- one writer, one reader, no concurrent
  observer -- and would cost the `O_EXCL` guarantee the filename uniqueness
  leans on. The close error is checked rather than deferred and swallowed,
  since a delayed out-of-space surfaces there on networked filesystems.
- The keep-alive prepend surviving a spill is asserted directly, because the
  failure it guards against is silent: a session recorded and reported as kept
  alive that was never armed.
