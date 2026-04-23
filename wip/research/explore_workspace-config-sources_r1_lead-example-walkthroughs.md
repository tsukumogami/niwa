# Lead: How would the two example brain repos actually adopt this?

## Findings

### Current standalone dot-niwa structure (tsukumogami/dot-niwa)

On-disk size: 636K total (including the `.git/` working directory).
Twenty source-controlled files across seven directories.

```
.niwa/
  workspace.toml          # the only top-level config file
  README.md               # repo-level docs (not workspace content)
  LICENSE
  .gitignore
  claude/                 # CLAUDE.md content tree (referenced via content_dir = "claude")
    workspace.md
    public.md
    repos/
      tsuku.md
      koto.md
      tsuku-subdirs/
        recipes.md
        telemetry.md
        website.md
  env/
    workspace.env         # non-secret env vars promoted into spawned shells
  extensions/             # markdown fragments materialized into per-repo .claude/
    design.md
    explore.md
    plan.md
    prd.md
    work-on.md
  hooks/                  # bash scripts wired into Claude Code lifecycle
    pre_tool_use/gate-online.sh
    stop/workflow-continue.sh
```

The `workspace.toml` shape clearly separates four concerns:
1. **Workspace identity & sources** (`[workspace]`, `[[sources]]`, `[groups.*]`).
2. **Claude Code configuration** (`[claude]` — marketplaces, plugins, hooks, settings, env promotion).
3. **Environment & secrets contract** (`[env]`, `[env.secrets.required|recommended|optional]`, per-repo overrides).
4. **File materialization & content tree** (`[files]` mapping plus `[claude.content.*]` referencing relative paths under `content_dir`).

Sorting by category: of the 20 files, only `workspace.toml` is "config-as-data."
Six (`claude/*.md`) are content templates niwa weaves into a CLAUDE.md hierarchy.
Five (`extensions/*.md`) are content templates copied into per-repo `.claude/`
trees. One (`env/workspace.env`) is an environment file. Two are hook scripts.
The remaining three are repo housekeeping (`README.md`, `LICENSE`, `.gitignore`)
that exist *because* this is a standalone repo and would not need to migrate.

The whole payload is text, totalling under a megabyte before `.git/`.

### Brain repo structures (high level)

**tsukumogami/vision** — pure docs/strategy repo.
Top-level: `.gitignore`, `CLAUDE.md`, `README.md`, `archive/`, `docs/`, `org/`, `projects/`.
- `docs/` carries strategic-document subfolders (`designs/`, `prds/`, `roadmaps/`, `competitive/`) plus the occasional top-level design file.
- `org/` carries an org-level overview file plus a `guides/` subdirectory.
- `projects/` is partitioned per project (per-tool subdirectories), each holding its own design/plan/research/roadmap files.
- `archive/` holds historical materials.
- No `.claude/` directory at the root, no `niwa.toml`, no `.niwa/`.

**codespar/codespar-web** — Next.js application repo.
Top-level mixes app code (`src/`, `public/`, `content/`, `package.json`,
`tsconfig.json`, `next.config.ts`, `eslint.config.mjs`, `postcss.config.mjs`,
`source.config.ts`, `package-lock.json`, `.npmrc`), CI metadata (`.github/`),
docs (`docs/`, `CLAUDE.md`, `README.md`), env scaffolding (`.env.example`),
plus two niwa-adjacent dirs already present: `.claude/` (currently containing
only `settings.json`) and `.source/` (a generated config file for the Next.js
content layer — unrelated to niwa despite the name collision).
- `docs/` here mirrors the vision shape (`designs/`, `prds/`, `roadmaps/`,
  `competitive/`) plus several top-level analysis/audit/changelog files.
- `.claude/` is small but already claimed.

### Proposed placements

**Common pattern**: a `.niwa/` directory at the brain repo root, containing
the same payload that today lives at the standalone repo's root (minus
repo housekeeping). `niwa.toml` at root is rejected by both cases — root is
already crowded, and a single TOML file cannot host the content tree, env
file, hooks, and extensions niwa needs to materialize.

**For tsukumogami/vision:**
- Niwa config lands at `vision/.niwa/`.
- Inside it: `workspace.toml`, `claude/` (the content tree), `env/`,
  `extensions/`, `hooks/`. Direct lift from the standalone repo.
- The existing root `CLAUDE.md` in vision describes how *agents working
  inside vision itself* should behave (Claude Code reads it when cwd is
  vision). The `.niwa/claude/workspace.md` content describes how agents
  working *across the whole workspace* should behave (niwa weaves it
  into the workspace root's CLAUDE.md hierarchy). These are different
  audiences and should remain two separate files. Worth documenting
  explicitly so contributors editing either don't think they're
  duplicates.
- No reorganization needed. `vision/docs/`, `vision/org/`, `vision/projects/`
  are unaffected.
- The thematic overlap the user flagged ("dot-niwa carries info about how
  the org's repos come together") is real here: vision's `org/PROJECTS.md`
  and `vision/projects/*/` already describe inter-repo relationships. Once
  `.niwa/` lives inside vision, those two views can cross-link, and the
  workspace.md content can reference (or even include via the content tree)
  parts of vision's existing org docs. This is the reduction in duplication
  the user is hoping for.

**For codespar/codespar-web:**
- Niwa config lands at `codespar-web/.niwa/`.
- The pre-existing root `.claude/settings.json` is for Claude Code when cwd
  is the codespar-web repo itself; the new `.niwa/claude/` content tree is
  for the workspace overlay niwa builds. They do not conflict. They could
  optionally cross-reference (e.g., niwa's repo-level CLAUDE.md fragment
  for codespar-web could describe the existing `.claude/settings.json`
  conventions), but they should not be merged — `.claude/settings.json` has
  to live where Claude Code looks for it.
- The `.source/` directory is unrelated (Next.js content config). Worth
  noting because a developer skimming layout might assume the name implies
  niwa source material.
- No reorganization required. The brain repo continues to serve its primary
  purpose (a Next.js app); niwa config is a tenant in `.niwa/`.

### Migration walkthrough

Starting state: developer has a workspace pointing at `tsukumogami/dot-niwa`
via a registry entry (e.g., `[registry.tsukumogami] source =
"git@github.com:tsukumogami/dot-niwa"`). On disk, `<workspace>/.niwa/` is the
working tree of that repo. The relevant code paths are in
`internal/cli/init.go:177`, `internal/cli/apply.go:213`, and
`internal/workspace/clone.go` (`Cloner`).

Target state: registry entry points at `tsukumogami/vision` with subpath
`.niwa/`, and `<workspace>/.niwa/` is a snapshot of just that subpath.

Sketch of the developer journey:

1. Maintainer prepares the brain repo: `git mv` (or copy) the dot-niwa
   payload into `vision/.niwa/`, drop the standalone repo's `README.md`,
   `LICENSE`, `.gitignore` (or fold useful bits into the brain repo's
   equivalents), commit and push to vision. The standalone dot-niwa repo
   is left in place, optionally archived or marked deprecated.

2. Each developer updates their global niwa config:
   - **Convention-based discovery path**: `niwa config set global
     tsukumogami/vision` — niwa probes the repo for `.niwa/workspace.toml`
     (or `niwa.toml` at root, or `workspace.toml` at root) and records the
     discovered subpath. Lower friction; does what the user expects.
   - **Explicit subpath path**: `niwa config set global
     tsukumogami/vision --subpath .niwa` for cases where discovery would be
     ambiguous (e.g., multiple `niwa.toml` files in the repo).

3. The registry entry's identity changes. Today the registry key is the
   workspace name (e.g., `tsukumogami`). The same key can be reused
   pointing at the new source — but the fields underneath change shape
   (URL changes; new `subpath` field appears). Existing TOML readers in
   `internal/config/registry.go` need to learn the new field; old entries
   with no `subpath` continue to mean "whole repo" (degenerate `subpath = "/"`).

4. On the next `niwa apply`, niwa needs to detect that the source URL
   changed and decide what to do with `<workspace>/.niwa/` on disk:
   - Today it's a working tree. The disk layout for a snapshot-based
     model is the same set of files but without `.git/`. There's a
     non-trivial migration: blow away the existing `.niwa/` and
     re-materialize from the new source. This is destructive of any
     local edits.
   - Safer flow: detect that the URL changed, refuse to proceed, prompt
     the user to either commit/push their pending dot-niwa changes (so
     they're not lost) or pass `--force` to discard. Then re-materialize.
   - Issue #72 is the precedent here — niwa already has a story about
     URL changes being painful; this migration is the same problem
     shape, scaled up.

5. Verification: `niwa status` should show that the workspace's effective
   config now resolves to `tsukumogami/vision[.niwa/]` with snapshot
   commit pinned to whatever vision's HEAD is.

Riskiest moments:
- **Step 4** is the worst. Today `.niwa/` is a working tree; developers
  may have uncommitted edits. Snapshot-mode replacement loses them
  silently unless niwa actively detects and warns.
- **Step 1** is risky for the maintainer who runs the lift: any
  hand-written paths inside dot-niwa that assume "repo root is the
  config root" need to become subpath-relative. The `[files]` map in
  workspace.toml (`"extensions/design.md" = ".claude/shirabe-extensions/"`)
  uses paths relative to the config root, so it survives unchanged. The
  `[claude.content.*] source = "..."` paths are also relative to
  `content_dir`, so they survive. Hook scripts referenced by relative
  path (`hooks/pre_tool_use/gate-online.sh`) are likewise fine. So in
  practice the lift is mechanical, but it has to be tested before
  developers point at it.
- **Step 2 / 3** is risky if the global config is shared across machines
  (e.g., dotfiles repo). Each machine needs to re-resolve.

### Frictions surfaced

- **Repo housekeeping doesn't move.** `README.md`, `LICENSE`, `.gitignore`
  in the standalone dot-niwa exist because it's a repo. Inside vision,
  the brain repo already has those at root. The standalone files don't
  cleanly belong inside `vision/.niwa/`. They either get dropped (acceptable
  loss; the LICENSE was for the standalone repo's source) or merged
  thoughtfully (e.g., the dot-niwa README content becomes a section in
  the brain repo's docs about the workspace).

- **CI configuration may exist on the standalone dot-niwa.** If the
  standalone repo had its own `.github/workflows/` validating the
  workspace.toml or running shellcheck on hooks, that CI either moves
  into the brain repo's existing `.github/` (which has its own concerns
  — for codespar-web, it's running web app builds) or gets abandoned.
  Path filtering in workflows becomes essential to keep niwa-related
  CI from running on every brain-repo PR.

- **Permission asymmetry.** A team member may have read access to
  `tsukumogami/vision` but not to `tsukumogami/dot-niwa`, or vice
  versa. Folding the config into vision means workspace access tracks
  vision access. For codespar-web, anyone consuming the workspace gets
  read access to the entire web app source, which may or may not be
  desired. This argues for the subpath model being more than a
  convenience — it lets the source repo serve broader audiences than
  the workspace consumers, but you cannot scope GitHub permissions to
  a subpath.

- **Brain repo rename / org transfer.** When `tsukumogami/vision` moves
  to a new org, every developer's registry entry breaks the same way
  any GitHub URL change breaks today (see issue #72). The subpath model
  doesn't help here — it makes the URL longer, but the failure mode is
  identical.

- **Snapshot purity.** Today niwa clones the whole repo. If the brain
  repo is a Next.js app with a 200MB `node_modules` history or large
  binary assets, naive whole-repo cloning is wasteful. The subpath
  model implies sparse-checkout or partial-clone, which materially
  changes the implementation in `internal/workspace/clone.go`. Without
  that, "subpath inside a big brain repo" trades one problem for
  another.

- **Discovery ambiguity.** vision is clean — one obvious place to land.
  codespar-web has `.claude/` (Claude Code's, not niwa's), `.source/`
  (Next.js, not niwa's), and a candidate `.niwa/`. Convention-based
  discovery should be unambiguous (only `.niwa/workspace.toml` counts),
  but the naming is going to confuse first-time contributors. Worth
  prominent docs.

- **Niwa reading random files.** Today niwa walks the cloned working
  tree and picks up everything it expects (workspace.toml, content_dir,
  hooks, extensions). With a brain repo, niwa should be strict about
  only reading paths declared in `workspace.toml` or under
  `content_dir`. If `content_dir = "claude"` is interpreted relative to
  the subpath, this is fine; if interpreted relative to the brain repo
  root, niwa starts reading vision's `org/` or codespar-web's `src/`,
  which would be a bug.

- **Two CLAUDE.md in one repo.** vision already has a root `CLAUDE.md`
  for cwd-vision usage. The `.niwa/claude/workspace.md` file is
  thematically similar. New contributors will conflate them. The
  resolution is editorial discipline + docs, not code.

## Implications

- **Convention-based discovery is necessary, not just nice.** Both
  brain repos point to `.niwa/` as the natural placement. Forcing
  developers to specify `--subpath .niwa` on every `niwa config set`
  removes most of the win. Niwa should probe for
  `.niwa/workspace.toml`, then `niwa.toml` at root, then
  `workspace.toml` at root, in that order, and record what it found.

- **Subpath must be first-class in the registry schema, not bolted
  on.** `internal/config/registry.go` defines `RegistryEntry` with a
  `Source` field. Adding `Subpath` (defaulting to empty / "/") and
  threading it through `internal/workspace/clone.go` is the minimal
  schema change. Old entries without `Subpath` mean "whole repo" —
  the degenerate case the design memo calls out.

- **Snapshot semantics are required, not optional.** As long as
  `<workspace>/.niwa/` is a working tree, migration from a standalone
  repo to a subpath is destructive. A snapshot model (no `.git/`,
  pinned to a commit recorded elsewhere) makes the cutover cleaner
  because the developer never thought of `.niwa/` as something to edit
  in place.

- **The brain repo's existing dirs constrain the niwa config dir
  name.** `.niwa/` is uncontested in both example brain repos. A
  competing name like `niwa/` or `workspace/` would collide with
  legitimate top-level dirs in many repos.

- **Partial clone or sparse checkout becomes important once subpaths
  inside large brain repos are common.** Cloning all of codespar-web
  to materialize 600K of niwa config is wasteful. This is a
  scalability concern, not a correctness one, but it shows up
  immediately for anyone whose brain repo is bigger than the snapshot.

## Surprises

- **dot-niwa is tiny.** I expected the standalone repo to be heavyweight
  enough that "fold it into a brain repo" felt awkward. It's 636K
  including `.git/` and 20 files. The case for keeping it separate on
  size grounds is essentially zero.

- **codespar-web already has a `.claude/` and a `.source/`**, both
  unrelated to niwa. The naming conflict surface is wider than I'd
  assumed and argues strongly for `.niwa/` (specific, unclaimed) over
  alternatives like `.config/niwa` or `workspace/`.

- **The CLAUDE.md ambiguity is real.** I'd assumed brain repos
  wouldn't have their own root `CLAUDE.md`. Both do. So adopting the
  subpath model means every brain repo lives with two distinct
  `CLAUDE.md` files (one for in-repo Claude Code, one woven into the
  workspace overlay), and contributors will need to be told which is
  which.

- **The thematic overlap the user flagged is real, especially for
  vision.** vision already has `org/` and `projects/` describing
  inter-repo structure; the standalone dot-niwa's `claude/workspace.md`
  is doing the same thing for a different audience. Folding them into
  one repo enables real consolidation, not just colocation.

- **The standalone repo's `README.md` and `LICENSE` don't survive the
  move cleanly.** This is a small, real friction the migration story
  needs to acknowledge.

## Open Questions

- Should the discovery probe order be configurable, or is hard-coded
  `.niwa/workspace.toml` → `niwa.toml` → `workspace.toml` enough?

- How does niwa want to handle the URL-change cutover safely — the same
  story as issue #72, or a dedicated `niwa registry migrate` command
  that detects "old source was whole-repo, new source is subpath in
  same org" and offers a guided flow?

- Is sparse-checkout / partial-clone in scope for the first cut, or is
  "clone the whole brain repo, copy the subpath into the snapshot dir,
  delete the rest" acceptable for v1?

- For brain repos like codespar-web that may be public-but-licensed
  differently from the niwa subpath, does the subpath-as-snapshot
  model need to carry a per-snapshot LICENSE marker, or is that the
  user's responsibility?

- When the brain repo contains a `.claude/settings.json` of its own
  (codespar-web), should niwa's repo-level overlay for that repo
  document or import any of it, or treat it as fully separate?

- Does the personal overlay model change at all? Today the personal
  overlay is keyed by registry name; if registry names stay stable
  across the migration, overlays should survive untouched, but this
  needs explicit verification.

## Summary

Both example brain repos converge on the same placement (`.niwa/` at the
brain repo root, holding the same ~20-file payload that today lives in a
standalone repo), and the standalone repo is small enough that there is
no size-based reason to keep it separate. The biggest migration risk is
the cutover moment when `<workspace>/.niwa/` flips from a working tree
to a snapshot — uncommitted edits will silently disappear unless niwa
actively detects the URL change and refuses to proceed without a flag.
The biggest open question is whether convention-based discovery
(`.niwa/workspace.toml` probe) plus a `Subpath` field in `RegistryEntry`
is enough, or whether the migration needs a dedicated guided command
(`niwa registry migrate`) to handle the dot-niwa-to-brain-repo cutover
gracefully.
