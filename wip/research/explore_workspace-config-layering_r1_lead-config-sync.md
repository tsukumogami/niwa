# Lead: Config sync and apply flow

## Findings

### 1. Config Sync Mechanism (configsync.go)

**File:** `internal/workspace/configsync.go`

`SyncConfigDir()` (lines 15-49) is the single function handling workspace config synchronization:
- **Preconditions check:** Verifies the config directory is a git repo (line 17-20) and has an origin remote (line 23-26)
- **Dirty-state check:** Unless `allowDirty=true`, rejects any uncommitted changes (lines 29-38) with error message pointing user to `--allow-dirty` flag
- **Sync operation:** Runs `git pull --ff-only origin` to fetch latest (lines 41-46)
- **Early exit on non-repo:** Returns nil (not an error) if config dir is not a git repo or has no origin -- permits local-only workspaces
- **Error propagation:** All git errors are wrapped and returned; stderr/stdout pass through to user

This is a **single-purpose, stateless function** -- it synchronizes exactly one directory and makes no decisions about what comes before or after.

### 2. Apply Command Flow (cli/apply.go)

**File:** `internal/cli/apply.go` - `runApply()` function (lines 54-122)

The apply command orchestrates in this sequence:

```
1. Scope resolution (lines 60-73)
   - Registry lookup (if workspace name given)
   - OR discovery from cwd (if no name given)
   → Outputs: ApplyScope with instance list + config path

2. Load workspace config (lines 75-93)
   - Check config path exists
   - Sync config dir via SyncConfigDir() (line 82)
   - Load workspace.toml via config.Load() (line 86)
   - Print warnings

3. GitHub token resolution (line 95)

4. Create applier and configure (lines 97-98)
   - ApplyNoPull flag set here

5. Apply to each instance (lines 102-110)
   - Sequential iteration over instances
   - Errors collected but don't abort loop
   - Each instance gets Applier.Apply(cfg, configDir, instanceRoot)

6. Update registry (lines 113-115)
   - After all instances complete
   - Stores workspace name → config path mapping

7. Error handling (lines 117-119)
   - Returns combined error if any instance failed
```

**Critical observation:** Between step 2 (sync config dir) and step 5 (apply to instances), there is NO personal config handling. The config loaded at line 86 is the workspace config only.

### 3. Workspace Apply Pipeline (workspace/apply.go)

**File:** `internal/workspace/apply.go` - `Apply()` method (lines 103-159)

The Apply() method on an instance:

```
1. Load existing instance state (lines 107-110)
   - Reads .niwa/instance.json
   - Required for incremental apply

2. Check drift on managed files (lines 113-122)
   - Warns if files modified outside niwa
   - Non-blocking (continues on warning)

3. Run shared pipeline (lines 124-128)
   - Calls runPipeline() with existingState
   - Pipeline does: discover → classify → clone → materialize

4. Clean up removed files/dirs (lines 136-139)
   - Deletes files from previous state no longer produced

5. Save updated state (lines 142-156)
   - Writes .niwa/instance.json with new LastApplied time
   - Includes managedFiles, repoStates, etc.
```

The pipeline itself (lines 164-416) is 250+ lines orchestrating:
- Repo discovery from sources
- Repo classification into groups
- Clone operations with conditional pull
- Content installation (workspace CLAUDE.md, group CLAUDE.md, repo CLAUDE.local.md)
- Materializer chain: hooks, settings, env, files
- Managed file tracking with hashes

**Critical insight:** The pipeline reads state (existingState) but does NOT merge any external configuration layer. It takes the loaded workspace config as the source of truth for all decisions.

### 4. Global Registry Structure (config/registry.go)

**File:** `internal/config/registry.go`

GlobalConfig structure (lines 12-26):
```go
type GlobalConfig struct {
    Global   GlobalSettings            // clone_protocol setting
    Registry map[string]RegistryEntry  // workspace name → entry
}

type RegistryEntry struct {
    Source string  // absolute path to workspace.toml
    Root   string  // absolute path to workspace root
}
```

**Key facts:**
- Stored in `~/.config/niwa/config.toml` (lines 61-71)
- Created if missing (lines 86-90 return empty config, not error)
- Maps workspace name → (config path, root path)
- Updated after each apply (cli/apply.go line 113-115)
- Used by `niwa apply <name>` to look up workspace location (cli/apply.go lines 126-150)

**Current use:** Only tracks workspace configs, not personal configs. Personal config would need to be added here.

### 5. Initialization Command (cli/init.go)

**File:** `internal/cli/init.go` - `runInit()` function (lines 79-167)

The init flow:
1. Check for init conflicts (line 86)
2. Load global config (line 94)
3. Resolve init mode: scaffold, named, or clone (line 99)
4. Execute mode logic (lines 101-126) - scaffolds locally or clones config repo
5. Verify workspace.toml parses (line 130)
6. Register in global registry (lines 136-162) - skipped for scaffold mode

**Relevant flag:** `--from` (line 16) lets users clone a config repo. **Missing:** No `--no-personal-config` flag currently.

## Implications

### Where Personal Config Sync Fits

Personal config should sync **between steps 2 and 5 in the apply command flow:**

```
Step 2: Sync workspace config
Step 2.5: [NEW] Sync personal config (if registered and not opt-out)
         - Load from ~/.config/niwa/config.toml
         - Check if personal config path exists and is a git repo
         - Run SyncConfigDir() on personal config dir (same as workspace)
         - Handle missing personal config gracefully (not an error)
Step 3-5: Apply to instances (existing pipeline, no changes needed)
```

This placement means:
- Personal config is fresh before apply decisions
- Same dirty-state and git-repo error handling as workspace config
- Works for single-instance and multi-instance applies
- Error at personal config sync aborts the whole apply (same as workspace config)

### What Personal Config Needs in Registry

The registry needs a new entry type or field:

Option A: Add to GlobalSettings
```toml
[global]
clone_protocol = "ssh"
personal_config_repo = "https://github.com/user/niwa-personal"
personal_config_path = "/home/user/.config/niwa/personal"  # Local clone
```

Option B: Separate registry section (parallel to workspace registry)
```toml
[personal_config]
source = "https://github.com/user/niwa-personal"
path = "/home/user/.config/niwa/personal"
disabled = false  # respect --no-personal-config opt-out
```

Registry location: `~/.config/niwa/config.toml` is the right place (already handles XDG_CONFIG_HOME).

### Error Handling Pattern to Follow

From `SyncConfigDir()`:
- Missing git repo → no error, silent pass
- Has origin remote → pull with `--ff-only`
- Dirty working tree → error with actionable suggestion (point to `--allow-dirty`)
- Git errors → wrap and return
- Non-blocking warnings → print to stderr, continue

Apply this same pattern to personal config:
- If personal config path doesn't exist → silent pass (user hasn't registered yet)
- If exists and is git repo → sync it (non-blocking error if it fails per workspace spec line 108)
- If exists but not git repo → skip sync, load as-is

### Dry-State Checks Already in Place

The apply command already has:
- `--allow-dirty` flag (line 18) for workspace config
- `applyAllowDirty` passed to SyncConfigDir (line 82)

For personal config to also respect dirty state, the same flag should apply to both config dirs, OR a separate `--allow-personal-dirty` flag if personal config is considered separately dirtable.

## Surprises

1. **SyncConfigDir is a pure git operation, not workspace-aware.** It doesn't know about workspaces or instances; it just pulls one directory. This is elegant for reuse but means personal config sync can follow the exact same pattern without special cases.

2. **Init command only scaffolds OR clones, never both.** There's no path that registers a workspace AND a personal config in one command. A future `niwa init --personal-config-repo <url>` would need to also handle personal config registration in the same flow.

3. **ApplyScope resolves exactly one config path**, but the registry can hold multiple workspaces. This works because each workspace.toml is authoritative for its instance set. Personal config is conceptually "one per machine" (not per workspace), so it fits differently in the registry model.

4. **Error handling is fail-fast per instance but fail-on-first for config.** If workspace config sync fails, the whole apply aborts (line 82). If repo sync fails in the pipeline, it's a warning (workspace/apply.go line 228). Personal config should follow the workspace config pattern (fail the apply).

5. **State is instance-scoped, not workspace-scoped.** Each instance has its own `.niwa/instance.json`, including LastApplied timestamp. Personal config doesn't have per-instance state; it's shared across instances in the same workspace. A new LastApplied field might be needed for personal config if we want to track it separately.

## Open Questions

1. **Should personal config be opt-in or opt-out per workspace?** The spec says `--no-personal-config` at init, implying opt-out is the default. But should already-initialized workspaces ignore personal config or respect it?

2. **How does personal config get registered the first time?** Is there a `niwa config register-personal <url>` command? Or does `niwa init` prompt/allow registration?

3. **Where does personal config go on disk?** The spec mentions `~/.config/niwa/personal/` but should it be:
   - `~/.config/niwa/personal/` (single machine-wide personal config)
   - `~/.config/niwa/personal/{workspace-name}/` (per-workspace personal overrides)
   - Something else?

4. **How does personal config identify its target workspace(s)?** When `niwa apply myworkspace` runs, how does the personal config know which sections apply? By workspace name in the registry? By a field in workspace.toml?

5. **What happens to personal config in multi-workspace setups?** If a machine has 3 registered workspaces and one personal config repo, does personal config apply to all 3 or just the one being applied?

6. **Should ApplyAllowDirty apply to personal config too?** Or should personal config have a separate flag?

## Summary

The apply command syncs workspace config between scope resolution and instance apply, via `SyncConfigDir()` which checks for dirty state and pulls with `--ff-only`. Personal config sync should follow the same pattern immediately after workspace config sync, before the pipeline runs; it can reuse the same `SyncConfigDir()` function and error handling. The registry at `~/.config/niwa/config.toml` needs a new section to store personal config repo URL and local path (not yet designed), and the `--no-personal-config` flag at init needs to store an opt-out flag alongside workspace entries to suppress personal config application per workspace.
