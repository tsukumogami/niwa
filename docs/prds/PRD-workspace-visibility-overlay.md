---
status: Delivered (extended)
version: 5
problem: |
  When a niwa workspace config repo is made public, the workspace.toml exposes information through several surfaces: source org identifiers in [[sources]], group names, [repos.*] section keys (which are repo names), [claude.content.repos.*] entries including subdirectory mappings that reveal code structure, [channels.*.access] sections containing user IDs, and vault infrastructure details such as provider kind, project IDs, and secret names in [vault.provider] and [env.secrets]. Teams that want to publish their workspace config — to enable open contribution, share it as a reference, or reduce maintenance burden — currently have no way to keep these details out of the public config without maintaining a completely separate workspace config that duplicates all the public configuration.
goals: |
  Teams can publish their niwa workspace config while keeping additional repo references, group definitions, operational config, and vault infrastructure in a separately access-controlled overlay repo that niwa fetches automatically when accessible. Users without overlay access experience a complete workspace with the base repos only — all configured repos are cloned, hooks execute without error, and environment setup matches the workspace config — with no output referencing the overlay or its contents. The base config documents what secrets are needed; the overlay specifies how to obtain them.
---

# PRD: Workspace overlay

## Status

Delivered (extended — R23/R24 open)

## Problem Statement

When a niwa workspace config repo is published, the `workspace.toml` reveals information through several surfaces: `[[sources]]` entries expose which GitHub orgs and repos the workspace includes; `[groups.*]` definitions expose organizational taxonomy; `[repos.*]` TOML section keys are repo names; `[claude.content.repos.*]` entries reveal internal directory structure via subdirectory mappings; `[channels.*.access]` sections expose user IDs; and `[vault.provider]` entries expose vault infrastructure details (provider kind, project IDs) while `[env.secrets]` entries expose the names of secrets the team depends on.

Teams that want the benefits of a published workspace config — enabling contributors to initialize from it, using it as documentation of workspace structure, sharing tooling practices — need a way to separate base configuration from additional configuration without abandoning niwa's single-source-of-truth model. This includes keeping vault infrastructure and secret resolution out of the public file while still documenting what secrets are required.

The overlay mechanism is about layering, not visibility. Both the base workspace config and the overlay repo can be public or private — a team might overlay a public supplemental config on top of a public base, or an access-controlled repo on top of a published one. The mechanism works the same either way.

## Goals

1. Teams can publish their workspace config repo while keeping additional repo references, group definitions, operational config, and vault infrastructure in a separate overlay repo.
2. Users with access to the overlay repo get a complete workspace (base and overlay repos) from a single `niwa init` + `niwa apply` — no additional registration step.
3. Users without overlay access get a complete workspace with base repos only, with no output (stdout, stderr, or log files) referencing the overlay or its contents.
4. Overlay discovery is automatic by convention with no configuration required for the common case.
5. The base config documents what secrets are needed (and at what tier: required, recommended, or optional) without specifying vault addresses. The overlay provides the vault provider and resolution paths.

## User Stories

**US1 — Team publishing a workspace config**
As a team lead, I want to publish the workspace config to GitHub while keeping additional repo configuration in a separate overlay, so that contributors can initialize from the public config and it can serve as documentation of the team's shared tooling.

*Scenario*: A team has repos managed by niwa. The lead splits the config: `acmecorp/dot-niwa` (the base) contains the public repos; `acmecorp/dot-niwa-overlay` (the overlay) contains additional repos and private context. When a contributor runs `niwa init --from acmecorp/dot-niwa`, niwa automatically discovers and applies `acmecorp/dot-niwa-overlay` if accessible. If not accessible, the workspace initializes with the base repos only.

**US2 — Contributor with overlay access**
As a contributor with access to both the base and overlay repos, I want `niwa init` to automatically discover and apply the overlay so I get a complete workspace without any additional steps.

*Scenario*: An engineer runs `niwa init --from acmecorp/dot-niwa ~/acmecorp`. niwa discovers `acmecorp/dot-niwa-overlay` automatically, clones it, and applies it. `niwa apply` subsequently keeps both in sync. No separate registration command is needed.

**US3 — Contributor without overlay access**
As a contributor who doesn't have access to the overlay, I want to initialize from the base config and work productively with those repos, without hitting errors or learning what the overlay contains.

*Scenario*: A new contributor runs `niwa init --from acmecorp/dot-niwa ~/acmecorp`. niwa tries `acmecorp/dot-niwa-overlay`, gets a 404, silently skips it, and initializes with the base repos only. No error or warning is shown. When the contributor is later granted overlay access, the next `niwa apply` discovers and applies it automatically.

**US4 — CI/CD environment**
As a CI/CD pipeline, I want to apply the workspace config with only the base repos and no overlay access required, so that builds are deterministic.

*Scenario*: A GitHub Actions workflow calls `niwa init --from acmecorp/dot-niwa --no-overlay`. The overlay is not attempted. The workspace has the base repos only. The build succeeds.

## Worked Example

This section walks through a complete setup for a team at `acmecorp`.

### GitHub org shape

```
acmecorp/
├── dot-niwa          — base workspace config repo
├── dot-niwa-overlay  — overlay repo (matches convention)
├── website           — marketing site
├── docs              — public documentation
├── api               — public API
├── billing           — billing service
├── auth-service      — internal auth service
└── internal-tools    — developer tooling
```

The base and overlay repos can have any visibility. In this example `dot-niwa` is public and `dot-niwa-overlay` is access-controlled, but the mechanism is the same if both are public.

### Base workspace config

`acmecorp/dot-niwa/workspace.toml`:

```toml
[workspace]
name = "acmecorp"

[[sources]]
org = "acmecorp"
repos = ["website", "docs", "api"]

[groups.frontend]
repos = ["website", "docs"]

[groups.backend]
repos = ["api"]

[claude.content.repos.api]
source = "content/api.md"

# Declare what secrets this workspace needs without specifying how to obtain them.
# The overlay provides vault addresses for these vars.
[env.secrets.required]
ACMECORP_API_KEY = "API key for acmecorp services - resolved by overlay vault"

[env.secrets.recommended]
ACMECORP_MONITORING_TOKEN = "Monitoring integration token - see internal runbook"
```

`acmecorp/dot-niwa/content/api.md`:

```markdown
# api

Public REST API. See docs/ for the OpenAPI spec.
Routes live in internal/routes/. Add new endpoints in their own file.
```

### Overlay repo

`acmecorp/dot-niwa-overlay/workspace-overlay.toml`:

```toml
# Vault provider — kept private so project IDs don't appear in the public base config.
[vault.provider]
kind    = "infisical"
project = "c6673ab0-c95d-4570-b947-5f77501ce38a"

# Vault resolution for secrets declared as required/recommended in the base config.
[env.secrets]
ACMECORP_API_KEY           = "vault://ACMECORP_API_KEY"
ACMECORP_MONITORING_TOKEN  = "vault://ACMECORP_MONITORING_TOKEN"

[[sources]]
org = "acmecorp"
repos = ["billing", "auth-service", "internal-tools"]

[groups.extended]
repos = ["billing", "auth-service", "internal-tools"]

[claude.content.repos.billing]
source = "content/billing.md"

[claude.content.repos.api]
overlay = "overlays/api-internal.md"

[env.vars]
ACMECORP_INTERNAL = "true"
```

`acmecorp/dot-niwa-overlay/content/billing.md`:

```markdown
# billing

Internal billing service. Stripe keys are in Vault at secret/billing/stripe.
Never log request bodies — they contain card data.
```

`acmecorp/dot-niwa-overlay/overlays/api-internal.md` — appended to `api/CLAUDE.local.md` for users with overlay access:

```markdown
## Internal notes

The public API has a shadow admin API at /internal/. It is not documented
externally. Auth uses the ACMECORP_ADMIN_TOKEN env var (see Vault).
```

`acmecorp/dot-niwa-overlay/CLAUDE.overlay.md`:

```markdown
Internal contributor context for acmecorp. Treat all data in the extended
repos as confidential. Do not reference internal repo names in public issues
or PRs.
```

### Init and apply — contributor with overlay access

```bash
# Init discovers dot-niwa-overlay automatically (convention: <repo>-overlay)
niwa init --from acmecorp/dot-niwa ~/acmecorp

# Apply clones all repos and installs CLAUDE context
cd ~/acmecorp
niwa apply
```

### Local workspace — overlay accessible

```
~/acmecorp/
├── .niwa/
│   ├── workspace.toml     — clone of acmecorp/dot-niwa
│   └── instance.json      — {overlay_url: "acmecorp/dot-niwa-overlay"}
├── CLAUDE.md              — @workspace-context.md, @CLAUDE.overlay.md
├── CLAUDE.overlay.md      — copied from dot-niwa-overlay
├── website/
├── docs/
├── api/
│   └── CLAUDE.local.md    — public content + overlay (see below)
├── billing/
│   └── CLAUDE.local.md    — overlay content only
├── auth-service/
└── internal-tools/
```

`api/CLAUDE.local.md`:

```markdown
# api

Public REST API. See docs/ for the OpenAPI spec.
Routes live in internal/routes/. Add new endpoints in their own file.

## Internal notes

The public API has a shadow admin API at /internal/. It is not documented
externally. Auth uses the ACMECORP_ADMIN_TOKEN env var (see Vault).
```

### Init and apply — contributor without overlay access

```bash
# Init tries dot-niwa-overlay, gets 404, silently skips
niwa init --from acmecorp/dot-niwa ~/acmecorp

# Apply with base repos only
cd ~/acmecorp
niwa apply
```

### Local workspace — overlay inaccessible

```
~/acmecorp/
├── .niwa/
│   ├── workspace.toml     — clone of acmecorp/dot-niwa
│   └── instance.json      — {} (no overlay_url stored)
├── CLAUDE.md              — @workspace-context.md
├── website/
├── docs/
└── api/
    └── CLAUDE.local.md    — public content only
```

`api/CLAUDE.local.md`:

```markdown
# api

Public REST API. See docs/ for the OpenAPI spec.
Routes live in internal/routes/. Add new endpoints in their own file.
```

The billing, auth-service, and internal-tools repos are absent. No warning is produced. The workspace is fully functional for development against the base repos.

### Later: contributor gains overlay access

No reconfiguration needed. The next `niwa apply` tries the convention, succeeds, stores the overlay URL in `instance.json`, and adds the overlay repos and context:

```bash
niwa apply
```

### CI/CD: opt out of overlay discovery

```bash
niwa init --from acmecorp/dot-niwa ~/acmecorp --no-overlay
cd ~/acmecorp
niwa apply   # overlay never attempted; instance.json has no_overlay: true
```

### Explicit overlay (non-convention name)

```bash
niwa init --from acmecorp/dot-niwa ~/acmecorp --overlay acmecorp/niwa-internal
```

If `acmecorp/niwa-internal` is inaccessible, `niwa init` aborts with a hard error. Explicit intent requires explicit success.

## Requirements

### Overlay Discovery and Init

**R1**: `niwa init --from <repo>` automatically attempts overlay discovery by trying `<org>/<repo-name>-overlay` (the overlay convention — same org, repo name with `-overlay` appended). If the clone succeeds, `overlay_url` is stored in `.niwa/instance.json`. If the clone fails for any reason (HTTP 404, HTTP 403, network timeout, DNS failure), discovery is silently skipped and nothing is stored. All failure reasons are treated identically.

**R2**: `niwa init --from <repo> --overlay <overlay-repo>` uses the specified overlay URL. `<overlay-repo>` is an org/repo shorthand or full HTTPS/SSH URL. If the clone fails for any reason, `niwa init` aborts with a non-zero exit code and an error identifying the overlay as the cause. If successful, `overlay_url` is stored in `.niwa/instance.json`.

**R3**: `niwa init --from <repo> --no-overlay` stores `no_overlay: true` in `.niwa/instance.json`. All subsequent `niwa apply` invocations on this instance skip overlay discovery and sync entirely, even if an overlay repo matching the convention later becomes accessible.

### Overlay Sync

**R4**: When `niwa apply` runs, it determines overlay behavior from `.niwa/instance.json`:
- If `no_overlay: true` → skip overlay entirely, proceed with base config only
- If `overlay_url` is set → sync the overlay (git pull); failure behavior governed by R5–R6
- If neither is set → attempt convention-based discovery identical to R1: try `<org>/<repo-name>-overlay`; if successful, store `overlay_url` in `instance.json` and apply the overlay; if not, skip silently

**R5**: If the overlay has never been successfully cloned on the current machine and the clone or sync fails for any reason, niwa silently skips the overlay and continues apply with the base config only. No error message, no warning, no non-zero exit code related to the overlay.

**R6**: If the overlay was previously successfully cloned on the current machine and the sync fails for any reason, `niwa apply` aborts with a non-zero exit code and an error message identifying the overlay sync as the cause. The error must not include the overlay's URL or path in standard output. Example: `"Workspace overlay sync failed: <error>. Use --no-overlay to skip."` 

**R7**: niwa derives "previously cloned" state using the following check: the overlay's local clone directory (see R19) exists AND `git -C <clone-dir> rev-parse HEAD` exits with code 0. If both conditions are true, the overlay is treated as previously cloned and R6 applies on sync failure. Otherwise R5 applies.

### Overlay Format

**R8**: A workspace overlay repo contains a file named `workspace-overlay.toml` at the repo root. If the overlay repo is accessible but `workspace-overlay.toml` is missing, `niwa apply` aborts with a non-zero exit code and an error identifying the missing file.

**R9**: `workspace-overlay.toml` supports these additive sections: `[[sources]]`, `[groups.*]`, `[repos.*]`, `[claude.content.*]`, `[vault.provider]`. Within `[claude.content.repos.*]`, two field variants are supported: `source` (for repos introduced by the overlay) and `overlay` (for repos already defined in the base config — see R13).

**R10**: `workspace-overlay.toml` supports these override sections: `[claude.hooks]`, `[claude.settings]`, `[env]`, `[files]`. Merge semantics: hooks append (overlay hooks added after base config hooks); settings per-key (overlay value used only if key absent in base config); `env.files` append (overlay files sourced after base config files); `env` vars per-key (overlay value used only if key absent in base config); `env.secrets` per-key (overlay resolution used only if key absent in base config's `[env.secrets]`); `files` per-key (overlay file used only if destination absent in base config).

**R11**: `workspace-overlay.toml` does not support workspace metadata fields (`[workspace]`, `[channels]`). All `[[sources]]` entries must include explicit `repos` lists — auto-discovery is prohibited in overlay source declarations for all orgs.

### Vault in the Overlay

**R23**: When `workspace-overlay.toml` declares `[vault.provider]`, niwa builds a vault provider bundle from that declaration and resolves `vault://` references in the overlay's `[env.secrets]` entries against it, before merging the overlay into the base config. The overlay vault provider is built and torn down independently of any vault provider declared in the base config — each layer resolves its own secrets in isolation (the same per-layer scoping applied to the global config overlay). Provider names are scoped to their layer; a named provider in the overlay and a same-named provider in the base config do not collide because they never interact.

**R24**: The base config may declare what env vars are needed without specifying vault addresses, using three sub-tables of `[env.secrets]`:

- `[env.secrets.required]`: env vars that must be present when `niwa apply` completes. If a required var is absent after all vault resolution and env file sourcing, `niwa apply` aborts with a non-zero exit code naming the missing var.
- `[env.secrets.recommended]`: env vars that should be present. If absent after all resolution, `niwa apply` emits a stderr warning and continues.
- `[env.secrets.optional]`: env vars that are documented as useful but not expected to be present universally. If absent, no diagnostic is produced.

All three sub-tables use `KEY = "description"` syntax, where the description explains the var's purpose and how to obtain it. These declarations coexist with vault:// references: a key declared in `[env.secrets.required]` and also appearing in the overlay's `[env.secrets]` (with a vault:// value) is not a conflict — the required check verifies the var is present, and the overlay's vault ref provides its value.

### Merge Semantics

When an entry exists in both the base config and the overlay, "base config takes precedence" means the base config's entry is used in its entirety and the overlay's entry for that key is discarded without warning or error. No field-level merging occurs within a group or repo entry. Content entries are the exception: overlays may use the `overlay` field to append context to a base-config repo's generated `CLAUDE.local.md` without replacing it (see R13).

**R12**: Sources from the overlay are appended to the base config's sources after parsing. The combined sources list drives repo discovery. If the same repo is discovered from multiple sources, it is deduplicated (keeping the first occurrence) and the duplicate is silently skipped. If any org in the overlay's `[[sources]]` entries matches an org in the base config's `[[sources]]` entries, `niwa apply` aborts before any git operations with: `"Duplicate source org '<org>' found in workspace config and overlay. Use explicit repos lists in both source declarations to resolve."`

**R13**: Content entries from the overlay are handled by field:
- **Overlay-only repo** (`source` field): creates `CLAUDE.local.md` for that repo from the specified file.
- **Base-config repo with `overlay` field**: appends the overlay file's content to the generated `CLAUDE.local.md` for that repo, separated from the base content by a blank line. The overlay path is resolved relative to the overlay's local clone directory and is subject to the same path-traversal validation as R20.
- **Base-config repo with `source` field**: error — `niwa apply` aborts with a message stating that `source` is not allowed for repos already defined in the base config; use `overlay` instead.

Users without overlay access receive the base `CLAUDE.local.md` only; no overlay content is applied.

**R14**: Groups from the overlay are added to the base config's group map. If a group name exists in both, the base config's definition is used and the overlay's is discarded without warning.

**R15**: Repo overrides from the overlay are added to the base config's repos map. If a repo entry exists in both, the base config's entry is used and the overlay's is discarded without warning.

### CLAUDE Context Injection

**R16**: If `CLAUDE.overlay.md` exists in the overlay repo's root directory, niwa copies it to the instance root and injects `@CLAUDE.overlay.md` into the workspace's `CLAUDE.md`, placed after the workspace context import and before the global config import (if registered).

**R17**: If `CLAUDE.overlay.md` does not exist in the overlay repo, no injection occurs and no error is produced.

### Security

**R18**: The overlay URL is stored in `.niwa/instance.json` as `overlay_url`. The local clone path is derived at runtime as `$XDG_CONFIG_HOME/niwa/overlays/<workspace-id>/` where `<workspace-id>` is derived from the overlay URL. The local path is never stored in `instance.json`. If `XDG_CONFIG_HOME` is not set, niwa uses `~/.config` as the default, following the XDG Base Directory specification.

**R19**: Previously-cloned detection uses the clone directory derived from `instance.json`'s `overlay_url` at runtime (see R18). The check: directory exists AND `git -C <dir> rev-parse HEAD` exits 0.

**R20**: Parsing `workspace-overlay.toml` must validate all `files` destination paths, `env.files` source paths, and `[claude.content.repos.*] overlay` paths: reject any path that is absolute (starts with `/`) or contains `..` as a path component. Rejection occurs during overlay parsing, before any workspace file operations. niwa exits with a non-zero exit code and an error identifying the invalid path. No workspace files are written.

**R21**: Hook scripts declared in `workspace-overlay.toml` are resolved to absolute paths using the overlay's local clone directory before merging. Relative hook script paths are resolved relative to the overlay's local clone directory.

**R22**: In standard apply output (without `--debug` or `--verbose` flags), niwa must not include the overlay's URL, local path, repo name, or any text indicating an overlay was consulted. Overlay details may appear in debug-level output when `--debug` or `--verbose` is passed. The error message in R6 must not include the overlay's URL or repository name.

## Acceptance Criteria

### Discovery and Init

- [ ] `niwa init --from acmecorp/dot-niwa ~/acmecorp` where `acmecorp/dot-niwa-overlay` exists and is accessible: `instance.json` contains `overlay_url: "acmecorp/dot-niwa-overlay"`.
- [ ] `niwa init --from acmecorp/dot-niwa ~/acmecorp` where `acmecorp/dot-niwa-overlay` returns 404 or 403: `instance.json` contains no `overlay_url`. stdout and stderr contain no text referencing an overlay attempt. Exit code 0.
- [ ] `niwa init --from acmecorp/dot-niwa --overlay acmecorp/niwa-internal ~/acmecorp` where `acmecorp/niwa-internal` is inaccessible: `niwa init` exits with a non-zero exit code and an error. No workspace directory is created.
- [ ] `niwa init --from acmecorp/dot-niwa --no-overlay ~/acmecorp`: `instance.json` contains `no_overlay: true`. Subsequent `niwa apply` does not attempt overlay discovery or sync even if `acmecorp/dot-niwa-overlay` is accessible.

### Overlay Sync

- [ ] `niwa apply` on an instance with no `overlay_url` and no `no_overlay` where `<repo>-overlay` is now accessible: apply stores `overlay_url` in `instance.json` and applies the overlay. Workspace contains overlay repos.
- [ ] `niwa apply` on an instance with `overlay_url` set, overlay previously cloned, sync fails: apply exits with a non-zero exit code and an error message identifying the overlay sync as the cause without disclosing the overlay's URL.
- [ ] `niwa apply` on an instance with `overlay_url` set, overlay never cloned on this machine, sync fails: apply exits with code 0. stdout and stderr contain no text referencing the overlay. Workspace contains only base repos.

### Merge Semantics

- [ ] Overlay with `[[sources]] org = "acmecorp" repos = ["billing"]` and base config with `[[sources]] org = "acmecorp" repos = ["api"]` (same org, different explicit repo lists): `niwa apply` exits with a non-zero exit code and a duplicate-source-org error before any git operations on workspace repos.
- [ ] Overlay with `[[sources]] org = "internal-org" repos = ["vision"]` and base config with `[[sources]] org = "acmecorp" repos = ["api"]` (different orgs): after `niwa apply`, workspace contains repos from both orgs.
- [ ] Overlay and base config both defining a group named `tools`: base config's `tools` group is used; overlay's is discarded. No warning or error.
- [ ] Overlay and base config both defining `[repos.api]`: base config's entry is used; overlay's is discarded. No warning or error.
- [ ] Overlay with `[env] OVERLAY_TOKEN = "x"` and base config with `[env] BASE_KEY = "y"`: after `niwa apply`, both env vars are available.
- [ ] Overlay with `env.files = ["overlay.env"]` and base config with `env.files = ["base.env"]`: after `niwa apply`, both files are sourced, base config files first.

### Content and CLAUDE Injection

- [ ] Overlay with `[claude.content.repos.billing] source = "content/billing.md"` and `billing` not defined in the base config: after `niwa apply`, `billing/CLAUDE.local.md` is generated from that file.
- [ ] Overlay with `[claude.content.repos.api] overlay = "overlays/api-internal.md"` and `api` defined in the base config's content map: after `niwa apply`, `api/CLAUDE.local.md` contains the base content followed by a blank line and the overlay content.
- [ ] Same as above, but overlay is inaccessible or skipped: `api/CLAUDE.local.md` contains only the base content. No indication of overlay content is present.
- [ ] Overlay with `[claude.content.repos.api] source = "..."` and `api` defined in the base config's content map: `niwa apply` exits with a non-zero exit code and an error stating `source` is not allowed for base-config repos.
- [ ] Overlay with `CLAUDE.overlay.md` at its root: after `niwa apply`, instance root contains `CLAUDE.overlay.md` and workspace `CLAUDE.md` contains `@CLAUDE.overlay.md`.
- [ ] Overlay without `CLAUDE.overlay.md`: workspace `CLAUDE.md` does not contain `@CLAUDE.overlay.md`. No error.
- [ ] `CLAUDE.md` import order (when both overlay and global config are registered): reading top-to-bottom, `@CLAUDE.overlay.md` appears after workspace context import and before `@CLAUDE.global.md`.
- [ ] Overlay with `[claude.hooks] on_apply = ["scripts/post-apply.sh"]`: after `niwa apply`, the hook path is resolved to the overlay's local clone directory (absolute path).

### Security

- [ ] `workspace-overlay.toml` with a `files` destination containing `..` as a path component: `niwa apply` exits with a non-zero exit code before writing any workspace files.
- [ ] `workspace-overlay.toml` with `env.files` containing an absolute path: `niwa apply` exits with a non-zero exit code before writing any workspace files.
- [ ] `niwa apply` stdout and stderr (without `--debug` or `--verbose`) contain no text matching the overlay's repo name, URL, or local path.
- [ ] Overlay repo accessible but `workspace-overlay.toml` absent: `niwa apply` exits with a non-zero exit code and an error identifying the missing file.

### Overlay Vault and Secret Tiers

- [ ] Base config with `[env.secrets.required] FOO = "description"` and overlay with `[vault.provider]` + `[env.secrets] FOO = "vault://FOO"`: after `niwa apply`, `FOO` is written to `.local.env` resolved from the overlay vault.
- [ ] Base config with `[env.secrets.required] FOO = "description"`, no overlay, `FOO` absent from environment: `niwa apply` aborts with a non-zero exit code and an error naming `FOO`.
- [ ] Base config with `[env.secrets.recommended] BAR = "description"`, `BAR` absent after all resolution: `niwa apply` exits with code 0 and emits a stderr warning naming `BAR`.
- [ ] Base config with `[env.secrets.optional] BAZ = "description"`, `BAZ` absent: `niwa apply` exits with code 0, no warning produced.
- [ ] Overlay with `[vault.provider]` and base config with `[vault.provider]`: both resolve their own layer's `[env.secrets]` independently; no collision error.
- [ ] Overlay with `[vault.providers.infisical]` and base config with `[vault.providers.infisical]` (same name, different layers): each resolves its own layer's secrets independently; no collision error.

## Out of Scope

- **Registering a non-convention overlay after init**: If an overlay repo with a non-convention name is created after the workspace is initialized, there is no v1 command to register it. Teams in this situation must use `--overlay` at init time, or use the convention name. A workspace-level `niwa config set` command is deferred to a future release.
- **Per-developer personal config**: The GlobalOverride (`niwa config set global`) handles personal hooks, env, and settings. The overlay is for workspace-level supplemental configuration, not personal preferences.
- **Selective per-repo access within the overlay**: Access to the overlay is all-or-nothing. Users either clone the overlay and get all overlay config, or they don't and get none.
- **Full content replacement for repos in the base config**: The overlay can provide content for repos it introduces (`source`) and can append context to base repos (`overlay` field). It cannot replace the base config's content entry for a base repo — `source` on a base-config repo is an error.
- **Multiple overlays per workspace**: A workspace instance has at most one overlay. Stacking multiple overlays is out of scope.
- **Auto-hiding of auto-discovered repos**: If the base config uses `[[sources]]` without explicit repo lists, auto-discovery queries the GitHub API and may expose repo names in warning output. Teams that need to hide repo names must use explicit repo lists.
- **Migration tooling**: No migration command in v1. Teams restructure their config manually.

## Known Limitations

**Explicit repos lists required for shared source orgs.** If the base and overlay configs share a source org declaration, both must use explicit `repos` lists. A shared org without explicit lists triggers a duplicate-source-org error.

**All-or-nothing overlay access.** A user either has access to the entire overlay repo or none of it. Fine-grained per-repo access within the overlay is not supported.

**Non-convention overlay names require `--overlay` at init.** If the overlay repo does not follow the `<base-repo>-overlay` naming convention, it must be specified explicitly at init. There is no mechanism to add or change the overlay URL after init in v1.

**Overlay sync failure is fatal after initial access.** Once the overlay has been cloned, any subsequent sync failure causes the apply to abort. Users with intermittent connectivity must re-initialize with `--no-overlay` to work offline.

## Decisions and Trade-offs

**Overlay discovery at init time, stored in `instance.json`.**
The overlay URL is discovered at `niwa init` time and stored in `.niwa/instance.json` as a workspace property. This means each workspace instance owns its overlay relationship independently — multiple workspaces on the same machine each have their own `instance.json` and therefore their own overlay URL. No machine-level config file entry is needed. The `niwa apply` command reads `instance.json` to determine overlay intent, and re-tries convention discovery if no URL is stored (to handle the "granted access later" case).

**No `niwa config set private` in v1.**
The explicit registration command is unnecessary when discovery is init-time and convention-based. The only gap is an overlay repo with a non-convention name that is created after the workspace is initialized — this is an acceptable v1 limitation. A workspace-level config command can address it in a future release without changing the core discovery model.

**Convention suffix is `-overlay`.**
The suffix describes the mechanism (layering) rather than the access model (private/public). Both the base and overlay repos can have any visibility. A team might overlay a public supplemental config on top of a public base; the suffix correctly describes what it is regardless of visibility.

**`niwa apply` re-tries convention discovery when no overlay is stored.**
If `instance.json` has no `overlay_url` and no `no_overlay` flag, `niwa apply` attempts convention discovery on every run. This is how a contributor automatically gains the overlay when they're later granted access — no user action required. The overhead is one git clone attempt per apply when no overlay exists, which fails fast (HTTP 404/403).

**Silent skip on first-time clone failure; hard error on explicit `--overlay`.**
Convention-based discovery silently skips on any failure because the user may simply not have access, and revealing that a discovery was attempted contradicts the goal of not leaking overlay existence. Explicit `--overlay` fails hard because the user has stated intent — a silent skip would leave them with an unexpectedly incomplete workspace.

**`workspace-overlay.toml`, not `workspace.toml`.**
A distinct filename clarifies that the overlay is additive, not a standalone workspace config. It also allows niwa to enforce different parsing rules (no `[workspace]` metadata, explicit repos required in sources).

**Base config precedence on structural collisions; append allowed for content.**
Groups and repo entries: base config wins entirely. Content is different — appending overlay context to a base repo's `CLAUDE.local.md` is additive augmentation, not structural override. The `overlay` field in content entries captures this distinction explicitly. Full content replacement (`source` on a base-config repo) remains an error.

**`niwa status` does not display overlay state.**
Any mention of the overlay in status output could reveal its existence to users who shouldn't know about it. Users who need to inspect overlay state can read `.niwa/instance.json` directly.

**Vault infrastructure belongs in the overlay, not the base config.**
The base config documents what secrets a workspace needs (`[env.secrets.required]`, `[env.secrets.recommended]`, `[env.secrets.optional]`). The overlay supplies how to obtain them (`[vault.provider]` + `[env.secrets]` vault:// references). This mirrors the separation between the personal overlay (global config) and the base config — each layer owns its vault provider and resolves its own secrets in isolation. The base config stays publishable without leaking vault project IDs or the names of secrets the team depends on. Teams without an overlay still get the required/recommended/optional declarations as documentation for manual secret setup.
