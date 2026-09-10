---
complexity: testable
complexity_rationale: Adds one persisted state field and a pure resolver carried through the existing pipeline result; it feeds a later permission decision but grants nothing on its own, and real-Create tests can fully verify it.
---

## Goal

Record the instance's resolved permission posture (`"bypass"`, `"ask"`, or empty) in `InstanceState` during every instance `Create` and `Apply`, without changing any generated settings document.

## Context

`niwa dispatch` currently decides whether to forward `--permission-mode bypassPermissions` by reading `permissions.defaultMode` back out of the instance-root `.claude/settings.json`. That's the same value a later issue in this plan stops writing, and a hand-edited or deleted settings file can change the decision. The fix is to have dispatch read the posture from niwa's own state file, `.niwa/instance.json`, instead. This issue builds the producer for that: it resolves the instance's effective posture during materialization and persists it. Nothing reads the new field yet.

The instance pipeline already holds the effective config (`effectiveCfg`, returned by `ResolveAndMergeEffectiveConfig` in `runPipeline`), and the instance-root settings materializer reads its posture from `MergeInstanceOverrides(effectiveCfg)`. That's the same precedence the PRD calls the instance's effective posture: the workspace overlay, then the workspace, then the personal overlay, then `[instance.claude.settings]`, with the highest winning. Per-repo overrides don't affect it. The pipeline already carries facts into state through `pipelineResult`, and `Create` and `Apply` copy `result.shadows` and `result.trustKeys` into `InstanceState`. This issue adds one more fact carried the same way.

The resolver doesn't depend on the materializer's mapping table, so it builds against today's materializer. Validation stays in the materializer: `RootSettingsMaterializer` rejects an invalid value before the pipeline saves state, so an unrecognized value never reaches `InstanceState`. The field stores niwa's own vocabulary (`bypass`, `ask`), never a Claude Code mode string and never a vault-resolved plaintext.

Design: `docs/designs/DESIGN-inert-defaultmode-key.md` (Implementation Approach, Phase 1; Solution Architecture > Components, "Instance posture resolver" and "Persisted posture")

PRD: `docs/prds/PRD-inert-defaultmode-key.md`

## Acceptance Criteria

- [ ] `InstanceState` in `internal/workspace/state.go` has a field `ClaudePermissions string` with JSON tag `json:"claude_permissions,omitempty"`. Its doc comment says it holds the declared posture the instance-root settings document resolved from (`"bypass"`, `"ask"`, or empty), that it's niwa's own record rather than a Claude Code mode string, and that only `niwa dispatch` reads it, for the instance it just provisioned.
- [ ] A new function `instancePermissionsPosture(cfg *config.WorkspaceConfig) string` in `internal/workspace`, in a new file beside `materialize.go`, reads `MergeInstanceOverrides(cfg).Claude.Settings["permissions"]` through `maybeSecretString`. It returns `"bypass"` or `"ask"` when the value is exactly one of those, and `""` otherwise, including when the key is absent. It never returns any other string and it doesn't return an error.
- [ ] Unit tests for `instancePermissionsPosture` show it returns `""` for an absent key, `""` for an unrecognized value such as `"bypassPermissions"`, `"bypass"` and `"ask"` for those literals, and the `[instance.claude.settings]` value when it and the workspace `[claude.settings]` value differ.
- [ ] `pipelineResult` in `internal/workspace/apply.go` has a `claudePermissions string` field. `runPipeline` fills it from `instancePermissionsPosture(effectiveCfg)` and returns it in the same `pipelineResult` literal that sets `shadows` and `trustKeys`.
- [ ] Both `Create` and `Apply` set `ClaudePermissions: result.claudePermissions` in the `InstanceState` they build, at the sites that copy `result.shadows` and `result.trustKeys`. `Apply` takes the value from the current run's result, never from `existingState`.
- [ ] Go tests run a real `Applier.Create` in a temporary workspace (following `TestApplyPersistsShadowsInState` in `internal/workspace/apply_vault_test.go`), load the result with `workspace.LoadState(instanceRoot)`, and check `ClaudePermissions` for each of these declarations:
  - workspace `[claude.settings] permissions = "bypass"` records `"bypass"`
  - workspace `permissions = "ask"` records `"ask"`
  - no declaration records `""`, and the raw `instance.json` contains no `claude_permissions` key
  - workspace `bypass` with `[instance.claude.settings] permissions = "ask"` records `"ask"`
  - workspace `ask` with `[instance.claude.settings] permissions = "bypass"` records `"bypass"`
  - workspace `bypass` with `[repos.<name>.claude.settings] permissions = "ask"` records `"bypass"` (per-repo overrides don't affect the instance posture)
  - no workspace declaration, with a personal overlay (`applier.GlobalConfigDir` set to a directory whose `niwa.toml` declares `permissions = "bypass"` for the workspace) records `"bypass"`
- [ ] A Go test runs `Create` with workspace `bypass`, then changes the declaration to undeclared and runs `Apply` on the same instance. The reloaded state then records `""`. The value is recomputed on every apply and never carried over from earlier state.
- [ ] Every settings document is byte-identical to before this change: `internal/workspace/materialize.go`'s permission mapping is unmodified, no file under `internal/workspace/testdata/characterization/` changes, and the characterization test passes without regenerating goldens.
- [ ] The workspace-root state file carries no posture: no code outside the instance pipeline's `Create`/`Apply` state construction sets `ClaudePermissions`, and `git grep -n ClaudePermissions -- internal/workspace` shows only the field definition, the two copy sites, and tests.
- [ ] A state file written by an older binary, without a `claude_permissions` key, loads through `LoadState` with no error and an empty `ClaudePermissions`.
- [ ] Must deliver: `InstanceState.ClaudePermissions`, set by both `Create` and `Apply` to exactly `"bypass"`, `"ask"`, or `""`, persisted under the JSON key `claude_permissions` in `.niwa/instance.json`, and readable through `workspace.LoadState` (required by <<ISSUE:2>>)
- [ ] `go test ./...` passes

## Dependencies

None

## Downstream Dependencies

<<ISSUE:2>> replaces dispatch's settings-file read with `workspace.LoadState(instancePath)` and a pure `derivePermissionMode(explicit, recorded, flags)`. It relies on this issue for three things. The field has to be named `ClaudePermissions` with the JSON key `claude_permissions`, and hold exactly `"bypass"`, `"ask"`, or `""`, so the derivation can compare `recorded == "bypass"`. `Create` has to save the field before it returns, so the state dispatch reads is the state its own process just wrote. And the value has to come from a real declaration run through the pipeline, so that issue's tamper test (a real materialization with the settings document overwritten or deleted afterwards) and its declaration-driven derivation tests can use a real `Create` instead of a hand-written state or settings file.
