---
complexity: testable
complexity_rationale: A new pointer field and a string constant with no runtime reader yet, but the decode, error, and round-trip behavior that later dispatch logic relies on needs unit tests to pin it.
---

## Goal

Add the `[global] accept_session_messages_on_dispatch` machine setting to niwa's configuration struct, and add the `crossSessionInbound` settings-key constant. Both decode and round-trip, and nothing reads them yet.

## Context

A later change lets `niwa dispatch` launch Claude workers that accept messages from other Claude Code sessions without an approval prompt. The behavior resolves from a per-dispatch flag first, then a machine-level setting in `config.toml`, then off. This issue adds only the configuration surface that resolution reads.

- `config.GlobalSettings` in `internal/config/registry.go` gains `AcceptSessionMessagesOnDispatch *bool` with the tag `toml:"accept_session_messages_on_dispatch,omitempty"`, next to its siblings `RemoteControlOnDispatch` and `KeepAliveOnDispatch`. It is a `*bool` so that "absent" (nil) stays distinct from an explicit `false`. Like those siblings, it has no `niwa config set` setter: developers hand-edit `config.toml` to use it.
- `internal/config/config.go` gains `CrossSessionInboundKey = "crossSessionInbound"`, placed beside `RemoteControlAtStartupKey`, so the Claude Code settings key is spelled in one place. The launch-settings code in a later change uses it.
- The PRD requires that when `config.toml` can't be read, the machine setting counts as absent and the flag still applies. A value that isn't a boolean, such as `accept_session_messages_on_dispatch = "yes"`, must make the whole file unreadable, the same as any other type error in it. `ParseGlobalConfig` already fails on type mismatches. The tests in this issue pin that for the new key, so the dispatch resolver can treat a parse error as "setting absent".
- `SaveGlobalConfigTo` re-encodes the whole struct. A key that isn't declared on the struct is dropped on the next `niwa config set`, so declaring the field is what keeps a hand-edited value from being lost.

Design: `docs/designs/DESIGN-dispatch-sendmessage-approval.md` (Decision Outcome > Summary; Solution Architecture > Components; Implementation Approach > Phase 1). Upstream requirements: `docs/prds/PRD-dispatch-sendmessage-approval.md` (R1, R15).

## Acceptance Criteria

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

## Dependencies

None

## Downstream Dependencies

<<ISSUE:4>> (feat(dispatch): add --accept-session-messages with its capability row and audit lines) builds on this. Its resolver `resolveDispatchInboundAcceptance(flag *bool, global config.GlobalSettings)` reads `GlobalSettings.AcceptSessionMessagesOnDispatch` from the `hostGlobal` value `runDispatch` builds. It relies on three behaviors pinned here: nil means the machine setting is absent, a non-nil value decides when no flag is given, and an unreadable or mistyped `config.toml` fails to load as a whole, so `hostGlobal` falls back to a zero `GlobalSettings`. At step 9c its launch-settings map uses `config.CrossSessionInboundKey` as the key it sets to `"accept"`, so the constant must exist under that exact name and value.
