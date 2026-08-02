---
schema: plan/v1
status: Active
execution_mode: single-pr
milestone: dispatch prompt spill
issue_count: 6
upstream: docs/designs/current/DESIGN-dispatch-paste-prompt.md
---

## Status

Active

Plans the amendment to `niwa dispatch`'s prompt handling: a prompt too large to
travel as one argv element is written to a file inside the instance and the
worker receives a pointer plus a fenced excerpt, instead of being refused.

The upstream DESIGN is at status Current rather than Accepted, because the
feature shipped and this amendment re-opened it in place. The chain is BRIEF
(Done) -> PRD (In Progress) -> DESIGN (Current) -> this plan.

## Scope Summary

Remove every user-facing prompt size limit from `niwa dispatch`, and route an
oversized prompt through a per-launch file inside the instance rather than
refusing it. Covers the capture's limit machinery, the launcher's prompt
parameter split, the spill itself, and the documentation and upstream artifacts
that still describe a refusal.

## Decomposition Strategy

**Horizontal**, with one deliberate ordering constraint rather than a skeleton.

A walking skeleton would mean landing a thin end-to-end spill first and
thickening it. That is the wrong shape here for a specific reason: the
end-to-end path is short and already exists -- prompt in, argv out, worker
launched -- and the risk is not integration but the two places where a naive
change is silently wrong. The launcher's prompt parameter split stops the
empty-prompt guard from firing once keep-alive arms, and the capture's limit
removal touches the paste-boundary state machine that the package's tests exist
to protect. Both are depth problems in known components, not integration
problems between them.

So the work goes layer by layer, with the sequencing chosen so that each issue
leaves the command working. The one hard constraint: the launcher split and the
spill land together, because the split without the spill is a refactor with no
purpose, and the spill without the split writes niwa's keep-alive instruction
into the file where the developer's text belongs.

**Execution mode: single-pr.** No hard constraint forces multiple PRs -- one
repository, no merge gate, no workflow that must reach main before it can be
invoked. And the pieces are not independently useful: removing the capture's
ceiling without the spill would let an oversized prompt through to die at exec,
which is worse than the refusal it replaces. The value confirmation therefore
fails for every proper subset, which is the test for staying single-pr.

## Issue Outlines

### 1. Bound the transcript's per-byte cost

**Complexity**: testable

**Goal**: Make the capture's rendering cost proportional to what it displays
rather than to what it holds, ahead of any change that lets it hold megabytes.

Two costs are invisible at 127 KB and painful at 8 MB. The transcript writes to
bare standard error, so each echo is an unbuffered write syscall -- on a
terminal without paste delimiters, where every pasted byte arrives as typed
input, a 582 KB paste becomes roughly 582,000 syscalls. And rendering a pasted
block converts the whole block to a string, then converts an entire long line to
a rune slice before cutting it to terminal width, allocating 2.4 MB to display
100 characters.

**Acceptance Criteria**:

- Accepting a 614,400-byte pasted block issues fewer than one write to the
  render target per hundred input bytes.
- Rendering a pasted block does work proportional to terminal width, not to the
  block: neither the full block nor a full line is converted to a rune slice to
  produce a bounded summary.
- Allocation per input byte does not grow by more than 1.5x between 61,440 and
  614,400 bytes, for a single-line paste, a multi-line paste, and typed input.
  An upper bound only.
- Every existing test in `internal/promptcapture` passes unchanged. This issue
  changes no observable behavior.

**Dependencies**: None

First because it is independently correct, and because later issues feed the
capture payloads large enough to make these costs bite.

### 2. Remove the capture's size ceiling

**Complexity**: critical

**Goal**: Delete the transport ceiling from `internal/promptcapture` and leave a
memory backstop in its place, so no size is ever surfaced to the developer.

`Read` loses its `limit` parameter; the backstop becomes a package constant
derived from local page arithmetic, with a test in `internal/cli` asserting it
clears 64 times the exec cap. The over-ceiling mark, the submit block, the
recovery message, and both ceiling-flavoured refusals go. The banner loses its
size clause. The cumulative bound stays, with its magnitude changed and its
refusal edge-triggered.

**Acceptance Criteria**:

- Over the sample {0, 1, 131,070, 131,071, 131,072, 614,400} bytes, every input
  is accepted and returned byte-for-byte.
- Over that sample and over the bytes written before the first read, nothing on
  the render target contains "limit", "too long", or "too large",
  case-insensitively.
- Six appends of 614,400 bytes each are all accepted, so pasting a large log
  twice does not refuse.
- The backstop constant is at least 64 times the exec cap, asserted against the
  derivation rather than a copied literal.
- An append crossing the backstop is refused in full, the buffer is unchanged,
  and the refusal names no byte ceiling and gives no size advice.
- Feeding bytes past the backstop as typed input emits exactly one refusal, not
  one per byte, and a deletion that brings the buffer back under re-arms it.
- The deleted tests are deleted, not commented out or skipped:
  `TestCeilingRetainsWhatCrossedAndDeletionRecovers` asserts a user-facing
  ceiling exists and must go with it.

**Dependencies**: `<<ISSUE:1>>`, to avoid two changes to the same rendering code
in flight at once.

### 3. Split the launcher's prompt parameter

**Complexity**: critical

**Goal**: Give the launcher the developer's text and niwa's prepends
separately, so a later spill can move only the former.

`realDispatchLaunch` takes `prefix` and `body` instead of one `prompt`.
`runDispatch` stops concatenating the keep-alive instruction and passes it as
the prefix; both `niwa watch` call sites pass an empty prefix. The spill seam is
introduced behind the parameter but does nothing yet.

**Acceptance Criteria**:

- The empty-prompt guard tests `body`, not the composed string. With keep-alive
  armed and an empty task, the launch is refused -- a test that fails if the
  guard is ported as `prefix+body == ""`.
- With keep-alive armed, the worker's argv element is byte-identical to what it
  was before this change, on both the capture and positional paths.
- The pre-exec assertion runs on the composed string and still names the exec
  cap rather than surfacing an operating-system error.
- All three `dispatchLaunch` call sites are updated; no caller passes a
  concatenated prompt.
- `go vet ./...` is clean and the full suite passes. This issue changes no
  observable behavior.

**Dependencies**: None

Touches `internal/cli` only, so it is independent of issues 1 and 2.

### 4. Spill an oversized prompt to a file

**Complexity**: critical

**Goal**: The behavior change. When the composed argv string would exceed the
exec cap, or the body contains a NUL, write the body to a file inside the
instance and hand the worker a pointer plus a fenced excerpt.

**Acceptance Criteria**:

- A prompt whose composed argv string is exactly the exec cap does not spill;
  one byte more does.
- A prompt just under the cap that crosses it once the keep-alive instruction is
  prepended does spill; the same prompt with keep-alive unarmed does not.
- With keep-alive armed and the prompt spilled, the argv element still begins
  with the arming instruction.
- The spill file's bytes equal the submitted bytes exactly -- no header, footer,
  or trailing newline.
- The argv element carries an absolute path, a read instruction, a fence built
  from a fixed prefix plus the launch token, a statement of how many bytes of
  how many were quoted, and at least 512 bytes of excerpt. Two prompts identical
  for their first 512 bytes and differing at byte 513 produce different argv
  elements.
- The excerpt is asserted not to contain the launch token rather than scrubbed
  of it; the token is not derived from the instance name's random suffix.
- The file is mode 0600, in a directory created at 0700, named so the instance's
  `*.local*` ignore pattern covers it.
- A second launch into the same instance produces a second file, neither
  overwriting the other. A launch whose spill directory exists but is a symlink,
  a non-directory, or more permissive than 0700 fails and provisions nothing.
- A prompt containing a NUL spills at any size; the NUL survives in the file and
  no argv element carries one.
- The file still exists after the launch call returns.
- With the spill write forced to fail, the dispatch reports it and leaves no
  instance. With the spill seam stubbed to a no-op, an over-cap string is
  refused before exec with a message naming the cap.
- `niwa watch`'s review and resume templates are below the threshold and do not
  spill, pinned so a template grown past it is caught as a change.
- No flag, config key, or environment variable influences the decision, the
  path, or the excerpt size.

**Dependencies**: `<<ISSUE:3>>` (needs the split parameter), `<<ISSUE:2>>` (the
capture must stop refusing before oversized input can reach the launcher).

### 5. Functional scenarios for the spill

**Complexity**: testable

**Goal**: Cover end to end what the unit tests cover at the seam, including the
one property no unit test can reach: that the worker can actually resolve the
path.

The harness needs a step that generates a prompt of N bytes, because the
existing runner splits its command string on whitespace and cannot express a
131 KB argument in a feature file.

**Acceptance Criteria**:

- A captured paste larger than the exec cap dispatches; the worker's argv names
  a file inside the instance; the file's contents equal the prompt
  byte-for-byte; and the fake worker resolves the path from a working directory
  other than the instance, so an instance-relative path fails the scenario.
  Through the capture, not a positional argument: the harness cannot exec the
  binary with an argument past the limit under test, which is the same wall a
  developer's shell hits.
- Reclaiming the instance behind a spilled dispatch removes the spill file.
- A pasted multiline block still dispatches with its text verbatim.
- Every step supplying the binary a standard input it does not control carries a
  bounded timeout.

**Dependencies**: `<<ISSUE:4>>`.

### 6. Correct every artifact that still describes a refusal

**Complexity**: simple

**Goal**: Leave nothing claiming a large prompt is refused or that a caller must
write a file to stay under the argument limit.

**Acceptance Criteria**:

- A grep over a fixed file list finds no surviving claim: the command's long
  help, the README, `internal/workspace/rootskills/dispatch/SKILL.md`,
  `docs/prds/PRD-instance-dispatch.md` R43, and
  `docs/designs/current/DESIGN-instance-dispatch.md` where it restates that
  requirement.
- The two upstream artifacts are at terminal status and are annotated in place
  naming this PRD as superseding, not rewritten.
- No code comment cites a requirement number whose meaning differs between the
  two PRDs without naming which document it means.
- The SIGQUIT comment in `internal/promptcapture/terminal.go` no longer claims
  the default action dumps core: the Go runtime intercepts SIGQUIT, prints
  goroutine stacks, and exits 2. The handler stays, justified on terminal
  restoration.
- The `/dispatch` skill keeps recommending a brief file, on its own merits,
  without citing a limit that no longer refuses anything.

**Dependencies**: `<<ISSUE:4>>`, so the documentation describes shipped
behavior.

## Implementation Sequence

**Critical path:** 1 -> 2 -> 4 -> 5. Issue 3 is off the critical path and can
land at any point before 4.

**Parallelizable:** issues 1 and 3 touch disjoint packages
(`internal/promptcapture` and `internal/cli` respectively) and have no ordering
between them. Issues 5 and 6 both depend only on 4 and are independent of each
other.

**Where the risk is.** Issue 2 deletes tests that assert the behavior being
removed, so it is the one place where a reviewer cannot tell a correct deletion
from a lost assertion by reading the diff alone -- each deletion should name the
requirement that retired it. Issue 4 is the largest and has the most acceptance
criteria; its non-obvious failures are the empty-guard regression that issue 3
guards against, and the ordering rule that the spill must run before the size
and NUL assertions rather than after, since the other order reinstates the
refusal on the path this work exists to keep open.

**What stays green throughout.** Issues 1 and 3 change no observable behavior.
Issue 2 changes what the capture accepts but leaves the command's launch path
alone. The command's behavior changes only at issue 4.
