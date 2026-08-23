# Decision log: /design codex-instance-root-skills

Inline-resolved under the parent-orchestration dispatch fallback
(decision-bypass-with-inline-resolution). Evidence base: the parent's
design-inputs file (measured against codex-cli 0.147.0), the exploration
findings (two rounds plus post-crystallize measurements), and direct
source reads in this worktree. Recommendations from the parent were
adopted where the evidence held; every departure is noted.

| id | tier | status | question (abbreviated) |
|----|------|--------|------------------------|
| D1 | 3 (inline per dispatch fallback) | confirmed | Row 18 binding shape, both agents |
| D2 | 3 (inline per dispatch fallback) | confirmed | Row 19 Codex delivery, site, collisions |
| D3 | 2 | confirmed | Warning re-gate mechanism |
| D4 | 2 | confirmed | Claude plugin manifest + row-19 Claude disposition |

<a id="d1"></a>
## D1: how row 18 binds

**Frame.** RoutePlan bindings must name a Delivery registered in the
workspace `deliveries` map as a Materializer whose Name() matches
(TestDeliveriesMatchTheBindings, TestRegisteredDeliveriesAreWhatTheyClaim).
Codex's root skills delivery does not exist yet; Claude's is
InstallWorkspaceRootSettings, a plain function.

**Gather.** Registry comment says values are identities, pipeline builds
its own instances with call-site options (EnvMaterializer/FilesMaterializer
precedent). R11 requires the registered type to be the write path.
Sibling-producer precedent for one capability at two scopes
(context.go). Row 5 tags PluginSkills; a root plan must tag
RootProjectSkills for the plan-shape guard to distinguish the rows.

**Decide.** Codex: new Producer.RootSkillsPlan (tags RootProjectSkills,
same skillsDir helper) + RootSkillsMaterializer registered under
Delivery "root-skills", driven by the pipeline at the instance-root step.
Claude: InstallWorkspaceRootSettings becomes RootSettingsMaterializer
registered under "root-settings", driven at its existing call site.
Rejected: plan-entry tagging alone (fails R9's named-delivery wording,
row 5's unbound posture is what the brief overrides); identity-only
registration with the write elsewhere (violates R11 -- the dead
abstraction); a third binding mechanism in the test (splits enforcement).
Status: confirmed.

<a id="d2"></a>
## D2: row 19's Codex delivery

**Frame.** RouteProcedure requires a registered procedure per implemented
pair. The embedded tree lives in internal/plugin, which imports
internal/workspace (cycle). procedureInput has no instance root.

**Gather.** plugin.Install's workspace dependency is three symbols
(Reporter, EmitPluginNotice, InstanceState -- the state param is always
nil at call sites). codexTrustProcedure precedent: registered value is
runnable, Deliver calls the package function. marketplaceContentRoot
establishes .niwa/<dir> for niwa-owned instance content, fetched-once,
path-stable replacement. Measured: copied tree resolves identically to
symlinked; sentinel file does not interfere.

**Decide.** Break the plugin-to-workspace import cycle (notice emission
moves to callers; Install takes an explicit home). Register
claudeNiwaPluginProcedure (Deliver calls plugin.Install) and
codexNiwaPluginProcedure (Deliver materializes the embedded tree at
<instanceRoot>/.niwa/plugin/niwa -- deliberately not under
.niwa/marketplaces/ -- then delivers it into the root skills dir via a
NiwaPluginPlan entry tagged NiwaPlugin, executed by applyPlan).
procedureInput gains InstanceRoot and the agent's Producer. Collisions:
site collision impossible by construction; delivered-name collision at
the root resolves in favor of niwa's own tree, with the configured
plugin named "niwa" skipped at the root and warned about, both sources
named; per-repo delivery of a plugin named "niwa" is unchanged.
Rejected: extract under .niwa/marketplaces/niwa (silent collision with a
configured marketplace named niwa -- exactly what R6 forbids); resolve
from the Claude-side global install (couples agents, spike finding 8's
warning); write the embedded tree directly into .codex/skills with no
niwa-owned source (loses the source-plus-link shape apply already
reconciles, and R7's path-stable position); keep the function-field seam
and register an identity-only procedure (R11 violation).
Status: confirmed.

<a id="d3"></a>
## D3: re-gating the dispatch warning

**Frame.** The warning is gated on row 18 and goes silent when it flips;
the remaining gap is gated by payloadLayouts[Codex].scope == PayloadInRepo,
which nothing exports.

**Gather.** No declaration row may express "delivered in a repo, not at
the root" (schema decision). Name-based gate at the call site is what
TestDispatchGateFollowsTheDeclaration exists to avoid. The warning text
already omits the session environment (pre-existing inaccuracy).

**Decide.** Export a predicate from agentplan over the payload layout
table (config-document capabilities are repository-scoped for this
agent); gate the warning on it; rewrite the text to name MCP servers,
the session environment, and the approval and sandbox posture, and stop
claiming skills. Pinning tests move in the same change. An agent with no
payload layout returns false and prints no warning.
Rejected: direct agent-name check (reintroduces the avoided pattern,
breaks on a third agent); new declaration row (forbidden); keep row-18
gate (goes silent while the gap is real).
Status: confirmed.

<a id="d4"></a>
## D4: the Claude plugin manifest, and the Claude row-19 disposition

**Frame.** Measured: without .claude-plugin/plugin.json the delivered
tree resolves the skill bare (niwa-migrate-config); with it, namespaced
(niwa:niwa-migrate-config). The brief feared breaking the documented
/niwa:migrate-config for existing Claude users.

**Gather.** On the preparing machine there is no working resolution to
break: the installed tree lacks .claude-plugin/, the marketplace is
absent from Claude Code's registry, no niwa:* skill resolves
(one-machine observation). Adding the file is mechanically safe:
Embedded() reads only manifest.json, idempotency compares only
manifest.json, stageAndRename walks the whole tree, no test pins the
file set.

**Decide.** Add .claude-plugin/plugin.json to the embedded tree; the
Codex delivery namespaces like every other plugin, and the acceptance
scenario asserts niwa:niwa-migrate-config. Claude disposition per R10:
the tree's format defect is partially corrected as a side effect (it
gains the plugin manifest), the registration defect is recorded (niwa
never writes Claude Code's marketplace registry; on the inspected
machine the marketplace is unregistered and the skill unresolvable), and
the (NiwaPlugin, Claude) binding's claim is stated exactly: the delivery
materializes the tree at the install path; it does not claim a Claude
session resolves it. Making registration work stays out of scope.
Rejected: ship the tree unchanged (bare name, defect wholly
uncorrected); fix Claude registration (out of scope, no measured
contract to build against).
Status: confirmed. The one-machine scoping of the observation is
recorded as an assumption, stated as such wherever the defect is
written down.

## Cross-validation (Phase 3)

- D1 x D2 at the root skills directory: the root reconcile's Keep set is
  produced by the leaf and includes the niwa tree name when NiwaPlugin
  is implemented for the agent; the delivered-name collision rule in D2
  constrains RootSkillsPlan (skip + warn on a configured plugin named
  "niwa"). The tree name is a leaf constant so both sides agree by
  construction. No conflict.
- D4 x D2: the materialized tree carries the manifest, so the namespace
  measurement applies to the delivered content unchanged. No conflict.
- D3 is independent of the other three (reads only the layout table).
- Verdict: passed, no restarts.
