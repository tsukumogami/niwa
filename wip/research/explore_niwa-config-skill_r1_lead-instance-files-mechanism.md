# Lead: What does the [instance.files] mechanism do, and can it scaffold a skill?

## Findings

**Schema.** `[instance.files]` is a `map[string]string` field (`internal/config/config.go:257-263`, field `Files`) on `InstanceConfig`, nested under `WorkspaceConfig.Instance` (`config.go:248`). Each entry is `source = destination`: source relative to the config dir (`.niwa/`), destination relative to the instance root. A trailing `/` on the source means "copy the whole directory recursively, preserving structure."

**Where it's consumed.** `MergeInstanceOverrides` (`internal/workspace/override.go:23-30, 199`) seeds a dedicated effective field `EffectiveConfig.InstanceFiles`, sourced only from `[instance.files]` (kept separate from the legacy `effective.Files`). `InstallWorkspaceRootSettings` (`internal/workspace/workspace_context.go:242`, `:377`) -- despite the name, this targets an instance root, not the workspace root -- calls `materializeVerbatimFiles(mctx, effective.InstanceFiles)`, writing files verbatim (no `.local` infix) via `materializeVerbatimFile`/`materializeVerbatimDir` (`internal/workspace/materialize.go:1660-1758`).

**Directory support: confirmed.** `materializeVerbatimFiles` dispatches on trailing `/`: single file, or a full recursive directory copy via `filepath.Walk` (`materialize.go:1724-1758`), preserving names and nested structure. Test `TestMaterializeVerbatimFilesDirSourceVerbatim` (`internal/workspace/materialize_test.go:2288`) confirms exactly this. So `"skills/" = ".claude/skills/"` is mechanically supported: `.niwa/skills/foo/SKILL.md` -> `<instanceRoot>/.claude/skills/foo/SKILL.md`.

**Lifecycle.** `InstallWorkspaceRootSettings` is called from exactly one place in non-test code: `internal/workspace/apply.go:1401`, inside `Applier.runPipeline`, which both `Applier.Create` (`apply.go:420`, i.e. `niwa create`) and `Applier.Apply` (`apply.go:569`, i.e. `niwa apply`) invoke. So `[instance.files]` materializes on every `niwa create` and every `niwa apply`, not once.

**Stays in sync / cleanup.** Written paths join the instance's ManagedFiles/tracked-writes set (`workspace_context.go:364-381`): dropping an `[instance.files]` entry deletes the file on next apply. `docs/guides/file-distribution.md:76-78` confirms the same in prose. So it genuinely re-syncs content and prunes removed entries on every apply -- the property needed to keep a shipped skill in sync with an upstream source.

**Companion `[root.files]`.** Same verbatim-copy core, applied at the workspace root by `MaterializeWorkspaceRoot` (`internal/workspace/root_materializer.go:153-173`). Difference: workspace root has no managed-file store, so `[root.files]` is overwrite-idempotent but not cleanup-tracked (`file-distribution.md:79-83`).

**Historical status.** `docs/prds/PRD-mcp-root-instance-distribution.md` (status: Done) and `docs/designs/current/DESIGN-mcp-root-instance-distribution.md` (status: Current) document that `[instance.files]` was previously parsed/merged but never materialized -- a "dead field" -- until this PRD activated it. The motivating example throughout is `.mcp.json` (needs an exact filename, incompatible with `[files]`'s forced `.local` infix).

**Documentation.** `docs/guides/file-distribution.md` (indexed in `CLAUDE.md:37`) is the authoritative guide: three tables (`[files]` repo-level w/ `.local` infix, `[instance.files]` instance-root verbatim, `[root.files]` workspace-root verbatim), the `.mcp.json` worked example, and a "Limitations" section noting distributed files are "meant for the workspace-root or instance-root project directory, not for niwa-internal directories" -- prose guidance, not an enforced constraint (see next point).

**No `.claude/`-destination guard on the main config -- asymmetric with a real enforced guard elsewhere.** A separate personal/global overlay config (`WorkspaceOverlay`, parsed by `ParseOverlay` in `internal/config/overlay.go`) explicitly rejects any `[files]` destination beginning with `.claude/` or `.niwa/` as a "protected directory" (`overlay.go:132-134`, `isProtectedDestination` at `:187-194`; tests `TestParseOverlay_ProtectedDestinationClaude`/`...Niwa` in `internal/config/overlay_test.go:422-452`). This guard exists only in `overlay.go`/`validateOverlay` -- the main `internal/config/config.go` validators have no equivalent. Confirming it's not accidental: the main workspace.toml's per-repo `[files]` table is already routinely used to target `.claude/` subpaths in tests, e.g. `"extensions/design.md": ".claude/shirabe-extensions/"` and `"commands/": ".claude/commands/"` (`internal/workspace/materialize_test.go:1481,1582`), and `"ext/design.md": ".claude/ext/"` (`internal/workspace/override_test.go:496`). So writing into `.claude/skills/` via `[instance.files]`/`[root.files]` in the main workspace.toml is not blocked by any validation found.

**Why `[instance.files]`/`[root.files]`, not `[files]`, is correct for a `SKILL.md`.** Per-repo `[files]` forces a `.local` infix (`injectLocalInfix`, `materialize.go:51`) so the repo gitignore pattern matches. `SKILL.md` -> `SKILL.local.md` would not be recognized by Claude Code, which needs the literal filename. `[instance.files]`/`[root.files]` copy verbatim precisely because "some files have to keep their exact name to work at all" (`file-distribution.md:24-29`).

**Niwa already has an established precedent for exactly this target shape -- but as a Go-embedded mechanism, not workspace.toml-authored.** `internal/workspace/root_materializer.go:16-34` embeds a `rootskills/` tree (`//go:embed rootskills`). `writeRootSkills` (`:189-223`) walks it and for every `rootskills/<name>/SKILL.md` writes `<workspaceRoot>/.claude/skills/<name>/SKILL.md` -- the identical directory shape the candidate `[instance.files]` pattern targets. Runs unconditionally on every apply inside `MaterializeWorkspaceRoot` (`:147-151`); it's how niwa ships its own `dispatch` skill today. This corroborates that the target shape is correct and functional with Claude Code's project-skill loading, but it's compiled into the binary, not something a workspace author drives via `workspace.toml`.

## Implications

- Mechanically, `[instance.files] "skills/" = ".claude/skills/"` would work as the private-repo precedent describes: materializes verbatim into `<instanceRoot>/.claude/skills/...` on every `create`/`apply`, with drift cleanup on removal.
- This is a novel use of a documented general-purpose mechanism, not an established or documented pattern for skill delivery. Every example in the docs and the activating PRD/DESIGN is `.mcp.json`; nothing in-repo shows `[instance.files]`/`[root.files]` used to deliver `.claude/skills/`.
- `[instance.files]` (tracked, cleanup-capable, per-instance) is the right choice over `[root.files]` (untracked, workspace-root-only) for keeping a skill in sync at the level where sessions actually run.
- Unverified: for a single-repo workspace (commuter, equity-planner style), whether "instance root" coincides with the adopting repo's own working directory or sits in a wrapper directory above it -- this determines whether `.claude/skills/` written via `[instance.files]` lands where a session in that repo actually looks.

## Surprises

- The `.claude/`/`.niwa/` protection is real and tested, but only fires in the personal/global overlay parser, not the main workspace.toml parser that owns `[instance.files]`/`[root.files]`/`[files]`. Whether that asymmetry is intentional (main config trusted; overlay is a lower-trust additive layer) or an oversight is not stated anywhere found.
- `[instance.files]` was a no-op "dead field" until a fairly recent, narrowly-scoped PRD activated it -- very little in-repo usage precedent exists beyond its own tests and the `.mcp.json` example.
- Niwa ships its own skills via Go-embed + a dedicated installer rather than via `[root.files]`/`[instance.files]`, even though the file tables now exist and could express the same thing.

## Open Questions

- Does "instance root" equal the adopting repo's working directory in the single-repo workspace topology, or is there still a wrapper level above it?
- Is the missing `.claude/`/`.niwa/` guard on the main config an intentional trust boundary or a gap worth flagging to a maintainer before relying on it?
- Does the `0o600` file mode niwa writes for verbatim files (`secretFileMode`, via `writeManagedFile`) cause any friction for a `SKILL.md` in typical environments? Not verified against Claude Code's loading requirements.

## Summary
`[instance.files]` is a real, documented, recently-activated mechanism that verbatim-copies files or whole directories from `.niwa/` into each instance root on every `niwa create`/`niwa apply`, with drift-tracked cleanup on removal -- mechanically it supports exactly the `"skills/" = ".claude/skills/"` directory-copy pattern from the private-repo precedent. No enforced guard blocks `.claude/` as a destination in the main workspace.toml (unlike the personal overlay config, which explicitly forbids it), and niwa's own embedded root-skill installer proves the `.claude/skills/<name>/SKILL.md` target shape works with Claude Code -- but no doc, test, or existing config anywhere uses `[instance.files]` for skill delivery specifically, so this would be a novel, well-supported, but unprecedented application of the mechanism.
