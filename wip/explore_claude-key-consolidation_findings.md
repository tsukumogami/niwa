# Exploration Findings: claude-key-consolidation

## Core Question

What does it take to rename the top-level `[content]` table in
`workspace.toml` to `[claude.content]` so the Claude-specific semantics
are explicit in the schema instead of implicit behind a generic key
name?

## Round 1

### Key Insights

- **`[content]` is 100% Claude-coupled — the user is right.** Every
  consumer of `ContentConfig` (`InstallWorkspaceContent`,
  `InstallGroupContent`, `InstallRepoContent` in
  `internal/workspace/content.go`) writes to hardcoded `CLAUDE.md` or
  `CLAUDE.local.md` destinations. The Go docstring on `ContentConfig`
  itself reads: *"declares the CLAUDE.md content hierarchy."* Repo
  content installation is gated on `ClaudeEnabled` (`apply.go:329`). No
  code path treats `ContentEntry.Source` as a generic artifact
  reference. [Lead 1]

- **The rename is a pure syntactic refactor with ~150 LOC across ~8
  files.** Only 13 references to `cfg.Content.*` / `ContentConfig` /
  `ContentEntry` / `RepoContentEntry` exist across the codebase — the
  majority in tests. Content never participates in `MergeOverrides`,
  `MergeInstanceOverrides`, `MergeGlobalOverride`, or `EffectiveConfig`
  resolution, so there's no hidden semantic cascade. Mechanical work:
  move the `Content` field from `WorkspaceConfig` to `ClaudeConfig`,
  update call sites (`content.go` install functions, `config.go`
  validation error strings), update test fixtures, update the scaffold
  template, update the design doc. [Lead 2]

- **Deprecation mechanic fits the existing `Parse` warnings plumbing.**
  `Parse()` already calls `toml.Decode` and reports `md.Undecoded()`
  keys as warnings. Keep a deprecated `WorkspaceConfig.Content` field
  alongside the new `ClaudeConfig.Content`; post-parse, detect presence
  of the old form, merge into the canonical location, emit a warning.
  Conflict case: if both are non-empty, emit a hard error. [Lead 2]

- **Per-repo override interaction resolves cleanly via a type split.**
  Today `RepoOverride.Claude` is `*ClaudeConfig`. After the rename,
  adding a `Content` field to `ClaudeConfig` would let users write
  `[repos.<name>.claude.content]` — which would parse, get silently
  dropped by the merge pipeline (content isn't merged), and lose the
  user's intent. The clean fix: split `ClaudeConfig` into a full form
  (workspace-level) and a narrower `ClaudeOverride` (no `Content`, no
  `Marketplaces`) used by `RepoOverride.Claude`,
  `InstanceConfig.Claude`, and `GlobalOverride.Claude`. The TOML
  decoder then surfaces `[repos.<name>.claude.content]` as an "unknown
  field" warning automatically — zero extra validation code,
  self-documenting at the type layer. [Lead 3]

- **`Marketplaces` already has a precedent for "workspace-scoped-only
  on `ClaudeConfig`":** the comment at `config.go:23-24` literally
  says *"Marketplaces is workspace-wide. Not merged from per-repo
  overrides."* The type split formalizes that comment into the
  compiler. [Lead 3]

### Tensions

- **Where does `workspace.content_dir` belong?** It currently sits at
  `[workspace].content_dir` pointing at the directory that holds
  CLAUDE.md sources. If `[content]` becomes `[claude.content]`, should
  `content_dir` become `[claude].content_dir` for symmetry? Lead 1
  flagged this as an open question; the user's original scope
  explicitly said "out of scope" but the design may want to revisit
  when drafting the final shape.

- **Should `InstanceConfig.Claude` grow a `Content` field?** Today
  `InstallWorkspaceContent` writes `{instanceRoot}/CLAUDE.md`, so the
  workspace-level content IS the instance content. A per-instance
  content override isn't needed today, but the type split decision
  has to declare whether `InstanceConfig.Claude` uses the narrower or
  fuller type. Lead 3 flagged this.

### Gaps

- **Behavior of `md.IsDefined()` / `md.Undecoded()` on nested paths
  like `content.workspace`.** Lead 2's deprecation-detection mechanic
  depends on this. It's standard BurntSushi/toml behavior, but worth
  spot-checking during implementation rather than deferring.

- **Deprecation window length.** Three releases? Until v1.0? Not
  a research question; a policy decision for the design doc.

### Decisions

See `wip/explore_claude-key-consolidation_decisions.md` for the
accumulated decision record. Summary of this round:

- Migration policy: accept both `[content]` and `[claude.content]` for
  N releases with a deprecation warning. (Decided by user during
  scoping.)
- Per-repo override shape: split `ClaudeConfig` into full +
  `ClaudeOverride` (narrower). Recommended by research.
- Scope stays at renaming `[content]` → `[claude.content]`.
  `workspace.content_dir` and the `InstanceConfig.Claude` content
  question are flagged as design-time decisions, not separate
  explorations.

### User Focus

User's specific question: *"Tell me if I am missing something that
makes this more difficult than it seems."* Answer: **no, you're not.
It's actually simpler than it looks.** The rename is a clean refactor
with no merge/override cascade. Three small adjacent decisions
(deprecation mechanic, type split for overrides, `content_dir`
naming) are the only design-worthy parts of the migration.

## Accumulated Understanding

Renaming `[content]` to `[claude.content]` is mechanically small and
semantically safe:

1. The coupling is already 100% to Claude (hardcoded `CLAUDE.md` /
   `CLAUDE.local.md` destinations, gating on `ClaudeEnabled`, even the
   Go docstring says so).
2. The refactor is ~150 LOC across ~8 files, mostly test fixtures.
3. Content never participates in override/merge resolution, so there's
   no hidden complexity.
4. The deprecation mechanic rides on the existing `Parse` warnings
   infrastructure.
5. Per-repo overrides resolve cleanly via a `ClaudeConfig` ↔
   `ClaudeOverride` type split that also formalizes the existing
   `Marketplaces` "workspace-wide" comment.

The three design-worthy points: (a) the exact BurntSushi/toml metadata
API for deprecation detection, (b) whether to do the type split now
and whether to move `Marketplaces` with it, (c) whether
`workspace.content_dir` moves too. None of these are blockers; they
shape the final design doc.

**Artifact recommendation: lean Design Doc.** Records the rename, the
type split, the deprecation mechanic, and the policy on the two small
open architectural decisions. Then `/plan` → `/implement` in single-pr
mode (scope is small, linear).

## Decision: Crystallize
