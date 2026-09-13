# Lead: How does niwa's embedded Claude Code plugin currently install, and what gates that install today?

## Findings

**The install function** (`internal/plugin/installer.go`, `Install()`, lines 74-106) atomically stages-and-renames the embedded plugin tree to `~/.claude/plugins/marketplaces/niwa/`. It is idempotent (compares on-disk `manifest.json` version to the embedded version) and is a pure function of `(state, reporter, InstallOpts{SkipInstall bool})` -- it has no rank-awareness of its own. Rank gating happens entirely at the call sites, not inside the installer.

**The gate is "rank-2 source detected," enforced at exactly 4 call sites in `internal/workspace/apply.go`**, all nested inside `if teamConfigRank == 2 ...` / `if overlayRank == 2 ...` blocks that also emit the one-time `NoticeIDRank2TeamConfig` / `NoticeIDRank2Overlay` deprecation notices:
- Line 443-452 (`Create` path, team config source)
- Line 595-608 (`Apply` path, team config source)
- Line 927-932 (overlay sync branch, `syncErr` path)
- Line 956-961 (overlay convention-discovery branch)

Each call site is gated by `sliceContains(disclosedNotices, NoticeIDRank2TeamConfig/Overlay)` too, so it fires once per workspace (per `DisclosedNotices` bookkeeping), not on every apply. There is no rank-1 trigger anywhere in the codebase -- grep for `InstallNiwaPlugin` shows all 4 invocations occur only inside a `rank == 2` conditional.

**The seam:** `workspace.Applier` has a function-field `InstallNiwaPlugin func(state *InstanceState, reporter *Reporter, skipInstall bool)` (apply.go line 92) that stays `nil` unless wired. `internal/cli/plugin_adapter.go`'s `configurePluginAutoInstall(applier, flagOptOut)` wires it to `installNiwaPluginAdapter` (which calls `plugin.Install`) and sets `applier.SkipPluginInstall`. Every CLI surface that constructs an `Applier` must call this helper: `internal/cli/create.go:217`, `internal/cli/apply.go:159`, `internal/cli/reset.go:103` (hardcoded `false`), `internal/cli/instance_from_hook.go:389` (hardcoded `false`). `init.go` computes `skipInstall := initNoInstallPlugins || globalCfg.SkipPluginInstall()` at line 700 rather than going through the adapter helper directly.

**`--no-install-plugins` flag:** Registered independently per-command (not shared) -- `internal/cli/init.go:52`, `internal/cli/create.go:21`, `internal/cli/apply.go:31` -- each a `BoolVar` defaulting to `false`. When set (or when `~/.config/niwa/config.toml` has `auto_install_plugins = false`, checked via `GlobalConfig.SkipPluginInstall()` in `internal/config/registry.go:76-86`), `plugin.Install` short-circuits at installer.go line 82-85: no filesystem reads/writes under the install path at all, and it emits `NoticeIDPluginSkipped` (which carries the manual fallback command `niwa plugins install`).

**Manifest and skill:** `internal/plugin/files/niwa/manifest.json` declares a single skill, `migrate-config` (path `skills/migrate-config/SKILL.md`), described as "Help migrate a workspace config source from the deprecated rank-2 (whole-repo) layout to the rank-1 layout." The SKILL.md content (`internal/plugin/files/niwa/skills/migrate-config/SKILL.md`) is invoked as `/niwa:migrate-config <workspace-name>`; it only handles rank-2->rank-1 migration (in-place restructure or slug-swap paths) and explicitly states it "MUST NOT modify the workspace snapshot at `<workspace>/.niwa/`" -- i.e., it's advisory-only over `~/.config/niwa/config.toml`, not a config-editing skill.

## Implications

- The rank-2 gate is baked directly into `workspace/apply.go`'s pipeline logic at 4 duplicated call sites, not centralized in one "should we install" decision function. Adding a new trigger condition (e.g., "rank-1 source detected, offer config-editing skill") would need to touch the same 4 sites, or a refactor to centralize the rank-based dispatch.
- The `Install()` function itself is content-agnostic -- it just materializes whatever's embedded at `internal/plugin/files/niwa/`. Adding a second skill (e.g. `edit-config`) to the same manifest/plugin bundle requires no installer changes, only manifest.json + a new skill dir, PLUS wiring a new trigger condition somewhere (since the existing trigger fires only on rank-2 detection, which by definition won't happen for the target scenario -- rank-1 config sources like commuter/equity-planner).
- There IS an unconditional, independent trigger: `niwa plugins install` (`internal/cli/plugins.go`) calls `plugin.Install(nil, reporter, InstallOpts{SkipInstall: false})` directly with no rank check at all. This is the cleanest existing lever if the intent is "always available regardless of rank" -- it's a one-line CLI command, already shipped, that any user/agent could invoke manually.
- `PrewarmDeclaredPlugins` (wired via the same `configurePluginAutoInstall` helper, `internal/cli/dispatch_plugins.go`) is a separate, unconditional-on-every-apply mechanism -- but it only prewarms workspace-declared marketplaces/plugins (from `.claude/settings.json`), not niwa's own embedded plugin. It's not reusable for this purpose but demonstrates the pattern of "always run, regardless of rank" already exists in the same file for a different plugin category.

## Surprises

- Rank-1 sources trigger literally nothing plugin-related today -- confirmed by exhaustive grep of `InstallNiwaPlugin` call sites; all 4 are inside rank-2 conditionals. There's no partial/latent rank-1 trigger to build on.
- The rank-2 gate is duplicated 4 times (create team-config, apply team-config, overlay-sync, overlay-convention-discovery) rather than centralized -- any new trigger condition added alongside it would need the same 4-way duplication unless refactored first.
- `niwa plugins install` already exists as a rank-agnostic, unconditional manual install path -- this is arguably the "right lever" already sitting in the codebase, sidestepping the rank-2 gate entirely. The CLI's own help text (`internal/cli/plugins.go:20-29`) already documents this as the escape hatch for opted-out users, but nothing stops it being the primary path for a new use case.
- `--no-install-plugins` is defined three separate times (`init.go`, `create.go`, `apply.go`) as independent package-level `BoolVar`s rather than one shared flag definition -- minor duplication risk if a new command surface is added.

## Open Questions

- Should a rank-1 config-editing skill ship in the same `niwa` plugin bundle (same manifest.json, new skill dir) or a separate plugin/marketplace entry? The installer (`plugin.Install`) treats the whole `internal/plugin/files/niwa/` tree as one atomic unit keyed by manifest version, so co-locating means version-bumping affects both skills together.
- If the trigger becomes "always install regardless of rank" (mirroring `PrewarmDeclaredPlugins`'s unconditional-per-apply pattern), does that change user-facing notice behavior (currently `NoticeIDPluginInstalled`/`NoticeIDPluginSkipped` fire only inside the rank-2-once-per-workspace block)? Need to check `internal/workspace/disclosure.go` and notice IDs for whether an unconditional install path already emits notices cleanly outside that context.
- Does `niwa source inspect --json` (referenced in migrate-config's SKILL.md step 1) expose rank-1 detection cleanly enough for a hypothetical new skill to self-determine "am I looking at a rank-1 config" without needing the CLI to gate its install?

## Summary
The embedded niwa plugin's auto-install is triggered exclusively by rank-2 (deprecated whole-repo) source detection, gated at 4 duplicated call sites in `internal/workspace/apply.go` (lines 443, 595, 927, 956), all inside rank-2-notice-adjacent conditionals wired via `internal/cli/plugin_adapter.go`'s `configurePluginAutoInstall`. `--no-install-plugins` (defined separately in `init.go`, `create.go`, `apply.go`) and `auto_install_plugins = false` in global config both short-circuit `plugin.Install` (`internal/plugin/installer.go:82-85`) before any filesystem write. Rank-1 sources trigger nothing today, but `niwa plugins install` (`internal/cli/plugins.go`) already provides a rank-agnostic, unconditional manual-install path that sidesteps the gate entirely and is likely the cleanest existing lever for shipping a rank-1 config-editing skill.
