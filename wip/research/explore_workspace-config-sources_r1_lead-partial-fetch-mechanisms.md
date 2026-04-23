# Lead: Which git mechanisms can fetch only a subdirectory of a repo without bringing the rest, and what are their trade-offs?

## Findings

### 1. Full clone + post-hoc subdirectory copy (today's niwa baseline)

Clones the entire repo and either keeps it whole or copies the subpath out. Useful only as a control case.

- **Disk footprint:** R + S (everything + the copy). For `cli/cli` shallow clone: 40 MB on disk for a 1.5 MB target subpath — a 26x penalty.
- **Privacy / bleed:** every blob, tree, and commit lands locally. `.git/` exposes the whole history.
- **Bandwidth (first):** R bytes, plus shallow-clone savings (~half of full history). For `cli/cli` `--depth=1`: ~40 MB.
- **Bandwidth (update):** `git fetch` can be incremental, but the first clone cost dominates.
- **Cognitive temptation:** maximum. Looks exactly like a normal repo. `git status`, `git log`, branch switching all work. Users will commit into it and be confused when their changes vanish.
- **Auth:** standard git credential helpers (HTTPS + PAT, SSH key). Best ergonomics.
- **Server requirements:** none beyond a working git remote. Works against any host (GitHub, GitLab, Bitbucket, Gitea, plain dumb-HTTP server, local file://).
- **Failure modes:** force-push survivable (re-fetches). Network blip retried by git. Resilient.
- **Update detection:** `git ls-remote <url> <ref>` returns the head oid in one round-trip (~150 ms, ~50 bytes). Cheapest possible "did it change" check across all mechanisms studied.

### 2. Shallow clone (`--depth=1`)

A `--depth=1` clone fetches one commit's worth of history but the entire tree at that commit. Already part of (1) above; everything else listed under "full clone" applies. Disk and bandwidth are R, not S.

### 3. Sparse-checkout, no partial filter (`git clone` then `git sparse-checkout set <subdir>`)

Server still streams the complete pack (all blobs reachable from HEAD) because no filter was passed. The working tree is then constrained to the chosen cone, but `.git/objects/` holds everything.

- **Disk footprint:** ~R in `.git/`, ~S in working tree. Worst-case combination: high.
- **Privacy:** same as full clone — every blob is on disk.
- **Bandwidth (first):** R.
- **Bandwidth (update):** standard git fetch (delta).
- **Cognitive:** moderate. `git status` works, root files are present; novice users will think the directories they don't see are "missing" and re-add them.
- **Auth / server / failure:** identical to (1).
- **Update detection:** `git ls-remote` (cheap).

This mechanism saves nothing on the wire. Only useful when you've already decided to clone everything and just want a tidier working tree.

### 4. Partial clone, blobless (`--filter=blob:none --sparse`) + sparse-checkout cone

Server sends commits + all trees, omits blobs not reachable from the sparse cone. On checkout, git lazily fetches the blobs needed for the cone via a "promisor remote."

Measured on `cli/cli` with target `docs/`:
- Disk: 3.1 MB total (working tree includes 11 root files git always materializes in cone mode plus `docs/`; `.git/objects` holds all 1226 trees plus 71 blobs for the included files).
- Trees in `.git/objects`: **1226** (the full directory structure of the repo).
- Blobs in `.git/objects`: **71** (only what the cone needs).

Notes:
- **Disk footprint:** roughly tree-metadata-of-R + S. For `cli/cli` (1226 tree objects), the metadata cost is small in absolute terms (~MB), but for huge monorepos (Chromium-class with millions of paths) it can be hundreds of MB.
- **Privacy / bleed:** **every directory and file path in the repo is locally visible** via `git ls-tree -r HEAD`. Filenames leak even when contents don't. Blob *contents* outside the cone are not on disk until you ask for them — but a `git checkout`, `git grep`, or `git log -p` outside the cone will silently re-fetch them from the promisor remote.
- **Bandwidth (first):** all commits + all trees + cone blobs. For `cli/cli` clone+checkout: ~7-10 MB over the wire (vs 40 MB full).
- **Bandwidth (update):** `git fetch` pulls new commits + trees + cone blobs only. Subsequent checks: `git ls-remote` (40 bytes).
- **Cognitive:** **dangerous**. The artifact is a real git repo. `git status` works, `git log` works, `git checkout <other-branch>` works (and triggers a hidden network call). User can `git add`, `git commit`, even `git push` — and have changes silently lost when niwa next re-materializes.
- **Auth:** standard git credentials. Promisor remote inherits them.
- **Server requirements:** GitHub.com (yes), GitHub Enterprise 2.22+ (yes), GitLab (yes), Bitbucket Cloud (yes since rollout), Bitbucket Data Center 7.13+ with git 2.18+, Gitea (yes by default since 1.x, can be disabled), Gerrit 3.1+. Plain dumb-HTTP servers: **no**. Self-hosted "throw a bare repo behind nginx" setups frequently lack it.
- **Failure modes:** if the promisor remote disappears (network outage, repo renamed, force-push that orphans the commit), local operations that touch un-fetched objects fail with cryptic "missing object" errors. Cone-mode root-file inclusion is non-negotiable: top-level files always materialize.
- **Update detection:** `git ls-remote <url> <ref>` → 40 bytes per check.

### 5. Partial clone, treeless (`--filter=tree:0 --sparse`) + sparse-checkout cone

Same as (4), but the server also omits trees not reachable from the cone. On `cli/cli`/`docs/` we measured 388 trees and 71 blobs — significantly less metadata than the blobless filter.

- **Disk footprint:** smaller than (4). Measured 3.1 MB total, with ~388 trees vs 1226.
- **Privacy / bleed:** fewer leaked filenames — only trees on the path to the cone are local. Sibling-directory listings can still be obtained (a `git ls-tree origin/main:internal` triggers a fetch). Without an active fetch, much less is on disk than (4).
- **Bandwidth (first):** smaller pack than (4). Roughly proportional to (depth-of-cone × siblings-per-level + cone size). For `cli/cli`/`docs/`: ~3-5 MB.
- **Bandwidth (update):** `git fetch` is cheap, but **GitHub Engineering explicitly discourages tree:0 for developer use**: any `git log -- <path>`, `git blame`, or branch switch can trigger a tree fetch per historical commit. For a snapshot use case (no history operations), this caveat doesn't apply.
- **Cognitive:** same trap as (4). Looks like a real repo, accepts edits, silently loses them.
- **Auth / server requirements:** same as (4) — server must support partial-clone protocol v2 and the `tree:0` filter specifically (git 2.20+ server-side).
- **Failure modes:** worse than (4) if the user does anything historical. Same promisor-remote dependency.
- **Update detection:** `git ls-remote` (cheap).

### 6. `git archive --remote`

Fetches a tar/zip of a ref, optionally limited to subpaths, in one round-trip with no `.git/` artifacts.

- **GitHub support:** **disabled.** GitHub does not run `git-upload-archive`. Confirmed unchanged through 2026; the long-standing issue is still open. (`http.uploadarchive` config in git 2.44+ enables HTTP transport, but the *server* must opt in.)
- **GitLab/Bitbucket Cloud:** also not enabled by default.
- **Self-hosted git:** can be enabled. Not portable.
- **Disk / bandwidth:** ideal — server filters subpath, sends ~S bytes, no `.git/`.
- **Cognitive:** zero — it's just a tarball.
- **Verdict:** unusable for the dominant target (GitHub).

### 7. GitHub REST tarball endpoint (`/repos/{owner}/{repo}/tarball/{ref}`) + selective tar extraction

API redirects (302) to `codeload.github.com/<owner>/<repo>/legacy.tar.gz/<ref>`. Standard `tar -xzf full.tar.gz <root>/<subpath>` extracts only the chosen subpath.

Measured on `cli/cli`/`docs/`:
- Wire transfer: 14 MB gzip tarball (full repo at HEAD; no way to ask the server for a subpath).
- Disk after extraction: **1.5 MB** for `docs/` only, **64 files**, no `.git/`, nothing outside `docs/`.
- Time: ~2 s on a fast link.

Properties:
- **Disk footprint:** **exactly S.** No `.git/`, no metadata, no extra files. Best of all options.
- **Privacy:** the tarball is in transit and can be streamed-extracted (`curl ... | tar -xz --wildcards '<root>/<subpath>/*'`) so nothing outside the subpath ever touches disk. Filenames outside the subpath transit the wire (encrypted to the host) but never persist.
- **Bandwidth (first):** R compressed. Worst-case axis. For tiny config-shaped subpaths inside huge repos this is the dominant cost.
- **Bandwidth (update):** **excellent.** `codeload.github.com` supports `If-None-Match: <etag>` and returns 304 with empty body when unchanged (verified by experiment). The ETag is stable per (ref, tree) — when the ref's tree oid hasn't changed, the tarball doesn't change either. Even cheaper: `GET /repos/{owner}/{repo}/commits/{ref}` with `Accept: application/vnd.github.sha` returns the 40-byte commit oid, has `cache-control: max-age=60`, and supports its own ETag.
- **Cognitive:** **zero.** It's a directory of files. No `.git/`, `git status` returns "not a git repo." Users have no temptation to edit-and-commit, because there's no commit target.
- **Auth:** standard `Authorization: Bearer <token>` or `token <PAT>` header. For private repos, classic PAT needs `repo` scope; fine-grained PAT needs `Contents: read`. The 302 redirect points to a temporary signed URL on `codeload.github.com` that **expires after 5 minutes** for private repos — clients must follow redirects within that window. Public-repo tarballs are cacheable indefinitely.
- **Server requirements:** GitHub-only API. GitLab has an equivalent (`/projects/:id/repository/archive`), Bitbucket has its own, and Gitea has `/repos/{owner}/{repo}/archive/{archive}`. Each is a separate adapter.
- **Failure modes:** API rate limit (5000/hr authenticated, 60/hr unauthenticated). Force-push on the ref re-points `<ref>` to the new commit; tarball follows. Repo rename: a 301 redirect on the API side handles it transparently. **Max tarball size:** GitHub doesn't publish a hard cap on tarball endpoint specifically, but repo size limits (5 GB recommended) bound it.
- **Update detection:** the SHA endpoint (40 bytes, max-age=60) is the cheapest practical "did it change" check. ETag-conditional GET on the tarball is the next cheapest and falls back to download-only-if-changed.

### 8. GitHub REST contents endpoint (`/repos/{owner}/{repo}/contents/{path}`)

Returns a JSON listing of a directory or, when `Accept: application/vnd.github.raw` is used and the path is a file, the raw bytes.

- **Disk footprint:** exactly S.
- **Privacy:** ideal — only the requested paths transit and persist. Nothing else is even named.
- **Bandwidth (first):** S, but at the cost of **one API call per directory + one per file**. For `cli/cli`/`docs/` (64 files in a few subdirs), that's ~70-80 sequential or batched API calls. For deeper trees, it's worse.
- **Bandwidth (update):** each file response carries an ETag; conditional GETs return 304. But you still need at least one directory-listing call per dir to detect added/removed files.
- **Cognitive:** zero — plain files.
- **Auth:** same as (7). PAT with `repo` (classic) or `Contents: read` (fine-grained) for private repos.
- **Server requirements:** GitHub-only. **Hard limit:** directory listings cap at 1000 entries; files >1 MB need `Accept: application/vnd.github.raw` and files >100 MB are unsupported (must use the git data API or raw.githubusercontent).
- **Failure modes:** rate limit is the big one. A subpath of 500 files = 500+ API calls = 1/10 of an authenticated user's hourly budget for one apply. Concurrent niwa users on one PAT will exhaust it.
- **Update detection:** per-file ETag, but no cheap "did the whole tree change" call. Have to walk it.

### 9. GitHub GraphQL `tree` queries

Query a `Repository.object(expression: "<ref>:<path>")` cast to a `Tree`, recurse over `entries`. With aliasing you can fetch many blobs in one HTTP call.

- **Disk footprint:** S.
- **Privacy:** ideal.
- **Bandwidth (first):** good — a single GraphQL request can return a directory tree several levels deep, and aliased blob fetches batch many files into one round-trip. Maximum response size is 50 MB.
- **Bandwidth (update):** GraphQL doesn't support ETags on arbitrary queries; you'd query the ref's commit oid via GraphQL (1 call) and skip if unchanged. Equivalent in cost to the SHA endpoint but in one composite call.
- **Cognitive:** zero — you write the files yourself.
- **Auth:** PAT with `repo` (classic) or `Contents: read` (fine-grained).
- **Server requirements:** GitHub-only. **No native recursion** — depth is hardcoded in the query, so you must either know the max depth (brittle) or fall back to multi-round trips.
- **Failure modes:** complex queries can hit GraphQL node-budget limits (500k node points per request). Requires writing and maintaining one query per max-depth tier.
- **Update detection:** query commit oid as part of the same call.

### 10. `git clone --depth=1 --filter=blob:none --no-checkout --sparse` written via `git fetch` into a manually-init'd repo

A workaround pattern: `git init`, configure a promisor remote, set sparse-checkout *before* the first checkout, then `git fetch --depth=1 --filter=tree:0 origin <ref>` and `git checkout FETCH_HEAD`. Verified that this still produces the cone-mode root-file leak (the 11 top-level files of `cli/cli` materialized in our experiment). Cone mode mandates root files; only non-cone (gitignore-pattern) sparse-checkout can suppress them, and non-cone is significantly slower and discouraged by upstream git for new code.

So even with maximum care, partial-clone-based approaches cannot match tarball extraction's "exactly S, nothing else" disk profile.

## Comparison Matrix

| Mechanism | Disk (S=subpath, R=repo) | Privacy bleed | BW first | BW update | Cognitive temptation | Auth ergonomics | GitHub-only? | Update-check cost |
|---|---|---|---|---|---|---|---|---|
| Full clone | R + S | full | R | delta | very high (full git repo) | git creds | no | `git ls-remote` 40 B |
| Shallow clone (`--depth=1`) | R + S | full | R (one commit) | delta | very high | git creds | no | `git ls-remote` 40 B |
| Sparse, no filter | ~R (.git) + S | full (all blobs in .git) | R | delta | high (looks like git repo) | git creds | no | `git ls-remote` 40 B |
| Partial blobless + sparse | tree-metadata-of-R + S | medium (all paths leak) | trees + cone blobs | trees + delta | high (looks like git repo, lazy fetch surprises) | git creds | needs server protocol v2 + filter | `git ls-remote` 40 B |
| Partial treeless + sparse | small + S | low (only on-path trees) | small | small but history ops trigger fetches | high | git creds | needs server protocol v2 + tree:0 | `git ls-remote` 40 B |
| `git archive --remote` | S | none | S | full re-fetch (no caching protocol) | none | git creds | self-hosted only — GitHub disables | full re-fetch |
| GitHub tarball + selective tar | S | none on disk; full on wire | R (gzip) | 304 via ETag | none | PAT (repo / Contents:read) | yes (per-host adapter) | 40-byte SHA endpoint or tarball ETag |
| GitHub contents API | S | none | S (many round-trips) | per-file ETag, dir-walk needed | none | PAT (repo / Contents:read) | yes | per-file/per-dir ETag |
| GitHub GraphQL trees | S | none | S | one composite query | none | PAT (repo / Contents:read) | yes | commit oid via same query |

## Recommendation

**Primary: GitHub REST tarball endpoint + selective `tar` extraction**, with a 40-byte SHA-endpoint pre-check for drift detection.

**Fallback: full git clone into a temp dir + copy subpath out + delete temp dir** for any non-GitHub host or when the API is rate-limited.

Justification against the four costs and private-repo support: tarball gives the only "exactly S, no `.git/`, nothing else" disk profile (perfect on the disk and cognitive axes — there is no working `git status` to confuse anyone), the strongest privacy guarantee on disk (nothing outside the subpath persists), the cheapest possible drift check (a 40-byte HTTP response with `max-age=60`, plus 304-conditional-GET on the tarball itself), and standard GitHub PAT auth (`repo` scope on classic PATs, `Contents: read` on fine-grained PATs — the same scopes a user already configures for private-repo cloning). The one weakness is bandwidth on first fetch (R, not S), which for niwa's expected payload size (config files measured in KB to low MB) is well under a second on any modern link and dwarfed by the wall-clock cost of the user noticing apply succeeded. The git-clone fallback covers GitLab, Bitbucket, Gitea, and self-hosted, where the same auth credentials a user has already configured for `git clone` continue to work; we accept its worse disk and cognitive profile in exchange for universal coverage.

## Implications

Choosing tarball-primary forces the niwa design in several specific directions:

1. **Per-host adapter layer.** GitHub's `/tarball/{ref}` is one entry; GitLab's `/projects/:id/repository/archive`, Bitbucket's `/2.0/repositories/{ws}/{repo}/src/{ref}/?format=tarball`, and Gitea's `/repos/{owner}/{repo}/archive/{archive}` are each separate. niwa needs a host-recognizer that maps a config URL to the right archive endpoint, and a credential resolver that knows each host's PAT conventions.
2. **Mandatory drift-check phase.** Every `niwa apply` should first call the SHA endpoint (or `git ls-remote` for the fallback path) and skip the download entirely on no-change. Without this, every apply costs R bytes.
3. **Stream-extract, never write the tarball.** `curl ... | tar -xz --wildcards 'PREFIX/<subpath>/*' --strip-components=1` keeps the privacy story honest: bytes outside the subpath never touch disk, even temporarily.
4. **Snapshot directory must look unmistakably ephemeral.** No `.git/` is the right default behavior here, but it should also live under a name that signals "do not edit" — `.niwa/snapshot/` or `.niwa/.cache/` rather than `.niwa/`. Optionally write a `.niwa-snapshot-of` marker file recording (source, ref, oid, fetched-at) for debuggability.
5. **Subpath = "/" is the degenerate case** — same code path, just a tar root with no `--wildcards` filter. The unified subpath model works.
6. **Git fallback path needs careful temp-dir handling** to maintain the privacy story: clone to `$TMPDIR/<random>`, copy the subpath out, `rm -rf` the temp dir before the apply finishes. Failure-mode handling: if niwa is killed mid-apply, leave a `.niwa/.cache.partial` marker so the next apply can clean up.
7. **Auth UX:** "use the same PAT you use for `gh`" is achievable — `GH_TOKEN` env, `~/.config/gh/hosts.yml` parsing, or `gh auth token` shell-out are all reasonable. Document that the token needs `Contents: read` on fine-grained or `repo` on classic.
8. **Rate-limit awareness:** authenticated PATs get 5000 req/hr. Each apply is 1 SHA call + at most 1 tarball download = 2 calls. Even at one apply per minute, niwa stays well under budget.

## Surprises

- **Cone-mode sparse-checkout always materializes top-level files.** This is hardcoded in cone semantics. Even with a perfect partial+sparse setup targeting `docs/`, you get the repo's root `README.md`, `LICENSE`, etc. on disk. The only way to suppress this is non-cone mode, which is slower and upstream-discouraged. This kills sparse-checkout as a "looks like just my subpath" mechanism.
- **`tree:0` partial clone has way less `.git/` metadata than `blob:none`** (388 vs 1226 trees on `cli/cli`/`docs/`), but GitHub Engineering explicitly tells developers not to use it because history operations become catastrophically expensive. For a snapshot use case, this caveat doesn't apply, making `tree:0` actually *better* than `blob:none` on every axis we care about — the opposite of the conventional ranking.
- **`codeload.github.com` (the actual download host the tarball API redirects to) supports HTTP `If-None-Match` and returns 304 for unchanged tarballs.** This is undocumented in the GitHub REST docs but verified empirically. It means the tarball endpoint has a real conditional-GET story, not just the rough "re-download every time" reputation.
- **The SHA-only commit endpoint** (`Accept: application/vnd.github.sha` against `/repos/.../commits/{ref}`) is a 40-byte response with `cache-control: max-age=60` and an ETag. This is the cheapest possible "did anything change" check on GitHub — same 40-byte commit oid you'd get from `git ls-remote`, but with proper HTTP caching.
- **`git archive --remote` is still a dead end on GitHub in 2026.** The original 2014-era issue is unresolved and shows no sign of moving. Anyone designing around it for the GitHub case will hit a wall.
- **Private-repo tarball URLs from `codeload.github.com` expire after 5 minutes.** Code that caches the redirect URL for later download will silently break. Always resolve the redirect immediately before downloading.

## Open Questions

- **Multi-host strategy details.** Should niwa ship a GitLab/Bitbucket/Gitea archive adapter at v1, or only GitHub-tarball + git-clone-fallback, and let the fallback handle every non-GitHub case until adapter demand emerges?
- **What happens when the user points niwa at a GitHub Enterprise Server installation?** API path is the same with a different base URL; tarball endpoint exists. But auth-token discovery (`GH_HOST=...`) needs to flow through.
- **Submodules in the source repo.** A tarball does not include submodule contents. If a workspace config sub-tree contains a submodule pointer, the snapshot will have an empty directory. Is this acceptable? (Probably yes — workspace config rarely uses submodules — but should be a documented limitation.)
- **LFS-tracked files in the source.** Tarball returns LFS pointer files, not real bytes. Same disposition needed.
- **Symlinks in the source.** Tarball preserves them; behavior on Windows hosts may be surprising. Document.
- **Should drift-check happen on `niwa apply`, on `niwa status`, both, or be configurable?** Tied to the broader UX question of whether niwa is a pull-on-demand or a pull-on-schedule tool.
- **Cache lifetime.** The SHA endpoint has `max-age=60`. Should niwa respect that and skip the network call entirely if the snapshot is <60 s old? Probably yes, with an `--ignore-cache` flag.

## Summary

The recommended mechanism is the GitHub REST tarball endpoint with a streamed `tar` extraction of the target subpath, gated by a 40-byte SHA-endpoint drift check, and falling back to a temp-dir git clone + copy for non-GitHub hosts. The primary trade-off is bandwidth on first fetch (the whole repo's gzipped bytes transit, even though only the subpath persists on disk), accepted because workspace-config subpaths are small in absolute terms and the disk, privacy, and cognitive-temptation wins are decisive. The biggest open question is how many host adapters niwa ships at v1 versus relying on the git-clone fallback for everything outside GitHub.
