# Decision 3: Placement and seams

Question: where does the capture reader live in the tree, and what seam shape
makes the capture unreachable from the launcher while keeping the
command-level behaviors unit-testable?

## What the tree looks like today

`runDispatch` (`internal/cli/dispatch.go:137-399`) runs in this order:

| Step | Lines | What it does |
|---|---|---|
| (1) | 138-146 | `prompt := args[0]`, then the empty-prompt and `maxPromptBytes` checks |
| (2) | 152-163 | `os.Getwd`, `workspace.ClassifyCwd`, refuse outside a workspace |
| (2b) | 173-181 | resolve the workspace agent, refuse if it is not Claude |
| (3) | 185-187 | `lookClaude()` preflight -- refuse if claude is not on PATH |
| (4) | 197-207 | `sanitizeInstanceSlug` + `dispatchNameSuffix` (pure) |
| (5) | 211 | `reapOpportunistically(workspaceRoot)` -- **first disk mutation** |
| (6) | 216-221 | `provisionInstanceFunc(...)` -- **first instance creation** |
| (7) | 229-234 | arm the deferred self-rollback |
| (8)-(9d) | 241-332 | marker, config load, model resolution, passthrough, keep-alive prepend |
| -- | 334 | `dispatchLaunch(ctx, instancePath, prompt, passthrough, nil)` |
| (10)-(14) | 349-398 | session-id capture, mapping write, hints, attach |

Three checks -- workspace root, agent-is-Claude, claude-on-PATH -- are cheap,
deterministic, and independent of the prompt, and they all sit above the first
disk mutation. That is the window the capture belongs in.

The launcher seam is `dispatchLaunch`, a package-level var
(`internal/cli/dispatch_launcher.go:14`) wired to `realDispatchLaunch`. It has
exactly three call sites: `dispatch.go:334`, `watch.go:579` (`continueReview`),
and `watch.go:826` (`stageReview`). Both watch call sites build their prompt
from a fixed template (`watch.BuildResumePrompt`, `watch.BuildReviewPrompt`)
and pass it as a `string` parameter. R11 therefore reduces to a single
structural claim: **the capture is called only from `runDispatch`, above
`dispatchLaunch`, and `dispatchLaunch` receives a prompt it cannot obtain
itself.**

TTY detection today is split across two spellings. `internal/cli/prompt.go:26-28`
has `IsStdinTTY`, a package-level `func() bool` var stubbable from tests, used
at `init.go:290`, `destroy.go:116`, `destroy.go:239`, `destroy.go:325`.
`internal/tui/picker.go:44-46` has `IsAvailable()`, a plain function (not a var)
that checks `os.Stderr`. The existing both-streams gate is `destroy.go:239`:

```go
if !IsStdinTTY() || !tui.IsAvailable() {
```

Half of that gate is unstubbable, so the four-combination criterion R21 needs
cannot be written against it as it stands.

## Options Considered

### (a) `internal/tui` -- a new file in the picker's package

`internal/tui` holds three files: `picker.go`, `sanitize.go`, `picker_test.go`.
`picker.go:1-14` carries this header verbatim:

```go
// Package tui provides terminal UI primitives. The picker (Pick) is an
// arrow-key driven single-select prompt; SanitizeDisplayString strips
// ANSI escapes from any externally-sourced string before render.
//
// Source provenance: this file is copied from
//
//	tsukumogami/tsuku@c8f58101 (#2369)
//	internal/tui/picker.go
//
// The package's `internal/` location in tsuku blocks cross-module import,
// so we maintain a copy here. When updating either copy, mirror the change
// in the other or document the divergence in the commit message. The two
// copies should be byte-equivalent except for this header and any niwa-
// specific path references.
```

The header says "this file", but the obligation is de facto package-wide. I
diffed all three files against `public/tsuku/internal/tui/`: `picker.go` differs
only by that header and two doc-comment sentences ("recipe description" ->
"caller-supplied description", "install command" -> "calling command");
`sanitize.go` differs only in doc prose and carries **no** provenance header at
all; `picker_test.go` differs only in fixture names (`openjdk`/`temurin` ->
`alpha`/`beta`). So today every file in `internal/tui` is a mirror of a tsuku
file, and one of them silently is.

Consequence for the sync contract: adding `capture.go` makes the package a
*mixture* -- three mirrored files plus one niwa-only file. Anyone syncing the
package has to know which is which, and `sanitize.go` already demonstrates the
failure mode (a mirrored file whose header does not say so). A directory-level
sync (`rsync`, `diff -r`, a `--delete`) would either drop the new file or flag
it as spurious divergence forever.

Consequence for testability: good, and the idiom is proven. `Pick`/`pick`
(`picker.go:64-75`) splits a production entry point over `os.Stdin`/`os.Stderr`
from an unexported core taking `io.Reader`/`io.Writer`, and `picker_test.go`
drives the core with `bytes.Reader` and a `chunkedReader` helper that returns
one escape sequence per `Read` -- exactly the shape the split-read and
line-break criteria need.

Consequence for R11: neutral. A `tui.Capture` export is reachable from any
package in niwa, including `watch.go`, so placement here buys nothing
structurally.

### (b) `internal/cli`, alongside `prompt.go`

`prompt.go:1-4` already declares itself the home of "niwa's first
interactive-prompt primitives ... kept generic so future commands ... can reuse
them", so a second reader beside it is not out of character. No sync contract
applies. The seams (`dispatchPromptCapture`, `IsStderrTTY`) must live in this
package regardless, so this option collapses the reader and its seam into one
place with zero new import edges.

Consequence for testability: this is where it loses. The reader core would be
tested inside package `cli`, which today carries ~50 test files that mutate
process-global state: `chdir(t, dir)` (`dispatch_test.go:56-66`) changes the
process working directory, and `installDispatchFakes` (`dispatch_test.go:85-163`)
rebinds thirteen package-level vars and command flags. Two of the fifteen
reader-core criteria are hostile to that environment -- R37's "a single line of
130,433 bytes ... takes no more than a small constant multiple of the time taken
to accept the same byte count split across many lines" is a timing comparison,
and R30/R27 assert on exact bytes written to a render target. Putting a timing
assertion in a package whose tests chdir the process and rebind globals is a
flake generator, and `go test -run` isolation is not available to CI as a
default.

Consequence for R11: neutral, same as (a) -- Go visibility is package-scoped, so
an unexported `capture()` in package `cli` is callable from `watch.go`.

### (c) A new package

A sibling package under `internal/`, importing `internal/tui` only for
`SanitizeDisplayString`. No sync contract. The reader-core tests run in a
package that contains nothing else, so the timing criterion and the byte-exact
render assertions are not sharing a process with command tests.

Consequence for the sync contract: none, and it leaves `internal/tui` a clean
three-file mirror. That is worth something on its own -- the next person to sync
against tsuku gets an unambiguous directory.

Consequence for R11: neutral again. No placement makes the capture
unreachable by Go visibility; reachability has to be enforced by where the call
sits plus a guard test (below).

Naming matters here more than usual. `internal/capture` collides head-on with
`dispatchCapture` / `captureSessionID` (`dispatch.go:91-93`, `dispatch_capture.go`),
which already means "capture the worker's session UUID" -- an unrelated thing in
the same command. `internal/tui/capture` avoids the collision but re-introduces
the sync hazard by sitting *inside* the mirrored directory. `internal/promptcapture`
avoids both.

## Recommendation

**Option (c): `internal/promptcapture`, a sibling of `internal/tui`.**

### Files

| Path | Contents |
|---|---|
| `internal/promptcapture/promptcapture.go` | `Read` (production entry) + `read` (injectable core) + sentinels |
| `internal/promptcapture/promptcapture_test.go` | the fifteen reader-core criteria |
| `internal/cli/prompt.go` | add `IsStderrTTY` beside `IsStdinTTY` |
| `internal/cli/dispatch.go` | the `dispatchPromptCapture` seam, the gate, the call site |
| `internal/cli/dispatch_prompt_test.go` | the command-level criteria |
| `internal/cli/dispatch_reachability_test.go` | the R11 guard |

### The capture core

Mirror the `Pick`/`pick` split verbatim -- it is the established idiom and its
test harness already exists:

```go
// Package promptcapture reads an interactive multiline prompt from the
// terminal. It is deliberately NOT part of internal/tui: every file in that
// package is a mirror of tsukumogami/tsuku's copy under a standing sync
// obligation, and this reader is niwa-only.
package promptcapture

// ErrCanceled reports that the developer abandoned the capture (R8). Callers
// treat it as a deliberate user action: no instance, no mapping, non-zero exit.
var ErrCanceled = errors.New("promptcapture: canceled")

// ErrEndOfInput reports end-of-input on an EMPTY buffer (R28). End-of-input on
// a non-empty buffer is a submit and returns (text, nil).
var ErrEndOfInput = errors.New("promptcapture: end of input on an empty buffer")

// Read runs the capture against the real terminal: reads os.Stdin, renders to
// os.Stderr (never os.Stdout, which stays redirectable -- R22). limit is the
// R14 ceiling, enforced as input crosses it while the capture stays open (R17).
func Read(ctx context.Context, limit int) (string, error) {
	return read(ctx, os.Stdin, os.Stderr, limit)
}

// read is the testable core. Tests pass a bytes.Reader and a bytes.Buffer to
// drive it without a terminal; production passes *os.File, which the raw-mode
// setup type-asserts for (the same trick pick uses at picker.go:79).
func read(ctx context.Context, stdin io.Reader, stderr io.Writer, limit int) (string, error)
```

Three notes on the signature. `limit` is a parameter, not a package constant,
because the ceiling is `maxArgStringBytes - dispatchPromptReserve` derived in
package `cli` (R14) and the core has no business re-deriving it. `ctx` is
carried even though decision 5 owns whether the read loop honours it -- adding
it later would rewrite every test stub, and the two neighbouring seams
(`dispatchLaunch`, `dispatchCapture`) already take one. The error is a sentinel
rather than an enum because `tui.ErrCanceled` (`picker.go:30`) already
established that idiom in this codebase, and `errors.Is` gives the three-way
discrimination R8 and R28 need.

### The capture seam

In `internal/cli/dispatch.go`, beside the existing `dispatchCapture` and
`dispatchAttach` vars (`dispatch.go:91-110`):

```go
// dispatchPromptCapture is the interactive-prompt capture seam. Production
// wires it to promptcapture.Read; tests substitute a fake that returns canned
// text, a cancellation, or that fails the test on call (R2, R11).
//
// Named "prompt" to distinguish it from dispatchCapture above, which captures
// the launched worker's SESSION ID -- an unrelated thing in the same command.
var dispatchPromptCapture = promptcapture.Read
```

Signature: `func(context.Context, int) (string, error)`. Direct assignment, no
wrapper closure, so the seam's type is the function's type and a stub that
compiles is a stub that matches.

### The TTY seams

In `internal/cli/prompt.go`, directly below `IsStdinTTY`:

```go
// IsStderrTTY reports whether stderr is connected to a terminal. The dispatch
// capture requires BOTH stdin and stderr to be terminals (R21): it reads the
// former and renders to the latter, while stdout carries the command's session
// hints and stays redirectable (R22).
//
// This duplicates tui.IsAvailable's check by design. That function lives in a
// file under a byte-for-byte sync obligation with tsuku's copy, and it is a
// plain func rather than a var -- turning it into a stubbable var would be a
// divergence from the mirrored copy, and without a stub the four-combination
// gate test cannot be written. Exposed as a var for the same reason IsStdinTTY
// is: tests stub the result without touching real stderr.
var IsStderrTTY = func() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}
```

Two vars, four stubbable combinations, and the gate is written inline in
`dispatch.go` in exactly the shape `destroy.go:239` already uses -- no helper
function, no third name.

Leave `destroy.go:239` alone. No requirement asks for it, and rewriting a tested
gate to reach consistency is churn against R25's spirit.

### The call site

Insert as step (3b), **between `dispatch.go:187` (the close of the claude
preflight) and `dispatch.go:189` (the `(4)` comment)**:

```go
// (3b) With no positional argument on an interactive session, capture the
// prompt from the terminal (R1). This sits AFTER the workspace, agent, and
// claude preflights so a developer never pastes a log only to be told they
// are not in a workspace, and BEFORE reapOpportunistically (line 211) and
// provisionInstanceFunc (line 217) so an abandoned or refused capture leaves
// nothing behind (R7, R26). It is the ONLY call to the capture seam, which is
// what keeps it unreachable from dispatchLaunch and therefore from
// `niwa watch` (R11).
if len(args) == 0 {
	if !IsStdinTTY() || !IsStderrTTY() {
		return fmt.Errorf("niwa: error: dispatch has no prompt and this session is not interactive; pass the prompt as an argument: niwa dispatch \"<task>\"")
	}
	text, capErr := dispatchPromptCapture(cmd.Context(), maxPromptBytes)
	if capErr != nil { /* map ErrCanceled / ErrEndOfInput, both non-zero (R31) */ }
	prompt = text
	// same validation the argument path ran at step (1) (R16, R29)
}
```

This requires three matching edits, none of which are this decision's to make in
detail but all of which follow from the placement:

- `dispatch.go:131` becomes `Args: cobra.MaximumNArgs(1)` (R3), from
  `cobra.ExactArgs(1)`.
- `dispatch.go:138-146` becomes conditional: `var prompt string; if len(args) == 1 { prompt = args[0]; ...existing checks unchanged... }`.
  Keeping the argument-path checks at the top, in their current order and
  wording, is what preserves R25 -- `TestDispatch_EmptyPrompt_Errors`
  (`dispatch_test.go:211`) asserts the empty-prompt error fires before anything
  is provisioned, and it must keep firing before `ClassifyCwd`.
- The capture path re-runs the same validation on the returned text. The ceiling
  is enforced twice by design: inside the core while the capture is open (R17)
  and again by the command (R16), and they read the same constant.

R2 falls out structurally: with one positional argument, control never enters
the `len(args) == 0` branch, so neither TTY var nor the capture seam is
consulted. The test is a stub that calls `t.Fatal` on invocation.

### Tests

`internal/promptcapture/promptcapture_test.go` -- the fifteen reader-core
criteria, driving `read` with `bytes.NewReader` and `bytes.Buffer`. Lift
`chunkedReader` from `picker_test.go` for the "input arriving across multiple
reads, split at an arbitrary boundary" criterion; it exists for exactly that.

`internal/cli/dispatch_prompt_test.go` -- the command-level criteria. Extend
`installDispatchFakes` (`dispatch_test.go:85-163`) with three more save/restore
pairs (`dispatchPromptCapture`, `IsStdinTTY`, `IsStderrTTY`), defaulting both
TTY stubs to `false` so every existing test keeps taking the argument path
unchanged. Add a sibling to `runDispatchCmd` (`dispatch_test.go:168-177`), which
today hard-codes `[]string{prompt}`:

```go
// runDispatchCmdArgs is runDispatchCmd over an arbitrary argv, so zero-argument
// (capture) and two-argument (R3 error) invocations are drivable.
func runDispatchCmdArgs(t *testing.T, args []string) (stdout, stderr string, err error)
```

`internal/cli/dispatch_reachability_test.go` -- the R11 guard. Parse
`internal/cli/dispatch_launcher.go` and `internal/cli/watch.go` with
`go/parser`, walk with `ast.Inspect`, and fail if either names the identifier
`dispatchPromptCapture` or imports a path ending in `/promptcapture`. This is
the Go-native form of "the capture is unreachable from the launcher", and it
fails the moment someone adds the call. Pair it with the direct drive the
criterion names: stub `dispatchPromptCapture` to `t.Fatal` on call, wire
`dispatchLaunch` to a recorder, and invoke it with
`watch.BuildReviewPrompt(...)` and `buildDispatchPassthrough(...)` -- the exact
argument shape `watch.go:826` builds -- asserting the launch lands and the stub
never fires.

## Why the alternatives lose

**(a) `internal/tui` loses on the sync contract, and the cost is not
hypothetical.** `sanitize.go` is already a mirrored file carrying no provenance
header -- the package has *already* lost track of which files are copies.
Adding a niwa-only file to a three-file mirror makes the next sync a judgement
call rather than a diff, and the header's own remedy ("document the divergence
in the commit message") does not survive into the tree where the next person
looks. Exploration round 1 already ruled out putting the reader *in* `picker.go`
for this reason; the same reasoning extends to the package, because the sync
unit a person actually operates on is the directory.

**(b) `internal/cli` loses on the reader-core tests, specifically R37.** The
PRD asks for a timing comparison -- 130,433 bytes on one line against the same
byte count across many lines -- and asserts exact bytes on a render target for
R27 and R30. Package `cli`'s test suite chdirs the process (`dispatch_test.go:62`)
and rebinds thirteen globals per test. Those are not abstract risks: they are
the two things that make a timing assertion unreliable. Nothing else about (b)
is wrong -- and note the *seams* go in `internal/cli` under this recommendation
anyway, so (b) is really "put the core there too", which buys one fewer package
and pays for it with the flakiest test in the feature.

**(c) does not win on R11**, and it is worth being explicit that no placement
does. Go visibility is package-scoped; `watch.go` can call anything package
`cli` or any exported symbol can reach. R11 is bought by the call site (one
call, in `runDispatch`, above the launcher) and enforced by the guard test, not
by the directory. (c) wins on the two things placement actually decides: the
sync contract and the test environment.

## Risks

**The R11 acceptance criterion is only partly satisfiable as written.**
"Driving the launcher path used by `niwa watch`" end to end is not reachable
today: `stageReview` (`watch.go:758`) takes a concrete `*github.APIClient` and
calls `watch.FetchPRHead` (`watch.go:797`) directly, and neither is a seam. No
existing test drives `stageReview` or `continueReview` at all. Fully satisfying
the criterion would mean narrowing the client to an interface (the pattern
`prFreshnessClient` at `watch.go:616` already establishes) and adding a
`fetchPRHeadFunc` seam -- two changes this feature has no other reason to make.
The AST guard plus the direct `dispatchLaunch` drive covers the requirement; the
gap between that and the criterion's wording should be closed in PLAN, either by
adding the two seams or by restating the criterion.

**`promptcapture` importing `internal/tui` for `SanitizeDisplayString` is a trap
if decision 4 needs more.** `tui.SanitizeDisplayString` (`sanitize.go:17`) strips
ANSI/OSC sequences but leaves bare C0 bytes alone -- `\r`, `\b`, `\x07` all pass
through, and all three can corrupt a live capture's display. If R30's
neutralization needs to cover them, the new logic must land in `promptcapture`,
**not** in `tui/sanitize.go`, because that file is a near-verbatim mirror of
tsuku's. Reusing the exported function read-only adds no sync burden; extending
it does.

**Two spellings of "is stderr a terminal" will invite a cleanup.**
`IsStderrTTY` and `tui.IsAvailable` compute the same thing. A future reviewer
routing dispatch through `tui.IsAvailable` for consistency would silently delete
the stub point the four-combination test depends on. The doc comment above
states why; that is the only defense short of a guard test.

**The name `dispatchPromptCapture` sits eight lines from `dispatchCapture`,
which means something else entirely.** The seam's doc comment calls this out
explicitly, and `dispatch.go:349` already carries a long comment about the
session-id capture, but a reader skimming var names will trip. The alternative
-- renaming `dispatchCapture` to `dispatchSessionCapture` -- touches five test
files for no requirement, so it is not recommended here, but it is the cleaner
end state if the churn is ever acceptable.

**Restructuring step (1) is the one place R25 can break.** The argument-path
checks move inside an `if len(args) == 1` block. If the ceiling check or the
empty check drifts below `ClassifyCwd` while doing so, the positional path's
error ordering changes and the R25 golden comparison catches it late. Keeping
the block textually where it is, and gating rather than moving it, is the
mitigation.

## Summary

The capture core goes in a new `internal/promptcapture` package that mirrors
`picker.go`'s `Pick`/`pick` split -- an exported `Read(ctx, limit)` over
`os.Stdin`/`os.Stderr` and an unexported `read(ctx, io.Reader, io.Writer, limit)`
core -- keeping `internal/tui` a clean three-file mirror of tsuku's copy and
keeping R37's timing assertion out of package `cli`, whose tests chdir the
process and rebind thirteen globals. The command seams stay in `internal/cli`:
`var dispatchPromptCapture = promptcapture.Read` beside the existing dispatch
seams, and a new `IsStderrTTY` var beside `IsStdinTTY` in `prompt.go`, since
`tui.IsAvailable` is a plain func in a mirrored file and cannot become stubbable
without diverging from tsuku. The single call site is a new step (3b) inserted
between `dispatch.go:187` and `:189` -- after the workspace, agent, and claude
preflights, before `reapOpportunistically` at `:211` and `provisionInstanceFunc`
at `:217` -- which buys R11 structurally, enforced by an AST guard test over
`dispatch_launcher.go` and `watch.go`.
