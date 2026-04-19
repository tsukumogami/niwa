# Lead: Industry CLI inline progress patterns

## Findings

### Cargo (Rust package manager)

Cargo uses a two-layer output model. During `cargo build`, individual `Compiling <crate>` lines scroll as jobs start. Simultaneously, a single-line progress bar occupies the bottom of the terminal, showing how many crates have completed (e.g., `Building [====>   ] 12/153: libc`). That bottom line is rewritten in place using a carriage return (`\r`) followed by a clear-to-end-of-line sequence — not cursor-up. When a crate finishes, the `Compiling` line is already gone (it was printed when the job started); the progress counter ticks forward.

This creates a known UX problem: printing the crate name at job start rather than job completion makes parallel builds appear sequential, and the progress bar is only useful on terminals wider than ~100 characters — on 80-character terminals it disappears entirely. A long-standing GitHub issue (#8889) proposes switching to past-tense `Compiled <crate> in X.YYs` printed on completion, similar to how linker timing would appear.

TTY detection is central: when stderr is not a TTY, progress bars are suppressed. A bug (#9155) showed that force-enabling them via `CARGO_TERM_PROGRESS_WHEN=always` causes display corruption in pipes because the carriage-return clear mechanism only works in interactive terminals.

**Mechanism**: carriage return + clear-to-EOL on a single reserved line. Scrolling log for individual events. Sequential and parallel events both feed the same bottom bar.

### Docker pull

Docker's multi-layer image pull is the canonical example of per-row inline progress for parallel operations. Each image layer gets its own line. As layers download concurrently, Docker maintains a `layers []string` array of seen layer IDs. When an update arrives for a layer already in the array, it calculates the cursor offset (how many rows up) and repositions using `\033[NF` (cursor up N lines) plus `\033[2K` (erase line), then rewrites that row in place.

New layers append to the bottom; completed layers receive a final status line and are left in place. The cursor hides during updates (`\033[?25l`) and is restored on completion (`\033[?25h`). Final summary lines (Digest, Status) are filtered to avoid appending redundant information.

This approach works well for a bounded, known set of parallel items (layers) but does not generalize cleanly to unbounded parallel work with unknown counts.

**Mechanism**: per-row cursor repositioning with `\033[NF` (cursor up). One persistent line per parallel operation. Parallel-first design.

### npm / yarn / pnpm

npm removed its default progress bar around version 10.7.0; as of 2025 it shows nothing during install by default unless `--verbose` is set, which produces a fire-hose log. This was a deliberate choice; a GitHub issue requesting the progress bar back was closed as "not planned."

Yarn (classic) used a progress bar on a single line with carriage-return rewriting; it had known bugs on Windows CMD (would print each update as a new line) and in VS Code's terminal.

pnpm has the most considered design. It exposes a `reporter` configuration with four modes:
- `default` — used when stdout is a TTY; shows live progress
- `append-only` — always appends new lines, no cursor manipulation (useful for CI logs)
- `ndjson` — machine-readable newline-delimited JSON
- `silent` — no output

For parallel script runs (`pnpm -r --parallel`), pnpm aggregates child process output and prints it only after each child finishes, avoiding interleaved output.

**Mechanism**: pnpm's `default` reporter uses TTY-aware in-place updates; `append-only` falls back to pure scrolling. The explicit reporter selection is a clean design pattern for handling the TTY vs. non-TTY tension.

### GitHub CLI (`gh run watch`)

`gh run watch` takes over the terminal using an alternate screen buffer. It calls `StartAlternateScreenBuffer()` at the start and `StopAlternateScreenBuffer()` on exit. Every 3 seconds it clears the screen and redraws from scratch into a `bytes.Buffer` before writing to stdout.

This is the full-screen TUI approach, distinct from inline progress. It works well for a "watch" mode where the user explicitly opts into a monitoring session, but it destroys scroll history and is inappropriate for commands that are part of a pipeline or script.

**Mechanism**: alternate screen buffer + periodic full redraw (not cursor-up line replacement). Appropriate only for interactive watching, not for `clone` / `apply` style one-shot commands.

### kubectl

kubectl's progress approach is minimal by design. `kubectl rollout status` prints incremental status lines as replicas come up — e.g., `Waiting for deployment 'slow' rollout to finish: 0 of 5 updated replicas are available...` — updating the count with a new line per change, not in-place rewriting. It does not use spinners or cursor manipulation. `kubectl wait` is even simpler: silent until the condition is met, then a single success message.

**Mechanism**: append-only log lines with periodic status messages. No inline rewriting. Reflects a philosophy of simplicity and script-friendliness over visual polish.

### Zig compiler (`zig build`)

Zig's progress bar (shipped 2024, designed by Andrew Kelley) represents the most technically sophisticated approach found in this research. Key design decisions:

1. **Terminal ownership**: When stderr is a TTY, Zig treats the terminal as owned by the process. This allows registering a `SIGWINCH` handler and using full ANSI control.

2. **Clear-and-redraw cycle**: On each update tick, Zig moves the cursor up by the count of previously drawn lines (`\033[NF` repeated), then clears to end of screen (`\033[J`), then redraws the entire progress tree. The draw buffer is assembled before acquiring the stderr lock to minimize lock hold time.

3. **Sync sequences**: The repaint is wrapped in terminal sync sequences to prevent flickering on high-refresh terminals.

4. **Child process coordination**: Child processes do not write to the terminal directly. Instead, the parent creates a `O_NONBLOCK` pipe and passes its FD to the child via `ZIG_PROGRESS=3`. Children serialize progress updates into the pipe; the parent reads and integrates them. The parent ignores all but the most recent message per child to avoid buffering lag.

5. **Thread safety without locks on hot path**: 200 progress nodes are pre-allocated (later reduced to 83 to fit IPC messages in a 4096-byte page). Threads update nodes via atomic operations; a dedicated update thread copies state before drawing.

6. **Child stdout still flows through**: child process stdout/stderr still reaches the terminal; the progress display moves down to accommodate it, rather than suppressing it.

**Mechanism**: periodic clear-and-redraw of a multi-line tree. Cursor-up to the top of the progress area, then erase-to-end-of-screen, then redraw. The canonical example of "owns the terminal" philosophy for parallel build progress.

### Terraform

Terraform's `apply` command prints an append-only log by default. Each resource gets a line when it starts (`aws_instance.web: Creating...`) and a line when it finishes (`aws_instance.web: Creation complete after 12s`). No in-place rewriting. For machine-readable output, `-json` emits a stream of JSON messages: `apply_start`, `apply_progress` (elapsed time), `apply_complete`, `apply_errored`.

Terraform Cloud has a richer web UI with per-resource rows and timelines, but the CLI stays append-only.

**Mechanism**: append-only log. Clear start/end events. `-json` for machine-readable streaming.

### CLIG (Command Line Interface Guidelines)

The widely-referenced https://clig.dev/ formalizes these patterns as principles:

- Send progress to stderr; results to stdout.
- Display something within 100ms to appear responsive.
- Don't display animations when stdout/stderr is not a TTY — prevents garbled CI logs.
- For parallel operations, use libraries that support multiple progress bars to avoid interleaved output.
- When errors occur during progress display, print accumulated logs so users understand what failed.
- Support `--plain` or `--no-color` for machine-readable output.

### Evil Martians CLI UX guide

Recommends matching the mechanism to the operation type:
- **Spinner**: one or a few sequential tasks completing in seconds.
- **X of Y**: step-by-step processes where you can count discrete units.
- **Progress bars**: multiple simultaneous lengthy processes where monitoring N separate X/Y counters becomes unwieldy.

On completion, clear the indicator and switch from gerund ("downloading") to past tense ("downloaded"). Maintain logs suitable for piping.

---

## Implications

**TTY detection is non-negotiable.** Every well-designed CLI in this survey detects whether stderr is a TTY and disables cursor-manipulation modes in non-interactive contexts. For niwa, this means the inline progress display should fall back to append-only logging when not a TTY — and the append-only path should produce clean, greppable lines.

**The two-layer cargo pattern maps well to niwa's sequential repo cloning.** Niwa clones repos one or several at a time, not hundreds in parallel. A single updating status line at the bottom (which repo is cloning, what step it's on) combined with a scroll log for completed events (or errors) would match how users already understand this kind of operation. This avoids the complexity of Docker's per-row cursor repositioning.

**Errors during inline progress need explicit handling.** When a clone fails, the spinner/status line must clear and the error must land in the scrolling record. If errors just overwrite the status line without persisting, users lose the error message on scroll. The CLIG guidance is explicit: print accumulated logs on failure.

**Alternate screen (Bubble Tea / `gh run watch` style) is wrong for niwa's use case.** `niwa apply` and `niwa create` are one-shot operations users might run in a script or pipe into other tools. Alternate screen destroys scroll history and is inappropriate for non-interactive invocations. Inline progress preserves the terminal log.

**pnpm's `append-only` reporter mode is a good prior art for CI friendliness.** Niwa should consider a `--plain` or similar flag that switches to pure append-only lines, making the output predictable for CI, logging, and scripts.

**Parallel cloning adds complexity.** If niwa ever clones repos concurrently, the Docker-style per-row cursor repositioning is the established pattern. But it requires knowing the number of parallel operations up front (or dynamically tracking rows), and it only works if the number of rows fits on screen. For small workspace counts this is feasible; for large ones, a single aggregate bar (X/N repos cloned) is safer.

---

## Surprises

**npm silently removed its progress bar.** This was not a well-publicized change and was closed as "not planned." It means npm's current default — showing nothing — is considered acceptable by the maintainers. This is a cautionary example: users strongly prefer some visible signal over silence.

**Cargo's "Compiling" lines are acknowledged as misleading.** The cargo maintainers know the current model (print on start, not on completion) makes parallel builds look serial. This is an accepted limitation with a known desired fix.

**Zig's progress protocol is a separate IPC mechanism.** The `ZIG_PROGRESS` environment variable and pipe-based protocol allows child processes to report progress to a parent aggregator without touching the terminal. This is far more sophisticated than most CLIs and only makes sense for a build system spawning many subprocesses.

**git clone writes progress to stderr using carriage returns, not cursor-up.** Git's progress is a single line overwritten in place, just like cargo's bar — not multi-line. The `--progress` flag forces this even when stderr is not a TTY.

**`gh run watch` uses a full alternate screen, not inline updates.** This was surprising given the command's "watch" semantics. It means `gh run watch` cannot be composed into a pipeline and leaves no scroll history.

---

## Open Questions

1. **Does niwa ever clone repos in parallel?** The Docker-style per-row model only adds value if multiple clones run concurrently. If cloning is always sequential, a single-line status is sufficient and simpler.

2. **What is the right fallback format for CI / non-TTY?** Should it be plain `printf` lines as now, or something more structured (e.g., a subset of pnpm's `append-only` style with consistent prefixes like `[clone] repo-name: done`)?

3. **How should errors during a multi-repo apply interact with the inline status?** If repo 3 of 8 fails, should niwa continue with the remaining repos and collect all errors at the end, or stop and display immediately? The answer affects how the progress display handles error state.

4. **What terminal width assumptions are safe?** Cargo's progress bar disappears at 80 columns. Niwa's status line should degrade gracefully to something meaningful at narrow widths.

5. **Is there value in a `--plain` flag that produces append-only output?** This would eliminate the complexity of TTY detection for users who always want simple logs (CI, logging pipelines, script consumption).

---

## Summary

Every major CLI in this space uses TTY detection to switch between inline cursor-manipulation progress and append-only log output, with the consensus being that interactive terminals get spinners or updating status lines while non-TTY contexts get plain scrolling text. For niwa's sequential or lightly-parallel repo cloning, the cargo two-layer model — scrolling log for completed events plus a single updating status line at the bottom — is the most appropriate pattern, as it preserves scroll history and works cleanly in non-TTY fallback mode without the complexity of Docker's per-row cursor repositioning. The biggest open question is whether niwa ever clones repos in parallel, since the answer determines whether a single status line is sufficient or whether per-row tracking (with its attendant terminal-width and cursor-management complexity) is needed.
