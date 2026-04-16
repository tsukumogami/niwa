---
status: Draft
problem: |
  When a niwa workspace config repo is made public, the workspace.toml exposes private information through several surfaces: source org identifiers in [[sources]], group names that imply private categories exist, [repos.*] section keys (which are repo names), [claude.content.repos.*] entries including subdirectory mappings that reveal internal code structure, and [channels.*.access] sections containing user IDs. Teams that want to publish their workspace config — to enable open contribution, share it as a reference, or reduce maintenance burden — currently have no way to keep private repo references out of the public config without maintaining a completely separate private workspace config that duplicates all the public configuration.
goals: |
  Teams can publish their niwa workspace config as a public GitHub repo without exposing private repo names, group names, source org identifiers, or operational config for private repos. Private repo configuration is kept in a separate companion repo that niwa fetches automatically when the user has permission, and ignores silently when they don't. Users without private access experience a fully functional workspace with only the public repos, with no indication that private configuration exists.
---

# PRD: Private workspace extension

## Status

Draft

## Problem Statement

When a niwa workspace config repo is made public, the workspace.toml reveals private information through several surfaces: `[[sources]]` entries expose which GitHub orgs and repos the workspace includes; `[groups.*]` definitions expose organizational taxonomy (a group named `private` tells readers the team has private repos); `[repos.*]` TOML section keys are repo names that leak identities of private repositories; `[claude.content.repos.*]` entries identify private repos and reveal their internal directory structure via subdirectory mappings; and `[channels.*.access]` sections expose user IDs.

Vault integration (enabling teams to remove secrets from workspace configs) creates the possibility of publishing workspace configs — but secrets are only one category of sensitive information. A workspace config that contains no secrets but does contain private repo names is still not safe to publish.

Teams that want the benefits of a public workspace config — enabling contributors to initialize from it, using it as documentation of team structure, sharing tooling practices — need a way to separate public-safe configuration from private configuration without abandoning niwa's single-source-of-truth model.

## Goals

1. Teams can publish their workspace config repo without exposing private repo names, group names, or operational config for private repos.
2. Users with access to the private companion repo get a complete workspace (public and private repos) from a single `niwa apply` command.
3. Users without access to the private companion (new contributors, CI/CD runners) get a fully functional workspace with public repos only — with no errors and no indication that private configuration exists.
4. Registering a private companion requires one command (`niwa config set private <repo>`), and the private companion is silently ignored when not registered.

## User Stories

**US1 — Team lead publishing workspace config**
As a team lead with both public and private repos, I want to publish the workspace config to GitHub without revealing which private repos the team uses, so that contributors can initialize from it and it can serve as documentation of the team's public tooling.

*Scenario*: A team has 8 public repos and 5 private repos, all managed by niwa. The lead splits the workspace config: `acmecorp/dot-niwa` (public) contains only the 8 public repos; `acmecorp/dot-niwa-private` (private GitHub repo) contains the 5 private repos. The public config is published. New contributors initialize from it and get 8 repos. Existing team members register the companion and get all 13.

**US2 — Contributor with full access**
As an existing contributor, I want to register a private companion once and have niwa automatically include the private repos on every `niwa apply`, so I don't have to manage separate workspace configs.

*Scenario*: A senior engineer with access to all private repos runs `niwa config set private acmecorp/dot-niwa-private` once. All subsequent `niwa apply` invocations merge the companion automatically, producing a workspace with all 13 repos.

**US3 — New contributor with public access only**
As a new contributor who hasn't yet been granted access to private repos, I want to initialize my workspace from the public config and work productively with public repos, without hitting errors or learning that private repos exist.

*Scenario*: A new hire runs `niwa init --from acmecorp/dot-niwa` and `niwa apply`. The private companion clone fails silently (no access). The workspace has 8 public repos. No error messages. When the new hire is later granted private access, the next `niwa apply` clones the companion and adds the 5 private repos automatically.

**US4 — CI/CD environment**
As a CI/CD pipeline, I want to apply the workspace config with only public repos and no private access required, so that builds are deterministic and don't fail due to missing private repo permissions.

*Scenario*: A GitHub Actions workflow calls `niwa apply --skip-private`. The private companion is not touched. The workspace has 8 public repos. The build succeeds.

## Requirements

### Registration and CLI

**R1**: `niwa config set private <repo>` registers a private workspace extension. `<repo>` is an org/repo shorthand (`acmecorp/dot-niwa-private`) or full HTTPS/SSH URL.

**R2**: `niwa config unset private` removes the registration and deletes the local clone of the companion.

**R3**: `niwa init --skip-private` stores a `skip_private` flag in the instance's `.niwa/instance.json`. Subsequent applies on that instance skip the private companion without requiring the flag again.

**R4**: `niwa apply --skip-private` skips the private companion for that single apply invocation, regardless of registration state or instance flags.

### Companion Sync and Discovery

**R5**: When `niwa apply` runs with a private companion registered and no `skip_private` flag, niwa syncs the companion (equivalent to the global config sync step).

**R6**: If the companion has never been successfully cloned on the current machine and the clone fails for any reason (not found, access denied, network error), niwa silently skips the private companion and continues apply with the public config only. No error message, no warning.

**R7**: If the companion was previously successfully cloned on the current machine and the sync fails for any reason, `niwa apply` aborts with an error identifying the companion as the cause. Example: `"Private workspace extension sync failed: <error>. Check access with \`niwa config set private\` or skip with --skip-private."`

**R8**: niwa derives "previously cloned" state from the presence of a git repository in the companion's local clone directory (`$XDG_CONFIG_HOME/niwa/private/`). If the directory exists and is a valid git repo, it was previously cloned.

### Private Companion Format

**R9**: A private companion repo contains a file named `workspace-extension.toml` at the repo root. This file is the extension configuration.

**R10**: `workspace-extension.toml` supports these additive sections: `[[sources]]`, `[groups.*]`, `[repos.*]`, `[claude.content.*]`.

**R11**: `workspace-extension.toml` supports these override sections: `[claude.hooks]`, `[claude.settings]`, `[env]`, `[files]`. Merge semantics match GlobalOverride (hooks append; settings per-key; env files append, env vars per-key; files per-key).

**R12**: `workspace-extension.toml` does not support workspace metadata fields (`[workspace]`, `[channels]`, `[[sources]]` auto-discovery without explicit `repos` list — see R17).

### Merge Semantics

**R13**: Sources from the companion are appended to the public config's sources after parsing. The combined sources list drives repo discovery.

**R14**: Groups from the companion are added to the public config's group map. If a group name exists in both the public config and the companion, the public config's definition takes precedence.

**R15**: Repo overrides from the companion are added to the public config's repos map. If a repo entry exists in both the public config and the companion, the public config's entry takes precedence.

**R16**: Content entries from the companion are added to the public config's content map. If a content entry exists for the same repo in both configs, the public config's entry takes precedence.

**R17**: If both the public config and the companion declare a `[[sources]]` entry for the same GitHub org, `niwa apply` aborts with an error: `"Duplicate source org '<org>' found in workspace config and private companion. Use explicit repos lists in both source declarations to resolve."` Auto-discovery (`[[sources]] org = "X"` without an explicit `repos` list) is prohibited in companion files for orgs that also appear in the public config.

### CLAUDE Context Injection

**R18**: If `CLAUDE.private.md` exists in the companion's root directory, niwa copies it to the instance root and injects `@CLAUDE.private.md` into the workspace's `CLAUDE.md`, placed after the workspace context import and before the global config import.

**R19**: If `CLAUDE.private.md` does not exist in the companion, no injection occurs and no error is produced.

### Security

**R20**: Private companion registration is stored in `~/.config/niwa/config.toml` under a `[private_workspace]` section. The local clone path is derived at runtime from `$XDG_CONFIG_HOME/niwa/private/` and is not stored in the config file.

**R21**: Parsing `workspace-extension.toml` must validate all `files` destination paths and `env.files` source paths using the same path-traversal rejection rules applied to GlobalOverride config (reject absolute paths and `..` path components).

**R22**: Hook scripts declared in `workspace-extension.toml` are resolved to absolute paths using the companion's local directory before merging, following the same pattern as GlobalOverride hook script resolution.

**R23**: niwa must not include the companion's registration URL, local path, or any reference to the companion's existence in standard apply output. These may appear in verbose/debug output only.

## Acceptance Criteria

### Registration

- [ ] `niwa config set private acmecorp/dot-niwa-private` stores the URL in `~/.config/niwa/config.toml` under `[private_workspace]` and clones the companion to `$XDG_CONFIG_HOME/niwa/private/`.
- [ ] `niwa config unset private` removes the `[private_workspace]` section from config and deletes `$XDG_CONFIG_HOME/niwa/private/`.
- [ ] After `niwa config set private <inaccessible-repo>`, `niwa apply` produces no error and no output related to the companion. The workspace applies with public config only.
- [ ] `niwa init --skip-private` results in `.niwa/instance.json` with `skip_private: true`. Subsequent `niwa apply` on that instance does not attempt to sync the companion, even if one is registered.

### First-Time and Graceful Degradation

- [ ] First `niwa apply` with a private companion registered on a machine where the companion was never cloned: if clone fails (auth error, not found, network error), apply completes successfully with public repos only. No error or warning output related to the companion.
- [ ] First `niwa apply` with a private companion registered and clone succeeds: companion repos are merged into the workspace. Apply produces a workspace with repos from both configs.
- [ ] Subsequent `niwa apply` after companion was previously cloned successfully: if sync fails, apply aborts with an error that identifies the companion as the cause.
- [ ] `niwa apply --skip-private` on an instance with `skip_private: false` skips the companion for that invocation only. The next `niwa apply` without the flag uses the companion normally.

### Merge Semantics

- [ ] With a companion that adds `[[sources]] org = "internal-org" repos = ["vision"]`, and public config with `[[sources]] org = "tsukumogami"`: after `niwa apply`, the workspace contains repos from both orgs.
- [ ] With companion and public config both defining a group named `tools`: the public config's `tools` group definition is used; the companion's is silently ignored.
- [ ] With companion and public config both defining `[repos.vision]`: the public config's entry is used; the companion's is silently ignored.
- [ ] With companion declaring `[[sources]] org = "tsukumogami"` (same org as public config), and public config also declaring `[[sources]] org = "tsukumogami"`: `niwa apply` aborts with a duplicate-source-org error before any repos are modified.

### Content and CLAUDE Injection

- [ ] Companion with `[claude.content.repos.vision] source = "repos/vision.md"` and `repos/vision.md` present in the companion repo: after `niwa apply`, the workspace instance contains `vision/CLAUDE.local.md` generated from that content file.
- [ ] Companion with `CLAUDE.private.md` at its root: after `niwa apply`, the instance root contains `CLAUDE.private.md` and the workspace `CLAUDE.md` contains `@CLAUDE.private.md`.
- [ ] Companion without `CLAUDE.private.md`: after `niwa apply`, the workspace `CLAUDE.md` does not contain `@CLAUDE.private.md`. No error.
- [ ] Workspace `CLAUDE.md` import order: workspace context appears before `@CLAUDE.private.md`, which appears before `@CLAUDE.global.md` (if global config is also registered).

### Security

- [ ] `workspace-extension.toml` with `files` containing a destination path of `../../.ssh/authorized_keys` is rejected at parse time with an error. No disk writes occur.
- [ ] `workspace-extension.toml` with `env.files` containing an absolute path is rejected at parse time with an error.
- [ ] Standard `niwa apply` output (non-verbose) contains no reference to the private companion's repo name, URL, or registration status.

## Out of Scope

- **Secrets management**: Vault integration for removing secrets from workspace configs. Covered separately.
- **Per-developer personal config**: The GlobalOverride (`niwa config set global`) handles personal hooks, env, and settings. The private workspace extension is for team-shared private repo configuration, not personal preferences.
- **Selective per-repo access within the companion**: Access to the private companion is all-or-nothing. Users either have permission to clone the companion (and get all private repos) or they don't (and get none). Fine-grained per-repo access within the companion is not supported in v1.
- **Content override for repos in the public config**: The companion can provide CLAUDE.md content for repos it introduces. It cannot override or augment CLAUDE.md content for repos already defined in the public config.
- **Multiple private companions per workspace**: A workspace has at most one registered private companion. Stacking multiple companions is out of scope.
- **Auto-hiding of auto-discovered repos**: If the public config uses `[[sources]] org = "shared-org"` without explicit repo lists, auto-discovery queries the GitHub API and may expose private repo names in warning output ("matched no group"). Teams that need to hide private repo names must use explicit repo lists in their public config for shared orgs.
- **Migration tooling**: No `niwa migrate-private` command in v1. Teams split their config manually. Documentation covers the process.
- **Workspace.toml companion declaration field**: The public `workspace.toml` does not gain a `private_extension` field. The companion is registered per-machine via `niwa config set private`, not declared in the public config. This preserves the property that the public config is completely unaware of the companion.

## Known Limitations

**Shared GitHub orgs require explicit repo lists.** If a team's public and private repos share a GitHub org, the public config's `[[sources]]` for that org must use an explicit `repos` list rather than auto-discovery (which would expose private repo names in warning output). This is a constraint on the public config's structure, not a niwa limitation per se, but it reduces the zero-configuration benefit of auto-discovery for mixed-visibility orgs.

**All-or-nothing private access.** A user either has access to the entire companion repo or none of it. Teams with more granular access needs (contributor A sees private repos P1 and P2 but not P3) cannot express this through niwa's companion model. They would need multiple workspace configs or per-repo access controls outside niwa.

**Machine-level registration.** The private companion is registered per machine, not per workspace instance. All instances of a workspace on the same machine use the same companion (unless initialized with `--skip-private`). If different instances should use different companions, `--skip-private` can disable the companion per-instance, but there is no positive per-instance companion override.

**Companion sync failure is fatal after initial access.** Once a user has successfully cloned the companion, any subsequent sync failure causes the apply to abort. This provides a fail-safe against silently operating with a stale companion after network or permission changes, but means users with intermittent connectivity issues must use `--skip-private` to work offline.

## Decisions and Trade-offs

**Registration-only, no workspace.toml field.**
The companion is registered via `niwa config set private`, not declared in `workspace.toml`. An explicit field in `workspace.toml` would disclose the companion's existence in the public config — even without revealing the companion's contents. Keeping the public config unaware of the companion's existence is the stronger privacy model, and it mirrors how the GlobalOverride layer works (registered per machine, not declared in workspace.toml). Tradeoff: new team members must know to register the companion; there's no self-documenting pointer in the public config. Mitigation: teams can add a comment to their public `workspace.toml` or README explaining the companion's existence, without niwa requiring it.

**Silent skip on first-time clone failure.**
When the companion has never been cloned on a machine and the clone fails, niwa skips it silently. The alternative (warn the user) would reveal the companion's existence to users without access. GitHub returns HTTP 404 for both "repo doesn't exist" and "access denied" — niwa cannot distinguish these, so any user-visible message would require revealing that a companion was attempted. The local-clone-presence heuristic (if `$XDG_CONFIG_HOME/niwa/private/` exists and is a git repo, the user has access) is the practical way to distinguish "never had access" from "had access but something went wrong."

**Companion format uses `workspace-extension.toml`, not `workspace.toml`.**
A distinct filename clarifies that the companion is an extension (additive and override), not a standalone workspace config. A companion named `workspace.toml` would mislead contributors into thinking it can be used as a standalone workspace config. The distinct name also allows niwa to apply different parsing rules (no `[workspace]` metadata, restricted `[[sources]]` behavior).

**Public config precedence on collisions.**
When both the public config and the companion define the same group, repo, or content entry, the public config wins. The alternative (companion wins) would allow private config to silently override public config behavior, which is unexpected for teams that think of the companion as additive-only. Public-config-wins is the conservative default; teams that need companion overrides can remove the entry from the public config.

## Open Questions

**Q1 — Should `niwa status` display companion registration state?**
Showing companion registration state (registered URL, sync state) in `niwa status` improves debuggability for users with access. But any output about the companion is potentially visible to users without access (shared terminals, log aggregation). Options: (a) always show in status output, (b) show only when a companion is registered and accessible, (c) omit from status entirely, available via a separate `niwa config show private` command. Decision needed before acceptance.

**Q2 — Should `workspace-extension.toml` permit auto-discovery for companion-only orgs?**
R17 prohibits auto-discovery for orgs that also appear in the public config's sources, but what about orgs that exist only in the companion (not referenced anywhere in the public config)? Auto-discovery for companion-only orgs would work without collision risk, and would reduce the companion's required configuration. The concern is that auto-discovery queries the GitHub API, and a companion-only org's repos would be visible to the apply pipeline (even if not in the public config). Permitting auto-discovery for companion-only orgs is the simpler model; requiring explicit lists everywhere is the safer model.

**Q3 — Should there be a `niwa config check` command to audit the public/private split?**
A command that validates that no private repo names appear in the public workspace config (by cross-referencing the GitHub visibility API) would help teams preparing to publish their config. Out of scope for v1, but worth noting as a follow-on feature.
