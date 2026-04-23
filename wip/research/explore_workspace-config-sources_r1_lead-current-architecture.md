# Lead: What does niwa do today when sourcing config and overlay clones, end to end?

## Findings

### 1. First-time clone (init)

**Entry point**: `cmd/niwa/main.go` → `internal/cli/init.go:runInit`

`niwa init` resolves one of three modes via `resolveInitMode` (init.go:65):

- `modeScaffold` (no args, no `--from`): writes a commented `.niwa/workspace.toml` template via `workspace.Scaffold(cwd, "")` (`internal/workspace/scaffold.go:97`). No clone. No registry entry.
- `modeNamed` (name given, not in registry, no `--from`): same scaffold path with `name` baked in.
- `modeClone` (`--from <slug>` OR name resolves to a registry entry with `SourceURL`): full repo clone.

The clone path at `init.go:127-141`:

```go
case modeClone:
    cloneURL, err := workspace.ResolveCloneURL(source, globalCfg.CloneProtocol())
    ...
    niwaDir := filepath.Join(cwd, workspace.StateDir)
    cloner := &workspace.Cloner{}
    _, err = cloner.CloneWith(cmd.Context(), cloneURL, niwaDir,
        workspace.CloneOptions{Depth: 1}, workspace.NewReporter(os.Stderr))
```

- `workspace.StateDir = ".niwa"` (`internal/workspace/state.go:19`) — same constant doubles as the state directory marker.
- `Cloner.CloneWith` (`internal/workspace/clone.go:43`) invokes `git clone --depth 1 <url> <cwd>/.niwa`.
- After the clone, the directory is treated as both the workspace config directory and a full git working tree. `Depth: 1` is the only "snapshot-ish" hint, but it's still a real git repo — `.git/` is present, `git pull` works, the user can edit and commit locally.
- Post-flight: `config.Load(.niwa/workspace.toml)` parses the file the clone is expected to contain (`init.go:144`).
- The clone protocol comes from `globalCfg.Global.CloneProtocol`, defaulting to `"ssh"` (`internal/config/registry.go:42`).

Then `init` registers a `RegistryEntry` (`init.go:177-185`):

```go
entry := config.RegistryEntry{
    Root:   absRoot,                    // cwd
    Source: absConfigPath,              // cwd/.niwa/workspace.toml
}
if source != "" {
    entry.SourceURL = source            // raw "--from" arg, untransformed
}
globalCfg.SetRegistryEntry(registryName, entry)
config.SaveGlobalConfig(globalCfg)
```

Finally, `init` may write an instance state file at `<cwd>/.niwa/instance.json` via `buildInitState` + `SaveState` (`init.go:280-340`). State is written when `--skip-global`, `--no-overlay`, `--overlay`, or any clone happened. This is the workspace-root state file — not an instance directory's state.

**Overlay clone at init**:
- `--overlay <slug>`: explicit; `init.go:295-310` calls `workspace.CloneOrSyncOverlay(initOverlay, overlayDir)` and records `OverlayURL` and `OverlayCommit` (HEAD SHA) in `InstanceState`. Failure here is a hard error.
- Convention discovery (`init.go:313-336`): when init is in `modeClone`, niwa derives a candidate overlay URL from the source via `config.DeriveOverlayURL` (e.g. `org/repo` → `org/repo-overlay`), tries to clone it, and silently skips on failure.

### 2. Apply-time sync

**Entry point**: `internal/cli/apply.go:runApply` → `applier.Apply(ctx, cfg, configDir, instanceRoot)`

The first thing `Applier.Apply` does (apply.go:271-278):

```go
if syncErr := SyncConfigDir(configDir, a.Reporter, a.AllowDirty); syncErr != nil {
    return syncErr
}
```

`SyncConfigDir` (`internal/workspace/configsync.go`):

1. If `<configDir>/.git` doesn't exist → return nil (treat as local-only).
2. If no origin remote → return nil.
3. If working tree is dirty AND `!allowDirty` → hard error: `"config directory has uncommitted changes ... Use --allow-dirty"`.
4. Otherwise: `git -C <configDir> pull --ff-only origin`. **This is failure-mode #1 in issue #72**.

The error from `git pull --ff-only` propagates up to `runApply` and aborts the apply for that instance (collected into `applyErrors` per-instance, but for a single instance run the user sees: `error: applying to <workspace>: pulling config from origin: exit status 128`).

Then `Applier.runPipeline` (apply.go:396) handles overlay sync at "Step 0.5" (apply.go:435-493):

- `opts.noOverlay`: skip entirely.
- `opts.overlayURL` set in state: call `a.cloneOrSync(opts.overlayURL, dir)` (defaults to `CloneOrSyncOverlay`). Sync failure → hard error with `"workspace overlay sync failed. Use --no-overlay to skip."` **This is failure-mode #1 propagated from the overlay clone**.
- Else if `opts.configSourceURL` set: convention discovery via `DeriveOverlayURL`. A fresh clone failure is silent skip; a pull failure on an existing clone is a hard error.

`CloneOrSyncOverlay` (`overlaysync.go:22`):
- If dir is not a valid git repo → `git clone <cloneURL> <dir>` (no `--depth`, suppressed output).
- Else → `git -C <dir> pull --ff-only` (suppressed output to honor R22 not-leaking-overlay-name rule).
- `isValidGitDir` checks `.git` exists AND `git rev-parse HEAD` succeeds (`overlaysync.go:55`).

There's also a third `pull --ff-only` site at `internal/workspace/sync.go:86` (`PullRepo`), used inside `SyncRepo` for managed source repos cloned under each instance. The lead description correctly excludes this one — those *are* working trees the user develops in, gated by `--no-pull` and a thorough dirty-check / on-default-branch / no-tracking-branch / ahead-vs-behind decision tree.

After overlay sync, `runPipeline` parses `<overlayDir>/workspace-overlay.toml` (apply.go:511) and merges it into `cfg`. Overlay also contributes `CLAUDE.overlay.md` (`workspace_context.go:107`) and per-repo content files from arbitrary paths inside the overlay clone.

The `Applier.GlobalConfigDir` field (sync'd at apply.go:609-615 via `SyncConfigDir`) carries the personal-overlay clone path — same pull-or-skip semantics on a separate clone at `~/.config/niwa/global/`. This is a *third* location that uses `git pull --ff-only` semantics:

```go
if a.GlobalConfigDir != "" && !opts.skipGlobal {
    a.Reporter.Status("syncing config...")
    if syncErr := SyncConfigDir(a.GlobalConfigDir, a.Reporter, a.AllowDirty); syncErr != nil {
        a.Reporter.Warn("could not sync config: %v", syncErr)
        return nil, fmt.Errorf("syncing global config: %w", syncErr)
    }
}
```

Issue #72 names two clones (team config at `<workspace>/.niwa/` and the personal/global overlay), but `SyncConfigDir` is **the same code path serving both**. The workspace-overlay clone (at `~/.config/niwa/overlays/<org>-<repo>/`) is the third, served by `CloneOrSyncOverlay`. All three are full working trees; all three use `git pull --ff-only`. The two affected by #72 are `SyncConfigDir` (used for team config + personal overlay) and `CloneOrSyncOverlay`.

### 3. `niwa config set global`

**Entry point**: `internal/cli/config_set.go:runConfigSetGlobal`

Flow (config_set.go:38-80):

1. Resolve clone URL via `workspace.ResolveCloneURL(repo, globalCfg.CloneProtocol())`.
2. `os.RemoveAll(globalConfigDir)` if present — re-running `set` always nukes the prior clone.
3. `Cloner.Clone` (no `--depth`, full clone) into `globalConfigDir` = `$XDG_CONFIG_HOME/niwa/global` (`registry.go:196`).
4. Persist:

```go
globalCfg.GlobalConfig = config.GlobalConfigSource{Repo: repo}
```

That's it. The slug is **stored as a raw string** (TOML field `repo` on `[global_config]`); `GlobalConfigSource` has only one field (`registry.go:22`). No structured parsing, no ref pinning, no subpath, no last-sync timestamp.

The local clone path is **derived at runtime** from XDG and **never stored** — the comment at `registry.go:21` is explicit:
> The local clone path is derived at runtime from XDG_CONFIG_HOME so it is never stored.

**Read path**: `Applier.GlobalConfigDir` is set in three callers (`cli/apply.go:117`, `cli/create.go:144`, `cli/reset.go:114`). All three call `config.GlobalConfigDir()` and assign the result unconditionally if it resolves — they don't check `globalCfg.GlobalConfig.Repo` for emptiness. The `niwa.toml` reader in `apply.go:638-649` silently skips a missing file, which is what makes the unconditional assignment safe but also what hides a misregistered overlay.

`niwa config unset global` (`internal/cli/config_unset.go`) does the inverse: `os.RemoveAll(globalConfigDir)` and clears `GlobalConfigSource{}` in the registry.

### 4. Workspace registry schema

`~/.config/niwa/config.toml` is parsed into `GlobalConfig` (`internal/config/registry.go:14-36`):

```go
type GlobalConfig struct {
    Global       GlobalSettings           // [global]
    GlobalConfig GlobalConfigSource       // [global_config]
    Registry     map[string]RegistryEntry // [registry.<name>]
}

type GlobalSettings struct {
    CloneProtocol string `toml:"clone_protocol,omitempty"` // "ssh" or "https"
}

type GlobalConfigSource struct {
    Repo string `toml:"repo,omitempty"` // raw slug/URL string
}

type RegistryEntry struct {
    Source    string `toml:"source"`               // abs path to workspace.toml on disk
    Root      string `toml:"root"`                 // abs path to workspace root
    SourceURL string `toml:"source_url,omitempty"` // raw --from value (slug or URL)
}
```

Per-workspace fields persisted: just `Source`, `Root`, `SourceURL`. No ref, no subpath, no commit pin, no last-sync time, no source-kind discriminator, no health flag.

**Invariants** (mostly implicit):

- `Source` should be `<Root>/.niwa/workspace.toml`; this is enforced because `init.go:144` and `apply.go:140-142` always derive `Source` as `filepath.Join(configDir, WorkspaceConfigFile)` from the cloned `.niwa/` dir.
- `SourceURL` is preserved across applies via the explicit "preserve" comment block in `apply.go:217-219` of `updateRegistry`. This is the only thing keeping convention overlay discovery alive across multiple applies.
- `RegistryEntry` keys are workspace names, validated by `validRegistryName` at read time (`registry.go:84`) — control characters and Unicode Cf/Zl/Zp are filtered on `RegisteredNames()` but never blocked at write time.
- Names containing slashes / shell metacharacters / paths are technically allowed because `SetRegistryEntry` doesn't validate.

`config.Discover(startDir)` (`internal/config/discover.go:18`) walks up from `startDir` looking for `.niwa/workspace.toml` — this is the "find the workspace from cwd" mechanism that powers create, apply, status, destroy, reset, and go.

### 5. State file (`.niwa/instance.json`)

`InstanceState` (`internal/workspace/state.go:56-73`):

```go
type InstanceState struct {
    SchemaVersion  int                  // 2 (v1 supported via shim)
    ConfigName     *string
    InstanceName   string
    InstanceNumber int
    Root           string
    Detached       bool
    SkipGlobal     bool
    OverlayURL     string               // raw slug/URL of workspace overlay
    NoOverlay      bool
    OverlayCommit  string               // HEAD SHA at last apply (7+ chars)
    Created        time.Time
    LastApplied    time.Time
    ManagedFiles   []ManagedFile
    Repos          map[string]RepoState
    Shadows        []Shadow
    DisclosedNotices []string
}
```

What it records about config sources:

- **Workspace overlay**: `OverlayURL` (raw string) and `OverlayCommit` (HEAD SHA at last successful sync). Used to detect "overlay has new commits since last apply" (apply.go:459-466) and emit a stderr note.
- **Team config (the `.niwa/` clone itself)**: nothing. No URL, no ref, no commit pin. The team config is identified solely by being on disk; the registry's `RegistryEntry.SourceURL` is the only indirect link to its remote.
- **Global/personal overlay**: nothing in the per-instance state. `Applier.GlobalConfigDir` is wired from `config.GlobalConfigDir()` at every apply; the slug lives only in `GlobalConfig.GlobalConfig.Repo`. No commit pin, no last-sync.
- **Source repos** (managed via `[[sources]]`): `RepoState{URL, Cloned bool}` per repo. URL is the clone URL used; no SHA tracked.

`SaveState` writes JSON with `MarshalIndent` to `<dir>/.niwa/instance.json` at mode 0o644 (state.go:211-227). State files exist at three layers: workspace root (init-time flags), per-instance root (full state), and the special workspace-root state for `DisclosedNotices` propagation across instances (`apply.go:1172-1182`).

### 6. Failure recovery

There is **no `niwa repair`, `niwa reset config`, or `niwa config refresh`** command. The only recovery primitive is `niwa reset <instance>` (`internal/cli/reset.go`), which destroys and recreates **an instance** (not the config dir). It even refuses to run if `<workspaceRoot>/.niwa/.git` is absent (`reset.go:84-87`):

> `cannot reset a local-only workspace; the config would be lost. Use destroy + init instead.`

Recovery scenarios today:

- **`.niwa/` corrupted/wedged via #72 fast-forward divergence**: there is no in-tool fix. The user must `cd <workspaceRoot>/.niwa && git fetch origin && git reset --hard origin/<branch>` manually. There's no documentation pointing at this; the error message just says "exit status 128".
- **`.niwa/` deleted**: apply fails on `config.Load`. User must re-run `niwa init <name> --from <slug>` from a parent dir, but `CheckInitConflicts` (`preflight.go:49`) will refuse if there's an `.niwa/instance.json` from a workspace-root state file. The path of least resistance is `rm -rf .niwa && niwa init`.
- **Personal overlay clone wedged at `~/.config/niwa/global/`**: same #72 failure. Workaround is `niwa config unset global && niwa config set global <slug>` — `set` always nukes and re-clones. The workspace-overlay clone at `~/.config/niwa/overlays/<dirname>/` has no equivalent unset/reset; the user must `rm -rf` manually.
- **Source repo dirty**: `SyncRepo` (`sync.go:117`) returns `SyncResult{Action: "skipped", Reason: "dirty working tree"}`, which is *informational only*, not a hard error. This is good ergonomics — but it's only present for source repos, not for `.niwa/` or the overlay clones, which is exactly the asymmetry #72 is complaining about.
- **`--allow-dirty` flag on apply**: only affects `SyncConfigDir`. It bypasses the dirty-check refusal but **does not** rescue the user from a fast-forward failure — `git pull --ff-only` still runs and still fails when origin diverges. The flag exists for the "I edited `.niwa/workspace.toml` to test something locally" case; it's not a recovery flag.

### 7. Discoverable surprises

**Things that contradict the snapshot redesign**:

- **`isClonedConfig` (reset.go:131)** explicitly checks `<configDir>/.git` is a directory to gate whether reset is allowed. If we drop `.git/` from the snapshot, this guard breaks: every workspace becomes "local-only" and reset starts refusing universally. The check is conceptually asking "did this config come from a remote?" but uses `.git/` as a proxy. The redesign needs a non-git marker (e.g., a sidecar `.niwa/.source.json` describing origin) to keep this check meaningful.
- **`CheckGitHubPublicRemoteSecrets` (`internal/guardrail/githubpublic.go:75`)** runs `git -C <configDir> remote -v` to enumerate remotes for the public-repo plaintext-secrets guardrail. With no `.git/` in the snapshot, the function returns `haveGit=false` and prints `"warning: no git remotes detected; public-repo guardrail skipped"`. The guardrail silently disables. This is a meaningful security regression unless we surface the source URL through a different channel (registry already has `SourceURL`; redirect the guardrail there).
- **`CloneOrSyncOverlay` is exported and called from `cli/init.go:301`** for explicit `--overlay`, plus from inside the apply pipeline. Both call sites need to migrate to whatever the snapshot primitive becomes.
- **Materializers and content readers all assume the overlay/global clones are filesystem trees with stable file paths.** `InstallOverlayClaudeContent` reads `<overlayDir>/CLAUDE.overlay.md`; `InstallGlobalClaudeContent` reads from `globalConfigDir`; `ParseOverlay` reads `<overlayDir>/workspace-overlay.toml`; `MergeGlobalOverride` accepts `globalConfigDir` to resolve hook script paths to absolute paths (`override.go:438-461`); `OverlayContentRepoConfig` references arbitrary subpaths inside the overlay (`overlay.go:64-73`). All of these continue to work with snapshot-as-directory; none assume `.git/`. The redesign can keep the on-disk shape intact even if the materialization mechanism changes.
- **`DeriveOverlayURL` (`config/overlay.go:202`) and `OverlayDir` (`overlay.go:293`) presume "the overlay is a separate whole repo named `<thing>-overlay`."** Subpath-based discovery breaks both: a `niwa.toml` inside `tsukumogami/vision` doesn't have an "overlay" sibling repo. The convention layer needs to be replaced or extended, not just adjusted.
- **`computeInstanceName` (`create.go:50`)** treats the config name as the instance directory base name, scanning siblings of `<workspaceRoot>/<configName>` to compute `-2`, `-3`, etc. This is independent of the source mechanism and unchanged by the redesign.
- **`workspace.StateDir = ".niwa"` is overloaded**: it's both (a) where `workspace.toml` lives in a workspace root and (b) where `instance.json` lives in an instance directory. They're the same constant, used in `Scaffold`, `Cloner` target, `LoadState`/`SaveState`, `EnumerateInstances`, etc. The init clone lands directly at `<cwd>/.niwa/` because that's where `workspace.toml` belongs. With subpath sourcing, the snapshot would only contain the niwa config bytes — but the *location* `<cwd>/.niwa/` is what `config.Discover` walks up to find. That coupling can stay; the change is internal to the snapshot mechanism.
- **The `--allow-dirty` flag** loses meaning under a snapshot model: there's no working tree to be dirty. Either remove the flag or repurpose it for "don't refresh, use whatever's on disk."

**Things that quietly help the redesign**:

- The `Cloner` is already abstracted (`internal/workspace/clone.go:19`), used uniformly for all three clone targets. Replacing its implementation under the snapshot model is a single-file change for the primitive (though call sites still need adapting).
- `Reporter` already routes git output through a TTY-aware reporter (`reporter.go`); it can absorb whatever snapshot mechanism replaces clone.
- Apply already records `OverlayCommit` in state (state.go:66), so the SHA is not a foreign concept. Extending state to also record team-config SHA and personal-overlay SHA is mechanical.
- `RegistryEntry.SourceURL` already preserves the original slug across applies (apply.go:215-219). The plumbing for "remember where the team config came from" already exists; the redesign needs to broaden it (subpath, ref, host) but not invent it.

## Implications

**Most invasive area**: the three sync primitives — `SyncConfigDir`, `CloneOrSyncOverlay`, and the implicit dirty-check / pull-ff-only assumption baked into `Applier.Apply` and `runPipeline`. Issue #72's surface-level fix (swap `pull --ff-only` for `fetch + reset --hard`) is mechanical, but the deeper redesign — disposable snapshots, subpath-aware materialization, convention discovery — fans out into:

1. The clone primitive (`Cloner.CloneWith`) needs a peer or replacement that does "fetch only this subpath at this ref into a snapshot directory."
2. The state schema (`InstanceState`, `RegistryEntry`, `GlobalConfigSource`) must expand to record `(host, owner, repo, subpath, ref, commit)` for each of the three source-tracked clones.
3. The slug grammar consumed by `niwa init --from`, `niwa config set global`, and `niwa create <name>` must accept subpath syntax (and the redesign should pick a syntax compatible with peer tools — see lead-peer-tool-survey).
4. `DeriveOverlayURL` / `OverlayRepoName` / `OverlayDir` need to either generalize to subpath or be retired in favor of convention-based discovery inside an existing repo (lead-discovery-conventions).
5. `isClonedConfig` and `CheckGitHubPublicRemoteSecrets` need a non-git source-of-truth for "where did this config come from" — likely a sidecar marker in the snapshot directory that records origin URL and ref.

**What can be left untouched**:

- `config.Discover` (walks up from cwd to find `.niwa/workspace.toml`). The location stays.
- The full apply pipeline post-snapshot (Steps 1-7 in `runPipeline`): discover, classify, clone source repos, materialize content, run setup scripts, hash files, write state. None of this cares whether the config bytes came from a working tree, a tarball, or a subpath fetch.
- All materializers (`HooksMaterializer`, `SettingsMaterializer`, `EnvMaterializer`, `FilesMaterializer`).
- Vault resolution, provider auth injection, R12/R14/R30 guardrails (modulo the public-remote check above).
- The instance state / managed-files / drift / rotation tracking machinery.
- The `[[sources]]` repo cloning and `SyncRepo` for managed source repos (explicitly out of scope).
- `niwa destroy` (per-instance), `niwa go`, `niwa status` — none of these touch the source-clone semantics.

**Hidden coupling the redesign will break**:

- `niwa reset` refuses to operate on "local-only" workspaces using `.git/` presence as the test. Needs replacement.
- The plaintext-secrets guardrail uses `git remote -v` to identify the source. Needs replacement.
- `--allow-dirty` becomes meaningless on the snapshot side. Decide: drop the flag, or keep it as a no-op alias for back-compat.
- The "did the overlay advance since last apply" stderr note (apply.go:459-466) reads `OverlayCommit` and compares to the live HEAD — needs the SHA-at-snapshot equivalent under the new model.
- `Cloner.CloneWith` is also used by `init` for the initial config clone and by `niwa config set global` for the overlay clone — three different call sites, all assuming a working tree. They need to migrate together; a partial migration leaves a mix of working-tree and snapshot directories with inconsistent recovery semantics.
- `Reporter` already does sophisticated git-output routing; if the snapshot mechanism stops using `git` entirely (e.g., GitHub tarball API), output routing needs a parallel path.

## Surprises

- **Three clone sites, not two**: issue #72 names the team config and the personal overlay, but there's also the **workspace overlay** (the `<configRepo>-overlay` convention), with its own `OverlayURL`/`OverlayCommit` state and its own clone at `~/.config/niwa/overlays/<org>-<repo>/`. It uses `CloneOrSyncOverlay` (the same code as the personal overlay), so the redesign needs to handle three clone semantics, not two. All three are full working trees; all three exhibit the same wedge mode under remote rewrite.
- **The `.niwa/` directory is overloaded**. It's the workspace config dir at the workspace root AND the instance state dir at every instance. Both use the same constant `StateDir`. This is fine in practice because they're at different levels of the tree, but it means "the snapshot" and "state.json" share a parent — and `SaveState`'s `os.MkdirAll(stateDir)` runs on the same directory the clone lives in.
- **`isClonedConfig` uses `.git/` presence as the proxy for "came from a remote"**. This is the most invasive surprise: removing `.git/` from the snapshot quietly disables `niwa reset` for everyone. The redesign needs an explicit source marker.
- **The plaintext-secrets guardrail also depends on `.git/`** via `git remote -v`. Same regression class.
- **The local clone path of the personal overlay is intentionally not stored** ("derived at runtime from XDG_CONFIG_HOME so it is never stored", `registry.go:21`). With subpath sourcing this is harder: two `niwa config set global` calls for two different subpaths of the same repo would collide in the same `~/.config/niwa/global/` dir. Either the dir name needs to encode subpath, or only one global overlay can exist per host (today's invariant — possibly OK to keep).
- **`SourceURL` on `RegistryEntry` is preserved on every apply via an explicit "preserve" block**. This is what keeps convention overlay discovery working — without it, `cfg.Workspace.Name` lookup would lose the original `--from` value after the first apply and the overlay would silently disappear. The redesign needs to keep this preservation pattern (or move the source identity entirely out of the registry into the snapshot's sidecar marker).
- **`niwa init --from` accepts shorthand like `org/repo` AND a full URL AND an SSH URL AND a file:// URL** (`ResolveCloneURL` in `clone.go:90` and `parseOrgRepo` in `overlay.go:227`). The grammar is permissive and inconsistent: parser branches differ between `init` and `OverlayDir`. Subpath syntax needs to fit cleanly into all parsers.
- **`Depth: 1` on the init clone but full clone for `niwa config set global`** (compare init.go:137 with config_set.go:65). The team config is shallow; the personal overlay is full. No documented reason; likely an oversight. The redesign could unify on shallow-snapshot semantics for both.
- **`niwa reset` reuses `Applier.Create` to recreate the instance, not `Apply`**. So the reset path goes through init-state-loading at the workspace root (apply.go:205) rather than reading the destroyed instance's prior state. That's working-as-intended but worth noting: any source-identity info we want preserved across reset must live at the workspace root, not just in the instance directory.

## Open Questions

1. **Should the snapshot directory name change from `.niwa/` to something less suggestive of a working tree?** Users who see `.niwa/` and recognize it as a clone are tempted to edit. A sibling like `.niwa/source/` (snapshot inside) plus `.niwa/instance.json` (state outside) would visually separate the two — but breaks `config.Discover` and a lot of test fixtures.
2. **Where should the source-identity marker live?** Options: (a) sidecar JSON in the snapshot dir (`.niwa/.source.json`), (b) a new field on `RegistryEntry`, (c) extend `InstanceState` (currently empty for team config). The choice affects what `niwa reset` and the public-remote guardrail use as their source-of-truth.
3. **Does `--allow-dirty` survive the redesign?** With no working tree, the flag is meaningless. Removing it is a breaking change for users who pass it in scripts; aliasing it to a no-op is more friendly but adds dead surface.
4. **Should `niwa config set global` continue to support full repos, or only subpaths?** The user's stated direction is "subpath = '/' is the degenerate whole-repo case," which suggests no config split — same code path for both. But the dir-name-collision question above needs resolution.
5. **What happens when convention discovery finds two competing markers in a brain repo (both `niwa.toml` AND `.niwa/`)?** Question for lead-discovery-conventions, but it intersects this lead because today the only "marker" is "`.niwa/workspace.toml` exists at the cloned dir."
6. **Does the "overlay has new commits since last apply" stderr note (apply.go:459-466) survive?** It compares state's `OverlayCommit` to live HEAD — the snapshot model can preserve this by recording the snapshot SHA in state. But the user-visible output ("was abc1234, now def5678") is git-flavored; under a tarball-based snapshot we'd need a different identifier.
7. **Today the overlay clone path is `~/.config/niwa/overlays/<org>-<repo>/` (collision-free across single-org users, but flat).** Subpath sourcing might want `~/.config/niwa/overlays/<org>-<repo>-<subpath-hash>/` or similar. Open question for lead-identity-and-state.

## Summary

niwa today materializes three independent git-hosted config sources — the team config at `<workspace>/.niwa/`, the workspace overlay at `~/.config/niwa/overlays/<dirname>/`, and the personal overlay at `~/.config/niwa/global/` — as full working trees synced with `git pull --ff-only`, with only `--allow-dirty` as an escape hatch and no in-tool recovery for the wedge mode in issue #72. The redesign is most invasive at the three clone primitives (`SyncConfigDir`, `CloneOrSyncOverlay`, and the per-call-site `Cloner.CloneWith` invocations) and at the source-identity surfaces (`RegistryEntry`, `GlobalConfigSource`, `InstanceState`), with two non-obvious dependencies — `niwa reset`'s `isClonedConfig` check and the plaintext-secrets guardrail's `git remote -v` enumeration — that quietly use `.git/` presence as the source-of-truth and will silently regress unless a non-git origin marker is introduced. The biggest open question is where the source-identity marker (origin URL, subpath, ref, snapshot SHA) should live so it survives the snapshot model without `.git/`: a sidecar in the snapshot dir, an expanded `RegistryEntry`, or an enriched `InstanceState`.
