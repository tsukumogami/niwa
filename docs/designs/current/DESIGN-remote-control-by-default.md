---
schema: design/v1
status: Current
upstream: docs/prds/PRD-remote-control-by-default.md
problem: |
  The accepted PRD requires a single host-level niwa preference that defaults
  `niwa dispatch` workers to start with Claude Code Remote on, scoped to dispatched
  workers only, overridable by downstream config, with a clear message when the host
  cannot use remote-control. The how -- where the toggle lives, how it reaches only
  the dispatch launch, and how a downstream "off" wins given that `claude --settings`
  outranks the worker's settings.json -- is undecided.
decision: |
  Add a `remote_control_on_dispatch *bool` to host config's `[global]`
  (config.GlobalSettings). In runDispatch, after the instance is provisioned, read
  the instance's effective settings.json for `remoteControlAtStartup`; if the host
  toggle is on AND the key is absent downstream, append
  `--settings {"remoteControlAtStartup":true}` to the dispatch-only argv. Teach
  niwa's settings vocabulary (buildSettingsDoc + readInstanceSettings) to carry
  `remoteControlAtStartup` so a downstream `[claude.settings]` value both reaches the
  worker and is readable by the resolver. Skip injection and print a one-line reason
  when a definitive local ineligibility signal (ANTHROPIC_API_KEY) is present.
rationale: |
  The dispatch argv (buildDispatchPassthrough / realDispatchLaunch) is the only
  launch seam reached exclusively by `niwa dispatch`; provisioning and
  settings-materialization are shared with interactive sessions, so the toggle must
  be consumed there, not materialized globally. The spike proved the per-session
  settings key alone makes a `--bg` worker steerable and that `--settings` outranks
  project settings.json, so niwa must default-fill (inject only when the key is
  unset) rather than override. The host toggle belongs on GlobalSettings (host
  layer 1), not the overlay's `[global.claude.settings]` (layer 2), because layer 2
  materializes into every instance and cannot be scoped to dispatch.
---

# DESIGN: remote-control by default on dispatched workers

## Status

Current

## Context and Problem Statement

The accepted PRD (`docs/prds/PRD-remote-control-by-default.md`) requires that
`niwa dispatch` workers default to starting with Claude Code Remote enabled,
configured by a single host-level niwa preference, applied **only** to dispatched
workers, as a default a downstream workspace / instance / repo config can turn off,
with a clear message when the host cannot use remote-control and no change to today's
behavior when the preference is unset.

Feasibility and the load-bearing mechanism facts are settled by
`docs/spikes/SPIKE-remote-control-by-default.md` (Complete). Live `claude` runs
established:

- Claude Code Remote is enabled by the per-session settings key
  **`remoteControlAtStartup: true`** ("Start Remote Control bridge automatically each
  session"). `--remote-control` is interactive-only; `--bg` alone does not start the
  bridge.
- Launching `claude --bg --settings '{"remoteControlAtStartup":true}'` -- and nothing
  else -- makes the worker live-steerable from Agent View / mobile. The daemon-level
  `autoAddRemoteControlDaemonWorker` is **not** required (Variant A).
- `claude --settings` **outranks** the worker's project `.claude/settings.json` for
  this key (Variant C). So a host default injected via `--settings` would override a
  downstream "off" unless niwa resolves the override itself.
- Remote-control additionally requires a first-party claude.ai login with scopes +
  subscription and an account/org where the bridge rollout is enabled; these are
  server/account-side and not something niwa can grant.

What remains is *how* to wire this into niwa: where the host toggle lives, how it
reaches only the dispatch launch, how a downstream value wins, and how ineligibility
is surfaced. Those are the decisions below.

The relevant niwa code:

- `internal/cli/dispatch.go` -- `runDispatch` (the `niwa dispatch` command) provisions
  an instance via the shared `provisionInstanceFunc`, then builds passthrough flags
  with `buildDispatchPassthrough(slug)` and launches via `dispatchLaunch`.
- `internal/cli/dispatch_launcher.go` -- `realDispatchLaunch` runs
  `claude --bg <passthrough> <prompt>` with `cmd.Dir = instanceDir` and
  `cmd.Env = os.Environ()`. `buildClaudeBgArgs` assembles `["--bg", ...passthrough, prompt]`.
- `internal/config/registry.go` -- `GlobalSettings` (the `[global]` table of
  `~/.config/niwa/config.toml`), today `CloneProtocol` and `AutoInstallPlugins *bool`.
  Loaded by `config.LoadGlobalConfig()`.
- `internal/workspace/materialize.go` -- `buildSettingsDoc` produces each instance's
  `.claude/settings.json` from a controlled vocabulary (permissions, hooks, env,
  enabledPlugins, extraKnownMarketplaces, includeGitInstructions). Arbitrary
  `[claude.settings]` keys are NOT passed through.
- `internal/cli/dispatch_plugins.go` -- `readInstanceSettings(instancePath)` reads the
  instance's `.claude/settings.json` into `instanceSettings` (today only
  `enabledPlugins` + `extraKnownMarketplaces`).

## Decision Drivers

- **Dispatch-only scope (PRD R2).** The default must affect `niwa dispatch` workers and
  nothing else. The provisioner and settings materialization are shared with
  interactive/ephemeral/apply sessions, so a dispatch-only effect can only be applied
  at the dispatch launch seam.
- **Downstream override that actually wins (PRD R4), against `--settings` precedence.**
  Because `--settings` outranks project settings.json, niwa must not blindly inject; it
  must apply the host default only when the downstream value is unset.
- **Zero change when unset (PRD R5, AC4).** With the preference unset, the dispatch argv
  and env must be byte-for-byte unchanged.
- **Honest eligibility (PRD R6).** niwa cannot grant entitlement; it should detect the
  cheap, definitive local blocker and report clearly, then still launch.
- **Small, testable surface.** Prefer reusing existing seams (the closed dispatch
  passthrough whitelist, `config.LoadGlobalConfig`, `readInstanceSettings`) over new
  config plumbing threaded through provisioning.

## Considered Options

### Decision 1 -- Where the host toggle lives

- **(1a) A `*bool` on `config.GlobalSettings`** (host config layer 1,
  `~/.config/niwa/config.toml` `[global]`). One struct field; loaded cheaply on the
  dispatch path via `config.LoadGlobalConfig()`. Consumed directly by `runDispatch`, so
  it can be scoped to dispatch.
- **(1b) The overlay's `[global.claude.settings]`** (host config layer 2). Rejected:
  `MergeGlobalOverride` materializes these into *every* instance's settings.json via the
  shared `SettingsMaterializer`, so it cannot be scoped to dispatch (violates R2), and
  `buildSettingsDoc`'s controlled vocabulary drops unknown keys anyway.
- **(1c) A brand-new niwa config file / surface.** Rejected: redundant with the existing
  host-config rung; more surface for no benefit.

### Decision 2 -- How the toggle reaches the worker (injection seam)

- **(2a) Append `--settings '{"remoteControlAtStartup":true}'` to the dispatch argv**
  (via `buildDispatchPassthrough` / the passthrough slice in `runDispatch`). The
  passthrough whitelist and `buildClaudeBgArgs` are reached only by dispatch, so this is
  dispatch-scoped by construction, and it uses the real enable mechanism (the settings
  key). Each value is a discrete argv element (no shell interpolation), preserving the
  existing anti-injection property.
- **(2b) Set an env var in `realDispatchLaunch`'s `cmd.Env`.** Rejected: there is no
  documented claude env var that enables remote-control (the lever is the settings key);
  env is also inherited by the worker's child processes.
- **(2c) Write the key into the instance's settings.json after provisioning.** Rejected:
  the instance settings.json is a niwa-managed, fingerprinted file; hand-editing it
  post-materialize reintroduces the "modified outside niwa" drift hazard and is not
  naturally dispatch-scoped (the provisioner is shared).

### Decision 3 -- How a downstream "off" wins (override resolution)

- **(3a) niwa default-fills: inject only when the downstream value is unset.** In
  `runDispatch`, read the provisioned instance's effective `remoteControlAtStartup`
  (extend `readInstanceSettings`); inject `--settings true` only if the host toggle is on
  AND the key is absent. To make a downstream `[claude.settings].remoteControlAtStartup`
  both reach the worker and be readable here, extend `buildSettingsDoc`'s vocabulary to
  emit the key. Correctness is double-anchored: even if niwa's inject decision were
  wrong, the worker still loads the downstream value from its own project settings.json
  when niwa does not inject.
- **(3b) Rely on claude's precedence.** Rejected: Variant C proved `--settings` outranks
  project settings.json, so a blind inject would override a downstream "off".
- **(3c) Make the preference a niwa-level value overridable at every rung, threaded out
  of provisioning.** Rejected for v1: requires a new field on multiple override structs
  plus plumbing the effective config out of `applier.Create` (today `provisionResult`
  returns only `{Name, Path}`); larger surface than reading the already-written instance
  settings.json.

## Decision Outcome

Chosen: **1a + 2a + 3a.**

- Add `RemoteControlOnDispatch *bool` (`toml:"remote_control_on_dispatch,omitempty"`) to
  `config.GlobalSettings`. Unset (`nil`) preserves today's behavior; `true` enables the
  default; `false` is an explicit host-level off.
- In `runDispatch`, after `provisionInstanceFunc` returns, load the global config and the
  provisioned instance's settings. Compute the inject decision and append
  `--settings '{"remoteControlAtStartup":true}'` to the dispatch passthrough only when
  warranted.
- Extend niwa's Claude settings vocabulary so a downstream
  `[claude.settings].remoteControlAtStartup` (workspace / instance / repo) materializes
  into the instance settings.json (`buildSettingsDoc`) and is read back
  (`readInstanceSettings`), making the downstream override both effective at the worker
  and visible to the resolver.
- When the host toggle would enable remote-control but `ANTHROPIC_API_KEY` is present in
  the environment niwa will pass to the worker (which forces API-key auth and definitively
  precludes remote-control), skip injection and print a single-line reason; still launch.

The resolver rule (the heart of the design):

```
enableRC := false
if globalSettings.RemoteControlOnDispatch != nil && *globalSettings.RemoteControlOnDispatch {
    // host default is ON
    if instanceSettings.RemoteControlAtStartup == nil {   // downstream did not set it
        if apiKeyAuthForced(env) {                        // definitive local ineligibility
            warn("remote-control on dispatch enabled, but ANTHROPIC_API_KEY forces " +
                 "API-key auth; Claude Code Remote needs a claude.ai login, so the " +
                 "worker will start without remote-control")
        } else {
            enableRC = true
        }
    }
    // if downstream set it (true or false), the worker honors it via its own
    // settings.json; niwa injects nothing.
}
if enableRC {
    passthrough = append(passthrough, "--settings", `{"remoteControlAtStartup":true}`)
}
```

This satisfies every PRD requirement: R1 (host field), R2 (dispatch-only seam), R3
(default-on when unset downstream), R4 (downstream value wins -- niwa never injects over
it, and the worker loads it directly), R5/AC4 (no append at all when the toggle is
unset), R6 (clear reason + still launch on the definitive ineligible signal).

## Solution Architecture

Four touch points, each small and independently testable:

1. **Host config field** -- `internal/config/registry.go`:
   `GlobalSettings.RemoteControlOnDispatch *bool`, TOML key `remote_control_on_dispatch`.
   No change to `LoadGlobalConfig` (it already decodes `[global]`).

2. **Settings vocabulary (downstream override plumbing)**:
   - `internal/workspace/materialize.go` -- `buildSettingsDoc` recognizes
     `remoteControlAtStartup` in `cfg.Settings` and emits `doc["remoteControlAtStartup"]`
     as a JSON boolean. (The `SettingsConfig` value is parsed to bool; an unparseable
     value is a config error, consistent with how `permissions` rejects unknown values.)
   - `internal/cli/dispatch_plugins.go` -- `instanceSettings` gains
     `RemoteControlAtStartup *bool `json:"remoteControlAtStartup"`` so
     `readInstanceSettings` surfaces the key (nil when absent).
   This addition only materializes the key when a user explicitly sets it in
   `[claude.settings]`; it does not turn remote-control on anywhere by default, so R2 (the
   *default* is dispatch-only) is preserved. A user who explicitly sets the key in
   `[claude.settings]` is making their own choice and it applies wherever they set it --
   which is correct and expected, distinct from niwa's dispatch-only default.

3. **Dispatch resolver + injection** -- `internal/cli/dispatch.go` (`runDispatch`):
   after `provisionInstanceFunc` returns `{Name, Path}`, call `config.LoadGlobalConfig()`
   and `readInstanceSettings(instancePath)`, apply the resolver rule above, and append the
   `--settings` pair to the `passthrough` slice (built by `buildDispatchPassthrough`)
   before `dispatchLaunch`. A `readInstanceSettings` error (missing/corrupt file) is
   treated as "key absent" for the inject decision but does not fail the dispatch.

4. **Eligibility check** -- a small helper that reports whether the env niwa will pass
   forces API-key auth (i.e., `ANTHROPIC_API_KEY` set). Used only to gate the warning +
   skip-inject branch.

Data flow: `~/.config/niwa/config.toml [global].remote_control_on_dispatch` →
`runDispatch` reads it → cross-checks the provisioned instance's effective
`remoteControlAtStartup` (which already reflects the full workspace/instance/repo +
overlay merge, now that the key is in the vocabulary) → conditionally appends one
dispatch-only `--settings` flag → `claude --bg` worker starts with the bridge on.

No diagram is required; the four touch points and the linear data flow above are the
whole architecture.

## Implementation Approach

Build order (each step compiles and is unit-tested before the next):

1. `GlobalSettings.RemoteControlOnDispatch *bool` + a TOML round-trip test
   (`internal/config`).
2. `buildSettingsDoc` emits `remoteControlAtStartup`; `instanceSettings` reads it. Unit
   tests: a `[claude.settings].remoteControlAtStartup = false` materializes to
   `{"remoteControlAtStartup": false}`; `readInstanceSettings` returns the pointer; absent
   key → nil (`internal/workspace`, `internal/cli`).
3. Extract the resolver rule into a pure helper
   `resolveDispatchRemoteControl(global, instanceSettings, env) (inject bool, warning string)`
   so the decision matrix is table-tested without exec: host on/off/unset ×
   downstream true/false/unset × ANTHROPIC_API_KEY present/absent.
4. Wire the helper into `runDispatch`: load global config, read instance settings, append
   the `--settings` pair when `inject`, print `warning` when non-empty. Assert via the
   existing `dispatchLaunch` test seam that the constructed argv contains the pair only in
   the expected cases and is unchanged when the toggle is unset (AC4).
5. Docs: a short section in the dispatch guide + global-config reference covering the host
   preference, the downstream `[claude.settings].remoteControlAtStartup` override, and the
   eligibility caveat (PRD R7).

Testing strategy: the decision logic is a pure function (step 3) covered by a truth table;
the argv wiring is covered by the package-level `dispatchLaunch` fake already used by
dispatch tests; materialization is covered by `buildSettingsDoc` golden-style assertions.
`go build ./...` and `go test ./...` are the gates. No live `claude` is needed in CI --
the spike already established the runtime behavior; niwa's tests assert only what niwa
constructs.

## Security Considerations

- **No shell interpolation.** The `--settings` value is a fixed literal appended as a
  discrete argv element via the existing passthrough slice; it is never concatenated into
  a shell string, preserving the dispatch path's anti-injection property (the prompt and
  passthrough are already discrete argv elements).
- **No secret handling.** `remote_control_on_dispatch` is a non-sensitive boolean; the
  injected JSON carries no credentials. Remote-control auth continues to flow through
  claude's own file-based OAuth credentials, untouched by niwa.
- **No new managed-file mutation.** The design deliberately avoids writing the instance
  settings.json post-provision (Decision 2c), so it does not touch niwa's managed-file
  fingerprint surface.
- **Honest failure.** niwa never claims remote-control is active; on the definitive
  ineligible signal it warns and degrades, and otherwise leaves claude to surface its own
  bridge-disabled reasons. niwa does not attempt to read or exfiltrate
  `~/.claude/.credentials.json`.

## Consequences

Positive:

- One host preference makes every dispatched worker steerable with no per-dispatch step;
  interactive and other sessions are untouched.
- Downstream override is double-anchored (niwa default-fills only when unset; the worker
  also honors its own settings.json), so a downstream "off" holds firm against claude's
  `--settings` precedence.
- The change is four small, independently testable touch points reusing existing seams; no
  provisioning plumbing or new config file.

Negative / trade-offs:

- niwa's Claude settings vocabulary grows by one key (`remoteControlAtStartup`). This is
  the cost of letting downstream config express the override through the surface users
  already know; it is a contained, well-understood addition.
- `runDispatch` now reads the global config and the instance settings.json on every
  dispatch (two cheap file reads); negligible cost, but it is new I/O on the dispatch path.
- Eligibility detection is intentionally minimal (only `ANTHROPIC_API_KEY`); other
  server-side ineligibilities (scopes, rollout, org policy) are surfaced by claude at
  bridge-connect time, not by niwa. This is honest but means niwa's "clear reason" covers
  only the most common local cause.

## References

- docs/prds/PRD-remote-control-by-default.md -- upstream PRD (requirements).
- docs/briefs/BRIEF-remote-control-by-default.md -- upstream brief (framing).
- docs/spikes/SPIKE-remote-control-by-default.md -- feasibility spike (Complete):
  per-session key suffices (Variant A), `--settings` outranks project settings.json
  (Variant C).
- Implementation issues and sequencing: see docs/plans/PLAN-remote-control-by-default.md.
