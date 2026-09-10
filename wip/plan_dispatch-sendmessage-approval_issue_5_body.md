---
complexity: testable
complexity_rationale: Two additive struct fields, one annotation branch, and one output tweak with clear unit-test seams, but the wire shape (omitempty on one side, always present on the other) and the no-liveness rule need tests that would catch a wrong implementation.
---

## Goal

Record on each dispatched session's mapping whether peer-message acceptance took effect, and report it through `niwa list` as an always-present `accepts_session_messages` field and a human `(accepts session messages)` marker.

## Context

Design: `docs/designs/DESIGN-dispatch-sendmessage-approval.md`

When a worker is launched with Claude Code's `crossSessionInbound: "accept"`, messages from other sessions reach it without an approval prompt. The audit line `niwa dispatch` prints lands in whatever terminal or agent ran the dispatch, and often nobody keeps it. So `niwa list` is the reliable way to see which instances accept unattended messages. It's also how a developer withdraws the grant: turning the machine key off doesn't reach sessions already launched, because Claude Code reapplies their launch settings when it restarts or reopens them.

The design (Decision 4) keeps this on the session mapping, which is already the durable per-session record that keep-alive observability uses and that the reaper removes together with the instance. `SessionMapping` gets an omitempty field, so mappings written by other paths, and mappings from before this change, stay byte-identical. `InstanceRecord` gets the same field without omitempty, so every record in `niwa list --json` carries it. The value is set from `inboundApplied`, the one boolean that also gates the audit line and the explanation. It's introduced by <<ISSUE:4>>, so the three surfaces can't disagree.

Unlike `keep_alive`, which `annotateFromSessionMappings` reports only while the session's job entry exists, this field describes what niwa launched the worker with. It must be reported for as long as the instance record exists, including after the session has finished.

Code touched: `internal/workspace/session_map.go` (`SessionMapping`), `internal/workspace/state.go` (`InstanceRecord`), `internal/cli/dispatch.go` (the step 11 mapping literal), and `internal/cli/list.go` (`annotateFromSessionMappings`, the human output loop, the `--json` flag help, and the `Long` text).

## Acceptance Criteria

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

## Dependencies

Blocked by <<ISSUE:4>>

## Downstream Dependencies

<<ISSUE:7>> (functional scenarios) asserts the `(accepts session messages)` marker in `niwa list` and `"accepts_session_messages": true` or `false` in `niwa list --json`. That includes the cases after the session has finished, a Codex dispatch with the flag, and an instance with no dispatched session record. It needs the marker text, its position after `(keep-alive)`, and the field name exactly as specified above.

<<ISSUE:8>> (guide) documents the marker, the JSON field, and what the field does and doesn't mean: niwa launched the worker with the setting, not that Claude Code confirmed it. It also explains how to withdraw the grant: list the instances reporting `accepts_session_messages: true` and stop their sessions. The guide needs the field to be present on every record and not gated on liveness, as this issue delivers.
