# Design Summary: global-config

## Input Context (Phase 0)

**Source PRD:** docs/prds/PRD-global-config.md
**Problem (implementation framing):** The niwa apply pipeline assumes a single config source (workspace.toml); adding global config requires a new sync step, a new intermediate merge layer, a new per-instance opt-out flag in instance state, and a new `niwa config` subcommand for registration.

## Decisions (Phase 2)

**D1 — Global config representation and merge chain:** Option B chosen — new `GlobalOverride` struct mirroring `RepoOverride`. Two new functions: `ResolveGlobalOverride` and `MergeGlobalOverride`. Merge semantics: hooks append, env vars global-wins, plugins union (not replace), files global-wins with empty-string suppression.

**D2 — niwa config subcommand:** Option A chosen — `niwa config set/unset global` subcommand tree. Three new files: `config.go`, `config_set.go`, `config_unset.go`. Mirrors existing action-verb top-level command pattern.

**D3 — CLAUDE.global.md injection:** Option A chosen — copy file + `@import` directive. New `InstallGlobalClaudeContent` function in `workspace_context.go`. Mirrors `InstallWorkspaceContext` exactly. Tracked in `writtenFiles`, cleaned by `cleanRemovedFiles`.

## Security Review (Phase 5)

**Outcome:** Option 2 — Document considerations
**Summary:** The design is architecturally sound. Two medium-severity implementation gaps were documented: (1) hook script source directory resolution when two config repos are merged must use absolute path rewriting at merge time; (2) `ParseGlobalConfigOverride` must include parse-time path-traversal validation on the `files` map. A minor file permission improvement (0o600 for config.toml) was also noted. No design changes required.

## Current Status

**Phase:** 5 - Security (complete)
**Last Updated:** 2026-04-04
