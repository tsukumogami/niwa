# Round 1 Testing Review: remote-control by default on dispatched workers

Scope: test coverage + correctness of the RC-by-default change in `niwa`.
Spec: `docs/prds/PRD-remote-control-by-default.md` (AC1-AC6).

## Commands run (actual results)

- `go build ./...` -> exit 0
- `go vet ./...` -> exit 0
- `go test ./internal/config/... ./internal/workspace/... ./internal/cli/...` -> PASS (all 4 packages ok)
- `go test -count=1 -race ./internal/config/... ./internal/workspace/... ./internal/cli/...` -> PASS
  - config 1.1s, workspace 14.0s, cli 54.0s, cli/sessionattach 2.4s; exit 0

All targeted RC tests pass individually (verbose, `-count=1`):
TestParseGlobalConfig_RemoteControlOnDispatch, TestGlobalSettings_RemoteControlOnDispatch_RoundTrip,
TestBuildSettingsDoc_RemoteControlAtStartup(+_Invalid), TestReadInstanceSettings_RemoteControlAtStartup,
TestResolveDispatchRemoteControl(+_NilInstance), TestApiKeyAuthForced,
TestDispatch_RemoteControl_{HostOn_Injects,DownstreamOff_Wins,HostUnset_NoChange,APIKey_WarnsAndSkips}.

## Implementation map (verified against tests)

- Host field: `config.GlobalSettings.RemoteControlOnDispatch *bool` (`internal/config/registry.go:36`), `toml:"remote_control_on_dispatch,omitempty"`.
- Resolve logic: `resolveDispatchRemoteControl` (`internal/cli/dispatch_remotecontrol.go:34`):
  host nil/false -> (false,""); host true & downstream decided -> (false,""); host true & downstream unset & API key -> (false,warning); else (true,"").
- Injection seam: `internal/cli/dispatch.go:224-233` -- the ONLY place `RemoteControlOnDispatch` is read and the only place `--settings remoteControlSettingsJSON` is appended (grep-confirmed below).
- Materialize side: `buildSettingsDoc` (`internal/workspace/materialize.go:425-431`) emits `remoteControlAtStartup` only when the downstream `[claude.settings]` key is present, via `strconv.ParseBool` (errors on junk).

Grep of all non-test usages confirms `RemoteControlOnDispatch` / `remote_control_on_dispatch` / the inject seam appear only in `internal/config/registry.go` and `internal/cli/dispatch.go` -- nothing in the apply / ephemeral / root materializer paths.

## AC-by-AC coverage

- AC1 (default-on injects; unset does not): COVERED.
  Unit `host-true/unset/clean` -> inject (dispatch_remotecontrol_test.go:33); `host-unset` -> no (line 26).
  Integration `TestDispatch_RemoteControl_HostOn_Injects` asserts `--settings` present (dispatch_wiring_remotecontrol_test.go:76); `..._HostUnset_NoChange` asserts absent (line 112).
- AC2 (dispatch-only; interactive/ephemeral/apply untouched): NOT DIRECTLY TESTED. See finding F1.
- AC3 (downstream off wins): COVERED.
  Unit `host-true/downstream-false` (line 35) and `.../api-key` (line 40) -> no inject.
  Integration `TestDispatch_RemoteControl_DownstreamOff_Wins` (line 94) asserts `--settings` absent.
- AC4 (byte-identical argv + env when unset): PARTIALLY tested. See finding F2.
- AC5 (ineligible warns + still launches): COVERED.
  Unit `host-true/unset/api-key` -> warn,no-inject (line 38).
  Integration `..._APIKey_WarnsAndSkips` (line 130): asserts stderr mentions ANTHROPIC_API_KEY, `--settings` absent, and `err==nil` (dispatch completed -> worker launched).
- AC6 (automated tests at dispatch layer: on/off/unset, override, dispatch-only scoping): COVERED for on/off/unset + override; dispatch-only scoping is structural-only (F1).

## Findings

### F1 -- BLOCKING -- AC2 has no test that a non-dispatch session is unaffected
The suite proves dispatch DOES inject, but nothing proves interactive root/instance, ephemeral
SessionStart-hook, or `niwa apply` sessions DON'T. AC2 (and AC6's "dispatch-only scoping") is a
stated acceptance criterion with no direct negative test.
- Mitigation (why risk is low, not zero): `RemoteControlOnDispatch` is read only at
  `internal/cli/dispatch.go:226`, and the `--settings` append exists only at `dispatch.go:231`
  (grep-confirmed). `buildSettingsDoc` -- the shared materializer for apply/ephemeral/interactive --
  never consults the host preference; its `remoteControlAtStartup` test (materialize_remotecontrol_test.go:9)
  shows the key is emitted only from downstream `[claude.settings]`.
- Why still BLOCKING: a future regression that wires the preference into the apply/ephemeral
  materializer would pass every current test. AC2 exists precisely to catch that. A small additive
  test (e.g. assert the root/instance materialized settings.json carries no `remoteControlAtStartup`
  when only the host preference is on, or that the apply/ephemeral launch path appends no `--settings`)
  closes it. Maps to: AC2, AC6 scoping clause.

### F2 -- NON-BLOCKING -- AC4 assertion is weaker than "byte-for-byte"
`TestDispatch_RemoteControl_HostUnset_NoChange` (dispatch_wiring_remotecontrol_test.go:125) only asserts
`!slices.Contains(pass, "--settings")`; the comment claims "argv unchanged from the no-flags baseline."
It does not compare argv against a captured baseline, and asserts nothing about the worker environment
(AC4 says argv AND environment). The feature only ever appends a `--settings` pair and never mutates env,
so the weaker check is sound in practice, but it doesn't actually verify the byte-for-byte claim.
Nice-to-have: capture baseline passthrough and assert slice-equality; assert env not mutated. Maps to: AC4.

### F3 -- NON-BLOCKING -- truth table is equivalence-complete but not the full 18-cell grid it claims
`dispatch_remotecontrol_test.go:16` comments "host {unset,false,true} × downstream {nil,false,true} ×
API key {absent,present}" (18 cells) but enumerates 10. All equivalence classes of the branch logic are
hit (host short-circuit, downstream-decided short-circuit, api-key gate, clean-inject), so coverage is
logically complete; the comment over-promises exhaustiveness. Missing literal cells include
host-true/downstream-true/api-key, host-false/api-key, host-unset/downstream-false. Maps to: AC6.

### F4 -- NON-BLOCKING -- invalid host-config bool and malformed instance settings.json have no test
- Invalid `remote_control_on_dispatch` value (non-bool) in config.toml: `ParseGlobalConfig` test only
  covers unset/true/false (registry_remotecontrol_test.go:16-18). A junk value makes `LoadGlobalConfig`
  error, so `dispatch.go:224` skips the block (degrade to no-inject, no dispatch failure) -- desirable,
  but untested.
- Malformed instance `settings.json`: `dispatch.go:225` does `inst, _ := readInstanceSettings(...)`
  (error ignored). resolve treats nil as "downstream unset" -> would inject when host is on. The
  nil-instance path is unit-tested (`TestResolveDispatchRemoteControl_NilInstance`) but there is no
  integration test that a corrupt downstream settings.json degrades to unset rather than honoring a
  value the user thought they set. Edge case; PRD R4/"unreadable => unset" is satisfied by code.
  Maps to: AC3 edge, R6 degrade behavior.

The materialize-side invalid-bool path IS tested:
`TestBuildSettingsDoc_RemoteControlAtStartup_Invalid` (materialize_remotecontrol_test.go:41) asserts
`"maybe"` returns an error. Good.

## Hermeticity / flakiness

No non-hermetic tests found.
- Unit tests (resolve, apiKeyAuthForced) pass env as explicit `[]string` -- no process-env dependence.
- Wiring tests set `XDG_CONFIG_HOME` and `ANTHROPIC_API_KEY` via `t.Setenv` (auto-restored) and `chdir`
  via `t.Cleanup(os.Chdir(prev))` (dispatch_test.go:56-65). Global seams `provisionInstanceFunc` /
  `dispatchLaunch` are saved and restored by `installDispatchFakes`'s cleanup (dispatch_test.go:144-147);
  `provisionWithInstanceSettings` overwrites after install and relies on that same cleanup -- correct,
  the comment at its definition documents the ordering requirement.
- No `t.Parallel()` in these tests, so the os.Chdir / global-seam mutation is serial-safe.
- `-race` run is clean.

## No wrong assertions found
`hasRemoteControlSettings` matches an adjacent `--settings` + exact literal pair; the API-key warning
constant contains the asserted "ANTHROPIC_API_KEY" substring; `apiKeyAuthForced` correctly handles
empty value and prefix collision (`ANTHROPIC_API_KEY_EXTRA`).

## Verdict
Build/vet/test all pass (incl. -race). One BLOCKING coverage gap (AC2 has no negative/scoping test),
strongly mitigated by structure but capable of hiding a future regression. Three non-blocking gaps.

BLOCKING COUNT: 1
