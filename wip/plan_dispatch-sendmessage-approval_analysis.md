# Plan Analysis: DESIGN-dispatch-sendmessage-approval

## Source Document
Path: docs/designs/DESIGN-dispatch-sendmessage-approval.md
Status: Planned (auto-transitioned from Accepted under the /scope parent sentinel)
Input Type: design
review_rounds: 1

## Scope Summary
Let `niwa dispatch` launch Claude workers that accept messages from other Claude
Code sessions without an approval prompt, opted into by a `[global]` machine key
and overridable per dispatch by a tri-state flag, with an audit line, a
`niwa list` record, and a one-time explanation. Alongside it, `niwa watch` review
sessions deny the tools that reach other sessions, and Claude launches put `--`
before the prompt so niwa alone writes the launch settings.

## Components Identified
- Machine config key `accept_session_messages_on_dispatch` and `CrossSessionInboundKey` constant (`internal/config`)
- Launch settings rendering (`renderLaunchSettings`, one map for remote control and inbound acceptance) and the Claude `PromptSeparator` (`internal/cli`, `internal/agentplan/dispatch.go`)
- `DispatchInboundAcceptance` capability row 25 with count, gap-list, and capability-contract updates (`internal/agentplan`, `docs/guides/codex-agent.md`, `docs/prds/PRD-agent-capability-contract.md`)
- Flag, resolver, `inboundApplied`, audit/override/warning lines, `hostGlobal` hoist (`internal/cli/dispatch_inbound.go`, `dispatch.go`)
- Durable record: `SessionMapping.AcceptsSessionMessages`, `InstanceRecord.AcceptsSessionMessages`, `niwa list` marker and JSON field
- One-time explanation and marker beside `config.toml`, attach-aware placement
- Review-session session-reach deny hook (`internal/watch/containment.go`)
- Guide, contributor-guide index, `@critical` functional scenarios, watch-site unit test, manual delivery check

## Implementation Phases (from design)
Phase 7 (review deny, independent) -> Phase 1 (config key) and Phase 2 (settings
rendering + prompt separator) -> Phase 3 (capability row, flag, resolver,
delivery, stderr lines) -> Phase 4 (record + list) and Phase 5 (explanation +
marker) -> Phase 6 (guide, functional scenarios, manual check). All in one PR.

## Success Metrics
- A worker dispatched with the behavior on accepts messages from a session in a
  different permission-mode class, and keeps accepting after `claude respawn`
  and `claude attach` (manual delivery check).
- Remote control's launch document stays byte-identical when it's the only
  contributor; keep-alive arming is unchanged.
- Every PRD acceptance criterion passes; `shirabe validate` and `go test ./...`
  are clean.

## External Dependencies
- Claude Code 2.1.267 semantics: `crossSessionInbound`, deep-merged `--settings`,
  saved `respawnFlags`, the `--` prompt separator, the session-reaching tool
  list, and hook matcher semantics.
- Existing seams: `triBoolValue`, `resolveDispatchRemoteControl`,
  `agentplan.Lookup`, `IsStderrTTY`, `annotateFromSessionMappings`,
  `ApplyReviewSettings`/`VerifyReviewSettings`, the functional `script` pty helper
  and fake `claude`.
