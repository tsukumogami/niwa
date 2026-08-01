# /prd Scope: dispatch-paste-prompt

## Problem Statement

Developers using `niwa dispatch` cannot hand a multiline error or log to a
dispatched worker: the prompt is a single positional argument, so the text must
be retyped, summarized down to a guess, or wrapped in shell quoting that has to
be right on the first try. The workaround that exists (`-d "$(cat)"` plus
Ctrl-D) requires three undiscoverable pieces of knowledge and forces detachment,
which drops the developer at a shell instead of the session they wanted to
watch. The framing is settled in `docs/briefs/BRIEF-dispatch-paste-prompt.md`
(Accepted); this PRD owns the requirements contract.

## Initial Scope

### In Scope

- Interactive capture of a multiline prompt in the terminal the developer is
  already in.
- One gesture serving both the bare paste and the annotated paste.
- A stated size ceiling, enforced where the developer can still recover their
  input.
- Clean abandonment: terminal restored, workspace unchanged.
- Preserving the default attach behavior.
- Correcting the existing prompt size cap, which is load-bearing once the
  ceiling is a stated requirement.

### Out of Scope

- `$EDITOR`, a clipboard flag, a prompt-file flag (excluded in the BRIEF as
  user-facing properties, not as implementations).
- Scripted piping as a design driver, though non-hanging behavior is required.
- Changing how the prompt reaches the worker after capture.
- Prompt synthesis (the `/dispatch` skill owns that).
- The capture mechanism, submit gesture, and newline chord -- DESIGN owns
  these, and the PRD must state requirements without picking them.

## Research Leads

1. **What size ceiling is defensible, and on what evidence?** The BRIEF says the
   PRD must state the ceiling rather than inherit today's value, because the
   current one is wrong in both value and coverage. Needs the per-platform
   argument-length math for both targets niwa ships (linux and darwin differ
   materially here), the size of the keep-alive text that is currently prepended
   after the check, and measured sizes of representative payloads (a Go panic, a
   failing `go test ./...` run, a CI log excerpt) so the number is grounded in
   what developers actually paste rather than in the limit alone.

2. **What should happen when the terminal cannot carry an interactive capture,
   and when stdin is not a TTY?** Two related non-happy paths the BRIEF
   deferred. Needs niwa's existing conventions for TTY detection and degraded
   interaction, plus what comparable CLIs do, to decide between refusing with
   guidance and falling back. The hard requirement is that a scripted or hooked
   invocation must never block.

3. **How does capture compose with dispatch's existing flag surface?** The BRIEF
   flags the `--detach` interaction as sitting directly against the in-scope
   commitment to preserve attach. Needs the full current flag enumeration, which
   combinations are coherent, what happens when a positional prompt argument is
   also supplied, and which combinations must be rejected with a clear error
   rather than silently resolved.

4. **What acceptance criteria are actually verifiable here, and by what
   harness?** Interactive terminal capture is the hardest thing in this repo to
   test. Needs the existing PTY-based functional-test harness, what the repo's
   `@critical` Gherkin convention requires for a user-facing CLI change, and
   which behaviors are unit-testable behind a seam versus needing a real
   terminal -- so the PRD's acceptance criteria are written against tests that
   can exist.

## Coverage Notes

All six coverage dimensions are carried by the Accepted BRIEF (who is affected,
current situation and workarounds, the gap, why now, scope boundaries) and by
the prior exploration round. The gaps this PRD must close are exactly the four
questions the BRIEF handed forward, which is what the leads target. Running in
`--auto`; decisions recorded in `wip/prd_dispatch-paste-prompt_decisions.md`.
