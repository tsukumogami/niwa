# Lead: Does migrate-config's structure offer a reusable pattern for a new skill?

## Findings

**Manifest registration** (`internal/plugin/files/niwa/manifest.json`): a top-level JSON with `name: "niwa"`, `version`, `description`, and a `skills` array. Each entry has `name`, `path` (relative to the plugin root, e.g. `skills/migrate-config/SKILL.md`), and its own `description`. Currently only one entry exists for `migrate-config`.

**SKILL.md conventions** (`internal/plugin/files/niwa/skills/migrate-config/SKILL.md`):
- YAML frontmatter with just two keys: `name: niwa-migrate-config` (plugin-prefixed, kebab-case) and `description:` (one sentence, third-person, states when to use it).
- Body opens with an H1 restating the invocation form (`# /niwa:migrate-config`), then states explicitly `This skill is invoked as /niwa:migrate-config <workspace-name>.`
- Structured with `## What this skill is for`, `## How to use this skill` (numbered steps, each step naming exact tools to use -- Bash, Read, Edit -- and exact CLI commands like `niwa source inspect <slug> --json`), and a closing `## What this skill MUST NOT do` guardrails section (bulleted "MUST NOT" list).
- Tone is directive/procedural, written for the agent (not the end user), ~53 lines total, medium length.
- Ends with a one-line reaffirmation of scope ("The skill is advisory: it inspects, plans, and edits `~/.config/niwa/config.toml` only.").

**Invocation mechanism**: it's a standard Claude Code plugin slash command, `/niwa:migrate-config <workspace-name>` -- namespaced `<plugin-name>:<skill-name>`, not auto-triggered by description matching. The manifest's `skills` array is what Claude Code's plugin loader reads to register it as a slash command; nothing in the niwa Go code parses that array (see below).

**Install path and trigger**: `internal/plugin/embed.go` embeds the whole `files/niwa` tree via `//go:embed files/niwa` and computes install path `~/.claude/plugins/marketplaces/niwa/` from `$HOME`. `internal/plugin/installer.go`'s `Install()` does an idempotent version-check-then-atomic-stage-and-rename of the entire embedded tree -- it doesn't care how many skills exist, it just copies everything under `files/niwa`.

Critically, install is gated, not unconditional. Every call site of `InstallNiwaPlugin` (`internal/workspace/apply.go` lines ~450, ~606, ~930, ~959, plus `internal/cli/init.go:701`) fires only inside an `if teamConfigRank == 2 && ...` or `if overlayRank == 2 && ...` branch -- i.e. only when a rank-2 (deprecated whole-repo) config source is detected. There is also `internal/cli/plugins.go` (`niwa plugins install`), a manual unconditional install command, and a wholly separate `PrewarmDeclaredPlugins` mechanism (`internal/cli/plugin_adapter.go`) for workspace-declared third-party plugins -- unrelated to this embedded niwa plugin.

**Adding a second skill mechanically**: the manifest's Go-side `manifest` struct (`internal/plugin/embed.go`) only parses `Name`, `Version`, `Description` -- it never reads or validates the `skills` array. Nothing in `installer_test.go` or `embed_test.go` checks that `manifest.json`'s `skills` list matches the files on disk. So from niwa's Go code's perspective, adding `skills/<new-skill>/SKILL.md` "just works" the moment it's a child of `files/niwa` -- the `go:embed` directive and `writeEmbeddedTree`'s `fs.WalkDir` pick up any new file automatically, no Go wiring needed. But `manifest.json`'s `skills` array must still be hand-edited to add the new entry (that's what Claude Code's plugin loader uses to register the slash command) -- that step is not automatic. Per convention, `version` should probably also bump per docs/wip design notes (not directly enforced in code, but `Install()`'s idempotence check compares `manifest.json`'s `version` field to decide whether to reinstall).

**Prior design context** (found in the repo's own design docs, referencing an original `DESIGN-config-source-discovery.md`-style naming decision): the plugin was deliberately named bare `niwa`, not `niwa-migration`, specifically to accommodate future niwa-owned skills -- the current single-skill setup was designed with multi-skill growth in mind (mentions `migrate-snapshot`, `migrate-overlay` as anticipated future skills).

## Implications

- Structurally, dropping in `skills/workspace-config-edit/SKILL.md` following the same frontmatter/section conventions (name, description, invocation line, "What this is for" / numbered "How to use" naming exact tools+CLI commands / "MUST NOT" guardrails) is straightforward and matches an established, intentional pattern.
- The only two required edits to "wire it in" are: (1) create the new `skills/<name>/SKILL.md` file, (2) add its entry to `manifest.json`'s `skills` array (and likely bump `version`). No Go code changes are needed purely to ship the file -- `go:embed` and the installer are directory-agnostic.
- However, the bigger blocker for the stated goal (getting config-editing guidance into rank-1/single-repo workspaces) is not the skill-authoring pattern but the install gate: today the plugin only ever installs when a rank-2 source is detected. A rank-1-only workspace -- the normal case this new skill targets -- will never get the plugin installed via the existing `InstallNiwaPlugin` call sites. Landing the new skill in this same plugin means either (a) also loosening/adding a new install trigger (e.g., unconditional install, or gated on rank-1 detection instead of/in addition to rank-2), or (b) the skill effectively only reaches users who already hit the rank-2 migration path -- which seems like a mismatch with the stated use case.

## Surprises

- The `manifest.json` `skills` array is purely documentary/consumed by Claude Code's plugin loader -- niwa's own Go code and tests never validate it against the actual files on disk, so it's easy for it to silently drift from `skills/*/SKILL.md` if someone forgets the manual edit.
- The embedded-plugin install is entirely rank-2-triggered; there is no code path today that installs this plugin for a purely rank-1 workspace, which directly affects whether adding a new skill to this plugin is sufficient to solve the parent lead's real goal.
- There are two unrelated "skills" mechanisms in the codebase: this embedded Claude Code plugin (`internal/plugin/`, installed to `~/.claude/plugins/marketplaces/niwa/`), and a separate `internal/workspace/root_materializer.go` embed (`rootskills` -> `.claude/skills/` inside the workspace root) used for workspace-root project skills -- worth not conflating when deciding where the new config-editing skill should live.

## Open Questions

- Should the new config-editing skill's install be gated on rank-1 detection (or fire unconditionally at `niwa init --bootstrap`/apply time) rather than piggybacking on the rank-2-only gate that currently governs `InstallNiwaPlugin`?
- Should `manifest.json`'s `skills` array gain a corresponding Go-side validation/test to prevent drift, especially once there's more than one skill?
- Does `version` bumping in `manifest.json` need to happen on every skill addition given `Install()`'s idempotence check keys off it?

## Summary
`migrate-config`'s `SKILL.md` establishes a clear, reusable authoring pattern (frontmatter name/description, invocation line, "What this is for" / numbered "How to use" naming exact tools and CLI commands, "MUST NOT" guardrails) that a new config-editing skill could follow directly, and mechanically adding a second skill file requires no Go changes beyond editing `manifest.json`'s `skills` array (the `go:embed`/installer already walk the whole directory). The real obstacle isn't the skill-authoring pattern but the install trigger: every `InstallNiwaPlugin` call site in `internal/workspace/apply.go` and `internal/cli/init.go` fires only when a rank-2 (deprecated) config source is detected, so this plugin -- and any new skill riding along in it -- never installs for the rank-1-only, single-repo workspaces the new skill is meant to serve. Landing the new skill here would also require rethinking (or adding to) that install gate.
