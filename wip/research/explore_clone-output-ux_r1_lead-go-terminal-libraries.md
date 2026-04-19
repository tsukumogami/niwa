# Lead: Go terminal UI libraries

## Findings

### bubbletea + bubbles (charmbracelet)

**Programming model:** Elm Architecture — the application defines a `Model` type
plus three methods (`Init`, `Update`, `View`). A central `Program` struct owns
the event loop, which processes messages serially in one goroutine. Side effects
happen inside `Cmd` functions that each run in their own goroutine and return
messages back through a single channel.

**Concurrency:** Multiple goroutines can produce output only by posting messages
through the event loop. The `p.Send()` method provides thread-safe injection
from external goroutines. Raw `fmt.Fprintf` calls alongside the running program
are explicitly incompatible: bubbletea takes exclusive ownership of the terminal
via raw mode and ANSI cursor control. Concurrent direct writes bypass the
rendering pipeline and produce visible corruption.

**TTY / non-TTY:** bubbletea detects non-TTY environments and degrades (no raw
mode, no cursor tricks), but the Elm model still holds. Output in CI is plain
text from the `View` function.

**Dependency footprint:** The charmbracelet ecosystem pulls in several related
packages (lipgloss, harmonica, etc.); `bubbles` adds more. Not the lightest
option.

**Mixing with normal output:** Not possible with scattered `fmt.Fprintf` calls.
All output must be routed through `View` or printed above the UI via the
`Program.Println` method, which requires the caller to have a reference to the
running `Program`.

**Maintenance:** Very active. bubbletea is the de facto standard for Go TUI apps
as of 2025–2026; frequent releases, large community.

**Verdict for niwa:** Requires a full architectural rewrite. Every output site
must become a message sent to the event loop. The git subprocess stderr pipes
(`cmd.Stderr = os.Stderr`) in `clone.go`, `sync.go`, `configsync.go`, and
`setup.go` would need capture-and-relay wrappers. Not feasible as a thin
abstraction.

---

### mpb (vbauerster/mpb)

**Programming model:** Container-based. `mpb.New()` creates a `Progress`
container with an internal goroutine that owns the redraw cycle. Bars are added
via `p.AddBar(total)` and incremented from any goroutine. Call `p.Wait()` when
all work is done.

**Concurrency:** Designed for concurrent bars. Each bar tracks an independent
counter; the container goroutine merges and redraws at a configurable refresh
interval. This is mpb's primary use case.

**TTY / non-TTY:** Handled via `WithOutput(w io.Writer)`. When the writer is not
a terminal (`cw.IsTerminal()` returns false), auto-refresh is disabled
automatically unless `WithAutoRefresh()` is set. Non-TTY output is plain — no
ANSI escape sequences, no redraws.

**Dependency footprint:** Lean. Direct dependencies: `VividCortex/ewma`,
`acarl005/stripansi`, `mattn/go-runewidth`, `golang.org/x/sys`. No heavy UI
framework.

**Mixing with normal output:** `*Progress` implements `io.Writer`. Writes routed
through `p.Write()` are queued and flushed at the next refresh cycle, printing
above the active bars. This is the supported pattern. However, external code
that writes directly to `os.Stderr` (bypassing `p.Write()`) causes the classic
problems documented in issues #84, #105, and #118: bars get duplicated,
overwritten, or stuck. This is the critical constraint for niwa: the git
subprocess pipes (`cmd.Stderr = os.Stderr`) are bypass writes.

**Maintenance:** Active. Latest release v8.12.0 was February 17, 2026; 2,164
commits, 94 releases.

**Verdict for niwa:** Best fit if a `Reporter` abstraction is introduced that
routes all output through `p.Write()` — including subprocess stderr, which
would need to be captured (e.g., `cmd.Stderr = &mpb.interceptWriter`) and
forwarded. Sequential (non-concurrent) operations work fine; mpb adds value
with multiple simultaneous bars. The migration scope is real but narrower than
bubbletea: a thin `Reporter` wrapper is sufficient.

---

### progressbar (schollz/progressbar)

**Programming model:** Direct imperative API. `progressbar.Default(total)` or
`progressbar.New(total)` returns a bar. Call `bar.Add(n)` or `bar.Set(n)` from
any goroutine. No container, no central coordinator.

**Concurrency:** Thread-safe internally via `sync.Mutex`. Each bar is
independent; there is no built-in mechanism to render multiple bars
simultaneously (single-line output only, by design — the author explicitly
states no multi-line support to keep OS portability).

**TTY / non-TTY:** Uses `mattn/go-isatty` (via `golang.org/x/term`) to detect
terminals. In non-TTY environments, ANSI escape codes are omitted and the bar
degrades gracefully. `OptionSetVisibility(false)` and `DefaultSilent()` /
`DefaultBytesSilent()` can suppress output entirely.

**Dependency footprint:** Small. Direct: `mattn/go-runewidth`,
`mitchellh/colorstring`, `k0kubun/go-ansi`, `golang.org/x/term`. Last release:
v3.19.0, December 26, 2025.

**Mixing with normal output:** Each bar writes to a single line and uses ANSI
escape sequences to redraw in place. Other output written to the same stream
will scroll the bar up and leave it orphaned. No built-in mechanism for above-
bar log printing. `bar.Clear()` exists, but managing interleaved output is
manual.

**Maintenance:** Active. Steadily maintained; not a large team but consistent
releases.

**Verdict for niwa:** Easiest to introduce as a per-operation spinner/bar with
no central coordinator. Works well for sequential per-repo feedback if
interleaved log output is suppressed or deferred. Does not solve the parallel-
repos-in-flight problem. Subprocess stderr bypass is the same problem as with
mpb; no built-in answer.

---

### pterm

**Programming model:** Fluent API with individual printer objects. Progress bars
and spinners are started with `.Start()`, updated with `.Increment()` /
`.UpdateText()`, and stopped with `.Stop()` / `.Success()` / `.Fail()`. No
central event loop; each printer is self-contained.

**Concurrency:** Printers are independent; multiple can run simultaneously
without explicit coordination. The library's own examples show spinners and
progress bars alongside `pterm.Success.Println()` calls.

**TTY / non-TTY:** Handles CI and non-interactive environments; mentioned
explicitly as compatible with GitHub Actions and similar systems.
`DisableOutput()` / `EnableOutput()` provide global control. The exact
detection mechanism is not documented in public-facing docs, but the library
strips ANSI codes when the output is not a TTY.

**Dependency footprint:** Heavier than the others. Direct dependencies include:
`atomicgo.dev/cursor`, `atomicgo.dev/keyboard`, `atomicgo.dev/schedule`,
`MarvinJWendt/testza`, `gookit/color`, `lithammer/fuzzysearch`,
`mattn/go-runewidth`, `golang.org/x/term`, `golang.org/x/text`. Nine direct
dependencies, plus several indirect ones. This is a large surface for a CLI
that prioritises dependency hygiene.

**Mixing with normal output:** pterm's own logging functions (`pterm.Info.Println`
etc.) appear to coexist with its spinners and bars in examples. External
`fmt.Fprintf(os.Stderr, ...)` calls face the same interleaving problem as all
ANSI-redraw libraries.

**Maintenance:** Active, 1,919 commits; Go 1.24 minimum.

**Verdict for niwa:** Richest feature set but heaviest dependency footprint.
The keyboard and schedule dependencies are completely unused for niwa's use
case. Using pterm would drag in capabilities that niwa doesn't need. Not
appropriate given niwa's dependency hygiene priority.

---

### uiprogress (gosuri/uiprogress)

**Programming model:** Global singleton with a central `Start()` call. Bars are
added via `uiprogress.AddBar(total)`. A background goroutine renders all bars.

**Concurrency:** Designed for multiple concurrent bars via the central renderer.

**TTY / non-TTY:** Not documented. The library pre-dates `golang.org/x/term`
becoming standard; behavior in non-TTY is unknown without source inspection.

**Dependency footprint:** Minimal; depends on `gosuri/uilive`.

**Maintenance:** Effectively abandoned. Last release April 11, 2019 (v0.0.1).
18 open issues, 11 open PRs, no activity. The library does not use Go modules
properly for its current state.

**Verdict for niwa:** Ruled out on maintenance grounds alone. Do not introduce a
dependency with no releases in seven years.

---

### briandowns/spinner

**Programming model:** Simple imperative: `spinner.New(...)`, `s.Start()`,
`s.Stop()`. Per-operation; no central coordinator.

**Concurrency:** Not explicitly thread-safe for concurrent updates. Known API
design issues around concurrent text updates were part of the motivation for
yacspin's creation.

**TTY / non-TTY:** Supports `spinner.WithWriter(os.Stderr)`; no documented TTY
detection.

**Dependency footprint:** Moderate; uses `fatih/color`.

**Maintenance:** Active; last release v1.23.2, January 20, 2025.

**Verdict for niwa:** Functional for sequential per-repo spinners, but no TTY
detection and known concurrency limitations. yacspin is a better-designed
alternative from the same niche.

---

### yacspin (theckman/yacspin)

**Programming model:** Single-spinner imperative API. Create a `SpinnerConfig`,
call `spinner.New(cfg)`, then `Start()` / `Stop()`. The animation runs
independently from message updates — updating text doesn't change animation
speed.

**Concurrency:** Explicitly safe for concurrent use. Multiple goroutines can call
update methods while the spinner is animating.

**TTY / non-TTY:** Automatically detects non-TTY environments (checks `TERM` and
terminal detection). When non-TTY is detected: no colors, no automatic
animation, spinner only animates on explicit message updates. Clean fallback.

**Dependency footprint:** Not fully extracted; appears minimal (borrows from
briandowns/spinner character sets, uses `fatih/color`).

**Maintenance:** Maintenance mode. Last release v0.13.12, December 31, 2021.
Stable but not actively developed.

**Verdict for niwa:** Better-designed than briandowns/spinner for concurrent
safety and TTY detection, but maintenance-stale and single-spinner only. No
parallel-bars capability.

---

## Implications

**The central constraint is subprocess stderr bypass.** In `clone.go`, `sync.go`,
`configsync.go`, and `setup.go`, git and other subprocesses are run with
`cmd.Stderr = os.Stderr`. Any library that redraws terminal lines in-place will
conflict with these direct writes. This is not a detail — it's the dominant
constraint. Fixing it requires either:

1. Capturing subprocess stderr into a buffer or pipe and routing it through the
   library's safe write path, or
2. Accepting that progress display only appears during non-subprocess phases and
   subprocess output is still streamed raw.

**mpb is the strongest fit if a thin Reporter abstraction is introduced.** Its
`p.Write()` mechanism already provides the above-bar log line pattern. Its TTY
detection is automatic and correct. Its dependency footprint is lean. The
migration path: introduce a `Reporter` interface accepted by apply/clone/sync,
implement it with an `*mpb.Progress` writer in TTY mode and plain `os.Stderr`
in non-TTY mode, and capture subprocess stderr into the reporter rather than
piping directly to `os.Stderr`.

**progressbar (schollz) is the lowest-effort option for sequential-only display.**
No central coordinator, thread-safe, one bar at a time per repo. Appropriate if
the decision is to show a simple per-repo spinner during clone and suppress all
other output until done. Falls short for parallel operations.

**bubbletea is not feasible** without a near-complete rewrite of the output
architecture. The incompatibility with scattered `fmt.Fprintf` calls is
fundamental, not incidental.

**pterm's dependency footprint disqualifies it** for a CLI tool with dependency
hygiene as a stated constraint. Pulling in keyboard input and scheduler
dependencies for a progress bar is not justified.

**uiprogress is ruled out** — abandoned since 2019.

**yacspin is a respectable option** for a single-spinner-per-operation approach
with good TTY fallback, but its maintenance status (last release 2021) is a
risk and it offers no multi-bar support.

---

## Surprises

- **mpb v8 is more actively maintained than expected** — released as recently as
  February 2026. The library's major version history shows sustained investment.

- **The subprocess stderr problem is more pervasive than it might appear.** Not
  just `clone.go` — `sync.go`, `configsync.go`, and `setup.go` all pipe git
  subprocess output directly to `os.Stderr`. Any ANSI-redraw library breaks on
  this pattern.

- **mpb's `p.Write()` is a documented and intentional solution** for mixing log
  lines with progress bars, but it requires all writers to use it. External
  bypass writes (the git pipes) still corrupt output.

- **schollz/progressbar explicitly chose not to support multi-line output** for
  OS portability. This is a deliberate design decision, not an oversight.

- **yacspin's non-TTY fallback is unusually thoughtful** — it doesn't just go
  silent, it animates only on explicit message updates, giving a readable log-
  style output in CI.

- **pterm's dependency list is much heavier than its marketing implies.** The
  "beautiful console output" pitch does not mention nine direct dependencies
  including keyboard input handling.

---

## Open Questions

1. **What does niwa's apply output look like in practice at scale?** For 10–20
   repos, is the log-dump actually painful, or does the issue appear mainly with
   larger workspace configs? The right UX decision depends on real usage
   patterns.

2. **Are the git subprocess pipes actually needed as live-streaming, or could
   they be captured and replayed?** If subprocess output is captured into a
   buffer, interleaving with progress display becomes tractable. But capturing
   git clone's live progress (remote counting objects, etc.) changes what users
   see.

3. **Is parallel clone/apply already in use, or is the current apply loop
   sequential?** If sequential, a single-bar library (schollz) may suffice.
   If parallel, mpb's multi-bar model fits better.

4. **What is the target for CI / non-TTY environments?** Should progress display
   degrade to a log-style one-line-per-repo format, or should it be completely
   suppressed in favor of raw git output?

5. **Does the `Reporter` abstraction belong in the workspace package or the CLI
   layer?** The workspace functions accept `io.Writer` for some things already
   (e.g., `FilesMaterializer.Stderr`). A consistent pattern for propagating a
   reporter down the call stack needs design before any library is chosen.

6. **How does yacspin's maintenance risk weigh against its good design?** Last
   release was 2021; the library appears stable but unmaintained. Is forking or
   vendoring an option if it becomes the preferred choice?

---

## Summary

Of the libraries investigated, **mpb is the best architectural fit** for niwa if
a thin `Reporter` abstraction is introduced, because it provides concurrent
multi-bar rendering, automatic TTY detection with clean non-TTY fallback, and a
supported `p.Write()` path for mixing log output — but it still requires
capturing subprocess stderr that currently bypasses all output control. The
**biggest constraint** is not which library to choose but rather that git
subprocess stderr is piped directly to `os.Stderr` in at least four files,
meaning any progress display library will require a subprocess capture layer
before it can work correctly. The **biggest open question** is whether niwa's
apply loop runs repos sequentially or concurrently, since the answer determines
whether the added complexity of a multi-bar library (mpb) is justified over a
simpler per-operation approach (schollz/progressbar).
