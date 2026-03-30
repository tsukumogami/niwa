---
status: Proposed
---

# PRD: Project-Scoped Plugin Installation

## Problem

niwa manages per-repo Claude Code configuration -- hooks, settings, env, files --
but can't install plugins. Plugin installation requires running `claude plugin
install` manually or through an external script for every repo in the workspace.
For a workspace with 6 repos and 2 plugins, that's 12 manual commands after every
fresh clone.

Plugins are a core part of the Claude Code experience. A workspace that distributes
hooks and settings but not plugins is incomplete -- the developer still needs a
separate setup step to get the full tooling.

## Scope Principle

niwa installs at project scope when available. When a dependency only supports user
scope (like marketplace registration), niwa manages it there as the minimum viable
scope. niwa never touches system-level configuration.

| Resource | Preferred Scope | Rationale |
|----------|----------------|-----------|
| Plugin installation | Project | Config lives with the project, no global side effects |
| Marketplace registration | User | Project scope not available; user scope is the minimum viable |

## User Stories

### US1: Declare plugins in workspace.toml

As a workspace author, I want to declare which plugins each repo needs so that
`niwa apply` installs them automatically.

```toml
[claude]
plugins = ["shirabe@shirabe", "tsukumogami@tsukumogami"]
```

After `niwa apply`, every repo in the workspace has both plugins installed at
project scope. Repos that already have the plugin installed are unaffected
(idempotent).

### US2: Declare marketplaces in workspace.toml

As a workspace author, I want to declare which marketplaces are needed so that
`niwa apply` registers them before installing plugins.

```toml
[claude]
marketplaces = ["tsukumogami/shirabe"]
plugins = ["shirabe@shirabe"]
```

If the `shirabe` marketplace isn't registered on this machine, niwa runs
`claude plugin marketplace add "tsukumogami/shirabe" --scope user` before
attempting plugin installs. If already registered, it's a no-op.

### US3: Marketplace from the config repo itself

As a workspace author, I want to bundle a marketplace inside my `.niwa` config
repo so that niwa registers it from the local path.

```toml
[claude]
marketplaces = [
    "tsukumogami/shirabe",         # GitHub source
    ".claude-plugin/marketplace.json"  # relative to .niwa config dir
]
```

Relative paths are resolved against the config directory. This avoids
machine-specific absolute paths and lets the marketplace travel with the
config repo.

### US4: Per-repo plugin override

As a workspace author, I want to override the plugin list for specific repos.

```toml
[claude]
plugins = ["shirabe@shirabe", "tsukumogami@tsukumogami"]

[repos.shirabe.claude]
plugins = []  # shirabe discovers its own plugin automatically
```

An empty list disables plugin installation for that repo. A non-empty list
replaces the workspace default entirely (same override semantics as settings).

### US5: Missing `claude` CLI

As a developer, I want a clear warning when `claude` isn't installed, rather
than a cryptic error or silent failure.

```
warning: claude CLI not found, skipping plugin installation
  Install Claude Code to enable plugin management: https://docs.anthropic.com/claude-code
```

Plugin installation is skipped entirely. The rest of `niwa apply` continues
normally. This matches the non-fatal approach used by setup scripts.

### US6: Marketplace update on apply

As a workspace author, I want niwa to refresh marketplace caches on apply so
that newly published plugin versions are available.

```toml
[claude]
marketplaces = ["tsukumogami/shirabe"]
```

After registering (or verifying registration), niwa runs `claude plugin
marketplace update <name>` to pull the latest plugin catalog. This ensures
that `claude plugin install` sees the latest versions without manual
intervention.

## Non-Goals

- **User-scoped plugin management.** Plugins installed globally (`--scope user`)
  are the user's responsibility. niwa only manages project-scoped installs.
- **Plugin version pinning.** niwa installs the latest available version from
  the marketplace. Version pinning is a future concern.
- **Plugin configuration.** Plugins may have their own config files; niwa
  distributes those via `[files]`, not through the plugin system.
- **Plugin uninstallation.** Removing a plugin from workspace.toml doesn't
  uninstall it from repos. This avoids destructive surprises.

## Requirements

### Functional

| ID | Requirement | Story |
|----|------------|-------|
| R1 | `[claude].plugins` declares a list of plugins to install at project scope | US1 |
| R2 | `[claude].marketplaces` declares a list of marketplace sources to register at user scope | US2 |
| R3 | Marketplace sources can be GitHub refs (`org/repo`) or relative paths (resolved against config dir) | US3 |
| R4 | Per-repo `[repos.X.claude].plugins` overrides the workspace plugin list (empty list disables) | US4 |
| R5 | Missing `claude` CLI produces a warning, not a fatal error | US5 |
| R6 | Marketplace registration is idempotent (no-op if already registered) | US2 |
| R7 | Plugin installation is idempotent (no-op if already installed at project scope) | US1 |
| R8 | Marketplace caches are refreshed on each apply | US6 |

### Execution Order

Marketplaces must be registered before plugins can be installed. Within the apply
pipeline, the sequence is:

1. Register/update marketplaces (user scope, once per apply)
2. Install plugins per repo (project scope, per repo)

This runs after all materializers and setup scripts (Step 6.75), as a new
Step 6.9 in the pipeline.

### Error Handling

| Scenario | Behavior |
|----------|----------|
| `claude` not on PATH | Warn, skip all plugin operations |
| Marketplace registration fails | Warn with marketplace name, continue |
| Plugin install fails | Warn with plugin name and repo, continue with next plugin/repo |
| Unknown marketplace in plugin ref | `claude plugin install` handles this -- its error message is sufficient |

## Examples

### Minimal: one marketplace, one plugin

```toml
[workspace]
name = "my-project"

[claude]
marketplaces = ["my-org/my-plugins"]
plugins = ["my-tool@my-plugins"]
```

### tsukumogami workspace

```toml
[workspace]
name = "tsukumogami"

[claude]
marketplaces = [
    "tsukumogami/shirabe",
    ".claude-plugin/marketplace.json",
]
plugins = ["shirabe@shirabe", "tsukumogami@tsukumogami"]

[repos.shirabe.claude]
plugins = []  # uses its own plugin via auto-discovery
```

### No plugins needed

```toml
[workspace]
name = "simple-workspace"

# No [claude] section or no plugins key -- nothing happens
```
