# Round 2 Pragmatism Review — remote-control by default on dispatch

Scope: Round-1 fixes (commit a4af4a1) — shared const, tag-pin test, materialize↔readback
round-trip test, AC2 guard test. Question: did the fixes over-build?

## Findings

### 1. `TestInstanceSettings_TagMatchesKey` is redundant — NON-BLOCKING
`internal/cli/dispatch_remotecontrol_roundtrip_test.go:63` pins the `instanceSettings`
struct tag to `RemoteControlAtStartupKey`. But `TestRemoteControlKey_EndToEnd_MaterializeReadBack`
in the same file already enforces this: a drifted tag makes the materialized key read
back as nil, failing the round-trip. The tag-pin test asserts a strict subset of what
the round-trip guarantees end-to-end.
Fix: delete `TestInstanceSettings_TagMatchesKey`; the round-trip already closes the gap.
(Kept-as-is is defensible — it gives a narrower failure message — hence non-blocking.)

### 2. `remoteControlSettingsJSON` via `fmt.Sprintf` — NOT over-clever, keep
`internal/cli/dispatch_remotecontrol.go:16` derives the injected JSON from the shared
const (`fmt.Sprintf("{%q:true}", config.RemoteControlAtStartupKey)`). This is the
*simpler* choice, not the clever one: a plain string literal would need its own pin-test
to stay in sync with the const (cf. the struct-tag problem). Deriving it removes that
test. No change.

### 3. Shared const usage is legitimate — not single-caller
`config.RemoteControlAtStartupKey` backs three real production sites: the materializer
emit (`materialize.go:425-431`), the dispatch inject (`dispatch_remotecontrol.go:16`),
and (via pin) the read-back tag. A genuine single-source-of-truth, not speculative.

### 4. Test proportionality (5 files / ~110 lines) — justified
The feature spans 3 packages and 4 layers (config parse → materialize emit → dispatch
resolve/inject → read-back). The files map one-per-concern: config parse+roundtrip,
materialize emit+AC2-guard, resolve unit, wiring integration, read-back unit,
materialize↔readback. The central risk is key-spelling agreement across sites; the
test set tracks that real surface. No wiring test duplicates the unit table — each
verifies the decision is actually plumbed to argv / stderr (HostUnset asserts
byte-for-byte argv equality; APIKey asserts stderr), which the pure unit cannot.
Only finding #1 is cuttable.

### 5. `*bool` tristate on `RemoteControlOnDispatch` — idiomatic, keep
`registry.go:36`: nil and false behave identically in the only consumer
(`resolveDispatchRemoteControl`). On its own that smells speculative, but it matches
the sibling `AutoInstallPlugins *bool` convention in the same struct and the omitempty
encode behavior is tested. Consistent with established config idiom; not a violation.

### 6. Dead code — none
`apiKeyForcedWarning`, `remoteControlSettingsJSON`, `apiKeyAuthForced`,
`resolveDispatchRemoteControl` all have live callers. `_NilInstance` test covers a real
distinct branch (literal-nil pointer vs empty struct) the table can't express.

## Verdict
Nothing blocks. The Round-1 fixes are proportionate; the only trimmable item is one
redundant tag-pin test subsumed by the round-trip test.

BLOCKING COUNT: 0
