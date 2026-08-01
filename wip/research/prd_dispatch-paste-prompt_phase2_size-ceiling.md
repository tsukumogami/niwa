# Lead: What size ceiling is defensible for a pasted `niwa dispatch` prompt, and on what evidence?

All measurements below were taken on this machine unless labelled DOCUMENTED or
INFERENCE.

Measurement host: `Linux 6.8.0-124-generic #124-Ubuntu SMP x86_64`, page size
4096 (`getconf PAGESIZE` -> `4096`), `getconf ARG_MAX` -> `2097152`,
`ulimit -s` -> `8192`.

## Findings

### 1. What the current behavior actually is

`internal/cli/dispatch.go:75-81` declares the cap and states its rationale:

```
// maxPromptBytes guards against a prompt that would exceed the operating
// system's argument-length limit. ARG_MAX is at least 4096 on every POSIX
// platform and is typically far larger; a conservative bound below it
// leaves room for the binary path, the flags, and the environment, and
// fails clearly rather than letting exec truncate or reject the call with
// an opaque error (DESIGN Decision 8, R43).
maxPromptBytes = 128 * 1024
```

`internal/cli/dispatch.go:144-146` is the only enforcement point:

```go
if len(prompt) > maxPromptBytes {
    return fmt.Errorf("niwa: error: dispatch prompt is too long (%d bytes, limit %d); shorten it rather than relying on truncation", len(prompt), maxPromptBytes)
}
```

`len()` on a Go string is bytes, so the cap is a byte cap, not a rune or line
cap. Pasted text containing non-ASCII characters consumes its UTF-8 byte
length.

The prompt reaches `execve` as a single argv element.
`internal/cli/dispatch_launcher.go:66-72`:

```go
func buildClaudeBgArgs(prompt string, passthrough []string) []string {
	args := make([]string, 0, 2+len(passthrough))
	args = append(args, "--bg")
	args = append(args, passthrough...)
	args = append(args, prompt)
	return args
}
```

and `internal/cli/dispatch_launcher.go:41-42` hands that slice to
`exec.CommandContext(ctx, bin, args...)`. The prompt is never shell-interpolated
and never concatenated with anything else on the command line, so the constraint
that binds it is the **per-argv-string** limit, not the total argv+envp budget.

Two things follow. First, the justifying comment is wrong about the mechanism:
`ARG_MAX` bounds the *total* of argv plus envp, which is not what constrains one
oversized string, and "leaves room for the binary path, the flags, and the
environment" buys nothing for a single-string limit. Second, `128 * 1024` is not
a "conservative bound below" anything -- it is exactly Linux's `MAX_ARG_STRLEN`,
and that constant counts the NUL terminator (see 3), so the check admits a
prompt one byte larger than `execve` will take.

Verified end to end (`go run` against `/bin/true`, wrapping the error through
the same two `fmt.Errorf` sites niwa uses at `dispatch_launcher.go:53` and
`dispatch.go:335`):

```
n=131071 err=<nil>
n=131072 err=niwa: error: launching dispatch worker: dispatch: launching claude --bg: fork/exec /bin/true: argument list too long
n=131710 err=niwa: error: launching dispatch worker: dispatch: launching claude --bg: fork/exec /bin/true: argument list too long
```

MEASURED. A prompt of exactly 131072 bytes passes niwa's check and then dies at
exec with "argument list too long" -- precisely the opaque failure the comment
at `dispatch.go:79-80` claims the constant prevents. And by that point an
instance has already been provisioned; the failure lands after the expensive
step, not before it.

### 2. The keep-alive prepend and where it sits relative to the check

`internal/cli/dispatch_keepalive.go:33-35` defines
`keepAliveArmingInstruction` as a raw string literal ending in a blank line.
Its exact length, extracted from the source between the backticks and
byte-counted:

```
bytes: 638
lines: 2
repr tail: 'again, and then proceed with the task.\n\n'
```

MEASURED: **638 bytes**.

`internal/cli/dispatch.go:327` prepends it:

```go
prompt = keepAliveArmingInstruction + prompt
```

That line is inside step (9d), which runs *after* the instance has been
provisioned, because whether keep-alive arms depends on the instance's
materialized `[claude.settings]` (`readInstanceSettings(instancePath)` at
`dispatch.go:286`, `remoteControlEnabled` at `dispatch.go:326`). The size check
is step (1), at `dispatch.go:144`. So the prepend happens roughly 180 lines and
one instance-creation later than the only thing that validates length.

`grep -n "prompt =\|prompt +=\|+ prompt" internal/cli/dispatch.go` returns line
327 and nothing else, so this is the only post-check mutation of the prompt.

Consequence: the effective ceiling is not one number. With keep-alive off it is
131071 (the real exec limit) and the check at 131072 is off by one. With
keep-alive armed it is 131071 - 638 = **130433**, and the check is off by 639.
Every prompt in the band **130434..131072 bytes** passes step (1), gets an
instance created for it, and then fails at exec with `E2BIG` -- surfaced as
`niwa: error: launching dispatch worker: dispatch: launching claude --bg:
fork/exec .../claude: argument list too long`.

The comment at `dispatch.go:312-313` asserts the instruction's "fixed
few-hundred-byte size is well inside the conservative maxPromptBytes margin
validated in step (1)". There is no such margin: `maxPromptBytes` is *at* the
limit, not below it, so the reserve the comment assumes exists is zero.

### 3. The real per-platform limits

**Platforms shipped.** `.goreleaser.yaml` builds `goos: [linux, darwin]` x
`goarch: [amd64, arm64]`. Four binaries; no windows. Confirmed by reading the
file.

**Linux -- MEASURED.** `MAX_ARG_STRLEN` caps a single argv string independently
of the total `ARG_MAX`. Binary search over `fork` + `execv("/bin/true", [arg])`,
classifying by `errno == E2BIG`:

```
largest single argv string that exec's (bytes, excl NUL): 131071
131070 ok
131071 ok
131072 E2BIG
131073 E2BIG
```

The total budget is not binding here: `getconf ARG_MAX` reports 2097152, and
`xargs --show-limits` reports "Maximum length of command we could actually use:
2080828" with 7138 bytes of environment. So a 128 KiB single string is 6% of the
total budget but 100% of the per-string budget. The per-string cap is the
constraint, and it is the one the current comment does not name.

DOCUMENTED, and it explains the measured number exactly --
`/usr/include/linux/binfmts.h:15`:

```
#define MAX_ARG_STRLEN (PAGE_SIZE * 32)
```

`getconf PAGESIZE` -> 4096, so `MAX_ARG_STRLEN` = 131072 *including* the NUL
terminator, hence 131071 usable bytes. This is a compile-time kernel constant --
not tunable by `ulimit`, `sysctl`, or a stack-size change (raising `ulimit -s`
moves the *total* ARG_MAX, which is `min(stack/4, ...)`, and leaves the
per-string cap untouched).

INFERENCE from the same definition: 4 KiB is the smallest page size on any
platform niwa ships to, so 131071 is the **floor**. A 16 KiB-page arm64 kernel
gives `MAX_ARG_STRLEN` = 524288. Deriving the cap from `32*4096` is therefore
safe on every Linux target, never too generous on any of them.

**Darwin -- DOCUMENTED, not measured** (no darwin host available to me; say so
plainly rather than presenting these as measurements). XNU has no per-argument
string cap of the Linux kind. It bounds argv plus envp plus their pointer arrays
together against `ARG_MAX`. Current XNU
(`apple-oss-distributions/xnu`, `bsd/sys/syslimits.h`, tag `xnu-10063.101.15`,
fetched):

```
/* max bytes for an exec function */
#ifdef XNU_KERNEL_PRIVATE
#if defined(XNU_TARGET_OS_OSX)
#define ARG_MAX           (1024 * 1024)
#else
#define ARG_MAX            (256 * 1024)
#endif
#else /* XNU_KERNEL_PRIVATE */
#if defined(__ENVIRONMENT_MAC_OS_X_VERSION_MIN_REQUIRED__)
#define ARG_MAX           (1024 * 1024)
#else
#define ARG_MAX            (256 * 1024)
#endif
#endif /* XNU_KERNEL_PRIVATE */
```

So on macOS specifically the total is **1048576** (1 MiB); the 262144 figure
widely quoted for Darwin (and listed by the in-ulm.de ARG_MAX reference table
for Mac OS X 10.6, sourced from `<sys/syslimits.h>`) is the value for
non-macOS Apple targets in the current header, and the historical macOS value.
That reference table also confirms no Darwin per-argument limit is documented,
while explicitly calling out Linux's `MAX_ARG_STRLEN` of 131072 as an additional
per-argument limit since 2.6.23.

Note for the PRD: because macOS shares one budget between argv and envp, its
effective headroom for the prompt is `ARG_MAX` minus the environment. The
environment on this Linux host measured 7138 bytes; a developer shell with a
loaded environment can be tens of KiB. Even at 64 KiB of env, macOS leaves
roughly 980 KiB for the prompt -- still 7.5x the Linux per-string floor.

**Binding constraint, per platform:**

| Platform | Mechanism | Limit for niwa's prompt | Source |
|---|---|---|---|
| Linux (4 KiB pages) | `MAX_ARG_STRLEN`, per single argv string, NUL included | 131071 usable bytes | MEASURED + `binfmts.h:15` |
| Linux (16 KiB pages) | same | 524287 usable bytes | INFERENCE from `PAGE_SIZE * 32` |
| macOS | `ARG_MAX`, total argv+envp+pointers, no per-string cap | ~1 MiB minus environment | DOCUMENTED (XNU `syslimits.h`) |

Linux at 4 KiB pages is the tightest, by a wide margin. It is the number the cap
should be derived from.

### 4. What developers actually paste

All figures MEASURED, `wc -c` and `wc -l`, generated or fetched in this repo
today.

| Payload | Bytes | Lines | How produced |
|---|---:|---:|---|
| Go panic, nil deref, 60-frame call stack | 5,564 | 129 | `go run` of a recursive nil-deref program |
| Go panic, 300-frame recursion (Go elides past ~100 frames) | 8,865 | 206 | same, `recurse(300)` |
| `go test ./...` in this repo, all passing | 1,513 | 25 | run in the worktree |
| `go test ./...` with 3 induced failures incl. a nil-map panic | 7,679 | 141 | temporary test file, since removed |
| `go test -v ./internal/cli` | 73,355 | 1,277 | run in the worktree |
| `go test -v ./...` | **325,645** | 5,712 | run in the worktree |
| CI failure excerpt, `gh run view --log-failed` (run 29198309981, Tests) | 9,192 | 109 | real failed run in this repo |
| CI full job log for that failed run, `gh run view --log` | 33,197 | 366 | same run |
| CI full log, Tests workflow on `main` (run 30702666595) | **581,950** | 3,569 | real passing run |

The shape of this is the answer the PRD needs, and it cuts two ways.

**Failure-shaped pastes -- what the BRIEF actually describes -- are tiny.** The
BRIEF's motivating case is "a command has just failed, the error or stack trace
is on screen". Every payload of that shape measured between 1.5 KB and 9.2 KB:
a deep panic is 5.6 KB, a `go test ./...` run with three failures including a
panic is 7.7 KB, and the pre-filtered CI failure excerpt is 9.2 KB. Those sit
14x to 86x below a 130 KB ceiling. A developer pasting the thing that just broke
will not come close.

Go bounds the panic case structurally: the runtime elides frames past ~100 per
goroutine, so even 300 levels of recursion produced only 8,865 bytes. A single
goroutine's traceback cannot grow without bound. `GOTRACEBACK=all` on a
single-goroutine program produced the identical 5,564 bytes; a many-goroutine
server dump would be larger, but it is not the case the BRIEF is about.

**Whole-log pastes blow straight through it.** `go test -v ./...` in this repo
is 325,645 bytes -- 2.5x the Linux exec limit. A full CI job log is 581,950
bytes -- 4.4x. Both are things a developer plausibly selects and pastes; "here
is the entire verbose test run, figure out which one regressed" is a real
request. Any ceiling anywhere near 130 KB will reject them.

**The reachability change this feature introduces is the important part.**
Today the ceiling is nearly unreachable: `MAX_ARG_STRLEN` applies to the
caller's own exec of `niwa`, so a shell cannot hand niwa a positional argument
larger than 131071 bytes -- the shell's own `execve` fails first, with its own
"Argument list too long". The only band in which niwa's check can fire at all is
the 638-byte window the keep-alive prepend opened (finding 2), which is why the
wrong value has gone unnoticed. Inline capture removes that outer guard: a
prompt read from the terminal, stdin, or an editor buffer is bounded by nothing
but the terminal's paste buffer. INFERENCE, but a direct one: this feature is
what makes the ceiling reachable for the first time, and it is why the PRD must
state it rather than inherit it.

### 5. Where the check must sit

The check cannot simply move to just-before-exec, and it cannot stay where it is
unchanged.

It cannot move late, because the value of validating early is that rejection
happens *before* an instance is provisioned. Step (9d) is after provisioning by
construction -- it reads the instance's own materialized settings to decide
whether to arm (`dispatch.go:286`, `dispatch.go:326`). Validating the final,
possibly-prepended prompt would put the rejection after the expensive,
rollback-requiring step.

It cannot stay as-is, because the step (1) check validates a string that is not
the string `execve` receives.

The resolution is a declared reserve plus a backstop:

1. Step (1) validates `len(userPrompt) <= maxArgStringBytes - reserve`, where
   `reserve` is the compile-time length of everything that may be prepended
   after the check. Today that is `len(keepAliveArmingInstruction)` = 638. This
   makes one early check sound for both outcomes: whichever way (9d) resolves,
   the string handed to `execve` fits. The cost is 638 bytes of headroom
   surrendered on the unarmed path, which is 0.5% and not worth a second code
   path to recover.
2. `realDispatchLaunch` gets a length assertion on the string it is about to
   pass to `exec.CommandContext`. This is the coverage half of the fix: a future
   prepend that someone forgets to add to the reserve then fails naming niwa's
   limit, before exec, instead of surfacing as an opaque `E2BIG` after an
   instance exists.

The reserve must be derived (`len(keepAliveArmingInstruction)`), never a
hardcoded 638, so editing the instruction text cannot silently reopen the same
gap.

**Note: an open PR already implements exactly this.** `tsukumogami/niwa#226`
("fix(dispatch): cap the prompt below the exec limit and reserve the keep-alive
prepend", closes #225, author dangazineu, open at time of writing) derives:

```go
maxArgStringBytes     = 32*4096 - 1                      // MAX_ARG_STRLEN, minus the NUL
dispatchPromptReserve = len(keepAliveArmingInstruction)  // everything prepended after the check
maxPromptBytes        = maxArgStringBytes - dispatchPromptReserve
```

and adds the `realDispatchLaunch` backstop. Its numbers agree with everything
measured here. One factual refinement: the PR's body states macOS `ARG_MAX` is
262144; current XNU defines 1048576 for macOS targets and reserves 262144 for
non-macOS Apple platforms (quoted in finding 3). The refinement does not change
the PR's conclusion -- macOS is more permissive either way, so applying the
Linux number on both platforms remains right, and it keeps the accepted prompt
size identical everywhere, so a prompt that dispatches on Linux dispatches on
macOS.

The PRD should treat #226 as the corrected baseline it states a requirement
over, not as work still to be specified.

## Recommendation

**Ceiling: 130,433 bytes of user-supplied prompt text** -- derived, not chosen,
as `(32 * 4096 - 1) - len(keepAliveArmingInstruction)` = 131071 - 638. State it
in the PRD as a derivation with those two terms named, so a reader can check it
and so a change to either term visibly moves it. Present it to users in round
terms ("about 127 KB", or roughly 1,400-3,000 lines at the 43-91 bytes/line
measured across the payloads in finding 4) while enforcing the exact byte count.

**Where the check sits: two places, both required.**

- The user-facing check runs at prompt-acceptance time, before any instance is
  created, against `len(userText) + reserve`. For inline capture this means the
  check must cover every input path the feature adds -- interactive capture,
  piped stdin, editor buffer -- not just `args[0]`. Coverage is the half of the
  current bug that the value fix alone does not address.
- A backstop immediately before `exec.CommandContext` in `realDispatchLaunch`
  validates the actual argv string. Its job is not to catch users; it is to
  ensure that any future addition to the prompt that forgets to declare itself
  in the reserve fails with niwa's named limit rather than `E2BIG` after an
  instance exists.

**Margin: the 638-byte reserve is the entire margin, and no round-number fudge
should be added on top.** Justification: (a) the per-string limit is exact and
measured, not estimated, so there is no measurement uncertainty to pad against;
(b) `32*4096` is the platform floor -- every larger page size and macOS itself
are strictly more permissive, so the derived number is already the worst case;
(c) the total `ARG_MAX` budget is not binding on either platform (2 MiB measured
on Linux, ~1 MiB minus environment on macOS, against a 128 KiB string); (d) an
arbitrary margin would cost real payload capacity in the one dimension this
feature exists to serve, and the failure mode it would guard against does not
exist. The reserve, by contrast, guards a real and currently-live bug.

**One number on both platforms.** Do not make the cap platform-conditional. The
Linux figure is the tighter one, so it is safe on macOS; a uniform cap means a
prompt that dispatches on one platform dispatches on the other, and it avoids
modelling a macOS total-size budget whose environment term niwa does not
control.

**What the PRD should require of the error.** The rejection must be actionable
in a way "shorten it" is not, because at this size the developer pasted a whole
log and cannot eyeball what to cut. State the actual size, state the limit, and
name the concrete alternative -- put the text in a file and dispatch a prompt
that points at it. This matters more than the number: the measurements say the
ceiling is unreachable for the case the feature is for (failure-shaped pastes,
1.5-9.2 KB) and reachable only for whole-log pastes (`go test -v ./...` at 318
KB, a full CI log at 568 KB), so essentially everyone who ever sees this error
will be someone who pasted a whole log. The message is written for exactly that
person.

## Open Questions

1. **Is ~127 KB the right product answer, or only the right engineering
   answer?** 130,433 is what argv can carry. It is 14x the largest
   failure-shaped payload measured and 0.4x a full CI log. If the PRD wants
   whole-log pastes to work, the ceiling has to stop being an argv question --
   the prompt would need a different transport (a file the worker is pointed
   at, or stdin to `claude`). That is a DESIGN-level transport change with real
   consequences (it touches the D8 "single argv element, never shell
   interpolated" guarantee that `dispatch_launcher.go:16-22` rests on), and it
   is out of scope for a requirements doc to decide. But the PRD should state
   which of the two payload classes it is committing to serve, because the
   evidence says they are on opposite sides of the line. My read: commit to
   failure-shaped pastes, accept that whole-log pastes are rejected with a good
   message, and leave the transport alone. A human should confirm that.

2. **Should the ceiling be stated as a floor rather than an exact value?**
   Writing "at least 128 KiB of pasted text is accepted" as the requirement
   would let a future transport change raise it without amending the PRD, but
   128 KiB is not deliverable over argv (130,433 < 131,072). Either the
   requirement is stated as the derivation, or it is stated as a smaller round
   floor such as 100 KiB. The first is precise, the second is stable. Needs a
   call.

3. **Does the terminal capture path have its own limit below this one?** Nothing
   here measured what a bracketed-paste read loop can actually absorb, or how a
   terminal behaves when 300 KB arrives at once. If that ceiling is lower than
   130,433, it -- not `MAX_ARG_STRLEN` -- is the number the user experiences.
   This overlaps the bracketed-paste research lead and should be reconciled
   against it before the PRD fixes a number.

4. **PR #226 lands the corrected cap independently of this feature.** The PRD
   should confirm it is stating a requirement over #226's baseline rather than
   re-specifying it, and should say what it adds: coverage of the new capture
   paths, and the wording of the rejection.

## Summary

The current `maxPromptBytes = 128 * 1024` is wrong in both value and coverage:
it is exactly Linux's `MAX_ARG_STRLEN`, which counts the NUL, so the largest
single argv string `execve` accepts is 131071 (measured by binary search) and
the check admits 131072; and the 638-byte keep-alive instruction is prepended at
`dispatch.go:327`, long after the step (1) check at `dispatch.go:144`, so
prompts in the 130434..131072 band pass validation and then die at exec with
"argument list too long" after an instance has already been provisioned. The
defensible ceiling is `(32*4096 - 1) - len(keepAliveArmingInstruction)` =
**130,433 bytes**, enforced before provisioning against every input path the
capture feature adds and backstopped immediately before `exec.CommandContext`,
with the 638-byte reserve as the only margin -- Linux at 4 KiB pages is the
platform floor, and macOS (no per-string cap, ~1 MiB total) is strictly more
permissive, so one number covers both. Measured payloads say the ceiling is
comfortably out of reach for the case the BRIEF is about (a Go panic is 5.6 KB,
a failing `go test ./...` is 7.7 KB, a CI failure excerpt is 9.2 KB) and
unreachable-until-now in general, since the caller's own exec caps the
positional argument -- but inline capture removes that outer guard, and whole-log
pastes like `go test -v ./...` (325,645 bytes) or a full CI log (581,950 bytes)
will hit it, so the rejection message matters as much as the number.
