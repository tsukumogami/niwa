---
status: Proposed
problem: |
  niwa's workspace.toml has a top-level `[content]` table whose semantics
  are 100% Claude-coupled — every consumer writes to literal `CLAUDE.md` /
  `CLAUDE.local.md` destinations, and the Go docstring on `ContentConfig`
  already says "the CLAUDE.md content hierarchy." But the TOML key itself
  is generic-sounding, so readers have to infer the coupling. Separately,
  niwa already has a `[claude]` namespace for explicitly-Claude-specific
  settings (hooks, settings, plugins, env). Moving `[content]` under
  `[claude]` — i.e., renaming to `[claude.content]` — makes the coupling
  explicit in the schema. The design needs to specify the exact rename
  shape, the deprecation mechanic for existing workspace.toml files that
  use `[content]`, and how this interacts with the per-repo override type
  (`RepoOverride.Claude`, which currently uses `*ClaudeConfig`).
---

# DESIGN: Claude-Key Consolidation

## Status

Proposed

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

## Decisions Already Made

These were settled during exploration. Treat them as constraints.

- **Migration policy: accept both `[content]` and `[claude.content]`
  for N releases with a deprecation warning.** Not a hard break. The
  user confirmed this during scoping. N is unspecified; the design
  will propose a window (e.g., until v1.0).

- **The rename preserves `[content]`'s shape.** Sub-tables
  (`workspace`, `groups`, `repos`, `repos.<name>.subdirs`) and their
  contents (`source = "..."`) move intact. Only the root path
  changes.

- **Out of scope for this design**: merging `[files]` or other
  adjacent keys, changing the semantics of content sub-tables,
  producing non-Claude artifacts from the same config.

- **Artifact recommendation**: Design Doc → `/plan` → `/implement` in
  single-pr mode. Scope is small and linear.
