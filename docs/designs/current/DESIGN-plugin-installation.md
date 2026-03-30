---
status: Current
upstream: docs/prds/PRD-plugin-installation.md
problem: |
  niwa manages hooks, settings, env, and files per-repo but can't install Claude
  Code plugins. Plugin installation requires manual CLI commands per repo. The
  apply pipeline needs a step that registers marketplaces and installs plugins
  declared in workspace.toml.
decision: |
  Add plugins (list) and marketplaces (list) to ClaudeConfig. Plugins use
  *[]string with replace semantics (nil inherits, empty disables). Marketplaces
  are workspace-wide, not per-repo overridable. repo: prefix refs are resolved
  at pipeline time against cloned repo paths. A new Step 6.9 in the pipeline
  handles marketplace registration then plugin installation.
rationale: |
  The *[]string pointer pattern matches ClaudeConfig.Enabled for three-state
  (inherit/replace/disable). Keeping marketplaces under [claude] groups related
  config while restricting them from per-repo override avoids confusion. Pipeline
  integration after setup scripts ensures repos are cloned and ready before
  repo: refs are resolved.
---

# DESIGN: Plugin Installation

## Status

Proposed

## Context and Problem Statement

niwa's apply pipeline distributes Claude Code configuration (hooks, settings,
env, files) and runs setup scripts, but can't install plugins. Plugins require
two CLI operations: marketplace registration (user scope) and plugin installation
(project scope). For a workspace with 6 repos and 2 plugins, that's 12+ manual
commands.

The `claude plugin` CLI provides the mechanics. niwa needs to declare the desired
state in workspace.toml and drive the CLI commands during apply.

Two complications: marketplace registration is user-scoped (no project scope
available), and some marketplaces live inside managed repos (`repo:` refs) which
creates a dependency on clone ordering.

## Decision Drivers

- Project scope preferred; user scope only when project scope isn't available
- Consistent with existing `ClaudeConfig` patterns (`*bool`, `*[]string`)
- `repo:` refs must be explicit dependencies for future lazy provisioning
- Non-fatal: missing `claude` CLI or failed installs warn, don't block
- Idempotent: safe to re-run on every apply

## Considered Options

### Decision 1: Config schema for plugins and marketplaces

Where plugins and marketplaces live in the TOML schema, what Go types they use,
and how per-repo overrides work.

#### Chosen: Plugins as `*[]string` on ClaudeConfig, marketplaces as `[]string`

```go
type ClaudeConfig struct {
    Enabled      *bool           `toml:"enabled,omitempty"`
    Plugins      *[]string       `toml:"plugins,omitempty"`
    // Marketplaces is workspace-wide. Not merged from per-repo overrides.
    Marketplaces []string        `toml:"marketplaces,omitempty"`
    Hooks        HooksConfig     `toml:"hooks,omitempty"`
    Settings     SettingsConfig  `toml:"settings,omitempty"`
    Env          ClaudeEnvConfig `toml:"env,omitempty"`
}
```

**Plugins** use `*[]string` for three-state semantics:
- `nil`: not set, inherit workspace default
- `&[]string{}`: explicitly empty, disable plugin installation
- `&[]string{"a", "b"}`: replace workspace default with this list

This matches the `Enabled *bool` pattern already on `ClaudeConfig`.

**Marketplaces** use plain `[]string`. They're workspace-wide and not per-repo
overridable -- it doesn't make sense for one repo to need a different set of
marketplaces than another. They live under `[claude]` for TOML grouping (all
Claude-related config together), but `MergeOverrides` ignores them.

**TOML:**

```toml
[claude]
marketplaces = ["tsukumogami/shirabe", "repo:tools/.claude-plugin/marketplace.json"]
plugins = ["shirabe@shirabe", "tsukumogami@tsukumogami"]

[repos.shirabe.claude]
plugins = []  # disable
```

**Merge semantics:**

| Field | Workspace | Repo override | Rule |
|-------|-----------|---------------|------|
| `plugins` | `*[]string` | `*[]string` | nil = inherit; non-nil = replace entirely |
| `marketplaces` | `[]string` | (not allowed) | workspace-only |

**Post-merge:** `EffectiveConfig` gets a `Plugins []string` field (non-pointer)
rather than inheriting `*[]string` through `ClaudeConfig`. `MergeOverrides`
resolves the pointer: nil becomes workspace default, non-nil replaces. Consumers
never see the pointer. This follows the same pattern as `ClaudeEnabled()` which
wraps `Enabled *bool`.

#### Alternatives Considered

**Plugins as `[]string` (no pointer):** Can't distinguish "not set" from "empty
list." Per-repo disable would need a separate flag or sentinel value like
`plugins = ["__none__"]`. Rejected because the pointer pattern already exists
on `ClaudeConfig` and solves this cleanly.

**Marketplaces on `WorkspaceConfig` (top-level):** Would make `[marketplaces]`
a peer of `[claude]` in TOML. Technically correct (marketplaces are workspace-
wide), but scatters Claude-related config across two TOML sections. Users expect
marketplace + plugin config near each other. Rejected for ergonomics.

### Decision 2: `repo:` reference resolution

How `repo:<name>/<path>` marketplace refs are parsed, validated against the
workspace config, and resolved to absolute paths.

#### Chosen: Parse at resolution time, index repos after clone

`repo:` refs stay as plain strings in the config. During the plugin pipeline
step (Step 6.9), before marketplace registration, niwa:

1. Identifies which marketplace sources use `repo:` prefix
2. Parses: strip `repo:` prefix, split on first `/` to get repo name and path
3. Validates: repo name must be in the set of managed repos (config-derived)
4. Resolves: looks up the repo's on-disk path from a name-to-path index built
   after the clone step, then joins with the file path
5. Verifies: the resolved absolute path must exist on disk

```go
// ResolveMarketplaceSource resolves a marketplace source string to a path
// or passthrough value suitable for `claude plugin marketplace add`.
func ResolveMarketplaceSource(source string, repoIndex map[string]string) (string, error)
```

The `repoIndex` maps repo names to their absolute on-disk paths. It's built from
the classified repos after cloning (Step 3) and passed into Step 6.9.

**Note:** `repo:` ref resolution is independent of the referenced repo's
`claude.enabled` state. A marketplace manifest is just a JSON file -- it doesn't
require Claude Code to be active in the source repo. A repo with `claude = false`
can still host a marketplace that other repos consume.

**Dependency tracking:** The resolution function returns an error if the repo
isn't cloned. The pipeline ordering (clone happens in Step 3, plugins in Step
6.9) guarantees repos are available. For future lazy provisioning, a pre-scan
of `repo:` refs in marketplaces can identify which repos must be cloned eagerly.
This pre-scan is a future concern -- today all repos are cloned.

**Error severity:** `repo:` resolution errors are **config errors** (typos,
missing repos) not transient failures. They are fatal -- the pipeline stops
and reports the error. This is distinct from CLI execution failures (marketplace
registration, plugin install) which are non-fatal warnings. The distinction:
if the config is wrong, stop early with a clear message. If the config is right
but a CLI command fails, warn and continue.

**Error messages:**

| Condition | Severity | Message |
|-----------|----------|---------|
| No `/` after prefix | Fatal | `invalid repo ref "repo:tools": expected "repo:<name>/<path>"` |
| Repo not managed | Fatal | `marketplace "repo:tools/...": repo "tools" is not managed by this workspace` |
| Repo not cloned | Fatal | `marketplace "repo:tools/...": repo "tools" has not been cloned` |
| File not found | Fatal | `marketplace "repo:tools/.claude-plugin/marketplace.json": file not found at <resolved-path>` |
| Path escapes repo dir | Fatal | `marketplace "repo:tools/../../etc/passwd": path escapes repo directory` |

#### Alternatives Considered

**Parse at config load time into a struct:** Convert `repo:` refs into a
`MarketplaceSource` struct with `RepoDeps []string` during config parsing.
Rejected because it changes the config types from simple `[]string` to a
custom struct, adding complexity to parsing, serialization, and testing. The
dependency information is only needed at resolution time, not parse time.

**Use relative paths instead of `repo:` prefix:** Bare relative paths
(e.g., `../tools/.claude-plugin/marketplace.json`) resolved against the
instance root. Rejected because relative paths are ambiguous (relative to
what?) and don't make the repo dependency explicit. The `repo:` prefix is
self-documenting.

### Decision 3: Pipeline integration

Where in the apply pipeline plugin operations run, how CLI commands are invoked,
and how errors propagate.

#### Chosen: Step 6.9 with exec, non-fatal warnings

A new pipeline step runs after setup scripts (Step 6.75) and before managed
file tracking (Step 7). It has two sub-phases:

**Phase A: Marketplace registration (once per apply)**
- Check if `claude` is on PATH; if not, warn (using the PRD's message with
  install link) and skip all plugin operations
- For each marketplace source:
  - Resolve `repo:` refs to absolute paths (fatal on resolution error)
  - Run `claude plugin marketplace add <source> --scope user`
  - Non-zero exit: warn with source name, continue
- After all registrations, run `claude plugin marketplace update` (no name
  argument -- updates all registered marketplaces). This avoids needing to
  derive marketplace names from source strings.

**Phase B: Plugin installation (per repo)**
- For each classified repo with a non-empty effective plugin list:
  - For each plugin in the list:
    - Run `claude plugin install <plugin> --scope project` from the repo directory
    - Non-zero exit: warn with plugin name and repo, continue

Commands are executed via `os/exec.Command`. Stdout/stderr inherit from the
niwa process for visibility. Each command runs synchronously.

**Why after setup scripts:** Setup scripts (Step 6.75) might install the
`claude` CLI itself, or prepare repos in ways that marketplace resolution
depends on. Running plugins last ensures all prerequisites are in place.

**Why non-fatal:** Consistent with setup scripts. A workspace with 6 repos
should still get 5 repos' plugins installed even if one fails.

#### Alternatives Considered

**Use `claude plugin list --json` to check before installing:** Query installed
plugins first and skip already-installed ones. Rejected because `claude plugin
install` is already idempotent -- it's a no-op for already-installed plugins.
Adding a pre-check doubles the CLI calls for no benefit.

**Run marketplace registration per-repo:** Register marketplaces inside the
per-repo loop. Rejected because marketplaces are workspace-wide -- registering
once before the per-repo loop avoids redundant operations.

## Decision Outcome

Plugin installation adds two fields to `ClaudeConfig`: `Plugins *[]string` and
`Marketplaces []string`. Plugins use pointer semantics for three-state override
(nil/empty/list). Marketplaces are workspace-wide and not per-repo overridable.

Step 6.9 in the apply pipeline first registers marketplaces (resolving `repo:`
refs against cloned repo paths), then installs plugins per-repo at project scope.
Both operations are non-fatal -- warnings on failure, continue with remaining
items.

The `repo:` prefix makes managed-repo dependencies explicit in the config.
Today all repos are cloned before Step 6.9 runs, so resolution always succeeds.
For future lazy provisioning, a pre-scan of `repo:` refs can identify which repos
need eager cloning.

## Solution Architecture

### Overview

Three components: config types with merge logic, `repo:` reference resolver, and
the pipeline step that drives `claude` CLI commands.

### Components

**Config types** (`config.go`):
- `ClaudeConfig.Plugins *[]string` -- per-repo overridable
- `ClaudeConfig.Marketplaces []string` -- workspace-only

**Merge logic** (`override.go`):
- `MergeOverrides` resolves `*[]string` to `[]string` on `EffectiveConfig.Plugins`
  (nil = use workspace default, non-nil = replace)
- Marketplaces not merged (workspace-only)
- `EffectiveConfig.Plugins` is `[]string` (non-pointer, separate from `ClaudeConfig`)

**Reference resolver** (`plugin.go`):
- `ResolveMarketplaceSource(source string, repoIndex map[string]string) (string, error)`
- Parses `repo:` prefix, validates, resolves to absolute path

**Pipeline step** (`apply.go`):
- Step 6.9: build repo index, register marketplaces, install plugins per repo
- `claude` CLI availability check at entry

### Data Flow

```
workspace.toml
    |
    v
Parse: ClaudeConfig.Marketplaces, ClaudeConfig.Plugins
    |
    v
MergeOverrides per repo: effective plugin list (replace semantics)
    |
    v
Step 6.9:
    |
    +-- Check: claude CLI on PATH?
    |     no  -> warn, skip all
    |
    +-- Build repoIndex from classified repos
    |
    +-- Phase A: for each marketplace source:
    |     resolve repo: refs -> absolute path
    |     claude plugin marketplace add <source> --scope user
    |     claude plugin marketplace update <name>
    |
    +-- Phase B: for each repo with plugins:
          for each plugin:
            cd <repoDir> && claude plugin install <plugin> --scope project
```

## Implementation Approach

### Phase 1: Config types and merge

- Add `Plugins *[]string` and `Marketplaces []string` to `ClaudeConfig`
- Update `MergeOverrides` with replace semantics for plugins
- Update scaffold template
- Config parsing and merge tests

### Phase 2: `repo:` resolver

- Implement `ResolveMarketplaceSource` with parsing, validation, resolution
- Build repo name-to-path index from classified repos
- Tests: GitHub ref passthrough, `repo:` resolution, error cases

### Phase 3: Pipeline integration

- Step 6.9 in apply.go
- `claude` CLI detection
- Marketplace registration + update
- Per-repo plugin installation
- Tests: integration with mock CLI (or skip in tests with env guard)

## Security Considerations

Plugin installation runs `claude plugin install` which downloads and installs
code from marketplace sources. This is the same trust model as the `claude` CLI
itself -- if the user trusts their marketplace sources, they trust the plugins.

`repo:` refs are validated against the managed repo set and resolved against
known clone paths. The resolved absolute path is checked with `checkContainment`
to ensure it stays within the repo directory -- `repo:tools/../../etc/passwd`
is rejected. niwa doesn't validate marketplace manifest content -- that's the
`claude` CLI's responsibility.

Marketplace registration happens at user scope, which means it affects all
Claude Code sessions on the machine, not just the current workspace. This is
documented in the PRD's scope principle as the minimum viable scope since
project-scoped marketplace registration isn't available.

## Consequences

### Positive

- "One command" setup now includes plugins
- Declarative: plugin requirements are in version-controlled config
- `repo:` refs make managed-repo dependencies explicit and queryable

### Negative

- User-scope marketplace registration is a global side effect of a per-workspace
  operation
- Depends on `claude` CLI being installed (gracefully degraded)
- No plugin version pinning -- always installs latest

### Mitigations

- Marketplace registration is idempotent and additive (doesn't remove existing)
- Missing `claude` CLI is a clear warning, not a silent failure
- Version pinning is a documented non-goal that can be added later
