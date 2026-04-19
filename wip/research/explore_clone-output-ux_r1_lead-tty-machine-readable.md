# Lead: TTY detection and machine-readable mode conventions

## Findings

### isatty / TTY detection

TTY detection is the most universally applied heuristic in CLI tooling. Nearly every tool that displays progress bars, spinners, or ANSI color checks `isatty()` (or the platform equivalent) on stdout/stderr before enabling those features. The pattern is: if the file descriptor is not attached to a terminal, interactive output is suppressed automatically.

**What it controls:** Progress bars, spinners, inline-updating output, and sometimes color. Essentially anything that relies on cursor movement or carriage-return rewriting.

**Who does this:** curl (suppresses progress when stdout is piped), wget, npm (progress defaults off unless both stderr and stdout are TTYs and not in CI), cargo (no progress bar on non-TTY), git (no progress output when stderr is not a terminal), ripgrep, fd, bat.

**Limitation:** TTY detection is not foolproof. A process running under a pseudo-TTY (e.g., script(1), expect, a CI runner that allocates a PTY) will appear interactive even though a human isn't watching. Conversely, a developer may pipe output to `tee` for logging, losing the TTY even though they are present. TTY detection handles the common case well but is a heuristic, not a guarantee.

**Verdict:** Necessary but not by itself sufficient. It is the right default behavior, but it cannot replace an explicit opt-in for consumers who need guaranteed stable output (scripts, automation pipelines, AI agents).

---

### NO_COLOR

The NO_COLOR standard (no-color.org, proposed 2017) specifies that any non-empty value of the `NO_COLOR` environment variable must suppress ANSI color codes. It has over 300 software implementations as of 2026, including git, cargo, ripgrep, npm, bat, fzf, jq, Homebrew, and most major color libraries (Chalk, Rich, Colorette, crossterm).

**What it controls:** ANSI color escape sequences only. Explicitly excluded: bold, underline, italic, and other non-color text attributes. Progress bars, spinners, and all other interactive output are outside its scope.

**Complementary variable:** `FORCE_COLOR` (force-color.org) overrides TTY detection in the other direction, forcing color even when stdout is piped. Some tools also check `CLICOLOR=0` (macOS convention) and `CLICOLOR_FORCE`.

**Verdict:** Widely adopted and worth respecting. Does not cover progress suppression — a tool that only checks NO_COLOR still shows spinners in CI.

---

### TERM=dumb

Setting `TERM=dumb` is a long-standing Unix convention indicating that the terminal is incapable of cursor movement or ANSI sequences. Many tools check this:

- cargo: disables its progress bar when `TERM=dumb`
- clig.dev guidelines explicitly list `TERM=dumb` as a condition for suppressing colors
- Various curses-based programs fall back to plain output

**What it controls:** Color and, in tools that check it, progress bars. Less standardized than NO_COLOR for color, but more likely to suppress progress indicators.

**Verdict:** Respected by a meaningful subset of tools, particularly those in the Rust/systems space. Less universal than NO_COLOR for color. Worth checking alongside NO_COLOR and isatty, but not sufficient on its own.

---

### CI environment variable

Many CI platforms (GitHub Actions, CircleCI, Travis CI, Jenkins, GitLab CI) set `CI=true` in their environments. A number of tools use this as a signal to change behavior:

- npm: its progress bar logic explicitly checks for CI. When `CI` is set, progress is off by default regardless of TTY state.
- Create React App / React Scripts: treats `CI=true` as a flag to make warnings into errors, which is a different behavioral axis.
- ora (Node.js spinner library): checks for CI to suppress spinners.
- npm published `ci-detect` specifically for detecting CI environments across platforms.

**What it controls:** Varies by tool — most use it to suppress progress/spinners. Some use it to change error-handling behavior (warnings become errors).

**Scope problem:** `CI` is not a formal standard. It's a de facto convention. Tools like Gemini CLI have had bugs where any `CI_*` prefixed variable (e.g., a user's `CI_TOKEN`) incorrectly triggered non-interactive mode. Terraform chose tool-specific variables (`TF_IN_AUTOMATION`) for this reason, to avoid false positives.

**Verdict:** Widely adopted in the JavaScript/Node ecosystem and many newer CLIs. Worth checking as a secondary signal after TTY detection. Not reliable enough to be the sole mechanism. Tool-specific env vars (like `TF_IN_AUTOMATION`) are a more precise alternative when false positives matter.

---

### --porcelain flag (git)

Git introduced `--porcelain` on `git status` and related commands to provide output that is guaranteed stable across versions and unaffected by user configuration (no colors, no relative paths, no locale-sensitive text). Version 2 (`--porcelain=v2`) adds more structured fields.

**What it controls:** Output format only — switches to a compact, parseable, stable line format. Does not suppress progress output; git handles that separately via TTY detection.

**Adoption:** The naming convention has some followers — asdf proposed adding `--porcelain` to all its commands. But it has not become a universal CLI standard. Most tools that followed git's intent chose `--json` instead, which provides the same stability guarantee with a more universally parseable format.

**Verdict:** The concept (stable, script-safe output format) is important and well-adopted. The specific `--porcelain` flag name is git-specific. For new tools, `--json` has become the preferred expression of the same intent.

---

### --plain flag

The `--plain` flag is documented in the Command Line Interface Guidelines (clig.dev) as a solution for when human-readable formatting (multi-line table cells, aligned columns) breaks line-oriented parsing. `--plain` switches to flat tabular text suitable for grep/awk.

**What it controls:** Output formatting — disables decorative alignment and multi-line cells, produces one record per line.

**Who uses it:** The flag is recommended in clig.dev guidelines, but specific widespread adoption in major tools is limited. LaunchDarkly's CLI uses it. Some tools use `--no-headers` as a related concept.

**Verdict:** Useful for table-heavy output but narrower than `--json`. Not a widely adopted standard — more of a convention from style guides. If niwa's output is not table-heavy, `--plain` is not the right fit.

---

### --no-progress flag

This flag explicitly suppresses progress output and is used by npm (`--no-progress`), yarn (requested in issues), and pip. Cargo does not have a dedicated `--no-progress` flag; instead it uses `--quiet` or `term.progress = "never"` in config.

**What it controls:** Progress bars and spinners only.

**Verdict:** Useful as a narrow escape hatch for users who want progress suppressed without affecting other output. Not a universal standard — naming varies (`--quiet`, `--no-progress`, `--silent`). If niwa adds inline progress, `--no-progress` or `--quiet` covers this use case, but it is separate from the machine-readable output question.

---

### --json flag

Not in the original investigation scope but emerged as the de facto modern convention. clig.dev, openstatus, Heroku style guide, and the "Rewrite Your CLI for Agents" pattern all recommend `--json` as the primary machine-readable flag. GitHub CLI uses `--json` on most commands. AWS CLI uses `--output json`. Tools that implement `--json` emit structured, schema-stable data to stdout, usually with no color or progress, and human-readable output moves to stderr.

**Verdict:** The strongest emerging standard for machine-readable output. More explicit than TTY detection, more structured than `--plain`, more universally understood than `--porcelain`.

---

### POSIX / formal standards

POSIX does not specify when a CLI should suppress progress indicators. POSIX 1.4 leaves diagnostic message format unspecified for most utilities and only requires that diagnostics go to stderr when indicating an error. There is no formal XDG or POSIX specification for progress suppression. The conventions above are community-driven, not standardized.

---

## Implications

For niwa's design, the research points to a layered approach:

1. **TTY detection as the default gate.** isatty() on stderr (where progress output goes) should be the first check. Progress indicators, spinners, and inline-updating output must never appear when stderr is not a TTY. This is the minimum baseline expected by every Unix tool, CI runner, and script environment.

2. **NO_COLOR for color only.** Respecting NO_COLOR (and TERM=dumb as a secondary check) is low-cost and widely expected. These should suppress ANSI color codes in all output, not just progress.

3. **CI env var as an additional signal.** Checking `CI=true` costs nothing and suppresses progress for users in common CI environments where a PTY happens to be allocated. It should not be the primary mechanism but is a reasonable belt-and-suspenders addition.

4. **An explicit flag for machine-readable consumers.** TTY detection gets the common cases right but fails for: automation that allocates a PTY, scripts that call niwa and want guaranteed stable output, and AI agents. An explicit `--json` or `--no-progress` flag gives those consumers a reliable opt-in. `--json` is the right choice if niwa's machine-readable consumers want structured data; `--no-progress` is the right choice if they just want to suppress inline updates while keeping human-readable text output.

5. **`--porcelain` is not the right choice for niwa.** The flag name is git-specific and carries baggage. niwa's output is not git-plumbing-style; `--json` or a simpler flag is cleaner.

The key design tension is between progress display, error visibility, and machine-readable output. The solution used by tools like npm and cargo is: progress goes to stderr (TTY-gated), errors always go to stderr (never suppressed), structured results go to stdout (optionally JSON). niwa should consider the same separation.

---

## Surprises

- **NO_COLOR is color-only, full stop.** Many people assume it suppresses all decorative output including spinners and progress bars. It does not. A tool that only respects NO_COLOR will still show progress bars in CI.

- **TERM=dumb actually does suppress progress in cargo**, not just color. This makes it more useful for niwa's problem than NO_COLOR alone.

- **npm's progress logic is a conjunction of three conditions**: not in CI AND stderr is TTY AND stdout is TTY. All three must be true for progress to appear. This is stricter than what most tools do.

- **`CI` env var false-positive problem is real.** Gemini CLI had a bug where any `CI_*`-prefixed variable triggered non-interactive mode. This is a good reason to not treat `CI` as a hard override but rather as a soft hint.

- **`--json` has largely displaced `--porcelain` as the machine-readable convention** in tools created after ~2015. The porcelain pattern was important but is now mostly a git-ism.

- **No formal standard exists.** Despite the widespread convergence on these patterns, there is no POSIX, XDG, or other formal specification. Everything is community convention.

---

## Open Questions

1. What exactly does niwa produce that a machine-readable consumer needs? If it's just "did this succeed and what repos were cloned," `--json` is overkill compared to clean exit codes plus stable stderr. If consumers need repo states, errors per repo, or structured progress events, `--json` becomes worthwhile.

2. Should niwa's progress output go to stderr (standard for progress) or to stdout? This affects whether piping `niwa clone ... | grep` silently swallows progress.

3. How does niwa handle the case where stderr is a TTY but stdout is piped (e.g., `niwa apply 2>&1 | tee log.txt`)? TTY detection on stderr would still show progress, but it would be garbled in the log file.

4. Is there an existing Go library in the niwa codebase for TTY detection? The `mattn/go-isatty` and `golang.org/x/term` packages are the standard Go options.

5. Would niwa benefit from a `GH_PROMPT_DISABLED`-style env var (e.g., `NIWA_NO_PROGRESS`) to allow per-project or per-user suppression without requiring a flag on every invocation?

---

## Summary

TTY detection via isatty() is the necessary baseline — virtually every major CLI tool gates progress and interactive output on it — but it is not sufficient alone because PTY allocation in CI, piping through tee, and scripted automation all break the assumption. The strongest modern pattern layers TTY detection with NO_COLOR (for color), optional CI env var checking (as a soft hint, not a hard override), and an explicit `--json` or `--no-progress` flag for consumers who need deterministic behavior regardless of terminal state. For niwa specifically, the most important decision is whether machine-readable consumers need structured data (favoring `--json`) or just want progress suppressed (favoring `--no-progress` or `--quiet`), since these point to different flag designs.
