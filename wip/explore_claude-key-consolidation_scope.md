# Explore Scope: claude-key-consolidation

## Visibility

Public

## Core Question

What does it take to rename the top-level `[content]` table in
`workspace.toml` to `[claude.content]` so the Claude-specific semantics
(`source = "..."` resolves to `CLAUDE.md` / `CLAUDE.local.md`) are
explicit in the schema instead of implicit behind a generic key name?

## Context

niwa's `workspace.toml` today has:

- `[claude]` — explicitly-Claude-specific settings (`hooks`, `settings`,
  `plugins`, `env`).
- `[content]` — generic-sounding top-level table whose only purpose is
  declaring where `CLAUDE.md` / `CLAUDE.local.md` content comes from
  (per-workspace, per-group, per-repo, per-subdir).

The user's concern: nothing in `[content]`'s name or shape signals "this
is about CLAUDE.md". Future readers have to infer the coupling. The fix
is to re-home the same shape under the existing `[claude]` namespace so
the coupling is explicit in the schema.

No structural change to the tree is proposed — just the path. Same
sub-tables (`workspace`, `groups`, `repos`) move with their contents
intact.

## Decided (before research)

- **Migration policy**: accept both `[content]` and `[claude.content]`
  for N releases with a deprecation warning on use of the old form.
  The user confirmed this during scoping.

## In Scope

- Auditing consumers of `ContentConfig` to confirm the Claude coupling.
- Estimating code impact of the rename (Go struct tags, call-site
  updates, tests).
- Interaction with per-repo overrides (`[repos.<name>.claude]` + new
  `[claude.content.repos.<name>]`).

## Out of Scope

- Moving `workspace.content_dir` — orthogonal naming question the user
  didn't raise.
- Merging `[files]` or other adjacent keys into `[claude]`.
- Changing the semantics or shape of the content sub-tables — only the
  path to the root changes.

## Research Leads

1. **Is `[content]` exclusively Claude-coupled, or does it ever produce
   non-CLAUDE.md artifacts?**
   Audit every consumer of `cfg.Content.*` in `internal/workspace/` and
   the tests. Confirm that `source = "..."` only ever resolves into
   CLAUDE.md or CLAUDE.local.md. If any path treats these entries as
   generic content (README, plugin manifests, etc.), the rename to
   `[claude.content]` would misrepresent — surface this as a blocker.

2. **Code impact of the rename.**
   Enumerate every Go file and test referencing `cfg.Content`,
   `ContentConfig`, `ContentEntry`, `RepoContentEntry`. Determine
   whether the migration is (a) a pure syntactic move — add `Content
   ContentConfig` field to `ClaudeConfig`, retire the top-level
   `Content` field, update tests; or (b) a deeper restructuring that
   would force changes elsewhere (merge semantics, validation,
   materialization). Count LOC delta and affected tests.

3. **Per-repo override interaction.**
   Today `[repos.<name>.claude]` exists as a per-repo override with the
   full `ClaudeConfig` shape. After this change, `ClaudeConfig` gains a
   `Content` field, meaning per-repo CLAUDE.md overrides could live at
   `[repos.<name>.claude.content]`. Today's per-repo content lives at
   `[content.repos.<name>]` instead. After the migration, is there
   ambiguity or duplication (can content for a given repo be defined in
   two places)? How does the existing merge logic behave when both are
   populated? If the answer is "there's no per-repo content override
   in the overlap sense", note that plainly and move on.
