# Explore Scope: niwa-init-creates-workspace-dir

## Core Question

When `niwa init <name>` is invoked with an explicit positional `<name>`, should
niwa create a new directory `<cwd>/<name>/` and initialize the workspace inside
it, instead of initializing in `cwd` (the current behavior)? The explicit name
should also become the workspace name, overriding whatever the `--from` config
declares.

## Context

Today `niwa init` always materializes the workspace in `cwd`. To get a
workspace at `/home/foo`, the user must `mkdir foo && cd foo && niwa init ...`.
The asymmetry is awkward because `niwa go foo` already abstracts away the path
— the user shouldn't need to think about directories at init time either.

Existing init modes (from `internal/cli/init.go`):
- `niwa init` — scaffold in cwd, name defaults to "workspace"
- `niwa init <name>` — scaffold in cwd; if `<name>` is registered, clone instead
- `niwa init --from <src>` — clone in cwd, name derived from cloned config
- `niwa init <name> --from <src>` — clone in cwd, register `<name>` mapping

The user's stated intent: when a positional `<name>` is supplied, both the
folder and the workspace name come from `<name>`. Modes invoked **without** a
positional `<name>` (including their current `niwa init --from <src>` workflow)
keep today's in-cwd behavior.

## In Scope

- Folder creation behavior for all `niwa init <name>` invocations (with or
  without `--from`)
- Reconciling explicit `<name>` with the cloned config's `[workspace] name`
  field (explicit name wins)
- Pre-flight conflict detection for the new target directory (error if it
  already exists)
- Registry entry's `Root` pointing to the newly created directory so
  `niwa go <name>` lands in the right place
- Doc/test/example updates for the changed UX

## Out of Scope

- `niwa init` with no positional name (including `niwa init --from <src>`):
  unchanged, still inits in cwd
- `niwa create`, `niwa apply`, `niwa go` internals: only registry-side
  consequences (Root field) are in scope; their command UX is not
- An `--in-place` / `--here` escape hatch: niwa is pre-1.0, the new behavior is
  the natural default, no flag-based opt-out
- Migrations or deprecation period: clean cut

## Decisions Already Made

These were settled in the scoping conversation and don't need further research:

- **Trigger:** Folder creation only when a positional `<name>` is given.
  No-name modes stay in cwd (preserves current `niwa init --from` workflow).
- **Conflict policy:** Error if `<cwd>/<name>` already exists. No "use if
  empty" or "reuse existing" paths.
- **Backward compat:** Pre-1.0, breaking change is fine. Users who were doing
  `mkdir foo && cd foo && niwa init foo --from src` get a clear error
  ("foo/foo already exists" or similar) and switch to the new pattern.

## Research Leads

1. **How should the explicit `<name>` override the cloned config's `[workspace]
   name` field?** The user wants explicit `<name>` to dictate the workspace
   name regardless of what `--from` declares. Options worth comparing:
   (a) rewrite the cloned `.niwa/workspace.toml` in place — but this leaves the
   workspace dirty against its source-repo HEAD, which complicates future
   sync/refresh; (b) persist the override in `.niwa/state.json` (instance
   state) and apply at runtime — keeps source repo clean but introduces a
   second source of truth for "what is this workspace named?"; (c) only
   override the registry-side name (status quo behavior when `<name>` is
   given), leaving `[workspace] name` as-is — simplest, but `niwa status`
   would show a different name than the registry. Need to understand how
   downstream code (status, apply, mesh, mcp) reads the workspace name to
   pick the right approach.

2. **What conflict semantic should the pre-flight check enforce on the new
   target dir?** Today `workspace.CheckInitConflicts(cwd)` checks for
   `.niwa/workspace.toml`, orphan `.niwa/`, and nested-instance scenarios.
   With the new flow, the target dir `<cwd>/<name>` doesn't exist yet (in
   the happy path). We need to define: error on any pre-existing path at
   that location? Only on non-empty dirs? Does "nested instance" still need
   checking, but now from `<cwd>/<name>` upward instead of `cwd` upward?
   How does this compose with the existing sentinel errors and
   `InitConflictError` shape?

3. **What ripple effects hit other niwa commands and infrastructure?**
   `niwa create [workspace-name]` exists and may share the "must mkdir
   first" papercut — should the same convention extend? `niwa go <target>`
   reads `Root` from the registry, so it gets the new path for free, but
   are there other commands that derive workspace location differently
   (mesh, session, mcp_serve, status)? Any assumptions that workspace root
   == cwd at init time?

4. **Test and documentation fallout.** What functional Gherkin scenarios in
   `test/functional/features/` currently exercise the `init <name>` path
   from cwd? What unit tests in `internal/cli/init_test.go` and
   `internal/workspace/preflight_test.go` need updates? Are there guides
   (`docs/guides/`), READMEs, or website examples on tsuku.dev that show
   the `mkdir + cd + init` pattern and need to be updated to the new flow?
