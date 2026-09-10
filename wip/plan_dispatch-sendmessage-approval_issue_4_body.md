---
complexity: critical
complexity_rationale: This is the change that makes dispatched workers accept messages from other sessions without an approval prompt, so a wrong precedence, eligibility, or rendering decision silently removes a human checkpoint on bypass-mode workers.
---

## Goal

Add the tri-state `--accept-session-messages` flag and the `DispatchInboundAcceptance` capability row. Resolve the flag over the `[global] accept_session_messages_on_dispatch` machine key into one `inboundApplied` boolean, add `crossSessionInbound: "accept"` to the single launch `--settings` document when that boolean is true, and print the audit, override, and warning lines on stderr.

## Context

Design: `docs/designs/DESIGN-dispatch-sendmessage-approval.md`

Claude Code holds a message from another session whenever the two sessions run in different permission-mode classes, unless the receiving session's `crossSessionInbound` setting is `accept`. The only channel for that setting that niwa is allowed to use is the launch `--settings` document. Remote control already occupies that document, and a second `--settings` element silently replaces the first. The config key (<<ISSUE:2>>) and the one-map launch settings rendering plus the Claude prompt separator (<<ISSUE:3>>) are the groundwork. This issue is the point where the behavior actually turns on. It covers the design's Decision 2 and Implementation Approach Phase 3, the Decision Outcome summary (the flag, `inboundResolution`, the `hostGlobal` hoist, `inboundApplied`, the override condition, and the stderr line placed between steps 12 and 13), the Key Interfaces strings, and Data Flow steps 1 to 6.

Resolution order is the flag, then the machine setting, then off (PRD R1-R3). Eligibility goes through a new capability row rather than an agent name, because `internal/cli/dispatch_layout_test.go` fails on any agent constant or whole-literal agent name in the dispatch path. Claude declares the row implemented. Codex declares it unavailable, because it has no setting for accepting messages from other sessions, and its generated gap list gains that entry. Adding a row moves fixed counts in several tests and documents, and `internal/agentplan/declaration.go` requires a row to flip in the same change that delivers it ("never before"). So the row, the count updates, the regenerated gap list, and the capability-contract PRD amendment all land in the same commit as the delivery.

The resolution lives only in `runDispatch`. `niwa watch` calls `dispatchLaunch` directly and must never receive the behavior (R14), so nothing moves into `dispatchLaunch`.

## Acceptance Criteria

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

- [ ] When `inboundApplied` is true, the line written after step 12 (mapping written, rollback disarmed) and before step 13's stdout hints is exactly `niwa dispatch: this worker accepts messages from other sessions without asking (source: machine setting accept_session_messages_on_dispatch); see <inboundGuideURL>` for the machine source. With the flag as the source, the parenthetical reads `(source: --accept-session-messages)`. It appears exactly once, and never when `inboundApplied` is false (R7).
- [ ] When the resolution's `overrodeMachineOn` is true and the behavior was deliverable, the line at that same point is exactly `niwa dispatch: this worker keeps Claude Code's default for messages from other sessions (--accept-session-messages=false overrides the machine setting)`. No override line prints for the key absent with `=false`, for the key `true` with `=true` (which prints the audit line only), or for a non-deliverable agent (R8).
- [ ] When the flag asked for the behavior (flag true) and it isn't deliverable, step 9c prints `niwa dispatch: --accept-session-messages does not apply to the %q agent and was ignored. %s` with the agent name and the declaration's reason. For a Codex dispatch that line names `"codex"`, the launch carries no `crossSessionInbound`, and no audit line prints. When only the machine key asked for a Codex dispatch, nothing prints, with or without `--accept-session-messages=false` (R13).
- [ ] When the stubbed `dispatchLaunch` returns an error with the behavior on, `runDispatch` returns before step 11, and stderr carries no audit line and no override line.
- [ ] Stdout is byte-identical between a dispatch with the behavior on and the same dispatch with it off, apart from the instance path and session id (R16). The printed resume commands stay `claude attach <id>`.
- [ ] Nothing the resolution adds reaches `dispatchLaunch`'s own logic or any `niwa watch` launch path: the new code is confined to `runDispatch` and `dispatch_inbound.go`.

Downstream deliverables:

- [ ] Must deliver: an `inboundApplied` boolean declared in `runDispatch` and in scope at the step 11 `workspace.SessionMapping` literal, set before `WriteSessionMapping` runs (required by <<ISSUE:5>>).
- [ ] Must deliver: the audit-line call site between steps 12 and 13, structured so the explanation can be called right after it when no attach follows, plus `inboundApplied` still in scope at step 14's `dispatchAttach` branches, and the package-level `inboundGuideURL` constant (required by <<ISSUE:6>>).
- [ ] Must deliver: the working `--accept-session-messages` flag, the machine key wired through `hostGlobal`, and the audit, override, and warning strings exactly as above, so the functional scenarios can assert them verbatim (required by <<ISSUE:7>>).
- [ ] Must deliver: the final flag help text above and the three stderr strings, unchanged after this issue, for the guide to quote (required by <<ISSUE:8>>).

Build:

- [ ] `go test ./...` and `go vet ./...` pass, and the code is gofmt-clean.

## Dependencies

Blocked by <<ISSUE:2>>, <<ISSUE:3>>

## Downstream Dependencies

<<ISSUE:5>> writes `AcceptsSessionMessages: inboundApplied` into the step 11 mapping literal and reports it in `niwa list`, so it needs the boolean computed before the mapping write and not recomputed later. <<ISSUE:6>> adds `showInboundExplanation` and calls it either right after the audit line (when no attach follows) or after `dispatchAttach` returns. It needs a clear call site next to the audit line, `inboundApplied` available at step 14, and `inboundGuideURL` defined once in `dispatch_inbound.go`. <<ISSUE:7>> drives the flag and the machine key end to end through the fake `claude` and asserts the recorded argv and the stderr strings verbatim, so the strings must be the exact Key Interfaces text. <<ISSUE:8>> quotes the flag help and the audit, override, and warning lines in the user guide, and checks `niwa dispatch --help` and completion for the flag.
