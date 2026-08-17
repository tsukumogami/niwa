# Verdict: FAIL

Reviewed: `docs/prds/PRD-dual-agent-workspace.md` (Draft) against
`docs/briefs/BRIEF-dual-agent-workspace.md` (Accepted) and
`wip/scope_dual-agent-workspace_handoff.md`.

The requirement set is complete against the BRIEF — every IN item traces to
requirements, every OUT item is preserved, all four journeys are exercised.
The failures are on the acceptance-criteria side: three requirements have
clauses no criterion would catch, and two of them correspond to silent-failure
modes the exploration explicitly flagged as acceptance-criterion material.

## Coverage against the BRIEF

IN items, all traced:

1. Both agents unconditionally, Claude tree untouched, Codex counterpart at
   all four layers — R1, R2, R3, R4. Covered.
2. Skills with same content — R8. Covered.
3. Sessions from anywhere (nested dirs, worktrees) — R4, R5. Covered.
4. Sessions that can act (write files, no setup) — R9, R10. Covered.
5. Clean repositories (git status, no overwrite) — R11, R12. Covered.
6. Compatibility with the per-workspace agent setting — R14. Covered.

OUT items, all eight present in the PRD's Out of Scope with none silently
re-included: Codex dispatch, ephemeral provisioning, Codex credentials/auth,
config-table renaming, workflow-skills/orchestration changes, Claude-side
changes, cross-repo context gap (niwa#247), hook injection. The two former
open questions (hooks, API key) are correctly landed as exclusions with their
rationale preserved in Decisions and Trade-offs. Covered.

## Journey coverage

- **Mara (mid-task switch, same directory, same context + skills):** R4, R8;
  reachable via criteria 1, 7. Covered.
- **Theo (fresh terminal deep in a repo, no env setup):** R4, R5; criterion 3
  (three levels deep, plain shell). Covered.
- **Iris (worktree, workspace context plus worktree framing):** R3, R4, R5;
  criterion 4. Covered.
- **Noah (declared agent, upgrade, apply, no migration):** R14; criteria 12
  and 13. Covered.

## Requirement-criterion correspondence

Forward direction (requirement → criterion):

- R1 → criteria 1, 13. R2 → 2. R4 → 1, 3, 4. R5 → 3, 4. R6 → 5. R7 → 6.
  R8 → 7. R9 → 8. R10 → 9. R11 → 10. R12 → 5. R14 → 12, 13. All sound.
- **R3, partial gap.** Criterion 14 tests re-apply on an *unchanged*
  instance (idempotence). R3 promises lifecycle commands "leave both agents'
  materializations current," which includes re-apply after the workspace
  changed — a repo added, context edited. No criterion would fail if re-apply
  after a change left the Codex materialization stale while a fresh instance
  passes everything. (Also minor: R3 names "applying a worktree" but only
  worktree creation is exercised, in criterion 4.)
- **R6, partial gap.** The requirement's "SHALL NOT ... suppress the
  repository's file" clause is only tested (criterion 5) in a scenario where
  the workspace presumably has repo-level content to deliver. See
  silent-failure item 3 below.
- **R13, partial gap.** Criterion 11 covers only the credential/login clause
  (sentinel files). The other two clauses — no modification of the Codex
  installation or its configuration defaults, no change to Codex behavior
  outside niwa-managed instances — have no criterion. This is not
  hypothetical: the exploration's rejected alternative (repointing the global
  `project_root_markers`) would degrade every repository on the machine
  outside niwa instances, and an implementation that did exactly that would
  pass every criterion currently listed, including 11.

Reverse direction: every criterion traces to a cited requirement; none tests
something no requirement asked for. Criterion 14 correctly maps to R3.

## Silent-failure coverage

Against the handoff's trap list ("Traps that belong in acceptance criteria"):

1. **Git-exclude pattern (`.codex` vs `.codex/`) leaving permanent dirt:**
   caught by criterion 10 (`git status` clean after apply and after a
   session). The trailing-slash form fails to match the repo-level symlink,
   which would surface as untracked. Covered.
2. **Byte-budget draining root-first, starving the repo layer, unmarked
   truncation:** caught by criterion 6 (end-of-repo-content marker visible
   with large outer layers). Covered.
3. **Empty or whitespace-only context file claiming the directory's slot and
   suppressing every remaining candidate:** NOT covered. The requirement
   exists (R6's "suppress" clause) but no criterion exercises the case that
   triggers the trap: a repository that ships its own agent-facing file while
   the workspace has nothing to say at that layer. An implementation that
   unconditionally writes a file — empty when there is no content — would
   pass criterion 5 (where workspace content exists) and silently swallow the
   repo's own context wherever it doesn't. The handoff names this trap
   explicitly.
4. **Missing project trust entry dropping the session to a read-only
   sandbox:** caught by criteria 8 (file creation on first attempt) and 9 (no
   trust prompt). Covered.
5. **Stale hook `trusted_hash` degrading silently:** moot — hook injection is
   excluded, and the PRD records that exclusion. Consistent; no criterion
   needed.

The three lead-named silent modes — session lost the repo's own context
(criterion 5), lost write ability (criterion 8), left a repo dirty
(criterion 10), innermost layer crowded out (criterion 6) — are all covered
in their basic form; item 3 above is the uncovered variant of the first.

## Required changes

1. **Add a criterion for the empty-layer suppression case (R6).** In a
   repository that ships its own committed agent-facing context file and for
   which the workspace defines no repository-level context, a Codex session
   still receives the repository's own content. This is the only way the
   "SHALL NOT suppress" clause can fail while every current criterion passes.
2. **Add a criterion for re-apply after a workspace change (R3).** Edit the
   workspace-level context (or add a repository to the workspace config),
   re-run `niwa apply`, and verify a Codex session sees the updated content
   (or the new repository is prepared to the same criteria). Criterion 14's
   idempotence check cannot catch stale materialization because nothing
   changed.
3. **Add a criterion for R13's outside-instance clause.** After `niwa create`
   and `niwa apply`, a Codex session in a repository outside any niwa
   instance behaves identically to before preparation (context discovery and
   trust state unchanged). Without this, the rejected global-marker design —
   the one the exploration ruled out precisely because it degrades unrelated
   work — passes the entire criteria list.

## Optional improvements

- Criterion 4 exercises worktree creation; R3 also names worktree apply.
  Extending criterion 4 (or 14) to re-apply a worktree would close the last
  R3 sliver.
- The Goals section says a session "can write files immediately"; R9 scopes
  this to "a prepared repository." A one-line note on whether writes from the
  instance root (outside any repo) are expected to work would prevent a
  future reader from testing the wrong thing. Not a gap in coverage of the
  BRIEF, which scopes the same way.

# Round 2

# Verdict: PASS

Re-adjudicated the revised PRD (19 criteria, R1-R14 unchanged). All three
required changes from round one landed, and the merges with testability's
overlapping items preserved the properties rather than diluting them.

## The three gaps, closed

1. **Empty-layer suppression (R6).** R6 gained the degenerate-case sentence
   ("when the workspace has nothing to say at a layer, the repository's own
   content still reaches the session undiminished"), and criterion 6 tests
   exactly the triggering scenario: a workspace configuring no
   repository-level content, a repository shipping its own committed file,
   and an explicit check that niwa writes no empty or whitespace-only file
   claiming the directory's single slot. The merge with testability's
   empty-slot criterion kept both halves — the delivered outcome and the
   mechanism that would break it. Closed.

2. **Re-apply after a workspace change (R3).** R3 now defines "current" to
   include refresh, and criterion 19 changes the configured content, re-runs
   `niwa apply`, and requires the new content present and the old content
   absent — absence being what distinguishes refresh from append. It also
   extends to `niwa worktree apply`, which incidentally closes the round-one
   optional item about R3's worktree-apply sliver. Criterion 18 separately
   pins idempotence (no accumulation over three applies), so the two
   properties are no longer conflated. Closed.

3. **R13's outside-instance clause.** R13 gained the scoped-additive
   boundary sentence, and criterion 14 operationalizes it: the developer's
   pre-existing config may differ only by per-project entries keyed inside
   niwa instances, with no pre-existing key removed, reordered, or altered,
   and no global key that changes Codex behavior outside instances — stating
   the outside-instance consequence explicitly. This fails the rejected
   global-marker design (a global `project_root_markers` key) that round one
   showed would pass the old criteria list. Criterion 13 keeps the
   credential/login half with a stronger check than before (byte-identical,
   mtime-unchanged, unreadable-file probe). Closed.

## Coverage re-check after the one-pass revision

Forward, every requirement still has at least one criterion that fails if
it is unmet: R1 (1, 17), R2 (2), R3 (4, 18, 19), R4 (1, 3, 4), R5 (3, 4),
R6 (5, 6), R7 (7), R8 (8), R9 (9, 10), R10 (11), R11 (12), R12 (5, 12),
R13 (10, 13, 14, 18), R14 (15, 16, 17). Reverse, every criterion cites a
requirement and tests something one asks for; new criterion 16 (launch-time
selection and the dispatch refusal survive) traces to R14's retained
launch-time meaning.

BRIEF IN items all remain traced (unconditional dual preparation, skills,
sessions from anywhere, sessions that act, clean repositories, setting
compatibility). All eight OUT items survive in the condensed Out of Scope
list with none re-included. All four journeys remain exercised and
reachable: Mara (1, 8), Theo (3), Iris (4), Noah (15-17). The revision
strengthened rather than weakened silent-failure coverage: the
byte-budget criterion (7) now names the 32768 default and adds an offline
declared-budget check, and the trust criterion (10) now rejects miskeyed
entries.

## Required changes

None.

## Optional improvements

None this round.
