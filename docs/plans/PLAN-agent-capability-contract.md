---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/current/DESIGN-agent-capability-contract.md
milestone: "Agent Capability Contract"
issue_count: 7
---

# PLAN: agent capability contract

## Status

Active

This plan covers the first of two mandated pull requests (R9 of
docs/prds/PRD-agent-capability-contract.md): the contract against existing
Claude behavior only, provably without behavior change. The second pull
request -- Codex delivered through this contract -- is the companion plan,
docs/plans/PLAN-codex-agent-delivery.md, and does not start until this
plan's PR has merged.

## Scope Summary

Land the agent capability contract the upstream design settles, with no
observable behavior change: a leaf `internal/agentplan` package holding the
closed 24-capability set, the two-state declaration table with reason kinds
and `Requires` edges, and per-agent plan producers over a closed four-op
vocabulary; one agent-blind executor in `internal/workspace`; three
stdlib-only structural tests; and a ManagedFiles characterization test
committed before any refactor. Claude's declaration column lands complete;
the Codex column states main's truth -- nothing delivered.

The two-PR split isn't a preference: the prior attempt
(tsukumogami/niwa#248) buried its structure inside eleven thousand lines
where it couldn't be reviewed on its own merits, and the PRD forbids
repeating that. This PR's whole job is to be invisible, and issue 7 is the
gate that proves it.

## Decomposition Strategy

**Horizontal by structural layer, characterization first.** Pin current
behavior (issue 1), declare the contract (issue 2), enforce it (issues
3-4), convert the agent-shaped surfaces under it (issues 5-6), verify
(issue 7). The delivery-side restructuring covers exactly the surfaces that
are agent-shaped today -- the eight context-writer sites and the
settings-document builder -- per R11; agent-agnostic materializers are
declared and bound but not restructured.

One intra-PR exception to test-green-at-every-issue is deliberate: the
layout scan's filename half (issue 3) is red at eight verified sites until
issues 5 and 6 convert them. That redness is a deliverable -- it proves the
test can fail, which is the property the prior attempt's structure lacked --
and it closes before the PR's head. R5 requires exactly this
meaningful-on-day-one behavior.

The ordering overview:

| Issue | Depends on |
|-------|------------|
| 1 characterization test | None |
| 2 agentplan package | 1 |
| 3 structural test suite | 2 |
| 4 executor + wiring test | 2 |
| 5 context-writer conversion | 3, 4 |
| 6 settings-builder conversion | 4 |
| 7 PR verification gate | 5, 6 |

The 1 -> 2 edge encodes R10's commit-order mandate: the characterization
test predates the first refactor commit, so it pins current behavior rather
than being written to match new code.

## Issue Outlines

### Issue 1: test(workspace): ManagedFiles characterization test pinning current apply output

**Complexity**: complex

**Type**: code

**Requirements**: R10, N1, N3

**Goal**: Commit, before any refactor and in its own commit, a
characterization test that pins every file the preparation path writes.
Build one or two representative fixture workspaces (reusing the existing
`TestCreateIntegration`-style fixture idiom), run `Create`, and assert the
sorted `(Path, ContentHash)` pairs from `state.ManagedFiles`
(`internal/workspace/apply.go` Step 7, `state.go`) against a checked-in
expectation -- the apply path's own record of what it wrote, not a
hand-picked subset.

**Acceptance Criteria**:
- The test reads `InstanceState.ManagedFiles` and compares the full sorted
  `(Path, ContentHash)` set against a committed expectation file
- The `{workspace}` template variable is normalized: the fixture's absolute
  instance root is replaced with a placeholder before comparison, and the
  fixture content exercises the variable rather than avoiding it
- The `os.Executable()` path in worktree-delegation hook commands is
  normalized by injecting a fixed `WorktreeDelegation.NiwaPath` through the
  existing `Applier` seam -- no production change needed
- `ManagedFile.Generated` timestamps are stripped before comparison
- The fixtures exercise the overlay-append, subdir-content, and `@import`
  migration boundary rules explicitly, since those are what issue 5's
  conversion is most likely to drop
- The test passes identically across machines and repeated runs (N3)
- The test lands in its own commit, first on the branch, containing no
  production-code change
- Only stdlib and existing test helpers are used; no new module
  dependency (N1)

**Dependencies**: None

**Files**: `internal/workspace/` (new test file plus fixtures and a
checked-in expectation)

---

### Issue 2: feat(agentplan): leaf package with capability set, declaration table, and plan types

**Complexity**: testable

**Type**: code

**Requirements**: R1, R2, R3, R11

**Goal**: Create `internal/agentplan` as a leaf package -- importing
`internal/agent` and `internal/config` and nothing above them -- holding the
closed capability enumeration, the per-agent declaration table, and the plan
types. Export `agent.All()` from `internal/agent` (today `known` is
unexported and every "for each agent" test hand-lists the constants).

**Acceptance Criteria**:
- `Capability` constants exist for the PRD's 24 rows; `agentplan.All()`
  returns the closed set; the two deliberate exclusions (vault-backed secret
  resolution, the `claude.enabled` gate) are recorded in the package doc
  beside the set
- `State` (Implemented/Unavailable), `ReasonKind` (cannot-receive,
  no-such-concept, not-built), and `Declaration` with `Requires
  []Capability` match the design's Decision 2 shape
- The declaration table carries Claude's column complete and the Codex
  column stating main's truth: nothing delivered (all rows unavailable with
  reasons except none implemented)
- Declaration lookup is fail-closed, mirroring `vault.Registry.Build`: an
  unknown (capability, agent) pair is an error, never a silent default
- The plan types land: `Op` (the closed four members including
  `OpDeliverTree`), `Precondition`, `Entry` with `Managed`/`ExcludeAs`/
  `Sources`, and `Plan` -- `Exempt` is deferred to the companion plan with
  its first consumer, per the design's no-dead-seam rule
- `SourceEntry` and `ComputeSourceFingerprint` migrate from
  `internal/workspace/state.go` with JSON tags and the state schema
  unchanged; existing callers compile against the new location
- `agent.All()` is exported and used by at least one existing test in place
  of a hand-listed pair
- Package doc names the callers and the import cycle the boundary avoids,
  per house style

**Dependencies**: <<ISSUE:1>>

**Files**: `internal/agentplan/` (new), `internal/agent/agent.go`,
`internal/workspace/state.go`

---

### Issue 3: test(agentplan): structural test suite -- layout scan, exhaustiveness, closure, binding

**Complexity**: testable

**Type**: code

**Requirements**: R2, R3, R4, R5, R6, N1

**Goal**: Land the layout scan and the exhaustiveness/plan-shape table
test, using only `go/parser`, `go/ast`, and `go/token`. Register the
delivery bindings for today's agent-agnostic materializers (dotenv files,
arbitrary file distribution, hook installation) so the binding half of the
table test has a registry to check -- these materializers are declared and
bound under the contract but not restructured (R11).

**Acceptance Criteria**:
- The layout scan asserts, over non-test files in `internal/workspace`,
  that no code names `agent.AgentClaude`/`agent.AgentCodex` and no string
  literal in {`"CLAUDE.md"`, `"CLAUDE.local.md"`, `"AGENTS.md"`,
  `"AGENTS.override.md"`} appears; and over `internal/agentplan`, that no
  call to `os.WriteFile`, `os.MkdirAll`, `os.Symlink`, `os.Remove`,
  `os.Chmod`, or `exec.Command` exists
- On the pre-conversion tree the filename half fails at exactly the eight
  known sites: `content.go:156`, `content.go:186`, `content.go:208`,
  `worktree_content.go:743`, `workspace_context.go:196`,
  `workspace_context.go:229`, `workspace_context.go:411`, and the dead
  `rootClaudeFile` constant at `root_materializer.go:51` -- the failure
  message names each site
- The table test enumerates `agent.All() x agentplan.All()`: exactly one
  declaration per pair; deleting any single declaration makes it fail
- Well-formedness is asserted: unavailable implies non-empty `Reason` and
  in-range `Kind`; implemented implies both zero; `Requires` empty unless
  implemented
- Closure is asserted: every capability named in `Requires` is implemented
  for the same agent, and the requirement graph is acyclic
- Binding is asserted in both directions: an implemented declaration with
  no producer path or registered delivery behind it fails, and a delivery
  registered for a pair not declared implemented fails
- No new module dependency; the suite runs under `go test -race ./...`

**Dependencies**: <<ISSUE:2>>

**Files**: `internal/agentplan/` (test files), `internal/workspace/`
(delivery-binding registration)

---

### Issue 4: feat(workspace): agent-blind executor and the wiring test

**Complexity**: testable

**Type**: code

**Requirements**: R4, R5, R6, R11

**Goal**: Add `applyPlan(*Plan) (written []string, excludes []string, err
error)` to `internal/workspace`: MkdirAll-and-write for `OpWriteFile`,
append-unless-present for `OpAppendLine`, delimited-section replace for
`OpReplaceSection`. Preconditions execute generically (`IfSourceExists`
stats the entry's `Source`; `IfNotForeign` consults the ownership verdict at
write time). `OpDeliverTree` stays an enum member with no executor arm until
the companion plan delivers its first consumer (the skills issue); an entry
carrying it errors loudly. Land the wiring test in the same issue.

**Acceptance Criteria**:
- `applyPlan` implements the three ops in scope here with the modes carried
  on entries (0o600, 0o644, 0o755) and contains no agent name and no agent
  context filename
- `runPipeline` Step 7 reads `Managed` and `Sources` off plan entries for
  plan-produced paths instead of a bare path list plus a side map
- `ExcludeAs` patterns feed the existing per-repo git-exclude call
- An `OpDeliverTree` entry produces a named error, not a silent skip
- The wiring test asserts the executor's only entry point takes a `*Plan`,
  and that `internal/workspace` constructs no composite literal of the plan
  types outside the leaf package -- plans come only from
  `agentplan.Plan(ag, ...)`
- Unit tests cover each op's semantics, including append idempotence and
  section-replace on a missing marker

**Dependencies**: <<ISSUE:2>>

**Files**: `internal/workspace/` (executor plus tests),
`internal/agentplan/` (wiring test may live beside the layout scan)

---

### Issue 5: refactor(workspace): convert the eight context-writer sites to plan producers

**Complexity**: complex

**Type**: code

**Requirements**: R5, R11

**Goal**: Convert the eight context-writer sites named in issue 3 to plan
production: `internal/workspace` fills narrow `Inputs` structs from
`EffectiveConfig`, `agentplan.Plan(ag, in)` declares the entries, and
`applyPlan` writes them. Producers consult the agent's filename accessors,
which retires `agent.LocalContextFileName()`'s zero-caller state. Delete the
dead `rootClaudeFile` constant.

**Acceptance Criteria**:
- All eight sites route through plan production; the layout scan's filename
  half goes green for `content.go`, `worktree_content.go`,
  `workspace_context.go`, and `root_materializer.go`
- The accumulated boundary rules survive conversion verbatim: overlay
  append, subdir content handling, and the `@import` migration removals --
  each is exercised by the characterization fixture and the pinned manifest
  is unchanged
- `InstallRepoContentTo` and `installWorktreeContextLayer` no longer take an
  agent parameter they use only as a gate: the agent reaches the filename
  through the producer
- `agent.LocalContextFileName()` has callers again (from the producers) or
  is absorbed into the producer's filename table -- either way, no dead
  accessor remains
- The characterization test (issue 1) passes unchanged
- No existing test is modified or deleted

**Dependencies**: <<ISSUE:3>>, <<ISSUE:4>>

**Files**: `internal/workspace/content.go`,
`internal/workspace/worktree_content.go`,
`internal/workspace/workspace_context.go`,
`internal/workspace/root_materializer.go`, `internal/agentplan/`

---

### Issue 6: refactor(workspace): convert the settings-document builder call sites

**Complexity**: simple

**Type**: code

**Requirements**: R11

**Goal**: `buildSettingsDoc` already returns a document and writes nothing;
its three call sites each carry their own marshal-mkdir-write copy. Convert
the settings delivery to plan production so the document lands through
`applyPlan`, deleting two of the three copies.

**Acceptance Criteria**:
- The settings documents an instance receives are declared as plan entries
  and written by the executor
- Two of the three marshal-mkdir-write copies are deleted; the remaining
  call site (if any survives outside the preparation path) is documented in
  the commit message
- The characterization test passes unchanged; no existing test is modified
  or deleted

**Dependencies**: <<ISSUE:4>>

**Files**: `internal/workspace/` (settings materializer and call sites)

---

### Issue 7: task(workspace): PR verification gate

**Complexity**: simple

**Type**: task

**Requirements**: R9, R10, N2

**Goal**: Verify the PR's no-behavior-change claim end to end before merge,
and record the evidence in the PR description. This gate is also what the
companion plan (docs/plans/PLAN-codex-agent-delivery.md) waits on.

**Acceptance Criteria**:
- The characterization test passes unchanged at PR head
- The full suite passes with no existing test modified or deleted; new
  tests only added
- The three structural tests are green, including the layout scan's
  filename half
- Generated example configuration is byte-identical; the same config keys
  parse before and after; no new warnings are emitted
- `gofmt -l .` is clean, `go vet ./...` passes, `go test -race ./...`
  passes
- One manual functional-suite run is performed as a sanity check and its
  result recorded in the PR description
- The PR description names the characterization commit's SHA and confirms
  it predates the first refactor commit

**Dependencies**: <<ISSUE:5>>, <<ISSUE:6>>

**Files**: none (verification and PR description only)

---

## Dependency Graph

_Empty in single-pr mode per the PLAN format spec. The ordering overview is the table at the end of the Decomposition Strategy section, and each outline's Dependencies line declares its own edges._

## Implementation Sequence

**Strictly ordered spine with one parallel pair**: issue 1 first and alone
-- its commit must predate everything, which is the whole point of a
characterization test. Then issue 2. Issues 3 and 4 can proceed in parallel
off issue 2. Issue 5 waits for both; issue 6 needs only 4. Issue 7 gates
the merge.

Between issue 3 landing and issues 5-6 completing, the layout scan's
filename half is red at the eight named sites. That window is intended --
the design treats the red-then-green transition as this PR's proof that the
test can fail. The PR merges only at green.

**Where "no behavior change" is hardest to defend**: issue 5. The eight
context writers carry accumulated boundary rules -- overlay append, subdir
content, the `@import` migration removals -- and two of them
(`InstallRepoContentTo`, `installWorktreeContextLayer`) are already
half-broken on main: they take an agent parameter, use it only as a gate,
then hardcode the filename. A mechanical conversion can silently drop a
rule. This is exactly what issue 1's pinned manifest is pointed at: any
dropped rule changes a produced path or hash and fails the
characterization test. Issue 1's fixtures therefore cover those three rules
explicitly, as its acceptance criteria state.

## References

- docs/designs/current/DESIGN-agent-capability-contract.md -- the upstream
  design; its Implementation Approach section is this plan's skeleton.
- docs/prds/PRD-agent-capability-contract.md -- requirements R1-R24,
  N1-N3, the 24-row capability matrix, and the acceptance criteria each
  issue's ACs trace to.
- docs/briefs/BRIEF-agent-capability-contract.md -- the framing: contract
  first, provably behavior-preserving, Codex as second implementation.
- docs/plans/PLAN-codex-agent-delivery.md -- the companion plan delivering
  Codex through this contract; starts only after this plan's PR merges.
- tsukumogami/niwa#248 (branch retained at `docs/dual-agent-workspace`) --
  the closed prior attempt whose structural failure issues 3-4 make
  unrepeatable.
