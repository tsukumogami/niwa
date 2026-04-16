# Design Summary: workspace-visibility-overlay

## Topic

Workspace overlay — a secondary `workspace-overlay.toml` config layer that adds
private/supplemental repos and content on top of a shared base workspace config,
discovered by convention (`<base-repo>-overlay`) at init time.

## Source PRD

`docs/prds/PRD-workspace-visibility-overlay.md` (status: In Progress)

## Decisions Made

Three independent decisions were resolved during codebase investigation:

1. **WorkspaceOverlay struct** — new dedicated type (not reuse of WorkspaceConfig or
   extension of GlobalOverride). Parses `workspace-overlay.toml` with additive
   sources/groups/repos/content sections plus override hooks/settings/env/files.

2. **Clone storage** — XDG home keyed by URL (`$XDG_CONFIG_HOME/niwa/overlays/<org>-<repo>/`).
   Mirrors GlobalConfigDir pattern. Multiple instances sharing same overlay URL share
   one clone. OverlayURL stored in instance.json, derived at runtime via OverlayDir().

3. **Content append** — OverlaySource field on ContentRepoConfig (TOML-hidden, toml:"-").
   Set by MergeWorkspaceOverlay when overlay content entry uses `overlay` field. Read
   by InstallRepoContent to append overlay file content to generated CLAUDE.local.md.

## New Components

- `internal/config/overlay.go` — WorkspaceOverlay struct, ParseOverlay(), OverlayDir(), deriveOverlayURL()
- `internal/workspace/configsync.go` (extended) — CloneOrSyncOverlay()
- `internal/workspace/override.go` (extended) — MergeWorkspaceOverlay()
- `internal/workspace/workspace_context.go` (extended) — InstallOverlayClaudeContent()
- `internal/config/config.go` — ContentRepoConfig.OverlaySource field
- `internal/workspace/state.go` — InstanceState.OverlayURL, InstanceState.NoOverlay
- `internal/workspace/apply.go` — runPipeline steps 2.5, 2.6
- `internal/cli/init.go` — --overlay, --no-overlay flags

## Implementation Phases

1. InstanceState schema + init flags
2. WorkspaceOverlay config type + merge
3. Apply pipeline integration
4. Content generation + CLAUDE injection

## Security Review (Phase 5)

**Outcome:** Option 2 — Document considerations
**Summary:** Design is architecturally sound. Convention discovery supply chain risk upgraded to HIGH. Three mitigations added: print overlay URL at init, store OverlayCommit SHA in instance.json, document proactive overlay repo creation. ParseOverlay() gains two explicit validation requirements: hook script paths must be validated as relative before MergeWorkspaceOverlay resolves them; [files] destinations must reject .claude/ and .niwa/ targets. Design updated to reflect these.

## Current Status

**Phase:** 6 - Final Review
**Last Updated:** 2026-04-16
