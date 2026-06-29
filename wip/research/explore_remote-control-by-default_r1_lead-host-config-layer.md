# Lead: niwa global override layer -- shape, merge, and override precedence

## Findings

### 1. Two distinct "global" structures (do not conflate them)

There are two top-level structures, parsed from two different files:

- **`GlobalConfig`** (`internal/config/registry.go:14`) -- parsed from `~/.config/niwa/config.toml` (path via `GlobalConfigPath()`, `registry.go:144`, respects `XDG_CONFIG_HOME`). This is the *machine-local* config. Fields:
  - `Global GlobalSettings` (toml `[global]`)
  - `GlobalConfig GlobalConfigSource` (toml `[global_config]`, holds `repo` = the registered global-config repo slug)
  - `Registry map[string]RegistryEntry` (toml `[registry]`, the workspace registry)
- **`GlobalSettings`** (`registry.go:27`) -- the `[global]` table in `config.toml`. Today it carries ONLY:
  - `CloneProtocol string` (`clone_protocol`)
  - `AutoInstallPlugins *bool` (`auto_install_plugins`)
  This is the natural home for a new *machine-local* first-class host toggle (see Implications, framing B).

- **`GlobalConfigOverride`** (`internal/config/config.go:601`) -- parsed from the *global config repo's* `niwa.toml` (filename const `GlobalConfigOverrideFile = "niwa.toml"`, `apply.go:183`). This is the "personal overlay" / host rung that carries actual Claude config. Fields:
  - `Global GlobalOverride` (toml `[global]`)
  - `Workspaces map[string]GlobalOverride` (toml `[workspaces.<name>]`)
- **`GlobalOverride`** (`config.go:583`): `Claude *ClaudeOverride` (`[global.claude]`), `Env EnvConfig`, `Files map[string]string`, `Vault *VaultRegistry`, `EnvExamplePolicy`, `EnvOutput`. Doc comment (`config.go:571-574`): *"applies on top of workspace config before per-repo overrides... omit repo-specific fields ... and Claude.Enabled."* Note: `GlobalOverride.Claude` is a `*ClaudeOverride`, which DOES include `Enabled *bool` (`config.go:53`), but the comment says global omits Claude.Enabled by convention.
- **`ClaudeOverride`** (`config.go:52`): `Enabled *bool`, `Plugins *[]string`, `Hooks HooksConfig`, `Settings SettingsConfig`, `Env ClaudeEnvConfig`. The narrow override type (no Content/Marketplaces).
- **`SettingsConfig`** (`config.go:320`): `map[string]MaybeSecret`. Doc: *"The primary key today is 'permissions' (values: 'bypass', 'ask')."* This is a CONTROLLED VOCABULARY, not a freeform passthrough -- see finding 5.
- `ParseGlobalConfigOverride` (`config.go:609`) decodes + validates path safety and vault refs.

### 2. Merge logic and the materialize pipeline

The merge of the host rung happens in `ResolveAndMergeEffectiveConfig` (`internal/workspace/effective_config.go:65`), shared by both the instance-apply path and the worktree-apply path:

1. `resolve.ResolveWorkspace` resolves the team workspace config (vault).
2. If a global override exists, `resolve.ResolveGlobalOverride` resolves the overlay's own secrets, then `ResolveGlobalOverride(resolvedOverride, cfg.Workspace.Name)` (`override.go:323`) flattens `[global]` + `[workspaces.<name>]` (workspace-specific wins per field).
3. `MergeGlobalOverride(resolvedCfg, flattened, globalConfigDir)` (`override.go:483`) applies the flattened global override **on top of** the workspace baseline. The result *replaces* `cfg` for all downstream materializers.

`MergeGlobalOverride` semantics (comment block `override.go:463-474`, code `override.go:496-655`):
- `Claude.Settings`: **global wins per key** (`override.go:521-533`).
- `Claude.Hooks`: global hooks **appended after** workspace hooks; scripts resolved to abs paths via `globalConfigDir`.
- `Claude.Env.Vars/Secrets`: global wins per key. `Promote`: union. `Plugins`: union/dedup.
- Subject to `[vault].team_only` locking: a global/personal-overlay write over a team-set key errors with `vault.ErrTeamOnlyLocked` (`override.go:522-528`).

Downstream of the global merge, two more rungs apply at materialize time:
- **Instance root**: `writeRootSettings` (`root_materializer.go:222`) calls `MergeInstanceOverrides(cfg)` (`override.go:166`), which applies `[instance.claude]` on top of the (already global-merged) `cfg.Claude` -- **instance wins per key** (`override.go:198-203`).
- **Per-repo**: `MergeOverrides` applies `[repos.<name>.claude]` last -- **repo wins per key** (settings loop `override.go` ~`MergeOverrides`).

Both rungs funnel into `buildSettingsDoc` (`materialize.go:377`), which writes `settings.json` (instance root) or `settings.local.json` (repos).

### 3. Override precedence (lowest -> highest, later wins)

```
workspace [claude]              (baseline)
  < [workspace-overlay] (private overlay, apply.go Step 0.6)
  < [global.claude] host rung   (MergeGlobalOverride: global wins over workspace)
  < [instance.claude]           (MergeInstanceOverrides: applied after global merge)
  < [repos.X.claude]            (MergeOverrides: applied last)
```

Critical consequence for "downstream off beats host on":
- The host rung (`[global.claude.settings]`) **wins over the workspace `[claude]` rung** -- the comment "applies on top of workspace config before per-repo overrides" (`config.go:571`) means workspace-level config CANNOT turn off a host-set value.
- `[instance.claude]` and `[repos.X.claude]` apply *after* the global merge, so they CAN override a host-set value to "off".
- So "downstream can turn off" holds for **instance + repo** rungs out of the box, but **NOT for the workspace rung** under a naive same-key override approach. If the feature requires the *workspace* rung to also be able to disable it, the host default must NOT be injected through `MergeGlobalOverride`'s "global wins" path -- it must be injected as a *fallback default only when no downstream rung set the key* (a default-fill, not an override).

### 4. When the global layer is consumed; cheap-load at dispatch time

- `LoadGlobalConfig()` / `LoadGlobalConfigFrom(path)` (`registry.go:158/168`) read `config.toml`; absent file returns an empty `&GlobalConfig{}` (not an error) -- a cheap, always-safe call. `GlobalConfigPath()` and `GlobalConfigDir()` (`registry.go:144/221`) are pure path helpers.
- The `GlobalConfigOverride` (`niwa.toml`) is consumed **only at apply/materialize time**: `apply.go:700-734` loads + parses it inside `Applier.Apply/Create`, and `ResolveAndMergeEffectiveConfig` merges it. It is NOT consulted by any command outside the apply pipeline.
- **Dispatch goes through the SAME apply path**: `runDispatch` (`dispatch.go:122`) -> `provisionInstanceFunc` -> `realProvisionInstance` (`instance_from_hook.go:344`), which calls `config.LoadGlobalConfig()` (`instance_from_hook.go:373`), sets `applier.GlobalConfigDir = GlobalConfigDir()` (`:375`), then `applier.Create(...)` (`:382`) runs the full materialize incl. the global-override merge. So the host rung is already loaded and merged for dispatch instances -- but it is loaded and merged **identically for every instance** (`niwa create`, hook-created ephemeral, `niwa apply`). The global override layer is NOT dispatch-scoped; it is workspace/instance-wide.
- `GlobalConfig` (config.toml) is also loaded at dispatch time (`instance_from_hook.go:373`), so a new field on `GlobalSettings` (framing B) is cheaply readable on the dispatch path.

### 5. Settings materialization is a controlled vocabulary, not a passthrough

`buildSettingsDoc` (`materialize.go:377-548`) only recognizes specific `SettingsConfig` keys:
- `"permissions"` -> `permissions.defaultMode` (via `permissionsMapping`, `materialize.go:390-399`); an unknown permissions *value* errors.
- Everything else in the doc comes from typed inputs: hooks (`InstalledHooks`), `env` (from `ResolvedEnvVars`, i.e. `[claude.env.vars]`/secrets/promote, `materialize.go:513-525`), `enabledPlugins`, `extraKnownMarketplaces`, `includeGitInstructions`, worktree/session hooks.
- **A raw `[global.claude.settings].<remoteKey> = true` would be silently dropped** -- there is no generic `for k,v := range Settings { doc[k]=v }`. Materializing any new settings.json key requires NEW code in `buildSettingsDoc`. (Framing A is therefore not "free"; it needs a code change AND it would hit every instance.)
- A Claude-Code-Remote *env var* could ride through `[global.claude.env.vars]` into the settings.json `env` block with no buildSettingsDoc change -- but again, for ALL instances.

### 6. Precedent: dispatch-only post-apply settings seam

`prewarmDeclaredPlugins` (`dispatch_plugins.go:48`) reads the *just-written* instance `settings.json` after apply and acts only on the dispatch/provision path. This is an existing pattern for "do something extra only for dispatch instances after the standard materialize," and it deliberately writes plugin enablement to `settings.local.json` (not the niwa-managed `settings.json`) to avoid the "modified outside niwa" fingerprint drift (`dispatch_plugins.go:83-91`). This is the closest existing analogue to a dispatch-only toggle injection.

## Implications

For a dispatch-only, downstream-overridable host default, the two framings map onto the machinery as follows:

- **Framing A (raw `[global.claude.settings].<remoteKey> = true`)** is a poor fit on two counts: (1) the global override merge is consumed by *every* instance's apply, so the value is NOT dispatch-scoped -- it would enable remote for `niwa create` and ephemeral hook instances too; (2) `buildSettingsDoc` would still need a new branch to actually emit the key (it is a controlled vocabulary). And because global wins over workspace, a *workspace*-level off would not beat it.

- **Framing B (a first-class host toggle read only by the dispatch path)** fits better. A new `*bool` on `GlobalSettings` in `config.toml` (e.g. alongside `AutoInstallPlugins`, `registry.go:27`) is already loaded cheaply on the dispatch path (`instance_from_hook.go:373`) and nowhere else by default. The dispatch path can then translate it into the actual remote-enable setting **only for the instance it just provisioned** -- ideally as a *default-fill* applied to the dispatch instance's `settings.json`/`settings.local.json` AFTER the standard materialize (mirroring the `prewarmDeclaredPlugins` post-apply seam), and ONLY when no downstream rung (`[instance.claude]`/`[repos.X.claude]`, and -- if you want workspace-level off to win -- `[claude]`) has already set the key. Default-fill (not override-merge) is what makes "downstream off wins" true at *every* rung, including workspace, which the `MergeGlobalOverride` "global wins" path cannot give you for the workspace rung.

- If the toggle is expressed as a host setting that "expands to the underlying setting," the expansion should land at the dispatch instance only, and should defer to any value already present in the merged effective config -- i.e. read the effective `[claude.settings]`/env after merge, and inject the remote default only if absent.

## Surprises

1. **Global wins over workspace.** The host rung is *higher* precedence than the workspace `[claude]` rung, not lower. "Downstream can turn off" is only automatically true for instance + repo rungs; the workspace rung cannot override a global-set key via same-key merge. This directly constrains the "downstream off beats host on" requirement.
2. **`[claude.settings]` is not a passthrough.** Only `"permissions"` is mapped into settings.json today; a new settings key needs explicit `buildSettingsDoc` support regardless of which rung sets it.
3. **The global override is already loaded and merged on the dispatch path -- but via the shared apply path, so it is in no way dispatch-scoped.** Reusing `[global.claude...]` as-is cannot achieve dispatch-only scope without a new discriminator that only the dispatch path consults.
4. There are two separate "global" surfaces: machine-local `config.toml` (`GlobalConfig`/`GlobalSettings`) vs the cloned config repo's `niwa.toml` (`GlobalConfigOverride`/`GlobalOverride`). A "first-class toggle on GlobalSettings" and "a key under `[global.claude]`" live in *different files*.

## Open Questions

1. Is Claude Code Remote enabled via a `settings.json` key, an `env` var, or a CLI flag to `claude --bg`? That determines whether the injection point is `buildSettingsDoc`, the env block, or `buildDispatchPassthrough`/`buildClaudeBgArgs` (`dispatch_launcher.go:56`). The dispatch launcher already forwards flags as discrete argv -- a passthrough flag would be the cleanest dispatch-only vector and would avoid touching the materialize vocabulary entirely.
2. Does "downstream off" need to be honorable at the **workspace** rung specifically, or are instance/repo rungs sufficient? If workspace must win, default-fill is mandatory (override-merge won't do it).
3. Should the host default live in machine-local `config.toml` (`GlobalSettings`) or in the cloned `niwa.toml` (`GlobalOverride`)? The former is per-machine and trivially dispatch-readable; the latter is shareable across machines via the config repo but is consumed by the shared apply path.
4. Where exactly should the dispatch-only expansion run -- a post-`applier.Create` seam in `realProvisionInstance`/`runDispatch` (like `prewarmDeclaredPlugins`), or via `claude --bg` argv? The former writes to the instance settings file; the latter avoids file fingerprint concerns.

## Summary

The host rung is the cloned config repo's `niwa.toml` parsed as `GlobalConfigOverride`/`GlobalOverride`; `MergeGlobalOverride` applies it on top of workspace config with "global wins per key," BEFORE `[instance.claude]` and `[repos.X.claude]` (which therefore win), so a downstream "off" beats a host "on" only at the instance/repo rungs and NOT at the workspace rung under same-key override -- workspace-level override requires a default-fill, not a merge. The layer is consumed only in the apply/materialize pipeline, which the dispatch path runs identically to every other instance, so reusing `[global.claude.*]` raw cannot achieve dispatch-only scope (and `buildSettingsDoc` is a controlled vocabulary that drops unknown settings keys anyway); the better fit is a first-class host toggle on `GlobalSettings` (cheaply read on the dispatch path) that the dispatch path alone expands into the remote setting on the just-provisioned instance, defaulting only when no downstream rung set it. The biggest open question is the actual enable mechanism for Claude Code Remote (settings key vs env var vs `claude --bg` flag), since that picks the injection point and decides whether the materialize vocabulary must change at all.
