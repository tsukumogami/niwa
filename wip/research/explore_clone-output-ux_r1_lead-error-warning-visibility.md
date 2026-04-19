# Lead: Error and warning visibility during progress display

## Findings

### The fundamental tension

Progress bars use `\r` (carriage return) to overwrite the current line in place. Any other output written to the same stream without coordination will either be overwritten by the next progress tick, or will push the progress bar down, leaving orphaned partial lines above it. The problem is especially acute when the progress display occupies multiple lines (docker-style per-layer bars), because cursor-up sequences are needed to redraw earlier lines and any interleaved write breaks the assumed cursor position.

### Cargo: clear-before-write via a `needs_clear` flag

Cargo channels all output through a `Shell` struct. The struct tracks a `needs_clear` boolean. When the progress bar draws itself (using `\r` + the bar text), it sets `needs_clear = true`. When any Shell output method (warn, error, status) is called, it checks the flag and, if set, emits `\x1b[K` (erase to end of line) first to wipe the progress bar from the current line, then prints the message on a fresh line, then sets `needs_clear = false`. The progress bar redraws on the next tick. The progress bar writes only to stderr; the `needs_clear` mechanism lives only in Shell, so stdout output from subprocesses bypasses it (a known limitation, documented in issue #9155 and PR #6618). The net effect: cargo warnings and errors interrupt the progress bar immediately, appear as normal scrolling text, and the progress bar resumes underneath. Errors are never buffered.

Key code path: `src/cargo/core/shell.rs` (Shell struct, `set_needs_clear`, output methods) and `src/cargo/util/progress.rs` (draws with `\r`, calls `shell.set_needs_clear(true)` after each draw, rate-limited at 100 ms intervals after the first 500 ms delay).

### npm / npmlog / gauge: same stream, pause-then-resume

npm uses `npmlog`, which wraps `gauge` for the progress bar. When a log message is written, gauge's `hide()` method fires first: it moves the cursor to the start of the line, emits `\r` + spaces (or `\x1b[K` in newer gauge versions) to erase the bar, then the log message prints as a normal scrolling line. Gauge then redraws on the next `pulse` or `show` call. This is conceptually identical to cargo's `needs_clear` flag but expressed as explicit `hide()`/`show()` calls on the gauge object. The `npmlog` README notes that stderr and stdout must be set to blocking mode (`set-blocking`) so that log lines and the gauge output can be correctly interleaved; without blocking, partial writes from two concurrent writes can corrupt terminal state. Like cargo, npm prints warnings immediately; they are not buffered.

A historical bug (fixed in npmlog 7.0.1) caused the progress bar to remain enabled even when logs were paused, so the most-recent log line (often an error) would appear frozen inside the bar for the rest of the process.

### Docker pull: per-layer multi-line display, error in-place

`docker pull` renders a multi-line progress display: one line per image layer (e.g. "Pulling fs layer", "Downloading", "Pull complete"). Each line is managed via cursor-up (`\033[A`) sequences to redraw individual layer lines in place. This is a more complex variant of the progress-bar pattern because the display is N lines tall and requires absolute cursor positioning.

When an error occurs during a pull (e.g. authentication failure, network error), Docker CLI renders the error directly below the progress region rather than interrupting the in-flight layer lines. For `docker service create` / `docker service update`, PR docker/cli#259 explicitly changed the behavior so that when a task encounters an error, the error replaces the progress indicator for that task rather than leaving a stuck bar. The general pattern in the BuildKit `--progress=tty` path is: clear the entire progress region (`\033[A` + `\033[K` per line, repeated for each line), print the error as scrolling output, then either stop or redraw the remaining progress. In non-TTY mode (`--progress=plain`), docker falls back to raw line-by-line output and the question does not arise.

### Zig: lock-then-clear-and-repaint, atomic writes

Zig's progress bar (described by Andrew Kelley in a 2024 blog post) owns the terminal when stderr is a TTY. It maintains the progress display at the bottom of the terminal. When any code path needs to print a message (a log, a warning, an error), it calls `std.debug.lockStdErr()`. While the lock is held, the progress bar update thread skips its frame (drops the update rather than blocking). The caller clears the progress region (`\033[A` + `\033[2K` per row), prints its message as scrolling text above, then releases the lock; the update thread then repaints the progress region. All terminal writes during a single frame are batched into one buffer and sent atomically, so the clear-and-repaint cannot be interleaved with writes from another thread. For child processes whose stderr goes directly to the terminal (non-cooperative), Zig keeps the cursor at the top-left of the progress region so child writes push the progress display down rather than corrupt it — the messages are preserved, the progress display reflows.

### Python tqdm: `tqdm.write()` as the escape hatch

`tqdm` uses `\r` to redraw its progress line. If user code calls `print()` or `logging.StreamHandler` writes to the same stream, those writes corrupt the bar. The solution is `tqdm.write(msg)`: it moves the cursor to the start of the line, emits the message followed by `\n`, then ensures the bar is redrawn on the next tick. Thread-safe in Python 3. The `tqdm.contrib.logging` module provides `logging_redirect_tqdm()` to patch the standard `logging` infrastructure so all `logging.warning()` / `logging.error()` calls go through `tqdm.write()` automatically. The key constraint: code outside tqdm (subprocesses writing directly to the fd) cannot be redirected this way. `warnings.warn()` also bypasses tqdm unless explicitly patched.

### Python Rich: stderr/stdout redirection + Live display

Rich's `Progress` and `Live` context managers redirect `sys.stdout` and `sys.stderr` by default (`redirect_stdout=True`, `redirect_stderr=True`). All output captured through these redirected handles is printed via `console.log()`, which renders above the live display. Internally, Rich uses `\r\033[K` (or equivalent) to clear the live area before reprinting, and a "sync" sequence is sent on supported terminals to suppress intermediate frames. Like Zig, Rich batches the clear + redraw. When external subprocesses write directly to the underlying file descriptor rather than through `sys.stderr`, Rich cannot capture them and interleaving corruption can occur.

### Git itself: `\r` only, no coordination with callers

Git's `progress.c` emits progress lines using only `\r` — no cursor-up, no erase-to-end-of-line. The progress line is overwritten in place by each tick. When git prints an actual error (e.g., "fatal: repository not found"), it emits a newline-terminated line to stderr. Because git flushes stderr, the error appears below the last progress line. There is no explicit "clear the progress bar before printing the error" logic; the error just lands on the next line. The progress line is left partially visible above it unless a subsequent progress tick (which won't come if git is dying) overwrites it. This is the simplest possible approach: progress and errors both go to the same fd, and the caller sees whatever lands there.

### Libraries: indicatif (Rust)

indicatif, used by many Rust CLIs, provides `ProgressBar::println(msg)` and `ProgressBar::suspend(f)`. `println` clears the current line, prints the message with a newline, then schedules a redraw. `suspend` hides the bar, executes a closure, then redraws — intended for cases where external code writes to stdout. When multiple bars are managed by `MultiProgress`, `println` prints above all bars. The library holds an internal lock during draws to make clear+redraw atomic. This is the same pattern as cargo's `needs_clear` but as a reusable library abstraction.

---

## Implications

**For niwa, the clearest parallel is cargo, not docker.**

Niwa's warnings and errors come from two sources: (a) niwa's own Go code (`fmt.Fprintf(os.Stderr, "warning: ...")`) and (b) git subprocess stderr flowing directly to `os.Stderr` via `cmd.Stderr = os.Stderr` in `clone.go` and `setup.go`. This distinction matters:

- Niwa's own warnings are under full control. A `needs_clear` flag (cargo style) or an explicit `suspend/println/resume` call (indicatif style) would work: before printing a niwa warning, clear the current progress line (`\r\033[K`), print the warning, then let the progress bar redraw on the next tick.

- Git subprocess stderr is not under niwa's control. `cmd.Stderr = os.Stderr` wires git's fd to the terminal directly. Git itself will write `\r`-based progress lines, then possibly error text. If niwa is also drawing a progress line on that same fd, git's writes and niwa's writes will interleave at the OS level. The only safe approaches are:
  1. **Pipe git stderr through a goroutine** that intercepts each line and either forwards it (for real git progress) or reformats it (for niwa's own display). This is the most control but most implementation complexity.
  2. **Suppress niwa's own progress display while git runs**, then resume after. Effectively: no concurrent progress.
  3. **Accept that git's native `\r`-based progress output will appear as-is** and forego a niwa-drawn progress line during clone/pull. This is the current behavior and is safe — git output scrolls normally, niwa warnings appear as normal lines.

If niwa adds a spinner or progress bar at all, option 2 (suppress while git runs, use the spinner only for niwa's own pre/post-clone steps) is the path of least resistance. Option 1 gives the best UX (a single integrated display) but requires parsing git's stderr stream, handling partial writes, and managing a goroutine per subprocess.

The "split-screen" pattern (progress pinned to bottom, logs scroll above) — used by Zig and Rich — requires full terminal ownership and cursor-absolute positioning. This is robust only when all writes go through a single controlled path. Because git writes directly to the fd, this approach would require fd-level interception (a pseudo-TTY between niwa and git), which is significant added complexity.

---

## Surprises

- **Git itself does not clear the progress line before printing errors.** The error just lands below whatever `\r`-based progress line last ran. This is visually acceptable because git terminates immediately after the error, but it is not "clean."
- **Docker's per-layer multi-line progress is genuinely hard.** Moving the cursor up N lines to update earlier lines means any unexpected write immediately corrupts the assumed cursor position. Docker mitigates this by batching the full clear-and-repaint; even so, users report terminal corruption when the layer count exceeds the terminal height.
- **tqdm's `tqdm.write()` cannot help with subprocess stderr.** The escape hatch only works for Python-level print/logging, not for external fd writes. The same limitation applies to Rich's stderr redirect.
- **npm had a bug where the last error was silently displayed inside the frozen progress bar** rather than as scrolling text. This failure mode — an error invisible because it is rendered as progress state — is exactly the wrong outcome. It underlines that "hold until done" approaches risk swallowing errors.
- **The cargo `needs_clear` approach has a known gap**: stdout output from subprocesses bypasses Shell and therefore bypasses the progress-clearing logic. Cargo filed this as a known issue.

---

## Open Questions

1. **What does git's `\r`-based progress output look like when captured through a pipe from niwa?** Does stripping `\r` lines produce clean output, or do the progress updates interleave with real messages? Testing with `git clone` piped through a Go `bufio.Scanner` on stderr would clarify this.

2. **Can niwa's warnings and git's progress output be disentangled reliably?** Git progress lines always use `\r` without a trailing `\n`. A goroutine reading git stderr line-by-line (using `\n` as delimiter) would naturally receive only the final complete lines — the `\r`-terminated in-progress updates would be buffered and overwritten before being flushed. This may make the interception approach cleaner than it looks.

3. **Is there appetite for a bubbletea / terminal-ownership approach in niwa?** The Zig/Rich pattern of owning the terminal is powerful but couples the CLI to a TUI library and requires raw-mode terminal handling. This is a much larger architectural commitment than a `needs_clear` flag.

4. **What happens to niwa warnings on non-TTY (CI/piped) output?** If progress display is suppressed on non-TTY (which is the correct behavior), the current `fmt.Fprintf(os.Stderr, "warning: ...")` approach is already correct for that case. The question is only about TTY sessions.

---

## Summary

The dominant pattern across cargo, npm, tqdm, Rich, and indicatif is "suspend-clear-print-redraw": before printing any warning or error, the progress line is erased (`\r\033[K` or equivalent), the message is written as a normal scrolling line, and the progress bar redraws on the next tick. Errors are never buffered — they interrupt immediately. For niwa, this pattern is straightforward to apply to niwa's own warning/error output, but git subprocess stderr writes directly to the terminal fd and cannot be intercepted without piping git through a goroutine, making concurrent niwa-drawn progress and git-native progress mutually exclusive without that extra plumbing. The biggest open question is whether reading git stderr through a pipe (capturing `\n`-delimited lines, discarding `\r`-terminated progress frames) would cleanly separate git error output from git progress chatter.
