---
status: Draft
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

Draft

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
   planning docs, and Claude config (e.g., `tsukumogami/vision`,
   `codespar/codespar-web`). The workspace config naturally belongs as a
   subdirectory of the brain repo — but niwa today demands a whole
   separate repo at the slug URL. Maintainers either duplicate brain-repo
   content into a standalone `dot-niwa` (creating drift) or skip niwa
   entirely.

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
  user action after upgrading.
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

These are the four scenarios v1 must support directly. Each is written
from the developer's first-person view, present tense.

### Story 1: First-time subpath adoption

A developer at an org with a brain repo (e.g., `tsukumogami/vision`)
creates a new workspace. They run
`niwa init --from tsukumogami/vision:.niwa my-workspace`. niwa parses
the slug, fetches the `.niwa/` subpath of the brain repo's default
branch as a snapshot, materializes it at `<cwd>/my-workspace/.niwa/`
(a pure file tree, no `.git/`), and registers the workspace. Subsequent
`niwa apply` runs refresh the snapshot atomically. The developer never
thinks to edit `.niwa/` in place because there's no `.git/` to suggest
they could.

### Story 2: Migrating from standalone `dot-niwa`

A developer has an existing workspace pointing at
`tsukumogami/dot-niwa`. The maintainer announces the config has moved
into the brain repo at `tsukumogami/vision:.niwa`. The developer runs
`niwa config set global tsukumogami/vision` (convention discovery
resolves the subpath to `.niwa` automatically) and then `niwa apply`.
niwa detects the source URL changed, refuses to proceed, prints a
diagnostic naming both URLs and instructing the developer to inspect
`<workspace>/.niwa/` for pending edits before re-running with `--force`.
After `niwa apply --force`, niwa atomically replaces the snapshot.
From this point forward, edits to `.niwa/` are never silently lost
because there's no working tree to commit into.

### Story 3: Brain-repo maintainer publishing

A maintainer of `tsukumogami/vision` decides to host the workspace
config inside the brain repo. They `git mv` the dot-niwa contents into
`vision/.niwa/`, drop the standalone repo's housekeeping files, commit,
and push. They post a one-line announcement: "the workspace config now
lives at `tsukumogami/vision:.niwa` — run
`niwa config set global tsukumogami/vision` to switch." Each consumer's
switch is independent; the standalone `dot-niwa` repo can stay in place
indefinitely for graceful overlap. No synchronized cutover.

### Story 4: Apply after brain-repo force-push

A developer's workspace points at `tsukumogami/vision:.niwa`. The
brain-repo maintainer force-pushes the default branch to clean up
history. The developer runs `niwa apply`. niwa fetches a fresh
representation of the new default-branch tip, computes the new resolved
commit, sees it differs from the snapshot's recorded commit, and
atomically replaces the snapshot. No merge conflicts, no fast-forward
errors, no manual reconciliation. `niwa status` shows the new commit
and the latest fetched-at timestamp. Issue #72 is invisible.

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
- **R3.** niwa MUST treat the slug parser as strict: empty subpath after
  a colon, malformed ordering of separators, embedded whitespace, and
  multiple `:` or `@` separators MUST be rejected at parse time with a
  diagnostic naming the offending input.
- **R4.** niwa MUST accept a subpath that resolves to a regular file
  (not a directory) and treat the parent directory as the config
  directory.

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
  `content_dir` in this case MUST fail with a diagnostic. The explicit
  value `content_dir = "."` is valid (opt-in to "the whole brain repo
  is content").
- **R9.** Explicit subpath in the slug MUST bypass discovery entirely.
  If the explicit subpath does not contain a `workspace.toml`, niwa
  MUST fail without falling back to discovery.

**Snapshot materialization**

- **R10.** niwa MUST materialize the workspace config as a pure file
  tree at `<workspace>/.niwa/` containing only the files from the
  resolved source subpath plus a single provenance marker. No `.git/`
  directory MUST exist in the materialized snapshot.
- **R11.** The provenance marker MUST record at minimum: the source URL,
  parsed source tuple, resolved commit oid, fetched-at timestamp, and
  fetch mechanism used.
- **R12.** Snapshot refresh MUST be atomic: niwa writes the new
  materialization to a sibling location and renames it into place on
  success. A failed refresh MUST leave the existing snapshot intact.
- **R13.** The same snapshot model MUST apply symmetrically to the team
  config clone, the personal overlay clone, and the workspace overlay
  clone. All three must share the no-`.git/`, atomic-refresh, marker-
  bearing posture.

**Fetch mechanisms**

- **R14.** When the source host is `github.com`, niwa MUST use the
  GitHub REST tarball endpoint with selective `tar` extraction filtered
  to the requested subpath. Files outside the subpath MUST never persist
  to disk.
- **R15.** When the source host is anything other than `github.com`
  (including GitHub Enterprise Server, GitLab, Bitbucket, Gitea, and
  `file://` URLs), niwa MUST use a temp-directory `git clone --depth=1`
  fallback followed by a copy of the requested subpath into the
  snapshot location. The temporary clone MUST be removed after copy.
- **R16.** Drift detection on the GitHub path MUST use the 40-byte
  `commits/{ref}` SHA endpoint with `Accept: application/vnd.github.sha`
  and `If-None-Match` ETag-conditional GETs against the tarball
  endpoint. Drift detection on the fallback path MUST use
  `git ls-remote <url> <ref>`.

**Default branch and ref resolution**

- **R17.** When the slug omits `@ref`, niwa MUST re-resolve the source
  repo's default branch on every `niwa apply` (not pin at init time).
  niwa MUST record the latest resolved commit oid in
  `InstanceState.config_source.resolved_commit` after each apply.
- **R18.** `niwa status` MUST distinguish a pinned ref from an
  auto-resolved default branch in its detail-view output (e.g., the
  string "(default branch)" appended when no ref was specified).
- **R19.** When a source URL omits `@ref` and the default branch
  cannot be re-resolved (network unreachable), niwa MUST continue with
  the cached snapshot and emit a `warning:`-prefixed notice naming the
  source URL, the cached commit oid, and the cached fetched-at
  timestamp. Apply MUST NOT abort.

**Registry and state schema**

- **R20.** niwa MUST persist registry entries with the parsed mirror
  fields (R2) populated. Existing entries written by older binaries
  (no mirror fields) MUST parse on read and MUST be lazily upgraded by
  populating the mirror fields on the next registry write.
- **R21.** niwa MUST bump the per-instance state schema to v3 and add a
  `config_source` block carrying `(url, host, owner, repo, subpath,
  ref, resolved_commit, fetched_at)`. v2 state files MUST load
  successfully and MUST be lazily upgraded to v3 on the next save.
- **R22.** When niwa reads a state file with `schema_version` greater
  than the highest version this binary supports, it MUST fail with a
  diagnostic naming the observed and supported versions. niwa MUST NOT
  attempt to silently down-convert.

**Migration from working tree to snapshot**

- **R23.** When `niwa apply` runs against a workspace whose registry
  source URL has changed and whose `<workspace>/.niwa/` is the legacy
  working-tree form (has `.git/` present), niwa MUST refuse to proceed
  without `--force`. The error MUST name the old and new source URLs
  and MUST suggest an inspection command (e.g., `cd .niwa && git
  status`) before re-running with `--force`.
- **R24.** When `--force` is passed (or `<workspace>/.niwa/` is already
  a snapshot), niwa MUST atomically replace the materialization from
  the new source per R12.

**Replacement of `.git/`-dependent paths**

- **R25.** `niwa reset`'s "is this config from a remote" check MUST read
  the snapshot provenance marker rather than `.git/` presence. When the
  marker is present, niwa MUST treat the config as cloned and offer
  re-fetch as the recovery path. When absent, niwa MUST treat the
  config as user-authored (matching today's local-only-workspace
  semantics).
- **R26.** The plaintext-secrets public-repo guardrail MUST enumerate
  remotes by reading the snapshot provenance marker's host/owner/repo
  fields rather than by invoking `git -C <configDir> remote -v`. The
  GitHub-public pattern match MUST run against the marker tuple.
- **R27.** The `--allow-dirty` flag MUST be silently accepted for one
  release with a stderr deprecation notice ("--allow-dirty is no longer
  meaningful under the snapshot model and will be removed in a future
  release"). It MUST be hard-removed in a subsequent release.

**Backwards compatibility**

- **R28.** Existing registries with `source_url = "org/dot-niwa"` (no
  subpath) MUST continue to resolve via discovery rank 2 (root
  `workspace.toml`) without user action. Registry behavior, state
  behavior, and on-disk content (subject to the snapshot conversion
  in R23/R24) MUST be unchanged.
- **R29.** No existing user MUST be required to take any action after
  upgrading to the v3-aware niwa binary. The first `niwa apply` after
  upgrade triggers the lazy migrations (R20, R21) automatically.

### Non-functional requirements

- **R30.** A first-time GitHub fetch SHOULD complete in under 5 seconds
  for a typical config-sized subpath (≤1 MB compressed) on a normal
  broadband connection. The 40-byte SHA-endpoint drift check on
  subsequent applies SHOULD complete in under 500 ms.
- **R31.** A snapshot refresh on a source whose commit oid has not
  changed MUST NOT incur the cost of re-extracting the tarball.
- **R32.** Files outside the resolved subpath MUST NOT persist to disk
  on the GitHub path, even temporarily during materialization.
- **R33.** The fallback path's temporary clone directory MUST be cleaned
  up on success and on most failure paths (process kill is the
  exception; document the resulting cleanup ritual).
- **R34.** The provenance marker MUST be readable with no specialized
  tooling — a future contributor inspecting `<workspace>/.niwa/`
  manually must be able to identify origin, ref, and fetched-at without
  running niwa.

## Acceptance Criteria

The criteria below are organized by capability. Each is binary
pass/fail and verifiable by a developer who didn't write the PRD.

### AC: Subpath sourcing (verifies R1-R4)

- [ ] `niwa init <name> --from owner/repo:path/to/config@v1.2.0` parses
  the slug into the four-tuple and stores all mirror fields plus the
  opaque slug in the registry.
- [ ] `niwa init <name> --from owner/repo` (bare slug) triggers
  discovery and persists the resolved subpath in the registry.
- [ ] `niwa init <name> --from owner/repo:` (empty subpath after colon)
  fails with a parse error naming the empty subpath.
- [ ] `niwa init <name> --from owner/repo:path/to/niwa.toml` (subpath
  resolves to a file) treats the parent directory as the config dir
  and validates the file as the workspace config.
- [ ] `niwa init <name> --from owner/repo:nonexistent` fails after
  fetch with a "subpath not found" diagnostic naming the subpath, the
  resolved commit oid, and the source slug. The on-disk snapshot is
  not modified.

### AC: Convention-based discovery (verifies R5-R9)

- [ ] When the source repo contains only `.niwa/workspace.toml`,
  discovery resolves `source_subpath = ".niwa/"`.
- [ ] When the source repo contains only a root `workspace.toml`,
  discovery resolves `source_subpath = ""` and the existing
  standalone-`dot-niwa` workflow continues to work.
- [ ] When the source repo contains only a root `niwa.toml`,
  discovery resolves `source_subpath = ""` and validates the file as
  the workspace config; a `niwa.toml` without `[workspace] content_dir`
  fails apply with a targeted diagnostic.
- [ ] When the source repo contains both `.niwa/workspace.toml` and a
  root `workspace.toml`, niwa fails with an "ambiguous niwa config"
  error naming both files.
- [ ] When the source repo contains a `.niwa/` directory but no
  `.niwa/workspace.toml` inside it, discovery skips rank 1 and tries
  ranks 2 and 3.
- [ ] When the source repo contains none of the three markers, niwa
  fails with a discovery error naming all three accepted markers.
- [ ] An explicit subpath bypasses discovery; a missing
  `workspace.toml` in the explicit subpath fails immediately rather
  than falling back to discovery.

### AC: Snapshot materialization (verifies R10-R13)

- [ ] After `niwa apply` (first run), `<workspace>/.niwa/` contains the
  source subpath's files plus a provenance marker; no `.git/`
  directory is present; `git status` inside the directory exits
  with "not a git repository".
- [ ] After `niwa apply` against a snapshot whose source has been
  force-pushed, niwa fetches the new commit oid and re-materializes
  the snapshot atomically. The previous snapshot is removed only
  after the rename completes. niwa does NOT fail with `fatal: Not
  possible to fast-forward, aborting` (issue #72 regression).
- [ ] After a snapshot refresh interrupted partway (network cut,
  tarball truncation, disk-full during extraction), the previous
  snapshot at `<workspace>/.niwa/` is intact.
- [ ] When the source's resolved commit oid matches the provenance
  marker's `resolved_commit`, niwa skips re-extraction and updates
  only the `fetched_at` timestamp.
- [ ] The same snapshot posture applies to the personal overlay clone
  and the workspace overlay clone (no `.git/`, atomic refresh,
  provenance marker present in each).

### AC: GitHub tarball fetch (verifies R14-R16)

- [ ] On `niwa apply` against a `github.com` source, no files outside
  the resolved subpath are present on disk after materialization.
- [ ] The drift check against `commits/{ref}` returns the cached oid
  (matching the snapshot provenance) without invoking the tarball
  endpoint.
- [ ] When the SHA endpoint reports a different oid, niwa issues the
  tarball request with `If-None-Match: <stored ETag>`; a 304 response
  is treated as "no change" without re-extracting.
- [ ] On a private GitHub repo, a 401 or 403 from the tarball or SHA
  endpoint surfaces an error naming the underlying API status with a
  remediation hint pointing at PAT scope documentation.

### AC: Non-GitHub fallback (verifies R15)

- [ ] On `niwa apply` against a non-GitHub URL (GitLab, Bitbucket,
  Gitea, GHE, `file://`), niwa runs `git clone --depth=1` into a
  temporary directory, copies the requested subpath into
  `<workspace>/.niwa/`, and removes the temporary directory.
- [ ] No `.git/` directory persists in `<workspace>/.niwa/` after the
  fallback path completes.

### AC: Default branch and ref resolution (verifies R17-R19)

- [ ] After `niwa apply` against a slug with no `@ref`, the latest
  commit on the remote default branch is recorded in
  `instance.json` under `config_source.resolved_commit`.
- [ ] `niwa status` against a workspace with a ref-less slug shows
  the source line with an explicit "(default branch)" annotation.
- [ ] `niwa status` against a workspace with `@v1.0` shows the
  pinned ref instead of "(default branch)".
- [ ] When the network is unreachable on `niwa apply` against a
  ref-less slug, apply continues with the cached snapshot and emits
  a warning naming the source URL, the cached commit oid, and the
  cached `fetched_at`. Apply exit code is 0.

### AC: Registry and state schema (verifies R20-R22)

- [ ] A registry entry written by an older binary (no mirror fields)
  loads successfully; the next registry mutation persists the
  parsed mirror fields.
- [ ] An `InstanceState` written under schema v2 loads successfully
  on a v3-aware binary; the next `niwa apply` save writes a v3 file
  with `config_source` populated.
- [ ] An `InstanceState` with `schema_version` greater than the
  highest supported version fails to load with a diagnostic naming
  both versions; niwa does not attempt down-conversion.

### AC: Migration from working tree to snapshot (verifies R23-R24)

- [ ] `niwa apply` against a workspace whose registry source URL
  changed AND whose `<workspace>/.niwa/` is a working tree exits
  non-zero with a diagnostic naming both URLs and an inspection
  command.
- [ ] `niwa apply --force` against the same workspace replaces the
  working tree with a snapshot atomically.
- [ ] `niwa apply` against a workspace whose `<workspace>/.niwa/` is
  already a snapshot does not require `--force` even when the
  registry source URL changed.

### AC: Replacement of `.git/`-dependent paths (verifies R25-R27)

- [ ] `niwa reset <instance>` against a workspace with a snapshot
  provenance marker treats the config as cloned and offers re-fetch
  as the recovery path.
- [ ] The plaintext-secrets public-repo guardrail enumerates remotes
  from the provenance marker; the GitHub-public pattern match runs
  against the marker tuple. The guardrail does not silently disable
  on snapshot configs.
- [ ] `niwa apply --allow-dirty` succeeds with a stderr deprecation
  notice ("--allow-dirty is no longer meaningful under the snapshot
  model and will be removed in a future release"). The notice is
  printed once per process invocation.

### AC: Backwards compatibility (verifies R28-R29)

- [ ] An existing workspace with `source_url = "org/dot-niwa"`
  resolves and applies successfully on the v3-aware binary with no
  user action other than triggering the migration flow if the
  on-disk `.niwa/` is still a working tree (R23).
- [ ] No upgrade-time prompt fires for users whose registries already
  point at whole-repo standalone sources.

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
  cache). The cache layer is a candidate for a v1.x follow-up when
  multi-workspace dedup demand emerges.
- **Per-host adapters for GitLab, Bitbucket, Gitea, and GitHub
  Enterprise Server.** v1 ships GitHub-tarball-fast-path plus the
  git-clone fallback; per-host adapters are pure performance
  optimizations deferred to follow-ups.
- **`vault_scope = "@source"` shorthand.** Real use case but a
  workable manual answer exists. Defer to v1.1 when usage data
  validates the right expansion scheme.
- **Provenance marker file format and on-disk location.** The PRD
  commits to the contract (R11, R34) and leaves the file format,
  filename, and exact placement to the design phase.
- **`instance.json` placement relative to the snapshot.** The state
  file's location is a downstream design-doc concern. The PRD
  contract is "state survives snapshot refresh."
- **Read-only enforcement of the snapshot directory** (e.g., `chmod
  -R a-w`). The model is "manual edits don't persist after refresh";
  hard-enforcement is an opt-in for a follow-up if the soft contract
  proves insufficient.
- **Dedicated `niwa registry migrate` command.** The migration UX
  (R23-R24) leans on the existing `--force` pattern. A guided
  command is a v1.x candidate if the current friction proves
  problematic in real adoption.
- **`niwa registry retarget` and `niwa registry refetch` commands.**
  Mentioned in some failure-mode narratives during research; v1
  remediation is "edit the registry by hand and run apply" for these
  cases.

## Known Limitations

- **Slug repo-root sentinel.** When discovery is ambiguous (more than
  one marker at the source repo root), there's no consumer-side way to
  ask explicitly for "repo root" via the slug — `org/repo:` is rejected
  as an empty subpath. The only resolution is for the brain-repo
  maintainer to remove one of the markers. This is documented in the
  guide. (Alternative sentinel syntax was considered and rejected: the
  bare-slug form already runs discovery, so an explicit "repo root"
  sentinel adds surface without a real use case.)
- **First-fetch bandwidth on the GitHub path.** The tarball endpoint
  delivers the entire repo's gzipped bytes even when only a small
  subpath persists. For brain repos with very large histories or
  binary assets, the first fetch may take noticeably longer than the
  resulting snapshot's size suggests. Subsequent applies are cheap
  (40-byte SHA endpoint or 304 ETag). The fallback path has the same
  characteristic via `git clone --depth=1`. Documented as expected
  behavior.
- **Manual edits inside the snapshot are silently discarded on
  refresh.** This is by design — the snapshot has no working-tree
  semantics — but is a behavior change for users who relied on
  `--allow-dirty` for local testing. The guide must call this out.
- **GitHub Enterprise Server uses the fallback path.** GHE supports the
  same tarball API shape, but v1 ships only `github.com` as
  first-class (symmetric with the existing plaintext-secrets
  guardrail's `v1 scope is strictly github.com`). GHE users get
  correct snapshots via the fallback and can adopt the GHE adapter
  when it ships in v1.x.
- **Snapshot integrity check granularity.** v1 treats "provenance
  marker present and parseable" as integrity confirmation. Tampered
  but-syntactically-valid snapshots are not detected. Content-hash
  validation is a candidate for a follow-up if real abuse emerges.
- **Submodules and LFS in the source.** Tarball-based fetches do not
  expand submodules or resolve LFS pointers. Workspace configs rarely
  use either; the limitation is documented and not addressed in v1.

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
hazard.

### Decision: re-resolve default branch on every apply (not pin at init)

**Decided**: ref-less slugs trigger fresh default-branch resolution on
each `niwa apply`; the resolved commit oid is recorded in state.
**Alternatives**: pin the resolved branch at `niwa init` time and
require explicit opt-in to HEAD-tracking. **Reasoning**: the dominant
mental model for users typing `niwa init --from owner/repo` is the
`git clone` model — track the default. Pinning at init time creates
silent staleness when maintainers rename the default branch and forces
users to learn refs as a first-class concept. The new tarball fetcher
resolves `HEAD` server-side, so re-resolution is cheap (40-byte SHA
endpoint).

### Decision: migration UX leans on `--force`, no new command

**Decided**: `niwa apply` detects URL changes and refuses without
`--force`; the error message is the migration guide. **Alternatives**:
dedicated `niwa registry migrate` command with interactive
confirmation. **Reasoning**: niwa's existing destructive-operation
pattern (`destroy --force`, `reset --force`) is non-interactive and
already familiar to users. Adding interactive prompts would be the
first such pattern in the CLI and would split the discovery surface
(the new command is invisible to users who edit the registry by hand).
The migration friction this leaves is bounded; if real adoption shows
the friction is intolerable, a `niwa registry migrate` command remains
a straightforward follow-up.

### Decision: GitHub-first-class + git-clone fallback (no per-host adapters in v1)

**Decided**: GitHub uses the REST tarball + selective `tar` extraction;
all other hosts (including GHE) use a temp-dir `git clone --depth=1`
fallback. **Alternatives**: ship per-host adapters for GitLab,
Bitbucket, Gitea, and GHE in v1. **Reasoning**: niwa already has a
host-agnostic clone code path; the fallback covers every git-reachable
host on day one. Each per-host adapter brings its own auth flow, URL
parser, response-code handling, and rate-limit budget — multi-week
investments per adapter that would significantly delay v1. The
fallback path's main cost (full-repo first-fetch) is acceptable for
niwa's expected payload sizes. Adapters become pure performance
follow-ups.

### Decision: `content_dir` required when discovery resolves via `niwa.toml`

**Decided**: when discovery resolves to repo root via the rank-3
`niwa.toml` convention, `[workspace] content_dir` MUST be set
explicitly; the value `"."` is a valid opt-in to "the whole brain repo
is content". **Alternatives**: leave `content_dir` optional with the
existing default of `"."` (silent). **Reasoning**: today's
`content_dir = "."` default silently turns the entire config dir into
the content root. In a brain repo with `niwa.toml` at root, that means
niwa would silently pick up `docs/`, `src/`, top-level `CLAUDE.md`, and
any directory named `repos/` — files written for human readers, not
for niwa templating. Making `content_dir` required for this specific
case forces brain-repo authors to make a conscious choice about which
subdirectory is content. Existing standalone-`dot-niwa` users
(rank 2 discovery) keep `content_dir` optional.

### Decision: `--allow-dirty` deprecated, not hard-removed

**Decided**: `--allow-dirty` is silently accepted for one release with
a stderr deprecation notice; hard-removed in v1.1. **Alternatives**:
hard-remove immediately. **Reasoning**: `--allow-dirty` becomes
meaningless under the snapshot model (no working tree to be dirty). But
users may have it baked into scripts or aliases. A one-release
deprecation cycle gives them time to discover and update. The notice
is printed once per process invocation to avoid noise.

### Decision: offline behavior on default-branch resolution failure

**Decided**: when the default branch can't be re-resolved (network
unreachable), apply continues with the cached snapshot and emits a
loud `warning:`-prefixed notice naming the source URL, cached commit
oid, and cached `fetched_at`. **Alternatives**: hard-error on
network unreachable. **Reasoning**: the cached snapshot is on disk
and still valid. Punishing users for ephemeral network loss when the
existing snapshot satisfies the apply is the wrong pose — it matches
the existing `SyncRepo` "fetch-failed → continue informationally"
precedent. A `--strict-refresh` flag for users who want
fail-on-stale (e.g., CI environments) is a follow-up candidate.

## Open Questions

(None remaining — all open questions surfaced during exploration and
Phase 2 discovery were resolved during drafting per the
research-first protocol. See the Decisions and Trade-offs section.)
