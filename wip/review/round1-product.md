# Round 1 Product Review: remote-control by default on dispatched workers

Scope: verify the implementation satisfies PRD R1-R7 / AC1-AC6 and that its
assumptions about niwa's config layering and dispatch flow are TRUE against the
actual source. Every judgement below is grounded in the files cited.

## Verdict

No blocking findings. The four design touch points are implemented as specified,
the dispatch-only scoping holds, the downstream override is correctly
double-anchored, and the eligibility signal matches the env the worker actually
inherits. Two NON-BLOCKING notes (a doc-level overstatement about repo scope, and
the intentionally narrow eligibility signal) are recorded for honesty.

---

## R2 / AC2 / AC4 -- Is the effect REALLY dispatch-only? -- VERIFIED (no finding)

The injection is the single append at `internal/cli/dispatch.go:230-232`:

```
if inject {
    passthrough = append(passthrough, "--settings", remoteControlSettingsJSON)
}
```

inside the block at `dispatch.go:224-233`, which lives in `runDispatch` and
nowhere else. Tracing the consumers:

- `buildDispatchPassthrough` is defined at `dispatch.go:407` and called only at
  `dispatch.go:213` (runDispatch). No other caller (grep).
- `dispatchLaunch` is referenced only at `dispatch.go:235`, its definition in
  `dispatch_launcher.go:14`, and tests. `buildClaudeBgArgs`
  (`dispatch_launcher.go:56`) is reached only by `realDispatchLaunch` and tests.
- The interactive / apply launch paths do not go through this seam; they launch
  claude via the sessionattach supervisor, not `claude --bg`.

Critically, the shared provisioner is NOT the injection point. `provisionInstanceFunc`
(`instance_from_hook.go:103`, → `realProvisionInstance` :344 → `applier.Create`)
is shared with the ephemeral SessionStart-hook path (`instance_from_hook.go:165`
`runFromHookCreate`) and `niwa apply`. The remote-control append happens AFTER
provisioning, in `runDispatch` only, so ephemeral/apply instances never receive
it. R2/AC2 hold.

AC4 (byte-identical argv + env when the toggle is unset): the entire block is
gated on `config.LoadGlobalConfig()` succeeding, and `resolveDispatchRemoteControl`
(`dispatch_remotecontrol.go:34-47`) returns `(false, "")` whenever
`RemoteControlOnDispatch` is nil or false. So with the preference unset: no
append, and no warning is printed (warning is "" → the `if warning != ""` at
`dispatch.go:227` is skipped). Env is unconditionally `os.Environ()`
(`dispatch_launcher.go:40`), unchanged. `TestDispatch_RemoteControl_HostUnset_NoChange`
(`dispatch_wiring_remotecontrol_test.go:112`) pins this. VERIFIED.

## R4 / AC3 -- Does a downstream `remoteControlAtStartup="false"` materialize AND get read back? -- VERIFIED (no finding)

Both halves trace cleanly through real code.

Materialize side: the dispatched instance's root `.claude/settings.json` is
written by `InstallWorkspaceRootSettings` (`workspace_context.go:242`), which
calls `buildSettingsDoc` at `workspace_context.go:334` with
`Settings: effective.Claude.Settings` where `effective = MergeInstanceOverrides(cfg)`
(:243). `buildSettingsDoc` now recognizes the key at `materialize.go:425-432`:

```
if rc, ok := cfg.Settings["remoteControlAtStartup"]; ok {
    raw := maybeSecretString(rc)
    b, err := strconv.ParseBool(strings.TrimSpace(raw))
    ...
    doc["remoteControlAtStartup"] = b
}
```

This path is on the dispatch provision pipeline: `runDispatch` →
`provisionInstanceFunc` → `realProvisionInstance` → `applier.Create`
(`apply.go:280`) → `runPipeline` → `InstallWorkspaceRootSettings`
(`apply.go:1306`). So the effective `[claude.settings]` reaches `buildSettingsDoc`
for dispatch-provisioned instances. CONFIRMED.

Read-back side: `instanceSettings` gained
`RemoteControlAtStartup *bool` (`dispatch_plugins.go:143`), and
`readInstanceSettings` (`dispatch_plugins.go:167`) reads
`<instancePath>/.claude/settings.json` -- exactly the file
`InstallWorkspaceRootSettings` writes. The resolver
(`dispatch_remotecontrol.go:39`) returns `inject=false` when
`inst.RemoteControlAtStartup != nil`, so a downstream `false` (or `true`) makes
niwa inject nothing and the worker honors its own settings.json. Double-anchored
exactly as the design claims. `TestDispatch_RemoteControl_DownstreamOff_Wins`
(`dispatch_wiring_remotecontrol_test.go:94`) exercises the full path. VERIFIED for
workspace + instance scope.

NON-BLOCKING note (repo scope wording): `MergeInstanceOverrides`
(`override.go:166-203`) merges workspace-level `[claude.settings]` plus
instance-level `[instance.claude.settings]` only. Per-repo
`[repo.<n>.claude.settings]` is materialized into each repo's
`settings.local.json` (the `SettingsMaterializer`, `materialize.go:666`), NOT the
instance-root `settings.json`. So the DESIGN's data-flow sentence -- "the instance
settings.json already reflects the full workspace/instance/repo + overlay merge"
-- overstates for the repo rung. This is not a behavioral bug: a dispatched worker
runs `claude --bg` with `cmd.Dir = instanceDir` (the instance ROOT,
`dispatch_launcher.go:37`), so it loads the instance-root settings.json and never
adopts a repo subdir's settings.local.json. A repo-scoped override is therefore
neither read by the resolver nor effective at the worker -- consistent, just not
what the doc sentence implies. PRD R4/AC3 are satisfiable at the scopes that
actually reach a root-rooted worker (workspace, instance, overlay). Severity:
NON-BLOCKING (doc accuracy only).

## Does `[claude.settings]` accept the key / is quoted-string-bool real / any rejecting validation? -- VERIFIED (no finding)

- `SettingsConfig` is `map[string]MaybeSecret` (`config.go:320`). TOML decodes
  arbitrary keys into it; there is no key whitelist at parse time.
- No validation pass rejects an unknown settings key before `buildSettingsDoc`.
  The only settings iterators are: the vault-ref check
  (`validate_vault_refs.go:332`) which only inspects values for `vault://`, the
  fingerprint-source loop (`materialize.go:727`) which only records provenance,
  and the override-merge loops. None reject unknown keys. `buildSettingsDoc`
  itself handles known keys (`permissions`, now `remoteControlAtStartup`) and
  silently ignores the rest. So the key reaches `buildSettingsDoc` unobstructed.
- The quoted-string-bool requirement is REAL. `MaybeSecret` carries its value via
  `UnmarshalText` (`maybesecret.go:61`+), which BurntSushi/toml dispatches only
  for TOML *string* primitives. A bare TOML bool (`remoteControlAtStartup = true`)
  would not decode into a `MaybeSecret` slot and would error at parse. Hence the
  value must be quoted (`"true"`/`"false"`), and `buildSettingsDoc` parses it with
  `strconv.ParseBool` (`materialize.go:427`), erroring on an unparseable string
  (`materialize.go:429`) -- consistent with how `permissions` rejects unknown
  values (:395). The guide documents the quoting requirement
  (`docs/guides/remote-control-on-dispatch.md:35-36`). VERIFIED.

## R6 / AC5 -- Is ANTHROPIC_API_KEY in the env niwa passes, and is it the right signal? -- VERIFIED (NON-BLOCKING note)

The resolver is fed `os.Environ()` at `dispatch.go:226`, and the worker is
launched with `cmd.Env = os.Environ()` at `dispatch_launcher.go:40` -- the SAME
environment. So `apiKeyAuthForced` (`dispatch_remotecontrol.go:53`) inspects
exactly what the worker will inherit; there is no divergence. ANTHROPIC_API_KEY is
the standard, most-common local signal that Claude Code is in API-key auth (which
precludes the claude.ai-login-backed bridge). The warning is printed to
`cmd.ErrOrStderr()` (`dispatch.go:228`) and the dispatch still launches.
`TestDispatch_RemoteControl_APIKey_WarnsAndSkips`
(`dispatch_wiring_remotecontrol_test.go:130`) pins warn-and-still-launch. R6/AC5
hold.

NON-BLOCKING note: the check is intentionally narrow -- only ANTHROPIC_API_KEY.
Other local API-key/non-claude.ai auth modes (AWS Bedrock `CLAUDE_CODE_USE_BEDROCK`,
Vertex, `ANTHROPIC_AUTH_TOKEN`) are not detected, so a worker on those would start
with the setting on and fail to connect the bridge, with the reason surfaced by
claude at bridge-connect time rather than by niwa. The DESIGN explicitly scopes
this ("covers only the most common local cause", Consequences). Acknowledged, not
a false assumption. Severity: NON-BLOCKING.

## R1 / R3 / R5 / R7 / AC1 / AC6 -- spot checks (no findings)

- R1: `GlobalSettings.RemoteControlOnDispatch *bool`
  (`registry.go:36`, TOML `remote_control_on_dispatch`), decoded by the existing
  `LoadGlobalConfig`. Round-trip test `registry_remotecontrol_test.go`. PASS.
- R3/AC1: host-on + downstream-unset + no API key → inject
  (`dispatch_remotecontrol.go:46`, `TestDispatch_RemoteControl_HostOn_Injects`).
  PASS.
- R5: preference unset → no injection, no env change (covered under AC4 above).
  PASS.
- R7: `docs/guides/remote-control-on-dispatch.md` documents host-enable,
  downstream override (with the quoted-string caveat), and eligibility. PASS.
- AC6: tests at the dispatch layer cover host on/off/unset, downstream override,
  API-key skip, plus unit coverage of the resolver truth table
  (`dispatch_remotecontrol_test.go`), TOML round-trip, `buildSettingsDoc`
  emit/invalid (`materialize_remotecontrol_test.go`), and `readInstanceSettings`
  (`dispatch_plugins_remotecontrol_test.go`). PASS.

## Summary of findings

1. NON-BLOCKING -- DESIGN data-flow sentence overstates that the instance
   settings.json reflects the "workspace/instance/repo" merge; repo-scope is not
   in `MergeInstanceOverrides` and is not effective for a root-rooted dispatch
   worker anyway. Doc-accuracy only; behavior is correct.
2. NON-BLOCKING -- eligibility detection covers only ANTHROPIC_API_KEY; other
   local non-claude.ai auth modes are not detected. Explicitly scoped by DESIGN.

BLOCKING COUNT: 0
