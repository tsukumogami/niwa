# Exploration Findings: clone-output-ux

## Core Question

Should niwa replace its linear log-dump approach to clone/apply output with inline
status indicators that update in place, and if so, how should it handle the
tensions between progress display, error/warning visibility, and machine-readable
output?

## Round 1

### Key Insights

- **The apply loop is sequential** (no goroutines in apply.go). The Docker-style
  per-row multi-line display is unnecessary. A single updating status line is
  sufficient. (codebase grep)

- **The git subprocess pipe is the central architectural challenge.** clone.go,
  sync.go, configsync.go, and setup.go all use `cmd.Stdout = os.Stderr;
  cmd.Stderr = os.Stderr`. Any ANSI-redraw library will corrupt output unless
  git's output is suppressed or captured via a goroutine. (output-site-map,
  go-libraries, error-warning-visibility)

- **The cargo two-layer model is the right pattern.** Completed events scroll as
  normal lines; a single carriage-return-rewritten status line at the bottom shows
  what's currently happening. Falls back to append-only on non-TTY — which is
  exactly niwa's current behavior. (industry-cli-patterns)

- **The `suspend-clear-print-redraw` pattern handles niwa's own warnings cleanly.**
  Before printing a `warning:` line, erase the status line (`\r\033[K`), print
  the warning, let the bar redraw on the next tick. 29 niwa-authored output sites
  to migrate, 17 concentrated in apply.go. (error-warning-visibility,
  output-site-map)

- **A thin `Reporter` abstraction is structurally ready.** Multiple functions
  already accept `io.Writer` (emitRotatedFiles, checkRequiredKeys,
  CheckGitHubPublicRemoteSecrets, both materializers) — they pass `os.Stderr`
  at call sites today. The abstraction is half-done; it needs wiring, not
  invention. (output-site-map)

- **TTY detection alone isn't sufficient; a layered approach is standard.**
  isatty() handles the common case but breaks on PTY-allocating CI runners.
  Convention: TTY detection + `NO_COLOR` (colors only) + `CI` env var (soft
  hint) + explicit `--no-progress` flag for consumers needing deterministic
  output. (tty-machine-readable)

- **Demand not validated, but not rejected.** Single-author repo, no external
  requests. DESIGN-shell-navigation-protocol.md hedges for future output modes.
  (adversarial-demand)

### Tensions

- **Suppress vs. capture git subprocess output.** Suppressing (like
  overlaysync.go) is simpler — niwa's own summary messages already carry the
  information. Capturing via goroutine preserves git error detail on failure.
  More complex but gives an integrated display where errors appear inline with
  the spinner.

- **`--json` vs. `--no-progress` for machine-readable mode.** `--no-progress`
  is sufficient if consumers need "no visual noise." `--json` is better if
  consumers need structured per-repo results. Findings favor `--no-progress`
  unless a concrete machine-readable consumer exists.

### Gaps

- Unknown whether functional tests capture raw stderr output (would affect
  Reporter refactor scope).
- Unknown whether to add a dependency (schollz/progressbar, mpb) or implement
  the ANSI mechanism directly (~20 lines, no new deps).

### Decisions

- **Approach to git subprocess output: capture via goroutine.** User chose to
  pipe git stderr through a goroutine reader, filtering `\r`-terminated progress
  frames and forwarding `\n`-terminated error lines. Enables an integrated
  display where git errors appear inline with the spinner.

### User Focus

Capture via goroutine is the chosen approach. This unlocks inline progress where
git errors surface in place rather than after the fact.

## Accumulated Understanding

The target UX for niwa's apply/create commands is a **cargo-style two-layer
display**: completed-repo events scroll as normal lines (past tense: "cloned
tools/myapp"), while a single status line at the bottom (using `\r` + `\033[K`)
shows the current operation ("cloning tools/tsuku..."). On non-TTY terminals,
the status line is suppressed and the behavior is identical to today.

The key implementation challenges are:

1. **Git subprocess stderr capture.** Each `git clone`, `git fetch`, `git pull`
   subprocess currently pipes directly to `os.Stderr`. This needs to be replaced
   with a goroutine reader that filters `\r`-terminated progress frames (the
   "remote: Enumerating objects" chatter) and forwards `\n`-terminated lines
   (errors). The filtered lines route through the `Reporter` as warnings/errors.

2. **`Reporter` abstraction.** All 29 niwa-authored `fmt.Fprintf(os.Stderr, ...)`
   sites need to route through a thin `Reporter` interface (or equivalent) so
   the TTY implementation can clear the status line before printing, and the
   non-TTY implementation can pass through unchanged. The infrastructure
   (injectable `io.Writer` fields, functions that already take `io.Writer`) is
   partially in place.

3. **Machine-readable mode.** A `--no-progress` flag (or `CI` env var detection
   as a secondary signal) suppresses the status line and keeps append-only output.

The overlay sync path (overlaysync.go) already suppresses all output — this must
not change.

The apply loop is sequential, so no multi-bar concurrent rendering is needed.

## Decision: Crystallize
