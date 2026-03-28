# Decision 3: How should each materializer write files to repos?

## Decision

Each materializer receives a `MaterializeInput` containing the repo directory, the merged `EffectiveConfig`, and the config directory (`.niwa/` root). Each returns a `[]string` of written file paths. The pipeline collects these paths into `writtenFiles` for managed-file tracking, identical to how `InstallRepoContent` works today.

File-writing follows the existing `installContentFile` pattern: resolve source path relative to config dir, validate containment, `os.MkdirAll` for parent directories, `os.WriteFile` for content. No new abstractions for file I/O -- each materializer calls standard `os` functions directly.

## Chosen Approach: Direct file writes with shared input struct

### Hooks Materializer

Reads the merged `EffectiveConfig.Hooks` map (keyed by event name, values are script path lists). For each event/script pair:

1. Resolve source: `filepath.Join(configDir, scriptPath)` -- validate containment within configDir
2. Compute target: `filepath.Join(repoDir, ".claude", "hooks", eventName, filepath.Base(scriptPath))`
3. `os.MkdirAll` the target directory (`.claude/hooks/{eventName}/`)
4. Copy file contents from source to target via `os.ReadFile` + `os.WriteFile`
5. `os.Chmod(target, 0o755)` to make executable
6. Append target path to written files

The `.claude/` directory is created implicitly by `os.MkdirAll` on the hooks subdirectory. Hook scripts are copied (not symlinked) so repos remain self-contained after `niwa apply`.

### Settings Materializer

Reads the merged `EffectiveConfig.Settings` map and produces `{repoDir}/.claude/settings.local.json`:

1. Build a `map[string]any` matching Claude Code's expected schema
2. If hooks were installed, populate the `hooks` field with references to the installed script paths (relative to repo root: `.claude/hooks/{event}/{script}`)
3. Marshal to JSON with `json.MarshalIndent` (2-space indent, matching Claude Code convention)
4. `os.MkdirAll` for `.claude/` directory
5. `os.WriteFile` with mode `0o644`
6. Append target path to written files

Settings materializer runs after hooks materializer so it can reference installed hook paths. The pipeline calls materializers in deterministic order: hooks, then settings, then env.

### Env Materializer

Reads the merged `EffectiveConfig.Env`:
- `files` key: list of relative paths to env files in `.niwa/` config dir
- `vars` key (or other string keys): inline KEY=VALUE pairs

Production steps:

1. Start with empty key-value map (preserves insertion order via `[]struct{Key,Value}` slice)
2. For each file in the `files` list: resolve relative to configDir, validate containment, parse KEY=VALUE lines (skip comments/blanks), append to map (later entries override earlier)
3. For each inline var: append to map (inline vars override file-sourced vars)
4. Per-repo env entries override workspace entries (already handled by `MergeOverrides`)
5. Write `{repoDir}/.local.env` in `KEY=VALUE` format, one per line
6. Append target path to written files

### Naming Convention

All written files use the `*.local*` pattern:
- `.claude/settings.local.json` -- already follows convention
- `.local.env` -- already follows convention
- Hook scripts in `.claude/hooks/` don't need the `.local` suffix because `.claude/` itself is typically gitignored, and hooks are executable scripts not config files

### Integration with Apply Pipeline

In `runPipeline`, after Step 6 (repo content) and before Step 7 (managed file hashing):

```
// Step 6.5: Materialize config (hooks, settings, env) for each repo.
for _, cr := range classified {
    if !ClaudeEnabled(cfg, cr.Repo.Name) {
        continue
    }
    repoDir := filepath.Join(instanceRoot, cr.Group, cr.Repo.Name)
    effective := MergeOverrides(cfg, cr.Repo.Name)
    input := MaterializeInput{
        RepoDir:   repoDir,
        ConfigDir: configDir,
        Effective: effective,
    }
    for _, m := range materializers {
        files, err := m.Materialize(input)
        if err != nil {
            return nil, fmt.Errorf("materializing %s for %s: %w", m.Name(), cr.Repo.Name, err)
        }
        writtenFiles = append(writtenFiles, files...)
    }
}
```

The `materializers` slice is built in `runPipeline` (or on the `Applier` struct) with fixed ordering: `[hooksMaterializer, settingsMaterializer, envMaterializer]`. This keeps the pipeline loop clean while letting each materializer own its file-writing logic.

### Drift Detection

All written files flow into `writtenFiles`, which Step 7 already hashes into `ManagedFile` entries. On re-apply, `CheckDrift` compares hashes and warns about manual edits. `cleanRemovedFiles` deletes files from previous state that are no longer produced. No changes to drift detection needed.

## Alternatives Considered

### Virtual filesystem / writer abstraction

Wrap all file writes behind an `io/fs`-style interface for testability. Rejected because:
- The existing content installation uses `os.ReadFile`/`os.WriteFile` directly and tests use `t.TempDir()`
- Adding an abstraction layer for 3 materializers that each write 1-3 files adds complexity without proportional benefit
- Go's `testing/fstest` doesn't support writes anyway

### Symlinks instead of copies for hooks

Link hook scripts to their source in `.niwa/` instead of copying. Rejected because:
- Breaks if the workspace config directory moves or is on a different filesystem
- Repos wouldn't be self-contained after apply
- Complicates drift detection (would need to check link targets)

### Single "write all config" function

One large function that handles hooks, settings, and env together. Rejected because:
- Violates the extensibility driver (adding a new type means modifying the monolithic function)
- Harder to test each materializer in isolation

## Confidence

High. The approach directly follows the pattern established by `InstallRepoContent` and `installContentFile`. The main design surface is small: a shared input struct, ordered materializer calls, and standard file I/O. The only subtle point is settings depending on hooks output, which the fixed ordering handles cleanly.
