---
status: Proposed
problem: |
  niwa manages plugins and marketplaces by spawning CLI subprocess calls
  (claude plugin marketplace add, claude plugin install) during the apply
  pipeline. This is slow (N repos x M plugins calls), fragile (requires
  claude CLI on PATH), and inconsistent with the workspace root which
  already uses declarative settings.json.
decision: |
  Move enabledPlugins and extraKnownMarketplaces into the settings
  materializer output (settings.local.json for repos). Remove Step 6.9
  entirely. The repo: marketplace ref resolver maps to the directory
  source type using the resolved on-disk path. GitHub refs map to the
  github source type.
rationale: |
  Claude Code's startup reconciler auto-materializes marketplaces from
  extraKnownMarketplaces -- no CLI calls needed. The workspace root
  already uses this approach (PR #33). Extending it to repos eliminates
  subprocess overhead, removes the claude CLI dependency, and makes the
  settings file the single source of truth.
---

# DESIGN: Declarative Plugin Management

## Status

Proposed

## Context and Problem Statement

niwa's apply pipeline has two separate mechanisms for plugin management:

1. **Repos**: Step 6.9 spawns `claude plugin marketplace add` (user scope)
   and `claude plugin install --scope local` (per repo) as subprocess calls.
   This requires the `claude` CLI, is slow (N*M subprocess spawns), and
   handles failures as warnings.

2. **Instance root**: Step 4.5 writes `enabledPlugins` and
   `extraKnownMarketplaces` directly into `.claude/settings.json`. Claude
   Code's startup reconciler handles materialization. No CLI calls needed.

The instance root approach is simpler, faster, and more reliable. Repos
should use the same declarative mechanism.

## Decision Drivers

- Eliminate subprocess dependency on `claude` CLI
- Single mechanism for both instance root and repos
- `repo:` marketplace refs must resolve to absolute paths at write time
- Per-repo plugin overrides (replace semantics) must still work
- No behavior change from the user's perspective

## Considered Options

### Decision 1: Where to write plugin config for repos

How to integrate `enabledPlugins` and `extraKnownMarketplaces` into the
per-repo settings.

#### Chosen: Extend SettingsMaterializer output

The settings materializer already generates `settings.local.json` for repos
with permissions, hooks, and env. Add `enabledPlugins` and
`extraKnownMarketplaces` to the same output. This is a one-line-per-field
addition to the existing materializer.

The materializer has access to the `MaterializeContext` which carries the
effective config (already merged via `MergeOverrides`). Plugins are on
`EffectiveConfig.Plugins` and marketplaces are on
`EffectiveConfig.Claude.Marketplaces`.

**Generated settings.local.json:**

```json
{
  "permissions": { "defaultMode": "bypassPermissions" },
  "hooks": { ... },
  "env": { "GH_TOKEN": "..." },
  "enabledPlugins": {
    "shirabe@shirabe": true,
    "tsukumogami@tsukumogami": true
  },
  "extraKnownMarketplaces": {
    "shirabe": {
      "source": { "source": "github", "repo": "tsukumogami/shirabe" },
      "autoUpdate": true
    },
    "tsukumogami": {
      "source": { "source": "directory", "path": "/abs/path/to/tools" },
      "autoUpdate": true
    }
  }
}
```

#### Alternatives Considered

**Separate PluginMaterializer**: Create a new materializer for plugin config.
Rejected because the output goes into the same file (`settings.local.json`).
Two materializers writing to the same file would need merge logic or ordering
guarantees. Simpler to extend the existing settings materializer.

### Decision 2: Mapping marketplace sources to extraKnownMarketplaces

How to convert niwa's marketplace source strings to the
`extraKnownMarketplaces` format.

#### Chosen: Map at settings-write time using repoIndex

Marketplace sources in niwa config are strings: `"tsukumogami/shirabe"`
(GitHub ref) or `"repo:tools/.claude-plugin/marketplace.json"` (managed
repo ref). These map to `extraKnownMarketplaces` entries:

| niwa source | extraKnownMarketplaces source type |
|-------------|-------------------------------------|
| `org/repo` | `{source: "github", repo: "org/repo"}` |
| `repo:name/path` | `{source: "directory", path: "/abs/path/to/name"}` |

For `repo:` refs, the `repoIndex` (built from classified repos after
cloning) provides the absolute on-disk path. The `path` field in the
directory source points to the directory containing
`.claude-plugin/marketplace.json`, not the JSON file itself. The resolver
strips the filename from the ref path.

The `mapMarketplaceSource` function (already implemented for the workspace
root in `workspace_context.go`) handles the mapping. For repos, the same
function is reused but with access to the `repoIndex` for `repo:` resolution.

#### Alternatives Considered

**Keep CLI calls for repo: refs only**: Use declarative for GitHub sources
but fall back to CLI for repo: refs since they need path resolution.
Rejected because it keeps the subprocess dependency for a common case
(the tsukumogami workspace uses repo: refs for the tools marketplace).

### Decision 3: Removing Step 6.9

What to do with the existing CLI-based plugin pipeline step.

#### Chosen: Remove entirely

Step 6.9 (RegisterMarketplaces + InstallPlugins) is removed from the
pipeline. All plugin/marketplace config is written by the settings
materializer in Step 6.5. The functions in `plugin.go`
(RegisterMarketplaces, InstallPlugins, FindClaude) become unused and are
deleted.

The `repo:` resolver (`ResolveMarketplaceSource`) is retained since
the settings materializer needs it for mapping `repo:` refs to absolute
paths.

## Decision Outcome

The settings materializer writes `enabledPlugins` and
`extraKnownMarketplaces` alongside permissions, hooks, and env in
`settings.local.json`. Step 6.9 is removed. The repoIndex (already
available in the pipeline) is threaded into the materializer context
for `repo:` ref resolution.

Both repos and the instance root use the same declarative approach.
Claude Code handles marketplace materialization and plugin activation
on startup.

## Solution Architecture

### Overview

Extend `SettingsMaterializer` and `MaterializeContext`, remove Step 6.9,
delete `plugin.go` CLI functions.

### Components

**`MaterializeContext`**: add `RepoIndex map[string]string` field so the
settings materializer can resolve `repo:` marketplace refs.

**`SettingsMaterializer.Materialize`**: add `enabledPlugins` and
`extraKnownMarketplaces` blocks to the generated JSON. Reuse
`mapMarketplaceSource` (moved from workspace_context.go to a shared
location or materialize.go).

**Step 6.5** (materializer loop): already runs for each repo. The
settings materializer will now emit plugin config as part of its output.

**Step 6.9**: removed. Pipeline goes from Step 6.75 (setup scripts)
directly to Step 7 (managed files).

### Data Flow

```
workspace.toml [claude].plugins + [claude].marketplaces
    |
    v
MergeOverrides per repo
    |
    v
SettingsMaterializer (Step 6.5)
    |
    +-- permissions, hooks, env (existing)
    +-- enabledPlugins (new)
    +-- extraKnownMarketplaces (new, using repoIndex for repo: refs)
    |
    v
settings.local.json per repo
```

## Implementation Approach

### Phase 1: Add repoIndex to MaterializeContext and map marketplaces

- Add `RepoIndex map[string]string` to `MaterializeContext`
- Move `mapMarketplaceSource` to materialize.go (or make it accept repoIndex)
- Add `mapMarketplaceSourceWithIndex` that handles `repo:` refs
- Build repoIndex before the materializer loop and pass it in

### Phase 2: Extend SettingsMaterializer

- Add `enabledPlugins` block from effective.Plugins
- Add `extraKnownMarketplaces` block from effective.Claude.Marketplaces
  using the marketplace mapper
- Tests for: plugins emitted, marketplaces mapped, repo: refs resolved

### Phase 3: Remove Step 6.9 and clean up

- Remove Step 6.9 from apply.go
- Delete RegisterMarketplaces, InstallPlugins, FindClaude from plugin.go
- Keep ResolveMarketplaceSource (used by marketplace mapper)
- Update plugin_test.go (remove CLI-dependent tests, keep resolver tests)

## Security Considerations

No change in security posture. The same marketplace sources and plugin IDs
are written to `settings.local.json` instead of passed as CLI arguments.
The `settings.local.json` file is already gitignored via `*.local*` patterns.
`repo:` ref resolution still uses `checkContainment` for path traversal
prevention.

## Consequences

### Positive

- No dependency on `claude` CLI being installed
- Faster: no subprocess overhead (was N*M CLI calls)
- Consistent: same mechanism for repos and instance root
- Simpler: settings file is the single source of truth

### Negative

- Marketplace materialization happens lazily on first Claude Code startup,
  not eagerly during `niwa apply`. First session may be slower.
- If a marketplace source is unreachable, the error surfaces in Claude Code
  startup, not during niwa apply.

### Mitigations

- `autoUpdate: true` ensures subsequent sessions stay fresh
- Claude Code's reconciler is designed for this pattern and handles errors
  gracefully
