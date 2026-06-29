# Lead: How does Claude Code enable remote-control on a launched session?

All findings below are grounded in the locally installed Claude Code binary
(`/home/dgazineu/.local/share/claude/versions/2.1.195`, a non-stripped ELF with a
bundled JS payload), `claude --help` / `claude agents --help`, the live
`~/.claude/` state, and the running process tree on this host.

## Findings

### The enablement mechanism (three surfaces, one underlying lever)

Remote Control = the "CCR bridge" (Claude Code Remote). It is turned on by a
**settings.json boolean key**, not a special launch mode:

- **`remoteControlAtStartup`** (settings.json). Schema description extracted from the
  binary: `remoteControlAtStartup: H.boolean().optional().describe("Start Remote
  Control bridge automatically each session")`. The resolver is:
  ```
  function Ccr(){ return a0()?.settings.remoteControlAtStartup ?? Dt().remoteControlAtStartup }
  function Lfe(){ let e=Ccr(); if(e!==void 0) return e; return getCcrAutoConnectDefault() }
  ```
  i.e. the per-session/project setting wins; if absent, the user setting; if still
  unset, a server/flag default (below).
- This host's `~/.claude/settings.json` currently has **`"remoteControlAtStartup": false`** —
  it is explicitly DISABLED right now. (It also has `"agentPushNotifEnabled": true`.)
- The settings.json key was migrated from an older `replBridgeEnabled` key (the binary
  contains the one-time migration `remoteControlAtStartup: Boolean(replBridgeEnabled)`).

CLI flag (interactive only):
- **`--remote-control [name]`** — "Start an interactive session with Remote Control
  enabled (optionally named)". Plus `--remote-control-session-name-prefix <prefix>`
  (default: hostname). These are documented as *interactive* session flags, so they
  pair with a normal `claude` launch, not cleanly with `--bg` (which is non-interactive).
- Runtime toggle: the **`/remote-control`** slash command (the binary even prints
  "--teleport sessions start without Remote Control. Use /remote-control to enable it.").

Default when `remoteControlAtStartup` is unset (`getCcrAutoConnectDefault`, fn `xVo`):
```
function xVo(){ if(TF()) return false;                       // cloud/remote session -> off
               if(cMe()) return true;                        // cMe() is hardcoded false
               let e=aKr("remote_control_at_startup");        // server-managed setting
               if(e!==void 0) return e;
               return at("tengu_cobalt_harbor", false) }      // GrowthBook flag, default false
```
So with no explicit setting, RC auto-connect is **off by default** unless the account's
server-side managed setting `remote_control_at_startup` or the `tengu_cobalt_harbor`
feature flag turns it on.

### Auth / eligibility gating (the `getBridgeDisabledReason` / `wir()` chain)

Even with the setting on, the bridge only connects if ALL of these pass (function
names and user-facing messages pulled from the binary):

1. **First-party OAuth login, not API key.** `Jl()` requires auth type `"firstParty"`.
   Messages: "You must be logged in to use Remote Control." / "Run `claude auth login`
   to use Remote Control." API-key / Bedrock / Vertex auth does NOT qualify. (Note
   `--bare` mode forces API-key-only auth and so cannot use RC.)
2. **Full-scope login token incl. the right scopes.** `Ecr()` checks the token scope
   set includes `xB` where `xB="user:inference"`; `Acr()` -> "Remote Control requires a
   full-scope login token"; the doctor separately flags "Sign-in is missing the
   user:profile scope". This host's `~/.claude/.credentials.json` (OAuth, key
   `claudeAiOauth`) has scopes `[user:file_upload, user:inference, user:mcp_servers,
   user:profile, user:sessions:claude_code]` and `subscriptionType: max` — fully eligible.
3. **A claude.ai subscription** (`Ecr()` subscription check; this host = Max).
4. **Org policy allows it.** Managed setting `disableRemoteControl` must not be `true`
   (`AZt()` -> "Remote Control is disabled by your organization's policy (managed
   setting `disableRemoteControl`)"), and the org policy verdict `allow_remote_control`
   (`ZXt()`) must be "allowed".
5. **Feature-flag rollout for the account.** Requires GrowthBook flag `tengu_ccr_bridge`
   (`await _U("tengu_ccr_bridge")`) and "Remote Control rollout enabled for this
   account". If `DISABLE_GROWTHBOOK` is set: "Remote Control requires feature-flag
   evaluation, which is disabled because DISABLE_GROWTHBOOK is set." Needs network
   egress to evaluate flags.
6. **Not inside a cloud/remote session.** `TF() = isTruthy(process.env.CLAUDE_CODE_REMOTE) || isCloud()`.
   If `CLAUDE_CODE_REMOTE` is set, RC is treated as already-remote and disabled locally.
7. **Recent enough version** ("... is too old for Remote Control") and an **HTTPS** bridge
   base URL ("Remote Control base URL uses HTTP" is rejected).

There is also an env override **`CLAUDE_CODE_FORCE_BRIDGE`** (binary symbol `vIu`) that
appears to force the bridge regardless of the auto-connect default — a possible
secondary lever, though the eligibility gates above still apply.

### Architecture: daemon + bridge (how `--bg` fits)

- Background agents are owned by a long-lived **daemon**: the process tree shows
  `claude daemon run --json-path ~/.claude/daemon.json ... --origin transient
  --spawned-by {"label":"claude agents",...}`, which forks `--bg-pty-host` and
  `--bg-spare` workers. This very session is one of those daemon-spawned bg workers.
- Daemon state lives in `~/.claude/daemon/` (`control.key`, `roster.json`, `auth/`) plus
  `~/.claude/daemon.status.json`.
- Related settings keys in the same group: **`autoAddRemoteControlDaemonWorker`** (the
  daemon registers itself as a Remote Control worker so Agent View can see/steer bg
  agents) and `agentPushNotifEnabled` (mobile push; binary logs "Mobile push not sent
  (Remote Control inactive)" when RC is off). Cross-session peers are described as
  `bridge:...` for cross-machine Remote Control sessions vs `uds:...` for same-machine.

### Is `--bg` remote-controllable automatically?

- `--bg` by itself only registers a background agent ("Start the session as a
  background agent and return immediately (manage with `claude agents`)"). It does NOT
  independently turn on the RC bridge — bridge auto-connect is governed by
  `remoteControlAtStartup` / its server default, gated by the eligibility chain above.
- That said, this dispatched bg worker DOES have the `mcp__claude_ai_Claude_Code_Remote__*`
  tools live. Those are claude.ai connector tools that appear because the account is
  logged into claude.ai with session sync active — strong evidence that on a properly
  logged-in host the claude.ai linkage is present for bg workers. (Worth distinguishing
  "session linked/uploaded to claude.ai" from "session live-steerable via the bridge";
  the MCP tools prove the former, `remoteControlAtStartup` controls the latter.)

## Implications

For niwa to make `niwa dispatch` workers (launched via `claude --bg <prompt>`,
`cmd.Env = os.Environ()`) remote-controllable by default, the host-level niwa setting
maps to exactly one concrete action plus some preconditions:

1. **Set `remoteControlAtStartup: true` in the settings the worker loads.** Options:
   - Write it into `~/.claude/settings.json` (user settings), or a project
     `.claude/settings.json` the worker picks up; or
   - Pass it at launch via `--settings '{"remoteControlAtStartup":true}'`, or for the
     agent-view path `claude agents --settings ...`.
   The host's current value is `false`, which explicitly disables RC — niwa must flip
   it. Do not rely on `--remote-control` for `--bg`; that flag is documented as
   interactive-only. The setting is the correct lever for background sessions.
2. **Inherit valid first-party OAuth creds.** RC needs claude.ai OAuth login (not API
   key) with full scopes (`user:inference` + `user:profile`) and an active subscription.
   These are file-based in `~/.claude/.credentials.json`, so a normally-logged-in host
   already satisfies this and the inherited environment carries nothing extra. If a
   worker ever runs with only `ANTHROPIC_API_KEY` / `--bare`, RC will be unavailable.
3. **Do not poison the environment.** Because niwa copies `os.Environ()`, ensure the
   parent does NOT export `CLAUDE_CODE_REMOTE` (would mark the worker as already-remote
   and disable local RC) or `DISABLE_GROWTHBOOK` (kills feature-flag eval). Ensure the
   worker has network egress for GrowthBook flag evaluation and the HTTPS bridge.
4. **Account/org preconditions niwa cannot set:** the `tengu_ccr_bridge` rollout must
   include the account, org policy must allow (`allow_remote_control`, not
   `disableRemoteControl`). These are server-side; niwa can at most surface a clear error
   if `claude` reports the bridge disabled.

Net: the niwa host setting is essentially "write `remoteControlAtStartup: true` into the
dispatched worker's settings, given a logged-in claude.ai host." Everything else is
precondition validation, not configuration niwa injects.

## Surprises

- The host's settings.json currently has `remoteControlAtStartup: false` (explicitly
  off), yet this bg worker still has the claude.ai Remote-Control MCP tools — confirming
  the MCP connector tools track claude.ai login/session-sync, which is separate from the
  per-session steering bridge that `remoteControlAtStartup` governs.
- RC is OAuth-only; an API-key worker (or `--bare`) can never be remote-controlled. This
  is the opposite of the usual "headless = API key" assumption.
- Background agents are mediated by a persistent `claude daemon run` supervisor, and a
  distinct setting `autoAddRemoteControlDaemonWorker` exists specifically to make the
  daemon expose bg agents to Agent View — niwa may need that too, not just per-session
  `remoteControlAtStartup`.

## Open Questions

- Does Agent-View steerability of a `--bg` worker require `autoAddRemoteControlDaemonWorker`
  (daemon-level) in addition to `remoteControlAtStartup` (session-level), or does the
  session setting alone suffice? Needs a live test toggling each independently.
- Empirically confirm a freshly dispatched `claude --bg` worker with
  `remoteControlAtStartup: true` actually appears as steerable in Agent View / mobile
  (vs merely linked). The MCP-tool presence is suggestive but not proof of live steering.
- Whether `CLAUDE_CODE_FORCE_BRIDGE=1` is a supported/forward-compatible way to force RC
  for headless workers, or an internal test hook to avoid.
- Exact precedence when both `--settings` and `~/.claude/settings.json` set the key for a
  daemon-dispatched worker (which "settings source" the daemon honors).

## Summary
Remote Control is enabled by the settings.json boolean `remoteControlAtStartup`
("Start Remote Control bridge automatically each session") — not by `--bg`, which only
registers a background agent; the `--remote-control` CLI flag is interactive-only and
the underlying default (when the key is unset) is decided by the server setting
`remote_control_at_startup` / the `tengu_cobalt_harbor` flag, and is currently
explicitly `false` on this host. For niwa, the host-level setting maps to writing
`remoteControlAtStartup: true` into the dispatched worker's settings (or `--settings`),
given a first-party claude.ai OAuth login with `user:inference`+`user:profile` scopes
and a subscription, no `CLAUDE_CODE_REMOTE`/`DISABLE_GROWTHBOOK` in the inherited env,
and network egress — all of which a normal logged-in host already provides. The biggest
open question is whether Agent-View steering of bg workers also needs the daemon-level
`autoAddRemoteControlDaemonWorker` setting in addition to the per-session key.
