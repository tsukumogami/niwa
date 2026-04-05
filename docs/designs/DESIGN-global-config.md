---
status: Proposed
upstream: docs/prds/PRD-global-config.md
problem: |
  The niwa apply pipeline loads a single workspace config and materializes it to
  disk. There is no mechanism to layer a second config source on top, so user-specific
  hooks, env vars, plugins, and Claude instructions cannot be managed by niwa without
  committing them to the shared team workspace repo.
decision: |
  Placeholder -- to be filled in Phase 4.
rationale: |
  Placeholder -- to be filled in Phase 4.
---

# DESIGN: Global config

## Status

Proposed

## Context and Problem Statement

The niwa apply pipeline is structured around a single config source: `.niwa/workspace.toml`, synced from a team-owned GitHub repo. The pipeline loads this config, resolves per-repo overrides with `MergeOverrides()`, and materializes results to disk (hooks, env files, settings, CLAUDE.md content, managed files). The whole pipeline assumes one config source and one merge layer.

To support a global config layer, the pipeline needs a second config source -- a user-owned GitHub repo -- that is synced independently and whose values overlay workspace defaults before per-repo overrides are applied. This is an insertion into the middle of the existing apply pipeline, not an extension of its endpoints.

The affected system boundaries are:

- **Config storage** (`internal/config/registry.go`): `GlobalConfig` needs a new section to hold the global config repo URL and local clone path.
- **Sync** (`internal/workspace/configsync.go`): already stateless and reusable; needs to be called for a second directory.
- **Merge** (`internal/workspace/override.go`): a new intermediate layer (workspace → global → per-repo) requires either a new merge function or a generalised merge chain.
- **Apply** (`internal/cli/apply.go`, `internal/workspace/apply.go`): the orchestration sequence gains a sync step and a config-loading step for global config.
- **Instance state** (`internal/workspace/state.go`): a per-instance `SkipGlobal` flag determines whether apply uses the global layer for that instance.
- **CLI** (`internal/cli/`): a new `niwa config` subcommand handles registration and unregistration; `niwa init` gains `--skip-global`.

## Decision Drivers

- **Reuse over new abstractions.** `SyncConfigDir()` already handles git pull with dirty-state checks. The merge machinery in `override.go` already handles all field types. Both should be extended, not replaced.
- **Consistent error semantics.** Global config sync failure must abort apply, same as workspace config sync failure. No degraded-mode fallback.
- **Zero impact on existing workspaces.** Workspaces with no global config registered, or instances initialized with `--skip-global`, must behave identically to today. The feature is completely inert when not configured.
- **Unambiguous merge precedence.** The three-layer order (workspace defaults → global overrides → per-repo overrides) must be explicit in code, not implicit in call order.
- **Testable opt-out.** The `SkipGlobal` flag in instance state must be inspectable without running apply -- i.e., stored in `.niwa/instance.json`, not inferred from the absence of global config registration.
- **Schema parsability.** Global config TOML must map to a subset of existing config types where possible, to avoid duplicating field definitions and merge logic.
