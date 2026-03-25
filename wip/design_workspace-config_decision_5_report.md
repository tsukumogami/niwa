# Decision 5: Per-Host Overrides and Channel Config

## Question

How should per-host overrides and channel config work in the TOML schema?

## Current System Analysis

The imperative installer handles per-host config through several interconnected mechanisms:

1. **channels.json** (in tools/env/, copied to ~/.tsuku/channels.json): Maps hostnames to bot tokens. Each host has named bots and pool bots. A "shared" section defines access control (allowFrom, groups) applied to all bots.

2. **Bot state directories** (~/.claude/channels/telegram/bots/<key>/): Each bot gets a .env with its token and an access.json with merged access rules. Created per-hostname at install time.

3. **Shell wrapper resolution** (tsuku-functions.sh): The `claude()` wrapper calls `__tsuku_resolve_bot`, which reads channels.json, matches `hostname -s` to a host entry, then maps the workspace name suffix (tsuku-3 -> key "3") to a specific bot token. It acquires a lock, stamps CLAUDE.local.md with the state directory path, and passes the token as an environment variable.

4. **Environment files** (workspace.env + repos/<name>.env): Merged into .local.env per repo. Contains API tokens, feature flags. Currently not host-aware, but the need exists (different API keys per machine).

The core pattern: shared config in a git-tracked file, secrets and host-specific values resolved at runtime to files outside the repo.

## Options Evaluated

### Option A: Separate host config file

workspace.toml (shared, committed) defines structure. Per-host overrides live in ~/.config/niwa/hosts/<hostname>.toml, completely outside the workspace.

**workspace.toml (committed):**
```toml
[workspace]
name = "tsuku"
org = "tsukumogami"

[channels.telegram]
access.allow_from = ["7902893668"]
access.groups = { "-1003723666197" = { require_mention = true } }
```

**~/.config/niwa/hosts/ryzen9.toml:**
```toml
[channels.telegram.bots]
1 = "8758431361:AAHHsx2I9..."
2 = "8667513242:AAFc2Q9Av..."
3 = "8790426367:AAFlmE-CF..."
4 = "8633178522:AAFZAGedP..."
```

**Tsukumogami case (3 machines):**
- workspace.toml defines the shared Telegram access rules (committed to git)
- ~/.config/niwa/hosts/ryzen9.toml has 4 bot tokens for ryzen9
- ~/.config/niwa/hosts/laptop.toml has 2 bot tokens for laptop
- ~/.config/niwa/hosts/macbook.toml has 3 bot tokens for macbook
- niwa reads workspace.toml, then overlays the matching host file based on `hostname -s`

**Pros:**
- Complete separation of secrets and shared config. Bot tokens never exist in or near the workspace directory.
- Host config survives workspace deletion and recreation (resettsuku). No re-setup needed.
- Multiple workspaces on the same host share host config automatically.
- Fits the XDG convention for user-level config.
- Host identity is the real hostname, which matches the existing channels.json pattern exactly.

**Cons:**
- Two file locations to understand: workspace.toml plus a system-level directory.
- Initial setup requires creating the host file manually or via `niwa init-host`.
- Host config must be provisioned separately on each machine (no single source of truth for "what bots does ryzen9 have?").
- Hostname as identifier can be fragile if machines get renamed (rare in practice).

### Option B: In-workspace local override (workspace.toml.local)

A gitignored file next to workspace.toml that merges on top of it.

**workspace.toml (committed):**
```toml
[workspace]
name = "tsuku"
org = "tsukumogami"

[channels.telegram]
access.allow_from = ["7902893668"]
access.groups = { "-1003723666197" = { require_mention = true } }
```

**workspace.toml.local (gitignored):**
```toml
[channels.telegram.bots]
1 = "8758431361:AAHHsx2I9..."
2 = "8667513242:AAFc2Q9Av..."
3 = "8790426367:AAFlmE-CF..."

[env]
GH_TOKEN = "ghp_xxxx"
```

**Tsukumogami case (3 machines):**
- workspace.toml committed with shared config
- Each machine has its own workspace.toml.local in each workspace instance
- ryzen9's tsuku-3/workspace.toml.local has bot 3's token
- After resettsuku, workspace.toml.local is destroyed and must be recreated

**Pros:**
- Single directory to look at. All config is "right there" next to workspace.toml.
- Follows the established .local pattern already used in the project (CLAUDE.local.md, settings.local.json, .local.env).
- Simple mental model: workspace.toml + workspace.toml.local = effective config.

**Cons:**
- Secrets live inside the workspace directory. Even gitignored, they can be accidentally copied, tarred, or exposed.
- Destroyed on workspace reset -- must be recreated for each workspace instance.
- No way to express "different bots for different hosts" from one file. Each workspace instance just has "my bots" with no host routing.
- Can't share bot pool config across workspace instances on the same host.

### Option C: Environment-based with ${VAR} references

workspace.toml references environment variables for host-specific values.

**workspace.toml (committed):**
```toml
[workspace]
name = "tsuku"
org = "tsukumogami"

[channels.telegram]
access.allow_from = ["7902893668"]
bot_token = "${TELEGRAM_BOT_TOKEN}"

[env]
GH_TOKEN = "${GH_TOKEN}"
```

**Tsukumogami case (3 machines):**
- workspace.toml committed with ${VAR} placeholders
- Each machine sets TELEGRAM_BOT_TOKEN in shell profile or direnv
- The shell wrapper or niwa resolves variables at runtime
- Per-workspace bot assignment requires additional logic (direnv per workspace, or a wrapper)

**Pros:**
- Familiar pattern for anyone using Docker, CI, or 12-factor apps.
- Secrets managed entirely outside niwa's domain (shell, direnv, vault, etc.).
- No niwa-specific host config files to learn.

**Cons:**
- Doesn't solve the multi-bot problem. The current system assigns different bots per workspace instance, not per host. A single TELEGRAM_BOT_TOKEN env var can't express "bot 1 for tsuku-1, bot 2 for tsuku-2, bot 3 for tsuku-3."
- Variable resolution adds parsing complexity (escaping, defaults, missing vars).
- Debugging is harder: "why is my bot wrong?" requires tracing env var sources through shell config, direnv, and workspace config.
- workspace.toml becomes less self-documenting -- you see placeholders, not structure.
- Go TOML libraries don't handle variable substitution natively; requires a pre-parse or post-parse expansion pass.

### Option D: Channel-specific config section with host routing in workspace.toml

All channel config including per-host bot assignment declared inline.

**workspace.toml (committed):**
```toml
[workspace]
name = "tsuku"
org = "tsukumogami"

[channels.telegram]
access.allow_from = ["7902893668"]
access.groups = { "-1003723666197" = { require_mention = true } }

[channels.telegram.hosts.ryzen9]
bots = { "1" = "8758431361:AAHHsx2I9...", "2" = "8667513242:AAFc2Q9Av...", "3" = "8790426367:AAFlmE-CF...", "4" = "8633178522:AAFZAGedP..." }

[channels.telegram.hosts.laptop]
bots = { "1" = "token1...", "2" = "token2..." }
```

**Tsukumogami case (3 machines):**
- All bot tokens for all hosts are in workspace.toml
- niwa resolves the right bot by matching hostname + workspace suffix (same as current shell wrapper)
- Single file defines the full channel topology

**Pros:**
- Everything in one place. The full bot topology is visible.
- No extra files or environment variables to manage.
- Matches the current channels.json structure almost 1:1.

**Cons:**
- **Secrets in a committed file.** Bot tokens are API credentials. This is the constraint that disqualifies D outright -- the task requirements say "per-host config must not leak into the shared workspace.toml (secrets concern)."
- Anyone with repo access sees all bot tokens for all machines.
- Rotating a token requires a commit visible in git history.

## Chosen Approach: Option A -- Separate host config file

### Rationale

Option A is the only approach that satisfies all stated constraints simultaneously. The deciding factors:

1. **Secrets isolation.** The hard constraint says per-host config must not leak into workspace.toml. Option A is the only option where secrets exist exclusively outside the workspace tree. Option B puts them in a gitignored file inside the workspace (fragile). Option D puts them in the committed file (disqualified). Option C pushes the problem to env vars without solving the multi-bot routing.

2. **Survives workspace lifecycle.** The tsukumogami workflow creates and destroys workspaces frequently (newtsuku/resettsuku). Host config at ~/.config/niwa/ persists across these operations. Option B's workspace.toml.local is destroyed every time, requiring re-setup.

3. **Solves the multi-bot assignment problem.** The host config file can declare a pool of bots. niwa (or the shell wrapper) assigns bots to workspace instances by suffix, exactly as the current system does. This mapping lives in one place per host, not scattered across workspace instances.

4. **Clean layering model.** The three layers are clear:
   - **workspace.toml** (committed): shared structure, access rules, non-secret defaults
   - **~/.config/niwa/hosts/<hostname>.toml** (per-machine): bot tokens, API keys, machine-specific paths
   - **Instance state** (~/.local/state/niwa/<workspace>/): runtime locks, bot assignments, ephemeral state

5. **XDG alignment.** Using ~/.config/niwa/ follows the XDG Base Directory spec that many Go CLI tools adopt. Users who already manage dotfiles per-machine (via chezmoi, yadm, etc.) can include the niwa host config in their dotfile management.

### Concrete Schema

**workspace.toml (committed):**
```toml
[workspace]
name = "tsuku"
org = "tsukumogami"

# Channel config: non-secret, shared across all hosts
[channels.telegram]
plugin = "telegram@claude-plugins-official"

[channels.telegram.access]
allow_from = ["7902893668"]

[channels.telegram.access.groups."-1003723666197"]
require_mention = true
```

**~/.config/niwa/hosts/ryzen9.toml:**
```toml
# Bot tokens for this host. Keys are workspace instance suffixes.
# tsuku-1 gets bot "1", tsuku-2 gets bot "2", etc.
[channels.telegram.bots]
"1" = "8758431361:AAHHsx2I9..."
"2" = "8667513242:AAFc2Q9Av..."
"3" = "8790426367:AAFlmE-CF..."
"4" = "8633178522:AAFZAGedP..."

# Host-specific environment overrides
[env]
GH_TOKEN = "ghp_xxxx"
```

**Resolution order at runtime:**
1. Parse workspace.toml for channel structure and access rules
2. Identify host via `hostname -s`
3. Load ~/.config/niwa/hosts/<hostname>.toml
4. Merge: host values overlay workspace values (host wins on conflict)
5. Resolve bot for this workspace instance by suffix key
6. Write runtime state (locks, stamped CLAUDE.local.md) to instance state dir

### Host Identification

Hosts are identified by the short hostname (`hostname -s`), matching the current channels.json convention. The host config filename IS the identifier, so there's no ambiguity: the file ryzen9.toml applies when `hostname -s` returns "ryzen9".

An explicit host name override is supported via environment variable for edge cases:
```bash
export NIWA_HOST=ryzen9  # override hostname detection
```

### Beyond Telegram: Generalized Per-Host Overrides

The host config file isn't Telegram-specific. Any value in workspace.toml can be overridden per-host:

```toml
# ~/.config/niwa/hosts/work-laptop.toml

# Stricter permission mode on shared work machines
[settings]
permission_mode = "strict"

# Different directory for workspace instances
[workspace]
instance_dir = "/data/workspaces"

[env]
GH_TOKEN = "ghp_work_token"
ANTHROPIC_API_KEY = "sk-ant-work..."
```

### Setup and Provisioning

For initial setup on a new host:
```bash
# Future: niwa generates a skeleton host config
niwa init-host

# Or manually create it
mkdir -p ~/.config/niwa/hosts
cat > ~/.config/niwa/hosts/$(hostname -s).toml << 'EOF'
[channels.telegram.bots]
"1" = "your-token-here"
EOF
```

For users who manage dotfiles across machines, the ~/.config/niwa/ directory is a natural addition to their dotfile repo (which already handles secrets via encryption or private repos).

### Go Types

```go
type HostConfig struct {
    Channels HostChannels          `toml:"channels"`
    Env      map[string]string     `toml:"env"`
    Settings map[string]string     `toml:"settings"`
    Workspace HostWorkspaceOverride `toml:"workspace"`
}

type HostChannels struct {
    Telegram HostTelegramConfig `toml:"telegram"`
}

type HostTelegramConfig struct {
    Bots map[string]string `toml:"bots"` // suffix -> token
}

type HostWorkspaceOverride struct {
    InstanceDir string `toml:"instance_dir,omitempty"`
}
```

### Edge Cases

- **No host config file**: niwa works without it. Channels that need host-specific tokens are simply not activated. A warning is logged.
- **Hostname change**: User renames the file. Since the filename is the identifier, this is a one-step fix.
- **Multiple workspaces sharing a bot pool**: The host config defines the pool once. Each workspace instance claims a bot by its suffix key. If two workspaces want bot "3", the lock mechanism (already implemented in the shell wrapper) prevents conflict.
- **CI/headless environments**: No host config needed. CI doesn't use Telegram channels. The host config is purely opt-in.

## Rejected Options

- **B (workspace.toml.local)**: Secrets inside the workspace tree, even if gitignored, are a liability. More critically, destroyed on workspace reset -- the operation that happens most often in this workflow. Having to re-create a secrets file after every resettsuku is a dealbreaker for usability.

- **C (Environment variables)**: Can't express the multi-bot-per-host mapping that the Telegram integration requires. A single env var per secret doesn't scale to "4 bots on ryzen9, 2 on laptop, 3 on macbook, with workspace-to-bot routing." Would require inventing a naming convention (TELEGRAM_BOT_1, TELEGRAM_BOT_2...) that duplicates what a structured config file does more clearly.

- **D (Inline host routing)**: Disqualified by the secrets constraint. Bot tokens in a committed file is a non-starter. Even if the repo is private, token rotation requires git commits, and any repo access grants access to all bot tokens for all machines.
