# Exploration Decisions: workspace-config-layering

## Round 1

- Machine-specific (host-local) config is out of scope: Secondary concern, not the main flow. The feature targets user identity (portable personal config), not machine identity.
- Personal config backing store is a GitHub repo: Chosen over other options (local-only, env vars, dotfiles-only) for portability; mirrors the existing workspace config model.
- Registration is once per machine in `~/.config/niwa/config.toml`: Follows existing GlobalConfig pattern; users shouldn't re-register per workspace.
- Workspace name is the identifier for per-workspace personal overrides: Portable across machines, validated, available before instance state loads.
- Plugins merge behavior: extend/union (not replace): Personal plugins are added to workspace plugins. Consistent with list-append pattern for hooks; avoids the npm "replace surprises users" antipattern.
- Personal config is distinct from host-level overrides: Host overrides = machine identity. Personal config = user identity following the person. Prior removal of host overrides from config distribution design doc does not affect this direction.
- Opt-out flag working name `--no-personal-config`, better name TBD: `--skip-personal` is the leading candidate (concise, consistent with `--no-pull`).
- CLAUDE.md content layering: deferred to design doc phase (no merge semantics exist today; needs new design).
- Opt-out persistence: deferred to design doc phase (whether `--skip-personal` persists in instance state is an implementation decision).
