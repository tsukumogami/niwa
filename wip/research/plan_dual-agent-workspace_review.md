# Verdict: FAIL

Two required changes, both localized and mechanical. Nothing in the plan encodes a
superseded design position — I checked every reversed decision named in the review
brief against the design's final text and all four are correct.

## A. Scope gate

Clean. Every DESIGN mechanism has exactly one issue behind it: payload and budget
(5), per-repository link and bare exclude patterns (6), composer with the
never-empty and regular-file-only rules (3), its pipeline consumers (4), the
conflict rule with the cleanup exemption (7), the trust writer with its write
discipline (8), conflict-driven retraction (9), worktree parity (10), the
per-agent pass and accessor retirement (1, 2), acceptance coverage (11), guide (12).

Nothing the design ruled out is reintroduced. Hooks and API-key binding appear
only as negative assertions in issue 5's criteria ("niwa writes no hook
definitions and no hook-state entries anywhere"; "No `OPENAI_API_KEY`,
`forced_login_method`, or any auth-related key appears in any generated file"),
which is the right shape — pinning the exclusion, not building toward it.
Background dispatch appears only as issue 1's unchanged-refusal criterion.
Ephemeral provisioning appears only in issue 2's re-wording of the now-inert agent
assignments on `instance_from_hook.go` and `session_lifecycle_cmd.go`, which
Decision 7A names explicitly. Destroy-time trust cleanup is correctly referenced
as *planned* work motivating the state record in issue 8, not implemented.

No design decision is left without a work item. The one arguable omission is the
"cheap materialization-time smoke test" floated in Consequences as drift
mitigation; it reads as a suggestion rather than a decision, and issue 11's
live-gated scenarios cover the same ground.

## B. Design fidelity

All four flagged decisions are at their final position.

**Conflict rule.** Issue 7 states it exactly: "the apply hands its per-run
conflicted-path set to the cleanup, which skips those paths before removing
recorded paths the apply did not produce — this is a named change to build, not
existing behavior; today the cleanup tests only membership in the produced set, so
absence is the deletion trigger. Conflicted entries leave the record." That is
drop-plus-exemption, not forward-carry, and it does not assume existing behavior.
Verified against source: `cleanRemovedFiles` (`internal/workspace/apply.go:1844`)
builds `currentFiles` from `result.managedFiles` and deletes on non-membership,
with no conflict input — so the plan's premise is factually right. Forward-carry
appears nowhere in the plan; the idiom it would have copied
(`apply.go:1641`, the worktree-refresh forward-carry) is not referenced.

**Trust retraction.** Issue 9 bounds removal by the instance-state record, names
shape-based removal as the thing not to do, and states the guarantee as
never-reinstated: "After removal, an identically-shaped entry at the same key that
is not in niwa's record (the developer's own answer) survives all later applies"
plus "While the conflict stands, repeated applies never reinstate the entry."
Record cleared with the removal, recorded-key-absent is a no-op, unrecorded key
left alone — all three present.

**`O_NOFOLLOW`.** Issue 3 puts the refusal on the open ("opened with `O_NOFOLLOW`
so a symlink fails at the open") and keeps the narrow scope: "the composer inlines
nothing, still composes the workspace layers, and returns the refusal for loud
reporting." The criteria test both the symlink and the any-other-non-regular-file
path. This is the final position, not the wider "refuse the override" reading.

**Accessors and the edit set.** Issue 1 inverts the three assertions Decision 7A
enumerates and explicitly protects the fourth: "The assertion `tools/app/AGENTS.md
does not exist` is untouched and still passes." Verified against
`test/functional/features/codex-agent.feature:35-38,85-86` — the four assertion
lines are exactly where the design says they are. The retirement targets in issue 2
match `internal/agent/agent.go:73,85`. One defect in this area, below.

## C. Acceptance-criterion discriminability

Strong overall, and several criteria are written as genuine traps rather than
restatements. The best of them: issue 6's "a test asserting the literal pattern
text (no trailing slash) fails if either gains one" (the feature's highest-risk
detail, made mechanically decidable); issue 7's requirement that a
record-drop-without-cleanup-consultation implementation must *fail* by deleting the
committed file; issue 8's symlinked-parent fixture with "a present-but-miskeyed
entry fails the test"; issue 9's developer's-own-answer survival check. None of
these is satisfiable by a stub.

All 19 PRD criteria have an issue behind them. Mapping, by ordinal: 1 → issues 1,
11; 2 → 1, 2; 3 → 4, 11; 4 → 10, 11; 5 → 4, 11; 6 → 3, 4, 11; 7 → 5, 11; 8 → 5,
11; 9 → 11 (live, grounded offline by 8's trust writes); 10 → 8, 10, 11; 11 → 5
(offline), 11 (live); 12 → 6, 10, 11; 13 → 8, 11; 14 → 8, 11; 15 → 1, 11; 16 → 1,
11; 17 → 1, 11; 18 → 5, 6, 8, 11; 19 → 4, 10, 11. No orphans.

One weak criterion, not fatal. Issue 1 pins R2 as content-identity "across applies
with `default_agent` set to claude, codex, and unset", where PRD criterion 2 asks
for identity "between a pre-change and post-change apply". A regression that
changes the Claude tree uniformly for all three settings passes the plan's check
and fails the PRD's. The real pre/post guard is the untouched remainder of the test
suite, which the edit-set bound preserves — but the plan never says so, and it
should.

## D. Sequencing integrity

The dependency graph (1←2; 1,3←4; 4←5; 5←6; 4,5,6←7; 1←8; 7,8←9; 4,6,7←10;
7,9,10←11; 10←12) is acyclic, and nothing is scheduled before something it needs.
Issue 5's dependency on issue 4 is non-obvious but correct: the budget must be
sized against composed files that exist. The stated critical path (1 → 4 → 5 → 6 →
7 → {9,10} → 11) matches the declarations at depth 7; issue 12 sits at the same
depth via 10 and is correctly described as landing last.

The re-derived batch rationale is carried verbatim, not the superseded one: "batch
1 gates batch 2 not because batch 2 reuses batch 1's writers (it is net-new code
beside them) but because batch 1 establishes the per-agent pass in the apply
pipeline that batch 2's writers ride, and lands the test-suite inversions batch 2's
assertions build on." That is the design's final text at lines 1025-1030. Issue 8
correctly depends on issue 1 alone (batch 3 parallel after batch 1), with issue 9
joining it to the conflict verdicts.

The Decomposition Strategy's cross-batch edge tally is wrong, though — see required
changes.

## Atomicity and mechanics

Each outline is one coherent, separately reviewable change. Issue 5 (payload plus
two-marketplace-kind plugin-root resolution) and issue 11 (all 19 criteria) are the
largest, but both are coherent units and splitting them would spread one mechanism
across reviews.

Every file path named in the outlines exists: `apply.go`, `content.go`,
`root_materializer.go`, `worktree_content.go`, `state.go`, `plugin.go`,
`agent/agent.go`, the four `internal/cli/` files, `internal/gitexclude`
(`EnsureRepoExclude` at `exclude.go:54`), the three test files, the feature file,
and `docs/guides/functional-testing.md`.

No `wip/` path appears in the document (grep clean). No private repo names, no
word-boundary "vision", no upstream-planning vocabulary; the only issue references
are niwa#228 and niwa#247 by way of the upstream docs, and neither appears in the
plan itself.

## Required changes

**1. Issue 2 — the `internal/agent/agent_test.go` edit is wrong as written, and
breaks the bound its own criterion invokes.**

The goal says "delete the repo-level-skip test in `internal/workspace/content_test.go`
and the accessor tests in `internal/agent/agent_test.go`", and the criterion says
"The repo-level-skip test and the retired accessors' tests are deleted; no other
test is removed (PRD criterion 2's edit-set bound)".

Two problems. First, `LocalContextFileName()` is not retired — only its Codex
branch is (Decision 7A). `TestLocalContextFileName` (`agent_test.go:67`) carries
three rows: `{AgentClaude, "CLAUDE.local.md"}`, `{AgentCodex, "AGENTS.md"}`,
`{Agent(""), "CLAUDE.local.md"}`. Deleting the test drops live coverage of a
surviving function; only the Codex row goes. `TestWritesRepoLevelContext`
(`agent_test.go:83`) is the one that is deleted outright.

Second, both `agent_test.go` edits fall outside PRD criterion 2's enumerated set
("The only tests modified are the exclusivity assertions in
`test/functional/features/codex-agent.feature` and the unit tests in
`content_test.go` and `root_materializer_test.go` that assert the other agent's
file is absent; no other test is modified or deleted") *and* outside the design's
"complete set of test edits". They are unavoidable — deleting a function makes its
test uncompilable — so the plan must state them as the entailed extension rather
than claim conformance to a bound it exceeds. As written the criterion cannot be
satisfied.

Should say, in place of the current criterion: "`TestWritesRepoLevelContext` in
`internal/agent/agent_test.go` is deleted with the accessor it exercises, and
`TestLocalContextFileName`'s Codex row is removed while its Claude and zero-value
rows stand; together with the repo-level-skip test in
`internal/workspace/content_test.go`, these are the only test deletions beyond PRD
criterion 2's enumerated set, entailed by the accessor retirement — no other test
is modified or removed."

**2. Decomposition Strategy — the cross-batch edge tally miscounts and
misattributes.**

The text reads: "Cross-batch dependency edges: 4 (batch 1 into batch 2: issues 4, 8
depend on issue 1; issue 2 consumes issue 1), 2 (batch 2 into batch 3: issue 9
depends on issues 7 and 8)".

Three edges are enumerated in the first group, not four; issue 2 is itself a batch-1
issue, so 2→1 is intra-batch; and issue 8 is a batch-3 issue (the trust writer), so
8→1 is a batch-1-into-batch-3 edge, not batch-1-into-batch-2. In the second group,
9→8 is intra-batch-3, so only 9→7 crosses — one edge, not two.

Should say: "Cross-batch dependency edges: 1 out of batch 1 into batch 2 (issue 4
depends on issue 1), 1 into batch 3 (issue 8 depends on issue 1), 1 from batch 2
into batch 3 (issue 9 depends on issue 7), and 3 from batch 2 into batch 4 (issue
10 depends on issues 4, 6, 7); issue 2's dependency on issue 1 is intra-batch, as
is issue 9's on issue 8."

## Optional improvements

- **Issue 1, R2 pin.** Add a sentence naming the untouched remainder of the test
  suite as the pre/post half of PRD criterion 2. The cross-`default_agent` identity
  check alone does not catch a regression that changes the Claude tree uniformly.

- **Issue 7, third criterion.** "a test that removes only the record change
  (without the cleanup consultation) must fail by deleting the committed file" is
  hard to parse on first read. Intent is clear from the goal paragraph, but "an
  implementation that drops the record entry without teaching the cleanup about
  conflicts must fail this test by deleting the committed file" says it plainly.

- **Issue 5, budget criterion.** "at least the byte size of the full composed chain
  on disk" matches PRD criterion 7 exactly, so it is not a defect — but the design
  asks for "generous headroom rather than a tested-once number", and a
  chain-size-exactly implementation passes. Consider asserting a margin.

- **Issue 11, criterion 3 coverage.** The design's and PRD's criterion 3 specifies
  "a shell with no `NIWA_*` variables and no `CODEX_HOME` set"; the outline
  describes the fixture shape but not the clean-environment condition. Worth
  naming, since R5's whole point is that no environment preparation happens.

- **Generation marker at the instance root.** Issue 4's composed group files carry
  the marker (via the issue 3 composer); issue 1's instance-root `AGENTS.md` comes
  from the re-parameterized existing writer and so will not. The design's "every
  composed file niwa writes begins with a generation marker" is written for the
  conflict rule, which never applies outside working trees, so nothing breaks —
  but the inconsistency is worth a deliberate note in issue 1 rather than being
  discovered during implementation.

# Round 2

# Verdict: PASS

Both required findings are closed, all five optionals are in, and nothing regressed.

**Required change 1 — closed, and closed better than I asked.** Amending PRD criterion
2 rather than having the plan admit an overrun is the right resolution: the amendment
is narrow (it licenses only deletions entailed by retiring an accessor, in the exact
three shapes at issue) rather than opening the bound generally, so R2 stays decidable
and the plan claims conformance to a bound it actually meets. Issue 2's goal now says
"`LocalContextFileName()` itself survives with its Claude and zero-value behavior",
which forecloses the misreading, and its criterion names the three edits precisely:
`TestWritesRepoLevelContext` deleted with its accessor, `TestLocalContextFileName`'s
Codex row removed with the other rows standing, the repository-level-skip test deleted.
That matches the amended PRD text word for word and matches the code — `agent.go:73`
(branch retired, function stays) and `agent.go:85` (function retired).

**Required change 2 — closed, and the added sentence is accurate.** The tally now reads
1 + 1 + 1 + 3 and each edge checks out against the declarations. The addition ("The
closing acceptance-test issue (11) draws on batches 2 through 4 (issues 7, 9, 10) and
is left out of the tally") is correct on both halves: issue 7 is batch 2, issue 9 batch
3, issue 10 batch 4, and declaring the issue out of the tally is what keeps the four
counted numbers exact — 11→7 would otherwise raise the batch-2-into-batch-4 count to 4.
Issue 12's dependency on issue 10 is intra-batch-4 and correctly absent. No miscount
reintroduced.

**Removed Dependency Graph section — no impact.** My round-1 sequencing assessment was
built from the twelve per-issue **Dependencies**: declarations and the Implementation
Sequence prose, never from the diagram; I re-read all twelve declarations and they are
byte-identical to what I verified then. The graph is unchanged, still acyclic, and the
stated critical path (1 → 4 → 5 → 6 → 7 → {9,10} → 11) still matches at depth 7.

**Optionals verified in place.** Issue 1 carries both the untouched-remainder sentence
on the R2 criterion and the deliberate no-marker note in its goal. Issue 5's budget
criterion now reads "exceeds ... by a stated headroom margin — an exact-fit budget fails
the test". Issue 7's third criterion is the plainer phrasing. Issue 11's criterion-3
scenario names the no-`NIWA_*`/no-`CODEX_HOME` shell.

**Mechanics re-checked.** No `wip/` reference (grep clean), 12 issue outlines, section
structure intact. Nothing that passed in round 1 was disturbed.
