# Lead: Is [content] exclusively Claude-coupled?

## Findings

**Yes — every consumer of `ContentConfig` produces only `CLAUDE.md` or `CLAUDE.local.md` artifacts. The destination filenames are hardcoded, not configurable.**

### Type definitions (confirmation)

`/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/internal/config/config.go`:

- Line 126: `// ContentConfig declares the CLAUDE.md content hierarchy.` — the doc comment itself claims Claude-only scope.
- Lines 127-131: `ContentConfig` has three fields: `Workspace ContentEntry`, `Groups map[string]ContentEntry`, `Repos map[string]RepoContentEntry`.
- Lines 134-136: `ContentEntry` has exactly one field, `Source string`.
- Lines 139-142: `RepoContentEntry` has `Source string` plus `Subdirs map[string]string`.

Neither `ContentEntry` nor `RepoContentEntry` has any field naming a destination, filename, artifact type, or output format. The destination is chosen entirely by the consumer.

### Consumers (exhaustive)

Only three functions in the codebase read from `cfg.Content.*`, all in `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/internal/workspace/content.go`:

1. **`InstallWorkspaceContent`** (content.go:27-49)
   - Reads `cfg.Content.Workspace.Source` (line 42).
   - Writes to `filepath.Join(instanceRoot, "CLAUDE.md")` — line 43. **Literal string `"CLAUDE.md"`.**

2. **`InstallGroupContent`** (content.go:55-78)
   - Reads `cfg.Content.Groups[groupName]` (line 56).
   - Writes to `filepath.Join(instanceRoot, groupName, "CLAUDE.md")` — line 72. **Literal string `"CLAUDE.md"`.**

3. **`InstallRepoContent`** (content.go:94-150)
   - Reads `cfg.Content.Repos[repoName]` (line 112) with an auto-discovery fallback (line 117: `autoDiscoverRepoSource`, which also looks for `{content_dir}/repos/{repoName}.md` — content destined for Claude).
   - Writes repo-level content to `filepath.Join(repoDir, "CLAUDE.local.md")` — line 121.
   - Writes each `Subdirs` entry to `filepath.Join(subdirPath, "CLAUDE.local.md")` — line 141. **Both literal strings.**

### Call sites

Only `internal/workspace/apply.go` invokes the three Install functions (lines 274, 310, 334). The surrounding comments explicitly frame these as Claude steps:

- apply.go:273 — `// Step 4: Install workspace-level CLAUDE.md.`
- apply.go:302 — `// Step 5: Install group-level CLAUDE.md files.`
- apply.go:326 — `// Step 6: Install repo-level CLAUDE.local.md files (and subdirectories).`
- apply.go:328-332 — Step 6 is skipped entirely when `ClaudeEnabled` returns false for a repo. That is, `ContentConfig` installation is conditional on the Claude-enabled flag, reinforcing that the content system is Claude-specific.

### Tests

`internal/workspace/content_test.go` exclusively asserts on `CLAUDE.md` / `CLAUDE.local.md` destinations (lines 44, 147, 229, 309, 318, 369, 410, 527). No test checks for any alternate filename or generic artifact.

### Template variables

`expandVars` (content.go:299-305) supports only `{workspace}`, `{workspace_name}`, `{group_name}`, `{repo_name}`. None of these can influence the destination filename (they are applied to body text only, via `installContentFile` line 227).

### Extension points / escape hatches

- No plugin hooks consume `ContentConfig`. `internal/workspace/plugin.go` contains zero references to `Content`.
- `materialize.go` does not reference `ContentConfig`; the only `source`-named symbol there is a marketplace source (plugin system), unrelated to content.
- No CLI commands in `internal/cli/` reference `ContentConfig`, `ContentEntry`, or `RepoContentEntry` directly.
- `Subdirs` (RepoContentEntry.Subdirs) writes the same hardcoded `CLAUDE.local.md` filename (content.go:141). It does not produce alternate artifacts — `Subdirs` keys select *which subdirectory* gets a CLAUDE.local.md, but the filename is fixed.

### Validation layer

`validateContentSource` and `validateSubdirKey` (config.go:236-249, 309-319) only enforce path-safety rules on the `Source` strings. They do not suggest any alternate use.

## Implications

**The rename `[content]` -> `[claude.content]` is safe in principle.** Every code path that reads `cfg.Content.*` writes files literally named `CLAUDE.md` or `CLAUDE.local.md`, gated by Claude-specific logic. Nothing in the codebase treats `ContentEntry.Source` as a generic artifact reference.

Mechanical work required for the rename:

1. Move `Content ContentConfig` from `WorkspaceConfig` into `ClaudeConfig` (currently at config.go:20-28) — or nest it under a new `claude.content` subkey via a small wrapper type.
2. Update the 6 consumer reads in content.go (lines 28, 42, 56, 112) and 3 call sites in apply.go (274, 310, 334) — accesses would become `cfg.Claude.Content.*`.
3. Update the validation block in config.go:209-229 which currently references `cfg.Content.*`.
4. Update content_test.go (roughly 15 test setup blocks that construct `config.ContentConfig{...}`).
5. Update design docs that document the schema: `docs/designs/current/DESIGN-workspace-config.md` and `docs/designs/current/DESIGN-global-config.md`.
6. Migration consideration: existing workspace.toml files with top-level `[content]` need a migration path (error message, auto-migrate, or forward-compat toml decode that accepts both locations during a deprecation window).

**No non-Claude artifacts are produced. No blocker found in the code.**

## Surprises

- **The doc comment on `ContentConfig` already says "CLAUDE.md content hierarchy"** (config.go:126). The Claude-specificity isn't just implicit — it's explicit in the Go docstring. The rename would make the TOML schema match what the Go type already declares.
- **`InstallRepoContent` silently falls through to auto-discovery** (content.go:117) when no explicit entry exists. Auto-discovery scans `{content_dir}/repos/{repoName}.md`. `content_dir` defaults semantically to `claude/` per the exploration context. This means the content system has a Claude-only behavior (auto-discovery in the Claude directory) even when no `[content.repos.*]` entry is declared — further evidence the subsystem is Claude-coupled.
- **Repo content installation is gated by `ClaudeEnabled`** (apply.go:329). If a repo has `claude.enabled = false`, the `[content]` entries for that repo are skipped entirely. A user setting `claude.enabled = false` effectively disables their `[content]` config — strong evidence the two are one subsystem.
- **`{content_dir}` defaults to `"."`** inside `installContentFile` when unset (content.go:211-213), not to `claude/`. The default is the workspace root, not a Claude-named subdir. So the field name `content_dir` is itself Claude-flavored only by convention, not by default value. This doesn't block the rename but worth noting if renaming `content_dir` -> `claude.content_dir` as a follow-on.

## Open Questions

1. Should `WorkspaceMeta.ContentDir` (config.go:67) also move under `[claude]`? It currently sits at `workspace.content_dir` and points at the directory holding CLAUDE.md source files. If `[content]` moves to `[claude.content]`, the paired directory field arguably should become `claude.content_dir` for symmetry — but that's a second rename with its own compat story.
2. What's the expected migration UX? Options: (a) accept both keys silently during a transition, (b) accept old key but emit a warning via `ParseResult.Warnings`, (c) hard error with a migration hint. The existing unknown-field warning machinery (config.go:168-171) could support option (b) cheaply.
3. Does the global-config overlay (`GlobalOverride` at config.go:255-259) need a `Content` field too? It currently exposes `Claude *ClaudeConfig`, `Env`, and `Files` — not `Content`. If `Content` merges into `ClaudeConfig`, global overrides would gain content override capability for free. Is that desired, or should `Content` stay opted out of global overlay?
4. Does `instance.toml`-style `InstanceConfig` (config.go:56-60) need the same treatment? Currently it also omits `Content`. Same question as #3.

## Summary

`[content]` is exclusively Claude-coupled: all three consumers (`InstallWorkspaceContent`, `InstallGroupContent`, `InstallRepoContent` in `internal/workspace/content.go`) write to hardcoded `CLAUDE.md` or `CLAUDE.local.md` destinations, and `ContentConfig`'s own Go docstring already describes it as "the CLAUDE.md content hierarchy." The rename to `[claude.content]` is mechanically safe — no non-Claude artifact path would be misrepresented — and should also tighten the subsystem by moving under the same `ClaudeEnabled` gating that already governs it. The biggest open question is whether paired fields like `workspace.content_dir` and the global-override/instance-override layers should move in the same change or be deferred to follow-ons.
