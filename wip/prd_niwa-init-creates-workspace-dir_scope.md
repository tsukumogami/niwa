# /prd Scope: niwa-init-creates-workspace-dir

## Problem Statement

Users initializing a niwa workspace from a remote config must currently
pre-create the target directory and `cd` into it before running `niwa init`.
The flow is `mkdir foo && cd foo && niwa init foo --from org/repo` instead of
the natural `niwa init foo --from org/repo`. Compounding the issue, when an
explicit name argument is provided, niwa only persists it in the global
registry — `niwa status`, `niwa apply`, and other commands continue to read
the cloned `[workspace] name` from the toml, leaving the user-given name
visible only to `niwa go`. This makes onboarding noisier than necessary and
creates a surprising disconnect between what the user typed and what niwa
shows.

## Initial Scope

### In Scope

- Behavior change: `niwa init <name>` (with or without `--from`) creates
  `<cwd>/<name>/` and initializes the workspace inside it.
- Pre-flight conflict detection: error if `<cwd>/<name>` already exists, for
  any pre-existing path type (file, directory, symlink).
- Name precedence: when a positional `<name>` is given, that name is the
  effective workspace name everywhere (status, apply output, registry,
  on-disk path), overriding the cloned `[workspace] name` from `--from`.
- `niwa init` with no positional name (including `niwa init` and
  `niwa init --from <src>`): unchanged — still initializes in `cwd`, name
  defaults / derives from cloned config.
- `niwa go <name>` lands the user in the new directory after init (already
  works via the registry's `Root` field once it points to the new dir).
- Help text and documentation updates reflecting the new flow.
- Test coverage for the new behavior, including a `@critical` Gherkin
  scenario per project convention.

### Out of Scope

- Extending the same "create-the-folder" convention to `niwa create`. It
  shares the papercut but is a separate concern; deferred to a follow-up
  issue.
- An `--in-place` / `--here` escape hatch for users who want the old
  behavior. Pre-1.0 status; clean breaking change.
- Migration tooling for existing scripts that pre-create the dir. The new
  error message is the migration aid.
- Re-syncing or refreshing the cloned `.niwa/` from its source repo after
  init (not changed by this PRD).
- Naming validation rules (allowed characters, length limits). These are
  whatever the existing registry already enforces; this PRD doesn't add new
  rules.

## Research Leads

1. **User-facing error message and remediation guidance**
   When `<cwd>/<name>` already exists, the error must clearly tell the user
   what happened and how to fix it. What's the right level of specificity?
   Should the message differentiate file vs. directory vs. symlink? What
   does the existing niwa error vocabulary look like (other `InitConflict`
   messages)? Worth investigating to keep the new error consistent with the
   tool's existing voice.

2. **Naming and path edge cases from the user's perspective**
   What happens when `<name>` contains characters the filesystem doesn't
   like (slashes, spaces, leading dots)? Relative paths with subdirs (`niwa
   init foo/bar`) — supported, error, or out of scope? Absolute paths
   (`niwa init /tmp/foo`) — same question. Surfacing what's plausible from
   user input so the PRD can either include or explicitly exclude these.

3. **Discoverability and migration for users who already know the old
   flow**
   How will existing users (those who learned `mkdir + cd + niwa init`)
   discover the new behavior? Help text, README, error message wording for
   the "directory exists" case (which is what they'll hit when they run the
   old pattern). Goal: make the error itself a learning moment, not a
   stumbling block.

## Coverage Notes

The exploration phase already established the implementation shape (option
(b) `InstanceState` override, caller-side preflight, error sentinel). The
PRD captures the user-facing requirements and acceptance criteria, not the
implementation. Decisions and trade-offs from explore (why option b, why
clean-break, why niwa-create deferred) feed into the PRD's Decisions and
Trade-offs section so they're not re-litigated downstream.

The 6 coverage dimensions:
- **Who is affected**: niwa users initializing new workspaces — primarily
  users following docs that say `niwa init <name> --from <src>`, plus the
  user who initiated this exploration who currently uses
  `niwa init --from <src>` (no name).
- **Current situation**: `niwa init` always materializes in cwd; explicit
  `<name>` only flows to the global registry.
- **What's missing**: directory creation when a name is given;
  override-everywhere semantics for the explicit name.
- **Why now**: tactical UX papercut, low cost to fix, aligns with niwa's
  declarative-by-name design.
- **Scope boundaries**: see In/Out of Scope above.
- **Success criteria**: see Goals derivation in Phase 3 (single-command
  init, no mkdir+cd, name shown consistently across `niwa go`/`status`/
  `apply`).
