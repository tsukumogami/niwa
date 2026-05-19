# Design Summary: init-bootstrap-empty-source

## Input Context (Phase 0)

**Source:** /shirabe:explore handoff

**Problem:** `niwa init <name> --from <empty-remote>` fails when the
remote exists but has no `.niwa/workspace.toml`. Users adopting a
freshly-created GitHub repo as a niwa-managed workspace must today
clone outside the workspace, hand-author the config, push, and only
then run init. The design must specify how a bootstrap fallback plugs
into the existing init flow, what CLI surface gates it, what the
scaffolded config looks like, and what new primitive handles the
worktree handoff — all while preserving the existing init failure
contract and the case-specific handling of adjacent failure modes
(malformed config, auth errors, 404, rank-2).

**Constraints (from exploration):**

- **Empty-source-only scope.** Adjacent failure modes get fail-loud
  hints, not auto-scaffold. Rank-2 already works, out of scope.
- **Worktree handoff is the only confirmation gate.** No automatic
  push from niwa.
- **Niwa proposes the minimal-ideal scaffold non-interactively.** No
  in-flight prompts for vault/plugins/marketplaces.
- **Trigger requires explicit `--bootstrap` flag.** TTY without the
  flag prompts. Non-TTY without the flag fails fast with a remediation
  hint. Auto-fallback on `NoMarkerError` was rejected because of
  GitHub's 404 ambiguity (empty / missing / private-without-creds all
  return 404) and the typo-resolves-to-different-empty-repo risk.

**Key technical anchors from exploration:**

- Plug point: `internal/cli/init.go:265` (branch on
  `config.IsNoMarker(err)` before the generic wrap; disarm the
  workspace-dir cleanup defer at `init.go:221-225`).
- New primitive: `workspace.StageInWorktree` (~30 lines: branch +
  worktree + commit). Existing session API (`handleCreateSession`) is
  the wrong shape — it gates on apply-produced state.
- Minimal scaffold derived from `--from` inputs: `[workspace]` (name,
  content_dir), active `[[sources]] org = "<derived>"`, active
  `[groups.<vis>] visibility = "<from GitHub repos/get>"`, commented
  `[claude.content.workspace]`, plus one schema doc link. Drops
  `default_branch`. No pre-wired vault/plugins/marketplaces.
- Adjacent failure modes need typed sentinels in
  `workspace/preflight.go` and a typed status error in
  `internal/github/fetch.go` so init can dispatch on error class.

## Current Status

**Phase:** 0 - Setup (Explore Handoff)

**Last Updated:** 2026-05-18

**Open design questions carried forward** (resolved during /shirabe:design):

- G1: worktree location on disk
- G2: registry-write timing in the bootstrap path
- G3: zero-commit (truly empty) repos vs. auto-init repos
- G4: commit-on-branch vs. leave working-tree dirty
- G5: typed-error refactor scope (v1 vs. follow-up)
- G6: interaction with `--overlay` / `--no-overlay` and
  `--install-plugins`
