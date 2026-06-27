---
status: Done
problem: |
  niwa cannot place a verbatim-named, non-gitignored file at the workspace root
  or at an instance root. The per-repo [files] mechanism forces a .local infix
  and targets repos only; the [instance.files] field is parsed and merged but
  never materialized; and no workspace-root file table exists in the schema.
  A tool config that loads by an exact filename, like a Claude Code .mcp.json,
  therefore cannot be delivered to sessions started at those non-repo levels.
goals: |
  Let a workspace author declare files in workspace config that niwa
  materializes verbatim -- exact name, no .local infix -- at the workspace root
  and at every instance root, on apply and on instance creation, tracked for
  drift and cleanup like other managed files. The motivating outcome is a
  workspace-wide Claude Code MCP server delivered through config alone.
upstream: docs/briefs/BRIEF-mcp-root-instance-distribution.md
---

## Status

Done

## Problem Statement

niwa materializes managed configuration at three levels: each repo it clones,
the instance root that contains those repos, and the workspace root that
contains the instances. The repos get a general file-copy surface, the `[files]`
table, but it does two things that make it unusable for the non-repo levels.
First, it runs only in the per-repo materializer set, so it never executes when
niwa materializes a workspace root or an instance root. Second, it rewrites
every destination filename to carry a `.local` infix (`config.json` ->
`config.local.json`) so the file matches the `*.local*` pattern a managed repo
gitignores. That rewrite is correct for repos and is explicitly out of scope to
change.

For the two non-repo levels, niwa offers no working equivalent. The instance
root has a field that looks like one -- `[instance.files]` -- but it is dead:
the value is parsed into config and merged into the effective instance config,
yet every code path that materializes the instance root reads only the Claude
settings and plugin fields and never the files field, so the table produces no
output. The workspace root has nothing at all: the schema has no
workspace-root file-distribution table.

These non-repo levels are not git repositories. The workspace root and instance
root hold managed repos as subdirectories, each with its own `.git`, while the
container directories themselves are untracked (niwa already maintains an
instance-root `.gitignore` carrying the `*.local*` pattern for any outer tree
the instance may be nested in, but the container itself is not a tracked repo).
Because there is no repo gitignore at these levels for a `.local` infix to
satisfy, the infix has no purpose here -- and it actively breaks files that must
keep an exact name. Claude Code loads a project MCP config from `.mcp.json` in
the directory a session starts in; renamed to `.mcp.local.json` the tool never
reads it. The result is that a workspace author who wants the same MCP server
available in sessions launched at the workspace root or any instance root must
hand-place the file in every instance and replace it whenever a new instance is
created. niwa's own file-distribution design anticipated verbatim names
("explicit destination filenames bypass renaming," citing tool-specific config
files that need exact names); the implementation diverged to always insert
`.local`, and no surface delivers a verbatim file to the non-repo levels.

## Goals

- A workspace author can declare, in workspace config, a file to be materialized
  verbatim at the instance-root level and a file to be materialized verbatim at
  the workspace-root level, using exact destination names with no `.local`
  rewrite.
- niwa materializes those files at the right level when it applies a workspace
  and when it provisions a new instance, so existing and future instances both
  receive them without manual copying.
- The materialized files are tracked as niwa-managed (drift detection and
  cleanup) consistent with other managed files at those levels.
- The per-repo `[files]` behavior, including its `.local` rewrite, is unchanged.
- The motivating use case works end to end: a declared `.mcp.json` lands
  verbatim at the workspace root and at each instance root, and a Claude Code
  session started at either level sees the configured MCP server.

## User Stories

### Terminology

- **Workspace root** -- the top-level directory niwa manages, containing one or
  more instance directories. Not a git repository.
- **Instance root** -- a single managed sandbox directory under the workspace
  root, containing cloned repos as subdirectories. Not a git repository.
- **Verbatim distribution** -- copying a file to a destination using the exact
  filename the author wrote, with no `.local` infix inserted.
- **Per-repo `[files]`** -- the existing file-copy mechanism that runs inside
  each managed repo and inserts a `.local` infix; unchanged by this feature.

- As a workspace author, I want to declare a file once in workspace config and
  have niwa place it verbatim at every instance root, so that every instance
  exposes the same tool configuration without my copying it into each one.
- As a workspace author, I want to declare a file that niwa places verbatim at
  the workspace root, so that a session started at the workspace root sees the
  same configuration the instances do.
- As a developer handed a freshly provisioned instance, I want a declared
  `.mcp.json` already present at the instance root under its real name, so that
  my Claude Code session loads the configured MCP server with no setup.
- As a workspace author, I want these distributed files tracked and cleaned up
  by niwa like other managed files, so that removing an entry from config
  removes the file on the next apply rather than leaving an orphan.
- As a maintainer, I want the per-repo `[files]` `.local` behavior to stay
  exactly as it is, so that adding non-repo distribution does not change how
  files land inside managed repos.

## Requirements

### Functional

- **R1** -- Workspace config MUST provide a declarative surface for files
  distributed to the **instance root**. This activates the existing
  `[instance.files]` field (or a successor surface decided in the DESIGN) so it
  produces materialized files.
- **R2** -- Workspace config MUST provide a declarative surface for files
  distributed to the **workspace root**. The schema currently has no such table;
  this feature adds one.
- **R3** -- Files distributed through the instance-root and workspace-root
  surfaces MUST be materialized **verbatim**: the destination filename the
  author wrote is used as-is, with no `.local` infix inserted.
- **R4** -- niwa MUST materialize the workspace-root files during the
  workspace-root materialization path and the instance-root files during the
  instance-root materialization path, so the files appear when niwa applies the
  workspace.
- **R5** -- A new instance MUST receive the declared instance-root files as part
  of being provisioned, so instances created after the config is set still get
  the files.
- **R6** -- **Instance-root** distributed files MUST be recorded as
  niwa-managed so that drift detection and cleanup apply: removing an
  `[instance.files]` entry and re-applying removes the previously materialized
  file, using the same managed-file tracking the instance root already has.
  **Workspace-root** distributed files MUST be re-materialized
  (overwrite-idempotent) on every apply, matching how the other root-managed
  files behave; removal-cleanup at the workspace root is NOT required by this
  feature (see Known Limitations) because the workspace root has no managed-file
  state store.
- **R7** -- Destinations MUST resolve to the project root of their level (the
  workspace root or the instance root). The feature MUST NOT write into protected
  directories such as `.claude/` or `.niwa/`.
- **R8** -- The per-repo `[files]` mechanism, including its `.local` rewrite and
  its repo-only placement, MUST be unchanged.
- **R9** -- Source paths for distributed files MUST be validated to stay within
  the trusted config directory (the same containment boundary the existing
  materializers enforce); path traversal MUST be rejected.

### Non-functional

- **R10** -- The instance-root and workspace-root surfaces SHOULD reuse the
  existing config merge and override semantics (workspace defaults, empty-string
  removal) so the new tables behave like the file/env/settings tables authors
  already know.
- **R11** -- The feature MUST keep niwa's non-repo levels invisible to any outer
  git tree they may be nested in -- it MUST NOT regress the existing instance-root
  `.gitignore` handling -- while still delivering the file under its verbatim
  name inside the (untracked) container directory.
- **R12** -- Behavior MUST be covered by tests at the level niwa already tests
  materialization (unit tests for merge and materialization; a functional
  scenario for the apply/create paths where the project conventions call for one).

## Acceptance Criteria

- [ ] Declaring an instance-root file entry and running apply produces the file
      at each instance root under its exact destination name (no `.local`
      infix).
- [ ] Declaring a workspace-root file entry and running apply produces the file
      at the workspace root under its exact destination name (no `.local`
      infix).
- [ ] A `.mcp.json` declared for the instance-root level is present and
      verbatim-named at the root of a newly provisioned instance.
- [ ] A Claude Code session started at the workspace root or an instance root
      that carries a materialized `.mcp.json` sees the configured MCP server
      (manual or scripted verification of the motivating case).
- [ ] Removing a previously declared `[instance.files]` entry and re-running
      apply removes the materialized file at the instance root (cleanup works
      through the existing managed-file tracking).
- [ ] A workspace-root file is re-materialized on every apply and is
      overwrite-idempotent (same bytes when config is unchanged); removal-cleanup
      at the workspace root is explicitly not asserted.
- [ ] A source path that escapes the config directory (e.g. via `..`) is
      rejected rather than copied.
- [ ] Per-repo `[files]` distribution still inserts `.local` and still targets
      repos only; existing per-repo file tests pass unchanged.
- [ ] All new and existing unit tests pass, and the functional suite passes
      where a scenario is added.

## Out of Scope

- Per-repo `.mcp.json` distribution and any change to the per-repo `[files]`
  `.local` infix or its repo-only placement. The repo level is untouched.
- Covering sessions started inside a managed repo subdirectory (see Known
  Limitations) -- this feature covers the workspace root and instance root only.
- Defining the exact config table shape and the materialization call-site wiring
  -- those are DESIGN decisions. This PRD fixes the behavior, not the mechanism.
- Any change to how Claude Code itself discovers or trusts MCP servers beyond
  what niwa can emit through its existing settings/file surfaces.

## Known Limitations

- **No ancestor walk-up for repo-subdir sessions.** Claude Code loads
  `.mcp.json` from the directory a session starts in and does not walk up to an
  ancestor directory. A file materialized at the workspace root or instance root
  therefore covers sessions started *at* those levels, but not sessions started
  *inside* a managed repo subdirectory. This is an accepted limitation: the
  requester wants coverage at the workspace root and instance root, and per-repo
  coverage is a separate concern handled by the (unchanged) per-repo mechanism.
  The feature names this boundary rather than solving it.

- **No removal-cleanup at the workspace root.** The workspace root has no
  managed-file state store -- its existing managed files (`settings.json`,
  `CLAUDE.md`, root skills) are overwrite-idempotent and never auto-removed.
  Workspace-root distributed files follow the same model: re-written every apply,
  but not deleted when their `[root.files]` entry is removed. Instance-root files
  do not share this gap (they are tracked and cleaned). Adding a workspace-root
  managed-file store is a possible follow-up; it is out of scope here.

- **MCP trust prompt may remain.** Depending on how Claude Code gates project
  MCP servers, a per-session trust prompt may still appear the first time a
  session loads the materialized `.mcp.json`. Whether niwa must emit an
  additional setting (for example to pre-trust project MCP servers) to suppress
  that prompt, or whether an existing permissions setting already suffices, is a
  question the DESIGN evaluates. If a settings change is needed and does not fit
  this feature, it is recorded as an explicit follow-up rather than silently
  assumed solved.

## Decisions and Trade-offs

This section closes the upstream BRIEF's open framing questions.

- **Activate the dead surface rather than invent a parallel one.** The
  instance-root need is served by making `[instance.files]` actually
  materialize, because the field already exists, is parsed, and is merged --
  only the materialization read is missing. Inventing a differently-named
  instance surface would orphan the existing field and break any config that
  already sets it. The DESIGN may rename or restructure for clarity, but the
  default is to give the existing field an effect.

- **Verbatim at non-repo levels, `.local` at repo levels.** The `.local` infix
  is a repo-gitignore accommodation. At the non-repo levels there is no repo
  gitignore for it to satisfy, and the motivating files require exact names, so
  these surfaces distribute verbatim. Keeping the repo behavior untouched avoids
  destabilizing the one mechanism that is working as designed. The trade-off is
  two distinct naming behaviors across levels; this is acceptable because the
  levels are genuinely different (tracked repo working tree vs. untracked
  container) and the difference is explainable in one sentence.

- **Destinations stay at the project root.** Restricting destinations to the
  workspace/instance project root (not `.claude/` or `.niwa/`) keeps the feature
  aligned with how the motivating tool discovers config (project-root
  `.mcp.json`) and avoids writing into directories niwa treats as protected
  internal state. Authors who need files elsewhere are not served by this
  feature; that constraint is intentional for the first version.
