# Lead: How much output does a real setup script produce, and does printing all of it break the apply UX? What do comparable tools do?

## Findings

### 1. There are zero real setup scripts to sample in this workspace's public repos

Exhaustive search across every public repo (`dot-niwa`, `koto`, `niwa`, `shirabe`,
`tsuku`) for `*/scripts/setup/*` returns nothing. No `setup_dir` override appears in
any committed TOML either. niwa's own `scripts/` holds one file, `docker-test.sh`,
which is not a setup script.

So the "real setup script" the design worries about does not exist yet in any repo
niwa itself manages publicly. The volume question has to be answered from the
*class* of work a setup script does, not from a corpus.

The nearest real analog is the one Decision 2 itself names — installing git hooks.
Two public repos carry a `scripts/install-hooks.sh` that is exactly what a
`01-git-hooks.sh` setup script would be:

```
public/koto/scripts/install-hooks.sh   — 9 lines
public/tsuku/scripts/install-hooks.sh  — 9 lines
```

Both are `ln -sfv` plus one `echo`. Measured output: **2 lines**. Decision 2's tidy
sample output is accurate for this class of script.

### 2. Measured volume for the chatty classes — and it is much lower than feared, because niwa always pipes

Measured in this environment, non-TTY (piped) invocation, which is what niwa always
produces (see §3):

| Command | Lines emitted |
|---|---|
| `install-hooks.sh` (real, from a public repo) | 2 |
| `npm install express` (cold) | 4 |
| `pip install requests --no-cache-dir` (cold, fresh venv) | 20 |
| `go mod download -x` (28 modules, cold `GOMODCACHE`) | 139 |
| `go build ./...` / `go vet ./...` (success) | 0 |

The 139-line case is a script that *opts into* verbosity (`-x`). The unadorned
`go mod download` is silent. `go build` on success is silent. This is the general
Unix shape: build and dependency tools are quiet on success and loud on failure,
which is precisely the shape that makes replay-on-failure cheap.

The counter-measurement matters as much: I ran `pip install` a second time under a
pty (`script -qec`) and got the same 20 lines. pip's progress bar is a single
`\r`-redrawn line, so it does not multiply the line count either way — but more
importantly, **pip, npm, cargo and friends all check `isatty` and suppress progress
rendering when piped.** The "dependency install spewing thousands of lines" scenario
in the exploration scope is largely a TTY-mode artifact of watching a tool
interactively; it does not reproduce through a pipe.

The realistic ceiling for an ordinary setup script is therefore tens of lines, not
thousands. The pathological cases are narrow and self-inflicted: `set -x` in the
script, `make` echoing every recipe line, or an explicit `-v`/`-x` flag.

### 3. niwa already forces every setup script into non-TTY mode

`runCmdWithReporter` (`internal/workspace/gitutil.go:93`) does:

```go
pr, pw := io.Pipe()
cmd.Stdout = pw
cmd.Stderr = pw
```

The script's stdout and stderr are an `io.Pipe`, never a terminal, **regardless of
whether niwa itself is on a TTY**. Every script niwa runs already sees
`isatty(1) == false`. Any tool that self-suppresses progress output when piped is
already in its quiet mode before niwa decides anything about routing. This is the
single strongest empirical argument that unconditional pass-through is affordable.

### 4. The design doc that introduced `runCmdWithReporter` says `Log`, not `Status` — in three of four places

`DESIGN-clone-output-ux.md` is the design that created both helpers. It describes
the setup-script path **three times as `reporter.Log`**:

- Components (`:381`): "`runCmdWithReporter`: same pipe pattern, all lines via
  `reporter.Log`, no classifier (setup scripts)"
- Components (`:393`): "`internal/workspace/setup.go` — ... uses `runCmdWithReporter`
  (all lines → `reporter.Log`; no git-specific classifier for arbitrary scripts)"
- Phase 4 deliverables (`:575`): "`runCmdWithReporter`: for non-git subprocesses
  (setup scripts). No classifier — all lines route through `r.Log`."

And **once as `r.Status`**, in the Key Interfaces code stub (`:436`):

```go
// runCmdWithReporter is the general-purpose variant for non-git subprocesses
// (e.g., setup scripts). No line classification — all output through r.Status
// (transient; silent in non-TTY/CI contexts).
```

The implementation followed the one-off stub. `grep` for any rationale — "transient",
"noisy", "noise", "verbose" — across that design turns up nothing about setup
scripts; the only noise argument in the document is about *git's* progress output,
which is why `runGitWithReporter` has a classifier and discards non-diagnostic lines.

So there is no recorded reasoning anywhere for the `Status` choice. Both design docs
that touch this path (`DESIGN-post-clone-scripts.md` Decision 2 and
`DESIGN-clone-output-ux.md` Components/Phase 4) specify durable printing. The current
behavior is unreviewed implementation drift, not a considered position. Whatever the
exploration decides, it is *changing* nothing that was ever argued for.

### 5. Neither README nor any guide documents an example setup script

`grep` across `README.md` and `docs/guides/` finds no worked setup-script example.
The only sample output that exists anywhere is Decision 2's block in
`DESIGN-post-clone-scripts.md:135-143`:

```
Running setup for tsuku...
  [01-git-hooks.sh] Installing git hooks... done.
  [02-install-deps.sh] Installing dependencies... done.
Running setup for koto...
  [01-git-hooks.sh] Warning: failed (exit code 1). Skipping remaining scripts.
Running setup for niwa... (no setup directory)
```

Read carefully, this sample is **not** raw pass-through. `Installing git hooks...
done.` on one line is niwa's rendering of a script that succeeded, not a verbatim
relay of its stdout — and the failure line is entirely niwa's own text, with the
script's output absent. The sample is closer to "per-script progress line, output
suppressed" than to "stream everything". The prose one line above it
("Stdout/stderr: printed to niwa's output, prefixed with the repo name") says the
opposite. **Decision 2 is internally inconsistent**, which is part of why the
implementation could drift without anyone noticing.

The two promises are separable, and only one of them is contested:

- *Per-script progress lines* (`Running setup for tsuku...`, the `[01-git-hooks.sh]`
  prefix, `(no setup directory)`) — promised, never implemented, and nobody disputes
  they should exist. The Security Considerations section's claim that niwa "prints
  each script name before execution" is the same missing feature stated a third time.
- *Raw script stdout/stderr* — promised in prose, contradicted by the sample, and
  the actual open question.

### 6. Prior art: the field splits three ways, and the split tracks who the output is for

**Stream always, verbatim (git hooks).** Git connects client-side hook stdout/stderr
straight to the terminal and does not capture. The one place it deviates is
instructive: when git *does* interpose (parent consuming `stderr` via
`child_process.err = -1`, or `push.recurseSubmodules`-style parallel jobs), it breaks
the hook's own `isatty` detection and has generated real regressions — a Git 2.36.0
regression report titled "pre-commit hooks no longer have stdout/stderr as tty"
exists for exactly this. Git's default is pass-through because a hook is *the user's
own code running on the user's own terminal*; interposing is the thing that causes
bugs.

**Stream always, prefixed (direnv).** direnv relays `.envrc` diagnostics on stderr
with a `direnv: ` prefix on every line, always, with `DIRENV_LOG_FORMAT` as the
escape hatch for silencing. This is the closest published analog to Decision 2's
promise ("printed to niwa's output, prefixed with the repo name") — same mechanism,
same prefix rationale, an opt-out rather than an opt-in.

**Buffer, replay on failure (pre-commit).** pre-commit captures each hook's output
and reproduces it only when the hook fails. Per-hook `verbose: true` in
`.pre-commit-config.yaml` "forces the output of the hook to be printed even when the
hook passes", and `--verbose` does it globally. The stated reason is that a passing
hook's output is noise on a run where the interesting signal is which hook failed.
This is the single closest match to niwa's situation: N independent user-provided
scripts, most of which pass, run as a side effect of a command whose real subject is
something else.

**Background by default, opt-in foreground (npm).** Since npm@7 install-lifecycle
scripts (`preinstall`/`install`/`postinstall`) run in the background; the docs
describe `--foreground-scripts` as sharing "standard input, output, and error with
the main npm process" and warn it "will generally make installs run slower, and be
much noisier, but can be useful for debugging." Two stated reasons — speed and noise
— and note that npm treats the always-stream mode as the *debugging* mode. The
well-documented complaint against npm's default (npm/feedback discussion #592) is
that with background scripts "if an error occurs, the user doesn't know about it" —
which is #239, filed against npm. npm's answer was a flag, not a default change.

**Log-the-command, not the output (Terraform provisioners).** `remote-exec` logs the
commands it runs and relays remote stdout, but the streaming is unreliable enough
that "Remote-exec appeared to hang even though it had not" (hashicorp/terraform
#19557) is a long-lived issue: output stops arriving mid-run and the operator cannot
tell a hang from silence. HashiCorp calls provisioners a "last resort" partly because
`terraform plan` cannot see inside them. This is the cautionary data point *against*
buffering: **buffered output makes a hang indistinguishable from a slow success.**

The axis that actually predicts the choice is not tool category, it is *whose output
it is and how many of them run*. One hook the user invoked deliberately (git, direnv)
→ stream. N hooks running as a side effect of a different command (npm install,
pre-commit run) → capture, replay on failure, flag to override. niwa's setup scripts
are unambiguously in the second group.

### 7. What streaming actually does to the spinner on a TTY

Reading `reporter.go` concretely, the "chatty script destroys the spinner" fear does
not survive contact with the code.

`Log` (`reporter.go:137`) calls `stopSpinner` **only when a spinner goroutine is
running**; `stopSpinner` (`:116`) returns immediately when `r.spinStop == nil`, and
it nils out `spinStop` on the way out. `Status` is what restarts the goroutine.
During `RunSetupScripts` nothing calls `Status` — the main goroutine is blocked in
`cmd.Run()` while the scanner goroutine drains the pipe. So if the lines were routed
to `Log`:

1. First line: `stopSpinner` closes the channel, joins the goroutine, writes
   `\r\033[K`, then prints the line. One teardown.
2. Every subsequent line: `stopSpinner` is a no-op; a plain `Fprintf`.
3. The spinner reappears at the next `Status` call, which is the next repo's
   clone/sync phase in the apply loop.

There is no per-line goroutine churn and no flicker. The visual result is: spinner
vanishes, N permanent lines scroll past, spinner resumes at the next phase — exactly
what already happens today when `runGitWithReporter` routes a git `warning:` line
through `r.Warn`. The cost of streaming on a TTY is scrollback lines, nothing more
exotic.

Two smaller notes from the same read:

- `Log` writes to `r.w` *without* holding `r.mu` (only `doTick` and the
  spinner-lifecycle fields are mutex-protected). Streaming from the scanner goroutine
  is safe at this call site because the main goroutine is parked in `cmd.Run()`, and
  it is the same pattern `runGitWithReporter` already uses. Not a new hazard, but not
  a guarantee the type provides either.
- `Reporter.Writer()` (`:175`) already exists and routes `Write` → `Log`. If the
  decision is "stream always", `runCmdWithReporter` does not strictly need a new
  Reporter method — though the per-line scanner is still wanted for escape-stripping
  and prefixing.

### 8. Buffer-and-replay vs always-stream for niwa's actual caller

The dominant caller is a background agent with no TTY, reading a dispatch log after
the fact. That changes the weight of every argument:

**What streaming buys that buffering does not:** a hanging script is visible. This is
the Terraform `remote-exec` failure mode and it is the only argument for streaming
that survives the non-TTY framing. Under buffering, a script stuck on a network read
produces a silent apply that looks identical to a fast one until it times out or the
operator kills it. Chronological interleaving with other repos' progress lines is
also lost under buffering, though for a per-repo-prefixed line that matters little.

**What buffering buys:** a successful apply stays quiet, and the failed script's
output arrives attached to the failure rather than 400 lines above it. In a dispatch
log the second point is worth more than it looks — the operator greps for `warning:`,
and under streaming the explanation is somewhere upstream of that line, unlabeled.

**The decisive asymmetry** is the one the brief names: #239 is entirely about a
*failing* script's output being lost. There is no reported demand for successful
scripts' output. Nobody has asked to watch `01-git-hooks.sh` say "Git hooks installed
successfully." Streaming pays a noise cost on every apply to serve a need nobody has
articulated, in order to also serve the one that has been.

**niwa already has the mechanism, one function away.** `runGitWithReporter`
(`gitutil.go:53`) collects classified lines into `errorLines` and, on failure,
returns `fmt.Errorf("%w\n%s", runErr, strings.Join(errorLines, "\n"))`. The setup
path's failure already flows into a `DeferWarn("setup script %s/%s failed for %s: %v",
..., sr.Error)` at `apply.go:1590`, formatting the error with `%v`. So if
`runCmdWithReporter` buffered lines and embedded them in the returned error exactly
the way its sibling does, **the script's output would appear inside the deferred
warning with no change to `apply.go` at all.** That is the smallest correct fix, it
reuses an in-repo convention rather than inventing one, and it lands the output at
precisely the place the operator is already looking.

The buffer needs a bound. A tail cap (last ~50 lines, with an elided-count marker) is
the standard answer and keeps a runaway `set -x` script from holding megabytes or
burying the summary. The measured numbers say 50 lines captures the entire output of
every non-pathological case above except `go mod download -x`.

**Recommended position for the design doc**, stated so it is explicit rather than an
undocumented constant:

1. Emit the per-script progress line Decision 2 already promises, unconditionally,
   via `Log`. This is uncontested, it is promised in three places, and it alone fixes
   the "impossible to tell what ran" half of the complaint. It costs one line per
   script.
2. Buffer each script's stdout/stderr to a bounded tail; on failure, embed the tail
   in the returned error the way `runGitWithReporter` does, so it surfaces through the
   existing `DeferWarn`.
3. Provide an explicit always-stream escape hatch (a flag or env var), which is what
   every tool in the buffering camp does — pre-commit's `--verbose`, npm's
   `--foreground-scripts`. This is also the hang-visibility answer: the operator
   debugging a stuck apply reruns with it on.
4. Write the tail cap and the reasoning into `DESIGN-post-clone-scripts.md`, and fix
   Decision 2's internal contradiction between its prose and its sample block while
   there.

### 9. Secrets: printing script output is a genuine new exposure, with an in-repo mitigation

**Does niwa put secrets in the script's environment?** Not deliberately.
`setup.go:104` builds the command as:

```go
cmd := exec.Command(scriptPath)
cmd.Dir = repoDir
```

`cmd.Env` is never set, so Go falls back to `os.Environ()` — the script inherits
niwa's own process environment verbatim. This matches Decision 2's stated contract
("Environment: inherits the niwa process environment") and niwa exports nothing extra.
Resolved vault secrets are materialized to **files** (`.env.local` and configured
secret-output targets, written mode 0600 and registered in `.git/info/exclude`), not
to the process env.

**So can a setup script see secrets? Yes, by two routes.**

- *Inherited env.* Whatever the operator exported is visible. `niwa dispatch` launches
  workers with `cmd.Env = os.Environ()` (`internal/cli/dispatch_launcher.go:93`), so
  an exported API key reaches a dispatched worker and from there any setup script it
  runs.
- *Materialized files.* Step 6.75 (setup scripts) runs **after** Step 6.5
  (materializers) and Step 6.6 (worktree env refresh) in `runPipeline`. The script's
  cwd is the repo root where `.env.local` was just written. A setup script that does
  `set -a; . ./.env.local` — a completely ordinary thing for a setup script to do — has
  the values in its environment, and under `set -x` echoes them.

**Is this a regression?** Yes, in the specific sense that matters. Today those lines
go to `Status`: dropped entirely off-TTY, and on-TTY overwritten and then erased by
`stopSpinner`. Nothing durable is written. Any change that prints script output puts
those lines into dispatch logs, CI logs, and terminal scrollback where they were never
written before. This is a real widening, not a theoretical one, and it applies to
streaming and to replay-on-failure alike — arguably more to replay, since a script
failing partway through `set -x` is exactly when half-expanded secret values land in
the buffer.

**The mitigation already exists and is in scope at the call site.**
`internal/secret/redactor.go` provides a `Redactor` that accumulates resolved secret
fragments and scrubs them from arbitrary strings. `runPipeline` constructs one at
`apply.go:1105` (`redactor := secret.NewRedactor()`) and threads it into `ctx`; the
vault resolver registers every value it resolves. `Scrub` is currently applied to
vault subprocess output (`internal/vault/scrub.go:42`, `infisical/auth.go:116`) and
to error messages (`secret/error.go:35`) — **not** to Reporter output. Since the
`redactor` local is in scope in the same function as the Step 6.75 loop, passing it
into `RunSetupScripts` and scrubbing each line before it reaches `Log` (or the error
buffer) is a small change built entirely on existing primitives.

Two honest limits on that mitigation, which belong in the design's Security
Considerations rather than being glossed:

- `minFragmentLen = 6` (`redactor.go:11`): fragments shorter than 6 bytes are silently
  refused and will never be scrubbed. The design's stated compensating control is that
  such secrets are rejected as a hard error at resolution time.
- The redactor only knows values **niwa itself resolved**. A setup script that fetches
  its own credential from a keychain, a cloud metadata endpoint, or an unmanaged
  `.env` and then echoes it is not covered by anything.

The escape-sequence hazard is already handled: `runCmdWithReporter` calls
`stripEscapes` on every line unconditionally, and `DESIGN-clone-output-ux.md`'s
Security Considerations section explains why ("Strip unconditionally — not just on TTY
paths — to keep log output clean as well"). Prefixing with the repo name additionally
prevents a script from forging niwa's own output lines.

## Implications

- **The volume objection is weaker than the exploration scope assumes.** Because niwa
  always attaches a pipe, every well-behaved tool is already in quiet mode. Measured
  ceiling for ordinary work is tens of lines. "Unconditional pass-through is unusable"
  is not supportable on the evidence; the honest framing is that pass-through is
  *affordable but unwanted*, which is a different and weaker argument.
- **That means the decision has to rest on relevance, not volume.** The case for
  buffering is that successful-script output is noise nobody asked for, not that it is
  voluminous. Writing "we buffer because scripts are chatty" into the design would be
  writing down something the measurements contradict.
- **Streaming on a TTY is mechanically cheap** — one spinner teardown per script, no
  flicker, identical to the existing git-warning path. Spinner interference is not a
  reason to prefer buffering.
- **The per-script progress line should be decoupled from the output question and
  shipped regardless.** It is promised three times across two documents, it is
  uncontested, and it independently fixes the discoverability half of #239.
- **The error-embedding pattern from `runGitWithReporter` makes replay-on-failure a
  near-zero-diff change** that requires no edit to `apply.go` and invents no new
  convention.
- **Any output decision needs a redaction decision attached.** Printing script output
  is a real new exposure surface for materialized secrets, and shipping the visibility
  fix without routing lines through the existing `Redactor` would trade one defect for
  a worse one.

## Surprises

- **Decision 2 contradicts itself.** Its prose promises raw stdout/stderr pass-through;
  its own sample output block shows niwa-rendered per-script lines with the script's
  output nowhere in evidence, and the failure case showing niwa's text only. The
  exploration cannot simply "implement what Decision 2 says" — it has to pick which
  half of Decision 2 to keep.
- **`DESIGN-clone-output-ux.md` specifies `reporter.Log` for setup scripts in three
  places and `r.Status` in one.** The implementation followed the outlier. There is no
  recorded rationale for `Status` anywhere in either design doc. This reframes the fix
  from "reverse a design decision" to "finish implementing the one that was written."
- **niwa forces every setup script into non-TTY mode via `io.Pipe`,** so the
  progress-bar spew that motivates the volume concern is already suppressed by the
  scripts' own tooling before niwa sees a byte.
- **npm's default is the exact defect in #239, filed against npm** (npm/feedback #592:
  background scripts mean "if an error occurs, the user doesn't know about it"), and
  npm's resolution was to add a flag rather than change the default. Worth knowing that
  the buffering camp's answer to this complaint is a documented one.
- **`go build`, `go vet` and bare `go mod download` emit zero lines on success.** The
  quietest possible always-stream policy costs nothing for the most likely setup-script
  workload in a Go workspace.
- **The redactor is already constructed in the same function as the setup-script loop,
  ~475 lines earlier.** The secret-scrubbing mitigation is a parameter pass, not new
  machinery.

## Open Questions

- Does the escape hatch want to be a persistent CLI flag (`--verbose-setup`), a reuse
  of the existing `--no-progress` display-mode plumbing in `internal/cli/root.go`, or
  an env var? The existing DisplayMode propagation suggests reuse, but `--no-progress`
  currently means the opposite thing.
- Where should replayed output land relative to `FlushDeferred`? Embedding it in the
  error puts a multi-line block inside a deferred warning block, which may read badly
  for a 50-line tail. An alternative is a short deferred warning plus the tail printed
  inline at failure time — but inline printing off-TTY reintroduces ordering questions.
- Should the tail cap be lines or bytes? Lines are friendlier to read; bytes bound
  memory against a script emitting one enormous line (`bufio.Scanner` already caps
  token length and would error out, silently truncating — worth checking what
  `runCmdWithReporter` does today when a script emits a >64KB line).
- Should redaction be unconditional, or does scrubbing risk mangling legitimate script
  output that coincidentally contains a resolved value? Given `minFragmentLen = 6` and
  the existing precedent of unconditional scrubbing on vault subprocess output,
  unconditional seems right, but it should be stated.
- Nothing here settles whether a *successful* script's output should ever be reachable
  after the fact (a log file). No caller has asked for it; flagging it as deliberately
  out of scope is probably the right move.

## Summary

No setup scripts exist in any public repo here, so volume was measured by class: a real git-hooks installer emits 2 lines, `npm install` 4, `pip install` 20, and a verbose `go mod download -x` 139 — and because `runCmdWithReporter` always attaches an `io.Pipe`, every script already runs non-TTY with its tooling's progress rendering self-suppressed, so unconditional pass-through is affordable rather than unusable and streaming costs the spinner only one teardown per script, not per line. Prior art splits on who the output belongs to: git and direnv stream one deliberately-invoked hook, while npm (`--foreground-scripts`) and pre-commit (`--verbose`) buffer N side-effect hooks and replay only on failure for noise reasons — putting niwa squarely in the second camp, and `runGitWithReporter`'s existing error-embedding makes replay-on-failure a near-zero-diff change that surfaces through the existing `DeferWarn` untouched. Two things complicate the brief: `DESIGN-clone-output-ux.md` actually specifies `reporter.Log` for setup scripts in three places against `r.Status` in one (the implementation followed the outlier, and no rationale for `Status` exists anywhere), and printing script output is a genuine new exposure for secrets a script can read from the `.env.local` materialized one pipeline step earlier — mitigable by passing the `Redactor` already constructed at `apply.go:1105` into the setup loop.
