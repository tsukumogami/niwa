---
status: Proposed
problem: |
  niwa distributes env vars to repos via [env] (.local.env files) and can inject
  vars into Claude Code's settings.local.json via [claude.env]. These are independent
  systems -- if a var needs to appear in both places, the value must be duplicated.
  There's no way to say "take GH_TOKEN from the env pipeline and also put it in
  settings." The resolution order across inline declarations, file-based vars,
  promote lists, and per-repo overrides needs clear, predictable semantics.
decision: |
  Add a promote list to [claude.env] that pulls named keys from the fully resolved
  env pipeline into settings.local.json. Inline key-value pairs remain for
  settings-only vars. Inline wins over promoted values for the same key. Per-repo
  promote lists extend (union), per-repo inline vars override per key.
rationale: |
  The promote + inline model matches existing patterns in the codebase where inline
  vars override file-based sources. It avoids value duplication for vars that need
  both targets, while still supporting settings-only vars that don't belong in
  .local.env. Hard errors for missing promoted keys prevent silent misconfiguration.
---

# DESIGN: Settings Env

## Status

Proposed

## Context and Problem Statement

niwa has two independent systems for distributing environment variables to repos:

1. **`[env]`** -- writes `.local.env` files. Uses convention-over-configuration:
   env files are auto-discovered from the config directory without explicit listing.
   The convention is:
   - `.niwa/env/workspace.env` -- workspace-wide vars, applied to all repos
   - `.niwa/env/repos/{repo-name}.env` -- per-repo overrides, overlaid on top

   Users can deviate from the convention by setting `[env].files` explicitly, which
   replaces the auto-discovered workspace file. Inline vars via `[env].vars` always
   overlay on top of file-based vars. Per-repo overrides (`[repos.X.env]`) append
   files and override vars per key.

2. **`[claude.env]`** -- writes the `env` block in `settings.local.json`. Currently
   supports only inline key-value pairs. Per-repo overrides win per key.

Some vars need to appear in both places. `GH_TOKEN` is the canonical example: it
goes in `.local.env` for shell access and in `settings.local.json` for Claude Code
process injection. Today this requires duplicating the literal value in both
`[env].vars` and `[claude.env]`, which means secrets appear in multiple places in
the config file and can drift.

A "promote" mechanism would let users declare which keys from the resolved env
pipeline should also appear in settings, without duplicating values. But this
introduces resolution order questions when the same key is promoted, declared
inline, and overridden at different levels of the hierarchy.

## Decision Drivers

- Users should control exactly which vars go to `.local.env`, `settings.local.json`,
  or both
- Resolution order must be predictable -- no surprises when vars appear at multiple
  levels
- Missing promoted keys must produce actionable errors
- The config format should make intent obvious when reading workspace.toml
- Consistency with existing merge semantics (inline wins over files, repo wins over
  workspace)

## Considered Options

### Decision 1: Env var resolution across promote, inline, and overrides

The central question is how `[claude.env]` should combine three sources of vars:
promoted keys (pulled from the resolved env pipeline), inline key-value pairs
(declared directly in `[claude.env]`), and per-repo overrides (at both levels).
The answer determines the TOML schema, the resolution order, and the error behavior.

#### Chosen: Promote list with inline override

`[claude.env]` accepts both a `promote` list and inline key-value pairs. The
`promote` list names keys to pull from the fully resolved env pipeline (after all
`[env]` file parsing, inline vars, and repo-level env merges). Inline vars in
`[claude.env]` are settings-only -- they appear in `settings.local.json` but not
in `.local.env`.

When the same key appears in both promote and inline, inline wins. This matches
the existing pattern where `[env].vars` overrides `[env].files`.

**TOML schema:**

```toml
# Workspace level
[claude.env]
promote = ["GH_TOKEN", "API_KEY"]   # pull from resolved [env]
EXTRA_FLAG = "claude-only-value"    # settings.local.json only

# Per-repo override
[repos.special.claude.env]
promote = ["REPO_SECRET"]           # extends workspace promote list
GH_TOKEN = "repo-specific-value"   # overrides promoted GH_TOKEN for this repo
```

**Resolution order for a given repo's settings env block:**

1. Resolve the repo's env pipeline. This follows convention-over-configuration:
   auto-discovered files (`.niwa/env/workspace.env`, `.niwa/env/repos/{name}.env`)
   are used by default. Setting `[env].files` explicitly replaces the auto-discovered
   workspace file. `[env].vars` overlays on top. Per-repo `[repos.X.env]` appends
   files and overrides vars. This produces the `.local.env` content.
2. Collect promoted keys: workspace `[claude.env].promote` union with repo
   `[repos.X.claude.env].promote`.
3. For each promoted key, look it up in the resolved env from step 1. If not found,
   emit a hard error naming the key and which promote list declared it.
4. Start with promoted key-value pairs as the base.
5. Overlay workspace `[claude.env]` inline vars (inline wins over promote).
6. Overlay repo `[repos.X.claude.env]` inline vars (repo wins over workspace).

The final result is written to the `env` block in `settings.local.json`.

**Error behavior:**

- Promoted key not in resolved env: hard error at materialize time.
  Message: `claude.env: promoted key "X" not found in resolved env vars`
- Promoted key also declared inline at the same level: not an error. Inline wins
  silently. This is intentional -- it lets users override a promoted value without
  removing it from the promote list.

**Combination scenarios:**

| Scenario | `.local.env` | `settings.local.json` env |
|----------|-------------|--------------------------|
| Key in `[env]` only | Yes | No |
| Key in `[claude.env]` inline only | No | Yes |
| Key in `[env]` + promoted | Yes | Yes (value from env) |
| Key in `[env]` + promoted + inline override | Yes | Yes (inline value) |
| Key in repo `[env]` + workspace promote | Yes (repo value) | Yes (repo env value) |
| Key in repo `[claude.env]` inline | No (unless also in env) | Yes (repo inline value) |

#### Alternatives Considered

**Promote-only (no inline vars):** Remove inline key-value support from
`[claude.env]` entirely, making it a pure promote mechanism.
Rejected because settings-only vars are a real use case -- some env vars make
sense for the Claude Code process but not the shell environment (e.g., feature
flags, debug toggles). Forcing these through the `[env]` pipeline just to promote
them back out adds unnecessary indirection.

**Promote with fallback (promoted wins over inline):** Same schema but promoted
values take precedence over inline at the same level. Inline acts as a default when
the key isn't in the env pipeline.
Rejected because it's counter-intuitive. If a user writes `GH_TOKEN = "override"`
inline, they expect it to win. Every other layering in niwa follows "more specific
wins" -- inline over files, repo over workspace. Making promote win breaks that
pattern.

## Decision Outcome

The settings env block is built from two sources: promoted keys pulled from the
resolved env pipeline, and inline key-value declarations. Promote gives users a
way to say "this env var should also be in settings" without duplicating the value.
Inline gives users a way to inject settings-only vars or override promoted values.

The resolution is layered: env pipeline resolves first (producing `.local.env`
content), promoted keys are looked up from that result, then inline vars overlay
on top. Per-repo overrides extend promote lists (union) and override inline vars
(per key). This matches how every other config section in niwa merges.

Missing promoted keys are hard errors with actionable messages. A promoted key
that's also declared inline at the same level isn't an error -- the inline value
wins silently. This lets users override without editing the promote list, which
is useful when a repo needs a different value than what the env pipeline provides.

## Solution Architecture

### Overview

The settings env feature extends three existing components: config parsing
(`config.go`), override merging (`override.go`), and the settings materializer
(`materialize.go`).

### Components

**`ClaudeConfig.Env`** -- already exists as `map[string]string` for inline vars.
Needs a `Promote` field (`[]string`) added alongside it.

**`MergeOverrides`** -- already merges `claude.env` inline vars with repo-wins
semantics. Needs to merge `promote` lists with union semantics (repo extends
workspace).

**`SettingsMaterializer`** -- already emits the `env` block from inline vars.
Needs to resolve promoted keys from the `MaterializeContext`'s env pipeline output,
apply the resolution order, and validate that all promoted keys exist.

### Key Interfaces

The `MaterializeContext` already carries `DiscoveredEnv` and `Effective.Env`. The
settings materializer needs access to the **resolved** env vars (the merged output
that the env materializer writes to `.local.env`). This means either:

- The env materializer runs first and exposes its resolved vars on the context, or
- A shared resolution function produces the merged env vars that both materializers
  consume

The pipeline already runs materializers in order (hooks, settings, env). The
settings materializer needs the resolved env, so either the order changes (env
before settings) or a shared resolver is extracted.

### Data Flow

The env pipeline resolves vars from multiple sources in a defined order. The
promote mechanism taps into the pipeline's output without affecting it.

```
Auto-discovery                     workspace.toml
    |                                   |
    v                                   v
.niwa/env/workspace.env ----+    [env].files (if set,
.niwa/env/repos/{name}.env  |     replaces auto-discovered
                            |     workspace file)
                            v          |
                       file-based -----+
                       vars            |
                            |          v
                            +---> [env].vars (inline overlay)
                                       |
                                       v
                               [repos.X.env].files (append)
                               [repos.X.env].vars (override per key)
                                       |
                                       v
                               auto-discovered repo file overlay
                                       |
                                       v
                              resolved env vars ──> .local.env
                                       |
                                       v
                              promote lookup (by key name)
                                       |
                                       v
                        [claude.env] inline overlay ──> settings.local.json
                              (workspace, then repo)        env block
```

## Implementation Approach

### Phase 1: Add promote to config and merge

- Add `Promote []string` to `ClaudeConfig`
- Update `MergeOverrides` to union promote lists
- Update `Parse` test fixtures
- Update scaffold template

### Phase 2: Extract env resolution

- Extract a shared `ResolveEnvVars` function from `EnvMaterializer` that returns
  the merged key-value map without writing the file
- `EnvMaterializer.Materialize` calls `ResolveEnvVars` then writes
- `SettingsMaterializer` calls `ResolveEnvVars` to look up promoted keys

### Phase 3: Implement promote in settings materializer

- Look up each promoted key in the resolved env vars
- Hard error if any promoted key is missing
- Layer: promoted base, workspace inline overlay, repo inline overlay
- Update existing tests, add promote-specific tests

## Security Considerations

This design handles environment variables that frequently contain secrets (API
keys, tokens). The security posture doesn't change from the current implementation:
secrets already live in `.local.env` files and workspace.toml within the private
config repo. The promote mechanism doesn't introduce new storage locations -- it
routes existing values to an additional destination (`settings.local.json`).

Both `.local.env` and `settings.local.json` are gitignored in target repos via
`*.local*` patterns, so promoted secrets don't leak into public repo histories.
The config repo itself must remain private if it contains secrets.

## Consequences

### Positive

- Users can put a secret in one place (`[env]`) and route it to both `.local.env`
  and `settings.local.json` via promote
- Settings-only vars remain possible via inline `[claude.env]` declarations
- Resolution order is consistent with existing niwa patterns (inline > files,
  repo > workspace)

### Negative

- Promote adds a level of indirection -- reading `promote = ["GH_TOKEN"]`
  requires knowing what the env pipeline resolves to
- The settings materializer now depends on the env pipeline's output, coupling
  the two materializers

### Mitigations

- Hard errors for missing promoted keys make the indirection visible immediately
  when something breaks
- Extracting a shared `ResolveEnvVars` function keeps the coupling clean -- both
  materializers depend on the resolver, not on each other
