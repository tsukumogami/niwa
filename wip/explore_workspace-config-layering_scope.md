# Explore Scope: workspace-config-layering

## Visibility

Public

## Core Question

How should niwa support a personal config layer -- a user-owned GitHub repo that overlays on top of the shared team workspace config? The personal config needs to be portable (synced across machines), registered once per machine, and applied automatically unless the user opts out.

## Context

Today, niwa workspaces are backed by a GitHub repo containing all config: hooks, env vars, plugins, secrets, and preferences. This makes workspaces hard to share across teams because personal preferences and user-specific secrets are mixed with shared config. The user wants a layered model: a shared team workspace (existing) plus a personal config layer (new) that follows the user across machines. The personal config repo can hold global preferences (across all workspaces) and named per-workspace overrides. Registration happens once per machine, stored in `~/.config/niwa/config.toml`. Personal config is pulled at `niwa apply` time. A flag (working name: `--no-personal-config`) at `niwa init` lets users opt out per workspace.

## In Scope

- Personal config repo registration in `~/.config/niwa/config.toml`
- Schema for personal config (global section + per-workspace override sections)
- Merge/overlay semantics (personal wins on conflict; list vs scalar merge behavior)
- Personal config sync at `niwa apply` time
- `niwa init --no-personal-config` opt-out flag (name TBD, better name needed)
- How niwa identifies the current workspace when applying personal overrides

## Out of Scope

- Machine-specific (host-local only) config -- secondary concern, not the primary flow
- New credentials or secret storage infrastructure
- Changes to how shared workspace config works today

## Research Leads

1. **How does the current workspace config sync work, and where does personal config pull fit?**
   Understand `configsync.go` and the `apply` command flow to know where a personal config sync step hooks in and what a parallel pull mechanism looks like.

2. **What should the personal config schema look like?**
   How to structure a file (or files) with both global-personal settings and named per-workspace overrides in one repo. How does niwa identify which workspace it is so it can select the right section?

3. **What are the right merge semantics?**
   Which fields are user-overridable (hooks, env, plugins, CLAUDE.md content) vs shared-only? How do list fields (hooks, plugins) merge vs scalar fields (settings, vars)? What wins when both layers define the same key?

4. **What's the registration and discovery UX?**
   Command to register the personal config repo once. Config key and storage in `~/.config/niwa/config.toml`. What `niwa init` does differently when a personal config is registered vs not. Better name for `--no-personal-config` flag.

5. **How do comparable tools handle layered config?**
   Git's global/local `.gitconfig`, SSH `~/.ssh/config` includes, direnv, and similar tools have solved multi-layer config merging -- worth knowing which patterns transferred well and which didn't.

6. **Is there evidence of real demand for this, and what do users do today instead?** (lead-adversarial-demand)

You are a demand-validation researcher. Investigate whether evidence supports
pursuing this topic. Report what you found. Cite only what you found in durable
artifacts. The verdict belongs to convergence and the user.

## Visibility

Public

Respect this visibility level. Do not include private-repo content in output
that will appear in public-repo artifacts.

## Six Demand-Validation Questions

Investigate each question. For each, report what you found and assign a
confidence level.

Confidence vocabulary:
- **High**: multiple independent sources confirm
- **Medium**: one source type confirms without corroboration
- **Low**: evidence exists but is weak
- **Absent**: searched relevant sources; found nothing

Questions:
1. Is demand real? Look for distinct issue reporters, explicit requests, maintainer acknowledgment.
2. What do people do today instead? Look for workarounds in issues, docs, or code comments.
3. Who specifically asked? Cite issue numbers, comment authors, PR references.
4. What behavior change counts as success? Look for acceptance criteria, stated outcomes.
5. Is it already built? Search the codebase and existing docs for prior implementations or partial work.
6. Is it already planned? Check open issues, linked design docs, roadmap items.
