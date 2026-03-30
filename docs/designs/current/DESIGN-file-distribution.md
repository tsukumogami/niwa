---
status: Current
problem: |
  niwa distributes hooks, settings, and env files through dedicated materializers,
  but some workflows need to copy arbitrary files into repos (plugin extensions,
  command fragments, templates). Each new file type would require a new materializer.
  Files landing in git repos also need .local-aware renaming to stay gitignored,
  but source files in the config repo can't use .local naming without being ignored
  by the config repo's own git.
decision: |
  Add a [files] section with inline source=destination mappings. Files are
  automatically renamed with .local inserted before the extension when the
  destination is a directory. Explicit destination filenames bypass renaming.
  Per-repo overrides use the same key-value semantics as other config sections.
rationale: |
  The inline mapping format is consistent with existing niwa patterns (env vars,
  settings) and gives natural merge semantics. Auto-renaming with opt-out matches
  the existing CLAUDE.md convention while keeping the common case zero-config.
  Tracking distributed files in instance.json reuses the existing drift detection
  infrastructure.
---

# DESIGN: File Distribution

## Status

Proposed

## Context and Problem Statement

niwa has dedicated materializers for hooks (copies scripts to `.claude/hooks/`),
settings (generates `settings.local.json`), and env (writes `.local.env`). Each
understands its own file format, naming convention, and merge semantics.

But some workflows need to copy arbitrary files into repos. Plugin extension files
go to `.claude/shirabe-extensions/`, command fragments to `.claude/commands/`,
configuration templates to repo subdirectories. Today, each new file type would
require writing a new materializer with its own config types, tests, and pipeline
integration. This doesn't scale.

The second problem is `.local`-aware renaming. Target repos gitignore `*.local*`
to keep niwa-generated files out of version control. Files distributed by niwa
must follow this pattern or they'll show up as untracked. But source files in the
`.niwa` config repo can't use `.local` naming -- they'd be ignored by the config
repo's own git. niwa's content system already handles this for CLAUDE.md files
(source `workspace.md` -> installed as `CLAUDE.local.md`), but there's no
equivalent for arbitrary files.

## Decision Drivers

- One mechanism for all file types, not a new materializer per file type
- `.local` renaming should be automatic in the common case, not manual per file
- Consistent with existing config patterns (key-value mappings, per-repo overrides)
- Distributed files should be tracked for drift detection and cleanup on removal
- Source path traversal must be rejected (same security boundary as other materializers)

## Considered Options

### Decision 1: TOML schema for file distribution

Users need a way to declare "copy this file from the config directory to this
path in each repo" in workspace.toml. The schema must support workspace-level
defaults, per-repo overrides, and both file and directory sources.

#### Chosen: Inline table mapping

Source-to-destination key-value pairs under `[files]`:

```toml
[files]
"extensions/design.md" = ".claude/shirabe-extensions/"
"extensions/plan.md" = ".claude/shirabe-extensions/"
"commands/" = ".claude/commands/"
```

Per-repo overrides follow the same pattern:

```toml
[repos.vision.files]
"extensions/strategic.md" = ".claude/shirabe-extensions/"

# Remove a workspace-level mapping for this repo
"commands/" = ""
```

**Merge semantics:** Source path is the key. Workspace-level `[files]` provides
defaults, repo-level `[repos.X.files]` overrides by source key. Empty string
removes a workspace-level mapping. This is the same pattern used by env vars
and settings.

**Directory sources:** Trailing slash on the source (`"commands/"`) indicates a
directory. All files within the directory are copied to the destination,
preserving the directory structure. No trailing slash means a single file.

**Extensibility:** If per-mapping options become necessary later (exclude
patterns, templating), the string value can be changed to an inline table
(`"src" = { dest = "...", exclude = [...] }`) while treating bare strings as
shorthand. This is backward-compatible.

#### Alternatives Considered

**Array of tables (`[[files]]` with src/dest fields):** Each mapping is a
separate `[[files]]` entry with `src` and `dest` keys. Extensible (easy to add
fields), but per-repo overrides are awkward -- arrays of tables have no natural
key for replacement, so merge requires matching by `src` or restating the entire
array. Also verbose: 10 mappings means 30+ lines.

**Nested by destination:** Group sources under destination table keys
(`[files.".claude/commands"]`). Optimizes for many-to-one (multiple sources to
one destination), but the common case is one-to-one, where it adds verbosity.
Destination paths as TOML table keys are visually noisy. Per-repo overrides
replace the entire source list for a destination rather than individual entries.

### Decision 2: `.local` renaming convention

Files landing in git repos need `.local` in the filename to stay gitignored.
Source files in the config repo can't use `.local` naming. The convention
determines when and how renaming happens.

#### Chosen: Auto-rename with opt-out

By default, when the destination is a **directory** (ends with `/`), niwa
inserts `.local` before the file extension during copy:

```
design.md     -> design.local.md
config.json   -> config.local.json
script.sh     -> script.local.sh
Makefile      -> Makefile.local
```

When the destination specifies an **explicit filename** (doesn't end with `/`),
niwa uses it as-is:

```toml
[files]
# Auto-renamed: design.md -> design.local.md
"extensions/design.md" = ".claude/shirabe-extensions/"

# Explicit name: no renaming
"extensions/design.md" = ".claude/shirabe-extensions/custom-name.local.md"
```

This gives zero-config `.local` renaming in the common case (destination is a
directory) while allowing full control when the default doesn't fit.

**Edge cases:**
- Files without extensions: `.local` is appended (`Makefile` -> `Makefile.local`)
- Directory sources: each file within the directory is renamed individually
- Destination inside `.claude/`: renaming still applies. Even though `.claude/`
  is typically gitignored, the `.local` pattern provides defense in depth and
  consistency

#### Alternatives Considered

**Always insert `.local` (no opt-out):** Simpler, but blocks legitimate use
cases where the destination filename matters (e.g., tool-specific config files
that must have exact names). Users would have no way to override.

**User specifies destination name (no auto-rename):** Full control, but every
mapping requires thinking about `.local` naming. The common case (just copy it
and make it gitignored) becomes the verbose case. Defeats the goal of `.local`
being natively supported.

**Pattern-based (`foo_.ext` -> `foo.local.ext`):** Follows the existing
`CLAUDE_.md` convention. But the underscore suffix is a niwa-internal convention
that shouldn't leak into the config repo's file naming. Users would have to name
their source files with an unintuitive trailing underscore.

## Decision Outcome

File distribution uses inline source-to-destination mappings under `[files]`.
Files are automatically renamed with `.local` inserted before the extension when
copied to a directory destination. Explicit destination filenames bypass renaming.

The two decisions work together: the inline mapping schema makes it easy to
declare file distribution, and the auto-rename convention means most mappings
don't need to think about `.local` at all. The opt-out (explicit filename) covers
cases where exact naming matters.

Per-repo overrides add or replace mappings by source key. Empty string removes a
workspace-level mapping for a specific repo. Distributed files are tracked in
instance.json with SHA-256 hashes, reusing the existing drift detection and
cleanup infrastructure.

## Solution Architecture

### Overview

A new `FilesMaterializer` implements the `Materializer` interface and is added to
the pipeline after the existing materializers. It reads file mappings from the
effective config, copies files from the config directory to the repo directory
with `.local` renaming, and returns the list of written paths for tracking.

### Components

**`config.WorkspaceConfig.Files`** -- new field: `map[string]string` mapping
source paths to destination paths. Same type on `RepoOverride`.

**`MergeOverrides`** -- merge `[files]` with source-key override semantics.
Empty string value means "remove this mapping." Repo extends workspace, repo
wins per key.

**`FilesMaterializer`** -- new materializer that:
1. Iterates effective file mappings
2. For each source: validates containment within config dir
3. Determines destination: if dest ends with `/`, auto-rename with `.local`;
   otherwise use as-is
4. If source is a directory (ends with `/`): walk and copy each file
5. Copies file content, creates directories as needed
6. Returns written paths for instance.json tracking

### Key Interfaces

```go
// In config.go
type WorkspaceConfig struct {
    // ... existing fields ...
    Files map[string]string `toml:"files,omitempty"`
}

type RepoOverride struct {
    // ... existing fields ...
    Files map[string]string `toml:"files,omitempty"`
}
```

```go
// In materialize.go
type FilesMaterializer struct{}

func (f *FilesMaterializer) Name() string { return "files" }
func (f *FilesMaterializer) Materialize(ctx *MaterializeContext) ([]string, error)
```

```go
// localRename inserts .local before the file extension.
// "design.md" -> "design.local.md"
// "Makefile" -> "Makefile.local"
func localRename(filename string) string
```

### Data Flow

```
workspace.toml [files]
       |
       v
MergeOverrides (source-key merge, empty = remove)
       |
       v
FilesMaterializer.Materialize()
  for each mapping:
    |
    +-- validate source containment
    |
    +-- is source a directory?
    |     yes -> walk files, copy each
    |     no  -> copy single file
    |
    +-- is dest a directory (ends with /)?
    |     yes -> auto-rename with .local
    |     no  -> use dest filename as-is
    |
    +-- write file, return path for tracking
```

## Implementation Approach

### Phase 1: Config and merge

- Add `Files map[string]string` to `WorkspaceConfig` and `RepoOverride`
- Update `MergeOverrides` with source-key override + empty-string removal
- Update scaffold template and config tests

### Phase 2: FilesMaterializer

- Implement `localRename` helper
- Implement `FilesMaterializer` with single-file copy and `.local` renaming
- Add to pipeline in apply.go
- Tests for: single file, directory dest rename, explicit dest name, path
  traversal rejection

### Phase 3: Directory sources and cleanup

- Directory source walking (trailing `/` on source)
- Instance.json tracking of distributed files
- Cleanup: removing a mapping and re-running apply deletes the installed file
- Tests for: directory copy, removal cleanup, drift detection

## Security Considerations

Source paths are validated with `checkContainment` (same as content and env file
distribution). Path traversal via `..` components or symlink escapes is rejected
before any file is read. Destination paths are validated to stay within the repo
directory. These are the same security boundaries used by the existing
materializers.

File distribution copies content verbatim -- no template expansion, no shell
execution. This limits the attack surface to the config repo itself, which is
already trusted (the user chose to clone it).

## Consequences

### Positive

- One mechanism for all file types -- no new materializer per file format
- `.local` renaming is automatic, consistent with existing CLAUDE.md convention
- Tracked in instance.json -- drift detection and cleanup work like other
  managed files
- Per-repo overrides follow the same key-value pattern as env, settings, hooks

### Negative

- The inline mapping format can't express per-file options (exclude, template
  expansion) without schema evolution to inline tables
- Auto-renaming adds a layer of indirection -- users must know that `design.md`
  becomes `design.local.md` at the destination

### Mitigations

- The schema supports future evolution to inline tables for per-file options,
  backward-compatible with the string shorthand
- The renaming rule is simple and consistent (insert `.local` before extension),
  easy to predict without documentation once the pattern is known
