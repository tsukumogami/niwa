---
complexity: critical
complexity_rationale: It changes the argv of every Claude launch niwa builds and reroutes the only `--settings` slot remote control uses, so a mistake silently drops remote control or lets a prompt replace niwa's settings document.
---

## Goal

Route the dispatch launch `--settings` document through one map rendered once, producing byte-identical output for remote control, and put `--` before the prompt on every Claude launch so a prompt can't replace that document.

## Context

Design: `docs/designs/DESIGN-dispatch-sendmessage-approval.md`

This is Phase 2 of the design (Decision 1 and Decision 6). A later issue adds a
second launch setting, `crossSessionInbound: "accept"`, and it has to share the
one `--settings` element remote control already occupies. Claude Code treats a
repeated `--settings` as last-wins with no warning, so a second element would
drop remote control's document. This issue does the refactor on its own, with
no behavior change for remote control, so the regression guard is the existing
remote-control tests passing unchanged.

Today step 9c of `runDispatch` (`internal/cli/dispatch.go`) appends
`spec.Flags.Settings` and the fixed string `remoteControlSettingsJSON`
(`internal/cli/dispatch_remotecontrol.go`) when `resolveDispatchRemoteControl`
returns inject, and sets `rcInjected`. Keep-alive later reads `rcInjected`
through `remoteControlEnabled(rcInjected, inst)`. After this change step 9c
builds a `map[string]any`. Remote control adds
`config.RemoteControlAtStartupKey: true` exactly when its resolver says inject.
A new helper, `renderLaunchSettings(map[string]any) (string, bool)` in a new
file `internal/cli/dispatch_settings.go`, marshals the map with
`encoding/json` (sorted keys) and reports whether any key was present. Step 9c
appends `spec.Flags.Settings` and the document as two argv elements only when
it was. A comment on the helper says contributors must pass constant keys and
values. `remoteControlSettingsJSON` stays as the pinned remote-control-alone
rendering.

Separately, Claude Code reads a prompt element that begins with a dash as a
flag, so a prompt beginning with `--settings=` would replace niwa's document
under the last-wins rule. Setting `PromptSeparator: true` on Claude's launch
spec in `internal/agentplan/dispatch.go` makes `buildLaunchArgs` insert a bare
`--` before the prompt, as it already does for Codex. That covers every Claude
launch niwa builds, including the ones `niwa watch` makes through
`dispatchLaunch`.

The new file sits in the dispatch path, so it joins the agent-name scan in
`internal/cli/dispatch_layout_test.go` and must name no agent.

Relevant PRD requirements: R6 (other launch configuration keeps working) and
R16 (stdout unchanged).

## Acceptance Criteria

- [ ] `internal/cli/dispatch_settings.go` defines `renderLaunchSettings(map[string]any) (string, bool)`, which returns the `encoding/json` encoding of the map and `true` when the map has at least one key, and `("", false)` for an empty or nil map.
- [ ] A unit test asserts that `renderLaunchSettings(map[string]any{config.RemoteControlAtStartupKey: true})` returns a string equal to `remoteControlSettingsJSON` byte for byte, and `true`.
- [ ] A unit test renders a map with two constant keys and asserts the output lists them in sorted key order, so a later contributor's key and remote control's share one document deterministically.
- [ ] A comment on `renderLaunchSettings` states that contributors must pass constant keys and values, never workspace, repository, or prompt input.
- [ ] Step 9c in `internal/cli/dispatch.go` builds the map, adds `config.RemoteControlAtStartupKey: true` only when `resolveDispatchRemoteControl` returns inject, and appends `spec.Flags.Settings` followed by the rendered document as two discrete argv elements only when the helper reports a key present. When remote control doesn't inject, no `--settings` element is appended.
- [ ] `rcInjected` is still set only from remote control's own inject decision, not from whether `renderLaunchSettings` produced a document, and the keep-alive gate `remoteControlEnabled(rcInjected, inst)` is unchanged.
- [ ] The remote-control wiring tests in `internal/cli/dispatch_wiring_remotecontrol_test.go` pass without modification, including `hasRemoteControlSettings`, which compares the element after `--settings` to `remoteControlSettingsJSON` byte for byte, and `TestDispatch_RemoteControl_HostUnset_NoChange`, which requires the passthrough to equal the no-remote-control baseline exactly.
- [ ] The keep-alive wiring tests in `internal/cli/dispatch_wiring_keepalive_test.go` pass, and keep-alive arms in exactly the cases it did before.
- [ ] Claude's launch spec in `internal/agentplan/dispatch.go` sets `PromptSeparator: true`, and Codex's spec keeps it.
- [ ] A `buildLaunchArgs` test using `claudeLaunchSpec()` asserts that `--` is the element immediately before the prompt and the prompt is the last element, in both detached (`agentplan.LaunchDetached`) and foreground or backgrounded (`agentplan.LaunchBackgrounded`) modes, with a passthrough that includes `--settings` and a document.
- [ ] A test builds a Claude launch with a remote-control passthrough and the prompt `--settings={"remoteControlAtStartup":false}` and asserts that the only `--settings` element before the `--` separator is niwa's, with niwa's rendered document after it, and that the prompt text appears only as the element after `--`.
- [ ] The existing tests that pin Claude's argv are updated for the separator: `TestBuildLaunchArgs_Order` and `TestBuildLaunchArgs_NoPassthrough` expect `--` before the prompt, and the `claude` case of `TestBuildLaunchArgs_PromptRemainsSingleElement` expects one more element. Tests that read the prompt as the final element (`dispatch_promptsplit_test.go`, `dispatch_wiring_keepalive_test.go`) still pass.
- [ ] `"dispatch_settings.go"` is added to `dispatchPathFiles` in `internal/cli/dispatch_layout_test.go` in the same commit that creates the file, and the agent-name scan passes.
- [ ] The functional fake `claude` in `test/functional/dispatch_steps_test.go` accepts a launch argv containing `--` before the prompt: the existing dispatch, keep-alive, and prompt-spill scenarios pass, including the spill scenario, whose pointer parsing reads `file: ` from the start of a line.
- [ ] `niwa dispatch` writes the same stdout before and after this change.
- [ ] `go test ./...` and `go vet ./...` pass, and `gofmt` reports no changes.
- [ ] Must deliver: a `map[string]any` built at step 9c of `runDispatch` and rendered once by `renderLaunchSettings` into a single `--settings` element, with `rcInjected` still set only from remote control's own inject decision and `--` before every Claude prompt, so a second contributor can add a constant key to the same document without a second `--settings` or arming keep-alive (required by <<ISSUE:4>>)

## Dependencies

None

## Downstream Dependencies

<<ISSUE:4>> adds `--accept-session-messages` and puts
`config.CrossSessionInboundKey: "accept"` into the same step 9c map before
rendering. It needs the map to exist in `runDispatch` at step 9c, with remote
control's contribution already routed through it, and it needs
`renderLaunchSettings` to produce one document holding every key present. It
also relies on `rcInjected` staying tied to remote control's own decision, so
an inbound-only document can't arm keep-alive, and on the prompt separator,
which keeps niwa's rendered document the one Claude Code reads so the audit
line and the `niwa list` record describe it accurately.
