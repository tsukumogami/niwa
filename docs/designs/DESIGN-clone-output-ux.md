---
status: Proposed
problem: |
  Niwa's create and apply commands dump a linear log of git subprocess output
  and status messages to stderr with no TTY awareness. Users see git progress
  chatter, per-repo status lines, and warnings all interleaved — no visual
  indication of what's currently happening. Git subprocess stderr is piped
  directly to os.Stderr in four files, making any inline progress display
  incompatible without an explicit capture layer, and 29 niwa-authored output
  sites are scattered across the workspace package with no shared abstraction.
---

# DESIGN: Clone/Apply Output UX

## Status

Proposed

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

## Solution Architecture

<!-- To be filled in by /design -->

## Considered Options

<!-- To be filled in by /design -->

## Implementation Approach

<!-- To be filled in by /design -->

## Security Considerations

<!-- To be filled in by /design -->

## Consequences

<!-- To be filled in by /design -->
