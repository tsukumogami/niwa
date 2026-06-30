---
schema: prd/v1
status: Accepted
problem: |
  `niwa dispatch` launches background Claude Code workers the developer cannot be
  at the keyboard for, yet Claude Code Remote (the steer-from-afar bridge) is off
  by default and only enableable per-session. So the unattended sessions a
  developer most wants to monitor and steer arrive unsteerable, and the only
  workaround is to hand-thread the setting through on every dispatch.
goals: |
  Give niwa a single host-level preference that makes `niwa dispatch` workers start
  with Claude Code Remote on, applied only to dispatched workers, as a default that
  a downstream workspace or instance can turn off. Where the host cannot
  actually use remote-control, steer the developer with a clear reason rather than
  a silent no-op.
upstream: docs/briefs/BRIEF-remote-control-by-default.md
motivating_context: |
  A feasibility spike (docs/spikes/SPIKE-remote-control-by-default.md, Complete)
  proved: (1) launching `claude --bg` with the settings key
  `remoteControlAtStartup: true` -- and nothing else, no daemon-level setting --
  makes the worker live-steerable from Agent View / mobile; (2) `claude --settings`
  outranks the worker's project settings.json, so a host default injected that way
  would override a downstream "off" unless niwa resolves the override itself. This
  PRD captures the requirements now that the mechanism and the override constraint
  are settled.
---

# PRD: remote-control by default on dispatched workers

## Status

Accepted

The downstream DESIGN owns the implementation mechanism (the host-config field, the
dispatch-time read-and-resolve, the `--settings` injection seam, and the eligibility
message). This PRD owns the requirements and the developer-facing contract.

## Problem Statement

niwa launches Claude Code sessions in several ways; `niwa dispatch` launches a
*background* worker -- a session a developer hands a task to and walks away from,
monitoring it later from Agent View, claude.ai, or mobile. The point of a dispatched
worker is that the developer is not at its keyboard.

Claude Code Remote -- the bridge that makes a session live-steerable from claude.ai
and mobile -- is governed by the per-session setting `remoteControlAtStartup`, and it
is off unless turned on. For an interactive session the developer enables it
themselves. For a dispatched worker they cannot: the worker starts headless and runs
unattended, so the moment to enable remote-control passes before the developer ever
looks. The host default is off, and niwa offers no way to say "every worker I dispatch
should start steerable." The sessions that most need remote-control are the ones that
arrive without it, and the only workaround is a per-dispatch ritual.

## Goals

- Make "dispatched workers start with remote-control on" a single host-level niwa
  preference, set once, with no per-dispatch step.
- Scope the effect to `niwa dispatch` workers only -- interactive root/instance
  sessions, ephemeral SessionStart-hook sessions, and `niwa apply` instances are
  untouched.
- Keep it a default, not a mandate: a downstream workspace or instance config
  can turn it off, and that downstream choice wins over the host default.
- When the host cannot actually use Claude Code Remote, surface a clear reason on
  dispatch rather than silently starting a worker that never becomes steerable.
- Preserve today's behavior when the preference is unset (off by default; no change
  to any existing dispatch).

## User Stories

- As a developer who dispatches workers, I want to set "dispatched workers start
  steerable" once at the host level so that every `niwa dispatch` worker is
  immediately watchable and steerable from Agent View / mobile without a per-dispatch
  step.
- As a developer with one sensitive workspace, I want to turn the default off for that
  workspace so that its dispatched workers start unsteerable while every other dispatch
  still starts steerable.
- As a developer on a host that cannot use remote-control, I want niwa to tell me
  why remote-control was not enabled for a dispatch so that I understand the gap
  instead of wondering why the worker never appeared as steerable.
- As a developer who has never set the preference, I want dispatch to behave exactly
  as it does today so that adopting niwa's new version changes nothing until I opt in.

## Requirements

R1. niwa MUST provide a host-level preference, set in niwa's existing host config
(`~/.config/niwa/config.toml`, the `[global]` table), that enables Claude Code Remote
by default on workers launched by `niwa dispatch`. The preference is a single boolean
with an explicit unset state.

R2. The preference MUST affect only sessions launched by `niwa dispatch`. Interactive
root/instance sessions, ephemeral SessionStart-hook sessions, and `niwa apply`
instances MUST be unaffected by it.

R3. When the preference is on and no downstream override is present, a dispatched
worker MUST start with Claude Code Remote enabled (the worker loads
`remoteControlAtStartup: true`).

R4. A downstream niwa config (workspace or instance) that sets
`remoteControlAtStartup` MUST win over the host preference. In particular, a downstream
value of `false` MUST cause the dispatched worker to start with remote-control off even
when the host preference is on. The host preference is a default-fill, never a forced
override.

R5. When the preference is unset, `niwa dispatch` MUST behave exactly as it does today
-- it MUST NOT enable remote-control and MUST NOT alter the worker's launch.

R6. When the preference is on but the host cannot use Claude Code Remote (for example,
API-key auth rather than a claude.ai login, missing scopes, or org policy disabling
remote-control), `niwa dispatch` MUST surface a clear, single-line reason that
remote-control was not enabled for the dispatch, and MUST still launch the worker
(degrade to today's behavior, do not fail the dispatch).

R7. The preference and its behavior MUST be documented for developers (how to set the
host preference, how to override it downstream, and the eligibility caveat).

## Acceptance Criteria

AC1. With the host preference on and no downstream `remoteControlAtStartup`, a worker
launched by `niwa dispatch` starts with `remoteControlAtStartup: true` in effect; the
same dispatch with the preference unset does not.

AC2. With the host preference on, launching an interactive root/instance session, an
ephemeral SessionStart-hook session, or a `niwa apply` instance does not enable
remote-control for it.

AC3. With the host preference on and a downstream config setting
`remoteControlAtStartup: false`, a worker launched by `niwa dispatch` starts with
remote-control off.

AC4. With the preference unset, the argv and environment `niwa dispatch` uses to launch
`claude` are byte-for-byte unchanged from current behavior.

AC5. With the preference on and a host that cannot use remote-control, `niwa dispatch`
prints a clear one-line reason and still launches the worker.

AC6. The behavior is covered by automated tests at the dispatch layer (preference on /
off / unset, downstream override, dispatch-only scoping), runnable via `go test ./...`.

## Decisions and Trade-offs

- **Host-config preference, not raw settings passthrough.** The preference lives as a
  first-class boolean in niwa's host config rather than being expressed as a raw
  Claude settings key in the overlay's `[global.claude.settings]`. The overlay path
  materializes into every instance's settings.json and cannot be scoped to dispatch;
  a dedicated preference can. (DESIGN owns the exact field.)
- **Default-fill, not override.** Because `claude --settings` outranks the worker's
  project settings.json (spike Variant C), niwa must apply the host preference only
  when the downstream value is unset, so a downstream "off" is honored. The trade-off
  is that niwa must read the worker's effective setting before deciding to inject.
- **Best-effort eligibility.** niwa cannot grant remote-control entitlement (OAuth
  login, scopes, org policy, feature rollout are server/account-side). niwa's
  obligation stops at enabling the setting and reporting clearly when the host is
  ineligible; it does not attempt to fix eligibility.

## Known Limitations

- Remote-control still requires a first-party claude.ai login with the right scopes,
  an active subscription, and an account/org where the bridge rollout is enabled.
  niwa enabling the setting does not bypass any of these.
- The preference is host-scoped (per machine, via `~/.config/niwa/config.toml`); it is
  not a per-workspace host preference. Per-workspace nuance is expressed through the
  downstream override (R4), not through multiple host preferences.

## Out of Scope

- Enabling remote-control on interactive root/instance sessions, ephemeral
  SessionStart-hook sessions, or `niwa apply` instances.
- Cross-machine / remote dispatch (an existing non-goal in
  `docs/briefs/BRIEF-instance-dispatch.md`).
- Building or changing Claude Code Remote itself; niwa only flips a switch the harness
  already provides.
- Daemon-level Agent-View registration tuning (`autoAddRemoteControlDaemonWorker`); the
  spike showed the per-session key alone suffices, so no daemon-level work is required.

## References

- docs/briefs/BRIEF-remote-control-by-default.md -- upstream brief.
- docs/spikes/SPIKE-remote-control-by-default.md -- feasibility spike (Complete): the
  per-session key suffices and `--settings` outranks project settings.json.
