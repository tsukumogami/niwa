# Lead: What does the workspace-ROOT scaffold emit into `.claude/settings.json` versus the per-INSTANCE materializer, and which fields can be hoisted to the root cleanly? (Issue #172, Option A)

## Findings

### 1. What `writeRootSettings` passes to `buildSettingsDoc`

`writeRootSettings` (internal/workspace/root_materializer.go:112-155) computes the
full effective config but forwards only a slice of it:

- Line 113: `effective := MergeInstanceOverrides(cfg)` — computes the *complete*
  instance-effective config, which **does** include `effective.Plugins` and
  `effective.Claude.Marketplaces` (see `MergeInstanceOverrides`,
  override.go:151-168, which clones `ws.Claude.Marketplaces` into
  `result.Claude.Marketplaces` and `ws.Claude.Plugins` into `result.Plugins`).
- Lines 117-130: the `BuildSettingsConfig` it actually constructs passes only:
  - `Settings: effective.Claude.Settings` (root_materializer.go:121)
  - `BaseDir`, `IncludeGitInstructions`, `UseAbsolutePaths`, `Reports`
  - `SessionHooks` (the SessionStart/SessionEnd ephemeral hook injection)

It does **NOT** pass `Plugins`, `Marketplaces`, `InstalledHooks`,
`ResolvedEnvVars`, or `RepoIndex`. So `effective.Plugins` and
`effective.Claude.Marketplaces` are computed at line 113 and then silently
dropped — the issue's claim is confirmed exactly. The struct fields exist
(materialize.go:328-329) but are left at their zero value for the root doc.

### 2. What the instance path (`SettingsMaterializer.Materialize`) passes

`SettingsMaterializer.Materialize` (materialize.go:644-712) reads all four
surfaces off the effective config and forwards them:

- Lines 645-648: `settings`, `hooks` (`ctx.InstalledHooks`),
  `plugins = ctx.Effective.Plugins`, `marketplaces = ctx.Effective.Claude.Marketplaces`.
- Lines 651-654: resolves env via `resolveClaudeEnvVars(ctx)`.
- Lines 666-677: the `BuildSettingsConfig` passes **all** of them:
  `Settings`, `InstalledHooks`, `ResolvedEnvVars`, `Plugins`, `Marketplaces`,
  `RepoIndex: ctx.RepoIndex`, plus `WorktreeDelegation`.

So the instance path passes Plugins and Marketplaces (confirmed), AND env, AND
installed hooks, AND a RepoIndex — none of which the root path passes.

### 3. `buildSettingsDoc` emission conditions

`buildSettingsDoc` (materialize.go:377-558) is a single shared helper. It is
purely field-driven: each block is emitted only if its input field is non-empty.

- `enabledPlugins`: emitted only `if len(cfg.Plugins) > 0` (lines 528-534). Each
  plugin becomes `{plugin: true}`.
- `extraKnownMarketplaces`: emitted only `if len(cfg.Marketplaces) > 0`
  (lines 537-555). Each source is mapped via `mapMarketplaceSourceWithIndex`.
- `env`: emitted only `if len(cfg.ResolvedEnvVars) > 0` (lines 509-520).
- `hooks`: built from `cfg.InstalledHooks` (lines 418-463), plus the
  WorktreeDelegation and SessionHooks injections.
- `permissions`: built from `cfg.Settings["permissions"]` (lines 389-415).

Confirmed: the *same* helper produces plugin-bearing output for instances and
plugin-less output for the root purely because the root caller passes empty
`Plugins`/`Marketplaces`/`InstalledHooks`/`ResolvedEnvVars`. There is no
root-specific branch inside `buildSettingsDoc` that suppresses plugins; the
difference is entirely in what the two callers pass.

### 4. Marketplace source classification (the key #172 Option A caveat)

A marketplace is declared by `MarketplaceConfig.Source` (config.go:67-71), a
string with two kinds, distinguished by the `repo:` prefix
(`repoRefPrefix = "repo:"`, plugin.go:10) inside `mapMarketplaceSourceWithIndex`
(workspace_context.go:452-493):

**Kind A — github `owner/repo` (ROOT-STABLE).** Example: `source = "anthropics/claude-code"`.
Matched at workspace_context.go:478-490. Emits
`{source: {source: "github", repo: "anthropics/claude-code"}, autoUpdate: ...}`.
This is a remote reference; it needs no local on-disk path and resolves
identically whether or not any instance exists. The only local lookup is an
optional GitHub release-tag resolution (`applyGithubTrack`), which degrades
gracefully to the default branch. **Root-stable: yes.**

**Kind B — `repo:<name>/<path>` directory source (INSTANCE-RELATIVE).** Example:
`source = "repo:tools/.claude-plugin/marketplace.json"`. Matched at
workspace_context.go:458-475. It resolves to an absolute `directory` path via
`ResolveMarketplaceSource(source, repoIndex)` (plugin.go:16-50), which:
  - requires `repoIndex[repoName]` to be populated (plugin.go:30-33) — fails with
    "repo %q is not managed by this workspace" otherwise;
  - `os.Stat`s the repo dir and the manifest file (plugin.go:35-47) — fails with
    "has not been cloned" / "file not found" if the repo isn't on disk;
  - returns an absolute path *inside a cloned instance repo*.

The emitted entry embeds that absolute instance path
(`{source: {source: "directory", path: <abs path inside instance>}}`,
workspace_context.go:468-475). **There is no root-stable form**: the path only
exists once an instance has cloned the repo, and it points *into* that instance.
The root scaffold runs at `niwa init`, before any instance exists, and passes no
`RepoIndex` at all (root_materializer.go has no RepoIndex field) — so a `repo:`
marketplace cannot be materialized at the root even in principle.

This is the precise caveat for #172 Option A: github-sourced marketplaces +
their plugins hoist cleanly to the root; `repo:` (directory) marketplaces do
not, because their path is instance-relative and unresolvable before/outside an
instance.

### 5. The contributor-guide "same path" claim

docs/guides/ephemeral-session-instances.md:304-306 says: "The root materializer
... reuses the shared `buildSettingsDoc`, so the root settings ride the same
path the instance settings do." This is true only for the *plumbing* (one shared
helper) — and is the same framing as DESIGN Decision 1 (lines 96-101: "The
settings ride the same `buildSettingsDoc` path ... it is the same config landing
at a new location"). It does **not** hold for plugins/marketplaces/env/hooks as
*content*: the root caller passes none of those fields (item 1), so they never
appear in the root doc. The shared helper is a code-reuse fact, not a
content-parity guarantee. The guide sentence is misleading about content and is
worth correcting alongside any Option-A fix.

### 6. Env and PreToolUse/Stop hooks at the root

Both are also dropped by the root scaffold, for the same structural reason as
plugins/marketplaces — `writeRootSettings` passes neither `ResolvedEnvVars` nor
`InstalledHooks`:

- **env (e.g. GH_TOKEN):** the instance path resolves env via
  `resolveClaudeEnvVars` (materialize.go:579-630, called at 651) and passes
  `ResolvedEnvVars`. The root path never calls it and never sets the field, so
  `buildSettingsDoc` skips the `env` block (materialize.go:509). Root-launched
  sessions get no `env`.
- **PreToolUse / Stop hooks:** these come from `ctx.InstalledHooks`, populated by
  the separate `HooksMaterializer` (materialize.go:177-245) during the per-repo
  pipeline, then forwarded as `InstalledHooks` by the instance settings path.
  The root path passes no `InstalledHooks`, so the only hooks in the root doc are
  the injected SessionStart/SessionEnd (+ optional Worktree) entries. The
  user-configured `pre_tool_use`/`stop` hooks are absent at the root.

**Env-resolution wrinkle (the failing `niwa worktree create`):** the error
`claude.env: promoted key "GH_TOKEN" not found in resolved env vars` originates
at materialize.go:600-602 in `resolveClaudeEnvVars`. A `claude.env.promote =
["GH_TOKEN"]` entry (scaffold.go:52 ships this commented out) requires the key to
exist in the resolved env pipeline (`ResolveEnvVars`). If `GH_TOKEN` isn't in any
configured/discovered env file or inline var, promotion fails hard. This matters
for Option A: hoisting env to the root would route the same promote-resolution
through the root materializer, which currently has *no* env resolution wired and
no `DiscoveredEnv`/`ConfigDir` context — so naively forwarding `ResolvedEnvVars`
at the root would need that resolution plumbing built, and would inherit the same
fail-hard-on-missing-promote behavior. The failing worktree-create suggests the
promote path is brittle when the source key is absent; any root-env work should
decide whether a missing promoted key at the root should fail or warn.

## Implications

- Option A (hoist plugins/marketplaces into the root scaffold) is mechanically a
  matter of forwarding `effective.Plugins` and `effective.Claude.Marketplaces`
  (already computed at root_materializer.go:113) into the existing
  `BuildSettingsConfig` — `buildSettingsDoc` already knows how to emit them.
- BUT marketplaces split: github sources hoist cleanly; `repo:` directory
  sources have no root-stable path. Option A must either (a) filter `repo:`
  sources out of the root doc, (b) emit only their github counterparts, or (c)
  accept that `repo:`-backed plugins won't load at the root. A plugin whose
  marketplace is `repo:`-sourced can't be enabled at the root even if listed in
  `enabledPlugins`, since its marketplace entry can't be materialized.
- The root scaffold runs at init with no `RepoIndex`; resolving `repo:` sources
  would require deferring root marketplace materialization until after instances
  exist (i.e. into `apply`, not `init`) — a bigger change than a field forward.
- Env and PreToolUse/Stop are dropped too, so #172's "Option A" framed only
  around plugins/marketplaces is incomplete: a root-launched session still misses
  env and user hooks unless those are also forwarded. Forwarding env reopens the
  promote-resolution brittleness (item 6).

## Surprises

- `MergeInstanceOverrides(cfg)` at root_materializer.go:113 already populates
  Plugins and Marketplaces on the returned struct — the data is right there and
  computed; only the forward into `BuildSettingsConfig` is missing. The fix for
  the github/plugin half is nearly trivial (two struct fields).
- The contributor guide and the DESIGN both use the "rides the same
  `buildSettingsDoc` path" framing, which is accurate about plumbing but actively
  misleads about content parity — the root doc is *structurally* plugin-/env-/
  hook-less by omission, not by any explicit root-specific suppression.
- `repo:` marketplace resolution is genuinely impossible at init time: it
  `os.Stat`s a cloned repo path that doesn't exist yet (plugin.go:35-47). This is
  not a "didn't forward the field" bug — it's a real init-vs-apply ordering
  constraint.

## Open Questions

- Should root marketplace/plugin hoisting happen at `init` (only github sources
  resolvable) or be deferred into root-scope `apply` (where a RepoIndex could
  exist after instances are cloned)? The DESIGN's context-aware `apply`
  (Decision 1, lines 108-132) already re-converges the root — is that the natural
  home for `repo:`-source resolution?
- For a plugin whose marketplace is `repo:`-sourced, what is the desired
  root-session behavior — silently omit, warn, or error? Same question for a
  promoted env key (`GH_TOKEN`) that doesn't resolve at the root.
- Does the root scaffold need its own env-resolution plumbing
  (`ConfigDir`/`DiscoveredEnv`/`ResolveEnvVars`), or should it inherit a
  *materialized* instance env (mirroring the worktree "inherit" primitive from
  Decision 1 / niwa#168) rather than re-resolving secrets at the root?
- Is the failing `niwa worktree create` (`promoted key "GH_TOKEN" not found`) a
  pre-existing config/env gap in the test workspace, or a regression that Option
  A work would need to fix first before root env can be trusted?

## Summary
The root scaffold computes the full effective config (`MergeInstanceOverrides`, root_materializer.go:113) but `writeRootSettings` forwards only `Settings`+`SessionHooks` to the shared `buildSettingsDoc`, dropping the already-computed `Plugins`, `Marketplaces`, plus `env` and user `InstalledHooks`, which is why root-launched sessions load none of them. The github-sourced half hoists cleanly (two field forwards), but `repo:<name>/<path>` directory marketplaces have no root-stable path — they `os.Stat` a cloned-instance path that doesn't exist at init and the root passes no `RepoIndex` — so Option A must filter or defer them. The biggest open question is whether root plugin/marketplace/env hoisting belongs at `init` (only github sources resolvable) or in context-aware root-scope `apply` (where instances are cloned and a RepoIndex plus a materialized-env-inherit path could exist).
