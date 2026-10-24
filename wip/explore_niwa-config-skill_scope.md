# Explore Scope: niwa-config-skill

## Visibility

Public

## Core Question

Single-repo niwa workspaces (commuter, equity-planner) commit a hand-authored
`.niwa/workspace.toml` directly into the adopting repo. An agent later working
inside that repo's own niwa instance -- with no access to the tsukumogami org's
private context -- may need to extend that config (add a hook, wire a secret,
add a plugin, add instance files). Today it has no in-session guidance on the
config schema; the only trail is doc-link comments pointing at
`docs/guides/workspace-config-sources.md` and `docs/guides/vault-integration.md`
in the niwa repo, which the agent has to go discover and fetch cold. What
mechanism should ship this guidance into single-repo workspaces, and what
should that guidance contain?

## Context

niwa already ships an embedded Claude Code plugin at
`internal/plugin/files/niwa/` (manifest.json + skills/*), auto-installed to
`~/.claude/plugins/marketplaces/niwa/` -- but only when `niwa apply`/`create`/
`init` detects a rank-2 (deprecated whole-repo config) source. Its one skill,
`skills/migrate-config/SKILL.md`, only covers the rank-2 -> rank-1 migration.
The embedded-plugin install path never fires for rank-1 sources, which is the
normal case for single-repo workspaces -- so the agents who most need
config-editing guidance never get the plugin that could carry it.

`docs/guides/init-bootstrap.md` documents the scaffold template written by
`niwa init --bootstrap` into a freshly-adopted repo's `.niwa/workspace.toml`.
That's a candidate seeding point if the fix ships something at adoption time
rather than (or in addition to) via the embedded plugin.

`dangazineu/commuter`'s `workspace.toml` already uses
`[instance.files] "skills/" = ".claude/skills/"` to drop
`commuter-booked`/`commuter-options` skills into `.claude/skills/` at apply
time -- an existing precedent for scaffolding skills into a single-repo
workspace via the instance-files mechanism, distinct from the embedded-plugin
path.

## In Scope

- Delivery mechanism for getting workspace.toml-editing guidance into a
  single-repo (rank-1) workspace's agent context
- Content shape: schema reference for `claude.*`, `env.*`, `vault.*`, `files`,
  `instance` blocks, common edit walkthroughs, doc pointers
- Interaction with `niwa init --bootstrap`'s scaffold template
- Whether this needs a different shape for org-wide/multi-repo workspaces
  (out of scope for implementation per the dispatch brief, but worth noting
  if explore surfaces it)
- `public/niwa` repo only

## Out of Scope

- Building the mechanism itself (that's `/shirabe:scope` and `/shirabe:execute`)
- Rewriting `docs/guides/workspace-config-sources.md` or
  `docs/guides/vault-integration.md` beyond what's needed to point to them
- Changes to `docs/dot-niwa` or other repos (note only, per dispatch brief
  guardrails)
- The rank-2 -> rank-1 migration skill itself (`skills/migrate-config/`)

## Research Leads

1. **How does the embedded plugin currently install, and what gates that
   install today?**
   Need to understand the "rank-2 source detected" gate in
   `internal/cli/create.go`, `internal/cli/apply.go`, `internal/cli/init.go`
   and the `--no-install-plugins` flag precisely, to know what would need to
   change to also fire for rank-1 sources (or whether that's even the right
   lever).

2. **What does the `[instance.files]` mechanism actually do, end to end, and
   how does commuter's `workspace.toml` use it to scaffold skills?**
   This is a candidate delivery mechanism (scaffold a skill file into
   `.niwa/skills/` at apply time) distinct from the embedded plugin. Need to
   understand its semantics, timing (apply vs. init --bootstrap), and whether
   it can carry a full SKILL.md.

3. **What does `niwa init --bootstrap`'s scaffold template look like today,
   and is bootstrap time the natural moment to seed a config-editing skill
   for new single-repo adopters?**
   `docs/guides/init-bootstrap.md` documents the current scaffold. Need to
   know whether it already references `[instance.files]` or plugins, and
   what changing it would imply for existing adopters who already bootstrapped.

4. **What's the full schema of `workspace.toml` for a rank-1 single-repo
   workspace (claude.*, env.*, vault.*, files, instance blocks), and what do
   `docs/guides/workspace-config-sources.md` and
   `docs/guides/vault-integration.md` currently document?**
   Need the actual schema and doc content to know what a static reference
   would need to cover, and how much drift risk exists if it's baked into a
   skill rather than generated or linked.

5. **What do commuter and equity-planner's real `workspace.toml` files
   contain, and what kinds of edits have their agents historically needed to
   make (hooks, plugins, secrets, instance files)?**
   Live examples of the actual edit patterns this skill needs to teach --
   grounds the "common edits" content in real usage rather than a
   hypothetical schema walkthrough.

6. **Does the migrate-config skill's structure (SKILL.md conventions,
   install path, how it's invoked) offer a reusable pattern for a new
   config-editing skill in the same embedded plugin?**
   If the new skill lands in the same plugin, its SKILL.md should probably
   follow the same authoring conventions already established by
   `skills/migrate-config/SKILL.md`.
