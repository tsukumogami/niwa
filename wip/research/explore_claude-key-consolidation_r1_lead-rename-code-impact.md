# Lead: Code impact of renaming [content] to [claude.content]

## Findings

(Captured from the Explore-subagent summary; the agent lacked Write access to
persist the full report, so the findings below are its own narrative.)

### Reference count and distribution

Only 13 references to `cfg.Content.*` / `ContentConfig` / `ContentEntry` /
`RepoContentEntry` across the entire codebase:

- `internal/config/config.go` — struct definitions and validation.
- `internal/config/config_test.go` — TOML fixtures and assertions.
- `internal/workspace/content.go` — the three consumers
  (`InstallWorkspaceContent`, `InstallGroupContent`, `InstallRepoContent`).
- `internal/workspace/content_test.go` — consumer tests.

### Merge semantics — content is not merged

Content is NOT involved in any `MergeOverrides`, `MergeGlobalOverride`, or
`EffectiveConfig` logic. It's read once at parse time and consumed directly
by the three install functions in `content.go`. This is the single biggest
finding: because content never participates in override resolution, moving
it to `ClaudeConfig.Content` is a pure field-relocation with no cascade.

### Per-call-site classification

- `config.go:validate()` — hard-codes error messages with `"content.*"`
  paths. Update them to `"claude.content.*"`.
- `content.go` install functions — read `cfg.Content.X`; after rename,
  read `cfg.Claude.Content.X`. Trivial.
- `config_test.go` and `content_test.go` — update TOML fixture strings
  (`[content]` -> `[claude.content]`) and Go struct literals
  (`config.ContentConfig{...}` stays the same type, but access paths
  through `WorkspaceConfig` change).
- `scaffold.go` — the example workspace.toml template shows `[claude]`
  and nearby but does not currently include `[content]` sections. The
  scaffold change, if any, is small.

### Parse deprecation hook

`Parse()` at `config.go:157` already calls `toml.Decode` and reports
`md.Undecoded()` keys as warnings. That mechanism already exists for
forward-compat. The deprecation-warning approach:

1. Keep `WorkspaceConfig.Content` as a deprecated alias field with TOML
   tag `toml:"content,omitempty"` for N releases.
2. Add `ClaudeConfig.Content` with TOML tag `toml:"content,omitempty"`.
3. After `toml.Decode` returns, check whether the deprecated top-level
   `Content` is non-empty. If yes:
   - Emit a `warnings` entry: "config field `content` is deprecated; use `claude.content` instead".
   - If `cfg.Claude.Content` is also non-empty, emit a conflict error
     (both forms present).
   - Otherwise, copy deprecated `Content` into `cfg.Claude.Content` so
     downstream consumers see a single canonical shape.

This is cleaner than a "second unmarshal pass" because BurntSushi/toml's
`md.Keys()` / `md.IsDefined()` can detect which top-level paths appeared
in the source.

### Documentation touches

- `docs/designs/current/DESIGN-workspace-config.md` — ~14 references to
  `[content]`. Update to show both forms during deprecation window.
- `README.md` — references the schema in the Quick start; update.
- scaffold template — brief.

### Estimated total diff

~150 LOC across ~8 files (majority in test fixtures).

## Implications

**Pure syntactic rename.** The absence of merge / override involvement
(content never goes through MergeOverrides or EffectiveConfig) means
there's no hidden semantic change to worry about. Risk is limited to
(a) forgetting to update all test fixtures and (b) the deprecation
detection mechanic.

**The deprecation-warning mechanic is the only design-worthy part of the
migration.** The mechanics of "accept both, warn on old, error on
conflict" needs to be specified carefully but is implementable with
BurntSushi/toml's existing metadata API. The recommended approach: keep
both fields on the struct with the same TOML tag on different paths,
detect the deprecated form post-parse, merge into the new canonical
location, emit a warning.

**Low-risk, small-surface refactor.** 150 LOC across 8 files is a single
PR, testable in isolation.

## Surprises

- The reference count is smaller than initially estimated (13 vs. the
  scoping scan's "20+"). Tests dominate the surface, which is the easy
  part.
- There's no merge/override layer for content — this alone collapses
  half the potential complexity.
- `Parse()` already has a forward-compat warnings machinery that the
  deprecation mechanic can ride on; no new infrastructure needed.

## Open Questions

- How exactly to detect the deprecated `[content]` usage from
  BurntSushi/toml's metadata? The summary proposes `md.IsDefined()` or
  `md.Keys()`; both exist but behavior at nested paths (e.g.,
  `md.IsDefined("content", "workspace")`) should be verified during
  implementation, not deferred past design.
- Should the deprecation window be a specific number of releases (e.g.,
  3) or a specific time window (e.g., until v1.0)? This is a policy
  question for the design doc.

## Summary

This is a pure syntactic rename with minimal cascade: move `Content` into
`ClaudeConfig`, retire top-level `WorkspaceConfig.Content` behind a
deprecation alias, update ~4 call sites and ~13 test fixtures, touch the
scaffold and design doc. Estimated ~150 LOC across ~8 files. No merge
semantics, validation structure, or materialization logic changes are
needed — content never participates in override resolution. The only
design-worthy open question is the exact BurntSushi/toml metadata API to
use for detecting and warning on the deprecated form.
