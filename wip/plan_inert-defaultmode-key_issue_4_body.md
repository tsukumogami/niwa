---
complexity: testable
complexity_rationale: Adds functional scenarios and step definitions that pin the dispatch argv and the S1-S9 settings-document matrix end to end, with no production behavior change.
---

## Goal

Cover the dispatch argv and the S1-S9 settings-document matrix end to end in the functional suite, so a broken derivation or materializer change fails a scenario run against the real binary.

## Context

The earlier issues move the dispatch derivation onto the posture recorded in instance state and change what the materializer writes: `bypass` writes no `permissions.defaultMode`, `ask` writes `default`. The Go tests pin single functions and byte comparisons. Two things can only be seen from outside the process. One is the argv the launched worker actually gets, recorded by the functional suite's fake `claude` and fake `codex`. The other is the worktree document that `niwa worktree create` writes, which resolves through its own config path and skips the personal overlay. This issue adds scenarios for both, taking the declared posture from a real `workspace.toml` or personal overlay each time, never from a hand-written settings file.

The test oracle is the upstream PRD's expected-value table (scenarios S1 through S9) and its functional acceptance criteria.

Design: `docs/designs/DESIGN-inert-defaultmode-key.md`

Anchors confirmed at authoring time:
- `test/functional/features/dispatch.feature` holds two `@critical` permission-mode scenarios: "dispatch derives --permission-mode from a bypass-declared workspace" and "an explicit --permission-mode wins over a bypass-declared workspace".
- The fake claude in `test/functional/dispatch_steps_test.go` writes its argument line to `$HOME/dispatch-launch-argv`. The steps `the launched claude was invoked with "..."` and `the launched claude was not invoked with "..."` do substring checks against that file.
- The fake codex (`a fake codex for dispatch with session "..."` in `test/functional/codex_agent_steps_test.go`) records to `$HOME/dispatch-codex-argv`. The steps `the codex launch argv contains "..."` and `the codex launch argv does not contain "..."` read it. `--harness codex` is already used in `agent-selection.feature`.
- `a personal overlay exists with body:` is registered in `test/functional/suite_test.go`. It writes `$HOME/.config/niwa/global/niwa.toml`.
- `I call niwa worktree create for repo "..." with purpose "..." in instance "..."` runs `niwa worktree create` from the instance root.
- Dispatch reads `[global].remote_control_on_dispatch` from the host config (`~/.config/niwa/config.toml`). When it's on, dispatch appends `--settings {"remoteControlAtStartup":true}`.

Missing anchors this issue adds as sub-tasks:
- No step writes a `[global]` body into the sandboxed host `config.toml`. Add one that appends the body. It must append rather than overwrite, because `niwa init` writes the workspace registry into the same file.
- No step counts how many times a fragment appears in the recorded claude argv. Add `the launched claude was invoked with "..." exactly once`, or an equivalent.
- No step reads a generated settings document's `permissions.defaultMode`. Add the settings-file step described in the acceptance criteria.

## Acceptance Criteria

Dispatch argv, in `test/functional/features/dispatch.feature`:

- [ ] The existing `@critical` scenario "dispatch derives --permission-mode from a bypass-declared workspace" (S1: workspace `bypass`, `niwa dispatch <task> --detach`, no permission flag) is still present, still tagged `@critical`, and still asserts the argv contains `--permission-mode bypassPermissions`.
- [ ] A new S1 scenario sets `[global]` `remote_control_on_dispatch = true` in the host config through the new host-config step. It asserts the argv contains `--permission-mode bypassPermissions`, `--settings`, and `remoteControlAtStartup`.
- [ ] The existing explicit-flag scenario (S1 with `--permission-mode acceptEdits`) keeps its current assertions and gains one: `--permission-mode` appears exactly once in the recorded argv. Its `workspace.toml` body is unchanged.
- [ ] A scenario for S2 (workspace `ask`) dispatched with `--permission-mode bypassPermissions` asserts `--permission-mode bypassPermissions` appears in the argv, and `--permission-mode` appears exactly once.
- [ ] Scenarios for S3, S5, and S7 each assert the argv doesn't contain `--permission-mode`. S3 is undeclared. S5 is workspace `bypass` with `[instance.claude.settings]` `permissions = "ask"`. S7 is workspace `ask` with `[repos.<name>.claude.settings]` `permissions = "bypass"`. Each override is written in the config repo's `workspace.toml` body, not in a settings file.
- [ ] Scenarios for S4 and S6 each assert the argv contains `--permission-mode bypassPermissions`. S4 is workspace `bypass` with a repo `[repos.<name>.claude.settings]` `permissions = "ask"`. S6 is workspace `ask` with `[instance.claude.settings]` `permissions = "bypass"`. The S4 and S7 fixtures declare the named repo in `workspace.toml`.
- [ ] Scenarios for S8 and S9 declare their personal-overlay posture through `a personal overlay exists with body:`, under `[global.claude.settings]` or `[workspaces.<name>.claude.settings]`. S8 is undeclared workspace plus personal overlay `bypass`, and its argv contains `--permission-mode bypassPermissions`. S9 is workspace `bypass` plus personal overlay `ask`, and its argv doesn't contain `--permission-mode`.
- [ ] A scenario for S1 dispatched with `--harness codex`, no permission flag, and the existing fake codex asserts the recorded codex argv contains neither `--permission-mode` nor `--sandbox`.
- [ ] None of the new dispatch scenarios gets its posture from a hand-written `.claude/settings.json`, `.claude/settings.local.json`, or `.niwa/instance.json`. Every posture comes from a config repo body or a personal overlay body run through `niwa init`.

Document matrix, in a new feature file under `test/functional/features/`:

- [ ] A new step definition takes a document location (workspace root, instance root, a named repo in an instance, or the last worktree) and an expected value, where `none` means absent. It fails when the file is missing, when it doesn't parse as JSON, when `permissions.defaultMode` differs from the expected value, and, for `none`, when the key is present. The four files are the workspace-root `.claude/settings.json`, the instance-root `.claude/settings.json`, the repo's `.claude/settings.local.json`, and the worktree's `.claude/settings.local.json`.
- [ ] A Scenario Outline runs one row per scenario, S1 through S9. Each row runs `niwa init` from a config repo, then `niwa create`, then `niwa worktree create` against a repo. The fixture declares two repos: the one that carries the per-repo override in S4 and S7, and one with no override. The worktree is created in the repo that carries the override.
- [ ] Before any value check, each row asserts that all four settings documents exist and parse as JSON.
- [ ] Each row then asserts the workspace root, instance root, both repo documents, and the worktree document against the expected-value table. S1, S3, and S8 are none everywhere. S2 is `default` everywhere. S4 is none at both roots, `default` for the override repo and its worktree, and none for the other repo. S5 is `default` at both roots and none for the repos and the worktree. S6 is none at both roots and `default` for the repos and the worktree. S7 is `default` at both roots, none for the override repo and its worktree, and `default` for the other repo. S9 is none at the workspace root and `default` at the instance root and for both repos.
- [ ] For S1 through S8 the worktree cell is checked right after `niwa worktree create`, with no apply in between. For S9 an instance `niwa apply` runs after `niwa worktree create`, and then the worktree document is asserted to be `default`.
- [ ] Across every document the outline checks, the new step (or a companion assertion) fails if `permissions.defaultMode` is `bypassPermissions`, `auto`, or `askPermissions`, or is any value outside `default`, `acceptEdits`, `plan`, and `dontAsk`.
- [ ] Only the S1 row is tagged `@critical`, for example by putting S1 in its own tagged `Examples` block. S2 through S9 run only in the full functional suite.

Compatibility and suite health:

- [ ] No `workspace.toml` body in a functional scenario that existed before this issue changes. Checking `git diff` on the pre-existing feature files shows only added scenarios and added assertion steps, with no edited line inside an existing config-repo docstring.
- [ ] `make test-functional-critical` and the full functional suite pass

## Dependencies

Blocked by <<ISSUE:3>>

## Downstream Dependencies

None
