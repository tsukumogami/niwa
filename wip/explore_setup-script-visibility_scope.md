# Explore Scope: setup-script-visibility

## Visibility

Public

## Core Question

When a repo's setup script fails during `niwa create` or `niwa apply`, the operator
gets one deferred warning line and an exit code of 0 — the script's own stdout and
stderr never reach the terminal at all. What is the right way to make a failed setup
script both visible (its output readable) and discoverable (impossible to mistake for
success), without reversing the deliberate cross-repo resilience that Decision 2 of
`DESIGN-post-clone-scripts.md` chose?

## Context

Two separate defects sit behind niwa#239, and they are different kinds of thing.

**The output defect is a plain implementation/design divergence.**
`DESIGN-post-clone-scripts.md` Decision 2 promises "Stdout/stderr: printed to niwa's
output, prefixed with the repo name", and shows per-script sample lines. The
implementation routes every script line through `runCmdWithReporter`
(`internal/workspace/gitutil.go`) into `Reporter.Status()`, which returns immediately
when `!r.isTTY` (`internal/workspace/reporter.go:62`). Off a TTY — `niwa dispatch`, CI,
cron, any background agent — the lines are discarded. On a TTY they become spinner text
that is overwritten and then cleared by `stopSpinner`. There is no `--verbose` and no log
file. The same design doc's Security Considerations claim niwa "prints each script name
before execution"; it does not do that either.

**The exit-code question is a live design decision, not a bug.** Decision 2 deliberately
chose "warn on failure, stop on first script error" so one repo's failure does not block
other repos. `internal/workspace/apply.go:1590` implements exactly that via
`Reporter.DeferWarn`, which appends to a slice and never touches the return value. The
consequence is that a single-repo workspace whose only provisioning script fails hands
back an unusable instance and reports success. Three options are on the table (summary
line, non-zero exit, opt-in strict flag) and picking among them is the point of this
exploration.

Measured evidence from the issue, reproduced in-package rather than inferred:
`isTTY=false` and `isTTY=true` both report `script-failed=true` with
`stderr-line-present=false` and an empty captured buffer.

## In Scope

- Routing setup-script stdout/stderr to a durable channel in both TTY and non-TTY runs,
  with the repo-name prefix the design already promises
- The per-repo / per-script progress lines Decision 2 specifies but does not emit
- Choosing and justifying a discoverability mechanism for a failed setup script
  (summary line vs. non-zero exit vs. opt-in strict flag), with the reasoning recorded
  in a durable artifact rather than only a PR body
- Reconciling `DESIGN-post-clone-scripts.md` with whatever the implementation ends up
  doing, in the same change
- Regression coverage: a script that writes a known line to stderr and exits non-zero,
  asserted visible in both TTY modes; plus proof that stop-on-first-error-within-repo /
  continue-to-next-repo still holds

## Out of Scope

- Issue #231 ("Setup scripts don't run for worktrees") — adjacent and open, not folded in
- The downstream script that triggered the report; it is fixed elsewhere and is not
  niwa's concern
- Reworking `Reporter` wholesale. `Status`-is-a-no-op-off-TTY is correct for spinner
  progress. The defect is routing a script's real output through a transient-status
  channel. Fix the routing, not the reporter's contract, unless research turns up a
  compelling reason stated explicitly
- Any identifying detail of the private repo where this was found (see the hard
  constraint in the dispatch brief): no repo name, owner, PR or issue number, CLI name,
  file path, or session id may appear in any artifact this exploration produces

## Research Leads

1. **How should setup-script output be routed so it survives both TTY and non-TTY runs,
   given `Reporter`'s existing contract?**
   `Status` is deliberately a no-op off-TTY and deliberately transient on-TTY, and
   `runGitWithReporter` right next door already demonstrates a different pattern —
   classify lines, keep the diagnostic ones, attach them to the returned error. Whether
   the fix is a new `Reporter` method, reusing `Log`, buffering per script and replaying
   only on failure, or wrapping the error the way `runGitWithReporter` does determines
   how invasive this gets and how noisy a successful apply becomes.

2. **What is the real blast radius of a non-zero exit on setup failure?**
   Everything that shells out to `niwa apply` or `niwa create` and checks `$?` is a
   potential breakage: the SessionStart ephemeral-instance hook, `niwa dispatch`,
   `niwa worktree apply`, the functional test suite, `install.sh`, any CI workflow. This
   lead needs an enumerated list of callers with an assessment of what each does on a
   non-zero exit, because option 2 is only viable if that list is short and benign.

3. **What precedent does niwa already set for "partial failure" reporting, and is there
   an existing summary/exit-code convention to match?**
   Vault sync, materializers, worktree refresh, and `.git/info/exclude` enforcement all
   have to decide between warn-and-continue and fail-closed. If niwa already has a
   pattern — a summary block, a counted-failures line, a `--strict`-style flag — the
   right answer is to reuse it rather than invent a fourth convention.

4. **How much output does a real setup script produce, and does printing all of it break
   the apply UX?**
   Decision 2's sample output shows two tidy lines per repo, but a real script can be a
   dependency install spewing thousands. If unconditional pass-through is unusable, the
   design needs an explicit position — buffer and replay on failure only, cap the tail,
   or prefix-and-stream — and that position belongs in the design doc, not in an
   undocumented constant.

5. **What does the existing test suite already assert about setup scripts, and what
   shape does the regression test need to take to fail on today's `main`?**
   `internal/workspace/setup_test.go` covers ordering, stop-on-error, non-executable
   files and the skip paths, but nothing asserts on captured output. Knowing what is
   already pinned tells us which assertions we can strengthen versus which are new, and
   whether a `@critical` functional Gherkin scenario is warranted per the repo's
   testing convention.

6. **Which artifact does this actually need — an ADR for the exit-code decision, an
   amendment to the existing design doc, or both?**
   The dispatch brief is explicit that a decision living only in code is how this drifted
   in the first place. `DESIGN-post-clone-scripts.md` is a Current design whose Decision 2
   is exactly what is being revisited, so the choice is between amending it in place and
   writing a superseding decision record that points at it. The repo's own conventions
   for design-doc status transitions and ADRs decide this.
