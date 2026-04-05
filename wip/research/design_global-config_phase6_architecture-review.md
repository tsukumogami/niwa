# Architecture Review: DESIGN-global-config

## Scope

This review covers the "Solution Architecture" and "Implementation Approach" sections of
`docs/designs/DESIGN-global-config.md` against the current codebase. The review answers
four questions: implementation clarity, missing components, phase sequencing, and simpler
alternatives.

---

## 1. Implementation Clarity

### Types — fully specified

`GlobalOverride`, `GlobalConfigOverride`, and `GlobalConfigSource` are completely
specified with TOML tags. The existing `GlobalConfig` struct already exists in
`internal/config/registry.go`; `GlobalConfigSource` becomes a new field on it under
`[global_config]`. The `InstanceState` addition (`SkipGlobal bool`) is unambiguous.
Nothing is left for the implementer to invent here.

### Merge functions — mostly specified, one gap

`ResolveGlobalOverride` and `MergeGlobalOverride` have signatures, example call sites,
and per-field merge semantics documented in Decision 1. The one gap: `MergeGlobalOverride`
returns `*config.WorkspaceConfig`, but the field coverage is not exhaustive. The doc
lists hooks, env files, env vars, plugins, and managed files. It does not specify what
happens to `ClaudeConfig.Settings` or `ClaudeConfig.Env.Promote` in the global layer.
The `InstanceConfig` struct in the codebase has the same shape as `GlobalOverride`
(both are `Claude *ClaudeConfig + Env + Files`), and `MergeInstanceOverrides` handles
`Settings` and `Env.Promote`. An implementer following the doc might either silently
omit `Settings` from the global layer or include it without documented precedent. A
one-sentence clarification per omitted field ("Settings is not settable via global
config") would close this.

### `InstallGlobalClaudeContent` — fully specified

The step sequence (check existence, copy verbatim, call `ensureImportInCLAUDE`) mirrors
`InstallWorkspaceContext` exactly. The doc's description is complete and parallel to the
existing function. No ambiguity.

### CLI commands — specified at behavior level, not signature level

The doc describes behavior for `runConfigSetGlobal` and `runConfigUnsetGlobal` in
numbered steps but does not give function signatures. Given the existing one-command-
per-file convention, an implementer can infer the structure from `init.go`. This is
acceptable for CLI code; no gap.

### `ParseGlobalConfigOverride` — referenced but not defined

The Security Considerations section calls `ParseGlobalConfigOverride` by name and
describes path-traversal validation it must perform. No signature or location is
specified in the Solution Architecture section. This is a missing interface definition
(see section 2 below).

### Hook script path resolution in `MergeGlobalOverride` — noted but not specified

The Security Considerations section states that global hook script paths must be resolved
to absolute paths at merge time inside `MergeGlobalOverride`, but the Solution
Architecture section does not include this in the function contract. An implementer
reading only the architecture section would produce a function that passes relative paths
to the materializer, which would then break at runtime because the materializer resolves
scripts relative to a single `configDir`. This is the most consequential underspecification
in the document.

---

## 2. Missing Components

### `ParseGlobalConfigOverride` (parse + validate)

The Security Considerations section identifies it by name and mandates path-traversal
validation for destination keys in `Files`. The Solution Architecture section does not
list it in new functions, does not specify its signature, and does not say which file
it lives in. Recommended addition to Solution Architecture:

```go
// internal/config/config.go (or a new internal/config/global.go)
func ParseGlobalConfigOverride(data []byte) (*GlobalConfigOverride, error)
```

The function should call the existing `validateContentSource` logic (or a parallel
`validateFilesDestination` function) for all values in `GlobalOverride.Files`.

### `allowDirty` propagation to global config sync

`SyncConfigDir(configDir, allowDirty bool)` takes an `allowDirty` flag. The apply
pipeline sequence (step 2a) shows `SyncConfigDir(globalConfigDir)` but the design
does not specify whether `--allow-dirty` applies to the global config directory. The
workspace config sync already uses `applyAllowDirty` from the CLI flag. The global
config sync should use the same flag for consistent UX, but the architecture section
is silent on this. It needs one sentence.

### State preservation across `config set global` re-registration

`runConfigSetGlobal` silently replaces registration and re-clones if global config is
already registered. The old `LocalPath` directory is not explicitly deleted before the
new clone; if the old and new paths differ (e.g., the user changes `$XDG_CONFIG_HOME`),
the old directory is orphaned. The behavior for the re-registration path should state
whether the old clone directory is removed (analogous to `os.RemoveAll` in unregistration).

### `GlobalConfigSource` integration with `GlobalConfig` struct

`registry.go` currently defines `GlobalConfig` with `Global GlobalSettings` and
`Registry map[string]RegistryEntry`. The design says `GlobalConfigSource` is stored
under `[global_config]` in the same file. The architecture section does not show the
updated `GlobalConfig` struct with the new field. This is a small but concrete gap:
the implementer must decide the field name, and inconsistency here breaks TOML
round-trips. Recommended: show the updated struct explicitly:

```go
type GlobalConfig struct {
    Global       GlobalSettings   `toml:"global"`
    GlobalConfig GlobalConfigSource `toml:"global_config,omitempty"`
    Registry     map[string]RegistryEntry `toml:"registry"`
}
```

---

## 3. Phase Sequencing

The design organizes implementation into four blocks: Config types and registry (Block
1), merge functions (Block 2), CLI commands (Block 3), and apply integration (Block 4).

### Block 1 → Block 2: correct dependency

Block 2 (`ResolveGlobalOverride`, `MergeGlobalOverride`) depends on the type definitions
from Block 1 (`GlobalOverride`, `GlobalConfigOverride`). This ordering is correct and
the blocks can be developed in parallel only after Block 1 types exist. The design
claims all four blocks are parallel; that's true for 2 and 3 relative to each other,
but Block 4 depends on both 1 and 2.

### Block 3 → Block 1 dependency understated

`runConfigSetGlobal` loads and saves `GlobalConfig`, which gains a new `GlobalConfigSource`
field in Block 1. Block 3 cannot be written or tested without that struct existing. The
design says Block 3 can run in parallel with Block 2, which is correct, but Block 3 is
not independent of Block 1. This is implicit in "registry.go gains a new field" being
listed in Block 1, but the dependency diagram could be clearer.

### Block 4 integrates in the right order

The apply pipeline sequence (2a → 3a-3c → 5c) maps cleanly to Block 4 touching
`apply.go` and `runPipeline`. `InstallGlobalClaudeContent` is called after
`InstallWorkspaceContext` (step 4.5+), which is already step 5b. The sequence is
correct: the workspace CLAUDE.md must exist before the global import is added.

### `ParseGlobalConfigOverride` placement

The security validation for `Files` paths in `ParseGlobalConfigOverride` is listed as
a Block 1 responsibility ("This validation must be in Block 1"). That sequencing is
correct: Block 4 must not merge unvalidated file paths. If the parse function is
omitted from Block 1, Block 4 loses its safety constraint.

### Summary

The declared parallel execution overstates independence slightly. A more accurate
description is: Block 1 must complete first (or at minimum define types), then Blocks
2 and 3 can proceed in parallel, then Block 4. This does not change the implementation
outcome — the code will work either way — but it sets realistic expectations for PR
structure.

---

## 4. Simpler Alternatives Overlooked

### Reuse `InstanceConfig` as `GlobalOverride`

`InstanceConfig` in `config.go` has an identical shape to the proposed `GlobalOverride`:

```go
type InstanceConfig struct {
    Claude *ClaudeConfig     `toml:"claude,omitempty"`
    Env    EnvConfig         `toml:"env,omitempty"`
    Files  map[string]string `toml:"files,omitempty"`
}
```

`GlobalOverride` would be a type alias or a renamed copy of the same struct. Reusing
`InstanceConfig` (perhaps renamed to `LayerOverride` or simply used directly as
`GlobalOverride = InstanceConfig`) eliminates a type definition and keeps the override
shape in one place. The merge semantics differ (global uses plugin union; instance uses
plugin replace), so a shared type would need per-call-site behavior parameters, which
may reduce clarity. The design reasonably chose not to conflate them, but this option
was not discussed and is worth a sentence of explanation in the document.

### Skip `GlobalConfigSource.LocalPath` in stored config

The design stores both `Repo` (the GitHub URL) and `LocalPath` (the clone destination)
in `GlobalConfigSource`. `LocalPath` is always `filepath.Join(configHome, "niwa", "global")` — it's computable from `$XDG_CONFIG_HOME` at runtime. Storing it adds a
consistency hazard: if `$XDG_CONFIG_HOME` changes between machines, the stored
`LocalPath` is wrong. Since the design specifies a fixed clone destination, `LocalPath`
can be derived rather than stored, and the `GlobalConfigSource` struct simplifies to
just `Repo string`. The `os.RemoveAll` call in unregistration uses the stored path;
deriving it at runtime would be equally reliable and removes the stale-path risk. This
was not discussed in the design.

### Deferred CLAUDE.global.md import removal

The design acknowledges a known negative consequence: the `@import` line in `CLAUDE.md`
is not removed when global config is unregistered, leaving a stale (but harmless)
directive. A one-line `removeImportFromCLAUDE` function (inverse of `ensureImportInCLAUDE`,
which already has string-search logic) would close this cleanly. The design defers it as
a follow-on. Given that `ensureImportInCLAUDE` is already 10 lines and the inverse
pattern is symmetric, implementing it in the same block as `InstallGlobalClaudeContent`
would cost almost nothing and avoids accumulating user-visible rough edges at v1.

---

## Summary Table

| Finding | Severity | Section |
|---------|----------|---------|
| `ParseGlobalConfigOverride` undefined in Solution Architecture | High — security gap without it | §2 |
| Hook path absolute resolution not in `MergeGlobalOverride` contract | High — materializer breaks at runtime | §1 |
| `ClaudeConfig.Settings` and `Env.Promote` merge behavior unspecified in global layer | Medium — implementer must guess | §1 |
| `allowDirty` flag not specified for global sync step | Low — UX inconsistency | §2 |
| Re-registration old clone directory not explicitly deleted | Low — orphan directory on path change | §2 |
| Updated `GlobalConfig` struct not shown in Solution Architecture | Low — TOML field name ambiguity | §2 |
| Block parallelism overstated (1 must precede 2 and 3) | Low — planning accuracy | §3 |
| `LocalPath` can be derived rather than stored | Low — simpler struct | §4 |
