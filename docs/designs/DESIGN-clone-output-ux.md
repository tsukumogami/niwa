---
status: Planned
problem: |
  Niwa's create and apply commands dump a linear log of git subprocess output
  and status messages to stderr with no TTY awareness. Users see git progress
  chatter, per-repo status lines, and warnings all interleaved — no visual
  indication of what's currently happening. Git subprocess stderr is piped
  directly to os.Stderr in four files, making any inline progress display
  incompatible without an explicit capture layer, and 29 niwa-authored output
  sites are scattered across the workspace package with no shared abstraction.
decision: |
  Introduce a Reporter struct (~50 lines, zero new deps beyond golang.org/x/term)
  with Status/Log/Warn methods and a needsClear flag. On TTY, Status rewrites a
  single bottom line in place via \r\033[K; Log and Warn clear it before printing.
  Git subprocess stderr is captured via io.Pipe + goroutine + bufio.Scanner, which
  classifies lines by prefix and routes them through Reporter while naturally
  discarding \r-terminated git progress frames. TTY mode activates only when
  term.IsTerminal returns true and --no-progress is absent.
rationale: |
  The sequential apply loop needs only a single status line, making the manual
  ANSI approach simpler and cheaper than any terminal library. Capturing git
  stderr via goroutine pipe is the only approach that simultaneously provides
  inline progress display and preserves git's diagnostic text in returned errors.
  Layering --no-progress over isatty gives CI users an explicit, portable escape
  hatch without the false-positive risk of CI env var detection.
---

# DESIGN: Clone/Apply Output UX

## Status

Planned

## Context and Problem Statement

Niwa's `create` and `apply` commands clone and sync many repos, producing a
linear log dump on stderr. The output mixes raw git progress lines (carriage-
return-overwritten, like "remote: Enumerating objects..."), niwa's own status
messages ("cloned tools/myapp"), and warnings ("warning: could not fetch..."),
all written by different code paths with no coordination.

There are approximately 35 output sites total: 29 niwa-authored `fmt.Fprintf`
calls (17 concentrated in `internal/workspace/apply.go`) and 6 git subprocess
pipes in `clone.go`, `sync.go`, `configsync.go`, and `setup.go` that use
`cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr`. The apply loop is
sequential — no goroutines — which simplifies the display model.

The target UX is the cargo two-layer model: completed-repo events scroll as
normal lines (e.g., "cloned tools/myapp") while a single carriage-return-
rewritten status line at the bottom shows the current operation
("cloning tools/tsuku..."). On non-TTY terminals (CI, pipes, scripts), the
status line is suppressed and the behavior is identical to today. Warnings
and errors interrupt the status line using the suspend-clear-print-redraw
pattern before resuming.

The key architectural challenges are:
1. Git subprocess stderr must be captured via a goroutine pipe rather than
   piped directly to `os.Stderr`. The goroutine filters `\r`-terminated
   progress frames and forwards `\n`-terminated error lines.
2. A thin Reporter abstraction must route niwa's 29 output sites through a
   common path so the TTY implementation can clear the status line before
   printing, and the non-TTY implementation passes through unchanged.
3. TTY detection must be layered: `isatty()` on stderr + `NO_COLOR`
   (suppress colors) + `CI` env var (soft hint) + `--no-progress` flag
   (explicit suppression for scripts and automation).

The overlay sync path (`overlaysync.go`) already suppresses all output —
this must not change (privacy requirement R22).

## Decision Drivers

- **Error/warning visibility must not regress.** Warnings and errors must
  always appear clearly, even when a status line is active. The
  suspend-clear-print-redraw pattern (cargo-style `needs_clear` flag) is
  the established solution for niwa's own output sites. Git subprocess errors
  must surface via the goroutine pipe, not be silently swallowed.

- **Non-TTY behavior must be identical to today.** CI runners, piped output,
  and scripts must see exactly what they see now: clean append-only lines.
  TTY detection prevents ANSI sequences from appearing in non-interactive
  contexts.

- **Overlay sync suppression is a hard constraint.** The overlay repo name
  must not appear in standard output. `overlaysync.go` must remain silent.

- **Dependency footprint matters.** Niwa is a small CLI with minimal deps
  (`go.mod` currently has cobra, godog, and toml). Any added library should
  earn its place.

- **Full TUI approach is not feasible.** bubbletea requires routing all output
  through an Elm-style event loop, which is incompatible with the scattered
  `fmt.Fprintf` calls and subprocess pipes. The cargo model does not require
  full terminal ownership.

- **Machine-readable consumers need progress suppression, not structured
  output.** No concrete consumer of structured JSON output exists. A
  `--no-progress` flag (or equivalent) is sufficient.

## Decisions Already Made

The following decisions were made during the exploration phase and should be
treated as constraints by this design, not reopened without strong reason.

- **Docker-style per-row multi-line display eliminated.** The apply loop is
  sequential (no goroutines in `apply.go`). Per-row cursor repositioning
  adds complexity without value for a sequential workflow.

- **Cargo two-layer model selected as target pattern.** Single status line at
  bottom using `\r` + erase-to-EOL, with scrolling log for completed events.
  This is the industry-standard pattern for sequential multi-step CLIs.

- **bubbletea ruled out.** Requires near-complete architectural rewrite due to
  incompatibility with scattered `fmt.Fprintf` calls and subprocess pipes.

- **Git subprocess output approach: capture via goroutine.** Each `git clone`,
  `git fetch`, `git pull` subprocess should have its stderr captured via a
  goroutine reader that filters `\r`-terminated progress frames and forwards
  `\n`-terminated error lines through the Reporter.

- **Machine-readable flag: `--no-progress` preferred over `--json`.** No
  structured-output consumer identified. Progress suppression is sufficient.

- **Overlay sync suppression must not change.** The `overlaysync.go` path
  stays silent — privacy requirement R22 is a hard constraint.

## Considered Options

### Decision 1: Reporter interface and progress display mechanism

Niwa's `create` and `apply` commands write output through 29 scattered
`fmt.Fprintf(os.Stderr, ...)` calls. The target UX is the cargo two-layer
model: completed events scroll as normal lines, a single status line at the
bottom rewrites in place via `\r + \033[K`. The apply loop is fully sequential,
so no concurrent multi-bar display is needed. Three display mechanisms were
evaluated.

**Key assumptions:** apply loop remains sequential; overlay sync stays suppressed
independently of Reporter; `golang.org/x/term` provides TTY detection.

#### Chosen: No-library manual ANSI

A small `Reporter` type in `internal/workspace/` (or `internal/output/`) with:

```go
type Reporter struct {
    w          io.Writer
    isTTY      bool
    needsClear bool
}

func NewReporter(w io.Writer) *Reporter  // detects TTY from w if *os.File
func (r *Reporter) Status(msg string)            // \r\033[K<msg> on TTY; no-op on non-TTY
func (r *Reporter) Log(format string, a ...any)  // clears status if set, prints line
func (r *Reporter) Warn(format string, a ...any) // same as Log, prefixes "warning: "
func (r *Reporter) Writer() io.Writer            // adapter for io.Writer call sites
```

The `Applier` struct gains a `Reporter *Reporter` field, defaulting to
`NewReporter(os.Stderr)` in `NewApplier`. The `needsClear` flag tracks whether
`\r\033[K` must precede the next output. `Log` and `Warn` both clear it before
printing; `Status` sets it after writing. On non-TTY, all three methods behave
identically: append-only writes with no ANSI sequences.

Status line lifecycle per repo:
1. `reporter.Status("cloning " + repo)` — before `CloneWithBranch`
2. On clone completion: `reporter.Log("cloned %s", repo)` — clears status, prints line
3. Warnings: `reporter.Warn("could not fetch %s: ...", repo)` — clears status, prints warning

#### Alternatives Considered

- **schollz/progressbar v3**: single-bar API, TTY detection built-in, 4 indirect
  deps added. Rejected: designed for percentage/ETA bars; using it as a plain
  status line requires overriding most defaults, and `bar.Clear()` must still be
  called manually before every warning — equivalent complexity to the manual
  approach but with the dependency cost.

- **mpb v8**: concurrent multi-bar container, `p.Write()` for interleaved output,
  lean deps. Rejected: the container model (p + bar + p.Wait()) adds lifecycle
  complexity for a single sequential bar; its core advantage — safe concurrent
  updates across multiple bars — is irrelevant to niwa's sequential apply loop.

---

### Decision 2: Git subprocess stderr capture mechanism

Niwa runs git subprocesses with `cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr`,
which bypasses the Reporter and prevents inline progress display. `cmd.Run()`
returns `*exec.ExitError` whose `Error()` string is always "exit status N" — git's
diagnostic text ("fatal: repository not found") lives only in the stderr stream, so
any approach that suppresses stderr without capturing error lines silently drops
actionable information.

`overlaysync.go` deliberately suppresses all git output (overlay repo name must not
leak) and must remain unchanged.

**Key assumptions:** Reporter provides Log/Warn methods; apply loop callers can pass
Reporter into Cloner, FetchRepo, PullRepo, SyncConfigDir, and RunSetupScripts;
`bufio.Scanner` with `\n` splitting naturally elides `\r`-terminated git progress
frames.

#### Chosen: Goroutine pipe with line-by-line classification

Replace `cmd.Stderr = os.Stderr` with an `io.Pipe()`. A goroutine reads with
`bufio.Scanner` (default `\n` splitting), classifies each line by prefix
("fatal:", "error:", "warning:"), and routes through Reporter. Error-classified
lines are accumulated and embedded in the error returned on `cmd.Run()` failure.

A shared helper encapsulates the pattern:

```go
func runGitWithReporter(ctx context.Context, r *Reporter, cmd *exec.Cmd) error {
    pr, pw := io.Pipe()
    cmd.Stdout = pw
    cmd.Stderr = pw

    var errLines strings.Builder
    done := make(chan struct{})
    go func() {
        defer close(done)
        scanner := bufio.NewScanner(pr)
        for scanner.Scan() {
            line := scanner.Text()
            if isGitErrorLine(line) {
                r.Warn(line)
                errLines.WriteString(line + "\n")
            } else {
                r.Log(line)
            }
        }
    }()

    runErr := cmd.Run()
    pw.Close()
    <-done

    if runErr != nil {
        if detail := strings.TrimSpace(errLines.String()); detail != "" {
            return fmt.Errorf("%s", detail)
        }
        return runErr
    }
    return nil
}
```

`isGitErrorLine` checks for "fatal:", "error:", or "warning:" prefixes that git
uses consistently. The Scanner's `\n`-based splitting naturally discards
`\r`-terminated progress frames — they are overwritten in the kernel's pty buffer
before being flushed as complete lines.

#### Alternatives Considered

- **Full suppression** (overlaysync.go pattern): Set `cmd.Stdout/Stderr = nil`.
  Rejected: `cmd.Run()` returns only "exit status N", not git's actual error
  message. Silently swallows "fatal: repository not found" — a hard constraint
  violation.

- **Buffer-and-replay on error**: Capture all git output in `bytes.Buffer`; discard
  on success, replay on failure. Rejected: no inline progress display during the
  operation (contradicts the design goal), and the replayed buffer on failure
  contains raw `\r`-terminated frame noise that renders incorrectly through Reporter.

---

### Decision 3: TTY detection and output mode strategy

Niwa currently has no TTY awareness, no NO_COLOR handling, and no `--no-progress`
flag. The detection result must be computed once and propagated to the Reporter.

**Key assumptions:** `golang.org/x/term` added as a direct dependency;
`--no-progress` suppresses only the status line, not completion lines; detection
runs in `PersistentPreRunE` and is passed down.

#### Chosen: isatty + NO_COLOR + --no-progress flag

Detection in `PersistentPreRunE` (root.go), evaluated in priority order:

```
1. --no-progress flag set      → non-TTY mode (explicit, highest priority)
2. term.IsTerminal(stderr)     → TTY mode if true, non-TTY if false
3. NO_COLOR env var set        → color off (does not affect progress gate)
```

The `--no-progress` flag is registered on `rootCmd.PersistentFlags()` so all
subcommands inherit it. The computed `DisplayMode` is passed to `NewReporter`.

In non-TTY mode: no status line, no ANSI sequences, completion lines still print
(append-only). In TTY mode: status line active, ANSI for status line only.

`golang.org/x/term` (official Go sub-repo) is added as a direct dependency.
`TERM=dumb` is handled implicitly: `term.IsTerminal` returns false for dumb
terminals, so no special case is needed. CI env vars are not checked — the
explicit `--no-progress` flag is the correct opt-out for CI pipelines, avoiding
false positives from `CI_TOKEN`-style variables.

#### Alternatives Considered

- **isatty-only**: No explicit escape hatch for scripts; fails in PTY-in-CI;
  ignores `NO_COLOR` convention. Rejected.

- **isatty + specific CI vars + --no-progress**: Incomplete CI coverage, false-
  positive risk from `act` and local env var leakage, growing maintenance burden.
  Rejected.

- **--quiet flag** (cargo-style): Suppresses completion lines too, losing per-repo
  output in CI logs. Conflates animation suppression with silent mode. Harder to
  extend. Rejected.

## Decision Outcome

The three decisions compose into a coherent architecture:

1. A `Reporter` struct in `internal/workspace/` (or `internal/output/`) provides
   `Status`, `Log`, and `Warn` methods. It detects TTY mode at construction time
   via `golang.org/x/term.IsTerminal`. On TTY, `Status` uses `\r\033[K` to
   rewrite a single status line in place; `Log` and `Warn` clear the status line
   before printing. On non-TTY, all three methods produce append-only output
   identical to today.

2. The `Applier` struct holds a `*Reporter`. Every git subprocess call site
   (`clone.go`, `sync.go`, `configsync.go`, `setup.go`) uses a shared
   `runGitWithReporter` helper that pipes subprocess stderr through a goroutine,
   classifies lines by prefix, and routes errors through `reporter.Warn`. Git
   error messages are embedded in the returned error, not silently swallowed.
   `overlaysync.go` is unchanged.

3. TTY detection runs once in `PersistentPreRunE`. The `--no-progress` flag
   (registered on `rootCmd`) suppresses the status line without affecting
   completion lines, providing a script-safe opt-out. `NO_COLOR` suppresses
   color-only output.

The result: on a TTY, users see a single updating status line ("cloning
tools/myapp...") that is replaced by completion lines ("cloned tools/myapp") as
each repo finishes. Warnings interrupt the status line cleanly and remain visible
in scroll history. On non-TTY, the output is identical to today.

## Solution Architecture

### Overview

A thin `Reporter` type in `internal/workspace/reporter.go` routes all output
through a single path. On TTY terminals it maintains a single updating status
line using `\r\033[K` rewrites; on non-TTY it produces append-only output
identical to today. The `Applier` struct holds a `*Reporter`. Git subprocess
calls are updated to pipe stderr through a goroutine reader that classifies
lines and routes them through the Reporter. TTY detection runs once at CLI
startup and is passed into the Reporter constructor.

### Components

```
internal/cli/root.go           -- adds --no-progress persistent flag (noProgress
                                  package-level bool, same pattern as applyNoPull);
                                  PersistentPreRunE captures noProgress + NO_COLOR
                                  into noColor bool; runApply and runCreate read
                                  these vars to construct the Reporter

internal/workspace/reporter.go -- Reporter struct: Status/Log/Warn/Writer methods
                                  needsClear flag; TTY-gated ANSI; non-TTY fallback

internal/workspace/apply.go    -- Applier.Reporter field; migrated output sites
                                  (17 fmt.Fprintf → reporter.Log/Warn calls);
                                  SyncConfigDir call moved here from cli/apply.go

internal/workspace/gitutil.go  -- runGitWithReporter: io.Pipe + goroutine + Scanner
(new file)                        + isGitErrorLine classifier (git subprocesses only)
                                  runCmdWithReporter: same pipe pattern, all lines
                                  via reporter.Log, no classifier (setup scripts)

internal/workspace/clone.go    -- CloneWithBranch accepts *Reporter; uses
                                  runGitWithReporter instead of cmd.Stderr=os.Stderr

internal/workspace/sync.go     -- FetchRepo, PullRepo accept *Reporter; same

internal/workspace/configsync.go -- SyncConfigDir accepts *Reporter; called from
                                  Applier.Apply (moved from cli/apply.go)

internal/workspace/setup.go    -- RunSetupScripts accepts *Reporter; uses
                                  runCmdWithReporter (all lines → reporter.Log;
                                  no git-specific classifier for arbitrary scripts)

internal/workspace/overlaysync.go -- unchanged (R22 privacy requirement)
```

### Key Interfaces

**Reporter type** (`internal/workspace/reporter.go`):

```go
type Reporter struct {
    w          io.Writer
    isTTY      bool
    needsClear bool
}

// NewReporter detects TTY from w if it is *os.File; isTTY=false otherwise.
// Pass isTTY=false explicitly for tests: NewReporterWithTTY(w, false).
func NewReporter(w io.Writer) *Reporter
func NewReporterWithTTY(w io.Writer, isTTY bool) *Reporter

func (r *Reporter) Status(msg string)            // TTY: \r\033[K + msg (no newline)
                                                 // non-TTY: no-op
func (r *Reporter) Log(format string, a ...any)  // clears status if set; appends line
func (r *Reporter) Warn(format string, a ...any) // same as Log, prefixes "warning: "
func (r *Reporter) Writer() io.Writer            // for call sites needing io.Writer
```

**runGitWithReporter helper** (`internal/workspace/gitutil.go`):

```go
// runGitWithReporter replaces cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr
// at git subprocess call sites. Classifies lines by git error prefix; routes
// fatal/error/warning lines through r.Warn, others through r.Log.
// Returns an error embedding git's diagnostic text on failure.
func runGitWithReporter(r *Reporter, cmd *exec.Cmd) error

// runCmdWithReporter is the general-purpose variant for non-git subprocesses
// (e.g., setup scripts). No line classification — all output through r.Log.
func runCmdWithReporter(r *Reporter, cmd *exec.Cmd) error
```

**Applier struct change** (`internal/workspace/apply.go`):

```go
type Applier struct {
    // ... existing fields ...
    Reporter *Reporter  // new; default: NewReporter(os.Stderr) in NewApplier
}
```

**DisplayMode propagation** (package-level vars in `internal/cli/root.go`):

The `--no-progress` flag follows the same package-level variable pattern as
`applyNoPull`, `applyAllowDirty`, and other command flags. Two vars are added
to `root.go`:

```go
var (
    noProgress bool  // --no-progress persistent flag
    noColor    bool  // derived from NO_COLOR env var in PersistentPreRunE
)
```

`PersistentPreRunE` sets `noColor = os.Getenv("NO_COLOR") != ""` after the
existing `captureNiwaResponseFile()` call. `runApply` and `runCreate` read
these vars when constructing the Reporter:

```go
reporter := workspace.NewReporterWithTTY(os.Stderr,
    !noProgress && term.IsTerminal(int(os.Stderr.Fd())))
```

`NO_COLOR` disables color output independently — the Reporter's `isTTY` flag
controls the progress gate; a separate `noColor` field (or stripping color
codes from `Status` output when `noColor` is true) handles the color path.

### Data Flow

```
niwa apply myws
  │
  ├─ root.go PersistentPreRunE
  │    noColor = os.Getenv("NO_COLOR") != ""   (package-level var)
  │    noProgress already captured by cobra from --no-progress flag
  │
  ├─ cli/apply.go runApply
  │    reporter := workspace.NewReporterWithTTY(os.Stderr,
  │        !noProgress && term.IsTerminal(int(os.Stderr.Fd())))
  │    applier.Reporter = reporter
  │
  └─ applier.Apply(ctx, cfg, configDir, instanceRoot)
       │
       ├─ reporter.Status("syncing config...")
       ├─ SyncConfigDir(configDir, reporter, ...)  ← moved here from cli/apply.go
       │    → runGitWithReporter(reporter, git-pull-cmd)
       │       git pull → io.Pipe → goroutine → bufio.Scanner
       │       "fatal: ..." → reporter.Warn(line)
       │       other lines  → reporter.Log(line)
       │
       └─ for each repo:
            reporter.Status("cloning tools/myapp...")
            clone.CloneWithBranch(ctx, reporter, ...)
              → runGitWithReporter(reporter, git-clone-cmd)
            reporter.Log("cloned tools/myapp")
            -- OR --
            reporter.Warn("could not fetch tools/myapp: fatal: ...")
            ...
            RunSetupScripts(repoDir, setupDir, reporter)
              → runCmdWithReporter(reporter, script-cmd)
                 all output → reporter.Log(line)

overlaysync.go: exec.Command(...).Run() — no Reporter, no output (unchanged)
```

## Implementation Approach

### Phase 1: Reporter type and apply.go migration

Build the `Reporter` struct in `internal/workspace/reporter.go`. Add
`NewReporter` and `NewReporterWithTTY` constructors. Implement `Status`, `Log`,
`Warn`, and `Writer` methods with the `needsClear` flag logic.

Migrate all 17 `fmt.Fprintf(os.Stderr, ...)` sites in `apply.go` to
`a.Reporter.Log(...)` or `a.Reporter.Warn(...)`. Wire `reporter.Writer()` to the
existing injectable `io.Writer` parameters in `emitRotatedFiles`,
`checkRequiredKeys`, `guardrail.CheckGitHubPublicRemoteSecrets`,
`EnvMaterializer.Stderr`, and `FilesMaterializer.Stderr`.

Deliverables:
- `internal/workspace/reporter.go` (new, ~60 lines)
- `internal/workspace/reporter_test.go` (new, tests both TTY and non-TTY paths)
- `internal/workspace/apply.go` (17 output site migrations, Reporter field on Applier)

### Phase 2: TTY detection and --no-progress flag

Add `golang.org/x/term` as a direct dependency. In `internal/cli/root.go`:

- Register `--no-progress` on `rootCmd.PersistentFlags()` into a `noProgress
  bool` package-level var (same pattern as `applyNoPull`, `applyAllowDirty`).
- In `PersistentPreRunE`, after `captureNiwaResponseFile()`, set a `noColor bool`
  package-level var from `os.Getenv("NO_COLOR") != ""`.

In `runApply` (and `runCreate`), read `noProgress` and call `term.IsTerminal` to
build the Reporter:

```go
reporter := workspace.NewReporterWithTTY(os.Stderr,
    !noProgress && term.IsTerminal(int(os.Stderr.Fd())))
```

Assign `reporter` to `applier.Reporter`. This follows the existing var pattern
and avoids any mechanism change to `PersistentPreRunE`'s return value.

Deliverables:
- `internal/cli/root.go` (noProgress + noColor vars, --no-progress persistent flag)
- `internal/cli/apply.go` and `internal/cli/create.go` (Reporter construction)
- `go.mod` / `go.sum` updated with `golang.org/x/term`

### Phase 3: Status line call sites

Add `reporter.Status(...)` calls in `apply.go`'s repo loop: before
`CloneWithBranch` ("cloning tools/myapp..."), before fetch/pull ("syncing
tools/myapp..."). After each operation completes (success or warning), the
corresponding `reporter.Log` or `reporter.Warn` call clears the status line.

Deliverables:
- `internal/workspace/apply.go` (status call sites in runPipeline repo loop)

### Phase 4: Subprocess stderr capture

Add `internal/workspace/gitutil.go` with two helpers:

- `runGitWithReporter`: for git subprocesses. Uses `isGitErrorLine` to
  classify lines by "fatal:", "error:", "warning:" prefixes; routes
  error-classified lines through `r.Warn`, others through `r.Log`.
- `runCmdWithReporter`: for non-git subprocesses (setup scripts). No
  classifier — all lines route through `r.Log`.

Update `clone.go`, `sync.go`, and `configsync.go` to accept `*Reporter` and
use `runGitWithReporter`. Update `setup.go` to accept `*Reporter` and use
`runCmdWithReporter`.

Move the `SyncConfigDir` call from `cli/apply.go` into `Applier.Apply`. The
`configDir` parameter is already passed to `Apply`; the `AllowDirty` field on
Applier replaces the `applyAllowDirty` var that `cli/apply.go` currently passes.

`overlaysync.go` is not touched.

Update functional tests: the critical-path feature scenarios should still pass
since non-TTY Reporter output matches today's format exactly.

Deliverables:
- `internal/workspace/gitutil.go` (new: runGitWithReporter, runCmdWithReporter, isGitErrorLine)
- `internal/workspace/clone.go`, `sync.go`, `configsync.go` (Reporter parameter, runGitWithReporter)
- `internal/workspace/setup.go` (Reporter parameter, runCmdWithReporter)
- `internal/workspace/apply.go` (SyncConfigDir call moved here; reporter passed to all subprocess calls)
- `internal/cli/apply.go` (SyncConfigDir call removed)

## Security Considerations

**Terminal escape injection.** Git relays server-provided text verbatim on stderr. A malicious server can embed ANSI or OSC escape sequences in error messages. When `runGitWithReporter` routes these lines to a terminal via `reporter.Warn()` or `reporter.Log()`, the sequences are executed by the terminal emulator. Mitigate by stripping non-printable characters and escape sequences from each scanned line before passing it to the reporter. A single `regexp.MustCompile` at package init (matching `\x1b\[[0-9;]*[A-Za-z]` for CSI and `\x1b\][^\x07]*\x07` for OSC) applied to every line is sufficient and adds no new dependency. Strip unconditionally — not just on TTY paths — to keep log output clean as well.

**Goroutine lifecycle.** The `io.Pipe` + goroutine pattern requires two defensive additions beyond the pseudocode in Decision 2:

1. `defer pw.Close()` immediately after `io.Pipe()` — ensures the write end closes even if `cmd.Run()` panics or an early-return path is added later.
2. `pr.Close()` inside the goroutine after the scanner loop exits — prevents the git process from blocking on a write when the scanner exits early (e.g., due to a token-too-long error at the 64KB default limit), which would otherwise cause `cmd.Run()` to hang indefinitely.

Neither fix changes observable behavior on the normal completion path.

**Supply chain.** `golang.org/x/term` is the only new dependency introduced by this design. Pin it in `go.sum` and review the diff on any future upgrade.

**Overlay privacy (R22).** `overlaysync.go` retains full output suppression. No overlay repo name or URL passes through the Reporter path.

## Consequences

### Positive

- Users see what is currently happening during `create` and `apply` on interactive
  terminals without scrolling through git progress noise.
- Warning and error messages remain visible in scroll history — they interrupt the
  status line rather than being overwritten by it.
- Git error messages are richer: `runGitWithReporter` embeds git's diagnostic text
  in returned errors rather than "exit status N".
- Non-TTY output is identical to today — no risk of breaking CI pipelines or log
  parsers.
- Zero new runtime dependencies beyond `golang.org/x/term` (official Go sub-repo).

### Negative

- `clone.go`, `sync.go`, `configsync.go`, and `setup.go` gain a `*Reporter`
  parameter, requiring call-site updates in `apply.go`.
- All 17 `fmt.Fprintf(os.Stderr, ...)` sites in `apply.go` must be migrated to
  Reporter method calls.
- The goroutine pipe pattern in `runGitWithReporter` adds non-trivial complexity
  to subprocess execution; goroutine lifecycle must be managed correctly to avoid
  leaks on error paths.

### Mitigations

- The goroutine pattern is extracted into a single shared helper, not duplicated
  at each call site.
- Existing tests for `apply.go` output use `os.Stderr` directly; they will need
  to be updated to inject a `bytes.Buffer`-backed Reporter.
- The `needsClear` flag and status line logic are confined to the ~50-line
  Reporter type — easy to audit and test in isolation.
