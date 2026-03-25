# Decision 4: Hooks, Settings, and Environment Distribution

## Context

The current tools repo installer (`install.sh`) distributes three types of
configuration to each repo:

1. **Hooks**: Shell scripts (`gate-online.sh`, `workflow-continue.sh`) copied to
   each repo's `.claude/hooks/`, then referenced by absolute path in settings.
2. **Settings**: `settings.local.json` generated per-repo containing permission
   mode (`bypassPermissions`), hook registrations, and optionally `GH_TOKEN`.
3. **Environment**: `workspace.env` merged with per-repo `env/repos/<name>.env`
   (overlay semantics, repo wins), output as `.local.env` in each repo root.

Today, every repo gets identical hooks and identical settings (the only
variation is whether `GH_TOKEN` is present). Per-repo env overrides exist in
the schema but no repo currently uses them. The distribution is uniform.

## Requirements

- Must not modify tracked files (only `.local` files and `.claude/` contents)
- Hook scripts referenced by path, not embedded
- Secrets need special handling (file permissions, not plaintext in TOML)
- Schema must be parseable in v0.1 even if generation logic ships in v0.2
- Convention over configuration: the common case should require zero config

## Options Evaluated

### Option A: Workspace-level sections with per-repo overrides

```toml
[hooks]
pre_tool_use = ["hooks/gate-online.sh"]
stop = ["hooks/workflow-continue.sh"]

[settings]
permissions = "bypass"

[env]
TAVILY_API_KEY = "tvly-..."
BRAVE_API_KEY = "BSA..."

[repos.niwa.env]
EXTRA_VAR = "only-for-niwa"

[repos.vision.hooks]
pre_tool_use = ["hooks/gate-online.sh", "hooks/extra-gate.sh"]
```

**Tsukumogami mapping**: Workspace-level `[hooks]` and `[env]` cover the 90%
case (all repos get the same config). Per-repo blocks override when needed.

**Pros**: Directly mirrors the current installer's workspace + per-repo overlay
model. Familiar structure. Per-repo overrides are optional and additive.

**Cons**: Secrets in plaintext TOML. Hook paths and env vars live in separate
sections, making the full "what does repo X get?" picture require mental
merging. The `[repos.X.hooks]` override semantics need clarification (replace
vs. extend).

### Option B: Unified [claude] section

```toml
[claude]
permissions = "bypass"
pre_tool_use_hooks = ["hooks/gate-online.sh"]
stop_hooks = ["hooks/workflow-continue.sh"]
env = { TAVILY_API_KEY = "tvly-...", BRAVE_API_KEY = "BSA..." }

[repos.niwa.claude]
env = { EXTRA_VAR = "only-for-niwa" }
```

**Tsukumogami mapping**: Everything Claude Code needs lives under `[claude]`.
Per-repo overrides under `[repos.X.claude]`.

**Pros**: Groups all Claude Code configuration as a single unit. Clear that
this section drives `settings.local.json` generation. Easy to explain: "the
`[claude]` section becomes your settings file."

**Cons**: Mixes hooks (which are file references) with env vars (which are
key-value pairs) and permissions (which are an enum). The `[claude]` name may
conflict if Claude Code itself ever adopts workspace-level config. Env vars
that aren't Claude-specific (used by hooks or MCP servers) feel misplaced.

### Option C: Profile-based

```toml
[profiles.standard]
permissions = "bypass"
hooks = ["hooks/gate-online.sh", "hooks/workflow-continue.sh"]
env_file = "env/workspace.env"

[profiles.restricted]
permissions = "ask"
hooks = ["hooks/gate-online.sh"]
env_file = "env/restricted.env"

[repos.niwa]
profile = "standard"

[repos.vision]
profile = "restricted"
```

**Tsukumogami mapping**: Define "standard" profile matching today's uniform
config. All repos reference it. If a repo needs different settings, create a
new profile.

**Pros**: Clean separation. Adding a new config variant doesn't duplicate
inline values. Scales well if you need 2-3 distinct configurations.

**Cons**: Indirection. Users must look up the profile to understand what a repo
gets. Overkill for the current case where every repo uses identical config.
Profile inheritance (if a profile extends another) adds complexity. Doesn't
handle fine-grained per-repo env overrides without falling back to Option A
patterns anyway.

### Option D: Workspace-level only, no per-repo overrides

```toml
[hooks]
pre_tool_use = ["hooks/gate-online.sh"]
stop = ["hooks/workflow-continue.sh"]

[settings]
permissions = "bypass"

[env]
TAVILY_API_KEY = "tvly-..."
BRAVE_API_KEY = "BSA..."
```

**Tsukumogami mapping**: Direct 1:1 with current reality (every repo gets
identical config, no per-repo overrides exist).

**Pros**: Simplest possible schema. No merge semantics to implement or
document. Matches the actual current state perfectly.

**Cons**: No escape hatch. When the first per-repo override is needed, the
schema must change. Contradicts the constraint that v0.1 schema should
accommodate v0.2 features.

## Analysis

### What the current system actually does

Looking at `install.sh`, the distribution is strikingly uniform:

- **Hooks**: Every repo gets the exact same two scripts. No per-repo variation.
- **Settings**: Every repo gets `bypassPermissions` + the same hooks + the same
  `GH_TOKEN`. The only variance is presence/absence of `GH_TOKEN`, which is
  binary and workspace-wide.
- **Environment**: `workspace.env` is copied verbatim to every repo. The
  `env/repos/<name>.env` overlay mechanism exists but has zero users.

This means the workspace-level-only model (Option D) matches today perfectly.
But the question is about forward compatibility.

### Secrets handling

The current `workspace.env` contains API keys and tokens in plaintext. This is
acceptable for a private tools repo but not for a TOML config that might be
committed to a public repo.

Practical approach: env files (`.env` format) handle secrets. The TOML config
references env files by path rather than embedding secret values. This keeps
secrets out of the config file and in `.local` files that are gitignored.

```toml
[env]
files = ["env/workspace.env"]
# inline values are for non-secret, non-sensitive config only
vars = { LOG_LEVEL = "debug" }
```

### Should niwa generate settings.local.json?

Yes. The current installer already generates it, and the content is derived
from declarative inputs (permission mode, hook paths, env vars). niwa should
own this generation. The alternative -- expecting users to hand-write
`settings.local.json` -- defeats the purpose of a declarative workspace
manager.

### Merge semantics

For env vars, overlay (repo wins) is the right default. It's what the current
installer implements, it's what users expect from layered config, and it's
what every similar system does (Docker Compose, Terraform, etc.).

For hooks, **extend** (workspace hooks + repo hooks) is safer than replace.
If a repo needs to remove a workspace hook, it can use an explicit
`hooks_exclude` list. But replacing silently is a footgun.

### Override granularity matters less than the escape hatch

The current workspace has zero per-repo overrides. The first one might be
months away. What matters is that the schema has a place for it, not that the
implementation handles it in v0.1.

## Decision

**Option A: Workspace-level sections with per-repo overrides**, with two
refinements:

1. **Secrets via env file references**, not inline values in TOML
2. **Hooks section uses extend semantics** (repo hooks are appended to
   workspace hooks, with an explicit exclude mechanism if needed)

### Chosen schema

```toml
# Workspace-level hooks (applied to all repos)
[hooks]
pre_tool_use = ["hooks/gate-online.sh"]   # paths relative to hooks source dir
stop = ["hooks/workflow-continue.sh"]

# Claude Code settings generation
[settings]
permissions = "bypass"    # "bypass", "ask", or "default"

# Environment distribution
[env]
files = ["env/workspace.env"]             # .env files, merged in order
vars = { LOG_LEVEL = "debug" }            # inline non-secret values

# Per-repo overrides (optional)
[repos.vision.env]
files = ["env/workspace.env", "env/repos/vision.env"]  # full file list replaces workspace default
vars = { EXTRA = "value" }                              # merged with workspace vars (repo wins)

[repos.vision.hooks]
stop = ["hooks/workflow-continue.sh", "hooks/extra-stop.sh"]  # extends workspace hooks

[repos.vision.settings]
permissions = "ask"       # override for this repo only
```

### How tsukumogami maps to this

```toml
[hooks]
pre_tool_use = ["hooks/gate-online.sh"]
stop = ["hooks/workflow-continue.sh"]

[settings]
permissions = "bypass"

[env]
files = ["env/workspace.env"]
```

Six repos, zero per-repo overrides. The entire hooks/settings/env config is 9
lines.

### Generation outputs

For each repo, `niwa sync` will generate:

| Output | Location | Content |
|--------|----------|---------|
| `settings.local.json` | `<repo>/.claude/settings.local.json` | Permissions + hooks + env from settings.env |
| `.local.env` | `<repo>/.local.env` | Merged env file contents + inline vars |
| Hook scripts | `<repo>/.claude/hooks/` | Copied from source paths |

### v0.1 vs v0.2 boundary

- **v0.1**: Schema is defined. Parser reads `[hooks]`, `[settings]`, `[env]`,
  and `[repos.X.hooks]` / `[repos.X.settings]` / `[repos.X.env]`. No
  generation logic -- just validation that the schema parses.
- **v0.2**: `niwa sync` generates `settings.local.json`, copies hooks, merges
  env files. Per-repo overrides are implemented.

## Summary

- **status**: complete
- **chosen**: A -- Workspace-level sections with per-repo overrides
- **confidence**: high
- **rationale**: Directly mirrors the proven imperative model with minimal abstraction. The schema accommodates per-repo overrides without requiring them, satisfying both current simplicity and forward compatibility.
- **assumptions**:
  - Secrets will remain in `.env` files, not in `workspace.toml`
  - Every repo continues to need `settings.local.json` generation (Claude Code won't absorb this)
  - Hook extend semantics (append, not replace) match expected usage patterns
  - The current uniform distribution pattern will remain the 90% case
- **rejected**:
  - B (unified `[claude]` section): Mixes concerns without clear benefit. The `[claude]` namespace risks future collision with Claude Code's own config.
  - C (profile-based): Indirection without payoff. Zero repos need a different profile today, and when one does, Option A's per-repo overrides are more precise than swapping an entire profile.
  - D (workspace-only): Too rigid. Violates the constraint that v0.1 schema must accommodate v0.2 per-repo overrides.
