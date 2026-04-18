---
status: Current
upstream: docs/prds/PRD-workspace-visibility-overlay.md
problem: |
  The niwa apply pipeline has no concept of a secondary configuration layer that is
  additive. Supporting workspace overlays — companion repos that layer private or
  supplemental repos, groups, content, and hooks on top of a shared base workspace
  config — requires threading a new config type, clone-or-sync operation, and content
  append mechanism into the pipeline without disrupting the existing
  workspace-plus-GlobalOverride merge chain.
decision: |
  A new WorkspaceOverlay struct parses workspace-overlay.toml from a
  convention-discovered or explicitly-specified overlay repo. MergeWorkspaceOverlay()
  inserts a merged *WorkspaceConfig between workspace config load and GlobalOverride
  application in runPipeline(). The overlay clone is stored at
  $XDG_CONFIG_HOME/niwa/overlays/<derived-name>/ and tracked via OverlayURL,
  NoOverlay, and OverlayCommit fields in InstanceState. Content append is handled by
  an OverlaySource field on ContentRepoConfig (TOML-hidden), read by
  InstallRepoContent().
rationale: |
  A dedicated struct rather than reusing WorkspaceConfig or extending GlobalOverride
  keeps the three config layers cleanly separated with their own parse and validation
  logic. XDG home storage mirrors the existing global config pattern, allowing
  multiple workspace instances to share one overlay clone. Inserting the overlay
  between workspace load and GlobalOverride preserves GlobalOverride as the outermost
  layer (personal config overrides team config), which is the correct precedence.
  Inline OverlaySource on ContentRepoConfig keeps content generation in one function
  rather than splitting it across a post-processing pass.
---

# DESIGN: Workspace overlay

## Status

Current

## Context and Problem Statement

The niwa apply pipeline is a linear sequence of numbered steps in `runPipeline()` (`internal/workspace/apply.go`). It loads a `WorkspaceConfig` from `workspace.toml`, optionally applies a `GlobalOverride` on top, discovers and clones repos, then installs CLAUDE context files. The pipeline has no concept of a secondary configuration layer that is additive (contributes sources, groups, repos, and content) rather than purely overriding.

Supporting workspace overlays requires threading three new capabilities into this pipeline without disrupting the existing two-layer merge (workspace + global override):

1. **A new config type**: `workspace-overlay.toml` is neither a `WorkspaceConfig` (it lacks workspace metadata and has additional restrictions on sources) nor a `GlobalOverride` (it adds repos and content rather than only overriding them). A new `WorkspaceOverlay` struct is needed with its own parse/validate logic.

2. **A clone-or-sync operation**: `SyncConfigDir()` assumes a clone already exists. Overlay discovery requires attempting a `git clone` on first contact, failing silently if the repo is inaccessible, and falling back to `SyncConfigDir`-style sync on subsequent applies. `InstanceState` (`internal/workspace/state.go`) must carry `OverlayURL` and `NoOverlay` to preserve init-time intent across apply invocations.

3. **Content append**: The existing `InstallRepoContent()` generates `CLAUDE.local.md` from a single source file per repo. The `overlay` field in overlay content entries needs to append a second file to the generated output for repos already defined in the base config — requiring either a schema extension on `ContentRepoConfig` or a post-generation pass.

The insertion point in the pipeline is between step 2 (workspace config loaded from disk) and step 2a (global config synced and merged). This preserves the GlobalOverride as the outermost layer — it applies on top of the already-merged workspace+overlay config, which is the correct precedence.

## Decision Drivers

- **Consistency with existing patterns**: `GlobalOverride` and `GlobalConfigSource` establish the model — new structs for new config types, XDG home for clone storage, `SyncConfigDir` for sync. Deviating requires justification.
- **Immutable merge**: all existing merge functions (`MergeGlobalOverride`, `MergeOverrides`) return new structs, never mutate. The overlay merge must follow this contract.
- **Instance-scoped intent**: `InstanceState.SkipGlobal` shows how per-instance boolean flags are stored in `.niwa/instance.json`. `OverlayURL` and `NoOverlay` follow the same pattern — no global config changes for per-instance state.
- **Silent degradation on first-time access**: the overlay may or may not be accessible, and the apply must not expose its existence through error output. This shapes error handling around the clone step.
- **Path safety**: `validateGlobalOverridePaths()` already enforces `..` and absolute-path rejection on GlobalOverride. The same validation must apply to overlay paths (`files`, `env.files`, content `overlay` paths).
- **Minimal API surface**: the overlay feature is additive-only at the user level. Implementation should not introduce new CLI flags beyond `--overlay` and `--no-overlay` on `niwa init`, and should not require changes to `niwa apply`'s command-line interface.

## Considered Options

### Decision 1: WorkspaceOverlay struct design

The `workspace-overlay.toml` format supports a strict subset of `workspace.toml` fields: additive sections (`[[sources]]`, `[groups.*]`, `[repos.*]`, `[claude.content.*]`) and override sections (`[claude.hooks]`, `[claude.settings]`, `[env]`, `[files]`). It explicitly prohibits `[workspace]`, `[channels]`, and auto-discovery sources. This is similar to `GlobalOverride`'s relationship to `WorkspaceConfig` — a distinct type with different fields and different semantics — but adds the additive sources/groups/repos/content that `GlobalOverride` intentionally omits.

#### Chosen: New `WorkspaceOverlay` struct

Define a dedicated `WorkspaceOverlay` struct in `internal/config/config.go` (or a new `overlay.go`):

```go
type WorkspaceOverlay struct {
    Sources []SourceConfig             `toml:"sources"`
    Groups  map[string]GroupConfig     `toml:"groups"`
    Repos   map[string]RepoOverride    `toml:"repos"`
    Claude  OverlayClaudeConfig        `toml:"claude"`
    Env     EnvConfig                  `toml:"env"`
    Files   map[string]string          `toml:"files,omitempty"`
}

type OverlayClaudeConfig struct {
    Hooks    HooksConfig        `toml:"hooks"`
    Settings map[string]any     `toml:"settings"`
    Content  OverlayContentConfig `toml:"content"`
}

type OverlayContentConfig struct {
    Repos map[string]OverlayContentRepoConfig `toml:"repos"`
}

type OverlayContentRepoConfig struct {
    Source  string `toml:"source"`   // for overlay-only repos
    Overlay string `toml:"overlay"`  // for base-config repos (append)
}
```

Parse via `config.ParseOverlay(path string) (*WorkspaceOverlay, error)` which reads TOML and validates: all `[[sources]]` entries must have explicit `repos` lists (no auto-discovery); `source` and `overlay` are mutually exclusive per content entry; paths in `files`, `env.files`, and `overlay` fields pass `validateOverlayPaths()`. The merge function `workspace.MergeWorkspaceOverlay(ws *WorkspaceConfig, o *WorkspaceOverlay, overlayDir string) *WorkspaceConfig` follows `MergeGlobalOverride`'s pattern — deep-copies `ws`, applies overlay fields in order.

#### Alternatives Considered

**Reuse `WorkspaceConfig` with a validation wrapper**: parse `workspace-overlay.toml` into a `WorkspaceConfig`, then reject prohibited fields at validation time. Rejected because `WorkspaceConfig` carries `Workspace`, `Channels`, and `Instance` fields with no-op semantics in the overlay context, and because the content entry struct (`ContentRepoConfig`) has only a `Source` field — adding `Overlay` string to `ContentRepoConfig` would expose an overlay-specific field in the base config's type, coupling the two concepts.

**Extend `GlobalOverride` to include additive sections**: add `Sources`, `Groups`, `Repos`, `Content` to `GlobalOverride`. Rejected because `GlobalOverride`'s design explicitly excludes these fields — the global config is personal preference, not workspace structure. Mixing workspace structure changes into the global override path confuses the layering model and would break the existing precedence semantics (global override currently applies to the merged workspace result; adding additive sections to it would change what "merged workspace result" means).

---

### Decision 2: Overlay clone storage and first-time access

The overlay clone must persist between apply invocations (so sync can be used instead of clone on subsequent runs) and must be findable given only the overlay URL stored in `instance.json`. The existing `GlobalConfigDir()` pattern (`$XDG_CONFIG_HOME/niwa/global`) provides a precedent for machine-level config clones, but the overlay is per-workspace-instance, not machine-global.

#### Chosen: XDG home keyed by URL-derived workspace ID

Store the overlay clone at `$XDG_CONFIG_HOME/niwa/overlays/<workspace-id>/` where `<workspace-id>` is derived from the overlay URL — specifically, a sanitized `<org>-<repo>` extracted from the URL (e.g., `acmecorp-dot-niwa-overlay`). If multiple instances use the same overlay URL (the common case — all instances of the same workspace use the same overlay), they share one clone, which is a disk-space win. The "previously cloned" heuristic (R7 in the PRD) checks that `<clone-dir>/.git/` exists and `git rev-parse HEAD` succeeds.

A new `OverlayDir(overlayURL string) (string, error)` function in `internal/config/` derives the path from the URL, analogous to `GlobalConfigDir()`.

First-time clone vs. sync is handled by `CloneOrSyncOverlay(url, dir string) error`:
- If `dir` does not exist or is not a valid git repo: `git clone <url> <dir>` (failure is the "never cloned" case — silent skip)
- If `dir` is a valid git repo: `git pull --ff-only origin` (failure is the "previously cloned" case — hard error)

This function is called from both `niwa init` (for `--overlay` and convention discovery) and from `runPipeline()` (step 2.5).

#### Alternatives Considered

**Per-workspace clone in `.niwa/overlay/`**: store the overlay clone inside the workspace's `.niwa/` directory. Rejected because `.niwa/` is version-controlled workspace state managed by the workspace config repo; a clone of an external repo inside it creates a nested git repository, which requires careful `.gitignore` management and breaks `git status` in the workspace root. It also cannot be shared across workspace instances on the same machine.

**Per-instance entry in `~/.config/niwa/config.toml`**: store overlay URL and clone path in the global config file, keyed by instance name. Rejected because this creates a machine-global config dependency for per-workspace state. Moving or renaming the workspace would leave stale entries. The PRD explicitly chose `instance.json` for overlay state — keeping it there avoids the global config coupling.

---

### Decision 3: Content overlay append mechanism

The `overlay` field in `OverlayContentRepoConfig` must cause the overlay file's content to be appended to the generated `CLAUDE.local.md` for a base-config repo. The existing `InstallRepoContent()` in `internal/workspace/content.go` generates `CLAUDE.local.md` from a single source file via the workspace config. The question is where to introduce the append.

#### Chosen: Extend `ContentRepoConfig` with `OverlaySource` field; modify `InstallRepoContent`

During `MergeWorkspaceOverlay`, when an overlay content entry specifies `overlay = "path/to/file.md"` for a repo that already exists in the base config's content map, copy the entry to the merged `WorkspaceConfig` with the overlay path stored in a new `OverlaySource string` field on `ContentRepoConfig`:

```go
type ContentRepoConfig struct {
    Source        string `toml:"source"`
    OverlaySource string `toml:"-"`  // not serialized; set by MergeWorkspaceOverlay
}
```

`InstallRepoContent()` is extended to check `OverlaySource`: if non-empty, read the file from `overlayDir`, append its content to the generated `CLAUDE.local.md` separated by a blank line.

This keeps content generation in one place (the existing `InstallRepoContent` call in step 6 of `runPipeline`) and avoids a separate post-processing pass. The `toml:"-"` tag ensures `OverlaySource` is never parsed from or serialized to TOML — it is pipeline-internal state set exclusively by the merge function.

#### Alternatives Considered

**Post-generation pass in `runPipeline`**: after step 6 (InstallRepoContent), iterate over overlay content entries and append to the generated files. Rejected because it splits content generation into two disconnected places in the pipeline, requiring the pipeline to carry separate overlay content state alongside the merged `WorkspaceConfig`. It also makes the content generation behavior harder to understand from `InstallRepoContent` alone.

**Separate `OverlayContentConfig` in the `Applier` struct**: store overlay content references in the `Applier` rather than in the merged `WorkspaceConfig`. Rejected for the same reason — it splits knowledge of what ends up in `CLAUDE.local.md` across two data structures. Single-source of truth (the merged config) is the existing pattern and should be maintained.

## Decision Outcome

**Chosen: New `WorkspaceOverlay` struct + XDG clone storage + `ContentRepoConfig.OverlaySource`**

### Summary

The overlay feature adds a new layer to the niwa config stack, inserted between the workspace config and the GlobalOverride. Three new components carry the implementation:

`WorkspaceOverlay` is a new struct in `internal/config/` that parses `workspace-overlay.toml`. It mirrors `GlobalOverride`'s role but adds additive sections (sources, groups, repos, content) that `GlobalOverride` intentionally omits. `config.ParseOverlay(path)` validates the file at parse time: sources must have explicit `repos` lists, content entries must use either `source` or `overlay` (not both), and all file paths must pass traversal checks.

`workspace.MergeWorkspaceOverlay(ws, overlay, overlayDir)` returns a new `*WorkspaceConfig` with overlay fields merged in. Source conflicts (same org in both configs) produce a hard error before any git operations. Group and repo collisions silently prefer the base config. Content entries for overlay-only repos populate `ContentRepoConfig.Source`; content entries with the `overlay` field populate `ContentRepoConfig.OverlaySource`. Hook scripts in the overlay are resolved to absolute paths using `overlayDir` before merging, exactly as `MergeGlobalOverride` does with `globalConfigDir`.

The overlay clone lives at `$XDG_CONFIG_HOME/niwa/overlays/<workspace-id>/` where `<workspace-id>` is derived from the overlay URL. A new `CloneOrSyncOverlay(url, dir)` function handles first-time clone (silent failure → skip) vs. subsequent sync (failure → hard error with non-revealing message). `InstanceState` gains two fields: `OverlayURL string` (set on successful discovery or explicit `--overlay`) and `NoOverlay bool` (set by `--no-overlay` at init, persists across applies).

The apply pipeline gains two steps between existing steps 2 and 2a:
- Step 2.5: determine overlay URL (from `instance.json`) or attempt convention discovery (`<org>/<repo>-overlay` derived from `RegistryEntry.Source`). Call `CloneOrSyncOverlay`. If silent-skip applies, proceed without overlay.
- Step 2.6: parse `workspace-overlay.toml` from clone dir. Call `MergeWorkspaceOverlay`. The resulting `*WorkspaceConfig` is the input to all subsequent pipeline steps including GlobalOverride application.

At step 6, `InstallRepoContent` checks `OverlaySource` and appends overlay file content where present.

`CLAUDE.overlay.md` injection follows the same pattern as `InstallGlobalClaudeContent` — a new `InstallOverlayClaudeContent(overlayDir, instanceRoot)` function copies `CLAUDE.overlay.md` if present and injects `@CLAUDE.overlay.md` into the workspace `CLAUDE.md` via the existing `ensureImportInCLAUDE` mechanism.

### Rationale

The three decisions compose cleanly. A dedicated `WorkspaceOverlay` struct with `OverlaySource` on `ContentRepoConfig` keeps the merged `*WorkspaceConfig` as the single source of truth for what the pipeline produces — no parallel state structures needed. XDG clone storage mirrors the GlobalOverride pattern exactly, so the sync/clone logic is familiar and the "previously cloned" heuristic reuses the same git-based check. Inserting the overlay merge between workspace load and GlobalOverride application means the GlobalOverride remains the outermost layer, which is the correct precedence: personal machine configuration overrides team workspace configuration which overlays shared base configuration.

## Solution Architecture

### Overview

The overlay feature introduces one new config layer into niwa's existing three-layer stack (workspace defaults → per-repo overrides → GlobalOverride). The overlay sits between workspace config load and GlobalOverride application, producing a merged `*WorkspaceConfig` that the rest of the pipeline treats identically to a base-only workspace config.

### Components

```
internal/
├── config/
│   ├── config.go              — ContentRepoConfig gains OverlaySource field
│   ├── overlay.go             — WorkspaceOverlay struct, ParseOverlay(), OverlayDir()
│   └── registry.go            — no changes (RegistryEntry.Source already stores URL)
└── workspace/
    ├── apply.go               — runPipeline() gains steps 2.5–2.6; Applier gains overlayDir
    ├── configsync.go          — CloneOrSyncOverlay() added (or new overlaysync.go)
    ├── content.go             — InstallRepoContent() appends OverlaySource if set
    ├── override.go            — MergeWorkspaceOverlay() added
    ├── state.go               — InstanceState gains OverlayURL, NoOverlay fields
    └── workspace_context.go   — InstallOverlayClaudeContent() added
internal/cli/
├── init.go                    — --overlay, --no-overlay flags; convention discovery at init
└── apply.go                   — no flag changes; overlayDir threaded to runPipeline
```

### Key Interfaces

**`config.ParseOverlay(path string) (*WorkspaceOverlay, error)`**
Reads `workspace-overlay.toml` from `path`. Validates: all sources have explicit `repos`; no auto-discovery; content entries use `source` xor `overlay`; path-traversal checks on `files`, `env.files`, content `overlay` paths. Returns parsed struct or validation error.

**`config.OverlayDir(overlayURL string) (string, error)`**
Returns `$XDG_CONFIG_HOME/niwa/overlays/<sanitized-url>/`. Sanitization: parse GitHub URL or shorthand, produce `<org>-<repo>` (e.g., `acmecorp-dot-niwa-overlay`). Returns error if `overlayURL` cannot be parsed.

**`workspace.CloneOrSyncOverlay(url, dir string) (firstTime bool, err error)`**
- `firstTime=true`: `dir` does not exist or is not a valid git repo → attempt `git clone <url> <dir>`; on failure return `(true, err)` (caller silently skips)
- `firstTime=false`: `dir` is a valid git repo → `git pull --ff-only origin`; on failure return `(false, err)` (caller aborts with error)

Callers distinguish behavior based on `firstTime`:
```go
firstTime, err := CloneOrSyncOverlay(url, dir)
if err != nil {
    if firstTime {
        // silent skip
    } else {
        return fmt.Errorf("workspace overlay sync failed: %w. Use --no-overlay to skip.", err)
    }
}
```

**`workspace.MergeWorkspaceOverlay(ws *WorkspaceConfig, o *WorkspaceOverlay, overlayDir string) (*WorkspaceConfig, error)`**
Deep-copies `ws`. Returns error on duplicate source org. Merges: sources appended; groups added (base wins); repos added (base wins); content entries added with source/overlay field per R13 logic; hooks appended (overlay after base); settings per-key (base wins); env vars per-key (base wins); env files appended (base first); files per-key (base wins); hook scripts resolved to absolute paths using `overlayDir`.

**`workspace.InstallOverlayClaudeContent(overlayDir, instanceRoot string) error`**
Copies `<overlayDir>/CLAUDE.overlay.md` to `<instanceRoot>/CLAUDE.overlay.md` if present. Calls `ensureImportInCLAUDE(instanceRoot, "@CLAUDE.overlay.md", afterWorkspaceContext)` to inject import at the correct position. No-op (no error) if `CLAUDE.overlay.md` is absent.

**`InstanceState` new fields:**
```go
OverlayURL    string `json:"overlay_url,omitempty"`    // set by init, updated by apply on discovery
NoOverlay     bool   `json:"no_overlay,omitempty"`     // set by --no-overlay at init, never updated
OverlayCommit string `json:"overlay_commit,omitempty"` // HEAD SHA at init time; warn if overlay advances
```

### Data Flow

```
niwa init --from acmecorp/dot-niwa ~/ws
  ├── clone workspace config → .niwa/
  ├── derive convention URL: acmecorp/dot-niwa-overlay
  ├── CloneOrSyncOverlay(url, overlayDir)
  │   ├── success → write OverlayURL to instance.json
  │   └── failure (404/403/network) → write nothing (instance.json has no OverlayURL)
  └── write instance.json {overlay_url: "..." | {}}

niwa apply
  ├── LoadState → read OverlayURL, NoOverlay
  ├── runPipeline(overlayURL, noOverlay, ...)
  │   ├── step 2: Load WorkspaceConfig from .niwa/workspace.toml
  │   ├── step 2.5: determine overlay
  │   │   ├── NoOverlay=true → skip entirely
  │   │   ├── OverlayURL set → CloneOrSyncOverlay (hard error on firstTime=false failure)
  │   │   └── neither → derive convention URL from RegistryEntry.Source
  │   │       ├── CloneOrSyncOverlay → success: store OverlayURL in state, proceed
  │   │       └── failure (firstTime=true) → skip silently
  │   ├── step 2.6: ParseOverlay(overlayDir/workspace-overlay.toml)
  │   │            MergeWorkspaceOverlay(ws, overlay, overlayDir) → mergedWS
  │   ├── step 2a: SyncConfigDir(globalConfigDir) [unchanged]
  │   ├── steps 3a-3c: MergeGlobalOverride(mergedWS, ...) [unchanged]
  │   ├── steps 3-6: clone repos, install context using mergedWS
  │   │   └── step 4.5+: InstallOverlayClaudeContent(overlayDir, instanceRoot)
  │   └── step 6: InstallRepoContent checks OverlaySource, appends if set
  └── SaveState (with updated OverlayURL if convention discovery succeeded)
```

## Implementation Approach

### Phase 1: InstanceState schema and init flags

Deliverables:
- `OverlayURL string`, `NoOverlay bool`, and `OverlayCommit string` added to `InstanceState` in `state.go`
- `--overlay <repo>` and `--no-overlay` flags wired in `init.go`
- Convention URL derivation (`deriveOverlayURL(sourceURL string) (string, bool)`) in `internal/config/overlay.go`
- `OverlayDir(url string) (string, error)` in `internal/config/overlay.go`
- `CloneOrSyncOverlay(url, dir string) (bool, error)` in `internal/workspace/configsync.go` or `overlaysync.go`
- Init stores `OverlayURL` and `OverlayCommit` (HEAD SHA) in state on successful clone; `NoOverlay=true` on `--no-overlay`
- Init prints the convention-discovered overlay URL to stdout so users see what was adopted
- Unit tests for URL derivation, `OverlayDir`, init flag behavior

### Phase 2: WorkspaceOverlay config type and merge

Deliverables:
- `WorkspaceOverlay`, `OverlayClaudeConfig`, `OverlayContentConfig`, `OverlayContentRepoConfig` structs in `internal/config/overlay.go`
- `OverlaySource string` field (TOML-hidden) on `ContentRepoConfig` in `config.go`
- `ParseOverlay(path string) (*WorkspaceOverlay, error)` with full validation:
  - Reject absolute paths and `..` components in all path fields (reuse `validateGlobalOverridePaths`)
  - Explicitly validate hook script paths as relative (before `MergeWorkspaceOverlay` resolves them)
  - Reject `[files]` destination paths beginning with `.claude/` or `.niwa/`
  - Enforce `source` xor `overlay` on content entries
  - Require explicit `repos` lists on all sources (no auto-discovery)
- `MergeWorkspaceOverlay(ws, overlay, overlayDir) (*WorkspaceConfig, error)` in `override.go`
- Unit tests for parse validation (prohibited fields, path traversal, source-xor-overlay, destination containment) and merge semantics (collision handling, hook resolution)

### Phase 3: Apply pipeline integration

Deliverables:
- `Applier` struct gains `overlayDir string` (or derived at call time from `OverlayURL` in state)
- `runPipeline()` gains steps 2.5–2.6 with the full state-reading and error-handling logic
- `runApply()` reads `OverlayURL`/`NoOverlay` from state, passes to pipeline
- State is updated (saved) when convention discovery succeeds and adds a new `OverlayURL`
- Integration test: apply with overlay, without overlay, with `NoOverlay=true`

### Phase 4: Content generation and CLAUDE injection

Deliverables:
- `InstallRepoContent()` extended to append `OverlaySource` content with blank-line separator
- `InstallOverlayClaudeContent(overlayDir, instanceRoot string) error` in `workspace_context.go`
- `CLAUDE.overlay.md` added to `ManagedFiles` when installed so it is cleaned up if the overlay is later disabled or removed
- `ensureImportInCLAUDE` extended (if needed) to support ordered insertion — the overlay import must appear after the workspace context import and before the global import; if the existing function only prepends, the ordered insertion logic must be built here
- `CheckGitignore` called whenever `settings.local.json` is written (not only from `InstallRepoContent`)
- Pipeline step 4.5 or equivalent calls `InstallOverlayClaudeContent` when `overlayDir` is set
- Tests: CLAUDE.local.md content with and without overlay, CLAUDE.md import ordering, missing CLAUDE.overlay.md is no-op, managed files cleanup on overlay removal

## Security Considerations

The workspace overlay feature downloads and processes content from an externally-hosted git repository. Several attack surfaces apply:

**External artifact handling — HIGH relevance**

The overlay clone is a git repository cloned from a user-specified or convention-derived URL. Its `workspace-overlay.toml` specifies file paths, env var names, hook scripts, and content file paths. `ParseOverlay()` rejects absolute paths and `..` components in all path fields, mirroring the existing `validateGlobalOverridePaths()` and `validateContentSource()` implementations.

Two validation gaps require explicit handling in `ParseOverlay()`: (1) hook script paths in `[hooks]` entries must be validated as relative *before* `MergeWorkspaceOverlay()` resolves them to absolute paths within the overlay clone directory — this must be explicit in `ParseOverlay()`, not relied upon implicitly from the join operation; (2) `[files]` destination paths must be checked against a containment allowlist that rejects targets beginning with `.claude/` or `.niwa/`, since additive overlay keys (keys not already present in the base config) execute unconditionally and would overwrite generated artifacts. The base-wins merge only protects keys already present in the base config's files map.

A third gap arises after path resolution: `MergeWorkspaceOverlay()` resolves hook script paths to absolute paths using `filepath.Join(overlayDir, scriptPath)`. The overlay author controls the clone contents and can place symlinks inside it (`hooks/evil.sh -> /etc/cron.d/something`). `filepath.Join` does not resolve symlinks, so the resolved path appears to be within `overlayDir` until the OS follows the symlink at execution time. After every `filepath.Join(overlayDir, scriptPath)`, a symlink-resolving containment check must be applied — the same `resolveExistingPrefix` / `checkContainment` approach used elsewhere in the codebase — to confirm the final path stays within `overlayDir`.

Additive `[sources]` entries in the overlay expand the trust boundary to new GitHub orgs: a malicious overlay can cause niwa to discover and clone repos from an org the base config never referenced. This is a trust expansion beyond what individual path validation addresses. The duplicate-org check (error on same-org collision) prevents name conflicts but does not prevent genuinely new orgs from being added. Teams should be aware that overlay sources are trusted at the same level as base sources.

`CLAUDE.overlay.md` is injected into the instance root as instruction content for Claude Code. A malicious overlay can use this file to alter Claude's behavior across all repos in the workspace. This is the same risk as `CLAUDE.global.md` from the global config layer, but that layer requires explicit `niwa global register`. Convention-discovered overlays do not require an equivalent explicit user step, making prompt injection via `CLAUDE.overlay.md` a higher residual risk.

Residual risk after mitigations: hook scripts and `CLAUDE.overlay.md` can execute or inject arbitrary instructions. The residual is the same as GlobalOverride for explicitly-specified overlay URLs; higher for convention-discovered overlays due to the lower explicit-user-consent bar.

**Supply chain or dependency trust — HIGH relevance**

Convention-based URL derivation (`<base>-overlay`) introduces a squatting vector. A team that publishes `acmecorp/dot-niwa` without creating `acmecorp/dot-niwa-overlay` leaves the overlay namespace open to any GitHub user. A squatter who creates a valid `workspace-overlay.toml` will have it silently cloned and applied on the next `niwa apply` after squatting. The design's "empty repo fails loudly" mitigation only addresses accidental name collisions, not intentional squatting with a valid config.

The silent adoption model compounds this risk: convention discovery prints no output at init time, so users have no visible confirmation of which overlay URL was adopted.

Three mitigations are required before this feature reaches stable:

1. Print the convention-discovered overlay URL to stdout at `niwa init` time, so users see what was adopted.
2. Store the overlay HEAD commit SHA in `instance.json` at init time (`OverlayCommit string`). On subsequent applies, warn when the overlay has advanced beyond the pinned commit, requiring explicit user acknowledgment for overlay updates.
3. Teams that publish a base config (`dot-niwa`) should immediately create a companion overlay repo (even empty) to prevent namespace squatting. `niwa init` output should include this recommendation when convention discovery fails silently.

Note on discovery scope: the PRD requires re-trying convention discovery on each apply (R9) to support the "granted access later" scenario, where a user gains org access after init. This keeps the squatting window open for the lifetime of a workspace rather than only at init time. Restricting discovery to `niwa init` only would eliminate this window but break the R9 use case. The trade-off is accepted in v1; commit SHA pinning (mitigation 2 above) is the primary compensating control.

**Permission scope — MEDIUM relevance**

Requires filesystem write to `$XDG_CONFIG_HOME/niwa/overlays/` and workspace instance root. Same scope as GlobalOverride. Concurrent `niwa apply` calls on the same overlay URL run without file locking, creating a narrow race window where one process reads a partially-updated config mid-pull. The impact is a loud `ParseOverlay()` error rather than silent corruption. No privilege escalation. The clone directory is created by niwa — the overlay cannot control where it is stored.

**Data exposure — LOW relevance**

Overlay file contents are processed in memory and written only to the workspace instance root. The overlay URL is stored locally in `instance.json` and not transmitted. One notable case: overlay `[claude.env] promote` can cause env vars from the workspace pipeline to be written into `settings.local.json` inside cloned repos. If a repo lacks a `*.local*` gitignore entry, these vars could be committed to version control. The existing `CheckGitignore` warning is only called from `InstallRepoContent`, so it does not fire for repos that have `claude.env.promote` configured but no CLAUDE content file. `CheckGitignore` must be called whenever `settings.local.json` is written, not only when content is installed.

## Consequences

### Positive

- Zero-configuration overlay for teams that follow the naming convention — `niwa init --from acmecorp/dot-niwa` discovers and applies `acmecorp/dot-niwa-overlay` without any additional commands.
- Per-instance intent captured at init time in `instance.json` — multiple workspaces on the same machine each carry independent overlay state, eliminating the machine-global registration problem.
- Full precedence chain preserved: GlobalOverride remains the outermost layer, overlay sits between workspace and GlobalOverride, matching the conceptual ordering (personal config > team supplement > shared base).
- Graceful degradation with no output: first-time inaccessible overlays produce no errors and no indication of existence, satisfying the privacy constraint.

### Negative

- Convention squatting risk: teams publishing a base config without proactively creating an overlay repo are exposed to the squatting attack described in Security Considerations.
- Shared clone across instances may cause sync contention: two concurrent `niwa apply` invocations on different instances using the same overlay URL both attempt to sync the same clone directory. No locking is implemented in `SyncConfigDir` today, and `CloneOrSyncOverlay` inherits this gap.
- No mechanism to change or remove overlay URL after init: `OverlayURL` in `instance.json` is set at init and updated on convention discovery success. Teams that rename their overlay repo or want to switch to a different overlay must edit `instance.json` manually in v1.
- `ContentRepoConfig.OverlaySource` is pipeline-internal state on a config struct: it is set by `MergeWorkspaceOverlay` and read by `InstallRepoContent`, but is not part of the TOML schema. This is a coupling point between the merge step and the content step that is invisible from the struct definition alone.

### Mitigations

- **Squatting**: document that teams publishing a base config should create a (possibly empty) overlay repo immediately. The convention discovery only succeeds on a successful clone — an empty repo with no `workspace-overlay.toml` triggers the "missing file" error, which is a hard error (overlay was cloned but is malformed), not a silent skip. This fails loudly rather than silently applying a squatted overlay.
- **Squatting rollback**: if convention discovery is found to be abused post-launch, the mitigation path is to require `--overlay` for all new inits and leave existing state untouched. This is a non-breaking change for existing workspaces. Convention discovery can be disabled in a patch release by gating it behind the absence of a (future) `--discovery=off` config flag. The recovery plan should be decided before v1 ships.
- **Sync contention**: document as a known v1 limitation. File-locking on the overlay clone directory can be added in a subsequent release, matching the same gap in `SyncConfigDir`.
- **Post-init overlay URL changes**: document that `instance.json` can be manually edited. A `niwa overlay set` command is deferred to a future release.
- **OverlaySource coupling**: the `toml:"-"` tag makes the pipeline-internal nature explicit. A code comment on the field explains that it is set by `MergeWorkspaceOverlay` and read only by `InstallRepoContent`.
