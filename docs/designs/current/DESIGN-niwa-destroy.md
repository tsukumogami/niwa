---
status: Planned
problem: |
  `niwa destroy` today accepts an instance name from any cwd and refuses to
  destroy the workspace itself. This makes the command ergonomically blunt —
  there's no contextual awareness of "destroy what I'm in," no picker for
  ambiguous cases at the workspace root, no path to delete an empty workspace,
  no shell-wrapper-driven `cd`-out-of-deleted-dir, and no comprehensive
  unpushed-work guardrail when an entire workspace is wiped. The rework adds
  contextual mode selection, a picker UX, a workspace-wipe path under
  `--force`, and shell-wrapper landing-path emission for cases where destroy
  removes the user's enclosing directory.
---

# DESIGN: niwa destroy

## Status

Planned

## Context and Problem Statement

`niwa destroy [instance]` (`internal/cli/destroy.go`,
`internal/workspace/destroy.go`) accepts an optional instance name from any
cwd. With no arg, it walks cwd up via `DiscoverInstance` to find an enclosing
instance. With a name, it enumerates instances under the workspace root and
matches. `--force` skips the uncommitted-changes guard (a single
`git status --porcelain` run per cloned repo). `ValidateInstanceDir` refuses
to destroy a workspace root by checking for `.niwa/workspace.toml`.

Three friction points motivate the rework:

1. **No contextual awareness.** `niwa destroy <name>` works from any cwd,
   including from inside another instance. From inside an instance, the
   user's intent is almost always "destroy this one I'm in," but the command
   accepts (and acts on) an unrelated name. Users typing the name of the
   wrong instance can destroy the wrong thing.

2. **No picker.** From the workspace root with no arg, today's command
   walks cwd up looking for an instance, doesn't find one, and errors.
   The user has to enumerate instances in their head and re-run with a
   specific name. A picker is the natural UX.

3. **No path to remove the workspace itself, or land outside it.** The
   workspace directory is left behind even after every instance is destroyed.
   And destroying the cwd's enclosing instance strands the user's shell in a
   deleted directory because `niwa destroy` doesn't participate in the
   `NIWA_RESPONSE_FILE` shell-wrapper protocol that `create` / `go` / `init`
   / `session create` already use.

In addition, today's `--force` only governs the uncommitted-changes guard. A
workspace-wipe operation deserves a stronger gate: a per-instance, per-repo
scan that detects unpushed commits, stashes, and the same state inside any
git worktrees the instance owns (niwa itself creates session worktrees under
`<instance>/.niwa/worktrees/`).

## Decision Drivers

Drawn from exploration findings and exploration decision blocks (see
`wip/explore_niwa-destroy-rework_findings.md` and `_decisions.md`):

- **Preserve `niwa reset`.** All four destroy helpers
  (`ResolveInstanceTarget`, `ValidateInstanceDir`, `CheckUncommittedChanges`,
  `DestroyInstance`) are shared with reset. The rework must land as additive
  sibling helpers, never as in-place edits. Reset's behavior must not
  silently change.
- **Preserve `ValidateInstanceDir`'s "refuses workspace root" invariant.**
  The new "wipe whole workspace" path under `--force` must NOT loosen this
  validator. It needs a separate sibling helper (`DestroyWorkspace`) with
  its own safety checks.
- **Reuse the existing landing-path protocol.** `NIWA_RESPONSE_FILE` and the
  `__niwa_cd_wrap` shell helper handle the deleted-cwd case correctly today
  (synchronous `cd` before prompt redraw, guarded by `[ -d ... ]` to avoid
  cd-ing to a missing path). The wrapper's command whitelist is the only
  thing that needs to grow — destroy is currently outside it.
- **Niwa has zero interactive prompts today.** The picker and typed-
  confirmation will be the first. Both must adopt niwa's existing
  conventions (stderr for interactive surfaces, stdout for the final
  summary line, lowercase-verb `fmt.Errorf`, `; use --force to override`
  wording, `hintShellInit` after success) and establish exactly two new
  patterns: a `term.IsTerminal(os.Stdin.Fd())` check and the picker/
  prompt helper.
- **Worktree scanning is mandatory in the workspace-wipe non-pushed-work
  scan.** Niwa creates session worktrees under
  `<instance>/.niwa/worktrees/<repo>-<session-id>/` via
  `internal/mcp/handlers_session.go:188`. Without scanning them, an active
  session's branch could vanish silently.
- **Sub-2s detector cost on realistic workspaces.** The detector runs ~5
  git plumbing commands per repo (`status --porcelain`,
  `for-each-ref refs/heads`, `stash list`, `worktree list --porcelain`,
  conditional detached-HEAD check). At the existing `cloneWorkers=8`
  parallelism (`apply.go:1093-1140`), 15 repos finishes in <2s — fine for
  an interactive prompt.
- **PRD/design-doc surface is small but non-trivial.** Three PRDs need
  amendments (`PRD-shell-integration` R1/R11/D3, `PRD-cross-session-
  communication` R38/AC-P11, `PRD-workspace-config-sources` line 1001)
  and three design docs need touch-ups
  (`DESIGN-instance-lifecycle` Decision 4,
  `DESIGN-shell-navigation-protocol` cd-eligible list,
  `DESIGN-contextual-completion` Decision 3). A new `PRD-niwa-destroy.md`
  is also warranted as the canonical home for the picker UX, contextual
  mode selection, and wrapper cd-out-of-deleted-dir requirements.

## Decisions Already Made

These decisions are exploration outputs and should be treated as constraints,
not reopened during design:

### From the user (settled before research)

- Trailed-off "These are probably the only" sentence in the original
  ask was an accident; ignore.
- Empty workspace + `niwa destroy` (no arg) deletes the workspace without
  `--force`, lands user one level up.
- Non-empty workspace + `niwa destroy` (no arg) shows a picker.
- `niwa destroy <name>` is only valid from the workspace root, never from
  inside an instance.
- Per-instance destroy keeps today's `--force` semantics (skip
  uncommitted-changes guard). The broader non-pushed-work check is
  workspace-self-destroy only.
- Empty-workspace definition is **lax**: no instance directories present is
  sufficient.
- Workspace-self-destroy under `--force` scans every instance for non-pushed
  work, including across git worktrees. Clean → delete silently. Dirty →
  list affected instances/branches/worktrees and require typed confirmation.

### From research (Round 1)

- **Picker reuse**: copy `tsuku/internal/tui/picker.go` (and `sanitize.go`,
  tests) into `niwa/internal/tui/`. The `internal/` location blocks module
  import; copy is cheaper than reorganizing tsuku for cross-module export.
  API: `Pick(prompt, []Choice) (int, error)` + `IsAvailable()` +
  `ErrCanceled`. Single dep is `golang.org/x/term` which niwa already
  requires.
- **Wrapper change**: extend `internal/cli/shell_init.go:54` from
  `create|go|init)` to `create|go|init|destroy)`. Update the two
  golden-string assertions in `internal/cli/shell_init_test.go`. Protocol
  primitives need no changes.
- **New helper `internal/workspace/scan.go`** for the comprehensive
  non-pushed-work detector. Types: `LossKind`, `Loss`, `RepoScan`,
  `InstanceScan`. Existing `CheckUncommittedChanges` stays untouched
  (reset still uses it).
- **New helper `internal/workspace/destroy_workspace.go`** for the
  `DestroyWorkspace(workspaceRoot)` path. `ValidateInstanceDir` stays
  strict.
- **Sequential workspace-wipe ordering** (alphabetical by instance name).
  Output races at a confirmation prompt are worse than 5s × N wall-clock;
  realistic N is small.
- **Typed-confirmation prompt fires BEFORE `writeLandingPath`.** A user
  hitting ESC must not be `cd`-ed away from a workspace they didn't
  actually destroy.
- **Confirmation token is the workspace name** (override-aware via
  `EffectiveConfigName`). Industry convention (GitHub, Heroku, Stripe)
  and defeats muscle-memory through fixed strings.

### From the lightweight decision protocol (auto-mode, Round 1)

- **Reset stays out of scope.** Destroy and reset diverge on contextual
  semantics; helpers stay shared.
- **Single-instance picker is skip-and-go.** With exactly one instance,
  destroy directly (still subject to today's dirty-repo gate).
- **PRD-shell-integration R2 cleanup is out of scope** for this PR.
- **No confirmation prompt** when destroying from inside an instance
  with no arg. Today's silent behavior preserved; new prompts apply only
  to new branches.

## Routing Matrix (settled)

| cwd            | args                            | --force | behavior                                                   | shell cwd lands |
|----------------|---------------------------------|---------|------------------------------------------------------------|-----------------|
| inside instance | none                           | any     | destroy enclosing instance                                 | workspace root  |
| inside instance | `<name>`                       | any     | reject — name only valid from workspace root               | unchanged       |
| workspace root | `<name>`                        | as today | destroy named instance                                     | unchanged       |
| workspace root | none, ≥2 instances              | absent  | interactive picker → destroy chosen                        | unchanged       |
| workspace root | none, 1 instance                | absent  | destroy that instance directly                             | unchanged       |
| workspace root | none, 0 instances (empty)       | absent  | destroy the entire workspace                               | workspace parent |
| workspace root | none, ≥1 instance               | present | scan all instances; clean → wipe silently; dirty → list & require typed confirm | workspace parent |
| outside both   | any                             | any     | error (today's behavior)                                   | unchanged       |

## Out of Scope

- Reset rework (separate follow-up if/when desired).
- Recovering destroyed workspaces (no undo, no soft-delete).
- Network access for the unpushed-work scan (no `git fetch`; trust local
  remote-tracking refs).
- Submodule recursion (informational "N submodules not scanned" only).
- PRD-shell-integration R2 stale-stdout cleanup (separate doc PR).

## References

- Exploration scope: `wip/explore_niwa-destroy-rework_scope.md`
- Round 1 findings: `wip/explore_niwa-destroy-rework_findings.md`
- Round 1 decisions: `wip/explore_niwa-destroy-rework_decisions.md`
- Round 1 research:
  - `wip/research/explore_niwa-destroy-rework_r1_lead-tsuku-picker-reuse.md`
  - `wip/research/explore_niwa-destroy-rework_r1_lead-shell-wrapper-coverage.md`
  - `wip/research/explore_niwa-destroy-rework_r1_lead-current-destroy-surface.md`
  - `wip/research/explore_niwa-destroy-rework_r1_lead-niwa-ux-patterns.md`
  - `wip/research/explore_niwa-destroy-rework_r1_lead-non-pushed-work-detection.md`
  - `wip/research/explore_niwa-destroy-rework_r1_lead-prd-impact.md`
- Existing PRDs to amend: `docs/prds/PRD-shell-integration.md`,
  `docs/prds/PRD-cross-session-communication.md`,
  `docs/prds/PRD-workspace-config-sources.md`
- Companion PRD to write: `docs/prds/PRD-niwa-destroy.md` (Proposed)
- Existing design docs to amend:
  `docs/designs/current/DESIGN-instance-lifecycle.md`,
  `docs/designs/current/DESIGN-shell-navigation-protocol.md`,
  `docs/designs/current/DESIGN-contextual-completion.md`
