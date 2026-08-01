# /prd Decisions: dispatch-paste-prompt

Running in `--auto`. Each decision follows the research recommendation unless
noted; alternatives and rationale are carried into the PRD's Decisions and
Trade-offs section, which is the closure surface for the BRIEF's open questions.

## Closing the BRIEF's four open questions

1. **Size ceiling** -- 130,433 bytes, stated as the derivation
   `(32*4096 - 1) - len(keepAliveArmingInstruction)` rather than a literal.
   Alternatives: a round floor such as 100 KiB (stable across transport
   changes but arbitrary), or raising the ceiling by changing the transport
   (out of scope for a requirements doc and touches the single-argv guarantee).
   Committing to failure-shaped pastes, which measure 5.6-9.2 KB; whole-log
   pastes (326-582 KB) are rejected with an actionable message.

2. **Terminal cannot carry the capture** -- split into two cases that differ in
   kind. Non-TTY stdin: refuse with guidance, exit 1, no read. Capability
   absent on a real TTY: no probe, no warning, degrade -- the requirement is
   that the submit rule stay safe when no paste markers arrive.

3. **Composition with --detach** -- they compose. No new rejected combination.
   The two exit shapes are the two the command already has.

4. **Non-interactive invocation** -- the single-argument path stays
   byte-identical and does not consult the TTY at all. The check lives only in
   the zero-argument branch.

## Decisions from research open questions

5. **Argument contract** -- `cobra.MaximumNArgs(1)`, matching `niwa destroy`'s
   optional-positional-with-interactive-fallback idiom. Rejected the `-`
   sentinel (no precedent in niwa, pulls toward the piping shape the BRIEF
   excludes) and an explicit opener flag (violates the no-mode-to-choose
   commitment). Makes "positional supplied alongside capture" impossible by
   construction.

6. **TTY gate is stdin AND stderr** -- following `destroy.go:239`. The capture
   renders to stderr; stdout carries dispatch's session hints and must stay
   redirectable. Cost: `niwa dispatch 2>err.log` from a terminal will not
   capture. Judged not worth supporting.

7. **`niwa dispatch ""` keeps erroring** -- an explicit empty argument is a
   caller bug, usually an unset variable. Turning it into a capture trigger
   would open an interactive prompt inside a script when `$TASK` is empty.

8. **No `--no-input` flag** -- the prompt argument already is the
   non-interactive channel, so the flag could only change which error prints.

9. **Capture lives in `runDispatch`, not `dispatchLaunch`** -- `niwa watch`
   reaches the launcher directly at `watch.go:579` and `watch.go:826`, so
   capture logic in the launcher would give a cron-driven review sweep an
   interactive read.

10. **Size-cap relationship to PR #226** -- #226 (closes issue #225) corrects
    the value and adds the launcher backstop independently. This PRD states its
    requirement over that baseline and adds only what #226 does not cover: the
    ceiling applying to the capture path, and the rejection wording.

11. **Single-line length** -- the testability probe measured that a single
    pasted line over roughly 4090 bytes hangs under canonical-mode line
    discipline. Stated as an explicit requirement rather than left to DESIGN to
    rediscover, since a minified stack frame or a long JSON line reaches it.

12. **Acceptance criteria at three levels**, with an explicit manual-
    verification line for what no harness in this repo can reach (terminals
    without DECSET 2004, tmux, rendering quality). Writing Gherkin against
    those would be decoration.

13. **PTY scenario count** -- two `@critical` scenarios plus one for terminal
    restoration. Each is a hang risk, so the count stays small and the PTY step
    gains a timeout.

## Round-2 jury findings

14. **Echo cost is superlinear on a single long line, and it is an
    implementation property rather than an inherent one.** The Phase 2 probe
    measured a 20,000-byte single line failing to complete in 20 seconds while
    12,000 completed, and attributed it to `x/term`'s per-character echo. The
    same probe round-tripped a 205 KB *multi-line* payload in 2.07 seconds, so
    the hazard is line length, not total size -- which matters, because a pasted
    log is many short lines and is the case the feature serves. The
    bracketed-paste research independently warned against that same library path
    for exactly this reason and recommended a reader that echoes a compact
    placeholder rather than replaying the payload. The PRD response is therefore
    a responsiveness requirement that constrains DESIGN away from the slow path,
    plus a Known Limitation recording the measurement against the specific
    library path, rather than lowering the ceiling to match one implementation's
    weakness.

15. **Rendering of captured input was never required.** The Problem Statement
    names the workaround's blindness as a defect, and R30 presupposed a render
    ("rendering MAY sanitize") that no requirement mandated. An implementation
    that read without echoing satisfied every requirement while reproducing the
    defect. Adding it as a requirement.

16. **A refusal that retains text needs a way to remove text.** R17 promised the
    developer could reduce their input, and nothing in the document gave the
    capture any means of deletion. The capability is a requirement; the gesture
    stays DESIGN's.

## Remaining unknowns carried into the PRD

- Whether a macOS-failing PTY scenario is acceptable (CI is ubuntu-only; the
  step errors rather than skips when `script -c` is not util-linux).
- The named set of terminals the manual-verification criterion covers.
- Whether Ctrl-C must produce a distinct cancel sentinel rather than `io.EOF`
  -- recorded as a requirement here because the house contract
  (`picker.go:26-30`) already draws that distinction, but DESIGN owns how.
