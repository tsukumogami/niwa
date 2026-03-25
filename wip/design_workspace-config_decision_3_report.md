# Decision 3: Multi-Instance Workspace Config and State Model

## Question

How should multi-instance workspaces work in the config and state model?

## Context

The PRD (R3, D8) establishes that niwa supports multiple instances from one workspace root. A workspace root contains `workspace.toml` and content files. Instances are subdirectories: first gets the config name (`tsuku/`), subsequent ones are numbered (`tsuku-2/`) or named (`tsuku-hotfix/`). Each instance has independent repo clones and a `.niwa/` marker directory.

The global registry at `~/.config/niwa/config.toml` maps workspace config names to their GitHub source repos (R1, D12). It's deliberately minimal -- "just a config file," not a workspace container.

Key PRD constraints:
- `niwa apply` inside an instance targets the current one (context-aware)
- `niwa status` at workspace root shows all instances
- `niwa reset` and `niwa destroy` operate on specific instances
- Detached workspaces (workspace.toml in-place, no root/instance split) are single-instance and unregistered
- The state file tracks what niwa manages, enabling drift detection (R10)
- The `.niwa/` marker in each instance stores instance metadata (R3)

## Options

### Option A: Registry-Centric

All instance state lives in the global registry.

**File layout:**
```
~/.config/niwa/config.toml          # Registry + all instance state
tsuku-root/
  workspace.toml
  content/
  tsuku/                             # Instance (no .niwa/ beyond a marker)
    public/
    private/
  tsuku-2/
    public/
    private/
```

**~/.config/niwa/config.toml schema:**
```toml
[global]
clone_protocol = "ssh"

[registry.tsuku]
source = "tsukumogami/niwa-tsuku-config"

[[registry.tsuku.instances]]
name = "tsuku"
path = "/home/user/dev/tsuku-root/tsuku"
created = 2026-03-25T10:00:00Z
instance_number = 1

[registry.tsuku.instances.repos]
"tsukumogami/tsuku" = { cloned = true, claude_local_written = true }
"tsukumogami/niwa" = { cloned = true, claude_local_written = true }

[[registry.tsuku.instances]]
name = "tsuku-2"
path = "/home/user/dev/tsuku-root/tsuku-2"
created = 2026-03-25T11:00:00Z
instance_number = 2
```

**Evaluation:**
- Centralizes everything, making `niwa list` trivial
- Violates D8: "debuggability suffers" when state is far from workspace
- Single file becomes a bottleneck and corruption risk as instances grow
- Context-aware commands (`niwa apply` with no args) still need to walk up to find the root, then query the registry -- no simpler than reading local state
- Detached workspaces don't fit cleanly (they aren't registered)
- TOML gets unwieldy for per-repo state across many instances

### Option B: Instance-Centric

Each instance is self-contained. The registry is a bare name-to-path index.

**File layout:**
```
~/.config/niwa/config.toml
tsuku-root/
  workspace.toml
  content/
  tsuku/
    .niwa/
      instance.json                  # All instance state
    public/
    private/
  tsuku-2/
    .niwa/
      instance.json
    public/
    private/
```

**~/.config/niwa/config.toml schema:**
```toml
[global]
clone_protocol = "ssh"

[registry.tsuku]
source = "tsukumogami/niwa-tsuku-config"
root = "/home/user/dev/tsuku-root"
```

**instance.json schema:**
```json
{
  "config_name": "tsuku",
  "instance_name": "tsuku",
  "instance_number": 1,
  "root": "/home/user/dev/tsuku-root",
  "created": "2026-03-25T10:00:00Z",
  "managed_files": [
    { "path": "CLAUDE.md", "hash": "sha256:abc123", "generated": "2026-03-25T10:00:00Z" },
    { "path": "public/CLAUDE.md", "hash": "sha256:def456", "generated": "2026-03-25T10:00:00Z" },
    { "path": "public/niwa/CLAUDE.local.md", "hash": "sha256:789abc", "generated": "2026-03-25T10:00:00Z" }
  ],
  "repos": {
    "public/tsuku": { "url": "git@github.com:tsukumogami/tsuku.git", "cloned": true },
    "public/niwa": { "url": "git@github.com:tsukumogami/niwa.git", "cloned": true }
  }
}
```

**Evaluation:**
- Instance state lives where the instance lives -- good debuggability
- Context-aware commands just walk up to `.niwa/` (like git walks to `.git/`)
- `niwa status` at workspace root must scan subdirectories for `.niwa/` dirs to enumerate instances
- Registry doesn't know about individual instances, only roots
- Detached workspaces work naturally: `.niwa/instance.json` in the workspace directory itself
- Clean separation: registry knows where configs come from, instances know their own state

### Option C: Hybrid

Registry maps names to root paths. Each instance has `.niwa/state.json` for local state.

**File layout:**
```
~/.config/niwa/config.toml
tsuku-root/
  workspace.toml
  content/
  .niwa/
    instances.json                   # Root-level instance index
  tsuku/
    .niwa/
      state.json                     # Per-instance state
    public/
    private/
  tsuku-2/
    .niwa/
      state.json
    public/
    private/
```

**~/.config/niwa/config.toml schema:**
```toml
[global]
clone_protocol = "ssh"

[registry.tsuku]
source = "tsukumogami/niwa-tsuku-config"
root = "/home/user/dev/tsuku-root"
```

**Root-level .niwa/instances.json schema:**
```json
{
  "config_name": "tsuku",
  "next_instance_number": 3,
  "instances": [
    { "name": "tsuku", "number": 1, "created": "2026-03-25T10:00:00Z" },
    { "name": "tsuku-2", "number": 2, "created": "2026-03-25T11:00:00Z" }
  ]
}
```

**Per-instance .niwa/state.json schema:**
```json
{
  "config_name": "tsuku",
  "instance_name": "tsuku",
  "instance_number": 1,
  "root": "/home/user/dev/tsuku-root",
  "created": "2026-03-25T10:00:00Z",
  "last_applied": "2026-03-25T10:05:00Z",
  "managed_files": [
    { "path": "CLAUDE.md", "hash": "sha256:abc123", "generated": "2026-03-25T10:00:00Z" },
    { "path": "public/CLAUDE.md", "hash": "sha256:def456", "generated": "2026-03-25T10:00:00Z" },
    { "path": "public/niwa/CLAUDE.local.md", "hash": "sha256:789abc", "generated": "2026-03-25T10:00:00Z" }
  ],
  "repos": {
    "public/tsuku": { "url": "git@github.com:tsukumogami/tsuku.git", "cloned": true },
    "public/niwa": { "url": "git@github.com:tsukumogami/niwa.git", "cloned": true }
  }
}
```

**Evaluation:**
- Root-level index answers "what instances exist" without scanning directories
- Per-instance state keeps detailed info local (good debuggability)
- `next_instance_number` in the root index prevents number collisions during `niwa create`
- Three files to keep in sync (registry, root index, instance state) -- more moving parts
- Context-aware commands walk up to `.niwa/state.json` in the instance, same as Option B
- Detached workspaces: `.niwa/state.json` in the workspace dir, no root index needed (single-instance)
- The root-level `.niwa/` adds a new concept (root marker vs instance marker)

### Option D: No Registry

Workspaces are self-contained. niwa discovers them by filesystem scanning.

**File layout:**
```
tsuku-root/
  workspace.toml
  content/
  tsuku/
    .niwa/
      instance.json
    public/
    private/
  tsuku-2/
    .niwa/
      instance.json
    public/
    private/
```

**No global config file.**

**Evaluation:**
- Directly contradicts R1 (global registry) and R7 (`niwa init <name>` using registry)
- `niwa init tsuku` can't resolve a name to a GitHub source without the registry
- No path to remote config update (R7a)
- Simplest model, but doesn't meet v0.1 requirements

**Eliminated.** R1 and R7 explicitly require a registry.

## Analysis

Options A and D are eliminated: A puts too much in the global config (violating D8's rationale), and D doesn't meet R1/R7.

The real question is Option B vs Option C: does the workspace root need its own `.niwa/` directory with an instance index, or can the root be stateless?

**The case for the root-level index (Option C):**
- `niwa create` needs to know the next available instance number. Without a root index, it must scan directories, parse `.niwa/` markers, and find the highest number. With the index, it reads one file.
- `niwa status` at the workspace root needs to list instances. Scanning works but is slower and can't distinguish niwa instances from other directories.
- `niwa destroy` needs to update the available-numbers tracking.

**The case against the root-level index (Option B):**
- One fewer file to keep in sync.
- Scanning is reliable and fast (workspace roots have few subdirectories).
- The root directory is conceptually a config source, not a managed artifact. Adding `.niwa/` there muddies the separation.

The scanning concern is real but minor -- workspace roots will have 1-10 instance directories, not thousands. However, the `next_instance_number` tracking is important: if instance `tsuku-3` is destroyed and `tsuku-4` exists, the next `niwa create` should produce `tsuku-5`, not `tsuku-3`. Without a root index, niwa either scans and takes max+1 (works) or risks reusing a recently-destroyed number that the user associates with old state.

Scanning for max+1 handles this correctly and doesn't require a root index. The root index is a convenience optimization, not a correctness requirement.

## Decision

**Option B: Instance-Centric** with one refinement from Option C.

The registry is a minimal name-to-root-path index. Each instance has `.niwa/instance.json` with all its own state. No root-level `.niwa/` directory.

For instance enumeration, `niwa status` at a workspace root scans immediate subdirectories for `.niwa/instance.json` markers. For `niwa create`, it scans existing instance directories and assigns max(existing_numbers) + 1.

The refinement: the registry tracks the root path per workspace name (from Option C's registry schema), enabling `niwa status` and `niwa create` to find the root when invoked from outside it.

**Final schemas:**

### ~/.config/niwa/config.toml

```toml
[global]
clone_protocol = "ssh"

[registry.tsuku]
source = "tsukumogami/niwa-tsuku-config"
root = "/home/user/dev/tsuku-root"

[registry.my-project]
root = "/home/user/dev/my-project-root"
# No source = local-only config
```

### Per-instance .niwa/instance.json

```json
{
  "schema_version": 1,
  "config_name": "tsuku",
  "instance_name": "tsuku-2",
  "instance_number": 2,
  "root": "/home/user/dev/tsuku-root",
  "created": "2026-03-25T10:00:00Z",
  "last_applied": "2026-03-25T10:05:00Z",
  "managed_files": [
    {
      "path": "CLAUDE.md",
      "hash": "sha256:abc123...",
      "generated": "2026-03-25T10:00:00Z"
    }
  ],
  "repos": {
    "public/tsuku": {
      "url": "git@github.com:tsukumogami/tsuku.git",
      "cloned": true,
      "claude_local_written": true
    }
  }
}
```

### Detached workspace .niwa/instance.json

```json
{
  "schema_version": 1,
  "config_name": null,
  "instance_name": "detached",
  "instance_number": null,
  "root": null,
  "created": "2026-03-25T10:00:00Z",
  "last_applied": "2026-03-25T10:05:00Z",
  "detached": true,
  "managed_files": [],
  "repos": {}
}
```

### Key behaviors

| Operation | How it works |
|-----------|-------------|
| `niwa create` | Scan root for `.niwa/instance.json` in subdirs, assign max+1, create instance dir with `.niwa/instance.json` |
| `niwa apply` (inside instance) | Walk up to find `.niwa/instance.json`, read root path from it, load `workspace.toml` from root, apply |
| `niwa apply <instance>` (from root) | Look for `<instance>/.niwa/instance.json` under root |
| `niwa status` (from root) | Scan immediate subdirs for `.niwa/instance.json`, report each |
| `niwa status` (inside instance) | Walk up to `.niwa/instance.json`, report that instance |
| `niwa destroy <instance>` | Remove instance directory, no other state to clean up |
| `niwa apply` (detached) | Find `workspace.toml` in current dir, find/create `.niwa/instance.json` alongside it |

## Interactions with Other Decisions

- **Decision 1 (repo/group schema):** Instance state references repos by their relative path within the instance (e.g., `public/tsuku`), which depends on how groups map to directories.
- **Decision 2 (CLAUDE.md hierarchy):** The `managed_files` list in instance state tracks all generated CLAUDE.md and CLAUDE.local.md files, enabling drift detection.
- **Decision 4 (hooks/settings/env):** When hooks are added (v0.2), the instance state will track distributed hook files the same way it tracks managed CLAUDE files.
- **Decision 5 (per-host overrides):** The `root` field in both the registry and instance state uses absolute paths. Per-host overrides (v0.3) may need to adjust these paths per hostname.
