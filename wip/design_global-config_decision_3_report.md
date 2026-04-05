# Decision 3: CLAUDE.global.md injection mechanism

## Chosen: Option A — Copy file + inject @import (mirrors workspace-context.md pattern)

## Rationale

Option A fits the existing codebase without adding abstraction layers. The
`workspace-context.md` pattern in `workspace_context.go` is the exact same
operation — copy a generated or sourced file to the instance root, then call
`ensureImportInCLAUDE()` to add an `@import` directive. The pipeline already
has a clear extension point (Step 4.5 in `runPipeline`), the tracking model
(accumulate paths in `writtenFiles`) is already in place, and `ensureImportInCLAUDE`
handles idempotency and missing files without modification.

## How It Works

At apply time (in `runPipeline`, immediately after Step 4.5 `InstallWorkspaceContext`):

1. **Locate source.** The call site receives a `globalConfigDir string` (the
   local clone of the global config repo, e.g. `~/.niwa/global-config`). Derive
   `globalClaudePath := filepath.Join(globalConfigDir, "CLAUDE.global.md")`.

2. **Guard: file must exist.** If `os.Stat(globalClaudePath)` returns
   `os.IsNotExist`, skip silently — zero effect on workspaces without global
   config. No error, no warning. This is the same "optional file" contract as
   `InstallWorkspaceContent` returning `nil, nil` when `source == ""`.

3. **Guard: instance opt-out.** If the instance config carries a `SkipGlobal`
   flag (or the CLI flag is `--skip-global`), skip the step entirely without
   writing or modifying anything.

4. **Copy.** Read `CLAUDE.global.md` and write it verbatim to
   `filepath.Join(instanceRoot, "CLAUDE.global.md")`. No template expansion —
   global config is personal instructions, not workspace-relative content.

5. **Inject @import.** Call `ensureImportInCLAUDE(claudePath, "@CLAUDE.global.md")`.
   The function already handles idempotency (no-op if the line is present), and
   the existing `return nil` on missing `CLAUDE.md` means it only runs if
   `InstallWorkspaceContent` already placed the workspace `CLAUDE.md`.

6. **Track.** Append `destPath` (the copied file) to `writtenFiles`. The `CLAUDE.md`
   modification is already tracked because `InstallWorkspaceContent` returned it
   earlier. The `cleanRemovedFiles` mechanism will delete `CLAUDE.global.md` on
   the next apply if `globalConfigDir` is absent or opt-out is set.

The new helper function is approximately 30 lines in `workspace_context.go`:

```go
// InstallGlobalClaudeContent copies CLAUDE.global.md from the global config
// clone to the instance root and adds an @import directive to the workspace
// CLAUDE.md. Returns nil, nil if the source file doesn't exist.
func InstallGlobalClaudeContent(globalConfigDir, instanceRoot string) ([]string, error) {
    src := filepath.Join(globalConfigDir, "CLAUDE.global.md")
    if _, err := os.Stat(src); os.IsNotExist(err) {
        return nil, nil
    }

    data, err := os.ReadFile(src)
    if err != nil {
        return nil, fmt.Errorf("reading CLAUDE.global.md: %w", err)
    }

    dest := filepath.Join(instanceRoot, "CLAUDE.global.md")
    if err := os.WriteFile(dest, data, 0o644); err != nil {
        return nil, fmt.Errorf("writing CLAUDE.global.md: %w", err)
    }

    claudePath := filepath.Join(instanceRoot, "CLAUDE.md")
    if err := ensureImportInCLAUDE(claudePath, "@CLAUDE.global.md"); err != nil {
        return nil, fmt.Errorf("adding CLAUDE.global.md import: %w", err)
    }

    return []string{dest}, nil
}
```

The `runPipeline` call in `apply.go` adds one block (~5 lines) after Step 4.5,
calling `InstallGlobalClaudeContent` when `globalConfigDir != ""`. The
`globalConfigDir` is threaded in as an argument to `runPipeline`, or placed in
the `Applier` struct if it is always known at applier construction time.

### Lifecycle of the managed files

| File | Created by | Removed by |
|------|-----------|-----------|
| `CLAUDE.global.md` | `InstallGlobalClaudeContent` (step 4.5+) | `cleanRemovedFiles` when no longer in `writtenFiles` |
| `@import` line in `CLAUDE.md` | `ensureImportInCLAUDE` | Left in place (the @import refers to a file that no longer exists; Claude Code silently ignores missing imports) |

The `@import` line in `CLAUDE.md` is not removed when global config is
unregistered. This is the same behavior as `@workspace-context.md` — the
import line is idempotent and harmless when the target is absent. If clean
removal of the import is needed later, a `removeImportFromCLAUDE` counterpart
can be added, but this is not required by the PRD.

### --skip-global opt-out

Add `SkipGlobal bool` to `InstanceConfig` in `config.go` and a `--skip-global`
CLI flag. When true, `InstallGlobalClaudeContent` is not called, so neither the
file nor the import is written. On a subsequent apply without `--skip-global`,
they are written as normal.

If `SkipGlobal` is meant to persist across applies (i.e., set once and remembered),
it must be stored in `InstanceState`. This is an open question (see below).

## Alternatives Rejected

**Option B: Treat as a managed file entry in global config TOML.** This makes
the @import injection a separate step that needs to know about CLAUDE.md
semantics, rather than having one cohesive function that does both. The files
materializer doesn't currently call `ensureImportInCLAUDE` for any file it
copies — adding that awareness there would bleed CLAUDE.md-specific logic into
a general-purpose materializer. Option A keeps the concern localized to
`workspace_context.go`, which already owns the `ensureImportInCLAUDE` pattern.

**Option C: New `global` content level in ContentConfig.** This is the most
architecturally ambitious option and adds the most infrastructure: a new struct
field in `ContentConfig`, new merge semantics between global and workspace
content, new code paths in the content installation pipeline. The benefit —
composing global content with workspace content at the config level — is not
required by the PRD, which says the global file is additive and doesn't replace
shared content. Option A delivers the same additive behavior with a fraction of
the code change.

## Assumptions

- `globalConfigDir` is the local clone path, already resolved by the time
  `runPipeline` runs. (The global config sync step happens before `Apply` is
  called, following the pattern of `SyncConfigDir`.)
- `CLAUDE.global.md` is copied verbatim. No template expansion is needed because
  the file contains personal instructions that don't reference workspace paths.
- The instance workspace `CLAUDE.md` already exists when Step 4.5+ runs.
  `InstallWorkspaceContent` (Step 4) creates it. If there is no workspace content
  source, `CLAUDE.md` may not exist, and `ensureImportInCLAUDE` returns `nil`
  silently — the @import is not added but the file is still copied. This is
  acceptable since Claude Code reads `CLAUDE.global.md` from the instance root
  regardless; it just won't be imported by `CLAUDE.md`.
- The PRD's "tracked as a managed file" requirement means only the copied file
  (`CLAUDE.global.md`) needs to be in `writtenFiles`. The `@import` line in
  `CLAUDE.md` is already tracked because `CLAUDE.md` itself is a managed file.

## Open Questions

1. **Should `--skip-global` persist in `InstanceState`?** If yes, add a
   `SkipGlobal bool` field to `InstanceState` and read it back on apply.
   If no, the flag must be passed on every apply call, which is inconvenient.
   The workspace-specific opt-out use case (one workspace where global config
   is intentionally excluded) suggests persistence is the right default.

2. **Should removing the @import line be supported?** If a user unregisters
   their global config, the copied file is cleaned up but the `@import` line
   stays in `CLAUDE.md`. Claude Code ignores missing imports silently, so this
   is not a functional problem. But it may confuse users who inspect their
   `CLAUDE.md`. A `removeImportFromCLAUDE` function would address this.

3. **Where is `globalConfigDir` threaded from?** It could be a field on
   `Applier` (set at construction time from global config), or a parameter to
   `runPipeline`. The `Applier` field approach is cleaner if global config is
   always resolved before Apply is called.
