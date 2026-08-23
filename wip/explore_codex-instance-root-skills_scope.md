# Explore Scope: codex-instance-root-skills

## Visibility

Public

## Scope

Tactical

## Execution Mode

auto (dispatched background worker; decision points resolve through the
lightweight decision protocol rather than blocking on the author)

## Core Question

Two capability rows in the agent contract are declared unavailable for Codex
with the reason kind `ReasonNotBuilt` — row 18 `RootProjectSkills` and row 19
`NiwaPlugin`. A Codex session started at a workspace instance root, which is
where every dispatched background worker stands, receives no skills at all,
while the same worker one directory down inside a cloned repository receives
the workspace's plugin trees. What has to change so the root-started session
receives them the same way, and what shape does that change take given that
registration — the route Claude takes at the root — is out of reach for Codex?

## Context

- `niwa dispatch` puts every background Codex worker's working directory at the
  instance root. The root now carries an orientation document whose prose tells
  the worker to invoke a skill it does not have.
- Measured on 2026-08-23 against `codex-cli 0.147.0`: the instance root has no
  `.codex/` directory at all; every cloned repository has
  `<repo>/.codex/skills/` holding one symlink per configured plugin. Codex's
  discovery walk runs from the nearest project-root marker down to the working
  directory, so a session standing at the root never reaches the per-repository
  trees.
- The Claude implementation at the root is registration
  (`extraKnownMarketplaces` plus `enabledPlugins` in `.claude/settings.json`),
  and row 6 declares registration something Codex cannot receive from a
  workspace, because it lives only in the developer's own `~/.codex/config.toml`.
  The Codex route is therefore the symlink tree niwa already builds per
  repository, one directory higher.
- Row 18's declaration comment names the change: give the Codex payload layout a
  second scope. The payload layout already distinguishes `PayloadAtInstanceRoot`
  from `PayloadInRepo`; `agentplan.SkillsInputs` has no scope concept at all and
  its `Dir` doc says "a cloned repository, or a worktree of one".
- Skills are the one project-layer capability measured to load from an untrusted
  layer (spike finding 5), which is why row 18 needs no `DirectoryTrust` edge.

## In Scope

- Delivering plugin skills trees to the instance root for Codex.
- Row 19: niwa's own plugin, which carries the migrate-config skill.
- Making both rows end implemented with a delivery bound to the declaration.
- Regenerating the guide's gap list so both bullets disappear.
- A functional acceptance scenario with a negative control.
- Deciding, explicitly, what happens to root symlink targets under rotation.

## Out of Scope

- Any `[projects."<instance root>"]` trust entry. Skills load untrusted;
  reaching for trust crosses into rows 5, 8, 9 and 12, and widening what niwa
  writes into the developer's own Codex config is the author's call.
- Adding a where-from axis to the declaration table. The schema is scoped by
  who receives a capability, never by where from.
- Shipping `codex exec --dangerously-bypass-hook-trust`.
- Changing the per-repository or worktree delivery, which works and must keep
  working unchanged.

## Research Leads

1. **How does the Codex plugin-skills delivery run end-to-end today, and what
   in that path is repository-shaped rather than directory-shaped?**
   (`lead-skills-path`)
   The root is not a git repository and belongs to no repo group. Anything the
   current path assumes about being inside a clone — git-exclude coverage,
   per-repo gating, worktree content install, reconcile scoping — is a place
   the root delivery either diverges or has to be generalized.

2. **What exactly is the "second scope" row 18 names, and how do the payload
   layout and the skills layout relate?** (`lead-layout-scope`)
   The payload layout carries a `PayloadScope`; the skills layout carries none.
   Whether the change lands in one table, the other, or both decides the shape
   of every downstream test.

3. **How is row 19 `NiwaPlugin` delivered for Claude today, and what is the
   "identical plugin manifest" wiring that is unbuilt for Codex?**
   (`lead-niwa-plugin`)
   Row 19 may be a consequence of row 18 or an independent delivery. Which one
   decides whether this is one change or two, and whether row 19 can bind to a
   delivery of its own.

4. **What structural scans, binding rules and drift tests already exist, and
   what precisely would each fail on?** (`lead-scans`)
   The mandate is to convert every structural requirement into a property a
   test can fail on. That starts with an inventory of the properties already
   enforced: the no-agent-constant-at-the-call-site scan, the
   `internal/workspace` names-no-agent scan, the declaration/delivery binding
   rule, and the generated gap list's drift test and its `-update` mode.

5. **Where do the symlink targets live, how often does niwa rotate them, and
   what does the per-repository delivery already do about it?**
   (`lead-rotation`)
   `.niwa/marketplaces/` is materialized from a source repo and replaced
   wholesale on refresh. The design owes a deliberate answer — inherit the
   exposure, repair on apply, or point somewhere more stable — rather than
   inheriting it silently because the repo case does.

6. **What functional-test surface exists for a Codex acceptance scenario, and
   how much can be measured without spending model quota?**
   (`lead-acceptance`)
   The acceptance bar is a scenario that starts a Codex session at an instance
   root and resolves a workspace skill, with a negative control one directory
   up. Existing Codex measurement patterns, isolated `CODEX_HOME` handling, and
   what the harness already does when the real binary is absent all shape what
   that scenario can assert.

7. **What cleanup, tracking and hygiene obligations attach to a niwa-written
   directory at the instance root?** (`lead-root-hygiene`)
   The repo path has git-exclude bookkeeping (row 24) and managed-file records
   behind it. The root has neither a git repository nor the same teardown path,
   so what keeps a de-configured plugin's root delivery from outliving its
   configuration needs to be named.
