# Round 2 Testing Review: remote-control by default on dispatched workers

Scope: re-verify Round-1 findings (F1 blocking, F2/F3/F4 non-blocking) against the
follow-up commit `a4af4a1`, run the suite, and look for any remaining blocker.
Spec: `docs/prds/PRD-remote-control-by-default.md` (AC1-AC6).

## Commands run (actual results)

- `go build ./...` -> exit 0
- `go vet ./...` -> exit 0
- `go test -count=1 -race ./internal/config/... ./internal/workspace/... ./internal/cli/...`
  - config 1.10s ok, workspace 13.81s ok, cli 51.89s ok, cli/sessionattach 2.42s ok
  - exit 0 (PASS, race clean)

All green.

## F1 (was BLOCKING, AC2 / AC6 scoping) -- CLOSED

Guard added: `TestInstallWorkspaceRootSettings_NoRemoteControlByDefault`
(`internal/workspace/materialize_remotecontrol_test.go:19`).

Genuinely closes the gap:
- It calls the REAL shared materializer `workspace.InstallWorkspaceRootSettings`
  (materialize_remotecontrol_test.go:30), reads the REAL produced
  `instance/.claude/settings.json` from disk (line 33), and asserts the file does
  not contain `config.RemoteControlAtStartupKey` (line 37).
- That function is the single shared materializer: the only non-test caller is
  `internal/workspace/apply.go:1306`, which is the path `niwa apply`, the ephemeral
  SessionStart hook, interactive root/instance, AND dispatch-provision all reach.
  So the negative assertion covers every non-dispatch path AC2 names.
- It routes through `buildSettingsDoc` (workspace_context.go:334 -> materialize.go:378),
  whose RC block (materialize.go:425-431) emits the key ONLY from an explicit
  `[claude.settings]` value. The host preference is never an input to this function.
- Would it fail on the regression AC2 exists to catch? Yes. If a future change made
  the materializer emit `remoteControlAtStartup` by default (the realistic regression
  -- wiring the dispatch default into the shared materializer), the produced
  settings.json would carry the key and this test fails deterministically.

Minor observation (NON-BLOCKING): the test does not pin `XDG_CONFIG_HOME`, so a
contrived regression that made the materializer itself read the host config.toml
could become environment-dependent. The realistic regression (unconditional emit)
is caught regardless. Not worth blocking.

## End-to-end round-trip test -- MEANINGFUL (real materializer, not hand-written JSON)

`TestRemoteControlKey_EndToEnd_MaterializeReadBack`
(`internal/cli/dispatch_remotecontrol_roundtrip_test.go:20`):
- Drives the REAL `workspace.InstallWorkspaceRootSettings` to produce settings.json
  from a `[claude.settings].remoteControlAtStartup` value (line 44), then the REAL
  `readInstanceSettings` to read it back (line 47). No hand-authored JSON on the
  emit side -- this exercises emit-key and read-back-tag agreement end to end.
- Covers both `true` and `false` (line 21), so a one-sided rename of the emit key
  or the read-back struct tag breaks it -- which is the silent-override gap it guards
  (read-back-as-absent would inject `--settings` and clobber a user's explicit false).
- Backed by `TestInstanceSettings_TagMatchesKey` (roundtrip_test.go:63), which pins
  the `instanceSettings` JSON tag to `config.RemoteControlAtStartupKey`.

## AC4 -- now BYTE-EXACT (F2 argv side resolved)

`TestDispatch_RemoteControl_HostUnset_NoChange`
(`internal/cli/dispatch_wiring_remotecontrol_test.go:112`) now asserts
`slices.Equal(pass, buildDispatchPassthrough(""))` (line 127) -- byte-for-byte argv
equality against the REAL baseline builder, not the old `!slices.Contains(...,
"--settings")`. With model/permission-mode/agent unset and slug "",
`buildDispatchPassthrough("")` returns no flags (dispatch.go:411-425), so the
assertion proves the preference-unset path leaves argv identical to the no-flag
baseline. The argv half of AC4 is genuinely byte-exact.

Residual (NON-BLOCKING, AC4 environment clause): no test asserts the worker
environment is unmutated. The dispatch RC seam (dispatch.go:215-237) only ever
appends a `--settings` pair and has no env-mutation code, so there is nothing to
regress; the env claim is sound by construction. Nice-to-have, not blocking.

## F3 (was NON-BLOCKING, AC6) -- RESOLVED

The truth-table comment (`internal/cli/dispatch_remotecontrol_test.go:16-19`) no
longer over-promises 18 cells. It now states only reachable equivalence classes are
enumerated and explains the short-circuit. The case list (10 cases) still covers
every branch of `resolveDispatchRemoteControl` (host short-circuit, downstream-decided
short-circuit, api-key gate, clean-inject). Honest and complete.

## F4 (was NON-BLOCKING, AC3 edge / R6 degrade) -- STILL NOT TESTED, stays NON-BLOCKING

No new test for an invalid (non-bool) `remote_control_on_dispatch` in config.toml
(`internal/config/registry_remotecontrol_test.go` still only covers unset/true/false),
and no integration test that a malformed instance `settings.json` degrades to
"unset". Both remain degrade-to-safe in code (`LoadGlobalConfig` error -> block
skipped at dispatch.go:224; `readInstanceSettings` error -> nil -> treated as unset,
unit-covered by `TestResolveDispatchRemoteControl_NilInstance`). Round 1 scored this
non-blocking; nothing changed to elevate it.

## AC5 exact-warning question -- functional AC covered; exact-string assertion is NON-BLOCKING

AC5 (ineligible warns + still launches) is covered:
`TestDispatch_RemoteControl_APIKey_WarnsAndSkips`
(`internal/cli/dispatch_wiring_remotecontrol_test.go:132`) asserts the worker still
launches (`err == nil`), `--settings` is absent (line 146), and stderr mentions
`ANTHROPIC_API_KEY` (line 149).

No test asserts the EXACT `apiKeyForcedWarning` constant
(`internal/cli/dispatch_remotecontrol.go:21`): the unit test checks only warning
presence/absence (`(warning != "") != tc.wantWarning`,
dispatch_remotecontrol_test.go:59), and the integration test checks a substring.
A reword/typo of the warning text would not be caught. This is gold-plating on a
UX string, not a functional gap -- the substring assertion catches gross regression.
NON-BLOCKING. If desired, swap the integration substring check to
`strings.Contains(stderr, apiKeyForcedWarning)` for a one-line tightening.

## Hermeticity / flakiness

Re-confirmed clean. New tests use `t.TempDir`, `t.Setenv` (auto-restored),
explicit `[]string` env for resolve, and the saved/restored `provisionInstanceFunc`
/ `dispatchLaunch` seams. No `t.Parallel()`, so os.Chdir / global-seam mutation is
serial-safe. `-race` run clean across all four packages.

## Verdict

Build/vet/test all pass, race clean. F1 (the Round-1 blocker) is genuinely closed
by a test that exercises the real shared materializer and would fail on the AC2
regression. The end-to-end round-trip runs the real materializer + read-back, not
hand-written JSON. AC4 argv is now byte-exact via `slices.Equal`. F3 resolved. F4
and the AC5 exact-warning assertion remain non-blocking. Nothing rises to blocking.

BLOCKING COUNT: 0
