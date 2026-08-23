# Exploration Findings: codex-instance-root-skills

## Round 1

### Key insights

**The row-18 comment points at the wrong table, and following it literally
would cross a line the brief forbids.** (`lead-layout-scope`, confirmed
directly.) Row 18's declaration says delivering it "means giving the Codex
payload layout a second scope." `payloadLayouts` governs `.codex/config.toml`
— MCP servers, the shell environment policy, approval and sandbox posture, the
doc budget — and its own comment at `internal/agentplan/payload.go:339-358`
says widening the Codex entry to `PayloadAtInstanceRoot` "is a decision about
what niwa writes outside an instance," because every key in that document needs
a `[projects."<path>"]` trust entry to take effect. That is precisely the
decision the brief reserves to the author. Skills are mechanically unrelated to
that table: `SkillsPlan` takes a bare `Dir` and no scope at all.

**The precedent for one capability at two scopes is a sibling producer method,
not a scope field.** (`lead-layout-scope`.) Orientation already splits this way
— `RootContextPlan`/`RootSessionOrientation` versus
`RepoContextPlan`/`RepoOrientationDoc` versus `WorktreeContextPlan`/
`WorktreeOrientationDoc` in `internal/agentplan/context.go` — four methods,
four capabilities, one shared path helper. Rows 5 and 18 are already declared
as two independent capabilities in exactly that shape. The payload table's
single-capability-with-an-internal-scope-gate is the other pattern, and it has
already blurred something: rows 8, 9 and 12 are declared `StateImplemented` for
Codex with no scope caveat even though `PayloadPlan` only ever fires in-repo
for Codex.

**The delivery machinery is already directory-shaped.** (`lead-skills-path`.)
`SkillsPlan`, `SkillsReconcileSpec`, `reconcileSkillsDir` and the executor's
`OpDeliverTree` never inspect `.git`, shell out to git, or consult the repo
list. The `Dir` doc comment ("a cloned repository, or a worktree of one") is
narrowing prose, not an enforced invariant. `AgentEnabled(cfg, "", agent)` —
the workspace-level gate with an empty repo name — is already used at the
instance root at `internal/workspace/apply.go:1702` for the sibling payload
call, so gating a root delivery needs no new mechanism.

**Row 19 is genuinely independent of row 18.** (`lead-niwa-plugin`.) niwa's own
plugin is embedded in the binary at `internal/plugin/files/niwa/` (a manifest
plus `skills/migrate-config/SKILL.md`) and installed by `plugin.Install`
straight into the developer's global `~/.claude/plugins/marketplaces/niwa/`,
fired from the `InstallNiwaPlugin` seam in `apply.go`. It never touches
`agentplan.Entry`, `ResolvePluginTrees`, or the configured-plugin list, so it
does not ride along on a root skills tree. Its Claude side is also not in
`binding.go`'s tables today.

**The acceptance bar is reachable with zero model spend, and the negative
control holds.** (`lead-acceptance`, plus a live measurement taken here.)
`codex debug prompt-input` renders a `<skills_instructions>` block listing every
skill the session resolved, with its source path. Measured against
`codex-cli 0.147.0` with an isolated empty `CODEX_HOME`, no `.git` anywhere in
the ancestry and no trust entry:

- A symlink to the real shirabe plugin tree at `<session dir>/.codex/skills/shirabe`
  yielded **20 skills, every one namespaced `shirabe:<skill>`**
  (`shirabe:brief`, `shirabe:explore`, `shirabe:execute`, …).
- The negative control — a second real plugin tree symlinked at
  `<parent>/.codex/skills/tsukumogami`, one directory up — yielded
  **0 `tsukumogami:*` skills**.
- The only other entries were Codex's own five bundled system skills.

This is the whole claim, measured end to end: the tree loads from where the
session stands, untrusted, and a tree above the session does not reach it.

**Rotation is not rotation.** (`lead-rotation`.) `.niwa/marketplaces/<name>` is
fetched **once** — `ensureMarketplaceContent` returns early when the manifest is
already present, with no TTL, no refresh flag and no re-fetch on apply, and the
doc comment says re-fetching "would swap the tree under a running session for no
benefit." When a re-fetch does happen (only after a human deletes the
directory), it extracts to `<dest>.staging` then `RemoveAll(dest)` +
`Rename(staging, dest)` — path-stable. A symlink resolves by path, so it keeps
working across the swap with no repair needed.

### Tensions

**"Apply repairs breakage" is not true today, and a design must not assume it.**
(`lead-rotation`.) The plan entry's precondition is `IfSourceExists`: when
`os.Stat(Source)` fails, the entry is skipped as a no-op, which means an
*existing dangling symlink is left untouched* rather than removed or reported.
`reconcileSkillsDir` only removes names that fell out of the configured set and
never resolves targets, so a dangling link for a still-configured plugin is
invisible to it too. The per-repository delivery's resilience is entirely a
byproduct of path-stable materialization, not a repair mechanism.

**Git-exclude coverage: two agents disagreed, and the cautious one is right.**
`lead-skills-path` reported `EnsureRepoExclude` no-ops safely on a non-git
directory; `lead-root-hygiene` found it *searches upward* for an enclosing
repository, so pointing it at an instance root nested inside a tracked outer
tree could write into that outer repo's exclude file. The root already has its
own narrower mechanism (`EnsureInstanceGitignore` / `InstanceExcludePatterns`),
and the root delivery should simply not reach for the repo-exclude path.

**Being unbound is currently tolerated; for these two rows it is not.**
`delivery_binding.go` says outright that marketplace registration and
git-exclude bookkeeping "are implemented and still unbound," and their absence
from `BoundCapabilities` records that honestly. Row 5 `PluginSkills` is
unbound too. The brief nevertheless requires rows 18 and 19 to end bound, so
this work adds both to `boundCapabilities` — which then forces a `bindings` row
for **every** implemented (capability, agent) pair among them, Claude included.
Claude's row 19 delivery (`plugin.Install`) therefore has to be named and
registered as part of this work.

### Corrections to inputs

Two agent claims were checked and are wrong; recording them so they do not
propagate.

- `lead-skills-path` asserted MCP servers and posture "are already delivered at
  the root" for Codex. They are not: `payloadLayouts[agent.AgentCodex]` carries
  `scope: PayloadInRepo` only, so the root payload call produces nothing for
  Codex. The `dispatch.go:402` warning is accurate today.
- `lead-scans` reported that Claude's instance-root skills come from a bespoke
  `rootSkillsFS` embed in `root_materializer.go`. That mechanism targets
  `<workspaceRoot>/.claude/skills/` — the *workspace* root, one level above the
  instance root, carrying niwa's own `/dispatch` skill. Claude's row-18
  delivery at the **instance** root is settings registration
  (`enabledPlugins` + `extraKnownMarketplaces`), confirmed on this live
  instance.

`lead-rotation` also reasoned about the workspace root rather than the instance
root when it concluded `repo:`-sourced marketplaces have no resolvable target.
At an instance root they resolve fine: the clones live under it, and
`.niwa/marketplaces/` sits there too. Verified on the live instance — the
existing per-repository links point at `<instance>/.niwa/marketplaces/shirabe`
and `<instance>/private/tools/plugin/tsukumogami`, both of which a root-level
link would name identically.

### Tests and documents that react to the change

From `lead-scans`, all confirmed by reading source:

| Surface | What breaks |
|---|---|
| `TestCodexColumnTotals` (`capability_test.go`) | Hardcoded `13, 11` becomes `15, 9` |
| `TestCodexColumnStatesWhatIsDelivered` | `codexDelivered` / `codexFinalGaps` lists must move both rows |
| `TestLookupAnswersEachDeclaredPair` | Literal `{NiwaPlugin, Codex, StateUnavailable, ReasonNotBuilt}` case dies; **no `ReasonNotBuilt` row survives for Codex at all** |
| `TestBindingsMatchTheirDeclarations` | Silent today; load-bearing once both rows join `boundCapabilities` |
| `TestCodexGuideGapSectionMatchesDeclarations` | Regenerate with `go test ./internal/agentplan -run TestCodexGuideGapSectionMatchesDeclarations -update` |
| `internal/cli/dispatch.go:402` + `dispatch_contract_test.go` | Warning is gated on row 18; its text also promises MCP and posture |
| `docs/guides/codex-agent.md` authored prose | "It gets nothing else, yet" section is hand-written, sits *above* the generated block, and goes stale |
| `docs/prds/PRD-agent-capability-contract.md` | Carries the matrix; **no mechanical drift test exists for it** |

The layout scan constrains the implementation rather than breaking: no agent
constant may appear at the materializer call site, and `internal/workspace` may
name no agent and no agent context filename. The root delivery must therefore
reach `.codex/skills` through the producer's `skillsDir`, never as a literal.

### Gaps carried into round 2

1. Row 19's concrete Codex delivery is unspecified: where the embedded plugin
   tree gets materialized so a symlink can name it, whether its manifest shape
   namespaces under Codex, and whether `migrate-config` is even meaningful to a
   Codex session.
2. What actually remains missing at the instance root for Codex once skills
   land — needed to rewrite the dispatch warning and the guide's authored prose
   truthfully rather than deleting them.

## Round 2

Two leads: what remains missing at the root once skills land (needed to rewrite
the dispatch warning and the guide's authored prose truthfully), and where
niwa's own embedded plugin tree should be materialized and how row 19 binds.

### Measured: the namespace depends on a manifest niwa's own tree does not ship

Taken directly against `codex-cli 0.147.0`, isolated empty `CODEX_HOME`, no
`.git` in the ancestry, no trust entry, via `codex debug prompt-input` — no
model turns. One variable between the two variants:

| Tree at `<session dir>/.codex/skills/niwa` | Resolved name |
|---|---|
| `internal/plugin/files/niwa/` exactly as shipped (`manifest.json` at tree root) | `niwa-migrate-config` — **no namespace** |
| the same tree plus `.claude-plugin/plugin.json` | `niwa:niwa-migrate-config` — namespaced |

The shirabe marketplace tree ships `.claude-plugin/plugin.json` and namespaces
all 20 of its skills. So `.claude-plugin/plugin.json` at the tree root is what
produces the `<plugin>:<skill>` name, and niwa's own embedded tree lacks it.

This corrects an assumption worth naming: row 19's reason string says "Codex
accepts the identical plugin manifest." Codex does read the tree and does
surface the skill either way — so the row is genuinely deliverable — but
"identical manifest" is loose. The manifest niwa's tree carries
(`manifest.json`, niwa's own marketplace format) is not the one that namespaces;
the Claude *plugin* manifest is. Whether to add it is a design decision with a
consequence for the existing Claude install path, not a detail.

The skill's own frontmatter name is already `niwa-migrate-config`, so the
namespaced form is the redundant-looking `niwa:niwa-migrate-config`. That
redundancy exists on the Claude side today and is not this work's to fix.

### What remains missing at the root has exactly one cause

(`lead-remaining-gap`, verified.) After root skills land, a root-started Codex
session still lacks MCP servers, the session environment, and the approval and
sandbox posture — plus the doc-budget key. All four are keys in
`.codex/config.toml`, and all four are gated by the single line
`payloadLayouts[agent.AgentCodex].scope = PayloadInRepo`. One cause, one code
location, and row 18 does not touch it. That is what lets the two prose sites be
corrected precisely instead of deleted.

Three consequences the lead pinned down:

- The `dispatch.go` warning is gated on `Lookup(RootProjectSkills, …)` and will
  **go silent** when row 18 flips, while the gap it warns about is still real.
  It must be re-gated on the payload-scope fact itself. No declaration row means
  "delivered in a repository, not at the root", and inventing one is forbidden,
  so the gate has to read the payload layout — which needs a new exported
  predicate, since `payloadLayout()` is unexported and nothing else exposes
  scope support. Its two pinning tests move with it
  (`dispatch_agentwarning_test.go`, `dispatch_contract_test.go`).
- The warning already omits the session environment from its list, which is a
  pre-existing inaccuracy worth folding into the same rewrite.
- In the guide, the budget paragraph's premise ("there's no project layer at the
  instance root") narrows rather than dies: after this change a `.codex/`
  directory does exist at the root — it just has no `config.toml`. Easy to miss
  if only the "gets nothing else" sentence is patched.

### The binding route is fixed per capability, and it decides the implementation

(`lead-niwa-plugin-delivery`, verified against `internal/agentplan/capability.go`
and `internal/workspace/delivery_binding_test.go`.) The catalog assigns each
capability a `Route`, and `TestDeliveriesMatchTheBindings` sends a binding to
the registry its route names:

- `RootProjectSkills` is **`RoutePlan`** → a binding for it must name a
  `Delivery` registered in the `deliveries` map as a `Materializer`
  (`Name() string` plus `Materialize`), whose `Name()` matches the delivery
  name.
- `NiwaPlugin` is **`RouteProcedure`** → its binding must name a `Delivery`
  registered in the `procedures` map, for **both** agents.

This is the single most consequential structural fact found. It means row 19's
Codex delivery, even though it writes inside the instance and is mechanically a
skills-tree write, must still be registered as a procedure. And
`procedureInput` carries no instance root today, so it needs one — a struct
whose doc comment currently leans on "a side effect outside the instance."

Two further facts about Claude's row 19 that the brief did not anticipate:
`plugin.Install` fires only inside rank-2-config-detection branches, not on
every apply, and it records nothing in instance state — it is global,
once-per-developer-machine, and untouched by `niwa destroy`/`reap`. The two
agents' row-19 deliveries therefore have genuinely different lifecycles.

### An unmeasured risk the design must not step on

Adding `.claude-plugin/plugin.json` to the embedded tree is what would give
Codex the `niwa:` namespace. But the documented command is
`/niwa:migrate-config`, which comes from the *marketplace manifest's*
`skills[].name` (`migrate-config`), while the SKILL.md frontmatter says
`niwa-migrate-config`. How Claude Code resolves this exact tree shape today is
not evidenced anywhere in the repo and was not measured. Changing the tree's
shape could silently rename the command for existing Claude users. This needs
its own measurement before the tree is touched, or the change must be scoped so
the Claude path cannot move.

### Leads are exhausted

Every question raised in round 1 has been answered or converted into a design
choice with its options and consequences enumerated. What is left — which
binding shape row 18 takes, whether the embedded tree gains a plugin manifest,
how the dispatch warning is re-gated — are decisions for the design hop, not
things more research would settle.

## Decision: Crystallize

## Post-crystallize observation (recorded during the scope chain)

While preparing design inputs, the Claude side of row 19 was inspected on this
live machine and does not appear to work. The evidence, all direct:

- `~/.claude/plugins/marketplaces/niwa/` exists and holds exactly what
  `plugin.Install` writes: `manifest.json` plus `skills/migrate-config/SKILL.md`.
- It has **no `.claude-plugin/` directory**. Every other marketplace under that
  path does — shirabe's carries both `marketplace.json` and `plugin.json`.
- `~/.claude/plugins/known_marketplaces.json` lists seven marketplaces and
  **`niwa` is not one of them**, while `shirabe` is.
- The Claude Code session running this chain surfaces `shirabe:*` and
  `tsukumogami:*` skills and **no `niwa:*` skill at all**.

Read together: niwa installs its own plugin into a directory Claude Code's
marketplace registry does not know about, in a format that is niwa's own rather
than Claude's plugin format, and the skill it carries is not reachable from a
session. Row 19 is declared implemented for Claude.

Two consequences for the design hop.

First, it dissolves the risk that blocked D6. The concern was that adding
`.claude-plugin/plugin.json` to the embedded tree could silently rename the
documented `/niwa:migrate-config` command for existing Claude users. There is no
working resolution to break — the command does not resolve today on this
machine.

Second, it is a defect adjacent to this work rather than inside it. Binding row
19 for Claude means naming `plugin.Install` as the delivery behind a declaration
that says the capability is delivered, and a delivery that writes bytes nothing
reads is the exact failure the binding rule exists to surface. The design must
decide whether to say so and stop, or to fix the tree's shape as part of giving
Codex the same tree. It must not bind the row silently as though the Claude side
were sound.

This is one machine, so the claim is scoped to what was observed rather than
asserted of every install.

### Copy delivery resolves identically to a symlink

Measured the same way, zero model turns: a plugin tree **copied** into
`<session dir>/.codex/skills/shirabe` — the shape the executor's Windows
fallback produces — resolved the same 20 namespaced skills as the symlinked
tree, and niwa's own `.niwa-delivered-tree` sentinel file sitting inside the
copied tree did not interfere with discovery or appear as a skill.

So the delivery shape is free at the root: symlink and copy both work, and the
choice can be made on the reconcile mechanism's terms rather than on whether
Codex will read the result.
