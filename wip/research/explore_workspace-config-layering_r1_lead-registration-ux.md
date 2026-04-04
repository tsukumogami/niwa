# Lead: Registration and discovery UX

## Findings

### Current GlobalConfig Structure

**File:** `internal/config/registry.go` (lines 12-26)

```go
type GlobalConfig struct {
    Global   GlobalSettings
    Registry map[string]RegistryEntry
}

type GlobalSettings struct {
    CloneProtocol string `toml:"clone_protocol,omitempty"`
}

type RegistryEntry struct {
    Source string
    Root   string
}
```

The global config already holds per-machine settings (`clone_protocol`) and a workspace registry. Personal config registration fits naturally here as a new field in `GlobalSettings` or a new top-level section.

**Storage:** `~/.config/niwa/config.toml` (XDG_CONFIG_HOME aware, lines 61-71). The file is created automatically if missing; registration can extend it safely.

### Current Init Command Flow

**File:** `internal/cli/init.go` - `runInit()` (lines 79-167)

Modes:
1. **Scaffold**: creates a local workspace.toml template
2. **Named**: resolves a named workspace from the registry
3. **Clone**: `--from <repo>` clones a config repo and registers it

Key steps:
1. Check for init conflicts
2. Load global config
3. Resolve mode, execute, verify workspace.toml parses
4. Register in global registry (skipped for scaffold)

**Missing:** No mechanism to register a personal config repo. No `--no-personal-config` flag exists yet.

### Existing Flag Patterns in Niwa

From surveying `internal/cli/`:

- `--allow-dirty` (apply): skips dirty-state check
- `--no-pull` (apply): skips git pull during apply
- `--force` (various): overrides safety checks
- `--from` (init): specifies config repo URL

The naming convention leans toward `--no-<thing>` for opt-outs and `--allow-<thing>` for safety-check bypasses.

### Personal Config Registration: Proposed UX

**Config key candidates:**

Option A: extend `GlobalSettings`
```toml
[global]
clone_protocol = "ssh"
personal_config_source = "https://github.com/user/niwa-personal"
personal_config_path = "/home/user/.config/niwa/personal"
```

Option B: separate top-level section
```toml
[personal_config]
source = "https://github.com/user/niwa-personal"
path = "/home/user/.config/niwa/personal"
```

Option B is cleaner -- personal config is structurally analogous to workspace entries and deserves its own section, not a crowded `GlobalSettings`.

**Registration command:**

No `niwa config` subcommand exists today. Options:
- `niwa config set personal <repo>` -- sets personal config source; niwa derives local path automatically
- `niwa personal init <repo>` -- a dedicated personal config subcommand
- `niwa init --personal <repo>` -- at init time (if registering at workspace setup makes sense)

The simplest path: `niwa config set personal <repo>` stores the source and a derived local path (`~/.config/niwa/personal/`) in `[personal_config]`. Niwa handles clone on first use.

**What niwa init does differently when personal config is registered:**
- If `[personal_config]` exists in global config and no `--skip-personal` flag: after workspace init, sync and merge personal config
- If `[personal_config]` is absent: no personal config step (existing behavior)
- If `--skip-personal` flag: skip personal config even if registered

### Flag Name Candidates for Opt-Out

Evaluating options against existing niwa naming conventions (`--no-pull`, `--allow-dirty`, `--force`):

| Name | Pros | Cons |
|------|------|------|
| `--no-personal-config` | Explicit, self-documenting | Verbose; "config" is redundant given context |
| `--skip-personal` | Concise, matches `--no-pull` verbosity | "skip" is slightly ambiguous (skip always vs skip this time) |
| `--without-personal` | Natural English | "without" not used elsewhere in niwa flags |
| `--local-only` | Communicates intent clearly | Doesn't name what's being skipped |
| `--no-overlay` | Abstract | Requires knowing "overlay" is the model |

**Recommendation:** `--skip-personal` -- concise, consistent with the `--no-pull` verbosity level, and "skip" clearly implies this is a one-time choice for this init. Working name `--no-personal-config` is fine as a placeholder; `--skip-personal` is the better final name.

## Implications

1. `GlobalSettings` is the right place for personal config registration, but as a new `[personal_config]` section rather than additional keys in `[global]`.
2. A `niwa config set personal <repo>` command is the minimal registration UX -- no new subcommand tree needed.
3. The opt-out flag at init should be `--skip-personal` (or retain `--no-personal-config` for explicitness if the team prefers verbosity).
4. Personal config path on disk can be derived automatically: `~/.config/niwa/personal/` (or `$XDG_CONFIG_HOME/niwa/personal/`).

## Surprises

- No `niwa config` subcommand exists at all today -- registration would require adding one.
- The init command has three modes but no general-purpose global config editing; adding personal config registration would be the first user-facing global config write command.
- `--no-pull` precedent supports `--no-personal-config` stylistically, but `--skip-personal` is more concise and equally clear.

## Open Questions

1. Should `niwa config set personal <repo>` also clone the repo on registration, or lazily on first apply?
2. Should there be a `niwa config unset personal` to remove registration?
3. Should personal config opt-out at init time persist in the workspace's instance state, or be a one-time flag?
4. Is `--skip-personal` the right name, or does the team prefer the more explicit `--no-personal-config`?

## Summary

The personal config registration belongs in a new `[personal_config]` section in `~/.config/niwa/config.toml`, with a new `niwa config set personal <repo>` command for registration. The init command gains a `--skip-personal` flag (working name: `--no-personal-config`) to opt out per workspace. Adding the registration command requires introducing a `niwa config` subcommand that doesn't exist today, which is the most significant new surface area.
