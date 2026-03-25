# Decision 6: Host x Workspace Override Handling

## Question

How should the per-host config handle host x workspace overrides?

## Problem

Decision 5 established that per-host config lives at `~/.config/niwa/hosts/<hostname>.toml`. This solves the HOST dimension cleanly -- different machines get different bot tokens, API keys, etc. But it doesn't address cases where the same host runs multiple workspaces that need different host-level config.

Real-world example on host "ryzen9":
- Workspace "tsuku" needs bots 1-4 assigned by instance suffix, with a specific GH_TOKEN for the tsukumogami GitHub org
- Workspace "my-project" might have its own set of bots and a different GH_TOKEN for a different GitHub org
- The current Decision 5 schema has no way to express this: `[channels.telegram.bots]` is flat, and `[env]` is flat

The existing imperative installer sidesteps this by assuming one workspace type per host (the bot key is the workspace instance suffix, not a workspace name + suffix). But niwa is designed to manage multiple distinct workspaces, so this gap needs addressing.

## Options Evaluated

### Option A: Workspace sections inside host config

The host config file gains `[workspaces.<name>]` sections that override host-level defaults for specific workspaces.

```toml
# ~/.config/niwa/hosts/ryzen9.toml

# Host-level defaults (apply to any workspace without a specific section)
[env]
GH_TOKEN = "ghp_default"

[channels.telegram.bots]
"1" = "default-bot-1"

# Per-workspace overrides on this host
[workspaces.tsuku.channels.telegram.bots]
"1" = "tsuku-bot-1"
"2" = "tsuku-bot-2"
"3" = "tsuku-bot-3"
"4" = "tsuku-bot-4"

[workspaces.tsuku.env]
GH_TOKEN = "ghp_tsukumogami_org"

[workspaces.my-project.channels.telegram.bots]
"1" = "myproj-bot-1"

[workspaces.my-project.env]
GH_TOKEN = "ghp_other_org"
```

**File count:**
- Common case (1 workspace): 1 file (ryzen9.toml). Workspace sections optional -- host-level defaults just work.
- Complex case (3 workspaces): 1 file. All overrides in workspace sections.

**Merge order:** workspace.toml -> host config (host-level) -> host config (workspace section). Each layer wins over the previous.

**Interaction with Decision 5:** Extends the existing host config file. Same location, same merge concept. The only structural change is the optional `[workspaces.*]` table.

**Pros:**
- Single file per host. All host-specific config for all workspaces is in one place.
- Common case (one workspace, or workspaces that share config) needs zero workspace sections -- just use host-level defaults.
- Easy to see the full picture for a host: open one file, see everything.
- Workspace name matching is straightforward: the `[workspace] name` from workspace.toml is the key.
- TOML parsing is standard -- `[workspaces.<name>]` is a regular nested table.

**Cons:**
- A host with many workspaces could make the file long.
- Merging three layers (workspace.toml, host defaults, host workspace section) is slightly more complex than two.
- Workspace names must be unique across all workspaces on a host (they should be anyway).

### Option B: Separate host x workspace files

A directory per host, with per-workspace override files alongside a default.

```
~/.config/niwa/hosts/
  ryzen9.toml              # host defaults (backward compatible)
  ryzen9/
    tsuku.toml             # host x workspace overrides
    my-project.toml        # host x workspace overrides
```

**File count:**
- Common case (1 workspace): 1 file (ryzen9.toml only, no directory needed).
- Complex case (3 workspaces): 4 files (1 host default + 3 workspace files).

**Merge order:** workspace.toml -> ryzen9.toml -> ryzen9/tsuku.toml.

**Interaction with Decision 5:** Requires adding directory-based lookup alongside the existing file-based lookup. The host config file ryzen9.toml coexists with a ryzen9/ directory, which is slightly unusual but valid on all filesystems.

**Pros:**
- Clean separation: each workspace's host overrides are in their own file.
- Individual workspace configs can be managed (permissions, backup) independently.
- No risk of one file getting unwieldy.
- Backward compatible: if no directory exists, only ryzen9.toml is read (pure Decision 5 behavior).

**Cons:**
- More files to manage. 3 workspaces means 4 files minimum.
- Having both ryzen9.toml (file) and ryzen9/ (directory) at the same path level is unusual and can confuse users.
- Must discover files in the directory -- adds directory scanning logic.
- Harder to get the full picture for a host: must read multiple files.

### Option C: Hyphenated host x workspace files

Flat file naming with a separator.

```
~/.config/niwa/hosts/
  ryzen9.toml              # host defaults
  ryzen9-tsuku.toml        # host x workspace
  ryzen9-my-project.toml   # host x workspace
```

**File count:**
- Common case (1 workspace): 1-2 files (host default + optional workspace file).
- Complex case (3 workspaces): 4 files.

**Merge order:** workspace.toml -> ryzen9.toml -> ryzen9-tsuku.toml.

**Interaction with Decision 5:** Only adds a naming convention for finding additional files. No structural changes to the host config format.

**Pros:**
- Flat directory, no subdirectories.
- Each file is independent and self-contained.
- Simple naming pattern.

**Cons:**
- Hyphen in the filename creates ambiguity: is "ryzen9-my-project" host "ryzen9" + workspace "my-project", or host "ryzen9-my" + workspace "project"? Hostnames and workspace names can both contain hyphens.
- Same file proliferation issue as Option B but without the organizational benefit of a directory.
- Listing all overrides for a host requires filename pattern matching.
- Ugly: `ryzen9-my-project.toml` reads awkwardly next to `ryzen9.toml`.

### Option D: Bot pool partitioning (no workspace dimension)

Don't add a workspace dimension to host config. Instead, declare the full bot pool in host config and partition it in workspace.toml.

```toml
# ~/.config/niwa/hosts/ryzen9.toml
[channels.telegram.bots]
"1" = "bot-1-token"
"2" = "bot-2-token"
"3" = "bot-3-token"
"4" = "bot-4-token"
"5" = "bot-5-token"
"6" = "bot-6-token"
```

```toml
# workspace.toml for workspace "tsuku"
[channels.telegram]
bot_range = [1, 4]
```

```toml
# workspace.toml for workspace "my-project"
[channels.telegram]
bot_range = [5, 6]
```

**File count:**
- Common case (1 workspace): 1 host file. workspace.toml optionally declares a range.
- Complex case (3 workspaces): 1 host file, but each workspace.toml must coordinate ranges to avoid overlap.

**Merge order:** workspace.toml -> host config. No third layer. workspace.toml's `bot_range` filters the host's bot pool.

**Interaction with Decision 5:** No change to host config structure. The workspace.toml schema adds a `bot_range` field.

**Pros:**
- Keeps host config simple -- it's just a flat pool of all bots on this machine.
- No new file types or naming conventions.
- Range-based partitioning is explicit and easy to reason about.

**Cons:**
- Doesn't solve the env var problem at all. Different GH_TOKENs per workspace on the same host can't be expressed this way -- env vars aren't range-partitionable.
- Ranges require cross-workspace coordination. Adding workspace C means checking what ranges A and B use. This coordination is error-prone and tedious.
- Bot numbering becomes global to the host rather than local to the workspace. Workspace "tsuku" can't call its first bot "1" if another workspace already claimed that range.
- The range concept is Telegram-specific. Other channels or env vars would need different partitioning mechanisms.
- If bot counts change (host gets more bots), all workspace.toml files may need range updates.

## Chosen Approach: Option A -- Workspace sections inside host config

### Rationale

Option A is the clear winner because it adds the workspace dimension with minimal structural disruption while keeping the common case simple.

**The common case stays unchanged.** A user with one workspace (or workspaces that share all host config) writes exactly what Decision 5 already specifies -- a flat host config file. The `[workspaces.*]` sections are entirely optional. This means Decision 5's design remains valid as-is for the typical user.

**The complex case is one file, not four.** Options B and C scatter host x workspace config across multiple files. A user managing 3 workspaces on ryzen9 would need to open 4 files to understand the full picture. Option A keeps it in one file. For the person who manages their machines, seeing "here's everything ryzen9 does" in a single file is the right granularity.

**It solves the general problem, not just Telegram.** Option D only addresses bot partitioning. But the motivating examples include env vars (different GH_TOKEN per workspace on the same host), which aren't partitionable by range. Option A handles both cases uniformly: the workspace section can override any host-level value, whether it's bots, env vars, settings, or anything else.

**Three-layer merge is straightforward.** The resolution order is:
1. workspace.toml (shared, committed)
2. Host config, host-level defaults (secrets, host-specific values)
3. Host config, workspace section (workspace-specific overrides for this host)

Each layer is a TOML table. Merging is deep merge with later layers winning. This is the same merge semantic Decision 5 already established, just with one more specific layer.

### Concrete Schema

**~/.config/niwa/hosts/ryzen9.toml (single workspace, common case):**
```toml
# No workspace sections needed -- everything applies to all workspaces
[channels.telegram.bots]
"1" = "8758431361:AAHHsx2I9..."
"2" = "8667513242:AAFc2Q9Av..."
"3" = "8790426367:AAFlmE-CF..."
"4" = "8633178522:AAFZAGedP..."

[env]
GH_TOKEN = "ghp_xxxx"
```

**~/.config/niwa/hosts/ryzen9.toml (multiple workspaces, complex case):**
```toml
# Host-level defaults: apply to any workspace without a specific override
[env]
ANTHROPIC_API_KEY = "sk-ant-shared..."

# Workspace-specific overrides
[workspaces.tsuku.channels.telegram.bots]
"1" = "tsuku-bot-1-token"
"2" = "tsuku-bot-2-token"
"3" = "tsuku-bot-3-token"
"4" = "tsuku-bot-4-token"

[workspaces.tsuku.env]
GH_TOKEN = "ghp_tsukumogami"

[workspaces.my-project.channels.telegram.bots]
"1" = "myproj-bot-1-token"

[workspaces.my-project.env]
GH_TOKEN = "ghp_other_org"
```

### Resolution Order

```
workspace.toml          # shared structure, access rules (committed)
  |
  v  merge (host defaults overlay workspace defaults)
host/<hostname>.toml    # host-level: top-level keys (env, channels, settings)
  |
  v  merge (workspace section overlays host defaults)
host/<hostname>.toml    # workspace section: [workspaces.<name>.*]
  |
  v
effective config        # fully resolved for this host + workspace
```

The workspace name used for matching is the `[workspace] name` field from workspace.toml.

### Go Types

Extending Decision 5's HostConfig:

```go
type HostConfig struct {
    Channels   HostChannels              `toml:"channels"`
    Env        map[string]string         `toml:"env"`
    Settings   map[string]string         `toml:"settings"`
    Workspace  HostWorkspaceOverride     `toml:"workspace"`
    Workspaces map[string]WorkspaceScope `toml:"workspaces"` // NEW
}

// WorkspaceScope holds per-workspace overrides within a host config.
// Its fields mirror HostConfig's top-level fields (minus Workspaces itself).
type WorkspaceScope struct {
    Channels  HostChannels          `toml:"channels"`
    Env       map[string]string     `toml:"env"`
    Settings  map[string]string     `toml:"settings"`
    Workspace HostWorkspaceOverride `toml:"workspace"`
}
```

The merge logic loads `HostConfig`, extracts the matching `WorkspaceScope` by workspace name, then deep-merges: host-level fields first, workspace scope fields on top.

### Edge Cases

- **No workspace section for this workspace:** Falls through to host-level defaults. This is the common case and works exactly like Decision 5's current design.
- **Workspace section exists but is sparse:** Only the specified keys override. An empty `[workspaces.tsuku]` section is a no-op.
- **Workspace name not in workspace.toml:** Resolution uses the literal `[workspace] name` value. If workspace.toml has `name = "tsuku"`, the section key is `tsuku`.
- **Host-level bots but workspace-level bots too:** The workspace section's `[channels.telegram.bots]` replaces (not merges with) the host-level bots. This prevents confusing partial overlays where some bots come from host defaults and others from the workspace section. A workspace that declares bots owns its full bot pool.

### Migration from Decision 5

No breaking changes. Decision 5's schema is a strict subset of Decision 6's schema. A host config file with no `[workspaces.*]` sections behaves identically to Decision 5. The only addition is the optional `workspaces` table in HostConfig.

## Rejected Options

- **B (Separate host x workspace files):** File proliferation without proportional benefit. 3 workspaces means 4 files on disk for one host. Having both `ryzen9.toml` and `ryzen9/` at the same directory level is confusing. The organizational separation isn't needed when the data fits comfortably in one file -- host config tends to be short (a few tokens and env vars per workspace).

- **C (Hyphenated filenames):** The hyphen separator creates parsing ambiguity since both hostnames and workspace names can contain hyphens. `work-laptop-my-project.toml` -- where does the hostname end? Using a different separator (underscore, dot) would work but feels arbitrary. Same file proliferation problem as B.

- **D (Bot pool partitioning):** Only solves Telegram bot assignment, not the general host x workspace override problem. Can't handle per-workspace env vars at all. Requires cross-workspace coordination of ranges, which is fragile and tedious. The concept doesn't generalize to non-range-based config.
