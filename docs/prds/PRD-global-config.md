---
status: Draft
problem: |
  niwa workspace config is a team artifact stored in a shared GitHub repo. This
  makes it impossible to separate user preferences and user-specific credentials
  from shared configuration. Users must either commit personal settings to the
  team repo (polluting shared config) or maintain entirely separate workspaces
  (losing the shared structure). There is no way to have user-specific config
  that is portable across machines without being visible to teammates.
goals: |
  A developer can maintain a global config repo, registered once per machine,
  that overlays any niwa workspace they join. Global hooks, env vars, plugins,
  and Claude instructions apply automatically on every niwa apply without
  appearing in the shared workspace config. Workspace-specific global overrides
  (e.g., per-workspace secrets) are scoped precisely. Teams can share workspace
  config freely without user-specific settings mixing in.
---

# PRD: Global config

## Status

Draft

## Problem statement

niwa workspaces are backed by a GitHub repo that holds all configuration: hooks, env vars, plugins, managed files, and CLAUDE.md content. Everything in that repo applies to every team member equally. This creates a conflict: user preferences and user-specific credentials belong to the individual, not the team, but there's nowhere else for them to go.

The practical result is that users either commit personal settings to the shared workspace repo (mixing personal and team config in ways that are hard to untangle), keep personal settings off niwa entirely (losing the benefit of managed distribution), or maintain a separate personal workspace that duplicates the team structure. None of these work well when a developer uses the same workspace across multiple machines.

The gap is most visible in three scenarios: a developer who wants Claude to always respond in a specific language, a developer who has workspace-specific API keys that are personal but should follow them to any machine they use, and a CI/CD operator who needs to run niwa apply without any user-specific config applied.

## Goals

1. **Portable global config.** A developer registers their global config repo once per machine. From that point on, every `niwa apply` automatically includes their preferences and credentials -- without them having to remember or re-configure anything.

2. **Clean workspace config.** Team workspace repos stay free of user-specific settings. What's in workspace.toml applies to everyone equally.

3. **Workspace-specific global overrides.** Global config supports both user-wide defaults (apply to all workspaces) and per-workspace sections (apply only when working with a named workspace). A workspace-specific API key is available wherever that workspace is configured.

4. **Global Claude instructions.** A developer's preferences about how Claude behaves (language, tone, workflow style) follow them into every workspace without modifying shared CLAUDE.md files.

5. **Opt-out at workspace creation.** Users can initialize a workspace instance without global config applied -- for automation, shared machines, or environments where user-specific settings shouldn't be present. This choice is made once at init time and persists for the lifetime of the instance.

## User stories

**US1: Developer registering global config.**
As a developer, I run `niwa config set global <repo>` on a new machine. On every subsequent `niwa apply`, my global hooks, env vars, and Claude instructions are applied automatically. I only have to register once per machine.

**US2: Developer with user-wide preferences.**
As a developer, I want Claude to always respond in English regardless of which workspace I'm in. I add a `CLAUDE.global.md` file to my global config repo. After `niwa apply`, every workspace instance has my global instructions available in the CLAUDE.md context hierarchy.

**US3: Developer with workspace-specific credentials.**
As a developer, I have an API key for a specific workspace that I don't want in the shared team config. I add it to the per-workspace section of my global config, keyed by workspace name. On any machine where I have that workspace configured, `niwa apply` includes the key in the workspace env.

**US4: Developer onboarding to a team.**
As a developer joining a team, I run `niwa init --from team-org/workspace-config`. Because I already have global config registered, it's applied alongside the team workspace config. I don't need to manually layer my settings on top.

**US5: CI/CD operator.**
As a CI/CD operator, I initialize the workspace instance with `niwa init --skip-global`. Every subsequent `niwa apply` on that instance runs without global config. No developer's personal settings bleed into automated pipelines.

## Requirements

### Functional

**R1: Global config registration.**
niwa provides a `niwa config set global <repo>` command that registers a GitHub-backed global config repo for the current machine. Registration and the local clone path are stored in `~/.config/niwa/config.toml` (XDG_CONFIG_HOME aware). The repo is cloned to `$XDG_CONFIG_HOME/niwa/global/` at registration time. Running the command when global config is already registered silently updates the registration and re-clones from the new repo URL. If the new repo is unreachable at registration time, the command fails and the previous registration is preserved.

**R2: Global config schema.**
The global config repo supports two scopes:
- User-wide scope: configuration that applies to all workspaces
- Per-workspace scope: configuration keyed by workspace name that applies only when that workspace is being applied

Both scopes support the same set of configurable fields: hooks, env vars, plugins, managed files, and Claude instructions file.

**R3: Automatic sync at apply time.**
`niwa apply` syncs the global config repo (pulls latest changes) before applying workspace config on instances that have global config enabled. Global config is always up to date on every machine when apply runs.

**R4: Hooks layering.**
Global hooks are added to workspace hooks -- they do not replace them. A hook declared in global config runs alongside workspace hooks, not instead of them. Per-workspace global hooks run only for the matching workspace.

**R5: Env vars layering.**
Global env vars take precedence over workspace env vars on a per-key basis. If global config and workspace config both declare a variable with the same name, the global value is used. Variables present only in workspace config are preserved.

**R6: Plugins layering.**
Global plugins are added to workspace plugins -- they do not replace them. Both global and workspace plugins are active after apply.

**R7: Managed files layering.**
Global managed files take precedence over workspace managed files on a per-source basis. A global config can suppress a workspace-managed file by mapping its source key to an empty string (`""`). The suppressed file is not written to the instance during apply.

**R8: Global Claude instructions.**
A file named `CLAUDE.global.md` in the global config repo root is injected into each workspace instance's CLAUDE.md context via an import directive at apply time. This file applies across all workspaces where global config is enabled. Global Claude content is additive -- it does not replace or override shared workspace CLAUDE.md content.

**R9: Marketplaces not overridable.**
Marketplace configuration is workspace-wide and cannot be overridden by global config.

**R10: Opt-out at workspace initialization.**
`niwa init` accepts a `--skip-global` flag that permanently disables global config for the initialized instance. The opt-out is stored in instance state and persists for the lifetime of the instance -- subsequent `niwa apply` calls on that instance always skip global config without requiring the flag again.

**R11: Sync failure handling.**
If the global config repo pull fails at apply time, the apply aborts and prints an error that identifies whether the failure is a network issue, an authentication failure, or a missing repo. Apply does not proceed with stale global config.

**R12: Unregistration.**
`niwa config unset global` removes global config registration from the machine-level config and deletes the local clone.

### Non-functional

**R13: Registration is machine-scoped.**
Global config registration is stored in `~/.config/niwa/config.toml` (the machine-level niwa config), not in workspace or instance config. It applies to all global-config-enabled instances on the machine. If global config is unregistered while instances exist that were initialized with it enabled, those instances skip global config on subsequent applies without error.

**R14: Global config does not affect workspace structural settings.**
Global config cannot modify workspace source discovery (GitHub org sources), group definitions, or workspace metadata. These remain workspace-only.

**R15: Allow-dirty applies to all config sources.**
The existing `--allow-dirty` flag on `niwa apply` bypasses the dirty-state check for all config sources, including global config.

## Acceptance criteria

- [ ] `niwa config set global <repo>` stores the registration in the machine-level config and clones the repo to a local path derived from the system config directory
- [ ] Running `niwa config set global <repo>` when global config is already registered updates the registration silently without prompting
- [ ] After `niwa apply`, changes committed to the global config repo on another machine are reflected on the current machine (global config was pulled)
- [ ] A hook declared in the user-wide scope of global config runs in every global-config-enabled workspace's repos after apply
- [ ] A hook declared in a per-workspace section of global config runs only when applying the named workspace
- [ ] An env var declared in global config takes precedence over the same var in workspace config
- [ ] An env var in workspace config that is not in global config is present after apply
- [ ] Plugins from both global config and workspace config are active after apply (union, not replace)
- [ ] `CLAUDE.global.md` from the global config repo root is accessible in every global-config-enabled workspace instance's CLAUDE.md context hierarchy after apply
- [ ] `niwa init --skip-global` initializes the instance without applying global config; the opt-out is stored in instance state
- [ ] Subsequent `niwa apply` calls on an instance initialized with `--skip-global` do not apply global config, without requiring the flag again
- [ ] If global config repo is unreachable at apply time, apply fails with an error message that identifies the cause
- [ ] `niwa apply --allow-dirty` bypasses the dirty-state check for both workspace config and global config
- [ ] `niwa config unset global` removes the registration and local clone; subsequent applies on enabled instances run without global config
- [ ] Per-workspace global config sections apply only to the workspace with the matching name
- [ ] Per-workspace global config sections do not apply to workspaces with a different name
- [ ] A workspace-managed file whose source key is mapped to an empty string in global config is not written to the instance after apply
- [ ] Declaring marketplace configuration in global config has no effect on the applied marketplace settings (workspace marketplace config is unchanged)
- [ ] If global config is unregistered and `niwa apply` is run on an instance that was previously initialized with global config enabled, apply completes without error using workspace config only
- [ ] `niwa apply` with a global config repo that has uncommitted local changes fails unless `--allow-dirty` is passed
- [ ] Workspace config source discovery, group definitions, and workspace metadata are unchanged by global config

## Out of scope

**Machine-specific (host-local) config.** Global config follows the user (user identity, portable via GitHub). Configuration that should differ between machines but not follow the user is a separate concept not covered here.

**New secret storage infrastructure.** Global config stores credentials the same way workspace config does -- in the global config GitHub repo. This PRD does not introduce encrypted secret storage, vaults, or alternative secret backends.

**Re-enabling global config after opt-out.** Once an instance is initialized with `--skip-global`, there is no command to re-enable global config for that instance in v1. Users who need to change this must re-initialize the workspace.

**Per-workspace global CLAUDE.md overrides.** `CLAUDE.global.md` applies across all workspaces. Workspace-specific global Claude instructions are deferred to a follow-on.

**Override of workspace structural settings.** Global config cannot change how workspace repos are discovered (GitHub org sources) or how they are grouped. These are team-owned settings.

## Known limitations

- The global config GitHub repo is user-managed. If the repo is deleted, renamed, or access is revoked, applies fail until the user re-registers or unregisters.
- Global plugins and workspace plugins are unioned without conflict detection. If the same plugin is declared in both, it may be installed twice depending on plugin installation semantics.
- `CLAUDE.global.md` applies across all workspaces. Users who want different global Claude instructions per workspace will need to wait for per-workspace global CLAUDE.md support.
- An instance initialized with `--skip-global` cannot re-enable global config without re-initialization. This is intentional for v1 simplicity but may be inconvenient if a developer sets up a machine and later registers global config.

## Decisions and trade-offs

**Naming: "global" over "personal" or "profile".**
"Global" follows the established precedent of git's `--global` flag and npm's global configuration, where developers already understand it as "my user-level settings that apply everywhere I work." "Profile" was considered (AWS, kubectl, SSH use it) but "global" has stronger precedent in the CLI tools developers use daily. "Personal" was rejected as too informal and not reflective of the portability concept.

**Plugins: union over replace.**
Global plugins are unioned with workspace plugins rather than replacing them. The alternative (replace) matches the current behavior for per-repo plugin overrides in workspace config, but creates a worse user experience: a user who adds a global plugin would accidentally disable all workspace plugins. Union is the intuitive default when users add their own tools -- they expect their tools plus the team's tools.

**Opt-out: persistent instance-level preference, not per-invocation flag.**
`--skip-global` at `niwa init` creates a permanent instance-level preference stored in instance state. An alternative was making it a per-invocation flag on both `niwa init` and `niwa apply`. That was rejected: apply re-materializes everything to disk, so skipping global config on one apply but not the next leaves inconsistent state (global artifacts from a previous apply persist, or get unexpectedly deleted). The decision to apply global config to an instance is made once at init time and is stable across all subsequent applies.

**Sync failure: abort over continue.**
A global config sync failure aborts the apply rather than continuing with workspace-only config. Continuing with stale global config is worse than a visible error: stale env vars or hooks could cause subtle, hard-to-diagnose behavior. This is consistent with workspace config sync failure, which is also fatal.

**Registration: eager clone, silent re-registration.**
`niwa config set global <repo>` clones the global config repo immediately at registration time. Lazy cloning was rejected because the sync mechanism assumes the repo already exists locally -- lazy cloning causes silent failure on the first apply. Re-registration silently updates rather than prompting, supporting scripted machine setup.

**CLAUDE.md injection: @import over content merging.**
Global Claude instructions are injected via an import directive into the workspace CLAUDE.md rather than merged into the workspace's source content files. Content merging would require new infrastructure (no merge semantics exist for content sources today) and would make shared CLAUDE.md files harder to audit. The @import pattern is already used by niwa for workspace context and provides clean separation: shared content stays unchanged; global instructions are a supplementary import.
