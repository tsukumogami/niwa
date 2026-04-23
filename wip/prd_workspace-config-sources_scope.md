# /prd Scope: workspace-config-sources

## Problem Statement

niwa today materializes git-hosted workspace configuration as a working
tree at `<workspace>/.niwa/`, synced with `git pull --ff-only`, and
assumes the whole repo at the URL is the config — which (a) wedges
unrecoverably when the remote rewrites history (issue
[#72](https://github.com/tsukumogami/niwa/issues/72)), (b) forces a
separate `dot-niwa` repo even when the natural home is a subdirectory
inside an existing "brain" repo (orgs with one private + multiple public
repos already centralize planning, conventions, and `CLAUDE.md` in a
brain repo; duplicating that into a standalone dot-niwa creates editing
friction and content drift), and (c) silently invites the
working-tree edits the model doesn't expect. The PRD must establish the
v1 contract for a unified subpath-aware, snapshot-based config-sourcing
pattern that fixes #72 as a byproduct, lets brain-repo users skip the
standalone dot-niwa, and keeps the existing `org/dot-niwa` workflow
backwards-compatible as the degenerate `subpath = "/"` case.

## Initial Scope

### In Scope

- **Unified subpath source model**: the workspace config can come from a
  subdirectory of any repo. "Whole repo" is the degenerate `subpath = "/"`
  case of the same mechanism. Same source model applies symmetrically to
  the team config, personal overlay, and workspace overlay.
- **Disposable snapshot materialization**: the on-disk artifact at
  `<workspace>/.niwa/` is a pure file tree (no `.git/`) with a
  provenance marker recording origin URL, ref, resolved commit oid,
  fetched-at timestamp, and fetch mechanism. Refresh is atomic
  (sibling-rename) and never merges. Issue #72 falls out as a byproduct.
- **Convention-based discovery**: when sourcing from a slug like
  `owner/repo` (no explicit subpath), niwa probes a fixed marker
  vocabulary at the repo root and resolves to the first match. Hard-error
  on ambiguity. Explicit slug subpath bypasses discovery.
- **GitHub-tarball-primary fetch**: GitHub repos are sourced via the REST
  tarball endpoint with selective `tar` extraction of just the subpath,
  drift-checked via the 40-byte SHA endpoint. Non-GitHub hosts fall back
  to a temp-dir clone-and-copy pattern. PRD must commit to which hosts
  are first-class at v1.
- **Registry and state schema evolution**: registry gains parsed mirror
  fields for the source tuple `(host, owner, repo, subpath, ref)`;
  `InstanceState` bumps to schema v3 with a `config_source` block
  carrying the resolved commit oid and fetched-at timestamp. Lazy
  migration on next save.
- **Replace `.git/`-dependent code paths**: `niwa reset`'s
  `isClonedConfig` and the plaintext-secrets guardrail's `git remote -v`
  enumeration both currently use `.git/` presence as the proxy for "did
  this config come from a remote." They must read the new
  source-identity marker instead.
- **Drop `--allow-dirty`**: meaningless once `.niwa/` is a snapshot, not
  a working tree. PRD decides whether to silently ignore for back-compat
  or hard-remove.
- **Backwards compatibility for existing standalone `org/dot-niwa`
  registries**: their workspace.toml at root resolves via the discovery
  rank-2 marker, no manual migration required.

### Out of Scope

- **Workspace.toml schema redesign** beyond what's needed to express
  subpath sourcing or to enforce the safety bounds on a brain-repo `niwa.toml`
  (e.g., requiring `content_dir` when discovery resolves to repo root).
  Revisiting `[claude]`, `[env]`, `[files]` block shapes is not in scope.
- **Vault provider sourcing**: already designed; not affected by this work.
- **Apply-pipeline behavior after the snapshot is materialized**: every
  materializer, validator, hook discovery, env discovery, and content
  reader stays unchanged.
- **Multi-tenant or hosted niwa scenarios**: out of v1.
- **Telemetry source-redaction design**: no telemetry pipeline exists
  today; document the redaction principle for future use but don't
  build it.
- **Multi-workspace shared cache (content-addressed dedup)**: defer to
  a v1.1 follow-up. v1 ships snapshots directly at
  `<workspace>/.niwa/`; the cache layer can land later when
  multi-workspace dedup demand emerges.

## Research Leads

The exploration's accumulated findings answer most of the "how" questions
this PRD will lean on, so the PRD's own research phase can focus on the
v1 *boundary* questions and the user-facing contract:

1. **What's the v1 host coverage commitment?**: GitHub-tarball + git-clone
   fallback for everything else, vs first-class adapters for GitLab /
   Bitbucket / Gitea at v1. Lead-partial-fetch-mechanisms surfaced this
   as the dominant open question; the PRD must state which.

2. **Migration cutover ergonomics**: rely on the existing #72-style
   "git fast-forward fails, user reconciles" path (which the redesign
   itself eliminates), or commit to a guided `niwa registry migrate`
   command that detects URL changes and handles the working-tree →
   snapshot transition gracefully. Lead-example-walkthroughs surfaced
   this as the riskiest moment in the brain-repo adoption story.

3. **Default-branch ref resolution timing**: ref-less slugs (the common
   case today) — pin the resolved default branch at `niwa init` time
   (registry shows stable identity, but missed remote-default-rename;
   user opts back into HEAD-tracking explicitly), or re-resolve every
   `niwa apply` (matches today's behavior, but `niwa status` shows a
   moving target). Lead-identity-and-state surfaced this as the
   biggest open contract question.

4. **`niwa.toml` `content_dir` requirement**: when discovery resolves
   to repo root via the one-file convention, should `content_dir` be
   *required* (forces the brain-repo author to declare which subdir is
   content, prevents niwa from accidentally reading random brain-repo
   files) or optional with a default? Lead-discovery-conventions
   leaned toward required.

5. **`vault_scope = "@source"` shorthand**: forward-looking ergonomic
   for "monorepo of teams under one brain" setups where every subpath
   workspace should share the same overlay scope. Lead-identity-and-state
   suggested deferring; the PRD should confirm or pull in.

6. **Acceptance criteria framing**: the PRD must turn the round-1
   convergence list (snapshot model, three-marker discovery, etc.) into
   testable acceptance criteria — what does a passing
   `niwa init --from owner/brain-repo` look like, what does
   `niwa apply` after a remote force-push look like, what does
   `niwa status` show for a subpath source.

## Coverage Notes

The exploration's round-1 findings are dense and decision-rich, but the
PRD process will need to address gaps the exploration did not:

- **User stories**: the exploration produced architectural decisions but
  not user-narrative stories. The PRD should frame the user perspective
  for: a developer adopting subpath-sourcing for the first time; a
  developer migrating from `org/dot-niwa` to `org/brain-repo:.niwa`;
  a brain-repo maintainer publishing a workspace config; a
  consumer running `niwa apply` after the brain repo's default branch
  is force-pushed.
- **Acceptance criteria**: round 1 produced converged decisions but
  none were phrased as testable contract statements. The PRD must
  elevate them into pass/fail acceptance criteria.
- **Edge-case behavior contracts**: "what does niwa do when…" questions
  the exploration didn't enumerate exhaustively — empty subpath
  (`org/repo:`), subpath that exists but contains no `workspace.toml`,
  subpath that resolves to a file (not a directory), default branch
  renamed remotely, repo renamed mid-flight, very large brain repos
  whose tarball is slow to fetch.
- **Failure-mode narratives**: stating the *intended* error message and
  remediation for each major failure category (subpath not found,
  ambiguous discovery, host adapter not implemented, network
  unreachable, snapshot corrupted on disk).
- **Documentation outline**: this redesign reshapes how users describe
  their config sources; the PRD should commit to which docs change
  (`docs/guides/`, `README.md`, examples) and at what level of detail.

## Decisions from Exploration

The following are settled by round-1 research and should be carried into
the PRD as constraints, not re-debated:

- **Snapshot model is the direction.** No working tree at the
  materialized snapshot. Pure file tree with a provenance marker; no
  `.git/` directory. (Convergent across L1, L2, L3, L5, L6, L7.)
- **`.niwa/` at brain-repo root is the placement convention.**
  `niwa.toml`-only is rejected for non-toy configs (single TOML file
  cannot host content tree, env file, hooks, extensions).
  `dot-niwa/`-as-dirname is rejected (reserve `dot-niwa` strictly as
  a standalone-repo name). (L4 + L7.)
- **Three-marker root-only discovery**: `.niwa/workspace.toml` (rank 1),
  root `workspace.toml` (rank 2, backwards-compat for existing
  standalone-dot-niwa repos), root `niwa.toml` (rank 3). Hard-error on
  ambiguity. Explicit slug subpath bypasses discovery entirely. (L4.)
- **GitHub REST tarball + selective `tar` extraction is the primary
  fetch mechanism**, with a 40-byte SHA-endpoint drift check and
  conditional-GET ETag on the tarball. Git-clone fallback for
  non-GitHub hosts. (L2.)
- **Subpath is first-class in the registry schema.** `RegistryEntry`
  gains parsed mirror fields (`source_host`, `source_owner`,
  `source_repo`, `source_subpath`, `source_ref`); `source_url` stays
  canonical opaque slug. Old entries with no subpath continue to mean
  "whole repo, default branch." (L6 + L7.)
- **State schema bumps to v3** with new `config_source` block
  recording `(url, host, owner, repo, subpath, ref, resolved_commit,
  fetched_at)`. Lazy migration on next save. (L6.)
- **`vault_scope` keyed on workspace name unchanged.** No semantic
  change from subpath sourcing. (L6.)
- **No `.niwaroot`-style indirection inside the source repo.**
  Discovery probes fixed marker names at the repo root only;
  registry-time caching obviates chezmoi-style flexibility. (L4 over L5.)
- **Defer multi-workspace shared cache to v1.1 follow-up.** v1 ships
  snapshots directly at `<workspace>/.niwa/`; the content-addressed
  cache layer can land later. (L3 Option A first, Option C deferred.)
- **Skip telemetry source-redaction design now.** No telemetry pipeline
  exists; document the principle but don't build. (L6.)

The following are explicitly OPEN (research lead 1-5 above) and should
be resolved by the PRD itself:

- Slug delimiter: `:subpath@ref` (L4, L6) vs `//subpath?ref=` (L5).
- v1 host coverage commitment.
- Migration cutover ergonomics (existing flow vs guided command).
- Default-branch ref resolution timing.
- `niwa.toml` `content_dir` requirement.
- `vault_scope = "@source"` shorthand.

The following are downstream design-doc questions, not PRD questions
(the PRD should leave them open for the design phase):

- Provenance/lock file shape (sidecar TOML vs `.niwa/lock.json` vs
  state-only) — implementation detail.
- Snapshot location at v1: direct vs symlink-to-cache — already
  decided "direct" by the v1.1 deferral above.
- `instance.json` placement — implementation detail of the snapshot
  refresh mechanism.
