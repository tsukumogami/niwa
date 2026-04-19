---
complexity: simple
complexity_rationale: Config schema additions and TOML round-trip tests only. No behavioral change, no new execution paths, no security boundary touched.
---

## Goal

Add `read_env_example *bool` opt-out fields to the workspace-level and per-repo config structs, and a resolver helper that downstream issues can call to determine whether `.env.example` materialization is enabled for a given repo.

## Context

`.env.example` integration (Phase 1 of the design) requires a new opt-out flag at two config levels: a workspace-wide default in `WorkspaceMeta` and a per-repo override in `RepoOverride`. Using `*bool` (pointer) makes the three states distinguishable — `nil` (unset, inherit), `true` (explicitly on), and `false` (explicitly off) — so a per-repo `true` can override a workspace-wide `false` and vice versa.

This issue delivers the schema additions and the resolver helper that Phase 4 wires into the pre-pass. Nothing here changes runtime behavior; `.env.example` is not read until Phase 4.

Design: `docs/designs/current/DESIGN-env-example-integration.md`

## Acceptance Criteria

- [ ] `WorkspaceMeta` in `internal/config/workspace.go` gains `ReadEnvExample *bool` with TOML tag `read_env_example`; godoc states nil means true (opt-out default).
- [ ] `RepoOverride` in `internal/config/workspace.go` gains `ReadEnvExample *bool` with TOML tag `read_env_example`; godoc states nil means inherit the workspace setting.
- [ ] `effectiveReadEnvExample(ws *config.WorkspaceConfig, repoName string) bool` is added in `internal/config/` (or `internal/workspace/`); it resolves workspace default then per-repo override using nil-pointer semantics, returning `true` when both are nil.
- [ ] The function signature accepts `*config.WorkspaceConfig` and `repoName string` so Issue 4 can call it as `effectiveReadEnvExample(ctx.Config, ctx.RepoName)` without requiring additional fields on `EffectiveConfig`.
- [ ] TOML round-trip test: workspace `read_env_example = false`, no per-repo override — `effectiveReadEnvExample` returns `false`.
- [ ] TOML round-trip test: workspace `read_env_example = false`, per-repo `read_env_example = true` — `effectiveReadEnvExample` returns `true` (per-repo override wins).
- [ ] TOML round-trip test: neither workspace nor per-repo field set (all nil) — `effectiveReadEnvExample` returns `true` (default-on).
- [ ] TOML round-trip test: workspace `read_env_example = true`, per-repo `read_env_example = false` — `effectiveReadEnvExample` returns `false` (per-repo suppression wins).
- [ ] Existing `config_test.go` round-trip tests continue to pass (`go test ./internal/config/...`).

## Dependencies

None

## Downstream Dependencies

Issue 4 (pre-pass integration in `EnvMaterializer.Materialize`) requires:

- `ReadEnvExample *bool` present on both `WorkspaceMeta` and `RepoOverride` so TOML parsing populates the fields from `workspace.toml`.
- `effectiveReadEnvExample(ws *config.WorkspaceConfig, repoName string) bool` callable at the top of the pre-pass as `effectiveReadEnvExample(ctx.Config, ctx.RepoName)` to gate whether `.env.example` discovery runs for a given repo.
