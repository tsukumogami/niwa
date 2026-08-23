---
schema: design/v1
status: Planned
upstream: docs/prds/PRD-codex-instance-root-skills.md
decision_provenance: inline-resolved
problem: |
  A Codex worker dispatched to a workspace instance root resolves none of
  the workspace's skills, while the identical session inside any cloned
  repository resolves them all -- and the root's own orientation document
  tells the worker to invoke a skill it doesn't have. Rows 18 and 19 of
  the capability contract record the gap as niwa's own unbuilt work, both
  outside the bound-capability set, so nothing mechanical ties the
  declarations to any delivery; and the dispatch warning describing the
  gap is gated on a row that goes silent the moment it flips.
decision: |
  Deliver the same plugin trees one directory higher through the existing
  plan machinery: a RootSkillsPlan producer method whose entries carry
  their own capability tag, driven by a registered RootSkillsMaterializer,
  with Claude's instance-root settings registration converted to a
  registered materializer so both row-18 pairs bind to real write paths.
  Row 19 binds as two runnable procedures once the shallow
  plugin-to-workspace import cycle is removed: Claude's calls
  plugin.Install; Codex's extracts the embedded tree to .niwa/plugin/niwa
  -- a site that cannot collide with configured marketplace content --
  and links it into the root skills directory. The embedded tree gains
  .claude-plugin/plugin.json so the delivered skill resolves as
  niwa:niwa-migrate-config, and the dispatch warning re-gates on an
  exported payload-scope predicate.
rationale: |
  Every binding names the type the pipeline actually drives, so both
  drift directions fail a test and the dead-abstraction failure the
  contract exists to prevent has nothing to hide behind. The sibling
  producer method is the tree's own precedent for one capability at two
  scopes, the runnable-procedure shape is the trust writer's, and the
  materialization site makes the collision case impossible by
  construction rather than handled by convention. The warning's new gate
  reads the same table cell that scopes everything still missing at the
  root, so it stays truthful across future flips; and the Claude row-19
  binding is stated over a recorded one-machine defect rather than
  papered over, which is the honest maximum inside this scope.
---

# DESIGN: codex instance root skills

## Status

Planned

This design owns the mechanism for closing rows 18 (`RootProjectSkills`)
and 19 (`NiwaPlugin`) of the capability contract for Codex: the delivery
shapes, the binding of both rows for both agents, the materialization
site and collision rules for niwa's embedded plugin tree, the dispatch
warning's new gate, and the verification split between placement and
discovery. The upstream PRD owns the requirements (R1-R19, N1-N2) and
is cited, not re-opened. Discovery mechanics are consumed from
docs/spikes/SPIKE-codex-discovery-mechanics.md, which this work's
measurements amend rather than fork.

## Upstream Design Reference

This design extends
docs/designs/current/DESIGN-agent-capability-contract.md, the capability
contract it binds two more rows into. The sections that govern here: its
Decision 1 (the plan model and the agent-blind executor this delivery
rides), Decision 2 (the declaration table, reason kinds, and routes; its
correction note is what re-classified row 18 to not-built), and Decision
3 (the three enforcement-test families, whose binding checks this work
extends to rows 18 and 19). Its requirements live in
docs/prds/PRD-agent-capability-contract.md; nothing here re-opens them.

## Context and Problem Statement

`niwa apply` already delivers workspace-declared plugin skills to Codex --
but only inside repositories. Step 6.2 of the pipeline resolves every
configured plugin to an installed tree once per apply
(`ResolvePluginTrees`, `internal/workspace/pluginskills.go`), then walks
the classified repositories and, for each agent, reconciles and delivers
the trees through the plan machinery: `Producer.SkillsPlan` declares one
`OpDeliverTree` entry per plugin, tagged `PluginSkills` (row 5), at the
layout `skillsLayouts` fixes for the agent (`.codex/skills/<plugin>` for
Codex), and `applyPlan` executes it. Nothing equivalent runs at the
instance root, so a Codex session started there -- which is where
`niwa dispatch` starts every background worker -- resolves zero workspace
skills while the identical session one directory down resolves all of
them, namespaced.

The capability contract records the gap honestly and passively. Row 18
(`RootProjectSkills`) and row 19 (`NiwaPlugin`) are declared
`StateUnavailable` with `ReasonNotBuilt` for Codex
(`internal/agentplan/declaration.go`), and neither capability is in
`boundCapabilities` (`internal/agentplan/binding.go`), so no test ties
the declarations to any delivery. The upstream PRD
(docs/prds/PRD-codex-instance-root-skills.md) requires both rows to flip
to implemented in the change that delivers them, both capabilities to
join the bound set with a named registered delivery behind every
implemented (capability, agent) pair -- Claude's existing deliveries
included -- and every structural claim to be a property a test can fail
on, in both drift directions.

That requirement lands in a specific mechanical landscape, and the
design problem is fitting it honestly:

- **The binding machinery asks route-shaped questions.** The catalog
  fixes a delivery `Route` per capability: `RootProjectSkills` is
  `RoutePlan`, `NiwaPlugin` is `RouteProcedure`
  (`internal/agentplan/capability.go`). `TestDeliveriesMatchTheBindings`
  (`internal/workspace/delivery_binding_test.go`) sends a plan-routed
  binding to the `deliveries` registry, which holds `Materializer`
  values, and a procedure-routed one to the `procedures` registry; the
  companion tests assert each registered value's `Name()` equals its
  delivery name. But the code that actually performs each of the four
  deliveries in question has none of those shapes today. Codex's root
  skills delivery does not exist. Claude's row-18 delivery is
  `InstallWorkspaceRootSettings` (`internal/workspace/workspace_context.go`),
  a plain function the pipeline calls directly. Claude's row-19 delivery
  is `plugin.Install`, which lives in `internal/plugin` -- a package that
  imports `internal/workspace`, so the workspace-side registry cannot
  name it without an import cycle; the pipeline reaches it through the
  `Applier.InstallNiwaPlugin` function-field seam. And Codex's row-19
  delivery must materialize a tree that exists only as an `embed.FS`
  inside that same cyclic package.
- **The layout scan constrains where names may appear.** No non-test
  file in `internal/workspace` may name an agent constant or an agent
  context filename, and the root delivery must reach `.codex/skills`
  through the producer's own `skillsDir` helper, never as a literal at a
  call site. The scan's last exemption window was closed deliberately;
  this work adds none.
- **The dispatch warning is gated on the wrong fact.** The warning in
  `internal/cli/dispatch.go` fires when row 18 is not implemented for
  the dispatched agent. When the row flips, the warning goes silent --
  but what it describes is still real, because MCP servers, the session
  environment, the approval and sandbox posture, and the doc budget all
  live in the trust-gated config document that
  `payloadLayouts[agent.AgentCodex]` scopes to repositories
  (`internal/agentplan/payload.go`). The contract deliberately has no
  where-from axis, so no declaration row exists for "delivered in a
  repository, not at the root," and nothing today exports the
  payload-scope fact the warning actually needs
  (`Producer.payloadLayout` is unexported).
- **The acceptance bar is a discovery claim, not a placement claim.**
  Whether a delivered tree loads from where a session stands can only be
  proven by the real binary; an offline tree assertion proves only that
  niwa wrote (or did not write) files. Two earlier effort on this
  initiative got that distinction wrong in opposite directions, so the
  design must say which scenario proves which claim.
- **The Claude side of row 19 appears defective where it was inspected.**
  On the machine that prepared this work, the tree `plugin.Install`
  writes is not in Claude Code's plugin format (no `.claude-plugin/`
  directory, unlike every other marketplace beside it), the `niwa`
  marketplace is absent from Claude Code's marketplace registry, and no
  `niwa:*` skill resolves in a live session. The observation is one
  machine's. Binding the row for Claude without disposing of it would
  reproduce the exact drift the binding rule exists to catch, and the
  PRD forbids that (R10) while also placing "make Claude's registration
  work" out of scope.

Discovery mechanics are consumed from the standing spike
(docs/spikes/SPIKE-codex-discovery-mechanics.md) and never re-derived
here. The load-bearing findings: a session's own working directory is
always the last directory of its discovery walk, marker or no marker
(finding 1); skills are the one project-layer surface measured to load
from an untrusted layer (finding 5); and -- measured for this work and
amended into those findings -- a real plugin tree at a marker-less
session directory yields every skill namespaced while an identical tree
one directory above yields none, a copied tree resolves identically to a
symlinked one, and the `<plugin>:<skill>` namespace comes from a
`.claude-plugin/plugin.json` at the tree root, which niwa's embedded
tree does not ship today.

## Decision Drivers

- **Bound and real, in both drift directions.** Both rows join
  `boundCapabilities`; every implemented (capability, agent) pair among
  them names a registered delivery; and the registered delivery is the
  code that performs the write -- removing it must break the delivery,
  not just a registry lookup (PRD R9, R11). A type registered only to
  satisfy a map lookup reproduces the dead-abstraction failure this
  contract exists to prevent.
- **No overclaiming.** A binding may name a delivery only if what it
  writes is something the receiving agent actually registers, and the
  Claude row-19 observation must be disposed of in writing -- corrected
  or recorded -- never papered over (R10).
- **The structural scans hold with no new exemptions.** No agent
  constant at the materializer call sites, no agent name or agent
  context filename in `internal/workspace`, and the agent-specific
  directory name reached only through the producing layer (R12).
- **The trust line is untouched by construction.** Every write lands
  inside the instance: nothing in the developer's own Codex
  configuration, nothing in any cloned repository, nothing above the
  instance root (R4). Skills sit on the untrusted side of the line the
  spike measured; the config-document capabilities stay behind it.
- **Idempotent, reconciled, collision-defined.** A second apply changes
  nothing; a removed plugin's root tree is removed on the next apply
  (R5); and a workspace that configures a marketplace or plugin named
  `niwa` gets stated, tested behavior rather than whichever write lands
  last (R6).
- **Truthful surfaces that stay truthful.** The dispatch warning
  re-gates on the payload-scope fact it describes and keeps firing after
  the rows flip (R13); the guide's gap list shrinks by regeneration
  only (R14); the authored prose sites are corrected to the new truth
  rather than deleted (R15); the capability matrix takes an appended
  amendment (R16); and the new measurements land in the standing spike,
  not a fork (R17).
- **Acceptance at zero model cost, with the negative control carrying
  the discovery claim.** The live scenario renders a real session's
  resolved skills without a credential or a model turn and skips only
  where the binary is absent; the offline scenario gates CI on placement
  and reconciliation (R18, R19).
- **House precedents govern shape.** One capability at two scopes is a
  sibling producer method, not a scope field (`RootContextPlan` beside
  `RepoContextPlan` in `internal/agentplan/context.go`); a
  procedure-routed delivery is a registered struct whose `Deliver` calls
  the package function that does the work (`codexTrustProcedure`); a
  plan-routed binding registers the materializer type as the identity of
  the delivery the pipeline actually drives; and code behind an import
  cycle is reached through a function-field seam only when the cycle
  cannot reasonably be removed.
- **Standard toolchain, loud failures.** Go standard library only, no
  new module dependencies (N1); where the contract declares a capability
  implemented, a delivery failure fails the apply with a named error
  (N2).

## Considered Options

### Decision 1 -- row 18 binds through registered materializers the pipeline drives, with root plan entries tagged as their own capability (R1, R3, R5, R9, R11, R12)

Row 18 is `RoutePlan`, so `TestDeliveriesMatchTheBindings` sends its
bindings to the `deliveries` registry: each must name a `Delivery`
registered as a `Materializer` whose `Name()` matches. That constraint
has to be satisfied twice, because binding the capability pulls in both
implemented pairs -- and the two agents' row-18 deliveries are different
code. For Codex the delivery doesn't exist yet. For Claude it is
`InstallWorkspaceRootSettings`, the instance-root settings registration
(`enabledPlugins` plus `extraKnownMarketplaces`), a plain function the
pipeline calls at `apply.go`.

Key assumptions:

- The registry's stated contract holds: registered values are the
  identity of a delivery, and the pipeline builds its own instances with
  call-site options (the `EnvMaterializer` / `FilesMaterializer`
  pattern). What R11 adds is that the registered *type* must be the code
  that performs the write, so the pipeline must reach the write through
  that type.
- The declaration flips and the deliveries land in one change. Verified
  safe in either order mechanically -- a producer method gated on
  `p.delivers(RootProjectSkills)` yields an empty plan while the row is
  unavailable, and no global test forces an implemented plan-routed
  capability to yield entries -- so the one-change rule is the
  declaration table's own stated convention, not a build-order
  necessity.

#### Chosen: two named materializers, and a sibling root producer method

**Codex.** `internal/agentplan` gains `Producer.RootSkillsPlan`, a
sibling of `SkillsPlan` following the established
one-capability-per-scope precedent (`RootContextPlan` beside
`RepoContextPlan` in `context.go`): same `skillsDir` path helper, same
`OpDeliverTree`/`IfSourceExists` entry shape, gated on
`p.delivers(RootProjectSkills)`, and tagging every entry
`Capability: RootProjectSkills`. The tag is not decoration -- it is what
lets the plan-shape suite tell row 18's delivery from row 5's, and it
keeps the existing guard meaningful: no producer may emit an entry
tagged with a capability not declared implemented for its agent.
`internal/workspace` gains `RootSkillsMaterializer`, whose
`Materialize` runs the root delivery -- reconcile from the producer's
root spec, produce the plan, execute through `applyPlan` -- and the
pipeline drives it at the instance-root step with the plugin trees
already resolved by step 6.2. It registers in `deliveries` under a new
constant, `agentplan.DeliveryRootSkills` (`"root-skills"`). Deleting the
type breaks the delivery, because the pipeline's only path to the root
write is through it; deleting only the registration fails the binding
test. Both drift directions fail loudly, which is R9's demand.

**Claude.** `InstallWorkspaceRootSettings` converts to
`RootSettingsMaterializer` (`Name() == "root-settings"`, registered
under `agentplan.DeliveryRootSettings`), carrying its non-context inputs
as struct fields the way `EnvMaterializer` and `FilesMaterializer`
already do. Its call site drives the type's method, so the same
deletion property holds. The conversion changes no behavior: the same
document, the same path, the same bytes.

The root delivery reaches `.codex/skills` only through the producer's
`skillsDir`; no agent constant and no agent filename appears in
`internal/workspace`, so both halves of the layout scan pass with no new
exemption (R12).

#### Alternatives considered

- **Bind by plan-entry tagging alone, without joining the bound set.**
  This is how every other plan-routed capability is checked today --
  `RepoOrientationDoc`, `MCPServers`, `ApprovalPosture` tag their
  entries and per-producer tests assert the tag -- and row 5 is not in
  `boundCapabilities` at all; `delivery_binding.go` says in prose that
  the absence "is what records that honestly." So this option is the
  codebase-consistent one, and it was seriously weighed. It fails the
  requirement, not the aesthetics: R9 requires a *named, registered*
  delivery for every implemented pair among the bound capabilities, and
  tagging names nothing. It also leaves the second drift direction
  untested -- a registered delivery for a pair the table doesn't declare
  is inexpressible when there is no registration. The tag survives in
  the chosen option as plan-shape coverage; it just isn't the binding.
- **Identity-only registration.** Register types under the two new
  names while the pipeline keeps calling `InstallRepoSkills`-style
  plain functions. Every binding test passes, and R11 is violated
  precisely: removing the registered type breaks a map lookup while the
  write continues elsewhere. This is the dead-abstraction failure the
  contract was built against, listed because it is the cheapest option
  and the one a hurried implementation would drift into.
- **Extend the binding test with a plan-fixture mechanism.** Teach
  `TestDeliveriesMatchTheBindings` a second answer for plan-routed
  bound capabilities: assert that a canonical fixture yields at least
  one tagged entry instead of requiring a registered materializer. This
  is closer to the enforcement-test family the capability contract's
  design originally described for `RoutePlan`, and it would spare the
  Claude-side conversion. Rejected because it splits the binding
  contract into two mechanisms inside one test, weakens "named delivery"
  into "some entry exists," and buys nothing the tag doesn't already
  provide -- while the registry mechanism is what the tree actually
  implements and what row 18's neighbors (`DotenvFiles`,
  `FileDistribution`, `Hooks`) already use.

### Decision 2 -- row 19's Codex delivery is a runnable procedure over a niwa-owned tree outside the marketplace directory, and the import cycle is removed rather than bridged (R2, R4, R5, R6, R7, R11)

Row 19 is `RouteProcedure`, so its bindings must register in the
`procedures` map -- for both agents. The precedent is
`codexTrustProcedure`: the registered value is runnable, and `Deliver`
calls the package function that does the work. Two obstacles stand in
the way. `internal/plugin` imports `internal/workspace` (for `Reporter`,
`EmitPluginNotice`, and an `InstanceState` parameter that is always nil
at its call sites), so the workspace-side registry cannot reach
`plugin.Install` today; the pipeline goes through the
`Applier.InstallNiwaPlugin` function-field seam instead. And the
embedded tree is an `embed.FS` -- a symlink cannot name it, so a Codex
delivery must first put it on disk somewhere inside the instance.

Key assumptions:

- Measured: a copied plugin tree resolves exactly as a symlinked one,
  and niwa's `.niwa-delivered-tree` sentinel inside a copied tree
  neither breaks discovery nor surfaces as a skill. The delivery shape
  is therefore free, and the choice is made on reconcile grounds.
- The two agents' row-19 deliveries have genuinely different
  lifecycles: Claude's is global, once per developer machine, untouched
  by instance destruction; Codex's is per-instance and reconciled by
  apply. One procedure per pair, not one procedure stretched over both.

#### Chosen: break the cycle; extract to `.niwa/plugin/niwa`; deliver through a tagged plan entry

**The cycle is removed.** `internal/plugin` stops importing
`internal/workspace`: `Install` drops its always-nil state parameter,
takes the developer home as data (the `EnsureCodexTrust` posture --
callers that aren't wired to a real home can't reach one by accident),
and returns its `Action` for the caller to report; notice emission moves
to the caller. The package also exports the embedded-tree
materialization (`plugin.MaterializeTo(dst)`, the existing
`writeEmbeddedTree` behind a name). `internal/workspace` then imports
`internal/plugin`, the CLI adapter and the function-field seam are
deleted, and both row-19 procedures are runnable registered values like
the trust procedure -- not identities for code that lives elsewhere.

**(NiwaPlugin, Claude)** binds `claudeNiwaPluginProcedure`
(`"niwa-plugin-claude"`), whose `Deliver` calls `plugin.Install` with
the input's developer home. Its claim is stated in Decision 4.

**(NiwaPlugin, Codex)** binds `codexNiwaPluginProcedure`
(`"niwa-plugin-codex"`). Its `Deliver` does two things. First it
materializes the embedded tree at
`<instanceRoot>/.niwa/plugin/niwa` -- idempotent by the same
manifest-version comparison `plugin.Install` uses, replaced path-stably
(stage beside, remove-and-rename at the same path) exactly as
`ensureMarketplaceContent` replaces fetched marketplace content. Then it
delivers the tree into the root skills directory through the plan
machinery: a new `Producer.NiwaPluginPlan` declares one
`OpDeliverTree` entry tagged `Capability: NiwaPlugin`, path built by the
same `skillsDir` helper, executed by `applyPlan`. To make that possible,
`procedureInput` gains two fields every procedure receives and the trust
procedure ignores: the instance root, and the agent's gated `Producer`.
The write is real end to end -- deleting the procedure removes both the
source tree and the link, and the skill stops resolving.

**The materialization site is deliberately not under
`.niwa/marketplaces/`.** That directory's names are claimed by
*configured* marketplaces, fetched-once with an early return when a
manifest is already present. Extracting niwa's own tree there under the
name `niwa` would mean a workspace that configures a marketplace named
`niwa` either finds niwa's embedded content masquerading as its
marketplace or silently overwrites it -- the exact silent collision R6
forbids. At `.niwa/plugin/niwa` the site collision is impossible by
construction, and a test configures a marketplace named `niwa` and
asserts both trees exist independently.

**The delivered-name collision is defined in niwa's favor.** At the
root skills directory, one name -- the niwa tree's delivered name, an
`agentplan` constant -- can be claimed twice: by row 19's tree and by a
workspace-configured plugin that happens to be named `niwa`. When
`NiwaPlugin` is implemented for the agent, `RootSkillsPlan` skips the
configured plugin at the root and appends a warning naming both sources;
the root reconcile's `Keep` set, produced by the leaf, includes the niwa
tree name so neither the skip nor a reconcile pass removes it. Inside
repositories nothing changes: row 19 is a root delivery, so a configured
plugin named `niwa` still delivers per-repository exactly as today (R3).
The behavior is stated, asserted by a test, and never left to write
order.

**The symlink-target position (R7), recorded here as the durable
statement.** Root-delivered symlinks stay valid across content
replacement because every replacement in the delivery path is
path-stable: fetched marketplace content is staged and renamed at the
same path, and the niwa tree's extraction follows the same discipline. A
link resolves by path, so it keeps working across a swap with no repair
step. What apply does *not* do -- today, and unchanged by this design --
is repair an already-dangling link for a still-configured plugin: the
entry's `IfSourceExists` precondition skips it, and
`reconcileSkillsDir` removes only names that left the configured set,
never resolving targets. Path-stable replacement makes the dangling case
rare, not impossible; building a repair mechanism is future work the
upstream PRD places out of scope.

#### Alternatives considered

- **Extract under `.niwa/marketplaces/niwa`.** The natural-looking
  reuse of the established directory, rejected for the silent collision
  described above. It would also entangle two lifecycles: marketplace
  content is fetched-once and refreshed only by human deletion, while
  the embedded tree must track the binary's version on every apply.
- **Resolve the Codex delivery from the Claude-side global install**
  (`~/.claude/plugins/marketplaces/niwa/`). No extraction step, but it
  couples the two agents' deliveries: a machine where the Claude-side
  install was skipped or failed leaves Codex with a dangling source and
  no way to self-heal. The standing spike records precisely this failure
  shape for github-sourced marketplaces (finding 8's note), and the
  per-repository delivery already moved off it.
- **Write the embedded tree directly into `.codex/skills/niwa` as a
  real directory, no niwa-owned source.** One write instead of two, and
  the copy is known to resolve. Rejected because it abandons the
  source-plus-link shape everything else uses: reconcile would depend
  entirely on the sentinel file, version refresh would need its own
  in-place update logic, and R7's position would need a second story for
  the one tree delivered differently.
- **Keep the function-field seam and register identity-only
  procedures.** Zero-value registrations can't run, so the pipeline
  would keep its own path to the writes while the registry holds
  stand-ins -- R11's named failure. Bridging the cycle with function
  fields threaded through `procedureInput` was also weighed and rejected
  as code-carrying data: the cycle is three symbols deep and cheaper to
  remove than to bridge.

### Decision 3 -- the dispatch warning gates on an exported payload-scope predicate (R13)

The warning in `internal/cli/dispatch.go` fires today when row 18 isn't
implemented for the dispatched agent, and its own comment concedes the
gate is a stand-in: "there is no declaration for 'delivered in a
repository, not at the root', so row 18 is the closest thing the table
has to one." When row 18 flips, the warning goes silent while everything
it actually describes -- MCP servers, the session environment, the
approval and sandbox posture, the doc budget -- is still
repository-scoped, because all of it rides the config document that
`payloadLayouts[agent.AgentCodex]` scopes to `PayloadInRepo`. Nothing
exports that fact; `Producer.payloadLayout` is unexported.

#### Chosen: export the scope fact from `internal/agentplan` and gate on it

A new exported predicate over the payload layout table -- shaped like
`agentplan.ConfigDocRepoScoped(ag agent.Agent) bool`, the exact name
settled at implementation -- returns true when the agent has a payload
layout whose scope is `PayloadInRepo`, and false otherwise. The dispatch
path gates the warning on the predicate, and the text is rewritten to
name what genuinely stays missing for such an agent: MCP servers, the
session environment, and the approval and sandbox posture. It stops
claiming skills among them, and it gains the session environment, which
today's text omits. For Claude the layout's scope is
`PayloadAtInstanceRoot`, so the predicate is false and no warning
prints; an agent with no payload layout at all gets false too -- there
is no repository-scoped config document for it to be missing. The two
pinning tests (`dispatch_agentwarning_test.go`,
`dispatch_contract_test.go`) move with the gate in the same change, and
the contract test asserts the gate reads the predicate rather than a
row or a name.

#### Alternatives considered

- **Read the agent's name at the call site.** One line, no new export
  -- and it reintroduces exactly the pattern the dispatch contract
  tests were built to remove (`TestDispatchGateFollowsTheDeclaration`
  exists so dispatch consults declarations, not agent identity). It
  also hardcodes today's truth: a third agent whose config document is
  repository-scoped would dispatch silently.
- **Add a declaration row for the root gap.** Forbidden by the schema
  decision the contract already made: rows are scoped by who receives a
  capability, never by where from. A "delivered in a repository, not at
  the root" row is the where-from axis by another name.
- **Keep the row-18 gate and reword the text.** The gate's referent
  flips from "skills are missing" to nothing at all the moment the row
  is implemented; any wording over a gate that goes silent is a warning
  that stops firing while the gap it described persists.

### Decision 4 -- the embedded tree gains `.claude-plugin/plugin.json`, and the Claude row-19 binding states its claim over a recorded defect (R2, R10, R16)

Measured, one variable: niwa's tree delivered at the root exactly as
shipped resolves its skill bare, as `niwa-migrate-config`; the same tree
plus a `.claude-plugin/plugin.json` resolves it namespaced, as
`niwa:niwa-migrate-config` -- the same `<plugin>:<skill>` shape every
other delivered plugin produces. The upstream brief had held this
change back on one risk: that adding the manifest could silently rename
a working `/niwa:migrate-config` command for existing Claude users.

Key assumptions:

- On the machine that prepared this work there is no working resolution
  to break: the installed tree has no `.claude-plugin/` directory while
  every other marketplace beside it does, Claude Code's marketplace
  registry does not list `niwa`, and no `niwa:*` skill resolves in a
  live session. **This is one machine's observation and is treated as
  such** -- it dissolves the specific rename risk on the evidence
  available, and it is recorded, not generalized to every install.
- Adding the file is mechanically inert on the install path:
  `Embedded()` reads only `manifest.json`, the idempotency check
  compares only `manifest.json`, `stageAndRename` copies the whole tree
  with no expected file list, and no test in `internal/plugin` pins the
  tree's file set.

#### Chosen: add the manifest; bind Claude's row with its claim stated in writing

The embedded tree at `internal/plugin/files/niwa/` gains
`.claude-plugin/plugin.json` naming the plugin `niwa`. The Codex root
delivery then namespaces like every other plugin, and the acceptance
scenario asserts the resolved name `niwa:niwa-migrate-config` so it
cannot drift silently afterward. The doubled `niwa` comes from the
skill's own existing frontmatter name; renaming it is out of scope.

The Claude side is disposed of per R10, in three parts. The tree's
format defect is partially corrected as a side effect: the installed
tree becomes a Claude-plugin-formatted tree, since `plugin.Install`
ships whatever the embed carries. The registration defect is recorded:
niwa writes the tree but never registers the marketplace with Claude
Code, and on the one machine inspected the marketplace is absent from
the registry and the skill unresolvable in a session. And the
`(NiwaPlugin, Claude)` binding's claim is stated exactly, in the
binding's own comment and in the capability matrix's appended amendment:
the delivery materializes niwa's plugin tree at the user-level install
path in Claude Code's plugin format; it does not claim a Claude session
resolves it. Making the registration work is explicitly out of scope
and stays a recorded defect adjacent to this work.

#### Alternatives considered

- **Ship the tree unchanged.** The Codex skill resolves bare, breaking
  the uniform `<plugin>:<skill>` naming the rest of the delivery
  produces and leaving R2's asserted name as the odd one out -- and the
  Claude-side format defect stays wholly uncorrected even though the
  work touches the exact tree that carries it.
- **Fix Claude Code's registration of the marketplace.** Out of scope
  by the PRD, and rightly: the registry's semantics are another
  product's, unmeasured here, and the change would widen a
  skills-delivery feature into speculative work against an interface
  with no recorded contract.
- **Refuse to bind Claude's row 19.** Honest about the defect but
  non-compliant with R9, which requires every implemented pair among
  the bound capabilities to name a delivery; the escape would be
  flipping Claude's row to unavailable, which is false -- the install
  demonstrably writes the tree. Stating the binding's claim over a
  recorded defect is the shape R10 prescribes for exactly this case.

### Decision 5 -- verification: the offline scenario carries placement, the live scenario carries discovery, and the live gate is credential-free (R18, R19)

Two scenarios prove two different claims, and the design says which is
which because collapsing them is how two earlier efforts on this
initiative got the discovery claim wrong in opposite directions.

#### Chosen: a placement scenario for CI, a discovery scenario against the real binary, separately gated

**Offline (CI-gating, placement and reconciliation).** A functional
scenario mirrors the existing per-repository skills scenario at the
instance root, using the step vocabulary that already resolves
locations against the harness workspace: the instance root holds
exactly the expected Codex skills trees, each mirroring its source; the
directory one level above the instance root holds zero; a second apply
changes nothing; removing a plugin from the configuration removes its
root tree. This proves what niwa wrote and where -- it cannot prove what
a session loads, because no session runs.

**Live (acceptance-carrying, discovery).** `codex debug prompt-input`
renders the `<skills_instructions>` block a real session resolved,
naming each skill and its source file, without a credential and without
a model turn -- measured against a completely empty `CODEX_HOME`. Run at
the instance root under an isolated Codex home, the delivered plugins'
skills appear with their expected namespaced names, `niwa`'s included;
run from a directory whose skills tree sits one level up, that tree's
skills do not appear. The negative control is a *real tree that fails
to load*, which is what distinguishes "it loaded from where the session
stands" from "the walk went somewhere else."

**The live gate is new and lighter than the existing one.** The
existing live-test gate copies the developer's real credential into the
sandbox home because the quota-spending scenarios need one; reusing it
would make this scenario skip on exactly the machines that could run
it. The new scenario gets its own tag and a gate that checks only that
the binary is on PATH: it skips on a machine without Codex, never on a
machine without a login, and it spends nothing. CI has no Codex binary,
so the live scenario skips there and the offline one carries CI's
gating.

#### Alternatives considered

- **Reuse the existing credential-gated live tag.** Rejected: it
  conflates "can spend quota" with "can render a prompt," and inverts
  R19 -- the scenario would skip for a missing credential it doesn't
  need.
- **Let the offline scenario carry the acceptance bar alone.** Its
  negative control -- no tree above the root -- proves niwa didn't write
  one there, not that a session ignores one. A placement assertion
  cannot fail when discovery breaks, so it cannot carry a discovery
  claim.

## Decision Outcome

Row 18 lands as a sibling scope of the delivery that already works one
directory down: `RootSkillsPlan` declares the instance root's tree
entries tagged with their own capability, a registered
`RootSkillsMaterializer` drives them through `applyPlan`, and Claude's
existing root settings registration converts to a registered
`RootSettingsMaterializer` -- so both implemented pairs of row 18 bind
to the code that performs their writes. Row 19 lands as two runnable
procedures: Claude's calls `plugin.Install` directly once the shallow
import cycle is removed, and Codex's materializes the embedded tree at
`.niwa/plugin/niwa` -- a site that cannot collide with configured
marketplace content -- then links it into the root skills directory
through a plan entry tagged `NiwaPlugin`. The embedded tree gains the
Claude plugin manifest, so the delivered skill resolves as
`niwa:niwa-migrate-config`, and the Claude-side binding states in
writing that it claims an installed tree, not a resolving session, over
the recorded one-machine registration defect. The dispatch warning
re-gates on an exported payload-scope predicate and keeps firing --
truthfully, naming MCP servers, the session environment, and the
approval and sandbox posture -- after both rows flip. Verification
splits by claim: an offline scenario gates CI on placement and
reconciliation, and a credential-free live scenario against the real
binary carries the acceptance bar on discovery, with a real
one-directory-up tree as its negative control.

The pieces reinforce each other. The bound set's two new rows are only
as honest as their deliveries are real, and every delivery here is the
pipeline's actual write path; the collision rules are only as good as
the name constants both sides share, and those live in the leaf; the
warning is only as durable as its gate's referent, and the referent is
now the same table cell that scopes the remaining gap.

## Solution Architecture

### Overview

One apply, two new delivery moments. At the instance-root step, after
step 6.2 has resolved the configured plugins once, the pipeline drives
the root skills materializer per agent (row 18) and, on the procedure
pass, the niwa-plugin procedures per agent (row 19). Everything an agent
reads at the root afterwards sits under the root's `.codex/skills/`
directory (for the one agent that takes delivered trees today): one
symlink per configured plugin into the instance's own marketplace
content or clones, plus one symlink named `niwa` into the extracted
embedded tree. Nothing is written into any repository, into the
developer's own Codex configuration, or above the instance root.

Delivered layout, per instance:

```
<instanceRoot>/
  .niwa/
    marketplaces/<name>/      fetched marketplace content (existing)
    plugin/niwa/              extracted embedded tree (new; version-
                              checked, path-stably replaced)
  .codex/
    skills/
      <plugin> -> resolved plugin tree        (row 18, per configured plugin)
      niwa     -> ../../.niwa/plugin/niwa     (row 19)
  <group>/<repo>/.codex/skills/<plugin>       (row 5, unchanged)
```

### Components

**`internal/agentplan` (the leaf decides names and shapes):**

- `Producer.RootSkillsPlan(in RootSkillsInputs) (*Plan, error)` --
  sibling of `SkillsPlan`; entries tagged `RootProjectSkills`; skips a
  configured plugin whose name collides with the niwa tree name when
  `NiwaPlugin` is implemented for the agent, recording the refusal in
  `Plan.Warnings`.
- `Producer.RootSkillsReconcileSpec(in RootSkillsInputs)
  SkillsReconcileSpec` -- `Keep` is the deliverable configured names
  plus the niwa tree name when `NiwaPlugin` is implemented, so the
  reconcile pass never removes row 19's delivery.
- `Producer.NiwaPluginPlan(in NiwaPluginInputs) (*Plan, error)` -- one
  `OpDeliverTree` entry tagged `NiwaPlugin`, path from `skillsDir`.
- `NiwaPluginTreeName` -- the delivered name (`"niwa"`), the constant
  both the collision rule and the reconcile `Keep` set read.
- The payload-scope predicate of Decision 3, exported beside the layout
  table it reads.
- `Delivery` constants: `DeliveryRootSkills` (`"root-skills"`),
  `DeliveryRootSettings` (`"root-settings"`),
  `DeliveryNiwaPluginClaude` (`"niwa-plugin-claude"`),
  `DeliveryNiwaPluginCodex` (`"niwa-plugin-codex"`). `boundCapabilities`
  gains `RootProjectSkills` and `NiwaPlugin`; `bindings` gains the four
  rows pairing them.
- Declaration flips: rows 18 and 19 to `StateImplemented` for Codex, in
  the same change as the deliveries. After the flip no Codex row carries
  `ReasonNotBuilt`, so the column tests' "niwa's own debt" category is
  rewritten away rather than patched (see Reacting surfaces).

**`internal/workspace` (the executor side does the doing):**

- `RootSkillsMaterializer` -- registered under `DeliveryRootSkills`;
  `Materialize` reconciles from the root spec, produces `RootSkillsPlan`,
  executes via `applyPlan`. Driven at the instance-root step for each
  agent, gated by the workspace-level `AgentEnabled` lookup with the
  empty repository name -- the same gate the sibling payload call
  already uses there. Its exclusions ride the instance-root mechanism
  (`EnsureInstanceGitignore` / `InstanceExcludePatterns`), never
  `EnsureRepoExclude`, which searches upward and could touch an
  enclosing repository's exclude file.
- `RootSettingsMaterializer` -- the converted
  `InstallWorkspaceRootSettings`, registered under
  `DeliveryRootSettings`, driven at its existing call site.
- `claudeNiwaPluginProcedure` / `codexNiwaPluginProcedure` -- registered
  in `procedures` under their delivery names; reached through
  `procedureFor(NiwaPlugin, ag)` on a deliver pass shaped like
  `deliverDirectoryTrust`. `procedureInput` gains `InstanceRoot` and
  `Producer`; the trust procedure ignores both.

**`internal/plugin` (becomes a true leaf):**

- Loses its `internal/workspace` import: `Install` drops the always-nil
  state parameter, takes the developer home as data, returns its
  `Action` for the caller to report; notice emission moves to the
  caller. Exports `MaterializeTo(dst)` for the embedded tree. The
  embedded tree gains `.claude-plugin/plugin.json`.
- `internal/cli/plugin_adapter.go` and the `Applier.InstallNiwaPlugin`
  function-field seam are deleted; the pipeline's rank-2 branches call
  through `procedureFor` instead.

**`internal/cli`:**

- The dispatch warning gates on the exported predicate and carries the
  rewritten text (Decision 3).

### Data flow, per apply

```
ResolvePluginTrees (once)
        │
        ├─ per repository, per agent: InstallRepoSkills  (row 5, unchanged)
        │
        └─ instance root, per agent (workspace-level gate):
             RootSkillsMaterializer.Materialize
               reconcile(RootSkillsReconcileSpec)   Keep ⊇ {configured...} ∪ {niwa}
               RootSkillsPlan ── entries tagged RootProjectSkills ──> applyPlan

procedure pass, per agent: procedureFor(NiwaPlugin, ag)
  claude: Deliver -> plugin.Install(home)            (global tree, ~/.claude/...)
  codex:  Deliver -> plugin.MaterializeTo(.niwa/plugin/niwa)   [version-checked]
                     NiwaPluginPlan ── entry tagged NiwaPlugin ──> applyPlan
```

Failures are loud where the contract declares the capability
implemented: a root delivery target that cannot be written fails the
apply with an error naming the capability, matching the established
Codex-side posture (N2), rather than degrading to a warning.

### Reacting surfaces, enumerated

The change lands against a set of tests and documents that react
mechanically; the design names them so the implementation moves them
deliberately rather than discovering them red.

- `internal/agentplan/capability_test.go`: `TestCodexColumnTotals`
  (13/11 becomes 15/9); `codexDelivered` gains both rows and
  `codexFinalGaps` loses `NiwaPlugin`;
  `TestLookupAnswersEachDeclaredPair`'s literal
  `{NiwaPlugin, Codex, StateUnavailable, ReasonNotBuilt}` case dies --
  and since no Codex row carries `ReasonNotBuilt` afterwards, the
  "niwa's own debt" case and the comments around both lists are
  rewritten, not patched (R8).
- `TestBindingsMatchTheirDeclarations` and
  `TestDeliveriesMatchTheBindings` become load-bearing for the four new
  binding rows in both drift directions.
- The guide's gap list regenerates via the drift test's `-update` flag;
  the generator omits an empty group, so the "What niwa hasn't built
  yet" heading disappears on its own (R14).
- Authored prose corrected by hand (R15): the guide's "Starting a
  session at the instance root" section (the "gets nothing else"
  account dies; the budget paragraph narrows -- a `.codex/` directory
  now exists at the root but carries no config document, so the budget
  key still has nowhere to live); the feature file's preamble ("lands
  inside a repository and nowhere else"); and
  `TestEachAgentTakesOneScope`'s doc comment, which still reasons from
  the disproven only-from-a-project-root claim.
- docs/prds/PRD-agent-capability-contract.md takes an appended
  amendment for rows 18 and 19 in its established below-the-table
  style, naming the tests as the authority its totals are not; the
  matrix body stays untouched (R16).
- docs/spikes/SPIKE-codex-discovery-mechanics.md carries this work's
  measurements as amendments to findings 1 and 5 -- the root-layer
  positive and negative controls, the copy-equals-symlink result, and
  the plugin-manifest namespacing rule -- landed with this design
  rather than deferred (R17).

## Implementation Approach

One PR: the PRD requires both rows to flip in the change that delivers
them, and the warning re-gate must not trail the flip. Commit-sized
increments, each keeping the suite green:

### Increment 1: unhook `internal/plugin`

Remove the workspace import (Install signature, caller-side notices,
`MaterializeTo` export), delete the CLI adapter, route the rank-2 call
sites through a deliver pass stub that still calls `plugin.Install`
directly. Behavior-preserving; existing installer tests move with the
signature.

### Increment 2: the embedded tree gains its manifest

`.claude-plugin/plugin.json` added under `internal/plugin/files/niwa/`.
Inert on the install path (verified: nothing reads or pins it there).

### Increment 3: leaf vocabulary

`RootSkillsPlan`, `RootSkillsReconcileSpec`, `NiwaPluginPlan`,
`NiwaPluginTreeName`, the payload-scope predicate, and the four
`Delivery` constants -- with their unit tests, all inert while the rows
are unavailable (a producer method gated on an unavailable capability
yields an empty plan).

### Increment 4: workspace deliveries

`RootSkillsMaterializer`, the `RootSettingsMaterializer` conversion,
both niwa-plugin procedures, the `procedureInput` fields, and the
pipeline wiring (root skills at the instance-root step; the niwa-plugin
deliver pass replacing the seam). All of it is inert until the flip: a
producer gated on an unavailable capability yields an empty plan, and
`procedureFor` answers false for an unimplemented pair.

### Increment 5: the warning re-gates

Dispatch reads the predicate; text rewritten; both pinning tests move.
Lands before the flip so the warning never has a silent window.

### Increment 6: the flip, the bound set, and the record

Rows 18 and 19 flip for Codex; `boundCapabilities` and `bindings` gain
their entries; the column and lookup tests are rewritten; the gap list
regenerates; the authored prose sites are corrected; the capability
matrix takes its amendment. One commit, so the declaration table's
one-change rule holds at the granularity it states.

### Increment 7: acceptance

The offline root scenario asserts placement, mirroring, the
one-level-up zero, idempotence, reconcile-on-removal, and the collision
behavior; the live scenario renders discovery under the new
binary-only gate, asserting the namespaced names -- 
`niwa:niwa-migrate-config` included -- at the root and the absence of a
one-level-up tree's skills.

The spike amendments (R17) are not an increment: they land with this
design document, since the measurements exist now and the spike is the
durable home the PRD names.

## Security Considerations

- **No new writes across the trust line.** Every new write lands inside
  the instance (`.niwa/plugin/`, `.codex/skills/` at the root) except
  the one that already existed: `plugin.Install`'s user-level tree,
  whose scope is unchanged by being bound. Nothing touches the
  developer's Codex configuration, and the trust posture of a
  root-started session is exactly what the spike measured: skills load
  untrusted, configuration keys stay inert without a trust entry niwa
  does not write. Delivering skills at the root widens *where* declared
  content is readable, not *what* a session can do -- the same trees
  are already delivered one directory down, and their content comes
  from workspace configuration the developer opted into.
- **No writes above the instance, including git metadata.** The root
  delivery's git-exclusions ride `EnsureInstanceGitignore` and
  `InstanceExcludePatterns` only. `EnsureRepoExclude` is explicitly
  off-limits for the root: it searches upward for an enclosing
  repository, so pointing it at an instance root nested inside a
  tracked outer tree would write into that outer repository's exclude
  file -- the one containment mistake this feature could plausibly
  make, named here so the implementation and its tests forbid it (R4).
- **Path safety is the producer's name rule, unchanged.** A delivered
  name reaches the filesystem only as a single path element
  (`deliverableName`), checked where the path is built; the deliberate
  absence of a resolving containment pass over skills plans -- a
  delivery is a symlink out of the tree by design -- carries over to
  the root with the same justification, and the niwa tree name is a
  constant, not input.
- **No secret material moves.** Skills trees are non-secret workspace
  content; the root `.codex/` directory carries no config document, so
  the 0o600-and-exclude obligations that attach to the payload config
  do not attach here. The embedded tree ships in the binary -- no
  network fetch, no new supply-chain surface.
- **The live check cannot leak a credential.** It runs under an
  isolated Codex home with no credential file present, and its gate
  deliberately does not copy the developer's real credential the way
  the quota-spending gate does; R19 makes that a requirement rather
  than a courtesy.
- **Collision behavior is deterministic.** Both R6 collisions resolve
  by stated rule with a warning, never by write order, so a
  maliciously- or accidentally-named workspace plugin cannot silently
  replace niwa's own skill content at the root.

## Consequences

### Positive

- A dispatched Codex worker can finally do what the root orientation
  document tells it to do, with the same namespaced skill names a
  repository session sees -- and the delivery is bound, so faking it
  fails a test in both drift directions.
- The dead-abstraction pattern loses its last two footholds in this
  corner: every implemented pair among the newly bound rows names the
  type the pipeline actually drives, and `internal/plugin` becomes a
  leaf with a directly callable install.
- The dispatch warning stops being wired to a stand-in row and starts
  reading the fact it reports; it survives this change and any future
  capability flip that doesn't move the payload scope.
- The record stays honest by regeneration: gap list, matrix amendment,
  spike amendments, corrected prose -- each in its designated home.

### Negative

- The bound set now contains rows whose two agents bind through
  different registries and different lifecycles, which is more for a
  maintainer to hold than the uniform materializer rows; and
  `procedureInput` grows two fields one of its three procedures
  ignores.
- Breaking the plugin-to-workspace cycle reshapes `plugin.Install`'s
  signature and deletes the adapter seam -- churn in a stable package,
  paid to make the binding real rather than nominal.
- niwa's own tree claims the name `niwa` at the root skills directory,
  so a workspace plugin genuinely named `niwa` loses root delivery
  (with a warning). The name is niwa's own to claim, but the
  restriction is real.
- The Claude side of row 19 is bound over a recorded defect: the
  binding claims an installed tree, not a resolving session. That is
  the honest maximum available inside this scope, and it leaves the
  registration gap standing.

### Mitigations

- The registry comments and the matrix amendment carry the
  two-registries story and the Claude-binding claim in the places a
  maintainer will actually look; the binding tests make forgetting them
  loud.
- The `plugin.Install` reshape lands as its own behavior-preserving
  increment with the existing installer tests moved, so the churn is
  reviewable in isolation.
- The root collision warning names both sources and the winning rule,
  so the excluded plugin's owner learns what happened and why from the
  apply output; per-repository delivery of that plugin is unaffected.
- The registration defect is recorded where its fix would start (the
  matrix amendment and the binding comment), scoped as a one-machine
  observation, with the correction path -- registering the marketplace
  -- explicitly named as future work.

## References

- docs/prds/PRD-codex-instance-root-skills.md -- the upstream
  requirements (R1-R19, N1-N2) and acceptance criteria this design
  implements.
- docs/briefs/BRIEF-codex-instance-root-skills.md -- the framing whose
  four open questions Decisions 1 through 4 close at mechanism level.
- docs/designs/current/DESIGN-agent-capability-contract.md -- the
  contract being extended: the plan model, the declaration table and
  routes, and the enforcement-test families the new bindings ride.
- docs/spikes/SPIKE-codex-discovery-mechanics.md -- the measured
  discovery mechanics (findings 1 and 5, including this work's
  amendments: the root-layer positive and negative controls, the
  copy-equals-symlink result, and the plugin-manifest namespacing
  rule).
- docs/prds/PRD-agent-capability-contract.md -- the capability matrix,
  taking an appended amendment for rows 18 and 19.
- docs/guides/codex-agent.md -- the guide whose generated gap list
  regenerates and whose instance-root section is corrected.
- `internal/agentplan/capability.go`, `internal/agentplan/skills.go`,
  `internal/agentplan/binding.go`, `internal/agentplan/payload.go` --
  the leaf surfaces this design extends.
- `internal/workspace/delivery_binding.go` and its test,
  `internal/workspace/pluginskills.go`, `internal/workspace/apply.go`
  -- the executor-side registries and pipeline steps.
- `internal/plugin/embed.go`, `internal/plugin/installer.go` -- the
  embedded tree and installer this design unhooks from
  `internal/workspace`.
- `internal/cli/dispatch.go`, `test/functional/features/codex-agent.feature`
  -- the warning and the acceptance surfaces.
