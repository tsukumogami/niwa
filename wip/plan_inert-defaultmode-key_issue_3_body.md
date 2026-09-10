---
complexity: critical
complexity_rationale: Changes the permission posture written into every generated Claude Code settings document and changes how three settings-parse errors handle vault-resolved secret values.
---

## Goal

Make `buildSettingsDoc` write no `permissions.defaultMode` for `bypass` and `default` for `ask`, reject invalid values with an error that doesn't leak secrets, and bring every test and golden that pinned the old bytes in line with the PRD's expected-value table.

## Context

Right now `permissionsMapping` in `internal/workspace/materialize.go` maps `bypass` to `bypassPermissions`, which Claude Code 2.1.257 and later ignores from project scope and then downgrades to `default`, overriding the developer's own mode. It maps `ask` to `askPermissions`, which was never a valid mode, so Claude Code throws out the whole file. That also drops the hooks, deny rules, and plugins niwa writes into `ask` scopes, along with the sandbox and guard hooks `niwa watch` merges into an `ask` instance root. All four generated documents go through `buildSettingsDoc`: the instance-root `.claude/settings.json`, each repo's `.claude/settings.local.json`, each worktree's `.claude/settings.local.json`, and the workspace-root `.claude/settings.json` (via `writeRootSettings` in `internal/workspace/root_materializer.go`).

This is Phase 3 of the design. It has to land after <<ISSUE:2>>, which moves the dispatch derivation off the instance-root settings file and onto the `ClaudePermissions` field in `.niwa/instance.json`. If the producer changed before the reader moved, dispatched bypass workers would lose their flag. This issue changes what's written, not which config inputs each document resolves from. The three errors in `buildSettingsDoc` currently echo their resolved value, so a `vault://`-backed value's plaintext can end up in plain `fmt.Errorf` text that the pipeline's redactor never scrubs. This issue closes that for `permissions`, `remoteControlAtStartup`, and `keepAliveOnDispatch`.

Design: `docs/designs/DESIGN-inert-defaultmode-key.md`

The PRD `docs/prds/PRD-inert-defaultmode-key.md` is the test oracle, specifically its S1-S9 expected-value table and acceptance criteria AC12, AC13, AC14, AC16, and AC19.

## Acceptance Criteria

Mapping and errors:

- [ ] `permissionsMapping` no longer exists (`git grep -n permissionsMapping` returns nothing). In its place, `internal/workspace/materialize.go` has `claudeDefaultMode(posture string) (mode string, write bool, err error)`, which returns `("", false, nil)` for `"bypass"`, `("default", true, nil)` for `"ask"`, and a non-nil error for any other input. Its doc comment explains why `bypass` writes nothing: the posture reaches dispatched workers on the `--permission-mode` flag.
- [ ] `buildSettingsDoc` adds `permissions.defaultMode` only when `claudeDefaultMode` reports `write == true`. The worktree-delegation `deny` entries still go into the same `permissions` map and coexist with `defaultMode: "default"`. `permissions` is left out of the document when the map ends up empty. One unit test covers `bypass` plus unsupported delegation (only `deny` is present) and another covers `ask` plus unsupported delegation (both `deny` and `defaultMode: "default"` are present).
- [ ] The invalid-value error names `bypass` and `ask`. For a plain value, it quotes that value. For a secret-backed value (`MaybeSecret.IsSecret()` true), it names the config key and the secret's `Origin()` and never includes the resolved plaintext. A unit test passes a secret-backed invalid value with a distinctive plaintext and asserts that the plaintext is absent from `err.Error()` and that the origin is present.
- [ ] The parse errors for `config.RemoteControlAtStartupKey` and `config.KeepAliveOnDispatchKey` in `buildSettingsDoc` use the same secret-safe form. For each key, a unit test passes a secret-backed unparseable value and asserts that its plaintext is absent from the error while the key name is present. The existing plain-value tests in `materialize_remotecontrol_test.go` and `materialize_keepalive_test.go` still pass.

Document matrix (Go level):

- [ ] AC12: at each of the four locations (workspace root, instance root, a repo, and a worktree), a test materializes S2 (workspace `ask`) and S3 (undeclared) and asserts that the S2 document parsed as JSON equals the S3 document plus `permissions.defaultMode: "default"`, with every other key identical. Hooks and plugins are configured in the fixture, so identical documents can't pass the test just by being empty.
- [ ] AC13: a test writes pre-change documents over a real materialized instance and workspace root: `bypassPermissions` at the instance root, `askPermissions` in one repo's document, and a hand-set `defaultMode` in another repo's document. It then runs apply from the workspace root and asserts that every rewritten document matches its expected-value cell and that the other generated keys are byte-identical to a fresh materialization of the same config.
- [ ] AC14: at each of the workspace, `[instance.claude.settings]`, `[repos.<name>.claude.settings]`, and personal-overlay levels, declaring `permissions = "bypassPermissions"`, `"auto"`, or `"acceptEdits"` makes apply fail with an error that contains both `bypass` and `ask`. The test also asserts that no settings document on disk afterward carries the invalid value.
- [ ] Recorded posture agreement: for each of S1-S9, a test runs a real `Applier.Create` and asserts that the `ClaudePermissions` value saved to `.niwa/instance.json` (`"bypass"`, `"ask"`, or empty) is the posture that produced the instance-root document's `permissions.defaultMode` cell: absent for `bypass`/undeclared, `default` for `ask`. This field is delivered by an earlier issue in the plan. If it's missing when this issue starts, stop and report it rather than redefining it here.
- [ ] AC16: for each of S1, S2, and S3, a test runs a real materialization, calls `ApplyReviewSettings` from `internal/watch/containment.go` in both the operator-approval posture and the hard-deny posture, and asserts that `VerifyReviewSettings` passes each time. After operator-approval, the instance root has `permissions.defaultMode: "default"`. After hard-deny, it has the table's value: absent for S1 and S3, `default` for S2. `internal/watch/containment.go` has no non-comment changes in this issue.
- [ ] AC19 parse test: a unit test parses `permissions = "bypass"` and `permissions = "ask"` without error at the workspace `[claude.settings]`, `[instance.claude.settings]`, `[repos.<name>.claude.settings]`, and personal-overlay (`ParseGlobalConfigOverride` / `[global.claude.settings]` and `[workspaces.<name>.claude.settings]`) levels.

Existing tests and goldens:

- [ ] Every Go test that asserted `bypassPermissions` or `askPermissions` as a written value now asserts the new value: `internal/workspace/materialize_test.go` (including `TestSettingsMaterializerAskPermissions`, which now expects `default`), `root_materializer_test.go`, `apply_test.go`, `materialize_worktree_test.go`, and `worktree_secret_ref_test.go`. Each keeps its original subject (hooks, deny rules, or secret resolution) and its assertions on that subject. A `bypass` case asserts that `defaultMode` is absent, not just that it isn't `bypassPermissions`. After this issue, `git grep -n -E 'bypassPermissions|askPermissions' -- internal/workspace` matches only negative assertions and comments.
- [ ] The characterization goldens under `internal/workspace/testdata/characterization/` are regenerated with `NIWA_UPDATE_CHARACTERIZATION=1 go test ./internal/workspace/ -run Characterization`. The PR description says the live bytes printed on mismatch were reviewed and that the only content change is the `permissions.defaultMode` value.
- [ ] The `@critical` scenario "niwa apply reconciles a settings change from the source on the same run" in `test/functional/features/workspace-config-sources.feature` keeps its `workspace.toml` bodies byte-for-byte unchanged. Its final step (currently `the file ".claude/settings.json" under the workspace root contains "bypassPermissions"`) becomes a step asserting that the instance's `.niwa/instance.json` records `claude_permissions` as `"bypass"`. If no such step definition exists under `test/functional/`, this issue adds it. Otherwise it reuses the existing one.
- [ ] No functional scenario's `workspace.toml` body changes in this issue (`git diff` over `test/functional/features/` touches only step lines).

Downstream deliverables:

- [ ] Must deliver: materializer output matching the PRD's S1-S9 expected-value table at all four locations, so the functional document matrix can assert each cell (required by <<ISSUE:4>>).
- [ ] Must deliver: new materializer behavior on `main` where `bypass` writes no `permissions.defaultMode`, `ask` writes `default`, and invalid values fail naming `bypass` and `ask`, so the corrected docs describe what the code does (required by <<ISSUE:5>>).
- [ ] `go test ./...` and `make test-functional-critical` pass

## Dependencies

Blocked by <<ISSUE:2>>

## Downstream Dependencies

<<ISSUE:4>> adds the S1-S9 Scenario Outline and the new settings-file step. It relies on this issue's materializer producing exactly the expected-value table (none for `bypass` and undeclared, `default` for `ask`) at the workspace root, instance root, repo, and worktree, and on the `claude_permissions` assertion step if it wants to reuse it.

<<ISSUE:5>> corrects seven committed documents to describe `--permission-mode` on the dispatch launch as the posture's route. It relies on the generated files no longer carrying `bypassPermissions` or `askPermissions`, so the guides can say that `bypass` writes nothing and `ask` writes `default`.
