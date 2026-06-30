---
status: Complete
question: |
  Does enabling Claude Code Remote on a `niwa dispatch` background worker actually
  make that worker live-steerable from Agent View / mobile — and is the per-session
  settings key `remoteControlAtStartup: true` sufficient, or is the daemon-level
  `autoAddRemoteControlDaemonWorker` also required? Secondary: what is Claude Code's
  settings-source precedence for a daemon-dispatched worker (so a host default can
  be overridden downstream)?
timebox: "1 session (manual live dogfood, no niwa code changes)"
---

# SPIKE: remote-control by default on `niwa dispatch` workers

## Status

Complete

Go. Variant A proved the core claim live: a `claude --bg` worker launched with
`--settings '{"remoteControlAtStartup":true}'` — and nothing else — is steerable
from Agent View / mobile. The per-session key alone is sufficient; the daemon-level
`autoAddRemoteControlDaemonWorker` is NOT required (it was unset during the test).
Variant C also settled the precedence question: `claude --settings` outranks the
project/user settings.json, so niwa must apply the host default as a default-fill
only (inject when unset), never as a blind override. The niwa plumbing the
exploration designed is viable with that one constraint.

## Question

The goal feature: `niwa dispatch`-launched Claude Code sessions should have Claude
Code Remote ("remote-control") on by default, configured by a host-level niwa
toggle, overridable downstream. Exploration determined the niwa plumbing, but the
approach rests on one unverified empirical claim.

Blocking questions before committing to a design:

1. **Does `remoteControlAtStartup: true` make a `--bg` worker steerable?** When a
   background worker is launched with the Remote Control bridge auto-connect on,
   does it appear as a *live-steerable* session in Agent View and/or mobile — not
   merely "linked/uploaded" to claude.ai? (The MCP connector tools being present is
   evidence of linkage, not proof of steering.)
2. **Per-session key alone, or daemon setting too?** Background workers are owned by
   a long-lived `claude daemon`. A distinct setting `autoAddRemoteControlDaemonWorker`
   exists specifically to make the daemon register bg agents as Remote Control
   workers. Is the per-session `remoteControlAtStartup` enough, or is the
   daemon-level setting also required for Agent-View steering of bg workers?
3. **Settings-source precedence (secondary).** For a daemon-dispatched worker, what
   is the precedence between `claude --settings '{...}'`, project
   `.claude/settings.json`, and `~/.claude/settings.json`? This decides whether a
   host "on" default injected via `--settings` can be overridden by a downstream
   "off", or whether niwa must resolve the override itself.

## Context

Why this matters now: the exploration `explore_remote-control-by-default` settled
the WHAT and most of the HOW, but flagged these as the only correctness-gating
unknowns — and they are claude-side facts resolvable only by a live test, not by
reading niwa or claude source.

What is already known (from exploration, grounds the experiment):

- **The enable lever is a settings key**, not a launch flag:
  `remoteControlAtStartup: true` ("Start Remote Control bridge automatically each
  session"). `--remote-control` is documented interactive-only and does not pair
  with `--bg`; `--bg` alone only registers a background agent and does not start the
  bridge.
- **This host currently has `remoteControlAtStartup: false`** in
  `~/.claude/settings.json` — RC is explicitly OFF, so the experiment must actively
  turn it on.
- **Auth preconditions are met on this host**: first-party claude.ai OAuth (not API
  key), scopes include `user:inference` + `user:profile`, `subscriptionType: max`,
  and `~/.claude/.credentials.json` is file-based so a dispatched worker inherits
  it. The inherited env must NOT contain `CLAUDE_CODE_REMOTE` or `DISABLE_GROWTHBOOK`,
  and the worker needs network egress for GrowthBook flag eval + the HTTPS bridge.
- **Account/org gates are out of niwa's control**: the `tengu_ccr_bridge` rollout
  must include the account and org policy must allow (`allow_remote_control`, not
  `disableRemoteControl`). If the bridge reports disabled for policy/rollout
  reasons, that is a no-go independent of the niwa plumbing.
- **niwa plumbing (for the eventual design, not this spike)**: the dispatch-only
  seam is the argv built in `buildDispatchPassthrough` / `buildClaudeBgArgs` and
  `realDispatchLaunch`'s `cmd.Env` (`internal/cli/dispatch.go`,
  `internal/cli/dispatch_launcher.go`); the host toggle belongs on
  `config.GlobalSettings` (layer 1), not the overlay's `[global.claude.settings]`
  (layer 2, which materializes into every instance and can't be dispatch-scoped).

## Approach

The experiment needs NO niwa code changes — it injects the setting the same way a
future niwa change would (`claude --settings`), or via a temporary settings file,
and observes Agent View / mobile. Run the variants in order and stop at the first
clear result.

Preconditions check (do first):
1. Confirm `~/.claude/settings.json` has `remoteControlAtStartup: false` (baseline)
   and note `autoAddRemoteControlDaemonWorker`'s current value.
2. Confirm the env that `niwa dispatch` would inherit has no `CLAUDE_CODE_REMOTE` /
   `DISABLE_GROWTHBOOK` and the host is logged in (`claude auth` status), with
   network egress.

Variant A — per-session key only, via `--settings` (mirrors the proposed niwa injection):
3. Launch a background worker the way niwa would, but with the setting injected:
   `claude --bg --settings '{"remoteControlAtStartup":true}' "<a harmless long-running prompt>"`
   (run from a throwaway dir; or use `niwa dispatch` once a manual `--settings`
   passthrough is wired in a scratch build — but prefer the raw `claude` form to
   keep the spike niwa-code-free).
4. Open Agent View (`claude agents`) and the mobile/claude.ai app. Record: does the
   worker appear, and can you actually *send it a steering message / interrupt it*
   (live control), versus only viewing its transcript?

Variant B — add the daemon-level setting (only if A is insufficient):
5. Set `autoAddRemoteControlDaemonWorker: true` (user settings), restart the daemon
   if needed, repeat steps 3–4. Record whether steerability now appears.

Variant C — precedence probe (secondary, only if A or B proves steering works):
6. With the worker-level `--settings` injecting `true`, set
   `remoteControlAtStartup: false` in the project `.claude/settings.json` the worker
   loads, dispatch again, and observe whether the bridge connects. This reveals
   whether `--settings` wins over project settings (→ niwa must self-resolve the
   override) or project settings win (→ downstream "off" works for free).

Measurement / evidence to capture for each variant:
- Whether the worker is listed in Agent View and on mobile.
- Whether a steering message actually reaches and affects the worker (the decisive
  signal — live control, not just visibility).
- Any bridge-disabled reason string `claude` emits (policy, rollout, scope, version).
- The effective `remoteControlAtStartup` the worker reports, if observable.

Cleanup: restore `~/.claude/settings.json` to its original
`remoteControlAtStartup: false` (and `autoAddRemoteControlDaemonWorker`) after the
test; remove any throwaway dispatched workers.

## Findings

Run on 2026-06-29, host `claude` v2.1.196, niwa-code-free (raw `claude --bg`).

Preconditions (confirmed before the test):
- `~/.claude/settings.json`: `remoteControlAtStartup: false` (baseline, RC off),
  `autoAddRemoteControlDaemonWorker: <unset>`, `agentPushNotifEnabled: true`.
- Inherited env clean: no `CLAUDE_CODE_REMOTE`, no `DISABLE_GROWTHBOOK`. Host
  logged in with network egress.

**Variant A — per-session key only: STEERABLE (proven).**
- Launched: `claude --bg --settings '{"remoteControlAtStartup":true}' "<keep-alive
  worker prompt>"` from a throwaway cwd. Worker short ID `19171115`.
- The daemon roster (`~/.claude/daemon/roster.json`) recorded the worker with the
  injected `--settings {"remoteControlAtStartup":true}` in both `launch.args` and
  `respawnFlags` — confirming the setting reached the spawned process and survives
  respawn. `claude agents --json` showed it `status: busy / state: working`.
- The user confirmed live from Agent View / mobile that the worker was
  remote-controllable (steerable), with `autoAddRemoteControlDaemonWorker` left
  unset. **Conclusion: the per-session settings key alone is sufficient.**

**Variant B — daemon-level `autoAddRemoteControlDaemonWorker`: NOT NEEDED.**
- Not run. Variant A succeeded with the daemon setting unset, which is exactly the
  condition Variant B existed to test. The per-session key does not depend on the
  daemon-global setting for `--bg` steerability.

**Variant C — settings-source precedence: `--settings` WINS over project settings.json.**
- Setup: throwaway cwd with project `.claude/settings.json` = `{"remoteControlAtStartup": false}`,
  launched `claude --bg --settings '{"remoteControlAtStartup":true}' "..."`. Worker
  short ID `4fce317c`.
- Result: the worker's status bar showed `/rc connecting…` → `/rc` (green) — the
  Remote Control bridge CONNECTED despite the project file saying `false`. (User
  settings also have `false`; `--settings` beat both.)
- **Conclusion: `claude --settings` outranks the project (and user)
  `.claude/settings.json` for `remoteControlAtStartup`.** A downstream project-level
  "off" does NOT defeat a `--settings`-injected "on".
- **Design consequence:** niwa must NOT blindly inject `--settings true`. The
  niwa-materialized instance settings.json IS a project settings.json, so a user who
  sets `remoteControlAtStartup: false` via workspace/instance niwa config would be
  silently overridden — breaking "overridable downstream". niwa must self-resolve:
  read the dispatched instance's effective `remoteControlAtStartup`, and inject
  `--settings true` ONLY when the key is unset downstream (i.e., apply the host
  default solely as a default-fill, never as an override).

## Recommendation

**Go — proceed to a Design Doc** for the niwa host toggle (`*bool` on
`config.GlobalSettings`) plus the dispatch-only `--settings
'{"remoteControlAtStartup":true}'` injection in `runDispatch`. Feasibility is
proven: the per-session key alone makes a dispatched `--bg` worker steerable, so the
design does not need to wrestle with the daemon-global
`autoAddRemoteControlDaemonWorker` scoping problem.

Override mechanism is now decided (Variant C): because `--settings` outranks the
instance's project settings.json, niwa must resolve the override itself — read the
dispatched instance's effective `remoteControlAtStartup` and inject `--settings true`
ONLY when the key is unset downstream. The host toggle is a default-fill, never a
hard override; this is what preserves "overridable downstream".

Eligibility caveat unchanged: remote-control requires a first-party claude.ai OAuth
login with the right scopes + subscription and an account/org where the bridge
rollout is enabled. An API-key-only worker can never be remote-controlled; niwa
should surface a clear message rather than silently no-op when ineligible.
