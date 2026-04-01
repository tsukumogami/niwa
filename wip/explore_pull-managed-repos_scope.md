# Explore Scope: pull-managed-repos

## Visibility

Public

## Core Question

How should niwa keep managed workspaces fresh after initial creation? This covers
two dimensions: syncing workspace TOML changes (new/removed repos) and pulling
latest code from remotes for existing repos. The primary driver is ensuring Claude
sessions see current code when exploring sibling repos.

## Context

niwa currently skips repos that already exist during `apply`. There's no mechanism
to pull latest changes or reconcile workspace config changes short of deleting and
recreating the workspace. The user wants a low-friction, non-destructive way to
keep workspaces current. Edge case handling (dirty repos, non-default branches)
needs a considered UX with sensible defaults and override options.

## In Scope

- Pulling latest from remote for managed repos
- Handling repos with local changes (uncommitted work)
- Handling repos on non-default branches
- Syncing workspace TOML changes (newly added or removed repos)
- UX design: command structure, flags, default behavior
- Workflow recommendation (separate command vs part of apply vs both)

## Out of Scope

- Force-pushing or rewriting remote history
- Managing non-git repos
- Workspace-level hooks or CI integration
- Changes to how initial clone works (beyond adding pull capability)

## Research Leads

1. **How does niwa's current apply pipeline handle repos, and where would sync logic fit?**
   Understanding the existing clone/skip flow is essential to decide whether pull
   belongs inside apply or alongside it.

2. **What do other workspace/monorepo managers do to keep repos fresh?**
   Tools like Google's repo, meta, devcontainers, and similar multi-repo managers
   have solved this. Their UX patterns can inform niwa's approach.

3. **What's the right command UX: subcommand, flag on apply, or both?**
   The user wants something low-friction. Need to evaluate whether a separate
   `niwa sync` command, a flag like `--pull` on apply, or automatic behavior
   during apply is the right fit.

4. **How should dirty repos and non-default branches be handled?**
   Need to map the state space (clean/dirty x default-branch/other-branch) and
   propose default behavior + override flags for each state.

5. **What git operations are needed and what are their failure modes?**
   Fast-forward pull, fetch+merge, rebase -- each has different failure modes.
   Need to pick the safest default and understand what can go wrong.

6. **How should workspace TOML drift be reconciled (added/removed repos)?**
   If a repo is added to the TOML, apply already clones it. But what about
   removed repos? Should niwa warn, delete, or ignore?
