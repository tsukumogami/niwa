<!-- decision:start id="clone-output-ux-reporter-interface" status="assumed" -->
### Decision: Reporter Interface and Progress Display Mechanism

**Context**

Niwa's `create` and `apply` commands write output through 29 scattered
`fmt.Fprintf(os.Stderr, ...)` calls across `internal/workspace/apply.go` (17
sites), `internal/cli/apply.go`, `internal/cli/create.go`, and supporting
packages. The target UX is the cargo two-layer model: completed events scroll
as normal lines, a single status line at the bottom rewrites in place via
`\r + \033[K`. The apply loop in `runPipeline` is fully sequential — one repo
at a time — so no concurrent multi-bar display is needed.

Several functions already accept `io.Writer` parameters (`emitRotatedFiles`,
`checkRequiredKeys`, `guardrail.CheckGitHubPublicRemoteSecrets`) and pass
`os.Stderr` at call sites. The abstraction is partially in place; this decision
formalizes the interface and selects the display mechanism.

Three library candidates were evaluated: no-library manual ANSI (~20 lines of
Go), `schollz/progressbar` v3 (sequential-optimized, 1 new direct dep + 4
indirect), and `mpb` v8 (concurrent-capable container model, 1 new direct dep).
`bubbletea`, `pterm`, and `uiprogress` were ruled out before this decision.

**Assumptions**

- The apply loop remains sequential (no goroutines in `apply.go`). A single
  status line is sufficient for the whole pipeline.
- Overlay sync output stays suppressed via `cmd.Run()` in `overlaysync.go`
  regardless of Reporter choice; that file is not touched.
- Non-TTY detection uses the standard `isatty` check on the output file
  descriptor (no new external dependency needed — `os.File.Fd()` + a syscall
  wrapper or `golang.org/x/term.IsTerminal` via `os.Stderr`). The check is
  encapsulated in the Reporter constructor.
- Status line updates fire before blocking git operations (e.g., before
  `CloneWithBranch`) so users see "cloning foo..." during the operation.
  Decision 2 (goroutine-based git stderr capture) is independent and can wire
  into the same Reporter after it lands.

**Chosen: No-library manual ANSI**

A small `Reporter` type is added to `internal/workspace/` (or
`internal/output/`) with the following interface:

```go
type Reporter struct {
    w          io.Writer
    isTTY      bool
    needsClear bool
}

func NewReporter(w io.Writer) *Reporter  // detects TTY from w if it is *os.File

func (r *Reporter) Status(msg string)            // \r\033[K<msg> on TTY; no-op on non-TTY
func (r *Reporter) Log(format string, a ...any)  // clears status if needed, prints line
func (r *Reporter) Warn(format string, a ...any) // same as Log with "warning: " prefix
func (r *Reporter) Writer() io.Writer            // adapter for io.Writer call sites
```

The `Applier` struct gains a `Reporter *Reporter` field, defaulting to
`NewReporter(os.Stderr)` in `NewApplier`. Tests inject a reporter wrapping
`bytes.Buffer` with `isTTY=false`. Functions already accepting `io.Writer`
receive `reporter.Writer()` instead of `os.Stderr`.

The status line lifecycle for each repo:
1. `reporter.Status("cloning " + repo)` — before `CloneWithBranch`
2. On clone completion: `reporter.Log("cloned %s", repo)` — clears status,
   prints completion line
3. Warnings during sync: `reporter.Warn("could not fetch %s: ...", repo)` —
   clears status, prints warning, does not redraw status

The `needsClear` flag tracks whether `\r\033[K` must precede the next output.
`Log` and `Warn` both clear it before printing. `Status` sets it after writing.
On non-TTY, all three methods behave identically: append-only writes to `w`
with no ANSI sequences, preserving today's behavior exactly.

**Rationale**

The manual approach fits the use case precisely because the apply loop is
sequential — there is no concurrent state to manage, no ticker goroutine, and no
bar lifecycle to finalize. The ~20 lines of implementation give complete control
over TTY codes with no risk of a library's defaults (progress-bar aesthetics,
color schemes, spinner frames) leaking into niwa's output. Dependency hygiene is
a first-class constraint: niwa currently has 3 direct dependencies, and adding
any terminal library doubles that footprint for functionality that fits in a
single small file. Both `schollz/progressbar` and `mpb` deliver more than is
needed and impose their own design conventions on a problem that is simple here.

**Alternatives Considered**

- **schollz/progressbar v3**: single-bar API, TTY detection built-in, 4
  indirect deps added. Rejected because the bar is designed to show percentage
  and ETA by default; using it as a plain text status line requires overriding
  most of its options, and `bar.Clear()` must still be called manually before
  every warning — no simpler than the manual approach but with the dep cost.

- **mpb v8**: concurrent multi-bar container, `p.Write()` for interleaved
  output. Rejected because the container model (p + bar + p.Wait()) adds
  lifecycle complexity for a single sequential bar; its main advantage — safe
  concurrent updates across multiple bars — is irrelevant to niwa's sequential
  apply loop.

**Consequences**

- `internal/workspace/reporter.go` (or `internal/output/reporter.go`) is a new
  ~50-line file: `Reporter` struct, constructor, `Status`/`Log`/`Warn`/`Writer`
  methods.
- `Applier` gains one field: `Reporter *Reporter` (or an interface if tests
  need a wider seam). `NewApplier` wires the default.
- All 17 `fmt.Fprintf(os.Stderr, ...)` sites in `apply.go` are migrated to
  `a.Reporter.Log(...)` or `a.Reporter.Warn(...)`. Sites in `cli/apply.go` and
  `cli/create.go` continue using `fmt.Fprintf(os.Stderr, ...)` or
  `cmd.ErrOrStderr()` — they are above the Applier layer and do not need the
  Reporter.
- `emitRotatedFiles`, `checkRequiredKeys`, and `guardrail.CheckGitHubPublicRemoteSecrets`
  receive `reporter.Writer()` instead of `os.Stderr`.
- Non-TTY output is identical to today. Tests validate both paths by constructing
  reporters with a `bytes.Buffer` and `isTTY=false`/`true`.
- When Decision 2 (goroutine git stderr capture) lands, captured git error lines
  route through `reporter.Warn(...)` at the same call site that currently handles
  the post-clone result.
<!-- decision:end -->
