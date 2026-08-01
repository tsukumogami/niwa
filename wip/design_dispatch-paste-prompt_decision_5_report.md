# Decision 5: Terminal lifecycle

How raw mode and bracketed-paste mode are entered, restored, and re-established
across every exit path, including signals and suspend/resume. Binding: R9, R38,
R39, R7.

Everything asserted below about signal behavior was measured on this machine
(Linux 6.8, Go 1.25.3, `golang.org/x/term` v0.42.0) with a Go program driven on a
real pty. One textbook idiom turned out to be wrong for one signal; that
measurement is the reason this decision is not a two-line answer.

## Options Considered

### A. Defer only

`term.MakeRaw` on entry, `defer term.Restore` on exit -- the discipline
`internal/tui/picker.go:82-87` already uses. Defers cover normal return, error
return, and panic unwind, which is a larger fraction of exit paths than it looks:
Cobra's `RunE` returns rather than exiting, and `internal/cli/root.go:96` calls
`os.Exit(1)` only after `rootCmd.Execute()` has returned, so every ordinary
failure path unwinds the capture's defers first.

It fails on exactly the paths R9 enumerates. Go's `os/signal` documentation is
explicit: "A SIGHUP, SIGINT, or SIGTERM signal causes the program to exit." That
exit does not unwind goroutine stacks, so no defer runs. Measured: a capture
killed by SIGTERM under defer-only discipline leaves the pty at
`icanon=0 echo=0 isig=0`. R9 names SIGINT, SIGTERM, and SIGHUP explicitly, so
this option is non-compliant by construction, and R38 is entirely outside its
reach.

### B. Defer plus a signal handler

Option A plus `signal.Notify` on the signals R9 and R38 name, with a handler that
restores the terminal and then re-raises so the process still dies with the
correct disposition. This is the shape prior research recommended and the shape
`internal/cli/sessionattach/supervise.go:58-60` already uses for signal
forwarding.

It is correct for R9's three fatal signals. It is *silently wrong* for R38 if
implemented from the textbook, because the standard `signal.Reset(sig)` then
`Kill(self, sig)` re-raise does not work for SIGTSTP in Go -- see the
Recommendation for the measurement and the mechanism. An implementation written
this way passes review, passes the PRD's R38 acceptance criterion as currently
worded, and does not suspend.

The other weakness is structural rather than behavioral: handling signals in a
separate goroutine means termios mutations happen concurrently with the render
path, and the restore-on-resume has to coordinate with a reader that is blocked
in `read(2)`.

### C. A scoped terminal-state manager

A small type owning the fd, the pre-capture state, the raw state, the signal
channel, and the select loop, exposing enter/suspend/resume/close. Same
mechanism as B, but every termios mutation happens on one goroutine, driven by a
`select` over `{input bytes, signals}` with a reader goroutine feeding the byte
channel. The reader goroutine is not optional in any design that handles signals
synchronously: a blocking `read(2)` on stdin cannot otherwise be interrupted.

Costs one more type and one goroutine than B. Buys three things B does not: the
suspend/resume sequence becomes expressible as two loop branches rather than
cross-goroutine coordination; the re-enter-raw step lands naturally in the
SIGCONT branch, which is where it must be for correctness (below); and the whole
lifecycle becomes unit-testable behind an injected interface without a pty.

## Recommendation

Option C, with the specifics below.

### Raw mode goes on the stdin fd, rendering goes to stderr

`picker.go:79-88` calls `MakeRaw` on the **stderr** fd while reading from
**stdin** (`picker.go:99`). Verified -- the prior research flag is correct. The
new capture must use the stdin fd instead.

Termios is a property of the tty *device*, not the fd: any fd open to the same
terminal sees the same line discipline. I confirmed this incidentally -- the test
child set raw mode through its stdin fd and the parent observed the change by
reading `tcgetattr` on the pty *master*, a different fd on the other end of the
pair. So when stdin and stderr are the same terminal, which R21's gate makes the
normal case, picker's choice happens to work.

It is still the wrong fd, in both directions. When stderr is a tty and stdin is
not, picker puts a terminal it never reads into raw mode. When stdin is a tty and
stderr is not, the `term.IsTerminal` guard skips raw mode entirely and the picker
reads in canonical mode. Raw mode exists to change how the *read* behaves --
canonical buffering, ISIG, echo -- so it belongs on the fd being read. R21's
both-must-be-terminals gate masks the defect rather than justifying it.

So: `MakeRaw` on `os.Stdin`'s fd; DECSET/DECRST 2004 and all rendering to stderr
(R21, R22). Enable in order -- raw first, then bracketed paste -- and tear down in
reverse, DECRST while writing is still meaningful, then termios.

Do not copy picker's test seam, which derives the raw-mode fd from the injected
stderr writer. Inject the fd (or a terminal-mode interface) explicitly.

### State capture

```
preState, err := term.MakeRaw(fd)   // the state R9 must return to
rawState, err := term.GetState(fd)  // sampled once, immediately after
```

Two states, not one. `preState` is the R9 restore target. `rawState` is what a
resume returns to, sampled at entry so that resuming never depends on what the
terminal looked like while the process was stopped -- a fresh `MakeRaw` on resume
would bake in any `stty` the developer ran from the foregrounded shell, and the
final restore would then silently clobber it. `term.GetState` is present in
x/term v0.42.0.

### Signal set and handler shape

`signal.Notify(ch, SIGINT, SIGTERM, SIGHUP, SIGTSTP, SIGCONT)` on a buffered
channel -- the docs require it: "Package signal will not block sending to c: the
caller must ensure that c has sufficient buffer space." Size 8 is ample.
Notify/Stop/Reset are scoped to the capture, never process-wide.

SIGQUIT is not required by R9 and is unreachable by keystroke under `MakeRaw`
(ISIG off makes Ctrl-\ a byte). Adding it costs one line and closes an external
`kill -QUIT` leak; treat as optional.

**Fatal branch (SIGINT, SIGTERM, SIGHUP):** DECRST 2004, `term.Restore(fd,
preState)`, `signal.Reset(sig)`, `syscall.Kill(syscall.Getpid(), sig)`.

Measured on a pty: the handler runs, the terminal returns to its pre-capture
mode, and the process dies *by signal* -- shell statuses 130, 143, and 129
respectively, with `WIFSIGNALED` true. That is the correct Unix outcome and does
not conflict with R31, which enumerates the non-TTY refusal, the empty refusal,
and abandonment, and gives those the ordinary error status (`os.Exit(1)`,
`root.go:96`). A signal death has no exit status to assign.

Why `Reset` works here: SIGHUP, SIGINT, and SIGTERM carry `_SigNotify + _SigKill`
in the runtime's signal table (`runtime/sigtab_linux_generic.go:11,12,25`), so
`initsig` installed a handler at startup and recorded the prior disposition.
After `Reset`, the still-installed Go handler finds no receiver, and `_SigKill`
makes it die from the signal with the right status.

R7 holds structurally on this path: the capture runs before any provisioning, so
there is nothing to roll back. That is a placement requirement, not a handler
requirement -- see the handoff section.

### The SIGTSTP sequence, and why it is not the textbook one

**The standard re-raise idiom does not work for SIGTSTP in Go.** Measured: after
`signal.Reset(syscall.SIGTSTP)`, `/proc/self/status` `SigCgt` *still* has
SIGTSTP's bit set -- Go's handler remains installed -- and the re-raised SIGTSTP
is swallowed by it. The process does not stop; it continues straight through, in
one case 11 ms later. `ps` confirms state `S`, never `T`.

The mechanism: SIGTSTP is `_SigNotify + _SigDefault + _SigIgn`
(`runtime/sigtab_linux_generic.go:30`). `initsig` skips every `_SigDefault`
signal (`runtime/signal_unix.go:127-129`), so no original disposition was ever
recorded, and `sigdisable` restores one only when `sigInstallGoHandler` returns
false -- which it does not for SIGTSTP. The handler stays installed, sees no
receiver, and since SIGTSTP carries neither `_SigKill` nor `_SigThrow`, returns
without acting. Re-arming `Notify` after `Reset` is worse: measured, it produces
an unbounded self-re-raise loop. This is a real divergence from the `os/signal`
documentation's "Reset will restore the system default behavior for the signal".

The sequence that works:

1. **On SIGTSTP**, from the select loop: write DECRST 2004 to stderr, then
   `term.Restore(fd, preState)`. The terminal is now in its pre-capture mode.
2. `syscall.Kill(syscall.Getpid(), syscall.SIGSTOP)`. SIGSTOP is uncatchable and
   unblockable, so it always stops. The shell's job control cannot distinguish
   it from SIGTSTP -- both report the job `Stopped`, and `fg` sends SIGCONT
   either way.
3. **Do not re-enter raw mode in straight-line code after that call.** `kill()`
   returns before the stop takes effect -- measured, the log line immediately
   after the raise printed in the same millisecond, and the process stopped only
   afterwards. Return to the select loop instead.
4. **On SIGCONT**: `term.Restore(fd, rawState)`, re-emit DECSET 2004, and repaint
   the captured buffer (R35 -- the screen may have scrolled or been cleared by the
   intervening shell). No `Notify` re-arm is needed, because `Reset` was never
   called for SIGTSTP.

Verified end-to-end on a pty: raw during capture, `icanon=1 echo=1 isig=1` while
the process was genuinely stopped (`ps` state `T`), raw again after SIGCONT,
input still accepted after resume, and a final state byte-identical to
pre-capture.

**Suspended while a paste is mid-arrival.** Under `MakeRaw` ISIG is off, so a
typed Ctrl-Z is byte `0x1A`, not a signal; R38's path is reachable only via an
external `kill -TSTP`/`-STOP`, or via Ctrl-Z if decision 2 keeps ISIG on. If it
happens mid-paste, the bytes still sitting in the tty input queue belong to
whoever holds the terminal next -- the foregrounded shell will consume the
remainder of the paste as commands. Nothing in-process prevents this; it is what
happens to any raw-mode program that is suspended mid-input. The design does not
attempt to drain or re-inject. On resume the buffer holds what arrived before the
stop, and the recovery is R36's deletion plus a re-paste. Separately, an external
SIGSTOP cannot be caught at all, so the terminal stays raw for that stop's
duration; only the SIGTSTP path can pre-restore.

### What this requires of decision 2's ISIG choice

- **ISIG off (full `MakeRaw`).** Ctrl-C arrives as byte `0x03`, is handled
  synchronously in the read loop, returns the abandon sentinel, and unwinds
  through the normal defer to the ordinary error status (R31). The signal
  handler is still mandatory, because SIGTERM, SIGHUP, and an external `kill
  -INT` still arrive as signals. Measured both branches on a pty: `0x03` returns
  normally and `kill -INT` gives 130, and both leave the terminal restored --
  which is exactly R39's "the observable outcome does not [depend on which]".
- **ISIG on.** Ctrl-C arrives as SIGINT, indistinguishable from an external one.
  The SIGINT branch must then *not* re-raise -- it must feed the loop an abandon
  outcome and return through the normal path -- or a developer pressing Ctrl-C
  gets status 130 where R31 wants the ordinary error status. Only SIGTERM and
  SIGHUP re-raise. The cost is that an external `kill -INT` also exits 1 rather
  than 130, a worse Unix citizen.

Both work. ISIG off is the cheaper branch and the one I would build against.

### Where restoration sits relative to the `claude attach` handoff

Restoration belongs to the **capture function's own defer**, not to a defer on
`runDispatch`.

`dispatchAttach` (`internal/cli/dispatch.go:100-110`) hands `os.Stdin`,
`os.Stdout`, and `os.Stderr` straight to `claude attach` and calls `cmd.Run()`.
Claude will call its own `MakeRaw`. If it inherited a raw terminal, it would
record *our raw state* as its pre-state and "restore" to raw when it exits -- the
leak would outlive niwa entirely and land on the developer's next command, which
is precisely the cost R9 exists to prevent.

The placement makes this automatic. The capture runs in the zero-argument branch
at the top of `runDispatch`, before the workspace classification at
`dispatch.go:152-163` and well before provisioning at `dispatch.go:217`; the
attach is step (14) at `dispatch.go:391`. A defer scoped to the capture function
unwinds roughly 250 lines and one provisioning before the attach inherits
anything. It also gives R7 and R26 for free on the signal paths: nothing durable
has been created yet, so dying inside the capture cannot leave an instance.

`signal.Stop(ch)` and `signal.Reset(...)` must be scoped to the capture for the
same reason. If our `Notify` were still live during the attach, a Ctrl-C aimed at
the attached Claude session would also be delivered to niwa, and our handler
would restore-and-re-raise, killing niwa out from under the attach. Note that
`dispatchAttach` does no signal forwarding of its own -- unlike
`sessionattach.Supervise` (`supervise.go:58-76`), which forwards to the child's
process group -- so it relies on the child sharing niwa's foreground process group
and the tty signalling both. That only behaves if niwa has restored default
dispositions first.

One rule to state in the code: the capture must never call `os.Exit`.

Teardown order on every exit: DECRST 2004, `term.Restore(fd, preState)`,
`signal.Stop`, `signal.Reset`.

## Why the alternatives lose

**A (defer only)** loses on the requirement text, not on judgment. Go's own
documentation says SIGHUP, SIGINT, and SIGTERM cause the program to exit, and
that exit runs no defers. R9 names all three. R38 is unreachable.

**B (defer plus signal handler)** loses on a fact that only measurement
surfaces. Written from the textbook, its SIGTSTP branch calls `signal.Reset` and
re-raises SIGTSTP, and the process never stops -- Ctrl-Z appears to do nothing.
That is a worse failure than not handling suspend at all, because it is
invisible: it passes code review, and it passes the PRD's R38 criterion as
currently worded (see below). B is recoverable -- swap the re-raise for SIGSTOP
and it becomes correct -- but as a design it does not tell the implementer that
the swap is necessary, and it puts termios mutations on a goroutine racing the
renderer.

**C** costs one type and one goroutine. The goroutine is not really a cost: any
design that handles signals synchronously needs a reader goroutine, because a
blocking `read(2)` cannot be interrupted. In exchange the SIGCONT branch is the
natural home for the re-enter-raw step, which is where step 3 above shows it has
to be.

## Risks

- **The SIGSTOP substitution reads like a bug.** A future contributor will see
  `Kill(getpid(), SIGSTOP)` in a SIGTSTP handler and "fix" it to the textbook
  form, silently breaking suspend. It needs a comment citing the `_SigDefault`
  mechanism and the measurement. Low-severity but near-certain without one. (If
  Go ever fixes `Reset` for these signals, SIGSTOP keeps working; only the
  rationale goes stale.)
- **The stop is asynchronous.** `kill()` returns before the process stops, so any
  refactor that inlines the re-enter-raw step after the raise -- instead of
  leaving it in the SIGCONT branch -- reintroduces a race in which the terminal
  goes raw before the process actually stops.
- **Mid-paste suspension loses the tail of the paste** to the foregrounded shell.
  Unfixable in-process; R36 is the recovery.
- **SIGKILL and external SIGSTOP leave the terminal raw.** Uncatchable. Prior
  measurement: bash repairs it at its next prompt, dash does not.
- **Backgrounding leaks a stop.** `niwa dispatch &` passes R20's gate (stdin is
  still a tty) and then calls `tcsetattr` from a background process group, which
  raises SIGTTOU and stops the job before anything renders. Observed during this
  investigation. The developer sees a stopped job rather than a broken terminal,
  so severity is low, but it deserves a sentence in the design.
- **Restoring to `preState` on resume deliberately discards** any `stty` the
  developer ran while suspended. Correct per R9's "its state before the capture
  began", but it is a choice, not an accident.
- **If decision 2 lands on ISIG on**, the SIGINT branch diverges as described;
  getting it wrong costs R31 rather than R9, so it will not show up in a
  terminal-state test.

## Testability

The assertion mechanism is confirmed implementable: with a pty, the harness reads
the line discipline from the **master** fd (master and slave share it on Linux),
so a Go functional test using creack/pty can sample before, during, and after
without a shell inside the pty. `stty -g` from inside the pty works too. R27's
required "waiting for input" text doubles as the harness's readiness signal
before it sends anything.

**Gets a real test** -- all of these were exercised in the harness built for this
decision, so they are known-writable, not merely plausible:

- R9 submit, abandonment, empty-capture refusal: compare tty mode before and
  after. To exercise submit without provisioning, run outside a workspace -- the
  capture completes, then `dispatch.go:160` fails. Honest limitation: this tests
  the capture's restore, not a fully successful dispatch.
- R9 SIGTERM and SIGHUP; R39 SIGINT-as-signal and SIGINT-as-byte. All four
  measured restoring correctly, with the wait-status distinction (143, 129, 130,
  and a normal return) visible to the harness.
- R7 on each of those paths: assert no instance directory appeared.
- R38, **but the PRD's criterion is vacuously passable as written.** "The
  terminal's mode after the capture is suspended and resumed matches its mode
  before" is satisfied by an implementation with no SIGTSTP handling whatsoever,
  because the final defer restores anyway -- and it is satisfied by option B's
  broken handler, which never suspends. The assertions that actually test R38:
  while the process is stopped (`ps` state `T`), the tty's mode equals the
  pre-capture mode; and after SIGCONT the capture still accepts input, proving
  raw was re-entered. Recommend strengthening the criterion.

**Verified by inspection only**, and I would not pretend otherwise:

- SIGKILL and external SIGSTOP leaving the terminal raw -- uncatchable.
- Signal handlers being torn down before the attach handoff -- needs a real
  `claude` to observe end-to-end. Partially recoverable with a unit test over a
  seam that records Notify/Stop/Reset calls.
- Panic-unwind restoration.
- stdin and stderr on different ttys.

**Complement with unit tests over an injected terminal-mode interface**
(`MakeRaw`/`GetState`/`Restore` plus a signal source), asserting the call
*sequence*: raw entered once, restored before the stop is raised, re-entered on
continue, and restored exactly once on each exit path. Those tests pin the
discipline and run everywhere; the pty tests pin the reality and run where the
PRD already says the terminal-driven scenarios run.

## Summary

The capture puts the **stdin** fd into raw mode -- not the stderr fd
`picker.go:82` uses -- renders to stderr, and holds two saved states: the
pre-capture state that every exit path restores, and the raw state that a resume
returns to. Restoration runs from a defer scoped to the capture function plus a
`select`-driven handler for SIGINT, SIGTERM, SIGHUP, SIGTSTP, and SIGCONT, all
torn down before `runDispatch` ever reaches `claude attach`, so the attach
inherits a clean terminal and default signal dispositions. The one non-obvious
piece is suspend: `signal.Reset` provably fails to restore SIGTSTP's default
disposition in Go, so the handler restores the terminal and raises **SIGSTOP**
instead, leaving the re-entry into raw mode to the SIGCONT branch -- a sequence
measured working end-to-end on a pty, unlike the textbook idiom, which silently
does not suspend at all.
