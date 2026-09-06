---
schema: plan/v1
status: Active
execution_mode: single-pr
milestone: none
issue_count: 8
upstream: docs/designs/DESIGN-worktree-setup-scripts.md
---

# PLAN: Worktree Setup Scripts

## Status

Active

Single-pr mode, so no GitHub issues or milestone were filed and the outlines
below are the unit of work. This file is deleted by the work-on cascade in the
same commit set that transitions the BRIEF and PRD to Done and the DESIGN to
Current.

## Scope Summary

Make a niwa worktree run its repo's setup scripts, and make a worktree-hook
event name that niwa does not consume report itself instead of failing silently
— without either change being able to reach the delegated create path's
teardown.

## Decomposition Strategy

**Horizontal.** The design's components have stable interfaces, and one is a
prerequisite for the rest for a reason the design argues explicitly: the event
set has to land before the setup step, or the setup step is written against a
single-event vocabulary and then reworked. A walking skeleton buys down
integration risk this change does not carry — the pipeline already calls
`ApplyToWorktree` per worktree, and this feature adds a step inside it rather
than a new runtime path between components.

**Execution mode: single-pr.** The repo declares no `## Delivery Preference:`
header, so `consolidated` applies and no split branch fires. None would fire on
the merits either: no cross-repo landing order, no workflow that must reach the
default branch before it can be invoked, no merge gate between steps. And the
units are not independently useful — a config switch nobody reads, an options
field nobody fills, and an event set the runner does not yet iterate each
deliver nothing to a reader who meets them alone. The first observably useful
unit is Issue 5, and it depends on three others.

**A note on how these criteria were written.** Three of the defects found while
scoping this feature were fixes that reproduced the bug they were fixing. So
each criterion below was attacked by constructing the wrong implementation an
engineer would plausibly reach for and checking the criterion fails against it.
That pass changed six criteria and split one outline in two. Where a criterion
looks oddly specific about a fixture, that is why.

## Issue Outlines

### Issue 1: Worktree hook events become a named set, validated and consumed from one place

**Complexity:** critical.

**Goal**: Make an event name niwa does not consume report itself, without the
report being able to destroy a worktree and without weakening the containment
control that shares the same error return.

**Acceptance Criteria**:

- [ ] A valid-event set is declared in one place beside `worktreeApplyEvent`,
      and both discovery and the hook runner read it. (R10, R12)
- [ ] A test adds a second event to that set and asserts `runWorktreeHooks`
      executes that event's scripts, with no edit to the runner. Without this,
      an implementation that validates against the set while still reading
      `hooks[worktreeApplyEvent]` passes every other criterion here — which is
      the original bug, blessed by validation instead of reported by it. (R12)
- [ ] `DiscoverWorktreeHooks` returns a diagnostic naming the offending path and
      the valid set when it meets an event name outside that set — for a
      directory name and for a `.sh` filename alike, so a typo is caught the
      same way. (R10)
- [ ] Discovery still returns the hooks it did find alongside that diagnostic. A
      config repo holding both a stale `worktree-hooks/create/` and a live
      `worktree-hooks/apply/` still runs the `apply/` hooks. (R10)
- [ ] `runWorktreeHooks` downgrades the unknown-event case to a warning on its
      `stderr` and returns nil. The match is `errors.Is` against a dedicated
      sentinel — not "discovery returned an error".
- [ ] A fixture holding **both** a stale `worktree-hooks/create/` directory and
      a `worktree-hooks/` symlink escaping `configDir` still fails
      `ApplyToWorktree`. A fixture with only the escape does not discriminate:
      an implementation that joins containment failures with unknown-event
      diagnostics and then matches the sentinel would pass it and still swallow
      the escape.
- [ ] With a config repo containing `worktree-hooks/create/01-x.sh`, the
      delegated `WorktreeCreate` path completes, the worktree it created is
      still present on disk, and no session is moved to a terminal state.
      Asserted by a test, not by argument. (R11)
- [ ] `TestDiscoverWorktreeHooks_TopLevelAndSubdir` asserts that a `create/`
      directory produces the diagnostic, rather than asserting its scripts are
      returned. (R10, R12)

**Dependencies**: None

### Issue 2: `WorktreeApplyOptions` gains the setup sink and its reporter and redactor inputs

**Complexity:** testable.

**Goal**: Give the setup outcome somewhere to go that is not an error return,
using the nil-tolerant idiom the struct already establishes.

**Acceptance Criteria**:

- [ ] `WorktreeApplyOptions` carries `Setup *SetupResult`, `Reporter *Reporter`
      and `Redactor *secret.Redactor`, each nil-tolerant.
- [ ] `WorktreeApplyOptions{}` behaves exactly as it does today: no reporter
      override, no redactor, no setup outcome reported. Every existing test
      compiles and passes unchanged. (R15)
- [ ] The precedence between `Reporter` and `Stderr` is documented on the fields
      and holds: `Reporter` when set, a reporter wrapping `Stderr` otherwise,
      and one wrapping `os.Stderr` when neither is supplied.
- [ ] No function signature in the package changes.

**Dependencies**: None

### Issue 3: The worktree fan-out moves below the clone's setup-script step

**Complexity:** testable.

**Goal**: Stop worktrees being provisioned against the previous apply's
clone-setup output, and pin the ordering so it is not silently reverted.

**Acceptance Criteria**:

- [ ] Step 6.6 runs after Step 6.75 in `runPipeline`.
- [ ] A pipeline-level test builds an instance with both a sessions directory
      and a repo with setup scripts, and asserts the clone's setup ran **before**
      the worktree fan-out — an ordering assertion, not a co-occurrence one.
      This test is the deliverable as much as the move is: nothing else in the
      suite observes the order, which is why the move is safe and also why it
      would be silently reverted.
- [ ] The existing worktree-refresh tests pass unchanged.

**Dependencies**: None

### Issue 4: A per-repo switch turns worktree setup on, defaulting off

**Complexity:** testable.

**Goal**: Let an operator opt a repo in, and make sure the opt-in survives every
path that copies config.

**Acceptance Criteria**:

- [ ] The switch exists on `WorkspaceMeta` and on `RepoOverride`, and a resolver
      mirrors `EffectiveReadEnvExample`'s cascade with an off default. (R3)
- [ ] A test asserts a repo's opt-in survives **a vault-resolved apply**.
      Omitting the field from `deepCopyRepos` must fail it — the opt-in would
      otherwise work on `worktree create` and silently not on `niwa apply`, and
      no linter or field-count guard in this repo catches it. This is not a
      hypothetical: `deepCopyRepos` already lists eleven of `RepoOverride`'s
      twelve fields, and the omitted one, `Codex`, is dropped to nil on that
      path today.
- [ ] A test enumerates `RepoOverride`'s fields, enumerates the literal's keys,
      and asserts they agree — the same both-sides instrument the design uses for
      the redactor walk. The pre-existing `Codex` omission is fixed as part of
      this, because the guard cannot be added without fixing it: the test fails
      against the tree as it stands. Leaving the bug and skipping the guard
      would ship this feature's own field into a copy function with a known
      silent-drop defect and no protection.
- [ ] An opt-in on repo A does not cause setup to run for repo B. (R3)
- [ ] With no opt-in anywhere, nothing about the clone path changes. (R15)

**Dependencies**: None

### Issue 5: Setup scripts run against a worktree

**Complexity:** critical.

**Goal**: The thing the feature is for — a worktree of an opted-in repo is
provisioned by that repo's own scripts, on create and on apply, and no failure
can destroy it. This outline runs and gates; Issue 6 reports.

**Acceptance Criteria**:

- [ ] A repo with an opted-in `scripts/setup/` containing an executable script
      has that script's effect present in a worktree after
      `niwa worktree create`, and the script ran with the worktree as its
      working directory. (R1, R2)
- [ ] The same holds after `niwa apply` for a worktree that existed before the
      script was added. This is what defeats a create-only implementation. (R2)
- [ ] A script that dumps its environment records the worktree path, the repo
      name and the instance root when run in a worktree, and at least one of
      those is absent in the clone run, so presence alone distinguishes them.
      (R4)
- [ ] A repo whose `scripts/setup/` holds two scripts — one that gates itself on
      the worktree signal and exits early, one that does not — runs only the
      second in a worktree and both in the clone. This is the mixed-directory
      case the whole two-mechanism opt-in exists for; without it nothing checks
      the signal is usable for what it was added for. (R4)
- [ ] The instance-root anchor is exported on the clone path too, so upward path
      arithmetic stops being the only way to find it. (R4, R15)
- [ ] The existing setup-script test suite passes unchanged, including the
      clone-path environment change above. (R15)
- [ ] No value declared under `env.secrets`, and no value of a `vars` key whose
      configured form is a `vault://` reference, appears in a setup script's
      environment on any surface — asserted against a fixture that declares one.
      Phrasing it against the registered redactor set instead would be vacuous
      on the standalone surfaces, where that set is empty until Issue 7. (R5)
- [ ] `NIWA_RESPONSE_FILE` is absent from a setup script's environment on every
      surface where setup runs, asserted by a test. See the precondition note
      below — this outline owns it.
- [ ] A script that exits non-zero in a worktree created through the delegated
      `WorktreeCreate` path leaves the worktree on disk and its session record
      non-terminal — whether it wrote only git-ignored output or nothing at all.
      (R6)
- [ ] That failure leaves the exit code of `niwa worktree create`,
      `niwa worktree apply` and `niwa apply` unchanged from the
      no-setup-script case. (R7)
- [ ] A repo with no opt-in spawns no setup process in any worktree, on any
      surface. (R17)
- [ ] A single `niwa apply` over a repo with three live worktrees runs that
      repo's setup at most three times in worktrees, in addition to the
      unchanged clone run. (R16)

**Precondition this outline owns.** `captureNiwaResponseFile` never runs for the
worktree commands, because cobra executes only the first persistent hook it
finds walking up from the leaf and `sessionCmd` declares its own. So scripts
currently inherit a writable `NIWA_RESPONSE_FILE`, which redirects the
operator's shell. Extending that to repo-authored scripts is not acceptable, and
deferring it would block the feature — so if the separate fix has not landed
when this outline is implemented, this outline lands the minimal fix itself
rather than disabling setup on the surfaces the criteria above require it on.
The earlier framing left those in contradiction; this resolves it in favour of
fixing the precondition.

**Dependencies**: Issue 1, Issue 2, Issue 4

### Issue 6: The setup outcome is reported on every surface

**Complexity:** testable.

**Goal**: Make a setup failure visible to whoever is standing there. Issue 5
fills the sink; nothing yet reads it, and an implementation that stops at Issue 5
runs setup while failing silently on the two interactive commands and the
delegated create — this feature's own defect class, on the surface its first
user story lives on.

**Acceptance Criteria**:

- [ ] A failing setup script during `niwa worktree create` produces a diagnostic
      on stderr naming the worktree and the failing script, at the unchanged
      exit code. (R6, R7, R8)
- [ ] The same for `niwa worktree apply`. (R6, R7, R8)
- [ ] The same for the delegated `WorktreeCreate` path — and that path's
      **stdout is byte-identical to the no-setup case**, still carrying only the
      worktree path. An implementation that reports the outcome on stdout would
      satisfy the criterion above and break the hook contract.
- [ ] A repo whose setup fails in its clone and in two of its worktrees produces
      a verdict naming three distinct locations rather than the same repo name
      three times. (R8)
- [ ] The fan-out's three skip warnings say that setup did not run, not only
      that env refresh was skipped.

**Dependencies**: Issue 5

### Issue 7: Output from a worktree is scrubbed

**Complexity:** critical.

**Goal**: Make redaction hold where the process resolves no secrets, and close
the unredacted path already open next door.

**Acceptance Criteria**:

- [ ] A secret value present in a worktree's inherited env file, echoed by a
      worktree **setup script**, does not appear unredacted in niwa's output on
      any surface where setup runs. (R9)
- [ ] A secret value echoed by a worktree **hook script** likewise does not
      appear unredacted. Without this the routing change below passes against an
      implementation that hands the choke point a nil redactor, and the live
      unredacted path the design set out to close stays open.
- [ ] The registered set is derived from `walkVaultRefsForUnknownProvider`'s slot
      enumeration rather than from a list of table names maintained beside it.
- [ ] A key in the `vars` table whose configured value is a `vault://` reference
      has its resolved value registered. The mechanism is the syntactic prefix
      in `cfg`, not persisted source provenance — `SourceEntry` carries no
      mapping from an env key to its kind.
- [ ] Ordinary configuration is not over-scrubbed: a workspace with
      `NODE_ENV=production` does not see "production" replaced in script output.
- [ ] The existing minimum-fragment-length guard remains in force underneath, so
      a short declared value does not become a scrub of every occurrence of that
      string. (R9)
- [ ] A test enumerates the secret-capable config slots and asserts the walk
      covers them, so narrowing the walk breaks a test named for redaction
      rather than silently shrinking coverage.
- [ ] `runWorktreeHooks` routes its scripts' output through the same choke
      point.

**Dependencies**: Issue 2, Issue 5

### Issue 8: The setup-script contract is published

**Complexity:** simple.

**Goal**: Write down the contract this feature relies on, where the person who
has to honour it will read it.

**Acceptance Criteria**:

- [ ] A repo author reading the guides — not a design document — can find the
      working directory a setup script gets, what it may assume about it, the
      environment it receives, what is deliberately removed from that
      environment, the ordering, the failure policy, and the idempotency
      requirement. (R13)
- [ ] The contract states that `niwa apply` skips worktrees currently in use and
      names `niwa worktree apply` as the override.
- [ ] The contract shows how a script gates itself on the worktree signal, since
      that is the only way a mixed `scripts/setup/` works correctly.
- [ ] `docs/guides/worktree.md` no longer claims `create` and `apply` need no
      network access, and states the secret-resolution guarantee instead. (R14)

**Dependencies**: Issue 6, Issue 7

## Dependency Graph

## Implementation Sequence

**Critical path:** 1 → 5 → 7 → 8, with 6 branching off 5 and rejoining at 8.
Redaction needs setup output flowing, which is Issue 5, not the reporting in
Issue 6 — so those two are siblings rather than sequential, and either can go
first once 5 lands. The two critical-complexity units in the middle are where
the risk sits.

**Parallelizable:** 1, 2, 3 and 4 have no dependencies and can be done in any
order or at once. Three of them are small; the first is not.

Issue 3 has no edges in either direction. That is not an oversight: the step
relocation is independent of everything else here, and saying so is the point —
it can land at any moment, including first, and nothing waits on it.

**Why the order is what it is.** The event set goes first because the setup step
would otherwise be written against a single-event vocabulary and reworked when
the set arrives — the design says so, and it is also the unit that changes an
error path reaching teardown, which is better settled before anything new starts
travelling through it. Reporting is separated from running because they are
different work with different failure modes, and because a plan that bundled
them let an implementation satisfy every criterion while never reading the sink.
Redaction follows both, because it needs a choke point that only exists once
setup output is flowing. The contract is published last because it documents
what the earlier units settled; writing it first would mean publishing a
contract and then discovering the implementation could not honour it.
