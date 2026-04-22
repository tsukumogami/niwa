# Explore Scope: workspace-config-sources

## Visibility

Public

## Core Question

What's the right pattern for sourcing git-hosted workspace configuration into
a niwa workspace? Today niwa assumes "the whole repo at the URL is the
config" and clones it as a working tree at `<workspace>/.niwa/`. That model
fails on remote rewrites (issue #72) and forces a separate `dot-niwa` repo
even when the natural home is a subdirectory inside an existing "brain" repo
that already carries the org's planning context (`tsukumogami/vision`,
`codespar/codespar-web`).

The exploration should produce a unified pattern where "whole repo" is the
degenerate case of "subpath of repo," materialization is a disposable
snapshot (no git working tree), the snapshot pulls only the bytes it needs
from remote, and the source location is discovered by convention where
possible. This may crystallize into a PRD before any design.

## Context

- Issue [#72](https://github.com/tsukumogami/niwa/issues/72) reports that
  config and overlay clones develop unrecoverable git divergence on remote
  rewrites because they're full working trees with `git pull --ff-only`.
- The user observes a recurring "central brain repo" pattern: orgs with one
  primary repo carrying `CLAUDE.md`, `.claude/`, `docs/`, planning content,
  and decisions. Two confirmed examples: `tsukumogami/vision` (private,
  pure docs/org/projects content) and `codespar/codespar-web` (private, the
  Next.js app whose root *is* the brain).
- In these setups, niwa workspace config naturally belongs *inside* the
  brain repo, not in a separate `dot-niwa` repo. But materializing the
  whole brain repo into `.niwa/` is wrong on every dimension the user
  flagged: disk size, content bleed (private brain content shouldn't appear
  next to a public workspace), bandwidth on every apply, and the cognitive
  trap of a directory that *looks like* a brain-repo working tree.
- The user prefers convention over configuration: rather than typing
  `owner/repo/path/to/dot-niwa`, niwa should infer the config location from
  patterns (e.g., `niwa.toml` at repo root, `.niwa/` folder at root).
- Whatever model emerges should subsume the current "whole repo" case as
  `subpath = "/"` rather than coexist as a parallel mode.

## In Scope

- Unified source model where whole-repo and subpath-of-repo are the same
  mechanism
- Local materialization model (snapshot vs working tree, partial vs full,
  what `.git` artifact remains)
- Convention for auto-discovering the config location within a referenced
  repo (`niwa.toml`, `.niwa/`, etc.) and the precedence rules
- Fix for issue #72 as a byproduct of the new materialization model
- The same model applied symmetrically to the team config clone and the
  personal overlay clone
- State and identity: how `niwa config set global <slug>` and the workspace
  registry refer to a "subpath@ref" source uniquely
- Migration story for the existing `dot-niwa`-style separate-repo
  workspaces (they should keep working under the unified model)

## Out of Scope

- Workspace.toml schema changes beyond what's needed to express the new
  source model (e.g., a `subpath` field on a source declaration is in
  scope; revisiting `[claude]` block shape is not)
- Vault provider sourcing (already designed; references to git for provider
  metadata could surface but aren't the focus)
- Apply-pipeline behavior after the snapshot is materialized
  (post-materialization consumption is unchanged)
- Multi-tenant / hosted niwa scenarios

## Research Leads

1. **What does niwa do today when sourcing config and overlay clones, end to end?** (lead-current-architecture)
   Audit `internal/workspace/configsync.go`, `internal/workspace/overlaysync.go`,
   the `niwa config set global` persistence path, the workspace registry
   schema, and how `.niwa/` gets created on `niwa init`/`niwa create`. Map
   the full lifecycle: first clone, sync on apply, what's recorded in
   state, what's recoverable on failure. Anchor the redesign in what
   exists, not assumptions.

2. **Which git mechanisms can fetch only a subdirectory of a repo without bringing the rest, and what are their trade-offs?** (lead-partial-fetch-mechanisms)
   Evaluate sparse-checkout, partial clone (`--filter=blob:none`,
   `--filter=tree:0`, cone mode), `git archive` (server-side support
   varies), GitHub REST/GraphQL API directory download (`/contents`,
   tarball endpoint), and `git clone --depth 1 --filter --sparse`
   combinations. Score each against the four costs the user named (disk,
   privacy/content bleed, bandwidth, cognitive temptation), against
   GitHub-only-but-portable concerns, and against authentication
   ergonomics for private repos.

3. **What does a "disposable snapshot" actually look like on disk, and how does it interoperate with subpath sourcing?** (lead-snapshot-shape)
   Once we drop the working-tree model, what's the on-disk artifact?
   Options: a directory with no `.git`, a directory with a one-commit
   shallow `.git`, a content-addressed cache layout, an extracted
   tarball. For each, work out: how does next apply detect "remote
   changed"? How does it handle offline operation? What happens on
   corruption? How does the user inspect what's there without being
   tempted to edit? Specifically rule on whether the same artifact
   shape works for whole-repo and subpath-of-repo cases or whether
   they diverge.

4. **What conventions should niwa adopt for auto-discovering the config location within a brain repo?** (lead-discovery-conventions)
   Candidate signals at the repo root: `niwa.toml`, `.niwa/`,
   `dot-niwa/`, `workspace.toml`. Precedence rules when more than one is
   present. Behavior when none is present (does the repo root itself
   become the config? error with a hint?). How explicit overrides
   coexist with discovery (e.g., `niwa config set global owner/repo`
   discovers; `owner/repo:custom/path` is explicit). What happens if
   discovery would land on something invalid (e.g., `niwa.toml` exists
   but has no `[workspace]` block).

5. **How do peer tools handle git-hosted configuration sourcing, especially subpath cases?** (lead-peer-tool-survey)
   Survey: GitHub Actions reusable workflows
   (`uses: org/repo/.github/workflows/foo@ref` syntax — subpath in URL,
   ref-pinned), Nix flake registry (`git+https://...?dir=foo` query
   param), Helm chart repos (subdirectory chart layout convention),
   Terraform module sources (`git::https://...//subdir?ref=v1`),
   chezmoi (single-repo with `.chezmoiroot` convention),
   dotfiles managers (yadm, stow), Renovate config presets
   (`org/repo:preset-name`). Extract the syntax conventions, the
   resolution rules, the materialization strategies, and what they got
   wrong (where does pain show up in their issue trackers).

6. **How should a config source be uniquely identified across the workspace registry, state, telemetry, and the personal overlay layer?** (lead-identity-and-state)
   Today the registry stores `source_url = "tsukumogami/fake-dot-niwa"`
   (a slug pointing at a whole repo). With subpath sourcing, the
   identity expands to `(host, owner, repo, subpath, ref)`. Work out
   the slug grammar, what's persisted in `~/.config/niwa/config.toml`,
   what's persisted per-instance in state, how the personal overlay's
   `vault_scope` resolution interacts with subpath sources, how
   collisions are detected (two workspaces sourcing the same brain repo
   from different subpaths), and how `niwa status` should display the
   source.

7. **How would the two example brain repos (`tsukumogami/vision`, `codespar/codespar-web`) actually adopt this?** (lead-example-walkthroughs)
   For each repo: where would the `niwa.toml` or `.niwa/` land
   (alongside existing `CLAUDE.md`, `.claude/`, `docs/`)? What content
   moves from a hypothetical separate `dot-niwa` repo into the brain
   repo? What stays separate (if anything)? What's the migration ritual
   for a developer who already has a workspace pointing at
   `org/dot-niwa` (force-update the registry? `niwa config set global
   --migrate`? manual edit)? Surface concrete frictions before they
   become design surprises.
