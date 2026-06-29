# Exploration Decisions: remote-control-by-default

## Round 1

- Session scope = `niwa dispatch` only: interactive root/instance and ephemeral
  SessionStart-hook sessions are explicitly out of scope.
- Host config placement = niwa's existing host config surface, specifically layer 1
  (`~/.config/niwa/config.toml` `[global]` → `config.GlobalSettings`), NOT the
  overlay's `[global.claude.settings]` (layer 2). Rationale: layer 2 materializes
  into every instance's settings.json and cannot be scoped to dispatch only.
- Enable mechanism = the Claude Code settings key `remoteControlAtStartup: true`,
  injected dispatch-only via `claude --settings '{...}'`. Rationale: `--remote-control`
  is interactive-only; `--bg` alone does not start the bridge; the settings key is
  the correct lever for background sessions.
- Injection seam = the dispatch-exclusive argv (`buildDispatchPassthrough` /
  `buildClaudeBgArgs`), not a post-provision settings.json write. Rationale: the
  provisioner is shared with interactive sessions; the argv is the only
  dispatch-only window, and hand-editing the managed settings.json collides with
  niwa's file-fingerprint check.
- Override model = host default, overridable downstream; preference to have niwa
  resolve the override (read effective instance settings, inject only when the key
  is unset downstream) rather than depend on claude's flag-vs-settings precedence.
- Two unknowns are claude-side and empirical (daemon `autoAddRemoteControlDaemonWorker`
  requirement; settings-source precedence for daemon workers) — accepted as
  validation/spike items, not another code-reading round.
