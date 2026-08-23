---
schema: prd/v1
status: Accepted
problem: |
  Every background worker niwa dispatches stands at a workspace instance
  root, and a Codex worker standing there receives none of the workspace's
  skills -- while the same worker one directory down inside a cloned
  repository receives them all, namespaced. The root's own orientation
  document tells the worker to invoke a skill it doesn't have. Rows 18 and
  19 of the capability contract record the gap as niwa's own unbuilt work,
  and both rows sit outside the bound-capability set, so nothing mechanical
  ties what the contract declares to what the code delivers.
goals: |
  A dispatched Codex worker invokes workspace skills at the instance root
  exactly as it would inside a repository, and niwa's own plugin reaches
  the root session too. Both capability rows end implemented with a named
  delivery bound to each declaration, failing a test in both drift
  directions. The guide's gap list drops both bullets by regeneration, the
  dispatch warning stays truthful about what's still missing, and the
  acceptance evidence is a real-binary scenario with a negative control,
  runnable at zero model cost.
upstream: docs/briefs/BRIEF-codex-instance-root-skills.md
motivating_context: |
  A previous attempt at dual-agent delivery shipped an accurate written
  diagnosis, 81 test functions, and green CI while its structural claim was
  never something a test could fail on. The capability contract exists so
  that can't happen again -- and these two rows are exactly the case it was
  built for: deliverability is measured, the debt is recorded as niwa's
  own, and the delivery is worthless unless every structural claim about it
  becomes a property a test can fail on.
---

# PRD: codex instance root skills

## Status

Accepted

This PRD owns the requirements for closing rows 18 (`RootProjectSkills`)
and 19 (`NiwaPlugin`) of the capability contract for Codex: skills at the
workspace instance root, honestly declared, mechanically bound, and proven
against the real binary. It pins the requirement-level property behind
each of its upstream brief's four open questions and leaves the four
mechanisms to the downstream design.

## Problem Statement

`niwa dispatch` launches every background worker at the root of a
workspace instance -- the directory that holds the cloned repositories,
not any one of them. A Claude Code session started there finds the
workspace's plugin skills in place. A Codex session doesn't: it resolves
zero workspace skills at the root, while the identical session one
directory down, inside any cloned repository, resolves all of them with
correct namespaces. The root even carries an orientation document whose
prose tells the worker to invoke a workspace skill -- an instruction a
dispatched Codex worker can't follow.

The capability contract records this honestly. Row 18,
`RootProjectSkills`, is declared unavailable for Codex with reason kind
`ReasonNotBuilt`: niwa writes no project layer at an instance root. Row
19, `NiwaPlugin` -- niwa's own plugin, which carries the config-migration
skill -- is declared unavailable the same way. Neither reason says Codex
can't receive the capability, because it can: skills are the one
project-layer capability measured to load from an untrusted layer
(docs/spikes/SPIKE-codex-discovery-mechanics.md). A real plugin tree at a
session's own working directory yields every skill, correctly namespaced,
with no trust entry and no project-root marker anywhere in the ancestry --
and the identical tree one directory above the session yields nothing.
Everything else a root session lacks lives in Codex's config document,
where every key is inert without a trust entry in the developer's own
Codex configuration. Whether niwa writes such an entry is a reserved
decision this work must not touch. Skills sit on the near side of that
line.

Left unbuilt, the gap does more than inconvenience a worker. The
dispatch-time warning, the guide's instance-root section, and the
acceptance feature file all describe the missing delivery in authored
prose that drifts as the facts change. And both rows sit outside the
bound-capability set, so nothing mechanical ties what the contract
declares to what the code actually delivers -- the precise failure mode
the contract was built to end.

## Goals

- A developer dispatches a background Codex worker and the worker can do
  what the orientation document tells it to do: the workspace's skills
  resolve at the instance root with the same namespaced names a
  repository-started session sees.
- niwa's own plugin reaches the root session, so a Codex worker can
  resolve the config-migration skill in the one place every dispatched
  worker starts.
- The honest accounting survives the fix. The guide's gap list drops both
  bullets on regeneration rather than by hand-editing, and the
  dispatch-time warning keeps telling the truth -- it names what a
  root-started Codex session genuinely still lacks instead of going
  silent.
- Every structural claim is a property a test can fail on. Both rows join
  the bound-capability set, a declaration that flips without its delivery
  or a delivery that vanishes without its declaration fails a test, and
  the acceptance evidence distinguishes "it loaded from where the session
  stands" from "the walk went somewhere else."
- Nothing changes for the teammate who opens the same instance with
  Claude Code, and nothing changes inside the repositories.

## User Stories

- As a developer handing work to a background Codex worker, I want the
  worker that boots at the instance root to resolve the workspace skill
  the orientation document names, with the namespace a repository-started
  session would see, so that the document's instruction stops being a dead
  end.
- As a developer whose MCP servers or sandbox posture don't reach a
  root-started Codex session, I want the dispatch-time warning and the
  guide's instance-root section to tell me exactly what's missing and why
  -- those capabilities live in Codex's config document, which niwa
  delivers per repository -- so that delivering skills doesn't silence the
  warning while the gap it describes is still real.
- As a maintainer auditing the contract months after the change, I want
  both rows to read implemented for Codex with their capabilities in the
  bound set and every implemented (capability, agent) pair among them
  bound to a named, registered delivery -- Claude's existing deliveries
  included -- so that removing a delivery or flipping a declaration
  without its counterpart fails a test instead of an audit.
- As a reviewer of the implementing pull request, I want the acceptance
  scenario to render a real session's resolved skills and show the root
  tree's skills present and a tree one directory above the session absent,
  without a credential or a single model turn, so that I'm reviewing
  evidence rather than a claim.

## Requirements

### Delivery

- **R1. Workspace skills resolve at the instance root for Codex.** A
  Codex session started at a prepared instance root resolves every
  workspace-declared plugin skill, namespaced `<plugin>:<skill>` exactly
  as a repository-started session resolves it, with no trust entry in the
  developer's Codex configuration and no project-root marker anywhere in
  the root's ancestry. The delivered content is the same plugin trees the
  per-repository delivery provides, delivered one directory higher.
  Discovery mechanics are consumed from the standing spike
  (docs/spikes/SPIKE-codex-discovery-mechanics.md), never re-derived.
- **R2. niwa's own plugin resolves at the instance root for Codex.** A
  Codex session started at the instance root resolves the
  config-migration skill from niwa's own plugin. The resolved name is
  settled by the design's manifest decision, and whatever name the design
  settles is asserted by the acceptance scenario so it can't drift
  silently afterward.
- **R3. Existing deliveries keep working unchanged.** The per-repository
  and worktree skills deliveries continue to work exactly as today.
  Nothing new is written inside any cloned repository, and the content a
  Claude Code session sees inside the instance is unchanged.
- **R4. The root delivery writes only inside the instance.** No write
  lands in the developer's own Codex configuration, in any cloned
  repository, or in any enclosing directory above the instance root --
  including the git metadata of any repository that happens to contain
  the instance. The delivery stays on the untrusted side of the trust
  line by construction, not by convention.
- **R5. Applying is idempotent and reconciled.** A second apply after a
  successful one adds nothing and changes nothing at the root. Removing a
  plugin from the workspace configuration removes its root tree on the
  next apply, matching the per-repository reconcile behavior.
- **R6. Name collisions are defined, never silent.** A workspace that
  configures a marketplace or plugin named `niwa` must not silently
  collide with niwa's own delivered tree -- neither at the location where
  niwa materializes its embedded plugin content nor at the delivered name
  in the root's skills tree. What happens on the collision is stated and
  tested; it is never left to whichever write lands last.
- **R7. The symlink-target position is written down.** How root-delivered
  symlink targets behave when marketplace content is replaced is recorded
  as a reasoned position in a durable document: replacement is path-stable
  (staging directory, then remove-and-rename at the same path), so links
  keep resolving across a swap -- and apply today does not repair an
  already-dangling link, which the position states plainly rather than
  assuming a repair mechanism that doesn't exist.

### Contract and binding

- **R8. Both rows flip in the change that delivers them.** Rows 18 and 19
  read implemented for Codex in the same change that lands their
  deliveries -- never before, never after. Once both flip, no Codex row
  carries `ReasonNotBuilt` at all; the tests pinning the column's
  membership and totals move in the same change, and the prose around
  them is rewritten rather than patched, because the "niwa's own debt"
  category they document becomes empty for Codex.
- **R9. Both capabilities join the bound set.** `RootProjectSkills` and
  `NiwaPlugin` enter the bound-capability set, which requires a named,
  registered delivery for every implemented (capability, agent) pair
  among them -- Claude's existing deliveries included. The binding tests
  fail in both drift directions: an implemented declaration with no
  registered delivery behind it, and a registered delivery for a pair the
  table doesn't declare implemented.
- **R10. Bindings don't overclaim.** A binding may name a delivery only
  if what that delivery writes is something the receiving agent actually
  registers. For Claude's row 19 this is live: on the machine that
  prepared this work, the installed tree is not in Claude Code's plugin
  format, its marketplace isn't in the registry, and the skill doesn't
  resolve in a session -- an observation scoped to that one machine. The
  work either corrects the installed tree's shape as part of giving Codex
  the same tree, or records the defect and states exactly what the
  binding claims. It never binds the row silently as though the delivery
  were sound. Making Claude's registration work is not in scope; refusing
  to paper over it is.
- **R11. The named deliveries are real.** The delivery registered behind
  each row is the code that performs the write: removing it breaks the
  delivery, not just a registry lookup. A type registered only to satisfy
  the binding test while the actual write happens elsewhere reproduces
  the dead-abstraction failure this contract exists to prevent.

### Structure

- **R12. The structural scans hold with no new exemptions.** No agent
  constant appears at the materializer call site, and `internal/workspace`
  names no agent and no agent context filename. Both properties are
  already enforced by AST scans over the source; this work adds no
  exemption entry and narrows no scan. The root delivery reaches its
  agent-specific directory name through the producing layer, never as a
  literal at the call site.

### The dispatch warning

- **R13. The warning is re-gated on the fact it describes.** The
  dispatch-time warning today is gated on row 18 and would go silent when
  the row flips, while the gap it describes is still real. After this
  work it fires for a dispatched agent exactly when that agent's
  config-document capabilities are repository-scoped -- the single fact
  behind everything still missing at the root -- and not on any
  capability row. Its text names what genuinely stays missing for such an
  agent: MCP servers, the session environment, and the approval and
  sandbox posture (the doc-budget override rides the same config
  document). It stops claiming skills among them, and it gains the
  session environment, which today's text omits. Dispatching an agent
  whose capabilities aren't repository-scoped prints no warning. The
  warning's pinning tests move with it in the same change.

### Documentation

- **R14. The gap list shrinks by regeneration only.** Both bullets
  disappear from the guide's generated gap list because the declarations
  changed and the generator was re-run -- never by hand-editing the
  generated block. The drift test between declarations and the committed
  guide section passes without any manual edit to the generated content.
- **R15. The authored prose is corrected, not deleted.** The prose sites
  the change makes stale are rewritten to state the new truth: the
  guide's instance-root section (its "gets nothing else" account, and the
  budget paragraph whose premise narrows -- after this change a `.codex/`
  directory exists at the root but carries no config document), the
  acceptance feature file's preamble that says the Codex project layer
  lands inside a repository and nowhere else, and the payload test's doc
  comment that still reasons from the disproven claim that Codex reads a
  project layer only from a project root downward.
- **R16. The capability matrix takes an amendment, not a rewrite.**
  docs/prds/PRD-agent-capability-contract.md is a point-in-time record
  with appended amendments; rows 18 and 19 take another amendment in that
  established style, below the table, naming the tests as the authority
  the matrix's totals are not. The matrix body stays untouched.
- **R17. New measurements reach the standing spike, never a fork.** The
  measurements this work produced -- the plugin-manifest namespacing rule,
  the root-layer positive and negative controls, and the
  copied-tree-equals-symlinked-tree result -- are contributed to
  docs/spikes/SPIKE-codex-discovery-mechanics.md. No competing spike
  document is created.

### Verification

- **R18. Acceptance is functional, with a negative control.** The
  acceptance evidence has two parts. An offline scenario asserts the
  delivered trees at the instance root mirror their sources and that the
  directory one level above the instance root holds no Codex skills tree;
  it runs in CI, which has no Codex binary. A live scenario renders a
  real Codex session's resolved skills against the real binary under an
  isolated Codex home: run at the instance root, the delivered plugins'
  skills appear with their expected names; run from a directory whose
  skills tree sits one level up, they don't. The negative control is what
  distinguishes "it loaded from where the session stands" from "the walk
  went somewhere else."
- **R19. The live check costs nothing.** The live scenario needs no
  credential and spends no model quota -- the render is a prompt
  construction, not a conversation -- and it skips only on a machine
  without the Codex binary, not on a machine without a login.

### Non-functional

- **N1. Standard toolchain only.** Tests use the Go standard library and
  introduce no new module dependencies. CI remains `gofmt -l .`,
  `go vet ./...`, and `go test -race ./...`.
- **N2. Failures are loud.** Where the contract declares these
  capabilities implemented, a delivery failure fails the apply with a
  clear error rather than degrading silently, matching the established
  Codex-side posture.

## Acceptance Criteria

Delivery, measured:

- [ ] From a prepared instance root -- no trust entry, no project-root
  marker in the ancestry -- rendering a Codex session's resolved skills
  against the real binary under an isolated Codex home lists every
  workspace-declared plugin skill, namespaced as a repository-started
  session would see it, and lists niwa's config-migration skill under the
  name the design settled.
- [ ] The identical check run from a directory whose skills tree sits one
  level above the session resolves none of that tree's skills.
- [ ] The live check runs with no credential file present and completes
  without a model turn; on a machine without the Codex binary it skips
  rather than fails.
- [ ] The offline scenario asserts the instance root holds exactly the
  expected Codex skills trees, each mirroring its source, and that the
  directory one level above holds zero -- and it passes in CI.
- [ ] Running apply twice in a row produces no change on the second run;
  removing a plugin from the workspace configuration removes its root
  tree on the next apply.
- [ ] With a workspace configuring a marketplace or plugin named `niwa`,
  the defined collision behavior is observed and asserted by a test;
  neither tree is silently overwritten.
- [ ] The per-repository and worktree scenarios that pass today pass
  unchanged, and apply writes no new file inside any cloned repository,
  above the instance root, or in the developer's own Codex
  configuration.
- [ ] With a root delivery target made unwritable, apply fails with a
  named error identifying the capability, rather than warning and
  continuing.

Contract and structure:

- [ ] Rows 18 and 19 read implemented for Codex, in the same change as
  their deliveries; no Codex row carries `ReasonNotBuilt`; the
  column-membership and totals tests are updated in that change.
- [ ] Both capabilities are in the bound set. Deleting either new
  delivery registration fails the binding test; reverting either
  declaration while its delivery remains registered also fails it.
- [ ] The change that binds row 19 for Claude carries a written
  disposition of the registration observation -- the installed tree's
  shape corrected, or the defect recorded with the binding's claim stated
  -- reviewable in the same change.
- [ ] Deleting the registered root-skills delivery code (not just its
  registration) breaks the delivery itself: the offline scenario fails.
- [ ] The structural scans pass with no exemption added and no scan
  narrowed; no agent constant appears at the materializer call sites the
  change touches; `internal/workspace` still names no agent and no agent
  context filename.

Warning and documentation:

- [ ] After both rows flip, dispatching a Codex worker still prints the
  root-gap warning; its text names MCP servers, the session environment,
  and the approval and sandbox posture, and does not name skills.
  Dispatching an agent whose config-document capabilities are not
  repository-scoped prints no warning.
- [ ] The guide's gap-list section is byte-identical to the generator's
  output, and neither row's bullet appears in it.
- [ ] The guide's instance-root section, the feature file's preamble, and
  the payload test's doc comment no longer state that a root-started
  Codex session gets nothing beyond orientation or that the project layer
  lands only inside repositories.
- [ ] docs/prds/PRD-agent-capability-contract.md carries a new appended
  amendment covering rows 18 and 19; its matrix body is unchanged.
- [ ] The symlink-target position (R7) is committed in a durable
  document, and the measurements named in R17 are contributed to the
  standing spike; no new spike document exists in the deliverables.

## Out of Scope

- Any trust entry for the instance root in the developer's own Codex
  configuration. Skills load untrusted; trust is what gates the
  config-document capabilities, and widening what niwa writes outside an
  instance is reserved to the author, not this chain.
- Widening the Codex payload layout to instance-root scope. MCP servers,
  the session environment, the approval and sandbox posture, and the doc
  budget stay repository-scoped; the layout table's own comment marks the
  widening as the same reserved decision.
- A where-from axis on the capability declaration schema. Rows are scoped
  by who receives a capability, never by where from.
- Shipping or relying on any trust-bypass flag.
- Changing the per-repository or worktree skills delivery, which works
  today and must keep working unchanged (R3).
- Making Claude Code's registration of niwa's plugin work. The observed
  defect is adjacent to this work; R10 requires it be handled honestly,
  not fixed.
- Repairing dangling symlinks at apply time. The position R7 records
  states the current behavior; building a repair mechanism is future
  work.

## Known Limitations

- The remaining root gap is real and stays. MCP servers, the session
  environment, the approval and sandbox posture, and the doc-budget
  override reach a Codex session only inside a repository, because all of
  them live in the trust-gated config document. This work delivers skills
  and corrects the record; it does not close that gap.
- The Claude-side row-19 observation -- installed tree unregistered, skill
  unresolvable -- is from one machine and is stated that way. R10 requires
  the work to dispose of it honestly, not to generalize it.
- If the design adds a Claude plugin manifest to the embedded tree, the
  namespaced skill name will read `niwa:niwa-migrate-config` -- the
  doubled `niwa` comes from the skill's own existing name and predates
  this work; renaming it is not in scope.
- An existing dangling root symlink for a still-configured plugin is not
  repaired by apply, only recorded (R7). Path-stable replacement makes
  the case rare, not impossible.

## Decisions and Trade-offs

Closing the brief's four open questions at requirements altitude; each
question's mechanism stays with the design and is listed in the next
section.

1. **Both rows end bound (R9), despite the codebase's tolerance for
   unbound implemented capabilities.** The tree today deliberately leaves
   some implemented capabilities out of the bound set and records that
   honestly in prose; row 5's per-repository skills are among them. The
   alternative -- deliver both rows, flip the declarations, and stay
   outside the bound set like their neighbors -- was rejected because
   this feature's whole point is that a previous attempt's structural
   claim was never something a test could fail on. Binding is the
   mechanism that makes the two rows' claims test-failable in both drift
   directions, and the upstream brief mandates it. The cost, accepted:
   joining the set pulls the Claude sides of both capabilities in,
   which is what forces R10's honesty clause.
2. **The manifest risk was re-measured and no longer blocks (feeds R2 and
   R10).** The brief's second open question warned that adding a Claude
   plugin manifest to niwa's embedded tree could silently rename a
   working command for existing Claude users. Measurement on the
   preparing machine found no working resolution to break: the installed
   tree isn't in Claude Code's plugin format, its marketplace isn't
   registered, and the skill doesn't resolve. The risk assessment in the
   brief is superseded -- there's no measured evidence of a functioning
   Claude-side resolution at stake, though the observation is one
   machine's. Whether the tree gains the manifest remains the design's
   call; what this PRD fixes is that the Claude side must be disposed of
   honestly either way (R10).
3. **The warning re-gates on the payload-scope fact, not on a row or a
   name (R13).** Everything still missing at the root after this change
   is gated by a single fact: the agent's config-document capabilities
   are repository-scoped. Gating the warning on row 18 goes silent while
   the gap is real; inventing a declaration row for "delivered in a
   repository, not at the root" is forbidden by the schema decision the
   contract already made. So the requirement pins the firing condition to
   the scope fact itself. The gate's mechanism -- an exported predicate
   over the layout versus reading the agent's name at the call site -- is
   the design's, with the evidence noting that a name check would
   reintroduce what the existing test design deliberately avoided.
4. **Collision behavior is a requirement; the materialization site is
   not (R6).** The brief asked where the embedded tree is materialized
   and whether that location can collide with a configured marketplace
   named `niwa`. The site is mechanism; the property that survives any
   site choice is that the collision case is defined and tested rather
   than silent. R6 pins the property, and the site stays with the design.
5. **The live acceptance check is required to be credential-free (R19).**
   Rendering a session's prompt input needs no login and spends nothing,
   measured against an empty Codex home. Reusing the existing live-test
   gate -- which copies a real credential into the sandbox because the
   quota-spending scenarios need one -- would make the scenario skip on
   exactly the machines that could run it. Recorded as assumed rather
   than confirmed: the gating shape is arguably design detail, but the
   zero-cost property is user-visible value the brief's reviewer journey
   names, so it's held as a requirement.

## Open Questions the Design Owns

These four are deliberately not answered here. Each has its evidence
gathered; none blocks the requirements above.

- **Row 18's binding mechanics.** Its route requires a named delivery to
  register a materializer, while every other plan-routed capability today
  binds by tagging plan entries and testing the tag. R9 and R11 fix the
  outcome -- bound, real -- and the design owns the shape.
- **Whether the embedded tree gains a Claude plugin manifest.** Measured:
  without one Codex resolves the skill unnamespaced; with one it
  namespaces. The design decides, and R2's name assertion plus R10's
  honesty clause hold whichever way it goes.
- **The warning gate's mechanism.** Exported predicate over the payload
  layout versus a direct agent check; R13 fixes the firing condition and
  the text.
- **Where the embedded tree is materialized inside the instance.** Any
  site satisfying R4, R5, and R6 is admissible; the design chooses and
  states the collision behavior R6 requires.

## References

- docs/briefs/BRIEF-codex-instance-root-skills.md -- the upstream framing
  this PRD's requirements are written from.
- docs/designs/current/DESIGN-agent-capability-contract.md -- the
  capability contract: its closed capability set, enforcement-test
  families, and the binding rule in both drift directions.
- docs/spikes/SPIKE-codex-discovery-mechanics.md -- the measured discovery
  mechanics; its finding that skills load from an untrusted layer is what
  makes root delivery possible without touching trust, and the
  destination for this work's new measurements (R17).
- docs/prds/PRD-agent-capability-contract.md -- the capability matrix,
  maintained as a point-in-time record with appended amendments; rows 18
  and 19 take another amendment in that style (R16).
- docs/guides/codex-agent.md -- the user guide whose generated gap list
  and authored instance-root section this work updates (R14, R15).
- internal/agentplan/capability.go, internal/workspace (delivery binding
  and its tests), internal/cli/dispatch.go,
  test/functional/features/codex-agent.feature -- the code and test
  surfaces the requirements above name.
