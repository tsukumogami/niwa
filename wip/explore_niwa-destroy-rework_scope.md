# Explore Scope: niwa destroy rework

## Visibility

Public

## Core Question

How should `niwa destroy` behave so it's contextual to where it's run — destroying
the enclosing instance from inside, offering a picker (or wiping the entire
workspace under `--force`) from the root, and using the existing shell-wrapper
landing-path protocol to drop the user out of any directory it just deleted?
What's the safest, least-surprising UX for the workspace-self-destroy path?

## Context

Today `niwa destroy [name]` accepts an optional name from any cwd
(`internal/cli/destroy.go:34`, `internal/workspace/destroy.go:20`). With no
arg it walks `cwd` up via `DiscoverInstance`. With a name, it enumerates
instances under the workspace root and matches. `--force` skips the
uncommitted-changes guard only. The command refuses to destroy the workspace
root and never emits a landing path, so the user's shell stays inside a
deleted directory.

Niwa already has a shell-wrapper landing-path protocol (`NIWA_RESPONSE_FILE`
in `internal/cli/landing.go`, called from `internal/cli/create.go:181`)
used by `niwa create` and `niwa go`. Reusing it for destroy is the natural
mechanism for "land the user at the workspace root."

The user has confirmed the high-level shape, the lax definition of "empty
workspace," and the workspace-self-destroy guardrail (scan every instance
for non-pushed work, including worktrees; if dirty, list and ask for typed
confirmation; if clean, delete silently).

## In Scope

- Routing matrix for `niwa destroy [name]` based on `(cwd, args, --force)`.
- Interactive picker at the workspace root when no name is provided and at
  least one instance exists.
- Workspace-self-destroy path (`--force`, no name, at the root) including a
  comprehensive non-pushed-work scan across instances and git worktrees.
- Landing-path emit via `NIWA_RESPONSE_FILE` only when the enclosing
  directory is destroyed (destroy-from-inside, workspace-self-destroy,
  empty-workspace destroy).
- Shell wrapper update if needed to honor `destroy`'s landing path.
- `@critical` Gherkin coverage for the create/destroy lifecycle in
  `test/functional/features/`.

## Out of Scope

- Per-instance destroy semantics with `--force` (today's "skip dirty guard"
  meaning is preserved for the named-instance and from-inside cases).
- Broader git-state detection in commands other than destroy.
- Recovering destroyed instances (no undo, no soft-delete).
- Tying destroy into mesh/daemon lifecycle beyond the existing
  `TerminateDaemon` call.

## User-Confirmed Decisions

- Empty-workspace destroy needs no `--force` and uses the lax definition: no
  instance directories present; remaining files/dirs (`.niwa/`, stray
  artifacts) are deleted along with the workspace.
- Workspace-self-destroy under `--force` scans every instance for non-pushed
  work and inside git worktrees. Clean → delete silently. Dirty → print the
  affected instances, branches, and worktrees, then require typed
  confirmation.
- Landing-path emit applies only when the enclosing dir is destroyed:
  destroy-from-inside (lands at workspace root), workspace-self-destroy
  (lands at workspace parent), empty-workspace destroy (lands at workspace
  parent). Not for `niwa destroy <name>` from the root.
- `niwa destroy <name>` is only valid from the workspace root. From inside
  an instance, providing a name is rejected.
- Per-instance destroy keeps today's `--force` semantics (skip uncommitted-
  changes guard) without extending to the broader non-pushed-work check.

## Research Leads

1. **Where is the tsuku picker, and how do we reuse it?**
   The user referenced "the picker we recently built for tsuku." Locate it in
   `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/tsuku/`. Determine
   how it's packaged, what its dependencies are, and whether niwa can import
   it as a Go module, vendor a helper, or needs a fresh implementation.
   Recommend the lowest-friction reuse path that doesn't introduce a
   tsuku→niwa coupling we'll regret.

2. **Does the niwa shell wrapper auto-honor `NIWA_RESPONSE_FILE` for any
   subcommand, or does it whitelist `create`/`go`?**
   Read `internal/cli/shell_init.go` and the shell function it emits.
   Determine what (if any) wrapper changes are needed so `niwa destroy`
   landing paths are honored. The tactical answer drives whether this is
   a single-PR change or also touches the wrapper.

3. **Map every code path, helper, and test that touches today's destroy.**
   Cover `internal/cli/destroy.go`, `internal/workspace/destroy.go`,
   `internal/workspace/destroy_test.go`, completion (`internal/cli/
   completion.go`), and the daemon-termination integration. Identify
   what guarantees we must preserve (e.g., `TerminateDaemon`,
   `ValidateInstanceDir`) and what helpers are reusable for the picker
   and workspace-self-destroy paths.

4. **How does niwa present pickers, prompts, and destructive operations
   today?**
   Survey existing UX in `niwa create`, `niwa apply`, `niwa go`, etc.
   Describe how confirmations, list outputs, and warnings are rendered so
   destroy can stay consistent. Specifically look for any prompt helpers
   we can reuse for typed confirmation.

5. **What does "non-pushed work" mean in practice, including across git
   worktrees?**
   Beyond `git status --porcelain`: detect unpushed commits (branches with
   no upstream or commits ahead of upstream), untracked files,
   stashes, and the same state inside any git worktrees the instance
   owns. Define the data shape we'll show the user in the typed-confirmation
   prompt. Note any cost concerns (worktree enumeration is per-repo, every
   instance can have many repos).

6. **Which existing PRDs reference niwa destroy or workspace lifecycle, and
   what needs updating?**
   Inventory the 12 PRDs in `docs/prds/`. The likely candidates are
   `PRD-shell-integration.md` (covers the NIWA_RESPONSE_FILE protocol and
   the wrapper's command whitelist), `PRD-niwa-init-creates-workspace-dir.md`
   (workspace creation lifecycle), and `PRD-mesh-session-lifecycle.md`
   (daemon shutdown on destroy). Grep across all PRDs for destroy- and
   lifecycle-relevant requirements. Output: per-PRD list of affected
   requirement IDs (e.g., R6, R14) with a one-line note on what needs to
   change. Whether we update PRDs in this branch or in a follow-up is a
   crystallize-time decision.
