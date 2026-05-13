# Explore Scope: embedded-niwa-config

## Visibility

Public

## Scope

Tactical

## Core Question

What's the smallest set of changes that lets niwa read its workspace
configuration from a subdirectory of a general-purpose repo (e.g. `.niwa/`)
instead of requiring the entire repo to be the config, so single-repo
workspaces become viable, while either keeping or sunsetting the existing
`dot-niwa` model, and letting the same mechanism compose with multi-repo
"brain repo" workspaces?

## Context

- **North-star goal:** make it possible to add niwa config to a single-repo
  workspace, so a developer with one repo can adopt niwa without standing up
  a second repo just for configuration.
- **Today's model:** niwa expects `--from owner/repo` to point at a repo whose
  *entire* content is the workspace config (i.e. `dot-niwa`). The full repo is
  cloned into an XDG snapshot at `<xdg>/niwa/overlays/<repo>/` and used as the
  source of truth. This is too heavy for general-purpose repos.
- **User's leanings:**
  - Embedded config likely lives at `.niwa/` (or similar known subdir).
  - `--from` flag *hopefully* doesn't need new syntax.
  - The overlay mechanism *hopefully* stays the same shape.
  - The same pattern should compose with multi-repo workspaces where one
    repo (e.g. a "brain" / `vision` repo) carries niwa config alongside
    other content.
  - **Consolidation is on the table:** rather than coexisting with the
    `dot-niwa` convention, we should consider migrating *all* niwa workspaces
    (including existing `dot-niwa` repos) to the new layout, so there's one
    convention instead of two.
- **Recent precedent:** PR #138 (issue #137) removed an unconditional filter
  that excluded the overlay repo from workspace classification. The XDG
  snapshot remains the source of truth for niwa config; a working copy in
  the workspace is treated as just another repo. Embedded config builds on
  that boundary.

## In Scope

- Config retrieval mechanism (sparse checkout, tarball, `git archive`,
  per-file fetch, etc.).
- Config layout convention (`.niwa/` subdir, alternative paths, manifest
  pointer).
- `--from` flag semantics and backwards compatibility.
- Overlay (`<repo>-overlay`) composition under embedded config.
- Single-repo workspace end-to-end shape (`niwa init`, `apply`, `session
  create`).
- Brain-repo multi-repo composition (where a workspace component is *also*
  the config source).
- Migration / consolidation strategy (coexist, opt-in, or migrate-all).

## Out of Scope

- Recipe schema or action-system changes in the `tsuku` package manager.
- Session lifecycle changes once the workspace is set up.
- Vault provider architecture.
- Telemetry or observability changes.

## Research Leads

1. **Where should embedded niwa config live in a general-purpose repo, and
   should the convention be a fixed path or discoverable?** Compare a fixed
   `.niwa/` convention against alternatives (`niwa/`, `.config/niwa/`, a
   top-level manifest like `niwa.toml` pointing elsewhere, multiple
   acceptable paths). What does each option imply for discoverability, for
   collision risk in busy repos, and for how a user signals "this repo has
   niwa config"?

2. **How can niwa fetch only the config subdirectory from a remote repo,
   without cloning the entire tree?** Survey the realistic options — git
   sparse-checkout (full or partial clone), `git archive --remote`, the
   GitHub Repository Contents API, the GitHub tarball/zipball API with a
   prefix filter, raw-file URL fetches. For each: auth requirements
   (token vs SSH), bandwidth and latency at typical repo sizes, offline
   behavior, snapshot integrity (whole-tree consistency vs per-file), and
   complexity to implement in Go.

3. **What changes does the `--from` flag need to support embedded config,
   and what stays backwards compatible?** Today `--from owner/repo` implies
   "the whole repo is the config." Evaluate the option space: auto-detect
   embedded config when `.niwa/` exists, explicit subdir syntax
   (`--from owner/repo:.niwa` or `--from owner/repo#path`), a separate
   flag (`--config-path`), or accept a local-path form
   (`--from ./.niwa`). Which options work in combination, which break
   existing CLI invocations, what does the help text look like?

4. **How does the overlay (`<repo>-overlay`) mechanism compose with
   embedded config?** Trace the current overlay flow end-to-end (clone
   into XDG, merge with base in Step 0.6, classify as a workspace repo
   per PR #138). Then ask: when base config is at
   `general-repo/.niwa/`, where does the personal overlay live —
   in its own dedicated repo (status quo), at `general-repo-overlay/.niwa/`,
   at `personal-overlay-repo/.niwa-overlay/`, or somewhere else? What
   does each option do to the snapshot layout and the merge boundary?

5. **What does a single-repo workspace look like end-to-end, from `niwa
   init` through `apply` to `session create`?** Walk through the user
   experience with one repo that is *also* the config source. Where does
   the workspace instance root live relative to the repo working copy?
   Does the repo get cloned twice (once for the XDG snapshot, once as a
   workspace component) or does the snapshot point into the working copy?
   What does the on-disk layout look like? What does
   `niwa session create <repo> <purpose>` reference?

6. **What does the "brain repo" multi-repo composition look like — where
   one workspace component is also the config source?** A user has, say,
   `tsukumogami/vision` (the brain repo) and three other workspace
   components. The config lives at `vision/.niwa/`. Trace the same
   walk-through as Lead 5 but for the multi-repo case. Same clone-twice
   question. Does Step 0.6 merge differ? Does the snapshot need to know
   "this config repo is also a workspace component"?

7. **What's the migration and consolidation strategy with existing
   `dot-niwa` setups?** Three branches to evaluate side by side:
   (a) **Coexistence** — both root-level config (legacy `dot-niwa`) and
       `.niwa/` subdir (new) work; niwa auto-detects which mode applies
       per source repo.
   (b) **Opt-in** — legacy continues by default; new mode requires an
       explicit flag or config switch.
   (c) **Consolidation** — there is only one convention going forward;
       existing `dot-niwa` repos are migrated (manually or with tooling)
       so their config moves under a `.niwa/` subdir. The legacy
       root-level mode is removed.
   For each: user impact (do existing workspaces keep working without
   action?), implementation complexity, long-term carrying cost in the
   codebase, footgun risk (what if both root-level and `.niwa/` content
   exist?), and what a migration tool / guide would need to look like.
