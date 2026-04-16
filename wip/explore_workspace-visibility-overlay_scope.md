## Visibility

Public

# Explore Scope: workspace-visibility-overlay

## Core Question

When a niwa workspace config repo is made public (after vault integration removes secrets), the workspace.toml still exposes private repo names, their group membership, content files, and operational config. We need a convention-based mechanism — something like a `dot-niwa-private` companion repo — that extends a public workspace config with private repo configuration, and is only fetched when the user has access to it.

## Context

PR #52 (vault integration) enables team workspace configs to go public by moving secrets into a vault. But even after secrets leave, a public workspace config reveals what private repos exist and how they're structured. This is an IP and security problem for teams that use niwa to manage private development.

The existing GlobalOverride layer (v0.5.0) handles per-developer personal config but is the wrong layer for team-level private extensions — a team-private companion repo needs to carry team-shared private repo references and configuration.

The user explicitly accepts: selective per-private-repo access (some but not all private repos) is an acceptable tradeoff if the alternative is a complex secondary mechanism. The all-or-nothing access model (can access companion repo = gets all private config; can't = gets none) is OK for v1.

The user has tentatively proposed a `owner/dot-niwa-private` naming convention that pairs with `owner/dot-niwa` (the public config). A hybrid approach is also on the table: put some config in companion repos and some config inside individual private repos.

## In Scope

- Defining what information a public workspace.toml exposes that must be kept private
- Designing a convention-based private extension mechanism for workspace config
- Merge semantics between public config and private extension
- Graceful degradation when the private extension repo is inaccessible
- Discovery mechanism: how niwa finds the private extension without configuration
- Impact on workspace.toml schema and apply pipeline
- Access control: GitHub visibility as the access gate

## Out of Scope

- Secrets/vault (covered by PR #52)
- Per-developer personal config (existing GlobalOverride)
- Selective per-repo access within a private extension (accepted tradeoff: all-or-nothing)
- GitHub variables as a config placement mechanism (user noted this is not recommended)

## Research Leads

1. **What exactly does a public workspace.toml expose about private repos, and which fields are the highest-risk?**
   Audit the full workspace.toml schema (sources, groups, content, repos, hooks, env) to identify every field that can reference or describe a private repo. Understanding the attack surface is prerequisite to designing the right split between public and private configs.

2. **How does the existing GlobalOverride layer (merge chain, type system, apply pipeline) constrain or enable a new "private workspace extension" layer?**
   The GlobalOverride design was built as the third merge layer. A private extension would be a second layer (between workspace and global). Understand what changes to the merge chain, type definitions, and apply pipeline would be needed, and whether GlobalOverride's patterns can be reused directly.

3. **What naming conventions and discovery mechanisms do analogous tools use for public/private config splits?**
   Look at git's include/includeIf, dotfiles managers (chezmoi, yadm), home-manager, and CI config tools (e.g., .github-private) that split configuration across public/private repos. Extract what conventions and fallback behaviors work well and which don't.

4. **What are the GitHub API behaviors when niwa tries to clone or sync an inaccessible private extension repo?**
   Graceful degradation requires understanding exactly how GitHub returns 401/404 for repos the user can't access, and how niwa's existing SyncConfigDir/Cloner handles these cases. This determines whether degradation logic needs new code or can reuse existing error handling.

5. **What is the minimal schema change to workspace.toml to support a private extension, and what are the compatibility constraints?**
   Should the public workspace.toml declare the private extension explicitly (a `private_extension` field), or should the naming convention be the only mechanism? Explore the tradeoffs between zero-config convention and explicit opt-in fields, considering that some teams may not want any private extension at all.

6. **What are the real-world edge cases around mixed public/private workspaces: repos in the private extension only, repos in both, and repos visible in the public config that should be hidden?**
   The private extension will need to add repos (sources, groups, content) that aren't in the public config. But there may also be cases where a repo should appear in neither public config nor be discoverable. Map out the state space to find edge cases that could break the merge model or leak info.
