# Decision 3: TTY Detection and Output Mode Strategy

**Status:** Decided
**Date:** 2026-04-19

## Decision

Use **Option B: isatty + NO_COLOR + --no-progress flag**.

TTY mode (animated status line) activates only when stderr is attached to a terminal
AND `--no-progress` is not set. NO_COLOR suppresses color output without affecting
the progress display.

## Detection Logic

Evaluated once in `PersistentPreRunE` (root.go), in priority order:

```
1. --no-progress flag set         → non-TTY mode (explicit, highest priority)
2. term.IsTerminal(stderr)        → TTY mode if true, non-TTY if false
3. NO_COLOR env var set           → color off (does not affect progress gate)
```

The resulting `DisplayMode` value is passed to the Reporter constructor and stored
on the Applier struct.

## What "Progress Suppressed" Means

In non-TTY mode (non-terminal OR --no-progress):
- No status line / animated spinner
- No ANSI escape sequences
- Completed-event lines still print (append-only): "cloned foo", "updated bar"
- Errors and warnings print as before

In TTY mode:
- Status line active (Decision 1 Reporter handles rendering)
- ANSI sequences for the status line only
- If NO_COLOR is set: status line renders without color codes

## Library Choice

Add `golang.org/x/term` as a direct dependency. It's the official Go sub-repo for
terminal handling (`IsTerminal` wraps the appropriate syscall per platform). Neither
x/term nor mattn/go-isatty is currently in go.mod — x/term is lower cost because
it introduces no third-party maintainers.

## Where Detection Lives

`internal/cli/root.go`, in `PersistentPreRunE`. This function runs before every
subcommand and already performs env-var capture (`captureNiwaResponseFile`). The
`--no-progress` flag is registered on `rootCmd.PersistentFlags()` so all subcommands
inherit it automatically.

The computed `DisplayMode` is passed to downstream consumers via a package-level
function or a simple struct, not re-evaluated per call.

## Flag Name

`--no-progress` (not `--quiet`). Rationale:
- Names exactly what it controls: the progress display
- `--quiet` implies broader suppression (cargo hides completion lines too), which
  loses valuable per-repo output in CI logs
- `--no-progress` is self-documenting in CI pipeline YAML

## CI Environment Handling

CI env vars (GITHUB_ACTIONS, GITLAB_CI, etc.) are not checked. The explicit
`--no-progress` flag is the correct opt-out for CI pipelines. This avoids:
- False positives from CI_TOKEN-style variables
- Incomplete coverage (dozens of CI systems exist beyond the big three)
- Surprising behavior when running locally with CI env vars set (e.g., via `act`)

Users add `--no-progress` to their pipeline invocation once. This is explicit,
portable, and searchable.

## TERM=dumb

Not checked. `term.IsTerminal` returns false for dumb terminals (the fd is not a
real TTY), so they fall into non-TTY mode automatically. No special case needed.

## Rejected Alternatives

**Option A (isatty-only):** No explicit escape hatch for scripts. Fails in
PTY-in-CI. Ignores NO_COLOR convention.

**Option C (isatty + specific CI vars + --no-progress):** Incomplete CI coverage
creates inconsistent behavior. False-positive risk (act, local env var leakage).
Maintenance burden grows as CI systems proliferate.

**Option D (--quiet):** Suppresses completion lines, which are useful in CI logs.
Conflates "no animation" with "silent". Harder to extend later.
