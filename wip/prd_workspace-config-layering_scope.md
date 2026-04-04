# /prd Scope: workspace-config-layering

## Problem Statement

Niwa workspaces backed by a GitHub repo store all configuration in one place: hooks, env vars, plugins, secrets, and personal preferences. This makes workspaces hard to share across teams because user-specific secrets and preferences are indistinguishable from shared config. Users need a personal config layer -- a user-owned GitHub repo that overlays the shared workspace config -- so that personal preferences and user-specific secrets are portable across machines without being committed to team repositories.

## Initial Scope

### In Scope

- Personal config GitHub repo: user-owned, registered once per machine
- Registration command: `niwa config set personal <repo>` (requires new `niwa config` subcommand)
- Personal config storage: `[personal_config]` section in `~/.config/niwa/config.toml`
- Schema: global personal defaults + per-workspace overrides keyed by `workspace.name`
- Sync: personal config pulled at `niwa apply` time via the same `SyncConfigDir()` mechanism as workspace config
- Merge semantics:
  - Hooks: append (personal extends workspace)
  - Env vars and settings: per-key personal wins
  - Plugins: union (personal adds to workspace plugins, does not replace)
  - Env files: append
  - Managed files: per-key personal wins; empty string removes workspace mapping
  - Marketplaces: workspace-only, personal cannot override
- Opt-out: flag at `niwa init` time (working name: `--no-personal-config`; leading candidate: `--skip-personal`)

### Out of Scope

- Machine-specific (host-local only) config -- secondary concern, different concept from user identity
- New credentials or secret storage infrastructure
- Changes to how shared workspace config works today
- Personal config override of workspace-structural fields: Sources, Groups, Content definitions

## Research Leads

1. **CLAUDE.md content layering**: Exploration found that content sources use hierarchical selection today (workspace → group → repo), not merging. Personal CLAUDE.md contributions (e.g., system prompt preferences) have no current mechanism. The PRD should define the requirement: do users need to add personal CLAUDE.md content, and if so, what does that look like from a user perspective?

2. **Opt-out persistence**: Should `--skip-personal` at init persist for future applies on that workspace instance, or is it a one-time flag? The behavior difference is significant: persistent opt-out means the user's personal config never applies to that workspace again without re-enabling; one-time means each apply would need the flag to skip. The PRD should define the expected user mental model.

3. **Registration clone timing**: When the user runs `niwa config set personal <repo>`, does niwa clone the personal config repo immediately, or lazily on first apply? Each has UX trade-offs (immediate = user sees errors at registration; lazy = errors surface unexpectedly at apply).

4. **Error behavior on personal config failure**: If personal config sync fails at apply time (network issue, repo gone), should apply abort or continue with just the workspace config? The PRD should specify user-visible behavior.

## Coverage Notes

- **Demand signal is internal**: No GitHub issues or external reporters validated the need. The feature originates from internal exploration. The PRD process should consider whether to validate externally before committing to design.
- **`niwa config` subcommand is new surface area**: No `niwa config` subcommand exists today. The PRD should scope the command fully -- not just `set personal` but any other config management users would expect alongside it.
- **Flag naming**: `--no-personal-config` is the working name; `--skip-personal` is the leading candidate. The PRD should settle on a name with rationale.

## Decisions from Exploration

- Machine-specific (host-local) config is out of scope: secondary concern, not the primary flow.
- Personal config backing store is a GitHub repo: chosen for portability; mirrors the existing workspace config model.
- Registration is once per machine in `~/.config/niwa/config.toml`: follows existing GlobalConfig pattern.
- Workspace name is the identifier for per-workspace personal overrides: portable across machines, validated, available before instance state loads.
- Plugins merge behavior: extend/union (personal plugins are added to workspace plugins, not replacing them). Chosen to avoid the npm "replace surprises users" antipattern.
- Personal config is distinct from host-level overrides: host overrides = machine identity; personal config = user identity following the person.
