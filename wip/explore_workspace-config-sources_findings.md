# Exploration Findings: workspace-config-sources

## Core Question

What's the right pattern for sourcing git-hosted workspace configuration into
a niwa workspace? Today niwa assumes "the whole repo at the URL is the
config" and clones it as a working tree at `<workspace>/.niwa/`. That model
fails on remote rewrites (issue #72) and forces a separate `dot-niwa` repo
even when the natural home is a subdirectory inside an existing "brain" repo.
The exploration aims for a unified pattern where (a) "whole repo" is the
degenerate `subpath = "/"` case of subpath sourcing, (b) materialization is
a disposable snapshot pulling only what's needed, and (c) the source
location is convention-discovered where possible.

## Round 1

### Key Insights

1. **The apply pipeline never calls git against the config directory itself.**
   (L1, L3) Every read of `configDir` is plain `os.ReadFile` / `os.ReadDir`;
   the only `git` invocations are `SyncConfigDir`'s `git pull --ff-only` and
   the `git status` dirty-check that gates it — both pure refresh-machinery.
   This makes the migration from working tree to disposable snapshot
   *substantially* smaller than it sounds. Materializers, validators,
   content readers, hook discovery, and env file discovery all keep working
   unchanged.

2. **There are three clone sites, not two.** (L1) Issue #72 names the team
   config and the personal overlay; the workspace overlay
   (`<configRepo>-overlay` convention, cloned at
   `~/.config/niwa/overlays/<dirname>/`) is a third with the same `git pull
   --ff-only` semantics and the same wedge mode. The redesign must address
   all three uniformly.

3. **Two non-obvious load-bearing dependencies on `.git/` presence.** (L1)
   `niwa reset` uses `<configDir>/.git` existence as the proxy for "did this
   config come from a remote" (`isClonedConfig` at `reset.go:131`); the
   plaintext-secrets guardrail uses `git -C <configDir> remote -v` to
   enumerate remotes (`guardrail/githubpublic.go:75`). Removing `.git/` from
   the snapshot silently disables `niwa reset` and the public-repo
   guardrail. **A non-git source-identity marker is mandatory**, not
   optional, in the snapshot model.

4. **GitHub REST tarball + selective `tar` extraction is the right fetch
   mechanism for the GitHub case.** (L2) Wins on disk (exactly S, no `.git/`),
   privacy (stream-extract keeps non-subpath bytes off disk), cognitive
   (no `git status` to confuse anyone), and update-detection (40-byte SHA
   endpoint with `max-age=60`, plus 304-conditional ETag on the tarball).
   Loses on first-fetch bandwidth (R compressed) but config subpaths are
   small in absolute terms so this is a sub-second cost. Fall back to
   "temp-dir clone + copy + delete" for non-GitHub hosts.

5. **Sparse-checkout and partial clone are disqualified.** (L2) Cone-mode
   sparse-checkout always materializes top-level files of the source repo —
   hardcoded in cone semantics — so the user always sees brain-repo
   `README.md`/`LICENSE` next to their niwa config. Partial clones leak
   filenames via `git ls-tree`, leave a working `.git/` that invites edits,
   and silently fetch lazily on operations like `git checkout`. None of
   these match "exactly S, nothing else, no temptation."

6. **Disposable snapshot shape: pure file tree at `<workspace>/.niwa/` with
   a `.niwa-snapshot.toml` provenance sidecar, refreshed via
   atomic-sibling-rename.** (L3) Apply already only requires a directory of
   plain files. No `.git/` means `git status` literally fails inside the
   dir, removing the cognitive trap. The provenance sidecar (commit_oid,
   ref, fetched_at, mechanism) carries the source-identity that L1 said
   the snapshot model needs.

7. **Three-marker root-only discovery is the right convention.** (L4)
   `.niwa/workspace.toml` (rank 1) > root `workspace.toml` (rank 2) > root
   `niwa.toml` (rank 3). Hard-error on ambiguity (don't pick one
   silently). Explicit slug subpath bypasses discovery entirely.
   Backwards-compatible with existing standalone `org/dot-niwa` repos
   (their root `workspace.toml` matches rank 2). Renovate's
   first-match-at-root model is the closest precedent; GitHub Actions's
   `.github/workflows/` rigidity is the cautionary tale (no extension
   path → 7+ years of community frustration).

8. **`//` is the de-facto ecosystem subpath delimiter.** (L5) Terraform
   (`git::https://...//subdir?ref=v1`), go-getter, Renovate
   (`github>org/repo//path/to/file.json`), and Cargo paths converge on the
   double-slash convention; Nix is the principled outlier with `?dir=`.
   `//` reads naturally as "everything after this is *inside* the
   previously-named package."

9. **Lock file pattern is universal across serious peer tools.** (L5)
   `flake.lock`, `Cargo.lock`, `*-lock.{json,yaml}`, `Chart.lock`,
   `crossplane.lock` all share the same shape: input-spec → resolved sha
   + content hash + (sometimes) timestamp. niwa needs the equivalent for
   reproducibility across teammates ("track main" otherwise resolves
   differently per machine).

10. **Both example brain repos (`tsukumogami/vision`,
    `codespar/codespar-web`) converge on `.niwa/` at root.** (L7)
    `niwa.toml`-at-root is rejected for both — root is already crowded and
    a single TOML file can't host the content tree, env file, hooks, or
    extensions niwa materializes. The migration risk lives in step 4 of
    the cutover (existing `<workspace>/.niwa/` working tree → snapshot):
    silent loss of uncommitted edits unless niwa actively detects the URL
    change and refuses without a flag.

11. **The standalone dot-niwa is small (636K including .git, 20 files).**
    (L7) The case for keeping the standalone repo alive on size grounds
    is essentially zero. The thematic overlap the user flagged is real,
    especially for vision (its `org/`, `projects/` already describe
    inter-repo structure that dot-niwa's `claude/workspace.md` duplicates).

12. **State schema needs a v3 bump for `config_source`.** (L6) Today
    `InstanceState` records `OverlayURL` + `OverlayCommit` for the
    workspace overlay but nothing about the team config source. A new
    `config_source` block carrying `(url, host, owner, repo, subpath,
    ref, resolved_commit, fetched_at)` generalizes the existing overlay
    pattern. Lazy migration on next save handles v2 → v3 transparently.

13. **`vault_scope` keyed on workspace name needs no change.** (L6)
    Subpath sourcing doesn't change the personal overlay's
    `[workspaces.<scope>]` resolution model. Defaulting `vault_scope`
    off the brain-repo name would cause collisions when one brain
    serves multiple workspace subpaths.

### Tensions

1. **Slug delimiter: `:` (L4, L6) vs `//` (L5).**
   - `:` argument: shorter, matches Renovate's preset syntax, parses
     trivially, fits the "feels like a slug" niwa house style today.
   - `//` argument: dominant ecosystem convention, survives inside real
     URLs (`git::https://host/repo.git//subdir?ref=v1` works because `//`
     can't appear in a normal URL path), unambiguous when subpath itself
     contains `/`.
   - Trade-off: ecosystem familiarity (`//`) vs niwa shorthand ergonomics
     (`:`). `:` collides with SSH URL syntax (`git@github.com:org/repo.git`)
     but slugs and URLs occupy disjoint syntactic niches in the registry,
     so the conflict never materializes inside `source_url`.
   - **PRD/design decision needed.** Both are defensible; pick one.

2. **Provenance / lock file location: sidecar vs lock file vs state block.**
   - L3 puts it in `.niwa/.niwa-snapshot.toml` (snapshot-self-describing,
     visible without consulting state).
   - L5 puts it in `.niwa/lock.json` (flake.lock-shaped, peer-tool
     pattern, separates "what's pinned" from "what's snapshotted").
   - L6 puts it in `instance.json` `state.config_source` block (single
     source of truth, no extra file).
   - These aren't fully exclusive — a sidecar inside the snapshot dir
     plus state-recorded resolved_commit could coexist (sidecar for
     debuggability, state for drift detection without cracking the
     snapshot). The lock file is a different concept (cross-machine
     reproducibility commitment) that overlaps with provenance only at
     the resolved-commit field.
   - **PRD/design decision needed**: how many files, where they live,
     which is canonical.

3. **Snapshot placement: `<workspace>/.niwa/` directly vs symlink to
   content-addressed cache.**
   - L3 Option A: snapshot lives directly at `<workspace>/.niwa/`. Lowest
     migration cost (path stays the same), zero infrastructure, fixes #72
     immediately. No multi-workspace dedup.
   - L3 Option C, L5, L6 implied: snapshot lives in `~/.cache/niwa/sources/<host>/<owner>/<repo>/<commit-oid>/`,
     `<workspace>/.niwa/` is a symlink. Multi-workspace dedup, rollback
     capability via cache history. Adds cache-GC story, complicates
     init/apply path.
   - L3's recommendation: ship A first (small blast radius, fixes #72),
     layer C underneath later. L5's recommendation: jump to C. L6's
     collision-handling assumes C.
   - **PRD/design decision needed**: incremental (A then C) or jump
     (C from day 1)?

4. **Discovery indirection: in-repo `.niwaroot`-style file or not?**
   - L4 explicitly rejects (discovery is one-time, cached in registry;
     stable convention beats indirection).
   - L5 raises it as worth borrowing from chezmoi (lets brain-repo
     owners reorganize without breaking external slugs).
   - L4's argument is stronger: the cost of `.niwaroot` is that two
     source-of-truth files must stay in sync, and niwa's discovery is
     cached at `niwa init` time anyway, so the flexibility chezmoi needs
     (multi-machine state) doesn't apply.
   - **Implicit decision**: skip `.niwaroot`. Discovery probes fixed
     marker names at root only. Note this in the artifact.

### Gaps

1. **Default-branch resolution timing** (L6 open question). Ref-less
   slugs: pin resolved default branch at `niwa init` (registry shows
   stable identity, but missed remote-default-rename; user has to
   explicitly opt back into HEAD-tracking) or re-resolve every `niwa
   apply` (matches today's behavior, but `niwa status` shows a moving
   target). PRD-shaped question: which UX is the contract?

2. **`instance.json` placement** (L3 open question). Once `.niwa/` is a
   fully-disposable directory the refresh path may rename out from
   under it, where does the per-instance state file live? Sibling file
   (`<workspace>/.niwa-state.json`), sibling subdir excluded from the
   swap (`<workspace>/.niwa/.state/`), or `$XDG_STATE_HOME/niwa/`
   keyed by workspace path? Affects how `init`, `apply`, and
   `DiscoverInstance` interact.

3. **Migration cutover ergonomics** (L7 open question). Same-as-#72
   ("git pull --ff-only fails, user reconciles manually") or dedicated
   `niwa registry migrate` command that detects "old source was
   whole-repo, new source is subpath in same org" and offers a guided
   flow? L7 worries that the URL-change cutover is the riskiest moment
   in the whole adoption story.

4. **`niwa.toml` `content_dir` requirement** (L4 open question). When
   the one-file-at-root convention resolves to repo root, should
   `content_dir` become *required* (forces the brain-repo author to
   declare which subdir is content, prevents niwa from accidentally
   reading random brain-repo files) or optional with a sensible
   default? L4 leans toward required.

5. **Multi-host adapter scope** (L2 open question). Ship GitLab /
   Bitbucket / Gitea archive adapters at v1, or only GitHub-tarball +
   git-clone-fallback and let demand drive future adapters?

6. **Read-only snapshot enforcement** (L3 open question). `chmod -R a-w`
   the snapshot dir to make "do not edit" enforced rather than
   conventional? Stronger guard, but breaks any tooling that wants to
   `cd .niwa && grep` modifying access times. Probably skip for v1.

7. **Repo housekeeping (`README.md`, `LICENSE`, `.gitignore`)** in the
   standalone dot-niwa doesn't migrate cleanly (L7). Document the
   migration story for these files; not a blocker.

8. **Two `CLAUDE.md` files in one repo** (L7). Brain repo's own
   `CLAUDE.md` (for cwd-brain-repo Claude Code) and `.niwa/claude/workspace.md`
   (woven into workspace overlay) are different audiences. Editorial
   discipline + docs needed; not a code concern.

### Decisions (this round)

1. **Snapshot model is the direction.** Converges across L1, L2, L3, L5,
   L6, L7. No serious alternative surfaced.

2. **`.niwa/` at brain-repo root is the placement convention.** L4 + L7
   agree; both example brain repos converge here. Reject `niwa.toml`-only
   for non-toy configs, reject `dot-niwa/`-as-dirname (reserve `dot-niwa`
   as standalone-repo name).

3. **Three-marker root-only discovery.** L4 recommendation:
   `.niwa/workspace.toml` > root `workspace.toml` > root `niwa.toml`,
   hard-error on ambiguity, explicit slug subpath bypasses.

4. **GitHub REST tarball + `tar` extraction is the primary fetch
   mechanism**, with git-clone fallback for non-GitHub hosts. L2's
   recommendation is decisive on every cost axis except first-fetch
   bandwidth, which is acceptable for config-sized payloads.

5. **Subpath is first-class in the registry schema.** L6 + L7 agree.
   `RegistryEntry` gains a parsed mirror including subpath; `source_url`
   stays canonical opaque slug.

6. **State schema bump to v3** with new `config_source` block. L6
   recommendation. Lazy migration on next save.

7. **`vault_scope` keyed on workspace name unchanged.** L6 explicit.

8. **No `.niwaroot`-style indirection.** L4 reasoning supersedes L5's
   suggestion (caching at registry time obviates the need).

9. **Defer multi-workspace shared cache to a follow-up if needed.** L3's
   recommendation: ship Option A first (snapshot at `<workspace>/.niwa/`
   directly), layer Option C (content-addressed cache + symlink) later
   when multi-workspace dedup demand emerges.

10. **Skip telemetry-source design now.** L6 confirms no telemetry
    pipeline exists today. Forward-looking redaction model is
    documented but not built.

### User Focus

(Not applicable — `--auto` mode skips the user-narrowing question. The
synthesis above plus the decisions list captures what would have come
out of that conversation.)

## Accumulated Understanding

The redesign has a clear shape after one round, with three categories of
remaining work:

**Settled by research, no further exploration needed:**
- Snapshot-not-working-tree model (L1 hidden gotchas + L3 shape recommendation
  + L7 migration analysis converge).
- GitHub tarball + selective tar extraction as the fetch primary, git-clone
  fallback for non-GitHub.
- `.niwa/` at brain-repo root as the placement, three-marker root-only
  discovery (`.niwa/workspace.toml` > root `workspace.toml` > root
  `niwa.toml`), hard-error on ambiguity.
- Subpath as first-class registry field; state schema v3 with
  `config_source` block; lazy migration.
- `vault_scope` semantics unchanged.
- No `.niwaroot` indirection.
- Drop `--allow-dirty` (becomes meaningless with no working tree).
- Replace `isClonedConfig` and `CheckGitHubPublicRemoteSecrets` with
  source-identity marker reads (sidecar or state, depending on tension #2).

**Decision-shaped questions for the artifact:**
- Slug delimiter: `:` vs `//` (tension #1)
- Provenance + lock file shape: sidecar TOML vs `.niwa/lock.json` vs
  state-only (tension #2)
- Snapshot location: direct at `<workspace>/.niwa/` (Option A) vs
  symlink-to-cache (Option C) at v1 (tension #3)
- Default-branch ref resolution timing (gap #1)
- `instance.json` placement (gap #2)
- Migration cutover ergonomics: lean on existing flow vs dedicated
  `niwa registry migrate` (gap #3)
- `niwa.toml` `content_dir` requirement (gap #4)
- Multi-host adapter scope at v1 (gap #5)

**Editorial / documentation work, not blocking:**
- Two `CLAUDE.md` per brain repo (gap #8)
- Repo housekeeping migration (gap #7)
- Discovery debugging UX (e.g., `niwa config show-discovery org/repo`) —
  L4 flagged for design phase

The remaining open questions are decision-shaped, not exploration-shaped.
They would not benefit from another discover-converge round; they need a
PRD that frames trade-offs and forces a pick.

## Decision: Crystallize
