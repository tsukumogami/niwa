# Design Summary: niwa-destroy-rework

## Input Context (Phase 0)

**Source:** /shirabe:explore handoff (auto-continued via /decision framework)

**Problem:** `niwa destroy` lacks contextual awareness, a picker for ambiguous
cases at the workspace root, a path to remove an empty workspace or wipe a
non-empty one with `--force`, and shell-wrapper-driven `cd`-out-of-deleted-
dir. The rework adds these behaviors while preserving today's per-instance
destroy semantics and `niwa reset`'s shared helpers.

**Constraints (from exploration):**

- Reset stays out of scope; share-helpers must not be edited in place.
- `ValidateInstanceDir`'s "refuses workspace root" invariant must be
  preserved; workspace-wipe uses a separate sibling helper.
- The picker is copied (not imported) from `tsuku/internal/tui/picker.go`
  into a new `niwa/internal/tui/` package.
- The shell wrapper extension (`internal/cli/shell_init.go:54`) is a
  one-line change with two golden-string test updates.
- The non-pushed-work detector (new `internal/workspace/scan.go`) must scan
  git worktrees because niwa creates session worktrees under
  `<instance>/.niwa/worktrees/`.
- Workspace-wipe destroys instances sequentially in alphabetical order.
- Typed-confirmation fires BEFORE `writeLandingPath`.
- Confirmation token is the override-aware workspace name.
- Single-instance picker case is skip-and-go.
- No confirmation prompt for the inside-an-instance no-arg path
  (today's silent behavior preserved).
- Picker writes to stderr; final summary line stays on stdout.

## Current Status

**Phase:** 0 - Setup (Explore Handoff)
**Last Updated:** 2026-05-07
**Mode:** --auto (decision protocol applies)
