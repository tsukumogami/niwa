# Remote-control by default on dispatched workers

`niwa dispatch` launches a background Claude Code worker you walk away from and
monitor later from Agent View, claude.ai, or your phone. Claude Code Remote -- the
bridge that makes a session steerable from those places -- is off unless turned on,
and a dispatched worker starts headless, so you can't flip it on yourself after the
fact. This host-level preference makes dispatched workers start steerable by default.

## Enable it (host level)

Set the preference once in your host niwa config at `~/.config/niwa/config.toml`
(or `$XDG_CONFIG_HOME/niwa/config.toml`):

```toml
[global]
remote_control_on_dispatch = true
```

From then on, every worker you launch with `niwa dispatch` starts with Claude Code
Remote enabled -- immediately watchable and steerable from Agent View / mobile -- with
no per-dispatch step.

The preference is **scoped to `niwa dispatch` only**. Interactive sessions, ephemeral
SessionStart-hook sessions, and `niwa apply` instances are unaffected. When the
preference is unset, dispatch behaves exactly as before.

## Turn it off for a specific workspace or instance

The host preference is a default, not a mandate. To opt out, set
`remoteControlAtStartup` under the relevant scope's Claude settings in
`workspace.toml` -- `[claude.settings]` for the whole workspace, or
`[instance.claude.settings]` for the instance root:

```toml
# workspace scope
[claude.settings]
# values in [claude.settings] are written as quoted strings
remoteControlAtStartup = "false"
```

```toml
# instance scope
[instance.claude.settings]
remoteControlAtStartup = "false"
```

A downstream value wins over the host default: with `remoteControlAtStartup = "false"`
set, workers dispatched for that scope start with remote-control off even while every
other dispatch still starts on. Setting it to `"true"` is also honored (and applies
wherever you set it, not just to dispatch).

## Eligibility

niwa enabling the setting does not grant remote-control entitlement. Claude Code Remote
also requires a first-party claude.ai login (not an API key) with the right scopes, an
active subscription, and an account/org where the bridge rollout is enabled.

If `ANTHROPIC_API_KEY` is set in the environment niwa passes to the worker, Claude Code
is forced into API-key auth, which cannot use remote-control. In that case `niwa
dispatch` prints a one-line reason and still launches the worker:

```
niwa dispatch: remote-control on dispatch is enabled, but ANTHROPIC_API_KEY is set, which forces API-key auth; Claude Code Remote requires a claude.ai login, so the worker will start without remote-control
```

Other eligibility gaps (missing scopes, subscription, org policy, feature rollout) are
surfaced by Claude Code itself when the worker tries to connect the bridge, not by niwa.
