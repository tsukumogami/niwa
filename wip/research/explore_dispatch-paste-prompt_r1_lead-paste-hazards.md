# Lead: What are the hazards of accepting arbitrary pasted terminal output as the prompt?

All measurements below were taken on this machine (Linux 6.8, x86_64, PAGE_SIZE
4096) against this worktree. Where a claim rests on terminal behavior rather
than repo code, it is marked as such.

## Findings

### 1. The 128 KB cap is not conservative on Linux -- it is exactly the kernel limit

`maxPromptBytes = 128 * 1024` = 131,072 (`internal/cli/dispatch.go:81`),
enforced with `len(prompt) > maxPromptBytes` at `internal/cli/dispatch.go:144`.

The justifying comment (`internal/cli/dispatch.go:75-81`) reasons about
`ARG_MAX`: "ARG_MAX is at least 4096 on every POSIX platform and is typically
far larger; a conservative bound below it leaves room for the binary path, the
flags, and the environment". That is the wrong limit. On Linux the binding
constraint for a *single* argument is `MAX_ARG_STRLEN` = 32 x PAGE_SIZE,
hardcoded in the kernel, which on any 4 KB-page Linux is exactly 131,072 bytes
-- the same number. `ARG_MAX` on this machine is 2,097,152, sixteen times
larger and not what binds.

Verified empirically (`/bin/true` with a single argument of N bytes):

| N | result |
|---|--------|
| 131,070 | OK |
| 131,071 | OK |
| 131,072 | `[Errno 7] Argument list too long` |

So the largest argument that actually execs is 131,071 bytes (the kernel counts
the terminating NUL). Two consequences:

**(a) Off-by-one in the permissive direction.** The check is `>`, so a prompt of
exactly 131,072 bytes passes validation at `dispatch.go:144` and then fails at
`execve` inside `realDispatchLaunch` (`internal/cli/dispatch_launcher.go:52`),
surfacing at `internal/cli/dispatch.go:335` as `niwa: error: launching dispatch
worker: dispatch: launching claude --bg: fork/exec ...: argument list too long`.
The instance was already provisioned at `dispatch.go:217`, so the deferred
rollback at `dispatch.go:230-234` correctly destroys it -- state stays clean --
but the user gets an opaque OS error for precisely the case the validation
exists to catch cleanly.

**(b) Keep-alive silently eats the (nonexistent) margin.**
`internal/cli/dispatch.go:327` does `prompt = keepAliveArmingInstruction +
prompt`, and that constant
(`internal/cli/dispatch_keepalive.go:33-35`) measures **638 bytes**. The prepend
happens at step (9d), long *after* the length check at step (1). The comment at
`internal/cli/dispatch.go:311-313` asserts the instruction's "fixed
few-hundred-byte size is well inside the conservative maxPromptBytes margin
validated in step (1)" -- there is no margin. With keep-alive armed, the real
ceiling is 131,071 - 638 = **130,433 bytes**, and anything above that passes
validation and dies at exec. The effective limit is state-dependent in a way
neither the constant nor the error message reflects.

**One-line fix:** re-check the length on the final string immediately before
`dispatchLaunch` at `dispatch.go:334`, and lower the constant below the per-arg
limit (e.g. 120 KB) so there is genuine headroom.

**macOS** differs: total `ARG_MAX` is 1 MiB and XNU has no separate per-string
cap, so 128 KB is genuinely conservative there. The repo ships darwin binaries
(`.github/workflows/release.yml:52-53`), so the bug is Linux-only but the
platform is supported.

### 2. Is 128 KB realistic against actual pasted content? Yes, generously so

Measured in this repo:

| content | bytes | lines |
|---|---|---|
| `go run` trivial panic (1 goroutine) | 117 | 6 |
| `go test ./...` (passing) | 1,468 | 25 |
| test-binary panic, 8 live goroutines, 12-frame stack | 2,263 | 46 |
| `go test ./internal/cli -v` | 71,354 | 1,254 |
| `go test ./... -v` | 323,602 | 5,689 |

Average density across that verbose run: **56.9 bytes/line**. At that density
131,071 bytes is roughly **2,300 lines** of terminal output.

The workflow being optimized is "an error scrolled past, I select it with the
mouse". That selection is tens to a few hundred lines -- 1 to 20 KB. The cap is
two orders of magnitude above the common case. It binds only when someone
selects an entire verbose test run (the full `go test ./... -v` above is 2.5x
over) or pastes a whole downloaded CI job log. Those are real but they are not
the recurring case in the scope doc.

**What should happen on exceed: reject, not truncate.** Truncating a stack trace
hands the worker a half-fact and it cannot tell that it is missing context --
strictly worse than a clear failure. But the *current* rejection is badly placed
for an interactive prompt: the message says "shorten it rather than relying on
truncation" (`dispatch.go:145`), which at a paste prompt means redo the mouse
selection. The proportionate behavior is to check at the paste boundary, the
instant the block lands and before any provisioning, report actual size vs
limit, and leave the buffer editable so the user can trim in place rather than
losing it.

### 3. ANSI escapes: mostly a non-problem for the one path in scope

**The key fact: a mouse selection from terminal scrollback contains no escape
sequences.** The terminal has already interpreted them into screen cells;
selection copies rendered cells, not the byte stream. Color SGR codes are gone.
Progress-bar `\r` frames are gone -- you get each line's final rendered state,
not the animation. Cursor movement is gone. For the input path the scope doc
names as the *only* one being optimized, the ANSI hazard is largely theoretical.

Where it stops being theoretical:

- **Non-mouse clipboard sources.** `xclip < build.log`, copying from a viewer
  showing raw escapes, or pasting a downloaded GitHub Actions raw log (those
  files do carry raw ANSI). Not the design driver, but one keystroke from the
  same prompt.
- **Terminals already filter as a second layer.** xterm's
  `disallowedPasteControls` defaults to `BS,DEL,ENQ,EOT,ESC,NUL` -- ESC is
  stripped on paste by default, converted to a space. PuTTY filters everything
  except CR, LF, tab, BS and DEL. This is per-terminal and not guaranteed, so it
  is defense in depth, not a reason to skip our own handling.
- **The bracketed-paste end marker is the one that actually matters.** If pasted
  bytes contain a literal `ESC [ 2 0 1 ~`, the paste ends early and everything
  after it arrives as if typed. This is the known bracketed-paste bypass class
  (PuTTY before 0.73, CVE-2019-17068). Practical implication for niwa's reader:
  after seeing the end marker, do not auto-submit on a `\r` arriving in the same
  read burst -- otherwise a payload can force premature submission of a
  truncated prompt.

**Do escapes need stripping before argv? No.**
`internal/cli/dispatch_launcher.go:66-71` passes the prompt as one discrete argv
element with no shell anywhere in the path, so escapes are inert bytes. Verified:
an argument containing `\n`, `\r` and `\x1b[31m` execs fine. The D8 reasoning in
that file's comment holds.

The one byte that does break argv is **NUL**. Verified: `exec.Command("/bin/true",
"abc\x00def")` returns `fork/exec /bin/true: invalid argument`. It errors rather
than truncating (good), but it would surface as the same opaque post-provision
failure as the length overflow. A paste containing binary garbage (a `cat` of a
binary, a truncated log) hits this. Strip or reject NUL at capture.

**Do escapes need stripping before echo? Yes -- and this feature creates that
surface.** The annotate-the-log requirement means the prompt must echo what was
pasted. Raw ESC bytes in an echoed buffer are executed by the terminal: cursor
repositioning over niwa's own frame, OSC title rewrites, potentially OSC 52
clipboard writes.

**Is `internal/tui/sanitize.go` reusable? Partly -- it has holes.** I ran its
regex against a control-character corpus:

| input | output | verdict |
|---|---|---|
| `\x1b[31mred\x1b[0m` | `red` | correct |
| `\x1b[201~` (paste end marker) | `[201~` | ESC removed, body left as junk |
| `\x1b[15~` (F5) | `[15~` | same |
| `\x1b[3@` | `[3@` | same |
| `\x1b]0;title\x07x` | `x` | correct |
| `\x1bP1;2q payload \x1b\\tail` (DCS) | `P1;2q payload \tail` | body left as junk |
| `progress 50%\rprogress 100%` | unchanged | CR passes through |
| `abc\b\b\bxyz` | unchanged | BS passes through |
| `ding\x07` | unchanged | BEL passes through |
| `a\x00b` | unchanged | NUL passes through |
| `\x9b31m` (8-bit CSI) | unchanged | passes through |

The root cause of the middle rows: the first alternative
(`internal/tui/sanitize.go:8`) requires a final byte in `[A-Za-z]`, but ECMA-48
allows any final byte in 0x40-0x7E. Sequences ending in `~`, `@`, `^` and
backtick are missed; the trailing `|\x1b` catch-all then strips only the ESC, so
nothing *executes* (safe for the picker's display-only use at
`internal/tui/picker.go:139,145,146`) but the sequence body is left as visible
garbage. For a picker row that is fine. For a prompt buffer the user then edits
and dispatches, it silently injects junk into the payload.

Recommendation: reuse the file, but widen the CSI final-byte class to
`[\x40-\x7E]` and add a C0 pass (keep `\n` and `\t`, drop or visualize the rest).
And keep the two jobs separate -- display sanitization (aggressive, lossy, for
what the user sees) and payload normalization (minimal: drop NUL, leave the rest)
are different functions with different correctness criteria.

### 4. Where the prompt is persisted or echoed today: nowhere

I traced every write in the dispatch path.

- **Session mapping** (`internal/cli/dispatch.go:356-365`, struct at
  `internal/workspace/session_map.go:49-73`, written to
  `.niwa/sessions/<uuid>.json` by `session_map.go:88-107`) carries SessionID,
  InstanceName, InstancePath, Ephemeral, Origin, Label, KeepAlive, Created.
  **No prompt field.**
- **Pending marker** (`internal/cli/dispatch.go:532-539`) contains only an
  RFC3339 timestamp.
- **stdout** (`internal/cli/dispatch.go:379-384`) prints the session UUID, the
  instance path, and three `claude` hints. Not the prompt.
- **stderr** carries only the model warning (`:265`), the remote-control warning
  (`:295`), the keep-alive warning (`:330`), and the attach warning (`:393`).
  Not the prompt.
- **`--label`** (`dispatch.go:21` -> `:362`) is the one persisted free-text
  field. I grepped the whole repo: nothing ever reads `SessionMapping.Label`
  back for display. It is inert today.

Two consequences. First, escapes reaching a niwa-written file or a niwa-printed
line is a non-issue *as the code stands* -- the concern is created by this
feature, at the echo, and would be created a second time if the paste UI
auto-derives `--label` from the paste's first line. That last one is tempting
and should either be avoided or sanitized, because a label is by design
something a future `niwa list` will render.

Second, **the real disclosure surface is argv, not disk.** The prompt is a
discrete argv element (`dispatch_launcher.go:70`), so it is readable via
`ps aux` and `/proc/<pid>/cmdline` for the life of the process. Verified on this
machine: `/proc` is mounted without `hidepid`, `/proc/self/cmdline` is mode 444,
and I read PID 1's cmdline as an unprivileged user. Pasted CI logs are exactly
the content that carries tokens.

This is **not a regression** -- today's `niwa dispatch "..."` has identical argv
exposure *plus* a copy in shell history. An inline paste prompt removes the shell
history copy, which is a modest security improvement worth stating explicitly in
the design rather than leaving implicit.

### 5. Terminal restoration: real risk, smaller than it sounds, shell-dependent

**What the repo does today.** `internal/tui/picker.go:79-88` is the only
raw-mode site: `term.MakeRaw` with a plain `defer term.Restore`. No signal
handling. That is adequate for Ctrl-C specifically, because `MakeRaw` clears
`ISIG`, so Ctrl-C arrives as byte 0x03 and is handled as ordinary input
(`picker.go:113`, `picker.go:191-194`) -- a normal return, defers run. A panic
also unwinds through the defer, as the doc comment at `picker.go:57-59` claims.

The uncovered signals are SIGTERM, SIGHUP (window closed, ssh dropped), SIGQUIT
and SIGKILL; Go's default disposition terminates without running defers.

There is a signal-handling precedent to copy:
`internal/cli/sessionattach/supervise.go:58-60` does
`signal.Notify(sigCh, SIGINT, SIGTERM, SIGHUP)` with `defer signal.Stop(sigCh)`.
It forwards rather than restores, but it is the established shape in this
codebase.

**How bad is "left broken"? I measured it.** Spawned a shell on a pty, ran a Go
program that calls `term.MakeRaw`, emits `\e[?2004h`, then SIGKILLs itself, and
read the flags back afterwards:

| shell | termios flags after the SIGKILL |
|---|---|
| bash | `icrnl opost isig icanon echo` -- fully restored |
| sh (dash) | `-icrnl -opost -isig -icanon -echo` -- left broken |

bash's readline resets the line discipline at every prompt, so it repairs the
damage by itself. dash does not; the user needs `stty sane` or `reset`.

The same capture incidentally confirms the scope doc's claim about bracketed
paste: bash emits `\e[?2004l` before running each command and `\e[?2004h` when
redrawing the prompt. So a leaked DECSET 2004 is also self-healed by bash --
and also not by dash.

Net: for this user in an interactive bash or zsh, the practical risk is low.
It is not zero: non-readline shells, and any `niwa` invoked from a script or
subshell, stay broken.

**The discipline in Go:**

1. Restore from a defer *and* from a signal handler. `signal.Notify` on SIGINT,
   SIGTERM, SIGHUP, SIGQUIT; on receipt restore termios, emit `\e[?2004l`, then
   `signal.Reset(sig)` and re-raise so the exit status stays correct.
2. Order matters. Enable raw mode first, then DECSET 2004. Tear down in reverse:
   DECRST 2004 while you can still write meaningfully, then `term.Restore`.
3. **Do not rely on ISIG being off.** `term.MakeRaw` disables it, so Ctrl-C is a
   byte. If the design instead picks a lighter-touch mode that keeps ISIG (a
   reasonable choice for a line editor that wants Ctrl-C to abort), Go's default
   SIGINT handling terminates the process *without running defers* --
   reintroducing exactly the hazard. Whichever mode is chosen, the signal
   handler is mandatory.
4. Never `os.Exit` while raw. Cobra's `RunE` returns normally, so defers run
   before the top-level exit -- fine as long as the prompt code does not
   shortcut.
5. SIGKILL and SIGSTOP cannot be caught; neither can a `kill -STOP` that
   suspends the process with the terminal raw. Nothing in-process fixes these.
   Accept and document.

### 6. Untrusted content: one real concern, cheaply fixed, and it solves a scope requirement

The proportionate framing: the user is pasting their own build output and
dispatching it deliberately. This is not an attacker-controlled channel in the
normal case, and treating it as one would be disproportionate.

**What is actually real: there is no framing at all.**
`internal/cli/dispatch.go:327` is a bare string concat, and
`keepAliveArmingInstruction` ends "...Arm it once, do not mention it again, and
then proceed with the task." followed by two newlines
(`internal/cli/dispatch_keepalive.go:33-35`). The pasted log therefore lands
exactly in the slot where the worker expects its task, with nothing marking it as
data rather than instruction. A log containing imperative text -- a test failure
that says "run `make db-reset` to fix", a CI step echoing a remediation script,
a tool's own usage hint -- reads to the worker as something it was asked to do.
The worker is autonomous with tool access and whatever `--permission-mode` was
forwarded (`internal/cli/dispatch.go:516-518`).

**The fix is cheap and does double duty.** Fence the pasted block with an
explicit statement that it is terminal output pasted by the user, to be
diagnosed rather than obeyed. That gives the bare-paste case a sane default task
and gives the annotated case a natural structure -- annotation outside the fence,
log inside -- which is exactly the "must support both a bare pasted log and an
annotated one" requirement from the scope doc. A hazard mitigation and a UX
requirement land on the same mechanism.

**Ordering relative to the keep-alive prefix:** as written, keep-alive is always
a prefix and pasted content is always last. Unfenced, last position is the worst
position -- it is the most recent instruction. Fenced, ordering stops mattering.
(A pasted log could also in principle contain text telling the worker to skip
arming keep-alive; low stakes, and fencing covers it.)

**What is not worth building:** escape-sequence scanning for "prompt injection",
content blocklists, or a confirmation gate on suspicious-looking text. The user
chose to dispatch this. Fencing, plus showing the user what will be sent -- the
echo you are already building -- is the proportionate answer.

## Implications

The scope assumption that pasted content is an escape-sequence minefield is
largely wrong for the mouse-selection path, and that should relax the design.
Escape handling is needed at exactly one place, the echo, and `sanitize.go` gets
you most of the way there after a small regex widening. Payload normalization
reduces to dropping NUL.

The genuinely load-bearing findings are elsewhere. `maxPromptBytes` has a real
arithmetic bug on Linux that a paste prompt makes much easier to hit -- pastes
are the only realistic way to approach 128 KB in the first place, so this feature
is what turns a dormant bug into a reachable one. It should be fixed in the same
change. The rejection behavior is right but its placement is wrong for an
interactive prompt: check at the paste boundary, before provisioning, and keep
the buffer.

Terminal restoration needs the signal handler that `picker.go` never needed,
because the paste prompt's raw-mode window is longer (a user composing an
annotation) and because the DECSET 2004 state is an extra thing to unwind. The
existing `supervise.go` pattern is the template. The residual risk after doing
this correctly is small and shell-dependent.

The untrusted-content question resolves into a framing decision, not a security
control, and the framing mechanism is the same one that satisfies the
bare-vs-annotated requirement. That argues for designing the fence early rather
than bolting it on.

## Surprises

1. **Mouse selection strips the escapes for you.** The terminal copies rendered
   cells, not the byte stream, so color codes, `\r` progress frames and cursor
   movement never reach the clipboard. This deflates most of the ANSI
   sub-question for the one path in scope.
2. **`maxPromptBytes` is not conservative -- it sits exactly on Linux's
   `MAX_ARG_STRLEN`,** and the 638-byte keep-alive prepend at
   `dispatch.go:327` happens *after* validation, so the effective limit is
   state-dependent. The code comment asserts a margin that does not exist.
3. **bash repairs a raw-mode terminal at its next prompt; dash does not.** The
   "broken shell" fear is real but shell-dependent and smaller than it sounds
   for an interactive user.
4. **niwa never persists the prompt anywhere.** The disclosure surface is
   argv/`ps`, not disk -- and an inline paste prompt is a net *improvement*
   because it removes the shell-history copy.
5. **`SanitizeDisplayString` does not match CSI sequences whose final byte is not
   a letter** -- including the bracketed-paste markers themselves.

## Open Questions

- Does the prompt echo the raw buffer or a sanitized rendering? If sanitized for
  display but raw in the payload, the user is not seeing what they are sending;
  if sanitized in both, the payload loses bytes. Needs an explicit decision.
- Enforce the length limit at paste time (reject the paste, keep the buffer
  editable) or at submit? Determines whether the user can trim in place.
- Should `--label` auto-derive from the paste's first line? If yes, sanitization
  becomes mandatory at a surface that is both persisted and rendered.
- Exact fencing wording, and whether it should be suppressible for users who
  want full control of the prompt.
- Should NUL bytes be stripped silently or reported? Silent stripping is
  friendlier; reporting is more honest about payload mutation.
- Not investigated: how tmux and screen affect clipboard contents and DECSET
  2004 pass-through. Lead 1 (bracketed paste) is the right owner for that.

## Summary

The ANSI/control-character hazard is much smaller than the scope assumed --
mouse selection copies rendered cells, so escapes never reach the clipboard on
the one path being optimized -- while a real bug sits underneath: `maxPromptBytes`
(131,072) is exactly Linux's per-argument `MAX_ARG_STRLEN`, not a conservative
bound below `ARG_MAX`, and the 638-byte keep-alive prepend at `dispatch.go:327`
is applied *after* the check, so oversized prompts pass validation and die at
`execve` post-provisioning. The proportionate design is: check the length at the
paste boundary against a genuinely lower cap, sanitize only at the echo (reusing
`sanitize.go` after widening its CSI final-byte class), add the signal handler
`picker.go` never needed, and fence the pasted block as data -- which
simultaneously satisfies the bare-vs-annotated requirement. The biggest open
question is whether the echoed buffer and the dispatched payload are the same
bytes, since sanitizing one but not the other means the user is not seeing what
they send.
