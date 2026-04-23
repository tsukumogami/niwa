# Lead: What conventions should niwa adopt for auto-discovering the config location within a brain repo?

## Findings

### Today's niwa config-dir shape

niwa's config layout is hard-coded in `internal/config/discover.go`:

- `ConfigDir = ".niwa"` and `ConfigFile = "workspace.toml"` are constants.
- `Discover(startDir)` walks upward from `startDir` looking for `.niwa/workspace.toml`. It returns the first hit and errors out with `"no .niwa/workspace.toml found in any parent of <startDir>"` if the walk reaches the filesystem root.
- The discovery is **filesystem-local only**. There is no notion of "discover the config location *inside* a remote repo." When sourcing a config repo today, niwa assumes the entire remote repo *is* the `.niwa` content and clones it directly into `<workspace>/.niwa/` (see `internal/cli/init.go:135-141` calling `cloner.CloneWith(..., niwaDir, ...)`).
- A valid `.niwa/` directory must contain at minimum a `workspace.toml` whose `[workspace]` block has a `name` field matching `[a-zA-Z0-9._-]+` (`internal/config/config.go:316-323`). The example in `tsuku/.niwa/workspace.toml` shows the typical companion content: a `claude/` content dir, an `env/` dir, an `extensions/` dir, and a `hooks/` dir, all referenced from `workspace.toml` via relative paths.
- Path-traversal in any content `source` is rejected (`validateContentSource`), which means the supporting dirs (`claude/`, `hooks/`, `env/`) are required to live under the same dir as `workspace.toml`. **The config dir is not just one file — it is `workspace.toml` plus an arbitrary supporting tree.** This rules out the "single `niwa.toml` file as complete config" interpretation in any non-toy setup.

### Peer-tool conventions surveyed

| Tool | Marker(s) | Walk strategy | Tie-break / collision | Error UX |
|---|---|---|---|---|
| **EditorConfig** | `.editorconfig` | Upward from target file; stops at `root = true` or filesystem root | Closer file wins per-key; multiple files merge with proximity precedence | Silent: missing file = no overrides applied |
| **Cargo** | `Cargo.toml` with `[workspace]` table | Upward from CWD until `[workspace]` is found | `package.workspace` key in member crate can override the auto-search; "virtual manifest" (no `[package]`) explicitly marks a workspace root | Hard error if member's resolved root has conflicting `[workspace]` entries; clear "could not find Cargo.toml" message |
| **chezmoi** | `.chezmoiroot` (file at source root containing relative path to actual source dir) | Read once at source root before any other file | Single signal — no precedence question; `.chezmoiroot` is read first and authoritative | Errors if path inside `.chezmoiroot` doesn't resolve |
| **direnv** | `.envrc` | Upward walk from CWD on every prompt | Each `.envrc` contributes; explicit `source_up` for inheritance | Silent until `direnv allow` is run; then loads on every cd |
| **git** | `.git/` directory or `.git` file (worktrees) | Upward walk from CWD | First hit wins; `GIT_DIR` env override is explicit | Hard error: "not a git repository" |
| **npm/pnpm** | `package.json` with `"workspaces"` field | Root manifest only; no upward walk past first `package.json` | Globs in `workspaces` enumerate members; no precedence between root and members for the workspace marker itself | Silent if field missing — degrades to single-package mode |
| **Renovate** | Looks in this order: `renovate.json`, `renovate.json5`, `.github/renovate.json`, `.github/renovate.json5`, `.gitlab/renovate.json`, `.renovaterc`, `.renovaterc.json`, `package.json` (renovate field) | Root only; first match wins, search stops | First-match precedence is documented and stable | Onboards via PR if no config found; never errors silently |
| **Terraform module sources** | Explicit `//` separator in source URL (`git::https://example.com/repo.git//modules/vpc?ref=v1`) | No discovery — explicit subpath always | N/A (explicit) | Hard error if subpath doesn't exist after fetch |
| **Nix flakes** | `flake.nix` at repo root or at `?dir=<subpath>` | No discovery — explicit `dir=` query param or root | N/A (explicit); registry currently can't *store* `dir=`, only consume it | Hard error if `flake.nix` not found at expected location |
| **GitHub Actions reusable workflows** | `org/repo/.github/workflows/foo.yml@ref` | No discovery — fixed convention path `.github/workflows/` is **mandatory** | N/A; rigid | Hard error if path doesn't exist |
| **Helm chart repo** | `index.yaml` at repo root + flat tarballs | Index-driven; no discovery walk | Index is authoritative | Errors if `index.yaml` missing |

### Candidate conventions for niwa

1. **`.niwa/` directory at repo root**
   - Implies: "this brain repo also hosts a niwa workspace config." Mirrors the standalone-repo layout exactly — what users see in `dot-niwa` repos today moves verbatim to a subdirectory of the brain repo.
   - Most useful when: brain repo carries supporting content (hooks, env, content/), which is the realistic case for any non-toy workspace.
   - Validation: `.niwa/workspace.toml` must exist and parse. A `.niwa/` with no `workspace.toml` is treated as "not a niwa source" rather than an error (it's a coincidental dir name in someone else's repo).
   - Cost: collides with niwa's own filesystem-local `.niwa/` convention if a developer happens to run `niwa init` inside a checkout of the brain repo. (Bearable — same name, same meaning.)

2. **`niwa.toml` at repo root (single file)**
   - Implies: "the workspace config lives in this one file; supporting content lives alongside it at the repo root." Treats the brain repo root *as* the config dir.
   - Most useful when: the brain repo IS naturally the workspace (codespar-web case where the Next.js root is the brain).
   - Validation: must contain a `[workspace]` block with a valid `name`.
   - Trap: `content_dir`, `hooks/`, `env/` paths in `niwa.toml` would resolve relative to repo root. That makes the brain repo's own working tree leak into niwa's view — `claude/` content references could pick up brain-repo files unintentionally. Mitigation: require an explicit `content_dir = "..."` rather than the implicit root, *or* require a sibling `.niwa/` dir for supporting content.

3. **`dot-niwa/` directory at root (mirroring the standalone-repo name)**
   - Implies: "this folder is what would be the standalone `dot-niwa` repo." Friendlier-to-read than `.niwa/` (no leading dot, visible in plain `ls`).
   - Most useful when: brain repo has cultural reasons to avoid hidden directories.
   - Cost: introduces a third convention name for the same concept (`.niwa/`, `dot-niwa/`, `niwa.toml`). The existing standalone-repo naming is `dot-niwa` *as a repo name* (e.g., `org/dot-niwa`); turning it into a directory name inside another repo blurs the meaning.
   - Recommendation: do **not** adopt as a discovery signal. Reserve `dot-niwa` strictly as a standalone-repo name; inside a brain repo, the convention is `.niwa/`.

4. **`workspace.toml` at root (treat the whole repo as the config)**
   - Implies: "this whole repo is a niwa workspace config." Indistinguishable from today's standalone `dot-niwa/` repos — they already have `workspace.toml` at root.
   - Most useful when: zero migration for existing standalone-repo users — discovery just finds their existing `workspace.toml` and concludes "the config dir is `/`".
   - Validation: must parse and have a `[workspace]` block. Same as for `.niwa/workspace.toml`.

## Precedence Proposal

Discovery looks at the repo root only (no recursive walk — see Implications below). It checks signals in this fixed order and stops at the first match:

| Rank | Signal | Resolved config dir | Rationale |
|---|---|---|---|
| 1 | **Explicit override in slug** (`org/repo:path/to/config@ref`) | `path/to/config` | User intent always wins. No discovery runs. |
| 2 | `.niwa/workspace.toml` at repo root | `.niwa/` | Highest-fidelity match. Mirrors standalone-repo layout, supports arbitrary supporting content under `.niwa/`, and is unambiguous (no other tool claims `.niwa/`). |
| 3 | `workspace.toml` at repo root | `/` (repo root) | Backwards-compatible with existing standalone `dot-niwa` repos. No migration required. |
| 4 | `niwa.toml` at repo root | `/` (repo root) | One-file convention for the brain-repo case. Repo root becomes the config dir; supporting content lives alongside `niwa.toml`. |
| 5 | (no signal) | error | Hard error with hint: "no niwa config found in `<repo>`. Expected one of: `.niwa/workspace.toml`, `workspace.toml`, or `niwa.toml` at repo root, or pass an explicit subpath via `org/repo:path/to/config`." |

**Tie-break on multiple signals**: If both `.niwa/workspace.toml` and a root-level `workspace.toml` or `niwa.toml` exist, **hard error**, do not pick deterministically. Rationale: the brain-repo author almost certainly didn't intend two configs; silent precedence would let one shadow the other across renames. The error message names both files and tells the user to remove one. (Cargo, chezmoi, and Renovate all hard-error on conflicting markers; only EditorConfig uses deterministic precedence and its model is "additive merge," not "pick one source of truth.")

**`.niwa/` exists but contains no `workspace.toml`**: Treat the `.niwa/` signal as not present and try the next signal in the table. Do **not** error: the directory name is plausible inside a brain repo for an unrelated reason (e.g., a `.niwa/` someone created for a different tool). Only `niwa.toml` and root `workspace.toml` are root-only files distinctive enough to error on if invalid.

**Validation after a signal is found**: The chosen `workspace.toml` must parse cleanly and pass the existing `validate()` rules (workspace.name set, source orgs valid, etc.). Parse failure is a hard error — discovery succeeded but the user's config is broken; we report exactly what's wrong rather than falling through to a lower-ranked signal (that would mask bugs).

**Explicit-override syntax**: Adopt `owner/repo:path/to/config@ref`, with the colon as the subpath separator and `@` as the ref separator. Compared with alternatives:

- `:` is unambiguous in URL slugs (already not legal in repo names) and matches Renovate's preset syntax (`org/repo:preset-name`), which the user community recognizes.
- `//` (Terraform's choice) requires upgrading parser to handle URL-like input, conflicts visually with protocol slashes (`git::https://...//path`), and is widely complained about.
- `?dir=` (Nix's choice) requires niwa slugs to look like full URLs, which conflicts with the existing terse `org/repo` shorthand.
- `#` would collide with shell comment behavior in unquoted CLI args.
- The ref separator `@` is already established by `niwa init --from` semantics and matches Go module syntax.

Parse complexity is trivial: one split on `:` (split into `owner/repo` and `subpath@ref`), then one split on `@` for ref. Slug grammar regex: `^[^/:]+/[^:/]+(:[^@]+)?(@.+)?$`.

## Discovery Behavior Matrix

| Scenario | Discovery outcome | Config dir resolved to | Error / warning |
|---|---|---|---|
| Only `.niwa/workspace.toml` present | Found via rank 2 | `.niwa/` | none |
| Only `workspace.toml` at root | Found via rank 3 | `/` | none |
| Only `niwa.toml` at root | Found via rank 4 | `/` | none |
| `.niwa/workspace.toml` + `workspace.toml` at root | **Hard error** | n/a | "Ambiguous niwa config in `<repo>`: both `.niwa/workspace.toml` and `workspace.toml` exist. Remove one or pass an explicit subpath." |
| `.niwa/workspace.toml` + `niwa.toml` at root | **Hard error** | n/a | Same shape as above with `niwa.toml` named. |
| `workspace.toml` + `niwa.toml` at root | **Hard error** | n/a | Same shape. |
| `.niwa/` exists but empty / no `workspace.toml`, root has `niwa.toml` | Skip rank 2, find rank 4 | `/` | Optional debug log: "skipping `.niwa/`: no `workspace.toml` inside." No user-visible warning. |
| `.niwa/workspace.toml` exists but fails `validate()` | **Hard error** at rank 2 | n/a | Standard parse-error message; do not fall through. |
| Nothing present | **Hard error** | n/a | "No niwa config found in `<repo>`. Expected `.niwa/workspace.toml`, `workspace.toml`, or `niwa.toml` at repo root. Pass `--from owner/repo:path/to/config` to use a different subpath." |
| Explicit subpath `org/repo:path` provided | Skip discovery entirely | `path/` | Hard error if `path/workspace.toml` doesn't exist; do not fall back to discovery. |
| Explicit subpath points at file rather than dir (`org/repo:path/to/niwa.toml`) | Treat as "config dir is the dir containing this file" | `path/to/` | Same single-file resolution as rank 4. |
| `.niwa/` at root + supporting content (`.niwa/claude/`, `.niwa/hooks/`) | Found via rank 2; supporting content available | `.niwa/` | none |
| `niwa.toml` at root + brain-repo content alongside (`docs/`, `src/`, `CLAUDE.md`) | Found via rank 4 | `/` | Risk: niwa "sees" the whole brain repo. Mitigated by requiring `content_dir` to be set in `niwa.toml` (proposed validation: when discovery resolves to repo root via `niwa.toml`, `[workspace] content_dir` becomes a *required* field, not optional). |

## Implications

**Slug grammar**: `owner/repo[:subpath][@ref]`. Subpath is a forward-slash path interpreted relative to the repo root. Empty subpath (or `:/`) means "repo root, run discovery." Ref defaults to the repo's default branch. The grammar is small enough to parse without a URL library.

**Registry schema**: `RegistryEntry` needs to grow a `Subpath` field (and probably `Ref`). The existing `Source` (absolute path to config file on disk) and `Root` (absolute workspace root) stay. `SourceURL` becomes the canonical slug form including subpath/ref. Migration: existing entries with no subpath/ref keep working — they're the degenerate "subpath = `/`, ref = HEAD" case.

**Discovery cacheability**: Yes — once discovery resolves `org/brain-repo` → `org/brain-repo` + subpath `.niwa/`, the resolution should be persisted. Best home: the registry entry itself, by storing the *resolved* slug (`org/brain-repo:.niwa`) rather than the *user-typed* slug (`org/brain-repo`). The next apply uses the resolved slug directly and skips discovery. If the brain repo later moves the config (e.g., `.niwa/` → `niwa.toml`), users see a clear "config not found at recorded subpath" error rather than silent re-discovery into a different layout. State (per-instance) doesn't need a separate cache — the registry is the source of truth.

**Error messages**: Three distinct failure shapes need clear copy:
1. *Discovery failed*: "no signal found at repo root" — list the three accepted markers.
2. *Discovery ambiguous*: "multiple signals at root" — name the conflicting files.
3. *Discovery succeeded but config invalid*: standard `validate()` error, prefixed with the resolved subpath so the user knows where to look.

**Migration story**:
- Existing `org/dot-niwa` repos with `workspace.toml` at root → discovered via rank 3, no breaking change.
- Existing `niwa init --from org/dot-niwa` invocations continue to work; the registry entry just gets the resolved slug `org/dot-niwa:` (empty subpath = root).
- Brain-repo adoption is purely additive: drop a `.niwa/workspace.toml` (or `niwa.toml`) into the brain repo, then `niwa init --from org/brain-repo`. Existing brain-repo workflows are untouched.

**No upward walk inside the remote repo**: Unlike git/EditorConfig/direnv, niwa fetches a remote repo and inspects only the *root*. Walking deeper would cost extra fetches (subpath sourcing means we don't have the whole tree locally) and introduces ambiguity (which `.niwa/` wins if there are several?). Constraining discovery to root keeps the model intelligible and aligns with Renovate's first-match-at-root model — the closest precedent for "find a config file in a hosted repo."

**`niwa.toml` content_dir requirement**: When `niwa.toml` discovery resolves to repo root, niwa should require `[workspace] content_dir` to be explicitly set (it is currently optional). This avoids the trap where a brain repo's `docs/`, `src/`, `CLAUDE.md` etc. become reachable as content sources. The validator can detect this case (resolved subpath = `/` AND signal was `niwa.toml`) and raise a targeted error.

## Surprises

- **chezmoi's `.chezmoiroot` is the closest analog and chose the *opposite* direction**: a marker file at repo root that *redirects* to the actual source dir. This is more flexible (any subpath works without changing slug syntax) but heavier on cognitive load (now there are two source-of-truth files to keep in sync). I considered recommending a `.niwarroot`-style redirect file but rejected it: discovery is a one-time event for niwa (cached in registry), so the flexibility chezmoi needs (multi-machine state) doesn't apply. Stable convention beats redirect indirection here.
- **GitHub Actions's `.github/workflows/` is famously rigid** and the community has been complaining about it for years (issue [actions/runner#2399](https://github.com/actions/runner/issues/2399)). The lesson is: pick a convention that has clear extension points (subpath override) before users start asking for them. Our explicit-override syntax handles this from day 1.
- **Nix's flake registry can't store `dir=`**, which means flake users have to re-type the subpath at every `inputs.foo.url` site. This is the worst of both worlds. We dodge it by storing the *resolved* slug (with subpath) in the registry — the user never re-types it.
- **Cargo's `package.workspace`-key escape hatch** (member crate explicitly names its workspace root) is interesting but solves a different problem: niwa workspaces aren't members of anything. We don't need it.
- **Renovate searches `.github/renovate.json` as a fallback** — a community convention that organizations centralize org-wide config in the `.github` repo. Niwa's `.niwa/` directory inside the brain repo plays the same role within a single repo. We do *not* need a cross-repo `.github`-style fallback because niwa already has the workspace-level personal-overlay clone for that use case.
- **The "single-file convention trap" is real**: `niwa.toml` as a complete one-file config can't carry hooks/, env/, or content/. Resolving `niwa.toml` to "the config dir is the dir containing this file" (rank 4) sidesteps the trap — supporting content lives alongside `niwa.toml` at repo root. The discipline this requires (`content_dir` becomes mandatory) is the cost.

## Open Questions

1. **Should we *require* a `niwa.toml` to coexist with a `[workspace] content_dir` setting?** My proposal says yes (mandatory when discovered at repo root). Alternative: allow `niwa.toml` without `content_dir` and treat the absence as "no Claude content," which is technically valid but probably unintended.
2. **Slug colon vs Renovate's colon**: Renovate uses `:` for *preset name* (a config-internal identifier), not subpath. Reusing the same separator for a semantically different thing (filesystem subpath) could cause confusion for users who know both tools. Acceptable risk?
3. **What about `.git` ignored or worktree edge cases?** When the brain repo is fetched via partial-clone snapshot (the redesign goal), does discovery still see the same root layout? I assume yes — the snapshot includes the repo root regardless of subpath sourcing — but this depends on the lead-snapshot-shape outcome.
4. **Should we expose a `niwa config show-discovery org/repo` command** to let users dry-run discovery without setting up a workspace? Useful for debugging and for migrating teams. Out of scope for this lead but flag for design phase.
5. **Versioning the convention**: If we ever need to evolve the marker names, what's the migration path? Probably "add new marker name with higher precedence, keep old name working with a deprecation warning." Worth pinning down before shipping.

## Summary

Recommend a three-marker root-only discovery (`.niwa/workspace.toml` > root `workspace.toml` > root `niwa.toml`) with hard-error on ambiguity and explicit `owner/repo:subpath@ref` override syntax that bypasses discovery entirely. The main trade-off is rigidity vs flexibility: root-only discovery keeps the model intelligible and matches Renovate's proven pattern, but it commits niwa to a small fixed marker vocabulary that's painful to extend later (GitHub Actions's `.github/workflows/` rigidity is the cautionary tale). The biggest open question is whether `niwa.toml` should require an explicit `content_dir` setting when it lives at brain-repo root, which determines whether the one-file convention is safe or a foot-gun.

Sources:
- [chezmoi `.chezmoiroot`](https://www.chezmoi.io/reference/special-files/chezmoiroot/)
- [chezmoi customize source directory](https://www.chezmoi.io/user-guide/advanced/customize-your-source-directory/)
- [Cargo workspaces](https://doc.rust-lang.org/cargo/reference/workspaces.html)
- [EditorConfig spec](https://spec.editorconfig.org/index.html)
- [Renovate config presets](https://docs.renovatebot.com/config-presets/)
- [Renovate config overview](https://docs.renovatebot.com/config-overview/)
- [Terraform module sources](https://docs.hashicorp.com/terraform/language/modules/sources)
- [GitHub Actions reusable workflows](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows)
- [GitHub Actions runner issue 2399 (path rigidity)](https://github.com/actions/runner/issues/2399)
- [Nix flake CLI reference](https://nix.dev/manual/nix/2.34/command-ref/new-cli/nix3-flake.html)
- [Nix flake registry `dir=` limitation](https://github.com/NixOS/nix/issues/4050)
- [Helm chart repository guide](https://helm.sh/docs/topics/chart_repository/)
- [npm workspaces guide (Bun docs)](https://bun.sh/guides/install/workspaces)
