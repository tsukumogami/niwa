---
schema: plan/v1
status: Active
execution_mode: single-pr
tracking_level: none
upstream: docs/designs/DESIGN-codex-instance-root-skills.md
milestone: "codex instance root skills"
issue_count: 7
---

# PLAN: codex instance root skills

## Status

Active

Single PR on one branch, seven commit-sized issues, no GitHub issues or
milestone (tracking level `none`). Source design:
docs/designs/DESIGN-codex-instance-root-skills.md (status Planned).
Requirements R1-R19 and N1-N2 live in
docs/prds/PRD-codex-instance-root-skills.md; each outline below names the
requirements it carries.

## Scope Summary

Deliver workspace plugin skills and niwa's own plugin to a Codex session
at the workspace instance root through the existing plan machinery, flip
capability rows 18 (`RootProjectSkills`) and 19 (`NiwaPlugin`) to
implemented for Codex with both capabilities bound to named registered
deliveries for both agents, re-gate the dispatch warning on an exported
payload-scope predicate, and prove the result with an offline placement
scenario that gates CI plus a credential-free live discovery scenario
against the real binary.

## Decomposition Strategy

**Horizontal.** The seven issues are the design's seven increments, in the
design's order. The end-to-end path deliberately goes live only at the
flip (issue 6), so a skeleton-first slice is unavailable: the declaration
table's rule is that a row flips in the change that delivers it, and
everything before the flip must be inert or behavior-preserving so the
suite stays green at every commit.

The ordering is load-bearing, and three of the design's decisions force
it:

- **The one-PR requirement is the PRD's, not a preference.** R8 requires
  both rows to read implemented in the same change that lands their
  deliveries, so the whole sequence is one pull request
  (`execution_mode: single-pr`). Mechanically the flip and the deliveries
  could land in either order without a test failing -- a producer gated
  on an unavailable capability yields an empty plan -- so the one-change
  rule is the table's own stated convention, held here deliberately.
- **The warning re-gate (issue 5) lands before the flip (issue 6)** so
  the dispatch warning never has a silent window. Its old gate (row 18
  not implemented) goes silent the moment the row flips; the new gate
  (the payload-scope predicate) is already true for Codex before the
  flip, so re-gating first keeps the warning firing continuously across
  the transition.
- **Issue 6 is one commit.** The flip, the bound-set and binding entries,
  the rewritten column and lookup tests, the regenerated gap list, the
  corrected prose, and the matrix amendment move together, so the
  contract, its tests, and its record never disagree on the branch.

Two structural facts shape the front of the sequence. `internal/plugin`
imports `internal/workspace` today, so registering row 19's procedures in
the workspace-side registry is an import cycle until issue 1 removes it
-- the cycle is three symbols deep and cheaper to remove than to bridge.
And the leaf (`internal/agentplan`) decides every name and shape the
executor registers, so issue 3 precedes issue 4.

Verification runs per issue with the commands named in each outline;
the branch-level gate is `go build ./...`, `go vet ./...`,
`go test ./...`, and `make test-functional` (the functional suite must
never run concurrently with another run in the same checkout).

## Issue Outlines

### Issue 1: refactor(plugin): unhook internal/plugin from internal/workspace

**Goal**: Make `internal/plugin` a true leaf so both row-19 procedures
can be runnable registered values instead of identities for code behind
a seam (R11; groundwork for R9).

**Acceptance Criteria**:
- [ ] `internal/plugin` no longer imports `internal/workspace`:
  `go list -f '{{join .Deps "\n"}}' ./internal/plugin | grep -c 'internal/workspace'`
  prints 0.
- [ ] `plugin.Install` drops the always-nil state parameter, takes the
  developer home as data (the `EnsureCodexTrust` posture), and returns
  its `Action` for the caller to report; notice emission moves to the
  caller.
- [ ] `plugin.MaterializeTo(dst)` is exported, wrapping the existing
  `writeEmbeddedTree` behavior (needed by <<ISSUE:4>>).
- [ ] `internal/cli/plugin_adapter.go` and the
  `Applier.InstallNiwaPlugin` function-field seam are deleted; the
  former call sites reach `plugin.Install` directly through a
  deliver-pass stub (replaced by `procedureFor` in <<ISSUE:4>>).
- [ ] Behavior-preserving: the existing installer tests move with the
  signature and pass;
  `go build ./... && go vet ./... && go test ./internal/plugin ./internal/cli ./internal/workspace`
  is green.

**Dependencies**: None

**Type**: code

### Issue 2: feat(plugin): add the Claude plugin manifest to the embedded tree

**Goal**: Add `.claude-plugin/plugin.json` to the embedded tree so the
delivered skill resolves namespaced as `niwa:niwa-migrate-config`,
matching every other delivered plugin (R2; part of the R10 disposition).

**Acceptance Criteria**:
- [ ] `internal/plugin/files/niwa/.claude-plugin/plugin.json` exists and
  names the plugin `niwa`.
- [ ] `go test ./internal/plugin` passes unchanged -- the file rides
  along to the install path with no behavior change (`Embedded()` reads
  only `manifest.json`, the idempotency check compares only
  `manifest.json`, `stageAndRename` copies the whole tree, and no test
  pins the tree's file set).
- [ ] The resolved name `niwa:niwa-migrate-config` is what
  <<ISSUE:7>>'s live scenario asserts; the doubled `niwa` comes from the
  skill's own existing frontmatter name, and renaming it is out of
  scope.

**Dependencies**: None

**Type**: code

### Issue 3: feat(agentplan): add root skills and niwa-plugin leaf vocabulary

**Goal**: Give the leaf the producer methods, name constant,
payload-scope predicate, and delivery constants both rows need -- all
inert while the rows are unavailable (R1, R6, R12, R13 groundwork).

**Acceptance Criteria**:
- [ ] `Producer.RootSkillsPlan(in RootSkillsInputs) (*Plan, error)`
  exists as a sibling of `SkillsPlan` (the `RootContextPlan`-beside-
  `RepoContextPlan` precedent): same `skillsDir` path helper, same
  `OpDeliverTree`/`IfSourceExists` entry shape, gated on
  `p.delivers(RootProjectSkills)`, every entry tagged
  `Capability: RootProjectSkills`. When `NiwaPlugin` is implemented for
  the agent it skips a configured plugin named `NiwaPluginTreeName`,
  recording the refusal in `Plan.Warnings`.
- [ ] `Producer.RootSkillsReconcileSpec(in RootSkillsInputs)
  SkillsReconcileSpec` exists; `Keep` is the deliverable configured
  names plus `NiwaPluginTreeName` when `NiwaPlugin` is implemented, so
  reconcile never removes row 19's delivery.
- [ ] `Producer.NiwaPluginPlan(in NiwaPluginInputs) (*Plan, error)`
  exists: one `OpDeliverTree` entry tagged `Capability: NiwaPlugin`,
  path built by `skillsDir`.
- [ ] `NiwaPluginTreeName` (`"niwa"`) is the exported constant both the
  collision rule and the reconcile `Keep` set read.
- [ ] The exported payload-scope predicate (shaped like
  `ConfigDocRepoScoped(ag agent.Agent) bool`; exact name settled here)
  returns true exactly when the agent's payload layout scope is
  `PayloadInRepo`; unit tests cover Codex (true), Claude (false), and an
  agent with no payload layout (false).
- [ ] The four `Delivery` constants exist: `DeliveryRootSkills`
  (`"root-skills"`), `DeliveryRootSettings` (`"root-settings"`),
  `DeliveryNiwaPluginClaude` (`"niwa-plugin-claude"`),
  `DeliveryNiwaPluginCodex` (`"niwa-plugin-codex"`).
- [ ] Unit tests assert the gated methods yield empty plans while the
  rows are unavailable; `go test ./internal/agentplan` is green with no
  change to existing declarations.

**Dependencies**: None

**Type**: code

### Issue 4: feat(workspace): register the root skills and niwa-plugin deliveries

**Goal**: Land the executor side of both rows -- two materializers, two
procedures, and the pipeline wiring -- all inert until the flip (R4, R5,
R6, R11, R12, N2).

**Acceptance Criteria**:
- [ ] `RootSkillsMaterializer` is registered in `deliveries` under
  `agentplan.DeliveryRootSkills`; `Materialize` reconciles from
  `RootSkillsReconcileSpec`, produces `RootSkillsPlan`, and executes
  through `applyPlan`. The pipeline drives it at the instance-root step
  per agent, gated by the workspace-level `AgentEnabled` lookup with the
  empty repository name, reusing the plugin trees step 6.2 already
  resolved.
- [ ] `InstallWorkspaceRootSettings` converts to
  `RootSettingsMaterializer` (`Name() == "root-settings"`, registered
  under `agentplan.DeliveryRootSettings`), carrying its non-context
  inputs as struct fields; its call site drives the type's method; same
  document, same path, same bytes.
- [ ] `claudeNiwaPluginProcedure` (`"niwa-plugin-claude"`) and
  `codexNiwaPluginProcedure` (`"niwa-plugin-codex"`) are registered in
  `procedures` and reached through `procedureFor(NiwaPlugin, ag)` on a
  deliver pass shaped like `deliverDirectoryTrust`, replacing the
  <<ISSUE:1>> stub. Claude's `Deliver` calls `plugin.Install` with the
  input's developer home; Codex's materializes the embedded tree at
  `<instanceRoot>/.niwa/plugin/niwa` (idempotent by manifest-version
  comparison, path-stably replaced: stage beside, remove-and-rename at
  the same path) and delivers it into the root skills directory via
  `NiwaPluginPlan` through `applyPlan`.
- [ ] `procedureInput` gains `InstanceRoot` and `Producer`; the trust
  procedure ignores both.
- [ ] Root-delivery git exclusions ride `EnsureInstanceGitignore` /
  `InstanceExcludePatterns` only; a test forbids `EnsureRepoExclude` on
  the root path, which searches upward and could write into an enclosing
  repository's exclude file.
- [ ] A test configures a marketplace named `niwa` and asserts its
  fetched content and the extracted embedded tree exist independently
  (the `.niwa/plugin/niwa` site cannot collide by construction).
- [ ] Where the contract declares these capabilities implemented, a
  delivery failure fails the apply with an error naming the capability
  (unit-tested with an unwritable target).
- [ ] The structural scans pass with no new exemption: no agent constant
  at the materializer call sites, no agent name or agent context
  filename in `internal/workspace`; the root delivery reaches
  `.codex/skills` only through the producer's `skillsDir`.
- [ ] Inert until the flip: with rows 18 and 19 still unavailable,
  `go build ./... && go vet ./... && go test ./...` passes and apply
  behavior is unchanged.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:3>>

**Type**: code

### Issue 5: refactor(cli): gate the dispatch warning on the payload-scope predicate

**Goal**: Re-gate the dispatch warning on the exported payload-scope
predicate and rewrite its text, before the flip, so the warning never
has a silent window (R13).

**Acceptance Criteria**:
- [ ] `internal/cli/dispatch.go` gates the warning on the exported
  payload-scope predicate -- not on
  `agentplan.Lookup(RootProjectSkills, ...)` and not on the agent's
  name.
- [ ] The text names what genuinely stays missing for a repo-scoped
  agent: MCP servers, the session environment, and the approval and
  sandbox posture. It no longer claims skills among them.
- [ ] Dispatching Claude (payload scope `PayloadAtInstanceRoot`) or an
  agent with no payload layout prints no warning.
- [ ] Both pinning tests (`dispatch_agentwarning_test.go`,
  `dispatch_contract_test.go`) move with the gate in this change; the
  contract test asserts the gate reads the predicate.
- [ ] `go test ./internal/cli` is green, and the warning still fires for
  a dispatched Codex worker both before and after the flip.

**Dependencies**: Blocked by <<ISSUE:3>>

**Type**: code

### Issue 6: feat(agentplan): flip rows 18 and 19 for codex and bind both capabilities

**Goal**: In one commit, flip both rows to implemented for Codex, bind
both capabilities for both agents, and move the whole record -- tests,
gap list, prose, and matrix amendment (R8, R9, R10, R14, R15, R16).

**Acceptance Criteria**:
- [ ] Rows 18 (`RootProjectSkills`) and 19 (`NiwaPlugin`) read
  `StateImplemented` for Codex in `internal/agentplan/declaration.go`;
  `boundCapabilities` gains both capabilities; `bindings` gains the four
  rows naming `DeliveryRootSkills`, `DeliveryRootSettings`,
  `DeliveryNiwaPluginClaude`, `DeliveryNiwaPluginCodex`.
- [ ] `TestCodexColumnTotals`'s hardcoded `13, 11` becomes `15, 9`;
  `codexDelivered` gains both rows; `codexFinalGaps` loses `NiwaPlugin`;
  `TestLookupAnswersEachDeclaredPair`'s literal
  `{NiwaPlugin, Codex, StateUnavailable, ReasonNotBuilt}` case is
  removed. After the flip no Codex row carries `ReasonNotBuilt`, so the
  "niwa's own debt" category and the comments around both lists are
  rewritten, not patched.
- [ ] `TestBindingsMatchTheirDeclarations`,
  `TestDeliveriesMatchTheBindings`, and
  `TestRegisteredDeliveriesAreWhatTheyClaim` pass and are load-bearing
  for the four new rows: reverting a declaration while its delivery
  stays registered fails, as does deleting a registration while the
  declaration stands.
- [ ] The guide's gap list regenerates via
  `go test ./internal/agentplan -run TestCodexGuideGapSectionMatchesDeclarations -update`;
  the drift test then passes with no manual edit to the generated block,
  and neither row's bullet appears (the generator omits an empty group,
  so the "What niwa hasn't built yet" heading disappears on its own).
- [ ] Authored prose corrected by hand: the guide's "Starting a session
  at the instance root" section (the "gets nothing else" account dies;
  the budget paragraph narrows -- a `.codex/` directory now exists at
  the root but carries no config document), the feature file's "lands
  inside a repository and nowhere else" preamble, and
  `TestEachAgentTakesOneScope`'s doc comment.
- [ ] `docs/prds/PRD-agent-capability-contract.md` takes an appended
  below-the-table amendment for rows 18 and 19 in its established
  style, naming the tests as the authority its totals are not; the
  matrix body is untouched. The amendment and the
  `(NiwaPlugin, Claude)` binding comment state the Claude claim
  exactly: the delivery materializes niwa's plugin tree at the
  user-level install path in Claude Code's plugin format; it does not
  claim a Claude session resolves it. The registration defect is
  recorded as a one-machine observation.
- [ ] `go build ./... && go vet ./... && go test ./...` green at this
  commit, which is a single commit.

**Dependencies**: Blocked by <<ISSUE:2>>, <<ISSUE:4>>, <<ISSUE:5>>

**Type**: code

### Issue 7: test(functional): add root skills placement and discovery scenarios

**Goal**: Prove the delivery with two scenarios that carry different
claims -- an offline placement scenario that gates CI, and a
credential-free live scenario that carries the acceptance bar on
discovery (R18, R19; asserts R2's name and R5/R6 behavior end to end).

**Acceptance Criteria**:
- [ ] Offline scenario in `test/functional/features/codex-agent.feature`,
  mirroring the per-repository skills scenario at the instance root with
  the existing step vocabulary: the instance root holds exactly the
  expected Codex skills trees, each mirroring its source; the directory
  one level above the instance root holds zero; a second apply changes
  nothing; removing a plugin from the configuration removes its root
  tree on the next apply.
- [ ] The collision behaviors are asserted: a workspace configuring a
  marketplace named `niwa` yields both trees independently; a workspace
  plugin named `niwa` is skipped at the root with a warning naming both
  sources, while its per-repository delivery is unchanged.
- [ ] The offline scenario passes in CI (which has no Codex binary) and
  gates it. It proves placement -- what niwa wrote and where -- and is
  never described as proving discovery, because no session runs.
- [ ] Live scenario: `codex debug prompt-input` renders the
  resolved-skills block under an isolated `CODEX_HOME` with no
  credential file present and no model turn. Run at the instance root,
  every delivered plugin's skills appear namespaced,
  `niwa:niwa-migrate-config` included; run from a directory whose skills
  tree sits one level up, that tree's skills do not appear. The negative
  control is a real tree that fails to load -- that is what carries the
  discovery claim.
- [ ] The live scenario has its own new tag and a gate that checks only
  that the `codex` binary is on PATH: it skips on a machine without
  Codex, never on a machine without a login, and does not reuse the
  credential-copying gate.
- [ ] `make test-functional` passes locally (never run concurrently with
  another functional run in the same checkout); `go test ./...` stays
  green.

**Dependencies**: Blocked by <<ISSUE:6>>

**Type**: code

## Implementation Sequence

**Recommended order: 1, 2, 3, 4, 5, 6, 7** -- the design's own increment
order. It satisfies every dependency edge, and it is the order in which
each commit keeps the suite green: 1 is behavior-preserving, 2 is inert
on the install path, 3 and 4 are gated off while the rows are
unavailable, 5 changes the warning's gate to a predicate that is already
true for Codex, 6 flips everything at once, and 7 proves it.

**Dependencies**, stated rather than drawn, since this is a single-PR plan
and the commits land in one order anyway: issue 4 needs issues 1 and 3;
issue 5 needs issue 3; issue 6 needs issues 2, 4 and 5; issue 7 needs
issue 6. Issues 1, 2 and 3 depend on nothing.

**Critical path**: 1 (or 3) -> 4 -> 6 -> 7, length 4.

**Parallelization**: theoretical only -- commits land sequentially on
one branch. Issues 1, 2, and 3 are mutually independent; issue 5 needs
only issue 3.

**Ordering constraints that outrank convenience**: issue 5 must precede
issue 6 (the warning's no-silent-window rule), and issue 6 must be a
single commit (the declaration table's one-change rule). Any
re-sequencing that preserves the dependency edges must also preserve
these two properties.

**Branch-level verification**: `go build ./...`, `go vet ./...`,
`go test ./...` at every commit; `make test-functional` before the PR
is ready (never concurrently with another functional run in the same
checkout). The PRD's acceptance criteria map onto the outlines as
follows: the delivery criteria land in issues 4 and 7, the contract and
structure criteria in issues 4 and 6, the warning and documentation
criteria in issues 5 and 6, and the symlink-target position (R7) plus
the spike measurements (R17) are already recorded durably -- R7 in the
design's Decision 2 and R17 as amendments landed in
docs/spikes/SPIKE-codex-discovery-mechanics.md with the design, so no
issue re-does them.
