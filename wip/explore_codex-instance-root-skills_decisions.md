# Exploration Decisions: codex-instance-root-skills

Decisions taken during convergence, in `--auto` mode under the lightweight
decision protocol. Each names what was decided, on what evidence, and its
status.

## Round 1

### D1 — The delivery is a sibling producer method, not a payload-layout scope

**Question.** Row 18's declaration comment says delivering it means "giving the
Codex payload layout a second scope." Does the change land in `payloadLayouts`?

**Evidence.** `payloadLayouts` governs `.codex/config.toml` only — MCP servers,
`shell_environment_policy`, approval and sandbox posture, the doc budget. Its
own comment (`internal/agentplan/payload.go:339-358`) states that widening the
Codex entry to `PayloadAtInstanceRoot` "is a decision about what niwa writes
outside an instance," because every key in that document is inert without a
`[projects."<path>"]` trust entry. The brief reserves that decision to the
author. Skills reach the session through an entirely separate table
(`skillsLayouts`) whose plan input carries no scope at all, and skills are the
one project-layer capability measured to load untrusted.

**Decided.** Leave `payloadLayouts` untouched. Deliver row 18 through a sibling
producer method on the skills path, gating on `RootProjectSkills` rather than
`PluginSkills`, following the `RootContextPlan` / `RepoContextPlan` precedent in
`internal/agentplan/context.go`. Record in the design that the row-18 comment
points at the wrong table, so the next reader is not sent where this one was.

**Status.** confirmed — the alternative is explicitly out of scope.

### D2 — No scope field is added to `SkillsInputs`

**Question.** Should the skills path gain a `PayloadScope`-style field so one
producer method serves both scopes?

**Evidence.** The codebase carries both patterns. Orientation splits by scope
into separate capabilities with separate producer methods (four of them). The
payload table uses one capability with an internal scope gate — and that pattern
has already produced a stated-but-untrue declaration: rows 8, 9 and 12 are
declared implemented for Codex with no scope caveat while `PayloadPlan` only
ever fires in-repo for Codex. Rows 5 and 18 are already two separate
capabilities, which is the orientation shape.

**Decided.** Second producer method, no scope field. A scope field would
introduce a second mechanism for a distinction the capability table already
expresses, and would reproduce the blur that made rows 8/9/12 overclaim.

**Status.** confirmed.

### D3 — Root links inherit the repository links' target handling, deliberately

**Question.** The brief requires an explicit answer: do root symlinks inherit
the rotation exposure, repair on apply, or point somewhere more stable?

**Evidence.** Three measured or read facts. First, `.niwa/marketplaces/<name>`
is fetched **once** — `ensureMarketplaceContent` returns early when the manifest
is present, with no TTL and no refresh on apply, and its comment says
re-fetching "would swap the tree under a running session for no benefit."
Second, when a re-fetch does happen (only after a human removes the directory),
it stages to `<dest>.staging` and then `RemoveAll` + `Rename` into place, so the
path is stable and a symlink resolving by path survives it. Third, verified on
this live instance: the per-repository links already point at
`<instance>/.niwa/marketplaces/shirabe` and at a plugin directory inside a
cloned repo — and a root link would name **the same absolute paths**, because
both live under the instance root.

**Decided.** Root links point at the same targets the repository links already
point at, and the design says so as a reasoned position rather than an
inheritance. The reason it is safe is not "the repo case does it": it is that
the target path is owned at the same scope as the link and is replaced
path-stably, never moved. Two honest limits get written down alongside it: there
is no dangling-link detection anywhere in this pipeline (`IfSourceExists` skips
a vanished source, leaving an existing stale link untouched, and
`reconcileSkillsDir` never resolves targets), so a design must not claim "apply
repairs it"; and a root link is one more link to the same target, so it adds no
new exposure class.

**Status.** confirmed.

### D4 — The root delivery does not reach for the repository exclude path

**Question.** Does a `.codex/skills` tree at the instance root need git-exclude
coverage (row 24)?

**Evidence.** Two agents disagreed. `EnsureRepoExclude` searches *upward* for an
enclosing repository, so aiming it at an instance root nested inside a tracked
outer tree could write into that outer repository's exclude file. The instance
root already has its own narrower mechanism, `EnsureInstanceGitignore` /
`InstanceExcludePatterns`. The delivered tree is symlinks to already-installed
plugin content and carries no secret material.

**Decided.** The root delivery does not call the repo-exclude path. If coverage
is wanted, it goes through the instance's own gitignore mechanism. The cautious
reading wins because the failure mode of the other one is writing into a
repository niwa was not asked to touch.

**Status.** confirmed.

### D5 — Both rows are added to `boundCapabilities`, which pulls in the Claude side

**Question.** The brief requires both rows to end bound. What does that force?

**Evidence.** `TestBindingsMatchTheirDeclarations` checks both directions over
`BoundCapabilities()`: once a capability is in that list, **every** implemented
(capability, agent) pair for it needs a `bindings` row, and every binding needs
an implemented declaration. Rows 18 and 19 are implemented for Claude already.
`delivery_binding.go` records that some capabilities are deliberately still
unbound, so this is opt-in — but the brief opts in.

**Decided.** Add both capabilities to `boundCapabilities` and register a
delivery for all four pairs: (18, Claude), (18, Codex), (19, Claude), (19,
Codex). Claude's existing deliveries get named and registered as part of this
work; that is scope the brief's mandate implies rather than scope creep.

**Status.** confirmed.

## Round 2

### D6 — Row 19's namespace requires a decision the measurement forced

**Question.** niwa's embedded plugin tree resolves under Codex as
`niwa-migrate-config` without a `.claude-plugin/plugin.json` and as
`niwa:niwa-migrate-config` with one. Should the manifest be added?

**Evidence.** Measured both variants against the real binary (see the findings
file). The skill's documentation heading already writes its invocation as
`/niwa:migrate-config`, and every other workspace plugin niwa delivers
namespaces. An unnamespaced skill name also collides in a flat namespace with
anything else called `niwa-migrate-config`.

**Decided.** Deferred to the design hop, because it has a consequence outside
this work: the same embedded tree is installed into the developer's global
Claude plugin directory by `plugin.Install`, and whether an added
`.claude-plugin/plugin.json` coexists with the existing root `manifest.json`
there has to be checked before it is added. `lead-niwa-plugin-delivery` is
answering exactly that.

**Status.** escalated to the design hop — evidence gathered, choice not taken.
