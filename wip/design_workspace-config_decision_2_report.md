# Decision 2: CLAUDE.md content hierarchy in the TOML schema

## Status

Complete

## Context

niwa replaces a 700-line imperative bash installer that wires a three-level CLAUDE.md hierarchy across a multi-repo workspace. The hierarchy exploits Claude Code's parent-directory traversal: a session started in `$WORKSPACE/public/tsuku/recipes/` inherits context from `recipes/CLAUDE.local.md`, `tsuku/CLAUDE.local.md`, `public/CLAUDE.md`, and `$WORKSPACE/CLAUDE.md`.

The current installer does this:

1. Copies `CLAUDE_.md` source files to target locations, renaming them to `CLAUDE.md` or `CLAUDE.local.md`.
2. Substitutes `$WORKSPACE` via sed so content can reference absolute paths.
3. Uses `CLAUDE.local.md` (gitignored via `*.local*`) for per-repo content to avoid polluting tracked files.
4. Recurses into subdirectories (e.g., `tsuku/recipes/`, `tsuku/website/`, `tsuku/telemetry/`) to install nested content files.

The content files are free-form markdown with embedded structured properties (`Repo Visibility: Public`, `Default Scope: Tactical`). These properties are consumed by workflow commands that adapt behavior per-repo.

### What must be declared

| Level | Target path | Source | Tracked? | Current count |
|-------|------------|--------|----------|---------------|
| Workspace | `$WORKSPACE/CLAUDE.md` | `claude/workspace/CLAUDE_.md` | Committed | 1 |
| Group | `$WORKSPACE/public/CLAUDE.md` | `claude/public/CLAUDE_.md` | Committed | 2 (public, private) |
| Group | `$WORKSPACE/private/CLAUDE.md` | `claude/private/CLAUDE_.md` | Committed | (same) |
| Repo | `$WORKSPACE/public/tsuku/CLAUDE.local.md` | `claude/public/tsuku/CLAUDE_local.md` | Generated, gitignored | 6 repos |
| Subdir | `$WORKSPACE/public/tsuku/recipes/CLAUDE.local.md` | `claude/public/tsuku/recipes/CLAUDE_local.md` | Generated, gitignored | 3 subdirs under tsuku |

### Observations from the real content

Looking at the actual CLAUDE_local.md files, there's significant shared structure between repos:

- **tsuku** and **koto** both have identical "Repo Visibility" and "Default Scope" sections with the same boilerplate text.
- Each repo has unique content (structure, commands, conventions).
- Subdirectory files (recipes, website, telemetry) are entirely unique and short.

The shared parts account for roughly 20 lines per file. The unique parts range from 30 lines (koto) to 130 lines (tsuku). Templating the shared parts would save repetition but the unique parts are the bulk.

## Options

### Option A: Explicit file mapping at every level

Every content file is declared as an explicit source-to-target mapping. Nothing is inferred.

```toml
[workspace]
name = "tsukumogami"

[content.workspace]
source = "claude/workspace.md"
target = "CLAUDE.md"  # relative to workspace root
type = "committed"

[[content.groups]]
name = "public"
source = "claude/public.md"
target = "public/CLAUDE.md"
type = "committed"

[[content.groups]]
name = "private"
source = "claude/private.md"
target = "private/CLAUDE.md"
type = "committed"

[[content.repos]]
name = "tsuku"
source = "claude/repos/tsuku.md"
target = "public/tsuku/CLAUDE.local.md"
type = "generated"

[[content.repos]]
name = "koto"
source = "claude/repos/koto.md"
target = "public/koto/CLAUDE.local.md"
type = "generated"

[[content.repos]]
name = "niwa"
source = "claude/repos/niwa.md"
target = "public/niwa/CLAUDE.local.md"
type = "generated"

[[content.repos]]
name = "shirabe"
source = "claude/repos/shirabe.md"
target = "public/shirabe/CLAUDE.local.md"
type = "generated"

[[content.repos]]
name = "tools"
source = "claude/repos/tools.md"
target = "private/tools/CLAUDE.local.md"
type = "generated"

[[content.repos]]
name = "vision"
source = "claude/repos/vision.md"
target = "private/vision/CLAUDE.local.md"
type = "generated"

# Subdirectory content
[[content.subdirs]]
repo = "tsuku"
source = "claude/repos/tsuku-recipes.md"
target = "public/tsuku/recipes/CLAUDE.local.md"
type = "generated"

[[content.subdirs]]
repo = "tsuku"
source = "claude/repos/tsuku-website.md"
target = "public/tsuku/website/CLAUDE.local.md"
type = "generated"

[[content.subdirs]]
repo = "tsuku"
source = "claude/repos/tsuku-telemetry.md"
target = "public/tsuku/telemetry/CLAUDE.local.md"
type = "generated"
```

**Strengths:**
- Completely transparent. Every file mapping is visible in one place.
- No magic. A new user reads the config and knows exactly what gets installed where.
- Easy to add arbitrary content at any path - not limited by conventions.
- Simple implementation: iterate the lists, copy files, run substitution.
- Migration from install.sh is mechanical: translate each sed/cp into a config entry.

**Weaknesses:**
- Verbose. The tsukumogami workspace needs ~15 content entries. Larger workspaces would be worse.
- Redundant target paths. The target for repo content is derivable from the repo's group and name (`{group}/{repo}/CLAUDE.local.md`), so declaring it explicitly violates DRY.
- The `type` field (committed vs generated) is redundant with the filename convention (CLAUDE.md vs CLAUDE.local.md).
- No reuse mechanism for shared boilerplate across repos.

### Option B: Convention-based auto-generation

niwa generates content from structured properties declared in the config. Only repo-level content that can't be derived needs explicit source files.

```toml
[workspace]
name = "tsukumogami"
content_dir = "claude"

# Workspace CLAUDE.md is auto-generated from [workspace] properties
# plus claude/workspace.md if it exists

[groups.public]
visibility = "public"
# Auto-generates public/CLAUDE.md from visibility-appropriate template

[groups.private]
visibility = "private"
# Auto-generates private/CLAUDE.md from visibility-appropriate template

[repos.tsuku]
group = "public"
visibility = "public"
scope = "tactical"
# CLAUDE.local.md auto-generated from visibility + scope + claude/repos/tsuku.md

[repos.koto]
group = "public"
visibility = "public"
scope = "tactical"

[repos.niwa]
group = "public"
visibility = "public"
scope = "tactical"

[repos.shirabe]
group = "public"
visibility = "public"
scope = "tactical"

[repos.tools]
group = "private"
visibility = "private"
scope = "tactical"

[repos.vision]
group = "private"
visibility = "private"
scope = "strategic"

# Subdirectory content: declared per-repo
[repos.tsuku.subdirs]
recipes = { source = "claude/repos/tsuku-recipes.md" }
website = { source = "claude/repos/tsuku-website.md" }
telemetry = { source = "claude/repos/tsuku-telemetry.md" }
```

**Strengths:**
- Concise. Properties are declared once; niwa assembles the content.
- The `visibility` and `scope` properties that are currently embedded as free-text in markdown become first-class config fields, queryable by code.
- Group-level content is fully generated from the visibility value - no separate source file needed for the standard case.

**Weaknesses:**
- Black box. A user can't see the generated content without running `niwa sync` and inspecting the output. The mapping from config to files is hidden in code.
- Loss of control over group-level content. The current `public/CLAUDE.md` and `private/CLAUDE.md` files contain carefully crafted prose (tone guidelines, specific rules about competitor mentions). Auto-generation can't reproduce this without embedding that prose somewhere - either in Go templates or in config, both of which are worse than a markdown file.
- The "Repo Visibility" and "Default Scope" sections in real content files aren't just property values. They include contextual guidance ("Issues are atomic, implementable work items"). Generating this from a property loses nuance.
- Forces a specific content structure. Users who want different group-level content (e.g., a "shared-libs" group with custom guidelines) must learn the generation system's extension points.
- Template explosion if different repos need different structural patterns in their generated headers.

### Option C: Template model

Content "templates" are defined once and applied to multiple repos via pattern matching. Templates use variables for per-repo customization.

```toml
[workspace]
name = "tsukumogami"
content_dir = "claude"

[content.workspace]
source = "claude/workspace.md"

# Templates: define once, apply many
[templates.public-repo]
source = "claude/templates/public-repo-header.md"
# This template contains {repo_name}, {visibility}, {scope} variables

[templates.private-repo]
source = "claude/templates/private-repo-header.md"

[groups.public]
content = "claude/public.md"

[groups.private]
content = "claude/private.md"

[repos.tsuku]
group = "public"
template = "public-repo"
content = "claude/repos/tsuku.md"  # appended after template
variables = { scope = "tactical" }

[repos.koto]
group = "public"
template = "public-repo"
content = "claude/repos/koto.md"
variables = { scope = "tactical" }

[repos.niwa]
group = "public"
template = "public-repo"
content = "claude/repos/niwa.md"
variables = { scope = "tactical" }

[repos.shirabe]
group = "public"
template = "public-repo"
content = "claude/repos/shirabe.md"
variables = { scope = "tactical" }

[repos.tools]
group = "private"
template = "private-repo"
content = "claude/repos/tools.md"
variables = { scope = "tactical" }

[repos.vision]
group = "private"
template = "private-repo"
content = "claude/repos/vision.md"
variables = { scope = "strategic" }

# Subdirectory content (no template, just content)
[repos.tsuku.subdirs.recipes]
content = "claude/repos/tsuku-recipes.md"

[repos.tsuku.subdirs.website]
content = "claude/repos/tsuku-website.md"

[repos.tsuku.subdirs.telemetry]
content = "claude/repos/tsuku-telemetry.md"
```

The template `claude/templates/public-repo-header.md` would contain:
```markdown
## Repo Visibility: Public

This is a public repository. Content should be written for external consumption:
- **Design docs**: Focus on external audience clarity
...

## Default Scope: {scope}

This repo is for {scope} planning...
```

And the final CLAUDE.local.md is assembled: template output + content file.

**Strengths:**
- Shared boilerplate lives in one place (the template file), not duplicated across 6 repo content files.
- Variables are explicit in the config. You can see what each repo customizes.
- The template is a real markdown file, so it's easy to edit and preview.
- Flexible: repos can opt out of a template entirely and use pure content.

**Weaknesses:**
- Two-file assembly adds cognitive overhead. A user must read both the template and the content file to understand what a repo's CLAUDE.local.md will contain.
- The composition model needs rules: does the template go before or after content? What about variable conflicts? Can templates include other templates?
- For the tsukumogami workspace specifically, the shared boilerplate is only ~20 lines. The template machinery might not be worth it for this scale.
- Template variables beyond `{workspace}` and `{repo_name}` create an implicit contract. If a template uses `{scope}` but a repo doesn't define it, you need error handling and documentation for the variable catalog.
- Risk of template proliferation. As workspaces grow, users create many small templates for slightly different patterns, ending up with a template management problem instead of a content management problem.

### Option D: Hybrid model

Workspace and group levels use explicit content files (reference, not template). Repo level uses explicit content file references with convention-driven placement. Subdirectory level uses a per-repo list. Template variables are available in all content files.

```toml
[workspace]
name = "tsukumogami"
content_dir = "claude"

# Workspace-level content: installed to $WORKSPACE/CLAUDE.md
# Committed to the workspace (not gitignored)
[content.workspace]
source = "workspace.md"  # relative to content_dir

# Group-level content: installed to $WORKSPACE/{group}/CLAUDE.md
# Committed (not gitignored)
[content.groups.public]
source = "public.md"

[content.groups.private]
source = "private.md"

# Repo-level content: installed to $WORKSPACE/{group}/{repo}/CLAUDE.local.md
# Generated, gitignored - niwa ensures *.local* is in each repo's .gitignore
#
# Convention: if a repo has no explicit content entry but a file exists at
# content_dir/repos/{repo}.md, it is used automatically.
# Explicit entries override the convention.

[content.repos.tsuku]
source = "repos/tsuku.md"

  # Subdirectory content: installed to $WORKSPACE/{group}/{repo}/{subdir}/CLAUDE.local.md
  [content.repos.tsuku.subdirs]
  recipes = "repos/tsuku-recipes.md"
  website = "repos/tsuku-website.md"
  telemetry = "repos/tsuku-telemetry.md"

[content.repos.koto]
source = "repos/koto.md"

[content.repos.niwa]
source = "repos/niwa.md"

[content.repos.shirabe]
source = "repos/shirabe.md"

[content.repos.tools]
source = "repos/tools.md"

[content.repos.vision]
source = "repos/vision.md"
```

Template variables available in all content files:

| Variable | Value | Example |
|----------|-------|---------|
| `{workspace}` | Absolute path to workspace root | `/home/user/tsuku-3` |
| `{workspace_name}` | Workspace name from config | `tsukumogami` |
| `{repo_name}` | Repository name | `tsuku` |
| `{group_name}` | Group the repo belongs to | `public` |

The content directory layout mirrors the logical structure:

```
claude/
  workspace.md          -> $WORKSPACE/CLAUDE.md
  public.md             -> $WORKSPACE/public/CLAUDE.md
  private.md            -> $WORKSPACE/private/CLAUDE.md
  repos/
    tsuku.md            -> $WORKSPACE/public/tsuku/CLAUDE.local.md
    tsuku-recipes.md    -> $WORKSPACE/public/tsuku/recipes/CLAUDE.local.md
    tsuku-website.md    -> $WORKSPACE/public/tsuku/website/CLAUDE.local.md
    tsuku-telemetry.md  -> $WORKSPACE/public/tsuku/telemetry/CLAUDE.local.md
    koto.md             -> $WORKSPACE/public/koto/CLAUDE.local.md
    niwa.md             -> $WORKSPACE/public/niwa/CLAUDE.local.md
    shirabe.md          -> $WORKSPACE/public/shirabe/CLAUDE.local.md
    tools.md            -> $WORKSPACE/private/tools/CLAUDE.local.md
    vision.md           -> $WORKSPACE/private/vision/CLAUDE.local.md
```

**Strengths:**
- Clear mental model: content files map 1:1 to output files. The config declares the mapping; the content lives in separate files.
- Target paths are derived from convention, not declared. A repo in group "public" named "tsuku" always gets `public/tsuku/CLAUDE.local.md`. You don't type the target path.
- Workspace and group content are explicit (because they're small in number and contain carefully crafted prose). Repo content is also explicit (because each repo's content is unique). But none of them require target path declaration.
- Template variables are simple: 4 variables, all derivable from config. No user-defined variable catalog.
- File-exists convention (`content_dir/repos/{repo}.md`) means a minimal config can omit `[content.repos.*]` entries entirely if the files follow naming convention. This makes the zero-config path clean while still allowing explicit overrides.
- Subdirectory content is the only place that requires explicit declaration, which is appropriate because subdirectory configs are an exception rather than the rule.
- Migration from install.sh is straightforward: rename files to drop the underscore convention, write the TOML, done.

**Weaknesses:**
- Doesn't solve boilerplate duplication across repo content files. The "Repo Visibility" and "Default Scope" sections are still copy-pasted. However, this is arguably a content authoring problem, not a config schema problem.
- The file-exists convention (auto-discovery of `repos/{name}.md`) adds a small amount of implicit behavior. Users must know the convention to understand why a repo gets content without an explicit config entry.
- No reuse mechanism if two repos need identical content. But in practice this doesn't happen in the tsukumogami workspace - every repo has unique content.

## Analysis

### Dimension 1: Understandability for new users

A new user needs to answer: "what content goes where, and how do I change it?"

| Option | Learning curve | Mental model |
|--------|---------------|--------------|
| A (explicit) | Low for reading, tedious for writing | "Config lists every mapping" |
| B (auto-gen) | High - must understand generation rules | "Config describes properties, tool generates content" |
| C (template) | Medium - must understand template + content assembly | "Templates produce headers, content files add the rest" |
| D (hybrid) | Low - convention is simple, explicit when needed | "Content files map to output files, target paths follow convention" |

Option A is the most transparent but also the most tedious to write. Option B is the hardest to understand because the mapping between config properties and generated output is invisible. Option D strikes the best balance: you can understand the system by looking at the config and the content directory together.

### Dimension 2: Migration from install.sh

The current installer has these content-related operations:

1. Copy workspace CLAUDE_.md with $WORKSPACE substitution
2. Copy group CLAUDE_.md files with $WORKSPACE substitution
3. Recursively copy repo CLAUDE_local.md files (renaming to CLAUDE.local.md) with $WORKSPACE substitution
4. Ensure *.local* is in each repo's .gitignore

| Option | Migration effort |
|--------|-----------------|
| A | Mechanical: one config entry per existing cp/sed line |
| B | Requires decomposing existing content files into "properties" + "unique content" |
| C | Requires splitting existing content files into "template part" + "unique part" |
| D | Rename files, write compact config, done |

Option B and C require restructuring existing content, which introduces risk of content regression. Options A and D work with the existing files as-is (just renamed).

### Dimension 3: Handling custom group-level content

What if a user wants a group called "experimental" with its own custom CLAUDE.md?

| Option | Effort |
|--------|--------|
| A | Add a `[[content.groups]]` entry. Simple. |
| B | Not possible without a custom visibility type or escape hatch. |
| C | Add a `[groups.experimental]` entry with a content file. Works. |
| D | Add `[content.groups.experimental]` with a source file. Simple. |

Option B fails here because group content is derived from visibility, and "experimental" isn't a standard visibility value. All other options handle this naturally.

### Dimension 4: Subdirectory configs

The tsuku monorepo has 3 subdirectory configs. Other workspaces might have more or fewer.

| Option | How subdirs work |
|--------|-----------------|
| A | Each subdir is a `[[content.subdirs]]` entry with full path. Verbose but clear. |
| B | Subdirs declared per-repo with source path. Compact. |
| C | Subdirs declared per-repo, no template (too granular). Compact. |
| D | Subdirs declared per-repo as a name-to-source map. Compact and clear. |

All options handle this. The difference is ergonomics: Option A requires the full target path, others derive it.

### Dimension 5: Template variables

The current installer substitutes only `$WORKSPACE`. What should niwa support?

The workspace path is essential - content files reference absolute paths for directory structures. Repo name and group name are derivable and useful for content that says "this is the {repo_name} repository." Beyond these, custom variables create maintenance burden without clear benefit.

Recommended set: `{workspace}`, `{workspace_name}`, `{repo_name}`, `{group_name}`. These are all derived from the config itself. No user-defined variables in v0.1 - that can be added later if needed.

### Dimension 6: Content by reference guarantee

All options maintain content by reference (content lives in .md files, config points to them). Option B partially violates this by generating some content from properties, meaning you'd need to run `niwa sync` to see the actual output. Options A, C, and D keep a strict 1:1 relationship between source files and output.

## Decision

**Option D: Hybrid with convention-driven placement.**

### Rationale

The config format must serve two audiences: power users who need full control and new users who need to understand the system quickly. Option D gives both. The content directory layout mirrors the logical hierarchy, making it easy to find and edit content. Convention-driven target paths eliminate redundant declarations without hiding behavior behind generation logic.

The key insight from studying the real content files is that there's no meaningful content that should be auto-generated. The workspace CLAUDE.md is 180 lines of carefully written prose. The group files are short but specific. The repo files contain unique technical context. Trying to generate any of this from config properties (Option B) or assemble it from templates (Option C) adds machinery without real benefit. The content is the content; the config's job is to say where it goes.

Option D also leaves the door open for templates in a future version. If a workspace with 50 repos needs boilerplate deduplication, a `template` field can be added to `[content.repos.*]` without changing the core schema. Starting without templates is the right call because the tsukumogami workspace (and likely most early adopters) doesn't need them.

### Template variables

v0.1 ships with four built-in variables:

| Variable | Source |
|----------|--------|
| `{workspace}` | Resolved at sync time (absolute path to workspace root) |
| `{workspace_name}` | `[workspace].name` |
| `{repo_name}` | Key from `[repos.*]` table |
| `{group_name}` | Derived from `[repos.*].group` |

User-defined variables are deferred. They can be added later as `[variables]` table entries without breaking the schema.

### File type convention

- `CLAUDE.md` at workspace and group levels: committed to version control, written by the workspace author.
- `CLAUDE.local.md` at repo and subdirectory levels: generated by niwa, gitignored. niwa ensures `*.local*` is in each repo's `.gitignore`.

This matches the current installer behavior and Claude Code's discovery mechanism.

### Convention-over-configuration path

When `content_dir` is set and the config omits `[content.repos.X]` for a repo:

1. niwa checks for `{content_dir}/repos/{repo_name}.md`
2. If found, uses it as the source for `{group}/{repo}/CLAUDE.local.md`
3. If not found, the repo gets no CLAUDE.local.md (which is valid - it still inherits from group and workspace levels)

This means a minimal config for a 6-repo workspace with content files following the naming convention would be:

```toml
[workspace]
name = "tsukumogami"
content_dir = "claude"

[content.workspace]
source = "workspace.md"

[content.groups.public]
source = "public.md"

[content.groups.private]
source = "private.md"

# Repos with convention-named content files need no [content.repos.*] entries.
# Only tsuku needs explicit subdirectory declarations:
[content.repos.tsuku.subdirs]
recipes = "repos/tsuku-recipes.md"
website = "repos/tsuku-website.md"
telemetry = "repos/tsuku-telemetry.md"
```

The full explicit form (shown in Option D above) is also valid, serving as documentation and preventing surprises for users who prefer explicit over implicit.

## Summary

```
status: complete
chosen: D - Hybrid with convention-driven placement
confidence: high
rationale: Content files are the content; the config's job is mapping, not generation.
  Convention-driven target paths eliminate boilerplate without hiding behavior.
  The schema leaves room for templates later without requiring them now.
assumptions:
  - Most workspaces will have fewer than 20 repos, so explicit content entries
    are manageable even without templates
  - Content boilerplate duplication across repo files is acceptable for v0.1
    and can be addressed with optional templates in a later version
  - Four built-in template variables cover the real substitution needs
  - Users prefer seeing content files 1:1 with output over generated/assembled content
rejected:
  - A (explicit mapping): target paths are redundant given group/repo structure;
    too verbose for the benefit
  - B (convention-based auto-generation): group content can't be generated
    without losing carefully crafted prose; generation model is a black box
  - C (template model): adds composition complexity for ~20 lines of shared
    boilerplate; template proliferation risk outweighs deduplication benefit
```
