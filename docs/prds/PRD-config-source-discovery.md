---
status: Draft
problem: |
  niwa today cannot be adopted in a single-repo workspace. When a developer
  runs `niwa init --from owner/repo` against a general-purpose repo that
  carries its workspace config at `.niwa/`, niwa materializes the entire
  repo at `<workspace>/.niwa/` because the subpath-discovery requirement
  (R5 in `PRD-workspace-config-sources.md`) was specified but never
  implemented. The same gap blocks the natural "brain repo" pattern where
  a workspace's strategic content and niwa config coexist in one repo.
goals: |
  Close the R5 implementation gap so that `niwa init --from owner/repo`
  against a general-purpose repo "just works" by probing for marker files
  in a fixed precedence order; keep the overlay slug derivation
  predictable by anchoring it to the source repo name regardless of
  subpath; resolve the three policy questions the upstream PRD left
  open (probe mechanism, migration policy, rank-3 `niwa.toml`
  keep-or-drop); ship a Claude-driven migration skill that walks the
  user through moving from the legacy whole-repo shape to the new
  `.niwa/` shape; and keep both shapes working in this release with a
  one-time deprecation notice on the legacy path so existing
  workspaces never break on upgrade.
upstream: docs/prds/PRD-workspace-config-sources.md
---

# PRD: Config Source Discovery

## Status

Draft

## Problem Statement

The umbrella PRD for workspace config sources
(`docs/prds/PRD-workspace-config-sources.md`, status `Done`) specifies a
v1 model where `niwa init --from owner/repo` against a repo whose
workspace config lives at `<repo>/.niwa/` "just works" — niwa probes the
source for marker files in a fixed precedence order, resolves the
subpath automatically, and materializes only that subpath. Most of that
PRD has shipped: the slug grammar `[host/]owner/repo[:subpath][@ref]`
parses, the subpath-aware fetch path is built, snapshots replace the old
working-tree posture, and the overlay slug derivation already handles
the subpath case.

The discovery step itself — requirement R5 — is the gap. Verified in
the code today:

- `internal/workspace/snapshotwriter.go:440` calls
  `github.ExtractSubpath(body, src.Subpath, staging)` directly. When
  `src.Subpath == ""`, this is the "extract everything" path.
- `internal/github/tar.go:117` short-circuits the subpath filter when
  subpath is empty.
- No code path probes the source for `.niwa/workspace.toml` or
  `workspace.toml` before deciding what to extract.

The user-visible consequence: a developer with a single general-purpose
repo (the entire workspace is one repo) cannot adopt niwa without
either (a) standing up a second `dot-niwa` repo just for config — the
exact friction niwa is supposed to remove — or (b) typing
`--from owner/repo:.niwa` verbatim each time and remembering that
syntax. The "brain repo" pattern — a workspace whose strategic content
and niwa config live in one repo — is blocked for the same reason.

A second concern in the upstream PRD's R35 — that the auto-discovered
overlay slug changes shape based on whether the source has an explicit
subpath (`org/brain:.niwa` → `org/.niwa-overlay`) versus a whole-repo
source (`org/dot-niwa` → `org/dot-niwa-overlay`) — interacts badly
with discovery, because once discovery resolves `org/brain` to subpath
`.niwa`, the overlay slug silently moves to `org/.niwa-overlay`. This
PRD overrides R35 to anchor the overlay slug to the source repo name
in every case.

Three policy questions the upstream PRD left implicit are resolved by
this PRD (see Decisions and Trade-offs below):

1. **Probe mechanism**: single fetch + in-stream scan, not a separate
   API call.
2. **Migration policy**: both formats work in this release; the legacy
   path emits a one-time deprecation notice; migration itself is
   handled by an interactive Claude skill rather than a niwa CLI
   command. Hard removal of the legacy path is deferred to a
   follow-up release.
3. **Rank-3 `niwa.toml` discovery**: removed in this release; only
   rank-1 (`.niwa/workspace.toml`) and rank-2 (root `workspace.toml`)
   remain. Rank-2 is the deprecation target; rank-1 is the future
   default.

## Goals

- **Make single-repo workspaces viable.** `niwa init --from owner/repo`
  against a general-purpose repo that contains `.niwa/workspace.toml`
  resolves automatically — no extra flags, no extra repo to stand up.
- **Make brain-repo composition first-class.** A workspace whose niwa
  config sits at a subdirectory of an existing brain repo is the
  default, well-trodden path; the user never has to think about
  subpath syntax unless they want to override discovery.
- **Keep overlay slug derivation predictable.** The auto-discovered
  overlay slug follows the source **repo name** in every case:
  `--from dangazineu/foo` derives `dangazineu/foo-overlay` whether
  `foo` carries config at root (rank-2) or under `.niwa/` (rank-1),
  and whether the subpath was explicit, discovered, or empty. The
  subpath does not participate in overlay naming. This overrides
  upstream PRD R35's case-split.
- **Both formats keep working in this release.** Existing
  `--from org/dot-niwa` users (rank-2, the legacy whole-repo shape)
  keep applying after upgrade with no registry edits and no `--force`
  flag. A one-time deprecation notice fires per workspace so they
  know there's a future migration ahead, but nothing breaks today.
- **Migration is a Claude skill, not a niwa command.** A shirabe skill
  walks the user through moving a workspace from the legacy
  whole-repo shape to the new `.niwa/` shape — inspecting the
  registry, suggesting the new slug, editing the registry entry, and
  pointing at the next `niwa apply --force`. The skill is the
  documented path; manual edit of the registry file is always
  available as the fallback.
- **Make discovery errors actionable.** When a source contains
  multiple markers at the source root, or none of them, the error
  message names every accepted path and the explicit-subpath escape
  hatch.

## User Stories

### Story 1: Single-repo workspace adoption

A developer has one repo, `dangazineu/foo`, that contains both their
project code and `.niwa/workspace.toml` declaring the workspace
config. They run `niwa init --from dangazineu/foo my-workspace`. niwa
probes the source, finds `.niwa/workspace.toml`, resolves the subpath
to `.niwa/`, fetches only that subpath into the workspace snapshot
at `<my-workspace>/.niwa/`, and registers the workspace.

niwa also attempts to clone the auto-discovered overlay at
`dangazineu/foo-overlay` (repo-name plus `-overlay`); if that repo
doesn't exist or the user lacks access, the overlay silently skips
and the workspace applies against the team config alone (preserving
upstream R35's silent-skip behaviour).

A subsequent `niwa apply` clones `dangazineu/foo` as a workspace
component. The developer's working copy ends up under
`<my-workspace>/dangazineu/foo/`; the snapshot of the config remains
the source of truth.

### Story 2: Brain-repo composition

A team uses `acme/vision` as their brain repo: it holds the project's
strategic documents, planning notes, Claude config, and now also the
workspace's niwa config at `vision/.niwa/`. The workspace declares
three other components (`acme/web`, `acme/api`, `acme/infra`). A
developer runs `niwa init --from acme/vision my-workspace`. Discovery
resolves the subpath to `.niwa/`, the snapshot materializes only the
config files, and `niwa apply` clones all four workspace repos —
including `acme/vision` itself — under the instance root.

niwa auto-discovers the workspace overlay at `acme/vision-overlay`,
NOT `acme/.niwa-overlay`. The overlay slug is derived from the
source repo, not the subpath, so the maintainer's mental model —
"the overlay sits next to its source repo, named after it" — holds
regardless of whether the team config lives at root, under `.niwa/`,
or behind an explicit subpath.

The brain repo flows through `discoverAllRepos` and `Classify` like
any other workspace repo (per the precedent established in PR #138).
The user never has to write a special config entry to say "the brain
repo is both my config source and a workspace component."

### Story 3: Existing standalone `dot-niwa` user upgrades

A developer with an established workspace pointing at
`org/dot-niwa` (whose entire content is the workspace config)
upgrades to a niwa binary that ships this PRD. They take no action.
The next `niwa apply` succeeds: discovery probes the source, finds
root `workspace.toml` (rank-2 marker), resolves the subpath to `""`
(whole-repo), and the materialization path matches what it did
before. The auto-discovered overlay slug remains
`org/dot-niwa-overlay` (unchanged from today). No `--force` flag,
no destroy/re-init ritual.

stderr also contains a one-time deprecation notice telling them the
whole-repo (rank-2) shape is on the way out and pointing at the
migration skill:

```
note: workspace 'my-workspace' is using the deprecated whole-repo
      config source layout (root workspace.toml). Future releases
      will require config under .niwa/workspace.toml. To migrate
      this workspace, run: /shirabe:niwa-migrate-config my-workspace
      in Claude Code.
```

The notice fires once per workspace and is suppressed on subsequent
applies via the existing `DisclosedNotices` mechanism. The
workspace continues to apply normally between now and whenever the
developer chooses to migrate.

### Story 4: Migration via the Claude skill

The developer from Story 3 decides to migrate. They open Claude Code
and run `/shirabe:niwa-migrate-config my-workspace`. The skill:

1. Reads the registry entry for `my-workspace` from
   `~/.config/niwa/config.toml` and shows the current `source_url`
   (e.g., `org/dot-niwa`).
2. Probes the registered source by inspecting its root layout (via
   the same fetch path niwa uses) and reports what it found:
   "Source `org/dot-niwa` has root `workspace.toml` (rank 2, the
   deprecated shape). Two migration paths:
   (a) Move the config in `org/dot-niwa` into `.niwa/` —
       `git mv -k * .niwa/` then commit & push — and keep the same
       registry slug. Discovery will resolve via rank 1 on next apply.
       The overlay slug stays `org/dot-niwa-overlay`.
   (b) Move the config into a different repo that has `.niwa/` at
       root (e.g., a brain repo like `org/vision`), and update the
       registry slug to point at the new repo. The overlay slug
       will change to `org/vision-overlay` (per R10) — the maintainer
       of the new repo must arrange for the overlay repo at the new
       slug before consumers complete the migration."
3. Asks the user which path they want. If (a), the skill tells the
   user the registry edit is not needed (the slug stays the same
   after the repo is restructured) and prints a checklist.
   If (b), the skill edits the registry entry's `source_url` to the
   new slug provided by the user and warns about the overlay slug
   change.
4. Tells the user to run `niwa apply --force <name>` to materialise
   the new snapshot.

The skill is read-mostly: it never pushes to git, never runs apply,
never deletes the on-disk snapshot. It writes only to the registry
file when path (b) is chosen.

### Story 5: Maintainer publishes config from brain repo

A maintainer of `acme/vision` decides to host the workspace config
inside the brain repo. They `git mv` the standalone-`dot-niwa`
contents into `acme/vision/.niwa/`, commit, and push. They also
create `acme/vision-overlay` (the new overlay repo, per R10) by
renaming or forking the existing `acme/dot-niwa-overlay`. They post
a one-line announcement: "the workspace config now lives in the
brain repo — run `/shirabe:niwa-migrate-config <your-workspace-name>`
in Claude Code to switch." Each consumer's switch is independent;
the standalone `dot-niwa` repo keeps working for anyone who hasn't
migrated yet, so there's no synchronized cutover.

### Story 6: Discovery ambiguity diagnostic

A maintainer publishes a brain repo with both
`.niwa/workspace.toml` and a top-level `workspace.toml` (perhaps
left over from a partial migration). A consumer runs
`niwa init --from acme/vision`. niwa probes the source, finds both
markers, and refuses with an error that names both files and points
the maintainer at the fix:

```
error: ambiguous niwa config in acme/vision
  found:   .niwa/workspace.toml
  found:   workspace.toml
  Remove one of the markers, or override discovery with
  --from acme/vision:.niwa (explicit subpath).
```

The consumer reports the error to the maintainer; the maintainer
removes the stale file; the consumer re-runs `niwa init` and
succeeds.

### Story 7: Discovery no-marker diagnostic

A consumer types `niwa init --from acme/random-repo` against a
public repo that doesn't carry niwa config at all. niwa probes,
finds neither `.niwa/workspace.toml` nor a root `workspace.toml`,
and refuses with a diagnostic that names both accepted paths and
the explicit-subpath escape hatch:

```
error: no niwa config found in acme/random-repo
  probed:  .niwa/workspace.toml  (rank 1)
  probed:  workspace.toml        (rank 2, repo root)
  If the config lives elsewhere, point at it explicitly:
    niwa init --from acme/random-repo:path/to/config
```

The user knows immediately whether the repo is meant to be a niwa
source and, if so, where else to look.

### Story 8: Rank-3 user migrates after upgrade

A team had been using a brain repo `acme/legacy` whose niwa config
sat at the repo root as `niwa.toml` (the upstream PRD's rank-3
shape, which this PRD removes). After upgrading to a binary that
ships this PRD, the first `niwa apply` against any workspace
sourced from `acme/legacy` exits with the Story-7 "no niwa config
found" diagnostic — the diagnostic deliberately does not mention
`niwa.toml` because rank-3 is no longer an accepted marker.

The team has two recovery paths, both documented in the
`docs/guides/workspace-config-sources.md` migration section:
(a) `git mv niwa.toml .niwa/workspace.toml` in `acme/legacy`,
commit, push, and re-run `niwa apply`; or (b) keep the file at
root, rename it to `workspace.toml`, commit, push (lands on the
deprecated rank-2 path but still works in this release). Either
path resolves discovery and the next `niwa apply` succeeds. No
upgrade-time pre-flight or proactive warning fires — the error
surfaces on first apply only.

## Requirements

### Functional requirements

**Convention-based subpath discovery**

- **R1.** When the user passes `--from owner/repo` (no explicit
  subpath; i.e., the parsed `Source.Subpath` is empty), `niwa init`
  MUST probe **only the source-root level** for marker files in this
  fixed precedence order: `.niwa/workspace.toml` (rank 1), root
  `workspace.toml` (rank 2). The probe MUST ignore nested occurrences
  of these filenames at deeper paths (e.g., `subdir/.niwa/workspace.toml`
  or `subdir/workspace.toml` are not discovery candidates). The first
  match resolves the subpath:
  - rank 1 match → `Source.Subpath = ".niwa"`
  - rank 2 match → `Source.Subpath = ""` (whole-repo case; preserves
    the existing standalone `dot-niwa` shape)
- **R2.** An explicit subpath in the slug (e.g.,
  `--from owner/repo:custom/path`) MUST bypass discovery entirely.
  niwa MUST NOT probe for rank-1 / rank-2 markers in this case, and
  ambiguity errors (R3) MUST NOT fire. If the explicit subpath does
  not contain a `workspace.toml`, niwa MUST fail without falling
  back to discovery (preserves upstream PRD R9).
- **R3.** When more than one marker is present at the source root
  (both `.niwa/workspace.toml` AND root `workspace.toml` exist),
  niwa MUST fail with an "ambiguous niwa config" diagnostic naming
  the **markers found** and the explicit-subpath escape hatch. niwa
  MUST NOT pick a winner silently.
- **R4.** When discovery finds no marker at the source root and no
  explicit subpath was given, niwa MUST fail with a "no niwa config
  found" diagnostic listing **every accepted marker path that was
  probed** (`.niwa/workspace.toml`, root `workspace.toml`) and the
  explicit-subpath escape hatch.
- **R5.** Discovery failure on any path (multiple markers, no
  marker, network unreachable, truncated tarball) MUST leave the
  on-disk `<workspace>/.niwa/` byte-identical to its pre-init state,
  **or absent entirely if `<workspace>/` did not exist before the
  failed init**. No partial snapshot is materialized. No files
  written by the init are present after a failure.
- **R6.** Empty `.niwa/` directory at the source root (the directory
  exists but contains no `workspace.toml`) MUST NOT count as a
  rank-1 match. Discovery MUST continue to rank 2. When the source
  has an empty `.niwa/` AND a root `workspace.toml`, rank 2 wins;
  no ambiguity error fires.

**Discovery probe mechanism**

- **R7.** Discovery MUST use a single fetch of the source — the
  GitHub tarball (`github.com` host) or a single shallow clone
  (`git clone --depth=1`, all other hosts) — the same fetch the
  snapshot materialization uses. niwa MUST scan the in-memory
  tarball stream (or the resulting shallow-clone working tree's
  top level) for marker files at the two accepted paths, decide the
  rank-1-wins-over-rank-2 precedence, and extract only the resolved
  subpath to the staging area. niwa MUST NOT make a separate "probe"
  API call (e.g., GitHub Contents API) before the fetch.
- **R8.** The marker scan on the GitHub path MUST happen during
  tarball streaming without buffering the full tarball to disk. The
  decompression-bomb cap and security defenses in
  `internal/github/tar.go` MUST apply unchanged to the probe pass;
  a tarball that exceeds the cap during the probe scan MUST fail
  with the existing cap-exceeded diagnostic and leave no snapshot
  materialized.
- **R9.** The marker scan on the non-GitHub path MUST inspect the
  top level of the shallow-clone working tree for the two accepted
  filenames (`workspace.toml` at root; `workspace.toml` inside
  `.niwa/` subdir). The clone is reused as the staging source if
  discovery resolves successfully; the temp clone is removed after
  the snapshot is promoted (per upstream R15).

**Overlay slug derivation**

- **R10.** The auto-discovered workspace overlay slug MUST be
  derived as `<host>/<source-org>/<source-repo>-overlay` in every
  case, regardless of how the source's subpath was resolved
  (explicit slug, discovered, or empty). The subpath MUST NOT
  participate in overlay slug derivation. This overrides upstream
  PRD R35's case-split between whole-repo and subpath sources.
  Worked examples:
  - `--from dangazineu/foo` (rank-1 discovery resolves
    `Subpath = ".niwa"`) → overlay slug = `dangazineu/foo-overlay`.
  - `--from dangazineu/foo:.niwa` (explicit subpath) → overlay
    slug = `dangazineu/foo-overlay`.
  - `--from acme/dot-niwa` (rank-2 legacy whole-repo) → overlay
    slug = `acme/dot-niwa-overlay`. (Unchanged from today.)
  - `--from acme/vision:teams/research` (multi-segment explicit
    subpath) → overlay slug = `acme/vision-overlay`, NOT
    `acme/research-overlay` (which would be upstream R35's output).
- **R11.** Existing behaviour from upstream PRD R35 around overlay
  fetch (same fetch-mechanism selection per host: GitHub tarball
  for `github.com`, git-clone fallback elsewhere) and silent
  skip on failure (so users without overlay access continue to
  apply against the team config alone) MUST remain unchanged.
  Only the slug derivation rule changes per R10.
- **R12.** The overlay snapshot MUST itself be subpath-aware in the
  same way as the team config: discovery (R1) runs against the
  overlay repo's root, accepting `.niwa/workspace-overlay.toml`
  (rank 1) or root `workspace-overlay.toml` (rank 2). The same
  rank-2 deprecation notice (R14) fires for an overlay resolved
  via rank-2, scoped to the workspace's overlay (one notice per
  workspace per command-type). This is a tightening of upstream
  PRD R35's "overlay clone is treated as a whole-repo source"
  decision, deferred to v1.x in the upstream PRD's parenthetical.

**Coexistence and deprecation of the legacy rank-2 shape**

- **R13.** Both rank-1 (`.niwa/workspace.toml`) and rank-2 (root
  `workspace.toml`) MUST resolve successfully in this release.
  Existing registry entries with `source_url = "org/dot-niwa"`
  (no subpath) continue to apply via rank-2 discovery with no user
  action required. The same dual acceptance applies to overlay
  config (rank-1 `.niwa/workspace-overlay.toml` and rank-2 root
  `workspace-overlay.toml`).
- **R14.** When discovery resolves a workspace's team config OR
  overlay via rank 2, niwa MUST emit a one-time `note:`-prefixed
  deprecation notice to stderr on `niwa apply` and `niwa init`.
  The notice MUST:
  - name the workspace by its registered name (apply context) or
    by the source slug (init context);
  - identify which artifact is on the deprecated path (team
    config, overlay, or both) and the rank-2 path using the
    literal substring `deprecated`;
  - point the user at the migration skill using the literal
    substring `/shirabe:niwa-migrate-config`;
  - fire at most once per workspace per artifact per command-type
    via the existing `DisclosedNotices` mechanism (the same
    mechanism upstream R18, R28, R32 already use).
- **R15.** Hard removal of rank-2 discovery is OUT of this release's
  scope. A follow-up release MUST remove rank-2 once all known
  workspaces have migrated; this PRD does not schedule that
  conversation but its Out of Scope section names the future
  removal as deferred work.

**Migration tooling: shirabe skill**

- **R16.** A migration skill MUST ship as part of this work,
  invocable as `/shirabe:niwa-migrate-config <workspace-name>` in
  Claude Code. The exact plugin/repo location for the skill source
  is left to the design phase but MUST satisfy the user-facing
  invocation path above. The skill's behaviour is specified by
  R17-R20.
- **R17.** The skill MUST read the registry entry for the named
  workspace from the user's niwa config (default
  `~/.config/niwa/config.toml`, honouring any niwa override env
  vars the design phase identifies) and present the current
  `source_url` to the user. If the workspace is not registered,
  the skill MUST exit with a clear error naming the workspace.
- **R18.** The skill MUST probe the workspace's current team-config
  source AND its auto-discovered overlay (per R10) via the same
  fetch path niwa uses (R7-R9) — reusing niwa's Go code where
  practical, otherwise making the same shape of request — and
  report which marker(s) it found at each source root. The skill
  MUST NOT call into a separate Contents API endpoint; this PRD's
  single-fetch contract (R7) applies to the skill's probes as
  well.
- **R19.** Based on the probe results, the skill MUST present the
  user with two clearly-labelled migration paths and surface any
  overlay implications:
  - **(a) In-place repo restructure**: the user moves the config
    contents inside the source repo from root into `.niwa/`,
    commits, and pushes. The same move can be applied to the
    overlay repo. The registry slug stays the same; the overlay
    slug stays the same (per R10). The skill MUST present the
    exact `git mv` (or equivalent) commands and tell the user to
    push before continuing.
  - **(b) Slug swap**: the user changes which repo the workspace
    points at (e.g., from a standalone `org/dot-niwa` to a brain
    repo `org/vision`). The skill MUST accept the new slug from
    the user, validate the slug grammar per upstream R1-R3,
    confirm with the user, warn explicitly that the overlay slug
    will change from `<old-repo>-overlay` to `<new-repo>-overlay`
    (per R10) and that the maintainer must arrange the new
    overlay repo before consumers complete migration, and rewrite
    the registry entry's `source_url`.
- **R20.** The skill MUST be read-mostly: it MUST NOT push to git,
  MUST NOT run `niwa apply`, MUST NOT delete or modify the on-disk
  `<workspace>/.niwa/` snapshot, and MUST NOT touch the source
  repo's git history. The only side effect (for path (b)) is the
  registry-file edit. After the skill completes any successful
  migration, it MUST tell the user to run
  `niwa apply --force <workspace-name>` as the next step.

**Backwards compatibility**

- **R21.** No existing user MUST be required to take any action
  after upgrading to a niwa binary that ships this PRD (preserves
  upstream PRD R34). The first `niwa apply` after upgrade triggers
  discovery transparently and succeeds for any source that
  matches a rank-1 or rank-2 marker. The rank-2 deprecation notice
  (R14) is informational only and never blocks apply.
- **R22.** All existing acceptance criteria from
  `docs/prds/PRD-workspace-config-sources.md` related to discovery
  (AC-D1 through AC-D9) and backwards compatibility
  (AC-B1) MUST remain pass after this PRD ships, with three
  exceptions: ACs that reference rank-3 (root `niwa.toml`)
  discovery are superseded by R23 below; AC-D3 and AC-D4 are
  amended to match the diagnostic substring contract specified in
  R24 (a tightening, not a contradiction); upstream AC-O2 and
  AC-O3 (the upstream PRD's overlay slug derivation cases for
  subpath sources) are superseded by R10 — the new behaviour is
  verified by AC-V1 through AC-V4 below.

**Rank-3 (`niwa.toml`) discovery removal**

- **R23.** Rank-3 discovery (root `niwa.toml` with explicit
  `content_dir`) specified by the upstream PRD's R5+R8 MUST be
  removed in this release. Brain repos relying on a root
  `niwa.toml` MUST migrate to either `.niwa/workspace.toml`
  (rank 1) or root `workspace.toml` (rank 2, the deprecated path
  still accepted for now). Existing registries whose source
  resolved only via the removed rank-3 marker MUST surface the
  R4 "no niwa config found" diagnostic on the next `niwa apply`;
  no upgrade-time prompt or pre-flight is added. Migration
  guidance for this case MUST appear in
  `docs/guides/workspace-config-sources.md`.

**Diagnostic clarity**

- **R24.** Every discovery error message (R3 ambiguity, R4 no
  marker, R5 partial-snapshot avoidance) MUST contain three pieces
  of information, each as a separately-grep-able substring:
  - the source slug (the user's `--from` value);
  - the relevant marker path(s) — for R3 ambiguity errors, every
    marker found at the source root; for R4 no-marker errors,
    every accepted marker path that was probed;
  - the explicit-subpath escape hatch syntax (a string containing
    the literal substring `--from` and the literal substring `:`).
  The exact rendering (line breaks, prefixes, ordering) is left to
  the design phase; the visible reference for the target format is
  the diagnostic block in Story 6 (R3) and Story 7 (R4).

**Upstream PRD reconciliation**

- **R25.** When this PRD is Accepted, the upstream PRD
  `docs/prds/PRD-workspace-config-sources.md` MUST be amended to
  reflect that R5+R6+R7+R8 and R33 from that PRD are tracked by
  this PRD as outstanding implementation work, AND that R35's
  overlay slug derivation rule is overridden by this PRD's R10.
  Either: (a) the upstream PRD's status changes to "In Progress"
  with this PRD cited as the tracking artifact in a new amendment
  block, or (b) the upstream PRD adds an amendment block (dated,
  like the existing 2026-04-23 entry) acknowledging both gaps and
  naming this PRD as the closing work. The choice between (a) and
  (b) is left to the maintainer accepting this PRD, but one of
  the two MUST happen at acceptance time.

**Documentation**

- **R26.** `docs/guides/workspace-config-sources.md` MUST be
  updated to include:
  - A section with the exact heading anchor `#single-repo-workspace`
    walking through Story 1 end-to-end, including the on-disk
    layout sketch, the `niwa init --from owner/repo` command
    without explicit subpath, and the overlay slug
    `dangazineu/foo-overlay` derivation.
  - A section with the exact heading anchor `#brain-repo` walking
    through Story 2 end-to-end, including the `discoverAllRepos`
    / `Classify` behaviour for the brain repo as a workspace
    component (cross-referencing the upstream PRD's overlay
    precedent and PR #138), AND the overlay slug
    `acme/vision-overlay` derivation (not `acme/.niwa-overlay`).
  - A section with the exact heading anchor `#overlay-slug-rule`
    explaining R10's unconditional repo-name-based derivation,
    with the four worked examples from R10.
  - A section with the exact heading anchor `#rank-2-deprecation`
    explaining the one-time notice (R14), the two migration paths
    handled by the skill (R19), and the deferred hard-removal
    timeline. The section body MUST contain the literal substrings
    `deprecated`, `/shirabe:niwa-migrate-config`, and `rank 2`
    (or `rank-2`).
  - A section with the exact heading anchor `#rank-3-removal`
    explaining the removed root `niwa.toml` path. The section
    body MUST contain the literal substrings `niwa.toml`,
    `rank 3` (or `rank-3`), and `.niwa/workspace.toml` (the
    primary migration target).

## Acceptance Criteria

Each AC is binary pass/fail. ACs that depend on a fixture name it
explicitly. The upstream PRD's Test Strategy section defines the
`tarballFakeServer`. Fixture A serves `acme/dot-niwa` (root
`workspace.toml` only, the legacy rank-2 shape). Fixture B serves
`acme/vision` (`.niwa/workspace.toml` only, the rank-1 shape).

### AC: Convention-based subpath discovery (verifies R1-R6)

- [ ] **AC-D1**. Given fixture B (`acme/vision` with only
  `.niwa/workspace.toml`), `niwa init <name> --from acme/vision`
  resolves `source_subpath = ".niwa"` in the registry. The on-disk
  `<workspace>/.niwa/` after init contains the files from the
  source's `.niwa/` directory and no files from outside it.
- [ ] **AC-D2**. Given fixture A (`acme/dot-niwa` with only a root
  `workspace.toml`), `niwa init <name> --from acme/dot-niwa`
  resolves `source_subpath = ""` in the registry. The on-disk
  `<workspace>/.niwa/` contains the source's whole tree (minus
  excluded entries per upstream PRD R10). Init succeeds. (R13
  coexistence is verified end-to-end by this AC together with
  AC-N1.)
- [ ] **AC-D3a**. Given an explicit slug
  `--from owner/repo:custom/path` where the source has
  `.niwa/workspace.toml` AND `workspace.toml` at root AND
  `custom/path/workspace.toml`, the resolved subpath is
  `custom/path` (the explicit slug bypasses discovery; no
  ambiguity error fires).
- [ ] **AC-D3b**. Given an explicit slug
  `--from owner/repo:custom/path` where the source has neither
  `.niwa/workspace.toml` nor a root `workspace.toml`, but DOES have
  `custom/path/workspace.toml`, the init succeeds. No discovery
  diagnostic is printed.
- [ ] **AC-D4**. Given an explicit slug
  `--from owner/repo:custom/path` where `custom/path/` exists but
  contains no `workspace.toml`, `niwa init` exits non-zero with
  stderr containing the slug `owner/repo:custom/path` and the
  literal `workspace.toml`. No fallback to discovery. The on-disk
  `<workspace>/.niwa/` is byte-identical to its pre-init state, or
  absent if `<workspace>/` did not exist before the failed init.
- [ ] **AC-D5**. Given a `tarballFakeServer` source containing both
  `.niwa/workspace.toml` AND a root `workspace.toml`,
  `niwa init <name> --from owner/repo` exits non-zero with stderr
  containing the literal `ambiguous niwa config`, both marker paths
  (`.niwa/workspace.toml` and `workspace.toml`), and a substring
  matching the literal `--from` together with the literal `:` (the
  explicit-subpath hint). After the failure, the on-disk
  `<workspace>/.niwa/` is byte-identical to its pre-init state, or
  absent if `<workspace>/` did not exist before the failed init.
  No files written by the init are present.
- [ ] **AC-D6**. Given a `tarballFakeServer` source containing
  neither `.niwa/workspace.toml` nor a root `workspace.toml`,
  `niwa init <name> --from owner/repo` exits non-zero with stderr
  containing the literal `no niwa config found`, both probed paths
  (`.niwa/workspace.toml`, `workspace.toml`), and the
  explicit-subpath hint (substrings `--from` and `:`). The on-disk
  `<workspace>/.niwa/` is byte-identical to its pre-init state, or
  absent if `<workspace>/` did not exist before the failed init.
- [ ] **AC-D7**. Given a `tarballFakeServer` configured to return
  500 on the first tarball request, `niwa init` exits non-zero and
  the on-disk `<workspace>/.niwa/` (if it existed before) is
  byte-identical to its pre-init state.
- [ ] **AC-D8**. Given a `tarballFakeServer` source containing a
  `.niwa/` directory at root WITHOUT a `workspace.toml` inside it
  (e.g., the directory has only a `.gitkeep` file) AND a root
  `workspace.toml`, `niwa init <name> --from owner/repo` resolves
  to rank 2: `source_subpath = ""`, the whole-repo materializes,
  no ambiguity error fires. This verifies that empty `.niwa/` is
  not treated as a rank-1 match (R6).

### AC: Single-call probe mechanism (verifies R7-R9)

- [ ] **AC-P1**. Given fixture B, the server records exactly **one**
  tarball request and **zero** Contents API (`/contents/`)
  requests during init. (Verifies R7: no separate probe call.)
- [ ] **AC-P2**. Given a `tarballFakeServer` source whose tarball
  is 200 MB compressed but whose `.niwa/` directory is 50 KB, the
  init succeeds and the resulting `<workspace>/.niwa/` directory
  contains the 50 KB. After init, `find $TMPDIR -path '*/niwa-*'
  -type f` returns no paths (no leaked scratch files outside the
  niwa-managed staging area and final snapshot).
- [ ] **AC-P3**. Given a `tarballFakeServer` configured with the
  `truncate-after:N` fixture mode that closes the connection
  mid-stream, `niwa init` exits non-zero and no snapshot is
  materialized (R5 / upstream R12).
- [ ] **AC-P4**. Given a `tarballFakeServer` source whose
  decompressed tarball size exceeds the 500 MB cap from
  `internal/github/tar.go`, `niwa init` exits non-zero with the
  existing cap-exceeded diagnostic. No snapshot is materialized.
  This verifies that R8's "security defenses MUST apply unchanged
  to the probe pass" is enforced (the cap fires during streaming,
  before the probe finishes scanning markers).
- [ ] **AC-P5** (non-GitHub). Given a `localGitServer` `file://`
  source with `.niwa/workspace.toml` at the repo root, a single
  `git clone --depth=1` is performed; after init, the temp clone
  directory does not exist (per upstream R15), the resulting
  `<workspace>/.niwa/` contains the files from the source's
  `.niwa/` directory and no files from outside it.

### AC: Overlay slug derivation (verifies R10-R12)

- [ ] **AC-V1** (rank-1 discovery). Given fixture B serving
  `acme/vision` with only `.niwa/workspace.toml`, plus a second
  fixture serving `acme/vision-overlay` (the new auto-discovered
  overlay slug per R10) with `.niwa/workspace-overlay.toml`,
  `niwa init <name> --from acme/vision` succeeds. The provenance
  marker for the overlay snapshot records
  `source_url = "acme/vision-overlay"` (NOT `acme/.niwa-overlay`,
  which would be upstream R35's output).
- [ ] **AC-V2** (explicit subpath). Given fixture B and the
  overlay fixture from AC-V1, `niwa init <name> --from
  acme/vision:.niwa` resolves the same overlay slug
  `acme/vision-overlay`. The overlay snapshot is byte-identical
  in content to AC-V1's overlay snapshot.
- [ ] **AC-V3** (rank-2 legacy). Given fixture A serving
  `acme/dot-niwa` with only a root `workspace.toml`, plus a
  fixture serving `acme/dot-niwa-overlay` with a root
  `workspace-overlay.toml`, `niwa init <name> --from
  acme/dot-niwa` succeeds. The overlay snapshot's provenance
  marker records `source_url = "acme/dot-niwa-overlay"`
  (unchanged from today's behaviour).
- [ ] **AC-V4** (overlay absent silently skips). Given fixture B
  but no overlay fixture (the would-be `acme/vision-overlay` does
  not exist or returns 404), `niwa init <name> --from acme/vision`
  succeeds with no overlay snapshot materialized. stderr does NOT
  contain an error about the missing overlay. (Preserves upstream
  R35's silent-skip-on-failure behaviour per R11.)
- [ ] **AC-V5** (multi-segment explicit subpath). Given a
  `tarballFakeServer` source with content under
  `teams/research/workspace.toml` and the user runs
  `niwa init <name> --from acme/vision:teams/research`, the
  auto-discovered overlay slug is `acme/vision-overlay`, NOT
  `acme/research-overlay`. (Verifies R10's "subpath does not
  participate" contract against upstream R35.)
- [ ] **AC-V6** (overlay rank-1 / rank-2 discovery). Given an
  overlay fixture serving `acme/vision-overlay` with only
  `.niwa/workspace-overlay.toml`, the overlay snapshot resolves
  via overlay-rank-1; subpath in the provenance marker is
  `.niwa`. Given a second run against an overlay fixture serving
  only a root `workspace-overlay.toml`, the snapshot resolves via
  overlay-rank-2 and stderr contains the rank-2 deprecation
  notice (per R14) scoped to the overlay.

### AC: Rank-2 deprecation notice (verifies R13-R15)

- [ ] **AC-N1** (init context, team config). Given fixture A and a
  fresh workspace, `niwa init <name> --from acme/dot-niwa`
  succeeds (exit 0) AND stderr contains a single line with the
  literal substring `deprecated`, the source slug
  `acme/dot-niwa`, and the literal substring
  `/shirabe:niwa-migrate-config`. The notice fires before init
  returns; the workspace is registered normally.
- [ ] **AC-N2** (apply context, first time). Given a workspace
  registered with `source_url = "acme/dot-niwa"` and the first
  `niwa apply <name>` after upgrade, apply succeeds (exit 0) AND
  stderr contains a single line with the literal substrings
  `deprecated`, the workspace name `<name>`, and
  `/shirabe:niwa-migrate-config`.
- [ ] **AC-N3** (apply context, second time). With AC-N2's
  preconditions plus the first apply having already emitted the
  notice, the second `niwa apply <name>` succeeds (exit 0) and
  stderr does NOT contain the literal `deprecated` substring.
  (Verifies one-time-per-workspace via `DisclosedNotices`.)
- [ ] **AC-N4** (rank-1 path silent). Given fixture B and
  `niwa init <name> --from acme/vision`, init succeeds (exit 0)
  and stderr does NOT contain the literal `deprecated` substring.
  Rank-1 is the non-deprecated path; no notice fires.
- [ ] **AC-N5** (both paths apply). Given two registered
  workspaces, one on fixture A (rank 2) and one on fixture B
  (rank 1), `niwa apply --all` applies both successfully (exit 0).
  stderr contains exactly one `deprecated` notice (for the rank-2
  workspace) and no notice for the rank-1 workspace.
- [ ] **AC-N6** (overlay rank-2 notice). Given a workspace whose
  team config is on rank-1 (fixture B) but whose overlay
  (`acme/vision-overlay`) serves only a root
  `workspace-overlay.toml` (rank-2 overlay), the first
  `niwa apply <name>` after upgrade emits a `deprecated` notice
  scoped to the overlay (the notice MUST identify the overlay
  artifact, not the team config).

### AC: Migration skill (verifies R16-R20)

- [ ] **AC-S1** (skill exists with documented invocation). The
  skill is installable / available such that running
  `/shirabe:niwa-migrate-config <workspace-name>` in a Claude
  Code session loads the skill and produces the expected initial
  output (reads the registry, prints the current `source_url`).
  The skill's source location and packaging are design-phase
  decisions; the AC verifies the user-facing invocation.
- [ ] **AC-S2** (unregistered workspace). The skill invoked with
  a workspace name that is not present in the registry produces
  a clear error naming the workspace and exits without modifying
  any file on disk.
- [ ] **AC-S3** (rank-2 source detected, in-place path). Given a
  workspace registered with `source_url = "acme/dot-niwa"` whose
  source (fixture A) has only a root `workspace.toml`, the skill
  reports rank-2 detection, offers paths (a) and (b), and when
  the user picks (a) (in-place restructure), prints the exact
  `git mv` commands and the next-step pointer (literal substring
  `niwa apply --force`) without modifying the registry. The
  skill output also notes that the overlay slug
  (`acme/dot-niwa-overlay`) is unchanged on path (a).
- [ ] **AC-S4** (rank-2 source detected, slug-swap path). Given
  the same preconditions as AC-S3 but the user picks (b) and
  provides a new slug `acme/vision`, the skill validates the
  slug grammatically, confirms with the user, prints an
  overlay-change warning containing the literal substrings
  `acme/dot-niwa-overlay` (old) and `acme/vision-overlay` (new),
  then rewrites the registry entry's `source_url` to
  `acme/vision`. After the skill returns, the registry file's
  `source_url` for the workspace is `acme/vision`; all other
  registry fields are byte-identical to their pre-skill state.
- [ ] **AC-S5** (rank-1 source detected, no migration needed).
  Given a workspace registered with
  `source_url = "acme/vision"` whose source (fixture B) has
  `.niwa/workspace.toml`, the skill reports rank-1 detection
  and tells the user no migration is needed (the workspace is
  already on the new path). The registry is byte-identical
  after the skill returns.
- [ ] **AC-S6** (skill is read-mostly). Across all AC-S2 through
  AC-S5 runs, the skill MUST NOT have invoked `git push`, MUST
  NOT have invoked `niwa apply`, MUST NOT have created or
  removed any file under `<workspace>/.niwa/`, and MUST NOT
  have written anywhere other than the registry file (and only
  in AC-S4's case). Verified via test instrumentation of the
  skill's tool calls or via filesystem before/after diffs.
- [ ] **AC-S7** (malformed slug in path (b)). Given AC-S4's
  preconditions but the user provides a malformed slug like
  `acme/repo:` (empty subpath after colon), the skill rejects
  with a clear error and does NOT modify the registry.

### AC: Backwards compatibility (verifies R21-R22)

- [ ] **AC-B1a** (legacy working tree, no URL change). Given a
  registry entry from an older binary with
  `source_url = "org/dot-niwa"` AND an on-disk
  `<workspace>/.niwa/.git/` directory (the legacy working-tree
  shape), the first `niwa apply` on a binary that ships this
  PRD succeeds with no user prompts. After apply,
  `<workspace>/.niwa/.git/` does not exist, the provenance marker
  file `<workspace>/.niwa/.niwa-snapshot.toml` (or the marker
  filename selected at design time) exists with
  `fetch_mechanism = github-tarball`, and the registry mirror
  fields are populated per upstream R23. stderr contains the
  rank-2 deprecation notice per AC-N2 in addition to the
  one-time working-tree-conversion notice per upstream R28.
- [ ] **AC-B1b** (registry-only upgrade). Given a registry entry
  from an older binary with `source_url = "org/dot-niwa"` AND an
  on-disk `<workspace>/.niwa/` that is already a snapshot (no
  `.git/`, provenance marker present), the first `niwa apply`
  succeeds without any one-time conversion notice. The registry
  mirror fields are populated per upstream R23 on first save.
  stderr still contains the rank-2 deprecation notice per AC-N2.
- [ ] **AC-B2**. Given the AC-B1a preconditions plus an additional
  workspace registered with the new slug shape
  `source_url = "acme/vision:.niwa"`, both workspaces apply
  successfully in the same `niwa apply --all` invocation. Neither
  triggers a prompt or `--force` requirement; only the rank-2
  workspace produces a deprecation notice.
- [ ] **AC-B3** (overlay slug continuity for legacy). Given a
  pre-PRD workspace whose overlay was at `org/dot-niwa-overlay`
  (the upstream R35 whole-repo case), the first `niwa apply`
  after upgrade continues to derive `org/dot-niwa-overlay` per
  R10. No re-fetch of a different overlay slug is attempted.

### AC: Rank-3 removal (verifies R23)

- [ ] **AC-R1**. Given a `tarballFakeServer` source containing
  only a root `niwa.toml` (no `.niwa/workspace.toml`, no root
  `workspace.toml`), `niwa init <name> --from owner/repo` exits
  non-zero with the R4 "no niwa config found" diagnostic. Stderr
  MUST NOT contain the substring `niwa.toml` (the removed marker
  is deliberately not advertised as an accepted path).
- [ ] **AC-R2**. The migration guidance in
  `docs/guides/workspace-config-sources.md` contains a section
  with the exact heading anchor `#rank-3-removal`. The section
  body contains all three literal substrings: `niwa.toml`,
  `rank 3` (or `rank-3`), and `.niwa/workspace.toml`.
- [ ] **AC-R3**. Given the AC-R1 setup with an existing registry
  whose source resolved only via the (now removed) rank-3
  marker, `niwa apply <name>` exits non-zero with the R4
  diagnostic. No upgrade-time pre-flight or proactive warning
  fires before the apply (verified by asserting that the first
  command after upgrade — `niwa apply` — produces the error;
  no earlier command emits a rank-3-removal warning).

### AC: Diagnostic clarity (verifies R24)

- [ ] **AC-X1**. The R3 ambiguity error message (verified
  end-to-end by AC-D5) MUST contain three independently
  grep-able substrings: the source slug (`acme/vision` for the
  test fixture), the literal `.niwa/workspace.toml`, and the
  literal `workspace.toml`. The substrings may appear on the
  same or different lines. The exact rendering is not asserted
  beyond Story 6's diagnostic block as a visible reference.
- [ ] **AC-X2**. The R4 no-marker error message (verified
  end-to-end by AC-D6) MUST contain four independently
  grep-able substrings: the source slug, the literal
  `.niwa/workspace.toml`, the literal `workspace.toml`, and a
  string containing both the literal `--from` and the literal
  `:` (the explicit-subpath hint). The substrings may appear
  on the same or different lines.

### AC: Upstream PRD reconciliation (verifies R25)

- [ ] **AC-U1**. After this PRD is Accepted,
  `docs/prds/PRD-workspace-config-sources.md` either (a) carries
  a `status` frontmatter field with the value `In Progress`
  rather than `Done`, with a body Status section matching; OR
  (b) contains an amendment block under the existing
  `## Amendments` section, dated with the acceptance date,
  containing the literal substring `PRD-config-source-discovery`
  (this PRD's filename). One of the two MUST be true. The chosen
  artifact MUST also name R35 as overridden by this PRD's R10.

### AC: Documentation (verifies R26)

- [ ] **AC-G1**. `docs/guides/workspace-config-sources.md`
  contains a heading with the exact anchor
  `#single-repo-workspace`. The section body contains the
  literal substring `niwa init --from owner/repo` (without
  explicit subpath), a fenced code block showing an on-disk
  layout that includes `.niwa/` and at least one workspace
  component subdirectory, and the literal substring
  `dangazineu/foo-overlay` (the worked-example overlay slug).
- [ ] **AC-G2**. The same guide contains a heading with the
  exact anchor `#brain-repo`. The section body contains the
  literal substrings `discoverAllRepos`, `Classify`, a reference
  to PR #138 (literal substring `#138`), and the literal
  substring `acme/vision-overlay` (the worked-example overlay
  slug — explicitly NOT `acme/.niwa-overlay`).
- [ ] **AC-G3**. The same guide contains a heading with the
  exact anchor `#overlay-slug-rule`. The section body contains
  the literal substring `repo-name`, and at least three of the
  four worked examples from R10 (each as a fenced or inline
  code block).
- [ ] **AC-G4**. The same guide contains a heading with the
  exact anchor `#rank-2-deprecation`. The section body contains
  the literal substrings `deprecated`,
  `/shirabe:niwa-migrate-config`, and `rank 2` (or `rank-2`).

## Out of Scope

The following are excluded from this release's scope.

- **Hard removal of rank-2 (`org/dot-niwa` whole-repo) discovery.**
  Deferred to a follow-up release once all workspaces have
  migrated. The user's stated intent is to migrate manually using
  the skill; the follow-up release will set the cutoff once that
  migration completes.
- **A niwa CLI command for migration.** Replaced by the shirabe
  skill (R16-R20). Users who want non-interactive automation can
  still edit the registry file directly; the skill is the
  Claude-driven walkthrough for the common case.
- **Re-specification of subpath fetch mechanics.** The upstream PRD
  R14 (GitHub tarball + selective extraction) and R15 (git-clone
  fallback) are inputs to this PRD, not outputs. This PRD does not
  modify the fetch pipeline; it adds a marker-scan pass during
  streaming.
- **Sparse-checkout for the non-GitHub fallback path.** Identified
  as a future optimization for very large monorepos; the existing
  shallow clone is sufficient.
- **Per-host adapters (GitLab, Bitbucket, GitHub Enterprise Server,
  Gitea).** The host-agnostic fallback path covers them on day one.
- **Schema changes to `workspace.toml` or other config files.** This
  PRD only changes *where* niwa looks for the file, not what it
  contains.
- **Recipe schema or action-system changes in `tsuku`.**
- **Vault provider, telemetry, or session lifecycle changes.**
- **`niwa.toml` rank-3 discovery.** Removed per R23. A future PRD
  could re-introduce a single-file root manifest if a concrete
  user need surfaces, but this PRD does not specify one.
- **A `--strict-refresh` flag for CI operators.** Mentioned in the
  upstream PRD (Story 6) as a deferred follow-up; remains deferred.
- **Multi-team brain-repo overlay separation.** Upstream PRD R35
  envisioned a brain repo hosting multiple team configs
  (`teams/research`, `teams/platform`) each with its own
  access-restricted overlay, by deriving overlay slugs from the
  subpath's last segment. R10 overrides that with a single
  per-repo overlay rule. A team that needs per-subdirectory
  overlay isolation can fall back to an explicit `--overlay
  <slug>` flag (already provided by upstream R35) on a per-init
  basis; this PRD does not add new auto-discovery for the
  multi-team-per-repo case.

## Open Questions

None. The three policy questions the upstream PRD left implicit
(probe mechanism, migration policy, rank-3 keep/drop) are resolved
in the Decisions and Trade-offs section below, alongside the
overlay-slug-derivation override. The choice of upstream-PRD
reconciliation style (status change vs amendment block) per R25 is
left to the maintainer accepting this PRD but is not an open
*requirements* question.

## Known Limitations

- **Slug repo-root sentinel preserved.** Per the upstream PRD's
  Known Limitations: when discovery is ambiguous (both rank-1 and
  rank-2 markers present), there is no consumer-side way to say
  "use rank-2 explicitly" via the slug — `owner/repo:` is rejected
  as an empty subpath (upstream R3a). The only resolution is for
  the source maintainer to remove one marker. This PRD inherits
  the limitation; users hitting it can use the explicit-subpath
  escape hatch (`owner/repo:.niwa` for rank-1) but cannot ask for
  "rank 2" by syntax.
- **First-fetch bandwidth on the GitHub path (inherited).** The
  tarball endpoint delivers the entire repo's gzipped bytes — the
  cost is linear in the source repo's size, not in the subpath's
  size. For brain repos that are large or carry binary assets,
  the first fetch may take noticeably longer than the resulting
  snapshot suggests. Subsequent applies are cheap (40-byte SHA
  endpoint or 304 ETag).
- **Probe-pass scan cost.** R7-R9 require scanning the tarball
  stream (or the shallow-clone working tree) for marker files
  before deciding what to extract. The scan is bounded by the
  existing 500 MB cap from `internal/github/tar.go`. For brain
  repos that approach the cap, the probe pass adds the same
  bounded cost as a full extraction.
- **Rank-2 path is still live.** Users running this release on a
  rank-2 source see a deprecation notice but apply still succeeds.
  Anyone reading the codebase will see two parallel discovery
  branches and may wonder why both exist. The Decisions section
  and the `#rank-2-deprecation` guide section both explain that
  rank-2 is scheduled for removal in a follow-up release once
  migration completes.
- **`niwa.toml` rank-3 removal is a behaviour change.** Any
  existing user relying on a root `niwa.toml` discovery will see
  the R4 "no niwa config found" diagnostic on first apply after
  upgrade. The diagnostic's "no rank-3 marker mentioned" omission
  is intentional (R24) and is a small breaking change; mitigation
  is documentation (AC-R2) plus the explicit-subpath escape hatch
  (which still works for any path the user knows about).
- **Migration skill requires Claude Code.** Users without access
  to Claude Code cannot run `/shirabe:niwa-migrate-config`. The
  fallback for those users is manual registry edit + manual
  source-repo restructure; the guide section
  `#rank-2-deprecation` documents the manual steps the skill
  performs so users can follow them by hand.
- **Overlay slug changes during slug-swap migration.** Migrating
  a workspace from `org/dot-niwa` to `org/brain` (slug-swap path)
  changes the auto-discovered overlay slug from
  `org/dot-niwa-overlay` to `org/brain-overlay` per R10. This is
  more predictable than upstream R35's behaviour (which would
  have produced `org/.niwa-overlay` after discovery) but the
  maintainer of the new repo MUST still arrange for an overlay
  repo at the new slug before consumers complete migration,
  otherwise the overlay clone silently skips and consumers lose
  the augmentation. The migration skill (R19 path (b)) MUST
  warn about this. For the in-place restructure path (R19 (a))
  the overlay slug does NOT change; this is the gentler
  migration shape.

## Decisions and Trade-offs

### Decision: single-call probe mechanism (R7-R9)

**Decided**: discovery scans the tarball stream during the single
fetch the materialization already performs (GitHub path), or the
shallow-clone working tree's top level (non-GitHub path).
**Alternatives**: (a) make a separate GitHub Contents API call
to inspect the source for marker files before deciding what to
extract; (b) two-pass — fetch a partial tarball just for the root
layout, then re-fetch the subpath. **Reasoning**: the existing
GitHub fetch path already streams the tarball through
`ExtractSubpath` with bounded memory and strong security defenses;
adding a scan pass during the stream costs no extra round-trip and
reuses the security-audited code path. A separate Contents API
call would add latency on the cold path and split the auth surface
(Contents API has its own rate limits and PAT scope requirements).
The bandwidth cost of fetching the whole tarball is the same
either way and is already documented as a Known Limitation in the
upstream PRD.

### Decision: overlay slug is derived from repo name only (R10)

**Decided**: the auto-discovered overlay slug is always
`<host>/<source-org>/<source-repo>-overlay`. The subpath is
ignored. **Alternatives**: (a) upstream PRD R35's case-split,
where whole-repo sources derive from the repo name and subpath
sources derive from the subpath's last segment — so
`org/brain:.niwa` would auto-discover `org/.niwa-overlay`;
(b) make overlay slug fully user-controlled and never
auto-derive. **Reasoning**: R35's case-split makes overlay
naming dependent on a *discovery* decision (the subpath) rather
than a *user* decision (the repo). Once discovery is implemented
(this PRD's R5 closure), the same `--from acme/vision` produces
different overlay slugs depending on whether the source has
`.niwa/` or root-level config — invisible to the user, brittle
to refactor against. Anchoring overlay derivation to the repo
name makes it predictable: the overlay sits next to its source
repo, named after it, full stop. The trade-off is the
multi-team-in-one-brain-repo case (R35's worked example,
`org/brain:teams/research` → `org/research-overlay`) loses its
auto-discovery; teams who want that pattern fall back to
explicit `--overlay <slug>`. That case is hypothetical at the
moment; the predictability win for the common case is
concrete.

### Decision: migration is a shirabe skill, not a niwa command

**Decided**: a Claude Code-invocable shirabe skill
(`/shirabe:niwa-migrate-config <workspace>`) handles the migration
walkthrough. niwa itself ships no migration CLI command.
**Alternatives**: (a) a `niwa migrate-source` CLI command;
(b) no tooling, registry edited by hand. **Reasoning**: the user
running this PRD is the only known niwa user; a CLI command is
over-engineered for a one-shot manual migration. A skill captures
the procedure as executable documentation that can probe the
source, suggest the right slug, edit the registry, and warn about
overlay-slug consequences (R19), while keeping the logic out of
niwa's binary surface. Manual registry edit remains available for
users without Claude Code (a Known Limitation rather than a
blocker).

### Decision: coexistence + deprecation notice (R13, R14)

**Decided**: both rank-1 and rank-2 discovery resolve in this
release. Rank-2 fires a one-time deprecation notice per workspace
per artifact per command-type via `DisclosedNotices`. Hard removal
of rank-2 is deferred to a follow-up release. **Alternatives**:
(a) hard-remove rank-2 now and require migration before upgrade;
(b) ship rank-2 support silently with no deprecation signal.
**Reasoning**: hard removal would break the user's existing
workspaces on upgrade, violating the upstream PRD's R34 ("no user
must be required to take action after upgrading"). Silent
coexistence would hide the fact that the path is going away, and
the user would have no clear signal that the migration skill
applies to them. The middle ground — both work, with a clear
one-time notice — preserves backwards compatibility, gives the
user agency over migration timing, and creates a forcing function
for the follow-up removal.

### Decision: drop rank-3 (`niwa.toml`) discovery (R23)

**Decided**: rank-3 discovery from the upstream PRD R5 is removed
in this release. **Alternatives**: keep rank-3 plus the upstream
R8 explicit-`content_dir` requirement. **Reasoning**: rank-3 was
designed for the case where a brain repo wants to host its workspace
config at the repo root via `niwa.toml` instead of a subdir. No
observed user is on this path today, and the additional
`content_dir` requirement makes the error matrix substantially more
complex (R8 in the upstream PRD specifies a different diagnostic when
discovery resolves via rank-3 vs ranks 1-2). Dropping rank-3
narrows the discovery surface to two ranks, simplifies the error
matrix, and aligns the convention with established ecosystem
practice (`.github/`, `.vscode/`, `.editorconfig` — all subdir
patterns).

### Decision: explicit-subpath slug bypasses discovery (R2, AC-D3a)

**Decided**: an explicit subpath in the slug
(`--from owner/repo:custom/path`) bypasses discovery entirely;
ambiguity errors do not fire. **Alternatives**: discovery always
runs and the explicit subpath is treated as a "prefer this rank"
hint. **Reasoning**: the explicit-subpath syntax is the user
asserting "I know what I want." Treating it as a hint adds
confusion when a brain repo has both an explicit
`custom/path/workspace.toml` and a `.niwa/workspace.toml`. The
user typed the subpath; honour it. This preserves the upstream
PRD R9 contract.

### Decision: discovery errors keep the on-disk snapshot untouched (R5)

**Decided**: any discovery failure (ambiguity, no marker, network
error, truncated tarball) leaves the existing on-disk
`<workspace>/.niwa/` byte-identical to its pre-init state, or
absent entirely if `<workspace>/` did not exist before. No partial
state is visible. **Alternatives**: partial materialization at the
staging path is visible for debugging. **Reasoning**: the upstream
PRD's snapshot posture (R12 atomic swap, R37 no-side-effect-files)
commits to "no partial state visible." This PRD preserves that
contract. Debugging is supported via verbose flags printing what
was probed and what was found; the on-disk state itself stays
clean.

### Decision: empty `.niwa/` is not a rank-1 match (R6)

**Decided**: a `.niwa/` directory at the source root that does
NOT contain `workspace.toml` MUST NOT trigger rank-1 matching;
discovery continues to rank 2 (or fails per R4 if rank 2 is also
absent). **Alternatives**: any presence of `.niwa/` (directory or
file) commits the source to rank 1 and fails with a "marker
incomplete" error if `workspace.toml` is missing inside.
**Reasoning**: a `.niwa/` directory with only ancillary files
(README, `.gitkeep`, etc.) is a plausible incremental setup state
during migration. Falling through to rank 2 makes the migration
forward-compatible: a maintainer can land an empty `.niwa/`
directory in PR #1, then add `.niwa/workspace.toml` in PR #2,
without breaking discovery for anyone using the source between
those two PRs.

### Decision: overlay is also subpath-aware (R12)

**Decided**: the auto-discovered overlay snapshot uses the same
rank-1 / rank-2 discovery as the team config, accepting
`.niwa/workspace-overlay.toml` (rank 1) or root
`workspace-overlay.toml` (rank 2). **Alternatives**: keep the
upstream R35 decision that "the overlay clone is treated as a
whole-repo source," deferring subpath-aware overlays to a future
release. **Reasoning**: if the team-config layout is moving to
`.niwa/workspace.toml`, the overlay should follow the same
convention so users have one mental model, not two. The
maintainer of `acme/vision-overlay` who restructures
`workspace-overlay.toml` into `.niwa/workspace-overlay.toml`
shouldn't have to wait for a separate release to land the
overlay-side discovery. Cost: a second rank-2 deprecation path
(scoped to the overlay) and AC-V6 to verify it. Benefit:
symmetry across the two artifacts.

### Decision: skill is read-mostly (R20)

**Decided**: the migration skill only writes to the registry file
(and only in path (b), the slug-swap case). It never pushes git,
never runs apply, never modifies the on-disk snapshot. The user
runs `git push` and `niwa apply --force` themselves.
**Alternatives**: a "full migration" skill that restructures the
source repo via `git mv` + commit + push, then runs
`niwa apply --force` in one shot. **Reasoning**: the source repo
is the user's primary artifact; the skill running `git push`
against it without the user's hands on the wheel is the wrong
level of automation for an irreversible operation. Separating
"prepare the registry" from "commit and push" from "materialize
the new snapshot" gives the user three discrete checkpoints to
inspect state and abort if something looks wrong.

## Next Steps

After this PRD is Accepted:

- `/design` against this PRD to produce the technical design for
  the discovery code path, the overlay-derivation override, and
  the shirabe migration skill.
- Apply the upstream PRD reconciliation chosen per R25 / AC-U1.
- Schedule a follow-up release that hard-removes rank-2 discovery
  once the user confirms all their workspaces have migrated. That
  follow-up is out of this PRD's scope (Out of Scope) but should
  be the natural next milestone after this one ships.
