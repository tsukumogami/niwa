---
complexity: critical
complexity_rationale: This change decides whether a dispatched worker gets `--permission-mode bypassPermissions` and runs with no approval prompts, and it touches the argv builder `niwa watch` shares, where a leaked flag would outrank the supervised review's `default` posture.
---

## Goal

Make `niwa dispatch` derive `--permission-mode bypassPermissions` from the posture recorded in the instance's `.niwa/instance.json` rather than from the generated `.claude/settings.json`, and pass the permission mode to `buildDispatchPassthrough` as an explicit argument so `niwa watch` visibly passes none.

## Context

Right now `runDispatch` (`internal/cli/dispatch.go`, the "(9a-derive)" block) decides whether to forward the flag by reading the instance-root `.claude/settings.json` back through `readInstanceSettings` and checking `inst.Permissions.DefaultMode == "bypassPermissions"`. That value is the one <<ISSUE:3>> must stop writing, and a hand-edited or deleted settings file can grant or withhold bypass. The derived value is also written into the package global `dispatchPermissionMode`, which `buildDispatchPassthrough` reads implicitly. `niwa watch` calls the same builder at two sites (the fresh review in `stageReview` and the `--resume` continuation), and those get an empty value only because nothing in a watch process sets the global.

<<ISSUE:1>> records the resolved instance posture (`"bypass"`, `"ask"`, or empty) as `InstanceState.ClaudePermissions` at materialization. This issue moves the reader onto that field, adds a pure `derivePermissionMode(explicit, recorded string, flags agentplan.LaunchFlags) (mode string, derived bool)`, changes the builder signature to `buildDispatchPassthrough(flags, slug, model, permissionMode string)`, removes the `Permissions` projection from `instanceSettings`, and deletes the dead `WorkerPermissionMode` reader. The materializer still writes the old values in this issue, so dispatch behavior doesn't change. Only the derivation's input does.

Design: `docs/designs/DESIGN-inert-defaultmode-key.md`

The PRD at `docs/prds/PRD-inert-defaultmode-key.md` has the acceptance criteria and the S1-S9 expected-value table used as the oracle below.

## Acceptance Criteria

Derivation and state load:

- [ ] `runDispatch` loads the dispatched instance's state with `workspace.LoadState(instancePath)` and passes `state.ClaudePermissions` to `derivePermissionMode`. The "(9a-derive)" block no longer references `inst.Permissions`, and no permission value is read from any `.claude/settings.json` on the dispatch path.
- [ ] `derivePermissionMode` returns `(explicit, false)` when the operator's value is non-empty. Otherwise it returns `("bypassPermissions", true)` exactly when `flags.PermissionMode == "--permission-mode"` and `recorded == "bypass"`, and `("", false)` otherwise. A table-driven unit test covers explicit plus `bypass`, explicit plus empty, `bypass` with Claude flags, `ask`, `""`, an unrecognized recorded value, and `bypass` with the Codex launch flags. The Codex case returns `("", false)`.
- [ ] When `derived` is true, dispatch still prints the existing stderr notice that the flag was derived. The notice wording is updated so it no longer names Claude Code 2.1.258 or the settings file as the source.
- [ ] If `.niwa/instance.json` is missing, unreadable, or unparseable, dispatch prints a stderr warning that contains the state file's path, forwards no derived `--permission-mode`, and doesn't fail. One test per condition (file removed, permissions set to 0000 with a skip when running as root, invalid JSON) asserts the warning and that no `--permission-mode` is on the argv. With an explicit `--permission-mode acceptEdits`, the argv still carries exactly one `--permission-mode acceptEdits`.
- [ ] `runDispatch` no longer assigns to `dispatchPermissionMode`. After this change, `git grep -n 'dispatchPermissionMode' -- internal/cli ':!*_test.go'` returns only the Cobra flag binding and the variable declaration, plus the single read in `runDispatch` that passes the operator's value to `derivePermissionMode`.

Argv builder and watch:

- [ ] `buildDispatchPassthrough` has the signature `(flags agentplan.LaunchFlags, slug, model, permissionMode string) []string` and doesn't read `dispatchPermissionMode`. The dispatch call site passes the derived mode. Both call sites in `internal/cli/watch.go` (fresh review and `--resume` continuation) pass the literal `""`. The test caller in `internal/cli/dispatch_wiring_remotecontrol_test.go` is updated.
- [ ] Unit-level watch argv test: materialize S1 (`permissions = "bypass"` in the workspace `[claude.settings]`) through a real `Applier.Create` and confirm the instance's `.niwa/instance.json` records `claude_permissions: "bypass"`. Then assert that the argv from watch's fresh-review launch and from its continuation launch contains no `--permission-mode`. To catch a regression, the test must fail if either watch site is changed to call `derivePermissionMode` or to read `ClaudePermissions`. Build the argv through the same function or seam the production watch code uses, not a copy.

Tamper test:

- [ ] Case one: materialize S3 (undeclared) through a real `Applier.Create` in a temporary workspace. The pattern in `internal/cli/allow_missing_secrets_test.go` is a usable model. Overwrite the instance-root `.claude/settings.json` with `{"permissions":{"defaultMode":"bypassPermissions"}}`, run the derivation path used by `runDispatch` (state load plus `derivePermissionMode` plus `buildDispatchPassthrough`), and assert the argv contains no `--permission-mode`.
- [ ] Case two: materialize S1 through a real `Applier.Create`, delete the instance-root `.claude/settings.json`, run the same path, and assert the argv contains `--permission-mode bypassPermissions` exactly once.

Test rewrite:

- [ ] The seven tests in `internal/cli/dispatch_permissionmode_test.go` that hand-write a `.claude/settings.json` are replaced with tests whose posture comes from a `workspace.toml` declaration run through real materialization. At minimum this covers S1 (flag derived), S1 with explicit `--permission-mode acceptEdits` (exactly one `--permission-mode`, value `acceptEdits`, no `bypassPermissions`), S2 and S3 (no `--permission-mode`), Codex under S1 (no `--permission-mode`), and S1 with remote control on (argv carries both `--permission-mode bypassPermissions` and the remote-control `--settings` pair, and keep-alive resolution is unchanged).
- [ ] Across `internal/cli`, no test that asserts on a forwarded `--permission-mode` gets its posture from a hand-written settings document, apart from the tamper cases above. A test whose fake provisioner writes `instance.json` with `claude_permissions` set by hand, with no real materialization, doesn't count as declaration-driven for this criterion.
- [ ] Every fake `provisionInstanceFunc` in the dispatch unit tests (`internal/cli/dispatch_test.go`, `internal/cli/dispatch_wiring_remotecontrol_test.go`, and any other test that drives `runDispatch`) writes a minimal valid `.niwa/instance.json`, so the missing-state warning doesn't fire in tests that aren't about the posture. A test that isn't about the posture doesn't assert on the warning's absence to pass.

Static checks and removals:

- [ ] A Go test scans the non-test sources under `internal/` and asserts that `ClaudePermissions` is read in exactly one place outside `internal/workspace`: the load site in `runDispatch`. Nothing in `internal/cli/watch.go` or `internal/watch` references it. The scan also asserts that no code copies `ClaudePermissions` from the workspace-root state, including the `workspace.SaveState(workspaceRoot, ...)` path in `internal/cli/init.go` and the `LoadState(workspaceRoot)` read in `internal/cli/effective_name.go`. The writes <<ISSUE:1>> added in `internal/workspace` (the pipeline carry and the Create and Apply copies) are the allowed writers.
- [ ] A comment at the `runDispatch` load site says the recorded value is trusted only for the instance this process just provisioned, and that code needing an existing instance's posture must re-resolve it from configuration.
- [ ] The `Permissions` field is removed from `instanceSettings` in `internal/cli/dispatch_plugins.go`. The struct's doc comment no longer lists "permissions", and the `readInstanceSettings` doc comment no longer points at `internal/workspace/permissions.go`. `readInstanceSettings` stays and still serves plugin prewarm, remote control, and keep-alive.
- [ ] `internal/workspace/permissions.go` and `internal/workspace/permissions_test.go` are deleted, and `git grep -n WorkerPermissionMode -- '*.go'` returns nothing. Committed docs outside `docs/designs/archive/` and the two feature documents may still name it until the documentation issue.

Re-entry:

- [ ] Tests assert that each of the four re-entry surfaces for a Claude dispatch produces `claude attach <handle>` with no further arguments, where `<handle>` equals the handle recorded for the dispatched session. The four surfaces are dispatch's final attach (the `reentryArgs` call in `dispatch.go`), the printed attach hint (`reentryHints`), the attach-failure fallback (`reentryCommand` in the fallback branch of `dispatch.go`), and the `niwa list` resume column (`sessionResumeCommand` in `internal/cli/list.go`). Each test runs under a `bypass` materialization, so a future change that appends a permission flag to re-entry fails the test. Existing tests in `internal/cli/dispatch_reentry_test.go` and `internal/cli/list_test.go` can be extended rather than duplicated.

Behavior unchanged:

- [ ] The existing `@critical` scenarios in `test/functional/features/dispatch.feature` for the derived flag and the explicit flag pass unmodified.
- [ ] `go test ./...` and `make test-functional-critical` pass.

Downstream contract:

- [ ] Must deliver: a dispatch derivation that reads no permission value from any generated settings document, so the materializer's output can change without changing what dispatch forwards (required by <<ISSUE:3>>). This is verified by the tamper case-one test passing both before and after the materializer's `bypass` output changes, with no edit to the test.

## Dependencies

Blocked by <<ISSUE:1>>

## Downstream Dependencies

<<ISSUE:3>> changes `buildSettingsDoc` so a `bypass` document carries no `permissions.defaultMode` and an `ask` document carries `default`. It needs three things from this issue:

- Dispatch no longer reading `instanceSettings.Permissions`, so an S1 instance still gets `--permission-mode bypassPermissions` once the instance-root file stops saying `bypassPermissions`.
- Declaration-driven dispatch tests on a real `Applier.Create` that keep passing across the materializer flip without edits.
- `WorkerPermissionMode` removed, so no second reader of the retired value is left for that issue to update.
