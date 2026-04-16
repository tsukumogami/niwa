# Codebase Research: workspace-visibility-overlay

## Apply Pipeline (`internal/cli/apply.go`, `internal/workspace/apply.go`)

Entry: `runApply()` → `Applier.Apply()` → `runPipeline()`

Pipeline stages (numbered in source):
1–2. Discover repos, classify into groups
2.1. Inject explicit repos from `[repos]` config
2a. Sync global config dir (if registered and !skipGlobal)
3a–3c. Load GlobalConfigOverride, resolve, merge via `MergeGlobalOverride()`
3. Clone/sync all repos
4. Install workspace CLAUDE.md
4.5. Install workspace context + root settings
5. Install group CLAUDE.md files
5c. Install global CLAUDE.md (if active)
6. Install repo CLAUDE.local.md
6.5. Run materializers (hooks, settings, env, files) per repo

Overlay insertion point: **between step 2 (load workspace config) and step 2a (global config sync)**
- New 2.5: Sync overlay clone
- New 2.6: Parse `workspace-overlay.toml`
- New 2.7: `MergeWorkspaceOverlay(ws, overlay, overlayDir)` → merged `*WorkspaceConfig`
- Then step 2a applies global override on top of merged result

## Init Command (`internal/cli/init.go`)

Three modes:
1. `niwa init` (scaffold): creates `.niwa/workspace.toml`, no source URL in registry
2. `niwa init <name>`: scaffold + name, no source URL
3. `niwa init <name> --from <url>`: clones config repo to `.niwa/`, stores source URL in registry

For overlay discovery, mode 3 is the only relevant case — only workspaces initialized with `--from` have a source URL to derive the convention overlay URL from.

`InstanceState.SkipGlobal` is set when `--skip-global` flag is passed at init. The overlay's `no_overlay` flag follows the same pattern.

## Registry (`internal/config/registry.go`)

```go
type RegistryEntry struct {
    Source string `toml:"source"`  // local path or repo URL
    Root   string `toml:"root"`    // workspace root directory
}
```

`GlobalConfigDir()` returns `~/.config/niwa/global` — the clone location for the global config repo. The overlay clone location follows the same XDG pattern: `$XDG_CONFIG_HOME/niwa/overlays/<workspace-id>/`.

## InstanceState (`internal/workspace/state.go`)

```go
type InstanceState struct {
    SchemaVersion  int                  `json:"schema_version"`  // currently 1
    ConfigName     *string              `json:"config_name"`
    InstanceName   string               `json:"instance_name"`
    InstanceNumber int                  `json:"instance_number"`
    Root           string               `json:"root"`
    Detached       bool                 `json:"detached,omitempty"`
    SkipGlobal     bool                 `json:"skip_global,omitempty"`
    Created        time.Time            `json:"created"`
    LastApplied    time.Time            `json:"last_applied"`
    ManagedFiles   []ManagedFile        `json:"managed_files"`
    Repos          map[string]RepoState `json:"repos"`
}
```

New fields needed:
- `OverlayURL string   json:"overlay_url,omitempty"`
- `NoOverlay  bool     json:"no_overlay,omitempty"`

## Config Structures (`internal/config/config.go`)

```go
type WorkspaceConfig struct {
    Workspace WorkspaceMeta
    Sources   []SourceConfig
    Groups    map[string]GroupConfig
    Repos     map[string]RepoOverride
    Claude    ClaudeConfig
    Env       EnvConfig
    Files     map[string]string
    Instance  InstanceConfig
    Channels  map[string]any
}

type ClaudeConfig struct {
    Enabled      bool
    Plugins      []string
    Marketplaces []string
    Hooks        HooksConfig
    Settings     map[string]any
    Env          EnvConfig
    Content      ContentConfig
}

type ContentConfig struct {
    Workspace ContentSource
    Groups    map[string]ContentSource
    Repos     map[string]ContentRepoConfig
}
```

`ContentRepoConfig` is the struct to extend with an `OverlaySource string` field for content overlay.

## Config Merge (`internal/workspace/override.go`)

`MergeGlobalOverride(ws *WorkspaceConfig, g GlobalOverride, globalConfigDir string) *WorkspaceConfig`:
- Deep-copies workspace config (never mutates original)
- Merges hooks (append), settings (global wins per-key), env vars (global wins), files (global wins)
- Resolves hook script paths to absolute using globalConfigDir

`MergeWorkspaceOverlay` will follow this same pattern:
- New sources: appended, with duplicate-org check
- New groups: added, base wins on collision
- New repos: added, base wins on collision
- Content: source entries added for overlay-only repos; overlay entries set OverlaySource on base repos
- Hooks: overlay hooks appended after base (same as GlobalOverride)
- Settings, env, files: base wins per-key (different from GlobalOverride where global wins)

## SyncConfigDir (`internal/workspace/configsync.go`)

```go
func SyncConfigDir(configDir string, allowDirty bool) error
```
- Assumes clone already exists (checks for `.git/` directory)
- No-op if not a git repo or no origin remote
- Runs `git pull --ff-only origin`

For overlay first-time clone: need `git clone <url> <dir>` before SyncConfigDir can be used. A new `CloneOrSync(url, dir string) error` function handles:
- If dir doesn't exist or isn't a git repo → `git clone <url> <dir>`
- If dir is a valid git repo → `git pull --ff-only origin`

## GlobalOverride Pattern (key reference for WorkspaceOverlay)

```go
type GlobalOverride struct {
    Claude *ClaudeOverride
    Env    EnvConfig
    Files  map[string]string
}
```

`WorkspaceOverlay` will be analogous but broader (additive sources/groups/repos/content plus the same override fields):

```go
type WorkspaceOverlay struct {
    Sources []SourceConfig
    Groups  map[string]GroupConfig
    Repos   map[string]RepoOverride
    Content OverlayContentConfig  // same as ContentConfig but with overlay field on repos
    Claude  ClaudeOverride        // hooks, settings only
    Env     EnvConfig
    Files   map[string]string
}
```

## Content Generation (`internal/workspace/content.go`)

`InstallRepoContent(repo, cfg, contentDir, instanceRoot)`:
- Generates `{instanceRoot}/{path}/{repo}/CLAUDE.local.md`
- Content comes from `cfg.Claude.Content.Repos[repo].Source` file

For overlay content (append), after generating from base source, read `OverlaySource` and append with blank line separator if non-empty.

## Convention URL Derivation

Given base config URL like `https://github.com/acmecorp/dot-niwa`:
- Parse org: `acmecorp`
- Parse repo: `dot-niwa`
- Overlay URL: `https://github.com/acmecorp/dot-niwa-overlay`

For SSH URLs (`git@github.com:acmecorp/dot-niwa.git`), apply same transformation after parsing.
For `org/repo` shorthand: `acmecorp/dot-niwa-overlay`.

Only available when `RegistryEntry.Source` is a URL (not a local path). Scaffold mode has no source URL — no convention discovery.

## Overlay Clone Storage

Pattern from `GlobalConfigDir()`:
```go
func GlobalConfigDir() string {
    xdg := os.Getenv("XDG_CONFIG_HOME")
    if xdg == "" {
        xdg = filepath.Join(os.Getenv("HOME"), ".config")
    }
    return filepath.Join(xdg, "niwa", "global")
}
```

Overlay clone: `$XDG_CONFIG_HOME/niwa/overlays/<workspace-id>/`
`<workspace-id>` = sanitized form of overlay URL (e.g., `acmecorp-dot-niwa-overlay`) or short hash.

Since the overlay URL is stored in `instance.json`, the path can be derived at runtime from the URL without storing it in any config file.
