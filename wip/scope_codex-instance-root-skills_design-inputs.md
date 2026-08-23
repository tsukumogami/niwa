# Design inputs: codex-instance-root-skills

Prepared by the `/scope` parent for the `/design` hop. Everything here is either
read from the tree or measured against the real `codex-cli 0.147.0` binary at
zero model cost. Recommendations are recommendations — the design runs its own
decision process and may overrule any of them, but it should not re-derive the
evidence.

## The four decisions the design owes

### D-A. How row 18 binds

**The constraint, verified.** `internal/agentplan/capability.go`'s catalog fixes
a `Route` per capability. `RootProjectSkills` is `RoutePlan`; `NiwaPlugin` is
`RouteProcedure`. `internal/workspace/delivery_binding_test.go`'s
`TestDeliveriesMatchTheBindings` sends a binding to the registry its route
names: plan-routed bindings must name a `Delivery` registered in the
`deliveries` map as a `Materializer`; procedure-routed ones must name one in the
`procedures` map. `TestRegisteredDeliveriesAreWhatTheyClaim` then asserts the
registered value's `Name()` equals the delivery name, so the registration cannot
agree with itself.

**The genuine fork.** Every other plan-routed capability in the tree binds by
tagging its plan entries with the capability and asserting the tag in a
per-producer test — that is how `RepoOrientationDoc`, `MCPServers` and
`ApprovalPosture` are done, and row 5 `PluginSkills` is not in
`boundCapabilities` at all. `delivery_binding.go` says in prose that some
implemented capabilities are deliberately still unbound and that their absence
from `BoundCapabilities` "is what records that honestly." So leaving row 18
unbound would be consistent with the codebase. The task brief nevertheless
requires both rows to end bound, and names the binding rule — which is the
`boundCapabilities` one.

**Recommendation.** Do both, and make the named delivery real rather than
decorative. Tag the root-scope entries `Capability: RootProjectSkills` (that is
what makes the plan-shape assertion meaningful and what distinguishes row 18's
delivery from row 5's), and register an actual materializer for the root skills
delivery whose `Name()` matches a new `Delivery` constant. The registry's own
comment says its values are "used as the identity of the delivery, not as
runnable materializers," so a registered identity is legal — but a type that
exists only to satisfy a map lookup while the real write happens elsewhere is
the inert bookkeeping the round-2 research warned about. Prefer a materializer
the apply pipeline actually drives.

**Watch out.** `internal/workspace` may name no agent and no agent context
filename (there is an AST scan, and its last exemption window was closed
deliberately). The root delivery must reach `.codex/skills` through the
producer's own path helper, never as a literal.

### D-B. Where niwa's own tree is materialized for Codex

**Facts.** The tree is embedded in the binary at `internal/plugin/files/niwa/`
and is `manifest.json` plus `skills/migrate-config/SKILL.md`. A symlink cannot
name an `embed.FS`, so for a symlink delivery the tree must first reach disk.
`marketplaceContentRoot` establishes `<instanceRoot>/.niwa/marketplaces/<name>`
as the pattern for niwa-owned plugin content inside an instance, fetched once
and replaced path-stably.

**Measured, so the shape is free.** A *copied* plugin tree at
`<session dir>/.codex/skills/<name>` resolves exactly as a symlinked one — 20
namespaced skills from the real shirabe tree either way — and niwa's
`.niwa-delivered-tree` sentinel inside a copied tree neither breaks discovery
nor appears as a skill. So symlink-versus-copy can be decided on reconcile
grounds alone.

**Recommendation.** Extract to a niwa-owned path that is *not* under
`marketplaces/`, so a workspace that configures a marketplace literally named
`niwa` cannot collide with it. Then consider whether the delivery name at
`.codex/skills/niwa` can collide with a configured plugin of the same name, and
say what happens if it does — `deliverableName` guards the path element, not
name uniqueness across sources.

### D-C. Re-gating the dispatch-time warning

**The problem.** `internal/cli/dispatch.go` prints a warning gated on
`agentplan.Lookup(agentplan.RootProjectSkills, dispatchedAgent)` not being
implemented. When row 18 flips, the warning goes silent — but what it warns
about is still real. Its text also promises "skills, MCP servers or posture",
and it already omits the session environment, which is equally repo-scoped.

**The single cause, verified.** Everything still missing at the root after this
change — MCP servers, session environment, approval and sandbox posture, and the
doc-budget key — is gated by one line: `payloadLayouts[agent.AgentCodex]` carries
`scope: PayloadInRepo`. No declaration row means "delivered in a repository, not
at the root", and inventing one is forbidden.

**Recommendation.** Add an exported predicate over payload-scope support —
nothing exposes it today; `payloadLayout()` is unexported — and gate the warning
on that, rewording it to name the four things that genuinely stay missing and to
stop claiming skills among them. Its two pinning tests
(`dispatch_agentwarning_test.go`, `dispatch_contract_test.go`) move with it.
Reading the agent name directly in the dispatch path is the simpler option and
reintroduces the name-based check the existing test design avoided; prefer the
predicate.

### D-D. Whether the embedded tree gains `.claude-plugin/plugin.json`

**Measured, one variable.** niwa's tree symlinked at `.codex/skills/niwa`
exactly as shipped resolves the skill as bare `niwa-migrate-config`. The same
tree plus a `.claude-plugin/plugin.json` resolves it as
`niwa:niwa-migrate-config`. The shirabe tree ships that file and namespaces all
20 of its skills. So that file is what produces the `<plugin>:<skill>` name.

**The risk that blocked this, and why it looks smaller than it did.** The
concern was breaking the documented `/niwa:migrate-config` command for existing
Claude users. On this machine that command does not resolve at all: the
installed tree at `~/.claude/plugins/marketplaces/niwa/` has no
`.claude-plugin/` directory while every other marketplace there does,
`known_marketplaces.json` does not list `niwa` while it lists six others, and
the Claude session running this chain surfaces no `niwa:*` skill while surfacing
`shirabe:*` and `tsukumogami:*`.

**Recommendation.** Add the file, so the Codex delivery namespaces like every
other plugin. Treat the Claude-side observation as a separate finding and say so
in the design rather than fixing it here — but do **not** bind row 19 for Claude
as though its delivery were sound without addressing it. Naming `plugin.Install`
as the delivery behind a declaration that says the capability is delivered, when
the bytes it writes are not in a format the agent registers, is precisely the
drift the binding rule exists to catch. The honest options are to fix the tree's
shape (which the Codex work is touching anyway), or to record the defect and
stop short of claiming it is bound. The scope of this chain does not include
making Claude's registration work.

The claim above is from one machine and should be stated that way.

## What the acceptance scenario should look like

Two scenarios, because they gate differently.

**Offline, no binary needed — can gate CI.** Mirror the existing
`the workspace's skills reach Codex whole and namespaced` scenario in
`test/functional/features/codex-agent.feature` at the instance root. The step
vocabulary already works there with no new step definitions:
`resolveLocation` joins the location onto the workspace root, so `"ws"` names
the instance root and `"ws/tools/app"` names a repo inside it. The assertions
already exist — `"<loc>" holds exactly N Codex skills trees` and
`the Codex skills tree "<name>" at "<loc>" mirrors "<source>"`. The negative
control is `"." holds exactly 0 Codex skills trees`, the workspace root one
directory above the instance.

**Live, against the real binary — proves discovery, spends no quota.**
`codex debug prompt-input` renders a `<skills_instructions>` block naming every
skill the session resolved and the file each came from. Run it from the instance
root under an isolated `CODEX_HOME` and assert the delivered plugin's skills
appear namespaced; run it from a directory whose skills tree sits one level up
and assert they do not.

**Do not reuse the `codex is available` gate for the live one.** That step
requires a credential file and copies the developer's real credential into the
sandbox home, because the existing live scenarios run `codex exec` and need
auth. `codex debug prompt-input` needs no credential — it was measured here
against a completely empty `CODEX_HOME`. The new scenario wants a lighter gate
that only checks the binary is on PATH, so it skips on a machine without codex
rather than on a machine without a login. Give it its own tag rather than
`@codex-live`, which exists to keep quota-spending scenarios out of routine
runs; this one spends nothing.

CI has no `codex` binary, so the live scenario skips there and the offline one
carries the gating coverage.

## Prose that goes stale and must be corrected by hand

The generated gap list regenerates itself — the generator omits an empty group
entirely, so the "What niwa hasn't built yet" heading disappears on its own once
both rows flip, which is exactly what the task asks for. These are the authored
sites that do not:

- `docs/guides/codex-agent.md`, the "Starting a session at the instance root"
  section, which sits *above* the generated block. "It gets nothing else, yet"
  and "a root-started session has the orientation and none of the rest" become
  false. The budget paragraph's premise narrows rather than dies: after this
  change a `.codex/` directory does exist at the root, it just has no
  `config.toml`. The trust-prompt and mechanism paragraphs stay accurate.
- `test/functional/features/codex-agent.feature`'s own preamble, which says the
  project layer niwa writes for Codex "lands inside a repository and nowhere
  else."
- `docs/prds/PRD-agent-capability-contract.md`. That document is already a
  point-in-time record with appended amendments — its row 22 and its stated
  column totals are both superseded by an amendment that names the test as the
  authority the totals are not. Rows 18 and 19 want another amendment in the
  same style, not a matrix rewrite.
- `internal/agentplan/payload_test.go`'s `TestEachAgentTakesOneScope` doc
  comment, which still says Codex "reads a project layer only from a project
  root downward, which an instance root is not." That reasoning was already
  corrected for row 2 and this work disproves it directly.

## Mechanical test surface that reacts

All confirmed by reading source. In `internal/agentplan/capability_test.go`:
`TestCodexColumnTotals` hardcodes `13, 11` and becomes `15, 9`;
`codexDelivered` gains both rows and `codexFinalGaps` loses `NiwaPlugin`;
`TestLookupAnswersEachDeclaredPair` carries a literal
`{NiwaPlugin, Codex, StateUnavailable, ReasonNotBuilt}` case that dies. Note the
consequence: after this change **no Codex row carries `ReasonNotBuilt` at all**,
so that test's "niwa's own debt for codex" case has no replacement and the
comments around both lists need rewriting rather than editing.

Regenerate the guide with
`go test ./internal/agentplan -run TestCodexGuideGapSectionMatchesDeclarations -update`.
