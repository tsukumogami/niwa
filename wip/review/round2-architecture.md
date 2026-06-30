# Round 2 Review — remote-control by default on dispatched workers

Focus: architecture + security/edge-cases. Verifying Round-1 fixes (commit a4af4a1) and hunting for residual blockers.

## Verdict

Nothing blocks. The Round-1 fixes are sound. The single-sourced key closes the
three-site consistency gap as well as Go allows, the Sprintf-built injection JSON
is valid, no new coupling is introduced, and the dispatch argv carries a fixed
literal with no injection surface. One pre-existing, pattern-consistent
error-message reveal is noted as NON-BLOCKING.

---

## (1) Single-sourced key closes the three-site gap — CONFIRMED

`config.RemoteControlAtStartupKey = "remoteControlAtStartup"` (config/config.go:329)
is now the source for the three sites that must agree:

- **Emit** (materialize.go:425,431): `cfg.Settings[config.RemoteControlAtStartupKey]`
  and `doc[config.RemoteControlAtStartupKey] = b` — compile-time bound to the const.
- **Inject** (dispatch_remotecontrol.go:16): `fmt.Sprintf("{%q:true}", config.RemoteControlAtStartupKey)`
  — compile-time bound to the const.
- **Read-back** (dispatch_plugins.go:143): struct tag `json:"remoteControlAtStartup"`
  — a Go struct tag is a string literal and CANNOT reference a const. This is the
  one site the const cannot reach.

The read-back gap is closed at test time by `TestInstanceSettings_TagMatchesKey`
(roundtrip_test.go:63), which marshals `instanceSettings` and asserts the produced
JSON carries `config.RemoteControlAtStartupKey`. A tag rename fails that test.
`TestRemoteControlKey_EndToEnd_MaterializeReadBack` (roundtrip_test.go:20) additionally
drives the real emit→on-disk→readback path for both true and false. This is the
best achievable in Go: two sites const-bound at compile time, the third pinned by
test. **No finding.**

## (2) Sprintf-built JSON validity, var-not-const — CONFIRMED

`fmt.Sprintf("{%q:true}", "remoteControlAtStartup")` yields
`{"remoteControlAtStartup":true}`. Go's `%q` on a string emits a double-quoted,
escaped literal; for the pure-ASCII const key this is byte-identical to a JSON
string. Valid JSON. The wiring test (dispatch_wiring_remotecontrol_test.go:69)
asserts the exact passthrough element, so a malformed render would fail CI.

`var` (not `const`) is mandatory and correct — `fmt.Sprintf` is a runtime call and
cannot initialize a const. The package-level var is only ever read (grep-confirmed:
materialize/inject/test references, no reassignment), so there's no mutable-global
hazard. **No finding.**

NON-BLOCKING note: `%q` would diverge from JSON only if the key contained runes Go
escapes as `\xNN`/`\u00NN`. The key is a fixed lowercase-ASCII const, so this is
purely theoretical today; worth a one-line comment only if the key ever becomes
dynamic. Not actionable now.

## (3) Coupling / layering — CLEAN

- `config` is the lowest layer; both `internal/cli` and `internal/workspace` import
  it (downward). No inversion.
- `readInstanceSettings` reads `<instance>/.claude/settings.json` with its own narrow
  `instanceSettings` struct (dispatch_plugins.go:137) — a filesystem contract, NOT a
  Go import of `internal/workspace` internals. The materializer/reader stay decoupled
  except through the shared on-disk key, which is exactly what the const + roundtrip
  test pin.
- The new roundtrip test (package `cli`) imports `internal/workspace`. `cli` already
  depends on `workspace` in production (dispatch.go, apply.go, +others) — this is the
  established downward direction, so the test introduces no new edge.
- No parallel pattern: the change reuses the existing `buildSettingsDoc` emit path,
  the existing `readInstanceSettings`/`instanceSettings` reader, the existing
  passthrough-whitelist append, and `config.LoadGlobalConfig`. The
  `resolveDispatchRemoteControl` helper is the single dispatch-exclusive decision
  seam — no second config parser, no duplicate error type. **No finding.**

## (4) Security — CLEAN

- **Argv injection**: the injected value is `remoteControlSettingsJSON`, a fixed
  literal built from a const with zero user input, appended as two discrete slice
  elements `"--settings", remoteControlSettingsJSON` (dispatch.go:235). The launcher
  passes the slice to `exec.CommandContext(bin, args...)` (dispatch_launcher.go:36) —
  no shell, no interpolation. Nothing can inject a flag. CONFIRMED.
- **Degradation**: `LoadGlobalConfig` error → the whole block is gated behind
  `gcErr == nil` (dispatch.go:224), so a missing/corrupt global config skips injection
  and dispatch proceeds with today's behavior. `readInstanceSettings` error →
  `inst` is nil (dispatch.go:225 discards the error), and `resolveDispatchRemoteControl`
  treats nil as "downstream unset" (documented at dispatch_remotecontrol.go:36) — the
  intended default-fill, never a dispatch failure. Both degrade safely. CONFIRMED.
- **Credentials**: `apiKeyAuthForced` (dispatch_remotecontrol.go:56) inspects only the
  presence and non-emptiness of `ANTHROPIC_API_KEY` via `strings.CutPrefix`; it never
  captures or logs the value. The warning is a `const` string (dispatch_remotecontrol.go:21)
  with no interpolation, so it cannot carry untrusted input. CONFIRMED.

## (5) Vault-backed remoteControlAtStartup edge case — NON-BLOCKING

If a user sets `[claude.settings].remoteControlAtStartup = "vault://..."`, the
materializer reveals the plaintext via `maybeSecretString` (materialize.go:426 →
reveal.UnsafeReveal) and feeds it to `strconv.ParseBool`:

- If the secret resolves to `"true"`/`"false"`, it parses to a bool and only the
  bool `b` is written to settings.json — the secret string never lands on disk. Sane.
- If it resolves to anything else, `ParseBool` errors and `buildSettingsDoc` returns,
  failing the dispatch cleanly before anything is written. Sane rejection — exactly
  what the question anticipated.

The one wrinkle: the error at materialize.go:429 echoes the revealed value via `%q`:
`fmt.Errorf("invalid [claude.settings] %s value %q: ...", key, raw)` where `raw` is
the revealed plaintext. So a vault secret that fails the bool parse would surface in
the error string (stderr/logs).

Why NON-BLOCKING:
- This is byte-for-byte the SAME pattern the pre-existing `permissions` handling uses
  (materialize.go:392-395: `maybeSecretString(perm)` then `%q` in the error). The new
  code copies the established convention rather than diverging from it — there is no
  parallel pattern and no NEW security regression introduced by this change.
- Exposure is the user's own secret, on the user's own terminal, only when they
  nonsensically back a boolean toggle with a vault reference and it fails to parse.
- The on-disk worker settings.json never receives the secret (parse failure aborts
  the write; parse success writes only the bool).

If the team wants to harden this, the fix belongs at BOTH sites uniformly (redact the
revealed value in the parse/lookup error for `remoteControlAtStartup` AND `permissions`)
so the convention stays consistent — that's a separate, pre-existing hardening task,
out of scope for this change. Fixing only the new site would itself create the
inconsistency this review otherwise praises the change for avoiding.

---

## Findings summary

- (1) three-site consistency — CONFIRMED, no finding
- (2) Sprintf JSON valid + var correct — CONFIRMED; theoretical `%q` note NON-BLOCKING
- (3) coupling/layering — CLEAN, no finding
- (4) argv injection / degradation / credentials — CLEAN, no finding
- (5) vault-backed value error-echo — NON-BLOCKING (pre-existing, pattern-consistent)

BLOCKING COUNT: 0
