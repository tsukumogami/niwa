# Phase 2 Research: Maintainer Perspective

This file captures the maintainer-perspective findings for the
`workspace-config-sources` PRD. Two leads are addressed:

- Lead A: Acceptance criteria framing (testable contract statements
  derived from the round-1 settled decisions).
- Lead B: Documentation outline (what docs change, what gets created,
  what migration material the PRD must commit to).

The criteria are written in Gherkin-adjacent prose so they pair
directly with the existing `test/functional/features/` style. Each
capability ends with a `Negative` line capturing the failure mode niwa
must NOT exhibit — the negative-space contract is often clearer than
the positive contract on its own.

---

## Lead A: Acceptance criteria

### Capability 1: Subpath sourcing

The unified slug grammar is `owner/repo[:subpath][@ref]` with `:` as
the subpath separator and `@` as the ref separator (per round-1 L4/L6
recommendation; the `:` vs `//` tie-break is one of the PRD's open
decisions and these criteria assume `:` — they should be re-keyed if
the PRD picks `//`).

- **AC-1.1**: When the user runs
  `niwa init <name> --from owner/repo:path/to/config@v1.2.0`, niwa
  parses the slug into `(owner, repo, subpath="path/to/config",
  ref="v1.2.0")` and stores all four as parsed mirror fields in the
  registry entry, alongside the canonical opaque `source_url`.
- **AC-1.2**: When the user runs `niwa init <name> --from owner/repo`
  (no subpath, no ref), niwa enters the convention-based discovery
  path described in Capability 3 and persists the resolved subpath in
  the registry entry's `source_subpath` field (`""` if discovery
  resolved to repo root via rank 3 or 4).
- **AC-1.3**: When an existing `org/dot-niwa`-style standalone
  registry entry (no subpath in `source_url`, schema v2 state) is
  read by a v3-aware niwa binary, niwa treats it as the degenerate
  `subpath = "/"` case: registry behavior is unchanged, no migration
  prompt fires, and `niwa apply` continues to materialize the same
  on-disk content (subject to Capability 7's snapshot-vs-working-tree
  switch, which is a separate cutover).
- **AC-1.4**: When the user runs `niwa init <name> --from owner/repo:`
  (explicit empty subpath after the colon), niwa rejects the slug
  with a parse error naming the empty subpath; it does NOT silently
  reinterpret as "no subpath" or fall through to discovery.
- **AC-1.5**: When the resolved subpath inside the source repo
  resolves to a regular file rather than a directory (e.g.
  `owner/repo:path/to/niwa.toml`), niwa treats the parent directory
  of that file as the config dir and validates the file as the
  workspace config (single-file resolution, matching the discovery
  rank-3/4 case).
- **AC-1.6**: When the resolved subpath inside the source repo does
  not exist after fetch, niwa fails with an error naming the
  requested subpath, the resolved commit oid, and the source slug;
  the on-disk snapshot at `<workspace>/.niwa/` is NOT modified
  (atomic-rename guarantee).
- **AC-1.7**: When the source slug omits the ref (`@v1.2.0` part is
  absent), niwa resolves the source repo's default branch at the
  timing the PRD commits to (init-time pin vs apply-time re-resolve
  is open) and records the resolved ref in the registry entry's
  `source_ref` field; `source_url` continues to show the slug as the
  user typed it.
- **Negative**: niwa MUST NOT silently treat a malformed slug
  (extra colons, embedded whitespace, empty owner or repo, leading
  slash) as valid. Every slug rejection must name the offending
  character class and show the canonical grammar.

### Capability 2: Snapshot materialization

The `<workspace>/.niwa/` artifact is a pure file tree (no `.git/`)
populated from the configured source. Refresh is sibling-rename
atomic; provenance lives in a marker file recording origin URL, ref,
resolved commit oid, fetched-at timestamp, and fetch mechanism.

- **AC-2.1**: When `niwa apply` materializes a snapshot for the first
  time, the resulting `<workspace>/.niwa/` directory contains exactly
  the files from the source subpath plus a single provenance marker
  file (location/format is a downstream design decision); no `.git/`
  directory exists, and `git status` run inside `<workspace>/.niwa/`
  exits with "not a git repository" (or equivalent for the chosen
  marker layout).
- **AC-2.2**: When `niwa apply` runs against a snapshot whose source
  has been force-pushed (the same ref now points at a different
  commit oid), niwa fetches the new commit oid and re-materializes
  the snapshot atomically by writing into a sibling directory and
  renaming on success; the previous snapshot is removed only after
  the rename completes.
- **AC-2.3**: When `niwa apply` runs and the source's resolved commit
  oid matches the provenance marker's `resolved_commit`, niwa skips
  re-extraction (drift check confirmed in-sync) and updates only the
  `fetched_at` timestamp.
- **AC-2.4**: When snapshot materialization fails partway (network
  cut, tarball truncation, disk-full during extraction), the previous
  snapshot at `<workspace>/.niwa/` remains intact and the next
  `niwa apply` operates against it as if the failed apply never ran.
- **AC-2.5**: When the user manually edits a file inside
  `<workspace>/.niwa/`, the edit survives until the next refresh; the
  refresh discards the edit silently because there is no working
  tree to detect it against. This MUST be documented in the
  user-facing migration guide so users adjust the model.
- **AC-2.6**: When `niwa apply` runs against a snapshot whose source
  has not changed since the last apply, niwa MAY skip the network
  round-trip entirely if a cached drift signal (40-byte SHA endpoint
  response with `max-age=60`) confirms in-sync within its TTL.
- **Negative**: niwa MUST NOT fail with
  `fatal: Not possible to fast-forward, aborting` on remote rewrite
  (issue #72). niwa MUST NOT leave a partial snapshot on disk after
  an interrupted refresh. niwa MUST NOT prompt the user to reconcile
  manual edits inside the snapshot dir — the snapshot is by design
  not user-editable.

### Capability 3: Convention-based discovery

When the slug omits an explicit subpath, niwa probes a fixed marker
vocabulary at the source repo root and resolves to the first match.
Hard-error on ambiguity. Explicit subpath bypasses discovery entirely.

- **AC-3.1**: When `niwa init <name> --from owner/repo` runs and the
  source repo contains `.niwa/workspace.toml` only (rank 1), niwa
  resolves `source_subpath = ".niwa/"` and records it in the registry.
- **AC-3.2**: When the source repo contains a root-level
  `workspace.toml` only (rank 2, the standalone `org/dot-niwa` case),
  niwa resolves `source_subpath = ""` (repo root) and records it.
- **AC-3.3**: When the source repo contains a root-level `niwa.toml`
  only (rank 3), niwa resolves `source_subpath = ""` and validates
  the file as a single-file workspace config; if the PRD requires
  `content_dir` for this case (open question), apply fails with a
  diagnostic naming the missing field when the file omits it.
- **AC-3.4**: When the source repo contains both
  `.niwa/workspace.toml` and a root-level `workspace.toml`, niwa
  fails with an "ambiguous niwa config" error naming both files and
  pointing the user at the explicit-subpath escape hatch
  (`--from owner/repo:.niwa`).
- **AC-3.5**: When the source repo contains a `.niwa/` directory but
  no `.niwa/workspace.toml` inside it, niwa skips rank 1 and tries
  ranks 2 and 3 in order. The empty `.niwa/` directory is NOT itself
  treated as ambiguous evidence.
- **AC-3.6**: When the source repo contains none of the three marker
  files, niwa fails with an error naming the three expected paths
  and the explicit-subpath escape hatch.
- **AC-3.7**: When the user provides an explicit subpath
  (`--from owner/repo:custom/path`), niwa skips discovery entirely
  and validates the explicit path; if no `workspace.toml` is found
  inside that subpath, niwa fails immediately rather than falling
  back to discovery.
- **Negative**: niwa MUST NOT silently pick a winner when two
  marker files coexist. niwa MUST NOT walk subdirectories beyond
  the repo root probing for markers.

### Capability 4: GitHub tarball fetch with drift detection

GitHub repos are sourced via the REST tarball endpoint with selective
`tar` extraction of just the subpath, drift-checked via the 40-byte
SHA endpoint and ETag-conditional GET. Non-GitHub hosts fall back to
a temp-dir clone-and-copy pattern.

- **AC-4.1**: When the source host is `github.com` (or a configured
  GitHub Enterprise host), niwa fetches via
  `GET /repos/{owner}/{repo}/tarball/{ref}` and pipes the response
  through `tar -xz` filtered to the requested subpath (`<root>/<subpath>/*`),
  so files outside the subpath never persist to disk.
- **AC-4.2**: When `niwa apply` runs against an existing snapshot and
  the source host is GitHub, niwa first checks
  `GET /repos/{owner}/{repo}/commits/{ref}` with
  `Accept: application/vnd.github.sha` and compares the returned 40-byte
  oid against the snapshot's provenance `resolved_commit`. If they
  match, niwa skips the tarball fetch entirely.
- **AC-4.3**: When the SHA-endpoint check returns a non-matching oid
  (or when its `max-age=60` cache has expired), niwa issues the
  tarball request with `If-None-Match: <stored ETag>` and treats a
  304 response as "no change" without re-extracting.
- **AC-4.4**: When the source host is not GitHub (e.g. `gitlab.com`,
  `bitbucket.org`, self-hosted Gitea), niwa falls back to a
  temp-directory `git clone --depth=1` followed by selective copy
  into the snapshot location and full removal of the temp clone.
  The PRD must commit to which non-GitHub hosts ship as first-class
  vs which are documented as "unsupported in v1, falls back to
  clone-and-copy."
- **AC-4.5**: When the GitHub tarball request returns 401 or 403 on
  a private repo, niwa surfaces the underlying API error (rate-limit
  vs scope-missing vs PAT-expired) with a remediation hint pointing
  at the relevant token-scope documentation.
- **AC-4.6**: When the GitHub redirect to `codeload.github.com`
  returns a temporary URL that has expired (5-minute window for
  private repos), niwa retries the original API call to obtain a
  fresh signed URL rather than failing.
- **Negative**: niwa MUST NOT use cone-mode sparse-checkout or
  partial clone as a fetch mechanism (rejected by L2 — they always
  materialize top-level repo files and leak filenames). niwa MUST
  NOT leave the temp-dir clone on disk after the non-GitHub fallback
  path completes successfully.

### Capability 5: Registry schema (parsed mirror fields, lazy migration)

`RegistryEntry` gains parsed mirror fields for the source tuple.
`source_url` stays canonical opaque slug; the parsed fields are
denormalized for fast lookups and for displaying source identity in
`niwa status` without re-parsing.

- **AC-5.1**: When `niwa init` writes a registry entry for a slug-style
  source, the entry contains both `source_url` (the original opaque
  slug as the user typed it) and the parsed mirror tuple
  (`source_host`, `source_owner`, `source_repo`, `source_subpath`,
  `source_ref`). All five mirror fields are populated; subpath and ref
  may be the empty string when not set.
- **AC-5.2**: When `niwa` reads a registry entry written by an older
  binary that lacks the parsed mirror fields, it parses `source_url`
  on read and continues without writing back; the next mutation
  (any registry write) persists the parsed mirror fields (lazy
  migration on next save).
- **AC-5.3**: When the registry contains an entry with parsed mirror
  fields that no longer match `source_url` (e.g. the user
  hand-edited the file and introduced inconsistency), niwa treats
  `source_url` as canonical, re-parses it, and overwrites the
  mirror fields on next save with a stderr warning naming the
  inconsistency.
- **AC-5.4**: When `niwa init` is run for a workspace whose name
  already exists in the registry with a different `source_url`,
  niwa fails with a "registered source mismatch" error naming the
  stored URL and the requested URL. (This is the cutover-friction
  surface that the migration UX in Capability 9 addresses.)
- **Negative**: niwa MUST NOT silently overwrite an existing
  registry entry's `source_url` when `--from` is passed with a
  different value. niwa MUST NOT require manual editing of the
  registry file to introduce subpath sourcing for an existing
  workspace name.

### Capability 6: State schema v3 with `config_source` block

`InstanceState` bumps to schema v3 with a `config_source` block
carrying `(url, host, owner, repo, subpath, ref, resolved_commit,
fetched_at)`. Lazy migration on next save.

- **AC-6.1**: When `niwa create` or `niwa apply` writes an
  `InstanceState` for the first time on a v3-aware binary, the state
  file's `schema_version` is `3` and the `config_source` block is
  populated with all eight fields.
- **AC-6.2**: When `niwa apply` reads a v2 state file (no
  `config_source` block, no `schema_version` field or
  `schema_version = 2`), niwa parses it successfully, populates
  `config_source` from the registry's parsed mirror fields plus the
  current snapshot's provenance marker, and writes a v3 state file
  on next save (lazy migration).
- **AC-6.3**: When `niwa apply` reads a v4-or-later state file (a
  future schema), niwa fails with a "state file written by a newer
  niwa version" error naming the observed schema version and the
  highest schema this binary supports; it does NOT attempt to
  silently down-convert.
- **AC-6.4**: When a v3 state file's `config_source.resolved_commit`
  differs from the snapshot provenance marker's `resolved_commit`
  (state and snapshot disagree on what's materialized), niwa logs a
  diagnostic and treats the snapshot as authoritative; the next
  apply re-syncs state to match.
- **Negative**: niwa MUST NOT lose information during v2→v3
  migration; every field present in the v2 state must round-trip
  into v3. niwa MUST NOT bump `schema_version` on a read that
  doesn't otherwise mutate state.

### Capability 7: Replacement of `.git/`-dependent code paths

`niwa reset`'s `isClonedConfig` and the plaintext-secrets guardrail's
`git remote -v` enumeration both currently use `.git/` presence as
the proxy for "did this config come from a remote." With no `.git/`
in the snapshot, both must read the new source-identity marker
instead.

- **AC-7.1**: When `niwa reset <instance>` runs against a workspace
  whose config came from a remote source (snapshot has a provenance
  marker), niwa identifies it as "cloned" via the marker rather
  than `<configDir>/.git` existence and offers to re-fetch the
  snapshot rather than treating the config as user-authored.
- **AC-7.2**: When the public-repo plaintext-secrets guardrail runs
  against a snapshot, it enumerates remotes by reading the
  provenance marker's `host`/`owner`/`repo` fields rather than by
  invoking `git -C <configDir> remote -v`; the GitHub-public
  pattern-match runs against the marker's host+owner+repo tuple.
- **AC-7.3**: When the snapshot has no provenance marker (e.g. a
  user ran `niwa init` locally without `--from`, or a future
  authoring mode lands the config directly), `niwa reset` and the
  guardrail both treat the config as "user-authored" — same
  behavior as today's "no `.git/` present" path.
- **AC-7.4**: When `--allow-dirty` is passed to any command, niwa
  either (PRD decision) silently ignores the flag for one release
  with a stderr deprecation notice, or hard-rejects with an
  "unrecognized flag" error pointing at the snapshot model.
- **Negative**: niwa MUST NOT shell out to `git` against
  `<workspace>/.niwa/` for any purpose after the snapshot model
  lands. niwa MUST NOT silently disable the public-repo guardrail
  on snapshot configs.

### Capability 8: Backwards compatibility for existing `org/dot-niwa` registries

Standalone `org/dot-niwa` registry entries written by older niwa
versions resolve via the discovery rank-2 marker (root-level
`workspace.toml`) without manual migration.

- **AC-8.1**: When a v3-aware binary runs `niwa apply` against a
  workspace whose registry entry has `source_url = "org/dot-niwa"`
  (no subpath), niwa resolves the source via discovery rank 2
  (root `workspace.toml`), populates the parsed mirror fields with
  `source_subpath = ""`, and the apply succeeds with no user-visible
  migration prompt.
- **AC-8.2**: When the user later wants to migrate the standalone
  `dot-niwa` content into a brain repo's `.niwa/` subpath, niwa
  supports an explicit registry update path (PRD-decided shape: a
  `niwa registry migrate` command per gap #3, or a manual
  `niwa init --from owner/brain-repo:.niwa --reregister` flag) that
  validates the new source resolves to the same workspace name
  before overwriting.
- **AC-8.3**: When a v3-aware binary reads a v2 state file referring
  to a standalone `org/dot-niwa` source and the source no longer
  exists at that URL, niwa fails with the standard "source
  unreachable" error and does NOT prompt the user to migrate
  blindly to the brain-repo subpath.
- **Negative**: niwa MUST NOT require existing standalone
  `dot-niwa` users to take any action after upgrading to the v3-aware
  binary. niwa MUST NOT change `source_url` for an existing entry
  without an explicit user-driven re-registration.

### Capability 9: Migration UX

The PRD must commit to one of two directions for the URL-change cutover
moment (gap #3, L7's "riskiest moment in the brain-repo adoption story"):

**Direction A: lean on existing flow.** The user runs
`niwa init --from owner/brain-repo:.niwa` against an existing
workspace, niwa rejects with "registered source mismatch" (AC-5.4),
the user manually edits the registry or removes-and-re-inits.

**Direction B: dedicated `niwa registry migrate` command.** Detects
URL changes against the registered source, offers a guided flow that
re-fetches via the new source, validates the workspace name matches,
and atomically updates the registry plus snapshot.

Acceptance criteria for both directions:

- **AC-9.1 (both)**: When the user attempts to register a new source
  for an already-registered workspace name, niwa surfaces the
  conflict with a diagnostic naming the stored URL, the requested
  URL, and the recommended next step (Direction A: pointer at
  manual remediation; Direction B: pointer at
  `niwa registry migrate`).
- **AC-9.2 (Direction A only)**: When the user manually
  removes-and-re-inits, the snapshot at `<workspace>/.niwa/` is
  rewritten atomically (Capability 2's atomic-rename) and the v3
  state file's `config_source` block reflects the new source on
  next save.
- **AC-9.3 (Direction B only)**: When `niwa registry migrate
  <workspace> --to owner/brain-repo:.niwa` runs, niwa fetches the
  new source, validates the resulting workspace name matches the
  registered name, and (on match) atomically updates the registry
  + snapshot + state file. On mismatch, niwa fails without
  modifying any of the three.
- **AC-9.4 (Direction B only)**: When the migration partially
  succeeds (e.g., snapshot updated but registry write fails), niwa
  rolls back the snapshot to the pre-migration state. The
  three-file mutation is atomic by best effort (no two-phase
  commit, but ordered so the failure window leaves the system in a
  recoverable state).
- **AC-9.5 (both)**: When the migration target source contains a
  workspace.toml with a different `[workspace].name` than the
  registered name, niwa fails with a "workspace name mismatch"
  error naming both names and refuses to proceed without a
  `--rename` flag (Direction B) or manual registry edit (Direction A).
- **Negative**: niwa MUST NOT silently overwrite the registry on
  source-URL mismatch, regardless of which direction the PRD picks.
  niwa MUST NOT delete the existing snapshot before the new source
  is successfully fetched and validated.

### Open Questions

The following acceptance criteria could not be written cleanly because
the PRD scope leaves the underlying contract open:

1. **Slug delimiter `:` vs `//`** (tension #1 in findings): the AC
   set above assumes `:`. If the PRD picks `//`, AC-1.1, AC-1.4,
   AC-1.6, AC-3.7, AC-9.3 need re-keying with the alternate
   delimiter. The semantic contract is unchanged; only the surface
   syntax changes.
2. **Default-branch ref resolution timing** (gap #1): AC-1.7 papers
   over the open question. Two distinct AC sets exist depending on
   the answer — init-time-pin needs an AC for "registry shows the
   resolved ref, not `HEAD`" and re-resolve-every-apply needs an AC
   for "`niwa status` warns when the upstream default has renamed."
   Pick one, then write the matching AC.
3. **`content_dir` requirement on rank-3 single-file resolution**
   (gap #4): AC-3.3 conditions on a PRD decision. If the PRD makes
   `content_dir` required, the AC is "apply fails when omitted." If
   optional with a default, the AC needs to specify what the default
   is and what content niwa reads.
4. **Multi-host adapter scope at v1** (gap #5 / lead 1): AC-4.4
   defers the host list to the PRD. The PRD must enumerate which
   hosts ship as first-class adapters in v1 (GitLab? Bitbucket?
   Gitea?) and which fall back to clone-and-copy. The AC then
   becomes one entry per first-class host.
5. **Migration UX direction** (gap #3): AC-9.1 through AC-9.5 are
   keyed to both Direction A and Direction B. The PRD picks one;
   the other set drops out.
6. **`--allow-dirty` disposition** (in-scope decision): AC-7.4
   reflects both options. The PRD picks "silently ignore for one
   release" or "hard-remove" and the AC fixes accordingly.
7. **`vault_scope = "@source"` shorthand** (lead 5): not addressed
   in the AC set above because the PRD scope marks it as deferable.
   If the PRD pulls it in, a new capability section is needed.
8. **Snapshot read-only enforcement** (gap #6): AC-2.5 documents
   that manual edits survive until refresh and are then silently
   discarded. If the PRD commits to `chmod -R a-w` enforcement, the
   AC flips to "manual edits fail with EACCES."
9. **`instance.json` placement** (gap #2): the AC set above does
   not specify where the per-instance state file lives relative to
   the snapshot. The PRD should leave this to the design phase but
   confirm the AC contract is "state survives snapshot refresh,"
   regardless of placement.

---

## Lead B: Documentation outline

### New guides

- **`docs/guides/workspace-config-sources.md`** — the deep guide for
  the new sourcing model. Mirrors the structure of
  `docs/guides/vault-integration.md` (~500-600 lines). Sections:
  "What you get" (publishable subpath sources, brain-repo skip,
  no more #72-style wedges), "Quick start" with three flavors
  (standalone `dot-niwa`, brain-repo `.niwa/` subpath, single-file
  `niwa.toml`), "Slug grammar" (delimiter, ref, subpath rules),
  "Discovery rules" (the three-marker table with rank ties and
  conflict errors), "Snapshot model" (no `.git/`, atomic refresh,
  why manual edits don't persist), "Drift detection" (SHA endpoint
  + ETag, when network round-trips happen), "Provenance marker"
  (what's recorded, where it lives — pending design-phase decision),
  "Failure modes" (subpath not found, ambiguous discovery, host
  not supported, snapshot corrupted, migration mismatch). Target
  length: 500-600 lines. Audience: workspace authors and team
  leads who set up the brain-repo or standalone-dot-niwa source.

- **`docs/guides/workspace-config-sources-acceptance-coverage.md`** —
  the AC-to-test mapping, mirroring
  `docs/guides/vault-integration-acceptance-coverage.md`. One row
  per AC from Lead A. Target length: ~250-350 lines. Audience:
  contributors verifying coverage during PR review.

### Existing guides updated

- **`docs/guides/functional-testing.md`** — small additions. The
  `localGitServer` helper section needs a note explaining the v1
  fetch path uses GitHub tarballs, not git-clone, so the
  `file://` bare-repo pattern only exercises the non-GitHub
  fallback. If the PRD decides v1 ships a fakeable HTTP tarball
  server for tests, document the helper alongside `Repo` /
  `ConfigRepo` / `OverlayRepo`. Mention the new `@critical`
  scenarios for snapshot refresh, force-push survival, and
  ambiguous-discovery rejection.

- **`docs/guides/vault-integration.md`** — three small updates.
  (1) The "Public-repo guardrail" section's "What it doesn't do"
  bullet that says "When `git remote -v` produces no output —
  either because the config directory has no `.git` tree…" needs
  to be reworded for the snapshot model: the guardrail now reads
  the provenance marker, so the failure mode is "no provenance
  marker present" (i.e., user-authored config with no remote). (2)
  The "What it does" sentence "Before resolving, niwa enumerates
  every remote from `git remote -v` in the config repo…" must be
  updated to "reads the provenance marker recorded at fetch time."
  (3) Cross-link from the multi-org section to the new sources
  guide for the `vault_scope` interaction with the open question
  about `vault_scope = "@source"` (only if the PRD pulls the
  shorthand in).

- **`docs/guides/one-time-notices.md`** — add a row to the
  "Existing notice keys" table if the PRD commits to a one-time
  notice for the v2→v3 state migration ("your state file was
  upgraded to schema v3").

### README and niwa CLAUDE.md updates

- **`README.md`** — three sections need rewording:
  (1) The "Quick start §2 Create a workspace" section says
  `niwa init my-project` creates `.niwa/workspace.toml`. Confirm
  this still holds for the local-config flow.
  (2) The "Shared workspace configs" section currently says
  "The config repo is cloned as `.niwa/` (a git checkout), so it
  can be updated later." This must change to: "The config source
  is fetched as a snapshot at `.niwa/` (a pure file tree, not a
  git checkout). Updates re-fetch atomically; manual edits inside
  the snapshot don't persist." Add a one-line forward-link to the
  new guide.
  (3) The "Commands" table's `niwa init <name> --from <org/repo>`
  row needs an updated description noting the optional `:subpath`
  and `@ref` syntax. Add a note that `--allow-dirty` (if
  previously documented anywhere) is removed/deprecated.

- **`CLAUDE.md` (niwa-specific)** — add the new guide to the
  "Contributor Guides" list:
  `docs/guides/workspace-config-sources.md — config source
  resolution, snapshot model, and discovery conventions`. No
  other change (the file is short and intentional).

- **Top-level workspace `CLAUDE.md`** — no change needed. The
  workspace context already says nothing specific about config
  sourcing.

### Migration notes

**Recommendation: yes, the PRD must commit to a one-page migration
guide**, but its location depends on the migration UX direction
(Capability 9 above):

- **If PRD picks Direction A (lean on existing flow):** add a
  `## Migrating from standalone dot-niwa to a brain-repo subpath`
  section inside the new
  `docs/guides/workspace-config-sources.md` guide. Six steps:
  (1) push the existing `dot-niwa` content into your brain repo
  under `.niwa/`, (2) on each developer machine, run
  `niwa registry remove <name>`, (3) re-init with the new source
  slug, (4) run `niwa apply`, (5) verify with `niwa status`, (6)
  retire the old standalone `dot-niwa` repo.

- **If PRD picks Direction B (`niwa registry migrate` command):**
  same content but a single command replaces steps 2-4. Worth a
  separate `docs/guides/migrating-to-brain-repo-sources.md`
  one-page guide so the existing `dot-niwa` user community has a
  bookmark-able URL. Target length: 100-150 lines.

Either way, the migration story needs a "what about the standalone
repo's `README.md` / `LICENSE` / `.gitignore`?" section (gap #7) —
these don't migrate cleanly. Recommend leaving them in the
standalone repo as historical context with a pointer to the new
brain-repo location.

### Examples and fixtures

The functional-test fixtures need new variants:

- **`localGitServer.ConfigRepo`** currently creates a bare repo
  with `workspace.toml` at root. Add either a sibling helper
  `localGitServer.BrainRepo(name, subpath, toml, otherFiles...)`
  that creates a bare repo with the workspace config at an
  arbitrary subpath plus other top-level files, or extend
  `ConfigRepo` with optional `subpath` and `extras` parameters.
- **New test fixture: `tarball-fake-server`**. To exercise the
  GitHub tarball fetch path without reaching `api.github.com`, the
  test suite needs an HTTP test server that serves
  `/repos/{owner}/{repo}/tarball/{ref}` and
  `/repos/{owner}/{repo}/commits/{ref}` against a synthetic repo
  state. This is large enough to deserve its own helper file in
  `test/functional/`. The PRD should note this fixture as required
  rather than leaving it to design.
- **New `@critical` scenarios** in `test/functional/features/`:
  one for snapshot refresh against a force-pushed source (covers
  AC-2.2 and the #72 regression), one for ambiguous-discovery
  rejection (AC-3.4), one for explicit-subpath bypass (AC-3.7),
  one for v2-to-v3 state migration (AC-6.2), one for the migration
  UX direction the PRD picks (AC-9.1 through AC-9.5).
- **A `test/functional/features/workspace-config-sources.feature`
  file** to host the above scenarios, keeping `critical-path.feature`
  focused on the original init→create→apply happy path.
- **Repo-internal sample**: the `niwa-test/` directory in the repo
  root may need a sample `niwa.toml`-only fixture if the PRD
  commits to first-class single-file sourcing.
- **Recipe / install-script implications**: none — install.sh and
  the tsuku recipe ship a binary, not a config; they're untouched.

### Open Questions

1. Should the new guide live as `workspace-config-sources.md` or
   under a more user-facing name like `config-sources.md` /
   `where-config-comes-from.md`? Naming sets the discoverability
   ceiling; recommend the long-form for searchability and
   consistency with `vault-integration.md`.
2. If the PRD picks Direction A migration UX (no dedicated
   command), is a separate one-page migration guide overkill for a
   six-step procedure? Could fold into the main guide.
3. Should `docs/prds/PRD-config-distribution.md` cross-link to the
   new PRD as a related-work pointer? Both PRDs touch the
   `.niwa/` shape from different angles.
4. The existing `docs/designs/current/DESIGN-config-distribution.md`
   describes the current `.niwa/` layout. Does the design doc need
   a "v2: snapshot model" addendum, or does the new design doc
   spawned by this PRD obsolete the relevant parts? Defer to the
   design phase; note the cross-reference need in the PRD.

---

## Summary

The most important acceptance criterion is AC-2.2 (snapshot refresh
after force-push) — it's the one-line statement of the #72 fix and
the entire snapshot model exists to satisfy it. The biggest
documentation gap is the absence of any current guide describing
where workspace config comes from at all (the README treats it as a
subordinate detail of "shared workspace configs"); the new
`workspace-config-sources.md` guide must elevate this to a
first-class concept. The biggest open question carrying through to
the AC set is the migration UX direction (Capability 9) — without it,
roughly five acceptance criteria, the migration-notes commitment, and
the `niwa registry migrate` fixture all branch on the answer.
