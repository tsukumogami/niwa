# Exploration Findings: remote-control-by-default

## Core Question

How should niwa enable Claude Code Remote ("remote-control") by default on the
sessions it starts via `niwa dispatch`, configured through a host-level niwa
setting, as an overridable default living in niwa's existing host config layer?

## Round 1

### Key Insights

- **The enable mechanism is a settings.json boolean, not a launch flag.** Remote
  Control turns on via `remoteControlAtStartup: true` ("Start Remote Control
  bridge automatically each session"). The `--remote-control` CLI flag is
  documented **interactive-only** and does not pair with `--bg`; `--bg` alone only
  registers a background agent and does not start the bridge. (lead-ccr-mechanism)
- **This host currently has `remoteControlAtStartup: false`** in
  `~/.claude/settings.json` — RC is explicitly OFF right now, so niwa must flip it,
  not just rely on a default. When unset, the default is server-side
  (`remote_control_at_startup` managed setting / `tengu_cobalt_harbor` flag),
  currently false. (lead-ccr-mechanism)
- **Only one dispatch-exclusive seam exists.** Dispatch and the interactive /
  ephemeral SessionStart hook share ONE provisioner (`provisionInstanceFunc` →
  `realProvisionInstance`), so anything injected at provision / settings-materialize
  is unavoidably shared with interactive sessions. The sole dispatch-only window is
  the argv/env built in `buildDispatchPassthrough` (`dispatch.go:385`) +
  `buildClaudeBgArgs` / `realDispatchLaunch`'s `cmd.Env` (`dispatch_launcher.go`).
  (lead-dispatch-plumbing)
- **The host toggle must live in config layer 1, not layer 2.** Layer 1 =
  `~/.config/niwa/config.toml` `[global]` → `config.GlobalSettings`
  (`registry.go:27`, today holds `clone_protocol`, `auto_install_plugins`). Layer 2
  = the overlay repo's `niwa.toml` `[global.claude.settings]`, which
  `MergeGlobalOverride` materializes into EVERY instance's settings.json — cannot be
  scoped to dispatch. Adding a `*bool` to `GlobalSettings` is a one-line struct
  change, and it's cheaply readable on the dispatch path via
  `config.LoadGlobalConfig()` (already called one function away in
  `realProvisionInstance`). (lead-dispatch-plumbing, lead-host-config-layer)
- **`buildSettingsDoc` is a controlled vocabulary** that silently drops unknown
  settings keys — so even if you wanted to route `remoteControlAtStartup` through
  the normal settings materialization, the key would need explicit vocabulary
  support, and that path isn't dispatch-scoped anyway. (lead-host-config-layer)
- **Override precedence across niwa rungs is known.** Global override applies on
  top of workspace ("global wins per key"), BEFORE instance/repo rungs (which
  therefore win). A downstream "off" beats a host "on" at the instance/repo level;
  at the workspace rung, same-key override needs a default-fill, not a merge.
  (lead-host-config-layer)
- **Auth preconditions are already satisfied on a normal logged-in host.** RC needs
  first-party claude.ai OAuth (NOT API key / `--bare`), scopes `user:inference` +
  `user:profile`, an active subscription, no `CLAUDE_CODE_REMOTE` /
  `DISABLE_GROWTHBOOK` in the inherited env, and network egress. `niwa dispatch`
  inherits `os.Environ()` and file-based `~/.claude/.credentials.json`, so a
  logged-in host qualifies; an API-key-only worker never can. (lead-ccr-mechanism)
- **No prior art, no conflicting non-goal.** `niwa dispatch` is fully built but
  forwards only an allowlisted flag set (`--model` / `--permission-mode` /
  `--agent` / `--name`); there is no host/global-sourced launch default today. The
  one nearby non-goal (cross-machine dispatch, BRIEF-instance-dispatch) is distinct
  from remote-controlling a local session. The `--no-ephemeral-sessions`
  overridable-default is a useful precedent pattern. (lead-prior-art-planned)

### Cross-Finding Resolution (the design that falls out)

The dispatch agent framed seams (a) "extra claude flag" and (b) "env var" as
gated on a claude-side flag/env that it couldn't confirm. The CCR agent supplies
the missing fact: the lever is a **settings key**, and `claude` accepts inline
settings via **`--settings '{...}'`**. That reconciles both into one clean design:

- New `*bool` on `config.GlobalSettings` (host layer 1), e.g. `remote_control_on_dispatch`.
- `runDispatch` loads it via `config.LoadGlobalConfig()` and, when on, injects
  `--settings '{"remoteControlAtStartup":true}'` into the dispatch-only argv.
- This is dispatch-scoped for free (the passthrough whitelist / `buildClaudeBgArgs`
  are reached nowhere else) and uses the real mechanism (the settings key) rather
  than the interactive-only `--remote-control` flag.
- For "overridable downstream": niwa can resolve the override itself — read the
  dispatched instance's effective `remoteControlAtStartup` (the instance
  settings.json already reflects the full workspace/instance/repo + overlay merge;
  `readInstanceSettings` helper exists) and only apply the host default when the
  user hasn't set the key downstream. This avoids depending on claude's
  flag-vs-settings precedence order.

### Tensions

- **Dispatch-only scope vs. reuse-the-existing-layer.** The user wanted to reuse
  the existing global override layer (layer 2), but layer 2 materializes into every
  session and can't be dispatch-scoped. Resolution: put the toggle in the *other*
  host rung (layer 1, `GlobalSettings`) — still "host level," still niwa's existing
  config surface, but consumed by the dispatch path directly. Worth confirming the
  user is fine with layer 1 rather than literal `[global.claude.*]`.
- **"Overridable downstream" vs. claude's `--settings` precedence.** If injected as
  a `--settings` flag and claude ranks flags above settings.json, a downstream "off"
  in settings.json would NOT win. Mitigation: have niwa resolve the override (read
  effective settings, inject only when unset) rather than relying on claude
  precedence. Needs the precedence fact confirmed either way.

### Gaps / Open Questions

- **Does Agent-View steering of a `--bg` worker also require the daemon-level
  `autoAddRemoteControlDaemonWorker` setting, in addition to per-session
  `remoteControlAtStartup`?** This is the biggest functional unknown — it decides
  whether the per-session key alone actually makes a dispatched worker steerable, or
  whether a daemon-global setting (harder to scope to dispatch) is also needed.
  Needs a live test, not more code reading.
- **What is claude's precedence between `--settings`, project settings.json, and
  `~/.claude/settings.json` for a daemon-dispatched worker?** Determines the
  override-resolution design.
- **`CLAUDE_CODE_FORCE_BRIDGE`** exists as a possible force lever — supported or an
  internal test hook? Probably avoid.
- **Demand is un-surfaced.** No niwa issue/PR/doc references this; it's a fresh
  user-originated workflow idea (not rejected, just never raised). (lead-adversarial-demand)

### Decisions (this round)

- Toggle lives in `config.GlobalSettings` (host config layer 1), not the overlay's
  `[global.claude.settings]` (layer 2) — layer 2 can't be dispatch-scoped.
- Injection seam = dispatch-only argv via `--settings '{"remoteControlAtStartup":true}'`,
  not the interactive-only `--remote-control` flag, and not a post-provision
  settings.json hand-edit (collides with niwa's managed-file fingerprint).
- Out of scope confirmed: interactive root/instance + ephemeral hook sessions.

## Accumulated Understanding

The feature is small and the implementation seam is well-determined: a new `*bool`
on `GlobalSettings`, read in `runDispatch`, expanded into a dispatch-only
`--settings '{"remoteControlAtStartup":true}'` injection, with niwa resolving
downstream overrides by reading the dispatched instance's effective settings. Auth
and environment preconditions are already met on a normal logged-in host. What is
NOT yet settled is empirical and claude-side: (1) whether the per-session key alone
makes a `--bg` worker steerable in Agent View or whether the daemon-level
`autoAddRemoteControlDaemonWorker` is also required, and (2) claude's settings-source
precedence for daemon workers (which shapes the override-resolution logic). Both are
resolvable only by a live test, so they belong in a spike or a design's validation
step rather than another code-reading round.
