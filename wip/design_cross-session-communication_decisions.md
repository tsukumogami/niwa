# Design Decisions: cross-session-communication (channels ergonomics amendments)

Auto mode. Amending DESIGN-cross-session-communication.md with three new decisions.

## Decision 5: Role Derivation Model
- **Question**: How should session roles be assigned — required explicit config, auto-derived from topology, or hybrid?
- **Choice**: C — auto-derive from topology (coordinator = instance root, role = repo basename), optional override via `[channels.mesh.roles]`
- **Why**: The roles map restated what the workspace topology already expressed; auto-derivation covers 90%+ of real workspaces with zero config; override mechanism retained for non-obvious repo names.
- **Impact**: `IsEmpty()` replaced by `IsEnabled()` (presence-based, not content-based); role resolution in `niwa session register` gains fallback to repo basename.

## Decision 6: Channels Activation Model
- **Question**: Config-only, flag-only, or hybrid (config + per-invocation flag)?
- **Choice**: C — hybrid: `[channels.mesh]` config section as declarative default; `--channels`/`--no-channels` flags on `niwa create`/`niwa apply` for per-invocation override
- **Why**: Config is the right shared/team path; flags are the right personal/escape-hatch path; the two are not mutually exclusive. Priority: `--no-channels` > `--channels` > config section > user default.
- **Impact**: New bool flags on create/apply; flag result merged into runtime config before provisioning; `InstallChannelInfrastructure` remains oblivious to activation source.

## Decision 7: User-Level Channels Default
- **Question**: What mechanism lets users set channels on/off by default across all workspaces?
- **Choice**: Personal overlay for workspace-scoped default (already supported); `NIWA_CHANNELS=1` env var for global default
- **Why**: Env var is consistent with existing niwa pattern (NIWA_INSTANCE_ROOT, NIWA_SESSION_ROLE), requires no new file format, covers the global case. Personal overlay covers workspace-scoped without shared config edits.
- **Impact**: Read `NIWA_CHANNELS` at CLI parse time as pre-flag default for `--channels`.

## Cross-Validation Notes (Phase 3)
- Decisions 5 and 6 interact: when `--channels` is passed without `[channels.mesh]`, auto-derivation (Decision 5) must kick in. This is coherent — flag triggers same provisioning path as config section, just without explicit role overrides.
- Decisions 6 and 7 interact: env var `NIWA_CHANNELS=1` is effectively a persistent `--channels` default. The priority chain (explicit flag > config section > env var) must be explicitly documented and tested.
- Decision 5 and registration: `niwa session register` must know the instance root path and current CWD to compute the relative repo basename for auto-derivation. `NIWA_INSTANCE_ROOT` already provides this — no new env var needed.

## Security Assessment (Phase 5)
- Role auto-derivation (Decision 5): repo basenames used as role names go through the existing field validation in `niwa_send_message` (`[^a-zA-Z0-9._-]` pattern). No new attack surface; basenames come from workspace config, not user input at runtime.
- `NIWA_CHANNELS` env var (Decision 7): simple boolean check (`== "1"`); reject anything else silently (treat as unset). No path traversal or injection risk.
- `--channels`/`--no-channels` flags (Decision 6): boolean cobra flags, no string parsing. No new risk.
