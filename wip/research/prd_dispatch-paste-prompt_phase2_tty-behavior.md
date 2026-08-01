# Lead: What should happen when the terminal cannot carry an interactive capture, and when stdin is not a TTY?

## Findings

### 1. niwa's existing conventions: the house pattern is refuse-with-guidance, with zero exceptions

Every terminal check in the codebase, in one list (`grep IsTerminal` over `internal/`, excluding tests):

| Site | What it gates | Which fd |
|---|---|---|
| `internal/cli/prompt.go:26-28` | the shared `IsStdinTTY` seam | stdin |
| `internal/tui/picker.go:44-46` | `tui.IsAvailable()`, the picker's render gate | **stderr** |
| `internal/tui/picker.go:81` | raw-mode entry inside `pick` | **stderr** |
| `internal/workspace/reporter.go:39-45` | status-line animation vs append-only output | the writer passed in |
| `internal/cli/apply.go:152`, `internal/cli/create.go:199` | same, wired to stderr and `--no-progress` | stderr |

There are exactly four places that gate *input* on a TTY, and all four refuse:

- `internal/cli/init.go:290-297` — non-TTY with neither `--bootstrap` nor `--no-bootstrap` fails fast with the exact string `remote has no .niwa/workspace.toml and stdin is not a terminal; re-run with --bootstrap to scaffold`, exit 4. The TTY branch (`init.go:299-300`) calls `promptBootstrap`, and the helper's doc comment at `init.go:309-312` states the seam explicitly: *"The TTY-vs-non-TTY decision belongs to the caller — this helper assumes the caller has already gated on IsStdinTTY."*
- `internal/cli/destroy.go:116-118` — unpushed work in a named instance: `instance has unpushed work and stdin is not a terminal; aborting (resolve unpushed work, or use --force to destroy without confirmation)`.
- `internal/cli/destroy.go:325-327` — same for a workspace wipe: `workspace has unpushed work and stdin is not a terminal; aborting (resolve unpushed work or run from a terminal to confirm)`.
- `internal/cli/destroy.go:239-245` — the picker. This is the only site that gates on **two** conditions: `if !IsStdinTTY() || !tui.IsAvailable()`. It first prints the list it *would* have offered (`destroy.go:240-243`) and then refuses with `no instance specified and not running in a terminal; pass an instance name or use --force to wipe the workspace`.

Three properties generalize from this set:

1. **Refuse, never fall back and never default.** There is no site in niwa that proceeds with an assumed answer when it cannot ask. The one place a default exists (`promptBootstrap` at `init.go:331` treats bare Enter as `Y`) is a default *within* an interactive prompt, not a substitute for one.
2. **The refusal names the flag that makes the invocation work.** All four strings end in a concrete escape hatch: `--bootstrap`, `--force`, `pass an instance name`.
3. **When the interactive surface would have shown information, print that information before refusing.** `destroy.go:240-243` prints the instance list; `destroy.go:114` and `destroy.go:323` print the full unpushed-work scan. The non-TTY caller is not left worse off than the TTY one — only the choice is withheld.

Two details worth carrying into the PRD:

- **Which stream.** niwa's interactive surfaces render to **stderr**, not stdout: the picker writes to `os.Stderr` (`picker.go:68`), `ReadConfirmation` is called with `cmd.ErrOrStderr()` (`destroy.go:120`, `destroy.go:329`), and `promptBootstrap` gets `cmd.ErrOrStderr()` (`init.go:300`). `PRD-init-bootstrap-empty-source.md:353` states the rule directly — prompt text belongs on stderr. Meanwhile `runDispatch`'s success output goes to **stdout** (`dispatch.go:379-384`). So the capture's UI belongs on stderr, and its availability check should be stdin-TTY **and** stderr-TTY (the `destroy.go:239` shape), never stdout — `niwa dispatch > log.txt` must still be able to capture.
- **A raw-mode quirk not to copy.** `picker.go:79-88` calls `term.MakeRaw` on the **stderr** fd while reading from `os.Stdin` (`picker.go:68`). This works only because stdin and stderr normally point at the same tty device. A paste capture should set raw mode on stdin's fd, which is the fd it actually reads.

Finally, `--no-progress` (`root.go:60-61`) is niwa's existing "don't do terminal-fancy things" override, but it is output-only and threads into the Reporter, not into any input path. There is no `--no-input`/`--no-prompt` equivalent today.

### 2. Programmatic callers of `niwa dispatch`: all of them pass the prompt, and the one in-repo caller bypasses the command entirely

Exhaustive sweep (`grep -rn "niwa dispatch"` plus a Go-level grep for `dispatchCmd`/`runDispatch`/`dispatchLaunch`):

| Caller | Location | Passes a prompt? | Would it reach an interactive capture? |
|---|---|---|---|
| `niwa watch` — fresh review stage | `internal/cli/watch.go:826` | yes, `watch.BuildReviewPrompt(...)` | **No.** It calls `dispatchLaunch` (the package-level launcher seam, `dispatch_launcher.go:14`) directly. It never constructs a cobra invocation, never parses args, and never enters `runDispatch`. |
| `niwa watch` — context-preserving continuation | `internal/cli/watch.go:579` | yes, `watch.BuildResumePrompt(...)` | No, same reason. |
| The `/dispatch` root skill | `internal/workspace/rootskills/dispatch/SKILL.md:86-88` | yes — a prompt pointing at the brief file, plus `--name` and `--detach` | No. The skill's whole design is "write the brief to a file, pass a short pointer as the prompt" (`SKILL.md:68-88`, and the "Don't paste giant context into the prompt" caution at `SKILL.md:114`). |
| Functional suite — `dispatch.feature` | `test/functional/features/dispatch.feature:36,55,73,92` etc. | yes, always a bare token (`hello-task`, `model-task`, `doomed-task`, `reap-me`) | No. |
| Functional suite — `codex-agent.feature:56` | `niwa dispatch some-task --detach` | yes | No. |
| `workspace-config-sources.feature:82-99` | exercises brief survival across a config refresh; does not run `niwa dispatch` itself | n/a | No. |
| README | `README.md:130` | documents the `niwa dispatch "<task>"` form | n/a |

There is no hook, no shell script (`scripts/` holds only `docker-test.sh`, which never mentions dispatch), and no settings file in this repo that shells out to `niwa dispatch`. `internal/plugin/files/` contains no hooks at all.

Two things this establishes:

- **The `watch.go` finding is structural, not incidental.** Because `watch` binds to `dispatchLaunch` rather than to the cobra command, any capture logic added to `runDispatch` is invisible to it — provided the capture lives in `runDispatch` and not in the launcher. That is a constraint the PRD should state, because moving capture down into `dispatchLaunch` would put an interactive read inside a cron-driven review sweep.
- **Every caller already supplies the prompt as an argument.** So the compatibility requirement is narrow: keep the one-argument path byte-identical, and gate everything new on the argument being *absent*.

One caveat on the functional harness. `runNiwa` (`test/functional/steps_test.go:144-152`) sets `cmd.Stdout` and `cmd.Stderr` but leaves `cmd.Stdin` nil, which `os/exec` connects to `os.DevNull`. So a scenario that ran a promptless dispatch would see stdin as a non-TTY that returns EOF immediately — it could not hang, even if the gate were wrong. The suite already has both seams for the other case: `runNiwaWithStdin` (`worktree_delegation_steps_test.go:146-154`) feeds a pipe, and `iRunUnderPTYWithInput` (`steps_init_bootstrap_test.go:144-185`) drives the binary under util-linux `script -q -c ... /dev/null` to get a real pty. A capture is testable both ways with no new harness work.

**The residual hang risk is not the piped caller.** A shell script or hook launched *from an interactive terminal* inherits that terminal as stdin, so `IsStdinTTY()` returns true inside it. A TTY check alone therefore does not protect such a caller — what protects it is that it passes the prompt as an argument. The TTY check protects pipes, CI, cron, and daemons; the argument-present check protects everything else. Both are load-bearing.

### 3. Today's baseline: cobra's arity error, exit 1, no usage banner

Verified against a binary built from this worktree:

```
$ niwa dispatch
accepts 1 arg(s), received 0                      # stderr, exit 1
$ niwa dispatch </dev/null
accepts 1 arg(s), received 0                      # identical; there is no stdin path today
$ niwa dispatch a b
accepts 1 arg(s), received 2                      # stderr, exit 1
$ niwa dispatch ""
niwa: error: dispatch prompt must not be empty    # stderr, exit 1
```

The bare arity string comes from `cobra.ExactArgs(1)` (`dispatch.go:131`). There is no `Error:` prefix and no usage banner because the root command sets `SilenceErrors`/`SilenceUsage` and those inherit to children (`root.go:39-41`); `Execute()` prints the error once and exits 1 (`root.go:96-97`). The empty-string case is niwa's own check at `dispatch.go:141-143`.

Two consequences for the PRD:

- Relaxing `ExactArgs(1)` to `MaximumNArgs(1)` also changes the >1-arg message (to `accepts at most 1 arg(s), received 2`). That case must still refuse — a developer who forgot to quote a multi-word prompt should get an error, not a capture.
- `niwa dispatch ""` and `niwa dispatch` are distinguishable at the cobra layer (`len(args)`), and they should stay distinguishable. An explicitly-supplied empty prompt is a caller error and should keep today's message; an *absent* prompt is the new capture trigger. Collapsing them would mean `niwa dispatch "$EMPTY_VAR"` in a script silently opens a capture.

### 4. Prior art: refuse-on-isatty is the convention; stdin-as-content is always an explicit opt-in; almost nobody probes capabilities

Behavior verified locally where the tool was installed (gh 2.97.0, git 2.43.0), from source or docs otherwise.

| Tool | Behavior when interactive input is needed and stdin is not a terminal | Distinguishes "not a TTY" from "terminal lacks a capability"? |
|---|---|---|
| `gh issue create` | Hard error naming the flags, plus the usage banner, exit 1: `must provide `--title` and `--body` when not running interactively`. | No — isatty only. |
| `gh pr create` | Same shape, listing every alternative: `must provide `--title` and `--body` (or `--fill` or `fill-first` or `--fillverbose`) when not running interactively`. | No. |
| `gh` internals | `CanPrompt() = !neverPrompt && IsStdinTTY() && IsStdoutTTY()` — **both** streams must be terminals, and `neverPrompt` is the explicit `--no-prompt` / `GH_PROMPT_DISABLED` override. Stdin is read as content only when asked: `--body-file -` ("use `-` to read from standard input"). | No. [cli/cli#5721](https://github.com/cli/cli/issues/5721) is an open report that gh ignores `TERM` and dumps escape codes into dumb terminals. |
| `git commit` (no `-m`) | Does **not** check isatty. It launches `$EDITOR` regardless; with stdin on `/dev/null` the editor exits immediately, the message is unchanged, and git aborts: `Aborting commit due to empty commit message.` exit 1. With no editor configured at all: `error: unable to start editor ''` / `Please supply the message using either -m or -F option.` | **Yes — the only one that does.** `TERM=dumb` with `EDITOR` unset produces a distinct refusal: `error: Terminal is dumb, but EDITOR unset` / `Please supply the message using either -m or -F option.` I confirmed this fires *under a real pty* (`script -q -c ... /dev/null`), so it is a capability check keyed on an environment variable, not a TTY check. |
| `git commit -F -` | Reads the message from stdin — explicit opt-in, works fine non-interactively. | n/a |
| `jj describe` | Same architecture: `-m/--message` skips the editor, `--stdin` explicitly reads the description from stdin, `--editor` forces the editor open even after `--stdin`/`--message`. The editor is the default and the non-interactive paths are named flags. | Not documented. |
| `gum write` | Reads stdin unconditionally and uses it as the textarea's **initial value** (`in, _ := stdin.Read(...); if in != "" && o.Value == "" { o.Value = ... }`), then still starts the bubbletea program on stderr. With no tty available it fails at terminal-open time with a raw runtime error — [`open /dev/tty: no such device or address`](https://github.com/charmbracelet/bubbletea/issues/761) in containers. So stdin is content, not the interaction channel, and the no-tty failure carries no guidance. | No. |
| `fzf` | Deliberately splits the channels: candidates from stdin, UI on `/dev/tty`, selection to stdout — which is what makes `find … \| fzf \| xargs …` work. The non-interactive escape is `--filter/-f`, which skips the UI entirely. When `/dev/tty` cannot be opened it errors (`Failed to open /dev/tty`). | No. |
| `docker run -it` | Hard error: `the input device is not a TTY`. | No. |
| `ssh -t` | Warns and proceeds without a pty: `Pseudo-terminal will not be allocated because stdin is not a terminal.` | No. |
| [clig.dev](https://clig.dev/) | *"Only use prompts or interactive elements if `stdin` is an interactive terminal (a TTY)."* / *"Never require a prompt. Always provide a way of passing input with flags or arguments. If `stdin` is not an interactive terminal, skip prompting and just require those flags/args."* Plus a `--no-input` convention. | n/a |

The pattern across all of them:

- **Refusal keyed on isatty is the norm**, and the good refusals (gh, and niwa's own) name the exact flag or argument that fixes the invocation. The bad ones (gum, fzf, docker) surface a runtime device error the user has to interpret.
- **Reading stdin as content is never implicit.** `git commit -F -`, `jj describe --stdin`, `gh --body-file -` — every one is an explicit opt-in. gum is the sole tool that reads stdin unasked, and even there it is a pre-fill for an interactive editor, not a substitute for one.
- **Capability probing is essentially absent.** One instance across the whole set (git's `TERM=dumb`), and it is a coarse environment-variable read rather than a terminal query. This is consistent with what the bracketed-paste research already established: terminfo `BD`/`BE` produces false negatives (`xterm-kitty` lacks it), and a `DECRQM` query needs a read timeout and is not universally answered.

### 5. Stdin is a TTY but the capability is absent

The specific capability is DEC private mode 2004, bracketed paste. Per the round-1 bracketed-paste findings, three facts settle this case:

- **A `DECSET` of an unrecognized private mode is silently ignored by every conformant terminal** — no error, no echo, no garbage. Enabling 2004 on a terminal that does not support it costs nothing. The paste simply arrives unbracketed: raw bytes with `\r` separators and no framing.
- **There is therefore nothing to refuse.** Unlike the non-TTY case, the capture *can* run; only the newline-disambiguation accelerator is missing. Refusing here would deny the feature to terminals where it mostly works.
- **Support is close to universal in 2026** (xterm, all VTE terminals, kitty, alacritty, WezTerm, Ghostty, iTerm2, Terminal.app, mintty, foot, VS Code's integrated terminal, Windows Terminal since ~1.17). The real gaps are old GNU screen, pre-2005 xterm, VTE < 0.23.3, and the legacy Windows conhost.

What degrades gracefully **without** a probe is lazy detection: enable the mode, start reading, and let the arrival of a paste-start marker decide which rule applies. The unavoidable constraint is timing — the first newline of an unbracketed paste arrives before you have learned anything about the terminal. So the *base* rule (the one in force before any marker has been seen) cannot be one that a pasted newline would trip. Whatever the DESIGN picks, this is the property the PRD has to require: **a paste that arrives without bracketing must not be silently truncated at its first newline.** The two families of mechanism that satisfy it without a probe are (a) a base terminator that a pasted newline cannot produce, with the bracketed path as an accelerator once a paste-end marker has been seen, and (b) a read-burst heuristic — bytes still pending in the same read imply a paste. Both are DESIGN's call; (b) is imperfect over a slow ssh link.

**What the developer sees: nothing.** There should be no "your terminal does not support bracketed paste" banner, because niwa genuinely cannot know — the absence of markers is indistinguishable from a developer who typed instead of pasting. Any such message would fire on every typed prompt on a fully-capable terminal. The only thing the developer should see in the degraded case is the same capture UI, behaving the same way.

There is one message that *is* worth showing, and it is not a capability warning: a confirmation of what was captured (line count, or the first and last line) before any instance is provisioned. `runDispatch` creates the instance at `dispatch.go:217` and only then arms rollback at `dispatch.go:229-234`; a capture that ends up shorter than the developer expected is far cheaper to catch before that point than after.

The hard invariant for this case is the opposite failure and it is the one that hurts bystanders: if niwa enables 2004 and exits without sending `\x1b[?2004l` — panic, `os.Exit` past a `defer`, an unhandled signal — the terminal stays in bracketed-paste mode and the *next* program to own the tty receives markers it does not understand, rendering `00~` before pasted text. Restoration must cover every exit path including SIGINT and SIGTERM, and the same applies to termios raw mode. This is a requirement about the state of the developer's terminal after `niwa dispatch` returns, which makes it a PRD-level property, not a DESIGN detail.

## Recommendation

### (a) stdin is not a TTY: refuse, with guidance, exit 1

`niwa dispatch` with no prompt argument and a non-interactive stdin should fail immediately, printing to stderr and exiting 1, with a message that names the working invocation. Something in the shape of the four existing refusals:

> `niwa: error: no prompt given and stdin is not a terminal; pass the prompt as an argument: niwa dispatch "<task>"`

Rationale, in order of weight:

1. It is the only pattern niwa has. All four existing non-TTY input gates refuse (`init.go:290`, `destroy.go:116`, `destroy.go:239`, `destroy.go:325`); none falls back or defaults. A new command that behaved differently would be the odd one out in a codebase of five.
2. It satisfies the hard requirement structurally rather than by care. There is no read, so there is nothing to block on — as opposed to a fallback that reads stdin, where the not-hanging depends on the pipe actually closing.
3. It matches the dominant convention (gh, clig.dev), and the alternative — reading stdin as the prompt — is something no comparable tool does implicitly. Every one of them makes it an explicit flag.

Concretely, do **not** read stdin as the prompt when it is a pipe. It would introduce a second implicit non-interactive channel alongside the argument, with an undefined resolution for `echo x | niwa dispatch "y"`, and it would turn `niwa dispatch < /dev/null` in a mis-written hook into a silent empty-prompt dispatch rather than an error. The BRIEF already rules piping out as a design driver, and agents have the better path (write a brief, pass a pointer). If a piped path is ever wanted it should be an explicit opt-in flag, decided on its own merits, not acquired as a side effect of this feature.

Also do not adopt fzf's trick of opening `/dev/tty` for the interaction while stdin is a pipe. In the invocations that matter here — cron, CI, a daemon — there is no controlling terminal and the open fails; in the one case where it succeeds (a script run from an interactive shell), it would seize the developer's terminal and block, which is precisely the outcome the hard requirement forbids.

**The gate should be stdin-TTY AND stderr-TTY**, mirroring `destroy.go:239`. The capture reads stdin and renders to stderr; stdout is where dispatch's session hints go (`dispatch.go:379-384`) and must stay redirectable. This deliberately differs from gh's `IsStdinTTY() && IsStdoutTTY()`, and the reason is that niwa puts prompts on stderr where gh puts them on stdout.

**Exit 1**, not a new code. `destroy`'s non-TTY refusals return plain errors and exit 1 via `root.go:96-97`; only `init` uses the typed exit-4 path, and that is tied to `PRD-init-bootstrap-empty-source`'s exit-code table, which `dispatch` has no equivalent of. Exit 1 also matches today's baseline, so a script that currently checks for non-zero keeps working.

### (b) stdin is a TTY but bracketed paste is absent: do not probe, do not refuse, degrade silently

Enable the mode unconditionally — it is a no-op where unsupported — and require that the submit rule be safe when no markers ever arrive. The PRD requirement should be the property, not the mechanism: an unbracketed multiline paste must not be silently truncated at its first newline, and the developer must be able to see what was captured before any instance is created. No capability warning is shown, because the missing-markers state is indistinguishable from ordinary typing.

Pair this with a restoration requirement: the terminal's bracketed-paste mode and termios state must be restored on every exit path, including SIGINT and SIGTERM. The failure this prevents is not niwa's own — it is the next command the developer runs in that terminal.

### (c) a script or hook that supplies the prompt some other way: unchanged, and provably so

The single-argument path must stay byte-identical, and must not consult the TTY at all. The TTY check belongs exclusively to the `len(args) == 0` branch. Everything in section 2 rides on this: `watch.go:579` and `watch.go:826` bypass `runDispatch` entirely and are unaffected regardless, and the `/dispatch` skill, the README form, and all five functional-test invocations pass a prompt and would never reach the new branch.

Keep `niwa dispatch ""` erroring with today's message (`dispatch.go:141-143`). An explicit empty argument is a caller bug — usually an unset variable — and turning it into a capture trigger would make `niwa dispatch "$TASK"` open an interactive prompt in a script when `$TASK` is empty.

I would **not** add a `--no-input` flag. clig.dev recommends one, but for `dispatch` the prompt argument already *is* the non-interactive channel, so the flag could only change which error message prints. If niwa ever wants a global no-input signal it belongs on the root command alongside `--no-progress`, not bolted onto this feature.

The one residual gap, stated so the PRD can decide rather than discover it: a hook or script run from an interactive terminal inherits that terminal as stdin, so a promptless `niwa dispatch` inside it *will* pass the TTY gate and open a capture. That is a caller bug, and the mitigations are already in scope — the capture must make it visibly obvious that niwa is waiting for input, and Ctrl-C must abandon it cleanly without creating anything.

## Open Questions

1. **Is stdin-TTY AND stderr-TTY the right gate, or stdin alone?** I recommend both, following `destroy.go:239`. The case for stdin alone is that `niwa dispatch 2>err.log` from a terminal would then still capture, at the cost of the capture UI going to the log file. Someone should decide whether that redirect is worth supporting; I think not, but it is a judgment call and it changes an observable behavior.

2. **Should the refusal be worded to mention the capture at all?** "no prompt given and stdin is not a terminal" tells a scripted caller what to do but says nothing about the feature they are missing. An alternative wording mentions that an interactive capture exists and requires a terminal. The first is better for the machine reader, the second better for a human who piped by accident. niwa's four existing strings all take the first shape.

3. **Does the pre-provision confirmation of captured content belong in this PRD or the next one?** I have argued for it as the mitigation that makes the degraded-capability case survivable, but it is also a general capture affordance that applies on fully-capable terminals, and it may collide with whatever the size-ceiling work decides to show at the moment of the paste.

4. **`niwa dispatch` under `script`/`expect`-style automation.** Such a caller has a real pty, passes the TTY gate, and would sit in a capture forever if it supplied no prompt. Nothing in this repo does that today. Whether the PRD should carry an explicit non-goal here, or a timeout, is a human call — I lean toward an explicit non-goal and no timeout, since a timeout would also fire on a developer who walked away mid-paste.

## Summary

niwa already has one unambiguous house pattern for this, applied at all four of its non-TTY input gates: print whatever the interactive surface would have shown, then refuse with a message naming the flag or argument that makes the invocation work — never fall back, never assume a default (`init.go:290`, `destroy.go:116`, `destroy.go:239`, `destroy.go:325`). That is also what `gh` does and what clig.dev prescribes, and reading stdin as content is something no comparable CLI does implicitly — `git commit -F -`, `jj describe --stdin`, and `gh --body-file -` are all explicit opt-ins — so I recommend refusing with guidance and exit 1 when stdin is not a TTY, gated on stdin and stderr both being terminals, and confining the whole check to the no-argument branch so the existing prompt-as-argument path stays byte-identical for every caller in the repo. The capability case is different in kind and should be handled differently: enabling bracketed paste on a terminal that lacks it is a silent no-op, so there is nothing to refuse and nothing honest to warn about, and the graceful degradation is to require that the submit rule stay safe when no markers ever arrive rather than to probe — with a hard requirement that the terminal's paste mode and termios state be restored on every exit path, since the cost of leaking that state lands on the developer's *next* command. Worth flagging for the PRD: `niwa watch` reaches the launcher directly at `watch.go:579` and `watch.go:826` rather than through the cobra command, so capture logic must live in `runDispatch` and not in `dispatchLaunch`, or a cron-driven review sweep would inherit an interactive read.
