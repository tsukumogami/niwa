# Exploration Findings: dispatch-handle-retask

## Round 1 — prior art and platform research

- #209 keep-alive keeps the remote-control bridge to claude.ai warm. It solves
  reachability for HUMAN clients (web/mobile/VS Code). It provides no
  programmatic push path for a headless coordinator.
- `claude --resume <id>` reuses the session id by default (`--fork-session` is
  the explicit fork flag), but a session owned by a live `--bg` worker cannot be
  resumed in place — the resume forks. This fully explains the cs-workspace
  incident; there was no correct invocation available.
- niwa watch ED2 (branch `worktree-ed2-pr-hardening`, issue #211) independently
  hit the same platform behavior: `claude --bg --resume` mints a new id and the
  stopped session's job entry lingers, making re-capture ambiguous
  (once-per-session continuation). Watch's `continueReview` is existing niwa
  prior art for a fork-tolerant retask; #211 is the disambiguation fix it needs.
  Watch stops the session first and STILL gets a new id — stop-then-resume does
  not avoid the fork for bg sessions.
- The Telegram plugin demonstrates the channel mechanism: an MCP stdio child of
  the session declares a `claude/channel` capability and pushes
  `notifications/claude/channel` up its own stdio pipe; Claude Code converts it
  into a user turn in the SAME session, waking it if idle.

## Round 2 — verification spikes (live, on claude 2.1.214)

Built a ~100-line dependency-free MCP channel server (spool-directory watcher)
and drove it through Claude Code's undocumented channel gate chain. Findings,
each verified against a live session:

1. The client checks `capabilities.experimental["claude/channel"]` — the
   capability must be declared under `experimental`, not top-level.
2. Channels must be opted in per session via the hidden variadic flag
   `--channels`, entries tagged `server:<name>` (manually configured MCP
   server) or `plugin:<name>@<marketplace>` (allowlist enforced).
3. `server:` entries additionally require
   `--dangerously-load-development-channels` (an alias of `--channels` that
   marks entries dev). Untagged/misplaced entries fail session startup.
4. **Blocker:** the background daemon's flag-forwarding whitelist forwards
   `--channels` but silently DROPS `--dangerously-load-development-channels` —
   on both the `--bg` launch path and the `claude respawn` path (verified via
   respawnFlags injection: the respawned process still hit the allowlist skip).
   So a third-party dev channel can never arm on a headless bg worker.
5. Print mode (`claude -p`, including streaming input) does not run the channel
   subsystem at all — no registration, notifications silently void.
6. `claude respawn <id>` preserves the session id and re-runs MCP servers
   (verified). The spool design self-heals through respawn IF the channel can
   register.
7. A managed-settings key `allowedChannelPlugins` (array of
   {marketplace, plugin}) exists — a managed-org allowlist of channel plugins.
   This is the likely production-grade unlock: package the niwa channel as a
   marketplace plugin and allowlist it via managed settings. NOT yet tested.

## Round 3 — sudo-free allowlist attempts (all blocked)

- `--plugin-dir` plugins get marketplace identity `inline`; the channels entry
  `plugin:niwa-push@inline` is recognized (list-match passes) but hits the
  approved-channels allowlist.
- A `--managed-settings <json>` CLI flag exists (no root needed, unlike the
  documented /etc path), with `channelsEnabled` and `allowedChannelPlugins`
  keys — but the bg daemon's flag-forwarding whitelist strips
  `--managed-settings` exactly as it strips the dev-channels flag.
- respawnFlags injection of `--managed-settings` also failed: the whitelist is
  applied at respawn/exec time inside the daemon protocol layer (workers run in
  pooled `bg-pty-host` processes; session flags travel over the daemon socket,
  not argv), so injected flags never reach the session.
- Conclusion: on 2.1.214 every path to arming a third-party channel on a
  HEADLESS session is closed. The gates are consistent and deliberate:
  approved plugin channels only, dev servers interactive-only.

## Verdict on the core question

Real capability gap, and it sits in the platform's trust policy, not in niwa's
bookkeeping and not in the mechanism. The channel mechanism is exactly the
right shape for `niwa retask <handle>` (in-place, same session id, headless,
queue-on-busy, spool+respawn self-healing), and every niwa-side piece is
buildable today — but Claude Code 2.1.214 only arms channels for
Anthropic-approved plugin channels on bg sessions. Third-party channels are
fenced to interactive dev use.

## Fix shapes surfaced (not yet chosen)

- A. Channel path (blocked on one unlock): niwa-push channel plugin +
  `niwa retask` spool writer. Unlock candidates: (1) `allowedChannelPlugins`
  managed setting with a niwa marketplace plugin — testable locally, needs
  root for /etc managed settings; (2) upstream ask to forward the dev flag to
  bg sessions or approve the plugin.
- B. Fork-tolerant `niwa retask` (buildable now, no platform dependency):
  generalize watch ED2's stop -> resume -> recapture -> rebind into a
  first-class command; needs #211's capture-newest disambiguation; session id
  changes but the niwa handle stays the stable identity, zero orphans.
- C. Upstream feature request: headless equivalent of the agents-TUI
  peek-reply (deliver a prompt to a live bg session by id) — the narrow
  missing primitive everything else works around.
