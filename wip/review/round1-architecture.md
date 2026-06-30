# Architecture Review (round 1): remote-control by default on dispatched workers

Scope: structural fit, layering, interface contracts, dependency direction.
Base: `main`. Files reviewed: `internal/config/registry.go`,
`internal/workspace/materialize.go`, `internal/cli/dispatch_plugins.go`,
`internal/cli/dispatch_remotecontrol.go`, `internal/cli/dispatch.go`, plus the
DESIGN/PRD and the four `*_remotecontrol_test.go` files.

## Verdict

No blocking architectural defects. The change is a clean, minimal extension that
reuses existing seams rather than introducing a parallel path. The four findings
below are advisory.

## Evaluated questions

### 1. Does the dispatch-only scoping actually hold? YES.

The host default (`GlobalSettings.RemoteControlOnDispatch`) is consumed in exactly
one place: `runDispatch` step 9a (`dispatch.go:224-233`), where the resolved
`--settings` pair is appended to the `passthrough` slice. That slice flows only
into `dispatchLaunch` -- the `claude --bg` launch seam reached exclusively by
`niwa dispatch`.

Critically, the materialization path does NOT read the host default:
`buildSettingsDoc` (`materialize.go:425-432`) emits `remoteControlAtStartup` ONLY
when `cfg.Settings["remoteControlAtStartup"]` is present, i.e. when a downstream
`[claude.settings]` explicitly set it. There is no reference to
`RemoteControlOnDispatch` anywhere in `internal/workspace`. So `niwa apply`,
`niwa create`, interactive, and ephemeral-session paths never receive the
host-default injection. The argv seam is genuinely the only dispatch-exclusive
path, matching DESIGN Decision 1a+2a. This is the load-bearing correctness
property and it holds.

### 2. Is reading the just-provisioned settings.json the right layer? YES.

`readInstanceSettings` / `instanceSettings` already existed in the `cli` package
(for plugin pre-warming). The change extends the existing narrow projection with
one field (`RemoteControlAtStartup *bool`, `dispatch_plugins.go:143`) and reuses
the existing reader. It is a filesystem read of `<instance>/.claude/settings.json`
plus an independent narrow struct -- NOT a Go import of `internal/workspace`
internals. The `cli` package already owns "read back the materialized instance
settings.json" as an established responsibility, so this adds no new coupling and
introduces no parallel reader. Reusing the already-written instance file (over
threading effective config out of provisioning) is the deliberate, documented
tradeoff in DESIGN Decision 3a vs 3c.

### 3. Is the settings-vocabulary extension placed correctly? YES.

`buildSettingsDoc` operates a closed vocabulary (permissions, hooks, env,
enabledPlugins, extraKnownMarketplaces, includeGitInstructions). The new key is
added as one more explicit case (`materialize.go:418-432`), positioned alongside
`permissions` and given the same "unparseable value is a config error" treatment
(`strconv.ParseBool` -> typed error), consistent with how `permissions` rejects
unknown values. No generic passthrough was opened; the closed-vocabulary invariant
is preserved. Correct placement.

### 4. Dependency direction / contract violations? NONE.

`config` is the lowest layer; `resolveDispatchRemoteControl` takes
`config.GlobalSettings` by value (`dispatch_remotecontrol.go:34`). `cli` imports
`config` and `workspace`; neither lower package imports `cli`. No inversion, no
cycle. The resolver is a pure function (no I/O), table-testable, with the
default-fill rule isolated from the wiring -- a clean seam.

### 5. GlobalSettings placement consistent with the existing rung? YES.

`RemoteControlOnDispatch *bool` sits beside `AutoInstallPlugins *bool`
(`registry.go:29-36`): same tri-state `*bool` + `omitempty` convention, same
host-config layer-1 rung. Consistent with how the existing rung is modeled.

## Findings

### Finding A -- Writer/reader JSON-key contract is duplicated with no shared constant (NON-BLOCKING)

The string `"remoteControlAtStartup"` is the contract between the writer
(`materialize.go:425,431`) and the reader (`dispatch_plugins.go:143` struct tag).
The two live in different packages with no shared constant, and there is no
cross-package round-trip test that feeds `buildSettingsDoc`'s actual output into
`readInstanceSettings`. A rename on one side only would not fail to compile and
would silently break the resolver's "downstream decided" detection:
`inst.RemoteControlAtStartup` would stay `nil`, the resolver would treat a
downstream explicit `false` as "unset," and default-fill would inject `true` --
i.e. a downstream opt-out would silently stop winning (a fail-OPEN).

Why non-blocking: this mirrors the pre-existing `enabledPlugins` /
`extraKnownMarketplaces` duplication in the same `instanceSettings` struct, so it
follows the codebase's established read-back convention rather than introducing a
new divergent pattern. It is contained and won't be copied as a *new* idiom.

Suggestion (optional): the failure mode here is worse than the plugin case (a
missed pre-warm self-heals at startup; a silently-overridden downstream "off" does
not). Consider one shared exported key constant referenced by both sides, or a
single cross-package round-trip test, to pin the contract. Not required for merge.

### Finding B -- Host default read raw vs. the accessor pattern on the sibling field (NON-BLOCKING / nit)

`AutoInstallPlugins` is consulted via a nil-guarded accessor
(`GlobalConfig.SkipPluginInstall()`), whereas `RemoteControlOnDispatch` is read
inline in the resolver (`dispatch_remotecontrol.go:35`). The nil-check is correctly
handled inside `resolveDispatchRemoteControl`, which IS the dedicated decision
function, so an accessor would be redundant here. Flagging only for symmetry; no
change needed.

### Finding C -- Global config is loaded twice on the dispatch path (NON-BLOCKING)

`runDispatch` calls `config.LoadGlobalConfig()` at step 9a (`dispatch.go:224`);
the shared provision path also loads global config (for `AutoInstallPlugins`). This
is a second cheap TOML read on every dispatch. The DESIGN explicitly accepts this
("two cheap file reads ... negligible cost") as the price of reusing the existing
seam over threading effective config out of `provisionInstanceFunc` (Decision 3c,
rejected). Documented and intentional; no action.

### Finding D -- `instanceSettings` is now a dual-purpose projection (NON-BLOCKING / observation)

`instanceSettings` now serves two unrelated consumers: plugin pre-warming
(`enabledPlugins`, `extraKnownMarketplaces`) and the remote-control resolver
(`remoteControlAtStartup`). It remains a single narrow read-back of the same file,
so this is a reasonable shared projection, not a god-struct. Worth a note only so a
future third consumer doesn't grow it without re-checking cohesion. No change
needed.

## Blocking count

0
