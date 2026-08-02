# Decision 1 — Where the spill decision lives

## Chosen: in `realDispatchLaunch`, with the prompt parameter split in two

`dispatchLaunch(ctx, instanceDir, prefix, body string, passthrough, env []string)`.
`prefix` is niwa-authored text that never leaves argv; `body` is the developer's
bytes and is the only spill candidate. The launcher, in order: rejects an empty
`body`; if `len(prefix)+len(body) > maxArgStringBytes`, calls a package-level
`spillPrompt` seam to write `body` into the instance and swaps it for the
pointer plus excerpt; asserts the composed element against `maxArgStringBytes`;
then resolves the binary and execs.

## Why

Three requirements only make sense with the spill at the launcher. R48's
criterion drives `dispatchLaunch` directly with an oversized prompt and expects
a spill. R57 exists solely to keep R55 constructible "after R48 makes it
otherwise unreachable", which only happens if the spill precedes the assertion
in the same path. R59's collision case is `niwa watch`'s continuation, which
only reaches the seam. Placing the spill in `runDispatch` would require
rewriting all three.

Splitting the parameter is what lets the deep placement satisfy R58. Today
`dispatch.go` concatenates `keepAliveArmingInstruction + prompt` before calling
the launcher, so by the time the launcher sees the string the distinction R58
needs has already been destroyed. A launcher that prefix-matched the keep-alive
constant to strip it back off would couple the exec layer to the keep-alive
feature and break silently the day a second prepend is added.

## Rejected

- **Spill in `runDispatch` only.** Cheapest by a wide margin and touches no
  existing test, but the watch paths would refuse rather than spill, R59 loses
  its motivation entirely, and R57's seam becomes decorative.
- **Spill in the launcher without splitting the parameter.** Cannot be built.
  The launcher would write the arming instruction into the spill file and hand
  the worker a pointer that arms nothing — exactly the failure R58 forbids.
- **A shared helper called by all three callers.** Preserves the existing test
  surface and does put the spill on the watch paths, but R48 stays a discipline
  rather than a property: a fourth caller that forgets the helper does not
  spill, it refuses, which is what R43 forbids.

## Verified against the code

- The launch at `dispatch.go:427` is inside the rollback window armed at
  `dispatch.go:319-324`.
- `stageReview` (`watch.go:758`) has its own `success` flag and destroy defer at
  `watch.go:783-793`.
- **`continueReview` (`watch.go:507`) has no `success` flag and no defer at
  all.** It launches at `watch.go:579` into `rec.InstancePath`, an instance it
  did not create; disposal is `pruneStagedRecords` at `watch.go:716`.
- The R55 assertion at `dispatch_launcher.go:40-42` fires before
  `exec.LookPath` at line 44, which is why the existing test never spawns
  anything.
- Both watch prompts are fixed templates carrying no niwa prepend.

## Main weakness

The most test churn of any option, and not cosmetic. `installDispatchFakes`
(`dispatch_test.go:85`) and `captureLaunchPrompt`
(`dispatch_wiring_keepalive_test.go:16`) replace `dispatchLaunch` wholesale, so
composing the final argv element below that stub makes every new spill
criterion invisible to command-level tests. The fix is a second, lower seam
inside `realDispatchLaunch` wrapping `cmd.Run()`, so a test can run the real
launcher and assert over the actual `exec.Cmd`. That is a net gain for R55's
criterion — it becomes an assertion over real argv rather than over a caller's
intent — but it is two seams where there was one, plus roughly eight stub sites
to update.

## Forces changes to

1. **R50's criterion** must name the spill decision helper, not the enclosing
   launcher: `realDispatchLaunch` calls `exec.LookPath` (reads `PATH`) and
   `os.Environ()`, so "no environment lookup in the call graph" fails if
   asserted over the whole function. The requirement text stands.
2. **R53** is scoped to "a dispatch that fails after the spill", and
   `continueReview` is not a dispatch and has no rollback window. Either widen
   it to "a launch that fails after the spill", or state that the launcher
   removes the spill file on exec failure only.
3. **R60** grows: `dispatch_launcher.go` becomes the home of several
   requirement citations rather than the one comment R60 anticipates.

## Also disappears under R45

`dispatchPromptReserve` (`dispatch.go:110`), `maxPromptBytes` (`dispatch.go:117`),
the size branch of `validateDispatchPrompt` (`dispatch.go:656-658`), and the
reserve assertions in `TestDispatchPromptLimits_DerivedFromExecLimit`
(`dispatch_promptsize_test.go:33-40`). The capture's limit argument at
`dispatch.go:263` switches from `maxPromptBytes` to the R49 backstop.
