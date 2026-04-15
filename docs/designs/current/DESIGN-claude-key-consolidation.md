---
status: Planned
problem: |
  niwa's workspace.toml has a top-level `[content]` table whose semantics
  are 100% Claude-coupled — every consumer writes to literal `CLAUDE.md` /
  `CLAUDE.local.md` destinations, and the Go docstring on `ContentConfig`
  already says "the CLAUDE.md content hierarchy." But the TOML key itself
  is generic-sounding, so readers have to infer the coupling. niwa already
  has a `[claude]` namespace for explicitly-Claude-specific settings
  (hooks, settings, plugins, env); moving `[content]` under it makes the
  coupling explicit in the schema.
decision: |
  Move `ContentConfig` from `WorkspaceConfig.Content` (TOML path
  `[content]`) to `ClaudeConfig.Content` (TOML path `[claude.content]`),
  preserving sub-table shape. Keep the old path as a deprecated alias
  that emits a warning on use; reject both forms set simultaneously with
  a hard error. Split `ClaudeConfig` into the existing full form
  (workspace-scoped, carrying `Content` and `Marketplaces`) and a
  narrower `ClaudeOverride` (carrying `Enabled`, `Plugins`, `Hooks`,
  `Settings`, `Env` only) used by `RepoOverride.Claude`,
  `InstanceConfig.Claude`, and `GlobalOverride.Claude`, so the TOML
  decoder auto-rejects `[repos.<name>.claude.content]` as an unknown
  field at parse time. Defer renaming `workspace.content_dir`.
rationale: |
  Research confirmed the rename is a pure syntactic refactor (~150 LOC,
  majority tests; content doesn't participate in any merge/override
  pipeline). The type split is the one architectural choice and it's
  cheap: `Marketplaces` already has a "workspace-wide, not merged from
  overrides" comment that the compiler can now enforce. The deprecation
  mechanic rides on the existing `Parse` warnings machinery — two fields
  with different TOML tags, a post-parse check, one new warning, one new
  error for the conflict case. `content_dir` renaming is deferred because
  the user explicitly scoped it out and the symmetry argument isn't
  strong enough to overrule that.
---

# DESIGN: Claude-Key Consolidation

## Status

Planned

## Context and Problem Statement

niwa's `workspace.toml` schema has two top-level TOML tables that both
describe Claude Code behavior, but only one of them makes that coupling
explicit in the key name:

- `[claude]` — explicitly Claude-specific: `enabled`, `plugins`,
  `marketplaces`, `hooks`, `settings`, `env`.
- `[content]` — generic-sounding but 100% Claude-coupled: declares
  where CLAUDE.md / CLAUDE.local.md content sources live, with
  sub-tables for workspace, groups, repos, and per-repo subdirs.

Research (lead-content-claude-coupling) confirmed the second point
with exhaustive code evidence: all three consumers (`InstallWorkspaceContent`,
`InstallGroupContent`, `InstallRepoContent` in `internal/workspace/content.go`)
write to hardcoded `CLAUDE.md` or `CLAUDE.local.md` destinations. The
`ContentConfig` Go docstring already reads *"declares the CLAUDE.md
content hierarchy."* Repo content installation is even gated by
`ClaudeEnabled`. The coupling is real but only visible after reading
Go source — the TOML key name hides it.

The rename `[content]` → `[claude.content]` makes the coupling
explicit. That's the user's observation, and it's factually correct.

The research surfaced that the rename is mechanically small (~150 LOC
across ~8 files, mostly tests; content never participates in the merge
pipeline). What needs design is:

- **Exactly which fields move under `[claude]`** — `Content`
  certainly; `content_dir` possibly (user flagged out-of-scope, design
  revisits).
- **The deprecation mechanic** — how to accept both `[content]` and
  `[claude.content]` during a transition, with the exact
  BurntSushi/toml metadata calls and the warning/error surface.
- **The `ClaudeConfig` type split** — today `RepoOverride.Claude` is
  `*ClaudeConfig`; adding a `Content` field to `ClaudeConfig` would
  silently let users write `[repos.<name>.claude.content]` that the
  merge pipeline ignores. The clean fix is a narrower `ClaudeOverride`
  type for override positions (no `Content`, probably no
  `Marketplaces`) used by `RepoOverride`, `InstanceConfig`, and
  `GlobalOverride`, so the decoder auto-rejects bad keys.

## Decision Drivers

- **Explicit coupling in the schema.** The whole point of the change
  is that the TOML path should announce its Claude-specificity. Any
  decision that weakens this (e.g., keeping `content_dir` at the top
  level while moving `[content]` to `[claude.content]`) needs
  justification.

- **Backwards compatibility during the deprecation window.** Existing
  `workspace.toml` files with `[content]` must continue to parse
  cleanly with a visible warning. Users must be able to migrate on
  their own timeline (no forced break).

- **Single source of truth.** A given setting must live in exactly
  one TOML path. The design must prevent users from writing the same
  value in two places (e.g., legacy `[content]` AND new
  `[claude.content]`) without a clear error.

- **Compile-time enforcement where possible.** Preferred over runtime
  validation when both achieve the same end. Specifically, the
  override-type split is preferred over a `validate()` rejection
  because the Go type system self-documents the constraint.

- **Minimum surface area.** The rename is small. Every design choice
  should be weighed against whether it actually simplifies the
  migration or expands its scope unnecessarily.

- **Symmetry with existing patterns.** `ClaudeConfig` already has a
  `Marketplaces` field with a comment that says it's workspace-wide
  and not merged from overrides — formalizing this as a type-level
  split is the same pattern applied consistently.

## Considered Options

### Decision 1: Migration policy

When a user's `workspace.toml` has the old `[content]` key, niwa could:

**Alternatives considered:**

- **Hard break** — error out on `[content]`, print a migration snippet.
  Forces the user to fix before proceeding. Clean long-term, painful
  short-term.
- **Silent accept** — parse both forms, no warning. Lowest friction but
  the deprecation never surfaces to users; they never migrate.

**Chosen: accept both `[content]` and `[claude.content]` with a
deprecation warning on the old form, error when both are present.**

Deprecation window: until v1.0. niwa is pre-1.0 (v0.6.0), and 1.0 is
the natural line for locking the schema. Users get several releases of
warning visibility before the old form is removed.

**Rationale:** The user settled this during scope — wants visible
migration pressure without forcing a hard break mid-pre-1.0. The
warning rides on the existing `ParseResult.Warnings` plumbing.

### Decision 2: Per-repo override shape — `ClaudeConfig` type split

Today `RepoOverride.Claude` is `*ClaudeConfig`. After moving `Content`
into `ClaudeConfig`, writing `[repos.<name>.claude.content]` would
parse cleanly, get silently dropped by the merge pipeline (content
isn't merged), and lose the user's intent.

**Alternatives considered:**

- **Runtime validation rejection** — keep a single `ClaudeConfig`, add
  a check in `validate()` that errors if
  `cfg.Repos[name].Claude.Content` is non-zero. More code, less
  self-documenting.
- **Silent accept** — let the override carry `Content` and ignore it.
  Fails the "explicit schema" goal that motivates this entire design.

**Chosen: split `ClaudeConfig` into a full form (workspace-level) and
a narrower `ClaudeOverride` (override-level), used by
`RepoOverride.Claude`, `InstanceConfig.Claude`, and
`GlobalOverride.Claude`.**

Shape:

```go
// ClaudeConfig is the full Claude configuration at the workspace level.
type ClaudeConfig struct {
    Enabled      *bool           `toml:"enabled,omitempty"`
    Plugins      *[]string       `toml:"plugins,omitempty"`
    Marketplaces []string        `toml:"marketplaces,omitempty"`
    Hooks        HooksConfig     `toml:"hooks,omitempty"`
    Settings     SettingsConfig  `toml:"settings,omitempty"`
    Env          ClaudeEnvConfig `toml:"env,omitempty"`
    Content      ContentConfig   `toml:"content,omitempty"`
}

// ClaudeOverride is the narrower form used at override positions where
// workspace-scoped fields (Content, Marketplaces) are not meaningful.
type ClaudeOverride struct {
    Enabled  *bool           `toml:"enabled,omitempty"`
    Plugins  *[]string       `toml:"plugins,omitempty"`
    Hooks    HooksConfig     `toml:"hooks,omitempty"`
    Settings SettingsConfig  `toml:"settings,omitempty"`
    Env      ClaudeEnvConfig `toml:"env,omitempty"`
}
```

**Rationale:** The TOML decoder auto-surfaces
`[repos.<name>.claude.content]` as an "unknown field" warning via
`md.Undecoded()` — zero extra validation code. Moves `Marketplaces`
under the split as well, formalizing the existing
*"Marketplaces is workspace-wide. Not merged from per-repo overrides"*
comment into the compiler. Symmetric with `GlobalOverride`'s existing
exclusion of workspace-scoped fields.

### Decision 3: `workspace.content_dir` rename

The `workspace.content_dir` field points at the directory holding
CLAUDE.md source files (typically `claude/`). It has the same implicit
coupling as `[content]` — the name `content_dir` doesn't say Claude
either.

**Alternatives considered:**

- **Rename now** to `[claude].content_dir`. Symmetry with
  `[claude.content]`, one migration instead of two. Adds ~20 LOC and
  a second deprecation warning.
- **Rename later in a follow-up PR.** Keeps this PR's scope tight;
  `content_dir` migration stands alone cleanly.

**Chosen: defer to a follow-up.**

**Rationale:** The user explicitly scoped `content_dir` out during
exploration. The symmetry benefit exists but isn't strong enough to
overrule that scope boundary. `content_dir` is a string field (not a
whole table tree), so its own migration is even smaller than the main
rename; it costs little to ship separately. If during implementation
the separation feels awkward, the type-split PR can open the door
with a TODO. This design explicitly records the deferred question so
a future reader knows it was considered, not forgotten.

### Decision 4: Deprecation detection mechanic

After parsing, niwa needs to detect whether the user wrote
`[content]`, `[claude.content]`, both, or neither.

**Alternatives considered:**

- **Dual-unmarshal** — unmarshal into two structs with different
  shapes, compare. Complex, redundant work.
- **BurntSushi/toml metadata API** — use `md.IsDefined("content")` /
  `md.IsDefined("claude", "content")` after the decode. Standard,
  well-understood.

**Chosen: keep both fields on the struct with different TOML tags;
detect presence via struct zero-value check after parse.**

Shape:

```go
type WorkspaceConfig struct {
    // ...existing fields...
    Claude ClaudeConfig `toml:"claude"`

    // Deprecated alias for Claude.Content. Used during the deprecation
    // window; Parse() detects usage, warns, and merges into
    // Claude.Content. Remove at v1.0.
    Content ContentConfig `toml:"content"`
}
```

`Parse()` logic after `toml.Decode`:

1. If `cfg.Content` is non-zero AND `cfg.Claude.Content` is also
   non-zero: return error *"config uses both [content] and
   [claude.content]; pick one — [claude.content] is canonical, [content]
   is deprecated"*.
2. If `cfg.Content` is non-zero: copy it into `cfg.Claude.Content`,
   clear `cfg.Content`, append warning *"[content] is deprecated; use
   [claude.content] instead"*.
3. Otherwise: nothing to do.

**Rationale:** Zero-value comparison is simpler than metadata walks and
doesn't care whether the user wrote `[content.workspace]` or just
`[content.groups]`. The merge semantics are trivial because there's no
partial-overlap case — if both paths are populated, it's an error, not
a merge. Cleared `cfg.Content` after copy ensures downstream consumers
see one canonical location.

## Decision Outcome

The four decisions compose into a single coherent migration:

1. **Schema change**: `[content]` → `[claude.content]`, preserving
   shape. `workspace.content_dir` stays at the top level (deferred).
2. **Type split**: `ClaudeConfig` (workspace, has `Content` and
   `Marketplaces`) vs `ClaudeOverride` (override positions, narrower).
   `RepoOverride.Claude`, `InstanceConfig.Claude`,
   `GlobalOverride.Claude` switch to `*ClaudeOverride`. Attempted
   `[repos.<name>.claude.content]` becomes a TOML decoder warning for
   free.
3. **Deprecation**: keep `WorkspaceConfig.Content` as a deprecated
   alias field through v0.x; detect post-parse, warn, merge into
   canonical, error on simultaneous use of both paths. Remove at v1.0.
4. **Documentation**: update `DESIGN-workspace-config.md`,
   `README.md`, and the scaffold template to show the new path. The
   deprecation window means both forms are valid during the
   transition; the doc explains both and recommends the new form for
   new configs.

## Out of Scope

- **Rename `workspace.content_dir`** — deferred to a follow-up PR.
  Flagged explicitly so a future reader knows it was considered.
- **Move `[files]` or other adjacent top-level keys under `[claude]`** —
  user did not raise these; their coupling is not as clean as
  `[content]`'s.
- **Change the shape of content sub-tables** — the rename preserves
  `workspace.source`, `groups.<name>.source`, `repos.<name>.source`,
  `repos.<name>.subdirs.<key>` structures intact.
- **Hard-break removal of `[content]`** — happens at v1.0 as a
  separate follow-up; this PR only introduces the deprecation.

## Solution Architecture

### Overview

Two struct changes in `internal/config/config.go`, one post-parse hook
in `Parse()`, plus call-site updates in `internal/workspace/content.go`
and validation messages. No changes to the merge/override pipeline
because content never participates in merging.

### Components

**`internal/config/config.go`** — schema definitions:

- `ContentConfig`, `ContentEntry`, `RepoContentEntry` retain their
  current shape.
- `ClaudeConfig` gains a `Content ContentConfig` field (TOML tag
  `content,omitempty`).
- New `ClaudeOverride` type with the narrower shape (no `Content`, no
  `Marketplaces`).
- `WorkspaceConfig.Claude` stays `ClaudeConfig` (full).
- `WorkspaceConfig.Content` remains as a deprecated alias field
  through v0.x (TOML tag `content,omitempty`); removal at v1.0.
- `RepoOverride.Claude` switches from `*ClaudeConfig` to
  `*ClaudeOverride`.
- `InstanceConfig.Claude` switches from `*ClaudeConfig` to
  `*ClaudeOverride`.
- `GlobalOverride.Claude` switches from `*ClaudeConfig` to
  `*ClaudeOverride`.

**`internal/config/config.go`** — `Parse()` post-decode hook:

```go
// After toml.Decode, before validate:
if !isZero(cfg.Content) {
    if !isZero(cfg.Claude.Content) {
        return nil, fmt.Errorf("config uses both [content] and [claude.content]; " +
            "pick one — [claude.content] is canonical, [content] is deprecated")
    }
    cfg.Claude.Content = cfg.Content
    cfg.Content = ContentConfig{}
    warnings = append(warnings,
        "[content] is deprecated; use [claude.content] instead")
}
```

`isZero(ContentConfig)` is a small package helper: returns true if
`Workspace.Source == ""` and `Groups` is empty and `Repos` is empty.

**`internal/workspace/content.go`** — consumer updates:

- `InstallWorkspaceContent`: `cfg.Content.Workspace` → `cfg.Claude.Content.Workspace`.
- `InstallGroupContent`: `cfg.Content.Groups[...]` → `cfg.Claude.Content.Groups[...]`.
- `InstallRepoContent`: `cfg.Content.Repos[...]` → `cfg.Claude.Content.Repos[...]`.

**`internal/config/config.go`** — `validate()` error messages:

- Replace hard-coded `"content.workspace.source"` etc. with
  `"claude.content.workspace.source"`.

**`internal/workspace/override.go`** — type updates:

- `RepoOverride.Claude`, `InstanceConfig.Claude`,
  `GlobalOverride.Claude` are now `*ClaudeOverride`. The merge code
  already reads only `Enabled`, `Plugins`, `Hooks`, `Settings`, `Env`,
  `Marketplaces` (the latter from the workspace-level config, not
  overrides), so the only edit is type names; field accesses are
  unchanged.

### Key Interfaces

**`ClaudeOverride` (new)**:

```go
type ClaudeOverride struct {
    Enabled  *bool           `toml:"enabled,omitempty"`
    Plugins  *[]string       `toml:"plugins,omitempty"`
    Hooks    HooksConfig     `toml:"hooks,omitempty"`
    Settings SettingsConfig  `toml:"settings,omitempty"`
    Env      ClaudeEnvConfig `toml:"env,omitempty"`
}
```

`ClaudeConfig.ToOverride() *ClaudeOverride` helper returns the
workspace-level config projected into the override shape, for merge
code that previously worked on a single type. (Optional; the merge
code can read fields directly without this helper if preferred.)

### Data Flow

No flow changes. Parse reads the TOML, the new post-parse hook
normalizes `[content]` → `[claude.content]` with a warning, the rest
of the pipeline consumes `cfg.Claude.Content` exactly as it used to
consume `cfg.Content`.

## Implementation Approach

### Phase 1: Schema + type split

- Add `ClaudeOverride` type in `internal/config/config.go`.
- Add `Content ContentConfig` field to `ClaudeConfig`.
- Switch `RepoOverride.Claude`, `InstanceConfig.Claude`,
  `GlobalOverride.Claude` to `*ClaudeOverride`.
- Keep `WorkspaceConfig.Content` as a deprecated alias (field
  retained, TOML tag unchanged at this phase — the handover logic
  comes in Phase 2).
- Update all compiler errors: `internal/workspace/override.go` merge
  code, test fixtures referencing `ClaudeConfig` at override
  positions.
- Unit tests for the type split: `RepoOverride.Claude` can't carry
  `Content` (attempted TOML decode surfaces as unknown-field
  warning).

Deliverables:
- `internal/config/config.go` — new type, moved field, adjusted
  pointer types.
- `internal/workspace/override.go` — type name updates (no behavior
  change).
- Test fixtures updated.

### Phase 2: Deprecation hook + consumer migration

- Add `isZero(ContentConfig) bool` helper in
  `internal/config/config.go`.
- Add post-decode hook in `Parse()`: detect deprecated `cfg.Content`,
  warn, merge into `cfg.Claude.Content`, error on conflict.
- Update `validate()` error messages to reference
  `claude.content.*` paths.
- Update `internal/workspace/content.go` consumers to read from
  `cfg.Claude.Content.*`.
- Unit tests covering: new-only form, old-only form (with warning),
  both forms (error), neither form.

Deliverables:
- `internal/config/config.go` — `Parse()` deprecation hook,
  validation messages, `isZero` helper.
- `internal/workspace/content.go` — consumer updates.
- Unit tests in `internal/config/config_test.go` and
  `internal/workspace/content_test.go`.

### Phase 3: Documentation + scaffold

- Update `internal/workspace/scaffold.go` — the example workspace.toml
  shipped with `niwa init` shows `[claude.content]` (new canonical).
- Update `docs/designs/current/DESIGN-workspace-config.md` —
  schema references show both forms with the deprecation note.
- Update `README.md` — Quick start / schema sections show
  `[claude.content]`.
- Add a `CHANGELOG.md` or release-notes snippet noting the
  deprecation (release notes machinery consumes this automatically).

Deliverables:
- `internal/workspace/scaffold.go` updated.
- `docs/designs/current/DESIGN-workspace-config.md` updated.
- `README.md` updated.

## Implicit Decisions

Re-reading the architecture above, one implementation choice was made
in prose without explicit decision treatment.

### Implicit Decision A: `ClaudeOverride` is a separate type, not an
anonymous embedded struct

The design chose to define `ClaudeOverride` as its own named type
rather than using struct embedding or type aliasing tricks.

- **Alternative:** embed a `ClaudeCommon` struct in both `ClaudeConfig`
  and `ClaudeOverride`, reducing field duplication.
- **Chosen:** named type with explicit fields. Embedding would save a
  few lines of struct definition but complicate TOML round-tripping
  (how do embedded fields encode?) and the merge code would still need
  to read fields one at a time. Named types are what the rest of the
  config package does; consistency wins over brevity.

## Security Considerations

This design is a schema rename with a deprecation mechanic; no new
code paths execute user-supplied data, no new filesystem reads, no
new network traffic, no new secrets handling.

- **External artifact handling**: not applicable. The parser reads
  `workspace.toml` the same as before; only the TOML key changes.
- **Permission scope**: unchanged. Content source paths still resolve
  relative to the workspace config directory, with the existing
  `validateContentSource` / `validateSubdirKey` path-safety checks.
- **Supply chain**: no new dependencies.
- **Data exposure**: unchanged. `workspace.toml` contents are not
  transmitted anywhere.

The only new failure mode is the deprecation-conflict error when both
`[content]` and `[claude.content]` are populated. This is a
user-facing validation error, not a security vulnerability — it
prevents silent behavior divergence, not data exposure.

## Consequences

### Positive

- **Schema self-documents the Claude coupling.** Readers of
  `workspace.toml` see `[claude.content]` and immediately know the
  path governs Claude-specific behavior. No more inferring from Go
  source.
- **Type split formalizes a comment into the compiler.** The existing
  *"Marketplaces is workspace-wide"* convention becomes a type-level
  guarantee. Future maintainers can't accidentally accept a workspace-
  scoped field at an override position.
- **Deprecation is gentle.** Existing users see a warning on old
  configs and have time to migrate; no forced break pre-1.0.
- **Zero merge/override code changes.** The research confirmed content
  doesn't participate in merging; the migration is purely structural.
- **Foundation for future `[claude]` consolidations.** If
  `workspace.content_dir` or `[files]` later need to move under
  `[claude]`, the pattern is established.

### Negative

- **Two ways to say the same thing during the deprecation window.**
  Readers of example `workspace.toml` files in the wild will see both
  forms until v1.0. Documentation must explain both.
- **Type split adds a second Claude-shaped struct.** `ClaudeConfig`
  and `ClaudeOverride` share most fields. Future additions to the
  shared subset must remember to add to both types.
- **Deferred `workspace.content_dir` rename is a known-asymmetry.**
  After this PR, `[claude.content]` exists but `[workspace].content_dir`
  still governs where the content sources are looked up. The
  asymmetry is explicit in the design's Out of Scope section but may
  feel incomplete to users who notice.

### Mitigations

- **Scaffold and docs show the new form only.** New `niwa init` users
  never see `[content]`; they see `[claude.content]`. The deprecation
  warning is what pushes existing users to migrate.
- **Type-split field drift**: documented via Go doc comments on both
  types, plus an entry in the follow-up PR for `content_dir` that can
  re-examine whether any `ClaudeOverride` fields should gain
  overridability.
- **Asymmetry of deferred `content_dir`**: a follow-up issue tracks
  the rename, and the design Out of Scope section records the
  deferral so future readers understand it was considered.
