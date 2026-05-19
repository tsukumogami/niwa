# Explore Scope: init-bootstrap-empty-source

## Visibility

Public

## Core Question

When a user runs `niwa init <name> --from <org/repo>` against a remote that
exists but has no `.niwa/` (e.g. a freshly created GitHub repo intended to
become a niwa-managed workspace), niwa fails. What should niwa do instead so
the bootstrap can complete in one step? The user's preferred shape: detect
the missing config, scaffold a minimal-but-ideal `.niwa/workspace.toml`,
stage the change on a branch using niwa's existing worktree session
mechanism, and surface the worktree path so the user can inspect and decide
whether to push.

## Context

- Pain comes from a real workflow: user created `dangazineu/commuter` on
  GitHub and wants to manage it as a niwa workspace with vault-backed
  secrets and plugin installation. Today they must clone the repo outside
  the workspace, hand-author `.niwa/workspace.toml`, push, and only then
  run `niwa init` for real.
- Today's `niwa init` has three modes (`modeScaffold`, `modeNamed`,
  `modeClone`). The clone path calls `workspace.MaterializeFromSource`,
  which is where the failure surfaces when the remote has no config.
- `niwa init <name>` without `--from` already scaffolds locally, but it
  doesn't record a source URL in the registry or know anything about the
  remote — that's why the user reached for `--from` and hit the wall.
- niwa already has a worktree-backed session mechanism
  (`docs/guides/sessions.md`) the user wants to reuse: stage the
  scaffolded config in a branch inside a worktree, print the location,
  and let the user inspect and push when ready.
- Broken/malformed/inaccessible remotes are out of the primary use case
  but the user wants them researched and handled differently (likely
  fail-loud with a remediation hint rather than auto-scaffold).

## In Scope

- Detecting "remote exists but lacks `.niwa/workspace.toml`" during
  `niwa init --from <empty-remote>`
- Designing the minimal-ideal scaffold contents (what fields to fill,
  what to leave as commented examples)
- Plugging into niwa's worktree session mechanism to stage the change
  on a branch rather than auto-pushing
- CLI surface: flag, mode, prompt, or transparent fallback
- Treatment of related failure modes (malformed config, missing
  workspace.toml inside an existing `.niwa/`, private/inaccessible
  remotes) — proposed handling, not full implementation
- Confirmation UX (when does niwa ask before scaffolding?)

## Out of Scope

- Adopting an existing workspace that already has `.niwa/` (already
  handled by today's clone path)
- Pushing the scaffolded branch to the remote automatically — user
  explicitly wants the worktree-handoff pattern instead
- Reworking the existing scaffold template across all modes (only the
  bootstrap path is in scope; if the same template is reused, that's a
  natural consequence, not a goal)
- Creating the GitHub repo on the user's behalf (assumed to already
  exist)

## Research Leads

1. **How does today's `niwa init --from` fail when the source has no `.niwa/`,
   and what signals does it observe at that point?** (lead-failure-mode)
   Need to know the exact failure point (HTTP 404, file-not-found, parse
   error, materialize error) so we know where the fallback would plug in
   and what classification logic distinguishes "empty source" from other
   failures.

2. **What is the minimal-ideal `.niwa/workspace.toml` for a brand-new
   workspace?** (lead-minimal-scaffold)
   The user wants a proposed minimal scaffold. Investigate what
   `workspace.Scaffold` produces today, what fields a freshly-created
   workspace actually needs to be functional (workspace name, sources,
   groups, vault stub, plugin entries), what can be auto-derived (name
   from positional arg, source URL from `--from`), and what should be
   commented examples vs. real defaults. Look at how other niwa workspaces
   in this org are configured for reference.

3. **How does niwa's worktree session mechanism work, and what's the
   integration surface for `init`?** (lead-worktree-integration)
   The user wants to stage the scaffold inside a niwa worktree session.
   Read `docs/guides/sessions.md`, the session/worktree code, and figure
   out the right entry point for `init` to call. Identify any
   assumptions sessions make about an already-applied workspace (since
   `init` happens before `apply`) and how to bridge the gap.

4. **What CLI surface fits best — a new flag, a new mode, or transparent
   fallback inside `--from`?** (lead-cli-surface)
   Compare options: (a) extend `--from` to silently fall back on empty
   source, (b) add `--bootstrap` / `--create-config` flag, (c) add a new
   `niwa init --adopt` mode, (d) prompt the user. Look at how other CLI
   tools handle the analogous "initialize from empty remote" pattern
   (`gh repo create --clone`, `cargo new --vcs git`, `terraform init`,
   `git init` in an existing dir). Recommend the option that fits niwa's
   existing UX conventions.

5. **What other failure modes should `init` handle, and how should each
   be treated differently from the empty-repo case?** (lead-other-failures)
   Per the user, research and propose handling for: (a) remote has
   `.niwa/` but `workspace.toml` is malformed; (b) remote has `.niwa/`
   but `workspace.toml` is missing entirely; (c) remote is private and
   the user lacks credentials; (d) remote 404 or does not exist; (e)
   remote returns rank-2 legacy layout. For each, recommend: auto-scaffold,
   fail-loud with hint, prompt, or unchanged. Goal is to scope these out
   of the main feature cleanly while ensuring they're not made worse by
   it.

6. **What confirmation UX does the user encounter when materialize fails
   and niwa offers to bootstrap?** (lead-confirmation-ux)
   Should this fallback happen silently, behind a prompt, only with an
   explicit flag, or only inside a worktree session that the user can
   inspect before acting? Think about non-interactive contexts (CI,
   scripted setup, agent-driven workflows) and how each option degrades.
   Cross-reference niwa's existing confirmation patterns
   (`--rebind` warnings, vault bootstrap pointers, rank-2 notices).

## Status

Phase 1 complete. Leads identified. Proceeding to Phase 2 (Discover).
