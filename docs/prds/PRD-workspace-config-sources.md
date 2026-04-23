---
status: In Progress
problem: |
  niwa today materializes git-hosted workspace configuration as a working
  tree at `<workspace>/.niwa/`, synced with `git pull --ff-only`, and
  assumes the whole repo at the URL is the config. The model wedges
  unrecoverably when the remote rewrites history (issue #72), forces a
  separate `dot-niwa` repo even when the natural home is a subdirectory of
  an existing brain repo, and silently invites the working-tree edits the
  model doesn't expect.
goals: |
  Establish the v1 contract for a unified subpath-aware, snapshot-based
  config-sourcing pattern. niwa fetches only the bytes the workspace needs,
  materializes them as a disposable file tree (no `.git/`), discovers the
  source location by convention where a slug is ambiguous, and applies the
  same model symmetrically to the team config and personal overlay. Issue
  #72 stops being possible. Existing standalone `org/dot-niwa` workflows
  continue to work without manual migration.
source_issue: 72
---

# PRD: Workspace Config Sources

## Status

In Progress

## Amendments

### 2026-04-23 — Instance state stays in `.niwa/`

What changed: the original PRD's Out-of-Scope language deferred
`instance.json` placement to design. Design Decision 2 chose to relocate
state to a sibling `<workspace>/.niwa-state/` directory; that choice is
reversed. State stays at `<workspace>/.niwa/instance.json` and is carried
through the snapshot refresh by an assembly step that copies the file
into staging immediately before the atomic swap.

Why: the relocation was implementation-driven — its only purpose was
to keep state safe from being clobbered by the wholesale-replace
snapshot swap. The simpler solution (copy state into staging before
swap) achieves the same safety without splitting the user-visible
layout into two hidden directories. PRs implementing Decision 2's
relocation never landed; the simpler approach is what `preserveInstanceState`
in PR #73 already does.

Out of Scope addition: state-file relocation (no longer planned).

### Future direction (needs-design, issue #74)

Issue #74 (`needs-design`) captures the longer-term improvement:
move from today's "pull the entire resolved subpath wholesale" fetch
to a convention-aware "pull only files niwa knows about" model. That
work is deferred because (a) the relevant conventions aren't currently
codified in one place, and (b) the migration story for workspaces that
rely on wholesale-pull semantics needs design attention. v1 ships
wholesale-pull; the new model is post-v1.

## Problem Statement

niwa's workspace config-sourcing model has three compounding problems that
hit users from different directions:

1. **Unrecoverable wedges on remote rewrite (issue #72)**. The current
   `git pull --ff-only` against `<workspace>/.niwa/` fails with `fatal:
   Not possible to fast-forward, aborting` when the remote is reset or
   force-pushed. Apply is then blocked until the user manually reconciles
   git history in a directory they aren't supposed to edit. The same
   failure shape recurs in the personal overlay clone and the workspace
   overlay clone, since all three use the same code path.

2. **No subpath sourcing forces a `dot-niwa` repo per workspace**. Many
   orgs already maintain a "brain" repo that carries `CLAUDE.md`,
   planning docs, and Claude config alongside the org's primary content
   (whether that's documentation, a flagship application, or a
   monorepo). The workspace config naturally belongs as a subdirectory
   of the brain repo — but niwa today demands a whole separate repo at
   the slug URL. Maintainers either duplicate brain-repo content into a
   standalone `dot-niwa` (creating drift) or skip niwa entirely.

3. **The working-tree posture invites silent data loss**. Because
   `<workspace>/.niwa/` is a real git working tree, users edit files in
   it and commit, expecting their changes to survive. They don't: the
   next `apply` either fast-forwards over the drift (silent loss) or
   wedges (case 1). The directory's appearance contradicts the model.

The three problems have one shared root cause: niwa treats the materialized
config as a working tree to be merged with upstream, rather than as a
disposable snapshot to be replaced from upstream. Fixing the underlying
posture lets niwa simultaneously fix #72, support brain-repo subpaths,
and remove the silent-edit-loss footgun.

## Goals

- **Fix issue #72.** Remote rewrites must never wedge `niwa apply`. The
  failure mode `fatal: Not possible to fast-forward, aborting` must
  disappear from the supported surface.
- **Make brain-repo subpath sourcing first-class.** A workspace config
  can live in any subdirectory of any git repo; niwa fetches only the
  bytes that subpath contains, never the rest of the brain repo.
- **Preserve the existing standalone `dot-niwa` workflow.** Existing
  registries pointing at whole-repo sources continue to apply with no
  user action after upgrading. Their on-disk working trees lazy-convert
  to snapshots transparently.
- **Make convention-based discovery the default ergonomic path.** Users
  who type `niwa init --from owner/brain-repo` get the right thing
  without having to know or type the subpath, when the brain repo
  follows one of three documented conventions at its root.
- **Replace the working-tree posture with a snapshot posture
  symmetrically across all three clone sites** (team config, personal
  overlay, workspace overlay). The same correctness guarantees apply to
  each.
- **Keep the door open for cross-host adoption** without committing v1
  to per-host adapter work. The fallback path covers all git-reachable
  hosts on day one; per-host fast paths land as follow-ups when demand
  emerges.

## User Stories

### Story 1: First-time subpath adoption

A developer at an org with a brain repo (referred to here as
`org/brain-repo`) creates a new workspace. They run
`niwa init --from org/brain-repo:.niwa my-workspace`. niwa parses
the slug, fetches the `.niwa/` subpath of the brain repo's default
branch as a snapshot, materializes it at `<cwd>/my-workspace/.niwa/`
(a pure file tree, no `.git/`), and registers the workspace. Subsequent
`niwa apply` runs refresh the snapshot atomically. The developer never
thinks to edit `.niwa/` in place because there's no `.git/` to suggest
they could.

### Story 2: Migrating from standalone `dot-niwa`

A developer has an existing workspace pointing at a standalone
`org/dot-niwa` repo. The maintainer announces the config has moved
into the brain repo at `org/brain-repo:.niwa`. The developer updates
the registered source via either path:

- CLI: `niwa config set global org/brain-repo`
- Manual: edit `~/.config/niwa/config.toml` and change the
  `[registry.<name>] source_url` field

Both paths leave the existing on-disk `<workspace>/.niwa/` working
tree untouched. The developer runs `niwa apply`. niwa detects the
source URL changed, refuses to proceed, and prints:

```
error: workspace config source changed
  was:  org/dot-niwa
  now:  org/brain-repo (subpath: .niwa, discovered)
  The current .niwa/ on disk is a working tree from the old source.
  Replacing it will discard any uncommitted edits.
To proceed:
  1. cd .niwa && git status   # check for uncommitted work
  2. niwa apply --force        # discard .niwa/ and re-materialize
```

After `niwa apply --force`, niwa validates that the new source's
`[workspace].name` matches the registered name (otherwise refusing
without `--rename`), then atomically replaces the snapshot. From this
point forward, edits to `.niwa/` are never silently lost because
there's no working tree to commit into.

### Story 3: Brain-repo maintainer publishing

A maintainer of a brain repo decides to host the workspace config
inside it. They `git mv` the standalone-`dot-niwa` contents into
`<brain-repo>/.niwa/`, drop the standalone repo's housekeeping files,
commit, and push. They post a one-line announcement: "the workspace
config now lives at `org/brain-repo:.niwa` — run
`niwa config set global org/brain-repo` to switch." Each consumer's
switch is independent; the standalone `dot-niwa` repo can stay in
place indefinitely for graceful overlap. No synchronized cutover.

### Story 4: Apply after brain-repo force-push

A developer's workspace points at `org/brain-repo:.niwa`. The
brain-repo maintainer force-pushes the default branch to clean up
history. The developer runs `niwa apply`. niwa fetches a fresh
representation of the new default-branch tip, computes the new resolved
commit, sees it differs from the snapshot's recorded commit, and
atomically replaces the snapshot. No merge conflicts, no fast-forward
errors, no manual reconciliation. `niwa status` shows the new commit
and the latest fetched-at timestamp. The failure mode that wedged this
workflow before this redesign no longer exists.

### Story 5: Existing standalone-`dot-niwa` user upgrades

A developer with an established workspace pointing at a standalone
`org/dot-niwa` repo upgrades to a v3-aware niwa binary. Their
registry source URL is unchanged; their on-disk `<workspace>/.niwa/`
is still a git working tree. They run `niwa apply` as usual. niwa
detects the working-tree shape under an unchanged URL, lazy-converts
to a snapshot in place (atomic rename), proceeds with apply, and
prints a one-time notice: `note: <workspace>/.niwa/ converted from
working tree to snapshot. Manual edits inside this directory will
no longer persist across apply.` Subsequent applies behave like Story
1. No `--force` flag, no destroy/re-init ritual.

### Story 6: CI / automation operator

A CI pipeline runs `niwa apply` against a workspace pointing at
`org/brain-repo:.niwa`. The pipeline runs after every push to a
related repo and must use the latest config or fail loudly. The
operator notices that v1 niwa, when the network is unreachable
mid-apply, falls back to the cached snapshot with a stderr warning
and exit 0 — acceptable for human developers but unacceptable for
CI where stale config is unsafe. The operator documents this as a
known limitation for now and tracks the planned `--strict-refresh`
flag (deferred to a follow-up release) for the use case. They
confirm that the workspace's normal failure modes (subpath not
found, host unreachable on first fetch with no cache, source URL
mismatch) all produce non-zero exits suitable for CI gating.

## Test Strategy

The acceptance criteria below depend on test fixtures niwa does not
have today. v1 commits to building the following fixtures as part of
this work; without them the GitHub-path acceptance criteria cannot be
verified mechanically.

### In-scope fixture deliverables

- **`tarballFakeServer`** — an in-process HTTP test server (paired
  helper alongside `localGitServer`) that serves
  `GET /repos/{owner}/{repo}/tarball/{ref}` and
  `GET /repos/{owner}/{repo}/commits/{ref}` against a
  test-controlled repo state. Supports:
  - configurable response bodies, status codes, and ETags
  - `If-None-Match` conditional GETs returning 304
  - request logging (so tests can assert "no tarball request was made")
  - fault-injection modes: `truncate-after:N` (close connection
    after N bytes), `delay:N`, `return-status:N`, `drop-next-request`
- **State-file factory step** — Gherkin step that authors a v1, v2,
  or v3 `InstanceState` file with arbitrary `schema_version` for the
  lazy-upgrade and forward-version-rejection scenarios.
- **Legacy working-tree fixture step** — Gherkin step that sets up a
  real `git clone`-shape `<workspace>/.niwa/` (with `.git/` present)
  for the snapshot-conversion migration scenarios.
- **Tarball-extraction fault-injection seam** — a niwa-side test hook
  (e.g., `NIWA_TEST_FAULT=truncate-after:N` env var) that simulates
  partial-write failures during snapshot materialization.

These fixtures live in `test/functional/` alongside `localGitServer`
and follow the same Go test-helper conventions. Each acceptance
criterion below that depends on a fixture names the fixture
explicitly.

### Acceptance-criterion conventions

- Each AC begins with a Given (precondition / fixture state), implicit
  When (the niwa command being verified), and Then (observable
  assertion).
- Observables include: process exit code, stderr substring presence,
  file/directory existence and contents, request counts on a fake
  server, byte-identity of state files.
- Performance targets are documented as expected behavior in Known
  Limitations rather than acceptance criteria; v1 does not gate-block
  on performance.

## Requirements

### Functional requirements

**Subpath sourcing**

- **R1.** niwa MUST accept a slug grammar of the form
  `[host/]owner/repo[:subpath][@ref]`, where `:` separates the subpath
  and `@` separates the ref. Whole-repo sources keep their current
  shorthand (`owner/repo`) and resolve as `subpath = ""` (interpreted as
  "run discovery").
- **R2.** niwa MUST persist the parsed source tuple
  (`source_host`, `source_owner`, `source_repo`, `source_subpath`,
  `source_ref`) as registry mirror fields alongside the canonical
  opaque `source_url` slug.
- **R3.** niwa MUST treat the slug parser as strict: the following
  inputs MUST be rejected at parse time with a diagnostic naming the
  offending input:
  (a) empty subpath after a colon (`org/repo:`),
  (b) malformed separator ordering (e.g., `@ref` appearing before
  `:subpath`: `org/repo@v1:.niwa`),
  (c) embedded whitespace anywhere in the slug,
  (d) multiple `:` separators (e.g., `org/repo:a:b`),
  (e) multiple `@` separators (e.g., `org/repo@v1@v2`).
- **R4.** niwa MUST accept a subpath that resolves to a regular file
  (not a directory) and treat the parent directory as the config
  directory. If the file is not a syntactically valid `workspace.toml`
  or `niwa.toml`, niwa MUST fail with the standard config-validation
  error path.

**Convention-based discovery**

- **R5.** When the slug omits an explicit subpath, niwa MUST probe the
  source repo root for marker files in this fixed precedence order:
  `.niwa/workspace.toml` (rank 1), root `workspace.toml` (rank 2), root
  `niwa.toml` (rank 3). The first match resolves the subpath.
- **R6.** When more than one marker is present at the source repo root,
  niwa MUST fail with an unambiguous "ambiguous niwa config" error
  naming the conflicting files. niwa MUST NOT pick a winner silently.
- **R7.** When discovery finds no marker at the source repo root and no
  explicit subpath was given, niwa MUST fail with a discovery error
  naming the three accepted markers and the explicit-subpath escape
  hatch.
- **R8.** When discovery resolves to repo root via the rank-3
  `niwa.toml` convention, niwa MUST require `[workspace] content_dir`
  to be set explicitly in the resolved config; an omitted
  `content_dir` in this case MUST fail with a diagnostic naming the
  resolved source slug, the resolved subpath (`/`), the missing
  setting (`[workspace] content_dir`), and the explicit-opt-in escape
  hatch (`content_dir = "."`). The explicit value `content_dir = "."`
  is valid (opt-in to "the whole brain repo is content").
- **R9.** Explicit subpath in the slug MUST bypass discovery entirely.
  If the explicit subpath does not contain a `workspace.toml`, niwa
  MUST fail without falling back to discovery.

**Snapshot materialization**

- **R10.** niwa MUST materialize the workspace config as a pure file
  tree at `<workspace>/.niwa/` containing exactly:
  (a) every regular file present at the resolved subpath in the source
  commit, with directory structure preserved;
  (b) one provenance marker file (location and format determined at
  design time, subject to R11 and R34);
  (c) niwa-local state files written by niwa itself (e.g.,
  `instance.json`), enumerated in the design as a closed set and
  carried across snapshot refresh by an assembly step (per the
  2026-04-23 amendment).
  No additional files MUST persist, including (but not limited to)
  `.git/`, `pax_global_header`, tarball-wrapper directories, or other
  VCS metadata.
- **R11.** The provenance marker MUST record at minimum: the source URL
  as a single canonical string, the parsed source tuple
  (`host`, `owner`, `repo`, `subpath`, `ref`), the resolved commit
  oid, the fetched-at timestamp (RFC 3339), and the fetch mechanism
  identifier (e.g., `github-tarball`, `git-clone-fallback`).
- **R12.** Snapshot refresh MUST be atomic from the perspective of
  concurrent readers: niwa MUST materialize the new snapshot at a
  sibling path (e.g., `<workspace>/.niwa.next/`), then swap it into
  place such that at no point during the swap is `<workspace>/.niwa/`
  absent or partially populated. The previous snapshot MUST be
  removed only after the new snapshot is observable at the canonical
  path. On platforms where atomic directory-swap is not available,
  niwa MUST use a documented best-effort sequence (rename old to
  backup, rename new to canonical, then delete backup) and accept the
  brief sub-microsecond non-atomic window.
- **R13.** The same snapshot model MUST apply symmetrically to the team
  config clone, the personal overlay clone, and the workspace overlay
  clone. All three must share the no-`.git/`, atomic-refresh, marker-
  bearing posture, and all three MUST use the same fetch-mechanism
  selection (R14/R15) based on their source host.

**Fetch mechanisms**

- **R14.** When the source host is `github.com`, niwa MUST use the
  GitHub REST tarball endpoint with selective extraction filtered to
  the requested subpath. niwa MUST stream-extract using Go's
  `archive/tar` package without invoking a system `tar` binary. Files
  outside the subpath MUST never be written to disk during extraction.
- **R15.** When the source host is anything other than `github.com`
  (including GitHub Enterprise Server, GitLab, Bitbucket, Gitea, and
  `file://` URLs), niwa MUST use a temp-directory `git clone --depth=1`
  fallback followed by a copy of the requested subpath into the
  snapshot location. The temporary clone MUST be removed after copy
  per R33.
- **R16.** Drift detection on the GitHub path MUST use the 40-byte
  `commits/{ref}` SHA endpoint with `Accept: application/vnd.github.sha`
  and `If-None-Match` ETag-conditional GETs against the tarball
  endpoint. Drift detection on the fallback path MUST use
  `git ls-remote <url> <ref>`.
- **R17.** Authentication for GitHub fetches MUST read the `GH_TOKEN`
  environment variable. When `GH_TOKEN` is unset, niwa MUST attempt
  the request anonymously (suitable for public repos). Authentication
  for the git-clone fallback MUST defer to git's existing credential
  resolution (SSH agent, `~/.netrc`, `git config insteadOf`,
  credential helpers) — niwa MUST NOT inject or override credentials
  for the fallback path.
- **R18.** When the GitHub API responds with a 301 redirect for a
  renamed repository, niwa MUST follow the redirect once for the
  immediate request to complete successfully, and MUST emit a
  one-time `note:`-prefixed notice (using the existing
  `DisclosedNotices` mechanism) naming the old and new repository
  paths. Subsequent applies against the same registry entry MUST NOT
  silently re-follow; the user is expected to update the registry to
  the new canonical name.

**Default branch and ref resolution**

- **R19.** When the slug omits `@ref`, niwa MUST re-resolve the source
  repo's default branch on every `niwa apply` (not pin at init time).
  niwa MUST record the latest resolved commit oid in
  `InstanceState.config_source.resolved_commit` after each apply.
- **R20.** `niwa status` MUST distinguish a pinned ref from an
  auto-resolved default branch in its detail-view output (e.g., the
  string "(default branch)" appended when no ref was specified).
- **R21.** When a source URL omits `@ref` and the default branch
  cannot be re-resolved (network unreachable), niwa MUST continue with
  the cached snapshot and emit a `warning:`-prefixed notice naming the
  source URL, the cached commit oid, and the cached fetched-at
  timestamp. Apply MUST NOT abort. (CI/automation operators wanting
  fail-on-stale behavior are deferred to a future `--strict-refresh`
  flag.)

**Registry and state schema**

- **R22.** niwa MUST persist registry entries with the parsed mirror
  fields (R2) populated. The opaque `source_url` field MUST be
  treated as canonical: when the parsed mirror fields disagree with
  the canonical slug (e.g., user hand-edited the registry file), niwa
  MUST re-parse `source_url`, overwrite the mirror fields on next
  save, and emit a stderr warning naming the inconsistency.
- **R23.** Existing registry entries written by older binaries (no
  mirror fields) MUST parse on read. The mirror fields MUST be
  populated on the first invocation of any command that loads the
  registry, and MUST be persisted to disk on the next save (lazy-
  upgrade-on-first-write). Read-only commands like `niwa status` MUST
  NOT mutate the file but MUST behave as if the mirror were present.
- **R24.** niwa MUST bump the per-instance state schema to v3 and add a
  `config_source` block carrying `(url, host, owner, repo, subpath,
  ref, resolved_commit, fetched_at)`. v2 state files MUST load
  successfully; the next `niwa apply` MUST populate `config_source`
  from the registry mirror plus the current snapshot's provenance and
  write a v3 file on next save.
- **R25.** When niwa reads a state file with `schema_version` greater
  than the highest version this binary supports, it MUST fail with a
  diagnostic naming the observed and supported versions. The on-disk
  state file MUST be byte-identical to its pre-failure state. niwa
  MUST NOT attempt down-conversion.

**Migration: working tree to snapshot**

- **R26.** When `niwa apply` runs against a workspace whose registry
  source URL has changed AND whose `<workspace>/.niwa/` is the legacy
  working-tree form (has `.git/` present), niwa MUST refuse to proceed
  without `--force`. The error MUST name the old and new source URLs,
  the discovered subpath in the new source, and an inspection command
  (`cd .niwa && git status`).
- **R27.** When `--force` is passed (or `<workspace>/.niwa/` is already
  a snapshot), niwa MUST atomically replace the materialization from
  the new source per R12. Before replacement, niwa MUST validate that
  the new source's `[workspace].name` matches the registered workspace
  name; on mismatch niwa MUST refuse without `--rename` and MUST name
  both the registered name and the new source's name in the error.
- **R28.** When `niwa apply` runs against a workspace whose registry
  source URL is UNCHANGED but whose `<workspace>/.niwa/` is the legacy
  working-tree form, niwa MUST lazy-convert the working tree to a
  snapshot in place (atomic, per R12) and proceed with apply. niwa
  MUST emit a one-time `note:`-prefixed notice (via the existing
  `DisclosedNotices` mechanism) naming the conversion. No `--force`
  flag is required for this case.
- **R29.** Both the `niwa config set global <slug>` CLI and direct
  edit of `~/.config/niwa/config.toml` MUST be supported as entry
  points for changing the registered source. The next `niwa apply`
  MUST detect the URL change identically regardless of which entry
  point was used.

**Replacement of `.git/`-dependent paths**

- **R30.** `niwa reset`'s "is this config from a remote" check
  (currently `isClonedConfig` in `internal/cli/reset.go`) MUST read
  the snapshot provenance marker rather than `<configDir>/.git/`
  presence. When the marker is present, niwa MUST treat the config
  as cloned and offer re-fetch as the recovery path. When absent,
  niwa MUST treat the config as user-authored (matching today's
  local-only-workspace semantics).
- **R31.** The plaintext-secrets public-repo guardrail (currently
  `CheckGitHubPublicRemoteSecrets` in
  `internal/guardrail/githubpublic.go`) MUST enumerate remotes by
  reading the snapshot provenance marker's `host`/`owner`/`repo`
  fields rather than by invoking `git -C <configDir> remote -v`. The
  existing GitHub-public detection contract is preserved (case-
  insensitive `host == "github.com"` plus public-visibility check via
  the configured GitHub API for the owner/repo); only the input
  source changes.
- **R32.** The `--allow-dirty` flag MUST be silently accepted in v1
  with a stderr deprecation notice (`warning: --allow-dirty is no
  longer meaningful under the snapshot model and will be removed in
  v1.1`). The notice MUST be printed once per process invocation. The
  flag MUST be hard-removed in the v1.1 release.

**Backwards compatibility**

- **R33.** Existing registries with `source_url = "org/dot-niwa"` (no
  subpath) MUST continue to resolve via discovery rank 2 (root
  `workspace.toml`) without user action. R28 covers the on-disk
  working-tree-to-snapshot conversion; R23 covers the registry
  schema upgrade. No upgrade-time prompt MUST fire for these users.
- **R34.** No existing user MUST be required to take any action after
  upgrading to the v3-aware niwa binary. The first `niwa apply` after
  upgrade triggers all lazy migrations (R23 registry, R24 state, R28
  on-disk conversion) automatically.

**Workspace overlay discovery**

The auto-discovered workspace overlay carries content that requires
permissions beyond the team-config audience (typically: private orgs
to clone, internal hooks, secrets metadata). Its purpose is access
asymmetry — users with overlay access get the augmentation, users
without it apply against the team config alone.

- **R35.** When `niwa init` resolves a team config source (via either
  explicit subpath or convention-based discovery from R5), niwa MUST
  attempt to clone an auto-discovered workspace overlay using the
  following slug-derivation rule:
  - If the resolved team-config dir is the source repo root (whole-
    repo case, including the file-resolves-to-parent case from R4):
    overlay slug = `<host>/<source-org>/<source-repo>-overlay`.
  - If the resolved team-config dir is a subdirectory of the source
    repo: overlay slug = `<host>/<source-org>/<basename>-overlay`,
    where `<basename>` is the last path segment of the resolved
    subpath. For example, `org/brain:.niwa` resolves to overlay
    `org/.niwa-overlay`; `org/brain:teams/research` resolves to
    `org/research-overlay`.
  - The overlay clone uses the SAME host as the source.
  - The overlay clone is treated as a whole-repo source: niwa expects
    `workspace-overlay.toml` at the overlay repo's root. (Subpath
    sourcing for the overlay itself is deferred to v1.x; users who
    need it can pass `--overlay <slug-with-subpath>` explicitly.)
  - The overlay clone MUST silently skip on failure (matching today's
    behavior) so users without overlay-repo access continue to apply
    against the team config alone.
  - The overlay snapshot inherits R13 (no `.git/`, atomic refresh,
    provenance marker present) and R14/R15 (GitHub-tarball or git-
    clone fallback based on host).
- **R36.** `niwa status` detail view MUST display the resolved
  workspace overlay slug on a line of its own when an overlay was
  discovered and cloned successfully. When no overlay was discovered,
  `--no-overlay` was passed at init, or the clone silently skipped
  (R35), niwa MUST NOT print an overlay line. The overlay line MUST
  be visually distinct from the team-config source line so users can
  tell which augmentation is in effect.

### Non-functional requirements

- **R37.** Files outside the resolved subpath MUST NOT be written to
  disk during materialization on the GitHub path, even temporarily
  (verified via the no-side-effect-files invariant in R10 plus the
  stream-extract requirement in R14).
- **R38.** The provenance marker MUST be readable with no specialized
  tooling — a contributor inspecting `<workspace>/.niwa/` manually
  with `cat`, `jq`, or `toml` (depending on the chosen format) MUST
  be able to identify origin, ref, and fetched-at without running
  niwa.

## Acceptance Criteria

Each AC is binary pass/fail. ACs that depend on a fixture name it
explicitly. The Test Strategy section above defines fixtures.

### AC: Subpath sourcing (verifies R1-R4)

- [ ] **AC-S1**. Given a slug `owner/repo:path/to/config@v1.2.0`,
  `niwa init <name> --from <slug>` parses into the four-tuple and
  stores all five mirror fields plus the opaque slug in
  `~/.config/niwa/config.toml` under `[registry.<name>]`.
- [ ] **AC-S2**. Given a bare slug `owner/repo`,
  `niwa init <name> --from <slug>` triggers discovery and persists
  the resolved subpath in `source_subpath`.
- [ ] **AC-S3a**. `niwa init <name> --from owner/repo:` exits non-zero
  with stderr containing the literal string "empty subpath".
- [ ] **AC-S3b**. `niwa init <name> --from owner/repo@v1:.niwa` exits
  non-zero with stderr naming the malformed separator ordering.
- [ ] **AC-S3c**. `niwa init <name> --from "owner/repo: .niwa"` (with
  embedded whitespace) exits non-zero with stderr naming the
  whitespace.
- [ ] **AC-S3d**. `niwa init <name> --from owner/repo:a:b` exits
  non-zero with stderr naming the multiple-colon error.
- [ ] **AC-S3e**. `niwa init <name> --from owner/repo@v1@v2` exits
  non-zero with stderr naming the multiple-`@` error.
- [ ] **AC-S4a**. Given a fixture source whose `path/to/niwa.toml`
  is a valid one-file workspace config,
  `niwa init <name> --from owner/repo:path/to/niwa.toml` treats
  `path/to/` as the config directory and validates the file as the
  workspace config.
- [ ] **AC-S4b**. Given a fixture source whose `path/to/notconfig`
  is a non-TOML file,
  `niwa init <name> --from owner/repo:path/to/notconfig` exits
  non-zero with the standard config-validation error.
- [ ] **AC-S5**. Given a slug `owner/repo:nonexistent` and a
  successful tarball fetch that does not contain the subpath, niwa
  fails with a "subpath not found" diagnostic naming the subpath, the
  resolved commit oid, and the source slug. The on-disk
  `<workspace>/.niwa/` is byte-identical to its pre-apply state.

### AC: Convention-based discovery (verifies R5-R9)

- [ ] **AC-D1**. Given a `tarballFakeServer` source containing only
  `.niwa/workspace.toml`, discovery resolves
  `source_subpath = ".niwa/"`.
- [ ] **AC-D2**. Given a source containing only a root
  `workspace.toml` (the standalone-`dot-niwa` shape), discovery
  resolves `source_subpath = ""` and apply succeeds without prompts.
- [ ] **AC-D3**. Given a source containing only a root `niwa.toml`
  with `[workspace] content_dir = "claude"`, discovery resolves
  `source_subpath = ""` and apply succeeds.
- [ ] **AC-D4**. Given a source containing a root `niwa.toml`
  without `[workspace] content_dir`, apply exits non-zero with stderr
  containing the resolved slug, the resolved subpath (`/`), the
  literal `content_dir`, and the literal `content_dir = "."`.
- [ ] **AC-D5**. Given a source containing a root `niwa.toml` with
  `[workspace] content_dir = "."`, apply succeeds and
  `InstallWorkspaceContent` reads from repo root as the content root.
- [ ] **AC-D6**. Given a source containing both `.niwa/workspace.toml`
  AND a root `workspace.toml`, apply exits non-zero with stderr
  naming both files and the literal "ambiguous niwa config".
- [ ] **AC-D7**. Given a source containing a `.niwa/` directory but
  no `.niwa/workspace.toml` inside it, AND a root `workspace.toml`,
  discovery resolves to rank 2 (no error fires for the empty
  `.niwa/`).
- [ ] **AC-D8**. Given a source containing none of the three
  markers, apply exits non-zero with stderr listing all three
  accepted marker paths.
- [ ] **AC-D9**. Given an explicit slug
  `owner/repo:custom/path` and a source where `custom/path/` exists
  but contains no `workspace.toml`, apply exits non-zero without
  attempting discovery; stderr names the missing `workspace.toml` at
  `custom/path/`.

### AC: Snapshot materialization (verifies R10-R13)

- [ ] **AC-M1**. After first `niwa apply` against a configured
  source, `<workspace>/.niwa/` contains the source subpath's regular
  files and the provenance marker, and ONLY those (plus any niwa-
  local state files carried by the assembly step). `find
  <workspace>/.niwa/ -name '.git*' -o -name 'pax_global_header'`
  returns no results. `git -C <workspace>/.niwa/ status` exits with a
  "not a git repository" status code.
- [ ] **AC-M2**. The provenance marker file in `<workspace>/.niwa/`
  contains values for keys `source_url`, `host`, `owner`, `repo`,
  `subpath`, `ref`, `resolved_commit`, `fetched_at`, and
  `fetch_mechanism`, parseable with a generic TOML / JSON / YAML
  reader (per the format chosen at design time) without invoking
  niwa.
- [ ] **AC-M3**. Given a snapshot whose source has been force-pushed
  (the `tarballFakeServer` returns a different commit oid for the
  same ref), `niwa apply` re-fetches and re-materializes the
  snapshot. The previous `<workspace>/.niwa/` directory is observable
  at the canonical path until the swap completes; partial
  intermediate states are not. niwa exits with code 0 (no
  fast-forward error).
- [ ] **AC-M4**. Given a snapshot mid-refresh (using the
  `truncate-after:N` fixture mode that closes the connection mid-
  tarball), the previous `<workspace>/.niwa/` is byte-identical to
  its pre-refresh tree. The provenance marker's `resolved_commit` is
  the pre-refresh oid.
- [ ] **AC-M5**. Given a snapshot whose source's `commits/{ref}`
  endpoint returns the same oid as the snapshot's
  `resolved_commit`, `niwa apply` does not extract the tarball. The
  `tarballFakeServer` records zero `GET /tarball` requests during
  this apply. The snapshot's `fetched_at` is updated.
- [ ] **AC-M6**. After first `niwa apply` against a workspace with a
  personal overlay AND a workspace overlay both pointing at GitHub
  sources, no `.git/` directories exist in any of the three
  snapshots (`<workspace>/.niwa/`, the personal overlay clone path,
  the workspace overlay clone path), and each contains a provenance
  marker.

### AC: GitHub tarball fetch and auth (verifies R14-R18)

- [ ] **AC-G1**. Given a `tarballFakeServer` source whose tarball
  contains files at `<root>/.niwa/...` and `<root>/src/...`, after
  apply against `owner/repo:.niwa`, no files from `src/` exist
  anywhere on disk inside `<workspace>/.niwa/` or in any temp
  directory under `$TMPDIR`.
- [ ] **AC-G2**. Given a snapshot apply, the `tarballFakeServer`
  receives the second apply's `commits/{ref}` request with
  `Accept: application/vnd.github.sha`; the response body is the
  cached oid; the second apply makes zero requests to
  `/repos/{owner}/{repo}/tarball/{ref}`.
- [ ] **AC-G3**. Given a `tarballFakeServer` configured to return
  304 on a conditional GET with `If-None-Match: <oid>`, niwa issues
  the conditional GET on the second apply when the SHA endpoint
  reports a different oid. The 304 is treated as no-change: the
  snapshot's mtime is unchanged and the snapshot directory is not
  re-extracted.
- [ ] **AC-G4**. Given `GH_TOKEN=test-token` in the niwa process
  env, the `tarballFakeServer` records both the `commits/` request
  and the `tarball/` request as carrying
  `Authorization: Bearer test-token` (or the GitHub-canonical
  equivalent).
- [ ] **AC-G5**. Given `GH_TOKEN` unset and a public source, niwa
  fetches successfully without an `Authorization` header.
- [ ] **AC-G6**. Given a `tarballFakeServer` configured to return
  401 on the tarball endpoint, niwa exits non-zero with stderr
  containing the literal `401` and a substring naming PAT scope
  (e.g., `PAT scope`).
- [ ] **AC-G7**. Given a `tarballFakeServer` configured to return a
  301 redirect from `/repos/oldorg/oldrepo/...` to
  `/repos/neworg/newrepo/...`, niwa follows the redirect for the
  immediate request and emits a one-time `note:`-prefixed notice
  naming both `oldorg/oldrepo` and `neworg/newrepo`. A second apply
  against the same registry entry (without registry update) emits no
  duplicate notice.

### AC: Non-GitHub fallback (verifies R15)

- [ ] **AC-F1**. Given a `localGitServer` `file://` source, after
  apply, `<workspace>/.niwa/` contains the source's files (per the
  resolved subpath) and no `.git/` directory.
- [ ] **AC-F2**. Given an apply that successfully completes against
  a non-GitHub source, the OS-level temp directory used for the
  intermediate `git clone` is removed (verified via the helper that
  records temp-dir lifetimes).

### AC: Default branch and ref resolution (verifies R19-R21)

- [ ] **AC-R1**. Given a slug `owner/repo` (no `@ref`), after apply,
  `instance.json` contains a `config_source.resolved_commit` value
  equal to the `tarballFakeServer`'s response for the default-branch
  HEAD.
- [ ] **AC-R2**. `niwa status` against a workspace with a ref-less
  slug shows the source line containing the literal substring
  `(default branch)`.
- [ ] **AC-R3**. `niwa status` against a workspace with `@v1.0`
  shows the source line containing `v1.0` and not `(default branch)`.
- [ ] **AC-R4**. Given a slug `owner/repo` and a
  `tarballFakeServer` configured with `drop-next-request` on both
  `commits/` and `tarball/` (simulating network unreachable), apply
  exits with code 0, stderr contains a `warning:`-prefixed line
  naming the source URL, the cached `resolved_commit`, and the
  cached `fetched_at`.

### AC: Registry and state schema (verifies R22-R25)

- [ ] **AC-X1**. Given a registry entry written by an older binary
  (no mirror fields), the next `niwa apply` writes the registry file
  with all mirror fields populated. The unrelated fields (`groups`,
  other `[registry.*]` entries) are byte-identical to before.
- [ ] **AC-X2**. Given a registry entry hand-edited so that
  `source_url = "owner/repo:.niwa"` but `source_subpath = "wrong"`,
  the next registry mutation rewrites `source_subpath = ".niwa"` and
  stderr emits a warning naming the inconsistency. The opaque
  `source_url` is preserved.
- [ ] **AC-X3**. Given an `instance.json` with `schema_version: 2`
  and no `config_source` block, `niwa apply` parses it, populates
  `config_source` from the registry mirror plus the snapshot
  provenance, and writes a v3 file on save.
- [ ] **AC-X4**. Given an `instance.json` with `schema_version: 99`,
  `niwa apply` exits non-zero with stderr naming both `99` and the
  highest supported version. The state file is byte-identical to
  its pre-load state.

### AC: Migration from working tree to snapshot (verifies R26-R29)

- [ ] **AC-Y1**. Given a workspace whose registry `source_url`
  changed AND whose `<workspace>/.niwa/` has a `.git/` directory,
  `niwa apply` (without `--force`) exits non-zero with stderr naming
  both URLs, the discovered subpath in the new source, and the
  literal `cd .niwa && git status`. The on-disk `<workspace>/.niwa/`
  is byte-identical to its pre-apply state.
- [ ] **AC-Y2**. Given the same setup as AC-Y1 plus the new source's
  `[workspace].name` matches the registered name,
  `niwa apply --force` succeeds. `<workspace>/.niwa/` is replaced
  with a snapshot from the new source per AC-M1.
- [ ] **AC-Y3**. Given the same setup as AC-Y1 plus the new source's
  `[workspace].name` is "differentname" (mismatched),
  `niwa apply --force` exits non-zero with stderr containing both the
  registered name and `differentname` plus the literal `--rename`.
- [ ] **AC-Y4**. Given a workspace whose registry `source_url` is
  unchanged AND whose `<workspace>/.niwa/` has a `.git/` directory
  (legacy working tree), `niwa apply` (without `--force`) succeeds.
  After apply, `<workspace>/.niwa/` has no `.git/` directory and
  contains a provenance marker. stderr contains a one-time
  `note:`-prefixed line naming the conversion. A second apply against
  the same workspace emits no duplicate notice.
- [ ] **AC-Y5**. Given a registry change applied via
  `niwa config set global <slug>`, the next `niwa apply` triggers
  AC-Y1 detection identically to a registry change applied via
  direct file edit.

### AC: Replacement of `.git/`-dependent paths (verifies R30-R32)

- [ ] **AC-Z1**. Given a workspace with a snapshot provenance
  marker, `niwa reset <instance>` recognizes the config as cloned
  (treats it as remote-sourced for recovery purposes), not as
  user-authored.
- [ ] **AC-Z2** (positive guardrail). Given a snapshot whose
  provenance marker names `host=github.com`, `owner=<public-org>`,
  AND a `workspace.toml` containing a plaintext value in
  `[env.secrets]`, `niwa apply` exits non-zero with stderr
  containing `public repo` and naming the offending key. The
  guardrail does not invoke `git remote -v` against the snapshot.
- [ ] **AC-Z3** (negative guardrail). Given a snapshot whose
  provenance marker names a non-`github.com` host, the guardrail
  does not fire on a workspace with plaintext secrets. apply
  succeeds.
- [ ] **AC-Z4**. `niwa apply --allow-dirty` against any workspace
  succeeds with stderr containing `--allow-dirty is no longer
  meaningful under the snapshot model and will be removed in v1.1`.
- [ ] **AC-Z5**. A second `niwa apply --allow-dirty` invocation in
  the same process does not print the deprecation notice; a fresh
  process invocation does.

### AC: Workspace overlay discovery (verifies R35-R36)

- [ ] **AC-O1**. Given a team-config source `org/dot-niwa` (whole
  repo, no subpath), `niwa init` attempts to clone the overlay slug
  `org/dot-niwa-overlay`. (Today's literal behavior preserved.)
- [ ] **AC-O2**. Given a team-config source `org/brain:.niwa` (subpath
  `.niwa`), `niwa init` attempts to clone the overlay slug
  `org/.niwa-overlay`.
- [ ] **AC-O3**. Given a team-config source `org/brain:teams/research`
  (multi-segment subpath), `niwa init` attempts to clone the overlay
  slug `org/research-overlay` (last path segment only, not
  `teams/research-overlay`).
- [ ] **AC-O4**. Given a team-config source `org/dot-niwa:niwa.toml`
  (subpath resolves to a file at repo root, R4 treats parent as
  config dir), `niwa init` attempts to clone the overlay slug
  `org/dot-niwa-overlay`. (Whole-repo behavior, since the resolved
  config dir is the source repo root.)
- [ ] **AC-O5**. Given the overlay slug derived above and the overlay
  repo does not exist (or the user lacks access), the overlay clone
  fails silently and `niwa apply` proceeds against the team config
  alone with exit code 0.
- [ ] **AC-O6**. Given a team-config source whose host is
  `github.com`, the overlay clone uses the GitHub tarball path (R14).
  Given a non-`github.com` source, the overlay clone uses the
  git-clone fallback (R15).
- [ ] **AC-O7**. After successful overlay clone, the overlay snapshot
  contains no `.git/` directory and contains a provenance marker
  (per R13).
- [ ] **AC-O8**. `niwa status` detail view against a workspace with a
  successfully-cloned overlay shows a dedicated overlay line naming
  the resolved overlay slug. `niwa status` against a workspace where
  the overlay clone silently skipped shows no overlay line.

### AC: Backwards compatibility (verifies R33-R34)

- [ ] **AC-B1**. Given a registry from an older binary with
  `source_url = "org/dot-niwa"` (no subpath, no mirror fields), the
  first `niwa apply` on the v3-aware binary succeeds; the on-disk
  `.niwa/` is converted per AC-Y4; the registry mirror fields are
  written per AC-X1; the state schema is upgraded per AC-X3. No
  user prompt fires.

### AC: Documented limitations (regression guards)

- [ ] **AC-L1**. Given a snapshot exists, when a file is modified
  inside `<workspace>/.niwa/` and `niwa apply` runs against a
  source whose oid changed, the modification is gone after apply
  (verifies the documented "manual edits silently discarded on
  refresh" behavior).
- [ ] **AC-L2**. Given a source with a `.niwa/workspace.toml` AND a
  root `workspace.toml` (ambiguous), then the maintainer removes the
  root `workspace.toml`, the next `niwa apply` resolves to the
  remaining marker and succeeds (verifies the documented workaround
  for the slug repo-root sentinel limitation).
- [ ] **AC-L3**. Given a source whose subpath contains a git
  submodule pointer, after apply, the submodule subdirectory exists
  but is empty (verifies the documented "submodules silently not
  expanded" behavior).
- [ ] **AC-L4**. Given a source whose subpath contains an LFS-
  tracked file, after apply, the LFS-tracked file contains the LFS
  pointer text rather than the real bytes (verifies the documented
  "LFS pointers passed through" behavior).

## Out of Scope

The items below are deliberately excluded from this PRD's v1
commitment. Each is either a downstream design-doc concern, a
follow-up release candidate, or unrelated work.

- **Schema redesign of `[claude]`, `[env]`, `[files]`, or other
  workspace.toml blocks.** Only the additions necessary to express
  subpath sourcing and to enforce R8 are in scope.
- **Vault provider sourcing.** Already designed in
  `docs/designs/current/DESIGN-vault-integration.md`; unaffected by
  this work.
- **Apply-pipeline behavior after the snapshot is materialized.** Every
  materializer, validator, hook discovery, env discovery, and content
  reader continues to operate against the materialized file tree
  unchanged.
- **Multi-tenant or hosted niwa scenarios.** Out of v1.
- **Telemetry source-redaction design.** No telemetry pipeline exists
  in niwa today; the redaction principle ("hash source identity by
  default; opt-in for full visibility") is documented in the
  exploration findings but not built.
- **Multi-workspace shared content-addressed cache.** The v1 snapshot
  lives directly at `<workspace>/.niwa/` (no symlink to a shared
  cache). Candidate for a v1.x follow-up.
- **Per-host adapters for GitLab, Bitbucket, Gitea, and GitHub
  Enterprise Server.** v1 ships GitHub-tarball-fast-path plus the
  git-clone fallback; per-host adapters are pure performance
  optimizations deferred to follow-ups.
- **`vault_scope = "@source"` shorthand.** Defer to v1.1.
- **Provenance marker file format and on-disk location.** The PRD
  commits to the contract (R11, R38) and leaves the file format,
  filename, and exact placement to the design phase.
- **`instance.json` placement relative to the snapshot.** The 2026-04-23
  amendment commits `instance.json` to live inside `<workspace>/.niwa/`
  alongside the source files. The earlier proposal to relocate to a
  sibling `.niwa-state/` directory is no longer planned. State survives
  snapshot refresh because the assembly step copies it into staging
  before the atomic swap.
- **Convention-aware fetch (manifest-driven pull).** The model where
  niwa pulls only files it knows about (workspace.toml + explicit
  references + codified conventions) instead of the entire resolved
  subpath is captured in a separate `needs-design` issue. v1 ships
  wholesale-subpath pull.
- **Read-only enforcement of the snapshot directory** (e.g., `chmod
  -R a-w`). The model is "manual edits don't persist after refresh";
  hard-enforcement is a candidate for a follow-up release.
- **Snapshot-edit warning on stale-mtime detection.** A future
  enhancement that could emit a warning when files inside the
  snapshot have mtimes newer than the marker's `fetched_at`. Adds
  scope without obvious v1 win; deferred.
- **Dedicated `niwa registry migrate` command.** R26-R29 lean on the
  existing `--force` pattern. A guided command is a v1.x candidate.
- **`niwa registry retarget` and `niwa registry refetch` commands.**
  v1 remediation is "edit the registry by hand and run apply" for
  these cases.
- **`--strict-refresh` flag for CI/automation operators.** Story 6
  documents the use case but the flag itself is deferred.
- **`gh auth token` integration as a fallback to `GH_TOKEN`.** v1
  uses `GH_TOKEN` env var only; `gh auth` integration is a
  follow-up candidate.
- **Performance benchmarks as gating CI checks.** Performance
  expectations live in Known Limitations; v1 does not gate-block on
  perf.

## Known Limitations

- **Slug repo-root sentinel.** When discovery is ambiguous (more than
  one marker at the source repo root), there's no consumer-side way to
  ask explicitly for "repo root" via the slug — `org/repo:` is
  rejected as an empty subpath (R3a). The only resolution is for the
  brain-repo maintainer to remove one of the markers.
- **First-fetch bandwidth on the GitHub path.** The tarball endpoint
  delivers the entire repo's gzipped bytes even when only a small
  subpath persists. For brain repos with very large histories or
  binary assets, the first fetch may take noticeably longer than the
  resulting snapshot's size suggests. Subsequent applies are cheap
  (40-byte SHA endpoint or 304 ETag).
- **Performance expectations (informal, not enforced).** A first-time
  GitHub fetch is expected to complete in under 5 seconds for a
  typical config-sized subpath (≤1 MB compressed) on a normal
  broadband connection. A drift check via the SHA endpoint is
  expected to complete in under 500 ms. These are not acceptance
  criteria; v1 does not gate on performance regression.
- **Manual edits inside the snapshot are silently discarded on
  refresh.** This is by design — the snapshot has no working-tree
  semantics — but is a behavior change for users who relied on
  `--allow-dirty` for local testing. The guide must call this out.
- **GitHub Enterprise Server uses the fallback path.** GHE supports
  the same tarball API shape, but v1 ships only `github.com` as
  first-class. GHE users get correct snapshots via the fallback.
- **Snapshot integrity check granularity.** v1 treats "provenance
  marker present and parseable" as integrity confirmation. Tampered
  but-syntactically-valid snapshots are not detected.
- **Submodules and LFS in the source.** Tarball-based fetches do not
  expand submodules or resolve LFS pointers. Workspace configs
  rarely use either.
- **CI / fail-on-stale gap.** v1 always continues with cached
  snapshot when the network is unreachable (R21). CI operators who
  need fail-on-stale behavior must wait for the deferred
  `--strict-refresh` flag (Story 6).
- **Overlay slug changes during the standalone-to-brain-repo
  migration.** Migrating a workspace from `org/dot-niwa` (whole-repo
  source) to `org/brain:.niwa` (subpath source) implicitly changes
  the auto-discovered overlay slug from `org/dot-niwa-overlay` to
  `org/.niwa-overlay` per R35. The brain-repo maintainer must
  arrange for the overlay repo to exist at the new slug (rename the
  existing overlay repo, or publish a new one) before consumers
  complete their migration; otherwise the overlay clone silently
  skips and consumers lose the augmentation without warning. This is
  a one-time coordination cost during the migration; subsequent
  applies behave normally.

## Decisions and Trade-offs

### Decision: slug delimiter is `:` not `//`

**Decided**: `[host/]owner/repo[:subpath][@ref]`. **Alternatives**:
Terraform-style `//subpath?ref=` (broader ecosystem precedent: go-getter,
Renovate, Cargo). **Reasoning**: niwa's existing CLI surface is
slug-shaped (`niwa init --from owner/repo`) and never URL-shaped. `:`
extends that with two new punctuation marks (`:` for "where in the repo",
`@` for "which version") and reads correctly for users coming from
Renovate's `org/repo:preset` convention. `?` is a glob in zsh and would
silently break unquoted invocations; `:` and `@` carry no shell metachar
hazard and survive `git config`-style URL parsing for the registry's
stored form.

### Decision: re-resolve default branch on every apply (not pin at init)

**Decided**: ref-less slugs trigger fresh default-branch resolution on
each `niwa apply`; the resolved commit oid is recorded in state.
**Alternatives**: pin the resolved branch at `niwa init` time and
require explicit opt-in to HEAD-tracking. **Reasoning**: the dominant
mental model for users typing `niwa init --from owner/repo` is the
`git clone` model — track the default. The new tarball fetcher
resolves `HEAD` server-side, so re-resolution is cheap (40-byte SHA
endpoint).

### Decision: migration UX leans on `--force`, no new command

**Decided**: `niwa apply` detects URL changes and refuses without
`--force`; the error message is the migration guide. Same-URL
upgrades (R28) lazy-convert without `--force` because no user-edit
loss is at stake. **Alternatives**: dedicated `niwa registry migrate`
command with interactive confirmation. **Reasoning**: niwa's existing
destructive-operation pattern (`destroy --force`, `reset --force`) is
non-interactive and already familiar. Adding interactive prompts would
be the first such pattern in the CLI and would split the discovery
surface.

### Decision: GitHub-first-class + git-clone fallback (no per-host adapters in v1)

**Decided**: GitHub uses the REST tarball + selective `tar` extraction;
all other hosts (including GHE) use a temp-dir `git clone --depth=1`
fallback. **Alternatives**: ship per-host adapters for GitLab,
Bitbucket, Gitea, and GHE in v1. **Reasoning**: niwa already has a
host-agnostic clone code path; the fallback covers every git-reachable
host on day one. Per-host adapters bring their own auth flows and URL
parsers — multi-week investments that would significantly delay v1.
Adapters become pure performance follow-ups.

### Decision: `content_dir` required when discovery resolves via `niwa.toml`

**Decided**: when discovery resolves to repo root via the rank-3
`niwa.toml` convention, `[workspace] content_dir` MUST be set
explicitly; the value `"."` is a valid opt-in. **Alternatives**: leave
`content_dir` optional with the existing default of `"."` (silent).
**Reasoning**: today's `content_dir = "."` default silently turns the
entire config dir into the content root. In a brain repo, that means
niwa would silently pick up `docs/`, `src/`, top-level `CLAUDE.md`,
and any `repos/` directory — files written for human readers.
Existing standalone-`dot-niwa` users (rank 2 discovery) keep
`content_dir` optional.

### Decision: `--allow-dirty` deprecated, not hard-removed

**Decided**: silently accepted in v1 with stderr deprecation notice;
hard-removed in v1.1. **Alternatives**: hard-remove immediately.
**Reasoning**: users may have it baked into scripts. A one-release
deprecation cycle gives them time to discover and update. The notice
is printed once per process invocation to avoid noise.

### Decision: offline behavior on default-branch resolution failure

**Decided**: continue with cached snapshot, emit `warning:`-prefixed
notice. **Alternatives**: hard-error on network unreachable.
**Reasoning**: the cached snapshot is on disk and still valid.
Punishing users for ephemeral network loss when the existing snapshot
satisfies the apply is the wrong pose — matches the existing
`SyncRepo` "fetch-failed → continue informationally" precedent. CI
operators wanting fail-on-stale wait for `--strict-refresh` (Story 6).

### Decision: same-URL upgrade is lazy, no `--force`

**Decided**: when `<workspace>/.niwa/` is a working tree but the
registry source URL is unchanged, niwa lazy-converts without
`--force` and emits a one-time notice. **Alternatives**: require
`--force` for all working-tree-to-snapshot conversions, regardless
of URL change. **Reasoning**: the `--force` gate exists to protect
developer edits during an identity change. For same-URL upgrades the
identity is preserved; the conversion is purely a materialization-
mechanism upgrade with no semantic interpretation of pending edits.
Forcing `--force` here punishes existing users for a niwa-internal
refactor.

### Decision: `GH_TOKEN` is the v1 GitHub auth source

**Decided**: niwa reads `GH_TOKEN` env var for GitHub fetches; falls
back to anonymous when unset. **Alternatives**: `gh auth token`
shell-out, `~/.config/niwa/credentials.toml`, or a credential
helper. **Reasoning**: ratifies the existing pattern used by
`internal/github/client.go`. Adding integration with `gh` (or other
sources) is a candidate follow-up; v1 keeps the surface narrow.

### Decision: workspace overlay discovery rule for subpath sources

**Decided**: when the team config is sourced from a subpath of a
brain repo, the auto-discovered workspace overlay slug is
`<host>/<source-org>/<basename>-overlay`, where `<basename>` is the
last path segment of the resolved subpath. For whole-repo sources
the rule reduces to `<host>/<source-org>/<source-repo>-overlay`,
preserving existing behavior. **Alternatives**: (a) sibling subpath
in the same brain repo (e.g., `org/brain:.niwa-overlay`); (b)
sibling repo with full-subpath inheritance (e.g.,
`org/brain-overlay:.niwa`); (c) drop auto-discovery for subpath
sources entirely. **Reasoning**: the overlay's purpose is *access
asymmetry* — it carries content that requires permissions beyond the
team-config audience (private orgs to clone, internal hooks, secrets
metadata). Folding the overlay into the same brain repo
(alternative a) destroys that asymmetry; users with brain-repo
access automatically see the overlay, defeating the entire
augmentation model. Mirroring the full subpath into a sibling repo
(alternative b) creates parallel directory structures across two
repos that have no semantic reason to mirror each other and
inflates the overlay's identity with the host repo's name (a
concept the overlay shouldn't have to know). Dropping auto-discovery
(alternative c) is a regression in ergonomics. The basename rule
keeps the access boundary intact, lets the overlay name reflect what
the team config is *about* (rather than what its host repo happens
to be called), and supports the natural case where one brain repo
hosts multiple team configs (`teams/research`, `teams/platform`)
each with its own access-restricted overlay.

### Decision: repo-rename behavior (follow once, warn once)

**Decided**: niwa follows the GitHub 301 redirect for the immediate
request and emits a one-time `DisclosedNotices` notice naming both
paths. **Alternatives**: hard-error on rename, or silently keep
following forever. **Reasoning**: the rename is real drift the user
should see; following silently masks it from the registry. Failing
hard punishes users for upstream changes they didn't cause.

