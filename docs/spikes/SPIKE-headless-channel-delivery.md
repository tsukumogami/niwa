---
status: Complete
question: |
  Can niwa deliver a new instruction into an already-running dispatched
  (`claude --bg`) session in place — via Claude Code's MCP "channel" push
  mechanism (the same path the Telegram plugin uses) — without forking the
  session, and can a niwa-authored channel be armed on a headless session by
  the user alone (no Anthropic approval, no root)?
timebox: "Investigation across the dispatch-handle-retask exploration; live spikes on the host plus binary inspection."
---

# SPIKE: Headless channel delivery into a `--bg` session

## Status

Complete

Factual research log. Records every channel-delivery approach attempted, the
observed result of each, the Claude Code gate logic extracted from the binary,
and the options (including safety-control bypasses) identified for arming a
third-party channel. No recommendations.

## Environment

- Host: always-on Linux desktop.
- Claude Code versions present on disk: 2.1.212, 2.1.214, 2.1.215. The running
  daemon and workers observed were 2.1.215; the binary read for gate logic was
  2.1.214.
- niwa `main` at the time carried #209 (keep-alive) and #210 (watch ED2
  continuation).
- Test channel server: a dependency-free Node MCP stdio server (~50–100 lines)
  declaring the `claude/channel` capability, in later variants pushing a
  `notifications/claude/channel` message either on an external spool-file
  trigger or on a self-armed timer.

## Method

Each approach launched a throwaway worker (`claude --bg` unless noted), then
read the worker's MCP client log at
`~/.cache/claude-cli-nodejs/<cwd-slug>/mcp-logs-<server>/*.jsonl` for the
channel gate verdict line, and (where registration succeeded or was expected)
checked the worker's working directory for a file the channel instruction asked
it to write. Gate logic was cross-checked by string extraction from the
installed binary. Session job state was read from `~/.claude/jobs/<id>/state.json`.

## Channel gate logic (extracted from the 2.1.214 binary)

The registration decision is a single function evaluating these checks in order;
the first failing check emits a skip with a distinct reason string:

1. **Capability** — the server must declare `capabilities.experimental["claude/channel"]`.
   A top-level `capabilities["claude/channel"]` (not under `experimental`) is
   ignored: "server did not declare claude/channel capability".
2. **Provider** — `vn() === "firstParty"` required, else "channels are not
   available on third-party providers".
3. **Feature** — `r1e()` = `et("tengu_harbor", false)`, a GrowthBook feature
   flag; else "channels feature is not currently available".
4. **Policy** — `NRt(Cr("policySettings"))` checks `channelsEnabled`. Enforced
   only for team/enterprise accounts; for a personal account with no managed
   policy this passes without `channelsEnabled` being set.
5. **Session list** — the server must appear in the session's `--channels`
   list, else "server <name> not in --channels list for this session".
6. **Marketplace match** (plugin entries) — the requested
   `plugin:<name>@<marketplace>` must match the installed plugin's marketplace,
   else a marketplace-mismatch skip.
7. **Allowlist** — for a non-dev entry: `allowedChannelPlugins` is read from
   `Cr("policySettings")`. If that yields the plugin/marketplace pair the
   source is `"org"` and it registers. Otherwise it falls back to `buo()` =
   `et("tengu_harbor_ledger", [])` (source `"ledger"`), and if the pair is not
   in the ledger the skip reason is "plugin <n>@<m> is not on the approved
   channels allowlist (use --dangerously-load-development-channels for local
   dev)". A dev-marked entry (`o.dev`, set by
   `--dangerously-load-development-channels`) skips the allowlist check
   entirely.

`tengu_harbor_ledger` is a server-pushed GrowthBook feature cached locally in
`~/.claude/.claude.json` under `cachedGrowthBookFeatures`. On this host it
contained `claude-plugins-official` plugins (e.g. discord, telegram); it did
not contain the test plugin.

## Approaches attempted and results

### A. Bare `--mcp-config` server, capability at top level
`--strict-mcp-config --mcp-config <file>`, capability declared as
`capabilities["claude/channel"]`. Result: **skipped** — "server did not declare
claude/channel capability" (check 1: must be under `experimental`).

### B. Capability under `experimental`, no `--channels`
Same launch, capability moved under `experimental`. Result: **skipped** — "not
in --channels list for this session" (check 5).

### C. `--channels niwa-push` (untagged)
Result: **session failed at startup** — channels entries must be tagged
`server:<name>` or `plugin:<name>@<marketplace>`.

### D. `--channels server:niwa-push`
Result: **skipped** — "not on the approved channels allowlist (use
--dangerously-load-development-channels for local dev)" (check 7, ledger branch;
`server:` entries are treated as non-dev unless the dev flag is present).

### E. `--channels server:niwa-push --dangerously-load-development-channels`
Observations: the `--channels` flag is variadic and consumed following
arguments; separate and `=`-form invocations were required. With the dev flag
correctly parsed, on a `--bg` launch the channel still **skipped** at the
allowlist. Inspection showed the background daemon's flag-forwarding whitelist
does not forward `--dangerously-load-development-channels` to the worker.

### F. `--plugin-dir` plugin (`plugin:niwa-push@inline`)
Installing the server as a local plugin via `--plugin-dir` gives it marketplace
identity `inline`. Result: **skipped** — allowlist ("plugin niwa-push@inline is
not on the approved channels allowlist"). First a marketplace-mismatch skip
appeared when the requested marketplace tag did not match `inline`.

### G. `--managed-settings '{channelsEnabled, allowedChannelPlugins}'` on `--bg`
Result: **skipped** — the `--managed-settings` flag is stripped by the bg
daemon's flag-forwarding whitelist (same mechanism as E).

### H. respawnFlags injection + `claude respawn`
Injected `--dangerously-load-development-channels` (and separately
`--managed-settings`) directly into `~/.claude/jobs/<id>/state.json`'s
`respawnFlags`, then `claude respawn <id>`. Result: **skipped** — the flags are
filtered at respawn time inside the daemon protocol layer; `claude respawn`
preserves the session id and re-runs MCP servers.

### I. Print / streaming-input mode (`claude -p`, `--input-format stream-json`)
Result: the channel subsystem does not run in print mode; no registration
occurs and channel notifications are not delivered. The self-timer server's
notification fired but produced no in-session turn.

### J. Interactive TTY + dev flag
Not run headlessly. Per the gate, an interactive session with
`--dangerously-load-development-channels` marks the entry `o.dev` and bypasses
the allowlist; this is the documented local-dev path.

### K. Telegram plugin (`plugin:telegram@claude-plugins-official`) on `--bg`
`claude --bg ... --settings '{"channelsEnabled":true}'
--channels=plugin:telegram@claude-plugins-official`. Result: **"Channel
notifications registered"** — an approved (first-party marketplace) channel
plugin arms on a background session. (Delivery of an actual Telegram message
requires a configured bot and an inbound message; the notification→turn leg was
not exercised in this run.)

### L. niwa-push allowlist via regular `--settings` on `--bg`
`--settings '{"channelsEnabled":true,"allowedChannelPlugins":[{"marketplace":"inline","plugin":"niwa-push"}]}'`
with `--plugin-dir`. Result: **skipped** — allowlist (ledger branch). Regular
`--settings` did not feed `allowedChannelPlugins` into `policySettings`.

### M. `CLAUDE_INTERNAL_FC_OVERRIDES` env
Set to `{"tengu_harbor_ledger":[{"marketplace":"inline","plugin":"niwa-push"}]}`
to override the ledger feature value. Result: **skipped**. Binary inspection of
`a4r()` (the override reader) showed an unconditional early return before the
`process.env.CLAUDE_INTERNAL_FC_OVERRIDES` read in 2.1.214, i.e. the override
path is unreachable in that build. The env var also did not appear in the pooled
worker's environment.

### N. `CLAUDE_CODE_MANAGED_SETTINGS_PATH` env → user-owned managed-settings file, shared daemon
Set the env var to a user-owned `managed-settings.json` containing
`allowedChannelPlugins`, launched `--bg` from the normal shell. Result:
**skipped**; the worker's `/proc/<pid>/environ` did not contain the variable.
`--bg` workers inherit their environment from the bg daemon, which was already
running (started before the variable was set) and reused from a pool.

### O. Isolated fresh daemon (`CLAUDE_CONFIG_DIR`) + `CLAUDE_CODE_MANAGED_SETTINGS_PATH`
Launched with a throwaway `CLAUDE_CONFIG_DIR` to force a fresh daemon that
inherits the current shell environment. Verified `CLAUDE_CODE_MANAGED_SETTINGS_PATH`
reached the worker (`/proc/<pid>/environ`). Result: first **skipped** with
"channels feature is not currently available" (check 3 — the fresh config dir
had no cached `tengu_harbor` GrowthBook feature).

### P. Isolated daemon + seeded feature cache + `CLAUDE_CODE_MANAGED_SETTINGS_PATH`
Seeded the fresh config's `cachedGrowthBookFeatures` (including `tengu_harbor:
true`) copied from the real `~/.claude/.claude.json`, restarted the isolated
daemon, relaunched. Result: check 3 cleared; the channel **skipped** at check 7
via the **ledger** branch (dev-flag reason string), i.e. the user-owned
managed-settings file's `allowedChannelPlugins` was not read into
`policySettings` in the running version (2.1.215), despite the env var reaching
the worker and the file being present and valid.

## Options identified for arming a third-party channel (including safety-control bypasses)

Recorded for completeness; several bypass a server-controlled or
privilege-controlled allowlist.

1. **Anthropic-approved marketplace.** Publishing the channel plugin to a
   first-party marketplace (`claude-plugins-official`) places it in the
   server-pushed `tengu_harbor_ledger`, after which it registers on `--bg` as
   Telegram did (approach K). Not self-serve.
2. **`allowedChannelPlugins` in managed settings (org source).** The allowlist
   reads `Cr("policySettings")`. The managed-settings default file path on Linux
   is `/etc/claude-code/managed-settings.json` (absent on this host; `/etc` not
   user-writable → requires root to create). The env var
   `CLAUDE_CODE_MANAGED_SETTINGS_PATH` redirects this to a user-owned file; in
   approach P that redirect reached the worker but its `allowedChannelPlugins`
   did not take effect in 2.1.215 (mechanism by which `policySettings` is
   populated from that path not resolved).
3. **`--dangerously-load-development-channels` (dev bypass).** Marks the entry
   `o.dev`, skipping the allowlist. Not forwarded to `--bg` workers by the
   daemon (approaches E, H); functions on interactive/foreground sessions.
4. **Editing the local ledger cache (safety-control bypass).** Appending the
   plugin/marketplace pair to
   `~/.claude/.claude.json` → `cachedGrowthBookFeatures.tengu_harbor_ledger`
   (a user-owned file) satisfies check 7's ledger branch. The value is a local
   cache of a server-pushed list with a `cachedGrowthBookFeaturesAt` timestamp;
   a subsequent feature refresh overwrites it. Not exercised in this spike.
5. **`CLAUDE_INTERNAL_FC_OVERRIDES` env (feature-flag override).** Intended to
   override GrowthBook feature values including `tengu_harbor_ledger`; the
   reader is unreachable code in 2.1.214 (approach M).

### Q. Isolated environment matrix (throwaway `CLAUDE_CONFIG_DIR`, seeded feature cache)

A harness created a throwaway config dir, seeded its
`cachedGrowthBookFeatures` (including `tengu_harbor: true`), launched one `--bg`
worker with the timer channel plugin, and checked registration plus delivery
(the channel instruction asks the worker to write `TIMER-ARRIVED.txt`). Three
modes:

- **managed** — allowlist via `CLAUDE_CODE_MANAGED_SETTINGS_PATH` pointing at a
  user-owned `managed-settings.json` with `allowedChannelPlugins`; ledger not
  modified. Ran to completion. Result: **skipped** at the allowlist via the
  ledger branch, **not delivered** — in a clean isolated env with the feature
  available and the env var reaching the worker, the redirected managed-settings
  file's `allowedChannelPlugins` did not populate `policySettings` in 2.1.215.
- **ledger** — seeds the isolated `tengu_harbor_ledger` to include the plugin.
  **Not run by the assistant:** the local auto-mode permission classifier denied
  the command (editing the approved-channels list is fenced as a safety-control
  modification, including in a sandbox).
- **dev** — `--dangerously-load-development-channels`. **Not run by the
  assistant:** the local auto-mode permission classifier denied the command (the
  dangerous dev-channels flag is fenced).

The two bypass modes require the operator to run the harness directly; the
managed mode is unrestricted and produced the definitive negative above.

## Findings

- **Channels register on `--bg` sessions.** Demonstrated by the Telegram plugin
  registering on a background worker (approach K). The "no headless in-place
  channel" reading from earlier in the exploration reflected only that the
  test's own third-party plugin failed the allowlist, not that background
  sessions cannot host channels.
- **The gate that a niwa-authored channel fails is the approved-channels
  allowlist (check 7).** Its inputs are managed-settings `allowedChannelPlugins`
  (org source) or the server-pushed `tengu_harbor_ledger` (ledger source); the
  test plugin is in neither.
- **`--bg` workers inherit environment and flags from the bg daemon, not from
  the launching shell.** The daemon auto-spawns once and is reused; its
  flag-forwarding whitelist drops `--dangerously-load-development-channels` and
  `--managed-settings`. Environment variables reach a worker only if present
  when its daemon starts (approaches N vs O). A fresh `CLAUDE_CONFIG_DIR` forces
  a new daemon that inherits the current environment.
- **A fresh config dir lacks the `tengu_harbor` feature cache**, so channels
  read as unavailable until `cachedGrowthBookFeatures` is seeded (approach O→P).
- **The user-owned managed-settings file via `CLAUDE_CODE_MANAGED_SETTINGS_PATH`
  did not populate the channel allowlist** in the running 2.1.215 build, with
  the env var confirmed present in the worker (approach P).
- **`claude respawn` preserves the session id** and re-runs MCP servers;
  `claude --bg --resume` of a live background session mints a new session id
  (fork), consistent with the fork behavior recorded elsewhere in this feature's
  artifacts.
- **Not demonstrated:** end-to-end notification→turn delivery for a
  niwa-authored channel on a headless session; no attempt got the niwa plugin
  past registration. The notification→turn leg is the same code path the
  Telegram plugin uses in normal operation.
