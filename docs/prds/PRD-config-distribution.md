---
status: Accepted
problem: |
  niwa sets up workspace structure and CLAUDE.md files but doesn't distribute
  Claude Code operational configuration. Hooks, settings, and environment
  files must still be configured manually per repo. A 5-repo workspace means
  5x the setup, and when a hook changes, you update 5 repos by hand.
goals: |
  After niwa apply, every repo in the workspace has the right hooks installed,
  the right settings.local.json generated, and the right .local.env merged --
  all from a single declarative config. Changing a hook script and re-running
  apply updates every repo.
---

# PRD: Config distribution

## Status

Accepted

## Problem statement

niwa manages workspace structure (repos, groups, directories) and CLAUDE.md context hierarchy. But Claude Code's operational configuration -- hooks that gate tool use, settings that control permissions, environment variables that configure integrations -- must still be set up manually in each repo.

Today's workaround is a 700-line bash installer that copies hook scripts, generates settings.local.json, and merges .env files into each repo. This works for one org but is hardcoded and fragile. niwa's config schema already declares hooks, settings, and env sections, and the merge logic for per-repo overrides exists. But the apply pipeline doesn't write any of it to disk.

The gap is concrete: the tsukumogami project can't adopt niwa until this works. The gap analysis identified these three capabilities as the blocking items for replacing install.sh.

## Goals

1. **Hooks distribution.** Declare hook scripts in workspace.toml. `niwa apply` copies them to `.claude/hooks/{event}/` in each repo, makes them executable. Changing a hook and re-running apply updates every repo.

2. **Settings generation.** Declare Claude Code settings (permissions, hook references) in workspace.toml. `niwa apply` generates `.claude/settings.local.json` in each repo with the merged configuration. Hook references point to the installed hook scripts.

3. **Environment file distribution.** Declare env files and inline variables in workspace.toml. `niwa apply` merges them into `.local.env` in each repo. Workspace-level defaults, per-repo overrides.

4. **Per-repo overrides.** Any repo can override workspace-level hooks (extend), settings (replace), and env (replace vars, append files). The `[repos.<name>]` section in workspace.toml controls this.

5. **Extensibility.** The distribution mechanism should accommodate future types (plugins, scripts, extensions) without requiring changes to the core apply pipeline.

## User stories

**US1: Developer setting up Claude Code hooks across repos.**
I declare `pre_tool_use = ["hooks/gate-online.sh"]` once in workspace.toml. After `niwa apply`, every repo has the script at `.claude/hooks/pre_tool_use/gate-online.sh` and it's executable. I don't touch individual repos.

**US2: Developer configuring Claude Code permissions.**
I set `permissions = "bypass"` in workspace.toml's `[settings]` section. One repo needs `"ask"` instead, so I add `[repos.that-repo.settings] permissions = "ask"`. After apply, that repo gets "ask" and the rest get "bypass".

**US3: Developer distributing environment variables.**
I have a `workspace.env` file with shared config (API URLs, log levels). One repo needs an extra variable. I add `[repos.that-repo.env] vars = { EXTRA = "value" }`. After apply, that repo's `.local.env` has everything from workspace.env plus the extra variable.

**US4: Developer updating a hook script.**
I edit `hooks/gate-online.sh` in `.niwa/`. I run `niwa apply`. Every repo gets the updated script. `niwa status` shows which repos had their hooks drifted (manually edited) before the update.

**US5: Team lead onboarding a new developer.**
A new developer runs `niwa init --from org/config && niwa create`. They get the same hooks, settings, and env as everyone else. No separate apply step -- create runs the full pipeline including distribution.

## Examples

### Config directory (what you author in .niwa/)

```
.niwa/
  workspace.toml
  claude/
    workspace.md
    repos/api.md
  hooks/
    gate-online.sh              # pre_tool_use hook script
    workflow-continue.sh        # stop hook script
  env/
    workspace.env               # shared env vars (API_URL=..., LOG_LEVEL=...)
    repos/api.env               # api-repo-specific vars (DB_HOST=...)
```

### workspace.toml config

```toml
[workspace]
name = "my-project"
content_dir = "claude"

[[sources]]
org = "my-org"

[groups.public]
visibility = "public"

# --- Hooks: scripts distributed to .claude/hooks/ in each repo ---

[hooks]
pre_tool_use = ["hooks/gate-online.sh"]
stop = ["hooks/workflow-continue.sh"]

# --- Settings: generates .claude/settings.local.json per repo ---

[settings]
permissions = "bypass"

# --- Env: merges into .local.env per repo ---

[env]
files = ["env/workspace.env"]
vars = { LOG_LEVEL = "info" }

# --- Per-repo overrides ---

[repos.api]
scope = "tactical"

[repos.api.settings]
permissions = "ask"              # api repo uses ask instead of bypass

[repos.api.env]
files = ["env/repos/api.env"]    # appended after workspace.env
vars = { LOG_LEVEL = "debug" }   # overrides workspace LOG_LEVEL for this repo

[repos.".github"]
claude = false                   # skip all Claude Code config for this repo
```

### After `niwa create` -- what each repo looks like

**Standard repo (e.g., `web-app`):**

```
my-project/public/web-app/
  .claude/
    hooks/
      pre_tool_use/
        gate-online.sh           # copied from .niwa/hooks/, chmod +x
      stop/
        workflow-continue.sh     # copied from .niwa/hooks/, chmod +x
    settings.local.json          # generated (see below)
  .local.env                     # merged from workspace.env + inline vars
  CLAUDE.local.md                # existing content installation
```

`.claude/settings.local.json`:
```json
{
  "permissions": {
    "defaultMode": "bypassPermissions"
  },
  "hooks": {
    "PreToolUse": [
      {
        "type": "command",
        "command": ".claude/hooks/pre_tool_use/gate-online.sh"
      }
    ],
    "Stop": [
      {
        "type": "command",
        "command": ".claude/hooks/stop/workflow-continue.sh"
      }
    ]
  }
}
```

`.local.env`:
```
# Generated by niwa - do not edit manually
API_URL=https://api.example.com
LOG_LEVEL=info
```

**Overridden repo (api):**

```
my-project/public/api/
  .claude/
    hooks/
      pre_tool_use/
        gate-online.sh           # same hooks as other repos
      stop/
        workflow-continue.sh
    settings.local.json          # permissions = "ask" (overridden)
  .local.env                     # workspace.env + api.env + overridden vars
  CLAUDE.local.md
```

`.claude/settings.local.json`:
```json
{
  "permissions": {
    "defaultMode": "askPermissions"
  },
  "hooks": {
    "PreToolUse": [
      {
        "type": "command",
        "command": ".claude/hooks/pre_tool_use/gate-online.sh"
      }
    ],
    "Stop": [
      {
        "type": "command",
        "command": ".claude/hooks/stop/workflow-continue.sh"
      }
    ]
  }
}
```

`.local.env`:
```
# Generated by niwa - do not edit manually
API_URL=https://api.example.com
LOG_LEVEL=debug
DB_HOST=localhost
```

Note: `LOG_LEVEL` is `debug` (per-repo override) and `DB_HOST` comes from `api.env`.

**Skipped repo (.github with `claude = false`):**

```
my-project/public/.github/
  # No .claude/ directory
  # No .local.env
  # No CLAUDE.local.md
  # Just the cloned repo contents
```

## Requirements

### Functional

**R1: Hook installation.** For each entry in `[hooks]`, niwa copies the referenced script from the config directory (`.niwa/`) to `{repoDir}/.claude/hooks/{eventName}/{scriptName}` and sets executable permissions (0755). The `.claude/hooks/` directory is created if missing.

**R2: Hook source validation.** Hook script paths are relative to the config directory. Paths containing `..` or absolute components are rejected. Source files must exist.

**R3: Settings generation.** niwa generates `{repoDir}/.claude/settings.local.json` from the merged `[settings]` config. The JSON includes the permissions field and references to installed hook scripts (using relative paths from the repo root).

**R4: Settings format.** The generated `settings.local.json` must match Claude Code's expected schema. At minimum: `permissions.defaultMode` and hook event arrays referencing installed script paths.

**R5: Env file merging.** niwa reads env source files referenced in `[env].files` (relative to config directory), parses KEY=VALUE lines (ignoring comments and blanks), overlays inline `[env].vars`, and writes the merged result to `{repoDir}/.local.env`.

**R6: Env merge semantics.** When workspace-level and per-repo env configs are both present: file lists are concatenated (repo files processed after workspace files, so repo values override for same key), inline vars are merged (repo wins for same key).

**R7: Per-repo hook overrides.** `[repos.<name>.hooks]` entries extend (append to) workspace-level hooks. A repo can add hooks but not remove workspace-level ones.

**R8: Per-repo settings overrides.** `[repos.<name>.settings]` entries replace workspace-level settings on a per-key basis. A repo setting of `permissions = "ask"` overrides workspace `permissions = "bypass"`.

**R9: Per-repo env overrides.** `[repos.<name>.env]` entries: `files` appends to workspace file list, `vars` overrides workspace vars for same key.

**R10: Claude skip.** Repos with `claude = false` in `[repos.<name>]` skip all distribution (hooks, settings, env) in addition to skipping CLAUDE.local.md (existing behavior).

**R11: Managed file tracking.** All files written by distribution (hook scripts, settings.local.json, .local.env) are tracked in `.niwa/instance.json` with SHA-256 hashes, the same as CLAUDE.md files. Drift detection and cleanup on re-apply work for distributed files.

**R12: Idempotent distribution.** Running `niwa apply` twice with no config changes produces identical files. Removing a hook from config and re-running apply removes the installed script and its reference from settings.local.json.

**R13: No secrets in workspace.toml.** Environment values in workspace.toml are configuration (API URLs, log levels, feature flags), not secrets. Secrets should live in .env files referenced by path, which are typically gitignored. niwa does not provide special secret handling.

### Non-functional

**R14: Extensible distribution mechanism.** The apply pipeline's distribution step should support adding new distribution types (plugins, scripts, extensions) without modifying the core pipeline loop. Each distribution type owns its config reading, merging, and file writing.

**R15: Distribution ordering.** Hooks are installed before settings are generated, because settings.local.json references installed hook paths. Env distribution is independent of both.

## Acceptance criteria

- [ ] `niwa apply` with `[hooks] pre_tool_use = ["hooks/gate.sh"]` copies the script to `.claude/hooks/pre_tool_use/gate.sh` in each repo
- [ ] Installed hook scripts are executable (mode 0755)
- [ ] Hook source paths with `..` are rejected with a clear error
- [ ] `niwa apply` with `[settings] permissions = "bypass"` generates `.claude/settings.local.json` in each repo
- [ ] Generated settings.local.json includes hook references matching installed hook paths
- [ ] `niwa apply` with `[env] files = ["env/workspace.env"]` writes `.local.env` in each repo with merged content
- [ ] `[env] vars = { KEY = "value" }` adds inline vars to .local.env
- [ ] Per-repo hook override extends workspace hooks (both scripts installed)
- [ ] Per-repo settings override replaces on per-key basis
- [ ] Per-repo env vars override workspace vars for same key
- [ ] Repo with `claude = false` gets no hooks, settings, or env files
- [ ] Distributed files appear in `.niwa/instance.json` managed_files with hashes
- [ ] `niwa status` shows drift for manually edited hook scripts or settings
- [ ] Removing a hook from config and re-running apply deletes the installed script
- [ ] Running apply twice with no changes produces no file modifications

## Out of scope

- **Plugin installation** (`claude plugin install`). Separate feature (F11), different mechanism (external CLI calls).
- **Channel/Telegram configuration.** Host-specific, involves secrets, out of scope for workspace-level config.
- **Secret management.** No special handling for sensitive values beyond "put them in .env files, don't inline in TOML."
- **Workspace scripts.** Copying arbitrary scripts to .local/bin/ -- separate feature, not Claude Code config.
- **`${VAR}` expansion in env files.** The roadmap mentions this but it adds complexity. Defer until a real use case demands it.
