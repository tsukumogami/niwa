# Exploration Findings: workspace-config-layering

## Core Question

How should niwa support a personal config layer -- a user-owned GitHub repo that overlays on top of the shared team workspace config? The personal config needs to be portable (synced across machines), registered once per machine, and applied automatically unless the user opts out.

## Round 1

### Key Insights

- **Integration point is clean** (config-sync): `SyncConfigDir()` in `configsync.go` is stateless and reusable. Personal config sync slots between workspace config sync and instance apply, reusing the exact same function and error handling pattern. No changes to the apply pipeline itself are needed.
- **Workspace name as identifier** (schema): `workspace.name` from `workspace.toml` is portable, validated, and available before any instance state loads. Per-workspace personal config sections keyed by workspace name work cleanly across machines.
- **Merge model mostly follows existing patterns** (merge-semantics): Lists (hooks, env files) append; maps (settings, env vars) per-key personal wins; marketplaces are workspace-only. The existing `MergeOverrides` structure can be extended with a personal layer without inventing new merge logic.
- **Plugins: union across layers** (user decision): Personal plugins extend workspace plugins (union), not replace. This diverges from current repo-override behavior (replace) in favor of user-friendliness. The npm "replace surprises users" antipattern informed this choice.
- **Registration needs a new subcommand** (registration-ux): No `niwa config` subcommand exists today. Adding `niwa config set personal <repo>` would be the first user-facing command to write global config. New `[personal_config]` section in `~/.config/niwa/config.toml` (not extending `[global]`).
- **`--skip-personal` beats `--no-personal-config`** (registration-ux): More concise, consistent with `--no-pull` verbosity. User confirmed working name is `--no-personal-config` until a better name is settled.
- **Personal config is distinct from host-level overrides** (adversarial + user): A similar feature (host overrides) was removed from the config distribution design doc March 27, 2026. Host = machine identity; personal = user identity follows the person. The removal does not affect this direction.

### Tensions

- **CLAUDE.md content sources have no merge semantics**: Content sources use hierarchical selection today (workspace → group → repo fallback), not merging. Personal CLAUDE.md additions (e.g., "always respond in English") have no mechanism. Needs new design.
- **Plugins union diverges from repo-override behavior**: Repo-level overrides replace plugins entirely; personal config will union. This inconsistency should be documented clearly.

### Gaps

- CLAUDE.md content layering: undesigned. Deferred to design doc phase.
- Opt-out persistence: should `--skip-personal` at init persist in instance state for future applies? Deferred to design doc.
- Registration clone timing: does `niwa config set personal <repo>` clone immediately or lazily on first apply? Not decided.

### Decisions

- Plugins merge: extend/union (personal plugins added to workspace plugins)
- Personal config is distinct from host-level overrides; prior removal doesn't affect this
- Machine-specific (host-local) config is out of scope
- Backing store: GitHub repo, registered once per machine
- Identifier: workspace.name for per-workspace overrides
- Flag working name: `--no-personal-config` (better: `--skip-personal`)

### User Focus

User confirmed personal config is a distinct concept from host-level overrides (user identity vs machine identity). Plugins should union across layers for user-friendliness. The team is ready to move to design artifact production.

## Accumulated Understanding

The problem is well-understood: team workspaces mix personal preferences and user-specific secrets with shared config, making them hard to share. The solution is a two-layer model: shared workspace config (existing, GitHub-backed) + personal config layer (new, user-owned GitHub repo, synced at apply time).

The integration is technically straightforward: the existing `SyncConfigDir()` function handles sync; the existing `MergeOverrides` merge logic handles overlay. A personal layer sits between workspace defaults and per-repo overrides. Workspace name identifies which per-workspace personal sections to apply.

The new surface area is:
1. `[personal_config]` section in `~/.config/niwa/config.toml`
2. `niwa config set personal <repo>` registration command (requires new `niwa config` subcommand)
3. `--skip-personal` (or `--no-personal-config`) flag at `niwa init`
4. Personal config sync step in `niwa apply`

Two design questions remain open for the design doc: CLAUDE.md content layering, and opt-out persistence. The core architecture is clear enough to proceed to a design document.
