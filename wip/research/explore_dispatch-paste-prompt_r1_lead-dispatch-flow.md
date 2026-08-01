# Lead: How does an inline prompt fit dispatch's existing control flow?

## Findings

### 1. The ordered steps of `runDispatch`, and the one slot the prompt can occupy

`runDispatch` (`internal/cli/dispatch.go:137-399`) runs fourteen numbered steps. They
divide cleanly into a **pure-check prefix** and a **side-effecting suffix**, and the
boundary is exactly where an interactive prompt has to sit.

Pure checks, no state touched anywhere:

- **(1) Prompt validation** — `dispatch.go:140-146`. Empty check, then
  `len(prompt) > maxPromptBytes` (128 KB, `dispatch.go:81`).
- **(2) Workspace classification** — `dispatch.go:148-163`. `os.Getwd` +
  `workspace.ClassifyCwd`; an empty `WorkspaceRoot` is a clean error.
- **(2b) Agent check** — `dispatch.go:165-181`. Loads the workspace config and
  refuses when the resolved agent is not Claude.
- **(3) `claude` preflight** — `dispatch.go:183-187`. `lookClaude()`; deliberately
  before any creation "so an absent claude fails with no instance dir and no mapping
  on disk".
- **(4) Name generation** — `dispatch.go:189-207`. `crypto/rand` suffix; pure.

First side effects:

- **(5) Opportunistic reap** — `dispatch.go:211`. `reapOpportunisticly(workspaceRoot)`
  sweeps *other* instances; unrelated to this dispatch but the first write to disk.
- **(6) Provision** — `dispatch.go:216-221`. Creates the instance directory. This is
  the first thing that belongs to *this* dispatch.
- **(7) Arm rollback** — `dispatch.go:229-234`. `success := false` plus a deferred
  `destroyInstanceFunc(instancePath)`. Everything from here on is inside the
  rollback window.
- **(8)** marker write, **(9/9a-9d)** global-config load, model resolution, argv build,
  remote-control injection, keep-alive prepend — `dispatch.go:236-332`.
- **(9-launch)** `dispatchLaunch` — `dispatch.go:334`.
- **(10)** session-id capture — `dispatch.go:349`.
- **(11)** mapping write — `dispatch.go:366`.
- **(12)** marker removal, `success = true` — `dispatch.go:372-373`.
- **(13)** hints, **(14)** attach — `dispatch.go:379-396`.

**Where the prompt goes: after (3), before (5).** Two independent reasons.

*Fail fast before taking the terminal.* Steps (2), (2b), and (3) are the three ways a
dispatch dies for environmental reasons — not in a workspace, wrong agent, no `claude`.
Making a user paste 200 lines of stack trace and only then telling them "not inside a
niwa workspace" is the worst possible ordering. Run all three first, then prompt.

*Abort must land outside the rollback window.* The deferred rollback does not exist
until `dispatch.go:229`. If the prompt sits before step (5), an abort — Ctrl-C, empty
submit, EOF — returns from `runDispatch` having created literally nothing: no instance
directory, no marker, no mapping, no launched process, and not even the opportunistic
reap sweep. The abort story requires no cleanup reasoning at all because there is
nothing to clean up. That is a much stronger position than "the rollback covers it".

The consequence for step (1): prompt validation has to split. When the prompt comes
from `args[0]` it should still be validated at step (1) (fail before touching the
terminal, and before the environment checks, exactly as today). When it comes from the
interactive capture, the same empty/length validation has to re-run immediately after
capture. Factoring `validatePrompt(string) error` out of `dispatch.go:140-146` and
calling it on both paths is the minimal change.

### 2. Why ordering matters *specifically* given the rollback semantics

The rollback is a Go `defer` guarded by a `success` flag (`dispatch.go:229-234`), and
the comment at `dispatch.go:225-228` is explicit that "a Go defer does not run on
SIGKILL". It does not run on an unhandled SIGINT either — Go's default SIGINT
disposition terminates the process without unwinding.

That matters because Ctrl-C at a prompt is the expected abort gesture:

- If the prompt uses **raw mode** (which bracketed paste requires anyway, since
  `ISIG` is off and the terminal stops translating `^C`), Ctrl-C arrives as byte
  `0x03` in our read loop. We handle it in-band, defers run normally. This is exactly
  what `internal/tui/picker.go:192-194` (`isCtrlC`) already does.
- If the prompt uses a **canonical line read** (a `bufio.Reader` over stdin, the
  `promptBootstrap` pattern at `internal/cli/init.go:312-330`), Ctrl-C generates a
  real SIGINT and the process dies with no defers.

So a prompt placed *after* provisioning would orphan the instance on any canonical-mode
Ctrl-C, leaving it to the name+TTL reaper backstop (`dispatch.go:59-68`). Placed before
provisioning, neither mode can orphan anything. The placement makes the raw-vs-canonical
choice irrelevant to correctness — worth having, since the raw-mode path is the one
where a *different* signal (SIGTERM, SIGHUP on a closed terminal) leaves the user's
terminal in raw mode with no restore. Nothing in this repo installs a
terminal-restoring signal handler; `internal/tui/picker.go:86` restores by `defer`
only, and the sole `signal.Notify` in the tree
(`internal/cli/sessionattach/supervise.go:59`) is for a different purpose.

### 3. The argument contract

Today: `Args: cobra.ExactArgs(1)` (`dispatch.go:131`), `Use: "dispatch <prompt>"`
(`dispatch.go:113`), with `SilenceUsage: true` (`dispatch.go:133`) so arg-count errors
print bare.

**Recommendation: `cobra.MaximumNArgs(1)` plus an explicit `-` sentinel, no new flags.**

- **Zero args + TTY → open the prompt.** The natural read of `niwa dispatch` with
  nothing to dispatch.
- **Zero args + non-TTY → clean error.** Never a read that could block. See §6.
- **One arg → byte-identical to today**, including an explicitly-passed empty string
  staying an error (`dispatch_test.go:214-227` pins this). An explicit `""` is a
  user mistake, not a request to open a prompt; keep them distinct.
- **Upper bound stays 1.** This is load-bearing and easy to lose: `ExactArgs(1)`
  currently catches the unquoted multi-word prompt (`niwa dispatch fix the bug`),
  which is a common typo. `MaximumNArgs(1)` keeps that. But cobra's message —
  "accepts at most 1 arg(s), received 3" — is now actively misleading, since it
  implies zero args is fine without saying what zero args *does*. A custom `Args`
  func should say: quote the prompt, or run `niwa dispatch` with no arguments to open
  the paste prompt.
- **`-` as the explicit stdin sentinel.** pflag treats a lone `-` as a positional, not
  a flag, so `niwa dispatch -` parses today with no special handling. It reads stdin to
  EOF. This is worth having *even though scripted piping is out of scope*, because it
  is the thing that lets zero-args stay purely interactive: without it, the tempting
  design is "zero args + non-TTY = read the pipe", which hangs when stdin is an
  inherited-but-idle fd (a hook, a supervisor). An explicit sentinel removes the
  ambiguity for the cost of about four lines.

Rejected: **`--prompt <text>`** duplicates the positional and forces a precedence rule
between two channels that say the same thing. **`--edit`** contradicts the scope
decision against `$EDITOR`.

The `Use:` string becomes `dispatch [prompt]`, and the `Long` text
(`dispatch.go:115-130`) needs a paragraph on the interactive path.

### 4. The `claude attach` handoff at `dispatch.go:100-110`

`dispatchAttach` hands `os.Stdin`, `os.Stdout`, `os.Stderr` straight to
`claude attach <short-id>` and blocks on `cmd.Run()`. It is step (14), the last thing
`runDispatch` does, and it runs only when `--detach` is absent.

**The good news, and it is the strongest argument for the inline-prompt design.**
Reading the prompt from the *terminal* leaves stdin a TTY afterwards, so the default
non-detach attach still works. The existing `niwa dispatch -d "$(cat)"` workaround
cannot do this — a consumed pipe and an attach cannot share stdin, which is precisely
why that workaround forces `-d`. An inline capture removes the `-d` requirement
entirely. Same terminal, same fd, handed over intact.

**What must be restored before the handoff.** Since capture happens at step ~3 and
attach at step 14, a function-scoped `defer` inside the capture helper satisfies all of
this naturally — the capture function returns long before attach:

1. **Raw mode** — `term.Restore(fd, oldState)`, the `internal/tui/picker.go:82-87`
   pattern. Non-negotiable: `claude` sets its own modes but restores to whatever it
   inherited on exit, so a raw-mode leak survives the whole attach and lands in the
   user's shell.
2. **Bracketed paste** — if we emit `CSI ?2004h`, we must emit `CSI ?2004l`. bash 4.4+
   and zsh 5.1+ re-assert their own bracketed-paste state at each prompt so this
   self-heals in the common `--detach` case, but it does not self-heal for `claude
   attach`, a pager, or `sh`.
3. **Cursor visibility** — if the prompt hides the cursor the way
   `internal/tui/picker.go:91-92` does, show it again.

**Not a problem:** the shell wrapper. `internal/cli/shell_init.go:52-71` routes
`dispatch` through the `*)` → `command niwa "$@"` branch — no `__niwa_cd_wrap`, no
command substitution, no output capture. All three streams are the real terminal.

### 5. Other consumers of the prompt string and of stdin

**`internal/cli/watch.go` bypasses the CLI command entirely.** Both
`dispatchLaunch(ctx, instancePath, prompt, passthrough, nil)` call sites —
`watch.go:579` (resume) and `watch.go:826` (fresh stage) — build their prompt in-process
via `watch.BuildResumePrompt` / `watch.BuildReviewPrompt` and call the launcher seam
directly. They never touch `runDispatch`, `Args`, or stdin. An arg-contract change
cannot reach them.

**The keep-alive prepend is the one real interaction.** `dispatch.go:325-332`:

```go
if resolveDispatchKeepAlive(dispatchKeepAlive, hostGlobal, inst) {
    if remoteControlEnabled(rcInjected, inst) {
        prompt = keepAliveArmingInstruction + prompt
```

`keepAliveArmingInstruction` (`dispatch_keepalive.go:33-35`) is a fixed ~900-byte
constant, prepended **after** the size validation at step (1). The comment at
`dispatch.go:311-313` justifies this as "well inside the conservative maxPromptBytes
margin". That reasoning holds for a hand-typed argv prompt; it is thinner once a paste
can legitimately approach 128 KB. Not a new bug — but the paste path is what makes the
edge reachable, so post-prepend re-validation is worth adding.

**Everything else is inert.** `dispatch_launcher.go:31-34` re-rejects an empty prompt
(defense in depth). `buildClaudeBgArgs` (`dispatch_launcher.go:66-72`) puts the prompt
as the final single argv element, so multiline content is *already* safe once captured —
the D8 no-shell-interpolation guard needs no change. `dispatch_capture.go`,
`dispatch_model.go`, and `dispatch_remotecontrol.go` never see the prompt and never
read stdin.

**Test surface.** `runDispatch(cmd, []string{prompt})` is called ~40 times across
`dispatch_test.go` (19), `dispatch_wiring_keepalive_test.go` (11),
`dispatch_wiring_remotecontrol_test.go` (6), `dispatch_model_test.go` (4), via the
`runDispatchCmd` helper at `dispatch_test.go:165-177`. All pass exactly one arg, so all
keep working if the capture sits behind a package-level seam variable in the same style
as `dispatchCapture` (`dispatch.go:93`) and `dispatchAttach` (`dispatch.go:100`).

### 6. The non-TTY case

**Requirement: zero args + non-TTY must be a clean error, never a read.** Reading stdin
to EOF on a non-TTY is the design that hangs — not on a pipe that closes, but on an
inherited-and-idle fd (a hook whose parent holds stdin open, a supervisor, a
self-dispatching worker). The `-` sentinel gives that case an explicit opt-in.

The existing gate is `IsStdinTTY` (`internal/cli/prompt.go:26-28`), already used at
`destroy.go:116`, `destroy.go:239`, `destroy.go:325`, and `init.go:290`. The error
wording should follow the established shape at `destroy.go:243` and `init.go:293` —
state the condition and name the fix.

**Programmatic invocations of `niwa dispatch` in this repo, all of which pass a
positional and are therefore unaffected:**

- **The `/dispatch` root skill**, `internal/workspace/rootskills/dispatch/SKILL.md`,
  step 3: `niwa dispatch "Read <abs-path-to-brief> ..." --name "<slug>" --detach`.
  Always quoted positional, always `--detach`, always from an agent with non-TTY stdin.
- **Functional tests**, `test/functional/features/dispatch.feature` — e.g. line 36
  `When I run "niwa dispatch hello-task --detach" from the workspace root`. The runner
  leaves `cmd.Stdin` unset (`/dev/null`).
- **`test/live/dispatch_live_test.go`** — same shape.
- No hooks or `scripts/` invoke dispatch.

**There is already a PTY test seam.** `iRunUnderPTYWithInput`
(`test/functional/steps_init_bootstrap_test.go:143-200`) drives the niwa binary under
util-linux `script -q -c ... /dev/null` so `IsStdinTTY()` returns true and input is fed
through the pty. Its header notes that `github.com/creack/pty` was considered and
rejected. This is directly reusable for paste-prompt scenarios, including feeding raw
`ESC[200~ ... ESC[201~` bracketed-paste bytes.

### 7. Docs that document dispatch's CLI surface

- **`README.md:130`** — the command table row: ``niwa dispatch "<task>" [--name <slug>]
  [--model <model>] [--detach]``.
- **`internal/cli/dispatch.go:113-130`** — the `Use` and `Long` strings, which are the
  `--help` output and the real user-facing contract.
- **`docs/guides/session-keep-alive.md:18`** — `niwa dispatch --keep-alive <prompt>`.
- **`docs/designs/current/DESIGN-instance-dispatch.md:343-365`** — the Decision Outcome
  narrative ("`niwa dispatch <prompt>` resolves the enclosing workspace root...") and the
  Solution Architecture bullet at :363-366 ("Cobra command; positional prompt; `--label`,
  `--model`, ... flags"). A new design doc supersedes rather than edits this, but the
  ordered-steps narrative at :343-357 is the thing a reader will compare against.
- **`internal/workspace/rootskills/dispatch/SKILL.md`** — should gain an explicit note
  that agents keep passing the positional and must **not** try the interactive form.
  Without it, a future agent reads the new `--help`, tries zero-args, and gets the
  non-TTY error. Its existing "Don't paste giant context into the prompt" caution
  (Cautions section) is also in direct tension with this feature — see Open Questions.
- **`docs/guides/remote-control-on-dispatch.md`** and
  **`docs/guides/workspace-config-sources.md:662-680`** mention `niwa dispatch` but
  never its argument shape; no change needed.

## Implications

**The placement question has one answer, and it makes several downstream questions
disappear.** Prompt between step (3) and step (5) — after every environment check, before
any creation. Abort then needs no rollback reasoning, the raw-vs-canonical Ctrl-C
distinction stops being a correctness issue, and the user never types into a dispatch
that was doomed before they started.

**The inline design is strictly better than the `$(cat)` workaround on the attach
axis, not just the ceremony axis.** Because capture reads the terminal rather than
consuming a pipe, stdin survives as a TTY and the default attach at `dispatch.go:391-396`
works unchanged. The workaround's forced `-d` was never incidental — it was structural.
This should be stated as a design goal, not discovered later.

**Nothing new is needed to build it.** `golang.org/x/term` is already a dependency;
`IsStdinTTY` (`prompt.go:26`), the raw-mode + escape-sequence read loop
(`tui/picker.go:75-122`), the Ctrl-C → `ErrCanceled` → "Canceled." convention
(`tui/picker.go:30`, `destroy.go:252-257`), and a PTY test harness
(`steps_init_bootstrap_test.go:143`) all exist. The missing piece is only a
multiline, paste-aware read loop.

**But it does not belong in `internal/tui/picker.go`.** That file's header
(`picker.go:6-14`) declares it a byte-for-byte vendored copy of
`tsukumogami/tsuku@c8f58101` with a standing "mirror the change in the other or
document the divergence" contract. A paste widget added there creates an
unmirrorable divergence. New file.

**The arg change is a two-line diff with a ~40-call-site blast radius that is entirely
avoidable** if the capture goes behind a package-level seam variable like the two that
already exist.

## Surprises

1. **`internal/cli/watch.go` calls `dispatchLaunch` directly** (`:579`, `:826`), not
   the cobra command. The obvious worry — "changing dispatch's arg contract breaks the
   PR-review watcher" — is unfounded. The launcher seam is the shared surface; the CLI
   command is not.

2. **The keep-alive prepend mutates the prompt *after* size validation**
   (`dispatch.go:327` vs `dispatch.go:144`). The in-code justification explicitly
   depends on prompts being small. A paste-sized prompt undermines that assumption.

3. **`internal/tui/picker.go` is vendored from tsuku with a sync contract** — a
   constraint on where the new widget can live, invisible unless you read the package
   header.

4. **The `/dispatch` skill's Cautions section already says "Don't paste giant context
   into the prompt. Put it in the brief file; the prompt just points at it."** That is
   the *opposite* of what this feature optimizes for. Both can be right — the skill
   addresses an agent synthesizing a brief, this feature addresses a human pasting a log
   they cannot summarize — but the design should say so explicitly rather than leave two
   contradictory pieces of guidance in the tree.

5. **No signal handler anywhere restores terminal state.** `picker.go:86` relies on
   `defer` alone. A raw-mode prompt inherits that gap for SIGTERM/SIGHUP.

## Open Questions

- **What happens when a paste exceeds `maxPromptBytes` (128 KB)?** Today's message —
  "shorten it rather than relying on truncation" (`dispatch.go:145`) — is fine for an
  argv prompt and hostile after a 300-line paste the user cannot get back. Options:
  truncate with confirmation, or **spill to a file under `.niwa/dispatch-briefs/` and
  pass a pointer in the prompt**, which is exactly the pattern the `/dispatch` skill
  already prescribes (SKILL.md step 2) and which sidesteps ARG_MAX entirely. This
  deserves a decision; it may be the more interesting design than the capture mechanism.

- **Should the keep-alive prepend be counted against the limit** via a post-prepend
  re-validation, or is the ~900 bytes a rounding error worth ignoring?

- **Is `-` worth shipping** given scripted piping is explicitly not a design driver? My
  read is yes, as the cheap thing that keeps zero-args purely interactive and removes
  every hang scenario — but it is a scope call.

- **Should the interactive path be reachable at all when `--detach` is set?** They
  compose fine (capture, then skip attach), but "paste a log, then get dropped back to
  the shell" may or may not be a mode anyone wants.

- **Terminal restore on SIGTERM/SIGHUP mid-prompt** — accept the existing
  picker-level gap, or add a handler for the raw-mode window? The prompt is short-lived,
  but "closed the terminal tab mid-paste and my next shell is raw" is a nasty failure.

## Summary

An inline prompt has exactly one correct slot in `runDispatch` — after the workspace,
agent, and `claude` preflight checks (`dispatch.go:148-187`) and before the first
creation at `dispatch.go:216` — which makes abort a literal no-op outside the deferred
rollback window and fails fast before the user types anything. Because the capture reads
the terminal rather than a pipe, stdin stays a TTY and the default `claude attach`
handoff at `dispatch.go:100-110` keeps working, so the design removes the `--detach`
requirement that the `$(cat)` workaround structurally imposed — provided raw mode,
bracketed paste, and cursor state are all restored by a function-scoped defer in the
capture helper. The biggest open question is not capture mechanics but size: a real
pasted log can approach or exceed the 128 KB `maxPromptBytes` cap, and the current
"shorten it" error would discard the paste, which argues for spilling large captures to
a brief file and passing a pointer instead.
