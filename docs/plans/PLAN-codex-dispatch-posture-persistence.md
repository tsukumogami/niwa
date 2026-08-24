---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-codex-dispatch-posture-persistence.md
milestone: "Codex dispatch posture persistence"
issue_count: 5
---

# PLAN: codex dispatch posture persistence

## Status

Active

Tracking level is none: no GitHub issues or milestone are created for
this plan, so it transitioned to Active when authoring finished.
Execution mode is single-pr -- the five issues below land on one branch
and merge as one pull request. When the work completes, this file is
deleted in the same commit set that transitions the upstream documents.

## Scope Summary

Decomposes
[DESIGN-codex-dispatch-posture-persistence](../designs/DESIGN-codex-dispatch-posture-persistence.md)
into five implementable units: the re-entry construction file, the three
call sites rewired through it, the scan rule that enforces the one-path
invariant, the acceptance scenario against the real binary, and the
guide and spike updates. Requirements R1-R9 are owned by
[PRD-codex-dispatch-posture-persistence](../prds/PRD-codex-dispatch-posture-persistence.md);
each issue's acceptance criteria trace to them by number.

## Decomposition Strategy

**Horizontal.** The design's Implementation Approach is already five
layers, and this plan keeps them: the construction file, the call
sites, the scan, the scenario, the documentation. The constructor is a
strict prerequisite for all three call sites -- nothing can route
through functions that don't exist -- and the interfaces between the
units are stable and stated in the design (the three function
signatures in its Solution Architecture). These are layers, not
vertical slices: no unit delivers a thinner end-to-end version of the
feature on its own, and slicing by surface would put three copies of
the construction on the branch before the scan could forbid them.

A walking skeleton was not chosen because there is no new end-to-end
pipeline to de-risk. Dispatch, attach, the hint block, and `niwa list`
all exist and run today; this work moves one construction that already
happens inside them behind a single function. The end-to-end risk that
remains -- does the grant actually survive resume against the real
binary -- is carried by issue 4, not by an early skeleton.

## Issue Outlines

### Issue 1: feat(cli): re-entry constructor, renderer, quoter, and print gates

**Goal**: Land `internal/cli/dispatch_reentry.go` -- the argv
constructor, the printed-form renderer, the allowlist quoter, and the
fail-closed print gates -- as pure functions with unit tests and no
call site changed.

**Acceptance Criteria**:
- [ ] `reentryArgs` returns a literal expected argv for a fixture
  declaration carrying a distinctive grant, for one declaring no grant
  (R2), and for an empty working directory, which yields no grant
  arguments at all (R6).
- [ ] The quoter passes a table covering bare tokens, the empty string,
  braces, quotes, equals signs, spaces, `~`, `!`, and embedded single
  quotes; only tokens made of `[A-Za-z0-9_@%+=:,./-]` pass bare (R5).
- [ ] Print-gate cases -- control characters, escape sequences, and an
  invalid handle -- each yield no printed command (R5).
- [ ] For a fixture declaration carrying a grant AND three hint verbs,
  `reentryHints` returns the grant on the line whose verb matches the
  declaration's resume arguments and on no other -- the other two lines
  are literally today's `<binary> <verb> <handle>`. The real table
  cannot distinguish this from "every hint verb carries the grant",
  because the one agent that declares a grant declares exactly one hint
  verb, so only a fixture can fail on it (R1).
- [ ] Every line `reentryHints` returns, plus what `reentryCommand`
  returns, executed through `/bin/sh -c` against a stub that records
  the arguments it received, yields a literal expected argument vector
  written into the test (R5).
- [ ] No production call site changes; `go test ./...` stays green.

**Dependencies**: None

**Complexity**: testable

### Issue 2: feat(cli): route the three re-entry surfaces through the new file

**Goal**: Rewire `dispatchAttach`, the post-dispatch hint block with
its attach-failure fallback, and `sessionResumeCommand` through the
constructor and renderer, with per-surface fixture-spec regression
tests whose expected values are literal.

**Acceptance Criteria**:
- [ ] The attach exec builds its argv through the constructor; the hint
  block and the fallback print what the renderer returns; `niwa list`
  prints the renderer's single-command form -- every re-entry command
  carries the grant for the session's instance directory (R1).
- [ ] The attach criterion runs the real `dispatchAttach` body against
  a recording stub binary on PATH and asserts the recorded argv
  literally. `dispatchAttach` is a package variable tests replace, so a
  criterion satisfied by a constructor unit test would never reach the
  call site it is about (R1, R7).
- [ ] Removing the grant from each of the three surfaces independently
  fails a test, and those tests' expected values are written literally
  rather than derived from the production declaration table (R7).
- [ ] For a fixture agent whose declaration carries a distinctive
  literal grant, changing the grant in the declaration alone changes
  every re-entry command to match (R2).
- [ ] For a fixture agent declaring no grant, every surface's output
  equals its pre-change output literally (R2).
- [ ] With a recorded instance directory, the command names it inside
  the grant; with none recorded, the command carries no grant argument
  (R6).
- [ ] Each of `sessionResumeCommand`'s fail-closed cases still yields
  empty output: an agent that does not parse, a spec that does not
  resolve, an empty binary, and a missing handle on an agent whose
  verbs do not take the session id. Without this, a refactor can drop
  the preamble and print a command that fails at the binary while every
  other criterion passes (R1).
- [ ] After this issue, no non-test file in the package other than
  `dispatch_reentry.go` reads `ResumeArgs` or `HintVerbs` (R1).
- [ ] The five functional scenarios that assert the old printed form --
  in `agent-selection.feature`, `dispatch.feature`, two in
  `codex-agent.feature`, and `workspace-config-sources.feature` -- are
  updated rather than deleted, and at least one of them asserts the
  grant itself rather than only the surviving prefix. A scenario that
  loses its assertion instead of gaining the new one is a coverage
  regression, not a fix (R1).

**Dependencies**: Blocked by <<ISSUE:1>>

**Complexity**: testable

### Issue 3: test(cli): directory-wide scan rule for the re-entry fields

**Goal**: Extend the dispatch-path scan so the one-path rule is
enforced rather than hoped for: no non-test file in `internal/cli`
except `dispatch_reentry.go` may select either declaration field.

**Acceptance Criteria**:
- [ ] A rule in `dispatch_layout_test.go` fails any non-test file in
  the package other than `dispatch_reentry.go` that selects
  `.ResumeArgs` or `.HintVerbs`, ranging over the directory rather
  than an enumerated file list (R1).
- [ ] The rule is demonstrated red against a deliberately violating
  control fixture, and that control lands as a permanent case in the
  scan's own self-check rather than as a one-time manual demonstration
  -- a scan nobody has shown to fail is a scan nobody has shown to
  work (R1).
- [ ] `dispatch_reentry.go` joins the agent-naming scan's file list, so
  the one file allowed to build re-entry commands names no agent while
  it does (R1).
- [ ] The issue changes no production code, and the scan passes on the
  tree issue 2 left behind.

**Dependencies**: Blocked by <<ISSUE:2>>

**Complexity**: testable

### Issue 4: test(functional): acceptance scenario against the real binary

**Goal**: Land the functional scenario from the design's Decision 6:
the real binary, an isolated Codex home, an unreachable model endpoint,
and the resolved posture read back from the session rollout.

**Acceptance Criteria**:
- [ ] One rollout carries three turn contexts recording
  `workspace-write` / `read-only` / `workspace-write` at turn
  bootstrap: launch with the grant, resume without it as the negative
  control, resume with it (R1, R8).
- [ ] The scenario runs with no credential present and spends zero
  model turns; a machine without the Codex binary skips it rather than
  failing (R8).
- [ ] On the same run that records `workspace-write`, the isolated
  Codex home holds no trust stanza, asserted by counting `[projects.`
  stanzas rather than checksumming the configuration file (R4).
- [ ] On the interactive resume form, the grant plus an appended
  explicit read-only sandbox selection records `read-only` (R3).

**Dependencies**: Blocked by <<ISSUE:2>>

**Complexity**: testable

### Issue 5: docs: the posture story in the guide, the measurements in the spike

**Goal**: Give `docs/guides/codex-agent.md` the posture story the PRD
requires and append the design's new measurements to the standing
spike.

**Acceptance Criteria**:
- [ ] The guide states what posture a resumed session holds, why the
  grant is per process rather than a write to the developer's
  configuration, that a resume command typed from memory carries no
  grant, and that a developer who lands on the trust prompt from a
  grantless command should decline it and re-copy the command from
  `niwa list` (R9).
- [ ] The design's measurements -- the bootstrap-time rollout record,
  the three-context posture sequence, the resume forms' flag
  asymmetry, the probe's behavior under an empty home, and the
  override's merge and keying semantics -- land as new findings in
  `docs/spikes/SPIKE-codex-discovery-mechanics.md`, each carrying the
  binary version (design Decision 7).

**Dependencies**: Blocked by <<ISSUE:2>>

**Type**: docs

**Complexity**: simple

## Dependency Graph

## Implementation Sequence

The critical path is issue 1 -> issue 2 -> issue 3, and it gates
everything else. Issue 1 lands pure functions with no callers, so
nothing can precede it. Issue 2 cannot start before the functions it
routes through exist. Issue 3's position is load-bearing rather than
convenient: its scan passes only on a tree where issue 2 has removed
every field read outside the re-entry file, so landing it earlier would
fail on production code the plan intends to change, and landing it
after 2 makes it the loud check that no site was missed.

Issues 4 and 5 depend on the behavior issues 1 and 2 deliver, not on
their internals, so both can be written alongside 2 and 3. The honest
limit: issue 4's scenario resumes sessions through commands the rewired
surfaces produce, so it cannot pass until issue 2 has landed on the
branch -- write it early, run it green only after 2. Issue 5's prose
describes behavior the design already fixes, so drafting can start any
time; it merges with the branch that ships that behavior.

Since execution is single-pr, all five land on one branch in dependency
order and merge together; the exit criteria are the design's -- unit,
contract, and scan suites green, the functional scenario recording its
three postures where the binary is present, and `gofmt`, `go vet`, and
`go test -race ./...` clean.
