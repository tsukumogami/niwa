---
upstream: docs/prds/PRD-codex-instance-root-skills.md
---

# DESIGN: codex instance root skills

## Status

Proposed

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
