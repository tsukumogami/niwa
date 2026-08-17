---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-dual-agent-workspace.md
milestone: "Dual-agent workspace"
issue_count: 12
---

# PLAN: dual-agent workspace

## Status

Active

## Scope Summary

Implement docs/designs/DESIGN-dual-agent-workspace.md: every instance
`niwa create` and `niwa apply` produce serves both Claude Code and Codex, with
the Claude tree byte-for-byte unchanged. The Codex side is a payload directory
at the instance root (`.codex/` with a budget-declaring `config.toml` and
whole-plugin skills symlinks), a per-repository `.codex` symlink and composed
`AGENTS.override.md` that inlines any committed `AGENTS.md`, composed
`AGENTS.md` files at the instance and group levels, bare git-exclude patterns,
one path-scoped trust entry per cloned repository in the developer's Codex
config, and the same per-tree writes for worktrees. Committed content at
either name niwa writes is a detected conflict, never overwritten and never
trusted on niwa's signature. The agent discriminator survives as a launch-time
selector only.

The upstream PRD (docs/prds/PRD-dual-agent-workspace.md) carries requirements
R1–R14 and 19 acceptance criteria; this plan cites criteria by their ordinal
position in that list. Between them the issues below cover all 19.

## Decomposition Strategy

**Hybrid, following the DESIGN's four batches.** The grouping rule is one
issue per mechanism the DESIGN names: the per-agent pass, the accessor
retirement, the composer, its pipeline consumers, the payload writer, the
link-and-exclude delivery, the conflict rule, the trust writer, the
conflict-driven trust retraction, worktree parity, acceptance tests, and the
guide. The DESIGN's sequencing rationale is preserved as stated there: batch 1
gates batch 2 not because batch 2 reuses batch 1's writers (it is net-new code
beside them) but because batch 1 establishes the per-agent pass in the apply
pipeline that batch 2's writers ride, and lands the test-suite inversions
batch 2's assertions build on. Batch 2 gates batch 4 (worktrees reuse the
per-repository composer and writers); batch 3 can proceed in parallel after
batch 1, with its conflict-driven withholding wired up once batch 2 is in.

Cross-batch dependency edges: 1 out of batch 1 into batch 2 (issue 4 depends
on issue 1), 1 into batch 3 (issue 8 depends on issue 1), 1 from batch 2 into
batch 3 (issue 9 depends on issue 7), and 3 from batch 2 into batch 4 (issue
10 depends on issues 4, 6, 7); issue 2's dependency on issue 1 is intra-batch,
as is issue 9's on issue 8. The closing acceptance-test issue (11) draws on
batches 2 through 4 (issues 7, 9, 10) and is left out of the tally.

The work lands as one pull request, so the authoritative ordering is each
outline's own **Dependencies**: declaration, with the critical path and the
batch grouping restated in Implementation Sequence at the end.

## Issue Outlines

### Issue 1: feat(workspace): materialize root- and instance-level context for both agents

**Complexity**: complex

**Goal**: Rework the apply pipeline (`internal/workspace/apply.go`) from one
resolved-agent materialization to a per-agent pass: the Claude pass runs
unconditionally as Claude at every level — workspace root, instance, group,
repository — regardless of `default_agent`, and the workspace-root and
instance-level writers (`internal/workspace/root_materializer.go`,
`InstallWorkspaceContent` in `internal/workspace/content.go`) additionally run
for Codex, producing `AGENTS.md` beside `CLAUDE.md`. This is the one place
where re-parameterization is the whole job (DESIGN Decision 7A); the group
Codex file and everything repository-level is net-new composition and lands in
later issues, not here. One deliberate consequence: the root- and
instance-level `AGENTS.md` files this issue produces come from the
re-parameterized existing writers and carry no generation marker — the marker serves the
conflict rule (issue 7), which applies only inside repository working trees,
so its absence at the niwa-owned levels is intended, not an oversight. Invert
the exclusivity assertions exactly as Decision 7A enumerates them, update the
feature file's description prose and `Design:` pointer, and pin the R2
byte-identity check.

**Acceptance Criteria**:

- With `default_agent = "codex"`, `niwa create` produces both `CLAUDE.md` and
  `AGENTS.md` at the instance root, and `tools/app/CLAUDE.local.md` in the
  fixture repository: the three assertions Decision 7A enumerates in
  `test/functional/features/codex-agent.feature` are inverted (PRD criterion 2's
  enumerated edit set)
- With a Claude or absent `default_agent`, `AGENTS.md` exists at the instance
  root and carries the instance-level content (PRD criteria 1, 18 setup)
- The assertion `tools/app/AGENTS.md does not exist` is untouched and still
  passes — niwa never writes `AGENTS.md` into a repository
- The feature file's description prose and `Design:` pointer no longer narrate
  the exclusive model
- The "other agent's file is absent" unit assertions in
  `internal/workspace/root_materializer_test.go` and
  `internal/workspace/content_test.go` are inverted; no other test is modified
  (PRD criterion 2)
- A test pins R2: the set of paths materialized for Claude (`CLAUDE.md` tree,
  `.claude/`, settings, skills tree) is content-identical across applies with
  `default_agent` set to claude, codex, and unset. The pre-change/post-change
  half of PRD criterion 2 is carried by the untouched remainder of the
  existing test suite, which the edit-set bound above preserves — this
  cross-setting check alone would not catch a regression that changed the
  Claude tree uniformly (PRD criterion 2)
- `niwa apply` on a workspace declaring `default_agent` exits zero with no
  migration step, and on a config predating the setting entirely (PRD criteria
  15, 17; R14)
- The dispatch-refusal scenario passes unchanged: a codex-default workspace
  still selects Codex at launch and `niwa dispatch` still refuses (PRD
  criterion 16)
- `go test ./...` and `make test-functional-critical` pass

**Dependencies**: None

---

### Issue 2: refactor(agent): retire the exclusive-materialization accessors and re-word selector surfaces

**Complexity**: simple

**Goal**: Remove the two accessors Decision 7A retires:
`WritesRepoLevelContext()` in `internal/agent/agent.go` (its gate is vacuous
once the Claude pass always runs as Claude and the Codex pass never calls the
repo-level installers) and `LocalContextFileName()`'s Codex branch (never
called, and wrong). `LocalContextFileName()` itself survives with its Claude
and zero-value behavior. Drop the gates at their call sites in
`internal/workspace/content.go` and `internal/workspace/worktree_content.go`.
Make the test deletions PRD criterion 2 names as entailed by the retirement:
the repo-level-skip test in `internal/workspace/content_test.go`,
`TestWritesRepoLevelContext` in `internal/agent/agent_test.go`, and the Codex
row of the table-driven `TestLocalContextFileName`. Re-word the dispatch
refusal's comments (`internal/cli/dispatch.go`), the now-inert agent
assignments on the launch-coupled provisioning paths
(`internal/cli/instance_from_hook.go`, `internal/cli/session_lifecycle_cmd.go`),
and the `--agent` help text on `niwa create` and `niwa apply`
(`internal/cli/create.go`, `internal/cli/apply.go`) so none of them promise a
materialization selection R1 removed.

**Acceptance Criteria**:

- `WritesRepoLevelContext` no longer exists anywhere in the tree; the
  repo-level installers run unconditionally on the Claude pass
- `LocalContextFileName()` has no Codex branch
- `TestWritesRepoLevelContext` in `internal/agent/agent_test.go` is deleted
  with the accessor it exercises; `TestLocalContextFileName`'s Codex row is
  removed while its Claude and zero-value rows stand; and the
  repo-level-skip test in `internal/workspace/content_test.go` is deleted.
  These are exactly the deletions PRD criterion 2 permits as entailed by the
  accessor retirement; no test outside that set is modified or removed
- `--agent` remains accepted on `niwa create` and `niwa apply` (R14) and its
  help text describes launch-time selection only
- Comments on the dispatch refusal and the launch-coupled provisioning paths
  no longer narrate the exclusive-materialization model
- `go test ./...` passes

**Dependencies**: <<ISSUE:1>>

---

### Issue 3: feat(workspace): Codex layer composer with generation marker and regular-file-only reads

**Complexity**: testable

**Goal**: Add the net-new composer (new file, e.g.
`internal/workspace/codex_compose.go`) that builds one Codex-facing context
document from an ordered chain of layers — instance, group, repository
content, inlined committed `AGENTS.md`, worktree framing — composed
outermost-first, with the generation marker as the first line of the
document. Two rules from DESIGN Decision 2 are the substance: the never-empty
rule (no content at any layer means no output at all — never an empty,
whitespace-only, or marker-only document), and the regular-file-only read
rule (any repository file the composer reads, today the committed
`AGENTS.md`, is opened with `O_NOFOLLOW` so a symlink fails at the open; on
refusal the composer inlines nothing, still composes the workspace layers,
and returns the refusal for loud reporting).

**Acceptance Criteria**:

- Layers appear in the output outermost-first, marker line first
- A chain with no content at any layer produces no output; the caller writes
  no file (grounds PRD criterion 6)
- Whitespace-only layer content counts as empty
- A committed `AGENTS.md` that is a symlink: the open fails, nothing from the
  target appears in the output, the workspace layers still compose, and the
  refusal is surfaced to the caller
- A committed `AGENTS.md` that is any other non-regular file takes the same
  refusal path
- A regular committed `AGENTS.md` is inlined verbatim
- Unit tests cover each of the above

**Dependencies**: None

---

### Issue 4: feat(workspace): composed group AGENTS.md and per-repository AGENTS.override.md

**Complexity**: testable

**Goal**: Wire the composer into the apply pipeline
(`internal/workspace/apply.go`, `internal/workspace/content.go`): each group
directory gets a composed `AGENTS.md` carrying instance plus group (net-new
composition sharing the issue 3 composer — not the existing group writer
re-run, which would violate the composition rule), and each cloned repository
gets a composed `AGENTS.override.md` carrying instance, group, and repository
layers with any committed `AGENTS.md` inlined. Every apply recomposes from
the current config sources and the repository's current committed `AGENTS.md`
(regeneration, not append). Written files are recorded as managed files.

**Acceptance Criteria**:

- After apply, a group's `AGENTS.md` carries sentinels from the instance and
  group layers; a repository's `AGENTS.override.md` carries sentinels from
  instance, group, and repository layers (PRD criteria 1, 3 offline halves)
- In a repository shipping a committed `AGENTS.md`, the override carries both
  the workspace layers and the committed content, and the committed file is
  byte-identical after `niwa apply` (PRD criterion 5; R6, R12)
- With no configured content at any layer, no override is written and the
  repository's own committed context file keeps the directory's context slot
  (PRD criterion 6)
- After changing configured content and re-applying, the new content is
  present in the composed files and the previous content is absent (PRD
  criterion 19, instance/repository half)
- Editing a repository's committed `AGENTS.md` and re-applying refreshes the
  inlined copy
- `go test ./...` passes

**Dependencies**: <<ISSUE:1>>, <<ISSUE:3>>

---

### Issue 5: feat(workspace): instance payload with byte budget and whole-plugin skills symlinks

**Complexity**: complex

**Goal**: Write the payload `<instance>/.codex/` (new file, e.g.
`internal/workspace/codex_payload.go`): a `config.toml` declaring
`project_doc_max_bytes` sized to cover at least the byte size of the largest
composed file plus committed context files in repository subdirectories, with
generous headroom (DESIGN, "The byte budget"); and one
`skills/<plugin> -> <plugin install root>` symlink per configured plugin,
whole and verbatim (Decision 3). Resolve the plugin root per marketplace
kind: for a repository-sourced marketplace, parse the marketplace manifest
and join the plugin's declared source directory onto the marketplace root
(extending `internal/workspace/plugin.go`); for a github-sourced marketplace,
resolve into Claude Code's user-global plugin cache. A plugin root missing at
apply time skips that plugin's symlink and reports it loudly, naming the
plugin and the expected path. Every apply rewrites the config and reconciles
the symlink set against the configured plugins, repairing links whose target
niwa owns.

**Acceptance Criteria**:

- `<instance>/.codex/config.toml` exists and its declared budget exceeds the
  byte size of the full composed chain on disk by a stated headroom margin —
  an exact-fit budget fails the test, per the DESIGN's "generous headroom
  rather than a tested-once number" (PRD criterion 7, offline half)
- Each configured plugin resolves to a symlink whose target root carries the
  plugin manifest and every `references/` and `scripts/` directory the source
  has, with every file byte-identical to its source — both marketplace kinds
  covered by tests (PRD criterion 8)
- A missing plugin root: apply succeeds, no symlink is created, the report
  names the plugin and the path it expected, and the next apply creates the
  link once the root exists
- niwa writes no hook definitions and no hook-state entries anywhere (PRD
  criterion 11, offline half; Decision 5)
- No `OPENAI_API_KEY`, `forced_login_method`, or any auth-related key appears
  in any generated file (Decision 6)
- After three applies the payload contains exactly one config and one link
  per configured plugin; a de-configured plugin's link is removed (PRD
  criterion 18, payload part)
- `go test ./...` passes

**Dependencies**: <<ISSUE:4>>

---

### Issue 6: feat(workspace): per-repository .codex link and git-exclude coverage

**Complexity**: testable

**Goal**: Plant `<repo>/.codex -> <instance>/.codex` in every cloned
repository, with a real-copy fallback where directory symlinks are
unavailable, and repair a dangling or wrong-target link on apply (a link is
recognized as niwa's by its target). Extend each repository's managed
git-exclude block with the two new patterns via
`internal/gitexclude.EnsureRepoExclude`, written **bare** — `.codex` and
`AGENTS.override.md`, no trailing slash — because a trailing-slash pattern is
directory-only and git classifies a symlink as a file, which would leave
`?? .codex` in `git status` forever (DESIGN Decision 7, the feature's
highest-risk detail).

**Acceptance Criteria**:

- After apply, each cloned repository has a `.codex` link resolving to the
  instance payload; payload content is reachable through it
- Both exclude patterns appear in the managed block in bare form; a test
  asserting the literal pattern text (no trailing slash) fails if either
  gains one
- `git status` reports a clean working tree in every cloned repository with
  the link and override present (PRD criterion 12, offline half; R11)
- The copy fallback produces a real `.codex` directory that the bare pattern
  still excludes
- A deleted or retargeted link is restored on the next apply
- After three applies the managed exclude block appears exactly once per
  repository (PRD criterion 18, exclude part)
- `go test ./...` passes

**Dependencies**: <<ISSUE:5>>

---

### Issue 7: feat(workspace): conflict detection, coupled suppression, and cleanup exemption

**Complexity**: complex

**Goal**: Implement DESIGN Decision 7's conflict rule. Before writing
`.codex` or `AGENTS.override.md`, check the target path: anything niwa did
not itself materialize — tracked or untracked file, directory, or symlink —
is a conflict; niwa writes nothing at that name, modifies and deletes
nothing, and emits a loud per-repository warning in the apply output.
Ownership recognition: the `.codex` link by its target, composed files by
being untracked and carrying the generation marker (content test, so the
record-less worktree-apply path can recognize its own prior override).
Coupling in one direction: a `.codex` conflict also suppresses the override
(its budget declaration lives in the payload the refused link would reach);
an override conflict alone suppresses only the override, while link, exclude
patterns, and trust still materialize. Extend the managed-file
reconciliation in `internal/workspace/apply.go`: the apply hands its per-run
conflicted-path set to the cleanup, which skips those paths before removing
recorded paths the apply did not produce — this is a named change to build,
not existing behavior; today the cleanup tests only membership in the
produced set, so absence is the deletion trigger. Conflicted entries leave
the record; the per-repository conflict verdicts are exposed for issue 9 to
consume.

**Acceptance Criteria**:

- A repository with a committed `.codex` directory: no link, no override, a
  warning naming the repository, nothing modified or deleted (R12)
- A repository with a committed `AGENTS.override.md` only: the override is
  suppressed, the link and exclude patterns still materialize, a warning
  names the repository
- A repository whose override was written and recorded on a prior apply and
  that now ships a committed file at that name: the next apply deletes
  nothing at the path, and the path's entry leaves the managed-file record —
  an implementation that drops the record entry without teaching the cleanup
  about conflicts must fail this test by deleting the committed file
- An untracked file carrying the generation marker at niwa's name is
  recognized as niwa's own and overwritten normally
- After a conflict clears, the next apply writes fresh files and re-records
  them
- Apply exposes the per-run conflict verdicts to later pipeline stages
- `go test ./...` passes

**Dependencies**: <<ISSUE:4>>, <<ISSUE:5>>, <<ISSUE:6>>

---

### Issue 8: feat(workspace): trust entries in the developer's Codex config

**Complexity**: complex

**Goal**: On `niwa create` and `niwa apply`, upsert
`[projects."<canonical repo root>"] trust_level = "trusted"` in the
developer's Codex config for each cloned repository (new file, e.g.
`internal/workspace/codex_trust.go`), idempotently, touching no other key.
The key is the **canonicalized** repository root — full symlink resolution of
every path component (Decision 4). The write discipline is part of the work:
atomic replacement (temp file beside the config, synced, renamed over);
refusal on an unparseable pre-existing file (byte-untouched, reported as an
error that makes the apply exit non-zero after the rest of materialization
completes); and an advisory lock in niwa's own state directory, keyed by the
config path, held across the whole read-modify-write with the file re-read
under the lock. Each trust key niwa writes is recorded in instance state
(`internal/workspace/state.go`) — the record that bounds issue 9's removal
and the planned destroy-time cleanup. Codex credential and login files are
never read or written.

**Acceptance Criteria**:

- After apply, the config carries exactly one per-project entry for each
  cloned repository, keyed by a path that resolves to that repository's
  actual root — a fixture with a symlinked parent directory produces the
  resolved key, and a present-but-miskeyed entry fails the test (PRD
  criterion 10)
- After three successive applies the entry count is unchanged (PRD criteria
  10, 18)
- A pre-existing config with the developer's own settings differs afterward
  only by the added per-project entries; no pre-existing key is removed,
  reordered, or altered, and no global key is written (PRD criterion 14;
  R13)
- An unparseable pre-existing config: the file is byte-untouched, the rest
  of materialization completes, and the command exits non-zero with an error
  naming the file
- Two concurrent applies against one config leave both applies' entries
  present in valid TOML
- The developer's credential and login files are byte-identical and
  mtime-unchanged after create and apply; with the credential file at mode
  `000` in a sandboxed home, both commands exit zero (PRD criterion 13)
- Written trust keys appear in instance state
- `go test ./...` passes

**Dependencies**: <<ISSUE:1>>

---

### Issue 9: feat(workspace): conflict-driven trust withholding and record-bounded removal

**Complexity**: testable

**Goal**: Wire issue 7's conflict verdicts into issue 8's writer (Decision
7's trust clause): a repository with a `.codex` conflict gets no trust entry,
and an entry niwa wrote before the conflict existed is removed on every apply
while the conflict stands — never re-added — with the record entry cleared
alongside the removal. Removal is bounded by the instance-state record, never
by shape: Codex writes identically-shaped entries when the developer answers
its own trust prompt, which is exactly where a conflicted repository is
routed, so only keys the record names may be removed. A recorded key already
absent from the file is a no-op; an unrecorded key present in the file is
left alone.

**Acceptance Criteria**:

- First apply against a repository with a committed `.codex`: no trust entry
  is written for it, while clean repositories still get theirs
- A repository trusted on one apply and conflicted on the next: its entry is
  removed and its record entry cleared
- After removal, an identically-shaped entry at the same key that is not in
  niwa's record (the developer's own answer) survives all later applies
- While the conflict stands, repeated applies never reinstate the entry
- A recorded key already absent from the file: apply succeeds without error
- `go test ./...` passes

**Dependencies**: <<ISSUE:7>>, <<ISSUE:8>>

---

### Issue 10: feat(worktree): Codex materialization for niwa-managed worktrees

**Complexity**: testable

**Goal**: Extend the worktree lifecycle
(`internal/workspace/worktree_content.go`) with the same two per-tree writes
every clone gets: a `.codex` link with its target computed for the worktree's
location, and a composed override carrying instance, group, and repository
layers plus the worktree's own framing (repository, purpose, branch) appended
last, inlining the checkout's committed `AGENTS.md` under the same
regular-file-only rule. `niwa worktree apply` refreshes both. The standalone
worktree-apply path keeps no managed-file records, so it recognizes its own
prior override by the generation marker (issue 7's content test). No
per-worktree trust entry is written — the repository's entry covers its
worktrees. Git-exclude coverage extends to the worktree.

**Acceptance Criteria**:

- A worktree created by `niwa worktree` carries the `.codex` link and an
  override with an instance-only sentinel plus the worktree's repository and
  branch framing (PRD criterion 4, offline half)
- After changing configured content, `niwa worktree apply` delivers the new
  content and the previous content is absent (PRD criterion 19, worktree
  half)
- Re-running `niwa worktree apply` recognizes the prior override by its
  marker and refreshes it rather than reporting a conflict
- No trust entry keyed by a worktree path exists after create or apply (PRD
  criterion 10, no-separate-entry half)
- `git status` in the worktree is clean after materialization (PRD criterion
  12)
- A worktree checkout committing `AGENTS.override.md` or `.codex` degrades
  under issue 7's conflict rule, reported
- `go test ./...` passes

**Dependencies**: <<ISSUE:4>>, <<ISSUE:6>>, <<ISSUE:7>>

---

### Issue 11: test(functional): dual-agent acceptance scenarios

**Complexity**: complex

**Goal**: Give every PRD acceptance criterion end-to-end coverage in
`test/functional/` per docs/guides/functional-testing.md: rewrite
`test/functional/features/codex-agent.feature` around the dual-agent model
(its remaining exclusivity framing) and add scenarios — using the
`localGitServer` fixtures — for the criteria the unit-scope issues ground
but don't exercise through the binary. Offline checks decide what a session
"sees" against the single context file Codex's first-match rule selects;
live checks gate on `codex` being on PATH and skip when absent, following
the repo's existing `claude`-gating pattern; a live check is never sole
coverage except for the interactive start.

**Acceptance Criteria**:

- Every one of the PRD's 19 acceptance criteria has a scenario or step
  asserting it; a coverage note in the feature file maps criteria to
  scenarios
- Offline scenarios cover: instance-root selection (criterion 1); deep
  in-repo selection with a committed context file in an intermediate
  directory, from a shell with no `NIWA_*` variables and no `CODEX_HOME` set
  (criterion 3); worktree sentinel plus framing (criterion 4);
  committed-`AGENTS.md` coexistence and byte-identity (criterion 5); the
  no-content degenerate case (criterion 6); a >32768-byte instance+group
  fixture whose declared budget still covers the chain (criterion 7);
  skills byte-identity and namespacing (criterion 8); trust-entry count,
  canonical keying, and worktree coverage (criterion 10); no hook
  definitions or state (criterion 11 offline half); clean `git status`
  everywhere (criterion 12); credential byte/mtime identity and the
  mode-`000` sandbox (criterion 13); config additivity (criterion 14); the
  three compatibility cases (criteria 15–17); triple-apply idempotency
  (criterion 18); and content refresh for instance and worktree (criterion
  19)
- Live, `codex`-gated scenarios cover: first-attempt file write (criterion
  9), interactive clean start under a PTY from root and nested directories
  (criterion 11), and post-session `git status` (criterion 12)
- Hostile fixtures are exercised: a committed `.codex` directory, a
  committed `AGENTS.override.md`, and an `AGENTS.md` committed as a symlink
- The create → apply workflow scenarios carry the `@critical` tag per repo
  convention
- `make test-functional` passes with `codex` absent (live scenarios skip)

**Dependencies**: <<ISSUE:7>>, <<ISSUE:9>>, <<ISSUE:10>>

---

### Issue 12: docs(guides): dual-agent workspace guide

**Complexity**: simple

**Goal**: Add `docs/guides/dual-agent-workspace.md` — the user-visible
surface the DESIGN's `user_visible_surface: true` warrants — covering what
dual-agent preparation produces and what it writes where: the payload and
its budget, the per-repository link and override, the composed instance and
group files, the trust entries in the developer's Codex config and the write
discipline behind them, how conflicts with committed content degrade and how
to resolve them, what worktrees get, and what is deliberately not written
(hooks, credentials, global keys). Add the guide to the Contributor Guides
list in the repository's CLAUDE.md.

**Acceptance Criteria**:

- `docs/guides/dual-agent-workspace.md` exists and covers each surface
  listed in the goal
- The conflict section names both degradation shapes (`.codex` costs the
  repository everything; `AGENTS.override.md` alone costs only the composed
  context) and the resolution paths
- The guide states what niwa never writes: hooks, hook state, API keys,
  auth keys, global Codex keys
- CLAUDE.md's Contributor Guides list links the new guide with a one-line
  description matching the existing entries' style
- The prose follows the repo's writing conventions (no emojis, direct
  style)

**Dependencies**: <<ISSUE:10>>

---

## Implementation Sequence

**Critical path:** Issue 1 → Issue 4 → Issue 5 → Issue 6 → Issue 7 → Issues
9 and 10 → Issue 11.

Open with issue 1: it lands the architectural pivot with the smallest new
write surface, proves R14 before any repository is touched, and establishes
the per-agent pass and test baseline every batch-2 writer rides. Issue 3 (the
composer, pure library code) can be built in parallel from the start; issue 2
(accessor retirement) follows issue 1 at any point.

Batch 2 proceeds in order along the critical path: composed files (4), the
payload (5), per-repository delivery (6), then the conflict rule (7), which
carries the feature's highest-risk details and consumes everything before it.

Issue 8 (the trust writer) can proceed in parallel with batch 2 any time
after issue 1 — it is the only write outside the instance and deserves its
own review focus. Issue 9 joins it to issue 7's conflict verdicts once both
are in.

Issue 10 extends the per-tree writers to worktrees after 4, 6, and 7. Issue
11 closes with acceptance coverage over the full mechanism, and issue 12
lands the guide last, once the behavior it documents is final.

**Parallelization opportunity:** {1, 3} first; after issue 1, issues 2 and 8
join issue 3 as parallel tracks beside the 4 → 5 → 6 → 7 chain.
