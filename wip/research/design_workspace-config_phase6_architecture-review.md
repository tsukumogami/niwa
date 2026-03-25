# Architecture Review: Workspace Config Design

**Reviewer**: architect-reviewer
**Date**: 2026-03-25
**Document**: docs/designs/DESIGN-workspace-config.md

## 1. Is the architecture clear enough to implement?

### Verdict: Mostly yes, with gaps in three areas.

**What's clear:**

- The TOML schema is fully specified with Go type definitions. An implementer can write the parsing layer directly from the schema reference (lines 312-385) and Go types (lines 389-449).
- The `niwa apply` command flow (lines 454-467) is a concrete 14-step sequence. Each step maps to a testable unit of work.
- The file hierarchy diagram (lines 280-308) makes the physical layout unambiguous.
- Template variable expansion is constrained to 4 variables with plain string replacement. No ambiguity about the expansion model.

**What needs clarification before implementation:**

**Gap 1: Config resolution for `niwa apply` is underspecified.** Step 1 says "find workspace.toml by walking up from cwd (or use registry if invoked with a name)." But the design doesn't specify which command surface triggers which resolution path. Does `niwa apply` accept an argument? A `--name` flag? Is cwd-based resolution the only mode? The commands listed in v0.1 (init, create, apply, status, reset, destroy) each need their argument/resolution model specified. `niwa create` in particular: does it take an instance name? A number? Does it auto-increment?

**Gap 2: Host config merge semantics are declared but not defined.** Step 4 says "merge host overrides onto workspace config (host wins on conflict)." But the host config schema isn't shown. Is it a full `WorkspaceConfig` TOML that overlays field-by-field? A partial schema with specific merge rules? The `EnvConfig` merge alone has at least two reasonable interpretations: do host `files` append or replace? Do host `vars` merge or replace the whole map? This ships in v0.3 but the answer affects whether the Go types need merge methods from v0.1.

**Gap 3: Convention-based content discovery needs edge case rules.** The design says: "When `content_dir` is set and a repo has no explicit `[content.repos.X]` entry, niwa checks for `{content_dir}/repos/{repo_name}.md` and uses it automatically if found." What about group content? If `content_dir` is set and there's no `[content.groups.public]`, does niwa check for `{content_dir}/public.md`? The convention should be stated for all hierarchy levels or explicitly limited to repos.

## 2. Are there missing components or interfaces?

### Missing from the Go architecture:

**No package structure proposed.** The existing codebase has `internal/cli/` and `internal/buildinfo/`. The design introduces at minimum 5 new concerns:

1. Config parsing (TOML -> `WorkspaceConfig`)
2. Content resolution (convention-based file discovery + template expansion)
3. Instance state management (`.niwa/instance.json` read/write/diff)
4. Registry management (`~/.config/niwa/config.toml`)
5. Workspace operations (clone, apply content, detect drift)

The design should indicate the intended package layout. Without it, implementers may inline everything into `internal/cli/` (the only existing non-buildinfo package) or create an ad-hoc structure that's hard to refactor.

**Recommended package structure:**
```
internal/
  cli/          # Command definitions (existing)
  config/       # workspace.toml parsing + validation
  content/      # Content resolution, template expansion, file writing
  instance/     # Instance state (.niwa/instance.json)
  registry/     # Global registry (~/.config/niwa/config.toml)
  workspace/    # Orchestration: ties config + content + instance together
```

This keeps dependency flow downward: `cli/` -> `workspace/` -> `config/`, `content/`, `instance/`, `registry/`. The `workspace` package is the orchestrator that the CLI commands call.

**No interface definitions for testability.** The design touches three external systems: the filesystem, git (for cloning), and the host environment (hostname, XDG paths). If these aren't behind interfaces from the start, testing `niwa apply` requires a real filesystem and git repos. At minimum:

- A filesystem abstraction (or `afero`-style) for content writing and state management
- A git interface for clone/remote-verify operations
- A host resolver interface (hostname, config paths)

These don't need to be in the design document, but the absence of any testability discussion is worth noting. The design's 14-step `niwa apply` flow is integration-heavy; without seams, it'll be hard to test steps 6-14 in isolation.

**No error model.** The design specifies path traversal validation (security section) and drift detection (instance.json hashes) but doesn't define what happens on failure. Does `niwa apply` stop on first error? Continue and report? Is a partially-applied state valid? For content hash mismatches (user edited a managed file), does apply overwrite, warn, or abort? These decisions affect the instance state design: if apply can partially succeed, `managed_files` needs per-file status, not just hashes.

### Missing from the schema:

**`ChannelsConfig` Go type is absent.** The TOML schema includes `[channels.telegram]` with nested access rules and groups, but no Go type is provided. Every other section has a corresponding struct. This will force the implementer to design the type during implementation, which may not match the TOML structure shown.

**No `HostConfig` Go type.** Same issue -- the host config is described conceptually but has no struct definition.

## 3. Are the implementation phases correctly sequenced?

### Phase sequencing is sound with one structural risk.

**What's correct:**
- v0.1 (parse + repos + content) is the right foundation. You can't build hooks/settings distribution without knowing where repos are.
- v0.2 (hooks, settings, env) depends on repo layout from v0.1.
- v0.3 (host config, channels) is the most complex layer and correctly ships last.

**Structural risk: schema-before-consumer.** The design says v0.1 parses the full schema (including `[hooks]`, `[settings]`, `[env]`, `[channels]`) but only acts on `[workspace]`, `[groups]`, and `[content]`. This means v0.1 ships Go types for `HooksConfig`, `SettingsConfig`, `EnvConfig`, and `ChannelsConfig` that have zero consumers.

This is the state contract violation pattern from my heuristics: struct fields with no reader. The risk is that the types will drift from actual requirements by the time v0.2 implements them. When v0.2 starts generating `settings.local.json`, the `SettingsConfig` struct may need fields that weren't anticipated (e.g., `AllowedTools []string`).

**Recommendation:** Parse the full TOML but use `map[string]any` (or `toml.Primitive` / `toml.MetaData.Undecoded()`) for sections that v0.1 doesn't consume. Define the typed structs when their consumers land. This avoids premature type commitment while still validating that the TOML is syntactically correct.

**Alternative (acceptable):** Keep the typed structs in v0.1 but mark them explicitly as unstable in comments and don't expose them outside the config package. This is less clean but pragmatic if the team prefers to see the full type picture early.

**Phase gap: `niwa init` needs a remote fetch mechanism.** v0.1 includes `niwa init <name>` which registers a workspace config. The security section mentions "remote config pinning" and "--review flag." But v0.1 doesn't specify whether init supports remote sources or only local paths. If remote, that's a significant chunk of work (git clone of config repo, commit pinning, review display). If local-only in v0.1, say so explicitly.

## 4. Are there simpler alternatives we overlooked?

### The design is appropriately scoped. Two simplifications are worth considering.

**Simplification 1: Drop the content section entirely in v0.1.**

The convention-based discovery (`content_dir` + filename conventions) could be the only mechanism. If `content_dir = "claude"` is set:
- `claude/workspace.md` -> workspace CLAUDE.md
- `claude/{group_name}.md` -> group CLAUDE.md
- `claude/repos/{repo_name}.md` -> repo CLAUDE.local.md
- `claude/repos/{repo_name}/{subdir}.md` -> subdir CLAUDE.local.md

The explicit `[content]` section then becomes an override mechanism for when conventions don't fit, added in a later phase. This eliminates ~30 lines of config for the common case and removes the `ContentConfig`/`ContentEntry`/`RepoContentConfig` types from v0.1.

The design already describes this convention but treats it as a fallback. Making it primary would be simpler. The risk is that repos with non-standard content mappings can't be expressed -- but the design's own example workspace doesn't have any.

**Simplification 2: Defer multi-instance to v0.2.**

The instance state model (`.niwa/instance.json`, instance numbering, root-level scanning) adds substantial complexity to v0.1. If v0.1 assumes a single instance per workspace root (workspace.toml lives alongside the instance, not above it), the state model simplifies to:

```
workspace-root/          # This IS the instance
  workspace.toml
  claude/
  .niwa/state.json       # No instance_number, no root scanning
  public/
    tsuku/
```

Multi-instance support (workspace.toml as parent, numbered instances as children) ships in v0.2 when the single-instance flow is proven. This matches the "convention over configuration" driver -- single instance is the common case.

The cost: multi-instance users wait one release. The benefit: v0.1's state model, path resolution, and command semantics are dramatically simpler. No "walk up to find workspace.toml" vs "walk up to find .niwa/instance.json" ambiguity.

**Not a simplification but a caution: the `{workspace}` template variable.**

The design acknowledges that `{workspace}` expands to an absolute path that gets committed to CLAUDE.md files. This is a real friction point for multi-developer teams: every developer's workspace path differs, so committed CLAUDE.md files will show constant diffs. The design could address this by:
- Making workspace/group CLAUDE.md files also `.local.md` (gitignored)
- Or using a relative path convention with a `$NIWA_ROOT` env var

This isn't blocking but will surface as a usability issue quickly.

## Summary of Findings

### Blocking (must resolve before implementation)

1. **Command argument model unspecified.** How each v0.1 command resolves its target (cwd walk, name argument, flags) determines the CLI surface and is a compatibility commitment. Specify before implementing.

2. **`ChannelsConfig` Go type missing.** If v0.1 parses the full schema, it needs all types. Either add the type or defer parsing of `[channels]` to v0.3.

### Advisory (address during implementation)

3. **No package structure guidance.** Without it, the first implementer sets the precedent. A one-paragraph recommendation in the design would prevent a flat `internal/cli/` monolith.

4. **Schema-before-consumer risk for v0.2/v0.3 types.** Consider `map[string]any` for unconsumed sections or explicitly mark types as unstable.

5. **Error model for partial apply.** Define behavior for: content hash mismatch (user edited managed file), clone failure mid-apply, missing content source file.

6. **Convention-based discovery scope.** Clarify whether auto-discovery from `content_dir` applies to workspace and group levels, or only repos.

### Simplification Candidates

7. **Convention-first content resolution** would eliminate the `[content]` section for the common case.

8. **Single-instance v0.1** would cut state model complexity in half, deferring multi-instance to v0.2.
