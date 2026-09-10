---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-dispatch-sendmessage-approval.md
milestone: "Unattended peer messages for dispatched sessions"
issue_count: 8
tracking_level: none
---

# PLAN: Unattended peer messages for dispatched sessions

## Status

Active

The work lands in one pull request. No GitHub issues or milestone are created;
the work items are the outlines below.

## Scope Summary

This plan implements the accepted design for letting `niwa dispatch` launch
Claude workers that accept messages from other Claude Code sessions without an
approval prompt. The behavior is off by default, turned on by the `[global]
accept_session_messages_on_dispatch` machine key, and overridable in either
direction per dispatch with `--accept-session-messages[=true|false]`. niwa
delivers it by adding `crossSessionInbound: "accept"` to the one launch
`--settings` document it already builds for remote control, gated by a new
`DispatchInboundAcceptance` capability row so Codex dispatches get a warning
instead. Every dispatch where it takes effect prints an audit line, records
`accepts_session_messages` on the session mapping for `niwa list`, and the first
one shows a one-time explanation remembered by a marker beside `config.toml`.

Two changes ride along because the feature depends on them. Claude launches put
`--` before the prompt, so a prompt can't replace niwa's settings document and
make the audit line and the record misreport. `niwa watch` review sessions deny
`SendMessage`, `SendFile`, `RemoteTrigger`, and `ListAgents` in every
containment mode, so turning the behavior on doesn't open a channel from a
contained reviewer to an uncontained worker.

## Decomposition Strategy

**Horizontal decomposition**, one unit per design phase in the design's commit
order, with the design's last phase split into functional coverage (unit 7) and
the guide plus manual check (unit 8).

The shape follows from the design's decisions rather than from a generic
layering:

- **Units 1, 2 and 3 have no dependencies.** The review-session deny (Decision 5)
  touches only `internal/watch` and stands alone. The config key is a pure
  addition that nothing reads yet. The settings rendering (Decision 1) is a
  behavior-preserving refactor of remote control's injection, and the prompt
  separator (Decision 6) is a one-field change to Claude's launch spec; both
  change step 9c's inputs without adding the new key, so remote control's
  byte-identical output is provable before anything else moves.
- **Unit 4 is where the behavior turns on**, and it has to be one unit.
  Decision 2 requires the capability row to land in the same commit as the
  delivery ("never before"), and adding the row moves fixed counts in capability
  tests, the generated Codex gap list, and the capability-contract PRD, so those
  travel with it. The same unit introduces `inboundApplied`, the single boolean
  the design makes the source for the audit line, the session record, and the
  explanation.
- **Units 5 and 6 build on `inboundApplied`** and can proceed in parallel: unit 5
  threads it into the step 11 mapping literal and `niwa list`; unit 6 adds the
  explanation at the two call sites Decision 3 fixes (after the audit line, or
  after `claude attach` returns).
- **Units 7 and 8 close out coverage and documentation.** Unit 7 is code (step
  definitions, feature file, the watch-site test observing real argv). Unit 8 is
  docs, and it owns the manual delivery check, whose results decide the guide's
  version line and its description of who can send.

Cross-batch edges: 2 into unit 4, 3 into unit 4, 4 into units 5 and 6, and 1,
4, 5, 6 into each of units 7 and 8.

## Issue Outlines

### Issue 1: feat(watch): deny session-reaching tools in review sessions

**Complexity**: critical

**Goal**: Add a PreToolUse hook to every `niwa watch` review session, in every containment mode, that denies the Claude Code tools able to reach other sessions (`SendMessage`, `SendFile`, `RemoteTrigger`, `ListAgents`). Verify the hook by matcher and by command before any review launches.

**Acceptance Criteria**:

- [ ] `internal/watch/containment.go` defines `sessionReachDenyMatcher = "SendMessage|SendFile|RemoteTrigger|ListAgents"`. Its doc comment names the four tools, says the list comes from Claude Code 2.1.267's tool list, and says the matcher uses only letters and `|` so Claude Code compares it as an exact list of tool names (aliases included, so `ListPeers` resolves to `ListAgents`) rather than as a substring or regex.
- [ ] A unit test asserts the matcher's form: it matches `^[A-Za-z0-9_]+(\|[A-Za-z0-9_]+)*$` (no `.`, `*`, `^`, `$`, `(`, `[`, spaces, or empty alternatives); splitting it on `|` yields exactly the set {`SendMessage`, `SendFile`, `RemoteTrigger`, `ListAgents`}, with no duplicates; and no token equals or is a substring of any tool the review sessions rely on (`Bash`, `Read`, `Glob`, `Grep`, `Write`, `Edit`, `MultiEdit`, `NotebookEdit`, `WebFetch`, `WebSearch`), and none of those names is a substring of any token. A matcher widened to a regex, or one that would also catch `Read` under substring matching, fails this test.
- [ ] `sessionReachDenyHook()` returns a map with exactly two keys, `matcher` (equal to `sessionReachDenyMatcher`) and `hooks`, where `hooks` holds exactly one entry with `"type": "command"` and a `command` string. The command writes exactly `niwa watch: review sessions don't reach other sessions` followed by a newline to stderr, writes nothing to stdout, and exits 2, regardless of stdin. It must survive the apostrophe in "don't" (for example via `shellQuote`).
- [ ] A unit test reads the command from the applied settings (not from `sessionReachDenyHook()` directly) and runs it with `sh -c`, once per tool with a PreToolUse payload of the form `{"hook_event_name":"PreToolUse","tool_name":"<name>","tool_input":{}}` for each of the four names, and once with empty stdin. Each run must exit 2, with stderr equal to the refusal line and stdout empty. The same test asserts that `Read` is not a token of the entry's matcher, so the hook that blocks the four tools never fires for a normal review read.
- [ ] A unit test asserts where the entry sits: after `ApplyReviewSettings`, the deny entry appears in `settings["hooks"]["PreToolUse"]` and in no other hook event key (no `PostToolUse`, `UserPromptSubmit`, or other key holds an entry with `sessionReachDenyMatcher`).
- [ ] `ApplyReviewSettings` appends `sessionReachDenyHook()` in every combination of `sandbox` and `ask`: (false, false), (true, false), and (true, true). It skips the append only when an entry with the same matcher and the same command is already present; checking the matcher alone isn't enough. `preToolUseHasMatcher` keeps its current matcher-only behavior for the existing hooks, and the matcher-and-command check is a new helper.
- [ ] `VerifyReviewSettings(merged, sandbox, ask)` returns an error in every mode when no PreToolUse entry has both matcher `sessionReachDenyMatcher` and niwa's exact deny command. The error names the matcher in the same `review settings check: ... PreToolUse hook (matcher %q) missing` form the other checks use. Because both watch call sites return this error before launch, a dropped hook stops the review launch.
- [ ] New unit test: for each of the three `(sandbox, ask)` combinations, a fresh `ApplyReviewSettings` produces settings where `countPreToolUseMatcher(t, got, sessionReachDenyMatcher)` is 1 and `VerifyReviewSettings` passes. Removing the deny entry from the resulting document makes `VerifyReviewSettings` fail in that same mode.
- [ ] New unit test: a settings file that already holds a PreToolUse entry with matcher `SendMessage|SendFile|RemoteTrigger|ListAgents` and a different command (for example `exit 0`) keeps that entry, gains niwa's deny hook as a second entry with the same matcher, and verifies. A hand-built document holding only the impostor entry fails `VerifyReviewSettings` in every mode, and so does one whose entry has niwa's command under a different matcher (for example `SendMessage`).
- [ ] New unit test: applying twice leaves exactly one deny-hook entry (extend `TestApplyReviewSettings_DedupesHooks` or add a sibling). Applying to a settings file written in the pre-feature shape (post-guard, egress-deny, and filesystem-guard hooks, no deny hook) adds the hook. This covers the resume re-assert for reviews staged before the upgrade.
- [ ] The existing tests that build settings documents by hand and expect them to verify (`TestVerifyReviewSettings_RequiresAskPostureBits`, `TestVerifyReviewSettings_RejectsRelaxations`, and the non-sandbox halves of `TestVerifyReviewSettings_RequiresEgressDeny` and `TestVerifyReviewSettings_RequiresFSGuard`) include `sessionReachDenyHook()` in their fixtures, and each still fails only for the condition its name describes. The existing assertions in `TestApplyReviewSettings_NoSandbox`, `TestApplyReviewSettings_Sandbox`, `TestApplyReviewSettings_AskPosture`, and `TestApplyReviewSettings_HardDenyPostureUnchanged` still hold, and each also asserts the deny hook is present exactly once.
- [ ] `egressDenyMatcher` stays `"WebFetch|WebSearch|mcp__"`, and the egress-deny hook stays sandbox-only. `postGuardMatcher`, `fsGuardMatcher`, and `autoAllowMatcher` and their hooks are unchanged.
- [ ] `noEgressSandboxStanza` gains a comment saying the stanza must keep unix sockets disallowed, because that's what keeps Bash in a sandboxed review off Claude Code's local cross-session inbox. A unit test asserts the stanza's `network` map has exactly one key, `allowedDomains`, holding an empty list, so any unix-socket allowance added later fails it.
- [ ] The doc comments on `ApplyReviewSettings` and `VerifyReviewSettings` describe the new hook as applied and required in every mode, keyed on matcher and command. The comment above the matcher constants, which says dedupe is by matcher alone, notes the deny hook's exception.
- [ ] `go test ./...` and `go vet ./...` pass, and `gofmt -l internal/watch` prints nothing.
- [ ] Must deliver: the deny hook applied by both watch launch paths, with `sessionReachDenyMatcher` and the refusal message kept as stable strings in `internal/watch/containment.go`. The end-to-end coverage can then rely on review-session containment already being in place without restating it (required by <<ISSUE:7>>).
- [ ] Must deliver: a hook that holds up in a real review session, which <<ISSUE:8>> verifies as a merge-blocking manual measurement. In a real `niwa watch` review session, run once in the sandbox posture and once with `watch_sandbox = off`, the session attempts `SendMessage` to an accepting dispatched worker and attempts `ListAgents`. Each call must be refused with `niwa watch: review sessions don't reach other sessions`, and nothing may reach the worker. If either call gets through, this issue's matcher or hook is fixed before the pull request merges (required by <<ISSUE:8>>).
- [ ] Must deliver: the denied tool list (`SendMessage`, `SendFile`, `RemoteTrigger`, `ListAgents`) and the Claude Code version it was taken from (2.1.267), recorded in the `sessionReachDenyMatcher` doc comment, plus the hook's stated limits: with `watch_sandbox = off` it's accident prevention only, it relies on the stanza keeping unix sockets disallowed, it applies only after watch re-stages or resumes a review, and `disableAllHooks` turns it off. The guide copies these (required by <<ISSUE:8>>).

**Dependencies**: None

**Type**: code

### Issue 2: feat(config): add the accept_session_messages_on_dispatch machine key

**Complexity**: testable

**Goal**: Add the `[global] accept_session_messages_on_dispatch` machine setting to niwa's configuration struct, and add the `crossSessionInbound` settings-key constant. Both decode and round-trip, and nothing reads them yet.

**Acceptance Criteria**:

- [ ] `config.GlobalSettings` has a field `AcceptSessionMessagesOnDispatch *bool` tagged `toml:"accept_session_messages_on_dispatch,omitempty"`, with a doc comment in the style of `KeepAliveOnDispatch`. The comment says the field is a host-level default scoped to dispatched workers, that the per-dispatch `--accept-session-messages` flag overrides it in both directions, that no workspace or instance source can set it, and that nil means off.
- [ ] `internal/config/config.go` declares `const CrossSessionInboundKey = "crossSessionInbound"` beside `RemoteControlAtStartupKey`, with a comment saying it is the Claude Code settings key for accepting inbound cross-session messages and the single source of its spelling.
- [ ] The setter-less key list in the `SaveGlobalConfigTo` doc comment (currently `dispatch_model, remote_control_on_dispatch, keep_alive_on_dispatch, watch_sandbox, watch_max_staged`) also names `accept_session_messages_on_dispatch`.
- [ ] No `niwa config set` or `niwa config unset` key is added for the setting.
- [ ] A new test file `internal/config/registry_inbound_test.go`, following the shape of `registry_keepalive_test.go` and `registry_remotecontrol_test.go`, has a table test `TestParseGlobalConfig_AcceptSessionMessagesOnDispatch`. It checks that:
  - a `[global]` table without the key (for example, only `clone_protocol = "ssh"`) decodes the field as nil;
  - `accept_session_messages_on_dispatch = true` decodes as a non-nil pointer to `true`;
  - `accept_session_messages_on_dispatch = false` decodes as a non-nil pointer to `false`.
- [ ] In the same file, `ParseGlobalConfig` returns a non-nil error and a nil `*GlobalConfig` for `[global]\nclone_protocol = "ssh"\naccept_session_messages_on_dispatch = "yes"\n`. So a non-boolean value makes the whole config unreadable, not only this key. The test also checks the same for a numeric value such as `= 1`.
- [ ] In the same file, `LoadGlobalConfigFrom` returns an error for a `config.toml` written to `t.TempDir()` whose `[global]` table sets `accept_session_messages_on_dispatch = "yes"`. This is the error the dispatch resolver will treat as "setting absent".
- [ ] In the same file, a round-trip test writes a `GlobalConfig` with `AcceptSessionMessagesOnDispatch: boolPtr(true)` through `SaveGlobalConfigTo` to a path in `t.TempDir()`, reads it back with `LoadGlobalConfigFrom`, and gets a non-nil pointer to `true`. The same round-trip preserves an explicit `false` as a non-nil pointer to `false`.
- [ ] In the same file, encoding a `GlobalConfig` whose field is nil (for example, only `CloneProtocol: "ssh"`) produces output that doesn't contain `accept_session_messages_on_dispatch`, confirming that `omitempty` drops the nil pointer.
- [ ] In the same file, a test asserts `CrossSessionInboundKey == "crossSessionInbound"`.
- [ ] The existing tests in `registry_keepalive_test.go`, `registry_remotecontrol_test.go`, and `registry_test.go` pass unchanged.
- [ ] Nothing outside `internal/config` reads the new field or constant in this change.
- [ ] `go test ./...` and `go vet ./...` pass, and the changed files are gofmt-clean.
- [ ] Must deliver: the exported field `config.GlobalSettings.AcceptSessionMessagesOnDispatch` (`*bool`, nil when the key is absent) and the exported constant `config.CrossSessionInboundKey` with the value `"crossSessionInbound"`, both importable from `internal/cli` (required by <<ISSUE:4>>)

**Dependencies**: None

**Type**: code

### Issue 3: refactor(dispatch): render launch settings from one map and separate the Claude prompt

**Complexity**: critical

**Goal**: Route the dispatch launch `--settings` document through one map rendered once, producing byte-identical output for remote control, and put `--` before the prompt on every Claude launch so a prompt can't replace that document.

**Acceptance Criteria**:

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

**Dependencies**: None

**Type**: code

### Issue 4: feat(dispatch): add --accept-session-messages with its capability row and audit lines

**Complexity**: critical

**Goal**: Add the tri-state `--accept-session-messages` flag and the `DispatchInboundAcceptance` capability row. Resolve the flag over the `[global] accept_session_messages_on_dispatch` machine key into one `inboundApplied` boolean, add `crossSessionInbound: "accept"` to the single launch `--settings` document when that boolean is true, and print the audit, override, and warning lines on stderr. The audit and override lines print only once the session mapping is durable.

**Acceptance Criteria**:

Capability row (same commit as the delivery):

- [ ] `internal/agentplan/capability.go` appends `DispatchInboundAcceptance` as row 25 at the end of the `iota` block, and the catalog entry `{DispatchInboundAcceptance, "dispatch-inbound-acceptance", RouteLaunch}` goes at the end of the catalog. `DirectoryTrust` (row 23) and `GitExcludeBookkeeping` (row 24) keep their numbers. The "24 rows" and "24 entries" prose in `capability.go` reads 25.
- [ ] `internal/agentplan/declaration.go` declares the row `StateImplemented` for Claude with `Requires: []Capability{DispatchLaunch}`, and `StateUnavailable` for Codex with `ReasonNoSuchConcept` and the reason `Codex has no setting for accepting messages from other sessions.` The declaration comment's Codex totals ("Fifteen ... implemented and nine are unavailable", "all nine") change to fifteen and ten.
- [ ] `internal/agentplan/gaplist.go` maps the row to the subject `Accepting messages from other sessions without an approval prompt`, and `TestEveryCapabilityHasAGuideSubject` passes.
- [ ] `TestAllIsTheClosedSet` expects 25. `TestCodexColumnTotals` expects 15 implemented and 10 unavailable. `codexFinalGaps` gains `DispatchInboundAcceptance: ReasonNoSuchConcept`, and its comment's "nine" counts read ten.
- [ ] The generated section of `docs/guides/codex-agent.md`, between `<!-- BEGIN GENERATED: codex gap list` and `<!-- END GENERATED: codex gap list -->`, is regenerated with `go test ./internal/agentplan -run TestCodexGuideGapSectionMatchesDeclarations -update`. It lists the new subject, and the drift test passes without `-update`.
- [ ] `docs/prds/PRD-agent-capability-contract.md` gets an amendment below the existing ones under the matrix (after "Amendment to rows 18 and 19"). The amendment adds row 25 (Claude implemented, Codex unavailable, no-such-concept) and states the settled Codex column as 15 implemented and 10 unavailable, asserted by `TestCodexColumnTotals`.

Flag and resolution:

- [ ] `niwa dispatch` registers `--accept-session-messages` with `triBoolValue` and `NoOptDefVal = "true"`, the way `--keep-alive` is registered, using this exact help text: `accept messages from other Claude Code sessions without an approval prompt; overrides the [global] accept_session_messages_on_dispatch machine setting in either direction`. A bare flag and `=true` mean on, and `=false` means off.
- [ ] A new file, `internal/cli/dispatch_inbound.go`, holds the flag variable, `inboundResolution` (fields `on`, `source`, and `overrodeMachineOn`), `resolveDispatchInboundAcceptance(flag *bool, global config.GlobalSettings) inboundResolution`, the `inboundGuideURL` constant (`https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md`), and the audit, override, and warning strings as named constants or format strings. The same commit adds the file to `dispatchPathFiles` in `dispatch_layout_test.go`, and the layout scan passes.
- [ ] A table test of the resolver covers every combination of flag (nil, true, false) and machine setting (nil, true, false). The flag wins whenever it's given, the machine setting decides when the flag is nil and the setting is non-nil, and the behavior is otherwise off. `source` is `--accept-session-messages` or `machine setting`, whichever decided. `overrodeMachineOn` is true only for flag false over machine true.
- [ ] `installDispatchFakes` in `internal/cli/dispatch_test.go` saves, resets to nil, and restores the new flag variable, as it does `dispatchKeepAlive`.
- [ ] The `hostGlobal` block (the zero-on-failure `config.GlobalSettings`) moves from step 9d to above step 9c, and both the inbound resolver and `resolveDispatchKeepAlive` read it. Test: with `config.toml` unopenable, with invalid TOML, and with `accept_session_messages_on_dispatch = "yes"`, a dispatch without the flag leaves the behavior off and prints no audit line. The same fixtures with `--accept-session-messages` put the key in the document and print the audit line naming the flag (R15). Remote control still injects nothing when the config can't be read.

Delivery and the launch document:

- [ ] At step 9c the behavior counts as deliverable only when `agentplan.Lookup(agentplan.DispatchInboundAcceptance, dispatchedAgent)` reports `StateImplemented` and `spec.Flags.Settings` is non-empty. `inboundApplied` is true exactly when the resolution is on and the behavior is deliverable. `config.CrossSessionInboundKey: "accept"` is added to the launch settings map exactly when `inboundApplied` is true.
- [ ] Precedence matrix, asserted on the Claude launch argv built by `runDispatch` with `dispatchLaunch` stubbed: with the key absent or `false` and no flag, the document lacks `crossSessionInbound`. With the key `true` and no flag, with the key absent and `--accept-session-messages`, or with the key absent and `=true`, the document has `"crossSessionInbound":"accept"`. With the key `true` and `=false`, the document lacks it.
- [ ] With remote control on dispatch, keep-alive, a declared bypass posture, and the behavior all on, the argv carries exactly one `--settings` element whose parsed document holds both `remoteControlAtStartup: true` and `crossSessionInbound: "accept"`, and it also carries `--permission-mode bypassPermissions`. Tests that build both keys parse the JSON rather than comparing strings.
- [ ] `rcInjected` is still set only by remote control's own inject decision. With the behavior on and remote control off, `--keep-alive` still takes the non-remote-control warning path and doesn't arm, and the mapping's `KeepAlive` is false. With remote control on, keep-alive arms exactly as it does without the behavior (R6). The existing remote-control tests, including the test that pins the remote-control-alone rendering to `remoteControlSettingsJSON` byte for byte, pass unchanged.

Stderr lines:

- [ ] When `inboundApplied` is true, the line written after step 12 (mapping written, rollback disarmed) and before step 13's stdout hints is exactly `niwa dispatch: this worker accepts messages from other sessions without asking (source: machine setting accept_session_messages_on_dispatch); see <inboundGuideURL>` for the machine source. With the flag as the source, the parenthetical reads `(source: --accept-session-messages)`. It appears exactly once, and never when `inboundApplied` is false (R7). The failure-path cases below are what pin it after step 12.
- [ ] When the resolution's `overrodeMachineOn` is true and the behavior was deliverable, the line at that same point is exactly `niwa dispatch: this worker keeps Claude Code's default for messages from other sessions (--accept-session-messages=false overrides the machine setting)`. No override line prints for the key absent with `=false`, for the key `true` with `=true` (which prints the audit line only), or for a non-deliverable agent (R8).
- [ ] When the flag asked for the behavior (flag true) and it isn't deliverable, step 9c prints `niwa dispatch: --accept-session-messages does not apply to the %q agent and was ignored. %s` with the agent name and the declaration's reason. For a Codex dispatch that line names `"codex"`, the launch carries no `crossSessionInbound`, and no audit line prints. When only the machine key asked for a Codex dispatch, nothing prints, with or without `--accept-session-messages=false` (R13).

Failure paths print neither line (D6). Each case below runs a Claude dispatch through `runDispatch` with `installDispatchFakes`, once with the behavior on (machine key `true` and no flag, and again with `--accept-session-messages`), and once with the machine key `true` and `--accept-session-messages=false` (the override case):

- [ ] Launch failure: the stubbed `dispatchLaunch` returns an error. `runDispatch` returns an error, the capture stub is never called, and stderr contains neither `this worker accepts messages from other sessions` nor `this worker keeps Claude Code's default`.
- [ ] Capture failure: `dispatchLaunch` succeeds and the `dispatchCapture` stub returns an error. `runDispatch` returns an error wrapping `capturing dispatch session id`, stderr contains neither the audit line nor the override line, the rollback's `destroyInstanceFunc` fake is called, and no file exists under the workspace's `.niwa/sessions/` directory.
- [ ] Mapping-write failure: `dispatchLaunch` and capture succeed, but `workspace.WriteSessionMapping` fails. Trigger it by having the capture stub return a session id that isn't a lowercase UUID, which `WriteSessionMapping` rejects before writing anything. As a second fixture, create `.niwa/sessions` as a regular file. `runDispatch` returns an error wrapping `writing dispatch session mapping`, stderr contains neither the audit line nor the override line, the rollback's destroy fake is called, and no session mapping file exists, so nothing records that the worker accepts messages from other sessions.

Output and scope:

- [ ] Stdout is byte-identical between a dispatch with the behavior on and the same dispatch with it off, apart from the instance path and session id (R16). The printed resume commands stay `claude attach <id>`.
- [ ] The `niwa watch` launches never carry the key (R14, D11). This issue adds no read of the new flag variable, of `AcceptSessionMessagesOnDispatch`, or of `config.CrossSessionInboundKey` anywhere `realDispatchLaunch`, `buildLaunchArgs`, or `internal/cli/watch.go` would reach it. The launch document still comes from the settings map handed to `dispatchLaunch` in `launchRequest`, and no package-level state feeds it. The test that checks this outcome belongs to <<ISSUE:7>>. It keeps the real `dispatchLaunch`, and with the machine key `true` and the dispatch flag variable set on, it observes the argv Claude would actually receive at both watch launch sites, below `buildLaunchArgs`, and finds no element containing `crossSessionInbound`. This issue's implementation must be shaped so that test passes without changes to it.

Downstream deliverables:

- [ ] Must deliver: an `inboundApplied` boolean declared in `runDispatch` and in scope at the step 11 `workspace.SessionMapping` literal, set before `WriteSessionMapping` runs (required by <<ISSUE:5>>).
- [ ] Must deliver: the audit-line call site between steps 12 and 13, structured so the explanation can be called right after it when no attach follows, plus `inboundApplied` still in scope at step 14's `dispatchAttach` branches, and the package-level `inboundGuideURL` constant (required by <<ISSUE:6>>).
- [ ] Must deliver: the working `--accept-session-messages` flag, the machine key wired through `hostGlobal`, and the audit, override, and warning strings exactly as above, so the functional scenarios can assert them verbatim (required by <<ISSUE:7>>).
- [ ] Must deliver: the final flag help text above and the three stderr strings, unchanged after this issue, for the guide to quote (required by <<ISSUE:8>>).

Build:

- [ ] `go test ./...` and `go vet ./...` pass, and the code is gofmt-clean.

**Dependencies**: Blocked by <<ISSUE:2>>, <<ISSUE:3>>

**Type**: code

### Issue 5: feat(list): record and report accepts_session_messages

**Complexity**: testable

**Goal**: Record on each dispatched session's mapping whether peer-message acceptance took effect, and report it through `niwa list` as an always-present `accepts_session_messages` field and a human `(accepts session messages)` marker.

**Acceptance Criteria**:

- [ ] `workspace.SessionMapping` has a field `AcceptsSessionMessages bool` with the JSON tag `accepts_session_messages,omitempty`, with a doc comment saying it's informational only and never read by the reaper, like `KeepAlive`.
- [ ] A mapping written with `AcceptsSessionMessages: false` serializes with no `accepts_session_messages` key, and a mapping written with `true` round-trips through `WriteSessionMapping` and `ReadSessionMapping` as `true`. The existing keep-alive mapping tests and the legacy-mapping fixture in `internal/workspace/session_map_test.go` pass unchanged.
- [ ] `workspace.InstanceRecord` has a field `AcceptsSessionMessages bool` with the JSON tag `accepts_session_messages`, without omitempty. `EnumerateInstanceRecords` leaves it `false`.
- [ ] In `runDispatch`, the step 11 `workspace.SessionMapping` literal sets `AcceptsSessionMessages: inboundApplied`, next to `KeepAlive: keepAliveArmed` and before `workspace.WriteSessionMapping` runs. It's the same boolean that gates the audit line, not a separate recomputation from the flag or machine setting.
- [ ] A dispatch wiring test shows that a dispatch where the behavior takes effect writes a mapping with `"accepts_session_messages": true`. A dispatch with the behavior off, or not deliverable (for example a Codex dispatch with the flag), writes a mapping with no `accepts_session_messages` key.
- [ ] `annotateFromSessionMappings` sets `AcceptsSessionMessages = true` on every instance record where some mapping pointing at its path recorded `AcceptsSessionMessages`. No `sessionLive` or job-entry check applies to it. A unit test gives one mapping the field and no live job entry, and asserts that the instance still reports `true`.
- [ ] A unit test covers an instance with no mapping, an instance whose mapping was written without the field (an older mapping), and a workspace with no session store. Each reports `accepts_session_messages: false`, and `niwa list` / `niwa list --json` returns no error. The early return in `annotateFromSessionMappings` for an empty or unreadable store still yields `false` records.
- [ ] `niwa list --json` emits `accepts_session_messages` on every record, whether `true` or `false`. A JSON-shape test in the style of `TestAnnotateKeepAlive_JSONShape` decodes the output into maps and asserts the key is present on each record. The always-present key check in `internal/cli/list_test.go` (currently `name`, `path`, `ephemeral`) is extended to include `accepts_session_messages`. `keep_alive` stays omitted when false.
- [ ] Human `niwa list` output puts ` (accepts session messages)` after the instance name for a `true` record. When keep-alive also applies, the line reads `<name> (keep-alive) (accepts session messages)`. A `false` record gets no marker. A test in the style of `TestRunList_KeepAliveMarker` asserts all three forms.
- [ ] For a given session, the `  resume: ...` line `niwa list` prints is the same whether or not the behavior took effect, and the marker only changes the name line.
- [ ] The `--json` flag help on `listCmd` names the new field. For example, `{name, path, ephemeral, accepts_session_messages[, keep_alive]}`.
- [ ] The `listCmd` `Long` text describes `accepts_session_messages`. It says the field is on every record, is true when the instance's dispatched session was launched accepting messages from other sessions without an approval prompt, is reported whether or not the session is still running, and shows as an `(accepts session messages)` marker in the human output.
- [ ] `go test ./...` and `go vet ./...` pass.
- [ ] Must deliver: the human marker ` (accepts session messages)`, placed after ` (keep-alive)` when both apply, and the always-present `accepts_session_messages` boolean in `niwa list --json` for finished sessions as well as live ones (required by <<ISSUE:7>>).
- [ ] Must deliver: the final marker text, the JSON field name, and the no-liveness semantics as written in the `listCmd` `Long` text, so they can be documented along with how to use `niwa list --json` to find the instances to stop when withdrawing the grant (required by <<ISSUE:8>>).

**Dependencies**: Blocked by <<ISSUE:4>>

**Type**: code

### Issue 6: feat(dispatch): show the one-time explanation and remember it beside config.toml

**Complexity**: testable

**Goal**: On the first dispatch where session-message acceptance takes effect, print the one-time explanation to stderr once the developer can read it. Remember that it was shown by creating an empty marker file beside `config.toml`, and only when stderr is a terminal.

**Acceptance Criteria**:

Helper and text:

- [ ] `internal/cli/dispatch_inbound.go` declares a constant `inboundNoticeMarker = "accept-session-messages-notice"` and the helper `showInboundExplanation(w io.Writer, dir string, dirErr error, isTTY func() bool)`. The helper returns no error, so nothing it does can fail a dispatch.
- [ ] The explanation text is held as named constants in `dispatch_inbound.go`: a shared body, the terminal closing sentence, and the non-terminal closing sentence. Together they reproduce the design's Key Interfaces text exactly, with `inboundGuideURL` substituted for `<guide URL>`. The helper writes it as one line starting `niwa dispatch: note: ` and ending in a newline.
- [ ] A unit test asserts that the printed line contains each of these verbatim: `accepting messages without asking is inbound only`; `dispatching that session again with the behavior on clears it`; `Your own interactive Claude Code sessions are one such case, and they are governed by your Claude Code user settings, which niwa doesn't change`; `"Messages from your other sessions"`; `"crossSessionInbound": "accept"`; `~/.claude/settings.json`; `That change applies to every Claude Code session you run and to messages from any session able to reach yours, on this machine or elsewhere`; and the guide URL. It also asserts the line contains exactly one of the two closing sentences.
- [ ] No path this issue adds opens, stats, or writes anything under `~/.claude/`, and nothing reads or writes `config.toml`.

Helper behavior. All of these are unit tests against `t.TempDir()` that pass `isTTY` as a stub, and none of them touch the real `IsStderrTTY`:

- [ ] Terminal path: with no marker and `isTTY` returning true, the helper prints the explanation ending in `niwa won't show this again; it's also at <guide URL>`. Afterwards `<dir>/accept-session-messages-notice` exists as a regular, empty file whose `Mode().Perm()` is `0o600`. A second call with the same arguments prints nothing.
- [ ] Non-terminal path: with no marker and `isTTY` returning false, the helper prints the explanation ending in `niwa will show this again until it's been shown at a terminal; it's also at <guide URL>` and creates nothing in `dir`. A second non-terminal call prints it again. A later terminal call prints the terminal version and creates the marker.
- [ ] Marker already present: with an existing marker file, the helper prints nothing, whatever `isTTY` returns, and the file is not modified.
- [ ] Dangling symlink: when the marker path is a symlink to a nonexistent target, the helper prints nothing, and it doesn't create the target even with `isTTY` returning true. The same holds for a symlink pointing at an existing file, and that file's contents are unchanged.
- [ ] Missing directory: with `dir` set to a path under `t.TempDir()` that doesn't exist yet (two levels deep) and `isTTY` returning true, the helper prints the terminal explanation, creates the directory, and creates the marker in it.
- [ ] Unwritable directory: with `dir` at mode `0o500` and `isTTY` returning true, the helper prints the terminal explanation, doesn't panic, and creates no marker. A second call prints the explanation again. The test is skipped when `os.Geteuid() == 0` and restores the mode in cleanup.
- [ ] Unsearchable directory: with `dir` at mode `0o000`, where `os.Lstat` fails with a permission error, the helper treats the marker as absent and prints the explanation without panicking. Skipped as root.
- [ ] Unresolvable configuration path: with `dirErr` non-nil, the helper prints the explanation with the non-terminal closing sentence even when `isTTY` returns true, and creates nothing. The test stubs `isTTY` to record whether it was called and asserts that it wasn't.
- [ ] Concurrent first calls: 8 goroutines calling the helper at once against the same missing marker, all with `isTTY` returning true, all return. Afterwards exactly one marker exists, empty and at mode `0o600`, and at least one goroutine's writer received the explanation. The test passes under `go test -race`.

Wiring in `runDispatch`. These tests drive `runDispatch` with the existing stubs for launch, capture, and `dispatchAttach`. They stub `IsStderrTTY` with save and restore, as `dispatch_prompt_test.go` does, and set `XDG_CONFIG_HOME` to a `t.TempDir()`:

- [ ] The explanation is shown only when `inboundApplied` is true. A dispatch where the behavior is off (no key and no flag, or the key `true` with `--accept-session-messages=false`) prints no explanation and creates no marker. So does a dispatch where the behavior isn't deliverable, such as a Codex dispatch with the flag.
- [ ] Detached path: with `--detach`, the behavior on, and `IsStderrTTY` stubbed true, the explanation is the stderr line directly after the audit line. The marker exists under `$XDG_CONFIG_HOME/niwa/` when `runDispatch` returns, and `dispatchAttach` is not called.
- [ ] Foreground path: a foreground launch, driven the way `dispatch_launchmode_test.go` drives one, prints the explanation directly after the audit line and before step 14's "the turn ended" line.
- [ ] Attach path, success: without `--detach`, with the behavior on and `IsStderrTTY` stubbed true, a `dispatchAttach` stub captures the stderr buffer and runs `os.Lstat` on the marker path at the moment it's called. At that moment stderr holds the audit line but no explanation, and the marker doesn't exist. After `runDispatch` returns, stderr ends with the explanation and the marker exists.
- [ ] Attach path, failure: the same setup with a `dispatchAttach` stub that returns an error. The existing `niwa: warning: could not attach to session` and `niwa: the session is running; attach later with:` lines still print, unchanged. The explanation prints after the attach call returns, and the marker exists when `runDispatch` returns. `runDispatch` still returns nil.
- [ ] Non-terminal dispatch: with `IsStderrTTY` stubbed false and `--detach`, the dispatch prints the non-terminal explanation and creates nothing under `$XDG_CONFIG_HOME/niwa/` beyond what the test itself placed there.
- [ ] `XDG_CONFIG_HOME` is honored: with it set, the marker is created at `$XDG_CONFIG_HOME/niwa/accept-session-messages-notice`, and nothing is created under `$HOME/.config/niwa/` (the test points `HOME` at a separate `t.TempDir()`). With `XDG_CONFIG_HOME` unset and `HOME` set, the marker is created under `$HOME/.config/niwa/`.
- [ ] Missing configuration directory at dispatch: with `XDG_CONFIG_HOME` pointing at an empty temp directory (so `niwa/` doesn't exist), `--accept-session-messages`, `--detach`, and `IsStderrTTY` stubbed true, the dispatch succeeds and creates both `$XDG_CONFIG_HOME/niwa/` and the marker.
- [ ] Failed launch: when the launch stub returns an error, when capture fails, or when `WriteSessionMapping` fails, all with the behavior on and `IsStderrTTY` stubbed true, stderr contains no explanation and no marker is created.
- [ ] Marker creation never fails a dispatch: with `$XDG_CONFIG_HOME/niwa/` at mode `0o500` and `IsStderrTTY` stubbed true, the dispatch returns nil and prints the explanation, and a second dispatch prints it again. Skipped as root.
- [ ] `config.toml` is unchanged: a test writes `$XDG_CONFIG_HOME/niwa/config.toml` with a comment line and `accept_session_messages_on_dispatch = true` under `[global]`, runs a dispatch that prints the explanation and creates the marker, and asserts that the file's bytes and modification time are the same afterwards.
- [ ] Stdout is unchanged: for the same stubbed dispatch, stdout has no explanation text, whether `IsStderrTTY` is stubbed true or false.
- [ ] Tests that stub `IsStderrTTY` or `dispatchAttach`, or that set `XDG_CONFIG_HOME`, restore them in `t.Cleanup`. The existing tests in `dispatch_contract_test.go`, `dispatch_launchmode_test.go`, and `dispatch_prompt_test.go` pass unchanged.
- [ ] `dispatch_inbound.go` stays in `dispatchPathFiles` in `internal/cli/dispatch_layout_test.go`, and the layout scan passes. The helper names no agent.
- [ ] `go test ./...` and `go vet ./...` pass, and the changed files are gofmt-clean.

Downstream deliverables:

- [ ] Must deliver: a built `niwa` binary that, on a Claude dispatch where the behavior takes effect, writes the explanation to stderr with the terminal or non-terminal closing sentence exactly as the design's Key Interfaces section gives it. At a terminal it creates the empty file `$XDG_CONFIG_HOME/niwa/accept-session-messages-notice`, and it leaves `config.toml` byte-unchanged. With `--detach`, the explanation comes right after the audit line, so the functional scenarios that run under the `script` pty helper can see it in the combined transcript (required by <<ISSUE:7>>)
- [ ] Must deliver: the final marker file name `accept-session-messages-notice`, its location beside `config.toml` (the directory of `config.GlobalConfigPath()`, following `XDG_CONFIG_HOME`), and the rule that the explanation shows whenever the marker is absent. Deleting the marker shows the explanation again, and creating it by hand (even as an empty file or a symlink) suppresses it. The verbatim explanation text lives in named constants the guide can quote (required by <<ISSUE:8>>)

**Dependencies**: Blocked by <<ISSUE:4>>

**Type**: code

### Issue 7: test(functional): cover dispatch session-message acceptance end to end

**Complexity**: testable

**Goal**: Cover `niwa dispatch --accept-session-messages` and the `[global] accept_session_messages_on_dispatch` machine setting end to end. That means `@critical` functional scenarios for every case in the PRD's functional-coverage requirement, including four parallel dispatches from a terminal. It also means scenarios for the configuration sources that must not turn the behavior on, for the Codex warning, and for a prompt beginning with `--settings=`, plus a unit test that shows the argv Claude receives from either `niwa watch` launch site never carries `crossSessionInbound`.

**Acceptance Criteria**:

**Harness and step definitions**
- [ ] The fake `claude`'s `--bg` branch also writes each argv element NUL-separated (for example `printf '%s\0' "$@"`) to a new file `$HOME/dispatch-launch-argv-elements`. The existing `$HOME/dispatch-launch-argv` line and every step that reads it keep working unchanged.
- [ ] The fake `claude` gains an opt-in mode, selected by a new step such as `a fake claude for dispatch that mints a new session per launch`, in which each `--bg` invocation generates its own valid UUID (for example from `/proc/sys/kernel/random/uuid`) instead of using `FAKE_CLAUDE_SESSION_ID` or the fixed default, and writes its job state under that id's short form. Four concurrent invocations produce four distinct session ids and four job-state files. Scenarios that don't select the mode behave as before.
- [ ] New step definitions live in a new file `test/functional/session_message_steps_test.go`, registered from `initializeScenario` through a `registerSessionMessageSteps` function, the way `registerKeepAliveSteps` is. At least these steps exist, with these patterns or ones that differ only in wording:
  - `the niwa machine config global table contains:` (docstring). It adds the given lines to the `[global]` table of `$XDG_CONFIG_HOME/niwa/config.toml` as the scenario environment resolves it, creating the table if it's missing and keeping the registry entries `niwa init` wrote. It never produces a second `[global]` header.
  - `the niwa machine config is replaced with:` (docstring), which writes the file verbatim (for the invalid-TOML fixture), and `the niwa machine config is not readable`, which sets mode 000.
  - `I record the niwa machine config` and `the niwa machine config is byte-for-byte unchanged`.
  - `the launched claude settings document has crossSessionInbound "accept"` and `the launched claude settings document has no crossSessionInbound`. Both read `$HOME/dispatch-launch-argv-elements` and take the element after the only `--settings` element that comes before the `--` prompt separator. The first step parses that element as JSON and checks the key. The second passes when that element parses and lacks the key, or when there's no `--settings` element. Both fail when more than one `--settings` element comes before `--`.
  - `the launched claude settings document has remoteControlAtStartup true`.
  - `the launched claude prompt is "..."`, which asserts that the element right after `--` equals the given text and is the last element.
  - `the session-message notice marker exists` and `the session-message notice marker does not exist`, checked with `os.Lstat` at `$XDG_CONFIG_HOME/niwa/accept-session-messages-notice`.
  - `the dispatch mapping for session "..." records session-message acceptance`, which reads `.niwa/sessions/<id>.json` like `readMappingKeepAlive` and requires `accepts_session_messages: true`. Its counterpart `... does not record session-message acceptance` requires the key to be absent, because the field is omitempty.
  - `the list JSON reports the dispatch instance as accepting session messages` and `... as not accepting session messages`. The second requires the field to be present and `false`.
  - `the error output has exactly (\d+) lines? containing "..."`, which counts matching lines in `s.stderr`. With a count of 0 it's the "no such line" assertion.
  - `the error output contains the text:` and `the error output does not contain the text:` (docstring), for strings that contain double quotes.
  - `I remember the dispatch standard output` and `the dispatch standard output matches the remembered one apart from instance names and session identifiers`. Before comparing, the second step replaces dispatch instance names (the `dispatchInstanceNameRe` shape) and UUIDs or short session ids with placeholders.
  - `the scenario is skipped when tests run as root`, which returns `godog.ErrSkip` when `os.Geteuid() == 0`.
- [ ] A parallel pty step, for example `I run "..." (\d+) times in parallel under a pty`, starts the given number of copies of the command at once, each under its own `script` pty the same way `iRunUnderPTYWithInput` does it, and waits for all of them within the pty step timeout. The single-run pty step and the parallel step share one extracted helper for building and running the `script` command, not two copies of it. The parallel step keeps each run's exit code and merged transcript separately. It's paired with at least these steps:
  - `all parallel runs exit 0`, which fails naming each run that exited non-zero, with its transcript;
  - `at least one parallel transcript contains the text:` (docstring);
  - `every parallel transcript has exactly (\d+) lines? containing "..."`;
  - `there are (\d+) dispatch mappings that record session-message acceptance`, which counts the `*.json` mapping files in the workspace root's `.niwa/sessions/` directory (where `WriteSessionMapping` writes every dispatch mapping, not per instance) that have `accepts_session_messages: true`, and checks their session ids are distinct.
- [ ] The scenarios live in a new feature file, `test/functional/features/session-message-acceptance.feature`. Its description names the design doc and explains that real delivery between live sessions is covered by the PRD's manual delivery check, not here. Every dispatch in it passes `--detach`.

**`@critical` scenarios (one per functional-coverage case)**
- [ ] **Off by default.** With no `accept_session_messages_on_dispatch` key and no flag, then again in a second scenario or example row with the key set to `false`, `niwa dispatch <task> --detach` exits 0. The launched claude settings document has no `crossSessionInbound`. The error output has 0 lines containing `accepts messages from other sessions without asking`. The mapping doesn't record session-message acceptance. `niwa list --json` reports the instance as not accepting session messages.
- [ ] **On by flag.** With the key absent, `--accept-session-messages` and, in a second example, `--accept-session-messages=true` each produce a launched claude settings document with crossSessionInbound `"accept"`. The error output has exactly 1 line containing `accepts messages from other sessions without asking`, and that line contains `(source: --accept-session-messages)` and the guide URL. The mapping records session-message acceptance. `niwa list --json` reports the instance as accepting session messages. `niwa list` output contains `(accepts session messages)`.
- [ ] **On by machine setting.** With `accept_session_messages_on_dispatch = true` and no flag, the document has crossSessionInbound `"accept"`. The error output has exactly 1 line containing `accepts messages from other sessions without asking`, and it contains `(source: machine setting accept_session_messages_on_dispatch)` and the guide URL. The error output has 0 lines containing `keeps Claude Code's default for messages from other sessions`. With `--accept-session-messages=true` added, there is still exactly 1 audit line and 0 override lines.
- [ ] **Machine setting on, flag off.** With the key `true` and `--accept-session-messages=false`, the document has no `crossSessionInbound`. There are 0 audit lines, and exactly 1 line that contains both `keeps Claude Code's default for messages from other sessions` and `--accept-session-messages=false`. The mapping doesn't record acceptance. A second example with the key absent and `--accept-session-messages=false` has 0 audit lines and 0 override lines.
- [ ] **One-time explanation and marker.** This one runs under the pty helper with the flag on and no marker present:
  - Before the first dispatch the scenario records the niwa machine config.
  - The first `niwa dispatch <task> --accept-session-messages --detach` under a pty exits 0. Its transcript contains, verbatim through the docstring step, each of these sentences from the design's explanation:
    - the inbound-only sentence ending "dispatching that session again with the behavior on clears it";
    - the interactive-sessions sentence;
    - the `/config` sentence naming `"Messages from your other sessions"` and `"crossSessionInbound": "accept"`;
    - the "applies to every Claude Code session you run" sentence;
    - `niwa won't show this again; it's also at <guide URL>`.
  - After the first dispatch the marker exists and the niwa machine config is byte-for-byte unchanged.
  - A second pty dispatch, which provisions a fresh instance, exits 0, still has exactly 1 audit line, and its transcript doesn't contain `accepting messages without asking is inbound only`.
- [ ] **Explanation without a terminal.** In a companion `@critical` scenario, a non-terminal dispatch (`I run "..." from the workspace root`) with the flag on prints the explanation with `niwa will show this again until it's been shown at a terminal; it's also at <guide URL>`, and the marker doesn't exist afterwards. A following pty dispatch prints the explanation with the "won't show this again" sentence and creates the marker.
- [ ] **Four parallel dispatches from a terminal.** With the fake claude minting a new session per launch, the flag on, and no marker (asserted with `the session-message notice marker does not exist` before the runs), the scenario records the niwa machine config and then runs `niwa dispatch <task> --accept-session-messages --detach` 4 times in parallel under a pty, against the one scenario configuration directory. Afterwards:
  - all four runs exit 0;
  - the marker exists;
  - at least one transcript contains the inbound-only sentence of the explanation verbatim;
  - every transcript has exactly 1 line containing `accepts messages from other sessions without asking`;
  - there are 4 dispatch mappings that record session-message acceptance, with 4 distinct session ids;
  - the niwa machine config is byte-for-byte unchanged.
- [ ] **Agent that can't receive it.** A Codex dispatch (`niwa dispatch <task> --harness codex --accept-session-messages --detach`, with the fake codex) exits 0. The error output contains the text `niwa dispatch: --accept-session-messages does not apply to the "codex" agent and was ignored.` The codex launch argv doesn't contain `crossSessionInbound`. There are 0 audit lines, and the output doesn't contain `accepting messages without asking is inbound only`. The marker doesn't exist. The mapping doesn't record session-message acceptance.
  - A second scenario or example sets only `accept_session_messages_on_dispatch = true` and adds no flag. It prints no `does not apply to the` warning, no audit line, and no explanation, creates no marker, and doesn't carry the setting.
  - With `--accept-session-messages=false` added to that machine-setting case, it also prints no override line.

**Additional scenarios (not required to be `@critical`)**
- [ ] **Configuration sources can't turn it on.** With the machine key absent and no flag, each fixture below dispatches with no `crossSessionInbound` in the launched claude settings document and no audit line:
  - `accept_session_messages_on_dispatch = true` in the workspace's `workspace.toml`, once in its top-level or `[workspace]` table and once in a per-repository table;
  - `crossSessionInbound = "accept"` under `[claude.settings]` in `workspace.toml`;
  - `"crossSessionInbound": "accept"` in the `.claude/settings.json` at the workspace root;
  - the same key in a cloned repository's committed `.claude/settings.json`, and in its `.claude/settings.local.json`.

  For each fixture, the settings files niwa materializes into the dispatch instance don't contain `crossSessionInbound`. Those are the instance-root `.claude/settings.json` and the `.claude/settings.local.json` niwa writes into each repository, but not a file the fixture itself committed. An unknown-key warning from `workspace.toml` is allowed, and the dispatch still exits 0. The repository fixtures reuse the existing `a source repo "..." exists with the staged files` and `a staged file "..." with body:` steps from `codex_agent_steps_test.go`.
- [ ] **Coexistence with other launch configuration.** The machine config sets `remote_control_on_dispatch = true`, `keep_alive_on_dispatch = true`, and `accept_session_messages_on_dispatch = true`, the workspace declares a bypass posture, and `ANTHROPIC_API_KEY` is removed from the scenario environment (extend `buildEnv`'s filter through `envOverrides` or a new field if needed). The recorded argv has exactly one `--settings` element before `--`, and that document has both crossSessionInbound `"accept"` and remoteControlAtStartup `true`. The launched claude was invoked with `--permission-mode bypassPermissions`, the prompt contains the keep-alive arming instruction, and the mapping records keep-alive.
- [ ] **A prompt beginning with `--settings=`.** With the flag on, the scenario dispatches the prompt `--settings={}`. It goes through niwa's own `--` (`niwa dispatch --accept-session-messages --detach -- --settings={}`), or through the pty capture if dispatch doesn't accept a positional prompt after `--`. The launched claude prompt is `--settings={}`, and the launched claude settings document (the one before `--`) has crossSessionInbound `"accept"`. There is exactly 1 audit line, and the mapping records session-message acceptance.
- [ ] **Launch failure.** With the flag on and `a fake claude for dispatch that fails to launch`, dispatch exits non-zero. There are 0 audit lines, the output doesn't contain `accepting messages without asking is inbound only`, the marker doesn't exist, and no dispatch-origin mapping remains.
- [ ] **Stdout unchanged.** In a non-terminal scenario, the stdout of a dispatch with the flag on matches the stdout of the same dispatch with it off, apart from instance names and session identifiers. That covers the printed resume commands.
- [ ] **Unwritable configuration directory.** The scenario starts with `the scenario is skipped when tests run as root`. After `niwa init`, it makes `$XDG_CONFIG_HOME/niwa` mode 0555 and restores it in cleanup. A pty dispatch with the flag on exits 0, prints the explanation, and leaves no marker. A second pty dispatch prints the explanation again.
- [ ] **Unreadable machine configuration.** Each fixture is dispatched without the flag and then with `--accept-session-messages`. Without the flag the behavior stays off with no audit line. With the flag the document has crossSessionInbound `"accept"` and exactly 1 audit line naming `(source: --accept-session-messages)`. The fixtures:
  - a `config.toml` with mode 000, preceded by `the scenario is skipped when tests run as root`;
  - a `config.toml` that isn't valid TOML;
  - `accept_session_messages_on_dispatch = "yes"`.
- [ ] **Personal settings untouched.** With the flag on and `$HOME/.claude/settings.json` present, the file's contents and modification time are unchanged after a dispatch. With the file at mode 000 (preceded by `the scenario is skipped when tests run as root`), the dispatch exits 0 and the error output doesn't contain `settings.json`.

**Watch-site unit test**
- [ ] A unit test in `internal/cli` (in `watch_test.go` or a new `watch_inbound_test.go`) observes the argv Claude receives at both `niwa watch` launch sites. It leaves `dispatchLaunch` set to `realDispatchLaunch` and doesn't replace `realDispatchLaunch` or `buildLaunchArgs`:
  - Setup: `XDG_CONFIG_HOME` points at a `t.TempDir()` whose `niwa/config.toml` sets `[global] accept_session_messages_on_dispatch = true`. The variable behind `--accept-session-messages` is set to on and restored afterwards, through the shared flag-reset helper in `dispatch_test.go`. `HOME` points at a `t.TempDir()`.
  - A fake `claude` script in a `t.TempDir()` bin directory is placed first on `PATH` with `t.Setenv`. It writes every argv element NUL-separated to a per-invocation file, and for `--bg` also writes a job state for the instance directory under `$HOME/.claude/jobs/`, the way the functional fake does. Any other invocation exits non-zero.
  - It drives both real launch sites. The fresh-stage launch is `stageReview` (near `watch.go:844`). The resume launch is `continueReview` (near `watch.go:581`), with a staged record and a live job state seeded so its liveness check passes. GitHub calls go to an `httptest` server through `github.APIClient.BaseURL`, and the PR-head fetch goes to a local bare repository or through a seam described below. `provisionInstanceFunc`, `stopSessionFunc`, and `watchCapture` may be replaced, because they sit outside the launch.
  - It asserts the fake `claude` recorded exactly one `--bg` invocation per site. The continue site's recorded argv contains `--resume` followed by the seeded session id, and each site's last element is the site's prompt, which shows the recording is the real launch. No recorded argv element at either site contains `crossSessionInbound`.
- [ ] If recording at the process boundary isn't workable, the test may instead replace a new package-level seam at the exec/start point inside `realDispatchLaunch`, below `buildLaunchArgs`, that receives the final binary path and argv. The seam must preserve behavior: production code calls the same `exec` path with the same arguments, the existing `dispatch_promptsize_test.go` and launcher tests pass unchanged, and the seam's default is the real exec.
- [ ] If either watch function can't be driven in a unit test as it stands, the change adds only behavior-preserving seams: package-level function variables for the GitHub, PR-head fetch, or settings calls around the launch, following `stopSessionFunc` and `provisionInstanceFunc`. It doesn't copy the passthrough-building or argv-building code into the test. The test fails if `stageReview`, `continueReview`, `realDispatchLaunch`, or `buildLaunchArgs` starts adding the key. Existing watch tests pass unchanged.

**Suite health**
- [ ] Existing scenarios in `dispatch.feature`, `keep-alive.feature`, and `codex-agent.feature` pass. Any argv expectation that changes because Claude's prompt now follows `--` was already updated by the issue that introduced the separator and isn't reworked here.
- [ ] `make test-functional-critical` passes with the new `@critical` scenarios included, and the parallel scenario passes in 5 consecutive runs. `make test-functional`, `go test ./...`, and `go vet ./...` pass, and every changed Go file is gofmt-clean.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:4>>, <<ISSUE:5>>, <<ISSUE:6>>

**Type**: code

### Issue 8: docs(guide): document session-message acceptance and record the manual check

**Complexity**: testable

**Goal**: Write `docs/guides/session-message-acceptance.md` covering everything the PRD's documentation requirement lists, add it to the contributor-guide index, check the flag's help and completion, and run the manual delivery check along with the design's extra measurements. Those measurements include a detached-launch smoke check and a real review session trying to reach an accepting worker. Their results set the guide's Claude Code version line and its description of who can send, and they block the merge if the code doesn't behave as designed.

**Acceptance Criteria**:

- [ ] `docs/guides/session-message-acceptance.md` exists, and `inboundGuideURL` in `internal/cli/dispatch_inbound.go` equals `https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md`, so the path the URL names is the file this issue adds.
- [ ] The guide's `##` headings match the keep-alive guide's five top-level headings one for one and in the same order. `## Opting in`, `## How it works`, and `## Validation` are verbatim. The two headings specific to keep-alive are adapted to this feature: the one about seeing which instances accept messages takes the place of "Seeing what is kept alive", and the one about withdrawing the grant takes the place of "Releasing keep-alive: close, don't archive". There are no other `##` headings; everything else sits under `###` headings inside them.
- [ ] The guide covers each item R17 lists:
  - [ ] the `[global] accept_session_messages_on_dispatch` machine key in `config.toml` (under `~/.config/niwa/`, or `$XDG_CONFIG_HOME/niwa/` when set), the tri-state `--accept-session-messages[=true|false]` flag, and the precedence: flag, then machine setting, then off. It also says no workspace config, instance setting, `[claude.settings]` entry, or settings file a repository carries can turn the behavior on or off, and that there's no `niwa config set` for the key;
  - [ ] the audit line, with both source spellings (`machine setting accept_session_messages_on_dispatch` and `--accept-session-messages`), and the override line, quoted as `niwa dispatch` prints them;
  - [ ] the `(accepts session messages)` marker in `niwa list` and the always-present `accepts_session_messages` field in `niwa list --json`, including that it stays `true` after the session finishes and that it means niwa launched the worker with the setting, not that Claude Code confirmed it;
  - [ ] that the behavior is inbound only: a worker's message into a session launched without it still waits there when the two are in different permission-mode classes, and dispatching that session again with the behavior on clears it;
  - [ ] that `--accept-session-messages=false` keeps Claude Code's default, which delivers messages between sessions of the same class, so it doesn't isolate a worker;
  - [ ] that sessions `niwa watch` launches or resumes never receive the behavior;
  - [ ] the behavior for agents that can't receive it: for Codex, the flag prints the warning and is ignored, the machine setting alone prints nothing, and in both cases the behavior doesn't take effect;
  - [ ] that turning the machine setting off doesn't reach sessions already dispatched, because Claude Code reapplies their launch settings on `claude respawn` and `claude attach`;
  - [ ] that a worker stopped with `claude stop` receives nothing, because the send fails, until something reopens it;
  - [ ] that a foreground `claude --resume` of a dispatched conversation doesn't carry the behavior;
  - [ ] the marker path, `accept-session-messages-notice` beside `config.toml`; that deleting it shows the explanation again and creating it by hand suppresses it; that it's created only when a terminal showed the explanation; and that a developer who only dispatches through agents sees the explanation repeatedly until then;
  - [ ] the step for the developer's own interactive sessions: the "Messages from your other sessions" row in Claude Code's `/config`, or `"crossSessionInbound": "accept"` in `~/.claude/settings.json`. It gives every cost R10 names: the change applies to every Claude Code session the developer runs, and to messages from any session able to reach theirs, on this machine or elsewhere. It also says niwa never makes the change;
  - [ ] the Claude Code version the manual delivery check last passed on, stated as a version line under `## Validation`.
- [ ] The guide covers the design's Security Considerations:
  - [ ] the recommended posture: turn the behavior on only on machines where every session sharing the Claude Code account is one the developer would let direct a bypass-mode worker, and prefer the per-dispatch flag to the machine key when only some fan-outs need it;
  - [ ] withdrawing the grant: list the instances reporting `"accepts_session_messages": true` with `niwa list --json` and stop their sessions, since turning the key off doesn't reach them;
  - [ ] the two relocation routes: a workspace's `[claude.env]` or `[session.env]`, or an overlay's `[env]`, setting `XDG_CONFIG_HOME` or `HOME`, which changes which `config.toml` a nested `niwa dispatch` reads. Relocating `HOME` also moves the Claude Code user settings a nested session reads, and can grant acceptance with no audit line or record;
  - [ ] that any agent can pass the flag, so a steered worker can dispatch more accepting workers;
  - [ ] the review-session deny hook: the denied list, the Claude Code version it was taken from, and the refusal message `niwa watch: review sessions don't reach other sessions`; that with `watch_sandbox = off` it's accident prevention only, because Bash has full access; that in sandbox mode, Bash is kept off the local inbox because niwa's no-egress stanza leaves unix sockets disallowed; that `disableAllHooks` in any settings source turns it off along with every other review hook; and that a review staged before the upgrade runs without the hook until watch re-stages it, so in-flight reviews should re-stage before the behavior is turned on.
- [ ] The guide describes who can send according to the inbox measurement. If a process that isn't Claude Code could send over the local inbox, the guide says any local process running as the developer can reach an accepting worker, not only sessions on the account. If it couldn't, the guide says the sender set is sessions able to address the worker by name across the developer's whole Claude Code account, including other machines and the cloud. Either way it notes that worker names are predictable, because niwa uses the dispatch name as the display name.
- [ ] `CLAUDE.md`'s Contributor Guides list gains a `docs/guides/session-message-acceptance.md — ...` entry in the same form as its neighbors, placed right after the `session-keep-alive.md` entry. The entry names the key, the flag, the audit and `niwa list` surfaces, and the manual check.
- [ ] `niwa dispatch --help`, run against a freshly built binary, lists `--accept-session-messages` with the help text the design fixes ("accept messages from other Claude Code sessions without an approval prompt; overrides the [global] accept_session_messages_on_dispatch machine setting in either direction"). `niwa __complete dispatch --acc` offers `--accept-session-messages`. If either is missing, the registration from <<ISSUE:4>> is fixed in this PR.
- [ ] Detached-launch smoke check, run as soon as <<ISSUE:3>>'s separator change is on the branch and before the rest of the work builds on it. Using a binary built from that commit, a real `niwa dispatch --detach` of a Claude worker produces a `claude --bg ... -- <prompt>` launch (confirmed from the launch argv). The session starts, shows up in `claude ls`, and `claude logs <id>` shows it took the prompt as its task rather than reporting an unknown option or starting with no task. A prompt beginning with `--settings=` is also dispatched this way, and the worker starts with niwa's settings document in force. The Claude Code version and the outcome are recorded in the PR description. A failure blocks the merge until the launch shape from <<ISSUE:3>> is fixed and the smoke check passes on the fixed commit.
- [ ] The PRD's manual delivery check is run with its preconditions: Claude Code version recorded, no `crossSessionInbound` or bypass default in the tester's user settings, no managed settings file, a workspace declaring `permissions = "bypass"`, and a sender started with `claude --bg` and no permission-mode flag. Each case gets the outcome the PRD expects: case 0 held, case 0b held after respawn and after stop and reopen, case 1 delivered, case 2 delivered after `claude respawn`, case 2b delivered after `claude stop` and reopen, case 2c send fails, case 3 held in the sender, case 4 delivered. A case with any other outcome blocks the merge.
- [ ] Review-session refusal check, which blocks the merge. With a binary built from this branch, `niwa watch` freshly stages a review session twice: once in the sandbox posture and once with `watch_sandbox = off`. A worker dispatched with `--accept-session-messages` is running, and the check first confirms that plain `claude --bg` sender S can deliver to it. In each review session, the tester tells the session in its own conversation to call `SendMessage` to that worker with a distinctive text, and separately to call `ListAgents`. For each posture:
  - [ ] the `SendMessage` call is refused, and the tool result the session sees contains `niwa watch: review sessions don't reach other sessions`;
  - [ ] the `ListAgents` call is refused with the same message and returns no session list;
  - [ ] `claude logs <worker>` shows no incoming message with the distinctive text within 60 seconds of the attempt.
  The posture, the Claude Code version, and each call's outcome are recorded in the PR description. If either call gets through in either posture, or anything reaches the worker, the matcher or hook from <<ISSUE:1>> in `internal/watch/containment.go` is fixed in this PR, with its unit tests updated to match, and the check is repeated until both postures pass.
- [ ] The same session records the other measurements from the design's Phase 6:
  - [ ] the permission class a review session in the hard-deny posture actually runs in;
  - [ ] whether a review session's subagents are blocked by the session-reach deny hook, measured by telling a subagent the review session starts to call `SendMessage` to the accepting worker. If they aren't blocked, the guide states it as a limit of the hook;
  - [ ] whether a process that isn't Claude Code can send over the local inbox; this sets the sender-set wording above;
  - [ ] Claude Code's tool list, taken from the init event of `claude -p --output-format stream-json`, compared against `SendMessage|SendFile|RemoteTrigger|ListAgents`. If a tool that reaches another session is missing from the denied list, the matcher from <<ISSUE:1>> is updated in this PR with its unit tests, the review-session refusal check is repeated, and the guide's list matches the new matcher.
- [ ] The smoke check result, the manual check's case results, the review-session refusal results, the Claude Code version, and the other measurements are all recorded in the PR description. They aren't committed in any file under a temporary directory. The guide carries only the version line, the denied list, and the conclusions the measurements set.
- [ ] If the manual check forced a code change, `go test ./...` and `go vet ./...` pass and `gofmt -l .` prints nothing after the change.
- [ ] The guide follows the repository's public-docs rules: no links to private resources, no internal tooling names, no emojis, and no reference to any temporary workflow path.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:4>>, <<ISSUE:5>>, <<ISSUE:6>>

**Type**: docs


## Dependency Graph

## Implementation Sequence

**Critical path:** unit 3, then unit 4, then unit 6, then unit 7 (four units).

**Recommended order:**

1. **Units 1, 2 and 3, in any order.** They share no files. Unit 3 goes first
   when possible, because it changes every Claude launch argv: as soon as it
   lands, run the detached smoke check unit 8 names (a real
   `claude --bg ... -- <prompt>` launch starts and takes the prompt as its task).
   If that check fails, stop and revisit Decision 6 before building on it.
2. **Unit 4.** Needs units 2 and 3. This is the largest unit and the one that
   turns the behavior on; keep the capability-table changes in the same commit.
3. **Units 5 and 6, in parallel.** Both need unit 4's `inboundApplied`. They edit
   different regions of `runDispatch` (the step 11 literal versus the audit-line
   and step 14 call sites).
4. **Units 7 and 8, in parallel.** Both need units 1, 4, 5 and 6. Unit 8's
   manual delivery check is merge-blocking: a case with an unexpected outcome, a
   review-session deny that lets a call through, or a session-reaching tool
   missing from the denied list is fixed in this pull request before it merges.

Each unit is a separately reviewable commit in the one pull request.
