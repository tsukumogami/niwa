# Lead: Per-repo override interaction after the rename

## Findings

### Today's struct shapes (confirmed)

`config.go:20-28` defines `ClaudeConfig` with `Enabled`, `Plugins`, `Marketplaces`,
`Hooks`, `Settings`, `Env`. No `Content` field. `config.go:115-124` defines
`RepoOverride` with a `Claude *ClaudeConfig` pointer. `config.go:127-142` defines
`ContentConfig` (top-level) with `Workspace`, `Groups`, `Repos map[string]RepoContentEntry`.
`RepoContentEntry` holds a `Source` string and optional `Subdirs`.

These two subtrees are reached via orthogonal top-level keys today:
- `cfg.Content.Repos[name]` — per-repo CLAUDE.md source
- `cfg.Repos[name].Claude` — per-repo override of the Claude config subtree

### The merge code for per-repo claude overrides never touches content

`internal/workspace/override.go:28-123` — `MergeOverrides` builds an
`EffectiveConfig` from `ws.Claude` (hooks, settings, env, plugins) and then layers
`override.Claude` on top. A grep for `Content` in `override.go` returns no hits.
The merge code has no knowledge of `ws.Content` or per-repo content at all.

`internal/workspace/override.go:128-213` — `MergeInstanceOverrides` mirrors the
same pattern for the instance root. Same result: no Content involvement.

`internal/workspace/override.go:327-436` — `MergeGlobalOverride` applies the
global config layer. It also operates only on `ws.Claude.*` and `ws.Env`/`ws.Files`.
The `GlobalOverride` struct (`config.go:255-259`) is explicitly
`Claude *ClaudeConfig + Env + Files` — there is no content field either at the
global override layer.

`config.go:255-267` confirms the global config schema deliberately excludes
per-repo structure: `GlobalOverride` has no `Content` and no `Repos` map. Content
source paths are an intrinsically workspace-scoped concern (resolved relative to
`cfg.Workspace.ContentDir` in the workspace config directory, see
`content.go:210-216`), so even the global override layer has no path for
content-style overrides.

### Content resolution path today

`internal/workspace/content.go:94-150` — `InstallRepoContent` reads the source
from `cfg.Content.Repos[repoName]` directly (line 112: `entry, hasExplicit := cfg.Content.Repos[repoName]`).
It never consults `cfg.Repos[repoName].Claude`. Same pattern in `InstallWorkspaceContent`
(line 28) and `InstallGroupContent` (line 56).

### Validation

`config.go:208-229` — `validate()` only validates `cfg.Content.*` source paths;
it does not walk `cfg.Repos[*].Claude` for content-like fields because the
struct cannot currently carry them.

## Implications

### Is there real ambiguity after the rename?

Strictly, no — today's merge code would keep ignoring content even if
`RepoOverride.Claude` started carrying a `Content` field, because override.go
only reads Hooks/Settings/Env/Plugins. A user could write
`[repos.<name>.claude.content]` and it would decode cleanly into
`RepoOverride.Claude.Content` (once the field exists), be silently dropped by
the merge pipeline, and never reach the content installer. That's the worst
kind of "works" — TOML parses, no warning fires, but the intent is lost.

### Recommended policy: reject at parse/validate time (policy A)

The rename's stated goal is to make Claude-specific semantics **explicit in the
schema**. Letting the schema carry a field that silently does nothing
undermines that goal. Per-repo content belongs under
`[claude.content.repos.<name>]` (workspace level), not under
`[repos.<name>.claude.content]`.

Why reject rather than accept-with-semantics:

1. **Content paths resolve relative to workspace config.** A per-repo override
   of "use this source file instead" doesn't compose cleanly with per-repo
   hooks/settings/env (which all layer by key/list). Content is a single
   source path, not a composable map.
2. **Symmetry with `GlobalOverride`.** The global override layer already
   excludes content for the same reason (it's a workspace-scoped concern).
   RepoOverride should mirror that exclusion.
3. **Auto-discovery already covers the per-repo case.** `content.go:190-204`
   auto-discovers `{content_dir}/repos/{repo}.md` when no explicit entry
   exists, so the workspace-level `[claude.content.repos.<name>]` is the one
   declarative knob; adding a second location is redundant.
4. **Silent ignore is worse than rejection.** A user writing
   `[repos.tsuku.claude.content]` expects it to do something. A parse warning
   points them at the correct key.

### Minimal enforcement

Two shapes are viable:

**Option 1 — Struct-level exclusion (preferred).** Keep `Content` *off* the
`ClaudeConfig` struct used by `RepoOverride`. Split the type: introduce a
narrower `ClaudeOverride` (no `Content`, no `Marketplaces`) for
`RepoOverride.Claude` and `InstanceConfig.Claude` and `GlobalOverride.Claude`,
and keep the full `ClaudeConfig` (with `Content`, `Marketplaces`) only at
`WorkspaceConfig.Claude`. The TOML decoder will then surface
`[repos.<name>.claude.content]` as an "unknown config field" warning
(via `md.Undecoded()` in `config.go:168-172`) automatically. Zero extra
validation code, self-documenting at the type layer.

**Option 2 — Validation rejection.** Put `Content` on the shared `ClaudeConfig`
but add a check in `validate()` that errors when
`cfg.Repos[name].Claude != nil && cfg.Repos[name].Claude.Content` is
non-zero, likewise for `cfg.Instance.Claude`. More code, less self-documenting,
but keeps a single type.

Option 1 is the right call because `ClaudeConfig` is already being reshaped to
add `Content` and `Marketplaces` is already workspace-scoped (see
`config.go:23-24`: the comment literally says "Marketplaces is workspace-wide.
Not merged from per-repo overrides"). A second workspace-scoped field joining
it is the natural time to split the type.

### Comparison to hooks merge semantics

`override.go:63-68` merges per-repo hooks by concatenating lists per event.
That works because hooks are additive: "also run this extra script." Content
is a single source path per repo — there is nothing to append to. Settings
(`override.go:55-60`) overwrite per key, which *could* theoretically extend
to content ("repo override wins"), but the merge semantics of a *source path*
are different from a settings scalar: if repo content overrides workspace
content, the workspace-level `[claude.content.repos.<name>]` slot becomes
meaningless for any repo with a local override, which is exactly the confusing
"two places define the same thing" problem the rename is trying to avoid.

Conclusion: content should not follow the hooks/settings merge pattern.

## Surprises

- The `Marketplaces` field already has a precedent for "workspace-scoped even
  though it lives on `ClaudeConfig`" (`config.go:23-24`). There's an existing
  comment-only convention that a type split would formalize.
- `GlobalOverride` (`config.go:255-259`) was already designed to exclude
  workspace-scoped fields. That design decision directly supports rejecting
  content on `RepoOverride.Claude` for the same reason.

## Open Questions

- Should the `Marketplaces` field migrate to the new `ClaudeOverride` vs
  `ClaudeConfig` split at the same time? The comment says it's not merged
  from overrides; a type-level split would enforce that statically. Out of
  scope for this lead but worth flagging to the design.
- If Option 1 is chosen, does `InstanceConfig.Claude` want `Content` either?
  Plausible use case: a per-instance CLAUDE.md distinct from the
  workspace-level one. Today `InstallWorkspaceContent` writes to
  `{instanceRoot}/CLAUDE.md`, so the workspace content already IS the instance
  content. Probably out of scope for the rename, but the type split decision
  should confirm.
- The `ParseResult.Warnings` mechanism (`config.go:168-172`) relies on
  `md.Undecoded()`. Confirm that nested fields like `repos.tsuku.claude.content`
  show up as unknown when the target struct lacks the field — this is
  BurntSushi/toml standard behavior but worth a spot check in a test.

## Summary

Today's per-repo claude override code in `override.go` never touches `cfg.Content`
at all — the two subtrees are independent. After the rename, the safest move is
to split `ClaudeConfig` so `RepoOverride.Claude` (and `InstanceConfig.Claude`,
`GlobalOverride.Claude`) use a narrower `ClaudeOverride` type that omits
`Content` (and `Marketplaces`), which makes `[repos.<name>.claude.content]` a
TOML decode-time "unknown field" warning for free. The biggest open question is
whether to also formalize `Marketplaces` in the same type split or leave it as
the comment-only convention it is today.
