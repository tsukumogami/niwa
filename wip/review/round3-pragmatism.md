# Round 3 Pragmatism Review (final) — remote-control by default on dispatch

Scope: total change at HEAD e76800f. Specific question: did Round-2 polish (two
degrade-path tests + a second TOML example in the guide) tip the work into
over-testing / over-documentation? Anything to cut, any new dead code or scope creep?

## Round-2 additions assessed

### Degrade test 1: `TestDispatch_RemoteControl_MalformedInstanceSettings_DegradesToInject` — JUSTIFIED, no finding
`dispatch_wiring_remotecontrol_test.go:157`. Covers a branch the unit test cannot:
`_NilInstance` passes nil directly, but this proves `readInstanceSettings` *swallows* a
JSON parse error (returns nil, not err) AND dispatch does not abort on it. Distinct
production seam, realistic input (bad settings.json on disk). Keep.

### Degrade test 2: `TestDispatch_RemoteControl_InvalidHostConfig_NoInjectNoFail` — JUSTIFIED, no finding
`dispatch_wiring_remotecontrol_test.go:177`. Only test covering the dispatch.go branch
where `LoadGlobalConfig` errors on a non-bool value and dispatch degrades to no-inject
instead of failing. The config-package test only covers parse success cases; it does not
prove dispatch swallows the error. Realistic (typo'd config.toml). Keep.

Neither is impossible-case handling — both are real error branches on realistic input.
Round-2 testing closed its own F4 with two proportionate tests. Not over-testing.

### Second TOML example (guide instance-scope block) — NON-BLOCKING (advisory)
`docs/guides/remote-control-on-dispatch.md:41-45`. The instance-scope block differs from
the workspace-scope block only by the table header `[instance.claude.settings]`, which
the prose at line 31-32 already names. Mildly redundant. One 5-line block; copy-paste
value is marginal but nonzero. Trim-or-keep is a wash; doc quality otherwise defers to
maintainer-reviewer.

## Carried-over finding (unchanged from R1/R2)

### `TestInstanceSettings_TagMatchesKey` still redundant — NON-BLOCKING
`dispatch_remotecontrol_roundtrip_test.go:63`. Subsumed by
`TestRemoteControlKey_EndToEnd_MaterializeReadBack` (same file, real materializer both
directions, true+false) and by `TestReadInstanceSettings_RemoteControlAtStartup`
(dispatch_plugins_remotecontrol_test.go, hand-written read-back incl. absent case). A
drifted tag fails the round-trip deterministically. The pin survives for a narrower
failure message — defensible, hence still non-blocking. Cuttable if trimming.

## Dead code / scope creep from last two rounds — NONE
All RC symbols (`remoteControlSettingsJSON`, `apiKeyForcedWarning`, `apiKeyAuthForced`,
`resolveDispatchRemoteControl`, `RemoteControlOnDispatch`, `RemoteControlAtStartupKey`)
remain live. Round-2 added only tests + one doc block — no new production surface, no
abstraction, no params. The 6 RC test files map one-per-concern (config parse, read-back
unit, materialize emit+AC2 guard, resolver unit, e2e round-trip, dispatch wiring) with
only the one redundant tag-pin overlapping. The 5 docs are standard niwa workflow
artifacts (PRD/BRIEF/DESIGN/PLAN/SPIKE) + one user guide — not creep.

## Verdict
Proportionate. Review-driven accretion stayed inside the feature's own surface: two
degrade tests each cover a distinct real branch, one doc block is mildly redundant.
Nothing blocks. Only trimmable items are the one redundant tag-pin test and (optionally)
the second TOML block — both inert.

BLOCKING COUNT: 0
