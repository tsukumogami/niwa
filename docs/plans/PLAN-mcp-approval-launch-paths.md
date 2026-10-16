---
schema: plan/v1
status: Active
execution_mode: single-pr
split_mode_source: none
tracking_level: none
upstream: docs/designs/DESIGN-mcp-approval-launch-paths.md
milestone: "MCP approval on every launch path"
issue_count: 4
---

# PLAN: MCP approval on every launch path

## Status

Active

## Scope Summary

Record the instance root's approved MCP servers in instance state, send them in
`niwa dispatch`'s launch settings so dispatched workers skip the approval
dialog under every permission mode, and have the SessionStart hook tell an
ephemeral session which servers it lacks, with the guide updated to match.

## Decomposition Strategy

**Horizontal.** One component is a prerequisite for the others: the recorded
list in instance state. Once it exists, the dispatch contributor and the hook
notice are independent consumers with no runtime interaction, and the guide
documents both. The layers have stable interfaces (a `[]string` in state), so
a walking skeleton would add nothing.

## Issue Outlines

### Issue 1: feat(workspace): record approved MCP servers in instance state

**Goal**: Add `InstanceState.ApprovedMCPServers`, recomputed on every Create and
Apply from the effective configuration, recorded only when niwa wrote the
instance root's `.mcp.json` and never for review instances.

**Acceptance Criteria**:
- [ ] A workspace declaring base server `zeta` and overlay server `example`
  records `["example","zeta"]`, equal to the instance root `settings.json`
  `enabledMcpjsonServers`.
- [ ] A workspace declaring no servers records nothing (field absent in JSON).
- [ ] An exempt instance-root `.mcp.json` records nothing.
- [ ] A review instance records nothing.
- [ ] The instance root's `.mcp.json` and `settings.json` are unchanged by the
  change (existing golden and materialize tests pass unmodified).
- [ ] The value is recomputed on Apply, not carried over from earlier state.

**Dependencies**: None

**Type**: code
**Files**: `internal/workspace/state.go`, `internal/workspace/apply.go`

### Issue 2: feat(dispatch): send approved MCP servers in the launch settings

**Goal**: Add the `enabledMcpjsonServers` contributor to step (9c) of `niwa
dispatch`, typed and filtered, independent of permission mode.

**Acceptance Criteria**:
- [ ] A dispatch in a workspace declaring `zeta` and `example` has exactly one
  `--settings` document with `enabledMcpjsonServers` `["example","zeta"]`.
- [ ] No servers: no key, and no document when nothing else contributes.
- [ ] Overlay-only server appears; a hand-edited `settings.json` list does not
  change the launch list.
- [ ] Servers `ok` and `bad name`: list is `["ok"]`, dispatch succeeds, stderr
  has one `%q`-escaped warning naming `bad name`; only `bad name`: no key.
- [ ] The list is identical under every permission mode, an explicit
  `--permission-mode`, the bypass posture, and no mode.
- [ ] Never emits `enableAllProjectMcpServers`; coexists with remote-control,
  inbound and lineage keys in one document.
- [ ] Codex dispatch and `niwa watch` review launches carry no such key.
- [ ] An unreadable state file with a settings-capable launch prints a warning
  naming the state file.
- [ ] The `renderLaunchSettings` comment names the second exception.
- [ ] A functional scenario in `test/functional/features/dispatch.feature`
  asserts the launch list with the fake `claude`.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/cli/dispatch.go`, `internal/cli/dispatch_settings.go`

### Issue 3: feat(instance): tell ephemeral sessions which MCP servers they lack

**Goal**: Append a fixed-text paragraph to the SessionStart hook's injected
context naming the recorded servers and the two workarounds.

**Acceptance Criteria**:
- [ ] A workspace declaring `alpha` and `beta`: the injected context contains
  both names in backticks, `niwa dispatch`, and the instruction to start a
  session inside the instance.
- [ ] Names outside `^[A-Za-z0-9_-]{1,64}$` are counted, not printed.
- [ ] A workspace declaring none: injected context byte-identical to before.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/cli/instance_from_hook.go`

### Issue 4: docs(trust): state MCP server coverage per launch path

**Goal**: Add a launch-path coverage section to the instance-trust guide and
record the manual check against a named Claude Code version.

**Acceptance Criteria**:
- [ ] The coverage section lists `niwa dispatch` workers, ephemeral sessions,
  foreground `claude`/`claude -p`, and hand-started `claude --bg`, each with
  whether declared servers load without a dialog.
- [ ] The ephemeral row gives the reason and both workarounds.
- [ ] A recorded manual check names the Claude Code version and confirms
  dispatched workers under `auto` and `default` reach their first turn with no
  startup dialog.

**Dependencies**: Blocked by <<ISSUE:2>>, <<ISSUE:3>>

**Type**: docs
**Files**: `docs/guides/instance-trust.md`

## Implementation Sequence

Issue 1 is the critical path. Issues 2 and 3 can then run in parallel, since
they touch different files and read the same state field. Issue 4 comes last,
because its manual check exercises the built dispatch path.
