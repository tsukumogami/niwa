# Round 3 (FINAL) Testing Review — remote-control by default on dispatched workers

Worktree HEAD: e76800f
Date: 2026-06-29

## Suite results (full module, not just the three packages)

- `go build ./...` — PASS (exit 0)
- `go vet ./...` — PASS (exit 0)
- `go test -count=1 -race ./...` — PASS (exit 0), race-clean, module-wide

```
ok  github.com/tsukumogami/niwa/internal/cli                 51.724s
ok  github.com/tsukumogami/niwa/internal/cli/sessionattach    2.420s
ok  github.com/tsukumogami/niwa/internal/config               1.104s
ok  github.com/tsukumogami/niwa/internal/envformat            1.031s
ok  github.com/tsukumogami/niwa/internal/gitexclude           1.107s
ok  github.com/tsukumogami/niwa/internal/github               1.183s
ok  github.com/tsukumogami/niwa/internal/guardrail            1.317s
ok  github.com/tsukumogami/niwa/internal/plugin               1.105s
ok  github.com/tsukumogami/niwa/internal/pluginrecord         1.097s
ok  github.com/tsukumogami/niwa/internal/secret               1.037s
ok  github.com/tsukumogami/niwa/internal/secret/reveal        1.030s
ok  github.com/tsukumogami/niwa/internal/source               1.039s
ok  github.com/tsukumogami/niwa/internal/testfault            1.031s
ok  github.com/tsukumogami/niwa/internal/tui                  1.035s
ok  github.com/tsukumogami/niwa/internal/vault                1.038s
ok  github.com/tsukumogami/niwa/internal/vault/fake           1.029s
ok  github.com/tsukumogami/niwa/internal/vault/infisical      1.332s
ok  github.com/tsukumogami/niwa/internal/vault/resolve        1.034s
ok  github.com/tsukumogami/niwa/internal/workspace           13.721s
ok  github.com/tsukumogami/niwa/internal/worktree             1.285s
ok  github.com/tsukumogami/niwa/test/functional              1.079s
```

Nothing else in the module broke.

## Review of the two new degrade tests

Both live in `internal/cli/dispatch_wiring_remotecontrol_test.go` and drive the real
call site (`dispatch.go` step 9a -> `resolveDispatchRemoteControl`), not just the resolver.

### TestDispatch_RemoteControl_MalformedInstanceSettings_DegradesToInject (test:157)
- Host config valid + on; instance settings.json = `{ this is not json`.
- Traced against production: `readInstanceSettings` (dispatch_plugins.go:167) returns
  `nil, err` on a JSON parse failure; the call site `inst, _ := readInstanceSettings(...)`
  (dispatch.go:225) discards the error, so `inst == nil`. The resolver
  (dispatch_remotecontrol.go:37) documents `inst == nil` as "downstream unset", so with
  host-on + no API key it returns `inject=true`.
- Assertions (no dispatch error; `--settings` present) match that behavior exactly. CORRECT.

### TestDispatch_RemoteControl_InvalidHostConfig_NoInjectNoFail (test:177)
- Host config `remote_control_on_dispatch = "notabool"` (string into `*bool`).
- Verified the premise is real: `LoadGlobalConfig` -> `ParseGlobalConfig` ->
  `toml.Unmarshal` (registry.go:189) errors on the type mismatch, so `gcErr != nil` and
  the entire step-9a block (dispatch.go:224-237) is skipped — no inject, no warning, no
  failure. The test genuinely exercises the invalid-host-config degrade path it claims.
- Assertions (no dispatch error; no `--settings` in argv) match. CORRECT.

Both tests assert the right behavior.

## PRD AC coverage (AC1-AC6) — all have real coverage

- **AC1** (host on injects; unset does not): `TestDispatch_RemoteControl_HostOn_Injects` +
  `TestDispatch_RemoteControl_HostUnset_NoChange`.
- **AC2** (interactive / ephemeral / `niwa apply` not enabled): guarded by
  `TestInstallWorkspaceRootSettings_NoRemoteControlByDefault`
  (internal/workspace/materialize_remotecontrol_test.go) — the shared materializer those
  three paths use never emits `remoteControlAtStartup`; the host preference is not an
  input to it. The dispatch-exclusive seam lives only at dispatch.go step 9a.
- **AC3** (downstream false wins): `TestDispatch_RemoteControl_DownstreamOff_Wins` +
  resolver subtests `host-true/downstream-false`.
- **AC4** (byte-for-byte unchanged when unset): `TestDispatch_RemoteControl_HostUnset_NoChange`
  asserts `slices.Equal(pass, buildDispatchPassthrough(""))` — byte-exact against the
  baseline, not merely "--settings absent".
- **AC5** (one-line reason + still launches): `TestDispatch_RemoteControl_APIKey_WarnsAndSkips`
  asserts the exact `apiKeyForcedWarning` on stderr, no injection, and a successful launch.
- **AC6** (automated dispatch-layer tests, `go test ./...`): satisfied by the full set above
  plus `TestResolveDispatchRemoteControl` table and `TestParseGlobalConfig_RemoteControlOnDispatch`.

## Findings

### NON-BLOCKING — dispatch.go:221-223 (AC1/AC3 degrade path) — stale/misleading comment
The step-9a comment states: "A missing/unreadable global config or instance settings file
degrades to 'no injection'." That is correct for the **global config** (LoadGlobalConfig
error -> block skipped), but **wrong for the instance settings file**: a missing or
unreadable instance settings file yields `inst == nil`, which the resolver treats as
"downstream unset", so with the host preference on it INJECTS. The Round-2 test
`...MalformedInstanceSettings_DegradesToInject` and its name assert exactly this opposite
outcome. The authoritative resolver doc (dispatch_remotecontrol.go:36) is already correct
("inst may be nil ... treated as 'downstream unset'"); only the call-site comment is loose.
Behavior and tests are correct — this is a comment-accuracy nit, not a functional defect.
Worth tightening so the comment doesn't contradict the test two files over.

### NON-BLOCKING — observation, not a defect
`...MalformedInstanceSettings_DegradesToInject` codifies that a corrupted downstream
settings.json loses its "off" intent and gets the host default-fill (RC on). This is
defensible under the PRD's default-fill model (unreadable == unset) and is documented in
the resolver, so it is acceptable as intended behavior — flagged only so a future reader
knows it is by-design, not an oversight.

## Verdict

All tests pass module-wide and race-clean. The two new degrade tests assert correct,
production-traced behavior. Every PRD AC (AC1-AC6) has real, executable coverage. No
blocker to marking the PR ready.

BLOCKING COUNT: 0
