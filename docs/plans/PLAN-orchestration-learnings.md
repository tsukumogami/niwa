---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-orchestration-learnings.md
milestone: "Coordinating a fleet of background workers"
issue_count: 6
---

# PLAN: Coordinating a fleet of background workers

## Status

Active

## Scope Summary

Ship the coordinator's operating knowledge into the paths that are actually loaded: two
embedded root skills, a canonical standing agreement merged into every workspace's
dispatch-briefs directory, and a contributor guide carrying the harness evidence. Includes
the two materializer changes that make multi-file skills and field-updatable content
possible.

## Decomposition Strategy

**Horizontal.** The design splits cleanly into machinery and content, with a stable
interface between them: the materializer writes files, the content is files. There's no
runtime interaction to integrate and no end-to-end path whose shape is in doubt, so a
walking skeleton would buy nothing — the "thin slice" would just be the materializer
change, which is issue 1 anyway.

The machinery lands first because the content depends on it. Whole-directory skills
(issue 1) unblock the two multi-file skills; the agreement writer (issue 2) is what makes
the shipped agreement exist at all. Both are independently reviewable and neither changes
shipped content, so a reviewer can check the mechanism without also weighing the prose.

## Execution Mode

**single-pr.** No named condition forces a split. Nothing here crosses repositories, no
step needs to reach main before another can run, and there's no merge gate between the
machinery and the content. The unit of usable value is the whole thing: a materializer
that can copy skill directories, with no skill that needs it, delivers nothing a reader
can observe.

## Issue Outlines

### 1. Copy whole root-skill directories, not just SKILL.md

**Complexity:** testable

**Goal**: `writeRootSkills` currently walks the embedded `rootskills` tree and installs
only `<name>/SKILL.md`, so a shipped skill cannot carry reference files. Make the skill
directory the unit: copy every regular file under `rootskills/<name>/` to
`<workspaceRoot>/.claude/skills/<name>/`, preserving relative paths.

**Acceptance Criteria**:

- A skill directory containing `SKILL.md` plus `references/foo.md` installs both, at
  `.claude/skills/<name>/SKILL.md` and `.claude/skills/<name>/references/foo.md`.
- Existing single-file behavior is unchanged: a directory with only `SKILL.md` installs
  exactly as before, and the existing `TestMaterializeWorkspaceRoot_DispatchSkill` passes
  untouched.
- Every installed path is included in the returned `written` slice.
- A skill directory with no `SKILL.md` is a build-time programming error and fails loudly
  rather than installing a directory the harness will not load as a skill.
- Overwrite semantics are unchanged — unconditional write, content-idempotent on re-run.
- Nested paths are joined against the destination root with no component taken from
  outside the embedded tree.

**Dependencies**: None

**Notes**: `internal/workspace/root_materializer.go`, around the existing walk at
`writeRootSkills`. The embedded FS is the only input, so there is no untrusted path
component; keep it that way rather than adding a cleaning step that implies otherwise.

### 2. Ship the standing agreement as a niwa-owned sentinel block

**Complexity:** critical

**Goal**: Embed a canonical `_common.md` and merge it into
`<workspaceRoot>/.niwa/dispatch-briefs/_common.md` on root materialization, so the
agreement every dispatched worker reads exists in every workspace and niwa can correct it
in the field. Workspace-authored content outside niwa's sentinel block survives untouched.

**Acceptance Criteria**:

- A new `go:embed` root carries `internal/workspace/dispatchbriefs/_common.md`.
- `writeDispatchBriefCommon(workspaceRoot)` runs from `MaterializeWorkspaceRoot` alongside
  `writeRootSkills`, creating `.niwa/dispatch-briefs/` when absent.
- No existing file: the workspace gets a file containing niwa's block, bounded by the
  start sentinel and the end marker.
- Existing file with no niwa block: niwa's block is appended and every pre-existing byte
  is preserved.
- Existing file with content *before and after* niwa's block: re-running replaces only the
  block; both surrounding regions are byte-identical afterwards.
- Re-running twice in a row is a no-op — exactly one block, no duplication, no drift.
- A truncated or malformed sentinel (start marker present, end marker missing) is treated
  as "no niwa block", producing an append rather than a guess at the bounds. The
  pre-existing content is not destroyed.
- The write is confined to the one constant path under the resolved workspace root.

**Mutation checks to record in the PR**: Each of these must break a named test:
break the end-marker search so the strip runs to end-of-file (the "content after the
block" test must fail); make the append unconditional so a re-run duplicates (the
idempotence test must fail); skip the create-directory step (the no-existing-file test
must fail).

**Dependencies**: None

**Notes**: Model on `installWorktreeContextLayer` and `stripWorktreeContextSection` in
`internal/workspace/worktree_content.go`. The one deliberate difference: that precedent
truncates at its heading and therefore owns the file's tail, while this block needs an
explicit end marker so a workspace can keep sections after it. This is the data-loss
surface in the change — the tests above are the guard, not a formality.

### 3. Refresh the workspace root on `niwa create`

**Complexity:** testable

**Goal**: `MaterializeWorkspaceRoot` runs only on root-scope `niwa apply` and on
`niwa init` in named and clone modes, so a workspace whose owner only runs instance-scoped
applies never receives shipped content or a correction to it. `niwa dispatch` goes through
`Create`, which is precisely the moment the agreement is about to be read.

**Acceptance Criteria**:

- `Create` calls `MaterializeWorkspaceRoot` at workspace-root scope.
- A `@critical` Gherkin scenario in `test/functional/features/` drives `niwa create`
  against the offline `localGitServer` fake and asserts the dispatch skill and the
  agreement are present at the workspace root afterwards.
- Creating an instance twice does not duplicate or corrupt the agreement's block.
- Failure to materialize is non-fatal and warns, matching how `init.go` already treats it —
  a create must not fail because a skill file could not be written.

**Dependencies**: <<ISSUE:1>>, <<ISSUE:2>>

The call site is only meaningful once there is multi-file skill content and an agreement
to write.

**Notes**: Follow the existing `@critical` scenarios covering apply and create for the
`localGitServer` pattern.

### 4. Extend /dispatch with launch mode and framing level

**Complexity:** simple

**Goal**: The shipped `/dispatch` skill covers writing a brief and does not cover the two
decisions that determine whether the worker finishes unattended. Add them at the point
where they are actually made.

**Acceptance Criteria**:

- A launch-mode section: hand off autonomously when what "done" looks like is unambiguous;
  settle the framing interactively first when the framing itself is the uncertainty. States
  the failure mode of getting it wrong — an autonomous agent confidently resolving a
  question the author would have answered differently, surfacing only at PR time.
- A framing-level section with the three levels from the design, each with the property of
  the work that selects it and the signal that the property holds.
- The two contradicted intuitions are stated as contradicted: a full investigation is not
  what catches a wrong direction (it is what produces a durable design document), and diff
  size does not predict the level.
- The evidence base and its limits are stated once, plainly, including the date
  confounding.
- A short note mapping the levels onto a workflow plugin's skills, framed as a mapping
  rather than as doctrine. No plugin's skill names appear as required steps.
- A section on naming sibling in-flight work and its file surfaces in each brief, which is
  what let ten parallel workers avoid collisions.
- The existing content — the worker starts blind, the work-in-flight block, `--detach`,
  one brief per worker — is preserved.
- Cross-reference to `/fleet` with the boundary stated.

**Dependencies**: <<ISSUE:1>>

### 5. Add the /fleet root skill

**Complexity:** simple

**Goal**: A new embedded root skill owning everything after launch. Separate from
`/dispatch` because its trigger moment is different: it fires when someone asks what their
workers are doing, not when they hand work off.

**Acceptance Criteria**:

- `internal/workspace/rootskills/fleet/SKILL.md` with frontmatter whose description
  triggers on asking what workers are doing, what is next, and reviewing what came back —
  and which explicitly distinguishes itself from per-session in-flight reporting, which
  covers the current session's own PRs and never issues a broader listing.
- The work-in-flight table, with the column that forge queries cannot supply: the
  filesystem work state of each worker's tree.
- The stranded-work sweep: check every worker's tree for uncommitted changes and unpushed
  commits, with the reason it exists — a PR can be green and complete on the forge while
  the change sits uncommitted beside it.
- The wake-or-fix-or-file decision, with the cost data and the criterion: wake a session
  when it holds context you would otherwise rebuild, or when the change needs its judgment
  about its own in-progress edits — not when the fix is small.
- The reporting rule: state the check and when it ran, or say you did not check. With the
  reason it is phrased that way — the failures it prevents all *felt* verified, because a
  check did run and answered a different question.
- Monitor seeding: seed from an explicit list of what you are waiting for, never from a
  snapshot of what already exists.
- A note on what is worth delegating: a lookup you can run directly is not, a sweep across
  a surface you would not otherwise visit is.
- `references/review-standard.md` — do not trust the PR body, what to verify by running,
  and what counts as feedback worth writing. Includes the finding that at this quality
  level most defects are false claims in prose rather than defects in code.
- `references/session-control.md` — the operational recipe for reaching a session, the
  three traps, and a pointer to the guide for the evidence.

**Dependencies**: <<ISSUE:1>>

This skill ships reference files, so it needs whole-directory installation.

### 6. Publish the background-session-control guide

**Complexity:** simple

**Goal**: `docs/guides/background-session-control.md`: the re-verifiable evidence behind
the operational recipe, for a reader who needs to check rather than trust.

**Acceptance Criteria**:

- Every mechanical claim carries the command that produces it and the observed output.
- The CLI version each claim was observed against is stated once, prominently.
- The corrected resume recipe, deriving the working directory from the transcript rather
  than from an encoded path or from the reported session cwd, with the evidence that both
  of those fail.
- The three traps with their observed signatures: the refusal error and its timing; the
  timeout exit code, terminal reason, and the fact that partial side effects still land;
  the stale registration's state value and absent pid.
- The correction that pid absence is not a safe-to-resume signal, stated as superseding
  the earlier rule, with the state values that are reliable.
- The measured costs, including the failure modes that cost more than the successes.
- An entry in the contributor-guides list in `CLAUDE.md`.

**Dependencies**: None

## Implementation Sequence

**Critical path:** issue 1, then issue 5. The `/fleet` skill is the largest single piece of
content and it cannot install correctly until whole-directory skills work.

**Parallel at the start:** issues 1, 2 and 6 have no dependencies on each other. Issue 6 is
pure documentation and touches no shared file, so it can land at any point.

**Order within the single PR:** machinery first (1, 2), then the call site (3), then
content (4, 5), with the guide (6) wherever it fits. Committing in that order keeps each
commit independently reviewable and means the content commits can be verified against a
materializer that already works.

**Verification before the PR opens.** `go test ./...`, `go vet ./...` and `gofmt` clean.
The functional suite for the new `@critical` scenario. And a manual check that the
materializer actually produces the expected tree, since the value of every content issue
depends on it landing on disk where the reader expects.
