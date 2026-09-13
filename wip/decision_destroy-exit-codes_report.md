<!-- decision:start id="destroy-exit-codes" status="confirmed" -->
### Decision: Which exit codes `niwa worktree destroy` returns for no match and ambiguity

**Context**

The PRD's outcome contract (R9) gives `niwa worktree destroy` two outcomes
that today have no code of their own: the value matched no session and no
worktree, and the value is ambiguous. The draft assigns 3 and 4. Both numbers
are already taken elsewhere in the same command family: `attach` and `detach`
return 3 when the attach lock is held (`internal/cli/sessionattach/attach.go:91,99`)
and `detach --force` returns 4 when it killed a live holder
(`detach.go:98`). So the same number would mean two different things depending
on which subcommand produced it.

Research established the rest of the landscape. Every non-zero code funnels
through `Execute` in `internal/cli/root.go:82-108`, which special-cases two
error types and sends everything else to `os.Exit(1)`; 0, 1 and 2 are taken
family-wide. There is no constants block, no allocation policy, and no
decisions directory. A cross-command collision already exists independent of
this design: `niwa init` reuses 3 and 4 for unrelated meanings
(`init.go:518-526`, `init_classifier.go:60-70`). Nothing in the repository
branches on a worktree-family code: only
`test/functional/features/init_bootstrap_failures.feature` asserts specific
numbers, and only against `niwa init`. The one external consumer of any niwa
exit code is `niwa watch guard-fs`'s exit 2, read by a hook protocol and
unrelated to destroy.

One fact cuts across every option. `destroy --by-path <path>` that resolves
nothing exits 1 today (`session_lifecycle_cmd.go:519-522`), and the PRD's R10
row keeps that "as today" while giving the positional form's no-match a new
code. Two paths to the same logical outcome would exit differently depending
on which flag located the target.

**Assumptions**

- Per-subcommand outcome tables are the codebase's existing documentation
  pattern, not a new concession: `PRD-niwa-session-attach.md:436-448` and
  `docs/guides/worktree.md:528-536` are the prior art.
- Adding a code later is compatible in a way that removing one is not: a
  script testing for generic failure keeps working when 1 is split, but a
  script testing for 3 breaks when 3 is renumbered.

**Chosen: A, with the `--by-path` inconsistency fixed inside `destroy`**

`destroy` returns 3 for no match and 4 for ambiguity, scoped to that
subcommand. `attach`, `detach` and `init` are untouched. The worktree guide
carries a second outcome table, bound to the subcommand that owns it rather
than presented as family-wide, and `destroy` gains inline `--help` text naming
its codes the way `attach` already does.

The one change beyond the draft: `--by-path` that resolves nothing also exits
3, through the same error. That stays inside `destroy`, the command this work
was sent to fix, so it is R9's own mandate rather than the cross-command
renumbering the brief forbids. It needs a PRD edit — R10's `destroy --by-path`
row currently reads "As today".

**Rationale**

A costs nothing today and buys the distinction the design's own rationale
names: an automated cleanup sweep after a reaper pass, where no match is the
expected steady state and ambiguity means stop and let a human look. No
consumer anywhere reads these numbers, so the risk is a future reader carrying
`attach`'s meaning of 3 into `destroy` — a documentation-layout problem, fixed
by keeping each table inseparable from its subcommand.

B's promise is the reason to reject it. "One code, one meaning" does not hold:
`init` already squats on 3 and 4, and nothing stops it or a future command
claiming 5 and 6 too. B delivers uniqueness within `niwa worktree` only, while
picking numbers a reader has no way to guess, and it leaves the `--by-path`
split standing. Paying in obscurity for a property the option cannot deliver
is the worst trade on the table.

D's best argument is real and is the reason the `--by-path` fix is adopted
here: collapsing everything into 1 would make `destroy` internally consistent
for free. But it pays for that by deleting the distinction, leaving a caller
to match stderr text — a weaker contract, and one nothing marks as stable, so
a later wording fix breaks it silently. A reaches the same consistency by
moving `--by-path` up to 3 rather than moving the positional form down to 1,
and keeps the numeric contract.

C is the only option that addresses the real underlying defect — niwa has no
exit-code policy at all — and it does not fit here. Renumbering codes
`attach`, `detach` and `init` already return is a behavior change to shipped
commands this work was not sent to touch, the brief forbids widening scope,
and a safe version needs a deprecation window rather than a renumber. It goes
to the coordinator as a follow-up proposal.

**Alternatives Considered**

- **B. Codes nobody else in the family uses (5 for no match, 6 for
  ambiguous).** Rejected. The uniqueness it promises holds only within
  `niwa worktree` — `init` already uses 3 and 4 for other meanings and nothing
  reserves 5 and 6 — so it buys unguessable numbers for a guarantee it cannot
  make, and leaves `--by-path` at 1. No hazard was found for numbers 5 and 6
  against shells or CI; the only threshold-aware code in the repo is attach's
  clamp at 125, kept clear of the 126-255 band.
- **C. Unify the family: one meaning per code, a constants block, a documented
  policy.** Rejected for this PR, proposed as a follow-up. Scope would reach
  `sessionattach/attach.go`, `detach.go`, `session_attach_register.go`, a new
  `internal/cli/exitcode` package, and — if `init` is folded in —
  `init_classifier.go`, `init.go`, `workspace/preflight.go`, plus the attach
  PRD's Exit Code Mapping table, the worktree guide, and
  `init_bootstrap_failures.feature`, which asserts exact codes. Not
  backward-compatible as a renumber: a script reading 3 as "lock held" would
  misread a reused code silently.
- **D. No new codes; both outcomes exit 1, distinguished by stderr.**
  Rejected. It would make R9's table and six acceptance criteria
  non-compliant, and it downgrades a machine-readable outcome to text
  matching for the cleanup-sweep case the design names. Its consistency
  argument is adopted instead, in the direction that keeps the contract.

**Consequences**

- `destroy` has its own documented code table; the guide gains a second one
  and `destroy --help` names its codes. The family stays non-uniform, and the
  docs say so rather than implying otherwise.
- PRD edit required: R10's `destroy --by-path <path>` row changes from "As
  today" to the no-match code, and R9 gains the matching row. The PLAN's
  Issue 6 and Issue 7 acceptance criteria pick it up.
- One behavior change beyond the draft: `--by-path` that resolves nothing
  moves from exit 1 to exit 3. No test, script, hook or CI step in the
  repository asserts on it. Exit codes are a user-visible contract, so this is
  announced as a behavior change rather than folded in as a fix: the pull
  request body carries it under its own "Behavior changes" heading, and it is
  flagged for the release notes. The repository has no `CHANGELOG` file, so the
  release notes are where a user meets it.
- Follow-up proposed to the coordinator (not filed): a niwa-wide exit-code
  policy with a constants block and a deprecation window for the codes
  `attach`, `detach` and `init` already return.
<!-- decision:end -->
