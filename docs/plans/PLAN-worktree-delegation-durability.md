---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/current/DESIGN-niwa-default-worktree.md
milestone: "Worktree delegation durability"
issue_count: 4
---

# PLAN: worktree delegation durability

## Status

Active

## Scope Summary

Make the shipped worktree-delegation integration survive a niwa upgrade and fail
cleanly, by implementing Decisions 7, 8, and 9 of
`docs/designs/current/DESIGN-niwa-default-worktree.md`. Resolves
`tsukumogami/niwa#221`.

## Decomposition Strategy

**Horizontal.** The three code changes touch disjoint seams — the emitted hook
command string, the hook create path's error handling, and the worktree apply
path's options plumbing — with stable boundaries between them and no runtime
interaction to shake out. There is no integration risk that a thin end-to-end
slice would surface earlier than the individual changes do, so a walking
skeleton would add sequencing for nothing.

The three implementation outlines are mutually independent and can land in any
order. The coverage outline depends on all three because it asserts their
combined end state.

**Execution mode: single-pr.** One PR delivers the fix. None of the escape
conditions apply: there is no cross-repo landing order, no merge gate between
steps, and no intermediate state that is independently useful to a reader. A PR
carrying only the hook-command change would leave a failed create still stranding
an `active` record; the observable value — delegation that keeps working across
upgrades and cleans up after itself — arrives when all three land together.

## Issue Outlines

### 1. Resolve the hook command from PATH with an absolute-path fallback

**Goal**: Implement Decision 7. Replace the single-token absolute-path hook
command with the guarded PATH-first form, through one helper shared by both hook
consumers.

**Approach**: Add a shared helper in `internal/workspace` that composes
`command -v niwa >/dev/null 2>&1 && exec niwa <suffix>; exec '<abs>' <suffix>`
from a binary path and a subcommand suffix. Point `worktreeFromHookCommand`
(`internal/workspace/materialize.go`) and `instanceFromHookCommand`
(`internal/workspace/root_materializer.go`) at it, keeping their existing
suffix constants as the argument. Single-quote the fallback path and escape
embedded single quotes (`'` → `'\''`).

Revisit the `os.Executable()` error branch in `internal/workspace/apply.go`,
which currently downgrades to the deny fallback on the grounds that no valid
hook command can be written. With a PATH-first command that premise no longer
holds; either emit the PATH-only form and keep the deferred warning, or keep the
deny behavior deliberately — and update the comment to say which and why.

**Acceptance Criteria**:
- Both emitted hook commands take the guarded form, verified by unit tests
  asserting the exact string.
- A fallback path containing a space survives shell word-splitting, covered by a
  test with such a path.
- Re-running apply produces a byte-identical command (idempotency, R11).
- The `os.Executable()` failure branch's behavior is asserted and its comment
  matches what the code does.
- Existing assertions that the command ends with or contains the subcommand
  suffix continue to pass.

**Dependencies**: None

**Complexity**: testable

### 2. Reconcile a failed delegated create through the guarded teardown

**Goal**: Implement Decision 8. A `niwa worktree from-hook` create that fails
after `git worktree add` must not leave an `active` session record behind.

**Approach**: In the hook create path (`internal/cli/session_from_hook_cmd.go`),
replace the bare error return after content install with a helper that runs the
existing non-force `DestroySession` and composes an error naming both the
original failure and the reconciliation outcome. Leave `DestroySession` itself
unchanged — its guard ordering and branch semantics are already correct — and
leave `ApplyToWorktree` fail-fast, since rollback is the caller's job.

Do not change the interactive `niwa worktree create` path. Its retain-and-tell
behavior is right for a human at a terminal; add a comment recording that the
divergence between the two callers is deliberate.

**Acceptance Criteria**:
- A content-install failure on the hook path leaves no git worktree, no session
  branch, and a terminal (not `active`) session record.
- The returned error names both the original cause and what niwa did about it.
- When the dirty guard refuses, the worktree is retained and the retention is
  logged, matching Decision 3's teardown behavior.
- The interactive create path's behavior is unchanged, asserted by an existing or
  added test.
- `niwa worktree list` shows no `active` row after a failed delegated create.

**Dependencies**: None

**Complexity**: testable

### 3. Thread the delegation decision into the worktree apply path

**Goal**: Implement Decision 9. A niwa worktree's `settings.local.json` should
carry the same worktree-delegation configuration as the clone it was made from.

**Approach**: Add a `WorktreeDelegation` field to `WorktreeApplyOptions` and pass
it through to `repoMaterializeInputs` in `ApplyToWorktree`
(`internal/workspace/worktree_content.go`). Populate it at both call sites — the
apply pipeline's worktree env refresh (`internal/workspace/apply.go`) and the
session lifecycle command path (`internal/cli/session_lifecycle_cmd.go`) —
reusing the same probe-plus-path resolution the instance apply path uses. A nil
value must keep today's behavior so no caller is forced to supply one.

**Acceptance Criteria**:
- A worktree created through either path has hook entries or deny entries in its
  `settings.local.json` matching its clone's.
- Passing a nil delegation writes neither, preserving current behavior.
- The worktree path writes no unresolved secret references, preserving the
  existing standalone-path safety property.
- Re-applying to an existing worktree is idempotent.

**Dependencies**: None

**Complexity**: testable

### 4. Cover the durability contract and update the worktree guide

**Goal**: Lock the three changes in with end-to-end coverage and bring the
contributor-facing documentation in line.

**Approach**: Add a `@critical` Gherkin scenario in
`test/functional/features/worktree-delegation.feature` asserting the emitted hook
command's guarded shape for both consumers, and a scenario covering a failed
delegated create leaving no `active` session. Use the existing `localGitServer`
helper so the scenarios stay offline. Extend `docs/guides/worktree.md`'s teardown
section to say the same reconciliation runs when a delegated create fails, and
note that an installed hook resolves niwa from PATH.

**Acceptance Criteria**:
- `make test-functional-critical` passes with the new scenarios.
- The scenarios fail against the pre-fix behavior (verified by running them
  before the fix, or by asserting the specific new strings).
- `docs/guides/worktree.md` documents both the create-failure reconciliation and
  the PATH-first hook resolution.
- No scratch-directory paths are referenced from any committed artifact.

**Dependencies**: <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>

**Complexity**: testable

## Implementation Sequence

**Critical path:** any one of outlines 1-3, then outline 4. Depth is two.

**Parallelization:** outlines 1, 2, and 3 are mutually independent and touch
disjoint files. Outline 1 changes the emitted command string in
`internal/workspace/materialize.go` and `internal/workspace/root_materializer.go`;
outline 2 changes error handling in `internal/cli/session_from_hook_cmd.go`;
outline 3 changes options plumbing in `internal/workspace/worktree_content.go`.
They can be implemented in any order or concurrently.

**Suggested order.** Take outline 1 first — it is the defect that currently
breaks worktree creation workspace-wide, and landing it makes the other two
verifiable by hand against a working delegation path. Outline 4 closes.
